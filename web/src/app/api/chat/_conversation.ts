import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { parseGatewayMessagesResponse } from "@/features/chat/chatTypes";

export function parseConversationId(rawId: string): string {
  const clean = rawId.trim();
  if (!clean || clean.length > 128 || /[\s/]/.test(clean)) {
    throw new GatewayError("not_found");
  }
  return clean;
}

export function parseMessageLimit(raw: string | null): number | undefined {
  if (!raw) return undefined;
  const n = Number.parseInt(raw, 10);
  if (!Number.isFinite(n) || n < 1) return undefined;
  return Math.min(100, n);
}

export interface ConversationMessagesQuery {
  before?: string;
  after?: string;
  limit?: number;
}

/**
 * Fetch messages for `convId` and fail closed with 404 unless `conversation.shop_id` strictly
 * matches `expectedShopId`. Shared by both GET (full page fetch) and POST (pre-send ownership
 * check with `{ limit: 1 }`) on buyer and seller chat BFF routes.
 */
export async function fetchShopConversationMessages(
  convId: string,
  expectedShopId: string,
  query: ConversationMessagesQuery = {}
) {
  const res = await gw<unknown>(
    `/api/v1/chat/conversations/${encodeURIComponent(convId)}/messages`,
    {
      query: {
        before: query.before,
        after: query.after,
        limit: query.limit,
      },
    }
  );
  const parsed = parseGatewayMessagesResponse(res);
  if (!expectedShopId || !parsed.conversation || parsed.conversation.shop_id !== expectedShopId) {
    throw new GatewayError("not_found");
  }
  return parsed;
}
