"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  Button,
  IconAlert,
  IconPackage,
  IconRefresh,
  IconSearch,
  IconSend,
  IconSpark,
  StateView,
} from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
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
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

const QUICK_REPLIES = [
  {
    label: "Tư vấn chọn size",
    text: "Chào anh/chị, vui lòng gửi chiều cao và cân nặng để gian hàng tư vấn đúng size theo bảng đo thực tế ạ.",
  },
  {
    label: "Xác nhận giao trong ngày",
    text: "Đơn hàng của anh/chị đã đủ hàng và sẽ được đóng gói bàn giao vận chuyển trong chuyến hôm nay ạ.",
  },
  {
    label: "Chính sách đổi size 45 ngày",
    text: "Gian hàng hỗ trợ đổi size 2 chiều trong 45 ngày với sản phẩm còn nguyên tem mác và chưa qua sử dụng ạ.",
  },
];

const AVATAR_TINTS = ["#e3edf0", "#e6efe8", "#efeae1", "#e5ecf2"];

type AttachMode = "none" | "image" | "product" | "order";
type ThreadFilter = "all" | "unread";

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
  tone: "online" | "sync" | "idle";
} {
  switch (status) {
    case "connected":
      return { label: "Trực tuyến", tone: "online" };
    case "connecting":
      return { label: "Đang kết nối", tone: "sync" };
    case "reconnecting":
      return { label: "Đang đồng bộ lại", tone: "sync" };
    default:
      return { label: "Kết nối tiêu chuẩn", tone: "idle" };
  }
}

export interface SellerChatClientProps {
  sellerUserId?: string;
}

