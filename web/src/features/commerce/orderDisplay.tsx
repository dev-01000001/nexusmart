import React from "react";
import type { StatusTone } from "@/data/orders";
import { formatDateTimeVi } from "@/lib/format";

export function statusToneClass(tone: StatusTone): string {
  if (tone === "pos") return "bg-badge-success-bg text-badge-success-ink";
  if (tone === "warn") return "bg-badge-warning-bg text-badge-warning-ink";
  if (tone === "neg") return "bg-badge-critical-bg text-badge-critical-ink";
  return "bg-badge-neutral-bg text-badge-neutral-ink";
}

export function renderOrderDateTime(iso: string): React.ReactNode {
  const formatted = formatDateTimeVi(iso);
  const parts = formatted.includes(", ") ? formatted.split(", ") : formatted.split(" · ");
  if (parts.length !== 2) return formatted;
  return (
    <span className="inline-flex flex-wrap items-baseline gap-x-1.5 tabular-nums">
      <span>{parts[0]}</span>
      <span className="text-muted-foreground">{parts[1]}</span>
    </span>
  );
}
