import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { verifySiteReturnAccess } from "@/lib/server/storefront";
import { validateEvidenceUrls } from "@/features/account/orderMeta";
import type { GwOrder } from "@/features/account/orderView";
import type { GatewayReturnRequest } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const POST = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const body = await readJson<{
      reason?: unknown;
      evidence_urls?: unknown;
    }>(req);

    const reason = typeof body?.reason === "string" ? body.reason.trim() : "";
    if (reason.length < 6) {
      throw new GatewayError("invalid", "INVALID_ESCALATE_REASON");
    }
    const rawUrls = Array.isArray(body?.evidence_urls)
      ? body.evidence_urls.filter((u): u is string => typeof u === "string")
      : [];
    const urlCheck = validateEvidenceUrls(rawUrls);
    if (!urlCheck.ok) {
      throw new GatewayError("invalid", "INVALID_EVIDENCE_URLS");
    }

    // Derive the order strictly from gateway GET /api/v1/returns/{id}; never trust client order_id.
    await verifySiteReturnAccess({
      returnId: cleanId,
      shopId: site.shopId,
      session,
      fetchReturn: (rid) =>
        gw<{ return_request?: GatewayReturnRequest | null }>(
          `/api/v1/returns/${encodeURIComponent(rid)}`
        ),
      fetchOrder: (oid) =>
        gw<{ order?: GwOrder | null }>(`/api/v1/orders/${encodeURIComponent(oid)}`),
    });

    const data = await gw<{ return_request: GatewayReturnRequest }>(
      `/api/v1/returns/${encodeURIComponent(cleanId)}/escalate`,
      {
        method: "POST",
        body: {
          reason: reason.slice(0, 1000),
          evidence_urls: urlCheck.urls,
        },
      }
    );

    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ESCALATE_REASON: "Vui lòng nêu rõ lý do khiếu nại (tối thiểu 6 ký tự).",
      INVALID_EVIDENCE_URLS:
        "Tối đa 5 liên kết bằng chứng hợp lệ (bắt đầu bằng http:// hoặc https://, tối đa 500 ký tự).",
    },
  }
);
