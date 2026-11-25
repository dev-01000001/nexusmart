import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { ResetPasswordForm } from "@/features/account/AuthForms";
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
  const base = buildSiteMetadata({
    config: ctx.payload.config,
    pageTitle: "Đặt lại mật khẩu",
    description: `Đặt lại mật khẩu tài khoản tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/dat-lai-mat-khau",
  });
  return {
    ...base,
    robots: { index: false, follow: false },
  };
}

export default async function SiteResetPasswordPage({ params }: PageProps) {
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
                  Đặt lại mật khẩu
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
            <ResetPasswordForm loginHref="/dang-nhap" forgotHref="/quen-mat-khau" />
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
