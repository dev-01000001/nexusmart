import React from "react";
import Link from "next/link";
import { NexusMark } from "@/components/Icons";
import styles from "./PlatformFooter.module.css";

export function PlatformFooter() {
  return (
    <footer className={styles.footer}>
      <div className={styles.inner}>
        <div className={styles.topGrid}>
          <div className={styles.brandCol}>
            <Link href="/" className={styles.brandLink} aria-label="NexusMart trang chủ">
              <NexusMark size={28} accentColor="var(--band-dark-accent)" />
              <span className={styles.brandName}>NexusMart</span>
            </Link>
          </div>

          <nav className={styles.columns} aria-label="Liên kết chân trang">
            <div className={styles.col}>
              <p className={styles.colTitle}>NexusMart</p>
              <ul className={styles.colList}>
                <li>
                  <Link href="/ve-nexusmart" className={styles.link}>
                    Về NexusMart
                  </Link>
                </li>
                <li>
                  <Link href="/tro-giup" className={styles.link}>
                    Trợ giúp
                  </Link>
                </li>
              </ul>
            </div>

            <div className={styles.col}>
              <p className={styles.colTitle}>Người bán</p>
              <ul className={styles.colList}>
                <li>
                  <Link href="/dang-ky?role=seller" className={styles.link}>
                    Mở gian hàng
                  </Link>
                </li>
                <li>
                  <Link href="/seller" className={styles.link}>
                    Kênh người bán
                  </Link>
                </li>
                <li>
                  <Link href="/dang-nhap" className={styles.link}>
                    Đăng nhập
                  </Link>
                </li>
              </ul>
            </div>

            <div className={styles.col}>
              <p className={styles.colTitle}>Mua sắm</p>
              <ul className={styles.colList}>
                <li>
                  <Link href="/#danh-ba-gian-hang" className={styles.link}>
                    Các cửa hàng
                  </Link>
                </li>
              </ul>
            </div>

            <div className={styles.col}>
              <p className={styles.colTitle}>Liên hệ</p>
              <ul className={styles.colList}>
                <li>
                  <a href="mailto:hotro@nexusmart.vn" className={styles.link}>
                    hotro@nexusmart.vn
                  </a>
                </li>
              </ul>
            </div>
          </nav>
        </div>

        <div className={styles.bottomRow}>
          <p className={`${styles.copyright} num`}>© 2026 NexusMart · Nền tảng cửa hàng độc lập</p>
        </div>
      </div>
    </footer>
  );
}
