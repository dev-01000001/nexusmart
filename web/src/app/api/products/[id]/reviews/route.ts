import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import type { GatewayProductReviewsResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const GET = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req);
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  // Enforce strict tenant isolation: verify the product belongs to this site's shop.
  const prodRes = await gw<{ product?: { id?: string; shop_id?: string } | null }>(
    `/api/v1/products/${encodeURIComponent(cleanId)}`,
    { auth: "none" }
  );
  if (!prodRes?.product || prodRes.product.shop_id !== site.shopId) {
    throw new GatewayError("not_found");
  }

  const { searchParams } = new URL(req.url);
  const page = Math.max(1, Number.parseInt(searchParams.get("page") ?? "1", 10) || 1);
  const pageSize = Math.min(
    50,
    Math.max(1, Number.parseInt(searchParams.get("page_size") ?? "10", 10) || 10)
  );
  const ratingFilter = Number.parseInt(searchParams.get("rating") ?? "0", 10);
  const rating =
    Number.isFinite(ratingFilter) && ratingFilter >= 1 && ratingFilter <= 5
      ? ratingFilter
      : undefined;

  const data = await gw<GatewayProductReviewsResponse>(
    `/api/v1/products/${encodeURIComponent(cleanId)}/reviews`,
    {
      auth: "none",
      query: { page, page_size: pageSize, rating },
    }
  );

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
