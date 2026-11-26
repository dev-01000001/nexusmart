"use client";

import React from "react";
import { useRouter } from "next/navigation";
import { IconAlert, IconRefresh } from "@/components/Icons";

export function DirectoryErrorState() {
  const router = useRouter();
  return (
    <div
      className="flex flex-wrap items-center justify-between gap-x-[var(--space-5)] gap-y-[var(--space-3)] rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-[var(--space-5)] py-[var(--space-4)]"
      role="alert"
    >
      <div className="inline-flex items-center gap-[var(--space-3)]">
        <IconAlert size={18} className="flex-none text-[var(--home-accent)]" />
        <p className="text-[length:var(--home-fs-small)] font-[450] text-[var(--home-ink)]">
          Chưa thể tải danh bạ lúc này.
        </p>
      </div>
      <button
        type="button"
        className="inline-flex min-h-[var(--home-btn-header-h)] items-center gap-[var(--space-2)] rounded-[var(--radius-pill)] [border:1px_solid_var(--home-border-strong)] bg-[var(--home-glass-strong)] px-[var(--space-4)] py-0 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink)] [transition:background-color_var(--t-hover)_var(--ease-out)] hover:bg-[var(--home-border-strong)]"
        onClick={() => router.refresh()}
      >
        <IconRefresh size={14} />
        <span>Tải lại</span>
      </button>
    </div>
  );
}
