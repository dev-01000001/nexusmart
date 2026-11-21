import React from "react";
import Link from "next/link";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeHref, sanitizeSafeUrl } from "@/lib/site/theme";
import type {
  BannerSection,
  CollectionSection,
  FeaturedProductsSection,
  HeroSection,
  PoliciesSection,
  SiteConfig,
  SitePublicShopDTO,
  SiteSection,
  TextSection,
  VoucherStripSection,
} from "@/lib/site/types";
import { SiteProductCard } from "./SiteProductCard";
import styles from "./site.module.css";

export interface SiteRendererProps {
  config: SiteConfig;
  shop: SitePublicShopDTO;
  products: Product[];
  vouchers: Voucher[];
  categories?: CategoryInfo[];
  previewMode?: boolean;
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
  const cap = Math.max(1, Math.min(12, limit > 0 ? limit : 8));
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
  const cap = Math.max(1, Math.min(12, limit > 0 ? limit : 4));
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

function renderHeroSection(
  section: HeroSection,
  config: SiteConfig,
  products: Product[],
  isFirstHeading: boolean,
  previewMode: boolean
) {
  const { props } = section;
  const heroImg =
    sanitizeSafeUrl(props.image_url) ||
    sanitizeSafeUrl(config.theme.banner_url) ||
    sanitizeSafeUrl(products[0]?.onModelImage) ||
    sanitizeSafeUrl(products[0]?.image) ||
    "/banners/vietnam-workshop.jpg";
  const ctaLabel = props.cta_label?.trim() || "Xem sản phẩm";
  const ctaHref = sanitizeSafeHref(props.cta_href, "/san-pham");
  const titleText = props.title.trim() || config.theme.brand_name;

  return (
    <section key={section.id} className={styles.heroSection} data-section-type="hero">
      <div className={styles.heroGrid}>
        <div className={styles.heroCopyCol}>
          {isFirstHeading ? (
            <h1
              className={styles.heroTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleText}
            </h1>
          ) : (
            <h2
              className={styles.heroTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleText}
            </h2>
          )}

          {props.eyebrow?.trim() && (
            <p
              className={styles.heroSubNote}
              data-rise=""
              style={{ "--i": 1 } as React.CSSProperties}
            >
              {props.eyebrow}
            </p>
          )}

          {props.subtitle?.trim() && (
            <p
              className={styles.heroSubtitle}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              {props.subtitle}
            </p>
          )}

          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <SafeActionLink
              href={ctaHref}
              label={ctaLabel}
              className={styles.btnPrimary}
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
        </div>

        <div
          className={styles.heroVisualCol}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.heroMediaFrame}>
            <img
              src={heroImg}
              alt={titleText}
              width={1365}
              height={768}
              className={styles.heroMediaImg}
            />
          </div>
        </div>
      </div>
    </section>
  );
}

function renderFeaturedProductsSection(
  section: FeaturedProductsSection,
  products: Product[],
  previewMode: boolean
) {
  const { props } = section;
  const items = selectFeaturedProducts(products, props.product_ids, props.limit ?? 8);
  const gridClass =
    items.length === 3 ? `${styles.productGrid} ${styles.productGridThree}` : styles.productGrid;

  return (
    <section key={section.id} className={styles.siteSection} data-section-type="featured_products">
      <div className={styles.sectionHead} data-reveal="">
        <div className={styles.sectionHeadCopy}>
          <h2 className={styles.sectionTitle}>{props.title || "Sản phẩm nổi bật"}</h2>
          {props.subtitle?.trim() && <p className={styles.sectionSubtitle}>{props.subtitle}</p>}
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
    </section>
  );
}

function renderCollectionSection(
  section: CollectionSection,
  config: SiteConfig,
  products: Product[],
  previewMode: boolean
) {
  const { props } = section;
  const items = selectCollectionProducts(
    products,
    props.category_slug,
    props.collection_slug,
    props.limit ?? 4
  );
  const bannerImg =
    sanitizeSafeUrl(props.image_url) ||
    sanitizeSafeUrl(config.theme.banner_url) ||
    sanitizeSafeUrl(items[0]?.onModelImage) ||
    "/banners/fabric-closeup.jpg";
  const viewHref = props.category_slug
    ? `/san-pham?cat=${encodeURIComponent(props.category_slug)}`
    : "/san-pham";
  const gridClass =
    items.length === 3 ? `${styles.productGrid} ${styles.productGridThree}` : styles.productGrid;

  return (
    <section
      key={section.id}
      className={`${styles.siteSection} ${styles.siteSectionSurface}`}
      data-section-type="collection"
    >
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

      {items.length > 0 && (
        <div className={gridClass}>
          {items.map((product, idx) => (
            <SiteProductCard
              key={product.id}
              product={product}
              previewMode={previewMode}
              index={idx}
            />
          ))}
        </div>
      )}
    </section>
  );
}

function renderTextSection(section: TextSection) {
  const { props } = section;
  const paragraphs = props.body
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean);
  const isCentered = props.align === "center";

  return (
    <section key={section.id} className={styles.siteSection} data-section-type="text">
      {isCentered ? (
        <div className={styles.textSectionCenter} data-reveal="">
          <h2 className={styles.sectionTitle}>{props.title}</h2>
          {paragraphs.map((para, idx) => (
            <p key={idx} className={styles.prosePara}>
              {para}
            </p>
          ))}
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
          </div>
        </div>
      )}
    </section>
  );
}

