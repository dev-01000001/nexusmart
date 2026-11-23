"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import type { SizeCode } from "@/data";
import type {
  GatewayActiveShopDiscount,
  GatewayProvince,
  GatewayVoucher,
} from "@/lib/gateway-types";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import {
  findProductSku,
  type CatalogFlashSaleItem,
  type CatalogProduct,
  type CatalogShop,
  type CatalogVoucher,
} from "@/lib/catalog-types";
import { useCart } from "@/context/CartContext";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import {
  BADGE_LABELS,
  FABRIC_FEATURE_LABELS,
  FIT_LABELS,
  GENDER_LABELS,
  MATERIAL_LABELS,
  OCCASION_LABELS,
  SPORT_LABELS,
} from "@/mock/productEditorial";
import {
  Button,
  IconAlert,
  IconChat,
  IconChevronDown,
  IconHeart,
  IconMinus,
  IconPlay,
  IconPlus,
  IconShield,
  IconStore,
  IconTruck,
  Price,
} from "@/components";
import { formatVnd } from "@/lib/format";
import { computePdpQuantityCap } from "@/features/checkout/helpers";
import { PROVINCES } from "@/lib/provinces";
import { estimateDelivery, recommendSize } from "@/lib/sizing";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import {
  PdpBackInStock,
  PdpGallery,
  PdpQuestions,
  PdpRecommendationsSection,
  PdpReviews,
  PdpSizeModal,
} from "@/features/product";
import styles from "./page.module.css";

export interface ProductDetailClientProps {
  product: CatalogProduct;
  shop: CatalogShop;
  shopVouchers: CatalogVoucher[];
  platformVouchers: CatalogVoucher[];
  sameShopProducts: CatalogProduct[];
  similarProducts: CatalogProduct[];
  allProducts: CatalogProduct[];
  liveFlashBySkuId: Record<string, CatalogFlashSaleItem>;
  activeShopDiscountBySkuId?: Record<string, GatewayActiveShopDiscount>;
  provinces: GatewayProvince[];
  initialColorQuery: string | null;
  initialSizeQuery: SizeCode | null;
}

function normalizeProvinceName(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/^(thanh pho|tinh)\s+/i, "")
    .replace(/\s+/g, " ")
    .trim();
}

function resolveInitialSize(
  product: CatalogProduct,
  colorCode: string,
  sizeQuery: SizeCode | null
): SizeCode {
  if (sizeQuery && product.sizes.includes(sizeQuery)) {
    return sizeQuery;
  }
  const preferredOrder: SizeCode[] = product.sizes.includes("L")
    ? ["L", "M", ...product.sizes]
    : [...product.sizes];
  for (const sz of preferredOrder) {
    if (!product.sizes.includes(sz)) continue;
    const sku = findProductSku(product, colorCode, sz);
    if (sku && sku.stock > 0) return sz;
  }
  return product.sizes[0] ?? "M";
}

function getCurrentReturnPath(fallbackPathname: string): string {
  if (typeof window !== "undefined") {
    return `${window.location.pathname}${window.location.search}`;
  }
  return fallbackPathname || "/";
}

