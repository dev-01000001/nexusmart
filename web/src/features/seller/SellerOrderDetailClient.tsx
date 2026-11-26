"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Button,
  IconArrowLeft,
  IconArrowUpRight,
  IconCheck,
  IconCopy,
  IconRefresh,
  StateView,
} from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import { formatCarrierLabel, formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta, normalizeSalesChannel } from "@/lib/salesChannel";
import {
  buildShippingLabelHref,
  getReturnStatusInfo,
  getShipmentStatusInfo,
  getTimelineEventTitle,
  isAddressUpdatedTimelineNote,
  localizeTimelineNote,
} from "@/features/account/orderMeta";
import {
  buildCarrierSelectOptions,
  buildSellerOrderBreakdownLines,
  canSellerCancelOrder,
  canSellerConfirmOrder,
  canSellerShipOrder,
  resolveOrderShippingDefaults,
  translateSellerShipError,
} from "./types";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getSellerOrderStatusMeta,
  normalizeSellerShipmentResponse,
  resolveSellerShipmentFeeVnd,
  type GwOrderShipment,
  type GwSellerOrder,
  type GwShipmentEvent,
} from "./sellerApiTypes";
import { useSellerContext } from "./SellerShell";
import { statusPillClass } from "./SellerUiPrimitives";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/don-hang/[id]/page.module.css";

const EMPTY_DASH = "—";

export interface SellerOrderDetailClientProps {
  initialOrder: GwSellerOrder | null;
  initialShipment: GwOrderShipment | null;
  initialShipmentEvents?: GwShipmentEvent[];
  loadError: string | null;
}

