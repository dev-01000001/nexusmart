"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { MessageCircle, Sparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { OPEN_SITE_CHAT_EVENT, type OpenSiteChatDetail } from "@/features/chat/chatTypes";
import { cn } from "@/lib/utils";
import { useSitePathname } from "./SiteChrome";

const SiteAssistantWidget = dynamic(
  () => import("@/features/assistant/SiteAssistantWidget").then((mod) => mod.SiteAssistantWidget),
  {
    ssr: false,
    loading: () => (
      <div
        role="status"
        aria-live="polite"
        className="flex h-[120px] w-[min(360px,calc(100vw-24px))] items-center justify-center rounded-xl border border-border bg-card px-4 text-sm text-muted-foreground shadow-overlay"
      >
        Đang mở trợ lý mua sắm…
      </div>
    ),
  }
);

const SiteChatWidget = dynamic(
  () => import("@/features/chat/SiteChatWidget").then((mod) => mod.SiteChatWidget),
  {
    ssr: false,
    loading: () => (
      <div
        role="status"
        aria-live="polite"
        className="flex h-[120px] w-[min(360px,calc(100vw-24px))] items-center justify-center rounded-xl border border-border bg-card px-4 text-sm text-muted-foreground shadow-overlay"
      >
        Đang mở khung trò chuyện…
      </div>
    ),
  }
);

export interface SiteSupportDockProps {
  shopId: string;
  brandName: string;
  showChat: boolean;
  showAssistant: boolean;
}

type DockMode = "closed" | "chooser" | "assistant" | "chat";

/**
 * Single unified floating support launcher combining AI Assistant and Shop Chat.
 * Opens a compact choice panel (or goes straight to the single enabled channel),
 * lifts cleanly above the mobile sticky buy bar on PDPs, and handles assistant->chat handoff.
 */
