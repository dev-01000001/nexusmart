import React from "react";
import Link from "next/link";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeHref } from "@/lib/site/theme";
import type { SiteConfig, SitePublicShopDTO } from "@/lib/site/types";
import { DenseGridHeroSearch } from "./DenseGridHeroSearch";
import { MultiSkuDirectory } from "./MultiSkuDirectory";
import { OnePageLaunchHero } from "./OnePageLaunchHero";
import { SafeActionLink } from "./SafeActionLink";
import { parseNumericStat } from "./siteMetrics";
import { SiteProductCard } from "./SiteProductCard";
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
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

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

/** Splits a Vietnamese headline cleanly into two balanced halves for flanking minimal-fashion portrait. */
function splitHeadlineHalves(text: string): [string, string] {
  const words = text.trim().split(/\s+/).filter(Boolean);
  if (words.length <= 1) return [text.trim(), ""];
  const mid = Math.ceil(words.length / 2);
  return [words.slice(0, mid).join(" "), words.slice(mid).join(" ")];
}

function renderStatCard(
  block: Extract<SiteSectionBlock, { type: "stat" }>,
  itemClass: string
): React.ReactNode {
  const parsed = parseNumericStat(block.props.value);
  return (
    <div key={block.id} className={itemClass}>
      <span
        className={`${tplStyles.metricValue} num`}
        data-reveal={parsed ? "count" : undefined}
        data-count-to={parsed ? String(parsed.to) : undefined}
        data-count-prefix={parsed?.prefix || undefined}
        data-count-suffix={parsed?.suffix || undefined}
      >
        {block.props.value}
      </span>
      <span className={tplStyles.metricLabel}>{block.props.label}</span>
      {block.props.note?.trim() && <span className={tplStyles.metricNote}>{block.props.note}</span>}
    </div>
  );
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
 * Orphan-free grid resolver:
 * - 1 item -> full-row horizontal showcase
 * - 2 items -> 2 half-width cards
 * - 3 items -> 3 columns (staggered on minimal-fashion)
 * - Respects archetype signature grids even on legacy v1 converted sites.
 */
function resolveProductGridClass(
  archetype: SiteArchetype,
  variant: string | undefined,
  columns: number | undefined,
  count: number
): string {
  if (count <= 1) return tplStyles.gridSingle;
  if (count === 2) return tplStyles.gridCols2;
  if (variant === "list-sku") return tplStyles.gridListSku;
  if (
    variant === "editorial-asym" ||
    (archetype === "editorial" && (!variant || variant === "grid-4"))
  ) {
    return tplStyles.gridEditorialAsym;
  }
  if (
    archetype === "minimal-fashion" &&
    (!variant || variant === "grid-3" || variant === "grid-4")
  ) {
    return tplStyles.gridStagger3;
  }
  if (variant === "dense-5" || columns === 5 || archetype === "dense-grid") {
    if (count >= 5) return tplStyles.gridCols5;
    if (count === 3) return tplStyles.gridCols3;
    return tplStyles.gridCols4;
  }
  if (variant === "grid-2" || columns === 2 || archetype === "luxury-maison") {
    return tplStyles.gridCols2;
  }
  if (variant === "grid-3" || columns === 3 || count === 3) {
    return tplStyles.gridCols3;
  }
  if (count === 5) return tplStyles.gridCols5;
  return tplStyles.gridCols4;
}

/**
 * Resolves the effective hero mode so every archetype (including legacy v1 seeded sites
 * converted with default `variant: "split"`) displays its unmistakable signature hero.
 */
function resolveEffectiveHeroMode(archetype: SiteArchetype, variant: string | undefined): string {
  const explicit = (variant || "").trim();
  if (
    archetype === "one-page-launch" &&
    (!explicit || explicit === "fullbleed" || explicit === "split")
  ) {
    return "one-page-launch";
  }
  if (
    archetype === "multi-sku-catalog" &&
    (!explicit || explicit === "compact-bar" || explicit === "split")
  ) {
    return "multi-sku-catalog";
  }
  if (
    archetype === "dense-grid" &&
    (!explicit || explicit === "compact-bar" || explicit === "split")
  ) {
    return "dense-grid";
  }
  if (!explicit || explicit === "split") {
    switch (archetype) {
      case "minimal-fashion":
        return "centered";
      case "editorial":
        return "editorial";
      case "craft-atelier":
        return "atelier";
      case "sport-active":
        return "sport-diagonal";
      case "luxury-maison":
        return "luxury-framed";
      case "classic":
        return "fullbleed";
      default:
        return "fullbleed";
    }
  }
  return explicit;
}

function renderMaskedHeading(Tag: "h1" | "h2", text: string, className: string, immediate = false) {
  return (
    <Tag
      className={`${className} ${immediate ? "lines" : ""}`}
      data-reveal={immediate ? undefined : "lines"}
    >
      <span className="line">
        <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
          {text}
        </span>
      </span>
    </Tag>
  );
}

function renderSingleBlock(block: SiteSectionBlock, previewMode: boolean): React.ReactNode {
  switch (block.type) {
    case "heading":
      return block.props.level === "h2" ? (
        <h2 key={block.id} className={tplStyles.blockHeadingH2}>
          {block.props.text}
        </h2>
      ) : (
        <h3 key={block.id} className={tplStyles.blockHeadingH3}>
          {block.props.text}
        </h3>
      );
    case "paragraph":
      return (
        <p key={block.id} className={tplStyles.blockParagraph}>
          {block.props.text}
        </p>
      );
    case "badge":
      return (
        <span
          key={block.id}
          className={tplStyles.blockBadge}
          data-tone={block.props.tone || "neutral"}
        >
          {block.props.label}
        </span>
      );
    case "cta": {
      const styleClass =
        block.props.style === "secondary"
          ? styles.btnSecondary
          : block.props.style === "link"
            ? tplStyles.blockCtaLink
            : styles.btnPrimary;
      return (
        <SafeActionLink
          key={block.id}
          href={block.props.href}
          label={block.props.label}
          className={styleClass}
          previewMode={previewMode}
        />
      );
    }
    case "image": {
      const imgUrl = sanitizeSafeImageUrl(block.props.image_url);
      if (!imgUrl) return null;
      return (
        <figure key={block.id} className={tplStyles.blockFigure}>
          <div
            className={tplStyles.blockImageFrame}
            data-aspect={block.props.aspect || "4:3"}
            data-reveal="clip"
          >
            <img
              src={imgUrl}
              alt={block.props.alt || block.props.caption || "Ảnh cửa hàng"}
              width={1200}
              height={900}
              className={tplStyles.blockImg}
            />
          </div>
          {block.props.caption?.trim() && (
            <figcaption className={tplStyles.blockCaption}>{block.props.caption}</figcaption>
          )}
        </figure>
      );
    }
    case "stat":
      return renderStatCard(block, tplStyles.metricsCard);
    case "policy":
      return (
        <article key={block.id} className={styles.policyItem}>
          <h3 className={styles.policyHeading}>{block.props.heading}</h3>
          <p className={styles.policyDetail}>{block.props.detail}</p>
        </article>
      );
    case "quote":
      return (
        <blockquote key={block.id} className={tplStyles.blockQuote}>
          <p className={tplStyles.blockQuoteBody}>“{block.props.body}”</p>
          <cite className={tplStyles.blockQuoteCite}>
            {block.props.author}
            {block.props.role?.trim() ? `, ${block.props.role}` : ""}
          </cite>
        </blockquote>
      );
    case "feature":
      return (
        <div key={block.id} className={tplStyles.blockFeatureCard}>
          {block.props.tag?.trim() && (
            <span className={tplStyles.blockFeatureTag}>{block.props.tag}</span>
          )}
          <h3 className={tplStyles.blockFeatureTitle}>{block.props.title}</h3>
          <p className={tplStyles.blockFeatureDetail}>{block.props.detail}</p>
        </div>
      );
  }
}

function renderInlineBlocks(blocks: SiteSectionBlock[] | undefined, previewMode: boolean) {
  if (!blocks || blocks.length === 0) return null;
  const inlineBadgesAndCtas = blocks.filter((b) => b.type === "badge" || b.type === "cta");
  const flowBlocks = blocks.filter((b) => b.type !== "badge" && b.type !== "cta");
  return (
    <>
      {inlineBadgesAndCtas.length > 0 && (
        <div className={tplStyles.blocksInlineRow}>
          {inlineBadgesAndCtas.map((b) => renderSingleBlock(b, previewMode))}
        </div>
      )}
      {flowBlocks.length > 0 && (
        <div className={tplStyles.blocksStack}>
          {flowBlocks.map((b) => renderSingleBlock(b, previewMode))}
        </div>
      )}
    </>
  );
}

function sectionSurfaceAttr(section: SiteSectionV2, fallback: SectionSurface): SectionSurface {
  return section.settings?.surface || fallback;
}

function sectionPaddingAttr(section: SiteSectionV2): string | undefined {
  return section.settings?.padding || undefined;
}

function renderHeroSection(
  section: Extract<SiteSectionV2, { type: "hero" }>,
  tpl: SiteTemplateConfig,
  shop: SitePublicShopDTO,
  products: Product[],
  categories: CategoryInfo[],
  isFirstHeading: boolean,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const archetype = tpl.layout.archetype;
  const mode = resolveEffectiveHeroMode(archetype, variant);
  const brandName = tpl.theme.brand_name?.trim() || shop.name;

  const primaryProductImg =
    sanitizeSafeImageUrl(products[0]?.onModelImage) || sanitizeSafeImageUrl(products[0]?.image);
  const secondaryProductImg =
    sanitizeSafeImageUrl(products[1]?.onModelImage) ||
    sanitizeSafeImageUrl(products[1]?.image) ||
    "/banners/fabric-closeup.jpg";

  const heroImg =
    sanitizeSafeImageUrl(props.image_url) ||
    sanitizeSafeImageUrl(tpl.theme.banner_url) ||
    primaryProductImg ||
    "/banners/vietnam-workshop.jpg";

  const ctaLabel = props.cta_label?.trim() || "Xem sản phẩm";
  const ctaHref = sanitizeSafeHref(props.cta_href, "/san-pham");
  const titleText = props.title.trim() || brandName;
  const subtitleText = props.subtitle?.trim() || "";
  const HeadingTag = isFirstHeading ? "h1" : "h2";

  // 1) ONE-PAGE LAUNCH: sticky center product spotlight with 3 color/angle stages + persistent buy bar
  if (mode === "one-page-launch") {
    const rawLaunchHref = props.cta_href?.trim() ? sanitizeSafeHref(props.cta_href, "") : "";
    return (
      <section
        key={section.id}
        id={section.id}
        className={`${tplStyles.sectionWrap} ${tplStyles.heroWrapFlush}`}
        data-section-type="hero"
        data-section-variant={variant || "fullbleed"}
        data-surface={sectionSurfaceAttr(section, "bg")}
      >
        <div className={tplStyles.sectionInner}>
          <OnePageLaunchHero
            title={titleText}
            subtitle={subtitleText}
            ctaLabel={ctaLabel}
            ctaHref={rawLaunchHref}
            fallbackImage={heroImg}
            product={products[0]}
            isFirstHeading={isFirstHeading}
            previewMode={previewMode}
          />
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 2) MULTI-SKU CATALOG: typographic row-by-row directory with anchored hover image
  if (mode === "multi-sku-catalog") {
    return (
      <section
        key={section.id}
        id={section.id}
        className={tplStyles.sectionWrap}
        data-section-type="hero"
        data-section-variant={variant || "compact-bar"}
        data-surface={sectionSurfaceAttr(section, "bg")}
        data-padding="compact"
      >
        <div className={tplStyles.sectionInner}>
          <MultiSkuDirectory
            title={titleText}
            subtitle={subtitleText}
            ctaLabel={ctaLabel}
            ctaHref={ctaHref}
            products={products}
            fallbackImage={heroImg}
            isFirstHeading={isFirstHeading}
            previewMode={previewMode}
          />
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 3) DENSE GRID: zero hero image, straight into prominent search bar + category rail
  if (mode === "dense-grid" || mode === "compact-bar") {
    return (
      <section
        key={section.id}
        id={section.id}
        className={tplStyles.sectionWrap}
        data-section-type="hero"
        data-section-variant={variant || "compact-bar"}
        data-surface={sectionSurfaceAttr(section, "surface")}
        data-padding="compact"
      >
        <div className={tplStyles.sectionInner}>
          <DenseGridHeroSearch
            title={titleText}
            subtitle={subtitleText}
            brandName={brandName}
            shopId={shop.id}
            categories={categories}
            productCount={products.length}
            isFirstHeading={isFirstHeading}
            previewMode={previewMode}
          />
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 4) MINIMAL FASHION: centered 3:4 portrait with headline split left & right
  if (mode === "centered") {
    const portraitImg = primaryProductImg || heroImg;
    const [leftHalf, rightHalf] = splitHeadlineHalves(titleText);
    return (
      <section
        key={section.id}
        id={section.id}
        className={tplStyles.sectionWrap}
        data-section-type="hero"
        data-section-variant={variant || "centered"}
        data-surface={sectionSurfaceAttr(section, "bg")}
        data-padding="generous"
      >
        <div className={tplStyles.sectionInner}>
          <div className={tplStyles.heroMinimalStage}>
            <HeadingTag
              className={tplStyles.heroMinimalSplitTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              <span className={tplStyles.heroMinimalHalfLeft}>{leftHalf}</span>
              {rightHalf && <span className={tplStyles.heroMinimalHalfRight}> {rightHalf}</span>}
            </HeadingTag>

            <figure className={tplStyles.heroMinimalPortrait} data-reveal="clip">
              <img
                src={portraitImg}
                alt={titleText}
                width={896}
                height={1200}
                className={styles.heroMediaImg}
              />
            </figure>

            <div
              className={tplStyles.heroMinimalFoot}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              {subtitleText && <p className={tplStyles.heroMinimalSub}>{subtitleText}</p>}
              <SafeActionLink
                href={ctaHref}
                label={ctaLabel}
                className={styles.btnPrimary}
                previewMode={previewMode}
              />
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        </div>
      </section>
    );
  }

  // 5) EDITORIAL: colossal tight headline overlaid across two vertically staggered photos
  if (mode === "editorial") {
    const photoA = heroImg;
    const photoB =
      secondaryProductImg !== photoA ? secondaryProductImg : "/banners/fabric-closeup.jpg";
    return (
      <section
        key={section.id}
        id={section.id}
        className={tplStyles.sectionWrap}
        data-section-type="hero"
        data-section-variant={variant || "editorial"}
        data-surface={sectionSurfaceAttr(section, "bg")}
        data-padding="normal"
      >
        <div className={tplStyles.sectionInner}>
          <div className={tplStyles.heroEditorialCanvas}>
            <div className={tplStyles.heroEditorialTypeLayer}>
              {renderMaskedHeading(HeadingTag, titleText, tplStyles.heroEditorialGiantTitle, true)}
              <div
                className={tplStyles.heroEditorialMeta}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {subtitleText && <p className={tplStyles.heroEditorialSub}>{subtitleText}</p>}
                <SafeActionLink
                  href={ctaHref}
                  label={ctaLabel}
                  className={styles.btnPrimary}
                  previewMode={previewMode}
                />
              </div>
            </div>

            <figure className={tplStyles.heroEditorialPhotoA} data-reveal="clip">
              <img
                src={photoA}
                alt={titleText}
                width={1000}
                height={1333}
                className={styles.heroMediaImg}
              />
            </figure>

            <figure className={tplStyles.heroEditorialPhotoB} data-reveal="clip">
              <img
                src={photoB}
                alt={`Chi tiết chất liệu ${brandName}`}
                width={900}
                height={900}
                className={styles.heroMediaImg}
              />
            </figure>
          </div>
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 6) CRAFT ATELIER: full-bleed fabric photograph with wide headline anchored bottom-left
  if (mode === "atelier") {
    const craftHeroImg =
      sanitizeSafeImageUrl(props.image_url) ||
      sanitizeSafeImageUrl(tpl.theme.banner_url) ||
      primaryProductImg ||
      "/banners/fabric-closeup.jpg";
    return (
      <section
        key={section.id}
        id={section.id}
        className={`${tplStyles.sectionWrap} ${tplStyles.heroFullbleedBleed}`}
        data-section-type="hero"
        data-section-variant={variant || "atelier"}
        data-surface="bg"
      >
        <div className={tplStyles.heroAtelierFull}>
          <figure className={tplStyles.heroAtelierBackdrop} data-reveal="clip">
            <img
              src={craftHeroImg}
              alt={titleText}
              width={1600}
              height={1000}
              className={styles.heroMediaImg}
            />
          </figure>
          <div className={tplStyles.heroAtelierCornerPlate}>
            {renderMaskedHeading(HeadingTag, titleText, tplStyles.heroAtelierTitle, true)}
            {subtitleText && (
              <p
                className={tplStyles.heroAtelierSub}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {subtitleText}
              </p>
            )}
            <div
              className={styles.heroActions}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <SafeActionLink
                href={ctaHref}
                label={ctaLabel}
                className={styles.btnPrimary}
                previewMode={previewMode}
              />
            </div>
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        </div>
      </section>
    );
  }

  // 7) SPORT ACTIVE: dark ground, 16-20vw condensed poster headline + diagonal clip-path action photo
  if (mode === "sport-diagonal") {
    return (
      <section
        key={section.id}
        id={section.id}
        className={tplStyles.sectionWrap}
        data-section-type="hero"
        data-section-variant={variant || "sport-diagonal"}
        data-surface={sectionSurfaceAttr(section, "bg")}
        data-padding="normal"
      >
        <div className={tplStyles.sectionInner}>
          <div className={tplStyles.heroSportStage}>
            <HeadingTag
              className={tplStyles.heroSportPosterTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleText}
            </HeadingTag>

            <div className={tplStyles.heroSportBottom}>
              <figure className={tplStyles.heroSportSlashFrame} data-reveal="clip">
                <img
                  src={heroImg}
                  alt={titleText}
                  width={1365}
                  height={768}
                  className={styles.heroMediaImg}
                />
              </figure>

              <div
                className={tplStyles.heroSportCtaCol}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {subtitleText && <p className={tplStyles.heroSportSub}>{subtitleText}</p>}
                <SafeActionLink
                  href={ctaHref}
                  label={ctaLabel}
                  className={styles.btnPrimary}
                  previewMode={previewMode}
                />
                {renderInlineBlocks(blocks, previewMode)}
              </div>
            </div>
          </div>
        </div>
      </section>
    );
  }

  // 8) LUXURY MAISON: deliberately small framed photo centered in expansive quiet field, 1200ms slow open
  if (mode === "luxury-framed") {
    return (
      <section
        key={section.id}
        id={section.id}
        className={tplStyles.sectionWrap}
        data-section-type="hero"
        data-section-variant={variant || "luxury-framed"}
        data-surface={sectionSurfaceAttr(section, "bg")}
        data-padding="normal"
      >
        <div className={tplStyles.sectionInner}>
          <div className={tplStyles.heroLuxurySanctuary}>
            <HeadingTag
              className={tplStyles.heroLuxuryTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleText}
            </HeadingTag>

            <figure className={tplStyles.heroLuxurySmallFrame} data-reveal="scale">
              <img
                src={heroImg}
                alt={titleText}
                width={900}
                height={1125}
                className={styles.heroMediaImg}
              />
            </figure>

            <div
              className={tplStyles.heroLuxuryFoot}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              {subtitleText && <p className={tplStyles.heroLuxurySub}>{subtitleText}</p>}
              <SafeActionLink
                href={ctaHref}
                label={ctaLabel}
                className={tplStyles.heroLuxuryLink}
                previewMode={previewMode}
              />
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        </div>
      </section>
    );
  }

  // 9) CLASSIC / FULLBLEED: full-bleed photograph with clean bottom-anchored headline bar
  return (
    <section
      key={section.id}
      id={section.id}
      className={`${tplStyles.sectionWrap} ${tplStyles.heroFullbleedBleed}`}
      data-section-type="hero"
      data-section-variant={variant || "fullbleed"}
      data-surface="bg"
    >
      <div className={tplStyles.heroClassicFull}>
        <figure className={tplStyles.heroClassicBackdrop} data-reveal="clip">
          <img
            src={heroImg}
            alt={titleText}
            width={1600}
            height={1000}
            className={styles.heroMediaImg}
          />
        </figure>
        <div className={tplStyles.heroClassicBottomBar}>
          <div className={tplStyles.heroClassicBottomInner}>
            <div className={tplStyles.heroClassicCopy}>
              {renderMaskedHeading(HeadingTag, titleText, tplStyles.heroClassicTitle, true)}
              {subtitleText && <p className={tplStyles.heroClassicSub}>{subtitleText}</p>}
            </div>
            <SafeActionLink
              href={ctaHref}
              label={ctaLabel}
              className={styles.btnPrimary}
              previewMode={previewMode}
            />
          </div>
          {blocks && blocks.length > 0 && (
            <div className={tplStyles.sectionInner}>{renderInlineBlocks(blocks, previewMode)}</div>
          )}
        </div>
      </div>
    </section>
  );
}

function renderFeaturedProductsSection(
  section: Extract<SiteSectionV2, { type: "featured_products" }>,
  archetype: SiteArchetype,
  products: Product[],
  cardStyle: SiteCardStyle,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const maxItems =
    settings?.max_items && settings.max_items > 0 ? settings.max_items : (props.limit ?? 8);
  const items = selectFeaturedProducts(products, props.product_ids, maxItems);
  const gridClass = resolveProductGridClass(archetype, variant, settings?.columns, items.length);
  const effectiveCardStyle: SiteCardStyle = variant === "list-sku" ? "compact-row" : cardStyle;
  const isCentered = settings?.align === "center" || archetype === "luxury-maison";

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="featured_products"
      data-section-variant={variant || "grid-4"}
      data-surface={sectionSurfaceAttr(section, "bg")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={isCentered ? tplStyles.sectionHeadCentered : styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title || "Sản phẩm nổi bật", styles.sectionTitle)}
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && (
            <Link href="/san-pham" className={styles.sectionLink}>
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
          <div className={styles.emptyPanel}>
            <p className={styles.prosePara}>Cửa hàng đang cập nhật sản phẩm mới.</p>
          </div>
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
  const gridClass = resolveProductGridClass(
    tpl.layout.archetype,
    undefined,
    settings?.columns,
    items.length
  );

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="collection"
      data-section-variant={variant || "split"}
      data-surface={sectionSurfaceAttr(section, "surface")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        {variant === "compact" ? (
          <div className={styles.sectionHead}>
            <div className={styles.sectionHeadCopy}>
              {renderMaskedHeading("h2", props.title || "Bộ sưu tập", styles.sectionTitle)}
              {props.subtitle?.trim() && (
                <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                  {props.subtitle}
                </p>
              )}
            </div>
            <SafeActionLink
              href={viewHref}
              label="Xem tất cả"
              className={styles.sectionLink}
              previewMode={previewMode}
            />
          </div>
        ) : (
          <div className={styles.collectionSplit}>
            <div className={styles.collectionBanner} data-reveal="clip">
              <img
                src={bannerImg}
                alt={props.title || "Bộ sưu tập"}
                width={1365}
                height={768}
                className={styles.collectionBannerImg}
              />
            </div>
            <div className={styles.collectionIntro}>
              {renderMaskedHeading("h2", props.title || "Bộ sưu tập", styles.sectionTitle)}
              {props.subtitle?.trim() && <p className={styles.sectionSubtitle}>{props.subtitle}</p>}
              <div className={styles.heroActions}>
                <SafeActionLink
                  href={viewHref}
                  label="Khám phá"
                  className={styles.btnPrimary}
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
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const paragraphs = props.body
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean);
  const isCentered =
    variant === "centered" || props.align === "center" || settings?.align === "center";

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="text"
      data-section-variant={variant || "split"}
      data-surface={sectionSurfaceAttr(section, "bg")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        {variant === "monograph" ? (
          <div className={tplStyles.textMonograph}>
            {renderMaskedHeading("h2", props.title, styles.sectionTitle)}
            {paragraphs.map((para, idx) => (
              <p key={idx} className={styles.prosePara}>
                {para}
              </p>
            ))}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : variant === "pullquote" ? (
          <div className={tplStyles.textPullquote}>
            <h2 className={tplStyles.textPullquoteLead}>{props.title}</h2>
            <div className={styles.textSectionBodyCol}>
              {paragraphs.map((para, idx) => (
                <p key={idx} className={styles.prosePara}>
                  {para}
                </p>
              ))}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        ) : isCentered ? (
          <div className={styles.textSectionCenter}>
            {renderMaskedHeading("h2", props.title, styles.sectionTitle)}
            {paragraphs.map((para, idx) => (
              <p key={idx} className={styles.prosePara}>
                {para}
              </p>
            ))}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : (
          <div className={styles.textSectionGrid}>
            <div className={styles.textSectionTitleCol}>
              {renderMaskedHeading("h2", props.title, styles.sectionTitle)}
            </div>
            <div className={styles.textSectionBodyCol}>
              {paragraphs.map((para, idx) => (
                <p key={idx} className={styles.prosePara}>
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
  const { props, variant, blocks } = section;
  const imgSrc =
    sanitizeSafeImageUrl(props.image_url) ||
    sanitizeSafeImageUrl(tpl.theme.banner_url) ||
    "/banners/campaign-everyday.jpg";
  const surface = sectionSurfaceAttr(
    section,
    variant === "framed" ? "surface" : variant === "compact" ? "accent-soft" : "band"
  );

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="banner"
      data-section-variant={variant || "band"}
      data-surface={surface}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        {variant === "compact" ? (
          <div className={tplStyles.bannerCompact}>
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>{props.title}</h2>
              {props.subtitle?.trim() && (
                <p className={tplStyles.blockParagraph}>{props.subtitle}</p>
              )}
            </div>
            {props.cta_label?.trim() && (
              <SafeActionLink
                href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                label={props.cta_label}
                className={styles.btnPrimary}
                previewMode={previewMode}
              />
            )}
          </div>
        ) : variant === "framed" ? (
          <div className={tplStyles.bannerFramed}>
            <h2 className={styles.sectionTitle}>{props.title}</h2>
            {props.subtitle?.trim() && <p className={styles.prosePara}>{props.subtitle}</p>}
            {props.cta_label?.trim() && (
              <SafeActionLink
                href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                label={props.cta_label}
                className={styles.btnPrimary}
                previewMode={previewMode}
              />
            )}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : variant === "split-image" ? (
          <div className={tplStyles.bannerSplitImage}>
            <div className={styles.bannerMedia} data-reveal="clip">
              <img
                src={imgSrc}
                alt={props.title}
                width={1365}
                height={768}
                className={styles.collectionBannerImg}
              />
            </div>
            <div className={styles.bannerCopy}>
              {renderMaskedHeading("h2", props.title, styles.sectionTitle)}
              {props.subtitle?.trim() && <p className={styles.bannerSubtitle}>{props.subtitle}</p>}
              {props.cta_label?.trim() && (
                <div className={styles.heroActions}>
                  <SafeActionLink
                    href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                    label={props.cta_label}
                    className={styles.btnOnBand}
                    previewMode={previewMode}
                  />
                </div>
              )}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        ) : (
          <div className={styles.bannerGrid}>
            <div className={styles.bannerCopy}>
              {renderMaskedHeading("h2", props.title, styles.sectionTitle)}
              {props.subtitle?.trim() && <p className={styles.bannerSubtitle}>{props.subtitle}</p>}
              {props.cta_label?.trim() && (
                <div className={styles.heroActions}>
                  <SafeActionLink
                    href={sanitizeSafeHref(props.cta_href, "/san-pham")}
                    label={props.cta_label}
                    className={styles.btnOnBand}
                    previewMode={previewMode}
                  />
                </div>
              )}
              {renderInlineBlocks(blocks, previewMode)}
            </div>
            <div className={styles.bannerMedia} data-reveal="clip">
              <img
                src={imgSrc}
                alt={props.title}
                width={1365}
                height={768}
                className={styles.collectionBannerImg}
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
  vouchers: Voucher[],
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const cap = Math.max(1, Math.min(6, props.limit && props.limit > 0 ? props.limit : 6));
  const shopOnlyVouchers = vouchers.filter((v) => v.scope === "shop").slice(0, cap);

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="voucher_strip"
      data-section-variant={variant || "grid"}
      data-surface={sectionSurfaceAttr(section, "surface")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title || "Ưu đãi cửa hàng", styles.sectionTitle)}
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && shopOnlyVouchers.length > 0 && (
            <Link href="/voucher" className={styles.sectionLink}>
              Tất cả mã giảm giá
            </Link>
          )}
        </div>

        {shopOnlyVouchers.length === 1 ? (
          /* Single voucher: full-row horizontal ticket band (zero blank half-screen!) */
          <article className={tplStyles.voucherSingleBand}>
            <div className={tplStyles.voucherSingleCodeCol}>
              <span className={tplStyles.voucherBigCode}>{shopOnlyVouchers[0].code}</span>
            </div>
            <div className={tplStyles.voucherSingleInfoCol}>
              <h3 className={styles.voucherHeadline}>
                {formatVoucherBenefit(shopOnlyVouchers[0])}
              </h3>
              <p className={styles.voucherDetail}>
                {shopOnlyVouchers[0].minOrderValue > 0
                  ? `Đơn từ ${formatVnd(shopOnlyVouchers[0].minOrderValue)}`
                  : "Áp dụng mọi đơn tại cửa hàng"}
                {`, còn ${formatInt(shopOnlyVouchers[0].remainingUses)} lượt`}
              </p>
            </div>
            <div className={tplStyles.voucherSingleActionCol}>
              {previewMode ? (
                <span className={styles.btnPrimary}>Lưu mã</span>
              ) : (
                <Link href="/voucher" className={styles.btnPrimary}>
                  Lưu mã
                </Link>
              )}
            </div>
          </article>
        ) : shopOnlyVouchers.length > 1 ? (
          variant === "compact-bar" ? (
            <div
              className={
                shopOnlyVouchers.length === 2
                  ? tplStyles.voucherCompactRow2
                  : tplStyles.voucherCompactRow
              }
              data-reveal="rows"
            >
              {shopOnlyVouchers.map((voucher) => (
                <article key={voucher.id || voucher.code} className={tplStyles.voucherCompactPill}>
                  <div>
                    <p className={styles.voucherCode}>{voucher.code}</p>
                    <p className={styles.voucherDetail}>{formatVoucherBenefit(voucher)}</p>
                  </div>
                  {previewMode ? (
                    <span className={styles.sectionLink}>Lưu</span>
                  ) : (
                    <Link href="/voucher" className={styles.sectionLink}>
                      Lưu
                    </Link>
                  )}
                </article>
              ))}
            </div>
          ) : (
            <div
              className={
                shopOnlyVouchers.length === 2 ? tplStyles.voucherGrid2 : styles.voucherGrid
              }
              data-reveal="rows"
            >
              {shopOnlyVouchers.map((voucher) => (
                <article key={voucher.id || voucher.code} className={styles.voucherCard}>
                  <p className={styles.voucherCode}>{voucher.code}</p>
                  <h3 className={styles.voucherHeadline}>{formatVoucherBenefit(voucher)}</h3>
                  <p className={styles.voucherDetail}>
                    {voucher.minOrderValue > 0
                      ? `Đơn từ ${formatVnd(voucher.minOrderValue)}`
                      : "Áp dụng toàn cửa hàng"}
                  </p>
                  <div className={styles.voucherFoot}>
                    <span className={`${styles.voucherQuota} num`}>
                      Còn {formatInt(voucher.remainingUses)} lượt
                    </span>
                    {previewMode ? (
                      <span className={styles.sectionLink}>Lưu mã</span>
                    ) : (
                      <Link href="/voucher" className={styles.sectionLink}>
                        Lưu mã
                      </Link>
                    )}
                  </div>
                </article>
              ))}
            </div>
          )
        ) : (
          <div className={styles.emptyPanel}>
            <p className={styles.prosePara}>Chưa có mã giảm giá đang mở.</p>
          </div>
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderPoliciesSection(
  section: Extract<SiteSectionV2, { type: "policies" }>,
  archetype: SiteArchetype,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const policyBlocks = (blocks ?? []).filter((b) => b.type === "policy");
  const extraBlocks = (blocks ?? []).filter((b) => b.type !== "policy");
  const items = [
    ...props.items,
    ...policyBlocks.map((b) => ({ heading: b.props.heading, detail: b.props.detail })),
  ];

  // Signature horizontal filmstrip strictly for craft-atelier default/timeline policies
  const useCraftFilmstrip = archetype === "craft-atelier" && (!variant || variant === "timeline");

  const containerClass =
    variant === "timeline"
      ? tplStyles.policyTimeline
      : variant === "compact-list"
        ? tplStyles.policyCompactList
        : variant === "cards"
          ? tplStyles.policyCardsGrid
          : items.length === 3
            ? tplStyles.policyCardsGrid
            : styles.policyGrid;

  const itemClass =
    variant === "timeline"
      ? tplStyles.policyTimelineStep
      : variant === "compact-list"
        ? tplStyles.policyCompactItem
        : variant === "cards"
          ? tplStyles.policyFramedCard
          : styles.policyItem;

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="policies"
      data-section-variant={variant || "grid"}
      data-surface={sectionSurfaceAttr(section, "bg")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title || "Chính sách", styles.sectionTitle)}
          </div>
        </div>

        {useCraftFilmstrip ? (
          <div
            className={tplStyles.craftFilmstrip}
            role="list"
            aria-label={props.title || "Công đoạn tại xưởng"}
            data-reveal="rows"
          >
            {items.map((item, idx) => (
              <article
                key={`${section.id}-${idx}`}
                className={tplStyles.craftFilmCard}
                role="listitem"
              >
                <div className={tplStyles.craftFilmMedia} data-reveal="clip">
                  <img
                    src={CRAFT_FILMSTRIP_IMAGES[idx % CRAFT_FILMSTRIP_IMAGES.length]}
                    alt={item.heading}
                    width={640}
                    height={480}
                    className={tplStyles.blockImg}
                    loading="lazy"
                  />
                </div>
                <div className={tplStyles.craftFilmBody}>
                  <h3 className={styles.policyHeading}>{item.heading}</h3>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <div className={containerClass} data-reveal="rows">
            {items.map((item, idx) => (
              <article key={`${section.id}-${idx}`} className={itemClass}>
                <h3 className={styles.policyHeading}>{item.heading}</h3>
                <p className={styles.policyDetail}>{item.detail}</p>
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
  products: Product[],
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const imageBlocks = (blocks ?? []).filter(
    (b): b is Extract<SiteSectionBlock, { type: "image" }> => b.type === "image"
  );
  const otherBlocks = (blocks ?? []).filter((b) => b.type !== "image");

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

  const gridClass =
    variant === "duo-split"
      ? tplStyles.lookbookDuo
      : variant === "strip-4"
        ? tplStyles.lookbookStrip4
        : tplStyles.lookbookAsym3;

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="lookbook"
      data-section-variant={variant || "asym-3"}
      data-surface={sectionSurfaceAttr(section, "surface")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title || "Lookbook", styles.sectionTitle)}
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {props.cta_label?.trim() && (
            <SafeActionLink
              href={sanitizeSafeHref(props.cta_href, "/san-pham")}
              label={props.cta_label}
              className={styles.sectionLink}
              previewMode={previewMode}
            />
          )}
        </div>

        <div className={gridClass}>
          {fallbackImages.map((blk) => renderSingleBlock(blk, previewMode))}
        </div>

        {renderInlineBlocks(otherBlocks, previewMode)}
      </div>
    </section>
  );
}

function renderMetricsStripSection(
  section: Extract<SiteSectionV2, { type: "metrics_strip" }>,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const statBlocks = (blocks ?? []).filter(
    (b): b is Extract<SiteSectionBlock, { type: "stat" }> => b.type === "stat"
  );
  const otherBlocks = (blocks ?? []).filter((b) => b.type !== "stat");

  const isCards3 = variant !== "telemetry" && (variant === "cards-3" || statBlocks.length === 3);

  const gridClass =
    variant === "telemetry"
      ? tplStyles.metricsTelemetry
      : isCards3
        ? tplStyles.metricsCards3
        : tplStyles.metricsBar4;

  const itemClass =
    variant === "telemetry"
      ? tplStyles.telemetryTile
      : isCards3
        ? tplStyles.metricsCard
        : tplStyles.metricsBarItem;

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="metrics_strip"
      data-section-variant={variant || "bar-4"}
      data-surface={sectionSurfaceAttr(section, "surface")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title, styles.sectionTitle)}
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
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
  products: Product[],
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
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
            colorHex: color?.hex ?? "#1F3038",
          };
        })
    )
    .slice(0, maxRows);

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="sku_matrix"
      data-section-variant={variant || "compact-table"}
      data-surface={sectionSurfaceAttr(section, "surface")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title || "Bảng SKU đặt nhanh", styles.sectionTitle)}
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && (
            <Link href="/san-pham" className={styles.sectionLink}>
              Toàn bộ bảng SKU
            </Link>
          )}
        </div>

        {rows.length === 0 ? (
          <div className={styles.emptyPanel}>
            <p className={styles.prosePara}>Chưa có biến thể SKU hiển thị.</p>
          </div>
        ) : variant === "grouped" ? (
          <div className={tplStyles.skuGroupedList} data-reveal="rows">
            {matchingProducts.slice(0, Math.min(8, maxRows)).map((product) => {
              const skus = product.skus.filter((s) => showOos || s.stock > 0);
              if (skus.length === 0) return null;
              return (
                <article key={product.id} className={tplStyles.skuGroupCard}>
                  <div className={tplStyles.skuGroupHead}>
                    <div>
                      <h3 className={styles.policyHeading}>{product.name}</h3>
                      <p className={`${tplStyles.cardSpec} num`}>{formatVnd(product.price)}</p>
                    </div>
                    {previewMode ? (
                      <span className={styles.sectionLink}>Đặt mua</span>
                    ) : (
                      <Link
                        href={`/san-pham/${encodeURIComponent(product.slug)}`}
                        className={styles.sectionLink}
                      >
                        Đặt mua
                      </Link>
                    )}
                  </div>
                  <div className={tplStyles.skuPillGrid}>
                    {skus.slice(0, 12).map((sku) => {
                      const colorName =
                        colorMapByProduct.get(product.id)?.get(sku.colorCode)?.name ??
                        sku.colorCode;
                      return (
                        <span key={sku.skuCode} className={`${tplStyles.skuMiniPill} num`}>
                          <strong>
                            {colorName} / {sku.size}
                          </strong>
                          <span>{sku.stock > 0 ? `Còn ${formatInt(sku.stock)}` : "Hết hàng"}</span>
                        </span>
                      );
                    })}
                  </div>
                </article>
              );
            })}
          </div>
        ) : (
          <div className={tplStyles.tableScrollWrap}>
            <table className={tplStyles.skuTable}>
              <thead>
                <tr>
                  <th>Sản phẩm</th>
                  <th>SKU</th>
                  <th>Màu</th>
                  <th>Size</th>
                  <th>Đơn giá</th>
                  <th>Tồn</th>
                  <th>Đặt nhanh</th>
                </tr>
              </thead>
              <tbody data-reveal="rows">
                {rows.map((row) => (
                  <tr key={row.key}>
                    <td>
                      <strong>{row.product.name}</strong>
                    </td>
                    <td className={`${tplStyles.skuCodeMono} num`}>{row.sku.skuCode}</td>
                    <td>
                      <span className={tplStyles.colorSwatchInline}>
                        <span
                          className={tplStyles.colorDot}
                          style={{ backgroundColor: row.colorHex }}
                          aria-hidden="true"
                        />
                        <span>{row.colorName}</span>
                      </span>
                    </td>
                    <td>{row.sku.size}</td>
                    <td className="num">{formatVnd(row.sku.price)}</td>
                    <td>
                      <span
                        className={`${tplStyles.stockBadge} ${row.sku.stock <= 0 ? tplStyles.stockBadgeOut : ""} num`}
                      >
                        {row.sku.stock > 0 ? `Còn ${formatInt(row.sku.stock)}` : "Hết hàng"}
                      </span>
                    </td>
                    <td>
                      {previewMode ? (
                        <span className={styles.sectionLink}>Chọn</span>
                      ) : (
                        <Link
                          href={`/san-pham/${encodeURIComponent(row.product.slug)}`}
                          className={styles.sectionLink}
                        >
                          Đặt mua
                        </Link>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderTestimonialsSection(
  section: Extract<SiteSectionV2, { type: "testimonials" }>,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const quoteBlocks = (blocks ?? []).filter(
    (b): b is Extract<SiteSectionBlock, { type: "quote" }> => b.type === "quote"
  );
  const otherBlocks = (blocks ?? []).filter((b) => b.type !== "quote");

  const gridClass =
    variant === "editorial-quotes" || quoteBlocks.length === 2
      ? tplStyles.testimonialsEditorial
      : tplStyles.testimonialsGrid3;

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="testimonials"
      data-section-variant={variant || "grid-3"}
      data-surface={sectionSurfaceAttr(section, "surface")}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>
        <div className={styles.sectionHead}>
          <div className={styles.sectionHeadCopy}>
            {renderMaskedHeading("h2", props.title || "Nhận xét khách hàng", styles.sectionTitle)}
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
        </div>

        {quoteBlocks.length > 0 ? (
          <div className={gridClass} data-reveal="rows">
            {quoteBlocks.map((q) => (
              <blockquote key={q.id} className={tplStyles.testimonialCard}>
                <p className={tplStyles.testimonialQuote}>“{q.props.body}”</p>
                <footer>
                  <p className={tplStyles.testimonialAuthor}>{q.props.author}</p>
                  {q.props.role?.trim() && (
                    <p className={tplStyles.testimonialRole}>{q.props.role}</p>
                  )}
                </footer>
              </blockquote>
            ))}
          </div>
        ) : (
          <div className={styles.emptyPanel}>
            <p className={styles.prosePara}>Chưa có nhận xét nào.</p>
          </div>
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
      <section className={styles.heroSection}>
        <div className={tplStyles.sectionInner}>
          <div
            className={styles.emptyPanel}
            data-rise=""
            style={{ "--i": 0 } as React.CSSProperties}
          >
            <h1 className={styles.heroTitle}>{brandName}</h1>
            {tpl.theme.tagline?.trim() && (
              <p className={styles.heroSubtitle}>{tpl.theme.tagline}</p>
            )}
            <div className={styles.heroActions}>
              <SafeActionLink
                href="/san-pham"
                label="Xem sản phẩm"
                className={styles.btnPrimary}
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
        <section className={styles.pageHeadBand}>
          <div className={tplStyles.sectionInner}>
            <h1
              className={styles.pageTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {tpl.theme.brand_name?.trim() || shop.name}
            </h1>
            {tpl.theme.tagline?.trim() && (
              <p
                className={styles.pageLead}
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
            return renderFeaturedProductsSection(
              section,
              archetype,
              products,
              cardStyle,
              previewMode
            );
          case "collection":
            return renderCollectionSection(section, tpl, products, cardStyle, previewMode);
          case "text":
            return renderTextSection(section, previewMode);
          case "banner":
            return renderBannerSection(section, tpl, previewMode);
          case "voucher_strip":
            return renderVoucherStripSection(section, vouchers, previewMode);
          case "policies":
            return renderPoliciesSection(section, archetype, previewMode);
          case "lookbook":
            return renderLookbookSection(section, products, previewMode);
          case "metrics_strip":
            return renderMetricsStripSection(section, previewMode);
          case "sku_matrix":
            return renderSkuMatrixSection(section, products, previewMode);
          case "testimonials":
            return renderTestimonialsSection(section, previewMode);
        }
      })}
    </>
  );
}
