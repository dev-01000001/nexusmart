import { GatewayError } from "@/lib/server/errors";
import { gw, gwRaw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertPaymentInStorefront } from "@/lib/server/storefront";
import type { GwPayment } from "@/features/commerce/types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

/**
 * Authenticated SSE stream proxy for live payment status updates (`/api/v1/payments/{id}/events`).
 * Enforces strict tenant & buyer ownership before opening the upstream SSE stream.
 */
export const GET = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req);
  const session = await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  // Verify the payment belongs to THIS site's shop and the authenticated buyer before streaming.
  const existing = await gw<{ payment?: GwPayment | null }>(
    `/api/v1/payments/${encodeURIComponent(cleanId)}`
  );
  assertPaymentInStorefront(existing.payment, site.shopId, session.user.id);

  const upstream = await gwRaw(`/api/v1/payments/${encodeURIComponent(cleanId)}/events`, {
    method: "GET",
    accept: "text/event-stream",
    timeoutMs: 120_000,
    signal: req.signal,
  });

  return new Response(upstream.body, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream; charset=utf-8",
      "Cache-Control": "no-cache, no-store, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
      "X-Content-Type-Options": "nosniff",
    },
  });
});
