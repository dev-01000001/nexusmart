"use client";

import { useEffect } from "react";
import { toast } from "sonner";
import { Toaster } from "@/components/ui/sonner";
import { useStore } from "@/context/StoreContext";

/**
 * Lazy-loaded site Toaster (sonner) that also bridges `StoreContext.showToast()`
 * messages so both legacy store actions and direct `sonner` calls render uniformly.
 */
export function SiteToasterInner() {
  const { toastMessage, dismissToast } = useStore();

  useEffect(() => {
    if (!toastMessage) return;
    toast(toastMessage);
    dismissToast();
  }, [toastMessage, dismissToast]);

  return <Toaster position="bottom-right" />;
}
