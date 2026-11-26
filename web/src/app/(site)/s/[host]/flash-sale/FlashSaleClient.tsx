"use client";

import React, { useEffect, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Bell, BellRing, Clock, Flame, Loader2, ShoppingBag, Tag, Zap } from "lucide-react";
import { FLASH_SALE_RULES, type FlashSaleSlot } from "@/data/flash-sale";
import type { Product } from "@/data/products";
import {
  listFlashSaleSkuOptions,
  pickDefaultFlashSlot,
  secondsUntilSlotEdge,
  type FlashSaleSkuOption,
  type SiteFlashSaleItem,
  type SiteFlashSaleSlot,
} from "@/lib/catalog-map";
import { formatDiscountPercent } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useStore } from "@/context/StoreContext";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { StatusBadge } from "@/components/app/StatusBadge";
import type { StatusTone } from "@/components/app/statusTone";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { SiteLoadingSkeleton } from "@/features/commerce/ui/SiteLoadingSkeleton";
import { SitePageFrame, SitePageHeader } from "@/features/commerce/ui/SitePageFrame";

export interface FlashSaleClientProps {
  brandName: string;
  slots: Array<FlashSaleSlot | SiteFlashSaleSlot>;
  products: Product[];
  loadError: string | null;
  serverNowMs: number;
}

const VN_OFFSET_MS = 7 * 60 * 60 * 1000;

function twoDigit(n: number): string {
  const safe = Math.max(0, Math.trunc(n));
  return safe < 10 ? `0${safe}` : `${safe}`;
}

function vnDayKey(ms: number): string {
  const d = new Date(ms + VN_OFFSET_MS);
  return `${d.getUTCFullYear()}-${d.getUTCMonth() + 1}-${d.getUTCDate()}`;
}

function vnShortDate(ms: number): string {
  const d = new Date(ms + VN_OFFSET_MS);
  return `${twoDigit(d.getUTCDate())}/${twoDigit(d.getUTCMonth() + 1)}`;
}

function formatSlotWindowLabel(slot: FlashSaleSlot, nowMs: number): string {
  const startMs = slot.startsAt ? Date.parse(slot.startsAt) : Number.NaN;
  const endMs = slot.endsAt ? Date.parse(slot.endsAt) : Number.NaN;
  const hasStart = Number.isFinite(startMs);
  const hasEnd = Number.isFinite(endMs);
  const crossDay = hasStart && hasEnd && vnDayKey(startMs) !== vnDayKey(endMs);

  if (slot.status === "live") {
    if (hasEnd && (vnDayKey(endMs) !== vnDayKey(nowMs) || crossDay)) {
      return `đến ${slot.endTime}, ${vnShortDate(endMs)}`;
    }
    return `${slot.startTime} đến ${slot.endTime}`;
  }
  if (slot.status === "upcoming") {
    if (hasStart && vnDayKey(startMs) !== vnDayKey(nowMs)) {
      return `mở lúc ${slot.startTime}, ${vnShortDate(startMs)}`;
    }
    if (hasEnd && crossDay) {
      return `${slot.startTime} đến ${slot.endTime}, ${vnShortDate(endMs)}`;
    }
    return `${slot.startTime} đến ${slot.endTime}`;
  }
  if (hasEnd && (vnDayKey(endMs) !== vnDayKey(nowMs) || crossDay)) {
    return `kết thúc ${slot.endTime}, ${vnShortDate(endMs)}`;
  }
  return `${slot.startTime} đến ${slot.endTime}`;
}

function slotStatusTone(status: FlashSaleSlot["status"]): StatusTone {
  if (status === "live") return "warning";
  if (status === "upcoming") return "info";
  return "neutral";
}

