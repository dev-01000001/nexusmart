import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { AdminUser } from "@/features/admin/adminComplaintsTypes";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const params = req.nextUrl.searchParams;
  const searchTerm = (params.get("q") ?? params.get("query") ?? "").trim() || undefined;
  const data = await gw<{ items: AdminUser[]; page: number; page_size: number; total: number }>(
    "/api/v1/admin/users",
    {
      query: {
        role: params.get("role") || undefined,
        status: params.get("status") || undefined,
        q: searchTerm,
        query: searchTerm,
        page: params.get("page") || undefined,
        page_size: params.get("page_size") || undefined,
      },
    }
  );
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
