import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { normalizeReviewSummary } from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (req, ctx) => {
  const site = await requireRequestSite(req);
  const { id } = await ctx.params;
  const cleanId = (id ?? "").trim();
  if (!cleanId) {
    throw new GatewayError("invalid", "INVALID_PRODUCT_ID");
  }

  const raw = await gw<unknown>(`/api/v1/products/${encodeURIComponent(cleanId)}/review-summary`, {
    auth: "none",
    query: { shop_id: site.shopId },
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
