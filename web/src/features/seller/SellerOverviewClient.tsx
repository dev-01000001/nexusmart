"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconChevronRight, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import {
  formatCompactVnd,
  formatInt,
  formatSignedInt,
  formatSignedVnd,
  formatVnd,
} from "@/lib/format";
import { SellerTrendChart } from "./SellerTrendChart";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { useSellerContext } from "./SellerShell";
import {
  computeSellerNetReceivable,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  summarizeOrderItems,
  type GwSellerBalance,
  type GwSellerOrder,
  type GwSellerProduct,
} from "./sellerApiTypes";
import { formatDeltaPct, parseUiState } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/page.module.css";

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
};

interface LowStockRow {
  productId: string;
  productName: string;
  skuId: string;
  skuCode: string;
  skuLabel: string;
  availableStock: number;
  priceVnd: number;
  image: string;
}

interface DailyBucket {
  key: string;
  label: string;
  gmv: number;
  netRevenue: number;
  orders: number;
}

function buildDailyBuckets(orders: GwSellerOrder[]): {
  daily7d: DailyBucket[];
  total7dGmv: number;
  total7dNet: number;
  total7dOrders: number;
  prior7dGmv: number;
  prior7dNet: number;
  prior7dOrders: number;
} {
  const vnOffsetMs = 7 * 3600_000;
  const nowVn = new Date(Date.now() + vnOffsetMs);
  const todayUtcDay = Date.UTC(nowVn.getUTCFullYear(), nowVn.getUTCMonth(), nowVn.getUTCDate());
  const dayMs = 24 * 3600_000;

  const buckets: DailyBucket[] = [];
  const byKey = new Map<string, DailyBucket>();
  for (let i = 6; i >= 0; i--) {
    const d = new Date(todayUtcDay - i * dayMs);
    const key = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-${String(d.getUTCDate()).padStart(2, "0")}`;
    const label = `${String(d.getUTCDate()).padStart(2, "0")}/${String(d.getUTCMonth() + 1).padStart(2, "0")}`;
    const b: DailyBucket = { key, label, gmv: 0, netRevenue: 0, orders: 0 };
    buckets.push(b);
    byKey.set(key, b);
  }

  let prior7dGmv = 0;
  let prior7dNet = 0;
  let prior7dOrders = 0;
  const startCur = todayUtcDay - 6 * dayMs;
  const startPrior = todayUtcDay - 13 * dayMs;

  for (const o of orders) {
    if (o.status.toUpperCase() === "CANCELLED") continue;
    const ts = o.created_at ? Date.parse(o.created_at) : Number.NaN;
    if (Number.isNaN(ts)) continue;
    const d = new Date(ts + vnOffsetMs);
    const dayStart = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
    const net = computeSellerNetReceivable(o);
    if (dayStart >= startCur && dayStart <= todayUtcDay) {
      const key = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-${String(d.getUTCDate()).padStart(2, "0")}`;
      const bucket = byKey.get(key);
      if (bucket) {
        bucket.gmv += o.subtotal_vnd;
        bucket.netRevenue += net;
        bucket.orders += 1;
      }
    } else if (dayStart >= startPrior && dayStart < startCur) {
      prior7dGmv += o.subtotal_vnd;
      prior7dNet += net;
      prior7dOrders += 1;
    }
  }

  const total7dGmv = buckets.reduce((s, b) => s + b.gmv, 0);
  const total7dNet = buckets.reduce((s, b) => s + b.netRevenue, 0);
  const total7dOrders = buckets.reduce((s, b) => s + b.orders, 0);

  return {
    daily7d: buckets,
    total7dGmv,
    total7dNet,
    total7dOrders,
    prior7dGmv,
    prior7dNet,
    prior7dOrders,
  };
}

export interface SellerOverviewClientProps {
  initialOrders: GwSellerOrder[];
  initialProducts: GwSellerProduct[];
  initialBalance: GwSellerBalance | null;
  loadError: string | null;
}

