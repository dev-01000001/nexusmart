import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { translatePromotionWarning } from "@/features/commerce/checkoutErrors";
import type { GwCart, GwDiscountBreakdown, GwVoucher } from "@/features/commerce/types";

interface ValidateBody {
  code?: string;
  shipping_fees?: Record<string, number>;
  shop_voucher_ids?: Record<string, string | null | undefined>;
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  flash_sale_item_by_sku?: Record<string, string>;
}

export const POST = apiRoute(
  async (req) => {
    await assertSession();
    const body = await readJson<ValidateBody>(req);
    const rawCode = (body.code ?? "").trim();
    if (!rawCode) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const cleanUpper = rawCode.toUpperCase();

    const [{ vouchers }, { cart }] = await Promise.all([
      gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers"),
      gw<{ cart: GwCart }>("/api/v1/cart"),
    ]);

    const found = (vouchers ?? []).find(
      (v) => v.code.toUpperCase() === cleanUpper || v.id === rawCode
    );
    if (!found) {
      return Response.json(
        {
          valid: false,
          reason: `Không tìm thấy mã ưu đãi ${cleanUpper}. Bạn kiểm tra lại ký tự nhé.`,
        },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    if (!found.is_active) {
      return Response.json(
        { valid: false, voucher: found, reason: `Mã ${found.code} hiện không còn hiệu lực.` },
        { headers: { "Cache-Control": "no-store" } }
      );
    }
    if (found.remaining_quota <= 0) {
      return Response.json(
        { valid: false, voucher: found, reason: `Mã ${found.code} đã hết lượt sử dụng.` },
        { headers: { "Cache-Control": "no-store" } }
      );
    }
    if (found.ends_at && Date.parse(found.ends_at) < Date.now()) {
      return Response.json(
        { valid: false, voucher: found, reason: `Mã ${found.code} đã hết hạn sử dụng.` },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    const groups = cart?.shop_groups ?? [];
    if (groups.length === 0) {
      return Response.json(
        { valid: false, voucher: found, reason: "Giỏ hàng đang trống nên chưa thể áp mã ưu đãi." },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    const scope = found.scope.toUpperCase();
    if (scope === "SHOP" && !groups.some((g) => g.shop_id === found.shop_id)) {
      return Response.json(
        {
          valid: false,
          voucher: found,
          reason: `Giỏ hàng chưa có sản phẩm thuộc gian hàng áp dụng mã ${found.code}.`,
        },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    const shopVoucherIds: Record<string, string> = {};
    for (const [k, v] of Object.entries(body.shop_voucher_ids ?? {})) {
      if (k.trim() && typeof v === "string" && v.trim()) shopVoucherIds[k.trim()] = v.trim();
    }
    let platformVoucherId = (body.platform_voucher_id ?? "").trim();
    let freeshipVoucherId = (body.freeship_voucher_id ?? "").trim();

    if (scope === "SHOP") {
      shopVoucherIds[found.shop_id] = found.id;
    } else if (scope === "FREESHIP") {
      freeshipVoucherId = found.id;
    } else {
      platformVoucherId = found.id;
    }

    const fees = body.shipping_fees ?? {};
    const flashBySku = body.flash_sale_item_by_sku ?? {};
    const shopItems = groups.map((g) => ({
      shop_id: g.shop_id,
      subtotal_vnd: g.subtotal_vnd,
      shipping_fee_vnd: Math.max(0, Math.trunc(Number(fees[g.shop_id] ?? 0) || 0)),
      items: g.items.map((it) => ({
        sku_id: it.sku_id,
        quantity: it.quantity,
        unit_price_vnd: it.price_vnd,
        flash_sale_item_id: flashBySku[it.sku_id] || "",
      })),
    }));

    const { breakdown } = await gw<{ breakdown: GwDiscountBreakdown }>(
      "/api/v1/promotions/preview",
      {
        method: "POST",
        body: {
          shop_items: shopItems,
          shop_voucher_ids: shopVoucherIds,
          platform_voucher_id: platformVoucherId,
          freeship_voucher_id: freeshipVoucherId,
          coins_to_use: Math.max(0, Math.trunc(Number(body.coins_to_use ?? 0) || 0)),
        },
      }
    );

    const warningsVi = (breakdown.warnings ?? []).map(translatePromotionWarning);
    const safeBreakdown: GwDiscountBreakdown = { ...breakdown, warnings: warningsVi };

    let discountVnd = 0;
    if (scope === "SHOP") {
      discountVnd =
        breakdown.shops.find((s) => s.shop_id === found.shop_id)?.shop_voucher_discount_vnd ?? 0;
    } else if (scope === "FREESHIP") {
      discountVnd = breakdown.total_freeship_discount_vnd;
    } else {
      discountVnd = breakdown.total_platform_voucher_discount_vnd;
    }

    // If freeship voucher is valid on order subtotal but shipping_fee_vnd was 0 in cart preview,
    // still accept it unless preview returned a warning for it.
    const hasBlockingWarning =
      warningsVi.length > 0 &&
      discountVnd <= 0 &&
      !(
        scope === "FREESHIP" &&
        breakdown.total_shipping_fee_vnd === 0 &&
        found.min_order_vnd <= breakdown.total_subtotal_vnd
      );

    if (hasBlockingWarning || (discountVnd <= 0 && scope !== "FREESHIP")) {
      const reason =
        warningsVi[0] ||
        (found.min_order_vnd > 0
          ? `Đơn hàng cần tối thiểu ${found.min_order_vnd.toLocaleString("vi-VN")}₫ để dùng mã ${found.code}.`
          : `Mã ${found.code} chưa đủ điều kiện áp dụng cho giỏ hàng hiện tại.`);
      return Response.json(
        { valid: false, voucher: found, scope, shop_id: found.shop_id, discount_vnd: 0, reason },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    return Response.json(
      {
        valid: true,
        voucher: found,
        scope,
        shop_id: found.shop_id || undefined,
        discount_vnd: discountVnd,
        breakdown: safeBreakdown,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng nhập mã ưu đãi cần kiểm tra.",
    },
  }
);
