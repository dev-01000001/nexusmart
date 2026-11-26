"use client";

import React, { Suspense, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { PackageSearch, Search, Truck } from "lucide-react";
import type { Product } from "@/data/products";
import { api } from "@/lib/api";
import { formatCarrierLabel, formatDateTimeVi, formatVnd } from "@/lib/format";
import {
  ORDER_LOOKUP_NOT_FOUND_MESSAGE,
  type OrderLookupResponse,
  type PublicOrderSummary,
  formatLookupRateLimitMessage,
  formatPublicOrderArea,
  translateOrderLookupError,
} from "@/lib/a8-types";
import {
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
} from "@/features/account/orderMeta";
import type { OrderView } from "@/features/account/orderView";
import {
  SITE_PAGE_CONTAINER,
  orderStatusTone,
  paymentStatusTone,
  splitDateTimeVi,
} from "@/features/account/ui/siteStatus";
import { useSession } from "@/components/SessionProvider";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Timeline, type TimelineItem } from "@/components/app/Timeline";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { focusAndShakeInvalid } from "@/features/commerce/formFeedback";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import { cn } from "@/lib/utils";

export interface OrderLookupViewProps {
  brandName?: string;
  suggestedProducts?: Product[];
}

type LookupResultState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "found"; order: PublicOrderSummary }
  | { kind: "not_found"; message: string }
  | { kind: "rate_limited"; message: string }
  | { kind: "error"; message: string };

function LookupLoadingSkeleton() {
  return (
    <Card className="border border-border shadow-none" aria-busy="true">
      <CardHeader className="space-y-2 border-b border-border pb-4">
        <Skeleton className="h-5 w-44" />
        <Skeleton className="h-4 w-64" />
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Skeleton className="h-20 w-full rounded-lg" />
          <Skeleton className="h-20 w-full rounded-lg" />
          <Skeleton className="h-20 w-full rounded-lg" />
          <Skeleton className="h-20 w-full rounded-lg" />
        </div>
        <Skeleton className="h-16 w-full rounded-lg" />
      </CardContent>
    </Card>
  );
}

