"use client";

import React, { useEffect, useState } from "react";
import { IconCheck, IconSpark, IconStar } from "@/components/Icons";
import { api } from "@/lib/api";
import { formatInt } from "@/lib/format";
import {
  normalizeReviewSummary,
  shouldDisplayReviewSummary,
  type ProductReviewSummaryProps,
  type ReviewSummaryDTO,
} from "./assistantTypes";
import styles from "./ProductReviewSummary.module.css";

export function ProductReviewSummary({
  productId,
  productName,
  className,
}: ProductReviewSummaryProps) {
  const [summary, setSummary] = useState<ReviewSummaryDTO | null>(null);

  useEffect(() => {
    const cleanId = productId.trim();
    if (!cleanId) {
      setSummary(null);
      return;
    }

    const controller = new AbortController();
    let active = true;

    api<unknown>(`/api/products/${encodeURIComponent(cleanId)}/review-summary`, {
      signal: controller.signal,
    })
      .then((raw) => {
        if (!active) return;
        const normalized = normalizeReviewSummary(raw, cleanId);
        setSummary(shouldDisplayReviewSummary(normalized) ? normalized : null);
      })
      .catch(() => {
        // Hide quietly on 404, 503, insufficient reviews, or network error.
        if (active) setSummary(null);
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [productId]);

  if (!shouldDisplayReviewSummary(summary) || !summary) {
    return null;
  }

  const hasPros = summary.pros.length > 0;
  const hasCons = summary.cons.length > 0;

  return (
    <section
      className={`${styles.card} ${className ?? ""}`}
      data-ui="review-summary"
      aria-label={
        productName ? `Tóm tắt đánh giá cho ${productName}` : "Tóm tắt đánh giá do AI tổng hợp"
      }
    >
      <div className={styles.header}>
        <div className={styles.titleRow}>
          <span className={styles.iconWrap} aria-hidden="true">
            <IconSpark size={15} />
          </span>
          <h3 className={styles.heading}>Tóm tắt đánh giá do AI tổng hợp</h3>
        </div>

        <div className={styles.metaRow}>
          {summary.averageRating > 0 && (
            <span className={styles.ratingBadge}>
              <IconStar size={13} filled />
              <span>{summary.averageRating.toFixed(1).replace(".", ",")}/5</span>
            </span>
          )}
          <span className={styles.countText}>
            Tổng hợp từ {formatInt(summary.reviewCount)} đánh giá
          </span>
        </div>
      </div>

      {summary.summary && <p className={styles.overview}>{summary.summary}</p>}

      {(hasPros || hasCons) && (
        <div className={`${styles.columns} ${hasPros && hasCons ? styles.columnsTwo : ""}`}>
          {hasPros && (
            <div className={styles.listCol}>
              <h4 className={styles.listHeadingPos}>Ưu điểm nổi bật</h4>
              <ul className={styles.bulletList}>
                {summary.pros.map((item) => (
                  <li key={item} className={styles.bulletItem}>
                    <span className={styles.posMark} aria-hidden="true">
                      <IconCheck size={13} />
                    </span>
                    <span>{item}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {hasCons && (
            <div className={styles.listCol}>
              <h4 className={styles.listHeadingWarn}>Điểm cần lưu ý</h4>
              <ul className={styles.bulletList}>
                {summary.cons.map((item) => (
                  <li key={item} className={styles.bulletItem}>
                    <span className={styles.warnDot} aria-hidden="true" />
                    <span>{item}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}

      <p className={styles.footnote}>
        Nội dung được tổng hợp tự động từ nhận xét thực tế của người mua trên cửa hàng.
      </p>
    </section>
  );
}
