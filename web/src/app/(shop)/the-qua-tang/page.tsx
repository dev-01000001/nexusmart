"use client";

import React, { Suspense, useMemo, useState } from "react";
import Image from "next/image";
import { useRouter } from "next/navigation";
import { EDU_DISCOUNT_CONFIG, GIFT_CARD_DENOMINATIONS, GIFT_CARD_POLICY, PRODUCTS } from "@/data";
import { MEDIA_BANNERS } from "@/data/media";
import { formatDateVi, formatVnd } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, ProductCard, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

function GiftCardsPageContent() {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { setPlatformVoucher } = useCart();
  const { membership, purchaseGiftCard, applyGiftCardCode, verifyEduEmail, showToast } =
    useMockStore();

  const [amountVnd, setAmountVnd] = useState<number>(500000);
  const [customCode, setCustomCode] = useState("QUATANG26");
  const [recipientEmail, setRecipientEmail] = useState("");
  const [message, setMessage] = useState(
    "Chúc bạn chọn được bộ đồ basic thật vừa ý trên NexusMart."
  );

  const [redeemCode, setRedeemCode] = useState("NEXUS-GIFT100");
  const [redeemResult, setRedeemResult] = useState<{ ok: boolean; message: string } | null>(null);

  const [eduEmailInput, setEduEmailInput] = useState(membership.eduEmail ?? "");
  const [eduResult, setEduResult] = useState<{ ok: boolean; message: string } | null>(null);

  useRevealOnScroll(
    `${viewState ?? "live"}-${membership.giftCards.length}-${membership.eduVerified}`
  );

  const suggestedProducts = useMemo(() => PRODUCTS.slice(6, 10), []);

  const cleanedCustom = customCode
    .toUpperCase()
    .replace(/[^A-Z0-9-]/g, "")
    .slice(0, GIFT_CARD_POLICY.customCodeMaxLen);

  const previewCode =
    cleanedCustom.length >= GIFT_CARD_POLICY.customCodeMinLen
      ? `NEXUS-${cleanedCustom}`
      : "NEXUS-GC2026";

  const handleIssueCard = (e: React.FormEvent) => {
    e.preventDefault();
    const created = purchaseGiftCard({
      amountVnd,
      customCode: cleanedCustom,
      recipientEmail: recipientEmail || undefined,
      message: message || undefined,
    });
    setRedeemCode(created.code);
  };

  const handleRedeem = (e: React.FormEvent) => {
    e.preventDefault();
    const res = applyGiftCardCode(redeemCode);
    setRedeemResult(res);
  };

  const handleVerifyEdu = (e: React.FormEvent) => {
    e.preventDefault();
    const res = verifyEduEmail(eduEmailInput);
    setEduResult(res);
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Thẻ quà tặng Nexus
            </span>
          </span>
        </h1>
        <p className={styles.leadCopy} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/gio-hang">
          Mở giỏ hàng
        </Button>
        <Button variant="secondary" size="md" href="/hoi-vien">
          Hội viên Nexus
        </Button>
      </div>
    </section>
  );

  const renderSuggestedShelf = (title: string, subtitle?: string) => (
    <section className={`${shared.section} ${shared.sectionGap}`} data-reveal="">
      <div className={shared.sectionHead}>
        <h2 className={shared.sectionTitle}>{title}</h2>
        {subtitle && <p className={styles.shelfSub}>{subtitle}</p>}
      </div>
      <div className={styles.productGrid}>
        {suggestedProducts.map((product) => (
          <div key={product.id} data-reveal="">
            <ProductCard product={product} />
          </div>
        ))}
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang tải danh mục mệnh giá thẻ quà tặng và ưu đãi sinh viên.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Phát hành thẻ quà tặng</h2>
          <StateView kind="loading" title="Đang tải thông tin thẻ quà tặng..." rows={4} />
        </section>
        {renderSuggestedShelf("Gợi ý mua bằng thẻ quà tặng")}
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        {renderHeader("Dịch vụ phát hành thẻ quà tặng đang tạm gián đoạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái dịch vụ</h2>
          <StateView
            kind="error"
            title="Chưa thể kết nối hệ thống thẻ quà tặng"
            message="Các thẻ đã phát hành vẫn giữ nguyên số dư. Vui lòng thử tải lại sau ít giây."
            actionLabel="Tải lại trang"
            actionHref="/the-qua-tang"
          />
        </section>
        {renderSuggestedShelf("Đồ cơ bản bán chạy")}
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        {renderHeader("Chưa có thẻ quà tặng nào trong tài khoản của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Kho thẻ quà tặng</h2>
          <StateView
            kind="empty"
            title="Bạn chưa phát hành hoặc lưu thẻ quà tặng nào"
            message="Chọn mệnh giá từ 100.000₫ đến 2.000.000₫ để tạo mã quà tặng dùng trên toàn sàn."
            actionLabel="Tạo thẻ quà tặng ngay"
            actionHref="/the-qua-tang"
          />
        </section>
        {renderSuggestedShelf("Sản phẩm dùng thẻ quà tặng")}
      </div>
    );
  }

  return (
    <div className={shared.page}>
      {renderHeader(
        "Tặng thẻ mua sắm dùng trên cả 4 xưởng dệt, tùy chọn lời chúc riêng và nhận ưu đãi sinh viên .edu."
      )}

      {/* Primary surface: Gift card builder + Dark live preview & unboxed redeem */}
      <section className={shared.section}>
        <div className={styles.twoColGrid}>
          <form
            className={styles.builderPanel}
            onSubmit={handleIssueCard}
            noValidate
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.panelHead}>
              <h2 className={shared.sectionTitle}>Tạo & Phát hành thẻ quà tặng</h2>
              <p className={styles.bodyCopy}>{GIFT_CARD_POLICY.note}</p>
            </div>

            <div className={shared.field} data-rise="" style={{ "--i": 3 } as React.CSSProperties}>
              <span className={shared.fieldLabel}>Chọn mệnh giá thẻ</span>
              <div className={styles.denomGrid} role="radiogroup" aria-label="Chọn mệnh giá thẻ">
                {GIFT_CARD_DENOMINATIONS.map((val) => {
                  const active = amountVnd === val;
                  return (
                    <button
                      key={val}
                      type="button"
                      role="radio"
                      aria-checked={active}
                      className={`${styles.denomBtn} ${active ? styles.denomBtnActive : ""} num`}
                      onClick={() => setAmountVnd(val)}
                    >
                      {formatVnd(val)}
                    </button>
                  );
                })}
              </div>
            </div>

            <div
              className={shared.formGrid}
              data-rise=""
              style={{ "--i": 4 } as React.CSSProperties}
            >
              <div className={shared.field}>
                <label htmlFor="gc-custom-code" className={shared.fieldLabel}>
                  Tùy biến mã ký tự ({GIFT_CARD_POLICY.customCodeMinLen}–
                  {GIFT_CARD_POLICY.customCodeMaxLen} ký tự)
                </label>
                <input
                  id="gc-custom-code"
                  type="text"
                  className={shared.input}
                  value={customCode}
                  onChange={(e) => setCustomCode(e.target.value)}
                  placeholder="SINHNHAT26"
                />
                <span className={shared.fieldHint}>
                  Mã dự kiến: <strong className="num">{previewCode}</strong>
                </span>
              </div>

              <div className={shared.field}>
                <label htmlFor="gc-recipient" className={shared.fieldLabel}>
                  Email người nhận (không bắt buộc)
                </label>
                <input
                  id="gc-recipient"
                  type="email"
                  className={shared.input}
                  value={recipientEmail}
                  onChange={(e) => setRecipientEmail(e.target.value)}
                  placeholder="banbe@email.vn"
                />
              </div>

              <div className={`${shared.field} ${shared.formFull}`}>
                <label htmlFor="gc-message" className={shared.fieldLabel}>
                  Lời chúc gửi kèm thẻ
                </label>
                <textarea
                  id="gc-message"
                  className={shared.textarea}
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder="Viết lời nhắn gửi đến người nhận thẻ quà tặng..."
                />
              </div>
            </div>

            <div
              className={styles.policyMetaRow}
              data-rise=""
              style={{ "--i": 5 } as React.CSSProperties}
            >
              <span>Hạn dùng {GIFT_CARD_POLICY.validityDays} ngày</span>
              <span>Chia nhỏ nhiều lần mua cho đến khi hết số dư</span>
            </div>

            <div data-rise="" style={{ "--i": 6 } as React.CSSProperties}>
              <Button
                type="submit"
                variant="primary"
                size="lg"
                fullWidth
                className={shared.wrapBtn}
              >
                Phát hành thẻ quà tặng · {formatVnd(amountVnd)}
              </Button>
            </div>
          </form>

          {/* Right column: Dark Gift Card Preview + Unboxed Redeem Section */}
          <div className={styles.previewCol}>
            <article
              className={styles.cardPreview}
              aria-label="Xem trước thẻ quà tặng"
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <Image
                src={MEDIA_BANNERS.fabricCloseup.src}
                alt={MEDIA_BANNERS.fabricCloseup.alt}
                width={MEDIA_BANNERS.fabricCloseup.width}
                height={MEDIA_BANNERS.fabricCloseup.height}
                sizes="(max-width: 1024px) 100vw, 44vw"
                priority
                className={styles.cardPreviewPhoto}
              />
              <div className={styles.cardPreviewBody}>
                <div className={styles.cardPreviewTop}>
                  <p className={`${styles.cardValueBig} num`}>{formatVnd(amountVnd)}</p>
                  <span className={styles.cardExpiry}>
                    Hạn dùng {GIFT_CARD_POLICY.validityDays} ngày
                  </span>
                </div>
                <p className={styles.cardBrand}>Thẻ quà tặng toàn sàn NexusMart</p>
                <div className={styles.cardCodeBar}>
                  <span>Mã thẻ</span>
                  <strong className="num">{previewCode}</strong>
                </div>
                <p className={styles.cardMessagePreview}>
                  {message.trim() || "Dùng thanh toán chéo cả 4 xưởng dệt trên NexusMart."}
                </p>
              </div>
            </article>

            <form
              className={styles.redeemSection}
              onSubmit={handleRedeem}
              noValidate
              data-rise=""
              style={{ "--i": 4 } as React.CSSProperties}
            >
              <h2 className={shared.sectionTitle}>Áp mã thẻ quà tặng vào giỏ</h2>
              <p className={styles.bodyCopy}>
                Nhập mã thẻ hiện có (ví dụ: <strong>NEXUS-GIFT100</strong> hoặc{" "}
                <strong>NEXUS-BANBE200</strong>) để trừ trực tiếp vào tổng đơn hàng.
              </p>
              <div className={shared.codeInputRow}>
                <input
                  type="text"
                  className={shared.input}
                  value={redeemCode}
                  onChange={(e) => setRedeemCode(e.target.value)}
                  placeholder="NEXUS-GIFT100"
                  aria-label="Mã thẻ quà tặng cần áp vào giỏ"
                />
                <Button type="submit" variant="ink" size="md">
                  Áp vào giỏ
                </Button>
              </div>
              {redeemResult && (
                <div
                  className={`${shared.feedbackBox} ${
                    redeemResult.ok ? shared.feedbackSuccess : shared.feedbackError
                  }`}
                  role={redeemResult.ok ? "status" : "alert"}
                >
                  {redeemResult.message}
                  {redeemResult.ok && (
                    <>
                      {" "}
                      <button
                        type="button"
                        className={shared.breadcrumbLink}
                        onClick={() => router.push("/gio-hang")}
                      >
                        Mở giỏ hàng ngay
                      </button>
                    </>
                  )}
                </div>
              )}
            </form>
          </div>
        </div>
      </section>

      {/* Issued Gift Cards (unboxed list) + Student .edu Verification (accent tint field) */}
      <section className={`${shared.section} ${shared.sectionGap}`}>
        <div className={styles.twoColGrid}>
          <div className={styles.issuedSection} data-reveal="">
            <div className={shared.sectionHead}>
              <h2 className={shared.sectionTitle}>
                Thẻ quà tặng đã phát hành ({membership.giftCards.length})
              </h2>
              <span className={shared.sectionNote}>Dùng trên mọi gian hàng</span>
            </div>

            <div className={styles.issuedList}>
              {membership.giftCards.map((card) => {
                const isApplied = membership.appliedGiftCardCode === card.code;
                return (
                  <article key={card.id} className={styles.issuedRow} data-reveal="">
                    <div className={styles.issuedInfo}>
                      <div className={styles.issuedCodeLine}>
                        <span className={`${styles.issuedCode} num`}>{card.code}</span>
                        <span
                          className={`${shared.statusTag} ${
                            card.remainingVnd > 0 ? shared.tonePos : shared.toneNeutral
                          }`}
                        >
                          {card.status === "kha_dung"
                            ? "Khả dụng"
                            : card.status === "da_dung_mot_phan"
                              ? "Đã dùng một phần"
                              : "Đã dùng hết"}
                        </span>
                      </div>
                      <p className={`${styles.issuedBalance} num`}>
                        Còn lại <strong>{formatVnd(card.remainingVnd)}</strong> /{" "}
                        {formatVnd(card.amountVnd)}
                        <span className={styles.issuedExpiry}>
                          {" "}
                          · HSD {formatDateVi(card.expiresAt)}
                        </span>
                      </p>
                      {card.message && <p className={styles.bodyCopy}>{card.message}</p>}
                    </div>

                    <div className={shared.formActions}>
                      <Button
                        variant={isApplied ? "primary" : "secondary"}
                        size="sm"
                        disabled={card.remainingVnd <= 0}
                        onClick={() => {
                          applyGiftCardCode(isApplied ? null : card.code);
                          if (!isApplied) router.push("/gio-hang");
                        }}
                      >
                        {isApplied ? "Đang áp dụng trong giỏ" : "Áp vào giỏ hàng"}
                      </Button>
                    </div>
                  </article>
                );
              })}
            </div>
          </div>

          {/* Student .edu / .edu.vn Discount Verification */}
          <form className={styles.eduPanel} onSubmit={handleVerifyEdu} noValidate data-reveal="">
            <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
              <h2 className={shared.sectionTitle}>Ưu đãi sinh viên (.edu / .edu.vn)</h2>
              <span
                className={`${shared.statusTag} ${
                  membership.eduVerified ? shared.tonePos : shared.toneNeutral
                }`}
              >
                {membership.eduVerified ? "Đã xác thực" : `Mã ${EDU_DISCOUNT_CONFIG.voucherCode}`}
              </span>
            </div>

            <p className={styles.bodyCopy}>{EDU_DISCOUNT_CONFIG.note}</p>

            <div className={styles.eduPerks}>
              <p className={styles.eduPerkLine}>
                Giảm ngay <strong className="num">{EDU_DISCOUNT_CONFIG.discountPercent}%</strong>{" "}
                (tối đa{" "}
                <strong className="num">{formatVnd(EDU_DISCOUNT_CONFIG.maxDiscountVnd)}</strong>)
              </p>
              <p className={styles.eduPerkLine}>
                Áp dụng cho đơn từ{" "}
                <strong className="num">{formatVnd(EDU_DISCOUNT_CONFIG.minOrderVnd)}</strong>, dùng
                cùng mã gian hàng và freeship.
              </p>
            </div>

            <div className={shared.field}>
              <label htmlFor="edu-email" className={shared.fieldLabel}>
                Email trường đại học / cao đẳng (.edu hoặc .edu.vn)
              </label>
              <div className={shared.codeInputRow}>
                <input
                  id="edu-email"
                  type="email"
                  className={shared.input}
                  value={eduEmailInput}
                  onChange={(e) => setEduEmailInput(e.target.value)}
                  placeholder="minhquan@sv.hcmut.edu.vn"
                />
                <Button type="submit" variant="primary" size="md">
                  Xác thực .edu
                </Button>
              </div>
            </div>

            {eduResult && (
              <p
                className={`${shared.feedbackBox} ${
                  eduResult.ok ? shared.feedbackSuccess : shared.feedbackError
                }`}
                role={eduResult.ok ? "status" : "alert"}
              >
                {eduResult.message}
              </p>
            )}

            {membership.eduVerified && (
              <div className={shared.formActions}>
                <Button
                  type="button"
                  variant="ink"
                  size="md"
                  onClick={() => {
                    setPlatformVoucher(EDU_DISCOUNT_CONFIG.voucherCode);
                    showToast("Đã chọn mã sinh viên EDU15 cho giỏ hàng.");
                    router.push("/gio-hang");
                  }}
                >
                  Dùng ngay mã EDU15 trong giỏ hàng
                </Button>
              </div>
            )}
          </form>
        </div>
      </section>

      {renderSuggestedShelf(
        "Gợi ý quà tặng đồ cơ bản dễ chọn size",
        "Đổi size hai chiều miễn phí 45 ngày nếu người nhận cần đổi cỡ"
      )}
    </div>
  );
}

export default function GiftCardsPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải trang Thẻ quà tặng..." rows={4} />
        </div>
      }
    >
      <GiftCardsPageContent />
    </Suspense>
  );
}
