"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  AlertTriangle,
  ArrowLeft,
  ExternalLink,
  Image as ImageIcon,
  MessageSquare,
  Package,
  RefreshCw,
  Search,
  Send,
  ShoppingBag,
  Sparkles,
  User,
} from "lucide-react";
import { EmptyState, ErrorState, PageHeader, StatusBadge, type StatusTone } from "@/components/app";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatTimeVi, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { cn } from "@/lib/utils";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
import {
  SELLER_CHAT_READ_EVENT,
  markSellerConversationRead,
  shouldClearUnreadAfterReadSync,
  type ReadSyncResult,
  type SellerChatReadEventDetail,
} from "@/features/seller/sellerReturns";
import { createChatSocketManager, type ChatSocketManager } from "@/features/chat/chatClient";
import {
  detectOffPlatformContact,
  mergeMessages,
  normalizeConversation,
  normalizeMessage,
  parseOrderRefCard,
  parseProductRefCard,
  validateOutgoingMessage,
  type ChatConnectionStatus,
  type ChatConversationDTO,
  type ChatMessageDTO,
  type ChatMessageKind,
  type WsIncomingEvent,
} from "@/features/chat/chatTypes";

const QUICK_REPLIES = [
  {
    label: "Tư vấn sản phẩm",
    text: "Chào anh/chị, vui lòng cho gian hàng biết nhu cầu sử dụng hoặc phân loại anh/chị quan tâm để tư vấn chi tiết ạ.",
  },
  {
    label: "Xác nhận giao trong ngày",
    text: "Đơn hàng của anh/chị đã đủ hàng và sẽ được đóng gói bàn giao vận chuyển trong chuyến hôm nay ạ.",
  },
  {
    label: "Chính sách đổi trả",
    text: "Gian hàng hỗ trợ đổi trả theo chính sách công bố khi sản phẩm còn nguyên vẹn bao bì và chưa qua sử dụng ạ.",
  },
];

const AVATAR_TONE_CLASSES = [
  "bg-badge-info-bg text-badge-info-ink",
  "bg-badge-success-bg text-badge-success-ink",
  "bg-badge-attention-bg text-badge-attention-ink",
  "bg-badge-neutral-bg text-badge-neutral-ink",
];

type AttachMode = "none" | "image" | "product" | "order";
type ThreadFilter = "all" | "unread";
type MobilePane = "list" | "thread";

interface ConversationsApiResponse {
  conversations?: unknown[];
  total?: number;
  total_unread?: number;
}

interface MessagesApiResponse {
  conversation?: unknown;
  messages?: unknown[];
}

interface SendMessageApiResponse {
  message?: unknown;
  auto_reply?: unknown;
}

interface SuggestReplyApiResponse {
  suggestions?: unknown[];
}

function getInitials(fullName: string): string {
  const parts = fullName.trim().split(/\s+/);
  if (parts.length <= 1) return fullName.slice(0, 2).toUpperCase();
  return `${parts[0][0] ?? ""}${parts[parts.length - 1][0] ?? ""}`.toUpperCase();
}

