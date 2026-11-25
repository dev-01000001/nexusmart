import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwCategory } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidResourceId } from "@/features/seller/sellerValidation";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const rawParentId = (req.nextUrl.searchParams.get("parent_id") ?? "").trim();
  const parentId = rawParentId && isValidResourceId(rawParentId) ? rawParentId : undefined;

  const data = await gw<{ categories: GwCategory[] }>("/api/v1/seller/categories", {
    query: { parent_id: parentId },
  });
  return Response.json(
    { categories: data.categories ?? [] },
    { headers: { "Cache-Control": "no-store" } }
  );
});
