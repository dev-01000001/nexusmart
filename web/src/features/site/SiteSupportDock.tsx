"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { IconChat, IconClose, IconSpark } from "@/components/Icons";
import { getFocusableElements, hasOpenModal } from "@/components/modalFocus";
import { SiteAssistantWidget } from "@/features/assistant";
import { OPEN_SITE_CHAT_EVENT, SiteChatWidget, type OpenSiteChatDetail } from "@/features/chat";
import { useSitePathname } from "./SiteChrome";
import styles from "./SiteSupportDock.module.css";

export interface SiteSupportDockProps {
  shopId: string;
  brandName: string;
  showChat: boolean;
  showAssistant: boolean;
}

type DockMode = "closed" | "chooser" | "assistant" | "chat";

/**
 * Single unified floating support launcher combining AI Assistant and Shop Chat.
 * Opens a compact choice sheet (or goes straight to the single enabled channel),
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
  const [closing, setClosing] = useState(false);
  const [assistantMounted, setAssistantMounted] = useState(false);
  const [chatMounted, setChatMounted] = useState(false);
  const [handoffConversationId, setHandoffConversationId] = useState<string | null>(null);
  const launcherRef = useRef<HTMLButtonElement | null>(null);
  const chooserRef = useRef<HTMLElement | null>(null);
  const closeTimerRef = useRef<number | null>(null);
  const aboveStickyBar = pathname.startsWith("/gio-hang") || pathname.startsWith("/thanh-toan");

  const openMode = useCallback((next: Exclude<DockMode, "closed">) => {
    if (next === "assistant") setAssistantMounted(true);
    if (next === "chat") setChatMounted(true);
    setMode(next);
  }, []);

  const clearCloseTimer = useCallback(() => {
    if (closeTimerRef.current !== null) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  }, []);

  const closeDock = useCallback(
    (restoreFocus = true) => {
      if (mode === "closed" || closing) return;
      clearCloseTimer();
      setClosing(true);
      if (restoreFocus && launcherRef.current?.isConnected) {
        launcherRef.current.focus({ preventScroll: true });
      }
      closeTimerRef.current = window.setTimeout(() => {
        closeTimerRef.current = null;
        setMode("closed");
        setClosing(false);
      }, 160);
    },
    [clearCloseTimer, closing, mode]
  );

  useEffect(() => {
    return () => clearCloseTimer();
  }, [clearCloseTimer]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const onOpenChat = (ev: Event) => {
      const custom = ev as CustomEvent<OpenSiteChatDetail | undefined>;
      const convId = custom.detail?.conversationId?.trim();
      if (convId) {
        setHandoffConversationId(convId);
      }
      if (showChat) {
        clearCloseTimer();
        setClosing(false);
        openMode("chat");
      } else {
        closeDock(true);
      }
    };
    window.addEventListener(OPEN_SITE_CHAT_EVENT, onOpenChat);
    return () => window.removeEventListener(OPEN_SITE_CHAT_EVENT, onOpenChat);
  }, [clearCloseTimer, closeDock, openMode, showChat]);

  useEffect(() => {
    if (mode !== "chooser" || closing) return;
    const chooser = chooserRef.current;
    if (!chooser) return;
    const target = getFocusableElements(chooser)[0] ?? chooser;
    target.focus({ preventScroll: true });
  }, [mode, closing]);

  useEffect(() => {
    if (mode === "closed" || closing) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !hasOpenModal()) {
        ev.preventDefault();
        closeDock(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mode, closing, closeDock]);

  if (!showChat && !showAssistant) return null;

  const handleTriggerClick = () => {
    if (mode !== "closed") {
      closeDock(true);
      return;
    }
    clearCloseTimer();
    setClosing(false);
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
      className={styles.dock}
      data-ui="site-support-dock"
      data-open={mode !== "closed"}
      data-above-sticky={aboveStickyBar ? "true" : undefined}
    >
      {mode === "chooser" && (
        <section
          ref={chooserRef}
          tabIndex={-1}
          className={`${styles.sheet} ${closing ? styles.sheetClosing : ""}`}
          role="dialog"
          aria-label={`Chọn kênh hỗ trợ ${brandName}`}
        >
          <header className={styles.sheetHead}>
            <span className={styles.sheetTitle}>Hỗ trợ {brandName}</span>
            <button
              type="button"
              className={styles.closeBtn}
              onClick={() => closeDock(true)}
              aria-label="Đóng bảng chọn hỗ trợ"
            >
              <IconClose size={16} />
            </button>
          </header>

          <div className={styles.optionList}>
            {showAssistant && (
              <button
                type="button"
                className={styles.optionBtn}
                onClick={() => openMode("assistant")}
              >
                <span className={styles.optionIcon} aria-hidden="true">
                  <IconSpark size={18} />
                </span>
                <span className={styles.optionText}>
                  <span className={styles.optionName}>Trợ lý mua sắm AI</span>
                  <span className={styles.optionHint}>Tư vấn mẫu, chọn size, tra đơn</span>
                </span>
              </button>
            )}

            {showChat && (
              <button type="button" className={styles.optionBtn} onClick={() => openMode("chat")}>
                <span className={styles.optionIcon} aria-hidden="true">
                  <IconChat size={18} />
                </span>
                <span className={styles.optionText}>
                  <span className={styles.optionName}>Nhắn tin cửa hàng</span>
                  <span className={styles.optionHint}>Trao đổi trực tiếp với nhân viên</span>
                </span>
              </button>
            )}
          </div>
        </section>
      )}

      {assistantMounted && showAssistant && (
        <div
          className={`${styles.panelSlot} ${
            mode === "assistant" && closing ? styles.sheetClosing : ""
          }`}
          hidden={mode !== "assistant"}
          inert={mode !== "assistant" || undefined}
        >
          <SiteAssistantWidget
            shopId={shopId}
            brandName={brandName}
            defaultOpen
            embedded
            active={mode === "assistant" && !closing}
            onClose={closeDock}
          />
        </div>
      )}

      {chatMounted && showChat && (
        <div
          className={`${styles.panelSlot} ${mode === "chat" && closing ? styles.sheetClosing : ""}`}
          hidden={mode !== "chat"}
          inert={mode !== "chat" || undefined}
        >
          <SiteChatWidget
            shopId={shopId}
            brandName={brandName}
            externalConversationId={handoffConversationId}
            defaultOpen
            embedded
            active={mode === "chat" && !closing}
            onClose={closeDock}
          />
        </div>
      )}

      <button
        ref={launcherRef}
        type="button"
        className={styles.launcher}
        onClick={handleTriggerClick}
        aria-expanded={mode !== "closed"}
        aria-label={mode !== "closed" ? "Đóng hỗ trợ" : "Hỗ trợ"}
      >
        {mode !== "closed" ? (
          <IconClose size={18} />
        ) : showAssistant && !showChat ? (
          <IconSpark size={17} />
        ) : (
          <IconChat size={17} />
        )}
        <span className={styles.launcherText}>{mode !== "closed" ? "Đóng" : singleLabel}</span>
      </button>
    </div>
  );
}
