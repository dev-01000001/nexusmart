"use client";

import React from "react";
import { ErrorState } from "@/components/app/ErrorState";
import { UiScope } from "@/components/ui/ui-scope";

interface SiteErrorProps {
  error: Error & { digest?: string };
  reset: () => void;
}

/**
 * Segment error boundary above `s/[host]/layout.tsx` (inside `(site)/layout.tsx`) so transient
 * gateway failures thrown while resolving a storefront host render a recoverable Vietnamese state
 * instead of a false 404. Never exposes raw error messages, stacks, or digests.
 */
export default function SiteError({ reset }: SiteErrorProps) {
  return (
    <UiScope className="flex min-h-svh items-center justify-center bg-background px-4 py-12 text-foreground">
      <main className="w-full max-w-[520px]" aria-labelledby="site-error-title">
        <h1
          id="site-error-title"
          className="mb-4 text-center text-2xl font-semibold tracking-tight text-foreground"
        >
          Hệ thống đang tạm bận.
        </h1>
        <ErrorState
          title="Chưa thể tải nội dung cửa hàng"
          description="Cửa hàng tạm thời chưa thể tải nội dung lúc này. Bạn vui lòng thử lại sau ít phút."
          onRetry={() => reset()}
          className="border border-border shadow-none"
        />
      </main>
    </UiScope>
  );
}
