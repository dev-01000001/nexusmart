"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { PAYMENT_METHOD_LABELS, SHOPS, getShopById } from "@/data";
import type { OrderStatus, PaymentMethod } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconCheck, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import {
  advanceOrderTwoWayExchange,
  advanceOrderVatStatus,
  applySellerOrderStatusOverride,
  getNextSellerTransition,
  getSafeOrderStatusMeta,
  getUnifiedSellerOrders,
  parseUiState,
  resolveOrderAmendment,
  useSellerSession,
} from "@/features/seller/sellerStore";
import type { UnifiedSellerOrder } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type OrderFilterKey =
  "all" | "cho_xac_nhan" | "dang_chuan_bi" | "dang_giao" | "da_giao" | "tra_huy";

type OpsQueueFilter = "all" | "vat" | "amendment" | "exchange" | "complaint" | "gift";

function getOrderSignals(o: UnifiedSellerOrder): Array<{ label: string; warn: boolean }> {
  const ex = o.wave2Extra;
  if (!ex) return [];
  const tags: Array<{ label: string; warn: boolean }> = [];
  if (ex.giftBagRequested || ex.giftCardMessage) {
    tags.push({ label: ex.giftCardMessage ? "Túi quà & thiệp" : "Túi quà", warn: false });
  }
  if (ex.vatRequest) {
    if (ex.vatRequest.status === "cho_duyet_nhap") {
      tags.push({ label: "VAT chờ gửi nháp", warn: true });
    } else if (ex.vatRequest.status === "da_gui_nhap_24h") {
      tags.push({ label: "VAT chờ phát hành", warn: true });
    } else if (ex.vatRequest.status === "da_phat_hanh") {
      tags.push({ label: "Đã xuất HĐĐT", warn: false });
    }
  }
  if (ex.amendment && ex.amendment.status === "cho_duyet") {
    tags.push({
      label: ex.amendment.kind === "huy_muon" ? "Duyệt hủy muộn" : "Duyệt đổi địa chỉ",
      warn: true,
    });
  }
  if (ex.twoWayExchange && ex.twoWayExchange.courierStatus !== "hoan_tat") {
    tags.push({ label: "Đổi size 2 chiều", warn: true });
  }
  if (ex.tier1Complaint && ex.tier1Complaint.status === "cho_phan_hoi") {
    tags.push({ label: "KN cấp 1 · SLA 24h", warn: true });
  }
  return tags;
}

