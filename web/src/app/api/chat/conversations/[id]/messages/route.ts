import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import {
  buildGatewaySendMessageBody,
  normalizeMessage,
  validateOutgoingMessage,
} from "@/features/chat/chatTypes";
import {
  fetchShopConversationMessages,
  parseConversationId,
  parseMessageLimit,
} from "@/app/api/chat/_conversation";

type RouteCtx = { params: Promise<{ id: string }> };

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
    const limit = parseMessageLimit(sp.get("limit"));

    const parsed = await fetchShopConversationMessages(convId, site.shopId, {
      before,
      after,
      limit,
    });

    return Response.json(parsed, { headers: { "Cache-Control": "no-store" } });
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
    const site = await requireRequestSite(req);
    await assertSession();
    const { id } = await ctx.params;
    const convId = parseConversationId(id);

    const rawBody = await readJson<unknown>(req);
    const validated = validateOutgoingMessage(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    await fetchShopConversationMessages(convId, site.shopId, { limit: 1 });

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
