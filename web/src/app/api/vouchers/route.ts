import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { STOREFRONT_CHANNEL, isSiteShopVoucher } from "@/lib/server/storefront";
import type { GwVoucher } from "@/features/commerce/types";

/** Only SHOP-scoped vouchers owned by THIS site's shop (shop derived from Host). */
export const GET = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  const { searchParams } = new URL(req.url);
  const rawOrderAmount = (searchParams.get("order_amount_vnd") ?? "").trim();
  const parsedAmount = Number(rawOrderAmount);
  const orderAmountVnd =
    rawOrderAmount && Number.isFinite(parsedAmount) && parsedAmount >= 0
      ? String(Math.trunc(parsedAmount))
      : undefined;

  // Never trust client shop_id / scope / sales_channel query params.
  const query = {
    shop_id: site.shopId,
    scope: "SHOP",
    sales_channel: STOREFRONT_CHANNEL,
    storefront_shop_id: site.shopId,
    order_amount_vnd: orderAmountVnd,
  };

  let rawVouchers: GwVoucher[];
  try {
    const data = await gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", { query });
    rawVouchers = data.vouchers ?? [];
  } catch (err) {
    // Public shop vouchers stay readable even when a stale session cookie fails refresh.
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      const data = await gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
        auth: "none",
        query,
      });
      rawVouchers = data.vouchers ?? [];
    } else {
      throw err;
    }
  }

  const vouchers = rawVouchers.filter((v) => isSiteShopVoucher(v, site.shopId));
  return Response.json({ vouchers }, { headers: { "Cache-Control": "no-store" } });
});
