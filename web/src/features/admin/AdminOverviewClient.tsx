"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { CheckCircle2, Download, Flag, ShieldCheck, Store, Wallet } from "lucide-react";
import {
  BarChart,
  ErrorState,
  IndexRowCard,
  KpiChartCard,
  LineChart,
  MetricStrip,
  Money,
  PageHeader,
  StatusBadge,
  orderStatusTone,
} from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
import { cn } from "@/lib/utils";
import { formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
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

type QueueCategory = "all" | "shop" | "product" | "return" | "needs-review" | "ledger";

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
  const [orderChannelFilter, setOrderChannelFilter] = useState<SalesChannelFilter>("");
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());

  const [report, setReport] = useState<GatewayPlatformReport | null>(initialReport);
  const [reportFrom, setReportFrom] = useState<string>(
    () => initialReportFrom ?? formatVnDateOffset(-29, anchorNowMs)
  );
  const [reportTo, setReportTo] = useState<string>(
    () => initialReportTo ?? formatVnDateOffset(0, anchorNowMs)
  );
  const [activePreset, setActivePreset] = useState<number | null>(30);
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
    setActivePreset(days);
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
      <div className="space-y-5">
        <PageHeader title="Tổng quan" />
        <ErrorState
          title="Không thể tải số liệu tổng quan"
          description={loadError}
          onRetry={() => router.refresh()}
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
        return Store;
      case "product":
        return ShieldCheck;
      case "return":
      case "needs-review":
        return Flag;
      case "ledger":
        return Wallet;
    }
  };

  const rangeFooter =
    dailySeries.length > 0
      ? `${dailySeries[0].label} – ${dailySeries[dailySeries.length - 1].label}`
      : `${reportFrom} – ${reportTo}`;

  const orderBarSeries = dailySeries.slice(-7);
  const orderBarRange =
    orderBarSeries.length > 0
      ? `${orderBarSeries[0].label} – ${orderBarSeries[orderBarSeries.length - 1].label}`
      : rangeFooter;

  const totalOrdersCount = report ? report.total_orders : validOrders.length;

  const orderChannelLabel =
    orderChannelFilter === "MARKETPLACE"
      ? `Sàn NexusMart (${formatInt(channelCounts.marketplace)})`
      : orderChannelFilter === "STOREFRONT"
        ? `Site riêng (${formatInt(channelCounts.storefront)})`
        : `Tất cả kênh (${formatInt(orders.length)})`;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Tổng quan"
        subtitle="Theo dõi vận hành nền tảng, hàng đợi cần xử lý và báo cáo theo kỳ."
        secondaryActions={
          canReports ? (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              disabled={reportExporting}
              onClick={() => void handleExportCsv()}
            >
              <Download className="size-4" aria-hidden="true" />
              <span>{reportExporting ? "Đang xuất..." : "Xuất CSV"}</span>
            </Button>
          ) : undefined
        }
        primaryAction={
          canShops ? (
            <Button size="sm" asChild>
              <Link href="/admin/gian-hang">Duyệt gian hàng</Link>
            </Button>
          ) : undefined
        }
      />

      {/* 4-cell KPI Strip */}
      <section aria-label="Chỉ số vận hành chính">
        <MetricStrip
          className="md:grid-flow-row md:grid-cols-2 md:divide-x-0 lg:grid-flow-col lg:auto-cols-fr lg:divide-x"
          items={[
            {
              label: "Hàng đợi cần xử lý",
              value: (
                <span
                  className={actionQueue.length > 0 ? "text-destructive" : "text-badge-success-ink"}
                >
                  {formatInt(actionQueue.length)}
                </span>
              ),
              hint:
                canOrders && needsReviewTotal > 0
                  ? `${formatInt(needsReviewTotal)} đơn cần rà soát`
                  : `${formatInt(activeShopsCount)} gian hàng hoạt động`,
            },
            {
              label: report ? "GMV kỳ báo cáo" : "GMV đơn hàng",
              value: formatVnd(totalGmv),
              hint: report
                ? `${formatInt(report.total_orders)} đơn · hoàn tất ${formatInt(report.completed_orders)}`
                : `${formatInt(validOrders.length)} đơn hợp lệ`,
            },
            {
              label: "Hoa hồng nền tảng",
              value: formatVnd(totalCommission),
              hint: report
                ? `Trợ giá ${formatVnd(report.platform_subsidy_vnd)}`
                : `${formatInt(channelCounts.storefront)} đơn site riêng`,
            },
            {
              label: "Sổ cái Nợ và Có",
              value: (
                <span
                  className={
                    !canLedger
                      ? "text-muted-foreground"
                      : !isBalanced
                        ? "text-destructive"
                        : "text-badge-success-ink"
                  }
                >
                  {canLedger ? (isBalanced ? "0₫ lệch" : formatSignedVnd(ledgerDiff)) : "—"}
                </span>
              ),
              hint: canLedger
                ? isBalanced
                  ? "Cân đối tuyệt đối"
                  : "Cần đối soát ngay"
                : "Không thuộc phạm vi quyền",
            },
          ]}
        />
      </section>

      {/* A4 Period Controls */}
      {canReports && (
        <section aria-labelledby="admin-report-heading" className="space-y-4">
          <Card>
            <CardHeader className="gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <CardTitle id="admin-report-heading">Báo cáo nền tảng</CardTitle>
                <div className="flex flex-wrap items-center gap-1.5">
                  {[7, 30, 90].map((days) => (
                    <Button
                      key={days}
                      type="button"
                      size="xs"
                      variant={activePreset === days ? "secondary" : "outline"}
                      onClick={() => handleApplyPreset(days)}
                    >
                      {days} ngày
                    </Button>
                  ))}
                </div>
              </div>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-2">
                  <label htmlFor="report-from" className="sr-only">
                    Từ ngày
                  </label>
                  <Input
                    id="report-from"
                    type="date"
                    className="h-9 w-40 bg-card tabular-nums"
                    value={reportFrom}
                    onChange={(e) => {
                      setActivePreset(null);
                      setReportFrom(e.target.value);
                    }}
                    aria-label="Ngày bắt đầu báo cáo"
                  />
                  <span className="text-[13px] text-muted-foreground">đến</span>
                  <label htmlFor="report-to" className="sr-only">
                    Đến ngày
                  </label>
                  <Input
                    id="report-to"
                    type="date"
                    className="h-9 w-40 bg-card tabular-nums"
                    value={reportTo}
                    onChange={(e) => {
                      setActivePreset(null);
                      setReportTo(e.target.value);
                    }}
                    aria-label="Ngày kết thúc báo cáo"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={reportLoading}
                    onClick={() => void handleLoadReport(reportFrom, reportTo)}
                  >
                    {reportLoading ? "Đang tải..." : "Cập nhật"}
                  </Button>
                </div>
                <Button
                  type="button"
                  size="sm"
                  disabled={reportExporting}
                  onClick={() => void handleExportCsv()}
                >
                  {reportExporting ? "Đang xuất..." : "Xuất CSV"}
                </Button>
              </div>

              {reportError && (
                <p className="text-[13px] font-medium text-destructive" role="alert">
                  {reportError}
                </p>
              )}

              {report ? (
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                  <div className="rounded-lg border border-border bg-muted/40 p-3.5">
                    <div className="text-[13px] text-muted-foreground">Doanh thu ròng</div>
                    <div className="mt-1 text-lg font-semibold text-foreground tabular-nums">
                      {formatVnd(report.net_revenue_vnd)}
                    </div>
                    <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                      GMV {formatVnd(report.gmv_vnd)}
                    </div>
                  </div>
                  <div className="rounded-lg border border-border bg-muted/40 p-3.5">
                    <div className="text-[13px] text-muted-foreground">Hoa hồng &amp; phí TT</div>
                    <div className="mt-1 text-lg font-semibold text-foreground tabular-nums">
                      {formatVnd(report.commission_vnd)}
                    </div>
                    <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                      Phí TT {formatVnd(report.payment_fee_vnd)}
                    </div>
                  </div>
                  <div className="rounded-lg border border-border bg-muted/40 p-3.5">
                    <div className="text-[13px] text-muted-foreground">Tỉ lệ hủy / trả hàng</div>
                    <div className="mt-1 text-lg font-semibold text-foreground tabular-nums">
                      {formatBpsPercent(report.cancel_rate_bps)} /{" "}
                      {formatBpsPercent(report.return_rate_bps)}
                    </div>
                    <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                      {formatInt(report.cancelled_orders)} hủy · {formatInt(report.returned_orders)}{" "}
                      trả
                    </div>
                  </div>
                  <div className="rounded-lg border border-border bg-muted/40 p-3.5">
                    <div className="text-[13px] text-muted-foreground">Tăng trưởng kỳ báo cáo</div>
                    <div className="mt-1 text-lg font-semibold text-foreground tabular-nums">
                      {report.user_stats
                        ? `+${formatInt(report.user_stats.new_users)} người dùng`
                        : `${formatInt(report.completed_orders)} đơn hoàn tất`}
                    </div>
                    <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                      {report.user_stats
                        ? `+${formatInt(report.user_stats.new_shops)} gian hàng mới`
                        : `Tổng ${formatInt(report.total_orders)} đơn`}
                    </div>
                  </div>
                </div>
              ) : (
                <p className="text-[13px] text-muted-foreground">
                  Chưa có dữ liệu báo cáo trong khoảng ngày đã chọn.
                </p>
              )}
            </CardContent>
          </Card>
        </section>
      )}

      {/* A4 KpiChartCard Grid */}
      <section
        aria-labelledby="admin-health-heading"
        className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3"
      >
        <h2 id="admin-health-heading" className="sr-only">
          Xu hướng vận hành nền tảng
        </h2>

        <KpiChartCard
          title="Xu hướng GMV"
          tooltip="Tổng giá trị đơn hàng hợp lệ ghi nhận theo ngày trong kỳ đang chọn."
          value={formatVnd(totalGmv)}
          chart={
            <LineChart
              labels={dailySeries.map((p) => p.label)}
              series={[
                {
                  name: "GMV ghi nhận",
                  values: dailySeries.map((p) => p.gmv),
                },
              ]}
              height={188}
              showArea
              formatValue={formatVnd}
              formatTick={formatCompactVnd}
              ariaLabel="Biểu đồ GMV toàn sàn"
            />
          }
          footer={<span>{rangeFooter}</span>}
        />

        <KpiChartCard
          title="Xu hướng số đơn"
          tooltip="Số lượng đơn hàng hợp lệ phát sinh mỗi ngày (7 ngày gần nhất trong kỳ)."
          value={formatInt(totalOrdersCount)}
          chart={
            <BarChart
              labels={orderBarSeries.map((p) => p.label)}
              values={orderBarSeries.map((p) => p.orderCount)}
              height={188}
              formatValue={(v) => `${formatInt(v)} đơn`}
              formatTick={(v) => formatInt(v)}
              ariaLabel="Biểu đồ số đơn hàng toàn sàn"
            />
          }
          footer={
            <span>
              {report
                ? `${orderBarRange} · Hoàn tất ${formatInt(report.completed_orders)} · Hủy ${formatInt(report.cancelled_orders)}`
                : orderBarRange}
            </span>
          }
        />

        <Card className="gap-3 md:col-span-2 xl:col-span-1">
          <CardHeader className="gap-1">
            <CardTitle className="text-[13px] font-medium">Tóm tắt sổ cái &amp; phân hệ</CardTitle>
            <div className="text-2xl leading-8 font-semibold tracking-tight text-foreground tabular-nums">
              {canLedger
                ? isBalanced
                  ? "Khớp 0₫"
                  : formatSignedVnd(ledgerDiff)
                : `${formatInt(activeShopsCount)} gian hàng`}
            </div>
          </CardHeader>
          <CardContent className="space-y-2.5 pt-0 text-[13px]">
            {canLedger && (
              <>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-muted-foreground">Ghi có sổ cái</span>
                  <span className="font-medium text-foreground tabular-nums">
                    {formatVnd(ledger?.total_credits_vnd ?? 0)}
                  </span>
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-muted-foreground">Ghi nợ sổ cái</span>
                  <span className="font-medium text-foreground tabular-nums">
                    {formatVnd(ledger?.total_debits_vnd ?? 0)}
                  </span>
                </div>
                <Separator />
              </>
            )}
            <div className="grid grid-cols-2 gap-2 pt-1">
              {canShops && (
                <Link
                  href="/admin/gian-hang"
                  className="rounded-lg border border-border bg-muted/40 p-2.5 transition-colors hover:bg-accent"
                >
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <Store className="size-3.5 shrink-0 text-brand" aria-hidden="true" />
                    <span>Gian hàng</span>
                  </div>
                  <div className="mt-1 text-xs font-semibold text-foreground tabular-nums">
                    {formatInt(pendingShops.length)} chờ · {formatInt(activeShopsCount)} chạy
                  </div>
                </Link>
              )}
              {canProducts && (
                <Link
                  href="/admin/kiem-duyet"
                  className="rounded-lg border border-border bg-muted/40 p-2.5 transition-colors hover:bg-accent"
                >
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <ShieldCheck className="size-3.5 shrink-0 text-brand" aria-hidden="true" />
                    <span>Sản phẩm</span>
                  </div>
                  <div className="mt-1 text-xs font-semibold text-foreground tabular-nums">
                    {formatInt(pendingProducts.length)} chờ duyệt
                  </div>
                </Link>
              )}
              {canReturns && (
                <Link
                  href="/admin/khieu-nai"
                  className="rounded-lg border border-border bg-muted/40 p-2.5 transition-colors hover:bg-accent"
                >
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <Flag className="size-3.5 shrink-0 text-brand" aria-hidden="true" />
                    <span>Khiếu nại trả hàng</span>
                  </div>
                  <div className="mt-1 text-xs font-semibold text-foreground tabular-nums">
                    {formatInt(escalatedReturns.length)} cần phân xử
                  </div>
                </Link>
              )}
              {canOrders && (
                <Link
                  href="/admin/doi-soat?tab=needs-review"
                  className="rounded-lg border border-border bg-muted/40 p-2.5 transition-colors hover:bg-accent"
                >
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <Wallet className="size-3.5 shrink-0 text-brand" aria-hidden="true" />
                    <span>Đơn cần rà soát</span>
                  </div>
                  <div className="mt-1 text-xs font-semibold text-foreground tabular-nums">
                    {formatInt(needsReviewTotal)} đơn
                  </div>
                </Link>
              )}
            </div>
            {canLedger && (
              <div className="pt-1">
                <Link
                  href="/admin/doi-soat"
                  className="text-xs font-medium text-brand hover:underline"
                >
                  Mở đối soát →
                </Link>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* Actionable Pending Queue */}
      <Card>
        <CardHeader className="gap-3 border-b border-border pb-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <CardTitle id="admin-queue-heading">Hàng đợi cần xử lý</CardTitle>
            <div
              role="tablist"
              aria-label="Lọc nhanh hàng đợi"
              className="flex flex-wrap items-center gap-1"
            >
              {queueChips.map((chip) => {
                const active = queueFilter === chip.id;
                return (
                  <button
                    key={chip.id}
                    type="button"
                    role="tab"
                    aria-selected={active}
                    onClick={() => setQueueFilter(chip.id)}
                    className={cn(
                      "inline-flex min-h-8 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      active
                        ? "bg-secondary font-semibold text-foreground"
                        : "text-muted-foreground hover:bg-secondary/60 hover:text-foreground"
                    )}
                  >
                    <span>{chip.label}</span>
                    <span className="tabular-nums opacity-75">{chip.count}</span>
                  </button>
                );
              })}
            </div>
          </div>
        </CardHeader>
        <CardContent className="pt-4">
          {visibleQueue.length > 0 ? (
            <div className="space-y-3">
              <ul className="divide-y divide-border" aria-labelledby="admin-queue-heading">
                {visibleQueue.slice(0, 8).map((item) => {
                  const Icon = queueIcon(item.category);
                  return (
                    <li
                      key={item.id}
                      className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between"
                    >
                      <div className="flex min-w-0 flex-1 flex-wrap items-start gap-2.5 sm:flex-nowrap sm:items-center">
                        <StatusBadge
                          tone={item.urgent ? "critical" : "attention"}
                          icon={Icon}
                          className="shrink-0"
                        >
                          {item.categoryLabel}
                        </StatusBadge>
                        <div className="min-w-0 flex-1 whitespace-normal [overflow-wrap:anywhere]">
                          <div className="text-sm font-medium text-foreground">{item.title}</div>
                          <div className="text-xs text-muted-foreground">{item.meta}</div>
                        </div>
                      </div>
                      <Button
                        variant="outline"
                        size="xs"
                        asChild
                        className="shrink-0 self-start sm:self-center"
                      >
                        <Link href={item.href}>{item.cta}</Link>
                      </Button>
                    </li>
                  );
                })}
              </ul>
              {visibleQueue.length <= 2 && (
                <div className="flex items-center gap-2 rounded-lg bg-muted/60 px-3 py-2 text-xs text-muted-foreground">
                  <CheckCircle2
                    className="size-4 shrink-0 text-badge-success-ink"
                    aria-hidden="true"
                  />
                  <span>Các nhóm còn lại hiện không có mục tồn đọng.</span>
                </div>
              )}
            </div>
          ) : (
            <div className="flex items-center gap-2 rounded-lg bg-badge-success-bg/60 px-3.5 py-3 text-[13px] font-medium text-badge-success-ink">
              <CheckCircle2 className="size-4 shrink-0" aria-hidden="true" />
              <span>Không còn mục tồn đọng cần xử lý.</span>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Top Products from Platform Report */}
      {canReports && report && (report.top_products?.length ?? 0) > 0 && (
        <Card>
          <CardHeader className="border-b border-border pb-3">
            <CardTitle>Top sản phẩm bán chạy</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <div className="hidden md:block">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Top sản phẩm bán chạy</TableHead>
                    <TableHead className="text-right">Số lượng bán</TableHead>
                    <TableHead className="text-right">Doanh thu</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {report.top_products.map((tp) => (
                    <TableRow key={tp.product_id}>
                      <TableCell className="max-w-[360px] whitespace-normal font-medium text-foreground">
                        {tp.product_name}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {formatInt(tp.units_sold)}
                      </TableCell>
                      <TableCell className="text-right">
                        <Money vnd={tp.revenue_vnd} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="md:hidden">
              {report.top_products.map((tp) => (
                <IndexRowCard
                  key={tp.product_id}
                  title={tp.product_name}
                  badge={<Money vnd={tp.revenue_vnd} size="sm" />}
                  meta={<span>Đã bán {formatInt(tp.units_sold)}</span>}
                />
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Recent Orders */}
      {canOrders && (
        <Card>
          <CardHeader className="gap-3 border-b border-border pb-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <CardTitle id="admin-orders-heading">Đơn hàng gần đây</CardTitle>
                <p className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                  {formatInt(channelCounts.marketplace)} đơn sàn,{" "}
                  {formatInt(channelCounts.storefront)} site riêng
                </p>
              </div>
              <Select
                value={orderChannelFilter || "ALL"}
                onValueChange={(val) =>
                  setOrderChannelFilter((val === "ALL" ? "" : val) as SalesChannelFilter)
                }
              >
                <SelectTrigger className="w-52 bg-card" aria-label="Lọc đơn hàng theo kênh bán">
                  <SelectValue>{orderChannelLabel}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="ALL">Tất cả kênh ({formatInt(orders.length)})</SelectItem>
                  <SelectItem value="MARKETPLACE">
                    Sàn NexusMart ({formatInt(channelCounts.marketplace)})
                  </SelectItem>
                  <SelectItem value="STOREFRONT">
                    Site riêng ({formatInt(channelCounts.storefront)})
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </CardHeader>
          <CardContent className="p-0">
            {filteredRecentOrders.length === 0 ? (
              <p className="px-5 py-8 text-center text-[13px] text-muted-foreground">
                Chưa có đơn hàng thuộc kênh đang chọn.
              </p>
            ) : (
              <>
                <div className="hidden md:block">
                  <Table aria-labelledby="admin-orders-heading">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Mã đơn</TableHead>
                        <TableHead className="hidden lg:table-cell">Gian hàng</TableHead>
                        <TableHead className="hidden lg:table-cell">Kênh bán</TableHead>
                        <TableHead className="text-right">Tổng tiền</TableHead>
                        <TableHead>Trạng thái</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {filteredRecentOrders.map((order) => {
                        const statusMeta = getAdminOrderStatusMeta(order.status);
                        const channelMeta = getSalesChannelMeta(order.sales_channel);
                        const shopName =
                          shopNameById.get(order.shop_id) ?? order.shop_id.slice(0, 8);
                        return (
                          <TableRow key={order.id}>
                            <TableCell>
                              <div className="font-medium text-foreground">{order.order_code}</div>
                              {order.created_at && (
                                <div className="text-xs text-muted-foreground tabular-nums">
                                  {formatShortTimeDateVi(order.created_at)}
                                </div>
                              )}
                              <div className="text-xs text-muted-foreground lg:hidden">
                                {shopName} · {channelMeta.label}
                              </div>
                            </TableCell>
                            <TableCell className="hidden lg:table-cell">{shopName}</TableCell>
                            <TableCell className="hidden lg:table-cell">
                              <Badge
                                variant={channelMeta.isStorefront ? "info" : "neutral"}
                                data-sales-channel={channelMeta.value}
                              >
                                {channelMeta.label}
                              </Badge>
                            </TableCell>
                            <TableCell className="text-right">
                              <Money vnd={order.total_payable_vnd} />
                            </TableCell>
                            <TableCell>
                              <StatusBadge tone={orderStatusTone(order.status)}>
                                {statusMeta.label}
                              </StatusBadge>
                            </TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                <div className="md:hidden">
                  {filteredRecentOrders.map((order) => {
                    const statusMeta = getAdminOrderStatusMeta(order.status);
                    const channelMeta = getSalesChannelMeta(order.sales_channel);
                    const shopName = shopNameById.get(order.shop_id) ?? order.shop_id.slice(0, 8);
                    return (
                      <IndexRowCard
                        key={order.id}
                        title={order.order_code}
                        badge={
                          <StatusBadge tone={orderStatusTone(order.status)}>
                            {statusMeta.label}
                          </StatusBadge>
                        }
                        meta={
                          <>
                            <Money vnd={order.total_payable_vnd} size="sm" />
                            <span>·</span>
                            <span>{shopName}</span>
                            <Badge
                              variant={channelMeta.isStorefront ? "info" : "neutral"}
                              data-sales-channel={channelMeta.value}
                            >
                              {channelMeta.label}
                            </Badge>
                          </>
                        }
                      />
                    );
                  })}
                </div>
              </>
            )}
          </CardContent>
        </Card>
      )}

      {/* Bottom Operational Pair: Ledger Accounts + Audit Feed */}
      {(canLedger || canAudit) && (
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(0,1fr)]">
          {canLedger && (
            <Card>
              <CardHeader className="border-b border-border pb-3">
                <CardTitle id="channel-recon-heading">Tài khoản sổ cái</CardTitle>
                <CardAction>
                  <Link
                    href="/admin/doi-soat"
                    className="text-xs font-medium text-brand hover:underline"
                  >
                    Xem tất cả ({formatInt(ledger?.accounts.length ?? 0)})
                  </Link>
                </CardAction>
              </CardHeader>
              <CardContent className="p-0">
                {!ledger || ledger.accounts.length === 0 ? (
                  <p className="px-5 py-8 text-center text-[13px] text-muted-foreground">
                    Chưa phát sinh bút toán trên sổ cái.
                  </p>
                ) : (
                  <>
                    <div className="hidden md:block">
                      <Table aria-labelledby="channel-recon-heading">
                        <TableHeader>
                          <TableRow>
                            <TableHead className="px-3">Tài khoản</TableHead>
                            <TableHead className="hidden px-3 text-right lg:table-cell">
                              Ghi nợ
                            </TableHead>
                            <TableHead className="hidden px-3 text-right lg:table-cell">
                              Ghi có
                            </TableHead>
                            <TableHead className="px-3 text-right">Số dư</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {ledger.accounts.slice(0, 6).map((acc) => (
                            <TableRow key={acc.account_code}>
                              <TableCell className="px-3">
                                <div className="text-[13px] font-medium text-foreground">
                                  {acc.account_code}
                                </div>
                                <div className="text-xs text-muted-foreground">
                                  {getAccountTypeLabelVi(acc.account_type)}
                                </div>
                              </TableCell>
                              <TableCell className="hidden px-3 text-right text-[13px] tabular-nums lg:table-cell">
                                {formatVnd(acc.debit_total_vnd)}
                              </TableCell>
                              <TableCell className="hidden px-3 text-right text-[13px] tabular-nums lg:table-cell">
                                {formatVnd(acc.credit_total_vnd)}
                              </TableCell>
                              <TableCell className="px-3 text-right text-[13px] font-medium tabular-nums">
                                {formatVnd(acc.balance_vnd)}
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                        <TableFooter>
                          <TableRow>
                            <TableCell className="px-3 font-semibold">Tổng sổ cái</TableCell>
                            <TableCell className="hidden px-3 text-right text-[13px] font-semibold tabular-nums lg:table-cell">
                              {formatVnd(ledger.total_debits_vnd)}
                            </TableCell>
                            <TableCell className="hidden px-3 text-right text-[13px] font-semibold tabular-nums lg:table-cell">
                              {formatVnd(ledger.total_credits_vnd)}
                            </TableCell>
                            <TableCell
                              className={cn(
                                "px-3 text-right text-[13px] font-semibold tabular-nums",
                                !isBalanced ? "text-destructive" : "text-badge-success-ink"
                              )}
                            >
                              {isBalanced ? "Khớp 0₫" : formatSignedVnd(ledgerDiff)}
                            </TableCell>
                          </TableRow>
                        </TableFooter>
                      </Table>
                    </div>

                    <div className="md:hidden">
                      {ledger.accounts.slice(0, 5).map((acc) => (
                        <IndexRowCard
                          key={acc.account_code}
                          title={acc.account_code}
                          badge={<Money vnd={acc.balance_vnd} size="sm" />}
                          meta={
                            <>
                              <span>{getAccountTypeLabelVi(acc.account_type)}</span>
                              <span>·</span>
                              <span className="tabular-nums">
                                Nợ {formatVnd(acc.debit_total_vnd)} / Có{" "}
                                {formatVnd(acc.credit_total_vnd)}
                              </span>
                            </>
                          }
                        />
                      ))}
                    </div>
                  </>
                )}
              </CardContent>
            </Card>
          )}

          {canAudit && (
            <Card>
              <CardHeader className="border-b border-border pb-3">
                <CardTitle id="rail-audit-heading">Nhật ký gần đây</CardTitle>
                <CardAction>
                  <Link
                    href="/admin/audit-log"
                    className="text-xs font-medium text-brand hover:underline"
                  >
                    Xem tất cả ({formatInt(auditLogs.length)})
                  </Link>
                </CardAction>
              </CardHeader>
              <CardContent className="pt-4">
                {recentAuditLogs.length === 0 ? (
                  <p className="py-4 text-center text-[13px] text-muted-foreground">
                    Chưa có bản ghi kiểm toán.
                  </p>
                ) : (
                  <ul className="divide-y divide-border" aria-labelledby="rail-audit-heading">
                    {recentAuditLogs.map((log) => (
                      <li key={log.id} className="space-y-0.5 py-2.5 first:pt-0 last:pb-0">
                        <div className="flex items-baseline justify-between gap-2">
                          <span className="text-sm font-medium text-foreground">
                            {getAdminAuditActionLabelVi(log.action)}
                          </span>
                          <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                            {log.created_at ? formatShortTimeDateVi(log.created_at) : ""}
                          </span>
                        </div>
                        <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
                          {getAdminAuditTargetTypeLabel(log.target_type)}{" "}
                          {log.target_id.slice(0, 10)}
                          {log.reason ? `: ${log.reason}` : ""}
                        </p>
                      </li>
                    ))}
                  </ul>
                )}
              </CardContent>
            </Card>
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