export function SellerOverviewClient({
  initialOrders,
  initialProducts,
  initialBalance,
  loadError,
}: SellerOverviewClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { shop, showToast } = useSellerContext();

  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<MetricTabKey>("netRevenue");

  const shopName = shop?.name ?? "Gian hàng của bạn";

  const lowStockAlerts = useMemo<LowStockRow[]>(() => {
    const rows: LowStockRow[] = [];
    for (const p of initialProducts) {
      const fallbackImg = p.images?.[0] || "/products/ao-thun-cotton-trang.png";
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
  const actionableCount = pendingConfirmCount + packingCount;

  const stats = useMemo(() => buildDailyBuckets(orders), [orders]);
  const avgOrderValue =
    stats.total7dOrders > 0 ? Math.round(stats.total7dGmv / stats.total7dOrders) : 0;
  const priorAvgOrder =
    stats.prior7dOrders > 0 ? Math.round(stats.prior7dGmv / stats.prior7dOrders) : 0;
  const avgOrderDelta = avgOrderValue - priorAvgOrder;

  const netPct = formatDeltaPct(stats.total7dNet, stats.prior7dNet);
  const ordersPct = formatDeltaPct(stats.total7dOrders, stats.prior7dOrders);
  const aovPct = formatDeltaPct(avgOrderValue, priorAvgOrder);
  const gmvPct = formatDeltaPct(stats.total7dGmv, stats.prior7dGmv);

  const chartConfig = useMemo(() => {
    const labels = stats.daily7d.map((d) => d.label);
    if (activeTab === "orders") {
      return {
        labels,
        values: stats.daily7d.map((d) => d.orders),
        metricName: "Số đơn phát sinh theo ngày",
        formatValue: (v: number) => `${formatInt(v)} đơn`,
        formatTick: (v: number) => formatInt(v),
      };
    }
    if (activeTab === "avgOrder") {
      return {
        labels,
        values: stats.daily7d.map((d) => (d.orders > 0 ? Math.round(d.gmv / d.orders) : 0)),
        metricName: "Giá trị đơn trung bình theo ngày",
        formatValue: formatVnd,
        formatTick: formatCompactVnd,
      };
    }
    return {
      labels,
      values: stats.daily7d.map((d) => d.netRevenue),
      metricName: "Doanh thu thuần sau phí 7 ngày gần nhất",
      formatValue: formatVnd,
      formatTick: formatCompactVnd,
    };
  }, [stats.daily7d, activeTab]);

  const topOrders = useMemo(() => {
    return [...orders]
      .sort(
        (a, b) =>
          (PRIORITY_STATUS_RANK[a.status.toUpperCase()] ?? 9) -
          (PRIORITY_STATUS_RANK[b.status.toUpperCase()] ?? 9)
      )
      .slice(0, 4);
  }, [orders]);

  const topLowStock = useMemo(() => lowStockAlerts.slice(0, 4), [lowStockAlerts]);

  const handleQuickAction = async (order: GwSellerOrder) => {
    const action = getNextBackendOrderAction(order.status);
    if (!action || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      const endpoint =
        action.kind === "confirm"
          ? `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`
          : `/api/seller/orders/${encodeURIComponent(order.id)}/ship`;
      const res = await api<{ order: GwSellerOrder }>(endpoint, { method: "POST" });
      if (res.order) {
        setOrders((prev) => prev.map((o) => (o.id === res.order.id ? res.order : o)));
      }
      showToast(`Đơn ${order.order_code}: ${action.label.toLowerCase()} thành công.`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật đơn hàng lúc này.");
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

  const availableVnd = initialBalance?.available_vnd ?? 0;
  const pendingVnd = initialBalance?.pending_vnd ?? 0;

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tổng quan gian hàng</h1>
          <p className={common.finding}>
            {shopName} · {formatInt(pendingConfirmCount)} đơn chờ xác nhận,{" "}
            {formatInt(lowStockAlerts.length)} SKU tồn khả dụng từ 5 chiếc trở xuống
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

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tổng hợp số liệu doanh thu và đối soát gian hàng..."
          rows={5}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải tổng quan gian hàng"
          message={loadError ?? "Kết nối đến dịch vụ đơn hàng tạm gián đoạn."}
          actionLabel="Tải lại"
          onAction={() => router.refresh()}
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

      {!uiState && !loadError && (
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
                  Xem chi tiết tài chính
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
              <span className={common.balanceCaption}>
                Tạm giữ chờ người mua xác nhận nhận hàng
              </span>
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
                  ? `${formatInt(pendingConfirmCount)} chờ duyệt, ${formatInt(packingCount)} đang gói`
                  : "Đã xử lý toàn bộ đơn hiện tại"}
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
                  : "Mọi biến thể đều đủ tồn kho"}
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
                <span className={styles.kpiTabLabel}>Doanh thu thuần</span>
                <span className={styles.kpiTabRow}>
                  <span className={styles.kpiTabValue}>{formatVnd(stats.total7dNet)}</span>
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
                <span className={styles.kpiTabLabel}>Số đơn 7 ngày</span>
                <span className={styles.kpiTabRow}>
                  <span className={styles.kpiTabValue}>{formatInt(stats.total7dOrders)}</span>
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
                  <span className={styles.kpiTabValue}>{formatVnd(avgOrderValue)}</span>
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
                  Tổng hợp từ đơn hàng thực tế 7 ngày qua · Đơn vị VND
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
                      <th>So với 7 ngày trước</th>
                      <th className={common.numCol}>7 ngày trước</th>
                      <th className={common.numCol}>7 ngày này</th>
                      <th className={common.numCol}>Chênh lệch</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>Tổng giá trị đơn hàng (GMV)</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(stats.prior7dGmv)}
                      </td>
                      <td className={common.numCol}>{formatVnd(stats.total7dGmv)}</td>
                      <td className={`${common.numCol} ${deltaClass(gmvPct.direction)}`}>
                        {formatSignedVnd(stats.total7dGmv - stats.prior7dGmv)} ({gmvPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Doanh thu thuần sau phí sàn</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(stats.prior7dNet)}
                      </td>
                      <td className={common.numCol}>{formatVnd(stats.total7dNet)}</td>
                      <td className={`${common.numCol} ${deltaClass(netPct.direction)}`}>
                        {formatSignedVnd(stats.total7dNet - stats.prior7dNet)} ({netPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Số đơn hàng và giá trị trung bình</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatInt(stats.prior7dOrders)} đơn
                      </td>
                      <td className={common.numCol}>
                        {formatInt(stats.total7dOrders)} đơn ({formatVnd(avgOrderValue)})
                      </td>
                      <td className={`${common.numCol} ${deltaClass(ordersPct.direction)}`}>
                        {formatSignedInt(stats.total7dOrders - stats.prior7dOrders)} đơn (
                        {formatSignedVnd(avgOrderDelta)})
                      </td>
                    </tr>
                  </tbody>
                </table>

                <div className={styles.compareMobile}>
                  <div className={styles.compareMobileHead}>So với 7 ngày trước</div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Tổng giá trị đơn hàng (GMV)</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>{formatVnd(stats.total7dGmv)}</span>
                      <span className={deltaClass(gmvPct.direction)}>
                        {formatSignedVnd(stats.total7dGmv - stats.prior7dGmv)} ({gmvPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Doanh thu thuần sau phí sàn</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>{formatVnd(stats.total7dNet)}</span>
                      <span className={deltaClass(netPct.direction)}>
                        {formatSignedVnd(stats.total7dNet - stats.prior7dNet)} ({netPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Số đơn và giá trị trung bình</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatInt(stats.total7dOrders)} đơn ({formatVnd(avgOrderValue)})
                      </span>
                      <span className={deltaClass(ordersPct.direction)}>
                        {formatSignedInt(stats.total7dOrders - stats.prior7dOrders)} đơn (
                        {ordersPct.label})
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

              {topOrders.length === 0 ? (
                <div className={common.inlineEmpty}>
                  Gian hàng chưa có đơn hàng nào. Đơn mới từ người mua sẽ xuất hiện tại đây.
                </div>
              ) : (
                <div className={styles.opRows}>
                  {topOrders.map((order) => {
                    const meta = getSellerOrderStatusMeta(order.status);
                    const channelMeta = getSalesChannelMeta(order.sales_channel);
                    const nextAction = getNextBackendOrderAction(order.status);
                    const recipient = order.shipping_address?.recipient_name || "Người mua";
                    const province = order.shipping_address?.province_name || "";
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
                              {channelMeta.label} · {summarizeOrderItems(order.items)}
                              {province ? ` · ${province}` : ""}
                            </span>
                          </div>
                        </div>

                        <div className={styles.opRight}>
                          <div className={styles.opFigure}>
                            <span className={styles.opMoney}>{formatVnd(order.subtotal_vnd)}</span>
                            <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          </div>

                          {nextAction ? (
                            <button
                              type="button"
                              className={styles.inlineActionBtn}
                              disabled={busyOrderId === order.id}
                              onClick={() => handleQuickAction(order)}
                            >
                              {busyOrderId === order.id ? "Đang xử lý..." : nextAction.shortLabel}
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
                  })}
                </div>
              )}
            </section>

            <section className={styles.opCard} aria-labelledby="op-stock-heading">
              <div className={common.cardHeader}>
                <h2 id="op-stock-heading" className={common.cardTitle}>
                  Cảnh báo tồn kho theo SKU
                </h2>
                <Link href="/seller/san-pham?filter=low" className={common.cardLink}>
                  <span>Quản lý kho ({formatInt(lowStockAlerts.length)})</span>
                  <IconChevronRight size={13} />
                </Link>
              </div>

              {topLowStock.length === 0 ? (
                <div className={common.inlineEmpty}>
                  Mọi biến thể SKU hiện đều có tồn kho khả dụng trên 5 chiếc.
                </div>
              ) : (
                <div className={styles.opRows}>
                  {topLowStock.map((alert) => (
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
                            {alert.productName} · {alert.skuLabel}
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
                            {alert.availableStock === 0 ? "Hết biến thể" : "Ngưỡng tối thiểu 5"}
                          </span>
                        </div>
                        <Link
                          href={`/seller/san-pham/${alert.productId}`}
                          className={styles.inlineActionBtn}
                        >
                          Nhập tồn
                        </Link>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </section>
          </div>
        </>
      )}
    </div>
  );
}
