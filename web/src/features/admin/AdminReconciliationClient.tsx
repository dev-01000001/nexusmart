"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconPlus, IconSearch, StateView } from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import { canDecideTwoPersonApproval, roleHasCapability } from "@/lib/admin-rbac";
import {
  formatCarrierLabel,
  formatDateTimeVi,
  formatInt,
  formatSignedVnd,
  formatVnIsoDate,
  formatVnd,
} from "@/lib/format";
import type {
  GatewayCarrierConfig,
  GatewayImportCodReconciliationResponse,
  GatewayOrder,
  GatewayReconciliationAction,
  GatewayReconciliationQueueItem,
  GatewayRefund,
  GatewayRefundResponse,
  GatewayResolveReconciliationResponse,
  GatewaySettleRefundResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { getSalesChannelMeta, type SalesChannelFilter } from "@/lib/salesChannel";
import { localizeRefundReason } from "@/features/account/orderMeta";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { AdminDrawer, SlidingTabBar } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import {
  getAccountTypeLabelVi,
  getAdminOrderStatusMeta,
  getReconItemStatusMeta,
  type GwAccountBalance,
  type GwLedgerSummary,
  type GwReconciliationItem,
  type GwReconciliationReportExtended,
  type GwReconciliationRunSummaryExtended,
} from "./adminApiTypes";
import {
  COD_RESULT_PAGE_SIZE,
  formatReconciliationNote,
  formatReconciliationReason,
  paginateCodItems,
  validateCodCsvFile,
  validateCodCsvText,
} from "./codReconciliationHelper";
import {
  formatApprovalActorDisplay,
  getAdminRefundStatusMeta,
  getReconciliationQueueStatusMeta,
  isAdminRefundManuallySettleable,
  isAdminWalletRefundAutoPending,
  RECONCILE_ITEM_STATUS_META,
} from "./types";
import {
  validateManualRefundInput,
  validateReconciliationQueueResolveInput,
  validateRefundApprovalDecisionInput,
  validateSettleRefundInput,
} from "./AdminReconciliationHelpers";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/doi-soat/page.module.css";

export type ReconciliationMainTab = "ledger" | "cod" | "queue" | "refunds" | "needs-review";

const CHANNEL_TABS: Array<{ id: SalesChannelFilter; label: string; dataChannelTab: string }> = [
  { id: "", label: "Tất cả kênh", dataChannelTab: "ALL" },
  { id: "MARKETPLACE", label: "Sàn NexusMart", dataChannelTab: "MARKETPLACE" },
  { id: "STOREFRONT", label: "Site riêng", dataChannelTab: "STOREFRONT" },
];

const PAGE_SIZE = 20;

function channelScopeLabel(channel: SalesChannelFilter): string {
  if (channel === "MARKETPLACE") return "Sàn NexusMart";
  if (channel === "STOREFRONT") return "Site riêng";
  return "tất cả kênh";
}

function ledgerUrlFor(channel: SalesChannelFilter): string {
  return channel
    ? `/api/admin/ledger?sales_channel=${encodeURIComponent(channel)}`
    : "/api/admin/ledger";
}

function generateIdempotencyKey(prefix: string): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

type DrawerTarget =
  | { kind: "account"; account: GwAccountBalance }
  | { kind: "recon"; item: GwReconciliationItem; runId: string }
  | { kind: "queue"; item: GatewayReconciliationQueueItem }
  | { kind: "refund"; refund: GatewayRefund }
  | { kind: "create-refund" }
  | { kind: "needs-review"; order: GatewayOrder };

export interface AdminReconciliationClientProps {
  initialTab?: ReconciliationMainTab;
  initialLedger: GwLedgerSummary | null;
  initialQueueItems?: GatewayReconciliationQueueItem[];
  initialQueueTotal?: number;
  initialRefunds?: GatewayRefund[];
  initialRefundsTotal?: number;
  initialNeedsReviewOrders?: GatewayOrder[];
  initialNeedsReviewTotal?: number;
  carriers?: GatewayCarrierConfig[];
  loadError: string | null;
  serverNowMs?: number;
}

function ReconciliationContent({
  initialTab,
  initialLedger,
  initialQueueItems = [],
  initialQueueTotal = 0,
  initialRefunds = [],
  initialRefundsTotal = 0,
  initialNeedsReviewOrders = [],
  initialNeedsReviewTotal = 0,
  carriers = [],
  loadError,
  serverNowMs,
  initialQuery,
}: AdminReconciliationClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canReadLedger = roleHasCapability(user.role, "ledger.read");
  const canWriteRecon = roleHasCapability(user.role, "reconciliation.write");
  const canReadRefunds = roleHasCapability(user.role, "refunds.read");
  const canCreateRefund = roleHasCapability(user.role, "refunds.create");
  const canSettleRefund = roleHasCapability(user.role, "refunds.settle");
  const canReadOrders = roleHasCapability(user.role, "orders.read");
  const canResolveOrderReview = roleHasCapability(user.role, "orders.review.resolve");

  const availableTabs = useMemo(() => {
    const list: Array<{ id: ReconciliationMainTab; label: string; count?: number }> = [];
    if (canReadLedger) {
      list.push({ id: "ledger", label: "Sổ cái & đối soát ngày" });
      list.push({ id: "cod", label: "COD & lịch sử" });
      list.push({ id: "queue", label: "Giao dịch lệch", count: initialQueueTotal });
    }
    if (canReadRefunds) {
      list.push({ id: "refunds", label: "Hoàn tiền", count: initialRefundsTotal });
    }
    if (canReadOrders) {
      list.push({ id: "needs-review", label: "Đơn cần rà soát", count: initialNeedsReviewTotal });
    }
    return list;
  }, [
    canReadLedger,
    canReadRefunds,
    canReadOrders,
    initialQueueTotal,
    initialRefundsTotal,
    initialNeedsReviewTotal,
  ]);

  const resolvedDefaultTab: ReconciliationMainTab = useMemo(() => {
    if (initialTab && availableTabs.some((t) => t.id === initialTab)) return initialTab;
    return availableTabs[0]?.id ?? "ledger";
  }, [initialTab, availableTabs]);

  const [mainTab, setMainTab] = useState<ReconciliationMainTab>(resolvedDefaultTab);
  const [drawerTarget, setDrawerTarget] = useState<DrawerTarget | null>(null);

  // --- Ledger & daily reconcile state (phase-0b sales_channel preserved) ---
  const [ledger, setLedger] = useState<GwLedgerSummary | null>(initialLedger);
  const [channelFilter, setChannelFilter] = useState<SalesChannelFilter>("");
  const [loadingChannel, setLoadingChannel] = useState(false);
  const [channelError, setChannelError] = useState<string | null>(null);
  const channelReqSeq = useRef(0);
  const [query, setQuery] = useState(initialQuery);
  const [accountTypeFilter, setAccountTypeFilter] = useState<string>("all");
  const [runDate, setRunDate] = useState<string>(() => formatVnIsoDate(serverNowMs ?? Date.now()));
  const [reportDate, setReportDate] = useState<string>("");
  const [running, setRunning] = useState(false);
  const [report, setReport] = useState<GwReconciliationReportExtended | null>(null);
  const [runError, setRunError] = useState<string | null>(null);

  const carrierOptions = useMemo(() => {
    if (carriers.length > 0) {
      return carriers.map((c) => ({
        code: c.carrier_code,
        label: formatCarrierLabel(c.carrier_code, c.name),
      }));
    }
    return [
      { code: "ghn", label: formatCarrierLabel("ghn") },
      { code: "mock_express", label: formatCarrierLabel("mock_express") },
    ];
  }, [carriers]);

  // --- COD import + Runs history ---
  const [codCarrier, setCodCarrier] = useState(() => carrierOptions[0]?.code ?? "ghn");
  const [codDate, setCodDate] = useState<string>(() => formatVnIsoDate(serverNowMs ?? Date.now()));
  const [codFile, setCodFile] = useState<File | null>(null);
  const [codImporting, setCodImporting] = useState(false);
  const [codError, setCodError] = useState<string | null>(null);
  const [codResult, setCodResult] = useState<GatewayImportCodReconciliationResponse | null>(null);
  const [codResultPage, setCodResultPage] = useState(1);

  const [runs, setRuns] = useState<GwReconciliationRunSummaryExtended[]>([]);
  const [runsTotal, setRunsTotal] = useState(0);
  const [runsPage, setRunsPage] = useState(1);
  const [runsTypeFilter, setRunsTypeFilter] = useState<"" | "vietqr" | "cod_file">("");
  const [runsChannelFilter, setRunsChannelFilter] = useState<SalesChannelFilter>("");
  const [runsLoading, setRunsLoading] = useState(false);
  const [runsLoadedOnce, setRunsLoadedOnce] = useState(false);

  // --- Mismatch queue ---
  const [queueItems, setQueueItems] = useState<GatewayReconciliationQueueItem[]>(initialQueueItems);
  const [queueTotal, setQueueTotal] = useState(initialQueueTotal);
  const [queueStatus, setQueueStatus] = useState<string>("pending");
  const [queuePage, setQueuePage] = useState(1);
  const [queueLoading, setQueueLoading] = useState(false);
  const [queueAction, setQueueAction] = useState<GatewayReconciliationAction>("ASSIGN");
  const [queuePaymentId, setQueuePaymentId] = useState("");
  const [queueNote, setQueueNote] = useState("");
  const [queueFormError, setQueueFormError] = useState<string | null>(null);
  const [queueSubmitting, setQueueSubmitting] = useState(false);

  // --- Refunds (two-step: approve/reject + settle + manual create) ---
  const [refunds, setRefunds] = useState<GatewayRefund[]>(initialRefunds);
  const [refundsTotal, setRefundsTotal] = useState(initialRefundsTotal);
  const [refundStatus, setRefundStatus] = useState<string>("");
  const [refundOrderFilter, setRefundOrderFilter] = useState<string>("");
  const [refundsPage, setRefundsPage] = useState(1);
  const [refundsLoading, setRefundsLoading] = useState(false);

  const [settleRefId, setSettleRefId] = useState("");
  const [settleNote, setSettleNote] = useState("");
  const [approvalNote, setApprovalNote] = useState("");
  const [refundFormError, setRefundFormError] = useState<string | null>(null);
  const [refundBusy, setRefundBusy] = useState(false);

  const [newRefundPaymentId, setNewRefundPaymentId] = useState("");
  const [newRefundOrderId, setNewRefundOrderId] = useState("");
  const [newRefundAmount, setNewRefundAmount] = useState("");
  const [newRefundReason, setNewRefundReason] = useState("");
  const [createRefundError, setCreateRefundError] = useState<string | null>(null);
  const [creatingRefund, setCreatingRefund] = useState(false);

  // --- Orders needing manual review ---
  const [needsReviewOrders, setNeedsReviewOrders] =
    useState<GatewayOrder[]>(initialNeedsReviewOrders);
  const [needsReviewTotal, setNeedsReviewTotal] = useState(initialNeedsReviewTotal);
  const [needsReviewPage, setNeedsReviewPage] = useState(1);
  const [needsReviewLoading, setNeedsReviewLoading] = useState(false);
  const [orderReviewNote, setOrderReviewNote] = useState("");
  const [orderReviewBusy, setOrderReviewBusy] = useState(false);
  const [orderReviewError, setOrderReviewError] = useState<string | null>(null);

  const loadRuns = useCallback(
    async (
      nextType: "" | "vietqr" | "cod_file",
      nextChannel: SalesChannelFilter,
      nextPage: number
    ) => {
      if (!canReadLedger) return;
      setRunsLoading(true);
      try {
        const qs = new URLSearchParams({
          page: String(nextPage),
          page_size: String(PAGE_SIZE),
        });
        if (nextType) qs.set("run_type", nextType);
        if (nextChannel) qs.set("sales_channel", nextChannel);
        const res = await api<PaginatedResponse<GwReconciliationRunSummaryExtended>>(
          `/api/admin/reconciliation/runs?${qs.toString()}`
        );
        setRuns(res.items ?? []);
        setRunsTotal(res.total ?? 0);
        setRunsLoadedOnce(true);
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể tải lịch sử phiên đối soát."));
      } finally {
        setRunsLoading(false);
      }
    },
    [canReadLedger, showToast]
  );

  const loadQueue = useCallback(
    async (nextStatus: string, nextPage: number) => {
      if (!canReadLedger) return;
      setQueueLoading(true);
      try {
        const qs = new URLSearchParams({
          page: String(nextPage),
          page_size: String(PAGE_SIZE),
        });
        if (nextStatus) qs.set("status", nextStatus);
        const res = await api<PaginatedResponse<GatewayReconciliationQueueItem>>(
          `/api/admin/reconciliation/queue?${qs.toString()}`
        );
        setQueueItems(res.items ?? []);
        setQueueTotal(res.total ?? 0);
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể tải hàng đợi giao dịch lệch."));
      } finally {
        setQueueLoading(false);
      }
    },
    [canReadLedger, showToast]
  );

  const loadRefunds = useCallback(
    async (nextStatus: string, nextOrderId: string, nextPage: number) => {
      if (!canReadRefunds) return;
      setRefundsLoading(true);
      try {
        const qs = new URLSearchParams({
          page: String(nextPage),
          page_size: String(PAGE_SIZE),
        });
        if (nextStatus) qs.set("status", nextStatus);
        if (nextOrderId.trim()) qs.set("order_id", nextOrderId.trim());
        const res = await api<PaginatedResponse<GatewayRefund>>(
          `/api/admin/refunds?${qs.toString()}`
        );
        setRefunds(res.items ?? []);
        setRefundsTotal(res.total ?? 0);
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể tải danh sách hoàn tiền."));
      } finally {
        setRefundsLoading(false);
      }
    },
    [canReadRefunds, showToast]
  );

  const loadNeedsReview = useCallback(
    async (nextPage: number) => {
      if (!canReadOrders) return;
      setNeedsReviewLoading(true);
      try {
        const qs = new URLSearchParams({
          page: String(nextPage),
          page_size: String(PAGE_SIZE),
        });
        const res = await api<PaginatedResponse<GatewayOrder>>(
          `/api/admin/orders/needs-review?${qs.toString()}`
        );
        setNeedsReviewOrders(res.items ?? []);
        setNeedsReviewTotal(res.total ?? 0);
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể tải danh sách đơn cần rà soát."));
      } finally {
        setNeedsReviewLoading(false);
      }
    },
    [canReadOrders, showToast]
  );

  useEffect(() => {
    if (mainTab === "cod" && !runsLoadedOnce && canReadLedger) {
      void loadRuns(runsTypeFilter, runsChannelFilter, 1);
    }
  }, [mainTab, runsLoadedOnce, canReadLedger, loadRuns, runsTypeFilter, runsChannelFilter]);

  const handleSelectChannel = async (nextChannel: SalesChannelFilter) => {
    if (nextChannel === channelFilter && !channelError) return;
    setChannelFilter(nextChannel);
    setChannelError(null);
    setRunError(null);
    setReport(null);
    setReportDate("");
    setRunning(false);
    setLoadingChannel(true);
    const reqId = ++channelReqSeq.current;
    try {
      const fresh = await api<GwLedgerSummary>(ledgerUrlFor(nextChannel));
      if (channelReqSeq.current !== reqId) return;
      setLedger(fresh);
    } catch (err) {
      if (channelReqSeq.current !== reqId) return;
      const msg = getErrorMessage(err, "Không thể tải số liệu sổ cái cho kênh bán này.");
      setChannelError(msg);
      showToast(msg);
    } finally {
      if (channelReqSeq.current === reqId) {
        setLoadingChannel(false);
      }
    }
  };

  const handleRunReconcile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (running || !canWriteRecon) return;
    const targetDate = runDate;
    const activeChannel = channelFilter;
    const reqId = ++channelReqSeq.current;
    setLoadingChannel(false);
    setRunning(true);
    setRunError(null);
    try {
      const [reconRes, freshLedger] = await Promise.all([
        api<GwReconciliationReportExtended>("/api/admin/reconcile", {
          method: "POST",
          body: activeChannel
            ? { date: targetDate, sales_channel: activeChannel }
            : { date: targetDate },
        }),
        api<GwLedgerSummary>(ledgerUrlFor(activeChannel)),
      ]);
      if (channelReqSeq.current !== reqId) return;
      setReport(reconRes);
      setReportDate(targetDate);
      setLedger(freshLedger);
      const issues =
        reconRes.missing_local_count +
        reconRes.missing_provider_count +
        reconRes.amount_mismatch_count;
      const scopeSuffix = activeChannel ? ` (${channelScopeLabel(activeChannel)})` : "";
      showToast(
        issues === 0
          ? `Đối soát ${targetDate}${scopeSuffix}: ${formatInt(reconRes.matched_count)} giao dịch khớp.`
          : `Đối soát ${targetDate}${scopeSuffix}: phát hiện ${formatInt(issues)} mục lệch.`
      );
      if (runsLoadedOnce) void loadRuns(runsTypeFilter, runsChannelFilter, 1);
    } catch (err) {
      if (channelReqSeq.current !== reqId) return;
      const msg = getErrorMessage(err, "Không thể chạy phiên đối soát lúc này.");
      setRunError(msg);
      showToast(msg);
    } finally {
      if (channelReqSeq.current === reqId) {
        setRunning(false);
      }
    }
  };

  const handleCodImport = async (e: React.FormEvent) => {
    e.preventDefault();
    if (codImporting || !canWriteRecon) return;
    const fileCheck = validateCodCsvFile(codFile);
    if (fileCheck.error || !codFile) {
      setCodError(fileCheck.error ?? "Vui lòng chọn tệp CSV.");
      return;
    }
    const text = await codFile.text();
    const textCheck = validateCodCsvText(text);
    if (textCheck.error) {
      setCodError(textCheck.error);
      return;
    }

    setCodError(null);
    setCodImporting(true);
    try {
      const formData = new FormData();
      formData.append("file", codFile, codFile.name);
      formData.append("carrier_code", codCarrier);
      formData.append("run_date", codDate);
      formData.append("file_name", codFile.name);

      const res = await api<GatewayImportCodReconciliationResponse>(
        "/api/admin/reconciliation/cod/import",
        {
          method: "POST",
          body: formData,
        }
      );
      setCodResult(res);
      setCodResultPage(1);
      setCodFile(null);
      showToast(
        `Đã đối soát COD ${formatCarrierLabel(codCarrier)}: ${formatInt(res.run.matched_count)}/${formatInt(res.run.total_rows)} dòng khớp.`
      );
      void loadRuns(runsTypeFilter, runsChannelFilter, 1);
      if (canReadLedger) {
        void api<GwLedgerSummary>(ledgerUrlFor(channelFilter))
          .then(setLedger)
          .catch(() => {});
      }
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể nhập tệp CSV đối soát COD lúc này.");
      setCodError(msg);
      showToast(msg);
    } finally {
      setCodImporting(false);
    }
  };

  const handleResolveQueueItem = async (
    e: React.FormEvent,
    item: GatewayReconciliationQueueItem
  ) => {
    e.preventDefault();
    if (queueSubmitting || !canWriteRecon) return;
    const validated = validateReconciliationQueueResolveInput({
      action: queueAction,
      payment_id: queuePaymentId,
      note: queueNote,
    });
    if (!validated.ok) {
      setQueueFormError(validated.message);
      return;
    }

    setQueueFormError(null);
    setQueueSubmitting(true);
    try {
      const res = await api<GatewayResolveReconciliationResponse>(
        `/api/admin/reconciliation/queue/${encodeURIComponent(item.id)}/resolve`,
        {
          method: "POST",
          headers: { "Idempotency-Key": generateIdempotencyKey("recon-q") },
          body: validated.value,
        }
      );
      setQueueItems((prev) => prev.map((q) => (q.id === item.id ? res.item : q)));
      setDrawerTarget({ kind: "queue", item: res.item });
      setQueueNote("");
      showToast("Đã cập nhật xử lý giao dịch không khớp.");
      void loadQueue(queueStatus, queuePage);
      if (res.refund && canReadRefunds) {
        void loadRefunds(refundStatus, refundOrderFilter, 1);
      }
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể xử lý mục đối soát này.");
      setQueueFormError(msg);
      showToast(msg);
    } finally {
      setQueueSubmitting(false);
    }
  };

  const handleDecideRefund = async (refund: GatewayRefund, approve: boolean) => {
    if (refundBusy) return;
    const validated = validateRefundApprovalDecisionInput({ approve, note: approvalNote });
    if (!validated.ok) {
      setRefundFormError(validated.message);
      return;
    }
    setRefundFormError(null);
    setRefundBusy(true);
    try {
      const res = await api<{ refund: GatewayRefund }>(
        `/api/admin/refunds/${encodeURIComponent(refund.refund_id)}/decide`,
        {
          method: "POST",
          body: validated.value,
        }
      );
      setRefunds((prev) => prev.map((rf) => (rf.refund_id === refund.refund_id ? res.refund : rf)));
      setDrawerTarget({ kind: "refund", refund: res.refund });
      setApprovalNote("");
      showToast(
        approve
          ? `Đã phê duyệt lệnh hoàn tiền ${formatVnd(refund.amount_vnd)}.`
          : "Đã từ chối lệnh hoàn tiền."
      );
      router.refresh();
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể cập nhật quyết định phê duyệt hoàn tiền.");
      setRefundFormError(msg);
      showToast(msg);
    } finally {
      setRefundBusy(false);
    }
  };

  const handleSettleRefund = async (e: React.FormEvent, refund: GatewayRefund) => {
    e.preventDefault();
    if (refundBusy || !canSettleRefund) return;
    const validated = validateSettleRefundInput({
      provider_refund_id: settleRefId,
      note: settleNote,
    });
    if (!validated.ok) {
      setRefundFormError(validated.message);
      return;
    }
    setRefundFormError(null);
    setRefundBusy(true);
    try {
      const res = await api<GatewaySettleRefundResponse>(
        `/api/admin/refunds/${encodeURIComponent(refund.refund_id)}/settle`,
        {
          method: "POST",
          headers: { "Idempotency-Key": generateIdempotencyKey("settle") },
          body: validated.value,
        }
      );
      setRefunds((prev) => prev.map((rf) => (rf.refund_id === refund.refund_id ? res.refund : rf)));
      setDrawerTarget({ kind: "refund", refund: res.refund });
      setSettleRefId("");
      setSettleNote("");
      showToast(`Đã xác nhận tất toán hoàn tiền ${formatVnd(refund.amount_vnd)}.`);
      router.refresh();
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể xác nhận tất toán hoàn tiền.");
      setRefundFormError(msg);
      showToast(msg);
    } finally {
      setRefundBusy(false);
    }
  };

  const handleCreateManualRefund = async (e: React.FormEvent) => {
    e.preventDefault();
    if (creatingRefund || !canCreateRefund) return;
    const parsedAmount = Number(newRefundAmount.trim());
    const validated = validateManualRefundInput({
      payment_id: newRefundPaymentId,
      order_id: newRefundOrderId,
      amount_vnd: parsedAmount,
      reason: newRefundReason,
    });
    if (!validated.ok) {
      setCreateRefundError(validated.message);
      return;
    }
    setCreateRefundError(null);
    setCreatingRefund(true);
    try {
      const res = await api<GatewayRefundResponse>("/api/admin/refunds", {
        method: "POST",
        headers: { "Idempotency-Key": generateIdempotencyKey("refund") },
        body: validated.value,
      });
      setRefunds((prev) => [
        res.refund,
        ...prev.filter((r) => r.refund_id !== res.refund.refund_id),
      ]);
      setRefundsTotal((prev) => prev + (res.already_existed ? 0 : 1));
      setNewRefundPaymentId("");
      setNewRefundOrderId("");
      setNewRefundAmount("");
      setNewRefundReason("");
      setDrawerTarget({ kind: "refund", refund: res.refund });
      showToast(
        res.refund.status === "PENDING_APPROVAL"
          ? "Đã tạo lệnh hoàn tiền lớn — chờ người thứ hai phê duyệt."
          : `Đã tạo lệnh hoàn tiền ${formatVnd(res.refund.amount_vnd)}.`
      );
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể tạo lệnh hoàn tiền lúc này.");
      setCreateRefundError(msg);
      showToast(msg);
    } finally {
      setCreatingRefund(false);
    }
  };

  const handleResolveOrderReview = async (e: React.FormEvent, order: GatewayOrder) => {
    e.preventDefault();
    if (orderReviewBusy || !canResolveOrderReview) return;
    setOrderReviewError(null);
    setOrderReviewBusy(true);
    try {
      await api<{ order: GatewayOrder }>(
        `/api/admin/orders/${encodeURIComponent(order.id)}/resolve-review`,
        {
          method: "POST",
          body: { note: orderReviewNote.trim() },
        }
      );
      setNeedsReviewOrders((prev) => prev.filter((o) => o.id !== order.id));
      setNeedsReviewTotal((prev) => Math.max(0, prev - 1));
      setOrderReviewNote("");
      setDrawerTarget(null);
      showToast(`Đã xác nhận rà soát đơn ${order.order_code}.`);
      router.refresh();
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể hoàn tất rà soát đơn hàng.");
      setOrderReviewError(msg);
      showToast(msg);
    } finally {
      setOrderReviewBusy(false);
    }
  };

  const filteredAccounts = useMemo(() => {
    const accounts = ledger?.accounts ?? [];
    const q = query.trim().toLowerCase();
    return accounts.filter((acc) => {
      if (accountTypeFilter !== "all" && acc.account_type.toUpperCase() !== accountTypeFilter) {
        return false;
      }
      if (!q) return true;
      return (
        acc.account_code.toLowerCase().includes(q) || acc.account_type.toLowerCase().includes(q)
      );
    });
  }, [ledger, query, accountTypeFilter]);

  const codPaged = useMemo(
    () => paginateCodItems(codResult?.items ?? [], codResultPage, COD_RESULT_PAGE_SIZE),
    [codResult, codResultPage]
  );

  const tabsWithLiveCounts = useMemo(
    () =>
      availableTabs.map((t) => {
        if (t.id === "queue") return { ...t, count: queueTotal };
        if (t.id === "refunds") return { ...t, count: refundsTotal };
        if (t.id === "needs-review") return { ...t, count: needsReviewTotal };
        return t;
      }),
    [availableTabs, queueTotal, refundsTotal, needsReviewTotal]
  );

  if (loadError && mainTab === "ledger") {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sổ cái đối soát"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const totalDebits = ledger?.total_debits_vnd ?? 0;
  const totalCredits = ledger?.total_credits_vnd ?? 0;
  const diff = totalDebits - totalCredits;
  const isBalanced = Boolean(ledger?.is_balanced && diff === 0);
  const activeScopeLabel = channelScopeLabel(channelFilter);
  const reportIssuesCount = report
    ? report.amount_mismatch_count + report.missing_local_count + report.missing_provider_count
    : 0;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Đối soát &amp; hoàn tiền</h1>
        </div>
        <div className={shared.titleActions}>
          {mainTab === "ledger" && canWriteRecon && (
            <form className={styles.runFormInline} onSubmit={(e) => void handleRunReconcile(e)}>
              <label htmlFor="recon-date" className="sr-only">
                Ngày đối soát
              </label>
              <input
                id="recon-date"
                type="date"
                className={shared.toolbarDate}
                value={runDate}
                onChange={(e) => setRunDate(e.target.value)}
                aria-label="Chọn ngày đối soát"
              />
              <Button type="submit" variant="primary" size="sm" shape="app" loading={running}>
                Chạy đối soát ngày
              </Button>
            </form>
          )}
          {mainTab === "refunds" && canCreateRefund && (
            <Button
              variant="primary"
              size="sm"
              shape="app"
              onClick={() => {
                setCreateRefundError(null);
                setDrawerTarget({ kind: "create-refund" });
              }}
            >
              <IconPlus size={14} />
              <span>Tạo lệnh hoàn tiền</span>
            </Button>
          )}
        </div>
      </header>

      <SlidingTabBar
        tabs={tabsWithLiveCounts}
        activeId={mainTab}
        onChange={setMainTab}
        ariaLabel="Phân hệ đối soát và hoàn tiền"
      />

      {/* TAB 1: LEDGER & DAILY RECONCILE (PRESERVES sales_channel) */}
      {mainTab === "ledger" && canReadLedger && (
        <div className={styles.sectionStack}>
          <SlidingTabBar
            tabs={CHANNEL_TABS}
            activeId={channelFilter}
            onChange={(id) => void handleSelectChannel(id)}
            ariaLabel="Lọc sổ cái và đối soát theo kênh bán"
            rightSlot={
              <span className={shared.resultCount}>
                {loadingChannel ? "Đang tải..." : `Phạm vi: ${activeScopeLabel}`}
              </span>
            }
          />

          {channelError && <p className={shared.validationError}>{channelError}</p>}
          {runError && <p className={shared.validationError}>{runError}</p>}

          <section className={shared.kpiStrip} aria-label="Tổng hợp sổ cái ghi kép">
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Tổng Ghi có</span>
              <span className={shared.kpiValue}>{formatVnd(totalCredits)}</span>
              <span className={shared.kpiCaption}>Số nguyên VND</span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Tổng Ghi nợ</span>
              <span className={shared.kpiValue}>{formatVnd(totalDebits)}</span>
              <span className={shared.kpiCaption}>Đối ứng ghi kép</span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Chênh lệch Nợ và Có</span>
              <span
                className={`${shared.kpiValue} ${!isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive}`}
              >
                {isBalanced ? "0₫" : formatSignedVnd(diff)}
              </span>
              <span className={shared.kpiCaption}>
                {isBalanced ? "Cân đối tuyệt đối" : "Cần kiểm tra lệch"}
              </span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Tài khoản sổ cái</span>
              <span className={shared.kpiValue}>{formatInt(ledger?.accounts.length ?? 0)}</span>
              <span className={shared.kpiCaption}>Escrow, gian hàng &amp; sàn</span>
            </div>
          </section>

          {report && (
            <section className={shared.panel} aria-labelledby="recon-result-heading">
              <div className={shared.panelHeader}>
                <h2 id="recon-result-heading" className={shared.panelTitle}>
                  Kết quả đối soát {reportDate}
                </h2>
                <span className={shared.panelMeta}>
                  Phiên {report.run_id.slice(0, 12)}, khớp {formatInt(report.matched_count)}, lệch{" "}
                  {formatInt(reportIssuesCount)}
                  {(report.auto_healed_count ?? 0) > 0
                    ? `, tự đồng bộ ${formatInt(report.auto_healed_count ?? 0)}`
                    : ""}
                </span>
              </div>

              {report.items.length === 0 ? (
                <p className={shared.emptyRow}>Không có giao dịch phát sinh ngày {reportDate}.</p>
              ) : (
                <>
                  <div className={shared.tableWrap}>
                    <table className={shared.dataTable}>
                      <thead>
                        <tr>
                          <th>Mã giao dịch nội bộ</th>
                          <th>Mã tham chiếu cổng</th>
                          <th>Kênh bán</th>
                          <th className={shared.colNum}>Nội bộ</th>
                          <th className={shared.colNum}>Sao kê</th>
                          <th>Trạng thái</th>
                          <th>Ghi chú</th>
                        </tr>
                      </thead>
                      <tbody>
                        {report.items.map((item, idx) => {
                          const meta = getReconItemStatusMeta(item.status);
                          const channelMeta = getSalesChannelMeta(item.sales_channel);
                          return (
                            <tr
                              key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}
                              className={shared.rowInteractive}
                              onClick={() =>
                                setDrawerTarget({ kind: "recon", item, runId: report.run_id })
                              }
                            >
                              <td className={shared.cellPrimary}>
                                <button
                                  type="button"
                                  className={shared.rowTriggerBtn}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setDrawerTarget({ kind: "recon", item, runId: report.run_id });
                                  }}
                                >
                                  {item.payment_id || EMPTY_DASH}
                                </button>
                              </td>
                              <td>{item.provider_tx_id || EMPTY_DASH}</td>
                              <td>
                                <span
                                  className={`${shared.channelBadge} ${channelMeta.isStorefront ? shared.channelBadgeStorefront : ""}`}
                                  data-sales-channel={channelMeta.value}
                                >
                                  {channelMeta.label}
                                </span>
                              </td>
                              <td className={shared.colNum}>{formatVnd(item.local_amount_vnd)}</td>
                              <td className={shared.colNum}>
                                {formatVnd(item.provider_amount_vnd)}
                              </td>
                              <td>
                                <StatusPill label={meta.label} tone={meta.tone} />
                              </td>
                              <td>{formatReconciliationNote(item.note) || EMPTY_DASH}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={shared.mobileList}>
                    {report.items.map((item, idx) => {
                      const meta = getReconItemStatusMeta(item.status);
                      const channelMeta = getSalesChannelMeta(item.sales_channel);
                      const itemDiff = item.local_amount_vnd - item.provider_amount_vnd;
                      return (
                        <div
                          key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}
                          className={shared.mobileItem}
                        >
                          <button
                            type="button"
                            className={shared.mobileItemBtn}
                            onClick={() =>
                              setDrawerTarget({ kind: "recon", item, runId: report.run_id })
                            }
                          >
                            <span className={shared.mobileLine1}>
                              <span className={shared.mobileIdentity}>
                                {item.payment_id || item.provider_tx_id || EMPTY_DASH}
                              </span>
                              <span
                                className={`${shared.mobileMetric} ${itemDiff !== 0 ? shared.numNegative : ""}`}
                              >
                                {formatVnd(item.local_amount_vnd)}
                              </span>
                            </span>
                            <span className={shared.mobileLine2}>
                              <span className={shared.mobileMetaText}>
                                {channelMeta.label}, sao kê {formatVnd(item.provider_amount_vnd)}
                              </span>
                              <StatusPill label={meta.label} tone={meta.tone} />
                            </span>
                          </button>
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </section>
          )}

          <div className={shared.toolbar}>
            <div className={shared.toolbarLeft}>
              <div className={shared.searchWrap}>
                <span className={shared.searchIcon}>
                  <IconSearch size={14} />
                </span>
                <input
                  type="search"
                  className={shared.toolbarSearch}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm mã tài khoản sổ cái..."
                  aria-label="Tìm kiếm tài khoản sổ cái"
                />
              </div>

              <select
                className={shared.toolbarSelect}
                value={accountTypeFilter}
                onChange={(e) => setAccountTypeFilter(e.target.value)}
                aria-label="Lọc theo nhóm tài khoản"
              >
                <option value="all">Tất cả nhóm tài khoản</option>
                <option value="ASSET">Tài sản</option>
                <option value="LIABILITY">Công nợ phải trả</option>
                <option value="REVENUE">Doanh thu sàn</option>
                <option value="EXPENSE">Chi phí / hoàn trả</option>
              </select>
            </div>

            <div className={shared.toolbarRight}>
              <span className={shared.resultCount}>
                {formatInt(filteredAccounts.length)} tài khoản
              </span>
            </div>
          </div>

          {filteredAccounts.length === 0 ? (
            <p className={shared.emptyRow}>Chưa có tài khoản sổ cái khớp bộ lọc.</p>
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
                    {filteredAccounts.map((acc) => {
                      const selected =
                        drawerTarget?.kind === "account" &&
                        drawerTarget.account.account_code === acc.account_code;
                      return (
                        <tr
                          key={acc.account_code}
                          className={`${shared.rowInteractive} ${selected ? shared.rowSelected : ""}`}
                          onClick={() => setDrawerTarget({ kind: "account", account: acc })}
                        >
                          <td className={shared.cellPrimary}>
                            <button
                              type="button"
                              className={shared.rowTriggerBtn}
                              onClick={(e) => {
                                e.stopPropagation();
                                setDrawerTarget({ kind: "account", account: acc });
                              }}
                            >
                              {acc.account_code}
                            </button>
                          </td>
                          <td>{getAccountTypeLabelVi(acc.account_type)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                          <td className={shared.colNum}>
                            <strong>{formatVnd(acc.balance_vnd)}</strong>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {filteredAccounts.map((acc) => (
                  <div key={acc.account_code} className={shared.mobileItem}>
                    <button
                      type="button"
                      className={shared.mobileItemBtn}
                      onClick={() => setDrawerTarget({ kind: "account", account: acc })}
                    >
                      <span className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{acc.account_code}</span>
                        <span className={shared.mobileMetric}>{formatVnd(acc.balance_vnd)}</span>
                      </span>
                      <span className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {getAccountTypeLabelVi(acc.account_type)}, Nợ{" "}
                          {formatVnd(acc.debit_total_vnd)}, Có {formatVnd(acc.credit_total_vnd)}
                        </span>
                      </span>
                    </button>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}

      {/* TAB 2: COD CSV IMPORT & RECONCILIATION RUNS HISTORY */}
      {mainTab === "cod" && canReadLedger && (
        <div className={styles.sectionStack}>
          {canWriteRecon && (
            <section className={shared.panel} aria-labelledby="cod-import-heading">
              <div className={shared.panelHeader}>
                <h2 id="cod-import-heading" className={shared.panelTitle}>
                  Nhập tệp CSV đối soát COD từ hãng vận chuyển
                </h2>
                <span className={shared.panelMeta}>Tối đa 2 MiB · 5.000 dòng</span>
              </div>

              <form
                onSubmit={(e) => void handleCodImport(e)}
                className={styles.formGrid}
                noValidate
              >
                <div className={styles.formField}>
                  <label htmlFor="cod-carrier" className={shared.fieldLabel}>
                    Hãng vận chuyển
                  </label>
                  <select
                    id="cod-carrier"
                    className={`${shared.toolbarSelect} ${styles.selectFull}`}
                    value={codCarrier}
                    disabled={codImporting}
                    onChange={(e) => setCodCarrier(e.target.value)}
                  >
                    {carrierOptions.map((c) => (
                      <option key={c.code} value={c.code}>
                        {c.label}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={styles.formField}>
                  <label htmlFor="cod-date" className={shared.fieldLabel}>
                    Ngày đối soát
                  </label>
                  <input
                    id="cod-date"
                    type="date"
                    className={shared.textInput}
                    value={codDate}
                    disabled={codImporting}
                    onChange={(e) => setCodDate(e.target.value)}
                  />
                </div>

                <div className={styles.formField}>
                  <label htmlFor="cod-file" className={shared.fieldLabel}>
                    Tệp CSV (.csv UTF-8)
                  </label>
                  <input
                    id="cod-file"
                    type="file"
                    accept=".csv,text/csv"
                    className={styles.fileInput}
                    disabled={codImporting}
                    onChange={(e) => {
                      setCodFile(e.target.files?.[0] ?? null);
                      setCodError(null);
                    }}
                  />
                </div>

                {codError && (
                  <div className={`${styles.formField} ${styles.formFieldWide}`}>
                    <p className={shared.validationError} role="alert">
                      {codError}
                    </p>
                  </div>
                )}

                <div className={`${styles.formField} ${styles.formFieldWide}`}>
                  <div className={shared.detailButtons}>
                    <Button
                      type="submit"
                      variant="primary"
                      size="sm"
                      shape="app"
                      loading={codImporting}
                    >
                      Tải lên &amp; đối soát COD
                    </Button>
                  </div>
                </div>
              </form>

              {codResult && (
                <div className={shared.detailWell}>
                  <div className={shared.detailGrid}>
                    <div className={shared.detailField}>
                      <span className={shared.detailKey}>Khớp / Tổng dòng</span>
                      <span className={`${shared.detailVal} num`}>
                        {formatInt(codResult.run.matched_count)} /{" "}
                        {formatInt(codResult.run.total_rows)}
                      </span>
                    </div>
                    <div className={shared.detailField}>
                      <span className={shared.detailKey}>Tổng tiền COD khớp</span>
                      <span className={`${shared.detailVal} num`}>
                        {formatVnd(codResult.run.total_matched_vnd)}
                      </span>
                    </div>
                    <div className={shared.detailField}>
                      <span className={shared.detailKey}>Lệch tiền / Thiếu trên sàn</span>
                      <span className={`${shared.detailVal} num`}>
                        {formatInt(codResult.run.amount_mismatch_count)} lệch ·{" "}
                        {formatInt(codResult.run.missing_local_count)} thiếu
                      </span>
                    </div>
                    <div className={shared.detailField}>
                      <span className={shared.detailKey}>Chưa giao / Đã đối soát trước</span>
                      <span className={`${shared.detailVal} num`}>
                        {formatInt(codResult.run.not_delivered_count)} chưa giao ·{" "}
                        {formatInt(codResult.run.already_reconciled_count)} trùng
                      </span>
                    </div>
                  </div>

                  {codResult.items.length > 0 && (
                    <>
                      <div className={shared.tableWrap}>
                        <table className={shared.dataTable}>
                          <thead>
                            <tr>
                              <th>Dòng</th>
                              <th>Mã vận đơn</th>
                              <th className={shared.colNum}>Trên sàn</th>
                              <th className={shared.colNum}>Tệp hãng</th>
                              <th>Trạng thái</th>
                              <th>Ghi chú</th>
                            </tr>
                          </thead>
                          <tbody>
                            {codPaged.slice.map((item, idx) => {
                              const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                                label: item.status,
                                tone: "neutral" as const,
                              };
                              return (
                                <tr key={`${item.tracking_code ?? idx}-${item.line_number ?? idx}`}>
                                  <td className="num">
                                    {item.line_number ?? codPaged.startIndex + idx + 1}
                                  </td>
                                  <td className={shared.cellPrimary}>
                                    {item.tracking_code || item.provider_tx_id || EMPTY_DASH}
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
                                  <td>{formatReconciliationNote(item.note) || EMPTY_DASH}</td>
                                </tr>
                              );
                            })}
                          </tbody>
                        </table>
                      </div>

                      {codPaged.totalPages > 1 && (
                        <div className={styles.paginationRow}>
                          <span className={styles.paginationInfo}>
                            Hiển thị {formatInt(codPaged.startIndex + 1)}–
                            {formatInt(codPaged.endIndex)} / {formatInt(codResult.items.length)}{" "}
                            dòng
                          </span>
                          <div className={styles.paginationBtns}>
                            <Button
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={codPaged.page <= 1}
                              onClick={() => setCodResultPage((p) => Math.max(1, p - 1))}
                            >
                              Trước
                            </Button>
                            <Button
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={codPaged.page >= codPaged.totalPages}
                              onClick={() =>
                                setCodResultPage((p) => Math.min(codPaged.totalPages, p + 1))
                              }
                            >
                              Sau
                            </Button>
                          </div>
                        </div>
                      )}
                    </>
                  )}
                </div>
              )}
            </section>
          )}

          <section className={shared.panel} aria-labelledby="recon-runs-heading">
            <div className={shared.panelHeader}>
              <h2 id="recon-runs-heading" className={shared.panelTitle}>
                Lịch sử phiên đối soát ({formatInt(runsTotal)})
              </h2>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={runsLoading}
                onClick={() => void loadRuns(runsTypeFilter, runsChannelFilter, runsPage)}
              >
                Làm mới
              </Button>
            </div>

            <div className={shared.toolbar}>
              <div className={shared.toolbarLeft}>
                <select
                  className={shared.toolbarSelect}
                  value={runsTypeFilter}
                  onChange={(e) => {
                    const next = e.target.value as "" | "vietqr" | "cod_file";
                    setRunsTypeFilter(next);
                    setRunsPage(1);
                    void loadRuns(next, runsChannelFilter, 1);
                  }}
                  aria-label="Lọc loại phiên đối soát"
                >
                  <option value="">Tất cả loại đối soát</option>
                  <option value="vietqr">VietQR hằng ngày</option>
                  <option value="cod_file">Đối soát COD (CSV)</option>
                </select>
                <select
                  className={shared.toolbarSelect}
                  value={runsChannelFilter}
                  onChange={(e) => {
                    const next = e.target.value as SalesChannelFilter;
                    setRunsChannelFilter(next);
                    setRunsPage(1);
                    void loadRuns(runsTypeFilter, next, 1);
                  }}
                  aria-label="Lọc phiên đối soát theo kênh bán"
                >
                  <option value="">Tất cả kênh bán</option>
                  <option value="MARKETPLACE">Sàn NexusMart</option>
                  <option value="STOREFRONT">Site riêng</option>
                </select>
              </div>
            </div>

            {runsLoading ? (
              <StateView
                kind="loading"
                surface="app"
                title="Đang tải lịch sử đối soát..."
                rows={4}
              />
            ) : runs.length === 0 ? (
              <p className={shared.emptyRow}>Chưa có phiên đối soát nào được ghi nhận.</p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Ngày &amp; loại</th>
                        <th>Đối tác / tệp</th>
                        <th className={shared.colNum}>Khớp / Tổng</th>
                        <th className={shared.colNum}>Mục lệch</th>
                        <th className={shared.colNum}>Tổng khớp</th>
                        <th>Trạng thái</th>
                      </tr>
                    </thead>
                    <tbody>
                      {runs.map((run) => {
                        const issues =
                          run.amount_mismatch_count +
                          run.missing_local_count +
                          run.missing_provider_count +
                          run.not_delivered_count;
                        return (
                          <tr key={run.run_id}>
                            <td>
                              <div className={shared.cellPrimary}>{run.run_date}</div>
                              <div className={shared.cellSub}>
                                {run.run_type === "cod_file" ? "COD CSV" : "VietQR"}
                                {run.created_at
                                  ? ` · ${formatShortTimeDateVi(run.created_at)}`
                                  : ""}
                              </div>
                            </td>
                            <td>
                              <div className={shared.cellPrimary}>
                                {run.run_type === "cod_file"
                                  ? formatCarrierLabel(run.provider)
                                  : run.provider || "VietQR"}
                              </div>
                              {run.file_name && (
                                <div className={shared.cellSub}>{run.file_name}</div>
                              )}
                            </td>
                            <td className={shared.colNum}>
                              {formatInt(run.matched_count)} / {formatInt(run.total_rows)}
                            </td>
                            <td
                              className={`${shared.colNum} ${issues > 0 ? shared.numNegative : ""}`}
                            >
                              {formatInt(issues)}
                            </td>
                            <td className={shared.colNum}>{formatVnd(run.total_matched_vnd)}</td>
                            <td>
                              <StatusPill
                                label={
                                  run.status.toUpperCase() === "COMPLETED" ? "Hoàn tất" : run.status
                                }
                                tone={
                                  run.status.toUpperCase() === "COMPLETED" ? "positive" : "neutral"
                                }
                              />
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {runs.map((run) => (
                    <article key={run.run_id} className={shared.mobileItem}>
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>
                          {run.run_date} · {run.run_type === "cod_file" ? "COD CSV" : "VietQR"}
                        </span>
                        <span className={shared.mobileMetric}>
                          {formatVnd(run.total_matched_vnd)}
                        </span>
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          Khớp {formatInt(run.matched_count)}/{formatInt(run.total_rows)} ·{" "}
                          {run.file_name || run.provider}
                        </span>
                      </div>
                    </article>
                  ))}
                </div>

                {Math.ceil(runsTotal / PAGE_SIZE) > 1 && (
                  <div className={styles.paginationRow}>
                    <span className={styles.paginationInfo}>
                      Trang {formatInt(runsPage)} /{" "}
                      {formatInt(Math.max(1, Math.ceil(runsTotal / PAGE_SIZE)))}
                    </span>
                    <div className={styles.paginationBtns}>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={runsPage <= 1 || runsLoading}
                        onClick={() => {
                          const p = runsPage - 1;
                          setRunsPage(p);
                          void loadRuns(runsTypeFilter, runsChannelFilter, p);
                        }}
                      >
                        Trang trước
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={runsPage >= Math.ceil(runsTotal / PAGE_SIZE) || runsLoading}
                        onClick={() => {
                          const p = runsPage + 1;
                          setRunsPage(p);
                          void loadRuns(runsTypeFilter, runsChannelFilter, p);
                        }}
                      >
                        Trang sau
                      </Button>
                    </div>
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      )}

      {/* TAB 3: MISMATCH RECONCILIATION QUEUE */}
      {mainTab === "queue" && canReadLedger && (
        <div className={styles.sectionStack}>
          <div className={shared.toolbar}>
            <div className={shared.toolbarLeft}>
              <select
                className={shared.toolbarSelect}
                value={queueStatus}
                onChange={(e) => {
                  const next = e.target.value;
                  setQueueStatus(next);
                  setQueuePage(1);
                  void loadQueue(next, 1);
                }}
                aria-label="Lọc trạng thái hàng đợi giao dịch lệch"
              >
                <option value="pending">Chờ xử lý</option>
                <option value="suspended">Đang tạm treo</option>
                <option value="assigned">Đã gán khớp</option>
                <option value="refunded">Đã tạo hoàn tiền</option>
                <option value="">Tất cả trạng thái</option>
              </select>
            </div>
            <div className={shared.toolbarRight}>
              <span className={shared.resultCount}>{formatInt(queueTotal)} mục</span>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={queueLoading}
                onClick={() => void loadQueue(queueStatus, queuePage)}
              >
                Làm mới
              </Button>
            </div>
          </div>

          {queueLoading ? (
            <StateView
              kind="loading"
              surface="app"
              title="Đang tải giao dịch không khớp..."
              rows={4}
            />
          ) : queueItems.length === 0 ? (
            <p className={shared.emptyRow}>Không có giao dịch không khớp trong trạng thái này.</p>
          ) : (
            <>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã tham chiếu / vận đơn</th>
                      <th>Nguyên nhân</th>
                      <th className={shared.colNum}>Kỳ vọng</th>
                      <th className={shared.colNum}>Thực nhận</th>
                      <th>Trạng thái</th>
                      <th className={shared.colAction}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {queueItems.map((item) => {
                      const meta = getReconciliationQueueStatusMeta(item.resolution_status);
                      return (
                        <tr
                          key={item.id}
                          className={shared.rowInteractive}
                          onClick={() => {
                            setQueueAction("ASSIGN");
                            setQueuePaymentId(item.expected_payment_id || "");
                            setQueueNote("");
                            setQueueFormError(null);
                            setDrawerTarget({ kind: "queue", item });
                          }}
                        >
                          <td>
                            <div className={shared.cellPrimary}>
                              {item.tracking_code || item.provider_tx_id || item.id.slice(0, 10)}
                            </div>
                            <div className={shared.cellSub}>
                              {item.provider}
                              {item.order_ref ? ` · ${item.order_ref}` : ""}
                            </div>
                          </td>
                          <td>{formatReconciliationReason(item.reason)}</td>
                          <td className={shared.colNum}>{formatVnd(item.expected_amount_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(item.actual_amount_vnd)}</td>
                          <td>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </td>
                          <td className={shared.colAction}>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              onClick={(e) => {
                                e.stopPropagation();
                                setQueueAction("ASSIGN");
                                setQueuePaymentId(item.expected_payment_id || "");
                                setQueueNote("");
                                setQueueFormError(null);
                                setDrawerTarget({ kind: "queue", item });
                              }}
                            >
                              Chi tiết
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
                  const meta = getReconciliationQueueStatusMeta(item.resolution_status);
                  return (
                    <div key={item.id} className={shared.mobileItem}>
                      <button
                        type="button"
                        className={shared.mobileItemBtn}
                        onClick={() => {
                          setQueueAction("ASSIGN");
                          setQueuePaymentId(item.expected_payment_id || "");
                          setQueueNote("");
                          setQueueFormError(null);
                          setDrawerTarget({ kind: "queue", item });
                        }}
                      >
                        <span className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>
                            {item.tracking_code || item.provider_tx_id || item.id.slice(0, 10)}
                          </span>
                          <span className={shared.mobileMetric}>
                            {formatVnd(item.actual_amount_vnd)}
                          </span>
                        </span>
                        <span className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {formatReconciliationReason(item.reason)}
                          </span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </span>
                      </button>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </div>
      )}

      {/* TAB 4: TWO-STEP REFUNDS (APPROVE / REJECT / SETTLE / MANUAL CREATE) */}
      {mainTab === "refunds" && canReadRefunds && (
        <div className={styles.sectionStack}>
          <div className={shared.toolbar}>
            <div className={shared.toolbarLeft}>
              <select
                className={shared.toolbarSelect}
                value={refundStatus}
                onChange={(e) => {
                  const next = e.target.value;
                  setRefundStatus(next);
                  setRefundsPage(1);
                  void loadRefunds(next, refundOrderFilter, 1);
                }}
                aria-label="Lọc trạng thái lệnh hoàn tiền"
              >
                <option value="">Tất cả trạng thái</option>
                <option value="PENDING_APPROVAL">Chờ duyệt 2 người</option>
                <option value="PENDING">Chờ chuyển khoản</option>
                <option value="MANUAL_REVIEW">Cần xử lý tay</option>
                <option value="DONE">Đã hoàn tiền</option>
                <option value="REJECTED">Từ chối duyệt</option>
                <option value="FAILED">Thất bại</option>
              </select>

              <div className={shared.searchWrap}>
                <span className={shared.searchIcon}>
                  <IconSearch size={14} />
                </span>
                <input
                  type="search"
                  className={shared.toolbarSearch}
                  value={refundOrderFilter}
                  onChange={(e) => setRefundOrderFilter(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      setRefundsPage(1);
                      void loadRefunds(refundStatus, refundOrderFilter, 1);
                    }
                  }}
                  placeholder="Lọc theo mã đơn hàng (UUID)..."
                  aria-label="Lọc lệnh hoàn tiền theo mã đơn hàng"
                />
              </div>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={refundsLoading}
                onClick={() => {
                  setRefundsPage(1);
                  void loadRefunds(refundStatus, refundOrderFilter, 1);
                }}
              >
                Lọc
              </Button>
            </div>
            <div className={shared.toolbarRight}>
              <span className={shared.resultCount}>{formatInt(refundsTotal)} lệnh</span>
            </div>
          </div>

          {refundsLoading ? (
            <StateView
              kind="loading"
              surface="app"
              title="Đang tải danh sách hoàn tiền..."
              rows={4}
            />
          ) : refunds.length === 0 ? (
            <p className={shared.emptyRow}>Chưa có lệnh hoàn tiền nào khớp bộ lọc.</p>
          ) : (
            <>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã lệnh &amp; đơn hàng</th>
                      <th>Lý do</th>
                      <th className={shared.colNum}>Số tiền hoàn</th>
                      <th>Trạng thái</th>
                      <th className={shared.colAction}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {refunds.map((rf) => {
                      const meta = getAdminRefundStatusMeta(rf.status, rf);
                      return (
                        <tr
                          key={rf.refund_id}
                          className={shared.rowInteractive}
                          onClick={() => {
                            setSettleRefId("");
                            setSettleNote("");
                            setApprovalNote("");
                            setRefundFormError(null);
                            setDrawerTarget({ kind: "refund", refund: rf });
                          }}
                        >
                          <td>
                            <div className={shared.cellPrimary}>{rf.refund_id.slice(0, 12)}</div>
                            <div className={shared.cellSub}>
                              Đơn {rf.order_id.slice(0, 10)}
                              {rf.created_at ? ` · ${formatShortTimeDateVi(rf.created_at)}` : ""}
                            </div>
                          </td>
                          <td>{localizeRefundReason(rf.reason) || EMPTY_DASH}</td>
                          <td className={shared.colNum}>
                            <strong>{formatVnd(rf.amount_vnd)}</strong>
                          </td>
                          <td>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </td>
                          <td className={shared.colAction}>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              onClick={(e) => {
                                e.stopPropagation();
                                setSettleRefId("");
                                setSettleNote("");
                                setApprovalNote("");
                                setRefundFormError(null);
                                setDrawerTarget({ kind: "refund", refund: rf });
                              }}
                            >
                              {rf.status === "PENDING_APPROVAL"
                                ? "Duyệt"
                                : isAdminRefundManuallySettleable(rf)
                                  ? "Tất toán"
                                  : "Chi tiết"}
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {refunds.map((rf) => {
                  const meta = getAdminRefundStatusMeta(rf.status, rf);
                  return (
                    <div key={rf.refund_id} className={shared.mobileItem}>
                      <button
                        type="button"
                        className={shared.mobileItemBtn}
                        onClick={() => {
                          setSettleRefId("");
                          setSettleNote("");
                          setApprovalNote("");
                          setRefundFormError(null);
                          setDrawerTarget({ kind: "refund", refund: rf });
                        }}
                      >
                        <span className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{rf.refund_id.slice(0, 12)}</span>
                          <span className={shared.mobileMetric}>{formatVnd(rf.amount_vnd)}</span>
                        </span>
                        <span className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {localizeRefundReason(rf.reason) || rf.order_id.slice(0, 10)}
                          </span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </span>
                      </button>
                    </div>
                  );
                })}
              </div>

              {Math.ceil(refundsTotal / PAGE_SIZE) > 1 && (
                <div className={styles.paginationRow}>
                  <span className={styles.paginationInfo}>
                    Trang {formatInt(refundsPage)} /{" "}
                    {formatInt(Math.max(1, Math.ceil(refundsTotal / PAGE_SIZE)))}
                  </span>
                  <div className={styles.paginationBtns}>
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      disabled={refundsPage <= 1 || refundsLoading}
                      onClick={() => {
                        const p = refundsPage - 1;
                        setRefundsPage(p);
                        void loadRefunds(refundStatus, refundOrderFilter, p);
                      }}
                    >
                      Trang trước
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      disabled={
                        refundsPage >= Math.ceil(refundsTotal / PAGE_SIZE) || refundsLoading
                      }
                      onClick={() => {
                        const p = refundsPage + 1;
                        setRefundsPage(p);
                        void loadRefunds(refundStatus, refundOrderFilter, p);
                      }}
                    >
                      Trang sau
                    </Button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* TAB 5: ORDERS NEEDING MANUAL REVIEW */}
      {mainTab === "needs-review" && canReadOrders && (
        <div className={styles.sectionStack}>
          <div className={shared.toolbar}>
            <div className={shared.toolbarLeft}>
              <span className={styles.helperText}>
                Các đơn phát sinh lỗi hủy vận đơn hoặc cần vận hành rà soát thủ công.
              </span>
            </div>
            <div className={shared.toolbarRight}>
              <span className={shared.resultCount}>{formatInt(needsReviewTotal)} đơn</span>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={needsReviewLoading}
                onClick={() => void loadNeedsReview(needsReviewPage)}
              >
                Làm mới
              </Button>
            </div>
          </div>

          {needsReviewLoading ? (
            <StateView kind="loading" surface="app" title="Đang tải đơn cần rà soát..." rows={4} />
          ) : needsReviewOrders.length === 0 ? (
            <p className={shared.emptyRow}>Hiện không có đơn hàng nào cần rà soát thủ công.</p>
          ) : (
            <>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã đơn</th>
                      <th>Gian hàng &amp; vận đơn</th>
                      <th className={shared.colNum}>Tổng tiền</th>
                      <th>Trạng thái</th>
                      <th className={shared.colAction}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {needsReviewOrders.map((ord) => {
                      const stMeta = getAdminOrderStatusMeta(ord.status);
                      return (
                        <tr
                          key={ord.id}
                          className={shared.rowInteractive}
                          onClick={() => {
                            setOrderReviewNote("");
                            setOrderReviewError(null);
                            setDrawerTarget({ kind: "needs-review", order: ord });
                          }}
                        >
                          <td>
                            <div className={shared.cellPrimary}>{ord.order_code}</div>
                            <div className={shared.cellSub}>
                              {ord.created_at ? formatShortTimeDateVi(ord.created_at) : EMPTY_DASH}
                            </div>
                          </td>
                          <td>
                            <div className={shared.cellPrimary}>Shop {ord.shop_id.slice(0, 8)}</div>
                            <div className={shared.cellSub}>
                              {ord.tracking_code || ord.payment_method}
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
                              onClick={(e) => {
                                e.stopPropagation();
                                setOrderReviewNote("");
                                setOrderReviewError(null);
                                setDrawerTarget({ kind: "needs-review", order: ord });
                              }}
                            >
                              Rà soát
                            </button>
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
                  return (
                    <div key={ord.id} className={shared.mobileItem}>
                      <button
                        type="button"
                        className={shared.mobileItemBtn}
                        onClick={() => {
                          setOrderReviewNote("");
                          setOrderReviewError(null);
                          setDrawerTarget({ kind: "needs-review", order: ord });
                        }}
                      >
                        <span className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{ord.order_code}</span>
                          <span className={shared.mobileMetric}>
                            {formatVnd(ord.total_payable_vnd)}
                          </span>
                        </span>
                        <span className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {ord.tracking_code || `Shop ${ord.shop_id.slice(0, 8)}`}
                          </span>
                          <StatusPill label={stMeta.label} tone={stMeta.tone} />
                        </span>
                      </button>
                    </div>
                  );
                })}
              </div>

              {Math.ceil(needsReviewTotal / PAGE_SIZE) > 1 && (
                <div className={styles.paginationRow}>
                  <span className={styles.paginationInfo}>
                    Trang {formatInt(needsReviewPage)} /{" "}
                    {formatInt(Math.max(1, Math.ceil(needsReviewTotal / PAGE_SIZE)))}
                  </span>
                  <div className={styles.paginationBtns}>
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      disabled={needsReviewPage <= 1 || needsReviewLoading}
                      onClick={() => {
                        const p = needsReviewPage - 1;
                        setNeedsReviewPage(p);
                        void loadNeedsReview(p);
                      }}
                    >
                      Trang trước
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      disabled={
                        needsReviewPage >= Math.ceil(needsReviewTotal / PAGE_SIZE) ||
                        needsReviewLoading
                      }
                      onClick={() => {
                        const p = needsReviewPage + 1;
                        setNeedsReviewPage(p);
                        void loadNeedsReview(p);
                      }}
                    >
                      Trang sau
                    </Button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* UNIFIED DRAWER */}
      <AdminDrawer
        open={Boolean(drawerTarget)}
        onClose={() => setDrawerTarget(null)}
        title={
          drawerTarget?.kind === "account"
            ? drawerTarget.account.account_code
            : drawerTarget?.kind === "recon"
              ? drawerTarget.item.payment_id || "Chi tiết đối soát"
              : drawerTarget?.kind === "queue"
                ? drawerTarget.item.tracking_code ||
                  drawerTarget.item.provider_tx_id ||
                  "Giao dịch không khớp"
                : drawerTarget?.kind === "refund"
                  ? `Hoàn tiền ${formatVnd(drawerTarget.refund.amount_vnd)}`
                  : drawerTarget?.kind === "create-refund"
                    ? "Tạo lệnh hoàn tiền thủ công"
                    : drawerTarget?.kind === "needs-review"
                      ? `Đơn ${drawerTarget.order.order_code}`
                      : ""
        }
        subtitle={
          drawerTarget?.kind === "account"
            ? getAccountTypeLabelVi(drawerTarget.account.account_type)
            : drawerTarget?.kind === "recon"
              ? `Phiên ${drawerTarget.runId.slice(0, 12)}`
              : drawerTarget?.kind === "refund"
                ? `Mã ${drawerTarget.refund.refund_id}`
                : drawerTarget?.kind === "needs-review"
                  ? drawerTarget.order.created_at
                    ? formatDateTimeVi(drawerTarget.order.created_at)
                    : undefined
                  : undefined
        }
        headerBadge={
          drawerTarget?.kind === "recon" ? (
            <StatusPill
              label={getReconItemStatusMeta(drawerTarget.item.status).label}
              tone={getReconItemStatusMeta(drawerTarget.item.status).tone}
            />
          ) : drawerTarget?.kind === "account" ? (
            <StatusPill
              label={isBalanced ? "Cân đối" : "Có chênh lệch"}
              tone={isBalanced ? "positive" : "negative"}
            />
          ) : drawerTarget?.kind === "queue" ? (
            <StatusPill
              label={getReconciliationQueueStatusMeta(drawerTarget.item.resolution_status).label}
              tone={getReconciliationQueueStatusMeta(drawerTarget.item.resolution_status).tone}
            />
          ) : drawerTarget?.kind === "refund" ? (
            <StatusPill
              label={
                getAdminRefundStatusMeta(drawerTarget.refund.status, drawerTarget.refund).label
              }
              tone={getAdminRefundStatusMeta(drawerTarget.refund.status, drawerTarget.refund).tone}
            />
          ) : drawerTarget?.kind === "needs-review" ? (
            <StatusPill
              label={getAdminOrderStatusMeta(drawerTarget.order.status).label}
              tone={getAdminOrderStatusMeta(drawerTarget.order.status).tone}
            />
          ) : undefined
        }
        width="lg"
      >
        {drawerTarget?.kind === "account" && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã tài khoản</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerTarget.account.account_code}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Nhóm tài khoản</span>
                <span className={shared.detailVal}>
                  {getAccountTypeLabelVi(drawerTarget.account.account_type)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Tổng Ghi nợ</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.account.debit_total_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Tổng Ghi có</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.account.credit_total_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số dư ròng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.account.balance_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Phạm vi lọc</span>
                <span className={shared.detailValPlain}>{activeScopeLabel}</span>
              </div>
            </div>
          </div>
        )}

        {drawerTarget?.kind === "recon" && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã giao dịch nội bộ</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerTarget.item.payment_id || EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã tham chiếu cổng</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerTarget.item.provider_tx_id || EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số tiền nội bộ</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.item.local_amount_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số tiền sao kê</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.item.provider_amount_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Kênh bán</span>
                <span className={shared.detailValPlain}>
                  {getSalesChannelMeta(drawerTarget.item.sales_channel).label}
                </span>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Ghi chú</span>
                <span className={shared.detailValPlain}>
                  {formatReconciliationNote(drawerTarget.item.note) || EMPTY_DASH}
                </span>
              </div>
            </div>
          </div>
        )}

        {drawerTarget?.kind === "queue" && (
          <div className={styles.sectionStack}>
            <div className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Đối tác / nguồn</span>
                  <span className={shared.detailValPlain}>
                    {drawerTarget.item.provider}
                    {drawerTarget.item.source_type ? ` (${drawerTarget.item.source_type})` : ""}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Nguyên nhân</span>
                  <span className={shared.detailVal}>
                    {formatReconciliationReason(drawerTarget.item.reason)}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Số tiền kỳ vọng</span>
                  <span className={`${shared.detailVal} num`}>
                    {formatVnd(drawerTarget.item.expected_amount_vnd)}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Số tiền thực nhận</span>
                  <span className={`${shared.detailVal} num`}>
                    {formatVnd(drawerTarget.item.actual_amount_vnd)}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã giao dịch dự kiến</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.item.expected_payment_id || EMPTY_DASH}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Tham chiếu đơn</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.item.order_ref || EMPTY_DASH}
                  </span>
                </div>
                {drawerTarget.item.notes && (
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.detailKey}>Ghi chú</span>
                    <span className={shared.detailValPlain}>
                      {formatReconciliationNote(drawerTarget.item.notes)}
                    </span>
                  </div>
                )}
              </div>
            </div>

            {canWriteRecon &&
              ["pending", "suspended"].includes(
                drawerTarget.item.resolution_status.toLowerCase()
              ) && (
                <form
                  className={styles.drawerSection}
                  onSubmit={(e) => void handleResolveQueueItem(e, drawerTarget.item)}
                  noValidate
                >
                  <h3 className={styles.drawerSectionTitle}>Xử lý giao dịch lệch</h3>
                  <div className={styles.formField}>
                    <label htmlFor="q-action" className={shared.fieldLabel}>
                      Hướng xử lý
                    </label>
                    <select
                      id="q-action"
                      className={`${shared.toolbarSelect} ${styles.selectFull}`}
                      value={queueAction}
                      disabled={queueSubmitting}
                      onChange={(e) => {
                        setQueueAction(e.target.value as GatewayReconciliationAction);
                        setQueueFormError(null);
                      }}
                    >
                      <option value="ASSIGN">Gán khớp vào giao dịch nội bộ (ASSIGN)</option>
                      <option value="REFUND">Tạo lệnh hoàn tiền (REFUND)</option>
                      <option value="SUSPEND">Tạm treo chờ xác minh (SUSPEND)</option>
                    </select>
                  </div>

                  {queueAction === "ASSIGN" && (
                    <div className={styles.formField}>
                      <label htmlFor="q-payment-id" className={shared.fieldLabel}>
                        Mã thanh toán nội bộ (payment_id)
                      </label>
                      <input
                        id="q-payment-id"
                        type="text"
                        className={shared.textInput}
                        value={queuePaymentId}
                        disabled={queueSubmitting}
                        onChange={(e) => {
                          setQueuePaymentId(e.target.value);
                          setQueueFormError(null);
                        }}
                        placeholder="Nhập payment_id cần gán khớp..."
                      />
                    </div>
                  )}

                  <div className={styles.formField}>
                    <label htmlFor="q-note" className={shared.fieldLabel}>
                      Ghi chú xử lý
                    </label>
                    <textarea
                      id="q-note"
                      className={shared.textareaInput}
                      value={queueNote}
                      disabled={queueSubmitting}
                      onChange={(e) => setQueueNote(e.target.value)}
                      placeholder="Ghi rõ lý do gán khớp, hoàn tiền hoặc tạm treo..."
                    />
                  </div>

                  {queueFormError && (
                    <p className={shared.validationError} role="alert">
                      {queueFormError}
                    </p>
                  )}

                  <div className={shared.detailButtons}>
                    <Button
                      type="submit"
                      variant="primary"
                      size="sm"
                      shape="app"
                      loading={queueSubmitting}
                    >
                      Xác nhận xử lý
                    </Button>
                  </div>
                </form>
              )}
          </div>
        )}

        {drawerTarget?.kind === "refund" && (
          <div className={styles.sectionStack}>
            <div className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã đơn hàng</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.refund.order_id}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã thanh toán</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.refund.payment_id}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Người khởi tạo</span>
                  <span className={shared.detailValPlain}>
                    {formatApprovalActorDisplay(drawerTarget.refund.requested_by)}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Người phê duyệt</span>
                  <span className={shared.detailValPlain}>
                    {formatApprovalActorDisplay(drawerTarget.refund.approved_by)}
                  </span>
                </div>
                {drawerTarget.refund.bank_code && (
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.detailKey}>Tài khoản nhận hoàn tiền</span>
                    <span className={shared.detailValPlain}>
                      {drawerTarget.refund.bank_code} ·{" "}
                      {drawerTarget.refund.bank_account_number ||
                        drawerTarget.refund.bank_account_masked ||
                        EMPTY_DASH}
                      {drawerTarget.refund.bank_account_name
                        ? ` (${drawerTarget.refund.bank_account_name})`
                        : ""}
                    </span>
                  </div>
                )}
                {drawerTarget.refund.provider_refund_id && (
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Mã chuyển khoản tất toán</span>
                    <span className={`${shared.detailValPlain} num`}>
                      {drawerTarget.refund.provider_refund_id}
                    </span>
                  </div>
                )}
                {drawerTarget.refund.settled_at && (
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Thời gian tất toán</span>
                    <span className={`${shared.detailValPlain} num`}>
                      {formatDateTimeVi(drawerTarget.refund.settled_at)}
                    </span>
                  </div>
                )}
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Lý do hoàn tiền</span>
                  <span className={shared.detailValPlain}>
                    {localizeRefundReason(drawerTarget.refund.reason) || EMPTY_DASH}
                  </span>
                </div>
                {drawerTarget.refund.approval_note && (
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.detailKey}>Ghi chú duyệt 2 người</span>
                    <span className={shared.detailValPlain}>
                      {drawerTarget.refund.approval_note}
                    </span>
                  </div>
                )}
              </div>
            </div>

            {drawerTarget.refund.status.toUpperCase() === "PENDING_APPROVAL" && (
              <div className={styles.drawerSection}>
                <h3 className={styles.drawerSectionTitle}>Phê duyệt 2 người (Maker–Checker)</h3>
                {canDecideTwoPersonApproval({
                  actorId: user.id,
                  actorRole: user.role,
                  requestedBy: drawerTarget.refund.requested_by,
                  decideCapability: "refunds.approve",
                }) ? (
                  <>
                    <div className={styles.formField}>
                      <label htmlFor="rf-approval-note" className={shared.fieldLabel}>
                        Ghi chú phê duyệt (bắt buộc khi từ chối)
                      </label>
                      <textarea
                        id="rf-approval-note"
                        className={shared.textareaInput}
                        value={approvalNote}
                        disabled={refundBusy}
                        onChange={(e) => {
                          setApprovalNote(e.target.value);
                          setRefundFormError(null);
                        }}
                        placeholder="Ghi chú kiểm tra chứng từ trước khi duyệt hoặc từ chối..."
                      />
                    </div>
                    {refundFormError && (
                      <p className={shared.validationError} role="alert">
                        {refundFormError}
                      </p>
                    )}
                    <div className={shared.detailButtons}>
                      <Button
                        variant="primary"
                        size="sm"
                        shape="app"
                        loading={refundBusy}
                        onClick={() => void handleDecideRefund(drawerTarget.refund, true)}
                      >
                        Phê duyệt hoàn tiền
                      </Button>
                      <Button
                        variant="danger"
                        size="sm"
                        shape="app"
                        loading={refundBusy}
                        onClick={() => void handleDecideRefund(drawerTarget.refund, false)}
                      >
                        Từ chối
                      </Button>
                    </div>
                  </>
                ) : (
                  <div className={styles.warningBanner}>
                    {drawerTarget.refund.requested_by &&
                    drawerTarget.refund.requested_by === user.id
                      ? "Bạn là người khởi tạo lệnh hoàn tiền này; cần quản trị viên hoặc kế toán khác phê duyệt."
                      : "Tài khoản của bạn không có quyền phê duyệt lệnh hoàn tiền 2 người."}
                  </div>
                )}
              </div>
            )}

            {isAdminWalletRefundAutoPending(drawerTarget.refund) && (
              <div className={styles.noticeBanner}>
                Lệnh hoàn tiền về ví ZaloPay đang được cổng thanh toán xử lý tự động; không cần tất
                toán thủ công.
              </div>
            )}

            {isAdminRefundManuallySettleable(drawerTarget.refund) && canSettleRefund && (
              <form
                className={styles.drawerSection}
                onSubmit={(e) => void handleSettleRefund(e, drawerTarget.refund)}
                noValidate
              >
                <h3 className={styles.drawerSectionTitle}>Xác nhận tất toán chuyển khoản</h3>
                <div className={styles.formField}>
                  <label htmlFor="rf-settle-id" className={shared.fieldLabel}>
                    Mã giao dịch chuyển khoản ngân hàng
                  </label>
                  <input
                    id="rf-settle-id"
                    type="text"
                    className={shared.textInput}
                    value={settleRefId}
                    disabled={refundBusy}
                    onChange={(e) => {
                      setSettleRefId(e.target.value);
                      setRefundFormError(null);
                    }}
                    placeholder="VD: FT2611250019283"
                  />
                </div>
                <div className={styles.formField}>
                  <label htmlFor="rf-settle-note" className={shared.fieldLabel}>
                    Ghi chú kế toán (tùy chọn)
                  </label>
                  <input
                    id="rf-settle-note"
                    type="text"
                    className={shared.textInput}
                    value={settleNote}
                    disabled={refundBusy}
                    onChange={(e) => setSettleNote(e.target.value)}
                    placeholder="Đã đối chiếu sao kê chuyển khoản hoàn tiền..."
                  />
                </div>
                {refundFormError && (
                  <p className={shared.validationError} role="alert">
                    {refundFormError}
                  </p>
                )}
                <div className={shared.detailButtons}>
                  <Button
                    type="submit"
                    variant="primary"
                    size="sm"
                    shape="app"
                    loading={refundBusy}
                  >
                    Xác nhận đã chuyển khoản
                  </Button>
                </div>
              </form>
            )}
          </div>
        )}

        {drawerTarget?.kind === "create-refund" && canCreateRefund && (
          <form
            className={styles.drawerSection}
            onSubmit={(e) => void handleCreateManualRefund(e)}
            noValidate
          >
            <div className={styles.formField}>
              <label htmlFor="new-rf-payment" className={shared.fieldLabel}>
                Mã thanh toán (payment_id)
              </label>
              <input
                id="new-rf-payment"
                type="text"
                data-autofocus="true"
                className={shared.textInput}
                value={newRefundPaymentId}
                disabled={creatingRefund}
                onChange={(e) => setNewRefundPaymentId(e.target.value)}
                placeholder="UUID giao dịch thanh toán..."
              />
            </div>
            <div className={styles.formField}>
              <label htmlFor="new-rf-order" className={shared.fieldLabel}>
                Mã đơn hàng (order_id)
              </label>
              <input
                id="new-rf-order"
                type="text"
                className={shared.textInput}
                value={newRefundOrderId}
                disabled={creatingRefund}
                onChange={(e) => setNewRefundOrderId(e.target.value)}
                placeholder="UUID đơn hàng..."
              />
            </div>
            <div className={styles.formField}>
              <label htmlFor="new-rf-amount" className={shared.fieldLabel}>
                Số tiền hoàn (VND)
              </label>
              <input
                id="new-rf-amount"
                type="number"
                min={1}
                step={1}
                className={shared.textInput}
                value={newRefundAmount}
                disabled={creatingRefund}
                onChange={(e) => setNewRefundAmount(e.target.value)}
                placeholder="VD: 199000"
              />
            </div>
            <div className={styles.formField}>
              <label htmlFor="new-rf-reason" className={shared.fieldLabel}>
                Lý do hoàn tiền
              </label>
              <textarea
                id="new-rf-reason"
                className={shared.textareaInput}
                value={newRefundReason}
                disabled={creatingRefund}
                onChange={(e) => setNewRefundReason(e.target.value)}
                placeholder="Ghi rõ căn cứ tạo lệnh hoàn tiền thủ công..."
              />
            </div>
            {createRefundError && (
              <p className={shared.validationError} role="alert">
                {createRefundError}
              </p>
            )}
            <div className={shared.detailButtons}>
              <Button
                type="submit"
                variant="primary"
                size="sm"
                shape="app"
                loading={creatingRefund}
              >
                Tạo lệnh hoàn tiền
              </Button>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={creatingRefund}
                onClick={() => setDrawerTarget(null)}
              >
                Hủy
              </Button>
            </div>
          </form>
        )}

        {drawerTarget?.kind === "needs-review" && (
          <div className={styles.sectionStack}>
            <div className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã đơn hàng</span>
                  <span className={`${shared.detailVal} num`}>{drawerTarget.order.order_code}</span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Tổng thanh toán</span>
                  <span className={`${shared.detailVal} num`}>
                    {formatVnd(drawerTarget.order.total_payable_vnd)}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Phương thức thanh toán</span>
                  <span className={shared.detailValPlain}>
                    {drawerTarget.order.payment_method} ({drawerTarget.order.payment_status})
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã vận đơn</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.order.tracking_code || EMPTY_DASH}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Gian hàng</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.order.shop_id}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Người mua</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {drawerTarget.order.buyer_id}
                  </span>
                </div>
              </div>
            </div>

            {canResolveOrderReview ? (
              <form
                className={styles.drawerSection}
                onSubmit={(e) => void handleResolveOrderReview(e, drawerTarget.order)}
                noValidate
              >
                <h3 className={styles.drawerSectionTitle}>Đánh dấu đã rà soát xong</h3>
                <div className={styles.formField}>
                  <label htmlFor="ord-review-note" className={shared.fieldLabel}>
                    Ghi chú vận hành
                  </label>
                  <textarea
                    id="ord-review-note"
                    className={shared.textareaInput}
                    value={orderReviewNote}
                    disabled={orderReviewBusy}
                    onChange={(e) => setOrderReviewNote(e.target.value)}
                    placeholder="Ghi kết quả kiểm tra vận đơn và phương án đã xử lý..."
                  />
                </div>
                {orderReviewError && (
                  <p className={shared.validationError} role="alert">
                    {orderReviewError}
                  </p>
                )}
                <div className={shared.detailButtons}>
                  <Button
                    type="submit"
                    variant="primary"
                    size="sm"
                    shape="app"
                    loading={orderReviewBusy}
                  >
                    Xác nhận đã xử lý
                  </Button>
                </div>
              </form>
            ) : (
              <p className={styles.helperText}>
                Tài khoản hiện tại chỉ có quyền xem đơn cần rà soát.
              </p>
            )}
          </div>
        )}
      </AdminDrawer>
    </div>
  );
}

export function AdminReconciliationClient(props: AdminReconciliationClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Đối soát & hoàn tiền" />}>
      <AdminStateGate
        title="Đối soát & hoàn tiền"
        emptyTitle="Chưa có dữ liệu đối soát"
        emptyMessage="Sổ cái sẽ hiển thị ngay khi phát sinh giao dịch."
        errorTitle="Không thể tải sổ đối soát"
        errorMessage="Dịch vụ sổ cái thanh toán tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery }) => <ReconciliationContent {...props} initialQuery={initialQuery} />}
      </AdminStateGate>
    </Suspense>
  );
}
