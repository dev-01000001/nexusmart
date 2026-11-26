import * as React from "react";
import { cn } from "@/lib/utils";

export interface MetricStripItem {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
}

export interface MetricStripProps {
  items: MetricStripItem[];
  className?: string;
}

export function MetricStrip({ items, className }: MetricStripProps) {
  if (items.length === 0) return null;

  return (
    <div
      className={cn(
        "grid grid-cols-1 divide-y divide-border overflow-hidden rounded-xl bg-card shadow-card sm:grid-cols-2 sm:divide-y-0 md:grid-flow-col md:auto-cols-fr md:divide-x",
        className
      )}
    >
      {items.map((item, idx) => (
        <div
          key={`${item.label}-${idx}`}
          className="flex min-w-0 flex-col gap-1 px-4 py-3.5 md:px-5"
        >
          <span className="truncate text-[13px] leading-[18px] font-medium text-muted-foreground">
            {item.label}
          </span>
          <div className="flex flex-wrap items-baseline gap-2">
            <span className="text-lg leading-6 font-semibold text-foreground tabular-nums">
              {item.value}
            </span>
            {item.hint ? <span className="text-xs text-muted-foreground">{item.hint}</span> : null}
          </div>
        </div>
      ))}
    </div>
  );
}
