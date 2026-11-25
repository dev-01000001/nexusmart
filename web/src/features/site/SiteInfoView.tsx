import React from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { Voucher } from "@/data/vouchers";
import type { PolicyItem, SitePublicShopDTO } from "@/lib/site/types";
import { CollapsibleProse } from "./CollapsibleProse";
import { SiteProductCard } from "./SiteProductCard";
import { SiteRenderer } from "./SiteRenderer";
import type { InfoPageConfig, SiteCardStyle, SiteTemplateConfig } from "./templateTypes";
import { sanitizeSafeImageUrl } from "./templateValidator";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

export interface SiteInfoViewProps {
  mode: "about" | "policies";
  templateConfig: SiteTemplateConfig;
  shop: SitePublicShopDTO;
  shopProfile: Shop | null;
  products: Product[];
  vouchers?: Voucher[];
  previewMode?: boolean;
}

export function SiteInfoView({
  mode,
  templateConfig,
  shop,
  shopProfile,
  products,
  vouchers = [],
  previewMode = false,
}: SiteInfoViewProps) {
  const brandName = templateConfig.theme.brand_name?.trim() || shop.name;
  const infoConfig: InfoPageConfig = templateConfig.pages.info;
  const layout = infoConfig.layout;
  const cardStyle: SiteCardStyle = templateConfig.layout.card_style;

  const textSections = templateConfig.sections.filter(
    (s): s is Extract<(typeof templateConfig.sections)[number], { type: "text" }> =>
      s.visible !== false && s.type === "text"
  );

  const configuredPolicies: PolicyItem[] = templateConfig.sections
    .filter(
      (s): s is Extract<(typeof templateConfig.sections)[number], { type: "policies" }> =>
        s.visible !== false && s.type === "policies"
    )
    .flatMap((s) => s.props.items);

  const fallbackPolicies: PolicyItem[] = [
    {
      heading: "Đổi size và đổi trả",
      detail:
        shopProfile?.returnPolicy ||
        "Đổi size hoặc đổi trả trong 45 ngày với sản phẩm còn nguyên tem mác.",
    },
    {
      heading: "Giao hàng và đồng kiểm",
      detail:
        shopProfile?.fastDeliveryNote ||
        "Giao hàng toàn quốc qua đối tác vận chuyển NexusMart, đồng kiểm khi nhận.",
    },
    {
      heading: "Thanh toán",
      detail: "Hỗ trợ thanh toán khi nhận hàng (COD) hoặc chuyển khoản VietQR.",
    },
    {
      heading: "Kho xuất hàng",
      detail: shopProfile?.warehouseAddress || `Xuất trực tiếp từ kho chính hãng ${brandName}.`,
    },
  ];

  const policyItems = configuredPolicies.length > 0 ? configuredPolicies : fallbackPolicies;
  const bannerImg =
    sanitizeSafeImageUrl(templateConfig.theme.banner_url) ||
    sanitizeSafeImageUrl(products[0]?.onModelImage) ||
    "/banners/vietnam-workshop.jpg";
  const showcaseProducts = products.slice(0, 4);

  const leadCopy =
    mode === "about"
      ? templateConfig.theme.tagline?.trim() ||
        shop.description ||
        shopProfile?.story ||
        `${brandName} cung cấp sản phẩm trực tiếp từ xưởng.`
      : `Giao nhận, đổi size và bảo vệ đơn hàng tại ${brandName}.`;

  const titleCopy = mode === "about" ? `Về ${brandName}` : `Chính sách ${brandName}`;

  const factoryEntries = shopProfile
    ? [
        shopProfile.city
          ? {
              heading: "Kho xuất hàng",
              detail: shopProfile.warehouseAddress || shopProfile.city,
            }
          : null,
        shopProfile.specialty ? { heading: "Thế mạnh", detail: shopProfile.specialty } : null,
        shopProfile.factoryProfile?.fiberOrigin
          ? { heading: "Nguồn sợi", detail: shopProfile.factoryProfile.fiberOrigin }
          : null,
        shopProfile.returnPolicy ? { heading: "Hậu mãi", detail: shopProfile.returnPolicy } : null,
      ].filter((x): x is { heading: string; detail: string } => x !== null)
    : [];

  const tocEntries = [
    { id: "info-overview", label: mode === "about" ? "Tổng quan" : "Quy định chung" },
    ...(mode === "about" && textSections.length > 0
      ? [{ id: "info-story", label: "Câu chuyện" }]
      : []),
    ...(mode === "policies" ? [{ id: "info-policies", label: "Chi tiết" }] : []),
    ...(infoConfig.show_factory_profile && factoryEntries.length > 0
      ? [{ id: "info-factory", label: "Hồ sơ xưởng" }]
      : []),
    ...(infoConfig.show_showcase && showcaseProducts.length > 0
      ? [{ id: "info-showcase", label: "Tiêu biểu" }]
      : []),
  ];

  const showcaseGridClass =
    showcaseProducts.length <= 1
      ? tplStyles.gridSingle
      : showcaseProducts.length === 2
        ? tplStyles.gridCols2
        : showcaseProducts.length === 3
          ? tplStyles.gridCols3
          : tplStyles.gridCols4;

  const renderHeaderHero = () => {
    if (layout === "athletic-manifesto") {
      return (
        <section id="info-overview" className={styles.siteSection} style={{ paddingBottom: 16 }}>
          <div className={tplStyles.container}>
            <div className={tplStyles.infoManifestoHero}>
              <h1
                className={tplStyles.infoBigStatement}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className={tplStyles.heroFullbleedSub}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
              <div className={styles.heroActions}>
                {previewMode ? (
                  <span className={styles.btnOnBand}>Xem sản phẩm</span>
                ) : (
                  <Link href="/san-pham" className={styles.btnOnBand}>
                    Xem sản phẩm
                  </Link>
                )}
              </div>
            </div>
          </div>
        </section>
      );
    }

    if (layout === "luxury-maison") {
      return (
        <section id="info-overview" className={styles.siteSection}>
          <div className={tplStyles.container}>
            <div className={tplStyles.infoMaisonHero}>
              <h1
                className={tplStyles.heroLuxuryTitle}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className={tplStyles.heroLuxurySub}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
              {mode === "about" && (
                <figure className={tplStyles.heroLuxurySmallFrame} data-reveal="scale">
                  <img
                    src={bannerImg}
                    alt={`Không gian ${brandName}`}
                    width={1365}
                    height={768}
                    className={styles.heroMediaImg}
                  />
                </figure>
              )}
            </div>
          </div>
        </section>
      );
    }

    if (mode === "policies" || layout === "compact-faq" || layout === "b2b-catalog-sheet") {
      return (
        <section id="info-overview" className={styles.pageHeadBand}>
          <div className={tplStyles.container}>
            <h1
              className={styles.pageTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleCopy}
            </h1>
            <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              {leadCopy}
            </p>
          </div>
        </section>
      );
    }

    // Large statement + single clean photograph
    return (
      <section id="info-overview" className={styles.siteSection}>
        <div className={tplStyles.container}>
          <div className={tplStyles.infoStatementHero}>
            <div className={tplStyles.infoStatementCopy}>
              <h1
                className={tplStyles.infoBigStatement}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className={styles.pageLead}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
              <div
                className={styles.heroActions}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {previewMode ? (
                  <span className={styles.btnPrimary}>Xem sản phẩm</span>
                ) : (
                  <>
                    <Link href="/san-pham" className={styles.btnPrimary}>
                      Xem sản phẩm
                    </Link>
                    <Link href="/chinh-sach" className={styles.btnSecondary}>
                      Chính sách
                    </Link>
                  </>
                )}
              </div>
            </div>

            <figure className={tplStyles.infoStatementMedia} data-reveal="clip">
              <img
                src={bannerImg}
                alt={`Không gian ${brandName}`}
                width={1365}
                height={768}
                className={styles.heroMediaImg}
              />
            </figure>
          </div>
        </div>
      </section>
    );
  };

  const renderBodyBlocks = () => (
    <div className={tplStyles.infoContentStack}>
      {mode === "policies" && (
        <div id="info-policies" className={tplStyles.infoAccordionList} data-reveal="rows">
          {policyItems.map((item, idx) => (
            <details key={idx} className={tplStyles.infoAccordion} open={idx === 0}>
              <summary className={tplStyles.infoAccordionSummary}>
                <span>{item.heading}</span>
                <span className={tplStyles.accordionPlus} aria-hidden="true" />
              </summary>
              <div className={tplStyles.infoAccordionBody}>
                <CollapsibleProse paragraphs={[item.detail]} previewChars={220} />
              </div>
            </details>
          ))}
        </div>
      )}

      {mode === "about" && textSections.length > 0 && (
        <div id="info-story" className={tplStyles.infoContentStack} data-reveal="rows">
          {textSections.map((sec) => {
            const paragraphs = sec.props.body
              .split(/\n{2,}/)
              .map((p) => p.trim())
              .filter(Boolean);
            return (
              <article key={sec.id} className={tplStyles.infoEssayCard}>
                <h2 className={styles.sectionTitle}>{sec.props.title}</h2>
                <CollapsibleProse paragraphs={paragraphs} previewChars={200} />
              </article>
            );
          })}
        </div>
      )}

      {infoConfig.show_factory_profile && factoryEntries.length > 0 && (
        <div id="info-factory">
          <div className={styles.sectionHead}>
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>Hồ sơ xưởng</h2>
            </div>
          </div>

          <div
            className={
              layout === "b2b-catalog-sheet" ? tplStyles.infoB2bSpecGrid : styles.policyGrid
            }
            data-reveal="rows"
          >
            {factoryEntries.map((item, idx) => (
              <article
                key={idx}
                className={
                  layout === "b2b-catalog-sheet" ? tplStyles.infoB2bSpecCell : styles.policyItem
                }
              >
                <h3 className={styles.policyHeading}>{item.heading}</h3>
                <CollapsibleProse paragraphs={[item.detail]} previewChars={160} />
              </article>
            ))}
          </div>
        </div>
      )}
    </div>
  );

  return (
    <div data-info-layout={layout}>
      {renderHeaderHero()}

      <section className={styles.siteSection} style={{ paddingTop: 12 }}>
        <div className={tplStyles.container}>
          {infoConfig.show_toc && tocEntries.length > 1 ? (
            <div className={tplStyles.infoTocShell}>
              <nav className={tplStyles.infoTocNav} aria-label="Mục lục">
                <span className={tplStyles.catalogSidebarTitle}>Mục lục</span>
                {tocEntries.map((entry) =>
                  previewMode ? (
                    <span key={entry.id} className={tplStyles.infoTocLink}>
                      {entry.label}
                    </span>
                  ) : (
                    <a key={entry.id} href={`#${entry.id}`} className={tplStyles.infoTocLink}>
                      {entry.label}
                    </a>
                  )
                )}
              </nav>
              {renderBodyBlocks()}
            </div>
          ) : (
            renderBodyBlocks()
          )}
        </div>
      </section>

      {infoConfig.sections && infoConfig.sections.length > 0 && (
        <SiteRenderer
          config={templateConfig}
          shop={shop}
          products={products}
          vouchers={vouchers}
          previewMode={previewMode}
          sectionsOverride={infoConfig.sections}
        />
      )}

      {infoConfig.show_showcase && showcaseProducts.length > 0 && (
        <section
          id="info-showcase"
          className={`${styles.siteSection} ${styles.siteSectionSurface}`}
        >
          <div className={tplStyles.container}>
            <div className={styles.sectionHead}>
              <div className={styles.sectionHeadCopy}>
                <h2 className={styles.sectionTitle}>Sản phẩm tiêu biểu</h2>
              </div>
              {previewMode ? (
                <span className={styles.sectionLink}>Xem tất cả</span>
              ) : (
                <Link href="/san-pham" className={styles.sectionLink}>
                  Xem tất cả
                </Link>
              )}
            </div>
            <div className={showcaseGridClass} data-reveal="rows">
              {showcaseProducts.map((product, idx) => (
                <SiteProductCard
                  key={product.id}
                  product={product}
                  index={idx}
                  previewMode={previewMode}
                  cardStyle={cardStyle}
                  layoutMode={showcaseProducts.length === 1 ? "single-wide" : "default"}
                />
              ))}
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
