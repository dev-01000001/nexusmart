"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Product } from "@/data";
import { isCatalogProduct, type CatalogProduct } from "@/lib/catalog-types";
import type { GatewayProductReview, GatewayProductReviewsResponse } from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateVi, formatRatingFromX100 } from "@/lib/format";
import { parseSafeReviewImageUrl } from "@/features/product/reviewHelpers";
import { Button, IconClose, IconStar } from "@/components";
import styles from "./PdpReviews.module.css";

export interface PdpReviewsProps {
  product: Product | CatalogProduct;
  initialReviews?: GatewayProductReviewsResponse | null;
}

type ReviewFilter = "all" | "with-images" | "5" | "4" | "3" | "2" | "1";

const STAR_LEVELS: Array<5 | 4 | 3 | 2 | 1> = [5, 4, 3, 2, 1];
const PAGE_SIZE = 10;

function readRatingCount(counts: Record<string, number> | undefined, star: number): number {
  if (!counts) return 0;
  const val = counts[String(star)];
  return typeof val === "number" && Number.isFinite(val) ? Math.max(0, Math.trunc(val)) : 0;
}

export function PdpReviews({ product, initialReviews }: PdpReviewsProps) {
  const catalogProduct = isCatalogProduct(product) ? product : null;
  const fallbackAvgX100 = catalogProduct?.ratingAvgX100 ?? 0;
  const fallbackCount = catalogProduct?.reviewCount ?? 0;

  const [filter, setFilter] = useState<ReviewFilter>("all");
  const [items, setItems] = useState<GatewayProductReview[]>(initialReviews?.items ?? []);
  const [ratingAvgX100, setRatingAvgX100] = useState<number>(
    initialReviews?.rating_avg_x100 ?? fallbackAvgX100
  );
  const [reviewCount, setReviewCount] = useState<number>(
    initialReviews?.review_count ?? fallbackCount
  );
  const [ratingCounts, setRatingCounts] = useState<Record<string, number>>(
    initialReviews?.rating_counts ?? {}
  );
  const [total, setTotal] = useState<number>(initialReviews?.total ?? fallbackCount);
  const [page, setPage] = useState<number>(1);
  const [loading, setLoading] = useState<boolean>(!initialReviews);
  const [loadingMore, setLoadingMore] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [zoomPhoto, setZoomPhoto] = useState<string | null>(null);

  const initialHydratedRef = useRef<boolean>(Boolean(initialReviews));
  const requestSeqRef = useRef<number>(0);

  useEffect(() => {
    if (!zoomPhoto) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setZoomPhoto(null);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [zoomPhoto]);

  const loadReviews = useCallback(
    async (targetFilter: ReviewFilter, targetPage: number, append: boolean) => {
      const seq = ++requestSeqRef.current;
      if (append) {
        setLoadingMore(true);
      } else {
        setLoading(true);
      }
      setError(null);

      try {
        const qs = new URLSearchParams();
        qs.set("page", String(targetPage));
        qs.set("page_size", String(PAGE_SIZE));
        if (targetFilter === "with-images") {
          qs.set("has_images", "true");
        } else if (
          targetFilter === "5" ||
          targetFilter === "4" ||
          targetFilter === "3" ||
          targetFilter === "2" ||
          targetFilter === "1"
        ) {
          qs.set("rating", targetFilter);
        }

        const res = await api<GatewayProductReviewsResponse>(
          `products/${encodeURIComponent(product.id)}/reviews?${qs.toString()}`,
          { method: "GET", retryOn401: false }
        );
        if (requestSeqRef.current !== seq) return;

        const nextItems = Array.isArray(res?.items) ? res.items : [];
        setItems((prev) => (append ? [...prev, ...nextItems] : nextItems));
        setRatingAvgX100(
          typeof res?.rating_avg_x100 === "number" ? res.rating_avg_x100 : fallbackAvgX100
        );
        setReviewCount(typeof res?.review_count === "number" ? res.review_count : fallbackCount);
        setRatingCounts(res?.rating_counts ?? {});
        setTotal(typeof res?.total === "number" ? res.total : nextItems.length);
        setPage(targetPage);
      } catch (err) {
        if (requestSeqRef.current !== seq) return;
        setError(getErrorMessage(err, "Chưa thể tải danh sách đánh giá sản phẩm lúc này."));
      } finally {
        if (requestSeqRef.current === seq) {
          setLoading(false);
          setLoadingMore(false);
        }
      }
    },
    [product.id, fallbackAvgX100, fallbackCount]
  );

  useEffect(() => {
    if (filter === "all" && initialHydratedRef.current) {
      initialHydratedRef.current = false;
      return;
    }
    void loadReviews(filter, 1, false);
  }, [filter, loadReviews]);

  const roundedStars = useMemo(() => Math.round(ratingAvgX100 / 100), [ratingAvgX100]);
  const formattedAvg = useMemo(() => formatRatingFromX100(ratingAvgX100), [ratingAvgX100]);
  const hasMore = items.length < total;

  return (
    <section className={styles.section} aria-labelledby="pdp-reviews-heading">
      <div className={styles.sectionHead}>
        <h2 id="pdp-reviews-heading" className={styles.sectionHeading}>
          Đánh giá từ người mua
        </h2>
        <p className={styles.sampleNotice}>
          {reviewCount > 0 ? `${reviewCount} đánh giá đã xác thực` : "Chưa có đánh giá"}
        </p>
      </div>

      <div className={styles.layoutGrid}>
        <aside className={styles.summaryCol}>
          {reviewCount > 0 ? (
            <div className={styles.scoreRow}>
              <span className={`${styles.scoreNum} num`}>{formattedAvg}</span>
              <div className={styles.scoreMeta}>
                <div
                  className={styles.starsRow}
                  aria-label={`Điểm trung bình ${formattedAvg} trên 5`}
                >
                  {[1, 2, 3, 4, 5].map((s) => (
                    <IconStar
                      key={s}
                      size={16}
                      filled={s <= roundedStars}
                      className={s <= roundedStars ? styles.starActive : styles.starIdle}
                    />
                  ))}
                </div>
                <span className={styles.scoreCount}>{reviewCount} đánh giá</span>
              </div>
            </div>
          ) : (
            <div className={styles.scoreRow}>
              <div className={styles.scoreMeta}>
                <span className={styles.subTitle}>Chưa có đánh giá</span>
                <span className={styles.scoreCount}>
                  Sản phẩm chưa có nhận xét từ đơn hoàn tất.
                </span>
              </div>
            </div>
          )}

          <div className={styles.starDist}>
            {STAR_LEVELS.map((star) => {
              const cnt = readRatingCount(ratingCounts, star);
              const pct = reviewCount > 0 ? Math.round((cnt / reviewCount) * 100) : 0;
              return (
                <button
                  key={star}
                  type="button"
                  className={styles.starDistRow}
                  onClick={() => setFilter(String(star) as ReviewFilter)}
                >
                  <span className={styles.starDistLabel}>{star} sao</span>
                  <span className={styles.fitTrack} aria-hidden="true">
                    <span className={styles.fitFill} style={{ width: `${pct}%` }} />
                  </span>
                  <span className={`${styles.fitPct} num`}>{cnt}</span>
                </button>
              );
            })}
          </div>

          <Button variant="secondary" size="md" fullWidth href="/don-hang">
            Đánh giá trong Đơn hàng của tôi
          </Button>
          <p className={styles.eligibilityNote}>
            Chỉ người mua đã hoàn tất đơn hàng mới có thể gửi đánh giá cho sản phẩm này.
          </p>
        </aside>

        <div className={styles.feedCol}>
          <div className={styles.filterBar} role="group" aria-label="Lọc đánh giá sản phẩm">
            <button
              type="button"
              className={`${styles.filterChip} ${filter === "all" ? styles.filterChipActive : ""}`}
              onClick={() => setFilter("all")}
              aria-pressed={filter === "all"}
            >
              Tất cả ({reviewCount})
            </button>
            <button
              type="button"
              className={`${styles.filterChip} ${filter === "with-images" ? styles.filterChipActive : ""}`}
              onClick={() => setFilter("with-images")}
              aria-pressed={filter === "with-images"}
            >
              Có ảnh
            </button>
            {STAR_LEVELS.map((star) => {
              const key = String(star) as ReviewFilter;
              const cnt = readRatingCount(ratingCounts, star);
              return (
                <button
                  key={star}
                  type="button"
                  className={`${styles.filterChip} ${filter === key ? styles.filterChipActive : ""}`}
                  onClick={() => setFilter(key)}
                  aria-pressed={filter === key}
                >
                  <span>{star} sao</span>
                  <span className={styles.chipCount}>({cnt})</span>
                </button>
              );
            })}
          </div>

          {error && (
            <div className={styles.emptyBox} role="alert">
              <p className={styles.errorText}>{error}</p>
              <div className={styles.emptyActions}>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => void loadReviews(filter, 1, false)}
                >
                  Thử lại
                </Button>
              </div>
            </div>
          )}

          {!error && loading ? (
            <div className={styles.emptyBox}>
              <p className={styles.emptyText}>Đang tải đánh giá sản phẩm...</p>
            </div>
          ) : !error && items.length === 0 ? (
            <div className={styles.emptyBox}>
              <p className={styles.emptyText}>
                {reviewCount === 0
                  ? "Chưa có đánh giá nào cho sản phẩm này."
                  : "Không có đánh giá nào khớp bộ lọc đang chọn."}
              </p>
              {filter !== "all" && (
                <div className={styles.emptyActions}>
                  <Button variant="secondary" size="sm" onClick={() => setFilter("all")}>
                    Xem tất cả đánh giá
                  </Button>
                </div>
              )}
            </div>
          ) : (
            !error && (
              <>
                <div className={styles.reviewList}>
                  {items.map((r) => {
                    const safeImages = (r.image_urls ?? [])
                      .map((u) => parseSafeReviewImageUrl(u))
                      .filter((u): u is string => Boolean(u));
                    return (
                      <article key={r.id} className={styles.reviewCard}>
                        <div className={styles.reviewTop}>
                          <div className={styles.authorBlock}>
                            <span className={styles.authorName}>
                              {r.buyer_name || "Người mua đã xác thực"}
                            </span>
                            <span className={styles.verifiedTag}>Đã mua hàng</span>
                          </div>
                          {r.created_at && (
                            <span className={styles.reviewDate}>{formatDateVi(r.created_at)}</span>
                          )}
                        </div>

                        <div className={styles.ratingMetaRow}>
                          <div className={styles.starsRow} aria-label={`${r.rating} trên 5 sao`}>
                            {[1, 2, 3, 4, 5].map((s) => (
                              <IconStar
                                key={s}
                                size={14}
                                filled={s <= r.rating}
                                className={s <= r.rating ? styles.starActive : styles.starIdle}
                              />
                            ))}
                          </div>
                          {r.sku_name && (
                            <span className={styles.fitTag}>Phân loại: {r.sku_name}</span>
                          )}
                        </div>

                        {r.comment && <p className={styles.comment}>{r.comment}</p>}

                        {safeImages.length > 0 && (
                          <div className={styles.photoRow}>
                            {safeImages.map((imgSrc, pIdx) => (
                              <button
                                key={`${r.id}-img-${pIdx}`}
                                type="button"
                                className={styles.photoBtn}
                                onClick={() => setZoomPhoto(imgSrc)}
                                aria-label={`Phóng lớn ảnh đánh giá ${pIdx + 1}`}
                              >
                                <img
                                  src={imgSrc}
                                  alt={`Ảnh đánh giá sản phẩm từ ${r.buyer_name || "người mua"}`}
                                  width={144}
                                  height={192}
                                  loading="lazy"
                                  referrerPolicy="no-referrer"
                                  className={styles.photoThumb}
                                />
                              </button>
                            ))}
                          </div>
                        )}
                      </article>
                    );
                  })}
                </div>

                {hasMore && (
                  <div className={styles.emptyActions}>
                    <Button
                      variant="secondary"
                      size="md"
                      disabled={loadingMore}
                      onClick={() => void loadReviews(filter, page + 1, true)}
                    >
                      {loadingMore ? "Đang tải thêm..." : "Xem thêm đánh giá"}
                    </Button>
                  </div>
                )}
              </>
            )
          )}
        </div>
      </div>

      {zoomPhoto && (
        <div
          className={styles.zoomBackdrop}
          role="dialog"
          aria-modal="true"
          aria-label="Xem ảnh đánh giá phóng lớn"
          onClick={(e) => {
            if (e.target === e.currentTarget) setZoomPhoto(null);
          }}
        >
          <div className={styles.zoomPanel}>
            <div className={styles.zoomHead}>
              <span className={styles.subTitle}>Ảnh đánh giá sản phẩm</span>
              <button
                type="button"
                className={styles.zoomClose}
                onClick={() => setZoomPhoto(null)}
                aria-label="Đóng ảnh phóng lớn"
              >
                <IconClose size={18} />
              </button>
            </div>
            <img
              src={zoomPhoto}
              alt="Ảnh đánh giá sản phẩm phóng lớn"
              width={640}
              height={854}
              referrerPolicy="no-referrer"
              className={styles.zoomImg}
            />
          </div>
        </div>
      )}
    </section>
  );
}
