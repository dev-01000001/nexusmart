import { domainErrorResponse, revalidateDomainCaches } from "@/lib/server/domains";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertAdminSession } from "@/features/admin/adminServer";

interface RouteCtx {
  params: Promise<{ id: string; domainId: string }>;
}

export const DELETE = apiRoute<RouteCtx>(async (_req, ctx) => {
  await assertAdminSession();
  const { id: rawId, domainId: rawDomainId } = await ctx.params;
  const id = typeof rawId === "string" ? rawId.trim() : "";
  const domainId = typeof rawDomainId === "string" ? rawDomainId.trim() : "";

  if (!id || !domainId) {
    return Response.json(
      {
        error: {
          code: "INVALID_DOMAIN_PARAMS",
          message: "Mã cửa hàng hoặc mã tên miền không hợp lệ.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  try {
    await gw<void>(
      `/api/v1/admin/sites/${encodeURIComponent(id)}/domains/${encodeURIComponent(domainId)}`,
      {
        method: "DELETE",
      }
    );
    revalidateDomainCaches({ shopId: id, clearHostCache: true });
    return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    return domainErrorResponse(err);
  }
});
