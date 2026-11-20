"use client";

import React, { useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { SHOPS, getShopById } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconWallet, StateView } from "@/components";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import {
  EMPTY_DASH,
  createSellerPayoutEntry,
  getShopBankAccount,
  getShopLedgerWithSession,
  parseUiState,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type LedgerFilter = "all" | "settled" | "pending";
const TRUE_MINUS = "\u2212";

function SellerFinanceContent() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { activeSellerShopId, showToast } = useStore();
  const session = useSellerSession();
  const activeShop = getShopById(activeSellerShopId) ?? SHOPS[0];

  const [ledgerFilter, setLedgerFilter] = useState<LedgerFilter>("all");
  const [showWithdraw, setShowWithdraw] = useState(false);
  const [amountStr, setAmountStr] = useState("5000000");
  const [withdrawError, setWithdrawError] = useState<string | null>(null);

  const ledgerSummary = useMemo(
    () => getShopLedgerWithSession(activeShop.id, session),
    [activeShop.id, session]
  );
  const bank = getShopBankAccount(activeShop.id);

  const filteredEntries = useMemo(() => {
    if (ledgerFilter === "all") return ledgerSummary.entries;
    return ledgerSummary.entries.filter((e) => e.state === ledgerFilter);
  }, [ledgerSummary.entries, ledgerFilter]);

  const visibleCredits = useMemo(
    () => filteredEntries.reduce((s, e) => s + e.credit, 0),
    [filteredEntries]
  );
  const visibleDebits = useMemo(
    () => filteredEntries.reduce((s, e) => s + e.debit, 0),
    [filteredEntries]
  );

  const handleWithdrawSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const raw = Number(amountStr);
    const intAmount = Math.trunc(raw);
    if (!Number.isFinite(raw) || intAmount !== raw || intAmount < 100000) {
      setWithdrawError("Số tiền rút phải là số nguyên VND tối thiểu 100.000₫.");
      return;
    }
    if (intAmount > ledgerSummary.availableBalance) {
      setWithdrawError(
        `Số tiền vượt quá số dư khả dụng (${formatVnd(ledgerSummary.availableBalance)}).`
      );
      return;
    }
    setWithdrawError(null);
    createSellerPayoutEntry(
      activeShop.id,
      intAmount,
      `Rút tiền về ${bank.bank} ${bank.mask} (${bank.branch})`
    );
    setShowWithdraw(false);
    showToast(`Đã tạo lệnh rút ${formatVnd(intAmount)} về ${bank.bank} ${bank.mask}.`);
  };

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tài chính & đối soát</h1>
          <p className={common.finding}>
            {activeShop.name} · Tài khoản nhận tiền {bank.bank} {bank.mask} · Số dư khớp sổ cái{" "}
            {formatVnd(ledgerSummary.netBalance)}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button
            variant="ink"
            size="sm"
            shape="app"
            disabled={ledgerSummary.availableBalance < 100000}
            onClick={() => {
              setWithdrawError(null);
              setAmountStr(String(Math.min(5000000, ledgerSummary.availableBalance)));
              setShowWithdraw((prev) => !prev);
            }}
          >
            <IconWallet size={14} />
            <span>{showWithdraw ? "Đóng lệnh rút" : "Tạo lệnh rút tiền"}</span>
          </Button>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang đối chiếu sổ cái ghi kép và số dư ngân hàng..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sổ cái đối soát"
          message="Dịch vụ thanh toán tạm thời không phản hồi. Số dư sẽ cập nhật lại sau vài phút."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/tai-chinh"
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa phát sinh bút toán đối soát nào"
          message="Bút toán ghi có và khấu trừ phí sàn sẽ tự động ghi nhận khi có đơn hàng hoàn tất."
          actionLabel="Xem đơn hàng gian hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && (
        <>
          {/* Divided 4-column balance strip */}
          <section className={common.balanceStrip} aria-label="Tổng hợp số dư gian hàng">
            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Khả dụng rút ngay</span>
              <span
                className={`${common.balanceValue} ${ledgerSummary.availableBalance === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(ledgerSummary.availableBalance)}
              </span>
              <span className={common.balanceCaption}>
                {ledgerSummary.availableBalance > 0
                  ? `Đã trừ ${formatVnd(ledgerSummary.totalPaidOut)} đã rút`
                  : "Hoàn tất chu kỳ giữ tiền để mở khóa rút"}
              </span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Đang chờ đối soát</span>
              <span
                className={`${common.balanceValue} ${ledgerSummary.pendingBalance === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(ledgerSummary.pendingBalance)}
              </span>
              <span className={common.balanceCaption}>Giữ 3 ngày sau khi giao thành công</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Tổng thu kỳ này</span>
              <span className={common.balanceValue}>{formatVnd(ledgerSummary.totalCredits)}</span>
              <span className={common.balanceCaption}>Tổng phát sinh Ghi có trên sổ cái</span>
            </div>

            <div className={common.balanceCell}>
              <span className={common.balanceLabel}>Phí sàn & hoàn trả</span>
              <span className={common.balanceValue}>
                {formatVnd(ledgerSummary.totalFeesAndRefunds)}
              </span>
              <span className={common.balanceCaption}>
                Hoa hồng {(activeShop.commissionRate * 100).toFixed(1).replace(".", ",")}% và bút
                toán hoàn trả
              </span>
            </div>
          </section>

          {/* Payout withdrawal request panel */}
          {showWithdraw && (
            <form
              className={styles.withdrawCard}
              onSubmit={handleWithdrawSubmit}
              aria-label="Tạo lệnh rút tiền về tài khoản ngân hàng"
            >
              <div className={common.cardHeader}>
                <h2 className={common.cardTitle}>Tạo lệnh rút tiền về tài khoản ngân hàng</h2>
                <span className={common.cardMeta}>
                  Khả dụng tối đa: {formatVnd(ledgerSummary.availableBalance)}
                </span>
              </div>

              <div className={styles.withdrawGrid}>
                <div className={common.field}>
                  <label htmlFor="withdraw-amount" className={common.fieldLabel}>
                    Số tiền muốn rút (số nguyên VND)
                  </label>
                  <input
                    id="withdraw-amount"
                    type="number"
                    min={100000}
                    max={ledgerSummary.availableBalance}
                    step={10000}
                    className={common.textInput}
                    value={amountStr}
                    onChange={(e) => setAmountStr(e.target.value)}
                  />
                  <div className={styles.quickFillRow}>
                    <button
                      type="button"
                      className={styles.quickChip}
                      onClick={() =>
                        setAmountStr(
                          String(Math.floor(ledgerSummary.availableBalance / 2 / 1000) * 1000)
                        )
                      }
                    >
                      50% khả dụng
                    </button>
                    <button
                      type="button"
                      className={styles.quickChip}
                      onClick={() => setAmountStr(String(ledgerSummary.availableBalance))}
                    >
                      Rút tối đa ({formatVnd(ledgerSummary.availableBalance)})
                    </button>
                  </div>
                  {withdrawError && <span className={common.fieldError}>{withdrawError}</span>}
                </div>

                <div className={styles.bankSummary}>
                  <span className={styles.bankTitle}>
                    {bank.bank} · {bank.mask}
                  </span>
                  <span className={styles.bankMeta}>{bank.holder}</span>
                  <span className={styles.bankMeta}>
                    {bank.branch} · Chuyển nhanh Napas 24/7 (miễn phí rút)
                  </span>
                </div>
              </div>

              <div className={styles.withdrawActions}>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={() => setShowWithdraw(false)}
                >
                  Hủy
                </Button>
                <Button type="submit" variant="ink" size="sm" shape="app">
                  Xác nhận lệnh rút
                </Button>
              </div>
            </form>
          )}

          {/* Double-entry ledger table */}
          <section className={common.card} aria-labelledby="ledger-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="ledger-heading" className={common.cardTitle}>
                  Sổ cái đối soát ghi kép
                </h2>
                <p className={styles.reconFormula}>
                  Tổng Có ({formatVnd(ledgerSummary.totalCredits)}) {TRUE_MINUS} Tổng Nợ (
                  {formatVnd(ledgerSummary.totalDebits)}) ={" "}
                  <strong>{formatVnd(ledgerSummary.netBalance)}</strong> (Khớp Khả dụng + Chờ đối
                  soát)
                </p>
              </div>

              <div className={common.segmented} role="group" aria-label="Lọc trạng thái bút toán">
                {(
                  [
                    { key: "all", label: `Tất cả (${formatInt(ledgerSummary.entries.length)})` },
                    { key: "settled", label: "Đã quyết toán" },
                    { key: "pending", label: "Chờ đối soát" },
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

            <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
              <table className={common.dataTable}>
                <thead>
                  <tr>
                    <th>Ngày</th>
                    <th>Mã bút toán</th>
                    <th>Loại giao dịch</th>
                    <th className={common.numCol}>Ghi có (VND)</th>
                    <th className={common.numCol}>Ghi nợ (VND)</th>
                    <th className={common.numCol}>Số dư lũy kế</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredEntries.map((entry) => (
                    <tr key={entry.id}>
                      <td>{formatDateVi(entry.date)}</td>
                      <td>
                        <strong>{entry.entryCode}</strong>
                        <span className={common.cellSub}>Tham chiếu: {entry.referenceCode}</span>
                      </td>
                      <td>
                        <span>{entry.typeLabel}</span>
                        <span className={common.cellSub}>{entry.description}</span>
                      </td>
                      <td
                        className={`${common.numCol} ${entry.credit > 0 ? styles.creditCell : styles.mutedCell}`}
                      >
                        {entry.credit > 0 ? `+${formatVnd(entry.credit)}` : EMPTY_DASH}
                      </td>
                      <td
                        className={`${common.numCol} ${entry.debit > 0 ? styles.debitCell : styles.mutedCell}`}
                      >
                        {entry.debit > 0 ? `${TRUE_MINUS}${formatVnd(entry.debit)}` : EMPTY_DASH}
                      </td>
                      <td className={`${common.numCol} ${styles.runningCell}`}>
                        {formatVnd(entry.runningBalance)}
                      </td>
                      <td>
                        <span
                          className={
                            entry.state === "settled"
                              ? `${common.pill} ${common.pillPos}`
                              : `${common.pill} ${common.pillWarn}`
                          }
                        >
                          {entry.state === "settled" ? "Đã quyết toán" : "Chờ đối soát"}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td colSpan={3}>
                      Tổng cộng ({formatInt(filteredEntries.length)} bút toán hiển thị)
                    </td>
                    <td className={`${common.numCol} ${styles.creditCell}`}>
                      +{formatVnd(visibleCredits)}
                    </td>
                    <td className={common.numCol}>
                      {visibleDebits > 0 ? `${TRUE_MINUS}${formatVnd(visibleDebits)}` : "0₫"}
                    </td>
                    <td className={common.numCol}>{formatVnd(visibleCredits - visibleDebits)}</td>
                    <td />
                  </tr>
                </tfoot>
              </table>
            </div>

            {/* Distinct phone 2-line ledger entries */}
            <div className={common.mobileList}>
              {filteredEntries.map((entry) => (
                <div key={entry.id} className={common.mobileRow}>
                  <div className={common.mobileRowTop}>
                    <div>
                      <strong>{entry.typeLabel}</strong>
                      <span className={common.cellSub}>
                        {formatDateVi(entry.date)} · {entry.entryCode}
                      </span>
                    </div>
                    <span
                      className={entry.credit > 0 ? styles.creditCell : styles.debitCell}
                      style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums" }}
                    >
                      {entry.credit > 0
                        ? `+${formatVnd(entry.credit)}`
                        : `${TRUE_MINUS}${formatVnd(entry.debit)}`}
                    </span>
                  </div>
                  <div className={common.mobileRowMeta}>
                    <span
                      className={
                        entry.state === "settled"
                          ? `${common.pill} ${common.pillPos}`
                          : `${common.pill} ${common.pillWarn}`
                      }
                    >
                      {entry.state === "settled" ? "Đã quyết toán" : "Chờ đối soát"}
                    </span>
                    <span style={{ fontVariantNumeric: "tabular-nums" }}>
                      Lũy kế: <strong>{formatVnd(entry.runningBalance)}</strong>
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </>
      )}
    </div>
  );
}

export default function SellerFinancePage() {
  return (
    <SellerSuspense>
      <SellerFinanceContent />
    </SellerSuspense>
  );
}
