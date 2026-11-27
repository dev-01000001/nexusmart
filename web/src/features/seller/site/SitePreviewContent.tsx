// Universal preview renderer for all 5 storefront pages (home, catalog, pdp, cart, info)
// using the real SiteShell, SiteRenderer, SiteCatalogView, and SitePdpView components.
// Safe for both Server Components and Client Components (Gallery, Customize & Code tabs).

import React from "react";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatVnd } from "@/lib/format";
import { formatVariantLabel } from "@/lib/formatVariant";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { SiteCatalogView, SitePdpView, SiteRenderer, SiteShell } from "@/features/site";
import { sanitizeSafeImageUrl } from "@/features/site/templateValidator";
import type { SiteTemplateConfig, TemplatePageKey } from "@/features/site/templateTypes";
import { CART_LAYOUT_OPTIONS, INFO_LAYOUT_OPTIONS } from "./siteEditorUtils";

export interface SitePreviewContentProps {
  config: SiteTemplateConfig;
  page: TemplatePageKey;
  shop: SitePublicShopDTO;
  products: Product[];
  categories: CategoryInfo[];
  vouchers: Voucher[];
  platformOrigin: string;
  skuAttributesById?: Record<string, Record<string, string>>;
}

function labelForOption<T extends string>(
  list: ReadonlyArray<{ value: T; label: string }>,
  val: T
): string {
  return list.find((o) => o.value === val)?.label ?? val;
}

