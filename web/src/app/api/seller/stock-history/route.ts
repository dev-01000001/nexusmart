import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { StockHistoryItem } from "@/lib/a8-types";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidResourceId } from "@/features/seller/sellerValidation";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const rawProductId = (q.get("product_id") ?? "").trim();
  const rawSkuId = (q.get("sku_id") ?? "").trim();

  const data = await gw<{
    items: StockHistoryItem[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/stock-history", {
    query: {
      product_id: rawProductId && isValidResourceId(rawProductId) ? rawProductId : undefined,
      sku_id: rawSkuId && isValidResourceId(rawSkuId) ? rawSkuId : undefined,
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
});
