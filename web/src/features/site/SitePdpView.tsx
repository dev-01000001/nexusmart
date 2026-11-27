"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Minus, Plus, ShoppingBag, Star } from "lucide-react";
import type { FlashSaleItem } from "@/data/flash-sale";
import type { Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import type {
  GatewayActiveShopDiscount,
  GatewayCarrierQuote,
  GatewayProductReviewsResponse,
  GatewayProvince,
  GatewayWard,
} from "@/lib/gateway-types";
import { Money } from "@/components/app/Money";
import { useSession } from "@/components/SessionProvider";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useStore } from "@/context/StoreContext";
import { api } from "@/lib/api";
import { getFlashSaleSkuDetail, type SiteFlashSaleItem } from "@/lib/catalog-map";
import {
  formatCarrierLabel,
  formatDateVi,
  formatDiscountPercent,
  formatInt,
  formatRatingFromX100,
  formatVnd,
} from "@/lib/format";
import { buildDynamicOptionGroups, formatVariantLabel, getSkuOptionMap } from "@/lib/formatVariant";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { ProductReviewSummary } from "@/features/assistant/ProductReviewSummary";
import { resolveSkuEffectivePromotion } from "@/features/checkout/helpers";
import { formatServiceLabel } from "@/features/commerce/cartEnrich";
import { parseSafeReviewImageUrl } from "@/features/product/reviewHelpers";
import { useSitePathname } from "./SiteChrome";
import { SiteProductCard } from "./SiteProductCard";
import type { PdpPageConfig, SiteCardStyle, SiteTemplateConfig } from "./templateTypes";
import { sanitizeSafeImageUrl } from "./templateValidator";

const SiteRenderer = dynamic(() => import("./SiteRenderer").then((mod) => mod.SiteRenderer));

const CartConfirmSheet = dynamic(
  () => import("./catalog/CartConfirmSheet").then((mod) => mod.CartConfirmSheet),
  { ssr: false }
);

const PdpShippingSelects = dynamic(
  () => import("./catalog/PdpShippingSelects").then((mod) => mod.PdpShippingSelects),
  { ssr: false }
);

export interface SitePdpViewProps {
  brandName: string;
  product: Product;
  skuAttributesById?: Record<string, Record<string, string>>;
  shopVouchers: Voucher[];
  relatedProducts: Product[];
  liveFlashItem?: FlashSaleItem | SiteFlashSaleItem | null;
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
  skuAttributesById,
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

  const colorNameMap = useMemo(
    () => new Map(product.colors.map((c) => [c.code, c])),
    [product.colors]
  );

  const skuMetaList = useMemo(() => {
    return product.skus.map((sku) => {
      const color = colorNameMap.get(sku.colorCode);
      const hasById =
        Boolean(sku.skuId) &&
        Boolean(skuAttributesById) &&
        Object.prototype.hasOwnProperty.call(skuAttributesById, sku.skuId as string);
      const hasByCode =
        !hasById &&
        Boolean(sku.skuCode) &&
        Boolean(skuAttributesById) &&
        Object.prototype.hasOwnProperty.call(skuAttributesById, sku.skuCode);

      let attributes: Record<string, string>;
      if ((hasById || hasByCode) && skuAttributesById) {
        const raw = skuAttributesById[hasById ? (sku.skuId as string) : sku.skuCode] ?? {};
        const hasColorKey = Object.keys(raw).some((k) => {
          const kl = k.trim().toLowerCase();
          return kl === "color" || kl === "color_name";
        });
        const hasColorHex = Object.keys(raw).some((k) => k.trim().toLowerCase() === "color_hex");
        attributes =
          hasColorKey && !hasColorHex && color?.hex ? { ...raw, color_hex: color.hex } : raw;
      } else {
        attributes = {};
        if (color && color.code !== "mac-dinh" && color.name !== "Mặc định") {
          attributes.color = color.name;
          if (color.hex) attributes.color_hex = color.hex;
        }
        const skuSize = String(sku.size ?? "").trim();
        if (skuSize && skuSize !== "Mặc định") {
          attributes.size = skuSize;
        }
      }
      const image = sanitizeSafeImageUrl(color?.image);
      return {
        sku,
        attributes,
        optionMap: getSkuOptionMap(attributes),
        image,
        label: formatVariantLabel({ attributes, fallback: "Mặc định" }),
      };
    });
  }, [product.skus, colorNameMap, skuAttributesById]);

  const optionGroups = useMemo(() => buildDynamicOptionGroups(skuMetaList), [skuMetaList]);

  const defaultSkuMeta = useMemo(
    () => skuMetaList.find((m) => m.sku.stock > 0) ?? skuMetaList[0],
    [skuMetaList]
  );

