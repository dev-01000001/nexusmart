import React from "react";
import Link from "next/link";
import styles from "./PlatformFooter.module.css";

export function PlatformFooter() {
  return (
    <footer className={styles.footer}>
      <div className={styles.topRow}>
        <div className={styles.metaCol}>
          <span className={styles.metaText}>Nền tảng cửa hàng độc lập</span>
          <a href="mailto:hotro@nexusmart.vn" className={styles.metaLink}>
            hotro@nexusmart.vn
          </a>
        </div>

        <nav className={styles.navRow} aria-label="Liên kết chân trang">
          <Link href="/#danh-ba-gian-hang" className={styles.navLink}>
            Cửa hàng
          </Link>
          <Link href="/ve-nexusmart" className={styles.navLink}>
            Về NexusMart
          </Link>
          <Link href="/tro-giup" className={styles.navLink}>
            Trợ giúp
          </Link>
          <Link href="/seller" className={styles.navLink}>
            Kênh người bán
          </Link>
        </nav>

        <div className={styles.rightCol}>
          <Link href="/dang-nhap" className={styles.navLink}>
            Đăng nhập
          </Link>
          <span className={`${styles.year} num`}>© 2026</span>
        </div>
      </div>

      <div className={styles.wordmarkWrap} aria-hidden="true">
        <svg
          viewBox="0 0 1000 156"
          className={styles.wordmarkSvg}
          preserveAspectRatio="xMidYMid meet"
        >
          <text
            x="0"
            y="132"
            textLength="1000"
            lengthAdjust="spacingAndGlyphs"
            className={`${styles.wordmarkText} type-wide`}
          >
            NexusMart
          </text>
        </svg>
      </div>
    </footer>
  );
}
