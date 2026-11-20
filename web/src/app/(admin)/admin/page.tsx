"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { computeAdminDailySeries, computeReconciliationSummary } from "@/data";
import { useStore } from "@/context/StoreContext";
import { BarChart, Button, LineChart } from "@/components";
import { formatCompactVnd, formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import {
  computeChannelBreakdown,
  formatShortTimeDateVi,
  RANGE_LABELS,
  SHOP_APPROVAL_META,
} from "@/features/admin/adminMeta";
import type { AdminRangeKey } from "@/features/admin/adminMeta";
import { useAdminAuditLogs } from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type OverviewScope = "toan_san" | "thanh_toan" | "van_hanh";

const SCOPE_TABS: Array<{ id: OverviewScope; label: string }> = [
  { id: "toan_san", label: "Toàn sàn" },
  { id: "thanh_toan", label: "Thanh toán" },
  { id: "van_hanh", label: "Vận hành" },
];

function AdminOverviewContent() {
  const { shopApprovals, disputes, reconciliationRows } = useStore();
  const auditLogs = useAdminAuditLogs();

  const [scope, setScope] = useState<OverviewScope>("toan_san");
  const [range, setRange] = useState<AdminRangeKey>("7d");

  const summary = useMemo(
    () => computeReconciliationSummary(reconciliationRows),
    [reconciliationRows]
  );
  const dailySeries = useMemo(
    () => computeAdminDailySeries(reconciliationRows),
    [reconciliationRows]
  );
  const channelBreakdown = useMemo(
    () => computeChannelBreakdown(reconciliationRows),
    [reconciliationRows]
  );

  const pendingApprovals = useMemo(
    () => shopApprovals.filter((a) => a.status === "cho_duyet"),
    [shopApprovals]
  );
  const supplementApprovals = useMemo(
    () => shopApprovals.filter((a) => a.status === "bo_sung_ho_so"),
    [shopApprovals]
  );
  const openDisputes = useMemo(() => disputes.filter((d) => d.status !== "da_xu_ly"), [disputes]);
  const openDisputeAmount = useMemo(
    () => openDisputes.reduce((sum, d) => sum + d.amount, 0),
    [openDisputes]
  );

  const priorityShopQueue = useMemo(() => {
    const rank = (status: string) =>
      status === "cho_duyet" ? 0 : status === "bo_sung_ho_so" ? 1 : 2;
    return [...shopApprovals]
      .sort((a, b) => rank(a.status) - rank(b.status) || a.slaHoursLeft - b.slaHoursLeft)
      .slice(0, 4);
  }, [shopApprovals]);

  const recentAuditLogs = useMemo(() => auditLogs.slice(0, 4), [auditLogs]);

  const peakIndex = useMemo(() => {
    let bestIdx = 0;
    for (let i = 1; i < dailySeries.length; i++) {
      if (dailySeries[i].grossCollected > dailySeries[bestIdx].grossCollected) {
        bestIdx = i;
      }
    }
    return bestIdx;
  }, [dailySeries]);

  const peakDay = dailySeries[peakIndex];

  const findingLine =
    summary.totalDiscrepancy > 0
      ? `Hiện có ${pendingApprovals.length} gian hàng chờ duyệt, ${openDisputes.length} khiếu nại đang mở và ${summary.flaggedBatches} lô COD lệch ${formatVnd(summary.totalDiscrepancy)} cần đối chiếu.`
      : `Toàn bộ ${reconciliationRows.length} lô thanh toán đã khớp sổ cái; còn ${pendingApprovals.length} gian hàng chờ duyệt và ${openDisputes.length} khiếu nại đang mở.`;

  const rangeCaption =
    range === "7d"
      ? `${formatInt(summary.totalTxCount)} gd · 15/11–19/11`
      : `15/11–19/11 (${RANGE_LABELS[range].toLowerCase()})`;

  const totalSettledVnd = summary.totalGross - summary.totalDiscrepancy;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Tổng quan sàn</h1>
          <p className={shared.pageSubtitle}>{findingLine}</p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="primary" size="sm" shape="app" href="/admin/gian-hang">
            Duyệt gian hàng
          </Button>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phạm vi theo dõi tổng quan">
          {SCOPE_TABS.map((tab) => {
            const active = scope === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => setScope(tab.id)}
              >
                {tab.label}
              </button>
            );
          })}
        </div>

        <div className={shared.tabsMeta}>
          <span className={styles.freshness}>Cập nhật 01:12 · 20/11</span>
          <select
            className={shared.toolbarSelect}
            value={range}
            onChange={(e) => setRange(e.target.value as AdminRangeKey)}
            aria-label="Chọn khoảng thời gian thống kê"
          >
            <option value="7d">7 ngày qua</option>
            <option value="14d">14 ngày qua</option>
            <option value="30d">30 ngày qua</option>
          </select>
        </div>
      </div>

      <div className={styles.overviewGrid}>
        <div className={styles.mainCol}>
          <section className={shared.kpiStrip} aria-label="Chỉ số vận hành chính">
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>GMV kỳ chọn</span>
              <span className={shared.kpiValue}>{formatVnd(summary.totalGross)}</span>
              <span className={shared.kpiCaption}>{rangeCaption}</span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Gian hàng chờ duyệt</span>
              <span className={shared.kpiValue}>{formatInt(pendingApprovals.length)}</span>
              <span className={shared.kpiCaption}>
                {supplementApprovals.length > 0
                  ? `Hạn 24h · +${supplementApprovals.length} bổ sung`
                  : "Cần xử lý trong 24h"}
              </span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Khiếu nại đang mở</span>
              <span className={shared.kpiValue}>{formatInt(openDisputes.length)}</span>
              <span className={shared.kpiCaption}>Tạm giữ {formatVnd(openDisputeAmount)}</span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Lệch đối soát</span>
              <span
                className={`${shared.kpiValue} ${
                  summary.totalDiscrepancy > 0 ? shared.kpiValueNegative : ""
                }`}
              >
                {summary.totalDiscrepancy > 0 ? formatSignedVnd(-summary.totalDiscrepancy) : "0₫"}
              </span>
              <span className={shared.kpiCaption}>
                {summary.flaggedBatches > 0
                  ? `${summary.flaggedBatches} phiên COD lệch`
                  : `${summary.matchedBatches}/${reconciliationRows.length} phiên đã khớp`}
              </span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="admin-chart-heading">
            <div className={shared.panelHeader}>
              <h2 id="admin-chart-heading" className={shared.panelTitle}>
                {scope === "toan_san"
                  ? "Tổng thu GMV theo ngày"
                  : scope === "thanh_toan"
                    ? "Tổng thu theo ngày đối soát"
                    : "Số giao dịch đã đối soát theo ngày"}
              </h2>
              <span className={shared.panelMeta}>
                {scope === "toan_san" && peakDay
                  ? `Đỉnh ${peakDay.label}: ${formatVnd(peakDay.grossCollected)}`
                  : scope === "thanh_toan"
                    ? `Thực trả gian hàng: ${formatVnd(summary.totalSellerPayable)}`
                    : `Tổng ${formatInt(summary.totalTxCount)} giao dịch`}
              </span>
            </div>

            {scope === "toan_san" && (
              <LineChart
                labels={dailySeries.map((p) => p.label)}
                series={[
                  {
                    name: "GMV đã ghi nhận",
                    values: dailySeries.map((p) => p.grossCollected),
                    color: "var(--chart-1)",
                  },
                ]}
                height={208}
                showArea
                formatValue={formatVnd}
                formatTick={formatCompactVnd}
                annotations={
                  peakDay ? [{ index: peakIndex, label: `Flash sale ${peakDay.label}` }] : undefined
                }
                ariaLabel="Biểu đồ tổng thu GMV toàn sàn theo ngày"
              />
            )}

            {scope === "thanh_toan" && (
              <BarChart
                labels={dailySeries.map((p) => p.label)}
                values={dailySeries.map((p) => p.grossCollected)}
                height={208}
                formatValue={formatVnd}
                formatTick={formatCompactVnd}
                ariaLabel="Biểu đồ tổng thu thanh toán theo ngày"
              />
            )}

            {scope === "van_hanh" && (
              <BarChart
                labels={dailySeries.map((p) => p.label)}
                values={dailySeries.map((p) => p.txCount)}
                height={208}
                formatValue={(v) => `${formatInt(v)} giao dịch`}
                formatTick={(v) => formatInt(v)}
                ariaLabel="Biểu đồ số giao dịch đối soát theo ngày"
              />
            )}

            <div className={styles.chartFooter}>
              <span>
                Phí hoa hồng sàn thu được: {formatVnd(summary.totalCommission)} (5,0% trên tổng thu)
              </span>
              <span>
                Hoàn tiền trong kỳ: {formatVnd(summary.totalRefunded)} · {summary.matchedBatches}/
                {reconciliationRows.length} lô khớp
              </span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="channel-recon-heading">
            <div className={shared.panelHeader}>
              <h2 id="channel-recon-heading" className={shared.panelTitle}>
                Đối soát theo cổng thanh toán
              </h2>
              <Link href="/admin/doi-soat" className={shared.panelLink}>
                Mở sổ đối soát
              </Link>
            </div>

            <div className={styles.channelTableWrap}>
              <table className={styles.channelTable}>
                <thead>
                  <tr>
                    <th>Cổng thanh toán</th>
                    <th className={shared.colNum}>Kỳ vọng</th>
                    <th className={shared.colNum}>Đã về sổ cái</th>
                    <th className={shared.colNum}>Chênh lệch</th>
                  </tr>
                </thead>
                <tbody>
                  {channelBreakdown.map((ch) => (
                    <tr key={ch.channel}>
                      <td>
                        <span>{ch.channelLabel}</span>
                        <span className={styles.channelMeta}>
                          {ch.batchCount} lô · {formatInt(ch.txCount)} gd
                        </span>
                      </td>
                      <td className={shared.colNum}>{formatVnd(ch.expectedVnd)}</td>
                      <td className={shared.colNum}>{formatVnd(ch.settledVnd)}</td>
                      <td
                        className={`${shared.colNum} ${
                          ch.discrepancyVnd > 0 ? shared.numNegative : shared.numMuted
                        }`}
                      >
                        {ch.discrepancyVnd > 0 ? formatSignedVnd(-ch.discrepancyVnd) : "0₫"}
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td>
                      Tổng cộng
                      <span className={styles.channelMeta}>
                        {reconciliationRows.length} lô · {formatInt(summary.totalTxCount)} gd
                      </span>
                    </td>
                    <td className={shared.colNum}>{formatVnd(summary.totalGross)}</td>
                    <td className={shared.colNum}>{formatVnd(totalSettledVnd)}</td>
                    <td
                      className={`${shared.colNum} ${
                        summary.totalDiscrepancy > 0 ? shared.numNegative : ""
                      }`}
                    >
                      {summary.totalDiscrepancy > 0
                        ? formatSignedVnd(-summary.totalDiscrepancy)
                        : "0₫"}
                    </td>
                  </tr>
                </tfoot>
              </table>
            </div>

            <div className={styles.channelMobileList}>
              {channelBreakdown.map((ch) => (
                <div key={ch.channel} className={styles.channelMobileRow}>
                  <div className={styles.channelMobileLine}>
                    <span className={shared.cellPrimary}>{ch.channelLabel}</span>
                    <span className={shared.cellPrimary}>{formatVnd(ch.settledVnd)}</span>
                  </div>
                  <div className={styles.channelMobileLine}>
                    <span className={shared.cellSub}>
                      {ch.batchCount} lô · Kỳ vọng {formatVnd(ch.expectedVnd)}
                    </span>
                    <span
                      className={`${shared.cellSub} ${
                        ch.discrepancyVnd > 0 ? shared.numNegative : ""
                      }`}
                    >
                      {ch.discrepancyVnd > 0
                        ? `Lệch ${formatSignedVnd(-ch.discrepancyVnd)}`
                        : "Khớp 0₫"}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>

        <aside className={styles.rightRail} aria-label="Hàng đợi và nhật ký gần đây">
          <section className={styles.railSection} aria-labelledby="rail-shops-heading">
            <h2 id="rail-shops-heading" className={styles.railHeading}>
              Hàng đợi duyệt gian hàng
            </h2>
            <div className={styles.railCard}>
              <ul className={styles.railList}>
                {priorityShopQueue.map((app) => {
                  const meta = SHOP_APPROVAL_META[app.status];
                  return (
                    <li key={app.id} className={styles.railRow}>
                      <Link
                        href={`/admin/gian-hang?selected=${encodeURIComponent(app.id)}`}
                        className={styles.railItemLink}
                      >
                        <div className={styles.railTopLine}>
                          <span className={styles.railPrimary}>{app.shopName}</span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </div>
                        <span className={styles.railSub}>
                          {app.applicationCode} · {app.primaryCategory} · {app.initialSkuCount} SKU
                          {app.slaHoursLeft > 0 ? ` · Còn ${app.slaHoursLeft}h` : ""}
                        </span>
                      </Link>
                    </li>
                  );
                })}
              </ul>
              <Link href="/admin/gian-hang" className={shared.viewAllBtn}>
                Xem tất cả ({shopApprovals.length})
              </Link>
            </div>
          </section>

          <section className={styles.railSection} aria-labelledby="rail-audit-heading">
            <h2 id="rail-audit-heading" className={styles.railHeading}>
              Nhật ký kiểm toán gần đây
            </h2>
            <div className={styles.railCard}>
              <ul className={styles.railList}>
                {recentAuditLogs.map((log) => (
                  <li key={log.id} className={styles.railRow}>
                    <div className={styles.railItemStatic}>
                      <div className={styles.railTopLine}>
                        <span className={styles.railPrimary}>{log.action}</span>
                        <span className={styles.railTime}>
                          {formatShortTimeDateVi(log.timestamp)}
                        </span>
                      </div>
                      <span className={styles.railSub}>
                        {log.actor} · {log.module} · {log.target}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
              <Link href="/admin/audit-log" className={shared.viewAllBtn}>
                Xem tất cả ({auditLogs.length})
              </Link>
            </div>
          </section>
        </aside>
      </div>
    </div>
  );
}

function AdminOverviewRoute() {
  return (
    <AdminStateGate
      title="Tổng quan sàn"
      subtitle="Theo dõi GMV, đối soát cổng thanh toán, hàng đợi duyệt gian hàng và nhật ký kiểm toán."
      emptyTitle="Chưa có dữ liệu tổng quan trong kỳ chọn"
      emptyMessage="Không có giao dịch hoặc lô đối soát nào được ghi nhận trong khoảng thời gian này."
      errorTitle="Không thể tải số liệu tổng quan quản trị"
      errorMessage="Dịch vụ đối soát và thống kê tạm thời gián đoạn. Thử tải lại để đồng bộ bản ghi mới nhất."
      loadingRows={5}
    >
      {() => <AdminOverviewContent />}
    </AdminStateGate>
  );
}

export default function AdminOverviewPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Tổng quan sàn" />}>
      <AdminOverviewRoute />
    </Suspense>
  );
}
