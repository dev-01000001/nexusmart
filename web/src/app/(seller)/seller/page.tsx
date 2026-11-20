"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { ORDER_STATUS_META, SHOPS, getSellerWorkspace, getShopById } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconChevronRight, StateView } from "@/components";
import {
  formatCompactVnd,
  formatInt,
  formatSignedInt,
  formatSignedVnd,
  formatVnd,
} from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerTrendChart } from "@/features/seller/SellerTrendChart";
import {
  applySellerOrderStatusOverride,
  computeShopLowStockAlerts,
  formatDeltaPct,
  getNextSellerTransition,
  getShopLedgerWithSession,
  getShopProductsWithSession,
  getUnifiedSellerOrders,
  parseUiState,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type MetricTabKey = "netRevenue" | "orders" | "avgOrder";

const PRIORITY_STATUS_RANK: Record<string, number> = {
  cho_xac_nhan: 0,
  dang_chuan_bi: 1,
  yeu_cau_tra_hang: 2,
  dang_giao: 3,
  da_giao: 4,
  da_huy: 5,
};

function SellerOverviewContent() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { activeSellerShopId, orders: storeOrders, updateOrderStatus, showToast } = useStore();
  const session = useSellerSession();

  const [activeTab, setActiveTab] = useState<MetricTabKey>("netRevenue");

  const activeShop = getShopById(activeSellerShopId) ?? SHOPS[0];
  const workspace = getSellerWorkspace(activeShop.id);
  const shopProducts = useMemo(
    () => getShopProductsWithSession(activeShop.id, session),
    [activeShop.id, session]
  );
  const lowStockAlerts = useMemo(() => computeShopLowStockAlerts(shopProducts, 5), [shopProducts]);
  const unifiedOrders = useMemo(
    () => getUnifiedSellerOrders(activeShop.id, storeOrders, session),
    [activeShop.id, storeOrders, session]
  );
  const ledgerSummary = useMemo(
    () => getShopLedgerWithSession(activeShop.id, session),
    [activeShop.id, session]
  );

  const pendingConfirmCount = unifiedOrders.filter((o) => o.status === "cho_xac_nhan").length;
  const packingCount = unifiedOrders.filter((o) => o.status === "dang_chuan_bi").length;
  const actionableCount = pendingConfirmCount + packingCount;

  const priorAvgOrder =
    workspace.prior7dOrders > 0 ? Math.round(workspace.prior7dGmv / workspace.prior7dOrders) : 0;
  const avgOrderDelta = workspace.avgOrderValue - priorAvgOrder;

  const netPct = formatDeltaPct(workspace.total7dNetRevenue, workspace.prior7dNetRevenue);
  const ordersPct = formatDeltaPct(workspace.total7dOrders, workspace.prior7dOrders);
  const aovPct = formatDeltaPct(workspace.avgOrderValue, priorAvgOrder);
  const gmvPct = formatDeltaPct(workspace.total7dGmv, workspace.prior7dGmv);

  const chartConfig = useMemo(() => {
    const labels = workspace.daily7d.map((d) => d.label);
    const annIdx = workspace.daily7d.findIndex((d) => Boolean(d.annotation));
    const annotation =
      annIdx >= 0 && workspace.daily7d[annIdx].annotation
        ? { index: annIdx, label: workspace.daily7d[annIdx].annotation! }
        : null;

    if (activeTab === "orders") {
      return {
        labels,
        values: workspace.daily7d.map((d) => d.orders),
        metricName: "Số đơn hoàn tất theo ngày",
        formatValue: (v: number) => `${formatInt(v)} đơn`,
        formatTick: (v: number) => formatInt(v),
        annotation,
      };
    }
    if (activeTab === "avgOrder") {
      return {
        labels,
        values: workspace.daily7d.map((d) => (d.orders > 0 ? Math.round(d.gmv / d.orders) : 0)),
        metricName: "Giá trị đơn trung bình theo ngày",
        formatValue: formatVnd,
        formatTick: formatCompactVnd,
        annotation: null,
      };
    }
    return {
      labels,
      values: workspace.daily7d.map((d) => d.netRevenue),
      metricName: "Doanh thu thuần sau phí 7 ngày gần nhất",
      formatValue: formatVnd,
      formatTick: formatCompactVnd,
      annotation,
    };
  }, [workspace.daily7d, activeTab]);

  // Exactly 4 rows in each paired card so left and right cards always share the exact bottom edge
  const topOrders = useMemo(() => {
    return [...unifiedOrders]
      .sort((a, b) => (PRIORITY_STATUS_RANK[a.status] ?? 9) - (PRIORITY_STATUS_RANK[b.status] ?? 9))
      .slice(0, 4);
  }, [unifiedOrders]);

  const topLowStock = useMemo(() => lowStockAlerts.slice(0, 4), [lowStockAlerts]);

  const handleQuickAdvance = (orderId: string, orderCode: string, storeOrderId?: string) => {
    const target = unifiedOrders.find((o) => o.id === orderId);
    if (!target) return;
    const transition = getNextSellerTransition(target.status);
    if (!transition) return;
    applySellerOrderStatusOverride(orderCode, transition.nextStatus, transition.note);
    if (storeOrderId) {
      updateOrderStatus(storeOrderId, transition.nextStatus, transition.note);
    }
    showToast(`Đơn ${orderCode}: ${transition.label.toLowerCase()} thành công.`);
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

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tổng quan gian hàng</h1>
          <p className={common.finding}>
            {activeShop.name} · {formatInt(pendingConfirmCount)} đơn chờ xác nhận,{" "}
            {formatInt(lowStockAlerts.length)} SKU tồn từ 5 chiếc trở xuống · Kỳ 14/11 - 20/11
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
              pendingConfirmCount > 0 ? "/seller/don-hang?status=cho_xac_nhan" : "/seller/don-hang"
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

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải tổng quan gian hàng"
          message="Kết nối đến dịch vụ đối soát đơn hàng tạm gián đoạn."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Tải lại"
          actionHref="/seller"
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

      {!uiState && (
        <>
          {/* Row 1: Balances strip split by 1px vertical dividers */}
          <section className={common.balanceStrip} aria-label="Số dư và hàng chờ vận hành">
            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Số dư khả dụng</span>
              <span
                className={`${common.balanceValue} ${ledgerSummary.availableBalance === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(ledgerSummary.availableBalance)}
              </span>
              {ledgerSummary.availableBalance > 0 ? (
                <Link href="/seller/tai-chinh" className={common.balanceActionLink}>
                  Rút về ngân hàng
                </Link>
              ) : (
                <span className={common.balanceCaption}>Hoàn tất đối soát để rút tiền</span>
              )}
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Chờ đối soát</span>
              <span
                className={`${common.balanceValue} ${ledgerSummary.pendingBalance === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(ledgerSummary.pendingBalance)}
              </span>
              <span className={common.balanceCaption}>Giữ 3 ngày sau giao thành công</span>
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
                  : "Đã xử lý toàn bộ đơn hôm nay"}
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
                  ? "Tồn kho từ 5 chiếc trở xuống"
                  : "Mọi biến thể đều đủ tồn kho"}
              </span>
            </div>
          </section>

          {/* Row 2: Main KPI-tabbed trend card + 3-row comparison table */}
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
                  <span className={styles.kpiTabValue}>
                    {formatVnd(workspace.total7dNetRevenue)}
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
                <span className={styles.kpiTabLabel}>Số đơn hoàn tất</span>
                <span className={styles.kpiTabRow}>
                  <span className={styles.kpiTabValue}>{formatInt(workspace.total7dOrders)}</span>
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
                  <span className={styles.kpiTabValue}>{formatVnd(workspace.avgOrderValue)}</span>
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
                annotation={chartConfig.annotation}
                desktopHeight={194}
                mobileHeight={180}
              />

              <div className={styles.compareWrap}>
                <table className={styles.compareTable}>
                  <thead>
                    <tr>
                      <th>So với tuần trước</th>
                      <th className={common.numCol}>Tuần trước</th>
                      <th className={common.numCol}>7 ngày này</th>
                      <th className={common.numCol}>Chênh lệch</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>Tổng giá trị đơn hàng (GMV)</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(workspace.prior7dGmv)}
                      </td>
                      <td className={common.numCol}>{formatVnd(workspace.total7dGmv)}</td>
                      <td className={`${common.numCol} ${deltaClass(gmvPct.direction)}`}>
                        {formatSignedVnd(workspace.gmvDelta)} ({gmvPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Doanh thu thuần sau phí sàn</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatVnd(workspace.prior7dNetRevenue)}
                      </td>
                      <td className={common.numCol}>{formatVnd(workspace.total7dNetRevenue)}</td>
                      <td className={`${common.numCol} ${deltaClass(netPct.direction)}`}>
                        {formatSignedVnd(workspace.netDelta)} ({netPct.label})
                      </td>
                    </tr>
                    <tr>
                      <td>Số đơn hàng và giá trị trung bình</td>
                      <td className={`${common.numCol} ${styles.comparePrior}`}>
                        {formatInt(workspace.prior7dOrders)} đơn
                      </td>
                      <td className={common.numCol}>
                        {formatInt(workspace.total7dOrders)} đơn (
                        {formatVnd(workspace.avgOrderValue)})
                      </td>
                      <td className={`${common.numCol} ${deltaClass(ordersPct.direction)}`}>
                        {formatSignedInt(workspace.ordersDelta)} đơn (
                        {formatSignedVnd(avgOrderDelta)})
                      </td>
                    </tr>
                  </tbody>
                </table>

                {/* Distinct 2-line phone comparison list (zero right-edge clipping at 390px) */}
                <div className={styles.compareMobile}>
                  <div className={styles.compareMobileHead}>So với tuần trước</div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Tổng giá trị đơn hàng (GMV)</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatVnd(workspace.total7dGmv)}
                      </span>
                      <span className={deltaClass(gmvPct.direction)}>
                        {formatSignedVnd(workspace.gmvDelta)} ({gmvPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Doanh thu thuần sau phí sàn</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatVnd(workspace.total7dNetRevenue)}
                      </span>
                      <span className={deltaClass(netPct.direction)}>
                        {formatSignedVnd(workspace.netDelta)} ({netPct.label})
                      </span>
                    </div>
                  </div>
                  <div className={styles.compareMobileRow}>
                    <span className={styles.compareMobileLabel}>Số đơn và giá trị trung bình</span>
                    <div className={styles.compareMobileFigs}>
                      <span className={styles.compareMobileVal}>
                        {formatInt(workspace.total7dOrders)} đơn (
                        {formatVnd(workspace.avgOrderValue)})
                      </span>
                      <span className={deltaClass(ordersPct.direction)}>
                        {formatSignedInt(workspace.ordersDelta)} đơn ({ordersPct.label})
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </section>

          {/* Row 3: Paired 2-column operational cards — balanced 4 rows each */}
          <div className={styles.pairedGrid}>
            <section className={styles.opCard} aria-labelledby="op-orders-heading">
              <div className={common.cardHeader}>
                <h2 id="op-orders-heading" className={common.cardTitle}>
                  Đơn hàng cần xử lý
                </h2>
                <Link href="/seller/don-hang" className={common.cardLink}>
                  <span>Xem tất cả ({formatInt(unifiedOrders.length)})</span>
                  <IconChevronRight size={13} />
                </Link>
              </div>

              <div className={styles.opRows}>
                {topOrders.map((order) => {
                  const meta = ORDER_STATUS_META[order.status];
                  const nextAction = getNextSellerTransition(order.status);
                  return (
                    <div key={order.id} className={styles.opRow}>
                      <div className={styles.opRowMain}>
                        <div className={styles.opInfo}>
                          <Link href={`/seller/don-hang/${order.id}`} className={styles.opPrimary}>
                            {order.orderCode} · {order.buyerName}
                          </Link>
                          <span className={styles.opSecondary}>
                            {order.itemSummary} · {order.provinceName}
                          </span>
                        </div>
                      </div>

                      <div className={styles.opRight}>
                        <div className={styles.opFigure}>
                          <span className={styles.opMoney}>{formatVnd(order.subtotal)}</span>
                          <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                        </div>

                        {nextAction ? (
                          <button
                            type="button"
                            className={styles.inlineActionBtn}
                            onClick={() =>
                              handleQuickAdvance(order.id, order.orderCode, order.storeOrderId)
                            }
                          >
                            {nextAction.shortLabel}
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

              <div className={styles.opRows}>
                {topLowStock.map((alert) => (
                  <div key={alert.skuCode} className={styles.opRow}>
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
                          {alert.productName} · {alert.colorName} / {alert.size}
                        </Link>
                        <span className={styles.opSecondary}>
                          {alert.skuCode} · {formatVnd(alert.price)}
                        </span>
                      </div>
                    </div>

                    <div className={styles.opRight}>
                      <div className={styles.opFigure}>
                        <span
                          className={`${styles.opMoney} ${alert.stock === 0 ? styles.opStockCrit : ""}`}
                        >
                          {alert.stock === 0 ? "0 chiếc" : `Còn ${formatInt(alert.stock)}`}
                        </span>
                        <span className={styles.opSubFigure}>
                          {alert.stock === 0 ? "Hết biến thể" : "Ngưỡng tối thiểu 5"}
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
            </section>
          </div>
        </>
      )}
    </div>
  );
}

export default function SellerOverviewPage() {
  return (
    <SellerSuspense>
      <SellerOverviewContent />
    </SellerSuspense>
  );
}
