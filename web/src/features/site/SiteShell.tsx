import React from "react";
import Link from "next/link";
import type { SiteConfig } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { SiteHeaderActions, SiteNav } from "./SiteChrome";
import { SiteSupportDock } from "./SiteSupportDock";
import { SiteThemeRoot } from "./shell/SiteThemeRoot";
import { containerWidthClass, SITE_PX } from "./shell/siteStyleVars";
import { SITE_NAV_ITEMS } from "./siteNav";
import type { SiteSectionV2, SiteTemplateConfig } from "./templateTypes";
import {
  createDefaultLayoutConfig,
  createDefaultPagesConfig,
  normalizeSiteTemplateConfig,
  sanitizeSafeImageUrl,
} from "./templateValidator";

export interface SiteShellProps {
  config: SiteConfig | SiteTemplateConfig;
  shopName: string;
  /** Backend shop ID (enables Swarm G search suggestions, chat & AI assistant). */
  shopId?: string;
  /** Origin of the NexusMart root host (footer "Vận hành trên NexusMart" link). */
  platformOrigin: string;
  /** Set true inside Seller draft preview (`/seller/site/xem-truoc`): no live links or actions. */
  previewMode?: boolean;
  /** Optional lazy-loaded toaster rendered inside `UiScope` on live site hosts. */
  toaster?: React.ReactNode;
  children: React.ReactNode;
}

const FOOTER_LINKS: ReadonlyArray<{ label: string; href: string }> = [
  ...SITE_NAV_ITEMS.map(({ label, href }) => ({ label, href })),
  { label: "Chính sách", href: "/chinh-sach" },
];

const BUYER_LINKS: ReadonlyArray<{ label: string; href: string }> = [
  { label: "Giỏ hàng", href: "/gio-hang" },
  { label: "Đơn hàng", href: "/don-hang" },
  { label: "Tra cứu đơn", href: "/tra-cuu-don-hang" },
  { label: "Tài khoản", href: "/tai-khoan" },
];

function buildAnchorItems(sections: SiteSectionV2[]): Array<{ id: string; label: string }> {
  const labelsByType: Record<string, string> = {
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
    if (out.length >= 3) break;
  }
  return out;
}

/**
 * Multi-template storefront shell supporting all 8 layout archetypes, 8 header styles,
 * 6 footer styles, unified floating support dock, lazy search & mobile sheet,
 * scoped `--ui-*` + `--site-*` theme vars via `UiScope`, and lossless v1 compatibility.
 */
