"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  AlertTriangle,
  Check,
  Loader2,
  LogIn,
  Minus,
  Plus,
  ShoppingBag,
  Tag,
  Trash2,
  X,
} from "lucide-react";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { Voucher } from "@/data/vouchers";
import type { GatewayActiveShopDiscount, GatewayFlashSale } from "@/lib/gateway-types";
import { ApiError, api } from "@/lib/api";
import { formatSignedVnd, formatVnd } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import {
  evaluateShopFlashLines,
  fetchActiveShopDiscountsWithFetcher,
  resolveCartFlashSaleState,
  resolveDisplayedLinePricing,
} from "@/features/checkout/helpers";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { SiteLoadingSkeleton } from "@/features/commerce/ui/SiteLoadingSkeleton";
import { SitePageFrame, SitePageHeader } from "@/features/commerce/ui/SitePageFrame";
import {
  enrichCartGroups,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
  shortVoucherHint,
  splitVariantParts,
} from "@/features/commerce/cartEnrich";
import type { GwDiscountBreakdown, GwVoucher } from "@/features/commerce/types";
import { SiteRenderer } from "@/features/site/SiteRenderer";
import type {
  CartPageConfig,
  SiteCartLayout,
  SiteTemplateConfig,
} from "@/features/site/templateTypes";

const UNDO_MS = 5000;

interface UndoItem {
  key: number;
  skuId: string;
  quantity: number;
  productName: string;
  flashSaleItemId?: string;
}

export interface CartClientProps {
  brandName: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialVouchers: GwVoucher[];
  initialFlashSales?: GatewayFlashSale[];
  cartConfig?: CartPageConfig;
  templateConfig?: SiteTemplateConfig;
  shop?: SitePublicShopDTO;
  vouchers?: Voucher[];
  serverNowMs?: number;
}

function getCartGridClasses(layout: SiteCartLayout): {
  grid: string;
  rowPad: string;
  thumbWidth: string;
  cardPad: string;
} {
  switch (layout) {
    case "compact-table":
      return {
        grid: "lg:grid-cols-[minmax(0,1.65fr)_minmax(320px,0.9fr)] lg:gap-5",
        rowPad: "py-3.5",
        thumbWidth: "w-[72px] sm:w-[76px]",
        cardPad: "px-4 md:px-5",
      };
    case "editorial-ledger":
      return {
        grid: "lg:grid-cols-[minmax(0,1.45fr)_minmax(340px,0.95fr)] lg:gap-10 xl:gap-12",
        rowPad: "py-5",
        thumbWidth: "w-20 sm:w-[104px]",
        cardPad: "px-5 md:px-6",
      };
    case "luxury-folio":
      return {
        grid: "lg:grid-cols-[minmax(0,1.45fr)_minmax(350px,0.95fr)] lg:gap-9",
        rowPad: "py-5",
        thumbWidth: "w-20 sm:w-[100px]",
        cardPad: "px-5 md:px-6",
      };
    case "express-bar":
      return {
        grid: "lg:grid-cols-[minmax(0,1.55fr)_minmax(330px,0.95fr)] lg:gap-5",
        rowPad: "py-4",
        thumbWidth: "w-20 sm:w-[84px]",
        cardPad: "px-4 md:px-5",
      };
    case "atelier-bag":
    case "split-summary":
    default:
      return {
        grid: "lg:grid-cols-[minmax(0,1.5fr)_minmax(340px,0.95fr)] lg:gap-8",
        rowPad: "py-4 sm:py-5",
        thumbWidth: "w-20 sm:w-[96px]",
        cardPad: "px-4 md:px-6",
      };
  }
}

