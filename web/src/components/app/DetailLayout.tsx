import * as React from "react";
import { cn } from "@/lib/utils";

export interface DetailLayoutProps {
  main: React.ReactNode;
  aside: React.ReactNode;
  className?: string;
}

export function DetailLayout({ main, aside, className }: DetailLayoutProps) {
  return (
    <div
      className={cn(
        "mx-auto grid w-full max-w-[1000px] grid-cols-1 gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)] lg:items-start",
        className
      )}
    >
      <div className="flex min-w-0 flex-col gap-4">{main}</div>
      <aside className="flex min-w-0 flex-col gap-4">{aside}</aside>
    </div>
  );
}
