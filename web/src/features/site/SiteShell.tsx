import React from "react";
import Link from "next/link";
import { buildSiteThemeVars, isDarkHex } from "@/lib/site/theme";
import type { SiteConfig } from "@/lib/site/types";
import { SiteAssistantWidget } from "@/features/assistant";
import { SiteChatWidget } from "@/features/chat";
import { SiteHeaderActions, SiteMotion, SiteNav, SiteToast } from "./SiteChrome";
import { SITE_NAV_ITEMS } from "./siteNav";
import type { SiteSectionV2, SiteTemplateConfig } from "./templateTypes";
import {
  createDefaultLayoutConfig,
  createDefaultPagesConfig,
  normalizeSiteTemplateConfig,
  sanitizeSafeImageUrl,
} from "./templateValidator";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

export interface SiteShellProps {
  config: SiteConfig | SiteTemplateConfig;
  shopName: string;
  /** Backend shop ID (enables Swarm G search suggestions & AI assistant). */
  shopId?: string;
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

function buildAnchorItems(sections: SiteSectionV2[]): Array<{ id: string; label: string }> {
  const labelsByType: Record<string, string> = {
    featured_products: "Sản phẩm",
    metrics_strip: "Thông số",
    testimonials: "Đánh giá",
    voucher_strip: "Ưu đãi",
    policies: "Cam kết",
    lookbook: "Lookbook",
    sku_matrix: "Bảng SKU",
    collection: "Bộ sưu tập",
    text: "Câu chuyện",
  };
  const out: Array<{ id: string; label: string }> = [];
  for (const sec of sections) {
    if (sec.visible === false || sec.type === "hero") continue;
    const label = labelsByType[sec.type];
    if (!label) continue;
    if (out.some((x) => x.label === label)) continue;
    out.push({ id: sec.id, label });
    if (out.length >= 4) break;
  }
  return out;
}

/**
 * Multi-template storefront shell supporting all 8 layout archetypes, 8 header styles,
 * 6 footer styles, Swarm G search/assistant integration, and lossless v1 compatibility.
 */
export function SiteShell({
  config,
  shopName,
  shopId,
  platformOrigin,
  previewMode = false,
  children,
}: SiteShellProps) {
  const tpl: SiteTemplateConfig = normalizeSiteTemplateConfig(config, shopName) ?? {
    schema_version: 2,
    template_id: "legacy-v1",
    theme: config.theme,
    layout: createDefaultLayoutConfig(),
    sections: [],
    pages: createDefaultPagesConfig(),
  };

  const { theme, layout } = tpl;
  const brandName = theme.brand_name?.trim() || shopName || "Cửa hàng trên NexusMart";
  const tagline = theme.tagline?.trim() || "";
  const logoUrl = sanitizeSafeImageUrl(theme.logo_url);
  const themeVars = buildSiteThemeVars(theme);
  const isDark = isDarkHex(themeVars["--site-bg"]);
  const monogram = brandName.slice(0, 1).toUpperCase();
  const radius = theme.radius || "none";
  const density = theme.density || "balanced";
  const headerStyle = layout.header_style;
  const footerStyle = layout.footer_style;
  const showSearchSlot = layout.slots?.search !== false;
  const showChatSlot = layout.slots?.chat !== false;
  const showAssistantSlot = layout.slots?.assistant !== false;

  const renderBrandMark = (
    opts: { withTagline?: boolean; centeredWordmark?: "editorial" | "maison" } = {}
  ) => {
    if (opts.centeredWordmark === "maison") {
      return (
        <span className={tplStyles.mastheadCenterBrand}>
          {logoUrl && (
            <span className={styles.brandLogoWrap}>
              <img
                src={logoUrl}
                alt={`Logo ${brandName}`}
                width={36}
                height={36}
                className={styles.brandLogoImg}
              />
            </span>
          )}
          <span className={tplStyles.maisonWordmark}>{brandName}</span>
        </span>
      );
    }

    if (opts.centeredWordmark === "editorial") {
      return (
        <span className={tplStyles.mastheadCenterBrand}>
          {logoUrl && (
            <span className={styles.brandLogoWrap}>
              <img
                src={logoUrl}
                alt={`Logo ${brandName}`}
                width={36}
                height={36}
                className={styles.brandLogoImg}
              />
            </span>
          )}
          <span className={tplStyles.mastheadWordmark}>{brandName}</span>
        </span>
      );
    }

    return (
      <>
        {headerStyle === "bold-dark" && (
          <span className={tplStyles.boldAccentPip} aria-hidden="true" />
        )}
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
          {opts.withTagline && tagline && (
            <span className={tplStyles.brandTaglineInline}>{tagline}</span>
          )}
        </span>
      </>
    );
  };

  const wrapBrand = (inner: React.ReactNode) =>
    previewMode ? (
      <span className={styles.brandLink}>{inner}</span>
    ) : (
      <Link href="/" className={styles.brandLink}>
        {inner}
      </Link>
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

  const renderHeader = () => {
    if (headerStyle === "centered-masthead") {
      return (
        <header className={tplStyles.header} data-site-header="centered-masthead">
          <div className={tplStyles.mastheadTopRow}>
            <span className={tplStyles.mastheadLeftNote}>
              {tagline || `Cửa hàng chính hãng ${brandName}`}
            </span>
            {wrapBrand(renderBrandMark({ centeredWordmark: "editorial" }))}
            <div className={tplStyles.mastheadRightActions}>
              {!previewMode && (
                <SiteHeaderActions
                  brandName={brandName}
                  shopId={shopId}
                  showSearchSlot={showSearchSlot}
                  hideDesktopSearch
                />
              )}
            </div>
          </div>
          <div className={tplStyles.mastheadNavRail}>
            <SiteNav previewMode={previewMode} />
          </div>
        </header>
      );
    }

    if (headerStyle === "maison-centered") {
      return (
        <header className={tplStyles.header} data-site-header="maison-centered">
          <div className={tplStyles.mastheadTopRow}>
            <span className={tplStyles.mastheadLeftNote}>
              {previewMode ? (
                "Phòng trưng bày"
              ) : (
                <Link href="/gioi-thieu" className={styles.headerTextAction}>
                  Câu chuyện Maison
                </Link>
              )}
            </span>
            {wrapBrand(renderBrandMark({ centeredWordmark: "maison" }))}
            <div className={tplStyles.mastheadRightActions}>
              {!previewMode && (
                <SiteHeaderActions
                  brandName={brandName}
                  shopId={shopId}
                  showSearchSlot={showSearchSlot}
                  hideDesktopSearch
                />
              )}
            </div>
          </div>
          <div className={tplStyles.maisonNavRail}>
            <SiteNav previewMode={previewMode} />
          </div>
        </header>
      );
    }

    if (headerStyle === "anchor-minimal") {
      const anchors = buildAnchorItems(tpl.sections);
      return (
        <header className={tplStyles.header} data-site-header="anchor-minimal">
          <div className={tplStyles.headerInner}>
            {wrapBrand(renderBrandMark())}
            {anchors.length >= 2 ? (
              <nav className={tplStyles.anchorNav} aria-label="Mục nổi bật trên trang">
                {anchors.map((a) =>
                  previewMode ? (
                    <span key={a.id} className={tplStyles.anchorLink}>
                      {a.label}
                    </span>
                  ) : (
                    <a
                      key={a.id}
                      href={`/#${encodeURIComponent(a.id)}`}
                      className={tplStyles.anchorLink}
                    >
                      {a.label}
                    </a>
                  )
                )}
                {previewMode ? (
                  <span className={tplStyles.anchorLink}>Danh mục</span>
                ) : (
                  <Link href="/san-pham" className={tplStyles.anchorLink}>
                    Danh mục
                  </Link>
                )}
              </nav>
            ) : (
              <SiteNav previewMode={previewMode} />
            )}
            {!previewMode && (
              <SiteHeaderActions
                brandName={brandName}
                shopId={shopId}
                showSearchSlot={showSearchSlot}
              />
            )}
          </div>
        </header>
      );
    }

    if (headerStyle === "catalog-rail") {
      return (
        <header className={tplStyles.header} data-site-header="catalog-rail">
          <div className={tplStyles.headerInner}>
            {wrapBrand(renderBrandMark())}
            {previewMode ? (
              <span className={tplStyles.catalogQuickBtn}>Bảng SKU</span>
            ) : (
              <Link href="/san-pham" className={tplStyles.catalogQuickBtn}>
                Bảng SKU
              </Link>
            )}
            <SiteNav previewMode={previewMode} />
            {!previewMode && (
              <SiteHeaderActions
                brandName={brandName}
                shopId={shopId}
                showSearchSlot={showSearchSlot}
              />
            )}
          </div>
        </header>
      );
    }

    // minimal-split, utility-compact, atelier-framed, bold-dark
    return (
      <header className={tplStyles.header} data-site-header={headerStyle}>
        <div className={tplStyles.headerInner}>
          {wrapBrand(renderBrandMark({ withTagline: headerStyle === "atelier-framed" }))}
          <SiteNav previewMode={previewMode} />
          {!previewMode && (
            <SiteHeaderActions
              brandName={brandName}
              shopId={shopId}
              showSearchSlot={showSearchSlot}
            />
          )}
        </div>
      </header>
    );
  };

  const renderFooterBottom = () => (
    <div className={tplStyles.footerBottomBar}>
      <div className={tplStyles.footerBottomInner}>
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
    </div>
  );

  const renderFooter = () => {
    if (footerStyle === "minimal-row") {
      return (
        <footer className={tplStyles.footer} data-site-footer="minimal-row">
          <div className={tplStyles.footerMinimalRow}>
            <div>
              <p className={styles.footerBrandName}>{brandName}</p>
              {tagline && <p className={styles.footerTagline}>{tagline}</p>}
            </div>
            <div className={tplStyles.footerMinimalLinks}>
              {FOOTER_LINKS.map(footerLink)}
              {BUYER_LINKS.slice(0, 2).map(footerLink)}
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    if (footerStyle === "compact-bar") {
      return (
        <footer className={tplStyles.footer} data-site-footer="compact-bar">
          <div className={tplStyles.footerCompactBar}>
            <div className={tplStyles.footerCompactTop}>
              <p className={styles.footerBrandName}>{brandName}</p>
              <div className={tplStyles.footerMinimalLinks}>
                {FOOTER_LINKS.map(footerLink)}
                {BUYER_LINKS.map(footerLink)}
              </div>
            </div>
            {tagline && <p className={styles.footerTagline}>{tagline}</p>}
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    if (footerStyle === "maison-minimal") {
      return (
        <footer className={tplStyles.footer} data-site-footer="maison-minimal">
          <div className={tplStyles.footerMaisonWrap}>
            <p className={tplStyles.footerMaisonWordmark}>{brandName}</p>
            {tagline && <p className={styles.footerTagline}>{tagline}</p>}
            <div className={tplStyles.footerMaisonLinks}>
              {FOOTER_LINKS.map(footerLink)}
              {BUYER_LINKS.slice(0, 3).map(footerLink)}
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    if (footerStyle === "editorial-cols") {
      return (
        <footer className={tplStyles.footer} data-site-footer="editorial-cols">
          <div className={tplStyles.footerColsGrid}>
            <div className={tplStyles.footerEditorialBrand} data-reveal="">
              <p className={styles.footerBrandName}>{brandName}</p>
              {tagline && <p className={styles.footerTagline}>{tagline}</p>}
            </div>
            <div className={tplStyles.footerEditorialCol} data-reveal="">
              <p className={styles.footerColTitle}>Cửa hàng</p>
              {FOOTER_LINKS.slice(0, 3).map(footerLink)}
            </div>
            <div className={tplStyles.footerEditorialCol} data-reveal="">
              <p className={styles.footerColTitle}>Thông tin</p>
              {FOOTER_LINKS.slice(3).map(footerLink)}
            </div>
            <div className={tplStyles.footerEditorialWideCol} data-reveal="">
              <p className={styles.footerColTitle}>Khách hàng</p>
              {BUYER_LINKS.map(footerLink)}
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    if (footerStyle === "atelier-footer") {
      return (
        <footer className={tplStyles.footer} data-site-footer="atelier-footer">
          <div className={tplStyles.footerColsGrid}>
            <div className={tplStyles.footerAtelierCallout} data-reveal="">
              <p className={styles.footerBrandName}>{brandName}</p>
              {tagline && <p className={styles.footerTagline}>{tagline}</p>}
            </div>
            <div className={styles.footerNavCol} data-reveal="">
              <p className={styles.footerColTitle}>Cửa hàng xưởng</p>
              {FOOTER_LINKS.map(footerLink)}
            </div>
            <div className={styles.footerMetaCol} data-reveal="">
              <p className={styles.footerColTitle}>Mua hàng & Đổi cỡ</p>
              {BUYER_LINKS.map(footerLink)}
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    // bold-dark & default fallback
    return (
      <footer className={tplStyles.footer} data-site-footer={footerStyle}>
        <div className={tplStyles.footerColsGrid}>
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
        {renderFooterBottom()}
      </footer>
    );
  };

  return (
    <div
      className={`${styles.siteRoot} ${tplStyles.templateRoot}`}
      data-site-root=""
      data-site-theme-mode={isDark ? "dark" : "light"}
      data-site-preview={previewMode ? "true" : "false"}
      data-site-archetype={layout.archetype}
      data-site-header={headerStyle}
      data-site-footer={footerStyle}
      data-site-container={layout.container_width}
      data-site-card={layout.card_style}
      data-site-radius={radius}
      data-site-density={density}
      style={themeVars as unknown as React.CSSProperties}
    >
      <SiteMotion depKey={`${tpl.template_id}-${tpl.sections.length}`} />

      {layout.show_announcement && layout.announcement_text.trim() && (
        <div className={tplStyles.announcementBar} role="note">
          {layout.announcement_text}
        </div>
      )}

      {renderHeader()}

      <main id="main-content" tabIndex={-1} className={styles.siteMain}>
        {children}
      </main>

      {renderFooter()}

      {!previewMode && <SiteToast />}
      {!previewMode && shopId && showAssistantSlot && (
        <SiteAssistantWidget shopId={shopId} brandName={brandName} />
      )}
      {!previewMode && shopId && showChatSlot && (
        <SiteChatWidget shopId={shopId} brandName={brandName} />
      )}
    </div>
  );
}
