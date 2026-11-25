import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayOrder, PaginatedResponse } from "@/lib/gateway-types";

export const GET = apiRoute(async (req) => {
  await assertCapability("orders.read");
  const q = req.nextUrl.searchParams;
  const data = await gw<PaginatedResponse<GatewayOrder>>("/api/v1/admin/orders/needs-review", {
    query: {
      page: q.get("page"),
      page_size: q.get("page_size"),
    },
  });
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
