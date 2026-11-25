// Universal preview renderer for all 5 storefront pages (home, catalog, pdp, cart, info)
// using the real SiteShell, SiteRenderer, SiteCatalogView, and SitePdpView components.
// Safe for both Server Components (/seller/site/xem-truoc) and Client Components (Gallery & Code tabs).

import React from "react";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatVnd } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { SiteCatalogView, SitePdpView, SiteRenderer, SiteShell } from "@/features/site";
import { sanitizeSafeImageUrl } from "@/features/site/templateValidator";
import type { SiteTemplateConfig, TemplatePageKey } from "@/features/site/templateTypes";
import { CART_LAYOUT_OPTIONS, INFO_LAYOUT_OPTIONS } from "./siteEditorUtils";
import styles from "./SellerSiteEditor.module.css";

export interface SitePreviewContentProps {
  config: SiteTemplateConfig;
  page: TemplatePageKey;
  shop: SitePublicShopDTO;
  products: Product[];
  categories: CategoryInfo[];
  vouchers: Voucher[];
  platformOrigin: string;
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
      <div className={styles.subpagePreviewWrap} data-cart-layout={cartCfg.layout}>
        <div className={styles.subpagePreviewHead}>
          <h1 className={styles.subpagePreviewTitle}>Giỏ hàng tại {brandName}</h1>
          <span className={styles.subpageLayoutTag}>
            Bố cục: {labelForOption(CART_LAYOUT_OPTIONS, cartCfg.layout)}
          </span>
        </div>

        <div className={isSingle ? styles.cartPreviewStack : styles.cartPreviewGrid}>
          <div className={styles.cartPreviewItems}>
            {sampleItems.map((item, idx) => {
              const qty = idx === 0 ? 2 : 1;
              const imgUrl = sanitizeSafeImageUrl(item.image) || "/banners/campaign-everyday.jpg";
              return (
                <div
                  key={item.id}
                  className={isTable ? styles.cartPreviewRowCompact : styles.cartPreviewRow}
                >
                  <img
                    src={imgUrl}
                    alt={item.name}
                    width={64}
                    height={64}
                    className={styles.cartPreviewThumb}
                  />
                  <div className={styles.cartPreviewItemBody}>
                    <div className={styles.cartPreviewItemName}>{item.name}</div>
                    <div className={styles.cartPreviewItemMeta}>
                      {item.colors[0]?.name || "Mặc định"}, Size {item.sizes[0] || "M"}, SL: {qty}
                    </div>
                  </div>
                  <div className={styles.cartPreviewItemPrice}>{formatVnd(item.price * qty)}</div>
                </div>
              );
            })}

            {cartCfg.show_addons && products[2] && (
              <div className={styles.cartPreviewAddonBox}>
                <div className={styles.cartPreviewAddonTitle}>Gợi ý mua thêm cùng đơn</div>
                <div className={styles.cartPreviewAddonRow}>
                  <span>{products[2].name}</span>
                  <strong>{formatVnd(products[2].price)}</strong>
                </div>
              </div>
            )}
          </div>

