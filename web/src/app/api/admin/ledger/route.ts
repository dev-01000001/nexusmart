import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwLedgerSummary } from "@/features/admin/adminApiTypes";

export const GET = apiRoute(async () => {
  await assertAdminSession();
  const data = await gw<GwLedgerSummary>("/api/v1/admin/ledger/summary");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
