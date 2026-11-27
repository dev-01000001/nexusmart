"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import { checkSubdomain, suggestSubdomain } from "./subdomain";

export interface SubdomainPreviewProps {
  rootDomain: string;
  value?: string;
  onValueChange?: (next: string) => void;
}

const CHECK_COPY: Record<Exclude<ReturnType<typeof checkSubdomain>, "empty">, string> = {
  short: "Cần ít nhất 3 ký tự chữ thường hoặc số.",
  reserved: "Tên hệ thống giữ riêng, thử tên khác.",
  ok: "Địa chỉ hợp lệ.",
};

export function SubdomainPreview({ rootDomain, value, onValueChange }: SubdomainPreviewProps) {
  const [internalValue, setInternalValue] = React.useState("");
  const shopName = value !== undefined ? value : internalValue;
  const setShopName = onValueChange ?? setInternalValue;

  const suggested = useMemo(() => suggestSubdomain(shopName), [shopName]);
  const status = useMemo(() => checkSubdomain(suggested), [suggested]);

  const ctaHref =
    status === "ok" && shopName.trim()
      ? `/dang-ky?role=seller&ten=${encodeURIComponent(shopName.trim())}`
      : "/dang-ky?role=seller";

  const statusText =
    status === "empty"
      ? ""
      : status === "ok"
        ? `${suggested}.${rootDomain} — ${CHECK_COPY.ok}`
        : CHECK_COPY[status];

  return (
    <div className="flex w-full flex-col gap-2">
      <div className="flex w-full flex-wrap items-stretch gap-3">
        <label htmlFor="platform-shop-name-input" className="sr-only">
          Tên thương hiệu của bạn
        </label>

        <div className="flex min-h-[var(--home-btn-mid-h)] min-w-0 flex-[1_1_260px] items-center gap-1 rounded-full [border:1px_solid_var(--home-border-strong)] bg-[var(--home-glass)] px-5 py-0 [transition:border-color_200ms_cubic-bezier(0.22,1,0.36,1),background-color_200ms_cubic-bezier(0.22,1,0.36,1)] focus-within:border-[color:var(--home-accent)] focus-within:bg-[var(--home-glass-strong)] [@media(max-width:520px)]:flex-[1_1_100%] [@media(max-width:520px)]:px-4">
          <input
            id="platform-shop-name-input"
            type="text"
            value={shopName}
            onChange={(e) => setShopName(e.target.value)}
            placeholder="ten-thuong-hieu"
            maxLength={64}
            autoComplete="organization"
            className="h-[var(--home-btn-mid-h)] min-w-0 flex-[1_1_90px] border-0 bg-transparent p-0 font-[family-name:var(--home-font)] text-[length:var(--home-fs-body)] font-normal text-[var(--home-ink)] outline-none placeholder:font-normal placeholder:text-[var(--home-ink-dim)]"
            aria-describedby="platform-subdomain-status"
          />
          <span
            className="flex-none select-none whitespace-nowrap text-[length:var(--home-fs-small)] font-normal text-[var(--home-ink-2)] [@media(max-width:520px)]:text-[length:var(--home-fs-caption)]"
            aria-hidden="true"
          >
            .{rootDomain}
          </span>
        </div>

        <Link
          href={ctaHref}
          className="inline-flex min-h-[var(--home-btn-mid-h)] flex-[0_0_auto] items-center justify-center whitespace-nowrap rounded-full bg-[var(--home-ink)] px-6 py-0 font-[family-name:var(--home-font)] text-[length:var(--home-fs-body)] font-[550] text-[var(--home-bg)] [transition:background-color_200ms_cubic-bezier(0.22,1,0.36,1),transform_140ms_cubic-bezier(0.22,1,0.36,1)] hover:bg-[#eff3f5] active:scale-[0.98] [@media(max-width:520px)]:w-full"
        >
          Mở gian hàng
        </Link>
      </div>

      <p
        id="platform-subdomain-status"
        aria-live="polite"
        className={`min-h-5 px-2 text-[length:var(--home-fs-small)] leading-[1.45] [transition:opacity_200ms_cubic-bezier(0.22,1,0.36,1)] [word-break:break-word] ${
          status === "ok"
            ? "font-medium text-[var(--home-accent)]"
            : status === "reserved" || status === "short"
              ? "font-medium text-[color-mix(in_srgb,#9e6b13_45%,var(--home-ink))]"
              : "opacity-0"
        }`}
      >
        {statusText}
      </p>
    </div>
  );
}
