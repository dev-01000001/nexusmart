// Pure chat types, DTO normalizers, message validation, UC-B14 off-platform guard,
// and WebSocket reconnect/frame helpers. Zero Next.js imports so Node 22 tests run directly.
import { sanitizeSafeUrl } from "../../lib/site/theme.ts";

export type ChatMessageKind = "text" | "image" | "product" | "order";
export type ChatSenderRole = "buyer" | "seller" | "system";
export type ChatConnectionStatus = "idle" | "connecting" | "connected" | "reconnecting" | "offline";

export const ALLOWED_CHAT_MESSAGE_KINDS: readonly ChatMessageKind[] = [
  "text",
  "image",
  "product",
  "order",
];

export const MAX_CHAT_BODY_LENGTH = 2000;
export const MAX_CHAT_REF_ID_LENGTH = 120;
export const MAX_AUTO_REPLY_LENGTH = 500;
export const SELLER_AUTO_REPLY_GATEWAY_PATH = "/api/v1/seller/chat/auto-reply";

export const OPEN_SITE_CHAT_EVENT = "nexusmart:open-chat";

export interface OpenSiteChatDetail {
  conversationId?: string;
}

export function dispatchOpenSiteChat(detail: OpenSiteChatDetail = {}): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent<OpenSiteChatDetail>(OPEN_SITE_CHAT_EVENT, { detail }));
}

