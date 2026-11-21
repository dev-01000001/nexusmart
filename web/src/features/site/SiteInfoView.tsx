import React from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { Voucher } from "@/data/vouchers";
import type { PolicyItem, SitePublicShopDTO } from "@/lib/site/types";
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
        "Hỗ trợ đổi size hoặc đổi trả trong vòng 45 ngày đối với sản phẩm còn nguyên tem mác.",
    },
    {
      heading: "Giao hàng và đồng kiểm",
      detail:
        shopProfile?.fastDeliveryNote ||
        "Giao hàng toàn quốc qua đối tác vận chuyển của NexusMart, cho phép kiểm tra sản phẩm khi nhận.",
    },
    {
      heading: "Thanh toán an toàn",
      detail:
        "Đơn hàng hỗ trợ thanh toán khi nhận hàng (COD) hoặc chuyển khoản VietQR xác nhận tự động.",
    },
    {
      heading: "Kho xuất hàng",
      detail:
        shopProfile?.warehouseAddress ||
        `Sản phẩm được đóng gói và xuất trực tiếp từ kho chính hãng của ${brandName}.`,
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
        `${brandName} cung cấp sản phẩm chính hãng trực tiếp từ xưởng, minh bạch chất liệu và tồn kho.`
      : `Quy định giao nhận, đổi size và bảo vệ đơn hàng áp dụng khi mua sắm trực tiếp tại cửa hàng ${brandName}.`;

  const titleCopy = mode === "about" ? `Về ${brandName}` : `Chính sách của ${brandName}`;

  const factoryEntries = shopProfile
    ? [
        shopProfile.city
          ? {
              heading: "Khu vực xuất hàng",
              detail: shopProfile.warehouseAddress || shopProfile.city,
            }
          : null,
        shopProfile.specialty
          ? { heading: "Thế mạnh sản phẩm", detail: shopProfile.specialty }
          : null,
        shopProfile.factoryProfile?.fiberOrigin
          ? { heading: "Nguồn nguyên liệu", detail: shopProfile.factoryProfile.fiberOrigin }
          : null,
        shopProfile.returnPolicy
          ? { heading: "Cam kết hậu mãi", detail: shopProfile.returnPolicy }
          : null,
      ].filter((x): x is { heading: string; detail: string } => x !== null)
    : [];

  const tocEntries = [
    { id: "info-overview", label: mode === "about" ? "Tổng quan thương hiệu" : "Quy định chung" },
    ...(mode === "about" && textSections.length > 0
      ? [{ id: "info-story", label: "Câu chuyện chất liệu" }]
      : []),
    ...(mode === "policies" ? [{ id: "info-policies", label: "Chính sách chi tiết" }] : []),
    ...(infoConfig.show_factory_profile && factoryEntries.length > 0
      ? [{ id: "info-factory", label: "Hồ sơ xưởng sản xuất" }]
      : []),
    ...(infoConfig.show_showcase && showcaseProducts.length > 0
      ? [{ id: "info-showcase", label: "Sản phẩm tiêu biểu" }]
      : []),
  ];

  const renderHeaderHero = () => {
    if (layout === "athletic-manifesto") {
      return (
        <section id="info-overview" className={styles.siteSection} style={{ paddingBottom: 16 }}>
          <div className={tplStyles.container}>
            <div className={tplStyles.infoManifestoHero}>
              <h1 className={tplStyles.heroFullbleedTitle}>{titleCopy}</h1>
              <p className={tplStyles.heroFullbleedSub}>{leadCopy}</p>
              <div className={styles.heroActions}>
                {previewMode ? (
                  <span className={styles.btnOnBand}>Xem sản phẩm</span>
                ) : (
                  <Link href="/san-pham" className={styles.btnOnBand}>
                    Xem sản phẩm của {brandName}
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
              <h1 className={styles.heroTitle}>{titleCopy}</h1>
              <p className={styles.heroSubtitle}>{leadCopy}</p>
              {mode === "about" && (
                <div className={tplStyles.heroLuxuryOuterFrame}>
                  <div className={tplStyles.heroLuxuryInnerFrame}>
                    <img
                      src={bannerImg}
                      alt={`Không gian thương hiệu ${brandName}`}
                      width={1365}
                      height={768}
                      className={styles.heroMediaImg}
                    />
                  </div>
                </div>
              )}
            </div>
          </div>
        </section>
      );
    }

    if (layout === "compact-faq" || layout === "b2b-catalog-sheet" || mode === "policies") {
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

    // split-hero, editorial-essay, craft-timeline
    return (
      <section id="info-overview" className={styles.heroSection}>
        <div className={tplStyles.container}>
          <div className={styles.heroGrid}>
            <div className={styles.heroCopyCol}>
              <h1
                className={styles.heroTitle}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className={styles.heroSubtitle}
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
                  <>
                    <span className={styles.btnPrimary}>Xem sản phẩm của {brandName}</span>
                    <span className={styles.btnSecondary}>
                      {mode === "about" ? "Xem chính sách" : "Về cửa hàng"}
                    </span>
                  </>
                ) : (
                  <>
                    <Link href="/san-pham" className={styles.btnPrimary}>
                      Xem sản phẩm của {brandName}
                    </Link>
                    <Link
                      href={mode === "about" ? "/chinh-sach" : "/gioi-thieu"}
                      className={styles.btnSecondary}
                    >
                      {mode === "about" ? "Xem chính sách" : "Về cửa hàng"}
                    </Link>
                  </>
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
                  src={bannerImg}
                  alt={`Không gian thương hiệu ${brandName}`}
                  width={1365}
                  height={768}
                  className={styles.heroMediaImg}
                />
              </div>
            </div>
          </div>
        </div>
      </section>
    );
  };

  const renderBodyBlocks = () => (
    <div className={tplStyles.infoContentStack}>
      {mode === "policies" && (
        <div id="info-policies">
          {layout === "compact-faq" ? (
            <div className={tplStyles.infoFaqList}>
              {policyItems.map((item, idx) => (
                <article key={idx} className={tplStyles.infoFaqItem} data-reveal="">
                  <h2 className={styles.policyHeading}>{item.heading}</h2>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          ) : layout === "craft-timeline" ? (
            <div className={tplStyles.policyTimeline}>
              {policyItems.map((item, idx) => (
                <article key={idx} className={tplStyles.policyTimelineStep} data-reveal="">
                  <h2 className={styles.policyHeading}>{item.heading}</h2>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          ) : layout === "b2b-catalog-sheet" ? (
            <div className={tplStyles.infoB2bSpecGrid}>
              {policyItems.map((item, idx) => (
                <article key={idx} className={tplStyles.infoB2bSpecCell} data-reveal="">
                  <h2 className={styles.policyHeading}>{item.heading}</h2>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          ) : (
            <div className={styles.policyGrid}>
              {policyItems.map((item, idx) => (
                <article key={idx} className={styles.policyItem} data-reveal="">
                  <h2 className={styles.policyHeading}>{item.heading}</h2>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          )}
        </div>
      )}

      {mode === "about" && textSections.length > 0 && (
        <div id="info-story" className={tplStyles.infoContentStack}>
          {textSections.map((sec) => {
            const paragraphs = sec.props.body
              .split(/\n{2,}/)
              .map((p) => p.trim())
              .filter(Boolean);
            return (
              <article key={sec.id} className={styles.emptyPanel} data-reveal="">
                <h2 className={styles.sectionTitle}>{sec.props.title}</h2>
                {paragraphs.map((para, idx) => (
                  <p key={idx} className={styles.prosePara}>
                    {para}
                  </p>
                ))}
              </article>
            );
          })}
        </div>
      )}

      {infoConfig.show_factory_profile && factoryEntries.length > 0 && (
        <div id="info-factory">
          <div className={styles.sectionHead} data-reveal="">
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>Hồ sơ xưởng sản xuất</h2>
            </div>
          </div>

          {layout === "craft-timeline" ? (
            <div className={tplStyles.policyTimeline}>
              {factoryEntries.map((item, idx) => (
                <article key={idx} className={tplStyles.policyTimelineStep} data-reveal="">
                  <h3 className={styles.policyHeading}>{item.heading}</h3>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          ) : layout === "b2b-catalog-sheet" ? (
            <div className={tplStyles.infoB2bSpecGrid}>
              {factoryEntries.map((item, idx) => (
                <article key={idx} className={tplStyles.infoB2bSpecCell} data-reveal="">
                  <h3 className={styles.policyHeading}>{item.heading}</h3>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          ) : (
            <div className={styles.policyGrid}>
              {factoryEntries.map((item, idx) => (
                <article key={idx} className={styles.policyItem} data-reveal="">
                  <h3 className={styles.policyHeading}>{item.heading}</h3>
                  <p className={styles.policyDetail}>{item.detail}</p>
                </article>
              ))}
            </div>
          )}
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
              <nav className={tplStyles.infoTocNav} aria-label="Mục lục trang thông tin">
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
            <div className={styles.sectionHead} data-reveal="">
              <div className={styles.sectionHeadCopy}>
                <h2 className={styles.sectionTitle}>Sản phẩm tiêu biểu của {brandName}</h2>
              </div>
              {previewMode ? (
                <span className={styles.sectionLink}>Xem tất cả</span>
              ) : (
                <Link href="/san-pham" className={styles.sectionLink}>
                  Xem tất cả
                </Link>
              )}
            </div>
            <div className={tplStyles.gridCols4}>
              {showcaseProducts.map((product, idx) => (
                <SiteProductCard
                  key={product.id}
                  product={product}
                  index={idx}
                  previewMode={previewMode}
                  cardStyle={cardStyle}
                />
              ))}
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
