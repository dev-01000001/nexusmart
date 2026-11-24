"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type {
  GatewayOrder,
  GatewaySellerBalance,
  GatewaySellerLedgerEntry,
  GatewayShop,
  GatewayShopBankAccount,
  GatewayShopBankAccountResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate } from "@/features/seller/SellerContext";
import {
  BANK_ACCOUNT_STATUS_META,
  PAYMENT_METHOD_TEXT,
  computeOrderDeductionsVnd,
  computeOrderNetVnd,
  getSellerOrderDisplayMeta,
  isTerminalBankOtpError,
  validateBankAccountInput,
} from "@/features/seller/types";
import { EMPTY_DASH, parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type FinanceFilter = "all" | "settled" | "pending";
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
];

function isSettledOrder(status: string): boolean {
  return status === "COMPLETED" || status === "DELIVERED";
}

function isPendingOrder(status: string): boolean {
  return status === "CONFIRMED" || status === "PREPARING" || status === "SHIPPING";
}

function pillClassForTone(tone: "neutral" | "warn" | "pos" | "neg"): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
}

function SellerFinanceInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useMockStore();

  const [balance, setBalance] = useState<GatewaySellerBalance | null>(null);
  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [bankAccount, setBankAccount] = useState<GatewayShopBankAccount | null>(null);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [filter, setFilter] = useState<FinanceFilter>("all");

  // Seller ledger state (GET /api/v1/seller/ledger)
  const [ledgerEntries, setLedgerEntries] = useState<GatewaySellerLedgerEntry[]>([]);
  const [ledgerTotal, setLedgerTotal] = useState(0);
  const [ledgerPage, setLedgerPage] = useState(1);
  const [ledgerAccountFilter, setLedgerAccountFilter] = useState<LedgerAccountFilter>("");
  const [ledgerLoading, setLedgerLoading] = useState(false);
  const [ledgerError, setLedgerError] = useState<string | null>(null);

  // Bank account form & OTP state
  const [showBankForm, setShowBankForm] = useState(false);
  const [bankCode, setBankCode] = useState("VCB");
  const [bankName, setBankName] = useState(VN_BANK_PRESETS[0].name);
  const [accountNumber, setAccountNumber] = useState("");
  const [accountHolderName, setAccountHolderName] = useState("");
  const [bankFormError, setBankFormError] = useState<string | null>(null);
  const [bankSaving, setBankSaving] = useState(false);

  const [otpInput, setOtpInput] = useState("");
  const [otpError, setOtpError] = useState<string | null>(null);
  const [otpSubmitting, setOtpSubmitting] = useState(false);

  const loadFinance = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [balRes, ordersRes, bankRes] = await Promise.all([
        api<GatewaySellerBalance>("seller/balance"),
        api<PaginatedResponse<GatewayOrder>>("seller/orders?page=1&page_size=50"),
        api<GatewayShopBankAccountResponse>("seller/bank-account").catch(() => ({
          bank_account: null,
        })),
      ]);
      setBalance(balRes);
      setOrders(ordersRes.items ?? []);
      setBankAccount(bankRes?.bank_account ?? null);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải số dư và đối soát tài chính lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  const ledgerSeqRef = useRef(0);

  const loadLedger = useCallback(async () => {
    const seq = ++ledgerSeqRef.current;
    setLedgerLoading(true);
    setLedgerError(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", String(ledgerPage));
      qs.set("page_size", "20");
      if (ledgerAccountFilter) {
        qs.set("account_filter", ledgerAccountFilter);
      }
      const res = await api<PaginatedResponse<GatewaySellerLedgerEntry>>(
        `seller/ledger?${qs.toString()}`
      );
      if (ledgerSeqRef.current !== seq) return;
      setLedgerEntries(res.items ?? []);
      setLedgerTotal(res.total ?? (res.items ?? []).length);
    } catch (err) {
      if (ledgerSeqRef.current !== seq) return;
      setLedgerError(getErrorMessage(err, "Không thể tải sổ giao dịch tài chính lúc này."));
    } finally {
      if (ledgerSeqRef.current === seq) {
        setLedgerLoading(false);
      }
    }
  }, [ledgerPage, ledgerAccountFilter]);

  useEffect(() => {
    if (!uiState) {
      void loadFinance();
    }
  }, [uiState, loadFinance]);

  useEffect(() => {
    if (!uiState) {
      void loadLedger();
    }
  }, [uiState, loadLedger]);

  const handleSubmitBankAccount = async (e: React.FormEvent) => {
    e.preventDefault();
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
      const res = await api<GatewayShopBankAccountResponse>("seller/bank-account", {
        method: "POST",
        json: {
          bank_code: bankCode.trim().toUpperCase(),
          bank_name: bankName.trim(),
          account_number: accountNumber.trim(),
          account_holder_name: accountHolderName.trim().toUpperCase(),
        },
      });
      setBankAccount(res?.bank_account ?? null);
      setAccountNumber("");
      setOtpInput("");
      setShowBankForm(false);
      showToast(
        "Đã ghi nhận tài khoản ngân hàng. Vui lòng kiểm tra email để nhập mã OTP xác thực 6 chữ số."
      );
    } catch (err) {
      setBankFormError(getErrorMessage(err, "Không thể đăng ký tài khoản ngân hàng lúc này."));
    } finally {
      setBankSaving(false);
    }
  };

  const handleVerifyBankOtp = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanOtp = otpInput.trim();
    if (!/^\d{6}$/.test(cleanOtp)) {
      setOtpError("Mã OTP phải gồm đúng 6 chữ số được gửi về email tài khoản.");
      return;
    }
    setOtpError(null);
    setOtpSubmitting(true);
    try {
      const res = await api<GatewayShopBankAccountResponse>("seller/bank-account/verify-otp", {
        method: "POST",
        json: { otp: cleanOtp },
      });
      const updated = res?.bank_account ?? null;
      setBankAccount(updated);
      setOtpInput("");
      if (updated?.status === "VERIFIED") {
        showToast("Đã xác thực OTP thành công. Tài khoản ngân hàng đã sẵn sàng nhận giải ngân.");
      } else if (updated?.status === "PENDING_ADMIN_REVIEW") {
        showToast(
          "Đã xác thực OTP. Tên chủ tài khoản khác tên chủ shop lúc được duyệt nên hồ sơ đang chờ quản trị viên duyệt."
        );
      } else {
        showToast("Đã xác thực mã OTP tài khoản ngân hàng.");
      }
    } catch (err) {
      if (isTerminalBankOtpError(err)) {
        const msg = getErrorMessage(
          err,
          "Mã OTP đã hết hạn hoặc nhập sai quá số lần cho phép. Vui lòng nhập lại thông tin tài khoản ngân hàng để nhận mã mới."
        );
        setOtpInput("");
        setOtpError(null);
        setBankAccount((prev) => (prev?.status === "PENDING_OTP" ? null : prev));
        setShowBankForm(true);
        setBankFormError(msg);
        void loadFinance();
      } else {
        setOtpError(getErrorMessage(err, "Mã OTP chưa chính xác. Vui lòng kiểm tra lại hộp thư."));
      }
    } finally {
      setOtpSubmitting(false);
    }
  };

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
  const visibleDeductions = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeOrderDeductionsVnd(o), 0),
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
          <h1 className={common.title}>Tài chính & đối soát</h1>
          <p className={common.finding}>
            {shop.name} · Khả dụng {formatVnd(availableVnd)} · Chờ đối soát {formatVnd(pendingVnd)}{" "}
            · Tổng doanh thu ghi nhận {formatVnd(totalEarnedVnd)}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => void Promise.all([loadFinance(), loadLedger()])}
          >
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
              <span className={common.balanceLabel}>Đang tạm giữ (72 giờ)</span>
              <span
                className={`${common.balanceValue} ${pendingVnd === 0 ? common.balanceValueZero : ""}`}
              >
                {formatVnd(pendingVnd)}
              </span>
              <span className={common.balanceCaption}>
                Tiền đơn đã hoàn tất đang giữ 72 giờ trước khi tự chuyển sang số dư khả dụng
              </span>
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

          {/* Real Bank Account + Email OTP verification */}
          <section className={common.card} aria-labelledby="bank-account-heading">
            <div className={common.cardHeader}>
              <div>
                <h2 id="bank-account-heading" className={common.cardTitle}>
                  Tài khoản ngân hàng nhận tiền
                </h2>
                <span className={common.cardMeta}>
                  Xác thực OTP qua email tài khoản và khóa an toàn giải ngân 24 giờ khi thay đổi
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
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 10,
                    flexWrap: "wrap",
                  }}
                >
                  <span className={styles.bankTitle}>
                    {bankAccount.bank_code} · {bankAccount.bank_name} ·{" "}
                    {bankAccount.account_number_masked}
                  </span>
                  {bankStatusMeta && (
                    <span className={pillClassForTone(bankStatusMeta.tone)}>
                      {bankStatusMeta.label}
                    </span>
                  )}
                </div>
                <span className={styles.bankMeta}>
                  Chủ tài khoản: <strong>{bankAccount.account_holder_name}</strong> ·{" "}
                  {bankAccount.holder_name_matched
                    ? "Khớp tên chủ shop"
                    : "Khác tên chủ shop lúc được duyệt (chờ quản trị viên duyệt sau OTP)"}
                </span>
                {bankAccount.status === "PENDING_ADMIN_REVIEW" && (
                  <p className={common.fieldHint} style={{ marginTop: 4 }}>
                    Hồ sơ đang chờ quản trị viên duyệt do tên chủ tài khoản khác tên chủ shop lúc
                    được duyệt.
                  </p>
                )}
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
                    style={{ marginTop: 4 }}
                  >
                    {bankAccount.status === "REJECTED" ? "Lý do từ chối: " : "Ghi chú duyệt: "}
                    {bankAccount.review_note}
                  </p>
                )}
                {isPayoutLocked && bankAccount.payout_locked_until && (
                  <p className={common.fieldHint} style={{ marginTop: 6, color: "var(--ink)" }}>
                    Bảo vệ chống chiếm đoạt: tài khoản ngân hàng vừa thay đổi, lệnh giải ngân tạm
                    khóa đến <strong>{formatDateTimeVi(bankAccount.payout_locked_until)}</strong>{" "}
                    (24 giờ).
                  </p>
                )}
              </div>
            ) : (
              <div className={common.inlineEmpty}>
                Gian hàng chưa liên kết tài khoản ngân hàng nhận giải ngân. Nhấn Liên kết ngân hàng
                để đăng ký và xác thực OTP qua email.
              </div>
            )}

            {bankAccount?.status === "PENDING_OTP" && (
              <form
                onSubmit={(e) => void handleVerifyBankOtp(e)}
                style={{
                  marginTop: 14,
                  padding: 14,
                  borderRadius: "var(--radius-control)",
                  background: "var(--bg)",
                  border: "1px solid var(--border)",
                }}
              >
                <div className={styles.withdrawGrid}>
                  <div className={common.field}>
                    <label htmlFor="bank-otp" className={common.fieldLabel}>
                      Nhập mã OTP 6 chữ số đã gửi về email
                    </label>
                    <input
                      id="bank-otp"
                      type="text"
                      inputMode="numeric"
                      maxLength={6}
                      className={common.textInput}
                      value={otpInput}
                      onChange={(e) => setOtpInput(e.target.value.replace(/\D/g, "").slice(0, 6))}
                      placeholder="VD: 123456"
                      required
                    />
                  </div>
                  <div className={styles.withdrawActions} style={{ marginTop: 0 }}>
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
                </div>
                {otpError && (
                  <p className={common.fieldError} role="alert" style={{ marginTop: 8 }}>
                    {otpError}
                  </p>
                )}
              </form>
            )}

            {showBankForm && (
              <form
                onSubmit={(e) => void handleSubmitBankAccount(e)}
                style={{
                  marginTop: 14,
                  paddingTop: 14,
                  borderTop: "1px solid var(--rule)",
                }}
              >
                <div className={styles.withdrawGrid}>
                  <div className={common.field}>
                    <label htmlFor="bank-preset" className={common.fieldLabel}>
                      Chọn nhanh ngân hàng
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
                      placeholder="Nhập số tài khoản nhận tiền"
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
                      placeholder="VD: NGUYEN VAN A"
                      required
                    />
                  </div>
                </div>

                {bankFormError && (
                  <p className={common.fieldError} role="alert" style={{ marginTop: 10 }}>
                    {bankFormError}
                  </p>
                )}

                <div className={styles.withdrawActions}>
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

          {/* Real Seller Double-Entry Ledger (GET /api/v1/seller/ledger) */}
          <section className={common.card} aria-labelledby="seller-ledger-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="seller-ledger-heading" className={common.cardTitle}>
                  Sổ giao dịch tài chính ({formatInt(ledgerTotal)})
                </h2>
                <p className={styles.reconFormula}>
                  Bút toán thực tế phát sinh trên tài khoản tạm giữ (pending) và khả dụng
                  (available) của gian hàng
                </p>
              </div>
              <div
                className={common.segmented}
                role="group"
                aria-label="Lọc tài khoản sổ giao dịch"
              >
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
                title="Đang tải sổ giao dịch gian hàng..."
                rows={4}
              />
            ) : ledgerError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải sổ giao dịch"
                message={ledgerError}
                actionLabel="Thử lại"
                onAction={() => void loadLedger()}
              />
            ) : ledgerEntries.length === 0 ? (
              <div className={common.inlineEmpty}>
                Chưa có bút toán nào phát sinh trong tài khoản đã chọn.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Thời gian</th>
                        <th>Tài khoản</th>
                        <th>Loại & tham chiếu</th>
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
                          className={entry.delta_vnd >= 0 ? styles.creditCell : styles.runningCell}
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
                  <div className={styles.withdrawActions}>
                    <span className={common.cardMeta}>
                      Trang {formatInt(ledgerPage)} / {formatInt(ledgerTotalPages)}
                    </span>
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
                )}
              </>
            )}
          </section>

          <section className={common.card} aria-labelledby="ledger-heading">
            <div className={styles.ledgerHead}>
              <div>
                <h2 id="ledger-heading" className={common.cardTitle}>
                  Đối chiếu doanh thu theo đơn hàng
                </h2>
                <p className={styles.reconFormula}>
                  Tiền hàng ({formatVnd(visibleSubtotal)}) {TRUE_MINUS} Chiết khấu &amp; phí sàn (
                  {formatVnd(visibleDeductions)}) = Thực nhận dự kiến{" "}
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
                        const meta = getSellerOrderDisplayMeta(order);
                        const deductions = computeOrderDeductionsVnd(order);
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
                  {filteredOrders.map((order) => {
                    const meta = getSellerOrderDisplayMeta(order);
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
        </>
      )}
    </div>
  );
}

function SellerFinanceContent() {
  return (
    <SellerShopGate pageTitle="Tài chính & đối soát" allowSuspended>
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
