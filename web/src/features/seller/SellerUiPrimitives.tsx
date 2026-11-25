"use client";

import React, { useEffect, useLayoutEffect, useRef, useState } from "react";
import { IconClose } from "@/components/Icons";
import { useModalFocusTrap } from "@/components/modalFocus";
import common from "./SellerCommon.module.css";

export type SellerPillTone = "pos" | "warn" | "neg" | "neutral";

export function statusPillClass(tone: SellerPillTone): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
}

export interface SlidingTabOption<T extends string> {
  key: T;
  label: React.ReactNode;
  count?: number;
}

export interface SellerSlidingTabsProps<T extends string> {
  tabs: ReadonlyArray<SlidingTabOption<T>>;
  value: T;
  onChange: (next: T) => void;
  ariaLabel: string;
  className?: string;
}

/**
 * Horizontal underline tabs with a single smoothly sliding 2px accent indicator.
 */
export function SellerSlidingTabs<T extends string>({
  tabs,
  value,
  onChange,
  ariaLabel,
  className,
}: SellerSlidingTabsProps<T>) {
  const listRef = useRef<HTMLDivElement | null>(null);
  const btnRefs = useRef<Record<string, HTMLButtonElement | null>>({});
  const [bar, setBar] = useState<{ left: number; width: number; ready: boolean }>({
    left: 0,
    width: 0,
    ready: false,
  });

  const tabsSignature = tabs.map((tab) => `${tab.key}:${tab.count ?? ""}`).join("|");

  useLayoutEffect(() => {
    const update = () => {
      const listEl = listRef.current;
      const activeEl = btnRefs.current[value];
      if (!listEl || !activeEl) return;
      setBar({
        left: activeEl.offsetLeft,
        width: activeEl.offsetWidth,
        ready: true,
      });
    };

    update();
    if (typeof ResizeObserver === "undefined" || !listRef.current) return;
    const ro = new ResizeObserver(update);
    ro.observe(listRef.current);
    for (const el of Object.values(btnRefs.current)) {
      if (el) ro.observe(el);
    }
    return () => ro.disconnect();
  }, [value, tabsSignature]);

  return (
    <div
      ref={listRef}
      className={`${common.underlineTabs} ${className ?? ""}`}
      role="tablist"
      aria-label={ariaLabel}
    >
      {tabs.map((tab) => {
        const active = tab.key === value;
        return (
          <button
            key={tab.key}
            ref={(el) => {
              btnRefs.current[tab.key] = el;
            }}
            type="button"
            role="tab"
            aria-selected={active}
            className={`${common.underlineTab} ${active ? common.underlineTabActive : ""}`}
            onClick={() => onChange(tab.key)}
          >
            <span>{tab.label}</span>
            {tab.count != null && <span className={common.underlineTabCount}>{tab.count}</span>}
          </button>
        );
      })}
      <span
        className={common.underlineSlider}
        aria-hidden="true"
        style={{
          transform: `translateX(${bar.left}px)`,
          width: `${bar.width}px`,
          opacity: bar.ready ? 1 : 0,
        }}
      />
    </div>
  );
}

export interface SellerDrawerProps {
  open: boolean;
  title: string;
  subtitle?: string;
  badge?: React.ReactNode;
  onClose: () => void;
  footer?: React.ReactNode;
  children: React.ReactNode;
}

/**
 * Right-hand slide-over detail drawer with animated enter and faster exit, Escape close,
 * flat tint scrim (zero glass/blur), and focus return.
 */
export function SellerDrawer({
  open,
  title,
  subtitle,
  badge,
  onClose,
  footer,
  children,
}: SellerDrawerProps) {
  const [rendered, setRendered] = useState(open);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const closeBtnRef = useRef<HTMLButtonElement | null>(null);
  const lastOpenContentRef = useRef<{
    title: string;
    subtitle?: string;
    badge?: React.ReactNode;
    footer?: React.ReactNode;
    children: React.ReactNode;
  } | null>(null);

  if (open) {
    lastOpenContentRef.current = { title, subtitle, badge, footer, children };
  }

  useEffect(() => {
    if (open) {
      setRendered(true);
      return;
    }
    if (!rendered) return;
    const timer = window.setTimeout(() => {
      setRendered(false);
      lastOpenContentRef.current = null;
    }, 180);
    return () => window.clearTimeout(timer);
  }, [open, rendered]);

  useModalFocusTrap({
    open,
    containerRef: panelRef,
    initialFocusRef: closeBtnRef,
    onEscape: onClose,
  });

  useEffect(() => {
    if (!open) return;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prevOverflow;
    };
  }, [open]);

  if (!open && !rendered) return null;

  const closing = !open && rendered;
  const display =
    open || !lastOpenContentRef.current
      ? { title, subtitle, badge, footer, children }
      : lastOpenContentRef.current;

  return (
    <div
      className={`${common.drawerBackdrop} ${closing ? common.drawerBackdropClosing : ""}`}
      aria-hidden={closing || undefined}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={display.title}
        className={`${common.drawerPanel} ${closing ? common.drawerPanelClosing : ""}`}
      >
        <div className={common.drawerHead}>
          <div className={common.drawerHeadText}>
            <div className={common.drawerTitleRow}>
              <h2 className={common.drawerTitle}>{display.title}</h2>
              {display.badge}
            </div>
            {display.subtitle && <p className={common.drawerSubtitle}>{display.subtitle}</p>}
          </div>
          <button
            ref={closeBtnRef}
            type="button"
            className={common.drawerCloseBtn}
            onClick={onClose}
            aria-label="Đóng ngăn chi tiết"
          >
            <IconClose size={16} />
          </button>
        </div>

        <div className={common.drawerBody}>{display.children}</div>

        {display.footer && <div className={common.drawerFooter}>{display.footer}</div>}
      </div>
    </div>
  );
}

export interface SellerBulkBarProps {
  count: number;
  label: string;
  onClear: () => void;
  children: React.ReactNode;
}

/**
 * Floating bottom slide-up bulk action bar for multi-select tables.
 */
export function SellerBulkBar({ count, label, onClear, children }: SellerBulkBarProps) {
  if (count <= 0) return null;

  return (
    <div className={common.bulkBar} role="region" aria-label="Thao tác hàng loạt">
      <div className={common.bulkCount}>
        <strong className="num">{count}</strong>
        <span>{label}</span>
      </div>
      <div className={common.bulkActions}>
        {children}
        <button type="button" className={common.bulkClearBtn} onClick={onClear}>
          Bỏ chọn
        </button>
      </div>
    </div>
  );
}
