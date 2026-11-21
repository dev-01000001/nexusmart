import React from "react";
import Link from "next/link";
import { buildSiteThemeVars, isDarkHex, sanitizeSafeUrl } from "@/lib/site/theme";
import type { SiteConfig } from "@/lib/site/types";
import { SiteHeaderActions, SiteMotion, SiteNav, SiteToast } from "./SiteChrome";
import { SITE_NAV_ITEMS } from "./siteNav";
import styles from "./site.module.css";

export interface SiteShellProps {
  config: SiteConfig;
  shopName: string;
  /** Origin of the NexusMart root host (footer "Vận hành trên NexusMart" link). */
  platformOrigin: string;
  /** Set true inside Seller draft preview (`/seller/site/xem-truoc`): no live links or actions. */
  previewMode?: boolean;
  children: React.ReactNode;
}

const FOOTER_LINKS: ReadonlyArray<{ label: string; href: string }> = [
  ...SITE_NAV_ITEMS.map(({ label, href }) => ({ label, href })),
  { label: "Chính sách mua hàng", href: "/chinh-sach" },
];

const BUYER_LINKS: ReadonlyArray<{ label: string; href: string }> = [
  { label: "Giỏ hàng", href: "/gio-hang" },
  { label: "Đơn hàng của tôi", href: "/don-hang" },
  { label: "Tra cứu đơn hàng", href: "/tra-cuu-don-hang" },
  { label: "Tài khoản", href: "/tai-khoan" },
];

/**
 * Layout chrome of one shop's site: themed header (nav, search, account, cart), main, footer.
 * The shop theme is applied as `--site-*` custom properties; `.siteRoot` also remaps the global
 * tokens (`--ink`, `--accent`, `--surface-white`…) to them so shared cart/checkout/account
 * components rendered inside follow the shop's palette and typeface.
 */
export function SiteShell({
  config,
  shopName,
  platformOrigin,
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

  const brandInner = (
    <>
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
    </>
  );

  const footerLink = (item: { label: string; href: string }) =>
    previewMode ? (
      <span key={item.href} className={styles.footerLink}>
        {item.label}
      </span>
    ) : (
      <Link key={item.href} href={item.href} className={styles.footerLink}>
        {item.label}
      </Link>
    );

  return (
    <div
      className={styles.siteRoot}
      data-site-root=""
      data-site-theme-mode={isDark ? "dark" : "light"}
      data-site-preview={previewMode ? "true" : "false"}
      style={themeVars as unknown as React.CSSProperties}
    >
      <SiteMotion depKey={config.sections.length} />

      <header className={styles.siteHeader}>
        <div className={styles.headerInner}>
          {previewMode ? (
            <span className={styles.brandLink}>{brandInner}</span>
          ) : (
            <Link href="/" className={styles.brandLink}>
              {brandInner}
            </Link>
          )}
          <SiteNav previewMode={previewMode} />
          {!previewMode && <SiteHeaderActions brandName={brandName} />}
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
            <p className={styles.footerColTitle}>Cửa hàng</p>
            {FOOTER_LINKS.map(footerLink)}
          </div>

          <div className={styles.footerMetaCol} data-reveal="">
            <p className={styles.footerColTitle}>Mua hàng</p>
            {BUYER_LINKS.map(footerLink)}
          </div>
        </div>

        <div className={styles.footerBottom}>
          <span>© {brandName}</span>
          <span>
            Vận hành trên{" "}
            {previewMode ? (
              "NexusMart"
            ) : (
              <a href={platformOrigin} className={styles.poweredByLink}>
                NexusMart
              </a>
            )}
          </span>
        </div>
      </footer>

      {!previewMode && <SiteToast />}
    </div>
  );
}
