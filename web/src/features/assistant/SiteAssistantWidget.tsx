"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { IconBag, IconChat, IconClose, IconSend, IconSpark } from "@/components/Icons";
import { useSession } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { ApiError, api } from "@/lib/api";
import { formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import {
  extractToolCardData,
  getToolActivityLabel,
  sanitizeAssistantError,
  type AssistantChatInputMessage,
  type AssistantCitation,
  type AssistantMessage,
  type AssistantOrderCard,
  type AssistantPendingCartAction,
  type AssistantProductCard,
  type SiteAssistantWidgetProps,
} from "./assistantTypes";
import { createSseParser } from "./sseParser";
import styles from "./SiteAssistantWidget.module.css";

interface HandoffApiResponse {
  conversation_id: string;
  summary?: string;
  estimated_response?: string;
}

function makeMessageId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
}

function mergeUniqueProducts(
  prev: AssistantProductCard[] | undefined,
  next: AssistantProductCard[]
): AssistantProductCard[] | undefined {
  if (next.length === 0) return prev;
  const list = [...(prev ?? [])];
  const seen = new Set(list.map((p) => p.id || p.slug));
  for (const item of next) {
    const key = item.id || item.slug;
    if (seen.has(key)) continue;
    seen.add(key);
    list.push(item);
  }
  return list.slice(0, 6);
}

function mergeUniqueOrders(
  prev: AssistantOrderCard[] | undefined,
  next: AssistantOrderCard[]
): AssistantOrderCard[] | undefined {
  if (next.length === 0) return prev;
  const list = [...(prev ?? [])];
  const seen = new Set(list.map((o) => o.orderCode));
  for (const item of next) {
    if (seen.has(item.orderCode)) continue;
    seen.add(item.orderCode);
    list.push(item);
  }
  return list.slice(0, 4);
}

function mergeUniqueCitations(
  prev: AssistantCitation[] | undefined,
  next: AssistantCitation[]
): AssistantCitation[] | undefined {
  if (next.length === 0) return prev;
  const list = [...(prev ?? [])];
  const seen = new Set(list.map((c) => `${c.href}|${c.title}`));
  for (const item of next) {
    const key = `${item.href}|${item.title}`;
    if (seen.has(key)) continue;
    seen.add(key);
    list.push(item);
  }
  return list.slice(0, 5);
}

function mergePendingCartActions(
  prev: AssistantPendingCartAction[] | undefined,
  next: AssistantPendingCartAction[]
): AssistantPendingCartAction[] | undefined {
  if (next.length === 0) return prev;
  const list = [...(prev ?? [])];
  const seen = new Set(list.map((a) => `${a.skuId}|${a.quantity}`));
  for (const item of next) {
    const key = `${item.skuId}|${item.quantity}`;
    if (seen.has(key)) continue;
    seen.add(key);
    list.push(item);
  }
  return list.slice(0, 3);
}

