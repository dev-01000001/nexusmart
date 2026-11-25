import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import type { GwOrder } from "@/features/account/orderView";
import type { GatewayShipmentResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const GET = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req);
  const session = await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  // Verify the order belongs to THIS site's shop and the authenticated buyer.
  const orderRes = await gw<{ order?: GwOrder | null }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}`
  );
  const scoped = assertInStorefront(orderRes.order, site.shopId);
  assertBuyerOrderOwnership(scoped, session);

  const { searchParams } = new URL(req.url);
  const rawDir = (searchParams.get("direction") ?? "").trim().toUpperCase();
  const direction = rawDir === "RETURN" || rawDir === "FORWARD" ? rawDir : undefined;

  const data = await gw<GatewayShipmentResponse>(
    `/api/v1/shipping/shipments/${encodeURIComponent(cleanId)}`,
    { query: { direction } }
  );

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
