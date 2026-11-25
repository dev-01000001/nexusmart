import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import type { GatewayPaymentMethodsResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

export const GET = apiRoute(async (req) => {
  await requireRequestSite(req);
  const data = await gw<GatewayPaymentMethodsResponse>("/api/v1/payment-methods", {
    auth: "none",
  });
  return Response.json(
    { methods: Array.isArray(data?.methods) ? data.methods : [] },
    { headers: { "Cache-Control": "no-store" } }
  );
});
