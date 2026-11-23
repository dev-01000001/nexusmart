"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCTS } from "@/data";
import type {
  GatewayOrder,
  GatewayOrderTimelineEvent,
  GatewayPublicShop,
  GatewayShipmentResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatSignedVnd, formatVnd } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPackage, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/don-hang/[id]/page.module.css";
import type { OrderVisualLookup } from "./types";
import {
  type BadgeTone,
  canCancelOrder,
  canConfirmReceived,
  canRequestReturn,
  formatUnpaidGroupCancelWarning,
  getCarrierLabel,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
  getShipmentStatusInfo,
  getTimelineEventTitle,
  localizeTimelineNote,
  needsVietQrPayment,
  resolveUnpaidVietQrCancelGroup,
} from "./orderMeta";

export interface OrderDetailClientProps {
  initialOrder: GatewayOrder | null;
  initialTimeline: GatewayOrderTimelineEvent[];
  initialShipment: GatewayShipmentResponse | null;
  shop: GatewayPublicShop | null;
  visuals: OrderVisualLookup;
  orderId: string;
  initialGroupOrders?: GatewayOrder[];
  groupShopsMap?: Record<string, GatewayPublicShop>;
  initialError?: string | null;
  forcedState?: "loading" | "empty" | "error" | null;
}

const CANCEL_REASONS = [
  "Thay đổi địa chỉ nhận hàng sang khu vực khác",
  "Muốn đổi kích cỡ hoặc chọn thêm màu khác",
  "Quên áp mã ưu đãi hoặc muốn đổi phương thức thanh toán",
  "Đặt nhầm hoặc không còn nhu cầu mua",
];

const RETURN_REASONS = [
  "Sản phẩm có lỗi đường may hoặc bề mặt vải",
  "Giao nhầm màu sắc, kích cỡ hoặc nhầm mẫu",
  "Sản phẩm không đúng mô tả",
  "Muốn đổi kích cỡ phù hợp hơn",
];

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function getMilestoneIndex(status: string): number {
  switch (status) {
    case "PENDING":
    case "CONFIRMED":
      return 0;
    case "PREPARING":
      return 1;
    case "SHIPPING":
      return 2;
    case "DELIVERED":
    case "COMPLETED":
      return 3;
    default:
      return -1;
  }
}

function isSafeExternalUrl(url: string): boolean {
  const trimmed = url.trim();
  return trimmed.startsWith("https://") || trimmed.startsWith("http://");
}

