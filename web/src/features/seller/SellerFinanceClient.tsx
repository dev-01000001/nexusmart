"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconRefresh, StateView } from "@/components";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  getSellerOrderStatusMeta,
  type GwSellerBalance,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { EMPTY_DASH, parseUiState } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/tai-chinh/page.module.css";

type LedgerFilter = "all" | "settled" | "pending";
const TRUE_MINUS = "\u2212";

function isOrderSettled(status: string): boolean {
  const st = status.toUpperCase();
  return st === "COMPLETED" || st === "DELIVERED";
}

export interface SellerFinanceClientProps {
  initialBalance: GwSellerBalance | null;
  balanceError?: string | null;
  initialOrders: GwSellerOrder[];
  loadError: string | null;
}

export function SellerFinanceClient({
  initialBalance,
  balanceError = null,
  initialOrders,
  loadError,
}: SellerFinanceClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));

  const [ledgerFilter, setLedgerFilter] = useState<LedgerFilter>("all");

  const nonCancelledOrders = useMemo(
    () => initialOrders.filter((o) => o.status.toUpperCase() !== "CANCELLED"),
    [initialOrders]
  );

  const filteredOrders = useMemo(() => {
    if (ledgerFilter === "all") return nonCancelledOrders;
    if (ledgerFilter === "settled")
      return nonCancelledOrders.filter((o) => isOrderSettled(o.status));
    return nonCancelledOrders.filter((o) => !isOrderSettled(o.status));
  }, [nonCancelledOrders, ledgerFilter]);

  const totalCommission = useMemo(
    () => nonCancelledOrders.reduce((s, o) => s + o.commission_vnd, 0),
    [nonCancelledOrders]
  );
  const visibleGross = useMemo(
    () => filteredOrders.reduce((s, o) => s + o.subtotal_vnd, 0),
    [filteredOrders]
  );
  const visibleCommission = useMemo(
    () => filteredOrders.reduce((s, o) => s + o.commission_vnd, 0),
    [filteredOrders]
  );
  const visibleNet = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeSellerNetReceivable(o), 0),
    [filteredOrders]
  );

  const availableVnd = initialBalance?.available_vnd ?? 0;
  const pendingVnd = initialBalance?.pending_vnd ?? 0;
  const totalEarnedVnd = initialBalance?.total_earned_vnd ?? 0;

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tài chính</h1>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải sổ cái tài chính..."
          rows={5}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sổ cái đối soát"
          message={
            loadError ??
            "Dịch vụ thanh toán tạm thời không phản hồi. Số dư sẽ cập nhật lại sau vài phút."
          }
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa phát sinh bút toán đối soát"
          message="Doanh thu và phí sàn sẽ ghi nhận tự động khi có đơn hàng phát sinh."
          actionLabel="Xem đơn hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && !loadError && (
        <>
          {balanceError && !initialBalance && (
            <div className={styles.balanceWarnBar} role="status">
              <span>Dịch vụ thanh toán tạm gián đoạn, số dư khả dụng đang chờ đồng bộ.</span>
              <Button variant="outline" size="sm" shape="app" onClick={() => router.refresh()}>
                <IconRefresh size={14} />
                <span>Thử lại</span>
              </Button>
            </div>
          )}

          {/* Prominent Hero Balance + 3 Compact Ledger Figures */}
          <section className={styles.balanceHero} aria-label="Tổng hợp số dư gian hàng">
            <div className={styles.heroPrimary}>
              <span className={styles.heroLabel}>Khả dụng rút ngay</span>
              {initialBalance ? (
                <>
                  <span
                    className={`${styles.heroAmount} num ${availableVnd === 0 ? common.balanceValueZero : ""}`}
                    data-reveal="count"
                    data-count-to={availableVnd}
                    data-count-format="vnd"
                  >
                    {formatVnd(availableVnd)}
                  </span>
                  <span className={`${styles.heroRecon} num`}>
                    Tổng số dư đối soát: <strong>{formatVnd(availableVnd + pendingVnd)}</strong>
                  </span>
                </>
              ) : (
                <>
                  <span className={`${styles.heroAmount} ${common.balanceValueZero} num`}>
                    {EMPTY_DASH}
                  </span>
                  <span className={styles.heroRecon}>Chờ đồng bộ từ dịch vụ thanh toán</span>
                </>
              )}
            </div>

            <div className={styles.heroSecondaryGrid}>
              <div className={styles.heroMetricCell}>
                <span className={common.balanceLabel}>Chờ đối soát</span>
                {initialBalance ? (
                  <span
                    className={`${styles.heroMetricVal} num ${pendingVnd === 0 ? common.balanceValueZero : ""}`}
                    data-reveal="count"
                    data-count-to={pendingVnd}
                    data-count-format="vnd"
                  >
                    {formatVnd(pendingVnd)}
                  </span>
                ) : (
                  <span className={`${styles.heroMetricVal} ${common.balanceValueZero}`}>
                    {EMPTY_DASH}
                  </span>
                )}
              </div>

              <div className={styles.heroMetricCell}>
                <span className={common.balanceLabel}>Tổng thu tích lũy</span>
                {initialBalance ? (
                  <span
                    className={`${styles.heroMetricVal} num ${totalEarnedVnd === 0 ? common.balanceValueZero : ""}`}
                    data-reveal="count"
                    data-count-to={totalEarnedVnd}
                    data-count-format="vnd"
                  >
                    {formatVnd(totalEarnedVnd)}
                  </span>
                ) : (
                  <span className={`${styles.heroMetricVal} ${common.balanceValueZero}`}>
                    {EMPTY_DASH}
                  </span>
                )}
              </div>

              <div className={styles.heroMetricCell}>
                <span className={common.balanceLabel}>
                  Phí sàn ({formatInt(nonCancelledOrders.length)} đơn)
                </span>
                <span
                  className={`${styles.heroMetricVal} num ${totalCommission === 0 ? common.balanceValueZero : ""}`}
                  data-reveal="count"
                  data-count-to={totalCommission}
                  data-count-format="vnd"
                >
                  {formatVnd(totalCommission)}
                </span>
              </div>
            </div>
          </section>

          <section className={common.card} aria-labelledby="ledger-heading">
            <div className={styles.ledgerHead}>
              <h2 id="ledger-heading" className={common.cardTitle}>
                Sổ cái đối soát
              </h2>

              <div className={common.segmented} role="group" aria-label="Lọc trạng thái đối soát">
                {(
                  [
                    { key: "all", label: `Tất cả (${formatInt(nonCancelledOrders.length)})` },
                    { key: "settled", label: "Đã giao" },
                    { key: "pending", label: "Đang xử lý" },
                  ] as const
                ).map((opt) => (
                  <button
                    key={opt.key}
                    type="button"
                    aria-pressed={ledgerFilter === opt.key}
                    className={`${common.segmentedBtn} ${ledgerFilter === opt.key ? common.segmentedBtnActive : ""}`}
                    onClick={() => setLedgerFilter(opt.key)}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>

            {filteredOrders.length === 0 ? (
              <div className={common.inlineEmpty}>
                Không có đơn hàng trong bộ lọc đối soát hiện tại.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Ngày</th>
                        <th>Mã đơn</th>
                        <th>Phương thức</th>
                        <th className={common.numCol}>Tiền hàng</th>
                        <th className={common.numCol}>Phí sàn</th>
                        <th className={common.numCol}>Thực nhận</th>
                        <th>Trạng thái</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredOrders.map((o) => {
                        const meta = getSellerOrderStatusMeta(o.status);
                        const channelMeta = getSalesChannelMeta(o.sales_channel);
                        const net = computeSellerNetReceivable(o);
                        return (
                          <tr key={o.id}>
                            <td>{o.created_at ? formatDateVi(o.created_at) : EMPTY_DASH}</td>
                            <td>
                              <Link href={`/seller/don-hang/${o.id}`} className={common.cellLink}>
                                {o.order_code}
                              </Link>
                              <span className={common.cellSub}>{channelMeta.label}</span>
                            </td>
                            <td>{formatPaymentMethodVi(o.payment_method)}</td>
                            <td className={`${common.numCol} ${styles.creditCell}`}>
                              +{formatVnd(o.subtotal_vnd)}
                            </td>
                            <td
                              className={`${common.numCol} ${o.commission_vnd > 0 ? styles.debitCell : styles.mutedCell}`}
                            >
                              {o.commission_vnd > 0
                                ? `${TRUE_MINUS}${formatVnd(o.commission_vnd)}`
                                : EMPTY_DASH}
                            </td>
                            <td className={`${common.numCol} ${styles.runningCell}`}>
                              {formatVnd(net)}
                            </td>
                            <td>
                              <span
                                className={
                                  isOrderSettled(o.status)
                                    ? `${common.pill} ${common.pillPos}`
                                    : `${common.pill} ${common.pillWarn}`
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
                        <td colSpan={3}>Tổng ({formatInt(filteredOrders.length)} đơn)</td>
                        <td className={`${common.numCol} ${styles.creditCell}`}>
                          +{formatVnd(visibleGross)}
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
                  {filteredOrders.map((o) => {
                    const meta = getSellerOrderStatusMeta(o.status);
                    const net = computeSellerNetReceivable(o);
                    return (
                      <div key={o.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div>
                            <Link href={`/seller/don-hang/${o.id}`} className={common.cellLink}>
                              {o.order_code}
                            </Link>
                            <span className={common.cellSub}>
                              {o.created_at ? formatDateVi(o.created_at) : EMPTY_DASH},{" "}
                              {formatPaymentMethodVi(o.payment_method)}
                            </span>
                          </div>
                          <span className={`${styles.creditCell} ${styles.mobileNetVal} num`}>
                            +{formatVnd(net)}
                          </span>
                        </div>
                        <div className={common.mobileRowMeta}>
                          <span
                            className={
                              isOrderSettled(o.status)
                                ? `${common.pill} ${common.pillPos}`
                                : `${common.pill} ${common.pillWarn}`
                            }
                          >
                            {meta.label}
                          </span>
                          <span className="num">Phí sàn: {formatVnd(o.commission_vnd)}</span>
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
