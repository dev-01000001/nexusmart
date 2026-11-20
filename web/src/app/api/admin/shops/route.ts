import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAdminShop } from "@/features/admin/adminApiTypes";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const q = req.nextUrl.searchParams;
  const data = await gw<{ items: GwAdminShop[]; page: number; page_size: number; total: number }>(
    "/api/v1/admin/shops",
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
