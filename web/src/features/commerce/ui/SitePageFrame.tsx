import React from "react";
import { cn } from "@/lib/utils";

export interface SitePageFrameProps {
  children: React.ReactNode;
  className?: string;
  narrow?: boolean;
}

/**
 * Shared container for storefront commerce pages (cart, checkout, payment waiting, flash sale, voucher).
 * Inherits `--site-max-w` from `SiteShell` and keeps consistent responsive padding across all 8 templates.
 */
export function SitePageFrame({ children, className, narrow = false }: SitePageFrameProps) {
  return (
    <div
      className={cn(
        "mx-auto w-full min-w-0 px-[clamp(1rem,2.2vw,2rem)] py-6 md:py-8",
        narrow ? "max-w-[760px]" : "max-w-[var(--site-max-w,1260px)]",
        className
      )}
    >
      {children}
    </div>
  );
}

export interface SitePageHeaderProps {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  action?: React.ReactNode;
  centered?: boolean;
  className?: string;
}

export function SitePageHeader({
  title,
  subtitle,
  action,
  centered = false,
  className,
}: SitePageHeaderProps) {
  return (
    <header
      className={cn(
        "mb-6 flex flex-col gap-3 border-b border-border pb-5 sm:flex-row sm:items-end sm:justify-between",
        centered && "items-center text-center sm:flex-col sm:items-center",
        className
      )}
    >
      <div className="min-w-0 space-y-1">
        <h1 className="text-2xl leading-tight font-semibold tracking-tight text-foreground sm:text-[28px]">
          {title}
        </h1>
        {subtitle ? <p className="text-sm leading-5 text-muted-foreground">{subtitle}</p> : null}
      </div>
      {action ? <div className="flex shrink-0 flex-wrap items-center gap-2">{action}</div> : null}
    </header>
  );
}
