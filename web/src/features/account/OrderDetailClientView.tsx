"use client";

import React, { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type OrderStatus,
  type StatusTone,
} from "@/data/orders";
import type { Product } from "@/data/products";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderDetailClientView.module.css";
import type { OrderView } from "./orderView";

const STANDARD_STEPS: Array<{ status: OrderStatus; label: string }> = [
  { status: "cho_xac_nhan", label: "Chờ xác nhận" },
  { status: "dang_chuan_bi", label: "Đang chuẩn bị" },
  { status: "dang_giao", label: "Đang giao hàng" },
  { status: "da_giao", label: "Đã giao" },
];

const CANCEL_REASONS = [
  "Thay đổi địa chỉ nhận hàng sang tỉnh/phường khác",
  "Muốn đổi size hoặc chọn thêm màu khác",
  "Quên áp mã giảm giá của cửa hàng",
  "Đặt trùng đơn hàng, không còn nhu cầu",
];

const RETURN_REASONS = [
  "Cần đổi size khác để mặc vừa vai hơn",
  "Sản phẩm có lỗi đường may hoặc bề mặt vải",
  "Giao nhầm màu sắc hoặc nhầm mẫu so với đơn đặt",
  "Chất liệu thực tế chưa phù hợp nhu cầu sử dụng",
];

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

export interface OrderDetailClientViewProps {
  brandName: string;
  orderId: string;
  initialOrder: OrderView | null;
  suggestedProducts: Product[];
  initialError?: string | null;
}

