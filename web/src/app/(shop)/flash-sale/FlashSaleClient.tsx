"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import type { SizeCode } from "@/data";
import {
  findProductSku,
  type CatalogFlashSaleItem,
  type CatalogFlashSaleSlot,
  type CatalogProduct,
  type CatalogShop,
} from "@/lib/catalog-types";
import { formatDiscountPercent } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import {
  FLASH_SALE_RULES,
  getMockFlashExtras,
  type MockComboOfferRef,
} from "@/mock/productEditorial";
import { Button, Price, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface FlashSaleClientProps {
  flashSlots: CatalogFlashSaleSlot[];
  products: CatalogProduct[];
  shops: CatalogShop[];
  serverNowMs: number;
  error: string | null;
}

function twoDigit(n: number): string {
  const safe = Math.max(0, Math.trunc(n));
  return safe < 10 ? `0${safe}` : `${safe}`;
}

function computeSlotRemainingSeconds(
  slot: CatalogFlashSaleSlot | undefined,
  nowMs: number
): number {
  if (!slot) return 0;
  const targetIso = slot.status === "upcoming" ? slot.startsAt : slot.endsAt;
  const targetMs = targetIso ? Date.parse(targetIso) : 0;
  if (!Number.isFinite(targetMs) || targetMs <= nowMs) return 0;
  return Math.max(0, Math.floor((targetMs - nowMs) / 1000));
}

function getCurrentReturnPath(fallbackPathname: string): string {
  if (typeof window !== "undefined") {
    return `${window.location.pathname}${window.location.search}`;
  }
  return fallbackPathname || "/flash-sale";
}

export function FlashSaleClient({
  flashSlots,
  products,
  shops,
  serverNowMs,
  error,
}: FlashSaleClientProps) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const rootRef = useRef<HTMLDivElement>(null);

  const { isAuthenticated } = useSession();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();

  const stateParam = searchParams.get("state");
  const slotQuery = searchParams.get("slot");

  const defaultSlot = useMemo(() => {
    if (slotQuery) {
      const matched = flashSlots.find((s) => s.id === slotQuery);
      if (matched) return matched;
    }
    return flashSlots.find((s) => s.status === "live") ?? flashSlots[0];
  }, [flashSlots, slotQuery]);

  const [activeSlotId, setActiveSlotId] = useState<string>(defaultSlot?.id ?? "");
  const [remindedIds, setRemindedIds] = useState<Record<string, boolean>>({});
  const [shockClaimedIds, setShockClaimedIds] = useState<Record<string, boolean>>({});
  const [selectedSizes, setSelectedSizes] = useState<Record<string, SizeCode>>({});
  const [comboSizes, setComboSizes] = useState<Record<string, SizeCode>>({});
  const [addingKey, setAddingKey] = useState<string | null>(null);

  const activeSlot = useMemo(
    () => flashSlots.find((s) => s.id === activeSlotId) ?? defaultSlot,
    [flashSlots, activeSlotId, defaultSlot]
  );

  const [secondsLeft, setSecondsLeft] = useState<number>(() =>
    computeSlotRemainingSeconds(activeSlot, serverNowMs)
  );

  useEffect(() => {
    setSecondsLeft(computeSlotRemainingSeconds(activeSlot, Date.now()));
    if (!activeSlot) return;
    const id = window.setInterval(() => {
      setSecondsLeft(computeSlotRemainingSeconds(activeSlot, Date.now()));
    }, 1000);
    return () => window.clearInterval(id);
  }, [activeSlot]);

  const productsBySlug = useMemo(() => new Map(products.map((p) => [p.slug, p])), [products]);
  const shopsById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);
  const shopsBySlug = useMemo(() => new Map(shops.map((s) => [s.slug, s])), [shops]);

  // Editorial mock sections (Deal sốc & Combo) resolved onto real backend products by slug
  const mockExtras = useMemo(() => getMockFlashExtras(), []);

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

  if (stateParam === "error" || (error && flashSlots.length === 0)) {
    return (
      <div className={styles.stateWrap}>
        <StateView
          kind="error"
          title="Không thể tải phiên Flash sale"
          message={error ?? "Hệ thống ưu đãi tạm gián đoạn. Vui lòng thử tải lại."}
          actionLabel="Tải lại Flash sale"
          onAction={() => {
            if (stateParam === "error") {
              router.replace("/flash-sale");
            } else {
              router.refresh();
            }
          }}
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
          message="Bạn có thể xem toàn bộ sản phẩm giá xưởng trong danh mục chính."
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

  // Contract 2: Flash sale direct checkout via /thanh-toan?sku=<sku_id>&qty=1&flash=<flash_sale_item_id>
  const handleFlashBuyNow = (item: CatalogFlashSaleItem) => {
    if (item.remainingSlots <= 0) {
      showToast("Suất ưu đãi của sản phẩm này đã hết.");
      return;
    }
    if (item.skuAvailableStock <= 0) {
      showToast("Phân loại sản phẩm này hiện đã hết hàng.");
      return;
    }
    const checkoutUrl = `/thanh-toan?sku=${encodeURIComponent(item.skuId)}&qty=1&flash=${encodeURIComponent(item.id)}`;
    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(checkoutUrl)}`);
      return;
    }
    router.push(checkoutUrl);
  };

  // Mock Deal sốc section: adds real backend SKU to real cart (Contract 1)
  const handleShockDealAdd = async (dealId: string, product: CatalogProduct, remaining: number) => {
    if (addingKey !== null) return;
    if (remaining <= 0) {
      showToast("Suất Deal sốc này đã hết.");
      return;
    }
    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
      return;
    }

    const firstColor = product.colors[0]?.code ?? "";
    const chosenSize =
      selectedSizes[dealId] ??
      product.skus.find((s) => s.colorCode === firstColor && s.stock > 0)?.size ??
      product.sizes[0];
    const targetSku = findProductSku(product, firstColor, chosenSize);
    if (!targetSku || targetSku.stock <= 0 || !("id" in targetSku)) {
      showToast("Phân loại sản phẩm này hiện đã hết hàng.");
      return;
    }

    setAddingKey(dealId);
    try {
      const res = await addToCart({ skuId: targetSku.id, quantity: 1 });
      if (res.ok) {
        setShockClaimedIds((prev) => ({ ...prev, [dealId]: true }));
        showToast(res.message ?? `Đã thêm ${product.shortName} (${chosenSize}) vào giỏ hàng.`);
      } else {
        showToast(res.message ?? "Không thể thêm sản phẩm vào giỏ hàng lúc này.");
      }
    } finally {
      setAddingKey(null);
    }
  };

  // Mock Combo Offers section: adds each selected real backend SKU to real cart (Contract 1)
  const handleAddComboOffer = async (offer: MockComboOfferRef) => {
    if (addingKey !== null) return;
    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
      return;
    }

    const comboProducts = offer.productSlugs
      .map((slug) => productsBySlug.get(slug))
      .filter((p): p is CatalogProduct => Boolean(p));
    if (comboProducts.length === 0) return;

    setAddingKey(offer.id);
    try {
      let addedCount = 0;
      for (const prod of comboProducts) {
        const key = `${offer.id}-${prod.id}`;
        const firstColor = prod.colors[0]?.code ?? "";
        const chosenSize =
          comboSizes[key] ??
          prod.skus.find((s) => s.colorCode === firstColor && s.stock > 0)?.size ??
          prod.sizes[0];
        const sku = findProductSku(prod, firstColor, chosenSize);
        if (!sku || sku.stock <= 0 || !("id" in sku)) continue;
        const res = await addToCart({ skuId: sku.id, quantity: 1 });
        if (res.ok) addedCount += 1;
      }

      if (addedCount > 0) {
        showToast(`Đã thêm ${addedCount} sản phẩm trong ${offer.title} vào giỏ hàng.`);
      } else {
        showToast("Không thể thêm bộ sản phẩm vào giỏ hàng lúc này.");
      }
    } finally {
      setAddingKey(null);
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

  const totalRemainingInSlot = activeSlot.items.reduce((sum, it) => sum + it.remainingSlots, 0);
  const slotGridClass =
    flashSlots.length === 3
      ? `${styles.slotGrid} ${styles.slotGridThree}`
      : flashSlots.length === 2
        ? `${styles.slotGrid} ${styles.slotGridTwo}`
        : styles.slotGrid;

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
              Khung giờ giá tốt từ các xưởng dệt Việt Nam với số suất cập nhật trực tiếp, kèm Deal
              sốc giới hạn và Combo tiết kiệm.
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
              <a href="#combo-offers-heading" className={styles.heroBtnDark}>
                Combo tiết kiệm
              </a>
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
              <span>{activeSlot.items.length} sản phẩm trong khung</span>
              <span>Còn {totalRemainingInSlot} suất ưu đãi</span>
            </div>
          </div>
        </div>

        <nav className={slotGridClass} aria-label="Chọn khung giờ Flash sale">
          {flashSlots.map((slot, idx) => {
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
                  {slot.items.length} SP · {slot.startTime}–{slot.endTime}
                </span>
              </button>
            );
          })}
        </nav>
      </section>

      {/* Section 1: 4-column Flash Sale grid from real backend /flash-sales */}
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
            {activeSlot.items.length} sản phẩm · Mua trực tiếp áp giá Flash sale
          </span>
        </div>

        {activeSlot.items.length === 0 ? (
          <StateView
            kind="empty"
            title="Chưa có sản phẩm nào được mở bán trong khung giờ này"
            actionLabel="Xem danh mục sản phẩm"
            actionHref="/danh-muc"
          />
        ) : (
          <div className={styles.cardsGrid}>
            {activeSlot.items.map((item, idx) => {
              const { product } = item;
              const shop = shopsById.get(product.shopId);
              const soldRatio =
                item.totalSlots > 0
                  ? Math.min(
                      100,
                      Math.round(((item.totalSlots - item.remainingSlots) / item.totalSlots) * 100)
                    )
                  : 100;
              const isReminded = Boolean(remindedIds[item.id]);
              const hoverImage = product.onModelImage ?? product.gallery[1];
              const discountTag = formatDiscountPercent(item.originalPriceVnd, item.flashPriceVnd);
              const outOfStock = item.skuAvailableStock <= 0;
              const soldOutSlots = item.remainingSlots <= 0;

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
                      {shop?.shortName ?? product.shopShortName}
                      {product.gsm > 0 ? ` · ${product.gsm}gsm` : ""}
                    </span>
                    <Link href={`/san-pham/${product.slug}`} className={styles.cardName}>
                      {product.name}
                    </Link>

                    <span className={styles.dealNote}>
                      {item.skuColorName} · Size {item.skuSize}
                      {outOfStock ? " · Tạm hết hàng" : ` · Tồn ${item.skuAvailableStock}`}
                    </span>

                    <Price
                      amount={item.flashPriceVnd}
                      originalAmount={
                        item.originalPriceVnd > item.flashPriceVnd
                          ? item.originalPriceVnd
                          : undefined
                      }
                      size="md"
                      showDiscount={false}
                    />

                    <div className={styles.quotaWrap}>
                      <div className={styles.quotaTop}>
                        <span className={styles.quotaRemain}>
                          {activeSlot.status === "ended"
                            ? `Đã kết thúc (${item.totalSlots} suất)`
                            : activeSlot.status === "upcoming"
                              ? `${item.totalSlots} suất lúc ${activeSlot.startTime}`
                              : `Còn ${item.remainingSlots}/${item.totalSlots} suất`}
                        </span>
                        <span className={styles.quotaLimit}>Tối đa {item.perUserLimit}/mẫu</span>
                      </div>
                      <div
                        className={styles.quotaTrack}
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={item.totalSlots}
                        aria-valuenow={item.totalSlots - item.remainingSlots}
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
                        <Button
                          variant="primary"
                          size="md"
                          fullWidth
                          disabled={soldOutSlots || outOfStock}
                          onClick={() => handleFlashBuyNow(item)}
                        >
                          {soldOutSlots
                            ? "Đã hết suất"
                            : outOfStock
                              ? "Tạm hết hàng"
                              : `Mua ngay (${item.skuSize})`}
                        </Button>
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

      {/* Section 2: Shock Deals (mock editorial showcase mapped to real catalog SKUs) */}
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
          {mockExtras.shockDeals.map((deal, idx) => {
            const product = productsBySlug.get(deal.productSlug);
            if (!product) return null;
            const shop = shopsById.get(product.shopId);
            const alreadyClaimed = Boolean(shockClaimedIds[deal.id]);
            const liveRemaining = Math.max(0, deal.remainingQuota - (alreadyClaimed ? 1 : 0));
            const soldRatio = Math.min(
              100,
              Math.round(((deal.totalQuota - liveRemaining) / Math.max(1, deal.totalQuota)) * 100)
            );
            const firstColor = product.colors[0]?.code ?? "";
            const chosenSize: SizeCode =
              selectedSizes[deal.id] ??
              product.skus.find((s) => s.colorCode === firstColor && s.stock > 0)?.size ??
              product.sizes[0] ??
              "M";
            const hoverImage = product.onModelImage ?? product.gallery[1];
            const discountTag = formatDiscountPercent(deal.originalPrice, deal.dealPrice);

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
                    {shop?.shortName ?? product.shopShortName} · Tối đa {deal.limitPerOrder} SP/đơn
                  </span>
                  <Link href={`/san-pham/${product.slug}`} className={styles.cardName}>
                    {product.name}
                  </Link>

                  <Price
                    amount={deal.dealPrice}
                    originalAmount={deal.originalPrice}
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
                        const sku = findProductSku(product, firstColor, sz);
                        const outOfStock = !sku || sku.stock <= 0;
                        const active = chosenSize === sz;
                        return (
                          <button
                            key={sz}
                            type="button"
                            disabled={outOfStock}
                            className={`${styles.sizeBtn} ${styles.sizeBtnOnWhite} ${
                              active ? styles.sizeBtnActive : ""
                            }`}
                            onClick={() => setSelectedSizes((prev) => ({ ...prev, [deal.id]: sz }))}
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
                      disabled={liveRemaining <= 0 || addingKey !== null}
                      onClick={() => void handleShockDealAdd(deal.id, product, liveRemaining)}
                    >
                      {addingKey === deal.id
                        ? "Đang thêm..."
                        : alreadyClaimed
                          ? "Đã thêm vào giỏ"
                          : `Thêm giỏ (${chosenSize})`}
                    </Button>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      </section>

      {/* Section 3: 4 Combo Offers (mock editorial combos linked to real catalog SKUs) */}
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
            Đóng gói cùng 1 xưởng · Tự động giảm thêm khi mua từ 2 sản phẩm
          </span>
        </div>

        <div className={styles.comboGrid}>
          {mockExtras.comboOffers.map((offer, idx) => {
            const shop = shopsBySlug.get(offer.shopSlug);
            const comboProducts = offer.productSlugs
              .map((slug) => productsBySlug.get(slug))
              .filter((p): p is CatalogProduct => Boolean(p));
            if (comboProducts.length === 0) return null;

            return (
              <article
                key={offer.id}
                className={styles.comboCard}
                data-cat-reveal=""
                style={{ "--stagger": idx % 2 } as React.CSSProperties}
              >
                <div className={styles.comboMediaRow}>
                  {comboProducts.map((prod) => (
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

                  <Price
                    amount={offer.comboPrice}
                    originalAmount={offer.originalTotalPrice}
                    size="md"
                    showDiscount={false}
                  />

                  <div className={styles.comboSelectors}>
                    {comboProducts.map((prod) => {
                      const key = `${offer.id}-${prod.id}`;
                      const firstColor = prod.colors[0]?.code ?? "";
                      const chosenSize: SizeCode =
                        comboSizes[key] ??
                        prod.skus.find((s) => s.colorCode === firstColor && s.stock > 0)?.size ??
                        prod.sizes[0] ??
                        "M";
                      return (
                        <div key={prod.id} className={styles.comboItemRow}>
                          <span className={styles.comboItemName}>{prod.shortName}</span>
                          <div
                            className={styles.sizePills}
                            role="group"
                            aria-label={`Chọn cỡ cho ${prod.shortName}`}
                          >
                            {prod.sizes.map((sz) => {
                              const sku = findProductSku(prod, firstColor, sz);
                              const outOfStock = !sku || sku.stock <= 0;
                              const active = chosenSize === sz;
                              return (
                                <button
                                  key={sz}
                                  type="button"
                                  disabled={outOfStock}
                                  className={`${styles.sizeBtn} ${styles.sizeBtnOnWhite} ${
                                    active ? styles.sizeBtnActive : ""
                                  }`}
                                  onClick={() => setComboSizes((prev) => ({ ...prev, [key]: sz }))}
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
                      disabled={addingKey !== null}
                      onClick={() => void handleAddComboOffer(offer)}
                    >
                      {addingKey === offer.id ? "Đang thêm..." : "Thêm trọn bộ vào giỏ hàng"}
                    </Button>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      </section>

      {/* Section 4: Unboxed rules band on subtle tonal surface */}
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
