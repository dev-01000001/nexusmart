"use client";

import React from "react";
import Link from "next/link";
import { Monitor, Smartphone, Tablet } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { TemplatePageKey } from "./siteEditorTypes";
import { TEMPLATE_PAGE_TABS } from "./siteEditorUtils";
import type { DevicePreviewWidth } from "./SitePreviewFrame";

export interface PreviewToolbarProps {
  activePage: TemplatePageKey;
  onPageChange?: (page: TemplatePageKey) => void;
  getPageHref?: (page: TemplatePageKey) => string;
  device: DevicePreviewWidth;
  onDeviceChange: (device: DevicePreviewWidth) => void;
  pageNavAriaLabel?: string;
  className?: string;
}

const DEVICE_OPTIONS: ReadonlyArray<{
  value: DevicePreviewWidth;
  label: string;
  ariaLabel: string;
  icon: typeof Smartphone;
}> = [
  { value: "390", label: "390", ariaLabel: "Khổ di động 390px", icon: Smartphone },
  { value: "768", label: "768", ariaLabel: "Khổ máy tính bảng 768px", icon: Tablet },
  { value: "1440", label: "1440", ariaLabel: "Khổ máy tính 1440px", icon: Monitor },
];

export function PreviewToolbar({
  activePage,
  onPageChange,
  getPageHref,
  device,
  onDeviceChange,
  pageNavAriaLabel = "Chọn trang xem trước",
  className,
}: PreviewToolbarProps) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center justify-between gap-2.5 border-b border-border bg-muted/45 px-3 py-2.5",
        className
      )}
    >
      {getPageHref ? (
        <nav className="flex min-w-0 flex-wrap items-center gap-1.5" aria-label={pageNavAriaLabel}>
          {TEMPLATE_PAGE_TABS.map((tab) => {
            const isActive = activePage === tab.key;
            return (
              <Link
                key={tab.key}
                href={getPageHref(tab.key)}
                aria-current={isActive ? "page" : undefined}
                className={cn(
                  "inline-flex min-h-10 items-center rounded-md border px-2.5 text-[13px] font-medium whitespace-nowrap transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:min-h-8",
                  isActive
                    ? "border-brand/40 bg-card font-semibold text-foreground shadow-card"
                    : "border-transparent text-muted-foreground hover:bg-card/80 hover:text-foreground"
                )}
              >
                {tab.shortLabel}
              </Link>
            );
          })}
        </nav>
      ) : (
        <div
          className="flex min-w-0 flex-wrap items-center gap-1.5"
          role="tablist"
          aria-label={pageNavAriaLabel}
        >
          {TEMPLATE_PAGE_TABS.map((tab) => {
            const isActive = activePage === tab.key;
            return (
              <button
                key={tab.key}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => onPageChange?.(tab.key)}
                className={cn(
                  "inline-flex min-h-10 items-center rounded-md border px-2.5 text-[13px] font-medium whitespace-nowrap transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:min-h-8",
                  isActive
                    ? "border-brand/40 bg-card font-semibold text-foreground shadow-card"
                    : "border-transparent text-muted-foreground hover:bg-card/80 hover:text-foreground"
                )}
              >
                {tab.shortLabel}
              </button>
            );
          })}
        </div>
      )}

      <div
        className="inline-flex shrink-0 items-center gap-1 rounded-lg border border-border bg-card p-1"
        role="group"
        aria-label="Chọn chiều rộng khung xem trước"
      >
        {DEVICE_OPTIONS.map((opt) => {
          const Icon = opt.icon;
          const isActive = device === opt.value;
          return (
            <Button
              key={opt.value}
              type="button"
              size="xs"
              variant={isActive ? "secondary" : "ghost"}
              aria-pressed={isActive}
              aria-label={opt.ariaLabel}
              onClick={() => onDeviceChange(opt.value)}
              className={cn(
                "min-h-9 gap-1 px-2 text-xs tabular-nums md:min-h-7",
                isActive ? "font-semibold text-foreground" : "text-muted-foreground"
              )}
            >
              <Icon className="size-3.5 shrink-0" aria-hidden="true" />
              <span>{opt.label}</span>
            </Button>
          );
        })}
      </div>
    </div>
  );
}
