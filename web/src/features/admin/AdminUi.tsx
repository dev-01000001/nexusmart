"use client";

import React, { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { Button, IconCheck, IconClose } from "@/components";
import { useModalFocusTrap } from "@/components/modalFocus";
import { formatInt, formatTimeVi, formatVnd } from "@/lib/format";
import shared from "./adminShared.module.css";

/* ---------------------------------------------------------------------------
   Shared sliding underline hook (used by Tier 1/2 AdminShell nav & SlidingTabBar).
   Pairs with a CSS ::after underline on the active tab so first paint (SSR and
   pre-measurement) is always accurate, then hands off without flicker to the GPU
   sliding indicator and re-measures on font load and resize.
   --------------------------------------------------------------------------- */
export interface SlidingIndicatorRect {
  x: number;
  w: number;
  ready: boolean;
  animate: boolean;
}

export function useSlidingUnderline(
  activeKey: string,
  depsKey: string,
  selector: string,
  scrollActiveIntoView = false
): {
  containerRef: React.RefObject<HTMLDivElement | null>;
  rect: SlidingIndicatorRect;
} {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [rect, setRect] = useState<SlidingIndicatorRect>({
    x: 0,
    w: 0,
    ready: false,
    animate: false,
  });
  const prevActiveKeyRef = useRef(activeKey);

  const measure = useCallback(
    (shouldAnimate: boolean) => {
      const container = containerRef.current;
      if (!container) return;
      const activeEl = container.querySelector<HTMLElement>(selector);
      if (!activeEl) {
        setRect((prev) => (prev.ready ? { ...prev, ready: false, animate: false } : prev));
        return;
      }
      const cRect = container.getBoundingClientRect();
      const aRect = activeEl.getBoundingClientRect();
      if (cRect.width <= 0 || aRect.width <= 0) {
        setRect((prev) => (prev.ready ? { ...prev, ready: false, animate: false } : prev));
        return;
      }
      const x = Math.round((aRect.left - cRect.left + container.scrollLeft) * 100) / 100;
      const w = Math.round(aRect.width * 100) / 100;
      setRect((prev) => {
        if (prev.ready && prev.x === x && prev.w === w) return prev;
        return { x, w, ready: true, animate: shouldAnimate && prev.ready };
      });
    },
    [selector]
  );

  useLayoutEffect(() => {
    const activeChanged = prevActiveKeyRef.current !== activeKey;
    prevActiveKeyRef.current = activeKey;
    measure(activeChanged);
    if (scrollActiveIntoView && containerRef.current) {
      const activeEl = containerRef.current.querySelector<HTMLElement>(selector);
      activeEl?.scrollIntoView({ inline: "nearest", block: "nearest" });
    }
    const raf = window.requestAnimationFrame(() => measure(false));
    return () => window.cancelAnimationFrame(raf);
  }, [measure, activeKey, depsKey, selector, scrollActiveIntoView]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let alive = true;
    const remeasure = () => {
      if (alive) measure(false);
    };

    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(remeasure);
      ro.observe(container);
      for (const child of Array.from(container.children)) {
        if (child instanceof HTMLElement && !child.hasAttribute("aria-hidden")) {
          ro.observe(child);
        }
      }
    }

    window.addEventListener("resize", remeasure);

    const fontSet = typeof document !== "undefined" && "fonts" in document ? document.fonts : null;
    if (fontSet) {
      void fontSet.ready.then(remeasure);
      fontSet.addEventListener?.("loadingdone", remeasure);
    }

    return () => {
      alive = false;
      ro?.disconnect();
      window.removeEventListener("resize", remeasure);
      fontSet?.removeEventListener?.("loadingdone", remeasure);
    };
  }, [measure, depsKey]);

  return { containerRef, rect };
}

export interface SlidingTabItem<T extends string> {
  id: T;
  label: string;
  count?: number;
  dataChannelTab?: string;
}

export interface SlidingTabBarProps<T extends string> {
  tabs: Array<SlidingTabItem<T>>;
  activeId: T;
  onChange: (id: T) => void;
  ariaLabel: string;
  rightSlot?: React.ReactNode;
}

