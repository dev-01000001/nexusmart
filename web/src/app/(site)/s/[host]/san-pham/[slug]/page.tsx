import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import shared from "@/features/commerce/commerce.module.css";
import { loadPublicSiteProduct } from "@/lib/server/sites";
import { buildSiteProductMetadata } from "@/lib/site/seo";
import { SitePdpView } from "@/features/site";
import { loadSiteContext } from "@/features/site/siteContext.server";

interface PageProps {
  params: Promise<{ host: string; slug: string }>;
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host, slug } = await params;
  const decodedHost = decodeURIComponent(host);
  const decodedSlug = decodeURIComponent(slug);
  const [ctx, data] = await Promise.all([
    loadSiteContext(host),
    loadPublicSiteProduct(decodedHost, decodedSlug),
  ]);
  if (!ctx || !data) {
    return {
      title: "Không tìm thấy sản phẩm",
      robots: { index: false, follow: false },
    };
  }
  if (data.discontinued) {
    return {
      title: `${data.discontinuedName} — Ngừng kinh doanh`,
      description: `Sản phẩm ${data.discontinuedName} hiện đã ngừng kinh doanh tại ${ctx.brandName}.`,
      robots: { index: false, follow: true },
    };
  }

  return buildSiteProductMetadata({
    config: data.bundle.payload.config,
    productName: data.product.name,
    productSlug: data.product.slug,
    productDescription: data.product.description,
    siteOrigin: ctx.siteOrigin,
  });
}

export default async function SubdomainSiteProductDetailPage({ params }: PageProps) {
  const { host, slug } = await params;
  const decodedHost = decodeURIComponent(host);
  const decodedSlug = decodeURIComponent(slug);
  const [ctx, data] = await Promise.all([
    loadSiteContext(host),
    loadPublicSiteProduct(decodedHost, decodedSlug),
  ]);
  if (!ctx || !data) notFound();

  const brandName = ctx.templateConfig.theme.brand_name?.trim() || data.bundle.payload.shop.name;

  if (data.discontinued) {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>{data.discontinuedName}</h1>
            <p className={shared.pageLead}>{brandName}</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Sản phẩm này đã ngừng kinh doanh."
            message="Bạn có thể xem các sản phẩm đang mở bán khác của cửa hàng bên dưới."
            actionLabel="Xem tất cả sản phẩm"
            actionHref="/san-pham"
          />
        </section>
        {data.relatedProducts.length > 0 && (
          <ProductAddonStrip
            title={`Sản phẩm khác tại ${brandName}`}
            products={data.relatedProducts}
          />
        )}
      </div>
    );
  }

  const {
    bundle,
    product,
    relatedProducts,
    liveFlashItem,
    initialReviews,
    activeShopDiscountBySkuId,
    provinces,
  } = data;
  const { payload, vouchers } = bundle;

  return (
    <SitePdpView
      brandName={brandName}
      product={product}
      shopVouchers={vouchers}
      relatedProducts={relatedProducts}
      liveFlashItem={liveFlashItem}
      initialReviews={initialReviews}
      activeShopDiscountBySkuId={activeShopDiscountBySkuId}
      provinces={provinces}
      pageConfig={ctx.templateConfig.pages.pdp}
      templateConfig={ctx.templateConfig}
      shop={payload.shop}
    />
  );
}
