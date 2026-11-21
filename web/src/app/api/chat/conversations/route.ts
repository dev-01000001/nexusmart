import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import {
  normalizeConversation,
  parseGatewayConversationsResponse,
} from "@/features/chat/chatTypes";

interface RawCreateConversationResponse {
  conversation?: unknown;
  created?: boolean;
}

export const GET = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  await assertSession();

  const res = await gw<unknown>("/api/v1/chat/conversations", {
    query: { shop_id: site.shopId, scope: "buyer" },
  });

  // Defense-in-depth tenant isolation: only return conversations for the current site's shop.
  const { conversations } = parseGatewayConversationsResponse(res, site.shopId);

  return Response.json({ conversations }, { headers: { "Cache-Control": "no-store" } });
});

export const POST = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  await assertSession();

  let initialMessage = "";
  try {
    const raw = (await req.json()) as { initial_message?: unknown } | null;
    if (raw && typeof raw.initial_message === "string") {
      initialMessage = raw.initial_message.trim().slice(0, 2000);
    }
  } catch {
    // Empty body is valid when opening a conversation with the current site's shop.
  }

  // Always ignore any client-supplied shop_id; lock strictly to the site's shopId.
  const res = await gw<RawCreateConversationResponse>("/api/v1/chat/conversations", {
    method: "POST",
    body: {
      shop_id: site.shopId,
      site_host: site.host,
      ...(initialMessage ? { initial_message: initialMessage } : {}),
    },
  });

  const conversation = normalizeConversation(res.conversation);
  if (!conversation) {
    throw new GatewayError("unavailable");
  }
  if (conversation.shop_id && conversation.shop_id !== site.shopId) {
    throw new GatewayError("not_found");
  }

  return Response.json(
    { conversation, created: Boolean(res.created) },
    { status: res.created ? 201 : 200, headers: { "Cache-Control": "no-store" } }
  );
});
