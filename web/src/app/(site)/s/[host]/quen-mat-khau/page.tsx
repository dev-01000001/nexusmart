import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { ForgotPasswordForm } from "@/features/account/AuthForms";
import styles from "@/features/account/AuthForms.module.css";
import { loadSiteContext } from "@/features/site/siteContext.server";

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
    pageTitle: "Quên mật khẩu",
    description: `Khôi phục mật khẩu tài khoản mua hàng tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/quen-mat-khau",
  });
}

export default async function SiteForgotPasswordPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;
  const featured = bundle.products[0];
  const heroImg =
    sanitizeSafeUrl(featured?.onModelImage) ||
    sanitizeSafeUrl(featured?.image) ||
    "/banners/vietnam-workshop.jpg";

  return (
    <section className={styles.siteAuthSection}>
      <div className={styles.siteAuthGrid}>
        <div className={styles.siteAuthFormCol}>
          <header className={styles.siteAuthHead}>
            <h1 className={`${styles.siteAuthTitle} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Quên mật khẩu
                </span>
              </span>
            </h1>
            <p
              className={styles.siteAuthLead}
              data-rise=""
              style={{ "--i": 1 } as React.CSSProperties}
            >
              {brandName}
            </p>
          </header>

          <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            <ForgotPasswordForm loginHref="/dang-nhap" registerHref="/dang-ky" />
          </div>
        </div>

        <aside className={styles.siteAuthVisual} aria-hidden="true">
          <figure className={styles.siteAuthPhotoFrame} data-reveal="clip">
            <img src={heroImg} alt="" width={896} height={1120} className={styles.siteAuthPhoto} />
          </figure>
        </aside>
      </div>
    </section>
  );
}
