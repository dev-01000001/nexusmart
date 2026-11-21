import { domainErrorResponse } from "@/lib/server/domains";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { normalizeSiteDomains, type RawSiteDomain } from "@/lib/site/domains";
import { assertAdminSession } from "@/features/admin/adminServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  await assertAdminSession();
  const { id: rawId } = await ctx.params;
  const id = typeof rawId === "string" ? rawId.trim() : "";

  if (!id) {
    return Response.json(
      {
        error: {
          code: "INVALID_SHOP_ID",
          message: "Mã cửa hàng không hợp lệ.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  try {
    const data = await gw<{ domains?: RawSiteDomain[] }>(
      `/api/v1/admin/sites/${encodeURIComponent(id)}/domains`
    );
    return Response.json(
      { domains: normalizeSiteDomains(data?.domains) },
      { headers: { "Cache-Control": "no-store" } }
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return Response.json({ domains: [] }, { headers: { "Cache-Control": "no-store" } });
    }
    return domainErrorResponse(err);
  }
});
