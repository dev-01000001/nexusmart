"use client";

import React, { useEffect } from "react";
import { ShopHeader } from "@/components/ShopHeader";
import { ShopFooter } from "@/components/ShopFooter";
import { BuyerChatPanel } from "@/features/commerce/BuyerChatPanel";
import { AiAssistantPanel } from "@/features/commerce/AiAssistantPanel";
import { useMockStore } from "@/mock/MockStoreContext";
import styles from "./layout.module.css";

export default function ShopLayout({ children }: { children: React.ReactNode }) {
  const { toastMessage, dismissToast } = useMockStore();

  useEffect(() => {
    if (!toastMessage) return;
    const timer = window.setTimeout(() => {
      dismissToast();
    }, 3600);
    return () => window.clearTimeout(timer);
  }, [toastMessage, dismissToast]);

  return (
    <div className={styles.shell}>
      <ShopHeader />
      <main id="main-content" tabIndex={-1} className={styles.main}>
        {children}
      </main>
      <ShopFooter />
      <BuyerChatPanel />
      <AiAssistantPanel />

      {toastMessage && (
        <div className={styles.toast} role="status" aria-live="polite">
          <span>{toastMessage}</span>
          <button type="button" className={styles.toastClose} onClick={dismissToast}>
            Đóng
          </button>
        </div>
      )}
    </div>
  );
}
