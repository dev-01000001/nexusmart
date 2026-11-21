"use client";

import React from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import { useSellerContext } from "./SellerShell";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/page.module.css";

/**
 * Overview band telling the seller whether buyers can reach the shop's site. Without a published
 * site there is nowhere to buy, so the unpublished states are written as a clear call to action.
 */
export function SellerSiteStatus() {
  const { site } = useSellerContext();

  if (site.state === "published") {
    return (
      <section className={styles.siteBand} aria-labelledby="site-status-heading">
        <div className={styles.siteBandText}>
          <span className={`${common.pill} ${common.pillPos}`}>Đang hoạt động</span>
          <h2 id="site-status-heading" className={styles.siteBandHost}>
            {site.host}
          </h2>
          <p className={styles.siteBandNote}>
            Site đã xuất bản. Người mua truy cập trực tiếp địa chỉ này để xem hàng và đặt đơn.
          </p>
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
        className={`${styles.siteBand} ${styles.siteBandAlert}`}
        aria-labelledby="site-status-heading"
      >
        <div className={styles.siteBandText}>
          <span className={`${common.pill} ${common.pillNeg}`}>Tạm khóa</span>
          <h2 id="site-status-heading" className={styles.siteBandHost}>
            {site.host || "Site của gian hàng"}
          </h2>
          <p className={styles.siteBandNote}>
            Site đang bị NexusMart tạm khóa
            {site.suspendedReason ? ` vì: ${site.suspendedReason}` : ""}. Trong thời gian tạm khóa,
            người mua chưa thể truy cập và mua hàng.
          </p>
        </div>
        <div className={styles.siteBandActions}>
          <Link href="/seller/site" className={styles.siteBandPrimary}>
            Xem tình trạng site
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
      <div className={styles.siteBandText}>
        <span className={`${common.pill} ${common.pillWarn}`}>
          {isDraft ? "Bản nháp" : "Chưa thiết lập"}
        </span>
        <h2 id="site-status-heading" className={styles.siteBandHeadline}>
          {isDraft ? "Site của bạn chưa xuất bản" : "Gian hàng chưa có site bán hàng"}
        </h2>
        <p className={styles.siteBandNote}>
          Chưa xuất bản site thì người mua chưa thể truy cập và mua hàng: mọi đơn hàng chỉ phát sinh
          trên site riêng của gian hàng.{" "}
          {isDraft
            ? `Hoàn thiện và xuất bản ${site.host || "site"} để bắt đầu mở bán.`
            : "Chọn tên miền phụ, bảng màu và sản phẩm nổi bật rồi xuất bản để mở bán."}
        </p>
      </div>
      <div className={styles.siteBandActions}>
        <Link href="/seller/site" className={styles.siteBandPrimary}>
          {isDraft ? "Xuất bản site" : "Thiết lập site"}
        </Link>
      </div>
    </section>
  );
}
