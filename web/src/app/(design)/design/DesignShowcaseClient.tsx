"use client";

import * as React from "react";
import Link from "next/link";
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  ChevronDown,
  Clock,
  Eye,
  EyeOff,
  Inbox,
  Info,
  Layers,
  LayoutDashboard,
  Lock,
  MoreHorizontal,
  Package,
  Palette,
  Search,
  Sparkles,
  Store,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { NexusMark } from "@/components/Icons";
import {
  BarChart,
  EmptyState,
  ErrorState,
  MetricStrip,
  Money,
  StatusBadge,
  Timeline,
} from "@/components/app";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
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
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Progress } from "@/components/ui/progress";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { ScrollArea } from "@/components/ui/scroll-area";
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
  SheetTrigger,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
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
import { Toggle } from "@/components/ui/toggle";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import {
  APP_PALETTE_SWATCHES,
  BADGE_TONE_SWATCHES,
  SAMPLE_ANALYTICS_LABELS,
  SAMPLE_ORDERS_VALUES,
  TYPE_SCALE_SAMPLES,
} from "./_samples";

export function DesignShowcaseClient() {
  const [commandOpen, setCommandOpen] = React.useState(false);
  const [showPassword, setShowPassword] = React.useState(false);
  const [collapsibleOpen, setCollapsibleOpen] = React.useState(false);
  const [switchChecked, setSwitchChecked] = React.useState(true);
  const [radioValue, setRadioValue] = React.useState("cod");
  const [selectValue, setSelectValue] = React.useState("ao-polo");
  const [toggleGroupValue, setToggleGroupValue] = React.useState("m");

  return (
    <div className="min-h-svh bg-background text-foreground">
      {/* Top navigation bar */}
      <header className="sticky top-0 z-20 border-b border-white/10 bg-topbar text-topbar-foreground">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-3 px-4 py-3 md:px-6">
          <div className="flex min-w-0 items-center gap-2.5">
            <NexusMark size={20} accentColor="var(--ui-brand)" className="shrink-0 text-white" />
            <span className="font-semibold text-white">NexusMart</span>
            <Badge variant="secondary" className="rounded-md bg-topbar-input text-white">
              Hệ thống thiết kế (nội bộ · mẫu)
            </Badge>
          </div>
          <nav aria-label="Mục lục trang duyệt" className="flex flex-wrap items-center gap-1.5">
            <Button size="sm" variant="secondary" asChild>
              <Link href="/design">
                <Palette className="size-4" aria-hidden="true" />
                <span>Nền tảng & Component</span>
              </Link>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              asChild
              className="text-topbar-foreground hover:bg-topbar-input hover:text-white"
            >
              <Link href="/design/app">
                <LayoutDashboard className="size-4" aria-hidden="true" />
                <span>AppShell & A1–A9</span>
              </Link>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              asChild
              className="text-topbar-foreground hover:bg-topbar-input hover:text-white"
            >
              <Link href="/design/auth">
                <Lock className="size-4" aria-hidden="true" />
                <span>AuthSplit</span>
              </Link>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              asChild
              className="text-topbar-foreground hover:bg-topbar-input hover:text-white"
            >
              <Link href="/design/site">
                <Store className="size-4" aria-hidden="true" />
                <span>Site 8 mẫu</span>
              </Link>
            </Button>
          </nav>
        </div>
      </header>

      <main className="mx-auto flex max-w-6xl flex-col gap-8 px-4 py-6 md:px-6 md:py-8">
        <section className="space-y-2">
          <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground">
            Trang duyệt Phase 0 — Nền tảng shadcn/ui + Tailwind v4 (mẫu)
          </h1>
          <p className="max-w-3xl text-sm text-muted-foreground">
            Toàn bộ dữ liệu hiển thị ở cụm tuyến <code className="font-mono">/design</code> là dữ
            liệu mẫu dùng để nghiệm thu bảng màu <code className="font-mono">--ui-*</code>, kiểu chữ
            Inter, component shadcn/ui, portal Radix trong{" "}
            <code className="font-mono">UiScope</code> và 9 kiểu trang ứng dụng.
          </p>
        </section>

        {/* 1. NỀN TẢNG: MÀU §6.1 */}
        <section aria-labelledby="sec-palette" className="space-y-4">
          <h2 id="sec-palette" className="text-base font-semibold text-foreground">
            1. Nền tảng — Bảng màu ứng dụng (§6.1)
          </h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {APP_PALETTE_SWATCHES.map((sw) => (
              <div
                key={sw.token}
                className="flex items-center gap-3 rounded-xl bg-card p-3 shadow-card"
              >
                <span
                  aria-hidden="true"
                  className="size-11 shrink-0 rounded-lg border border-border"
                  style={{ backgroundColor: sw.hex }}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <code className="truncate text-xs font-semibold text-foreground">
                      {sw.token}
                    </code>
                    <span className="shrink-0 font-mono text-xs text-muted-foreground">
                      {sw.hex}
                    </span>
                  </div>
                  <p className="truncate text-xs text-muted-foreground">{sw.usage}</p>
                  <p className="text-[11px] text-muted-foreground tabular-nums">
                    Tương phản: {sw.contrast}
                  </p>
                </div>
              </div>
            ))}
          </div>

          <Card>
            <CardHeader>
              <CardTitle>6 tông nhãn trạng thái (sáng & tối cho site)</CardTitle>
              <CardDescription>
                Đo tương phản WCAG AA trên cả nền sáng ứng dụng và 2 mẫu site tối.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                {BADGE_TONE_SWATCHES.map((item) => (
                  <StatusBadge key={item.tone} tone={item.tone} icon={CheckCircle2}>
                    {item.label}
                  </StatusBadge>
                ))}
              </div>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
                {BADGE_TONE_SWATCHES.map((item) => (
                  <div
                    key={`meta-${item.tone}`}
                    className="rounded-lg border border-border p-2.5 text-xs"
                  >
                    <div className="font-semibold text-foreground">{item.tone}</div>
                    <div className="mt-1 text-muted-foreground tabular-nums">
                      Sáng: {item.lightBg} / {item.lightInk} ({item.lightContrast})
                    </div>
                    <div className="text-muted-foreground tabular-nums">
                      Tối: {item.darkBg} / {item.darkInk} ({item.darkContrast})
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </section>

        {/* 2. THANG CHỮ, BO GÓC, BÓNG §6.2–§6.3 */}
        <section aria-labelledby="sec-type" className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle id="sec-type">Thang chữ Inter (§6.2)</CardTitle>
              <CardDescription>Chữ thân 14px đủ thoáng cho dấu tiếng Việt.</CardDescription>
            </CardHeader>
            <CardContent className="divide-y divide-border">
              {TYPE_SCALE_SAMPLES.map((row) => (
                <div key={row.role} className="space-y-1 py-3 first:pt-0 last:pb-0">
                  <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
                    <span>{row.role}</span>
                    <span className="font-mono">{row.spec}</span>
                  </div>
                  <div className={row.className}>{row.sample}</div>
                </div>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Bo góc & Bóng phần ứng dụng (§6.3)</CardTitle>
              <CardDescription>
                Site gian hàng giữ phẳng; phần ứng dụng dùng 3 bậc bóng nhẹ.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-5">
              <div className="space-y-2">
                <div className="text-xs font-medium text-muted-foreground">
                  Bo góc (--ui-radius)
                </div>
                <div className="flex flex-wrap items-center gap-3">
                  <div className="flex h-12 w-24 items-center justify-center rounded-sm border border-border bg-muted text-xs">
                    rounded-sm
                  </div>
                  <div className="flex h-12 w-24 items-center justify-center rounded-md border border-border bg-muted text-xs">
                    rounded-md
                  </div>
                  <div className="flex h-12 w-24 items-center justify-center rounded-lg border border-border bg-muted text-xs">
                    rounded-lg
                  </div>
                  <div className="flex h-12 w-24 items-center justify-center rounded-xl border border-border bg-muted text-xs">
                    rounded-xl
                  </div>
                  <div className="flex h-10 items-center justify-center rounded-full border border-border bg-muted px-4 text-xs">
                    rounded-full
                  </div>
                </div>
              </div>

              <Separator />

              <div className="space-y-2">
                <div className="text-xs font-medium text-muted-foreground">3 bậc bóng</div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <div className="rounded-xl bg-card p-4 text-center text-xs shadow-card">
                    shadow-card
                  </div>
                  <div className="rounded-md bg-primary p-4 text-center text-xs text-primary-foreground shadow-button">
                    shadow-button
                  </div>
                  <div className="rounded-xl bg-popover p-4 text-center text-xs shadow-overlay">
                    shadow-overlay
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        </section>

        {/* 3. BUTTONS, BADGES, ALERTS */}
        <section aria-labelledby="sec-buttons" className="space-y-4">
          <h2 id="sec-buttons" className="text-base font-semibold text-foreground">
            2. Component cơ bản & Trạng thái (§7)
          </h2>

          <Card>
            <CardHeader>
              <CardTitle>Button & Biến thể</CardTitle>
              <CardDescription>
                Di chuột để kiểm trạng thái hover; bấm Tab để kiểm vòng focus teal (--ui-ring).
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="default">Thêm sản phẩm</Button>
                <Button variant="outline">Xử lý đơn</Button>
                <Button variant="secondary">Nhập CSV</Button>
                <Button variant="ghost">Bỏ qua</Button>
                <Button variant="link">Xem hướng dẫn</Button>
                <Button variant="destructive">
                  <Trash2 className="size-4" aria-hidden="true" />
                  <span>Xoá mẫu</span>
                </Button>
                <Button disabled>Đang khoá (disabled)</Button>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button size="sm">Cỡ nhỏ (sm)</Button>
                <Button size="default">Mặc định (default)</Button>
                <Button size="lg">Cỡ lớn (lg)</Button>
                <Button size="icon" variant="outline" aria-label="Tìm kiếm nhanh">
                  <Search className="size-4" aria-hidden="true" />
                </Button>
              </div>
              <Separator />
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="default">Mặc định</Badge>
                <Badge variant="secondary" className="rounded-md">
                  Đen / L / Cotton (mẫu)
                </Badge>
                <Badge variant="outline">249.000₫</Badge>
                <Badge variant="destructive">Hết hàng</Badge>
                <Money vnd={199000} compareAtVnd={259000} size="md" />
              </div>
            </CardContent>
          </Card>

          {/* Inputs & Form Controls */}
          <Card>
            <CardHeader>
              <CardTitle>Ô nhập, Chọn, Checkbox, Radio, Switch, Toggle</CardTitle>
              <CardDescription>
                Gồm đủ trạng thái bình thường, vô hiệu hoá và lỗi xác thực (aria-invalid).
              </CardDescription>
            </CardHeader>
            <CardContent className="grid grid-cols-1 gap-6 md:grid-cols-2">
              <FieldGroup className="gap-4">
                <Field>
                  <FieldLabel htmlFor="design-email">Email liên hệ (mẫu)</FieldLabel>
                  <Input id="design-email" type="email" defaultValue="chu-shop@nexusmart.vn" />
                  <FieldDescription>Nhận thông báo đơn hàng và đối soát.</FieldDescription>
                </Field>

                <Field>
                  <FieldLabel htmlFor="design-password">Mật khẩu (có nút hiện/ẩn)</FieldLabel>
                  <div className="relative">
                    <Input
                      id="design-password"
                      type={showPassword ? "text" : "password"}
                      defaultValue="NexusMart@2026"
                      className="pr-10"
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => setShowPassword((v) => !v)}
                      aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
                      className="absolute top-0.5 right-1"
                    >
                      {showPassword ? (
                        <EyeOff className="size-4" aria-hidden="true" />
                      ) : (
                        <Eye className="size-4" aria-hidden="true" />
                      )}
                    </Button>
                  </div>
                </Field>

                <Field data-invalid="true">
                  <FieldLabel htmlFor="design-error-input">
                    Tên miền phụ (trạng thái lỗi)
                  </FieldLabel>
                  <Input id="design-error-input" aria-invalid="true" defaultValue="admin" />
                  <FieldError>
                    Tên miền phụ &quot;admin&quot; thuộc danh sách hệ thống bảo vệ.
                  </FieldError>
                </Field>

                <Field>
                  <FieldLabel htmlFor="design-disabled">Mã gian hàng (disabled)</FieldLabel>
                  <Input id="design-disabled" disabled value="SHOP-VN-0042 (mẫu)" />
                </Field>

                <Field>
                  <FieldLabel htmlFor="design-note">Ghi chú sản phẩm</FieldLabel>
                  <Textarea
                    id="design-note"
                    rows={2}
                    placeholder="Nhập mô tả chất liệu vải và hướng dẫn bảo quản..."
                  />
                </Field>
              </FieldGroup>

              <div className="flex flex-col gap-5">
                <Field>
                  <FieldLabel>Danh mục sản phẩm (Select)</FieldLabel>
                  <Select value={selectValue} onValueChange={setSelectValue}>
                    <SelectTrigger className="w-full bg-card">
                      <SelectValue placeholder="Chọn danh mục" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="ao-polo">Áo polo nam/nữ (mẫu)</SelectItem>
                      <SelectItem value="ao-thun">Áo thun cotton 220gsm (mẫu)</SelectItem>
                      <SelectItem value="quan-short">Quần short linen (mẫu)</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>

                <div className="space-y-2">
                  <Label>Checkbox & Switch</Label>
                  <div className="flex flex-wrap items-center gap-4">
                    <label className="inline-flex items-center gap-2 text-sm">
                      <Checkbox defaultChecked />
                      <span>Cho phép đồng kiểm COD</span>
                    </label>
                    <label className="inline-flex items-center gap-2 text-sm text-muted-foreground">
                      <Checkbox disabled />
                      <span>Đã khoá</span>
                    </label>
                    <label className="inline-flex items-center gap-2 text-sm">
                      <Switch checked={switchChecked} onCheckedChange={setSwitchChecked} />
                      <span>Hiển thị trên site</span>
                    </label>
                  </div>
                </div>

                <div className="space-y-2">
                  <Label>RadioGroup (Phương thức thanh toán mẫu)</Label>
                  <RadioGroup value={radioValue} onValueChange={setRadioValue} className="gap-2">
                    <label className="flex items-center gap-2 text-sm">
                      <RadioGroupItem value="cod" id="r-cod" />
                      <span>Thanh toán khi nhận hàng (COD)</span>
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <RadioGroupItem value="vietqr" id="r-qr" />
                      <span>Chuyển khoản VietQR</span>
                    </label>
                  </RadioGroup>
                </div>

                <div className="space-y-2">
                  <Label>Toggle & ToggleGroup (Chọn kích cỡ)</Label>
                  <div className="flex flex-wrap items-center gap-3">
                    <Toggle variant="outline" aria-label="Bật lọc nhanh">
                      <Sparkles className="size-4" aria-hidden="true" />
                      <span>Nổi bật</span>
                    </Toggle>
                    <ToggleGroup
                      type="single"
                      variant="outline"
                      value={toggleGroupValue}
                      onValueChange={(v) => {
                        if (v) setToggleGroupValue(v);
                      }}
                    >
                      <ToggleGroupItem value="s">S</ToggleGroupItem>
                      <ToggleGroupItem value="m">M</ToggleGroupItem>
                      <ToggleGroupItem value="l">L</ToggleGroupItem>
                      <ToggleGroupItem value="xl" disabled>
                        XL (hết)
                      </ToggleGroupItem>
                    </ToggleGroup>
                  </div>
                </div>

                <div className="space-y-2">
                  <div className="flex items-center justify-between text-xs text-muted-foreground">
                    <span>Tiến độ Flash Sale (mẫu)</span>
                    <span className="tabular-nums">68%</span>
                  </div>
                  <Progress value={68} aria-label="Tiến độ bán Flash Sale" />
                </div>
              </div>
            </CardContent>
          </Card>

          {/* Overlays & Portal triggers */}
          <Card>
            <CardHeader>
              <CardTitle>Hộp thoại, Sheet, Menu, Popover, Tooltip, Command & Toast</CardTitle>
              <CardDescription>Mọi portal Radix đều gắn vào container của UiScope.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap items-center gap-2">
              <Dialog>
                <DialogTrigger asChild>
                  <Button variant="outline">Mở Dialog</Button>
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Nhập sản phẩm từ tệp CSV (mẫu)</DialogTitle>
                    <DialogDescription>
                      Kiểm tra lại cột mã SKU, giá VND nguyên và số lượng tồn trước khi tải lên.
                    </DialogDescription>
                  </DialogHeader>
                  <Input type="text" defaultValue="danh-sach-san-pham-mau.csv" readOnly />
                  <DialogFooter showCloseButton>
                    <Button
                      type="button"
                      onClick={() => toast.success("Đã kiểm tra tệp CSV (mẫu)")}
                    >
                      Xác nhận nhập
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>

              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="outline">Mở AlertDialog</Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Khôi phục phiên bản site #3 (mẫu)?</AlertDialogTitle>
                    <AlertDialogDescription>
                      Bản nháp hiện tại sẽ được thay bằng cấu hình đã xuất bản ngày 24/11/2026.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Huỷ</AlertDialogCancel>
                    <AlertDialogAction onClick={() => toast("Đã khôi phục phiên bản mẫu #3")}>
                      Khôi phục
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>

              <Sheet>
                <SheetTrigger asChild>
                  <Button variant="outline">Mở Sheet phải</Button>
                </SheetTrigger>
                <SheetContent side="right">
                  <SheetHeader>
                    <SheetTitle>Xem nhanh đơn #NM-10293 (mẫu)</SheetTitle>
                    <SheetDescription>Nguyễn Minh Hoàng · 2 sản phẩm · COD</SheetDescription>
                  </SheetHeader>
                  <div className="space-y-3 px-4 text-sm">
                    <div className="flex items-center justify-between">
                      <span className="text-muted-foreground">Trạng thái</span>
                      <StatusBadge tone="attention" icon={Clock}>
                        Đã xác nhận
                      </StatusBadge>
                    </div>
                    <div className="flex items-center justify-between">
                      <span className="text-muted-foreground">Tổng thu hộ</span>
                      <Money vnd={518000} />
                    </div>
                  </div>
                  <SheetFooter>
                    <Button type="button" onClick={() => toast.success("Đã tạo vận đơn GHN (mẫu)")}>
                      Tạo vận đơn
                    </Button>
                  </SheetFooter>
                </SheetContent>
              </Sheet>

              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="secondary">
                    <span>Thêm thao tác</span>
                    <ChevronDown className="size-4" aria-hidden="true" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuLabel>Thao tác đơn hàng (mẫu)</DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={() => toast("Đã tải nhãn in A6 (mẫu)")}>
                    In nhãn vận chuyển A6
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => toast("Đã sao chép mã đơn (mẫu)")}>
                    Sao chép mã đơn
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    variant="destructive"
                    onSelect={() => toast.error("Đã huỷ đơn mẫu")}
                  >
                    Huỷ đơn hàng
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>

              <Popover>
                <PopoverTrigger asChild>
                  <Button variant="outline">Mở Popover</Button>
                </PopoverTrigger>
                <PopoverContent className="space-y-2">
                  <div className="text-sm font-semibold">Bộ lọc nhanh tồn kho (mẫu)</div>
                  <p className="text-xs text-muted-foreground">
                    Lọc các SKU còn dưới 5 sản phẩm để nhập bổ sung kịp thời.
                  </p>
                </PopoverContent>
              </Popover>

              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button variant="outline">Di chuột Tooltip</Button>
                  </TooltipTrigger>
                  <TooltipContent>Số liệu tổng hợp theo múi giờ Việt Nam (UTC+7).</TooltipContent>
                </Tooltip>
              </TooltipProvider>

              <Button variant="outline" onClick={() => setCommandOpen(true)}>
                <Search className="size-4" aria-hidden="true" />
                <span>Mở CommandDialog (⌘K)</span>
              </Button>

              <Button
                variant="default"
                onClick={() =>
                  toast.success("Đã lưu thay đổi sản phẩm (mẫu)", {
                    description: "Tồn kho 3 SKU đã được cập nhật đồng bộ.",
                  })
                }
              >
                <Bell className="size-4" aria-hidden="true" />
                <span>Bắn Toast</span>
              </Button>
            </CardContent>
          </Card>

          {/* Alerts */}
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
            <Alert>
              <Info className="size-4" aria-hidden="true" />
              <AlertTitle>Thông báo thường (mẫu)</AlertTitle>
              <AlertDescription>
                Site gian hàng của bạn đã sẵn sàng nhận đơn thanh toán COD và VietQR.
              </AlertDescription>
            </Alert>
            <Alert variant="destructive">
              <AlertTriangle className="size-4" aria-hidden="true" />
              <AlertTitle>Cảnh báo tồn kho (mẫu)</AlertTitle>
              <AlertDescription>
                Có 2 biến thể áo polo dệt kim đã về 0, khách sẽ không thể thêm vào giỏ.
              </AlertDescription>
            </Alert>
            <Alert variant="inverse">
              <Sparkles className="size-4" aria-hidden="true" />
              <AlertTitle>Site đang ở bản nháp (mẫu)</AlertTitle>
              <AlertDescription>
                Xuất bản site trong Site Studio để mở địa chỉ mua hàng cho khách.
              </AlertDescription>
            </Alert>
          </div>

          {/* Navigation, Disclosure, CardAction, Table, Skeleton */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>Tabs, Accordion, Collapsible & ScrollArea</CardTitle>
                <CardAction>
                  <Button variant="ghost" size="icon-sm" aria-label="Tuỳ chọn thẻ">
                    <MoreHorizontal className="size-4" aria-hidden="true" />
                  </Button>
                </CardAction>
              </CardHeader>
              <CardContent className="space-y-4">
                <Tabs defaultValue="all">
                  <TabsList>
                    <TabsTrigger value="all">Tất cả</TabsTrigger>
                    <TabsTrigger value="active">Đang bán</TabsTrigger>
                    <TabsTrigger value="draft">Chờ duyệt</TabsTrigger>
                    <TabsTrigger value="hidden">Đã ẩn</TabsTrigger>
                  </TabsList>
                  <TabsContent value="all" className="pt-2 text-xs text-muted-foreground">
                    Hiển thị toàn bộ sản phẩm mẫu trong kho gian hàng.
                  </TabsContent>
                  <TabsContent value="active" className="pt-2 text-xs text-muted-foreground">
                    Chỉ gồm các sản phẩm đã duyệt và còn tồn kho.
                  </TabsContent>
                  <TabsContent value="draft" className="pt-2 text-xs text-muted-foreground">
                    Các sản phẩm đang chờ kiểm duyệt nội dung.
                  </TabsContent>
                  <TabsContent value="hidden" className="pt-2 text-xs text-muted-foreground">
                    Sản phẩm tạm ẩn khỏi site bán hàng.
                  </TabsContent>
                </Tabs>

                <Accordion type="single" collapsible defaultValue="q1">
                  <AccordionItem value="q1">
                    <AccordionTrigger>Chính sách đồng kiểm và đổi size (mẫu)</AccordionTrigger>
                    <AccordionContent className="text-xs text-muted-foreground">
                      Khách hàng được kiểm tra chất vải và kích cỡ trước khi thanh toán COD, hỗ trợ
                      đổi size hai chiều trong 45 ngày.
                    </AccordionContent>
                  </AccordionItem>
                  <AccordionItem value="q2">
                    <AccordionTrigger>Thời gian đối soát VietQR (mẫu)</AccordionTrigger>
                    <AccordionContent className="text-xs text-muted-foreground">
                      Giao dịch VietQR được ghi nhận tức thì vào sổ cái người bán sau khi hoàn tất.
                    </AccordionContent>
                  </AccordionItem>
                </Accordion>

                <Collapsible open={collapsibleOpen} onOpenChange={setCollapsibleOpen}>
                  <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
                    <span className="text-xs font-medium">Chi tiết kỹ thuật đóng gói (mẫu)</span>
                    <CollapsibleTrigger asChild>
                      <Button variant="ghost" size="xs">
                        {collapsibleOpen ? "Thu gọn" : "Mở rộng"}
                      </Button>
                    </CollapsibleTrigger>
                  </div>
                  <CollapsibleContent className="mt-2 rounded-lg bg-muted p-3 text-xs text-muted-foreground">
                    Hộp giấy tái chế 28×20×6cm kèm phiếu đổi size in sẵn mã đơn hàng.
                  </CollapsibleContent>
                </Collapsible>

                <ScrollArea className="h-24 rounded-lg border border-border p-3">
                  <div className="space-y-2 text-xs text-muted-foreground">
                    <p>Bản ghi mẫu #1 — Cập nhật tồn SKU POLO-DEN-M → 24 chiếc.</p>
                    <p>Bản ghi mẫu #2 — Xuất bản giao diện Thời trang tối giản.</p>
                    <p>Bản ghi mẫu #3 — Duyệt tài khoản ngân hàng Vietcombank.</p>
                    <p>Bản ghi mẫu #4 — Kích hoạt mã giảm giá THANG11 (-10%).</p>
                    <p>Bản ghi mẫu #5 — Tạo vận đơn lô 4 đơn hàng GHN.</p>
                  </div>
                </ScrollArea>
              </CardContent>
              <CardFooter className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
                <div className="flex items-center gap-2">
                  <Avatar className="size-7">
                    <AvatarFallback className="bg-brand text-xs text-white">NM</AvatarFallback>
                  </Avatar>
                  <Breadcrumb>
                    <BreadcrumbList>
                      <BreadcrumbItem>
                        <BreadcrumbLink href="/design">Thiết kế</BreadcrumbLink>
                      </BreadcrumbItem>
                      <BreadcrumbSeparator />
                      <BreadcrumbItem>
                        <BreadcrumbPage>Component</BreadcrumbPage>
                      </BreadcrumbItem>
                    </BreadcrumbList>
                  </Breadcrumb>
                </div>
                <Pagination className="mx-0 w-auto">
                  <PaginationContent>
                    <PaginationItem>
                      <PaginationPrevious href="#sec-buttons" />
                    </PaginationItem>
                    <PaginationItem>
                      <PaginationLink href="#sec-buttons" isActive>
                        1
                      </PaginationLink>
                    </PaginationItem>
                    <PaginationItem>
                      <PaginationLink href="#sec-buttons">2</PaginationLink>
                    </PaginationItem>
                    <PaginationItem>
                      <PaginationNext href="#sec-buttons" />
                    </PaginationItem>
                  </PaginationContent>
                </Pagination>
              </CardFooter>
            </Card>

            {/* Table + Skeleton + States */}
            <div className="flex flex-col gap-4">
              <Card>
                <CardHeader>
                  <CardTitle>Bảng dữ liệu & Trạng thái đang tải (Skeleton)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Mã SKU</TableHead>
                        <TableHead>Trạng thái</TableHead>
                        <TableHead className="text-right">Giá bán</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      <TableRow>
                        <TableCell className="font-medium">TEE-TRANG-M (mẫu)</TableCell>
                        <TableCell>
                          <StatusBadge tone="success">Đang bán</StatusBadge>
                        </TableCell>
                        <TableCell className="text-right">
                          <Money vnd={199000} />
                        </TableCell>
                      </TableRow>
                      <TableRow>
                        <TableCell className="font-medium">POLO-DEN-L (mẫu)</TableCell>
                        <TableCell>
                          <StatusBadge tone="warning">Hết hàng</StatusBadge>
                        </TableCell>
                        <TableCell className="text-right">
                          <Money vnd={289000} />
                        </TableCell>
                      </TableRow>
                    </TableBody>
                  </Table>

                  <div className="space-y-2 rounded-lg border border-border p-3">
                    <div className="text-xs font-medium text-muted-foreground">
                      Mẫu đang tải (Skeleton)
                    </div>
                    <div className="flex items-center gap-3">
                      <Skeleton className="size-10 rounded-md" />
                      <div className="flex-1 space-y-1.5">
                        <Skeleton className="h-3.5 w-3/5" />
                        <Skeleton className="h-3 w-2/5" />
                      </div>
                    </div>
                  </div>
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>Biểu đồ cột phẳng (BarChart mẫu)</CardTitle>
                </CardHeader>
                <CardContent>
                  <BarChart
                    labels={SAMPLE_ANALYTICS_LABELS}
                    values={SAMPLE_ORDERS_VALUES}
                    height={160}
                    highlightIndex={5}
                    ariaLabel="Số đơn hàng mẫu 7 ngày qua"
                  />
                </CardContent>
              </Card>
            </div>
          </div>

          {/* App Patterns preview: MetricStrip, EmptyState, ErrorState, Timeline */}
          <MetricStrip
            items={[
              { label: "Sản phẩm đang bán (mẫu)", value: 128, hint: "4 danh mục" },
              { label: "Sắp hết hàng (mẫu)", value: 6, hint: "dưới 5 SKU" },
              { label: "Đơn chờ xác nhận (mẫu)", value: 3 },
            ]}
          />

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
            <EmptyState
              icon={Inbox}
              title="Chưa có đơn hàng nào (mẫu)"
              description="Chia sẻ địa chỉ site gian hàng để nhận đơn hàng đầu tiên."
              action={
                <Button size="sm" onClick={() => toast("Đã sao chép địa chỉ site mẫu")}>
                  <Package className="size-4" aria-hidden="true" />
                  <span>Sao chép link site</span>
                </Button>
              }
            />
            <ErrorState
              description="Kết nối tới dịch vụ đơn hàng đang tạm gián đoạn (trạng thái lỗi mẫu)."
              onRetry={() => toast("Đang thử tải lại dữ liệu mẫu...")}
            />
            <Card>
              <CardHeader>
                <CardTitle>Lịch sử xử lý (Timeline chỉ đọc)</CardTitle>
              </CardHeader>
              <CardContent>
                <Timeline
                  items={[
                    {
                      id: "t-1",
                      at: "10:02 26/11",
                      title: "Xác nhận đơn hàng (mẫu)",
                      description: "Người bán xác nhận đủ tồn kho 2 SKU.",
                    },
                    {
                      id: "t-2",
                      at: "09:48 26/11",
                      title: "Khách đặt hàng COD (mẫu)",
                      description: "Tạo từ site aodep.nexusmart.vn.",
                    },
                  ]}
                />
              </CardContent>
            </Card>
          </div>
        </section>
      </main>

      <CommandDialog
        open={commandOpen}
        onOpenChange={setCommandOpen}
        title="Tìm kiếm nhanh trang duyệt"
        description="Di chuyển giữa các màn hình duyệt hệ thống thiết kế."
      >
        <CommandInput placeholder="Tìm tuyến duyệt thiết kế..." />
        <CommandList>
          <CommandEmpty>Không tìm thấy mục phù hợp.</CommandEmpty>
          <CommandGroup heading="Trang duyệt /design">
            <CommandItem onSelect={() => setCommandOpen(false)}>
              <Layers className="size-4" aria-hidden="true" />
              <span>Nền tảng & Component (/design)</span>
            </CommandItem>
            <CommandItem onSelect={() => setCommandOpen(false)}>
              <LayoutDashboard className="size-4" aria-hidden="true" />
              <span>AppShell & 9 kiểu trang (/design/app)</span>
            </CommandItem>
            <CommandItem onSelect={() => setCommandOpen(false)}>
              <Lock className="size-4" aria-hidden="true" />
              <span>AuthSplit (/design/auth)</span>
            </CommandItem>
            <CommandItem onSelect={() => setCommandOpen(false)}>
              <Store className="size-4" aria-hidden="true" />
              <span>Site gian hàng 8 mẫu (/design/site)</span>
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </div>
  );
}
