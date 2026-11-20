import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerOrder } from "@/features/seller/sellerApiTypes";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  // Never trust client shop_id; gateway reads shop_id from the seller's JWT.
  const data = await gw<{ items: GwSellerOrder[]; page: number; page_size: number; total: number }>(
    "/api/v1/seller/orders",
    {
      query: {
        status: q.get("status"),
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    }
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
