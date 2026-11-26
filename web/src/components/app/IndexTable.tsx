"use client";

import * as React from "react";
import Link from "next/link";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface IndexTableTab {
  key: string;
  label: string;
  count?: number;
  href?: string;
}

export interface IndexTableSelection {
  count: number;
  actions: React.ReactNode;
  onClear: () => void;
}

export interface IndexTableProps {
  tabs?: IndexTableTab[];
  activeTab?: string;
  onTabChange?: (key: string) => void;
  toolbar?: React.ReactNode;
  selection?: IndexTableSelection;
  footer?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}

export function IndexTable({
  tabs,
  activeTab,
  onTabChange,
  toolbar,
  selection,
  footer,
  children,
  className,
}: IndexTableProps) {
  const hasSelection = Boolean(selection && selection.count > 0);
  const showHeader = hasSelection || Boolean(tabs && tabs.length > 0) || Boolean(toolbar);

  return (
    <div className={cn("overflow-hidden rounded-xl bg-card shadow-card", className)}>
      {showHeader ? (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2.5 md:px-4">
          {hasSelection && selection ? (
            <div className="flex w-full flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2 text-[13px] font-semibold text-foreground tabular-nums">
                <span>Đã chọn {selection.count}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={selection.onClear}
                  className="text-muted-foreground hover:text-foreground"
                >
                  <X className="size-3.5" aria-hidden="true" />
                  <span>Bỏ chọn</span>
                </Button>
              </div>
              <div className="flex flex-wrap items-center gap-2">{selection.actions}</div>
            </div>
          ) : (
            <>
              {tabs && tabs.length > 0 ? (
                <div
                  role="tablist"
                  aria-label="Bộ lọc danh sách"
                  className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto pb-0.5"
                >
                  {tabs.map((tab) => {
                    const isActive = tab.key === activeTab;
                    const tabClass = cn(
                      "inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md px-3 text-[13px] font-medium whitespace-nowrap transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      isActive
                        ? "bg-secondary font-semibold text-foreground"
                        : "text-muted-foreground hover:bg-secondary/60 hover:text-foreground"
                    );
                    const content = (
                      <>
                        <span>{tab.label}</span>
                        {typeof tab.count === "number" ? (
                          <span className="text-xs text-muted-foreground tabular-nums">
                            {tab.count}
                          </span>
                        ) : null}
                      </>
                    );

                    if (tab.href) {
                      return (
                        <Link
                          key={tab.key}
                          href={tab.href}
                          role="tab"
                          aria-selected={isActive}
                          className={tabClass}
                        >
                          {content}
                        </Link>
                      );
                    }

                    return (
                      <button
                        key={tab.key}
                        type="button"
                        role="tab"
                        aria-selected={isActive}
                        onClick={() => onTabChange?.(tab.key)}
                        className={tabClass}
                      >
                        {content}
                      </button>
                    );
                  })}
                </div>
              ) : (
                <div className="flex-1" />
              )}

              {toolbar ? <div className="flex shrink-0 items-center gap-2">{toolbar}</div> : null}
            </>
          )}
        </div>
      ) : null}

      <div className="min-w-0">{children}</div>

      {footer ? (
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border px-4 py-3 text-[13px] text-muted-foreground">
          {footer}
        </div>
      ) : null}
    </div>
  );
}

export interface IndexRowCardProps {
  href?: string;
  onClick?: () => void;
  media?: React.ReactNode;
  title: React.ReactNode;
  badge?: React.ReactNode;
  meta?: React.ReactNode;
  leading?: React.ReactNode;
  className?: string;
}

export function IndexRowCard({
  href,
  onClick,
  media,
  title,
  badge,
  meta,
  leading,
  className,
}: IndexRowCardProps) {
  const body = (
    <div className="flex min-w-0 flex-1 items-start gap-3">
      {media ? <div className="shrink-0">{media}</div> : null}
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center justify-between gap-1.5">
          <div className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">{title}</div>
          {badge ? <div className="shrink-0">{badge}</div> : null}
        </div>
        {meta ? (
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
            {meta}
          </div>
        ) : null}
      </div>
    </div>
  );

  return (
    <div
      className={cn(
        "flex min-h-14 items-start gap-3 border-b border-border px-4 py-3 transition-colors last:border-b-0",
        (href || onClick) && "hover:bg-accent/55",
        className
      )}
    >
      {leading ? (
        <div
          className="shrink-0 pt-0.5"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          {leading}
        </div>
      ) : null}

      {href ? (
        <Link href={href} className="flex min-w-0 flex-1 items-start">
          {body}
        </Link>
      ) : onClick ? (
        <button
          type="button"
          onClick={onClick}
          className="flex min-w-0 flex-1 items-start text-left"
        >
          {body}
        </button>
      ) : (
        body
      )}
    </div>
  );
}
