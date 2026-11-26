"use client";

import * as React from "react";
import Link from "next/link";
import { ArrowLeft, ChevronDown, ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export interface PageHeaderMoreAction {
  label: string;
  href?: string;
  onSelect?: () => void;
  destructive?: boolean;
}

export interface PageHeaderProps {
  title: string;
  backHref?: string;
  badges?: React.ReactNode;
  subtitle?: React.ReactNode;
  secondaryActions?: React.ReactNode;
  moreActions?: PageHeaderMoreAction[];
  pager?: { prevHref?: string; nextHref?: string };
  primaryAction?: React.ReactNode;
  className?: string;
}

export function PageHeader({
  title,
  backHref,
  badges,
  subtitle,
  secondaryActions,
  moreActions,
  pager,
  primaryAction,
  className,
}: PageHeaderProps) {
  const hasMoreDesktop = Boolean(moreActions && moreActions.length > 0);
  const hasMobileOverflow = Boolean(
    hasMoreDesktop || secondaryActions || pager?.prevHref || pager?.nextHref
  );

  return (
    <header
      className={cn("flex flex-col gap-3 md:flex-row md:items-start md:justify-between", className)}
    >
      <div className="flex min-w-0 items-start gap-2">
        {backHref ? (
          <Button
            variant="ghost"
            size="icon-sm"
            asChild
            className="mt-0.5 shrink-0 text-muted-foreground hover:text-foreground"
          >
            <Link href={backHref} aria-label="Quay lại">
              <ArrowLeft className="size-4" aria-hidden="true" />
            </Link>
          </Button>
        ) : null}

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="min-w-0 truncate text-xl leading-7 font-semibold tracking-tight text-foreground">
              {title}
            </h1>
            {badges ? <div className="flex flex-wrap items-center gap-1.5">{badges}</div> : null}
          </div>
          {subtitle ? (
            <div className="mt-0.5 text-[13px] leading-[18px] text-muted-foreground">
              {subtitle}
            </div>
          ) : null}
        </div>
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-2">
        {/* Desktop secondary + more + pager */}
        <div className="hidden items-center gap-2 md:flex">
          {secondaryActions}
          {hasMoreDesktop ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button type="button" variant="secondary" size="sm">
                  <span>Thêm thao tác</span>
                  <ChevronDown className="size-4" aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {moreActions?.map((action) =>
                  action.href ? (
                    <DropdownMenuItem
                      key={action.label}
                      asChild
                      variant={action.destructive ? "destructive" : "default"}
                    >
                      <Link href={action.href}>{action.label}</Link>
                    </DropdownMenuItem>
                  ) : (
                    <DropdownMenuItem
                      key={action.label}
                      onSelect={action.onSelect}
                      variant={action.destructive ? "destructive" : "default"}
                    >
                      {action.label}
                    </DropdownMenuItem>
                  )
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
          {pager ? (
            <div className="inline-flex items-center overflow-hidden rounded-md border border-border bg-card shadow-card">
              {pager.prevHref ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  asChild
                  className="rounded-none border-r border-border"
                >
                  <Link href={pager.prevHref} aria-label="Bản ghi trước">
                    <ChevronLeft className="size-4" aria-hidden="true" />
                  </Link>
                </Button>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  disabled
                  aria-label="Không có bản ghi trước"
                  className="rounded-none border-r border-border"
                >
                  <ChevronLeft className="size-4" aria-hidden="true" />
                </Button>
              )}
              {pager.nextHref ? (
                <Button variant="ghost" size="icon-sm" asChild className="rounded-none">
                  <Link href={pager.nextHref} aria-label="Bản ghi tiếp theo">
                    <ChevronRight className="size-4" aria-hidden="true" />
                  </Link>
                </Button>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  disabled
                  aria-label="Không có bản ghi tiếp theo"
                  className="rounded-none"
                >
                  <ChevronRight className="size-4" aria-hidden="true" />
                </Button>
              )}
            </div>
          ) : null}
        </div>

        {/* Mobile overflow menu (< 768px): keeps primaryAction outside */}
        {hasMobileOverflow ? (
          <div className="md:hidden">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button type="button" variant="secondary" size="sm">
                  <span>Thêm thao tác</span>
                  <ChevronDown className="size-4" aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-48">
                {secondaryActions ? (
                  <div className="flex flex-col gap-1 p-1">{secondaryActions}</div>
                ) : null}
                {secondaryActions && (hasMoreDesktop || pager) ? <DropdownMenuSeparator /> : null}
                {moreActions?.map((action) =>
                  action.href ? (
                    <DropdownMenuItem
                      key={action.label}
                      asChild
                      variant={action.destructive ? "destructive" : "default"}
                    >
                      <Link href={action.href}>{action.label}</Link>
                    </DropdownMenuItem>
                  ) : (
                    <DropdownMenuItem
                      key={action.label}
                      onSelect={action.onSelect}
                      variant={action.destructive ? "destructive" : "default"}
                    >
                      {action.label}
                    </DropdownMenuItem>
                  )
                )}
                {pager?.prevHref || pager?.nextHref ? (
                  <>
                    {hasMoreDesktop ? <DropdownMenuSeparator /> : null}
                    {pager.prevHref ? (
                      <DropdownMenuItem asChild>
                        <Link href={pager.prevHref}>Bản ghi trước</Link>
                      </DropdownMenuItem>
                    ) : null}
                    {pager.nextHref ? (
                      <DropdownMenuItem asChild>
                        <Link href={pager.nextHref}>Bản ghi tiếp theo</Link>
                      </DropdownMenuItem>
                    ) : null}
                  </>
                ) : null}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ) : null}

        {primaryAction}
      </div>
    </header>
  );
}
