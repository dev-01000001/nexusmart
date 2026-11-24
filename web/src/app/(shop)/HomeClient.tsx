"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type {
  CatalogCategory,
  CatalogFlashSaleSlot,
  CatalogProduct,
  CatalogShop,
} from "@/lib/catalog-types";
import {
  CAMPAIGN_BANNERS,
  FABRIC_MEDIA,
  JOURNAL_ARTICLES,
  MEDIA_BANNERS,
  MEMBERSHIP_TIERS,
  WORKSHOP_MEDIA,
  getEditorialModelShotBySlug,
  getHomeCategoryBonusTile,
  matchesGenderFilter,
} from "@/mock/productEditorial";
import { ProductCard, StateView } from "@/components";
import { SizeFinder } from "@/features/catalog/SizeFinder";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import { formatVnd } from "@/lib/format";
import { getRootCategories, pickPreferredFlashSlot } from "@/lib/catalog-mapper";
import styles from "./page.module.css";

export interface HomeClientProps {
  categories: CatalogCategory[];
  shops: CatalogShop[];
  products: CatalogProduct[];
  flashSlots: CatalogFlashSaleSlot[];
  serverNowMs: number;
  error: string | null;
}

interface RingFrameGeom {
  product: CatalogProduct;
  src: string;
  alt: string;
  isModel: boolean;
  dx: string;
  dy: string;
  dw: string;
  tw: string;
  dr: string;
  ratio: string;
  mobileHide: boolean;
  mx: string;
  my: string;
  mw: string;
  mr: string;
  enterDelay: string;
}

interface FabricStandard {
  id: string;
  name: string;
  gsm: string;
  detail: string;
  origin: string;
}

const FABRIC_STANDARDS: FabricStandard[] = [
  {
    id: "cotton-220",
    name: "Cotton Compact 220gsm",
    gsm: "220gsm",
    detail:
      "Sợi bông chải kỹ se gọn giúp bề mặt vải mịn, hạn chế xù lông sau nhiều lần giặt máy và đủ dày để áo trắng không bị mỏng lộ da.",
    origin: "Xưởng Dệt Bạch Đằng · Áo thun cổ tròn mặc hằng ngày",
  },
  {
    id: "heavy-260",
    name: "Cotton Heavyweight 260gsm",
    gsm: "260gsm",
    detail:
      "Dệt sợi đôi đứng phom vai rõ nét cho dáng áo rộng nam nữ, mặt trong thoáng da và giữ dáng bo cổ 2,2 cm chắc chắn.",
    origin: "Kho Basic Sài Gòn · Áo thun dáng rộng",
  },
  {
    id: "pique-215",
    name: "Pique mắt chim 215gsm",
    gsm: "215gsm",
    detail:
      "Cấu trúc dệt lỗ mắt chim mở rãnh lưu thông không khí, giữ cổ áo thẳng nếp suốt ngày làm việc công sở nóng ẩm.",
    origin: "Sợi Tự Nhiên Đà Nẵng · Áo polo công sở",
  },
  {
    id: "terry-320",
    name: "Nỉ chân cua 320gsm",
    gsm: "320gsm",
    detail:
      "Mặt trong dệt vòng thoáng khí, giữ đầu gối không bị phồng sau thời gian dài mặc và bo cổ chân ôm gọn cổ giày.",
    origin: "Dệt Kim Hà Nội · Quần jogger nỉ",
  },
  {
    id: "modal-bamboo",
    name: "Sợi gỗ sồi Modal & Sợi tre",
    gsm: "175 - 180gsm",
    detail:
      "Chạm dịu và hút ẩm vượt trội hơn cotton thông thường, may chập phẳng giảm cọ xát và dùng nhãn ép nhiệt không gây ngứa.",
    origin: "Hải Phòng & Đà Nẵng · Combo đồ lót mặc cả ngày",
  },
  {
    id: "dryfit-145",
    name: "Dry-Fit Micro-mesh 145gsm",
    gsm: "145gsm",
    detail:
      "Sợi dệt lỗ siêu nhỏ thoát mồ hôi nhanh và ráo nước chỉ sau khoảng 45 phút phơi gió, nhẹ thoáng cho buổi chạy bộ sáng sớm.",
    origin: "Kho Basic Sài Gòn · Đồ chạy bộ & tập luyện",
  },
];

const COMMITMENT_ITEMS = [
  {
    id: "return-7",
    title: "Hỗ trợ trả hàng trong 7 ngày",
    detail:
      "Tạo yêu cầu trả hàng, gửi bằng chứng trực tiếp trên trang đơn hàng và nhận hoàn tiền minh bạch trong vòng 7 ngày kể từ khi nhận hàng.",
  },
  {
    id: "freeship-shop",
    title: "Ưu đãi vận chuyển theo gian hàng & sàn",
    detail:
      "Áp dụng đồng thời hỗ trợ freeship của từng gian hàng và mã vận chuyển toàn sàn trên một màn hình thanh toán.",
  },
  {
    id: "cod-check",
    title: "Thanh toán VietQR hoặc COD linh hoạt",
    detail:
      "Chuyển khoản nhanh qua mã VietQR xác nhận tự động hoặc thanh toán tiền mặt khi nhận hàng (COD) cho các đơn đủ điều kiện.",
  },
  {
    id: "direct-4-workshops",
    title: "Trực tiếp từ 4 xưởng dệt Việt",
    detail:
      "Bốn xưởng đối tác tại Hải Phòng, Hà Nội, Đà Nẵng và TP.HCM kiểm vải từng lô và giữ mức giá ổn định quanh năm.",
  },
];

