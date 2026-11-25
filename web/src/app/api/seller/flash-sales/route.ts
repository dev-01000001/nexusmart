import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwFlashSale } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const data = await gw<{
    flash_sales?: GwFlashSale[];
    items?: GwFlashSale[];
    page?: number;
    page_size?: number;
    total?: number;
  }>("/api/v1/seller/flash-sales", {
    query: {
      status: q.get("status"),
      page: q.get("page"),
      page_size: q.get("page_size"),
    },
  });
  const list = data.flash_sales ?? data.items ?? [];
  return Response.json(
    {
      flash_sales: list,
      items: list,
      page: data.page ?? 1,
      page_size: data.page_size ?? 50,
      total: data.total ?? list.length,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
