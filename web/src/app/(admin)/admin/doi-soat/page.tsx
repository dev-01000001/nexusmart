"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayLedgerSummary,
  GatewayOrder,
  GatewayReconcileResponse,
  GatewayReconciliationAction,
  GatewayReconciliationQueueItem,
  GatewayRefund,
  GatewayRefundResponse,
  GatewayResolveReconciliationResponse,
  GatewaySettleRefundResponse,
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
import {
  RECONCILE_ITEM_STATUS_META,
  getAdminOrderStatusMeta,
  getAdminRefundStatusMeta,
  getReconciliationQueueStatusMeta,
} from "@/features/admin/types";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type DoiSoatTab = "so_cai" | "hang_doi" | "hoan_tien" | "can_xu_ly" | "phien_mau";

function todayDateIso(): string {
  return formatVnDayKey(Date.now());
}

function generateIdempotencyKey(prefix: string): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function isQueueItemActionable(status: string): boolean {
  const s = (status ?? "").trim().toLowerCase();
  return s === "pending" || s === "suspended";
}

function isRefundPendingStatus(status: string): boolean {
  const s = (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^REFUND_STATUS_/, "");
  return s === "PENDING";
}

function ReconciliationContent() {
  const { showToast } = useMockStore();
  const reconciliationRows = ADMIN_RECONCILIATION;

  const [activeTab, setActiveTab] = useState<DoiSoatTab>("so_cai");
  const [ledger, setLedger] = useState<GatewayLedgerSummary | null>(null);
  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Real reconcile run state (Vietnam date default)
  const [reconcileDate, setReconcileDate] = useState(todayDateIso);
  const [reconciling, setReconciling] = useState(false);
  const [reconcileError, setReconcileError] = useState<string | null>(null);
  const [reconcileResult, setReconcileResult] = useState<GatewayReconcileResponse | null>(null);

  // Mismatched reconciliation queue state
  const [queueItems, setQueueItems] = useState<GatewayReconciliationQueueItem[]>([]);
  const [queueTotal, setQueueTotal] = useState(0);
  const [queuePendingTotal, setQueuePendingTotal] = useState(0);
  const [queueStatusFilter, setQueueStatusFilter] = useState("");
  const [queueLoading, setQueueLoading] = useState(false);
  const [queueError, setQueueError] = useState<string | null>(null);
  const [selectedQueueId, setSelectedQueueId] = useState<string | null>(null);
  const [resolveAction, setResolveAction] = useState<GatewayReconciliationAction>("ASSIGN");
  const [resolvePaymentId, setResolvePaymentId] = useState("");
  const [resolveNote, setResolveNote] = useState("");
  const [resolveIdempotencyKey, setResolveIdempotencyKey] = useState("");
  const [resolvingQueue, setResolvingQueue] = useState(false);
  const [resolveQueueError, setResolveQueueError] = useState<string | null>(null);

  // Pending refunds settlement queue state
  const [refundsList, setRefundsList] = useState<GatewayRefund[]>([]);
  const [refundsTotal, setRefundsTotal] = useState(0);
  const [pendingRefundsCount, setPendingRefundsCount] = useState(0);
  const [refundStatusFilter, setRefundStatusFilter] = useState("pending");
  const [refundsLoading, setRefundsLoading] = useState(false);
  const [refundsError, setRefundsError] = useState<string | null>(null);
  const [settlingRefundId, setSettlingRefundId] = useState<string | null>(null);
  const [settleProviderRef, setSettleProviderRef] = useState("");
  const [settleNote, setSettleNote] = useState("");
  const [settleIdempotencyKey, setSettleIdempotencyKey] = useState("");
  const [settleBusy, setSettleBusy] = useState(false);
  const [settleError, setSettleError] = useState<string | null>(null);

  // Manual refund form state
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

  // Orphan / manual-review orders (GET /api/v1/admin/orders/needs-review)
  const [needsReviewOrders, setNeedsReviewOrders] = useState<GatewayOrder[]>([]);
  const [needsReviewTotal, setNeedsReviewTotal] = useState(0);
  const [needsReviewLoading, setNeedsReviewLoading] = useState(false);
  const [needsReviewError, setNeedsReviewError] = useState<string | null>(null);
  const [reviewNoteDrafts, setReviewNoteDrafts] = useState<Record<string, string>>({});
  const [resolvingOrderId, setResolvingOrderId] = useState<string | null>(null);

  const loadNeedsReviewOrders = useCallback(async () => {
    setNeedsReviewLoading(true);
    setNeedsReviewError(null);
    try {
      const res = await api<PaginatedResponse<GatewayOrder>>(
        "admin/orders/needs-review?page=1&page_size=50"
      );
      const items = res.items ?? [];
      setNeedsReviewOrders(items);
      setNeedsReviewTotal(res.total ?? items.length);
    } catch (err) {
      setNeedsReviewError(
        getErrorMessage(err, "Không thể tải danh sách đơn cần xử lý tay lúc này.")
      );
    } finally {
      setNeedsReviewLoading(false);
    }
  }, []);

  const loadQueue = useCallback(async () => {
    setQueueLoading(true);
    setQueueError(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", "1");
      qs.set("page_size", "50");
      if (queueStatusFilter) qs.set("status", queueStatusFilter);

      const [listRes, openRes] = await Promise.all([
        api<PaginatedResponse<GatewayReconciliationQueueItem>>(
          `admin/reconciliation/queue?${qs.toString()}`
        ),
        api<PaginatedResponse<GatewayReconciliationQueueItem>>(
          "admin/reconciliation/queue?status=pending&page=1&page_size=1"
        ).catch(() => null),
      ]);
      const items = listRes.items ?? [];
      setQueueItems(items);
      setQueueTotal(listRes.total ?? items.length);
      if (openRes) {
        setQueuePendingTotal(openRes.total ?? openRes.items.length);
      }
    } catch (err) {
      setQueueError(getErrorMessage(err, "Không thể tải hàng đợi giao dịch không khớp lúc này."));
    } finally {
      setQueueLoading(false);
    }
  }, [queueStatusFilter]);

  const loadRefundsQueue = useCallback(async () => {
    setRefundsLoading(true);
    setRefundsError(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", "1");
      qs.set("page_size", "50");
      if (refundStatusFilter) qs.set("status", refundStatusFilter);

      const [listRes, pendRes] = await Promise.all([
        api<PaginatedResponse<GatewayRefund>>(`admin/refunds?${qs.toString()}`),
        api<PaginatedResponse<GatewayRefund>>(
          "admin/refunds?status=pending&page=1&page_size=1"
        ).catch(() => null),
      ]);
      const items = listRes.items ?? [];
      setRefundsList(items);
      setRefundsTotal(listRes.total ?? items.length);
      if (pendRes) {
        setPendingRefundsCount(pendRes.total ?? pendRes.items.length);
      }
    } catch (err) {
      setRefundsError(
        getErrorMessage(err, "Không thể tải danh sách khoản hoàn tiền chờ tất toán lúc này.")
      );
    } finally {
      setRefundsLoading(false);
    }
  }, [refundStatusFilter]);

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

  useEffect(() => {
    void loadQueue();
  }, [loadQueue]);

  useEffect(() => {
    void loadRefundsQueue();
  }, [loadRefundsQueue]);

  useEffect(() => {
    void loadNeedsReviewOrders();
  }, [loadNeedsReviewOrders]);

  const handleResolveManualOrder = async (order: GatewayOrder) => {
    const note = (reviewNoteDrafts[order.id] ?? "").trim();
    if (note.length < 4) {
      showToast("Vui lòng nhập ghi chú xử lý tay (tối thiểu 4 ký tự).");
      return;
    }
    setResolvingOrderId(order.id);
    try {
      await api<{ order: GatewayOrder }>(
        `admin/orders/${encodeURIComponent(order.id)}/resolve-review`,
        {
          method: "POST",
          json: { note },
        }
      );
      setReviewNoteDrafts((prev) => ({ ...prev, [order.id]: "" }));
      showToast(`Đã đánh dấu hoàn tất xử lý tay cho đơn ${order.order_code}.`);
      await Promise.all([loadNeedsReviewOrders(), loadData()]);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật trạng thái xử lý tay cho đơn này."));
    } finally {
      setResolvingOrderId(null);
    }
  };

  const handleRunReconcile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!reconcileDate || reconciling) return;
    setReconcileError(null);
    setReconciling(true);
    try {
      const res = await api<GatewayReconcileResponse>("admin/payments/reconcile", {
        method: "POST",
        json: { date: reconcileDate },
      });
      setReconcileResult(res);
      showToast(`Đã hoàn tất phiên đối soát ngày ${reconcileDate}.`);
      await Promise.all([loadData(), loadQueue()]);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể chạy đối soát ngày đã chọn.");
      setReconcileError(msg);
      showToast(msg);
    } finally {
      setReconciling(false);
    }
  };

  const handleSelectQueueItem = (item: GatewayReconciliationQueueItem) => {
    if (selectedQueueId === item.id) {
      setSelectedQueueId(null);
      setResolveQueueError(null);
      return;
    }
    setSelectedQueueId(item.id);
    setResolveAction("ASSIGN");
    setResolvePaymentId(item.expected_payment_id || "");
    setResolveNote(item.notes || "");
    setResolveIdempotencyKey(generateIdempotencyKey("recon-resolve"));
    setResolveQueueError(null);
  };

  const handleResolveQueueSubmit = async (
    e: React.FormEvent,
    item: GatewayReconciliationQueueItem
  ) => {
    e.preventDefault();
    if (resolvingQueue) return;

    const cleanPaymentId = resolvePaymentId.trim();
    const cleanNote = resolveNote.trim();

    if ((resolveAction === "ASSIGN" || resolveAction === "REFUND") && !cleanPaymentId) {
      setResolveQueueError("Vui lòng nhập mã giao dịch thanh toán (payment_id) cần gắn xử lý.");
      return;
    }
    if (resolveAction === "SUSPEND" && cleanNote.length < 4) {
      setResolveQueueError("Vui lòng ghi chú lý do tạm treo (tối thiểu 4 ký tự).");
      return;
    }

    const activeKey = resolveIdempotencyKey || generateIdempotencyKey("recon-resolve");
    if (!resolveIdempotencyKey) setResolveIdempotencyKey(activeKey);

    setResolveQueueError(null);
    setResolvingQueue(true);
    try {
      const res = await api<GatewayResolveReconciliationResponse>(
        `admin/reconciliation/queue/${encodeURIComponent(item.id)}/resolve`,
        {
          method: "POST",
          headers: { "Idempotency-Key": activeKey },
          json: {
            action: resolveAction,
            payment_id: cleanPaymentId,
            note: cleanNote,
          },
        }
      );
      setResolveIdempotencyKey(generateIdempotencyKey("recon-resolve"));
      setSelectedQueueId(null);
      showToast(
        res.already_resolved
          ? "Bản ghi đối soát này đã được xử lý trước đó."
          : "Đã xử lý giao dịch đối soát thành công."
      );
      await Promise.all([loadQueue(), loadRefundsQueue(), loadData()]);
    } catch (err) {
      setResolveQueueError(getErrorMessage(err, "Không thể xử lý giao dịch đối soát này lúc này."));
    } finally {
      setResolvingQueue(false);
    }
  };

  const handleOpenSettle = (rf: GatewayRefund) => {
    if (settlingRefundId === rf.refund_id) {
      setSettlingRefundId(null);
      setSettleError(null);
      return;
    }
    setSettlingRefundId(rf.refund_id);
    setSettleProviderRef(rf.provider_refund_id || "");
    setSettleNote(rf.settlement_note || "");
    setSettleIdempotencyKey(generateIdempotencyKey("settle-refund"));
    setSettleError(null);
  };

  const handleSubmitSettle = async (e: React.FormEvent, rf: GatewayRefund) => {
    e.preventDefault();
    if (settleBusy) return;
    const cleanRef = settleProviderRef.trim();
    const cleanNote = settleNote.trim();
    if (cleanRef.length < 3) {
      setSettleError("Vui lòng nhập mã giao dịch chuyển khoản ngân hàng (tối thiểu 3 ký tự).");
      return;
    }

    const activeKey = settleIdempotencyKey || generateIdempotencyKey("settle-refund");
    if (!settleIdempotencyKey) setSettleIdempotencyKey(activeKey);

    setSettleError(null);
    setSettleBusy(true);
    try {
      const res = await api<GatewaySettleRefundResponse>(
        `admin/refunds/${encodeURIComponent(rf.refund_id)}/settle`,
        {
          method: "POST",
          headers: { "Idempotency-Key": activeKey },
          json: {
            provider_refund_id: cleanRef,
            note: cleanNote,
          },
        }
      );
      setSettleIdempotencyKey(generateIdempotencyKey("settle-refund"));
      setSettlingRefundId(null);
      showToast(
        res.already_settled
          ? "Khoản hoàn tiền này đã được tất toán trước đó."
          : `Đã tất toán chuyển khoản hoàn tiền ${formatVnd(rf.amount_vnd)}.`
      );
      await Promise.all([loadRefundsQueue(), loadData()]);
    } catch (err) {
      setSettleError(getErrorMessage(err, "Không thể tất toán khoản hoàn tiền lúc này."));
    } finally {
      setSettleBusy(false);
    }
  };

  const handleOpenRefundForm = (presetOrder?: GatewayOrder) => {
    setRefundIdempotencyKey(generateIdempotencyKey("refund"));
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

    const activeKey = refundIdempotencyKey || generateIdempotencyKey("refund");
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
      setRefundIdempotencyKey(generateIdempotencyKey("refund"));
      showToast(
        res.already_existed
          ? "Yêu cầu hoàn tiền với khóa hiện tại đã được ghi nhận trước đó."
          : `Đã tạo lệnh hoàn tiền ${formatVnd(amountVnd)} thành công.`
      );
      await Promise.all([loadData(), loadRefundsQueue()]);
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

  const selectedQueueItem = useMemo(
    () => queueItems.find((item) => item.id === selectedQueueId) ?? null,
    [queueItems, selectedQueueId]
  );

  const settlingRefund = useMemo(
    () => refundsList.find((rf) => rf.refund_id === settlingRefundId) ?? null,
    [refundsList, settlingRefundId]
  );

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
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() =>
              void Promise.all([
                loadData(),
                loadQueue(),
                loadRefundsQueue(),
                loadNeedsReviewOrders(),
              ])
            }
          >
            Làm mới dữ liệu
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
          <span className={shared.kpiLabel}>Hàng đợi cần xử lý</span>
          <span
            className={`${shared.kpiValue} ${
              queuePendingTotal + pendingRefundsCount + needsReviewTotal > 0
                ? shared.kpiValueNegative
                : ""
            }`}
          >
            {formatInt(queuePendingTotal + pendingRefundsCount + needsReviewTotal)}
          </span>
          <span className={shared.kpiCaption}>
            {formatInt(queuePendingTotal)} lệch khớp · {formatInt(pendingRefundsCount)} chờ hoàn ·{" "}
            {formatInt(needsReviewTotal)} xử lý tay
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
            aria-selected={activeTab === "hang_doi"}
            className={`${shared.tabBtn} ${activeTab === "hang_doi" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("hang_doi")}
          >
            <span>Giao dịch không khớp</span>
            {queuePendingTotal > 0 && <span className={shared.tabCount}>{queuePendingTotal}</span>}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "hoan_tien"}
            className={`${shared.tabBtn} ${activeTab === "hoan_tien" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("hoan_tien")}
          >
            <span>Hoàn tiền & Tất toán</span>
            {pendingRefundsCount > 0 && (
              <span className={shared.tabCount}>{pendingRefundsCount}</span>
            )}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "can_xu_ly"}
            className={`${shared.tabBtn} ${activeTab === "can_xu_ly" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("can_xu_ly")}
          >
            <span>Đơn cần xử lý tay</span>
            {needsReviewTotal > 0 && <span className={shared.tabCount}>{needsReviewTotal}</span>}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "phien_mau"}
            className={`${shared.tabBtn} ${activeTab === "phien_mau" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("phien_mau")}
          >
            <span>Lịch sử lô (mẫu)</span>
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
                Chạy đối soát thanh toán theo ngày (giờ Việt Nam UTC+7)
              </h2>
              <StatusPill
                label={ledger.is_balanced ? "Sổ cái cân bằng" : "Sổ cái có chênh lệch"}
                tone={ledger.is_balanced ? "positive" : "negative"}
              />
            </div>

            <form onSubmit={(e) => void handleRunReconcile(e)} className={styles.inlineFormRow}>
              <div className={styles.dateField}>
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

            {reconcileError && (
              <p className={styles.alertError} role="alert">
                {reconcileError}
              </p>
            )}

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
                  <>
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
                                <td className={shared.colNum}>
                                  {formatVnd(item.local_amount_vnd)}
                                </td>
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

                    <div className={shared.mobileList}>
                      {reconcileResult.items.map((item, idx) => {
                        const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                          label: item.status,
                          tone: "neutral" as const,
                        };
                        return (
                          <div
                            key={`m-rec-${item.payment_id}-${idx}`}
                            className={shared.mobileItem}
                          >
                            <div className={shared.mobileLine1}>
                              <span className={shared.mobileIdentity}>
                                {item.payment_id || item.provider_tx_id || "—"}
                              </span>
                              <span className={shared.mobileMetric}>
                                {formatVnd(item.local_amount_vnd)}
                              </span>
                            </div>
                            <div className={shared.mobileLine2}>
                              <span className={shared.mobileMetaText}>
                                Đối tác: {formatVnd(item.provider_amount_vnd)}{" "}
                                {item.note ? `· ${item.note}` : ""}
                              </span>
                              <StatusPill label={meta.label} tone={meta.tone} />
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </>
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
              <>
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

                <div className={shared.mobileList}>
                  {ledger.accounts.map((acc) => (
                    <div key={`m-acc-${acc.account_code}`} className={shared.mobileItem}>
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{acc.account_code}</span>
                        <span className={shared.mobileMetric}>{formatVnd(acc.balance_vnd)}</span>
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>{acc.account_type}</span>
                        <span className={shared.mobileMetaText}>
                          Nợ {formatVnd(acc.debit_total_vnd)} · Có {formatVnd(acc.credit_total_vnd)}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </>
            )}
          </section>
        </div>
      )}

      {/* TAB 2: Mismatched Reconciliation Queue */}
      {activeTab === "hang_doi" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-labelledby="recon-queue-heading">
            <div className={shared.panelHeader}>
              <div>
                <h2 id="recon-queue-heading" className={shared.panelTitle}>
                  Hàng đợi giao dịch không khớp ({formatInt(queueTotal)})
                </h2>
                <span className={shared.panelMeta}>
                  Xử lý webhook chuyển khoản lệch số tiền hoặc sai mã tham chiếu bằng Gán khớp, Hoàn
                  tiền hoặc Tạm treo.
                </span>
              </div>
              <div className={styles.filterGroup}>
                <select
                  className={shared.toolbarSelect}
                  value={queueStatusFilter}
                  onChange={(e) => setQueueStatusFilter(e.target.value)}
                  aria-label="Lọc trạng thái giao dịch không khớp"
                >
                  <option value="">Tất cả trạng thái</option>
                  <option value="pending">Chờ xử lý</option>
                  <option value="suspended">Đang tạm treo</option>
                  <option value="assigned">Đã gán khớp</option>
                  <option value="refunded">Đã tạo hoàn tiền</option>
                </select>
                <Button variant="outline" size="sm" shape="app" onClick={() => void loadQueue()}>
                  Làm mới
                </Button>
              </div>
            </div>

            {queueLoading ? (
              <StateView
                kind="loading"
                surface="app"
                title="Đang tải hàng đợi giao dịch không khớp..."
                rows={4}
              />
            ) : queueError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải hàng đợi giao dịch không khớp"
                message={queueError}
                actionLabel="Thử lại"
                onAction={() => void loadQueue()}
              />
            ) : queueItems.length === 0 ? (
              <p className={shared.emptyRow}>
                Không có giao dịch không khớp nào trong trạng thái đã chọn.
              </p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã GD đối tác & đơn tham chiếu</th>
                        <th className={shared.colNum}>Kỳ vọng</th>
                        <th className={shared.colNum}>Thực nhận</th>
                        <th>Lý do lệch</th>
                        <th>Trạng thái</th>
                        <th className={shared.colAction}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {queueItems.map((item) => {
                        const stMeta = getReconciliationQueueStatusMeta(item.resolution_status);
                        const isSelected = selectedQueueId === item.id;
                        const actionable = isQueueItemActionable(item.resolution_status);
                        const diff = item.actual_amount_vnd - item.expected_amount_vnd;
                        return (
                          <tr
                            key={item.id}
                            className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                            onClick={() => handleSelectQueueItem(item)}
                          >
                            <td>
                              <div className={shared.cellPrimary}>
                                {item.provider_tx_id || item.id.slice(0, 8)}
                              </div>
                              <div className={shared.cellSub}>
                                {item.provider} ·{" "}
                                {item.order_ref || item.expected_payment_id || "—"}
                              </div>
                            </td>
                            <td className={shared.colNum}>{formatVnd(item.expected_amount_vnd)}</td>
                            <td
                              className={`${shared.colNum} ${
                                diff !== 0 ? shared.numNegative : shared.numPositive
                              }`}
                            >
                              {formatVnd(item.actual_amount_vnd)}
                            </td>
                            <td>
                              <div>{item.reason || "—"}</div>
                              {item.notes && <div className={shared.cellSub}>{item.notes}</div>}
                            </td>
                            <td>
                              <StatusPill label={stMeta.label} tone={stMeta.tone} />
                            </td>
                            <td className={shared.colAction}>
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  handleSelectQueueItem(item);
                                }}
                              >
                                {isSelected ? "Đóng" : actionable ? "Xử lý" : "Xem"}
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {queueItems.map((item) => {
                    const stMeta = getReconciliationQueueStatusMeta(item.resolution_status);
                    const isSelected = selectedQueueId === item.id;
                    return (
                      <div key={item.id} className={shared.mobileItem}>
                        <button
                          type="button"
                          className={shared.mobileItemBtn}
                          onClick={() => handleSelectQueueItem(item)}
                          aria-expanded={isSelected}
                        >
                          <div className={shared.mobileLine1}>
                            <span className={shared.mobileIdentity}>
                              {item.provider_tx_id || item.order_ref || item.id.slice(0, 8)}
                            </span>
                            <span className={shared.mobileMetric}>
                              {formatVnd(item.actual_amount_vnd)}
                            </span>
                          </div>
                          <div className={shared.mobileLine2}>
                            <span className={shared.mobileMetaText}>
                              Kỳ vọng {formatVnd(item.expected_amount_vnd)} · {item.reason}
                            </span>
                            <StatusPill label={stMeta.label} tone={stMeta.tone} />
                          </div>
                        </button>
                      </div>
                    );
                  })}
                </div>

                {selectedQueueItem && (
                  <div className={shared.detailWell} style={{ marginTop: 16 }}>
                    <div className={shared.detailHeader}>
                      <h3 className={shared.detailHeading}>
                        Xử lý giao dịch: {selectedQueueItem.provider_tx_id || selectedQueueItem.id}
                      </h3>
                      <StatusPill
                        label={
                          getReconciliationQueueStatusMeta(selectedQueueItem.resolution_status)
                            .label
                        }
                        tone={
                          getReconciliationQueueStatusMeta(selectedQueueItem.resolution_status).tone
                        }
                      />
                    </div>

                    <div className={shared.detailGrid}>
                      <div className={shared.detailField}>
                        <span className={shared.detailKey}>Cổng / Mã đối tác</span>
                        <span className={shared.detailVal}>
                          {selectedQueueItem.provider} · {selectedQueueItem.provider_tx_id || "—"}
                        </span>
                      </div>
                      <div className={shared.detailField}>
                        <span className={shared.detailKey}>Kỳ vọng / Thực nhận</span>
                        <span className={`${shared.detailVal} num`}>
                          {formatVnd(selectedQueueItem.expected_amount_vnd)} /{" "}
                          {formatVnd(selectedQueueItem.actual_amount_vnd)}
                        </span>
                      </div>
                      <div className={shared.detailField}>
                        <span className={shared.detailKey}>Thời điểm ghi nhận</span>
                        <span className={`${shared.detailValPlain} num`}>
                          {selectedQueueItem.created_at
                            ? formatDateTimeVi(selectedQueueItem.created_at)
                            : "—"}
                        </span>
                      </div>
                    </div>

                    {isQueueItemActionable(selectedQueueItem.resolution_status) ? (
                      <form
                        onSubmit={(e) => void handleResolveQueueSubmit(e, selectedQueueItem)}
                        className={styles.resolveStack}
                      >
                        <div className={shared.detailGrid}>
                          <div className={shared.detailField}>
                            <label htmlFor="rq-action" className={shared.fieldLabel}>
                              Hướng xử lý
                            </label>
                            <select
                              id="rq-action"
                              className={styles.fullSelect}
                              value={resolveAction}
                              onChange={(e) =>
                                setResolveAction(e.target.value as GatewayReconciliationAction)
                              }
                              disabled={resolvingQueue}
                            >
                              <option value="ASSIGN">
                                Gán khớp vào thanh toán (ASSIGN — báo lỗi nếu thiếu tiền)
                              </option>
                              <option value="REFUND">Tạo khoản hoàn tiền cho khách (REFUND)</option>
                              <option value="SUSPEND">Tạm treo chờ kiểm tra thêm (SUSPEND)</option>
                            </select>
                          </div>
                          <div className={shared.detailField}>
                            <label htmlFor="rq-payment-id" className={shared.fieldLabel}>
                              Mã thanh toán (payment_id)
                            </label>
                            <input
                              id="rq-payment-id"
                              type="text"
                              className={shared.textInput}
                              value={resolvePaymentId}
                              onChange={(e) => setResolvePaymentId(e.target.value)}
                              placeholder="Nhập payment_id liên quan..."
                              disabled={resolvingQueue}
                              required={resolveAction === "ASSIGN" || resolveAction === "REFUND"}
                            />
                          </div>
                          <div className={shared.detailField}>
                            <label htmlFor="rq-note" className={shared.fieldLabel}>
                              Ghi chú kế toán
                            </label>
                            <input
                              id="rq-note"
                              type="text"
                              className={shared.textInput}
                              value={resolveNote}
                              onChange={(e) => setResolveNote(e.target.value)}
                              placeholder="Ví dụ: Khách chuyển dư 20.000₫, gán khớp và hoàn phần dư..."
                              disabled={resolvingQueue}
                            />
                          </div>
                        </div>

                        {resolveQueueError && (
                          <p className={shared.validationError} role="alert">
                            {resolveQueueError}
                          </p>
                        )}

                        <div className={shared.detailActionBar}>
                          <span className={shared.savedStamp}>
                            Khóa chống trùng lặp: {resolveIdempotencyKey}
                          </span>
                          <div className={shared.detailButtons}>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={resolvingQueue}
                              onClick={() => setSelectedQueueId(null)}
                            >
                              Đóng
                            </Button>
                            <Button
                              type="submit"
                              variant="primary"
                              size="sm"
                              shape="app"
                              disabled={resolvingQueue}
                            >
                              {resolvingQueue ? "Đang xử lý..." : "Xác nhận xử lý"}
                            </Button>
                          </div>
                        </div>
                      </form>
                    ) : (
                      <p className={shared.savedStamp}>
                        Giao dịch này đã hoàn tất xử lý
                        {selectedQueueItem.resolved_at
                          ? ` lúc ${formatDateTimeVi(selectedQueueItem.resolved_at)}`
                          : ""}
                        {selectedQueueItem.notes ? ` · Ghi chú: ${selectedQueueItem.notes}` : "."}
                      </p>
                    )}
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      )}

      {/* TAB 3: Refunds Settlement Queue + Manual Refund Flow */}
      {activeTab === "hoan_tien" && (
        <div className={shared.sectionStack}>
          {/* Pending Refunds Settlement Queue */}
          <section className={shared.panel} aria-labelledby="refund-queue-heading">
            <div className={shared.panelHeader}>
              <div>
                <h2 id="refund-queue-heading" className={shared.panelTitle}>
                  Hàng đợi hoàn tiền chờ chuyển khoản ({formatInt(refundsTotal)})
                </h2>
                <span className={shared.panelMeta}>
                  Kiểm tra số tài khoản nhận hoàn tiền người mua đã cung cấp và xác nhận tất toán
                  sau khi chuyển khoản.
                </span>
              </div>
              <div className={styles.filterGroup}>
                <select
                  className={shared.toolbarSelect}
                  value={refundStatusFilter}
                  onChange={(e) => setRefundStatusFilter(e.target.value)}
                  aria-label="Lọc trạng thái khoản hoàn tiền"
                >
                  <option value="pending">Chờ chuyển khoản</option>
                  <option value="completed">Đã tất toán</option>
                  <option value="failed">Thất bại</option>
                  <option value="">Tất cả trạng thái</option>
                </select>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={() => void loadRefundsQueue()}
                >
                  Làm mới
                </Button>
              </div>
            </div>

            {refundsLoading ? (
              <StateView
                kind="loading"
                surface="app"
                title="Đang tải danh sách khoản hoàn tiền..."
                rows={4}
              />
            ) : refundsError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải danh sách hoàn tiền"
                message={refundsError}
                actionLabel="Thử lại"
                onAction={() => void loadRefundsQueue()}
              />
            ) : refundsList.length === 0 ? (
              <p className={shared.emptyRow}>
                Không có khoản hoàn tiền nào trong trạng thái đã chọn.
              </p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã hoàn & đơn hàng</th>
                        <th className={shared.colNum}>Số tiền</th>
                        <th>Tài khoản nhận của người mua</th>
                        <th>Trạng thái</th>
                        <th className={shared.colAction}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {refundsList.map((rf) => {
                        const rfMeta = getAdminRefundStatusMeta(rf.status);
                        const pending = isRefundPendingStatus(rf.status);
                        const isSelected = settlingRefundId === rf.refund_id;
                        const accountDisplay =
                          rf.bank_account_number || rf.bank_account_masked || "";
                        return (
                          <tr
                            key={rf.refund_id}
                            className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                            onClick={() => handleOpenSettle(rf)}
                          >
                            <td>
                              <div className={shared.cellPrimary}>
                                {rf.refund_id.slice(0, 8)} · Đơn {rf.order_id.slice(0, 8)}
                              </div>
                              <div className={shared.cellSub}>
                                {rf.is_late_payment ? "Thanh toán trễ · " : ""}
                                {rf.reason || "Hoàn tiền đơn hàng"}
                              </div>
                            </td>
                            <td className={shared.colNum}>{formatVnd(rf.amount_vnd)}</td>
                            <td>
                              {rf.bank_code && accountDisplay ? (
                                <>
                                  <div className={`${shared.cellPrimary} num`}>
                                    {rf.bank_code} · {accountDisplay}
                                  </div>
                                  <div className={shared.cellSub}>
                                    {rf.bank_account_name || "Chưa có tên chủ TK"}
                                  </div>
                                </>
                              ) : (
                                <span className={shared.numMuted}>
                                  Người mua chưa cung cấp tài khoản
                                </span>
                              )}
                            </td>
                            <td>
                              <StatusPill label={rfMeta.label} tone={rfMeta.tone} />
                            </td>
                            <td className={shared.colAction}>
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  handleOpenSettle(rf);
                                }}
                              >
                                {isSelected ? "Đóng" : pending ? "Tất toán" : "Xem"}
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {refundsList.map((rf) => {
                    const rfMeta = getAdminRefundStatusMeta(rf.status);
                    const isSelected = settlingRefundId === rf.refund_id;
                    const accountDisplay = rf.bank_account_number || rf.bank_account_masked || "";
                    return (
                      <div key={rf.refund_id} className={shared.mobileItem}>
                        <button
                          type="button"
                          className={shared.mobileItemBtn}
                          onClick={() => handleOpenSettle(rf)}
                          aria-expanded={isSelected}
                        >
                          <div className={shared.mobileLine1}>
                            <span className={shared.mobileIdentity}>
                              Hoàn #{rf.refund_id.slice(0, 8)}
                            </span>
                            <span className={shared.mobileMetric}>{formatVnd(rf.amount_vnd)}</span>
                          </div>
                          <div className={shared.mobileLine2}>
                            <span className={shared.mobileMetaText}>
                              {rf.bank_code && accountDisplay
                                ? `${rf.bank_code} · ${accountDisplay}`
                                : "Chưa có STK người mua"}
                            </span>
                            <StatusPill label={rfMeta.label} tone={rfMeta.tone} />
                          </div>
                        </button>
                      </div>
                    );
                  })}
                </div>

                {settlingRefund && (
                  <div className={shared.detailWell} style={{ marginTop: 16 }}>
                    <div className={shared.detailHeader}>
                      <h3 className={shared.detailHeading}>
                        Chi tiết khoản hoàn tiền: {settlingRefund.refund_id}
                      </h3>
                      <StatusPill
                        label={getAdminRefundStatusMeta(settlingRefund.status).label}
                        tone={getAdminRefundStatusMeta(settlingRefund.status).tone}
                      />
                    </div>

                    <div className={shared.detailGrid}>
                      <div className={shared.detailField}>
                        <span className={shared.detailKey}>Số tiền hoàn</span>
                        <span className={`${shared.detailVal} num`}>
                          {formatVnd(settlingRefund.amount_vnd)}
                        </span>
                      </div>
                      <div className={shared.detailField}>
                        <span className={shared.detailKey}>Đơn hàng / Thanh toán</span>
                        <span className={`${shared.detailValPlain} num`}>
                          {settlingRefund.order_id}
                        </span>
                      </div>
                      <div className={shared.detailField}>
                        <span className={shared.detailKey}>Ngân hàng & số tài khoản nhận</span>
                        <span className={`${shared.detailVal} num`}>
                          {settlingRefund.bank_code &&
                          (settlingRefund.bank_account_number || settlingRefund.bank_account_masked)
                            ? `${settlingRefund.bank_code} · ${settlingRefund.bank_account_number || settlingRefund.bank_account_masked} · ${settlingRefund.bank_account_name || ""}`
                            : "Người mua chưa nhập tài khoản nhận"}
                        </span>
                      </div>
                    </div>

                    {isRefundPendingStatus(settlingRefund.status) ? (
                      <form
                        onSubmit={(e) => void handleSubmitSettle(e, settlingRefund)}
                        className={styles.resolveStack}
                      >
                        <div className={shared.detailGrid}>
                          <div className={shared.detailField}>
                            <label htmlFor="settle-ref" className={shared.fieldLabel}>
                              Mã giao dịch chuyển khoản ngân hàng (bắt buộc)
                            </label>
                            <input
                              id="settle-ref"
                              type="text"
                              className={shared.textInput}
                              value={settleProviderRef}
                              onChange={(e) => setSettleProviderRef(e.target.value)}
                              placeholder="Ví dụ: FT261123000188"
                              disabled={settleBusy}
                              required
                            />
                          </div>
                          <div className={`${shared.detailField} ${styles.spanTwo}`}>
                            <label htmlFor="settle-note" className={shared.fieldLabel}>
                              Ghi chú tất toán (tùy chọn)
                            </label>
                            <input
                              id="settle-note"
                              type="text"
                              className={shared.textInput}
                              value={settleNote}
                              onChange={(e) => setSettleNote(e.target.value)}
                              placeholder="Ví dụ: Đã chuyển khoản hoàn trả qua Vietcombank..."
                              disabled={settleBusy}
                            />
                          </div>
                        </div>

                        {settleError && (
                          <p className={shared.validationError} role="alert">
                            {settleError}
                          </p>
                        )}

                        <div className={shared.detailActionBar}>
                          <span className={shared.savedStamp}>
                            Khóa chống trùng lặp: {settleIdempotencyKey}
                          </span>
                          <div className={shared.detailButtons}>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={settleBusy}
                              onClick={() => setSettlingRefundId(null)}
                            >
                              Đóng
                            </Button>
                            <Button
                              type="submit"
                              variant="primary"
                              size="sm"
                              shape="app"
                              disabled={settleBusy}
                            >
                              {settleBusy ? "Đang tất toán..." : "Xác nhận đã chuyển khoản"}
                            </Button>
                          </div>
                        </div>
                      </form>
                    ) : (
                      <p className={shared.savedStamp}>
                        {settlingRefund.settled_at
                          ? `Đã tất toán lúc ${formatDateTimeVi(settlingRefund.settled_at)}`
                          : "Khoản hoàn tiền đã xử lý xong."}
                        {settlingRefund.provider_refund_id
                          ? ` · Mã chuyển khoản: ${settlingRefund.provider_refund_id}`
                          : ""}
                        {settlingRefund.settlement_note
                          ? ` · ${settlingRefund.settlement_note}`
                          : ""}
                      </p>
                    )}
                  </div>
                )}
              </>
            )}
          </section>

          {/* Manual Refund Creation Panel */}
          <section className={shared.panel} aria-labelledby="refund-panel-heading">
            <div className={shared.panelHeader}>
              <h2 id="refund-panel-heading" className={shared.panelTitle}>
                Tạo phiếu hoàn tiền thủ công
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
                          ? `Đã ghi nhận trước đó (${getAdminRefundStatusMeta(refundResult.refund.status).label})`
                          : `Tạo khoản hoàn tiền thành công (${getAdminRefundStatusMeta(refundResult.refund.status).label})`
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
              <>
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
                      {orders.slice(0, 20).map((ord) => {
                        const stMeta = getAdminOrderStatusMeta(ord.status);
                        return (
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
                              <StatusPill label={stMeta.label} tone={stMeta.tone} />
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
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {orders.slice(0, 20).map((ord) => {
                    const stMeta = getAdminOrderStatusMeta(ord.status);
                    return (
                      <div key={`m-ord-${ord.id}`} className={shared.mobileItem}>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{ord.order_code}</span>
                          <span className={shared.mobileMetric}>
                            {formatVnd(ord.total_payable_vnd)}
                          </span>
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>{ord.payment_method}</span>
                          <div className={shared.detailButtons}>
                            <StatusPill label={stMeta.label} tone={stMeta.tone} />
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              onClick={() => handleOpenRefundForm(ord)}
                            >
                              Hoàn
                            </button>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </section>
        </div>
      )}

      {/* TAB 4: Orphan / Manual Review Orders */}
      {activeTab === "can_xu_ly" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-labelledby="needs-review-heading">
            <div className={shared.panelHeader}>
              <div>
                <h2 id="needs-review-heading" className={shared.panelTitle}>
                  Đơn hàng cần xử lý tay ({formatInt(needsReviewTotal)})
                </h2>
                <span className={shared.panelMeta}>
                  Các đơn phát sinh lỗi hoàn tác / bù trừ tồn kho hoặc sai lệch saga cần vận hành
                  kiểm tra và xác nhận đã xử lý.
                </span>
              </div>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => void loadNeedsReviewOrders()}
              >
                Làm mới
              </Button>
            </div>

            {needsReviewLoading ? (
              <StateView
                kind="loading"
                surface="app"
                title="Đang tải danh sách đơn cần xử lý tay..."
                rows={4}
              />
            ) : needsReviewError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải danh sách đơn cần xử lý tay"
                message={needsReviewError}
                actionLabel="Thử lại"
                onAction={() => void loadNeedsReviewOrders()}
              />
            ) : needsReviewOrders.length === 0 ? (
              <p className={shared.emptyRow}>
                Không có đơn hàng nào bị gắn cờ cần xử lý tay lúc này.
              </p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã đơn & thời gian</th>
                        <th>Thanh toán & trạng thái</th>
                        <th className={shared.colNum}>Tổng tiền</th>
                        <th>Ghi chú & xác nhận xử lý</th>
                      </tr>
                    </thead>
                    <tbody>
                      {needsReviewOrders.map((ord) => {
                        const stMeta = getAdminOrderStatusMeta(ord.status);
                        const busy = resolvingOrderId === ord.id;
                        return (
                          <tr key={ord.id}>
                            <td>
                              <div className={shared.cellPrimary}>{ord.order_code}</div>
                              <div className={shared.cellSub}>
                                ID: {ord.id.slice(0, 8)} ·{" "}
                                {ord.created_at ? formatDateTimeVi(ord.created_at) : "—"}
                              </div>
                            </td>
                            <td>
                              <StatusPill label={stMeta.label} tone={stMeta.tone} />
                              <div className={shared.cellSub}>
                                {ord.payment_method}
                                {ord.payment_id ? ` · ${ord.payment_id.slice(0, 8)}` : ""}
                              </div>
                            </td>
                            <td className={shared.colNum}>{formatVnd(ord.total_payable_vnd)}</td>
                            <td>
                              <div className={shared.detailButtons}>
                                <input
                                  type="text"
                                  className={shared.textInput}
                                  style={{ height: 32, minWidth: 200 }}
                                  value={reviewNoteDrafts[ord.id] ?? ""}
                                  onChange={(e) =>
                                    setReviewNoteDrafts((prev) => ({
                                      ...prev,
                                      [ord.id]: e.target.value,
                                    }))
                                  }
                                  placeholder="Ghi chú kết quả kiểm tra & xử lý..."
                                  aria-label={`Ghi chú xử lý đơn ${ord.order_code}`}
                                />
                                <Button
                                  type="button"
                                  variant="primary"
                                  size="sm"
                                  shape="app"
                                  disabled={busy}
                                  onClick={() => void handleResolveManualOrder(ord)}
                                >
                                  {busy ? "Đang lưu..." : "Đã xử lý"}
                                </Button>
                              </div>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {needsReviewOrders.map((ord) => {
                    const stMeta = getAdminOrderStatusMeta(ord.status);
                    const busy = resolvingOrderId === ord.id;
                    return (
                      <div key={`m-nr-${ord.id}`} className={shared.mobileItem}>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{ord.order_code}</span>
                          <span className={shared.mobileMetric}>
                            {formatVnd(ord.total_payable_vnd)}
                          </span>
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>{ord.payment_method}</span>
                          <StatusPill label={stMeta.label} tone={stMeta.tone} />
                        </div>
                        <div
                          style={{
                            marginTop: 8,
                            display: "flex",
                            flexDirection: "column",
                            gap: 6,
                          }}
                        >
                          <input
                            type="text"
                            className={shared.textInput}
                            value={reviewNoteDrafts[ord.id] ?? ""}
                            onChange={(e) =>
                              setReviewNoteDrafts((prev) => ({
                                ...prev,
                                [ord.id]: e.target.value,
                              }))
                            }
                            placeholder="Ghi chú kết quả xử lý..."
                          />
                          <Button
                            type="button"
                            variant="primary"
                            size="sm"
                            shape="app"
                            disabled={busy}
                            onClick={() => void handleResolveManualOrder(ord)}
                          >
                            {busy ? "Đang lưu..." : "Đánh dấu đã xử lý"}
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </section>
        </div>
      )}

      {/* TAB 5: Historical Mock Batches (kept separated) */}
      {activeTab === "phien_mau" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-label="Lịch sử phiên đối soát tham khảo">
            <div style={{ marginBottom: 12 }}>
              <StatusPill label="Bản xem trước, dữ liệu mẫu" tone="warning" />
            </div>
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

            <div className={shared.mobileList}>
              {filteredMockRows.map((row) => {
                const meta = RECON_STATUS_META[row.status];
                return (
                  <div key={`m-mock-${row.id}`} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{row.batchCode}</span>
                      <span className={shared.mobileMetric}>{formatVnd(row.grossCollected)}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {CHANNEL_META[row.channel]} · {formatDateVi(row.date)}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                  </div>
                );
              })}
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
      subtitle="Theo dõi cân đối sổ cái ghi kép, xử lý giao dịch không khớp và tất toán hoàn tiền."
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
