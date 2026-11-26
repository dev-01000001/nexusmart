import React from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export interface SiteLoadingSkeletonProps {
  title?: string;
  rows?: number;
  variant?: "list" | "grid" | "detail";
  className?: string;
}

export function SiteLoadingSkeleton({
  title = "Đang tải dữ liệu...",
  rows = 3,
  variant = "list",
  className,
}: SiteLoadingSkeletonProps) {
  const count = Math.max(1, Math.min(8, rows));

  if (variant === "grid") {
    return (
      <div role="status" aria-live="polite" aria-busy="true" className={cn("space-y-4", className)}>
        <span className="sr-only">{title}</span>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: count }).map((_, idx) => (
            <Card key={idx} className="overflow-hidden border border-border py-0 shadow-none">
              <Skeleton className="aspect-[3/4] w-full rounded-none" />
              <CardContent className="space-y-2.5 py-4">
                <Skeleton className="h-4 w-3/4" />
                <Skeleton className="h-4 w-1/2" />
                <Skeleton className="h-9 w-full" />
              </CardContent>
            </Card>
          ))}
        </div>
      </div>
    );
  }

  if (variant === "detail") {
    return (
      <div
        role="status"
        aria-live="polite"
        aria-busy="true"
        className={cn(
          "grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.95fr)]",
          className
        )}
      >
        <span className="sr-only">{title}</span>
        <div className="space-y-4">
          {Array.from({ length: Math.min(count, 4) }).map((_, idx) => (
            <Card key={idx} className="border border-border shadow-none">
              <CardContent className="space-y-3 py-2">
                <Skeleton className="h-5 w-44" />
                <Skeleton className="h-10 w-full" />
                <Skeleton className="h-4 w-2/3" />
              </CardContent>
            </Card>
          ))}
        </div>
        <Card className="border border-border shadow-none">
          <CardContent className="space-y-3 py-2">
            <Skeleton className="h-5 w-36" />
            <Skeleton className="h-14 w-full" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-4/5" />
            <Skeleton className="h-11 w-full" />
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <Card
      role="status"
      aria-live="polite"
      aria-busy="true"
      className={cn("border border-border shadow-none", className)}
    >
      <span className="sr-only">{title}</span>
      <CardContent className="space-y-4 py-2">
        {Array.from({ length: count }).map((_, idx) => (
          <div key={idx} className="flex items-center gap-4">
            <Skeleton className="size-16 shrink-0 rounded-lg" />
            <div className="min-w-0 flex-1 space-y-2">
              <Skeleton className="h-4 w-3/4 max-w-md" />
              <Skeleton className="h-3.5 w-1/2 max-w-xs" />
            </div>
            <Skeleton className="h-5 w-20 shrink-0" />
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
