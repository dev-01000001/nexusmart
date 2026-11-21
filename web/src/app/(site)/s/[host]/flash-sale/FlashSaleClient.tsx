"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { FLASH_SALE_RULES, type FlashSaleItem, type FlashSaleSlot } from "@/data/flash-sale";
import { getSku, type Product, type SizeCode } from "@/data/products";
import { pickDefaultFlashSlot, secondsUntilSlotEdge } from "@/lib/catalog-map";
import { formatDiscountPercent } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, Price, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface FlashSaleClientProps {
  brandName: string;
  slots: FlashSaleSlot[];
  products: Product[];
  loadError: string | null;
  serverNowMs: number;
}

function twoDigit(n: number): string {
  const safe = Math.max(0, Math.trunc(n));
  return safe < 10 ? `0${safe}` : `${safe}`;
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
  const rootRef = useRef<HTMLDivElement>(null);

  const { addToCart, cartItems, flashSaleItemBySku, showToast } = useStore();

  const stateParam = searchParams.get("state");
  const slotQuery = searchParams.get("slot");

  const productById = useMemo(() => new Map(products.map((p) => [p.id, p])), [products]);
  const defaultSlot = useMemo(() => pickDefaultFlashSlot(slots, slotQuery), [slots, slotQuery]);

  const [activeSlotId, setActiveSlotId] = useState<string>(defaultSlot?.id ?? "");
  const [nowOffsetSec, setNowOffsetSec] = useState<number>(0);
  const [claimedCounts, setClaimedCounts] = useState<Record<string, number>>({});
  const [remindedIds, setRemindedIds] = useState<Record<string, boolean>>({});
  const [selectedSizes, setSelectedSizes] = useState<Record<string, SizeCode>>({});
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

  const secondsLeft = useMemo(
    () => secondsUntilSlotEdge(activeSlot, serverNowMs + nowOffsetSec * 1000),
    [activeSlot, nowOffsetSec, serverNowMs]
  );

  const flashCountInCart = useMemo(
    () =>
      cartItems
        .filter((it) => Boolean(flashSaleItemBySku[it.sku_id]))
        .reduce((sum, it) => sum + it.quantity, 0),
    [cartItems, flashSaleItemBySku]
  );

  useRevealObserver(rootRef, `${activeSlot?.id ?? "none"}|${stateParam ?? ""}`);

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
          message={loadError ?? "Hệ thống ưu đãi tạm gián đoạn. Vui lòng thử tải lại."}
          actionLabel="Tải lại Flash sale"
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
          title={`${brandName} hiện chưa mở khung giờ Flash sale`}
          message={`Bạn có thể xem toàn bộ sản phẩm hiện có hoặc lưu mã giảm giá riêng của ${brandName}.`}
          actionLabel="Xem sản phẩm cửa hàng"
          actionHref="/san-pham"
        />
      </div>
    );
  }

  const handleSelectSlot = (id: string) => {
    setActiveSlotId(id);
    router.replace(`/flash-sale?slot=${encodeURIComponent(id)}`, { scroll: false });
  };

  const handleQuickFlashBuy = async (item: FlashSaleItem, liveRemaining: number) => {
    if (busyKey) return;
    const product = productById.get(item.productId);
    if (!product) return;
    if (flashCountInCart >= FLASH_SALE_RULES.maxItemsPerOrder) {
      showToast(
        `Mỗi đơn Flash sale chỉ chọn tối đa ${FLASH_SALE_RULES.maxItemsPerOrder} sản phẩm ưu đãi.`
      );
      return;
    }
    const already = claimedCounts[item.id] ?? 0;
    if (already >= item.limitPerBuyer) {
      showToast(`Bạn đã chọn tối đa ${item.limitPerBuyer} suất cho sản phẩm này.`);
      return;
    }
    if (liveRemaining <= 0) {
      showToast("Suất ưu đãi của sản phẩm này đã hết.");
      return;
    }
    const firstColor = product.colors[0]?.code ?? "";
    const defaultSize =
      selectedSizes[item.id] ??
      product.skus.find((s) => s.colorCode === firstColor && s.stock > 0 && s.skuId)?.size ??
      product.sizes[0];
    const sku = getSku(product, firstColor, defaultSize);
    if (!sku || sku.stock <= 0 || !sku.skuId) {
      showToast(`Size ${defaultSize} hiện đã hết hàng.`);
      return;
    }

    setBusyKey(item.id);
    try {
      const res = await addToCart({
        skuId: sku.skuId,
        quantity: 1,
        flashSaleItemId: item.itemIdBySku?.[sku.skuId] ?? item.id,
        productName: product.shortName,
        size: defaultSize,
      });
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent("/flash-sale")}`);
        return;
      }
      if (res.ok) {
        setClaimedCounts((prev) => ({ ...prev, [item.id]: already + 1 }));
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

  return (
    <div ref={rootRef} className={styles.page}>
      <section className={styles.heroBand} aria-label={`Flash sale của ${brandName}`}>
        <div className={styles.headerGrid}>
          <div className={styles.titleBlock}>
            <h1 className={`${styles.title} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Flash sale · {brandName}
                </span>
              </span>
            </h1>
            <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              Khung giờ giá tốt trực tiếp từ kho của {brandName} với số suất cập nhật theo thời gian
              thực.
            </p>
            <div
              className={styles.heroActionRow}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <a href="#flash-slot-heading" className={styles.heroBtnPrimary}>
                Khung {activeSlot.label}
              </a>
              <Link href="/voucher" className={styles.heroBtnDark}>
                Mã giảm giá cửa hàng
              </Link>
              <Link href="/san-pham" className={styles.heroBtnDark}>
                Tất cả sản phẩm
              </Link>
            </div>
          </div>

          <div className={styles.timerBox} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            <div className={styles.timerClockRow} aria-live="off">
              <span className={styles.timerHeading}>
                {activeSlot.status === "live"
                  ? "Kết thúc sau"
                  : activeSlot.status === "upcoming"
                    ? "Mở bán sau"
                    : "Đã kết thúc"}
              </span>
              <span
                className={styles.timerBadge}
                aria-label={`${hrs} giờ ${mins} phút ${secs} giây`}
              >
                {`${hrs}:${mins}:${secs}`}
              </span>
            </div>

            <span className={styles.timerSlotLine}>
              Khung {activeSlot.startTime} – {activeSlot.endTime} · {activeSlot.statusLabel}
            </span>

            <div className={styles.quotaStatusStrip}>
              <span>
                Flash sale trong giỏ: {flashCountInCart}/{FLASH_SALE_RULES.maxItemsPerOrder} SP
              </span>
            </div>
          </div>
        </div>

        <nav className={styles.slotGrid} aria-label="Chọn khung giờ Flash sale">
          {slots.map((slot, idx) => {
            const isActive = slot.id === activeSlot.id;
            return (
              <button
                key={slot.id}
                type="button"
                className={`${styles.slotBtn} ${isActive ? styles.slotBtnActive : ""}`}
                onClick={() => handleSelectSlot(slot.id)}
                aria-pressed={isActive}
                data-cat-reveal=""
                style={{ "--stagger": idx } as React.CSSProperties}
              >
                <span className={styles.slotTopRow}>
                  <span className={styles.slotTime}>{slot.label}</span>
                  <span className={styles.slotStatus}>{slot.statusLabel}</span>
                </span>
                <span className={styles.slotCount}>
                  {slot.items.length} SP · tối đa {slot.maxItemsPerOrder ?? 3}/đơn
                </span>
              </button>
            );
          })}
        </nav>
      </section>

      <section className={styles.itemsSection} aria-labelledby="flash-slot-heading">
        <div
          className={styles.sectionHeader}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="flash-slot-heading" className={styles.sectionTitle}>
            Khung {activeSlot.startTime} · {activeSlot.statusLabel}
          </h2>
          <span className={styles.sectionMeta}>
            {activeSlot.items.length} sản phẩm · Tối đa {FLASH_SALE_RULES.maxItemsPerOrder} SP/đơn
          </span>
        </div>

        {activeSlot.items.length === 0 ? (
          <StateView
            kind="empty"
            title="Khung giờ này chưa có sản phẩm ưu đãi"
            message={`Bạn có thể chọn khung giờ khác ở phía trên hoặc khám phá toàn bộ sản phẩm của ${brandName}.`}
            actionLabel="Xem sản phẩm cửa hàng"
            actionHref="/san-pham"
          />
        ) : (
          <div className={styles.cardsGrid}>
            {activeSlot.items.map((item, idx) => {
              const product = productById.get(item.productId);
              if (!product) return null;
              const claimed = claimedCounts[item.id] ?? 0;
              const liveRemaining = Math.max(0, item.remainingQuota - claimed);
              const soldRatio =
                item.totalQuota > 0
                  ? Math.min(
                      100,
                      Math.round(((item.totalQuota - liveRemaining) / item.totalQuota) * 100)
                    )
                  : 100;

              const firstColor = product.colors[0]?.code ?? "";
              const chosenSize: SizeCode =
                selectedSizes[item.id] ??
                product.skus.find((s) => s.colorCode === firstColor && s.stock > 0 && s.skuId)
                  ?.size ??
                product.sizes[0];
              const chosenSku = getSku(product, firstColor, chosenSize);
              const skuOutOfStock = !chosenSku || chosenSku.stock <= 0 || !chosenSku.skuId;
              const isReminded = Boolean(remindedIds[item.id]);
              const hoverImage = product.onModelImage ?? product.gallery[1];
              const discountTag = formatDiscountPercent(item.originalPrice, item.flashPrice);

              return (
                <article
                  key={item.id}
                  className={styles.card}
                  data-cat-reveal=""
                  style={{ "--stagger": idx % 4 } as React.CSSProperties}
                >
                  <div className={`${styles.mediaWrap} ${hoverImage ? styles.hasHoverImage : ""}`}>
                    <Link href={`/san-pham/${product.slug}`} className={styles.mediaLink}>
                      <Image
                        src={product.image}
                        alt={product.name}
                        fill
                        sizes="(max-width: 1023px) 50vw, 25vw"
                        priority={idx < 4}
                        className={styles.mediaPrimary}
                      />
                      {hoverImage && (
                        <Image
                          src={hoverImage}
                          alt={`${product.name} khi mặc thực tế`}
                          fill
                          sizes="(max-width: 1023px) 50vw, 25vw"
                          className={styles.mediaHover}
                        />
                      )}
                    </Link>
                    {discountTag && <span className={styles.flashBadge}>{discountTag}</span>}
                  </div>

                  <div className={styles.cardBody}>
                    <span className={styles.cardShop}>
                      {brandName}
                      {product.gsm > 0 ? ` · ${product.gsm}gsm` : ""}
                    </span>
                    <Link href={`/san-pham/${product.slug}`} className={styles.cardName}>
                      {product.name}
                    </Link>

                    <Price
                      amount={item.flashPrice}
                      originalAmount={item.originalPrice}
                      size="md"
                      showDiscount={false}
                    />

                    <div className={styles.quotaWrap}>
                      <div className={styles.quotaTop}>
                        <span className={styles.quotaRemain}>
                          {activeSlot.status === "ended"
                            ? `Đã kết thúc (${item.totalQuota} suất)`
                            : activeSlot.status === "upcoming"
                              ? `${item.totalQuota} suất lúc ${activeSlot.startTime}`
                              : `Còn ${liveRemaining}/${item.totalQuota} suất`}
                        </span>
                        <span className={styles.quotaLimit}>Tối đa {item.limitPerBuyer}/mẫu</span>
                      </div>
                      <div
                        className={styles.quotaTrack}
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={item.totalQuota}
                        aria-valuenow={item.totalQuota - liveRemaining}
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
                            aria-label={`Chọn size ${product.shortName}`}
                          >
                            {product.sizes.map((sz) => {
                              const sku = getSku(product, firstColor, sz);
                              const outOfStock = !sku || sku.stock <= 0 || !sku.skuId;
                              const active = chosenSize === sz;
                              return (
                                <button
                                  key={sz}
                                  type="button"
                                  disabled={outOfStock}
                                  className={`${styles.sizeBtn} ${active ? styles.sizeBtnActive : ""}`}
                                  onClick={() =>
                                    setSelectedSizes((prev) => ({ ...prev, [item.id]: sz }))
                                  }
                                  aria-pressed={active}
                                >
                                  {sz}
                                </button>
                              );
                            })}
                          </div>

                          <Button
                            variant="primary"
                            size="md"
                            fullWidth
                            disabled={liveRemaining <= 0 || skuOutOfStock || busyKey === item.id}
                            onClick={() => handleQuickFlashBuy(item, liveRemaining)}
                          >
                            {liveRemaining <= 0
                              ? "Đã hết suất"
                              : skuOutOfStock
                                ? "Tạm hết hàng"
                                : busyKey === item.id
                                  ? "Đang thêm..."
                                  : `Mua ưu đãi (${chosenSize})`}
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
