"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayCarrierConfig,
  GatewayImportCodReconciliationResponse,
  GatewayLedgerSummary,
  GatewayOrder,
  GatewayReconcileResponse,
  GatewayReconciliationAction,
  GatewayReconciliationQueueItem,
  GatewayReconciliationRunSummary,
  GatewayRefund,
  GatewayRefundResponse,
  GatewayResolveReconciliationResponse,
  GatewaySettleRefundResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import {
  formatCarrierLabel,
  formatDateTimeVi,
  formatInt,
  formatVnDayKey,
  formatVnd,
} from "@/lib/format";
import {
  COD_RESULT_PAGE_SIZE,
  formatReconciliationNote,
  formatReconciliationReason,
  paginateCodItems,
  validateCodCsvFile,
  validateCodCsvText,
} from "@/features/admin/codReconciliationHelper";
import {
  type ApprovalActorLookupEntry,
  RECONCILE_ITEM_STATUS_META,
  formatApprovalActorDisplay,
  getAdminOrderStatusMeta,
  getAdminRefundStatusMeta,
  getReconciliationQueueStatusMeta,
  isAdminRefundManuallySettleable,
  isAdminWalletRefundAutoPending,
} from "@/features/admin/types";
import { AdminApprovalsPanel } from "@/features/admin/AdminApprovalsPanel";
import { localizeRefundReason } from "@/features/account/orderMeta";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminUser } from "@/features/admin/AdminUserContext";
import { roleHasCapability } from "@/lib/admin-rbac";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type DoiSoatTab =
  "so_cai" | "doi_soat_cod" | "hang_doi" | "hoan_tien" | "duyet_hai_nguoi" | "can_xu_ly";

const FALLBACK_COD_CARRIERS: ReadonlyArray<{ carrier_code: string; name: string }> = [
  { carrier_code: "ghn", name: formatCarrierLabel("ghn") },
  { carrier_code: "ghtk", name: "Giao Hàng Tiết Kiệm (GHTK)" },
  { carrier_code: "viettel_post", name: "Viettel Post" },
  { carrier_code: "vnpost", name: "Vietnam Post" },
  { carrier_code: "jnt", name: "J&T Express" },
  { carrier_code: "spx", name: "SPX Express" },
  { carrier_code: "mock_express", name: formatCarrierLabel("mock_express") },
];

function formatCarrierFromList(
  carrierCode: string | undefined,
  carriers: ReadonlyArray<{ carrier_code: string; name?: string }>
): string {
  const clean = (carrierCode ?? "").trim();
  if (!clean) return "—";
  const matched = carriers.find((c) => c.carrier_code.trim().toLowerCase() === clean.toLowerCase());
  return formatCarrierLabel(clean, matched?.name);
}

function formatQueueSourceType(sourceType?: string): string {
  switch ((sourceType ?? "").trim().toLowerCase()) {
    case "cod_file":
      return "Đối soát COD (file)";
    case "webhook":
      return "Webhook cổng thanh toán";
    case "reconciliation":
      return "Đối soát ngày";
    default:
      return sourceType || "Hệ thống";
  }
}

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

