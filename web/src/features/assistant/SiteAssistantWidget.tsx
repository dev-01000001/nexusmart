"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  MessageCircle,
  Send,
  ShoppingBag,
  Sparkles,
  Square,
  ThumbsDown,
  ThumbsUp,
  X,
} from "lucide-react";
import { useSession } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api } from "@/lib/api";
import { formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { cn } from "@/lib/utils";
import {
  extractToolCardData,
  getToolActivityLabel,
  sanitizeAssistantError,
  shouldRefreshCartAfterToolResult,
  type AssistantChatInputMessage,
  type AssistantCitation,
  type AssistantMessage,
  type AssistantOrderCard,
  type AssistantPendingCartAction,
  type AssistantProductCard,
  type SiteAssistantWidgetProps,
} from "./assistantTypes";
import { renderAssistantMarkdown } from "./assistantMarkdown";
import { createSseParser } from "./sseParser";

interface HandoffApiResponse {
  conversation_id: string;
  summary?: string;
  estimated_response?: string;
}

export interface SiteAssistantWidgetComponentProps extends SiteAssistantWidgetProps {
  /** Render inside SiteSupportDock without a standalone fixed launcher. */
  embedded?: boolean;
  /** Whether the embedded panel is currently visible (allows preserving history/stream when hidden). */
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
  defaultOpen = false,
  offsetBottomPx = 76,
  className,
  embedded = false,
  active = true,
  onClose,
}: SiteAssistantWidgetComponentProps) {
  const pathname = usePathname();
  const { isAuthenticated } = useSession();
  const { addToCart, refreshCart, showToast } = useStore();

  const [open, setOpen] = useState(embedded ? true : defaultOpen);
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
  const panelRef = useRef<HTMLElement | null>(null);
  const launcherRef = useRef<HTMLButtonElement | null>(null);
  const wasOpenRef = useRef(false);
  const restoreFocusOnCloseRef = useRef(true);
  const lastShopIdRef = useRef(shopId);
  const conversationIdRef = useRef<string>("");

  const effectiveOpen = embedded ? active : open;

  const handleClose = useCallback(() => {
    if (onClose) {
      onClose();
    } else {
      setOpen(false);
    }
  }, [onClose]);

  const loginHref = useMemo(() => {
    const nextPath = pathname && pathname.startsWith("/") ? pathname : "/";
    return `/dang-nhap?next=${encodeURIComponent(nextPath)}`;
  }, [pathname]);

  const starterPrompts = useMemo(() => {
    if (productContext?.name) {
      return [
        `Chọn size cho ${productContext.name}`,
        "Áo mặc mát dưới 300k",
        "Chính sách đổi trả",
        "Kiểm tra đơn gần nhất",
      ];
    }
    return ["Áo mặc mát dưới 300k", "Chính sách đổi trả", "Kiểm tra đơn gần nhất"];
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
    if (!effectiveOpen) return;
    const el = scrollRef.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
    }
  }, [messages, effectiveOpen]);

  // Escape key closes panel (standalone mode; dock manages Escape when embedded).
  useEffect(() => {
    if (!effectiveOpen || embedded) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !hasActiveModalDialog()) {
        e.preventDefault();
        handleClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [effectiveOpen, embedded, handleClose]);

  // Focus composer when opening panel; restore launcher focus on close in standalone mode.
  useEffect(() => {
    if (effectiveOpen) {
      wasOpenRef.current = true;
      restoreFocusOnCloseRef.current = true;
      const inputEl = inputRef.current;
      if (inputEl && !inputEl.disabled) {
        inputEl.focus({ preventScroll: true });
      } else if (panelRef.current) {
        focusFirstInteractive(panelRef.current);
      }
      return;
    }
    if (wasOpenRef.current) {
      wasOpenRef.current = false;
      const shouldRestore = restoreFocusOnCloseRef.current;
      restoreFocusOnCloseRef.current = true;
      if (!embedded && shouldRestore && launcherRef.current?.isConnected) {
        launcherRef.current.focus({ preventScroll: true });
      }
    }
  }, [effectiveOpen, embedded]);

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
            if (shouldRefreshCartAfterToolResult(extracted)) {
              void refreshCart();
            }
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
              : "Chưa có câu trả lời phù hợp. Bạn thử diễn đạt cụ thể hơn nhé.",
        }));
      } catch (err) {
        if (err instanceof Error && err.name === "AbortError") {
          patchAssistant((m) => ({
            ...m,
            streaming: false,
            toolStatus: null,
            content: m.content || "Đã dừng phản hồi.",
          }));
        } else {
          patchAssistant((m) => ({
            ...m,
            streaming: false,
            toolStatus: null,
            error: "Không kết nối được tới trợ lý. Vui lòng thử lại.",
          }));
        }
      } finally {
        if (abortRef.current === controller) {
          abortRef.current = null;
        }
        setStreaming(false);
      }
    },
    [messages, refreshCart, streaming]
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
          statusMessage: result.message || "Đã thêm vào giỏ hàng.",
        });
      } else if (result.unauthenticated) {
        updateAction({
          status: "pending",
          requiresAuth: true,
          statusMessage: "Đăng nhập để thêm sản phẩm vào giỏ.",
        });
      } else {
        updateAction({
          status: "error",
          statusMessage: result.message || "Chưa thể thêm sản phẩm lúc này.",
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
      setHandoffSummary(res.summary || "Đã chuyển tóm tắt sang nhân viên cửa hàng.");
      if (conversationId) {
        if (typeof window !== "undefined") {
          window.dispatchEvent(
            new CustomEvent("nexusmart:open-chat", {
              detail: { conversationId },
            })
          );
        }
        showToast("Đã chuyển cuộc trò chuyện tới nhân viên cửa hàng.");
        if (!embedded) {
          restoreFocusOnCloseRef.current = false;
          setOpen(false);
        }
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setHandoffLoginHint(true);
      } else {
        const msg =
          err instanceof ApiError
            ? sanitizeAssistantError(err.code, err.message)
            : "Chưa kết nối được nhân viên lúc này. Bạn có thể mở mục nhắn tin trực tiếp.";
        setHandoffError(msg);
      }
    } finally {
      setHandoffBusy(false);
    }
  }, [embedded, handoffBusy, isAuthenticated, messages, productContext?.name, showToast]);

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

  return (
    <div
      data-ui="site-assistant"
      className={cn(
        embedded
          ? "static flex w-full flex-col items-stretch"
          : "fixed right-3 z-50 flex flex-col items-end gap-2.5 sm:right-5",
        "font-sans text-foreground",
        className
      )}
      style={embedded ? undefined : { bottom: `${Math.max(16, Math.trunc(offsetBottomPx))}px` }}
    >
      {(embedded || effectiveOpen) && (
        <section
          ref={panelRef}
          tabIndex={-1}
          id="site-assistant-panel"
          role="dialog"
          aria-label={`Trợ lý mua sắm ${brandName}`}
          className="flex h-[min(560px,calc(100svh-128px))] w-[min(404px,calc(100vw-24px))] flex-col overflow-hidden rounded-xl border border-border bg-card text-card-foreground shadow-overlay outline-none animate-in fade-in-0 slide-in-from-bottom-2 duration-200"
        >
          <header className="flex shrink-0 items-center justify-between gap-2 border-b border-border bg-card px-3.5 py-3">
            <div className="flex min-w-0 items-center gap-2.5">
              <span
                className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"
                aria-hidden="true"
              >
                <Sparkles className="size-4" />
              </span>
              <div className="min-w-0">
                <h2 className="truncate text-sm leading-5 font-semibold text-foreground">
                  Trợ lý mua sắm
                </h2>
                <p className="truncate text-xs text-muted-foreground">{brandName}</p>
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 px-2.5 text-xs shadow-none"
                onClick={() => void handleHandoff()}
                disabled={handoffBusy}
              >
                <MessageCircle className="size-3.5" aria-hidden="true" />
                <span>{handoffBusy ? "Đang chuyển..." : "Gặp nhân viên"}</span>
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="size-9 text-muted-foreground hover:text-foreground"
                onClick={handleClose}
                aria-label="Đóng khung trợ lý"
              >
                <X className="size-4" aria-hidden="true" />
              </Button>
            </div>
          </header>

          {productContext?.name && (
            <div className="flex shrink-0 items-center gap-2 border-b border-border bg-background px-3.5 py-2 text-xs">
              <span className="shrink-0 text-muted-foreground">Đang xem:</span>
              <span className="min-w-0 flex-1 truncate font-medium text-foreground">
                {productContext.name}
              </span>
              {typeof productContext.priceVnd === "number" && productContext.priceVnd > 0 && (
                <span className="shrink-0 font-semibold text-brand tabular-nums">
                  {formatVnd(productContext.priceVnd)}
                </span>
              )}
            </div>
          )}

          <div
            ref={scrollRef}
            className="flex-1 space-y-3 overflow-y-auto bg-background px-3.5 py-3.5"
            aria-live="polite"
          >
            {messages.length === 0 ? (
              <div className="space-y-3 rounded-xl border border-border bg-card p-4">
                <div className="space-y-1">
                  <p className="text-sm font-semibold text-foreground">Hỏi nhanh tại {brandName}</p>
                  <p className="text-xs leading-5 text-muted-foreground">
                    Tìm mẫu phù hợp, chọn size, xem chính sách hoặc tra đơn hàng.
                  </p>
                </div>
                <div className="flex flex-wrap gap-2">
                  {starterPrompts.map((prompt) => (
                    <Button
                      key={prompt}
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-auto min-h-9 py-1.5 text-left text-xs whitespace-normal shadow-none"
                      onClick={() => void sendMessage(prompt)}
                      disabled={streaming}
                    >
                      {prompt}
                    </Button>
                  ))}
                </div>
              </div>
            ) : (
              messages.map((msg) => (
                <div
                  key={msg.id}
                  className={cn(
                    "flex flex-col gap-1.5",
                    msg.role === "user" ? "items-end" : "items-start"
                  )}
                >
                  <div
                    className={cn(
                      "max-w-[90%] space-y-2.5 rounded-xl px-3.5 py-2.5 text-[13px] leading-5 break-words",
                      msg.role === "user"
                        ? "bg-primary text-primary-foreground"
                        : "border border-border bg-card text-card-foreground"
                    )}
                  >
                    {msg.content &&
                      (msg.role === "assistant" ? (
                        <div className="space-y-1.5">
                          {renderAssistantMarkdown(msg.content, {
                            stripUuids: true,
                            streaming: Boolean(msg.streaming),
                            LinkComponent: Link,
                            classNames: {
                              paragraph: "m-0 leading-relaxed",
                              list: "my-1 list-disc space-y-1 pl-4",
                              listItem: "leading-relaxed",
                              strong: "font-semibold text-foreground",
                              link: "font-medium text-brand underline underline-offset-2 hover:opacity-80",
                            },
                          })}
                        </div>
                      ) : (
                        <p className="m-0 whitespace-pre-wrap">{msg.content}</p>
                      ))}

                    {msg.streaming && !msg.content && !msg.toolStatus && (
                      <p className="m-0 text-xs text-muted-foreground">Đang soạn trả lời...</p>
                    )}

                    {msg.toolStatus && (
                      <p className="m-0 text-xs text-muted-foreground">{msg.toolStatus}</p>
                    )}

                    {msg.error && (
                      <div
                        className="space-y-2 rounded-lg border border-destructive/30 bg-badge-critical-bg p-3 text-xs text-badge-critical-ink"
                        role="alert"
                      >
                        <p className="m-0">{msg.error}</p>
                        <button
                          type="button"
                          className="font-semibold underline underline-offset-2 hover:opacity-80"
                          onClick={() => void handleHandoff()}
                          disabled={handoffBusy}
                        >
                          Gặp nhân viên cửa hàng
                        </button>
                      </div>
                    )}

                    {msg.requiresAuthPrompt && !isAuthenticated && (
                      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-background px-3 py-2 text-xs">
                        <span className="text-muted-foreground">
                          Đăng nhập để xem đơn hàng và thao tác giỏ.
                        </span>
                        <Link href={loginHref} className="font-semibold text-brand hover:underline">
                          Đăng nhập
                        </Link>
                      </div>
                    )}

                    {msg.products && msg.products.length > 0 && (
                      <div className="space-y-2">
                        {msg.products.map((prod) => {
                          const safeImg = sanitizeSafeUrl(prod.image);
                          return (
                            <Link
                              key={prod.id}
                              href={prod.href}
                              className="flex items-center gap-2.5 rounded-lg border border-border bg-background p-2 transition-colors hover:bg-accent/50"
                            >
                              {safeImg ? (
                                <img
                                  src={safeImg}
                                  alt={prod.name}
                                  className="h-14 w-11 shrink-0 rounded object-cover"
                                  width={52}
                                  height={68}
                                />
                              ) : (
                                <div
                                  className="flex h-14 w-11 shrink-0 items-center justify-center rounded bg-muted text-muted-foreground"
                                  aria-hidden="true"
                                >
                                  <ShoppingBag className="size-4" />
                                </div>
                              )}
                              <div className="min-w-0 flex-1 space-y-0.5">
                                <p className="line-clamp-2 text-xs font-semibold text-foreground">
                                  {prod.name}
                                </p>
                                <div className="flex flex-wrap items-center gap-2 text-xs tabular-nums">
                                  {prod.priceVnd > 0 && (
                                    <span className="font-semibold text-brand">
                                      {formatVnd(prod.priceVnd)}
                                    </span>
                                  )}
                                  {prod.stockLabel && (
                                    <span className="text-muted-foreground">{prod.stockLabel}</span>
                                  )}
                                </div>
                              </div>
                            </Link>
                          );
                        })}
                      </div>
                    )}

                    {msg.orders && msg.orders.length > 0 && (
                      <div className="space-y-2">
                        {msg.orders.map((order) => (
                          <div
                            key={order.orderCode}
                            className="space-y-1.5 rounded-lg border border-border bg-background p-2.5 text-xs"
                          >
                            <div className="flex flex-wrap items-center justify-between gap-2">
                              <span className="font-semibold text-foreground tabular-nums">
                                Đơn #{order.orderCode}
                              </span>
                              <Badge variant="secondary" className="text-[11px]">
                                {order.statusLabel}
                              </Badge>
                            </div>
                            <div className="flex items-center justify-between gap-2">
                              {typeof order.totalVnd === "number" && (
                                <span className="font-medium text-foreground tabular-nums">
                                  {formatVnd(order.totalVnd)}
                                </span>
                              )}
                              <Link
                                href={order.href}
                                className="font-medium text-brand hover:underline"
                              >
                                Xem chi tiết
                              </Link>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}

                    {msg.pendingCartActions && msg.pendingCartActions.length > 0 && (
                      <div className="space-y-2">
                        {msg.pendingCartActions.map((action) => (
                          <div
                            key={action.id}
                            className="space-y-2 rounded-lg border border-border bg-background p-3 text-xs"
                          >
                            <div className="flex items-center gap-1.5 font-semibold text-foreground">
                              <ShoppingBag className="size-3.5 text-brand" aria-hidden="true" />
                              <span>
                                {action.status === "added"
                                  ? "Đã thêm vào giỏ"
                                  : "Xác nhận thêm vào giỏ"}
                              </span>
                            </div>
                            <div className="space-y-0.5">
                              <p className="font-medium text-foreground">{action.productName}</p>
                              <p className="text-muted-foreground tabular-nums">
                                {action.variantLabel ? `${action.variantLabel}, ` : ""}
                                SL: {action.quantity}
                                {action.priceVnd ? `, ${formatVnd(action.priceVnd)}` : ""}
                              </p>
                            </div>
                            {action.status !== "added" && (
                              <p className="text-[11px] text-muted-foreground">
                                Kiểm tra phân loại trước khi thêm. Trợ lý không tự đặt đơn.
                              </p>
                            )}

                            {action.status === "added" ? (
                              <div className="flex flex-wrap items-center justify-between gap-2">
                                <span className="font-medium text-badge-success-ink">
                                  {action.statusMessage ?? "Đã thêm vào giỏ."}
                                </span>
                                <Link
                                  href="/gio-hang"
                                  className="font-semibold text-brand hover:underline"
                                >
                                  Xem giỏ hàng
                                </Link>
                              </div>
                            ) : action.status === "dismissed" ? (
                              <p className="text-muted-foreground">Đã bỏ qua đề xuất.</p>
                            ) : (
                              <>
                                {action.statusMessage && (
                                  <p className="text-destructive">
                                    {action.statusMessage}{" "}
                                    {action.requiresAuth && (
                                      <Link
                                        href={loginHref}
                                        className="font-semibold text-brand underline"
                                      >
                                        Đăng nhập
                                      </Link>
                                    )}
                                  </p>
                                )}
                                <div className="flex flex-wrap items-center gap-2 pt-0.5">
                                  <Button
                                    type="button"
                                    size="sm"
                                    className="h-8 shadow-none"
                                    disabled={action.status === "adding"}
                                    onClick={() => void handleConfirmCartAction(msg.id, action)}
                                  >
                                    {action.status === "adding" ? "Đang thêm..." : "Thêm vào giỏ"}
                                  </Button>
                                  <Button
                                    type="button"
                                    variant="ghost"
                                    size="sm"
                                    className="h-8"
                                    disabled={action.status === "adding"}
                                    onClick={() => handleDismissCartAction(msg.id, action.id)}
                                  >
                                    Bỏ qua
                                  </Button>
                                </div>
                              </>
                            )}
                          </div>
                        ))}
                      </div>
                    )}

                    {msg.citations && msg.citations.length > 0 && (
                      <div className="space-y-1 border-t border-border pt-2 text-xs">
                        <span className="block font-medium text-muted-foreground">
                          Nguồn tham khảo:
                        </span>
                        <div className="flex flex-wrap gap-1.5">
                          {msg.citations.map((cit) => {
                            const safeHref = sanitizeSafeUrl(cit.href);
                            if (!safeHref) return null;
                            const isExternal = safeHref.startsWith("https://");
                            const cls =
                              "inline-flex items-center rounded-md border border-border bg-background px-2 py-0.5 text-[11px] font-medium text-brand hover:underline";
                            return isExternal ? (
                              <a
                                key={`${cit.href}-${cit.title}`}
                                href={safeHref}
                                target="_blank"
                                rel="noopener noreferrer"
                                className={cls}
                              >
                                {cit.title}
                                {cit.section ? ` (${cit.section})` : ""}
                              </a>
                            ) : (
                              <Link
                                key={`${cit.href}-${cit.title}`}
                                href={safeHref}
                                className={cls}
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
                    <div className="flex items-center gap-1.5 px-1">
                      <Button
                        type="button"
                        variant={msg.feedback === "helpful" ? "secondary" : "ghost"}
                        size="xs"
                        aria-pressed={msg.feedback === "helpful"}
                        onClick={() => handleFeedback(msg.id, "helpful")}
                      >
                        <ThumbsUp className="size-3" aria-hidden="true" />
                        <span>Hữu ích</span>
                      </Button>
                      <Button
                        type="button"
                        variant={msg.feedback === "unhelpful" ? "secondary" : "ghost"}
                        size="xs"
                        aria-pressed={msg.feedback === "unhelpful"}
                        onClick={() => handleFeedback(msg.id, "unhelpful")}
                      >
                        <ThumbsDown className="size-3" aria-hidden="true" />
                        <span>Chưa đúng</span>
                      </Button>
                      {msg.feedback && (
                        <span className="text-[11px] text-muted-foreground">Đã ghi nhận.</span>
                      )}
                    </div>
                  )}
                </div>
              ))
            )}

            {handoffLoginHint && (
              <div
                className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-card px-3 py-2 text-xs"
                role="status"
              >
                <span>Đăng nhập để nhắn trực tiếp với {brandName}.</span>
                <Link href={loginHref} className="font-semibold text-brand hover:underline">
                  Đăng nhập
                </Link>
              </div>
            )}

            {handoffError && (
              <div
                className="rounded-lg border border-destructive/30 bg-badge-critical-bg px-3 py-2 text-xs text-badge-critical-ink"
                role="alert"
              >
                {handoffError}
              </div>
            )}

            {handoffSummary && (
              <div
                className="rounded-lg border border-border bg-card px-3 py-2 text-xs text-foreground"
                role="status"
              >
                <span>{handoffSummary}</span>
              </div>
            )}
          </div>

          <footer className="shrink-0 space-y-2 border-t border-border bg-card px-3 py-2.5">
            {streaming ? (
              <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
                <span>Đang trả lời...</span>
                <Button type="button" variant="outline" size="xs" onClick={stopStreaming}>
                  <Square className="size-3" aria-hidden="true" />
                  <span>Dừng</span>
                </Button>
              </div>
            ) : (
              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <span>Gợi ý theo dữ liệu cửa hàng</span>
                <button
                  type="button"
                  className="font-medium text-brand hover:underline disabled:opacity-50"
                  onClick={() => void handleHandoff()}
                  disabled={handoffBusy}
                >
                  {handoffBusy ? "Đang kết nối..." : "Gặp nhân viên"}
                </button>
              </div>
            )}

            <form className="flex items-end gap-2" onSubmit={handleSubmit}>
              <Textarea
                ref={inputRef}
                rows={1}
                className="max-h-24 min-h-10 flex-1 resize-none bg-background py-2 text-sm"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="Hỏi về mẫu, size, đổi trả..."
                maxLength={2000}
                disabled={streaming}
                aria-label="Nhập câu hỏi cho trợ lý mua sắm"
              />
              <Button
                type="submit"
                size="icon"
                className="size-10 shrink-0 shadow-none"
                disabled={streaming || !input.trim()}
                aria-label="Gửi câu hỏi"
              >
                <Send className="size-4" aria-hidden="true" />
              </Button>
            </form>
          </footer>
        </section>
      )}

      {!embedded && (
        <Button
          ref={launcherRef}
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-controls="site-assistant-panel"
          aria-label={open ? "Thu gọn trợ lý mua sắm" : `Mở trợ lý mua sắm ${brandName}`}
          className="h-11 rounded-full px-4 shadow-overlay"
        >
          <Sparkles className="size-4" aria-hidden="true" />
          <span>{open ? "Đóng trợ lý" : "Trợ lý AI"}</span>
        </Button>
      )}
    </div>
  );
}
