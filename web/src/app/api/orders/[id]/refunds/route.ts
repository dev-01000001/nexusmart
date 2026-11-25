import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import type { GwOrder } from "@/features/account/orderView";
import type { GatewayRefund, PaginatedResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const GET = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req);
  const session = await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  const existing = await gw<{ order?: GwOrder | null }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}`
  );
  const scoped = assertInStorefront(existing.order, site.shopId);
  assertBuyerOrderOwnership(scoped, session);

  const { searchParams } = new URL(req.url);
  const page = Math.max(1, Number.parseInt(searchParams.get("page") ?? "1", 10) || 1);
  const pageSize = Math.min(
    50,
    Math.max(1, Number.parseInt(searchParams.get("page_size") ?? "20", 10) || 20)
  );

  const data = await gw<PaginatedResponse<GatewayRefund>>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}/refunds`,
    { query: { page, page_size: pageSize } }
  );

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
