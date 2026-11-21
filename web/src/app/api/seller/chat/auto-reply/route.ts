import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { normalizeAutoReplyConfig, validateAutoReplyInput } from "@/features/chat/chatTypes";

async function proxyAutoReply<T>(
  method: "GET" | "PUT",
  shopId: string,
  body?: Record<string, unknown>
): Promise<T> {
  try {
    return await gw<T>("/api/v1/chat/auto-reply", {
      method,
      query: method === "GET" ? { shop_id: shopId } : undefined,
      body,
    });
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return await gw<T>("/api/v1/seller/chat/auto-reply", {
        method,
        query: method === "GET" ? { shop_id: shopId } : undefined,
        body,
      });
    }
    throw err;
  }
}

export const GET = apiRoute(async () => {
  const session = await assertSellerSession();
  const raw = await proxyAutoReply<unknown>("GET", session.user.shopId);
  const normalized = normalizeAutoReplyConfig(raw);
  return Response.json(normalized, { headers: { "Cache-Control": "no-store" } });
});

export const PUT = apiRoute(
  async (req) => {
    const session = await assertSellerSession();
    const rawBody = await readJson<unknown>(req);
    const validated = validateAutoReplyInput(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    const { enabled, message } = validated.value;
    const raw = await proxyAutoReply<unknown>("PUT", session.user.shopId, {
      shop_id: session.user.shopId,
      enabled,
      message,
      greeting_message: message,
    });

    const normalized = normalizeAutoReplyConfig(raw);
    return Response.json(
      {
        enabled: normalized.enabled,
        message: normalized.message || message,
        ...(normalized.updated_at ? { updated_at: normalized.updated_at } : {}),
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_AUTO_REPLY: "Vui lòng chọn trạng thái bật hoặc tắt trả lời tự động.",
      EMPTY_AUTO_REPLY: "Vui lòng nhập lời chào tự động khi bật tính năng này.",
      AUTO_REPLY_TOO_LONG: "Tin nhắn trả lời tự động tối đa 500 ký tự.",
    },
  }
);