export function OrderDetailClient({
  initialOrder,
  initialTimeline,
  initialShipment,
  shop,
  visuals,
  orderId,
  initialGroupOrders = [],
  groupShopsMap = {},
  initialError,
  forcedState,
}: OrderDetailClientProps) {
  const router = useRouter();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();

  const [order, setOrder] = useState<GatewayOrder | null>(initialOrder);
  const [timeline, setTimeline] = useState<GatewayOrderTimelineEvent[]>(initialTimeline);
  const [shipmentData, setShipmentData] = useState<GatewayShipmentResponse | null>(initialShipment);
  const [groupOrders, setGroupOrders] = useState<GatewayOrder[]>(initialGroupOrders);

  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);
  const [cancelNote, setCancelNote] = useState("");
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [confirmGroupCancelOpen, setConfirmGroupCancelOpen] = useState(false);

  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);

  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [returning, setReturning] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);

  const [reordering, setReordering] = useState(false);

  const cancelGroupInfo = useMemo(
    () => resolveUnpaidVietQrCancelGroup(order, groupOrders),
    [order, groupOrders]
  );

  useEffect(() => {
    if (!confirmGroupCancelOpen) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !cancelling) {
        setConfirmGroupCancelOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [confirmGroupCancelOpen, cancelling]);

  useRevealOnScroll(
    `${forcedState ?? "live"}-${order?.id ?? "none"}-${order?.status ?? ""}-${order?.payment_status ?? ""}`
  );

  const suggestedProducts = useMemo(() => PRODUCTS.slice(0, 4), []);

  const refreshOrder = useCallback(async () => {
    const id = encodeURIComponent(orderId);
    try {
      const [orderRes, timelineRes, shipRes] = await Promise.all([
        api<{ order: GatewayOrder }>(`/orders/${id}`),
        api<{ events: GatewayOrderTimelineEvent[] }>(`/orders/${id}/timeline`).catch(() => null),
        api<GatewayShipmentResponse>(`/shipping/shipments/${id}`).catch(() => null),
      ]);
      if (orderRes?.order) {
        setOrder(orderRes.order);
        if (timelineRes && Array.isArray(timelineRes.events)) {
          setTimeline(timelineRes.events);
        } else if (Array.isArray(orderRes.order.timeline)) {
          setTimeline(orderRes.order.timeline);
        }
      }
      if (shipRes?.shipment) {
        setShipmentData(shipRes);
      }
    } catch {
      router.refresh();
    }
  }, [orderId, router]);

  const handleReorder = async () => {
    if (!order || reordering || order.items.length === 0) return;
    setReordering(true);
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
            ? `Đã thêm ${added} sản phẩm vào giỏ hàng (đã điều chỉnh theo số lượng còn sẵn).`
            : `Đã đưa lại ${added} sản phẩm vào giỏ hàng.`
        );
        router.push("/gio-hang");
      } else {
        showToast(lastError || "Các sản phẩm trong đơn này hiện không còn sẵn để mua lại.");
      }
    } finally {
      setReordering(false);
    }
  };

  const executeCancelOrder = async () => {
    if (!order || cancelling) return;
    const wasMultiShopGroup = cancelGroupInfo.isMultiShopGroup;
    const groupShopCount = cancelGroupInfo.shopCount;
    const targetGroupId = order.checkout_group_id;

    setCancelError(null);
    setCancelling(true);
    const fullReason = cancelNote.trim() ? `${cancelReason}: ${cancelNote.trim()}` : cancelReason;
    try {
      const res = await api<{ order: GatewayOrder }>(
        `/orders/${encodeURIComponent(order.id)}/cancel`,
        {
          method: "POST",
          json: { reason: fullReason },
        }
      );
      if (res?.order) {
        setOrder(res.order);
        if (Array.isArray(res.order.timeline) && res.order.timeline.length > 0) {
          setTimeline(res.order.timeline);
        }
      }
      setConfirmGroupCancelOpen(false);
      setCancelNote("");
      showToast(
        wasMultiShopGroup
          ? `Đã hủy toàn bộ lần đặt hàng gồm ${groupShopCount} gian hàng.`
          : "Đã hủy đơn hàng thành công."
      );
      await refreshOrder();
      if (wasMultiShopGroup && targetGroupId) {
        try {
          const refreshedList = await api<PaginatedResponse<GatewayOrder>>(
            "/orders?page=1&page_size=50"
          );
          if (refreshedList && Array.isArray(refreshedList.items)) {
            const refreshedSiblings = refreshedList.items.filter(
              (item) => item.checkout_group_id === targetGroupId
            );
            if (refreshedSiblings.length > 0) {
              setGroupOrders(refreshedSiblings);
            }
          }
        } catch {
          // Ignore secondary list reload errors; router.refresh refreshes server cache below.
        }
        router.refresh();
      }
    } catch (err) {
      setCancelError(getErrorMessage(err, "Chưa thể hủy đơn hàng lúc này. Vui lòng thử lại."));
    } finally {
      setCancelling(false);
    }
  };

  const handleCancelSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || cancelling) return;
    setCancelError(null);
    if (cancelGroupInfo.isMultiShopGroup) {
      setConfirmGroupCancelOpen(true);
      return;
    }
    void executeCancelOrder();
  };

  const handleConfirmReceived = async () => {
    if (!order || confirming) return;
    setConfirmError(null);
    setConfirming(true);
    try {
      const res = await api<{ order: GatewayOrder }>(
        `/orders/${encodeURIComponent(order.id)}/confirm-received`,
        { method: "POST" }
      );
      if (res?.order) {
        setOrder(res.order);
        if (Array.isArray(res.order.timeline) && res.order.timeline.length > 0) {
          setTimeline(res.order.timeline);
        }
      }
      showToast("Đã xác nhận nhận hàng. Đơn hàng hoàn tất!");
      await refreshOrder();
    } catch (err) {
      setConfirmError(
        getErrorMessage(err, "Chưa thể xác nhận đã nhận hàng lúc này. Vui lòng thử lại.")
      );
    } finally {
      setConfirming(false);
    }
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || returning) return;
    const cleanDetail = returnDetail.trim();
    if (cleanDetail.length < 6) {
      setReturnError("Vui lòng mô tả ngắn tình trạng sản phẩm cần trả hàng (tối thiểu 6 ký tự).");
      return;
    }
    setReturnError(null);
    setReturning(true);
    try {
      const res = await api<{ order: GatewayOrder; return_request_id?: string }>(
        `/orders/${encodeURIComponent(order.id)}/return`,
        {
          method: "POST",
          json: {
            reason: `${returnReason}. Ghi chú: ${cleanDetail}`,
            evidence_urls: [],
          },
        }
      );
      if (res?.order) {
        setOrder(res.order);
        if (Array.isArray(res.order.timeline) && res.order.timeline.length > 0) {
          setTimeline(res.order.timeline);
        }
      }
      setReturnDetail("");
      showToast("Đã gửi yêu cầu trả hàng đến gian hàng.");
      await refreshOrder();
    } catch (err) {
      setReturnError(
        getErrorMessage(err, "Chưa thể gửi yêu cầu trả hàng lúc này. Vui lòng thử lại.")
      );
    } finally {
      setReturning(false);
    }
  };

  const renderBreadcrumb = () => (
    <nav className={shared.breadcrumbNav} aria-label="Đường dẫn đơn hàng">
      <Link href="/don-hang" className={shared.breadcrumbLink}>
        Đơn hàng của tôi
      </Link>
      <span aria-hidden="true">/</span>
      <span className="num">{order?.order_code ?? orderId}</span>
    </nav>
  );

  if (forcedState === "loading") {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Chi tiết đơn hàng</h1>
            <p className={styles.detailLead}>
              Đang tải hành trình giao hàng và thông tin thanh toán cho đơn hàng này.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Thông tin đơn</h2>
          <StateView kind="loading" title="Đang tải chi tiết đơn hàng..." rows={5} />
        </section>
        <ProductAddonStrip title="Sản phẩm cơ bản từ NexusMart" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "error" || initialError) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Chi tiết đơn hàng</h1>
            <p className={styles.detailLead}>
              Không thể lấy dữ liệu mới nhất cho mã đơn hàng này vào lúc này.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
          <StateView
            kind="error"
            title="Chưa thể tải chi tiết đơn hàng"
            message={
              initialError || "Hệ thống theo dõi đơn hàng đang tạm gián đoạn. Vui lòng thử tải lại."
            }
            actionLabel="Quay lại danh sách đơn"
            actionHref="/don-hang"
          />
        </section>
        <ProductAddonStrip title="Sản phẩm gợi ý" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "empty" || !order) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Không tìm thấy đơn</h1>
            <p className={styles.detailLead}>
              Đơn hàng bạn mở không thuộc tài khoản hiện tại hoặc không còn tồn tại.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="empty"
            title="Không tìm thấy đơn hàng tương ứng"
            message="Kiểm tra danh sách đơn hàng của bạn hoặc quay lại mua sắm."
            actionLabel="Về danh sách đơn hàng"
            actionHref="/don-hang"
          />
        </section>
        <ProductAddonStrip title="Gợi ý sản phẩm cơ bản" products={suggestedProducts} />
      </div>
    );
  }

  const statusMeta = getOrderStatusInfo(order.status);
  const payMeta = getPaymentStatusInfo(order.payment_status, order.payment_method, order.status);
  const needsRetry = needsVietQrPayment(order);
  const currentStepIdx = getMilestoneIndex(order.status);
  const isTerminalSpecial =
    order.status === "CANCELLED" ||
    order.status === "RETURN_REQUESTED" ||
    order.status === "RETURNED";
  const allowCancel = canCancelOrder(order.status);
  const allowConfirm = canConfirmReceived(order.status);
  const allowReturn = canRequestReturn(order.status);

  const milestoneSteps = [
    {
      key: "confirm",
      label: order.status === "PENDING" ? "Chờ thanh toán" : "Đã xác nhận",
    },
    { key: "preparing", label: "Đang chuẩn bị" },
    { key: "shipping", label: "Đang giao hàng" },
    {
      key: "done",
      label: order.status === "COMPLETED" ? "Hoàn tất" : "Đã giao hàng",
    },
  ];

  const sortedTimeline = [...timeline].sort((a, b) => {
    const ta = a.created_at ? Date.parse(a.created_at) : 0;
    const tb = b.created_at ? Date.parse(b.created_at) : 0;
    return tb - ta;
  });

  const shipment = shipmentData?.shipment ?? null;
  const shipmentEvents = Array.isArray(shipmentData?.events)
    ? [...shipmentData.events].sort((a, b) => {
        const ta = a.occurred_at ? Date.parse(a.occurred_at) : 0;
        const tb = b.occurred_at ? Date.parse(b.occurred_at) : 0;
        return tb - ta;
      })
    : [];

  const shopName = shop?.name ?? "Gian hàng NexusMart";

  return (
    <div className={shared.page}>
      {renderBreadcrumb()}

      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${shared.pageTitle} lines num`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Chi tiết đơn {order.order_code}
              </span>
            </span>
          </h1>
          <p className={styles.detailLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Đơn hàng tại <strong>{shopName}</strong>
            {order.created_at ? (
              <>
                , đặt lúc <span className="num">{formatDateTimeVi(order.created_at)}</span>
              </>
            ) : null}
            . Theo dõi giao nhận và quản lý trạng thái đơn hàng.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>{payMeta.label}</span>
          <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
            {statusMeta.label}
          </span>
          {needsRetry && (
            <Button
              variant="primary"
              size="md"
              href={`/thanh-toan/cho-thanh-toan/${encodeURIComponent(order.payment_id)}`}
            >
              Thanh toán VietQR
            </Button>
          )}
          {order.items.length > 0 && (
            <Button
              variant="ink"
              size="md"
              disabled={reordering}
              onClick={() => void handleReorder()}
            >
              {reordering ? "Đang thêm vào giỏ..." : "Mua lại"}
            </Button>
          )}
        </div>
      </section>

      <section className={shared.section}>
        <div className={styles.detailGrid}>
          {/* Left column: VietQR retry highlight + ONE white order sheet + unboxed state-gated actions */}
          <div className={styles.mainCol}>
            {needsRetry && (
              <div
                className={styles.retryStack}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <RetryPaymentBox
                  paymentId={order.payment_id}
                  onPaid={() => {
                    showToast(`Đơn ${order.order_code} đã hoàn tất thanh toán.`);
                    void refreshOrder();
                  }}
                />
              </div>
            )}

            <div className={styles.orderSheet}>
              {/* Timeline section */}
              <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Tiến trình đơn hàng</h2>
                  {order.updated_at && (
                    <span className={`${shared.sectionNote} num`}>
                      Cập nhật: {formatDateTimeVi(order.updated_at)}
                    </span>
                  )}
                </div>

                {!isTerminalSpecial ? (
                  <div className={styles.milestoneGrid}>
                    {milestoneSteps.map((step, idx) => {
                      const done =
                        currentStepIdx > idx ||
                        (order.status === "COMPLETED" && idx === milestoneSteps.length - 1);
                      const current = currentStepIdx === idx && order.status !== "COMPLETED";
                      return (
                        <div
                          key={step.key}
                          className={`${styles.milestoneStep} ${
                            current ? styles.milestoneCurrent : done ? styles.milestoneDone : ""
                          }`}
                        >
                          <span className={styles.milestoneLabel}>{step.label}</span>
                          <span className={styles.milestoneSub}>
                            {current ? "Hiện tại" : done ? "Hoàn tất" : "Sắp tới"}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <div className={styles.reasonBanner}>
                    <p>
                      Trạng thái đơn hàng: <strong>{statusMeta.label}</strong>
                    </p>
                    <p>{order.cancel_reason || statusMeta.description}</p>
                  </div>
                )}

                {sortedTimeline.length > 0 ? (
                  <ol className={styles.eventList}>
                    {sortedTimeline.map((ev, idx) => (
                      <li
                        key={ev.id || `${ev.created_at ?? idx}-${idx}`}
                        className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                        data-reveal=""
                      >
                        <span className={`${styles.eventTime} num`}>
                          {ev.created_at ? formatDateTimeVi(ev.created_at) : "—"}
                        </span>
                        <div className={styles.eventBody}>
                          <span className={styles.eventTitle}>{getTimelineEventTitle(ev)}</span>
                          {ev.note && (
                            <span className={styles.eventNote}>
                              {localizeTimelineNote(ev.note)}
                            </span>
                          )}
                        </div>
                      </li>
                    ))}
                  </ol>
                ) : (
                  <p className={styles.sideMuted} style={{ marginTop: 16 }}>
                    Chưa có mốc cập nhật chi tiết nào cho đơn hàng này.
                  </p>
                )}
              </div>

              {/* Real shipment block — hidden when no shipment exists */}
              {shipment && (
                <div className={styles.sheetDivider} data-reveal="">
                  <div className={shared.sectionHead}>
                    <h2 className={shared.sectionTitle}>Thông tin vận đơn</h2>
                    <span
                      className={`${shared.statusTag} ${toneClass(
                        getShipmentStatusInfo(shipment.status).tone
                      )}`}
                    >
                      {getShipmentStatusInfo(shipment.status).label}
                    </span>
                  </div>

                  <dl className={styles.shipmentMetaGrid}>
                    <div className={styles.shipmentMetaItem}>
                      <dt className={styles.shipmentKey}>Đơn vị vận chuyển</dt>
                      <dd className={styles.shipmentVal}>
                        {getCarrierLabel(shipment.carrier_code)}
                      </dd>
                    </div>
                    <div className={styles.shipmentMetaItem}>
                      <dt className={styles.shipmentKey}>Mã vận đơn</dt>
                      <dd className={`${styles.shipmentVal} num`}>
                        {shipment.tracking_code || "Đang cấp mã"}
                      </dd>
                    </div>
                    <div className={styles.shipmentMetaItem}>
                      <dt className={styles.shipmentKey}>Phí giao hàng</dt>
                      <dd className={`${styles.shipmentVal} num`}>{formatVnd(shipment.fee_vnd)}</dd>
                    </div>
                    {shipment.cod_amount_vnd > 0 && (
                      <div className={styles.shipmentMetaItem}>
                        <dt className={styles.shipmentKey}>Thu hộ COD</dt>
                        <dd className={`${styles.shipmentVal} num`}>
                          {formatVnd(shipment.cod_amount_vnd)}
                        </dd>
                      </div>
                    )}
                  </dl>

                  {shipment.label_url && isSafeExternalUrl(shipment.label_url) && (
                    <div className={shared.formActions} style={{ marginTop: 12 }}>
                      <a
                        href={shipment.label_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className={shared.breadcrumbLink}
                      >
                        Xem phiếu giao hàng
                      </a>
                    </div>
                  )}

                  {shipmentEvents.length > 0 && (
                    <ol className={styles.eventList}>
                      {shipmentEvents.map((ev, idx) => (
                        <li
                          key={ev.id || `${ev.occurred_at ?? idx}-${idx}`}
                          className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                        >
                          <span className={`${styles.eventTime} num`}>
                            {ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : "—"}
                          </span>
                          <div className={styles.eventBody}>
                            <span className={styles.eventTitle}>
                              {getShipmentStatusInfo(ev.status).label}
                            </span>
                            {ev.description && (
                              <span className={styles.eventNote}>{ev.description}</span>
                            )}
                          </div>
                        </li>
                      ))}
                    </ol>
                  )}
                </div>
              )}

              {/* Ordered products */}
              <div
                className={styles.sheetDivider}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm đã đặt</h2>
                  {shop?.slug && (
                    <Link href={`/gian-hang/${shop.slug}`} className={shared.breadcrumbLink}>
                      Xem gian hàng {shop.name}
                    </Link>
                  )}
                </div>

                <div className={styles.itemStack}>
                  {order.items.map((item) => {
                    const prodVisual = visuals.byProductId[item.product_id];
                    const thumbUrl = visuals.bySkuId[item.sku_id] ?? prodVisual?.image;
                    const productHref = prodVisual?.slug ? `/san-pham/${prodVisual.slug}` : null;
                    const effectivePrice = item.effective_unit_price_vnd || item.unit_price_vnd;
                    const hasFlashDiscount =
                      item.unit_price_vnd > 0 && effectivePrice < item.unit_price_vnd;

                    return (
                      <div key={item.id || item.sku_id} className={styles.itemRow} data-reveal="">
                        {productHref ? (
                          <Link href={productHref} className={styles.itemThumbLink}>
                            {thumbUrl ? (
                              <img
                                src={thumbUrl}
                                alt={item.product_name}
                                width={896}
                                height={1200}
                                className={styles.itemThumb}
                              />
                            ) : (
                              <span className={styles.itemThumbPlaceholder} aria-hidden="true">
                                <IconPackage size={24} />
                              </span>
                            )}
                          </Link>
                        ) : (
                          <div className={styles.itemThumbLink}>
                            {thumbUrl ? (
                              <img
                                src={thumbUrl}
                                alt={item.product_name}
                                width={896}
                                height={1200}
                                className={styles.itemThumb}
                              />
                            ) : (
                              <span className={styles.itemThumbPlaceholder} aria-hidden="true">
                                <IconPackage size={24} />
                              </span>
                            )}
                          </div>
                        )}

                        <div className={styles.itemInfo}>
                          {productHref ? (
                            <Link href={productHref} className={styles.itemName}>
                              {item.product_name}
                            </Link>
                          ) : (
                            <span className={styles.itemName}>{item.product_name}</span>
                          )}
                          {item.sku_name && (
                            <span className={styles.itemMeta}>{item.sku_name}</span>
                          )}
                          <span className={`${styles.itemMeta} num`}>
                            {formatVnd(effectivePrice)} × {item.quantity}
                            {hasFlashDiscount && (
                              <>
                                {" "}
                                <span className={styles.priceOriginal}>
                                  {formatVnd(item.unit_price_vnd)}
                                </span>
                              </>
                            )}
                          </span>
                        </div>
                        <span className={`${styles.itemAmount} num`}>
                          {formatVnd(item.line_total_vnd)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            {/* Confirm received — ONLY allowed in DELIVERED per docs/contracts.md §6 */}
            {allowConfirm && (
              <div className={styles.confirmBox} data-reveal="">
                <h2 className={shared.sectionTitle}>Xác nhận đã nhận hàng</h2>
                <p className={styles.actionPanelDesc}>
                  Nếu bạn đã nhận đủ sản phẩm và hài lòng với chất lượng, hãy xác nhận để hoàn tất
                  đơn hàng và nhận điểm thưởng Xu vào tài khoản.
                </p>
                {confirmError && (
                  <p className={shared.fieldError} role="alert">
                    {confirmError}
                  </p>
                )}
                <div className={shared.formActions}>
                  <Button
                    variant="primary"
                    size="md"
                    disabled={confirming}
                    onClick={() => void handleConfirmReceived()}
                  >
                    {confirming ? "Đang xác nhận..." : "Đã nhận được hàng"}
                  </Button>
                </div>
              </div>
            )}

            {/* Cancel order — ONLY allowed in PENDING, CONFIRMED, PREPARING per docs/contracts.md §6 */}
            {allowCancel && (
              <form
                className={styles.unboxedSection}
                onSubmit={handleCancelSubmit}
                noValidate
                data-reveal=""
              >
                <h2 className={shared.sectionTitle}>Hủy đơn hàng</h2>
                {cancelGroupInfo.isMultiShopGroup ? (
                  <div className={`${shared.feedbackBox} ${shared.feedbackError}`}>
                    <strong>{formatUnpaidGroupCancelWarning(cancelGroupInfo.shopCount)}.</strong>{" "}
                    Các gian hàng trong lần đặt này dùng chung một mã chuyển khoản VietQR nên khi
                    hủy sẽ áp dụng cho toàn bộ đơn đang chờ thanh toán trong nhóm.
                  </div>
                ) : (
                  <p className={styles.actionPanelDesc}>
                    Bạn có thể hủy đơn trước khi bàn giao cho đơn vị vận chuyển. Nếu đơn đã thanh
                    toán trước, số tiền{" "}
                    <strong className="num">{formatVnd(order.total_payable_vnd)}</strong> sẽ được xử
                    lý hoàn lại theo quy định.
                  </p>
                )}

                <div className={shared.formGrid}>
                  <div className={`${shared.field} ${shared.formFull}`}>
                    <label htmlFor="cancel-reason" className={shared.fieldLabel}>
                      Lý do hủy đơn
                    </label>
                    <select
                      id="cancel-reason"
                      className={shared.select}
                      value={cancelReason}
                      onChange={(e) => setCancelReason(e.target.value)}
                      disabled={cancelling}
                    >
                      {CANCEL_REASONS.map((reason) => (
                        <option key={reason} value={reason}>
                          {reason}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className={`${shared.field} ${shared.formFull}`}>
                    <label htmlFor="cancel-note" className={shared.fieldLabel}>
                      Ghi chú thêm (không bắt buộc)
                    </label>
                    <input
                      id="cancel-note"
                      type="text"
                      className={shared.input}
                      value={cancelNote}
                      onChange={(e) => setCancelNote(e.target.value)}
                      placeholder="Nhập chi tiết nếu muốn đổi sản phẩm hoặc địa chỉ..."
                      disabled={cancelling}
                    />
                  </div>
                </div>

                {cancelError && (
                  <p className={shared.fieldError} role="alert">
                    {cancelError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button type="submit" variant="danger" size="md" disabled={cancelling}>
                    {cancelling ? "Đang hủy đơn..." : "Xác nhận hủy đơn"}
                  </Button>
                </div>
              </form>
            )}

            {groupOrders.length > 1 && (
              <div className={styles.unboxedSection}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>
                    Các đơn trong cùng lần đặt hàng ({groupOrders.length} gian hàng)
                  </h2>
                  <Link href="/don-hang" className={shared.breadcrumbLink}>
                    Xem danh sách đơn hàng
                  </Link>
                </div>
                <ul className={shared.confirmList}>
                  {groupOrders.map((sib) => {
                    const sibMeta = getOrderStatusInfo(sib.status);
                    const sibShopName =
                      groupShopsMap[sib.shop_id]?.name ??
                      (sib.shop_id === order.shop_id ? shopName : "Gian hàng NexusMart");
                    return (
                      <li key={sib.id} className={shared.confirmListItem}>
                        <span>
                          <Link href={`/don-hang/${sib.id}`} className={shared.breadcrumbLink}>
                            <strong className="num">{sib.order_code}</strong>
                          </Link>{" "}
                          · {sibShopName}
                        </span>
                        <span className={`${shared.statusTag} ${toneClass(sibMeta.tone)}`}>
                          {sibMeta.label}
                        </span>
                      </li>
                    );
                  })}
                </ul>
              </div>
            )}

            {/* Request return — ONLY allowed in DELIVERED per docs/contracts.md §6 */}
            {allowReturn && (
              <form
                className={styles.unboxedSection}
                onSubmit={(e) => void handleReturnSubmit(e)}
                noValidate
                data-reveal=""
              >
                <h2 className={shared.sectionTitle}>Yêu cầu trả hàng / hoàn tiền</h2>
                <p className={styles.actionPanelDesc}>
                  Nếu sản phẩm bị lỗi, giao nhầm mẫu hoặc không đúng kích cỡ, bạn có thể gửi yêu cầu
                  trả hàng trong vòng 7 ngày kể từ khi nhận hàng.
                </p>

                <div className={shared.field}>
                  <label htmlFor="return-reason" className={shared.fieldLabel}>
                    Lý do trả hàng
                  </label>
                  <select
                    id="return-reason"
                    className={shared.select}
                    value={returnReason}
                    onChange={(e) => setReturnReason(e.target.value)}
                    disabled={returning}
                  >
                    {RETURN_REASONS.map((reason) => (
                      <option key={reason} value={reason}>
                        {reason}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={shared.field}>
                  <label htmlFor="return-note" className={shared.fieldLabel}>
                    Mô tả chi tiết tình trạng sản phẩm
                  </label>
                  <textarea
                    id="return-note"
                    className={shared.textarea}
                    value={returnDetail}
                    onChange={(e) => setReturnDetail(e.target.value)}
                    placeholder="Ví dụ: Áo còn nguyên tem mác, cần trả hàng do giao nhầm size."
                    disabled={returning}
                  />
                </div>

                {returnError && (
                  <p className={shared.fieldError} role="alert">
                    {returnError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button type="submit" variant="ink" size="md" disabled={returning}>
                    {returning ? "Đang gửi yêu cầu..." : "Gửi yêu cầu trả hàng"}
                  </Button>
                </div>
              </form>
            )}
          </div>

          {/* Right column: ONE unified white summary sheet with dark total footer */}
          <aside className={styles.sideCol}>
            <div
              className={styles.summarySheet}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              {/* Delivery & Recipient */}
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Địa chỉ & Giao hàng</h2>
                {order.shipping_address ? (
                  <>
                    <p className={styles.sideLine}>
                      <strong>{order.shipping_address.recipient_name}</strong> ·{" "}
                      <span className="num">{order.shipping_address.recipient_phone}</span>
                    </p>
                    <p className={styles.sideLine}>
                      {[
                        order.shipping_address.street_address,
                        order.shipping_address.ward_name,
                        order.shipping_address.province_name,
                      ]
                        .filter(Boolean)
                        .join(", ")}
                    </p>
                  </>
                ) : (
                  <p className={styles.sideMuted}>Đang cập nhật địa chỉ nhận hàng.</p>
                )}

                {order.carrier_code && (
                  <p className={styles.sideMuted}>
                    Đơn vị giao: <strong>{getCarrierLabel(order.carrier_code)}</strong>
                  </p>
                )}
                {order.tracking_code && (
                  <p className={`${styles.sideLine} num`}>
                    Mã vận đơn: <strong>{order.tracking_code}</strong>
                  </p>
                )}
              </div>

              {/* Payment summary — strictly integer VND from backend */}
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Chi tiết thanh toán</h2>
                <p className={styles.sideLine}>{getPaymentMethodLabel(order.payment_method)}</p>
                <p className={styles.sideMuted}>
                  Trạng thái: <strong>{payMeta.label}</strong>
                </p>

                <dl className={styles.ledgerBox}>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Tạm tính tiền hàng</dt>
                    <dd className="num">{formatVnd(order.subtotal_vnd)}</dd>
                  </div>
                  {order.flash_sale_discount_vnd > 0 && (
                    <div className={styles.ledgerRow}>
                      <dt className={styles.ledgerLabel}>Giảm giá Flash Sale</dt>
                      <dd className={`${styles.ledgerDiscount} num`}>
                        {formatSignedVnd(-order.flash_sale_discount_vnd)}
                      </dd>
                    </div>
                  )}
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Voucher gian hàng</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.shop_voucher_discount_vnd > 0
                        ? formatSignedVnd(-order.shop_voucher_discount_vnd)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Voucher toàn sàn</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.platform_voucher_discount_vnd > 0
                        ? formatSignedVnd(-order.platform_voucher_discount_vnd)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                    <dd className="num">{formatVnd(order.shipping_fee_vnd)}</dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Giảm phí vận chuyển</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.freeship_discount_vnd > 0
                        ? formatSignedVnd(-order.freeship_discount_vnd)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Trừ Xu NexusMart</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.coins_discount_vnd > 0
                        ? formatSignedVnd(-order.coins_discount_vnd)
                        : "0₫"}
                    </dd>
                  </div>
                </dl>
              </div>

              <div className={styles.totalRow}>
                <span>Tổng thanh toán</span>
                <span className={`${styles.totalVal} num`}>
                  {formatVnd(order.total_payable_vnd)}
                </span>
              </div>
            </div>
          </aside>
        </div>
      </section>

      <ProductAddonStrip
        title="Gợi ý thêm đồ cơ bản trên NexusMart"
        subtitle="Đồ cơ bản từ các xưởng dệt Việt Nam, tiện mua thêm cho đơn kế tiếp"
        products={suggestedProducts}
      />

      {confirmGroupCancelOpen && cancelGroupInfo.isMultiShopGroup && (
        <div
          className={shared.confirmBackdrop}
          onClick={(ev) => {
            if (ev.target === ev.currentTarget && !cancelling) {
              setConfirmGroupCancelOpen(false);
            }
          }}
        >
          <div
            className={shared.confirmDialog}
            role="dialog"
            aria-modal="true"
            aria-labelledby="detail-cancel-dialog-title"
          >
            <h2 id="detail-cancel-dialog-title" className={shared.confirmTitle}>
              Xác nhận hủy lần đặt hàng
            </h2>
            <p className={shared.confirmBody}>
              <strong>{formatUnpaidGroupCancelWarning(cancelGroupInfo.shopCount)}.</strong>
            </p>
            <p className={shared.confirmBody}>
              Các đơn hàng đang chờ thanh toán chung mã VietQR bên dưới sẽ được hủy cùng lúc:
            </p>
            <ul className={shared.confirmList}>
              {cancelGroupInfo.groupOrders.map((sib) => (
                <li key={sib.id} className={shared.confirmListItem}>
                  <span>
                    <strong className="num">{sib.order_code}</strong> ·{" "}
                    {groupShopsMap[sib.shop_id]?.name ??
                      (sib.shop_id === order.shop_id ? shopName : "Gian hàng NexusMart")}
                  </span>
                  <span className="num">{formatVnd(sib.total_payable_vnd)}</span>
                </li>
              ))}
            </ul>
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
                disabled={cancelling}
                onClick={() => setConfirmGroupCancelOpen(false)}
              >
                Quay lại
              </Button>
              <Button
                type="button"
                variant="danger"
                size="md"
                disabled={cancelling}
                onClick={() => void executeCancelOrder()}
              >
                {cancelling ? "Đang hủy..." : `Đồng ý hủy cả ${cancelGroupInfo.shopCount} đơn`}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
