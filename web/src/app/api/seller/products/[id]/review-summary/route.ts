import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { normalizeReviewSummary } from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const cleanId = (id ?? "").trim();
  if (!cleanId) {
    throw new GatewayError("invalid", "INVALID_PRODUCT_ID");
  }

  // Verify the product belongs to the authenticated seller's shop before querying review summary.
  let prodRes: { product?: { id?: string; shop_id?: string } | null };
  try {
    prodRes = await gw<{ product?: { id?: string; shop_id?: string } | null }>(
      `/api/v1/seller/products/${encodeURIComponent(cleanId)}`
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "forbidden") {
      throw new GatewayError("not_found");
    }
    throw err;
  }
  if (!prodRes?.product || prodRes.product.shop_id !== session.user.shopId) {
    throw new GatewayError("not_found");
  }

  const raw = await gw<unknown>(`/api/v1/products/${encodeURIComponent(cleanId)}/review-summary`, {
    query: { shop_id: session.user.shopId },
  });

  const normalized = normalizeReviewSummary(raw, cleanId);
  return Response.json(
    normalized ?? {
      productId: cleanId,
      available: false,
      reviewCount: 0,
      minReviewsRequired: 10,
      averageRating: 0,
      summary: "",
      pros: [],
      cons: [],
      aiGenerated: false,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