export interface ChatMessageDTO {
  id: string;
  conversation_id: string;
  sender_id: string;
  sender_role?: ChatSenderRole;
  sender_name?: string;
  kind: ChatMessageKind;
  body: string;
  ref_id: string;
  seq?: number;
  is_auto_reply?: boolean;
  client_msg_id?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface ChatConversationDTO {
  id: string;
  buyer_id: string;
  buyer_name: string;
  shop_id: string;
  shop_name: string;
  site_host: string;
  last_message_id: string;
  last_message_seq: number;
  last_message_preview: string;
  last_message_sender_role: string;
  last_message_at: string;
  unread_count: number;
  created_at: string;
  updated_at: string;
}

export interface ChatAutoReplyConfigDTO {
  enabled: boolean;
  message: string;
  offline_enabled?: boolean;
  offline_message?: string;
  updated_at?: string;
}

export interface WsTicketDTO {
  ticket: string;
  ws_url: string;
  expires_at: string;
}

export type WsIncomingEvent =
  | { type: "message"; conversation_id: string; message: ChatMessageDTO }
  | { type: "typing"; conversation_id: string; sender_id: string; is_typing: boolean }
  | {
      type: "read";
      conversation_id: string;
      reader_id: string;
      last_read_seq?: number;
      message_id?: string;
    }
  | {
      type: "ack";
      conversation_id: string;
      message_id: string;
      client_msg_id?: string;
      seq?: number;
      message?: ChatMessageDTO;
    }
  | { type: "error"; code: string; message: string };

export interface SiteChatProductContext {
  id: string;
  slug?: string;
  name: string;
  priceVnd: number;
  image?: string;
}

export interface SiteChatOrderContext {
  id: string;
  orderCode: string;
}

export interface SiteChatWidgetProps {
  shopId: string;
  brandName: string;
  productContext?: SiteChatProductContext;
  orderContext?: SiteChatOrderContext;
  /** Khi truyền từ ngoài (vd: sau khi handoff từ trợ lý), tự mở tới hội thoại này. */
  externalConversationId?: string | null;
  defaultOpen?: boolean;
  /** Vị trí nổi mặc định tránh chồng với SiteAssistantWidget. */
  offsetBottomPx?: number;
  className?: string;
}

export interface OutgoingMessageInput {
  kind?: string;
  body?: string;
  ref_id?: string;
  client_msg_id?: string;
  metadata?: Record<string, unknown>;
}

export interface ValidatedOutgoingMessage {
  kind: ChatMessageKind;
  body: string;
  ref_id: string;
  client_msg_id?: string;
  metadata?: Record<string, unknown>;
}

export type ValidationResult<T> =
  { ok: true; value: T } | { ok: false; code: string; message: string };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readStr(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function readNum(value: unknown, fallback = 0): number {
  if (typeof value === "number" && Number.isFinite(value)) return Math.trunc(value);
  if (typeof value === "string" && /^\d+$/.test(value.trim())) {
    const parsed = Number.parseInt(value.trim(), 10);
    return Number.isFinite(parsed) ? parsed : fallback;
  }
  return fallback;
}

function readIsoTimestamp(value: unknown): string {
  if (typeof value === "string") return value.trim();
  if (isRecord(value) && typeof value.seconds === "number") {
    const ms = value.seconds * 1000 + Math.trunc(readNum(value.nanos, 0) / 1_000_000);
    const d = new Date(ms);
    if (!Number.isNaN(d.getTime())) return d.toISOString();
  }
  return "";
}

function parseMetadata(raw: unknown): Record<string, unknown> | undefined {
  if (!isRecord(raw)) return undefined;
  return Object.keys(raw).length > 0 ? raw : undefined;
}

export function normalizeMessageKind(raw: unknown): ChatMessageKind {
  if (typeof raw === "number") {
    if (raw === 2) return "image";
    if (raw === 3) return "product";
    if (raw === 4) return "order";
    return "text";
  }
  if (typeof raw !== "string") return "text";
  const clean = raw.trim().toLowerCase();
  if (clean === "image" || clean === "message_kind_image") return "image";
  if (clean === "product" || clean === "message_kind_product") return "product";
  if (clean === "order" || clean === "message_kind_order") return "order";
  return "text";
}

function normalizeSenderRole(raw: unknown): ChatSenderRole | undefined {
  if (typeof raw !== "string") return undefined;
  const clean = raw.trim().toLowerCase();
  if (clean === "buyer" || clean === "seller" || clean === "system") return clean;
  return undefined;
}

export function normalizeMessage(raw: unknown): ChatMessageDTO | null {
  if (!isRecord(raw)) return null;
  const id = readStr(raw.id).trim();
  const conversationId = readStr(raw.conversation_id ?? raw.conversationId).trim();
  const body = readStr(raw.body ?? raw.text);
  const refId = readStr(raw.ref_id ?? raw.refId).trim();
  if (!id && !body && !refId) return null;

  const senderId = readStr(raw.sender_id ?? raw.senderId).trim();
  const senderRole = normalizeSenderRole(raw.sender_role ?? raw.senderRole);
  const senderName = readStr(raw.sender_name ?? raw.senderName).trim() || undefined;
  const kind = normalizeMessageKind(raw.kind);
  const seqVal = readNum(raw.seq, 0);
  const seq = seqVal > 0 ? seqVal : undefined;
  const isAutoReply = Boolean(raw.is_auto_reply ?? raw.isAutoReply);
  const clientMsgId = readStr(raw.client_msg_id ?? raw.clientMsgId).trim() || undefined;
  const metadata = parseMetadata(raw.metadata);
  const createdAt = readIsoTimestamp(raw.created_at ?? raw.createdAt) || new Date(0).toISOString();

  return {
    id: id || clientMsgId || `msg-${createdAt}`,
    conversation_id: conversationId,
    sender_id: senderId,
    ...(senderRole ? { sender_role: senderRole } : {}),
    ...(senderName ? { sender_name: senderName } : {}),
    kind,
    body,
    ref_id: refId,
    ...(seq !== undefined ? { seq } : {}),
    ...(isAutoReply ? { is_auto_reply: true } : {}),
    ...(clientMsgId ? { client_msg_id: clientMsgId } : {}),
    ...(metadata ? { metadata } : {}),
    created_at: createdAt,
  };
}

export function normalizeConversation(raw: unknown): ChatConversationDTO | null {
  if (!isRecord(raw)) return null;
  const id = readStr(raw.id).trim();
  if (!id) return null;

  const buyerId = readStr(raw.buyer_id ?? raw.buyerId).trim();
  const buyerName =
    readStr(raw.buyer_name ?? raw.buyerName).trim() ||
    (buyerId ? `Khách hàng #${buyerId.slice(0, 6)}` : "Khách hàng");
  const shopId = readStr(raw.shop_id ?? raw.shopId).trim();
  const shopName = readStr(raw.shop_name ?? raw.shopName).trim();
  const siteHost = readStr(raw.site_host ?? raw.siteHost).trim();
  const lastMsgObj = isRecord(raw.last_message) ? raw.last_message : null;
  const lastMessageId = readStr(raw.last_message_id ?? raw.lastMessageId ?? lastMsgObj?.id).trim();
  const lastMessageSeq = readNum(raw.last_message_seq ?? raw.lastMessageSeq, 0);
  const lastMessagePreview = readStr(
    raw.last_message_preview ?? raw.lastMessagePreview ?? lastMsgObj?.body
  ).trim();
  const lastMessageSenderRole = readStr(
    raw.last_message_sender_role ?? raw.lastMessageSenderRole ?? lastMsgObj?.sender_role
  ).trim();
  const createdAt = readIsoTimestamp(raw.created_at ?? raw.createdAt);
  const updatedAt = readIsoTimestamp(raw.updated_at ?? raw.updatedAt) || createdAt;
  const lastMessageAt =
    readIsoTimestamp(raw.last_message_at ?? raw.lastMessageAt ?? lastMsgObj?.created_at) ||
    updatedAt;
  const unreadCount = Math.max(0, readNum(raw.unread_count ?? raw.unreadCount ?? raw.unread, 0));

  return {
    id,
    buyer_id: buyerId,
    buyer_name: buyerName,
    shop_id: shopId,
    shop_name: shopName,
    site_host: siteHost,
    last_message_id: lastMessageId,
    last_message_seq: lastMessageSeq,
    last_message_preview: lastMessagePreview,
    last_message_sender_role: lastMessageSenderRole,
    last_message_at: lastMessageAt,
    unread_count: unreadCount,
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function messageSortTimestamp(msg: ChatMessageDTO): number {
  const t = Date.parse(msg.created_at);
  return Number.isNaN(t) ? 0 : t;
}

/**
 * Merge message lists: deduplicates by `id` and reconciles optimistic `client_msg_id` entries,
 * returning messages sorted ascending by `created_at` (tie-broken by `seq` then `id`).
 */
export function mergeMessages(
  existing: readonly ChatMessageDTO[],
  incoming: readonly ChatMessageDTO[]
): ChatMessageDTO[] {
  const byId = new Map<string, ChatMessageDTO>();
  const clientIdToKey = new Map<string, string>();

  const upsert = (msg: ChatMessageDTO) => {
    if (msg.client_msg_id && clientIdToKey.has(msg.client_msg_id)) {
      const prevKey = clientIdToKey.get(msg.client_msg_id)!;
      if (prevKey !== msg.id) {
        byId.delete(prevKey);
      }
    }
    byId.set(msg.id, msg);
    if (msg.client_msg_id) {
      clientIdToKey.set(msg.client_msg_id, msg.id);
    }
  };

  for (const msg of existing) upsert(msg);
  for (const msg of incoming) upsert(msg);

  return [...byId.values()].sort((a, b) => {
    const timeDiff = messageSortTimestamp(a) - messageSortTimestamp(b);
    if (timeDiff !== 0) return timeDiff;
    const seqDiff = (a.seq ?? 0) - (b.seq ?? 0);
    if (seqDiff !== 0) return seqDiff;
    return a.id.localeCompare(b.id);
  });
}

export function validateOutgoingMessage(
  input: unknown
): ValidationResult<ValidatedOutgoingMessage> {
  if (!isRecord(input)) {
    return { ok: false, code: "INVALID_PAYLOAD", message: "Nội dung tin nhắn không hợp lệ." };
  }

  const rawKind = input.kind === undefined || input.kind === "" ? "text" : input.kind;
  if (
    typeof rawKind !== "string" ||
    !(ALLOWED_CHAT_MESSAGE_KINDS as readonly string[]).includes(rawKind.trim().toLowerCase())
  ) {
    return { ok: false, code: "INVALID_KIND", message: "Loại tin nhắn không được hỗ trợ." };
  }
  const kind = rawKind.trim().toLowerCase() as ChatMessageKind;

  const rawBody = typeof input.body === "string" ? input.body.trim() : "";
  const rawRefId = typeof input.ref_id === "string" ? input.ref_id.trim() : "";
  const clientMsgId =
    typeof input.client_msg_id === "string" && input.client_msg_id.trim().length > 0
      ? input.client_msg_id.trim().slice(0, 100)
      : undefined;
  const metadata = isRecord(input.metadata) ? input.metadata : undefined;

  if (rawBody.length > MAX_CHAT_BODY_LENGTH) {
    return {
      ok: false,
      code: "MESSAGE_TOO_LONG",
      message: `Tin nhắn không được vượt quá ${MAX_CHAT_BODY_LENGTH.toLocaleString("vi-VN")} ký tự.`,
    };
  }

  if (rawRefId.length > MAX_CHAT_REF_ID_LENGTH) {
    return {
      ok: false,
      code: "INVALID_REF_ID",
      message: "Mã tham chiếu sản phẩm hoặc đơn hàng quá dài.",
    };
  }

  if (kind === "text") {
    if (!rawBody) {
      return {
        ok: false,
        code: "EMPTY_MESSAGE",
        message: "Vui lòng nhập nội dung tin nhắn trước khi gửi.",
      };
    }
    return {
      ok: true,
      value: {
        kind,
        body: rawBody,
        ref_id: rawRefId,
        ...(clientMsgId ? { client_msg_id: clientMsgId } : {}),
        ...(metadata ? { metadata } : {}),
      },
    };
  }

  if (kind === "image") {
    const safeImageUrl = sanitizeSafeUrl(rawBody);
    if (!safeImageUrl) {
      return {
        ok: false,
        code: "INVALID_IMAGE_URL",
        message: "Đường dẫn ảnh không hợp lệ. Chỉ chấp nhận ảnh nội bộ hoặc liên kết HTTPS.",
      };
    }
    return {
      ok: true,
      value: {
        kind,
        body: safeImageUrl,
        ref_id: rawRefId,
        ...(clientMsgId ? { client_msg_id: clientMsgId } : {}),
        ...(metadata ? { metadata } : {}),
      },
    };
  }

  // kind === "product" | "order"
  if (!rawRefId) {
    return {
      ok: false,
      code: "MISSING_REF_ID",
      message:
        kind === "product"
          ? "Vui lòng chọn hoặc nhập mã sản phẩm cần gửi."
          : "Vui lòng nhập mã đơn hàng cần gửi.",
    };
  }

  const fallbackBody = kind === "product" ? `Sản phẩm ${rawRefId}` : `Đơn hàng ${rawRefId}`;
  return {
    ok: true,
    value: {
      kind,
      body: rawBody || fallbackBody,
      ref_id: rawRefId,
      ...(clientMsgId ? { client_msg_id: clientMsgId } : {}),
      ...(metadata ? { metadata } : {}),
    },
  };
}

export function validateAutoReplyInput(
  input: unknown
): ValidationResult<{ enabled: boolean; message: string }> {
  if (!isRecord(input) || typeof input.enabled !== "boolean") {
    return {
      ok: false,
      code: "INVALID_AUTO_REPLY",
      message: "Vui lòng chọn trạng thái bật hoặc tắt trả lời tự động.",
    };
  }
  const message = typeof input.message === "string" ? input.message.trim() : "";
  if (message.length > MAX_AUTO_REPLY_LENGTH) {
    return {
      ok: false,
      code: "AUTO_REPLY_TOO_LONG",
      message: `Tin nhắn trả lời tự động tối đa ${MAX_AUTO_REPLY_LENGTH} ký tự.`,
    };
  }
  if (input.enabled && !message) {
    return {
      ok: false,
      code: "EMPTY_AUTO_REPLY",
      message: "Vui lòng nhập lời chào tự động khi bật tính năng này.",
    };
  }
  return { ok: true, value: { enabled: input.enabled, message } };
}

export function parseGatewayConversationsResponse(
  raw: unknown,
  shopId: string,
  opts: { allowEmptyShopId?: boolean } = {}
): { conversations: ChatConversationDTO[]; total: number; total_unread: number } {
  const rec = isRecord(raw) ? raw : {};
  const rawItems = Array.isArray(rec.items) ? rec.items : [];
  const conversations = rawItems
    .map((item) => normalizeConversation(item))
    .filter(
      (c): c is ChatConversationDTO =>
        c !== null && (c.shop_id === shopId || (Boolean(opts.allowEmptyShopId) && !c.shop_id))
    );
  const total = typeof rec.total === "number" ? rec.total : conversations.length;
  const totalUnread =
    typeof rec.total_unread === "number"
      ? rec.total_unread
      : conversations.reduce((sum, c) => sum + c.unread_count, 0);
  return { conversations, total, total_unread: totalUnread };
}

export function parseGatewayMessagesResponse(raw: unknown): {
  conversation: ChatConversationDTO | null;
  messages: ChatMessageDTO[];
  next_before: string;
  latest_cursor: string;
  has_more: boolean;
} {
  const rec = isRecord(raw) ? raw : {};
  const conversation = normalizeConversation(rec.conversation);
  const rawItems = Array.isArray(rec.items) ? rec.items : [];
  const parsed = rawItems
    .map((item) => normalizeMessage(item))
    .filter((m): m is ChatMessageDTO => m !== null);
  return {
    conversation,
    messages: mergeMessages([], parsed),
    next_before: readStr(rec.next_before),
    latest_cursor: readStr(rec.latest_cursor),
    has_more: Boolean(rec.has_more),
  };
}

export function buildGatewaySendMessageBody(
  payload: ValidatedOutgoingMessage
): Record<string, unknown> {
  return {
    kind: payload.kind,
    body: payload.body,
    ref_id: payload.ref_id,
    ...(payload.client_msg_id ? { client_msg_id: payload.client_msg_id } : {}),
    ...(payload.metadata ? { metadata: payload.metadata } : {}),
  };
}

export function buildGatewayAutoReplyBody(input: { enabled: boolean; message: string }): {
  enabled: boolean;
  greeting_message: string;
} {
  return {
    enabled: input.enabled,
    greeting_message: input.message,
  };
}

export function normalizeAutoReplyConfig(raw: unknown): ChatAutoReplyConfigDTO {
  if (!isRecord(raw) || !isRecord(raw.config)) {
    return { enabled: false, message: "" };
  }
  const src = raw.config;
  const enabled = Boolean(src.enabled);
  const message = readStr(src.greeting_message).trim();
  const offlineEnabled = typeof src.offline_enabled === "boolean" ? src.offline_enabled : undefined;
  const offlineMessage = readStr(src.offline_message).trim() || undefined;
  const updatedAt = readIsoTimestamp(src.updated_at) || undefined;
  return {
    enabled,
    message,
    ...(offlineEnabled !== undefined ? { offline_enabled: offlineEnabled } : {}),
    ...(offlineMessage ? { offline_message: offlineMessage } : {}),
    ...(updatedAt ? { updated_at: updatedAt } : {}),
  };
}

export interface OffPlatformContactCheck {
  flagged: boolean;
  reasons: ("phone" | "external_link")[];
}

// Vietnamese mobile (03x/05x/07x/08x/09x) & landline (02x) or +84/0084 prefixed numbers
const VN_PHONE_RE =
  /(?:^|[^\d])(?:(?:\+|00)84[\s.-]?|0)(?:(?:3|5|7|8|9)\d(?:[\s.-]?\d){7}|2\d(?:[\s.-]?\d){8})(?:$|[^\d])/;

const URL_MATCH_RE = /\bhttps?:\/\/[^\s<>"']+/gi;
const EXTERNAL_SHORT_HOST_RE =
  /\b(?:www\.|zalo\.me\/|fb\.com\/|fb\.me\/|m\.me\/|facebook\.com\/|t\.me\/|telegram\.me\/|bit\.ly\/|tinyurl\.com\/|shopee\.vn\/|lazada\.vn\/|tiki\.vn\/)[^\s<>"']*/i;

function isTrustedPlatformHost(hostname: string): boolean {
  const lower = hostname.toLowerCase();
  return (
    lower === "nexusmart.vn" ||
    lower.endsWith(".nexusmart.vn") ||
    lower === "nexusmart.localhost" ||
    lower.endsWith(".nexusmart.localhost") ||
    lower === "localhost"
  );
}

/**
 * UC-B14: Detect Vietnamese phone numbers or external off-platform links in chat text
 * so the UI displays a safety warning against off-platform transactions.
 */
export function detectOffPlatformContact(text: string): OffPlatformContactCheck {
  const reasons: ("phone" | "external_link")[] = [];
  if (!text || !text.trim()) {
    return { flagged: false, reasons };
  }

  if (VN_PHONE_RE.test(text)) {
    reasons.push("phone");
  }

  let hasExternalLink = false;
  const urlMatches = text.match(URL_MATCH_RE) ?? [];
  for (const rawUrl of urlMatches) {
    try {
      const parsed = new URL(rawUrl);
      if (!isTrustedPlatformHost(parsed.hostname)) {
        hasExternalLink = true;
        break;
      }
    } catch {
      hasExternalLink = true;
      break;
    }
  }

  if (!hasExternalLink && EXTERNAL_SHORT_HOST_RE.test(text)) {
    hasExternalLink = true;
  }

  if (hasExternalLink) {
    reasons.push("external_link");
  }

  return { flagged: reasons.length > 0, reasons };
}

/**
 * Capped exponential backoff for WebSocket reconnects:
 * 1s -> 2s -> 4s -> 8s -> 15s (max).
 */
export function computeReconnectDelayMs(attempt: number): number {
  const safeAttempt = Number.isFinite(attempt) && attempt > 0 ? Math.floor(attempt) : 0;
  const raw = 1000 * Math.pow(2, Math.min(safeAttempt, 6));
  return Math.min(15_000, raw);
}

export function parseWsIncomingEvent(raw: unknown): WsIncomingEvent | null {
  let obj: unknown = raw;
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) return null;
    try {
      obj = JSON.parse(trimmed);
    } catch {
      return null;
    }
  }
  if (!isRecord(obj)) return null;

  const type = readStr(obj.type ?? obj.event)
    .trim()
    .toLowerCase();
  if (type === "message" || type === "new_message" || type === "chat.message") {
    const msg = normalizeMessage(obj.message ?? obj.payload ?? obj);
    if (!msg) return null;
    const convId = msg.conversation_id || readStr(obj.conversation_id ?? obj.conversationId).trim();
    return {
      type: "message",
      conversation_id: convId,
      message: { ...msg, conversation_id: convId },
    };
  }

  if (type === "typing") {
    const convId = readStr(obj.conversation_id ?? obj.conversationId).trim();
    if (!convId) return null;
    return {
      type: "typing",
      conversation_id: convId,
      sender_id: readStr(obj.sender_id ?? obj.senderId ?? obj.user_id).trim(),
      is_typing: obj.is_typing !== false,
    };
  }

  if (type === "read") {
    const convId = readStr(obj.conversation_id ?? obj.conversationId).trim();
    if (!convId) return null;
    const seq = readNum(obj.last_read_seq ?? obj.lastReadSeq, 0);
    const msgId = readStr(obj.message_id ?? obj.messageId).trim() || undefined;
    return {
      type: "read",
      conversation_id: convId,
      reader_id: readStr(obj.reader_id ?? obj.user_id ?? obj.sender_id).trim(),
      ...(seq > 0 ? { last_read_seq: seq } : {}),
      ...(msgId ? { message_id: msgId } : {}),
    };
  }

  if (type === "ack") {
    const embedded = obj.message ? normalizeMessage(obj.message) : null;
    const convId =
      readStr(obj.conversation_id ?? obj.conversationId).trim() || embedded?.conversation_id || "";
    const messageId =
      readStr(obj.message_id ?? obj.messageId ?? obj.id).trim() || embedded?.id || "";
    if (!messageId) return null;
    const clientMsgId =
      readStr(obj.client_msg_id ?? obj.clientMsgId).trim() || embedded?.client_msg_id || undefined;
    const seqVal = readNum(obj.seq, embedded?.seq ?? 0);
    return {
      type: "ack",
      conversation_id: convId,
      message_id: messageId,
      ...(clientMsgId ? { client_msg_id: clientMsgId } : {}),
      ...(seqVal > 0 ? { seq: seqVal } : {}),
      ...(embedded ? { message: embedded } : {}),
    };
  }

  if (type === "error") {
    return {
      type: "error",
      code: readStr(obj.code, "WS_ERROR").trim() || "WS_ERROR",
      message: readStr(obj.message, "Không thể xử lý tin nhắn lúc này.").trim(),
    };
  }

  return null;
}

export interface ParsedProductRefCard {
  name: string;
  priceVnd?: number;
  slug?: string;
  image?: string;
}

export function parseProductRefCard(msg: ChatMessageDTO): ParsedProductRefCard {
  const meta = msg.metadata;
  if (meta) {
    const name = readStr(meta.name).trim() || msg.body || `Sản phẩm ${msg.ref_id}`;
    const price = readNum(meta.price_vnd ?? meta.priceVnd, -1);
    const slug = readStr(meta.slug).trim() || undefined;
    const image = sanitizeSafeUrl(readStr(meta.image)) || undefined;
    return {
      name,
      ...(price >= 0 ? { priceVnd: price } : {}),
      ...(slug ? { slug } : {}),
      ...(image ? { image } : {}),
    };
  }
  if (msg.body.startsWith("{")) {
    try {
      const parsed = JSON.parse(msg.body);
      if (isRecord(parsed)) {
        const name = readStr(parsed.name).trim() || `Sản phẩm ${msg.ref_id}`;
        const price = readNum(parsed.price_vnd ?? parsed.priceVnd, -1);
        const slug = readStr(parsed.slug).trim() || undefined;
        const image = sanitizeSafeUrl(readStr(parsed.image)) || undefined;
        return {
          name,
          ...(price >= 0 ? { priceVnd: price } : {}),
          ...(slug ? { slug } : {}),
          ...(image ? { image } : {}),
        };
      }
    } catch {
      // fall through to plain body
    }
  }
  return { name: msg.body || `Sản phẩm ${msg.ref_id}` };
}

export function parseOrderRefCard(msg: ChatMessageDTO): { label: string; orderCode: string } {
  const meta = msg.metadata;
  const codeFromMeta = meta ? readStr(meta.order_code ?? meta.orderCode).trim() : "";
  const orderCode = codeFromMeta || msg.ref_id;
  const label = msg.body || `Đơn hàng ${orderCode}`;
  return { label, orderCode };
}
