import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { normalizeSuggestReply } from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

interface SuggestReplyRequestBody {
  conversation_id?: unknown;
  messages?: unknown;
  product_name?: unknown;
  product_id?: unknown;
  customer_message?: unknown;
}

interface NormalizedChatEntry {
  role: "buyer" | "seller" | "user" | "assistant";
  content: string;
}

function sanitizeMessages(raw: unknown): NormalizedChatEntry[] {
  if (!Array.isArray(raw)) return [];
  const out: NormalizedChatEntry[] = [];
  for (const item of raw.slice(-30)) {
    if (!item || typeof item !== "object") continue;
    const obj = item as Record<string, unknown>;
    const rawRole = String(obj.role ?? obj.sender ?? "buyer")
      .trim()
      .toLowerCase();
    const role: NormalizedChatEntry["role"] =
      rawRole === "seller" || rawRole === "assistant"
        ? rawRole
        : rawRole === "user"
          ? "user"
          : "buyer";
    const content = String(obj.content ?? obj.body ?? "")
      .trim()
      .slice(0, 1500);
    if (!content) continue;
    out.push({ role, content });
  }
  return out;
}

export const POST = apiRoute(
  async (req) => {
    const session = await assertSellerSession();
    const body = await readJson<SuggestReplyRequestBody>(req);

    const conversationId =
      typeof body.conversation_id === "string" ? body.conversation_id.trim().slice(0, 120) : "";
    const productName =
      typeof body.product_name === "string" ? body.product_name.trim().slice(0, 200) : "";
    const productId =
      typeof body.product_id === "string" ? body.product_id.trim().slice(0, 120) : "";
    const messages = sanitizeMessages(body.messages);
    const explicitCustomerMsg =
      typeof body.customer_message === "string" ? body.customer_message.trim().slice(0, 1500) : "";

    const lastCustomerMessage =
      explicitCustomerMsg ||
      [...messages].reverse().find((m) => m.role === "buyer" || m.role === "user")?.content ||
      messages[messages.length - 1]?.content ||
      "";

    if (!conversationId && messages.length === 0 && !lastCustomerMessage) {
      throw new GatewayError("invalid", "INVALID_SUGGEST_REPLY_INPUT");
    }

    const upstream = await gw<unknown>("/api/v1/seller/assistant/suggest-reply", {
      method: "POST",
      body: {
        conversation_id: conversationId,
        messages,
        product_name: productName,
        ...(productId ? { product_id: productId } : {}),
        ...(lastCustomerMessage ? { customer_message: lastCustomerMessage } : {}),
        history: messages.map((m) => ({
          sender: m.role === "seller" || m.role === "assistant" ? "seller" : "buyer",
          body: m.content,
        })),
        shop_id: session.user.shopId,
      },
      timeoutMs: 30_000,
    });

    const normalized = normalizeSuggestReply(upstream);
    return Response.json(normalized, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_SUGGEST_REPLY_INPUT: "Cần có nội dung hội thoại để tạo gợi ý trả lời.",
      rate_limited: "Bạn đang dùng gợi ý trả lời hơi nhanh. Vui lòng thử lại sau ít phút.",
      unavailable: "Dịch vụ gợi ý trả lời AI tạm thời chưa sẵn sàng.",
    },
  }
);
