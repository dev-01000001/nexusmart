import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwFlashSaleItem } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidResourceId } from "@/features/seller/sellerValidation";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const flashSaleIdRaw = (q.get("flash_sale_id") ?? "").trim();
  const flashSaleId =
    flashSaleIdRaw && isValidResourceId(flashSaleIdRaw) ? flashSaleIdRaw : undefined;

  const data = await gw<{
    items: GwFlashSaleItem[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/flash-sales/items", {
    query: {
      status: q.get("status"),
      flash_sale_id: flashSaleId,
      page: q.get("page"),
      page_size: q.get("page_size"),
    },
  });
  return Response.json(
    {
      items: data.items ?? [],
      page: data.page ?? 1,
      page_size: data.page_size ?? 50,
      total: data.total ?? 0,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
