"use client";

import type React from "react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Toaster } from "@/components/ui/sonner";
import { usePortalContainer } from "@/components/ui/ui-scope";
import { useStore } from "@/context/StoreContext";

type SiteThemeMode = "light" | "dark";

function findSiteThemeRoot(portalContainer: HTMLElement | null): HTMLElement | null {
  if (portalContainer) {
    const ancestor = portalContainer.closest<HTMLElement>("[data-site-root]");
    if (ancestor) return ancestor;
  }
  if (typeof document === "undefined") return null;
  return document.querySelector<HTMLElement>("[data-site-root]");
}

function readSiteThemeMode(root: HTMLElement | null): SiteThemeMode {
  return root?.getAttribute("data-site-theme-mode") === "dark" ? "dark" : "light";
}

const SITE_TOASTER_STYLE = {
  "--normal-bg": "var(--ui-popover)",
  "--normal-text": "var(--ui-popover-foreground)",
  "--normal-border": "var(--ui-border)",
  "--normal-bg-hover": "var(--ui-accent)",
  "--normal-border-hover": "var(--ui-input)",
  "--border-radius": "var(--ui-radius)",
  fontFamily: "var(--ui-font), ui-sans-serif, system-ui, sans-serif",
} as React.CSSProperties;

const SITE_TOAST_OPTIONS = {
  style: {
    background: "var(--ui-popover)",
    color: "var(--ui-popover-foreground)",
    borderColor: "var(--ui-border)",
    borderRadius: "var(--ui-radius)",
    boxShadow: "var(--ui-shadow-overlay)",
    fontFamily: "var(--ui-font), ui-sans-serif, system-ui, sans-serif",
  },
  actionButtonStyle: {
    background: "var(--ui-primary)",
    color: "var(--ui-primary-foreground)",
  },
  cancelButtonStyle: {
    background: "var(--ui-secondary)",
    color: "var(--ui-secondary-foreground)",
  },
  classNames: {
    toast:
      "group toast bg-popover text-popover-foreground border border-border shadow-overlay rounded-lg px-4 py-3 text-sm",
    title: "text-popover-foreground font-medium",
    description: "text-muted-foreground text-xs",
    actionButton:
      "bg-primary text-primary-foreground font-medium rounded-md px-2.5 py-1 text-xs hover:opacity-90",
    cancelButton:
      "bg-secondary text-secondary-foreground font-medium rounded-md px-2.5 py-1 text-xs hover:bg-accent",
    closeButton: "bg-popover text-popover-foreground border-border hover:bg-accent",
  },
};

/**
 * Lazy-loaded site Toaster (sonner) that adapts to the active site theme mode
 * (`data-site-theme-mode` on `[data-site-root]`), uses the site's `--ui-*`
 * tokens, and bridges `StoreContext.showToast()` messages.
 */
export function SiteToasterInner() {
  const { toastMessage, dismissToast } = useStore();
  const portalContainer = usePortalContainer();
  const [themeMode, setThemeMode] = useState<SiteThemeMode>(() =>
    readSiteThemeMode(findSiteThemeRoot(portalContainer))
  );

  useEffect(() => {
    const root = findSiteThemeRoot(portalContainer);
    setThemeMode(readSiteThemeMode(root));
    if (!root || typeof MutationObserver === "undefined") return;

    const observer = new MutationObserver(() => {
      setThemeMode(readSiteThemeMode(root));
    });
    observer.observe(root, {
      attributes: true,
      attributeFilter: ["data-site-theme-mode"],
    });
    return () => observer.disconnect();
  }, [portalContainer]);

  useEffect(() => {
    if (!toastMessage) return;
    toast(toastMessage);
    dismissToast();
  }, [toastMessage, dismissToast]);

  return (
    <Toaster
      position="bottom-right"
      theme={themeMode}
      style={SITE_TOASTER_STYLE}
      toastOptions={SITE_TOAST_OPTIONS}
    />
  );
}
