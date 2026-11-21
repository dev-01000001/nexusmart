import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { parseGatewayConversationsResponse } from "@/features/chat/chatTypes";

export const GET = apiRoute(async (req) => {
  const session = await assertSellerSession();
  const sp = req.nextUrl.searchParams;
  const unrepliedOnly = sp.get("unreplied_only") === "true" ? true : undefined;
  const page = sp.get("page")?.trim() || undefined;
  const pageSize = sp.get("page_size")?.trim() || undefined;

  const res = await gw<unknown>("/api/v1/seller/chat/conversations", {
    query: {
      unreplied_only: unrepliedOnly,
      page,
      page_size: pageSize,
    },
  });

  const payload = parseGatewayConversationsResponse(res, session.user.shopId, {
    allowEmptyShopId: true,
  });

  return Response.json(payload, { headers: { "Cache-Control": "no-store" } });
});
