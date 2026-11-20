"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  COMBO_OFFERS,
  FLASH_SALE_RULES,
  SHOCK_DEALS,
  getSku,
  type ComboOffer,
  type FlashSaleItem,
  type FlashSaleSlot,
  type Product,
  type Shop,
  type SizeCode,
} from "@/data";
import { pickDefaultFlashSlot, secondsUntilSlotEdge } from "@/lib/catalog-map";
import { formatDiscountPercent } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, Price, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface FlashSaleClientProps {
  slots: FlashSaleSlot[];
  products: Product[];
  shops: Shop[];
  loadError: string | null;
  serverNowMs: number;
}

function twoDigit(n: number): string {
  const safe = Math.max(0, Math.trunc(n));
  return safe < 10 ? `0${safe}` : `${safe}`;
}

export function FlashSaleClient({
  slots,
  products,
  shops,
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
  const shopById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

  const defaultSlot = useMemo(() => pickDefaultFlashSlot(slots, slotQuery), [slots, slotQuery]);

  const [activeSlotId, setActiveSlotId] = useState<string>(defaultSlot?.id ?? "");
  const [nowOffsetSec, setNowOffsetSec] = useState<number>(0);
  const [claimedCounts, setClaimedCounts] = useState<Record<string, number>>({});
  const [shockClaimedIds, setShockClaimedIds] = useState<Record<string, boolean>>({});
  const [remindedIds, setRemindedIds] = useState<Record<string, boolean>>({});
  const [selectedSizes, setSelectedSizes] = useState<Record<string, SizeCode>>({});
  const [comboSizes, setComboSizes] = useState<Record<string, SizeCode>>({});
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

  const shockCountInCart = useMemo(
    () => Object.values(shockClaimedIds).filter(Boolean).length,
    [shockClaimedIds]
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
          title="Chưa có chương trình Flash sale trong khung giờ này"
          message="Bạn có thể xem toàn bộ sản phẩm giá xưởng trong danh mục chính hoặc lưu mã ưu đãi."
          actionLabel="Xem danh mục sản phẩm"
          actionHref="/danh-muc"
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

  const handleShockDealBuy = async (dealId: string, productId: string, remaining: number) => {
    if (busyKey) return;
    const product = productById.get(productId);
    if (!product) return;
    if (remaining <= 0) {
      showToast("Suất Deal sốc này đã hết.");
      return;
    }
    const firstColor = product.colors[0]?.code ?? "";
    const chosenSize =
      selectedSizes[dealId] ??
      product.skus.find((s) => s.colorCode === firstColor && s.stock > 0 && s.skuId)?.size ??
      product.sizes[0];
    const sku = getSku(product, firstColor, chosenSize);
    if (!sku || sku.stock <= 0 || !sku.skuId) {
      showToast(`Size ${chosenSize} hiện đã hết hàng.`);
      return;
    }

    setBusyKey(dealId);
    try {
      const res = await addToCart({
        skuId: sku.skuId,
        quantity: 1,
        productName: product.shortName,
        size: chosenSize,
      });
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent("/flash-sale")}`);
        return;
      }
      if (res.ok) {
        setShockClaimedIds((prev) => ({ ...prev, [dealId]: true }));
      }
    } finally {
      setBusyKey(null);
    }
  };

  const handleAddComboOffer = async (offer: ComboOffer) => {
    if (busyKey) return;
    const comboProds = offer.productIds
      .map((id) => productById.get(id))
      .filter((p): p is Product => Boolean(p));
    if (comboProds.length === 0) return;

    setBusyKey(offer.id);
    try {
      let addedCount = 0;
      for (const prod of comboProds) {
        const key = `${offer.id}-${prod.id}`;
        const firstColor = prod.colors[0]?.code ?? "";
        const chosenSize =
          comboSizes[key] ??
          prod.skus.find((s) => s.colorCode === firstColor && s.stock > 0 && s.skuId)?.size ??
          prod.sizes[0];
        const sku = getSku(prod, firstColor, chosenSize);
        if (!sku || sku.stock <= 0 || !sku.skuId) continue;

        const res = await addToCart({
          skuId: sku.skuId,
          quantity: 1,
          productName: prod.shortName,
          size: chosenSize,
        });
        if (res.unauthenticated) {
          router.push(`/dang-nhap?next=${encodeURIComponent("/flash-sale")}`);
          return;
        }
        if (res.ok) addedCount += 1;
      }
      if (addedCount > 0) {
        showToast(`Đã thêm ${addedCount} sản phẩm trong ${offer.title} vào giỏ hàng.`);
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

  const shockDeals = SHOCK_DEALS.filter((d) => productById.has(d.productId));
  const comboOffers = COMBO_OFFERS.filter((c) => c.productIds.some((id) => productById.has(id)));

  return (
    <div ref={rootRef} className={styles.page}>
      {/* Strong dark slate banner with integrated live countdown and slot bar */}
      <section className={styles.heroBand} aria-label="Tổng quan Flash sale và Deal sốc">
        <div className={styles.headerGrid}>
          <div className={styles.titleBlock}>
            <h1 className={`${styles.title} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Flash sale &amp; Deal sốc
                </span>
              </span>
            </h1>
            <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              Khung giờ giá tốt mỗi ngày từ các xưởng dệt Việt Nam với số suất ưu đãi cập nhật trực
              tiếp, kèm Deal sốc giới hạn và Combo tiết kiệm.
            </p>
            <div
              className={styles.heroActionRow}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <a href="#flash-slot-heading" className={styles.heroBtnPrimary}>
                Khung {activeSlot.label}
              </a>
              <a href="#shock-deals-heading" className={styles.heroBtnDark}>
                Săn Deal Sốc
              </a>
              <Link href="/voucher" className={styles.heroBtnDark}>
                Kho Voucher
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
              <span>
                Deal sốc đã chọn: {shockCountInCart}/{FLASH_SALE_RULES.shockDealMaxPerOrder} SP
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

      {/* Section 1: 4-column Flash Sale grid */}
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
            message="Bạn có thể chọn khung giờ khác ở phía trên hoặc khám phá toàn bộ danh mục."
            actionLabel="Xem danh mục sản phẩm"
            actionHref="/danh-muc"
          />
        ) : (
          <div className={styles.cardsGrid}>
            {activeSlot.items.map((item, idx) => {
              const product = productById.get(item.productId);
              if (!product) return null;
              const shop = shopById.get(product.shopId);
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
                      {shop?.shortName ?? "NexusMart"}
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

      {/* Section 2: Shock Deals (curated editorial deals wired to real product SKUs) */}
      {shockDeals.length > 0 && (
        <section className={styles.shockSection} aria-labelledby="shock-deals-heading">
          <div
            className={styles.sectionHeader}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            <h2 id="shock-deals-heading" className={styles.sectionTitle}>
              Săn Deal Sốc hôm nay
            </h2>
            <span className={styles.sectionMeta}>
              Giới hạn {FLASH_SALE_RULES.shockDealMaxPerOrder} SP/lần · Tối đa{" "}
              {FLASH_SALE_RULES.shockDealMaxPerDay} suất/ngày · Không áp dụng đổi trả
            </span>
          </div>

          <div className={styles.shockGrid}>
            {shockDeals.map((deal, idx) => {
              const product = productById.get(deal.productId);
              if (!product) return null;
              const shop = shopById.get(product.shopId);
              const alreadyClaimed = Boolean(shockClaimedIds[deal.id]);
              const liveRemaining = Math.max(0, deal.remainingQuota - (alreadyClaimed ? 1 : 0));
              const soldRatio = Math.min(
                100,
                Math.round(((deal.totalQuota - liveRemaining) / Math.max(1, deal.totalQuota)) * 100)
              );
              const firstColor = product.colors[0]?.code ?? "";
              const chosenSize: SizeCode =
                selectedSizes[deal.id] ??
                product.skus.find((s) => s.colorCode === firstColor && s.stock > 0 && s.skuId)
                  ?.size ??
                product.sizes[0];
              const chosenSku = getSku(product, firstColor, chosenSize);
              const skuOutOfStock = !chosenSku || chosenSku.stock <= 0 || !chosenSku.skuId;
              const hoverImage = product.onModelImage ?? product.gallery[1];
              const discountTag = formatDiscountPercent(
                product.originalPrice ?? product.price,
                product.price
              );

              return (
                <article
                  key={deal.id}
                  className={styles.shockCard}
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
                    {discountTag && <span className={styles.shockBadge}>{discountTag}</span>}
                  </div>

                  <div className={styles.cardBody}>
                    <span className={styles.cardShop}>
                      {shop?.shortName ?? "NexusMart"} · Tối đa {deal.limitPerOrder} SP/đơn
                    </span>
                    <Link href={`/san-pham/${product.slug}`} className={styles.cardName}>
                      {product.name}
                    </Link>

                    <Price
                      amount={product.price}
                      originalAmount={product.originalPrice}
                      size="md"
                      showDiscount={false}
                    />

                    <div className={styles.quotaWrap}>
                      <div className={styles.quotaTop}>
                        <span className={styles.quotaRemain}>
                          Còn {liveRemaining}/{deal.totalQuota} suất
                        </span>
                        <span className={styles.quotaLimit}>{deal.limitPerDay} suất/ngày</span>
                      </div>
                      <div
                        className={styles.quotaTrack}
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={deal.totalQuota}
                        aria-valuenow={deal.totalQuota - liveRemaining}
                        aria-label={`Suất Deal sốc ${product.shortName}`}
                      >
                        <div className={styles.quotaFill} style={{ width: `${soldRatio}%` }} />
                      </div>
                    </div>

                    <span className={styles.dealNote}>{deal.note}</span>

                    <div className={styles.actionRow}>
                      <div
                        className={styles.sizePills}
                        role="group"
                        aria-label={`Chọn size deal sốc ${product.shortName}`}
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
                              className={`${styles.sizeBtn} ${styles.sizeBtnOnWhite} ${
                                active ? styles.sizeBtnActive : ""
                              }`}
                              onClick={() =>
                                setSelectedSizes((prev) => ({ ...prev, [deal.id]: sz }))
                              }
                              aria-pressed={active}
                            >
                              {sz}
                            </button>
                          );
                        })}
                      </div>

                      <Button
                        variant={alreadyClaimed ? "ink" : "primary"}
                        size="md"
                        fullWidth
                        disabled={liveRemaining <= 0 || skuOutOfStock || busyKey === deal.id}
                        onClick={() => handleShockDealBuy(deal.id, product.id, liveRemaining)}
                      >
                        {skuOutOfStock
                          ? "Tạm hết hàng"
                          : alreadyClaimed
                            ? "Đã thêm vào giỏ"
                            : `Săn Deal (${chosenSize})`}
                      </Button>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}

      {/* Section 3: Combo Offers (wired to real product SKUs) */}
      {comboOffers.length > 0 && (
        <section className={styles.comboSection} aria-labelledby="combo-offers-heading">
          <div
            className={styles.sectionHeader}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            <h2 id="combo-offers-heading" className={styles.sectionTitle}>
              Combo &amp; Mua nhiều giảm thêm
            </h2>
            <span className={styles.sectionMeta}>
              Đóng gói cùng 1 xưởng · Dễ dàng gộp đơn áp mã giảm giá gian hàng
            </span>
          </div>

          <div className={styles.comboGrid}>
            {comboOffers.map((offer, idx) => {
              const shop = shopById.get(offer.shopId);
              const comboProds = offer.productIds
                .map((id) => productById.get(id))
                .filter((p): p is Product => Boolean(p));
              const realTotal = comboProds.reduce((sum, p) => sum + p.price, 0);

              return (
                <article
                  key={offer.id}
                  className={styles.comboCard}
                  data-cat-reveal=""
                  style={{ "--stagger": idx % 2 } as React.CSSProperties}
                >
                  <div className={styles.comboMediaRow}>
                    {comboProds.map((prod) => (
                      <Link
                        key={prod.id}
                        href={`/san-pham/${prod.slug}`}
                        className={styles.comboThumbLink}
                      >
                        <Image
                          src={prod.onModelImage ?? prod.image}
                          alt={prod.name}
                          fill
                          sizes="(max-width: 1023px) 45vw, 18vw"
                          className={styles.comboThumbImg}
                        />
                      </Link>
                    ))}
                  </div>

                  <div className={styles.comboBody}>
                    <h3 className={styles.comboTitle}>{offer.title}</h3>
                    <div className={styles.comboMetaRow}>
                      <span className={styles.comboSaveTag}>{offer.discountLabel}</span>
                      <span className={styles.cardShop}>{shop?.shortName ?? "NexusMart"}</span>
                    </div>

                    <Price amount={realTotal} size="md" showDiscount={false} />

                    <div className={styles.comboSelectors}>
                      {comboProds.map((prod) => {
                        const key = `${offer.id}-${prod.id}`;
                        const firstColor = prod.colors[0]?.code ?? "";
                        const chosenSize: SizeCode =
                          comboSizes[key] ??
                          prod.skus.find(
                            (s) => s.colorCode === firstColor && s.stock > 0 && s.skuId
                          )?.size ??
                          prod.sizes[0];
                        return (
                          <div key={prod.id} className={styles.comboItemRow}>
                            <span className={styles.comboItemName}>{prod.shortName}</span>
                            <div
                              className={styles.sizePills}
                              role="group"
                              aria-label={`Chọn cỡ cho ${prod.shortName}`}
                            >
                              {prod.sizes.map((sz) => {
                                const sku = getSku(prod, firstColor, sz);
                                const outOfStock = !sku || sku.stock <= 0 || !sku.skuId;
                                const active = chosenSize === sz;
                                return (
                                  <button
                                    key={sz}
                                    type="button"
                                    disabled={outOfStock}
                                    className={`${styles.sizeBtn} ${styles.sizeBtnOnWhite} ${
                                      active ? styles.sizeBtnActive : ""
                                    }`}
                                    onClick={() =>
                                      setComboSizes((prev) => ({ ...prev, [key]: sz }))
                                    }
                                    aria-pressed={active}
                                  >
                                    {sz}
                                  </button>
                                );
                              })}
                            </div>
                          </div>
                        );
                      })}
                    </div>

                    <span className={styles.dealNote}>{offer.note}</span>

                    <div className={styles.comboCtaWrap}>
                      <Button
                        variant="primary"
                        size="md"
                        fullWidth
                        disabled={busyKey === offer.id}
                        onClick={() => handleAddComboOffer(offer)}
                      >
                        {busyKey === offer.id ? "Đang thêm..." : "Thêm trọn bộ vào giỏ hàng"}
                      </Button>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}

      {/* Section 4: Unboxed rules band */}
      <section className={styles.rulesSection} aria-labelledby="flash-rules-heading">
        <h2 id="flash-rules-heading" className={styles.rulesHeading}>
          Quy tắc ưu đãi Flash sale &amp; Deal sốc
        </h2>
        <div className={styles.rulesList}>
          <p>
            Mỗi đơn hàng Flash sale được chọn tối đa {FLASH_SALE_RULES.maxItemsPerOrder} sản phẩm ưu
            đãi. Suất giảm giá được giữ ngay khi bạn đặt hàng thành công để đảm bảo công bằng trong
            suốt khung giờ mở bán.
          </p>
          <p>
            Suất Săn Deal Sốc giới hạn {FLASH_SALE_RULES.shockDealMaxPerOrder} sản phẩm mỗi đơn và
            tối đa {FLASH_SALE_RULES.shockDealMaxPerDay} lượt mỗi ngày. Deal sốc không áp dụng đổi
            trả 45 ngày, trong khi các combo tiết kiệm vẫn được đổi size miễn phí và freeship từ
            249.000₫.
          </p>
        </div>
      </section>
    </div>
  );
}
