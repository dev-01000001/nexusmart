"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type {
  GatewayOrder,
  GatewayProduct,
  GatewaySellerBalance,
  GatewaySellerReport,
  GatewayShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, apiBlob, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { BarList, Button, IconChevronRight, IconDownload, StateView } from "@/components";
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
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import { SellerTrendChart } from "@/features/seller/SellerTrendChart";
import {
  canSellerConfirmOrder,
  getOrderStatusMeta,
  getProductCoverImage,
} from "@/features/seller/types";
import { parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type MetricTabKey = "netRevenue" | "gmv" | "orders";

const PRIORITY_STATUS_RANK: Record<string, number> = {
  CONFIRMED: 0,
  PREPARING: 1,
  PENDING: 2,
  RETURN_REQUESTED: 3,
  SHIPPING: 4,
  DELIVERED: 5,
  COMPLETED: 6,
  CANCELLED: 7,
  RETURNED: 8,
};

interface LowStockRow {
  skuId: string;
  skuCode: string;
  skuName: string;
  productId: string;
  productName: string;
  image: string;
  priceVnd: number;
  availableStock: number;
  totalStock: number;
}

function summarizeOrderItems(order: GatewayOrder): string {
  if (!order.items || order.items.length === 0) return "Chưa có dòng sản phẩm";
  const first = order.items[0];
  const rest = order.items.length - 1;
  return rest > 0
    ? `${first.product_name} (+${rest} sản phẩm khác)`
    : `${first.product_name} · ${first.sku_name}`;
}

function formatShortIsoDay(isoDate: string): string {
  if (/^\d{4}-\d{2}-\d{2}$/.test(isoDate)) {
    const [, mm, dd] = isoDate.split("-");
    return `${dd}/${mm}`;
  }
  return formatDayMonthVi(isoDate);
}

function SellerOverviewInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useMockStore();
  const { refreshPendingOrders } = useSellerContext();

  const [activeTab, setActiveTab] = useState<MetricTabKey>("netRevenue");
  const [fromDate, setFromDate] = useState(() => formatVnDateOffset(-29));
  const [toDate, setToDate] = useState(() => formatVnDateOffset(0));
  const [rangeError, setRangeError] = useState<string | null>(null);
  const [exportingCsv, setExportingCsv] = useState(false);

  const [report, setReport] = useState<GatewaySellerReport | null>(null);
  const [reportLoading, setReportLoading] = useState(true);
  const [reportError, setReportError] = useState<string | null>(null);

  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [products, setProducts] = useState<GatewayProduct[]>([]);
  const [balance, setBalance] = useState<GatewaySellerBalance | null>(null);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);

  const loadOverview = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [ordersRes, productsRes, balanceRes] = await Promise.all([
        api<PaginatedResponse<GatewayOrder>>("seller/orders?page=1&page_size=50"),
        api<PaginatedResponse<GatewayProduct>>("seller/products?page=1&page_size=100"),
        api<GatewaySellerBalance>("seller/balance").catch(() => null),
      ]);
      setOrders(ordersRes.items ?? []);
      setProducts(productsRes.items ?? []);
      setBalance(balanceRes);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải tổng quan gian hàng lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  const reportSeqRef = useRef(0);

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
      const res = await api<GatewaySellerReport | { report: GatewaySellerReport }>(
        `seller/reports?${qs.toString()}`
      );
      if (reportSeqRef.current !== seq) return;
      setReport(res && "report" in res ? res.report : res);
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
    if (!uiState) {
      void loadOverview();
    }
  }, [uiState, loadOverview]);

  useEffect(() => {
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
      const blob = await apiBlob(`seller/reports/export?${qs.toString()}`);
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `bao-cao-gian-hang-${fromDate}-${toDate}.csv`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      showToast(`Đã tải xuống báo cáo CSV (${fromDate} đến ${toDate}).`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xuất tệp CSV báo cáo lúc này."));
    } finally {
      setExportingCsv(false);
    }
  };

  const lowStockAlerts = useMemo<LowStockRow[]>(() => {
    const rows: LowStockRow[] = [];
    for (const p of products) {
      const cover = getProductCoverImage(p);
      for (const s of p.skus ?? []) {
        if (!s.is_active) continue;
        if (s.available_stock <= 5) {
          rows.push({
            skuId: s.id,
            skuCode: s.sku_code,
            skuName: s.name,
            productId: p.id,
            productName: p.name,
            image: s.image_url || cover,
            priceVnd: s.price_vnd,
            availableStock: s.available_stock,
            totalStock: s.stock,
          });
        }
      }
    }
    return rows.sort((a, b) => a.availableStock - b.availableStock);
  }, [products]);

  const pendingConfirmCount = useMemo(
    () => orders.filter((o) => o.status === "CONFIRMED").length,
    [orders]
  );
  const preparingCount = useMemo(
    () => orders.filter((o) => o.status === "PREPARING").length,
    [orders]
  );
  const actionableCount = pendingConfirmCount + preparingCount;

  const dailyPoints = useMemo(() => report?.daily ?? [], [report]);

  const chartConfig = useMemo(() => {
    const labels = dailyPoints.map((d) => formatShortIsoDay(d.date));
    if (activeTab === "orders") {
      return {
        labels,
        values: dailyPoints.map((d) => d.order_count),
        metricName: `Số đơn hàng theo ngày (${fromDate} → ${toDate})`,
        formatValue: (v: number) => `${formatInt(v)} đơn`,
        formatTick: (v: number) => formatInt(v),
      };
    }
    if (activeTab === "gmv") {
      return {
        labels,
        values: dailyPoints.map((d) => d.gmv_vnd),
        metricName: `Tổng giá trị đơn hàng (GMV) theo ngày (${fromDate} → ${toDate})`,
        formatValue: formatVnd,
        formatTick: formatCompactVnd,
      };
    }
    return {
      labels,
      values: dailyPoints.map((d) => d.net_revenue_vnd),
      metricName: `Doanh thu thuần sau phí theo ngày (${fromDate} → ${toDate})`,
      formatValue: formatVnd,
      formatTick: formatCompactVnd,
    };
  }, [dailyPoints, activeTab, fromDate, toDate]);

  const topOrders = useMemo(() => {
    return [...orders]
      .sort((a, b) => {
        const diff = (PRIORITY_STATUS_RANK[a.status] ?? 9) - (PRIORITY_STATUS_RANK[b.status] ?? 9);
        if (diff !== 0) return diff;
        return (b.created_at ?? "").localeCompare(a.created_at ?? "");
      })
      .slice(0, 4);
  }, [orders]);

  const topLowStock = useMemo(() => lowStockAlerts.slice(0, 4), [lowStockAlerts]);

  const handleQuickConfirm = async (order: GatewayOrder) => {
    if (!canSellerConfirmOrder(order.status) || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      const res = await api<{ order: GatewayOrder }>(`seller/orders/${order.id}/confirm`, {
        method: "POST",
      });
      if (res.order) {
        setOrders((prev) => prev.map((item) => (item.id === order.id ? res.order : item)));
      } else {
        await loadOverview();
      }
      await refreshPendingOrders();
      showToast(`Đã xác nhận đơn ${order.order_code} và chuyển sang chuẩn bị hàng.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setBusyOrderId(null);
    }
  };

  const statusPillClass = (tone: "neutral" | "warn" | "pos" | "neg") =>
    tone === "pos"
      ? `${common.pill} ${common.pillPos}`
      : tone === "warn"
        ? `${common.pill} ${common.pillWarn}`
        : tone === "neg"
          ? `${common.pill} ${common.pillNeg}`
          : common.pill;

  const availableVnd = balance?.available_vnd ?? 0;
  const pendingVnd = balance?.pending_vnd ?? 0;
  const totalFeesVnd = (report?.commission_vnd ?? 0) + (report?.payment_fee_vnd ?? 0);

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tổng quan & Báo cáo gian hàng</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(pendingConfirmCount)} đơn chờ xác nhận,{" "}
            {formatInt(lowStockAlerts.length)} SKU tồn khả dụng từ 5 chiếc trở xuống · Kỳ {fromDate}{" "}
            đến {toDate}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/san-pham/moi">
            Thêm sản phẩm
          </Button>
          <Button
            variant="ink"
            size="sm"
            shape="app"
            href={
              pendingConfirmCount > 0 ? "/seller/don-hang?status=CONFIRMED" : "/seller/don-hang"
            }
          >
            {pendingConfirmCount > 0
              ? `Xác nhận đơn chờ (${formatInt(pendingConfirmCount)})`
              : "Xem đơn hàng"}
          </Button>
        </div>
      </header>

      {(uiState === "loading" || (!uiState && loading)) && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tổng hợp số liệu doanh thu và đối soát gian hàng..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải tổng quan gian hàng"
          message={errorMsg ?? "Kết nối đến dịch vụ đối soát đơn hàng tạm gián đoạn."}
          actionLabel="Tải lại"
          onAction={() => void loadOverview()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa phát sinh đơn hàng trong kỳ chọn"
          message="Đăng sản phẩm có đủ biến thể size và tạo chương trình khuyến mãi để bắt đầu ghi nhận doanh thu."
          actionLabel="Thêm sản phẩm đầu tiên"
          actionHref="/seller/san-pham/moi"
        />
      )}

      {!uiState && !loading && !errorMsg && (
        <>
          <section className={common.balanceStrip} aria-label="Số dư và hàng chờ vận hành">
            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Số dư khả dụng</span>
              <span
                className={`${common.balanceValue} ${availableVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(availableVnd)}
              </span>
              {availableVnd > 0 ? (
                <Link href="/seller/tai-chinh" className={common.balanceActionLink}>
                  Xem tài chính
                </Link>
              ) : (
                <span className={common.balanceCaption}>Hoàn tất đối soát để giải ngân</span>
              )}
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Chờ đối soát</span>
              <span
                className={`${common.balanceValue} ${pendingVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(pendingVnd)}
              </span>
              <span className={common.balanceCaption}>Giữ an toàn 72 giờ sau khi hoàn tất</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Đơn chờ xử lý</span>
              <span
                className={`${common.balanceValue} ${actionableCount === 0 ? common.balanceValueZero : ""}`}
              >
                {formatInt(actionableCount)}
              </span>
              <span className={common.balanceCaption}>
                {actionableCount > 0
                  ? `${formatInt(pendingConfirmCount)} chờ xác nhận, ${formatInt(preparingCount)} đang chuẩn bị`
                  : "Đã xử lý hết đơn cần giao"}
              </span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>SKU sắp hết hàng</span>
              <span
                className={`${common.balanceValue} ${lowStockAlerts.length === 0 ? common.balanceValueZero : ""}`}
              >
                {formatInt(lowStockAlerts.length)}
              </span>
              <span className={common.balanceCaption}>
                {lowStockAlerts.length > 0
                  ? "Tồn khả dụng từ 5 chiếc trở xuống"
                  : "Mọi biến thể đang hoạt động đều đủ tồn"}
              </span>
            </div>
          </section>

          {/* Report date filter bar & CSV export */}
          <section className={common.card} aria-label="Bộ lọc kỳ báo cáo gian hàng">
            <div className={common.toolbar}>
              <div className={common.toolbarLeft}>
                <div className={common.segmented} role="group" aria-label="Chọn nhanh kỳ báo cáo">
                  <button
                    type="button"
                    className={common.segmentedBtn}
                    onClick={() => applyPresetDays(7)}
                  >
                    7 ngày
                  </button>
                  <button
                    type="button"
                    className={common.segmentedBtn}
                    onClick={() => applyPresetDays(30)}
                  >
                    30 ngày
                  </button>
                  <button
                    type="button"
                    className={common.segmentedBtn}
                    onClick={() => applyPresetDays(90)}
                  >
                    90 ngày
                  </button>
                </div>

                <label className={common.fieldLabel} htmlFor="rep-from">
                  Từ ngày
                </label>
                <input
                  id="rep-from"
                  type="date"
                  className={common.textInput}
                  style={{ width: 152, height: 34 }}
                  value={fromDate}
                  onChange={(e) => setFromDate(e.target.value)}
                />
                <label className={common.fieldLabel} htmlFor="rep-to">
                  Đến ngày
                </label>
                <input
                  id="rep-to"
                  type="date"
                  className={common.textInput}
                  style={{ width: 152, height: 34 }}
                  value={toDate}
                  onChange={(e) => setToDate(e.target.value)}
                />
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

          {reportLoading ? (
            <StateView
              kind="loading"
              surface="app"
              title="Đang tổng hợp báo cáo doanh thu gian hàng..."
              rows={4}
            />
          ) : reportError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải báo cáo doanh thu gian hàng"
              message={reportError}
              actionLabel="Thử lại"
              onAction={() => void loadReport(fromDate, toDate)}
            />
          ) : (
            report && (
              <>
                <section className={common.balanceStrip} aria-label="Tổng hợp báo cáo kỳ chọn">
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Tổng giá trị đơn hàng (GMV)</span>
                    <span className={common.balanceValue}>{formatVnd(report.gmv_vnd)}</span>
                    <span className={common.balanceCaption}>
                      {formatInt(report.total_orders)} đơn trong kỳ
                    </span>
                  </div>
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Doanh thu thuần sau phí</span>
                    <span className={common.balanceValue}>{formatVnd(report.net_revenue_vnd)}</span>
                    <span className={common.balanceCaption}>
                      {formatInt(report.completed_orders)} đơn hoàn tất
                    </span>
                  </div>
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Phí sàn & phí thanh toán</span>
                    <span className={common.balanceValue}>{formatVnd(totalFeesVnd)}</span>
                    <span className={common.balanceCaption}>
                      Hoa hồng {formatVnd(report.commission_vnd)} · Thanh toán{" "}
                      {formatVnd(report.payment_fee_vnd)}
                    </span>
                  </div>
                  <div className={common.balanceCell}>
                    <span className={common.balanceLabel}>Tỷ lệ hủy / trả hàng</span>
                    <span className={common.balanceValue}>
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
                        <span className={styles.kpiTabValue}>
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
                        <span className={styles.kpiTabValue}>{formatVnd(report.gmv_vnd)}</span>
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
                        <span className={styles.kpiTabValue}>{formatInt(report.total_orders)}</span>
                      </span>
                    </button>
                  </div>

                  <div className={styles.chartBody}>
                    <div className={styles.chartCaption}>
                      <h2 className={styles.chartHeading}>{chartConfig.metricName}</h2>
                      <span className={styles.chartPeriod}>
                        Di chuột trên biểu đồ để xem từng ngày · Tối đa 366 ngày
                      </span>
                    </div>

                    {dailyPoints.length > 0 ? (
                      <SellerTrendChart
                        labels={chartConfig.labels}
                        values={chartConfig.values}
                        metricName={chartConfig.metricName}
                        formatValue={chartConfig.formatValue}
                        formatTick={chartConfig.formatTick}
                        annotation={null}
                        desktopHeight={194}
                        mobileHeight={180}
                      />
                    ) : (
                      <div className={common.inlineEmpty}>
                        Chưa có dữ liệu doanh thu theo ngày trong khoảng thời gian đã chọn.
                      </div>
                    )}

                    {(report.orders_by_status ?? []).length > 0 && (
                      <div
                        style={{
                          display: "flex",
                          flexWrap: "wrap",
                          gap: 8,
                          paddingTop: 10,
                          borderTop: "1px solid var(--rule)",
                        }}
                      >
                        {report.orders_by_status.map((row) => {
                          const meta = getOrderStatusMeta(row.status);
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
                      Top sản phẩm bán chạy trong kỳ ({formatInt(report.top_products?.length ?? 0)})
                    </h2>
                    <span className={common.cardMeta}>
                      Kỳ {report.from_date} → {report.to_date}
                    </span>
                  </div>

                  {(report.top_products ?? []).length === 0 ? (
                    <div className={common.inlineEmpty}>
                      Chưa có sản phẩm nào phát sinh doanh số trong kỳ báo cáo này.
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

          <div className={styles.pairedGrid}>
            <section className={styles.opCard} aria-labelledby="op-orders-heading">
              <div className={common.cardHeader}>
                <h2 id="op-orders-heading" className={common.cardTitle}>
                  Đơn hàng cần xử lý
                </h2>
                <Link href="/seller/don-hang" className={common.cardLink}>
                  <span>Xem tất cả ({formatInt(orders.length)})</span>
                  <IconChevronRight size={13} />
                </Link>
              </div>

              <div className={styles.opRows}>
                {topOrders.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Chưa có đơn hàng nào thuộc gian hàng này.
                  </div>
                ) : (
                  topOrders.map((order) => {
                    const meta = getOrderStatusMeta(order.status);
                    const canConfirm = canSellerConfirmOrder(order.status);
                    const recipient = order.shipping_address?.recipient_name || "Khách đặt hàng";
                    const province = order.shipping_address?.province_name || "Chưa rõ khu vực";
                    return (
                      <div key={order.id} className={styles.opRow}>
                        <div className={styles.opRowMain}>
                          <div className={styles.opInfo}>
                            <Link
                              href={`/seller/don-hang/${order.id}`}
                              className={styles.opPrimary}
                            >
                              {order.order_code} · {recipient}
                            </Link>
                            <span className={styles.opSecondary}>
                              {summarizeOrderItems(order)} · {province}
                              {order.created_at ? ` · ${formatDateTimeVi(order.created_at)}` : ""}
                            </span>
                          </div>
                        </div>

                        <div className={styles.opRight}>
                          <div className={styles.opFigure}>
                            <span className={styles.opMoney}>{formatVnd(order.subtotal_vnd)}</span>
                            <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          </div>

                          {canConfirm ? (
                            <button
                              type="button"
                              className={styles.inlineActionBtn}
                              disabled={busyOrderId === order.id}
                              onClick={() => void handleQuickConfirm(order)}
                            >
                              {busyOrderId === order.id ? "Đang xử lý..." : "Xác nhận"}
                            </button>
                          ) : (
                            <Link
                              href={`/seller/don-hang/${order.id}`}
                              className={styles.inlineActionBtn}
                            >
                              Chi tiết
                            </Link>
                          )}
                        </div>
                      </div>
                    );
                  })
                )}
              </div>
            </section>

            <section className={styles.opCard} aria-labelledby="op-stock-heading">
              <div className={common.cardHeader}>
                <h2 id="op-stock-heading" className={common.cardTitle}>
                  Cảnh báo tồn kho theo SKU
                </h2>
                <Link href="/seller/san-pham?filter=LOW_OOS" className={common.cardLink}>
                  <span>Quản lý kho ({formatInt(lowStockAlerts.length)})</span>
                  <IconChevronRight size={13} />
                </Link>
              </div>

              <div className={styles.opRows}>
                {topLowStock.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Tất cả biến thể SKU đang mở bán đều có tồn kho khả dụng trên 5 chiếc.
                  </div>
                ) : (
                  topLowStock.map((alert) => (
                    <div key={alert.skuId} className={styles.opRow}>
                      <div className={styles.opRowMain}>
                        <img
                          src={alert.image}
                          alt={alert.productName}
                          className={styles.opThumb}
                          width={34}
                          height={45}
                        />
                        <div className={styles.opInfo}>
                          <Link
                            href={`/seller/san-pham/${alert.productId}`}
                            className={styles.opPrimary}
                          >
                            {alert.productName} · {alert.skuName}
                          </Link>
                          <span className={styles.opSecondary}>
                            {alert.skuCode} · {formatVnd(alert.priceVnd)}
                          </span>
                        </div>
                      </div>

                      <div className={styles.opRight}>
                        <div className={styles.opFigure}>
                          <span
                            className={`${styles.opMoney} ${alert.availableStock === 0 ? styles.opStockCrit : ""}`}
                          >
                            {alert.availableStock === 0
                              ? "0 chiếc"
                              : `Còn ${formatInt(alert.availableStock)}`}
                          </span>
                          <span className={styles.opSubFigure}>
                            {alert.availableStock === 0 ? "Hết tồn khả dụng" : "Ngưỡng tối thiểu 5"}
                          </span>
                        </div>
                        <Link
                          href={`/seller/san-pham/${alert.productId}`}
                          className={styles.inlineActionBtn}
                        >
                          Cập nhật
                        </Link>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </section>
          </div>
        </>
      )}
    </div>
  );
}

function SellerOverviewContent() {
  return (
    <SellerShopGate pageTitle="Tổng quan gian hàng">
      {(shop) => <SellerOverviewInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerOverviewPage() {
  return (
    <SellerSuspense>
      <SellerOverviewContent />
    </SellerSuspense>
  );
}
