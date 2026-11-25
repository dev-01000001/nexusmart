"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconRefresh, StateView } from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import {
  formatDateTimeVi,
  formatDateVi,
  formatInt,
  formatSignedVnd,
  formatVnd,
} from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { useSellerContext } from "./SellerShell";
import { statusPillClass } from "./SellerUiPrimitives";
import {
  computeSellerNetReceivable,
  computeSellerOrderDeductions,
  formatPaymentMethodVi,
  getSellerOrderStatusMeta,
  type GwSellerBalance,
  type GwSellerBankAccount,
  type GwSellerLedgerEntry,
  type GwSellerOrder,
} from "./sellerApiTypes";
import {
  BANK_ACCOUNT_STATUS_META,
  EMPTY_DASH,
  isTerminalBankOtpError,
  parseUiState,
  validateBankAccountInput,
} from "./types";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/tai-chinh/page.module.css";

type OrderFilter = "all" | "settled" | "pending";
type LedgerAccountFilter = "" | "pending" | "available";
const TRUE_MINUS = "\u2212";

const VN_BANK_PRESETS = [
  { code: "VCB", name: "Ngân hàng TMCP Ngoại thương Việt Nam (Vietcombank)" },
  { code: "TCB", name: "Ngân hàng TMCP Kỹ thương Việt Nam (Techcombank)" },
  { code: "ACB", name: "Ngân hàng TMCP Á Châu (ACB)" },
  { code: "MBB", name: "Ngân hàng TMCP Quân đội (MB Bank)" },
  { code: "BIDV", name: "Ngân hàng TMCP Đầu tư và Phát triển Việt Nam (BIDV)" },
  { code: "CTG", name: "Ngân hàng TMCP Công thương Việt Nam (VietinBank)" },
  { code: "VPB", name: "Ngân hàng TMCP Việt Nam Thịnh Vượng (VPBank)" },
  { code: "TPB", name: "Ngân hàng TMCP Tiên Phong (TPBank)" },
] as const;

function isOrderSettled(status: string): boolean {
  const st = status.toUpperCase();
  return st === "COMPLETED" || st === "DELIVERED";
}

export interface SellerFinanceClientProps {
  initialBalance: GwSellerBalance | null;
  balanceError?: string | null;
  initialBankAccount: GwSellerBankAccount | null;
  initialOrders: GwSellerOrder[];
  loadError: string | null;
}

