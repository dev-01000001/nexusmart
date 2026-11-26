"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ChevronRight, Download, Package, Plus, ShoppingBag, Wallet } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  KpiChartCard,
  LineChart,
  MetricStrip,
  Money,
  PageHeader,
  SetupGuide,
  StatusBadge,
  type SetupGuideStep,
} from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, apiBlob, ApiError, getErrorMessage } from "@/lib/api";
import {
  formatBpsPercent,
  formatCompactVnd,
  formatDayMonthVi,
  formatInt,
  formatVnDateOffset,
  formatVnd,
  validateReportDateRange,
} from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import type { SiteDomainDTO } from "@/lib/site/domains";
import { cn } from "@/lib/utils";
import { deriveSellerSetupSteps } from "./ops/sellerSetup";
import { resolveSellerOrderTone } from "./ops/sellerTone";
import { SellerOrderQuickDrawer } from "./SellerOrderQuickDrawer";
import { SellerTrendChart } from "./SellerTrendChart";
import { useSellerContext } from "./SellerShell";
import { SellerSiteStatus } from "./SellerSiteStatus";
import {
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  summarizeOrderItems,
  type GwSellerBalance,
  type GwSellerBankAccount,
  type GwSellerOrder,
  type GwSellerProduct,
  type GwSellerReport,
} from "./sellerApiTypes";
import { parseUiState, translateSellerShipError } from "./types";

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
  productsTotal?: number;
  productsError?: string | null;
  initialBalance: GwSellerBalance | null;
  balanceError?: string | null;
  initialBankAccount?: GwSellerBankAccount | null;
  bankAccountError?: string | null;
  initialDomains?: SiteDomainDTO[];
  domainsError?: string | null;
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
  productsTotal,
  productsError = null,
  initialBalance,
  balanceError = null,
  initialBankAccount = null,
  bankAccountError = null,
  initialDomains = [],
  domainsError = null,
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
  const { shop, site, showToast } = useSellerContext();

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

  const activePresetDays = useMemo(() => {
    const today = formatVnDateOffset(0);
    if (toDate !== today) return null;
    for (const days of [7, 30, 90] as const) {
      if (fromDate === formatVnDateOffset(-(days - 1))) return days;
    }
    return null;
  }, [fromDate, toDate]);

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
  const dailyLabels = useMemo(
    () => dailyPoints.map((d) => formatShortIsoDay(d.date)),
    [dailyPoints]
  );

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

  const setupSteps = useMemo<SetupGuideStep[]>(() => {
    const descriptors = deriveSellerSetupSteps({
      productsCount: productsTotal ?? initialProducts.length,
      productsError,
      bankAccount: initialBankAccount,
      bankAccountError,
      site,
      domains: initialDomains,
      domainsError,
    });

    return descriptors.map((step) => {
      if (step.id === "product") {
        return {
          ...step,
          primary: (
            <Button size="sm" asChild>
              <Link href="/seller/san-pham/moi">Thêm sản phẩm</Link>
            </Button>
          ),
          secondary: (
            <Button size="sm" variant="ghost" asChild>
              <Link href="/seller/san-pham">Nhập CSV</Link>
            </Button>
          ),
          image: {
            src: "/banners/campaign-everyday.jpg",
            alt: "Thêm sản phẩm lên gian hàng NexusMart",
          },
        };
      }
      if (step.id === "bank") {
        return {
          ...step,
          primary: (
            <Button size="sm" asChild>
              <Link href="/seller/tai-chinh">Thêm tài khoản</Link>
            </Button>
          ),
          image: {
            src: "/banners/vietnam-workshop.jpg",
            alt: "Tài khoản ngân hàng nhận tiền đối soát",
          },
        };
      }
      if (step.id === "site") {
        return {
          ...step,
          primary: (
            <Button size="sm" asChild>
              <Link href="/seller/site">Mở Site Studio</Link>
            </Button>
          ),
          image: {
            src: "/banners/campaign-office.jpg",
            alt: "Chọn mẫu giao diện và xuất bản site bán hàng",
          },
        };
      }
      return {
        ...step,
        primary: (
          <Button size="sm" variant="outline" asChild>
            <Link href="/seller/site/ten-mien">Thêm tên miền</Link>
          </Button>
        ),
        image: {
          src: "/banners/campaign-green-fiber.jpg",
          alt: "Gắn tên miền riêng cho site gian hàng",
        },
      };
    });
  }, [
    productsTotal,
    initialProducts.length,
    productsError,
    initialBankAccount,
    bankAccountError,
    site,
    initialDomains,
    domainsError,
  ]);

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
  const maxTopProductRevenue = useMemo(
    () => Math.max(1, ...(report?.top_products ?? []).map((p) => p.revenue_vnd)),
    [report?.top_products]
  );

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

  const isActiveShop = shop?.status === "ACTIVE";

  return (
    <div className="space-y-5">
      <PageHeader
        title="Tổng quan"
        secondaryActions={
          <Button variant="secondary" size="sm" asChild>
            <Link href="/seller/san-pham/moi">
              <Plus className="size-4" aria-hidden="true" />
              <span>Thêm sản phẩm</span>
            </Link>
          </Button>
        }
        primaryAction={
          <Button size="sm" asChild>
            <Link
              href={
                pendingConfirmCount > 0 ? "/seller/don-hang?status=CONFIRMED" : "/seller/don-hang"
              }
            >
              {pendingConfirmCount > 0
                ? `Xác nhận đơn (${formatInt(pendingConfirmCount)})`
                : "Đơn hàng"}
            </Link>
          </Button>
        }
      />

      {isActiveShop ? (
        <SetupGuide
          title="Hướng dẫn thiết lập"
          steps={setupSteps}
          storageKey="nm.seller.setupGuide.v1"
        />
      ) : null}

      <SellerSiteStatus />

      {uiState === "loading" ? (
        <Card role="status" aria-label="Đang tải tổng quan...">
          <CardContent className="space-y-3 py-5">
            <Skeleton className="h-5 w-44" />
            <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
              <Skeleton className="h-32 w-full" />
              <Skeleton className="h-32 w-full" />
              <Skeleton className="h-32 w-full" />
            </div>
            <Skeleton className="h-48 w-full" />
          </CardContent>
        </Card>
      ) : null}

      {uiState === "error" || (loadError && !uiState) ? (
        <ErrorState
          title="Không thể tải tổng quan"
          description={loadError ?? "Kết nối dịch vụ đơn hàng tạm gián đoạn."}
          onRetry={() => router.refresh()}
        />
      ) : null}

      {uiState === "empty" ? (
        <EmptyState
          icon={ShoppingBag}
          title="Chưa có đơn hàng trong kỳ chọn."
          description="Chia sẻ địa chỉ site gian hàng hoặc thêm sản phẩm mới để bắt đầu nhận đơn đầu tiên."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/san-pham/moi">Thêm sản phẩm</Link>
            </Button>
          }
        />
      ) : null}

      {!uiState && !loadError ? (
        <>
          {/* 1. Today's Action Queue */}
          <section aria-labelledby="today-queue-heading" className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 id="today-queue-heading" className="text-sm font-semibold text-foreground">
                Hôm nay cần làm gì?
              </h2>
              <Link
                href="/seller/tai-chinh"
                className="inline-flex flex-wrap items-baseline gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-[13px] text-muted-foreground shadow-card transition-colors hover:border-brand/50 hover:text-foreground"
              >
                <Wallet className="size-3.5 self-center text-brand" aria-hidden="true" />
                <span>Khả dụng</span>
                {initialBalance ? (
                  <>
                    <strong className="font-semibold text-foreground tabular-nums">
                      {formatVnd(availableVnd)}
                    </strong>
                    {pendingVnd > 0 ? (
                      <span className="text-xs text-muted-foreground tabular-nums">
                        (Tạm giữ {formatVnd(pendingVnd)})
                      </span>
                    ) : null}
                  </>
                ) : (
                  <span className="text-xs text-muted-foreground">
                    {balanceError ? "Đang chờ đồng bộ" : "0₫"}
                  </span>
                )}
              </Link>
            </div>

            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              {todayTasks.map((task) => (
                <Link
                  key={task.key}
                  href={task.href}
                  className={cn(
                    "group flex min-h-[88px] flex-col justify-between gap-2 rounded-xl bg-card p-4 shadow-card transition-colors hover:bg-accent/35 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    task.urgent && "ring-1 ring-brand/25"
                  )}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span
                      className={cn(
                        "text-[13px] font-medium",
                        task.urgent ? "text-foreground" : "text-muted-foreground"
                      )}
                    >
                      {task.label}
                    </span>
                    <ChevronRight
                      className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-brand"
                      aria-hidden="true"
                    />
                  </div>
                  <strong
                    className={cn(
                      "text-2xl leading-8 font-semibold tracking-tight tabular-nums",
                      task.urgent
                        ? "text-brand"
                        : task.count === 0
                          ? "font-medium text-muted-foreground"
                          : "text-foreground"
                    )}
                  >
                    {formatInt(task.count)}
                  </strong>
                </Link>
              ))}
            </div>
          </section>

          {/* 2. Date-Range Report Filter Bar + CSV Export */}
          <Card className="py-0" aria-label="Bộ lọc kỳ báo cáo gian hàng">
            <CardContent className="space-y-2 py-3.5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-2">
                  <div
                    role="group"
                    aria-label="Chọn nhanh kỳ báo cáo"
                    className="inline-flex items-center gap-1 rounded-lg bg-muted p-1"
                  >
                    {(
                      [
                        [7, "7 ngày"],
                        [30, "30 ngày"],
                        [90, "90 ngày"],
                      ] as const
                    ).map(([days, label]) => (
                      <Button
                        key={days}
                        type="button"
                        size="xs"
                        variant={activePresetDays === days ? "default" : "ghost"}
                        onClick={() => applyPresetDays(days)}
                      >
                        {label}
                      </Button>
                    ))}
                  </div>

                  <div className="flex flex-wrap items-center gap-2">
                    <label
                      htmlFor="rep-from"
                      className="inline-flex items-center gap-1.5 text-[13px] text-muted-foreground"
                    >
                      <span>Từ</span>
                      <Input
                        id="rep-from"
                        type="date"
                        value={fromDate}
                        onChange={(e) => setFromDate(e.target.value)}
                        className="h-8 w-auto bg-card text-[13px] tabular-nums"
                      />
                    </label>
                    <label
                      htmlFor="rep-to"
                      className="inline-flex items-center gap-1.5 text-[13px] text-muted-foreground"
                    >
                      <span>Đến</span>
                      <Input
                        id="rep-to"
                        type="date"
                        value={toDate}
                        onChange={(e) => setToDate(e.target.value)}
                        className="h-8 w-auto bg-card text-[13px] tabular-nums"
                      />
                    </label>
                  </div>
                </div>

                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  disabled={exportingCsv}
                  onClick={() => void handleExportCsv()}
                >
                  <Download className="size-4" aria-hidden="true" />
                  <span>{exportingCsv ? "Đang xuất..." : "Xuất CSV"}</span>
                </Button>
              </div>
              {rangeError ? (
                <p className="text-xs font-medium text-destructive" role="alert">
                  {rangeError}
                </p>
              ) : null}
            </CardContent>
          </Card>

          {/* 3. Report KPIs (A4 KpiChartCard Grid) + Detailed Trend Chart + Top Products */}
          {reportLoading ? (
            <Card role="status" aria-label="Đang tổng hợp báo cáo doanh thu...">
              <CardContent className="space-y-3 py-5">
                <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                  <Skeleton className="h-48 w-full" />
                  <Skeleton className="h-48 w-full" />
                  <Skeleton className="h-48 w-full" />
                </div>
              </CardContent>
            </Card>
          ) : reportError ? (
            <ErrorState
              title="Không thể tải báo cáo doanh thu"
              description={reportError}
              onRetry={() => void loadReport(fromDate, toDate)}
            />
          ) : report ? (
            <>
              <section
                aria-label="Tổng hợp báo cáo kỳ chọn"
                className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3"
              >
                <KpiChartCard
                  title="Doanh thu thuần"
                  tooltip="Doanh thu gian hàng thực nhận sau khi trừ chiết khấu gian hàng, hoa hồng sàn và phí thanh toán trong kỳ chọn."
                  value={formatVnd(report.net_revenue_vnd)}
                  chart={
                    dailyPoints.length > 0 ? (
                      <LineChart
                        labels={dailyLabels}
                        series={[
                          {
                            name: "Doanh thu thuần",
                            values: dailyPoints.map((d) => d.net_revenue_vnd),
                          },
                        ]}
                        height={156}
                        showArea
                        formatValue={formatVnd}
                        formatTick={formatCompactVnd}
                        ariaLabel={`Biểu đồ doanh thu thuần từ ${fromDate} đến ${toDate}`}
                      />
                    ) : (
                      <p className="py-8 text-center text-xs text-muted-foreground">
                        Chưa có dữ liệu theo ngày trong kỳ chọn.
                      </p>
                    )
                  }
                  footer={`${formatInt(report.completed_orders)} đơn hoàn tất · ${fromDate} → ${toDate}`}
                />

                <KpiChartCard
                  title="Tổng GMV"
                  tooltip="Tổng giá trị hàng hoá phát sinh trong khoảng ngày đã chọn trước khi trừ phí sàn và thanh toán."
                  value={formatVnd(report.gmv_vnd)}
                  chart={
                    dailyPoints.length > 0 ? (
                      <LineChart
                        labels={dailyLabels}
                        series={[
                          {
                            name: "Tổng GMV",
                            values: dailyPoints.map((d) => d.gmv_vnd),
                          },
                        ]}
                        height={156}
                        showArea
                        formatValue={formatVnd}
                        formatTick={formatCompactVnd}
                        ariaLabel={`Biểu đồ tổng GMV từ ${fromDate} đến ${toDate}`}
                      />
                    ) : (
                      <p className="py-8 text-center text-xs text-muted-foreground">
                        Chưa có dữ liệu theo ngày trong kỳ chọn.
                      </p>
                    )
                  }
                  footer={`${formatInt(report.total_orders)} đơn trong kỳ · ${fromDate} → ${toDate}`}
                />

                <KpiChartCard
                  title="Số đơn hàng"
                  tooltip="Tổng số đơn hàng được tạo tại gian hàng trong khoảng ngày đã chọn."
                  value={formatInt(report.total_orders)}
                  chart={
                    dailyPoints.length > 0 ? (
                      <LineChart
                        labels={dailyLabels}
                        series={[
                          {
                            name: "Số đơn hàng",
                            values: dailyPoints.map((d) => d.order_count),
                          },
                        ]}
                        height={156}
                        formatValue={formatInt}
                        ariaLabel={`Biểu đồ số đơn hàng từ ${fromDate} đến ${toDate}`}
                      />
                    ) : (
                      <p className="py-8 text-center text-xs text-muted-foreground">
                        Chưa có dữ liệu theo ngày trong kỳ chọn.
                      </p>
                    )
                  }
                  footer={`Hoàn tất ${formatInt(report.completed_orders)} · Hủy ${formatInt(report.cancelled_orders)} · Trả ${formatInt(report.returned_orders)}`}
                />
              </section>

              <MetricStrip
                items={[
                  {
                    label: "Phí sàn & thanh toán",
                    value: formatVnd(totalFeesVnd),
                    hint: `Hoa hồng ${formatVnd(report.commission_vnd)} · Thanh toán ${formatVnd(report.payment_fee_vnd)}`,
                  },
                  {
                    label: "Tỷ lệ hủy / trả hàng",
                    value: `${formatBpsPercent(report.cancel_rate_bps)} / ${formatBpsPercent(report.return_rate_bps)}`,
                    hint: `${formatInt(report.cancelled_orders)} hủy · ${formatInt(report.returned_orders)} trả hàng`,
                  },
                ]}
              />

              <Card aria-label="Biểu đồ báo cáo doanh thu">
                <CardHeader className="gap-3 border-b border-border pb-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <CardTitle>{chartConfig.metricName}</CardTitle>
                      <p className="mt-0.5 text-xs text-muted-foreground">Tối đa 366 ngày</p>
                    </div>
                    <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as MetricTabKey)}>
                      <TabsList aria-label="Chỉ số theo ngày">
                        <TabsTrigger value="netRevenue">Doanh thu thuần</TabsTrigger>
                        <TabsTrigger value="gmv">Tổng GMV</TabsTrigger>
                        <TabsTrigger value="orders">Số đơn hàng</TabsTrigger>
                      </TabsList>
                    </Tabs>
                  </div>
                </CardHeader>
                <CardContent className="space-y-4">
                  {dailyPoints.length > 0 ? (
                    <SellerTrendChart
                      ariaLabel={chartConfig.metricName}
                      points={chartConfig.points}
                      valueKind={chartConfig.valueKind}
                      currentLegend={chartConfig.currentLegend}
                    />
                  ) : (
                    <p className="py-6 text-center text-sm text-muted-foreground">
                      Chưa có dữ liệu theo ngày trong khoảng thời gian đã chọn.
                    </p>
                  )}

                  {(report.orders_by_status ?? []).length > 0 ? (
                    <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3">
                      {report.orders_by_status.map((row) => {
                        const meta = getSellerOrderStatusMeta(row.status);
                        return (
                          <StatusBadge key={row.status} tone={resolveSellerOrderTone(row.status)}>
                            {meta.label}: {formatInt(row.count)}
                          </StatusBadge>
                        );
                      })}
                    </div>
                  ) : null}
                </CardContent>
              </Card>

              <Card aria-labelledby="seller-top-products-heading">
                <CardHeader>
                  <CardTitle id="seller-top-products-heading">
                    Top sản phẩm bán chạy ({formatInt(report.top_products?.length ?? 0)})
                  </CardTitle>
                  <CardAction>
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {report.from_date} → {report.to_date}
                    </span>
                  </CardAction>
                </CardHeader>
                <CardContent>
                  {(report.top_products ?? []).length === 0 ? (
                    <p className="py-4 text-center text-sm text-muted-foreground">
                      Chưa có sản phẩm nào phát sinh doanh số trong kỳ chọn.
                    </p>
                  ) : (
                    <div className="space-y-3">
                      {report.top_products.map((item) => {
                        const pct = Math.max(
                          4,
                          Math.round((item.revenue_vnd / maxTopProductRevenue) * 100)
                        );
                        return (
                          <div key={item.product_id || item.product_name} className="space-y-1.5">
                            <div className="flex items-baseline justify-between gap-3 text-sm">
                              <div className="min-w-0 flex-1">
                                <div className="truncate font-medium text-foreground">
                                  {item.product_name}
                                </div>
                                <div className="text-xs text-muted-foreground tabular-nums">
                                  {formatInt(item.units_sold)} sản phẩm đã bán
                                </div>
                              </div>
                              <Money vnd={item.revenue_vnd} className="shrink-0" />
                            </div>
                            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                              <div
                                className="h-full rounded-full bg-brand"
                                style={{ width: `${pct}%` }}
                              />
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </CardContent>
              </Card>
            </>
          ) : null}

          {/* 4. Recent Orders Table */}
          <Card aria-labelledby="recent-orders-heading" className="gap-0 overflow-hidden py-0">
            <CardHeader className="border-b border-border py-4">
              <CardTitle id="recent-orders-heading">Đơn hàng mới</CardTitle>
              <CardAction>
                <Button variant="link" size="xs" asChild className="text-brand">
                  <Link href="/seller/don-hang">
                    <span>Tất cả ({formatInt(orders.length)})</span>
                    <ChevronRight className="size-3.5" aria-hidden="true" />
                  </Link>
                </Button>
              </CardAction>
            </CardHeader>

            {recentOrders.length === 0 ? (
              <CardContent className="py-8 text-center text-sm text-muted-foreground">
                Chưa có đơn hàng nào.
              </CardContent>
            ) : (
              <>
                <div className="hidden md:block">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Đơn hàng</TableHead>
                        <TableHead>Người nhận</TableHead>
                        <TableHead>Kênh</TableHead>
                        <TableHead className="text-right">Tiền hàng</TableHead>
                        <TableHead>Trạng thái</TableHead>
                        <TableHead className="text-right">Thao tác</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {recentOrders.map((order) => {
                        const meta = getSellerOrderStatusMeta(
                          order.status,
                          order.shipment_job_status,
                          order.tracking_code
                        );
                        const tone = resolveSellerOrderTone(
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
                          <TableRow
                            key={order.id}
                            className="cursor-pointer"
                            onClick={() => setDrawerOrderId(order.id)}
                          >
                            <TableCell className="max-w-[280px]">
                              <div className="flex items-center gap-3">
                                {thumb ? (
                                  <img
                                    src={thumb}
                                    alt={firstItem?.product_name || order.order_code}
                                    className="size-10 shrink-0 rounded-md border border-border bg-muted object-cover"
                                  />
                                ) : (
                                  <span
                                    className="flex size-10 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground"
                                    aria-hidden="true"
                                  >
                                    <Package className="size-4" />
                                  </span>
                                )}
                                <div className="min-w-0 flex-1">
                                  <button
                                    type="button"
                                    className="font-semibold text-foreground hover:text-brand hover:underline"
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      setDrawerOrderId(order.id);
                                    }}
                                  >
                                    {order.order_code}
                                  </button>
                                  <div className="truncate text-xs text-muted-foreground">
                                    {summarizeOrderItems(order.items)}
                                  </div>
                                </div>
                              </div>
                            </TableCell>
                            <TableCell className="max-w-[160px]">
                              <div
                                className="truncate font-medium text-foreground"
                                title={recipient}
                              >
                                {recipient}
                              </div>
                              {province ? (
                                <div
                                  className="truncate text-xs text-muted-foreground"
                                  title={province}
                                >
                                  {province}
                                </div>
                              ) : null}
                            </TableCell>
                            <TableCell>
                              <Badge
                                variant={channelMeta.isStorefront ? "info" : "secondary"}
                                className="rounded-md"
                                data-sales-channel={channelMeta.value}
                              >
                                {channelMeta.label}
                              </Badge>
                            </TableCell>
                            <TableCell className="text-right">
                              <Money vnd={order.subtotal_vnd} />
                            </TableCell>
                            <TableCell>
                              <StatusBadge tone={tone}>{meta.label}</StatusBadge>
                            </TableCell>
                            <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                              <div className="inline-flex items-center justify-end gap-1.5">
                                {nextAction ? (
                                  <Button
                                    type="button"
                                    size="xs"
                                    disabled={busyOrderId === order.id}
                                    onClick={() => void handleQuickAction(order)}
                                  >
                                    {busyOrderId === order.id
                                      ? "Đang lưu..."
                                      : nextAction.shortLabel}
                                  </Button>
                                ) : null}
                                <Button variant="outline" size="xs" asChild>
                                  <Link href={`/seller/don-hang/${order.id}`}>Chi tiết</Link>
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                <div className="md:hidden">
                  {recentOrders.map((order) => {
                    const meta = getSellerOrderStatusMeta(
                      order.status,
                      order.shipment_job_status,
                      order.tracking_code
                    );
                    const tone = resolveSellerOrderTone(
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
                      <div key={order.id} className="border-b border-border last:border-b-0">
                        <IndexRowCard
                          title={order.order_code}
                          badge={<StatusBadge tone={tone}>{meta.label}</StatusBadge>}
                          meta={
                            <>
                              <Money vnd={order.subtotal_vnd} size="sm" />
                              <span>·</span>
                              <span>{recipient}</span>
                              <span>·</span>
                              <span className="truncate">{summarizeOrderItems(order.items)}</span>
                            </>
                          }
                          onClick={() => setDrawerOrderId(order.id)}
                          className="border-b-0 pb-2"
                        />
                        <div className="flex flex-wrap items-center justify-end gap-2 px-4 pb-3">
                          {nextAction ? (
                            <Button
                              type="button"
                              size="xs"
                              className="min-h-9"
                              disabled={busyOrderId === order.id}
                              onClick={() => void handleQuickAction(order)}
                            >
                              {busyOrderId === order.id ? "Đang lưu..." : nextAction.shortLabel}
                            </Button>
                          ) : null}
                          <Button variant="outline" size="xs" className="min-h-9" asChild>
                            <Link href={`/seller/don-hang/${order.id}`}>Chi tiết</Link>
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </Card>

          {/* 5. Low-Stock SKU Alert Strip */}
          {topLowStock.length > 0 ? (
            <Card aria-labelledby="op-stock-heading" className="gap-0 overflow-hidden py-0">
              <CardHeader className="border-b border-border py-4">
                <CardTitle id="op-stock-heading">
                  SKU sắp hết tồn ({formatInt(lowStockAlerts.length)})
                </CardTitle>
                <CardAction>
                  <Button variant="link" size="xs" asChild className="text-brand">
                    <Link href="/seller/san-pham?filter=LOW_OOS">
                      <span>Kho sản phẩm</span>
                      <ChevronRight className="size-3.5" aria-hidden="true" />
                    </Link>
                  </Button>
                </CardAction>
              </CardHeader>
              <CardContent className="divide-y divide-border px-4 py-0 md:px-5">
                {topLowStock.map((alert) => (
                  <div
                    key={alert.skuId}
                    className="flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div className="flex min-w-0 flex-1 items-center gap-3">
                      {alert.image ? (
                        <img
                          src={alert.image}
                          alt={alert.productName}
                          className="size-10 shrink-0 rounded-md border border-border bg-muted object-cover"
                        />
                      ) : (
                        <span
                          className="flex size-10 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground"
                          aria-hidden="true"
                        >
                          <Package className="size-4" />
                        </span>
                      )}
                      <div className="min-w-0 flex-1">
                        <Link
                          href={`/seller/san-pham/${alert.productId}`}
                          className="block truncate text-sm font-medium text-foreground hover:text-brand hover:underline"
                        >
                          {alert.productName} ({alert.skuLabel})
                        </Link>
                        <span className="block truncate text-xs text-muted-foreground tabular-nums">
                          {alert.skuCode}, {formatVnd(alert.priceVnd)}
                        </span>
                      </div>
                    </div>

                    <div className="flex shrink-0 items-center justify-between gap-3 sm:justify-end">
                      <span
                        className={cn(
                          "text-sm font-semibold tabular-nums",
                          alert.availableStock === 0 ? "text-destructive" : "text-foreground"
                        )}
                      >
                        {alert.availableStock === 0
                          ? "Hết hàng"
                          : `Còn ${formatInt(alert.availableStock)}`}
                      </span>
                      <Button variant="outline" size="xs" asChild>
                        <Link href={`/seller/san-pham/${alert.productId}`}>Xem sản phẩm</Link>
                      </Button>
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : null}
        </>
      ) : null}

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
