import React from "react";
import Link from "next/link";
import { NexusMark } from "@/components/Icons";
import styles from "./PlatformFooter.module.css";

const CAPABILITY_CHIPS = [
  "Tên miền phụ riêng",
  "Thanh toán COD",
  "Chuyển khoản VietQR",
  "Giao hàng 34 tỉnh thành",
  "Đối soát sổ cái VND",
];

export function PlatformFooter() {
  return (
    <footer className={styles.footer}>
      <div className={styles.topBand}>
        <div className={styles.topGrid}>
          <div className={styles.brandCol}>
            <Link href="/" className={styles.brandLockup}>
              <NexusMark size={22} accentColor="var(--band-dark-accent)" />
              <span className={styles.brandName}>NexusMart</span>
            </Link>
            <p className={styles.brandLead}>
              Nền tảng thương mại điện tử cho phép từng thương hiệu và xưởng sản xuất tự vận hành
              cửa hàng trực tuyến trên tên miền phụ riêng, độc lập từ giao diện đến đơn hàng.
            </p>
            <div className={styles.contactList}>
              <p className={styles.contactItem}>
                <span className={styles.contactLabel}>Hỗ trợ nền tảng:</span>{" "}
                <a href="mailto:hotro@nexusmart.vn" className={styles.contactValue}>
                  hotro@nexusmart.vn
                </a>
              </p>
              <p className={styles.contactItem}>
                <span className={styles.contactLabel}>Khung giờ tiếp nhận:</span>{" "}
                <span className={styles.contactValue}>8:30 - 22:00 hằng ngày</span>
              </p>
            </div>
          </div>

          <div className={styles.startCol}>
            <h2 className={styles.startTitle}>Đưa thương hiệu của bạn lên cửa hàng riêng</h2>
            <p className={styles.startLead}>
              Đăng ký tài khoản người bán, thiết kế giao diện cửa hàng và bắt đầu bán hàng trên địa
              chỉ riêng của bạn.
            </p>
            <div className={styles.startActions}>
              <Link href="/dang-ky?role=seller" className={styles.startPrimary}>
                Mở gian hàng
              </Link>
              <Link href="/dang-nhap" className={styles.startSecondary}>
                Đăng nhập Kênh người bán
              </Link>
            </div>
          </div>
        </div>
      </div>

      <div className={styles.linksBand}>
        <div className={styles.linksGrid}>
          <nav className={styles.linkCol} aria-label="Khám phá nền tảng">
            <h3 className={styles.colTitle}>Nền tảng</h3>
            <ul className={styles.colList}>
              <li>
                <Link href="/" className={styles.colLink}>
                  Trang chủ NexusMart
                </Link>
              </li>
              <li>
                <Link href="/#danh-ba-gian-hang" className={styles.colLink}>
                  Danh bạ gian hàng
                </Link>
              </li>
              <li>
                <Link href="/#danh-cho-nguoi-ban" className={styles.colLink}>
                  Giải pháp cho người bán
                </Link>
              </li>
              <li>
                <Link href="/#cach-van-hanh" className={styles.colLink}>
                  Quy trình mở gian hàng
                </Link>
              </li>
            </ul>
          </nav>

          <nav className={styles.linkCol} aria-label="Dành cho người bán và vận hành">
            <h3 className={styles.colTitle}>Người bán &amp; Quản trị</h3>
            <ul className={styles.colList}>
              <li>
                <Link href="/dang-ky?role=seller" className={styles.colLink}>
                  Đăng ký mở gian hàng
                </Link>
              </li>
              <li>
                <Link href="/dang-nhap" className={styles.colLink}>
                  Đăng nhập tài khoản
                </Link>
              </li>
              <li>
                <Link href="/seller" className={styles.colLink}>
                  Kênh người bán
                </Link>
              </li>
              <li>
                <Link href="/admin" className={styles.colLink}>
                  Cổng quản trị
                </Link>
              </li>
            </ul>
          </nav>

          <nav className={styles.linkCol} aria-label="Tìm hiểu và hỗ trợ">
            <h3 className={styles.colTitle}>Tìm hiểu &amp; Hỗ trợ</h3>
            <ul className={styles.colList}>
              <li>
                <Link href="/ve-nexusmart" className={styles.colLink}>
                  Về NexusMart
                </Link>
              </li>
              <li>
                <Link href="/tro-giup" className={styles.colLink}>
                  Trung tâm trợ giúp
                </Link>
              </li>
              <li>
                <Link href="/tro-giup#thanh-toan-doi-soat" className={styles.colLink}>
                  Thanh toán &amp; đối soát
                </Link>
              </li>
              <li>
                <Link href="/tro-giup#bao-mat" className={styles.colLink}>
                  Bảo mật &amp; tách biệt dữ liệu
                </Link>
              </li>
            </ul>
          </nav>
        </div>
      </div>

      <div className={styles.bottomBand}>
        <div className={styles.chipsRow}>
          <p className={styles.chipsLabel}>Hạ tầng tích hợp sẵn cho từng cửa hàng</p>
          <div className={styles.chips}>
            {CAPABILITY_CHIPS.map((chip) => (
              <span key={chip} className={styles.chip}>
                {chip}
              </span>
            ))}
          </div>
        </div>

        <div className={styles.legalRow}>
          <p className={styles.legalCopy}>
            © 2026 NexusMart. Nền tảng thương mại điện tử đa gian hàng độc lập.
          </p>
          <div className={styles.legalLinks}>
            <Link href="/ve-nexusmart" className={styles.legalLink}>
              Về nền tảng
            </Link>
            <Link href="/tro-giup" className={styles.legalLink}>
              Hỗ trợ
            </Link>
          </div>
        </div>
      </div>
    </footer>
  );
}
