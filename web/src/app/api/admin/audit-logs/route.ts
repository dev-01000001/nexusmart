import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAuditLog } from "@/features/admin/adminApiTypes";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const q = req.nextUrl.searchParams;
  const data = await gw<{ items: GwAuditLog[]; page: number; page_size: number; total: number }>(
    "/api/v1/admin/audit-logs",
    {
      query: {
        actor_id: q.get("actor_id"),
        target_type: q.get("target_type"),
        target_id: q.get("target_id"),
        from_time: q.get("from_time"),
        to_time: q.get("to_time"),
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    }
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
