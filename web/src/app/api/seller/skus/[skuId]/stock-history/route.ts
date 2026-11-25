import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { StockHistoryItem } from "@/lib/a8-types";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ skuId: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { skuId } = await ctx.params;
    const cleanSkuId = assertValidId(skuId, "INVALID_SKU_ID");
    const q = req.nextUrl.searchParams;

    const data = await gw<{
      items: StockHistoryItem[];
      page: number;
      page_size: number;
      total: number;
    }>(`/api/v1/seller/skus/${encodeURIComponent(cleanSkuId)}/stock-history`, {
      query: {
        change_type: q.get("change_type"),
        from_time: q.get("from_time"),
        to_time: q.get("to_time"),
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(
      {
        items: data.items ?? [],
        page: data.page ?? 1,
        page_size: data.page_size ?? 20,
        total: data.total ?? 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_SKU_ID: "Mã biến thể SKU không hợp lệ.",
    },
  }
);