function createClientMsgId(): string {
  return `scli-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function extractSuggestionStrings(raw: unknown): string[] {
  if (!Array.isArray(raw)) return [];
  const list: string[] = [];
  for (const item of raw) {
    if (typeof item === "string" && item.trim()) {
      list.push(item.trim());
    } else if (item && typeof item === "object") {
      const rec = item as Record<string, unknown>;
      const text =
        (typeof rec.text === "string" && rec.text.trim()) ||
        (typeof rec.reply === "string" && rec.reply.trim()) ||
        (typeof rec.body === "string" && rec.body.trim()) ||
        "";
      if (text) list.push(text);
    }
    if (list.length >= 3) break;
  }
  return list;
}

function connectionStatusMeta(status: ChatConnectionStatus): {
  label: string;
  tone: StatusTone;
} {
  switch (status) {
    case "connected":
      return { label: "Trực tuyến", tone: "success" };
    case "connecting":
      return { label: "Đang kết nối", tone: "attention" };
    case "reconnecting":
      return { label: "Đang đồng bộ lại", tone: "attention" };
    default:
      return { label: "Kết nối tiêu chuẩn", tone: "neutral" };
  }
}

export interface SellerChatClientProps {
  sellerUserId?: string;
}

function SellerChatContent({ sellerUserId }: SellerChatClientProps) {
  const { user, shop, showToast } = useSellerContext();
  const currentSellerId = sellerUserId || user.id;
  const shopDisplayName = shop?.name ?? "Gian hàng";

  const [conversations, setConversations] = useState<ChatConversationDTO[]>([]);
  const [loadingConversations, setLoadingConversations] = useState(true);
  const [conversationsError, setConversationsError] = useState<string | null>(null);

  const [query, setQuery] = useState("");
  const [threadFilter, setThreadFilter] = useState<ThreadFilter>("all");
  const [selectedId, setSelectedId] = useState<string>("");
  const [mobilePane, setMobilePane] = useState<MobilePane>("list");
  const [contextSheetOpen, setContextSheetOpen] = useState(false);

  const [messagesByConv, setMessagesByConv] = useState<Record<string, ChatMessageDTO[]>>({});
  const [loadingMessages, setLoadingMessages] = useState(false);
  const [messagesError, setMessagesError] = useState<string | null>(null);

  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);

  const [attachMode, setAttachMode] = useState<AttachMode>("none");
  const [attachUrl, setAttachUrl] = useState("");
  const [attachRefId, setAttachRefId] = useState("");
  const [attachLabel, setAttachLabel] = useState("");

  const [aiLoading, setAiLoading] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  const [aiSuggestions, setAiSuggestions] = useState<string[]>([]);

  const [connStatus, setConnStatus] = useState<ChatConnectionStatus>("idle");

  const socketRef = useRef<ChatSocketManager | null>(null);
  const selectedIdRef = useRef<string>("");
  const conversationsRef = useRef<ChatConversationDTO[]>([]);
  const messagesByConvRef = useRef<Record<string, ChatMessageDTO[]>>({});
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  const composerInputRef = useRef<HTMLInputElement | null>(null);

  selectedIdRef.current = selectedId;
  conversationsRef.current = conversations;
  messagesByConvRef.current = messagesByConv;

  const isSellerMessage = useCallback(
    (msg: ChatMessageDTO): boolean => {
      if (msg.sender_role === "seller") return true;
      if (msg.sender_role === "buyer") return false;
      return Boolean(currentSellerId && msg.sender_id === currentSellerId);
    },
    [currentSellerId]
  );

  const loadConversations = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) {
      setLoadingConversations(true);
      setConversationsError(null);
    }
    try {
      const res = await api<ConversationsApiResponse>("/api/seller/chat/conversations");
      const list = (res.conversations ?? [])
        .map((item) => normalizeConversation(item))
        .filter((c): c is ChatConversationDTO => c !== null)
        .sort(
          (a, b) =>
            Date.parse(b.last_message_at || b.updated_at || "") -
            Date.parse(a.last_message_at || a.updated_at || "")
        );
      conversationsRef.current = list;
      setConversations(list);
      setConversationsError(null);
      setSelectedId((prev) => {
        if (prev && list.some((c) => c.id === prev)) return prev;
        return list[0]?.id ?? "";
      });
    } catch (err) {
      if (!opts.silent) {
        setConversationsError(
          err instanceof ApiError
            ? err.message
            : "Dịch vụ hộp thư khách hàng tạm gián đoạn. Vui lòng thử lại."
        );
      }
    } finally {
      if (!opts.silent) {
        setLoadingConversations(false);
      }
    }
  }, []);

  const syncConversationRead = useCallback(
    async (convId: string, lastReadSeq?: number, messageId?: string): Promise<ReadSyncResult> =>
      markSellerConversationRead({
        conversationId: convId,
        lastReadSeq,
        messageId,
        sendWsRead: (id, seq, msgId) => Boolean(socketRef.current?.sendRead(id, seq, msgId)),
        postRestRead: (id, body) =>
          api(`/api/seller/chat/conversations/${encodeURIComponent(id)}/read`, {
            method: "POST",
            body,
          }),
      }),
    []
  );

  const clearConversationUnread = useCallback((convId: string, overrideReadCount?: number) => {
    const currentConv = conversationsRef.current.find((c) => c.id === convId);
    const currentUnread =
      currentConv && currentConv.unread_count > 0 ? currentConv.unread_count : 0;
    const clearedCount =
      overrideReadCount !== undefined ? Math.max(0, Math.trunc(overrideReadCount)) : currentUnread;
    if (currentUnread > 0) {
      conversationsRef.current = conversationsRef.current.map((c) =>
        c.id === convId ? { ...c, unread_count: 0 } : c
      );
    }
    setConversations((prev) => prev.map((c) => (c.id === convId ? { ...c, unread_count: 0 } : c)));
    if (clearedCount > 0 && typeof window !== "undefined") {
      window.dispatchEvent(
        new CustomEvent<SellerChatReadEventDetail>(SELLER_CHAT_READ_EVENT, {
          detail: { readCount: clearedCount },
        })
      );
    }
  }, []);

  const syncAndClearUnread = useCallback(
    async (convId: string, lastReadSeq?: number, messageId?: string, preWsUnread = 0) => {
      const result = await syncConversationRead(convId, lastReadSeq, messageId);
      if (shouldClearUnreadAfterReadSync(result, true)) {
        clearConversationUnread(convId, preWsUnread);
      }
    },
    [clearConversationUnread, syncConversationRead]
  );

  const loadMessages = useCallback(
    async (convId: string, afterId?: string) => {
      if (!convId) return;
      if (!afterId) {
        setLoadingMessages(true);
        setMessagesError(null);
      }
      try {
        const qs = afterId ? `?after=${encodeURIComponent(afterId)}` : "?limit=50";
        const res = await api<MessagesApiResponse>(
          `/api/seller/chat/conversations/${encodeURIComponent(convId)}/messages${qs}`
        );
        const parsed = (res.messages ?? [])
          .map((item) => normalizeMessage(item))
          .filter((m): m is ChatMessageDTO => m !== null);
        const merged = afterId
          ? mergeMessages(messagesByConvRef.current[convId] ?? [], parsed)
          : mergeMessages([], parsed);
        setMessagesByConv((prev) => ({
          ...prev,
          [convId]: afterId ? mergeMessages(prev[convId] ?? [], parsed) : mergeMessages([], parsed),
        }));
        const latest =
          parsed[parsed.length - 1] ?? [...merged].reverse().find((m) => !m.id.startsWith("scli-"));
        if (latest) {
          const syncResult = await syncConversationRead(convId, latest.seq, latest.id);
          if (shouldClearUnreadAfterReadSync(syncResult, true)) {
            clearConversationUnread(convId);
          }
        } else if (shouldClearUnreadAfterReadSync(null, false)) {
          setConversations((prev) =>
            prev.map((c) => (c.id === convId ? { ...c, unread_count: 0 } : c))
          );
        }
      } catch (err) {
        if (!afterId) {
          setMessagesError(
            err instanceof ApiError ? err.message : "Không thể tải lịch sử tin nhắn lúc này."
          );
        }
      } finally {
        if (!afterId) {
          setLoadingMessages(false);
        }
      }
    },
    [clearConversationUnread, syncConversationRead]
  );

  useEffect(() => {
    void loadConversations();
  }, [loadConversations]);

  useEffect(() => {
    if (!selectedId) return;
    setAiSuggestions([]);
    setAiError(null);
    setSendError(null);
    void loadMessages(selectedId);
  }, [selectedId, loadMessages]);

  useEffect(() => {
    if (loadingConversations || conversationsError) {
      socketRef.current?.dispose();
      socketRef.current = null;
      return;
    }

    const handleWsEvent = (ev: WsIncomingEvent) => {
      if (ev.type === "message") {
        const convId = ev.conversation_id || ev.message.conversation_id;
        if (!convId) return;
        setMessagesByConv((prev) => ({
          ...prev,
          [convId]: mergeMessages(prev[convId] ?? [], [ev.message]),
        }));
        const active = selectedIdRef.current === convId;
        const fromSeller = isSellerMessage(ev.message);
        const prevConv = conversationsRef.current.find((c) => c.id === convId);
        const preWsUnread = prevConv && prevConv.unread_count > 0 ? prevConv.unread_count : 0;
        setConversations((prev) => {
          const exists = prev.some((c) => c.id === convId);
          if (!exists) {
            void loadConversations({ silent: true });
            return prev;
          }
          const updated = prev.map((c) =>
            c.id === convId
              ? {
                  ...c,
                  last_message_id: ev.message.id,
                  last_message_preview: ev.message.body,
                  last_message_at: ev.message.created_at,
                  updated_at: ev.message.created_at,
                  unread_count: fromSeller ? 0 : c.unread_count + 1,
                }
              : c
          );
          return updated.sort(
            (a, b) =>
              Date.parse(b.last_message_at || b.updated_at || "") -
              Date.parse(a.last_message_at || a.updated_at || "")
          );
        });
        if (active && !fromSeller) {
          void syncAndClearUnread(convId, ev.message.seq, ev.message.id, preWsUnread);
        }
      } else if (ev.type === "ack") {
        const convId = ev.conversation_id;
        if (!convId) return;
        if (ev.message) {
          setMessagesByConv((prev) => ({
            ...prev,
            [convId]: mergeMessages(prev[convId] ?? [], [ev.message!]),
          }));
        } else if (ev.client_msg_id) {
          setMessagesByConv((prev) => ({
            ...prev,
            [convId]: (prev[convId] ?? []).map((m) =>
              m.client_msg_id === ev.client_msg_id
                ? { ...m, id: ev.message_id, ...(ev.seq ? { seq: ev.seq } : {}) }
                : m
            ),
          }));
        }
      } else if (ev.type === "error") {
        setSendError(ev.message || "Không thể gửi tin nhắn lúc này.");
      }
    };

    const manager = createChatSocketManager({
      ticketEndpoint: "/api/seller/chat/ws-ticket",
      onMessage: handleWsEvent,
      onStatusChange: (next) => setConnStatus(next),
      onReconnectSync: async () => {
        await loadConversations({ silent: true });
        const activeId = selectedIdRef.current;
        if (!activeId) return;
        const currentMsgs = messagesByConvRef.current[activeId] ?? [];
        const serverMsgs = currentMsgs.filter((m) => !m.id.startsWith("scli-"));
        const lastId = serverMsgs[serverMsgs.length - 1]?.id;
        await loadMessages(activeId, lastId);
      },
    });
    socketRef.current = manager;
    manager.connect();

    return () => {
      manager.dispose();
      if (socketRef.current === manager) {
        socketRef.current = null;
      }
    };
  }, [
    loadingConversations,
    conversationsError,
    isSellerMessage,
    loadConversations,
    loadMessages,
    syncAndClearUnread,
  ]);

  const unreadTotal = useMemo(
    () => conversations.reduce((sum, c) => sum + (c.unread_count > 0 ? 1 : 0), 0),
    [conversations]
  );

  const filteredConversations = useMemo(() => {
    const q = query.trim().toLowerCase();
    return conversations.filter((c) => {
      if (threadFilter === "unread" && c.unread_count <= 0) return false;
      if (!q) return true;
      return (
        c.buyer_name.toLowerCase().includes(q) ||
        c.last_message_preview.toLowerCase().includes(q) ||
        c.id.toLowerCase().includes(q)
      );
    });
  }, [conversations, query, threadFilter]);

  const activeConv = useMemo(
    () =>
      filteredConversations.find((c) => c.id === selectedId) ??
      conversations.find((c) => c.id === selectedId) ??
      filteredConversations[0] ??
      null,
    [filteredConversations, conversations, selectedId]
  );

  const activeMessages = useMemo(
    () => (activeConv ? (messagesByConv[activeConv.id] ?? []) : []),
    [activeConv, messagesByConv]
  );

  const referencedEntities = useMemo(() => {
    const orders: Array<{ id: string; label: string }> = [];
    const products: Array<{ id: string; name: string; priceVnd?: number; image?: string }> = [];
    const seenOrders = new Set<string>();
    const seenProducts = new Set<string>();
    for (const msg of activeMessages) {
      if (msg.kind === "order" && msg.ref_id && !seenOrders.has(msg.ref_id)) {
        seenOrders.add(msg.ref_id);
        const card = parseOrderRefCard(msg);
        orders.push({ id: msg.ref_id, label: card.label });
      } else if (msg.kind === "product" && msg.ref_id && !seenProducts.has(msg.ref_id)) {
        seenProducts.add(msg.ref_id);
        const card = parseProductRefCard(msg);
        products.push({
          id: msg.ref_id,
          name: card.name,
          priceVnd: card.priceVnd,
          image: card.image,
        });
      }
    }
    return { orders, products };
  }, [activeMessages]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [activeMessages.length, activeConv?.id]);

  const offPlatformCheck = useMemo(() => detectOffPlatformContact(draft), [draft]);

  const sendOutgoing = useCallback(
    async (payload: {
      kind: ChatMessageKind;
      body: string;
      ref_id?: string;
      metadata?: Record<string, unknown>;
    }): Promise<boolean> => {
      if (!activeConv) return false;
      const validated = validateOutgoingMessage(payload);
      if (!validated.ok) {
        setSendError(validated.message);
        return false;
      }

      setSendError(null);
      const clientMsgId = createClientMsgId();
      const nowIso = new Date().toISOString();
      const optimistic: ChatMessageDTO = {
        id: clientMsgId,
        conversation_id: activeConv.id,
        sender_id: currentSellerId,
        sender_role: "seller",
        kind: validated.value.kind,
        body: validated.value.body,
        ref_id: validated.value.ref_id,
        client_msg_id: clientMsgId,
        metadata: validated.value.metadata,
        created_at: nowIso,
      };

      const sentLive = socketRef.current?.send({
        conversation_id: activeConv.id,
        kind: validated.value.kind,
        body: validated.value.body,
        ref_id: validated.value.ref_id,
        client_msg_id: clientMsgId,
        metadata: validated.value.metadata,
      });

      if (sentLive) {
        setMessagesByConv((prev) => ({
          ...prev,
          [activeConv.id]: mergeMessages(prev[activeConv.id] ?? [], [optimistic]),
        }));
        setConversations((prev) =>
          prev.map((c) =>
            c.id === activeConv.id
              ? {
                  ...c,
                  last_message_preview: validated.value.body,
                  last_message_at: nowIso,
                  unread_count: 0,
                }
              : c
          )
        );
        return true;
      }

      setSending(true);
      try {
        const res = await api<SendMessageApiResponse>(
          `/api/seller/chat/conversations/${encodeURIComponent(activeConv.id)}/messages`,
          {
            method: "POST",
            body: {
              kind: validated.value.kind,
              body: validated.value.body,
              ref_id: validated.value.ref_id,
              client_msg_id: clientMsgId,
              ...(validated.value.metadata ? { metadata: validated.value.metadata } : {}),
            },
          }
        );
        const confirmed = normalizeMessage(res.message) ?? optimistic;
        setMessagesByConv((prev) => ({
          ...prev,
          [activeConv.id]: mergeMessages(prev[activeConv.id] ?? [], [confirmed]),
        }));
        setConversations((prev) =>
          prev.map((c) =>
            c.id === activeConv.id
              ? {
                  ...c,
                  last_message_id: confirmed.id,
                  last_message_preview: confirmed.body,
                  last_message_at: confirmed.created_at,
                  unread_count: 0,
                }
              : c
          )
        );
        return true;
      } catch (err) {
        setSendError(
          err instanceof ApiError
            ? err.message
            : "Không thể gửi phản hồi lúc này. Vui lòng thử lại."
        );
        return false;
      } finally {
        setSending(false);
      }
    },
    [activeConv, currentSellerId]
  );

  const handleSendText = async (ev: React.FormEvent) => {
    ev.preventDefault();
    const clean = draft.trim();
    if (!clean || !activeConv || sending) return;
    const ok = await sendOutgoing({ kind: "text", body: clean });
    if (ok) {
      setDraft("");
      setAiSuggestions([]);
      showToast(`Đã gửi phản hồi đến ${activeConv.buyer_name}.`);
    }
  };

  const handleSendAttachment = async (ev: React.FormEvent) => {
    ev.preventDefault();
    if (!activeConv || sending) return;

    let ok = false;
    if (attachMode === "image") {
      ok = await sendOutgoing({ kind: "image", body: attachUrl.trim() });
    } else if (attachMode === "product") {
      const refId = attachRefId.trim();
      const label = attachLabel.trim() || `Sản phẩm ${refId}`;
      ok = await sendOutgoing({
        kind: "product",
        ref_id: refId,
        body: label,
        metadata: { name: label },
      });
    } else if (attachMode === "order") {
      const refId = attachRefId.trim();
      const label = attachLabel.trim() || `Đơn hàng ${refId}`;
      ok = await sendOutgoing({
        kind: "order",
        ref_id: refId,
        body: label,
        metadata: { order_code: refId },
      });
    }

    if (ok) {
      setAttachMode("none");
      setAttachUrl("");
      setAttachRefId("");
      setAttachLabel("");
      showToast("Đã gửi thẻ đính kèm vào hội thoại.");
    }
  };

  const handleSuggestAiReplies = async () => {
    if (!activeConv || aiLoading) return;
    setAiLoading(true);
    setAiError(null);
    try {
      const recent = activeMessages.slice(-8).map((m) => ({
        role: isSellerMessage(m) ? "assistant" : "user",
        sender_role: isSellerMessage(m) ? "seller" : "buyer",
        content: m.body,
        body: m.body,
      }));
      const res = await api<SuggestReplyApiResponse>("/api/seller/assistant/suggest-reply", {
        method: "POST",
        body: {
          conversation_id: activeConv.id,
          messages: recent,
          shop_name: shopDisplayName,
        },
      });
      const list = extractSuggestionStrings(res.suggestions);
      if (list.length === 0) {
        setAiError("AI chưa tạo được gợi ý cho hội thoại này.");
      } else {
        setAiSuggestions(list);
      }
    } catch (err) {
      setAiError(
        err instanceof ApiError ? err.message : "Tính năng gợi ý trả lời AI tạm thời chưa sẵn sàng."
      );
    } finally {
      setAiLoading(false);
    }
  };

  const handlePickReply = (text: string) => {
    setDraft(text);
    setMobilePane("thread");
    setContextSheetOpen(false);
    composerInputRef.current?.focus();
  };

  const handleSelectThread = (id: string) => {
    setSelectedId(id);
    setMobilePane("thread");
  };

  const statusInfo = connectionStatusMeta(connStatus);

  const renderContextBody = () => {
    if (!activeConv) {
      return (
        <p className="p-4 text-[13px] text-muted-foreground">
          Chọn hội thoại để xem ngữ cảnh khách hàng.
        </p>
      );
    }

    return (
      <div className="space-y-4 p-4 text-[13px]">
        <div className="space-y-3">
          <div className="flex items-center gap-2.5">
            <Avatar className="size-9 shrink-0">
              <AvatarFallback className={cn("text-xs font-semibold", AVATAR_TONE_CLASSES[0])}>
                {getInitials(activeConv.buyer_name)}
              </AvatarFallback>
            </Avatar>
            <div className="min-w-0 flex-1">
              <div className="truncate font-semibold text-foreground">{activeConv.buyer_name}</div>
              <div className="truncate text-xs text-muted-foreground">
                {activeConv.site_host || shopDisplayName}
              </div>
            </div>
          </div>

          <div className="space-y-1.5 rounded-lg border border-border bg-card p-3 tabular-nums">
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-muted-foreground">Tin nhắn</span>
              <strong className="font-semibold text-foreground">
                {formatInt(activeMessages.length)}
              </strong>
            </div>
            {activeConv.last_message_at ? (
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-muted-foreground">Gần nhất</span>
                <strong className="font-semibold text-foreground">
                  {formatTimeVi(activeConv.last_message_at)}
                </strong>
              </div>
            ) : null}
          </div>

          <div className="flex flex-col gap-1.5">
            <Button variant="outline" size="xs" className="justify-between bg-card" asChild>
              <Link href="/seller/don-hang">
                <span>Đơn hàng gian hàng</span>
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </Link>
            </Button>
            <Button variant="outline" size="xs" className="justify-between bg-card" asChild>
              <Link href="/seller/ho-so?tab=autoreply">
                <span>Trả lời tự động</span>
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </Link>
            </Button>
          </div>
        </div>

        <Separator />

        <div className="space-y-2">
          <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
            Đơn &amp; Sản phẩm đã nhắc
          </h3>
          {referencedEntities.orders.length === 0 && referencedEntities.products.length === 0 ? (
            <div className="space-y-2 text-xs text-muted-foreground">
              <p>Chưa có thẻ đơn/sản phẩm trong hội thoại.</p>
              <div className="flex flex-wrap gap-1.5">
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  onClick={() => {
                    setAttachMode("product");
                    setMobilePane("thread");
                    setContextSheetOpen(false);
                  }}
                >
                  Gửi SP
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  onClick={() => {
                    setAttachMode("order");
                    setMobilePane("thread");
                    setContextSheetOpen(false);
                  }}
                >
                  Gửi đơn
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              {referencedEntities.orders.map((ord) => (
                <Link
                  key={`ctx-ord-${ord.id}`}
                  href={`/seller/don-hang/${encodeURIComponent(ord.id)}`}
                  className="flex items-center gap-2.5 rounded-lg border border-border bg-card p-2.5 transition-colors hover:border-brand/50"
                >
                  <ShoppingBag className="size-4 shrink-0 text-brand" aria-hidden="true" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-semibold text-foreground">{ord.label}</div>
                    <div className="text-xs text-muted-foreground">Mở chi tiết đơn</div>
                  </div>
                </Link>
              ))}
              {referencedEntities.products.map((prod) => (
                <Link
                  key={`ctx-prod-${prod.id}`}
                  href={`/seller/san-pham/${encodeURIComponent(prod.id)}`}
                  className="flex items-center gap-2.5 rounded-lg border border-border bg-card p-2.5 transition-colors hover:border-brand/50"
                >
                  {prod.image ? (
                    <img
                      src={prod.image}
                      alt={prod.name}
                      className="size-9 shrink-0 rounded-md border border-border bg-muted object-cover"
                    />
                  ) : (
                    <Package className="size-4 shrink-0 text-brand" aria-hidden="true" />
                  )}
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-semibold text-foreground">{prod.name}</div>
                    {prod.priceVnd !== undefined ? (
                      <div className="text-xs text-muted-foreground tabular-nums">
                        {formatVnd(prod.priceVnd)}
                      </div>
                    ) : null}
                  </div>
                </Link>
              ))}
            </div>
          )}
        </div>

        <Separator />

        <div className="space-y-2">
          <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
            Mẫu trả lời nhanh
          </h3>
          <div className="space-y-2">
            {QUICK_REPLIES.map((tpl) => (
              <button
                key={tpl.label}
                type="button"
                onClick={() => handlePickReply(tpl.text)}
                className="flex w-full flex-col gap-0.5 rounded-lg border border-border bg-card p-2.5 text-left transition-colors hover:border-brand/50 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <span className="text-xs font-semibold text-foreground">{tpl.label}</span>
                <span className="line-clamp-2 text-xs text-muted-foreground">{tpl.text}</span>
              </button>
            ))}
          </div>
        </div>
      </div>
    );
  };

  return (
    <div className="space-y-4">
      <PageHeader
        title="Tin nhắn"
        badges={<StatusBadge tone={statusInfo.tone}>{statusInfo.label}</StatusBadge>}
        primaryAction={
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void loadConversations()}
            disabled={loadingConversations}
          >
            <RefreshCw
              className={cn("size-3.5", loadingConversations && "animate-spin")}
              aria-hidden="true"
            />
            <span>Làm mới</span>
          </Button>
        }
      />

      {loadingConversations ? (
        <Card role="status" aria-label="Đang tải hộp thư khách hàng...">
          <CardContent className="space-y-3 py-5">
            {Array.from({ length: 5 }).map((_, idx) => (
              <Skeleton key={idx} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      ) : null}

      {!loadingConversations && conversationsError ? (
        <ErrorState
          title="Không thể kết nối hộp thư"
          description={conversationsError}
          onRetry={() => void loadConversations()}
        />
      ) : null}

      {!loadingConversations && !conversationsError && conversations.length === 0 ? (
        <EmptyState
          icon={MessageSquare}
          title="Chưa có tin nhắn khách hàng"
          description="Khi người mua nhắn hỏi về sản phẩm, phân loại hoặc đơn hàng, hội thoại sẽ hiển thị tại đây."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/don-hang">Xem đơn hàng</Link>
            </Button>
          }
        />
      ) : null}

      {!loadingConversations && !conversationsError && conversations.length > 0 ? (
        <section
          aria-label="Hộp thư khách hàng"
          className="grid h-[clamp(520px,calc(100svh-180px),740px)] grid-cols-1 overflow-hidden rounded-xl bg-card shadow-card md:grid-cols-[300px_minmax(0,1fr)] xl:grid-cols-[300px_minmax(0,1fr)_272px]"
        >
          {/* Column 1: Conversations List */}
          <div
            className={cn(
              "flex min-h-0 flex-col border-border md:border-r",
              mobilePane !== "list" ? "hidden md:flex" : "flex"
            )}
          >
            <div className="space-y-2 border-b border-border p-3">
              <div className="relative">
                <Search
                  className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm khách hoặc tin nhắn..."
                  aria-label="Tìm hội thoại khách hàng"
                  className="h-9 bg-card pl-8 text-[13px]"
                />
              </div>

              <div role="tablist" aria-label="Lọc hội thoại" className="flex items-center gap-1.5">
                <Button
                  type="button"
                  role="tab"
                  aria-selected={threadFilter === "all"}
                  size="xs"
                  variant={threadFilter === "all" ? "default" : "secondary"}
                  onClick={() => setThreadFilter("all")}
                  className="tabular-nums"
                >
                  Tất cả ({formatInt(conversations.length)})
                </Button>
                <Button
                  type="button"
                  role="tab"
                  aria-selected={threadFilter === "unread"}
                  size="xs"
                  variant={threadFilter === "unread" ? "default" : "secondary"}
                  onClick={() => setThreadFilter("unread")}
                  className="tabular-nums"
                >
                  Chưa đọc ({formatInt(unreadTotal)})
                </Button>
              </div>
            </div>

            <div
              role="listbox"
              aria-label="Danh sách hội thoại"
              className="flex-1 divide-y divide-border overflow-y-auto"
            >
              {filteredConversations.length === 0 ? (
                <p className="p-5 text-center text-[13px] text-muted-foreground">
                  Không có hội thoại phù hợp.
                </p>
              ) : (
                filteredConversations.map((conv, idx) => {
                  const isSelected = activeConv?.id === conv.id;
                  const unread = conv.unread_count > 0;
                  return (
                    <button
                      key={conv.id}
                      type="button"
                      role="option"
                      aria-selected={isSelected}
                      onClick={() => handleSelectThread(conv.id)}
                      className={cn(
                        "flex w-full items-start gap-2.5 px-3 py-3 text-left transition-colors hover:bg-accent/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
                        isSelected && "bg-accent/75"
                      )}
                    >
                      <span
                        aria-hidden="true"
                        className={cn(
                          "mt-2 size-2 shrink-0 rounded-full",
                          unread ? "bg-brand" : "bg-transparent"
                        )}
                      />
                      <Avatar className="size-8 shrink-0">
                        <AvatarFallback
                          className={cn(
                            "text-xs font-semibold",
                            AVATAR_TONE_CLASSES[idx % AVATAR_TONE_CLASSES.length]
                          )}
                        >
                          {getInitials(conv.buyer_name)}
                        </AvatarFallback>
                      </Avatar>
                      <span className="min-w-0 flex-1 space-y-0.5">
                        <span className="flex items-baseline justify-between gap-2">
                          <span
                            className={cn(
                              "truncate text-sm",
                              unread
                                ? "font-semibold text-foreground"
                                : "font-medium text-foreground"
                            )}
                          >
                            {conv.buyer_name}
                          </span>
                          {conv.last_message_at ? (
                            <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                              {formatTimeVi(conv.last_message_at)}
                            </span>
                          ) : null}
                        </span>
                        <span
                          className={cn(
                            "block truncate text-xs",
                            unread ? "font-medium text-foreground" : "text-muted-foreground"
                          )}
                        >
                          {conv.last_message_preview || "Chưa có tin nhắn"}
                        </span>
                        <span className="flex items-center justify-between gap-2 pt-0.5">
                          <span className="truncate text-[11px] text-muted-foreground">
                            {conv.site_host || shopDisplayName}
                          </span>
                          {unread ? (
                            <Badge variant="info" className="px-1.5 py-0 text-[11px] tabular-nums">
                              {conv.unread_count}
                            </Badge>
                          ) : null}
                        </span>
                      </span>
                    </button>
                  );
                })
              )}
            </div>
          </div>

          {/* Column 2: Active Message Stream & Composer */}
          <div
            className={cn(
              "flex min-h-0 min-w-0 flex-col",
              mobilePane !== "thread" ? "hidden md:flex" : "flex"
            )}
          >
            {activeConv ? (
              <>
                <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
                  <div className="flex min-w-0 items-center gap-2">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label="Quay lại danh sách hội thoại"
                      onClick={() => setMobilePane("list")}
                      className="md:hidden"
                    >
                      <ArrowLeft className="size-4" aria-hidden="true" />
                    </Button>
                    <div className="min-w-0">
                      <h2 className="truncate text-sm font-semibold text-foreground">
                        {activeConv.buyer_name}
                      </h2>
                      <p className="truncate text-xs text-muted-foreground">
                        {activeConv.site_host || shopDisplayName}
                      </p>
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    className="xl:hidden"
                    onClick={() => setContextSheetOpen(true)}
                  >
                    <User className="size-3.5" aria-hidden="true" />
                    <span>Ngữ cảnh</span>
                  </Button>
                </div>

                <div className="flex-1 space-y-3 overflow-y-auto p-4" aria-live="polite">
                  {loadingMessages && activeMessages.length === 0 ? (
                    <div className="space-y-2.5">
                      <Skeleton className="h-12 w-2/3" />
                      <Skeleton className="ml-auto h-12 w-1/2" />
                      <Skeleton className="h-12 w-3/5" />
                    </div>
                  ) : messagesError && activeMessages.length === 0 ? (
                    <ErrorState
                      title="Không thể tải tin nhắn"
                      description={messagesError}
                      onRetry={() => void loadMessages(activeConv.id)}
                    />
                  ) : activeMessages.length === 0 ? (
                    <p className="py-8 text-center text-[13px] text-muted-foreground">
                      Chưa có tin nhắn. Gửi lời chào đến khách hàng ngay bên dưới.
                    </p>
                  ) : (
                    activeMessages.map((msg) => {
                      const sellerSent = isSellerMessage(msg);
                      return (
                        <div
                          key={msg.id}
                          className={cn(
                            "flex max-w-[82%] flex-col gap-1",
                            sellerSent ? "ml-auto items-end" : "mr-auto items-start"
                          )}
                        >
                          <div
                            className={cn(
                              "rounded-xl px-3.5 py-2.5 text-sm break-words",
                              sellerSent
                                ? "bg-primary text-primary-foreground"
                                : "bg-muted text-foreground"
                            )}
                          >
                            {msg.kind === "image" ? (
                              (() => {
                                const safeImg = sanitizeSafeUrl(msg.body);
                                if (!safeImg) {
                                  return (
                                    <span className="text-xs opacity-80">
                                      Ảnh đính kèm không hợp lệ.
                                    </span>
                                  );
                                }
                                return (
                                  <a
                                    href={safeImg}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="block"
                                  >
                                    <img
                                      src={safeImg}
                                      alt="Ảnh đính kèm trong hội thoại"
                                      className="max-h-60 max-w-[240px] rounded-md object-cover"
                                    />
                                  </a>
                                );
                              })()
                            ) : msg.kind === "product" ? (
                              (() => {
                                const card = parseProductRefCard(msg);
                                return (
                                  <Link
                                    href={`/seller/san-pham/${encodeURIComponent(msg.ref_id)}`}
                                    className="flex min-w-[190px] items-center gap-2.5"
                                  >
                                    {card.image ? (
                                      <img
                                        src={card.image}
                                        alt={card.name}
                                        className="size-10 shrink-0 rounded-md bg-card object-cover"
                                      />
                                    ) : null}
                                    <span className="min-w-0 flex-1 space-y-0.5 text-xs">
                                      <span className="block opacity-75">Sản phẩm</span>
                                      <span className="block truncate font-semibold">
                                        {card.name}
                                      </span>
                                      {card.priceVnd !== undefined ? (
                                        <span className="block tabular-nums opacity-90">
                                          {formatVnd(card.priceVnd)}
                                        </span>
                                      ) : null}
                                    </span>
                                  </Link>
                                );
                              })()
                            ) : msg.kind === "order" ? (
                              (() => {
                                const card = parseOrderRefCard(msg);
                                return (
                                  <Link
                                    href={`/seller/don-hang/${encodeURIComponent(msg.ref_id)}`}
                                    className="flex min-w-[190px] items-center gap-2.5"
                                  >
                                    <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary-foreground/15">
                                      <Package className="size-4" aria-hidden="true" />
                                    </span>
                                    <span className="min-w-0 flex-1 space-y-0.5 text-xs">
                                      <span className="block opacity-75">Đơn hàng</span>
                                      <span className="block truncate font-semibold">
                                        {card.label}
                                      </span>
                                      <span className="block opacity-85">Mở chi tiết đơn</span>
                                    </span>
                                  </Link>
                                );
                              })()
                            ) : (
                              <span className="whitespace-pre-wrap">{msg.body}</span>
                            )}
                          </div>
                          <span className="text-[11px] text-muted-foreground tabular-nums">
                            {sellerSent
                              ? msg.is_auto_reply
                                ? "Trả lời tự động"
                                : shopDisplayName
                              : activeConv.buyer_name}
                            , {formatDateTimeVi(msg.created_at)}
                          </span>
                        </div>
                      );
                    })
                  )}
                  <div ref={messagesEndRef} />
                </div>

                {aiSuggestions.length > 0 ? (
                  <div
                    aria-label="Gợi ý trả lời từ trợ lý AI"
                    className="space-y-2 border-t border-border bg-badge-info-bg/45 px-4 py-2.5"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-brand">
                        <Sparkles className="size-3.5" aria-hidden="true" />
                        <span>Gợi ý AI, nhấn để điền vào ô nhập</span>
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        onClick={() => setAiSuggestions([])}
                      >
                        Ẩn
                      </Button>
                    </div>
                    <div className="flex flex-col gap-1.5">
                      {aiSuggestions.map((suggestion, idx) => (
                        <button
                          key={`${idx}-${suggestion.slice(0, 16)}`}
                          type="button"
                          onClick={() => handlePickReply(suggestion)}
                          className="rounded-lg border border-border bg-card px-3 py-2 text-left text-xs text-foreground transition-colors hover:bg-accent/55"
                        >
                          {suggestion}
                        </button>
                      ))}
                    </div>
                  </div>
                ) : null}

                {aiError ? (
                  <div
                    role="status"
                    className="border-t border-border bg-muted px-4 py-2 text-xs text-muted-foreground"
                  >
                    {aiError}
                  </div>
                ) : null}

                {offPlatformCheck.flagged ? (
                  <div
                    role="note"
                    className="flex items-center gap-2 border-t border-border bg-badge-warning-bg px-4 py-2 text-xs text-badge-warning-ink"
                  >
                    <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
                    <span>Tránh trao đổi số điện thoại hoặc dẫn khách giao dịch ngoài sàn.</span>
                  </div>
                ) : null}

                {sendError ? (
                  <div
                    role="alert"
                    className="border-t border-border bg-badge-critical-bg px-4 py-2 text-xs font-medium text-badge-critical-ink"
                  >
                    {sendError}
                  </div>
                ) : null}

                {attachMode !== "none" ? (
                  <form
                    onSubmit={handleSendAttachment}
                    className="flex flex-wrap items-center gap-2 border-t border-border bg-muted/40 px-4 py-2.5"
                  >
                    {attachMode === "image" ? (
                      <Input
                        type="text"
                        value={attachUrl}
                        onChange={(e) => setAttachUrl(e.target.value)}
                        placeholder="Liên kết ảnh (/products/... hoặc https://...)"
                        aria-label="Liên kết ảnh cần gửi"
                        className="h-8 min-w-[180px] flex-1 bg-card text-xs"
                      />
                    ) : (
                      <>
                        <Input
                          type="text"
                          value={attachRefId}
                          onChange={(e) => setAttachRefId(e.target.value)}
                          placeholder={
                            attachMode === "product" ? "Mã sản phẩm (ID)..." : "Mã đơn hàng (ID)..."
                          }
                          aria-label={attachMode === "product" ? "Mã sản phẩm" : "Mã đơn hàng"}
                          className="h-8 min-w-[140px] flex-1 bg-card text-xs"
                        />
                        <Input
                          type="text"
                          value={attachLabel}
                          onChange={(e) => setAttachLabel(e.target.value)}
                          placeholder={
                            attachMode === "product"
                              ? "Tên sản phẩm (tùy chọn)"
                              : "Ghi chú mã đơn (tùy chọn)"
                          }
                          aria-label="Mô tả đính kèm"
                          className="h-8 min-w-[140px] flex-1 bg-card text-xs"
                        />
                      </>
                    )}
                    <Button type="submit" size="xs" disabled={sending}>
                      Gửi thẻ
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      onClick={() => {
                        setAttachMode("none");
                        setSendError(null);
                      }}
                    >
                      Hủy
                    </Button>
                  </form>
                ) : null}

                <form
                  onSubmit={handleSendText}
                  className="space-y-2 border-t border-border px-4 py-3"
                >
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      onClick={() => void handleSuggestAiReplies()}
                      disabled={aiLoading}
                      className="border-brand/40 text-brand hover:bg-badge-info-bg/40"
                    >
                      <Sparkles className="size-3.5" aria-hidden="true" />
                      <span>{aiLoading ? "Đang tạo..." : "Gợi ý AI"}</span>
                    </Button>
                    <Button
                      type="button"
                      variant={attachMode === "image" ? "default" : "outline"}
                      size="xs"
                      onClick={() => setAttachMode((m) => (m === "image" ? "none" : "image"))}
                    >
                      <ImageIcon className="size-3.5" aria-hidden="true" />
                      <span>Ảnh</span>
                    </Button>
                    <Button
                      type="button"
                      variant={attachMode === "product" ? "default" : "outline"}
                      size="xs"
                      onClick={() => setAttachMode((m) => (m === "product" ? "none" : "product"))}
                    >
                      <span>Sản phẩm</span>
                    </Button>
                    <Button
                      type="button"
                      variant={attachMode === "order" ? "default" : "outline"}
                      size="xs"
                      onClick={() => setAttachMode((m) => (m === "order" ? "none" : "order"))}
                    >
                      <span>Đơn hàng</span>
                    </Button>
                  </div>

                  <div className="flex items-center gap-2">
                    <Input
                      ref={composerInputRef}
                      type="text"
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      placeholder={`Nhắn cho ${activeConv.buyer_name}...`}
                      aria-label="Nhập nội dung phản hồi"
                      maxLength={2000}
                      className="h-10 bg-card"
                    />
                    <Button
                      type="submit"
                      size="sm"
                      className="h-10 shrink-0"
                      disabled={!draft.trim() || sending}
                    >
                      <Send className="size-4" aria-hidden="true" />
                      <span>Gửi</span>
                    </Button>
                  </div>
                </form>
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center p-6 text-sm text-muted-foreground">
                Chọn hội thoại bên trái để xem và phản hồi.
              </div>
            )}
          </div>

          {/* Column 3: Desktop Context Rail (>= 1280px) */}
          <aside
            aria-label="Ngữ cảnh khách hàng và đơn hàng"
            className="hidden min-h-0 flex-col border-l border-border bg-muted/35 xl:flex"
          >
            <div className="border-b border-border px-4 py-3 text-sm font-semibold text-foreground">
              Ngữ cảnh khách
            </div>
            <div className="flex-1 overflow-y-auto">{renderContextBody()}</div>
          </aside>
        </section>
      ) : null}

      {/* Mobile / Tablet Context Sheet (< 1280px) */}
      <Sheet open={contextSheetOpen} onOpenChange={setContextSheetOpen}>
        <SheetContent side="right" className="w-full gap-0 p-0 sm:max-w-sm">
          <SheetHeader className="border-b border-border px-4 py-3.5 pr-12">
            <SheetTitle>Ngữ cảnh khách</SheetTitle>
            <SheetDescription className="text-xs">
              Thông tin hội thoại, đơn liên quan và mẫu trả lời nhanh.
            </SheetDescription>
          </SheetHeader>
          <div className="flex-1 overflow-y-auto">{renderContextBody()}</div>
        </SheetContent>
      </Sheet>
    </div>
  );
}

export function SellerChatClient(props: SellerChatClientProps) {
  return (
    <SellerSuspense>
      <SellerChatContent {...props} />
    </SellerSuspense>
  );
}
