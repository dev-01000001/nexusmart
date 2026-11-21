"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { FlashSaleItem } from "@/data/flash-sale";
import type { Product, SizeCode } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { useStore } from "@/context/StoreContext";
import { formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { ProductReviewSummary } from "@/features/assistant";
import { useSitePathname } from "./SiteChrome";
import { SiteProductCard } from "./SiteProductCard";
import { SiteRenderer } from "./SiteRenderer";
import type { PdpPageConfig, SiteCardStyle, SiteTemplateConfig } from "./templateTypes";
import { sanitizeSafeImageUrl } from "./templateValidator";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

export interface SitePdpViewProps {
  brandName: string;
  product: Product;
  shopVouchers: Voucher[];
  relatedProducts: Product[];
  liveFlashItem?: FlashSaleItem | null;
  pageConfig?: PdpPageConfig;
  templateConfig?: SiteTemplateConfig;
  shop?: SitePublicShopDTO;
  previewMode?: boolean;
}

export function SitePdpView({
  brandName,
  product,
  shopVouchers,
  relatedProducts,
  liveFlashItem = null,
  pageConfig,
  templateConfig,
  shop,
  previewMode = false,
}: SitePdpViewProps) {
  const router = useRouter();
  const pathname = useSitePathname();
  const { addToCart, recordProductView } = useStore();

  const layout = pageConfig?.layout ?? templateConfig?.pages.pdp.layout ?? "classic-split";
  const galleryStyle =
    pageConfig?.gallery_style ?? templateConfig?.pages.pdp.gallery_style ?? "thumbnails";
  const showVouchers = pageConfig?.show_vouchers ?? templateConfig?.pages.pdp.show_vouchers ?? true;
  const showSkuTable =
    pageConfig?.show_sku_table ??
    templateConfig?.pages.pdp.show_sku_table ??
    layout === "sku-order-matrix";
  const showStoryBand =
    pageConfig?.show_story_band ?? templateConfig?.pages.pdp.show_story_band ?? true;
  const showRelated = pageConfig?.show_related ?? templateConfig?.pages.pdp.show_related ?? true;
  const extraSections = pageConfig?.sections ?? templateConfig?.pages.pdp.sections ?? [];
  const cardStyle: SiteCardStyle = templateConfig?.layout.card_style ?? "minimal";

  useEffect(() => {
    if (!previewMode) {
      recordProductView(product.id);
    }
  }, [product.id, previewMode, recordProductView]);

  const gallery = useMemo(() => {
    const raw = [
      product.onModelImage,
      product.image,
      ...(product.gallery ?? []),
      ...product.colors.map((c) => c.image),
    ]
      .map((u) => sanitizeSafeImageUrl(u))
      .filter(Boolean);
    const unique = Array.from(new Set(raw));
    return unique.length > 0 ? unique : ["/banners/fabric-closeup.jpg"];
  }, [product]);

  const defaultSku = useMemo(
    () => product.skus.find((s) => s.stock > 0) ?? product.skus[0],
    [product.skus]
  );

  const [selectedColor, setSelectedColor] = useState<string>(
    defaultSku?.colorCode ?? product.colors[0]?.code ?? ""
  );
  const [selectedSize, setSelectedSize] = useState<SizeCode | string>(
    defaultSku?.size ?? product.sizes[0] ?? "M"
  );
  const [activeImage, setActiveImage] = useState<string>(gallery[0]);
  const [qty, setQty] = useState<number>(1);
  const [busyAction, setBusyAction] = useState<"add" | "buy" | null>(null);
  const [addedNotice, setAddedNotice] = useState<string | null>(null);

  const selectedSku = useMemo(() => {
    return (
      product.skus.find((s) => s.colorCode === selectedColor && s.size === selectedSize) ??
      product.skus.find((s) => s.size === selectedSize) ??
      product.skus.find((s) => s.colorCode === selectedColor) ??
      product.skus[0]
    );
  }, [product.skus, selectedColor, selectedSize]);

  const maxStock = Math.max(0, selectedSku?.stock ?? 0);
  const clampedQty = maxStock > 0 ? Math.min(qty, maxStock) : 1;
  const skuId = selectedSku?.skuId || "";
  const flashSaleItemId = skuId ? liveFlashItem?.itemIdBySku?.[skuId] : undefined;
  const flashPrice =
    flashSaleItemId && liveFlashItem && liveFlashItem.flashPrice > 0
      ? liveFlashItem.flashPrice
      : null;
  const skuPrice = selectedSku?.price ?? product.price;
  const displayPrice = flashPrice ?? skuPrice;
  const comparePrice = flashPrice ? skuPrice : product.originalPrice;
  const hasDiscount = typeof comparePrice === "number" && comparePrice > displayPrice;

  // Strict defense in depth: only SHOP vouchers render on a shop's subdomain PDP.
  const validShopVouchers = useMemo(
    () => shopVouchers.filter((v) => v.scope === "shop"),
    [shopVouchers]
  );

  const colorNameMap = useMemo(
    () => new Map(product.colors.map((c) => [c.code, c])),
    [product.colors]
  );

  const handleCartAction = async (mode: "add" | "buy") => {
    if (previewMode) {
      setAddedNotice(
        mode === "buy"
          ? "Chế độ xem trước: nút Mua ngay sẽ đưa khách tới thanh toán khi xuất bản."
          : "Chế độ xem trước: sản phẩm sẽ được thêm vào giỏ hàng khi xuất bản."
      );
      return;
    }
    if (!skuId || maxStock <= 0 || busyAction) return;
    setBusyAction(mode);
    setAddedNotice(null);
    try {
      const res = await addToCart({
        skuId,
        quantity: clampedQty,
        flashSaleItemId,
        productName: product.shortName || product.name,
        size: String(selectedSize),
      });
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent(pathname || "/san-pham")}`);
        return;
      }
      if (res.ok) {
        if (mode === "buy") {
          router.push("/thanh-toan");
          return;
        }
        setAddedNotice(res.message || "Đã thêm sản phẩm vào giỏ hàng.");
      }
    } finally {
      setBusyAction(null);
    }
  };

  const renderGallery = () => {
    if (galleryStyle === "stacked" || layout === "editorial-tall") {
      return (
        <div className={tplStyles.pdpStackedGallery} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {gallery.slice(0, 4).map((src, idx) => (
            <div key={src} className={styles.pdpMainFrame}>
              <img
                src={src}
                alt={`${product.name} ảnh ${idx + 1}`}
                width={896}
                height={1200}
                className={styles.pdpMainImg}
              />
            </div>
          ))}
        </div>
      );
    }

    const frameClass =
      galleryStyle === "compact" || layout === "dense-spec" || layout === "sku-order-matrix"
        ? tplStyles.pdpCompactMainFrame
        : styles.pdpMainFrame;

    const mainImageEl = (
      <div className={frameClass}>
        <img
          src={activeImage}
          alt={product.name}
          width={896}
          height={1200}
          className={styles.pdpMainImg}
        />
      </div>
    );

    return (
      <div className={styles.pdpMediaCol} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
        {galleryStyle === "framed" || layout === "luxury-framed" ? (
          <div className={tplStyles.pdpLuxuryFrame}>{mainImageEl}</div>
        ) : (
          mainImageEl
        )}

        {gallery.length > 1 && (
          <div className={styles.pdpThumbRow}>
            {gallery.slice(0, 4).map((src, idx) => (
              <button
                key={src}
                type="button"
                className={`${styles.pdpThumbBtn} ${activeImage === src ? styles.pdpThumbActive : ""}`}
                onClick={() => setActiveImage(src)}
                aria-label={`Xem ảnh ${idx + 1} của ${product.name}`}
              >
                <img
                  src={src}
                  alt={`${product.name} ảnh ${idx + 1}`}
                  width={240}
                  height={320}
                  className={styles.pdpMainImg}
                />
              </button>
            ))}
          </div>
        )}
      </div>
    );
  };

  const renderSkuMatrixTable = () => {
    if (!showSkuTable && layout !== "sku-order-matrix") return null;
    if (product.skus.length === 0) return null;
    return (
      <div className={tplStyles.pdpSkuMatrixWrap} data-reveal="">
        <div className={styles.sectionHead} style={{ marginBottom: 4 }}>
          <div className={styles.sectionHeadCopy}>
            <h2 className={styles.pdpOptionLabel}>Bảng biến thể SKU sẵn kho (bấm dòng để chọn nhanh)</h2>
          </div>
        </div>
        <div className={tplStyles.tableScrollWrap}>
          <table className={tplStyles.skuTable}>
            <thead>
              <tr>
                <th>Mã SKU</th>
                <th>Màu sắc</th>
                <th>Kích cỡ</th>
                <th>Đơn giá</th>
                <th>Tồn kho</th>
                <th>Trạng thái</th>
              </tr>
            </thead>
            <tbody>
              {product.skus.map((sku) => {
                const color = colorNameMap.get(sku.colorCode);
                const isSelected = sku.colorCode === selectedColor && sku.size === selectedSize;
                const available = sku.stock > 0;
                return (
                  <tr
                    key={`${sku.skuCode}-${sku.colorCode}-${sku.size}`}
                    className={`${available ? tplStyles.skuTableRowInteractive : ""} ${
                      isSelected ? tplStyles.skuTableRowActive : ""
                    }`}
                    onClick={() => {
                      if (!available) return;
                      setSelectedColor(sku.colorCode);
                      setSelectedSize(sku.size);
                      const colorImg = sanitizeSafeImageUrl(color?.image);
                      if (colorImg) setActiveImage(colorImg);
                      setQty(1);
                      setAddedNotice(null);
                    }}
                  >
                    <td className={tplStyles.skuCodeMono}>{sku.skuCode}</td>
                    <td>
                      <span className={tplStyles.colorSwatchInline}>
                        <span
                          className={tplStyles.colorDot}
                          style={{ backgroundColor: color?.hex ?? "#1F3038" }}
                          aria-hidden="true"
                        />
                        <span>{color?.name ?? sku.colorCode}</span>
                      </span>
                    </td>
                    <td>{sku.size}</td>
                    <td className="num">{formatVnd(sku.price)}</td>
                    <td className="num">{formatInt(sku.stock)}</td>
                    <td>
                      <span
                        className={`${tplStyles.stockBadge} ${!available ? tplStyles.stockBadgeOut : ""}`}
                      >
                        {isSelected ? "Đang chọn" : available ? "Chọn phân loại" : "Hết hàng"}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    );
  };

  const renderVariantSelectors = () => (
    <>
      {product.colors.length > 0 && (
        <div className={styles.pdpOptionGroup}>
          <span className={styles.pdpOptionLabel}>
            Màu sắc: {colorNameMap.get(selectedColor)?.name ?? selectedColor}
          </span>
          <div className={styles.pdpOptionList}>
            {product.colors.map((color) => {
              const active = color.code === selectedColor;
              return (
                <button
                  key={color.code}
                  type="button"
                  className={`${styles.pdpOptionBtn} ${active ? styles.pdpOptionBtnActive : ""}`}
                  onClick={() => {
                    setSelectedColor(color.code);
                    const colorImg = sanitizeSafeImageUrl(color.image);
                    if (colorImg) setActiveImage(colorImg);
                    setQty(1);
                    setAddedNotice(null);
                  }}
                >
                  {color.name}
                </button>
              );
            })}
          </div>
        </div>
      )}

      {product.sizes.length > 0 && (
        <div className={styles.pdpOptionGroup}>
          <span className={styles.pdpOptionLabel}>Kích cỡ: {selectedSize}</span>
          <div className={styles.pdpOptionList}>
            {product.sizes.map((size) => {
              const skuMatch = product.skus.find(
                (s) => s.colorCode === selectedColor && s.size === size
              );
              const available = (skuMatch?.stock ?? 0) > 0;
              const active = size === selectedSize;
              return (
                <button
                  key={size}
                  type="button"
                  disabled={!available}
                  className={`${styles.pdpOptionBtn} ${active ? styles.pdpOptionBtnActive : ""}`}
                  onClick={() => {
                    setSelectedSize(size);
                    setQty(1);
                    setAddedNotice(null);
                  }}
                >
                  {size}
                </button>
              );
            })}
          </div>
        </div>
      )}

      <div className={styles.pdpQtyRow}>
        <div className={styles.qtyStepper} data-ui="">
          <button
            type="button"
            className={styles.qtyBtn}
            disabled={clampedQty <= 1 || maxStock <= 0}
            onClick={() => setQty((q) => Math.max(1, q - 1))}
            aria-label="Giảm số lượng"
          >
            −
          </button>
          <span className={`${styles.qtyValue} num`}>{clampedQty}</span>
          <button
            type="button"
            className={styles.qtyBtn}
            disabled={maxStock <= 0 || clampedQty >= maxStock}
            onClick={() => setQty((q) => Math.min(maxStock, q + 1))}
            aria-label="Tăng số lượng"
          >
            +
          </button>
        </div>
        <span className={`${styles.productStockTag} num`}>
          {maxStock > 0
            ? `Còn ${formatInt(maxStock)} sản phẩm sẵn giao`
            : "Phân loại này hiện tạm hết hàng"}
        </span>
      </div>
    </>
  );

  const renderBuyboxContent = () => (
    <>
      <h1 className={styles.pdpTitle}>{product.name}</h1>

      <div className={styles.pdpPriceWrap}>
        <span className={`${styles.pdpPrice} num`}>{formatVnd(displayPrice)}</span>
        {hasDiscount && comparePrice ? (
          <>
            <span className={`${styles.productOriginalPrice} num`}>{formatVnd(comparePrice)}</span>
            <span className={styles.filterChipActive} style={{ padding: "4px 10px" }}>
              {formatDiscountPercent(comparePrice, displayPrice)}
            </span>
          </>
        ) : null}
      </div>

      {layout === "sport-telemetry" && (
        <div className={tplStyles.pdpTelemetryStrip}>
          <div className={tplStyles.pdpTelemetryBadge}>
            <span className={tplStyles.pdpStoryLabel}>Định lượng</span>
            <span className={`${tplStyles.pdpStoryVal} num`}>
              {product.gsm > 0 ? `${product.gsm}gsm` : "Siêu nhẹ"}
            </span>
          </div>
          <div className={tplStyles.pdpTelemetryBadge}>
            <span className={tplStyles.pdpStoryLabel}>Phom dáng</span>
            <span className={tplStyles.pdpStoryVal}>{product.fit || "Athletic"}</span>
          </div>
          <div className={tplStyles.pdpTelemetryBadge}>
            <span className={tplStyles.pdpStoryLabel}>Sẵn kho</span>
            <span className={`${tplStyles.pdpStoryVal} num`}>{formatInt(maxStock)}</span>
          </div>
        </div>
      )}

      <p className={styles.prosePara}>{product.description}</p>

      {(product.fabric || product.fit || product.origin) && (
        <p className={styles.productSpec}>
          {[
            product.fabric,
            product.gsm > 0 ? `${product.gsm}gsm` : "",
            product.fit,
            product.origin,
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
      )}

      {layout === "one-page-drawer" ? (
        <div className={tplStyles.pdpOnePageStep}>{renderVariantSelectors()}</div>
      ) : (
        renderVariantSelectors()
      )}

      {showVouchers && validShopVouchers.length > 0 && (
        <div className={styles.pdpShopVoucherBox} data-reveal="">
          <div className={styles.voucherFoot} style={{ paddingTop: 0 }}>
            <p className={styles.pdpOptionLabel}>Ưu đãi riêng của {brandName}</p>
            {previewMode ? (
              <span className={styles.sectionLink}>Xem kho mã</span>
            ) : (
              <Link href="/voucher" className={styles.sectionLink}>
                Xem kho mã
              </Link>
            )}
          </div>
          {validShopVouchers.slice(0, 3).map((v) => (
            <p
              key={v.id || v.code}
              className={styles.pdpHandoffNote}
              style={{ color: "inherit" }}
            >
              <strong>{v.code}</strong> ·{" "}
              {v.discountType === "percent"
                ? `Giảm ${v.discountValue}%`
                : `Giảm ${formatVnd(v.discountValue)}`}
              {v.minOrderValue > 0 ? ` cho đơn từ ${formatVnd(v.minOrderValue)}` : ""}
            </p>
          ))}
        </div>
      )}

      <div className={styles.pdpCtaStack}>
        {maxStock > 0 && (skuId || previewMode) ? (
          <>
            <button
              type="button"
              className={styles.btnPrimary}
              disabled={busyAction !== null}
              onClick={() => void handleCartAction("buy")}
            >
              {busyAction === "buy"
                ? "Đang chuyển tới thanh toán..."
                : `Mua ngay · ${formatVnd(displayPrice * clampedQty)}`}
            </button>
            <button
              type="button"
              className={styles.btnSecondary}
              disabled={busyAction !== null}
              onClick={() => void handleCartAction("add")}
            >
              {busyAction === "add" ? "Đang thêm vào giỏ..." : "Thêm vào giỏ hàng"}
            </button>
          </>
        ) : (
          <button type="button" className={styles.btnPrimary} disabled>
            Phân loại tạm hết hàng
          </button>
        )}

        {addedNotice && (
          <p className={styles.pdpAddedNotice} role="status">
            <span>{addedNotice}</span>{" "}
            {!previewMode && (
              <Link href="/gio-hang" className={styles.sectionLink}>
                Mở giỏ hàng
              </Link>
            )}
          </p>
        )}

        <p className={styles.pdpHandoffNote}>
          Đặt hàng trực tiếp tại {brandName}, hỗ trợ thanh toán khi nhận hàng (COD) đồng kiểm hoặc
          chuyển khoản VietQR.
        </p>
      </div>

      {product.highlights.length > 0 && (
        <div className={styles.pdpOptionGroup} data-reveal="">
          <h2 className={styles.pdpOptionLabel}>Điểm nổi bật</h2>
          <ul className={styles.pdpDetailList}>
            {product.highlights.map((item, idx) => (
              <li key={idx}>{item}</li>
            ))}
          </ul>
        </div>
      )}
    </>
  );

  const useEditorialTwoCol = layout === "editorial-tall" || galleryStyle === "stacked";
  const useDenseTwoCol = layout === "dense-spec" || layout === "sku-order-matrix";
  const relatedGridClass =
    relatedProducts.length === 3 ? tplStyles.gridCols3 : tplStyles.gridCols4;

  return (
    <div data-pdp-layout={layout} data-pdp-gallery={galleryStyle}>
      <section className={styles.siteSection} style={{ paddingTop: 24 }}>
        <div className={tplStyles.container}>
          <nav className={styles.breadcrumb} aria-label="Đường dẫn trang">
            {previewMode ? (
              <span className={styles.breadcrumbLink}>Trang chủ</span>
            ) : (
              <Link href="/" className={styles.breadcrumbLink}>
                Trang chủ
              </Link>
            )}
            <span aria-hidden="true">/</span>
            {previewMode ? (
              <span className={styles.breadcrumbLink}>Sản phẩm</span>
            ) : (
              <Link href="/san-pham" className={styles.breadcrumbLink}>
                Sản phẩm
              </Link>
            )}
            <span aria-hidden="true">/</span>
            <span>{product.name}</span>
          </nav>

          {useEditorialTwoCol ? (
            <div className={tplStyles.pdpEditorialGrid}>
              {renderGallery()}
              <div className={tplStyles.pdpStickyBuybox}>{renderBuyboxContent()}</div>
            </div>
          ) : useDenseTwoCol ? (
            <div className={tplStyles.pdpDenseGrid}>
              {renderGallery()}
              <div className={tplStyles.pdpStickyBuybox} style={{ position: "static" }}>
                {renderBuyboxContent()}
              </div>
            </div>
          ) : (
            <div className={styles.pdpGrid}>
              <div
                className={styles.pdpHeadBlock}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                <h1 className={styles.pdpTitle}>{product.name}</h1>

                <div className={styles.pdpPriceWrap}>
                  <span className={`${styles.pdpPrice} num`}>{formatVnd(displayPrice)}</span>
                  {hasDiscount && comparePrice ? (
                    <>
                      <span className={`${styles.productOriginalPrice} num`}>
                        {formatVnd(comparePrice)}
                      </span>
                      <span className={styles.filterChipActive} style={{ padding: "4px 10px" }}>
                        {formatDiscountPercent(comparePrice, displayPrice)}
                      </span>
                    </>
                  ) : null}
                </div>
              </div>

              {renderGallery()}

              <div
                className={styles.pdpInfoCol}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {layout === "sport-telemetry" && (
                  <div className={tplStyles.pdpTelemetryStrip}>
                    <div className={tplStyles.pdpTelemetryBadge}>
                      <span className={tplStyles.pdpStoryLabel}>Định lượng</span>
                      <span className={`${tplStyles.pdpStoryVal} num`}>
                        {product.gsm > 0 ? `${product.gsm}gsm` : "Siêu nhẹ"}
                      </span>
                    </div>
                    <div className={tplStyles.pdpTelemetryBadge}>
                      <span className={tplStyles.pdpStoryLabel}>Phom dáng</span>
                      <span className={tplStyles.pdpStoryVal}>{product.fit || "Athletic"}</span>
                    </div>
                    <div className={tplStyles.pdpTelemetryBadge}>
                      <span className={tplStyles.pdpStoryLabel}>Sẵn kho</span>
                      <span className={`${tplStyles.pdpStoryVal} num`}>{formatInt(maxStock)}</span>
                    </div>
                  </div>
                )}

                <p className={styles.prosePara}>{product.description}</p>

                {(product.fabric || product.fit || product.origin) && (
                  <p className={styles.productSpec}>
                    {[
                      product.fabric,
                      product.gsm > 0 ? `${product.gsm}gsm` : "",
                      product.fit,
                      product.origin,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                )}

                {layout === "one-page-drawer" ? (
                  <div className={tplStyles.pdpOnePageStep}>{renderVariantSelectors()}</div>
                ) : (
                  renderVariantSelectors()
                )}

                {showVouchers && validShopVouchers.length > 0 && (
                  <div className={styles.pdpShopVoucherBox} data-reveal="">
                    <div className={styles.voucherFoot} style={{ paddingTop: 0 }}>
                      <p className={styles.pdpOptionLabel}>Ưu đãi riêng của {brandName}</p>
                      {previewMode ? (
                        <span className={styles.sectionLink}>Xem kho mã</span>
                      ) : (
                        <Link href="/voucher" className={styles.sectionLink}>
                          Xem kho mã
                        </Link>
                      )}
                    </div>
                    {validShopVouchers.slice(0, 3).map((v) => (
                      <p
                        key={v.id || v.code}
                        className={styles.pdpHandoffNote}
                        style={{ color: "inherit" }}
                      >
                        <strong>{v.code}</strong> ·{" "}
                        {v.discountType === "percent"
                          ? `Giảm ${v.discountValue}%`
                          : `Giảm ${formatVnd(v.discountValue)}`}
                        {v.minOrderValue > 0 ? ` cho đơn từ ${formatVnd(v.minOrderValue)}` : ""}
                      </p>
                    ))}
                  </div>
                )}

                <div className={styles.pdpCtaStack}>
                  {maxStock > 0 && (skuId || previewMode) ? (
                    <>
                      <button
                        type="button"
                        className={styles.btnPrimary}
                        disabled={busyAction !== null}
                        onClick={() => void handleCartAction("buy")}
                      >
                        {busyAction === "buy"
                          ? "Đang chuyển tới thanh toán..."
                          : `Mua ngay · ${formatVnd(displayPrice * clampedQty)}`}
                      </button>
                      <button
                        type="button"
                        className={styles.btnSecondary}
                        disabled={busyAction !== null}
                        onClick={() => void handleCartAction("add")}
                      >
                        {busyAction === "add" ? "Đang thêm vào giỏ..." : "Thêm vào giỏ hàng"}
                      </button>
                    </>
                  ) : (
                    <button type="button" className={styles.btnPrimary} disabled>
                      Phân loại tạm hết hàng
                    </button>
                  )}

                  {addedNotice && (
                    <p className={styles.pdpAddedNotice} role="status">
                      <span>{addedNotice}</span>{" "}
                      {!previewMode && (
                        <Link href="/gio-hang" className={styles.sectionLink}>
                          Mở giỏ hàng
                        </Link>
                      )}
                    </p>
                  )}

                  <p className={styles.pdpHandoffNote}>
                    Đặt hàng trực tiếp tại {brandName}, hỗ trợ thanh toán khi nhận hàng (COD) đồng
                    kiểm hoặc chuyển khoản VietQR.
                  </p>
                </div>

                {product.highlights.length > 0 && (
                  <div className={styles.pdpOptionGroup} data-reveal="">
                    <h2 className={styles.pdpOptionLabel}>Điểm nổi bật</h2>
                    <ul className={styles.pdpDetailList}>
                      {product.highlights.map((item, idx) => (
                        <li key={idx}>{item}</li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            </div>
          )}

          {renderSkuMatrixTable()}

          {showStoryBand && (product.fabric || product.fit || product.origin || product.gsm > 0) && (
            <div className={tplStyles.pdpStoryBand} data-reveal="">
              <div className={tplStyles.pdpStoryCell}>
                <span className={tplStyles.pdpStoryLabel}>Chất liệu</span>
                <span className={tplStyles.pdpStoryVal}>{product.fabric || "Sợi chọn lọc"}</span>
              </div>
              <div className={tplStyles.pdpStoryCell}>
                <span className={tplStyles.pdpStoryLabel}>Định lượng vải</span>
                <span className={`${tplStyles.pdpStoryVal} num`}>
                  {product.gsm > 0 ? `${product.gsm}gsm` : "Tiêu chuẩn xưởng"}
                </span>
              </div>
              <div className={tplStyles.pdpStoryCell}>
                <span className={tplStyles.pdpStoryLabel}>Phom mặc</span>
                <span className={tplStyles.pdpStoryVal}>{product.fit || "Regular"}</span>
              </div>
              <div className={tplStyles.pdpStoryCell}>
                <span className={tplStyles.pdpStoryLabel}>Xuất xứ hoàn thiện</span>
                <span className={tplStyles.pdpStoryVal}>{product.origin || brandName}</span>
              </div>
            </div>
          )}

          {!previewMode && (
            <div className={tplStyles.pdpReviewSummaryWrap}>
              <ProductReviewSummary
                productId={product.backendId || product.id}
                productName={product.name}
              />
            </div>
          )}
        </div>
      </section>

      {templateConfig && extraSections.length > 0 && (
        <SiteRenderer
          config={templateConfig}
          shop={shop ?? { id: "", name: brandName }}
          products={relatedProducts}
          vouchers={shopVouchers}
          previewMode={previewMode}
          sectionsOverride={extraSections}
        />
      )}

      {showRelated && relatedProducts.length > 0 && (
        <section className={`${styles.siteSection} ${styles.siteSectionSurface}`}>
          <div className={tplStyles.container}>
            <div className={styles.sectionHead} data-reveal="">
              <div className={styles.sectionHeadCopy}>
                <h2 className={styles.sectionTitle}>Sản phẩm khác của {brandName}</h2>
              </div>
              {previewMode ? (
                <span className={styles.sectionLink}>Xem tất cả</span>
              ) : (
                <Link href="/san-pham" className={styles.sectionLink}>
                  Xem tất cả
                </Link>
              )}
            </div>
            <div className={relatedGridClass}>
              {relatedProducts.map((item, idx) => (
                <SiteProductCard
                  key={item.id}
                  product={item}
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
