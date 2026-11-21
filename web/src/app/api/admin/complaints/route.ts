import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import { normalizeComplaintsListResponse } from "@/features/admin/adminComplaintsTypes";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const q = req.nextUrl.searchParams;
  const raw = await gw<unknown>("/api/v1/admin/complaints", {
    query: {
      status: q.get("status") || undefined,
      page: q.get("page") || undefined,
      page_size: q.get("page_size") || undefined,
    },
  });
  const normalized = normalizeComplaintsListResponse(raw);
  return Response.json(normalized, { headers: { "Cache-Control": "no-store" } });
});