export function SlidingTabBar<T extends string>({
  tabs,
  activeId,
  onChange,
  ariaLabel,
  rightSlot,
}: SlidingTabBarProps<T>) {
  const tabsKey = tabs.map((t) => `${t.id}:${t.count ?? ""}`).join("|");
  const { containerRef: listRef, rect: indicator } = useSlidingUnderline(
    activeId,
    tabsKey,
    '[role="tab"][aria-selected="true"]'
  );

  return (
    <div className={shared.tabsBar}>
      <div
        ref={listRef}
        className={`${shared.tabList} ${indicator.ready ? shared.tabListMeasured : ""}`}
        role="tablist"
        aria-label={ariaLabel}
      >
        {tabs.map((tab) => {
          const active = tab.id === activeId;
          return (
            <button
              key={tab.id || "__all__"}
              type="button"
              role="tab"
              aria-selected={active}
              data-channel-tab={tab.dataChannelTab}
              className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
              onClick={() => onChange(tab.id)}
            >
              <span>{tab.label}</span>
              {tab.count !== undefined && <span className={shared.tabCount}>{tab.count}</span>}
            </button>
          );
        })}
        <span
          aria-hidden="true"
          className={`${shared.tabIndicator} ${indicator.ready ? shared.tabIndicatorReady : ""} ${indicator.animate ? shared.tabIndicatorAnimate : ""}`}
          style={{
            transform: `translate3d(${indicator.x}px, 0, 0) scaleX(${indicator.w})`,
          }}
        />
      </div>
      {rightSlot && <div className={shared.tabsMeta}>{rightSlot}</div>}
    </div>
  );
}

function formatCountValue(n: number, format: "int" | "vnd"): string {
  return format === "vnd" ? formatVnd(Math.round(n)) : formatInt(Math.round(n));
}

export function CountUpValue({ value, format = "int" }: { value: number; format?: "int" | "vnd" }) {
  const [display, setDisplay] = useState<string>(() => formatCountValue(value, format));
  const animatedRef = useRef(false);

  useEffect(() => {
    const finalText = formatCountValue(value, format);
    if (animatedRef.current || value === 0) {
      setDisplay(finalText);
      return;
    }
    const reduced =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced || typeof requestAnimationFrame === "undefined") {
      animatedRef.current = true;
      setDisplay(finalText);
      return;
    }

    animatedRef.current = true;
    const duration = 560;
    const start = performance.now();
    let rafId = 0;

    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / duration);
      const eased = 1 - Math.pow(1 - p, 3);
      setDisplay(p >= 1 ? finalText : formatCountValue(value * eased, format));
      if (p < 1) {
        rafId = requestAnimationFrame(tick);
      }
    };

    rafId = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(rafId);
  }, [value, format]);

  return <>{display}</>;
}

export interface AdminDrawerProps {
  open: boolean;
  onClose: () => void;
  title: string;
  subtitle?: string;
  headerBadge?: React.ReactNode;
  width?: "md" | "lg";
  children: React.ReactNode;
}