  const [selectedOptions, setSelectedOptions] = useState<Record<string, string>>(
    () => defaultSkuMeta?.optionMap ?? {}
  );
  const [activeImage, setActiveImage] = useState<string>(gallery[0]);
  const [qty, setQty] = useState<number>(1);
  const [busyAction, setBusyAction] = useState<"add" | "buy" | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerMounted, setDrawerMounted] = useState(false);
  const [addedNotice, setAddedNotice] = useState<string | null>(null);

  const selectedSkuMeta = useMemo(() => {
    if (skuMetaList.length === 0) return undefined;
    if (optionGroups.length === 0) return defaultSkuMeta;
    const exactMatch = skuMetaList.find((m) =>
      optionGroups.every((g) => m.optionMap[g.key] === selectedOptions[g.key])
    );
    if (exactMatch) return exactMatch;
    const partialMatch = skuMetaList.find((m) =>
      optionGroups.some(
        (g) => Boolean(selectedOptions[g.key]) && m.optionMap[g.key] === selectedOptions[g.key]
      )
    );
    return partialMatch ?? defaultSkuMeta;
  }, [skuMetaList, optionGroups, selectedOptions, defaultSkuMeta]);

  const selectedSku = selectedSkuMeta?.sku;
  const effectiveSelectedOptions = selectedSkuMeta?.optionMap ?? selectedOptions;

  const maxStock = Math.max(0, selectedSku?.stock ?? 0);
  const clampedQty = maxStock > 0 ? Math.min(qty, maxStock) : 1;
  const skuId = selectedSku?.skuId || "";
  const skuFlashDetail = getFlashSaleSkuDetail(liveFlashItem, skuId);
  const skuPrice = selectedSku?.price ?? product.price;
  const activeShopDiscount = skuId ? (activeShopDiscountBySkuId[skuId] ?? null) : null;
  const effectivePromo = resolveSkuEffectivePromotion({
    regularPriceVnd: skuPrice,
    liveFlash: skuFlashDetail
      ? {
          flashSaleItemId: skuFlashDetail.id,
          flashPriceVnd: skuFlashDetail.flashPrice,
          remainingSlots: skuFlashDetail.remainingQuota,
          perUserLimit: skuFlashDetail.limitPerBuyer,
        }
      : null,
    activeShopDiscount,
  });
  const flashSaleItemId =
    effectivePromo.kind === "flash" ? effectivePromo.flashSaleItemId : undefined;
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
  const [shippingOpened, setShippingOpened] = useState<boolean>(false);

  // Real buyer reviews state (GET /api/products/[id]/reviews)
  const backendProductId = product.backendId || product.id;
  const [reviewsData, setReviewsData] = useState<GatewayProductReviewsResponse | null>(
    initialReviews
  );
  const [reviewStarFilter, setReviewStarFilter] = useState<number>(0);
  const [reviewPage, setReviewPage] = useState<number>(1);
  const [loadingReviews, setLoadingReviews] = useState<boolean>(false);

  const [openAccordions, setOpenAccordions] = useState<string[]>(() => {
    const initial: string[] = [];
    if (layout !== "one-page-drawer") initial.push("specs");
    if ((initialReviews?.review_count ?? 0) > 0) initial.push("reviews");
    return initial;
  });

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

  const handleSelectOption = (groupKey: string, value: string, valueImage?: string) => {
    const candidate = { ...effectiveSelectedOptions, [groupKey]: value };
    const exact =
      skuMetaList.find(
        (m) => m.sku.stock > 0 && optionGroups.every((g) => m.optionMap[g.key] === candidate[g.key])
      ) ??
      skuMetaList.find((m) => optionGroups.every((g) => m.optionMap[g.key] === candidate[g.key]));
    const fallback =
      exact ??
      skuMetaList.find((m) => m.sku.stock > 0 && m.optionMap[groupKey] === value) ??
      skuMetaList.find((m) => m.optionMap[groupKey] === value);
    setSelectedOptions(fallback ? fallback.optionMap : candidate);
    const nextImg = sanitizeSafeImageUrl(fallback?.image || valueImage);
    if (nextImg) {
      setActiveImage(nextImg);
    }
    setQty(1);
    setAddedNotice(null);
  };

  const selectedVariantLabel = useMemo(
    () =>
      selectedSkuMeta
        ? formatVariantLabel({ attributes: selectedSkuMeta.attributes, fallback: "" })
        : "",
    [selectedSkuMeta]
  );

  const handleCartAction = async (mode: "add" | "buy") => {
    if (previewMode) {
      setAddedNotice(
        mode === "buy"
          ? "Xem trước: nút Mua ngay sẽ đưa tới thanh toán khi xuất bản."
          : "Xem trước: sản phẩm sẽ được thêm vào giỏ khi xuất bản."
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
        size: selectedVariantLabel || undefined,
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
        setDrawerMounted(true);
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
    <div className={cn("relative w-full overflow-hidden rounded-lg bg-muted", frameClass)}>
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
            className={cn(
              "absolute inset-0 block size-full object-cover object-[center_16%] transition-opacity duration-300 ease-out",
              isCurrent ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0"
            )}
            loading={idx === 0 ? "eager" : "lazy"}
          />
        );
      })}
    </div>
  );

  const renderThumbStrip = (className?: string) =>
    gallery.length > 1 ? (
      <div className={cn("grid grid-cols-4 gap-2.5", className)}>
        {gallery.slice(0, 4).map((src, idx) => {
          const active = activeImage === src;
          return (
            <button
              key={src}
              type="button"
              className={cn(
                "relative aspect-[3/4] overflow-hidden rounded-md border bg-muted transition-all",
                active
                  ? "border-primary ring-2 ring-ring/40"
                  : "border-border opacity-80 hover:opacity-100"
              )}
              onClick={() => setActiveImage(src)}
              aria-label={`Xem ảnh ${idx + 1}`}
              aria-pressed={active}
            >
              <img
                src={src}
                alt=""
                width={240}
                height={320}
                className="block size-full object-cover object-[center_16%]"
              />
            </button>
          );
        })}
      </div>
    ) : null;

  const renderGallery = () => {
    const isDense =
      layout === "dense-spec" || layout === "sku-order-matrix" || galleryStyle === "compact";
    const isLuxury = layout === "luxury-framed" || galleryStyle === "framed";
    const isThumbnails = galleryStyle === "thumbnails" && layout !== "editorial-tall";

    if (isDense) {
      return (
        <div className="flex min-w-0 flex-col gap-3">
          {renderCrossfadeFrame("aspect-[4/5] max-h-[480px]")}
          {renderThumbStrip()}
        </div>
      );
    }

    if (isLuxury) {
      return (
        <div className="flex min-w-0 flex-col gap-3">
          <div className="rounded-lg border border-border bg-card p-3.5 md:p-6">
            {renderCrossfadeFrame("aspect-[3/4]")}
          </div>
          {renderThumbStrip()}
        </div>
      );
    }

    if (isThumbnails) {
      return (
        <div className="flex min-w-0 flex-col gap-3">
          {renderCrossfadeFrame("aspect-[3/4]")}
          {renderThumbStrip()}
        </div>
      );
    }

    // Stacked vertical scrolling photography gallery (lead crossfade frame + tall stacked secondary frames)
    return (
      <div className="flex min-w-0 flex-col gap-3.5">
        {renderCrossfadeFrame("aspect-[3/4]")}
        {renderThumbStrip("md:hidden")}

        {secondaryGallery.map((src, idx) => (
          <figure
            key={src}
            className="hidden aspect-[3/4] w-full overflow-hidden rounded-lg bg-muted md:block"
            data-reveal="clip"
          >
            <img
              src={src}
              alt={`${product.name} góc ${idx + 2}`}
              width={896}
              height={1200}
              className="block size-full object-cover object-[center_16%]"
              loading="lazy"
            />
          </figure>
        ))}
      </div>
    );
  };

  const renderSkuMatrixTable = () => {
    if (!showSkuTable && layout !== "sku-order-matrix") return null;
    if (skuMetaList.length === 0) return null;
    return (
      <section className="mt-8 flex flex-col gap-3" aria-labelledby="pdp-sku-matrix-heading">
        <h2
          id="pdp-sku-matrix-heading"
          className="text-sm font-semibold tracking-tight text-foreground"
        >
          Bảng biến thể SKU
        </h2>
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/50 hover:bg-muted/50">
                <TableHead>Mã SKU</TableHead>
                <TableHead>Phân loại</TableHead>
                <TableHead>Giá</TableHead>
                <TableHead>Tồn</TableHead>
                <TableHead>Chọn</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {skuMetaList.map((meta) => {
                const { sku } = meta;
                const isSelected = selectedSkuMeta?.sku === sku;
                const available = sku.stock > 0;
                return (
                  <TableRow
                    key={sku.skuId || sku.skuCode}
                    data-state={isSelected ? "selected" : undefined}
                    className={cn(available && "cursor-pointer")}
                    onClick={() => {
                      if (!available) return;
                      setSelectedOptions(meta.optionMap);
                      if (meta.image) setActiveImage(meta.image);
                      setQty(1);
                      setAddedNotice(null);
                    }}
                  >
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {sku.skuCode}
                    </TableCell>
                    <TableCell>{meta.label}</TableCell>
                    <TableCell>
                      <Money vnd={sku.price} size="sm" />
                    </TableCell>
                    <TableCell className="tabular-nums">{formatInt(sku.stock)}</TableCell>
                    <TableCell>
                      <Badge
                        variant={isSelected ? "default" : available ? "secondary" : "neutral"}
                        className="rounded-sm"
                      >
                        {isSelected ? "Đang chọn" : available ? "Chọn" : "Hết"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      </section>
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

  const handleAccordionChange = (values: string[]) => {
    setOpenAccordions(values);
    if (values.includes("shipping")) {
      setShippingOpened(true);
    }
  };

  const renderAccordions = () => (
    <Accordion
      type="multiple"
      value={openAccordions}
      onValueChange={handleAccordionChange}
      className="flex flex-col gap-2"
    >
      {showSpecAccordion && (
        <AccordionItem
          value="specs"
          className="rounded-lg border border-border bg-background px-4 last:border-b"
        >
          <AccordionTrigger className="min-h-12 py-3.5 text-[15px] font-semibold text-foreground hover:no-underline">
            Thông tin sản phẩm
          </AccordionTrigger>
          <AccordionContent className="space-y-3 pb-4 text-sm leading-6 text-foreground/90">
            {descriptionText && <p className="max-w-[66ch]">{descriptionText}</p>}
            {accordionSpecs.length > 0 && (
              <dl className="grid grid-cols-2 gap-x-4 gap-y-2.5">
                {accordionSpecs.map((item) => (
                  <div key={item.key} className="flex flex-col gap-0.5">
                    <dt className="text-xs text-muted-foreground">{item.label}</dt>
                    <dd
                      className={cn(
                        "text-sm font-semibold text-foreground",
                        item.isNum && "tabular-nums"
                      )}
                    >
                      {item.value}
                    </dd>
                  </div>
                ))}
              </dl>
            )}
            {highlights.length > 0 && (
              <ul className="list-disc space-y-1 pl-5 text-sm text-foreground/90">
                {highlights.map((item, idx) => (
                  <li key={idx}>{item}</li>
                ))}
              </ul>
            )}
          </AccordionContent>
        </AccordionItem>
      )}

      <AccordionItem
        value="shipping"
        className="rounded-lg border border-border bg-background px-4 last:border-b"
      >
        <AccordionTrigger className="min-h-12 py-3.5 text-[15px] font-semibold text-foreground hover:no-underline">
          Đổi trả &amp; giao hàng
        </AccordionTrigger>
        <AccordionContent className="space-y-3.5 pb-4 text-sm leading-6 text-foreground/90">
          <p className="max-w-[66ch]">
            Hỗ trợ đổi trả theo chính sách cửa hàng. Thanh toán linh hoạt qua COD, chuyển khoản
            VietQR hoặc Ví ZaloPay trực tiếp tại {brandName}.
          </p>

          {!previewMode && provinces.length > 0 && skuId && (
            <div className="space-y-2.5 rounded-lg border border-border bg-card p-3.5">
              <p className="text-[13px] font-semibold text-foreground">Ước tính phí vận chuyển</p>

              {shippingOpened && (
                <PdpShippingSelects
                  provinces={provinces}
                  wards={shipWards}
                  provinceCode={shipProvinceCode}
                  wardCode={shipWardCode}
                  loadingWards={loadingShipWards}
                  onProvinceChange={setShipProvinceCode}
                  onWardChange={setShipWardCode}
                />
              )}

              {loadingShipQuote && (
                <p className="text-[13px] text-muted-foreground">Đang tính phí giao hàng...</p>
              )}
              {shipQuoteError && (
                <p className="text-[13px] font-medium text-destructive" role="alert">
                  {shipQuoteError}
                </p>
              )}
              {!loadingShipQuote && shipQuotes.length > 0 && (
                <div className="flex flex-col gap-2 pt-1">
                  {shipQuotes.map((q) => (
                    <div
                      key={`${q.carrier_code}-${q.service_code}`}
                      className="flex flex-wrap items-baseline justify-between gap-2 text-sm"
                    >
                      <span className="text-foreground">
                        <strong>{formatCarrierLabel(q.carrier_code, q.carrier_name)}</strong> (
                        {formatServiceLabel(q.service_code, q.service_name)} · dự kiến{" "}
                        <span className="tabular-nums">{q.estimated_days}</span> ngày)
                        {q.cod_supported === false && " · Không hỗ trợ COD"}
                      </span>
                      <Money vnd={q.fee_vnd} size="sm" />
                    </div>
                  ))}
                  {shipIsEstimated && (
                    <p className="text-xs text-muted-foreground">
                      Mức phí tạm tính; phí chính xác được xác nhận ở bước thanh toán.
                    </p>
                  )}
                </div>
              )}
            </div>
          )}

          {!previewMode && (
            <Link
              href="/chinh-sach"
              className="inline-flex min-h-9 items-center text-sm font-semibold text-brand underline-offset-4 hover:underline"
            >
              Xem chính sách đầy đủ
            </Link>
          )}
        </AccordionContent>
      </AccordionItem>

      {!previewMode && (
        <AccordionItem
          value="reviews"
          className="rounded-lg border border-border bg-background px-4 last:border-b"
        >
          <AccordionTrigger className="min-h-12 py-3.5 text-[15px] font-semibold text-foreground hover:no-underline">
            <span>
              Đánh giá khách hàng
              {(reviewsData?.review_count ?? 0) > 0 && (
                <span className="font-normal text-muted-foreground tabular-nums">
                  {" "}
                  ({formatRatingFromX100(reviewsData!.rating_avg_x100)} ★ ·{" "}
                  {formatInt(reviewsData!.review_count)})
                </span>
              )}
            </span>
          </AccordionTrigger>
          <AccordionContent className="space-y-4 pb-4">
            <ProductReviewSummary productId={backendProductId} productName={product.name} />

            <Card className="gap-3.5 rounded-lg border border-border py-4 shadow-none">
              <CardContent className="space-y-4 px-4">
                {reviewsData && reviewsData.review_count > 0 ? (
                  <>
                    <div
                      className="flex flex-wrap items-center gap-1.5"
                      role="group"
                      aria-label="Lọc đánh giá theo số sao"
                    >
                      {[0, 5, 4, 3, 2, 1].map((star) => {
                        const active = reviewStarFilter === star;
                        return (
                          <Button
                            key={star}
                            type="button"
                            size="sm"
                            variant={active ? "default" : "outline"}
                            aria-pressed={active}
                            disabled={loadingReviews}
                            className="min-h-9 px-2.5 text-xs shadow-none"
                            onClick={() => {
                              setReviewStarFilter(star);
                              setReviewPage(1);
                              void fetchReviewsPage(1, star);
                            }}
                          >
                            {star === 0 ? "Tất cả" : `${star} ★`}
                          </Button>
                        );
                      })}
                    </div>

                    {loadingReviews ? (
                      <p className="text-[13px] text-muted-foreground">Đang tải đánh giá...</p>
                    ) : reviewsData.items.length === 0 ? (
                      <p className="text-[13px] text-muted-foreground">
                        Chưa có đánh giá ở mức sao này.
                      </p>
                    ) : (
                      <div className="divide-y divide-border">
                        {reviewsData.items.map((rev) => (
                          <article
                            key={rev.id}
                            className="flex flex-col gap-1.5 py-3 first:pt-0 last:pb-0"
                          >
                            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
                              <strong className="font-semibold text-foreground">
                                {rev.buyer_name || "Khách mua hàng"}
                              </strong>
                              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground tabular-nums">
                                <Star
                                  className="size-3.5 fill-current text-brand"
                                  aria-hidden="true"
                                />
                                <span>{rev.rating}/5</span>
                                {rev.created_at ? ` · ${formatDateVi(rev.created_at)}` : ""}
                              </span>
                            </div>
                            {rev.sku_name && (
                              <span className="text-xs text-muted-foreground">
                                Phân loại: {rev.sku_name}
                              </span>
                            )}
                            {rev.comment && (
                              <p className="text-sm leading-6 text-foreground/90">{rev.comment}</p>
                            )}
                            {Array.isArray(rev.image_urls) && rev.image_urls.length > 0 && (
                              <div className="flex flex-wrap gap-2 pt-1">
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
                                      className="size-16 rounded-md border border-border object-cover"
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
                      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          className="min-h-9 shadow-none"
                          disabled={loadingReviews || reviewPage <= 1}
                          onClick={() => {
                            const prev = Math.max(1, reviewPage - 1);
                            setReviewPage(prev);
                            void fetchReviewsPage(prev, reviewStarFilter);
                          }}
                        >
                          Trang trước
                        </Button>
                        <span className="text-xs text-muted-foreground tabular-nums">
                          Trang {reviewPage}/{Math.ceil(reviewsData.total / reviewsData.page_size)}
                        </span>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          className="min-h-9 shadow-none"
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
                        </Button>
                      </div>
                    )}
                  </>
                ) : (
                  <p className="text-[13px] text-muted-foreground">
                    Chưa có đánh giá nào. Đánh giá chỉ được gửi từ đơn hàng đã hoàn tất.
                  </p>
                )}
              </CardContent>
            </Card>
          </AccordionContent>
        </AccordionItem>
      )}
    </Accordion>
  );

  const renderBuyboxContent = () => (
    <>
      <div className="flex flex-col gap-2.5">
        <h1
          className={cn(
            "text-[clamp(24px,2.8vw,34px)] leading-[1.14] tracking-[-0.02em] text-foreground",
            layout === "luxury-framed" ? "font-normal tracking-[0.015em]" : "font-semibold"
          )}
        >
          {product.name}
        </h1>

        <div className="flex flex-wrap items-center gap-2.5">
          <Money
            vnd={displayPrice}
            compareAtVnd={hasDiscount ? comparePrice : undefined}
            size="lg"
          />
          {hasDiscount && comparePrice ? (
            <Badge variant="default" className="rounded-sm tabular-nums">
              {formatDiscountPercent(comparePrice, displayPrice)}
            </Badge>
          ) : null}
          {effectivePromo.kind === "flash" && (
            <Badge variant="warning" className="rounded-sm tabular-nums">
              {skuFlashDetail && skuFlashDetail.totalQuota > 0
                ? `Flash sale · Còn ${formatInt(skuFlashDetail.remainingQuota)}/${formatInt(skuFlashDetail.totalQuota)} suất`
                : "Flash sale"}
            </Badge>
          )}
          {effectivePromo.kind === "shop_discount" && (
            <Badge variant="success" className="rounded-sm">
              {effectivePromo.programName || "Giảm giá gian hàng"}
            </Badge>
          )}
        </div>
      </div>

      {layout === "sport-telemetry" && (
        <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
          {product.gsm > 0 && (
            <div className="flex flex-col gap-1 rounded-lg border border-border bg-background px-3.5 py-3">
              <span className="text-xs text-muted-foreground">Định lượng</span>
              <span className="text-[15px] font-semibold text-foreground tabular-nums">
                {`${product.gsm}gsm`}
              </span>
            </div>
          )}
          {product.fit?.trim() && (
            <div className="flex flex-col gap-1 rounded-lg border border-border bg-background px-3.5 py-3">
              <span className="text-xs text-muted-foreground">Phom dáng</span>
              <span className="text-[15px] font-semibold text-foreground">{product.fit}</span>
            </div>
          )}
          <div className="flex flex-col gap-1 rounded-lg border border-border bg-background px-3.5 py-3">
            <span className="text-xs text-muted-foreground">Sẵn kho</span>
            <span className="text-[15px] font-semibold text-foreground tabular-nums">
              {formatInt(maxStock)}
            </span>
          </div>
        </div>
      )}

      {/* CRITICAL E2E INVARIANT: option label span ("<Label>: <value>") and ToggleGroup buttons share the same parent container */}
      {optionGroups.map((group, groupIdx) => {
        const currentValue = effectiveSelectedOptions[group.key] ?? group.values[0]?.value ?? "";
        const isPrimaryVisual = group.isColorSwatch;
        return (
          <div key={group.key} className="flex flex-col gap-2">
            <span className="text-[13px] font-semibold text-muted-foreground">
              {group.label}: <span className="text-foreground">{currentValue}</span>
            </span>
            <ToggleGroup
              type="single"
              variant="outline"
              spacing={2}
              value={currentValue}
              onValueChange={(nextVal) => {
                if (!nextVal) return;
                const matchedOpt = group.values.find((v) => v.value === nextVal);
                handleSelectOption(group.key, nextVal, matchedOpt?.image);
              }}
              className="flex w-full flex-wrap justify-start gap-2"
              aria-label={`Chọn ${group.label.toLowerCase()}`}
            >
              {group.values.map((opt) => {
                const available =
                  groupIdx === 0 && (group.isColorSwatch || optionGroups.length > 1)
                    ? true
                    : skuMetaList.some(
                        (m) =>
                          m.sku.stock > 0 &&
                          m.optionMap[group.key] === opt.value &&
                          optionGroups
                            .slice(0, groupIdx)
                            .every(
                              (prev) => m.optionMap[prev.key] === effectiveSelectedOptions[prev.key]
                            )
                      );
                return (
                  <ToggleGroupItem
                    key={opt.value}
                    value={opt.value}
                    disabled={!available}
                    className={cn(
                      isPrimaryVisual
                        ? "min-h-11 gap-2 rounded-md border border-border bg-background px-3.5 text-sm font-medium text-foreground shadow-none data-[state=on]:border-primary data-[state=on]:bg-accent data-[state=on]:font-semibold data-[state=on]:text-accent-foreground"
                        : "min-h-11 min-w-[60px] rounded-md border border-border bg-background px-3.5 text-sm font-medium text-foreground shadow-none data-[state=on]:border-primary data-[state=on]:bg-primary data-[state=on]:font-semibold data-[state=on]:text-primary-foreground",
                      !available && "cursor-not-allowed line-through opacity-40"
                    )}
                  >
                    {isPrimaryVisual && opt.hex && (
                      <span
                        className="size-3.5 shrink-0 rounded-full border border-foreground/25"
                        style={{ backgroundColor: opt.hex }}
                        aria-hidden="true"
                      />
                    )}
                    <span>{opt.value}</span>
                  </ToggleGroupItem>
                );
              })}
            </ToggleGroup>
          </div>
        );
      })}

      <div className="flex flex-wrap items-center gap-3.5">
        <div className="inline-flex items-center rounded-md border border-border bg-background">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-10 rounded-r-none"
            disabled={clampedQty <= 1 || maxStock <= 0}
            onClick={() => setQty((q) => Math.max(1, q - 1))}
            aria-label="Giảm số lượng"
          >
            <Minus className="size-4" aria-hidden="true" />
          </Button>
          <span className="w-10 text-center text-sm font-semibold tabular-nums">{clampedQty}</span>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-10 rounded-l-none"
            disabled={maxStock <= 0 || clampedQty >= maxStock}
            onClick={() => setQty((q) => Math.min(maxStock, q + 1))}
            aria-label="Tăng số lượng"
          >
            <Plus className="size-4" aria-hidden="true" />
          </Button>
        </div>
        <span
          className={cn(
            "text-sm font-medium tabular-nums",
            maxStock > 0 ? "text-muted-foreground" : "text-destructive"
          )}
        >
          {maxStock > 0
            ? `Còn ${formatInt(maxStock)} sản phẩm sẵn giao`
            : "Phân loại này tạm hết hàng"}
        </span>
      </div>

      {showVouchers && validShopVouchers.length > 0 && (
        <div className="flex flex-col gap-2.5 rounded-lg border border-border bg-background p-3.5">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-semibold text-foreground">Mã ưu đãi {brandName}</span>
            {!previewMode && (
              <Link
                href="/voucher"
                className="text-xs font-semibold text-brand underline-offset-4 hover:underline"
              >
                Lưu mã
              </Link>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            {validShopVouchers.slice(0, 3).map((v) => (
              <Badge
                key={v.id || v.code}
                variant="secondary"
                className="h-auto rounded-sm px-2.5 py-1 text-left text-xs leading-relaxed font-normal whitespace-normal"
              >
                <span>
                  <strong className="font-semibold">{v.code}</strong>,{" "}
                  {v.discountType === "percent"
                    ? `giảm ${v.discountValue}%`
                    : `giảm ${formatVnd(v.discountValue)}`}
                  {v.minOrderValue > 0 ? ` (đơn từ ${formatVnd(v.minOrderValue)})` : ""}
                </span>
              </Badge>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-2.5">
        {maxStock > 0 && (skuId || previewMode) ? (
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            <Button
              type="button"
              size="lg"
              className="min-h-11 w-full font-semibold shadow-none"
              disabled={busyAction !== null}
              onClick={() => void handleCartAction("add")}
            >
              {busyAction === "add" ? "Đang thêm..." : "Thêm vào giỏ"}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="lg"
              className="min-h-11 w-full font-semibold shadow-none"
              disabled={busyAction !== null}
              onClick={() => void handleCartAction("buy")}
            >
              {busyAction === "buy"
                ? "Đang chuyển..."
                : `Mua ngay (${formatVnd(displayPrice * clampedQty)})`}
            </Button>
          </div>
        ) : (
          <Button type="button" size="lg" className="min-h-11 w-full shadow-none" disabled>
            Tạm hết hàng
          </Button>
        )}

        {addedNotice && !drawerOpen && (
          <p
            className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/60 px-3.5 py-2.5 text-sm text-foreground"
            role="status"
          >
            <span>{addedNotice}</span>
            {!previewMode && (
              <Link
                href="/gio-hang"
                className="font-semibold text-brand underline-offset-4 hover:underline"
              >
                Xem giỏ hàng
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
      ? "grid grid-cols-1"
      : relatedProducts.length === 2
        ? "grid grid-cols-1 gap-5 sm:grid-cols-2 lg:gap-8"
        : relatedProducts.length === 3
          ? "grid grid-cols-2 gap-4 lg:grid-cols-3 lg:gap-6"
          : "grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4 lg:gap-6";

  const mobileVariantLabel = selectedVariantLabel;
  const drawerVariantLabel = [selectedVariantLabel, `SL ${clampedQty}`].filter(Boolean).join(" · ");

  const storyColsClass =
    storySpecs.length === 1
      ? "grid-cols-1"
      : storySpecs.length === 2
        ? "grid-cols-1 sm:grid-cols-2"
        : storySpecs.length === 3
          ? "grid-cols-1 sm:grid-cols-3"
          : "grid-cols-2 lg:grid-cols-4";

  return (
    <div
      data-pdp-layout={layout}
      data-pdp-gallery={galleryStyle}
      className={cn("w-full text-foreground", !previewMode && "pb-20 md:pb-0")}
    >
      <section className="w-full pt-5 pb-[clamp(36px,4.5vw,60px)]">
        <div className="mx-auto w-full max-w-[var(--site-max-w,1260px)] px-[clamp(1rem,2.2vw,2rem)]">
          <Breadcrumb className="mb-5">
            <BreadcrumbList>
              <BreadcrumbItem>
                {previewMode ? (
                  <span>Trang chủ</span>
                ) : (
                  <BreadcrumbLink asChild>
                    <Link href="/">Trang chủ</Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
              <BreadcrumbSeparator />
              <BreadcrumbItem>
                {previewMode ? (
                  <span>Sản phẩm</span>
                ) : (
                  <BreadcrumbLink asChild>
                    <Link href="/san-pham">Sản phẩm</Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
              <BreadcrumbSeparator />
              <BreadcrumbItem className="min-w-0">
                <BreadcrumbPage className="truncate font-medium">{product.name}</BreadcrumbPage>
              </BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>

          <div
            className={cn(
              "grid grid-cols-1 items-start gap-6 lg:gap-10",
              useDenseTwoCol
                ? "lg:grid-cols-[minmax(0,5.5fr)_minmax(0,6.5fr)]"
                : "lg:grid-cols-[minmax(0,7fr)_minmax(0,5fr)] lg:gap-12"
            )}
          >
            {renderGallery()}

            <div className="flex min-w-0 flex-col gap-5 rounded-lg border border-border bg-card p-5 lg:sticky lg:top-20 lg:p-7">
              {renderBuyboxContent()}
            </div>
          </div>

          {renderSkuMatrixTable()}

          {storySpecs.length > 0 && (
            <div
              className={cn(
                "mt-7 grid gap-4 rounded-lg border border-border bg-card p-5 md:p-7",
                storyColsClass
              )}
              data-cols={storySpecs.length}
              data-reveal="rows"
            >
              {storySpecs.map((item) => (
                <div key={item.key} className="flex flex-col gap-1">
                  <span className="text-[13px] text-muted-foreground">{item.label}</span>
                  <span
                    className={cn(
                      "text-[15px] font-semibold text-foreground",
                      item.isNum && "tabular-nums"
                    )}
                  >
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
        <section
          className="w-full border-t border-border bg-card py-[clamp(36px,4.8vw,64px)]"
          aria-labelledby="pdp-related-heading"
        >
          <div className="mx-auto w-full max-w-[var(--site-max-w,1260px)] px-[clamp(1rem,2.2vw,2rem)]">
            <div className="mb-6 flex flex-wrap items-baseline justify-between gap-3">
              <h2
                id="pdp-related-heading"
                className="text-xl font-semibold tracking-tight text-foreground"
              >
                Sản phẩm cùng cửa hàng
              </h2>
              {previewMode ? (
                <span className="text-sm font-semibold text-brand">Xem tất cả</span>
              ) : (
                <Link
                  href="/san-pham"
                  className="text-sm font-semibold text-brand underline-offset-4 hover:underline"
                >
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
        <div
          className="fixed inset-x-0 bottom-0 z-40 flex items-center justify-between gap-3 border-t border-border bg-card px-3.5 py-2.5 pb-[calc(0.625rem+env(safe-area-inset-bottom,0px))] md:hidden"
          data-mobile-buybar=""
        >
          <div className="flex min-w-0 flex-col">
            <Money vnd={displayPrice} size="md" />
            {mobileVariantLabel && (
              <span className="truncate text-xs text-muted-foreground">{mobileVariantLabel}</span>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-11 shadow-none"
              disabled={maxStock <= 0 || !skuId || busyAction !== null}
              onClick={() => void handleCartAction("add")}
              aria-label="Cho vào giỏ"
            >
              <ShoppingBag className="size-[18px]" aria-hidden="true" />
            </Button>
            <Button
              type="button"
              className="min-h-11 px-5 text-[15px] font-semibold shadow-none"
              disabled={maxStock <= 0 || !skuId || busyAction !== null}
              onClick={() => void handleCartAction("buy")}
            >
              {maxStock <= 0 ? "Hết hàng" : busyAction === "buy" ? "Đang chuyển..." : "Mua ngay"}
            </Button>
          </div>
        </div>
      )}

      {/* Lazy-loaded slide-right Add-to-Cart confirmation Sheet */}
      {drawerMounted && (
        <CartConfirmSheet
          open={drawerOpen}
          onOpenChange={setDrawerOpen}
          brandName={brandName}
          productName={product.name}
          imageSrc={activeImage}
          variantLabel={drawerVariantLabel}
          subtotalVnd={displayPrice * clampedQty}
          addedNotice={addedNotice}
        />
      )}
    </div>
  );
}
