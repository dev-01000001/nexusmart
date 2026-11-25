import { getSiteRootDomain } from "@/lib/server/config";
import { GatewayError } from "@/lib/server/errors";
import { gw, gwRaw } from "@/lib/server/gateway";
import { apiRoute, resolveRequestHost } from "@/lib/server/http";
import { applyProxySecurityHeaders } from "@/lib/server/proxy-security";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { verifySiteShippingLabelAccess } from "@/lib/server/storefront";
import { classifyHost, isTenantSiteHost } from "@/lib/site/host";
import { isValidTrackingCode } from "@/features/seller/sellerValidation";
import type { GwOrder } from "@/features/account/orderView";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ code: string }> };

/**
 * Authenticated same-origin proxy for shipping labels (`GET /api/v1/shipping/labels/{code}`).
 * - On tenant site hosts: requires a published site, `order_id` query param belonging to this
 *   site's shop and the authenticated buyer, and a FORWARD or RETURN shipment of that order
 *   whose `tracking_code` matches `code` (returns 404 on any mismatch).
 * - On the root host: delegates seller/admin/buyer authorization directly to gateway.
 * Always enforces `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, and strict
 * sandbox CSP (`FALLBACK_HTML_SANDBOX_CSP`) on HTML responses.
 */
export const GET = apiRoute<RouteCtx>(async (req, { params }) => {
  const session = await assertSession();
  const { code } = await params;
  const cleanCode = (code ?? "").trim();
  if (!isValidTrackingCode(cleanCode)) throw new GatewayError("not_found");

  const hostClass = classifyHost(resolveRequestHost(req.headers), getSiteRootDomain());
  if (isTenantSiteHost(hostClass)) {
    const site = await requireRequestSite(req);
    const orderId = new URL(req.url).searchParams.get("order_id")?.trim() ?? "";
    await verifySiteShippingLabelAccess({
      trackingCode: cleanCode,
      orderId,
      shopId: site.shopId,
      session,
      fetchOrder: (oid) =>
        gw<{ order?: GwOrder | null }>(`/api/v1/orders/${encodeURIComponent(oid)}`),
      fetchShipment: (oid, direction) =>
        gw<{ shipment?: { tracking_code?: string | null } | null }>(
          `/api/v1/shipping/shipments/${encodeURIComponent(oid)}`,
          { query: { direction } }
        ),
    });
  } else if (hostClass.kind !== "root") {
    throw new GatewayError("not_found");
  }

  const upstream = await gwRaw(`/api/v1/shipping/labels/${encodeURIComponent(cleanCode)}`, {
    method: "GET",
    accept: "text/html, application/pdf, */*",
  });

  const contentType = upstream.headers.get("content-type") || "text/html; charset=utf-8";
  const headers = new Headers({
    "Content-Type": contentType,
    "Cache-Control": "no-store",
  });
  const disposition = upstream.headers.get("content-disposition");
  if (disposition) {
    headers.set("Content-Disposition", disposition);
  }
  applyProxySecurityHeaders(headers, upstream.headers, contentType);

  return new Response(upstream.body, {
    status: upstream.status,
    headers,
  });
});
