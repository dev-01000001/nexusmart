import * as React from "react";
import { cn } from "@/lib/utils";

export interface TimelineItem {
  id: string;
  at: string;
  title: string;
  description?: string;
}

export interface TimelineProps {
  items: TimelineItem[];
  className?: string;
}

export function Timeline({ items, className }: TimelineProps) {
  if (items.length === 0) return null;

  return (
    <ol className={cn("relative flex flex-col gap-4 pl-5", className)}>
      <span aria-hidden="true" className="absolute top-2 bottom-2 left-[7px] w-px bg-border" />
      {items.map((item, idx) => (
        <li key={item.id} className="relative flex flex-col gap-0.5">
          <span
            aria-hidden="true"
            className={cn(
              "absolute top-1.5 -left-5 flex size-3.5 items-center justify-center rounded-full border-2 border-card",
              idx === 0 ? "bg-brand" : "bg-input"
            )}
          />
          <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
            <span className="text-sm font-medium text-foreground">{item.title}</span>
            <time className="text-xs text-muted-foreground tabular-nums">{item.at}</time>
          </div>
          {item.description ? (
            <p className="text-[13px] leading-[18px] text-muted-foreground">{item.description}</p>
          ) : null}
        </li>
      ))}
    </ol>
  );
}
