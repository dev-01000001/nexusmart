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
      note?: unknown;
      evidence_urls?: unknown;
    }>(req);

    const note = typeof body?.note === "string" ? body.note.trim() : "";
    const rawUrls = Array.isArray(body?.evidence_urls)
      ? body.evidence_urls.filter((u): u is string => typeof u === "string")
      : [];
    const urlCheck = validateEvidenceUrls(rawUrls);
    if (!urlCheck.ok) {
      throw new GatewayError("invalid", "INVALID_EVIDENCE_URLS");
    }
    if (note.length < 4 && urlCheck.urls.length === 0) {
      throw new GatewayError("invalid", "EMPTY_EVIDENCE");
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
      `/api/v1/returns/${encodeURIComponent(cleanId)}/evidence`,
      {
        method: "POST",
        body: {
          note: note.slice(0, 1000),
          evidence_urls: urlCheck.urls,
        },
      }
    );

    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      EMPTY_EVIDENCE: "Vui lòng nhập ghi chú bổ sung hoặc ít nhất một liên kết bằng chứng.",
      INVALID_EVIDENCE_URLS:
        "Tối đa 5 liên kết bằng chứng hợp lệ (bắt đầu bằng http:// hoặc https://, tối đa 500 ký tự).",
    },
  }
);
