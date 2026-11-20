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
import type { PolicyItem } from "@/lib/site/types";
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
    pageTitle: "Chính sách bán hàng",
    description: `Chính sách giao hàng, đổi trả và bảo hành của ${payload.config.theme.brand_name || payload.shop.name}.`,
    siteOrigin,
    pathname: "/chinh-sach",
  });
}

export default async function SubdomainSitePoliciesPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const { payload, shopProfile, products } = bundle;
  const effectiveHost = payload.site.primary_host || decodedHost;
  const { marketplaceOrigin } = await resolveOrigins(effectiveHost);
  const brandName = payload.config.theme.brand_name?.trim() || payload.shop.name;

  const configuredPolicies: PolicyItem[] = payload.config.sections
    .filter((s) => s.visible && s.type === "policies")
    .flatMap((s) => (s.type === "policies" ? s.props.items : []));

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
        "Đơn hàng hỗ trợ thanh toán khi nhận hàng (COD) hoặc chuyển khoản VietQR xác nhận tự động qua NexusMart.",
    },
    {
      heading: "Kho xuất hàng",
      detail:
        shopProfile?.warehouseAddress ||
        `Sản phẩm được đóng gói và xuất trực tiếp từ kho chính hãng của ${brandName}.`,
    },
  ];

  const items = configuredPolicies.length > 0 ? configuredPolicies : fallbackPolicies;
  const showcaseProducts = products.slice(0, 4);

  return (
    <SiteShell
      config={payload.config}
      shopName={payload.shop.name}
      shopId={payload.shop.id}
      siteHost={effectiveHost}
      activeNav="policies"
      marketplaceOrigin={marketplaceOrigin}
    >
      <section className={styles.pageHeadBand}>
        <h1 className={styles.pageTitle} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
          Chính sách của {brandName}
        </h1>
        <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          Quy định giao nhận, đổi size và bảo vệ đơn hàng áp dụng khi mua sắm trực tiếp tại cửa hàng{" "}
          {brandName}.
        </p>
      </section>

      <section className={styles.siteSection} style={{ paddingTop: 0 }}>
        <div className={styles.policyGrid}>
          {items.map((item, idx) => (
            <article key={idx} className={styles.policyItem} data-reveal="">
              <h2 className={styles.policyHeading}>{item.heading}</h2>
              <p className={styles.policyDetail}>{item.detail}</p>
            </article>
          ))}
        </div>
      </section>

      {showcaseProducts.length > 0 && (
        <section className={`${styles.siteSection} ${styles.siteSectionSurface}`}>
          <div className={styles.sectionHead} data-reveal="">
            <div className={styles.sectionHeadCopy}>
              <h2 className={styles.sectionTitle}>Sản phẩm tiêu biểu tại {brandName}</h2>
            </div>
            <Link href="/san-pham" className={styles.sectionLink}>
              Xem danh mục
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
