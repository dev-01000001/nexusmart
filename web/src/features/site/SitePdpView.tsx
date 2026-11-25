"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { FlashSaleItem } from "@/data/flash-sale";
import type { Product, SizeCode } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import type {
  GatewayActiveShopDiscount,
  GatewayCarrierQuote,
  GatewayProductReviewsResponse,
  GatewayProvince,
  GatewayWard,
} from "@/lib/gateway-types";
import { api } from "@/lib/api";
import { IconBag, IconCheck, IconClose } from "@/components/Icons";
import { useSession } from "@/components/SessionProvider";
import { useModalFocusTrap } from "@/components/modalFocus";
import { useStore } from "@/context/StoreContext";
import {
  formatCarrierLabel,
  formatDateVi,
  formatDiscountPercent,
  formatInt,
  formatRatingFromX100,
  formatVnd,
} from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { ProductReviewSummary } from "@/features/assistant";
import { resolveSkuEffectivePromotion } from "@/features/checkout/helpers";
import { formatServiceLabel } from "@/features/commerce/cartEnrich";
import shared from "@/features/commerce/commerce.module.css";
import { parseSafeReviewImageUrl } from "@/features/product/reviewHelpers";
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
  initialReviews?: GatewayProductReviewsResponse | null;
  activeShopDiscountBySkuId?: Record<string, GatewayActiveShopDiscount>;
  provinces?: GatewayProvince[];
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
  initialReviews = null,
  activeShopDiscountBySkuId = {},
  provinces = [],
  pageConfig,
  templateConfig,
  shop,
  previewMode = false,
}: SitePdpViewProps) {
  const router = useRouter();
  const pathname = useSitePathname();
  const { isAuthenticated } = useSession();
  const { addToCart, recordProductView } = useStore();

  const layout = pageConfig?.layout ?? templateConfig?.pages.pdp.layout ?? "classic-split";
  const galleryStyle =
    pageConfig?.gallery_style ??
    templateConfig?.pages.pdp.gallery_style ??
    (layout === "editorial-tall" ? "stacked" : "thumbnails");
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
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerClosing, setDrawerClosing] = useState(false);
  const [addedNotice, setAddedNotice] = useState<string | null>(null);
  const cartDrawerRef = useRef<HTMLElement | null>(null);
  const cartTriggerRef = useRef<HTMLElement | null>(null);
  const drawerRestoreFocusRef = useRef(true);
  const drawerCloseTimerRef = useRef<number | null>(null);

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
  const activeShopDiscount = skuId ? (activeShopDiscountBySkuId[skuId] ?? null) : null;
  const effectivePromo = resolveSkuEffectivePromotion({
    regularPriceVnd: skuPrice,
    liveFlash:
      liveFlashItem && flashSaleItemId && flashPrice
        ? {
            flashSaleItemId,
            flashPriceVnd: flashPrice,
            remainingSlots: Math.max(0, liveFlashItem.remainingQuota),
            perUserLimit: Math.max(0, liveFlashItem.limitPerBuyer),
          }
        : null,
    activeShopDiscount,
  });
  const displayPrice = effectivePromo.effectiveUnitPriceVnd;
  const comparePrice =
    effectivePromo.kind !== "none" ? effectivePromo.originalUnitPriceVnd : product.originalPrice;
  const hasDiscount = typeof comparePrice === "number" && comparePrice > displayPrice;

  // Shipping fee estimator state (guest-friendly via POST /api/checkout/shipping-quote)
  const [shipProvinceCode, setShipProvinceCode] = useState<string>("");
  const [shipWards, setShipWards] = useState<GatewayWard[]>([]);
  const [shipWardCode, setShipWardCode] = useState<string>("");
  const [loadingShipWards, setLoadingShipWards] = useState<boolean>(false);
  const [shipQuotes, setShipQuotes] = useState<GatewayCarrierQuote[]>([]);
  const [shipIsEstimated, setShipIsEstimated] = useState<boolean>(false);
  const [loadingShipQuote, setLoadingShipQuote] = useState<boolean>(false);
  const [shipQuoteError, setShipQuoteError] = useState<string | null>(null);

  // Real buyer reviews state (GET /api/products/[id]/reviews)
  const backendProductId = product.backendId || product.id;
  const [reviewsData, setReviewsData] = useState<GatewayProductReviewsResponse | null>(
    initialReviews
  );
  const [reviewStarFilter, setReviewStarFilter] = useState<number>(0);
  const [reviewPage, setReviewPage] = useState<number>(1);
  const [loadingReviews, setLoadingReviews] = useState<boolean>(false);

  useEffect(() => {
    if (previewMode || !shipProvinceCode) {
      setShipWards([]);
      setShipWardCode("");
      setShipQuotes([]);
      return;
    }
    let active = true;
    setLoadingShipWards(true);
    setShipWardCode("");
    setShipQuotes([]);
    setShipQuoteError(null);
    api<{ wards?: GatewayWard[] }>(
      `/api/checkout/locations?province_code=${encodeURIComponent(shipProvinceCode)}`,
      { retryOn401: false }
    )
      .then((res) => {
        if (active) setShipWards(res.wards ?? []);
      })
      .catch(() => {
        if (active) setShipWards([]);
      })
      .finally(() => {
        if (active) setLoadingShipWards(false);
      });
    return () => {
      active = false;
    };
  }, [previewMode, shipProvinceCode]);

  useEffect(() => {
    if (previewMode || !shipProvinceCode || !shipWardCode || !skuId) {
      setShipQuotes([]);
      return;
    }
    let active = true;
    setLoadingShipQuote(true);
    setShipQuoteError(null);
    api<{ quotes?: GatewayCarrierQuote[]; is_estimated?: boolean }>(
      "/api/checkout/shipping-quote",
      {
        method: "POST",
        body: {
          to_province_code: shipProvinceCode,
          to_ward_code: shipWardCode,
          items: [{ sku_id: skuId, quantity: clampedQty }],
          payment_method: "VIETQR",
        },
        retryOn401: isAuthenticated,
      }
    )
      .then((res) => {
        if (!active) return;
        setShipQuotes(res.quotes ?? []);
        setShipIsEstimated(Boolean(res.is_estimated));
      })
      .catch(() => {
        if (!active) return;
        setShipQuotes([]);
        setShipQuoteError("Chưa thể ước tính phí giao hàng cho khu vực này lúc này.");
      })
      .finally(() => {
        if (active) setLoadingShipQuote(false);
      });
    return () => {
      active = false;
    };
  }, [previewMode, shipProvinceCode, shipWardCode, skuId, clampedQty, isAuthenticated]);

  const fetchReviewsPage = useCallback(
    async (page: number, rating: number) => {
      if (previewMode || !backendProductId) return;
      setLoadingReviews(true);
      try {
        const qs = new URLSearchParams({ page: String(page), page_size: "5" });
        if (rating >= 1 && rating <= 5) qs.set("rating", String(rating));
        const res = await api<GatewayProductReviewsResponse>(
          `/api/products/${encodeURIComponent(backendProductId)}/reviews?${qs.toString()}`,
          { retryOn401: false }
        );
        setReviewsData(res);
      } catch {
        // Keep current reviews snapshot on transient error
      } finally {
        setLoadingReviews(false);
      }
    },
    [backendProductId, previewMode]
  );

  const validShopVouchers = useMemo(
    () => shopVouchers.filter((v) => v.scope === "shop"),
    [shopVouchers]
  );

  const colorNameMap = useMemo(
    () => new Map(product.colors.map((c) => [c.code, c])),
    [product.colors]
  );

  const closeDrawer = useCallback(
    (restoreFocus = true) => {
      if (!drawerOpen || drawerClosing) return;
      drawerRestoreFocusRef.current = restoreFocus;
      setDrawerClosing(true);
      drawerCloseTimerRef.current = window.setTimeout(() => {
        drawerCloseTimerRef.current = null;
        setDrawerOpen(false);
        setDrawerClosing(false);
      }, 180);
    },
    [drawerOpen, drawerClosing]
  );

  useEffect(() => {
    return () => {
      if (drawerCloseTimerRef.current !== null) {
        window.clearTimeout(drawerCloseTimerRef.current);
      }
    };
  }, []);

  useModalFocusTrap({
    open: !previewMode && drawerOpen && !drawerClosing,
    containerRef: cartDrawerRef,
    onEscape: () => closeDrawer(true),
    shouldRestoreFocusRef: drawerRestoreFocusRef,
    returnFocusToRef: cartTriggerRef,
  });

  const handleSelectColor = (colorCode: string, colorImgRaw?: string) => {
    setSelectedColor(colorCode);
    const colorImg = sanitizeSafeImageUrl(colorImgRaw);
    if (colorImg) {
      setActiveImage(colorImg);
    }
    setQty(1);
    setAddedNotice(null);
  };

  const handleCartAction = async (mode: "add" | "buy", triggerEl?: HTMLElement | null) => {
    if (previewMode) {
      setAddedNotice(
        mode === "buy"
          ? "Xem trước: nút Mua ngay sẽ đưa tới thanh toán khi xuất bản."
          : "Xem trước: sản phẩm sẽ được thêm vào giỏ khi xuất bản."
      );
      return;
    }
    if (!skuId || maxStock <= 0 || busyAction) return;
    if (mode === "add") {
      cartTriggerRef.current =
        triggerEl ??
        (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    }
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
        setAddedNotice(res.message || "Đã thêm vào giỏ hàng.");
        if (drawerCloseTimerRef.current !== null) {
          window.clearTimeout(drawerCloseTimerRef.current);
          drawerCloseTimerRef.current = null;
        }
        drawerRestoreFocusRef.current = true;
        setDrawerClosing(false);
        setDrawerOpen(true);
      }
    } finally {
      setBusyAction(null);
    }
  };

  // Crossfade stage ensures switching colors smoothly transitions without flash
  const crossfadeGallery = useMemo(() => {
    if (gallery.includes(activeImage)) return gallery;
    return [activeImage, ...gallery];
  }, [gallery, activeImage]);

  const secondaryGallery = useMemo(
    () => gallery.filter((src) => src !== activeImage).slice(0, 3),
    [gallery, activeImage]
  );

  const renderCrossfadeFrame = (frameClass: string) => (
    <div className={`${frameClass} ${tplStyles.pdpCrossfadeFrame}`}>
      {crossfadeGallery.map((src, idx) => {
        const isCurrent = src === activeImage;
        return (
          <img
            key={src}
            src={src}
            alt={isCurrent ? product.name : ""}
            aria-hidden={!isCurrent}
            width={896}
            height={1200}
            className={`${tplStyles.pdpCrossfadeImg} ${
              isCurrent ? tplStyles.pdpCrossfadeImgActive : ""
            }`}
            loading={idx === 0 ? "eager" : "lazy"}
          />
        );
      })}
    </div>
  );

  const renderThumbStrip = (rowClass: string) =>
    gallery.length > 1 ? (
      <div className={rowClass}>
        {gallery.slice(0, 4).map((src, idx) => (
          <button
            key={src}
            type="button"
            className={`${styles.pdpThumbBtn} ${activeImage === src ? styles.pdpThumbActive : ""}`}
            onClick={() => setActiveImage(src)}
            aria-label={`Xem ảnh ${idx + 1}`}
            aria-pressed={activeImage === src}
          >
            <img src={src} alt="" width={240} height={320} className={styles.pdpMainImg} />
          </button>
        ))}
      </div>
    ) : null;

  const renderGallery = () => {
    const isDense =
      layout === "dense-spec" || layout === "sku-order-matrix" || galleryStyle === "compact";
    const isLuxury = layout === "luxury-framed" || galleryStyle === "framed";
    const isThumbnails = galleryStyle === "thumbnails" && layout !== "editorial-tall";

    if (isDense) {
      return (
        <div className={styles.pdpMediaCol}>
          {renderCrossfadeFrame(tplStyles.pdpCompactMainFrame)}
          {renderThumbStrip(styles.pdpThumbRow)}
        </div>
      );
    }

    if (isLuxury) {
      return (
        <div className={styles.pdpMediaCol}>
          <div className={tplStyles.pdpLuxuryFrame}>
            {renderCrossfadeFrame(styles.pdpMainFrame)}
          </div>
          {renderThumbStrip(styles.pdpThumbRow)}
        </div>
      );
    }

    if (isThumbnails) {
      return (
        <div className={styles.pdpMediaCol}>
          {renderCrossfadeFrame(styles.pdpMainFrame)}
          {renderThumbStrip(styles.pdpThumbRow)}
        </div>
      );
    }

    // Stacked vertical scrolling photography gallery (lead crossfade frame + tall stacked secondary frames)
    return (
      <div className={tplStyles.pdpStackedGallery}>
        {renderCrossfadeFrame(styles.pdpMainFrame)}
        {renderThumbStrip(tplStyles.pdpMobileThumbRow)}

        {/* Desktop vertical scroll continuation frames */}
        {secondaryGallery.map((src, idx) => (
          <figure key={src} className={tplStyles.pdpScrollPhoto} data-reveal="clip">
            <img
              src={src}
              alt={`${product.name} góc ${idx + 2}`}
              width={896}
              height={1200}
              className={styles.pdpMainImg}
              loading="lazy"
            />
          </figure>
        ))}
      </div>
    );
  };

  const renderSkuMatrixTable = () => {
    if (!showSkuTable && layout !== "sku-order-matrix") return null;
    if (product.skus.length === 0) return null;
    return (
      <div className={tplStyles.pdpSkuMatrixWrap}>
        <h2 className={styles.pdpOptionLabel}>Bảng biến thể SKU</h2>
        <div className={tplStyles.tableScrollWrap}>
          <table className={tplStyles.skuTable}>
            <thead>
              <tr>
                <th>Mã SKU</th>
                <th>Màu</th>
                <th>Size</th>
                <th>Giá</th>
                <th>Tồn</th>
                <th>Chọn</th>
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
                      handleSelectColor(sku.colorCode, color?.image);
                      setSelectedSize(sku.size);
                    }}
                  >
                    <td className={`${tplStyles.skuCodeMono} num`}>{sku.skuCode}</td>
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
                        {isSelected ? "Đang chọn" : available ? "Chọn" : "Hết"}
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

  const isSportTelemetry = layout === "sport-telemetry";
  const realSpecs = useMemo(() => {
    const items: Array<{
      key: "fabric" | "gsm" | "fit" | "origin";
      label: string;
      value: string;
      isNum?: boolean;
    }> = [];
    const fabric = product.fabric?.trim();
    if (fabric) items.push({ key: "fabric", label: "Chất liệu", value: fabric });
    if (product.gsm > 0) {
      items.push({ key: "gsm", label: "Định lượng", value: `${product.gsm}gsm`, isNum: true });
    }
    const fit = product.fit?.trim();
    if (fit) items.push({ key: "fit", label: "Phom dáng", value: fit });
    const origin = product.origin?.trim();
    if (origin) items.push({ key: "origin", label: "Xuất xứ", value: origin });
    return items;
  }, [product.fabric, product.gsm, product.fit, product.origin]);

  const remainingSpecs = useMemo(
    () =>
      isSportTelemetry
        ? realSpecs.filter((item) => item.key !== "gsm" && item.key !== "fit")
        : realSpecs,
    [isSportTelemetry, realSpecs]
  );

  const storySpecs = showStoryBand ? remainingSpecs : [];
  const accordionSpecs = showStoryBand ? [] : remainingSpecs;
  const descriptionText = product.description?.trim() ?? "";
  const highlights = useMemo(
    () => (product.highlights ?? []).map((item) => item.trim()).filter(Boolean),
    [product.highlights]
  );
  const showSpecAccordion =
    Boolean(descriptionText) || accordionSpecs.length > 0 || highlights.length > 0;

  const renderAccordions = () => (
    <div className={tplStyles.pdpAccordions}>
      {showSpecAccordion && (
        <details className={tplStyles.pdpDetails} open={layout !== "one-page-drawer"}>
          <summary className={tplStyles.pdpSummary}>
            <span>Thông số & chất liệu</span>
            <span className={tplStyles.accordionPlus} aria-hidden="true" />
          </summary>
          <div className={tplStyles.pdpDetailsBody}>
            {descriptionText && <p className={styles.prosePara}>{descriptionText}</p>}
            {accordionSpecs.length > 0 && (
              <dl className={tplStyles.pdpSpecGrid}>
                {accordionSpecs.map((item) => (
                  <div key={item.key} className={tplStyles.pdpSpecItem}>
                    <dt>{item.label}</dt>
                    <dd className={item.isNum ? "num" : undefined}>{item.value}</dd>
                  </div>
                ))}
              </dl>
            )}
            {highlights.length > 0 && (
              <ul className={styles.pdpDetailList}>
                {highlights.map((item, idx) => (
                  <li key={idx}>{item}</li>
                ))}
              </ul>
            )}
          </div>
        </details>
      )}

      <details className={tplStyles.pdpDetails}>
        <summary className={tplStyles.pdpSummary}>
          <span>Đổi trả & giao hàng</span>
          <span className={tplStyles.accordionPlus} aria-hidden="true" />
        </summary>
        <div className={tplStyles.pdpDetailsBody}>
          <p className={styles.prosePara}>
            Đổi size tận nhà trong 45 ngày. Hỗ trợ COD, chuyển khoản VietQR hoặc Ví ZaloPay trực
            tiếp tại {brandName}.
          </p>

          {!previewMode && provinces.length > 0 && skuId && (
            <div style={{ marginTop: 12, marginBottom: 12 }}>
              <p className={shared.fieldLabel} style={{ marginBottom: 8 }}>
                Ước tính phí vận chuyển
              </p>
              <div className={shared.formGrid}>
                <div className={shared.field}>
                  <select
                    aria-label="Chọn Tỉnh/Thành phố ước tính phí ship"
                    className={shared.select}
                    value={shipProvinceCode}
                    onChange={(e) => setShipProvinceCode(e.target.value)}
                  >
                    <option value="">Chọn Tỉnh / Thành phố</option>
                    {provinces.map((p) => (
                      <option key={p.code} value={p.code}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className={shared.field}>
                  <select
                    aria-label="Chọn Phường/Xã ước tính phí ship"
                    className={shared.select}
                    value={shipWardCode}
                    onChange={(e) => setShipWardCode(e.target.value)}
                    disabled={!shipProvinceCode || loadingShipWards}
                  >
                    <option value="">
                      {loadingShipWards ? "Đang tải phường/xã..." : "Chọn Phường / Xã"}
                    </option>
                    {shipWards.map((w) => (
                      <option key={w.code} value={w.code}>
                        {w.name}
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              {loadingShipQuote && (
                <p className={tplStyles.pdpMutedNote} style={{ marginTop: 8 }}>
                  Đang tính phí giao hàng...
                </p>
              )}
              {shipQuoteError && (
                <p className={shared.fieldError} style={{ marginTop: 8 }} role="alert">
                  {shipQuoteError}
                </p>
              )}
              {!loadingShipQuote && shipQuotes.length > 0 && (
                <div style={{ marginTop: 10, display: "flex", flexDirection: "column", gap: 6 }}>
                  {shipQuotes.map((q) => (
                    <div
                      key={`${q.carrier_code}-${q.service_code}`}
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        gap: 12,
                        fontSize: 14,
                      }}
                    >
                      <span>
                        <strong>{formatCarrierLabel(q.carrier_code, q.carrier_name)}</strong> (
                        {formatServiceLabel(q.service_code, q.service_name)} · dự kiến{" "}
                        <span className="num">{q.estimated_days}</span> ngày)
                        {q.cod_supported === false && " · Không hỗ trợ COD"}
                      </span>
                      <strong className="num">{formatVnd(q.fee_vnd)}</strong>
                    </div>
                  ))}
                  {shipIsEstimated && (
                    <p className={tplStyles.pdpMutedNote}>
                      Mức phí tạm tính; phí chính xác được xác nhận ở bước thanh toán.
                    </p>
                  )}
                </div>
              )}
            </div>
          )}

          {!previewMode && (
            <Link href="/chinh-sach" className={styles.sectionLink}>
              Xem chính sách đầy đủ
            </Link>
          )}
        </div>
      </details>

      {!previewMode && (
        <details className={tplStyles.pdpDetails} open={(reviewsData?.review_count ?? 0) > 0}>
          <summary className={tplStyles.pdpSummary}>
            <span>
              Đánh giá khách hàng
              {(reviewsData?.review_count ?? 0) > 0 && (
                <span className="num">
                  {" "}
                  ({formatRatingFromX100(reviewsData!.rating_avg_x100)} ★ ·{" "}
                  {formatInt(reviewsData!.review_count)})
                </span>
              )}
            </span>
            <span className={tplStyles.accordionPlus} aria-hidden="true" />
          </summary>
          <div className={tplStyles.pdpDetailsBody}>
            <ProductReviewSummary productId={backendProductId} productName={product.name} />

            {reviewsData && reviewsData.review_count > 0 ? (
              <div style={{ marginTop: 14, display: "flex", flexDirection: "column", gap: 12 }}>
                <div className={shared.formActions}>
                  {[0, 5, 4, 3, 2, 1].map((star) => {
                    const active = reviewStarFilter === star;
                    return (
                      <button
                        key={star}
                        type="button"
                        aria-pressed={active}
                        className={`${shared.statusTag} ${
                          active ? shared.toneWarn : shared.toneNeutral
                        }`}
                        disabled={loadingReviews}
                        onClick={() => {
                          setReviewStarFilter(star);
                          setReviewPage(1);
                          void fetchReviewsPage(1, star);
                        }}
                      >
                        {star === 0 ? "Tất cả" : `${star} ★`}
                      </button>
                    );
                  })}
                </div>

                {loadingReviews ? (
                  <p className={tplStyles.pdpMutedNote}>Đang tải đánh giá...</p>
                ) : reviewsData.items.length === 0 ? (
                  <p className={tplStyles.pdpMutedNote}>Chưa có đánh giá ở mức sao này.</p>
                ) : (
                  <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
                    {reviewsData.items.map((rev) => (
                      <article
                        key={rev.id}
                        style={{
                          paddingTop: 10,
                          borderTop: "1px solid var(--border)",
                          display: "flex",
                          flexDirection: "column",
                          gap: 6,
                        }}
                      >
                        <div
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            gap: 8,
                            flexWrap: "wrap",
                            fontSize: 14,
                          }}
                        >
                          <strong>{rev.buyer_name || "Khách mua hàng"}</strong>
                          <span className="num">
                            {rev.rating}/5 ★
                            {rev.created_at ? ` · ${formatDateVi(rev.created_at)}` : ""}
                          </span>
                        </div>
                        {rev.sku_name && (
                          <span className={tplStyles.pdpMutedNote}>Phân loại: {rev.sku_name}</span>
                        )}
                        {rev.comment && <p className={styles.prosePara}>{rev.comment}</p>}
                        {Array.isArray(rev.image_urls) && rev.image_urls.length > 0 && (
                          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                            {rev.image_urls.map((rawUrl, idx) => {
                              const safe = parseSafeReviewImageUrl(rawUrl);
                              if (!safe) return null;
                              return (
                                <img
                                  key={safe}
                                  src={safe}
                                  alt={`Ảnh đánh giá ${idx + 1}`}
                                  width={64}
                                  height={64}
                                  referrerPolicy="no-referrer"
                                  loading="lazy"
                                  style={{
                                    width: 64,
                                    height: 64,
                                    objectFit: "cover",
                                    borderRadius: 4,
                                    border: "1px solid var(--border)",
                                  }}
                                />
                              );
                            })}
                          </div>
                        )}
                      </article>
                    ))}
                  </div>
                )}

                {reviewsData.total > reviewsData.page_size && (
                  <div className={shared.formActions}>
                    <button
                      type="button"
                      className={shared.copyBtn}
                      disabled={loadingReviews || reviewPage <= 1}
                      onClick={() => {
                        const prev = Math.max(1, reviewPage - 1);
                        setReviewPage(prev);
                        void fetchReviewsPage(prev, reviewStarFilter);
                      }}
                    >
                      Trang trước
                    </button>
                    <span className={`${tplStyles.pdpMutedNote} num`}>
                      Trang {reviewPage}/{Math.ceil(reviewsData.total / reviewsData.page_size)}
                    </span>
                    <button
                      type="button"
                      className={shared.copyBtn}
                      disabled={
                        loadingReviews ||
                        reviewPage >= Math.ceil(reviewsData.total / reviewsData.page_size)
                      }
                      onClick={() => {
                        const next = reviewPage + 1;
                        setReviewPage(next);
                        void fetchReviewsPage(next, reviewStarFilter);
                      }}
                    >
                      Trang sau
                    </button>
                  </div>
                )}
              </div>
            ) : (
              <p className={tplStyles.pdpMutedNote}>
                Chưa có đánh giá nào. Đánh giá chỉ được gửi từ đơn hàng đã hoàn tất.
              </p>
            )}
          </div>
        </details>
      )}
    </div>
  );

  const renderBuyboxContent = () => (
    <>
      <div className={tplStyles.pdpTitleBlock}>
        <h1 className={styles.pdpTitle}>{product.name}</h1>

        <div className={styles.pdpPriceWrap}>
          <span className={`${styles.pdpPrice} num`}>{formatVnd(displayPrice)}</span>
          {hasDiscount && comparePrice ? (
            <>
              <span className={`${styles.productOriginalPrice} num`}>
                {formatVnd(comparePrice)}
              </span>
              <span className={`${tplStyles.pdpDiscountText} num`}>
                {formatDiscountPercent(comparePrice, displayPrice)}
              </span>
            </>
          ) : null}
          {effectivePromo.kind === "flash" && (
            <span className={`${shared.statusTag} ${shared.toneWarn}`}>Flash sale</span>
          )}
          {effectivePromo.kind === "shop_discount" && (
            <span className={`${shared.statusTag} ${shared.tonePos}`}>
              {effectivePromo.programName || "Giảm giá gian hàng"}
            </span>
          )}
        </div>
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
                  className={`${tplStyles.pdpColorBtn} ${
                    active ? tplStyles.pdpColorBtnActive : ""
                  }`}
                  aria-pressed={active}
                  onClick={() => handleSelectColor(color.code, color.image)}
                >
                  <span
                    className={tplStyles.pdpColorSwatch}
                    style={{ backgroundColor: color.hex }}
                    aria-hidden="true"
                  />
                  <span>{color.name}</span>
                </button>
              );
            })}
          </div>
        </div>
      )}

      {/* CRITICAL E2E INVARIANT: span "Kích cỡ: ..." and size buttons MUST share the same parent container */}
      {product.sizes.length > 0 && (
        <div className={styles.pdpOptionGroup}>
          <span className={styles.pdpOptionLabel}>Kích cỡ: {selectedSize}</span>
          <div className={tplStyles.pdpSizeGrid}>
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
                  aria-pressed={active}
                  className={`${tplStyles.pdpSizeTile} ${
                    active ? tplStyles.pdpSizeTileActive : ""
                  }`}
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
            : "Phân loại này tạm hết hàng"}
        </span>
      </div>

      {showVouchers && validShopVouchers.length > 0 && (
        <div className={styles.pdpShopVoucherBox}>
          <div className={tplStyles.pdpVoucherHead}>
            <span className={styles.pdpOptionLabel}>Mã ưu đãi {brandName}</span>
            {!previewMode && (
              <Link href="/voucher" className={styles.sectionLink}>
                Lưu mã
              </Link>
            )}
          </div>
          <div className={tplStyles.pdpVoucherPills}>
            {validShopVouchers.slice(0, 3).map((v) => (
              <span key={v.id || v.code} className={tplStyles.pdpVoucherChip}>
                <strong>{v.code}</strong>,{" "}
                {v.discountType === "percent"
                  ? `giảm ${v.discountValue}%`
                  : `giảm ${formatVnd(v.discountValue)}`}
                {v.minOrderValue > 0 ? ` (đơn từ ${formatVnd(v.minOrderValue)})` : ""}
              </span>
            ))}
          </div>
        </div>
      )}

      <div className={styles.pdpCtaStack}>
        {maxStock > 0 && (skuId || previewMode) ? (
          <>
            <button
              type="button"
              className={styles.btnPrimary}
              disabled={busyAction !== null}
              onClick={(e) => void handleCartAction("add", e.currentTarget)}
            >
              {busyAction === "add" ? "Đang thêm..." : "Thêm vào giỏ"}
            </button>
            <button
              type="button"
              className={styles.btnSecondary}
              disabled={busyAction !== null}
              onClick={() => void handleCartAction("buy")}
            >
              {busyAction === "buy"
                ? "Đang chuyển..."
                : `Mua ngay (${formatVnd(displayPrice * clampedQty)})`}
            </button>
          </>
        ) : (
          <button type="button" className={styles.btnPrimary} disabled>
            Tạm hết hàng
          </button>
        )}

        {addedNotice && !drawerOpen && (
          <p className={styles.pdpAddedNotice} role="status">
            <span>{addedNotice}</span>
            {!previewMode && (
              <Link href="/gio-hang" className={styles.sectionLink}>
                Mở giỏ hàng
              </Link>
            )}
          </p>
        )}
      </div>

      {renderAccordions()}
    </>
  );

  const useDenseTwoCol =
    layout === "dense-spec" || layout === "sku-order-matrix" || layout === "one-page-drawer";
  const relatedGridClass =
    relatedProducts.length <= 1
      ? tplStyles.gridSingle
      : relatedProducts.length === 2
        ? tplStyles.gridCols2
        : relatedProducts.length === 3
          ? tplStyles.gridCols3
          : tplStyles.gridCols4;

  const activeColorName = colorNameMap.get(selectedColor)?.name ?? selectedColor;
  const mobileVariantLabel = [activeColorName, selectedSize].filter(Boolean).join(", ");
  const drawerVariantLabel = [
    activeColorName,
    selectedSize ? `Size ${selectedSize}` : "",
    `SL ${clampedQty}`,
  ]
    .filter(Boolean)
    .join(", ");

  return (
    <div data-pdp-layout={layout} data-pdp-gallery={galleryStyle} className={tplStyles.pdpRoot}>
      <section className={styles.siteSection} style={{ paddingTop: 20 }}>
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
            <span className={styles.breadcrumbTailSep} aria-hidden="true">
              /
            </span>
            <span className={styles.breadcrumbCurrent}>{product.name}</span>
          </nav>

          <div className={useDenseTwoCol ? tplStyles.pdpDenseGrid : tplStyles.pdpEditorialGrid}>
            {renderGallery()}
            <div className={tplStyles.pdpStickyBuybox}>{renderBuyboxContent()}</div>
          </div>

          {renderSkuMatrixTable()}

          {storySpecs.length > 0 && (
            <div
              className={tplStyles.pdpStoryBand}
              data-cols={storySpecs.length}
              data-reveal="rows"
            >
              {storySpecs.map((item) => (
                <div key={item.key} className={tplStyles.pdpStoryCell}>
                  <span className={tplStyles.pdpStoryLabel}>{item.label}</span>
                  <span className={`${tplStyles.pdpStoryVal}${item.isNum ? " num" : ""}`}>
                    {item.value}
                  </span>
                </div>
              ))}
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
            <div className={styles.sectionHead}>
              <div className={styles.sectionHeadCopy}>
                <h2 className={styles.sectionTitle}>Sản phẩm cùng cửa hàng</h2>
              </div>
              {previewMode ? (
                <span className={styles.sectionLink}>Xem tất cả</span>
              ) : (
                <Link href="/san-pham" className={styles.sectionLink}>
                  Xem tất cả
                </Link>
              )}
            </div>
            <div className={relatedGridClass} data-reveal="rows">
              {relatedProducts.slice(0, 4).map((rel, idx) => (
                <SiteProductCard
                  key={rel.id}
                  product={rel}
                  index={idx}
                  previewMode={previewMode}
                  cardStyle={cardStyle}
                  layoutMode={relatedProducts.length === 1 ? "single-wide" : "default"}
                />
              ))}
            </div>
          </div>
        </section>
      )}

      {/* Mobile sticky buy bar (hidden on desktop; unified support dock lifts above it via :has) */}
      {!previewMode && (
        <div className={tplStyles.pdpMobileBuyBar} data-mobile-buybar="">
          <div className={tplStyles.pdpMobileBuyPrice}>
            <span className={`${tplStyles.pdpMobilePriceVal} num`}>{formatVnd(displayPrice)}</span>
            {mobileVariantLabel && (
              <span className={tplStyles.pdpMobileVariantVal}>{mobileVariantLabel}</span>
            )}
          </div>
          <div className={tplStyles.pdpMobileBuyActions}>
            <button
              type="button"
              className={tplStyles.pdpMobileBagBtn}
              disabled={maxStock <= 0 || !skuId || busyAction !== null}
              onClick={(e) => void handleCartAction("add", e.currentTarget)}
              aria-label="Cho vào giỏ"
            >
              <IconBag size={18} />
            </button>
            <button
              type="button"
              className={tplStyles.pdpMobilePrimaryBtn}
              disabled={maxStock <= 0 || !skuId || busyAction !== null}
              onClick={() => void handleCartAction("buy")}
            >
              {maxStock <= 0 ? "Hết hàng" : busyAction === "buy" ? "Đang chuyển..." : "Mua ngay"}
            </button>
          </div>
        </div>
      )}

      {/* Slide-right Add-to-Cart confirmation drawer */}
      {drawerOpen && (
        <div
          className={`${styles.siteDrawerBackdrop} ${
            drawerClosing ? styles.siteDrawerBackdropClosing : ""
          }`}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeDrawer(true);
          }}
        >
          <aside
            ref={cartDrawerRef}
            tabIndex={-1}
            className={`${tplStyles.cartConfirmDrawer} ${
              drawerClosing ? styles.siteDrawerPanelClosing : ""
            }`}
            role="dialog"
            aria-modal="true"
            aria-label="Đã thêm vào giỏ hàng"
          >
            <header className={tplStyles.cartConfirmHead}>
              <span className={tplStyles.cartConfirmStatus}>
                <IconCheck size={16} />
                <span>Đã thêm vào giỏ</span>
              </span>
              <button
                type="button"
                className={styles.siteDrawerClose}
                onClick={() => closeDrawer(true)}
                aria-label="Đóng thông báo giỏ hàng"
              >
                <IconClose size={18} />
              </button>
            </header>

            <div className={tplStyles.cartConfirmItem}>
              <img
                src={activeImage}
                alt={product.name}
                width={84}
                height={112}
                className={tplStyles.cartConfirmThumb}
              />
              <div className={tplStyles.cartConfirmMeta}>
                <p className={tplStyles.cartConfirmName}>{product.name}</p>
                <p className={tplStyles.cartConfirmVariant}>{drawerVariantLabel}</p>
                <p className={`${tplStyles.cartConfirmPrice} num`}>
                  {formatVnd(displayPrice * clampedQty)}
                </p>
              </div>
            </div>

            {addedNotice && (
              <p className={tplStyles.pdpMutedNote} role="status">
                {addedNotice}
              </p>
            )}

            <div className={tplStyles.cartConfirmActions}>
              <Link
                href="/gio-hang"
                className={styles.btnPrimary}
                onClick={() => closeDrawer(false)}
              >
                Mở giỏ hàng
              </Link>
              <button
                type="button"
                className={styles.btnSecondary}
                onClick={() => closeDrawer(true)}
              >
                Tiếp tục xem
              </button>
            </div>
          </aside>
        </div>
      )}
    </div>
  );
}
