"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { useMockStore } from "@/mock/MockStoreContext";
import { IconClose, NexusMark } from "./Icons";
import styles from "./ShopFooter.module.css";

const PAYMENT_METHODS = ["COD thanh toán khi nhận", "Chuyển khoản VietQR", "Ví điện tử"];

const SHIPPING_METHODS = [
  "Giao tiêu chuẩn toàn quốc",
  "Giao nhanh nội thành",
  "Đồng kiểm khi nhận hàng",
];

export function ShopFooter() {
  const { cookiePrefs, updateCookiePrefs, cookieModalOpen, openCookieModal, closeCookieModal } =
    useMockStore();

  const [draftAnalytics, setDraftAnalytics] = useState(cookiePrefs.analytics);
  const [draftPersonalization, setDraftPersonalization] = useState(cookiePrefs.personalization);

  const [newsletterEmail, setNewsletterEmail] = useState("");
  const [newsletterError, setNewsletterError] = useState<string | null>(null);
  const [subscribedEmail, setSubscribedEmail] = useState<string | null>(null);

  useEffect(() => {
    if (cookieModalOpen) {
      setDraftAnalytics(cookiePrefs.analytics);
      setDraftPersonalization(cookiePrefs.personalization);
    }
  }, [cookieModalOpen, cookiePrefs.analytics, cookiePrefs.personalization]);

  useEffect(() => {
    if (!cookieModalOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeCookieModal();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [cookieModalOpen, closeCookieModal]);

  const handleSave = () => {
    updateCookiePrefs({
      analytics: draftAnalytics,
      personalization: draftPersonalization,
    });
    closeCookieModal();
  };

  const handleAcceptAll = () => {
    updateCookiePrefs({ analytics: true, personalization: true });
    closeCookieModal();
  };

  const handleEssentialOnly = () => {
    updateCookiePrefs({ analytics: false, personalization: false });
    closeCookieModal();
  };

  const handleNewsletterSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = newsletterEmail.trim();
    if (!clean || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(clean)) {
      setNewsletterError("Vui lòng nhập địa chỉ email hợp lệ.");
      return;
    }
    setNewsletterError(null);
    setSubscribedEmail(clean);
    setNewsletterEmail("");
  };

  return (
    <>
      <footer className={styles.footer}>
        {/* Top band: Brand identity, contact channels & newsletter */}
        <div className={styles.topBand}>
          <div className={styles.topGrid}>
            <div className={styles.brandCol}>
              <Link href="/" className={styles.brandLockup}>
                <NexusMark size={22} accentColor="var(--band-dark-accent)" />
                <span className={styles.brandName}>NexusMart</span>
              </Link>
              <p className={styles.brandLead}>
                Đồ basic may trực tiếp từ các xưởng dệt Việt Nam. Chất liệu rõ định lượng, giá bán
                minh bạch và hỗ trợ trả hàng hoàn tiền trong 7 ngày.
              </p>
              <div className={styles.contactList}>
                <p className={styles.contactItem}>
                  <span className={styles.contactLabel}>Hotline:</span>{" "}
                  <a href="tel:19006845" className={styles.contactValue}>
                    1900 6845
                  </a>
                </p>
                <p className={styles.contactItem}>
                  <span className={styles.contactLabel}>Email:</span>{" "}
                  <a href="mailto:hotro@nexusmart.vn" className={styles.contactValue}>
                    hotro@nexusmart.vn
                  </a>
                </p>
                <p className={styles.contactItem}>
                  <span className={styles.contactLabel}>Giờ làm việc:</span>{" "}
                  <span className={styles.contactValue}>8:30–22:00 (Thứ 2 – Chủ nhật)</span>
                </p>
              </div>
            </div>

            <div className={styles.newsletterCol}>
              <h2 className={styles.newsletterTitle}>Đăng ký nhận tin ưu đãi</h2>
              <p className={styles.newsletterLead}>
                Nhận thông báo đợt mở bán mới, lịch Flash sale trong ngày và mã giảm giá dành riêng
                cho thành viên.
              </p>
              {subscribedEmail ? (
                <div className={styles.newsletterSuccess} role="status" aria-live="polite">
                  <p className={styles.newsletterSuccessText}>
                    Đã đăng ký nhận tin cho <strong>{subscribedEmail}</strong>. Bạn sẽ nhận thông
                    báo ưu đãi mới nhất qua hộp thư này.
                  </p>
                  <button
                    type="button"
                    className={styles.newsletterReset}
                    onClick={() => setSubscribedEmail(null)}
                  >
                    Đăng ký email khác
                  </button>
                </div>
              ) : (
                <form
                  className={styles.newsletterForm}
                  onSubmit={handleNewsletterSubmit}
                  noValidate
                >
                  <label htmlFor="footer-newsletter-email" className="sr-only">
                    Địa chỉ email nhận tin
                  </label>
                  <input
                    id="footer-newsletter-email"
                    type="email"
                    value={newsletterEmail}
                    onChange={(e) => {
                      setNewsletterEmail(e.target.value);
                      if (newsletterError) setNewsletterError(null);
                    }}
                    placeholder="Nhập email của bạn"
                    autoComplete="email"
                    className={styles.newsletterInput}
                  />
                  <button type="submit" className={styles.newsletterBtn}>
                    Đăng ký
                  </button>
                </form>
              )}
              {newsletterError && (
                <p className={styles.newsletterError} role="alert">
                  {newsletterError}
                </p>
              )}
            </div>
          </div>
        </div>

        {/* Middle band: 4 structured storefront columns */}
        <div className={styles.linksBand}>
          <div className={styles.linksGrid}>
            <nav className={styles.linkCol} aria-label="Hỗ trợ khách hàng">
              <h3 className={styles.colTitle}>Hỗ trợ khách hàng</h3>
              <ul className={styles.colList}>
                <li>
                  <Link href="/tro-giup" className={styles.colLink}>
                    Trung tâm trợ giúp
                  </Link>
                </li>
                <li>
                  <Link href="/tra-cuu-don-hang" className={styles.colLink}>
                    Tra cứu đơn hàng
                  </Link>
                </li>
                <li>
                  <Link
                    href="/tap-chi/huong-dan-chon-size-theo-3-dang-nguoi-va-form-xuong"
                    className={styles.colLink}
                  >
                    Bảng chọn size
                  </Link>
                </li>
                <li>
                  <Link href="/tro-giup#doi-tra" className={styles.colLink}>
                    Chính sách đổi trả 7 ngày
                  </Link>
                </li>
              </ul>
            </nav>

            <nav className={styles.linkCol} aria-label="Chính sách">
              <h3 className={styles.colTitle}>Chính sách</h3>
              <ul className={styles.colList}>
                <li>
                  <Link href="/tro-giup#giao-hang" className={styles.colLink}>
                    Chính sách vận chuyển
                  </Link>
                </li>
                <li>
                  <Link href="/tro-giup#bao-mat" className={styles.colLink}>
                    Chính sách bảo mật
                  </Link>
                </li>
                <li>
                  <Link href="/tro-giup#dieu-khoan" className={styles.colLink}>
                    Điều khoản sử dụng
                  </Link>
                </li>
                <li>
                  <button type="button" className={styles.colLinkBtn} onClick={openCookieModal}>
                    Tùy chọn cookie
                  </button>
                </li>
              </ul>
            </nav>

            <nav className={styles.linkCol} aria-label="Về NexusMart">
              <h3 className={styles.colTitle}>Về NexusMart</h3>
              <ul className={styles.colList}>
                <li>
                  <Link href="/ve-nexusmart" className={styles.colLink}>
                    Câu chuyện thương hiệu
                  </Link>
                </li>
                <li>
                  <Link href="/chien-dich/cong-nghe-vai" className={styles.colLink}>
                    Công nghệ vải
                  </Link>
                </li>
                <li>
                  <Link href="/chien-dich/soi-xanh-nexus" className={styles.colLink}>
                    Sợi Xanh Nexus
                  </Link>
                </li>
                <li>
                  <Link href="/tap-chi" className={styles.colLink}>
                    Sổ tay vải
                  </Link>
                </li>
              </ul>
            </nav>

            <nav className={styles.linkCol} aria-label="Bán hàng cùng NexusMart">
              <h3 className={styles.colTitle}>Bán hàng cùng NexusMart</h3>
              <ul className={styles.colList}>
                <li>
                  <Link href="/seller" prefetch={false} className={styles.colLink}>
                    Kênh Người bán
                  </Link>
                </li>
                <li>
                  <Link href="/admin" prefetch={false} className={styles.colLink}>
                    Cổng Quản trị
                  </Link>
                </li>
                <li>
                  <Link href="/ve-nexusmart#xuong-det" className={styles.colLink}>
                    Quy chuẩn xưởng dệt
                  </Link>
                </li>
                <li>
                  <Link href="/seller/ho-so" prefetch={false} className={styles.colLink}>
                    Đăng ký mở gian hàng
                  </Link>
                </li>
              </ul>
            </nav>
          </div>
        </div>

        {/* Bottom band: Payment, shipping & sample legal line */}
        <div className={styles.bottomBand}>
          <div className={styles.serviceRow}>
            <div className={styles.serviceGroup}>
              <p className={styles.serviceLabel}>Phương thức thanh toán</p>
              <div className={styles.serviceChips}>
                {PAYMENT_METHODS.map((label) => (
                  <span key={label} className={styles.serviceChip}>
                    {label}
                  </span>
                ))}
              </div>
            </div>

            <div className={styles.serviceGroup}>
              <p className={styles.serviceLabel}>Đơn vị vận chuyển</p>
              <div className={styles.serviceChips}>
                {SHIPPING_METHODS.map((label) => (
                  <span key={label} className={styles.serviceChip}>
                    {label}
                  </span>
                ))}
              </div>
            </div>
          </div>

          <div className={styles.legalRow}>
            <p className={styles.legalCopy}>
              © 2026 NexusMart · Sàn đồ basic trực tiếp từ xưởng dệt Việt Nam (nội dung mẫu)
            </p>
            <div className={styles.legalLinks}>
              <Link href="/ve-nexusmart#nguon-hinh-anh" className={styles.legalLink}>
                Nguồn ảnh & nội dung mẫu
              </Link>
              <button type="button" className={styles.legalLinkBtn} onClick={openCookieModal}>
                Tùy chọn cookie
              </button>
            </div>
          </div>
        </div>
      </footer>

      {cookieModalOpen && (
        <div
          className={styles.modalBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeCookieModal();
          }}
        >
          <div
            className={styles.modalPanel}
            data-ui="cookie-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="cookie-dialog-title"
          >
            <div className={styles.modalHeader}>
              <h2 id="cookie-dialog-title" className={styles.modalTitle}>
                Tùy chọn cookie
              </h2>
              <button
                type="button"
                className={styles.modalClose}
                onClick={closeCookieModal}
                aria-label="Đóng bảng tùy chọn cookie"
              >
                <IconClose size={18} />
              </button>
            </div>

            <p className={styles.modalLead}>
              Chọn nhóm cookie bạn muốn bật khi mua sắm trên NexusMart. Các lựa chọn được lưu trực
              tiếp trên trình duyệt của bạn.
            </p>

            <div className={styles.cookieList}>
              <div className={styles.cookieRow}>
                <div className={styles.cookieInfo}>
                  <p className={styles.cookieName}>Cookie thiết yếu</p>
                  <p className={styles.cookieDesc}>
                    Giữ giỏ hàng, địa chỉ nhận hàng đã lưu và trạng thái đăng nhập của bạn.
                  </p>
                </div>
                <span className={styles.cookieLocked}>Luôn bật</span>
              </div>

              <label className={styles.cookieRow}>
                <div className={styles.cookieInfo}>
                  <span className={styles.cookieName}>Cải thiện trải nghiệm</span>
                  <span className={styles.cookieDesc}>
                    Giúp tối ưu tốc độ tải trang và gợi ý sản phẩm phù hợp hơn khi tìm kiếm.
                  </span>
                </div>
                <input
                  type="checkbox"
                  checked={draftAnalytics}
                  onChange={(e) => setDraftAnalytics(e.target.checked)}
                  className={styles.cookieCheck}
                />
              </label>

              <label className={styles.cookieRow}>
                <div className={styles.cookieInfo}>
                  <span className={styles.cookieName}>Cá nhân hóa size & lịch sử xem</span>
                  <span className={styles.cookieDesc}>
                    Ghi nhớ số đo gợi ý size, danh sách yêu thích và sản phẩm đã xem gần đây.
                  </span>
                </div>
                <input
                  type="checkbox"
                  checked={draftPersonalization}
                  onChange={(e) => setDraftPersonalization(e.target.checked)}
                  className={styles.cookieCheck}
                />
              </label>
            </div>

            <div className={styles.modalActions}>
              <button type="button" className={styles.btnSecondary} onClick={handleEssentialOnly}>
                Chỉ dùng thiết yếu
              </button>
              <button type="button" className={styles.btnSecondary} onClick={handleSave}>
                Lưu lựa chọn
              </button>
              <button type="button" className={styles.btnPrimary} onClick={handleAcceptAll}>
                Chấp nhận tất cả
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
