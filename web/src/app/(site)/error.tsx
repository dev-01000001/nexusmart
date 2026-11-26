"use client";

import React from "react";
import { Button } from "@/components/Button";
import styles from "./error.module.css";

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
    <main className={styles.page}>
      <section className={styles.panel} role="alert" aria-labelledby="site-error-title">
        <h1 id="site-error-title" className={`${styles.title} type-normal`}>
          Hệ thống đang tạm bận.
        </h1>
        <p className={styles.message}>
          Cửa hàng tạm thời chưa thể tải nội dung lúc này. Bạn vui lòng thử lại sau ít phút.
        </p>
        <div className={styles.actions}>
          <Button variant="primary" size="md" shape="square" onClick={() => reset()}>
            Thử lại
          </Button>
        </div>
      </section>
    </main>
  );
}
