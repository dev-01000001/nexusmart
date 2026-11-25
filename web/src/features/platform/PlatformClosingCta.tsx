"use client";

import React, { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { checkSubdomain, suggestSubdomain } from "./subdomain";
import styles from "./PlatformClosingCta.module.css";

export interface PlatformClosingCtaProps {
  rootDomain: string;
}

export function PlatformClosingCta({ rootDomain }: PlatformClosingCtaProps) {
  const router = useRouter();
  const [brandName, setBrandName] = useState("");

  const suggested = useMemo(() => suggestSubdomain(brandName), [brandName]);
  const status = useMemo(() => checkSubdomain(suggested), [suggested]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = brandName.trim();
    const href =
      status === "ok" && clean
        ? `/dang-ky?role=seller&ten=${encodeURIComponent(clean)}`
        : "/dang-ky?role=seller";
    router.push(href);
  };

  return (
    <section className={styles.band} aria-label="Bắt đầu mở gian hàng">
      <form className={styles.form} onSubmit={handleSubmit}>
        <div className={`${styles.sentence} type-wide`}>
          <span className={styles.word}>Mở</span>
          <label htmlFor="closing-cta-brand" className="sr-only">
            Tên thương hiệu
          </label>
          <input
            id="closing-cta-brand"
            type="text"
            value={brandName}
            onChange={(e) => setBrandName(e.target.value)}
            placeholder="tên thương hiệu"
            maxLength={64}
            className={`${styles.blankInput} type-wide`}
          />
          <button type="submit" className={`${styles.submitWord} type-wide`}>
            hôm nay.
          </button>
        </div>

        <p className={styles.liveNote} aria-live="polite">
          {status === "ok" ? (
            <>
              <span className={styles.liveSlug}>{suggested}</span>
              <span className={styles.liveDomain}>.{rootDomain}</span>
            </>
          ) : status === "short" ? (
            <span className={styles.liveWarn}>Cần ít nhất 3 ký tự chữ thường hoặc số.</span>
          ) : status === "reserved" ? (
            <span className={styles.liveWarn}>Tên hệ thống giữ riêng, thử tên khác.</span>
          ) : (
            <span className={styles.livePlaceholder}>&nbsp;</span>
          )}
        </p>
      </form>
    </section>
  );
}
