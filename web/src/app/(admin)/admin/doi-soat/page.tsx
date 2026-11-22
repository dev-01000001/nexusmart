"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayLedgerSummary,
  GatewayOrder,
  GatewayReconcileResponse,
  GatewayRefundResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import type { PaymentMethod, PaymentReconciliationRow } from "@/data";
import { ADMIN_RECONCILIATION, computeReconciliationSummary } from "@/data";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import {
  formatDateTimeVi,
  formatDateVi,
  formatInt,
  formatSignedVnd,
  formatVnDayKey,
  formatVnd,
} from "@/lib/format";
import { CHANNEL_META, RECON_STATUS_META } from "@/features/admin/adminMeta";
import { RECONCILE_ITEM_STATUS_META } from "@/features/admin/types";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type DoiSoatTab = "so_cai" | "hoan_tien" | "phien_mau";

function todayDateIso(): string {
  return formatVnDayKey(Date.now());
}

function generateRefundIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `refund-${crypto.randomUUID()}`;
  }
  return `refund-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function ReconciliationContent() {
  const { showToast } = useMockStore();
  const reconciliationRows = ADMIN_RECONCILIATION;

  const [activeTab, setActiveTab] = useState<DoiSoatTab>("so_cai");
  const [ledger, setLedger] = useState<GatewayLedgerSummary | null>(null);
  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Real reconcile run state
  const [reconcileDate, setReconcileDate] = useState(todayDateIso);
  const [reconciling, setReconciling] = useState(false);
  const [reconcileResult, setReconcileResult] = useState<GatewayReconcileResponse | null>(null);

  // Real refund form state (fresh Idempotency-Key each time the form is opened; retries reuse the same key)
  const [refundFormOpen, setRefundFormOpen] = useState(false);
  const [refundIdempotencyKey, setRefundIdempotencyKey] = useState<string>("");
  const [refundPaymentId, setRefundPaymentId] = useState("");
  const [refundOrderId, setRefundOrderId] = useState("");
  const [refundAmountStr, setRefundAmountStr] = useState("");
  const [refundReason, setRefundReason] = useState("");
  const [refundBusy, setRefundBusy] = useState(false);
  const [refundError, setRefundError] = useState<string | null>(null);
  const [refundResult, setRefundResult] = useState<GatewayRefundResponse | null>(null);

  const [mockChannelFilter, setMockChannelFilter] = useState<"all" | PaymentMethod>("all");

  const loadData = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [ledgerRes, ordersRes] = await Promise.all([
        api<GatewayLedgerSummary>("admin/ledger/summary"),
        api<PaginatedResponse<GatewayOrder>>("admin/orders?page=1&page_size=50").catch(() => ({
          items: [],
          page: 1,
          page_size: 50,
          total: 0,
        })),
      ]);
      setLedger(ledgerRes);
      setOrders(ordersRes.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải tổng hợp sổ cái ghi kép lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const handleRunReconcile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!reconcileDate || reconciling) return;
    setReconciling(true);
    try {
      const res = await api<GatewayReconcileResponse>("admin/payments/reconcile", {
        method: "POST",
        json: { date: reconcileDate },
      });
      setReconcileResult(res);
      showToast(`Đã hoàn tất phiên đối soát ngày ${reconcileDate}.`);
      await loadData();
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể chạy đối soát ngày đã chọn."));
    } finally {
      setReconciling(false);
    }
  };

  const handleOpenRefundForm = (presetOrder?: GatewayOrder) => {
    setRefundIdempotencyKey(generateRefundIdempotencyKey());
    setRefundError(null);
    setRefundResult(null);
    if (presetOrder) {
      setRefundPaymentId(presetOrder.payment_id || "");
      setRefundOrderId(presetOrder.id);
      setRefundAmountStr(String(presetOrder.total_payable_vnd));
      setRefundReason("");
    }
    setRefundFormOpen(true);
    setActiveTab("hoan_tien");
  };

  const handleCloseRefundForm = () => {
    setRefundFormOpen(false);
    setRefundError(null);
  };

  const handleSubmitRefund = async (e: React.FormEvent) => {
    e.preventDefault();
    if (refundBusy) return;
    const paymentId = refundPaymentId.trim();
    const orderId = refundOrderId.trim();
    const amountVnd = Math.trunc(Number(refundAmountStr) || 0);
    const reason = refundReason.trim();

    if (!paymentId) {
      setRefundError("Vui lòng nhập mã giao dịch thanh toán cần hoàn.");
      return;
    }
    if (!orderId) {
      setRefundError("Vui lòng nhập mã đơn hàng tương ứng.");
      return;
    }
    if (amountVnd <= 0) {
      setRefundError("Số tiền hoàn phải là số nguyên VND lớn hơn 0.");
      return;
    }
    if (reason.length < 4) {
      setRefundError("Vui lòng nhập lý do hoàn tiền tối thiểu 4 ký tự.");
      return;
    }

    const activeKey = refundIdempotencyKey || generateRefundIdempotencyKey();
    if (!refundIdempotencyKey) {
      setRefundIdempotencyKey(activeKey);
    }

    setRefundError(null);
    setRefundBusy(true);
    try {
      const res = await api<GatewayRefundResponse>(
        `admin/payments/${encodeURIComponent(paymentId)}/refund`,
        {
          method: "POST",
          headers: {
            "Idempotency-Key": activeKey,
          },
          json: {
            order_id: orderId,
            amount_vnd: amountVnd,
            reason,
          },
        }
      );
      setRefundResult(res);
      setRefundIdempotencyKey(generateRefundIdempotencyKey());
      showToast(
        res.already_existed
          ? "Yêu cầu hoàn tiền với khóa hiện tại đã được ghi nhận trước đó."
          : `Đã tạo lệnh hoàn tiền ${formatVnd(amountVnd)} thành công.`
      );
      await loadData();
    } catch (err) {
      setRefundError(getErrorMessage(err, "Không thể thực hiện hoàn tiền lúc này."));
    } finally {
      setRefundBusy(false);
    }
  };

  const mockSummary = useMemo(
    () => computeReconciliationSummary(reconciliationRows),
    [reconciliationRows]
  );

  const filteredMockRows = useMemo<PaymentReconciliationRow[]>(() => {
    if (mockChannelFilter === "all") return reconciliationRows;
    return reconciliationRows.filter((r) => r.channel === mockChannelFilter);
  }, [reconciliationRows, mockChannelFilter]);

  if (loading) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Đối soát thanh toán & sổ cái</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tải tổng hợp sổ cái ghi kép..."
            rows={5}
          />
        </div>
      </div>
    );
  }

  if (errorMsg || !ledger) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Đối soát thanh toán & sổ cái</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải sổ cái thanh toán"
            message={errorMsg ?? "Dịch vụ thanh toán tạm gián đoạn."}
            actionLabel="Thử lại"
            onAction={() => void loadData()}
          />
        </div>
      </div>
    );
  }

  const diffVnd = ledger.total_debits_vnd - ledger.total_credits_vnd;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Đối soát thanh toán & sổ cái</h1>
          <p className={shared.pageSubtitle}>
            Sổ cái ghi kép hiện đang ở trạng thái{" "}
            <strong>{ledger.is_balanced ? "Cân bằng (khớp Nợ = Có)" : "Chưa cân bằng"}</strong> ·{" "}
            {formatInt(ledger.accounts.length)} tài khoản hạch toán bằng số nguyên VND.
          </p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadData()}>
            Làm mới sổ cái
          </Button>
          <Button variant="primary" size="sm" shape="app" onClick={() => handleOpenRefundForm()}>
            Tạo phiếu hoàn tiền
          </Button>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp sổ cái ghi kép"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng Ghi nợ (Debits)</span>
          <span className={shared.kpiValue}>{formatVnd(ledger.total_debits_vnd)}</span>
          <span className={shared.kpiCaption}>
            {formatInt(ledger.accounts.length)} tài khoản sổ cái
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng Ghi có (Credits)</span>
          <span className={shared.kpiValue}>{formatVnd(ledger.total_credits_vnd)}</span>
          <span className={shared.kpiCaption}>Chênh lệch ròng: {formatVnd(diffVnd)}</span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Trạng thái cân đối</span>
          <span
            className={`${shared.kpiValue} ${
              ledger.is_balanced ? shared.kpiValuePositive : shared.kpiValueNegative
            }`}
          >
            {ledger.is_balanced ? "Cân bằng" : "Lệch sổ cái"}
          </span>
          <span className={shared.kpiCaption}>
            {ledger.is_balanced
              ? "Tổng Nợ khớp tuyệt đối Tổng Có"
              : "Cần kiểm tra lại bút toán phát sinh"}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Đơn hàng gần nhất</span>
          <span className={shared.kpiValue}>{formatInt(orders.length)}</span>
          <span className={shared.kpiCaption}>
            {formatInt(orders.filter((o) => Boolean(o.payment_id)).length)} đơn có mã thanh toán
          </span>
        </div>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phân mục đối soát và hoàn tiền">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "so_cai"}
            className={`${shared.tabBtn} ${activeTab === "so_cai" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("so_cai")}
          >
            Sổ cái & Đối soát ngày
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "hoan_tien"}
            className={`${shared.tabBtn} ${activeTab === "hoan_tien" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("hoan_tien")}
          >
            Hoàn tiền giao dịch
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "phien_mau"}
            className={`${shared.tabBtn} ${activeTab === "phien_mau" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("phien_mau")}
          >
            <span>Lịch sử lô tham khảo</span>
            <span className={shared.tabCount}>{reconciliationRows.length}</span>
          </button>
        </div>
      </div>

      {/* TAB 1: Real Ledger Summary + Real Reconcile Trigger */}
      {activeTab === "so_cai" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-labelledby="reconcile-trigger-heading">
            <div className={shared.panelHeader}>
              <h2 id="reconcile-trigger-heading" className={shared.panelTitle}>
                Chạy đối soát thanh toán theo ngày
              </h2>
              <StatusPill
                label={ledger.is_balanced ? "Sổ cái cân bằng" : "Sổ cái có chênh lệch"}
                tone={ledger.is_balanced ? "positive" : "negative"}
              />
            </div>

            <form
              onSubmit={(e) => void handleRunReconcile(e)}
              style={{ display: "flex", alignItems: "flex-end", gap: 12, flexWrap: "wrap" }}
            >
              <div style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 200 }}>
                <label htmlFor="recon-date" className={shared.fieldLabel}>
                  Chọn ngày chốt sao kê
                </label>
                <input
                  id="recon-date"
                  type="date"
                  className={shared.textInput}
                  value={reconcileDate}
                  onChange={(e) => setReconcileDate(e.target.value)}
                  required
                />
              </div>
              <Button type="submit" variant="primary" size="sm" shape="app" disabled={reconciling}>
                {reconciling ? "Đang chạy đối soát..." : "Chạy đối soát"}
              </Button>
            </form>

            {reconcileResult && (
              <div className={shared.detailWell} style={{ marginTop: 16 }}>
                <div className={shared.detailHeader}>
                  <h3 className={shared.detailHeading}>
                    Kết quả phiên đối soát: {reconcileResult.run_id}
                  </h3>
                  <span className={shared.panelMeta}>Ngày {reconcileDate}</span>
                </div>
                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Giao dịch khớp</span>
                    <span className={shared.detailVal}>
                      {formatInt(reconcileResult.matched_count)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Thiếu trên sàn / đối tác</span>
                    <span className={shared.detailVal}>
                      {formatInt(reconcileResult.missing_local_count)} /{" "}
                      {formatInt(reconcileResult.missing_provider_count)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Lệch số tiền</span>
                    <span className={shared.detailVal}>
                      {formatInt(reconcileResult.amount_mismatch_count)}
                    </span>
                  </div>
                </div>

                {(reconcileResult.items ?? []).length > 0 && (
                  <div className={shared.tableWrap}>
                    <table className={shared.dataTable}>
                      <thead>
                        <tr>
                          <th>Mã thanh toán</th>
                          <th>Mã đối tác</th>
                          <th className={shared.colNum}>Trên sàn</th>
                          <th className={shared.colNum}>Tại đối tác</th>
                          <th>Kết quả</th>
                          <th>Ghi chú</th>
                        </tr>
                      </thead>
                      <tbody>
                        {reconcileResult.items.map((item, idx) => {
                          const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                            label: item.status,
                            tone: "neutral" as const,
                          };
                          return (
                            <tr key={`${item.payment_id}-${idx}`}>
                              <td>{item.payment_id || "—"}</td>
                              <td>{item.provider_tx_id || "—"}</td>
                              <td className={shared.colNum}>{formatVnd(item.local_amount_vnd)}</td>
                              <td className={shared.colNum}>
                                {formatVnd(item.provider_amount_vnd)}
                              </td>
                              <td>
                                <StatusPill label={meta.label} tone={meta.tone} />
                              </td>
                              <td>{item.note || "—"}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            )}
          </section>

          <section className={shared.panel} aria-labelledby="ledger-accounts-heading">
            <div className={shared.panelHeader}>
              <h2 id="ledger-accounts-heading" className={shared.panelTitle}>
                Chi tiết tài khoản sổ cái ghi kép
              </h2>
              <span className={shared.panelMeta}>
                Tổng Nợ {formatVnd(ledger.total_debits_vnd)} · Tổng Có{" "}
                {formatVnd(ledger.total_credits_vnd)}
              </span>
            </div>

            {ledger.accounts.length === 0 ? (
              <p className={shared.emptyRow}>Chưa có tài khoản hạch toán nào được khởi tạo.</p>
            ) : (
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã tài khoản</th>
                      <th>Nhóm tài khoản</th>
                      <th className={shared.colNum}>Tổng Ghi nợ</th>
                      <th className={shared.colNum}>Tổng Ghi có</th>
                      <th className={shared.colNum}>Số dư hiện tại</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ledger.accounts.map((acc) => (
                      <tr key={acc.account_code}>
                        <td>
                          <span className={shared.cellPrimary}>{acc.account_code}</span>
                        </td>
                        <td>{acc.account_type}</td>
                        <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                        <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                        <td className={shared.colNum}>
                          <strong>{formatVnd(acc.balance_vnd)}</strong>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </div>
      )}

      {/* TAB 2: Real Refund Flow */}
      {activeTab === "hoan_tien" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-labelledby="refund-panel-heading">
            <div className={shared.panelHeader}>
              <h2 id="refund-panel-heading" className={shared.panelTitle}>
                Phiếu hoàn tiền giao dịch thanh toán
              </h2>
              <Button
                variant={refundFormOpen ? "outline" : "primary"}
                size="sm"
                shape="app"
                onClick={() => (refundFormOpen ? handleCloseRefundForm() : handleOpenRefundForm())}
              >
                {refundFormOpen ? "Đóng phiếu" : "Mở phiếu hoàn tiền mới"}
              </Button>
            </div>

            {!refundFormOpen ? (
              <p className={shared.pageSubtitle}>
                Mỗi lần mở phiếu mới, hệ thống tự tạo một mã chống trùng lặp riêng cho phiên hoàn
                tiền đó; nếu bấm gửi lại trong cùng phiếu sẽ không bị hoàn lặp hai lần. Chọn một đơn
                bên dưới hoặc nhấn &ldquo;Mở phiếu hoàn tiền mới&rdquo;.
              </p>
            ) : (
              <form className={shared.detailWell} onSubmit={(e) => void handleSubmitRefund(e)}>
                <div className={shared.detailHeader}>
                  <h3 className={shared.detailHeading}>Thông tin lệnh hoàn tiền</h3>
                  <span className={shared.panelMeta}>
                    Mã chống trùng lặp: {refundIdempotencyKey}
                  </span>
                </div>

                {refundError && <p className={shared.validationError}>{refundError}</p>}

                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <label htmlFor="rf-pay-id" className={shared.fieldLabel}>
                      Mã giao dịch thanh toán
                    </label>
                    <input
                      id="rf-pay-id"
                      type="text"
                      className={shared.textInput}
                      value={refundPaymentId}
                      onChange={(e) => setRefundPaymentId(e.target.value)}
                      placeholder="Nhập payment_id cần hoàn..."
                      required
                    />
                  </div>
                  <div className={shared.detailField}>
                    <label htmlFor="rf-ord-id" className={shared.fieldLabel}>
                      Mã đơn hàng
                    </label>
                    <input
                      id="rf-ord-id"
                      type="text"
                      className={shared.textInput}
                      value={refundOrderId}
                      onChange={(e) => setRefundOrderId(e.target.value)}
                      placeholder="Nhập order_id tương ứng..."
                      required
                    />
                  </div>
                  <div className={shared.detailField}>
                    <label htmlFor="rf-amount" className={shared.fieldLabel}>
                      Số tiền hoàn (số nguyên VND)
                    </label>
                    <input
                      id="rf-amount"
                      type="number"
                      min={1000}
                      step={1000}
                      className={shared.textInput}
                      value={refundAmountStr}
                      onChange={(e) => setRefundAmountStr(e.target.value)}
                      required
                    />
                  </div>
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <label htmlFor="rf-reason" className={shared.fieldLabel}>
                      Lý do hoàn tiền
                    </label>
                    <input
                      id="rf-reason"
                      type="text"
                      className={shared.textInput}
                      value={refundReason}
                      onChange={(e) => setRefundReason(e.target.value)}
                      placeholder="Ví dụ: Hoàn tiền đơn hủy theo yêu cầu khách hàng..."
                      required
                    />
                  </div>
                </div>

                <div className={shared.detailActionBar}>
                  <span className={shared.savedStamp}>
                    Khóa chống trùng lặp được giữ khi thử lại lỗi và tự tạo mới sau khi hoàn tiền
                    thành công.
                  </span>
                  <div className={shared.detailButtons}>
                    <Button variant="outline" size="sm" shape="app" onClick={handleCloseRefundForm}>
                      Đóng
                    </Button>
                    <Button
                      type="submit"
                      variant="primary"
                      size="sm"
                      shape="app"
                      disabled={refundBusy}
                    >
                      {refundBusy ? "Đang gửi lệnh hoàn..." : "Xác nhận hoàn tiền"}
                    </Button>
                  </div>
                </div>

                {refundResult && (
                  <div style={{ paddingTop: 10 }}>
                    <StatusPill
                      label={
                        refundResult.already_existed
                          ? `Đã ghi nhận trước đó (${refundResult.refund.status})`
                          : `Hoàn tiền thành công (${refundResult.refund.status})`
                      }
                      tone="positive"
                    />
                    <span className={shared.cellSub} style={{ marginLeft: 10 }}>
                      Mã hoàn: {refundResult.refund.refund_id} · Số tiền:{" "}
                      {formatVnd(refundResult.refund.amount_vnd)}
                    </span>
                  </div>
                )}
              </form>
            )}
          </section>

          <section className={shared.panel} aria-labelledby="refund-orders-heading">
            <div className={shared.panelHeader}>
              <h2 id="refund-orders-heading" className={shared.panelTitle}>
                Đơn hàng gần nhất trên sàn (chọn nhanh điền phiếu hoàn)
              </h2>
              <span className={shared.panelMeta}>{formatInt(orders.length)} đơn gần nhất</span>
            </div>

            {orders.length === 0 ? (
              <p className={shared.emptyRow}>Chưa có đơn hàng nào trên hệ thống.</p>
            ) : (
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã đơn</th>
                      <th>Phương thức & mã thanh toán</th>
                      <th className={shared.colNum}>Tổng thanh toán</th>
                      <th>Trạng thái</th>
                      <th className={shared.colAction}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {orders.slice(0, 20).map((ord) => (
                      <tr key={ord.id}>
                        <td>
                          <div className={shared.cellPrimary}>{ord.order_code}</div>
                          <div className={shared.cellSub}>
                            {ord.created_at ? formatDateTimeVi(ord.created_at) : ord.id}
                          </div>
                        </td>
                        <td>
                          <div>{ord.payment_method}</div>
                          <div className={shared.cellSub}>
                            {ord.payment_id || "Chưa có mã giao dịch thanh toán"}
                          </div>
                        </td>
                        <td className={shared.colNum}>{formatVnd(ord.total_payable_vnd)}</td>
                        <td>
                          <StatusPill label={ord.status} tone="neutral" />
                        </td>
                        <td className={shared.colAction}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={() => handleOpenRefundForm(ord)}
                          >
                            Điền phiếu hoàn
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </div>
      )}

      {/* TAB 3: Historical Mock Batches (kept separated) */}
      {activeTab === "phien_mau" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-label="Lịch sử phiên đối soát tham khảo">
            <div className={shared.panelHeader}>
              <div>
                <h2 className={shared.panelTitle}>Lịch sử phiên đối soát mẫu (tham khảo)</h2>
                <span className={shared.panelMeta}>
                  Dữ liệu minh họa phân bổ theo cổng · Tổng kỳ vọng{" "}
                  {formatVnd(mockSummary.totalGross)}
                </span>
              </div>
              <select
                className={shared.toolbarSelect}
                value={mockChannelFilter}
                onChange={(e) => setMockChannelFilter(e.target.value as "all" | PaymentMethod)}
                aria-label="Lọc cổng thanh toán mẫu"
              >
                <option value="all">Tất cả cổng</option>
                <option value="cod">COD thu hộ</option>
                <option value="vietqr">VietQR</option>
                <option value="vi_sandbox">Ví điện tử</option>
              </select>
            </div>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Mã phiên & ngày</th>
                    <th>Cổng thanh toán</th>
                    <th className={shared.colNum}>Kỳ vọng</th>
                    <th className={shared.colNum}>Chênh lệch</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredMockRows.map((row) => {
                    const meta = RECON_STATUS_META[row.status];
                    return (
                      <tr key={row.id}>
                        <td>
                          <div className={shared.cellPrimary}>{row.batchCode}</div>
                          <div className={shared.cellSub}>
                            {formatDateVi(row.date)} · {formatInt(row.txCount)} giao dịch
                          </div>
                        </td>
                        <td>{CHANNEL_META[row.channel]}</td>
                        <td className={shared.colNum}>{formatVnd(row.grossCollected)}</td>
                        <td
                          className={`${shared.colNum} ${
                            row.discrepancy > 0 ? shared.numNegative : shared.numMuted
                          }`}
                        >
                          {row.discrepancy > 0 ? formatSignedVnd(-row.discrepancy) : "0₫"}
                        </td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

function ReconciliationRoute() {
  return (
    <AdminStateGate
      title="Đối soát thanh toán & sổ cái"
      subtitle="Theo dõi cân đối sổ cái ghi kép, chạy đối soát theo ngày và xử lý hoàn tiền."
      emptyTitle="Chưa có phiên đối soát trong kỳ chọn"
      emptyMessage="Các bản ghi sổ cái sẽ xuất hiện tại đây khi có giao dịch thanh toán."
      errorTitle="Không thể tải sổ đối soát thanh toán"
      errorMessage="Kết nối tới dịch vụ sổ cái thanh toán tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {() => <ReconciliationContent />}
    </AdminStateGate>
  );
}

export default function AdminReconciliationPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Đối soát thanh toán & sổ cái" />}>
      <ReconciliationRoute />
    </Suspense>
  );
}