export function CartClient({
  brandName,
  catalogProducts,
  catalogShops,
  initialVouchers,
  initialFlashSales = [],
  cartConfig,
  templateConfig,
  shop,
  vouchers: siteVouchers = [],
  serverNowMs,
}: CartClientProps) {
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());
  const { isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cart,
    cartLoading,
    cartError,
    refreshCart,
    addToCart,
    updateCartItemQty,
    removeCartItem,
    clearCart,
    selectedVoucherId,
    flashSaleItemBySku,
    optedOutFlashSkus,
    optOutFlashSaleSku,
    restoreFlashSaleSku,
    setSelectedVoucher,
    saveVoucher,
    showToast,
  } = useStore();

  const cartLayout: SiteCartLayout =
    cartConfig?.layout ?? templateConfig?.pages.cart.layout ?? "split-summary";
  const showVouchers =
    cartConfig?.show_vouchers ?? templateConfig?.pages.cart.show_vouchers ?? true;
  const showAddons = cartConfig?.show_addons ?? templateConfig?.pages.cart.show_addons ?? true;
  const trustNote =
    cartConfig?.trust_note?.trim() || templateConfig?.pages.cart.trust_note?.trim() || "";
  const extraSections = cartConfig?.sections ?? templateConfig?.pages.cart.sections ?? [];
  const layoutCls = getCartGridClasses(cartLayout);

  const [vouchers, setVouchers] = useState<GwVoucher[]>(initialVouchers);
  const [savingVoucherId, setSavingVoucherId] = useState<string | null>(null);
  const [breakdown, setBreakdown] = useState<GwDiscountBreakdown | null>(null);
  const [rejectedFlashSaleItemIds, setRejectedFlashSaleItemIds] = useState<string[]>([]);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [busySkuId, setBusySkuId] = useState<string | null>(null);
  const [undoEntry, setUndoEntry] = useState<UndoItem | null>(null);
  const [undoing, setUndoing] = useState<boolean>(false);
  const undoTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);

  const [clearing, setClearing] = useState<boolean>(false);
  const [confirmClear, setConfirmClear] = useState<boolean>(false);
  const [manualCode, setManualCode] = useState<string>("");
  const [validatingCode, setValidatingCode] = useState<boolean>(false);
  const [codeFeedback, setCodeFeedback] = useState<{ ok: boolean; message: string } | null>(null);
  const [shopDiscountBySku, setShopDiscountBySku] = useState<
    Record<string, GatewayActiveShopDiscount>
  >({});

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (undoTimerRef.current !== null) window.clearTimeout(undoTimerRef.current);
    };
  }, []);

  const enrichedGroups = useMemo(
    () => enrichCartGroups(cart?.shop_groups ?? [], catalogProducts, catalogShops),
    [cart?.shop_groups, catalogProducts, catalogShops]
  );
  const activeGroup = enrichedGroups[0] ?? null;
  const cartItems = useMemo(() => activeGroup?.items ?? [], [activeGroup]);
  const availableItems = useMemo(() => cartItems.filter((it) => !it.isUnavailable), [cartItems]);
  const unavailableCount = cartItems.length - availableItems.length;
  const availableSkuKey = useMemo(
    () =>
      availableItems
        .map((it) => it.raw.sku_id)
        .sort()
        .join(","),
    [availableItems]
  );

  useEffect(() => {
    const skuIds = availableSkuKey ? availableSkuKey.split(",").filter(Boolean) : [];
    if (skuIds.length === 0) {
      setShopDiscountBySku({});
      return;
    }
    let cancelled = false;
    void fetchActiveShopDiscountsWithFetcher({ skuIds }, async (batch) => {
      const qs = new URLSearchParams();
      if (batch.skuIds && batch.skuIds.length > 0) {
        qs.set("sku_ids", batch.skuIds.join(","));
      }
      const res = await api<{ discounts?: GatewayActiveShopDiscount[] }>(
        `/api/shop-discounts/active?${qs.toString()}`,
        { retryOn401: false }
      );
      return res.discounts ?? [];
    }).then((list) => {
      if (cancelled) return;
      const map: Record<string, GatewayActiveShopDiscount> = {};
      for (const d of list) {
        if (d.sku_id) map[d.sku_id] = d;
      }
      setShopDiscountBySku(map);
    });
    return () => {
      cancelled = true;
    };
  }, [availableSkuKey]);

  const addonProducts = useMemo(() => {
    const inCartProductIds = new Set(cartItems.map((c) => c.raw.product_id));
    const filtered = catalogProducts.filter(
      (p) => !inCartProductIds.has(p.backendId ?? p.id) && !inCartProductIds.has(p.id)
    );
    return (filtered.length >= 4 ? filtered : catalogProducts).slice(0, 4);
  }, [cartItems, catalogProducts]);

  const loadShopVouchers = useCallback(async () => {
    if (!isAuthenticated || !showVouchers) return;
    try {
      const vRes = await api<{ vouchers: GwVoucher[] }>("/api/vouchers");
      if (Array.isArray(vRes.vouchers)) {
        setVouchers(vRes.vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active));
      }
    } catch {
      // Keep SSR shop vouchers
    }
  }, [isAuthenticated, showVouchers]);

  useEffect(() => {
    void loadShopVouchers();
  }, [loadShopVouchers]);

  const flashState = useMemo(
    () =>
      resolveCartFlashSaleState({
        items: availableItems.map((it) => ({
          skuId: it.raw.sku_id,
          quantity: it.raw.quantity,
          regularUnitPriceVnd: it.raw.price_vnd,
        })),
        serverFlashSales: initialFlashSales,
        storedFlashItemIdBySku: flashSaleItemBySku,
        optedOutFlashSkus,
        nowMs: anchorNowMs,
      }),
    [availableItems, initialFlashSales, flashSaleItemBySku, optedOutFlashSkus, anchorNowMs]
  );

  const flashKey = JSON.stringify(flashState.activeFlashItemIdBySku);

  useEffect(() => {
    if (!isAuthenticated || !activeGroup || availableItems.length === 0) {
      setBreakdown(null);
      setRejectedFlashSaleItemIds([]);
      setPreviewError(null);
      return;
    }

    let cancelled = false;
    setPreviewLoading(true);
    setPreviewError(null);

    api<{
      breakdown: GwDiscountBreakdown;
      warnings_vi?: string[];
      rejected_flash_sale_item_ids?: string[];
    }>("/api/vouchers/preview", {
      method: "POST",
      body: {
        shop_voucher_id: showVouchers ? selectedVoucherId || "" : "",
        shipping_fee_vnd: 0,
        flash_sale_item_by_sku: flashState.activeFlashItemIdBySku,
      },
    })
      .then((res) => {
        if (!cancelled) {
          setBreakdown(res.breakdown);
          setRejectedFlashSaleItemIds(
            Array.isArray(res.rejected_flash_sale_item_ids) ? res.rejected_flash_sale_item_ids : []
          );
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setPreviewError(err instanceof ApiError ? err.message : "Chưa tính được ưu đãi lúc này.");
        }
      })
      .finally(() => {
        if (!cancelled) setPreviewLoading(false);
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAuthenticated, activeGroup, selectedVoucherId, flashKey, showVouchers]);

  const shopVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active),
    [vouchers]
  );

  const rawAvailableSubtotal = useMemo(
    () => availableItems.reduce((sum, it) => sum + it.raw.price_vnd * it.raw.quantity, 0),
    [availableItems]
  );
  const subtotal = breakdown?.total_subtotal_vnd ?? rawAvailableSubtotal;
  const flashDiscount = breakdown?.total_flash_sale_discount_vnd ?? 0;
  const shopProgramDiscount = breakdown?.total_shop_program_discount_vnd ?? 0;
  const shopVoucherDiscount = breakdown?.total_shop_voucher_discount_vnd ?? 0;
  const shopFreeshipDiscount = breakdown?.total_shop_freeship_discount_vnd ?? 0;
  const totalDiscount = breakdown?.total_discount_vnd ?? 0;
  const grandTotal = breakdown?.final_payable_vnd ?? subtotal;
  const previewWarnings = breakdown?.warnings ?? [];

  const totalItemQty = availableItems.reduce((sum, it) => sum + it.raw.quantity, 0);
  const hasStockIssue = useMemo(
    () => availableItems.some((it) => it.isOutOfStock || it.exceedsStock),
    [availableItems]
  );
  const canProceedCheckout = availableItems.length > 0 && !hasStockIssue;
  const shopBreakdown = breakdown?.shops?.[0] ?? null;
  const flashLineEvals = useMemo(
    () =>
      evaluateShopFlashLines({
        lines: availableItems.map((it) => {
          const lf = flashState.liveFlashBySku[it.raw.sku_id];
          return {
            skuId: it.raw.sku_id,
            quantity: it.raw.quantity,
            regularUnitPriceVnd: it.raw.price_vnd,
            flashSaleItemId: lf?.flashSaleItemId,
            flashUnitPriceVnd: lf?.flashPriceVnd,
            flashPerUserLimit: lf?.perUserLimit,
            hasActiveShopDiscount: Boolean(shopDiscountBySku[it.raw.sku_id]),
          };
        }),
        previewShopFlashDiscountVnd: shopBreakdown ? shopBreakdown.flash_sale_discount_vnd : null,
        previewWarnings: breakdown?.warnings ?? [],
        rejectedFlashSaleItemIds,
      }),
    [
      availableItems,
      flashState.liveFlashBySku,
      shopDiscountBySku,
      shopBreakdown,
      breakdown?.warnings,
      rejectedFlashSaleItemIds,
    ]
  );
  const isCartMutating = Boolean(busySkuId || clearing);

  const startUndoWindow = (item: Omit<UndoItem, "key">) => {
    if (!mountedRef.current) return;
    if (undoTimerRef.current !== null) window.clearTimeout(undoTimerRef.current);
    setUndoEntry({ ...item, key: Date.now() });
    undoTimerRef.current = window.setTimeout(() => {
      if (!mountedRef.current) return;
      setUndoEntry(null);
      undoTimerRef.current = null;
    }, UNDO_MS);
  };

  const dismissUndo = () => {
    if (undoTimerRef.current !== null) window.clearTimeout(undoTimerRef.current);
    undoTimerRef.current = null;
    setUndoEntry(null);
  };

  const handleRemoveWithUndo = async (skuId: string, quantity: number, productName: string) => {
    if (busySkuId || clearing) return;
    const savedFlashId = flashState.activeFlashItemIdBySku[skuId] ?? flashSaleItemBySku[skuId];
    setBusySkuId(skuId);
    try {
      const res = await removeCartItem(skuId);
      if (res.ok && mountedRef.current) {
        startUndoWindow({ skuId, quantity, productName, flashSaleItemId: savedFlashId });
      }
    } finally {
      if (mountedRef.current) {
        setBusySkuId(null);
      }
    }
  };

  const handleUndo = async () => {
    if (!undoEntry || undoing || clearing) return;
    const snapshot = undoEntry;
    setUndoing(true);
    dismissUndo();
    try {
      await addToCart({
        skuId: snapshot.skuId,
        quantity: snapshot.quantity,
        productName: snapshot.productName,
        flashSaleItemId: snapshot.flashSaleItemId,
      });
    } finally {
      if (mountedRef.current) setUndoing(false);
    }
  };

  const handleQtyChange = async (
    skuId: string,
    currentQty: number,
    nextQty: number,
    productName: string
  ) => {
    if (busySkuId || clearing) return;
    if (nextQty <= 0) {
      await handleRemoveWithUndo(skuId, currentQty, productName);
      return;
    }
    setBusySkuId(skuId);
    try {
      await updateCartItemQty(skuId, nextQty);
    } finally {
      if (mountedRef.current) setBusySkuId(null);
    }
  };

  const handleClearAll = async () => {
    if (clearing || busySkuId) return;
    setClearing(true);
    try {
      const ok = await clearCart();
      if (ok && mountedRef.current) {
        setConfirmClear(false);
        dismissUndo();
        showToast("Đã làm trống giỏ hàng.");
      }
    } finally {
      if (mountedRef.current) setClearing(false);
    }
  };

  const handleSaveVoucher = async (voucher: GwVoucher) => {
    if (savingVoucherId) return;
    setSavingVoucherId(voucher.id);
    try {
      const res = await saveVoucher(voucher.id);
      if (res.ok && mountedRef.current) {
        setVouchers((prev) => prev.map((v) => (v.id === voucher.id ? { ...v, saved: true } : v)));
      }
    } finally {
      if (mountedRef.current) setSavingVoucherId(null);
    }
  };

  const handleApplyManualCode = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = manualCode.trim().toUpperCase();
    if (!clean) {
      setCodeFeedback({ ok: false, message: "Nhập mã ưu đãi trước khi áp dụng." });
      return;
    }
    if (validatingCode) return;
    setValidatingCode(true);
    setCodeFeedback(null);
    try {
      const res = await api<{
        valid: boolean;
        voucher?: GwVoucher;
        discount_vnd: number;
        reason?: string;
        breakdown?: GwDiscountBreakdown;
        rejected_flash_sale_item_ids?: string[];
      }>("/api/vouchers/validate", {
        method: "POST",
        body: {
          code: clean,
          shop_voucher_id: selectedVoucherId || "",
          shipping_fee_vnd: 0,
          flash_sale_item_by_sku: flashState.activeFlashItemIdBySku,
        },
      });

      if (!res.valid || !res.voucher) {
        setCodeFeedback({
          ok: false,
          message: res.reason || `Mã ${clean} chưa đủ điều kiện áp dụng.`,
        });
        return;
      }

      setSelectedVoucher(res.voucher.id);
      if (res.breakdown) {
        setBreakdown(res.breakdown);
        if (Array.isArray(res.rejected_flash_sale_item_ids)) {
          setRejectedFlashSaleItemIds(res.rejected_flash_sale_item_ids);
        }
      }

      const discText = res.discount_vnd > 0 ? ` (-${formatVnd(res.discount_vnd)})` : "";
      setCodeFeedback({
        ok: true,
        message: `Đã áp mã ${res.voucher.code}${discText}.`,
      });
      setManualCode("");
    } catch (err) {
      setCodeFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa kiểm tra được mã ưu đãi.",
      });
    } finally {
      setValidatingCode(false);
    }
  };

  const renderUndoBar = () =>
    undoEntry ? (
      <div
        key={undoEntry.key}
        role="status"
        aria-live="polite"
        className="mb-5 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-card px-4 py-3 text-sm text-foreground"
      >
        <span>
          Đã xóa <strong className="font-semibold">{undoEntry.productName}</strong>.
        </span>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="default"
            size="sm"
            className="min-h-10 shadow-none sm:min-h-8"
            disabled={undoing}
            onClick={() => void handleUndo()}
          >
            Hoàn tác
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-10 shadow-none sm:size-8"
            aria-label="Đóng thông báo hoàn tác"
            onClick={dismissUndo}
          >
            <X className="size-4" aria-hidden="true" />
          </Button>
        </div>
      </div>
    ) : null;

  const renderExtraSections = () =>
    templateConfig && extraSections.length > 0 ? (
      <SiteRenderer
        config={templateConfig}
        shop={shop ?? { id: "", name: brandName }}
        products={catalogProducts}
        vouchers={siteVouchers}
        sectionsOverride={extraSections}
      />
    ) : null;

  const renderHeader = (note?: string) => (
    <SitePageHeader
      title="Giỏ hàng"
      subtitle={note}
      action={
        <Button asChild variant="outline" className="min-h-10 shadow-none">
          <Link href="/san-pham">Tiếp tục mua</Link>
        </Button>
      }
    />
  );

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <SitePageFrame data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        <SiteLoadingSkeleton title="Đang tải giỏ hàng..." rows={3} variant="detail" />
      </SitePageFrame>
    );
  }

  if (!isAuthenticated) {
    return (
      <SitePageFrame data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        <EmptyState
          icon={LogIn}
          title="Đăng nhập để xem giỏ hàng của bạn."
          description={`Đăng nhập tài khoản người mua để đồng bộ sản phẩm, mã giảm giá và thanh toán tại ${brandName}.`}
          className="border border-border shadow-none"
          action={
            <Button asChild className="min-h-10 shadow-none">
              <Link href="/dang-nhap?next=%2Fgio-hang">Đăng nhập</Link>
            </Button>
          }
        />
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm tại ${brandName}`} products={addonProducts} />
        )}
      </SitePageFrame>
    );
  }

  if (cartError && !cart) {
    return (
      <SitePageFrame data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        <ErrorState
          title="Chưa thể tải giỏ hàng."
          description={cartError}
          onRetry={() => void refreshCart()}
          className="border border-border shadow-none"
        />
      </SitePageFrame>
    );
  }

  if (!activeGroup || cartItems.length === 0) {
    return (
      <SitePageFrame data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        {renderUndoBar()}
        <EmptyState
          icon={ShoppingBag}
          title="Giỏ hàng đang trống."
          description={`Chọn sản phẩm yêu thích tại ${brandName} để thêm vào giỏ hàng.`}
          className="border border-border shadow-none"
          action={
            <Button asChild className="min-h-10 shadow-none">
              <Link href="/san-pham">Xem sản phẩm</Link>
            </Button>
          }
        />
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={addonProducts} />
        )}
      </SitePageFrame>
    );
  }

  return (
    <div data-cart-layout={cartLayout} className="pb-24 lg:pb-0">
      <SitePageFrame>
        {renderHeader(`${totalItemQty} sản phẩm tại ${brandName}`)}

        {renderUndoBar()}

        <div className={cn("grid grid-cols-1 items-start gap-6", layoutCls.grid)}>
          {/* Left column: cart line items */}
          <div className="min-w-0 space-y-4">
            {unavailableCount > 0 && (
              <Alert
                className="border-badge-warning-ink/25 bg-badge-warning-bg text-badge-warning-ink shadow-none"
                role="status"
              >
                <AlertTriangle className="size-4" aria-hidden="true" />
                <AlertTitle>Sản phẩm không còn khả dụng</AlertTitle>
                <AlertDescription className="text-badge-warning-ink">
                  Có <strong className="font-semibold tabular-nums">{unavailableCount}</strong> sản
                  phẩm không còn khả dụng và sẽ không được tính vào đơn thanh toán. Bạn có thể xóa
                  các mục này khỏi giỏ hàng.
                </AlertDescription>
              </Alert>
            )}

            <Card className="border border-border py-0 shadow-none">
              <CardContent className={cn("divide-y divide-border py-1", layoutCls.cardPad)}>
                <ul className="divide-y divide-border">
                  {cartItems.map((item) => {
                    const raw = item.raw;
                    const isBusy = Boolean(busySkuId) || clearing;
                    const liveFlash = !item.isUnavailable
                      ? (flashState.liveFlashBySku[raw.sku_id] ?? null)
                      : null;
                    const availableLiveFlash = !item.isUnavailable
                      ? (flashState.availableLiveFlashBySku[raw.sku_id] ?? null)
                      : null;
                    const isFlash = Boolean(liveFlash);
                    const isFlashOptedOut =
                      !item.isUnavailable && Boolean(availableLiveFlash) && !liveFlash;
                    const flashEval = !item.isUnavailable
                      ? (flashLineEvals[raw.sku_id] ?? null)
                      : null;
                    const flashInspection = !item.isUnavailable
                      ? (flashState.inspectionBySku[raw.sku_id] ?? null)
                      : null;
                    const activeShopDisc = !item.isUnavailable
                      ? (shopDiscountBySku[raw.sku_id] ?? null)
                      : null;
                    const linePricing = resolveDisplayedLinePricing({
                      regularUnitPriceVnd: raw.price_vnd,
                      quantity: raw.quantity,
                      isFlashLine: isFlash,
                      flashUnitPriceVnd: liveFlash?.flashPriceVnd,
                      flashShortfall: Boolean(flashEval?.needsRegularPriceChoice),
                      activeShopDiscount: activeShopDisc,
                      shopBreakdown,
                      isInPreview: Boolean(shopBreakdown),
                    });
                    const lineTotal = item.isUnavailable ? 0 : linePricing.lineTotalVnd;
                    const showLowStock =
                      !item.isUnavailable &&
                      !item.isOutOfStock &&
                      !item.exceedsStock &&
                      raw.available_stock <= 5;
                    const variantParts = splitVariantParts(item.variantLabel);

                    return (
                      <li
                        key={raw.sku_id}
                        className={cn(
                          "grid grid-cols-[auto_minmax(0,1fr)] gap-3.5 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:gap-5",
                          layoutCls.rowPad,
                          item.isUnavailable && "opacity-60"
                        )}
                      >
                        <Link
                          href={`/san-pham/${item.productSlug}`}
                          className={cn(
                            "block shrink-0 overflow-hidden rounded-lg border border-border bg-muted",
                            layoutCls.thumbWidth
                          )}
                        >
                          <img
                            src={item.image}
                            alt={item.productName}
                            width={896}
                            height={1200}
                            className="aspect-[3/4] h-full w-full object-cover"
                          />
                        </Link>

                        <div className="min-w-0 space-y-2">
                          <Link
                            href={`/san-pham/${item.productSlug}`}
                            className="line-clamp-2 block text-[15px] leading-snug font-semibold text-foreground transition-colors hover:text-brand sm:text-base"
                          >
                            {item.productName}
                          </Link>

                          {variantParts.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1.5">
                              {variantParts.map((part) => (
                                <Badge
                                  key={part}
                                  variant="secondary"
                                  className="rounded-sm font-normal"
                                >
                                  {part}
                                </Badge>
                              ))}
                            </div>
                          )}

                          {!item.isUnavailable &&
                            (raw.quantity > 1 || linePricing.originalUnitPriceVnd) && (
                              <div>
                                <Money
                                  vnd={linePricing.effectiveUnitPriceVnd}
                                  compareAtVnd={linePricing.originalUnitPriceVnd}
                                  size="sm"
                                />
                              </div>
                            )}

                          {(item.isUnavailable ||
                            isFlash ||
                            isFlashOptedOut ||
                            linePricing.shopProgramApplied ||
                            raw.price_changed ||
                            item.isOutOfStock ||
                            item.exceedsStock ||
                            showLowStock) && (
                            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                              {item.isUnavailable && (
                                <StatusBadge tone="critical">
                                  {item.unavailableReason || "Không khả dụng"}
                                </StatusBadge>
                              )}
                              {!item.isUnavailable && isFlash && (
                                <>
                                  <StatusBadge tone="warning">Flash sale</StatusBadge>
                                  <Button
                                    type="button"
                                    variant="ghost"
                                    size="xs"
                                    className="min-h-8 text-xs text-brand hover:text-brand"
                                    disabled={isBusy}
                                    onClick={() => optOutFlashSaleSku(raw.sku_id)}
                                  >
                                    Mua giá thường
                                  </Button>
                                </>
                              )}
                              {!item.isUnavailable && isFlashOptedOut && (
                                <>
                                  <StatusBadge tone="neutral">Giá thường</StatusBadge>
                                  <Button
                                    type="button"
                                    variant="ghost"
                                    size="xs"
                                    className="min-h-8 text-xs text-brand hover:text-brand"
                                    disabled={isBusy}
                                    onClick={() => restoreFlashSaleSku(raw.sku_id)}
                                  >
                                    Dùng giá Flash
                                  </Button>
                                </>
                              )}
                              {!item.isUnavailable && linePricing.shopProgramApplied && (
                                <StatusBadge tone="success">
                                  {linePricing.shopProgramName || "Giảm giá gian hàng"}
                                </StatusBadge>
                              )}
                              {!item.isUnavailable && raw.price_changed && (
                                <StatusBadge tone="attention">
                                  Giá mới (trước {formatVnd(raw.added_price_vnd)})
                                </StatusBadge>
                              )}
                              {item.isOutOfStock && (
                                <StatusBadge tone="critical">Hết hàng</StatusBadge>
                              )}
                              {item.exceedsStock && (
                                <StatusBadge tone="critical">
                                  Chỉ còn{" "}
                                  <span className="tabular-nums">{raw.available_stock}</span>
                                </StatusBadge>
                              )}
                              {showLowStock && (
                                <StatusBadge tone="warning">
                                  Còn <span className="tabular-nums">{raw.available_stock}</span>
                                </StatusBadge>
                              )}
                            </div>
                          )}

                          {flashEval?.reason ? (
                            <p className="text-xs leading-relaxed font-medium text-destructive">
                              {flashEval.reason}
                            </p>
                          ) : flashInspection && flashInspection.state !== "live" ? (
                            <p className="text-xs leading-relaxed text-muted-foreground">
                              {flashInspection.message}
                            </p>
                          ) : null}
                        </div>

                        <div className="col-span-2 flex items-center justify-between gap-3 pt-1 sm:col-span-1 sm:flex-col sm:items-end sm:justify-start sm:pt-0">
                          <span className="text-base font-semibold text-foreground tabular-nums sm:text-lg">
                            {item.isUnavailable ? "—" : formatVnd(lineTotal)}
                          </span>

                          <div className="flex items-center gap-2 sm:flex-col sm:items-end">
                            <div
                              className="inline-flex items-center rounded-md border border-border bg-background"
                              aria-label="Điều chỉnh số lượng"
                            >
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                className="size-10 rounded-r-none shadow-none sm:size-9"
                                disabled={isBusy || item.isUnavailable}
                                onClick={() =>
                                  void handleQtyChange(
                                    raw.sku_id,
                                    raw.quantity,
                                    raw.quantity - 1,
                                    item.productName
                                  )
                                }
                                aria-label={`Giảm số lượng ${item.productName}`}
                              >
                                <Minus className="size-3.5" aria-hidden="true" />
                              </Button>
                              <span className="min-w-9 text-center text-sm font-semibold text-foreground tabular-nums">
                                {raw.quantity}
                              </span>
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                className="size-10 rounded-l-none shadow-none sm:size-9"
                                disabled={
                                  isBusy ||
                                  item.isUnavailable ||
                                  item.isOutOfStock ||
                                  raw.quantity >= raw.available_stock
                                }
                                onClick={() =>
                                  void handleQtyChange(
                                    raw.sku_id,
                                    raw.quantity,
                                    raw.quantity + 1,
                                    item.productName
                                  )
                                }
                                aria-label={`Tăng số lượng ${item.productName}`}
                              >
                                <Plus className="size-3.5" aria-hidden="true" />
                              </Button>
                            </div>

                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="min-h-10 gap-1.5 text-muted-foreground shadow-none hover:text-destructive sm:min-h-8"
                              disabled={isBusy}
                              onClick={() =>
                                void handleRemoveWithUndo(
                                  raw.sku_id,
                                  raw.quantity,
                                  item.productName
                                )
                              }
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                              <span>Xóa</span>
                            </Button>
                          </div>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              </CardContent>

              <CardFooter
                className={cn(
                  "flex flex-wrap items-center justify-end gap-2 border-t border-border py-3",
                  layoutCls.cardPad
                )}
              >
                {confirmClear ? (
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      className="min-h-10 shadow-none sm:min-h-8"
                      disabled={clearing || Boolean(busySkuId)}
                      onClick={() => void handleClearAll()}
                    >
                      {clearing ? (
                        <>
                          <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                          <span>Đang xóa</span>
                        </>
                      ) : (
                        "Xóa tất cả"
                      )}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="min-h-10 shadow-none sm:min-h-8"
                      onClick={() => setConfirmClear(false)}
                    >
                      Giữ lại
                    </Button>
                  </div>
                ) : (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="min-h-10 text-muted-foreground shadow-none hover:text-destructive sm:min-h-8"
                    disabled={Boolean(busySkuId)}
                    onClick={() => setConfirmClear(true)}
                  >
                    Xóa giỏ hàng
                  </Button>
                )}
              </CardFooter>
            </Card>
          </div>

          {/* Right column: sticky order summary & voucher controls */}
          <aside className="min-w-0 lg:sticky lg:top-24" aria-label="Tóm tắt đơn hàng và mã ưu đãi">
            <Card className="overflow-hidden border border-border py-0 shadow-none">
              <CardHeader className="border-b border-border py-4">
                <CardTitle
                  className={cn(
                    "text-base font-semibold text-foreground",
                    cartLayout === "luxury-folio" && "tracking-wide"
                  )}
                >
                  Tóm tắt đơn hàng
                </CardTitle>
                {trustNote ? (
                  <p className="text-xs leading-relaxed text-muted-foreground">{trustNote}</p>
                ) : null}
              </CardHeader>

              <CardContent className="space-y-5 py-4">
                {showVouchers && (
                  <div className="space-y-3">
                    <form onSubmit={handleApplyManualCode} noValidate className="space-y-2">
                      <label
                        htmlFor="cart-manual-code"
                        className="flex items-center gap-1.5 text-xs font-semibold text-foreground"
                      >
                        <Tag className="size-3.5 text-muted-foreground" aria-hidden="true" />
                        <span>Mã giảm giá</span>
                      </label>
                      <div className="flex items-stretch gap-2">
                        <Input
                          id="cart-manual-code"
                          type="text"
                          className="min-h-10 flex-1 bg-background"
                          aria-describedby={codeFeedback ? "cart-code-feedback" : undefined}
                          value={manualCode}
                          onChange={(e) => setManualCode(e.target.value)}
                          placeholder="Nhập mã giảm giá"
                        />
                        <Button
                          type="submit"
                          variant="secondary"
                          className="min-h-10 shrink-0 shadow-none"
                          disabled={validatingCode}
                        >
                          {validatingCode ? (
                            <>
                              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                              <span>Đang kiểm tra</span>
                            </>
                          ) : (
                            "Áp mã"
                          )}
                        </Button>
                      </div>
                      {codeFeedback && (
                        <Alert
                          id="cart-code-feedback"
                          variant={codeFeedback.ok ? "default" : "destructive"}
                          className={cn(
                            "py-2.5 shadow-none",
                            codeFeedback.ok &&
                              "border-badge-success-ink/25 bg-badge-success-bg text-badge-success-ink"
                          )}
                          role={codeFeedback.ok ? "status" : "alert"}
                        >
                          <AlertDescription
                            className={cn("text-xs", codeFeedback.ok && "text-badge-success-ink")}
                          >
                            {codeFeedback.message}
                          </AlertDescription>
                        </Alert>
                      )}
                    </form>

                    {shopVouchers.length > 0 && (
                      <div className="space-y-2">
                        {shopVouchers.map((voucher) => {
                          const reason = formatVoucherIneligibleReason(
                            voucher,
                            subtotal,
                            anchorNowMs
                          );
                          const eligible = reason === null;
                          const isSelected =
                            (selectedVoucherId === voucher.id ||
                              selectedVoucherId === voucher.code) &&
                            eligible;
                          const canSave = !voucher.saved && voucher.remaining_quota > 0;
                          const savingThis = savingVoucherId === voucher.id;

                          return (
                            <div key={voucher.id} className="flex items-stretch gap-1.5">
                              <button
                                type="button"
                                disabled={!eligible}
                                title={reason ?? undefined}
                                onClick={() => setSelectedVoucher(isSelected ? null : voucher.id)}
                                className={cn(
                                  "flex min-h-11 min-w-0 flex-1 items-center justify-between gap-2 rounded-lg border px-3 py-2 text-left transition-colors",
                                  isSelected
                                    ? "border-brand bg-accent text-foreground"
                                    : "border-border bg-background text-foreground hover:bg-accent/60",
                                  !eligible && "cursor-not-allowed opacity-55 hover:bg-background"
                                )}
                              >
                                <span className="flex min-w-0 flex-col gap-0.5">
                                  <span className="truncate text-xs font-bold tracking-wide text-foreground">
                                    {voucher.code}
                                  </span>
                                  <span className="truncate text-xs text-muted-foreground">
                                    {formatVoucherHeadline(voucher)}
                                  </span>
                                </span>
                                {eligible ? (
                                  <span className="inline-flex shrink-0 items-center gap-1 text-xs font-semibold text-brand">
                                    {isSelected ? (
                                      <>
                                        <Check className="size-3.5" aria-hidden="true" />
                                        <span>Đang dùng</span>
                                      </>
                                    ) : (
                                      "Dùng"
                                    )}
                                  </span>
                                ) : (
                                  <span className="shrink-0 text-xs font-medium text-muted-foreground tabular-nums">
                                    {shortVoucherHint(voucher, subtotal, anchorNowMs)}
                                  </span>
                                )}
                              </button>
                              {canSave && (
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  className="h-auto min-h-11 shrink-0 px-3 text-xs shadow-none"
                                  disabled={Boolean(savingVoucherId)}
                                  onClick={() => void handleSaveVoucher(voucher)}
                                >
                                  {savingThis ? "Đang lưu" : "Lưu mã"}
                                </Button>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </div>
                )}

                {(previewError || previewWarnings.length > 0) && (
                  <Alert
                    className="border-badge-warning-ink/25 bg-badge-warning-bg py-2.5 text-badge-warning-ink shadow-none"
                    role="status"
                  >
                    <AlertDescription className="text-xs text-badge-warning-ink">
                      {previewError ?? previewWarnings[0]}
                    </AlertDescription>
                  </Alert>
                )}

                <Separator />

                <dl className="space-y-2.5 text-sm">
                  <div className="flex items-baseline justify-between gap-3">
                    <dt className="text-muted-foreground">
                      Tạm tính (<span className="tabular-nums">{totalItemQty}</span> sản phẩm)
                    </dt>
                    <dd className="font-medium text-foreground tabular-nums">
                      {formatVnd(subtotal)}
                    </dd>
                  </div>

                  {flashDiscount > 0 && (
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-muted-foreground">Flash sale</dt>
                      <dd className="font-semibold text-badge-success-ink tabular-nums">
                        {formatSignedVnd(-flashDiscount)}
                      </dd>
                    </div>
                  )}

                  {shopProgramDiscount > 0 && (
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-muted-foreground">Giảm giá chương trình gian hàng</dt>
                      <dd className="font-semibold text-badge-success-ink tabular-nums">
                        {formatSignedVnd(-shopProgramDiscount)}
                      </dd>
                    </div>
                  )}

                  {shopFreeshipDiscount > 0 && (
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-muted-foreground">Gian hàng hỗ trợ phí ship</dt>
                      <dd className="font-semibold text-badge-success-ink tabular-nums">
                        {formatSignedVnd(-shopFreeshipDiscount)}
                      </dd>
                    </div>
                  )}

                  {showVouchers && (
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="text-muted-foreground">Mã giảm giá</dt>
                      <dd className="font-semibold text-badge-success-ink tabular-nums">
                        {shopVoucherDiscount > 0 ? formatSignedVnd(-shopVoucherDiscount) : "0₫"}
                      </dd>
                    </div>
                  )}
                </dl>
              </CardContent>

              <div className="space-y-3 border-t border-border bg-muted/40 px-4 py-4 md:px-5">
                <div className="flex items-baseline justify-between gap-3">
                  <span className="text-sm font-medium text-muted-foreground">
                    {previewLoading ? "Đang tính…" : "Tạm tính"}
                  </span>
                  <span className="text-2xl font-bold text-foreground tabular-nums">
                    {formatVnd(grandTotal)}
                  </span>
                </div>

                {totalDiscount > 0 && (
                  <p className="text-xs font-medium text-badge-success-ink">
                    Tiết kiệm <strong className="tabular-nums">{formatVnd(totalDiscount)}</strong>
                  </p>
                )}

                {!canProceedCheckout ? (
                  <div className="space-y-2 pt-1">
                    <Button
                      type="button"
                      size="lg"
                      className="min-h-11 w-full shadow-none"
                      disabled
                    >
                      Tiến hành thanh toán
                    </Button>
                    <p className="text-xs leading-relaxed text-muted-foreground">
                      {availableItems.length === 0
                        ? "Không còn sản phẩm khả dụng trong giỏ hàng để đặt đơn."
                        : "Điều chỉnh sản phẩm hết hàng hoặc vượt tồn kho để tiếp tục."}
                    </p>
                  </div>
                ) : isCartMutating ? (
                  <Button type="button" size="lg" className="min-h-11 w-full shadow-none" disabled>
                    Đang tính…
                  </Button>
                ) : (
                  <Button asChild size="lg" className="min-h-11 w-full shadow-none">
                    <Link href="/thanh-toan">
                      {previewLoading
                        ? "Đang tính…"
                        : `Tiến hành thanh toán ${formatVnd(grandTotal)}`}
                    </Link>
                  </Button>
                )}
              </div>
            </Card>
          </aside>
        </div>

        {renderExtraSections()}

        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={addonProducts} />
        )}
      </SitePageFrame>

      {/* Mobile sticky quick-checkout footer bar */}
      <div
        className="fixed inset-x-0 bottom-0 z-30 flex items-center justify-between gap-3 border-t border-border bg-card px-4 py-3 lg:hidden"
        aria-label="Thanh toán nhanh"
      >
        <div className="min-w-0">
          <span className="block text-xs text-muted-foreground">
            Tạm tính (<span className="tabular-nums">{totalItemQty}</span>)
          </span>
          <span className="block text-lg font-bold text-foreground tabular-nums">
            {formatVnd(grandTotal)}
          </span>
        </div>
        {!canProceedCheckout ? (
          <Button type="button" className="min-h-10 shrink-0 shadow-none" disabled>
            {availableItems.length === 0 ? "Không khả dụng" : "Kiểm tra tồn"}
          </Button>
        ) : isCartMutating ? (
          <Button type="button" className="min-h-10 shrink-0 shadow-none" disabled>
            Đang tính…
          </Button>
        ) : (
          <Button asChild className="min-h-10 shrink-0 px-5 shadow-none">
            <Link href="/thanh-toan">{previewLoading ? "Đang tính…" : "Thanh toán"}</Link>
          </Button>
        )}
      </div>
    </div>
  );
}