function SellerChatContent({ sellerUserId }: SellerChatClientProps) {
  const { user, shop, showToast } = useSellerContext();
  const currentSellerId = sellerUserId || user.id;
  const shopDisplayName = shop?.name ?? "Gian hàng của bạn";

  const [conversations, setConversations] = useState<ChatConversationDTO[]>([]);
  const [loadingConversations, setLoadingConversations] = useState(true);
  const [conversationsError, setConversationsError] = useState<string | null>(null);

  const [query, setQuery] = useState("");
  const [threadFilter, setThreadFilter] = useState<ThreadFilter>("all");
  const [selectedId, setSelectedId] = useState<string>("");
  const [mobileView, setMobileView] = useState<"list" | "thread">("list");

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
  const messagesByConvRef = useRef<Record<string, ChatMessageDTO[]>>({});
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  const composerInputRef = useRef<HTMLInputElement | null>(null);

  selectedIdRef.current = selectedId;
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

  const loadMessages = useCallback(async (convId: string, afterId?: string) => {
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
      setMessagesByConv((prev) => ({
        ...prev,
        [convId]: afterId ? mergeMessages(prev[convId] ?? [], parsed) : mergeMessages([], parsed),
      }));
      const latest = parsed[parsed.length - 1];
      if (latest) {
        socketRef.current?.sendRead(convId, latest.seq, latest.id);
      }
      setConversations((prev) =>
        prev.map((c) => (c.id === convId ? { ...c, unread_count: 0 } : c))
      );
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
  }, []);

  // Initial load
  useEffect(() => {
    void loadConversations();
  }, [loadConversations]);

  // Load messages when active thread changes
  useEffect(() => {
    if (!selectedId) return;
    setAiSuggestions([]);
    setAiError(null);
    setSendError(null);
    void loadMessages(selectedId);
  }, [selectedId, loadMessages]);

  // Connect live WebSocket channel once conversations endpoint succeeds
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
        if (active && !fromSeller) {
          socketRef.current?.sendRead(convId, ev.message.seq, ev.message.id);
        }
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
                  unread_count: active || fromSeller ? 0 : c.unread_count + 1,
                }
              : c
          );
          return updated.sort(
            (a, b) =>
              Date.parse(b.last_message_at || b.updated_at || "") -
              Date.parse(a.last_message_at || a.updated_at || "")
          );
        });
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
  }, [loadingConversations, conversationsError, isSellerMessage, loadConversations, loadMessages]);

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

      // HTTP fallback
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
        setAiError("AI chưa tạo được gợi ý cho hội thoại này. Bạn vui lòng tự soạn phản hồi.");
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

  const handlePickAiSuggestion = (text: string) => {
    setDraft(text);
    composerInputRef.current?.focus();
  };

  const handleSelectThread = (id: string) => {
    setSelectedId(id);
    setMobileView("thread");
  };

  const statusInfo = connectionStatusMeta(connStatus);

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tin nhắn khách hàng</h1>
          <p className={common.finding}>
            {shopDisplayName} · {formatInt(conversations.length)} hội thoại
            {unreadTotal > 0 ? ` · ${formatInt(unreadTotal)} chưa đọc` : ""}
          </p>
        </div>
        <div className={common.headerActions}>
          <span className={styles.connBadge}>
            <span
              className={`${styles.connDot} ${
                statusInfo.tone === "online"
                  ? styles.connDotOnline
                  : statusInfo.tone === "sync"
                    ? styles.connDotSync
                    : styles.connDotIdle
              }`}
              aria-hidden="true"
            />
            <span>{statusInfo.label}</span>
          </span>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => void loadConversations()}
            disabled={loadingConversations}
          >
            <IconRefresh size={14} />
            <span>Làm mới</span>
          </Button>
        </div>
      </header>

      {loadingConversations && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang đồng bộ hộp thư khách hàng..."
          rows={5}
        />
      )}

      {!loadingConversations && conversationsError && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể kết nối hộp thư trò chuyện"
          message={conversationsError}
          actionLabel="Thử lại"
          onAction={() => void loadConversations()}
        />
      )}

      {!loadingConversations && !conversationsError && conversations.length === 0 && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có tin nhắn khách hàng nào"
          message="Khi người mua nhắn hỏi về sản phẩm, kích cỡ hoặc tình trạng đơn hàng từ site gian hàng, hội thoại sẽ hiển thị tại đây."
          actionLabel="Xem đơn hàng gian hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!loadingConversations && !conversationsError && conversations.length > 0 && (
        <section className={styles.inboxShell} aria-label="Hộp thư người bán">
          <div
            className={`${styles.threadPane} ${mobileView === "thread" ? styles.paneHiddenMobile : ""}`}
          >
            <div className={styles.threadPaneHead}>
              <div className={common.searchWrap} style={{ width: "100%" }}>
                <span className={common.searchIcon}>
                  <IconSearch size={14} />
                </span>
                <input
                  type="search"
                  className={common.searchInput}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm khách hàng hoặc nội dung..."
                  aria-label="Tìm hội thoại khách hàng"
                />
              </div>

              <div className={styles.filterRow} role="tablist" aria-label="Lọc hội thoại">
                <button
                  type="button"
                  role="tab"
                  aria-selected={threadFilter === "all"}
                  className={`${styles.filterTab} ${threadFilter === "all" ? styles.filterTabActive : ""}`}
                  onClick={() => setThreadFilter("all")}
                >
                  Tất cả ({formatInt(conversations.length)})
                </button>
                <button
                  type="button"
                  role="tab"
                  aria-selected={threadFilter === "unread"}
                  className={`${styles.filterTab} ${threadFilter === "unread" ? styles.filterTabActive : ""}`}
                  onClick={() => setThreadFilter("unread")}
                >
                  Chưa đọc ({formatInt(unreadTotal)})
                </button>
              </div>
            </div>

            <div className={styles.threadList} role="listbox" aria-label="Danh sách hội thoại">
              {filteredConversations.length === 0 ? (
                <div className={styles.emptyPaneHint}>
                  <p className={common.cardMeta}>Không tìm thấy hội thoại phù hợp.</p>
                </div>
              ) : (
                filteredConversations.map((conv, idx) => {
                  const isSelected = activeConv?.id === conv.id;
                  return (
                    <button
                      key={conv.id}
                      type="button"
                      role="option"
                      aria-selected={isSelected}
                      className={`${styles.threadItem} ${isSelected ? styles.threadItemSelected : ""}`}
                      onClick={() => handleSelectThread(conv.id)}
                    >
                      <span
                        className={styles.avatar}
                        style={{ backgroundColor: AVATAR_TINTS[idx % AVATAR_TINTS.length] }}
                        aria-hidden="true"
                      >
                        {getInitials(conv.buyer_name)}
                      </span>
                      <span className={styles.threadBody}>
                        <span className={styles.threadTop}>
                          <span className={styles.threadName}>{conv.buyer_name}</span>
                          {conv.last_message_at && (
                            <span className={styles.threadTime}>
                              {formatDateTimeVi(conv.last_message_at).split(" · ")[0]}
                            </span>
                          )}
                        </span>
                        <span className={styles.threadSnippet}>
                          {conv.last_message_preview || "Chưa có tin nhắn"}
                        </span>
                        <span className={styles.threadMetaRow}>
                          <span className={styles.threadProdTag}>
                            {conv.site_host || shopDisplayName}
                          </span>
                          {conv.unread_count > 0 && (
                            <span className={styles.unreadCount}>{conv.unread_count}</span>
                          )}
                        </span>
                      </span>
                    </button>
                  );
                })
              )}
            </div>
          </div>

          <div
            className={`${styles.chatPane} ${mobileView === "list" ? styles.paneHiddenMobile : ""}`}
          >
            {activeConv ? (
              <>
                <div className={styles.chatHeader}>
                  <div className={styles.chatHeaderLeft}>
                    <button
                      type="button"
                      className={styles.mobileBackBtn}
                      onClick={() => setMobileView("list")}
                    >
                      Danh sách
                    </button>
                    <div>
                      <h2 className={styles.chatBuyerName}>{activeConv.buyer_name}</h2>
                      <p className={styles.chatBuyerSub}>
                        {activeConv.site_host ? `${activeConv.site_host} · ` : ""}
                        {shopDisplayName}
                      </p>
                    </div>
                  </div>
                </div>

                <div className={styles.messages} aria-live="polite">
                  {loadingMessages && activeMessages.length === 0 ? (
                    <p className={common.cardMeta}>Đang tải lịch sử tin nhắn...</p>
                  ) : messagesError && activeMessages.length === 0 ? (
                    <StateView
                      kind="error"
                      surface="app"
                      title="Không thể tải tin nhắn"
                      message={messagesError}
                      actionLabel="Thử lại"
                      onAction={() => void loadMessages(activeConv.id)}
                    />
                  ) : activeMessages.length === 0 ? (
                    <p className={common.cardMeta}>
                      Hội thoại chưa có tin nhắn nào. Gửi lời chào đến khách hàng ngay bên dưới.
                    </p>
                  ) : (
                    activeMessages.map((msg) => {
                      const sellerSent = isSellerMessage(msg);
                      return (
                        <div
                          key={msg.id}
                          className={`${styles.msgGroup} ${sellerSent ? styles.msgSeller : styles.msgBuyer}`}
                        >
                          <div
                            className={`${styles.bubble} ${sellerSent ? styles.bubbleSeller : styles.bubbleBuyer}`}
                          >
                            {msg.kind === "image" ? (
                              (() => {
                                const safeImg = sanitizeSafeUrl(msg.body);
                                if (!safeImg) {
                                  return (
                                    <span className={styles.msgFallback}>
                                      Ảnh đính kèm không hợp lệ.
                                    </span>
                                  );
                                }
                                return (
                                  <a
                                    href={safeImg}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className={styles.msgImageLink}
                                  >
                                    <img
                                      src={safeImg}
                                      alt="Ảnh đính kèm trong hội thoại"
                                      className={styles.msgImage}
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
                                    className={styles.refCard}
                                  >
                                    {card.image && (
                                      <img
                                        src={card.image}
                                        alt={card.name}
                                        className={styles.refThumb}
                                        width={38}
                                        height={46}
                                      />
                                    )}
                                    <span className={styles.refCardText}>
                                      <span className={styles.refCardKind}>Sản phẩm</span>
                                      <span className={styles.refCardTitle}>{card.name}</span>
                                      {card.priceVnd !== undefined && (
                                        <span className={styles.refCardSub}>
                                          {formatVnd(card.priceVnd)}
                                        </span>
                                      )}
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
                                    className={styles.refCard}
                                  >
                                    <span className={styles.refIconBox}>
                                      <IconPackage size={15} />
                                    </span>
                                    <span className={styles.refCardText}>
                                      <span className={styles.refCardKind}>Đơn hàng</span>
                                      <span className={styles.refCardTitle}>{card.label}</span>
                                      <span className={styles.refCardSub}>Mở chi tiết đơn</span>
                                    </span>
                                  </Link>
                                );
                              })()
                            ) : (
                              <span className={styles.msgText}>{msg.body}</span>
                            )}
                          </div>
                          <span className={styles.msgMeta}>
                            {sellerSent
                              ? msg.is_auto_reply
                                ? "Trả lời tự động"
                                : shopDisplayName
                              : activeConv.buyer_name}{" "}
                            · {formatDateTimeVi(msg.created_at)}
                          </span>
                        </div>
                      );
                    })
                  )}
                  <div ref={messagesEndRef} />
                </div>

                {aiSuggestions.length > 0 && (
                  <div className={styles.aiSuggestBox} aria-label="Gợi ý trả lời từ trợ lý AI">
                    <div className={styles.aiSuggestHead}>
                      <span className={styles.aiSuggestLabel}>
                        <IconSpark size={13} />
                        <span>AI gợi ý, nhấn để đưa vào ô nhập và chỉnh sửa</span>
                      </span>
                      <button
                        type="button"
                        className={styles.aiDismissBtn}
                        onClick={() => setAiSuggestions([])}
                      >
                        Ẩn gợi ý
                      </button>
                    </div>
                    <div className={styles.aiChipList}>
                      {aiSuggestions.map((suggestion, idx) => (
                        <button
                          key={`${idx}-${suggestion.slice(0, 16)}`}
                          type="button"
                          className={styles.aiChipBtn}
                          onClick={() => handlePickAiSuggestion(suggestion)}
                        >
                          {suggestion}
                        </button>
                      ))}
                    </div>
                  </div>
                )}

                {aiError && (
                  <div className={styles.inlineNotice} role="status">
                    {aiError}
                  </div>
                )}

                {offPlatformCheck.flagged && (
                  <div className={styles.safetyNotice} role="note">
                    <IconAlert size={14} />
                    <span>
                      Lưu ý: Tránh trao đổi số điện thoại hoặc dẫn khách giao dịch ngoài nền tảng
                      NexusMart.
                    </span>
                  </div>
                )}

                {sendError && (
                  <div className={styles.sendErrorBar} role="alert">
                    {sendError}
                  </div>
                )}

                {attachMode !== "none" && (
                  <form className={styles.attachBar} onSubmit={handleSendAttachment}>
                    {attachMode === "image" ? (
                      <input
                        type="text"
                        className={styles.attachInput}
                        value={attachUrl}
                        onChange={(e) => setAttachUrl(e.target.value)}
                        placeholder="Liên kết ảnh (/products/... hoặc https://...)"
                        aria-label="Liên kết ảnh cần gửi"
                      />
                    ) : (
                      <>
                        <input
                          type="text"
                          className={styles.attachInput}
                          value={attachRefId}
                          onChange={(e) => setAttachRefId(e.target.value)}
                          placeholder={
                            attachMode === "product" ? "Mã sản phẩm (ID)..." : "Mã đơn hàng (ID)..."
                          }
                          aria-label={attachMode === "product" ? "Mã sản phẩm" : "Mã đơn hàng"}
                        />
                        <input
                          type="text"
                          className={styles.attachInput}
                          value={attachLabel}
                          onChange={(e) => setAttachLabel(e.target.value)}
                          placeholder={
                            attachMode === "product"
                              ? "Tên hiển thị sản phẩm (tuỳ chọn)..."
                              : "Ghi chú mã đơn (tuỳ chọn)..."
                          }
                          aria-label="Mô tả đính kèm"
                        />
                      </>
                    )}
                    <Button type="submit" variant="ink" size="sm" shape="app" disabled={sending}>
                      Gửi thẻ
                    </Button>
                    <button
                      type="button"
                      className={styles.aiDismissBtn}
                      onClick={() => {
                        setAttachMode("none");
                        setSendError(null);
                      }}
                    >
                      Hủy
                    </button>
                  </form>
                )}

                <form className={styles.composerArea} onSubmit={handleSendText}>
                  <div className={styles.composerTools}>
                    <button
                      type="button"
                      className={styles.aiTriggerBtn}
                      onClick={() => void handleSuggestAiReplies()}
                      disabled={aiLoading}
                    >
                      <IconSpark size={13} />
                      <span>{aiLoading ? "Đang tạo gợi ý..." : "Gợi ý trả lời AI"}</span>
                    </button>

                    <button
                      type="button"
                      className={`${styles.toolChipBtn} ${attachMode === "image" ? styles.toolChipActive : ""}`}
                      onClick={() => setAttachMode((m) => (m === "image" ? "none" : "image"))}
                    >
                      Gửi ảnh
                    </button>
                    <button
                      type="button"
                      className={`${styles.toolChipBtn} ${attachMode === "product" ? styles.toolChipActive : ""}`}
                      onClick={() => setAttachMode((m) => (m === "product" ? "none" : "product"))}
                    >
                      Thẻ sản phẩm
                    </button>
                    <button
                      type="button"
                      className={`${styles.toolChipBtn} ${attachMode === "order" ? styles.toolChipActive : ""}`}
                      onClick={() => setAttachMode((m) => (m === "order" ? "none" : "order"))}
                    >
                      Mã đơn hàng
                    </button>
                  </div>

                  <div className={styles.quickReplies} aria-label="Mẫu trả lời nhanh">
                    {QUICK_REPLIES.map((tpl) => (
                      <button
                        key={tpl.label}
                        type="button"
                        className={styles.quickReplyBtn}
                        onClick={() => {
                          setDraft(tpl.text);
                          composerInputRef.current?.focus();
                        }}
                      >
                        {tpl.label}
                      </button>
                    ))}
                  </div>

                  <div className={styles.composerRow}>
                    <input
                      ref={composerInputRef}
                      type="text"
                      className={styles.composerInput}
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      placeholder={`Nhắn phản hồi cho ${activeConv.buyer_name}...`}
                      aria-label="Nhập nội dung phản hồi"
                      maxLength={2000}
                    />
                    <Button
                      type="submit"
                      variant="ink"
                      size="sm"
                      shape="app"
                      disabled={!draft.trim() || sending}
                    >
                      <IconSend size={14} />
                      <span>Gửi phản hồi</span>
                    </Button>
                  </div>
                </form>
              </>
            ) : (
              <div className={styles.emptyPaneHint}>
                <p className={common.cardMeta}>Chọn một hội thoại bên trái để xem và phản hồi.</p>
              </div>
            )}
          </div>
        </section>
      )}
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
