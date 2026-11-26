"use client";

import React from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import type { StatusTone } from "@/components/app";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { PillTone } from "../adminMeta";

/**
 * Maps legacy 4-tone PillTone values from read-only admin helpers to 6-tone shadcn StatusTone.
 */
export function pillToneToStatusTone(tone: PillTone): StatusTone {
  switch (tone) {
    case "positive":
      return "success";
    case "negative":
      return "critical";
    case "warning":
      return "warning";
    case "neutral":
    default:
      return "neutral";
  }
}

export function flashWindowStateTone(state: "UPCOMING" | "ACTIVE" | "ENDED"): StatusTone {
  switch (state) {
    case "ACTIVE":
      return "success";
    case "UPCOMING":
      return "attention";
    case "ENDED":
    default:
      return "neutral";
  }
}

export function flashItemStatusTone(status?: string | null): StatusTone {
  switch ((status ?? "").trim().toUpperCase()) {
    case "APPROVED":
      return "success";
    case "PENDING":
      return "attention";
    case "REJECTED":
      return "critical";
    default:
      return "neutral";
  }
}

export function approvalStatusTone(status?: string | null): StatusTone {
  switch ((status ?? "").trim().toUpperCase()) {
    case "PENDING":
    case "PENDING_APPROVAL":
      return "attention";
    case "APPROVED":
    case "DONE":
      return "success";
    case "MANUAL_REVIEW":
      return "warning";
    case "REJECTED":
    case "FAILED":
      return "critical";
    default:
      return "neutral";
  }
}

export function refundStatusTone(status?: string | null): StatusTone {
  switch ((status ?? "").trim().toUpperCase()) {
    case "PENDING_APPROVAL":
      return "attention";
    case "PENDING":
      return "info";
    case "DONE":
      return "success";
    case "MANUAL_REVIEW":
      return "warning";
    case "REJECTED":
    case "FAILED":
      return "critical";
    default:
      return "neutral";
  }
}

export function adminUserStatusTone(status?: string | null): StatusTone {
  switch ((status ?? "").trim().toUpperCase()) {
    case "ACTIVE":
      return "success";
    case "PENDING_VERIFICATION":
      return "attention";
    case "LOCKED":
      return "critical";
    default:
      return "neutral";
  }
}

export function searchReindexStateTone(
  state: "RUNNING" | "COMPLETED" | "FAILED" | "UNKNOWN"
): StatusTone {
  switch (state) {
    case "RUNNING":
      return "info";
    case "COMPLETED":
      return "success";
    case "FAILED":
      return "critical";
    default:
      return "neutral";
  }
}

export function DetailGrid({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <dl
      className={cn(
        "grid grid-cols-1 gap-x-4 gap-y-3 rounded-lg border border-border bg-muted/35 p-3.5 sm:grid-cols-2",
        className
      )}
    >
      {children}
    </dl>
  );
}

export function DetailItem({
  label,
  children,
  wide = false,
  numeric = false,
  className,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
  numeric?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0 space-y-0.5", wide && "sm:col-span-2", className)}>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "text-[13px] leading-[18px] text-foreground [overflow-wrap:anywhere]",
          numeric && "tabular-nums"
        )}
      >
        {children}
      </dd>
    </div>
  );
}

export interface ConfirmActionDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel?: string;
  confirmVariant?: "default" | "destructive";
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmActionDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel = "Hủy",
  confirmVariant = "destructive",
  busy = false,
  onConfirm,
  onCancel,
}: ConfirmActionDialogProps) {
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy) onCancel();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy} onClick={onCancel}>
            {cancelLabel}
          </AlertDialogCancel>
          <AlertDialogAction
            variant={confirmVariant}
            disabled={busy}
            onClick={(e) => {
              e.preventDefault();
              if (!busy) onConfirm();
            }}
          >
            {busy ? "Đang xử lý..." : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function TablePagination({
  page,
  totalPages,
  summary,
  disabled = false,
  onPageChange,
  prevLabel = "Trang trước",
  nextLabel = "Trang sau",
}: {
  page: number;
  totalPages: number;
  summary: React.ReactNode;
  disabled?: boolean;
  onPageChange: (nextPage: number) => void;
  prevLabel?: string;
  nextLabel?: string;
}) {
  return (
    <>
      <span className="tabular-nums">{summary}</span>
      <div className="flex items-center gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="xs"
          disabled={disabled || page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <ChevronLeft className="size-3.5" aria-hidden="true" />
          <span>{prevLabel}</span>
        </Button>
        <Button
          type="button"
          variant="outline"
          size="xs"
          disabled={disabled || page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          <span>{nextLabel}</span>
          <ChevronRight className="size-3.5" aria-hidden="true" />
        </Button>
      </div>
    </>
  );
}

export function TableLoadingSkeleton({
  rows = 5,
  label = "Đang tải dữ liệu...",
}: {
  rows?: number;
  label?: string;
}) {
  const count = Math.max(1, Math.min(rows, 10));
  return (
    <div role="status" aria-live="polite" className="space-y-3 rounded-xl bg-card p-5 shadow-card">
      <p className="text-[13px] text-muted-foreground">{label}</p>
      <div className="space-y-2.5">
        {Array.from({ length: count }).map((_, idx) => (
          <div key={idx} className="flex items-center gap-3">
            <Skeleton className="h-9 flex-1 rounded-md" />
            <Skeleton className="h-9 w-28 rounded-md" />
            <Skeleton className="hidden h-9 w-24 rounded-md sm:block" />
          </div>
        ))}
      </div>
    </div>
  );
}

const EMPTY_SENTINEL = "__nm_empty__";

export interface FilterSelectOption {
  value: string;
  label: string;
}

/**
 * Radix Select wrapper that safely accepts empty-string `""` values (mapped internally to a sentinel).
 */
export function FilterSelect({
  id,
  value,
  onValueChange,
  options,
  ariaLabel,
  placeholder,
  disabled = false,
  className,
  size = "default",
}: {
  id?: string;
  value: string;
  onValueChange: (next: string) => void;
  options: FilterSelectOption[];
  ariaLabel?: string;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  size?: "sm" | "default";
}) {
  const mappedValue = value === "" ? EMPTY_SENTINEL : value;
  const selectedLabel = options.find(
    (opt) => (opt.value === "" ? EMPTY_SENTINEL : opt.value) === mappedValue
  )?.label;
  return (
    <Select
      value={mappedValue}
      onValueChange={(next) => onValueChange(next === EMPTY_SENTINEL ? "" : next)}
      disabled={disabled}
    >
      <SelectTrigger
        id={id}
        size={size}
        aria-label={ariaLabel}
        className={cn("w-full bg-card", className)}
      >
        <SelectValue placeholder={placeholder}>{selectedLabel}</SelectValue>
      </SelectTrigger>
      <SelectContent>
        {options.map((opt) => (
          <SelectItem
            key={opt.value === "" ? EMPTY_SENTINEL : opt.value}
            value={opt.value === "" ? EMPTY_SENTINEL : opt.value}
          >
            {opt.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

export function formatPageCountSummary(
  page: number,
  totalPages: number,
  total: number,
  unit: string
): string {
  return `Trang ${formatInt(page)} / ${formatInt(totalPages)} (${formatInt(total)} ${unit})`;
}
