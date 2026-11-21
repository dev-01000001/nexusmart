import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { normalizeConversation, type ChatConversationDTO } from "@/features/chat/chatTypes";

interface RawListConversationsResponse {
  conversations?: unknown[];
  total?: number;
  total_unread?: number;
}

export const GET = apiRoute(async (req) => {
  const session = await assertSellerSession();
  const sp = req.nextUrl.searchParams;
  const unrepliedOnly = sp.get("unreplied_only") === "true" ? true : undefined;
  const page = sp.get("page")?.trim() || undefined;
  const pageSize = sp.get("page_size")?.trim() || undefined;

  const res = await gw<RawListConversationsResponse>("/api/v1/chat/conversations", {
    query: {
      shop_id: session.user.shopId,
      scope: "seller",
      unreplied_only: unrepliedOnly,
      page,
      page_size: pageSize,
    },
  });

  const conversations: ChatConversationDTO[] = (res.conversations ?? [])
    .map((item) => normalizeConversation(item))
    .filter(
      (c): c is ChatConversationDTO =>
        c !== null && (!c.shop_id || c.shop_id === session.user.shopId)
    );

  const totalUnread =
    typeof res.total_unread === "number"
      ? res.total_unread
      : conversations.reduce((sum, c) => sum + c.unread_count, 0);

  return Response.json(
    {
      conversations,
      total: typeof res.total === "number" ? res.total : conversations.length,
      total_unread: totalUnread,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
