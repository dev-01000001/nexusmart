"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconChevronRight, IconPackage, IconPlus, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import {
  buildVnPastDays,
  formatCompactVnd,
  formatInt,
  formatSignedInt,
  formatSignedVnd,
  formatVnd,
  getVnDateParts,
} from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { SellerOrderQuickDrawer } from "./SellerOrderQuickDrawer";
import { SellerTrendChart } from "./SellerTrendChart";
import { useSellerContext } from "./SellerShell";
import { SellerSiteStatus } from "./SellerSiteStatus";
import { statusPillClass } from "./SellerUiPrimitives";
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

interface DailyBucket {
  key: string;
  label: string;
  gmv: number;
  netRevenue: number;
  orders: number;
}

function buildDailyBuckets(
  orders: GwSellerOrder[],
  nowMs: number
): {
  daily7d: DailyBucket[];
  total7dGmv: number;
  total7dNet: number;
  total7dOrders: number;
  prior7dGmv: number;
  prior7dNet: number;
  prior7dOrders: number;
} {
  const days = buildVnPastDays(7, nowMs);
  const buckets: DailyBucket[] = [];
  const byKey = new Map<string, DailyBucket>();
  for (const day of days) {
    const b: DailyBucket = { key: day.key, label: day.label, gmv: 0, netRevenue: 0, orders: 0 };
    buckets.push(b);
    byKey.set(day.key, b);
  }

  let prior7dGmv = 0;
  let prior7dNet = 0;
  let prior7dOrders = 0;
  const dayMs = 24 * 3600_000;
  const todayUtcDay = days[days.length - 1]?.dayStartUtcMs ?? 0;
  const startCur = days[0]?.dayStartUtcMs ?? 0;
  const startPrior = todayUtcDay - 13 * dayMs;

  for (const o of orders) {
    if (o.status.toUpperCase() === "CANCELLED" || !o.created_at) continue;
    const parts = getVnDateParts(o.created_at);
    if (!parts) continue;
    const net = computeSellerNetReceivable(o);
    if (parts.dayStartUtcMs >= startCur && parts.dayStartUtcMs <= todayUtcDay) {
      const bucket = byKey.get(parts.dateKey);
      if (bucket) {
        bucket.gmv += o.subtotal_vnd;
        bucket.netRevenue += net;
        bucket.orders += 1;
      }
    } else if (parts.dayStartUtcMs >= startPrior && parts.dayStartUtcMs < startCur) {
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
  balanceError?: string | null;
  loadError: string | null;
  serverNowMs?: number;
}

export function SellerOverviewClient({
  initialOrders,
  initialProducts,
  initialBalance,
  balanceError = null,
  loadError,
  serverNowMs,
}: SellerOverviewClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useSellerContext();

  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<MetricTabKey>("netRevenue");
  const [drawerOrderId, setDrawerOrderId] = useState<string | null>(null);
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());

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
  const returnRequestedCount = orders.filter(
    (o) => o.status.toUpperCase() === "RETURN_REQUESTED"
  ).length;

  const stats = useMemo(() => buildDailyBuckets(orders, anchorNowMs), [orders, anchorNowMs]);
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
        metricName: "Số đơn 7 ngày gần nhất",
        formatValue: (v: number) => `${formatInt(v)} đơn`,
        formatTick: (v: number) => formatInt(v),
      };
    }
    if (activeTab === "avgOrder") {
      return {
        labels,
        values: stats.daily7d.map((d) => (d.orders > 0 ? Math.round(d.gmv / d.orders) : 0)),
        metricName: "Giá trị đơn trung bình",
        formatValue: formatVnd,
        formatTick: formatCompactVnd,
      };
    }
    return {
      labels,
      values: stats.daily7d.map((d) => d.netRevenue),
      metricName: "Doanh thu thuần 7 ngày",
      formatValue: formatVnd,
      formatTick: formatCompactVnd,
    };
  }, [stats.daily7d, activeTab]);

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

  const availableVnd = initialBalance?.available_vnd ?? 0;
  const pendingVnd = initialBalance?.pending_vnd ?? 0;

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
      count: returnRequestedCount,
      href: "/seller/tuong-tac?tab=returns",
      urgent: returnRequestedCount > 0,
    },
    {
      key: "stock",
      label: "Sắp hết tồn",
      count: lowStockAlerts.length,
      href: "/seller/san-pham?filter=low",
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
          title="Chưa có đơn hàng trong 7 ngày qua."
          actionLabel="Thêm sản phẩm"
          actionHref="/seller/san-pham/moi"
        />
      )}

      {!uiState && !loadError && (
        <>
          {/* 1. Today's Action Queue: "Hôm nay cần làm gì?" */}
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
                        (Chờ đối soát <span className="num">{formatVnd(pendingVnd)}</span>)
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

          {/* 2. KPI Tabs driving ONE Large Chart */}
          <section className={common.cardFlush} aria-label="Biểu đồ hiệu suất 7 ngày">
            <div className={styles.kpiTabs} role="tablist" aria-label="Chỉ số 7 ngày">
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "netRevenue"}
                className={`${styles.kpiTab} ${activeTab === "netRevenue" ? styles.kpiTabActive : ""}`}
                onClick={() => setActiveTab("netRevenue")}
              >
                <span className={styles.kpiTabLabel}>Doanh thu thuần</span>
                <span className={styles.kpiTabRow}>
                  <span
                    className={`${styles.kpiTabValue} num`}
                    data-reveal="count"
                    data-count-to={stats.total7dNet}
                    data-count-format="vnd"
                  >
                    {formatVnd(stats.total7dNet)}
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
                <span className={styles.kpiTabLabel}>Số đơn 7 ngày</span>
                <span className={styles.kpiTabRow}>
                  <span
                    className={`${styles.kpiTabValue} num`}
                    data-reveal="count"
                    data-count-to={stats.total7dOrders}
                  >
                    {formatInt(stats.total7dOrders)}
                  </span>
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
                  <span
                    className={`${styles.kpiTabValue} num`}
                    data-reveal="count"
                    data-count-to={avgOrderValue}
                    data-count-format="vnd"
                  >
                    {formatVnd(avgOrderValue)}
                  </span>
                  <span className={`${styles.kpiTabDelta} ${deltaClass(aovPct.direction)}`}>
                    {aovPct.label}
                  </span>
                </span>
              </button>
            </div>

            <div className={styles.chartBody}>
              <SellerTrendChart
                labels={chartConfig.labels}
                values={chartConfig.values}
                metricName={chartConfig.metricName}
                formatValue={chartConfig.formatValue}
                formatTick={chartConfig.formatTick}
                annotation={null}
                desktopHeight={214}
                mobileHeight={180}
              />

              <div className={styles.compareWrap}>
                <table className={styles.compareTable}>
                  <thead>
                    <tr>
                      <th>So với 7 ngày trước</th>
                      <th className={common.numCol}>Kỳ trước</th>
                      <th className={common.numCol}>7 ngày này</th>
                      <th className={common.numCol}>Chênh lệch</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>Tổng tiền hàng (GMV)</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(stats.prior7dGmv)}
                      </td>
                      <td className={common.numCol}>{formatVnd(stats.total7dGmv)}</td>
                      <td className={`${common.numCol} ${deltaClass(gmvPct.direction)}`}>
                        {formatSignedVnd(stats.total7dGmv - stats.prior7dGmv)} ({gmvPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Doanh thu thuần</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(stats.prior7dNet)}
                      </td>
                      <td className={common.numCol}>{formatVnd(stats.total7dNet)}</td>
                      <td className={`${common.numCol} ${deltaClass(netPct.direction)}`}>
                        {formatSignedVnd(stats.total7dNet - stats.prior7dNet)} ({netPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Số đơn &amp; trung bình</td>
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
                    <span className={styles.compareMobileLabel}>Tổng tiền hàng (GMV)</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>{formatVnd(stats.total7dGmv)}</span>
                      <span className={deltaClass(gmvPct.direction)}>
                        {formatSignedVnd(stats.total7dGmv - stats.prior7dGmv)} ({gmvPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Doanh thu thuần</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>{formatVnd(stats.total7dNet)}</span>
                      <span className={deltaClass(netPct.direction)}>
                        {formatSignedVnd(stats.total7dNet - stats.prior7dNet)} ({netPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Số đơn &amp; trung bình</span>
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

          {/* 3. Recent Orders Table */}
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
                        const meta = getSellerOrderStatusMeta(order.status);
                        const channelMeta = getSalesChannelMeta(order.sales_channel);
                        const nextAction = getNextBackendOrderAction(order.status);
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
                    const meta = getSellerOrderStatusMeta(order.status);
                    const nextAction = getNextBackendOrderAction(order.status);
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

          {/* 4. Low-Stock SKU Alert Strip (only shown when relevant, actionable in 1 click) */}
          {topLowStock.length > 0 && (
            <section className={common.card} aria-labelledby="op-stock-heading">
              <div className={common.cardHeader}>
                <h2 id="op-stock-heading" className={common.cardTitle}>
                  SKU sắp hết tồn ({formatInt(lowStockAlerts.length)})
                </h2>
                <Link href="/seller/san-pham?filter=low" className={common.cardLink}>
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

      {/* Slide-over Quick Order Detail Drawer (zero extra API call) */}
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