function renderBannerSection(section: BannerSection, config: SiteConfig, previewMode: boolean) {
  const { props } = section;
  const imgSrc =
    sanitizeSafeUrl(props.image_url) ||
    sanitizeSafeUrl(config.theme.banner_url) ||
    "/banners/campaign-everyday.jpg";

  return (
    <section
      key={section.id}
      className={`${styles.siteSection} ${styles.siteSectionBand}`}
      data-section-type="banner"
    >
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
    </section>
  );
}

function renderVoucherStripSection(
  section: VoucherStripSection,
  vouchers: Voucher[],
  previewMode: boolean
) {
  const { props } = section;
  const cap = Math.max(1, Math.min(6, props.limit && props.limit > 0 ? props.limit : 6));
  // Defense in depth: strictly render SHOP-scoped vouchers only.
  const shopOnlyVouchers = vouchers.filter((v) => v.scope === "shop").slice(0, cap);

  return (
    <section
      key={section.id}
      className={`${styles.siteSection} ${styles.siteSectionSurface}`}
      data-section-type="voucher_strip"
    >
      <div className={styles.sectionHead} data-reveal="">
        <div className={styles.sectionHeadCopy}>
          <h2 className={styles.sectionTitle}>{props.title || "Ưu đãi riêng của cửa hàng"}</h2>
          {props.subtitle?.trim() && <p className={styles.sectionSubtitle}>{props.subtitle}</p>}
        </div>
        {!previewMode && shopOnlyVouchers.length > 0 && (
          <Link href="/voucher" className={styles.sectionLink}>
            Xem tất cả mã giảm giá
          </Link>
        )}
      </div>

      {shopOnlyVouchers.length > 0 ? (
        <div className={styles.voucherGrid}>
          {shopOnlyVouchers.map((voucher) => (
            <article key={voucher.id || voucher.code} className={styles.voucherCard} data-reveal="">
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
      ) : (
        <div className={styles.emptyPanel} data-reveal="">
          <p className={styles.prosePara}>
            Cửa hàng hiện chưa mở mã giảm giá mới. Mức giá niêm yết luôn cập nhật trực tiếp từ kho.
          </p>
        </div>
      )}
    </section>
  );
}

function renderPoliciesSection(section: PoliciesSection) {
  const { props } = section;
  return (
    <section key={section.id} className={styles.siteSection} data-section-type="policies">
      <div className={styles.sectionHead} data-reveal="">
        <div className={styles.sectionHeadCopy}>
          <h2 className={styles.sectionTitle}>{props.title || "Chính sách bán hàng"}</h2>
        </div>
      </div>

      <div className={styles.policyGrid}>
        {props.items.map((item, idx) => (
          <article key={`${section.id}-${idx}`} className={styles.policyItem} data-reveal="">
            <h3 className={styles.policyHeading}>{item.heading}</h3>
            <p className={styles.policyDetail}>{item.detail}</p>
          </article>
        ))}
      </div>
    </section>
  );
}

/**
 * Reusable pure React renderer for all 7 `SiteConfig` section types.
 * Shared identically between public subdomain sites and Seller draft preview (`/seller/site/xem-truoc`).
 * Zero `dangerouslySetInnerHTML`, zero raw CSS/HTML injection; every string renders as plain text.
 */
export function SiteRenderer({
  config,
  shop,
  products,
  vouchers,
  previewMode = false,
}: SiteRendererProps) {
  const visibleSections = (config.sections ?? []).filter((s) => s.visible !== false);
  const firstHeroIndex = visibleSections.findIndex((s) => s.type === "hero");

  if (visibleSections.length === 0) {
    const brandName = config.theme.brand_name?.trim() || shop.name;
    return (
      <section className={styles.heroSection}>
        <div className={styles.emptyPanel} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
          <h1 className={styles.heroTitle}>{brandName}</h1>
          <p className={styles.heroSubtitle}>
            {config.theme.tagline?.trim() ||
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
      </section>
    );
  }

  return (
    <>
      {firstHeroIndex === -1 && (
        <section className={styles.pageHeadBand}>
          <h1 className={styles.pageTitle} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
            {config.theme.brand_name?.trim() || shop.name}
          </h1>
          {config.theme.tagline?.trim() && (
            <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              {config.theme.tagline}
            </p>
          )}
        </section>
      )}

      {visibleSections.map((section: SiteSection, idx: number) => {
        switch (section.type) {
          case "hero":
            return renderHeroSection(
              section,
              config,
              products,
              idx === firstHeroIndex,
              previewMode
            );
          case "featured_products":
            return renderFeaturedProductsSection(section, products, previewMode);
          case "collection":
            return renderCollectionSection(section, config, products, previewMode);
          case "text":
            return renderTextSection(section);
          case "banner":
            return renderBannerSection(section, config, previewMode);
          case "voucher_strip":
            return renderVoucherStripSection(section, vouchers, previewMode);
          case "policies":
            return renderPoliciesSection(section);
        }
      })}
    </>
  );
}
