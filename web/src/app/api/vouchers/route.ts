import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwVoucher } from "@/features/commerce/types";

export const GET = apiRoute(async (req) => {
  const { searchParams } = new URL(req.url);
  const query = {
    shop_id: searchParams.get("shop_id") || undefined,
    scope: searchParams.get("scope") || undefined,
    order_amount_vnd: searchParams.get("order_amount_vnd") || undefined,
  };

  try {
    const data = await gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers", { query });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    // Public voucher catalog stays readable even when a stale session cookie fails refresh.
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      const data = await gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers", {
        auth: "none",
        query,
      });
      return Response.json(data, { headers: { "Cache-Control": "no-store" } });
    }
    throw err;
  }
});
