import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import {
  STOREFRONT_CHANNEL,
  filterPreviewableCartGroups,
  hasPlatformPromoInput,
  isSiteShopVoucher,
  resolveSiteShopVoucherMap,
} from "@/lib/server/storefront";
import {
  extractRejectedFlashSaleItemIds,
  translatePromotionWarning,
} from "@/features/commerce/checkoutErrors";
import type { GwCart, GwDiscountBreakdown, GwVoucher } from "@/features/commerce/types";

interface ValidateBody {
  code?: string;
  shipping_fee_vnd?: number;
  shipping_fees?: Record<string, number>;
  shop_voucher_id?: string | null;
  shop_voucher_ids?: Record<string, string | null | undefined>;
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  flash_sale_item_by_sku?: Record<string, string>;
}

export const POST = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    await assertSession();
    const body = await readJson<ValidateBody>(req);

    if (hasPlatformPromoInput(body)) {
      throw new GatewayError("invalid", "PLATFORM_PROMO_NOT_ALLOWED");
    }
    resolveSiteShopVoucherMap(body, site.shopId);

    const rawCode = (body.code ?? "").trim();
    if (!rawCode) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const cleanUpper = rawCode.toUpperCase();

    const [{ vouchers }, { cart }] = await Promise.all([
      gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
        query: {
          shop_id: site.shopId,
          scope: "SHOP",
          sales_channel: STOREFRONT_CHANNEL,
          storefront_shop_id: site.shopId,
        },
      }),
      gw<{ cart: GwCart }>("/api/v1/cart", { query: { shop_id: site.shopId } }),
    ]);

    const siteVouchers = (vouchers ?? []).filter((v) => isSiteShopVoucher(v, site.shopId));
    const found = siteVouchers.find((v) => v.code.toUpperCase() === cleanUpper || v.id === rawCode);
    if (!found) {
      // Reject platform, freeship, foreign-shop, and nonexistent codes with 404.
      const reason = `Mã ưu đãi ${cleanUpper} không tồn tại hoặc không áp dụng tại cửa hàng này.`;
      return Response.json(
        {
          valid: false,
          reason,
          error: { code: "VOUCHER_NOT_FOUND", message: reason },
        },
        { status: 404, headers: { "Cache-Control": "no-store" } }
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

    const availableGroups = filterPreviewableCartGroups(cart, site.shopId);
    if (availableGroups.length === 0) {
      return Response.json(
        { valid: false, voucher: found, reason: "Giỏ hàng đang trống nên chưa thể áp mã ưu đãi." },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    const rawFee = body.shipping_fee_vnd ?? body.shipping_fees?.[site.shopId] ?? 0;
    const shippingFeeVnd = Math.max(0, Math.trunc(Number(rawFee) || 0));
    const flashBySku = body.flash_sale_item_by_sku ?? {};
    // Do NOT send unit_price_vnd — the gateway resolves authoritative SKU prices from product-service.
    const shopItems = availableGroups.map((g) => ({
      shop_id: site.shopId,
      subtotal_vnd: g.subtotal_vnd,
      shipping_fee_vnd: shippingFeeVnd,
      items: g.items.map((it) => ({
        sku_id: it.sku_id,
        quantity: it.quantity,
        flash_sale_item_id: flashBySku[it.sku_id] || "",
      })),
    }));

    const { breakdown } = await gw<{ breakdown: GwDiscountBreakdown }>(
      "/api/v1/promotions/preview",
      {
        method: "POST",
        body: {
          shop_items: shopItems,
          shop_voucher_ids: { [site.shopId]: found.id },
          platform_voucher_id: "",
          freeship_voucher_id: "",
          coins_to_use: 0,
          sales_channel: STOREFRONT_CHANNEL,
          storefront_shop_id: site.shopId,
        },
      }
    );

    const rawWarnings = breakdown.warnings ?? [];
    const rejectedFlashSaleItemIds = extractRejectedFlashSaleItemIds(rawWarnings);
    const warningsVi = rawWarnings.map(translatePromotionWarning);
    const safeBreakdown: GwDiscountBreakdown = { ...breakdown, warnings: warningsVi };
    const discountVnd =
      breakdown.shops.find((s) => s.shop_id === site.shopId)?.shop_voucher_discount_vnd ?? 0;

    if (discountVnd <= 0) {
      const voucherWarnIdx = rawWarnings.findIndex((w) => !w.toLowerCase().includes("flash sale"));
      const voucherWarnVi = voucherWarnIdx >= 0 ? warningsVi[voucherWarnIdx] : "";
      const reason =
        voucherWarnVi ||
        (found.min_order_vnd > 0
          ? `Đơn hàng cần tối thiểu ${found.min_order_vnd.toLocaleString("vi-VN")}₫ để dùng mã ${found.code}.`
          : `Mã ${found.code} chưa đủ điều kiện áp dụng cho giỏ hàng hiện tại.`);
      return Response.json(
        {
          valid: false,
          voucher: found,
          scope: "SHOP",
          shop_id: site.shopId,
          discount_vnd: 0,
          reason,
          rejected_flash_sale_item_ids: rejectedFlashSaleItemIds,
        },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    return Response.json(
      {
        valid: true,
        voucher: found,
        scope: "SHOP",
        shop_id: site.shopId,
        discount_vnd: discountVnd,
        breakdown: safeBreakdown,
        rejected_flash_sale_item_ids: rejectedFlashSaleItemIds,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng nhập mã ưu đãi cần kiểm tra.",
      PLATFORM_PROMO_NOT_ALLOWED:
        "Cửa hàng này chỉ hỗ trợ mã ưu đãi riêng của gian hàng, không áp dụng mã sàn hay Xu.",
      FOREIGN_SHOP_VOUCHER: "Mã ưu đãi không thuộc cửa hàng này.",
    },
  }
);