          <aside className={styles.cartPreviewSummary}>
            <div className={styles.cartPreviewSummaryTitle}>Tóm tắt đơn hàng</div>
            {cartCfg.show_vouchers && (
              <div className={styles.cartPreviewVoucherBox}>
                <span>Mã giảm giá gian hàng</span>
                <span className={styles.cartPreviewVoucherCode}>
                  {vouchers[0]?.code || "SHOP30K"}
                </span>
              </div>
            )}
            <div className={styles.cartPreviewSummaryRow}>
              <span>Tạm tính</span>
              <strong>{formatVnd(subtotal)}</strong>
            </div>
            <div className={styles.cartPreviewSummaryRow}>
              <span>Vận chuyển</span>
              <span>Tính ở bước thanh toán</span>
            </div>
            <div className={styles.cartPreviewCta}>Tiến hành thanh toán</div>
            {cartCfg.trust_note && (
              <p className={styles.cartPreviewTrustNote}>{cartCfg.trust_note}</p>
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
      <div className={styles.subpagePreviewWrap} data-info-layout={infoCfg.layout}>
        <div className={styles.subpagePreviewHead}>
          <h1 className={styles.subpagePreviewTitle}>Về {brandName}</h1>
          <span className={styles.subpageLayoutTag}>
            Bố cục: {labelForOption(INFO_LAYOUT_OPTIONS, infoCfg.layout)}
          </span>
        </div>

        <div className={infoCfg.show_toc ? styles.infoPreviewWithToc : styles.infoPreviewSingle}>
          {infoCfg.show_toc && (
            <aside className={styles.infoPreviewToc}>
              <div className={styles.infoPreviewTocTitle}>Mục lục</div>
              <ul className={styles.infoPreviewTocList}>
                <li>Câu chuyện thương hiệu</li>
                {infoCfg.show_factory_profile && <li>Hồ sơ xưởng sản xuất</li>}
                <li>Chính sách đổi trả & giao hàng</li>
                {infoCfg.show_showcase && <li>Sản phẩm tiêu biểu</li>}
              </ul>
            </aside>
          )}

          <div className={styles.infoPreviewMain}>
            <div className={styles.infoPreviewHero}>
              <div>
                <p className={styles.infoPreviewLead}>
                  {config.theme.tagline ||
                    shop.description ||
                    `${brandName} cung cấp sản phẩm trực tiếp từ xưởng, minh bạch chất liệu và bảng cỡ.`}
                </p>
              </div>
              <img
                src={bannerImg}
                alt={`Không gian thương hiệu ${brandName}`}
                width={640}
                height={360}
                className={styles.infoPreviewHeroImg}
              />
            </div>

            {textSections.length > 0 && (
              <div className={styles.infoPreviewStoryList}>
                {textSections.map((sec) =>
                  sec.type === "text" ? (
                    <article key={sec.id} className={styles.infoPreviewStoryCard}>
                      <h3>{sec.props.title}</h3>
                      <p>{sec.props.body}</p>
                    </article>
                  ) : null
                )}
              </div>
            )}

            {infoCfg.show_factory_profile && (
              <div className={styles.infoPreviewFactoryGrid}>
                <div className={styles.infoPreviewFactoryCell}>
                  <strong>Nguồn nguyên liệu</strong>
                  <span>Sợi bông chải kỹ & chất liệu kiểm định độ co rút trước khi lên rập.</span>
                </div>
                <div className={styles.infoPreviewFactoryCell}>
                  <strong>Kiểm soát chất lượng</strong>
                  <span>Soi mật độ mũi kim và kiểm phom từng thành phẩm trước đóng gói.</span>
                </div>
                <div className={styles.infoPreviewFactoryCell}>
                  <strong>Hậu mãi minh bạch</strong>
                  <span>Hỗ trợ đổi cỡ tận nơi trong 45 ngày và đồng kiểm COD toàn quốc.</span>
                </div>
              </div>
            )}

            {infoCfg.show_showcase && showcase.length > 0 && (
              <div className={styles.infoPreviewShowcase}>
                <h3>Sản phẩm tiêu biểu của {brandName}</h3>
                <div className={styles.infoPreviewShowcaseGrid}>
                  {showcase.map((p) => {
                    const img = sanitizeSafeImageUrl(p.image) || "/banners/campaign-everyday.jpg";
                    return (
                      <div key={p.id} className={styles.infoPreviewShowcaseItem}>
                        <img src={img} alt={p.name} width={180} height={220} />
                        <div className={styles.infoPreviewShowcaseName}>
                          {p.shortName || p.name}
                        </div>
                        <div className={styles.infoPreviewShowcasePrice}>{formatVnd(p.price)}</div>
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
            shopVouchers={vouchers}
            relatedProducts={products.slice(1, 5)}
            pageConfig={config.pages.pdp}
            templateConfig={config}
            shop={shop}
            previewMode={true}
          />
        ) : (
          <div className={styles.subpagePreviewWrap}>
            <p>Gian hàng chưa có sản phẩm để hiển thị trang chi tiết.</p>
          </div>
        ))}

      {page === "cart" && renderCartPreview()}
      {page === "info" && renderInfoPreview()}
    </SiteShell>
  );
}
