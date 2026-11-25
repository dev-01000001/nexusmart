"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Button,
  IconArrowUpRight,
  IconCheck,
  IconRefresh,
  IconSearch,
  IconSpark,
  StateView,
} from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
import {
  SellerDrawer,
  SellerSlidingTabs,
  statusPillClass,
} from "@/features/seller/SellerUiPrimitives";
import {
  getSellerOrderStatusMeta,
  getSellerProductStatusMeta,
  summarizeOrderItems,
  type GwSellerOrder,
  type GwSellerProduct,
} from "@/features/seller/sellerApiTypes";
import {
  countPendingReturns,
  getNotificationCategoryLabel,
  getReturnStatusMeta,
  normalizeProductReviewSummary,
  normalizeSellerNotifications,
  normalizeSellerReturns,
  parseUiStateParam,
  summarizeNotifications,
  validateRejectReason,
  type SellerNotification,
  type SellerProductReviewSummary,
  type SellerReturnRequest,
} from "@/features/seller/sellerReturns";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type InteractionTab = "returns" | "reviews" | "notifications";
type ReturnFilter = "all" | "pending" | "returned";
type NotifFilter = "all" | "unread";

interface ProductSummaryState {
  loading: boolean;
  data: SellerProductReviewSummary | null;
  error: string | null;
}

interface OrderReturnsState {
  loading: boolean;
  items: SellerReturnRequest[];
  error: string | null;
}

export interface SellerInteractionClientProps {
  initialOrders: GwSellerOrder[];
  ordersError: string | null;
  products: GwSellerProduct[];
  productsError: string | null;
  initialNotifications: SellerNotification[];
  notificationsError: string | null;
}

