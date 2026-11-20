import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GwCoinBalance } from "@/features/commerce/types";

export const GET = apiRoute(async () => {
  await assertSession();
  const data = await gw<GwCoinBalance>("/api/v1/coins/balance");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