export function SiteAssistantWidget({
  shopId,
  brandName,
  productContext,
  onHandoffToChat,
  defaultOpen = false,
  offsetBottomPx = 76,
  className,
}: SiteAssistantWidgetProps) {
  const pathname = usePathname();
  const { isAuthenticated } = useSession();
  const { addToCart, showToast } = useStore();

  const [open, setOpen] = useState(defaultOpen);
  const [messages, setMessages] = useState<AssistantMessage[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [handoffBusy, setHandoffBusy] = useState(false);
  const [handoffError, setHandoffError] = useState<string | null>(null);
  const [handoffLoginHint, setHandoffLoginHint] = useState(false);
  const [handoffSummary, setHandoffSummary] = useState<string | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const lastShopIdRef = useRef(shopId);
  const conversationIdRef = useRef<string>("");

  const loginHref = useMemo(() => {
    const nextPath = pathname && pathname.startsWith("/") ? pathname : "/";
    return `/dang-nhap?next=${encodeURIComponent(nextPath)}`;
  }, [pathname]);

  const starterPrompts = useMemo(() => {
    if (productContext?.name) {
      return [
        `Tư vấn chọn size và chất liệu cho ${productContext.name}`,
        "Gợi ý áo mặc mát dưới 300k",
        "Chính sách đổi trả của cửa hàng",
        "Kiểm tra đơn hàng gần nhất của tôi",
      ];
    }
    return [
      "Gợi ý áo mặc mát dưới 300k",
      "Chính sách đổi trả của cửa hàng",
      "Kiểm tra đơn hàng gần nhất của tôi",
    ];
  }, [productContext?.name]);

  // Reset conversation state if shopId switches across tenant contexts.
  useEffect(() => {
    if (lastShopIdRef.current !== shopId) {
      lastShopIdRef.current = shopId;
      conversationIdRef.current = "";
      abortRef.current?.abort();
      setMessages([]);
      setStreaming(false);
      setHandoffError(null);
      setHandoffLoginHint(false);
      setHandoffSummary(null);
    }
  }, [shopId]);

  // Abort active request cleanly on unmount.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  // Scroll transcript to latest message/chunk.
  useEffect(() => {
    if (!open) return;
    const el = scrollRef.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
    }
  }, [messages, open]);

  // Escape key closes panel.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  // Focus composer when opening panel.
  useEffect(() => {
    if (open) {
      inputRef.current?.focus();
    }
  }, [open]);

  const stopStreaming = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
  }, []);

  const sendMessage = useCallback(
    async (rawText: string) => {
      const trimmed = rawText.trim();
      if (!trimmed || streaming) return;

      setHandoffError(null);
      setHandoffLoginHint(false);
      setInput("");

      const userMsg: AssistantMessage = {
        id: makeMessageId("user"),
        role: "user",
        content: trimmed,
      };
      const assistantId = makeMessageId("assistant");
      const assistantPlaceholder: AssistantMessage = {
        id: assistantId,
        role: "assistant",
        content: "",
        streaming: true,
        toolStatus: null,
      };

      const historyForApi: AssistantChatInputMessage[] = [
        ...messages
          .filter((m) => (m.role === "user" || m.role === "assistant") && m.content.trim())
          .map((m): AssistantChatInputMessage => ({ role: m.role, content: m.content.trim() })),
        { role: "user" as const, content: trimmed },
      ].slice(-30);

      setMessages((prev) => [...prev, userMsg, assistantPlaceholder]);
      setStreaming(true);

      const controller = new AbortController();
      abortRef.current = controller;

      const patchAssistant = (updater: (msg: AssistantMessage) => AssistantMessage) => {
        setMessages((prev) => prev.map((m) => (m.id === assistantId ? updater(m) : m)));
      };

      try {
        const res = await fetch("/api/assistant/chat", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "text/event-stream",
          },
          credentials: "same-origin",
          cache: "no-store",
          body: JSON.stringify({ messages: historyForApi }),
          signal: controller.signal,
        });

        if (!res.ok) {
          const errPayload = (await res.json().catch(() => null)) as {
            error?: { code?: string; message?: string };
          } | null;
          const safeMsg = sanitizeAssistantError(
            errPayload?.error?.code,
            errPayload?.error?.message
          );
          patchAssistant((m) => ({
            ...m,
            streaming: false,
            toolStatus: null,
            error: safeMsg,
          }));
          return;
        }

        if (!res.body) {
          patchAssistant((m) => ({
            ...m,
            streaming: false,
            toolStatus: null,
            error: sanitizeAssistantError("UNAVAILABLE"),
          }));
          return;
        }

        const parser = createSseParser((ev) => {
          if (ev.type === "token") {
            patchAssistant((m) => ({
              ...m,
              content: m.content + ev.text,
              toolStatus: null,
            }));
          } else if (ev.type === "tool_call") {
            patchAssistant((m) => ({
              ...m,
              toolStatus: getToolActivityLabel(ev.name),
            }));
          } else if (ev.type === "tool_result") {
            const extracted = extractToolCardData(ev);
            patchAssistant((m) => ({
              ...m,
              toolStatus: null,
              products: mergeUniqueProducts(m.products, extracted.products),
              pendingCartActions: mergePendingCartActions(
                m.pendingCartActions,
                extracted.pendingCartActions
              ),
              orders: mergeUniqueOrders(m.orders, extracted.orders),
              citations: mergeUniqueCitations(m.citations, extracted.citations),
              requiresAuthPrompt: m.requiresAuthPrompt || extracted.requiresAuth,
            }));
          } else if (ev.type === "done") {
            if (ev.conversationId) {
              conversationIdRef.current = ev.conversationId;
            }
            patchAssistant((m) => ({
              ...m,
              streaming: false,
              toolStatus: null,
            }));
          } else if (ev.type === "error") {
            patchAssistant((m) => ({
              ...m,
              streaming: false,
              toolStatus: null,
              error: sanitizeAssistantError(ev.code, ev.message),
            }));
          }
        });

        const reader = res.body.getReader();
        const decoder = new TextDecoder("utf-8");
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          const chunk = decoder.decode(value, { stream: true });
          if (chunk) parser.feed(chunk);
        }
        const tail = decoder.decode();
        if (tail) parser.feed(tail);
        parser.flush();

        patchAssistant((m) => ({
          ...m,
          streaming: false,
          toolStatus: null,
          content:
            m.content ||
            m.error ||
            m.products?.length ||
            m.orders?.length ||
            m.pendingCartActions?.length
              ? m.content
              : "Mình đã tiếp nhận câu hỏi nhưng chưa có nội dung trả lời phù hợp. Bạn thử diễn đạt cụ thể hơn nhé.",
        }));
      } catch (err) {
        if (err instanceof Error && err.name === "AbortError") {
          patchAssistant((m) => ({
            ...m,
            streaming: false,
            toolStatus: null,
            content: m.content || "Đã dừng phản hồi theo yêu cầu của bạn.",
          }));
        } else {
          patchAssistant((m) => ({
            ...m,
            streaming: false,
            toolStatus: null,
            error: "Không kết nối được tới trợ lý. Bạn kiểm tra mạng và thử lại nhé.",
          }));
        }
      } finally {
        if (abortRef.current === controller) {
          abortRef.current = null;
        }
        setStreaming(false);
      }
    },
    [messages, streaming]
  );

  const handleConfirmCartAction = useCallback(
    async (messageId: string, action: AssistantPendingCartAction) => {
      if (action.status === "adding" || action.status === "added") return;

      const updateAction = (patch: Partial<AssistantPendingCartAction>) => {
        setMessages((prev) =>
          prev.map((m) => {
            if (m.id !== messageId || !m.pendingCartActions) return m;
            return {
              ...m,
              pendingCartActions: m.pendingCartActions.map((a) =>
                a.id === action.id ? { ...a, ...patch } : a
              ),
            };
          })
        );
      };

      updateAction({ status: "adding", statusMessage: undefined, requiresAuth: false });

      const result = await addToCart({
        skuId: action.skuId,
        quantity: action.quantity,
        productName: action.productName,
        size: action.size,
      });

      if (result.ok) {
        updateAction({
          status: "added",
          statusMessage: result.message || "Đã thêm vào giỏ hàng của bạn.",
        });
      } else if (result.unauthenticated) {
        updateAction({
          status: "pending",
          requiresAuth: true,
          statusMessage: "Vui lòng đăng nhập để thêm sản phẩm vào giỏ hàng.",
        });
      } else {
        updateAction({
          status: "error",
          statusMessage: result.message || "Chưa thể thêm sản phẩm vào giỏ lúc này.",
        });
      }
    },
    [addToCart]
  );

  const handleDismissCartAction = useCallback((messageId: string, actionId: string) => {
    setMessages((prev) =>
      prev.map((m) => {
        if (m.id !== messageId || !m.pendingCartActions) return m;
        return {
          ...m,
          pendingCartActions: m.pendingCartActions.map((a) =>
            a.id === actionId ? { ...a, status: "dismissed" } : a
          ),
        };
      })
    );
  }, []);

  const handleFeedback = useCallback((messageId: string, choice: "helpful" | "unhelpful") => {
    setMessages((prev) =>
      prev.map((m) => {
        if (m.id !== messageId) return m;
        return {
          ...m,
          feedback: m.feedback === choice ? null : choice,
        };
      })
    );
  }, []);

  const handleHandoff = useCallback(async () => {
    setHandoffError(null);
    if (!isAuthenticated) {
      setHandoffLoginHint(true);
      return;
    }
    if (handoffBusy) return;
    setHandoffBusy(true);
    setHandoffLoginHint(false);

    try {
      const history: AssistantChatInputMessage[] = messages
        .filter((m) => (m.role === "user" || m.role === "assistant") && m.content.trim())
        .map((m) => ({ role: m.role, content: m.content.trim() }))
        .slice(-30);

      const res = await api<HandoffApiResponse>("/api/assistant/handoff", {
        method: "POST",
        body: {
          messages: history,
          ...(conversationIdRef.current ? { conversation_id: conversationIdRef.current } : {}),
          reason: productContext?.name
            ? `Khách hàng cần nhân viên tư vấn thêm về ${productContext.name}`
            : "Khách hàng yêu cầu trao đổi trực tiếp với nhân viên gian hàng",
        },
      });

      const conversationId = res.conversation_id;
      setHandoffSummary(
        res.summary || "Đã chuyển tóm tắt cuộc trò chuyện sang nhân viên gian hàng."
      );
      if (conversationId) {
        onHandoffToChat?.(conversationId);
        if (typeof window !== "undefined") {
          window.dispatchEvent(
            new CustomEvent("nexusmart:open-chat", {
              detail: { conversationId },
            })
          );
        }
        showToast("Đã chuyển cuộc trò chuyện tới nhân viên gian hàng.");
        setOpen(false);
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setHandoffLoginHint(true);
      } else {
        const msg =
          err instanceof ApiError
            ? sanitizeAssistantError(err.code, err.message)
            : "Chưa thể kết nối tới nhân viên lúc này. Bạn có thể mở khung chat góc dưới để nhắn trực tiếp.";
        setHandoffError(msg);
      }
    } finally {
      setHandoffBusy(false);
    }
  }, [handoffBusy, isAuthenticated, messages, onHandoffToChat, productContext?.name, showToast]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    void sendMessage(input);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void sendMessage(input);
    }
  };

  const rootStyle = {
    "--assistant-offset-bottom": `${Math.max(16, Math.trunc(offsetBottomPx))}px`,
  } as React.CSSProperties;

  return (
    <div className={`${styles.root} ${className ?? ""}`} style={rootStyle} data-ui="site-assistant">
      {open && (
        <section
          id="site-assistant-panel"
          className={styles.panel}
          role="dialog"
          aria-label={`Trợ lý mua sắm ${brandName}`}
        >
          <header className={styles.header}>
            <div className={styles.headerLead}>
              <span className={styles.headerBadge} aria-hidden="true">
                <IconSpark size={15} />
              </span>
              <div className={styles.headerText}>
                <h2 className={styles.headerTitle}>Trợ lý mua sắm</h2>
                <p className={styles.headerSub}>{brandName}</p>
              </div>
            </div>
            <div className={styles.headerActions}>
              <button
                type="button"
                className={styles.handoffHeaderBtn}
                onClick={() => void handleHandoff()}
                disabled={handoffBusy}
              >
                <IconChat size={14} />
                <span>{handoffBusy ? "Đang chuyển..." : "Gặp nhân viên"}</span>
              </button>
              <button
                type="button"
                className={styles.closeBtn}
                onClick={() => setOpen(false)}
                aria-label="Đóng khung trợ lý"
              >
                <IconClose size={16} />
              </button>
            </div>
          </header>

          {productContext?.name && (
            <div className={styles.contextStrip}>
              <span className={styles.contextLabel}>Đang xem:</span>
              <span className={styles.contextProduct}>{productContext.name}</span>
              {typeof productContext.priceVnd === "number" && productContext.priceVnd > 0 && (
                <span className={styles.contextPrice}>{formatVnd(productContext.priceVnd)}</span>
              )}
            </div>
          )}

          <div ref={scrollRef} className={styles.transcript} aria-live="polite">
            {messages.length === 0 ? (
              <div className={styles.welcome}>
                <p className={styles.welcomeTitle}>
                  Xin chào! Mình có thể giúp gì tại {brandName}?
                </p>
                <p className={styles.welcomeDesc}>
                  Hỏi nhanh về sản phẩm phù hợp, chất liệu, chọn size, chính sách đổi trả hoặc tình
                  trạng đơn hàng của bạn.
                </p>
                <div className={styles.starterList}>
                  {starterPrompts.map((prompt) => (
                    <button
                      key={prompt}
                      type="button"
                      className={styles.starterChip}
                      onClick={() => void sendMessage(prompt)}
                      disabled={streaming}
                    >
                      {prompt}
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              messages.map((msg) => (
                <div
                  key={msg.id}
                  className={msg.role === "user" ? styles.rowUser : styles.rowAssistant}
                >
                  <div className={msg.role === "user" ? styles.bubbleUser : styles.bubbleAssistant}>
                    {msg.content && <p className={styles.msgText}>{msg.content}</p>}

                    {msg.streaming && !msg.content && !msg.toolStatus && (
                      <p className={styles.statusNote}>Đang soạn câu trả lời...</p>
                    )}

                    {msg.toolStatus && <p className={styles.statusNote}>{msg.toolStatus}</p>}

                    {msg.error && (
                      <div className={styles.errorBox} role="alert">
                        <p>{msg.error}</p>
                        <button
                          type="button"
                          className={styles.inlineLinkBtn}
                          onClick={() => void handleHandoff()}
                          disabled={handoffBusy}
                        >
                          Gặp nhân viên gian hàng
                        </button>
                      </div>
                    )}

                    {msg.requiresAuthPrompt && !isAuthenticated && (
                      <div className={styles.authNotice}>
                        <span>Đăng nhập để xem thông tin đơn hàng và thao tác giỏ hàng.</span>
                        <Link href={loginHref} className={styles.authLink}>
                          Đăng nhập ngay
                        </Link>
                      </div>
                    )}

                    {msg.products && msg.products.length > 0 && (
                      <div className={styles.cardStack}>
                        {msg.products.map((prod) => {
                          const safeImg = sanitizeSafeUrl(prod.image);
                          return (
                            <Link key={prod.id} href={prod.href} className={styles.productCard}>
                              {safeImg ? (
                                <img
                                  src={safeImg}
                                  alt={prod.name}
                                  className={styles.productThumb}
                                  width={52}
                                  height={68}
                                />
                              ) : (
                                <div className={styles.productThumbPlaceholder} aria-hidden="true">
                                  <IconBag size={16} />
                                </div>
                              )}
                              <div className={styles.productBody}>
                                <span className={styles.productName}>{prod.name}</span>
                                <span className={styles.productMetaRow}>
                                  {prod.priceVnd > 0 && (
                                    <span className={styles.productPrice}>
                                      {formatVnd(prod.priceVnd)}
                                    </span>
                                  )}
                                  {prod.stockLabel && (
                                    <span className={styles.productStock}>{prod.stockLabel}</span>
                                  )}
                                </span>
                              </div>
                            </Link>
                          );
                        })}
                      </div>
                    )}

                    {msg.orders && msg.orders.length > 0 && (
                      <div className={styles.cardStack}>
                        {msg.orders.map((order) => (
                          <div key={order.orderCode} className={styles.orderCard}>
                            <div className={styles.orderTop}>
                              <span className={styles.orderCode}>Đơn #{order.orderCode}</span>
                              <span className={styles.orderStatus}>{order.statusLabel}</span>
                            </div>
                            <div className={styles.orderBottom}>
                              {typeof order.totalVnd === "number" && (
                                <span className={styles.orderPrice}>
                                  {formatVnd(order.totalVnd)}
                                </span>
                              )}
                              <Link href={order.href} className={styles.orderLink}>
                                Xem chi tiết
                              </Link>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}

                    {msg.pendingCartActions && msg.pendingCartActions.length > 0 && (
                      <div className={styles.cardStack}>
                        {msg.pendingCartActions.map((action) => (
                          <div key={action.id} className={styles.cartConfirmCard}>
                            <div className={styles.cartConfirmHead}>
                              <IconBag size={14} />
                              <span>Xác nhận thêm vào giỏ hàng</span>
                            </div>
                            <div className={styles.cartConfirmDetail}>
                              <span className={styles.cartConfirmName}>{action.productName}</span>
                              <span className={styles.cartConfirmMeta}>
                                {action.variantLabel ? `${action.variantLabel} · ` : ""}
                                Số lượng: {action.quantity}
                                {action.priceVnd ? ` · ${formatVnd(action.priceVnd)}` : ""}
                              </span>
                            </div>
                            <p className={styles.cartConfirmHint}>
                              Bạn kiểm tra lại phân loại trước khi thêm. Trợ lý không tự đặt đơn hay
                              thanh toán.
                            </p>

                            {action.status === "added" ? (
                              <div className={styles.cartDoneRow}>
                                <span className={styles.cartDoneText}>
                                  {action.statusMessage ?? "Đã thêm vào giỏ hàng."}
                                </span>
                                <Link href="/gio-hang" className={styles.authLink}>
                                  Xem giỏ hàng
                                </Link>
                              </div>
                            ) : action.status === "dismissed" ? (
                              <p className={styles.cartDismissedText}>Đã bỏ qua đề xuất này.</p>
                            ) : (
                              <>
                                {action.statusMessage && (
                                  <p className={styles.cartStatusError}>
                                    {action.statusMessage}{" "}
                                    {action.requiresAuth && (
                                      <Link href={loginHref} className={styles.authLink}>
                                        Đăng nhập
                                      </Link>
                                    )}
                                  </p>
                                )}
                                <div className={styles.cartConfirmActions}>
                                  <button
                                    type="button"
                                    className={styles.cartConfirmPrimary}
                                    disabled={action.status === "adding"}
                                    onClick={() => void handleConfirmCartAction(msg.id, action)}
                                  >
                                    {action.status === "adding"
                                      ? "Đang thêm..."
                                      : "Xác nhận thêm vào giỏ"}
                                  </button>
                                  <button
                                    type="button"
                                    className={styles.cartConfirmGhost}
                                    disabled={action.status === "adding"}
                                    onClick={() => handleDismissCartAction(msg.id, action.id)}
                                  >
                                    Bỏ qua
                                  </button>
                                </div>
                              </>
                            )}
                          </div>
                        ))}
                      </div>
                    )}

                    {msg.citations && msg.citations.length > 0 && (
                      <div className={styles.citationList}>
                        <span className={styles.citationHeading}>Nguồn tham khảo:</span>
                        <div className={styles.citationLinks}>
                          {msg.citations.map((cit) => {
                            const safeHref = sanitizeSafeUrl(cit.href);
                            if (!safeHref) return null;
                            const isExternal = safeHref.startsWith("https://");
                            return isExternal ? (
                              <a
                                key={`${cit.href}-${cit.title}`}
                                href={safeHref}
                                target="_blank"
                                rel="noopener noreferrer"
                                className={styles.citationLink}
                              >
                                {cit.title}
                                {cit.section ? ` (${cit.section})` : ""}
                              </a>
                            ) : (
                              <Link
                                key={`${cit.href}-${cit.title}`}
                                href={safeHref}
                                className={styles.citationLink}
                              >
                                {cit.title}
                                {cit.section ? ` (${cit.section})` : ""}
                              </Link>
                            );
                          })}
                        </div>
                      </div>
                    )}
                  </div>

                  {msg.role === "assistant" && !msg.streaming && !msg.error && msg.content && (
                    <div className={styles.feedbackBar}>
                      <button
                        type="button"
                        className={`${styles.feedbackBtn} ${
                          msg.feedback === "helpful" ? styles.feedbackBtnActive : ""
                        }`}
                        aria-pressed={msg.feedback === "helpful"}
                        onClick={() => handleFeedback(msg.id, "helpful")}
                      >
                        Hữu ích
                      </button>
                      <button
                        type="button"
                        className={`${styles.feedbackBtn} ${
                          msg.feedback === "unhelpful" ? styles.feedbackBtnActive : ""
                        }`}
                        aria-pressed={msg.feedback === "unhelpful"}
                        onClick={() => handleFeedback(msg.id, "unhelpful")}
                      >
                        Chưa đúng
                      </button>
                      {msg.feedback && (
                        <span className={styles.feedbackNote}>Cảm ơn phản hồi của bạn.</span>
                      )}
                    </div>
                  )}
                </div>
              ))
            )}

            {handoffLoginHint && (
              <div className={styles.handoffBanner} role="status">
                <span>Vui lòng đăng nhập để trao đổi trực tiếp với nhân viên {brandName}.</span>
                <Link href={loginHref} className={styles.authLink}>
                  Đăng nhập
                </Link>
              </div>
            )}

            {handoffError && (
              <div className={styles.handoffBannerError} role="alert">
                {handoffError}
              </div>
            )}

            {handoffSummary && (
              <div className={styles.handoffBanner} role="status">
                <span>{handoffSummary}</span>
              </div>
            )}
          </div>

          <footer className={styles.footer}>
            {streaming ? (
              <div className={styles.stopRow}>
                <span className={styles.streamingHint}>Trợ lý đang trả lời...</span>
                <button type="button" className={styles.stopBtn} onClick={stopStreaming}>
                  Dừng trả lời
                </button>
              </div>
            ) : (
              <div className={styles.handoffStrip}>
                <span className={styles.disclaimerText}>Gợi ý tự động theo dữ liệu cửa hàng</span>
                <button
                  type="button"
                  className={styles.handoffInlineBtn}
                  onClick={() => void handleHandoff()}
                  disabled={handoffBusy}
                >
                  {handoffBusy ? "Đang kết nối..." : "Gặp nhân viên gian hàng"}
                </button>
              </div>
            )}

            <form className={styles.composer} onSubmit={handleSubmit}>
              <textarea
                ref={inputRef}
                rows={1}
                className={styles.composerInput}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="Hỏi về sản phẩm, size, đổi trả..."
                maxLength={2000}
                disabled={streaming}
                aria-label="Nhập câu hỏi cho trợ lý mua sắm"
              />
              <button
                type="submit"
                className={styles.sendBtn}
                disabled={streaming || !input.trim()}
                aria-label="Gửi câu hỏi"
              >
                <IconSend size={15} />
              </button>
            </form>
          </footer>
        </section>
      )}

      <button
        type="button"
        className={styles.launcher}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls="site-assistant-panel"
        aria-label={open ? "Thu gọn trợ lý mua sắm" : `Mở trợ lý mua sắm ${brandName}`}
      >
        <IconSpark size={16} />
        <span>{open ? "Đóng trợ lý" : "Trợ lý AI"}</span>
      </button>
    </div>
  );
}
