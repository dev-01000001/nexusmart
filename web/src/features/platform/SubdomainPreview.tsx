"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import { checkSubdomain, suggestSubdomain } from "./subdomain";
import styles from "./SubdomainPreview.module.css";

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
    <div className={styles.wrap}>
      <div className={styles.controlRow}>
        <label htmlFor="platform-shop-name-input" className="sr-only">
          Tên thương hiệu của bạn
        </label>

        <div className={styles.inputGroup}>
          <input
            id="platform-shop-name-input"
            type="text"
            value={shopName}
            onChange={(e) => setShopName(e.target.value)}
            placeholder="ten-thuong-hieu"
            maxLength={64}
            autoComplete="organization"
            className={styles.input}
            aria-describedby="platform-subdomain-status"
          />
          <span className={styles.domainSuffix} aria-hidden="true">
            .{rootDomain}
          </span>
        </div>

        <Link href={ctaHref} className={styles.cta}>
          Mở gian hàng
        </Link>
      </div>

      <p
        id="platform-subdomain-status"
        aria-live="polite"
        className={`${styles.status} ${
          status === "ok"
            ? styles.statusOk
            : status === "reserved" || status === "short"
              ? styles.statusWarn
              : styles.statusEmpty
        }`}
      >
        {statusText}
      </p>
    </div>
  );
}
