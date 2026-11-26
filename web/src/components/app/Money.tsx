import * as React from "react";
import { formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface MoneyProps {
  vnd: number;
  compareAtVnd?: number;
  size?: "sm" | "md" | "lg";
  className?: string;
}

const SIZE_CLASSES: Record<"sm" | "md" | "lg", string> = {
  sm: "text-[13px] leading-[18px]",
  md: "text-sm leading-5",
  lg: "text-lg leading-7 font-semibold",
};

export function Money({ vnd, compareAtVnd, size = "md", className }: MoneyProps) {
  const showCompare =
    typeof compareAtVnd === "number" && Number.isFinite(compareAtVnd) && compareAtVnd > vnd;

  return (
    <span className={cn("inline-flex flex-wrap items-baseline gap-1.5 tabular-nums", className)}>
      <span className={cn("font-medium text-foreground", SIZE_CLASSES[size])}>
        {formatVnd(vnd)}
      </span>
      {showCompare ? (
        <span className="text-xs text-muted-foreground line-through">
          {formatVnd(compareAtVnd)}
        </span>
      ) : null}
    </span>
  );
}
