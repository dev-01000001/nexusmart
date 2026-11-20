import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAdminOrder } from "@/features/admin/adminApiTypes";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const q = req.nextUrl.searchParams;
  const data = await gw<{ items: GwAdminOrder[]; page: number; page_size: number; total: number }>(
    "/api/v1/admin/orders",
    {
      query: {
        status: q.get("status"),
        shop_id: q.get("shop_id"),
        buyer_id: q.get("buyer_id"),
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    }
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
