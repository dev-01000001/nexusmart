"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type { SellerSizeChartRow } from "@/data/seller";
import { Button, IconCheck, StateView } from "@/components";
import { formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
import {
  getShopFactoryProfileWithSession,
  getShopOperationsConfigWithSession,
  getShopSizeChartsWithSession,
  parseUiState,
  updateShopFactoryProfile,
  updateShopOperationsConfig,
  updateShopSizeCharts,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type ProfileTab = "factory" | "sizechart" | "shipping";
type SizeGroupKey = "tops" | "bottoms";
type BodyShape = "gay" | "vua" | "day";

function parseRangeMid(rangeStr: string, fallback: number): number {
  const nums = rangeStr.match(/\d+/g);
  if (!nums || nums.length === 0) return fallback;
  if (nums.length === 1) return Number(nums[0]);
  return (Number(nums[0]) + Number(nums[1])) / 2;
}

function recommendSizeFromChart(
  rows: SellerSizeChartRow[],
  heightCm: number,
  weightKg: number,
  shape: BodyShape
): SellerSizeChartRow {
  const shapeWeightAdj = shape === "day" ? 4 : shape === "gay" ? -3 : 0;
  const effWeight = weightKg + shapeWeightAdj;
  let bestIdx = 0;
  let bestScore = Number.POSITIVE_INFINITY;
  rows.forEach((row, idx) => {
    const midH = parseRangeMid(row.heightRange, 170);
    const midW = parseRangeMid(row.weightRange, 68);
    const score = Math.abs(heightCm - midH) * 0.6 + Math.abs(effWeight - midW) * 1.4;
    if (score < bestScore) {
      bestScore = score;
      bestIdx = idx;
    }
  });
  return rows[bestIdx] ?? rows[0];
}

function SellerProfileOpsContent() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as ProfileTab | null;

  const { shop, presentationShopKey, showToast } = useSellerContext();
  const session = useSellerSession();
  const displayShopName = shop?.name ?? "Gian hàng của bạn";

  const [tab, setTab] = useState<ProfileTab>(
    initialTab && ["factory", "sizechart", "shipping"].includes(initialTab) ? initialTab : "factory"
  );

  const profile = getShopFactoryProfileWithSession(presentationShopKey, session);
  const sizeCharts = getShopSizeChartsWithSession(presentationShopKey, session);
  const opsConfig = getShopOperationsConfigWithSession(presentationShopKey, session);

  return (
    <SellerProfileOpsInner
      key={presentationShopKey}
      shopId={presentationShopKey}
      shopName={displayShopName}
      shopShortName={displayShopName}
      uiState={uiState}
      tab={tab}
      setTab={setTab}
      profile={profile}
      sizeCharts={sizeCharts}
      opsConfig={opsConfig}
      onNotify={showToast}
    />
  );
}

interface InnerProps {
  shopId: string;
  shopName: string;
  shopShortName: string;
  uiState: "loading" | "empty" | "error" | null;
  tab: ProfileTab;
  setTab: (t: ProfileTab) => void;
  profile: ReturnType<typeof getShopFactoryProfileWithSession>;
  sizeCharts: ReturnType<typeof getShopSizeChartsWithSession>;
  opsConfig: ReturnType<typeof getShopOperationsConfigWithSession>;
  onNotify: (msg: string) => void;
}

function SellerProfileOpsInner({
  shopId,
  shopName,
  shopShortName,
  uiState,
  tab,
  setTab,
  profile,
  sizeCharts,
  opsConfig,
  onNotify,
}: InnerProps) {
  // Tab 1: Factory profile drafts
  const [factoryName, setFactoryName] = useState(profile.factoryName);
  const [establishedYear, setEstablishedYear] = useState(String(profile.establishedYear));
  const [factoryAddress, setFactoryAddress] = useState(profile.factoryAddress);
  const [monthlyCapacity, setMonthlyCapacity] = useState(profile.monthlyCapacity);
  const [yarnText, setYarnText] = useState(profile.yarnSources.join("\n"));

  // Tab 2: Size charts drafts & live simulator
  const [sizeGroup, setSizeGroup] = useState<SizeGroupKey>("tops");
  const [topsRows, setTopsRows] = useState<SellerSizeChartRow[]>(sizeCharts.tops);
  const [bottomsRows, setBottomsRows] = useState<SellerSizeChartRow[]>(sizeCharts.bottoms);
  const [topsNote, setTopsNote] = useState(sizeCharts.topsNote);
  const [bottomsNote, setBottomsNote] = useState(sizeCharts.bottomsNote);
  const [simHeight, setSimHeight] = useState(174);
  const [simWeight, setSimWeight] = useState(70);
  const [simShape, setSimShape] = useState<BodyShape>("vua");

  // Tab 3: Shipping & Return 45 days drafts
  const [freeShipStr, setFreeShipStr] = useState(String(opsConfig.freeShipThresholdVnd));
  const [shipFeeStr, setShipFeeStr] = useState(String(opsConfig.standardShipFeeVnd));
  const [prepHoursStr, setPrepHoursStr] = useState(String(opsConfig.prepSlaHours));
  const [fastEnabled, setFastEnabled] = useState(opsConfig.fastDeliveryEnabled);
  const [fastArea, setFastArea] = useState(opsConfig.fastDeliveryArea);
  const [fastCutoff, setFastCutoff] = useState(opsConfig.fastDeliveryCutoff);
  const [return45, setReturn45] = useState(opsConfig.return45DaysEnabled);
  const [twoWayEx, setTwoWayEx] = useState(opsConfig.twoWayExchangeEnabled);
  const [exceptionNote, setExceptionNote] = useState(opsConfig.returnExceptionNote);

  const activeRows = sizeGroup === "tops" ? topsRows : bottomsRows;
  const activeSizeNote = sizeGroup === "tops" ? topsNote : bottomsNote;

  const recommendedRow = useMemo(
    () => recommendSizeFromChart(activeRows, simHeight, simWeight, simShape),
    [activeRows, simHeight, simWeight, simShape]
  );

  const handleSaveFactory = (e: React.FormEvent) => {
    e.preventDefault();
    updateShopFactoryProfile(shopId, {
      factoryName: factoryName.trim() || profile.factoryName,
      establishedYear: Math.max(1990, Math.trunc(Number(establishedYear) || 2020)),
      factoryAddress: factoryAddress.trim() || profile.factoryAddress,
      monthlyCapacity: monthlyCapacity.trim() || profile.monthlyCapacity,
      yarnSources: yarnText
        .split("\n")
        .map((s) => s.trim())
        .filter(Boolean),
    });
    onNotify(`Đã lưu hồ sơ xưởng & nguồn sợi cho ${shopShortName}.`);
  };

  const updateSizeRow = (
    idx: number,
    field: keyof Omit<SellerSizeChartRow, "size">,
    val: string
  ) => {
    const updater = (prev: SellerSizeChartRow[]) =>
      prev.map((row, i) => (i === idx ? { ...row, [field]: val } : row));
    if (sizeGroup === "tops") {
      setTopsRows(updater);
    } else {
      setBottomsRows(updater);
    }
  };

  const handleSaveSizeCharts = () => {
    updateShopSizeCharts(shopId, {
      shopId,
      tops: topsRows,
      bottoms: bottomsRows,
      topsNote: topsNote.trim(),
      bottomsNote: bottomsNote.trim(),
      updatedAt: "2026-11-20",
    });
    onNotify(
      `Đã lưu bảng quy đổi size ${sizeGroup === "tops" ? "nhóm Áo" : "nhóm Quần"} cho site gian hàng.`
    );
  };

  const handleSaveOps = (e: React.FormEvent) => {
    e.preventDefault();
    const freeShipThresholdVnd = Math.max(99000, Math.trunc(Number(freeShipStr) || 199000));
    const standardShipFeeVnd = Math.max(0, Math.trunc(Number(shipFeeStr) || 25000));
    const prepSlaHours = Math.max(2, Math.min(48, Math.trunc(Number(prepHoursStr) || 12)));
    updateShopOperationsConfig(shopId, {
      freeShipThresholdVnd,
      standardShipFeeVnd,
      prepSlaHours,
      fastDeliveryEnabled: fastEnabled,
      fastDeliveryArea: fastArea.trim() || opsConfig.fastDeliveryArea,
      fastDeliveryCutoff: fastCutoff.trim() || opsConfig.fastDeliveryCutoff,
      return45DaysEnabled: return45,
      twoWayExchangeEnabled: twoWayEx,
      returnExceptionNote: exceptionNote.trim(),
    });
    onNotify(
      `Đã cập nhật cấu hình giao hàng (freeship từ ${formatVnd(freeShipThresholdVnd)}) và chính sách đổi trả.`
    );
  };

  const liveFreeShip = Math.max(0, Math.trunc(Number(freeShipStr) || 0));
  const liveShipFee = Math.max(0, Math.trunc(Number(shipFeeStr) || 0));

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Hồ sơ xưởng, Bảng size &amp; Vận hành</h1>
          <p className={common.finding}>
            {shopName} · Freeship gian hàng từ {formatVnd(opsConfig.freeShipThresholdVnd)} ·{" "}
            {opsConfig.return45DaysEnabled
              ? "Đổi trả 45 ngày tận nơi"
              : "Đổi trả theo chính sách shop"}{" "}
            · {opsConfig.fastDeliveryEnabled ? "Đang bật giao nhanh nội thành" : "Giao tiêu chuẩn"}
          </p>
        </div>
        <div className={common.headerActions}>
          {tab === "sizechart" && (
            <Button variant="ink" size="sm" shape="app" onClick={handleSaveSizeCharts}>
              <IconCheck size={14} />
              <span>Lưu bảng size xưởng</span>
            </Button>
          )}
          {tab === "factory" && (
            <Button variant="outline" size="sm" shape="app" href="/seller/site">
              Chỉnh giao diện &amp; bố cục site
            </Button>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải hồ sơ xưởng, quy chuẩn QC và bảng quy đổi size..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải cấu hình gian hàng"
          message="Dịch vụ hồ sơ xưởng tạm thời gián đoạn. Vui lòng thử lại."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/ho-so"
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa hoàn thiện hồ sơ xưởng và bảng size"
          message="Khai báo nguồn sợi, quy chuẩn QC và bảng size riêng để hiển thị trên site gian hàng."
          actionLabel="Mở cấu hình ngay"
          actionHref="/seller/ho-so"
        />
      )}

      {!uiState && (
        <>
          <div
            className={common.underlineTabs}
            role="tablist"
            aria-label="Phân mục hồ sơ và vận hành"
          >
            <button
              type="button"
              role="tab"
              aria-selected={tab === "factory"}
              className={`${common.underlineTab} ${tab === "factory" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("factory")}
            >
              Hồ sơ xưởng &amp; QC
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "sizechart"}
              className={`${common.underlineTab} ${tab === "sizechart" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("sizechart")}
            >
              Bảng quy đổi size
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "shipping"}
              className={`${common.underlineTab} ${tab === "shipping" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("shipping")}
            >
              Giao hàng &amp; Đổi trả
            </button>
          </div>

          {/* TAB 1: Factory profile, Yarn sources, QC standards */}
          {tab === "factory" && (
            <div className={styles.twoCol}>
              <form
                className={common.card}
                onSubmit={handleSaveFactory}
                aria-labelledby="factory-info-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="factory-info-heading" className={common.cardTitle}>
                    Thông tin nhà máy &amp; nguồn sợi
                  </h2>
                  <span className={`${common.pill} ${common.pillPos}`}>Đã kiểm định QC</span>
                </div>

                <div className={styles.fieldStack}>
                  <div className={common.field}>
                    <label htmlFor="fac-name" className={common.fieldLabel}>
                      Tên xưởng / nhà máy hợp tác
                    </label>
                    <input
                      id="fac-name"
                      type="text"
                      className={common.textInput}
                      value={factoryName}
                      onChange={(e) => setFactoryName(e.target.value)}
                    />
                  </div>

                  <div className={styles.fields2}>
                    <div className={common.field}>
                      <label htmlFor="fac-year" className={common.fieldLabel}>
                        Năm thành lập
                      </label>
                      <input
                        id="fac-year"
                        type="number"
                        className={common.textInput}
                        value={establishedYear}
                        onChange={(e) => setEstablishedYear(e.target.value)}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="fac-cap" className={common.fieldLabel}>
                        Công suất trung bình
                      </label>
                      <input
                        id="fac-cap"
                        type="text"
                        className={common.textInput}
                        value={monthlyCapacity}
                        onChange={(e) => setMonthlyCapacity(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="fac-addr" className={common.fieldLabel}>
                      Địa chỉ nhà máy sản xuất
                    </label>
                    <input
                      id="fac-addr"
                      type="text"
                      className={common.textInput}
                      value={factoryAddress}
                      onChange={(e) => setFactoryAddress(e.target.value)}
                    />
                  </div>

                  <div className={common.field}>
                    <label htmlFor="fac-yarn" className={common.fieldLabel}>
                      Nguồn sợi &amp; vật liệu chủ lực (mỗi dòng một mục)
                    </label>
                    <textarea
                      id="fac-yarn"
                      className={common.textArea}
                      value={yarnText}
                      onChange={(e) => setYarnText(e.target.value)}
                    />
                  </div>

                  <div className={styles.formActions}>
                    <Button type="submit" variant="ink" size="sm" shape="app">
                      Lưu hồ sơ xưởng
                    </Button>
                  </div>
                </div>
              </form>

              <section className={common.card} aria-labelledby="qc-heading">
                <div className={common.cardHeader}>
                  <h2 id="qc-heading" className={common.cardTitle}>
                    Quy chuẩn kiểm định QC tại xưởng
                  </h2>
                  <Link href="/seller/site" className={common.cardLink}>
                    Tùy chỉnh trang Giới thiệu trên site
                  </Link>
                </div>
                <div className={styles.qcList}>
                  {profile.qcStandards.map((qc) => (
                    <div key={qc.code} className={styles.qcRow}>
                      <div className={styles.qcHead}>
                        <span className={styles.qcTitle}>{qc.title}</span>
                        <span className={`${common.pill} ${common.pillPos}`}>{qc.code}</span>
                      </div>
                      <p className={styles.qcDesc}>{qc.detail}</p>
                    </div>
                  ))}
                  <div className={styles.qcRow}>
                    <div className={styles.qcHead}>
                      <span className={styles.qcTitle}>
                        Quy trình lưu mẫu &amp; đối chiếu chất lượng
                      </span>
                      <span className={common.pill}>Định kỳ quý</span>
                    </div>
                    <p className={styles.qcDesc}>
                      Mọi lô sản xuất lưu mẫu đối chứng tại kho và cho phép đồng kiểm khi giao hàng
                      để đảm bảo chất lượng ổn định trên site gian hàng.
                    </p>
                  </div>
                </div>
              </section>
            </div>
          )}

          {/* TAB 2: Editable Shop Size Charts + Live Recommendation Widget Preview */}
          {tab === "sizechart" && (
            <div className={styles.colStack}>
              <section className={common.card} aria-labelledby="size-chart-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="size-chart-heading" className={common.cardTitle}>
                      Bảng quy đổi thông số size riêng của {shopShortName}
                    </h2>
                    <span className={common.cardMeta}>
                      Bảng gợi ý chọn size trên site gian hàng đọc thông số từ bảng này · Cập nhật{" "}
                      {sizeCharts.updatedAt}
                    </span>
                  </div>
                  <div className={common.segmented} role="tablist" aria-label="Nhóm bảng size">
                    <button
                      type="button"
                      role="tab"
                      aria-selected={sizeGroup === "tops"}
                      className={`${common.segmentedBtn} ${sizeGroup === "tops" ? common.segmentedBtnActive : ""}`}
                      onClick={() => setSizeGroup("tops")}
                    >
                      Nhóm Áo (Thun, Polo, Khoác)
                    </button>
                    <button
                      type="button"
                      role="tab"
                      aria-selected={sizeGroup === "bottoms"}
                      className={`${common.segmentedBtn} ${sizeGroup === "bottoms" ? common.segmentedBtnActive : ""}`}
                      onClick={() => setSizeGroup("bottoms")}
                    >
                      Nhóm Quần (Short, Jogger, Đồ lót)
                    </button>
                  </div>
                </div>

                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Size</th>
                        <th className={common.numCol}>Chiều cao (cm)</th>
                        <th className={common.numCol}>Cân nặng (kg)</th>
                        <th className={common.numCol}>
                          {sizeGroup === "tops" ? "Vòng ngực (cm)" : "Vòng eo (cm)"}
                        </th>
                        <th className={common.numCol}>
                          {sizeGroup === "tops" ? "Rộng vai (cm)" : "Vòng mông (cm)"}
                        </th>
                        <th className={common.numCol}>
                          {sizeGroup === "tops" ? "Dài áo (cm)" : "Dài quần (cm)"}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {activeRows.map((row, idx) => (
                        <tr key={row.size}>
                          <td>
                            <strong>{row.size}</strong>
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="text"
                              className={styles.sizeCellInput}
                              value={row.heightRange}
                              onChange={(e) => updateSizeRow(idx, "heightRange", e.target.value)}
                              aria-label={`Chiều cao size ${row.size}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="text"
                              className={styles.sizeCellInput}
                              value={row.weightRange}
                              onChange={(e) => updateSizeRow(idx, "weightRange", e.target.value)}
                              aria-label={`Cân nặng size ${row.size}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="text"
                              className={styles.sizeCellInput}
                              value={row.chestOrWaistCm}
                              onChange={(e) => updateSizeRow(idx, "chestOrWaistCm", e.target.value)}
                              aria-label={`Vòng ngực hoặc eo size ${row.size}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="text"
                              className={styles.sizeCellInput}
                              value={row.shoulderOrHipCm}
                              onChange={(e) =>
                                updateSizeRow(idx, "shoulderOrHipCm", e.target.value)
                              }
                              aria-label={`Vai hoặc mông size ${row.size}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="text"
                              className={styles.sizeCellInput}
                              value={row.lengthCm}
                              onChange={(e) => updateSizeRow(idx, "lengthCm", e.target.value)}
                              aria-label={`Chiều dài size ${row.size}`}
                            />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {activeRows.map((row, idx) => (
                    <div key={row.size} className={styles.mobileSizeCard}>
                      <span className={styles.mobileSizeHead}>Size {row.size}</span>
                      <div className={styles.mobileSizeGrid}>
                        <div className={common.field}>
                          <label className={common.fieldHint}>Cao (cm)</label>
                          <input
                            type="text"
                            className={styles.sizeCellInput}
                            value={row.heightRange}
                            onChange={(e) => updateSizeRow(idx, "heightRange", e.target.value)}
                          />
                        </div>
                        <div className={common.field}>
                          <label className={common.fieldHint}>Nặng (kg)</label>
                          <input
                            type="text"
                            className={styles.sizeCellInput}
                            value={row.weightRange}
                            onChange={(e) => updateSizeRow(idx, "weightRange", e.target.value)}
                          />
                        </div>
                        <div className={common.field}>
                          <label className={common.fieldHint}>
                            {sizeGroup === "tops" ? "Ngực (cm)" : "Eo (cm)"}
                          </label>
                          <input
                            type="text"
                            className={styles.sizeCellInput}
                            value={row.chestOrWaistCm}
                            onChange={(e) => updateSizeRow(idx, "chestOrWaistCm", e.target.value)}
                          />
                        </div>
                        <div className={common.field}>
                          <label className={common.fieldHint}>Dài (cm)</label>
                          <input
                            type="text"
                            className={styles.sizeCellInput}
                            value={row.lengthCm}
                            onChange={(e) => updateSizeRow(idx, "lengthCm", e.target.value)}
                          />
                        </div>
                      </div>
                    </div>
                  ))}
                </div>

                <div className={styles.fieldStack} style={{ marginTop: 14 }}>
                  <div className={common.field}>
                    <label htmlFor="size-note" className={common.fieldLabel}>
                      Ghi chú phom dáng hiển thị dưới bảng size trên trang sản phẩm
                    </label>
                    <input
                      id="size-note"
                      type="text"
                      className={common.textInput}
                      value={activeSizeNote}
                      onChange={(e) =>
                        sizeGroup === "tops"
                          ? setTopsNote(e.target.value)
                          : setBottomsNote(e.target.value)
                      }
                    />
                  </div>
                  <div className={styles.formActions}>
                    <Button variant="ink" size="sm" shape="app" onClick={handleSaveSizeCharts}>
                      <IconCheck size={14} />
                      <span>Lưu &amp; đồng bộ bảng size</span>
                    </Button>
                  </div>
                </div>
              </section>

              <section className={common.card} aria-labelledby="size-sim-heading">
                <div className={common.cardHeader}>
                  <h2 id="size-sim-heading" className={common.cardTitle}>
                    Kiểm tra trực tiếp gợi ý chọn size của gian hàng
                  </h2>
                  <span className={common.cardMeta}>
                    Xem trước kết quả đề xuất theo số đo người mua từ bảng size xưởng vừa khai báo
                  </span>
                </div>

                <div className={styles.sizePreviewBox}>
                  <div className={styles.fields3}>
                    <div className={common.field}>
                      <label htmlFor="sim-h" className={common.fieldLabel}>
                        Chiều cao kiểm tra: {simHeight} cm
                      </label>
                      <input
                        id="sim-h"
                        type="range"
                        min={155}
                        max={190}
                        value={simHeight}
                        onChange={(e) => setSimHeight(Number(e.target.value))}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="sim-w" className={common.fieldLabel}>
                        Cân nặng kiểm tra: {simWeight} kg
                      </label>
                      <input
                        id="sim-w"
                        type="range"
                        min={48}
                        max={95}
                        value={simWeight}
                        onChange={(e) => setSimWeight(Number(e.target.value))}
                      />
                    </div>
                    <div className={common.field}>
                      <span className={common.fieldLabel}>Dáng người</span>
                      <div className={common.segmented}>
                        {(
                          [
                            ["gay", "Gầy"],
                            ["vua", "Vừa"],
                            ["day", "Đầy"],
                          ] as const
                        ).map(([sh, lbl]) => (
                          <button
                            key={sh}
                            type="button"
                            className={`${common.segmentedBtn} ${simShape === sh ? common.segmentedBtnActive : ""}`}
                            onClick={() => setSimShape(sh)}
                          >
                            {lbl}
                          </button>
                        ))}
                      </div>
                    </div>
                  </div>

                  <div className={styles.sizeResultWell}>
                    <div>
                      <span className={common.fieldLabel}>
                        Gợi ý cỡ cho khách ({simHeight}cm · {simWeight}kg · dáng{" "}
                        {simShape === "gay" ? "Gầy" : simShape === "day" ? "Đầy" : "Vừa"})
                      </span>
                      <p className={styles.previewLine}>
                        Khớp nhóm {sizeGroup === "tops" ? "Áo" : "Quần"} của {shopShortName}: Cao{" "}
                        {recommendedRow.heightRange}cm · Nặng {recommendedRow.weightRange}kg · Dài{" "}
                        {recommendedRow.lengthCm}cm
                      </p>
                    </div>
                    <span className={styles.sizeResultVal}>Size {recommendedRow.size}</span>
                  </div>
                </div>
              </section>
            </div>
          )}

          {/* TAB 3: Shipping threshold, Fast inner-city delivery & Return policy */}
          {tab === "shipping" && (
            <div className={styles.twoCol}>
              <form
                className={common.card}
                onSubmit={handleSaveOps}
                aria-labelledby="shipping-ops-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="shipping-ops-heading" className={common.cardTitle}>
                    Cấu hình vận chuyển và cam kết đổi trả
                  </h2>
                  <span className={common.cardMeta}>
                    Hiển thị trên site riêng, trang sản phẩm và chính sách bán hàng
                  </span>
                </div>

                <div className={styles.fieldStack}>
                  <div className={styles.fields3}>
                    <div className={common.field}>
                      <label htmlFor="ops-freeship" className={common.fieldLabel}>
                        Ngưỡng freeship riêng của shop (VND)
                      </label>
                      <input
                        id="ops-freeship"
                        type="number"
                        step={10000}
                        min={99000}
                        className={common.textInput}
                        value={freeShipStr}
                        onChange={(e) => setFreeShipStr(e.target.value)}
                      />
                      <span className={common.fieldHint}>
                        Áp dụng cho đơn trên site từ {formatVnd(liveFreeShip)}
                      </span>
                    </div>

                    <div className={common.field}>
                      <label htmlFor="ops-shipfee" className={common.fieldLabel}>
                        Cước giao chuẩn (VND)
                      </label>
                      <input
                        id="ops-shipfee"
                        type="number"
                        step={1000}
                        min={0}
                        className={common.textInput}
                        value={shipFeeStr}
                        onChange={(e) => setShipFeeStr(e.target.value)}
                      />
                    </div>

                    <div className={common.field}>
                      <label htmlFor="ops-sla" className={common.fieldLabel}>
                        Hạn chuẩn bị hàng (giờ)
                      </label>
                      <input
                        id="ops-sla"
                        type="number"
                        min={2}
                        max={48}
                        className={common.textInput}
                        value={prepHoursStr}
                        onChange={(e) => setPrepHoursStr(e.target.value)}
                      />
                    </div>
                  </div>

                  <div>
                    <div className={styles.switchRow}>
                      <div className={styles.switchText}>
                        <span className={styles.switchTitle}>Giao nhanh nội thành (2-4 giờ)</span>
                        <span className={styles.switchDesc}>
                          Cho phép khách cùng tỉnh thành chọn giao gấp ngay tại bước thanh toán.
                        </span>
                      </div>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={fastEnabled}
                        onChange={(e) => setFastEnabled(e.target.checked)}
                        aria-label="Bật hoặc tắt giao nhanh nội thành"
                      />
                    </div>

                    {fastEnabled && (
                      <div className={styles.fields2} style={{ marginBottom: 12 }}>
                        <div className={common.field}>
                          <label htmlFor="ops-fast-area" className={common.fieldLabel}>
                            Khu vực giao nhanh
                          </label>
                          <input
                            id="ops-fast-area"
                            type="text"
                            className={common.textInput}
                            value={fastArea}
                            onChange={(e) => setFastArea(e.target.value)}
                          />
                        </div>
                        <div className={common.field}>
                          <label htmlFor="ops-fast-cutoff" className={common.fieldLabel}>
                            Khung giờ cắt đơn trong ngày
                          </label>
                          <input
                            id="ops-fast-cutoff"
                            type="text"
                            className={common.textInput}
                            value={fastCutoff}
                            onChange={(e) => setFastCutoff(e.target.value)}
                          />
                        </div>
                      </div>
                    )}

                    <div className={styles.switchRow}>
                      <div className={styles.switchText}>
                        <span className={styles.switchTitle}>
                          Cam kết đổi trả miễn phí 45 ngày (Huy hiệu xưởng)
                        </span>
                        <span className={styles.switchDesc}>
                          Hiển thị huy hiệu Đổi trả 45 ngày trên site riêng và các sản phẩm áp dụng.
                        </span>
                      </div>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={return45}
                        onChange={(e) => setReturn45(e.target.checked)}
                        aria-label="Bật cam kết đổi trả 45 ngày"
                      />
                    </div>

                    <div className={styles.switchRow}>
                      <div className={styles.switchText}>
                        <span className={styles.switchTitle}>Hỗ trợ đổi size 2 chiều tận nơi</span>
                        <span className={styles.switchDesc}>
                          Bưu tá giao size mới và thu hồi hàng đổi trong cùng 1 chuyến tại nhà
                          khách.
                        </span>
                      </div>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={twoWayEx}
                        onChange={(e) => setTwoWayEx(e.target.checked)}
                        aria-label="Bật đổi size 2 chiều tận nơi"
                      />
                    </div>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="ops-exc" className={common.fieldLabel}>
                      Ghi chú ngoại lệ đổi trả của gian hàng
                    </label>
                    <textarea
                      id="ops-exc"
                      className={common.textArea}
                      value={exceptionNote}
                      onChange={(e) => setExceptionNote(e.target.value)}
                    />
                  </div>

                  <div className={styles.formActions}>
                    <Button type="submit" variant="ink" size="sm" shape="app">
                      <IconCheck size={14} />
                      <span>Lưu cấu hình vận hành</span>
                    </Button>
                  </div>
                </div>
              </form>

              <section className={common.card} aria-labelledby="badge-preview-heading">
                <div className={common.cardHeader}>
                  <h2 id="badge-preview-heading" className={common.cardTitle}>
                    Xem trước cam kết trên site gian hàng
                  </h2>
                </div>

                <div className={styles.badgePreviewList}>
                  {opsConfig.verifiedWorkshopBadge && (
                    <span className={`${common.pill} ${common.pillPos}`}>Xưởng chuẩn QC</span>
                  )}
                  {return45 && (
                    <span className={`${common.pill} ${common.pillPos}`}>Đổi trả 45 ngày</span>
                  )}
                  {twoWayEx && (
                    <span className={`${common.pill} ${common.pillPos}`}>
                      Đổi size 2 chiều tận nơi
                    </span>
                  )}
                  {fastEnabled && <span className={common.pill}>Giao nhanh nội thành</span>}
                </div>

                <div className={styles.previewWell}>
                  <span className={styles.previewHeading}>Chính sách giao hàng của gian hàng</span>
                  <p className={styles.previewLine}>
                    Miễn phí vận chuyển cho đơn từ <strong>{formatVnd(liveFreeShip)}</strong> tại{" "}
                    {shopShortName} (tiết kiệm {formatVnd(liveShipFee)} phí giao tiêu chuẩn).
                  </p>
                  <p className={styles.previewLine}>
                    Thời gian chuẩn bị tại kho: tối đa {prepHoursStr || "12"} giờ làm việc.
                  </p>
                  {fastEnabled && (
                    <p className={styles.previewLine}>
                      Giao nhanh nội thành: {fastArea} ({fastCutoff}).
                    </p>
                  )}
                  <p className={styles.previewLine}>{exceptionNote}</p>
                </div>
              </section>
            </div>
          )}
        </>
      )}
    </div>
  );
}

export default function SellerProfileOpsPage() {
  return (
    <SellerSuspense>
      <SellerProfileOpsContent />
    </SellerSuspense>
  );
}
