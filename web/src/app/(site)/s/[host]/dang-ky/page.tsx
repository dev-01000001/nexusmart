import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { RegisterForm } from "@/features/account/AuthForms";
import { loadSiteContext } from "@/features/site/siteContext.server";
import styles from "@/features/site/site.module.css";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host } = await params;
  const ctx = await loadSiteContext(host);
  if (!ctx) {
    return {
      title: "Không tìm thấy cửa hàng",
      robots: { index: false, follow: false },
    };
  }
  return buildSiteMetadata({
    config: ctx.payload.config,
    pageTitle: "Tạo tài khoản",
    description: `Đăng ký tài khoản mua sắm trực tiếp tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/dang-ky",
  });
}

export default async function SiteRegisterPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;
  const showcase = bundle.products.slice(0, 4);

  return (
    <section className={styles.siteSection}>
      <div className={styles.authGrid}>
        <div className={styles.authFormCol}>
          <div className={styles.authHead}>
            <h1
              className={styles.pageTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              Tạo tài khoản tại {brandName}
            </h1>
            <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              Xác thực email một lần bằng mã 6 chữ số để đặt hàng, lưu sổ địa chỉ và theo dõi đơn
              hàng tại {brandName}.
            </p>
          </div>

          <RegisterForm variant="site" loginHref="/dang-nhap" />
        </div>

        <aside className={styles.authSideCol}>
          <div
            className={styles.authPerksBand}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <h2 className={styles.authPerksTitle}>Quyền lợi khi có tài khoản</h2>
            <ul className={styles.authPerksList}>
              <li className={styles.authPerkItem}>
                <span className={styles.authPerkHeading}>Sổ địa chỉ nhận hàng</span>
                <p className={styles.authPerkBody}>
                  Lưu sẵn nhiều địa chỉ tỉnh thành, phường xã để đặt đơn nhanh không cần nhập lại.
                </p>
              </li>
              <li className={styles.authPerkItem}>
                <span className={styles.authPerkHeading}>Lưu mã giảm giá cửa hàng</span>
                <p className={styles.authPerkBody}>
                  Giữ các mã ưu đãi của {brandName} và áp dụng ngay khi kiểm tra giỏ hàng.
                </p>
              </li>
              <li className={styles.authPerkItem}>
                <span className={styles.authPerkHeading}>
                  Phiên đăng nhập riêng tư theo từng cửa hàng
                </span>
                <p className={styles.authPerkBody}>
                  Giỏ hàng và lịch sử đơn hàng của bạn tại {brandName} được bảo vệ độc lập.
                </p>
              </li>
            </ul>
          </div>

          {showcase.length > 0 && (
            <div className={styles.authPhotoStrip}>
              {showcase.map((product) => {
                const imgSrc =
                  sanitizeSafeUrl(product.onModelImage) ||
                  sanitizeSafeUrl(product.image) ||
                  "/banners/fabric-closeup.jpg";
                return (
                  <Link
                    key={product.id}
                    href={`/san-pham/${encodeURIComponent(product.slug)}`}
                    className={styles.authPhotoLink}
                  >
                    <div className={styles.authPhotoFrame}>
                      <img
                        src={imgSrc}
                        alt={product.name}
                        width={448}
                        height={600}
                        className={styles.authPhotoImg}
                      />
                    </div>
                    <span className={styles.authPhotoCaption}>{product.shortName}</span>
                  </Link>
                );
              })}
            </div>
          )}
        </aside>
      </div>
    </section>
  );
}
