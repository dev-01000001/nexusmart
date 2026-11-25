import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { normalizeSearchResponse, parseSearchQueryParams } from "@/features/search/searchTypes";

export const GET = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    const parsed = parseSearchQueryParams(req.nextUrl.searchParams);

    // Host isolation: always lock shop_id to the site resolved from Host, ignoring client shop_id.
    const raw = await gw<unknown>("/api/v1/search", {
      auth: "none",
      query: {
        q: parsed.q || undefined,
        shop_id: site.shopId,
        category_slug: parsed.category || undefined,
        min_price_vnd: parsed.min_price,
        max_price_vnd: parsed.max_price,
        sort_by: parsed.sort,
        page: parsed.page,
        page_size: parsed.page_size,
      },
    });

    const normalized = normalizeSearchResponse(raw, {
      page: parsed.page,
      page_size: parsed.page_size,
      shopId: site.shopId,
    });

    return Response.json(normalized, {
      headers: { "Cache-Control": "no-store" },
    });
  },
  {
    messages: {
      not_found: "Không tìm thấy cửa hàng tương ứng.",
      unavailable: "Dịch vụ tìm kiếm tạm gián đoạn. Bạn vui lòng thử lại sau.",
    },
  }
);