export function SellerFinanceClient({
  initialBalance,
  balanceError = null,
  initialBankAccount,
  initialOrders,
  loadError,
}: SellerFinanceClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useSellerContext();

  const [balance, setBalance] = useState<GwSellerBalance | null>(initialBalance);
  const [bankAccount, setBankAccount] = useState<GwSellerBankAccount | null>(initialBankAccount);
  const [orderFilter, setOrderFilter] = useState<OrderFilter>("all");
  const [refreshing, setRefreshing] = useState(false);

  // Bank account form & Email OTP state
  const [showBankForm, setShowBankForm] = useState(false);
  const [bankCode, setBankCode] = useState<string>(VN_BANK_PRESETS[0].code);
  const [bankName, setBankName] = useState<string>(VN_BANK_PRESETS[0].name);
  const [accountNumber, setAccountNumber] = useState("");
  const [accountHolderName, setAccountHolderName] = useState("");
  const [bankFormError, setBankFormError] = useState<string | null>(null);
  const [bankSaving, setBankSaving] = useState(false);

  const [otpInput, setOtpInput] = useState("");
  const [otpError, setOtpError] = useState<string | null>(null);
  const [otpSubmitting, setOtpSubmitting] = useState(false);

  // Real double-entry ledger (/api/seller/ledger)
  const [ledgerEntries, setLedgerEntries] = useState<GwSellerLedgerEntry[]>([]);
  const [ledgerTotal, setLedgerTotal] = useState(0);
  const [ledgerPage, setLedgerPage] = useState(1);
  const [ledgerAccountFilter, setLedgerAccountFilter] = useState<LedgerAccountFilter>("");
  const [ledgerLoading, setLedgerLoading] = useState(true);
  const [ledgerError, setLedgerError] = useState<string | null>(null);
  const ledgerSeqRef = useRef(0);

  const loadLedger = useCallback(async () => {
    const seq = ++ledgerSeqRef.current;
    setLedgerLoading(true);
    setLedgerError(null);
    try {
      const qs = new URLSearchParams({
        page: String(ledgerPage),
        page_size: "20",
      });
      if (ledgerAccountFilter) {
        qs.set("account_filter", ledgerAccountFilter);
      }
      const res = await api<{ items?: GwSellerLedgerEntry[]; total?: number }>(
        `/api/seller/ledger?${qs.toString()}`
      );
      if (ledgerSeqRef.current !== seq) return;
      const items = res.items ?? [];
      setLedgerEntries(items);
      setLedgerTotal(Math.max(0, Math.trunc(res.total ?? items.length)));
    } catch (err) {
      if (ledgerSeqRef.current !== seq) return;
      setLedgerError(getErrorMessage(err, "Không thể tải sổ cái giao dịch lúc này."));
    } finally {
      if (ledgerSeqRef.current === seq) {
        setLedgerLoading(false);
      }
    }
  }, [ledgerPage, ledgerAccountFilter]);

  useEffect(() => {
    if (!uiState && !loadError) {
      void loadLedger();
    }
  }, [uiState, loadError, loadLedger]);

  const handleRefreshAll = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      const [balRes, bankRes] = await Promise.all([
        api<GwSellerBalance>("/api/seller/balance").catch(() => null),
        api<{ bank_account?: GwSellerBankAccount | null }>("/api/seller/bank-account").catch(
          () => null
        ),
        loadLedger(),
      ]);
      if (balRes) setBalance(balRes);
      if (bankRes) setBankAccount(bankRes.bank_account ?? null);
      router.refresh();
    } finally {
      setRefreshing(false);
    }
  };

  const handleSubmitBankAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    if (bankSaving) return;
    const validation = validateBankAccountInput({
      bankCode,
      bankName,
      accountNumber,
      accountHolderName,
    });
    if (!validation.ok) {
      setBankFormError(validation.message);
      return;
    }

    setBankFormError(null);
    setBankSaving(true);
    try {
      const res = await api<{ bank_account?: GwSellerBankAccount | null }>(
        "/api/seller/bank-account",
        {
          method: "POST",
          body: {
            bank_code: bankCode.trim().toUpperCase(),
            bank_name: bankName.trim(),
            account_number: accountNumber.trim(),
            account_holder_name: accountHolderName.trim().toUpperCase(),
          },
        }
      );
      setBankAccount(res?.bank_account ?? null);
      setAccountNumber("");
      setOtpInput("");
      setOtpError(null);
      setShowBankForm(false);
      showToast("Đã ghi nhận tài khoản ngân hàng. Kiểm tra email để nhập mã OTP 6 chữ số.");
    } catch (err) {
      setBankFormError(getErrorMessage(err, "Không thể lưu tài khoản ngân hàng lúc này."));
    } finally {
      setBankSaving(false);
    }
  };

  const handleVerifyBankOtp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (otpSubmitting) return;
    const cleanOtp = otpInput.trim();
    if (!/^\d{6}$/.test(cleanOtp)) {
      setOtpError("Mã OTP gồm đúng 6 chữ số gửi về email tài khoản.");
      return;
    }
    setOtpError(null);
    setOtpSubmitting(true);
    try {
      const res = await api<{ bank_account?: GwSellerBankAccount | null }>(
        "/api/seller/bank-account/verify-otp",
        {
          method: "POST",
          body: { otp: cleanOtp },
        }
      );
      const updated = res?.bank_account ?? null;
      setBankAccount(updated);
      setOtpInput("");
      if (updated?.status === "VERIFIED") {
        showToast("Đã xác thực OTP thành công. Tài khoản ngân hàng sẵn sàng nhận giải ngân.");
      } else if (updated?.status === "PENDING_ADMIN_REVIEW") {
        showToast(
          "Đã xác thực OTP. Tên chủ tài khoản khác tên chủ shop lúc được duyệt nên hồ sơ chờ quản trị duyệt."
        );
      } else {
        showToast("Đã xác thực mã OTP tài khoản ngân hàng.");
      }
    } catch (err) {
      if (isTerminalBankOtpError(err)) {
        const msg = getErrorMessage(
          err,
          "Mã OTP đã hết hạn hoặc nhập sai quá số lần quy định. Vui lòng gửi lại thông tin ngân hàng để nhận mã mới."
        );
        setOtpInput("");
        setOtpError(null);
        setBankAccount((prev) => (prev?.status === "PENDING_OTP" ? null : prev));
        setShowBankForm(true);
        setBankFormError(msg);
      } else {
        setOtpError(getErrorMessage(err, "Mã OTP chưa chính xác. Vui lòng kiểm tra lại."));
      }
    } finally {
      setOtpSubmitting(false);
    }
  };

  const nonCancelledOrders = useMemo(
    () =>
      initialOrders.filter((o) => {
        const st = o.status.toUpperCase();
        return st !== "CANCELLED" && st !== "RETURNED";
      }),
    [initialOrders]
  );

  const filteredOrders = useMemo(() => {
    if (orderFilter === "all") return nonCancelledOrders;
    if (orderFilter === "settled")
      return nonCancelledOrders.filter((o) => isOrderSettled(o.status));
    return nonCancelledOrders.filter((o) => !isOrderSettled(o.status));
  }, [nonCancelledOrders, orderFilter]);

  const totalDeductionsAll = useMemo(
    () => nonCancelledOrders.reduce((s, o) => s + computeSellerOrderDeductions(o), 0),
    [nonCancelledOrders]
  );
  const visibleGross = useMemo(
    () => filteredOrders.reduce((s, o) => s + o.subtotal_vnd, 0),
    [filteredOrders]
  );
  const visibleDeductions = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeSellerOrderDeductions(o), 0),
    [filteredOrders]
  );
  const visibleNet = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeSellerNetReceivable(o), 0),
    [filteredOrders]
  );

  const availableVnd = balance?.available_vnd ?? 0;
  const pendingVnd = balance?.pending_vnd ?? 0;
  const totalEarnedVnd = balance?.total_earned_vnd ?? 0;

  const bankStatusMeta = bankAccount
    ? (BANK_ACCOUNT_STATUS_META[bankAccount.status] ?? {
        label: bankAccount.status,
        tone: "neutral" as const,
      })
    : null;

  const isPayoutLocked = useMemo(() => {
    if (!bankAccount?.payout_locked_until) return false;
    const ms = Date.parse(bankAccount.payout_locked_until);
    return Number.isFinite(ms) && ms > Date.now();
  }, [bankAccount?.payout_locked_until]);

  const ledgerTotalPages = Math.max(1, Math.ceil(ledgerTotal / 20));

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tài chính</h1>
        </div>
        <div className={common.headerActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={refreshing}
            onClick={() => void handleRefreshAll()}
          >
            <IconRefresh size={14} />
            <span>{refreshing ? "Đang tải..." : "Làm mới"}</span>
          </Button>
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
          message={loadError ?? "Dịch vụ thanh toán tạm thời không phản hồi."}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa phát sinh bút toán đối soát"
          message="Doanh thu và phí sẽ ghi nhận tự động khi có đơn hàng phát sinh."
          actionLabel="Xem đơn hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && !loadError && (
        <>
          {balanceError && !balance && (
            <div className={styles.balanceWarnBar} role="status">
              <span>Dịch vụ thanh toán tạm gián đoạn, số dư khả dụng đang chờ đồng bộ.</span>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => void handleRefreshAll()}
              >
                <IconRefresh size={14} />
                <span>Thử lại</span>
              </Button>
            </div>
          )}

          {/* Hero Balance Strip */}
          <section className={styles.balanceHero} aria-label="Tổng hợp số dư gian hàng">
            <div className={styles.heroPrimary}>
              <span className={styles.heroLabel}>Khả dụng rút ngay</span>
              {balance ? (
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
                    Tổng số dư: <strong>{formatVnd(availableVnd + pendingVnd)}</strong>
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
                <span className={common.balanceLabel}>Đang tạm giữ (72 giờ)</span>
                {balance ? (
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
                {balance ? (
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
                  Chiết khấu &amp; phí ({formatInt(nonCancelledOrders.length)} đơn)
                </span>
                <span
                  className={`${styles.heroMetricVal} num ${totalDeductionsAll === 0 ? common.balanceValueZero : ""}`}
                  data-reveal="count"
                  data-count-to={totalDeductionsAll}
                  data-count-format="vnd"
                >
                  {formatVnd(totalDeductionsAll)}
                </span>
              </div>
            </div>
          </section>

          {/* Bank Account + Email OTP + 24h Payout Lock */}
          <section className={common.card} aria-labelledby="bank-account-heading">
            <div className={common.cardHeader}>
              <div>
                <h2 id="bank-account-heading" className={common.cardTitle}>
                  Tài khoản ngân hàng nhận tiền
                </h2>
                <span className={common.cardMeta}>
                  Xác thực OTP qua email và khóa giải ngân 24 giờ khi thay đổi
                </span>
              </div>
              <Button
                variant={showBankForm ? "outline" : "ink"}
                size="sm"
                shape="app"
                onClick={() => {
                  setBankFormError(null);
                  setShowBankForm((prev) => !prev);
                }}
              >
                {showBankForm
                  ? "Đóng biểu mẫu"
                  : bankAccount
                    ? "Cập nhật tài khoản"
                    : "Liên kết ngân hàng"}
              </Button>
            </div>

            {bankAccount ? (
              <div className={styles.bankSummary}>
                <div className={styles.bankSummaryHead}>
                  <span className={styles.bankTitle}>
                    {bankAccount.bank_code} · {bankAccount.bank_name} ·{" "}
                    {bankAccount.account_number_masked}
                  </span>
                  {bankStatusMeta && (
                    <span className={statusPillClass(bankStatusMeta.tone)}>
                      {bankStatusMeta.label}
                    </span>
                  )}
                </div>
                <span className={styles.bankMeta}>
                  Chủ tài khoản: <strong>{bankAccount.account_holder_name}</strong> ·{" "}
                  {bankAccount.holder_name_matched
                    ? "Khớp tên chủ shop"
                    : "Khác tên chủ shop lúc được duyệt (chờ quản trị duyệt sau OTP)"}
                </span>
                {bankAccount.verified_at && (
                  <span className={styles.bankMeta}>
                    Xác minh lúc: {formatDateTimeVi(bankAccount.verified_at)}
                  </span>
                )}
                {bankAccount.review_note && (
                  <p
                    className={
                      bankAccount.status === "REJECTED" ? common.fieldError : common.fieldHint
                    }
                  >
                    {bankAccount.status === "REJECTED" ? "Lý do từ chối: " : "Ghi chú duyệt: "}
                    {bankAccount.review_note}
                  </p>
                )}
                {isPayoutLocked && bankAccount.payout_locked_until && (
                  <div className={styles.bankLockNotice} role="status">
                    Tài khoản ngân hàng vừa thay đổi — lệnh giải ngân tạm khóa 24 giờ đến{" "}
                    <strong>{formatDateTimeVi(bankAccount.payout_locked_until)}</strong>.
                  </div>
                )}
              </div>
            ) : (
              <div className={common.inlineEmpty}>
                Chưa liên kết tài khoản ngân hàng nhận giải ngân.
              </div>
            )}

            {bankAccount?.status === "PENDING_OTP" && (
              <form
                className={styles.otpBox}
                onSubmit={(e) => void handleVerifyBankOtp(e)}
                aria-label="Xác thực OTP tài khoản ngân hàng"
              >
                <div className={styles.otpRow}>
                  <div className={`${common.field} ${styles.otpInputWrap}`}>
                    <label htmlFor="bank-otp" className={common.fieldLabel}>
                      Mã OTP 6 chữ số (gửi về email)
                    </label>
                    <input
                      id="bank-otp"
                      type="text"
                      inputMode="numeric"
                      maxLength={6}
                      className={common.textInput}
                      value={otpInput}
                      onChange={(e) => setOtpInput(e.target.value.replace(/\D/g, "").slice(0, 6))}
                      placeholder="123456"
                      required
                    />
                  </div>
                  <Button
                    type="submit"
                    variant="ink"
                    size="sm"
                    shape="app"
                    disabled={otpSubmitting}
                  >
                    {otpSubmitting ? "Đang xác thực..." : "Xác thực OTP"}
                  </Button>
                </div>
                {otpError && (
                  <p className={common.fieldError} role="alert">
                    {otpError}
                  </p>
                )}
              </form>
            )}

            {showBankForm && (
              <form
                className={styles.bankForm}
                onSubmit={(e) => void handleSubmitBankAccount(e)}
                aria-label="Biểu mẫu liên kết tài khoản ngân hàng"
              >
                <div className={styles.bankFormGrid}>
                  <div className={common.field}>
                    <label htmlFor="bank-preset" className={common.fieldLabel}>
                      Ngân hàng
                    </label>
                    <select
                      id="bank-preset"
                      className={common.textInput}
                      value={bankCode}
                      onChange={(e) => {
                        const code = e.target.value;
                        setBankCode(code);
                        const preset = VN_BANK_PRESETS.find((b) => b.code === code);
                        if (preset) setBankName(preset.name);
                      }}
                    >
                      {VN_BANK_PRESETS.map((b) => (
                        <option key={b.code} value={b.code}>
                          {b.code} — {b.name}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="bank-name" className={common.fieldLabel}>
                      Tên ngân hàng
                    </label>
                    <input
                      id="bank-name"
                      type="text"
                      className={common.textInput}
                      value={bankName}
                      onChange={(e) => setBankName(e.target.value)}
                      required
                    />
                  </div>

                  <div className={common.field}>
                    <label htmlFor="bank-number" className={common.fieldLabel}>
                      Số tài khoản (6–19 chữ số)
                    </label>
                    <input
                      id="bank-number"
                      type="text"
                      inputMode="numeric"
                      className={common.textInput}
                      value={accountNumber}
                      onChange={(e) => setAccountNumber(e.target.value.replace(/\D/g, ""))}
                      placeholder="Nhập số tài khoản"
                      required
                    />
                  </div>

                  <div className={common.field}>
                    <label htmlFor="bank-holder" className={common.fieldLabel}>
                      Tên chủ tài khoản (không dấu)
                    </label>
                    <input
                      id="bank-holder"
                      type="text"
                      className={common.textInput}
                      value={accountHolderName}
                      onChange={(e) => setAccountHolderName(e.target.value)}
                      placeholder="NGUYEN VAN A"
                      required
                    />
                  </div>
                </div>

                {bankFormError && (
                  <p className={common.fieldError} role="alert">
                    {bankFormError}
                  </p>
                )}

                <div className={styles.formActions}>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => setShowBankForm(false)}
                  >
                    Hủy
                  </Button>
                  <Button type="submit" variant="ink" size="sm" shape="app" disabled={bankSaving}>
                    {bankSaving ? "Đang gửi OTP..." : "Lưu & gửi mã OTP"}
                  </Button>
                </div>
              </form>
            )}
          </section>

          {/* Real Seller Double-Entry Ledger (/api/seller/ledger) */}
          <section className={common.card} aria-labelledby="seller-ledger-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="seller-ledger-heading" className={common.cardTitle}>
                  Sổ cái giao dịch ({formatInt(ledgerTotal)})
                </h2>
                <p className={styles.reconFormula}>
                  Bút toán ghi kép trên tài khoản tạm giữ (pending) và khả dụng (available)
                </p>
              </div>
              <div className={common.segmented} role="group" aria-label="Lọc tài khoản sổ cái">
                {(
                  [
                    { key: "", label: "Tất cả" },
                    { key: "pending", label: "Đang tạm giữ" },
                    { key: "available", label: "Khả dụng" },
                  ] as const
                ).map((opt) => (
                  <button
                    key={opt.key || "all"}
                    type="button"
                    aria-pressed={ledgerAccountFilter === opt.key}
                    className={`${common.segmentedBtn} ${ledgerAccountFilter === opt.key ? common.segmentedBtnActive : ""}`}
                    onClick={() => {
                      setLedgerAccountFilter(opt.key);
                      setLedgerPage(1);
                    }}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>

            {ledgerLoading ? (
              <StateView
                kind="loading"
                surface="app"
                skeletonVariant="table"
                title="Đang tải sổ cái giao dịch..."
                rows={4}
              />
            ) : ledgerError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải sổ cái giao dịch"
                message={ledgerError}
                actionLabel="Thử lại"
                onAction={() => void loadLedger()}
              />
            ) : ledgerEntries.length === 0 ? (
              <div className={common.inlineEmpty}>
                Chưa có bút toán nào trong tài khoản đã chọn.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Thời gian</th>
                        <th>Tài khoản</th>
                        <th>Tham chiếu</th>
                        <th>Diễn giải</th>
                        <th className={common.numCol}>Ghi nợ</th>
                        <th className={common.numCol}>Ghi có</th>
                        <th className={common.numCol}>Biến động</th>
                      </tr>
                    </thead>
                    <tbody>
                      {ledgerEntries.map((entry) => (
                        <tr key={entry.id}>
                          <td>
                            {entry.created_at ? formatDateTimeVi(entry.created_at) : EMPTY_DASH}
                          </td>
                          <td>
                            <span className={common.pill}>{entry.account_code}</span>
                          </td>
                          <td>
                            <div>{entry.reference_type || EMPTY_DASH}</div>
                            <span className={common.cellSub}>
                              {entry.reference_id || EMPTY_DASH}
                            </span>
                          </td>
                          <td>{entry.description || EMPTY_DASH}</td>
                          <td className={`${common.numCol} ${styles.debitCell}`}>
                            {entry.debit_vnd > 0 ? formatVnd(entry.debit_vnd) : EMPTY_DASH}
                          </td>
                          <td className={`${common.numCol} ${styles.creditCell}`}>
                            {entry.credit_vnd > 0 ? formatVnd(entry.credit_vnd) : EMPTY_DASH}
                          </td>
                          <td
                            className={`${common.numCol} ${entry.delta_vnd >= 0 ? styles.creditCell : styles.debitCell}`}
                          >
                            {formatSignedVnd(entry.delta_vnd)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {ledgerEntries.map((entry) => (
                    <div key={entry.id} className={common.mobileRow}>
                      <div className={common.mobileRowTop}>
                        <div>
                          <strong>{entry.reference_type || entry.account_code}</strong>
                          <span className={common.cellSub}>
                            {entry.created_at ? formatDateTimeVi(entry.created_at) : EMPTY_DASH} ·{" "}
                            {entry.account_code}
                          </span>
                        </div>
                        <span
                          className={`${entry.delta_vnd >= 0 ? styles.creditCell : styles.runningCell} num`}
                        >
                          {formatSignedVnd(entry.delta_vnd)}
                        </span>
                      </div>
                      <div className={common.mobileRowMeta}>
                        <span>{entry.description || entry.reference_id || EMPTY_DASH}</span>
                      </div>
                    </div>
                  ))}
                </div>

                {ledgerTotalPages > 1 && (
                  <div className={styles.paginationBar}>
                    <span className={common.cardMeta}>
                      Trang {formatInt(ledgerPage)} / {formatInt(ledgerTotalPages)}
                    </span>
                    <div className={styles.paginationBtns}>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={ledgerPage <= 1}
                        onClick={() => setLedgerPage((p) => Math.max(1, p - 1))}
                      >
                        Trang trước
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={ledgerPage >= ledgerTotalPages}
                        onClick={() => setLedgerPage((p) => Math.min(ledgerTotalPages, p + 1))}
                      >
                        Trang sau
                      </Button>
                    </div>
                  </div>
                )}
              </>
            )}
          </section>

          {/* Order Revenue Reconciliation */}
          <section className={common.card} aria-labelledby="order-recon-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="order-recon-heading" className={common.cardTitle}>
                  Đối chiếu doanh thu theo đơn hàng
                </h2>
                <p className={styles.reconFormula}>
                  Tiền hàng ({formatVnd(visibleGross)}) {TRUE_MINUS} Chiết khấu &amp; phí (
                  {formatVnd(visibleDeductions)}) = Thực nhận dự kiến{" "}
                  <strong>{formatVnd(visibleNet)}</strong>
                </p>
              </div>

              <div className={common.segmented} role="group" aria-label="Lọc trạng thái đơn hàng">
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
                    aria-pressed={orderFilter === opt.key}
                    className={`${common.segmentedBtn} ${orderFilter === opt.key ? common.segmentedBtnActive : ""}`}
                    onClick={() => setOrderFilter(opt.key)}
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
                        <th>Thanh toán</th>
                        <th className={common.numCol}>Tiền hàng</th>
                        <th className={common.numCol}>Chiết khấu &amp; phí</th>
                        <th className={common.numCol}>Thực nhận</th>
                        <th>Trạng thái</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredOrders.map((o) => {
                        const meta = getSellerOrderStatusMeta(
                          o.status,
                          o.shipment_job_status,
                          o.tracking_code
                        );
                        const channelMeta = getSalesChannelMeta(o.sales_channel);
                        const deductions = computeSellerOrderDeductions(o);
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
                              className={`${common.numCol} ${deductions > 0 ? styles.debitCell : styles.mutedCell}`}
                            >
                              {deductions > 0
                                ? `${TRUE_MINUS}${formatVnd(deductions)}`
                                : EMPTY_DASH}
                            </td>
                            <td className={`${common.numCol} ${styles.runningCell}`}>
                              {formatVnd(net)}
                            </td>
                            <td>
                              <span className={statusPillClass(meta.tone)}>{meta.label}</span>
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
                          {visibleDeductions > 0
                            ? `${TRUE_MINUS}${formatVnd(visibleDeductions)}`
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
                    const meta = getSellerOrderStatusMeta(
                      o.status,
                      o.shipment_job_status,
                      o.tracking_code
                    );
                    const deductions = computeSellerOrderDeductions(o);
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
                          <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          <span className="num">Phí &amp; giảm giá: {formatVnd(deductions)}</span>
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