export function ProductDetailClient({
  product,
  shop,
  shopVouchers,
  platformVouchers,
  sameShopProducts,
  similarProducts,
  allProducts,
  liveFlashBySkuId,
  activeShopDiscountBySkuId = {},
  provinces,
  initialColorQuery,
  initialSizeQuery,
}: ProductDetailClientProps) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const rootRef = useRef<HTMLDivElement>(null);
  const fabricVideoRef = useRef<HTMLVideoElement>(null);

  const { isAuthenticated } = useSession();
  const { addToCart } = useCart();
  const { openChat, showToast, toggleWishlist, isWishlisted, recordProductView, bodyProfile } =
    useMockStore();

  const defaultPromotedSku =
    !initialColorQuery && !initialSizeQuery
      ? (product.skus.find((s) => s.stock > 0 && Boolean(liveFlashBySkuId[s.id])) ??
        product.skus.find((s) => s.stock > 0 && Boolean(activeShopDiscountBySkuId[s.id])))
      : undefined;

  const initialColor =
    initialColorQuery && product.colors.some((c) => c.code === initialColorQuery)
      ? initialColorQuery
      : (defaultPromotedSku?.colorCode ?? product.colors[0]?.code ?? "");
  const initialSize =
    !initialSizeQuery &&
    defaultPromotedSku &&
    defaultPromotedSku.colorCode === initialColor &&
    product.sizes.includes(defaultPromotedSku.size)
      ? defaultPromotedSku.size
      : resolveInitialSize(product, initialColor, initialSizeQuery);
  const initialColorObj = product.colors.find((c) => c.code === initialColor) ?? product.colors[0];

  const [selectedColor, setSelectedColor] = useState<string>(initialColor);
  const [selectedSize, setSelectedSize] = useState<SizeCode>(initialSize);
  const [activeImage, setActiveImage] = useState<string>(initialColorObj?.image ?? product.image);
  const [quantity, setQuantity] = useState<number>(1);
  const [addingToCart, setAddingToCart] = useState<boolean>(false);
  const [copiedCode, setCopiedCode] = useState<string | null>(null);
  const [savingVoucherId, setSavingVoucherId] = useState<string | null>(null);
  const [savedVoucherIds, setSavedVoucherIds] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {};
    for (const v of [...shopVouchers, ...platformVouchers]) {
      if (v.saved) initial[v.id] = true;
    }
    return initial;
  });
  const [sizeModalState, setSizeModalState] = useState<{
    open: boolean;
    tab: "chart" | "finder";
  }>({ open: false, tab: "chart" });
  const [bisDrawerOpen, setBisDrawerOpen] = useState<boolean>(false);
  const [deliveryProvince, setDeliveryProvince] = useState<string>("HCM");
  const [fabricVideoPlaying, setFabricVideoPlaying] = useState<boolean>(true);

  useEffect(() => {
    recordProductView(product.id);
  }, [product.id, recordProductView]);

  useEffect(() => {
    const vid = fabricVideoRef.current;
    if (!vid) return;
    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) {
      vid.pause();
      return;
    }
    vid
      .play()
      .then(() => setFabricVideoPlaying(true))
      .catch(() => setFabricVideoPlaying(false));
  }, []);

  useRevealObserver(rootRef, product.id);

  // Map backend provinces from GET /shipping/provinces onto local province codes for ETA calculation
  const deliveryProvinceOptions = useMemo(() => {
    if (provinces.length === 0) {
      return PROVINCES.map((p) => ({ code: p.code, label: p.shortName }));
    }
    const localByNorm = new Map(PROVINCES.map((p) => [normalizeProvinceName(p.name), p]));
    const mapped: Array<{ code: string; label: string }> = [];
    const seenCodes = new Set<string>();
    for (const gwProv of provinces) {
      const matched = localByNorm.get(normalizeProvinceName(gwProv.name));
      const code = matched?.code ?? gwProv.code;
      if (seenCodes.has(code)) continue;
      seenCodes.add(code);
      mapped.push({ code, label: matched?.shortName ?? gwProv.name });
    }
    return mapped.length > 0
      ? mapped
      : PROVINCES.map((p) => ({ code: p.code, label: p.shortName }));
  }, [provinces]);

  const effectiveColor = product.colors.some((c) => c.code === selectedColor)
    ? selectedColor
    : (product.colors[0]?.code ?? "");
  const effectiveSize = product.sizes.includes(selectedSize)
    ? selectedSize
    : (product.sizes[0] ?? "M");
  const colorObj = product.colors.find((c) => c.code === effectiveColor) ?? product.colors[0];
  const activeSku = product.skus.find(
    (s) => s.colorCode === effectiveColor && s.size === effectiveSize
  );
  const skuStock = activeSku?.stock ?? 0;
  const activeFlashItem = activeSku ? liveFlashBySkuId[activeSku.id] : undefined;
  const activeShopDiscount =
    !activeFlashItem && activeSku ? activeShopDiscountBySkuId[activeSku.id] : undefined;
  const maxQty = computePdpQuantityCap({
    skuStock,
    productMaxPerOrder: product.maxPerOrder,
    liveFlash: activeFlashItem,
  });
  const clampedQty = skuStock > 0 ? Math.max(1, Math.min(quantity, maxQty)) : 0;
  const shopDiscountLimitExceeded = Boolean(
    activeShopDiscount &&
    activeShopDiscount.per_user_limit > 0 &&
    clampedQty > activeShopDiscount.per_user_limit
  );
  const regularPrice = activeSku?.regularPriceVnd ?? activeSku?.price ?? product.price;
  const unitPrice = activeFlashItem
    ? activeFlashItem.flashPriceVnd
    : activeShopDiscount &&
        !shopDiscountLimitExceeded &&
        activeShopDiscount.discounted_price_vnd < regularPrice
      ? activeShopDiscount.discounted_price_vnd
      : regularPrice;
  const displayedOriginalPrice = activeFlashItem
    ? Math.max(regularPrice, activeFlashItem.originalPriceVnd)
    : activeShopDiscount &&
        !shopDiscountLimitExceeded &&
        activeShopDiscount.discounted_price_vnd < regularPrice
      ? Math.max(regularPrice, activeShopDiscount.original_price_vnd)
      : undefined;

  const effectiveSubtotal = unitPrice * Math.max(1, clampedQty);

  const wishlisted = isWishlisted(product.id);

  const isOutlet = Boolean(product.badges?.includes("outlet"));
  const hasOutOfStockVariant = product.skus.some((s) => s.stock <= 0);

  const deliveryEstimate = useMemo(
    () =>
      estimateDelivery({
        shopId: shop.editorialShopId ?? shop.id,
        provinceCode: deliveryProvince,
        orderSubtotal: effectiveSubtotal,
      }),
    [shop.editorialShopId, shop.id, deliveryProvince, effectiveSubtotal]
  );

  const garmentGroup = useMemo(() => {
    if (product.shopSizeChart) return product.shopSizeChart;
    if (product.categorySlug === "ao-thun" || product.categorySlug === "ao-polo") return "top";
    if (product.categorySlug === "quan-jogger") return "jogger";
    if (product.categorySlug === "do-lot") return "underwear";
    if (
      product.categorySlug === "tat-phu-kien" &&
      product.slug !== "ao-khoac-gio-nhe-gap-gon-xam-tro"
    ) {
      return "sock";
    }
    return product.slug === "ao-khoac-gio-nhe-gap-gon-xam-tro" ? "top" : "bottom";
  }, [product]);

  const quickSizeHint = useMemo(
    () =>
      recommendSize({
        heightCm: bodyProfile.heightCm,
        weightKg: bodyProfile.weightKg,
        shape: bodyProfile.shape,
        preferredFit: bodyProfile.preferredFit,
        garmentGroup,
        shopId: shop.editorialShopId,
        availableSizes: product.sizes.length > 0 ? product.sizes : undefined,
      }),
    [bodyProfile, garmentGroup, shop.editorialShopId, product.sizes]
  );

  const syncColorUrl = (colorCode: string) => {
    const nextParams = new URLSearchParams(searchParams.toString());
    nextParams.set("color", colorCode);
    nextParams.delete("state");
    router.replace(`/san-pham/${product.slug}?${nextParams.toString()}`, { scroll: false });
  };

  const handleSelectColor = (code: string) => {
    setSelectedColor(code);
    const col = product.colors.find((c) => c.code === code);
    if (col) setActiveImage(col.image);
    const nextSku = findProductSku(product, code, effectiveSize);
    if (nextSku && nextSku.stock > 0) {
      const cap = Math.min(nextSku.stock, product.maxPerOrder ?? nextSku.stock);
      setQuantity((q) => Math.max(1, Math.min(q, cap)));
    }
    syncColorUrl(code);
  };

  const handleSelectImage = (imgSrc: string) => {
    setActiveImage(imgSrc);
    const matchedColor = product.colors.find((c) => c.image === imgSrc);
    if (matchedColor && matchedColor.code !== effectiveColor) {
      setSelectedColor(matchedColor.code);
      syncColorUrl(matchedColor.code);
    }
  };

  const handleSelectSize = (sz: SizeCode) => {
    setSelectedSize(sz);
    const nextSku = findProductSku(product, effectiveColor, sz);
    if (nextSku && nextSku.stock > 0) {
      const cap = Math.min(nextSku.stock, product.maxPerOrder ?? nextSku.stock);
      setQuantity((q) => Math.max(1, Math.min(q, cap)));
    }
  };

  const handleAddToCart = async () => {
    if (!activeSku || skuStock <= 0 || addingToCart) return;

    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
      return;
    }

    setAddingToCart(true);
    try {
      const res = await addToCart({ skuId: activeSku.id, quantity: clampedQty });
      if (res.ok) {
        if (res.clampedToStock) {
          showToast(res.message ?? "Số lượng trong giỏ đã được điều chỉnh theo tồn kho hiện còn.");
        } else {
          showToast(
            res.message ?? `Đã thêm ${product.shortName} (Size ${effectiveSize}) vào giỏ hàng.`
          );
        }
      } else {
        showToast(res.message ?? "Không thể thêm sản phẩm vào giỏ hàng lúc này.");
      }
    } finally {
      setAddingToCart(false);
    }
  };

  const handleBuyNow = () => {
    if (!activeSku || skuStock <= 0) return;
    const qs = new URLSearchParams({
      sku: activeSku.id,
      qty: String(clampedQty),
    });
    if (activeFlashItem && activeFlashItem.remainingSlots > 0) {
      qs.set("flash", activeFlashItem.id);
    }
    const checkoutUrl = `/thanh-toan?${qs.toString()}`;
    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(checkoutUrl)}`);
      return;
    }
    router.push(checkoutUrl);
  };

  const handleCopyVoucher = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    setCopiedCode(code);
    showToast(`Đã sao chép mã ${code}.`);
  };

  const handleSaveVoucher = async (voucher: CatalogVoucher) => {
    if (savingVoucherId === voucher.id || savedVoucherIds[voucher.id]) return;

    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
      return;
    }

    setSavingVoucherId(voucher.id);
    try {
      await api<{ saved: boolean; voucher: GatewayVoucher }>(
        `vouchers/${encodeURIComponent(voucher.id)}/save`,
        {
          method: "POST",
          json: { code: voucher.code },
        }
      );
      setSavedVoucherIds((prev) => ({ ...prev, [voucher.id]: true }));
      showToast(`Đã lưu mã ${voucher.code} vào ví voucher.`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
        return;
      }
      showToast(getErrorMessage(err, "Không thể lưu mã ưu đãi lúc này."));
    } finally {
      setSavingVoucherId(null);
    }
  };

  const handleToggleFabricVideo = () => {
    const vid = fabricVideoRef.current;
    if (!vid) return;
    if (vid.paused) {
      vid
        .play()
        .then(() => setFabricVideoPlaying(true))
        .catch(() => setFabricVideoPlaying(false));
    } else {
      vid.pause();
      setFabricVideoPlaying(false);
    }
  };

  const isTopChart = garmentGroup === "top";
  const isSockChart = garmentGroup === "sock";
  const shopTopRows = isTopChart ? shop.sizeCharts?.top : undefined;
  const hasSizeChartRows = Boolean(
    (shopTopRows && shopTopRows.filter((r) => product.sizes.includes(r.size)).length > 0) ||
    product.sizeChart.length > 0
  );

  const specItems = [
    product.fabric,
    product.gsm > 0 ? `${product.gsm}gsm` : "",
    product.fit,
  ].filter(Boolean);

  const chatShopId = shop.editorialShopId ?? shop.id;

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <Link href="/danh-muc" className={styles.crumbLink}>
          Danh mục
        </Link>
        <span aria-hidden="true">/</span>
        <Link href={`/danh-muc?cat=${product.rawCategorySlug}`} className={styles.crumbLink}>
          {product.categoryName}
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>{product.shortName}</span>
      </nav>

      <section className={styles.mainGrid} aria-label="Thông tin mua sản phẩm">
        <div className={styles.mediaCol}>
          <PdpGallery
            product={product}
            activeImage={activeImage}
            onSelectImage={handleSelectImage}
          />
        </div>

        <div className={styles.infoCol}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {product.name}
              </span>
            </span>
          </h1>

          <div
            className={styles.priceBadgeRow}
            data-rise=""
            style={{ "--i": 1 } as React.CSSProperties}
          >
            <Price
              amount={unitPrice}
              originalAmount={displayedOriginalPrice}
              size="lg"
              showDiscount={true}
            />
            {((product.badges && product.badges.length > 0) ||
              product.isFlashSale ||
              Boolean(activeFlashItem) ||
              Boolean(activeShopDiscount)) && (
              <div className={styles.badgeGroup} aria-label="Nhãn sản phẩm">
                {product.badges?.map((b) => (
                  <span
                    key={b}
                    className={`${styles.badgeTag} ${
                      b === "outlet"
                        ? styles.badgeOutlet
                        : b === "pre-order"
                          ? styles.badgePreorder
                          : ""
                    }`}
                  >
                    {BADGE_LABELS[b]}
                  </span>
                ))}
                {(product.isFlashSale || activeFlashItem) && (
                  <span className={styles.badgeTag}>
                    {activeFlashItem
                      ? `Flash sale · Còn ${activeFlashItem.remainingSlots} suất`
                      : "Flash sale"}
                  </span>
                )}
                {!activeFlashItem && activeShopDiscount && (
                  <span className={styles.badgeTag}>
                    {activeShopDiscount.program_name || "Giảm giá gian hàng"}
                    {activeShopDiscount.discount_percent > 0
                      ? ` -${activeShopDiscount.discount_percent}%`
                      : ""}
                  </span>
                )}
              </div>
            )}
          </div>

          {specItems.length > 0 && (
            <p
              className={styles.specStrip}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              {specItems.map((part, idx) => (
                <React.Fragment key={part}>
                  {idx > 0 && <span aria-hidden="true">·</span>}
                  <span>{part}</span>
                </React.Fragment>
              ))}
            </p>
          )}

          {product.colors.length > 0 && (
            <div className={styles.selectorBlock}>
              <div className={styles.selectorLabel}>
                <span>Màu sắc</span>
                <span className={styles.selectorSelected}>{colorObj?.name ?? "Mặc định"}</span>
              </div>
              <div className={styles.colorRow} role="group" aria-label="Chọn màu sắc">
                {product.colors.map((c) => {
                  const active = c.code === effectiveColor;
                  return (
                    <button
                      key={c.code}
                      type="button"
                      className={`${styles.colorBtn} ${active ? styles.colorBtnActive : ""}`}
                      onClick={() => handleSelectColor(c.code)}
                      aria-pressed={active}
                    >
                      <span
                        className={styles.swatch}
                        style={{ backgroundColor: c.hex }}
                        aria-hidden="true"
                      />
                      <span>{c.name}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}

          {product.sizes.length > 0 && (
            <div className={styles.selectorBlock}>
              <div className={styles.selectorLabel}>
                <span>
                  Kích cỡ: <strong className={styles.selectorSelected}>Size {effectiveSize}</strong>
                </span>
                <div className={styles.sizeModalTriggers}>
                  <button
                    type="button"
                    className={styles.sizeLinkBtn}
                    onClick={() => setSizeModalState({ open: true, tab: "chart" })}
                  >
                    Bảng size
                  </button>
                  <span aria-hidden="true">·</span>
                  <button
                    type="button"
                    className={styles.sizeLinkBtn}
                    onClick={() => setSizeModalState({ open: true, tab: "finder" })}
                  >
                    Gợi ý size ({quickSizeHint.size})
                  </button>
                </div>
              </div>

              <div className={styles.sizeRow} role="group" aria-label="Chọn kích cỡ">
                {product.sizes.map((sz) => {
                  const sku = findProductSku(product, effectiveColor, sz);
                  const stock = sku?.stock ?? 0;
                  const active = sz === effectiveSize;
                  const out = stock <= 0;
                  return (
                    <button
                      key={sz}
                      type="button"
                      className={`${styles.sizeBtn} ${active ? styles.sizeBtnActive : ""} ${out ? styles.sizeBtnOut : ""}`}
                      onClick={() => handleSelectSize(sz)}
                      aria-pressed={active}
                      aria-label={
                        out ? `Size ${sz}, hết hàng` : `Size ${sz}, còn ${stock} sản phẩm`
                      }
                    >
                      <span className={styles.sizeCode}>{sz}</span>
                      <span className={styles.sizeQty}>
                        {stock > 0 ? `${stock} sp` : "Hết hàng"}
                      </span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}

          {product.modelInfo && product.modelInfo.length > 0 && (
            <div className={styles.modelBox} aria-label="Thông tin người mẫu mặc">
              {product.modelInfo.map((m, idx) => (
                <p key={`${m.gender}-${idx}`} className={styles.modelLine}>
                  <strong>{m.gender === "nam" ? "Mẫu nam" : "Mẫu nữ"}:</strong> {m.heightCm} cm ·{" "}
                  {m.weightKg} kg · {m.measurements} · <strong>Mặc size {m.wearingSize}</strong>
                  {m.note ? ` (${m.note})` : ""}
                </p>
              ))}
            </div>
          )}

          <div className={styles.skuPanel} aria-live="polite">
            <div className={styles.skuLeft}>
              <span className={styles.skuCode}>
                Mã mẫu: {activeSku?.skuCode ?? "Đang cập nhật"}
              </span>
              {activeFlashItem ? (
                <span className={styles.skuCap}>
                  {activeFlashItem.perUserLimit > 0
                    ? `Giới hạn ${activeFlashItem.perUserLimit} suất Flash sale/tài khoản`
                    : `Flash sale (còn ${activeFlashItem.remainingSlots} suất)`}
                </span>
              ) : activeShopDiscount && activeShopDiscount.per_user_limit > 0 ? (
                <span className={styles.skuCap}>
                  Ưu đãi tối đa {activeShopDiscount.per_user_limit} SP/tài khoản (vượt giới hạn tính
                  giá thường)
                </span>
              ) : product.maxPerOrder && skuStock > 0 ? (
                <span className={styles.skuCap}>Giới hạn {product.maxPerOrder} sp/đơn</span>
              ) : null}
            </div>
            <div className={styles.skuRight}>
              {skuStock > 0 ? (
                <span className={styles.skuStock}>
                  Còn {skuStock} chiếc tại kho {shop.city}
                </span>
              ) : (
                <span className={styles.skuStockEmpty}>Tạm hết hàng</span>
              )}
              {skuStock > 0 && hasOutOfStockVariant && (
                <button
                  type="button"
                  className={styles.bisToggleBtn}
                  onClick={() => setBisDrawerOpen((v) => !v)}
                >
                  {bisDrawerOpen ? "Ẩn báo có hàng" : "Báo tôi khi có hàng"}
                </button>
              )}
            </div>
          </div>

          <div className={styles.buyRow}>
            <div className={styles.qtyWishlistRow}>
              <div className={styles.qtyStepper} aria-label="Chọn số lượng">
                <button
                  type="button"
                  className={styles.qtyBtn}
                  disabled={clampedQty <= 1 || skuStock <= 0}
                  onClick={() => setQuantity((q) => Math.max(1, q - 1))}
                  aria-label="Giảm số lượng"
                >
                  <IconMinus size={15} />
                </button>
                <span className={styles.qtyValue}>{skuStock > 0 ? clampedQty : 0}</span>
                <button
                  type="button"
                  className={styles.qtyBtn}
                  disabled={clampedQty >= maxQty || skuStock <= 0}
                  onClick={() => setQuantity((q) => Math.min(maxQty, q + 1))}
                  aria-label="Tăng số lượng"
                >
                  <IconPlus size={15} />
                </button>
              </div>

              <button
                type="button"
                className={`${styles.wishlistBtn} ${wishlisted ? styles.wishlistBtnActive : ""}`}
                onClick={() =>
                  toggleWishlist(product.id, activeSku?.skuCode, {
                    name: product.name,
                    slug: product.slug,
                    price: unitPrice,
                    originalPrice: displayedOriginalPrice,
                    image: product.image,
                    shopName: shop.shortName || shop.name,
                  })
                }
                aria-pressed={wishlisted}
                aria-label={
                  wishlisted
                    ? `Bỏ ${product.shortName} khỏi yêu thích`
                    : `Lưu ${product.shortName} vào yêu thích`
                }
              >
                <IconHeart size={16} filled={wishlisted} />
                <span>{wishlisted ? "Đã lưu" : "Yêu thích"}</span>
              </button>
            </div>

            <div className={styles.buyActions}>
              <Button
                variant="primary"
                size="lg"
                disabled={skuStock <= 0 || !activeSku || addingToCart}
                onClick={() => void handleAddToCart()}
                fullWidth
              >
                {skuStock <= 0 ? "Tạm hết hàng" : addingToCart ? "Đang thêm..." : "Thêm vào giỏ"}
              </Button>
              <Button
                variant="ink"
                size="lg"
                disabled={skuStock <= 0 || !activeSku}
                onClick={handleBuyNow}
                fullWidth
              >
                {activeFlashItem ? "Mua ngay Flash sale" : "Mua ngay"}
              </Button>
            </div>
          </div>

          {shopDiscountLimitExceeded && activeShopDiscount && (
            <p className={styles.wishlistHint} role="status">
              Số lượng đang chọn ({clampedQty}) vượt giới hạn {activeShopDiscount.per_user_limit}{" "}
              SP/tài khoản của chương trình giảm giá gian hàng — toàn bộ dòng sẽ tính giá thường (
              {formatVnd(regularPrice)}/SP).
            </p>
          )}

          {wishlisted && (
            <p className={styles.wishlistHint}>
              Đã lưu vào danh sách yêu thích để theo dõi giảm giá và tình trạng có hàng.{" "}
              <Link href="/tai-khoan" className={styles.inlineLink}>
                Xem trong tài khoản
              </Link>
            </p>
          )}

          <PdpBackInStock
            product={product}
            activeSku={activeSku}
            colorName={colorObj?.name ?? "Mặc định"}
            size={effectiveSize}
            isOpen={skuStock <= 0 || bisDrawerOpen}
            onClose={() => setBisDrawerOpen(false)}
          />

          {shopVouchers.length + platformVouchers.length > 0 && (
            <div
              className={styles.voucherBlock}
              data-cat-reveal=""
              style={{ "--stagger": 2 } as React.CSSProperties}
            >
              <div className={styles.voucherHeadRow}>
                <p className={styles.subBlockTitle}>Mã ưu đãi áp dụng</p>
              </div>

              {isOutlet && (
                <p className={styles.outletExclusionNote}>
                  <IconAlert size={15} />
                  <span>
                    Sản phẩm Outlet: không áp dụng voucher giảm phần trăm toàn sàn hoặc ưu đãi
                    chồng.
                  </span>
                </p>
              )}

              <ul className={styles.voucherList} aria-label="Danh sách mã giảm giá">
                {shopVouchers.map((v) => {
                  const isSaved = Boolean(savedVoucherIds[v.id]);
                  const isSaving = savingVoucherId === v.id;
                  const outOfQuota = v.remainingQuota <= 0;
                  const meetsMin = effectiveSubtotal >= v.minOrderVnd;
                  const missing = Math.max(0, v.minOrderVnd - effectiveSubtotal);
                  return (
                    <li key={v.id} className={styles.voucherRow}>
                      <div className={styles.voucherLead}>
                        <span
                          className={`${styles.voucherDot} ${styles.voucherDotShop}`}
                          aria-hidden="true"
                        />
                        <div className={styles.voucherInfo}>
                          <span className={styles.voucherName}>{v.name}</span>
                          <span className={styles.voucherMeta}>
                            {v.code} (Mã gian hàng):{" "}
                            {outOfQuota
                              ? "Đã hết lượt sử dụng"
                              : meetsMin
                                ? `Đủ điều kiện áp dụng, còn ${v.remainingQuota}/${v.quota} lượt`
                                : `Đơn từ ${formatVnd(v.minOrderVnd)} (cần thêm ${formatVnd(missing)}), còn ${v.remainingQuota}/${v.quota} lượt`}
                          </span>
                        </div>
                      </div>
                      <div className={styles.voucherActions}>
                        <button
                          type="button"
                          className={
                            outOfQuota
                              ? styles.voucherActionMuted
                              : isSaved
                                ? styles.voucherActionApplied
                                : styles.voucherActionPrimary
                          }
                          disabled={outOfQuota || isSaving || isSaved}
                          onClick={() => void handleSaveVoucher(v)}
                        >
                          {outOfQuota
                            ? "Hết lượt"
                            : isSaving
                              ? "Đang lưu..."
                              : isSaved
                                ? "Đã lưu"
                                : "Lưu mã"}
                        </button>
                        <button
                          type="button"
                          className={styles.voucherActionMuted}
                          onClick={() => handleCopyVoucher(v.code)}
                        >
                          {copiedCode === v.code ? "Đã chép" : "Sao chép"}
                        </button>
                      </div>
                    </li>
                  );
                })}

                {platformVouchers.map((v) => {
                  const isSaved = Boolean(savedVoucherIds[v.id]);
                  const isSaving = savingVoucherId === v.id;
                  const outOfQuota = v.remainingQuota <= 0;
                  const blockedByOutlet = Boolean(v.excludeOutlet && isOutlet);
                  const meetsMin = effectiveSubtotal >= v.minOrderVnd;
                  const missing = Math.max(0, v.minOrderVnd - effectiveSubtotal);
                  return (
                    <li key={v.id} className={styles.voucherRow}>
                      <div className={styles.voucherLead}>
                        <span
                          className={`${styles.voucherDot} ${
                            v.scope === "freeship"
                              ? styles.voucherDotShip
                              : styles.voucherDotPlatform
                          }`}
                          aria-hidden="true"
                        />
                        <div className={styles.voucherInfo}>
                          <span className={styles.voucherName}>{v.name}</span>
                          <span
                            className={blockedByOutlet ? styles.voucherBlocked : styles.voucherMeta}
                          >
                            {v.code} ({v.scope === "freeship" ? "Freeship sàn" : "Mã toàn sàn"}):{" "}
                            {blockedByOutlet
                              ? "Không áp dụng cho sản phẩm Outlet"
                              : outOfQuota
                                ? "Đã hết lượt sử dụng"
                                : meetsMin
                                  ? `Đủ điều kiện áp dụng, còn ${v.remainingQuota} lượt`
                                  : `Đơn từ ${formatVnd(v.minOrderVnd)} (cần thêm ${formatVnd(missing)}), còn ${v.remainingQuota} lượt`}
                          </span>
                        </div>
                      </div>
                      <div className={styles.voucherActions}>
                        <button
                          type="button"
                          className={
                            blockedByOutlet || outOfQuota
                              ? styles.voucherActionMuted
                              : isSaved
                                ? styles.voucherActionApplied
                                : styles.voucherActionPrimary
                          }
                          disabled={blockedByOutlet || outOfQuota || isSaving || isSaved}
                          onClick={() => void handleSaveVoucher(v)}
                        >
                          {blockedByOutlet
                            ? "Không áp"
                            : outOfQuota
                              ? "Hết lượt"
                              : isSaving
                                ? "Đang lưu..."
                                : isSaved
                                  ? "Đã lưu"
                                  : "Lưu mã"}
                        </button>
                        <button
                          type="button"
                          className={styles.voucherActionMuted}
                          onClick={() => handleCopyVoucher(v.code)}
                        >
                          {copiedCode === v.code ? "Đã chép" : "Sao chép"}
                        </button>
                      </div>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}

          <div
            className={styles.logisticsBlock}
            data-cat-reveal=""
            style={{ "--stagger": 3 } as React.CSSProperties}
          >
            <div className={styles.deliveryHead}>
              <span className={styles.deliveryTitle}>
                <IconTruck size={16} />
                <span>Ước tính giao hàng</span>
              </span>
              <label className={styles.provincePicker}>
                <span className="sr-only">Chọn tỉnh thành giao hàng</span>
                <select
                  value={deliveryProvince}
                  onChange={(e) => setDeliveryProvince(e.target.value)}
                  className={styles.provinceSelect}
                >
                  {deliveryProvinceOptions.map((prov) => (
                    <option key={prov.code} value={prov.code}>
                      {prov.label}
                    </option>
                  ))}
                </select>
              </label>
            </div>

            <div className={styles.deliveryDetails}>
              <p className={styles.deliveryLine}>
                Từ kho <strong>{shop.city}</strong> đến{" "}
                <strong>{deliveryEstimate.destinationProvinceName}</strong> · Dự kiến{" "}
                <strong>{deliveryEstimate.etaLabel}</strong>.
              </p>
              <p className={styles.deliveryLine}>
                Phí vận chuyển chính xác được tính theo kho gian hàng và địa chỉ nhận tại bước thanh
                toán; áp dụng mã miễn phí vận chuyển nếu có.
              </p>
              {product.slaText && (
                <p className={styles.deliverySub}>
                  Hạn chuẩn bị hàng: {product.slaText}. {deliveryEstimate.coInspectionNote}
                </p>
              )}
            </div>

            <div
              className={
                product.returnEligible === false ? styles.returnExcluded : styles.returnBox
              }
            >
              <div className={styles.returnTop}>
                <span className={styles.returnLabel}>
                  {product.returnEligible === false ? (
                    <IconAlert size={16} />
                  ) : (
                    <IconShield size={16} />
                  )}
                  <strong>
                    {product.returnEligible === false
                      ? "Không áp dụng đổi trả"
                      : "Đổi trả miễn phí 7 ngày"}
                  </strong>
                </span>
                <Link href="/tro-giup" className={styles.inlineLink}>
                  Xem chính sách
                </Link>
              </div>
              <p className={styles.returnDesc}>
                {product.returnEligible === false
                  ? (product.returnExclusionReason ??
                    "Sản phẩm thuộc nhóm ngoại lệ (Outlet hoặc pack niêm phong), chỉ hỗ trợ đổi khi có lỗi sản xuất.")
                  : `Shipper đổi size 2 chiều tận nơi, hoàn chuyển khoản định kỳ Thứ 2 và Thứ 5 (tối đa 3 lần/đơn).${
                      product.shopSizeChart === "sock" ? " Hỗ trợ bù lẻ 1 chiếc tất thất lạc." : ""
                    }`}
              </p>
            </div>
          </div>

          <div
            className={styles.shopCard}
            data-cat-reveal=""
            style={{ "--stagger": 4 } as React.CSSProperties}
          >
            <div className={styles.shopTop}>
              {shop.slug ? (
                <Link href={`/gian-hang/${shop.slug}`} className={styles.shopName}>
                  {shop.name}
                </Link>
              ) : (
                <span className={styles.shopName}>{shop.name}</span>
              )}
              <span className={styles.shopCity}>{shop.city}</span>
            </div>
            {shop.verifiedBadge && <p className={styles.shopBadge}>{shop.verifiedBadge}</p>}
            <p className={styles.shopMeta}>
              {shop.hasEditorialProfile
                ? `${shop.specialty}. Phản hồi ${shop.responseTime.toLowerCase()} · Tỷ lệ hoàn thành ${shop.fulfillmentRate}.`
                : shop.story}
            </p>
            <div className={styles.shopActions}>
              {shop.slug && (
                <Button variant="secondary" size="sm" href={`/gian-hang/${shop.slug}`}>
                  <IconStore size={15} />
                  <span>Xem gian hàng</span>
                </Button>
              )}
              <Button variant="primary" size="sm" onClick={() => openChat(chatShopId)}>
                <IconChat size={15} />
                <span>Chat với người bán</span>
              </Button>
            </div>
          </div>
        </div>
      </section>

      <section className={styles.detailsSection} aria-labelledby="pdp-specs-heading">
        <h2
          id="pdp-specs-heading"
          className={styles.sectionHeading}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Chất liệu và bảng quy đổi size
        </h2>

        <div className={styles.detailsGrid}>
          <div
            className={styles.specTextCol}
            data-cat-reveal=""
            style={{ "--stagger": 1 } as React.CSSProperties}
          >
            <p className={styles.descText}>
              {product.description || `${product.name} chính hãng từ ${shop.name}.`}
            </p>

            <div className={styles.detailGroup}>
              <h3 className={styles.detailGroupTitle}>Thông số sản phẩm</h3>
              <ul className={styles.plainList}>
                {product.fabric && (
                  <li>
                    <strong>Tỷ lệ sợi:</strong> {product.fabric}
                  </li>
                )}
                {product.gsm > 0 && (
                  <li>
                    <strong>Định lượng &amp; chất liệu:</strong> {product.gsm}gsm
                    {product.material
                      ? ` (${MATERIAL_LABELS[product.material]})`
                      : product.materialLabel
                        ? ` (${product.materialLabel})`
                        : ""}
                  </li>
                )}
                {product.fit && (
                  <li>
                    <strong>Phom dáng &amp; giới tính:</strong> {product.fit}
                    {product.fitType ? ` · ${FIT_LABELS[product.fitType]}` : ""} ·{" "}
                    {GENDER_LABELS[product.gender]}
                  </li>
                )}
                {product.occasion && product.occasion.length > 0 && (
                  <li>
                    <strong>Hoàn cảnh mặc:</strong>{" "}
                    {product.occasion.map((o) => OCCASION_LABELS[o]).join(", ")}
                  </li>
                )}
                {product.sport && product.sport.length > 0 && (
                  <li>
                    <strong>Môn vận động:</strong>{" "}
                    {product.sport.map((s) => SPORT_LABELS[s]).join(", ")}
                  </li>
                )}
                <li>
                  <strong>Gian hàng:</strong> {shop.name}
                  {product.origin && !product.origin.includes(shop.name)
                    ? ` (${product.origin})`
                    : ""}
                </li>
                {product.highlights.map((h) => (
                  <li key={h}>{h}</li>
                ))}
              </ul>
            </div>

            {product.fabricFeatures && product.fabricFeatures.length > 0 && (
              <div className={styles.detailGroup}>
                <div className={styles.featureHeadRow}>
                  <h3 className={styles.detailGroupTitle}>Tính năng công nghệ vải</h3>
                  <Link href="/chien-dich/cong-nghe-vai" className={styles.inlineLink}>
                    Xem từ điển công nghệ vải
                  </Link>
                </div>
                <div className={styles.featureTags}>
                  {product.fabricFeatures.map((feat) => (
                    <Link
                      key={feat}
                      href={`/danh-muc?feature=${feat}`}
                      className={styles.featureTag}
                    >
                      {FABRIC_FEATURE_LABELS[feat]}
                    </Link>
                  ))}
                  {product.material && (
                    <Link
                      href={`/danh-muc?material=${product.material}`}
                      className={styles.featureTag}
                    >
                      Lọc theo {MATERIAL_LABELS[product.material]}
                    </Link>
                  )}
                </div>
              </div>
            )}

            {((product.careInstructions && product.careInstructions.length > 0) ||
              product.careGuide.length > 0) && (
              <details className={styles.careDetails} open>
                <summary className={styles.careSummary}>
                  <span>Hướng dẫn bảo quản sợi vải</span>
                  <IconChevronDown size={16} className={styles.careChevron} />
                </summary>
                <div className={styles.careBody}>
                  {product.careInstructions && product.careInstructions.length > 0 ? (
                    <ul className={styles.careList}>
                      {product.careInstructions.map((item) => (
                        <li key={`${item.code}-${item.label}`} className={styles.careItem}>
                          <strong>{item.label}:</strong> {item.detail}
                        </li>
                      ))}
                    </ul>
                  ) : null}
                  {product.careGuide.length > 0 && (
                    <ul className={styles.plainList}>
                      {product.careGuide.map((tip) => (
                        <li key={tip}>{tip}</li>
                      ))}
                    </ul>
                  )}
                </div>
              </details>
            )}

            {product.hasVideo && product.videoUrl && (
              <figure className={styles.fabricVideoFigure}>
                <div className={styles.fabricVideoWrap}>
                  <video
                    ref={fabricVideoRef}
                    src={product.videoUrl}
                    poster={product.videoPoster}
                    muted
                    loop
                    playsInline
                    className={styles.fabricVideo}
                    aria-label={`Cận cảnh quy trình dệt vải cho ${product.shortName}`}
                  />
                  <button
                    type="button"
                    className={styles.fabricVideoToggle}
                    onClick={handleToggleFabricVideo}
                  >
                    <IconPlay size={14} />
                    <span>{fabricVideoPlaying ? "Tạm dừng" : "Phát video"}</span>
                  </button>
                </div>
                <figcaption className={styles.fabricVideoCaption}>
                  Cận cảnh khung dệt và cấu trúc sợi {product.gsm > 0 ? `${product.gsm}gsm ` : ""}
                  (tắt tiếng lặp mượt).
                </figcaption>
              </figure>
            )}
          </div>

          <div
            className={styles.sizeChartCol}
            data-cat-reveal=""
            style={{ "--stagger": 2 } as React.CSSProperties}
          >
            <div className={styles.chartHeadRow}>
              <h3 className={styles.detailGroupTitle}>
                {`Bảng thông số size ${shop.shortName} (cm / kg)`}
              </h3>
              <button
                type="button"
                className={styles.sizeLinkBtn}
                onClick={() => setSizeModalState({ open: true, tab: "finder" })}
              >
                Tính size theo số đo
              </button>
            </div>

            {shop.sizeCharts?.sizeNote && (
              <p className={styles.chartShopNote}>{shop.sizeCharts.sizeNote}</p>
            )}

            {hasSizeChartRows ? (
              <>
                <div className={styles.tableWrap}>
                  <table className={styles.sizeTable}>
                    <thead>
                      <tr>
                        <th scope="col">Size</th>
                        <th scope="col">{isSockChart ? "Cỡ giày" : "Cao"}</th>
                        <th scope="col">{isSockChart ? "Chân" : "Nặng"}</th>
                        <th scope="col">{isTopChart ? "Ngực" : isSockChart ? "Co giãn" : "Eo"}</th>
                        {shopTopRows && <th scope="col">Vai</th>}
                        <th scope="col">Dài</th>
                      </tr>
                    </thead>
                    <tbody>
                      {shopTopRows
                        ? shopTopRows
                            .filter((r) => product.sizes.includes(r.size))
                            .map((row, idx) => (
                              <tr
                                key={row.size}
                                className={
                                  row.size === effectiveSize ? styles.sizeRowActive : undefined
                                }
                                data-cat-reveal=""
                                style={{ "--stagger": idx } as React.CSSProperties}
                              >
                                <td>{row.size}</td>
                                <td>
                                  {row.heightMin}-{row.heightMax}
                                </td>
                                <td>
                                  {row.weightMin}-{row.weightMax}
                                </td>
                                <td>{row.chestOrWaistCm}</td>
                                <td>{row.shoulderCm ?? "-"}</td>
                                <td>{row.lengthCm}</td>
                              </tr>
                            ))
                        : product.sizeChart.map((row, idx) => (
                            <tr
                              key={row.size}
                              className={
                                row.size === effectiveSize ? styles.sizeRowActive : undefined
                              }
                              data-cat-reveal=""
                              style={{ "--stagger": idx } as React.CSSProperties}
                            >
                              <td>{row.size}</td>
                              <td>{row.heightCm}</td>
                              <td>{row.weightKg}</td>
                              <td>{row.chestOrWaistCm}</td>
                              <td>{row.lengthCm}</td>
                            </tr>
                          ))}
                    </tbody>
                  </table>
                </div>
                <p className={styles.chartNote}>
                  Sai số đo thực tế khoảng 1 đến 1,5 cm do co giãn tự nhiên của sợi dệt. Hàng đang
                  chọn (Size {effectiveSize}) được tô nền nổi bật trong bảng.
                </p>
              </>
            ) : (
              <p className={styles.chartNote}>
                Gian hàng chưa bổ sung bảng thông số chi tiết cho sản phẩm này. Bạn có thể bấm{" "}
                <strong>Tính size theo số đo</strong> để nhận gợi ý cỡ theo chiều cao và cân nặng.
              </p>
            )}
          </div>
        </div>
      </section>

      <PdpReviews
        product={product}
        shop={shop}
        selectedColorName={colorObj?.name ?? "Mặc định"}
        selectedSize={effectiveSize}
      />

      <PdpQuestions product={product} shop={shop} />

      <PdpRecommendationsSection
        product={product}
        shop={shop}
        sameShopProducts={sameShopProducts}
        similarProducts={similarProducts}
        allProducts={allProducts}
      />

      <PdpSizeModal
        open={sizeModalState.open}
        initialTab={sizeModalState.tab}
        onClose={() => setSizeModalState((prev) => ({ ...prev, open: false }))}
        product={product}
        shop={shop}
        selectedSize={effectiveSize}
        onSelectSize={(sz) => {
          handleSelectSize(sz);
          showToast(`Đã chọn Size ${sz} cho ${product.shortName}.`);
        }}
      />
    </div>
  );
}
