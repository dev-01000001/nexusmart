"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { CheckCircle2, ChevronDown, Minus, Plus, Search, ShoppingBag, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { AuthSplit, Money, StatusBadge } from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
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
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { UiScope } from "@/components/ui/ui-scope";
import { PLATFORM_TEMPLATES } from "@/features/site/templateCatalog";
import { buildSiteUiVars, isDarkHex } from "@/lib/site/theme";
import { BADGE_TONE_SWATCHES, SAMPLE_SITE_PRODUCTS } from "../_samples";

export function DesignSiteClient({ initialTemplateId }: { initialTemplateId: string }) {
  const router = useRouter();
  const [templateId, setTemplateId] = React.useState(initialTemplateId);
  const [selectedSize, setSelectedSize] = React.useState("m");
  const [qty, setQty] = React.useState(1);
  const [cartQty, setCartQty] = React.useState(2);
  const [payMethod, setPayMethod] = React.useState("cod");
  const [searchOpen, setSearchOpen] = React.useState(false);

  React.useEffect(() => {
    setTemplateId(initialTemplateId);
  }, [initialTemplateId]);

  const template = PLATFORM_TEMPLATES.find((t) => t.id === templateId) ?? PLATFORM_TEMPLATES[0];
  const theme = template.config.theme;
  const siteVars = React.useMemo(() => buildSiteUiVars(theme), [theme]);
  const darkMode = isDarkHex(theme.bg_color);

  const handleSelectTemplate = (nextId: string) => {
    setTemplateId(nextId);
    router.replace(`/design/site?mau=${encodeURIComponent(nextId)}`, { scroll: false });
  };

  return (
    <div className="min-h-svh bg-background text-foreground">
      {/* Control bar outside site preview */}
      <header className="sticky top-0 z-20 border-b border-border bg-card shadow-card">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-3 px-4 py-3 md:px-6">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Link href="/design" className="font-medium text-brand hover:underline">
              ← Về mục lục /design
            </Link>
            <span className="text-muted-foreground">·</span>
            <span className="font-semibold text-foreground">Khung thử Site 8 mẫu (mẫu)</span>
            <Badge variant={darkMode ? "default" : "secondary"}>
              {darkMode ? "Nền tối (dark)" : "Nền sáng (light)"}
            </Badge>
            <Badge variant="outline" className="font-mono text-[11px]">
              font: {theme.font_family} · radius: {theme.radius ?? "none"}
            </Badge>
          </div>

          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">Chọn mẫu:</span>
            <Select value={template.id} onValueChange={handleSelectTemplate}>
              <SelectTrigger className="w-60 bg-card" aria-label="Chọn mẫu giao diện site">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PLATFORM_TEMPLATES.map((tpl) => (
                  <SelectItem key={tpl.id} value={tpl.id}>
                    {tpl.name} ({isDarkHex(tpl.config.theme.bg_color) ? "Tối" : "Sáng"})
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-6 md:px-6">
        {/* Scoped site frame: UiScope + buildSiteUiVars + data-site-theme-mode (NO data-site-root!) */}
        <UiScope
          style={siteVars as React.CSSProperties}
          data-site-theme-mode={darkMode ? "dark" : "light"}
          className="overflow-hidden rounded-2xl border border-border bg-background text-foreground"
        >
          {/* 1. Storefront Header */}
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border bg-card px-4 py-3.5 md:px-6">
            <div className="flex min-w-0 items-center gap-4">
              <span className="truncate text-base font-semibold tracking-tight text-foreground">
                {theme.brand_name} (mẫu)
              </span>
              <nav
                aria-label="Menu site mẫu"
                className="hidden items-center gap-4 text-sm text-muted-foreground md:flex"
              >
                <span className="font-medium text-foreground">Trang chủ</span>
                <span>Sản phẩm</span>
                <span>Ưu đãi</span>
                <span>Giới thiệu</span>
              </nav>
            </div>
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setSearchOpen(true)}
                className="shadow-none"
              >
                <Search className="size-4" aria-hidden="true" />
                <span className="hidden sm:inline">Tìm sản phẩm...</span>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="shadow-none"
                aria-label="Giỏ hàng 2 sản phẩm"
                onClick={() => toast("Mở giỏ hàng mẫu")}
              >
                <ShoppingBag className="size-4" aria-hidden="true" />
                <Badge variant="default" className="px-1.5 py-0 text-[11px]">
                  {cartQty}
                </Badge>
              </Button>
            </div>
          </div>

          <div className="space-y-8 p-4 md:p-6">
            {/* 2. Portal tests inside site theme scope (critical for sport-active / luxury-maison!) */}
            <section className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card p-3.5">
              <span className="mr-1 text-xs font-medium text-muted-foreground">
                Kiểm portal nhận màu site ({template.id}):
              </span>

              <Sheet>
                <SheetTrigger asChild>
                  <Button size="sm" variant="default">
                    Mở Sheet &quot;Đã thêm vào giỏ&quot;
                  </Button>
                </SheetTrigger>
                <SheetContent side="right">
                  <SheetHeader>
                    <SheetTitle>Đã thêm vào giỏ hàng (mẫu)</SheetTitle>
                    <SheetDescription>{theme.brand_name}</SheetDescription>
                  </SheetHeader>
                  <div className="space-y-3 px-4 text-sm">
                    <div className="flex items-center gap-3 rounded-lg border border-border p-3">
                      <img
                        src="/products/ao-thun-cotton-trang.png"
                        alt="Áo thun cotton trắng"
                        className="size-12 rounded-md object-cover"
                      />
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-medium">Áo thun Cotton 220gsm (mẫu)</div>
                        <div className="text-xs text-muted-foreground">
                          Trắng / {selectedSize.toUpperCase()} × {qty}
                        </div>
                      </div>
                      <Money vnd={199000 * qty} size="sm" />
                    </div>
                  </div>
                  <SheetFooter>
                    <Button
                      className="w-full"
                      onClick={() => toast("Chuyển tới trang thanh toán mẫu")}
                    >
                      Thanh toán ngay
                    </Button>
                  </SheetFooter>
                </SheetContent>
              </Sheet>

              <Dialog>
                <DialogTrigger asChild>
                  <Button size="sm" variant="outline" className="shadow-none">
                    Mở Dialog chọn size
                  </Button>
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Bảng quy đổi kích cỡ (mẫu)</DialogTitle>
                    <DialogDescription>
                      Hộp thoại mở trong UiScope phải giữ đúng nền, chữ và bo góc của mẫu site.
                    </DialogDescription>
                  </DialogHeader>
                  <div className="space-y-1 text-xs text-muted-foreground tabular-nums">
                    <div>Size S: 1m60 – 1m67 · 52 – 60 kg</div>
                    <div>Size M: 1m67 – 1m73 · 60 – 68 kg</div>
                    <div>Size L: 1m73 – 1m79 · 68 – 76 kg</div>
                  </div>
                  <DialogFooter showCloseButton />
                </DialogContent>
              </Dialog>

              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button size="sm" variant="secondary">
                    <span>Sắp xếp</span>
                    <ChevronDown className="size-4" aria-hidden="true" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem>Mới nhất</DropdownMenuItem>
                  <DropdownMenuItem>Giá thấp đến cao</DropdownMenuItem>
                  <DropdownMenuItem>Giá cao đến thấp</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>

              <Popover>
                <PopoverTrigger asChild>
                  <Button size="sm" variant="outline" className="shadow-none">
                    Popover
                  </Button>
                </PopoverTrigger>
                <PopoverContent className="text-xs">
                  Miễn phí đổi size tận nơi trong 45 ngày (mẫu).
                </PopoverContent>
              </Popover>

              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button size="sm" variant="ghost">
                      Tooltip
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>Tooltip nhận màu từ scope của site mẫu.</TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </section>

            {/* 3. 4 Sample Product Cards */}
            <section className="space-y-3">
              <div className="flex items-baseline justify-between gap-2">
                <h2 className="text-lg font-semibold text-foreground">Sản phẩm nổi bật (mẫu)</h2>
                <span className="text-xs text-muted-foreground">{theme.tagline}</span>
              </div>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                {SAMPLE_SITE_PRODUCTS.map((item) => (
                  <div
                    key={item.id}
                    className="flex flex-col overflow-hidden rounded-lg border border-border bg-card"
                  >
                    <div className="relative aspect-[3/4] overflow-hidden bg-muted">
                      <img
                        src={item.image}
                        alt={item.name}
                        className="h-full w-full object-cover object-top"
                      />
                      <Badge
                        variant="default"
                        className="absolute top-2.5 left-2.5 rounded-sm px-2 py-0.5 text-[11px]"
                      >
                        {item.discount}
                      </Badge>
                    </div>
                    <div className="flex flex-1 flex-col justify-between gap-2 p-3">
                      <div className="line-clamp-2 text-sm font-medium text-foreground">
                        {item.name}
                      </div>
                      <Money vnd={item.priceVnd} compareAtVnd={item.compareAtVnd} size="sm" />
                    </div>
                  </div>
                ))}
              </div>
            </section>

            {/* 4. PDP Buy Box + Cart Line + Checkout Summary */}
            <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
              {/* PDP Buy Box */}
              <Card className="border border-border shadow-none">
                <CardHeader>
                  <CardTitle>Khối mua hàng PDP (mẫu)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <div>
                    <div className="text-base font-semibold text-foreground">
                      Áo thun Cotton Compact 220gsm (mẫu)
                    </div>
                    <div className="mt-1">
                      <Money vnd={199000} compareAtVnd={259000} size="lg" />
                    </div>
                  </div>

                  <div className="space-y-2">
                    <span className="text-xs font-medium text-muted-foreground">
                      Chọn kích cỡ (XL hết hàng):
                    </span>
                    <ToggleGroup
                      type="single"
                      variant="outline"
                      value={selectedSize}
                      onValueChange={(v) => {
                        if (v) setSelectedSize(v);
                      }}
                      className="flex flex-wrap justify-start gap-1.5"
                    >
                      <ToggleGroupItem value="s" className="min-w-10">
                        S
                      </ToggleGroupItem>
                      <ToggleGroupItem value="m" className="min-w-10">
                        M
                      </ToggleGroupItem>
                      <ToggleGroupItem value="l" className="min-w-10">
                        L
                      </ToggleGroupItem>
                      <ToggleGroupItem
                        value="xl"
                        disabled
                        className="min-w-10 line-through opacity-50"
                      >
                        XL
                      </ToggleGroupItem>
                    </ToggleGroup>
                  </div>

                  <div className="flex items-center gap-3">
                    <span className="text-xs font-medium text-muted-foreground">Số lượng:</span>
                    <div className="inline-flex items-center rounded-md border border-border">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Giảm số lượng"
                        onClick={() => setQty((q) => Math.max(1, q - 1))}
                      >
                        <Minus className="size-3.5" aria-hidden="true" />
                      </Button>
                      <span className="w-8 text-center text-sm font-medium tabular-nums">
                        {qty}
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Tăng số lượng"
                        onClick={() => setQty((q) => q + 1)}
                      >
                        <Plus className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  </div>
                </CardContent>
                <CardFooter className="flex flex-col gap-2 sm:flex-row">
                  <Button
                    className="w-full flex-1 shadow-none"
                    onClick={() => toast.success("Đã thêm vào giỏ (mẫu)")}
                  >
                    Thêm vào giỏ
                  </Button>
                  <Button
                    variant="outline"
                    className="w-full flex-1 shadow-none"
                    onClick={() => toast("Mua ngay (mẫu)")}
                  >
                    Mua ngay
                  </Button>
                </CardFooter>
              </Card>

              {/* Cart Line */}
              <Card className="border border-border shadow-none">
                <CardHeader>
                  <CardTitle>Dòng giỏ hàng (mẫu)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <div className="flex items-start gap-3">
                    <img
                      src="/products/ao-polo-det-kim-den.png"
                      alt="Áo polo dệt kim đen"
                      className="size-16 shrink-0 rounded-md border border-border bg-muted object-cover"
                    />
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <div className="line-clamp-2 text-sm font-medium text-foreground">
                        Áo polo dệt kim cổ bẻ (mẫu)
                      </div>
                      <Badge variant="secondary" className="rounded-sm">
                        Đen / L
                      </Badge>
                      <div className="flex items-center justify-between pt-1">
                        <div className="inline-flex items-center rounded-md border border-border">
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            aria-label="Giảm số lượng trong giỏ"
                            onClick={() => setCartQty((q) => Math.max(1, q - 1))}
                          >
                            <Minus className="size-3" aria-hidden="true" />
                          </Button>
                          <span className="w-7 text-center text-xs tabular-nums">{cartQty}</span>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            aria-label="Tăng số lượng trong giỏ"
                            onClick={() => setCartQty((q) => q + 1)}
                          >
                            <Plus className="size-3" aria-hidden="true" />
                          </Button>
                        </div>
                        <Money vnd={289000 * cartQty} size="sm" />
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      aria-label="Xoá khỏi giỏ hàng"
                      onClick={() => toast("Đã xoá sản phẩm mẫu khỏi giỏ")}
                    >
                      <Trash2 className="size-3.5 text-destructive" aria-hidden="true" />
                    </Button>
                  </div>

                  <Separator />

                  <div className="space-y-2">
                    <div className="text-xs font-medium text-muted-foreground">
                      6 tông nhãn trên nền {darkMode ? "tối" : "sáng"}:
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {BADGE_TONE_SWATCHES.map((sw) => (
                        <StatusBadge key={sw.tone} tone={sw.tone} icon={CheckCircle2}>
                          {sw.tone}
                        </StatusBadge>
                      ))}
                    </div>
                  </div>
                </CardContent>
              </Card>

              {/* Checkout Summary + COD / VietQR RadioGroup */}
              <Card className="border border-border shadow-none">
                <CardHeader>
                  <CardTitle>Tóm tắt thanh toán (mẫu)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <RadioGroup value={payMethod} onValueChange={setPayMethod} className="space-y-2">
                    <label className="flex cursor-pointer items-center gap-2.5 rounded-lg border border-border p-3 text-sm">
                      <RadioGroupItem value="cod" id="site-cod" />
                      <span>Thanh toán khi nhận hàng (COD)</span>
                    </label>
                    <label className="flex cursor-pointer items-center gap-2.5 rounded-lg border border-border p-3 text-sm">
                      <RadioGroupItem value="vietqr" id="site-vietqr" />
                      <span>Chuyển khoản nhanh VietQR</span>
                    </label>
                  </RadioGroup>

                  <div className="space-y-1.5 text-sm tabular-nums">
                    <div className="flex justify-between text-muted-foreground">
                      <span>Tạm tính</span>
                      <span>
                        <Money vnd={289000 * cartQty} size="sm" />
                      </span>
                    </div>
                    <div className="flex justify-between text-muted-foreground">
                      <span>Phí giao hàng (GHN)</span>
                      <span>30.000₫</span>
                    </div>
                    <Separator />
                    <div className="flex justify-between font-semibold text-foreground">
                      <span>Tổng thanh toán</span>
                      <Money vnd={289000 * cartQty + 30000} size="md" />
                    </div>
                  </div>
                </CardContent>
                <CardFooter>
                  <Button
                    className="w-full shadow-none"
                    onClick={() => toast.success("Đặt hàng mẫu thành công")}
                  >
                    Đặt hàng ({payMethod.toUpperCase()})
                  </Button>
                </CardFooter>
              </Card>
            </section>

            {/* 5. AuthSplit embedded inheriting site theme */}
            <section className="space-y-3">
              <h2 className="text-base font-semibold text-foreground">
                Đăng nhập trong khung site (AuthSplit embedded · mẫu)
              </h2>
              <AuthSplit
                variant="embedded"
                brand={{ name: `${theme.brand_name} (mẫu)`, href: "/design/site" }}
                title="Đăng nhập tài khoản mua hàng"
                subtitle="Theo dõi đơn hàng, sổ địa chỉ và mã giảm giá của cửa hàng."
                aside={{
                  imageSrc: theme.banner_url || "/banners/campaign-everyday.jpg",
                  caption: theme.tagline || "Mua hàng trực tiếp tại gian hàng chính hãng.",
                }}
              >
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    toast("Đăng nhập mẫu trên site");
                  }}
                  className="space-y-3.5"
                >
                  <Field>
                    <FieldLabel htmlFor="site-login-email">Email</FieldLabel>
                    <Input id="site-login-email" type="email" defaultValue="khachhang@example.vn" />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="site-login-pw">Mật khẩu</FieldLabel>
                    <Input id="site-login-pw" type="password" defaultValue="********" />
                  </Field>
                  <Button type="submit" className="w-full shadow-none">
                    Đăng nhập (mẫu)
                  </Button>
                </form>
              </AuthSplit>
            </section>
          </div>

          {/* Search CommandDialog INSIDE site UiScope so portal inherits dark/light site vars */}
          <CommandDialog
            open={searchOpen}
            onOpenChange={setSearchOpen}
            title={`Tìm sản phẩm tại ${theme.brand_name}`}
            description="Nhập tên sản phẩm hoặc chất liệu muốn tìm."
          >
            <CommandInput placeholder="Tìm áo thun, áo polo, quần jogger..." />
            <CommandList>
              <CommandEmpty>Không có sản phẩm mẫu phù hợp.</CommandEmpty>
              <CommandGroup heading="Sản phẩm mẫu tại cửa hàng">
                {SAMPLE_SITE_PRODUCTS.map((p) => (
                  <CommandItem
                    key={p.id}
                    onSelect={() => {
                      setSearchOpen(false);
                      toast(`Đã chọn ${p.name}`);
                    }}
                  >
                    <span>{p.name}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </CommandDialog>
        </UiScope>
      </main>
    </div>
  );
}