const FAQ_ITEMS = [
  {
    q: "Tôi có thể mua sản phẩm từ nhiều xưởng trong cùng một đơn không?",
    a: "Có. Giỏ hàng tự sắp xếp sản phẩm theo từng xưởng, áp riêng ưu đãi của mỗi xưởng rồi gộp chung mã giảm giá toàn sàn, ưu đãi vận chuyển và Xu trên một màn hình thanh toán.",
  },
  {
    q: "Làm sao chọn đúng size giữa dáng vừa và dáng rộng?",
    a: "Bạn chỉ cần kéo thanh chiều cao, cân nặng và chọn dáng người ngay ở mục Gợi ý size phía trên để đối chiếu theo bảng rập riêng của từng xưởng dệt.",
  },
  {
    q: "Tình trạng còn hàng theo màu và size có được cập nhật ngay không?",
    a: "Có. Từng lựa chọn màu sắc và kích cỡ đều hiển thị rõ số lượng hiện có tại kho xưởng để bạn yên tâm đặt đúng món mình cần.",
  },
  {
    q: "Khung giờ Flash sale diễn ra khi nào?",
    a: "Các khung giờ mở bán trong ngày được cập nhật trực tiếp cùng số suất ưu đãi còn lại trên từng sản phẩm. Đơn hàng có sản phẩm Flash sale thanh toán qua VietQR trong 5 phút.",
  },
  {
    q: "NexusMart hỗ trợ những hình thức thanh toán và giao hàng nào?",
    a: "Bạn có thể trả tiền mặt khi nhận hàng (COD) hoặc chuyển khoản nhanh qua VietQR; giao hàng toàn quốc đủ 34 tỉnh thành với cước vận chuyển tính trực tiếp theo địa chỉ nhận.",
  },
  {
    q: "Chính sách trả hàng và hoàn tiền trong 7 ngày áp dụng ra sao?",
    a: "Trong vòng 7 ngày kể từ khi nhận hàng, bạn có thể gửi yêu cầu trả hàng ngay trên trang chi tiết đơn hàng để gian hàng và bộ phận hỗ trợ sàn xử lý hoàn tiền.",
  },
];

function twoDigit(n: number): string {
  const safe = Math.max(0, Math.trunc(n));
  return safe < 10 ? `0${safe}` : `${safe}`;
}

function getSafeJournalCover(slug: string, coverImage: string): string {
  if (
    slug === "bo-do-chay-bo-5k-khong-co-xat-va-nhanh-kho" &&
    coverImage.startsWith("/atmosphere/")
  ) {
    return MEDIA_BANNERS.running.src;
  }
  return coverImage;
}

function computeRemainingSeconds(slot: CatalogFlashSaleSlot | undefined, nowMs: number): number {
  if (!slot) return 0;
  const targetIso = slot.status === "upcoming" ? slot.startsAt : slot.endsAt;
  const targetMs = targetIso ? Date.parse(targetIso) : 0;
  if (!Number.isFinite(targetMs) || targetMs <= nowMs) return 0;
  return Math.max(0, Math.floor((targetMs - nowMs) / 1000));
}

function buildRingGeometry(products: CatalogProduct[]): RingFrameGeom[] {
  const withLocalImageFirst = [...products].sort((a, b) => {
    const aLocal = a.image.startsWith("/") ? 0 : 1;
    const bLocal = b.image.startsWith("/") ? 0 : 1;
    return aLocal - bLocal;
  });
  const ringProducts = withLocalImageFirst.slice(0, 18);
  const total = ringProducts.length;
  if (total === 0) return [];
  const mobileIndices = ringProducts.map((_, idx) => idx).filter((idx) => idx % 3 !== 2);
  const mobileTotal = Math.max(1, mobileIndices.length);

  return ringProducts.map((product, i) => {
    const theta = -Math.PI / 2 + (i * 2 * Math.PI) / total;
    const rx = 40;
    const ry = 38.5;
    const xPct = 50 + rx * Math.cos(theta);
    const yPct = 50 + ry * Math.sin(theta);

    const depth = (Math.sin(theta) + 1) / 2;
    const sideWeight = Math.abs(Math.cos(theta));
    const scaleFactor = Math.min(1, depth * 0.78 + sideWeight * 0.36);
    const desktopW = Math.round(92 + 44 * scaleFactor);
    const tabletW = Math.round(64 + 28 * scaleFactor);
    const tiltDeg = Math.round(-Math.sin(2 * theta) * 20);
    const ratio = i % 2 === 0 ? "3 / 4" : "4 / 3";

    const mPos = mobileIndices.indexOf(i);
    const mobileHide = mPos === -1;
    let mx = "50%";
    let my = "50%";
    let mw = "68px";
    let mr = "0deg";

    if (!mobileHide) {
      const mTheta = -Math.PI / 2 + (mPos * 2 * Math.PI) / mobileTotal;
      const mXOffsetVw = 64 * Math.cos(mTheta);
      const mYOffsetPx = 224 * Math.sin(mTheta);
      mx = `calc(50% + ${mXOffsetVw.toFixed(1)}vw)`;
      my = `calc(50% + ${Math.round(mYOffsetPx)}px)`;
      const mDepth = (Math.sin(mTheta) + 1) / 2;
      mw = `${Math.round(60 + 20 * mDepth)}px`;
      mr = `${Math.round(-Math.sin(2 * mTheta) * 16)}deg`;
    }

    const modelShot = getEditorialModelShotBySlug(product.slug);
    const useModel = i % 2 === 0 && Boolean(modelShot || product.onModelImage);
    const src = useModel
      ? (product.onModelImage ?? modelShot?.src ?? product.image)
      : product.image;
    const alt = useModel ? (modelShot?.alt ?? `${product.name} khi mặc thực tế`) : product.name;

    return {
      product,
      src,
      alt,
      isModel: useModel,
      dx: `${xPct.toFixed(2)}%`,
      dy: `${yPct.toFixed(2)}%`,
      dw: `${desktopW}px`,
      tw: `${tabletW}px`,
      dr: `${tiltDeg}deg`,
      ratio,
      mobileHide,
      mx,
      my,
      mw,
      mr,
      enterDelay: `${60 + (i % 9) * 45}ms`,
    };
  });
}

