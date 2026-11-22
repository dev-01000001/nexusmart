"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type {
  GatewayOrder,
  GatewayProduct,
  GatewaySellerBalance,
  GatewayShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconChevronRight, StateView } from "@/components";
import {
  formatCompactVnd,
  formatDateTimeVi,
  formatDayMonthVi,
  formatInt,
  formatSignedInt,
  formatSignedVnd,
  formatVnDayKey,
  formatVnd,
} from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import { SellerTrendChart } from "@/features/seller/SellerTrendChart";
import {
  canSellerConfirmOrder,
  computeOrderNetVnd,
  getOrderStatusMeta,
  getProductCoverImage,
} from "@/features/seller/types";
import { formatDeltaPct, parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type MetricTabKey = "netRevenue" | "orders" | "avgOrder";

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

interface DayBucket {
  key: string;
  label: string;
  gmv: number;
  netRevenue: number;
  orders: number;
}

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

const DAY_MS = 24 * 60 * 60 * 1000;

function isRevenueOrder(status: string): boolean {
  return status !== "CANCELLED" && status !== "RETURNED";
}

function summarizeOrderItems(order: GatewayOrder): string {
  if (!order.items || order.items.length === 0) return "Chưa có dòng sản phẩm";
  const first = order.items[0];
  const rest = order.items.length - 1;
  return rest > 0
    ? `${first.product_name} (+${rest} sản phẩm khác)`
    : `${first.product_name} · ${first.sku_name}`;
}

function SellerOverviewInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useMockStore();
  const { refreshPendingOrders } = useSellerContext();

  const [activeTab, setActiveTab] = useState<MetricTabKey>("netRevenue");
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
        api<PaginatedResponse<GatewayOrder>>("seller/orders?page=1&page_size=100"),
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

  useEffect(() => {
    if (!uiState) {
      void loadOverview();
    }
  }, [uiState, loadOverview]);

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

  const periodStats = useMemo(() => {
    const nowMs = Date.now();
    const currentDays: DayBucket[] = [];
    const currentIndexByKey = new Map<string, number>();

    for (let offset = 6; offset >= 0; offset--) {
      const ts = nowMs - offset * DAY_MS;
      const key = formatVnDayKey(ts);
      currentIndexByKey.set(key, currentDays.length);
      currentDays.push({
        key,
        label: formatDayMonthVi(new Date(ts).toISOString()),
        gmv: 0,
        netRevenue: 0,
        orders: 0,
      });
    }

    const priorKeys = new Set<string>();
    for (let offset = 13; offset >= 7; offset--) {
      priorKeys.add(formatVnDayKey(nowMs - offset * DAY_MS));
    }

    let priorGmv = 0;
    let priorNet = 0;
    let priorOrders = 0;

    for (const order of orders) {
      if (!isRevenueOrder(order.status) || !order.created_at) continue;
      const key = formatVnDayKey(order.created_at);
      const net = computeOrderNetVnd(order);
      const curIdx = currentIndexByKey.get(key);
      if (curIdx !== undefined) {
        currentDays[curIdx].gmv += order.subtotal_vnd;
        currentDays[curIdx].netRevenue += net;
        currentDays[curIdx].orders += 1;
      } else if (priorKeys.has(key)) {
        priorGmv += order.subtotal_vnd;
        priorNet += net;
        priorOrders += 1;
      }
    }

    const total7dGmv = currentDays.reduce((s, d) => s + d.gmv, 0);
    const total7dNetRevenue = currentDays.reduce((s, d) => s + d.netRevenue, 0);
    const total7dOrders = currentDays.reduce((s, d) => s + d.orders, 0);
    const avgOrderValue = total7dOrders > 0 ? Math.trunc(total7dGmv / total7dOrders) : 0;
    const priorAvgOrder = priorOrders > 0 ? Math.trunc(priorGmv / priorOrders) : 0;

    return {
      daily7d: currentDays,
      periodLabel: `${currentDays[0]?.label ?? ""} - ${currentDays[currentDays.length - 1]?.label ?? ""}`,
      total7dGmv,
      total7dNetRevenue,
      total7dOrders,
      avgOrderValue,
      prior7dGmv: priorGmv,
      prior7dNetRevenue: priorNet,
      prior7dOrders: priorOrders,
      priorAvgOrder,
      gmvDelta: total7dGmv - priorGmv,
      netDelta: total7dNetRevenue - priorNet,
      ordersDelta: total7dOrders - priorOrders,
      avgOrderDelta: avgOrderValue - priorAvgOrder,
    };
  }, [orders]);

  const netPct = formatDeltaPct(periodStats.total7dNetRevenue, periodStats.prior7dNetRevenue);
  const ordersPct = formatDeltaPct(periodStats.total7dOrders, periodStats.prior7dOrders);
  const aovPct = formatDeltaPct(periodStats.avgOrderValue, periodStats.priorAvgOrder);
  const gmvPct = formatDeltaPct(periodStats.total7dGmv, periodStats.prior7dGmv);

  const chartConfig = useMemo(() => {
    const labels = periodStats.daily7d.map((d) => d.label);
    if (activeTab === "orders") {
      return {
        labels,
        values: periodStats.daily7d.map((d) => d.orders),
        metricName: "Số đơn hợp lệ theo ngày (7 ngày gần nhất)",
        formatValue: (v: number) => `${formatInt(v)} đơn`,
        formatTick: (v: number) => formatInt(v),
      };
    }
    if (activeTab === "avgOrder") {
      return {
        labels,
        values: periodStats.daily7d.map((d) => (d.orders > 0 ? Math.trunc(d.gmv / d.orders) : 0)),
        metricName: "Giá trị đơn trung bình theo ngày",
        formatValue: formatVnd,
        formatTick: formatCompactVnd,
      };
    }
    return {
      labels,
      values: periodStats.daily7d.map((d) => d.netRevenue),
      metricName: "Doanh thu thuần sau phí 7 ngày gần nhất",
      formatValue: formatVnd,
      formatTick: formatCompactVnd,
    };
  }, [periodStats.daily7d, activeTab]);

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

  const deltaClass = (dir: "up" | "down" | "flat") =>
    dir === "up" ? common.deltaPos : dir === "down" ? common.deltaNeg : common.deltaFlat;

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

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tổng quan gian hàng</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(pendingConfirmCount)} đơn chờ xác nhận,{" "}
            {formatInt(lowStockAlerts.length)} SKU tồn khả dụng từ 5 chiếc trở xuống · Kỳ{" "}
            {periodStats.periodLabel}
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
          title="Gian hàng chưa phát sinh đơn hàng trong 7 ngày qua"
          message="Đăng sản phẩm có đủ biến thể size và đăng ký suất Flash sale để bắt đầu ghi nhận doanh thu."
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
              <span className={common.balanceCaption}>Giữ an toàn cho tới khi đơn hoàn tất</span>
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

          <section className={common.cardFlush} aria-label="Biểu đồ hiệu suất 7 ngày">
            <div className={styles.kpiTabs} role="tablist" aria-label="Chỉ số theo ngày">
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "netRevenue"}
                className={`${styles.kpiTab} ${activeTab === "netRevenue" ? styles.kpiTabActive : ""}`}
                onClick={() => setActiveTab("netRevenue")}
              >
                <span className={styles.kpiTabLabel}>Doanh thu thuần (7 ngày)</span>
                <span className={styles.kpiTabRow}>
                  <span className={styles.kpiTabValue}>
                    {formatVnd(periodStats.total7dNetRevenue)}
                  </span>
                  <span className={`${styles.kpiTabDelta} ${deltaClass(netPct.direction)}`}>
                    {netPct.label}
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
                <span className={styles.kpiTabLabel}>Số đơn hợp lệ (7 ngày)</span>
                <span className={styles.kpiTabRow}>
                  <span className={styles.kpiTabValue}>{formatInt(periodStats.total7dOrders)}</span>
                  <span className={`${styles.kpiTabDelta} ${deltaClass(ordersPct.direction)}`}>
                    {ordersPct.label}
                  </span>
                </span>
              </button>

              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "avgOrder"}
                className={`${styles.kpiTab} ${activeTab === "avgOrder" ? styles.kpiTabActive : ""}`}
                onClick={() => setActiveTab("avgOrder")}
              >
                <span className={styles.kpiTabLabel}>Giá trị đơn trung bình</span>
                <span className={styles.kpiTabRow}>
                  <span className={styles.kpiTabValue}>{formatVnd(periodStats.avgOrderValue)}</span>
                  <span className={`${styles.kpiTabDelta} ${deltaClass(aovPct.direction)}`}>
                    {aovPct.label}
                  </span>
                </span>
              </button>
            </div>

            <div className={styles.chartBody}>
              <div className={styles.chartCaption}>
                <h2 className={styles.chartHeading}>{chartConfig.metricName}</h2>
                <span className={styles.chartPeriod}>
                  Di chuột trên biểu đồ để xem từng ngày · Đơn vị VND
                </span>
              </div>

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

              <div className={styles.compareWrap}>
                <table className={styles.compareTable}>
                  <thead>
                    <tr>
                      <th>So với 7 ngày trước đó</th>
                      <th className={common.numCol}>7 ngày trước</th>
                      <th className={common.numCol}>7 ngày gần nhất</th>
                      <th className={common.numCol}>Chênh lệch</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>Tổng giá trị đơn hàng (GMV)</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(periodStats.prior7dGmv)}
                      </td>
                      <td className={common.numCol}>{formatVnd(periodStats.total7dGmv)}</td>
                      <td className={`${common.numCol} ${deltaClass(gmvPct.direction)}`}>
                        {formatSignedVnd(periodStats.gmvDelta)} ({gmvPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Doanh thu thuần sau chiết khấu & phí sàn</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(periodStats.prior7dNetRevenue)}
                      </td>
                      <td className={common.numCol}>{formatVnd(periodStats.total7dNetRevenue)}</td>
                      <td className={`${common.numCol} ${deltaClass(netPct.direction)}`}>
                        {formatSignedVnd(periodStats.netDelta)} ({netPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Số đơn hợp lệ và giá trị trung bình</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatInt(periodStats.prior7dOrders)} đơn
                      </td>
                      <td className={common.numCol}>
                        {formatInt(periodStats.total7dOrders)} đơn (
                        {formatVnd(periodStats.avgOrderValue)})
                      </td>
                      <td className={`${common.numCol} ${deltaClass(ordersPct.direction)}`}>
                        {formatSignedInt(periodStats.ordersDelta)} đơn (
                        {formatSignedVnd(periodStats.avgOrderDelta)})
                      </td>
                    </tr>
                  </tbody>
                </table>

                <div className={styles.compareMobile}>
                  <div className={styles.compareMobileHead}>So với 7 ngày trước đó</div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Tổng giá trị đơn hàng (GMV)</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatVnd(periodStats.total7dGmv)}
                      </span>
                      <span className={deltaClass(gmvPct.direction)}>
                        {formatSignedVnd(periodStats.gmvDelta)} ({gmvPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Doanh thu thuần sau phí sàn</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatVnd(periodStats.total7dNetRevenue)}
                      </span>
                      <span className={deltaClass(netPct.direction)}>
                        {formatSignedVnd(periodStats.netDelta)} ({netPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Số đơn và giá trị trung bình</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatInt(periodStats.total7dOrders)} đơn (
                        {formatVnd(periodStats.avgOrderValue)})
                      </span>
                      <span className={deltaClass(ordersPct.direction)}>
                        {formatSignedInt(periodStats.ordersDelta)} đơn ({ordersPct.label})
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </section>

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
