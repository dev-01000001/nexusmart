import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { LoginForm } from "@/features/account/AuthForms";
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
    pageTitle: "Đăng nhập",
    description: `Đăng nhập tài khoản mua hàng tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/dang-nhap",
  });
}

export default async function SiteLoginPage({ params }: PageProps) {
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
              Đăng nhập tại {brandName}
            </h1>
            <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              Lưu giỏ hàng, chọn địa chỉ nhận hàng và theo dõi các đơn hàng đã đặt tại {brandName}.
            </p>
          </div>

          <LoginForm variant="site" registerHref="/dang-ky" />
        </div>

        <aside className={styles.authSideCol}>
          <div
            className={styles.authPerksBand}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <h2 className={styles.authPerksTitle}>Mua sắm trực tiếp tại {brandName}</h2>
            <ul className={styles.authPerksList}>
              <li className={styles.authPerkItem}>
                <span className={styles.authPerkHeading}>Thanh toán linh hoạt</span>
                <p className={styles.authPerkBody}>
                  Hỗ trợ thanh toán khi nhận hàng (COD) đồng kiểm hoặc chuyển khoản VietQR xác nhận
                  tự động.
                </p>
              </li>
              <li className={styles.authPerkItem}>
                <span className={styles.authPerkHeading}>Mã giảm giá riêng của cửa hàng</span>
                <p className={styles.authPerkBody}>
                  Lưu và áp dụng trực tiếp ưu đãi của {brandName} ngay trong giỏ hàng.
                </p>
              </li>
              <li className={styles.authPerkItem}>
                <span className={styles.authPerkHeading}>Theo dõi đơn hàng minh bạch</span>
                <p className={styles.authPerkBody}>
                  Cập nhật trạng thái chuẩn bị, giao hàng, hủy đơn hoặc yêu cầu đổi trả ngay trên
                  website này.
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
