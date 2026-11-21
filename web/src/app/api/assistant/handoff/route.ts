import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import {
  validateAssistantMessages,
  type AssistantChatInputMessage,
} from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

interface HandoffRequestBody {
  messages?: unknown;
  reason?: unknown;
  conversation_id?: unknown;
}

interface UpstreamHandoffResponse {
  conversation_id?: string;
  summary?: string;
  ticket_id?: string;
  status?: string;
  within_business_hours?: boolean;
  estimated_response?: string;
}

export const POST = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    await assertSession();

    const body = await readJson<HandoffRequestBody>(req);
    let messages: AssistantChatInputMessage[] = [];
    if (Array.isArray(body.messages) && body.messages.length > 0) {
      const validated = validateAssistantMessages(body.messages);
      if (!validated.ok) {
        throw new GatewayError("invalid", "INVALID_MESSAGES", validated.reason);
      }
      messages = validated.messages;
    }

    const reason =
      typeof body.reason === "string"
        ? body.reason.trim().slice(0, 500)
        : "Khách hàng yêu cầu trao đổi trực tiếp với nhân viên gian hàng.";
    const conversationId =
      typeof body.conversation_id === "string" ? body.conversation_id.trim().slice(0, 120) : "";

    // Always inject shop_id from the resolved request site host.
    const data = await gw<UpstreamHandoffResponse>("/api/v1/assistant/handoff", {
      method: "POST",
      body: {
        shop_id: site.shopId,
        messages,
        reason,
        ...(conversationId ? { conversation_id: conversationId } : {}),
      },
    });

    return Response.json(
      {
        conversation_id: data.conversation_id ?? "",
        summary: data.summary ?? "",
        ticket_id: data.ticket_id ?? undefined,
        status: data.status ?? undefined,
        within_business_hours: data.within_business_hours,
        estimated_response: data.estimated_response ?? undefined,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      unauthenticated: "Vui lòng đăng nhập để kết nối trò chuyện với nhân viên gian hàng.",
      rate_limited: "Bạn vừa yêu cầu hỗ trợ. Vui lòng chờ ít phút trước khi gửi lại.",
      unavailable:
        "Chưa thể chuyển tiếp sang nhân viên lúc này. Bạn có thể mở khung chat góc dưới để nhắn trực tiếp.",
    },
  }
);