export function SitePreviewContent({
  config,
  page,
  shop,
  products,
  categories,
  vouchers,
  platformOrigin,
  skuAttributesById,
}: SitePreviewContentProps) {
  const brandName = config.theme.brand_name?.trim() || shop.name || "Cửa hàng của bạn";
  const primaryProduct = products[0];

  const renderCartPreview = () => {
    const cartCfg = config.pages.cart;
    const sampleItems = products.slice(0, 2);
    const subtotal = sampleItems.reduce((sum, p, idx) => sum + p.price * (idx === 0 ? 2 : 1), 0);
    const isTable = cartCfg.layout === "compact-table";
    const isSingle = cartCfg.layout === "express-bar";

    return (
      <div
        className="mx-auto flex max-w-[1160px] flex-col gap-5 px-5 pt-7 pb-11"
        data-cart-layout={cartCfg.layout}
      >
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h1 className="text-2xl leading-tight font-semibold text-foreground">
            Giỏ hàng tại {brandName}
          </h1>
          <span className="rounded-sm border border-border bg-card px-2.5 py-1 text-[13px] text-muted-foreground">
            Bố cục: {labelForOption(CART_LAYOUT_OPTIONS, cartCfg.layout)}
          </span>
        </div>

        <div
          className={
            isSingle
              ? "flex flex-col gap-4"
              : "grid grid-cols-1 items-start gap-5 md:grid-cols-[minmax(0,1.45fr)_minmax(260px,0.9fr)]"
          }
        >
          <div className="flex flex-col gap-2.5">
            {sampleItems.map((item, idx) => {
              const qty = idx === 0 ? 2 : 1;
              const imgUrl = sanitizeSafeImageUrl(item.image) || "/banners/campaign-everyday.jpg";
              return (
                <div
                  key={item.id}
                  className={cn(
                    "flex items-center gap-3 rounded-lg border border-border bg-card text-card-foreground",
                    isTable ? "px-2.5 py-2" : "p-3"
                  )}
                >
                  <img
                    src={imgUrl}
                    alt={item.name}
                    width={64}
                    height={64}
                    className="size-14 shrink-0 rounded-md object-cover"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold text-foreground">{item.name}</div>
                    <div className="text-[13px] text-muted-foreground">
                      {(() => {
                        const firstSku = item.skus[0];
                        const rawAttrs = firstSku
                          ? (firstSku.skuId && skuAttributesById?.[firstSku.skuId]) ||
                            skuAttributesById?.[firstSku.skuCode]
                          : undefined;
                        const variantText = formatVariantLabel({
                          attributes: rawAttrs,
                          fallback: "Mặc định",
                        });
                        return `${variantText} · SL: ${qty}`;
                      })()}
                    </div>
                  </div>
                  <div className="text-sm font-semibold whitespace-nowrap text-foreground tabular-nums">
                    {formatVnd(item.price * qty)}
                  </div>
                </div>
              );
            })}

            {cartCfg.show_addons && products[2] && (
              <div className="flex flex-col gap-1.5 rounded-lg border border-border bg-card p-3 text-card-foreground">
                <div className="text-[13px] font-semibold text-muted-foreground">
                  Gợi ý mua thêm cùng đơn
                </div>
                <div className="flex items-center justify-between gap-2 text-[13px]">
                  <span>{products[2].name}</span>
                  <strong className="tabular-nums">{formatVnd(products[2].price)}</strong>
                </div>
              </div>
            )}
          </div>

          <aside className="flex flex-col gap-2.5 rounded-lg border border-border bg-card p-4 text-card-foreground">
            <div className="text-[15px] font-semibold text-foreground">Tóm tắt đơn hàng</div>
            {cartCfg.show_vouchers && (
              <div className="flex items-center justify-between gap-2 rounded-md bg-muted px-2.5 py-2 text-[13px]">
                <span className="text-muted-foreground">Mã giảm giá gian hàng</span>
                <span className="font-semibold text-foreground tabular-nums">
                  {vouchers[0]?.code || "SHOP30K"}
                </span>
              </div>
            )}
            <div className="flex items-center justify-between gap-2 text-[13px]">
              <span className="text-muted-foreground">Tạm tính</span>
              <strong className="text-foreground tabular-nums">{formatVnd(subtotal)}</strong>
            </div>
            <div className="flex items-center justify-between gap-2 text-[13px]">
              <span className="text-muted-foreground">Vận chuyển</span>
              <span className="text-muted-foreground">Tính ở bước thanh toán</span>
            </div>
            <div className="rounded-md bg-primary px-3.5 py-2.5 text-center text-sm font-semibold text-primary-foreground">
              Tiến hành thanh toán
            </div>
            {cartCfg.trust_note && (
              <p className="text-[13px] leading-[18px] text-muted-foreground">
                {cartCfg.trust_note}
              </p>
            )}
          </aside>
        </div>

        {cartCfg.sections && cartCfg.sections.length > 0 && (
          <SiteRenderer
            config={config}
            shop={shop}
            products={products}
            vouchers={vouchers}
            categories={categories}
            previewMode={true}
            sectionsOverride={cartCfg.sections}
          />
        )}
      </div>
    );
  };

  const renderInfoPreview = () => {
    const infoCfg = config.pages.info;
    const bannerImg =
      sanitizeSafeImageUrl(config.theme.banner_url) || "/banners/vietnam-workshop.jpg";
    const textSections = config.sections.filter((s) => s.visible && s.type === "text");
    const showcase = products.slice(0, 4);

    return (
      <div
        className="mx-auto flex max-w-[1160px] flex-col gap-5 px-5 pt-7 pb-11"
        data-info-layout={infoCfg.layout}
      >
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h1 className="text-2xl leading-tight font-semibold text-foreground">Về {brandName}</h1>
          <span className="rounded-sm border border-border bg-card px-2.5 py-1 text-[13px] text-muted-foreground">
            Bố cục: {labelForOption(INFO_LAYOUT_OPTIONS, infoCfg.layout)}
          </span>
        </div>

        <div
          className={
            infoCfg.show_toc
              ? "grid grid-cols-1 items-start gap-5 md:grid-cols-[200px_minmax(0,1fr)]"
              : "flex flex-col gap-4"
          }
        >
          {infoCfg.show_toc && (
            <aside className="rounded-lg border border-border bg-card p-3.5 text-card-foreground">
              <div className="mb-2 text-[13px] font-semibold text-foreground">Mục lục</div>
              <ul className="flex flex-col gap-1.5 text-[13px] text-muted-foreground">
                <li>Câu chuyện thương hiệu</li>
                {infoCfg.show_factory_profile && <li>Thông tin cửa hàng</li>}
                <li>Chính sách đổi trả &amp; giao hàng</li>
                {infoCfg.show_showcase && <li>Sản phẩm tiêu biểu</li>}
              </ul>
            </aside>
          )}

          <div className="flex min-w-0 flex-col gap-4">
            <div className="grid grid-cols-1 items-center gap-4 rounded-lg border border-border bg-card p-4 text-card-foreground md:grid-cols-2">
              <div>
                <p className="text-[15px] leading-6 text-foreground">
                  {config.theme.tagline ||
                    shop.description ||
                    `${brandName} cung cấp sản phẩm chính hãng, minh bạch thông tin và tồn kho.`}
                </p>
              </div>
              <img
                src={bannerImg}
                alt={`Không gian thương hiệu ${brandName}`}
                width={640}
                height={360}
                className="aspect-video w-full rounded-md object-cover"
              />
            </div>

            {textSections.length > 0 && (
              <div className="flex flex-col gap-3">
                {textSections.map((sec) =>
                  sec.type === "text" ? (
                    <article
                      key={sec.id}
                      className="rounded-lg border border-border bg-card p-4 text-card-foreground"
                    >
                      <h3 className="mb-1.5 text-base font-semibold text-foreground">
                        {sec.props.title}
                      </h3>
                      <p className="text-sm leading-relaxed text-muted-foreground">
                        {sec.props.body}
                      </p>
                    </article>
                  ) : null
                )}
              </div>
            )}

            {infoCfg.show_factory_profile && (
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <div className="flex flex-col gap-1 rounded-lg border border-border bg-card p-3.5 text-[13px] text-card-foreground">
                  <strong className="font-semibold text-foreground">Nguồn gốc rõ ràng</strong>
                  <span className="text-muted-foreground">
                    Sản phẩm chọn lọc, minh bạch thông số và mô tả thực tế.
                  </span>
                </div>
                <div className="flex flex-col gap-1 rounded-lg border border-border bg-card p-3.5 text-[13px] text-card-foreground">
                  <strong className="font-semibold text-foreground">Kiểm soát chất lượng</strong>
                  <span className="text-muted-foreground">
                    Kiểm tra kỹ từng sản phẩm và đóng gói cẩn thận trước khi giao.
                  </span>
                </div>
                <div className="flex flex-col gap-1 rounded-lg border border-border bg-card p-3.5 text-[13px] text-card-foreground">
                  <strong className="font-semibold text-foreground">Hậu mãi minh bạch</strong>
                  <span className="text-muted-foreground">
                    Hỗ trợ đổi trả theo chính sách cửa hàng và đồng kiểm COD toàn quốc.
                  </span>
                </div>
              </div>
            )}

            {infoCfg.show_showcase && showcase.length > 0 && (
              <div className="space-y-2.5">
                <h3 className="text-base font-semibold text-foreground">
                  Sản phẩm tiêu biểu của {brandName}
                </h3>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  {showcase.map((p) => {
                    const img = sanitizeSafeImageUrl(p.image) || "/banners/campaign-everyday.jpg";
                    return (
                      <div
                        key={p.id}
                        className="flex flex-col gap-1.5 overflow-hidden rounded-lg border border-border bg-card p-2 text-card-foreground"
                      >
                        <img
                          src={img}
                          alt={p.name}
                          width={180}
                          height={220}
                          className="aspect-[4/5] w-full rounded-md object-cover"
                        />
                        <div className="line-clamp-2 text-[13px] font-medium text-foreground">
                          {p.shortName || p.name}
                        </div>
                        <div className="text-[13px] font-semibold text-foreground tabular-nums">
                          {formatVnd(p.price)}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        </div>

        {infoCfg.sections && infoCfg.sections.length > 0 && (
          <SiteRenderer
            config={config}
            shop={shop}
            products={products}
            vouchers={vouchers}
            categories={categories}
            previewMode={true}
            sectionsOverride={infoCfg.sections}
          />
        )}
      </div>
    );
  };

  return (
    <SiteShell
      config={config}
      shopName={shop.name}
      shopId={shop.id}
      platformOrigin={platformOrigin}
      previewMode={true}
    >
      {page === "home" && (
        <SiteRenderer
          config={config}
          shop={shop}
          products={products}
          vouchers={vouchers}
          categories={categories}
          previewMode={true}
        />
      )}

      {page === "catalog" && (
        <SiteCatalogView
          key={`${config.pages.catalog.layout}:${config.pages.catalog.show_sku_matrix}`}
          brandName={brandName}
          products={products}
          categories={categories}
          pageConfig={config.pages.catalog}
          templateConfig={config}
          shop={shop}
          vouchers={vouchers}
          previewMode={true}
        />
      )}

      {page === "pdp" &&
        (primaryProduct ? (
          <SitePdpView
            brandName={brandName}
            product={primaryProduct}
            skuAttributesById={skuAttributesById}
            shopVouchers={vouchers}
            relatedProducts={products.slice(1, 5)}
            pageConfig={config.pages.pdp}
            templateConfig={config}
            shop={shop}
            previewMode={true}
          />
        ) : (
          <div className="mx-auto max-w-[1160px] px-5 py-10 text-sm text-muted-foreground">
            <p>Gian hàng chưa có sản phẩm để hiển thị trang chi tiết.</p>
          </div>
        ))}

      {page === "cart" && renderCartPreview()}
      {page === "info" && renderInfoPreview()}
    </SiteShell>
  );
}
