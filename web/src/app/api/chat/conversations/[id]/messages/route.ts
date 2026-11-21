import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import {
  mergeMessages,
  normalizeConversation,
  normalizeMessage,
  validateOutgoingMessage,
  type ChatMessageDTO,
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

interface RawListMessagesResponse {
  conversation?: unknown;
  messages?: unknown[];
  next_before?: string;
  latest_cursor?: string;
  has_more?: boolean;
}

interface RawSendMessageResponse {
  message?: unknown;
  auto_reply?: unknown;
}

export const GET = apiRoute<RouteCtx>(
  async (req, ctx) => {
    const site = await requireRequestSite(req);
    await assertSession();
    const { id } = await ctx.params;
    const convId = parseConversationId(id);

    const sp = req.nextUrl.searchParams;
    const before = sp.get("before")?.trim() || undefined;
    const after = sp.get("after")?.trim() || undefined;
    const limit = parseLimit(sp.get("limit"));

    const res = await gw<RawListMessagesResponse>(
      `/api/v1/chat/conversations/${encodeURIComponent(convId)}/messages`,
      { query: { before, after, limit } }
    );

    const conversation = normalizeConversation(res.conversation);
    if (conversation && conversation.shop_id && conversation.shop_id !== site.shopId) {
      throw new GatewayError("not_found");
    }

    const parsed = (res.messages ?? [])
      .map((item) => normalizeMessage(item))
      .filter((m): m is ChatMessageDTO => m !== null);
    const messages = mergeMessages([], parsed);

    return Response.json(
      {
        conversation,
        messages,
        next_before: res.next_before ?? "",
        latest_cursor: res.latest_cursor ?? "",
        has_more: Boolean(res.has_more),
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      not_found: "Không tìm thấy cuộc trò chuyện này.",
      forbidden: "Bạn không có quyền xem cuộc trò chuyện này.",
    },
  }
);

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await requireRequestSite(req);
    await assertSession();
    const { id } = await ctx.params;
    const convId = parseConversationId(id);

    const rawBody = await readJson<unknown>(req);
    const validated = validateOutgoingMessage(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    const payload = validated.value;
    const res = await gw<RawSendMessageResponse>(
      `/api/v1/chat/conversations/${encodeURIComponent(convId)}/messages`,
      {
        method: "POST",
        body: {
          kind: payload.kind,
          body: payload.body,
          ref_id: payload.ref_id,
          ...(payload.client_msg_id ? { client_msg_id: payload.client_msg_id } : {}),
          ...(payload.metadata ? { metadata_json: JSON.stringify(payload.metadata) } : {}),
        },
      }
    );

    const message = normalizeMessage(res.message ?? res);
    const autoReply = res.auto_reply ? normalizeMessage(res.auto_reply) : null;

    return Response.json(
      { message, ...(autoReply ? { auto_reply: autoReply } : {}) },
      { status: 201, headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      EMPTY_MESSAGE: "Vui lòng nhập nội dung tin nhắn trước khi gửi.",
      MESSAGE_TOO_LONG: "Tin nhắn không được vượt quá 2.000 ký tự.",
      INVALID_IMAGE_URL:
        "Đường dẫn ảnh không hợp lệ. Chỉ chấp nhận ảnh nội bộ hoặc liên kết HTTPS.",
      MISSING_REF_ID: "Vui lòng chọn sản phẩm hoặc đơn hàng hợp lệ trước khi gửi.",
      INVALID_KIND: "Loại tin nhắn không được hỗ trợ.",
      not_found: "Không tìm thấy cuộc trò chuyện này.",
      forbidden: "Bạn không có quyền gửi tin nhắn vào cuộc trò chuyện này.",
    },
  }
);