export function FlashSaleClient({
  brandName,
  slots,
  products,
  loadError,
  serverNowMs,
}: FlashSaleClientProps) {
  const searchParams = useSearchParams();
  const router = useRouter();

  const { addToCart, cartItems, flashSaleItemBySku, showToast } = useStore();

  const stateParam = searchParams.get("state");
  const slotQuery = searchParams.get("slot");

  const productById = useMemo(() => new Map(products.map((p) => [p.id, p])), [products]);
  const defaultSlot = useMemo(() => pickDefaultFlashSlot(slots, slotQuery), [slots, slotQuery]);

  const [activeSlotId, setActiveSlotId] = useState<string>(defaultSlot?.id ?? "");
  const [nowOffsetSec, setNowOffsetSec] = useState<number>(0);
  const [claimedCounts, setClaimedCounts] = useState<Record<string, number>>({});
  const [remindedIds, setRemindedIds] = useState<Record<string, boolean>>({});
  const [selectedSkuIds, setSelectedSkuIds] = useState<Record<string, string>>({});
  const [busyKey, setBusyKey] = useState<string | null>(null);

  useEffect(() => {
    const id = window.setInterval(() => {
      setNowOffsetSec((prev) => prev + 1);
    }, 1000);
    return () => window.clearInterval(id);
  }, []);

  const activeSlot = useMemo(
    () => slots.find((s) => s.id === activeSlotId) ?? defaultSlot,
    [activeSlotId, defaultSlot, slots]
  );

  const liveNowMs = serverNowMs + nowOffsetSec * 1000;
  const secondsLeft = useMemo(
    () => secondsUntilSlotEdge(activeSlot, liveNowMs),
    [activeSlot, liveNowMs]
  );

  const flashCountInCart = useMemo(
    () =>
      cartItems
        .filter((it) => Boolean(flashSaleItemBySku[it.sku_id]))
        .reduce((sum, it) => sum + it.quantity, 0),
    [cartItems, flashSaleItemBySku]
  );

  const hrs = twoDigit(Math.floor(secondsLeft / 3600));
  const mins = twoDigit(Math.floor((secondsLeft % 3600) / 60));
  const secs = twoDigit(secondsLeft % 60);

  if (stateParam === "loading") {
    return (
      <SitePageFrame>
        <SitePageHeader
          title={`Flash sale ${brandName}`}
          subtitle="Giá ưu đãi theo khung giờ, suất cập nhật trực tiếp."
        />
        <SiteLoadingSkeleton title="Đang tải khung giờ Flash sale..." rows={4} variant="grid" />
      </SitePageFrame>
    );
  }

  if (loadError || stateParam === "error") {
    return (
      <SitePageFrame>
        <SitePageHeader
          title={`Flash sale ${brandName}`}
          subtitle="Giá ưu đãi theo khung giờ, suất cập nhật trực tiếp."
        />
        <ErrorState
          title="Không thể tải phiên Flash sale"
          description={loadError ?? "Hệ thống ưu đãi tạm gián đoạn. Vui lòng thử lại."}
          onRetry={() => (loadError ? router.refresh() : router.replace("/flash-sale"))}
          className="border border-border shadow-none"
        />
      </SitePageFrame>
    );
  }

  if (stateParam === "empty" || !activeSlot) {
    return (
      <SitePageFrame>
        <SitePageHeader
          title={`Flash sale ${brandName}`}
          subtitle="Giá ưu đãi theo khung giờ, suất cập nhật trực tiếp."
          action={
            <>
              <Button asChild variant="outline" className="min-h-10 shadow-none">
                <Link href="/voucher">Mã giảm giá cửa hàng</Link>
              </Button>
              <Button asChild className="min-h-10 shadow-none">
                <Link href="/san-pham">Tất cả sản phẩm</Link>
              </Button>
            </>
          }
        />
        <EmptyState
          icon={Zap}
          title={`${brandName} chưa mở khung Flash sale`}
          description={`Xem toàn bộ sản phẩm hoặc lưu mã giảm giá riêng của ${brandName}.`}
          className="border border-border shadow-none"
          action={
            <Button asChild className="min-h-10 shadow-none">
              <Link href="/san-pham">Xem sản phẩm</Link>
            </Button>
          }
        />
      </SitePageFrame>
    );
  }

  const handleSelectSlot = (id: string) => {
    setActiveSlotId(id);
    router.replace(`/flash-sale?slot=${encodeURIComponent(id)}`, { scroll: false });
  };

  const handleQuickFlashBuy = async (
    item: SiteFlashSaleItem,
    option: FlashSaleSkuOption | null,
    liveRemaining: number
  ) => {
    if (busyKey) return;
    const product = productById.get(item.productId);
    if (!product) return;
    if (!option || !option.skuId || !option.detail.id) {
      showToast("Phân loại này không nằm trong suất Flash sale.");
      return;
    }
    if (flashCountInCart >= FLASH_SALE_RULES.maxItemsPerOrder) {
      showToast(
        `Mỗi đơn Flash sale chỉ chọn tối đa ${FLASH_SALE_RULES.maxItemsPerOrder} sản phẩm ưu đãi.`
      );
      return;
    }
    const flashItemId = option.detail.id;
    const already = claimedCounts[flashItemId] ?? 0;
    if (option.detail.limitPerBuyer > 0 && already >= option.detail.limitPerBuyer) {
      showToast(`Bạn đã chọn tối đa ${option.detail.limitPerBuyer} suất cho phân loại này.`);
      return;
    }
    if (liveRemaining <= 0) {
      showToast("Suất ưu đãi của phân loại này đã hết.");
      return;
    }
    if (option.sku.stock <= 0) {
      showToast(`Phân loại ${option.label} hiện đã hết hàng.`);
      return;
    }

    setBusyKey(item.id);
    try {
      const res = await addToCart({
        skuId: option.skuId,
        quantity: 1,
        flashSaleItemId: flashItemId,
        productName: product.shortName,
        size: option.label,
      });
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent("/flash-sale")}`);
        return;
      }
      if (res.ok) {
        setClaimedCounts((prev) => ({ ...prev, [flashItemId]: already + 1 }));
      }
    } finally {
      setBusyKey(null);
    }
  };

  const handleToggleRemind = (itemId: string, productShortName: string, slotStart: string) => {
    setRemindedIds((prev) => {
      const nextVal = !prev[itemId];
      showToast(
        nextVal
          ? `Đã bật nhắc mở bán ${productShortName} lúc ${slotStart}.`
          : `Đã tắt nhắc mở bán cho ${productShortName}.`
      );
      return { ...prev, [itemId]: nextVal };
    });
  };

  const renderableItems = activeSlot.items
    .map((item) => {
      const product = productById.get(item.productId);
      if (!product) return null;
      const skuOptions = listFlashSaleSkuOptions(product, item);
      return { item: item as SiteFlashSaleItem, product, skuOptions };
    })
    .filter(
      (
        entry
      ): entry is {
        item: SiteFlashSaleItem;
        product: Product;
        skuOptions: FlashSaleSkuOption[];
      } => entry !== null && entry.skuOptions.length > 0
    );

  const gridClass =
    renderableItems.length === 1
      ? "grid grid-cols-1"
      : renderableItems.length === 2
        ? "grid grid-cols-1 gap-4 sm:grid-cols-2"
        : renderableItems.length === 3
          ? "grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3"
          : "grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4";

  const slotColsClass =
    slots.length <= 1
      ? "grid-cols-1"
      : slots.length === 2
        ? "grid-cols-1 sm:grid-cols-2"
        : slots.length === 3
          ? "grid-cols-1 sm:grid-cols-3"
          : "grid-cols-2 lg:grid-cols-4";

  const activeWindowLabel = formatSlotWindowLabel(activeSlot, liveNowMs);

  return (
    <SitePageFrame className="space-y-8">
      {/* Hero + live countdown + slot selector */}
      <section aria-label={`Flash sale của ${brandName}`} className="space-y-4">
        <Card className="border border-border py-0 shadow-none">
          <CardContent className="grid grid-cols-1 gap-6 py-5 sm:p-6 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
            <div className="min-w-0 space-y-3">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge tone={slotStatusTone(activeSlot.status)} icon={Flame}>
                  {activeSlot.statusLabel}
                </StatusBadge>
                <span className="text-xs text-muted-foreground">{activeWindowLabel}</span>
              </div>

              <div className="space-y-1.5">
                <h1 className="text-2xl leading-tight font-semibold tracking-tight text-foreground sm:text-3xl">
                  Flash sale {brandName}
                </h1>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  Giá ưu đãi theo khung giờ, suất cập nhật trực tiếp.
                </p>
              </div>

              <div className="flex flex-wrap items-center gap-2.5 pt-1">
                <Button asChild className="min-h-10 shadow-none">
                  <Link href="/voucher">
                    <Tag className="size-4" aria-hidden="true" />
                    <span>Mã giảm giá cửa hàng</span>
                  </Link>
                </Button>
                <Button asChild variant="outline" className="min-h-10 shadow-none">
                  <Link href="/san-pham">Tất cả sản phẩm</Link>
                </Button>
              </div>
            </div>

            {/* Countdown panel */}
            <div className="flex flex-col gap-3 rounded-xl border border-border bg-muted/40 p-4 sm:min-w-[290px]">
              <div className="flex items-center justify-between gap-2 text-xs">
                <span className="inline-flex items-center gap-1.5 font-semibold text-foreground">
                  <Clock className="size-3.5 text-brand" aria-hidden="true" />
                  <span>
                    {activeSlot.status === "live"
                      ? "Kết thúc sau"
                      : activeSlot.status === "upcoming"
                        ? "Mở bán sau"
                        : "Đã kết thúc"}
                  </span>
                </span>
                <span className="text-muted-foreground">
                  {activeSlot.statusLabel.toLowerCase()}
                </span>
              </div>

              <div
                className="flex items-center justify-center gap-1.5 tabular-nums"
                aria-live="off"
                aria-label={`${hrs} giờ ${mins} phút ${secs} giây`}
              >
                <span className="inline-flex min-w-14 items-center justify-center rounded-lg border border-border bg-card px-2.5 py-2 text-2xl font-bold text-foreground">
                  {hrs}
                </span>
                <span className="text-xl font-bold text-muted-foreground" aria-hidden="true">
                  :
                </span>
                <span className="inline-flex min-w-14 items-center justify-center rounded-lg border border-border bg-card px-2.5 py-2 text-2xl font-bold text-foreground">
                  {mins}
                </span>
                <span className="text-xl font-bold text-muted-foreground" aria-hidden="true">
                  :
                </span>
                <span className="inline-flex min-w-14 items-center justify-center rounded-lg border border-border bg-card px-2.5 py-2 text-2xl font-bold text-foreground">
                  {secs}
                </span>
              </div>

              <div className="flex items-center justify-between gap-2 border-t border-border pt-2.5 text-xs text-muted-foreground">
                <span className="inline-flex items-center gap-1.5">
                  <ShoppingBag className="size-3.5" aria-hidden="true" />
                  <span>
                    Trong giỏ:{" "}
                    <strong className="font-semibold text-foreground tabular-nums">
                      {flashCountInCart}/{FLASH_SALE_RULES.maxItemsPerOrder}
                    </strong>{" "}
                    SP Flash sale
                  </span>
                </span>
              </div>
            </div>
          </CardContent>
        </Card>

        {slots.length > 0 && (
          <nav className={cn("grid gap-2.5", slotColsClass)} aria-label="Chọn khung giờ Flash sale">
            {slots.map((slot) => {
              const isActive = slot.id === activeSlot.id;
              return (
                <button
                  key={slot.id}
                  type="button"
                  onClick={() => handleSelectSlot(slot.id)}
                  aria-pressed={isActive}
                  className={cn(
                    "flex min-h-14 flex-col justify-between gap-1.5 rounded-xl border px-4 py-3 text-left transition-colors",
                    isActive
                      ? "border-brand bg-card text-foreground ring-1 ring-brand"
                      : "border-border bg-card/70 text-foreground hover:bg-accent/60"
                  )}
                >
                  <span className="flex w-full items-center justify-between gap-2">
                    <span className="text-base font-semibold tabular-nums">{slot.label}</span>
                    <StatusBadge tone={slotStatusTone(slot.status)}>{slot.statusLabel}</StatusBadge>
                  </span>
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {slot.items.length} SP, tối đa {slot.maxItemsPerOrder ?? 3}/đơn
                  </span>
                </button>
              );
            })}
          </nav>
        )}
      </section>

      {/* Flash sale products grid */}
      <section className="space-y-4" aria-labelledby="flash-slot-heading">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 id="flash-slot-heading" className="text-lg font-semibold text-foreground">
            Khung {activeSlot.startTime} ({activeWindowLabel})
          </h2>
          <span className="text-xs text-muted-foreground tabular-nums">
            {renderableItems.length} sản phẩm, tối đa {FLASH_SALE_RULES.maxItemsPerOrder} SP/đơn
          </span>
        </div>

        {renderableItems.length === 0 ? (
          <EmptyState
            icon={Zap}
            title="Khung giờ này chưa có sản phẩm ưu đãi"
            description={`Chọn khung giờ khác phía trên hoặc xem toàn bộ sản phẩm của ${brandName}.`}
            className="border border-border shadow-none"
            action={
              <Button asChild className="min-h-10 shadow-none">
                <Link href="/san-pham">Xem sản phẩm</Link>
              </Button>
            }
          />
        ) : (
          <div className={gridClass}>
            {renderableItems.map(({ item, product, skuOptions }, idx) => {
              const defaultOption =
                skuOptions.find((o) => o.sku.stock > 0 && o.detail.remainingQuota > 0) ??
                skuOptions.find((o) => o.sku.stock > 0) ??
                skuOptions[0] ??
                null;
              const chosenOption =
                skuOptions.find((o) => o.skuId === selectedSkuIds[item.id]) ?? defaultOption;
              const detail = chosenOption?.detail;
              const flashPrice = detail?.flashPrice ?? item.flashPrice;
              const originalPrice = detail?.originalPrice ?? item.originalPrice;
              const totalQuota = detail?.totalQuota ?? item.totalQuota;
              const limitPerBuyer = detail?.limitPerBuyer ?? item.limitPerBuyer;
              const claimed = detail ? (claimedCounts[detail.id] ?? 0) : 0;
              const liveRemaining = detail ? Math.max(0, detail.remainingQuota - claimed) : 0;
              const soldRatio =
                totalQuota > 0
                  ? Math.min(100, Math.round(((totalQuota - liveRemaining) / totalQuota) * 100))
                  : 100;

              const skuOutOfStock = !chosenOption || chosenOption.sku.stock <= 0;
              const isReminded = Boolean(remindedIds[item.id]);
              const hoverImage = product.onModelImage ?? product.gallery[1];
              const discountTag = formatDiscountPercent(originalPrice, flashPrice);
              const singleLayout = renderableItems.length === 1;

              return (
                <Card
                  key={item.id}
                  className={cn(
                    "overflow-hidden border border-border py-0 shadow-none",
                    singleLayout
                      ? "grid grid-cols-1 md:grid-cols-[minmax(0,320px)_minmax(0,1fr)]"
                      : "flex flex-col"
                  )}
                >
                  <Link
                    href={`/san-pham/${product.slug}`}
                    className="group relative block aspect-[3/4] w-full overflow-hidden bg-muted"
                  >
                    <Image
                      src={product.image}
                      alt={product.name}
                      fill
                      sizes={
                        singleLayout
                          ? "(max-width: 768px) 100vw, 40vw"
                          : "(max-width: 1023px) 50vw, 25vw"
                      }
                      priority={idx < 4}
                      className={cn(
                        "object-cover object-top transition-opacity duration-200",
                        hoverImage && "group-hover:opacity-0"
                      )}
                    />
                    {hoverImage && (
                      <Image
                        src={hoverImage}
                        alt={`${product.name} khi mặc thực tế`}
                        fill
                        sizes={
                          singleLayout
                            ? "(max-width: 768px) 100vw, 40vw"
                            : "(max-width: 1023px) 50vw, 25vw"
                        }
                        className="object-cover object-top opacity-0 transition-opacity duration-200 group-hover:opacity-100"
                      />
                    )}
                    {discountTag && (
                      <Badge
                        variant="default"
                        className="absolute top-2.5 left-2.5 rounded-sm px-2 py-0.5 text-[11px] font-semibold tabular-nums"
                      >
                        {discountTag}
                      </Badge>
                    )}
                  </Link>

                  <CardContent className="flex flex-1 flex-col justify-between gap-4 p-4">
                    <div className="space-y-3">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <StatusBadge tone="warning" icon={Flame}>
                          Flash sale
                        </StatusBadge>
                        {limitPerBuyer > 0 && (
                          <Badge variant="outline" className="rounded-sm text-[11px] tabular-nums">
                            Tối đa {limitPerBuyer}/mẫu
                          </Badge>
                        )}
                      </div>

                      <Link
                        href={`/san-pham/${product.slug}`}
                        className="line-clamp-2 block text-[15px] leading-snug font-semibold text-foreground transition-colors hover:text-brand"
                      >
                        {product.name}
                      </Link>

                      <Money vnd={flashPrice} compareAtVnd={originalPrice} size="lg" />

                      <div className="space-y-1.5">
                        <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
                          <span className="font-medium text-foreground">
                            {activeSlot.status === "ended"
                              ? `Đã kết thúc (${totalQuota} suất)`
                              : activeSlot.status === "upcoming"
                                ? `${totalQuota} suất lúc ${activeSlot.startTime}`
                                : `Còn ${liveRemaining}/${totalQuota} suất`}
                          </span>
                          <span>
                            {activeSlot.status === "upcoming" ? "0%" : `${soldRatio}% đã bán`}
                          </span>
                        </div>
                        <Progress
                          value={activeSlot.status === "upcoming" ? 0 : soldRatio}
                          aria-label={`Tiến độ suất ưu đãi ${product.shortName}`}
                          className={cn("h-2", activeSlot.status === "ended" && "opacity-50")}
                        />
                      </div>
                    </div>

                    <div className="space-y-2.5 pt-1">
                      {activeSlot.status === "live" && (
                        <>
                          <div
                            className="flex flex-wrap gap-1.5"
                            role="group"
                            aria-label={`Chọn phân loại ưu đãi ${product.shortName}`}
                          >
                            {skuOptions.map((opt) => {
                              const optClaimed = claimedCounts[opt.detail.id] ?? 0;
                              const optDisabled =
                                opt.sku.stock <= 0 || opt.detail.remainingQuota - optClaimed <= 0;
                              const active = chosenOption?.skuId === opt.skuId;
                              return (
                                <Button
                                  key={opt.skuId}
                                  type="button"
                                  size="sm"
                                  variant={active ? "default" : "outline"}
                                  disabled={optDisabled}
                                  onClick={() =>
                                    setSelectedSkuIds((prev) => ({ ...prev, [item.id]: opt.skuId }))
                                  }
                                  aria-pressed={active}
                                  className={cn(
                                    "min-h-9 px-2.5 text-xs shadow-none",
                                    optDisabled && "line-through opacity-50"
                                  )}
                                >
                                  {opt.label}
                                </Button>
                              );
                            })}
                          </div>

                          <Button
                            type="button"
                            className="min-h-10 w-full shadow-none"
                            disabled={
                              !chosenOption ||
                              liveRemaining <= 0 ||
                              skuOutOfStock ||
                              busyKey === item.id
                            }
                            onClick={() =>
                              void handleQuickFlashBuy(item, chosenOption, liveRemaining)
                            }
                          >
                            {busyKey === item.id && (
                              <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                            )}
                            <span>
                              {liveRemaining <= 0
                                ? "Đã hết suất"
                                : skuOutOfStock
                                  ? "Tạm hết hàng"
                                  : busyKey === item.id
                                    ? "Đang thêm..."
                                    : `Mua ưu đãi (${chosenOption?.label ?? ""})`}
                            </span>
                          </Button>
                        </>
                      )}

                      {activeSlot.status === "upcoming" && (
                        <Button
                          type="button"
                          variant={isReminded ? "default" : "outline"}
                          className="min-h-10 w-full shadow-none"
                          onClick={() =>
                            handleToggleRemind(item.id, product.shortName, activeSlot.startTime)
                          }
                        >
                          {isReminded ? (
                            <BellRing className="size-4" aria-hidden="true" />
                          ) : (
                            <Bell className="size-4" aria-hidden="true" />
                          )}
                          <span>
                            {isReminded
                              ? `Đã bật nhắc lúc ${activeSlot.startTime}`
                              : `Nhắc tôi lúc ${activeSlot.startTime}`}
                          </span>
                        </Button>
                      )}

                      {activeSlot.status === "ended" && (
                        <Button asChild variant="outline" className="min-h-10 w-full shadow-none">
                          <Link href={`/san-pham/${product.slug}`}>Xem giá thường</Link>
                        </Button>
                      )}
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        )}
      </section>
    </SitePageFrame>
  );
}
