"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { SHOPS, getShopById } from "@/data";
import type { FitFeedbackKind, LookbookStatus } from "@/data";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPlus, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import {
  answerSellerQuestion,
  createSellerFaqItem,
  createSellerLookbookPost,
  getBackInStockRowsWithSession,
  getProductExtendedMetaWithSession,
  getShopProductsWithSession,
  parseUiState,
  quickRestockSellerSku,
  replyToSellerReview,
  submitSellerLookbookForReview,
  toggleSellerFaqActive,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type InteractionTab = "reviews" | "qa" | "lookbook" | "waiting";
type ReviewFilter = "all" | "unreplied" | "photo" | "fit_issue";

const FIT_META: Record<FitFeedbackKind, { label: string; cls: string }> = {
  chat: { label: "Hơi chật", cls: `${common.pill} ${common.pillWarn}` },
  dung: { label: "Đúng cỡ", cls: `${common.pill} ${common.pillPos}` },
  rong: { label: "Hơi rộng", cls: `${common.pill} ${common.pillWarn}` },
};

const LOOKBOOK_CATEGORIES = [
  "Phối đồ hằng ngày",
  "Chạy bộ & thể thao",
  "Công sở tối giản",
  "Bảo quản vải",
];

const FAQ_TOPICS = [
  "Chọn size",
  "Chất liệu & giặt ủi",
  "Giao hàng & đổi trả 45 ngày",
  "Hóa đơn VAT",
];

function SellerInteractionContent() {
  return (
    <SellerShopGate pageTitle="Tương tác & Nội dung gian hàng">
      {() => <SellerInteractionInner />}
    </SellerShopGate>
  );
}

function SellerInteractionInner() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as InteractionTab | null;

  const { showToast } = useMockStore();
  const { shop, mockShopKey } = useSellerContext();
  const session = useSellerSession();
  const activeShop = getShopById(mockShopKey) ?? SHOPS[0];
  const displayShopName = shop?.name ?? activeShop.name;

  const [tab, setTab] = useState<InteractionTab>(
    initialTab && ["reviews", "qa", "lookbook", "waiting"].includes(initialTab)
      ? initialTab
      : "reviews"
  );

  // Reviews tab state
  const [reviewFilter, setReviewFilter] = useState<ReviewFilter>("all");
  const [reviewDrafts, setReviewDrafts] = useState<Record<string, string>>({});

  // Q&A + FAQ tab state
  const [qaFilter, setQaFilter] = useState<"all" | "unanswered">("all");
  const [qaDrafts, setQaDrafts] = useState<Record<string, string>>({});
  const [showFaqForm, setShowFaqForm] = useState(false);
  const [faqTopic, setFaqTopic] = useState(FAQ_TOPICS[0]);
  const [faqQuestion, setFaqQuestion] = useState("");
  const [faqAnswer, setFaqAnswer] = useState("");

  // Lookbook tab state
  const [lbFilter, setLbFilter] = useState<"all" | LookbookStatus>("all");
  const [showLbForm, setShowLbForm] = useState(false);
  const [lbTitle, setLbTitle] = useState("");
  const [lbCategory, setLbCategory] = useState(LOOKBOOK_CATEGORIES[0]);
  const [lbExcerpt, setLbExcerpt] = useState("");
  const [lbTaggedIds, setLbTaggedIds] = useState<string[]>([]);

  const shopProducts = useMemo(
    () => getShopProductsWithSession(activeShop.id, session),
    [activeShop.id, session]
  );

  const reviews = useMemo(
    () => session.reviews.filter((r) => r.shopId === activeShop.id),
    [session.reviews, activeShop.id]
  );
  const questions = useMemo(
    () => session.questions.filter((q) => q.shopId === activeShop.id),
    [session.questions, activeShop.id]
  );
  const faqs = useMemo(
    () => session.faqs.filter((f) => f.shopId === activeShop.id),
    [session.faqs, activeShop.id]
  );
  const lookbooks = useMemo(
    () => session.lookbooks.filter((l) => l.shopId === activeShop.id),
    [session.lookbooks, activeShop.id]
  );
  const backInStockRows = useMemo(
    () => getBackInStockRowsWithSession(activeShop.id, session),
    [activeShop.id, session]
  );

  const wishlistRanking = useMemo(() => {
    return shopProducts
      .map((p) => {
        const meta = getProductExtendedMetaWithSession(p.id, p.categorySlug, session);
        const waiting = Object.values(meta.backInStockSubscribers).reduce((s, n) => s + n, 0);
        return {
          productId: p.id,
          shortName: p.shortName,
          categoryName: p.categoryName,
          price: p.price,
          wishlistCount: meta.wishlistCount,
          waitingCount: waiting,
        };
      })
      .sort((a, b) => b.wishlistCount - a.wishlistCount);
  }, [shopProducts, session]);

  // Review stats (computed strictly from rows)
  const unrepliedReviewsCount = reviews.filter((r) => !r.reply).length;
  const photoReviewsCount = reviews.filter((r) => r.hasPhoto).length;
  const fitStats = useMemo(() => {
    const total = reviews.length;
    if (total === 0) return { chat: 0, dung: 0, rong: 0 };
    const chatCount = reviews.filter((r) => r.fitFeedback === "chat").length;
    const rongCount = reviews.filter((r) => r.fitFeedback === "rong").length;
    const chat = Math.round((chatCount / total) * 100);
    const rong = Math.round((rongCount / total) * 100);
    const dung = Math.max(0, 100 - chat - rong);
    return { chat, dung, rong };
  }, [reviews]);

  const filteredReviews = useMemo(() => {
    return reviews.filter((r) => {
      if (reviewFilter === "unreplied") return !r.reply;
      if (reviewFilter === "photo") return r.hasPhoto;
      if (reviewFilter === "fit_issue") return r.fitFeedback !== "dung";
      return true;
    });
  }, [reviews, reviewFilter]);

  const unansweredQuestionsCount = questions.filter((q) => !q.answer).length;
  const filteredQuestions = useMemo(
    () => (qaFilter === "unanswered" ? questions.filter((q) => !q.answer) : questions),
    [questions, qaFilter]
  );

  const lbCounts = useMemo(
    () => ({
      all: lookbooks.length,
      da_dang: lookbooks.filter((l) => l.status === "da_dang").length,
      cho_duyet: lookbooks.filter((l) => l.status === "cho_duyet").length,
      nhap: lookbooks.filter((l) => l.status === "nhap").length,
    }),
    [lookbooks]
  );

  const filteredLookbooks = useMemo(
    () => (lbFilter === "all" ? lookbooks : lookbooks.filter((l) => l.status === lbFilter)),
    [lookbooks, lbFilter]
  );

  const totalWaitingCustomers = backInStockRows.reduce((s, r) => s + r.waitingCount, 0);
  const totalWishlists = wishlistRanking.reduce((s, r) => s + r.wishlistCount, 0);
  const maxWishlist = Math.max(1, ...wishlistRanking.map((w) => w.wishlistCount));

  const handleReplyReview = (reviewId: string) => {
    const text = (reviewDrafts[reviewId] ?? "").trim();
    if (!text) return;
    replyToSellerReview(reviewId, text);
    setReviewDrafts((prev) => ({ ...prev, [reviewId]: "" }));
    showToast("Đã đăng phản hồi công khai của xưởng dưới đánh giá.");
  };

  const handleAnswerQuestion = (questionId: string) => {
    const text = (qaDrafts[questionId] ?? "").trim();
    if (!text) return;
    answerSellerQuestion(questionId, text);
    setQaDrafts((prev) => ({ ...prev, [questionId]: "" }));
    showToast("Đã gửi câu trả lời công khai lên trang sản phẩm.");
  };

  const handleCreateFaq = (e: React.FormEvent) => {
    e.preventDefault();
    if (faqQuestion.trim().length < 6 || faqAnswer.trim().length < 10) return;
    createSellerFaqItem({
      id: `faq-custom-${Date.now()}`,
      shopId: activeShop.id,
      topic: faqTopic,
      question: faqQuestion.trim(),
      answer: faqAnswer.trim(),
      updatedAt: "2026-11-20",
      active: true,
    });
    setFaqQuestion("");
    setFaqAnswer("");
    setShowFaqForm(false);
    showToast("Đã thêm câu hỏi thường gặp vào bộ FAQ riêng của xưởng.");
  };

  const toggleLbProduct = (prodId: string) => {
    setLbTaggedIds((prev) =>
      prev.includes(prodId) ? prev.filter((id) => id !== prodId) : [...prev, prodId]
    );
  };

  const handleCreateLookbook = (targetStatus: "nhap" | "cho_duyet") => {
    if (lbTitle.trim().length < 6) {
      showToast("Tiêu đề bài Lookbook cần tối thiểu 6 ký tự.");
      return;
    }
    const tagged = lbTaggedIds.length > 0 ? lbTaggedIds : [shopProducts[0]?.id].filter(Boolean);
    const coverProd = shopProducts.find((p) => p.id === tagged[0]) ?? shopProducts[0];
    createSellerLookbookPost({
      id: `lb-custom-${Date.now()}`,
      shopId: activeShop.id,
      title: lbTitle.trim(),
      category: lbCategory,
      excerpt:
        lbExcerpt.trim() ||
        "Bài phối đồ thực tế gắn thẻ SKU trực tiếp từ kho của gian hàng để người mua chọn cỡ nhanh.",
      coverImage: coverProd?.image ?? "/products/ao-thun-cotton-trang.png",
      taggedProductIds: tagged,
      status: targetStatus,
      submittedAt: "2026-11-20",
      adminNote:
        targetStatus === "cho_duyet"
          ? "Đã gửi hàng đợi biên tập nội dung Admin duyệt đăng lên Sổ tay vải & Phối đồ."
          : undefined,
      views: 0,
    });
    setLbTitle("");
    setLbExcerpt("");
    setLbTaggedIds([]);
    setShowLbForm(false);
    showToast(
      targetStatus === "cho_duyet"
        ? "Đã gửi bài Lookbook gắn SKU sang Admin chờ duyệt."
        : "Đã lưu bản nháp bài Lookbook."
    );
  };

  const handleQuickRestock = (productId: string, skuCode: string) => {
    const product = shopProducts.find((p) => p.id === productId);
    if (!product) return;
    const res = quickRestockSellerSku(product, skuCode, 20);
    if (res.notifiedCount > 0) {
      showToast(
        `Đã nhập +20 chiếc cho ${skuCode} (tồn mới: ${formatInt(res.newStock)}) và gửi báo có hàng tới ${formatInt(res.notifiedCount)} khách.`
      );
    } else {
      showToast(`Đã bổ sung +20 tồn kho cho ${skuCode} (tồn mới: ${formatInt(res.newStock)}).`);
    }
  };

  const lbStatusPill = (status: LookbookStatus) =>
    status === "da_dang"
      ? { label: "Đã đăng", cls: `${common.pill} ${common.pillPos}` }
      : status === "cho_duyet"
        ? { label: "Chờ Admin duyệt", cls: `${common.pill} ${common.pillWarn}` }
        : { label: "Bản nháp", cls: common.pill };

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tương tác & Nội dung gian hàng</h1>
          <p className={common.finding}>
            {displayShopName} · {formatInt(unrepliedReviewsCount)} đánh giá chưa phản hồi,{" "}
            {formatInt(unansweredQuestionsCount)} câu hỏi sản phẩm chờ trả lời ·{" "}
            {formatInt(totalWaitingCustomers)} khách đang chờ báo có hàng
          </p>
        </div>
        <div className={common.headerActions}>
          {tab === "qa" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowFaqForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showFaqForm ? "Đóng mẫu FAQ" : "Thêm câu FAQ xưởng"}</span>
            </Button>
          )}
          {tab === "lookbook" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowLbForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showLbForm ? "Đóng trình soạn" : "Gửi bài Lookbook mới"}</span>
            </Button>
          )}
          {tab === "reviews" && unrepliedReviewsCount > 0 && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setReviewFilter("unreplied")}
            >
              Lọc chưa trả lời ({formatInt(unrepliedReviewsCount)})
            </Button>
          )}
          {tab === "waiting" && (
            <Button variant="ink" size="sm" shape="app" href="/seller/san-pham">
              Quản lý tồn kho SKU
            </Button>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải đánh giá, hỏi đáp, bài viết Lookbook và danh sách khách chờ hàng..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu tương tác gian hàng"
          message="Dịch vụ đánh giá và nội dung tạm thời chưa phản hồi."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/tuong-tac"
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa có đánh giá hay câu hỏi nào"
          message="Khi đơn hàng hoàn tất hoặc khách gửi câu hỏi trên trang sản phẩm, nội dung sẽ hiển thị tại đây."
          actionLabel="Gửi bài Lookbook đầu tiên"
          onAction={() => {
            setTab("lookbook");
            setShowLbForm(true);
          }}
        />
      )}

      {!uiState && (
        <>
          <div className={common.underlineTabs} role="tablist" aria-label="Phân mục tương tác">
            <button
              type="button"
              role="tab"
              aria-selected={tab === "reviews"}
              className={`${common.underlineTab} ${tab === "reviews" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("reviews")}
            >
              Đánh giá ({formatInt(reviews.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "qa"}
              className={`${common.underlineTab} ${tab === "qa" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("qa")}
            >
              Hỏi đáp & FAQ ({formatInt(questions.length + faqs.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "lookbook"}
              className={`${common.underlineTab} ${tab === "lookbook" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("lookbook")}
            >
              Lookbook & Bài viết ({formatInt(lookbooks.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "waiting"}
              className={`${common.underlineTab} ${tab === "waiting" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("waiting")}
            >
              Chờ hàng & Yêu thích ({formatInt(totalWaitingCustomers)})
            </button>
          </div>

          {/* TAB 1: Customer Reviews (Sample data) + Fit breakdown */}
          {tab === "reviews" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp đánh giá và độ vừa">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đánh giá chờ phản hồi</span>
                  <span
                    className={`${common.balanceValue} ${unrepliedReviewsCount === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(unrepliedReviewsCount)} / {formatInt(reviews.length)}
                  </span>
                  <span className={common.balanceCaption}>
                    Chỉ ghi nhận từ đơn hàng đã hoàn tất
                  </span>
                </div>

                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>
                    Tỷ lệ độ vừa thực tế (Chật / Đúng / Rộng)
                  </span>
                  <div className={styles.fitBarTrack} aria-label="Biểu đồ tỷ lệ độ vừa">
                    {fitStats.chat > 0 && (
                      <div className={styles.fitSegTight} style={{ width: `${fitStats.chat}%` }} />
                    )}
                    {fitStats.dung > 0 && (
                      <div
                        className={styles.fitSegCorrect}
                        style={{ width: `${fitStats.dung}%` }}
                      />
                    )}
                    {fitStats.rong > 0 && (
                      <div className={styles.fitSegLoose} style={{ width: `${fitStats.rong}%` }} />
                    )}
                  </div>
                  <div className={styles.fitLegend}>
                    <span className={styles.fitLegendItem}>
                      <span
                        className={styles.legendDot}
                        style={{ backgroundColor: "var(--ink)" }}
                      />
                      Chật {fitStats.chat}%
                    </span>
                    <span className={styles.fitLegendItem}>
                      <span
                        className={styles.legendDot}
                        style={{ backgroundColor: "var(--chart-1)" }}
                      />
                      Đúng cỡ {fitStats.dung}%
                    </span>
                    <span className={styles.fitLegendItem}>
                      <span
                        className={styles.legendDot}
                        style={{ backgroundColor: "var(--chart-muted)" }}
                      />
                      Rộng {fitStats.rong}%
                    </span>
                  </div>
                </div>

                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đánh giá có ảnh & số đo</span>
                  <span className={common.balanceValue}>
                    {formatInt(photoReviewsCount)} / {formatInt(reviews.length)}
                  </span>
                  <span className={common.balanceCaption}>
                    Giúp người mua mới đối chiếu phom chuẩn hơn
                  </span>
                </div>
              </section>

              <section className={common.card} aria-labelledby="reviews-list-heading">
                <div className={common.cardHeader}>
                  <h2 id="reviews-list-heading" className={common.cardTitle}>
                    Danh sách đánh giá từ đơn hoàn tất
                  </h2>
                  <div className={common.segmented} role="tablist" aria-label="Lọc đánh giá">
                    {(
                      [
                        ["all", `Tất cả (${reviews.length})`],
                        ["unreplied", `Chưa trả lời (${unrepliedReviewsCount})`],
                        ["photo", `Có ảnh (${photoReviewsCount})`],
                        ["fit_issue", "Chật / Rộng"],
                      ] as const
                    ).map(([k, lbl]) => (
                      <button
                        key={k}
                        type="button"
                        role="tab"
                        aria-selected={reviewFilter === k}
                        className={`${common.segmentedBtn} ${reviewFilter === k ? common.segmentedBtnActive : ""}`}
                        onClick={() => setReviewFilter(k)}
                      >
                        {lbl}
                      </button>
                    ))}
                  </div>
                </div>

                {filteredReviews.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Không có đánh giá nào thuộc nhóm bộ lọc hiện tại.
                  </div>
                ) : (
                  <div className={styles.reviewList}>
                    {filteredReviews.map((rev) => {
                      const fitInfo = FIT_META[rev.fitFeedback];
                      return (
                        <article key={rev.id} className={styles.reviewItem}>
                          <div className={styles.reviewTop}>
                            <div className={styles.reviewProd}>
                              <img
                                src={rev.productImage}
                                alt={rev.productName}
                                className={styles.reviewThumb}
                                width={40}
                                height={53}
                              />
                              <div>
                                <div className={styles.reviewTitle}>
                                  {rev.productName} · {rev.skuLabel}
                                </div>
                                <div className={styles.reviewMeta}>
                                  {rev.buyerName} ({rev.buyerMeasurements}) · Đơn{" "}
                                  {rev.verifiedOrderCode} · {formatDateTimeVi(rev.createdAt)}
                                </div>
                              </div>
                            </div>
                            <div className={styles.reviewBadges}>
                              <span className={common.pill}>{rev.rating}/5 điểm</span>
                              <span className={fitInfo.cls}>{fitInfo.label}</span>
                              {rev.hasPhoto && <span className={common.pill}>Có ảnh mẫu</span>}
                            </div>
                          </div>

                          <p className={styles.reviewBody}>{rev.comment}</p>

                          {rev.reply ? (
                            <div className={styles.replyBox}>
                              <span className={styles.replyHead}>
                                Phản hồi công khai của {activeShop.shortName} ·{" "}
                                {formatDateTimeVi(rev.reply.repliedAt)}
                              </span>
                              <p className={styles.replyText}>{rev.reply.text}</p>
                            </div>
                          ) : (
                            <div className={styles.composerRow}>
                              <input
                                type="text"
                                className={styles.composerInput}
                                value={reviewDrafts[rev.id] ?? ""}
                                onChange={(e) =>
                                  setReviewDrafts((prev) => ({
                                    ...prev,
                                    [rev.id]: e.target.value,
                                  }))
                                }
                                placeholder="Nhập phản hồi công khai của xưởng (cảm ơn hoặc hướng dẫn đổi size 45 ngày)..."
                                aria-label={`Phản hồi đánh giá của ${rev.buyerName}`}
                              />
                              <Button
                                variant="ink"
                                size="sm"
                                shape="app"
                                onClick={() => handleReplyReview(rev.id)}
                              >
                                Gửi phản hồi
                              </Button>
                            </div>
                          )}
                        </article>
                      );
                    })}
                  </div>
                )}
              </section>
            </>
          )}

          {/* TAB 2: Public Q&A on PDP + Shop Custom FAQs */}
          {tab === "qa" && (
            <>
              <section className={common.card} aria-labelledby="pdp-qa-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="pdp-qa-heading" className={common.cardTitle}>
                      Câu hỏi công khai trên trang sản phẩm
                    </h2>
                    <span className={common.cardMeta}>
                      Nội dung trả lời đi qua bộ lọc từ cấm của sàn trước khi hiển thị công khai
                    </span>
                  </div>
                  <div
                    className={common.segmented}
                    role="tablist"
                    aria-label="Lọc câu hỏi sản phẩm"
                  >
                    <button
                      type="button"
                      role="tab"
                      aria-selected={qaFilter === "all"}
                      className={`${common.segmentedBtn} ${qaFilter === "all" ? common.segmentedBtnActive : ""}`}
                      onClick={() => setQaFilter("all")}
                    >
                      Tất cả ({formatInt(questions.length)})
                    </button>
                    <button
                      type="button"
                      role="tab"
                      aria-selected={qaFilter === "unanswered"}
                      className={`${common.segmentedBtn} ${qaFilter === "unanswered" ? common.segmentedBtnActive : ""}`}
                      onClick={() => setQaFilter("unanswered")}
                    >
                      Chờ trả lời ({formatInt(unansweredQuestionsCount)})
                    </button>
                  </div>
                </div>

                {filteredQuestions.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Toàn bộ câu hỏi trên trang sản phẩm đã được xưởng giải đáp.
                  </div>
                ) : (
                  <div className={styles.reviewList}>
                    {filteredQuestions.map((q) => (
                      <article key={q.id} className={styles.reviewItem}>
                        <div className={styles.reviewTop}>
                          <div>
                            <div className={styles.reviewTitle}>{q.productName}</div>
                            <div className={styles.reviewMeta}>
                              {q.buyerName} hỏi lúc {formatDateTimeVi(q.askedAt)}
                            </div>
                          </div>
                          <span
                            className={
                              q.answer
                                ? `${common.pill} ${common.pillPos}`
                                : `${common.pill} ${common.pillWarn}`
                            }
                          >
                            {q.answer ? "Đã trả lời" : "Chờ xưởng trả lời"}
                          </span>
                        </div>
                        <p className={styles.reviewBody}>{q.question}</p>

                        {q.answer ? (
                          <div className={styles.replyBox}>
                            <span className={styles.replyHead}>
                              Giải đáp từ {activeShop.shortName}
                              {q.answeredAt ? ` · ${formatDateTimeVi(q.answeredAt)}` : ""}
                            </span>
                            <p className={styles.replyText}>{q.answer}</p>
                          </div>
                        ) : (
                          <div className={styles.composerRow}>
                            <input
                              type="text"
                              className={styles.composerInput}
                              value={qaDrafts[q.id] ?? ""}
                              onChange={(e) =>
                                setQaDrafts((prev) => ({ ...prev, [q.id]: e.target.value }))
                              }
                              placeholder="Nhập câu trả lời chi tiết về chất liệu, phom dáng hoặc cách bảo quản..."
                              aria-label={`Trả lời câu hỏi của ${q.buyerName}`}
                            />
                            <Button
                              variant="ink"
                              size="sm"
                              shape="app"
                              onClick={() => handleAnswerQuestion(q.id)}
                            >
                              Đăng trả lời
                            </Button>
                          </div>
                        )}
                      </article>
                    ))}
                  </div>
                )}
              </section>

              <section className={common.card} aria-labelledby="shop-faq-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="shop-faq-heading" className={common.cardTitle}>
                      Bộ câu hỏi thường gặp (FAQ) riêng của {activeShop.shortName}
                    </h2>
                    <span className={common.cardMeta}>
                      Hiển thị trong khối Câu hỏi thường gặp trên trang sản phẩm và trang gian hàng
                    </span>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => setShowFaqForm((prev) => !prev)}
                  >
                    <IconPlus size={14} />
                    <span>{showFaqForm ? "Đóng" : "Soạn FAQ mới"}</span>
                  </Button>
                </div>

                {showFaqForm && (
                  <form className={styles.inlineForm} onSubmit={handleCreateFaq}>
                    <div className={styles.formGrid2}>
                      <div className={common.field}>
                        <label htmlFor="faq-topic" className={common.fieldLabel}>
                          Nhóm chủ đề FAQ
                        </label>
                        <select
                          id="faq-topic"
                          className={common.textInput}
                          value={faqTopic}
                          onChange={(e) => setFaqTopic(e.target.value)}
                        >
                          {FAQ_TOPICS.map((t) => (
                            <option key={t} value={t}>
                              {t}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div className={common.field}>
                        <label htmlFor="faq-q" className={common.fieldLabel}>
                          Câu hỏi thường gặp
                        </label>
                        <input
                          id="faq-q"
                          type="text"
                          className={common.textInput}
                          value={faqQuestion}
                          onChange={(e) => setFaqQuestion(e.target.value)}
                          placeholder="Ví dụ: Áo cotton 220gsm có bị co sau khi sấy máy không?"
                        />
                      </div>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="faq-a" className={common.fieldLabel}>
                        Nội dung giải đáp chuẩn của xưởng
                      </label>
                      <textarea
                        id="faq-a"
                        className={common.textArea}
                        value={faqAnswer}
                        onChange={(e) => setFaqAnswer(e.target.value)}
                        placeholder="Nhập câu trả lời rõ ràng, trung thực theo thực tế sản xuất của xưởng..."
                      />
                    </div>
                    <div className={styles.formActions}>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        onClick={() => setShowFaqForm(false)}
                      >
                        Hủy
                      </Button>
                      <Button type="submit" variant="ink" size="sm" shape="app">
                        Lưu vào bộ FAQ
                      </Button>
                    </div>
                  </form>
                )}

                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Chủ đề</th>
                        <th>Câu hỏi & Giải đáp</th>
                        <th>Cập nhật</th>
                        <th className={common.numCol}>Hiển thị</th>
                      </tr>
                    </thead>
                    <tbody>
                      {faqs.map((f) => (
                        <tr key={f.id}>
                          <td>
                            <span className={common.pill}>{f.topic}</span>
                          </td>
                          <td>
                            <strong>{f.question}</strong>
                            <span className={common.cellSub}>{f.answer}</span>
                          </td>
                          <td>{f.updatedAt}</td>
                          <td className={common.numCol}>
                            <input
                              type="checkbox"
                              className={common.toggleSwitch}
                              checked={f.active}
                              onChange={(e) => {
                                toggleSellerFaqActive(f.id, e.target.checked);
                                showToast("Đã cập nhật hiển thị mục FAQ.");
                              }}
                              aria-label={`Hiển thị FAQ ${f.question}`}
                            />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {faqs.map((f) => (
                    <div key={f.id} className={common.mobileRow}>
                      <div className={common.mobileRowTop}>
                        <div>
                          <strong>{f.question}</strong>
                          <span className={common.cellSub}>
                            {f.topic} · {f.updatedAt}
                          </span>
                        </div>
                        <input
                          type="checkbox"
                          className={common.toggleSwitch}
                          checked={f.active}
                          onChange={(e) => toggleSellerFaqActive(f.id, e.target.checked)}
                          aria-label={`Hiển thị FAQ ${f.question}`}
                        />
                      </div>
                      <p className={styles.replyText}>{f.answer}</p>
                    </div>
                  ))}
                </div>
              </section>
            </>
          )}

          {/* TAB 3: Lookbook & Editorial Posts waiting Admin review */}
          {tab === "lookbook" && (
            <>
              <div
                className={common.countCards4}
                role="tablist"
                aria-label="Lọc trạng thái bài viết"
              >
                {(
                  [
                    ["all", "Tất cả bài viết", lbCounts.all],
                    ["da_dang", "Đã đăng trên sàn", lbCounts.da_dang],
                    ["cho_duyet", "Chờ Admin duyệt", lbCounts.cho_duyet],
                    ["nhap", "Bản nháp", lbCounts.nhap],
                  ] as const
                ).map(([k, lbl, cnt]) => (
                  <button
                    key={k}
                    type="button"
                    role="tab"
                    aria-selected={lbFilter === k}
                    className={`${common.countCard} ${lbFilter === k ? common.countCardSelected : ""}`}
                    onClick={() => setLbFilter(k)}
                  >
                    <span className={common.countCardLabel}>{lbl}</span>
                    <span className={common.countCardValue}>{formatInt(cnt)}</span>
                  </button>
                ))}
              </div>

              <div
                className={common.chipScroller}
                role="tablist"
                aria-label="Lọc trạng thái bài viết"
              >
                {(
                  [
                    ["all", "Tất cả", lbCounts.all],
                    ["da_dang", "Đã đăng", lbCounts.da_dang],
                    ["cho_duyet", "Chờ duyệt", lbCounts.cho_duyet],
                    ["nhap", "Nháp", lbCounts.nhap],
                  ] as const
                ).map(([k, lbl, cnt]) => (
                  <button
                    key={k}
                    type="button"
                    role="tab"
                    aria-selected={lbFilter === k}
                    className={`${common.chipBtn} ${lbFilter === k ? common.chipBtnActive : ""}`}
                    onClick={() => setLbFilter(k)}
                  >
                    <span>{lbl}</span>
                    <span className={common.chipCount}>{formatInt(cnt)}</span>
                  </button>
                ))}
              </div>

              <section className={common.card} aria-labelledby="lookbook-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="lookbook-heading" className={common.cardTitle}>
                      Lookbook & Bài viết phối đồ gắn SKU
                    </h2>
                    <span className={common.cardMeta}>
                      Bài viết được duyệt sẽ hiển thị trên Sổ tay vải & Phối đồ kèm thẻ Mua ngay
                    </span>
                  </div>
                </div>

                {showLbForm && (
                  <div className={styles.inlineForm}>
                    <div className={styles.formGrid2}>
                      <div className={common.field}>
                        <label htmlFor="lb-title" className={common.fieldLabel}>
                          Tiêu đề bài Lookbook / Phối đồ
                        </label>
                        <input
                          id="lb-title"
                          type="text"
                          className={common.textInput}
                          value={lbTitle}
                          onChange={(e) => setLbTitle(e.target.value)}
                          placeholder="Ví dụ: Phối 2 áo thun Cotton Compact cho lịch làm việc 5 ngày"
                        />
                      </div>
                      <div className={common.field}>
                        <label htmlFor="lb-cat" className={common.fieldLabel}>
                          Chuyên mục tạp chí sàn
                        </label>
                        <select
                          id="lb-cat"
                          className={common.textInput}
                          value={lbCategory}
                          onChange={(e) => setLbCategory(e.target.value)}
                        >
                          {LOOKBOOK_CATEGORIES.map((cat) => (
                            <option key={cat} value={cat}>
                              {cat}
                            </option>
                          ))}
                        </select>
                      </div>
                    </div>

                    <div className={common.field}>
                      <label htmlFor="lb-excerpt" className={common.fieldLabel}>
                        Tóm tắt & gợi ý phối đồ
                      </label>
                      <textarea
                        id="lb-excerpt"
                        className={common.textArea}
                        value={lbExcerpt}
                        onChange={(e) => setLbExcerpt(e.target.value)}
                        placeholder="Mô tả ngắn cách phối màu, chọn cỡ và hoàn cảnh mặc..."
                      />
                    </div>

                    <div className={common.field}>
                      <span className={common.fieldLabel}>
                        Gắn thẻ sản phẩm của {activeShop.shortName} nhúng trong bài
                      </span>
                      <div className={styles.skuChipGrid}>
                        {shopProducts.map((p) => {
                          const selected = lbTaggedIds.includes(p.id);
                          return (
                            <button
                              key={p.id}
                              type="button"
                              className={`${styles.skuChip} ${selected ? styles.skuChipActive : ""}`}
                              aria-pressed={selected}
                              onClick={() => toggleLbProduct(p.id)}
                            >
                              {p.shortName}
                            </button>
                          );
                        })}
                      </div>
                    </div>

                    <div className={styles.formActions}>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        onClick={() => setShowLbForm(false)}
                      >
                        Hủy
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        onClick={() => handleCreateLookbook("nhap")}
                      >
                        Lưu bản nháp
                      </Button>
                      <Button
                        variant="ink"
                        size="sm"
                        shape="app"
                        onClick={() => handleCreateLookbook("cho_duyet")}
                      >
                        Gửi Admin duyệt đăng
                      </Button>
                    </div>
                  </div>
                )}

                {filteredLookbooks.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Chưa có bài Lookbook nào ở trạng thái này.
                  </div>
                ) : (
                  <div className={styles.reviewList}>
                    {filteredLookbooks.map((lb) => {
                      const st = lbStatusPill(lb.status);
                      return (
                        <article key={lb.id} className={styles.reviewItem}>
                          <div className={styles.reviewTop}>
                            <div className={styles.reviewProd}>
                              <img
                                src={lb.coverImage}
                                alt={lb.title}
                                className={styles.reviewThumb}
                                width={40}
                                height={53}
                              />
                              <div>
                                <div className={styles.reviewTitle}>{lb.title}</div>
                                <div className={styles.reviewMeta}>
                                  {lb.category} · Gửi ngày {lb.submittedAt}
                                  {lb.views > 0 ? ` · ${formatInt(lb.views)} lượt xem` : ""}
                                </div>
                              </div>
                            </div>
                            <div className={styles.reviewBadges}>
                              <span className={st.cls}>{st.label}</span>
                              {lb.status === "nhap" && (
                                <button
                                  type="button"
                                  className={styles.actionBtnSm}
                                  onClick={() => {
                                    submitSellerLookbookForReview(lb.id);
                                    showToast(`Đã gửi bài "${lb.title}" chờ Admin duyệt.`);
                                  }}
                                >
                                  Gửi duyệt
                                </button>
                              )}
                            </div>
                          </div>

                          <p className={styles.reviewBody}>{lb.excerpt}</p>

                          <div className={styles.taggedSkuRow}>
                            {lb.taggedProductIds.map((pid) => {
                              const p = shopProducts.find((item) => item.id === pid);
                              return (
                                <Link
                                  key={pid}
                                  href={`/seller/san-pham/${pid}`}
                                  className={styles.taggedSkuPill}
                                >
                                  SKU gắn thẻ: {p?.shortName ?? pid}
                                </Link>
                              );
                            })}
                          </div>

                          {lb.adminNote && (
                            <span className={common.cardMeta}>
                              Ghi chú biên tập: {lb.adminNote}
                            </span>
                          )}
                        </article>
                      );
                    })}
                  </div>
                )}
              </section>
            </>
          )}

          {/* TAB 4: Back-in-stock subscribers & Wishlist ranking */}
          {tab === "waiting" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp chờ hàng và yêu thích">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Khách đăng ký Báo có hàng</span>
                  <span
                    className={`${common.balanceValue} ${totalWaitingCustomers === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(totalWaitingCustomers)}
                  </span>
                  <span className={common.balanceCaption}>
                    Tự động gửi thông báo khi xưởng nhập tồn &gt; 0
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Tổng lượt yêu thích gian hàng</span>
                  <span className={common.balanceValue}>{formatInt(totalWishlists)}</span>
                  <span className={common.balanceCaption}>
                    Trên {formatInt(shopProducts.length)} sản phẩm đang mở bán
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>SKU ưu tiên nhập kho</span>
                  <span className={common.balanceValue}>
                    {formatInt(backInStockRows.filter((r) => r.waitingCount > 0).length)}
                  </span>
                  <span className={common.balanceCaption}>
                    Các biến thể có khách chờ mua lại ngay
                  </span>
                </div>
              </section>

              <div className={styles.waitingGrid}>
                <section className={common.card} aria-labelledby="bis-heading">
                  <div className={common.cardHeader}>
                    <h2 id="bis-heading" className={common.cardTitle}>
                      SKU có khách đăng ký Báo có hàng
                    </h2>
                    <span className={common.cardMeta}>Xếp theo lượng khách chờ cao nhất</span>
                  </div>

                  {backInStockRows.length === 0 ? (
                    <div className={common.inlineEmpty}>
                      Mọi biến thể đều đủ tồn kho và không có khách đang chờ báo hàng.
                    </div>
                  ) : (
                    <>
                      <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                        <table className={common.dataTable}>
                          <thead>
                            <tr>
                              <th>Sản phẩm & biến thể</th>
                              <th className={common.numCol}>Tồn kho</th>
                              <th className={common.numCol}>Khách chờ</th>
                              <th className={common.numCol}>Thao tác</th>
                            </tr>
                          </thead>
                          <tbody>
                            {backInStockRows.map((row) => (
                              <tr key={row.skuCode}>
                                <td>
                                  <div className={styles.reviewProd}>
                                    <img
                                      src={row.productImage}
                                      alt={row.productName}
                                      className={styles.reviewThumb}
                                      width={34}
                                      height={45}
                                    />
                                    <div>
                                      <Link
                                        href={`/seller/san-pham/${row.productId}`}
                                        className={common.cellLink}
                                      >
                                        {row.productName}
                                      </Link>
                                      <span className={common.cellSub}>
                                        {row.colorName} / Size {row.size} · {row.skuCode}
                                      </span>
                                    </div>
                                  </div>
                                </td>
                                <td className={common.numCol}>
                                  <span className={row.stock === 0 ? common.deltaNeg : ""}>
                                    {formatInt(row.stock)}
                                  </span>
                                </td>
                                <td className={common.numCol}>
                                  <strong>{formatInt(row.waitingCount)}</strong>
                                </td>
                                <td className={common.numCol}>
                                  <button
                                    type="button"
                                    className={styles.actionBtnSm}
                                    onClick={() => handleQuickRestock(row.productId, row.skuCode)}
                                  >
                                    {row.waitingCount > 0 ? "Nhập +20 & báo khách" : "Nhập +20"}
                                  </button>
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>

                      <div className={common.mobileList}>
                        {backInStockRows.map((row) => (
                          <div key={row.skuCode} className={common.mobileRow}>
                            <div className={common.mobileRowTop}>
                              <div>
                                <strong>
                                  {row.productName} · {row.colorName} / {row.size}
                                </strong>
                                <span className={common.cellSub}>
                                  Tồn {formatInt(row.stock)} · {formatInt(row.waitingCount)} khách
                                  chờ
                                </span>
                              </div>
                              <button
                                type="button"
                                className={styles.actionBtnSm}
                                onClick={() => handleQuickRestock(row.productId, row.skuCode)}
                              >
                                +20 & báo
                              </button>
                            </div>
                          </div>
                        ))}
                      </div>
                    </>
                  )}
                </section>

                <section className={common.card} aria-labelledby="wish-rank-heading">
                  <div className={common.cardHeader}>
                    <h2 id="wish-rank-heading" className={common.cardTitle}>
                      Lượt yêu thích theo mẫu
                    </h2>
                    <span className={common.cardMeta}>Theo dõi nhu cầu mua lặp</span>
                  </div>

                  <div>
                    {wishlistRanking.map((item, idx) => (
                      <div key={item.productId} className={styles.wishRow}>
                        <div className={styles.wishTop}>
                          <Link
                            href={`/seller/san-pham/${item.productId}`}
                            className={common.cellLink}
                          >
                            {item.shortName}
                          </Link>
                          <span className={styles.wishCount}>
                            {formatInt(item.wishlistCount)} lượt thích
                          </span>
                        </div>
                        <span className={common.cardMeta}>
                          {item.categoryName} · {formatVnd(item.price)}
                          {item.waitingCount > 0
                            ? ` · ${formatInt(item.waitingCount)} khách chờ hàng`
                            : ""}
                        </span>
                        <div className={styles.rankBarTrack}>
                          <div
                            className={idx === 0 ? styles.rankBarFillTop : styles.rankBarFill}
                            style={{
                              width: `${Math.max(6, Math.round((item.wishlistCount / maxWishlist) * 100))}%`,
                            }}
                          />
                        </div>
                      </div>
                    ))}
                  </div>
                </section>
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}

export default function SellerInteractionPage() {
  return (
    <SellerSuspense>
      <SellerInteractionContent />
    </SellerSuspense>
  );
}
