"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCTS } from "@/data";
import type {
  GatewayOrder,
  GatewayOrderStatus,
  GatewayPublicShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPackage, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import { clearCheckoutWarnings, readCheckoutWarnings } from "@/features/checkout/helpers";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/don-hang/page.module.css";
import type { OrderVisualLookup } from "./types";
import {
  ORDER_FILTER_TABS,
  type BadgeTone,
  canCancelOrder,
  formatUnpaidGroupCancelWarning,
  getCarrierLabel,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
  needsVietQrPayment,
  resolveUnpaidVietQrCancelGroup,
} from "./orderMeta";

export interface OrdersListClientProps {
  orders: GatewayOrder[];
  pendingOrders?: GatewayOrder[];
  total: number;
  allOrdersTotal: number;
  page: number;
  pageSize: number;
  activeStatus: "" | GatewayOrderStatus;
  shopsMap: Record<string, GatewayPublicShop>;
  visuals: OrderVisualLookup;
  initialError?: string | null;
  forcedState?: "loading" | "empty" | "error" | null;
}

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function buildOrdersHref(status: string, page: number): string {
  const params = new URLSearchParams();
  if (status) params.set("status", status);
  if (page > 1) params.set("page", String(page));
  const qs = params.toString();
  return qs ? `/don-hang?${qs}` : "/don-hang";
}

export function OrdersListClient({
  orders,
  pendingOrders = [],
  total,
  allOrdersTotal,
  page,
  pageSize,
  activeStatus,
  shopsMap,
  visuals,
  initialError,
  forcedState,
}: OrdersListClientProps) {
  const router = useRouter();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();
  const [orderList, setOrderList] = useState<GatewayOrder[]>(orders);
  const [listTotal, setListTotal] = useState<number>(total);
  const [pendingPool, setPendingPool] = useState<GatewayOrder[]>(
    pendingOrders.length > 0 ? pendingOrders : orders
  );
  const [retryingOrderId, setRetryingOrderId] = useState<string | null>(null);
  const [reorderingId, setReorderingId] = useState<string | null>(null);
  const [confirmCancelTarget, setConfirmCancelTarget] = useState<GatewayOrder | null>(null);
  const [cancellingId, setCancellingId] = useState<string | null>(null);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [checkoutWarnings, setCheckoutWarnings] = useState<string[]>([]);

  useEffect(() => {
    const ids = orderList.map((o) => o.id).filter(Boolean);
    if (ids.length === 0) return;
    const found = readCheckoutWarnings({ orderIds: ids });
    if (found.length > 0) {
      setCheckoutWarnings(found);
    }
  }, [orderList]);

  useEffect(() => {
    setOrderList(orders);
    setListTotal(total);
    setPendingPool(pendingOrders.length > 0 ? pendingOrders : orders);
  }, [orders, total, pendingOrders]);

  useEffect(() => {
    if (!confirmCancelTarget) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !cancellingId) {
        setConfirmCancelTarget(null);
        setCancelError(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [confirmCancelTarget, cancellingId]);

  useRevealOnScroll(`${forcedState ?? "live"}-${activeStatus}-${page}-${orderList.length}`);

  const totalPages = Math.max(1, Math.ceil(listTotal / pageSize));
  const suggestedProducts = useMemo(() => PRODUCTS.slice(0, 4), []);

  const inProgressCount = useMemo(
    () =>
      orderList.filter(
        (o) =>
          o.status === "PENDING" ||
          o.status === "CONFIRMED" ||
          o.status === "PREPARING" ||
          o.status === "SHIPPING"
      ).length,
    [orderList]
  );

  const pendingPayCount = useMemo(
    () => orderList.filter((order) => needsVietQrPayment(order)).length,
    [orderList]
  );

  const cancelGroupInfo = useMemo(
    () => resolveUnpaidVietQrCancelGroup(confirmCancelTarget, [...orderList, ...pendingPool]),
    [confirmCancelTarget, orderList, pendingPool]
  );

  const reloadOrdersList = async () => {
    const query = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    });
    if (activeStatus) {
      query.set("status", activeStatus);
    }
    try {
      const refreshed = await api<PaginatedResponse<GatewayOrder>>(`/orders?${query.toString()}`);
      if (refreshed && Array.isArray(refreshed.items)) {
        setOrderList(refreshed.items);
        if (typeof refreshed.total === "number") {
          setListTotal(refreshed.total);
        }
        setPendingPool(refreshed.items);
      }
    } catch {
      // Fallback to router refresh below
    }
    router.refresh();
  };

  const handleConfirmCancel = async () => {
    if (!confirmCancelTarget || cancellingId) return;
    const target = confirmCancelTarget;
    const wasMultiShopGroup = cancelGroupInfo.isMultiShopGroup;
    const groupShopCount = cancelGroupInfo.shopCount;

    setCancelError(null);
    setCancellingId(target.id);
    try {
      await api<{ order: GatewayOrder }>(`/orders/${encodeURIComponent(target.id)}/cancel`, {
        method: "POST",
        json: { reason: "Người mua hủy đơn hàng" },
      });
      setConfirmCancelTarget(null);
      if (retryingOrderId === target.id) {
        setRetryingOrderId(null);
      }
      await reloadOrdersList();
      showToast(
        wasMultiShopGroup
          ? `Đã hủy toàn bộ lần đặt hàng gồm ${groupShopCount} gian hàng.`
          : `Đã hủy đơn hàng ${target.order_code}.`
      );
    } catch (err) {
      setCancelError(getErrorMessage(err, "Chưa thể hủy đơn hàng lúc này. Vui lòng thử lại."));
    } finally {
      setCancellingId(null);
    }
  };

  const handleReorder = async (order: GatewayOrder) => {
    if (reorderingId || order.items.length === 0) return;
    setReorderingId(order.id);
    let added = 0;
    let clamped = false;
    let lastError = "";

    try {
      for (const item of order.items) {
        const res = await addToCart({
          skuId: item.sku_id,
          quantity: item.quantity,
        });
        if (res.ok) {
          added += 1;
          if (res.clampedToStock) clamped = true;
        } else if (res.message) {
          lastError = res.message;
        }
      }

      if (added > 0) {
        showToast(
          clamped
            ? `Đã thêm ${added} sản phẩm từ đơn ${order.order_code} vào giỏ (đã điều chỉnh theo số lượng còn sẵn).`
            : `Đã thêm lại ${added} sản phẩm từ đơn ${order.order_code} vào giỏ hàng.`
        );
        router.push("/gio-hang");
      } else {
        showToast(lastError || "Các sản phẩm trong đơn này hiện không còn sẵn để mua lại.");
      }
    } finally {
      setReorderingId(null);
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Đơn hàng của tôi
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={styles.summaryStat} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <div className={styles.statItem}>
          <span className={styles.statLabel}>Tổng số đơn</span>
          <span className={`${styles.statVal} num`}>{allOrdersTotal} đơn</span>
        </div>
        {inProgressCount > 0 && (
          <div className={styles.statItem}>
            <span className={styles.statLabel}>Đang xử lý</span>
            <span className={`${styles.statVal} num`}>{inProgressCount} đơn</span>
          </div>
        )}
        {pendingPayCount > 0 && (
          <div className={styles.statItem}>
            <span className={styles.statLabel}>Chờ thanh toán</span>
            <span className={`${styles.statVal} num`}>{pendingPayCount} đơn</span>
          </div>
        )}
        <Button variant="secondary" size="md" href="/tra-cuu-don-hang">
          Tra cứu bằng mã đơn
        </Button>
      </div>
    </section>
  );

  if (forcedState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật lịch sử đơn hàng và trạng thái giao nhận của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <StateView kind="loading" title="Đang tải danh sách đơn hàng..." rows={5} />
        </section>
        <ProductAddonStrip title="Sản phẩm cơ bản nổi bật" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "error" || initialError) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối đến lịch sử đơn hàng lúc này.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tải đơn hàng</h2>
          <StateView
            kind="error"
            title="Chưa tải được danh sách đơn hàng"
            message={initialError || "Kết nối đang tạm gián đoạn. Vui lòng thử lại sau ít giây."}
            actionLabel="Tải lại trang"
            onAction={() => router.refresh()}
          />
        </section>
        <ProductAddonStrip title="Gợi ý xem nhanh danh mục" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "empty" || (allOrdersTotal === 0 && !activeStatus)) {
    return (
      <div className={shared.page}>
        {renderHeader("Bạn chưa có đơn hàng nào. Khám phá đồ cơ bản chuẩn chất liệu để bắt đầu.")}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Lịch sử mua hàng</h2>
          <StateView
            kind="empty"
            title="Chưa có đơn hàng nào"
            message="Mọi đơn đặt từ các xưởng dệt Việt Nam trên NexusMart sẽ được lưu và theo dõi từng chặng tại đây."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        <ProductAddonStrip title="Gợi ý cho đơn hàng đầu tiên" products={suggestedProducts} />
      </div>
    );
  }

  return (
    <div className={shared.page}>
      {renderHeader(
        "Theo dõi hành trình giao hàng, hoàn tất thanh toán hoặc gửi yêu cầu trả hàng trực tiếp."
      )}

      <section className={shared.section}>
        {checkoutWarnings.length > 0 && (
          <div
            className={`${shared.feedbackBox} ${shared.feedbackWarn} ${shared.noticeSpacer}`}
            role="status"
          >
            <div>
              <strong>Lưu ý về ưu đãi khi tạo đơn:</strong>
              {checkoutWarnings.map((w, idx) => (
                <p key={idx} className={shared.noticeItem}>
                  {w}
                </p>
              ))}
              <p className={shared.noticeSubtle}>
                Tổng thanh toán trên mỗi đơn là giá cuối cùng đã được hệ thống xác nhận.
              </p>
            </div>
            <div className={shared.formActions}>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  clearCheckoutWarnings();
                  setCheckoutWarnings([]);
                }}
              >
                Đã hiểu
              </Button>
            </div>
          </div>
        )}
        <div className={shared.sectionHead}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <p className={shared.sectionNote}>
            Hiển thị <strong className="num">{orderList.length}</strong> /{" "}
            <span className="num">{listTotal}</span> đơn
            {totalPages > 1 && (
              <>
                {" "}
                · Trang <span className="num">{page}</span>/
                <span className="num">{totalPages}</span>
              </>
            )}
          </p>
        </div>

        <div
          className={styles.filterControls}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.tabBar} role="tablist" aria-label="Lọc trạng thái đơn hàng">
            {ORDER_FILTER_TABS.map((tab) => {
              const isActive = activeStatus === tab.key;
              return (
                <Link
                  key={tab.key || "all"}
                  href={buildOrdersHref(tab.key, 1)}
                  role="tab"
                  aria-selected={isActive}
                  className={`${styles.tabBtn} ${isActive ? styles.tabBtnActive : ""}`}
                >
                  <span>{tab.label}</span>
                </Link>
              );
            })}
          </div>
        </div>

        {orderList.length === 0 ? (
          <StateView
            kind="empty"
            title="Không có đơn hàng khớp trạng thái đã chọn"
            message="Chuyển về mục Tất cả để xem toàn bộ lịch sử mua hàng của bạn."
            actionLabel="Xem tất cả đơn hàng"
            actionHref="/don-hang"
          />
        ) : (
          <>
            <div className={styles.orderList}>
              {orderList.map((order, idx) => {
                const statusMeta = getOrderStatusInfo(order.status);
                const payMeta = getPaymentStatusInfo(
                  order.payment_status,
                  order.payment_method,
                  order.status
                );
                const shop = shopsMap[order.shop_id];
                const canRetryPay = needsVietQrPayment(order);
                const showRetryForm = retryingOrderId === order.id;

                return (
                  <article
                    key={order.id}
                    className={`${styles.orderCard} ${canRetryPay ? styles.orderCardPending : ""}`}
                    data-rise=""
                    style={{ "--i": Math.min(idx + 3, 7) } as React.CSSProperties}
                  >
                    <div className={styles.orderTop}>
                      <div className={styles.orderMetaLeft}>
                        <Link href={`/don-hang/${order.id}`} className={`${styles.orderCode} num`}>
                          {order.order_code}
                        </Link>
                        {shop?.slug ? (
                          <Link href={`/gian-hang/${shop.slug}`} className={styles.shopLink}>
                            {shop.name}
                          </Link>
                        ) : (
                          <span className={styles.shopLink}>
                            {shop?.name ?? "Gian hàng NexusMart"}
                          </span>
                        )}
                        {order.created_at && (
                          <span className={`${styles.orderDate} num`}>
                            {formatDateTimeVi(order.created_at)}
                          </span>
                        )}
                      </div>

                      <div className={styles.orderMetaRight}>
                        <span className={styles.payNote}>
                          {getPaymentMethodLabel(order.payment_method)}
                        </span>
                        <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
                          {payMeta.label}
                        </span>
                        <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
                          {statusMeta.label}
                        </span>
                      </div>
                    </div>

                    <div className={styles.itemsWrap}>
                      {order.items.map((item) => {
                        const prodVisual = visuals.byProductId[item.product_id];
                        const thumbUrl = visuals.bySkuId[item.sku_id] ?? prodVisual?.image;
                        const productHref = prodVisual?.slug
                          ? `/san-pham/${prodVisual.slug}`
                          : `/don-hang/${order.id}`;

                        return (
                          <div key={item.id || item.sku_id} className={styles.itemLine}>
                            <Link href={productHref} className={styles.itemThumbLink}>
                              {thumbUrl ? (
                                <img
                                  src={thumbUrl}
                                  alt={item.product_name}
                                  width={896}
                                  height={1200}
                                  loading="lazy"
                                  className={styles.itemThumb}
                                />
                              ) : (
                                <span className={styles.itemThumbPlaceholder} aria-hidden="true">
                                  <IconPackage size={24} />
                                </span>
                              )}
                            </Link>
                            <div className={styles.itemDetails}>
                              <Link href={productHref} className={styles.itemTitle}>
                                {item.product_name}
                              </Link>
                              <span className={styles.itemVariant}>
                                {item.sku_name ? `${item.sku_name} · ` : ""}SL{" "}
                                <span className="num">{item.quantity}</span> ×{" "}
                                <span className="num">
                                  {formatVnd(item.effective_unit_price_vnd || item.unit_price_vnd)}
                                </span>
                              </span>
                            </div>
                            <span className={`${styles.itemSubtotal} num`}>
                              {formatVnd(item.line_total_vnd)}
                            </span>
                          </div>
                        );
                      })}
                    </div>

                    {canRetryPay && showRetryForm && (
                      <div className={styles.retryWrap}>
                        <RetryPaymentBox
                          paymentId={order.payment_id}
                          onPaid={() => {
                            setRetryingOrderId(null);
                            showToast(`Đơn ${order.order_code} đã được ghi nhận thanh toán.`);
                            router.refresh();
                          }}
                          onPaymentReplaced={(next) => {
                            const patchOrder = (o: GatewayOrder): GatewayOrder =>
                              o.id === order.id ||
                              (Boolean(order.checkout_group_id) &&
                                o.checkout_group_id === order.checkout_group_id)
                                ? {
                                    ...o,
                                    payment_id: next.payment_id,
                                    payment_status: next.status,
                                    payment_expires_at: next.expires_at,
                                  }
                                : o;
                            setOrderList((prev) => prev.map(patchOrder));
                            setPendingPool((prev) => prev.map(patchOrder));
                            router.refresh();
                          }}
                        />
                      </div>
                    )}

                    <div className={styles.orderFoot}>
                      <div className={styles.totalCluster}>
                        <span className={styles.totalLabel}>Tổng thanh toán</span>
                        <span className={`${styles.totalValue} num`}>
                          {formatVnd(order.total_payable_vnd)}
                        </span>
                        {order.tracking_code && (
                          <span className={`${styles.trackingCode} num`}>
                            {order.carrier_code ? `${getCarrierLabel(order.carrier_code)} · ` : ""}
                            Vận đơn: {order.tracking_code}
                          </span>
                        )}
                      </div>

                      <div className={styles.actionRow}>
                        {canRetryPay && (
                          <>
                            <Button
                              variant="primary"
                              size="sm"
                              onClick={() => setRetryingOrderId(showRetryForm ? null : order.id)}
                            >
                              {showRetryForm ? "Ẩn thanh toán" : "Thanh toán lại"}
                            </Button>
                            <Button
                              variant="secondary"
                              size="sm"
                              href={`/thanh-toan/cho-thanh-toan/${order.payment_id}`}
                            >
                              Mở trang chờ VietQR
                            </Button>
                          </>
                        )}
                        <Button variant="ink" size="sm" href={`/don-hang/${order.id}`}>
                          Xem chi tiết
                        </Button>
                        {canCancelOrder(order.status) && (
                          <Button
                            variant="danger"
                            size="sm"
                            disabled={cancellingId !== null}
                            onClick={() => {
                              setCancelError(null);
                              setConfirmCancelTarget(order);
                            }}
                          >
                            Hủy đơn
                          </Button>
                        )}
                        {order.items.length > 0 && (
                          <Button
                            variant="secondary"
                            size="sm"
                            disabled={reorderingId === order.id}
                            onClick={() => void handleReorder(order)}
                          >
                            {reorderingId === order.id ? "Đang thêm..." : "Mua lại"}
                          </Button>
                        )}
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>

            {totalPages > 1 && (
              <nav className={styles.paginationBar} aria-label="Phân trang danh sách đơn hàng">
                <span className={`${styles.paginationInfo} num`}>
                  Trang {page} / {totalPages} ({listTotal} đơn hàng)
                </span>
                <div className={styles.paginationControls}>
                  {page > 1 ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      href={buildOrdersHref(activeStatus, page - 1)}
                    >
                      Trang trước
                    </Button>
                  ) : (
                    <Button variant="secondary" size="sm" disabled>
                      Trang trước
                    </Button>
                  )}
                  {page < totalPages ? (
                    <Button variant="ink" size="sm" href={buildOrdersHref(activeStatus, page + 1)}>
                      Trang sau
                    </Button>
                  ) : (
                    <Button variant="secondary" size="sm" disabled>
                      Trang sau
                    </Button>
                  )}
                </div>
              </nav>
            )}
          </>
        )}
      </section>

      <ProductAddonStrip
        title="Mua thêm đồ cơ bản từ xưởng dệt"
        subtitle="Chuẩn chất liệu, giao nhanh toàn quốc và theo dõi trực tuyến"
        products={suggestedProducts}
      />

      {confirmCancelTarget && (
        <div
          className={shared.confirmBackdrop}
          onClick={(ev) => {
            if (ev.target === ev.currentTarget && !cancellingId) {
              setConfirmCancelTarget(null);
              setCancelError(null);
            }
          }}
        >
          <div
            className={shared.confirmDialog}
            role="dialog"
            aria-modal="true"
            aria-labelledby="list-cancel-dialog-title"
          >
            <h2 id="list-cancel-dialog-title" className={shared.confirmTitle}>
              {cancelGroupInfo.isMultiShopGroup
                ? "Xác nhận hủy lần đặt hàng"
                : "Xác nhận hủy đơn hàng"}
            </h2>

            {cancelGroupInfo.isMultiShopGroup ? (
              <>
                <p className={shared.confirmBody}>
                  <strong>{formatUnpaidGroupCancelWarning(cancelGroupInfo.shopCount)}.</strong>
                </p>
                <p className={shared.confirmBody}>
                  Các gian hàng trong lần đặt này dùng chung một mã chuyển khoản VietQR nên toàn bộ
                  đơn chờ thanh toán bên dưới sẽ được hủy cùng lúc:
                </p>
                <ul className={shared.confirmList}>
                  {cancelGroupInfo.groupOrders.map((sib) => (
                    <li key={sib.id} className={shared.confirmListItem}>
                      <span>
                        <strong className="num">{sib.order_code}</strong> ·{" "}
                        {shopsMap[sib.shop_id]?.name ?? "Gian hàng NexusMart"}
                      </span>
                      <span className="num">{formatVnd(sib.total_payable_vnd)}</span>
                    </li>
                  ))}
                </ul>
              </>
            ) : (
              <p className={shared.confirmBody}>
                Bạn có chắc muốn hủy đơn hàng{" "}
                <strong className="num">{confirmCancelTarget.order_code}</strong>?
                {confirmCancelTarget.payment_method.toUpperCase() === "VIETQR" &&
                confirmCancelTarget.payment_status.toUpperCase() === "SUCCEEDED"
                  ? ` Số tiền ${formatVnd(confirmCancelTarget.total_payable_vnd)} đã thanh toán sẽ được xử lý hoàn lại theo quy định.`
                  : " Thao tác này không thể hoàn tác."}
              </p>
            )}

            {cancelError && (
              <p className={shared.fieldError} role="alert">
                {cancelError}
              </p>
            )}

            <div className={shared.formActions}>
              <Button
                type="button"
                variant="secondary"
                size="md"
                disabled={cancellingId !== null}
                onClick={() => {
                  setConfirmCancelTarget(null);
                  setCancelError(null);
                }}
              >
                Quay lại
              </Button>
              <Button
                type="button"
                variant="danger"
                size="md"
                disabled={cancellingId !== null}
                onClick={() => void handleConfirmCancel()}
              >
                {cancellingId
                  ? "Đang hủy..."
                  : cancelGroupInfo.isMultiShopGroup
                    ? `Xác nhận hủy cả ${cancelGroupInfo.shopCount} đơn`
                    : "Xác nhận hủy đơn"}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
