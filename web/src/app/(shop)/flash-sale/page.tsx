"use client";

import React, { Suspense, useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  COMBO_OFFERS,
  FLASH_SALE_RULES,
  FLASH_SALE_SLOTS,
  SHOCK_DEALS,
  getLiveFlashSlot,
  getProductById,
  getShopById,
  type ComboOffer,
  type SizeCode,
} from "@/data";
import { formatDiscountPercent } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, Price, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

function twoDigit(n: number): string {
  const safe = Math.max(0, Math.trunc(n));
  return safe < 10 ? `0${safe}` : `${safe}`;
}

function FlashSaleInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);

  const { addToCart, cartItems, showToast } = useStore();

  const stateParam = searchParams.get("state");
  const slotQuery = searchParams.get("slot");

  const defaultSlotId = useMemo(() => {
    if (slotQuery && FLASH_SALE_SLOTS.some((s) => s.id === slotQuery)) return slotQuery;
    return getLiveFlashSlot().id;
  }, [slotQuery]);

  const [activeSlotId, setActiveSlotId] = useState<string>(defaultSlotId);
  // Deterministic initial countdown: 2h 47m 18s remaining in live 12:00 slot
  const [secondsLeft, setSecondsLeft] = useState<number>(2 * 3600 + 47 * 60 + 18);
  const [claimedCounts, setClaimedCounts] = useState<Record<string, number>>({});
  const [shockClaimedIds, setShockClaimedIds] = useState<Record<string, boolean>>({});
  const [remindedIds, setRemindedIds] = useState<Record<string, boolean>>({});
  const [selectedSizes, setSelectedSizes] = useState<Record<string, SizeCode>>({});
  const [comboSizes, setComboSizes] = useState<Record<string, SizeCode>>({});

  useEffect(() => {
    const id = window.setInterval(() => {
      setSecondsLeft((prev) => (prev > 0 ? prev - 1 : 0));
    }, 1000);
    return () => window.clearInterval(id);
  }, []);

  const activeSlot = useMemo(
    () => FLASH_SALE_SLOTS.find((s) => s.id === activeSlotId) ?? getLiveFlashSlot(),
    [activeSlotId]
  );

  const flashCountInCart = useMemo(
    () => cartItems.filter((it) => it.isFlashSaleItem).reduce((sum, it) => sum + it.quantity, 0),
    [cartItems]
  );

  const shockCountInCart = useMemo(
    () => cartItems.filter((it) => it.isShockDeal).reduce((sum, it) => sum + it.quantity, 0),
    [cartItems]
  );

  useRevealObserver(rootRef, `${activeSlot.id}|${stateParam ?? ""}`);

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

  if (stateParam === "error") {
    return (
      <div className={styles.stateWrap}>
        <StateView
          kind="error"
          title="Không thể tải phiên Flash sale"
          message="Hệ thống ưu đãi tạm gián đoạn. Vui lòng thử tải lại."
          actionLabel="Tải lại Flash sale"
          onAction={() => router.replace("/flash-sale")}
        />
      </div>
    );
  }

  if (stateParam === "empty") {
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
    router.replace(`/flash-sale?slot=${id}`, { scroll: false });
  };

  const handleQuickFlashBuy = (
    itemId: string,
    productId: string,
    flashPrice: number,
    limitPerBuyer: number,
    remaining: number
  ) => {
    const product = getProductById(productId);
    if (!product) return;
    if (flashCountInCart >= FLASH_SALE_RULES.maxItemsPerOrder) {
      showToast(
        `Mỗi đơn Flash sale chỉ chọn tối đa ${FLASH_SALE_RULES.maxItemsPerOrder} sản phẩm ưu đãi.`
      );
      return;
    }
    const already = claimedCounts[itemId] ?? 0;
    if (already >= limitPerBuyer) {
      showToast(`Bạn đã chọn tối đa ${limitPerBuyer} suất cho sản phẩm này.`);
      return;
    }
    if (remaining <= 0) {
      showToast("Suất ưu đãi của sản phẩm này đã hết.");
      return;
    }
    const defaultSize =
      selectedSizes[itemId] ??
      product.skus.find((s) => s.colorCode === product.colors[0].code && s.stock > 0)?.size ??
      product.sizes[0];

    const ok = addToCart({
      productId: product.id,
      colorCode: product.colors[0].code,
      size: defaultSize,
      quantity: 1,
      overridePrice: flashPrice,
      isFlashSaleItem: true,
    });
    if (ok) {
      setClaimedCounts((prev) => ({ ...prev, [itemId]: already + 1 }));
    }
  };

  const handleShockDealBuy = (
    dealId: string,
    productId: string,
    dealPrice: number,
    remaining: number
  ) => {
    const product = getProductById(productId);
    if (!product) return;
    if (remaining <= 0) {
      showToast("Suất Deal sốc này đã hết.");
      return;
    }
    const chosenSize =
      selectedSizes[dealId] ??
      product.skus.find((s) => s.colorCode === product.colors[0].code && s.stock > 0)?.size ??
      product.sizes[0];

    const ok = addToCart({
      productId: product.id,
      colorCode: product.colors[0].code,
      size: chosenSize,
      quantity: 1,
      overridePrice: dealPrice,
      isShockDeal: true,
    });
    if (ok) {
      setShockClaimedIds((prev) => ({ ...prev, [dealId]: true }));
    }
  };

  const handleAddComboOffer = (offer: ComboOffer) => {
    const products = offer.productIds
      .map((id) => getProductById(id))
      .filter((p): p is NonNullable<typeof p> => Boolean(p));
    if (products.length === 0) return;

    const totalBase = products.reduce((s, p) => s + p.price, 0);
    let allocated = 0;
    let addedAll = true;

    products.forEach((prod, idx) => {
      const key = `${offer.id}-${prod.id}`;
      const chosenSize =
        comboSizes[key] ??
        prod.skus.find((s) => s.colorCode === prod.colors[0].code && s.stock > 0)?.size ??
        prod.sizes[0];

      // Exact integer VND split summing strictly to offer.comboPrice
      const itemPrice =
        idx === products.length - 1
          ? Math.max(1000, offer.comboPrice - allocated)
          : Math.round(((prod.price / Math.max(1, totalBase)) * offer.comboPrice) / 1000) * 1000;
      allocated += itemPrice;

      const ok = addToCart({
        productId: prod.id,
        colorCode: prod.colors[0].code,
        size: chosenSize,
        quantity: 1,
        overridePrice: itemPrice,
      });
      if (!ok) addedAll = false;
    });

    if (addedAll) {
      showToast(`Đã thêm trọn ${offer.title} vào giỏ hàng.`);
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
      {/* Strong dark slate banner with integrated live countdown and 4-slot bar */}
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
              Bốn khung giờ giá tốt mỗi ngày từ bốn xưởng dệt Việt Nam, kèm Deal sốc giới hạn và
              Combo tiết kiệm.
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
              <span>
                Flash sale trong giỏ: {flashCountInCart}/{FLASH_SALE_RULES.maxItemsPerOrder} SP
              </span>
              <span>
                Deal sốc trong giỏ: {shockCountInCart}/{FLASH_SALE_RULES.shockDealMaxPerOrder} SP
              </span>
            </div>
          </div>
        </div>

        <nav className={styles.slotGrid} aria-label="Chọn khung giờ Flash sale">
          {FLASH_SALE_SLOTS.map((slot, idx) => {
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

      {/* Section 1: 4-column Flash Sale grid on open page ground */}
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

        <div className={styles.cardsGrid}>
          {activeSlot.items.map((item, idx) => {
            const product = getProductById(item.productId);
            if (!product) return null;
            const shop = getShopById(product.shopId);
            const claimed = claimedCounts[item.id] ?? 0;
            const liveRemaining = Math.max(0, item.remainingQuota - claimed);
            const soldRatio =
              item.totalQuota > 0
                ? Math.min(
                    100,
                    Math.round(((item.totalQuota - liveRemaining) / item.totalQuota) * 100)
                  )
                : 100;

            const chosenSize: SizeCode =
              selectedSizes[item.id] ??
              product.skus.find((s) => s.colorCode === product.colors[0].code && s.stock > 0)
                ?.size ??
              product.sizes[0];
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
                  <span className={styles.flashBadge}>{discountTag}</span>
                </div>

                <div className={styles.cardBody}>
                  <span className={styles.cardShop}>
                    {shop?.shortName ?? "NexusMart"} · {product.gsm}gsm
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
                          ? `Đã hết ${item.totalQuota} suất`
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
                            const sku = product.skus.find(
                              (s) => s.colorCode === product.colors[0].code && s.size === sz
                            );
                            const outOfStock = (sku?.stock ?? 0) <= 0;
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
                          disabled={liveRemaining <= 0}
                          onClick={() =>
                            handleQuickFlashBuy(
                              item.id,
                              product.id,
                              item.flashPrice,
                              item.limitPerBuyer,
                              liveRemaining
                            )
                          }
                        >
                          {liveRemaining <= 0 ? "Đã hết suất" : `Mua ưu đãi (${chosenSize})`}
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
      </section>

      {/* Section 2: Shock Deals (unboxed 4-col grid on clean primary white band) */}
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
          {SHOCK_DEALS.map((deal, idx) => {
            const product = getProductById(deal.productId);
            if (!product) return null;
            const shop = getShopById(product.shopId);
            const alreadyClaimed = Boolean(shockClaimedIds[deal.id]);
            const liveRemaining = Math.max(0, deal.remainingQuota - (alreadyClaimed ? 1 : 0));
            const soldRatio = Math.min(
              100,
              Math.round(((deal.totalQuota - liveRemaining) / Math.max(1, deal.totalQuota)) * 100)
            );
            const chosenSize: SizeCode =
              selectedSizes[deal.id] ??
              product.skus.find((s) => s.colorCode === product.colors[0].code && s.stock > 0)
                ?.size ??
              product.sizes[0];
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
                  <span className={styles.shockBadge}>{discountTag}</span>
                </div>

                <div className={styles.cardBody}>
                  <span className={styles.cardShop}>
                    {shop?.shortName ?? "NexusMart"} · Tối đa {deal.limitPerOrder} SP/đơn
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
                        const sku = product.skus.find(
                          (s) => s.colorCode === product.colors[0].code && s.size === sz
                        );
                        const outOfStock = (sku?.stock ?? 0) <= 0;
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
                      disabled={liveRemaining <= 0}
                      onClick={() =>
                        handleShockDealBuy(deal.id, product.id, deal.dealPrice, liveRemaining)
                      }
                    >
                      {alreadyClaimed ? "Đã thêm Deal sốc" : `Săn Deal (${chosenSize})`}
                    </Button>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      </section>

      {/* Section 3: 4 Combo Offers in a 2-column horizontal editorial grid (zero orphans) */}
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
          {COMBO_OFFERS.map((offer, idx) => {
            const shop = getShopById(offer.shopId);
            const products = offer.productIds
              .map((id) => getProductById(id))
              .filter((p): p is NonNullable<typeof p> => Boolean(p));

            return (
              <article
                key={offer.id}
                className={styles.comboCard}
                data-cat-reveal=""
                style={{ "--stagger": idx % 2 } as React.CSSProperties}
              >
                <div className={styles.comboMediaRow}>
                  {products.map((prod) => (
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
                    {products.map((prod) => {
                      const key = `${offer.id}-${prod.id}`;
                      const chosenSize: SizeCode =
                        comboSizes[key] ??
                        prod.skus.find((s) => s.colorCode === prod.colors[0].code && s.stock > 0)
                          ?.size ??
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
                              const sku = prod.skus.find(
                                (s) => s.colorCode === prod.colors[0].code && s.size === sz
                              );
                              const outOfStock = (sku?.stock ?? 0) <= 0;
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
                      onClick={() => handleAddComboOffer(offer)}
                    >
                      Thêm trọn bộ vào giỏ hàng
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

export default function FlashSalePage() {
  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải khung giờ Flash sale..." rows={6} />
        </div>
      }
    >
      <FlashSaleInner />
    </Suspense>
  );
}
