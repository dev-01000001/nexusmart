"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type { GatewayOrder, GatewayShop, PaginatedResponse } from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconCheck, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import {
  CARRIER_OPTIONS,
  PAYMENT_METHOD_TEXT,
  canSellerConfirmOrder,
  canSellerShipOrder,
  computeOrderNetVnd,
  getOrderStatusMeta,
} from "@/features/seller/types";
import { parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type OrderFilterKey = "all" | "CONFIRMED" | "PREPARING" | "SHIPPING" | "DONE" | "OTHER";

function summarizeItems(order: GatewayOrder): { text: string; totalQty: number } {
  const items = order.items ?? [];
  const totalQty = items.reduce((s, item) => s + item.quantity, 0);
  if (items.length === 0) return { text: "Chưa có dòng sản phẩm", totalQty: 0 };
  const first = items[0];
  const rest = items.length - 1;
  return {
    text:
      rest > 0
        ? `${first.product_name} (+${rest} sản phẩm khác)`
        : `${first.product_name} · ${first.sku_name}`,
    totalQty,
  };
}

function SellerOrdersInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const rawStatus = searchParams.get("status");
  const initialFilter: OrderFilterKey =
    rawStatus === "CONFIRMED" || rawStatus === "cho_xac_nhan"
      ? "CONFIRMED"
      : rawStatus === "PREPARING" || rawStatus === "dang_chuan_bi"
        ? "PREPARING"
        : rawStatus === "SHIPPING" || rawStatus === "dang_giao"
          ? "SHIPPING"
          : rawStatus === "DONE" || rawStatus === "DELIVERED" || rawStatus === "COMPLETED"
            ? "DONE"
            : rawStatus === "OTHER" || rawStatus === "CANCELLED"
              ? "OTHER"
              : "all";

  const { showToast } = useMockStore();
  const { refreshPendingOrders } = useSellerContext();

  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [filter, setFilter] = useState<OrderFilterKey>(initialFilter);
  const [query, setQuery] = useState("");
  const [paymentFilter, setPaymentFilter] = useState<"all" | "COD" | "VIETQR">("all");

  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);
  const [batchBusy, setBatchBusy] = useState(false);

  // Inline ship form per order
  const [shippingOrderId, setShippingOrderId] = useState<string | null>(null);
  const [carrierCode, setCarrierCode] = useState(CARRIER_OPTIONS[0].carrierCode);
  const [serviceCode, setServiceCode] = useState(CARRIER_OPTIONS[0].services[0].serviceCode);

  const loadOrders = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<PaginatedResponse<GatewayOrder>>("seller/orders?page=1&page_size=100");
      setOrders(res.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách đơn hàng lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!uiState) {
      void loadOrders();
    }
  }, [uiState, loadOrders]);

  const counts = useMemo(() => {
    return {
      all: orders.length,
      CONFIRMED: orders.filter((o) => o.status === "CONFIRMED").length,
      PREPARING: orders.filter((o) => o.status === "PREPARING").length,
      SHIPPING: orders.filter((o) => o.status === "SHIPPING").length,
      DONE: orders.filter((o) => o.status === "DELIVERED" || o.status === "COMPLETED").length,
      OTHER: orders.filter(
        (o) =>
          o.status === "PENDING" ||
          o.status === "CANCELLED" ||
          o.status === "RETURN_REQUESTED" ||
          o.status === "RETURNED"
      ).length,
    };
  }, [orders]);

  const filteredOrders = useMemo(() => {
    const q = query.trim().toLowerCase();
    return orders.filter((o) => {
      if (filter === "DONE") {
        if (o.status !== "DELIVERED" && o.status !== "COMPLETED") return false;
      } else if (filter === "OTHER") {
        if (
          o.status !== "PENDING" &&
          o.status !== "CANCELLED" &&
          o.status !== "RETURN_REQUESTED" &&
          o.status !== "RETURNED"
        ) {
          return false;
        }
      } else if (filter !== "all" && o.status !== filter) {
        return false;
      }

      if (paymentFilter !== "all" && o.payment_method !== paymentFilter) return false;

      if (q) {
        const hit =
          o.order_code.toLowerCase().includes(q) ||
          (o.shipping_address?.recipient_name.toLowerCase().includes(q) ?? false) ||
          (o.shipping_address?.province_name.toLowerCase().includes(q) ?? false) ||
          (o.tracking_code ?? "").toLowerCase().includes(q) ||
          (o.items ?? []).some(
            (item) =>
              item.product_name.toLowerCase().includes(q) || item.sku_name.toLowerCase().includes(q)
          );
        if (!hit) return false;
      }
      return true;
    });
  }, [orders, filter, paymentFilter, query]);

  const activeCarrierOption = useMemo(
    () => CARRIER_OPTIONS.find((c) => c.carrierCode === carrierCode) ?? CARRIER_OPTIONS[0],
    [carrierCode]
  );

  const handleCarrierChange = (nextCarrier: string) => {
    setCarrierCode(nextCarrier);
    const opt = CARRIER_OPTIONS.find((c) => c.carrierCode === nextCarrier);
    if (opt && opt.services[0]) {
      setServiceCode(opt.services[0].serviceCode);
    }
  };

  const handleConfirmOrder = async (order: GatewayOrder) => {
    if (!canSellerConfirmOrder(order.status) || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      const res = await api<{ order: GatewayOrder }>(`seller/orders/${order.id}/confirm`, {
        method: "POST",
      });
      if (res.order) {
        setOrders((prev) => prev.map((item) => (item.id === order.id ? res.order : item)));
      } else {
        await loadOrders();
      }
      await refreshPendingOrders();
      showToast(`Đã xác nhận đơn ${order.order_code} sang trạng thái Đang chuẩn bị.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setBusyOrderId(null);
    }
  };

  const handleShipOrder = async (order: GatewayOrder) => {
    if (!canSellerShipOrder(order.status) || busyOrderId) return;
    setBusyOrderId(order.id);
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
      if (res.order) {
        setOrders((prev) => prev.map((item) => (item.id === order.id ? res.order : item)));
      } else {
        await loadOrders();
      }
      setShippingOrderId(null);
      await refreshPendingOrders();
      showToast(
        res.tracking_code
          ? `Đã bàn giao đơn ${order.order_code} · Mã vận đơn ${res.tracking_code}.`
          : `Đã chuyển đơn ${order.order_code} sang trạng thái Đang giao.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo vận đơn giao hàng lúc này."));
    } finally {
      setBusyOrderId(null);
    }
  };

  const handleConfirmAllPending = async () => {
    const pending = orders.filter((o) => o.status === "CONFIRMED");
    if (pending.length === 0 || batchBusy) return;
    setBatchBusy(true);
    let okCount = 0;
    try {
      for (const order of pending) {
        try {
          await api<{ order: GatewayOrder }>(`seller/orders/${order.id}/confirm`, {
            method: "POST",
          });
          okCount += 1;
        } catch {
          // Continue confirming remaining orders
        }
      }
      await loadOrders();
      await refreshPendingOrders();
      showToast(`Đã xác nhận ${formatInt(okCount)}/${formatInt(pending.length)} đơn chờ xác nhận.`);
    } finally {
      setBatchBusy(false);
    }
  };

  const countCards: Array<{ key: OrderFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "CONFIRMED", label: "Chờ xác nhận", count: counts.CONFIRMED },
    { key: "PREPARING", label: "Đang chuẩn bị", count: counts.PREPARING },
    { key: "SHIPPING", label: "Đang giao", count: counts.SHIPPING },
    { key: "DONE", label: "Đã giao / Hoàn tất", count: counts.DONE },
    { key: "OTHER", label: "Chờ TT / Hủy / Trả", count: counts.OTHER },
  ];

  const pillCls = (tone: "neutral" | "warn" | "pos" | "neg") =>
    tone === "pos"
      ? `${common.pill} ${common.pillPos}`
      : tone === "warn"
        ? `${common.pill} ${common.pillWarn}`
        : tone === "neg"
          ? `${common.pill} ${common.pillNeg}`
          : common.pill;

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Quản lý đơn hàng</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(counts.CONFIRMED)} đơn chờ xác nhận,{" "}
            {formatInt(counts.PREPARING)} đơn đang chuẩn bị giao hàng
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadOrders()}>
            Làm mới
          </Button>
          {counts.CONFIRMED > 0 && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              disabled={batchBusy}
              onClick={() => void handleConfirmAllPending()}
            >
              <IconCheck size={14} />
              <span>
                {batchBusy
                  ? "Đang xác nhận..."
                  : `Xác nhận đơn chờ (${formatInt(counts.CONFIRMED)})`}
              </span>
            </Button>
          )}
        </div>
      </header>

      {(uiState === "loading" || (!uiState && loading)) && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang đồng bộ danh sách đơn hàng và trạng thái vận chuyển..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách đơn hàng"
          message={errorMsg ?? "Kết nối đến hệ thống xử lý đơn hàng tạm thời gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => void loadOrders()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có đơn hàng nào trong giai đoạn này"
          message="Khi người mua đặt hàng từ gian hàng, đơn sẽ xuất hiện tại đây."
          actionLabel="Xem sản phẩm đang bán"
          actionHref="/seller/san-pham"
        />
      )}

      {!uiState && !loading && !errorMsg && (
        <>
          <div className={common.countCards6} role="tablist" aria-label="Lọc theo trạng thái đơn">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                  onClick={() => setFilter(c.key)}
                >
                  <span className={common.countCardLabel}>{c.label}</span>
                  <span className={common.countCardValue}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          <div className={common.chipScroller} role="tablist" aria-label="Lọc theo trạng thái đơn">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.chipBtn} ${selected ? common.chipBtnActive : ""}`}
                  onClick={() => setFilter(c.key)}
                >
                  <span>{c.label}</span>
                  <span className={common.chipCount}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          <section className={common.card} aria-label="Bảng đơn hàng gian hàng">
            <div className={common.toolbar} style={{ marginBottom: 14 }}>
              <div className={common.toolbarLeft}>
                <div className={common.searchWrap}>
                  <span className={common.searchIcon}>
                    <IconSearch size={14} />
                  </span>
                  <input
                    type="search"
                    className={common.searchInput}
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Tìm mã đơn, tên khách, mã vận đơn..."
                    aria-label="Tìm kiếm đơn hàng"
                  />
                </div>

                <select
                  className={common.filterSelect}
                  value={paymentFilter}
                  onChange={(e) => setPaymentFilter(e.target.value as "all" | "COD" | "VIETQR")}
                  aria-label="Lọc theo phương thức thanh toán"
                >
                  <option value="all">Mọi hình thức thanh toán</option>
                  <option value="COD">Thu hộ COD</option>
                  <option value="VIETQR">Chuyển khoản VietQR</option>
                </select>
              </div>
              <span className={common.cardMeta}>
                Hiển thị {formatInt(filteredOrders.length)} trên {formatInt(orders.length)} đơn hàng
              </span>
            </div>

            {filteredOrders.length === 0 ? (
              <div className={common.inlineEmpty}>
                {orders.length === 0
                  ? "Gian hàng chưa phát sinh đơn hàng nào."
                  : "Không có đơn hàng nào khớp bộ lọc trạng thái hiện tại."}
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã đơn</th>
                        <th>Người nhận</th>
                        <th>Sản phẩm & thanh toán</th>
                        <th className={common.numCol}>SL</th>
                        <th className={common.numCol}>Tiền hàng</th>
                        <th className={common.numCol}>Thực nhận</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredOrders.map((o) => {
                        const meta = getOrderStatusMeta(o.status);
                        const summary = summarizeItems(o);
                        const netVnd = computeOrderNetVnd(o);
                        const isBusy = busyOrderId === o.id;
                        const canConfirm = canSellerConfirmOrder(o.status);
                        const canShip = canSellerShipOrder(o.status);
                        const showShipDrawer = shippingOrderId === o.id;

                        return (
                          <React.Fragment key={o.id}>
                            <tr>
                              <td className={styles.orderCodeCell}>
                                <Link href={`/seller/don-hang/${o.id}`} className={common.cellLink}>
                                  {o.order_code}
                                </Link>
                                {o.created_at && (
                                  <span className={common.cellSub}>
                                    {formatDateTimeVi(o.created_at)}
                                  </span>
                                )}
                              </td>
                              <td>
                                <span>{o.shipping_address?.recipient_name || "Người mua"}</span>
                                <span className={common.cellSub}>
                                  {o.shipping_address
                                    ? `${o.shipping_address.ward_name}, ${o.shipping_address.province_name}`
                                    : "Chưa có địa chỉ"}
                                </span>
                              </td>
                              <td>
                                <div className={styles.itemSummaryCell} title={summary.text}>
                                  {summary.text}
                                </div>
                                <span className={common.cellSub}>
                                  {PAYMENT_METHOD_TEXT[o.payment_method] ?? o.payment_method}
                                  {o.tracking_code ? ` · Vận đơn: ${o.tracking_code}` : ""}
                                </span>
                              </td>
                              <td className={common.numCol}>{formatInt(summary.totalQty)}</td>
                              <td className={common.numCol}>{formatVnd(o.subtotal_vnd)}</td>
                              <td className={common.numCol}>
                                <strong>{formatVnd(netVnd)}</strong>
                              </td>
                              <td>
                                <span className={pillCls(meta.tone)}>{meta.label}</span>
                              </td>
                              <td className={common.numCol}>
                                <div className={styles.actionGroup}>
                                  {canConfirm && (
                                    <button
                                      type="button"
                                      className={styles.rowAdvanceBtn}
                                      disabled={isBusy}
                                      onClick={() => void handleConfirmOrder(o)}
                                    >
                                      {isBusy ? "..." : "Xác nhận"}
                                    </button>
                                  )}
                                  {canShip && (
                                    <button
                                      type="button"
                                      className={styles.rowAdvanceBtn}
                                      disabled={isBusy}
                                      onClick={() =>
                                        setShippingOrderId((prev) => (prev === o.id ? null : o.id))
                                      }
                                    >
                                      {showShipDrawer ? "Đóng" : "Giao hàng"}
                                    </button>
                                  )}
                                  <Link
                                    href={`/seller/don-hang/${o.id}`}
                                    className={styles.rowDetailLink}
                                  >
                                    Chi tiết
                                  </Link>
                                </div>
                              </td>
                            </tr>
                            {showShipDrawer && (
                              <tr>
                                <td colSpan={8} className={styles.shipDrawer}>
                                  <div className={styles.shipFormRow}>
                                    <span className={styles.queueMeta}>
                                      Tạo vận đơn & bàn giao cho đơn <strong>{o.order_code}</strong>
                                    </span>
                                    <div className={styles.shipFormControls}>
                                      <select
                                        className={common.filterSelect}
                                        value={carrierCode}
                                        onChange={(e) => handleCarrierChange(e.target.value)}
                                        aria-label="Chọn đơn vị vận chuyển"
                                      >
                                        {CARRIER_OPTIONS.map((c) => (
                                          <option key={c.carrierCode} value={c.carrierCode}>
                                            {c.carrierLabel}
                                          </option>
                                        ))}
                                      </select>
                                      <select
                                        className={common.filterSelect}
                                        value={serviceCode}
                                        onChange={(e) => setServiceCode(e.target.value)}
                                        aria-label="Chọn gói dịch vụ vận chuyển"
                                      >
                                        {activeCarrierOption.services.map((s) => (
                                          <option key={s.serviceCode} value={s.serviceCode}>
                                            {s.serviceLabel}
                                          </option>
                                        ))}
                                      </select>
                                      <Button
                                        variant="ink"
                                        size="sm"
                                        shape="app"
                                        disabled={isBusy}
                                        onClick={() => void handleShipOrder(o)}
                                      >
                                        {isBusy ? "Đang tạo vận đơn..." : "Xác nhận giao hàng"}
                                      </Button>
                                    </div>
                                  </div>
                                </td>
                              </tr>
                            )}
                          </React.Fragment>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {filteredOrders.map((o) => {
                    const meta = getOrderStatusMeta(o.status);
                    const summary = summarizeItems(o);
                    const isBusy = busyOrderId === o.id;
                    const canConfirm = canSellerConfirmOrder(o.status);
                    const canShip = canSellerShipOrder(o.status);
                    const showShipDrawer = shippingOrderId === o.id;
                    return (
                      <div key={o.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div>
                            <Link
                              href={`/seller/don-hang/${o.id}`}
                              className={styles.mobileOrderCode}
                            >
                              {o.order_code}
                            </Link>
                            <span className={common.cellSub}>
                              {o.shipping_address?.recipient_name || "Người mua"} ·{" "}
                              {o.shipping_address?.province_name || "Chưa rõ khu vực"}
                            </span>
                          </div>
                          <span className={styles.mobileOrderTotal}>
                            {formatVnd(o.subtotal_vnd)}
                          </span>
                        </div>
                        <p className={styles.mobileOrderSummary}>{summary.text}</p>
                        <div className={common.mobileRowMeta}>
                          <span className={pillCls(meta.tone)}>{meta.label}</span>
                          <div className={styles.actionGroup}>
                            {canConfirm && (
                              <button
                                type="button"
                                className={styles.rowAdvanceBtn}
                                disabled={isBusy}
                                onClick={() => void handleConfirmOrder(o)}
                              >
                                {isBusy ? "..." : "Xác nhận"}
                              </button>
                            )}
                            {canShip && (
                              <button
                                type="button"
                                className={styles.rowAdvanceBtn}
                                disabled={isBusy}
                                onClick={() =>
                                  setShippingOrderId((prev) => (prev === o.id ? null : o.id))
                                }
                              >
                                Giao hàng
                              </button>
                            )}
                            <Link
                              href={`/seller/don-hang/${o.id}`}
                              className={styles.rowDetailLink}
                            >
                              Chi tiết
                            </Link>
                          </div>
                        </div>
                        {showShipDrawer && (
                          <div className={styles.shipMobileBox}>
                            <select
                              className={common.filterSelect}
                              value={carrierCode}
                              onChange={(e) => handleCarrierChange(e.target.value)}
                            >
                              {CARRIER_OPTIONS.map((c) => (
                                <option key={c.carrierCode} value={c.carrierCode}>
                                  {c.carrierLabel}
                                </option>
                              ))}
                            </select>
                            <select
                              className={common.filterSelect}
                              value={serviceCode}
                              onChange={(e) => setServiceCode(e.target.value)}
                            >
                              {activeCarrierOption.services.map((s) => (
                                <option key={s.serviceCode} value={s.serviceCode}>
                                  {s.serviceLabel}
                                </option>
                              ))}
                            </select>
                            <Button
                              variant="ink"
                              size="sm"
                              shape="app"
                              disabled={isBusy}
                              onClick={() => void handleShipOrder(o)}
                            >
                              {isBusy ? "Đang tạo vận đơn..." : "Xác nhận giao hàng"}
                            </Button>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </section>
        </>
      )}
    </div>
  );
}

function SellerOrdersContent() {
  return (
    <SellerShopGate pageTitle="Quản lý đơn hàng">
      {(shop) => <SellerOrdersInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerOrdersPage() {
  return (
    <SellerSuspense>
      <SellerOrdersContent />
    </SellerSuspense>
  );
}