function SellerOrdersContent() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialStatus = searchParams.get("status") as OrderFilterKey | null;

  const { activeSellerShopId, orders: storeOrders, updateOrderStatus, showToast } = useStore();
  const session = useSellerSession();
  const activeShop = getShopById(activeSellerShopId) ?? SHOPS[0];

  const [filter, setFilter] = useState<OrderFilterKey>(
    initialStatus &&
      ["all", "cho_xac_nhan", "dang_chuan_bi", "dang_giao", "da_giao", "tra_huy"].includes(
        initialStatus
      )
      ? initialStatus
      : "all"
  );
  const [opsFilter, setOpsFilter] = useState<OpsQueueFilter>("all");
  const [query, setQuery] = useState("");
  const [paymentFilter, setPaymentFilter] = useState<"all" | PaymentMethod>("all");

  const orders = useMemo(
    () => getUnifiedSellerOrders(activeShop.id, storeOrders, session),
    [activeShop.id, storeOrders, session]
  );

  const counts = useMemo(() => {
    return {
      all: orders.length,
      cho_xac_nhan: orders.filter((o) => o.status === "cho_xac_nhan").length,
      dang_chuan_bi: orders.filter((o) => o.status === "dang_chuan_bi").length,
      dang_giao: orders.filter((o) => o.status === "dang_giao").length,
      da_giao: orders.filter((o) => o.status === "da_giao").length,
      tra_huy: orders.filter((o) => o.status === "yeu_cau_tra_hang" || o.status === "da_huy")
        .length,
    };
  }, [orders]);

  const opsQueueOrders = useMemo(() => {
    return orders.filter((o) => {
      const ex = o.wave2Extra;
      if (!ex) return false;
      const pendingVat =
        ex.vatRequest &&
        (ex.vatRequest.status === "cho_duyet_nhap" || ex.vatRequest.status === "da_gui_nhap_24h");
      const pendingAmend = ex.amendment && ex.amendment.status === "cho_duyet";
      const activeEx = ex.twoWayExchange && ex.twoWayExchange.courierStatus !== "hoan_tat";
      const openCmp =
        ex.tier1Complaint &&
        (ex.tier1Complaint.status === "cho_phan_hoi" || ex.tier1Complaint.status === "da_phan_hoi");
      return Boolean(pendingVat || pendingAmend || activeEx || openCmp);
    });
  }, [orders]);

  const filteredOrders = useMemo(() => {
    const q = query.trim().toLowerCase();
    return orders.filter((o) => {
      if (filter === "tra_huy") {
        if (o.status !== "yeu_cau_tra_hang" && o.status !== "da_huy") return false;
      } else if (filter !== "all" && o.status !== filter) {
        return false;
      }
      if (paymentFilter !== "all" && o.paymentMethod !== paymentFilter) return false;

      const ex = o.wave2Extra;
      if (opsFilter === "vat" && !ex?.vatRequest) return false;
      if (opsFilter === "amendment" && !ex?.amendment) return false;
      if (opsFilter === "exchange" && !ex?.twoWayExchange) return false;
      if (opsFilter === "complaint" && !ex?.tier1Complaint) return false;
      if (opsFilter === "gift" && !(ex?.giftBagRequested || ex?.giftCardMessage)) return false;

      if (q) {
        const hit =
          o.orderCode.toLowerCase().includes(q) ||
          o.buyerName.toLowerCase().includes(q) ||
          o.itemSummary.toLowerCase().includes(q) ||
          o.provinceName.toLowerCase().includes(q) ||
          (ex?.vatRequest?.taxCode.toLowerCase().includes(q) ?? false);
        if (!hit) return false;
      }
      return true;
    });
  }, [orders, filter, opsFilter, paymentFilter, query]);

  const handleAdvance = (orderCode: string, currentStatus: OrderStatus, storeOrderId?: string) => {
    const action = getNextSellerTransition(currentStatus);
    if (!action) return;
    applySellerOrderStatusOverride(orderCode, action.nextStatus, action.note);
    if (storeOrderId) {
      updateOrderStatus(storeOrderId, action.nextStatus, action.note);
    }
    showToast(`Đơn ${orderCode}: ${action.label} thành công.`);
  };

  const handleConfirmAllPending = () => {
    const pending = orders.filter((o) => o.status === "cho_xac_nhan");
    if (pending.length === 0) return;
    for (const o of pending) {
      const action = getNextSellerTransition(o.status);
      if (!action) continue;
      applySellerOrderStatusOverride(o.orderCode, action.nextStatus, action.note);
      if (o.storeOrderId) {
        updateOrderStatus(o.storeOrderId, action.nextStatus, action.note);
      }
    }
    showToast(`Đã xác nhận ${formatInt(pending.length)} đơn chờ và chuyển sang đóng gói.`);
  };

  const countCards: Array<{ key: OrderFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "cho_xac_nhan", label: "Chờ xác nhận", count: counts.cho_xac_nhan },
    { key: "dang_chuan_bi", label: "Đang chuẩn bị", count: counts.dang_chuan_bi },
    { key: "dang_giao", label: "Đang giao", count: counts.dang_giao },
    { key: "da_giao", label: "Đã giao", count: counts.da_giao },
    { key: "tra_huy", label: "Trả hàng / Hủy", count: counts.tra_huy },
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
            {activeShop.name} · {formatInt(counts.cho_xac_nhan)} đơn chờ xác nhận,{" "}
            {formatInt(opsQueueOrders.length)} đơn có yêu cầu VAT, hủy/đổi hoặc khiếu nại cấp 1
          </p>
        </div>
        <div className={common.headerActions}>
          {counts.cho_xac_nhan > 0 && (
            <Button variant="ink" size="sm" shape="app" onClick={handleConfirmAllPending}>
              <IconCheck size={14} />
              <span>Xác nhận đơn chờ ({formatInt(counts.cho_xac_nhan)})</span>
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

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách đơn hàng"
          message="Kết nối đến hệ thống xử lý đơn hàng tạm thời gián đoạn."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/don-hang"
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có đơn hàng nào trong giai đoạn này"
          message="Khi người mua đặt hàng từ gian hàng, đơn sẽ xuất hiện tại đây theo thời gian thực."
          actionLabel="Xem sản phẩm đang bán"
          actionHref="/seller/san-pham"
        />
      )}

      {!uiState && (
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
                    placeholder="Tìm mã đơn, tên khách, MST VAT..."
                    aria-label="Tìm kiếm đơn hàng"
                  />
                </div>

                <select
                  className={common.filterSelect}
                  value={opsFilter}
                  onChange={(e) => setOpsFilter(e.target.value as OpsQueueFilter)}
                  aria-label="Lọc theo yêu cầu vận hành"
                >
                  <option value="all">Mọi yêu cầu bổ sung</option>
                  <option value="vat">Xuất hóa đơn VAT</option>
                  <option value="amendment">Hủy muộn / Đổi địa chỉ</option>
                  <option value="exchange">Đổi size 2 chiều tận nơi</option>
                  <option value="complaint">Khiếu nại cấp 1 (SLA 24h)</option>
                  <option value="gift">Có lời nhắn thiệp / Túi quà</option>
                </select>

                <select
                  className={common.filterSelect}
                  value={paymentFilter}
                  onChange={(e) => setPaymentFilter(e.target.value as "all" | PaymentMethod)}
                  aria-label="Lọc theo phương thức thanh toán"
                >
                  <option value="all">Mọi hình thức thanh toán</option>
                  <option value="vietqr">VietQR</option>
                  <option value="cod">Thu hộ COD</option>
                  <option value="vi_sandbox">Ví NexusPay</option>
                </select>
              </div>
              <span className={common.cardMeta}>
                Hiển thị {formatInt(filteredOrders.length)} trên {formatInt(orders.length)} đơn hàng
              </span>
            </div>

            {filteredOrders.length === 0 ? (
              <div className={common.inlineEmpty}>
                Không có đơn hàng nào khớp bộ lọc trạng thái và yêu cầu hiện tại.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã đơn</th>
                        <th>Người mua</th>
                        <th>Sản phẩm & yêu cầu kèm theo</th>
                        <th className={common.numCol}>SL</th>
                        <th className={common.numCol}>Tổng đơn</th>
                        <th className={common.numCol}>Thực nhận</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredOrders.map((o) => {
                        const meta = getSafeOrderStatusMeta(o.status);
                        const nextAction = getNextSellerTransition(o.status);
                        const signals = getOrderSignals(o);
                        return (
                          <tr key={o.id}>
                            <td className={styles.orderCodeCell}>
                              <Link href={`/seller/don-hang/${o.id}`} className={common.cellLink}>
                                {o.orderCode}
                              </Link>
                              <span className={common.cellSub}>
                                {formatDateTimeVi(o.createdAt)}
                              </span>
                            </td>
                            <td>
                              <span>{o.buyerName}</span>
                              <span className={common.cellSub}>
                                {o.wardName}, {o.provinceName}
                              </span>
                            </td>
                            <td>
                              <div className={styles.itemSummaryCell} title={o.itemSummary}>
                                {o.itemSummary}
                              </div>
                              <span className={common.cellSub}>
                                {PAYMENT_METHOD_LABELS[o.paymentMethod]}
                              </span>
                              {signals.length > 0 && (
                                <div className={styles.signalList}>
                                  {signals.map((sig) => (
                                    <span
                                      key={sig.label}
                                      className={`${styles.signalTag} ${sig.warn ? styles.signalTagWarn : ""}`}
                                    >
                                      {sig.label}
                                    </span>
                                  ))}
                                </div>
                              )}
                            </td>
                            <td className={common.numCol}>{formatInt(o.quantity)}</td>
                            <td className={common.numCol}>{formatVnd(o.subtotal)}</td>
                            <td className={common.numCol}>
                              <strong>{formatVnd(o.netReceivable)}</strong>
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
                                    onClick={() =>
                                      handleAdvance(o.orderCode, o.status, o.storeOrderId)
                                    }
                                  >
                                    {nextAction.shortLabel}
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
                    const meta = getSafeOrderStatusMeta(o.status);
                    const nextAction = getNextSellerTransition(o.status);
                    const signals = getOrderSignals(o);
                    return (
                      <div key={o.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div>
                            <Link
                              href={`/seller/don-hang/${o.id}`}
                              className={styles.mobileOrderCode}
                            >
                              {o.orderCode}
                            </Link>
                            <span className={common.cellSub}>
                              {o.buyerName} · {o.provinceName}
                            </span>
                          </div>
                          <span className={styles.mobileOrderTotal}>{formatVnd(o.subtotal)}</span>
                        </div>
                        <p className={styles.mobileOrderSummary}>{o.itemSummary}</p>
                        {signals.length > 0 && (
                          <div className={styles.signalList}>
                            {signals.map((sig) => (
                              <span
                                key={sig.label}
                                className={`${styles.signalTag} ${sig.warn ? styles.signalTagWarn : ""}`}
                              >
                                {sig.label}
                              </span>
                            ))}
                          </div>
                        )}
                        <div className={common.mobileRowMeta}>
                          <span className={pillCls(meta.tone)}>{meta.label}</span>
                          <div className={styles.actionGroup}>
                            {nextAction && (
                              <button
                                type="button"
                                className={styles.rowAdvanceBtn}
                                onClick={() => handleAdvance(o.orderCode, o.status, o.storeOrderId)}
                              >
                                {nextAction.shortLabel}
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

          {/* Actionable Wave-2 queue: VAT, Late Cancel/Address, 2-Way Exchange, Tier-1 Complaints */}
          {opsQueueOrders.length > 0 && (
            <section className={common.card} aria-labelledby="ops-queue-heading">
              <div className={common.cardHeader}>
                <h2 id="ops-queue-heading" className={common.cardTitle}>
                  Yêu cầu VAT, hủy/đổi địa chỉ, đổi size 2 chiều và khiếu nại cấp 1
                </h2>
                <span className={common.cardMeta}>
                  {formatInt(opsQueueOrders.length)} đơn cần xử lý đúng mốc SLA gian hàng
                </span>
              </div>

              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Đơn hàng</th>
                      <th>Loại yêu cầu</th>
                      <th>Thông tin cần kiểm tra</th>
                      <th className={common.numCol}>Xử lý nhanh</th>
                    </tr>
                  </thead>
                  <tbody>
                    {opsQueueOrders.map((o) => {
                      const ex = o.wave2Extra!;
                      const hasPendingVat =
                        ex.vatRequest &&
                        (ex.vatRequest.status === "cho_duyet_nhap" ||
                          ex.vatRequest.status === "da_gui_nhap_24h");
                      const hasPendingAmend = ex.amendment && ex.amendment.status === "cho_duyet";
                      const hasActiveExchange =
                        ex.twoWayExchange && ex.twoWayExchange.courierStatus !== "hoan_tat";
                      const hasComplaint =
                        ex.tier1Complaint && ex.tier1Complaint.status !== "da_xu_ly";

                      const kindLabel = hasPendingAmend
                        ? ex.amendment?.kind === "huy_muon"
                          ? "Hủy muộn"
                          : "Đổi địa chỉ"
                        : hasActiveExchange
                          ? "Đổi size 2 chiều"
                          : hasComplaint
                            ? "Khiếu nại cấp 1"
                            : "Xuất hóa đơn VAT";

                      const detailText = hasPendingAmend
                        ? ex.amendment?.proposedAddress
                          ? `Địa chỉ mới: ${ex.amendment.proposedAddress}`
                          : ex.amendment?.reason
                        : hasActiveExchange
                          ? `${ex.twoWayExchange?.fromVariant} -> ${ex.twoWayExchange?.toVariant}`
                          : hasComplaint
                            ? `${ex.tier1Complaint?.ticketCode} · ${ex.tier1Complaint?.slaRemainingLabel}`
                            : `MST ${ex.vatRequest?.taxCode} · ${ex.vatRequest?.email}`;

                      return (
                        <tr key={`ops-${o.id}`}>
                          <td className={styles.orderCodeCell}>
                            <Link href={`/seller/don-hang/${o.id}`} className={common.cellLink}>
                              {o.orderCode}
                            </Link>
                            <span className={common.cellSub}>{o.buyerName}</span>
                          </td>
                          <td>
                            <span className={`${common.pill} ${common.pillWarn}`}>{kindLabel}</span>
                          </td>
                          <td>
                            <span>{detailText}</span>
                          </td>
                          <td className={common.numCol}>
                            <div className={styles.actionGroup}>
                              {hasPendingVat && ex.vatRequest?.status === "cho_duyet_nhap" && (
                                <button
                                  type="button"
                                  className={styles.rowAdvanceBtn}
                                  onClick={() => {
                                    advanceOrderVatStatus(o.orderCode, o.status, "da_gui_nhap_24h");
                                    showToast(`Đã gửi bản nháp VAT 24h cho đơn ${o.orderCode}.`);
                                  }}
                                >
                                  Gửi nháp 24h
                                </button>
                              )}
                              {hasPendingVat && ex.vatRequest?.status === "da_gui_nhap_24h" && (
                                <button
                                  type="button"
                                  className={styles.rowAdvanceBtn}
                                  onClick={() => {
                                    advanceOrderVatStatus(o.orderCode, o.status, "da_phat_hanh");
                                    showToast(`Đã phát hành HĐĐT cho đơn ${o.orderCode}.`);
                                  }}
                                >
                                  Phát hành HĐĐT
                                </button>
                              )}
                              {hasPendingAmend && (
                                <button
                                  type="button"
                                  className={styles.rowAdvanceBtn}
                                  onClick={() => {
                                    resolveOrderAmendment(
                                      o.orderCode,
                                      o.status,
                                      "da_duyet",
                                      "Gian hàng đã xác nhận và cập nhật ngay trước khi bàn giao ĐVVC."
                                    );
                                    showToast(
                                      `Đã duyệt yêu cầu ${ex.amendment?.kind === "huy_muon" ? "hủy muộn" : "đổi địa chỉ"} đơn ${o.orderCode}.`
                                    );
                                  }}
                                >
                                  Duyệt yêu cầu
                                </button>
                              )}
                              {hasActiveExchange &&
                                ex.twoWayExchange?.courierStatus === "cho_dieu_phoi" && (
                                  <button
                                    type="button"
                                    className={styles.rowAdvanceBtn}
                                    onClick={() => {
                                      advanceOrderTwoWayExchange(
                                        o.orderCode,
                                        o.status,
                                        "da_dat_buu_ta"
                                      );
                                      showToast(
                                        `Đã điều phối bưu tá đổi size 2 chiều cho đơn ${o.orderCode}.`
                                      );
                                    }}
                                  >
                                    Đặt bưu tá 2 chiều
                                  </button>
                                )}
                              <Link
                                href={`/seller/don-hang/${o.id}?tab=ops`}
                                className={styles.rowDetailLink}
                              >
                                Mở xử lý
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
                {opsQueueOrders.map((o) => (
                  <div key={`ops-m-${o.id}`} className={common.mobileRow}>
                    <div className={common.mobileRowTop}>
                      <div>
                        <Link
                          href={`/seller/don-hang/${o.id}?tab=ops`}
                          className={styles.mobileOrderCode}
                        >
                          {o.orderCode} · {o.buyerName}
                        </Link>
                        <span className={common.cellSub}>
                          {getOrderSignals(o)
                            .map((s) => s.label)
                            .join(" · ")}
                        </span>
                      </div>
                      <Link
                        href={`/seller/don-hang/${o.id}?tab=ops`}
                        className={styles.rowDetailLink}
                      >
                        Mở xử lý
                      </Link>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}
        </>
      )}
    </div>
  );
}

export default function SellerOrdersPage() {
  return (
    <SellerSuspense>
      <SellerOrdersContent />
    </SellerSuspense>
  );
}
