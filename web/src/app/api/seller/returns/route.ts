import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwSellerReturnRequest } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const status = (q.get("status") ?? "").trim().toUpperCase();
  const data = await gw<{
    items: GwSellerReturnRequest[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/returns", {
    query: {
      status: status && status !== "ALL" ? status : undefined,
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
