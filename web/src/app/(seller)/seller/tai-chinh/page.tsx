"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type {
  GatewayOrder,
  GatewaySellerBalance,
  GatewayShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { Button, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import {
  PAYMENT_METHOD_TEXT,
  computeOrderNetVnd,
  getOrderStatusMeta,
} from "@/features/seller/types";
import { EMPTY_DASH, getShopBankAccount, parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type FinanceFilter = "all" | "settled" | "pending";
const TRUE_MINUS = "\u2212";

function isSettledOrder(status: string): boolean {
  return status === "COMPLETED" || status === "DELIVERED";
}

function isPendingOrder(status: string): boolean {
  return status === "CONFIRMED" || status === "PREPARING" || status === "SHIPPING";
}

function SellerFinanceInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { mockShopKey } = useSellerContext();

  const [balance, setBalance] = useState<GatewaySellerBalance | null>(null);
  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [filter, setFilter] = useState<FinanceFilter>("all");

  const bankRef = useMemo(() => getShopBankAccount(mockShopKey), [mockShopKey]);

  const loadFinance = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [balRes, ordersRes] = await Promise.all([
        api<GatewaySellerBalance>("seller/balance"),
        api<PaginatedResponse<GatewayOrder>>("seller/orders?page=1&page_size=50"),
      ]);
      setBalance(balRes);
      setOrders(ordersRes.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải số dư và đối soát tài chính lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!uiState) {
      void loadFinance();
    }
  }, [uiState, loadFinance]);

  const activeOrders = useMemo(
    () => orders.filter((o) => o.status !== "CANCELLED" && o.status !== "RETURNED"),
    [orders]
  );

  const filteredOrders = useMemo(() => {
    if (filter === "settled") return activeOrders.filter((o) => isSettledOrder(o.status));
    if (filter === "pending") return activeOrders.filter((o) => isPendingOrder(o.status));
    return activeOrders;
  }, [activeOrders, filter]);

  const visibleSubtotal = useMemo(
    () => filteredOrders.reduce((s, o) => s + o.subtotal_vnd, 0),
    [filteredOrders]
  );
  const visibleCommission = useMemo(
    () => filteredOrders.reduce((s, o) => s + o.commission_vnd, 0),
    [filteredOrders]
  );
  const visibleNet = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeOrderNetVnd(o), 0),
    [filteredOrders]
  );

  const completedCount = useMemo(
    () => activeOrders.filter((o) => isSettledOrder(o.status)).length,
    [activeOrders]
  );

  const availableVnd = balance?.available_vnd ?? 0;
  const pendingVnd = balance?.pending_vnd ?? 0;
  const totalEarnedVnd = balance?.total_earned_vnd ?? 0;

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tài chính & đối soát</h1>
          <p className={common.finding}>
            {shop.name} · Khả dụng {formatVnd(availableVnd)} · Chờ đối soát {formatVnd(pendingVnd)}{" "}
            · Tổng doanh thu ghi nhận {formatVnd(totalEarnedVnd)}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadFinance()}>
            Làm mới số dư
          </Button>
        </div>
      </header>

      {(uiState === "loading" || (!uiState && loading)) && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải số dư gian hàng và bảng đối soát đơn hàng..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sổ dư tài chính"
          message={errorMsg ?? "Dịch vụ thanh toán tạm thời không phản hồi."}
          actionLabel="Thử lại"
          onAction={() => void loadFinance()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa phát sinh giao dịch đối soát nào"
          message="Doanh thu và hoa hồng sàn sẽ tự động cập nhật khi có đơn hàng phát sinh."
          actionLabel="Xem đơn hàng gian hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && !loading && !errorMsg && (
        <>
          <section className={common.balanceStrip} aria-label="Tổng hợp số dư gian hàng">
            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Khả dụng</span>
              <span
                className={`${common.balanceValue} ${availableVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(availableVnd)}
              </span>
              <span className={common.balanceCaption}>Số dư đã quyết toán trên hệ thống</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Đang chờ đối soát</span>
              <span
                className={`${common.balanceValue} ${pendingVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(pendingVnd)}
              </span>
              <span className={common.balanceCaption}>Tạm giữ cho các đơn đang xử lý & giao</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Tổng thu lũy kế</span>
              <span
                className={`${common.balanceValue} ${totalEarnedVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(totalEarnedVnd)}
              </span>
              <span className={common.balanceCaption}>Tổng số tiền đã ghi nhận về gian hàng</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Đơn đã giao / hoàn tất</span>
              <span
                className={`${common.balanceValue} ${completedCount === 0 ? common.balanceValueZero : ""}`}
              >
                {formatInt(completedCount)}
              </span>
              <span className={common.balanceCaption}>
                Trên {formatInt(activeOrders.length)} đơn hợp lệ gần nhất
              </span>
            </div>
          </section>

          <section className={common.card} aria-labelledby="ledger-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="ledger-heading" className={common.cardTitle}>
                  Đối chiếu doanh thu theo đơn hàng
                </h2>
                <p className={styles.reconFormula}>
                  Tiền hàng ({formatVnd(visibleSubtotal)}) {TRUE_MINUS} Phí sàn (
                  {formatVnd(visibleCommission)}) = Thực nhận dự kiến{" "}
                  <strong>{formatVnd(visibleNet)}</strong>
                </p>
              </div>

              <div className={common.segmented} role="group" aria-label="Lọc trạng thái đơn hàng">
                {(
                  [
                    { key: "all", label: `Tất cả (${formatInt(activeOrders.length)})` },
                    { key: "settled", label: "Đã giao / Hoàn tất" },
                    { key: "pending", label: "Đang xử lý" },
                  ] as const
                ).map((opt) => (
                  <button
                    key={opt.key}
                    type="button"
                    aria-pressed={filter === opt.key}
                    className={`${common.segmentedBtn} ${filter === opt.key ? common.segmentedBtnActive : ""}`}
                    onClick={() => setFilter(opt.key)}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>

            {filteredOrders.length === 0 ? (
              <div className={common.inlineEmpty}>
                Chưa có đơn hàng nào thuộc nhóm đối soát này.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Thời gian</th>
                        <th>Mã đơn hàng</th>
                        <th>Thanh toán</th>
                        <th className={common.numCol}>Tiền hàng</th>
                        <th className={common.numCol}>Chiết khấu & phí sàn</th>
                        <th className={common.numCol}>Thực nhận dự kiến</th>
                        <th>Trạng thái</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredOrders.map((order) => {
                        const meta = getOrderStatusMeta(order.status);
                        const deductions =
                          order.flash_sale_discount_vnd +
                          order.shop_voucher_discount_vnd +
                          order.commission_vnd;
                        const netVnd = computeOrderNetVnd(order);
                        return (
                          <tr key={order.id}>
                            <td>
                              {order.created_at ? formatDateTimeVi(order.created_at) : EMPTY_DASH}
                            </td>
                            <td>
                              <Link
                                href={`/seller/don-hang/${order.id}`}
                                className={common.cellLink}
                              >
                                {order.order_code}
                              </Link>
                            </td>
                            <td>
                              {PAYMENT_METHOD_TEXT[order.payment_method] ?? order.payment_method}
                            </td>
                            <td className={`${common.numCol} ${styles.creditCell}`}>
                              +{formatVnd(order.subtotal_vnd)}
                            </td>
                            <td className={`${common.numCol} ${styles.debitCell}`}>
                              {deductions > 0
                                ? `${TRUE_MINUS}${formatVnd(deductions)}`
                                : EMPTY_DASH}
                            </td>
                            <td className={`${common.numCol} ${styles.runningCell}`}>
                              {formatVnd(netVnd)}
                            </td>
                            <td>
                              <span
                                className={
                                  meta.tone === "pos"
                                    ? `${common.pill} ${common.pillPos}`
                                    : meta.tone === "warn"
                                      ? `${common.pill} ${common.pillWarn}`
                                      : common.pill
                                }
                              >
                                {meta.label}
                              </span>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                    <tfoot>
                      <tr>
                        <td colSpan={3}>Tổng cộng ({formatInt(filteredOrders.length)} đơn hàng)</td>
                        <td className={`${common.numCol} ${styles.creditCell}`}>
                          +{formatVnd(visibleSubtotal)}
                        </td>
                        <td className={common.numCol}>
                          {visibleCommission > 0
                            ? `${TRUE_MINUS}${formatVnd(visibleCommission)}`
                            : "0₫"}
                        </td>
                        <td className={common.numCol}>{formatVnd(visibleNet)}</td>
                        <td />
                      </tr>
                    </tfoot>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {filteredOrders.map((order) => {
                    const meta = getOrderStatusMeta(order.status);
                    const netVnd = computeOrderNetVnd(order);
                    return (
                      <div key={order.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div>
                            <Link href={`/seller/don-hang/${order.id}`} className={common.cellLink}>
                              {order.order_code}
                            </Link>
                            <span className={common.cellSub}>
                              {order.created_at ? formatDateTimeVi(order.created_at) : EMPTY_DASH}
                            </span>
                          </div>
                          <span className={styles.runningCell}>{formatVnd(netVnd)}</span>
                        </div>
                        <div className={common.mobileRowMeta}>
                          <span className={common.pill}>{meta.label}</span>
                          <span>Tiền hàng: {formatVnd(order.subtotal_vnd)}</span>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </section>

          <section className={common.card} aria-label="Thông tin tài khoản nhận giải ngân">
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>Tài khoản nhận giải ngân (tham khảo)</h2>
              <span className={common.cardMeta}>
                Lệnh rút tiền ngân hàng tự động sẽ mở ở giai đoạn thanh toán tiếp theo
              </span>
            </div>
            <div className={styles.bankSummary}>
              <span className={styles.bankTitle}>
                {bankRef.bank} · {bankRef.mask}
              </span>
              <span className={styles.bankMeta}>{bankRef.holder}</span>
              <span className={styles.bankMeta}>{bankRef.branch}</span>
            </div>
          </section>
        </>
      )}
    </div>
  );
}

function SellerFinanceContent() {
  return (
    <SellerShopGate pageTitle="Tài chính & đối soát">
      {(shop) => <SellerFinanceInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerFinancePage() {
  return (
    <SellerSuspense>
      <SellerFinanceContent />
    </SellerSuspense>
  );
}
