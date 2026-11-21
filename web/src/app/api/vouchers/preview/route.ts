import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import {
  STOREFRONT_CHANNEL,
  hasPlatformPromoInput,
  resolveSiteShopVoucherMap,
  scopeCartToShop,
} from "@/lib/server/storefront";
import { translatePromotionWarning } from "@/features/commerce/checkoutErrors";
import type { GwCart, GwDiscountBreakdown } from "@/features/commerce/types";

interface PreviewLineInput {
  sku_id?: string;
  flash_sale_item_id?: string;
}

interface PreviewShopInput {
  shop_id?: string;
  shipping_fee_vnd?: number;
  items?: PreviewLineInput[];
}

interface PreviewBody {
  shop_items?: PreviewShopInput[];
  /** Preferred flat shipping fee (no shop_id from client). */
  shipping_fee_vnd?: number;
  shipping_fees?: Record<string, number>;
  /** Preferred flat voucher ID (no shop_id from client). */
  shop_voucher_id?: string | null;
  shop_voucher_ids?: Record<string, string | null | undefined>;
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  flash_sale_item_by_sku?: Record<string, string>;
}

const EMPTY_BREAKDOWN: GwDiscountBreakdown = {
  shops: [],
  total_subtotal_vnd: 0,
  total_flash_sale_discount_vnd: 0,
  total_shop_voucher_discount_vnd: 0,
  total_platform_voucher_discount_vnd: 0,
  total_freeship_discount_vnd: 0,
  total_coins_discount_vnd: 0,
  total_discount_vnd: 0,
  platform_cost_vnd: 0,
  shop_cost_vnd: 0,
  total_shipping_fee_vnd: 0,
  final_payable_vnd: 0,
  warnings: [],
};

export const POST = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    await assertSession();
    const body = await readJson<PreviewBody>(req);

    if (hasPlatformPromoInput(body)) {
      throw new GatewayError("invalid", "PLATFORM_PROMO_NOT_ALLOWED");
    }
    const shopVoucherIds = resolveSiteShopVoucherMap(body, site.shopId);

    const bodyShops = Array.isArray(body.shop_items) ? body.shop_items : [];
    if (bodyShops.some((s) => (s?.shop_id ?? "").trim() && s.shop_id!.trim() !== site.shopId)) {
      throw new GatewayError("invalid", "FOREIGN_SHOP_ITEM");
    }

    const { cart } = await gw<{ cart: GwCart }>("/api/v1/cart", {
      query: { shop_id: site.shopId },
    });
    const scoped = scopeCartToShop(cart, site.shopId);

    if (scoped.shop_groups.length === 0) {
      return Response.json(
        { breakdown: EMPTY_BREAKDOWN, warnings_vi: [] },
        { headers: { "Cache-Control": "no-store" } }
      );
    }

    const bodyShop = bodyShops.find(
      (s) => !(s?.shop_id ?? "").trim() || (s?.shop_id ?? "").trim() === site.shopId
    );
    const rawFee =
      body.shipping_fee_vnd ?? body.shipping_fees?.[site.shopId] ?? bodyShop?.shipping_fee_vnd ?? 0;
    const shippingFeeVnd = Math.max(0, Math.trunc(Number(rawFee) || 0));

    const flashBySku: Record<string, string> = {};
    for (const it of bodyShop?.items ?? []) {
      const sku = typeof it?.sku_id === "string" ? it.sku_id.trim() : "";
      const fs = typeof it?.flash_sale_item_id === "string" ? it.flash_sale_item_id.trim() : "";
      if (sku && fs) flashBySku[sku] = fs;
    }
    for (const [k, v] of Object.entries(body.flash_sale_item_by_sku ?? {})) {
      const sku = k.trim();
      const fs = typeof v === "string" ? v.trim() : "";
      if (sku && fs) flashBySku[sku] = fs;
    }

    const shopItems = scoped.shop_groups.map((g) => ({
      shop_id: site.shopId,
      subtotal_vnd: g.subtotal_vnd,
      shipping_fee_vnd: shippingFeeVnd,
      items: g.items.map((it) => ({
        sku_id: it.sku_id,
        quantity: it.quantity,
        unit_price_vnd: it.price_vnd,
        flash_sale_item_id: flashBySku[it.sku_id] || "",
      })),
    }));

    const data = await gw<{ breakdown: GwDiscountBreakdown }>("/api/v1/promotions/preview", {
      method: "POST",
      body: {
        shop_items: shopItems,
        shop_voucher_ids: shopVoucherIds,
        platform_voucher_id: "",
        freeship_voucher_id: "",
        coins_to_use: 0,
        sales_channel: STOREFRONT_CHANNEL,
        storefront_shop_id: site.shopId,
      },
    });

    const warningsVi = (data.breakdown?.warnings ?? []).map(translatePromotionWarning);
    const safeBreakdown: GwDiscountBreakdown = {
      ...data.breakdown,
      warnings: warningsVi,
    };

    return Response.json(
      { breakdown: safeBreakdown, warnings_vi: warningsVi },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      PLATFORM_PROMO_NOT_ALLOWED:
        "Cửa hàng này chỉ hỗ trợ mã ưu đãi riêng của gian hàng, không áp dụng mã sàn hay Xu.",
      FOREIGN_SHOP_VOUCHER: "Mã ưu đãi không thuộc cửa hàng này.",
      FOREIGN_SHOP_ITEM: "Sản phẩm không thuộc cửa hàng này.",
      INVALID_ARGUMENT: "Mã ưu đãi không hợp lệ hoặc không thuộc cửa hàng này.",
    },
  }
);
