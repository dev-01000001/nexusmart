"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  BarList,
  Button,
  IconChevronRight,
  IconDownload,
  IconPackage,
  IconPlus,
  StateView,
} from "@/components";
import { api, apiBlob, ApiError, getErrorMessage } from "@/lib/api";
import {
  formatBpsPercent,
  formatDayMonthVi,
  formatInt,
  formatVnDateOffset,
  formatVnd,
  validateReportDateRange,
} from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { SellerOrderQuickDrawer } from "./SellerOrderQuickDrawer";
import { SellerTrendChart } from "./SellerTrendChart";
import { useSellerContext } from "./SellerShell";
import { SellerSiteStatus } from "./SellerSiteStatus";
import { statusPillClass } from "./SellerUiPrimitives";
import {
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  summarizeOrderItems,
  type GwSellerBalance,
  type GwSellerOrder,
  type GwSellerProduct,
  type GwSellerReport,
} from "./sellerApiTypes";
import { parseUiState, translateSellerShipError } from "./types";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/page.module.css";

type MetricTabKey = "netRevenue" | "gmv" | "orders";

const PRIORITY_STATUS_RANK: Record<string, number> = {
  CONFIRMED: 0,
  PREPARING: 1,
  RETURN_REQUESTED: 2,
  PENDING: 3,
  SHIPPING: 4,
  DELIVERED: 5,
  COMPLETED: 6,
  CANCELLED: 7,
};

interface LowStockRow {
  productId: string;
  productName: string;
  skuId: string;
  skuCode: string;
  skuLabel: string;
  availableStock: number;
  priceVnd: number;
  image: string | null;
}

function formatShortIsoDay(isoDate: string): string {
  if (/^\d{4}-\d{2}-\d{2}$/.test(isoDate)) {
    const [, mm, dd] = isoDate.split("-");
    return `${dd}/${mm}`;
  }
  return formatDayMonthVi(isoDate);
}

export interface SellerOverviewClientProps {
  initialOrders: GwSellerOrder[];
  initialProducts: GwSellerProduct[];
  initialBalance: GwSellerBalance | null;
  balanceError?: string | null;
  initialReport: GwSellerReport | null;
  initialReportError?: string | null;
  initialFromDate: string;
  initialToDate: string;
  pendingReturnsCount: number;
  loadError: string | null;
}

