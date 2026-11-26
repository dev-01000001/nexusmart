"use client";

import React, { useEffect, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
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
import { useStore } from "@/context/StoreContext";
import { Button, Price, StateView } from "@/components";
import styles from "./page.module.css";

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
      <div className={styles.stateWrap}>
        <StateView kind="loading" title="Đang tải khung giờ Flash sale..." rows={6} />
      </div>
    );
  }

  if (loadError || stateParam === "error") {
    return (
      <div className={styles.stateWrap}>
        <StateView
          kind="error"
          title="Không thể tải phiên Flash sale"
          message={loadError ?? "Hệ thống ưu đãi tạm gián đoạn. Vui lòng thử lại."}
          actionLabel="Tải lại"
          onAction={() => (loadError ? router.refresh() : router.replace("/flash-sale"))}
        />
      </div>
    );
  }

  if (stateParam === "empty" || !activeSlot) {
    return (
      <div className={styles.stateWrap}>
        <StateView
          kind="empty"
          title={`${brandName} chưa mở khung Flash sale`}
          message={`Xem toàn bộ sản phẩm hoặc lưu mã giảm giá riêng của ${brandName}.`}
          actionLabel="Xem sản phẩm"
          actionHref="/san-pham"
        />
      </div>
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

  const gridVariantClass =
    renderableItems.length === 1
      ? styles.cardsGridSingle
      : renderableItems.length === 2
        ? styles.cardsGridTwo
        : renderableItems.length === 3
          ? styles.cardsGridThree
          : styles.cardsGrid;

  const slotCols = Math.max(1, Math.min(4, slots.length));
  const activeWindowLabel = formatSlotWindowLabel(activeSlot, liveNowMs);

  return (
    <div className={styles.page}>
      <section className={styles.heroBand} aria-label={`Flash sale của ${brandName}`}>
        <div className={styles.headerGrid}>
          <div className={styles.titleBlock}>
            <h1 className={styles.title}>Flash sale {brandName}</h1>
            <p className={styles.lead}>Giá ưu đãi theo khung giờ, suất cập nhật trực tiếp.</p>
            <div className={styles.heroActionRow}>
              <Link href="/voucher" className={styles.heroBtnPrimary}>
                Mã giảm giá cửa hàng
              </Link>
              <Link href="/san-pham" className={styles.heroBtnDark}>
                Tất cả sản phẩm
              </Link>
            </div>
          </div>

          <div className={styles.timerBox}>
            <div className={styles.timerMetaRow}>
              <span className={styles.timerHeading}>
                {activeSlot.status === "live"
                  ? "Kết thúc sau"
                  : activeSlot.status === "upcoming"
                    ? "Mở bán sau"
                    : "Đã kết thúc"}
              </span>
              <span className={styles.timerSlotLine}>
                {activeWindowLabel}, {activeSlot.statusLabel.toLowerCase()}
              </span>
            </div>

            <div
              className={styles.posterClock}
              aria-live="off"
              aria-label={`${hrs} giờ ${mins} phút ${secs} giây`}
            >
              <span>{hrs}</span>
              <span className={styles.clockSep} aria-hidden="true">
                :
              </span>
              <span>{mins}</span>
              <span className={styles.clockSep} aria-hidden="true">
                :
              </span>
              <span>{secs}</span>
            </div>

            <div className={styles.quotaStatusStrip}>
              <span>
                Trong giỏ: {flashCountInCart}/{FLASH_SALE_RULES.maxItemsPerOrder} SP Flash sale
              </span>
            </div>
          </div>
        </div>

        <nav
          className={styles.slotGrid}
          style={{ "--slot-cols": slotCols } as React.CSSProperties}
          aria-label="Chọn khung giờ Flash sale"
        >
          {slots.map((slot) => {
            const isActive = slot.id === activeSlot.id;
            return (
              <button
                key={slot.id}
                type="button"
                className={`${styles.slotBtn} ${isActive ? styles.slotBtnActive : ""}`}
                onClick={() => handleSelectSlot(slot.id)}
                aria-pressed={isActive}
              >
                <span className={styles.slotTopRow}>
                  <span className={styles.slotTime}>{slot.label}</span>
                  <span className={styles.slotStatus}>{slot.statusLabel}</span>
                </span>
                <span className={styles.slotCount}>
                  {slot.items.length} SP, tối đa {slot.maxItemsPerOrder ?? 3}/đơn
                </span>
              </button>
            );
          })}
        </nav>
      </section>

      <section className={styles.itemsSection} aria-labelledby="flash-slot-heading">
        <div className={styles.sectionHeader}>
          <h2 id="flash-slot-heading" className={styles.sectionTitle}>
            Khung {activeSlot.startTime} ({activeWindowLabel})
          </h2>
          <span className={styles.sectionMeta}>
            {renderableItems.length} sản phẩm, tối đa {FLASH_SALE_RULES.maxItemsPerOrder} SP/đơn
          </span>
        </div>

        {renderableItems.length === 0 ? (
          <StateView
            kind="empty"
            title="Khung giờ này chưa có sản phẩm ưu đãi"
            message={`Chọn khung giờ khác phía trên hoặc xem toàn bộ sản phẩm của ${brandName}.`}
            actionLabel="Xem sản phẩm"
            actionHref="/san-pham"
          />
        ) : (
          <div className={gridVariantClass}>
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
                <article
                  key={item.id}
                  className={`${styles.card} ${singleLayout ? styles.cardWide : ""}`}
                >
                  <div className={`${styles.mediaWrap} ${hoverImage ? styles.hasHoverImage : ""}`}>
                    <Link href={`/san-pham/${product.slug}`} className={styles.mediaLink}>
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
                        className={styles.mediaPrimary}
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
                          className={styles.mediaHover}
                        />
                      )}
                    </Link>
                  </div>

                  <div className={styles.cardBody}>
                    <div className={styles.cardMetaRow}>
                      <span className={styles.flashTag}>Flash sale</span>
                      {discountTag && <span className={styles.discountTag}>{discountTag}</span>}
                    </div>

                    <Link href={`/san-pham/${product.slug}`} className={styles.cardName}>
                      {product.name}
                    </Link>

                    <Price
                      amount={flashPrice}
                      originalAmount={originalPrice}
                      size="md"
                      showDiscount={false}
                    />

                    <div className={styles.quotaWrap}>
                      <div className={styles.quotaTop}>
                        <span className={styles.quotaRemain}>
                          {activeSlot.status === "ended"
                            ? `Đã kết thúc (${totalQuota} suất)`
                            : activeSlot.status === "upcoming"
                              ? `${totalQuota} suất lúc ${activeSlot.startTime}`
                              : `Còn ${liveRemaining}/${totalQuota} suất`}
                        </span>
                        {limitPerBuyer > 0 && (
                          <span className={styles.quotaLimit}>Tối đa {limitPerBuyer}/mẫu</span>
                        )}
                      </div>
                      <div
                        className={styles.quotaTrack}
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={totalQuota}
                        aria-valuenow={totalQuota - liveRemaining}
                        aria-label={`Tiến độ suất ưu đãi ${product.shortName}`}
                      >
                        <div
                          className={`${styles.quotaFill} ${activeSlot.status === "ended" ? styles.quotaFillEnded : ""}`}
                          style={{
                            width: `${activeSlot.status === "upcoming" ? 0 : soldRatio}%`,
                          }}
                        />
                      </div>
                    </div>

                    <div className={styles.actionRow}>
                      {activeSlot.status === "live" && (
                        <>
                          <div
                            className={styles.sizePills}
                            role="group"
                            aria-label={`Chọn phân loại ưu đãi ${product.shortName}`}
                          >
                            {skuOptions.map((opt) => {
                              const optClaimed = claimedCounts[opt.detail.id] ?? 0;
                              const optDisabled =
                                opt.sku.stock <= 0 || opt.detail.remainingQuota - optClaimed <= 0;
                              const active = chosenOption?.skuId === opt.skuId;
                              return (
                                <button
                                  key={opt.skuId}
                                  type="button"
                                  disabled={optDisabled}
                                  className={`${styles.sizeBtn} ${active ? styles.sizeBtnActive : ""}`}
                                  onClick={() =>
                                    setSelectedSkuIds((prev) => ({ ...prev, [item.id]: opt.skuId }))
                                  }
                                  aria-pressed={active}
                                >
                                  {opt.label}
                                </button>
                              );
                            })}
                          </div>

                          <Button
                            variant="primary"
                            size="md"
                            fullWidth
                            disabled={
                              !chosenOption ||
                              liveRemaining <= 0 ||
                              skuOutOfStock ||
                              busyKey === item.id
                            }
                            onClick={() => handleQuickFlashBuy(item, chosenOption, liveRemaining)}
                          >
                            {liveRemaining <= 0
                              ? "Đã hết suất"
                              : skuOutOfStock
                                ? "Tạm hết hàng"
                                : busyKey === item.id
                                  ? "Đang thêm..."
                                  : `Mua ưu đãi (${chosenOption?.label ?? ""})`}
                          </Button>
                        </>
                      )}

                      {activeSlot.status === "upcoming" && (
                        <Button
                          variant={isReminded ? "ink" : "secondary"}
                          size="md"
                          fullWidth
                          onClick={() =>
                            handleToggleRemind(item.id, product.shortName, activeSlot.startTime)
                          }
                        >
                          {isReminded
                            ? `Đã bật nhắc lúc ${activeSlot.startTime}`
                            : `Nhắc tôi lúc ${activeSlot.startTime}`}
                        </Button>
                      )}

                      {activeSlot.status === "ended" && (
                        <Button
                          variant="secondary"
                          size="md"
                          fullWidth
                          href={`/san-pham/${product.slug}`}
                        >
                          Xem giá thường
                        </Button>
                      )}
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
