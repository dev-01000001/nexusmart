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
import { api, apiBlob, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import {
  buildVnPastDays,
  formatBpsPercent,
  formatCompactVnd,
  formatDayMonthVi,
  formatInt,
  formatSignedVnd,
  formatVnDateOffset,
  formatVnd,
  getVnDateParts,
  validateReportDateRange,
} from "@/lib/format";
import type {
  GatewayOrder,
  GatewayPlatformReport,
  GatewayPlatformReportResponse,
  GatewayReturnRequest,
} from "@/lib/gateway-types";
import {
  getSalesChannelMeta,
  normalizeSalesChannel,
  type SalesChannelFilter,
} from "@/lib/salesChannel";
import { formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { CountUpValue } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import {
  getAccountTypeLabelVi,
  getAdminOrderStatusMeta,
  type GwAdminOrder,
  type GwAdminProductExtended,
  type GwAdminShop,
  type GwAuditLog,
  type GwLedgerSummary,
} from "./adminApiTypes";
import { getAdminAuditTargetTypeLabel } from "./types";
import { getAdminAuditActionLabelVi } from "./AdminAuditHelpers";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/page.module.css";

type QueueCategory = "all" | "shop" | "product" | "return" | "needs-review" | "ledger";
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
  pendingProducts?: GwAdminProductExtended[];
  escalatedReturns?: GatewayReturnRequest[];
  orders: GwAdminOrder[];
  needsReviewOrders?: GatewayOrder[];
  needsReviewTotal?: number;
  ledger: GwLedgerSummary | null;
  auditLogs: GwAuditLog[];
  initialReport?: GatewayPlatformReport | null;
  initialReportFrom?: string;
  initialReportTo?: string;
  loadError: string | null;
  serverNowMs?: number;
}

function AdminOverviewContent({
  shops,
  pendingProducts = [],
  escalatedReturns = [],
  orders,
  needsReviewOrders = [],
  needsReviewTotal = 0,
  ledger,
  auditLogs,
  initialReport = null,
  initialReportFrom,
  initialReportTo,
  loadError,
  serverNowMs,
}: AdminOverviewClientProps) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canShops = roleHasCapability(user.role, "shops.read");
  const canProducts = roleHasCapability(user.role, "products.read");
  const canReturns = roleHasCapability(user.role, "returns.read");
  const canOrders = roleHasCapability(user.role, "orders.read");
  const canLedger = roleHasCapability(user.role, "ledger.read");
  const canAudit = roleHasCapability(user.role, "audit_logs.read");
  const canReports = roleHasCapability(user.role, "reports.read");

  const [queueFilter, setQueueFilter] = useState<QueueCategory>("all");
  const [healthMetric, setHealthMetric] = useState<HealthMetric>("gmv");
  const [orderChannelFilter, setOrderChannelFilter] = useState<SalesChannelFilter>("");
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());

  const [report, setReport] = useState<GatewayPlatformReport | null>(initialReport);
  const [reportFrom, setReportFrom] = useState<string>(
    () => initialReportFrom ?? formatVnDateOffset(-29, anchorNowMs)
  );
  const [reportTo, setReportTo] = useState<string>(
    () => initialReportTo ?? formatVnDateOffset(0, anchorNowMs)
  );
  const [reportLoading, setReportLoading] = useState(false);
  const [reportExporting, setReportExporting] = useState(false);
  const [reportError, setReportError] = useState<string | null>(null);

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
    () => report?.gmv_vnd ?? validOrders.reduce((s, o) => s + o.total_payable_vnd, 0),
    [report, validOrders]
  );
  const totalCommission = useMemo(
    () => report?.commission_vnd ?? validOrders.reduce((s, o) => s + o.commission_vnd, 0),
    [report, validOrders]
  );

  const dailySeries = useMemo<DailyPoint[]>(() => {
    if (report && report.daily && report.daily.length > 0) {
      return report.daily.slice(-14).map((d) => ({
        label: formatDayMonthVi(`${d.date}T00:00:00+07:00`),
        gmv: d.gmv_vnd,
        orderCount: d.order_count,
      }));
    }
    return buildDailySeries(validOrders, anchorNowMs);
  }, [report, validOrders, anchorNowMs]);

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
    if (canLedger && !isBalanced) {
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
    if (canOrders) {
      for (const ord of needsReviewOrders) {
        const shopLabel = shopNameById.get(ord.shop_id) ?? `Shop ${ord.shop_id.slice(0, 8)}`;
        list.push({
          id: `review-${ord.id}`,
          category: "needs-review",
          categoryLabel: "Cần rà soát",
          title: `Đơn ${ord.order_code}`,
          meta: `${shopLabel}, ${formatVnd(ord.total_payable_vnd)}`,
          href: "/admin/doi-soat?tab=needs-review",
          cta: "Rà soát",
          urgent: true,
        });
      }
    }
    if (canReturns) {
      for (const ret of escalatedReturns) {
        const shopLabel = shopNameById.get(ret.shop_id) ?? `Shop ${ret.shop_id.slice(0, 8)}`;
        list.push({
          id: `ret-${ret.id}`,
          category: "return",
          categoryLabel: "Khiếu nại",
          title: `Đơn ${ret.order_code || ret.order_id.slice(0, 8)} (${shopLabel})`,
          meta:
            ret.refund_amount_vnd > 0
              ? `Hoàn dự kiến ${formatVnd(ret.refund_amount_vnd)}`
              : ret.reason || "Đang khiếu nại",
          href: `/admin/khieu-nai?selected=${encodeURIComponent(ret.id)}`,
          cta: "Phân xử",
          urgent: true,
        });
      }
    }
    if (canShops) {
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
    }
    if (canProducts) {
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
    }
    return list;
  }, [
    canLedger,
    canOrders,
    canReturns,
    canShops,
    canProducts,
    isBalanced,
    ledgerDiff,
    ledger,
    needsReviewOrders,
    escalatedReturns,
    pendingShops,
    pendingProducts,
    shopNameById,
  ]);

  const visibleQueue = useMemo(
    () =>
      queueFilter === "all"
        ? actionQueue
        : actionQueue.filter((item) => item.category === queueFilter),
    [actionQueue, queueFilter]
  );

  const recentAuditLogs = useMemo(() => auditLogs.slice(0, 6), [auditLogs]);

  const handleLoadReport = async (fromDate: string, toDate: string) => {
    if (!canReports || reportLoading) return;
    const check = validateReportDateRange(fromDate, toDate);
    if (!check.ok) {
      setReportError(check.message);
      return;
    }
    setReportError(null);
    setReportLoading(true);
    try {
      const qs = new URLSearchParams({
        from_date: fromDate,
        to_date: toDate,
        top_limit: "5",
      });
      const res = await api<GatewayPlatformReportResponse>(`/api/admin/reports?${qs.toString()}`);
      setReport(res.report ?? null);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể tải báo cáo nền tảng lúc này.");
      setReportError(msg);
      showToast(msg);
    } finally {
      setReportLoading(false);
    }
  };

  const handleApplyPreset = (days: number) => {
    const nextTo = formatVnDateOffset(0, anchorNowMs);
    const nextFrom = formatVnDateOffset(-(days - 1), anchorNowMs);
    setReportFrom(nextFrom);
    setReportTo(nextTo);
    void handleLoadReport(nextFrom, nextTo);
  };

  const handleExportCsv = async () => {
    if (!canReports || reportExporting) return;
    const check = validateReportDateRange(reportFrom, reportTo);
    if (!check.ok) {
      setReportError(check.message);
      return;
    }
    setReportError(null);
    setReportExporting(true);
    try {
      const qs = new URLSearchParams({
        from_date: reportFrom,
        to_date: reportTo,
      });
      const blob = await apiBlob(`/api/admin/reports/export?${qs.toString()}`);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `nexusmart-report-${reportFrom}-to-${reportTo}.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      showToast("Đã xuất tệp CSV báo cáo nền tảng.");
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xuất tệp CSV báo cáo lúc này."));
    } finally {
      setReportExporting(false);
    }
  };

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
    ...(canOrders
      ? [{ id: "needs-review" as const, label: "Cần rà soát", count: needsReviewOrders.length }]
      : []),
    ...(canReturns
      ? [{ id: "return" as const, label: "Khiếu nại", count: escalatedReturns.length }]
      : []),
    ...(canShops ? [{ id: "shop" as const, label: "Gian hàng", count: pendingShops.length }] : []),
    ...(canProducts
      ? [{ id: "product" as const, label: "Sản phẩm", count: pendingProducts.length }]
      : []),
    ...(canLedger ? [{ id: "ledger" as const, label: "Đối soát", count: isBalanced ? 0 : 1 }] : []),
  ];

  const queueIcon = (cat: ActionQueueItem["category"]) => {
    switch (cat) {
      case "shop":
        return <IconStore size={15} />;
      case "product":
        return <IconShield size={15} />;
      case "return":
      case "needs-review":
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
          {canReports && (
            <Button
              variant="outline"
              size="sm"
              shape="app"
              loading={reportExporting}
              onClick={() => void handleExportCsv()}
            >
              Xuất CSV
            </Button>
          )}
          {canShops && (
            <Button variant="primary" size="sm" shape="app" href="/admin/gian-hang">
              Duyệt gian hàng
            </Button>
          )}
        </div>
      </header>

      {/* Compact 4-cell KPI Strip */}
      <section className={shared.kpiStrip} aria-label="Chỉ số vận hành chính">
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Hàng đợi cần xử lý</span>
          <span
            className={`${shared.kpiValue} ${actionQueue.length > 0 ? shared.kpiValueNegative : shared.kpiValuePositive}`}
          >
            <CountUpValue value={actionQueue.length} format="int" />
          </span>
          <span className={shared.kpiCaption}>
            {canOrders && needsReviewTotal > 0
              ? `${formatInt(needsReviewTotal)} đơn cần rà soát`
              : `${formatInt(activeShopsCount)} gian hàng hoạt động`}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>{report ? "GMV kỳ báo cáo" : "GMV đơn hàng"}</span>
          <span className={shared.kpiValue}>
            <CountUpValue value={totalGmv} format="vnd" />
          </span>
          <span className={shared.kpiCaption}>
            {report
              ? `${formatInt(report.total_orders)} đơn · hoàn tất ${formatInt(report.completed_orders)}`
              : `${formatInt(validOrders.length)} đơn hợp lệ`}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Hoa hồng nền tảng</span>
          <span className={shared.kpiValue}>
            <CountUpValue value={totalCommission} format="vnd" />
          </span>
          <span className={shared.kpiCaption}>
            {report
              ? `Trợ giá ${formatVnd(report.platform_subsidy_vnd)}`
              : `${formatInt(channelCounts.storefront)} đơn site riêng`}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Sổ cái Nợ và Có</span>
          <span
            className={`${shared.kpiValue} ${!isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive}`}
          >
            {canLedger ? (isBalanced ? "0₫ lệch" : formatSignedVnd(ledgerDiff)) : "—"}
          </span>
          <span className={shared.kpiCaption}>
            {canLedger
              ? isBalanced
                ? "Cân đối tuyệt đối"
                : "Cần đối soát ngay"
              : "Không thuộc phạm vi quyền"}
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
              Hàng đợi cần xử lý
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
                  <span>Các nhóm còn lại hiện không có mục tồn đọng.</span>
                </div>
              )}
            </div>
          ) : (
            <div className={styles.queueClearBanner}>
              <IconCheck size={16} />
              <span>Không còn mục tồn đọng cần xử lý.</span>
            </div>
          )}

          <div className={styles.queueSummaryGrid}>
            {canShops && (
              <Link href="/admin/gian-hang" className={styles.queueSummaryTile}>
                <span className={styles.queueSummaryLabel}>
                  <IconStore size={15} />
                  <span>Gian hàng</span>
                </span>
                <strong className={styles.queueSummaryVal}>
                  {formatInt(pendingShops.length)} chờ duyệt, {formatInt(activeShopsCount)} hoạt
                  động
                </strong>
              </Link>
            )}

            {canProducts && (
              <Link href="/admin/kiem-duyet" className={styles.queueSummaryTile}>
                <span className={styles.queueSummaryLabel}>
                  <IconShield size={15} />
                  <span>Sản phẩm</span>
                </span>
                <strong className={styles.queueSummaryVal}>
                  {formatInt(pendingProducts.length)} chờ duyệt
                </strong>
              </Link>
            )}

            {canReturns && (
              <Link href="/admin/khieu-nai" className={styles.queueSummaryTile}>
                <span className={styles.queueSummaryLabel}>
                  <IconFlag size={15} />
                  <span>Khiếu nại trả hàng</span>
                </span>
                <strong className={styles.queueSummaryVal}>
                  {formatInt(escalatedReturns.length)} cần phân xử
                </strong>
              </Link>
            )}

            {canOrders && (
              <Link href="/admin/doi-soat?tab=needs-review" className={styles.queueSummaryTile}>
                <span className={styles.queueSummaryLabel}>
                  <IconWallet size={15} />
                  <span>Đơn cần rà soát</span>
                </span>
                <strong className={styles.queueSummaryVal}>
                  {formatInt(needsReviewTotal)} đơn
                </strong>
              </Link>
            )}
          </div>
        </section>

        <section className={shared.panel} aria-labelledby="admin-health-heading">
          <div className={shared.panelHeader}>
            <h2 id="admin-health-heading" className={shared.panelTitle}>
              {healthMetric === "gmv" ? "Xu hướng GMV" : "Xu hướng số đơn"}
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
              ariaLabel="Biểu đồ GMV toàn sàn"
            />
          ) : (
            <BarChart
              labels={dailySeries.map((p) => p.label)}
              values={dailySeries.map((p) => p.orderCount)}
              height={224}
              formatValue={(v) => `${formatInt(v)} đơn`}
              formatTick={(v) => formatInt(v)}
              ariaLabel="Biểu đồ số đơn hàng toàn sàn"
            />
          )}

          {canLedger && (
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
          )}
        </section>
      </div>

      {/* Platform Report (/admin/reports + CSV Export) */}
      {canReports && (
        <section className={shared.panel} aria-labelledby="admin-report-heading">
          <div className={shared.panelHeader}>
            <h2 id="admin-report-heading" className={shared.panelTitle}>
              Báo cáo nền tảng
            </h2>
            <div className={styles.chipRow}>
              <button
                type="button"
                className={styles.filterChip}
                onClick={() => handleApplyPreset(7)}
              >
                7 ngày
              </button>
              <button
                type="button"
                className={styles.filterChip}
                onClick={() => handleApplyPreset(30)}
              >
                30 ngày
              </button>
              <button
                type="button"
                className={styles.filterChip}
                onClick={() => handleApplyPreset(90)}
              >
                90 ngày
              </button>
            </div>
          </div>

          <div className={styles.reportControls}>
            <div className={styles.reportDateGroup}>
              <label htmlFor="report-from" className="sr-only">
                Từ ngày
              </label>
              <input
                id="report-from"
                type="date"
                className={shared.toolbarDate}
                value={reportFrom}
                onChange={(e) => setReportFrom(e.target.value)}
                aria-label="Ngày bắt đầu báo cáo"
              />
              <span className={shared.panelMeta}>đến</span>
              <label htmlFor="report-to" className="sr-only">
                Đến ngày
              </label>
              <input
                id="report-to"
                type="date"
                className={shared.toolbarDate}
                value={reportTo}
                onChange={(e) => setReportTo(e.target.value)}
                aria-label="Ngày kết thúc báo cáo"
              />
              <Button
                variant="outline"
                size="sm"
                shape="app"
                loading={reportLoading}
                onClick={() => void handleLoadReport(reportFrom, reportTo)}
              >
                Cập nhật
              </Button>
            </div>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              loading={reportExporting}
              onClick={() => void handleExportCsv()}
            >
              Xuất CSV
            </Button>
          </div>

          {reportError && (
            <p className={shared.validationError} role="alert">
              {reportError}
            </p>
          )}

          {report ? (
            <>
              <div className={styles.reportMetricsGrid}>
                <div className={styles.reportMetricCard}>
                  <span className={styles.reportMetricLabel}>Doanh thu ròng</span>
                  <span className={styles.reportMetricValue}>
                    {formatVnd(report.net_revenue_vnd)}
                  </span>
                  <span className={styles.reportMetricSub}>GMV {formatVnd(report.gmv_vnd)}</span>
                </div>
                <div className={styles.reportMetricCard}>
                  <span className={styles.reportMetricLabel}>Hoa hồng &amp; phí TT</span>
                  <span className={styles.reportMetricValue}>
                    {formatVnd(report.commission_vnd)}
                  </span>
                  <span className={styles.reportMetricSub}>
                    Phí TT {formatVnd(report.payment_fee_vnd)}
                  </span>
                </div>
                <div className={styles.reportMetricCard}>
                  <span className={styles.reportMetricLabel}>Tỉ lệ hủy / trả hàng</span>
                  <span className={styles.reportMetricValue}>
                    {formatBpsPercent(report.cancel_rate_bps)} /{" "}
                    {formatBpsPercent(report.return_rate_bps)}
                  </span>
                  <span className={styles.reportMetricSub}>
                    {formatInt(report.cancelled_orders)} hủy · {formatInt(report.returned_orders)}{" "}
                    trả
                  </span>
                </div>
                <div className={styles.reportMetricCard}>
                  <span className={styles.reportMetricLabel}>Tăng trưởng kỳ báo cáo</span>
                  <span className={styles.reportMetricValue}>
                    {report.user_stats
                      ? `+${formatInt(report.user_stats.new_users)} người dùng`
                      : `${formatInt(report.completed_orders)} đơn hoàn tất`}
                  </span>
                  <span className={styles.reportMetricSub}>
                    {report.user_stats
                      ? `+${formatInt(report.user_stats.new_shops)} gian hàng mới`
                      : `Tổng ${formatInt(report.total_orders)} đơn`}
                  </span>
                </div>
              </div>

              {(report.top_products?.length ?? 0) > 0 && (
                <div className={`${shared.tableWrap} ${shared.tableWrapAuto}`}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Top sản phẩm bán chạy</th>
                        <th className={shared.colNum}>Số lượng bán</th>
                        <th className={shared.colNum}>Doanh thu</th>
                      </tr>
                    </thead>
                    <tbody>
                      {report.top_products.map((tp) => (
                        <tr key={tp.product_id}>
                          <td className={shared.cellPrimary}>{tp.product_name}</td>
                          <td className={shared.colNum}>{formatInt(tp.units_sold)}</td>
                          <td className={shared.colNum}>{formatVnd(tp.revenue_vnd)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          ) : (
            <p className={shared.emptyRow}>Chưa có dữ liệu báo cáo trong khoảng ngày đã chọn.</p>
          )}
        </section>
      )}

      {/* Recent Orders */}
      {canOrders && (
        <section className={shared.panel} aria-labelledby="admin-orders-heading">
          <div className={shared.panelHeader}>
            <h2 id="admin-orders-heading" className={shared.panelTitle}>
              Đơn hàng gần đây
            </h2>
            <div className={styles.panelControls}>
              <span className={shared.panelMeta}>
                {formatInt(channelCounts.marketplace)} đơn sàn,{" "}
                {formatInt(channelCounts.storefront)} site riêng
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
                <option value="STOREFRONT">
                  Site riêng ({formatInt(channelCounts.storefront)})
                </option>
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
      )}

      {/* Bottom Operational Pair: Ledger Summary + Audit Feed */}
      {(canLedger || canAudit) && (
        <div className={styles.bottomGrid}>
          {canLedger && (
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
                          <td
                            className={`${shared.colNum} ${!isBalanced ? shared.numNegative : ""}`}
                          >
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
                            Nợ {formatVnd(acc.debit_total_vnd)} / Có{" "}
                            {formatVnd(acc.credit_total_vnd)}
                          </span>
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </section>
          )}

          {canAudit && (
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
                        <span className={styles.auditAction}>
                          {getAdminAuditActionLabelVi(log.action)}
                        </span>
                        <span className={styles.auditTime}>
                          {log.created_at ? formatShortTimeDateVi(log.created_at) : ""}
                        </span>
                      </div>
                      <span className={styles.auditSub}>
                        {getAdminAuditTargetTypeLabel(log.target_type)} {log.target_id.slice(0, 10)}
                        {log.reason ? `: ${log.reason}` : ""}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}
        </div>
      )}
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