function OrderLookupContent({ brandName, suggestedProducts = [] }: OrderLookupViewProps) {
  const viewState = usePageStateParam();
  const { isAuthenticated, user } = useSession();

  const lookupFormRef = useRef<HTMLFormElement | null>(null);
  const [orderCode, setOrderCode] = useState("");
  const [phone, setPhone] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);
  const [lookupState, setLookupState] = useState<LookupResultState>({ kind: "idle" });
  const [recentOrders, setRecentOrders] = useState<OrderView[]>([]);

  const storeLabel = brandName || "cửa hàng";

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(lookupFormRef.current);
    }
  }, [errorPulse]);

  useEffect(() => {
    if (user?.phone && !phone) {
      setPhone(user.phone);
    }
  }, [user?.phone, phone]);

  // Optional convenience chips for already-signed-in buyers (public lookup still works without login)
  useEffect(() => {
    if (!isAuthenticated) {
      setRecentOrders([]);
      return;
    }
    let active = true;
    api<{ items?: OrderView[] }>("/api/orders?page=1&page_size=6", { retryOn401: false })
      .then((res) => {
        if (active && Array.isArray(res?.items)) {
          setRecentOrders(res.items.slice(0, 4));
        }
      })
      .catch(() => {
        // Non-blocking
      });
    return () => {
      active = false;
    };
  }, [isAuthenticated]);

  const runLookup = async (code: string, recipientPhone: string) => {
    setLookupState({ kind: "loading" });
    try {
      const res = await fetch("/api/orders/lookup", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          order_code: code,
          phone: recipientPhone,
        }),
      });

      if (res.status === 404) {
        setLookupState({
          kind: "not_found",
          message: ORDER_LOOKUP_NOT_FOUND_MESSAGE,
        });
        return;
      }

      if (res.status === 429) {
        setLookupState({
          kind: "rate_limited",
          message: formatLookupRateLimitMessage(res.headers.get("retry-after")),
        });
        return;
      }

      if (!res.ok) {
        let errorCode: string | null = null;
        try {
          const errBody = (await res.json()) as { error?: { code?: string } } | null;
          if (typeof errBody?.error?.code === "string") {
            errorCode = errBody.error.code;
          }
        } catch {
          // Keep status-based Vietnamese fallback
        }
        setLookupState({
          kind: "error",
          message: translateOrderLookupError(res.status, errorCode, res.headers.get("retry-after")),
        });
        return;
      }

      const data = (await res.json()) as Partial<OrderLookupResponse> | null;
      if (!data?.order?.order_code) {
        setLookupState({
          kind: "not_found",
          message: ORDER_LOOKUP_NOT_FOUND_MESSAGE,
        });
        return;
      }
      setLookupState({ kind: "found", order: data.order });
    } catch {
      setLookupState({
        kind: "error",
        message: "Kết nối đến hệ thống tra cứu đơn hàng đang tạm gián đoạn. Vui lòng thử lại.",
      });
    }
  };

  const handleLookupSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = orderCode.trim();
    const digits = phone.replace(/\D/g, "");
    if (cleanCode.length < 3) {
      setFormError("Vui lòng nhập mã đơn hàng nhận được trong email hoặc mục Đơn hàng của tôi.");
      setErrorPulse((n) => n + 1);
      return;
    }
    if (digits.length < 8) {
      setFormError(
        "Vui lòng nhập số điện thoại người nhận đã dùng khi đặt hàng (tối thiểu 8 chữ số)."
      );
      setErrorPulse((n) => n + 1);
      return;
    }
    setFormError(null);
    void runLookup(cleanCode, phone.trim());
  };

  const renderHeroHead = (lead: string) => (
    <header className="space-y-1.5 text-center">
      <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground sm:text-2xl sm:leading-8">
        Tra cứu đơn hàng
      </h1>
      <p className="mx-auto max-w-2xl text-sm leading-5 text-muted-foreground">{lead}</p>
    </header>
  );

  if (viewState === "loading") {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
        <section className="mx-auto max-w-[820px] space-y-6">
          {renderHeroHead(storeLabel)}
          <LookupLoadingSkeleton />
        </section>
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
        <section className="mx-auto max-w-[820px] space-y-6">
          {renderHeroHead("Nhập mã đơn và số điện thoại đặt hàng để kiểm tra không cần đăng nhập.")}
          <EmptyState
            icon={PackageSearch}
            title="Chưa nhập mã đơn cần tra cứu"
            description="Nhập mã đơn hàng trong email xác nhận kèm số điện thoại người nhận để xem tiến trình giao hàng."
            action={
              <Button asChild className="min-h-10 shadow-none sm:min-h-9">
                <Link href="/tra-cuu-don-hang">Mở tra cứu đơn</Link>
              </Button>
            }
            className="border border-border shadow-none"
          />
        </section>
      </div>
    );
  }

  const foundOrder = lookupState.kind === "found" ? lookupState.order : null;
  const statusMeta = foundOrder ? getOrderStatusInfo(foundOrder.status) : null;
  const payMeta = foundOrder
    ? getPaymentStatusInfo(foundOrder.payment_status, foundOrder.payment_method, foundOrder.status)
    : null;
  const sortedTimeline = foundOrder
    ? [...(foundOrder.timeline ?? [])].sort((a, b) => {
        const ta = a.created_at ? Date.parse(a.created_at) : 0;
        const tb = b.created_at ? Date.parse(b.created_at) : 0;
        return tb - ta;
      })
    : [];
  const timelineItems: TimelineItem[] = sortedTimeline.map((ev, i) => {
    const evMeta = getOrderStatusInfo(ev.to_status);
    return {
      id: `${ev.to_status}-${ev.created_at ?? i}-${i}`,
      at: ev.created_at ? formatDateTimeVi(ev.created_at) : "—",
      title: evMeta.label,
    };
  });
  const isSubmitting = lookupState.kind === "loading";
  const createdDt = foundOrder?.created_at ? splitDateTimeVi(foundOrder.created_at) : null;

  return (
    <div className={cn(SITE_PAGE_CONTAINER, "space-y-8")}>
      <section className="mx-auto max-w-[820px] space-y-5" aria-labelledby="lookup-form-heading">
        {renderHeroHead(
          `Nhập mã đơn hàng và số điện thoại người nhận để theo dõi tiến trình giao nhận tại ${storeLabel} mà không cần đăng nhập.`
        )}

        <Card className="border border-border shadow-none">
          <CardHeader className="sr-only">
            <CardTitle id="lookup-form-heading">Biểu mẫu tra cứu đơn hàng</CardTitle>
          </CardHeader>
          <CardContent>
            <form
              ref={lookupFormRef}
              onSubmit={handleLookupSubmit}
              noValidate
              className="space-y-4"
            >
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field data-shake-target="" data-invalid={Boolean(formError) || undefined}>
                  <FieldLabel htmlFor="lookup-code">Mã đơn hàng</FieldLabel>
                  <Input
                    id="lookup-code"
                    name="orderCode"
                    type="text"
                    autoComplete="off"
                    className="h-11 bg-background text-base sm:h-10 sm:text-sm"
                    aria-invalid={Boolean(formError) || undefined}
                    aria-describedby={formError ? "lookup-code-error" : undefined}
                    value={orderCode}
                    onChange={(e) => {
                      setOrderCode(e.target.value);
                      if (formError) setFormError(null);
                    }}
                    placeholder="VD: NM-2026..."
                    disabled={isSubmitting}
                  />
                </Field>

                <Field data-shake-target="" data-invalid={Boolean(formError) || undefined}>
                  <FieldLabel htmlFor="lookup-phone">Số điện thoại người nhận</FieldLabel>
                  <Input
                    id="lookup-phone"
                    name="phone"
                    type="tel"
                    autoComplete="tel"
                    className="h-11 bg-background text-base sm:h-10 sm:text-sm"
                    aria-invalid={Boolean(formError) || undefined}
                    aria-describedby={formError ? "lookup-code-error" : undefined}
                    value={phone}
                    onChange={(e) => {
                      setPhone(e.target.value);
                      if (formError) setFormError(null);
                    }}
                    placeholder="0912 486 305"
                    disabled={isSubmitting}
                  />
                </Field>
              </div>

              {formError && <FieldError id="lookup-code-error">{formError}</FieldError>}

              <Button
                type="submit"
                size="lg"
                disabled={isSubmitting}
                className="min-h-11 w-full shadow-none"
              >
                <Search className="size-4" aria-hidden="true" />
                <span>{isSubmitting ? "Đang tra cứu..." : "Tra cứu đơn hàng"}</span>
              </Button>
            </form>

            {recentOrders.length > 0 && (
              <div className="mt-4 space-y-2 border-t border-border pt-4">
                <span className="block text-xs font-medium text-muted-foreground">
                  Đơn gần đây của bạn
                </span>
                <div className="flex flex-wrap gap-2">
                  {recentOrders.map((o) => {
                    const isActive = foundOrder?.order_code === o.orderCode;
                    const meta = getOrderStatusInfo(o.backendStatus);
                    return (
                      <Button
                        key={o.id}
                        type="button"
                        variant={isActive ? "default" : "outline"}
                        size="sm"
                        className="min-h-10 shadow-none sm:min-h-8"
                        onClick={() => {
                          const nextPhone = o.buyerPhone || phone || user?.phone || "";
                          setOrderCode(o.orderCode);
                          if (nextPhone) setPhone(nextPhone);
                          setFormError(null);
                          if (nextPhone.replace(/\D/g, "").length >= 8) {
                            void runLookup(o.orderCode, nextPhone);
                          }
                        }}
                      >
                        <span className="font-mono text-xs tabular-nums">{o.orderCode}</span>
                        <span className="opacity-75">· {meta.label}</span>
                      </Button>
                    );
                  })}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      <section className="mx-auto max-w-[820px]" aria-label="Kết quả tra cứu đơn hàng">
        {lookupState.kind === "idle" && (
          <EmptyState
            icon={Truck}
            title="Nhập mã đơn và số điện thoại để bắt đầu"
            description="Để bảo vệ quyền riêng tư, trang tra cứu công khai chỉ hiển thị thông tin người nhận đã che và khu vực giao hàng (Phường/Xã, Tỉnh/Thành phố)."
            action={
              <Button variant="outline" asChild className="min-h-10 shadow-none sm:min-h-9">
                <Link href="/don-hang">Mở đơn hàng của tôi</Link>
              </Button>
            }
            className="border border-border shadow-none"
          />
        )}

        {lookupState.kind === "loading" && <LookupLoadingSkeleton />}

        {lookupState.kind === "not_found" && (
          <EmptyState
            icon={PackageSearch}
            title="Không tìm thấy đơn hàng"
            description={lookupState.message}
            action={
              <Button variant="outline" asChild className="min-h-10 shadow-none sm:min-h-9">
                <Link href="/don-hang">Đăng nhập xem đơn của tôi</Link>
              </Button>
            }
            className="border border-border shadow-none"
          />
        )}

        {lookupState.kind === "rate_limited" && (
          <ErrorState
            title="Tạm giới hạn lượt tra cứu"
            description={lookupState.message}
            retryHref="/don-hang"
            className="border border-border shadow-none"
          />
        )}

        {lookupState.kind === "error" && (
          <ErrorState
            title="Chưa thể tra cứu đơn hàng"
            description={lookupState.message}
            onRetry={() => {
              const cleanCode = orderCode.trim();
              if (cleanCode && phone.replace(/\D/g, "").length >= 8) {
                void runLookup(cleanCode, phone.trim());
              }
            }}
            className="border border-border shadow-none"
          />
        )}

        {foundOrder && statusMeta && payMeta && (
          <Card className="border border-border shadow-none">
            <CardHeader className="flex flex-col gap-4 border-b border-border pb-4 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0 space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <h2 className="text-lg font-semibold tracking-tight text-foreground tabular-nums">
                    {foundOrder.order_code}
                  </h2>
                  <StatusBadge tone={orderStatusTone(foundOrder.status)}>
                    {statusMeta.label}
                  </StatusBadge>
                  <StatusBadge
                    tone={paymentStatusTone(
                      foundOrder.payment_status,
                      foundOrder.payment_method,
                      foundOrder.status
                    )}
                  >
                    {payMeta.label}
                  </StatusBadge>
                </div>
                <p className="text-xs text-muted-foreground tabular-nums">
                  {storeLabel}
                  {createdDt ? ` · ${createdDt.date}, ${createdDt.time}` : ""}
                </p>
              </div>
              <div className="flex flex-col sm:items-end">
                <span className="text-xs text-muted-foreground">Tổng thanh toán</span>
                <Money vnd={foundOrder.total_payable_vnd} size="lg" />
              </div>
            </CardHeader>

            <CardContent className="space-y-6">
              <div className="space-y-3">
                <h3 className="text-sm font-semibold text-foreground">Thông tin giao nhận</h3>
                <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div className="rounded-lg border border-border bg-background p-3.5">
                    <dt className="text-xs font-medium text-muted-foreground">
                      Người nhận &amp; khu vực
                    </dt>
                    <dd className="mt-1 text-sm font-medium text-foreground">
                      {[foundOrder.recipient_name_masked, foundOrder.recipient_phone_masked]
                        .filter(Boolean)
                        .join(" · ") || "Thông tin đã được che"}
                    </dd>
                    <dd className="mt-0.5 text-xs text-muted-foreground">
                      {formatPublicOrderArea(foundOrder.ward_name, foundOrder.province_name)}
                    </dd>
                  </div>

                  <div className="rounded-lg border border-border bg-background p-3.5">
                    <dt className="text-xs font-medium text-muted-foreground">Đơn vị vận chuyển</dt>
                    <dd className="mt-1 text-sm font-medium text-foreground tabular-nums">
                      {foundOrder.carrier_code
                        ? formatCarrierLabel(foundOrder.carrier_code)
                        : "Đang sắp xếp vận chuyển"}
                    </dd>
                    <dd className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                      {foundOrder.tracking_code
                        ? `Mã vận đơn: ${foundOrder.tracking_code}`
                        : "Chưa có mã vận đơn"}
                    </dd>
                  </div>

                  <div className="rounded-lg border border-border bg-background p-3.5">
                    <dt className="text-xs font-medium text-muted-foreground">Thanh toán</dt>
                    <dd className="mt-1 text-sm font-medium text-foreground">
                      {getPaymentMethodLabel(foundOrder.payment_method)}
                    </dd>
                    <dd className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                      Tiền hàng: {formatVnd(foundOrder.subtotal_vnd)} · Phí giao hàng:{" "}
                      {formatVnd(foundOrder.shipping_fee_vnd)}
                    </dd>
                  </div>

                  <div className="rounded-lg border border-border bg-background p-3.5">
                    <dt className="text-xs font-medium text-muted-foreground">Cập nhật gần nhất</dt>
                    <dd className="mt-1 text-sm font-medium text-foreground tabular-nums">
                      {foundOrder.updated_at
                        ? formatDateTimeVi(foundOrder.updated_at)
                        : statusMeta.label}
                    </dd>
                    <dd className="mt-0.5 text-xs text-muted-foreground">
                      {foundOrder.delivered_at
                        ? `Giao thành công lúc ${formatDateTimeVi(foundOrder.delivered_at)}`
                        : statusMeta.description}
                    </dd>
                  </div>
                </dl>
              </div>

              {Array.isArray(foundOrder.items) && foundOrder.items.length > 0 && (
                <>
                  <Separator />
                  <div className="space-y-3">
                    <h3 className="text-sm font-semibold text-foreground">Sản phẩm trong đơn</h3>
                    <div className="divide-y divide-border rounded-lg border border-border">
                      {foundOrder.items.map((item, idx) => (
                        <div
                          key={`${item.product_name}-${item.sku_name}-${idx}`}
                          className="flex items-center justify-between gap-3 px-3.5 py-2.5"
                        >
                          <div className="min-w-0 space-y-0.5">
                            <p className="truncate text-sm font-medium text-foreground">
                              {item.product_name}
                            </p>
                            <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground tabular-nums">
                              {item.sku_name ? <span>{item.sku_name}</span> : null}
                              <span>SL {item.quantity}</span>
                            </p>
                          </div>
                          <Money vnd={item.line_total_vnd} size="sm" className="shrink-0" />
                        </div>
                      ))}
                    </div>
                  </div>
                </>
              )}

              <Separator />

              <div className="space-y-3">
                <h3 className="text-sm font-semibold text-foreground">Hành trình đơn hàng</h3>
                {timelineItems.length > 0 ? (
                  <Timeline items={timelineItems} />
                ) : (
                  <p className="text-sm text-muted-foreground">
                    Chưa có mốc cập nhật hành trình cho đơn này.
                  </p>
                )}
              </div>

              <Separator />

              <div className="space-y-3">
                <p className="text-xs leading-5 text-muted-foreground">
                  Để sửa địa chỉ nhận hàng, hủy đơn, thanh toán lại hoặc gửi yêu cầu trả hàng, vui
                  lòng đăng nhập tài khoản đặt hàng của bạn.
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  <Button asChild className="min-h-10 shadow-none sm:min-h-9">
                    <Link href="/don-hang">Quản lý đơn hàng của tôi</Link>
                  </Button>
                  <Button variant="secondary" asChild className="min-h-10 sm:min-h-9">
                    <Link href="/chinh-sach">Chính sách cửa hàng</Link>
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>
        )}
      </section>

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip title={`Sản phẩm tại ${storeLabel}`} products={suggestedProducts} />
      )}
    </div>
  );
}

export function OrderLookupView(props: OrderLookupViewProps) {
  return (
    <Suspense
      fallback={
        <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
          <div className="mx-auto max-w-[820px]">
            <LookupLoadingSkeleton />
          </div>
        </div>
      }
    >
      <OrderLookupContent {...props} />
    </Suspense>
  );
}
