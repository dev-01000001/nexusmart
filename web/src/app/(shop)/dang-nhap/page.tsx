import type { Metadata } from "next";
import React from "react";
import Image from "next/image";
import Link from "next/link";
import { FABRIC_MEDIA, MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import { LoginForm } from "@/features/account/AuthForms";
import { PlatformMotion } from "@/features/platform/PlatformMotion";
import styles from "./page.module.css";

export const metadata: Metadata = {
  title: "Đăng nhập",
  description:
    "Đăng nhập Kênh người bán hoặc Cổng quản trị trên nền tảng NexusMart. Khách mua hàng sẽ được dẫn tới danh bạ cửa hàng.",
};

const PLATFORM_ROLES = [
  {
    title: "Kênh người bán",
    detail:
      "Quản lý sản phẩm, tồn kho từng phiên bản, tùy biến giao diện cửa hàng trên tên miền phụ, xử lý đơn và theo dõi đối soát VND.",
  },
  {
    title: "Cổng quản trị nền tảng",
    detail:
      "Duyệt hồ sơ mở gian hàng, kiểm duyệt sản phẩm, quản lý trạng thái xuất bản trang cửa hàng và kiểm tra cân đối sổ cái.",
  },
  {
    title: "Khách mua hàng ghé thăm",
    detail:
      "Mỗi cửa hàng giữ giỏ hàng và đơn hàng riêng trên tên miền của mình. Nếu đăng nhập tại đây, hệ thống đưa bạn tới danh bạ gian hàng.",
  },
];

const EDITORIAL_FRAMES = [
  { src: WORKSHOP_MEDIA.src, alt: WORKSHOP_MEDIA.alt },
  { src: FABRIC_MEDIA.src, alt: FABRIC_MEDIA.alt },
  { src: MEDIA_BANNERS.everyday.src, alt: "Không gian trưng bày cửa hàng trực tuyến" },
  { src: MEDIA_BANNERS.office.src, alt: "Nhận diện riêng của từng thương hiệu trên NexusMart" },
];

export default function PlatformLoginPage() {
  return (
    <div className={styles.page}>
      <PlatformMotion depKey="platform-login" />

      <section className={styles.grid} aria-label="Đăng nhập nền tảng NexusMart">
        <div className={styles.formCol}>
          <header className={styles.header}>
            <h1 className={`${styles.title} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Đăng nhập
                </span>
              </span>
            </h1>
            <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              Dành cho người bán quản trị cửa hàng và quản trị viên nền tảng. Phiên đăng nhập chỉ áp
              dụng trên địa chỉ này.
            </p>
          </header>

          <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            <LoginForm
              variant="platform"
              buyerHref="/#danh-ba-gian-hang"
              registerHref="/dang-ky?role=seller"
            />
          </div>

          <div
            className={styles.buyerNotice}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <p className={styles.buyerNoticeTitle}>Bạn đang tìm trang để mua hàng?</p>
            <p className={styles.buyerNoticeText}>
              Hãy mở trực tiếp trang của cửa hàng bạn muốn mua trong{" "}
              <Link href="/#danh-ba-gian-hang" className={styles.inlineLink}>
                danh bạ gian hàng
              </Link>{" "}
              và đăng nhập tại đó để xem giỏ hàng và lịch sử đơn.
            </p>
          </div>
        </div>

        <aside className={styles.asideCol} aria-label="Thông tin vai trò trên trang gốc">
          <div className={styles.darkBand} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            <h2 className={styles.darkHeading}>Làm việc trên địa chỉ nền tảng</h2>
            <ul className={styles.pointList}>
              {PLATFORM_ROLES.map((item, idx) => (
                <li
                  key={item.title}
                  className={styles.pointRow}
                  data-rise=""
                  style={{ "--i": idx + 3 } as React.CSSProperties}
                >
                  <span className={styles.pointTitle}>{item.title}</span>
                  <p className={styles.pointDesc}>{item.detail}</p>
                </li>
              ))}
            </ul>
          </div>

          <div className={styles.photoStrip}>
            {EDITORIAL_FRAMES.map((frame, idx) => (
              <div
                key={frame.src}
                className={styles.photoFrame}
                data-rise=""
                style={{ "--i": idx + 6 } as React.CSSProperties}
              >
                <Image
                  src={frame.src}
                  alt={frame.alt}
                  fill
                  priority
                  sizes="(max-width: 640px) 50vw, (max-width: 960px) 25vw, 13vw"
                  className={styles.photoImg}
                />
              </div>
            ))}
          </div>
        </aside>
      </section>
    </div>
  );
}
