"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import type { CategorySlug, Product, Shop } from "@/data";
import type { CatalogProduct, CatalogShop } from "@/lib/catalog-types";
import { useMockStore } from "@/mock/MockStoreContext";
import { getEditorialCatalogFallback } from "@/mock/productEditorial";
import { resolveSizeFinderProductPricing } from "@/lib/a8-types";
import { Price } from "@/components/Price";
import { BODY_SHAPE_LABELS, recommendSize, type BodyShape, type FitPreference } from "@/lib/sizing";
import styles from "./SizeFinder.module.css";

type GarmentGroup = "ao" | "quan" | "dolot";

export interface SizeFinderProps {
  defaultShopId?: string;
  products?: Array<Product | CatalogProduct>;
  shops?: Array<Shop | CatalogShop>;
}

const GROUP_CATEGORIES: Record<GarmentGroup, CategorySlug[]> = {
  ao: ["ao-thun", "ao-polo"],
  quan: ["quan-short", "quan-jogger"],
  dolot: ["do-lot"],
};

const GROUP_LABELS: Record<GarmentGroup, string> = {
  ao: "Áo thun & Polo",
  quan: "Quần short & Jogger",
  dolot: "Đồ lót",
};

const GROUP_TO_SIZING: Record<GarmentGroup, "top" | "bottom" | "underwear"> = {
  ao: "top",
  quan: "bottom",
  dolot: "underwear",
};

const FIT_LABELS: Record<FitPreference, string> = {
  om: "Ôm gọn",
  vua: "Vừa vặn chuẩn",
  rong: "Rộng thoải mái",
};

const SHAPE_KEYS: BodyShape[] = ["gay", "vua", "day"];

const FIT_BIAS_SHORT: Record<"slim" | "regular" | "relaxed", string> = {
  slim: "Form ôm",
  regular: "Form chuẩn",
  relaxed: "Form thoáng",
};

function getEditorialShopId(shop: Shop | CatalogShop | undefined): string | undefined {
  if (!shop) return undefined;
  if ("editorialShopId" in shop) return shop.editorialShopId;
  return shop.id;
}

