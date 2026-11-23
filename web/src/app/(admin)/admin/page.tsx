"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import type {
  GatewayAuditLog,
  GatewayLedgerSummary,
  GatewayOrder,
  GatewayPlatformReport,
  GatewayProduct,
  GatewayShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, apiBlob, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { BarChart, BarList, Button, IconDownload, LineChart, StateView } from "@/components";
import {
  formatBpsPercent,
  formatCompactVnd,
  formatDateTimeVi,
  formatDayMonthVi,
  formatInt,
  formatVnDateOffset,
  formatVnd,
  validateReportDateRange,
} from "@/lib/format";
import {
  getAdminAuditTargetTypeLabel,
  getAdminOrderStatusMeta,
  getAdminProductStatusMeta,
  getAdminShopStatusMeta,
} from "@/features/admin/types";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type OverviewScope = "toan_san" | "thanh_toan" | "van_hanh";

const SCOPE_TABS: Array<{ id: OverviewScope; label: string }> = [
  { id: "toan_san", label: "Báo cáo doanh thu" },
  { id: "thanh_toan", label: "Sổ cái & Trạng thái" },
  { id: "van_hanh", label: "Đơn hàng & Tăng trưởng" },
];

function formatShortIsoDay(isoDate: string): string {
  if (/^\d{4}-\d{2}-\d{2}$/.test(isoDate)) {
    const [, mm, dd] = isoDate.split("-");
    return `${dd}/${mm}`;
  }
  return formatDayMonthVi(isoDate);
}