function OrderDetailInner({
  brandName,
  orderId,
  initialOrder,
  suggestedProducts,
  initialError = null,
}: OrderDetailClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { addToCart, showToast } = useStore();

  const [order, setOrder] = useState<OrderView | null>(initialOrder);
  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);

  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);

  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [submittingReturn, setSubmittingReturn] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);

  const [retryingPayment, setRetryingPayment] = useState(false);
  const [retryError, setRetryError] = useState<string | null>(null);
  const [reordering, setReordering] = useState(false);

  useRevealOnScroll(
    `${viewState ?? "live"}-${order?.id ?? "none"}-${order?.status ?? ""}-${order?.paymentStatus ?? ""}`
  );

  const handleReorder = async () => {
    if (!order) return;
    setReordering(true);
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
          router.push(`/dang-nhap?next=${encodeURIComponent(`/don-hang/${order.id}`)}`);
          return;
        }
        if (res.ok) added += 1;
      }
      if (added > 0) {
        showToast(`Đã đưa lại ${added} sản phẩm vào giỏ hàng.`);
        router.push("/gio-hang");
      } else {
        showToast("Sản phẩm trong đơn hiện không còn sẵn hàng để mua lại.");
      }
    } finally {
      setReordering(false);
    }
  };

  const handleRetryVietQr = async () => {
    if (!order?.paymentId) return;
    setRetryError(null);
    setRetryingPayment(true);
    try {
      const res = await api<{ payment: { payment_id: string } }>(
        `/api/payments/${encodeURIComponent(order.paymentId)}/retry`,
        {
          method: "POST",
          body: { method: "VIETQR" },
        }
      );
      const nextId = res.payment?.payment_id || order.paymentId;
      router.push(`/thanh-toan/cho/${encodeURIComponent(nextId)}`);
    } catch (err) {
      setRetryError(
        err instanceof ApiError
          ? err.message
          : "Chưa tạo lại được mã VietQR lúc này. Bạn vui lòng thử lại."
      );
    } finally {
      setRetryingPayment(false);
    }
  };

  const handleCancelSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    setCancelError(null);
    setCancelling(true);
    try {
      const res = await api<{ order: OrderView }>(
        `/api/orders/${encodeURIComponent(order.id)}/cancel`,
        {
          method: "POST",
          body: { reason: cancelReason },
        }
      );
      setOrder(res.order);
      showToast(`Đã hủy đơn hàng ${res.order.orderCode}.`);
      router.refresh();
    } catch (err) {
      setCancelError(
        err instanceof ApiError ? err.message : "Chưa hủy được đơn hàng lúc này. Vui lòng thử lại."
      );
    } finally {
      setCancelling(false);
    }
  };

  const handleConfirmReceived = async () => {
    if (!order) return;
    setConfirmError(null);
    setConfirming(true);
    try {
      const res = await api<{ order: OrderView }>(
        `/api/orders/${encodeURIComponent(order.id)}/confirm-received`,
        { method: "POST" }
      );
      setOrder(res.order);
      showToast(`Đã xác nhận nhận hàng. Cảm ơn bạn đã mua sắm tại ${brandName}.`);
      router.refresh();
    } catch (err) {
      setConfirmError(
        err instanceof ApiError ? err.message : "Chưa xác nhận được lúc này. Bạn vui lòng thử lại."
      );
    } finally {
      setConfirming(false);
    }
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    const cleanNote = returnDetail.trim();
    if (cleanNote.length < 6) {
      setReturnError(
        "Vui lòng mô tả ngắn tình trạng sản phẩm hoặc size cần đổi (tối thiểu 6 ký tự)."
      );
      return;
    }
    setReturnError(null);
    setSubmittingReturn(true);
    try {
      const res = await api<{ order: OrderView; returnRequestId: string }>(
        `/api/orders/${encodeURIComponent(order.id)}/return`,
        {
          method: "POST",
          body: { reason: `${returnReason}. ${cleanNote}` },
        }
      );
      setOrder(res.order);
      setReturnDetail("");
      showToast(`Đã gửi yêu cầu đổi trả tới ${brandName}.`);
      router.refresh();
    } catch (err) {
      setReturnError(
        err instanceof ApiError
          ? err.message
          : "Chưa gửi được yêu cầu đổi trả lúc này. Vui lòng thử lại."
      );
    } finally {
      setSubmittingReturn(false);
    }
  };

  const renderBreadcrumb = () => (
    <nav className={shared.breadcrumbNav} aria-label="Đường dẫn đơn hàng">
      <Link href="/don-hang" className={shared.breadcrumbLink}>
        Đơn hàng của tôi
      </Link>
      <span aria-hidden="true">/</span>
      <span className="num">{order?.orderCode ?? orderId}</span>
    </nav>
  );

  if (viewState === "loading") {
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
      </div>
    );
  }

  if (viewState === "error" || initialError) {
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
              initialError ?? "Hệ thống theo dõi đơn hàng đang tạm gián đoạn. Vui lòng thử tải lại."
            }
            actionLabel="Quay lại danh sách đơn"
            actionHref="/don-hang"
          />
        </section>
      </div>
    );
  }

  if (viewState === "empty" || !order) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Không tìm thấy đơn</h1>
            <p className={styles.detailLead}>
              Mã đơn hàng bạn mở không thuộc tài khoản hiện tại tại {brandName}.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="empty"
            title="Không tìm thấy đơn hàng tương ứng"
            message="Quay lại danh sách đơn hàng của tài khoản để kiểm tra lại."
            actionLabel="Về danh sách đơn hàng"
            actionHref="/don-hang"
          />
        </section>
      </div>
    );
  }

  const statusMeta = ORDER_STATUS_META[order.status];
  const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
  const isUnpaidQr =
    order.paymentMethod === "vietqr" &&
    order.status !== "da_huy" &&
    (order.paymentStatus === "cho_thanh_toan" || order.paymentStatus === "thanh_toan_that_bai") &&
    Boolean(order.paymentId);
  const currentStepIdx = STANDARD_STEPS.findIndex((s) => s.status === order.status);
  const isTerminalSpecial = order.status === "da_huy" || order.status === "yeu_cau_tra_hang";

  return (
    <div className={shared.page}>
      {renderBreadcrumb()}

      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${shared.pageTitle} lines num`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Chi tiết đơn {order.orderCode}
              </span>
            </span>
          </h1>
          <p className={styles.detailLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Đơn hàng tại <strong>{brandName}</strong>
            {order.createdAt ? (
              <>
                , đặt lúc <span className="num">{formatDateTimeVi(order.createdAt)}</span>
              </>
            ) : null}
            . Theo dõi giao nhận và hỗ trợ sau mua.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          {payMeta && (
            <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
              {payMeta.label}
            </span>
          )}
          <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
            {statusMeta.label}
          </span>
          <Button
            variant="ink"
            size="md"
            disabled={reordering}
            onClick={() => void handleReorder()}
          >
            {reordering ? "Đang thêm..." : "Mua lại"}
          </Button>
        </div>
      </section>

      <section className={shared.section}>
        <div className={styles.detailGrid}>
          <div className={styles.mainCol}>
            {isUnpaidQr && (
              <div
                className={styles.vietQrBanner}
                role="region"
                aria-label="Hoàn tất thanh toán VietQR"
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div className={styles.notifyHead}>
                  <strong>Đơn {order.orderCode} đang chờ hoàn tất thanh toán VietQR</strong>
                  <span className={`${shared.statusTag} ${shared.toneWarn} num`}>
                    Cần thanh toán: {formatVnd(order.total)}
                  </span>
                </div>
                <p className={styles.actionPanelDesc}>
                  Mở trang quét mã VietQR để hoàn tất chuyển khoản
                  {order.paymentDueAt ? ` trước ${formatDateTimeVi(order.paymentDueAt)}` : ""}. Hệ
                  thống sẽ tự xác nhận đơn ngay khi nhận được thanh toán.
                </p>
                {retryError && (
                  <p className={shared.fieldError} role="alert">
                    {retryError}
                  </p>
                )}
                <div className={shared.formActions}>
                  <Button
                    variant="primary"
                    size="md"
                    href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}`}
                  >
                    Mở trang thanh toán VietQR
                  </Button>
                  {order.paymentStatus === "thanh_toan_that_bai" && (
                    <Button
                      variant="secondary"
                      size="md"
                      disabled={retryingPayment}
                      onClick={() => void handleRetryVietQr()}
                    >
                      {retryingPayment ? "Đang tạo mã mới..." : "Tạo lại mã VietQR"}
                    </Button>
                  )}
                </div>
              </div>
            )}

            <div className={styles.orderSheet}>
              <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Tiến trình đơn hàng</h2>
                  {order.updatedAt && (
                    <span className={`${shared.sectionNote} num`}>
                      Cập nhật: {formatDateTimeVi(order.updatedAt)}
                    </span>
                  )}
                </div>

                {!isTerminalSpecial ? (
                  <div className={styles.milestoneGrid}>
                    {STANDARD_STEPS.map((step, idx) => {
                      const done = currentStepIdx > idx;
                      const current = currentStepIdx === idx;
                      return (
                        <div
                          key={step.status}
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
                      Trạng thái xử lý: <strong>{statusMeta.label}</strong>
                    </p>
                    <p>{order.returnReason ?? order.cancelReason ?? statusMeta.description}</p>
                  </div>
                )}

                {order.timeline.length > 0 && (
                  <ol className={styles.eventList}>
                    {order.timeline
                      .slice()
                      .reverse()
                      .map((ev, idx) => (
                        <li
                          key={`${ev.timestamp}-${idx}`}
                          className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                          data-reveal=""
                        >
                          <span className={`${styles.eventTime} num`}>
                            {ev.timestamp ? formatDateTimeVi(ev.timestamp) : ""}
                          </span>
                          <div className={styles.eventBody}>
                            <span className={styles.eventTitle}>{ev.title}</span>
                            <span className={styles.eventNote}>{ev.note}</span>
                          </div>
                        </li>
                      ))}
                  </ol>
                )}
              </div>

              <div
                className={styles.sheetDivider}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm đã đặt</h2>
                  <Link href="/san-pham" className={shared.breadcrumbLink}>
                    Xem thêm tại {brandName}
                  </Link>
                </div>

                <div className={styles.itemStack}>
                  {order.items.map((item) => {
                    const productHref = `/san-pham/${item.productSlug || item.productId}`;
                    return (
                      <div key={item.skuCode} className={styles.itemRow} data-reveal="">
                        <Link href={productHref} className={styles.itemThumbLink}>
                          <img
                            src={item.image || "/products/ao-thun-cotton-trang.png"}
                            alt={item.productName}
                            width={896}
                            height={1200}
                            className={styles.itemThumb}
                          />
                        </Link>
                        <div className={styles.itemInfo}>
                          <Link href={productHref} className={styles.itemName}>
                            {item.productName}
                          </Link>
                          <span className={styles.itemMeta}>
                            {item.colorName}
                            {item.size ? ` · Size ${item.size}` : ""}
                          </span>
                          <span className={`${styles.itemMeta} num`}>
                            {formatVnd(item.unitPrice)} × {item.quantity}
                          </span>
                        </div>
                        <span className={`${styles.itemAmount} num`}>
                          {formatVnd(item.lineTotal)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            {order.canConfirmReceived && (
              <div className={styles.unboxedSection} data-reveal="">
                <h2 className={shared.sectionTitle}>Xác nhận đã nhận hàng</h2>
                <p className={styles.actionPanelDesc}>
                  Nếu bạn đã kiểm tra đủ sản phẩm và hài lòng với kiện hàng, hãy xác nhận để hoàn
                  tất đơn hàng.
                </p>
                {confirmError && (
                  <p className={shared.fieldError} role="alert">
                    {confirmError}
                  </p>
                )}
                <div className={shared.formActions}>
                  <Button
                    type="button"
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

            {order.canCancel && (
              <form className={styles.unboxedSection} onSubmit={handleCancelSubmit} data-reveal="">
                <h2 className={shared.sectionTitle}>Hủy đơn trước khi bàn giao vận chuyển</h2>
                <p className={styles.actionPanelDesc}>
                  Đơn chưa bàn giao cho đơn vị vận chuyển nên bạn có thể hủy ngay. Với đơn đã thanh
                  toán trước, số tiền <strong className="num">{formatVnd(order.total)}</strong> sẽ
                  được hoàn về tài khoản gốc.
                </p>

                <div className={shared.field}>
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

            {order.canRequestReturn && (
              <form
                className={styles.unboxedSection}
                onSubmit={handleReturnSubmit}
                noValidate
                data-reveal=""
              >
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Gửi yêu cầu đổi trả hàng</h2>
                  <span className={`${shared.statusTag} ${shared.tonePos}`}>Hỗ trợ sau mua</span>
                </div>
                <p className={styles.actionPanelDesc}>
                  Nếu sản phẩm chưa vừa size hoặc có lỗi sản xuất, hãy gửi yêu cầu để {brandName}{" "}
                  tiếp nhận đổi trả hoặc hoàn tiền.
                </p>

                <div className={shared.field}>
                  <label htmlFor="return-reason" className={shared.fieldLabel}>
                    Lý do đổi trả
                  </label>
                  <select
                    id="return-reason"
                    className={shared.select}
                    value={returnReason}
                    onChange={(e) => setReturnReason(e.target.value)}
                    disabled={submittingReturn}
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
                    Mô tả tình trạng sản phẩm hoặc size cần đổi
                  </label>
                  <textarea
                    id="return-note"
                    className={shared.textarea}
                    value={returnDetail}
                    onChange={(e) => setReturnDetail(e.target.value)}
                    placeholder="Ví dụ: Áo còn nguyên tem mác, cần đổi từ size M sang size L."
                    disabled={submittingReturn}
                  />
                </div>

                {returnError && (
                  <p className={shared.fieldError} role="alert">
                    {returnError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button type="submit" variant="primary" size="md" disabled={submittingReturn}>
                    {submittingReturn ? "Đang gửi yêu cầu..." : "Gửi yêu cầu đổi trả"}
                  </Button>
                </div>
              </form>
            )}
          </div>

          <aside className={styles.sideCol}>
            <div
              className={styles.summarySheet}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Địa chỉ & Giao hàng</h2>
                <p className={styles.sideLine}>
                  <strong>{order.buyerName || "Người nhận"}</strong>
                  {order.buyerPhone ? ` · ${order.buyerPhone}` : ""}
                </p>
                <p className={styles.sideLine}>
                  {order.shippingAddress || "Đang cập nhật địa chỉ"}
                </p>
                {order.trackingCode && (
                  <p className={`${styles.sideLine} num`}>
                    {order.carrier ? `${order.carrier} · ` : ""}Mã vận đơn:{" "}
                    <strong>{order.trackingCode}</strong>
                  </p>
                )}
              </div>

              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Chi tiết thanh toán</h2>
                <p className={styles.sideLine}>{PAYMENT_METHOD_LABELS[order.paymentMethod]}</p>
                <p className={styles.sideMuted}>
                  Trạng thái:{" "}
                  <strong>
                    {payMeta?.label ?? (order.isPaid ? "Đã thanh toán" : "Thu hộ khi nhận hàng")}
                  </strong>
                </p>

                <dl className={styles.ledgerBox}>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Tạm tính tiền hàng</dt>
                    <dd className="num">{formatVnd(order.subtotal)}</dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Mã giảm giá cửa hàng</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.shopVoucherDiscount > 0
                        ? formatSignedVnd(-order.shopVoucherDiscount)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                    <dd className="num">{formatVnd(order.shippingFee)}</dd>
                  </div>
                </dl>
              </div>

              <div className={styles.totalRow}>
                <span>Tổng thanh toán</span>
                <span className={`${styles.totalVal} num`}>{formatVnd(order.total)}</span>
              </div>
            </div>
          </aside>
        </div>
      </section>

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip
          title={`Gợi ý thêm từ ${brandName}`}
          subtitle="Sản phẩm đang sẵn hàng tại kho của cửa hàng"
          products={suggestedProducts}
        />
      )}
    </div>
  );
}

export function OrderDetailClientView(props: OrderDetailClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải chi tiết đơn hàng..." rows={5} />
        </div>
      }
    >
      <OrderDetailInner {...props} />
    </Suspense>
  );
}
