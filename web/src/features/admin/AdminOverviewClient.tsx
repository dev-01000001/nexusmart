"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  BarChart,
  Button,
  IconCheck,
  IconFlag,
  IconShield,
  IconStore,
  IconWallet,
  LineChart,
  StateView,
} from "@/components";
import {
  buildVnPastDays,
  formatCompactVnd,
  formatInt,
  formatSignedVnd,
  formatVnd,
  getVnDateParts,
} from "@/lib/format";
import {
  getSalesChannelMeta,
  normalizeSalesChannel,
  type SalesChannelFilter,
} from "@/lib/salesChannel";
import { formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { CountUpValue } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import type { AdminComplaint } from "./adminComplaintsTypes";
import {
  getAccountTypeLabelVi,
  getAdminOrderStatusMeta,
  getAuditActionLabelVi,
  getAuditTargetLabelVi,
  type GwAdminOrder,
  type GwAdminProduct,
  type GwAdminShop,
  type GwAuditLog,
  type GwLedgerSummary,
} from "./adminApiTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/page.module.css";

type QueueCategory = "all" | "shop" | "product" | "complaint" | "ledger";
type HealthMetric = "gmv" | "orders";

interface ActionQueueItem {
  id: string;
  category: Exclude<QueueCategory, "all">;
  categoryLabel: string;
  title: string;
  meta: string;
  href: string;
  cta: string;
  urgent?: boolean;
}

interface DailyPoint {
  label: string;
  gmv: number;
  orderCount: number;
}

function buildDailySeries(orders: GwAdminOrder[], nowMs: number): DailyPoint[] {
  const points: Array<DailyPoint & { key: string }> = [];
  const byKey = new Map<string, DailyPoint & { key: string }>();
  for (const day of buildVnPastDays(7, nowMs)) {
    const pt = { key: day.key, label: day.label, gmv: 0, orderCount: 0 };
    points.push(pt);
    byKey.set(day.key, pt);
  }

  for (const o of orders) {
    if (o.status.toUpperCase() === "CANCELLED" || !o.created_at) continue;
    const parts = getVnDateParts(o.created_at);
    if (!parts) continue;
    const pt = byKey.get(parts.dateKey);
    if (pt) {
      pt.gmv += o.total_payable_vnd;
      pt.orderCount += 1;
    }
  }
  return points;
}

export interface AdminOverviewClientProps {
  shops: GwAdminShop[];
  pendingProducts?: GwAdminProduct[];
  openComplaints?: AdminComplaint[];
  orders: GwAdminOrder[];
  ledger: GwLedgerSummary | null;
  auditLogs: GwAuditLog[];
  loadError: string | null;
  serverNowMs?: number;
}

function AdminOverviewContent({
  shops,
  pendingProducts = [],
  openComplaints = [],
  orders,
  ledger,
  auditLogs,
  loadError,
  serverNowMs,
}: AdminOverviewClientProps) {
  const router = useRouter();
  const [queueFilter, setQueueFilter] = useState<QueueCategory>("all");
  const [healthMetric, setHealthMetric] = useState<HealthMetric>("gmv");
  const [orderChannelFilter, setOrderChannelFilter] = useState<SalesChannelFilter>("");
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());

  const shopNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const sh of shops) map.set(sh.id, sh.name);
    return map;
  }, [shops]);

  const filteredRecentOrders = useMemo(() => {
    const list = orderChannelFilter
      ? orders.filter((o) => normalizeSalesChannel(o.sales_channel) === orderChannelFilter)
      : orders;
    return list.slice(0, 10);
  }, [orders, orderChannelFilter]);

  const channelCounts = useMemo(() => {
    let marketplace = 0;
    let storefront = 0;
    for (const o of orders) {
      if (normalizeSalesChannel(o.sales_channel) === "STOREFRONT") storefront += 1;
      else marketplace += 1;
    }
    return { marketplace, storefront };
  }, [orders]);

  const validOrders = useMemo(
    () => orders.filter((o) => o.status.toUpperCase() !== "CANCELLED"),
    [orders]
  );
  const totalGmv = useMemo(
    () => validOrders.reduce((s, o) => s + o.total_payable_vnd, 0),
    [validOrders]
  );
  const totalCommission = useMemo(
    () => validOrders.reduce((s, o) => s + o.commission_vnd, 0),
    [validOrders]
  );
  const dailySeries = useMemo(
    () => buildDailySeries(validOrders, anchorNowMs),
    [validOrders, anchorNowMs]
  );

  const pendingShops = useMemo(
    () => shops.filter((s) => s.status.toUpperCase() === "PENDING"),
    [shops]
  );
  const activeShopsCount = useMemo(
    () => shops.filter((s) => s.status.toUpperCase() === "ACTIVE").length,
    [shops]
  );

  const ledgerDiff = ledger ? ledger.total_debits_vnd - ledger.total_credits_vnd : 0;
  const isBalanced = ledger ? ledger.is_balanced && ledgerDiff === 0 : true;

  const actionQueue = useMemo<ActionQueueItem[]>(() => {
    const list: ActionQueueItem[] = [];
    if (!isBalanced) {
      list.push({
        id: "ledger-unbalanced",
        category: "ledger",
        categoryLabel: "Đối soát lệch",
        title: `Sổ cái ghi kép lệch ${formatSignedVnd(ledgerDiff)}`,
        meta: `Nợ ${formatVnd(ledger?.total_debits_vnd ?? 0)} / Có ${formatVnd(ledger?.total_credits_vnd ?? 0)}`,
        href: "/admin/doi-soat",
        cta: "Đối soát ngay",
        urgent: true,
      });
    }
    for (const shop of pendingShops) {
      list.push({
        id: `shop-${shop.id}`,
        category: "shop",
        categoryLabel: "Gian hàng",
        title: shop.name,
        meta: [
          shop.contact_phone || shop.id.slice(0, 8),
          shop.created_at ? formatShortTimeDateVi(shop.created_at) : "",
        ]
          .filter(Boolean)
          .join(", "),
        href: `/admin/gian-hang?selected=${encodeURIComponent(shop.id)}`,
        cta: "Duyệt hồ sơ",
      });
    }
    for (const prod of pendingProducts) {
      const shopLabel = shopNameById.get(prod.shop_id) ?? `Shop ${prod.shop_id.slice(0, 8)}`;
      list.push({
        id: `prod-${prod.id}`,
        category: "product",
        categoryLabel: "Sản phẩm",
        title: prod.name,
        meta: `${shopLabel}, ${formatVnd(prod.min_price_vnd)}`,
        href: `/admin/kiem-duyet?selected=${encodeURIComponent(prod.id)}`,
        cta: "Kiểm duyệt",
      });
    }
    for (const cmp of openComplaints) {
      const resolvedShopName = shopNameById.get(cmp.shop_id) ?? cmp.shop_name;
      list.push({
        id: `cmp-${cmp.id}`,
        category: "complaint",
        categoryLabel: "Khiếu nại",
        title: `${cmp.complaint_code} (${resolvedShopName})`,
        meta:
          cmp.dispute_amount_vnd > 0
            ? `Đơn ${cmp.order_code}, tranh chấp ${formatVnd(cmp.dispute_amount_vnd)}`
            : `Đơn ${cmp.order_code}`,
        href: `/admin/khieu-nai?selected=${encodeURIComponent(cmp.id)}`,
        cta: "Phân xử",
        urgent: true,
      });
    }
    return list;
  }, [isBalanced, ledgerDiff, ledger, pendingShops, pendingProducts, openComplaints, shopNameById]);

  const visibleQueue = useMemo(
    () =>
      queueFilter === "all"
        ? actionQueue
        : actionQueue.filter((item) => item.category === queueFilter),
    [actionQueue, queueFilter]
  );

  const recentAuditLogs = useMemo(() => auditLogs.slice(0, 6), [auditLogs]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải số liệu tổng quan"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const queueChips: Array<{ id: QueueCategory; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: actionQueue.length },
    { id: "shop", label: "Gian hàng", count: pendingShops.length },
    { id: "product", label: "Sản phẩm", count: pendingProducts.length },
    { id: "complaint", label: "Khiếu nại", count: openComplaints.length },
    { id: "ledger", label: "Đối soát", count: isBalanced ? 0 : 1 },
  ];

  const queueIcon = (cat: ActionQueueItem["category"]) => {
    switch (cat) {
      case "shop":
        return <IconStore size={15} />;
      case "product":
        return <IconShield size={15} />;
      case "complaint":
        return <IconFlag size={15} />;
      case "ledger":
        return <IconWallet size={15} />;
    }
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Tổng quan</h1>
        </div>
        <div className={shared.titleActions}>
          <Button variant="primary" size="sm" shape="app" href="/admin/gian-hang">
            Duyệt gian hàng
          </Button>
        </div>
      </header>

      {/* Compact 4-cell KPI Strip */}
      <section className={shared.kpiStrip} aria-label="Chỉ số vận hành chính">
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Hàng đợi cần duyệt</span>
          <span
            className={`${shared.kpiValue} ${actionQueue.length > 0 ? shared.kpiValueNegative : shared.kpiValuePositive}`}
          >
            <CountUpValue value={actionQueue.length} format="int" />
          </span>
          <span className={shared.kpiCaption}>
            {formatInt(activeShopsCount)} gian hàng hoạt động
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>GMV đơn hàng</span>
          <span className={shared.kpiValue}>
            <CountUpValue value={totalGmv} format="vnd" />
          </span>
          <span className={shared.kpiCaption}>{formatInt(validOrders.length)} đơn hợp lệ</span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Phí nền tảng</span>
          <span className={shared.kpiValue}>
            <CountUpValue value={totalCommission} format="vnd" />
          </span>
          <span className={shared.kpiCaption}>
            {formatInt(channelCounts.storefront)} đơn site riêng
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Sổ cái Nợ và Có</span>
          <span
            className={`${shared.kpiValue} ${!isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive}`}
          >
            {isBalanced ? "0₫ lệch" : formatSignedVnd(ledgerDiff)}
          </span>
          <span className={shared.kpiCaption}>
            {isBalanced ? "Cân đối tuyệt đối" : "Cần đối soát ngay"}
          </span>
        </div>
      </section>

      {/* Primary Hero: Left Actionable Pending Queue + Right Single-Question Health Chart */}
      <div className={styles.heroGrid}>
        <section
          className={`${shared.panel} ${styles.queuePanel}`}
          aria-labelledby="admin-queue-heading"
        >
          <div className={shared.panelHeader}>
            <h2 id="admin-queue-heading" className={shared.panelTitle}>
              Hàng đợi cần duyệt
            </h2>
            <div className={styles.chipRow} role="tablist" aria-label="Lọc nhanh hàng đợi">
              {queueChips.map((chip) => {
                const active = queueFilter === chip.id;
                return (
                  <button
                    key={chip.id}
                    type="button"
                    role="tab"
                    aria-selected={active}
                    className={`${styles.filterChip} ${active ? styles.filterChipActive : ""}`}
                    onClick={() => setQueueFilter(chip.id)}
                  >
                    <span>{chip.label}</span>
                    <span className={styles.filterChipCount}>{chip.count}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {visibleQueue.length > 0 ? (
            <div className={styles.queueBody}>
              <ul className={styles.queueList}>
                {visibleQueue.slice(0, 8).map((item, idx) => (
                  <li
                    key={item.id}
                    className={styles.queueRow}
                    style={{ "--i": idx } as React.CSSProperties}
                  >
                    <div className={styles.queueRowLeft}>
                      <span
                        className={`${styles.queueTypeTag} ${item.urgent ? styles.queueTypeTagUrgent : ""}`}
                      >
                        {queueIcon(item.category)}
                        <span>{item.categoryLabel}</span>
                      </span>
                      <div className={styles.queueText}>
                        <span className={styles.queueTitle}>{item.title}</span>
                        <span className={styles.queueMeta}>{item.meta}</span>
                      </div>
                    </div>
                    <Link href={item.href} className={shared.rowActionBtn}>
                      {item.cta}
                    </Link>
                  </li>
                ))}
              </ul>
              {visibleQueue.length <= 2 && (
                <div className={styles.queueCalmNote}>
                  <IconCheck size={15} />
                  <span>
                    {queueFilter === "all"
                      ? "Các nhóm còn lại hiện không có mục tồn đọng."
                      : "Không còn mục chờ nào khác trong nhóm này."}
                  </span>
                </div>
              )}
            </div>
          ) : (
            <div className={styles.queueClearBanner}>
              <IconCheck size={16} />
              <span>
                {queueFilter === "all"
                  ? "Không còn mục tồn đọng cần duyệt."
                  : "Không có mục chờ trong nhóm này."}
              </span>
            </div>
          )}

          <div className={styles.queueSummaryGrid}>
            <Link href="/admin/gian-hang" className={styles.queueSummaryTile}>
              <span className={styles.queueSummaryLabel}>
                <IconStore size={15} />
                <span>Gian hàng</span>
              </span>
              <strong className={styles.queueSummaryVal}>
                {formatInt(pendingShops.length)} chờ duyệt, {formatInt(activeShopsCount)} hoạt động
              </strong>
            </Link>

            <Link href="/admin/kiem-duyet" className={styles.queueSummaryTile}>
              <span className={styles.queueSummaryLabel}>
                <IconShield size={15} />
                <span>Sản phẩm</span>
              </span>
              <strong className={styles.queueSummaryVal}>
                {formatInt(pendingProducts.length)} chờ duyệt
              </strong>
            </Link>

            <Link href="/admin/khieu-nai" className={styles.queueSummaryTile}>
              <span className={styles.queueSummaryLabel}>
                <IconFlag size={15} />
                <span>Khiếu nại</span>
              </span>
              <strong className={styles.queueSummaryVal}>
                {formatInt(openComplaints.length)} đang mở
              </strong>
            </Link>

            <Link href="/admin/doi-soat" className={styles.queueSummaryTile}>
              <span className={styles.queueSummaryLabel}>
                <IconWallet size={15} />
                <span>Đối soát</span>
              </span>
              <strong className={styles.queueSummaryVal}>
                {isBalanced ? "Khớp 0₫" : formatSignedVnd(ledgerDiff)}
              </strong>
            </Link>
          </div>
        </section>

        <section className={shared.panel} aria-labelledby="admin-health-heading">
          <div className={shared.panelHeader}>
            <h2 id="admin-health-heading" className={shared.panelTitle}>
              {healthMetric === "gmv" ? "GMV 7 ngày qua" : "Số đơn 7 ngày qua"}
            </h2>
            <div className={styles.segmentSwitch} role="tablist" aria-label="Chọn chỉ số biểu đồ">
              <button
                type="button"
                role="tab"
                aria-selected={healthMetric === "gmv"}
                className={`${styles.segmentBtn} ${healthMetric === "gmv" ? styles.segmentBtnActive : ""}`}
                onClick={() => setHealthMetric("gmv")}
              >
                GMV
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={healthMetric === "orders"}
                className={`${styles.segmentBtn} ${healthMetric === "orders" ? styles.segmentBtnActive : ""}`}
                onClick={() => setHealthMetric("orders")}
              >
                Số đơn
              </button>
            </div>
          </div>

          {healthMetric === "gmv" ? (
            <LineChart
              labels={dailySeries.map((p) => p.label)}
              series={[
                {
                  name: "GMV ghi nhận",
                  values: dailySeries.map((p) => p.gmv),
                  color: "var(--chart-1)",
                },
              ]}
              height={224}
              showArea
              formatValue={formatVnd}
              formatTick={formatCompactVnd}
              ariaLabel="Biểu đồ GMV toàn sàn 7 ngày gần nhất"
            />
          ) : (
            <BarChart
              labels={dailySeries.map((p) => p.label)}
              values={dailySeries.map((p) => p.orderCount)}
              height={224}
              formatValue={(v) => `${formatInt(v)} đơn`}
              formatTick={(v) => formatInt(v)}
              ariaLabel="Biểu đồ số đơn hàng 7 ngày gần nhất"
            />
          )}

          <div className={styles.healthFooter}>
            <div className={styles.healthStat}>
              <span className={styles.healthStatLabel}>Ghi có sổ cái</span>
              <span className={`${styles.healthStatVal} num`}>
                {formatVnd(ledger?.total_credits_vnd ?? 0)}
              </span>
            </div>
            <div className={styles.healthStat}>
              <span className={styles.healthStatLabel}>Ghi nợ sổ cái</span>
              <span className={`${styles.healthStatVal} num`}>
                {formatVnd(ledger?.total_debits_vnd ?? 0)}
              </span>
            </div>
            <Link href="/admin/doi-soat" className={shared.panelLink}>
              Mở đối soát
            </Link>
          </div>
        </section>
      </div>

      {/* Recent Orders */}
      <section className={shared.panel} aria-labelledby="admin-orders-heading">
        <div className={shared.panelHeader}>
          <h2 id="admin-orders-heading" className={shared.panelTitle}>
            Đơn hàng gần đây
          </h2>
          <div className={styles.panelControls}>
            <span className={shared.panelMeta}>
              {formatInt(channelCounts.marketplace)} đơn sàn, {formatInt(channelCounts.storefront)}{" "}
              site riêng
            </span>
            <select
              className={shared.toolbarSelect}
              value={orderChannelFilter}
              onChange={(e) => setOrderChannelFilter(e.target.value as SalesChannelFilter)}
              aria-label="Lọc đơn hàng theo kênh bán"
            >
              <option value="">Tất cả kênh ({formatInt(orders.length)})</option>
              <option value="MARKETPLACE">
                Sàn NexusMart ({formatInt(channelCounts.marketplace)})
              </option>
              <option value="STOREFRONT">Site riêng ({formatInt(channelCounts.storefront)})</option>
            </select>
          </div>
        </div>

        {filteredRecentOrders.length === 0 ? (
          <p className={shared.emptyRow}>Chưa có đơn hàng thuộc kênh đang chọn.</p>
        ) : (
          <>
            <div className={`${shared.tableWrap} ${shared.tableWrapAuto}`}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Mã đơn</th>
                    <th>Gian hàng</th>
                    <th>Kênh bán</th>
                    <th className={shared.colNum}>Tổng tiền</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredRecentOrders.map((order) => {
                    const statusMeta = getAdminOrderStatusMeta(order.status);
                    const channelMeta = getSalesChannelMeta(order.sales_channel);
                    const shopName = shopNameById.get(order.shop_id) ?? order.shop_id.slice(0, 8);
                    return (
                      <tr key={order.id}>
                        <td>
                          <div className={shared.cellPrimary}>{order.order_code}</div>
                          {order.created_at && (
                            <div className={shared.cellSub}>
                              {formatShortTimeDateVi(order.created_at)}
                            </div>
                          )}
                        </td>
                        <td>{shopName}</td>
                        <td>
                          <span
                            className={`${shared.channelBadge} ${channelMeta.isStorefront ? shared.channelBadgeStorefront : ""}`}
                            data-sales-channel={channelMeta.value}
                          >
                            {channelMeta.label}
                          </span>
                        </td>
                        <td className={shared.colNum}>{formatVnd(order.total_payable_vnd)}</td>
                        <td>
                          <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {filteredRecentOrders.map((order) => {
                const statusMeta = getAdminOrderStatusMeta(order.status);
                const channelMeta = getSalesChannelMeta(order.sales_channel);
                const shopName = shopNameById.get(order.shop_id) ?? order.shop_id.slice(0, 8);
                return (
                  <div key={order.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{order.order_code}</span>
                      <span className={shared.mobileMetric}>
                        {formatVnd(order.total_payable_vnd)}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>{shopName}</span>
                      <div className={styles.badgeCluster}>
                        <span
                          className={`${shared.channelBadge} ${channelMeta.isStorefront ? shared.channelBadgeStorefront : ""}`}
                          data-sales-channel={channelMeta.value}
                        >
                          {channelMeta.label}
                        </span>
                        <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </section>

      {/* Bottom Operational Pair: Ledger Summary + Audit Feed */}
      <div className={styles.bottomGrid}>
        <section className={shared.panel} aria-labelledby="channel-recon-heading">
          <div className={shared.panelHeader}>
            <h2 id="channel-recon-heading" className={shared.panelTitle}>
              Tài khoản sổ cái
            </h2>
            <Link href="/admin/doi-soat" className={shared.panelLink}>
              Xem tất cả ({formatInt(ledger?.accounts.length ?? 0)})
            </Link>
          </div>

          {!ledger || ledger.accounts.length === 0 ? (
            <p className={shared.emptyRow}>Chưa phát sinh bút toán trên sổ cái.</p>
          ) : (
            <>
              <div className={`${shared.tableWrap} ${shared.tableWrapAuto}`}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Tài khoản</th>
                      <th className={shared.colNum}>Ghi nợ</th>
                      <th className={shared.colNum}>Ghi có</th>
                      <th className={shared.colNum}>Số dư</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ledger.accounts.slice(0, 6).map((acc) => (
                      <tr key={acc.account_code}>
                        <td>
                          <div className={shared.cellPrimary}>{acc.account_code}</div>
                          <div className={shared.cellSub}>
                            {getAccountTypeLabelVi(acc.account_type)}
                          </div>
                        </td>
                        <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                        <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                        <td className={shared.colNum}>{formatVnd(acc.balance_vnd)}</td>
                      </tr>
                    ))}
                  </tbody>
                  <tfoot>
                    <tr>
                      <td>Tổng sổ cái</td>
                      <td className={shared.colNum}>{formatVnd(ledger.total_debits_vnd)}</td>
                      <td className={shared.colNum}>{formatVnd(ledger.total_credits_vnd)}</td>
                      <td className={`${shared.colNum} ${!isBalanced ? shared.numNegative : ""}`}>
                        {isBalanced ? "Khớp 0₫" : formatSignedVnd(ledgerDiff)}
                      </td>
                    </tr>
                  </tfoot>
                </table>
              </div>

              <div className={shared.mobileList}>
                {ledger.accounts.slice(0, 5).map((acc) => (
                  <div key={acc.account_code} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{acc.account_code}</span>
                      <span className={shared.mobileMetric}>{formatVnd(acc.balance_vnd)}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {getAccountTypeLabelVi(acc.account_type)}
                      </span>
                      <span>
                        Nợ {formatVnd(acc.debit_total_vnd)} / Có {formatVnd(acc.credit_total_vnd)}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </>
          )}
        </section>

        <section className={shared.panel} aria-labelledby="rail-audit-heading">
          <div className={shared.panelHeader}>
            <h2 id="rail-audit-heading" className={shared.panelTitle}>
              Nhật ký gần đây
            </h2>
            <Link href="/admin/audit-log" className={shared.panelLink}>
              Xem tất cả ({formatInt(auditLogs.length)})
            </Link>
          </div>

          {recentAuditLogs.length === 0 ? (
            <p className={shared.emptyRow}>Chưa có bản ghi kiểm toán.</p>
          ) : (
            <ul className={styles.auditFeed}>
              {recentAuditLogs.map((log) => (
                <li key={log.id} className={styles.auditRow}>
                  <div className={styles.auditTopLine}>
                    <span className={styles.auditAction}>{getAuditActionLabelVi(log.action)}</span>
                    <span className={styles.auditTime}>
                      {log.created_at ? formatShortTimeDateVi(log.created_at) : ""}
                    </span>
                  </div>
                  <span className={styles.auditSub}>
                    {getAuditTargetLabelVi(log.target_type)} {log.target_id.slice(0, 10)}
                    {log.reason ? `: ${log.reason}` : ""}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}

export function AdminOverviewClient(props: AdminOverviewClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Tổng quan" />}>
      <AdminStateGate
        title="Tổng quan"
        emptyTitle="Chưa có dữ liệu tổng quan"
        emptyMessage="Hệ thống chưa ghi nhận giao dịch hoặc gian hàng nào."
        errorTitle="Không thể tải số liệu tổng quan"
        errorMessage="Dịch vụ đối soát tạm gián đoạn. Thử tải lại."
        loadingRows={5}
      >
        {() => <AdminOverviewContent {...props} />}
      </AdminStateGate>
    </Suspense>
  );
}