export function SizeFinder({ defaultShopId, products, shops }: SizeFinderProps) {
  const { bodyProfile, updateBodyProfile, showToast } = useMockStore();

  // Only fall back to editorial catalog when embedded inside mock-only pages (e.g. /tap-chi)
  const fallback = useMemo(() => {
    if (products && shops) return null;
    return getEditorialCatalogFallback();
  }, [products, shops]);

  const effectiveProducts = useMemo(
    () => products ?? fallback?.products ?? [],
    [products, fallback]
  );
  const effectiveShops = useMemo(() => shops ?? fallback?.shops ?? [], [shops, fallback]);

  const [heightCm, setHeightCm] = useState<number>(bodyProfile.heightCm);
  const [weightKg, setWeightKg] = useState<number>(bodyProfile.weightKg);
  const [shape, setShape] = useState<BodyShape>(bodyProfile.shape);
  const [preferredFit, setPreferredFit] = useState<FitPreference>(
    bodyProfile.preferredFit ?? "vua"
  );
  const [group, setGroup] = useState<GarmentGroup>("ao");
  const [selectedShopId, setSelectedShopId] = useState<string>(defaultShopId ?? "all");

  // Đồng bộ form khi hồ sơ số đo (tiện ích người xem lưu trong MockStore) vừa được nạp sau hydration
  useEffect(() => {
    setHeightCm(bodyProfile.heightCm);
    setWeightKg(bodyProfile.weightKg);
    setShape(bodyProfile.shape);
    setPreferredFit(bodyProfile.preferredFit ?? "vua");
  }, [bodyProfile.heightCm, bodyProfile.weightKg, bodyProfile.shape, bodyProfile.preferredFit]);

  useEffect(() => {
    if (defaultShopId) {
      setSelectedShopId(defaultShopId);
    }
  }, [defaultShopId]);

  const selectedShop = useMemo(
    () =>
      selectedShopId === "all"
        ? undefined
        : effectiveShops.find((s) => s.id === selectedShopId || s.slug === selectedShopId),
    [effectiveShops, selectedShopId]
  );

  const recommendation = useMemo(
    () =>
      recommendSize({
        heightCm,
        weightKg,
        shape,
        preferredFit,
        garmentGroup: GROUP_TO_SIZING[group],
        shopId: selectedShop ? getEditorialShopId(selectedShop) : undefined,
      }),
    [heightCm, weightKg, shape, preferredFit, group, selectedShop]
  );

  const recommendedSize = recommendation.size;

  const isSynced =
    heightCm === bodyProfile.heightCm &&
    weightKg === bodyProfile.weightKg &&
    shape === bodyProfile.shape &&
    preferredFit === (bodyProfile.preferredFit ?? "vua");

  const handleSaveProfile = () => {
    updateBodyProfile({ heightCm, weightKg, shape, preferredFit });
    showToast("Đã lưu hồ sơ số đo dùng chung cho toàn bộ gian hàng.");
  };

  const matchingProducts = useMemo(() => {
    const allowedCats = GROUP_CATEGORIES[group];
    const inShopPool =
      selectedShopId === "all"
        ? effectiveProducts
        : effectiveProducts.filter((p) => p.shopId === selectedShopId);
    const primaryMatches = inShopPool
      .filter((p) => allowedCats.includes(p.categorySlug))
      .map((product) => {
        const sizeStock = product.skus
          .filter((s) => s.size === recommendedSize)
          .reduce((sum, s) => sum + s.stock, 0);
        return { product, sizeStock };
      })
      .filter((entry) => entry.sizeStock > 0);

    if (primaryMatches.length > 0) return primaryMatches.slice(0, 3);

    // Fallback across all shops when a specific workshop does not produce the chosen garment group
    return effectiveProducts
      .filter((p) => allowedCats.includes(p.categorySlug))
      .map((product) => {
        const sizeStock = product.skus
          .filter((s) => s.size === recommendedSize)
          .reduce((sum, s) => sum + s.stock, 0);
        return { product, sizeStock };
      })
      .filter((entry) => entry.sizeStock > 0)
      .slice(0, 3);
  }, [effectiveProducts, group, recommendedSize, selectedShopId]);

  const referenceSpec = useMemo(() => {
    if (recommendation.measurements) {
      return {
        heightCm: `${recommendation.measurements.heightMin}-${recommendation.measurements.heightMax}`,
        weightKg: `${recommendation.measurements.weightMin}-${recommendation.measurements.weightMax}`,
        chestOrWaistCm: recommendation.measurements.chestOrWaistCm,
        lengthCm: recommendation.measurements.lengthCm,
      };
    }
    const sample = matchingProducts[0]?.product ?? effectiveProducts[0];
    if (!sample || sample.sizeChart.length === 0) return null;
    return (
      sample.sizeChart.find((row) => row.size === recommendedSize) ??
      sample.sizeChart[2] ??
      sample.sizeChart[0]
    );
  }, [effectiveProducts, matchingProducts, recommendation.measurements, recommendedSize]);

  const clampHeight = (v: number) => Math.max(155, Math.min(190, v));
  const clampWeight = (v: number) => Math.max(48, Math.min(95, v));

  const catalogFilterHref = selectedShop
    ? `/danh-muc?size=${recommendedSize}&cat=${GROUP_CATEGORIES[group][0]}&shop=${encodeURIComponent(selectedShop.slug)}`
    : `/danh-muc?size=${recommendedSize}&cat=${GROUP_CATEGORIES[group][0]}`;

  const selectableShops = useMemo(
    () =>
      effectiveShops.filter((s) => Boolean(s.sizeCharts) || s.id === selectedShopId).slice(0, 4),
    [effectiveShops, selectedShopId]
  );

  const displayedShopName = selectedShop?.name ?? recommendation.shopName;

  return (
    <div className={styles.wrap} id="chon-size">
      <div className={styles.controlsCol}>
        <div className={styles.headingRow}>
          <h3 className={styles.title}>Gợi ý size theo chiều cao &amp; cân nặng</h3>
          <p className={styles.lead}>
            Chọn chiều cao, cân nặng và dáng người để nhận size phù hợp nhất cho từng xưởng dệt.
          </p>
        </div>

        <div className={styles.fitRow}>
          <span className={styles.fitLabel}>Bảng size theo xưởng dệt</span>
          <div className={styles.groupRow} role="group" aria-label="Chọn bảng size theo xưởng">
            <button
              type="button"
              className={`${styles.choiceBtn} ${selectedShopId === "all" ? styles.choiceBtnActive : ""}`}
              onClick={() => setSelectedShopId("all")}
              aria-pressed={selectedShopId === "all"}
            >
              Chuẩn NexusMart
            </button>
            {selectableShops.map((shop) => {
              const active = selectedShopId === shop.id;
              const bias = shop.sizeCharts?.fitBias ?? "regular";
              return (
                <button
                  key={shop.id}
                  type="button"
                  className={`${styles.choiceBtn} ${active ? styles.choiceBtnActive : ""}`}
                  onClick={() => setSelectedShopId(shop.id)}
                  aria-pressed={active}
                >
                  {shop.shortName} · {FIT_BIAS_SHORT[bias]}
                </button>
              );
            })}
          </div>
        </div>

        <div className={styles.fitRow}>
          <span className={styles.fitLabel}>Nhóm sản phẩm</span>
          <div className={styles.groupRow} role="group" aria-label="Chọn nhóm sản phẩm">
            {(Object.keys(GROUP_LABELS) as GarmentGroup[]).map((key) => (
              <button
                key={key}
                type="button"
                className={`${styles.choiceBtn} ${group === key ? styles.choiceBtnActive : ""}`}
                onClick={() => setGroup(key)}
                aria-pressed={group === key}
              >
                {GROUP_LABELS[key]}
              </button>
            ))}
          </div>
        </div>

        <div className={styles.slidersGrid}>
          <div className={styles.metricControl}>
            <div className={styles.metricTop}>
              <label htmlFor="size-finder-height" className={styles.metricLabel}>
                Chiều cao
              </label>
              <span className={styles.metricValue}>{heightCm} cm</span>
            </div>
            <div className={styles.sliderRow}>
              <button
                type="button"
                className={styles.stepBtn}
                onClick={() => setHeightCm((h) => clampHeight(h - 1))}
                aria-label="Giảm 1 cm chiều cao"
              >
                −
              </button>
              <input
                id="size-finder-height"
                type="range"
                min={155}
                max={190}
                value={heightCm}
                onChange={(e) => setHeightCm(clampHeight(Number(e.target.value)))}
                className={styles.rangeInput}
              />
              <button
                type="button"
                className={styles.stepBtn}
                onClick={() => setHeightCm((h) => clampHeight(h + 1))}
                aria-label="Tăng 1 cm chiều cao"
              >
                +
              </button>
            </div>
          </div>

          <div className={styles.metricControl}>
            <div className={styles.metricTop}>
              <label htmlFor="size-finder-weight" className={styles.metricLabel}>
                Cân nặng
              </label>
              <span className={styles.metricValue}>{weightKg} kg</span>
            </div>
            <div className={styles.sliderRow}>
              <button
                type="button"
                className={styles.stepBtn}
                onClick={() => setWeightKg((w) => clampWeight(w - 1))}
                aria-label="Giảm 1 kg cân nặng"
              >
                −
              </button>
              <input
                id="size-finder-weight"
                type="range"
                min={48}
                max={95}
                value={weightKg}
                onChange={(e) => setWeightKg(clampWeight(Number(e.target.value)))}
                className={styles.rangeInput}
              />
              <button
                type="button"
                className={styles.stepBtn}
                onClick={() => setWeightKg((w) => clampWeight(w + 1))}
                aria-label="Tăng 1 kg cân nặng"
              >
                +
              </button>
            </div>
          </div>
        </div>

        <div className={styles.fitRow}>
          <span className={styles.fitLabel}>Dáng người</span>
          <div className={styles.groupRow} role="group" aria-label="Chọn dáng người">
            {SHAPE_KEYS.map((key) => (
              <button
                key={key}
                type="button"
                className={`${styles.choiceBtn} ${shape === key ? styles.choiceBtnActive : ""}`}
                onClick={() => setShape(key)}
                aria-pressed={shape === key}
              >
                {BODY_SHAPE_LABELS[key].label}
              </button>
            ))}
          </div>
          <p className={styles.shapeHint}>{BODY_SHAPE_LABELS[shape].hint}</p>
        </div>

        <div className={styles.fitRow}>
          <span className={styles.fitLabel}>Cảm giác mặc mong muốn</span>
          <div className={styles.groupRow} role="group" aria-label="Chọn cảm giác mặc">
            {(Object.keys(FIT_LABELS) as FitPreference[]).map((key) => (
              <button
                key={key}
                type="button"
                className={`${styles.choiceBtn} ${preferredFit === key ? styles.choiceBtnActive : ""}`}
                onClick={() => setPreferredFit(key)}
                aria-pressed={preferredFit === key}
              >
                {FIT_LABELS[key]}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className={styles.resultCol} aria-live="polite">
        <div className={styles.resultHead}>
          <div className={styles.resultHeadMeta}>
            <span className={styles.resultSizeLabel}>Size gợi ý · {displayedShopName}</span>
            <span className={styles.confidenceText}>{recommendation.confidenceLabel}</span>
          </div>
          <span className={styles.resultSize}>{recommendedSize}</span>
        </div>

        <p className={styles.resultReason}>{recommendation.reason}</p>
        <p className={styles.shopNote}>{recommendation.shopSizeNote}</p>

        {referenceSpec && (
          <div className={styles.specGrid}>
            <div className={styles.specItem}>
              <span className={styles.specKey}>Khung chiều cao</span>
              <span className={styles.specVal}>{referenceSpec.heightCm} cm</span>
            </div>
            <div className={styles.specItem}>
              <span className={styles.specKey}>Khung cân nặng</span>
              <span className={styles.specVal}>{referenceSpec.weightKg} kg</span>
            </div>
            <div className={styles.specItem}>
              <span className={styles.specKey}>{group === "ao" ? "Vòng ngực áo" : "Vòng eo"}</span>
              <span className={styles.specVal}>{referenceSpec.chestOrWaistCm} cm</span>
            </div>
            <div className={styles.specItem}>
              <span className={styles.specKey}>Chiều dài</span>
              <span className={styles.specVal}>{referenceSpec.lengthCm} cm</span>
            </div>
          </div>
        )}

        <p className={styles.matchHeading}>
          Đang còn hàng size {recommendedSize} ({GROUP_LABELS[group]})
        </p>

        <div className={styles.matchList}>
          {matchingProducts.map(({ product, sizeStock }) => (
            <Link
              key={product.id}
              href={`/san-pham/${product.slug}?size=${recommendedSize}`}
              className={styles.matchItem}
            >
              <img
                src={product.image}
                alt={product.name}
                width={88}
                height={116}
                loading="lazy"
                className={styles.matchThumb}
              />
              <span className={styles.matchInfo}>
                <span className={styles.matchName}>{product.shortName}</span>
                <span className={styles.matchStock}>
                  {product.gsm > 0 ? `${product.gsm}gsm · ` : ""}Còn {sizeStock} chiếc size{" "}
                  {recommendedSize}
                </span>
              </span>
              {(() => {
                const pricing = resolveSizeFinderProductPricing(product);
                return (
                  <Price
                    amount={pricing.displayPriceVnd}
                    originalAmount={pricing.originalPriceVnd}
                    size="sm"
                    className={styles.matchPrice}
                  />
                );
              })()}
            </Link>
          ))}
        </div>

        <div className={styles.resultActions}>
          <button
            type="button"
            className={`${styles.saveProfileBtn} ${isSynced ? styles.saveProfileBtnSynced : ""}`}
            onClick={handleSaveProfile}
          >
            {isSynced ? "Đã lưu số đo của bạn" : "Lưu số đo cho lần mua sau"}
          </button>

          <Link href={catalogFilterHref} className={styles.catalogAction}>
            Lọc mẫu còn size {recommendedSize}
          </Link>
        </div>
      </div>
    </div>
  );
}
