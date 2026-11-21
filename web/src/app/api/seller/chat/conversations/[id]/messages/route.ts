import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  buildGatewaySendMessageBody,
  normalizeMessage,
  parseGatewayMessagesResponse,
  validateOutgoingMessage,
} from "@/features/chat/chatTypes";

type RouteCtx = { params: Promise<{ id: string }> };

function parseConversationId(rawId: string): string {
  const clean = rawId.trim();
  if (!clean || clean.length > 128 || /[\s/]/.test(clean)) {
    throw new GatewayError("not_found");
  }
  return clean;
}

function parseLimit(raw: string | null): number | undefined {
  if (!raw) return undefined;
  const n = Number.parseInt(raw, 10);
  if (!Number.isFinite(n) || n < 1) return undefined;
  return Math.min(100, n);
}

interface RawSendMessageResponse {
  message?: unknown;
  auto_reply?: unknown;
}

export const GET = apiRoute<RouteCtx>(
  async (req, ctx) => {
    const session = await assertSellerSession();
    const { id } = await ctx.params;
    const convId = parseConversationId(id);

    const sp = req.nextUrl.searchParams;
    const before = sp.get("before")?.trim() || undefined;
    const after = sp.get("after")?.trim() || undefined;
    const limit = parseLimit(sp.get("limit"));

    const res = await gw<unknown>(
      `/api/v1/chat/conversations/${encodeURIComponent(convId)}/messages`,
      { query: { before, after, limit } }
    );

    const parsed = parseGatewayMessagesResponse(res);
    if (
      parsed.conversation &&
      parsed.conversation.shop_id &&
      parsed.conversation.shop_id !== session.user.shopId
    ) {
      throw new GatewayError("not_found");
    }

    return Response.json(parsed, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      not_found: "Không tìm thấy hội thoại khách hàng này.",
      forbidden: "Bạn không có quyền xem hội thoại này.",
    },
  }
);

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const convId = parseConversationId(id);

    const rawBody = await readJson<unknown>(req);
    const validated = validateOutgoingMessage(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    const res = await gw<RawSendMessageResponse>(
      `/api/v1/chat/conversations/${encodeURIComponent(convId)}/messages`,
      {
        method: "POST",
        body: buildGatewaySendMessageBody(validated.value),
      }
    );

    const message = normalizeMessage(res.message);
    const autoReply = res.auto_reply ? normalizeMessage(res.auto_reply) : null;

    return Response.json(
      { message, ...(autoReply ? { auto_reply: autoReply } : {}) },
      { status: 201, headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      EMPTY_MESSAGE: "Vui lòng nhập nội dung phản hồi trước khi gửi.",
      MESSAGE_TOO_LONG: "Tin nhắn không được vượt quá 2.000 ký tự.",
      INVALID_IMAGE_URL:
        "Đường dẫn ảnh không hợp lệ. Chỉ chấp nhận ảnh nội bộ hoặc liên kết HTTPS.",
      MISSING_REF_ID: "Vui lòng nhập mã sản phẩm hoặc mã đơn hàng trước khi gửi.",
      INVALID_KIND: "Loại tin nhắn không được hỗ trợ.",
      not_found: "Không tìm thấy hội thoại khách hàng này.",
      forbidden: "Bạn không có quyền phản hồi hội thoại này.",
    },
  }
);