function ReconciliationContent() {
  const { showToast } = useMockStore();
  const adminUser = useAdminUser();
  const role = adminUser?.role ?? "";
  const knownActorsById = useMemo<Record<string, ApprovalActorLookupEntry>>(
    () =>
      adminUser?.id
        ? {
            [adminUser.id]: {
              full_name: adminUser.full_name,
              email: adminUser.email,
              role: adminUser.role,
            },
          }
        : {},
    [adminUser]
  );

  const canReadLedger = roleHasCapability(role, "ledger.read");
  const canWriteReconciliation = roleHasCapability(role, "reconciliation.write");
  const canReadOrders = roleHasCapability(role, "orders.read");
  const canReadRefunds = roleHasCapability(role, "refunds.read");
  const canCreateRefund = roleHasCapability(role, "refunds.create");
  const canSettleRefund = roleHasCapability(role, "refunds.settle");
  const canReadCarriers = roleHasCapability(role, "carriers.read");
  const canReadApprovals = roleHasCapability(role, "approvals.read");
  const canResolveOrderReview = roleHasCapability(role, "orders.review.resolve");

  const defaultTab: DoiSoatTab = canReadLedger
    ? "so_cai"
    : canReadRefunds
      ? "hoan_tien"
      : canReadOrders
        ? "can_xu_ly"
        : "duyet_hai_nguoi";

  const [activeTab, setActiveTab] = useState<DoiSoatTab>(defaultTab);
  const [ledger, setLedger] = useState<GatewayLedgerSummary | null>(null);
  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Real reconcile run state (Vietnam date default)
  const [reconcileDate, setReconcileDate] = useState(todayDateIso);
  const [reconciling, setReconciling] = useState(false);
  const [reconcileError, setReconcileError] = useState<string | null>(null);
  const [reconcileResult, setReconcileResult] = useState<GatewayReconcileResponse | null>(null);

  // COD CSV reconciliation import & runs history state (E20 / E20-bis)
  const [codCarriers, setCodCarriers] = useState<GatewayCarrierConfig[]>([]);
  const [codCarrierCode, setCodCarrierCode] = useState("ghn");
  const [codRunDate, setCodRunDate] = useState(todayDateIso);
  const [codFile, setCodFile] = useState<File | null>(null);
  const [codFileInputKey, setCodFileInputKey] = useState(0);
  const [codImporting, setCodImporting] = useState(false);
  const [codImportError, setCodImportError] = useState<string | null>(null);
  const [codImportResult, setCodImportResult] =
    useState<GatewayImportCodReconciliationResponse | null>(null);
  const [codResultPage, setCodResultPage] = useState(1);
  const [codRuns, setCodRuns] = useState<GatewayReconciliationRunSummary[]>([]);
  const [codRunsTotal, setCodRunsTotal] = useState(0);
  const [codRunsLoading, setCodRunsLoading] = useState(false);
  const [codRunsError, setCodRunsError] = useState<string | null>(null);
  const [isCompactViewport, setIsCompactViewport] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mq = window.matchMedia("(max-width: 720px)");
    const sync = () => setIsCompactViewport(mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);

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
  const [refundStatusFilter, setRefundStatusFilter] = useState("");
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

  // Orphan / manual-review orders (GET /api/v1/admin/orders/needs-review)
  const [needsReviewOrders, setNeedsReviewOrders] = useState<GatewayOrder[]>([]);
  const [needsReviewTotal, setNeedsReviewTotal] = useState(0);
  const [needsReviewLoading, setNeedsReviewLoading] = useState(false);
  const [needsReviewError, setNeedsReviewError] = useState<string | null>(null);
  const [reviewNoteDrafts, setReviewNoteDrafts] = useState<Record<string, string>>({});
  const [resolvingOrderId, setResolvingOrderId] = useState<string | null>(null);

  const loadNeedsReviewOrders = useCallback(async () => {
    if (!canReadOrders) return;
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
  }, [canReadOrders]);

  const loadQueue = useCallback(async () => {
    if (!canReadLedger) return;
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
  }, [queueStatusFilter, canReadLedger]);

  const loadRefundsQueue = useCallback(async () => {
    if (!canReadRefunds) return;
    setRefundsLoading(true);
    setRefundsError(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", "1");
      qs.set("page_size", "50");
      if (refundStatusFilter) qs.set("status", refundStatusFilter);

      const [listRes, pendRes, manualRes] = await Promise.all([
        api<PaginatedResponse<GatewayRefund>>(`admin/refunds?${qs.toString()}`),
        api<PaginatedResponse<GatewayRefund>>(
          "admin/refunds?status=pending&page=1&page_size=1"
        ).catch(() => null),
        api<PaginatedResponse<GatewayRefund>>(
          "admin/refunds?status=MANUAL_REVIEW&page=1&page_size=1"
        ).catch(() => null),
      ]);
      const items = listRes.items ?? [];
      setRefundsList(items);
      setRefundsTotal(listRes.total ?? items.length);
      if (pendRes || manualRes) {
        const pCount = pendRes ? (pendRes.total ?? pendRes.items.length) : 0;
        const mCount = manualRes ? (manualRes.total ?? manualRes.items.length) : 0;
        setPendingRefundsCount(pCount + mCount);
      }
    } catch (err) {
      setRefundsError(
        getErrorMessage(err, "Không thể tải danh sách khoản hoàn tiền chờ tất toán lúc này.")
      );
    } finally {
      setRefundsLoading(false);
    }
  }, [refundStatusFilter, canReadRefunds]);

  const loadData = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [ledgerRes, ordersRes] = await Promise.all([
        canReadLedger ? api<GatewayLedgerSummary>("admin/ledger/summary") : Promise.resolve(null),
        canReadOrders
          ? api<PaginatedResponse<GatewayOrder>>("admin/orders?page=1&page_size=50").catch(() => ({
              items: [],
              page: 1,
              page_size: 50,
              total: 0,
            }))
          : Promise.resolve({ items: [] as GatewayOrder[], page: 1, page_size: 50, total: 0 }),
      ]);
      setLedger(ledgerRes);
      setOrders(ordersRes.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải tổng hợp sổ cái ghi kép lúc này."));
    } finally {
      setLoading(false);
    }
  }, [canReadLedger, canReadOrders]);

  const loadCodCarriers = useCallback(async () => {
    if (!canReadCarriers) return;
    try {
      const res = await api<{ carriers: GatewayCarrierConfig[] }>("admin/carriers");
      const list = Array.isArray(res?.carriers) ? res.carriers : [];
      setCodCarriers(list);
      if (list.length > 0) {
        setCodCarrierCode((prev) =>
          list.some((c) => c.carrier_code === prev) ? prev : list[0].carrier_code
        );
      }
    } catch {
      // Keep fallback options if carriers endpoint is temporarily unavailable
    }
  }, [canReadCarriers]);

  const loadCodRuns = useCallback(async () => {
    if (!canReadLedger) return;
    setCodRunsLoading(true);
    setCodRunsError(null);
    try {
      const res = await api<PaginatedResponse<GatewayReconciliationRunSummary>>(
        "admin/reconciliation/runs?run_type=cod_file&page=1&page_size=20"
      );
      const items = res.items ?? [];
      setCodRuns(items);
      setCodRunsTotal(res.total ?? items.length);
    } catch (err) {
      setCodRunsError(getErrorMessage(err, "Không thể tải lịch sử phiên đối soát COD lúc này."));
    } finally {
      setCodRunsLoading(false);
    }
  }, [canReadLedger]);

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

  useEffect(() => {
    void loadCodRuns();
  }, [loadCodRuns]);

  useEffect(() => {
    void loadCodCarriers();
  }, [loadCodCarriers]);

  const handleCodCsvImport = async (e: React.FormEvent) => {
    e.preventDefault();
    if (codImporting) return;
    setCodImportError(null);

    const carrierCode = codCarrierCode.trim();
    const runDate = codRunDate.trim();
    if (!carrierCode) {
      setCodImportError("Vui lòng chọn đơn vị vận chuyển.");
      return;
    }
    if (!runDate) {
      setCodImportError("Vui lòng chọn ngày chốt đối soát COD.");
      return;
    }
    const fileValidation = validateCodCsvFile(codFile);
    if (fileValidation.error || !codFile) {
      setCodImportError(fileValidation.error ?? "Vui lòng chọn tệp CSV đối soát COD.");
      return;
    }

    try {
      const csvText = await codFile.text();
      const contentCheck = validateCodCsvText(csvText);
      if (contentCheck.error) {
        setCodImportError(contentCheck.error);
        return;
      }
    } catch {
      setCodImportError("Không thể đọc nội dung tệp CSV đã chọn.");
      return;
    }

    setCodImporting(true);
    try {
      const qs = new URLSearchParams({
        carrier_code: carrierCode,
        run_date: runDate,
        file_name: codFile.name,
      });
      const res = await api<GatewayImportCodReconciliationResponse>(
        `admin/reconciliation/cod/import?${qs.toString()}`,
        {
          method: "POST",
          headers: {
            "Content-Type": "text/csv; charset=utf-8",
          },
          body: codFile,
        }
      );
      setCodImportResult(res);
      setCodResultPage(1);
      setCodFile(null);
      setCodFileInputKey((k) => k + 1);
      const run = res.run;
      if (run && run.matched_count === 0 && (run.already_reconciled_count ?? 0) > 0) {
        showToast(
          `Tệp ${run.file_name || codFile.name} đã đối soát trước đó (${formatInt(run.already_reconciled_count)} dòng trùng) — không ghi lặp bút toán.`
        );
      } else {
        showToast(
          `Đã nhập đối soát COD: ${formatInt(run?.matched_count ?? 0)} khớp, ${formatInt(run?.amount_mismatch_count ?? 0)} lệch tiền, ${formatInt(run?.missing_local_count ?? 0)} thiếu hệ thống, ${formatInt(run?.not_delivered_count ?? 0)} chưa giao.`
        );
      }
      await Promise.all([loadData(), loadQueue(), loadCodRuns()]);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể nhập tệp CSV đối soát COD lúc này.");
      setCodImportError(msg);
      showToast(msg);
    } finally {
      setCodImporting(false);
    }
  };

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
    if (reconciling) return;
    if (!reconcileDate.trim()) {
      setReconcileError("Vui lòng chọn ngày chốt sao kê.");
      return;
    }
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

  if (errorMsg || (canReadLedger && !ledger)) {
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

  const diffVnd = ledger ? ledger.total_debits_vnd - ledger.total_credits_vnd : 0;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Đối soát thanh toán & sổ cái</h1>
          <p className={shared.pageSubtitle}>
            {ledger ? (
              <>
                Sổ cái ghi kép hiện đang ở trạng thái{" "}
                <strong>{ledger.is_balanced ? "Cân bằng (khớp Nợ = Có)" : "Chưa cân bằng"}</strong>{" "}
                · {formatInt(ledger.accounts.length)} tài khoản hạch toán bằng số nguyên VND.
              </>
            ) : (
              "Theo dõi đơn hàng, yêu cầu hoàn tiền và hàng đợi phê duyệt theo phạm vi phân quyền."
            )}
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
                loadCodCarriers(),
                loadCodRuns(),
                loadQueue(),
                loadRefundsQueue(),
                loadNeedsReviewOrders(),
              ])
            }
          >
            Làm mới dữ liệu
          </Button>
          {canCreateRefund && (
            <Button variant="primary" size="sm" shape="app" onClick={() => handleOpenRefundForm()}>
              Tạo phiếu hoàn tiền
            </Button>
          )}
        </div>
      </header>

      {ledger && (
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
      )}

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phân mục đối soát và hoàn tiền">
          {canReadLedger && (
            <>
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "so_cai"}
                className={`${shared.tabBtn} ${activeTab === "so_cai" ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab("so_cai")}
              >
                Sổ cái &amp; Đối soát ngày
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "doi_soat_cod"}
                className={`${shared.tabBtn} ${activeTab === "doi_soat_cod" ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab("doi_soat_cod")}
              >
                <span>Đối soát COD (file hãng)</span>
                {codRunsTotal > 0 && <span className={shared.tabCount}>{codRunsTotal}</span>}
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "hang_doi"}
                className={`${shared.tabBtn} ${activeTab === "hang_doi" ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab("hang_doi")}
              >
                <span>Giao dịch không khớp</span>
                {queuePendingTotal > 0 && (
                  <span className={shared.tabCount}>{queuePendingTotal}</span>
                )}
              </button>
            </>
          )}
          {canReadRefunds && (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "hoan_tien"}
              className={`${shared.tabBtn} ${activeTab === "hoan_tien" ? shared.tabBtnActive : ""}`}
              onClick={() => setActiveTab("hoan_tien")}
            >
              <span>Hoàn tiền &amp; Tất toán</span>
              {pendingRefundsCount > 0 && (
                <span className={shared.tabCount}>{pendingRefundsCount}</span>
              )}
            </button>
          )}
          {canReadApprovals && (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "duyet_hai_nguoi"}
              className={`${shared.tabBtn} ${activeTab === "duyet_hai_nguoi" ? shared.tabBtnActive : ""}`}
              onClick={() => setActiveTab("duyet_hai_nguoi")}
            >
              <span>Chờ duyệt 2 người</span>
            </button>
          )}
          {canReadOrders && (
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
          )}
        </div>
      </div>

      {activeTab === "duyet_hai_nguoi" && canReadApprovals && <AdminApprovalsPanel />}

      {/* TAB 1: Real Ledger Summary + Real Reconcile Trigger */}
      {activeTab === "so_cai" && ledger && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-labelledby="reconcile-trigger-heading">
            <div className={shared.panelHeader}>
              <h2 id="reconcile-trigger-heading" className={shared.panelTitle}>
                Chạy đối soát thanh toán theo ngày (giờ Việt Nam UTC+7)
              </h2>
              {ledger && (
                <StatusPill
                  label={ledger.is_balanced ? "Sổ cái cân bằng" : "Sổ cái có chênh lệch"}
                  tone={ledger.is_balanced ? "positive" : "negative"}
                />
              )}
            </div>

            {canWriteReconciliation ? (
              <form
                onSubmit={(e) => void handleRunReconcile(e)}
                className={styles.inlineFormRow}
                noValidate
              >
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
                <Button
                  type="submit"
                  variant="primary"
                  size="sm"
                  shape="app"
                  disabled={reconciling}
                >
                  {reconciling ? "Đang chạy đối soát..." : "Chạy đối soát"}
                </Button>
              </form>
            ) : (
              <p className={shared.pageSubtitle}>
                Vai trò hiện tại chỉ có quyền xem sổ cái (chế độ chỉ đọc).
              </p>
            )}

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
                                <td>{formatReconciliationNote(item.note) || "—"}</td>
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
                                {item.note ? `· ${formatReconciliationNote(item.note)}` : ""}
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

      {/* TAB 2: COD CSV Reconciliation Import & Runs History (E20 / E20-bis) */}
      {activeTab === "doi_soat_cod" && (
        <div className={shared.sectionStack}>
          <section className={shared.panel} aria-labelledby="cod-import-heading">
            <div className={shared.panelHeader}>
              <div>
                <h2 id="cod-import-heading" className={shared.panelTitle}>
                  Nhập tệp CSV đối soát thu hộ COD từ hãng vận chuyển
                </h2>
                <span className={shared.panelMeta}>
                  Khớp mã vận đơn, hạch toán sổ cái ghi kép cho đơn đã giao và đẩy dòng sai lệch vào
                  hàng đợi xử lý.
                </span>
              </div>
              {ledger && (
                <StatusPill
                  label={ledger.is_balanced ? "Sổ cái cân bằng" : "Sổ cái có chênh lệch"}
                  tone={ledger.is_balanced ? "positive" : "negative"}
                />
              )}
            </div>

            <form
              onSubmit={(e) => void handleCodCsvImport(e)}
              className={styles.codFormStack}
              noValidate
            >
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <label htmlFor="cod-carrier" className={shared.fieldLabel}>
                    Đơn vị vận chuyển
                  </label>
                  <select
                    id="cod-carrier"
                    className={styles.fullSelect}
                    value={codCarrierCode}
                    onChange={(e) => setCodCarrierCode(e.target.value)}
                    disabled={codImporting}
                  >
                    {(codCarriers.length > 0 ? codCarriers : FALLBACK_COD_CARRIERS).map((c) => (
                      <option key={c.carrier_code} value={c.carrier_code}>
                        {formatCarrierFromList(c.carrier_code, codCarriers)}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={shared.detailField}>
                  <label htmlFor="cod-run-date" className={shared.fieldLabel}>
                    Ngày chốt đối soát (UTC+7)
                  </label>
                  <input
                    id="cod-run-date"
                    type="date"
                    className={shared.textInput}
                    value={codRunDate}
                    onChange={(e) => setCodRunDate(e.target.value)}
                    disabled={codImporting}
                    required
                  />
                </div>

                <div className={shared.detailField}>
                  <label htmlFor="cod-csv-file" className={shared.fieldLabel}>
                    Tệp sao kê CSV (UTF-8, tối đa 2 MiB · 5.000 dòng)
                  </label>
                  <input
                    key={codFileInputKey}
                    id="cod-csv-file"
                    type="file"
                    accept=".csv,text/csv"
                    className={styles.fileInput}
                    disabled={codImporting}
                    onChange={(e) => {
                      const picked = e.target.files?.[0] ?? null;
                      setCodFile(picked);
                      setCodImportError(null);
                    }}
                  />
                </div>
              </div>

              <p className={styles.codHint}>
                Cột bắt buộc trong dòng tiêu đề:{" "}
                <code>tracking_code,cod_amount_vnd,delivered_at</code>. Cột tùy chọn{" "}
                <code>order_code</code> là mã đơn NexusMart đã gửi cho hãng vận chuyển (ID đơn), chỉ
                cần khi đơn chưa có mã vận đơn trong hệ thống. Tải lại cùng tệp sẽ giữ nguyên sổ cái
                và báo trạng thái Đã đối soát trước đó.
              </p>

              {codImportError && (
                <p className={styles.alertError} role="alert">
                  {codImportError}
                </p>
              )}

              {canWriteReconciliation && (
                <div className={shared.detailButtons}>
                  <Button
                    type="submit"
                    variant="primary"
                    size="sm"
                    shape="app"
                    disabled={codImporting}
                  >
                    {codImporting ? "Đang đối soát tệp CSV..." : "Nhập & đối soát COD"}
                  </Button>
                </div>
              )}
            </form>

            {codImportResult && (
              <div className={shared.detailWell} style={{ marginTop: 16 }}>
                <div className={shared.detailHeader}>
                  <h3 className={shared.detailHeading}>
                    Kết quả nhập COD:{" "}
                    {codImportResult.run.file_name || codImportResult.run.run_id.slice(0, 8)}
                  </h3>
                  <span className={shared.panelMeta}>
                    {formatCarrierFromList(codImportResult.run.provider, codCarriers)} · Ngày{" "}
                    {codImportResult.run.run_date}
                  </span>
                </div>

                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Tổng số dòng CSV</span>
                    <span className={`${shared.detailVal} num`}>
                      {formatInt(codImportResult.run.total_rows)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Khớp mới / Tổng tiền khớp</span>
                    <span className={`${shared.detailVal} num`}>
                      {formatInt(codImportResult.run.matched_count)} dòng ·{" "}
                      {formatVnd(codImportResult.run.total_matched_vnd)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Đã đối soát trước đó</span>
                    <span className={`${shared.detailVal} num`}>
                      {formatInt(codImportResult.run.already_reconciled_count)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Lệch số tiền</span>
                    <span className={`${shared.detailVal} num`}>
                      {formatInt(codImportResult.run.amount_mismatch_count)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Thiếu trên hệ thống</span>
                    <span className={`${shared.detailVal} num`}>
                      {formatInt(codImportResult.run.missing_local_count)}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Đơn chưa giao thành công</span>
                    <span className={`${shared.detailVal} num`}>
                      {formatInt(codImportResult.run.not_delivered_count)}
                    </span>
                  </div>
                </div>

                {(codImportResult.items ?? []).length > 0 &&
                  (() => {
                    const paged = paginateCodItems(
                      codImportResult.items,
                      codResultPage,
                      COD_RESULT_PAGE_SIZE
                    );
                    return (
                      <>
                        {!isCompactViewport ? (
                          <div className={shared.tableWrap} style={{ marginTop: 12 }}>
                            <table className={shared.dataTable}>
                              <thead>
                                <tr>
                                  <th>Dòng</th>
                                  <th>Mã vận đơn & đơn hàng</th>
                                  <th className={shared.colNum}>Trên sàn</th>
                                  <th className={shared.colNum}>Từ file hãng</th>
                                  <th>Kết quả</th>
                                  <th>Ghi chú</th>
                                </tr>
                              </thead>
                              <tbody>
                                {paged.slice.map((item, idx) => {
                                  const rowIndex = paged.startIndex + idx;
                                  const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                                    label: item.status,
                                    tone: "neutral" as const,
                                  };
                                  return (
                                    <tr
                                      key={`cod-item-${item.tracking_code || rowIndex}-${rowIndex}`}
                                    >
                                      <td className="num">{item.line_number ?? rowIndex + 1}</td>
                                      <td>
                                        <div className={shared.cellPrimary}>
                                          {item.tracking_code || item.provider_tx_id || "—"}
                                        </div>
                                        <div className={shared.cellSub}>
                                          {item.order_id
                                            ? `Đơn ${item.order_id.slice(0, 8)}`
                                            : item.payment_id
                                              ? `TT ${item.payment_id.slice(0, 8)}`
                                              : "Không tìm thấy đơn"}
                                        </div>
                                      </td>
                                      <td className={shared.colNum}>
                                        {formatVnd(item.local_amount_vnd)}
                                      </td>
                                      <td className={shared.colNum}>
                                        {formatVnd(item.provider_amount_vnd)}
                                      </td>
                                      <td>
                                        <StatusPill label={meta.label} tone={meta.tone} />
                                      </td>
                                      <td>{formatReconciliationNote(item.note) || "—"}</td>
                                    </tr>
                                  );
                                })}
                              </tbody>
                            </table>
                          </div>
                        ) : (
                          <div className={shared.mobileList} style={{ marginTop: 12 }}>
                            {paged.slice.map((item, idx) => {
                              const rowIndex = paged.startIndex + idx;
                              const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                                label: item.status,
                                tone: "neutral" as const,
                              };
                              return (
                                <div
                                  key={`m-cod-item-${item.tracking_code || rowIndex}-${rowIndex}`}
                                  className={shared.mobileItem}
                                >
                                  <div className={shared.mobileLine1}>
                                    <span className={shared.mobileIdentity}>
                                      #{item.line_number ?? rowIndex + 1} ·{" "}
                                      {item.tracking_code || item.provider_tx_id || "—"}
                                    </span>
                                    <span className={shared.mobileMetric}>
                                      {formatVnd(item.provider_amount_vnd)}
                                    </span>
                                  </div>
                                  <div className={shared.mobileLine2}>
                                    <span className={shared.mobileMetaText}>
                                      Sàn: {formatVnd(item.local_amount_vnd)}
                                      {item.note ? ` · ${formatReconciliationNote(item.note)}` : ""}
                                    </span>
                                    <StatusPill label={meta.label} tone={meta.tone} />
                                  </div>
                                </div>
                              );
                            })}
                          </div>
                        )}

                        {paged.totalPages > 1 && (
                          <div className={shared.detailActionBar}>
                            <span className={shared.savedStamp}>
                              Trang {formatInt(paged.page)} / {formatInt(paged.totalPages)} · Hiển
                              thị {formatInt(paged.startIndex + 1)}–{formatInt(paged.endIndex)} /{" "}
                              {formatInt(codImportResult.items.length)} dòng
                            </span>
                            <div className={shared.detailButtons}>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={paged.page <= 1}
                                onClick={() => setCodResultPage((p) => Math.max(1, p - 1))}
                              >
                                Trang trước
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={paged.page >= paged.totalPages}
                                onClick={() =>
                                  setCodResultPage((p) => Math.min(paged.totalPages, p + 1))
                                }
                              >
                                Trang sau
                              </Button>
                            </div>
                          </div>
                        )}
                      </>
                    );
                  })()}
              </div>
            )}
          </section>

          <section className={shared.panel} aria-labelledby="cod-runs-history-heading">
            <div className={shared.panelHeader}>
              <div>
                <h2 id="cod-runs-history-heading" className={shared.panelTitle}>
                  Lịch sử phiên đối soát COD ({formatInt(codRunsTotal)})
                </h2>
                <span className={shared.panelMeta}>
                  Danh sách các tệp sao kê COD đã nhập và lưu vết thực tế trên dịch vụ thanh toán.
                </span>
              </div>
              <Button variant="outline" size="sm" shape="app" onClick={() => void loadCodRuns()}>
                Làm mới
              </Button>
            </div>

            {codRunsLoading ? (
              <StateView
                kind="loading"
                surface="app"
                title="Đang tải lịch sử phiên đối soát COD..."
                rows={4}
              />
            ) : codRunsError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải lịch sử đối soát COD"
                message={codRunsError}
                actionLabel="Thử lại"
                onAction={() => void loadCodRuns()}
              />
            ) : codRuns.length === 0 ? (
              <p className={shared.emptyRow}>
                Chưa có phiên đối soát COD nào được nhập. Tải tệp CSV ở biểu mẫu phía trên để bắt
                đầu.
              </p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Tệp & ngày chốt</th>
                        <th>Hãng vận chuyển</th>
                        <th className={shared.colNum}>Tổng dòng</th>
                        <th className={shared.colNum}>Khớp / Trùng</th>
                        <th className={shared.colNum}>Lệch / Thiếu / Chưa giao</th>
                        <th className={shared.colNum}>Tổng tiền khớp</th>
                      </tr>
                    </thead>
                    <tbody>
                      {codRuns.map((run) => (
                        <tr key={run.run_id}>
                          <td>
                            <div className={shared.cellPrimary}>
                              {run.file_name || run.run_id.slice(0, 8)}
                            </div>
                            <div className={shared.cellSub}>
                              Ngày {run.run_date}
                              {run.created_at
                                ? ` · Nhập lúc ${formatDateTimeVi(run.created_at)}`
                                : ""}
                            </div>
                          </td>
                          <td>{formatCarrierFromList(run.provider, codCarriers)}</td>
                          <td className={shared.colNum}>{formatInt(run.total_rows)}</td>
                          <td className={shared.colNum}>
                            {formatInt(run.matched_count)} /{" "}
                            {formatInt(run.already_reconciled_count)}
                          </td>
                          <td className={shared.colNum}>
                            {formatInt(run.amount_mismatch_count)} /{" "}
                            {formatInt(run.missing_local_count)} /{" "}
                            {formatInt(run.not_delivered_count)}
                          </td>
                          <td className={shared.colNum}>
                            <strong>{formatVnd(run.total_matched_vnd)}</strong>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {codRuns.map((run) => (
                    <div key={`m-cod-run-${run.run_id}`} className={shared.mobileItem}>
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>
                          {run.file_name || run.run_id.slice(0, 8)}
                        </span>
                        <span className={shared.mobileMetric}>
                          {formatVnd(run.total_matched_vnd)}
                        </span>
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {formatCarrierFromList(run.provider, codCarriers)} · {run.run_date} ·{" "}
                          {formatInt(run.matched_count)} khớp ·{" "}
                          {formatInt(run.already_reconciled_count)} trùng ·{" "}
                          {formatInt(
                            run.amount_mismatch_count +
                              run.missing_local_count +
                              run.not_delivered_count
                          )}{" "}
                          cần xử lý
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

      {/* TAB 3: Mismatched Reconciliation Queue */}
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
                                {item.tracking_code || item.provider_tx_id || item.id.slice(0, 8)}
                              </div>
                              <div className={shared.cellSub}>
                                {formatQueueSourceType(item.source_type)} ·{" "}
                                {formatCarrierFromList(item.provider, codCarriers)} ·{" "}
                                {item.order_ref || item.expected_payment_id || "—"}
                                {item.tracking_code && item.provider_tx_id
                                  ? ` · GD ${item.provider_tx_id}`
                                  : ""}
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
                              <div>{formatReconciliationReason(item.reason)}</div>
                              {item.notes && (
                                <div className={shared.cellSub}>
                                  {formatReconciliationNote(item.notes)}
                                </div>
                              )}
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

                {(() => {
                  const renderQueueDetail = (
                    item: GatewayReconciliationQueueItem,
                    prefix: string
                  ) => (
                    <div className={shared.detailWell}>
                      <div className={shared.detailHeader}>
                        <h3 className={shared.detailHeading}>
                          Xử lý giao dịch: {item.tracking_code || item.provider_tx_id || item.id}
                        </h3>
                        <StatusPill
                          label={getReconciliationQueueStatusMeta(item.resolution_status).label}
                          tone={getReconciliationQueueStatusMeta(item.resolution_status).tone}
                        />
                      </div>

                      <div className={shared.detailGrid}>
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Nguồn phát sinh / Cổng</span>
                          <span className={shared.detailVal}>
                            {formatQueueSourceType(item.source_type)} ·{" "}
                            {formatCarrierFromList(item.provider, codCarriers)}
                          </span>
                        </div>
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Mã vận đơn / Mã đối tác</span>
                          <span className={shared.detailVal}>
                            {item.tracking_code || item.provider_tx_id || "—"}
                          </span>
                        </div>
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Kỳ vọng / Thực nhận</span>
                          <span className={`${shared.detailVal} num`}>
                            {formatVnd(item.expected_amount_vnd)} /{" "}
                            {formatVnd(item.actual_amount_vnd)}
                          </span>
                        </div>
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Thời điểm ghi nhận</span>
                          <span className={`${shared.detailValPlain} num`}>
                            {item.created_at ? formatDateTimeVi(item.created_at) : "—"}
                          </span>
                        </div>
                      </div>

                      {canWriteReconciliation && isQueueItemActionable(item.resolution_status) ? (
                        <form
                          onSubmit={(e) => void handleResolveQueueSubmit(e, item)}
                          className={styles.resolveStack}
                          noValidate
                        >
                          <div className={shared.detailGrid}>
                            <div className={shared.detailField}>
                              <label htmlFor={`${prefix}-action`} className={shared.fieldLabel}>
                                Hướng xử lý
                              </label>
                              <select
                                id={`${prefix}-action`}
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
                                <option value="REFUND">
                                  Tạo khoản hoàn tiền cho khách (REFUND)
                                </option>
                                <option value="SUSPEND">
                                  Tạm treo chờ kiểm tra thêm (SUSPEND)
                                </option>
                              </select>
                            </div>
                            <div className={shared.detailField}>
                              <label htmlFor={`${prefix}-payment-id`} className={shared.fieldLabel}>
                                Mã thanh toán (payment_id)
                              </label>
                              <input
                                id={`${prefix}-payment-id`}
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
                              <label htmlFor={`${prefix}-note`} className={shared.fieldLabel}>
                                Ghi chú kế toán
                              </label>
                              <input
                                id={`${prefix}-note`}
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
                          {item.resolved_at ? ` lúc ${formatDateTimeVi(item.resolved_at)}` : ""}
                          {item.notes ? ` · Ghi chú: ${formatReconciliationNote(item.notes)}` : "."}
                        </p>
                      )}
                    </div>
                  );

                  return (
                    <>
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
                                    {item.tracking_code ||
                                      item.provider_tx_id ||
                                      item.order_ref ||
                                      item.id.slice(0, 8)}
                                  </span>
                                  <span className={shared.mobileMetric}>
                                    {formatVnd(item.actual_amount_vnd)}
                                  </span>
                                </div>
                                <div className={shared.mobileLine2}>
                                  <span className={shared.mobileMetaText}>
                                    {formatQueueSourceType(item.source_type)} · Kỳ vọng{" "}
                                    {formatVnd(item.expected_amount_vnd)} ·{" "}
                                    {formatReconciliationReason(item.reason)}
                                  </span>
                                  <StatusPill label={stMeta.label} tone={stMeta.tone} />
                                </div>
                              </button>
                              {isSelected && (
                                <div className={shared.mobileDetailWrap}>
                                  {renderQueueDetail(item, `m-rq-${item.id}`)}
                                </div>
                              )}
                            </div>
                          );
                        })}
                      </div>

                      {selectedQueueItem && (
                        <div className={shared.desktopDetailSection}>
                          {renderQueueDetail(selectedQueueItem, "rq")}
                        </div>
                      )}
                    </>
                  );
                })()}
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
                  <option value="">Tất cả trạng thái</option>
                  <option value="pending">Chờ chuyển khoản / hoàn tự động</option>
                  <option value="MANUAL_REVIEW">Cần xử lý tay</option>
                  <option value="PENDING_APPROVAL">Chờ duyệt 2 người</option>
                  <option value="completed">Đã tất toán</option>
                  <option value="REJECTED">Từ chối duyệt</option>
                  <option value="failed">Thất bại</option>
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
                        const rfMeta = getAdminRefundStatusMeta(rf.status, rf);
                        const settleable = isAdminRefundManuallySettleable(rf);
                        const walletAutoPending = isAdminWalletRefundAutoPending(rf);
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
                                {localizeRefundReason(rf.reason) || "Hoàn tiền đơn hàng"}
                              </div>
                            </td>
                            <td className={shared.colNum}>{formatVnd(rf.amount_vnd)}</td>
                            <td>
                              {walletAutoPending ? (
                                <>
                                  <div className={shared.cellPrimary}>
                                    ZaloPay đang hoàn tự động
                                  </div>
                                  <div className={`${shared.cellSub} num`}>
                                    {rf.provider_status
                                      ? `Trạng thái đối tác: ${rf.provider_status}`
                                      : rf.provider_m_refund_id || "Đang đồng bộ từ ZaloPay"}
                                  </div>
                                </>
                              ) : rf.bank_code && accountDisplay ? (
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
                                {isSelected
                                  ? "Đóng"
                                  : settleable && canSettleRefund
                                    ? "Tất toán"
                                    : "Xem"}
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
                    const rfMeta = getAdminRefundStatusMeta(rf.status, rf);
                    const walletAutoPending = isAdminWalletRefundAutoPending(rf);
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
                              {walletAutoPending
                                ? `ZaloPay đang hoàn tự động${rf.provider_status ? ` · ${rf.provider_status}` : ""}`
                                : rf.bank_code && accountDisplay
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
                        label={
                          getAdminRefundStatusMeta(settlingRefund.status, settlingRefund).label
                        }
                        tone={getAdminRefundStatusMeta(settlingRefund.status, settlingRefund).tone}
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
                          {isAdminWalletRefundAutoPending(settlingRefund)
                            ? `ZaloPay đang hoàn tự động${settlingRefund.provider_status ? ` (${settlingRefund.provider_status})` : ""}`
                            : settlingRefund.bank_code &&
                                (settlingRefund.bank_account_number ||
                                  settlingRefund.bank_account_masked)
                              ? `${settlingRefund.bank_code} · ${settlingRefund.bank_account_number || settlingRefund.bank_account_masked} · ${settlingRefund.bank_account_name || ""}`
                              : "Người mua chưa nhập tài khoản nhận"}
                        </span>
                      </div>
                      {settlingRefund.requested_by && (
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Người lập yêu cầu</span>
                          <span
                            className={shared.detailValPlain}
                            title={settlingRefund.requested_by}
                          >
                            {formatApprovalActorDisplay(
                              settlingRefund.requested_by,
                              undefined,
                              knownActorsById
                            )}
                          </span>
                        </div>
                      )}
                      {settlingRefund.approved_by && (
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Người phê duyệt 2 người</span>
                          <span
                            className={shared.detailValPlain}
                            title={settlingRefund.approved_by}
                          >
                            {formatApprovalActorDisplay(
                              settlingRefund.approved_by,
                              undefined,
                              knownActorsById
                            )}
                            {settlingRefund.approval_note
                              ? ` · ${settlingRefund.approval_note}`
                              : ""}
                          </span>
                        </div>
                      )}
                      {settlingRefund.provider_m_refund_id && (
                        <div className={shared.detailField}>
                          <span className={shared.detailKey}>Mã hoàn đối tác ZaloPay</span>
                          <span className={`${shared.detailValPlain} num`}>
                            {settlingRefund.provider_m_refund_id}
                            {settlingRefund.provider_status
                              ? ` (${settlingRefund.provider_status})`
                              : ""}
                          </span>
                        </div>
                      )}
                    </div>

                    {isAdminRefundManuallySettleable(settlingRefund) && canSettleRefund ? (
                      <form
                        onSubmit={(e) => void handleSubmitSettle(e, settlingRefund)}
                        className={styles.resolveStack}
                        noValidate
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
                    ) : isAdminWalletRefundAutoPending(settlingRefund) ? (
                      <p className={shared.savedStamp}>
                        ZaloPay đang hoàn tự động về ví người mua
                        {settlingRefund.provider_status
                          ? ` (trạng thái đối tác: ${settlingRefund.provider_status})`
                          : ""}
                        . Không thực hiện tất toán tay khi giao dịch hoàn ví đang xử lý tự động.
                      </p>
                    ) : (
                      <p className={shared.savedStamp}>
                        {settlingRefund.settled_at
                          ? `Đã tất toán lúc ${formatDateTimeVi(settlingRefund.settled_at)}`
                          : getAdminRefundStatusMeta(settlingRefund.status, settlingRefund).label}
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
              <form
                className={shared.detailWell}
                onSubmit={(e) => void handleSubmitRefund(e)}
                noValidate
              >
                <div className={shared.detailHeader}>
                  <h3 className={shared.detailHeading}>Thông tin lệnh hoàn tiền</h3>
                  <span className={shared.panelMeta}>
                    Mã chống trùng lặp: {refundIdempotencyKey}
                  </span>
                </div>

                {refundError && (
                  <p className={shared.validationError} role="alert">
                    {refundError}
                  </p>
                )}

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
                      min={1}
                      step={1}
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
                                  disabled={busy || !canResolveOrderReview}
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
