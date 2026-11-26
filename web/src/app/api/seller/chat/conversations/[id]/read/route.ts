import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { MAX_BFF_BODY_BYTES, readBoundedBody } from "@/lib/server/proxy-security";
import { parseOptionalMarkReadBody } from "@/features/seller/sellerReturns";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { fetchShopConversationMessages, parseConversationId } from "@/app/api/chat/_conversation";

type RouteCtx = { params: Promise<{ id: string }> };

interface RawMarkReadResponse {
  conversation_id?: string;
  last_read_seq?: number;
  unread_count?: number;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    const session = await assertSellerSession();
    const { id } = await ctx.params;
    const convId = parseConversationId(id);

    const bounded = await readBoundedBody(req, MAX_BFF_BODY_BYTES);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }
    const validated = parseOptionalMarkReadBody(bounded.body);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    await fetchShopConversationMessages(convId, session.user.shopId, { limit: 1 });

    const res = await gw<RawMarkReadResponse>(
      `/api/v1/chat/conversations/${encodeURIComponent(convId)}/read`,
      {
        method: "POST",
        body: validated.value,
      }
    );

    return Response.json(
      {
        conversation_id: res.conversation_id ?? convId,
        last_read_seq: res.last_read_seq ?? validated.value.last_read_seq ?? 0,
        unread_count: res.unread_count ?? 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_READ_PAYLOAD: "Dữ liệu đánh dấu đã đọc không hợp lệ.",
      not_found: "Không tìm thấy hội thoại khách hàng này.",
      forbidden: "Bạn không có quyền cập nhật hội thoại này.",
    },
  }
);