export function SellerOrderDetailClient({
  initialOrder,
  initialShipment,
  initialShipmentEvents = [],
  loadError,
}: SellerOrderDetailClientProps) {
  const router = useRouter();
  const { showToast } = useSellerContext();

  const [order, setOrder] = useState<GwSellerOrder | null>(initialOrder);
  const [shipment, setShipment] = useState<GwOrderShipment | null>(initialShipment);
  const [shipmentEvents, setShipmentEvents] = useState<GwShipmentEvent[]>(() =>
    initialShipmentEvents.length > 0 ? initialShipmentEvents : (initialShipment?.events ?? [])
  );
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  const initialDefaults = useMemo(() => resolveOrderShippingDefaults(initialOrder), [initialOrder]);
  const [carrierCode, setCarrierCode] = useState(initialDefaults.carrierCode);
  const [serviceCode, setServiceCode] = useState(initialDefaults.serviceCode);

  const [cancelOpen, setCancelOpen] = useState(false);
  const [cancelReason, setCancelReason] = useState("");
  const [cancelError, setCancelError] = useState<string | null>(null);

  const pollCountRef = useRef(0);
  const [pollTick, setPollTick] = useState(0);

  const reloadDetail = useCallback(
    async (opts?: { silent?: boolean }) => {
      if (!order?.id) return;
      if (!opts?.silent) setRefreshing(true);
      try {
        const [ordRes, shipRes] = await Promise.all([
          api<{ order: GwSellerOrder }>(`/api/seller/orders/${encodeURIComponent(order.id)}`),
          api<unknown>(`/api/seller/orders/${encodeURIComponent(order.id)}/shipment`).catch(
            () => null
          ),
        ]);
        if (ordRes.order) setOrder(ordRes.order);
        if (shipRes !== null) {
          const parsedShip = normalizeSellerShipmentResponse(shipRes);
          setShipment(parsedShip.shipment);
          setShipmentEvents(parsedShip.events);
        }
      } catch (err) {
        if (!opts?.silent) {
          showToast(getErrorMessage(err, "Không thể tải lại chi tiết đơn hàng."));
        }
      } finally {
        if (!opts?.silent) setRefreshing(false);
      }
    },
    [order?.id, showToast]
  );

  // Silent poll while shipment job is QUEUED
  const isShipmentQueued =
    Boolean(order) &&
    !order?.tracking_code &&
    (order?.shipment_job_status ?? "").trim().toUpperCase() === "QUEUED";

  useEffect(() => {
    if (!isShipmentQueued) {
      pollCountRef.current = 0;
      return;
    }
    if (pollCountRef.current >= 20) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      pollCountRef.current += 1;
      void reloadDetail({ silent: true }).finally(() => {
        if (!cancelled) {
          setPollTick((v) => v + 1);
        }
      });
    }, 3000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [isShipmentQueued, reloadDetail, pollTick]);

  const carrierOptions = useMemo(() => buildCarrierSelectOptions(order), [order]);
  const activeCarrierOption = useMemo(
    () => carrierOptions.find((c) => c.carrierCode === carrierCode) ?? carrierOptions[0],
    [carrierOptions, carrierCode]
  );

  if (loadError) {
    return (
      <div className={common.page}>
        <Link href="/seller/don-hang" className={styles.backLink}>
          <IconArrowLeft size={14} />
          <span>Quay lại danh sách đơn hàng</span>
        </Link>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải chi tiết đơn hàng"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  if (!order) {
    return (
      <div className={common.page}>
        <Link href="/seller/don-hang" className={styles.backLink}>
          <IconArrowLeft size={14} />
          <span>Quay lại danh sách đơn hàng</span>
        </Link>
        <StateView
          kind="empty"
          surface="app"
          title="Không tìm thấy đơn hàng"
          message="Đơn hàng không tồn tại hoặc không thuộc gian hàng của bạn."
          actionLabel="Về danh sách đơn hàng"
          actionHref="/seller/don-hang"
        />
      </div>
    );
  }

  const statusMeta = getSellerOrderStatusMeta(
    order.status,
    order.shipment_job_status,
    order.tracking_code
  );
  const jobStatus = (order.shipment_job_status ?? "").trim().toUpperCase();
  const isShipmentFailed = !order.tracking_code && jobStatus === "FAILED";
  const canConfirm = canSellerConfirmOrder(order.status, order.shipment_job_status);
  const canShip = canSellerShipOrder(order.status, order.tracking_code);
  const canCancel = canSellerCancelOrder(order.status);

  const netReceivable = computeSellerNetReceivable(order);
  const breakdownLines = buildSellerOrderBreakdownLines(order);
  const totalQty = (order.items ?? []).reduce((s, it) => s + Math.max(0, it.quantity), 0);
  const labelHref = buildShippingLabelHref(order.tracking_code);
  const channel = normalizeSalesChannel(order.sales_channel);
  const channelBadge = getSalesChannelMeta(channel);
  const localizedCancelReason = localizeTimelineNote(order.cancel_reason ?? "");
  const hasReturnLink =
    Boolean(order.return_request_id) ||
    order.status === "RETURN_REQUESTED" ||
    order.status === "RETURNED" ||
    order.status === "REFUNDED";

  const handleCopyCode = async () => {
    try {
      await navigator.clipboard.writeText(order.order_code);
      showToast(`Đã sao chép mã đơn ${order.order_code}.`);
    } catch {
      showToast("Không thể sao chép mã đơn.");
    }
  };

  const handleConfirm = async () => {
    if (!canConfirm || busy) return;
    setBusy(true);
    try {
      const res = await api<{ order: GwSellerOrder }>(
        `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`,
        { method: "POST" }
      );
      if (res.order) setOrder(res.order);
      router.refresh();
      showToast(`Đã xác nhận đơn ${order.order_code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const handleShip = async () => {
    if (!canShip || busy) return;
    setBusy(true);
    try {
      const res = await api<{
        order: GwSellerOrder;
        tracking_code?: string;
        queued?: boolean;
      }>(`/api/seller/orders/${encodeURIComponent(order.id)}/ship`, {
        method: "POST",
        body: {
          carrier_code: carrierCode,
          service_code: serviceCode,
        },
      });
      if (res.order) setOrder(res.order);
      await reloadDetail({ silent: true });
      router.refresh();
      if (res.queued) {
        showToast(
          "Hãng vận chuyển đang tạm gián đoạn. Đơn chuyển sang Đang chờ tạo vận đơn — bạn có thể thử tạo lại ngay."
        );
      } else {
        showToast(
          res.tracking_code
            ? `Đã tạo vận đơn ${res.tracking_code}.`
            : `Đã tạo vận đơn cho đơn ${order.order_code}.`
        );
      }
    } catch (err) {
      showToast(translateSellerShipError(err));
    } finally {
      setBusy(false);
    }
  };

  const handleCancelOrder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canCancel || busy) return;
    const reason = cancelReason.trim();
    if (reason.length < 3) {
      setCancelError("Vui lòng nhập lý do hủy đơn (tối thiểu 3 ký tự).");
      return;
    }
    setCancelError(null);
    setBusy(true);
    try {
      const res = await api<{ order: GwSellerOrder }>(
        `/api/seller/orders/${encodeURIComponent(order.id)}/cancel`,
        {
          method: "POST",
          body: { reason },
        }
      );
      if (res.order) setOrder(res.order);
      setCancelOpen(false);
      setCancelReason("");
      router.refresh();
      showToast(`Đã hủy đơn ${order.order_code}.`);
    } catch (err) {
      setCancelError(getErrorMessage(err, "Không thể hủy đơn hàng lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const shipmentStatusInfo = shipment?.status ? getShipmentStatusInfo(shipment.status) : null;
  const shipmentFeeVnd = resolveSellerShipmentFeeVnd(shipment);
  const paymentStatusLabel =
    order.payment_status || (order.payment_method ?? "").toUpperCase() === "COD"
      ? formatPaymentStatusVi(order.payment_status ?? "", order.payment_method, order.status)
      : "";

  return (
    <div className={common.page}>
      <Link href="/seller/don-hang" className={styles.backLink}>
        <IconArrowLeft size={14} />
        <span>Đơn hàng</span>
      </Link>

      {/* Object Header */}
      <header className={styles.objectHeader}>
        <div className={styles.objectIdentity}>
          <div className={styles.objectText}>
            <div className={styles.objectTitleRow}>
              <h1 className={styles.objectTitle}>{order.order_code}</h1>
              <button
                type="button"
                className={styles.iconBtn28}
                onClick={() => void handleCopyCode()}
                aria-label="Sao chép mã đơn hàng"
                title="Sao chép mã đơn"
              >
                <IconCopy size={14} />
              </button>
              <span className={statusPillClass(statusMeta.tone)}>{statusMeta.label}</span>
              <span
                className={`${common.channelTag} ${
                  channel === "STOREFRONT" ? common.channelTagStorefront : ""
                }`}
              >
                {channelBadge.label}
              </span>
            </div>
            <span className={styles.objectMeta}>
              Đặt lúc {order.created_at ? formatDateTimeVi(order.created_at) : EMPTY_DASH} ·{" "}
              {formatPaymentMethodVi(order.payment_method)}
              {paymentStatusLabel ? ` (${paymentStatusLabel})` : ""}
            </span>
          </div>
        </div>

        <div className={styles.objectActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={refreshing}
            onClick={() => void reloadDetail()}
          >
            <IconRefresh size={14} />
            <span>Làm mới</span>
          </Button>
          {labelHref && (
            <a
              href={labelHref}
              target="_blank"
              rel="noopener noreferrer"
              className={common.cardLink}
              title="Mở nhãn vận đơn ở tab mới — nhấn Ctrl+P để in"
            >
              In nhãn (Ctrl+P)
            </a>
          )}
          {canConfirm && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              disabled={busy}
              onClick={() => void handleConfirm()}
            >
              <IconCheck size={14} />
              <span>{busy ? "Đang xác nhận..." : "Xác nhận đơn"}</span>
            </Button>
          )}
          {canCancel && (
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={busy}
              onClick={() => {
                setCancelOpen((prev) => !prev);
                setCancelError(null);
              }}
            >
              {cancelOpen ? "Đóng hủy đơn" : "Hủy đơn"}
            </Button>
          )}
        </div>
      </header>

      {/* Async Shipment Job Banner (QUEUED / FAILED) */}
      {(isShipmentQueued || isShipmentFailed) && (
        <section
          className={common.suspendedBanner}
          role="status"
          aria-label="Trạng thái tạo vận đơn"
        >
          <span className={common.suspendedBannerTitle}>
            {isShipmentQueued
              ? "Đơn đang chờ tạo vận đơn với hãng vận chuyển"
              : "Tạo vận đơn với hãng thất bại"}
          </span>
          <span className={common.suspendedBannerText}>
            {isShipmentQueued
              ? "Hệ thống đang tự động thử lại theo chu kỳ. Bạn cũng có thể bấm Thử tạo lại ngay bên dưới."
              : "Vui lòng kiểm tra địa chỉ kho lấy hàng và thử tạo lại vận đơn."}
          </span>
        </section>
      )}

      {/* Cancel Order Reason Form */}
      {cancelOpen && canCancel && (
        <form className={common.card} onSubmit={(e) => void handleCancelOrder(e)}>
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Hủy đơn hàng {order.order_code}</h2>
          </div>
          <div className={styles.opsReplyBox}>
            <div className={common.field}>
              <label htmlFor="seller-cancel-reason" className={common.fieldLabel}>
                Lý do hủy đơn (gửi cho người mua)
              </label>
              <textarea
                id="seller-cancel-reason"
                className={common.textArea}
                value={cancelReason}
                onChange={(e) => setCancelReason(e.target.value)}
                placeholder="Ví dụ: Hết hàng đột xuất hoặc không thể đáp ứng thời gian giao..."
                disabled={busy}
              />
            </div>
            {cancelError && (
              <p className={common.fieldError} role="alert">
                {cancelError}
              </p>
            )}
            <div className={styles.opsActionBar}>
              <Button
                type="button"
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => setCancelOpen(false)}
              >
                Đóng
              </Button>
              <Button type="submit" variant="danger" size="sm" shape="app" disabled={busy}>
                {busy ? "Đang hủy..." : "Xác nhận hủy đơn"}
              </Button>
            </div>
          </div>
        </form>
      )}

      {/* Return link banner if order has a return request */}
      {hasReturnLink && (
        <section className={common.card} aria-label="Thông tin yêu cầu trả hàng">
          <div className={common.cardHeader} style={{ marginBottom: 0 }}>
            <div>
              <h2 className={common.cardTitle}>Yêu cầu trả hàng / hoàn tiền</h2>
              <span className={common.cardMeta}>
                {order.return_status
                  ? `Trạng thái: ${getReturnStatusInfo(order.return_status).label}`
                  : "Đơn hàng có hồ sơ trả hàng đang được xử lý tại mục Trả hàng."}
              </span>
            </div>
            <Button
              variant="ink"
              size="sm"
              shape="app"
              href={
                order.return_request_id
                  ? `/seller/tra-hang?selected=${encodeURIComponent(order.return_request_id)}`
                  : "/seller/tra-hang"
              }
            >
              <span>Xử lý trả hàng</span>
              <IconArrowUpRight size={14} />
            </Button>
          </div>
        </section>
      )}

      {/* 6-column KPI & Detail Grid */}
      <div className={styles.sixGrid}>
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Tiền hàng ({formatInt(totalQty)} sản phẩm)</span>
          <span className={styles.kpiTileVal}>{formatVnd(order.subtotal_vnd)}</span>
          <span className={styles.kpiTileSub}>Khách trả: {formatVnd(order.total_payable_vnd)}</span>
        </div>
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Thực nhận gian hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(netReceivable)}</span>
          <span className={styles.kpiTileSub}>
            Đã trừ chiết khấu, hoa hồng &amp; phí thanh toán
          </span>
        </div>
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Vận chuyển</span>
          <span className={styles.kpiTileVal}>
            {order.carrier_code ? formatCarrierLabel(order.carrier_code) : "Chưa gán"}
          </span>
          <span className={styles.kpiTileSub}>
            {order.tracking_code ? `Mã: ${order.tracking_code}` : statusMeta.label}
          </span>
        </div>

        {/* Items & Financial Breakdown */}
        <section
          className={`${styles.detailCard} ${styles.span4}`}
          aria-label="Sản phẩm & đối soát"
        >
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Sản phẩm trong đơn ({formatInt(totalQty)})</h2>
          </div>

          <div className={common.tableWrap}>
            <table className={common.dataTable}>
              <thead>
                <tr>
                  <th>Sản phẩm &amp; phân loại</th>
                  <th className={common.numCol}>Đơn giá</th>
                  <th className={common.numCol}>SL</th>
                  <th className={common.numCol}>Thành tiền</th>
                </tr>
              </thead>
              <tbody>
                {(order.items ?? []).map((item) => (
                  <tr key={item.id}>
                    <td>
                      <Link
                        href={`/seller/san-pham/${item.product_id}`}
                        className={common.cellLink}
                      >
                        {item.product_name}
                      </Link>
                      {item.sku_name && <span className={common.cellSub}>{item.sku_name}</span>}
                    </td>
                    <td className={common.numCol}>{formatVnd(item.unit_price_vnd)}</td>
                    <td className={common.numCol}>{formatInt(item.quantity)}</td>
                    <td className={common.numCol}>
                      <strong>{formatVnd(item.line_total_vnd)}</strong>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className={styles.reconBlock}>
            {breakdownLines.map((line) => (
              <div key={line.key} className={styles.reconRow}>
                <span>{line.label}</span>
                <span>
                  {line.amountVnd < 0
                    ? `−${formatVnd(Math.abs(line.amountVnd))}`
                    : formatVnd(line.amountVnd)}
                </span>
              </div>
            ))}
            <div className={`${styles.reconRow} ${styles.reconRowTotal}`}>
              <span>Thực nhận dự kiến</span>
              <span>{formatVnd(netReceivable)}</span>
            </div>
          </div>
        </section>

        {/* Shipping, Address & Shipment Creation */}
        <section
          className={`${styles.detailCard} ${styles.span2}`}
          aria-label="Thông tin giao hàng"
        >
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Giao nhận &amp; vận đơn</h2>
          </div>

          <div className={styles.kvList}>
            <div className={styles.kvRow}>
              <span className={styles.kvKey}>Người nhận</span>
              <span className={styles.kvVal}>
                {order.shipping_address?.recipient_name || EMPTY_DASH}
              </span>
            </div>
            <div className={styles.kvRow}>
              <span className={styles.kvKey}>Điện thoại</span>
              <span className={styles.kvVal}>
                {order.shipping_address?.recipient_phone || EMPTY_DASH}
              </span>
            </div>
            <div className={styles.kvRow}>
              <span className={styles.kvKey}>Địa chỉ</span>
              <span className={styles.kvVal}>
                {order.shipping_address
                  ? [
                      order.shipping_address.street_address,
                      order.shipping_address.ward_name,
                      order.shipping_address.province_name,
                    ]
                      .filter(Boolean)
                      .join(", ")
                  : EMPTY_DASH}
              </span>
            </div>
            <div className={styles.kvRow}>
              <span className={styles.kvKey}>Phí ship khách trả</span>
              <span className={styles.kvVal}>{formatVnd(order.shipping_fee_vnd)}</span>
            </div>
            {shipment && (
              <>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Trạng thái vận đơn</span>
                  <span className={styles.kvVal}>
                    {shipmentStatusInfo ? shipmentStatusInfo.label : shipment.status}
                  </span>
                </div>
                {shipmentFeeVnd > 0 && (
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Phí vận chuyển hãng</span>
                    <span className={styles.kvVal}>{formatVnd(shipmentFeeVnd)}</span>
                  </div>
                )}
                {shipment.cod_amount_vnd > 0 && (
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Thu hộ COD</span>
                    <span className={styles.kvVal}>{formatVnd(shipment.cod_amount_vnd)}</span>
                  </div>
                )}
                {typeof shipment.cod_fee_vnd === "number" && shipment.cod_fee_vnd > 0 && (
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Phí thu hộ COD</span>
                    <span className={styles.kvVal}>{formatVnd(shipment.cod_fee_vnd)}</span>
                  </div>
                )}
              </>
            )}
            {localizedCancelReason && (
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Lý do hủy</span>
                <span className={styles.kvVal}>{localizedCancelReason}</span>
              </div>
            )}
          </div>

          {canShip && (
            <div className={styles.giftBox}>
              <div className={common.field}>
                <label htmlFor="detail-carrier" className={common.fieldLabel}>
                  Đơn vị vận chuyển
                </label>
                <select
                  id="detail-carrier"
                  className={common.textInput}
                  value={carrierCode}
                  onChange={(e) => {
                    const nextCarrier = e.target.value;
                    setCarrierCode(nextCarrier);
                    const opt = carrierOptions.find((c) => c.carrierCode === nextCarrier);
                    if (opt?.services[0]) setServiceCode(opt.services[0].serviceCode);
                  }}
                >
                  {carrierOptions.map((c) => (
                    <option key={c.carrierCode} value={c.carrierCode}>
                      {c.carrierLabel}
                    </option>
                  ))}
                </select>
              </div>
              <div className={common.field}>
                <label htmlFor="detail-service" className={common.fieldLabel}>
                  Gói dịch vụ
                </label>
                <select
                  id="detail-service"
                  className={common.textInput}
                  value={serviceCode}
                  onChange={(e) => setServiceCode(e.target.value)}
                >
                  {activeCarrierOption.services.map((s) => (
                    <option key={s.serviceCode} value={s.serviceCode}>
                      {s.serviceLabel}
                    </option>
                  ))}
                </select>
              </div>
              <Button
                variant="ink"
                size="sm"
                shape="app"
                disabled={busy}
                onClick={() => void handleShip()}
              >
                {busy
                  ? "Đang tạo vận đơn..."
                  : isShipmentQueued || isShipmentFailed
                    ? "Thử tạo lại ngay"
                    : "Tạo vận đơn"}
              </Button>
            </div>
          )}
        </section>

        {/* Carrier Tracking Events */}
        {shipmentEvents.length > 0 && (
          <section
            className={`${styles.detailCard} ${styles.span6}`}
            aria-label="Hành trình vận đơn"
          >
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>
                Hành trình vận đơn (
                {shipment?.tracking_code || order.tracking_code || order.order_code})
              </h2>
            </div>
            <div className={styles.kvList}>
              {shipmentEvents.map((ev) => {
                const stInfo = getShipmentStatusInfo(ev.status);
                return (
                  <div key={ev.id} className={styles.kvRow}>
                    <div>
                      <strong>{stInfo.label}</strong>
                      {ev.description && <span className={common.cellSub}>{ev.description}</span>}
                      {ev.location && <span className={common.cellSub}>{ev.location}</span>}
                    </div>
                    <span className={styles.kvKey}>
                      {ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : EMPTY_DASH}
                    </span>
                  </div>
                );
              })}
            </div>
          </section>
        )}

        {/* Order Status & Address Update Timeline */}
        {Array.isArray(order.timeline) && order.timeline.length > 0 && (
          <section className={`${styles.detailCard} ${styles.span6}`} aria-label="Lịch sử đơn hàng">
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>Lịch sử xử lý đơn hàng</h2>
            </div>
            <div className={styles.kvList}>
              {order.timeline.map((ev) => {
                const title = isAddressUpdatedTimelineNote(ev.note)
                  ? "Cập nhật địa chỉ giao hàng"
                  : getTimelineEventTitle(ev, order.payment_method);
                const noteText = localizeTimelineNote(ev.note);
                return (
                  <div key={ev.id} className={styles.kvRow}>
                    <div>
                      <strong>{title}</strong>
                      {noteText && <span className={common.cellSub}>{noteText}</span>}
                    </div>
                    <span className={styles.kvKey}>
                      {ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH}
                    </span>
                  </div>
                );
              })}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
