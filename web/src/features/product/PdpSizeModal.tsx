"use client";

import React, { useEffect, useMemo, useState } from "react";
import type { Product, Shop, SizeCode } from "@/data";
import { useStore } from "@/context/StoreContext";
import { BODY_SHAPE_LABELS, recommendSize, type BodyShape, type FitPreference } from "@/lib/sizing";
import { Button, IconClose } from "@/components";
import styles from "./PdpSizeModal.module.css";

export interface PdpSizeModalProps {
  open: boolean;
  initialTab: "chart" | "finder";
  onClose: () => void;
  product: Product;
  shop: Shop;
  selectedSize: SizeCode;
  onSelectSize: (size: SizeCode) => void;
}

const FIT_PREF_OPTIONS: Array<{ id: FitPreference; label: string }> = [
  { id: "om", label: "Mặc ôm gọn" },
  { id: "vua", label: "Vừa vặn" },
  { id: "rong", label: "Thoải mái rộng" },
];

const FIT_BIAS_LABEL: Record<"slim" | "regular" | "relaxed", string> = {
  slim: "Form xưởng ôm gọn",
  regular: "Form xưởng tiêu chuẩn",
  relaxed: "Form xưởng rộng thoáng",
};

const SHAPES: BodyShape[] = ["gay", "vua", "day"];