export function SiteShell({
  config,
  shopName,
  shopId,
  platformOrigin,
  previewMode = false,
  toaster,
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
  const radius = theme.radius || "none";
  const density = theme.density || "balanced";
  const headerStyle = layout.header_style;
  const footerStyle = layout.footer_style;
  const showSearchSlot = layout.slots?.search !== false;
  const showChatSlot = layout.slots?.chat !== false;
  const showAssistantSlot = layout.slots?.assistant !== false;

  const containerCls = containerWidthClass(layout.container_width);

  const renderBrandMark = (
    opts: { withTagline?: boolean; centeredWordmark?: "editorial" | "maison" } = {}
  ) => {
    if (opts.centeredWordmark === "maison") {
      return (
        <span className="inline-flex items-center justify-start text-left text-foreground lg:justify-center lg:text-center">
          <span className="text-[clamp(19px,1.9vw,26px)] leading-[1.2] font-normal tracking-[0.16em] text-foreground">
            {brandName}
          </span>
        </span>
      );
    }

    if (opts.centeredWordmark === "editorial") {
      return (
        <span className="inline-flex items-center justify-start text-left text-foreground lg:justify-center lg:text-center">
          <span className="text-[clamp(22px,2.6vw,36px)] leading-[1.04] font-bold tracking-[-0.025em] text-foreground">
            {brandName}
          </span>
        </span>
      );
    }

    return (
      <>
        {logoUrl && (
          <span className="flex size-7 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-muted sm:size-8 lg:size-[34px]">
            <img
              src={logoUrl}
              alt={`Logo ${brandName}`}
              width={36}
              height={36}
              className="size-full object-cover"
            />
          </span>
        )}
        <span className="flex min-w-0 flex-col">
          <span
            className={cn(
              "line-clamp-2 leading-[1.18] tracking-[-0.015em] text-foreground sm:line-clamp-1 sm:truncate",
              headerStyle === "bold-dark"
                ? "text-[15px] font-extrabold tracking-[-0.02em] sm:text-[20px] lg:text-[22px]"
                : headerStyle === "atelier-framed"
                  ? "text-[15px] font-semibold tracking-[0.01em] sm:text-[17px]"
                  : "text-[15px] font-semibold sm:text-base lg:text-lg"
            )}
          >
            {brandName}
          </span>
          {opts.withTagline && tagline && (
            <span className="max-w-[28ch] truncate text-[13px] leading-[1.3] text-muted-foreground">
              {tagline}
            </span>
          )}
        </span>
      </>
    );
  };

  const brandLinkCls =
    "inline-flex min-w-0 flex-1 items-center gap-2 text-foreground transition-opacity hover:opacity-80 sm:flex-initial sm:gap-2.5";

  const wrapBrand = (inner: React.ReactNode) =>
    previewMode ? (
      <span className={brandLinkCls}>{inner}</span>
    ) : (
      <Link href="/" className={brandLinkCls}>
        {inner}
      </Link>
    );

  const footerLinkCls =
    "inline-flex min-h-7 w-fit items-center py-0.5 text-[15px] text-inherit transition-opacity hover:opacity-75";

  const footerLink = (item: { label: string; href: string }) =>
    previewMode ? (
      <span key={item.href} className={footerLinkCls}>
        {item.label}
      </span>
    ) : (
      <Link key={item.href} href={item.href} className={footerLinkCls}>
        {item.label}
      </Link>
    );

  const renderHeader = () => {
    if (headerStyle === "centered-masthead") {
      return (
        <header
          className="sticky top-0 z-40 border-b border-border bg-background"
          data-site-header="centered-masthead"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 pt-4 pb-2.5 lg:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]"
            )}
          >
            <span className="hidden min-w-0 truncate text-[13px] text-muted-foreground lg:block">
              {layout.show_announcement && layout.announcement_text.trim()
                ? layout.announcement_text
                : brandName}
            </span>
            {wrapBrand(renderBrandMark({ centeredWordmark: "editorial" }))}
            <div className="flex min-w-0 items-center justify-end">
              {!previewMode && (
                <SiteHeaderActions
                  brandName={brandName}
                  shopId={shopId}
                  showSearchSlot={showSearchSlot}
                />
              )}
            </div>
          </div>
          <div
            className={cn(
              "w-full items-center justify-center border-t border-border bg-card py-2",
              SITE_PX,
              previewMode ? "flex" : "hidden lg:flex"
            )}
          >
            <SiteNav previewMode={previewMode} />
          </div>
        </header>
      );
    }

    if (headerStyle === "maison-centered") {
      return (
        <header
          className="sticky top-0 z-40 border-b border-border/60 bg-background"
          data-site-header="maison-centered"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 pt-4 pb-2.5 lg:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]"
            )}
          >
            <span className="hidden min-w-0 truncate text-[13px] text-muted-foreground lg:block">
              {previewMode ? (
                "Maison"
              ) : (
                <Link
                  href="/gioi-thieu"
                  className="inline-flex min-h-10 items-center text-sm font-medium text-foreground transition-colors hover:text-brand"
                >
                  Giới thiệu
                </Link>
              )}
            </span>
            {wrapBrand(renderBrandMark({ centeredWordmark: "maison" }))}
            <div className="flex min-w-0 items-center justify-end">
              {!previewMode && (
                <SiteHeaderActions
                  brandName={brandName}
                  shopId={shopId}
                  showSearchSlot={showSearchSlot}
                />
              )}
            </div>
          </div>
          <div
            className={cn(
              "w-full items-center justify-center pt-1.5 pb-3.5",
              SITE_PX,
              previewMode ? "flex" : "hidden lg:flex"
            )}
          >
            <SiteNav previewMode={previewMode} />
          </div>
        </header>
      );
    }

    if (headerStyle === "anchor-minimal") {
      const anchors = buildAnchorItems(tpl.sections);
      const anchorLinkCls =
        "py-1.5 text-sm font-medium whitespace-nowrap text-foreground transition-colors hover:text-brand";
      return (
        <header
          className="sticky top-0 z-40 border-b border-border bg-background"
          data-site-header="anchor-minimal"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "flex min-h-16 flex-nowrap items-center justify-between gap-x-5 gap-y-3 py-2.5"
            )}
          >
            {wrapBrand(renderBrandMark())}
            {anchors.length >= 2 ? (
              <nav
                className={cn(
                  "items-center gap-x-5 gap-y-2",
                  previewMode ? "flex flex-wrap" : "hidden flex-nowrap lg:flex"
                )}
                aria-label="Điều hướng cửa hàng"
              >
                {anchors.map((a) =>
                  previewMode ? (
                    <span key={a.id} className={anchorLinkCls}>
                      {a.label}
                    </span>
                  ) : (
                    <a key={a.id} href={`/#${encodeURIComponent(a.id)}`} className={anchorLinkCls}>
                      {a.label}
                    </a>
                  )
                )}
                {previewMode ? (
                  <span className={anchorLinkCls}>Sản phẩm</span>
                ) : (
                  <Link href="/san-pham" className={anchorLinkCls}>
                    Sản phẩm
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
      const quickBtnCls =
        "inline-flex min-h-9 items-center rounded-md bg-[var(--site-accent-soft)] px-3 text-[13px] font-semibold whitespace-nowrap text-[var(--site-accent-soft-ink)] transition-colors hover:bg-primary hover:text-primary-foreground";
      return (
        <header
          className="sticky top-0 z-40 border-b border-border bg-card"
          data-site-header="catalog-rail"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "flex min-h-14 flex-nowrap items-center justify-between gap-x-5 gap-y-2 py-2"
            )}
          >
            {wrapBrand(renderBrandMark())}
            <div
              className={cn(
                "items-center gap-4",
                previewMode ? "flex flex-wrap" : "hidden lg:flex"
              )}
            >
              {previewMode ? (
                <span className={quickBtnCls}>Bảng SKU</span>
              ) : (
                <Link href="/san-pham" className={quickBtnCls}>
                  Bảng SKU
                </Link>
              )}
              <SiteNav previewMode={previewMode} />
            </div>
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
    const headerBgCls =
      headerStyle === "minimal-split" || headerStyle === "bold-dark" ? "bg-background" : "bg-card";
    const headerHeightCls =
      headerStyle === "utility-compact"
        ? "min-h-[52px] py-1.5"
        : headerStyle === "atelier-framed"
          ? "min-h-[70px] py-2.5"
          : "min-h-16 py-2.5";

    return (
      <header
        className={cn("sticky top-0 z-40 border-b border-border", headerBgCls)}
        data-site-header={headerStyle}
      >
        <div
          className={cn(
            containerCls,
            SITE_PX,
            "flex flex-nowrap items-center justify-between gap-2 sm:gap-x-5 sm:gap-y-3",
            headerHeightCls
          )}
        >
          {wrapBrand(renderBrandMark())}
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
    <div className="w-full border-t border-current/10">
      <div
        className={cn(
          containerCls,
          SITE_PX,
          "flex flex-wrap items-center justify-between gap-x-6 gap-y-3 py-4 text-[13px]"
        )}
      >
        <span>© {brandName}</span>
        <span>
          Vận hành trên{" "}
          {previewMode ? (
            "NexusMart"
          ) : (
            <a
              href={platformOrigin}
              className="inline-flex min-h-7 items-center py-0.5 font-semibold underline underline-offset-[3px]"
            >
              NexusMart
            </a>
          )}
        </span>
      </div>
    </div>
  );

  const footerBrandNameCls = "text-xl font-semibold text-inherit";
  const footerTaglineCls = "max-w-[40ch] text-[15px] leading-[1.5] text-inherit opacity-85";
  const footerColTitleCls = "text-sm font-semibold text-inherit opacity-70";

  const renderFooter = () => {
    if (footerStyle === "minimal-row") {
      return (
        <footer
          className="mt-auto border-t border-border bg-card text-card-foreground"
          data-site-footer="minimal-row"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "flex flex-wrap items-start justify-between gap-x-9 gap-y-5 py-[clamp(28px,3.5vw,44px)]"
            )}
          >
            <div className="flex flex-col gap-1.5">
              <p className={footerBrandNameCls}>{brandName}</p>
              {tagline && <p className={footerTaglineCls}>{tagline}</p>}
            </div>
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2.5">
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
        <footer
          className="mt-auto bg-[var(--site-band-bg)] text-[var(--site-band-ink)]"
          data-site-footer="compact-bar"
        >
          <div className={cn(containerCls, SITE_PX, "py-5")}>
            <div className="flex flex-wrap items-center justify-between gap-x-7 gap-y-3.5">
              <p className={footerBrandNameCls}>{brandName}</p>
              <div className="flex flex-wrap items-center gap-x-5 gap-y-2.5">
                {FOOTER_LINKS.map(footerLink)}
                {BUYER_LINKS.map(footerLink)}
              </div>
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    if (footerStyle === "maison-minimal") {
      return (
        <footer
          className="mt-auto border-t border-border bg-card text-card-foreground"
          data-site-footer="maison-minimal"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "flex flex-col items-center gap-5 pt-[clamp(48px,6vw,76px)] pb-[clamp(32px,4vw,44px)] text-center"
            )}
          >
            <p className="text-[clamp(20px,2vw,26px)] font-normal tracking-[0.16em] text-inherit">
              {brandName}
            </p>
            <div className="flex flex-wrap items-center justify-center gap-x-6 gap-y-2.5">
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
        <footer
          className="mt-auto bg-[var(--site-band-bg)] text-[var(--site-band-ink)]"
          data-site-footer="editorial-cols"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "grid grid-cols-1 gap-7 pt-[clamp(36px,5vw,56px)] pb-[clamp(24px,3vw,36px)] lg:grid-cols-12"
            )}
          >
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-4">
              <p className="text-[clamp(24px,2.5vw,32px)] leading-[1.06] font-bold tracking-[-0.02em] text-inherit">
                {brandName}
              </p>
              {tagline && <p className={footerTaglineCls}>{tagline}</p>}
            </div>
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-2">
              <p className={footerColTitleCls}>Cửa hàng</p>
              {FOOTER_LINKS.slice(0, 3).map(footerLink)}
            </div>
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-2">
              <p className={footerColTitleCls}>Thông tin</p>
              {FOOTER_LINKS.slice(3).map(footerLink)}
            </div>
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-4">
              <p className={footerColTitleCls}>Khách hàng</p>
              {BUYER_LINKS.map(footerLink)}
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    if (footerStyle === "atelier-footer") {
      return (
        <footer
          className="mt-auto bg-[var(--site-band-bg)] text-[var(--site-band-ink)]"
          data-site-footer="atelier-footer"
        >
          <div
            className={cn(
              containerCls,
              SITE_PX,
              "grid grid-cols-1 gap-7 pt-[clamp(36px,5vw,56px)] pb-[clamp(24px,3vw,36px)] lg:grid-cols-12"
            )}
          >
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-5">
              <p className={footerBrandNameCls}>{brandName}</p>
              {tagline && <p className={footerTaglineCls}>{tagline}</p>}
            </div>
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-3">
              <p className={footerColTitleCls}>Cửa hàng xưởng</p>
              {FOOTER_LINKS.map(footerLink)}
            </div>
            <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-4">
              <p className={footerColTitleCls}>Đơn hàng</p>
              {BUYER_LINKS.map(footerLink)}
            </div>
          </div>
          {renderFooterBottom()}
        </footer>
      );
    }

    // bold-dark & fallback
    return (
      <footer
        className="mt-auto bg-[var(--site-band-bg)] text-[var(--site-band-ink)]"
        data-site-footer={footerStyle}
      >
        <div
          className={cn(
            containerCls,
            SITE_PX,
            "grid grid-cols-1 gap-7 pt-[clamp(36px,5vw,56px)] pb-[clamp(24px,3vw,36px)] lg:grid-cols-12"
          )}
        >
          <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-5">
            <p className="text-[clamp(26px,3vw,40px)] leading-[1.04] font-extrabold tracking-[-0.02em] text-inherit">
              {brandName}
            </p>
            {tagline && <p className={footerTaglineCls}>{tagline}</p>}
          </div>
          <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-3">
            <p className={footerColTitleCls}>Cửa hàng</p>
            {FOOTER_LINKS.map(footerLink)}
          </div>
          <div className="flex min-w-0 flex-col gap-2.5 lg:col-span-4">
            <p className={footerColTitleCls}>Khách hàng</p>
            {BUYER_LINKS.map(footerLink)}
          </div>
        </div>
        {renderFooterBottom()}
      </footer>
    );
  };

  const showAnnouncementStrip =
    layout.show_announcement &&
    layout.announcement_text.trim().length > 0 &&
    headerStyle !== "centered-masthead";

  return (
    <SiteThemeRoot
      className={cn(
        "flex min-h-svh flex-col overflow-x-clip bg-background font-sans leading-[1.55] text-foreground",
        layout.archetype === "dense-grid" ? "text-sm" : "text-base"
      )}
      theme={{
        bg_color: theme.bg_color,
        surface_color: theme.surface_color,
        text_color: theme.text_color,
        accent_color: theme.accent_color,
        accent_text_color: theme.accent_text_color,
        font_family: theme.font_family,
        radius,
        density,
      }}
      archetype={layout.archetype}
      headerStyle={headerStyle}
      footerStyle={footerStyle}
      containerWidth={layout.container_width}
      cardStyle={layout.card_style}
      radius={radius}
      density={density}
      previewMode={previewMode}
    >
      {showAnnouncementStrip && (
        <div
          className={cn(
            "w-full bg-[var(--site-band-bg)] py-2 text-center text-[13px] leading-[1.4] font-medium text-[var(--site-band-ink)]",
            SITE_PX
          )}
          role="note"
        >
          {layout.announcement_text}
        </div>
      )}

      {renderHeader()}

      <main id="main-content" tabIndex={-1} className="w-full flex-1">
        {children}
      </main>

      {renderFooter()}

      {!previewMode && shopId && (showAssistantSlot || showChatSlot) && (
        <SiteSupportDock
          shopId={shopId}
          brandName={brandName}
          showChat={showChatSlot}
          showAssistant={showAssistantSlot}
        />
      )}

      {!previewMode && toaster}
    </SiteThemeRoot>
  );
}
