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

function pillCls(tone: "pos" | "warn" | "neg" | "neutral"): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
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
  const { shop, showToast } = useSellerContext();
  const shopDisplayName = shop?.name ?? "Gian hàng của bạn";

  const [tab, setTab] = useState<InteractionTab>(
    initialTabParam && ["returns", "reviews", "notifications"].includes(initialTabParam)
      ? initialTabParam
      : "returns"
  );

  // Tab 1: Returns & Refunds state
  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [returnFilter, setReturnFilter] = useState<ReturnFilter>("all");
  const [expandedOrderId, setExpandedOrderId] = useState<string | null>(null);
  const [orderReturnsMap, setOrderReturnsMap] = useState<Record<string, OrderReturnsState>>({});
  const [returnBusyId, setReturnBusyId] = useState<string | null>(null);
  const [rejectOpenId, setRejectOpenId] = useState<string | null>(null);
  const [rejectDrafts, setRejectDrafts] = useState<Record<string, string>>({});
  const [rejectErrors, setRejectErrors] = useState<Record<string, string>>({});

  // Tab 2: AI Review Summaries state
  const [productQuery, setProductQuery] = useState("");
  const [summaryMap, setSummaryMap] = useState<Record<string, ProductSummaryState>>({});

  // Tab 3: Operational Notifications state
  const [notifications, setNotifications] = useState<SellerNotification[]>(initialNotifications);
  const [notifError, setNotifError] = useState<string | null>(notificationsError);
  const [notifFilter, setNotifFilter] = useState<NotifFilter>("all");
  const [notifBusy, setNotifBusy] = useState(false);

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

  const handleToggleOrderReturns = (orderId: string) => {
    if (expandedOrderId === orderId) {
      setExpandedOrderId(null);
      return;
    }
    setExpandedOrderId(orderId);
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
          ? "Chưa có dữ liệu tóm tắt AI cho sản phẩm này hoặc dịch vụ đánh giá AI tạm thời chưa hoạt động."
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
      showToast("Đã làm mới danh sách thông báo vận hành.");
    } catch (err) {
      setNotifError(
        err instanceof ApiError ? err.message : "Không thể tải thông báo vận hành lúc này."
      );
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
      showToast(
        markAll ? "Đã đánh dấu tất cả thông báo là đã đọc." : "Đã đánh dấu thông báo là đã đọc."
      );
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái thông báo.");
    } finally {
      setNotifBusy(false);
    }
  };

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tương tác &amp; Xử lý sau bán</h1>
          <p className={common.finding}>
            {shopDisplayName} · {formatInt(pendingReturnsCount)} yêu cầu đổi trả chờ duyệt ·{" "}
            {formatInt(products.length)} sản phẩm · {formatInt(notifSummary.unread)} thông báo chưa
            đọc
          </p>
        </div>
        <div className={common.headerActions}>
          {tab === "returns" && (
            <Button variant="outline" size="sm" shape="app" href="/seller/don-hang">
              <span>Xem tất cả đơn hàng</span>
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
                <span>Đánh dấu tất cả đã đọc</span>
              </Button>
            </>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải yêu cầu đổi trả, đánh giá sản phẩm và thông báo..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu tương tác gian hàng"
          message="Hệ thống tương tác tạm thời chưa phản hồi. Vui lòng thử tải lại."
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có mục tương tác nào cần xử lý"
          message="Khi phát sinh yêu cầu đổi trả hoặc thông báo mới, nội dung sẽ hiển thị tại đây."
          actionLabel="Về tổng quan"
          actionHref="/seller"
        />
      )}

      {!uiState && (
        <>
          <div className={common.underlineTabs} role="tablist" aria-label="Phân mục tương tác">
            <button
              type="button"
              role="tab"
              aria-selected={tab === "returns"}
              className={`${common.underlineTab} ${tab === "returns" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("returns")}
            >
              Đổi trả &amp; Hoàn tiền ({formatInt(returnOrders.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "reviews"}
              className={`${common.underlineTab} ${tab === "reviews" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("reviews")}
            >
              Tóm tắt đánh giá AI ({formatInt(products.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "notifications"}
              className={`${common.underlineTab} ${tab === "notifications" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("notifications")}
            >
              Thông báo vận hành ({formatInt(notifSummary.unread)})
            </button>
          </div>

          {tab === "returns" && (
            <>
              <section className={common.balanceStrip3} aria-label="Thống kê yêu cầu đổi trả">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Chờ gian hàng duyệt</span>
                  <span
                    className={`${common.balanceValue} ${pendingReturnsCount === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(pendingReturnsCount)}
                  </span>
                  <span className={common.balanceCaption}>
                    Ưu tiên phản hồi sớm để tránh phát sinh khiếu nại
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đã xử lý / hoàn trả</span>
                  <span
                    className={`${common.balanceValue} ${returnedOrdersCount === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(returnedOrdersCount)}
                  </span>
                  <span className={common.balanceCaption}>
                    Đơn đã hoàn tất quy trình đổi trả về gian hàng
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Tổng đơn có yêu cầu đổi trả</span>
                  <span
                    className={`${common.balanceValue} ${returnOrders.length === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(returnOrders.length)}
                  </span>
                  <span className={common.balanceCaption}>
                    Tính trên {formatInt(orders.length)} đơn hàng gần nhất của shop
                  </span>
                </div>
              </section>

              <section className={common.card} aria-labelledby="returns-list-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="returns-list-heading" className={common.cardTitle}>
                      Danh sách đơn hàng đổi trả &amp; hoàn tiền
                    </h2>
                    <span className={common.cardMeta}>
                      Mở từng đơn để xem minh chứng khách gửi và duyệt hoặc từ chối trực tiếp
                    </span>
                  </div>
                  <div
                    className={common.segmented}
                    role="tablist"
                    aria-label="Lọc trạng thái đổi trả"
                  >
                    {(
                      [
                        ["all", `Tất cả (${formatInt(returnOrders.length)})`],
                        ["pending", `Chờ duyệt (${formatInt(pendingReturnsCount)})`],
                        ["returned", `Đã hoàn trả (${formatInt(returnedOrdersCount)})`],
                      ] as const
                    ).map(([k, lbl]) => (
                      <button
                        key={k}
                        type="button"
                        role="tab"
                        aria-selected={returnFilter === k}
                        className={`${common.segmentedBtn} ${returnFilter === k ? common.segmentedBtnActive : ""}`}
                        onClick={() => setReturnFilter(k)}
                      >
                        {lbl}
                      </button>
                    ))}
                  </div>
                </div>

                {ordersError ? (
                  <StateView
                    kind="error"
                    surface="app"
                    title="Không thể tải danh sách đơn đổi trả"
                    message={ordersError}
                    actionLabel="Tải lại"
                    onAction={() => router.refresh()}
                  />
                ) : filteredReturnOrders.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Không có đơn hàng nào có yêu cầu đổi trả trong nhóm bộ lọc hiện tại.
                  </div>
                ) : (
                  <div className={styles.itemList}>
                    {filteredReturnOrders.map((order) => {
                      const stMeta = getSellerOrderStatusMeta(order.status);
                      const isOpen = expandedOrderId === order.id;
                      const detailState = orderReturnsMap[order.id];

                      return (
                        <article key={order.id} className={styles.itemRow}>
                          <div className={styles.itemTop}>
                            <div className={styles.itemMain}>
                              <div className={styles.itemTitleRow}>
                                <Link
                                  href={`/seller/don-hang/${order.id}`}
                                  className={common.cellLink}
                                >
                                  {order.order_code}
                                </Link>
                                <span className={pillCls(stMeta.tone)}>{stMeta.label}</span>
                              </div>
                              <p className={styles.itemSub}>{summarizeOrderItems(order.items)}</p>
                              <p className={styles.itemMeta}>
                                Giá trị đơn {formatVnd(order.total_payable_vnd)}
                                {order.created_at
                                  ? ` · Đặt lúc ${formatDateTimeVi(order.created_at)}`
                                  : ""}
                              </p>
                            </div>

                            <div className={styles.rowActions}>
                              <Button
                                variant="outline"
                                size="sm"
                                shape="app"
                                onClick={() => handleToggleOrderReturns(order.id)}
                              >
                                {isOpen ? "Thu gọn yêu cầu" : "Xem yêu cầu đổi trả"}
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                shape="app"
                                href={`/seller/don-hang/${order.id}`}
                              >
                                <span>Mở đơn</span>
                                <IconArrowUpRight size={14} />
                              </Button>
                            </div>
                          </div>

                          {isOpen && (
                            <div className={styles.detailPanel}>
                              {detailState?.loading ? (
                                <p className={styles.mutedLine}>
                                  Đang tải chi tiết yêu cầu đổi trả...
                                </p>
                              ) : detailState?.error ? (
                                <div className={styles.errorRow}>
                                  <span>{detailState.error}</span>
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    shape="app"
                                    onClick={() => void loadOrderReturns(order.id)}
                                  >
                                    Thử lại
                                  </Button>
                                </div>
                              ) : !detailState || detailState.items.length === 0 ? (
                                <p className={styles.mutedLine}>
                                  Chưa có hồ sơ đổi trả chi tiết từ dịch vụ đổi trả cho đơn{" "}
                                  {order.order_code}. Bạn có thể kiểm tra thêm tại trang chi tiết
                                  đơn.
                                </p>
                              ) : (
                                <div className={styles.returnDetailList}>
                                  {detailState.items.map((ret) => {
                                    const retMeta = getReturnStatusMeta(ret.status);
                                    const isRejectOpen = rejectOpenId === ret.id;
                                    const isProcessing = returnBusyId === ret.id;
                                    const safePhotos = ret.photos
                                      .map((p) => sanitizeSafeUrl(p))
                                      .filter(Boolean);

                                    return (
                                      <div key={ret.id} className={styles.returnCard}>
                                        <div className={styles.returnHead}>
                                          <div>
                                            <strong className={styles.returnCode}>
                                              Yêu cầu {ret.id}
                                            </strong>
                                            {ret.created_at && (
                                              <span className={common.cellSub}>
                                                Gửi lúc {formatDateTimeVi(ret.created_at)}
                                              </span>
                                            )}
                                          </div>
                                          <span className={pillCls(retMeta.tone)}>
                                            {retMeta.label}
                                          </span>
                                        </div>

                                        <div className={styles.returnGrid}>
                                          <div>
                                            <span className={styles.returnLabel}>
                                              Lý do khách gửi
                                            </span>
                                            <p className={styles.returnValue}>
                                              {ret.reason || "Không có ghi chú"}
                                            </p>
                                          </div>
                                          {ret.refund_amount_vnd > 0 && (
                                            <div>
                                              <span className={styles.returnLabel}>
                                                Số tiền hoàn dự kiến
                                              </span>
                                              <p className={styles.returnValue}>
                                                {formatVnd(ret.refund_amount_vnd)}
                                              </p>
                                            </div>
                                          )}
                                          {ret.items.length > 0 && (
                                            <div>
                                              <span className={styles.returnLabel}>
                                                Sản phẩm hoàn
                                              </span>
                                              <p className={styles.returnValue}>
                                                {ret.items
                                                  .map((it) => {
                                                    const name =
                                                      it.product_name || it.sku_name || it.sku_id;
                                                    return `${name} x${formatInt(it.quantity)}`;
                                                  })
                                                  .join(", ")}
                                              </p>
                                            </div>
                                          )}
                                          {safePhotos.length > 0 && (
                                            <div>
                                              <span className={styles.returnLabel}>
                                                Ảnh minh chứng
                                              </span>
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
                                          <p className={styles.mutedLine}>
                                            Lý do từ chối đã gửi: {ret.reject_reason}
                                          </p>
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
                                                    setRejectDrafts((prev) => ({
                                                      ...prev,
                                                      [ret.id]: v,
                                                    }));
                                                    if (rejectErrors[ret.id]) {
                                                      setRejectErrors((prev) => ({
                                                        ...prev,
                                                        [ret.id]: "",
                                                      }));
                                                    }
                                                  }}
                                                  placeholder="Nhập lý do từ chối rõ ràng cho người mua..."
                                                />
                                                {rejectErrors[ret.id] && (
                                                  <span className={common.fieldError}>
                                                    {rejectErrors[ret.id]}
                                                  </span>
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
                                                        order.id,
                                                        order.order_code,
                                                        ret
                                                      )
                                                    }
                                                  >
                                                    {isProcessing
                                                      ? "Đang gửi..."
                                                      : "Xác nhận từ chối"}
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
                                                        order.id,
                                                        order.order_code,
                                                        ret
                                                      )
                                                    }
                                                  >
                                                    <IconCheck size={14} />
                                                    <span>
                                                      {isProcessing
                                                        ? "Đang xử lý..."
                                                        : "Duyệt đổi trả"}
                                                    </span>
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
                          )}
                        </article>
                      );
                    })}
                  </div>
                )}
              </section>
            </>
          )}

          {tab === "reviews" && (
            <section className={common.card} aria-labelledby="ai-reviews-heading">
              <div className={common.cardHeader}>
                <div>
                  <h2 id="ai-reviews-heading" className={common.cardTitle}>
                    Tóm tắt đánh giá sản phẩm do AI tổng hợp
                  </h2>
                  <span className={common.cardMeta}>
                    Chọn sản phẩm để xem tổng hợp ưu điểm và nhược điểm từ đánh giá thực tế của
                    người mua
                  </span>
                </div>
                <div className={common.searchWrap}>
                  <span className={common.searchIcon}>
                    <IconSearch size={14} />
                  </span>
                  <input
                    type="search"
                    className={common.searchInput}
                    value={productQuery}
                    onChange={(e) => setProductQuery(e.target.value)}
                    placeholder="Tìm theo tên sản phẩm..."
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
                    ? "Gian hàng chưa có sản phẩm nào. Tạo sản phẩm đầu tiên để nhận đánh giá từ khách."
                    : "Không tìm thấy sản phẩm khớp với từ khóa tìm kiếm."}
                </div>
              ) : (
                <div className={styles.itemList}>
                  {filteredProducts.map((product) => {
                    const pMeta = getSellerProductStatusMeta(product.status);
                    const sumState = summaryMap[product.id];
                    const priceText =
                      product.max_price_vnd > product.min_price_vnd
                        ? `${formatVnd(product.min_price_vnd)} - ${formatVnd(product.max_price_vnd)}`
                        : formatVnd(product.min_price_vnd);

                    return (
                      <article key={product.id} className={styles.itemRow}>
                        <div className={styles.itemTop}>
                          <div className={styles.itemMain}>
                            <div className={styles.itemTitleRow}>
                              <Link
                                href={`/seller/san-pham/${product.id}`}
                                className={common.cellLink}
                              >
                                {product.name}
                              </Link>
                              <span className={pillCls(pMeta.tone)}>{pMeta.label}</span>
                            </div>
                            <p className={styles.itemMeta}>
                              Giá bán {priceText} · {formatInt(product.skus?.length ?? 0)} biến thể
                              SKU
                            </p>
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
                                    ? "Làm mới tóm tắt AI"
                                    : "Xem tóm tắt AI"}
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
                                Chưa đủ 10 đánh giá để tổng hợp tóm tắt AI
                                {sumState.data.review_count > 0
                                  ? ` (hiện có ${formatInt(sumState.data.review_count)} đánh giá).`
                                  : "."}
                              </p>
                            ) : sumState.data ? (
                              <div className={styles.aiSummaryBox}>
                                <div className={styles.returnHead}>
                                  <strong className={styles.returnCode}>
                                    Tổng hợp từ {formatInt(sumState.data.review_count)} đánh giá
                                    {sumState.data.average_rating > 0
                                      ? ` · Điểm trung bình ${sumState.data.average_rating.toFixed(1).replace(".", ",")}/5`
                                      : ""}
                                  </strong>
                                  {sumState.data.updated_at && (
                                    <span className={common.cardMeta}>
                                      Cập nhật {formatDateTimeVi(sumState.data.updated_at)}
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
                                      <p className={styles.mutedLine}>Chưa có ghi nhận nổi bật.</p>
                                    ) : (
                                      <ul className={styles.bulletList}>
                                        {sumState.data.pros.map((item, idx) => (
                                          <li key={`${product.id}-pro-${idx}`}>{item}</li>
                                        ))}
                                      </ul>
                                    )}
                                  </div>
                                  <div className={styles.consBox}>
                                    <span className={styles.consTitle}>Nhược điểm cần lưu ý</span>
                                    {sumState.data.cons.length === 0 ? (
                                      <p className={styles.mutedLine}>Chưa ghi nhận điểm trừ.</p>
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

          {tab === "notifications" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp thông báo vận hành">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Thông báo chưa đọc</span>
                  <span
                    className={`${common.balanceValue} ${notifSummary.unread === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(notifSummary.unread)}
                  </span>
                  <span className={common.balanceCaption}>
                    Trên tổng {formatInt(notifSummary.total)} thông báo gần nhất
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Liên quan đến đơn hàng</span>
                  <span
                    className={`${common.balanceValue} ${notifSummary.byCategory.order === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(notifSummary.byCategory.order)}
                  </span>
                  <span className={common.balanceCaption}>
                    Đơn mới, vận chuyển và yêu cầu đổi trả
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Khuyến mãi &amp; Hệ thống</span>
                  <span
                    className={`${common.balanceValue} ${notifSummary.byCategory.promotion + notifSummary.byCategory.system === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(notifSummary.byCategory.promotion + notifSummary.byCategory.system)}
                  </span>
                  <span className={common.balanceCaption}>
                    Flash sale, phê duyệt và thông báo nền tảng
                  </span>
                </div>
              </section>

              <section className={common.card} aria-labelledby="notifications-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="notifications-heading" className={common.cardTitle}>
                      Hộp thông báo vận hành gian hàng
                    </h2>
                    <span className={common.cardMeta}>
                      Cập nhật trực tiếp từ hệ thống thông báo NexusMart
                    </span>
                  </div>
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
                    title="Không thể tải thông báo vận hành"
                    message={notifError}
                    actionLabel="Thử lại"
                    onAction={() => void handleRefreshNotifications()}
                  />
                ) : filteredNotifications.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    {notifFilter === "unread"
                      ? "Tất cả thông báo vận hành đã được đọc."
                      : "Chưa có thông báo vận hành nào."}
                  </div>
                ) : (
                  <div className={styles.itemList}>
                    {filteredNotifications.map((item) => {
                      const safeLink = sanitizeSafeUrl(item.link);
                      const isExternal = safeLink.startsWith("https://");
                      return (
                        <article
                          key={item.id}
                          className={`${styles.itemRow} ${!item.is_read ? styles.notifUnread : ""}`}
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

                            <div className={styles.rowActions}>
                              {safeLink &&
                                (isExternal ? (
                                  <a
                                    href={safeLink}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className={common.cardLink}
                                  >
                                    Xem chi tiết
                                  </a>
                                ) : (
                                  <Link href={safeLink} className={common.cardLink}>
                                    Xem chi tiết
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
                                  Đánh dấu đã đọc
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
