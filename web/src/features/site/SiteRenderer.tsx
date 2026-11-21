import React from "react";
import Link from "next/link";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeHref } from "@/lib/site/theme";
import type { SiteConfig, SitePublicShopDTO } from "@/lib/site/types";
import { SiteProductCard } from "./SiteProductCard";
import type {
  SectionSurface,
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

function SafeActionLink({
  href,
  label,
  className,
  previewMode,
}: {
  href: string;
  label: string;
  className: string;
  previewMode: boolean;
}) {
  const cleanHref = sanitizeSafeHref(href);
  if (!label.trim() || !cleanHref) return null;
  if (previewMode) {
    return <span className={className}>{label}</span>;
  }
  if (cleanHref.startsWith("/")) {
    return (
      <Link href={cleanHref} className={className}>
        {label}
      </Link>
    );
  }
  return (
    <a href={cleanHref} className={className} rel="noopener noreferrer">
      {label}
    </a>
  );
}

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

function resolveGridClass(
  variant: string | undefined,
  columns: number | undefined,
  count: number
): string {
  if (variant === "editorial-asym") return tplStyles.gridEditorialAsym;
  if (variant === "list-sku") return tplStyles.gridListSku;
  if (variant === "dense-5" || columns === 5) return tplStyles.gridCols5;
  if (variant === "grid-2" || columns === 2) return tplStyles.gridCols2;
  if (variant === "grid-3" || columns === 3) return tplStyles.gridCols3;
  if (variant === "grid-4" || columns === 4) return tplStyles.gridCols4;
  return count === 3 ? tplStyles.gridCols3 : tplStyles.gridCols4;
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
        <figure key={block.id} className={tplStyles.blockFigure} data-reveal="">
          <div className={tplStyles.blockImageFrame} data-aspect={block.props.aspect || "4:3"}>
            <img
              src={imgUrl}
              alt={block.props.alt || block.props.caption || "Ảnh minh họa cửa hàng"}
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
      return (
        <div key={block.id} className={tplStyles.metricsCard} data-reveal="">
          <span className={`${tplStyles.metricValue} num`}>{block.props.value}</span>
          <span className={tplStyles.metricLabel}>{block.props.label}</span>
          {block.props.note?.trim() && (
            <span className={tplStyles.metricNote}>{block.props.note}</span>
          )}
        </div>
      );
    case "policy":
      return (
        <article key={block.id} className={styles.policyItem} data-reveal="">
          <h3 className={styles.policyHeading}>{block.props.heading}</h3>
          <p className={styles.policyDetail}>{block.props.detail}</p>
        </article>
      );
    case "quote":
      return (
        <blockquote key={block.id} className={tplStyles.blockQuote} data-reveal="">
          <p className={tplStyles.blockQuoteBody}>“{block.props.body}”</p>
          <cite className={tplStyles.blockQuoteCite}>
            {block.props.author}
            {block.props.role?.trim() ? ` · ${block.props.role}` : ""}
          </cite>
        </blockquote>
      );
    case "feature":
      return (
        <div key={block.id} className={tplStyles.blockFeatureCard} data-reveal="">
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
  products: Product[],
  isFirstHeading: boolean,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const heroImg =
    sanitizeSafeImageUrl(props.image_url) ||
    sanitizeSafeImageUrl(tpl.theme.banner_url) ||
    sanitizeSafeImageUrl(products[0]?.onModelImage) ||
    sanitizeSafeImageUrl(products[0]?.image) ||
    "/banners/vietnam-workshop.jpg";
  const ctaLabel = props.cta_label?.trim() || "Xem sản phẩm";
  const ctaHref = sanitizeSafeHref(props.cta_href, "/san-pham");
  const titleText = props.title.trim() || tpl.theme.brand_name;
  const surface = sectionSurfaceAttr(
    section,
    variant === "fullbleed" ? "band" : variant === "compact-bar" ? "surface" : "bg"
  );

  const HeadingTag = isFirstHeading ? "h1" : "h2";
  const titleClass = variant === "fullbleed" ? tplStyles.heroFullbleedTitle : styles.heroTitle;
  const subtitleClass = variant === "fullbleed" ? tplStyles.heroFullbleedSub : styles.heroSubtitle;

  const actions = (
    <div className={styles.heroActions} data-rise="" style={{ "--i": 3 } as React.CSSProperties}>
      <SafeActionLink
        href={ctaHref}
        label={ctaLabel}
        className={surface === "band" ? styles.btnOnBand : styles.btnPrimary}
        previewMode={previewMode}
      />
      {ctaHref !== "/san-pham" && (
        <SafeActionLink
          href="/san-pham"
          label="Danh mục cửa hàng"
          className={styles.btnSecondary}
          previewMode={previewMode}
        />
      )}
    </div>
  );

  const copyContent = (
    <>
      {props.eyebrow?.trim() && (
        <p className={styles.heroSubNote} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
          {props.eyebrow}
        </p>
      )}
      <HeadingTag className={titleClass} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
        {titleText}
      </HeadingTag>
      {props.subtitle?.trim() && (
        <p className={subtitleClass} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
          {props.subtitle}
        </p>
      )}
      {actions}
      {renderInlineBlocks(blocks, previewMode)}
    </>
  );

  const imgEl = (
    <img src={heroImg} alt={titleText} width={1365} height={768} className={styles.heroMediaImg} />
  );

  let body: React.ReactNode;
  if (variant === "centered") {
    body = (
      <div className={tplStyles.heroCentered}>
        <div className={tplStyles.heroCenteredCopy}>{copyContent}</div>
        <div className={tplStyles.heroCenteredMedia}>{imgEl}</div>
      </div>
    );
  } else if (variant === "editorial") {
    body = (
      <div className={tplStyles.heroEditorial}>
        <div className={tplStyles.heroEditorialCopy}>{copyContent}</div>
        <div className={tplStyles.heroEditorialMedia}>{imgEl}</div>
      </div>
    );
  } else if (variant === "compact-bar") {
    body = (
      <div className={tplStyles.heroCompactBar}>
        <div className={styles.heroCopyCol}>{copyContent}</div>
        <div className={tplStyles.heroCompactMedia}>{imgEl}</div>
      </div>
    );
  } else if (variant === "atelier") {
    body = (
      <div className={tplStyles.heroAtelier}>
        <div className={tplStyles.heroAtelierCopy}>{copyContent}</div>
        <div className={styles.heroVisualCol}>
          <div className={styles.heroMediaFrame}>{imgEl}</div>
        </div>
      </div>
    );
  } else if (variant === "sport-diagonal") {
    body = (
      <div className={tplStyles.heroSport}>
        <div className={tplStyles.heroSportCopy}>{copyContent}</div>
        <div className={tplStyles.heroSportFrame}>
          <div className={tplStyles.heroSportMedia}>{imgEl}</div>
        </div>
      </div>
    );
  } else if (variant === "luxury-framed") {
    body = (
      <div className={tplStyles.heroLuxury}>
        <div className={tplStyles.heroLuxuryCopy}>{copyContent}</div>
        <div className={tplStyles.heroLuxuryOuterFrame}>
          <div className={tplStyles.heroLuxuryInnerFrame}>{imgEl}</div>
        </div>
      </div>
    );
  } else if (variant === "fullbleed") {
    body = (
      <div className={tplStyles.heroFullbleed}>
        <div className={styles.heroCopyCol}>{copyContent}</div>
        <div className={styles.heroMediaFrame}>{imgEl}</div>
      </div>
    );
  } else {
    body = (
      <div className={styles.heroGrid}>
        <div className={styles.heroCopyCol}>{copyContent}</div>
        <div className={styles.heroVisualCol}>
          <div className={styles.heroMediaFrame}>{imgEl}</div>
        </div>
      </div>
    );
  }

  return (
    <section
      key={section.id}
      id={section.id}
      className={tplStyles.sectionWrap}
      data-section-type="hero"
      data-section-variant={variant || "split"}
      data-surface={surface}
      data-padding={sectionPaddingAttr(section)}
    >
      <div className={tplStyles.sectionInner}>{body}</div>
    </section>
  );
}

function renderFeaturedProductsSection(
  section: Extract<SiteSectionV2, { type: "featured_products" }>,
  products: Product[],
  cardStyle: SiteCardStyle,
  previewMode: boolean
) {
  const { props, variant, settings, blocks } = section;
  const maxItems =
    settings?.max_items && settings.max_items > 0 ? settings.max_items : (props.limit ?? 8);
  const items = selectFeaturedProducts(products, props.product_ids, maxItems);
  const gridClass = resolveGridClass(variant, settings?.columns, items.length);
  const effectiveCardStyle: SiteCardStyle = variant === "list-sku" ? "compact-row" : cardStyle;
  const isCentered = settings?.align === "center";

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
        <div
          className={isCentered ? tplStyles.sectionHeadCentered : styles.sectionHead}
          data-reveal=""
        >
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title || "Sản phẩm nổi bật"}</h2>
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && (
            <Link href="/san-pham" className={styles.sectionLink}>
              Xem tất cả sản phẩm
            </Link>
          )}
        </div>

        {items.length > 0 ? (
          <div className={gridClass}>
            {items.map((product, idx) => (
              <SiteProductCard
                key={product.id}
                product={product}
                previewMode={previewMode}
                index={idx}
                cardStyle={effectiveCardStyle}
                showExcerpt={variant === "editorial-asym" && idx === 0}
              />
            ))}
          </div>
        ) : (
          <div className={styles.emptyPanel} data-reveal="">
            <p className={styles.prosePara}>
              Cửa hàng đang cập nhật thêm sản phẩm mới trong danh mục này.
            </p>
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
  const gridClass = resolveGridClass(undefined, settings?.columns, items.length);

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
          <div className={styles.sectionHead} data-reveal="">
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>{props.title || "Bộ sưu tập tuyển chọn"}</h2>
              {props.subtitle?.trim() && (
                <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                  {props.subtitle}
                </p>
              )}
            </div>
            <SafeActionLink
              href={viewHref}
              label="Xem bộ sưu tập"
              className={styles.sectionLink}
              previewMode={previewMode}
            />
          </div>
        ) : (
          <div className={styles.collectionSplit}>
            <div className={styles.collectionBanner} data-reveal="">
              <img
                src={bannerImg}
                alt={props.title || "Bộ sưu tập"}
                width={1365}
                height={768}
                className={styles.collectionBannerImg}
              />
            </div>
            <div className={styles.collectionIntro} data-reveal="">
              <h2 className={styles.sectionTitle}>{props.title || "Bộ sưu tập tuyển chọn"}</h2>
              {props.subtitle?.trim() && <p className={styles.sectionSubtitle}>{props.subtitle}</p>}
              <div className={styles.heroActions}>
                <SafeActionLink
                  href={viewHref}
                  label="Khám phá bộ sưu tập"
                  className={styles.btnPrimary}
                  previewMode={previewMode}
                />
              </div>
            </div>
          </div>
        )}

        {items.length > 0 && (
          <div className={gridClass}>
            {items.map((product, idx) => (
              <SiteProductCard
                key={product.id}
                product={product}
                previewMode={previewMode}
                index={idx}
                cardStyle={cardStyle}
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
          <div className={tplStyles.textMonograph} data-reveal="">
            <h2 className={styles.sectionTitle}>{props.title}</h2>
            {paragraphs.map((para, idx) => (
              <p key={idx} className={styles.prosePara}>
                {para}
              </p>
            ))}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : variant === "pullquote" ? (
          <div className={tplStyles.textPullquote} data-reveal="">
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
          <div className={styles.textSectionCenter} data-reveal="">
            <h2 className={styles.sectionTitle}>{props.title}</h2>
            {paragraphs.map((para, idx) => (
              <p key={idx} className={styles.prosePara}>
                {para}
              </p>
            ))}
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        ) : (
          <div className={styles.textSectionGrid}>
            <div className={styles.textSectionTitleCol} data-reveal="">
              <h2 className={styles.sectionTitle}>{props.title}</h2>
            </div>
            <div className={styles.textSectionBodyCol} data-reveal="">
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
    variant === "framed" ? "bg" : variant === "compact" ? "accent-soft" : "band"
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
          <div className={tplStyles.bannerCompact} data-reveal="">
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
          <div className={tplStyles.bannerFramed} data-reveal="">
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
            <div className={styles.bannerMedia} data-reveal="">
              <img
                src={imgSrc}
                alt={props.title}
                width={1365}
                height={768}
                className={styles.collectionBannerImg}
              />
            </div>
            <div className={styles.bannerCopy} data-reveal="">
              <h2 className={styles.sectionTitle}>{props.title}</h2>
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
            <div className={styles.bannerCopy} data-reveal="">
              <h2 className={styles.sectionTitle}>{props.title}</h2>
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
            <div className={styles.bannerMedia} data-reveal="">
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
        <div className={styles.sectionHead} data-reveal="">
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title || "Ưu đãi riêng của cửa hàng"}</h2>
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && shopOnlyVouchers.length > 0 && (
            <Link href="/voucher" className={styles.sectionLink}>
              Xem tất cả mã giảm giá
            </Link>
          )}
        </div>

        {shopOnlyVouchers.length > 0 ? (
          variant === "compact-bar" ? (
            <div className={tplStyles.voucherCompactRow}>
              {shopOnlyVouchers.map((voucher) => (
                <article
                  key={voucher.id || voucher.code}
                  className={tplStyles.voucherCompactPill}
                  data-reveal=""
                >
                  <div>
                    <p className={styles.voucherCode}>{voucher.code}</p>
                    <p className={styles.voucherDetail}>{formatVoucherBenefit(voucher)}</p>
                  </div>
                  {previewMode ? (
                    <span className={styles.sectionLink}>Lưu mã</span>
                  ) : (
                    <Link href="/voucher" className={styles.sectionLink}>
                      Lưu mã
                    </Link>
                  )}
                </article>
              ))}
            </div>
          ) : (
            <div className={styles.voucherGrid}>
              {shopOnlyVouchers.map((voucher) => (
                <article
                  key={voucher.id || voucher.code}
                  className={styles.voucherCard}
                  data-reveal=""
                >
                  <p className={styles.voucherCode}>{voucher.code}</p>
                  <h3 className={styles.voucherHeadline}>{formatVoucherBenefit(voucher)}</h3>
                  <p className={styles.voucherDetail}>
                    {voucher.minOrderValue > 0
                      ? `Áp dụng cho đơn từ ${formatVnd(voucher.minOrderValue)} tại cửa hàng.`
                      : "Áp dụng trực tiếp cho sản phẩm của cửa hàng."}
                  </p>
                  <div className={styles.voucherFoot}>
                    <span className={`${styles.voucherQuota} num`}>
                      Còn {formatInt(voucher.remainingUses)} lượt
                    </span>
                    {previewMode ? (
                      <span className={styles.sectionLink}>Lưu và dùng mã</span>
                    ) : (
                      <Link href="/voucher" className={styles.sectionLink}>
                        Lưu và dùng mã
                      </Link>
                    )}
                  </div>
                </article>
              ))}
            </div>
          )
        ) : (
          <div className={styles.emptyPanel} data-reveal="">
            <p className={styles.prosePara}>
              Cửa hàng hiện chưa mở mã giảm giá mới. Mức giá niêm yết luôn cập nhật trực tiếp từ
              kho.
            </p>
          </div>
        )}

        {renderInlineBlocks(blocks, previewMode)}
      </div>
    </section>
  );
}

function renderPoliciesSection(
  section: Extract<SiteSectionV2, { type: "policies" }>,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const policyBlocks = (blocks ?? []).filter((b) => b.type === "policy");
  const extraBlocks = (blocks ?? []).filter((b) => b.type !== "policy");
  const items = [
    ...props.items,
    ...policyBlocks.map((b) => ({ heading: b.props.heading, detail: b.props.detail })),
  ];

  const containerClass =
    variant === "timeline"
      ? tplStyles.policyTimeline
      : variant === "compact-list"
        ? tplStyles.policyCompactList
        : variant === "cards"
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
        <div className={styles.sectionHead} data-reveal="">
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title || "Chính sách bán hàng"}</h2>
          </div>
        </div>

        <div className={containerClass}>
          {items.map((item, idx) => (
            <article key={`${section.id}-${idx}`} className={itemClass} data-reveal="">
              <h3 className={styles.policyHeading}>{item.heading}</h3>
              <p className={styles.policyDetail}>{item.detail}</p>
            </article>
          ))}
        </div>

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
              caption: p.name,
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
        <div className={styles.sectionHead} data-reveal="">
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title || "Khung hình Lookbook"}</h2>
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

  const gridClass =
    variant === "telemetry"
      ? tplStyles.metricsTelemetry
      : variant === "cards-3"
        ? tplStyles.metricsCards3
        : tplStyles.metricsBar4;

  const itemClass =
    variant === "telemetry"
      ? tplStyles.telemetryTile
      : variant === "cards-3"
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
        <div className={styles.sectionHead} data-reveal="">
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title}</h2>
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
        </div>

        {statBlocks.length > 0 && (
          <div className={gridClass}>
            {statBlocks.map((b) => (
              <div key={b.id} className={itemClass} data-reveal="">
                <span className={`${tplStyles.metricValue} num`}>{b.props.value}</span>
                <span className={tplStyles.metricLabel}>{b.props.label}</span>
                {b.props.note?.trim() && (
                  <span className={tplStyles.metricNote}>{b.props.note}</span>
                )}
              </div>
            ))}
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
        <div className={styles.sectionHead} data-reveal="">
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title || "Bảng tồn kho theo SKU"}</h2>
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
          {!previewMode && (
            <Link href="/san-pham" className={styles.sectionLink}>
              Mở toàn bộ bảng SKU
            </Link>
          )}
        </div>

        {rows.length === 0 ? (
          <div className={styles.emptyPanel}>
            <p className={styles.prosePara}>Chưa có dữ liệu biến thể SKU sẵn hiển thị.</p>
          </div>
        ) : variant === "grouped" ? (
          <div className={tplStyles.skuGroupedList}>
            {matchingProducts.slice(0, Math.min(8, maxRows)).map((product) => {
              const skus = product.skus.filter((s) => showOos || s.stock > 0);
              if (skus.length === 0) return null;
              return (
                <article key={product.id} className={tplStyles.skuGroupCard} data-reveal="">
                  <div className={tplStyles.skuGroupHead}>
                    <div>
                      <h3 className={styles.policyHeading}>{product.name}</h3>
                      <p className={tplStyles.cardSpec}>
                        {product.fabric} · Giá từ {formatVnd(product.price)}
                      </p>
                    </div>
                    {previewMode ? (
                      <span className={styles.sectionLink}>Xem chi tiết</span>
                    ) : (
                      <Link
                        href={`/san-pham/${encodeURIComponent(product.slug)}`}
                        className={styles.sectionLink}
                      >
                        Chọn mua
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
          <div className={tplStyles.tableScrollWrap} data-reveal="">
            <table className={tplStyles.skuTable}>
              <thead>
                <tr>
                  <th>Sản phẩm</th>
                  <th>Mã SKU</th>
                  <th>Màu sắc</th>
                  <th>Kích cỡ</th>
                  <th>Đơn giá</th>
                  <th>Tồn kho</th>
                  <th>Đặt mua</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.key}>
                    <td>
                      <strong>{row.product.name}</strong>
                    </td>
                    <td className={tplStyles.skuCodeMono}>{row.sku.skuCode}</td>
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
                        {row.sku.stock > 0 ? `Còn ${formatInt(row.sku.stock)}` : "Tạm hết"}
                      </span>
                    </td>
                    <td>
                      {previewMode ? (
                        <span className={styles.sectionLink}>Chi tiết</span>
                      ) : (
                        <Link
                          href={`/san-pham/${encodeURIComponent(row.product.slug)}`}
                          className={styles.sectionLink}
                        >
                          Xem SP
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
    variant === "editorial-quotes" ? tplStyles.testimonialsEditorial : tplStyles.testimonialsGrid3;

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
        <div className={styles.sectionHead} data-reveal="">
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.sectionTitle}>{props.title || "Nhận xét từ khách hàng"}</h2>
            {props.subtitle?.trim() && (
              <p className={`${styles.sectionSubtitle} ${tplStyles.mutedOnSurface}`}>
                {props.subtitle}
              </p>
            )}
          </div>
        </div>

        {quoteBlocks.length > 0 ? (
          <div className={gridClass}>
            {quoteBlocks.map((q) => (
              <blockquote key={q.id} className={tplStyles.testimonialCard} data-reveal="">
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
            <p className={styles.prosePara}>Chưa có nhận xét nào được thêm vào mục đánh giá này.</p>
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
            <p className={styles.heroSubtitle}>
              {tpl.theme.tagline?.trim() ||
                shop.description ||
                "Cửa hàng đang hoàn thiện nội dung trang chủ. Bạn vẫn có thể xem toàn bộ sản phẩm hiện có."}
            </p>
            <div className={styles.heroActions}>
              <SafeActionLink
                href="/san-pham"
                label="Xem sản phẩm của cửa hàng"
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
              products,
              !sectionsOverride && idx === firstHeroIndex,
              previewMode
            );
          case "featured_products":
            return renderFeaturedProductsSection(section, products, cardStyle, previewMode);
          case "collection":
            return renderCollectionSection(section, tpl, products, cardStyle, previewMode);
          case "text":
            return renderTextSection(section, previewMode);
          case "banner":
            return renderBannerSection(section, tpl, previewMode);
          case "voucher_strip":
            return renderVoucherStripSection(section, vouchers, previewMode);
          case "policies":
            return renderPoliciesSection(section, previewMode);
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
