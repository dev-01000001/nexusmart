"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ChevronLeft, ChevronRight, Package, PackageSearch } from "lucide-react";
import type { Product } from "@/data/products";
import { useStore } from "@/context/StoreContext";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import { cn } from "@/lib/utils";
import {
  ORDER_FILTER_TABS,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
  needsVietQrPayment,
} from "./orderMeta";
import {
  countOrdersByBackendStatus,
  filterOrdersByBackendStatus,
  type BuyerOrderFilterKey,
  type OrderView,
} from "./orderView";
import {
  SITE_PAGE_CONTAINER,
  orderStatusTone,
  paymentStatusTone,
  splitDateTimeVi,
} from "./ui/siteStatus";

const ORDERS_PAGE_SIZE = 10;

function isUnpaidOnlineOrder(order: OrderView): boolean {
  return needsVietQrPayment({
    status: order.backendStatus,
    payment_method: order.paymentMethodRaw,
    payment_id: order.paymentId,
    payment_status: order.paymentStatusRaw,
    payment_expires_at: order.paymentExpiresAt,
  });
}

function OrdersLoadingSkeleton() {
  return (
    <div className="space-y-3" aria-busy="true" aria-label="Đang tải đơn hàng">
      {Array.from({ length: 5 }).map((_, i) => (
        <Card key={i} className="border border-border shadow-none">
          <CardContent className="flex flex-col gap-4 sm:flex-row sm:items-center">
            <Skeleton className="size-16 shrink-0 rounded-md" />
            <div className="flex-1 space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-5 w-24 rounded-full" />
              </div>
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-3.5 w-1/2" />
            </div>
            <div className="flex flex-col items-start gap-2 sm:items-end">
              <Skeleton className="h-5 w-24" />
              <div className="flex gap-2">
                <Skeleton className="h-9 w-24" />
                <Skeleton className="h-9 w-20" />
              </div>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

export interface OrdersClientViewProps {
  brandName: string;
  initialOrders: OrderView[];
  suggestedProducts: Product[];
  initialError?: string | null;
}

function OrdersClientInner({
  brandName,
  initialOrders,
  suggestedProducts,
  initialError = null,
}: OrdersClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { addToCart, showToast } = useStore();
  const [activeTab, setActiveTab] = useState<BuyerOrderFilterKey>("");
  const [page, setPage] = useState(1);
  const [reorderingId, setReorderingId] = useState<string | null>(null);

  const counts = useMemo(() => countOrdersByBackendStatus(initialOrders), [initialOrders]);

  const filteredOrders = useMemo(
    () => filterOrdersByBackendStatus(initialOrders, activeTab),
    [initialOrders, activeTab]
  );

  const totalPages = Math.max(1, Math.ceil(filteredOrders.length / ORDERS_PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pagedOrders = useMemo(() => {
    const start = (currentPage - 1) * ORDERS_PAGE_SIZE;
    return filteredOrders.slice(start, start + ORDERS_PAGE_SIZE);
  }, [filteredOrders, currentPage]);

  const handleTabChange = (value: string) => {
    const next = (value === "ALL" ? "" : value) as BuyerOrderFilterKey;
    setActiveTab(next);
    setPage(1);
  };

  const handleReorder = async (order: OrderView) => {
    setReorderingId(order.id);
    let added = 0;
    try {
      for (const item of order.items) {
        const skuId = order.itemSkuIds[item.skuCode];
        if (!skuId) continue;
        const res = await addToCart({
          skuId,
          quantity: item.quantity,
          productName: item.productName,
          size: item.size || undefined,
        });
        if (res.unauthenticated) {
          router.push("/dang-nhap?next=%2Fdon-hang");
          return;
        }
        if (res.ok) added += 1;
      }
      if (added > 0) {
        showToast(`Đã thêm ${added} sản phẩm vào giỏ hàng.`);
        router.push("/gio-hang");
      } else {
        showToast("Sản phẩm trong đơn hiện hết hàng.");
      }
    } finally {
      setReorderingId(null);
    }
  };

  const renderHeader = () => (
    <header className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 space-y-1">
        <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground sm:text-2xl sm:leading-8">
          Đơn hàng <span className="font-normal text-muted-foreground">tại {brandName}</span>
        </h1>
        <p className="text-[13px] leading-[18px] text-muted-foreground">
          Theo dõi trạng thái thanh toán, vận chuyển và đổi trả cho các đơn đã đặt.
        </p>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" asChild className="min-h-10 shadow-none sm:min-h-9">
          <Link href="/tra-cuu-don-hang">Tra cứu đơn</Link>
        </Button>
        <Button variant="secondary" size="sm" asChild className="min-h-10 sm:min-h-9">
          <Link href="/tai-khoan">Tài khoản</Link>
        </Button>
      </div>
    </header>
  );

  if (viewState === "loading") {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
        {renderHeader()}
        <section aria-labelledby="orders-loading-heading">
          <h2 id="orders-loading-heading" className="sr-only">
            Danh sách đơn hàng
          </h2>
          <OrdersLoadingSkeleton />
        </section>
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
        {renderHeader()}
        <ErrorState
          title="Chưa tải được danh sách đơn hàng."
          description={initialError ?? "Thử lại sau ít giây."}
          retryHref="/don-hang"
          className="border border-border shadow-none"
        />
      </div>
    );
  }

  if (viewState === "empty" || initialOrders.length === 0) {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-8")}>
        {renderHeader()}
        <EmptyState
          icon={Package}
          title="Chưa có đơn hàng nào."
          description={`Các đơn đặt tại ${brandName} sẽ xuất hiện tại đây để bạn theo dõi giao vận và đổi trả.`}
          action={
            <Button asChild className="min-h-10 shadow-none sm:min-h-9">
              <Link href="/san-pham">Xem sản phẩm</Link>
            </Button>
          }
          className="border border-border shadow-none"
        />
        {suggestedProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={suggestedProducts} />
        )}
      </div>
    );
  }

  return (
    <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
      {renderHeader()}

      <section aria-label="Danh sách đơn hàng" className="space-y-4">
        <Tabs value={activeTab || "ALL"} onValueChange={handleTabChange}>
          <div className="-mx-1 overflow-x-auto px-1 pb-1">
            <TabsList
              aria-label="Lọc trạng thái đơn hàng"
              className="inline-flex h-auto w-max gap-1 bg-transparent p-0"
            >
              {ORDER_FILTER_TABS.map((tab) => (
                <TabsTrigger
                  key={tab.key || "ALL"}
                  value={tab.key || "ALL"}
                  className="min-h-10 px-3 py-1.5 text-[13px] sm:min-h-8"
                >
                  <span>{tab.label}</span>
                  <span className="ml-1 rounded-full bg-muted px-1.5 py-0.5 text-[11px] leading-none text-muted-foreground tabular-nums">
                    {counts[tab.key]}
                  </span>
                </TabsTrigger>
              ))}
            </TabsList>
          </div>
        </Tabs>

        {filteredOrders.length === 0 ? (
          <EmptyState
            icon={PackageSearch}
            title="Không có đơn hàng ở trạng thái này."
            description="Chọn mục Tất cả để xem toàn bộ lịch sử đơn hàng của bạn."
            action={
              <Button
                type="button"
                variant="outline"
                onClick={() => handleTabChange("ALL")}
                className="min-h-10 shadow-none sm:min-h-9"
              >
                Xem tất cả
              </Button>
            }
            className="border border-border shadow-none"
          />
        ) : (
          <div className="space-y-3">
            {pagedOrders.map((order) => {
              const meta = getOrderStatusInfo(order.backendStatus);
              const payMeta =
                order.paymentStatusRaw || order.paymentMethodRaw === "COD"
                  ? getPaymentStatusInfo(
                      order.paymentStatusRaw,
                      order.paymentMethodRaw,
                      order.backendStatus
                    )
                  : null;
              const pendingOnline = isUnpaidOnlineOrder(order);
              const visibleThumbs = order.items.slice(0, 3);
              const extraCount = Math.max(0, order.items.length - visibleThumbs.length);
              const dt = order.createdAt ? splitDateTimeVi(order.createdAt) : null;

              return (
                <Card
                  key={order.id}
                  className={cn(
                    "border bg-card py-4 shadow-none transition-colors",
                    pendingOnline ? "border-brand" : "border-border"
                  )}
                >
                  <CardContent className="grid grid-cols-1 gap-4 md:grid-cols-[auto_minmax(0,1fr)_auto] md:items-center">
                    <div className="flex items-center gap-2">
                      {visibleThumbs.map((item) => (
                        <Link
                          key={item.skuCode}
                          href={`/don-hang/${order.id}`}
                          className={cn(
                            "block shrink-0 overflow-hidden rounded-md border border-border bg-muted",
                            visibleThumbs.length > 1 ? "h-16 w-12" : "h-20 w-16"
                          )}
                        >
                          <img
                            src={item.image || "/products/ao-thun-cotton-trang.png"}
                            alt={item.productName}
                            width={896}
                            height={1200}
                            loading="lazy"
                            className="size-full object-cover"
                          />
                        </Link>
                      ))}
                      {extraCount > 0 && (
                        <Badge variant="secondary" className="tabular-nums">
                          +{extraCount}
                        </Badge>
                      )}
                    </div>

                    <div className="min-w-0 space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <Link
                          href={`/don-hang/${order.id}`}
                          className="text-sm font-semibold tracking-tight text-foreground tabular-nums hover:text-brand hover:underline"
                        >
                          {order.orderCode}
                        </Link>
                        <StatusBadge tone={orderStatusTone(order.backendStatus)}>
                          {meta.label}
                        </StatusBadge>
                        {payMeta && (
                          <StatusBadge
                            tone={paymentStatusTone(
                              order.paymentStatusRaw,
                              order.paymentMethodRaw,
                              order.backendStatus
                            )}
                          >
                            {payMeta.label}
                          </StatusBadge>
                        )}
                        {dt && (
                          <span className="text-xs text-muted-foreground tabular-nums">
                            {dt.date} · {dt.time}
                          </span>
                        )}
                      </div>

                      <ul className="space-y-1">
                        {order.items.map((item) => (
                          <li
                            key={item.skuCode}
                            className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 text-sm"
                          >
                            <span className="min-w-0 truncate font-medium text-foreground">
                              {item.productName}
                            </span>
                            <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground tabular-nums">
                              {item.colorName ? <span>{item.colorName}</span> : null}
                              {item.size ? <span>Size {item.size}</span> : null}
                              <span>×{item.quantity}</span>
                            </span>
                          </li>
                        ))}
                      </ul>

                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                        <span>{getPaymentMethodLabel(order.paymentMethodRaw)}</span>
                        {order.carrier && <span>{order.carrier}</span>}
                        {order.trackingCode && (
                          <span className="font-mono tabular-nums">{order.trackingCode}</span>
                        )}
                      </div>
                    </div>

                    <div className="flex flex-col gap-3 border-t border-border pt-3 md:items-end md:border-t-0 md:pt-0">
                      <div className="flex items-baseline justify-between gap-2 md:flex-col md:items-end md:gap-0.5">
                        <span className="text-xs text-muted-foreground">Tổng thanh toán</span>
                        <Money vnd={order.total} size="lg" />
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        {pendingOnline && order.paymentId && (
                          <Button size="sm" asChild className="min-h-10 shadow-none sm:min-h-8">
                            <Link
                              href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}?order=${encodeURIComponent(order.id)}`}
                            >
                              {order.paymentMethod === "vi_sandbox"
                                ? "Thanh toán ZaloPay"
                                : "Thanh toán VietQR"}
                            </Link>
                          </Button>
                        )}
                        <Button
                          variant={pendingOnline ? "outline" : "default"}
                          size="sm"
                          asChild
                          className="min-h-10 shadow-none sm:min-h-8"
                        >
                          <Link href={`/don-hang/${order.id}`}>Xem chi tiết</Link>
                        </Button>
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          disabled={reorderingId === order.id}
                          onClick={() => void handleReorder(order)}
                          className="min-h-10 sm:min-h-8"
                        >
                          {reorderingId === order.id ? "Đang thêm..." : "Mua lại"}
                        </Button>
                      </div>
                    </div>
                  </CardContent>
                </Card>
              );
            })}

            {totalPages > 1 && (
              <nav
                aria-label="Phân trang đơn hàng"
                className="flex items-center justify-between gap-3 pt-2"
              >
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={currentPage <= 1}
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  className="min-h-10 shadow-none sm:min-h-8"
                >
                  <ChevronLeft className="size-4" aria-hidden="true" />
                  <span>Trước</span>
                </Button>
                <span className="text-[13px] text-muted-foreground tabular-nums">
                  Trang {currentPage} / {totalPages}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={currentPage >= totalPages}
                  onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  className="min-h-10 shadow-none sm:min-h-8"
                >
                  <span>Sau</span>
                  <ChevronRight className="size-4" aria-hidden="true" />
                </Button>
              </nav>
            )}
          </div>
        )}
      </section>

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip title={`Mua thêm từ ${brandName}`} products={suggestedProducts} />
      )}
    </div>
  );
}

export function OrdersClientView(props: OrdersClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
          <OrdersLoadingSkeleton />
        </div>
      }
    >
      <OrdersClientInner {...props} />
    </Suspense>
  );
}
