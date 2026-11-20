"use client";

import React, { useEffect, useMemo, useState } from "react";
import {
  SAMPLE_DATA_NOTICE,
  computeReviewSummary,
  type FitFeedback,
  type Product,
  type Shop,
  type SizeCode,
} from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconClose, IconStar } from "@/components";
import { formatDateVi } from "@/lib/format";
import styles from "./PdpReviews.module.css";

export interface PdpReviewsProps {
  product: Product;
  shop: Shop;
  selectedColorName: string;
  selectedSize: SizeCode;
}

type ReviewFilter = "all" | "with-images" | "with-reply" | "5" | "4" | "3" | "2" | "1";

const FIT_LABELS: Record<FitFeedback, string> = {
  chat: "Chật",
  dung: "Đúng cỡ",
  rong: "Rộng",
};

const FIT_ORDER: FitFeedback[] = ["chat", "dung", "rong"];
const STAR_LEVELS: Array<5 | 4 | 3 | 2 | 1> = [5, 4, 3, 2, 1];

export function PdpReviews({ product, shop, selectedColorName, selectedSize }: PdpReviewsProps) {
  const { reviews, submitReview, bodyProfile, orders, user } = useStore();

  const [filter, setFilter] = useState<ReviewFilter>("all");
  const [formOpen, setFormOpen] = useState<boolean>(false);
  const [zoomPhoto, setZoomPhoto] = useState<string | null>(null);

  useEffect(() => {
    if (!zoomPhoto) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setZoomPhoto(null);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [zoomPhoto]);

  // Form state
  const [authorName, setAuthorName] = useState<string>(user.fullName);
  const [rating, setRating] = useState<number>(5);
  const [fitFeedback, setFitFeedback] = useState<FitFeedback>("dung");
  const [heightCm, setHeightCm] = useState<number>(bodyProfile.heightCm);
  const [weightKg, setWeightKg] = useState<number>(bodyProfile.weightKg);
  const [purchasedSize, setPurchasedSize] = useState<SizeCode>(selectedSize);
  const [colorName, setColorName] = useState<string>(selectedColorName);
  const [comment, setComment] = useState<string>("");
  const [attachSampleImg, setAttachSampleImg] = useState<boolean>(false);
  const [formError, setFormError] = useState<string>("");

  const productReviews = useMemo(
    () => reviews.filter((r) => r.productId === product.id),
    [reviews, product.id]
  );

  const summary = useMemo(() => computeReviewSummary(reviews, product.id), [reviews, product.id]);

  const hasDeliveredOrder = useMemo(
    () =>
      orders.some(
        (o) => o.status === "da_giao" && o.items.some((it) => it.productId === product.id)
      ),
    [orders, product.id]
  );

  const filteredReviews = useMemo(() => {
    return productReviews.filter((r) => {
      if (filter === "with-images") return r.images.length > 0;
      if (filter === "with-reply") return Boolean(r.shopReply);
      if (filter === "5" || filter === "4" || filter === "3" || filter === "2" || filter === "1") {
        return Math.round(r.rating) === Number(filter);
      }
      return true;
    });
  }, [productReviews, filter]);

  const handleOpenForm = () => {
    setHeightCm(bodyProfile.heightCm);
    setWeightKg(bodyProfile.weightKg);
    setPurchasedSize(selectedSize);
    setColorName(selectedColorName);
    setFormOpen((v) => !v);
    setFormError("");
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (comment.trim().length < 10) {
      setFormError("Vui lòng chia sẻ cảm nhận tối thiểu 10 ký tự về chất vải hoặc độ vừa.");
      return;
    }
    const matchedColor = product.colors.find((c) => c.name === colorName);
    submitReview({
      productId: product.id,
      authorName: authorName.trim() || user.fullName,
      rating,
      fitFeedback,
      buyerHeightCm: Math.max(140, Math.min(210, Math.round(heightCm))),
      buyerWeightKg: Math.max(35, Math.min(140, Math.round(weightKg))),
      purchasedSize,
      colorName: colorName || product.colors[0]?.name || "Mặc định",
      comment: comment.trim(),
      images: attachSampleImg ? [matchedColor?.image ?? product.image] : [],
    });
    setComment("");
    setAttachSampleImg(false);
    setFormError("");
    setFormOpen(false);
    setFilter("all");
  };

  return (
    <section className={styles.section} aria-labelledby="pdp-reviews-heading">
      <div className={styles.sectionHead}>
        <h2 id="pdp-reviews-heading" className={styles.sectionHeading}>
          Đánh giá và cảm nhận độ vừa
        </h2>
        <p className={styles.sampleNotice}>Đánh giá mẫu tham khảo về độ vừa</p>
      </div>

      <div className={styles.layoutGrid}>
        <aside className={styles.summaryCol}>
          <div className={styles.scoreRow}>
            <span className={styles.scoreNum}>
              {summary.count > 0 ? summary.average.toFixed(1) : "0,0"}
            </span>
            <div className={styles.scoreMeta}>
              <div
                className={styles.starsRow}
                aria-label={`Điểm trung bình mẫu ${summary.average} trên 5`}
              >
                {[1, 2, 3, 4, 5].map((s) => (
                  <IconStar
                    key={s}
                    size={16}
                    filled={s <= Math.round(summary.average)}
                    className={
                      s <= Math.round(summary.average) ? styles.starActive : styles.starIdle
                    }
                  />
                ))}
              </div>
              <span className={styles.scoreCount}>{summary.count} đánh giá mẫu</span>
            </div>
          </div>

          <div className={styles.fitBlock}>
            <h3 className={styles.subTitle}>Tỷ lệ cảm nhận độ vừa</h3>
            <div className={styles.fitBars}>
              {FIT_ORDER.map((key) => {
                const pct = summary.fitPercentages[key];
                return (
                  <div key={key} className={styles.fitBarRow}>
                    <span className={styles.fitLabel}>{FIT_LABELS[key]}</span>
                    <div className={styles.fitTrack} aria-hidden="true">
                      <span
                        className={`${styles.fitFill} ${key === "dung" ? styles.fitFillPrimary : ""}`}
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                    <span className={styles.fitPct}>{pct}%</span>
                  </div>
                );
              })}
            </div>
          </div>

          <div className={styles.starDist}>
            {STAR_LEVELS.map((star) => {
              const cnt = summary.starCounts[star];
              const pct = summary.count > 0 ? Math.round((cnt / summary.count) * 100) : 0;
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
                  <span className={styles.fitPct}>{cnt}</span>
                </button>
              );
            })}
          </div>

          <p className={styles.noticeDetail}>{SAMPLE_DATA_NOTICE}</p>

          <Button variant="ink" size="md" fullWidth onClick={handleOpenForm}>
            {formOpen ? "Đóng biểu mẫu đánh giá" : "Viết đánh giá sản phẩm"}
          </Button>
          <p className={styles.eligibilityNote}>
            {hasDeliveredOrder
              ? "Đơn hàng đã giao của bạn có mẫu này, đủ điều kiện gửi đánh giá."
              : "Chia sẻ cảm nhận về chất vải và độ vừa để giúp người mua sau chọn đúng size."}
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
              Tất cả ({summary.count})
            </button>
            <button
              type="button"
              className={`${styles.filterChip} ${filter === "with-images" ? styles.filterChipActive : ""}`}
              onClick={() => setFilter("with-images")}
              aria-pressed={filter === "with-images"}
            >
              Có ảnh ({summary.withImagesCount})
            </button>
            <button
              type="button"
              className={`${styles.filterChip} ${filter === "with-reply" ? styles.filterChipActive : ""}`}
              onClick={() => setFilter("with-reply")}
              aria-pressed={filter === "with-reply"}
            >
              Có phản hồi xưởng ({summary.withReplyCount})
            </button>
            {STAR_LEVELS.map((star) => {
              const key = String(star) as ReviewFilter;
              return (
                <button
                  key={star}
                  type="button"
                  className={`${styles.filterChip} ${filter === key ? styles.filterChipActive : ""}`}
                  onClick={() => setFilter(key)}
                  aria-pressed={filter === key}
                >
                  <span>{star} sao</span>
                  <span className={styles.chipCount}>({summary.starCounts[star]})</span>
                </button>
              );
            })}
          </div>

          {formOpen && (
            <form className={styles.reviewForm} onSubmit={handleSubmit} noValidate>
              <h3 className={styles.subTitle}>Gửi đánh giá mới cho {product.shortName}</h3>

              <div className={styles.formRow}>
                <div className={styles.formField}>
                  <span className={styles.fieldLabel}>Số sao đánh giá</span>
                  <div className={styles.ratingPick} role="group" aria-label="Chọn số sao">
                    {[1, 2, 3, 4, 5].map((s) => (
                      <button
                        key={s}
                        type="button"
                        className={`${styles.ratingBtn} ${rating === s ? styles.ratingBtnActive : ""}`}
                        onClick={() => setRating(s)}
                        aria-pressed={rating === s}
                      >
                        <IconStar size={14} filled={s <= rating} />
                        <span>{s}</span>
                      </button>
                    ))}
                  </div>
                </div>

                <div className={styles.formField}>
                  <span className={styles.fieldLabel}>Cảm nhận độ vừa</span>
                  <div className={styles.fitPick} role="group" aria-label="Chọn cảm nhận độ vừa">
                    {FIT_ORDER.map((fitKey) => (
                      <button
                        key={fitKey}
                        type="button"
                        className={`${styles.ratingBtn} ${fitFeedback === fitKey ? styles.ratingBtnActive : ""}`}
                        onClick={() => setFitFeedback(fitKey)}
                        aria-pressed={fitFeedback === fitKey}
                      >
                        {FIT_LABELS[fitKey]}
                      </button>
                    ))}
                  </div>
                </div>
              </div>

              <div className={styles.formGrid4}>
                <label className={styles.formField}>
                  <span className={styles.fieldLabel}>Tên hiển thị</span>
                  <input
                    type="text"
                    value={authorName}
                    onChange={(e) => setAuthorName(e.target.value)}
                    className={styles.input}
                  />
                </label>

                <label className={styles.formField}>
                  <span className={styles.fieldLabel}>Chiều cao (cm)</span>
                  <input
                    type="number"
                    min={140}
                    max={210}
                    value={heightCm}
                    onChange={(e) => setHeightCm(Number(e.target.value) || 170)}
                    className={styles.input}
                  />
                </label>

                <label className={styles.formField}>
                  <span className={styles.fieldLabel}>Cân nặng (kg)</span>
                  <input
                    type="number"
                    min={35}
                    max={140}
                    value={weightKg}
                    onChange={(e) => setWeightKg(Number(e.target.value) || 65)}
                    className={styles.input}
                  />
                </label>

                <label className={styles.formField}>
                  <span className={styles.fieldLabel}>Size đã mua</span>
                  <select
                    value={purchasedSize}
                    onChange={(e) => setPurchasedSize(e.target.value as SizeCode)}
                    className={styles.select}
                  >
                    {product.sizes.map((sz) => (
                      <option key={sz} value={sz}>
                        Size {sz}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              <div className={styles.formRow}>
                <label className={styles.formField}>
                  <span className={styles.fieldLabel}>Màu đã mua</span>
                  <select
                    value={colorName}
                    onChange={(e) => setColorName(e.target.value)}
                    className={styles.select}
                  >
                    {product.colors.map((c) => (
                      <option key={c.code} value={c.name}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>

                <label className={styles.checkLabel}>
                  <input
                    type="checkbox"
                    checked={attachSampleImg}
                    onChange={(e) => setAttachSampleImg(e.target.checked)}
                    className={styles.checkbox}
                  />
                  <span>Đính kèm ảnh sản phẩm mẫu minh họa</span>
                </label>
              </div>

              <label className={styles.formField}>
                <span className={styles.fieldLabel}>Nhận xét về chất vải và phom dáng</span>
                <textarea
                  rows={3}
                  value={comment}
                  onChange={(e) => {
                    setComment(e.target.value);
                    if (formError) setFormError("");
                  }}
                  placeholder="Ví dụ: Vải mặc thoáng, cổ áo đứng dáng, mình 1m73 nặng 68 kg mặc size L vừa gọn..."
                  className={styles.textarea}
                />
              </label>

              {formError && <p className={styles.errorText}>{formError}</p>}

              <div className={styles.formActions}>
                <Button type="submit" variant="primary" size="md">
                  Đăng đánh giá
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  size="md"
                  onClick={() => setFormOpen(false)}
                >
                  Hủy
                </Button>
              </div>
            </form>
          )}

          {filteredReviews.length === 0 ? (
            <div className={styles.emptyBox}>
              <p className={styles.emptyText}>
                {summary.count === 0
                  ? `Chưa có đánh giá nào cho ${product.shortName}. Hãy là người đầu tiên chia sẻ cảm nhận về chất vải và độ vừa.`
                  : "Không có đánh giá nào khớp bộ lọc đang chọn."}
              </p>
              <div className={styles.emptyActions}>
                {filter !== "all" && (
                  <Button variant="secondary" size="sm" onClick={() => setFilter("all")}>
                    Xem tất cả đánh giá
                  </Button>
                )}
                {!formOpen && (
                  <Button variant="ink" size="sm" onClick={handleOpenForm}>
                    Viết đánh giá mẫu
                  </Button>
                )}
              </div>
            </div>
          ) : (
            <div className={styles.reviewList}>
              {filteredReviews.map((r) => (
                <article key={r.id} className={styles.reviewCard}>
                  <div className={styles.reviewTop}>
                    <div className={styles.authorBlock}>
                      <span className={styles.authorName}>{r.authorName}</span>
                      <span className={styles.verifiedTag}>
                        {r.isSampleData ? "Đánh giá mẫu" : "Đơn đã xác nhận"}
                      </span>
                    </div>
                    <span className={styles.reviewDate}>{formatDateVi(r.createdAt)}</span>
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
                    <span className={styles.fitTag}>Độ vừa: {FIT_LABELS[r.fitFeedback]}</span>
                  </div>

                  <p className={styles.buyerSpec}>
                    Cao {r.buyerHeightCm} cm · Nặng {r.buyerWeightKg} kg · Đã mua Size{" "}
                    {r.purchasedSize} ({r.colorName})
                  </p>

                  <p className={styles.comment}>{r.comment}</p>

                  {r.images.length > 0 && (
                    <div className={styles.photoRow}>
                      {r.images.map((imgSrc, pIdx) => (
                        <button
                          key={`${r.id}-img-${pIdx}`}
                          type="button"
                          className={styles.photoBtn}
                          onClick={() => setZoomPhoto(imgSrc)}
                          aria-label={`Phóng lớn ảnh đánh giá ${pIdx + 1} của ${r.authorName}`}
                        >
                          <img
                            src={imgSrc}
                            alt={`Ảnh mẫu đánh giá từ ${r.authorName}`}
                            width={144}
                            height={192}
                            loading="lazy"
                            className={styles.photoThumb}
                          />
                        </button>
                      ))}
                    </div>
                  )}

                  {r.shopReply && (
                    <div className={styles.shopReply}>
                      <div className={styles.replyHead}>
                        <span className={styles.replyShop}>
                          Phản hồi công khai từ {r.shopReply.shopName || shop.name}
                        </span>
                        <span className={styles.reviewDate}>
                          {formatDateVi(r.shopReply.repliedAt)}
                        </span>
                      </div>
                      <p className={styles.replyText}>{r.shopReply.text}</p>
                    </div>
                  )}
                </article>
              ))}
            </div>
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
              <span className={styles.subTitle}>Ảnh đánh giá mẫu</span>
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
              className={styles.zoomImg}
            />
          </div>
        </div>
      )}
    </section>
  );
}