export function AdminDrawer({
  open,
  onClose,
  title,
  subtitle,
  headerBadge,
  width = "md",
  children,
}: AdminDrawerProps) {
  const [rendered, setRendered] = useState(open);
  const [visible, setVisible] = useState(open);
  const panelRef = useRef<HTMLElement | null>(null);
  const closeBtnRef = useRef<HTMLButtonElement | null>(null);
  const lastOpenContentRef = useRef<{
    title: string;
    subtitle?: string;
    headerBadge?: React.ReactNode;
    width: "md" | "lg";
    children: React.ReactNode;
  } | null>(null);

  if (open) {
    lastOpenContentRef.current = { title, subtitle, headerBadge, width, children };
  }

  const isMounted = open || rendered;

  useEffect(() => {
    if (open) {
      setRendered(true);
      const raf = window.requestAnimationFrame(() => {
        setVisible(true);
      });
      return () => window.cancelAnimationFrame(raf);
    }
    setVisible(false);
    const timer = window.setTimeout(() => {
      setRendered(false);
      lastOpenContentRef.current = null;
    }, 210);
    return () => window.clearTimeout(timer);
  }, [open]);

  const resolveDrawerFocus = useCallback((container: HTMLElement): HTMLElement | null => {
    const autoFocused = container.querySelector<HTMLElement>("[data-autofocus]");
    if (autoFocused && !autoFocused.hasAttribute("disabled")) {
      return autoFocused;
    }
    return closeBtnRef.current;
  }, []);

  useModalFocusTrap({
    open,
    containerRef: panelRef,
    resolveInitialFocus: resolveDrawerFocus,
    onEscape: onClose,
  });

  if (!isMounted) return null;

  const display =
    open || !lastOpenContentRef.current
      ? { title, subtitle, headerBadge, width, children }
      : lastOpenContentRef.current;

  return (
    <div
      className={visible ? `${shared.drawerRoot} ${shared.drawerOpen}` : shared.drawerRoot}
      role="presentation"
    >
      <div className={shared.drawerBackdrop} onClick={onClose} aria-hidden="true" />
      <aside
        ref={panelRef}
        tabIndex={-1}
        className={`${shared.drawerPanel} ${display.width === "lg" ? shared.drawerPanelLg : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label={display.title}
      >
        <div className={shared.drawerHeader}>
          <div className={shared.drawerHeaderText}>
            <div className={shared.drawerTitleRow}>
              <h2 className={shared.drawerTitle}>{display.title}</h2>
              {display.headerBadge}
            </div>
            {display.subtitle && <span className={shared.drawerSubtitle}>{display.subtitle}</span>}
          </div>
          <button
            ref={closeBtnRef}
            type="button"
            className={shared.drawerCloseBtn}
            onClick={onClose}
            aria-label="Đóng chi tiết"
          >
            <IconClose size={18} />
          </button>
        </div>

        <div className={shared.drawerBody}>{display.children}</div>
      </aside>
    </div>
  );
}

export interface AdminConfirmDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  children?: React.ReactNode;
}

export function AdminConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  busy = false,
  onConfirm,
  onCancel,
  children,
}: AdminConfirmDialogProps) {
  const baseId = useId();
  const titleId = `${baseId}-title`;
  const descId = `${baseId}-desc`;
  const [rendered, setRendered] = useState(open);
  const [visible, setVisible] = useState(open);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const lastOpenContentRef = useRef<{
    title: string;
    description: string;
    confirmLabel: string;
    children?: React.ReactNode;
  } | null>(null);

  if (open) {
    lastOpenContentRef.current = { title, description, confirmLabel, children };
  }

  const isMounted = open || rendered;

  useEffect(() => {
    if (open) {
      setRendered(true);
      const raf = window.requestAnimationFrame(() => setVisible(true));
      return () => window.cancelAnimationFrame(raf);
    }
    setVisible(false);
    const timer = window.setTimeout(() => {
      setRendered(false);
      lastOpenContentRef.current = null;
    }, 200);
    return () => window.clearTimeout(timer);
  }, [open]);

  const resolveDialogFocus = useCallback((container: HTMLElement): HTMLElement | null => {
    const autoFocused = container.querySelector<HTMLElement>("[data-autofocus]");
    if (autoFocused && !autoFocused.hasAttribute("disabled")) return autoFocused;

    const fieldInput = container.querySelector<HTMLElement>(
      'input:not([disabled]):not([type="hidden"]), textarea:not([disabled])'
    );
    if (fieldInput) return fieldInput;

    const cancelBtn = container.querySelector<HTMLElement>("[data-dialog-cancel]");
    if (cancelBtn && !cancelBtn.hasAttribute("disabled")) return cancelBtn;

    return null;
  }, []);

  useModalFocusTrap({
    open,
    containerRef: cardRef,
    resolveInitialFocus: resolveDialogFocus,
    onEscape: onCancel,
    disableEscape: busy,
  });

  if (!isMounted) return null;

  const display =
    open || !lastOpenContentRef.current
      ? { title, description, confirmLabel, children }
      : lastOpenContentRef.current;

  return (
    <div
      className={visible ? `${shared.dialogRoot} ${shared.dialogOpen}` : shared.dialogRoot}
      role="presentation"
    >
      <div
        className={shared.dialogBackdrop}
        onClick={() => {
          if (!busy) onCancel();
        }}
        aria-hidden="true"
      />
      <div
        ref={cardRef}
        tabIndex={-1}
        className={shared.dialogCard}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descId}
      >
        <div className={shared.dialogHead}>
          <h2 id={titleId} className={shared.dialogTitle}>
            {display.title}
          </h2>
          <button
            type="button"
            className={shared.drawerCloseBtn}
            onClick={onCancel}
            disabled={busy}
            aria-label="Đóng hộp xác nhận"
          >
            <IconClose size={16} />
          </button>
        </div>
        <p id={descId} className={shared.dialogDesc}>
          {display.description}
        </p>
        {display.children && <div className={shared.dialogExtra}>{display.children}</div>}
        <div className={shared.dialogActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={busy}
            data-dialog-cancel="true"
            onClick={onCancel}
          >
            Hủy
          </Button>
          <Button variant="danger" size="sm" shape="app" loading={busy} onClick={onConfirm}>
            {display.confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}

export function InlineSavedBadge({ stamp }: { stamp: string | null }) {
  if (!stamp) return null;
  return (
    <span className={shared.inlineSavedBadge} role="status" aria-live="polite">
      <IconCheck size={14} />
      <span>Đã lưu lúc {stamp}</span>
    </span>
  );
}

export function formatNowTimeVi(): string {
  return formatTimeVi(Date.now());
}

export function isEditableElement(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  return target.isContentEditable;
}
