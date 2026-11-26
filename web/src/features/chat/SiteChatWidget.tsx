"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { AlertTriangle, ImagePlus, MessageCircle, Package, RefreshCw, Send, X } from "lucide-react";
import { useSession } from "@/components/SessionProvider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { cn } from "@/lib/utils";
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

export interface SiteChatWidgetComponentProps extends SiteChatWidgetProps {
  /** Render directly inside SiteSupportDock without a standalone fixed launcher. */
  embedded?: boolean;
  /** Whether the embedded panel is currently visible (preserves draft/history when hidden). */
  active?: boolean;
  /** Invoked when user closes the panel in embedded mode. */
  onClose?: () => void;
}

function hasActiveModalDialog(): boolean {
  if (typeof document === "undefined") return false;
  return Boolean(document.querySelector('[role="dialog"][aria-modal="true"]'));
}

function focusFirstInteractive(container: HTMLElement): void {
  const first = container.querySelector<HTMLElement>(
    'button:not([disabled]), a[href], input:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
  );
  (first ?? container).focus({ preventScroll: true });
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
      return { label: "Đang đồng bộ", tone: "sync" };
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
  embedded = false,
  active = true,
  onClose,
}: SiteChatWidgetComponentProps) {
  const { user, isAuthenticated } = useSession();
  const pathname = usePathname();

  const [isOpen, setIsOpen] = useState(embedded ? true : defaultOpen);
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
  const panelRef = useRef<HTMLElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const wasOpenRef = useRef(false);

  conversationIdRef.current = conversationId;
  const effectiveOpen = embedded ? active : isOpen;

  const handleClose = useCallback(() => {
    if (onClose) {
      onClose();
    } else {
      setIsOpen(false);
    }
  }, [onClose]);

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
    if (!embedded) setIsOpen(true);
  }, [embedded, externalConversationId]);

  // Listen for custom handoff event from SiteAssistantWidget.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const handleOpenEvent = (ev: Event) => {
      const custom = ev as CustomEvent<OpenSiteChatDetail | undefined>;
      const targetId = custom.detail?.conversationId?.trim();
      if (targetId) {
        setConversationId(targetId);
      }
      if (!embedded) setIsOpen(true);
    };
    window.addEventListener(OPEN_SITE_CHAT_EVENT, handleOpenEvent);
    return () => window.removeEventListener(OPEN_SITE_CHAT_EVENT, handleOpenEvent);
  }, [embedded]);

  // Close on Escape key while open (standalone mode; embedded mode lets dock handle Escape).
  useEffect(() => {
    if (!effectiveOpen || embedded) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !hasActiveModalDialog()) {
        ev.preventDefault();
        handleClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [effectiveOpen, embedded, handleClose]);

  // Focus inside panel on open; restore focus to standalone launcher on close.
  useEffect(() => {
    if (effectiveOpen) {
      wasOpenRef.current = true;
      const input = inputRef.current;
      if (input && !input.disabled) {
        input.focus({ preventScroll: true });
      } else if (panelRef.current) {
        focusFirstInteractive(panelRef.current);
      }
      return;
    }
    if (wasOpenRef.current) {
      wasOpenRef.current = false;
      if (!embedded && triggerRef.current?.isConnected) {
        triggerRef.current.focus({ preventScroll: true });
      }
    }
  }, [effectiveOpen, embedded]);

  // Scroll to bottom when new messages arrive.
  useEffect(() => {
    if (!effectiveOpen) return;
    listEndRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [effectiveOpen, messages.length]);

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
            : "Kênh nhắn tin tạm gián đoạn. Vui lòng thử lại sau."
        );
      } finally {
        setLoading(false);
      }
    },
    [fetchMessages, isAuthenticated]
  );

  // Load conversation & history when widget opens for a signed-in buyer.
  useEffect(() => {
    if (!effectiveOpen || !isAuthenticated) return;
    void bootstrapConversation();
  }, [effectiveOpen, isAuthenticated, conversationId, shopId, bootstrapConversation]);

  // Connect live messaging channel while widget is open and authenticated.
  useEffect(() => {
    if (!effectiveOpen || !isAuthenticated || !conversationId) {
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
  }, [effectiveOpen, isAuthenticated, conversationId, fetchMessages, user]);

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
            : "Không thể gửi tin nhắn lúc này. Vui lòng thử lại."
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
      className={cn(
        embedded ? "static w-full" : "fixed right-3 z-50 sm:right-5",
        "font-sans text-foreground",
        className
      )}
      style={embedded ? undefined : { bottom: `${offsetBottomPx}px` }}
    >
      {!embedded && !effectiveOpen ? (
        <Button
          ref={triggerRef}
          type="button"
          onClick={() => setIsOpen(true)}
          aria-label={`Nhắn tin với ${brandName}`}
          aria-expanded={false}
          className="h-11 rounded-full px-4 shadow-overlay"
        >
          <MessageCircle className="size-4" aria-hidden="true" />
          <span>Nhắn tin</span>
        </Button>
      ) : (
        <section
          ref={panelRef}
          tabIndex={-1}
          role="dialog"
          aria-label={`Trò chuyện cùng ${brandName}`}
          className="flex h-[min(540px,calc(100svh-128px))] w-[min(380px,calc(100vw-24px))] flex-col overflow-hidden rounded-xl border border-border bg-card text-card-foreground shadow-overlay outline-none animate-in fade-in-0 slide-in-from-bottom-2 duration-200"
        >
          <header className="flex shrink-0 items-center justify-between gap-3 border-b border-border bg-primary px-4 py-3 text-primary-foreground">
            <div className="min-w-0 space-y-0.5">
              <h2 className="truncate text-sm leading-5 font-semibold">{brandName}</h2>
              {isAuthenticated && (
                <span className="inline-flex items-center gap-1.5 text-xs opacity-90">
                  <span
                    className={cn(
                      "size-2 rounded-full",
                      statusInfo.tone === "online"
                        ? "bg-badge-success-ink"
                        : statusInfo.tone === "sync"
                          ? "bg-badge-warning-ink"
                          : "bg-current opacity-60"
                    )}
                    aria-hidden="true"
                  />
                  <span>{statusInfo.label}</span>
                </span>
              )}
            </div>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={handleClose}
              aria-label="Đóng khung trò chuyện"
              className="size-9 shrink-0 text-primary-foreground hover:bg-white/15 hover:text-primary-foreground"
            >
              <X className="size-4" aria-hidden="true" />
            </Button>
          </header>

          {!isAuthenticated ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-8 text-center">
              <div className="flex size-12 items-center justify-center rounded-full bg-muted text-foreground">
                <MessageCircle className="size-5" aria-hidden="true" />
              </div>
              <p className="text-sm font-semibold text-foreground">
                Nhắn trực tiếp cho {brandName}
              </p>
              <p className="max-w-xs text-[13px] leading-5 text-muted-foreground">
                Đăng nhập để hỏi size, chất liệu hoặc kiểm tra nhanh đơn hàng.
              </p>
              <Button asChild size="sm" className="mt-1 min-h-10 shadow-none">
                <Link href={loginHref}>Đăng nhập để nhắn tin</Link>
              </Button>
            </div>
          ) : (
            <>
              {(productContext || orderContext) && (
                <div className="shrink-0 space-y-2 border-b border-border bg-background px-3.5 py-2.5">
                  {productContext && (
                    <div className="flex items-center justify-between gap-2.5">
                      <div className="flex min-w-0 items-center gap-2.5">
                        {safeContextImage && (
                          <img
                            src={safeContextImage}
                            alt={productContext.name}
                            className="h-10 w-8 shrink-0 rounded border border-border object-cover"
                            width={34}
                            height={42}
                          />
                        )}
                        <div className="min-w-0">
                          <p className="truncate text-xs font-medium text-foreground">
                            {productContext.name}
                          </p>
                          <p className="text-xs text-muted-foreground tabular-nums">
                            {formatVnd(productContext.priceVnd)}
                          </p>
                        </div>
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="h-8 shrink-0 shadow-none"
                        onClick={handleSendProductContext}
                        disabled={sending || !conversationId}
                      >
                        Gửi mẫu
                      </Button>
                    </div>
                  )}

                  {orderContext && (
                    <div className="flex items-center justify-between gap-2.5">
                      <div className="flex min-w-0 items-center gap-2.5">
                        <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-foreground">
                          <Package className="size-4" aria-hidden="true" />
                        </span>
                        <div className="min-w-0">
                          <p className="truncate text-xs font-medium text-foreground tabular-nums">
                            Đơn {orderContext.orderCode}
                          </p>
                          <p className="text-xs text-muted-foreground">Đính kèm vào trò chuyện</p>
                        </div>
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="h-8 shrink-0 shadow-none"
                        onClick={handleSendOrderContext}
                        disabled={sending || !conversationId}
                      >
                        Gửi mã đơn
                      </Button>
                    </div>
                  )}
                </div>
              )}

              <div
                className="flex-1 space-y-3 overflow-y-auto bg-background px-3.5 py-3.5"
                role="log"
                aria-live="polite"
              >
                {loading && messages.length === 0 ? (
                  <p className="py-8 text-center text-xs text-muted-foreground">
                    Đang kết nối với {brandName}...
                  </p>
                ) : loadError && messages.length === 0 ? (
                  <div
                    className="space-y-2.5 rounded-lg border border-destructive/30 bg-card p-3.5 text-center"
                    role="alert"
                  >
                    <p className="text-xs text-destructive">{loadError}</p>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="shadow-none"
                      onClick={() => void bootstrapConversation({ force: true })}
                    >
                      <RefreshCw className="size-3.5" aria-hidden="true" />
                      <span>Thử lại</span>
                    </Button>
                  </div>
                ) : messages.length === 0 ? (
                  <p className="py-8 text-center text-xs leading-5 text-muted-foreground">
                    Nhắn cho {brandName} khi bạn cần tư vấn mẫu, kích cỡ hoặc đơn hàng.
                  </p>
                ) : (
                  messages.map((msg) => {
                    const own = isOwnMessage(msg);
                    return (
                      <div
                        key={msg.id}
                        className={cn("flex flex-col gap-1", own ? "items-end" : "items-start")}
                      >
                        <div
                          className={cn(
                            "max-w-[85%] rounded-xl px-3.5 py-2.5 text-[13px] leading-5 break-words",
                            own
                              ? "bg-primary text-primary-foreground"
                              : "border border-border bg-card text-card-foreground"
                          )}
                        >
                          {msg.kind === "image" ? (
                            (() => {
                              const safeImg = sanitizeSafeUrl(msg.body);
                              if (!safeImg) {
                                return (
                                  <span className="text-xs opacity-75">
                                    Ảnh đính kèm không hiển thị được.
                                  </span>
                                );
                              }
                              return (
                                <a
                                  href={safeImg}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  className="block overflow-hidden rounded-lg"
                                >
                                  <img
                                    src={safeImg}
                                    alt="Ảnh đính kèm trong trò chuyện"
                                    className="max-h-52 w-full rounded-lg object-cover"
                                  />
                                </a>
                              );
                            })()
                          ) : msg.kind === "product" ? (
                            (() => {
                              const card = parseProductRefCard(msg);
                              const productHref = `/san-pham/${encodeURIComponent(card.slug || msg.ref_id)}`;
                              return (
                                <Link
                                  href={productHref}
                                  className="flex items-center gap-2.5 rounded-lg border border-border bg-card p-2 text-card-foreground transition-opacity hover:opacity-85"
                                >
                                  {card.image && (
                                    <img
                                      src={card.image}
                                      alt={card.name}
                                      className="h-12 w-10 shrink-0 rounded object-cover"
                                      width={40}
                                      height={48}
                                    />
                                  )}
                                  <span className="flex min-w-0 flex-col gap-0.5">
                                    <span className="text-[11px] text-muted-foreground">
                                      Sản phẩm
                                    </span>
                                    <span className="line-clamp-2 text-xs font-semibold text-foreground">
                                      {card.name}
                                    </span>
                                    {card.priceVnd !== undefined && (
                                      <span className="text-xs font-medium text-brand tabular-nums">
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
                                <Link
                                  href={orderHref}
                                  className="flex items-center gap-2.5 rounded-lg border border-border bg-card p-2 text-card-foreground transition-opacity hover:opacity-85"
                                >
                                  <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-foreground">
                                    <Package className="size-4" aria-hidden="true" />
                                  </span>
                                  <span className="flex min-w-0 flex-col gap-0.5">
                                    <span className="text-[11px] text-muted-foreground">
                                      Đơn hàng
                                    </span>
                                    <span className="truncate text-xs font-semibold text-foreground tabular-nums">
                                      {card.label}
                                    </span>
                                    <span className="text-[11px] font-medium text-brand">
                                      Xem chi tiết đơn
                                    </span>
                                  </span>
                                </Link>
                              );
                            })()
                          ) : (
                            <p className="m-0 whitespace-pre-wrap">{msg.body}</p>
                          )}
                        </div>
                        <span className="px-1 text-[11px] text-muted-foreground tabular-nums">
                          {msg.is_auto_reply ? "Tự động, " : ""}
                          {formatDateTimeVi(msg.created_at)}
                        </span>
                      </div>
                    );
                  })
                )}
                <div ref={listEndRef} />
              </div>

              {offPlatformCheck.flagged && (
                <div
                  className="flex shrink-0 items-start gap-2 border-t border-border bg-badge-warning-bg px-3.5 py-2 text-xs text-badge-warning-ink"
                  role="note"
                >
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                  <span>
                    Không chia sẻ số điện thoại hoặc giao dịch ngoài NexusMart để giữ quyền đổi trả
                    và hoàn tiền.
                  </span>
                </div>
              )}

              {sendError && (
                <div
                  className="shrink-0 border-t border-border bg-badge-critical-bg px-3.5 py-2 text-xs text-badge-critical-ink"
                  role="alert"
                >
                  {sendError}
                </div>
              )}

              {imagePanelOpen && (
                <form
                  className="flex shrink-0 items-center gap-1.5 border-t border-border bg-card px-3 py-2"
                  onSubmit={handleSendImage}
                >
                  <Input
                    type="text"
                    className="h-9 flex-1 bg-background text-xs"
                    value={imageUrl}
                    onChange={(e) => setImageUrl(e.target.value)}
                    placeholder="Dán liên kết ảnh (/... hoặc https://...)"
                    aria-label="Liên kết ảnh cần gửi"
                  />
                  <Button
                    type="submit"
                    size="sm"
                    disabled={sending || !imageUrl.trim()}
                    className="h-9 shrink-0 shadow-none"
                  >
                    Gửi
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-9 shrink-0"
                    onClick={() => {
                      setImagePanelOpen(false);
                      setImageUrl("");
                      setSendError(null);
                    }}
                  >
                    Hủy
                  </Button>
                </form>
              )}

              <form
                className="flex shrink-0 items-center gap-2 border-t border-border bg-card px-3 py-2.5"
                onSubmit={handleSubmitText}
              >
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-10 shrink-0 px-2.5 shadow-none"
                  onClick={() => setImagePanelOpen((v) => !v)}
                  aria-label="Đính kèm ảnh"
                  aria-expanded={imagePanelOpen}
                  disabled={!conversationId}
                >
                  <ImagePlus className="size-4" aria-hidden="true" />
                  <span className="text-xs">Ảnh</span>
                </Button>
                <Input
                  ref={inputRef}
                  type="text"
                  className="h-10 flex-1 bg-background text-sm"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder={`Nhắn cho ${brandName}...`}
                  aria-label={`Nhập tin nhắn cho ${brandName}`}
                  maxLength={2000}
                  disabled={!conversationId || loading}
                />
                <Button
                  type="submit"
                  size="icon"
                  className="size-10 shrink-0 shadow-none"
                  disabled={!draft.trim() || sending || !conversationId}
                  aria-label="Gửi tin nhắn"
                >
                  <Send className="size-4" aria-hidden="true" />
                </Button>
              </form>
            </>
          )}
        </section>
      )}
    </div>
  );
}
