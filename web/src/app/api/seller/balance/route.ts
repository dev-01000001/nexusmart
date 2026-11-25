import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerBalance } from "@/features/seller/sellerApiTypes";

export const GET = apiRoute(async () => {
  await assertSellerSession();
  const data = await gw<GwSellerBalance>("/api/v1/seller/balance");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
