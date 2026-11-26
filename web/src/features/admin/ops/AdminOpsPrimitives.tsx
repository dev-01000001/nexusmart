"use client";

import React from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
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
import { formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";

export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  return target.isContentEditable;
}

export function hasOpenRadixDialog(): boolean {
  if (typeof document === "undefined") return false;
  return Boolean(document.querySelector('[role="dialog"], [role="alertdialog"]'));
}

export function AdminDetailGrid({
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

export function AdminDetailField({
  label,
  children,
  wide = false,
  mono = false,
  className,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
  mono?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0 space-y-0.5", wide && "sm:col-span-2", className)}>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "text-[13px] leading-[18px] text-foreground [overflow-wrap:anywhere]",
          mono && "tabular-nums"
        )}
      >
        {children}
      </dd>
    </div>
  );
}

export interface AdminConfirmActionDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel?: string;
  confirmVariant?: "default" | "destructive";
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  children?: React.ReactNode;
}

export function AdminConfirmActionDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel = "Hủy",
  confirmVariant = "destructive",
  busy = false,
  onConfirm,
  onCancel,
  children,
}: AdminConfirmActionDialogProps) {
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
        {children ? <div className="text-[13px] text-foreground">{children}</div> : null}
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

export function AdminKbdHint({ label = "chuyển hồ sơ" }: { label?: string }) {
  return (
    <span
      className="hidden items-center gap-1 text-xs text-muted-foreground md:inline-flex"
      aria-label={`Phím tắt ${label}`}
    >
      <kbd className="rounded border border-border bg-card px-1.5 py-0.5 text-[11px] font-medium text-foreground shadow-card">
        J
      </kbd>
      <kbd className="rounded border border-border bg-card px-1.5 py-0.5 text-[11px] font-medium text-foreground shadow-card">
        K
      </kbd>
      <span>{label}</span>
    </span>
  );
}

export function AdminPaginationBar({
  page,
  totalPages,
  totalItems,
  itemLabel,
  disabled = false,
  onPageChange,
}: {
  page: number;
  totalPages: number;
  totalItems: number;
  itemLabel: string;
  disabled?: boolean;
  onPageChange: (nextPage: number) => void;
}) {
  return (
    <>
      <span className="tabular-nums">
        Trang {formatInt(page)} / {formatInt(totalPages)} ({formatInt(totalItems)} {itemLabel})
      </span>
      <div className="flex items-center gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="xs"
          disabled={disabled || page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <ChevronLeft className="size-3.5" aria-hidden="true" />
          <span>Trang trước</span>
        </Button>
        <Button
          type="button"
          variant="outline"
          size="xs"
          disabled={disabled || page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          <span>Trang sau</span>
          <ChevronRight className="size-3.5" aria-hidden="true" />
        </Button>
      </div>
    </>
  );
}
