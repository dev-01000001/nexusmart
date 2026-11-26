import { loadCatalogLookups } from "@/lib/catalog";
import type { GatewayReturnRequest } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import { validateEvidenceUrls } from "@/features/account/orderMeta";
import { mapGatewayOrder, type GwOrder } from "@/features/account/orderView";

type Params = { params: Promise<{ id: string }> };

async function readOrderReturn(cleanId: string): Promise<GatewayReturnRequest | null> {
  try {
    const res = await gw<{ return_request?: GatewayReturnRequest | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}/return`
    );
    return res?.return_request ?? null;
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return null;
    }
    throw err;
  }
}

export const GET = apiRoute<Params>(async (req, ctx) => {
  const site = await requireRequestSite(req);
  const session = await assertSession();
  const { id } = await ctx.params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  const existing = await gw<{ order?: GwOrder | null }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}`
  );
  const scoped = assertInStorefront(existing.order, site.shopId);
  assertBuyerOrderOwnership(scoped, session);

  const returnRequest = await readOrderReturn(cleanId);
  return Response.json(
    { return_request: returnRequest },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const POST = apiRoute<Params>(
  async (req, ctx) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const body = await readJson<{
      reason?: unknown;
      evidenceUrls?: unknown;
      evidence_urls?: unknown;
    }>(req);
    const reason = typeof body?.reason === "string" ? body.reason.trim() : "";
    if (reason.length < 5) {
      throw new GatewayError("invalid", "INVALID_REASON");
    }
    const rawUrls = Array.isArray(body?.evidence_urls)
      ? body.evidence_urls
      : Array.isArray(body?.evidenceUrls)
        ? body.evidenceUrls
        : [];
    const strUrls = rawUrls.filter((u): u is string => typeof u === "string");
    const urlCheck = validateEvidenceUrls(strUrls);
    if (!urlCheck.ok) {
      throw new GatewayError("invalid", "INVALID_EVIDENCE_URLS");
    }

    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    const scoped = assertInStorefront(existing.order, site.shopId);
    assertBuyerOrderOwnership(scoped, session);

    const res = await gw<{
      order: GwOrder;
      return_request_id: string;
      return_request?: GatewayReturnRequest | null;
    }>(`/api/v1/orders/${encodeURIComponent(cleanId)}/return`, {
      method: "POST",
      body: { reason: reason.slice(0, 500), evidence_urls: urlCheck.urls },
    });
    const [catalog, returnRequest] = await Promise.all([
      loadCatalogLookups(),
      res.return_request
        ? Promise.resolve(res.return_request)
        : readOrderReturn(cleanId).catch(() => null),
    ]);

    return Response.json(
      {
        order: mapGatewayOrder(res.order, catalog.lookups),
        return_request_id: res.return_request_id,
        returnRequestId: res.return_request_id,
        return_request: returnRequest,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_REASON: "Vui lòng nhập lý do đổi trả rõ ràng (tối thiểu 5 ký tự).",
      INVALID_EVIDENCE_URLS:
        "Tối đa 5 liên kết bằng chứng hợp lệ (bắt đầu bằng http:// hoặc https://, tối đa 500 ký tự).",
      not_found: "Không tìm thấy đơn hàng này.",
      forbidden: "Bạn không có quyền gửi yêu cầu đổi trả cho đơn hàng này.",
    },
  }
);