export function SellerOverviewClient({
  initialOrders,
  initialProducts,
  initialBalance,
  balanceError = null,
  initialReport,
  initialReportError = null,
  initialFromDate,
  initialToDate,
  pendingReturnsCount,
  loadError,
}: SellerOverviewClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useSellerContext();

  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<MetricTabKey>("netRevenue");
  const [drawerOrderId, setDrawerOrderId] = useState<string | null>(null);

  // Date-range report state (/api/seller/reports & /api/seller/reports/export)
  const [fromDate, setFromDate] = useState(initialFromDate);
  const [toDate, setToDate] = useState(initialToDate);
  const [rangeError, setRangeError] = useState<string | null>(null);
  const [report, setReport] = useState<GwSellerReport | null>(initialReport);
  const [reportLoading, setReportLoading] = useState(false);
  const [reportError, setReportError] = useState<string | null>(initialReportError);
  const [exportingCsv, setExportingCsv] = useState(false);
  const reportSeqRef = useRef(0);
  const isInitialRangeRef = useRef(true);

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
      const res = await api<{ report?: GwSellerReport } | GwSellerReport>(
        `/api/seller/reports?${qs.toString()}`
      );
      if (reportSeqRef.current !== seq) return;
      const nextReport =
        res && typeof res === "object" && "report" in res && res.report
          ? res.report
          : (res as GwSellerReport);
      setReport(nextReport ?? null);
    } catch (err) {
      if (reportSeqRef.current !== seq) return;
      setReportError(getErrorMessage(err, "Chưa thể tải báo cáo doanh thu gian hàng."));
    } finally {
      if (reportSeqRef.current === seq) {
        setReportLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    if (isInitialRangeRef.current) {
      isInitialRangeRef.current = false;
      return;
    }
    if (!uiState) {
      void loadReport(fromDate, toDate);
    }
  }, [uiState, fromDate, toDate, loadReport]);

  const applyPresetDays = (days: number) => {
    const nextTo = formatVnDateOffset(0);
    const nextFrom = formatVnDateOffset(-(days - 1));
    setFromDate(nextFrom);
    setToDate(nextTo);
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
      const blob = await apiBlob(`/api/seller/reports/export?${qs.toString()}`);
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `bao-cao-gian-hang-${fromDate}-${toDate}.csv`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.setTimeout(() => window.URL.revokeObjectURL(url), 1000);
      showToast(`Đã tải xuống báo cáo CSV (${fromDate} đến ${toDate}).`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xuất tệp CSV báo cáo lúc này."));
    } finally {
      setExportingCsv(false);
    }
  };

  const productImageById = useMemo(() => {
    const map = new Map<string, string>();
    for (const p of initialProducts) {
      if (p.images?.[0]) map.set(p.id, p.images[0]);
    }
    return map;
  }, [initialProducts]);

  const lowStockAlerts = useMemo<LowStockRow[]>(() => {
    const rows: LowStockRow[] = [];
    for (const p of initialProducts) {
      const fallbackImg = p.images?.[0] || null;
      for (const sku of p.skus ?? []) {
        if (!sku.is_active) continue;
        if (sku.available_stock <= 5) {
          rows.push({
            productId: p.id,
            productName: p.name,
            skuId: sku.id,
            skuCode: sku.sku_code,
            skuLabel:
              sku.name || `${sku.attributes?.color_name ?? ""} / ${sku.attributes?.size ?? ""}`,
            availableStock: Math.max(0, sku.available_stock),
            priceVnd: sku.price_vnd,
            image: sku.image_url || fallbackImg,
          });
        }
      }
    }
    return rows.sort((a, b) => a.availableStock - b.availableStock);
  }, [initialProducts]);

  const pendingConfirmCount = orders.filter((o) => o.status.toUpperCase() === "CONFIRMED").length;
  const packingCount = orders.filter((o) => o.status.toUpperCase() === "PREPARING").length;

  const dailyPoints = useMemo(() => report?.daily ?? [], [report]);

  const chartConfig = useMemo(() => {
    if (activeTab === "orders") {
      return {
        points: dailyPoints.map((d) => ({
          label: formatShortIsoDay(d.date),
          current: d.order_count,
        })),
        valueKind: "int" as const,
        metricName: `Số đơn hàng (${fromDate} → ${toDate})`,
        currentLegend: "Số đơn theo ngày",
      };
    }
    if (activeTab === "gmv") {
      return {
        points: dailyPoints.map((d) => ({
          label: formatShortIsoDay(d.date),
          current: d.gmv_vnd,
        })),
        valueKind: "vnd" as const,
        metricName: `Tổng GMV (${fromDate} → ${toDate})`,
        currentLegend: "GMV theo ngày",
      };
    }
    return {
      points: dailyPoints.map((d) => ({
        label: formatShortIsoDay(d.date),
        current: d.net_revenue_vnd,
      })),
      valueKind: "vnd" as const,
      metricName: `Doanh thu thuần (${fromDate} → ${toDate})`,
      currentLegend: "Doanh thu thuần",
    };
  }, [dailyPoints, activeTab, fromDate, toDate]);

  const recentOrders = useMemo(() => {
    return [...orders]
      .sort(
        (a, b) =>
          (PRIORITY_STATUS_RANK[a.status.toUpperCase()] ?? 9) -
          (PRIORITY_STATUS_RANK[b.status.toUpperCase()] ?? 9)
      )
      .slice(0, 6);
  }, [orders]);

  const topLowStock = useMemo(() => lowStockAlerts.slice(0, 4), [lowStockAlerts]);
  const drawerOrder = useMemo(
    () => (drawerOrderId ? (orders.find((o) => o.id === drawerOrderId) ?? null) : null),
    [orders, drawerOrderId]
  );

  const handleQuickAction = async (order: GwSellerOrder) => {
    const action = getNextBackendOrderAction(
      order.status,
      order.tracking_code,
      order.shipment_job_status
    );
    if (!action || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      const endpoint =
        action.kind === "confirm"
          ? `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`
          : `/api/seller/orders/${encodeURIComponent(order.id)}/ship`;
      const res = await api<{ order?: GwSellerOrder; queued?: boolean }>(endpoint, {
        method: "POST",
      });
      if (res.order) {
        setOrders((prev) => prev.map((o) => (o.id === res.order!.id ? res.order! : o)));
      }
      if (res.queued) {
        showToast(`Đơn ${order.order_code}: đã đưa vào hàng đợi tạo vận đơn.`);
      } else {
        showToast(`Đơn ${order.order_code}: ${action.label.toLowerCase()} thành công.`);
      }
      router.refresh();
    } catch (err) {
      if (action.kind === "ship") {
        showToast(translateSellerShipError(err));
      } else {
        showToast(err instanceof ApiError ? err.message : "Không thể cập nhật đơn hàng lúc này.");
      }
    } finally {
      setBusyOrderId(null);
    }
  };

  const availableVnd = initialBalance?.available_vnd ?? 0;
  const pendingVnd = initialBalance?.pending_vnd ?? 0;
  const totalFeesVnd = (report?.commission_vnd ?? 0) + (report?.payment_fee_vnd ?? 0);

  const todayTasks = [
    {
      key: "confirm",
      label: "Chờ xác nhận",
      count: pendingConfirmCount,
      href: "/seller/don-hang?status=CONFIRMED",
      urgent: pendingConfirmCount > 0,
    },
    {
      key: "ship",
      label: "Chờ bàn giao",
      count: packingCount,
      href: "/seller/don-hang?status=PREPARING",
      urgent: packingCount > 0,
    },
    {
      key: "returns",
      label: "Yêu cầu trả hàng",
      count: pendingReturnsCount,
      href: "/seller/tra-hang",
      urgent: pendingReturnsCount > 0,
    },
    {
      key: "stock",
      label: "Sắp hết tồn",
      count: lowStockAlerts.length,
      href: "/seller/san-pham?filter=LOW_OOS",
      urgent: lowStockAlerts.length > 0,
    },
  ];

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <h1 className={common.title}>Tổng quan</h1>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/san-pham/moi">
            <IconPlus size={14} />
            <span>Thêm sản phẩm</span>
          </Button>
          <Button
            variant="primary"
            size="sm"
            shape="app"
            href={
              pendingConfirmCount > 0 ? "/seller/don-hang?status=CONFIRMED" : "/seller/don-hang"
            }
          >
            {pendingConfirmCount > 0
              ? `Xác nhận đơn (${formatInt(pendingConfirmCount)})`
              : "Đơn hàng"}
          </Button>
        </div>
      </header>

      <SellerSiteStatus />

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải tổng quan..."
          rows={5}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải tổng quan"
          message={loadError ?? "Kết nối dịch vụ đơn hàng tạm gián đoạn."}
          actionLabel="Tải lại"
          onAction={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có đơn hàng trong kỳ chọn."
          actionLabel="Thêm sản phẩm"
          actionHref="/seller/san-pham/moi"
        />
      )}

      {!uiState && !loadError && (
        <>
          {/* 1. Today's Action Queue */}
          <section aria-labelledby="today-queue-heading" className={styles.todaySection}>
            <div className={styles.sectionHeadRow}>
              <h2 id="today-queue-heading" className={common.cardTitle}>
                Hôm nay cần làm gì?
              </h2>
              <Link href="/seller/tai-chinh" className={styles.balancePillLink}>
                <span>Khả dụng</span>
                {initialBalance ? (
                  <>
                    <strong
                      className="num"
                      data-reveal="count"
                      data-count-to={availableVnd}
                      data-count-format="vnd"
                    >
                      {formatVnd(availableVnd)}
                    </strong>
                    {pendingVnd > 0 && (
                      <span className={styles.balancePendingSub}>
                        (Tạm giữ <span className="num">{formatVnd(pendingVnd)}</span>)
                      </span>
                    )}
                  </>
                ) : (
                  <span className={styles.balancePendingSub}>
                    {balanceError ? "Đang chờ đồng bộ" : "0₫"}
                  </span>
                )}
              </Link>
            </div>

            <div className={styles.taskGrid}>
              {todayTasks.map((task) => (
                <Link
                  key={task.key}
                  href={task.href}
                  className={`${styles.taskTile} ${task.urgent ? styles.taskTileActive : ""}`}
                >
                  <div className={styles.taskTileTop}>
                    <span className={styles.taskLabel}>{task.label}</span>
                    <IconChevronRight size={14} className={styles.taskArrow} />
                  </div>
                  <strong
                    className={`${styles.taskCount} num ${task.count === 0 ? styles.taskCountZero : ""}`}
                    data-reveal="count"
                    data-count-to={task.count}
                  >
                    {formatInt(task.count)}
                  </strong>
                </Link>
              ))}
            </div>
          </section>

          {/* 2. Date-Range Report Filter Bar + CSV Export */}
          <section className={common.card} aria-label="Bộ lọc kỳ báo cáo gian hàng">
            <div className={styles.reportDateBar}>
              <div className={styles.reportDateControls}>
                <div className={common.segmented} role="group" aria-label="Chọn nhanh kỳ báo cáo">
                  {(
                    [
                      [7, "7 ngày"],
                      [30, "30 ngày"],
                      [90, "90 ngày"],
                    ] as const
                  ).map(([days, label]) => (
                    <button
                      key={days}
                      type="button"
                      className={common.segmentedBtn}
                      onClick={() => applyPresetDays(days)}
                    >
                      {label}
                    </button>
                  ))}
                </div>

                <div className={styles.reportDateField}>
                  <label className={styles.reportDateLabel} htmlFor="rep-from">
                    Từ
                  </label>
                  <input
                    id="rep-from"
                    type="date"
                    className={styles.reportDateInput}
                    value={fromDate}
                    onChange={(e) => setFromDate(e.target.value)}
                  />
                </div>
                <div className={styles.reportDateField}>
                  <label className={styles.reportDateLabel} htmlFor="rep-to">
                    Đến
                  </label>
                  <input
                    id="rep-to"
                    type="date"
                    className={styles.reportDateInput}
                    value={toDate}
                    onChange={(e) => setToDate(e.target.value)}
                  />
                </div>
              </div>

              <Button
                type="button"
                variant="outline"
                size="sm"
                shape="app"
                disabled={exportingCsv}
                onClick={() => void handleExportCsv()}
              >
                <IconDownload size={14} />
                <span>{exportingCsv ? "Đang xuất..." : "Xuất CSV"}</span>
              </Button>
            </div>
            {rangeError && (
              <p className={common.fieldError} role="alert" style={{ marginTop: 8 }}>
                {rangeError}
              </p>
            )}
          </section>

          {/* 3. Report KPIs + Trend Chart + Top Products */}
          {reportLoading ? (
            <StateView
              kind="loading"
              surface="app"
              title="Đang tổng hợp báo cáo doanh thu..."
              rows={4}
            />
          ) : reportError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải báo cáo doanh thu"
              message={reportError}
              actionLabel="Thử lại"
              onAction={() => void loadReport(fromDate, toDate)}
            />
          ) : (
            report && (
              <>
                <section className={common.balanceStrip} aria-label="Tổng hợp báo cáo kỳ chọn">
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Tổng GMV</span>
                    <span className={`${common.balanceValue} num`}>
                      {formatVnd(report.gmv_vnd)}
                    </span>
                    <span className={common.balanceCaption}>
                      {formatInt(report.total_orders)} đơn trong kỳ
                    </span>
                  </div>
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Doanh thu thuần</span>
                    <span className={`${common.balanceValue} num`}>
                      {formatVnd(report.net_revenue_vnd)}
                    </span>
                    <span className={common.balanceCaption}>
                      {formatInt(report.completed_orders)} đơn hoàn tất
                    </span>
                  </div>
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Phí sàn &amp; thanh toán</span>
                    <span className={`${common.balanceValue} num`}>{formatVnd(totalFeesVnd)}</span>
                    <span className={common.balanceCaption}>
                      Hoa hồng {formatVnd(report.commission_vnd)} · Thanh toán{" "}
                      {formatVnd(report.payment_fee_vnd)}
                    </span>
                  </div>
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Tỷ lệ hủy / trả hàng</span>
                    <span className={`${common.balanceValue} num`}>
                      {formatBpsPercent(report.cancel_rate_bps)} /{" "}
                      {formatBpsPercent(report.return_rate_bps)}
                    </span>
                    <span className={common.balanceCaption}>
                      {formatInt(report.cancelled_orders)} hủy · {formatInt(report.returned_orders)}{" "}
                      trả hàng
                    </span>
                  </div>
                </section>

                <section className={common.cardFlush} aria-label="Biểu đồ báo cáo doanh thu">
                  <div className={styles.kpiTabs} role="tablist" aria-label="Chỉ số theo ngày">
                    <button
                      type="button"
                      role="tab"
                      aria-selected={activeTab === "netRevenue"}
                      className={`${styles.kpiTab} ${activeTab === "netRevenue" ? styles.kpiTabActive : ""}`}
                      onClick={() => setActiveTab("netRevenue")}
                    >
                      <span className={styles.kpiTabLabel}>Doanh thu thuần</span>
                      <span className={styles.kpiTabRow}>
                        <span className={`${styles.kpiTabValue} num`}>
                          {formatVnd(report.net_revenue_vnd)}
                        </span>
                      </span>
                    </button>

                    <button
                      type="button"
                      role="tab"
                      aria-selected={activeTab === "gmv"}
                      className={`${styles.kpiTab} ${activeTab === "gmv" ? styles.kpiTabActive : ""}`}
                      onClick={() => setActiveTab("gmv")}
                    >
                      <span className={styles.kpiTabLabel}>Tổng GMV</span>
                      <span className={styles.kpiTabRow}>
                        <span className={`${styles.kpiTabValue} num`}>
                          {formatVnd(report.gmv_vnd)}
                        </span>
                      </span>
                    </button>

                    <button
                      type="button"
                      role="tab"
                      aria-selected={activeTab === "orders"}
                      className={`${styles.kpiTab} ${activeTab === "orders" ? styles.kpiTabActive : ""}`}
                      onClick={() => setActiveTab("orders")}
                    >
                      <span className={styles.kpiTabLabel}>Số đơn hàng</span>
                      <span className={styles.kpiTabRow}>
                        <span className={`${styles.kpiTabValue} num`}>
                          {formatInt(report.total_orders)}
                        </span>
                      </span>
                    </button>
                  </div>

                  <div className={styles.chartBody}>
                    <div className={styles.chartCaption}>
                      <h2 className={styles.chartHeading}>{chartConfig.metricName}</h2>
                      <span className={styles.chartPeriod}>Tối đa 366 ngày</span>
                    </div>

                    {dailyPoints.length > 0 ? (
                      <SellerTrendChart
                        ariaLabel={chartConfig.metricName}
                        points={chartConfig.points}
                        valueKind={chartConfig.valueKind}
                        currentLegend={chartConfig.currentLegend}
                      />
                    ) : (
                      <div className={common.inlineEmpty}>
                        Chưa có dữ liệu theo ngày trong khoảng thời gian đã chọn.
                      </div>
                    )}

                    {(report.orders_by_status ?? []).length > 0 && (
                      <div className={styles.statusPillStrip}>
                        {report.orders_by_status.map((row) => {
                          const meta = getSellerOrderStatusMeta(row.status);
                          return (
                            <span key={row.status} className={statusPillClass(meta.tone)}>
                              {meta.label}: {formatInt(row.count)}
                            </span>
                          );
                        })}
                      </div>
                    )}
                  </div>
                </section>

                <section className={common.card} aria-labelledby="seller-top-products-heading">
                  <div className={common.cardHeader}>
                    <h2 id="seller-top-products-heading" className={common.cardTitle}>
                      Top sản phẩm bán chạy ({formatInt(report.top_products?.length ?? 0)})
                    </h2>
                    <span className={common.cardMeta}>
                      {report.from_date} → {report.to_date}
                    </span>
                  </div>

                  {(report.top_products ?? []).length === 0 ? (
                    <div className={common.inlineEmpty}>
                      Chưa có sản phẩm nào phát sinh doanh số trong kỳ chọn.
                    </div>
                  ) : (
                    <BarList
                      rows={report.top_products.map((item) => ({
                        label: item.product_name,
                        subLabel: `${formatInt(item.units_sold)} sản phẩm đã bán`,
                        value: item.revenue_vnd,
                      }))}
                      formatValue={formatVnd}
                    />
                  )}
                </section>
              </>
            )
          )}

          {/* 4. Recent Orders Table */}
          <section className={common.card} aria-labelledby="recent-orders-heading">
            <div className={common.cardHeader}>
              <h2 id="recent-orders-heading" className={common.cardTitle}>
                Đơn hàng mới
              </h2>
              <Link href="/seller/don-hang" className={common.cardLink}>
                <span>Tất cả ({formatInt(orders.length)})</span>
                <IconChevronRight size={14} />
              </Link>
            </div>

            {recentOrders.length === 0 ? (
              <div className={common.inlineEmpty}>Chưa có đơn hàng nào.</div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Đơn hàng</th>
                        <th>Người nhận</th>
                        <th>Kênh</th>
                        <th className={common.numCol}>Tiền hàng</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {recentOrders.map((order) => {
                        const meta = getSellerOrderStatusMeta(
                          order.status,
                          order.shipment_job_status,
                          order.tracking_code
                        );
                        const channelMeta = getSalesChannelMeta(order.sales_channel);
                        const nextAction = getNextBackendOrderAction(
                          order.status,
                          order.tracking_code,
                          order.shipment_job_status
                        );
                        const firstItem = order.items?.[0];
                        const thumb =
                          (firstItem && productImageById.get(firstItem.product_id)) || null;
                        const recipient = order.shipping_address?.recipient_name || "Người mua";
                        const province = order.shipping_address?.province_name || "";

                        return (
                          <tr key={order.id}>
                            <td>
                              <div className={styles.orderLeadCell}>
                                {thumb ? (
                                  <img
                                    src={thumb}
                                    alt={firstItem?.product_name || order.order_code}
                                    className={styles.orderThumb}
                                    width={34}
                                    height={44}
                                  />
                                ) : (
                                  <span
                                    className={`${common.thumbPlaceholder} ${styles.orderThumb}`}
                                    aria-hidden="true"
                                  >
                                    <IconPackage size={15} />
                                  </span>
                                )}
                                <div className={styles.opInfo}>
                                  <button
                                    type="button"
                                    className={common.rowTriggerBtn}
                                    onClick={() => setDrawerOrderId(order.id)}
                                  >
                                    {order.order_code}
                                  </button>
                                  <span className={styles.opSecondary}>
                                    {summarizeOrderItems(order.items)}
                                  </span>
                                </div>
                              </div>
                            </td>
                            <td className={styles.buyerCell}>
                              <span className={styles.buyerName} title={recipient}>
                                {recipient}
                              </span>
                              {province && (
                                <span className={styles.buyerSub} title={province}>
                                  {province}
                                </span>
                              )}
                            </td>
                            <td>
                              <span
                                className={`${common.channelTag} ${channelMeta.isStorefront ? common.channelTagStorefront : ""}`}
                                data-sales-channel={channelMeta.value}
                              >
                                {channelMeta.label}
                              </span>
                            </td>
                            <td className={common.numCol}>
                              <strong>{formatVnd(order.subtotal_vnd)}</strong>
                            </td>
                            <td>
                              <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                            </td>
                            <td className={common.numCol}>
                              <div className={common.rowHoverActions}>
                                {nextAction && (
                                  <button
                                    type="button"
                                    className={styles.inlinePrimaryBtn}
                                    disabled={busyOrderId === order.id}
                                    onClick={() => void handleQuickAction(order)}
                                  >
                                    {busyOrderId === order.id
                                      ? "Đang lưu..."
                                      : nextAction.shortLabel}
                                  </button>
                                )}
                                <Link
                                  href={`/seller/don-hang/${order.id}`}
                                  className={styles.inlineActionBtn}
                                >
                                  Chi tiết
                                </Link>
                              </div>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {recentOrders.map((order) => {
                    const meta = getSellerOrderStatusMeta(
                      order.status,
                      order.shipment_job_status,
                      order.tracking_code
                    );
                    const nextAction = getNextBackendOrderAction(
                      order.status,
                      order.tracking_code,
                      order.shipment_job_status
                    );
                    const recipient = order.shipping_address?.recipient_name || "Người mua";
                    return (
                      <div key={order.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div>
                            <Link href={`/seller/don-hang/${order.id}`} className={common.cellLink}>
                              {order.order_code}
                            </Link>
                            <span className={common.cellSub}>{recipient}</span>
                          </div>
                          <strong className="num">{formatVnd(order.subtotal_vnd)}</strong>
                        </div>
                        <p className={styles.opSecondary}>{summarizeOrderItems(order.items)}</p>
                        <div className={common.mobileRowMeta}>
                          <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          <div className={styles.opRight}>
                            {nextAction && (
                              <button
                                type="button"
                                className={styles.inlinePrimaryBtn}
                                disabled={busyOrderId === order.id}
                                onClick={() => void handleQuickAction(order)}
                              >
                                {busyOrderId === order.id ? "Đang lưu..." : nextAction.shortLabel}
                              </button>
                            )}
                            <Link
                              href={`/seller/don-hang/${order.id}`}
                              className={styles.inlineActionBtn}
                            >
                              Chi tiết
                            </Link>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </section>

          {/* 5. Low-Stock SKU Alert Strip */}
          {topLowStock.length > 0 && (
            <section className={common.card} aria-labelledby="op-stock-heading">
              <div className={common.cardHeader}>
                <h2 id="op-stock-heading" className={common.cardTitle}>
                  SKU sắp hết tồn ({formatInt(lowStockAlerts.length)})
                </h2>
                <Link href="/seller/san-pham?filter=LOW_OOS" className={common.cardLink}>
                  <span>Kho sản phẩm</span>
                  <IconChevronRight size={14} />
                </Link>
              </div>

              <div className={styles.opRows}>
                {topLowStock.map((alert) => (
                  <div key={alert.skuId} className={`${styles.opRow} ${common.rowHoverParent}`}>
                    <div className={styles.opRowMain}>
                      {alert.image ? (
                        <img
                          src={alert.image}
                          alt={alert.productName}
                          className={styles.opThumb}
                          width={34}
                          height={44}
                        />
                      ) : (
                        <span
                          className={`${common.thumbPlaceholder} ${styles.opThumb}`}
                          aria-hidden="true"
                        >
                          <IconPackage size={15} />
                        </span>
                      )}
                      <div className={styles.opInfo}>
                        <Link
                          href={`/seller/san-pham/${alert.productId}`}
                          className={styles.opPrimary}
                        >
                          {alert.productName} ({alert.skuLabel})
                        </Link>
                        <span className={styles.opSecondary}>
                          {alert.skuCode}, {formatVnd(alert.priceVnd)}
                        </span>
                      </div>
                    </div>

                    <div className={styles.opRight}>
                      <span
                        className={`${styles.opMoney} ${alert.availableStock === 0 ? styles.opStockCrit : ""}`}
                      >
                        {alert.availableStock === 0
                          ? "Hết hàng"
                          : `Còn ${formatInt(alert.availableStock)}`}
                      </span>
                      <div className={common.rowHoverActions}>
                        <Link
                          href={`/seller/san-pham/${alert.productId}`}
                          className={styles.inlineActionBtn}
                        >
                          Xem sản phẩm
                        </Link>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}
        </>
      )}

      <SellerOrderQuickDrawer
        order={drawerOrder}
        onClose={() => setDrawerOrderId(null)}
        onAction={(order) => void handleQuickAction(order)}
        actionBusy={Boolean(drawerOrder && busyOrderId === drawerOrder.id)}
        actionDisabled={busyOrderId !== null}
      />
    </div>
  );
}
