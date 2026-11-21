import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { normalizeSuggestResponse, parseSuggestQuery } from "@/features/search/searchTypes";

export const GET = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    const q = parseSuggestQuery(req.nextUrl.searchParams.get("q"));

    if (!q) {
      return Response.json({ suggestions: [] }, { headers: { "Cache-Control": "no-store" } });
    }

    // Host isolation: always lock shop_id to the site resolved from Host, ignoring client shop_id.
    const raw = await gw<unknown>("/api/v1/search/suggest", {
      auth: "none",
      query: {
        q,
        shop_id: site.shopId,
      },
    });

    const normalized = normalizeSuggestResponse(raw);
    return Response.json(normalized, {
      headers: { "Cache-Control": "no-store" },
    });
  },
  {
    messages: {
      not_found: "Không tìm thấy cửa hàng tương ứng.",
      unavailable: "Chưa tải được gợi ý tìm kiếm lúc này.",
    },
  }
);
