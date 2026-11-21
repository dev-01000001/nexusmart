"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  IconAlert,
  IconChat,
  IconClose,
  IconPackage,
  IconRefresh,
  IconSend,
} from "@/components/Icons";
import { useSession } from "@/components/SessionProvider";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { createChatSocketManager, type ChatSocketManager } from "./chatClient";
import {
  OPEN_SITE_CHAT_EVENT,
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
  type OpenSiteChatDetail,
  type SiteChatWidgetProps,
  type WsIncomingEvent,
} from "./chatTypes";
import styles from "./SiteChatWidget.module.css";

interface MessagesApiResponse {
  conversation?: unknown;
  messages?: unknown[];
}

interface ConversationApiResponse {
  conversation?: unknown;
}

interface SendMessageApiResponse {
  message?: unknown;
  auto_reply?: unknown;
}

function createClientMsgId(): string {
  return `cli-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
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
      return { label: "Sẵn sàng", tone: "idle" };
  }
}

export function SiteChatWidget({
  shopId,
  brandName,
  productContext,
  orderContext,
  externalConversationId,
  defaultOpen = false,
  offsetBottomPx = 20,
  className,
}: SiteChatWidgetProps) {
  const { user, isAuthenticated } = useSession();
  const pathname = usePathname();

  const [isOpen, setIsOpen] = useState(defaultOpen);
  const [conversationId, setConversationId] = useState<string>(externalConversationId ?? "");
  const [messages, setMessages] = useState<ChatMessageDTO[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [sendError, setSendError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [draft, setDraft] = useState("");
  const [imagePanelOpen, setImagePanelOpen] = useState(false);
  const [imageUrl, setImageUrl] = useState("");
  const [connStatus, setConnStatus] = useState<ChatConnectionStatus>("idle");

  const socketRef = useRef<ChatSocketManager | null>(null);
  const conversationIdRef = useRef<string>(conversationId);
  const lastServerMsgIdRef = useRef<string>("");
  const listEndRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  conversationIdRef.current = conversationId;

  useEffect(() => {
    const serverMsgs = messages.filter((m) => !m.id.startsWith("cli-"));
    const latest = serverMsgs[serverMsgs.length - 1];
    if (latest) {
      lastServerMsgIdRef.current = latest.id;
    }
  }, [messages]);

  // React to external conversation ID passed from parent (e.g. assistant handoff).
  useEffect(() => {
    const nextId = (externalConversationId ?? "").trim();
    if (!nextId) return;
    setConversationId(nextId);
    setIsOpen(true);
  }, [externalConversationId]);

  // Listen for custom handoff event from SiteAssistantWidget.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const handleOpenEvent = (ev: Event) => {
      const custom = ev as CustomEvent<OpenSiteChatDetail | undefined>;
      const targetId = custom.detail?.conversationId?.trim();
      if (targetId) {
        setConversationId(targetId);
      }
      setIsOpen(true);
    };
    window.addEventListener(OPEN_SITE_CHAT_EVENT, handleOpenEvent);
    return () => window.removeEventListener(OPEN_SITE_CHAT_EVENT, handleOpenEvent);
  }, []);

  // Close on Escape key while open.
  useEffect(() => {
    if (!isOpen) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        setIsOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [isOpen]);

  // Scroll to bottom when new messages arrive.
  useEffect(() => {
    if (!isOpen) return;
    listEndRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [isOpen, messages.length]);

  const fetchMessages = useCallback(async (convId: string, afterId?: string) => {
    const query = afterId ? `?after=${encodeURIComponent(afterId)}` : "?limit=50";
    const res = await api<MessagesApiResponse>(
      `/api/chat/conversations/${encodeURIComponent(convId)}/messages${query}`
    );
    const parsed = (res.messages ?? [])
      .map((item) => normalizeMessage(item))
      .filter((m): m is ChatMessageDTO => m !== null);
    setMessages((prev) => (afterId ? mergeMessages(prev, parsed) : mergeMessages([], parsed)));
  }, []);

  const loadedConvIdRef = useRef<string>("");

  const bootstrapConversation = useCallback(
    async (opts: { force?: boolean } = {}) => {
      if (!isAuthenticated) return;
      const existingId = conversationIdRef.current.trim();
      if (!opts.force && existingId && loadedConvIdRef.current === existingId) {
        return;
      }
      setLoading(true);
      setLoadError(null);
      try {
        let targetId = existingId;
        if (!targetId) {
          const created = await api<ConversationApiResponse>("/api/chat/conversations", {
            method: "POST",
            body: {},
          });
          const conv: ChatConversationDTO | null = normalizeConversation(created.conversation);
          if (!conv) {
            throw new Error("Không thể khởi tạo cuộc trò chuyện.");
          }
          targetId = conv.id;
          conversationIdRef.current = targetId;
          setConversationId(targetId);
        }
        await fetchMessages(targetId);
        loadedConvIdRef.current = targetId;
      } catch (err) {
        setLoadError(
          err instanceof ApiError
            ? err.message
            : "Kênh nhắn tin tạm gián đoạn. Bạn vui lòng thử lại sau."
        );
      } finally {
        setLoading(false);
      }
    },
    [fetchMessages, isAuthenticated]
  );

  // Load conversation & history when widget opens for a signed-in buyer.
  useEffect(() => {
    if (!isOpen || !isAuthenticated) return;
    void bootstrapConversation();
  }, [isOpen, isAuthenticated, conversationId, shopId, bootstrapConversation]);

  // Connect live messaging channel while widget is open and authenticated.
  useEffect(() => {
    if (!isOpen || !isAuthenticated || !conversationId) {
      socketRef.current?.dispose();
      socketRef.current = null;
      setConnStatus("idle");
      return;
    }

    const handleWsEvent = (ev: WsIncomingEvent) => {
      const activeId = conversationIdRef.current;
      if (ev.type === "message") {
        if (ev.conversation_id && ev.conversation_id !== activeId) return;
        setMessages((prev) => mergeMessages(prev, [ev.message]));
        if (user && ev.message.sender_id && ev.message.sender_id !== user.id) {
          socketRef.current?.sendRead(activeId, ev.message.seq, ev.message.id);
        }
      } else if (ev.type === "ack") {
        if (ev.conversation_id && ev.conversation_id !== activeId) return;
        if (ev.message) {
          setMessages((prev) => mergeMessages(prev, [ev.message!]));
        } else if (ev.client_msg_id) {
          setMessages((prev) =>
            prev.map((m) =>
              m.client_msg_id === ev.client_msg_id
                ? { ...m, id: ev.message_id, ...(ev.seq ? { seq: ev.seq } : {}) }
                : m
            )
          );
        }
      } else if (ev.type === "error") {
        setSendError(ev.message || "Không thể gửi tin nhắn lúc này.");
      }
    };

    const manager = createChatSocketManager({
      ticketEndpoint: "/api/chat/ws-ticket",
      onMessage: handleWsEvent,
      onStatusChange: (next) => setConnStatus(next),
      onReconnectSync: async () => {
        const activeId = conversationIdRef.current;
        if (!activeId) return;
        try {
          await fetchMessages(activeId, lastServerMsgIdRef.current || undefined);
        } catch {
          // Silent catch-up retry on next sync
        }
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
  }, [isOpen, isAuthenticated, conversationId, fetchMessages, user]);

  const offPlatformCheck = useMemo(() => detectOffPlatformContact(draft), [draft]);

  const dispatchOutgoing = useCallback(
    async (payload: {
      kind: ChatMessageKind;
      body: string;
      ref_id?: string;
      metadata?: Record<string, unknown>;
    }): Promise<boolean> => {
      const activeId = conversationIdRef.current.trim();
      if (!activeId || !user) return false;

      const validated = validateOutgoingMessage(payload);
      if (!validated.ok) {
        setSendError(validated.message);
        return false;
      }

      setSendError(null);
      const clientMsgId = createClientMsgId();
      const optimistic: ChatMessageDTO = {
        id: clientMsgId,
        conversation_id: activeId,
        sender_id: user.id,
        sender_role: "buyer",
        kind: validated.value.kind,
        body: validated.value.body,
        ref_id: validated.value.ref_id,
        client_msg_id: clientMsgId,
        metadata: validated.value.metadata,
        created_at: new Date().toISOString(),
      };

      const sentLive = socketRef.current?.send({
        conversation_id: activeId,
        kind: validated.value.kind,
        body: validated.value.body,
        ref_id: validated.value.ref_id,
        client_msg_id: clientMsgId,
        metadata: validated.value.metadata,
      });

      if (sentLive) {
        setMessages((prev) => mergeMessages(prev, [optimistic]));
        return true;
      }

      // HTTP fallback when live channel is not yet open.
      setSending(true);
      try {
        const res = await api<SendMessageApiResponse>(
          `/api/chat/conversations/${encodeURIComponent(activeId)}/messages`,
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
        const incoming: ChatMessageDTO[] = [confirmed];
        const autoReply = res.auto_reply ? normalizeMessage(res.auto_reply) : null;
        if (autoReply) incoming.push(autoReply);
        setMessages((prev) => mergeMessages(prev, incoming));
        return true;
      } catch (err) {
        setSendError(
          err instanceof ApiError
            ? err.message
            : "Không thể gửi tin nhắn lúc này. Bạn vui lòng thử lại."
        );
        return false;
      } finally {
        setSending(false);
      }
    },
    [user]
  );

  const handleSubmitText = async (ev: React.FormEvent) => {
    ev.preventDefault();
    const clean = draft.trim();
    if (!clean || sending) return;
    const ok = await dispatchOutgoing({ kind: "text", body: clean });
    if (ok) {
      setDraft("");
      inputRef.current?.focus();
    }
  };

  const handleSendImage = async (ev: React.FormEvent) => {
    ev.preventDefault();
    if (sending) return;
    const ok = await dispatchOutgoing({ kind: "image", body: imageUrl.trim() });
    if (ok) {
      setImageUrl("");
      setImagePanelOpen(false);
    }
  };

  const handleSendProductContext = async () => {
    if (!productContext || sending) return;
    await dispatchOutgoing({
      kind: "product",
      ref_id: productContext.id,
      body: `${productContext.name} (${formatVnd(productContext.priceVnd)})`,
      metadata: {
        name: productContext.name,
        slug: productContext.slug ?? "",
        price_vnd: productContext.priceVnd,
        image: productContext.image ?? "",
      },
    });
  };

  const handleSendOrderContext = async () => {
    if (!orderContext || sending) return;
    await dispatchOutgoing({
      kind: "order",
      ref_id: orderContext.id,
      body: `Đơn hàng ${orderContext.orderCode}`,
      metadata: {
        order_code: orderContext.orderCode,
      },
    });
  };

  const loginHref = `/dang-nhap?next=${encodeURIComponent(pathname || "/")}`;
  const statusInfo = connectionStatusMeta(connStatus);
  const safeContextImage = sanitizeSafeUrl(productContext?.image);

  const isOwnMessage = (msg: ChatMessageDTO): boolean => {
    if (msg.sender_role === "buyer") return true;
    if (msg.sender_role === "seller" || msg.sender_role === "system") return false;
    return Boolean(user && msg.sender_id === user.id);
  };

  return (
    <div
      data-ui="site-chat"
      data-shop-id={shopId}
      className={`${styles.root} ${className ?? ""}`}
      style={{ bottom: `${offsetBottomPx}px` }}
    >
      {!isOpen ? (
        <button
          type="button"
          className={styles.triggerBtn}
          onClick={() => setIsOpen(true)}
          aria-label={`Nhắn tin với ${brandName}`}
          aria-expanded={false}
        >
          <IconChat size={18} />
          <span>Nhắn tin</span>
        </button>
      ) : (
        <section className={styles.panel} role="dialog" aria-label={`Trò chuyện cùng ${brandName}`}>
          <header className={styles.header}>
            <div className={styles.headerMeta}>
              <h2 className={styles.headerTitle}>{brandName}</h2>
              {isAuthenticated && (
                <span className={styles.statusLine}>
                  <span
                    className={`${styles.statusDot} ${
                      statusInfo.tone === "online"
                        ? styles.statusDotOnline
                        : statusInfo.tone === "sync"
                          ? styles.statusDotSync
                          : styles.statusDotIdle
                    }`}
                    aria-hidden="true"
                  />
                  <span>{statusInfo.label}</span>
                </span>
              )}
            </div>
            <button
              type="button"
              className={styles.closeBtn}
              onClick={() => setIsOpen(false)}
              aria-label="Đóng khung trò chuyện"
            >
              <IconClose size={16} />
            </button>
          </header>

          {!isAuthenticated ? (
            <div className={styles.guestBody}>
              <div className={styles.guestIcon}>
                <IconChat size={22} />
              </div>
              <p className={styles.guestTitle}>Trò chuyện trực tiếp cùng {brandName}</p>
              <p className={styles.guestDesc}>
                Đăng nhập để hỏi tư vấn kích cỡ, chất liệu, hoặc kiểm tra nhanh đơn hàng của bạn.
              </p>
              <Link href={loginHref} className={styles.loginBtn}>
                Đăng nhập để nhắn tin
              </Link>
            </div>
          ) : (
            <>
              {(productContext || orderContext) && (
                <div className={styles.contextBar}>
                  {productContext && (
                    <div className={styles.contextItem}>
                      <div className={styles.contextMain}>
                        {safeContextImage && (
                          <img
                            src={safeContextImage}
                            alt={productContext.name}
                            className={styles.contextThumb}
                            width={34}
                            height={42}
                          />
                        )}
                        <div className={styles.contextText}>
                          <span className={styles.contextName}>{productContext.name}</span>
                          <span className={styles.contextSub}>
                            {formatVnd(productContext.priceVnd)}
                          </span>
                        </div>
                      </div>
                      <button
                        type="button"
                        className={styles.contextActionBtn}
                        onClick={handleSendProductContext}
                        disabled={sending || !conversationId}
                      >
                        Gửi sản phẩm
                      </button>
                    </div>
                  )}

                  {orderContext && (
                    <div className={styles.contextItem}>
                      <div className={styles.contextMain}>
                        <span className={styles.contextOrderIcon}>
                          <IconPackage size={15} />
                        </span>
                        <div className={styles.contextText}>
                          <span className={styles.contextName}>Đơn {orderContext.orderCode}</span>
                          <span className={styles.contextSub}>Đính kèm vào cuộc trò chuyện</span>
                        </div>
                      </div>
                      <button
                        type="button"
                        className={styles.contextActionBtn}
                        onClick={handleSendOrderContext}
                        disabled={sending || !conversationId}
                      >
                        Gửi mã đơn
                      </button>
                    </div>
                  )}
                </div>
              )}

              <div className={styles.messages} role="log" aria-live="polite">
                {loading && messages.length === 0 ? (
                  <p className={styles.emptyNotice}>Đang mở cuộc trò chuyện với {brandName}...</p>
                ) : loadError && messages.length === 0 ? (
                  <div className={styles.errorBox} role="alert">
                    <p className={styles.errorText}>{loadError}</p>
                    <button
                      type="button"
                      className={styles.retryBtn}
                      onClick={() => void bootstrapConversation({ force: true })}
                    >
                      <IconRefresh size={13} />
                      <span>Thử lại</span>
                    </button>
                  </div>
                ) : messages.length === 0 ? (
                  <p className={styles.emptyNotice}>
                    Chào bạn, hãy nhắn cho {brandName} nếu bạn cần tư vấn sản phẩm, kích cỡ hoặc đơn
                    hàng.
                  </p>
                ) : (
                  messages.map((msg) => {
                    const own = isOwnMessage(msg);
                    return (
                      <div
                        key={msg.id}
                        className={`${styles.msgRow} ${own ? styles.msgOwn : styles.msgShop}`}
                      >
                        <div
                          className={`${styles.bubble} ${own ? styles.bubbleOwn : styles.bubbleShop}`}
                        >
                          {msg.kind === "image" ? (
                            (() => {
                              const safeImg = sanitizeSafeUrl(msg.body);
                              if (!safeImg) {
                                return (
                                  <span className={styles.mutedInline}>
                                    Ảnh đính kèm không hiển thị được.
                                  </span>
                                );
                              }
                              return (
                                <a
                                  href={safeImg}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  className={styles.imageLink}
                                >
                                  <img
                                    src={safeImg}
                                    alt="Ảnh đính kèm trong trò chuyện"
                                    className={styles.msgImage}
                                  />
                                </a>
                              );
                            })()
                          ) : msg.kind === "product" ? (
                            (() => {
                              const card = parseProductRefCard(msg);
                              const productHref = `/san-pham/${encodeURIComponent(card.slug || msg.ref_id)}`;
                              return (
                                <Link href={productHref} className={styles.refCard}>
                                  {card.image && (
                                    <img
                                      src={card.image}
                                      alt={card.name}
                                      className={styles.refThumb}
                                      width={40}
                                      height={48}
                                    />
                                  )}
                                  <span className={styles.refCardBody}>
                                    <span className={styles.refCardKind}>Sản phẩm</span>
                                    <span className={styles.refCardTitle}>{card.name}</span>
                                    {card.priceVnd !== undefined && (
                                      <span className={styles.refCardPrice}>
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
                              const orderHref = `/don-hang/${encodeURIComponent(msg.ref_id)}`;
                              return (
                                <Link href={orderHref} className={styles.refCard}>
                                  <span className={styles.refOrderIcon}>
                                    <IconPackage size={16} />
                                  </span>
                                  <span className={styles.refCardBody}>
                                    <span className={styles.refCardKind}>Đơn hàng</span>
                                    <span className={styles.refCardTitle}>{card.label}</span>
                                    <span className={styles.refCardMeta}>Xem chi tiết đơn</span>
                                  </span>
                                </Link>
                              );
                            })()
                          ) : (
                            <p className={styles.msgText}>{msg.body}</p>
                          )}
                        </div>
                        <span className={styles.msgTime}>
                          {msg.is_auto_reply ? "Tin nhắn tự động · " : ""}
                          {formatDateTimeVi(msg.created_at)}
                        </span>
                      </div>
                    );
                  })
                )}
                <div ref={listEndRef} />
              </div>

              {offPlatformCheck.flagged && (
                <div className={styles.safetyBanner} role="note">
                  <IconAlert size={14} className={styles.safetyIcon} />
                  <span>
                    Lưu ý an toàn: Không chia sẻ số điện thoại hoặc giao dịch qua liên kết ngoài
                    NexusMart để được bảo vệ quyền lợi đổi trả và hoàn tiền.
                  </span>
                </div>
              )}

              {sendError && (
                <div className={styles.sendError} role="alert">
                  {sendError}
                </div>
              )}

              {imagePanelOpen && (
                <form className={styles.imageForm} onSubmit={handleSendImage}>
                  <input
                    type="text"
                    className={styles.imageInput}
                    value={imageUrl}
                    onChange={(e) => setImageUrl(e.target.value)}
                    placeholder="Dán liên kết ảnh (/... hoặc https://...)"
                    aria-label="Liên kết ảnh cần gửi"
                  />
                  <button
                    type="submit"
                    className={styles.imageSubmitBtn}
                    disabled={sending || !imageUrl.trim()}
                  >
                    Gửi ảnh
                  </button>
                  <button
                    type="button"
                    className={styles.imageCancelBtn}
                    onClick={() => {
                      setImagePanelOpen(false);
                      setImageUrl("");
                      setSendError(null);
                    }}
                  >
                    Hủy
                  </button>
                </form>
              )}

              <form className={styles.composer} onSubmit={handleSubmitText}>
                <button
                  type="button"
                  className={styles.attachBtn}
                  onClick={() => setImagePanelOpen((v) => !v)}
                  aria-label="Đính kèm ảnh"
                  aria-expanded={imagePanelOpen}
                  disabled={!conversationId}
                >
                  Ảnh
                </button>
                <input
                  ref={inputRef}
                  type="text"
                  className={styles.input}
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder={`Nhắn cho ${brandName}...`}
                  aria-label={`Nhập tin nhắn cho ${brandName}`}
                  maxLength={2000}
                  disabled={!conversationId || loading}
                />
                <button
                  type="submit"
                  className={styles.sendBtn}
                  disabled={!draft.trim() || sending || !conversationId}
                  aria-label="Gửi tin nhắn"
                >
                  <IconSend size={15} />
                </button>
              </form>
            </>
          )}
        </section>
      )}
    </div>
  );
}
