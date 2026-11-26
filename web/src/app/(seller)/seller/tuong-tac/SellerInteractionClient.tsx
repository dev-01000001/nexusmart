"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Bell, Check, ExternalLink, Package, RefreshCw, Search, Sparkles } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  PageHeader,
  StatusBadge,
  productStatusTone,
} from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { cn } from "@/lib/utils";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
import { getSellerProductStatusMeta, type GwSellerProduct } from "@/features/seller/sellerApiTypes";
import {
  getNotificationCategoryLabel,
  normalizeProductReviewSummary,
  normalizeSellerNotifications,
  parseUiStateParam,
  summarizeNotifications,
  type SellerNotification,
  type SellerProductReviewSummary,
} from "@/features/seller/sellerReturns";

type InteractionTab = "reviews" | "notifications";
type NotifFilter = "all" | "unread";

interface ProductSummaryState {
  loading: boolean;
  data: SellerProductReviewSummary | null;
  error: string | null;
}

export interface SellerInteractionClientProps {
  initialTab?: InteractionTab;
  products: GwSellerProduct[];
  productsError: string | null;
  initialNotifications: SellerNotification[];
  notificationsError: string | null;
}

function SellerInteractionContent({
  initialTab = "reviews",
  products,
  productsError,
  initialNotifications,
  notificationsError,
}: SellerInteractionClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiStateParam(searchParams.get("state"));
  const { showToast } = useSellerContext();

  const [tab, setTab] = useState<InteractionTab>(initialTab);

  // Tab 1: AI Review Summaries (read-only)
  const [productQuery, setProductQuery] = useState("");
  const [summaryMap, setSummaryMap] = useState<Record<string, ProductSummaryState>>({});

  // Tab 2: Notifications
  const [notifications, setNotifications] = useState<SellerNotification[]>(initialNotifications);
  const [notifError, setNotifError] = useState<string | null>(notificationsError);
  const [notifFilter, setNotifFilter] = useState<NotifFilter>("all");
  const [notifBusy, setNotifBusy] = useState(false);

  const filteredProducts = useMemo(() => {
    const q = productQuery.trim().toLowerCase();
    if (!q) return products;
    return products.filter((p) => p.name.toLowerCase().includes(q));
  }, [products, productQuery]);

  const notifSummary = useMemo(() => summarizeNotifications(notifications), [notifications]);
  const filteredNotifications = useMemo(
    () => (notifFilter === "unread" ? notifications.filter((n) => !n.is_read) : notifications),
    [notifications, notifFilter]
  );

  const handleLoadReviewSummary = async (productId: string) => {
    setSummaryMap((prev) => ({
      ...prev,
      [productId]: { loading: true, data: prev[productId]?.data ?? null, error: null },
    }));
    try {
      const raw = await api<unknown>(
        `/api/seller/products/${encodeURIComponent(productId)}/review-summary`
      );
      const normalized = normalizeProductReviewSummary(raw, productId);
      setSummaryMap((prev) => ({
        ...prev,
        [productId]: { loading: false, data: normalized, error: null },
      }));
    } catch (err) {
      const msg =
        err instanceof ApiError && (err.status === 404 || err.status === 503 || err.status === 0)
          ? "Chưa có dữ liệu tóm tắt AI cho sản phẩm này."
          : err instanceof ApiError
            ? err.message
            : "Không thể tải tóm tắt đánh giá AI lúc này.";
      setSummaryMap((prev) => ({
        ...prev,
        [productId]: { loading: false, data: null, error: msg },
      }));
    }
  };

  const handleRefreshNotifications = async () => {
    if (notifBusy) return;
    setNotifBusy(true);
    setNotifError(null);
    try {
      const raw = await api<unknown>("/api/seller/notifications?page=1&page_size=50");
      const normalized = normalizeSellerNotifications(raw);
      setNotifications(normalized.items);
      showToast("Đã làm mới thông báo.");
    } catch (err) {
      setNotifError(err instanceof ApiError ? err.message : "Không thể tải thông báo lúc này.");
    } finally {
      setNotifBusy(false);
    }
  };

  const handleMarkNotificationRead = async (ids: string[], markAll = false) => {
    if (notifBusy) return;
    setNotifBusy(true);
    try {
      await api<{ updated_count: number; unread_count: number }>("/api/seller/notifications/read", {
        method: "POST",
        body: markAll ? { mark_all: true } : { notification_ids: ids },
      });
      const idSet = new Set(ids);
      setNotifications((prev) =>
        prev.map((n) => (markAll || idSet.has(n.id) ? { ...n, is_read: true } : n))
      );
      showToast(markAll ? "Đã đánh dấu tất cả là đã đọc." : "Đã đánh dấu đã đọc.");
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái thông báo.");
    } finally {
      setNotifBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader
        title="Tương tác"
        secondaryActions={
          <>
            <Button variant="secondary" size="sm" asChild>
              <Link href="/seller/tra-hang">
                <span>Trả hàng &amp; hoàn tiền</span>
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </Link>
            </Button>
            {tab === "notifications" ? (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                disabled={notifBusy}
                onClick={() => void handleRefreshNotifications()}
              >
                <RefreshCw
                  className={cn("size-3.5", notifBusy && "animate-spin")}
                  aria-hidden="true"
                />
                <span>Làm mới</span>
              </Button>
            ) : null}
          </>
        }
        primaryAction={
          tab === "notifications" ? (
            <Button
              type="button"
              size="sm"
              disabled={notifBusy || notifSummary.unread === 0}
              onClick={() => void handleMarkNotificationRead([], true)}
            >
              <Check className="size-4" aria-hidden="true" />
              <span>Đọc tất cả</span>
            </Button>
          ) : null
        }
      />

      {uiState === "loading" ? (
        <Card role="status" aria-label="Đang tải dữ liệu tương tác...">
          <CardContent className="space-y-3 py-5">
            {Array.from({ length: 5 }).map((_, idx) => (
              <Skeleton key={idx} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      ) : null}

      {uiState === "error" ? (
        <ErrorState
          title="Không thể tải dữ liệu tương tác"
          description="Hệ thống tương tác tạm thời chưa phản hồi. Vui lòng thử lại."
          onRetry={() => router.refresh()}
        />
      ) : null}

      {uiState === "empty" ? (
        <EmptyState
          icon={Bell}
          title="Chưa có mục tương tác cần xử lý"
          description="Thông báo mới và tóm tắt đánh giá sản phẩm sẽ hiển thị tại đây."
          action={
            <Button size="sm" asChild>
              <Link href="/seller">Về tổng quan</Link>
            </Button>
          }
        />
      ) : null}

      {!uiState ? (
        <Tabs value={tab} onValueChange={(v) => setTab(v as InteractionTab)} className="space-y-4">
          <TabsList aria-label="Phân mục tương tác" className="bg-card p-1 shadow-card">
            <TabsTrigger value="reviews" className="gap-1.5">
              <span>Đánh giá</span>
              <span className="text-xs text-muted-foreground tabular-nums">
                {formatInt(products.length)}
              </span>
            </TabsTrigger>
            <TabsTrigger value="notifications" className="gap-1.5">
              <span>Thông báo</span>
              <span className="text-xs text-muted-foreground tabular-nums">
                {formatInt(notifSummary.unread)}
              </span>
            </TabsTrigger>
          </TabsList>

          {/* TAB 1: Read-only AI Review Summaries */}
          <TabsContent value="reviews">
            <Card aria-labelledby="ai-reviews-heading">
              <CardHeader className="flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-center sm:justify-between">
                <CardTitle id="ai-reviews-heading">
                  Đánh giá sản phẩm ({formatInt(filteredProducts.length)})
                </CardTitle>
                <div className="relative w-full sm:w-64">
                  <Search
                    className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    type="search"
                    value={productQuery}
                    onChange={(e) => setProductQuery(e.target.value)}
                    placeholder="Tìm tên sản phẩm..."
                    aria-label="Tìm sản phẩm để xem tóm tắt đánh giá AI"
                    className="h-8 bg-card pl-8 text-[13px]"
                  />
                </div>
              </CardHeader>

              <CardContent className="divide-y divide-border py-0">
                {productsError ? (
                  <div className="py-6">
                    <ErrorState
                      title="Không thể tải danh sách sản phẩm"
                      description={productsError}
                      onRetry={() => router.refresh()}
                    />
                  </div>
                ) : filteredProducts.length === 0 ? (
                  <p className="py-10 text-center text-sm text-muted-foreground">
                    {products.length === 0
                      ? "Gian hàng chưa có sản phẩm nào."
                      : "Không tìm thấy sản phẩm khớp từ khóa."}
                  </p>
                ) : (
                  filteredProducts.map((product) => {
                    const pMeta = getSellerProductStatusMeta(product.status);
                    const sumState = summaryMap[product.id];
                    const priceText =
                      product.max_price_vnd > product.min_price_vnd
                        ? `${formatVnd(product.min_price_vnd)} đến ${formatVnd(product.max_price_vnd)}`
                        : formatVnd(product.min_price_vnd);
                    const thumb = product.images?.[0];

                    return (
                      <article key={product.id} className="space-y-3 py-4">
                        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                          <div className="flex min-w-0 flex-1 items-center gap-3">
                            {thumb ? (
                              <img
                                src={thumb}
                                alt=""
                                className="size-11 shrink-0 rounded-md border border-border bg-muted object-cover"
                              />
                            ) : (
                              <span
                                className="flex size-11 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground"
                                aria-hidden="true"
                              >
                                <Package className="size-4" />
                              </span>
                            )}
                            <div className="min-w-0 flex-1 space-y-1">
                              <div className="flex flex-wrap items-center gap-2">
                                <Link
                                  href={`/seller/san-pham/${product.id}`}
                                  className="font-medium text-foreground hover:text-brand hover:underline"
                                >
                                  {product.name}
                                </Link>
                                <StatusBadge tone={productStatusTone(product.status)}>
                                  {pMeta.label}
                                </StatusBadge>
                              </div>
                              <p className="text-xs text-muted-foreground tabular-nums">
                                {priceText}, {formatInt(product.skus?.length ?? 0)} SKU
                              </p>
                            </div>
                          </div>

                          <Button
                            type="button"
                            variant={sumState?.data ? "outline" : "default"}
                            size="sm"
                            className="min-h-10 shrink-0 sm:min-h-8"
                            disabled={sumState?.loading}
                            onClick={() => void handleLoadReviewSummary(product.id)}
                          >
                            <Sparkles className="size-3.5" aria-hidden="true" />
                            <span>
                              {sumState?.loading
                                ? "Đang tổng hợp..."
                                : sumState?.data
                                  ? "Làm mới AI"
                                  : "Tóm tắt AI"}
                            </span>
                          </Button>
                        </div>

                        {sumState && !sumState.loading ? (
                          <div className="rounded-lg border border-border bg-muted/40 p-3.5 text-[13px]">
                            {sumState.error ? (
                              <div className="flex flex-wrap items-center justify-between gap-2 text-destructive">
                                <span>{sumState.error}</span>
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="xs"
                                  onClick={() => void handleLoadReviewSummary(product.id)}
                                >
                                  Thử lại
                                </Button>
                              </div>
                            ) : sumState.data && !sumState.data.ready ? (
                              <p className="text-muted-foreground">
                                Chưa đủ 10 đánh giá để tổng hợp AI
                                {sumState.data.review_count > 0
                                  ? ` (hiện có ${formatInt(sumState.data.review_count)} đánh giá).`
                                  : "."}
                              </p>
                            ) : sumState.data ? (
                              <div className="space-y-3">
                                <div className="flex flex-wrap items-baseline justify-between gap-2">
                                  <strong className="text-sm font-semibold text-foreground tabular-nums">
                                    {formatInt(sumState.data.review_count)} đánh giá
                                    {sumState.data.average_rating > 0
                                      ? ` (${sumState.data.average_rating.toFixed(1).replace(".", ",")}/5)`
                                      : ""}
                                  </strong>
                                  {sumState.data.updated_at ? (
                                    <span className="text-xs text-muted-foreground tabular-nums">
                                      {formatDateTimeVi(sumState.data.updated_at)}
                                    </span>
                                  ) : null}
                                </div>

                                {sumState.data.summary ? (
                                  <p className="leading-relaxed text-foreground">
                                    {sumState.data.summary}
                                  </p>
                                ) : null}

                                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                                  <div className="space-y-1.5 rounded-lg border border-border bg-card p-3">
                                    <span className="block text-xs font-semibold text-badge-success-ink">
                                      Ưu điểm nổi bật
                                    </span>
                                    {sumState.data.pros.length === 0 ? (
                                      <p className="text-xs text-muted-foreground">
                                        Chưa có ghi nhận.
                                      </p>
                                    ) : (
                                      <ul className="list-disc space-y-1 pl-4 text-[13px] text-foreground">
                                        {sumState.data.pros.map((item, idx) => (
                                          <li key={`${product.id}-pro-${idx}`}>{item}</li>
                                        ))}
                                      </ul>
                                    )}
                                  </div>
                                  <div className="space-y-1.5 rounded-lg border border-border bg-card p-3">
                                    <span className="block text-xs font-semibold text-badge-warning-ink">
                                      Nhược điểm lưu ý
                                    </span>
                                    {sumState.data.cons.length === 0 ? (
                                      <p className="text-xs text-muted-foreground">
                                        Chưa có ghi nhận.
                                      </p>
                                    ) : (
                                      <ul className="list-disc space-y-1 pl-4 text-[13px] text-foreground">
                                        {sumState.data.cons.map((item, idx) => (
                                          <li key={`${product.id}-con-${idx}`}>{item}</li>
                                        ))}
                                      </ul>
                                    )}
                                  </div>
                                </div>
                              </div>
                            ) : null}
                          </div>
                        ) : null}
                      </article>
                    );
                  })
                )}
              </CardContent>
            </Card>
          </TabsContent>

          {/* TAB 2: Notifications */}
          <TabsContent value="notifications">
            <Card aria-labelledby="notif-heading" className="gap-0 overflow-hidden py-0">
              <CardHeader className="flex-col gap-3 border-b border-border py-4 sm:flex-row sm:items-center sm:justify-between">
                <CardTitle id="notif-heading">
                  Thông báo ({formatInt(filteredNotifications.length)})
                </CardTitle>
                <CardAction>
                  <div
                    role="tablist"
                    aria-label="Lọc thông báo"
                    className="inline-flex items-center gap-1 rounded-lg bg-muted p-1"
                  >
                    {(
                      [
                        ["all", `Tất cả (${formatInt(notifSummary.total)})`],
                        ["unread", `Chưa đọc (${formatInt(notifSummary.unread)})`],
                      ] as const
                    ).map(([k, lbl]) => (
                      <Button
                        key={k}
                        type="button"
                        role="tab"
                        aria-selected={notifFilter === k}
                        size="xs"
                        variant={notifFilter === k ? "default" : "ghost"}
                        onClick={() => setNotifFilter(k)}
                      >
                        {lbl}
                      </Button>
                    ))}
                  </div>
                </CardAction>
              </CardHeader>

              <CardContent className="divide-y divide-border p-0">
                {notifError ? (
                  <div className="p-5">
                    <ErrorState
                      title="Không thể tải thông báo"
                      description={notifError}
                      onRetry={() => void handleRefreshNotifications()}
                    />
                  </div>
                ) : filteredNotifications.length === 0 ? (
                  <p className="px-6 py-10 text-center text-sm text-muted-foreground">
                    {notifFilter === "unread"
                      ? "Không còn thông báo chưa đọc."
                      : "Chưa có thông báo nào."}
                  </p>
                ) : (
                  filteredNotifications.map((notif) => {
                    const safeHref = sanitizeSafeUrl(notif.link);
                    return (
                      <article
                        key={notif.id}
                        className={cn(
                          "flex flex-col gap-3 px-4 py-3.5 transition-colors sm:flex-row sm:items-center sm:justify-between md:px-5",
                          !notif.is_read && "bg-muted/45"
                        )}
                      >
                        <div className="flex min-w-0 flex-1 items-start gap-2.5">
                          <span
                            aria-hidden="true"
                            className={cn(
                              "mt-2 size-2 shrink-0 rounded-full",
                              !notif.is_read ? "bg-brand" : "bg-transparent"
                            )}
                          />
                          <div className="min-w-0 flex-1 space-y-1">
                            <div className="flex flex-wrap items-center gap-2">
                              <Badge variant="neutral">
                                {getNotificationCategoryLabel(notif.category)}
                              </Badge>
                              <strong className="text-sm font-semibold text-foreground">
                                {notif.title}
                              </strong>
                            </div>
                            {notif.body ? (
                              <p className="text-[13px] leading-[18px] text-muted-foreground">
                                {notif.body}
                              </p>
                            ) : null}
                            {notif.created_at ? (
                              <span className="block text-xs text-muted-foreground tabular-nums">
                                {formatDateTimeVi(notif.created_at)}
                              </span>
                            ) : null}
                          </div>
                        </div>

                        <div className="flex shrink-0 flex-wrap items-center gap-2 pl-4 sm:pl-0">
                          {safeHref ? (
                            <Button
                              variant="outline"
                              size="xs"
                              className="min-h-9 sm:min-h-7"
                              asChild
                            >
                              <Link href={safeHref}>
                                <span>Xem</span>
                                <ExternalLink className="size-3" aria-hidden="true" />
                              </Link>
                            </Button>
                          ) : null}
                          {!notif.is_read ? (
                            <Button
                              type="button"
                              variant="ghost"
                              size="xs"
                              className="min-h-9 sm:min-h-7"
                              disabled={notifBusy}
                              onClick={() => void handleMarkNotificationRead([notif.id], false)}
                            >
                              Đã đọc
                            </Button>
                          ) : null}
                        </div>
                      </article>
                    );
                  })
                )}
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      ) : null}
    </div>
  );
}

export default function SellerInteractionClient(props: SellerInteractionClientProps) {
  return (
    <SellerSuspense>
      <SellerInteractionContent {...props} />
    </SellerSuspense>
  );
}