export function PdpSizeModal({
  open,
  initialTab,
  onClose,
  product,
  shop,
  selectedSize,
  onSelectSize,
}: PdpSizeModalProps) {
  const { bodyProfile, updateBodyProfile } = useStore();
  const [tab, setTab] = useState<"chart" | "finder">(initialTab);
  const [heightCm, setHeightCm] = useState<number>(bodyProfile.heightCm);
  const [weightKg, setWeightKg] = useState<number>(bodyProfile.weightKg);
  const [shape, setShape] = useState<BodyShape>(bodyProfile.shape);
  const [preferredFit, setPreferredFit] = useState<FitPreference>(
    bodyProfile.preferredFit ?? "vua"
  );
  const [shoeSizeEu, setShoeSizeEu] = useState<number>(41);

  useEffect(() => {
    if (!open) return;
    setTab(initialTab);
    setHeightCm(bodyProfile.heightCm);
    setWeightKg(bodyProfile.weightKg);
    setShape(bodyProfile.shape);
    setPreferredFit(bodyProfile.preferredFit ?? "vua");
  }, [open, initialTab, bodyProfile]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  const garmentGroup = useMemo(() => {
    if (product.shopSizeChart) return product.shopSizeChart;
    if (product.categorySlug === "ao-thun" || product.categorySlug === "ao-polo") return "top";
    if (product.categorySlug === "quan-jogger") return "jogger";
    if (product.categorySlug === "do-lot") return "underwear";
    if (product.categorySlug === "tat-phu-kien" && product.id !== "prod-aokhoac-gio") return "sock";
    return product.id === "prod-aokhoac-gio" ? "top" : "bottom";
  }, [product]);

  const recommendation = useMemo(
    () =>
      recommendSize({
        heightCm,
        weightKg,
        shape,
        preferredFit,
        garmentGroup,
        shopId: shop.id,
        availableSizes: product.sizes,
        shoeSizeEu: garmentGroup === "sock" ? shoeSizeEu : undefined,
      }),
    [heightCm, weightKg, shape, preferredFit, garmentGroup, shop.id, product.sizes, shoeSizeEu]
  );

  if (!open) return null;

  const isTop = garmentGroup === "top";
  const isSock = garmentGroup === "sock";
  const shopTopRows = isTop ? shop.sizeCharts?.top : undefined;
  const fitBias = shop.sizeCharts?.fitBias ?? "regular";

  const handleSaveProfile = () => {
    updateBodyProfile({
      heightCm,
      weightKg,
      shape,
      preferredFit,
    });
  };

  const handlePickRecommended = (sz: SizeCode) => {
    onSelectSize(sz);
    onClose();
  };

  return (
    <div
      className={styles.backdrop}
      role="dialog"
      aria-modal="true"
      aria-labelledby="pdp-size-modal-title"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className={styles.panel}>
        <div className={styles.header}>
          <div className={styles.headerText}>
            <h2 id="pdp-size-modal-title" className={styles.title}>
              Bảng size và gợi ý cỡ {shop.shortName}
            </h2>
            <p className={styles.subtitle}>
              {FIT_BIAS_LABEL[fitBias]} · {product.fit}
            </p>
          </div>
          <button
            type="button"
            className={styles.closeBtn}
            onClick={onClose}
            aria-label="Đóng bảng size"
          >
            <IconClose size={18} />
          </button>
        </div>

        <div className={styles.tabRow} role="tablist" aria-label="Chế độ xem kích cỡ">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "chart"}
            className={`${styles.tabBtn} ${tab === "chart" ? styles.tabBtnActive : ""}`}
            onClick={() => setTab("chart")}
          >
            Bảng thông số đo
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "finder"}
            className={`${styles.tabBtn} ${tab === "finder" ? styles.tabBtnActive : ""}`}
            onClick={() => setTab("finder")}
          >
            Gợi ý size theo số đo
          </button>
        </div>

        {tab === "chart" ? (
          <div className={styles.body}>
            {shop.sizeCharts?.sizeNote && (
              <p className={styles.shopNote}>{shop.sizeCharts.sizeNote}</p>
            )}

            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th scope="col">Size</th>
                    <th scope="col">{isSock ? "Cỡ giày" : "Cao (cm)"}</th>
                    <th scope="col">{isSock ? "Chiều dài chân" : "Nặng (kg)"}</th>
                    <th scope="col">{isTop ? "Ngực (cm)" : isSock ? "Độ co giãn" : "Eo (cm)"}</th>
                    {shopTopRows && <th scope="col">Vai (cm)</th>}
                    <th scope="col">Dài (cm)</th>
                  </tr>
                </thead>
                <tbody>
                  {shopTopRows
                    ? shopTopRows
                        .filter((r) => product.sizes.includes(r.size))
                        .map((row) => {
                          const isCurrent = row.size === selectedSize;
                          return (
                            <tr key={row.size} className={isCurrent ? styles.rowActive : undefined}>
                              <td>
                                <button
                                  type="button"
                                  className={styles.sizePickCell}
                                  onClick={() => handlePickRecommended(row.size)}
                                >
                                  {row.size}
                                </button>
                              </td>
                              <td>
                                {row.heightMin}-{row.heightMax}
                              </td>
                              <td>
                                {row.weightMin}-{row.weightMax}
                              </td>
                              <td>{row.chestOrWaistCm}</td>
                              <td>{row.shoulderCm ?? "-"}</td>
                              <td>{row.lengthCm}</td>
                            </tr>
                          );
                        })
                    : product.sizeChart.map((row) => {
                        const isCurrent = row.size === selectedSize;
                        return (
                          <tr key={row.size} className={isCurrent ? styles.rowActive : undefined}>
                            <td>
                              <button
                                type="button"
                                className={styles.sizePickCell}
                                onClick={() => handlePickRecommended(row.size)}
                              >
                                {row.size}
                              </button>
                            </td>
                            <td>{row.heightCm}</td>
                            <td>{row.weightKg}</td>
                            <td>{row.chestOrWaistCm}</td>
                            <td>{row.lengthCm}</td>
                          </tr>
                        );
                      })}
                </tbody>
              </table>
            </div>

            <div className={styles.chartFooter}>
              <p className={styles.footnote}>
                Sai số thực tế 1 đến 1,5 cm theo độ co giãn sợi dệt. Bấm trực tiếp vào mã cỡ trong
                bảng để áp dụng ngay.
              </p>
              <Button variant="secondary" size="md" onClick={() => setTab("finder")}>
                Tính size theo chiều cao, cân nặng
              </Button>
            </div>
          </div>
        ) : (
          <div className={styles.body}>
            <div className={styles.finderGrid}>
              <div className={styles.controlsCol}>
                <div className={styles.field}>
                  <div className={styles.fieldTop}>
                    <label htmlFor="pdp-height-range" className={styles.label}>
                      Chiều cao
                    </label>
                    <span className={styles.valueNum}>{heightCm} cm</span>
                  </div>
                  <input
                    id="pdp-height-range"
                    type="range"
                    min={150}
                    max={195}
                    value={heightCm}
                    onChange={(e) => setHeightCm(Number(e.target.value))}
                    className={styles.slider}
                  />
                </div>

                <div className={styles.field}>
                  <div className={styles.fieldTop}>
                    <label htmlFor="pdp-weight-range" className={styles.label}>
                      Cân nặng
                    </label>
                    <span className={styles.valueNum}>{weightKg} kg</span>
                  </div>
                  <input
                    id="pdp-weight-range"
                    type="range"
                    min={42}
                    max={100}
                    value={weightKg}
                    onChange={(e) => setWeightKg(Number(e.target.value))}
                    className={styles.slider}
                  />
                </div>

                {isSock && (
                  <div className={styles.field}>
                    <div className={styles.fieldTop}>
                      <label htmlFor="pdp-shoe-range" className={styles.label}>
                        Cỡ giày (EU)
                      </label>
                      <span className={styles.valueNum}>{shoeSizeEu} EU</span>
                    </div>
                    <input
                      id="pdp-shoe-range"
                      type="range"
                      min={36}
                      max={46}
                      value={shoeSizeEu}
                      onChange={(e) => setShoeSizeEu(Number(e.target.value))}
                      className={styles.slider}
                    />
                  </div>
                )}

                <div className={styles.field}>
                  <span className={styles.label}>Dáng người</span>
                  <div className={styles.choiceRow} role="group" aria-label="Chọn dáng người">
                    {SHAPES.map((s) => (
                      <button
                        key={s}
                        type="button"
                        className={`${styles.choiceBtn} ${shape === s ? styles.choiceBtnActive : ""}`}
                        onClick={() => setShape(s)}
                        aria-pressed={shape === s}
                      >
                        {s === "gay" ? "Gầy" : s === "vua" ? "Vừa" : "Đầy"}
                      </button>
                    ))}
                  </div>
                  <p className={styles.hintText}>{BODY_SHAPE_LABELS[shape].hint}</p>
                </div>

                <div className={styles.field}>
                  <span className={styles.label}>Cảm giác mặc mong muốn</span>
                  <div className={styles.choiceRow} role="group" aria-label="Cảm giác mặc">
                    {FIT_PREF_OPTIONS.map((opt) => (
                      <button
                        key={opt.id}
                        type="button"
                        className={`${styles.choiceBtn} ${preferredFit === opt.id ? styles.choiceBtnActive : ""}`}
                        onClick={() => setPreferredFit(opt.id)}
                        aria-pressed={preferredFit === opt.id}
                      >
                        {opt.label}
                      </button>
                    ))}
                  </div>
                </div>
              </div>

              <div className={styles.resultCol} aria-live="polite">
                <div className={styles.resultHeader}>
                  <span className={styles.resultLabel}>Cỡ đề xuất cho {shop.shortName}</span>
                  <span className={styles.resultConfidence}>{recommendation.confidenceLabel}</span>
                </div>

                <p className={styles.resultSize}>Size {recommendation.size}</p>
                <p className={styles.resultReason}>{recommendation.reason}</p>

                {recommendation.measurements && (
                  <p className={styles.resultSpec}>
                    Thông số cỡ {recommendation.size}: {isTop ? "Ngực" : "Eo"}{" "}
                    {recommendation.measurements.chestOrWaistCm} cm · Dài{" "}
                    {recommendation.measurements.lengthCm} cm
                    {recommendation.measurements.shoulderCm
                      ? ` · Vai ${recommendation.measurements.shoulderCm} cm`
                      : ""}
                  </p>
                )}

                <p className={styles.resultNote}>{recommendation.shopSizeNote}</p>

                <div className={styles.resultActions}>
                  <Button
                    variant="primary"
                    size="md"
                    fullWidth
                    onClick={() => handlePickRecommended(recommendation.size)}
                  >
                    Chọn size {recommendation.size} ngay
                  </Button>
                  {recommendation.alternativeSize &&
                    product.sizes.includes(recommendation.alternativeSize) && (
                      <Button
                        variant="secondary"
                        size="md"
                        fullWidth
                        onClick={() => handlePickRecommended(recommendation.alternativeSize!)}
                      >
                        Hoặc chọn size {recommendation.alternativeSize}
                      </Button>
                    )}
                  <Button variant="ink" size="md" fullWidth onClick={handleSaveProfile}>
                    Lưu số đo vào hồ sơ toàn sàn
                  </Button>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