function AdminOverviewContent() {
  const { showToast } = useMockStore();
  const [scope, setScope] = useState<OverviewScope>("toan_san");
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [fromDate, setFromDate] = useState(() => formatVnDateOffset(-29));
  const [toDate, setToDate] = useState(() => formatVnDateOffset(0));
  const [rangeError, setRangeError] = useState<string | null>(null);
  const [exportingCsv, setExportingCsv] = useState(false);

  const [report, setReport] = useState<GatewayPlatformReport | null>(null);
  const [reportLoading, setReportLoading] = useState(true);
  const [reportError, setReportError] = useState<string | null>(null);
  const reportSeqRef = useRef(0);

  const [pendingShops, setPendingShops] = useState<GatewayShop[]>([]);
  const [pendingShopsTotal, setPendingShopsTotal] = useState(0);
  const [pendingProducts, setPendingProducts] = useState<GatewayProduct[]>([]);
  const [pendingProductsTotal, setPendingProductsTotal] = useState(0);
  const [needsReviewTotal, setNeedsReviewTotal] = useState(0);
  const [ledger, setLedger] = useState<GatewayLedgerSummary | null>(null);
  const [auditLogs, setAuditLogs] = useState<GatewayAuditLog[]>([]);
  const [auditTotal, setAuditTotal] = useState(0);

  const loadOverview = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [shopsRes, prodsRes, ledgerRes, auditRes, needsReviewRes] = await Promise.all([
        api<PaginatedResponse<GatewayShop>>("admin/shops?status=PENDING&page=1&page_size=5"),
        api<PaginatedResponse<GatewayProduct>>("admin/products?status=PENDING&page=1&page_size=5"),
        api<GatewayLedgerSummary>("admin/ledger/summary"),
        api<PaginatedResponse<GatewayAuditLog>>("admin/audit-logs?page=1&page_size=5"),
        api<PaginatedResponse<GatewayOrder>>("admin/orders/needs-review?page=1&page_size=1").catch(
          () => ({ items: [], total: 0, page: 1, page_size: 1 })
        ),
      ]);
      setPendingShops(shopsRes.items ?? []);
      setPendingShopsTotal(shopsRes.total ?? (shopsRes.items ?? []).length);
      setPendingProducts(prodsRes.items ?? []);
      setPendingProductsTotal(prodsRes.total ?? (prodsRes.items ?? []).length);
      setLedger(ledgerRes);
      setAuditLogs(auditRes.items ?? []);
      setAuditTotal(auditRes.total ?? (auditRes.items ?? []).length);
      setNeedsReviewTotal(needsReviewRes.total ?? (needsReviewRes.items ?? []).length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải số liệu tổng quan quản trị lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadReport = useCallback(async (from: string, to: string) => {
    const check = validateReportDateRange(from, to);
    if (!check.ok) {
      setRangeError(check.message);
      return;
    }
    const seq = ++reportSeqRef.current;
    setRangeError(null);
    setReportLoading(true);
    setReportError(null);
    try {
      const qs = new URLSearchParams({
        from_date: from,
        to_date: to,
        top_limit: "10",
      });
      const res = await api<GatewayPlatformReport | { report: GatewayPlatformReport }>(
        `admin/reports?${qs.toString()}`
      );
      if (reportSeqRef.current !== seq) return;
      setReport(res && "report" in res ? res.report : res);
    } catch (err) {
      if (reportSeqRef.current !== seq) return;
      setReportError(getErrorMessage(err, "Không thể tải báo cáo toàn sàn lúc này."));
    } finally {
      if (reportSeqRef.current === seq) {
        setReportLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    void loadOverview();
  }, [loadOverview]);

  useEffect(() => {
    void loadReport(fromDate, toDate);
  }, [fromDate, toDate, loadReport]);

  const applyPresetDays = (days: number) => {
    setFromDate(formatVnDateOffset(-(days - 1)));
    setToDate(formatVnDateOffset(0));
  };

  const handleExportCsv = async () => {
    const check = validateReportDateRange(fromDate, toDate);
    if (!check.ok) {
      setRangeError(check.message);
      return;
    }
    setExportingCsv(true);
    try {
      const qs = new URLSearchParams({ from_date: fromDate, to_date: toDate });
      const blob = await apiBlob(`admin/reports/export?${qs.toString()}`);
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `bao-cao-toan-san-${fromDate}-${toDate}.csv`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      showToast(`Đã tải xuống báo cáo toàn sàn CSV (${fromDate} đến ${toDate}).`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tải xuống báo cáo CSV."));
    } finally {
      setExportingCsv(false);
    }
  };

  const dailySeries = useMemo(() => report?.daily ?? [], [report]);
  const userStats = report?.user_stats ?? null;
  const userDailySeries = useMemo(() => userStats?.daily ?? [], [userStats]);

  if (loading) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Tổng quan & Báo cáo sàn</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tổng hợp số liệu vận hành toàn sàn..."
            rows={5}
          />
        </div>
      </div>
    );
  }

  if (errorMsg) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Tổng quan & Báo cáo sàn</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải số liệu tổng quan quản trị"
            message={errorMsg}
            actionLabel="Tải lại"
            onAction={() => void loadOverview()}
          />
        </div>
      </div>
    );
  }

  const isBalanced = ledger ? ledger.is_balanced : true;
  const ledgerDiff = ledger ? ledger.total_debits_vnd - ledger.total_credits_vnd : 0;
  const totalFeesVnd = (report?.commission_vnd ?? 0) + (report?.payment_fee_vnd ?? 0);

  const findingLine = `Kỳ ${fromDate} → ${toDate} · ${formatInt(pendingShopsTotal)} gian hàng chờ duyệt, ${formatInt(pendingProductsTotal)} sản phẩm chờ kiểm duyệt${needsReviewTotal > 0 ? `, ${formatInt(needsReviewTotal)} đơn cần xử lý tay` : ""}.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Tổng quan & Báo cáo sàn</h1>
          <p className={shared.pageSubtitle}>{findingLine}</p>
        </div>
        <div className={shared.titleActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={exportingCsv}
            onClick={() => void handleExportCsv()}
          >
            <IconDownload size={14} />
            <span>{exportingCsv ? "Đang xuất..." : "Xuất CSV"}</span>
          </Button>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => void Promise.all([loadOverview(), loadReport(fromDate, toDate)])}
          >
            Làm mới
          </Button>
          <Button variant="primary" size="sm" shape="app" href="/admin/kiem-duyet">
            Kiểm duyệt sản phẩm
          </Button>
        </div>
      </header>

      {/* Date Range Toolbar */}
      <div className={shared.toolbar} style={{ marginBottom: 0 }}>
        <div className={shared.toolbarLeft}>
          <button type="button" className={shared.rowActionBtn} onClick={() => applyPresetDays(7)}>
            7 ngày
          </button>
          <button type="button" className={shared.rowActionBtn} onClick={() => applyPresetDays(30)}>
            30 ngày
          </button>
          <button type="button" className={shared.rowActionBtn} onClick={() => applyPresetDays(90)}>
            90 ngày
          </button>
          <label htmlFor="admin-rep-from" className={shared.fieldLabel}>
            Từ
          </label>
          <input
            id="admin-rep-from"
            type="date"
            className={shared.textInput}
            style={{ width: 152, height: 34 }}
            value={fromDate}
            onChange={(e) => setFromDate(e.target.value)}
          />
          <label htmlFor="admin-rep-to" className={shared.fieldLabel}>
            Đến
          </label>
          <input
            id="admin-rep-to"
            type="date"
            className={shared.textInput}
            style={{ width: 152, height: 34 }}
            value={toDate}
            onChange={(e) => setToDate(e.target.value)}
          />
        </div>
        <div className={shared.toolbarRight}>
          <span className={styles.freshness}>
            Sổ cái: {isBalanced ? "Cân bằng" : "Có chênh lệch"}
          </span>
        </div>
      </div>

      {rangeError && (
        <p className={shared.validationError} role="alert" style={{ marginTop: 8 }}>
          {rangeError}
        </p>
      )}

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
      </div>

      <div className={styles.overviewGrid}>
        <div className={styles.mainCol}>
          {reportLoading ? (
            <StateView
              kind="loading"
              surface="app"
              title="Đang tổng hợp báo cáo tài chính & tăng trưởng toàn sàn..."
              rows={4}
            />
          ) : reportError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải báo cáo toàn sàn"
              message={reportError}
              actionLabel="Thử lại"
              onAction={() => void loadReport(fromDate, toDate)}
            />
          ) : (
            report && (
              <>
                <section className={shared.kpiStrip} aria-label="Chỉ số doanh thu toàn sàn">
                  <div className={shared.kpiCell}>
                    <span className={shared.kpiLabel}>GMV toàn sàn</span>
                    <span className={shared.kpiValue}>{formatVnd(report.gmv_vnd)}</span>
                    <span className={shared.kpiCaption}>
                      {formatInt(report.total_orders)} đơn · Thuần{" "}
                      {formatCompactVnd(report.net_revenue_vnd)}
                    </span>
                  </div>

                  <div className={shared.kpiCell}>
                    <span className={shared.kpiLabel}>Phí hoa hồng & thanh toán</span>
                    <span className={shared.kpiValue}>{formatVnd(totalFeesVnd)}</span>
                    <span className={shared.kpiCaption}>
                      Hoa hồng {formatCompactVnd(report.commission_vnd)} · Thanh toán{" "}
                      {formatCompactVnd(report.payment_fee_vnd)}
                    </span>
                  </div>

                  <div className={shared.kpiCell}>
                    <span className={shared.kpiLabel}>Trợ giá sàn & Tỷ lệ hủy/trả</span>
                    <span className={shared.kpiValue}>
                      {formatVnd(report.platform_subsidy_vnd)}
                    </span>
                    <span className={shared.kpiCaption}>
                      Hủy {formatBpsPercent(report.cancel_rate_bps)} · Trả{" "}
                      {formatBpsPercent(report.return_rate_bps)}
                    </span>
                  </div>

                  <div className={shared.kpiCell}>
                    <span className={shared.kpiLabel}>Người dùng & Gian hàng</span>
                    <span className={shared.kpiValue}>
                      {formatInt(userStats?.total_users ?? 0)} /{" "}
                      {formatInt(userStats?.total_shops ?? 0)}
                    </span>
                    <span className={shared.kpiCaption}>
                      Kỳ này: +{formatInt(userStats?.new_users ?? 0)} khách · +
                      {formatInt(userStats?.new_shops ?? 0)} shop
                    </span>
                  </div>
                </section>

                <section className={shared.panel} aria-labelledby="admin-chart-heading">
                  <div className={shared.panelHeader}>
                    <h2 id="admin-chart-heading" className={shared.panelTitle}>
                      {scope === "toan_san"
                        ? `GMV & Doanh thu thuần (${report.from_date} → ${report.to_date})`
                        : scope === "thanh_toan"
                          ? "Số dư các tài khoản sổ cái ghi kép"
                          : `Số đơn hàng theo ngày (${report.from_date} → ${report.to_date})`}
                    </h2>
                    <span className={shared.panelMeta}>
                      Hoàn tất: {formatInt(report.completed_orders)} đơn
                    </span>
                  </div>

                  {scope === "toan_san" &&
                    (dailySeries.length > 0 ? (
                      <LineChart
                        labels={dailySeries.map((p) => formatShortIsoDay(p.date))}
                        series={[
                          {
                            name: "GMV ghi nhận",
                            values: dailySeries.map((p) => p.gmv_vnd),
                            color: "var(--chart-1)",
                          },
                          {
                            name: "Doanh thu thuần",
                            values: dailySeries.map((p) => p.net_revenue_vnd),
                            color: "var(--chart-muted)",
                            dashed: true,
                          },
                        ]}
                        height={210}
                        showArea
                        formatValue={formatVnd}
                        formatTick={formatCompactVnd}
                        ariaLabel="Biểu đồ GMV và doanh thu thuần toàn sàn"
                      />
                    ) : (
                      <p className={shared.emptyRow}>Chưa có dữ liệu doanh thu trong kỳ chọn.</p>
                    ))}

                  {scope === "thanh_toan" && (
                    <BarChart
                      labels={(ledger?.accounts ?? []).map((a) => a.account_code)}
                      values={(ledger?.accounts ?? []).map((a) => Math.abs(a.balance_vnd))}
                      height={210}
                      formatValue={formatVnd}
                      formatTick={formatCompactVnd}
                      ariaLabel="Biểu đồ số dư tài khoản sổ cái"
                    />
                  )}

                  {scope === "van_hanh" &&
                    (dailySeries.length > 0 ? (
                      <BarChart
                        labels={dailySeries.map((p) => formatShortIsoDay(p.date))}
                        values={dailySeries.map((p) => p.order_count)}
                        height={210}
                        formatValue={(v) => `${formatInt(v)} đơn`}
                        formatTick={(v) => formatInt(v)}
                        ariaLabel="Biểu đồ số đơn hàng theo ngày"
                      />
                    ) : (
                      <p className={shared.emptyRow}>Chưa có dữ liệu đơn hàng trong kỳ chọn.</p>
                    ))}

                  <div className={styles.chartFooter}>
                    <span>Tổng Ghi nợ: {formatVnd(ledger?.total_debits_vnd ?? 0)}</span>
                    <span>
                      Tổng Ghi có: {formatVnd(ledger?.total_credits_vnd ?? 0)} · Chênh lệch:{" "}
                      {formatVnd(ledgerDiff)}
                    </span>
                  </div>
                </section>

                {scope === "van_hanh" && userDailySeries.length > 0 && (
                  <section className={shared.panel} aria-labelledby="user-growth-heading">
                    <div className={shared.panelHeader}>
                      <h2 id="user-growth-heading" className={shared.panelTitle}>
                        Tăng trưởng tài khoản người dùng & gian hàng mới
                      </h2>
                      <span className={shared.panelMeta}>
                        Hoạt động: {formatInt(userStats?.active_users ?? 0)} người dùng ·{" "}
                        {formatInt(userStats?.active_shops ?? 0)} gian hàng
                      </span>
                    </div>
                    <LineChart
                      labels={userDailySeries.map((p) => formatShortIsoDay(p.date))}
                      series={[
                        {
                          name: "Người dùng mới",
                          values: userDailySeries.map((p) => p.new_users),
                          color: "var(--chart-1)",
                        },
                        {
                          name: "Gian hàng mới",
                          values: userDailySeries.map((p) => p.new_shops),
                          color: "var(--chart-muted)",
                        },
                      ]}
                      height={190}
                      formatValue={formatInt}
                      ariaLabel="Biểu đồ người dùng mới và gian hàng mới"
                    />
                  </section>
                )}

                {(report.orders_by_status ?? []).length > 0 && (
                  <section className={shared.panel} aria-labelledby="order-status-dist-heading">
                    <div className={shared.panelHeader}>
                      <h2 id="order-status-dist-heading" className={shared.panelTitle}>
                        Phân bổ trạng thái đơn hàng trong kỳ
                      </h2>
                      <span className={shared.panelMeta}>
                        Tổng {formatInt(report.total_orders)} đơn
                      </span>
                    </div>
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                      {report.orders_by_status.map((item) => {
                        const meta = getAdminOrderStatusMeta(item.status);
                        return (
                          <StatusPill
                            key={item.status}
                            label={`${meta.label}: ${formatInt(item.count)}`}
                            tone={meta.tone}
                          />
                        );
                      })}
                    </div>
                  </section>
                )}

                <section className={shared.panel} aria-labelledby="top-products-heading">
                  <div className={shared.panelHeader}>
                    <h2 id="top-products-heading" className={shared.panelTitle}>
                      Top sản phẩm bán chạy toàn sàn ({formatInt(report.top_products?.length ?? 0)})
                    </h2>
                    <span className={shared.panelMeta}>
                      Kỳ {report.from_date} → {report.to_date}
                    </span>
                  </div>
                  {(report.top_products ?? []).length === 0 ? (
                    <p className={shared.emptyRow}>Chưa có sản phẩm bán ra trong kỳ báo cáo này.</p>
                  ) : (
                    <BarList
                      rows={report.top_products.map((p) => ({
                        label: p.product_name,
                        subLabel: `${formatInt(p.units_sold)} sản phẩm đã bán`,
                        value: p.revenue_vnd,
                      }))}
                      formatValue={formatVnd}
                    />
                  )}
                </section>
              </>
            )
          )}

          <section className={shared.panel} aria-labelledby="ledger-summary-heading">
            <div className={shared.panelHeader}>
              <h2 id="ledger-summary-heading" className={shared.panelTitle}>
                Tóm tắt tài khoản sổ cái ghi kép
              </h2>
              <Link href="/admin/doi-soat" className={shared.panelLink}>
                Mở sổ đối soát
              </Link>
            </div>

            {!ledger || ledger.accounts.length === 0 ? (
              <p className={shared.emptyRow}>Chưa có bút toán nào phát sinh trên sổ cái.</p>
            ) : (
              <>
                <div className={styles.channelTableWrap}>
                  <table className={styles.channelTable}>
                    <thead>
                      <tr>
                        <th>Tài khoản</th>
                        <th className={shared.colNum}>Tổng Nợ</th>
                        <th className={shared.colNum}>Tổng Có</th>
                        <th className={shared.colNum}>Số dư</th>
                      </tr>
                    </thead>
                    <tbody>
                      {ledger.accounts.map((acc) => (
                        <tr key={acc.account_code}>
                          <td>
                            <span>{acc.account_code}</span>
                            <span className={styles.channelMeta}>{acc.account_type}</span>
                          </td>
                          <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.balance_vnd)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className={styles.channelMobileList}>
                  {ledger.accounts.map((acc) => (
                    <div key={acc.account_code} className={styles.channelMobileRow}>
                      <div className={styles.channelMobileLine}>
                        <span className={shared.cellPrimary}>{acc.account_code}</span>
                        <span className={shared.cellPrimary}>{formatVnd(acc.balance_vnd)}</span>
                      </div>
                      <div className={styles.channelMobileLine}>
                        <span className={shared.cellSub}>{acc.account_type}</span>
                        <span className={shared.cellSub}>
                          Nợ {formatVnd(acc.debit_total_vnd)} · Có {formatVnd(acc.credit_total_vnd)}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </>
            )}
          </section>
        </div>

        <aside className={styles.rightRail} aria-label="Hàng đợi và nhật ký gần đây">
          {needsReviewTotal > 0 && (
            <section className={styles.railSection} aria-labelledby="rail-orphan-heading">
              <h2 id="rail-orphan-heading" className={styles.railHeading}>
                Đơn cần xử lý tay ({formatInt(needsReviewTotal)})
              </h2>
              <div className={styles.railCard}>
                <p className={shared.pageSubtitle} style={{ padding: "8px 0" }}>
                  Có {formatInt(needsReviewTotal)} đơn hàng phát sinh cảnh báo cần vận hành rà soát
                  và xác nhận xử lý.
                </p>
                <Link href="/admin/doi-soat" className={shared.viewAllBtn}>
                  Mở hàng đợi xử lý tay
                </Link>
              </div>
            </section>
          )}

          <section className={styles.railSection} aria-labelledby="rail-shops-heading">
            <h2 id="rail-shops-heading" className={styles.railHeading}>
              Gian hàng chờ duyệt ({formatInt(pendingShopsTotal)})
            </h2>
            <div className={styles.railCard}>
              {pendingShops.length === 0 ? (
                <p className={shared.emptyRow}>Không có gian hàng nào chờ duyệt.</p>
              ) : (
                <ul className={styles.railList}>
                  {pendingShops.map((shop) => {
                    const meta = getAdminShopStatusMeta(shop.status);
                    return (
                      <li key={shop.id} className={styles.railRow}>
                        <Link
                          href={`/admin/gian-hang?selected=${encodeURIComponent(shop.id)}`}
                          className={styles.railItemLink}
                        >
                          <div className={styles.railTopLine}>
                            <span className={styles.railPrimary}>{shop.name}</span>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </div>
                          <span className={styles.railSub}>
                            {shop.contact_phone || "Chưa có SĐT"} ·{" "}
                            {shop.created_at ? formatDateTimeVi(shop.created_at) : ""}
                          </span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
              <Link href="/admin/gian-hang" className={shared.viewAllBtn}>
                Xem hàng đợi gian hàng
              </Link>
            </div>
          </section>

          <section className={styles.railSection} aria-labelledby="rail-products-heading">
            <h2 id="rail-products-heading" className={styles.railHeading}>
              Sản phẩm chờ duyệt ({formatInt(pendingProductsTotal)})
            </h2>
            <div className={styles.railCard}>
              {pendingProducts.length === 0 ? (
                <p className={shared.emptyRow}>Không có sản phẩm nào chờ duyệt.</p>
              ) : (
                <ul className={styles.railList}>
                  {pendingProducts.map((prod) => {
                    const meta = getAdminProductStatusMeta(prod.status);
                    return (
                      <li key={prod.id} className={styles.railRow}>
                        <Link href="/admin/kiem-duyet" className={styles.railItemLink}>
                          <div className={styles.railTopLine}>
                            <span className={styles.railPrimary}>{prod.name}</span>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </div>
                          <span className={styles.railSub}>
                            {formatVnd(prod.min_price_vnd)} · {formatInt(prod.skus?.length ?? 0)}{" "}
                            SKU
                          </span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
              <Link href="/admin/kiem-duyet" className={shared.viewAllBtn}>
                Mở hàng đợi kiểm duyệt
              </Link>
            </div>
          </section>

          <section className={styles.railSection} aria-labelledby="rail-audit-heading">
            <h2 id="rail-audit-heading" className={styles.railHeading}>
              Nhật ký kiểm toán gần đây
            </h2>
            <div className={styles.railCard}>
              {auditLogs.length === 0 ? (
                <p className={shared.emptyRow}>Chưa có bản ghi kiểm toán nào.</p>
              ) : (
                <ul className={styles.railList}>
                  {auditLogs.map((log) => (
                    <li key={log.id} className={styles.railRow}>
                      <div className={styles.railItemStatic}>
                        <div className={styles.railTopLine}>
                          <span className={styles.railPrimary}>{log.action}</span>
                          <span className={styles.railTime}>
                            {log.created_at ? formatDateTimeVi(log.created_at) : ""}
                          </span>
                        </div>
                        <span className={styles.railSub}>
                          {log.actor_role} · {getAdminAuditTargetTypeLabel(log.target_type)} ·{" "}
                          {log.reason || log.target_id}
                        </span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <Link href="/admin/audit-log" className={shared.viewAllBtn}>
                Xem tất cả ({formatInt(auditTotal)})
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
      title="Tổng quan & Báo cáo sàn"
      subtitle="Theo dõi báo cáo GMV toàn sàn, tăng trưởng người dùng/gian hàng, sổ cái ghi kép và hàng đợi duyệt."
      emptyTitle="Chưa có dữ liệu tổng quan trong kỳ chọn"
      emptyMessage="Không có giao dịch hoặc hàng đợi nào được ghi nhận trong khoảng thời gian này."
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
    <Suspense fallback={<AdminSuspenseFallback title="Tổng quan & Báo cáo sàn" />}>
      <AdminOverviewRoute />
    </Suspense>
  );
}
