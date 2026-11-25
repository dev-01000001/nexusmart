import React from "react";
import Link from "next/link";
import styles from "@/features/site/site.module.css";

/**
 * Storefront-scoped 404 rendered inside SiteShell's `<main>` (avoids nesting root NotFoundView's
 * `<header>` and `<main>` when a storefront product/page calls `notFound()`).
 */
export default function SiteNotFound() {
  return (
    <section className={styles.siteSection} aria-labelledby="site-not-found-title">
      <div className={styles.emptyPanel}>
        <p className="type-poster" aria-hidden="true">
          404
        </p>
        <h1 id="site-not-found-title" className={styles.sectionTitle}>
          Trang này không tồn tại.
        </h1>
        <div className={styles.heroActions}>
          <Link href="/" className={styles.btnPrimary}>
            Về trang chủ
          </Link>
          <Link href="/san-pham" className={styles.btnSecondary}>
            Xem sản phẩm
          </Link>
        </div>
      </div>
    </section>
  );
}
