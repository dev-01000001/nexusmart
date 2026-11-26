import React from "react";
import Link from "next/link";
import { MessageSquareQuote, Package, TableProperties, Ticket } from "lucide-react";
import { EmptyState } from "@/components/app/EmptyState";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeHref } from "@/lib/site/theme";
import type { SiteConfig, SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { SafeActionLink } from "./SafeActionLink";
import { SiteProductCard } from "./SiteProductCard";
import { renderHeroSection } from "./sections/SiteHeroSection";
import {
  renderInlineBlocks,
  renderMaskedHeading,
  renderSingleBlock,
  renderStatCard,
  SITE_PRIMARY_BTN_CLS,
  SITE_SECTION_LINK_CLS,
} from "./sections/SiteSectionBlocks";
import {
  archetypeTitleTone,
  containerWidthClass,
  sectionPaddingClass,
  sectionSurfaceClass,
  SITE_PX,
} from "./shell/siteStyleVars";
import type {
  SectionSurface,
  SiteArchetype,
  SiteCardStyle,
  SiteSectionBlock,
  SiteSectionV2,
  SiteTemplateConfig,
} from "./templateTypes";
import {
  createDefaultLayoutConfig,
  createDefaultPagesConfig,
  normalizeSiteTemplateConfig,
  sanitizeSafeImageUrl,
} from "./templateValidator";

export interface SiteRendererProps {
  config: SiteConfig | SiteTemplateConfig;
  shop: SitePublicShopDTO;
  products: Product[];
  vouchers: Voucher[];
  categories?: CategoryInfo[];
  previewMode?: boolean;
  /** Optional override section list (used by Catalog, PDP, Cart, and Info sub-pages). */
  sectionsOverride?: SiteSectionV2[];
}

const CRAFT_FILMSTRIP_IMAGES: readonly string[] = [
  "/atmosphere/cotton-boll-harvest.jpg",
  "/atmosphere/cotton-jersey-weave.webp",
  "/atmosphere/cotton-jersey-drape.webp",
  "/banners/vietnam-workshop.jpg",
];

function formatVoucherBenefit(voucher: Voucher): string {
  if (voucher.discountType === "percent") {
    const cap = voucher.maxDiscount ? ` (tối đa ${formatVnd(voucher.maxDiscount)})` : "";
    return `Giảm ${voucher.discountValue}%${cap}`;
  }
  return `Giảm ${formatVnd(voucher.discountValue)}`;
}

function selectFeaturedProducts(
  allProducts: Product[],
  productIds: string[] | undefined,
  limit: number
): Product[] {
  const cap = Math.max(1, Math.min(24, limit > 0 ? limit : 8));
  if (productIds && productIds.length > 0) {
    const picked: Product[] = [];
    const seen = new Set<string>();
    for (const rawId of productIds) {
      const target = rawId.trim().toLowerCase();
      if (!target) continue;
      const found = allProducts.find(
        (p) =>
          p.backendId?.toLowerCase() === target ||
          p.id.toLowerCase() === target ||
          p.slug.toLowerCase() === target
      );
      if (found && !seen.has(found.id)) {
        seen.add(found.id);
        picked.push(found);
      }
      if (picked.length >= cap) break;
    }
    if (picked.length > 0) return picked;
  }
  return allProducts.slice(0, cap);
}

function selectCollectionProducts(
  allProducts: Product[],
  categorySlug: string | undefined,
  collectionSlug: string | undefined,
  limit: number
): Product[] {
  const cap = Math.max(1, Math.min(24, limit > 0 ? limit : 4));
  let filtered = allProducts;

  const cleanCat = (categorySlug || "").trim().toLowerCase();
  if (cleanCat) {
    const byCat = filtered.filter((p) => p.categorySlug.toLowerCase() === cleanCat);
    if (byCat.length > 0) filtered = byCat;
  }

  const cleanCol = (collectionSlug || "").trim().toLowerCase();
  if (cleanCol) {
    const byCol = filtered.filter((p) =>
      (p.collectionSlugs ?? []).some((slug) => slug.toLowerCase() === cleanCol)
    );
    if (byCol.length > 0) filtered = byCol;
  }

  return filtered.slice(0, cap);
}

/**
 * Orphan-free Tailwind product grid resolver preserving each archetype's structural signature:
 * - 1 item -> full-row single wide showcase
 * - 2 items -> 2 columns
 * - minimal-fashion -> staggered 3-column gallery
 * - editorial -> asymmetric magazine grid (first card spans 2 cols)
 * - dense-grid -> 5-column wall
 * - luxury-maison -> spacious 2-column gallery
 * - multi-sku-catalog -> 2-col compact-row list
 */
function resolveProductGridClass(
  archetype: SiteArchetype,
  variant: string | undefined,
  columns: number | undefined,
  count: number
): string {
  if (count <= 1) return "grid grid-cols-1";
  if (count === 2) return "grid grid-cols-1 gap-[clamp(20px,3vw,36px)] sm:grid-cols-2";
  if (variant === "list-sku") return "grid grid-cols-1 gap-3 lg:grid-cols-2";
  if (
    variant === "editorial-asym" ||
    (archetype === "editorial" && (!variant || variant === "grid-4"))
  ) {
    return "grid grid-cols-2 items-start gap-3 lg:grid-cols-3 lg:gap-[clamp(18px,2.4vw,32px)] [&>:first-child]:col-span-2";
  }
  if (
    archetype === "minimal-fashion" &&
    (!variant || variant === "grid-3" || variant === "grid-4")
  ) {
    return "grid grid-cols-2 items-start gap-3 lg:grid-cols-3 lg:gap-[clamp(20px,2.8vw,36px)] lg:[&>:nth-child(3n+2)]:mt-[clamp(28px,4vw,56px)]";
  }
  if (variant === "dense-5" || columns === 5 || archetype === "dense-grid") {
    if (count >= 5) return "grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5 lg:gap-4";
    if (count === 3)
      return "grid grid-cols-2 gap-3 lg:grid-cols-3 lg:gap-[var(--site-gutter,24px)]";
    return "grid grid-cols-2 gap-3 lg:grid-cols-4 lg:gap-[var(--site-gutter,24px)]";
  }
  if (variant === "grid-2" || columns === 2 || archetype === "luxury-maison") {
    return "grid grid-cols-1 gap-[clamp(20px,3vw,36px)] sm:grid-cols-2";
  }
  if (variant === "grid-3" || columns === 3 || count === 3) {
    return "grid grid-cols-2 gap-3 lg:grid-cols-3 lg:gap-[var(--site-gutter,24px)]";
  }
  if (count === 5) return "grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5 lg:gap-4";
  return "grid grid-cols-2 gap-3 lg:grid-cols-4 lg:gap-[var(--site-gutter,24px)]";
}

function sectionSurfaceAttr(section: SiteSectionV2, fallback: SectionSurface): SectionSurface {
  return section.settings?.surface || fallback;
}

function sectionPaddingAttr(section: SiteSectionV2): string | undefined {
  return section.settings?.padding || undefined;
}

function subtitleColorForSurface(surface: SectionSurface): string {
  return surface === "band" || surface === "accent-soft"
    ? "text-current opacity-85"
    : "text-muted-foreground";
}

function renderFeaturedProductsSection(
  section: Extract<SiteSectionV2, { type: "featured_products" }>,
  tpl: SiteTemplateConfig,
  products: Product[],
  cardStyle: SiteCardStyle,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const maxItems =
    settings?.max_items && settings.max_items > 0 ? settings.max_items : (props.limit ?? 8);
  const items = selectFeaturedProducts(products, props.product_ids, maxItems);
  const gridClass = resolveProductGridClass(archetype, variant, settings?.columns, items.length);
  const effectiveCardStyle: SiteCardStyle = variant === "list-sku" ? "compact-row" : cardStyle;
  const isCentered = settings?.align === "center" || archetype === "luxury-maison";
  const surf = sectionSurfaceAttr(section, "bg");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="featured_products"
      data-section-variant={variant || "grid-4"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div
          className={
            isCentered
              ? "mb-[clamp(28px,3.5vw,44px)] flex flex-col items-center gap-2.5 text-center"
              : "mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4"
          }
        >
          <div className={cn("flex min-w-0 flex-col gap-2", isCentered && "items-center")}>
            {renderMaskedHeading("h2", props.title || "Sản phẩm nổi bật", titleCls)}
            {props.subtitle?.trim() && (
              <p
                className={cn(
                  "max-w-[54ch] text-base leading-[1.55]",
                  subtitleColorForSurface(surf)
                )}
              >
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && (
            <Link href="/san-pham" className={SITE_SECTION_LINK_CLS}>
              Xem tất cả
            </Link>
          )}
        </div>

        {items.length > 0 ? (
          <div className={gridClass} data-reveal="rows">
            {items.map((product, idx) => (
              <SiteProductCard
                key={product.id}
                product={product}
                previewMode={previewMode}
                index={idx}
                cardStyle={effectiveCardStyle}
                layoutMode={items.length === 1 ? "single-wide" : "default"}
                showExcerpt={variant === "editorial-asym" && idx === 0}
              />
            ))}
          </div>
        ) : (
          <EmptyState
            icon={Package}
            title="Cửa hàng đang cập nhật sản phẩm mới."
            className="border border-border shadow-none"
          />
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderCollectionSection(
  section: Extract<SiteSectionV2, { type: "collection" }>,
  tpl: SiteTemplateConfig,
  products: Product[],
  cardStyle: SiteCardStyle,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const maxItems =
    settings?.max_items && settings.max_items > 0 ? settings.max_items : (props.limit ?? 4);
  const items = selectCollectionProducts(
    products,
    props.category_slug,
    props.collection_slug,
    maxItems
  );
  const bannerImg =
    sanitizeSafeImageUrl(props.image_url) ||
    sanitizeSafeImageUrl(tpl.theme.banner_url) ||
    sanitizeSafeImageUrl(items[0]?.onModelImage) ||
    "/banners/fabric-closeup.jpg";
  const viewHref = props.category_slug
    ? `/san-pham?cat=${encodeURIComponent(props.category_slug)}`
    : "/san-pham";
  const gridClass = resolveProductGridClass(archetype, undefined, settings?.columns, items.length);
  const surf = sectionSurfaceAttr(section, "surface");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="collection"
      data-section-variant={variant || "split"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        {variant === "compact" ? (
          <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
            <div className="flex min-w-0 flex-col gap-2">
              {renderMaskedHeading("h2", props.title || "Bộ sưu tập", titleCls)}
              {props.subtitle?.trim() && (
                <p
                  className={cn(
                    "max-w-[54ch] text-base leading-[1.55]",
                    subtitleColorForSurface(surf)
                  )}
                >
                  {props.subtitle}
                </p>
              )}
            </div>
            <SafeActionLink
              href={viewHref}
              label="Xem tất cả"
              className={SITE_SECTION_LINK_CLS}
              previewMode={previewMode}
            />
          </div>
        ) : (
          <div className="mb-[clamp(24px,3vw,40px)] grid grid-cols-1 items-stretch gap-[clamp(20px,3vw,36px)] lg:grid-cols-12">
            <div
              className="aspect-[16/10] min-w-0 overflow-hidden rounded-lg bg-muted lg:col-span-7"
              data-reveal="clip"
            >
              <img
                src={bannerImg}
                alt={props.title || "Bộ sưu tập"}
                width={1365}
                height={768}
                className="block size-full object-cover"
              />
            </div>
            <div className="flex min-w-0 flex-col justify-center gap-3.5 rounded-lg bg-background p-[clamp(22px,3vw,36px)] text-foreground lg:col-span-5">
              {renderMaskedHeading("h2", props.title || "Bộ sưu tập", titleCls)}
              {props.subtitle?.trim() && (
                <p className="max-w-[54ch] text-base leading-[1.55] text-muted-foreground">
                  {props.subtitle}
                </p>
              )}
              <div className="mt-1 flex flex-wrap gap-3">
                <SafeActionLink
                  href={viewHref}
                  label="Khám phá"
                  className={SITE_PRIMARY_BTN_CLS}
                  previewMode={previewMode}
                />
              </div>
            </div>
          </div>
        )}

        {items.length > 0 && (
          <div className={gridClass} data-reveal="rows">
            {items.map((product, idx) => (
              <SiteProductCard
                key={product.id}
                product={product}
                previewMode={previewMode}
                index={idx}
                cardStyle={cardStyle}
                layoutMode={items.length === 1 ? "single-wide" : "default"}
              />
            ))}
          </div>
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderTextSection(
  section: Extract<SiteSectionV2, { type: "text" }>,
  tpl: SiteTemplateConfig,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const paragraphs = props.body
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean);
  const isCentered =
    variant === "centered" || props.align === "center" || settings?.align === "center";
  const surf = sectionSurfaceAttr(section, "bg");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));
  const proseCls = "max-w-[62ch] text-base leading-[1.65] text-inherit";

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="text"
      data-section-variant={variant || "split"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        {variant === "monograph" ? (
          <div className="mx-auto flex max-w-[56ch] flex-col items-center gap-4 py-[clamp(24px,3.5vw,44px)] text-center">
            {renderMaskedHeading("h2", props.title, titleCls)}
            {paragraphs.map((para, idx) => (
              <p key={idx} className={proseCls}>
                {para}
              </p>
            ))}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : variant === "pullquote" ? (
          <div className="grid grid-cols-1 items-start gap-[clamp(24px,3vw,48px)] lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
            <h2
              className={cn(
                "text-[clamp(26px,2.8vw,38px)] leading-[1.12] text-inherit",
                archetypeTitleTone(archetype)
              )}
            >
              {props.title}
            </h2>
            <div className="flex min-w-0 flex-col gap-3.5">
              {paragraphs.map((para, idx) => (
                <p key={idx} className={proseCls}>
                  {para}
                </p>
              ))}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        ) : isCentered ? (
          <div className="mx-auto flex max-w-[60ch] flex-col items-center gap-4 text-center">
            {renderMaskedHeading("h2", props.title, titleCls)}
            {paragraphs.map((para, idx) => (
              <p key={idx} className={proseCls}>
                {para}
              </p>
            ))}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : (
          <div className="grid grid-cols-1 items-start gap-[clamp(20px,3vw,44px)] lg:grid-cols-12">
            <div className="min-w-0 lg:col-span-5">
              {renderMaskedHeading("h2", props.title, titleCls)}
            </div>
            <div className="flex min-w-0 flex-col gap-3.5 lg:col-span-7">
              {paragraphs.map((para, idx) => (
                <p key={idx} className={proseCls}>
                  {para}
                </p>
              ))}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

function renderBannerSection(
  section: Extract<SiteSectionV2, { type: "banner" }>,
  tpl: SiteTemplateConfig,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const imgSrc =
    sanitizeSafeImageUrl(props.image_url) ||
    sanitizeSafeImageUrl(tpl.theme.banner_url) ||
    "/banners/campaign-everyday.jpg";
  const surf = sectionSurfaceAttr(
    section,
    variant === "framed" ? "surface" : variant === "compact" ? "accent-soft" : "band"
  );
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="banner"
      data-section-variant={variant || "band"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        {variant === "compact" ? (
          <div className="flex flex-wrap items-center justify-between gap-x-7 gap-y-4">
            <div className="flex min-w-0 flex-col gap-2">
              <h2 className={titleCls}>{props.title}</h2>
              {props.subtitle?.trim() && (
                <p className="max-w-[60ch] text-base leading-[1.6] text-inherit opacity-90">
                  {props.subtitle}
                </p>
              )}
            </div>
            {props.cta_label?.trim() && (
              <SafeActionLink
                href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                label={props.cta_label}
                className={SITE_PRIMARY_BTN_CLS}
                previewMode={previewMode}
              />
            )}
          </div>
        ) : variant === "framed" ? (
          <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-card p-[clamp(28px,4vw,48px)] text-center text-card-foreground">
            <h2 className={titleCls}>{props.title}</h2>
            {props.subtitle?.trim() && (
              <p className="max-w-[60ch] text-base leading-[1.6] text-muted-foreground">
                {props.subtitle}
              </p>
            )}
            {props.cta_label?.trim() && (
              <SafeActionLink
                href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                label={props.cta_label}
                className={SITE_PRIMARY_BTN_CLS}
                previewMode={previewMode}
              />
            )}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : variant === "split-image" ? (
          <div className="grid grid-cols-1 items-center gap-[clamp(20px,3vw,44px)] lg:grid-cols-2">
            <div
              className="aspect-video min-w-0 overflow-hidden rounded-lg bg-muted"
              data-reveal="clip"
            >
              <img
                src={imgSrc}
                alt={props.title}
                width={1365}
                height={768}
                className="block size-full object-cover"
              />
            </div>
            <div className="flex min-w-0 flex-col gap-3.5">
              {renderMaskedHeading("h2", props.title, titleCls)}
              {props.subtitle?.trim() && (
                <p className="max-w-[52ch] text-[17px] leading-[1.55] text-inherit opacity-90">
                  {props.subtitle}
                </p>
              )}
              {props.cta_label?.trim() && (
                <div className="mt-1 flex flex-wrap gap-3">
                  <SafeActionLink
                    href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                    label={props.cta_label}
                    className={SITE_PRIMARY_BTN_CLS}
                    previewMode={previewMode}
                  />
                </div>
              )}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-1 items-center gap-[clamp(20px,3vw,44px)] lg:grid-cols-12">
            <div className="flex min-w-0 flex-col gap-3.5 lg:col-span-5">
              {renderMaskedHeading("h2", props.title, titleCls)}
              {props.subtitle?.trim() && (
                <p className="max-w-[52ch] text-[17px] leading-[1.55] text-inherit opacity-90">
                  {props.subtitle}
                </p>
              )}
              {props.cta_label?.trim() && (
                <div className="mt-1 flex flex-wrap gap-3">
                  <SafeActionLink
                    href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                    label={props.cta_label}
                    className={SITE_PRIMARY_BTN_CLS}
                    previewMode={previewMode}
                  />
                </div>
              )}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
            <div
              className="aspect-video min-w-0 overflow-hidden rounded-lg bg-muted lg:col-span-7"
              data-reveal="clip"
            >
              <img
                src={imgSrc}
                alt={props.title}
                width={1365}
                height={768}
                className="block size-full object-cover"
              />
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

function renderVoucherStripSection(
  section: Extract<SiteSectionV2, { type: "voucher_strip" }>,
  tpl: SiteTemplateConfig,
  vouchers: Voucher[],
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const cap = Math.max(1, Math.min(6, props.limit && props.limit > 0 ? props.limit : 6));
  const shopOnlyVouchers = vouchers.filter((v) => v.scope === "shop").slice(0, cap);
  const surf = sectionSurfaceAttr(section, "surface");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="voucher_strip"
      data-section-variant={variant || "grid"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
          <div className="flex min-w-0 flex-col gap-2">
            {renderMaskedHeading("h2", props.title || "Ưu đãi cửa hàng", titleCls)}
            {props.subtitle?.trim() && (
              <p
                className={cn(
                  "max-w-[54ch] text-base leading-[1.55]",
                  subtitleColorForSurface(surf)
                )}
              >
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && shopOnlyVouchers.length > 0 && (
            <Link href="/voucher" className={SITE_SECTION_LINK_CLS}>
              Tất cả mã giảm giá
            </Link>
          )}
        </div>

        {shopOnlyVouchers.length === 1 ? (
          <article className="grid grid-cols-1 items-center gap-x-8 gap-y-3.5 rounded-lg border border-border bg-background p-[clamp(22px,3vw,36px)] text-foreground md:grid-cols-[auto_minmax(0,1fr)_auto]">
            <div className="flex items-center">
              <span className="text-[clamp(28px,3.6vw,44px)] leading-[1.04] font-extrabold tracking-[0.02em] text-brand">
                {shopOnlyVouchers[0].code}
              </span>
            </div>
            <div className="flex min-w-0 flex-col gap-1">
              <h3 className="text-lg font-semibold text-foreground">
                {formatVoucherBenefit(shopOnlyVouchers[0])}
              </h3>
              <p className="text-sm leading-[1.5] text-muted-foreground tabular-nums">
                {shopOnlyVouchers[0].minOrderValue > 0
                  ? `Đơn từ ${formatVnd(shopOnlyVouchers[0].minOrderValue)}`
                  : "Áp dụng mọi đơn tại cửa hàng"}
                {`, còn ${formatInt(shopOnlyVouchers[0].remainingUses)} lượt`}
              </p>
            </div>
            <div className="flex items-center">
              {previewMode ? (
                <span className={SITE_PRIMARY_BTN_CLS}>Lưu mã</span>
              ) : (
                <Link href="/voucher" className={SITE_PRIMARY_BTN_CLS}>
                  Lưu mã
                </Link>
              )}
            </div>
          </article>
        ) : shopOnlyVouchers.length > 1 ? (
          variant === "compact-bar" ? (
            <div
              className={cn(
                "grid grid-cols-1 gap-3 sm:grid-cols-2",
                shopOnlyVouchers.length === 2 ? "lg:grid-cols-2" : "lg:grid-cols-3"
              )}
              data-reveal="rows"
            >
              {shopOnlyVouchers.map((voucher) => (
                <article
                  key={voucher.id || voucher.code}
                  className="flex items-center justify-between gap-3 rounded-lg border border-border bg-card px-4 py-3.5 text-card-foreground"
                >
                  <div className="min-w-0">
                    <p className="text-lg font-bold tracking-[0.02em] text-brand">{voucher.code}</p>
                    <p className="text-sm leading-[1.5] text-muted-foreground">
                      {formatVoucherBenefit(voucher)}
                    </p>
                  </div>
                  {previewMode ? (
                    <span className={SITE_SECTION_LINK_CLS}>Lưu</span>
                  ) : (
                    <Link href="/voucher" className={SITE_SECTION_LINK_CLS}>
                      Lưu
                    </Link>
                  )}
                </article>
              ))}
            </div>
          ) : (
            <div
              className={cn(
                "grid grid-cols-1 gap-4 sm:grid-cols-2",
                shopOnlyVouchers.length === 2 ? "lg:grid-cols-2" : "lg:grid-cols-3"
              )}
              data-reveal="rows"
            >
              {shopOnlyVouchers.map((voucher) => (
                <article
                  key={voucher.id || voucher.code}
                  className="flex min-w-0 flex-col gap-2.5 rounded-lg border border-border bg-background p-[clamp(20px,2.4vw,28px)] text-foreground"
                >
                  <p className="text-xl font-bold tracking-[0.02em] text-brand">{voucher.code}</p>
                  <h3 className="text-lg font-semibold text-foreground">
                    {formatVoucherBenefit(voucher)}
                  </h3>
                  <p className="text-sm leading-[1.5] text-muted-foreground">
                    {voucher.minOrderValue > 0
                      ? `Đơn từ ${formatVnd(voucher.minOrderValue)}`
                      : "Áp dụng toàn cửa hàng"}
                  </p>
                  <div className="mt-auto flex flex-wrap items-center justify-between gap-2.5 pt-2">
                    <span className="text-sm text-muted-foreground tabular-nums">
                      Còn {formatInt(voucher.remainingUses)} lượt
                    </span>
                    {previewMode ? (
                      <span className={SITE_SECTION_LINK_CLS}>Lưu mã</span>
                    ) : (
                      <Link href="/voucher" className={SITE_SECTION_LINK_CLS}>
                        Lưu mã
                      </Link>
                    )}
                  </div>
                </article>
              ))}
            </div>
          )
        ) : (
          <EmptyState
            icon={Ticket}
            title="Chưa có mã giảm giá đang mở."
            className="border border-border shadow-none"
          />
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderPoliciesSection(
  section: Extract<SiteSectionV2, { type: "policies" }>,
  tpl: SiteTemplateConfig,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const policyBlocks = (blocks ?? []).filter((b) => b.type === "policy");
  const extraBlocks = (blocks ?? []).filter((b) => b.type !== "policy");
  const items = [
    ...props.items,
    ...policyBlocks.map((b) => ({ heading: b.props.heading, detail: b.props.detail })),
  ];
  const surf = sectionSurfaceAttr(section, "bg");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  const useCraftFilmstrip = archetype === "craft-atelier" && (!variant || variant === "timeline");

  const gridClass =
    variant === "timeline"
      ? "grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4"
      : variant === "compact-list"
        ? "grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4"
        : variant === "cards" || items.length === 3
          ? "grid grid-cols-1 gap-[var(--site-gutter,24px)] sm:grid-cols-2 lg:grid-cols-3"
          : "grid grid-cols-1 gap-[clamp(16px,2vw,24px)] sm:grid-cols-2";

  const itemCardClass = cn(
    "flex min-w-0 flex-col gap-2 rounded-lg border border-border",
    surf === "surface" ? "bg-background text-foreground" : "bg-card text-card-foreground",
    variant === "compact-list" ? "p-[18px]" : "p-[clamp(20px,2.5vw,28px)]"
  );

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="policies"
      data-section-variant={variant || "grid"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
          <div className="flex min-w-0 flex-col gap-2">
            {renderMaskedHeading("h2", props.title || "Chính sách", titleCls)}
          </div>
        </div>

        {useCraftFilmstrip ? (
          <div
            className="flex snap-x snap-mandatory gap-[clamp(14px,2vw,24px)] overflow-x-auto pb-3"
            role="list"
            aria-label={props.title || "Công đoạn tại xưởng"}
            data-reveal="rows"
          >
            {items.map((item, idx) => (
              <article
                key={`${section.id}-${idx}`}
                className="flex flex-[0_0_clamp(260px,28vw,340px)] snap-start flex-col gap-3.5 overflow-hidden rounded-lg border border-border bg-background text-foreground"
                role="listitem"
              >
                <div className="aspect-[4/3] w-full overflow-hidden bg-muted" data-reveal="clip">
                  <img
                    src={CRAFT_FILMSTRIP_IMAGES[idx % CRAFT_FILMSTRIP_IMAGES.length]}
                    alt={item.heading}
                    width={640}
                    height={480}
                    className="block size-full object-cover"
                  />
                </div>
                <div className="flex flex-col gap-1.5 px-4 pb-4">
                  <h3 className="text-lg font-semibold text-foreground">{item.heading}</h3>
                  <p className="max-w-[56ch] text-[15px] leading-[1.55] text-muted-foreground">
                    {item.detail}
                  </p>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <div className={gridClass} data-reveal="rows">
            {items.map((item, idx) => (
              <article key={`${section.id}-${idx}`} className={itemCardClass}>
                <h3 className="text-lg font-semibold text-foreground">{item.heading}</h3>
                <p className="max-w-[56ch] text-[15px] leading-[1.55] text-muted-foreground">
                  {item.detail}
                </p>
              </article>
            ))}
          </div>
        )}

        {renderInlineBlocks(extraBlocks, previewMode)}
      </div>
    </section>
  );
}

function renderLookbookSection(
  section: Extract<SiteSectionV2, { type: "lookbook" }>,
  tpl: SiteTemplateConfig,
  products: Product[],
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const imageBlocks = (blocks ?? []).filter(
    (b): b is Extract<SiteSectionBlock, { type: "image" }> => b.type === "image"
  );
  const otherBlocks = (blocks ?? []).filter((b) => b.type !== "image");
  const surf = sectionSurfaceAttr(section, "surface");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  const fallbackImages: Array<Extract<SiteSectionBlock, { type: "image" }>> =
    imageBlocks.length > 0
      ? imageBlocks
      : products
          .slice(0, variant === "duo-split" ? 2 : variant === "strip-4" ? 4 : 3)
          .map((p, i) => ({
            id: `${section.id}-fallback-${i}`,
            type: "image",
            props: {
              image_url:
                sanitizeSafeImageUrl(p.onModelImage) ||
                sanitizeSafeImageUrl(p.image) ||
                "/banners/fabric-closeup.jpg",
              alt: p.name,
              caption: "",
              aspect: i === 0 && variant === "asym-3" ? "3:4" : "4:3",
            },
          }));

  const isAsym3 = variant !== "duo-split" && variant !== "strip-4";
  const gridClass =
    variant === "duo-split"
      ? "grid grid-cols-1 gap-[var(--site-gutter,24px)] sm:grid-cols-2"
      : variant === "strip-4"
        ? "grid grid-cols-1 gap-[var(--site-gutter,24px)] sm:grid-cols-2 lg:grid-cols-4"
        : "grid grid-cols-1 gap-[var(--site-gutter,24px)] lg:grid-cols-[minmax(0,7fr)_minmax(0,5fr)] lg:grid-rows-2 [&>:first-child]:lg:row-span-2";

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="lookbook"
      data-section-variant={variant || "asym-3"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
          <div className="flex min-w-0 flex-col gap-2">
            {renderMaskedHeading("h2", props.title || "Lookbook", titleCls)}
            {props.subtitle?.trim() && (
              <p
                className={cn(
                  "max-w-[54ch] text-base leading-[1.55]",
                  subtitleColorForSurface(surf)
                )}
              >
                {props.subtitle}
              </p>
            )}
          </div>
          {props.cta_label?.trim() && (
            <SafeActionLink
              href={sanitizeSafeHref(props.cta_href, "/san-pham")}
              label={props.cta_label}
              className={SITE_SECTION_LINK_CLS}
              previewMode={previewMode}
            />
          )}
        </div>

        <div className={gridClass}>
          {fallbackImages.map((blk, idx) =>
            renderSingleBlock(blk, previewMode, { fillImageHeight: isAsym3 && idx === 0 })
          )}
        </div>

        {renderInlineBlocks(otherBlocks, previewMode)}
      </div>
    </section>
  );
}

function renderMetricsStripSection(
  section: Extract<SiteSectionV2, { type: "metrics_strip" }>,
  tpl: SiteTemplateConfig,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const statBlocks = (blocks ?? []).filter(
    (b): b is Extract<SiteSectionBlock, { type: "stat" }> => b.type === "stat"
  );
  const otherBlocks = (blocks ?? []).filter((b) => b.type !== "stat");
  const surf = sectionSurfaceAttr(section, "surface");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  const isCards3 = variant !== "telemetry" && (variant === "cards-3" || statBlocks.length === 3);

  const gridClass =
    variant === "telemetry"
      ? "grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4"
      : isCards3
        ? "grid grid-cols-1 gap-[var(--site-gutter,24px)] sm:grid-cols-3"
        : "grid grid-cols-1 divide-y divide-border overflow-hidden rounded-lg border border-border bg-card sm:grid-cols-2 sm:divide-x lg:grid-cols-4 lg:divide-y-0";

  const itemClass =
    variant === "telemetry"
      ? "flex min-w-0 flex-col gap-1.5 rounded-lg border border-border bg-background p-[clamp(18px,2.2vw,24px)]"
      : isCards3
        ? "flex min-w-0 flex-col gap-1.5 rounded-lg border border-border bg-background p-[clamp(20px,2.5vw,28px)]"
        : "flex min-w-0 flex-col gap-1.5 bg-card p-[clamp(20px,2.5vw,28px)]";

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="metrics_strip"
      data-section-variant={variant || "bar-4"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
          <div className="flex min-w-0 flex-col gap-2">
            {renderMaskedHeading("h2", props.title, titleCls)}
            {props.subtitle?.trim() && (
              <p
                className={cn(
                  "max-w-[54ch] text-base leading-[1.55]",
                  subtitleColorForSurface(surf)
                )}
              >
                {props.subtitle}
              </p>
            )}
          </div>
        </div>

        {statBlocks.length > 0 && (
          <div className={gridClass} data-reveal="rows">
            {statBlocks.map((b) => renderStatCard(b, itemClass))}
          </div>
        )}

        {renderInlineBlocks(otherBlocks, previewMode)}
      </div>
    </section>
  );
}

function renderSkuMatrixSection(
  section: Extract<SiteSectionV2, { type: "sku_matrix" }>,
  tpl: SiteTemplateConfig,
  products: Product[],
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const cleanCat = (props.category_slug || "").trim().toLowerCase();
  const matchingProducts = cleanCat
    ? products.filter((p) => p.categorySlug.toLowerCase() === cleanCat)
    : products;
  const showOos = props.show_out_of_stock !== false;
  const maxRows = Math.max(
    1,
    Math.min(
      24,
      settings?.max_items && settings.max_items > 0 ? settings.max_items : (props.limit ?? 12)
    )
  );
  const surf = sectionSurfaceAttr(section, "surface");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  const colorMapByProduct = new Map<string, Map<string, { name: string; hex: string }>>();
  for (const p of matchingProducts) {
    const m = new Map<string, { name: string; hex: string }>();
    for (const c of p.colors) {
      m.set(c.code, { name: c.name, hex: c.hex });
    }
    colorMapByProduct.set(p.id, m);
  }

  const rows = matchingProducts
    .flatMap((p) =>
      p.skus
        .filter((s) => showOos || s.stock > 0)
        .map((sku) => {
          const color = colorMapByProduct.get(p.id)?.get(sku.colorCode);
          return {
            key: `${p.id}-${sku.skuCode}-${sku.colorCode}-${sku.size}`,
            product: p,
            sku,
            colorName: color?.name ?? sku.colorCode,
            colorHex: color?.hex ?? "currentColor",
          };
        })
    )
    .slice(0, maxRows);

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="sku_matrix"
      data-section-variant={variant || "compact-table"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
          <div className="flex min-w-0 flex-col gap-2">
            {renderMaskedHeading("h2", props.title || "Bảng SKU đặt nhanh", titleCls)}
            {props.subtitle?.trim() && (
              <p
                className={cn(
                  "max-w-[54ch] text-base leading-[1.55]",
                  subtitleColorForSurface(surf)
                )}
              >
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && (
            <Link href="/san-pham" className={SITE_SECTION_LINK_CLS}>
              Toàn bộ bảng SKU
            </Link>
          )}
        </div>

        {rows.length === 0 ? (
          <EmptyState
            icon={TableProperties}
            title="Chưa có biến thể SKU hiển thị."
            className="border border-border shadow-none"
          />
        ) : variant === "grouped" ? (
          <div className="flex flex-col gap-3.5" data-reveal="rows">
            {matchingProducts.slice(0, Math.min(8, maxRows)).map((product) => {
              const skus = product.skus.filter((s) => showOos || s.stock > 0);
              if (skus.length === 0) return null;
              return (
                <article
                  key={product.id}
                  className="flex flex-col gap-3 rounded-lg border border-border bg-background p-5 text-foreground"
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <h3 className="text-lg font-semibold text-foreground">{product.name}</h3>
                      <p className="text-[13px] text-muted-foreground tabular-nums">
                        {formatVnd(product.price)}
                      </p>
                    </div>
                    {previewMode ? (
                      <span className={SITE_SECTION_LINK_CLS}>Đặt mua</span>
                    ) : (
                      <Link
                        href={`/san-pham/${encodeURIComponent(product.slug)}`}
                        className={SITE_SECTION_LINK_CLS}
                      >
                        Đặt mua
                      </Link>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {skus.slice(0, 12).map((sku) => {
                      const colorName =
                        colorMapByProduct.get(product.id)?.get(sku.colorCode)?.name ??
                        sku.colorCode;
                      return (
                        <Badge
                          key={sku.skuCode}
                          variant={sku.stock > 0 ? "secondary" : "outline"}
                          className="gap-1.5 rounded-sm px-2.5 py-1 text-[13px] tabular-nums"
                        >
                          <strong className="font-semibold">
                            {colorName} / {sku.size}
                          </strong>
                          <span>{sku.stock > 0 ? `Còn ${formatInt(sku.stock)}` : "Hết hàng"}</span>
                        </Badge>
                      );
                    })}
                  </div>
                </article>
              );
            })}
          </div>
        ) : (
          <div className="overflow-hidden rounded-lg border border-border bg-background">
            <Table className="min-w-[620px]">
              <TableHeader className="bg-muted">
                <TableRow className="hover:bg-transparent">
                  <TableHead>Sản phẩm</TableHead>
                  <TableHead>SKU</TableHead>
                  <TableHead>Màu</TableHead>
                  <TableHead>Size</TableHead>
                  <TableHead>Đơn giá</TableHead>
                  <TableHead>Tồn</TableHead>
                  <TableHead>Đặt nhanh</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody data-reveal="rows">
                {rows.map((row) => (
                  <TableRow key={row.key}>
                    <TableCell className="font-semibold whitespace-normal text-foreground">
                      {row.product.name}
                    </TableCell>
                    <TableCell className="text-[13px] text-muted-foreground">
                      {row.sku.skuCode}
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex items-center gap-2">
                        <span
                          className="size-3 shrink-0 rounded-full border border-foreground/25"
                          style={{ backgroundColor: row.colorHex }}
                          aria-hidden="true"
                        />
                        <span>{row.colorName}</span>
                      </span>
                    </TableCell>
                    <TableCell>{row.sku.size}</TableCell>
                    <TableCell>{formatVnd(row.sku.price)}</TableCell>
                    <TableCell>
                      <Badge
                        variant={row.sku.stock > 0 ? "success" : "neutral"}
                        className="tabular-nums"
                      >
                        {row.sku.stock > 0 ? `Còn ${formatInt(row.sku.stock)}` : "Hết hàng"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {previewMode ? (
                        <span className={SITE_SECTION_LINK_CLS}>Chọn</span>
                      ) : (
                        <Link
                          href={`/san-pham/${encodeURIComponent(row.product.slug)}`}
                          className={SITE_SECTION_LINK_CLS}
                        >
                          Đặt mua
                        </Link>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderTestimonialsSection(
  section: Extract<SiteSectionV2, { type: "testimonials" }>,
  tpl: SiteTemplateConfig,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const archetype = tpl.layout.archetype;
  const quoteBlocks = (blocks ?? []).filter(
    (b): b is Extract<SiteSectionBlock, { type: "quote" }> => b.type === "quote"
  );
  const otherBlocks = (blocks ?? []).filter((b) => b.type !== "quote");
  const surf = sectionSurfaceAttr(section, "surface");
  const titleCls = cn("text-[clamp(24px,2.8vw,38px)] text-inherit", archetypeTitleTone(archetype));

  const gridClass =
    variant === "editorial-quotes" || quoteBlocks.length === 2
      ? "grid grid-cols-1 gap-[clamp(20px,3vw,36px)] sm:grid-cols-2"
      : "grid grid-cols-1 gap-[var(--site-gutter,24px)] sm:grid-cols-2 lg:grid-cols-3";

  return (
    <section
      key={section.id}
      id={section.id}
      className={cn(
        "w-full",
        SITE_PX,
        sectionPaddingClass(settings?.padding),
        sectionSurfaceClass(surf)
      )}
      data-section-type="testimonials"
      data-section-variant={variant || "grid-3"}
      data-surface={surf}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={containerWidthClass(tpl.layout.container_width)}>
        <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
          <div className="flex min-w-0 flex-col gap-2">
            {renderMaskedHeading("h2", props.title || "Nhận xét khách hàng", titleCls)}
            {props.subtitle?.trim() && (
              <p
                className={cn(
                  "max-w-[54ch] text-base leading-[1.55]",
                  subtitleColorForSurface(surf)
                )}
              >
                {props.subtitle}
              </p>
            )}
          </div>
        </div>

        {quoteBlocks.length > 0 ? (
          <div className={gridClass} data-reveal="rows">
            {quoteBlocks.map((q) => (
              <blockquote
                key={q.id}
                className="m-0 flex flex-col justify-between gap-4 rounded-lg border border-border bg-background p-[clamp(20px,2.6vw,28px)] text-foreground"
              >
                <p className="text-base leading-[1.6] text-foreground">“{q.props.body}”</p>
                <footer>
                  <p className="text-sm font-semibold text-foreground">{q.props.author}</p>
                  {q.props.role?.trim() && (
                    <p className="text-[13px] text-muted-foreground">{q.props.role}</p>
                  )}
                </footer>
              </blockquote>
            ))}
          </div>
        ) : (
          <EmptyState
            icon={MessageSquareQuote}
            title="Chưa có nhận xét nào."
            className="border border-border shadow-none"
          />
        )}

        {renderInlineBlocks(otherBlocks, previewMode)}
      </div>
    </section>
  );
}

/**
 * Pure React multi-template renderer for all 11 Declarative Theme JSON v2 section types,
 * their structural variants, section settings, and all 9 typed blocks.
 * Zero `dangerouslySetInnerHTML`, zero raw HTML/CSS injection.
 */
export function SiteRenderer({
  config,
  shop,
  products,
  vouchers,
  categories = [],
  previewMode = false,
  sectionsOverride,
}: SiteRendererProps) {
  const tpl: SiteTemplateConfig = normalizeSiteTemplateConfig(config, shop.name) ?? {
    schema_version: 2,
    template_id: "legacy-v1",
    theme: config.theme,
    layout: createDefaultLayoutConfig(),
    sections: [],
    pages: createDefaultPagesConfig(),
  };

  const sourceSections = sectionsOverride ?? tpl.sections;
  const visibleSections = sourceSections.filter((s) => s.visible !== false);
  const firstHeroIndex = visibleSections.findIndex((s) => s.type === "hero");
  const archetype = tpl.layout.archetype;
  const cardStyle: SiteCardStyle = tpl.layout.card_style;

  if (sectionsOverride && visibleSections.length === 0) {
    return null;
  }

  if (visibleSections.length === 0) {
    const brandName = tpl.theme.brand_name?.trim() || shop.name;
    return (
      <section className={cn("w-full py-[clamp(28px,4vw,56px)]", SITE_PX)}>
        <div className={containerWidthClass(tpl.layout.container_width)}>
          <div
            className="flex flex-col items-start gap-3 rounded-lg border border-border bg-card p-[clamp(24px,4vw,44px)] text-card-foreground"
            data-rise=""
            style={{ "--i": 0 } as React.CSSProperties}
          >
            <h1
              className={cn(
                "text-[clamp(32px,4.2vw,56px)] text-foreground",
                archetypeTitleTone(archetype)
              )}
            >
              {brandName}
            </h1>
            {tpl.theme.tagline?.trim() && (
              <p className="max-w-[52ch] text-[17px] leading-[1.55] text-muted-foreground">
                {tpl.theme.tagline}
              </p>
            )}
            <div className="mt-1 flex flex-wrap gap-3">
              <SafeActionLink
                href="/san-pham"
                label="Xem sản phẩm"
                className={SITE_PRIMARY_BTN_CLS}
                previewMode={previewMode}
              />
            </div>
          </div>
        </div>
      </section>
    );
  }

  return (
    <>
      {!sectionsOverride && firstHeroIndex === -1 && (
        <section
          className={cn("w-full pt-[clamp(28px,3.8vw,48px)] pb-[clamp(20px,2.5vw,30px)]", SITE_PX)}
        >
          <div className={containerWidthClass(tpl.layout.container_width)}>
            <h1
              className={cn(
                "text-[clamp(32px,4.4vw,60px)] text-foreground",
                archetypeTitleTone(archetype)
              )}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {tpl.theme.brand_name?.trim() || shop.name}
            </h1>
            {tpl.theme.tagline?.trim() && (
              <p
                className="mt-2.5 max-w-[56ch] text-[17px] leading-[1.55] text-muted-foreground"
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {tpl.theme.tagline}
              </p>
            )}
          </div>
        </section>
      )}

      {visibleSections.map((section, idx) => {
        switch (section.type) {
          case "hero":
            return renderHeroSection(
              section,
              tpl,
              shop,
              products,
              categories,
              !sectionsOverride && idx === firstHeroIndex,
              previewMode
            );
          case "featured_products":
            return renderFeaturedProductsSection(section, tpl, products, cardStyle, previewMode);
          case "collection":
            return renderCollectionSection(section, tpl, products, cardStyle, previewMode);
          case "text":
            return renderTextSection(section, tpl, previewMode);
          case "banner":
            return renderBannerSection(section, tpl, previewMode);
          case "voucher_strip":
            return renderVoucherStripSection(section, tpl, vouchers, previewMode);
          case "policies":
            return renderPoliciesSection(section, tpl, previewMode);
          case "lookbook":
            return renderLookbookSection(section, tpl, products, previewMode);
          case "metrics_strip":
            return renderMetricsStripSection(section, tpl, previewMode);
          case "sku_matrix":
            return renderSkuMatrixSection(section, tpl, products, previewMode);
          case "testimonials":
            return renderTestimonialsSection(section, tpl, previewMode);
        }
      })}
    </>
  );
}
