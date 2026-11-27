import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { PackageX } from "lucide-react";
import { EmptyState } from "@/components/app/EmptyState";
import { Button } from "@/components/ui/button";
import { loadPublicSiteProduct } from "@/lib/server/sites";
import { buildSiteProductMetadata } from "@/lib/site/seo";
import { SitePdpView } from "@/features/site/SitePdpView";
import { SiteProductCard } from "@/features/site/SiteProductCard";
import { loadProductSkuAttributes, loadSiteContext } from "@/features/site/siteContext.server";

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
    const cardStyle = ctx.templateConfig.layout.card_style ?? "minimal";
    return (
      <div className="mx-auto flex w-full max-w-[var(--site-max-w,1260px)] flex-col gap-8 px-[clamp(1rem,2.2vw,2rem)] pt-[clamp(24px,3.2vw,44px)] pb-[clamp(48px,6vw,80px)] text-foreground">
        <section className="flex flex-col gap-1.5" aria-labelledby="discontinued-product-heading">
          <h1
            id="discontinued-product-heading"
            className="text-[clamp(26px,3.6vw,40px)] leading-[1.12] font-semibold tracking-[-0.02em] text-foreground"
          >
            {data.discontinuedName}
          </h1>
          <p className="text-[15px] text-muted-foreground">{brandName}</p>
        </section>

        <section>
          <EmptyState
            icon={PackageX}
            title="Sản phẩm này đã ngừng kinh doanh."
            description="Bạn có thể xem các sản phẩm đang mở bán khác của cửa hàng bên dưới."
            className="rounded-lg border border-border shadow-none"
            action={
              <Button className="min-h-10 shadow-none" asChild>
                <Link href="/san-pham">Xem tất cả sản phẩm</Link>
              </Button>
            }
          />
        </section>

        {data.relatedProducts.length > 0 && (
          <section className="flex flex-col gap-5" aria-labelledby="discontinued-related-heading">
            <h2
              id="discontinued-related-heading"
              className="text-xl font-semibold tracking-tight text-foreground"
            >
              Sản phẩm khác tại {brandName}
            </h2>
            <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4 lg:gap-6">
              {data.relatedProducts.slice(0, 4).map((rel, idx) => (
                <SiteProductCard key={rel.id} product={rel} index={idx} cardStyle={cardStyle} />
              ))}
            </div>
          </section>
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
  const skuAttributesById = await loadProductSkuAttributes(product.backendId || product.id);

  return (
    <SitePdpView
      brandName={brandName}
      product={product}
      skuAttributesById={skuAttributesById}
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
