import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerShop } from "@/features/seller/sellerApiTypes";

export const GET = apiRoute(async () => {
  await assertSellerSession();
  const data = await gw<{ shop: GwSellerShop | null }>("/api/v1/seller/shop");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
