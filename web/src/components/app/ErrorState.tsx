import * as React from "react";
import Link from "next/link";
import { AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface ErrorStateProps {
  title?: string;
  description: string;
  onRetry?: () => void;
  retryHref?: string;
  className?: string;
}

export function ErrorState({
  title = "Chưa tải được dữ liệu",
  description,
  onRetry,
  retryHref,
  className,
}: ErrorStateProps) {
  return (
    <div
      role="alert"
      className={cn(
        "flex flex-col items-center justify-center rounded-xl bg-card px-6 py-10 text-center shadow-card",
        className
      )}
    >
      <div className="mb-3 flex size-10 items-center justify-center rounded-full bg-badge-critical-bg text-badge-critical-ink">
        <AlertCircle className="size-5" aria-hidden="true" />
      </div>
      <h3 className="text-sm font-semibold text-foreground">{title}</h3>
      <p className="mt-1.5 max-w-md text-[13px] leading-[18px] text-muted-foreground">
        {description}
      </p>
      {onRetry ? (
        <div className="mt-4">
          <Button type="button" variant="outline" size="sm" onClick={onRetry}>
            Thử lại
          </Button>
        </div>
      ) : retryHref ? (
        <div className="mt-4">
          <Button variant="outline" size="sm" asChild>
            <Link href={retryHref}>Thử lại</Link>
          </Button>
        </div>
      ) : null}
    </div>
  );
}
