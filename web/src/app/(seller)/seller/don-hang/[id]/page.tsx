"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useSearchParams } from "next/navigation";
import type {
  GatewayOrder,
  GatewayOrderTimelineEvent,
  GatewayShipmentResponse,
  GatewayShop,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconCheck, IconFileText, IconTruck, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import {
  CARRIER_OPTIONS,
  PAYMENT_METHOD_TEXT,
  SHIPMENT_STATUS_LABELS,
  buildCarrierSelectOptions,
  canSellerCancelOrder,
  canSellerConfirmOrder,
  canSellerShipOrder,
  computeOrderNetVnd,
  getOrderStatusMeta,
  resolveOrderShippingDefaults,
} from "@/features/seller/types";
import { EMPTY_DASH, parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type DetailTab = "overview" | "shipping" | "history";

function SellerOrderDetailInner({ shop }: { shop: GatewayShop }) {
  const params = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as DetailTab | null;
  const orderId = typeof params?.id === "string" ? params.id : "";

  const { showToast } = useMockStore();
  const { refreshPendingOrders } = useSellerContext();

  const [tab, setTab] = useState<DetailTab>(
    initialTab && ["overview", "shipping", "history"].includes(initialTab) ? initialTab : "overview"
  );

  const [order, setOrder] = useState<GatewayOrder | null>(null);
  const [timeline, setTimeline] = useState<GatewayOrderTimelineEvent[]>([]);
  const [shipmentData, setShipmentData] = useState<GatewayShipmentResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);

  const [actionBusy, setActionBusy] = useState(false);
  const [showShipPanel, setShowShipPanel] = useState(false);
  const [carrierCode, setCarrierCode] = useState(CARRIER_OPTIONS[0].carrierCode);
  const [serviceCode, setServiceCode] = useState(CARRIER_OPTIONS[0].services[0].serviceCode);

  const [showCancelPanel, setShowCancelPanel] = useState(false);
  const [cancelReason, setCancelReason] = useState("");

  const loadOrderDetail = useCallback(async () => {
    if (!orderId) {
      setNotFound(true);
      setLoading(false);
      return;
    }
    setLoading(true);
    setErrorMsg(null);
    setNotFound(false);
    try {
      const orderRes = await api<{ order: GatewayOrder | null }>(`seller/orders/${orderId}`);
      if (!orderRes.order) {
        setNotFound(true);
        setOrder(null);
        return;
      }
      setOrder(orderRes.order);
      const shippingDefaults = resolveOrderShippingDefaults(orderRes.order);
      setCarrierCode(shippingDefaults.carrierCode);
      setServiceCode(shippingDefaults.serviceCode);

      const [timelineRes, shipmentRes] = await Promise.all([
        api<{ events: GatewayOrderTimelineEvent[] }>(`orders/${orderId}/timeline`).catch(() => ({
          events: orderRes.order?.timeline ?? [],
        })),
        api<GatewayShipmentResponse>(`shipping/shipments/${orderId}`).catch(() => null),
      ]);
      setTimeline(timelineRes.events ?? orderRes.order.timeline ?? []);
      setShipmentData(shipmentRes);
    } catch (err) {
      if (err instanceof ApiError && (err.status === 404 || err.status === 403)) {
        setNotFound(true);
        setOrder(null);
      } else {
        setErrorMsg(getErrorMessage(err, "Không thể tải chi tiết đơn hàng lúc này."));
      }
    } finally {
      setLoading(false);
    }
  }, [orderId]);

  useEffect(() => {
    if (!uiState) {
      void loadOrderDetail();
    }
  }, [uiState, loadOrderDetail]);

  const carrierOptions = useMemo(() => buildCarrierSelectOptions(order), [order]);

  const activeCarrier = useMemo(
    () => carrierOptions.find((c) => c.carrierCode === carrierCode) ?? carrierOptions[0],
    [carrierOptions, carrierCode]
  );

  const handleCarrierChange = (nextCarrier: string) => {
    setCarrierCode(nextCarrier);
    const opt = carrierOptions.find((c) => c.carrierCode === nextCarrier);
    if (opt && opt.services[0]) {
      setServiceCode(opt.services[0].serviceCode);
    }
  };

  const handleConfirm = async () => {
    if (!order || actionBusy) return;
    setActionBusy(true);
    try {
      await api<{ order: GatewayOrder }>(`seller/orders/${order.id}/confirm`, {
        method: "POST",
      });
      await Promise.all([loadOrderDetail(), refreshPendingOrders()]);
      showToast(`Đã xác nhận đơn ${order.order_code} sang trạng thái Đang chuẩn bị.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setActionBusy(false);
    }
  };

  const handleShip = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || actionBusy) return;
    setActionBusy(true);
    try {
      const res = await api<{
        order: GatewayOrder;
        tracking_code?: string;
        label_url?: string;
      }>(`seller/orders/${order.id}/ship`, {
        method: "POST",
        json: {
          carrier_code: carrierCode,
          service_code: serviceCode,
        },
      });
      setShowShipPanel(false);
      await Promise.all([loadOrderDetail(), refreshPendingOrders()]);
      showToast(
        res.tracking_code
          ? `Đã tạo vận đơn ${res.tracking_code} và chuyển đơn sang Đang giao.`
          : `Đã chuyển đơn ${order.order_code} sang trạng thái Đang giao.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo vận đơn giao hàng lúc này."));
    } finally {
      setActionBusy(false);
    }
  };

  const handleCancel = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || actionBusy) return;
    const reason = cancelReason.trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do hủy đơn tối thiểu 4 ký tự.");
      return;
    }
    setActionBusy(true);
    try {
      await api<{ order: GatewayOrder }>(`orders/${order.id}/cancel`, {
        method: "POST",
        json: { reason },
      });
      setShowCancelPanel(false);
      setCancelReason("");
      await Promise.all([loadOrderDetail(), refreshPendingOrders()]);
      showToast(`Đã hủy đơn ${order.order_code} và hoàn lại tồn kho SKU.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể hủy đơn hàng lúc này."));
    } finally {
      setActionBusy(false);
    }
  };

  const pillCls = (tone: "neutral" | "warn" | "pos" | "neg") =>
    tone === "pos"
      ? `${common.pill} ${common.pillPos}`
      : tone === "warn"
        ? `${common.pill} ${common.pillWarn}`
        : tone === "neg"
          ? `${common.pill} ${common.pillNeg}`
          : common.pill;

  if (uiState === "loading" || (!uiState && loading)) {
    return (
      <StateView
        kind="loading"
        surface="app"
        title="Đang tải chi tiết đơn hàng và chứng từ vận chuyển..."
        rows={5}
      />
    );
  }

  if (uiState === "error" || (!uiState && !loading && errorMsg)) {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể tải chi tiết đơn hàng"
        message={errorMsg ?? "Hệ thống đơn hàng tạm thời chưa phản hồi."}
        actionLabel="Thử lại"
        onAction={() => void loadOrderDetail()}
      />
    );
  }

  if (uiState === "empty" || notFound || !order) {
    return (
      <div className={common.page}>
        <div className={styles.backRow}>
          <Link href="/seller/don-hang" className={styles.backLink}>
            Quay lại danh sách đơn hàng
          </Link>
        </div>
        <StateView
          kind="empty"
          surface="app"
          title="Không tìm thấy mã đơn hàng"
          message="Đơn hàng không tồn tại hoặc không thuộc gian hàng của bạn."
          actionLabel="Về danh sách đơn hàng"
          actionHref="/seller/don-hang"
        />
      </div>
    );
  }

  const meta = getOrderStatusMeta(order.status);
  const netReceivable = computeOrderNetVnd(order);
  const totalQty = (order.items ?? []).reduce((s, i) => s + i.quantity, 0);
  const totalDeductions =
    order.flash_sale_discount_vnd + order.shop_voucher_discount_vnd + order.commission_vnd;
  const canConfirm = canSellerConfirmOrder(order.status);
  const canShip = canSellerShipOrder(order.status);
  const canCancel = canSellerCancelOrder(order.status);
  const shipment = shipmentData?.shipment ?? null;
  const shipmentEvents = shipmentData?.events ?? [];

  const handleCopyCode = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(order.order_code).catch(() => {});
    }
    showToast(`Đã sao chép mã đơn ${order.order_code}.`);
  };

  return (
    <div className={common.page}>
      <div className={styles.backRow}>
        <Link href="/seller/don-hang" className={styles.backLink}>
          Quay lại danh sách đơn hàng
        </Link>
      </div>

      <header className={styles.objectHeader}>
        <div className={styles.objectIdentity}>
          <img
            src="/products/ao-thun-cotton-trang.png"
            alt={order.order_code}
            className={styles.objectCover}
            width={50}
            height={62}
          />
          <div className={styles.objectText}>
            <div className={styles.objectTitleRow}>
              <h1 className={styles.objectTitle}>{order.order_code}</h1>
              <span className={pillCls(meta.tone)}>{meta.label}</span>
              <button
                type="button"
                className={styles.iconBtn28}
                onClick={handleCopyCode}
                title="Sao chép mã đơn"
                aria-label="Sao chép mã đơn hàng"
              >
                <IconFileText size={14} />
              </button>
            </div>
            <p className={styles.objectMeta}>
              {shop.name}
              {order.created_at ? ` · Đặt lúc ${formatDateTimeVi(order.created_at)}` : ""}
              {order.shipping_address
                ? ` · ${order.shipping_address.ward_name}, ${order.shipping_address.province_name}`
                : ""}
            </p>
          </div>
        </div>

        <div className={styles.objectActions}>
          {canCancel && (
            <button
              type="button"
              className={`${styles.actionBtn36} ${styles.actionOutline}`}
              disabled={actionBusy}
              onClick={() => {
                setShowCancelPanel((prev) => !prev);
                setShowShipPanel(false);
              }}
            >
              Hủy đơn
            </button>
          )}
          {canConfirm && (
            <button
              type="button"
              className={`${styles.actionBtn36} ${styles.actionInk}`}
              disabled={actionBusy}
              onClick={() => void handleConfirm()}
            >
              <IconCheck size={14} />
              <span>{actionBusy ? "Đang xử lý..." : "Xác nhận đơn"}</span>
            </button>
          )}
          {canShip && (
            <button
              type="button"
              className={`${styles.actionBtn36} ${canConfirm ? styles.actionOutline : styles.actionInk}`}
              disabled={actionBusy}
              onClick={() => {
                setShowShipPanel((prev) => {
                  const next = !prev;
                  if (next) {
                    const defaults = resolveOrderShippingDefaults(order);
                    setCarrierCode(defaults.carrierCode);
                    setServiceCode(defaults.serviceCode);
                  }
                  return next;
                });
                setShowCancelPanel(false);
              }}
            >
              <IconTruck size={14} />
              <span>{showShipPanel ? "Đóng form giao" : "Tạo vận đơn & giao hàng"}</span>
            </button>
          )}
        </div>
      </header>

      {showShipPanel && canShip && (
        <form className={common.card} onSubmit={(e) => void handleShip(e)}>
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Chọn đơn vị vận chuyển & tạo vận đơn</h2>
            <span className={common.cardMeta}>
              Trạng thái đơn sẽ chuyển sang Đang giao ngay sau khi tạo vận đơn thành công
            </span>
          </div>
          <div className={styles.opsReplyBox}>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
                gap: 12,
              }}
            >
              <div className={common.field}>
                <label htmlFor="ship-carrier" className={common.fieldLabel}>
                  Đơn vị vận chuyển
                </label>
                <select
                  id="ship-carrier"
                  className={common.textInput}
                  value={carrierCode}
                  onChange={(e) => handleCarrierChange(e.target.value)}
                >
                  {carrierOptions.map((c) => (
                    <option key={c.carrierCode} value={c.carrierCode}>
                      {c.carrierLabel}
                    </option>
                  ))}
                </select>
              </div>
              <div className={common.field}>
                <label htmlFor="ship-service" className={common.fieldLabel}>
                  Gói dịch vụ
                </label>
                <select
                  id="ship-service"
                  className={common.textInput}
                  value={serviceCode}
                  onChange={(e) => setServiceCode(e.target.value)}
                >
                  {activeCarrier.services.map((s) => (
                    <option key={s.serviceCode} value={s.serviceCode}>
                      {s.serviceLabel}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div className={styles.opsActionBar}>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => setShowShipPanel(false)}
              >
                Hủy
              </Button>
              <Button type="submit" variant="ink" size="sm" shape="app" disabled={actionBusy}>
                {actionBusy ? "Đang tạo vận đơn..." : "Xác nhận giao hàng"}
              </Button>
            </div>
          </div>
        </form>
      )}

      {showCancelPanel && canCancel && (
        <form className={common.card} onSubmit={(e) => void handleCancel(e)}>
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Hủy đơn hàng {order.order_code}</h2>
            <span className={common.cardMeta}>
              Tồn kho SKU sẽ được hoàn trả tự động sau khi hủy
            </span>
          </div>
          <div className={styles.opsReplyBox}>
            <div className={common.field}>
              <label htmlFor="cancel-reason" className={common.fieldLabel}>
                Lý do hủy đơn
              </label>
              <input
                id="cancel-reason"
                type="text"
                className={common.textInput}
                value={cancelReason}
                onChange={(e) => setCancelReason(e.target.value)}
                placeholder="Ví dụ: Sản phẩm lỗi kiểm định chất lượng tại kho trước khi đóng gói"
                required
              />
            </div>
            <div className={styles.opsActionBar}>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => setShowCancelPanel(false)}
              >
                Quay lại
              </Button>
              <Button type="submit" variant="ink" size="sm" shape="app" disabled={actionBusy}>
                {actionBusy ? "Đang hủy đơn..." : "Xác nhận hủy đơn"}
              </Button>
            </div>
          </div>
        </form>
      )}

      <div className={common.underlineTabs} role="tablist" aria-label="Phân mục chi tiết đơn hàng">
        <button
          type="button"
          role="tab"
          aria-selected={tab === "overview"}
          className={`${common.underlineTab} ${tab === "overview" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("overview")}
        >
          Tổng quan đơn
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "shipping"}
          className={`${common.underlineTab} ${tab === "shipping" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("shipping")}
        >
          Vận chuyển & vận đơn
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "history"}
          className={`${common.underlineTab} ${tab === "history" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("history")}
        >
          Lịch sử trạng thái ({formatInt(timeline.length)})
        </button>
      </div>

      <div className={styles.sixGrid}>
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Tổng tiền hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(order.subtotal_vnd)}</span>
          <span className={styles.kpiTileSub}>
            {formatInt(totalQty)} sản phẩm · {formatInt(order.items?.length ?? 0)} dòng SKU
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Chiết khấu shop & phí sàn</span>
          <span className={styles.kpiTileVal}>{formatVnd(totalDeductions)}</span>
          <span className={styles.kpiTileSub}>
            Voucher {formatVnd(order.shop_voucher_discount_vnd)} · Phí sàn{" "}
            {formatVnd(order.commission_vnd)}
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Thực nhận gian hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(netReceivable)}</span>
          <span className={styles.kpiTileSub}>
            {PAYMENT_METHOD_TEXT[order.payment_method] ?? order.payment_method} · Khách trả{" "}
            {formatVnd(order.total_payable_vnd)}
          </span>
        </div>

        {tab === "overview" && (
          <>
            <section
              className={`${styles.detailCard} ${styles.span4}`}
              aria-labelledby="items-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="items-heading" className={common.cardTitle}>
                  Sản phẩm trong đơn hàng
                </h2>
                <span className={common.cardMeta}>Số tiền tính theo số nguyên VND</span>
              </div>

              <div className={common.tableWrap}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm & biến thể</th>
                      <th className={common.numCol}>Đơn giá</th>
                      <th className={common.numCol}>SL</th>
                      <th className={common.numCol}>Thành tiền</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(order.items ?? []).map((item) => (
                      <tr key={item.id}>
                        <td>
                          <div>
                            <Link
                              href={`/seller/san-pham/${item.product_id}`}
                              className={common.cellLink}
                            >
                              {item.product_name}
                            </Link>
                            <span className={common.cellSub}>
                              {item.sku_name} · {formatInt(item.weight_grams)}g
                            </span>
                          </div>
                        </td>
                        <td className={common.numCol}>
                          {formatVnd(item.effective_unit_price_vnd || item.unit_price_vnd)}
                        </td>
                        <td className={common.numCol}>{formatInt(item.quantity)}</td>
                        <td className={common.numCol}>
                          <strong>{formatVnd(item.line_total_vnd)}</strong>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {order.cancel_reason && (
                <div className={styles.giftBox}>
                  <div className={styles.giftRow}>
                    <span className={styles.kvKey}>Lý do hủy đơn</span>
                    <span className={styles.kvVal}>{order.cancel_reason}</span>
                  </div>
                </div>
              )}

              <div className={styles.reconBlock}>
                <div className={styles.reconRow}>
                  <span>Tổng tiền hàng</span>
                  <span>{formatVnd(order.subtotal_vnd)}</span>
                </div>
                {order.flash_sale_discount_vnd > 0 && (
                  <div className={styles.reconRow}>
                    <span>Giảm giá Flash sale</span>
                    <span>{`\u2212${formatVnd(order.flash_sale_discount_vnd)}`}</span>
                  </div>
                )}
                <div className={styles.reconRow}>
                  <span>Voucher gian hàng tài trợ</span>
                  <span>
                    {order.shop_voucher_discount_vnd > 0
                      ? `\u2212${formatVnd(order.shop_voucher_discount_vnd)}`
                      : "0₫"}
                  </span>
                </div>
                <div className={styles.reconRow}>
                  <span>Phí hoa hồng sàn</span>
                  <span>
                    {order.commission_vnd > 0 ? `\u2212${formatVnd(order.commission_vnd)}` : "0₫"}
                  </span>
                </div>
                <div className={`${styles.reconRow} ${styles.reconRowTotal}`}>
                  <span>Thực nhận dự kiến về gian hàng</span>
                  <span>{formatVnd(netReceivable)}</span>
                </div>
              </div>
            </section>

            <section
              className={`${styles.detailCard} ${styles.span2}`}
              aria-labelledby="buyer-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="buyer-heading" className={common.cardTitle}>
                  Thông tin giao nhận
                </h2>
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
                      ? `${order.shipping_address.street_address}, ${order.shipping_address.ward_name}, ${order.shipping_address.province_name}`
                      : EMPTY_DASH}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Thanh toán</span>
                  <span className={styles.kvVal}>
                    {PAYMENT_METHOD_TEXT[order.payment_method] ?? order.payment_method}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Phí vận chuyển</span>
                  <span className={styles.kvVal}>{formatVnd(order.shipping_fee_vnd)}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Đơn vị vận chuyển</span>
                  <span className={styles.kvVal}>
                    {order.carrier_code
                      ? `${order.carrier_code.toUpperCase()} (${order.service_code || "STANDARD"})`
                      : EMPTY_DASH}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Mã vận đơn</span>
                  <span className={styles.kvVal}>{order.tracking_code || EMPTY_DASH}</span>
                </div>
              </div>
            </section>
          </>
        )}

        {tab === "shipping" && (
          <section
            className={`${styles.detailCard} ${styles.span6}`}
            aria-labelledby="shipping-heading"
          >
            <div className={common.cardHeader}>
              <h2 id="shipping-heading" className={common.cardTitle}>
                Thông tin vận đơn & hành trình giao hàng
              </h2>
              {shipment && (
                <span className={common.pill}>
                  {SHIPMENT_STATUS_LABELS[shipment.status] ?? shipment.status}
                </span>
              )}
            </div>

            {!shipment ? (
              <div className={common.inlineEmpty}>
                Đơn hàng chưa có vận đơn.{" "}
                {canShip
                  ? "Hãy nhấn nút Tạo vận đơn & giao hàng ở đầu trang để bàn giao cho đơn vị vận chuyển."
                  : "Vận đơn sẽ xuất hiện ngay sau khi gian hàng bàn giao."}
              </div>
            ) : (
              <>
                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Mã vận đơn</span>
                    <span className={styles.kvVal}>{shipment.tracking_code}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Đơn vị & dịch vụ</span>
                    <span className={styles.kvVal}>
                      {shipment.carrier_code.toUpperCase()} · {shipment.service_code}
                    </span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Phí vận chuyển thực tế</span>
                    <span className={styles.kvVal}>{formatVnd(shipment.fee_vnd)}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Thu hộ COD</span>
                    <span className={styles.kvVal}>{formatVnd(shipment.cod_amount_vnd)}</span>
                  </div>
                  {shipment.label_url && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Phiếu giao hàng</span>
                      <span className={styles.kvVal}>{shipment.label_url}</span>
                    </div>
                  )}
                </div>

                {shipmentEvents.length > 0 && (
                  <div className={styles.timeline} style={{ marginTop: 16 }}>
                    {shipmentEvents.map((ev) => (
                      <div key={ev.id} className={styles.timelineRow}>
                        <span className={styles.timelineTime}>
                          {ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : EMPTY_DASH}
                        </span>
                        <div>
                          <div className={styles.timelineTitle}>
                            {SHIPMENT_STATUS_LABELS[ev.status] ?? ev.status}
                          </div>
                          {ev.description && (
                            <p className={styles.timelineNote}>{ev.description}</p>
                          )}
                        </div>
                        <span className={common.pill}>{ev.raw_status || ev.status}</span>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </section>
        )}

        {tab === "history" && (
          <section
            className={`${styles.detailCard} ${styles.span6}`}
            aria-labelledby="history-heading"
          >
            <div className={common.cardHeader}>
              <h2 id="history-heading" className={common.cardTitle}>
                Lịch sử chuyển trạng thái đơn hàng
              </h2>
              <span className={common.cardMeta}>
                Ghi nhận theo thời gian thực mọi thao tác trên đơn hàng
              </span>
            </div>

            {timeline.length === 0 ? (
              <div className={common.inlineEmpty}>
                Chưa có sự kiện trạng thái nào được ghi nhận.
              </div>
            ) : (
              <div className={styles.timeline}>
                {timeline.map((ev) => {
                  const toMeta = getOrderStatusMeta(ev.to_status);
                  const fromMeta = ev.from_status ? getOrderStatusMeta(ev.from_status) : null;
                  return (
                    <div key={ev.id} className={styles.timelineRow}>
                      <span className={styles.timelineTime}>
                        {ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH}
                      </span>
                      <div>
                        <div className={styles.timelineTitle}>
                          {fromMeta ? `${fromMeta.label} → ${toMeta.label}` : toMeta.label}
                        </div>
                        <p className={styles.timelineNote}>
                          {ev.note || `Cập nhật bởi ${ev.actor_role || "hệ thống"}`}
                        </p>
                      </div>
                      <span className={pillCls(toMeta.tone)}>{toMeta.label}</span>
                    </div>
                  );
                })}
              </div>
            )}
          </section>
        )}
      </div>
    </div>
  );
}

function SellerOrderDetailContent() {
  return (
    <SellerShopGate pageTitle="Chi tiết đơn hàng">
      {(shop) => <SellerOrderDetailInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerOrderDetailPage() {
  return (
    <SellerSuspense>
      <SellerOrderDetailContent />
    </SellerSuspense>
  );
}