function SellerInteractionContent({
  initialOrders,
  ordersError,
  products,
  productsError,
  initialNotifications,
  notificationsError,
}: SellerInteractionClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiStateParam(searchParams.get("state"));
  const initialTabParam = searchParams.get("tab") as InteractionTab | null;
  const { showToast } = useSellerContext();

  const [tab, setTab] = useState<InteractionTab>(
    initialTabParam && ["returns", "reviews", "notifications"].includes(initialTabParam)
      ? initialTabParam
      : "returns"
  );

  // Tab 1: Returns & Refunds state with slide-over drawer
  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [returnFilter, setReturnFilter] = useState<ReturnFilter>("all");
  const [drawerOrderId, setDrawerOrderId] = useState<string | null>(null);
  const [orderReturnsMap, setOrderReturnsMap] = useState<Record<string, OrderReturnsState>>({});
  const [returnBusyId, setReturnBusyId] = useState<string | null>(null);
  const [rejectOpenId, setRejectOpenId] = useState<string | null>(null);
  const [rejectDrafts, setRejectDrafts] = useState<Record<string, string>>({});
  const [rejectErrors, setRejectErrors] = useState<Record<string, string>>({});

  // Tab 2: AI Review Summaries
  const [productQuery, setProductQuery] = useState("");
  const [summaryMap, setSummaryMap] = useState<Record<string, ProductSummaryState>>({});

  // Tab 3: Notifications
  const [notifications, setNotifications] = useState<SellerNotification[]>(initialNotifications);
  const [notifError, setNotifError] = useState<string | null>(notificationsError);
  const [notifFilter, setNotifFilter] = useState<NotifFilter>("all");
  const [notifBusy, setNotifBusy] = useState(false);

  const productImageById = useMemo(() => {
    const map: Record<string, string> = {};
    for (const p of products) {
      if (p.images?.[0]) map[p.id] = p.images[0];
    }
    return map;
  }, [products]);

  const returnOrders = useMemo(
    () =>
      orders.filter((o) => {
        const st = o.status.toUpperCase();
        return st === "RETURN_REQUESTED" || st === "RETURNED";
      }),
    [orders]
  );

  const pendingReturnsCount = useMemo(() => countPendingReturns(returnOrders), [returnOrders]);
  const returnedOrdersCount = useMemo(
    () => returnOrders.filter((o) => o.status.toUpperCase() === "RETURNED").length,
    [returnOrders]
  );

  const filteredReturnOrders = useMemo(() => {
    return returnOrders.filter((o) => {
      const st = o.status.toUpperCase();
      if (returnFilter === "pending") return st === "RETURN_REQUESTED";
      if (returnFilter === "returned") return st === "RETURNED";
      return true;
    });
  }, [returnOrders, returnFilter]);

  const drawerOrder = useMemo(
    () => (drawerOrderId ? (returnOrders.find((o) => o.id === drawerOrderId) ?? null) : null),
    [drawerOrderId, returnOrders]
  );

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

  const loadOrderReturns = async (orderId: string) => {
    setOrderReturnsMap((prev) => ({
      ...prev,
      [orderId]: { loading: true, items: prev[orderId]?.items ?? [], error: null },
    }));
    try {
      const res = await api<{ returns?: SellerReturnRequest[] }>(
        `/api/seller/orders/${encodeURIComponent(orderId)}/returns`
      );
      setOrderReturnsMap((prev) => ({
        ...prev,
        [orderId]: {
          loading: false,
          items: normalizeSellerReturns(res),
          error: null,
        },
      }));
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Dịch vụ đổi trả tạm thời chưa phản hồi. Vui lòng thử lại sau.";
      setOrderReturnsMap((prev) => ({
        ...prev,
        [orderId]: { loading: false, items: [], error: msg },
      }));
    }
  };

  const handleOpenReturnDrawer = (orderId: string) => {
    setDrawerOrderId(orderId);
    if (!orderReturnsMap[orderId]) {
      void loadOrderReturns(orderId);
    }
  };

  const handleApproveReturn = async (
    orderId: string,
    orderCode: string,
    ret: SellerReturnRequest
  ) => {
    if (returnBusyId) return;
    setReturnBusyId(ret.id);
    try {
      const res = await api<{ return_request?: SellerReturnRequest | null }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/approve`,
        { method: "POST", body: {} }
      );
      setOrderReturnsMap((prev) => {
        const entry = prev[orderId];
        if (!entry) return prev;
        return {
          ...prev,
          [orderId]: {
            ...entry,
            items: entry.items.map((item) =>
              item.id === ret.id ? (res.return_request ?? { ...item, status: "APPROVED" }) : item
            ),
          },
        };
      });
      setOrders((prev) => prev.map((o) => (o.id === orderId ? { ...o, status: "RETURNED" } : o)));
      if (rejectOpenId === ret.id) setRejectOpenId(null);
      showToast(`Đã duyệt yêu cầu đổi trả cho đơn ${orderCode}.`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể duyệt yêu cầu đổi trả lúc này.");
    } finally {
      setReturnBusyId(null);
    }
  };

  const handleRejectReturn = async (
    orderId: string,
    orderCode: string,
    ret: SellerReturnRequest
  ) => {
    if (returnBusyId) return;
    const check = validateRejectReason(rejectDrafts[ret.id] ?? "");
    if (!check.ok) {
      setRejectErrors((prev) => ({ ...prev, [ret.id]: check.error }));
      return;
    }
    setRejectErrors((prev) => ({ ...prev, [ret.id]: "" }));
    setReturnBusyId(ret.id);
    try {
      const res = await api<{ return_request?: SellerReturnRequest | null }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/reject`,
        { method: "POST", body: { reason: check.value } }
      );
      setOrderReturnsMap((prev) => {
        const entry = prev[orderId];
        if (!entry) return prev;
        return {
          ...prev,
          [orderId]: {
            ...entry,
            items: entry.items.map((item) =>
              item.id === ret.id
                ? (res.return_request ?? {
                    ...item,
                    status: "REJECTED",
                    reject_reason: check.value,
                  })
                : item
            ),
          },
        };
      });
      setRejectOpenId(null);
      setRejectDrafts((prev) => ({ ...prev, [ret.id]: "" }));
      showToast(`Đã từ chối yêu cầu đổi trả cho đơn ${orderCode}.`);
      router.refresh();
    } catch (err) {
      showToast(
        err instanceof ApiError ? err.message : "Không thể từ chối yêu cầu đổi trả lúc này."
      );
    } finally {
      setReturnBusyId(null);
    }
  };

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

  const interactionTabs: ReadonlyArray<{ key: InteractionTab; label: string; count?: number }> = [
    { key: "returns", label: "Đổi trả", count: returnOrders.length },
    { key: "reviews", label: "Đánh giá AI", count: products.length },
    { key: "notifications", label: "Thông báo", count: notifSummary.unread },
  ];

  const returnFilterCards: ReadonlyArray<{ key: ReturnFilter; label: string; count: number }> = [
    { key: "all", label: "Tất cả đổi trả", count: returnOrders.length },
    { key: "pending", label: "Chờ gian hàng duyệt", count: pendingReturnsCount },
    { key: "returned", label: "Đã hoàn trả", count: returnedOrdersCount },
  ];

  const drawerReturnsState = drawerOrder ? orderReturnsMap[drawerOrder.id] : undefined;

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tương tác</h1>
        </div>
        <div className={common.headerActions}>
          {tab === "returns" && (
            <Button variant="outline" size="sm" shape="app" href="/seller/don-hang">
              <span>Tất cả đơn hàng</span>
              <IconArrowUpRight size={14} />
            </Button>
          )}
          {tab === "notifications" && (
            <>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={notifBusy}
                onClick={() => void handleRefreshNotifications()}
              >
                <IconRefresh size={14} />
                <span>Làm mới</span>
              </Button>
              <Button
                variant="ink"
                size="sm"
                shape="app"
                disabled={notifBusy || notifSummary.unread === 0}
                onClick={() => void handleMarkNotificationRead([], true)}
              >
                <IconCheck size={14} />
                <span>Đọc tất cả</span>
              </Button>
            </>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải dữ liệu tương tác..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu tương tác"
          message="Hệ thống tương tác tạm thời chưa phản hồi. Vui lòng thử lại."
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có mục tương tác cần xử lý"
          message="Yêu cầu đổi trả và thông báo mới sẽ hiển thị tại đây."
          actionLabel="Về tổng quan"
          actionHref="/seller"
        />
      )}

      {!uiState && (
        <>
          <SellerSlidingTabs
            tabs={interactionTabs}
            value={tab}
            onChange={setTab}
            ariaLabel="Phân mục tương tác"
          />

          {/* TAB 1: Returns & Refunds */}
          {tab === "returns" && (
            <>
              <div
                className={common.countCards3}
                role="tablist"
                aria-label="Lọc trạng thái đổi trả"
              >
                {returnFilterCards.map((c) => {
                  const selected = returnFilter === c.key;
                  return (
                    <button
                      key={c.key}
                      type="button"
                      role="tab"
                      aria-selected={selected}
                      className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                      onClick={() => setReturnFilter(c.key)}
                    >
                      <span className={common.countCardLabel}>{c.label}</span>
                      <span className={common.countCardValue}>{formatInt(c.count)}</span>
                    </button>
                  );
                })}
              </div>

              <div className={common.chipScroller} role="tablist" aria-label="Lọc đổi trả di động">
                {returnFilterCards.map((c) => {
                  const selected = returnFilter === c.key;
                  return (
                    <button
                      key={`m-${c.key}`}
                      type="button"
                      role="tab"
                      aria-selected={selected}
                      className={`${common.chipBtn} ${selected ? common.chipBtnActive : ""}`}
                      onClick={() => setReturnFilter(c.key)}
                    >
                      <span>{c.label}</span>
                      <span className={common.chipCount}>{formatInt(c.count)}</span>
                    </button>
                  );
                })}
              </div>

              <section className={common.card} aria-labelledby="returns-list-heading">
                <div className={common.cardHeader}>
                  <h2 id="returns-list-heading" className={common.cardTitle}>
                    Đơn đổi trả ({formatInt(filteredReturnOrders.length)})
                  </h2>
                </div>

                {ordersError ? (
                  <StateView
                    kind="error"
                    surface="app"
                    title="Không thể tải danh sách đổi trả"
                    message={ordersError}
                    actionLabel="Tải lại"
                    onAction={() => router.refresh()}
                  />
                ) : filteredReturnOrders.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Không có đơn đổi trả trong nhóm bộ lọc hiện tại.
                  </div>
                ) : (
                  <>
                    <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            <th>Đơn hàng</th>
                            <th>Sản phẩm</th>
                            <th className={common.numCol}>Giá trị</th>
                            <th>Trạng thái</th>
                            <th className={common.numCol}>Thao tác</th>
                          </tr>
                        </thead>
                        <tbody>
                          {filteredReturnOrders.map((order) => {
                            const stMeta = getSellerOrderStatusMeta(order.status);
                            const firstPid = order.items?.[0]?.product_id;
                            const thumb = firstPid ? productImageById[firstPid] : undefined;
                            return (
                              <tr key={order.id}>
                                <td style={{ whiteSpace: "nowrap" }}>
                                  <button
                                    type="button"
                                    className={common.rowTriggerBtn}
                                    onClick={() => handleOpenReturnDrawer(order.id)}
                                  >
                                    {order.order_code}
                                  </button>
                                  {order.created_at && (
                                    <span className={common.cellSub}>
                                      {formatDateTimeVi(order.created_at)}
                                    </span>
                                  )}
                                </td>
                                <td>
                                  <div className={styles.thumbCell}>
                                    {thumb && (
                                      <img
                                        src={thumb}
                                        alt=""
                                        className={styles.rowThumb}
                                        width={34}
                                        height={42}
                                      />
                                    )}
                                    <span className={styles.itemSummaryText}>
                                      {summarizeOrderItems(order.items)}
                                    </span>
                                  </div>
                                </td>
                                <td className={common.numCol}>
                                  <strong>{formatVnd(order.total_payable_vnd)}</strong>
                                </td>
                                <td>
                                  <span className={statusPillClass(stMeta.tone)}>
                                    {stMeta.label}
                                  </span>
                                </td>
                                <td className={common.numCol}>
                                  <div className={common.rowHoverActions}>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      shape="app"
                                      onClick={() => handleOpenReturnDrawer(order.id)}
                                    >
                                      Xem yêu cầu
                                    </Button>
                                    <Button
                                      variant="ghost"
                                      size="sm"
                                      shape="app"
                                      href={`/seller/don-hang/${order.id}`}
                                    >
                                      <span>Mở đơn</span>
                                      <IconArrowUpRight size={13} />
                                    </Button>
                                  </div>
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>

                    <div className={common.mobileList}>
                      {filteredReturnOrders.map((order) => {
                        const stMeta = getSellerOrderStatusMeta(order.status);
                        return (
                          <div key={order.id} className={common.mobileRow}>
                            <div className={common.mobileRowTop}>
                              <div>
                                <button
                                  type="button"
                                  className={common.rowTriggerBtn}
                                  onClick={() => handleOpenReturnDrawer(order.id)}
                                >
                                  {order.order_code}
                                </button>
                                <span className={common.cellSub}>
                                  {summarizeOrderItems(order.items)}
                                </span>
                              </div>
                              <span className={statusPillClass(stMeta.tone)}>{stMeta.label}</span>
                            </div>
                            <div className={common.mobileRowMeta}>
                              <strong className="num">{formatVnd(order.total_payable_vnd)}</strong>
                              <div className={styles.rowActions}>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  onClick={() => handleOpenReturnDrawer(order.id)}
                                >
                                  Xem yêu cầu
                                </Button>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  shape="app"
                                  href={`/seller/don-hang/${order.id}`}
                                >
                                  Mở đơn
                                </Button>
                              </div>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </>
                )}
              </section>
            </>
          )}

          {/* TAB 2: AI Review Summaries */}
          {tab === "reviews" && (
            <section className={common.card} aria-labelledby="ai-reviews-heading">
              <div className={common.cardHeader}>
                <h2 id="ai-reviews-heading" className={common.cardTitle}>
                  Đánh giá sản phẩm ({formatInt(filteredProducts.length)})
                </h2>
                <div className={common.searchWrap}>
                  <span className={common.searchIcon}>
                    <IconSearch size={14} />
                  </span>
                  <input
                    type="search"
                    className={common.searchInput}
                    value={productQuery}
                    onChange={(e) => setProductQuery(e.target.value)}
                    placeholder="Tìm tên sản phẩm..."
                    aria-label="Tìm sản phẩm để xem tóm tắt đánh giá AI"
                  />
                </div>
              </div>

              {productsError ? (
                <StateView
                  kind="error"
                  surface="app"
                  title="Không thể tải danh sách sản phẩm"
                  message={productsError}
                  actionLabel="Tải lại"
                  onAction={() => router.refresh()}
                />
              ) : filteredProducts.length === 0 ? (
                <div className={common.inlineEmpty}>
                  {products.length === 0
                    ? "Gian hàng chưa có sản phẩm nào."
                    : "Không tìm thấy sản phẩm khớp từ khóa."}
                </div>
              ) : (
                <div className={styles.itemList}>
                  {filteredProducts.map((product) => {
                    const pMeta = getSellerProductStatusMeta(product.status);
                    const sumState = summaryMap[product.id];
                    const priceText =
                      product.max_price_vnd > product.min_price_vnd
                        ? `${formatVnd(product.min_price_vnd)} đến ${formatVnd(product.max_price_vnd)}`
                        : formatVnd(product.min_price_vnd);
                    const thumb = product.images?.[0];

                    return (
                      <article key={product.id} className={styles.itemRow}>
                        <div className={styles.itemTop}>
                          <div className={styles.thumbCell}>
                            {thumb && (
                              <img
                                src={thumb}
                                alt=""
                                className={styles.rowThumb}
                                width={38}
                                height={48}
                              />
                            )}
                            <div className={styles.itemMain}>
                              <div className={styles.itemTitleRow}>
                                <Link
                                  href={`/seller/san-pham/${product.id}`}
                                  className={common.cellLink}
                                >
                                  {product.name}
                                </Link>
                                <span className={statusPillClass(pMeta.tone)}>{pMeta.label}</span>
                              </div>
                              <p className={styles.itemMeta}>
                                {priceText}, {formatInt(product.skus?.length ?? 0)} SKU
                              </p>
                            </div>
                          </div>

                          <div className={styles.rowActions}>
                            <Button
                              variant={sumState?.data ? "outline" : "ink"}
                              size="sm"
                              shape="app"
                              disabled={sumState?.loading}
                              onClick={() => void handleLoadReviewSummary(product.id)}
                            >
                              <IconSpark size={14} />
                              <span>
                                {sumState?.loading
                                  ? "Đang tổng hợp..."
                                  : sumState?.data
                                    ? "Làm mới AI"
                                    : "Tóm tắt AI"}
                              </span>
                            </Button>
                          </div>
                        </div>

                        {sumState && !sumState.loading && (
                          <div className={styles.detailPanel}>
                            {sumState.error ? (
                              <div className={styles.errorRow}>
                                <span>{sumState.error}</span>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  onClick={() => void handleLoadReviewSummary(product.id)}
                                >
                                  Thử lại
                                </Button>
                              </div>
                            ) : sumState.data && !sumState.data.ready ? (
                              <p className={styles.mutedLine}>
                                Chưa đủ 10 đánh giá để tổng hợp AI
                                {sumState.data.review_count > 0
                                  ? ` (hiện có ${formatInt(sumState.data.review_count)} đánh giá).`
                                  : "."}
                              </p>
                            ) : sumState.data ? (
                              <div className={styles.aiSummaryBox}>
                                <div className={styles.returnHead}>
                                  <strong className={styles.returnCode}>
                                    {formatInt(sumState.data.review_count)} đánh giá
                                    {sumState.data.average_rating > 0
                                      ? ` (${sumState.data.average_rating.toFixed(1).replace(".", ",")}/5)`
                                      : ""}
                                  </strong>
                                  {sumState.data.updated_at && (
                                    <span className={common.cardMeta}>
                                      {formatDateTimeVi(sumState.data.updated_at)}
                                    </span>
                                  )}
                                </div>

                                {sumState.data.summary && (
                                  <p className={styles.summaryLead}>{sumState.data.summary}</p>
                                )}

                                <div className={styles.prosConsGrid}>
                                  <div className={styles.prosBox}>
                                    <span className={styles.prosTitle}>Ưu điểm nổi bật</span>
                                    {sumState.data.pros.length === 0 ? (
                                      <p className={styles.mutedLine}>Chưa có ghi nhận.</p>
                                    ) : (
                                      <ul className={styles.bulletList}>
                                        {sumState.data.pros.map((item, idx) => (
                                          <li key={`${product.id}-pro-${idx}`}>{item}</li>
                                        ))}
                                      </ul>
                                    )}
                                  </div>
                                  <div className={styles.consBox}>
                                    <span className={styles.consTitle}>Nhược điểm lưu ý</span>
                                    {sumState.data.cons.length === 0 ? (
                                      <p className={styles.mutedLine}>Chưa ghi nhận.</p>
                                    ) : (
                                      <ul className={styles.bulletList}>
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
                        )}
                      </article>
                    );
                  })}
                </div>
              )}
            </section>
          )}

          {/* TAB 3: Operational Notifications */}
          {tab === "notifications" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp thông báo">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Chưa đọc</span>
                  <span
                    className={`${common.balanceValue} ${notifSummary.unread === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(notifSummary.unread)}
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đơn hàng</span>
                  <span
                    className={`${common.balanceValue} ${notifSummary.byCategory.order === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(notifSummary.byCategory.order)}
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Khuyến mãi &amp; Hệ thống</span>
                  <span
                    className={`${common.balanceValue} ${notifSummary.byCategory.promotion + notifSummary.byCategory.system === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(notifSummary.byCategory.promotion + notifSummary.byCategory.system)}
                  </span>
                </div>
              </section>

              <section className={common.card} aria-labelledby="notifications-heading">
                <div className={common.cardHeader}>
                  <h2 id="notifications-heading" className={common.cardTitle}>
                    Thông báo ({formatInt(filteredNotifications.length)})
                  </h2>
                  <div className={common.segmented} role="tablist" aria-label="Lọc thông báo">
                    <button
                      type="button"
                      role="tab"
                      aria-selected={notifFilter === "all"}
                      className={`${common.segmentedBtn} ${notifFilter === "all" ? common.segmentedBtnActive : ""}`}
                      onClick={() => setNotifFilter("all")}
                    >
                      Tất cả ({formatInt(notifSummary.total)})
                    </button>
                    <button
                      type="button"
                      role="tab"
                      aria-selected={notifFilter === "unread"}
                      className={`${common.segmentedBtn} ${notifFilter === "unread" ? common.segmentedBtnActive : ""}`}
                      onClick={() => setNotifFilter("unread")}
                    >
                      Chưa đọc ({formatInt(notifSummary.unread)})
                    </button>
                  </div>
                </div>

                {notifError && notifications.length === 0 ? (
                  <StateView
                    kind="error"
                    surface="app"
                    title="Không thể tải thông báo"
                    message={notifError}
                    actionLabel="Thử lại"
                    onAction={() => void handleRefreshNotifications()}
                  />
                ) : filteredNotifications.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    {notifFilter === "unread"
                      ? "Tất cả thông báo đã được đọc."
                      : "Chưa có thông báo vận hành."}
                  </div>
                ) : (
                  <div className={styles.itemList}>
                    {filteredNotifications.map((item) => {
                      const safeLink = sanitizeSafeUrl(item.link);
                      const isExternal = safeLink.startsWith("https://");
                      return (
                        <article
                          key={item.id}
                          className={`${styles.itemRow} ${common.rowHoverParent} ${!item.is_read ? styles.notifUnread : ""}`}
                        >
                          <div className={styles.itemTop}>
                            <div className={styles.itemMain}>
                              <div className={styles.itemTitleRow}>
                                {!item.is_read && (
                                  <span
                                    className={styles.unreadDot}
                                    aria-label="Thông báo chưa đọc"
                                  />
                                )}
                                <strong className={styles.notifTitle}>{item.title}</strong>
                                <span className={common.pill}>
                                  {getNotificationCategoryLabel(item.category)}
                                </span>
                              </div>
                              {item.body && <p className={styles.itemSub}>{item.body}</p>}
                              <p className={styles.itemMeta}>
                                {item.created_at
                                  ? formatDateTimeVi(item.created_at)
                                  : "Mới cập nhật"}
                              </p>
                            </div>

                            <div className={`${styles.rowActions} ${common.rowHoverActions}`}>
                              {safeLink &&
                                (isExternal ? (
                                  <a
                                    href={safeLink}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className={common.cardLink}
                                  >
                                    Mở
                                  </a>
                                ) : (
                                  <Link href={safeLink} className={common.cardLink}>
                                    Mở
                                  </Link>
                                ))}
                              {!item.is_read && (
                                <Button
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  disabled={notifBusy}
                                  onClick={() => void handleMarkNotificationRead([item.id], false)}
                                >
                                  Đã đọc
                                </Button>
                              )}
                            </div>
                          </div>
                        </article>
                      );
                    })}
                  </div>
                )}
              </section>
            </>
          )}
        </>
      )}

      {/* Slide-Over Return Request Drawer */}
      <SellerDrawer
        open={Boolean(drawerOrder)}
        title={drawerOrder ? `Đổi trả ${drawerOrder.order_code}` : "Yêu cầu đổi trả"}
        subtitle={
          drawerOrder
            ? `${formatVnd(drawerOrder.total_payable_vnd)}${drawerOrder.created_at ? `, ${formatDateTimeVi(drawerOrder.created_at)}` : ""}`
            : undefined
        }
        badge={
          drawerOrder ? (
            <span className={statusPillClass(getSellerOrderStatusMeta(drawerOrder.status).tone)}>
              {getSellerOrderStatusMeta(drawerOrder.status).label}
            </span>
          ) : undefined
        }
        onClose={() => {
          setDrawerOrderId(null);
          setRejectOpenId(null);
        }}
        footer={
          drawerOrder ? (
            <>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => void loadOrderReturns(drawerOrder.id)}
              >
                <IconRefresh size={14} />
                <span>Tải lại</span>
              </Button>
              <Button
                variant="ink"
                size="sm"
                shape="app"
                href={`/seller/don-hang/${drawerOrder.id}`}
              >
                <span>Mở chi tiết đơn</span>
                <IconArrowUpRight size={14} />
              </Button>
            </>
          ) : undefined
        }
      >
        {drawerOrder && (
          <>
            <div className={common.drawerSection}>
              <h3 className={common.drawerSectionTitle}>Sản phẩm trong đơn</h3>
              <p className={styles.itemSub}>{summarizeOrderItems(drawerOrder.items)}</p>
            </div>

            <div className={common.drawerSection}>
              <h3 className={common.drawerSectionTitle}>Hồ sơ yêu cầu đổi trả</h3>
              {drawerReturnsState?.loading ? (
                <p className={styles.mutedLine}>Đang tải yêu cầu đổi trả...</p>
              ) : drawerReturnsState?.error ? (
                <div className={styles.errorRow}>
                  <span>{drawerReturnsState.error}</span>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => void loadOrderReturns(drawerOrder.id)}
                  >
                    Thử lại
                  </Button>
                </div>
              ) : !drawerReturnsState || drawerReturnsState.items.length === 0 ? (
                <p className={styles.mutedLine}>
                  Chưa có hồ sơ đổi trả chi tiết cho đơn {drawerOrder.order_code}.
                </p>
              ) : (
                <div className={styles.returnDetailList}>
                  {drawerReturnsState.items.map((ret) => {
                    const retMeta = getReturnStatusMeta(ret.status);
                    const isRejectOpen = rejectOpenId === ret.id;
                    const isProcessing = returnBusyId === ret.id;
                    const safePhotos = ret.photos.map((p) => sanitizeSafeUrl(p)).filter(Boolean);

                    return (
                      <div key={ret.id} className={styles.returnCard}>
                        <div className={styles.returnHead}>
                          <div>
                            <strong className={styles.returnCode}>Yêu cầu {ret.id}</strong>
                            {ret.created_at && (
                              <span className={common.cellSub}>
                                {formatDateTimeVi(ret.created_at)}
                              </span>
                            )}
                          </div>
                          <span className={statusPillClass(retMeta.tone)}>{retMeta.label}</span>
                        </div>

                        <div className={styles.returnGrid}>
                          <div>
                            <span className={styles.returnLabel}>Lý do khách gửi</span>
                            <p className={styles.returnValue}>{ret.reason || "Không có ghi chú"}</p>
                          </div>
                          {ret.refund_amount_vnd > 0 && (
                            <div>
                              <span className={styles.returnLabel}>Hoàn dự kiến</span>
                              <p className={`${styles.returnValue} num`}>
                                {formatVnd(ret.refund_amount_vnd)}
                              </p>
                            </div>
                          )}
                          {ret.items.length > 0 && (
                            <div>
                              <span className={styles.returnLabel}>Sản phẩm hoàn</span>
                              <p className={styles.returnValue}>
                                {ret.items
                                  .map((it) => {
                                    const name = it.product_name || it.sku_name || it.sku_id;
                                    return `${name} ×${formatInt(it.quantity)}`;
                                  })
                                  .join(", ")}
                              </p>
                            </div>
                          )}
                          {safePhotos.length > 0 && (
                            <div>
                              <span className={styles.returnLabel}>Minh chứng</span>
                              <div className={styles.photoLinks}>
                                {safePhotos.map((photoUrl, idx) => (
                                  <a
                                    key={`${ret.id}-p-${idx}`}
                                    href={photoUrl}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className={common.cellLink}
                                  >
                                    Ảnh {idx + 1}
                                  </a>
                                ))}
                              </div>
                            </div>
                          )}
                        </div>

                        {ret.reject_reason && (
                          <p className={styles.mutedLine}>Lý do từ chối: {ret.reject_reason}</p>
                        )}

                        {retMeta.canDecide && (
                          <div className={styles.decisionArea}>
                            {isRejectOpen && (
                              <div className={common.field}>
                                <label
                                  htmlFor={`tuong-tac-reject-${ret.id}`}
                                  className={common.fieldLabel}
                                >
                                  Lý do từ chối (tối thiểu 5 ký tự)
                                </label>
                                <textarea
                                  id={`tuong-tac-reject-${ret.id}`}
                                  className={common.textArea}
                                  value={rejectDrafts[ret.id] ?? ""}
                                  onChange={(e) => {
                                    const v = e.target.value;
                                    setRejectDrafts((prev) => ({ ...prev, [ret.id]: v }));
                                    if (rejectErrors[ret.id]) {
                                      setRejectErrors((prev) => ({ ...prev, [ret.id]: "" }));
                                    }
                                  }}
                                  placeholder="Nhập lý do từ chối rõ ràng cho người mua..."
                                />
                                {rejectErrors[ret.id] && (
                                  <span className={common.fieldError}>{rejectErrors[ret.id]}</span>
                                )}
                              </div>
                            )}

                            <div className={styles.formActions}>
                              {isRejectOpen ? (
                                <>
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    shape="app"
                                    disabled={isProcessing}
                                    onClick={() => setRejectOpenId(null)}
                                  >
                                    Đóng
                                  </Button>
                                  <Button
                                    variant="danger"
                                    size="sm"
                                    shape="app"
                                    disabled={isProcessing}
                                    onClick={() =>
                                      void handleRejectReturn(
                                        drawerOrder.id,
                                        drawerOrder.order_code,
                                        ret
                                      )
                                    }
                                  >
                                    {isProcessing ? "Đang gửi..." : "Xác nhận từ chối"}
                                  </Button>
                                </>
                              ) : (
                                <>
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    shape="app"
                                    disabled={isProcessing}
                                    onClick={() => setRejectOpenId(ret.id)}
                                  >
                                    Từ chối đổi trả
                                  </Button>
                                  <Button
                                    variant="ink"
                                    size="sm"
                                    shape="app"
                                    disabled={isProcessing}
                                    onClick={() =>
                                      void handleApproveReturn(
                                        drawerOrder.id,
                                        drawerOrder.order_code,
                                        ret
                                      )
                                    }
                                  >
                                    <IconCheck size={14} />
                                    <span>{isProcessing ? "Đang xử lý..." : "Duyệt đổi trả"}</span>
                                  </Button>
                                </>
                              )}
                            </div>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </>
        )}
      </SellerDrawer>
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
