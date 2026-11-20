import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { mapVoucherError } from "@/features/commerce/checkoutErrors";
import type { GwVoucher } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ id: string }> };

export const POST = apiRoute<RouteCtx>(async (req, { params }) => {
  await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  let code: string | undefined;
  const raw = await req.text();
  if (raw.trim()) {
    try {
      const parsed = JSON.parse(raw) as { code?: unknown };
      if (typeof parsed.code === "string" && parsed.code.trim()) {
        code = parsed.code.trim();
      }
    } catch {
      throw new GatewayError("invalid", "INVALID_JSON");
    }
  }

  try {
    const data = await gw<{ saved: boolean; voucher: GwVoucher }>(
      `/api/v1/vouchers/${encodeURIComponent(cleanId)}/save`,
      {
        method: "POST",
        body: code ? { code } : {},
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    if (
      err instanceof GatewayError &&
      (err.kind === "conflict" || err.kind === "not_found" || err.kind === "rate_limited")
    ) {
      const mapped = mapVoucherError(err.code, err.detail);
      return Response.json(
        { error: { code: mapped.code, message: mapped.message } },
        { status: mapped.status ?? err.status, headers: { "Cache-Control": "no-store" } }
      );
    }
    throw err;
  }
});
