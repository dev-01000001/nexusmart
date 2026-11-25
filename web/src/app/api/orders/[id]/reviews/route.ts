import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import type { GwOrder } from "@/features/account/orderView";
import { MAX_REVIEW_COMMENT_LEN, validateReviewImageUrls } from "@/features/product/reviewHelpers";
import type { GatewayOrderReviewsResponse, GatewayProductReview } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const GET = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req);
  const session = await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  const existing = await gw<{ order?: GwOrder | null }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}`
  );
  const scoped = assertInStorefront(existing.order, site.shopId);
  assertBuyerOrderOwnership(scoped, session);

  const data = await gw<GatewayOrderReviewsResponse>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}/reviews`
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const POST = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const body = await readJson<{
      order_item_id?: unknown;
      rating?: unknown;
      comment?: unknown;
      image_urls?: unknown;
    }>(req);

    const orderItemId = typeof body?.order_item_id === "string" ? body.order_item_id.trim() : "";
    const rating = Math.trunc(Number(body?.rating ?? 0));
    const comment = typeof body?.comment === "string" ? body.comment.trim() : "";
    const rawImages = Array.isArray(body?.image_urls)
      ? body.image_urls.filter((u): u is string => typeof u === "string")
      : [];

    if (!orderItemId || !Number.isFinite(rating) || rating < 1 || rating > 5) {
      throw new GatewayError("invalid", "INVALID_REVIEW");
    }
    if (comment.length > MAX_REVIEW_COMMENT_LEN) {
      throw new GatewayError("invalid", "REVIEW_COMMENT_TOO_LONG");
    }
    const { urls, error } = validateReviewImageUrls(rawImages);
    if (error) {
      throw new GatewayError("invalid", "INVALID_REVIEW_IMAGES");
    }

    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    const scoped = assertInStorefront(existing.order, site.shopId);
    assertBuyerOrderOwnership(scoped, session);

    const data = await gw<{ review: GatewayProductReview }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}/reviews`,
      {
        method: "POST",
        body: {
          order_item_id: orderItemId,
          rating,
          comment,
          image_urls: urls,
        },
      }
    );

    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_REVIEW: "Vui lòng chọn sản phẩm và số sao đánh giá hợp lệ (từ 1 đến 5 sao).",
      REVIEW_COMMENT_TOO_LONG: "Nội dung đánh giá vượt quá giới hạn 2.000 ký tự.",
      INVALID_REVIEW_IMAGES:
        "Liên kết ảnh đánh giá chưa hợp lệ (tối đa 5 ảnh https:// hoặc /products/..., mỗi liên kết tối đa 500 ký tự).",
    },
  }
);
