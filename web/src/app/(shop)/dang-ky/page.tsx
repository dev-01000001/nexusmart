import type { Metadata } from "next";
import React from "react";
import Image from "next/image";
import Link from "next/link";
import { MEDIA_BANNERS } from "@/data/media";
import { RegisterForm } from "@/features/account/AuthForms";
import { PlatformMotion } from "@/features/platform/PlatformMotion";
import styles from "./page.module.css";

export const metadata: Metadata = {
  title: "Đăng ký mở gian hàng",
  description:
    "Tạo tài khoản trên nền tảng NexusMart để mở gian hàng, tùy biến giao diện và bán hàng trên tên miền phụ riêng.",
};

const ONBOARDING_STEPS = [
  {
    title: "Xác thực tài khoản qua email",
    detail:
      "Điền thông tin cơ bản và nhập mã xác thực 6 chữ số gửi về hộp thư để kích hoạt tài khoản.",
  },
  {
    title: "Đăng ký hồ sơ gian hàng và chờ duyệt",
    detail:
      "Cung cấp tên thương hiệu, địa chỉ kho và thông tin liên hệ để đội ngũ quản trị kích hoạt quyền Kênh người bán.",
  },
  {
    title: "Chọn tên miền phụ và xuất bản cửa hàng",
    detail:
      "Tùy biến bảng màu, phông chữ, khối nội dung và bấm xuất bản để cửa hàng lên sóng ngay trên địa chỉ riêng.",
  },
];

const EDITORIAL_FRAMES = [
  { src: MEDIA_BANNERS.everyday.src, alt: "Minh họa không gian trưng bày cửa hàng trực tuyến" },
  { src: MEDIA_BANNERS.office.src, alt: "Minh họa trang cửa hàng với nhận diện thương hiệu riêng" },
  { src: MEDIA_BANNERS.running.src, alt: "Minh họa phong cách cửa hàng thể thao năng động" },
  { src: MEDIA_BANNERS.greenFiber.src, alt: "Minh họa cửa hàng chất liệu tự nhiên" },
];

interface PlatformRegisterPageProps {
  searchParams?: Promise<{ role?: string | string[]; ten?: string | string[] }>;
}

function firstParam(raw: string | string[] | undefined): string {
  return (Array.isArray(raw) ? raw[0] : raw)?.trim() ?? "";
}

export default async function PlatformRegisterPage({ searchParams }: PlatformRegisterPageProps) {
  const params = searchParams ? await searchParams : {};
  const isSellerIntent = firstParam(params.role).toLowerCase() === "seller";
  const suggestedShopName = firstParam(params.ten).slice(0, 64);

  return (
    <div className={styles.page}>
      <PlatformMotion depKey="platform-register" />

      <section className={styles.grid} aria-label="Đăng ký tài khoản nền tảng NexusMart">
        <div className={styles.formCol}>
          <header className={styles.header}>
            <h1 className={`${styles.title} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  {isSellerIntent ? "Mở gian hàng" : "Tạo tài khoản NexusMart"}
                </span>
              </span>
            </h1>
            <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              {isSellerIntent
                ? "Tạo tài khoản người bán làm bước đầu tiên để thiết lập hồ sơ gian hàng và mở cửa hàng riêng trên tên miền phụ."
                : "Khởi tạo tài khoản để bắt đầu quy trình mở gian hàng hoặc tham gia hệ sinh thái các cửa hàng độc lập trên NexusMart."}
            </p>
            {suggestedShopName && (
              <p className={styles.shopHint} role="status">
                Tên gian hàng dự kiến: <strong>{suggestedShopName}</strong>. Sau khi tài khoản được
                xác thực và kích hoạt quyền người bán, bạn có thể chọn tên miền phụ tương ứng trong
                Kênh người bán.
              </p>
            )}
          </header>

          <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            <RegisterForm
              variant="platform"
              buyerHref="/#danh-ba-gian-hang"
              loginHref="/dang-nhap"
            />
          </div>

          <div
            className={styles.buyerNotice}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <p className={styles.buyerNoticeTitle}>Bạn chỉ muốn mua sắm từ một thương hiệu?</p>
            <p className={styles.buyerNoticeText}>
              Bạn có thể vào thẳng trang cửa hàng đó từ{" "}
              <Link href="/#danh-ba-gian-hang" className={styles.inlineLink}>
                danh bạ gian hàng
              </Link>{" "}
              để tạo tài khoản và đặt hàng ngay trên trang của họ.
            </p>
          </div>
        </div>

        <aside className={styles.asideCol} aria-label="Lộ trình mở gian hàng">
          <div className={styles.darkBand} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            <h2 className={styles.darkHeading}>Lộ trình đưa cửa hàng lên sóng</h2>
            <ul className={styles.pointList}>
              {ONBOARDING_STEPS.map((item, idx) => (
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
