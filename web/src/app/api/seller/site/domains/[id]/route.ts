import { domainErrorResponse, revalidateDomainCaches } from "@/lib/server/domains";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const DELETE = apiRoute<RouteCtx>(async (_req, ctx) => {
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
    await gw<void>(`/api/v1/seller/site/domains/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
    revalidateDomainCaches({ shopId: session.user.shopId, clearHostCache: true });
    return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    return domainErrorResponse(err);
  }
});
