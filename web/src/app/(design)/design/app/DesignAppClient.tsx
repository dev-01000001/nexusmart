"use client";

import * as React from "react";
import Link from "next/link";
import {
  ArrowLeft,
  BarChart3,
  Check,
  ChevronDown,
  ChevronUp,
  Copy,
  FileDown,
  Globe,
  Home,
  LayoutTemplate,
  MessageSquare,
  Monitor,
  Package,
  Pencil,
  Percent,
  Plus,
  RotateCcw,
  Search,
  Send,
  Settings,
  ShoppingBag,
  SlidersHorizontal,
  Smartphone,
  Sparkles,
  Store,
  Tablet,
  Truck,
  Users,
  Wallet,
} from "lucide-react";
import { toast } from "sonner";
import {
  AppShell,
  BarChart,
  DetailLayout,
  IndexRowCard,
  IndexTable,
  KpiChartCard,
  LineChart,
  MetricStrip,
  Money,
  PageHeader,
  SaveBar,
  SetupGuide,
  StatusBadge,
  Timeline,
  orderStatusTone,
  productStatusTone,
  shipmentJobTone,
  siteStatusTone,
  type AppCommandItem,
  type AppNavGroup,
} from "@/components/app";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
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
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { PLATFORM_TEMPLATES } from "@/features/site/templateCatalog";
import { formatCompactVnd, formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  SAMPLE_ANALYTICS_LABELS,
  SAMPLE_AOV_VALUES,
  SAMPLE_CONVERSATIONS,
  SAMPLE_ORDERS,
  SAMPLE_ORDERS_VALUES,
  SAMPLE_PRODUCTS,
  SAMPLE_REVENUE_VALUES,
  SAMPLE_SHOP_NAME,
  type SampleOrder,
} from "../_samples";

export type DesignAppPattern = "a1" | "a2" | "a3" | "a4" | "a5" | "a6" | "a7" | "a8" | "a9";

const PATTERN_LINKS: Array<{ key: DesignAppPattern; label: string }> = [
  { key: "a1", label: "A1 Bảng" },
  { key: "a2", label: "A2 Chi tiết đơn" },
  { key: "a3", label: "A3 Form + SaveBar" },
  { key: "a4", label: "A4 Phân tích" },
  { key: "a5", label: "A5 Cài đặt" },
  { key: "a6", label: "A6 Hộp thư" },
  { key: "a7", label: "A7 Site Studio" },
  { key: "a8", label: "A8 Chọn mẫu" },
  { key: "a9", label: "A9 SetupGuide" },
];

const SELLER_NAV_GROUPS: AppNavGroup[] = [
  {
    title: "Vận hành",
    items: [
      { href: "/design/app?kieu=a9", label: "Tổng quan", icon: Home, match: "exact" },
      { href: "/design/app?kieu=a2", label: "Đơn hàng", icon: ShoppingBag, badge: 3 },
      { href: "/design/app?kieu=a1&muc=tra-hang", label: "Trả hàng", icon: RotateCcw, badge: 1 },
      {
        href: "/design/app?kieu=a1",
        label: "Sản phẩm & Kho hàng",
        icon: Package,
        match: "exact",
        children: [
          { href: "/design/app?kieu=a3", label: "Thêm sản phẩm mới" },
          { href: "/design/app?kieu=a1&view=ton-kho", label: "Tồn kho theo SKU" },
        ],
      },
      { href: "/design/app?kieu=a4&muc=khuyen-mai", label: "Khuyến mãi", icon: Percent },
    ],
  },
  {
    title: "Khách hàng",
    items: [
      { href: "/design/app?kieu=a6", label: "Tin nhắn", icon: MessageSquare, badge: 1 },
      { href: "/design/app?kieu=a5&muc=tuong-tac", label: "Tương tác", icon: Users },
    ],
  },
  {
    title: "Cửa hàng",
    items: [
      {
        href: "/design/app?kieu=a7",
        label: "Site bán hàng",
        icon: LayoutTemplate,
        children: [{ href: "/design/app?kieu=a8", label: "Thư viện mẫu" }],
      },
      { href: "/design/site", label: "Xem site mẫu", icon: Store, external: true },
      { href: "/design/app?kieu=a4", label: "Tài chính & Báo cáo AI", icon: Wallet },
      { href: "/design/app?kieu=a5", label: "Hồ sơ gian hàng", icon: Settings },
    ],
  },
];

const COMMAND_ITEMS: AppCommandItem[] = [
  {
    label: "A1 — Danh sách sản phẩm & đơn hàng (mẫu)",
    group: "Kiểu trang",
    href: "/design/app?kieu=a1",
  },
  {
    label: "A2 — Chi tiết đơn hàng #NM-10293 (mẫu)",
    group: "Kiểu trang",
    href: "/design/app?kieu=a2",
  },
  {
    label: "A3 — Form tạo sản phẩm & SaveBar (mẫu)",
    group: "Kiểu trang",
    href: "/design/app?kieu=a3",
  },
  { label: "A4 — Tổng quan phân tích KPI (mẫu)", group: "Kiểu trang", href: "/design/app?kieu=a4" },
  { label: "A5 — Cài đặt hồ sơ shop (mẫu)", group: "Kiểu trang", href: "/design/app?kieu=a5" },
  { label: "A6 — Hộp thư khách hàng (mẫu)", group: "Kiểu trang", href: "/design/app?kieu=a6" },
  { label: "A7 — Site Studio 5 chế độ (mẫu)", group: "Kiểu trang", href: "/design/app?kieu=a7" },
  { label: "A8 — Chọn mẫu site 3 cột (mẫu)", group: "Kiểu trang", href: "/design/app?kieu=a8" },
  { label: "A9 — Hướng dẫn thiết lập (mẫu)", group: "Kiểu trang", href: "/design/app?kieu=a9" },
  { label: "Về mục lục hệ thống thiết kế", group: "Điều hướng", href: "/design" },
  { label: "Xem khung Site 8 mẫu", group: "Điều hướng", href: "/design/site" },
];

