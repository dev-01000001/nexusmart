import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { headers } from "next/headers";
import { notFound } from "next/navigation";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { fetchPublishedSiteByHost, loadPublicSitePageBundle } from "@/lib/server/sites";
import { deriveMarketplaceOrigin, deriveSiteOrigin } from "@/lib/site/host";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { SiteProductCard, SiteShell } from "@/features/site";
import styles from "@/features/site/site.module.css";

interface PageProps {
  params: Promise<{ host: string }>;
}

async function resolveOrigins(siteHost: string) {
  const h = await headers();
  const rawHost = resolveRequestHost(h, siteHost);
  const proto = resolveRequestProto(h, "http://localhost");
  const marketplaceOrigin = deriveMarketplaceOrigin({
    rawHost,
    proto,
    rootDomain: getSiteRootDomain(),
    appOrigin: APP_ORIGINS[0],
  });
  const siteOrigin = deriveSiteOrigin({ siteHost, rawHost, proto });
  return { marketplaceOrigin, siteOrigin };
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const payload = await fetchPublishedSiteByHost(decodedHost);
  if (!payload) {
    return {
      title: "Không tìm thấy cửa hàng",
      robots: { index: false, follow: false },
    };
  }
  const { siteOrigin } = await resolveOrigins(payload.site.primary_host || decodedHost);
  return buildSiteMetadata({
    config: payload.config,
    pageTitle: "Giới thiệu",
    description:
      payload.config.theme.tagline ||
      payload.shop.description ||
      `Câu chuyện thương hiệu và quy chuẩn sản phẩm của ${payload.config.theme.brand_name || payload.shop.name}.`,
    siteOrigin,
    pathname: "/gioi-thieu",
  });
}

export default async function SubdomainSiteAboutPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const { payload, shopProfile, products } = bundle;
  const effectiveHost = payload.site.primary_host || decodedHost;
  const { marketplaceOrigin } = await resolveOrigins(effectiveHost);
  const brandName = payload.config.theme.brand_name?.trim() || payload.shop.name;

  const textSections = payload.config.sections.filter((s) => s.visible && s.type === "text");
  const bannerImg =
    sanitizeSafeUrl(payload.config.theme.banner_url) ||
    sanitizeSafeUrl(products[0]?.onModelImage) ||
    "/banners/vietnam-workshop.jpg";
  const showcaseProducts = products.slice(0, 4);

  return (
    <SiteShell
      config={payload.config}
      shopName={payload.shop.name}
      shopId={payload.shop.id}
      siteHost={effectiveHost}
      activeNav="about"
      marketplaceOrigin={marketplaceOrigin}
    >
      <section className={styles.heroSection}>
        <div className={styles.heroGrid}>
          <div className={styles.heroCopyCol}>
            <h1
              className={styles.heroTitle}
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              Về {brandName}
            </h1>
            <p
              className={styles.heroSubtitle}
              data-rise=""
              style={{ "--i": 1 } as React.CSSProperties}
            >
              {payload.config.theme.tagline?.trim() ||
                payload.shop.description ||
                shopProfile?.story ||
                `${brandName} cung cấp sản phẩm chính hãng trực tiếp từ xưởng, minh bạch chất liệu và tồn kho trên NexusMart.`}
            </p>
            <div
              className={styles.heroActions}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <Link href="/san-pham" className={styles.btnPrimary}>
                Xem sản phẩm của {brandName}
              </Link>
              <Link href="/chinh-sach" className={styles.btnSecondary}>
                Xem chính sách
              </Link>
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
      </section>

      {textSections.length > 0 &&
        textSections.map((sec) => {
          if (sec.type !== "text") return null;
          const paragraphs = sec.props.body
            .split(/\n{2,}/)
            .map((p) => p.trim())
            .filter(Boolean);
          return (
            <section key={sec.id} className={`${styles.siteSection} ${styles.siteSectionSurface}`}>
              <div className={styles.textSectionGrid}>
                <div className={styles.textSectionTitleCol} data-reveal="">
                  <h2 className={styles.sectionTitle}>{sec.props.title}</h2>
                </div>
                <div className={styles.textSectionBodyCol} data-reveal="">
                  {paragraphs.map((para, idx) => (
                    <p key={idx} className={styles.prosePara}>
                      {para}
                    </p>
                  ))}
                </div>
              </div>
            </section>
          );
        })}

      {shopProfile && (
        <section className={styles.siteSection}>
          <div className={styles.sectionHead} data-reveal="">
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>Hồ sơ xưởng sản xuất</h2>
            </div>
          </div>

          <div className={styles.policyGrid}>
            {shopProfile.city && (
              <article className={styles.policyItem} data-reveal="">
                <h3 className={styles.policyHeading}>Khu vực xuất hàng</h3>
                <p className={styles.policyDetail}>
                  {shopProfile.warehouseAddress || shopProfile.city}
                </p>
              </article>
            )}
            {shopProfile.specialty && (
              <article className={styles.policyItem} data-reveal="">
                <h3 className={styles.policyHeading}>Thế mạnh sản phẩm</h3>
                <p className={styles.policyDetail}>{shopProfile.specialty}</p>
              </article>
            )}
            {shopProfile.factoryProfile?.fiberOrigin && (
              <article className={styles.policyItem} data-reveal="">
                <h3 className={styles.policyHeading}>Nguồn nguyên liệu</h3>
                <p className={styles.policyDetail}>{shopProfile.factoryProfile.fiberOrigin}</p>
              </article>
            )}
            {shopProfile.returnPolicy && (
              <article className={styles.policyItem} data-reveal="">
                <h3 className={styles.policyHeading}>Cam kết hậu mãi</h3>
                <p className={styles.policyDetail}>{shopProfile.returnPolicy}</p>
              </article>
            )}
          </div>
        </section>
      )}

      {showcaseProducts.length > 0 && (
        <section className={`${styles.siteSection} ${styles.siteSectionSurface}`}>
          <div className={styles.sectionHead} data-reveal="">
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>Sản phẩm nổi bật của {brandName}</h2>
            </div>
            <Link href="/san-pham" className={styles.sectionLink}>
              Xem tất cả
            </Link>
          </div>
          <div className={styles.productGrid}>
            {showcaseProducts.map((product, idx) => (
              <SiteProductCard key={product.id} product={product} index={idx} />
            ))}
          </div>
        </section>
      )}
    </SiteShell>
  );
}
