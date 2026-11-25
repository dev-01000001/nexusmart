import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import type { GwOrder } from "@/features/account/orderView";
import type { GatewayShipmentResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const POST = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req, { fresh: true });
  const session = await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  const body = await readJson<{ preferred_date?: unknown; note?: unknown }>(req);
  const preferredDate = typeof body?.preferred_date === "string" ? body.preferred_date.trim() : "";
  const note = typeof body?.note === "string" ? body.note.trim().slice(0, 300) : "";

  const existing = await gw<{ order?: GwOrder | null }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}`
  );
  const scoped = assertInStorefront(existing.order, site.shopId);
  assertBuyerOrderOwnership(scoped, session);

  const data = await gw<{ shipment: GatewayShipmentResponse["shipment"] }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}/redelivery`,
    {
      method: "POST",
      body: {
        preferred_date: preferredDate,
        note,
      },
    }
  );

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