function PatternA1() {
  const [productTab, setProductTab] = React.useState("all");
  const [orderTab, setOrderTab] = React.useState("all");
  const [showFilter, setShowFilter] = React.useState(false);
  const [expandedProductId, setExpandedProductId] = React.useState<string | null>("sp-1");
  const [skuStocks, setSkuStocks] = React.useState<Record<string, number>>(() => {
    const init: Record<string, number> = {};
    for (const p of SAMPLE_PRODUCTS) {
      for (const s of p.skus) init[s.sku] = s.stock;
    }
    return init;
  });
  const [selectedOrderIds, setSelectedOrderIds] = React.useState<string[]>(["ord-1"]);
  const [quickOrder, setQuickOrder] = React.useState<SampleOrder | null>(null);

  const toggleOrder = (id: string) => {
    setSelectedOrderIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Sản phẩm (mẫu)"
        secondaryActions={
          <Button
            variant="secondary"
            size="sm"
            onClick={() => toast("Mở hộp thoại nhập CSV (mẫu)")}
          >
            Nhập CSV
          </Button>
        }
        moreActions={[
          { label: "Tải tệp mẫu CSV", onSelect: () => toast("Đã tải tệp mẫu CSV") },
          { label: "Lọc SKU sắp hết hàng", onSelect: () => setProductTab("out") },
        ]}
        primaryAction={
          <Button size="sm" asChild>
            <Link href="/design/app?kieu=a3">
              <Plus className="size-4" aria-hidden="true" />
              <span>Thêm sản phẩm</span>
            </Link>
          </Button>
        }
      />

      <MetricStrip
        items={[
          { label: "Đang bán (mẫu)", value: 128 },
          { label: "Sắp hết hàng (mẫu)", value: 6, hint: "≤ 5 chiếc" },
          { label: "Chờ duyệt (mẫu)", value: 2 },
        ]}
      />

      {/* Product table with expandable inline SKU inventory */}
      <IndexTable
        tabs={[
          { key: "all", label: "Tất cả", count: 4 },
          { key: "active", label: "Đang bán", count: 2 },
          { key: "pending", label: "Chờ duyệt", count: 1 },
          { key: "out", label: "Hết hàng", count: 1 },
        ]}
        activeTab={productTab}
        onTabChange={setProductTab}
        toolbar={
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="Mở thanh tìm kiếm và bộ lọc sản phẩm"
            onClick={() => setShowFilter((v) => !v)}
          >
            <SlidersHorizontal className="size-4" aria-hidden="true" />
          </Button>
        }
        footer={
          <>
            <span>Hiển thị 4 / 4 sản phẩm (mẫu)</span>
            <span>Trang 1 / 1</span>
          </>
        }
      >
        {showFilter ? (
          <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/40 px-4 py-2.5">
            <Input placeholder="Tìm tên sản phẩm hoặc mã SKU..." className="h-8 max-w-xs bg-card" />
            <Button size="sm" variant="secondary" onClick={() => setShowFilter(false)}>
              Đóng bộ lọc
            </Button>
          </div>
        ) : null}

        {/* Desktop table */}
        <div className="hidden md:block">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Sản phẩm</TableHead>
                <TableHead>Trạng thái</TableHead>
                <TableHead>Tồn kho</TableHead>
                <TableHead>Danh mục</TableHead>
                <TableHead className="text-right">Giá bán</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {SAMPLE_PRODUCTS.map((p) => {
                const totalStock = p.skus.reduce(
                  (acc, s) => acc + (skuStocks[s.sku] ?? s.stock),
                  0
                );
                const isExpanded = expandedProductId === p.id;
                return (
                  <React.Fragment key={p.id}>
                    <TableRow>
                      <TableCell className="max-w-[320px] whitespace-normal">
                        <div className="flex items-center gap-3">
                          <img
                            src={p.image}
                            alt={p.name}
                            className="size-10 shrink-0 rounded-md border border-border bg-muted object-cover"
                          />
                          <span className="line-clamp-2 font-medium text-foreground">{p.name}</span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <StatusBadge tone={productStatusTone(p.status)}>
                          {p.statusLabel}
                        </StatusBadge>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <span
                            className={cn(
                              "tabular-nums",
                              totalStock === 0 ? "font-medium text-destructive" : "text-foreground"
                            )}
                          >
                            {totalStock} còn · {p.skus.length} SKU
                          </span>
                          <Button
                            type="button"
                            variant="ghost"
                            size="xs"
                            onClick={() => setExpandedProductId(isExpanded ? null : p.id)}
                          >
                            <span>Sửa tồn</span>
                            {isExpanded ? (
                              <ChevronUp className="size-3.5" aria-hidden="true" />
                            ) : (
                              <ChevronDown className="size-3.5" aria-hidden="true" />
                            )}
                          </Button>
                        </div>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{p.category}</TableCell>
                      <TableCell className="text-right">
                        <Money vnd={p.priceVnd} />
                      </TableCell>
                    </TableRow>
                    {isExpanded ? (
                      <TableRow className="bg-muted/45 hover:bg-muted/45">
                        <TableCell colSpan={5} className="py-3">
                          <div className="flex flex-col gap-2 pl-12">
                            {p.skus.map((sku) => (
                              <div
                                key={sku.sku}
                                className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-card px-3 py-2"
                              >
                                <div className="flex items-center gap-2 text-xs">
                                  <Badge variant="secondary" className="rounded-md font-mono">
                                    {sku.sku}
                                  </Badge>
                                  <span className="font-medium text-foreground">{sku.label}</span>
                                </div>
                                <div className="flex items-center gap-2">
                                  <Input
                                    type="number"
                                    min={0}
                                    aria-label={`Tồn kho ${sku.sku}`}
                                    value={skuStocks[sku.sku] ?? sku.stock}
                                    onChange={(e) =>
                                      setSkuStocks((prev) => ({
                                        ...prev,
                                        [sku.sku]: Math.max(0, Number(e.target.value) || 0),
                                      }))
                                    }
                                    className="h-8 w-20 bg-card text-right tabular-nums"
                                  />
                                  <Button
                                    size="xs"
                                    variant="outline"
                                    onClick={() => toast.success(`Đã lưu tồn ${sku.sku} (mẫu)`)}
                                  >
                                    Lưu
                                  </Button>
                                </div>
                              </div>
                            ))}
                          </div>
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </React.Fragment>
                );
              })}
            </TableBody>
          </Table>
        </div>

        {/* Mobile row cards (< 768px) */}
        <div className="md:hidden">
          {SAMPLE_PRODUCTS.map((p) => (
            <IndexRowCard
              key={p.id}
              media={
                <img
                  src={p.image}
                  alt={p.name}
                  className="size-11 rounded-md border border-border bg-muted object-cover"
                />
              }
              title={p.name}
              badge={<StatusBadge tone={productStatusTone(p.status)}>{p.statusLabel}</StatusBadge>}
              meta={
                <>
                  <Money vnd={p.priceVnd} size="sm" />
                  <span>·</span>
                  <span>{p.category}</span>
                </>
              }
              onClick={() => toast(`Mở chi tiết sản phẩm mẫu: ${p.id}`)}
            />
          ))}
        </div>
      </IndexTable>

      {/* Order table with bulk checkbox selection + Quick Drawer Sheet */}
      <div className="space-y-3">
        <h2 className="text-base font-semibold text-foreground">
          Đơn hàng có thao tác lô & xem nhanh Sheet (mẫu)
        </h2>
        <IndexTable
          tabs={[
            { key: "all", label: "Tất cả", count: 4 },
            { key: "pending", label: "Chờ xử lý", count: 2 },
            { key: "shipping", label: "Đang giao", count: 1 },
            { key: "done", label: "Hoàn tất", count: 1 },
          ]}
          activeTab={orderTab}
          onTabChange={setOrderTab}
          selection={
            selectedOrderIds.length > 0
              ? {
                  count: selectedOrderIds.length,
                  onClear: () => setSelectedOrderIds([]),
                  actions: (
                    <>
                      <Button
                        size="xs"
                        variant="outline"
                        onClick={() =>
                          toast.success(
                            `Đã tạo vận đơn lô cho ${selectedOrderIds.length} đơn (mẫu)`
                          )
                        }
                      >
                        Lên đơn lô GHN
                      </Button>
                      <Button
                        size="xs"
                        onClick={() =>
                          toast(`Đã xuất nhãn PDF A6 cho ${selectedOrderIds.length} đơn (mẫu)`)
                        }
                      >
                        In nhãn PDF
                      </Button>
                    </>
                  ),
                }
              : undefined
          }
        >
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <Checkbox
                      aria-label="Chọn tất cả đơn hàng mẫu"
                      checked={selectedOrderIds.length === SAMPLE_ORDERS.length}
                      onCheckedChange={(checked) =>
                        setSelectedOrderIds(checked ? SAMPLE_ORDERS.map((o) => o.id) : [])
                      }
                    />
                  </TableHead>
                  <TableHead>Mã đơn</TableHead>
                  <TableHead>Khách hàng</TableHead>
                  <TableHead>Trạng thái</TableHead>
                  <TableHead>Vận đơn</TableHead>
                  <TableHead className="text-right">Tổng tiền</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {SAMPLE_ORDERS.map((ord) => {
                  const checked = selectedOrderIds.includes(ord.id);
                  return (
                    <TableRow
                      key={ord.id}
                      data-state={checked ? "selected" : undefined}
                      className="cursor-pointer"
                      onClick={() => setQuickOrder(ord)}
                    >
                      <TableCell onClick={(e) => e.stopPropagation()}>
                        <Checkbox
                          aria-label={`Chọn đơn ${ord.code}`}
                          checked={checked}
                          onCheckedChange={() => toggleOrder(ord.id)}
                        />
                      </TableCell>
                      <TableCell className="font-semibold text-foreground">{ord.code}</TableCell>
                      <TableCell>
                        <div className="font-medium">{ord.customer}</div>
                        <div className="text-xs text-muted-foreground">{ord.createdAt}</div>
                      </TableCell>
                      <TableCell>
                        <StatusBadge tone={orderStatusTone(ord.status)}>
                          {ord.statusLabel}
                        </StatusBadge>
                      </TableCell>
                      <TableCell>
                        {ord.shipmentJob ? (
                          <StatusBadge tone={shipmentJobTone(ord.shipmentJob)}>
                            {ord.shipmentJobLabel}
                          </StatusBadge>
                        ) : (
                          <span className="text-xs text-muted-foreground">
                            {ord.shipmentJobLabel}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        <Money vnd={ord.totalVnd} />
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          <div className="md:hidden">
            {SAMPLE_ORDERS.map((ord) => (
              <IndexRowCard
                key={ord.id}
                leading={
                  <Checkbox
                    aria-label={`Chọn đơn ${ord.code}`}
                    checked={selectedOrderIds.includes(ord.id)}
                    onCheckedChange={() => toggleOrder(ord.id)}
                  />
                }
                title={`${ord.code} · ${ord.customer}`}
                badge={
                  <StatusBadge tone={orderStatusTone(ord.status)}>{ord.statusLabel}</StatusBadge>
                }
                meta={
                  <>
                    <Money vnd={ord.totalVnd} size="sm" />
                    <span>·</span>
                    <span>{ord.createdAt}</span>
                  </>
                }
                onClick={() => setQuickOrder(ord)}
              />
            ))}
          </div>
        </IndexTable>
      </div>

      <Sheet open={quickOrder !== null} onOpenChange={(open) => !open && setQuickOrder(null)}>
        <SheetContent side="right">
          {quickOrder ? (
            <>
              <SheetHeader>
                <SheetTitle>Xem nhanh {quickOrder.code} (mẫu)</SheetTitle>
                <SheetDescription>
                  {quickOrder.customer} · {quickOrder.createdAt}
                </SheetDescription>
              </SheetHeader>
              <div className="space-y-3 px-4 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">Trạng thái</span>
                  <StatusBadge tone={orderStatusTone(quickOrder.status)}>
                    {quickOrder.statusLabel}
                  </StatusBadge>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">Thanh toán</span>
                  <span className="font-medium">{quickOrder.paymentMethod}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">Tổng đơn</span>
                  <Money vnd={quickOrder.totalVnd} size="md" />
                </div>
              </div>
              <SheetFooter>
                <Button asChild>
                  <Link href="/design/app?kieu=a2" onClick={() => setQuickOrder(null)}>
                    Mở trang chi tiết
                  </Link>
                </Button>
              </SheetFooter>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}

function PatternA2() {
  const copyText = (label: string) => toast.success(`Đã sao chép ${label} (mẫu)`);

  return (
    <div className="space-y-4">
      <PageHeader
        title="#NM-10293 (mẫu)"
        backHref="/design/app?kieu=a1"
        badges={
          <>
            <StatusBadge tone={orderStatusTone("PENDING")}>Chờ thanh toán</StatusBadge>
            <StatusBadge tone={orderStatusTone("CONFIRMED")}>Chờ xử lý</StatusBadge>
          </>
        }
        subtitle="26/11/2026 lúc 09:48 · Site: aodep.nexusmart.vn (mẫu)"
        secondaryActions={
          <Button variant="secondary" size="sm" onClick={() => toast("Đang in nhãn A6 (mẫu)")}>
            In nhãn
          </Button>
        }
        moreActions={[
          { label: "Sao chép mã đơn", onSelect: () => copyText("mã đơn #NM-10293") },
          { label: "Huỷ đơn hàng", destructive: true, onSelect: () => toast.error("Đã huỷ mẫu") },
        ]}
        pager={{ prevHref: "/design/app?kieu=a2", nextHref: "/design/app?kieu=a2" }}
        primaryAction={
          <Button size="sm" onClick={() => toast.success("Đã xác nhận đơn #NM-10293 (mẫu)")}>
            Xác nhận đơn
          </Button>
        }
      />

      <DetailLayout
        main={
          <>
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <StatusBadge tone="attention">Chờ xử lý (2)</StatusBadge>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex items-start gap-3 rounded-lg border border-border p-3">
                  <img
                    src="/products/ao-polo-det-kim-den.png"
                    alt="Áo polo dệt kim đen"
                    className="size-12 shrink-0 rounded-md border border-border bg-muted object-cover"
                  />
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="font-medium text-foreground">
                      Áo polo dệt kim cổ bẻ sợi bông chải kỹ (mẫu)
                    </div>
                    <Badge variant="secondary" className="rounded-md">
                      Đen / L
                    </Badge>
                  </div>
                  <div className="shrink-0 text-right text-sm tabular-nums">
                    <div className="text-muted-foreground">289.000₫ × 1</div>
                    <div className="font-semibold text-foreground">289.000₫</div>
                  </div>
                </div>
                <div className="flex items-start gap-3 rounded-lg border border-border p-3">
                  <img
                    src="/products/ao-thun-cotton-trang.png"
                    alt="Áo thun cotton trắng"
                    className="size-12 shrink-0 rounded-md border border-border bg-muted object-cover"
                  />
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="font-medium text-foreground">
                      Áo thun Cotton Compact 220gsm (mẫu)
                    </div>
                    <Badge variant="secondary" className="rounded-md">
                      Trắng / M
                    </Badge>
                  </div>
                  <div className="shrink-0 text-right text-sm tabular-nums">
                    <div className="text-muted-foreground">199.000₫ × 1</div>
                    <div className="font-semibold text-foreground">199.000₫</div>
                  </div>
                </div>
              </CardContent>
              <CardFooter className="justify-end gap-2 border-t border-border pt-4">
                <Button variant="outline" size="sm" onClick={() => toast("Đánh dấu đóng gói mẫu")}>
                  Đóng gói
                </Button>
                <Button size="sm" onClick={() => toast.success("Tạo vận đơn GHN thành công (mẫu)")}>
                  Tạo vận đơn GHN
                </Button>
              </CardFooter>
            </Card>

            <Card>
              <CardHeader>
                <StatusBadge tone="warning">Chờ thu hộ (COD)</StatusBadge>
              </CardHeader>
              <CardContent className="space-y-2.5 text-sm tabular-nums">
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Tạm tính (2 sản phẩm)</span>
                  <span>488.000₫</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Mã giảm giá (SHOP10)</span>
                  <span className="text-badge-success-ink">−20.000₫</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Phí giao (GHN · 0,8 kg)</span>
                  <span>30.000₫</span>
                </div>
                <Separator />
                <div className="flex justify-between font-semibold text-foreground">
                  <span>Tổng cộng</span>
                  <span>498.000₫</span>
                </div>
                <div className="flex justify-between text-xs text-muted-foreground">
                  <span>Thu khi giao hàng (COD)</span>
                  <span>498.000₫</span>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Lịch sử xử lý</CardTitle>
              </CardHeader>
              <CardContent>
                <Timeline
                  items={[
                    {
                      id: "o-1",
                      at: "10:02 26/11",
                      title: "Xác nhận tồn kho SKU (mẫu)",
                      description: "Đã giữ tồn POLO-DEN-L (1) và TEE-TRANG-M (1).",
                    },
                    {
                      id: "o-2",
                      at: "09:48 26/11",
                      title: "Khách đặt đơn COD (mẫu)",
                      description: "Khởi tạo từ site gian hàng aodep.nexusmart.vn.",
                    },
                  ]}
                />
              </CardContent>
            </Card>
          </>
        }
        aside={
          <>
            <Card>
              <CardHeader>
                <CardTitle>Ghi chú của khách</CardTitle>
                <CardAction>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label="Sửa ghi chú"
                    onClick={() => toast("Chế độ sửa ghi chú mẫu")}
                  >
                    <Pencil className="size-3.5" aria-hidden="true" />
                  </Button>
                </CardAction>
              </CardHeader>
              <CardContent className="text-[13px] text-muted-foreground">
                Giao giờ hành chính, gọi trước 15 phút giúp mình (mẫu).
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Khách hàng</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 text-[13px]">
                <div>
                  <div className="font-medium text-brand">Nguyễn Minh Hoàng (mẫu)</div>
                  <div className="text-muted-foreground">3 đơn hàng trước đây</div>
                </div>
                <Separator />
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="font-medium text-foreground">Liên hệ</div>
                    <div className="truncate text-muted-foreground">hoang.nguyen@example.vn</div>
                    <div className="text-muted-foreground tabular-nums">0908 112 233</div>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label="Sao chép thông tin liên hệ"
                    onClick={() => copyText("thông tin liên hệ")}
                  >
                    <Copy className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>
                <Separator />
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="font-medium text-foreground">Địa chỉ giao hàng</div>
                    <div className="text-muted-foreground">
                      128 Võ Văn Ngân, Phường Bình Thọ, TP. Thủ Đức, TP. Hồ Chí Minh (mẫu)
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label="Sao chép địa chỉ giao hàng"
                    onClick={() => copyText("địa chỉ giao hàng")}
                  >
                    <Copy className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Vận chuyển</CardTitle>
              </CardHeader>
              <CardContent className="space-y-1.5 text-[13px]">
                <div className="flex items-center gap-2 font-medium text-foreground">
                  <Truck className="size-4 text-brand" aria-hidden="true" />
                  <span>Giao Hàng Nhanh (GHN)</span>
                </div>
                <div className="text-muted-foreground">Trạng thái: Đang chờ bàn giao (mẫu)</div>
              </CardContent>
            </Card>
          </>
        }
      />
    </div>
  );
}

function PatternA3() {
  const [name, setName] = React.useState("Áo polo dệt kim cổ bẻ phiên bản mới (mẫu)");
  const [price, setPrice] = React.useState("289000");
  const [status, setStatus] = React.useState("APPROVED");
  const [dirty, setDirty] = React.useState(true);

  return (
    <div className="space-y-4">
      <SaveBar
        visible={dirty}
        onDiscard={() => {
          setName("Áo polo dệt kim cổ bẻ phiên bản mới (mẫu)");
          setPrice("289000");
          setDirty(false);
          toast("Đã hoàn tác thay đổi mẫu");
        }}
        onSave={() => {
          setDirty(false);
          toast.success("Đã lưu sản phẩm mẫu");
        }}
      />

      <PageHeader
        title="Sửa sản phẩm (mẫu)"
        backHref="/design/app?kieu=a1"
        badges={<StatusBadge tone={productStatusTone(status)}>Đang bán</StatusBadge>}
      />

      <DetailLayout
        main={
          <>
            <Card>
              <CardHeader>
                <CardTitle>Thông tin cơ bản</CardTitle>
              </CardHeader>
              <CardContent>
                <FieldGroup className="gap-4">
                  <Field>
                    <FieldLabel htmlFor="a3-name">Tên sản phẩm</FieldLabel>
                    <Input
                      id="a3-name"
                      value={name}
                      onChange={(e) => {
                        setName(e.target.value);
                        setDirty(true);
                      }}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="a3-desc">Mô tả chất liệu & phom dáng</FieldLabel>
                    <Textarea
                      id="a3-desc"
                      rows={3}
                      defaultValue="Vải cotton piqué 220gsm thoáng khí, giữ phom cổ sau giặt máy (mẫu)."
                      onChange={() => setDirty(true)}
                    />
                  </Field>
                </FieldGroup>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Giá & Biến thể SKU</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="a3-price">Giá bán (VND nguyên)</FieldLabel>
                    <Input
                      id="a3-price"
                      type="number"
                      value={price}
                      onChange={(e) => {
                        setPrice(e.target.value);
                        setDirty(true);
                      }}
                    />
                    <FieldDescription>Hiển thị: {formatVnd(Number(price) || 0)}</FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="a3-compare">Giá gốc gạch ngang (tuỳ chọn)</FieldLabel>
                    <Input
                      id="a3-compare"
                      type="number"
                      defaultValue="349000"
                      onChange={() => setDirty(true)}
                    />
                  </Field>
                </div>
              </CardContent>
            </Card>
          </>
        }
        aside={
          <Card>
            <CardHeader>
              <CardTitle>Trạng thái hiển thị</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <Field>
                <FieldLabel>Trạng thái sản phẩm</FieldLabel>
                <Select
                  value={status}
                  onValueChange={(v) => {
                    setStatus(v);
                    setDirty(true);
                  }}
                >
                  <SelectTrigger className="w-full bg-card">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="APPROVED">Đang bán</SelectItem>
                    <SelectItem value="HIDDEN">Tạm ẩn</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="a3-cat">Danh mục</FieldLabel>
                <Input id="a3-cat" defaultValue="Áo polo (mẫu)" onChange={() => setDirty(true)} />
              </Field>
            </CardContent>
          </Card>
        }
      />
    </div>
  );
}

function PatternA4() {
  const [period, setPeriod] = React.useState("7d");
  return (
    <div className="space-y-4">
      <PageHeader
        title="Tổng quan phân tích (mẫu)"
        subtitle="Số liệu theo kỳ đã chọn — chỉ hiển thị dữ liệu trả về từ báo cáo."
        primaryAction={
          <Button
            size="sm"
            variant="secondary"
            onClick={() => toast.success("Đã xuất tệp báo cáo CSV (mẫu)")}
          >
            <FileDown className="size-4" aria-hidden="true" />
            <span>Xuất CSV</span>
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <Select value={period} onValueChange={setPeriod}>
          <SelectTrigger className="w-44 bg-card">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="7d">7 ngày qua (mẫu)</SelectItem>
            <SelectItem value="30d">30 ngày qua (mẫu)</SelectItem>
            <SelectItem value="90d">90 ngày qua (mẫu)</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <KpiChartCard
          title="Doanh thu (mẫu)"
          tooltip="Tổng giá trị đơn hàng hợp lệ phát sinh trong khoảng ngày đã chọn."
          value={formatVnd(12450000)}
          chart={
            <LineChart
              labels={SAMPLE_ANALYTICS_LABELS}
              series={[{ name: "Doanh thu", values: SAMPLE_REVENUE_VALUES }]}
              height={176}
              showArea
              formatValue={formatVnd}
              formatTick={formatCompactVnd}
              ariaLabel="Biểu đồ doanh thu mẫu 7 ngày qua"
            />
          }
          footer="20/11/2026 – 26/11/2026 (mẫu)"
        />
        <KpiChartCard
          title="Đơn hàng (mẫu)"
          tooltip="Tổng số đơn hàng được tạo trong khoảng ngày đã chọn."
          value="54"
          chart={
            <LineChart
              labels={SAMPLE_ANALYTICS_LABELS}
              series={[{ name: "Đơn hàng", values: SAMPLE_ORDERS_VALUES }]}
              height={176}
              ariaLabel="Biểu đồ số đơn hàng mẫu 7 ngày qua"
            />
          }
          footer="20/11/2026 – 26/11/2026 (mẫu)"
        />
        <KpiChartCard
          title="Giá trị TB/đơn (mẫu)"
          tooltip="Doanh thu chia cho tổng số đơn hàng trong kỳ."
          value={formatVnd(230555)}
          chart={
            <LineChart
              labels={SAMPLE_ANALYTICS_LABELS}
              series={[{ name: "Giá trị TB", values: SAMPLE_AOV_VALUES }]}
              height={176}
              formatValue={formatVnd}
              formatTick={formatCompactVnd}
              ariaLabel="Biểu đồ giá trị trung bình đơn hàng mẫu 7 ngày qua"
            />
          }
          footer="20/11/2026 – 26/11/2026 (mẫu)"
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Phân bổ đơn theo ngày (BarChart mẫu)</CardTitle>
        </CardHeader>
        <CardContent>
          <BarChart
            labels={SAMPLE_ANALYTICS_LABELS}
            values={SAMPLE_ORDERS_VALUES}
            highlightIndex={5}
            height={190}
          />
        </CardContent>
      </Card>
    </div>
  );
}

function PatternA5() {
  const [dirty, setDirty] = React.useState(false);
  return (
    <div className="mx-auto max-w-[800px] space-y-6">
      <SaveBar
        visible={dirty}
        onDiscard={() => setDirty(false)}
        onSave={() => {
          setDirty(false);
          toast.success("Đã lưu cấu hình gian hàng (mẫu)");
        }}
      />
      <PageHeader title="Hồ sơ & Cài đặt gian hàng (mẫu)" />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-foreground">Thông tin cửa hàng</h2>
          <p className="text-[13px] text-muted-foreground">
            Tên hiển thị trên đầu trang site, hoá đơn giao hàng và email xác nhận đơn.
          </p>
        </div>
        <Card>
          <CardContent className="space-y-4">
            <Field>
              <FieldLabel htmlFor="a5-shop">Tên gian hàng</FieldLabel>
              <Input id="a5-shop" defaultValue={SAMPLE_SHOP_NAME} onChange={() => setDirty(true)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="a5-hotline">Số điện thoại hỗ trợ khách</FieldLabel>
              <Input id="a5-hotline" defaultValue="0901 234 567" onChange={() => setDirty(true)} />
            </Field>
          </CardContent>
        </Card>
      </div>

      <Separator />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-foreground">Kho lấy hàng mặc định</h2>
          <p className="text-[13px] text-muted-foreground">
            Dùng để tính cước vận chuyển GHN và in địa chỉ người gửi trên nhãn A6.
          </p>
        </div>
        <Card>
          <CardContent className="space-y-4">
            <Field>
              <FieldLabel htmlFor="a5-addr">Địa chỉ kho (mẫu)</FieldLabel>
              <Input
                id="a5-addr"
                defaultValue="45 Đường số 8, Phường Linh Xuân, TP. Thủ Đức, TP.HCM"
                onChange={() => setDirty(true)}
              />
            </Field>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function PatternA6() {
  const [activeId, setActiveId] = React.useState<string>("c-1");
  const [mobileShowThread, setMobileShowThread] = React.useState(false);
  const [draft, setDraft] = React.useState("");
  const activeConv = SAMPLE_CONVERSATIONS.find((c) => c.id === activeId) ?? SAMPLE_CONVERSATIONS[0];

  return (
    <div className="space-y-4">
      <PageHeader title="Hộp thư khách hàng (mẫu)" />
      <div className="grid min-h-[480px] grid-cols-1 overflow-hidden rounded-xl bg-card shadow-card md:grid-cols-[320px_minmax(0,1fr)]">
        {/* Conversation list */}
        <div
          className={cn(
            "flex flex-col border-border md:border-r",
            mobileShowThread ? "hidden md:flex" : "flex"
          )}
        >
          <div className="border-b border-border p-3">
            <div className="relative">
              <Search
                className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                aria-hidden="true"
              />
              <Input placeholder="Tìm hội thoại mẫu..." className="h-9 pl-8" />
            </div>
          </div>
          <div className="divide-y divide-border overflow-y-auto">
            {SAMPLE_CONVERSATIONS.map((conv) => (
              <button
                key={conv.id}
                type="button"
                onClick={() => {
                  setActiveId(conv.id);
                  setMobileShowThread(true);
                }}
                className={cn(
                  "flex w-full items-start gap-2.5 px-3.5 py-3 text-left transition-colors hover:bg-accent/55",
                  conv.id === activeId && "bg-accent/70"
                )}
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    "mt-1.5 size-2 shrink-0 rounded-full",
                    conv.unread ? "bg-brand" : "bg-transparent"
                  )}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline justify-between gap-2">
                    <span
                      className={cn(
                        "truncate text-sm",
                        conv.unread ? "font-semibold text-foreground" : "font-medium"
                      )}
                    >
                      {conv.customer}
                    </span>
                    <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                      {conv.at}
                    </span>
                  </div>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">{conv.preview}</p>
                </div>
              </button>
            ))}
          </div>
        </div>

        {/* Thread view */}
        <div className={cn("flex flex-col", mobileShowThread ? "flex" : "hidden md:flex")}>
          <div className="flex items-center gap-2 border-b border-border px-4 py-3">
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="Quay lại danh sách hội thoại"
              onClick={() => setMobileShowThread(false)}
              className="md:hidden"
            >
              <ArrowLeft className="size-4" aria-hidden="true" />
            </Button>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">{activeConv.customer}</div>
              <div className="text-xs text-muted-foreground">Đang xem hội thoại mẫu</div>
            </div>
          </div>

          <div className="flex flex-1 flex-col gap-3 overflow-y-auto p-4">
            {activeConv.messages.map((msg) => (
              <div
                key={msg.id}
                className={cn(
                  "max-w-[80%] rounded-xl px-3.5 py-2.5 text-sm",
                  msg.from === "seller"
                    ? "self-end bg-primary text-primary-foreground"
                    : "self-start bg-muted text-foreground"
                )}
              >
                <p>{msg.text}</p>
                <div
                  className={cn(
                    "mt-1 text-[11px] tabular-nums",
                    msg.from === "seller" ? "text-primary-foreground/75" : "text-muted-foreground"
                  )}
                >
                  {msg.at}
                </div>
              </div>
            ))}
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!draft.trim()) return;
              toast("Tin nhắn mẫu — không gửi lên máy chủ.");
              setDraft("");
            }}
            className="flex items-center gap-2 border-t border-border p-3"
          >
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder="Nhập phản hồi và nhấn Enter (mẫu)..."
            />
            <Button type="submit" size="icon" aria-label="Gửi tin nhắn">
              <Send className="size-4" aria-hidden="true" />
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}

function PatternA7() {
  const [viewport, setViewport] = React.useState<"390" | "768" | "1440">("1440");

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-card px-4 py-3 shadow-card">
        <div className="flex min-w-0 items-center gap-2">
          <h1 className="truncate text-base font-semibold">
            Site Studio — aodep.nexusmart.vn (mẫu)
          </h1>
          <StatusBadge tone={siteStatusTone("DRAFT")}>Bản nháp</StatusBadge>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => toast("Đã lưu bản nháp mẫu")}>
            Lưu nháp
          </Button>
          <Button size="sm" onClick={() => toast.success("Đã xuất bản site mẫu")}>
            Xuất bản
          </Button>
        </div>
      </div>

      <Tabs defaultValue="customize" className="gap-4">
        <TabsList className="w-full justify-start overflow-x-auto rounded-xl bg-card p-1.5 shadow-card">
          <TabsTrigger value="templates">Mẫu</TabsTrigger>
          <TabsTrigger value="customize">Tuỳ chỉnh</TabsTrigger>
          <TabsTrigger value="json">Mã JSON</TabsTrigger>
          <TabsTrigger value="domain">Tên miền</TabsTrigger>
          <TabsTrigger value="versions">Phiên bản</TabsTrigger>
        </TabsList>

        <TabsContent value="customize">
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-[360px_minmax(0,1fr)]">
            <Card>
              <CardHeader>
                <CardTitle>Tuỳ chỉnh giao diện & khối (mẫu)</CardTitle>
              </CardHeader>
              <CardContent>
                <Accordion type="single" collapsible defaultValue="theme">
                  <AccordionItem value="theme">
                    <AccordionTrigger>Bảng màu & Phông chữ</AccordionTrigger>
                    <AccordionContent className="space-y-3 text-xs text-muted-foreground">
                      <div>Nền #FAFAFA · Chữ #14181C · Tương phản 17.4:1 (đạt AA).</div>
                      <Select defaultValue="inter">
                        <SelectTrigger className="w-full bg-card">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="inter">Inter</SelectItem>
                          <SelectItem value="work-sans">Work Sans</SelectItem>
                          <SelectItem value="be-vietnam-pro">Be Vietnam Pro</SelectItem>
                          <SelectItem value="ibm-plex-sans">IBM Plex Sans</SelectItem>
                          <SelectItem value="plus-jakarta-sans">Plus Jakarta Sans</SelectItem>
                        </SelectContent>
                      </Select>
                    </AccordionContent>
                  </AccordionItem>
                  <AccordionItem value="layout">
                    <AccordionTrigger>Bố cục 4 trang phụ</AccordionTrigger>
                    <AccordionContent className="text-xs text-muted-foreground">
                      Danh mục (lưới 3 cột), Chi tiết sản phẩm (chia đôi), Giỏ hàng, Giới thiệu.
                    </AccordionContent>
                  </AccordionItem>
                  <AccordionItem value="sections">
                    <AccordionTrigger>Khối & Block trang chủ</AccordionTrigger>
                    <AccordionContent className="text-xs text-muted-foreground">
                      Hero trung tâm · Sản phẩm tuyển chọn · Câu chuyện xưởng · Cam kết 3 cột.
                    </AccordionContent>
                  </AccordionItem>
                </Accordion>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Xem trước trực tiếp (chỉ hình mẫu)</CardTitle>
                <CardAction>
                  <div className="flex items-center gap-1">
                    <Button
                      size="icon-xs"
                      variant={viewport === "390" ? "secondary" : "ghost"}
                      aria-label="Khổ di động 390px"
                      onClick={() => setViewport("390")}
                    >
                      <Smartphone className="size-3.5" aria-hidden="true" />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant={viewport === "768" ? "secondary" : "ghost"}
                      aria-label="Khổ máy tính bảng 768px"
                      onClick={() => setViewport("768")}
                    >
                      <Tablet className="size-3.5" aria-hidden="true" />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant={viewport === "1440" ? "secondary" : "ghost"}
                      aria-label="Khổ máy tính 1440px"
                      onClick={() => setViewport("1440")}
                    >
                      <Monitor className="size-3.5" aria-hidden="true" />
                    </Button>
                  </div>
                </CardAction>
              </CardHeader>
              <CardContent>
                <div
                  className={cn(
                    "mx-auto overflow-hidden rounded-lg border border-border bg-muted/50 p-4 text-center transition-all",
                    viewport === "390"
                      ? "max-w-[360px]"
                      : viewport === "768"
                        ? "max-w-[540px]"
                        : "w-full"
                  )}
                >
                  <img
                    src="/banners/campaign-everyday.jpg"
                    alt="Xem trước mẫu Thời trang tối giản"
                    className="h-52 w-full rounded-md object-cover"
                  />
                  <p className="mt-3 text-xs text-muted-foreground">
                    Khung xem trước chế độ {viewport}px ·{" "}
                    <Link href="/design/site" className="font-medium text-brand hover:underline">
                      Mở trang thử 8 mẫu đầy đủ →
                    </Link>
                  </p>
                </div>
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="templates">
          <PatternA8 embedded />
        </TabsContent>
        <TabsContent value="json">
          <Card>
            <CardContent className="font-mono text-xs text-muted-foreground">
              {`{ "schema_version": 2, "template_id": "minimal-fashion", "theme": { "font_family": "inter" } }`}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="domain">
          <Card>
            <CardContent className="space-y-3 text-sm">
              <div className="flex items-center gap-2 font-medium">
                <Globe className="size-4 text-brand" aria-hidden="true" />
                <span>aodep.nexusmart.vn (tên miền phụ mặc định · mẫu)</span>
              </div>
              <p className="text-xs text-muted-foreground">
                Gắn thêm tên miền riêng (CNAME + TXT) trong chế độ Tên miền.
              </p>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="versions">
          <Card>
            <CardContent className="text-xs text-muted-foreground">
              Phiên bản #3 (24/11/2026) · Phiên bản #2 (20/11/2026) · Phiên bản #1 (18/11/2026)
              (mẫu).
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function PatternA8({ embedded = false }: { embedded?: boolean }) {
  const templates = PLATFORM_TEMPLATES.slice(0, 3);
  return (
    <div className="space-y-4">
      {!embedded ? (
        <PageHeader
          title="Chọn mẫu giao diện site (mẫu)"
          subtitle="Mỗi mẫu có bố cục riêng và giữ nguyên cấu trúc Template JSON v2. Miễn phí hoàn toàn."
        />
      ) : null}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {templates.map((tpl, idx) => (
          <Card key={tpl.id} className="justify-between">
            <CardHeader className="gap-2">
              <div className="h-5">
                {idx === 0 ? <Badge variant="info">Đang dùng</Badge> : null}
              </div>
              <div className="text-2xl leading-8 font-semibold tracking-tight text-foreground">
                {tpl.name}
              </div>
              <CardDescription>{tpl.description}</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="overflow-hidden rounded-lg border border-border">
                <img
                  src={tpl.preview_image_url}
                  alt={tpl.name}
                  className="aspect-[16/10] w-full object-cover"
                />
              </div>
              <Button
                className="w-full"
                variant={idx === 0 ? "outline" : "default"}
                onClick={() => toast.success(`Đã áp dụng mẫu ${tpl.name} (mẫu)`)}
              >
                {idx === 0 ? "Đang sử dụng mẫu này" : "Dùng mẫu này"}
              </Button>
              <div className="space-y-2 border-t border-border pt-3">
                <div className="text-xs font-semibold text-foreground">Đặc điểm bố cục</div>
                <ul className="space-y-1.5 text-[13px] text-muted-foreground">
                  {tpl.tags.map((tag) => (
                    <li key={tag} className="flex items-center gap-2">
                      <Check className="size-4 shrink-0 text-brand" aria-hidden="true" />
                      <span>{tag}</span>
                    </li>
                  ))}
                </ul>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}

function PatternA9() {
  const [simulateComplete, setSimulateComplete] = React.useState(false);

  return (
    <div className="mx-auto max-w-[820px] space-y-4">
      <Alert variant="inverse">
        <Sparkles className="size-4" aria-hidden="true" />
        <AlertTitle>Site bán hàng đang ở chế độ bản nháp (mẫu)</AlertTitle>
        <AlertDescription>
          Hoàn tất 3 bước dưới đây và xuất bản site để khách hàng có thể đặt mua trực tiếp.
        </AlertDescription>
      </Alert>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <PageHeader
          title="Sẵn sàng bán hàng (mẫu)"
          subtitle="Mỗi bước đánh dấu hoàn thành dựa trên dữ liệu thực tế của gian hàng."
        />
        <Button size="sm" variant="outline" onClick={() => setSimulateComplete((v) => !v)}>
          {simulateComplete ? "Xem trạng thái 1/3" : "Xem trạng thái hoàn tất"}
        </Button>
      </div>

      <SetupGuide
        title="Hướng dẫn thiết lập gian hàng (mẫu)"
        storageKey="nm_design_setup_guide_v1"
        steps={[
          {
            id: "product",
            status: "done",
            title: "Thêm sản phẩm đầu tiên",
            description: "Gian hàng đã có 4 sản phẩm (mẫu) kèm tồn kho SKU.",
            primary: (
              <Button size="sm" asChild>
                <Link href="/design/app?kieu=a3">Thêm sản phẩm</Link>
              </Button>
            ),
            secondary: (
              <Button size="sm" variant="ghost" onClick={() => toast("Mở nhập CSV (mẫu)")}>
                Nhập CSV
              </Button>
            ),
          },
          {
            id: "bank",
            status: simulateComplete ? "done" : "todo",
            title: "Thêm tài khoản ngân hàng nhận tiền",
            description: "Xác minh OTP tài khoản nhận đối soát VietQR và tiền thu hộ COD định kỳ.",
            badge: simulateComplete ? undefined : { tone: "attention", label: "Chờ duyệt" },
            primary: (
              <Button size="sm" onClick={() => toast("Đi tới cài đặt tài khoản ngân hàng (mẫu)")}>
                Thêm tài khoản
              </Button>
            ),
            image: {
              src: "/banners/vietnam-workshop.jpg",
              alt: "Minh hoạ gian hàng NexusMart",
            },
          },
          {
            id: "site",
            status: simulateComplete ? "done" : "todo",
            title: "Chọn mẫu và xuất bản site bán hàng",
            description: "Chọn 1 trong 8 mẫu giao diện và xuất bản để mở địa chỉ mua hàng độc lập.",
            primary: (
              <Button size="sm" asChild>
                <Link href="/design/app?kieu=a7">Mở Site Studio</Link>
              </Button>
            ),
          },
          {
            id: "domain",
            status: "unknown",
            optional: true,
            title: "Gắn tên miền riêng (tuỳ chọn)",
            description:
              "Kết nối tên miền thương hiệu của riêng bạn qua bản ghi DNS mà không mất phí.",
            primary: (
              <Button
                size="sm"
                variant="outline"
                onClick={() => toast("Mở cấu hình tên miền (mẫu)")}
              >
                Thêm tên miền
              </Button>
            ),
          },
        ]}
      />
    </div>
  );
}

export function DesignAppClient({ kieu }: { kieu: DesignAppPattern }) {
  const mobileNav = (
    <nav aria-label="Điều hướng nhanh di động" className="grid grid-cols-5">
      {[
        { href: "/design/app?kieu=a9", label: "Tổng quan", icon: Home, active: kieu === "a9" },
        {
          href: "/design/app?kieu=a2",
          label: "Đơn hàng",
          icon: ShoppingBag,
          active: kieu === "a2",
        },
        {
          href: "/design/app?kieu=a1",
          label: "Sản phẩm",
          icon: Package,
          active: kieu === "a1" || kieu === "a3",
        },
        {
          href: "/design/app?kieu=a6",
          label: "Tin nhắn",
          icon: MessageSquare,
          active: kieu === "a6",
        },
        {
          href: "/design/app?kieu=a7",
          label: "Site",
          icon: BarChart3,
          active: kieu === "a7" || kieu === "a8" || kieu === "a4" || kieu === "a5",
        },
      ].map((item) => {
        const Icon = item.icon;
        return (
          <Link
            key={item.label}
            href={item.href}
            className={cn(
              "flex min-h-14 flex-col items-center justify-center gap-0.5 px-1 py-1.5 text-[11px] font-medium",
              item.active ? "text-brand" : "text-muted-foreground"
            )}
          >
            <Icon className="size-4 shrink-0" aria-hidden="true" />
            <span className="truncate">{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );

  return (
    <AppShell
      area="seller"
      areaLabel="Người bán"
      homeHref="/design/app"
      navGroups={SELLER_NAV_GROUPS}
      commandItems={COMMAND_ITEMS}
      account={{
        name: SAMPLE_SHOP_NAME,
        subtitle: "aodep.nexusmart.vn (mẫu)",
        menu: [
          { label: "Xem site gian hàng mẫu", href: "/design/site" },
          { label: "Hồ sơ gian hàng (mẫu)", href: "/design/app?kieu=a5" },
          { label: "Về mục lục /design", href: "/design" },
          { label: "Đăng xuất (mẫu)", destructive: true, onSelect: () => toast("Đăng xuất mẫu") },
        ],
      }}
      storageKey="nm_design_sidebar_v1"
      mobileNav={mobileNav}
    >
      {/* Quick pattern switcher bar */}
      <div className="mb-5 flex flex-wrap items-center gap-1.5 rounded-xl bg-card p-2 shadow-card">
        <Button size="xs" variant="ghost" asChild>
          <Link href="/design">← /design</Link>
        </Button>
        <Separator orientation="vertical" className="h-4" />
        <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto pb-0.5">
          {PATTERN_LINKS.map((item) => (
            <Button
              key={item.key}
              size="xs"
              variant={kieu === item.key ? "default" : "ghost"}
              asChild
            >
              <Link href={`/design/app?kieu=${item.key}`}>{item.label}</Link>
            </Button>
          ))}
        </div>
      </div>

      {kieu === "a1" ? <PatternA1 /> : null}
      {kieu === "a2" ? <PatternA2 /> : null}
      {kieu === "a3" ? <PatternA3 /> : null}
      {kieu === "a4" ? <PatternA4 /> : null}
      {kieu === "a5" ? <PatternA5 /> : null}
      {kieu === "a6" ? <PatternA6 /> : null}
      {kieu === "a7" ? <PatternA7 /> : null}
      {kieu === "a8" ? <PatternA8 /> : null}
      {kieu === "a9" ? <PatternA9 /> : null}
    </AppShell>
  );
}