export function HomeClient({
  categories,
  shops,
  products,
  flashSlots,
  serverNowMs,
  error,
}: HomeClientProps) {
  const router = useRouter();
  const pageRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  const liveSlot = useMemo(() => pickPreferredFlashSlot(flashSlots), [flashSlots]);

  const [hoveredProductId, setHoveredProductId] = useState<string | null>(null);
  const [genderTab, setGenderTab] = useState<"all" | "nam" | "nu">("all");
  const [catalogMode, setCatalogMode] = useState<"bestseller" | "newest">("bestseller");
  const [activeFabricId, setActiveFabricId] = useState<string>(FABRIC_STANDARDS[0].id);
  const [secondsLeft, setSecondsLeft] = useState<number>(() =>
    computeRemainingSeconds(liveSlot, serverNowMs)
  );

  useRevealObserver(pageRef);

  useEffect(() => {
    setSecondsLeft(computeRemainingSeconds(liveSlot, Date.now()));
    if (!liveSlot) return;
    const timer = window.setInterval(() => {
      setSecondsLeft(computeRemainingSeconds(liveSlot, Date.now()));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [liveSlot]);

  // Respect prefers-reduced-motion on looping Jacquard loom video
  useEffect(() => {
    const vid = videoRef.current;
    if (!vid || typeof window === "undefined") return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const syncPlayback = () => {
      if (mq.matches) {
        vid.pause();
      } else {
        vid.play().catch(() => {});
      }
    };
    syncPlayback();
    mq.addEventListener?.("change", syncPlayback);
    return () => mq.removeEventListener?.("change", syncPlayback);
  }, []);

  const ringFrames = useMemo(() => buildRingGeometry(products), [products]);

  const hoveredProduct = useMemo(
    () => (hoveredProductId ? (products.find((p) => p.id === hoveredProductId) ?? null) : null),
    [hoveredProductId, products]
  );

  const flashCards = useMemo(() => {
    if (!liveSlot) return [];
    return liveSlot.items.slice(0, 4);
  }, [liveSlot]);

  const flashProductIds = useMemo(
    () => new Set(flashCards.map((entry) => entry.product.id)),
    [flashCards]
  );

  const showcaseProducts = useMemo(() => {
    const matched = products.filter((p) => matchesGenderFilter(p, genderTab));
    const sorted = [...matched];
    if (catalogMode === "newest") {
      sorted.sort((a, b) => {
        const aTime = a.createdAt ? Date.parse(a.createdAt) : 0;
        const bTime = b.createdAt ? Date.parse(b.createdAt) : 0;
        return bTime - aTime || Number(Boolean(b.isNew)) - Number(Boolean(a.isNew));
      });
    } else {
      sorted.sort((a, b) => (a.salesRank ?? 99) - (b.salesRank ?? 99));
    }
    const distinct = sorted.filter((p) => !flashProductIds.has(p.id));
    const ordered =
      distinct.length >= 8
        ? distinct
        : [...distinct, ...sorted.filter((p) => flashProductIds.has(p.id))];
    return ordered.slice(0, 8);
  }, [products, flashProductIds, genderTab, catalogMode]);

  const activeFabric = useMemo(
    () => FABRIC_STANDARDS.find((f) => f.id === activeFabricId) ?? FABRIC_STANDARDS[0],
    [activeFabricId]
  );

  const categoryTiles = useMemo(() => {
    const mapped = getRootCategories(categories).map((cat) => ({
      slug: cat.slug,
      label: cat.name,
      subtitle: cat.subtitle,
      href: `/danh-muc?cat=${encodeURIComponent(cat.slug)}`,
      src: cat.featuredImage,
      alt: cat.featuredAlt,
    }));
    // Keep 7 tiles (1 wide featured + 6 tiles) so desktop 4x2 asymmetric sheet stays balanced
    if (mapped.length === 6) {
      const bonus = getHomeCategoryBonusTile();
      mapped.push({
        slug: bonus.slug,
        label: bonus.label,
        subtitle: bonus.subtitle,
        href: bonus.href,
        src: bonus.src,
        alt: bonus.alt,
      });
    }
    return mapped;
  }, [categories]);

  const featuredCategory = categoryTiles[0];
  const secondaryCategories = categoryTiles.slice(1);
  // Show 4 workshops on desktop 4-column grid so there are zero orphan columns
  const featuredShops = useMemo(() => shops.slice(0, 4), [shops]);

  const hrs = Math.floor(secondsLeft / 3600);
  const mins = Math.floor((secondsLeft % 3600) / 60);
  const secs = secondsLeft % 60;
  const countdownText = `${twoDigit(hrs)}:${twoDigit(mins)}:${twoDigit(secs)}`;

  if (error && products.length === 0) {
    return (
      <div className={styles.page} style={{ padding: "40px var(--margin)" }}>
        <StateView
          kind="error"
          title="Không thể tải dữ liệu cửa hàng lúc này"
          message={error}
          actionLabel="Tải lại trang"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  return (
    <div ref={pageRef} className={styles.page}>
      {/* =====================================================================
          1. HERO: Contact-sheet ring mixing studio flat-lays & on-model shots
          ===================================================================== */}
      <section className={styles.hero} aria-label="Giới thiệu NexusMart">
        <div
          className={`${styles.ring} ${hoveredProductId ? styles.ringHasActive : ""}`}
          onMouseLeave={() => setHoveredProductId(null)}
        >
          {ringFrames.map((item) => {
            const isActive = hoveredProductId === item.product.id;
            const frameStyle = {
              "--dx": item.dx,
              "--dy": item.dy,
              "--dw": item.dw,
              "--tw": item.tw,
              "--dr": item.dr,
              "--ratio": item.ratio,
              "--mx": item.mx,
              "--my": item.my,
              "--mw": item.mw,
              "--mr": item.mr,
              "--enter-delay": item.enterDelay,
            } as React.CSSProperties;

            return (
              <Link
                key={item.product.id}
                href={`/san-pham/${item.product.slug}`}
                className={`${styles.frame} ${isActive ? styles.frameActive : ""}`}
                style={frameStyle}
                data-mobile-hide={item.mobileHide ? "1" : "0"}
                onMouseEnter={() => setHoveredProductId(item.product.id)}
                onFocus={() => setHoveredProductId(item.product.id)}
                onBlur={() => setHoveredProductId(null)}
                aria-label={`${item.product.name}${item.product.gsm > 0 ? `, ${item.product.gsm}gsm` : ""}, ${formatVnd(item.product.price)}`}
              >
                <Image
                  src={item.src}
                  alt={item.alt}
                  fill
                  sizes="(max-width: 640px) 84px, (max-width: 1080px) 110px, 148px"
                  priority={item.isModel}
                  className={`${styles.frameImg} ${item.isModel ? styles.frameImgModel : ""}`}
                />
              </Link>
            );
          })}
        </div>

        <div className={styles.heroCore}>
          <h1 className={`${styles.heroTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Đồ basic Việt
              </span>
            </span>
            <span className="line">
              <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                từ xưởng dệt
              </span>
            </span>
          </h1>

          <p
            className={styles.heroLead}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
            aria-live="polite"
          >
            {hoveredProduct ? (
              <>
                <span className={styles.heroPreviewName}>
                  {hoveredProduct.name}
                  {hoveredProduct.gsm > 0 ? ` · ${hoveredProduct.gsm}gsm` : ""}
                </span>
                <span className={styles.heroPreviewPrice}>{formatVnd(hoveredProduct.price)}</span>
              </>
            ) : (
              "Bốn xưởng dệt Việt Nam, chất vải ghi rõ định lượng sợi, giao nhanh 34 tỉnh thành."
            )}
          </p>

          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Link href="/danh-muc" className={styles.heroCtaPrimary}>
              Mua sắm ngay
            </Link>
            {liveSlot && liveSlot.items.length > 0 && (
              <Link href="/flash-sale" className={styles.heroCtaSecondary}>
                {`Săn Flash sale ${liveSlot.label}`}
              </Link>
            )}
          </div>
        </div>
      </section>

      {/* =====================================================================
          2. CATEGORY PHOTO TILES: Real backend categories in balanced sheet
          ===================================================================== */}
      {categoryTiles.length > 0 && (
        <section className={styles.categorySection} aria-labelledby="home-categories-heading">
          <div className={styles.sectionHead}>
            <h2 id="home-categories-heading" className={styles.sectionTitle}>
              Danh mục đồ cơ bản
            </h2>
            <Link href="/danh-muc" className={styles.sectionLink}>
              Xem tất cả {products.length} sản phẩm
            </Link>
          </div>

          <div className={styles.categoryGrid}>
            {featuredCategory && (
              <Link
                href={featuredCategory.href}
                className={styles.categoryFeature}
                data-cat-reveal=""
                style={{ "--stagger": 0 } as React.CSSProperties}
              >
                <div className={styles.categoryFeatureCopy}>
                  <div className={styles.categoryFeatureTop}>
                    <h3 className={styles.categoryFeatureTitle}>{featuredCategory.label}</h3>
                    <p className={styles.categoryFeatureSub}>{featuredCategory.subtitle}</p>
                  </div>
                  <p className={styles.categoryFeatureDesc}>
                    Cotton Compact 220gsm mặc đi làm, Dry-Fit 145gsm chạy bộ và áo dáng rộng 260gsm
                    giữ phom vai gọn.
                  </p>
                  <span className={styles.categoryFeatureCta}>Xem dòng áo thun</span>
                </div>
                <div className={styles.categoryFeatureMedia}>
                  <Image
                    src={featuredCategory.src}
                    alt={featuredCategory.alt}
                    fill
                    sizes="(max-width: 768px) 50vw, 25vw"
                    priority
                    className={styles.categoryImg}
                  />
                </div>
              </Link>
            )}

            {secondaryCategories.map((cat, idx) => (
              <Link
                key={cat.slug}
                href={cat.href}
                className={styles.categoryTile}
                data-cat-reveal=""
                style={{ "--stagger": (idx % 4) + 1 } as React.CSSProperties}
              >
                <div className={styles.categoryTileMedia}>
                  <Image
                    src={cat.src}
                    alt={cat.alt}
                    fill
                    sizes="(max-width: 768px) 50vw, 25vw"
                    className={styles.categoryImg}
                  />
                </div>
                <div className={styles.categoryTileInfo}>
                  <h3 className={styles.categoryTileTitle}>{cat.label}</h3>
                  <p className={styles.categoryTileSub}>{cat.subtitle}</p>
                </div>
              </Link>
            ))}
          </div>
        </section>
      )}

      {/* =====================================================================
          3. FLASH SALE BAND: Real backend slots, remaining slots & direct buy
          ===================================================================== */}
      {liveSlot && flashCards.length > 0 && (
        <section className={styles.flashSection} aria-labelledby="home-flash-heading">
          <div
            className={styles.flashBannerBar}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            <div className={styles.flashBannerLead}>
              <h2 id="home-flash-heading" className={styles.flashTitle}>
                Flash sale khung {liveSlot.label}
              </h2>
              <p className={styles.flashMeta}>
                {liveSlot.statusLabel} từ {liveSlot.startTime} đến {liveSlot.endTime} ·{" "}
                {liveSlot.items.length} sản phẩm ưu đãi · Áp dụng theo giới hạn suất của từng sản
                phẩm
              </p>
            </div>

            <div className={styles.flashBannerRight}>
              <div className={styles.countdownBlock} aria-live="off">
                <span className={styles.countdownCaption}>
                  {liveSlot.status === "upcoming" ? "Mở bán sau" : "Kết thúc sau"}
                </span>
                <span className={`${styles.countdownClock} num`}>{countdownText}</span>
              </div>
              <Link href="/flash-sale" className={styles.flashCta}>
                Xem tất cả Flash sale
              </Link>
            </div>
          </div>

          <div className={styles.productGrid4}>
            {flashCards.map((slotItem, idx) => (
              <div
                key={slotItem.id}
                data-cat-reveal=""
                style={{ "--stagger": idx } as React.CSSProperties}
              >
                <ProductCard
                  product={slotItem.product}
                  overridePrice={slotItem.flashPriceVnd}
                  overrideOriginalPrice={slotItem.originalPriceVnd}
                  quotaLabel={`Còn ${slotItem.remainingSlots} suất`}
                  isFlashSaleItem
                  flashSaleItemId={slotItem.id}
                  flashSaleSkuId={slotItem.skuId}
                  flashSaleDisabled={
                    liveSlot.status !== "live" ||
                    slotItem.remainingSlots <= 0 ||
                    slotItem.skuAvailableStock <= 0
                  }
                  showRating
                  enableQuickAdd
                />
              </div>
            ))}
          </div>
        </section>
      )}

      {/* =====================================================================
          4. BESTSELLERS & NEW ARRIVALS: Full-width 4-col grid of real products
          ===================================================================== */}
      <section className={styles.catalogSection} aria-labelledby="home-catalog-heading">
        <div className={styles.catalogHead}>
          <div className={styles.catalogTitleWrap}>
            <h2 id="home-catalog-heading" className={styles.sectionTitle}>
              Sản phẩm nổi bật từ xưởng
            </h2>
            <p className={styles.catalogLead}>
              Rê chuột lên ảnh để xem dáng mặc thực tế, chọn màu hoặc thêm nhanh theo size ngay trên
              thẻ sản phẩm.
            </p>
          </div>

          <div className={styles.catalogToolbar}>
            <div className={styles.filterGroup} role="group" aria-label="Lọc theo giới tính">
              {(
                [
                  { id: "all", label: "Tất cả" },
                  { id: "nam", label: "Nam" },
                  { id: "nu", label: "Nữ" },
                ] as const
              ).map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  className={`${styles.filterBtn} ${
                    genderTab === tab.id ? styles.filterBtnActive : ""
                  }`}
                  onClick={() => setGenderTab(tab.id)}
                  aria-pressed={genderTab === tab.id}
                >
                  {tab.label}
                </button>
              ))}
            </div>

            <div className={styles.filterGroup} role="group" aria-label="Sắp xếp sản phẩm">
              <button
                type="button"
                className={`${styles.filterBtn} ${
                  catalogMode === "bestseller" ? styles.filterBtnActive : ""
                }`}
                onClick={() => setCatalogMode("bestseller")}
                aria-pressed={catalogMode === "bestseller"}
              >
                Bán chạy
              </button>
              <button
                type="button"
                className={`${styles.filterBtn} ${
                  catalogMode === "newest" ? styles.filterBtnActive : ""
                }`}
                onClick={() => setCatalogMode("newest")}
                aria-pressed={catalogMode === "newest"}
              >
                Mới về
              </button>
            </div>
          </div>
        </div>

        {showcaseProducts.length === 0 ? (
          <StateView
            kind="empty"
            title="Chưa có sản phẩm phù hợp với bộ lọc đang chọn"
            actionLabel="Xem toàn bộ danh mục"
            actionHref="/danh-muc"
          />
        ) : (
          <div className={styles.productGrid4}>
            {showcaseProducts.map((product, idx) => (
              <div
                key={product.id}
                data-cat-reveal=""
                style={{ "--stagger": idx % 4 } as React.CSSProperties}
              >
                <ProductCard product={product} showRating enableQuickAdd />
              </div>
            ))}
          </div>
        )}

        <div className={styles.catalogFooter}>
          <Link
            href={
              genderTab === "all"
                ? `/danh-muc?sort=${catalogMode}`
                : `/danh-muc?gender=${genderTab}&sort=${catalogMode}`
            }
            className={styles.catalogMoreBtn}
          >
            Xem toàn bộ {products.length} sản phẩm
          </Link>
        </div>
      </section>

      {/* =====================================================================
          5. CAMPAIGN & COLLECTION WIDE BANNERS: 2x2 editorial lookbook (16:9)
          ===================================================================== */}
      <section className={styles.campaignSection} aria-labelledby="home-campaigns-heading">
        <div className={styles.sectionHead}>
          <h2 id="home-campaigns-heading" className={styles.sectionTitle}>
            Bộ sưu tập theo nhịp sống
          </h2>
          <Link href="/bo-suu-tap" className={styles.sectionLink}>
            Xem tất cả bộ sưu tập
          </Link>
        </div>

        <div className={styles.campaignGrid}>
          {CAMPAIGN_BANNERS.map((banner, idx) => (
            <article
              key={banner.slug}
              className={styles.campaignItem}
              data-cat-reveal=""
              style={{ "--stagger": idx % 2 } as React.CSSProperties}
            >
              <Link href={banner.href} className={styles.campaignMediaLink}>
                <div className={styles.campaignMedia}>
                  <Image
                    src={banner.src}
                    alt={banner.alt}
                    fill
                    sizes="(max-width: 768px) 100vw, 50vw"
                    className={styles.campaignImg}
                  />
                </div>
              </Link>
              <div className={styles.campaignBody}>
                <div className={styles.campaignText}>
                  <h3 className={styles.campaignTitle}>
                    <Link href={banner.href} className={styles.campaignTitleLink}>
                      {banner.title}
                    </Link>
                  </h3>
                  <p className={styles.campaignCaption}>{banner.caption}</p>
                </div>
                <Link href={banner.href} className={styles.campaignAction}>
                  {banner.slug === "soi-xanh-nexus" ? "Xem chiến dịch" : "Khám phá"}
                </Link>
              </div>
            </article>
          ))}
        </div>
      </section>

      {/* =====================================================================
          6. VIETNAM WORKSHOPS, FABRIC CRAFT & FEATURED SHOPS (from GET /shops)
          ===================================================================== */}
      <section className={styles.workshopSection} aria-labelledby="home-workshop-heading">
        <div className={styles.sectionHead}>
          <h2 id="home-workshop-heading" className={styles.sectionTitle}>
            Trực tiếp từ bốn xưởng dệt Việt
          </h2>
          <Link href="/ve-nexusmart" className={styles.sectionLink}>
            Tìm hiểu tiêu chuẩn xưởng
          </Link>
        </div>

        <div className={styles.workshopMediaGrid}>
          <figure
            className={styles.workshopFigure}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            <div className={styles.workshopFrame}>
              <Image
                src={WORKSHOP_MEDIA.src}
                alt={WORKSHOP_MEDIA.alt}
                fill
                sizes="(max-width: 768px) 100vw, 50vw"
                className={styles.workshopImg}
              />
            </div>
            <figcaption className={styles.workshopCaption}>
              Thợ dệt kiểm tra mật độ sợi và độ đều bề mặt từng cây vải cotton trước khi cắt rập.
            </figcaption>
          </figure>

          <figure
            className={styles.workshopFigure}
            data-cat-reveal=""
            style={{ "--stagger": 1 } as React.CSSProperties}
          >
            <div className={styles.workshopFrame}>
              <video
                ref={videoRef}
                className={styles.loomVideo}
                src={WORKSHOP_MEDIA.loomVideo.src}
                poster={WORKSHOP_MEDIA.loomVideo.poster}
                width={WORKSHOP_MEDIA.loomVideo.width}
                height={WORKSHOP_MEDIA.loomVideo.height}
                autoPlay
                muted
                loop
                playsInline
                preload="metadata"
                aria-label={WORKSHOP_MEDIA.loomVideo.alt}
              />
            </div>
            <figcaption className={styles.workshopCaption}>
              Nhịp đan sợi liên tục trên khung dệt Jacquard giúp vải giữ cấu trúc ổn định sau nhiều
              lần giặt.
            </figcaption>
          </figure>
        </div>

        <div
          className={styles.craftSplit}
          data-cat-reveal=""
          style={{ "--stagger": 1 } as React.CSSProperties}
        >
          <div className={styles.craftPhotoWrap}>
            <Image
              src={FABRIC_MEDIA.src}
              alt={FABRIC_MEDIA.alt}
              fill
              sizes="(max-width: 960px) 100vw, 42vw"
              className={styles.workshopImg}
            />
          </div>

          <div className={styles.craftContent}>
            <h3 className={styles.craftTitle}>Sáu dòng vải chủ lực ghi rõ định lượng sợi</h3>
            <div className={styles.fabricSelector} role="group" aria-label="Chọn chất liệu vải">
              {FABRIC_STANDARDS.map((fabric) => {
                const isActive = fabric.id === activeFabric.id;
                return (
                  <button
                    key={fabric.id}
                    type="button"
                    className={`${styles.fabricBtn} ${isActive ? styles.fabricBtnActive : ""}`}
                    onClick={() => setActiveFabricId(fabric.id)}
                    aria-pressed={isActive}
                  >
                    {fabric.name}
                  </button>
                );
              })}
            </div>

            <div className={styles.fabricPanel} aria-live="polite">
              <div className={styles.fabricPanelHead}>
                <span className={styles.fabricPanelName}>{activeFabric.name}</span>
                <span className={`${styles.fabricPanelGsm} num`}>{activeFabric.gsm}</span>
              </div>
              <p className={styles.fabricPanelBody}>{activeFabric.detail}</p>
              <p className={styles.fabricPanelOrigin}>{activeFabric.origin}</p>
            </div>
          </div>
        </div>

        {featuredShops.length > 0 && (
          <>
            <div className={styles.shopsSubHead}>
              <h3 className={styles.shopsSubTitle}>Gian hàng xưởng nổi bật</h3>
              <Link href="/danh-muc" className={styles.sectionLink}>
                Lọc sản phẩm theo xưởng
              </Link>
            </div>

            <div className={styles.shopsGrid}>
              {featuredShops.map((shop, idx) => (
                <article
                  key={shop.id}
                  className={styles.shopItem}
                  data-cat-reveal=""
                  style={{ "--stagger": idx } as React.CSSProperties}
                >
                  <div
                    className={styles.accentBar}
                    style={{ backgroundColor: shop.accentDot }}
                    aria-hidden="true"
                  />
                  <h4 className={styles.shopName}>
                    <Link href={`/gian-hang/${shop.slug}`} className={styles.shopNameLink}>
                      {shop.name}
                    </Link>
                  </h4>
                  <p className={styles.shopCity}>{shop.city}</p>
                  <p className={styles.shopSpecialty}>{shop.specialty}</p>
                  <p className={styles.shopStats}>
                    Phản hồi {shop.responseTime.toLowerCase()} · Giao đúng hẹn{" "}
                    {shop.fulfillmentRate}
                  </p>
                  <Link href={`/gian-hang/${shop.slug}`} className={styles.shopAction}>
                    Ghé gian hàng
                  </Link>
                </article>
              ))}
            </div>
          </>
        )}
      </section>

      {/* =====================================================================
          7. INTERACTIVE SIZE FINDER: Framed cleanly on --surface-white
          ===================================================================== */}
      <section className={styles.sizeSection} aria-labelledby="home-size-heading">
        <div className={styles.sectionHead}>
          <h2 id="home-size-heading" className={styles.sectionTitle}>
            Chọn size chuẩn theo từng xưởng
          </h2>
          <Link href="/danh-muc" className={styles.sectionLink}>
            Lọc sản phẩm theo size
          </Link>
        </div>
        <SizeFinder products={products} shops={shops} />
      </section>

      {/* =====================================================================
          8. MEMBERSHIP BAND: Preview notice for upcoming loyalty program
          ===================================================================== */}
      <section className={styles.membershipSection} aria-labelledby="home-membership-heading">
        <div className={styles.membershipTop}>
          <div className={styles.membershipIntro}>
            <h2 id="home-membership-heading" className={styles.membershipTitle}>
              Chương trình Xu &amp; Hội viên Nexus (Bản xem trước)
            </h2>
            <p className={styles.membershipLead}>
              Bản xem trước, dữ liệu mẫu cho hệ thống phân hạng sắp ra mắt. Hiện tại bạn đã có thể
              dùng Xu NexusMart (1 Xu = 1₫) trừ trực tiếp khi thanh toán đơn hàng.
            </p>
          </div>
          <div className={styles.membershipActions}>
            <Link href="/danh-muc" className={styles.membershipCtaPrimary}>
              Khám phá danh mục
            </Link>
            <Link href="/flash-sale" className={styles.membershipCtaSecondary}>
              Săn Flash sale hôm nay
            </Link>
          </div>
        </div>

        <div className={styles.tierGrid}>
          {MEMBERSHIP_TIERS.map((tier, idx) => (
            <div
              key={tier.id}
              className={styles.tierItem}
              data-cat-reveal=""
              style={{ "--stagger": idx } as React.CSSProperties}
            >
              <div className={styles.tierAccentBar} aria-hidden="true" />
              <div className={styles.tierHead}>
                <h3 className={styles.tierName}>{tier.name}</h3>
                <span className={`${styles.tierRate} num`}>Hoàn {tier.cashbackPercentLabel}</span>
              </div>
              <p className={styles.tierThreshold}>
                {tier.minSpend4Q === 0
                  ? "Mở ngay khi đăng ký tài khoản"
                  : `Chi tiêu từ ${formatVnd(tier.minSpend4Q)} / 4 quý`}
              </p>
              <p className={styles.tierPerk}>{tier.perks[0]}</p>
            </div>
          ))}
        </div>
      </section>

      {/* =====================================================================
          9. JOURNAL STRIP: 4 articles in 4-col desktop / 2-col tablet / 1-col mobile
          ===================================================================== */}
      <section className={styles.journalSection} aria-labelledby="home-journal-heading">
        <div className={styles.sectionHead}>
          <h2 id="home-journal-heading" className={styles.sectionTitle}>
            Sổ tay vải &amp; Phối đồ
          </h2>
          <Link href="/tap-chi" className={styles.sectionLink}>
            Đọc tất cả bài viết
          </Link>
        </div>

        <div className={styles.journalGrid}>
          {JOURNAL_ARTICLES.slice(0, 4).map((article, idx) => (
            <article
              key={article.slug}
              className={styles.journalItem}
              data-cat-reveal=""
              style={{ "--stagger": idx } as React.CSSProperties}
            >
              <Link href={`/tap-chi/${article.slug}`} className={styles.journalMediaLink}>
                <div className={styles.journalMedia}>
                  <Image
                    src={getSafeJournalCover(article.slug, article.coverImage)}
                    alt={article.title}
                    fill
                    sizes="(max-width: 768px) 100vw, (max-width: 1080px) 50vw, 25vw"
                    className={styles.journalImg}
                  />
                </div>
              </Link>
              <h3 className={styles.journalTitle}>
                <Link href={`/tap-chi/${article.slug}`} className={styles.journalTitleLink}>
                  {article.title}
                </Link>
              </h3>
              <p className={styles.journalSummary}>{article.summary}</p>
              <p className={styles.journalMeta}>
                {article.categoryLabel} · {article.readMinutes} phút đọc
              </p>
            </article>
          ))}
        </div>
      </section>

      {/* =====================================================================
          10. COMMITMENT / TRUST BAR: Typographic + accent rule (ZERO icon cards)
          ===================================================================== */}
      <section className={styles.commitmentSection} aria-labelledby="home-commitment-heading">
        <h2 id="home-commitment-heading" className={styles.commitmentHeading}>
          Cam kết mua sắm tại NexusMart
        </h2>
        <div className={styles.commitmentGrid}>
          {COMMITMENT_ITEMS.map((item) => (
            <div key={item.id} className={styles.commitmentItem}>
              <div className={styles.commitmentBar} aria-hidden="true" />
              <h3 className={styles.commitmentTitle}>{item.title}</h3>
              <p className={styles.commitmentDetail}>{item.detail}</p>
            </div>
          ))}
        </div>
      </section>

      {/* =====================================================================
          11. COMPACT FAQ + WIKIMEDIA / POLY HAVEN CREDITS
          ===================================================================== */}
      <section className={styles.faqSection} aria-labelledby="home-faq-heading">
        <div className={styles.faqGrid}>
          <div className={styles.faqLeft}>
            <h2 id="home-faq-heading" className={styles.sectionTitle}>
              Câu hỏi thường gặp
            </h2>
            <p className={styles.faqNote}>
              Cần hỗ trợ thêm về đơn hàng, đổi size hoặc chọn chất vải theo mùa?
            </p>
            <Link href="/tro-giup" className={styles.sectionLink}>
              Mở trung tâm trợ giúp
            </Link>
          </div>

          <div className={styles.faqBody}>
            <div className={styles.faqColumns}>
              {FAQ_ITEMS.map((item) => (
                <p key={item.q} className={styles.faqItem}>
                  <span className={styles.faqQuestion}>{item.q}</span> {item.a}
                </p>
              ))}
            </div>

            <p className={styles.credits}>
              Tư liệu khung dệt trên trang:{" "}
              <a
                href="https://commons.wikimedia.org/wiki/File:M%C3%A9tier_%C3%A0_tisser_Jacquard_02.webm"
                target="_blank"
                rel="noreferrer"
                className={styles.creditLink}
              >
                Métier à tisser Jacquard 02
              </a>{" "}
              (Bastenbas,{" "}
              <a
                href="https://creativecommons.org/licenses/by-sa/4.0/"
                target="_blank"
                rel="noreferrer"
                className={styles.creditLink}
              >
                CC BY-SA 4.0
              </a>
              ) qua Wikimedia Commons.
            </p>
          </div>
        </div>
      </section>
    </div>
  );
}
