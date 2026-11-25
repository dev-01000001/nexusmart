import { domainErrorResponse, revalidateDomainCaches } from "@/lib/server/domains";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { normalizeSiteDomain, type RawSiteDomain } from "@/lib/site/domains";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id: rawId } = await ctx.params;
  const id = typeof rawId === "string" ? rawId.trim() : "";

  if (!id) {
    return Response.json(
      {
        error: {
          code: "INVALID_DOMAIN_ID",
          message: "Mã tên miền không hợp lệ.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  try {
    const data = await gw<{ domain?: RawSiteDomain | null }>(
      `/api/v1/seller/site/domains/${encodeURIComponent(id)}/check`,
      {
        method: "POST",
      }
    );
    const domain = normalizeSiteDomain(data?.domain);
    revalidateDomainCaches({
      shopId: session.user.shopId,
      hosts: domain ? [domain.host] : [],
      clearHostCache: domain?.status === "ACTIVE" || Boolean(domain?.is_primary),
    });
    return Response.json({ domain }, { headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    return domainErrorResponse(err);
  }
});
