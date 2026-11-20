import React from "react";
import Link from "next/link";
import { buildStorefrontCartUrl } from "@/lib/site/host";
import { buildSiteThemeVars, isDarkHex, sanitizeSafeUrl } from "@/lib/site/theme";
import type { SiteConfig } from "@/lib/site/types";
import { SiteMotion } from "./SiteMotion";
import styles from "./site.module.css";

export interface SiteShellProps {
  config: SiteConfig;
  shopName: string;
  shopId?: string;
  siteHost?: string;
  activeNav?: "home" | "products" | "policies" | "about";
  marketplaceOrigin: string;
  /** Set true inside Seller draft preview (`/seller/site/xem-truoc`). */
  previewMode?: boolean;
  children: React.ReactNode;
}

const NAV_ITEMS: ReadonlyArray<{
  key: "home" | "products" | "policies" | "about";
  label: string;
  href: string;
}> = [
  { key: "home", label: "Trang chủ", href: "/" },
  { key: "products", label: "Sản phẩm", href: "/san-pham" },
  { key: "policies", label: "Chính sách", href: "/chinh-sach" },
  { key: "about", label: "Giới thiệu", href: "/gioi-thieu" },
];

export function SiteShell({
  config,
  shopName,
  shopId,
  siteHost,
  activeNav = "home",
  marketplaceOrigin,
  previewMode = false,
  children,
}: SiteShellProps) {
  const theme = config.theme;
  const brandName = theme.brand_name?.trim() || shopName || "Cửa hàng trên NexusMart";
  const tagline = theme.tagline?.trim() || "";
  const logoUrl = sanitizeSafeUrl(theme.logo_url);
  const themeVars = buildSiteThemeVars(theme);
  const isDark = isDarkHex(themeVars["--site-bg"]);
  const monogram = brandName.slice(0, 1).toUpperCase();

  const cartHandoffHref =
    !previewMode && shopId
      ? buildStorefrontCartUrl({
          marketplaceOrigin,
          shopId,
          siteHost: siteHost || "",
        })
      : `${marketplaceOrigin.replace(/\/+$/, "")}/gio-hang`;

  return (
    <div
      className={styles.siteRoot}
      data-site-root=""
      data-site-theme-mode={isDark ? "dark" : "light"}
      data-site-preview={previewMode ? "true" : "false"}
      style={themeVars as unknown as React.CSSProperties}
    >
      <SiteMotion depKey={`${activeNav}-${config.sections.length}`} />

      <header className={styles.siteHeader}>
        <div className={styles.headerInner}>
          {previewMode ? (
            <span className={styles.brandLink}>
              {logoUrl ? (
                <span className={styles.brandLogoWrap}>
                  <img
                    src={logoUrl}
                    alt={`Logo ${brandName}`}
                    width={40}
                    height={40}
                    className={styles.brandLogoImg}
                  />
                </span>
              ) : (
                <span className={styles.brandMonogram} aria-hidden="true">
                  {monogram}
                </span>
              )}
              <span className={styles.brandText}>
                <span className={styles.brandName}>{brandName}</span>
              </span>
            </span>
          ) : (
            <Link href="/" className={styles.brandLink}>
              {logoUrl ? (
                <span className={styles.brandLogoWrap}>
                  <img
                    src={logoUrl}
                    alt={`Logo ${brandName}`}
                    width={40}
                    height={40}
                    className={styles.brandLogoImg}
                  />
                </span>
              ) : (
                <span className={styles.brandMonogram} aria-hidden="true">
                  {monogram}
                </span>
              )}
              <span className={styles.brandText}>
                <span className={styles.brandName}>{brandName}</span>
              </span>
            </Link>
          )}

          <nav className={styles.headerNav} aria-label="Điều hướng cửa hàng">
            {NAV_ITEMS.map((item) => {
              const active = activeNav === item.key;
              if (previewMode) {
                return (
                  <span
                    key={item.key}
                    className={`${styles.navLink} ${active ? styles.navLinkActive : ""}`}
                  >
                    {item.label}
                  </span>
                );
              }
              return (
                <Link
                  key={item.key}
                  href={item.href}
                  className={`${styles.navLink} ${active ? styles.navLinkActive : ""}`}
                >
                  {item.label}
                </Link>
              );
            })}

            {!previewMode && (
              <a href={cartHandoffHref} className={styles.headerAction}>
                Giỏ hàng
              </a>
            )}
          </nav>
        </div>
      </header>

      <main id="main-content" tabIndex={-1} className={styles.siteMain}>
        {children}
      </main>

      <footer className={styles.siteFooter}>
        <div className={styles.footerInner}>
          <div className={styles.footerBrandCol} data-reveal="">
            <p className={styles.footerBrandName}>{brandName}</p>
            {tagline && <p className={styles.footerTagline}>{tagline}</p>}
          </div>

          <div className={styles.footerNavCol} data-reveal="">
            <p className={styles.footerColTitle}>Khám phá cửa hàng</p>
            {NAV_ITEMS.map((item) =>
              previewMode ? (
                <span key={item.key} className={styles.footerLink}>
                  {item.label}
                </span>
              ) : (
                <Link key={item.key} href={item.href} className={styles.footerLink}>
                  {item.label}
                </Link>
              )
            )}
          </div>

          <div className={styles.footerMetaCol} data-reveal="">
            <p className={styles.footerColTitle}>Mua sắm an tâm</p>
            <p className={styles.footerTagline}>
              Đơn hàng và thanh toán được bảo vệ tập trung trên nền tảng NexusMart.
            </p>
            {!previewMode && (
              <a href={cartHandoffHref} className={styles.footerLink}>
                Mở giỏ hàng của {brandName}
              </a>
            )}
          </div>
        </div>

        <div className={styles.footerBottom}>
          <span>© {brandName}</span>
          <span>
            Vận hành trên{" "}
            <a href={marketplaceOrigin} className={styles.poweredByLink}>
              NexusMart
            </a>
          </span>
        </div>
      </footer>
    </div>
  );
}
