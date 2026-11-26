"use client";

import * as React from "react";
import Link from "next/link";
import { UiScope } from "@/components/ui/ui-scope";
import { cn } from "@/lib/utils";

export interface AuthSplitProps {
  variant: "standalone" | "embedded";
  brand: {
    name: string;
    href: string;
    logo?: React.ReactNode;
  };
  title: string;
  subtitle?: React.ReactNode;
  aside?: {
    imageSrc: string;
    caption: string;
  };
  footer?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}

export function AuthSplit({
  variant,
  brand,
  title,
  subtitle,
  aside,
  footer,
  children,
  className,
}: AuthSplitProps) {
  const cardContent = (
    <div
      className={cn(
        "grid w-full grid-cols-1 overflow-hidden bg-card text-card-foreground",
        aside ? "lg:grid-cols-2" : "",
        variant === "standalone"
          ? "min-h-svh max-w-[1180px] lg:min-h-[680px] lg:rounded-2xl lg:shadow-overlay"
          : "mx-auto max-w-[1000px] rounded-xl border border-border",
        className
      )}
    >
      <div className="flex flex-col justify-between p-6 sm:p-8 lg:p-10">
        <div>
          <Link
            href={brand.href}
            className="inline-flex items-center gap-2 text-base font-semibold tracking-tight text-foreground"
          >
            {brand.logo ? <span className="shrink-0">{brand.logo}</span> : null}
            <span className="truncate">{brand.name}</span>
          </Link>

          <div className="mx-auto mt-8 w-full max-w-[420px] lg:mt-10">
            <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground sm:text-2xl sm:leading-8">
              {title}
            </h1>
            {subtitle ? (
              <div className="mt-1.5 text-sm leading-5 text-muted-foreground">{subtitle}</div>
            ) : null}

            <div className="mt-6">{children}</div>
          </div>
        </div>

        {footer ? (
          <div className="mx-auto mt-8 w-full max-w-[420px] border-t border-border pt-4 text-xs text-muted-foreground">
            {footer}
          </div>
        ) : null}
      </div>

      {aside ? (
        <div className="hidden p-3 lg:block">
          <div className="relative flex h-full min-h-[540px] w-full flex-col justify-end overflow-hidden rounded-xl bg-topbar">
            <img
              src={aside.imageSrc}
              alt=""
              className="absolute inset-0 h-full w-full object-cover"
            />
            <div className="relative z-10 m-4 rounded-xl bg-[#152229]/85 p-5 text-white">
              <p className="text-base leading-6 font-medium text-white">{aside.caption}</p>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );

  if (variant === "standalone") {
    return (
      <UiScope className="flex min-h-svh w-full items-center justify-center bg-card text-sm text-foreground lg:bg-topbar lg:p-6">
        {cardContent}
      </UiScope>
    );
  }

  return cardContent;
}
