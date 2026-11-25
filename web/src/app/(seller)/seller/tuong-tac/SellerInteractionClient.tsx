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
import { SellerSlidingTabs, statusPillClass } from "@/features/seller/SellerUiPrimitives";
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
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

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

  // Tab 1: AI Review Summaries
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

  const interactionTabs: ReadonlyArray<{ key: InteractionTab; label: string; count?: number }> = [
    { key: "reviews", label: "Đánh giá AI", count: products.length },
    { key: "notifications", label: "Thông báo", count: notifSummary.unread },
  ];

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tương tác</h1>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/tra-hang">
            <span>Trả hàng &amp; hoàn tiền</span>
            <IconArrowUpRight size={14} />
          </Button>
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
          message="Thông báo mới và tóm tắt đánh giá sản phẩm sẽ hiển thị tại đây."
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

          {/* TAB 1: AI Review Summaries */}
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
                                      <p className={styles.mutedLine}>Chưa có ghi nhận.</p>
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

          {/* TAB 2: Notifications */}
          {tab === "notifications" && (
            <section className={common.card} aria-labelledby="notif-heading">
              <div className={common.cardHeader}>
                <h2 id="notif-heading" className={common.cardTitle}>
                  Thông báo ({formatInt(filteredNotifications.length)})
                </h2>
                <div className={common.segmented} role="tablist" aria-label="Lọc thông báo">
                  {(
                    [
                      ["all", `Tất cả (${formatInt(notifSummary.total)})`],
                      ["unread", `Chưa đọc (${formatInt(notifSummary.unread)})`],
                    ] as const
                  ).map(([k, lbl]) => (
                    <button
                      key={k}
                      type="button"
                      role="tab"
                      aria-selected={notifFilter === k}
                      className={`${common.segmentedBtn} ${notifFilter === k ? common.segmentedBtnActive : ""}`}
                      onClick={() => setNotifFilter(k)}
                    >
                      {lbl}
                    </button>
                  ))}
                </div>
              </div>

              {notifError ? (
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
                    ? "Không còn thông báo chưa đọc."
                    : "Chưa có thông báo nào."}
                </div>
              ) : (
                <div className={styles.itemList}>
                  {filteredNotifications.map((notif) => {
                    const safeHref = sanitizeSafeUrl(notif.link);
                    return (
                      <article
                        key={notif.id}
                        className={`${styles.itemRow} ${!notif.is_read ? styles.notifUnread : ""}`}
                      >
                        <div className={styles.itemTop}>
                          <div className={styles.itemMain}>
                            <div className={styles.itemTitleRow}>
                              <span className={common.pill}>
                                {getNotificationCategoryLabel(notif.category)}
                              </span>
                              <strong className={styles.notifTitle}>{notif.title}</strong>
                            </div>
                            {notif.body && <p className={styles.notifBody}>{notif.body}</p>}
                            {notif.created_at && (
                              <span className={common.cellSub}>
                                {formatDateTimeVi(notif.created_at)}
                              </span>
                            )}
                          </div>
                          <div className={styles.rowActions}>
                            {safeHref && (
                              <Button variant="outline" size="sm" shape="app" href={safeHref}>
                                <span>Xem</span>
                                <IconArrowUpRight size={13} />
                              </Button>
                            )}
                            {!notif.is_read && (
                              <Button
                                variant="ghost"
                                size="sm"
                                shape="app"
                                disabled={notifBusy}
                                onClick={() => void handleMarkNotificationRead([notif.id], false)}
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