export function SiteSupportDock({
  shopId,
  brandName,
  showChat,
  showAssistant,
}: SiteSupportDockProps) {
  const pathname = useSitePathname();
  const [mode, setMode] = useState<DockMode>("closed");
  const [assistantMounted, setAssistantMounted] = useState(false);
  const [chatMounted, setChatMounted] = useState(false);
  const [handoffConversationId, setHandoffConversationId] = useState<string | null>(null);
  const launcherRef = useRef<HTMLButtonElement | null>(null);
  const chooserRef = useRef<HTMLElement | null>(null);
  const aboveStickyBar = pathname.startsWith("/gio-hang") || pathname.startsWith("/thanh-toan");

  const openMode = useCallback((next: Exclude<DockMode, "closed">) => {
    if (next === "assistant") setAssistantMounted(true);
    if (next === "chat") setChatMounted(true);
    setMode(next);
  }, []);

  const closeDock = useCallback(
    (restoreFocus = true) => {
      if (mode === "closed") return;
      setMode("closed");
      if (restoreFocus && launcherRef.current?.isConnected) {
        launcherRef.current.focus({ preventScroll: true });
      }
    },
    [mode]
  );

  useEffect(() => {
    if (typeof window === "undefined") return;
    const onOpenChat = (ev: Event) => {
      const custom = ev as CustomEvent<OpenSiteChatDetail | undefined>;
      const convId = custom.detail?.conversationId?.trim();
      if (convId) {
        setHandoffConversationId(convId);
      }
      if (showChat) {
        openMode("chat");
      } else {
        closeDock(true);
      }
    };
    window.addEventListener(OPEN_SITE_CHAT_EVENT, onOpenChat);
    return () => window.removeEventListener(OPEN_SITE_CHAT_EVENT, onOpenChat);
  }, [closeDock, openMode, showChat]);

  useEffect(() => {
    if (mode !== "chooser") return;
    const chooser = chooserRef.current;
    if (!chooser) return;
    const target = chooser.querySelector<HTMLElement>("button:not([disabled])") ?? chooser;
    target.focus({ preventScroll: true });
  }, [mode]);

  useEffect(() => {
    if (mode === "closed") return;
    const onKeyDown = (ev: KeyboardEvent) => {
      const modalOpen = Boolean(
        document.querySelector('[role="dialog"][aria-modal="true"], [role="alertdialog"]')
      );
      if (ev.key === "Escape" && !modalOpen) {
        ev.preventDefault();
        closeDock(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mode, closeDock]);

  if (!showChat && !showAssistant) return null;

  const handleTriggerClick = () => {
    if (mode !== "closed") {
      closeDock(true);
      return;
    }
    if (showAssistant && showChat) {
      openMode("chooser");
    } else if (showAssistant) {
      openMode("assistant");
    } else {
      openMode("chat");
    }
  };

  const singleLabel =
    showAssistant && showChat ? "Hỗ trợ" : showAssistant ? "Trợ lý AI" : "Nhắn tin";

  return (
    <div
      className={cn(
        "fixed right-3 bottom-4 z-[64] flex flex-col items-end gap-2.5 font-sans text-foreground md:right-5 md:bottom-5",
        mode !== "closed" && "z-[70]",
        aboveStickyBar && "max-md:bottom-[calc(88px+env(safe-area-inset-bottom,0px))]",
        "[[data-site-root]:has([data-mobile-buybar])_&]:max-md:bottom-[calc(88px+env(safe-area-inset-bottom,0px))]"
      )}
      data-ui="site-support-dock"
      data-open={mode !== "closed"}
      data-above-sticky={aboveStickyBar ? "true" : undefined}
    >
      {mode === "chooser" && (
        <section
          ref={chooserRef}
          tabIndex={-1}
          className="w-[min(320px,calc(100vw-24px))] overflow-hidden rounded-xl border border-border bg-card text-card-foreground shadow-overlay animate-in fade-in slide-in-from-bottom-2 duration-200"
          role="dialog"
          aria-label={`Chọn kênh hỗ trợ ${brandName}`}
        >
          <header className="flex items-center justify-between gap-3 border-b border-border bg-muted px-4 py-3">
            <span className="min-w-0 truncate text-sm font-semibold text-foreground">
              Hỗ trợ {brandName}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => closeDock(true)}
              aria-label="Đóng bảng chọn hỗ trợ"
              className="size-8 shrink-0 text-muted-foreground hover:bg-card hover:text-foreground"
            >
              <X className="size-4" aria-hidden="true" />
            </Button>
          </header>

          <div className="flex flex-col gap-1.5 p-2.5">
            {showAssistant && (
              <button
                type="button"
                onClick={() => openMode("assistant")}
                className="flex min-h-[54px] w-full items-center gap-3 rounded-lg bg-background px-3 py-2.5 text-left text-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
              >
                <span
                  className="inline-flex size-9 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"
                  aria-hidden="true"
                >
                  <Sparkles className="size-[18px]" />
                </span>
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-sm leading-tight font-semibold">Trợ lý mua sắm AI</span>
                  <span className="text-[13px] leading-snug text-muted-foreground">
                    Tư vấn mẫu, chọn size, tra đơn
                  </span>
                </span>
              </button>
            )}

            {showChat && (
              <button
                type="button"
                onClick={() => openMode("chat")}
                className="flex min-h-[54px] w-full items-center gap-3 rounded-lg bg-background px-3 py-2.5 text-left text-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
              >
                <span
                  className="inline-flex size-9 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"
                  aria-hidden="true"
                >
                  <MessageCircle className="size-[18px]" />
                </span>
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-sm leading-tight font-semibold">Nhắn tin cửa hàng</span>
                  <span className="text-[13px] leading-snug text-muted-foreground">
                    Trao đổi trực tiếp với nhân viên
                  </span>
                </span>
              </button>
            )}
          </div>
        </section>
      )}

      {assistantMounted && showAssistant && (
        <div
          className="animate-in fade-in slide-in-from-bottom-2 duration-200"
          hidden={mode !== "assistant"}
          inert={mode !== "assistant" || undefined}
        >
          <SiteAssistantWidget
            shopId={shopId}
            brandName={brandName}
            defaultOpen
            embedded
            active={mode === "assistant"}
            onClose={closeDock}
          />
        </div>
      )}

      {chatMounted && showChat && (
        <div
          className="animate-in fade-in slide-in-from-bottom-2 duration-200"
          hidden={mode !== "chat"}
          inert={mode !== "chat" || undefined}
        >
          <SiteChatWidget
            shopId={shopId}
            brandName={brandName}
            externalConversationId={handoffConversationId}
            defaultOpen
            embedded
            active={mode === "chat"}
            onClose={closeDock}
          />
        </div>
      )}

      <button
        ref={launcherRef}
        type="button"
        onClick={handleTriggerClick}
        aria-expanded={mode !== "closed"}
        aria-label={mode !== "closed" ? "Đóng hỗ trợ" : "Hỗ trợ"}
        className="inline-flex size-12 items-center justify-center rounded-full bg-[var(--site-band-bg)] text-sm font-semibold tracking-[-0.01em] text-[var(--site-band-ink)] transition-colors hover:bg-primary hover:text-primary-foreground active:scale-[0.97] md:h-11 md:w-auto md:min-w-11 md:gap-2 md:px-[18px]"
      >
        {mode !== "closed" ? (
          <X className="size-[18px]" aria-hidden="true" />
        ) : showAssistant && !showChat ? (
          <Sparkles className="size-[17px]" aria-hidden="true" />
        ) : (
          <MessageCircle className="size-[17px]" aria-hidden="true" />
        )}
        <span className="hidden leading-none md:inline">
          {mode !== "closed" ? "Đóng" : singleLabel}
        </span>
      </button>
    </div>
  );
}
