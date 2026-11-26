"use client";

import * as React from "react";
import { AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface SaveBarProps {
  visible: boolean;
  onDiscard: () => void;
  onSave: () => void;
  saving?: boolean;
  message?: string;
  className?: string;
}

export function SaveBar({
  visible,
  onDiscard,
  onSave,
  saving = false,
  message = "Thay đổi chưa lưu",
  className,
}: SaveBarProps) {
  if (!visible) return null;

  return (
    <div
      role="region"
      aria-label="Thanh lưu thay đổi"
      className={cn(
        "sticky top-2 z-30 flex flex-wrap items-center justify-between gap-3 rounded-xl bg-topbar px-4 py-2.5 text-sm text-white shadow-overlay",
        className
      )}
    >
      <div className="flex min-w-0 items-center gap-2 text-topbar-foreground">
        <AlertCircle className="size-4 shrink-0 text-white" aria-hidden="true" />
        <span className="truncate font-medium text-white">{message}</span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={saving}
          onClick={onDiscard}
          className="text-topbar-foreground hover:bg-white/10 hover:text-white"
        >
          Huỷ
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={saving}
          onClick={onSave}
          className="bg-white text-[#152229] shadow-none hover:bg-white/90"
        >
          {saving ? "Đang lưu..." : "Lưu"}
        </Button>
      </div>
    </div>
  );
}
