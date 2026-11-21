"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconCheck, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { useSellerContext } from "./SellerShell";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  summarizeOrderItems,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { parseUiState } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/don-hang/page.module.css";

type OrderFilterKey = "all" | "CONFIRMED" | "PREPARING" | "SHIPPING" | "DELIVERED" | "CLOSED";

function bucketOf(status: string): OrderFilterKey {
  switch (status.toUpperCase()) {
    case "PENDING":
    case "CONFIRMED":
      return "CONFIRMED";
    case "PREPARING":
      return "PREPARING";
    case "SHIPPING":
      return "SHIPPING";
    case "DELIVERED":
    case "COMPLETED":
      return "DELIVERED";
    default:
      return "CLOSED";
  }
}

export interface SellerOrdersClientProps {
  initialOrders: GwSellerOrder[];
  loadError: string | null;
}

export function SellerOrdersClient({ initialOrders, loadError }: SellerOrdersClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialStatus = searchParams.get("status")?.toUpperCase();
  const { shop, showToast } = useSellerContext();

  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [bulkBusy, setBulkBusy] = useState(false);

  const [filter, setFilter] = useState<OrderFilterKey>(
    initialStatus &&
      ["all", "CONFIRMED", "PREPARING", "SHIPPING", "DELIVERED", "CLOSED"].includes(initialStatus)
      ? (initialStatus as OrderFilterKey)
      : "all"
  );
  const [query, setQuery] = useState("");
  const [paymentFilter, setPaymentFilter] = useState<"all" | "COD" | "VIETQR">("all");

  const counts = useMemo(
    () => ({
      all: orders.length,
      CONFIRMED: orders.filter((o) => bucketOf(o.status) === "CONFIRMED").length,
      PREPARING: orders.filter((o) => bucketOf(o.status) === "PREPARING").length,
      SHIPPING: orders.filter((o) => bucketOf(o.status) === "SHIPPING").length,
      DELIVERED: orders.filter((o) => bucketOf(o.status) === "DELIVERED").length,
      CLOSED: orders.filter((o) => bucketOf(o.status) === "CLOSED").length,
    }),
    [orders]
  );

  const confirmableCount = useMemo(
    () => orders.filter((o) => o.status.toUpperCase() === "CONFIRMED").length,
    [orders]
  );

  const filteredOrders = useMemo(() => {
    const q = query.trim().toLowerCase();
    return orders.filter((o) => {
      if (filter !== "all" && bucketOf(o.status) !== filter) return false;
      if (paymentFilter !== "all" && o.payment_method.toUpperCase() !== paymentFilter) return false;
      if (q) {
        const addr = o.shipping_address;
        const summary = summarizeOrderItems(o.items).toLowerCase();
        const hit =
          o.order_code.toLowerCase().includes(q) ||
          (addr?.recipient_name.toLowerCase().includes(q) ?? false) ||
          (addr?.province_name.toLowerCase().includes(q) ?? false) ||
          o.tracking_code.toLowerCase().includes(q) ||
          summary.includes(q);
        if (!hit) return false;
      }
      return true;
    });
  }, [orders, filter, paymentFilter, query]);

  const handleAction = async (order: GwSellerOrder) => {
    const action = getNextBackendOrderAction(order.status);
    if (!action || busyId) return;
    setBusyId(order.id);
    try {
      const url =
        action.kind === "confirm"
          ? `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`
          : `/api/seller/orders/${encodeURIComponent(order.id)}/ship`;
      const res = await api<{ order: GwSellerOrder }>(url, { method: "POST" });
      if (res.order) {
        setOrders((prev) => prev.map((o) => (o.id === res.order.id ? res.order : o)));
      }
      showToast(`Đơn ${order.order_code}: ${action.label} thành công.`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái đơn hàng.");
    } finally {
      setBusyId(null);
    }
  };

  const handleConfirmAllPending = async () => {
    const targets = orders.filter((o) => o.status.toUpperCase() === "CONFIRMED");
    if (targets.length === 0 || bulkBusy) return;
    setBulkBusy(true);
    let okCount = 0;
    let failCount = 0;
    const updatedMap = new Map<string, GwSellerOrder>();
    for (const o of targets) {
      try {
        const res = await api<{ order: GwSellerOrder }>(
          `/api/seller/orders/${encodeURIComponent(o.id)}/confirm`,
          { method: "POST" }
        );
        if (res.order) updatedMap.set(res.order.id, res.order);
        okCount += 1;
      } catch {
        failCount += 1;
      }
    }
    if (updatedMap.size > 0) {
      setOrders((prev) => prev.map((o) => updatedMap.get(o.id) ?? o));
      router.refresh();
    }
    if (failCount === 0) {
      showToast(`Đã xác nhận ${formatInt(okCount)} đơn chờ và chuyển sang đóng gói.`);
    } else {
      showToast(
        `Đã xác nhận ${formatInt(okCount)} đơn; ${formatInt(failCount)} đơn chưa thể cập nhật.`
      );
    }
    setBulkBusy(false);
  };

  const countCards: Array<{ key: OrderFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "CONFIRMED", label: "Chờ xác nhận", count: counts.CONFIRMED },
    { key: "PREPARING", label: "Đang chuẩn bị", count: counts.PREPARING },
    { key: "SHIPPING", label: "Đang giao", count: counts.SHIPPING },
    { key: "DELIVERED", label: "Đã giao", count: counts.DELIVERED },
    { key: "CLOSED", label: "Trả hàng / Hủy", count: counts.CLOSED },
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
            {shop?.name ?? "Gian hàng"} · {formatInt(confirmableCount)} đơn chờ xác nhận,{" "}
            {formatInt(counts.PREPARING)} đơn đang chuẩn bị bàn giao vận chuyển
          </p>
        </div>
        <div className={common.headerActions}>
          {confirmableCount > 0 && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              disabled={bulkBusy}
              onClick={() => void handleConfirmAllPending()}
            >
              <IconCheck size={14} />
              <span>
                {bulkBusy
                  ? "Đang xác nhận..."
                  : `Xác nhận đơn chờ (${formatInt(confirmableCount)})`}
              </span>
            </Button>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang đồng bộ danh sách đơn hàng và trạng thái vận chuyển..."
          rows={5}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách đơn hàng"
          message={loadError ?? "Kết nối đến hệ thống xử lý đơn hàng tạm thời gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {(uiState === "empty" || (!uiState && !loadError && orders.length === 0)) && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có đơn hàng nào trong giai đoạn này"
          message="Khi người mua đặt hàng từ gian hàng, đơn sẽ xuất hiện tại đây theo thời gian thực."
          actionLabel="Xem sản phẩm đang bán"
          actionHref="/seller/san-pham"
        />
      )}

      {!uiState && !loadError && orders.length > 0 && (
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
                    placeholder="Tìm mã đơn, người nhận, mã vận đơn..."
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
                  <option value="VIETQR">VietQR</option>
                  <option value="COD">Thu hộ COD</option>
                </select>
              </div>
              <span className={common.cardMeta}>
                Hiển thị {formatInt(filteredOrders.length)} trên {formatInt(orders.length)} đơn hàng
              </span>
            </div>

            {filteredOrders.length === 0 ? (
              <div className={common.inlineEmpty}>
                Không có đơn hàng nào khớp bộ lọc trạng thái hiện tại.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã đơn</th>
                        <th>Kênh bán</th>
                        <th>Người mua</th>
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
                        const meta = getSellerOrderStatusMeta(o.status);
                        const channelMeta = getSalesChannelMeta(o.sales_channel);
                        const nextAction = getNextBackendOrderAction(o.status);
                        const totalQty = (o.items ?? []).reduce((s, it) => s + it.quantity, 0);
                        const netVnd = computeSellerNetReceivable(o);
                        const summary = summarizeOrderItems(o.items);
                        const recipient = o.shipping_address?.recipient_name || "Người mua";
                        const location = [
                          o.shipping_address?.ward_name,
                          o.shipping_address?.province_name,
                        ]
                          .filter(Boolean)
                          .join(", ");

                        return (
                          <tr key={o.id}>
                            <td className={styles.orderCodeCell}>
                              <Link href={`/seller/don-hang/${o.id}`} className={common.cellLink}>
                                {o.order_code}
                              </Link>
                              <span className={common.cellSub}>
                                {o.created_at ? formatDateTimeVi(o.created_at) : ""}
                              </span>
                            </td>
                            <td>
                              <span
                                className={`${common.channelTag} ${channelMeta.isStorefront ? common.channelTagStorefront : ""}`}
                                data-sales-channel={channelMeta.value}
                              >
                                {channelMeta.label}
                              </span>
                            </td>
                            <td>
                              <span>{recipient}</span>
                              {location && <span className={common.cellSub}>{location}</span>}
                            </td>
                            <td>
                              <div className={styles.itemSummaryCell} title={summary}>
                                {summary}
                              </div>
                              <span className={common.cellSub}>
                                {formatPaymentMethodVi(o.payment_method)} ·{" "}
                                {formatPaymentStatusVi(o.payment_status)}
                                {o.tracking_code ? ` · ${o.tracking_code}` : ""}
                              </span>
                            </td>
                            <td className={common.numCol}>{formatInt(totalQty)}</td>
                            <td className={common.numCol}>{formatVnd(o.subtotal_vnd)}</td>
                            <td className={common.numCol}>
                              <strong>{formatVnd(netVnd)}</strong>
                            </td>
                            <td>
                              <span className={pillCls(meta.tone)}>{meta.label}</span>
                            </td>
                            <td className={common.numCol}>
                              <div className={styles.actionGroup}>
                                {nextAction && (
                                  <button
                                    type="button"
                                    className={styles.rowAdvanceBtn}
                                    disabled={busyId === o.id}
                                    onClick={() => void handleAction(o)}
                                  >
                                    {busyId === o.id ? "Đang xử lý..." : nextAction.shortLabel}
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
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {filteredOrders.map((o) => {
                    const meta = getSellerOrderStatusMeta(o.status);
                    const channelMeta = getSalesChannelMeta(o.sales_channel);
                    const nextAction = getNextBackendOrderAction(o.status);
                    const recipient = o.shipping_address?.recipient_name || "Người mua";
                    const province = o.shipping_address?.province_name || "";
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
                              {recipient}
                              {province ? ` · ${province}` : ""}
                            </span>
                          </div>
                          <span className={styles.mobileOrderTotal}>
                            {formatVnd(o.subtotal_vnd)}
                          </span>
                        </div>
                        <p className={styles.mobileOrderSummary}>{summarizeOrderItems(o.items)}</p>
                        <div className={common.mobileRowMeta}>
                          <div className={styles.actionGroup}>
                            <span className={pillCls(meta.tone)}>{meta.label}</span>
                            <span
                              className={`${common.channelTag} ${channelMeta.isStorefront ? common.channelTagStorefront : ""}`}
                              data-sales-channel={channelMeta.value}
                            >
                              {channelMeta.label}
                            </span>
                          </div>
                          <div className={styles.actionGroup}>
                            {nextAction && (
                              <button
                                type="button"
                                className={styles.rowAdvanceBtn}
                                disabled={busyId === o.id}
                                onClick={() => void handleAction(o)}
                              >
                                {busyId === o.id ? "Đang xử lý..." : nextAction.shortLabel}
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
