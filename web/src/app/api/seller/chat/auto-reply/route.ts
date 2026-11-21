import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  SELLER_AUTO_REPLY_GATEWAY_PATH,
  buildGatewayAutoReplyBody,
  normalizeAutoReplyConfig,
  validateAutoReplyInput,
} from "@/features/chat/chatTypes";

export const GET = apiRoute(async () => {
  await assertSellerSession();
  const raw = await gw<unknown>(SELLER_AUTO_REPLY_GATEWAY_PATH);
  const normalized = normalizeAutoReplyConfig(raw);
  return Response.json(normalized, { headers: { "Cache-Control": "no-store" } });
});

export const PUT = apiRoute(
  async (req) => {
    await assertSellerSession();
    const rawBody = await readJson<unknown>(req);
    const validated = validateAutoReplyInput(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    const raw = await gw<unknown>(SELLER_AUTO_REPLY_GATEWAY_PATH, {
      method: "PUT",
      body: buildGatewayAutoReplyBody(validated.value),
    });

    const normalized = normalizeAutoReplyConfig(raw);
    return Response.json(
      {
        enabled: normalized.enabled,
        message: normalized.message || validated.value.message,
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
