"use client";

import React from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import { useSellerContext } from "./SellerShell";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/page.module.css";

/**
 * Compact storefront live status bar on Seller Overview.
 * Keeps published state quiet (1 line) and highlights unpublished/suspended states with 1 direct CTA.
 */
export function SellerSiteStatus() {
  const { site } = useSellerContext();

  if (site.state === "published") {
    return (
      <section className={styles.siteBand} aria-labelledby="site-status-heading">
        <div className={styles.siteBandInline}>
          <span className={`${common.pill} ${common.pillPos}`}>Đang mở bán</span>
          <h2 id="site-status-heading" className={styles.siteBandHost}>
            {site.host}
          </h2>
        </div>
        <div className={styles.siteBandActions}>
          <Link href="/seller/site" className={styles.siteBandSecondary}>
            Chỉnh sửa site
          </Link>
          <a
            href={site.url}
            target="_blank"
            rel="noopener noreferrer"
            className={styles.siteBandPrimary}
          >
            <span>Xem site</span>
            <IconArrowUpRight size={14} />
          </a>
        </div>
      </section>
    );
  }

  if (site.state === "suspended") {
    return (
      <section
        className={`${styles.siteBand} ${styles.siteBandDanger}`}
        aria-labelledby="site-status-heading"
      >
        <div className={styles.siteBandInline}>
          <span className={`${common.pill} ${common.pillNeg}`}>Tạm khóa</span>
          <h2 id="site-status-heading" className={styles.siteBandHost}>
            {site.host || "Site gian hàng"}
          </h2>
          {site.suspendedReason && (
            <span className={styles.siteBandNote}>{site.suspendedReason}</span>
          )}
        </div>
        <div className={styles.siteBandActions}>
          <Link href="/seller/site" className={styles.siteBandPrimary}>
            Xem tình trạng
          </Link>
        </div>
      </section>
    );
  }

  const isDraft = site.state === "draft";
  return (
    <section
      className={`${styles.siteBand} ${styles.siteBandAlert}`}
      aria-labelledby="site-status-heading"
    >
      <div className={styles.siteBandInline}>
        <span className={`${common.pill} ${common.pillWarn}`}>
          {isDraft ? "Bản nháp" : "Chưa có site"}
        </span>
        <h2 id="site-status-heading" className={styles.siteBandHost}>
          {isDraft ? site.host || "Site chưa xuất bản" : "Chưa mở bán công khai"}
        </h2>
        <span className={styles.siteBandNote}>Xuất bản site để nhận đơn hàng từ người mua.</span>
      </div>
      <div className={styles.siteBandActions}>
        <Link href="/seller/site" className={styles.siteBandPrimary}>
          {isDraft ? "Xuất bản site" : "Thiết lập site"}
        </Link>
      </div>
    </section>
  );
}
