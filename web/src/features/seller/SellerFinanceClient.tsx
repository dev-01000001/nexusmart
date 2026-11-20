"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { StateView } from "@/components";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { useSellerContext } from "./SellerShell";
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
  initialOrders: GwSellerOrder[];
  loadError: string | null;
}

export function SellerFinanceClient({
  initialBalance,
  initialOrders,
  loadError,
}: SellerFinanceClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { shop } = useSellerContext();

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
          <h1 className={common.title}>Tài chính & đối soát</h1>
          <p className={common.finding}>
            {shop?.name ?? "Gian hàng"} · Số dư khả dụng {formatVnd(availableVnd)} · Đang chờ đối
            soát {formatVnd(pendingVnd)}
          </p>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang đối chiếu sổ cái ghi kép và số dư gian hàng..."
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
          title="Chưa phát sinh bút toán đối soát nào"
          message="Doanh thu và khấu trừ phí sàn sẽ tự động ghi nhận khi có đơn hàng phát sinh."
          actionLabel="Xem đơn hàng gian hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && !loadError && (
        <>
          <section className={common.balanceStrip} aria-label="Tổng hợp số dư gian hàng">
            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Khả dụng rút ngay</span>
              <span
                className={`${common.balanceValue} ${availableVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(availableVnd)}
              </span>
              <span className={common.balanceCaption}>
                Đã giải ngân vào tài khoản khả dụng của gian hàng
              </span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Đang chờ đối soát</span>
              <span
                className={`${common.balanceValue} ${pendingVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(pendingVnd)}
              </span>
              <span className={common.balanceCaption}>Giữ tạm cho tới khi đơn hoàn tất</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Tổng thu tích lũy</span>
              <span
                className={`${common.balanceValue} ${totalEarnedVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(totalEarnedVnd)}
              </span>
              <span className={common.balanceCaption}>Ghi nhận từ sổ cái dịch vụ thanh toán</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Tổng phí sàn (đơn hiện tại)</span>
              <span className={common.balanceValue}>{formatVnd(totalCommission)}</span>
              <span className={common.balanceCaption}>
                Tổng hoa hồng sàn trên {formatInt(nonCancelledOrders.length)} đơn hợp lệ
              </span>
            </div>
          </section>

          <section className={common.card} aria-labelledby="ledger-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="ledger-heading" className={common.cardTitle}>
                  Bảng đối soát doanh thu theo đơn hàng
                </h2>
                <p className={styles.reconFormula}>
                  Số dư khả dụng ({formatVnd(availableVnd)}) + Chờ đối soát ({formatVnd(pendingVnd)}
                  ) = <strong>{formatVnd(availableVnd + pendingVnd)}</strong> · Lệnh rút ngân hàng
                  tự động sẽ mở ở pha chi hộ tiếp theo
                </p>
              </div>

              <div className={common.segmented} role="group" aria-label="Lọc trạng thái đối soát">
                {(
                  [
                    { key: "all", label: `Tất cả (${formatInt(nonCancelledOrders.length)})` },
                    { key: "settled", label: "Đã giao / Hoàn tất" },
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
                Chưa có đơn hàng nào phát sinh doanh thu trong bộ lọc hiện tại.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Ngày</th>
                        <th>Mã đơn hàng</th>
                        <th>Phương thức</th>
                        <th className={common.numCol}>Tiền hàng (VND)</th>
                        <th className={common.numCol}>Phí sàn (VND)</th>
                        <th className={common.numCol}>Thực nhận (VND)</th>
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
                        <td colSpan={3}>
                          Tổng cộng ({formatInt(filteredOrders.length)} đơn hàng hiển thị)
                        </td>
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
                              {o.created_at ? formatDateVi(o.created_at) : EMPTY_DASH} ·{" "}
                              {formatPaymentMethodVi(o.payment_method)}
                            </span>
                          </div>
                          <span
                            className={styles.creditCell}
                            style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums" }}
                          >
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
                          <span style={{ fontVariantNumeric: "tabular-nums" }}>
                            Phí sàn: {formatVnd(o.commission_vnd)}
                          </span>
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
