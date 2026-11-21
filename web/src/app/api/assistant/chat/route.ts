import { GatewayError } from "@/lib/server/errors";
import { gwRaw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { validateAssistantMessages } from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

interface ChatRequestBody {
  messages?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    const body = await readJson<ChatRequestBody>(req);
    const validated = validateAssistantMessages(body.messages);
    if (!validated.ok) {
      throw new GatewayError("invalid", "INVALID_MESSAGES", validated.reason);
    }

    // Never trust client-supplied shop_id: always scope to the request host's published shop.
    // gwRaw forwards the logged-in buyer's HttpOnly JWT cookie (no elevated service credentials).
    const upstream = await gwRaw("/api/v1/assistant/chat", {
      method: "POST",
      body: {
        messages: validated.messages,
        shop_id: site.shopId,
      },
      signal: req.signal,
    });

    if (!upstream.body) {
      throw new GatewayError("unavailable");
    }

    return new Response(upstream.body, {
      status: 200,
      headers: {
        "Content-Type": "text/event-stream; charset=utf-8",
        "Cache-Control": "no-cache, no-transform",
        Connection: "keep-alive",
        "X-Accel-Buffering": "no",
      },
    });
  },
  {
    messages: {
      INVALID_MESSAGES: "Vui lòng nhập nội dung câu hỏi hợp lệ (tối đa 2.000 ký tự).",
      rate_limited: "Bạn gửi câu hỏi hơi nhanh. Vui lòng chờ một lát rồi thử lại.",
      unavailable:
        "Trợ lý mua sắm đang tạm gián đoạn. Bạn có thể nhắn trực tiếp cho nhân viên gian hàng.",
    },
  }
);
