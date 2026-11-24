"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import type { CatalogFlashSaleItem, CatalogFlashSaleSlot, CatalogShop } from "@/lib/catalog-types";
import { pickPreferredFlashSlot } from "@/lib/catalog-mapper";
import { formatDiscountPercent } from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, Price, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface FlashSaleClientProps {
  flashSlots: CatalogFlashSaleSlot[];
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

export function FlashSaleClient({ flashSlots, shops, serverNowMs, error }: FlashSaleClientProps) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);

  const { isAuthenticated } = useSession();
  const { showToast } = useMockStore();

  const stateParam = searchParams.get("state");
  const slotQuery = searchParams.get("slot");

  const defaultSlot = useMemo(() => {
    if (slotQuery) {
      const matched = flashSlots.find((s) => s.id === slotQuery);
      if (matched) return matched;
    }
    return pickPreferredFlashSlot(flashSlots);
  }, [flashSlots, slotQuery]);

  const [activeSlotId, setActiveSlotId] = useState<string>(defaultSlot?.id ?? "");
  const [remindedIds, setRemindedIds] = useState<Record<string, boolean>>({});

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

  const shopsById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

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
      <section className={styles.heroBand} aria-label="Tổng quan Flash sale">
        <div className={styles.headerGrid}>
          <div className={styles.titleBlock}>
            <h1 className={`${styles.title} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Flash sale
                </span>
              </span>
            </h1>
            <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              Khung giờ giá tốt từ các xưởng dệt Việt Nam với số suất ưu đãi cập nhật trực tiếp theo
              từng khung giờ mở bán.
            </p>
            <div
              className={styles.heroActionRow}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <a href="#flash-slot-heading" className={styles.heroBtnPrimary}>
                Khung {activeSlot.label}
              </a>
              <Link href="/danh-muc" className={styles.heroBtnDark}>
                Xem toàn bộ danh mục
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

      {/* 4-column Flash Sale grid from real backend /flash-sales */}
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

      {/* Unboxed rules band on subtle tonal surface */}
      <section className={styles.rulesSection} aria-labelledby="flash-rules-heading">
        <h2 id="flash-rules-heading" className={styles.rulesHeading}>
          Quy tắc ưu đãi Flash sale
        </h2>
        <div className={styles.rulesList}>
          <p>
            Áp dụng theo giới hạn suất của từng sản phẩm trong khung giờ mở bán. Suất giảm giá được
            giữ ngay khi bạn đặt hàng thành công để đảm bảo công bằng cho người mua.
          </p>
        </div>
      </section>
    </div>
  );
}
