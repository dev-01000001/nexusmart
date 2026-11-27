"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  AlertCircle,
  Banknote,
  ClipboardCheck,
  FileSpreadsheet,
  Info,
  Plus,
  RefreshCw,
  Scale,
  Search,
  Upload,
  Wallet,
} from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  Money,
  PageHeader,
  StatusBadge,
  orderStatusTone,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
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
  GatewaySellerPayout,
  GatewaySellerPayoutDecision,
  GatewaySellerPayoutListResponse,
  GatewaySellerPayoutResponse,
  GatewaySettleRefundResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { getOptionalSalesChannelMeta, type SalesChannelFilter } from "@/lib/salesChannel";
import { cn } from "@/lib/utils";
import { getPaymentMethodLabel, localizeRefundReason } from "@/features/account/orderMeta";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import {
  formatAdminOrderPaymentDisplay,
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
  buildCodCarrierOptions,
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
  buildAdminMoneyFingerprint,
  computePendingPayoutTotalVnd,
  formatEscrowReleaseToastMessage,
  mapEscrowReleaseErrorMessage,
  nextAdminMoneyIdempotencyCacheAfterError,
  resolveAdminMoneyIdempotencyKey,
  type AdminEscrowReleaseResponse,
  type StableReferenceCache,
  validateEscrowReleaseOrderId,
  validateManualRefundInput,
  validateReconciliationQueueResolveInput,
  validateRefundApprovalDecisionInput,
  validateSellerPayoutDecisionInput,
  validateSettleRefundInput,
} from "./AdminReconciliationHelpers";
import {
  DetailGrid,
  DetailItem,
  FilterSelect,
  TableLoadingSkeleton,
  TablePagination,
  pillToneToStatusTone,
  refundStatusTone,
} from "./config/adminUiHelpers";
import { CodCsvImportDialog } from "./finance/CodCsvImportDialog";
import { getSellerPayoutStatusMeta } from "@/features/seller/SellerFinancePayoutHelpers";

export type ReconciliationMainTab =
  "ledger" | "cod" | "queue" | "refunds" | "payouts" | "needs-review";

const CHANNEL_TABS: Array<{ id: SalesChannelFilter; label: string; dataChannelTab: string }> = [
  { id: "", label: "Tất cả kênh", dataChannelTab: "ALL" },
  { id: "MARKETPLACE", label: "Sàn NexusMart", dataChannelTab: "MARKETPLACE" },
  { id: "STOREFRONT", label: "Site riêng", dataChannelTab: "STOREFRONT" },
];

const PAGE_SIZE = 20;
const ALL_REFUND_SENTINEL = "__all_refund__";
const ALL_PAYOUT_SENTINEL = "__all_payout__";

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
  initialPayouts?: GatewaySellerPayout[];
  initialPayoutsTotal?: number;
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
  initialPayouts = [],
  initialPayoutsTotal = 0,
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
  const canReadPayouts = roleHasCapability(user.role, "payouts.read");
  const canDecidePayouts = roleHasCapability(user.role, "payouts.decide");
  const canReleaseEscrow = roleHasCapability(user.role, "escrow.release");
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
    if (canReadPayouts) {
      list.push({ id: "payouts", label: "Chi trả người bán", count: initialPayoutsTotal });
    }
    if (canReadOrders) {
      list.push({ id: "needs-review", label: "Đơn cần rà soát", count: initialNeedsReviewTotal });
    }
    return list;
  }, [
    canReadLedger,
    canReadRefunds,
    canReadPayouts,
    canReadOrders,
    initialQueueTotal,
    initialRefundsTotal,
    initialPayoutsTotal,
    initialNeedsReviewTotal,
  ]);

  const resolvedDefaultTab: ReconciliationMainTab = useMemo(() => {
    if (initialTab && availableTabs.some((t) => t.id === initialTab)) return initialTab;
    return availableTabs[0]?.id ?? "ledger";
  }, [initialTab, availableTabs]);

  const [mainTab, setMainTab] = useState<ReconciliationMainTab>(resolvedDefaultTab);
  const [drawerTarget, setDrawerTarget] = useState<DrawerTarget | null>(null);
  const [codDialogOpen, setCodDialogOpen] = useState(false);

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

  const carrierOptions = useMemo(() => buildCodCarrierOptions(carriers), [carriers]);
  const carrierSelectOptions = useMemo(
    () => carrierOptions.map((c) => ({ value: c.code, label: c.label })),
    [carrierOptions]
  );
  const queueIdemCacheRef = useRef<StableReferenceCache | null>(null);
  const settleIdemCacheRef = useRef<StableReferenceCache | null>(null);
  const refundIdemCacheRef = useRef<StableReferenceCache | null>(null);

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

  // --- Seller payouts queue (UC-PAY05) ---
  const [payouts, setPayouts] = useState<GatewaySellerPayout[]>(initialPayouts);
  const [payoutsTotal, setPayoutsTotal] = useState(initialPayoutsTotal);
  const [payoutsPendingCount, setPayoutsPendingCount] = useState(initialPayoutsTotal);
  const [payoutsPendingTotalVnd, setPayoutsPendingTotalVnd] = useState(() =>
    computePendingPayoutTotalVnd(initialPayouts)
  );
  const [payoutStatus, setPayoutStatus] = useState<string>("PENDING");
  const [payoutShopFilter, setPayoutShopFilter] = useState<string>("");
  const [payoutsPage, setPayoutsPage] = useState(1);
  const [payoutsLoading, setPayoutsLoading] = useState(false);
  const [payoutDecisionTarget, setPayoutDecisionTarget] = useState<{
    payout: GatewaySellerPayout;
    decision: GatewaySellerPayoutDecision;
  } | null>(null);
  const [payoutRejectReason, setPayoutRejectReason] = useState("");
  const [payoutDecisionError, setPayoutDecisionError] = useState<string | null>(null);
  const [payoutDecisionBusy, setPayoutDecisionBusy] = useState(false);

  // --- Early escrow release (escrow.release) ---
  const [escrowOrderId, setEscrowOrderId] = useState("");
  const [escrowFormError, setEscrowFormError] = useState<string | null>(null);
  const [escrowConfirmOrderId, setEscrowConfirmOrderId] = useState<string | null>(null);
  const [escrowReleasing, setEscrowReleasing] = useState(false);

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

  const loadPayouts = useCallback(
    async (nextStatus: string, nextShopId: string, nextPage: number) => {
      if (!canReadPayouts) return;
      setPayoutsLoading(true);
      try {
        const qs = new URLSearchParams({
          page: String(nextPage),
          page_size: String(PAGE_SIZE),
        });
        if (nextStatus) qs.set("status", nextStatus);
        if (nextShopId.trim()) qs.set("shop_id", nextShopId.trim());
        const res = await api<GatewaySellerPayoutListResponse>(
          `/api/admin/payouts?${qs.toString()}`
        );
        const items = res.items ?? [];
        setPayouts(items);
        setPayoutsTotal(res.total ?? items.length);
        if (nextStatus === "PENDING" && !nextShopId.trim()) {
          setPayoutsPendingCount(res.total ?? items.length);
          setPayoutsPendingTotalVnd(computePendingPayoutTotalVnd(items));
        }
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể tải danh sách chi trả người bán."));
      } finally {
        setPayoutsLoading(false);
      }
    },
    [canReadPayouts, showToast]
  );

  const handleConfirmPayoutDecision = async () => {
    if (!payoutDecisionTarget || payoutDecisionBusy) return;
    const { payout, decision } = payoutDecisionTarget;
    const validated = validateSellerPayoutDecisionInput({
      decision,
      reason: decision === "REJECT" ? payoutRejectReason : "",
    });
    if (!validated.ok) {
      setPayoutDecisionError(validated.message);
      return;
    }

    setPayoutDecisionError(null);
    setPayoutDecisionBusy(true);
    try {
      await api<GatewaySellerPayoutResponse>(
        `/api/admin/payouts/${encodeURIComponent(payout.id)}/decision`,
        {
          method: "POST",
          body: validated.value,
        }
      );
      setPayoutDecisionTarget(null);
      setPayoutRejectReason("");
      showToast(
        decision === "APPROVE"
          ? "Đã duyệt chi trả cho người bán."
          : "Đã từ chối yêu cầu rút tiền và hoàn lại số dư khả dụng."
      );
      await loadPayouts(payoutStatus, payoutShopFilter, payoutsPage);
      if (payoutStatus !== "PENDING" || payoutShopFilter.trim()) {
        const pendingRes = await api<GatewaySellerPayoutListResponse>(
          "/api/admin/payouts?status=PENDING&page=1&page_size=100"
        ).catch(() => null);
        if (pendingRes) {
          setPayoutsPendingCount(pendingRes.total ?? pendingRes.items.length);
          setPayoutsPendingTotalVnd(computePendingPayoutTotalVnd(pendingRes.items ?? []));
        }
      }
      if (canReadLedger) {
        const freshLedger = await api<GwLedgerSummary>(ledgerUrlFor(channelFilter)).catch(
          () => null
        );
        if (freshLedger) setLedger(freshLedger);
      }
    } catch (err) {
      setPayoutDecisionError(
        getErrorMessage(err, "Không thể cập nhật quyết định chi trả lúc này.")
      );
    } finally {
      setPayoutDecisionBusy(false);
    }
  };

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
      setCodDialogOpen(false);
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
      const fingerprint = buildAdminMoneyFingerprint("recon-q", {
        item_id: item.id,
        action: validated.value.action,
        payment_id: validated.value.payment_id,
        note: validated.value.note,
      });
      const { key: idempotencyKey, cache } = resolveAdminMoneyIdempotencyKey({
        prefix: "recon-q",
        fingerprint,
        cache: queueIdemCacheRef.current,
      });
      queueIdemCacheRef.current = cache;

      const res = await api<GatewayResolveReconciliationResponse>(
        `/api/admin/reconciliation/queue/${encodeURIComponent(item.id)}/resolve`,
        {
          method: "POST",
          headers: { "Idempotency-Key": idempotencyKey },
          body: validated.value,
        }
      );
      queueIdemCacheRef.current = null;
      setQueueItems((prev) => prev.map((q) => (q.id === item.id ? res.item : q)));
      setDrawerTarget({ kind: "queue", item: res.item });
      setQueueNote("");
      showToast("Đã cập nhật xử lý giao dịch không khớp.");
      void loadQueue(queueStatus, queuePage);
      if (res.refund && canReadRefunds) {
        void loadRefunds(refundStatus, refundOrderFilter, 1);
      }
    } catch (err) {
      queueIdemCacheRef.current = nextAdminMoneyIdempotencyCacheAfterError(
        queueIdemCacheRef.current,
        err
      );
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
      const fingerprint = buildAdminMoneyFingerprint("settle", {
        refund_id: refund.refund_id,
        provider_refund_id: validated.value.provider_refund_id,
        note: validated.value.note,
      });
      const { key: idempotencyKey, cache } = resolveAdminMoneyIdempotencyKey({
        prefix: "settle",
        fingerprint,
        cache: settleIdemCacheRef.current,
      });
      settleIdemCacheRef.current = cache;

      const res = await api<GatewaySettleRefundResponse>(
        `/api/admin/refunds/${encodeURIComponent(refund.refund_id)}/settle`,
        {
          method: "POST",
          headers: { "Idempotency-Key": idempotencyKey },
          body: validated.value,
        }
      );
      settleIdemCacheRef.current = null;
      setRefunds((prev) => prev.map((rf) => (rf.refund_id === refund.refund_id ? res.refund : rf)));
      setDrawerTarget({ kind: "refund", refund: res.refund });
      setSettleRefId("");
      setSettleNote("");
      showToast(`Đã xác nhận tất toán hoàn tiền ${formatVnd(refund.amount_vnd)}.`);
      router.refresh();
    } catch (err) {
      settleIdemCacheRef.current = nextAdminMoneyIdempotencyCacheAfterError(
        settleIdemCacheRef.current,
        err
      );
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
      const fingerprint = buildAdminMoneyFingerprint("refund", {
        payment_id: validated.value.payment_id,
        order_id: validated.value.order_id,
        amount_vnd: validated.value.amount_vnd,
        reason: validated.value.reason,
      });
      const { key: idempotencyKey, cache } = resolveAdminMoneyIdempotencyKey({
        prefix: "refund",
        fingerprint,
        cache: refundIdemCacheRef.current,
      });
      refundIdemCacheRef.current = cache;

      const res = await api<GatewayRefundResponse>("/api/admin/refunds", {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: validated.value,
      });
      refundIdemCacheRef.current = null;
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
      refundIdemCacheRef.current = nextAdminMoneyIdempotencyCacheAfterError(
        refundIdemCacheRef.current,
        err
      );
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

  const handleRequestEscrowRelease = (e: React.FormEvent) => {
    e.preventDefault();
    if (escrowReleasing || !canReleaseEscrow) return;
    const validated = validateEscrowReleaseOrderId(escrowOrderId);
    if (!validated.ok) {
      setEscrowFormError(validated.message);
      return;
    }
    setEscrowFormError(null);
    setEscrowConfirmOrderId(validated.value);
  };

  const handleConfirmEscrowRelease = async () => {
    if (!escrowConfirmOrderId || escrowReleasing || !canReleaseEscrow) return;
    setEscrowReleasing(true);
    setEscrowFormError(null);
    try {
      const res = await api<AdminEscrowReleaseResponse>(
        `/api/admin/orders/${encodeURIComponent(escrowConfirmOrderId)}/escrow-release`,
        {
          method: "POST",
        }
      );
      showToast(formatEscrowReleaseToastMessage(res, formatVnd));
      setEscrowConfirmOrderId(null);
      if (res.released || res.already_released) {
        setEscrowOrderId("");
      }
      if (canReadLedger && res.released && !res.already_released) {
        const freshLedger = await api<GwLedgerSummary>(ledgerUrlFor(channelFilter)).catch(
          () => null
        );
        if (freshLedger) setLedger(freshLedger);
      }
      router.refresh();
    } catch (err) {
      const msg = mapEscrowReleaseErrorMessage(
        err,
        getErrorMessage(err, "Không thể giải ngân sớm cho đơn hàng lúc này.")
      );
      setEscrowFormError(msg);
      setEscrowConfirmOrderId(null);
      showToast(msg);
    } finally {
      setEscrowReleasing(false);
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
        if (t.id === "payouts") return { ...t, count: payoutsPendingCount };
        if (t.id === "needs-review") return { ...t, count: needsReviewTotal };
        return t;
      }),
    [availableTabs, queueTotal, refundsTotal, payoutsPendingCount, needsReviewTotal]
  );

  if (loadError && mainTab === "ledger") {
    return (
      <div className="space-y-5">
        <PageHeader title="Đối soát & hoàn tiền" />
        <ErrorState
          title="Không thể tải sổ cái đối soát"
          description={loadError}
          onRetry={() => router.refresh()}
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

  const openCodDialog = () => {
    setCodError(null);
    setCodDialogOpen(true);
  };

  const headerPrimaryAction =
    mainTab === "ledger" && canWriteRecon ? (
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => void handleRunReconcile(e)}
      >
        <label htmlFor="recon-date" className="sr-only">
          Ngày đối soát
        </label>
        <Input
          id="recon-date"
          type="date"
          value={runDate}
          onChange={(e) => setRunDate(e.target.value)}
          aria-label="Chọn ngày đối soát"
          className="h-8 w-40 bg-card tabular-nums"
        />
        <Button type="submit" size="sm" disabled={running}>
          {running ? "Đang đối soát..." : "Chạy đối soát ngày"}
        </Button>
      </form>
    ) : mainTab === "cod" && canWriteRecon ? (
      <Button type="button" size="sm" onClick={openCodDialog}>
        <Upload className="size-4" aria-hidden="true" />
        <span>Tải CSV đối soát COD</span>
      </Button>
    ) : mainTab === "refunds" && canCreateRefund ? (
      <Button
        type="button"
        size="sm"
        onClick={() => {
          setCreateRefundError(null);
          setDrawerTarget({ kind: "create-refund" });
        }}
      >
        <Plus className="size-4" aria-hidden="true" />
        <span>Tạo lệnh hoàn tiền</span>
      </Button>
    ) : undefined;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Đối soát & hoàn tiền"
        subtitle="Theo dõi sổ cái ghi kép, đối soát VietQR & COD CSV, xử lý giao dịch lệch và hoàn tiền 2 bước."
        primaryAction={headerPrimaryAction}
      />

      <Tabs
        value={mainTab}
        onValueChange={(v) => setMainTab(v as ReconciliationMainTab)}
        className="space-y-4"
      >
        <div className="max-w-full overflow-x-auto pb-0.5">
          <TabsList aria-label="Phân hệ đối soát và hoàn tiền">
            {tabsWithLiveCounts.map((t) => (
              <TabsTrigger key={t.id} value={t.id}>
                <span>{t.label}</span>
                {typeof t.count === "number" ? (
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {formatInt(t.count)}
                  </span>
                ) : null}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>

        {/* TAB 1: LEDGER & DAILY RECONCILE (PRESERVES sales_channel) */}
        {canReadLedger ? (
          <TabsContent value="ledger" className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div
                role="tablist"
                aria-label="Lọc sổ cái và đối soát theo kênh bán"
                className="flex flex-wrap items-center gap-1"
              >
                {CHANNEL_TABS.map((ch) => {
                  const isActive = channelFilter === ch.id;
                  return (
                    <button
                      key={ch.dataChannelTab}
                      type="button"
                      role="tab"
                      aria-selected={isActive}
                      data-channel-tab={ch.dataChannelTab}
                      onClick={() => void handleSelectChannel(ch.id)}
                      className={cn(
                        "inline-flex h-8 items-center rounded-md px-3 text-[13px] font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                        isActive
                          ? "bg-secondary font-semibold text-foreground"
                          : "text-muted-foreground hover:bg-secondary/60 hover:text-foreground"
                      )}
                    >
                      {ch.label}
                    </button>
                  );
                })}
              </div>
              <span className="text-xs text-muted-foreground">
                {loadingChannel ? "Đang tải..." : `Phạm vi: ${activeScopeLabel}`}
              </span>
            </div>

            {channelError ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{channelError}</AlertDescription>
              </Alert>
            ) : null}
            {runError ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{runError}</AlertDescription>
              </Alert>
            ) : null}

            <MetricStrip
              items={[
                {
                  label: "Tổng Ghi có",
                  value: formatVnd(totalCredits),
                  hint: "Số nguyên VND",
                },
                {
                  label: "Tổng Ghi nợ",
                  value: formatVnd(totalDebits),
                  hint: "Đối ứng ghi kép",
                },
                {
                  label: "Chênh lệch Nợ và Có",
                  value: (
                    <span className={isBalanced ? "text-badge-success-ink" : "text-destructive"}>
                      {isBalanced ? "0₫" : formatSignedVnd(diff)}
                    </span>
                  ),
                  hint: isBalanced ? "Cân đối tuyệt đối" : "Cần kiểm tra lệch",
                },
                {
                  label: "Tài khoản sổ cái",
                  value: formatInt(ledger?.accounts.length ?? 0),
                  hint: "Escrow, gian hàng & sàn",
                },
              ]}
            />

            {report ? (
              <section className="space-y-3" aria-labelledby="recon-result-heading">
                <IndexTable
                  toolbar={
                    <div className="flex w-full flex-wrap items-center justify-between gap-2">
                      <h2
                        id="recon-result-heading"
                        className="text-sm font-semibold text-foreground"
                      >
                        Kết quả đối soát {reportDate}
                      </h2>
                      <span className="text-xs text-muted-foreground tabular-nums">
                        Phiên {report.run_id.slice(0, 12)}, khớp {formatInt(report.matched_count)},
                        lệch {formatInt(reportIssuesCount)}
                        {(report.auto_healed_count ?? 0) > 0
                          ? `, tự đồng bộ ${formatInt(report.auto_healed_count ?? 0)}`
                          : ""}
                      </span>
                    </div>
                  }
                >
                  {report.items.length === 0 ? (
                    <p className="p-6 text-center text-[13px] text-muted-foreground">
                      Không có giao dịch phát sinh ngày {reportDate}.
                    </p>
                  ) : (
                    <>
                      <div className="hidden md:block">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Mã giao dịch nội bộ</TableHead>
                              <TableHead>Mã tham chiếu cổng</TableHead>
                              <TableHead>Kênh bán</TableHead>
                              <TableHead className="text-right">Nội bộ</TableHead>
                              <TableHead className="text-right">Sao kê</TableHead>
                              <TableHead>Trạng thái</TableHead>
                              <TableHead>Ghi chú</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {report.items.map((item, idx) => {
                              const meta = getReconItemStatusMeta(item.status);
                              const channelMeta = getOptionalSalesChannelMeta(item.sales_channel);
                              return (
                                <TableRow
                                  key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}
                                  className="cursor-pointer"
                                  onClick={() =>
                                    setDrawerTarget({ kind: "recon", item, runId: report.run_id })
                                  }
                                >
                                  <TableCell className="font-medium tabular-nums">
                                    <button
                                      type="button"
                                      className="text-left focus-visible:underline focus-visible:outline-none"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setDrawerTarget({
                                          kind: "recon",
                                          item,
                                          runId: report.run_id,
                                        });
                                      }}
                                    >
                                      {item.payment_id || EMPTY_DASH}
                                    </button>
                                  </TableCell>
                                  <TableCell className="tabular-nums">
                                    {item.provider_tx_id || EMPTY_DASH}
                                  </TableCell>
                                  <TableCell>
                                    {channelMeta ? (
                                      <Badge
                                        variant={channelMeta.isStorefront ? "info" : "neutral"}
                                        data-sales-channel={channelMeta.value}
                                      >
                                        {channelMeta.label}
                                      </Badge>
                                    ) : (
                                      "—"
                                    )}
                                  </TableCell>
                                  <TableCell className="text-right">
                                    <Money vnd={item.local_amount_vnd} size="sm" />
                                  </TableCell>
                                  <TableCell className="text-right">
                                    <Money vnd={item.provider_amount_vnd} size="sm" />
                                  </TableCell>
                                  <TableCell>
                                    <StatusBadge tone={pillToneToStatusTone(meta.tone)}>
                                      {meta.label}
                                    </StatusBadge>
                                  </TableCell>
                                  <TableCell className="max-w-[240px] whitespace-normal text-muted-foreground">
                                    {formatReconciliationNote(item.note) || EMPTY_DASH}
                                  </TableCell>
                                </TableRow>
                              );
                            })}
                          </TableBody>
                        </Table>
                      </div>

                      <div className="md:hidden">
                        {report.items.map((item, idx) => {
                          const meta = getReconItemStatusMeta(item.status);
                          const channelMeta = getOptionalSalesChannelMeta(item.sales_channel);
                          const itemDiff = item.local_amount_vnd - item.provider_amount_vnd;
                          return (
                            <IndexRowCard
                              key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}
                              title={item.payment_id || item.provider_tx_id || EMPTY_DASH}
                              badge={
                                <StatusBadge tone={pillToneToStatusTone(meta.tone)}>
                                  {meta.label}
                                </StatusBadge>
                              }
                              meta={
                                <>
                                  <span
                                    className={cn(
                                      "font-semibold tabular-nums",
                                      itemDiff !== 0 ? "text-destructive" : "text-foreground"
                                    )}
                                  >
                                    {formatVnd(item.local_amount_vnd)}
                                  </span>
                                  <span>·</span>
                                  <span>{channelMeta ? channelMeta.label : "—"}</span>
                                  <span>·</span>
                                  <span className="tabular-nums">
                                    sao kê {formatVnd(item.provider_amount_vnd)}
                                  </span>
                                </>
                              }
                              onClick={() =>
                                setDrawerTarget({ kind: "recon", item, runId: report.run_id })
                              }
                            />
                          );
                        })}
                      </div>
                    </>
                  )}
                </IndexTable>
              </section>
            ) : null}

            <IndexTable
              toolbar={
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(filteredAccounts.length)} tài khoản
                </span>
              }
            >
              <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
                <div className="relative min-w-0 flex-1 sm:max-w-xs">
                  <Search
                    className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    type="search"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Tìm mã tài khoản sổ cái..."
                    aria-label="Tìm kiếm tài khoản sổ cái"
                    className="h-9 bg-card pl-8"
                  />
                </div>
                <div className="w-full sm:w-52">
                  <FilterSelect
                    value={accountTypeFilter}
                    onValueChange={setAccountTypeFilter}
                    ariaLabel="Lọc theo nhóm tài khoản"
                    options={[
                      { value: "all", label: "Tất cả nhóm tài khoản" },
                      { value: "ASSET", label: "Tài sản" },
                      { value: "LIABILITY", label: "Công nợ phải trả" },
                      { value: "REVENUE", label: "Doanh thu sàn" },
                      { value: "EXPENSE", label: "Chi phí / hoàn trả" },
                    ]}
                  />
                </div>
              </div>

              {filteredAccounts.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={Scale}
                    title="Chưa có tài khoản sổ cái khớp bộ lọc"
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  <div className="hidden md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Mã tài khoản</TableHead>
                          <TableHead>Nhóm tài khoản</TableHead>
                          <TableHead className="text-right">Tổng Ghi nợ</TableHead>
                          <TableHead className="text-right">Tổng Ghi có</TableHead>
                          <TableHead className="text-right">Số dư hiện tại</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {filteredAccounts.map((acc) => {
                          const selected =
                            drawerTarget?.kind === "account" &&
                            drawerTarget.account.account_code === acc.account_code;
                          return (
                            <TableRow
                              key={acc.account_code}
                              data-state={selected ? "selected" : undefined}
                              className="cursor-pointer"
                              onClick={() => setDrawerTarget({ kind: "account", account: acc })}
                            >
                              <TableCell className="font-medium tabular-nums">
                                <button
                                  type="button"
                                  className="text-left focus-visible:underline focus-visible:outline-none"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setDrawerTarget({ kind: "account", account: acc });
                                  }}
                                >
                                  {acc.account_code}
                                </button>
                              </TableCell>
                              <TableCell>{getAccountTypeLabelVi(acc.account_type)}</TableCell>
                              <TableCell className="text-right tabular-nums">
                                {formatVnd(acc.debit_total_vnd)}
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                {formatVnd(acc.credit_total_vnd)}
                              </TableCell>
                              <TableCell className="text-right font-semibold tabular-nums">
                                {formatVnd(acc.balance_vnd)}
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="md:hidden">
                    {filteredAccounts.map((acc) => (
                      <IndexRowCard
                        key={acc.account_code}
                        title={acc.account_code}
                        meta={
                          <>
                            <span className="font-semibold text-foreground tabular-nums">
                              {formatVnd(acc.balance_vnd)}
                            </span>
                            <span>·</span>
                            <span>{getAccountTypeLabelVi(acc.account_type)}</span>
                            <span>·</span>
                            <span className="tabular-nums">
                              Nợ {formatVnd(acc.debit_total_vnd)}, Có{" "}
                              {formatVnd(acc.credit_total_vnd)}
                            </span>
                          </>
                        }
                        onClick={() => setDrawerTarget({ kind: "account", account: acc })}
                      />
                    ))}
                  </div>
                </>
              )}
            </IndexTable>
          </TabsContent>
        ) : null}

        {/* TAB 2: COD CSV IMPORT (DIALOG) & RECONCILIATION RUNS HISTORY */}
        {canReadLedger ? (
          <TabsContent value="cod" className="space-y-4">
            {canWriteRecon ? (
              <Card>
                <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <CardTitle>Đối soát COD từ tệp CSV hãng vận chuyển</CardTitle>
                    <CardDescription>
                      Tải bảng kê thu hộ (.csv UTF-8, tối đa 2 MiB · 5.000 dòng) qua hộp thoại nhập
                      tệp.
                    </CardDescription>
                  </div>
                  <Button type="button" size="sm" onClick={openCodDialog}>
                    <Upload className="size-4" aria-hidden="true" />
                    <span>Nhập tệp CSV COD</span>
                  </Button>
                </CardHeader>
              </Card>
            ) : null}

            {codResult ? (
              <section className="space-y-3" aria-label="Kết quả đối soát COD vừa nhập">
                <MetricStrip
                  items={[
                    {
                      label: "Khớp / Tổng dòng",
                      value: `${formatInt(codResult.run.matched_count)} / ${formatInt(codResult.run.total_rows)}`,
                    },
                    {
                      label: "Tổng tiền COD khớp",
                      value: formatVnd(codResult.run.total_matched_vnd),
                    },
                    {
                      label: "Lệch tiền / Thiếu trên sàn",
                      value: `${formatInt(codResult.run.amount_mismatch_count)} lệch · ${formatInt(codResult.run.missing_local_count)} thiếu`,
                    },
                    {
                      label: "Chưa giao / Đã đối soát trước",
                      value: `${formatInt(codResult.run.not_delivered_count)} chưa giao · ${formatInt(codResult.run.already_reconciled_count)} trùng`,
                    },
                  ]}
                />

                {codResult.items.length > 0 ? (
                  <IndexTable
                    footer={
                      codPaged.totalPages > 1 ? (
                        <TablePagination
                          page={codPaged.page}
                          totalPages={codPaged.totalPages}
                          summary={`Hiển thị ${formatInt(codPaged.startIndex + 1)}–${formatInt(codPaged.endIndex)} / ${formatInt(codResult.items.length)} dòng`}
                          prevLabel="Trước"
                          nextLabel="Sau"
                          onPageChange={(nextPage) => setCodResultPage(nextPage)}
                        />
                      ) : undefined
                    }
                  >
                    <div className="hidden md:block">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>Dòng</TableHead>
                            <TableHead>Mã vận đơn</TableHead>
                            <TableHead className="text-right">Trên sàn</TableHead>
                            <TableHead className="text-right">Tệp hãng</TableHead>
                            <TableHead>Trạng thái</TableHead>
                            <TableHead>Ghi chú</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {codPaged.slice.map((item, idx) => {
                            const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                              label: item.status,
                              tone: "neutral" as const,
                            };
                            return (
                              <TableRow
                                key={`${item.tracking_code ?? idx}-${item.line_number ?? idx}`}
                              >
                                <TableCell className="tabular-nums">
                                  {item.line_number ?? codPaged.startIndex + idx + 1}
                                </TableCell>
                                <TableCell className="font-medium tabular-nums">
                                  {item.tracking_code || item.provider_tx_id || EMPTY_DASH}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  {formatVnd(item.local_amount_vnd)}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  {formatVnd(item.provider_amount_vnd)}
                                </TableCell>
                                <TableCell>
                                  <StatusBadge tone={pillToneToStatusTone(meta.tone)}>
                                    {meta.label}
                                  </StatusBadge>
                                </TableCell>
                                <TableCell className="max-w-[240px] whitespace-normal text-muted-foreground">
                                  {formatReconciliationNote(item.note) || EMPTY_DASH}
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>

                    <div className="md:hidden">
                      {codPaged.slice.map((item, idx) => {
                        const meta = RECONCILE_ITEM_STATUS_META[item.status] ?? {
                          label: item.status,
                          tone: "neutral" as const,
                        };
                        const lineNo = item.line_number ?? codPaged.startIndex + idx + 1;
                        const noteText = formatReconciliationNote(item.note);
                        return (
                          <IndexRowCard
                            key={`${item.tracking_code ?? idx}-${item.line_number ?? idx}`}
                            title={`#${lineNo} · ${item.tracking_code || item.provider_tx_id || EMPTY_DASH}`}
                            badge={
                              <StatusBadge tone={pillToneToStatusTone(meta.tone)}>
                                {meta.label}
                              </StatusBadge>
                            }
                            meta={
                              <>
                                <span className="font-semibold text-foreground tabular-nums">
                                  {formatVnd(item.provider_amount_vnd)}
                                </span>
                                <span>·</span>
                                <span className="tabular-nums">
                                  Sàn {formatVnd(item.local_amount_vnd)}
                                </span>
                                {noteText ? (
                                  <>
                                    <span>·</span>
                                    <span>{noteText}</span>
                                  </>
                                ) : null}
                              </>
                            }
                          />
                        );
                      })}
                    </div>
                  </IndexTable>
                ) : null}
              </section>
            ) : null}

            <section className="space-y-3" aria-labelledby="recon-runs-heading">
              <IndexTable
                toolbar={
                  <div className="flex w-full flex-wrap items-center justify-between gap-2">
                    <h2 id="recon-runs-heading" className="text-sm font-semibold text-foreground">
                      Lịch sử phiên đối soát ({formatInt(runsTotal)})
                    </h2>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      disabled={runsLoading}
                      onClick={() => void loadRuns(runsTypeFilter, runsChannelFilter, runsPage)}
                    >
                      <RefreshCw className="size-3.5" aria-hidden="true" />
                      <span>Làm mới</span>
                    </Button>
                  </div>
                }
                footer={
                  Math.ceil(runsTotal / PAGE_SIZE) > 1 ? (
                    <TablePagination
                      page={runsPage}
                      totalPages={Math.max(1, Math.ceil(runsTotal / PAGE_SIZE))}
                      summary={`Trang ${formatInt(runsPage)} / ${formatInt(Math.max(1, Math.ceil(runsTotal / PAGE_SIZE)))}`}
                      disabled={runsLoading}
                      onPageChange={(nextPage) => {
                        setRunsPage(nextPage);
                        void loadRuns(runsTypeFilter, runsChannelFilter, nextPage);
                      }}
                    />
                  ) : undefined
                }
              >
                <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
                  <div className="w-full sm:w-52">
                    <FilterSelect
                      value={runsTypeFilter}
                      onValueChange={(next) => {
                        const typed = next as "" | "vietqr" | "cod_file";
                        setRunsTypeFilter(typed);
                        setRunsPage(1);
                        void loadRuns(typed, runsChannelFilter, 1);
                      }}
                      ariaLabel="Lọc loại phiên đối soát"
                      options={[
                        { value: "", label: "Tất cả loại đối soát" },
                        { value: "vietqr", label: "VietQR hằng ngày" },
                        { value: "cod_file", label: "Đối soát COD (CSV)" },
                      ]}
                    />
                  </div>
                  <div className="w-full sm:w-48">
                    <FilterSelect
                      value={runsChannelFilter}
                      onValueChange={(next) => {
                        const typed = next as SalesChannelFilter;
                        setRunsChannelFilter(typed);
                        setRunsPage(1);
                        void loadRuns(runsTypeFilter, typed, 1);
                      }}
                      ariaLabel="Lọc phiên đối soát theo kênh bán"
                      options={[
                        { value: "", label: "Tất cả kênh bán" },
                        { value: "MARKETPLACE", label: "Sàn NexusMart" },
                        { value: "STOREFRONT", label: "Site riêng" },
                      ]}
                    />
                  </div>
                </div>

                {runsLoading ? (
                  <div className="p-4">
                    <TableLoadingSkeleton rows={4} label="Đang tải lịch sử đối soát..." />
                  </div>
                ) : runs.length === 0 ? (
                  <div className="p-4">
                    <EmptyState
                      icon={FileSpreadsheet}
                      title="Chưa có phiên đối soát nào được ghi nhận"
                      className="shadow-none"
                    />
                  </div>
                ) : (
                  <>
                    <div className="hidden md:block">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>Ngày &amp; loại</TableHead>
                            <TableHead>Đối tác / tệp</TableHead>
                            <TableHead className="text-right">Khớp / Tổng</TableHead>
                            <TableHead className="text-right">Mục lệch</TableHead>
                            <TableHead className="text-right">Tổng khớp</TableHead>
                            <TableHead>Trạng thái</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {runs.map((run) => {
                            const issues =
                              run.amount_mismatch_count +
                              run.missing_local_count +
                              run.missing_provider_count +
                              run.not_delivered_count;
                            return (
                              <TableRow key={run.run_id}>
                                <TableCell>
                                  <div className="font-medium text-foreground tabular-nums">
                                    {run.run_date}
                                  </div>
                                  <div className="text-xs text-muted-foreground">
                                    {run.run_type === "cod_file" ? "COD CSV" : "VietQR"}
                                    {run.created_at
                                      ? ` · ${formatShortTimeDateVi(run.created_at)}`
                                      : ""}
                                  </div>
                                </TableCell>
                                <TableCell className="whitespace-normal">
                                  <div className="font-medium text-foreground">
                                    {run.run_type === "cod_file"
                                      ? formatCarrierLabel(run.provider)
                                      : run.provider || "VietQR"}
                                  </div>
                                  {run.file_name ? (
                                    <div className="text-xs text-muted-foreground">
                                      {run.file_name}
                                    </div>
                                  ) : null}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  {formatInt(run.matched_count)} / {formatInt(run.total_rows)}
                                </TableCell>
                                <TableCell
                                  className={cn(
                                    "text-right tabular-nums",
                                    issues > 0 && "font-semibold text-destructive"
                                  )}
                                >
                                  {formatInt(issues)}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  {formatVnd(run.total_matched_vnd)}
                                </TableCell>
                                <TableCell>
                                  <StatusBadge
                                    tone={
                                      run.status.toUpperCase() === "COMPLETED"
                                        ? "success"
                                        : "neutral"
                                    }
                                  >
                                    {run.status.toUpperCase() === "COMPLETED"
                                      ? "Hoàn tất"
                                      : run.status}
                                  </StatusBadge>
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>

                    <div className="md:hidden">
                      {runs.map((run) => (
                        <IndexRowCard
                          key={run.run_id}
                          title={`${run.run_date} · ${run.run_type === "cod_file" ? "COD CSV" : "VietQR"}`}
                          badge={
                            <StatusBadge
                              tone={
                                run.status.toUpperCase() === "COMPLETED" ? "success" : "neutral"
                              }
                            >
                              {run.status.toUpperCase() === "COMPLETED" ? "Hoàn tất" : run.status}
                            </StatusBadge>
                          }
                          meta={
                            <>
                              <span className="font-semibold text-foreground tabular-nums">
                                {formatVnd(run.total_matched_vnd)}
                              </span>
                              <span>·</span>
                              <span className="tabular-nums">
                                Khớp {formatInt(run.matched_count)}/{formatInt(run.total_rows)}
                              </span>
                              <span>·</span>
                              <span>{run.file_name || run.provider}</span>
                            </>
                          }
                        />
                      ))}
                    </div>
                  </>
                )}
              </IndexTable>
            </section>
          </TabsContent>
        ) : null}

        {/* TAB 3: MISMATCH RECONCILIATION QUEUE */}
        {canReadLedger ? (
          <TabsContent value="queue" className="space-y-4">
            <IndexTable
              toolbar={
                <div className="flex items-center gap-2">
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {formatInt(queueTotal)} mục
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={queueLoading}
                    onClick={() => void loadQueue(queueStatus, queuePage)}
                  >
                    <RefreshCw className="size-3.5" aria-hidden="true" />
                    <span>Làm mới</span>
                  </Button>
                </div>
              }
            >
              <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
                <div className="w-full sm:w-52">
                  <FilterSelect
                    value={queueStatus}
                    onValueChange={(next) => {
                      setQueueStatus(next);
                      setQueuePage(1);
                      void loadQueue(next, 1);
                    }}
                    ariaLabel="Lọc trạng thái hàng đợi giao dịch lệch"
                    options={[
                      { value: "pending", label: "Chờ xử lý" },
                      { value: "suspended", label: "Đang tạm treo" },
                      { value: "assigned", label: "Đã gán khớp" },
                      { value: "refunded", label: "Đã tạo hoàn tiền" },
                      { value: "", label: "Tất cả trạng thái" },
                    ]}
                  />
                </div>
              </div>

              {queueLoading ? (
                <div className="p-4">
                  <TableLoadingSkeleton rows={4} label="Đang tải giao dịch không khớp..." />
                </div>
              ) : queueItems.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={Scale}
                    title="Không có giao dịch không khớp trong trạng thái này"
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  <div className="hidden md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Mã tham chiếu / vận đơn</TableHead>
                          <TableHead>Nguyên nhân</TableHead>
                          <TableHead className="text-right">Kỳ vọng</TableHead>
                          <TableHead className="text-right">Thực nhận</TableHead>
                          <TableHead>Trạng thái</TableHead>
                          <TableHead className="text-right">Thao tác</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {queueItems.map((item) => {
                          const meta = getReconciliationQueueStatusMeta(item.resolution_status);
                          const openQueueSheet = () => {
                            setQueueAction("ASSIGN");
                            setQueuePaymentId(item.expected_payment_id || "");
                            setQueueNote("");
                            setQueueFormError(null);
                            setDrawerTarget({ kind: "queue", item });
                          };
                          return (
                            <TableRow
                              key={item.id}
                              className="cursor-pointer"
                              onClick={openQueueSheet}
                            >
                              <TableCell className="whitespace-normal">
                                <div className="font-medium text-foreground tabular-nums">
                                  {item.tracking_code ||
                                    item.provider_tx_id ||
                                    item.id.slice(0, 10)}
                                </div>
                                <div className="text-xs text-muted-foreground">
                                  {item.provider}
                                  {item.order_ref ? ` · ${item.order_ref}` : ""}
                                </div>
                              </TableCell>
                              <TableCell className="whitespace-normal">
                                {formatReconciliationReason(item.reason)}
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                {formatVnd(item.expected_amount_vnd)}
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                {formatVnd(item.actual_amount_vnd)}
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={pillToneToStatusTone(meta.tone)}>
                                  {meta.label}
                                </StatusBadge>
                              </TableCell>
                              <TableCell className="text-right">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="xs"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    openQueueSheet();
                                  }}
                                >
                                  Chi tiết
                                </Button>
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="md:hidden">
                    {queueItems.map((item) => {
                      const meta = getReconciliationQueueStatusMeta(item.resolution_status);
                      return (
                        <IndexRowCard
                          key={item.id}
                          title={item.tracking_code || item.provider_tx_id || item.id.slice(0, 10)}
                          badge={
                            <StatusBadge tone={pillToneToStatusTone(meta.tone)}>
                              {meta.label}
                            </StatusBadge>
                          }
                          meta={
                            <>
                              <span className="font-semibold text-foreground tabular-nums">
                                {formatVnd(item.actual_amount_vnd)}
                              </span>
                              <span>·</span>
                              <span>{formatReconciliationReason(item.reason)}</span>
                            </>
                          }
                          onClick={() => {
                            setQueueAction("ASSIGN");
                            setQueuePaymentId(item.expected_payment_id || "");
                            setQueueNote("");
                            setQueueFormError(null);
                            setDrawerTarget({ kind: "queue", item });
                          }}
                        />
                      );
                    })}
                  </div>
                </>
              )}
            </IndexTable>
          </TabsContent>
        ) : null}

        {/* TAB 4: TWO-STEP REFUNDS (APPROVE / REJECT / SETTLE / MANUAL CREATE) */}
        {canReadRefunds ? (
          <TabsContent value="refunds" className="space-y-4">
            <IndexTable
              toolbar={
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(refundsTotal)} lệnh
                </span>
              }
              footer={
                Math.ceil(refundsTotal / PAGE_SIZE) > 1 ? (
                  <TablePagination
                    page={refundsPage}
                    totalPages={Math.max(1, Math.ceil(refundsTotal / PAGE_SIZE))}
                    summary={`Trang ${formatInt(refundsPage)} / ${formatInt(Math.max(1, Math.ceil(refundsTotal / PAGE_SIZE)))}`}
                    disabled={refundsLoading}
                    onPageChange={(nextPage) => {
                      setRefundsPage(nextPage);
                      void loadRefunds(refundStatus, refundOrderFilter, nextPage);
                    }}
                  />
                ) : undefined
              }
            >
              <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
                <div className="w-full sm:w-52">
                  <Select
                    value={refundStatus || ALL_REFUND_SENTINEL}
                    onValueChange={(v) => {
                      const next = v === ALL_REFUND_SENTINEL ? "" : v;
                      setRefundStatus(next);
                      setRefundsPage(1);
                      void loadRefunds(next, refundOrderFilter, 1);
                    }}
                  >
                    <SelectTrigger
                      aria-label="Lọc trạng thái lệnh hoàn tiền"
                      className="w-full bg-card"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={ALL_REFUND_SENTINEL}>Tất cả trạng thái</SelectItem>
                      <SelectItem value="PENDING_APPROVAL">Chờ duyệt 2 người</SelectItem>
                      <SelectItem value="PENDING">Chờ chuyển khoản</SelectItem>
                      <SelectItem value="MANUAL_REVIEW">Cần xử lý tay</SelectItem>
                      <SelectItem value="DONE">Đã hoàn tiền</SelectItem>
                      <SelectItem value="REJECTED">Từ chối duyệt</SelectItem>
                      <SelectItem value="FAILED">Thất bại</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <div className="relative min-w-0 flex-1 sm:max-w-xs">
                  <Search
                    className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    type="search"
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
                    className="h-9 bg-card pl-8"
                  />
                </div>

                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={refundsLoading}
                  onClick={() => {
                    setRefundsPage(1);
                    void loadRefunds(refundStatus, refundOrderFilter, 1);
                  }}
                >
                  Lọc
                </Button>
              </div>

              {refundsLoading ? (
                <div className="p-4">
                  <TableLoadingSkeleton rows={4} label="Đang tải danh sách hoàn tiền..." />
                </div>
              ) : refunds.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={Wallet}
                    title="Chưa có lệnh hoàn tiền nào khớp bộ lọc"
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  <div className="hidden md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Mã lệnh &amp; đơn hàng</TableHead>
                          <TableHead>Lý do</TableHead>
                          <TableHead className="text-right">Số tiền hoàn</TableHead>
                          <TableHead>Trạng thái</TableHead>
                          <TableHead className="text-right">Thao tác</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {refunds.map((rf) => {
                          const meta = getAdminRefundStatusMeta(rf.status, rf);
                          const openRefundSheet = () => {
                            setSettleRefId("");
                            setSettleNote("");
                            setApprovalNote("");
                            setRefundFormError(null);
                            setDrawerTarget({ kind: "refund", refund: rf });
                          };
                          return (
                            <TableRow
                              key={rf.refund_id}
                              className="cursor-pointer"
                              onClick={openRefundSheet}
                            >
                              <TableCell>
                                <div className="font-medium text-foreground tabular-nums">
                                  {rf.refund_id.slice(0, 12)}
                                </div>
                                <div className="text-xs text-muted-foreground tabular-nums">
                                  Đơn {rf.order_id.slice(0, 10)}
                                  {rf.created_at
                                    ? ` · ${formatShortTimeDateVi(rf.created_at)}`
                                    : ""}
                                </div>
                              </TableCell>
                              <TableCell className="max-w-[280px] whitespace-normal">
                                {localizeRefundReason(rf.reason) || EMPTY_DASH}
                              </TableCell>
                              <TableCell className="text-right">
                                <Money vnd={rf.amount_vnd} />
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={refundStatusTone(rf.status)}>
                                  {meta.label}
                                </StatusBadge>
                              </TableCell>
                              <TableCell className="text-right">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="xs"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    openRefundSheet();
                                  }}
                                >
                                  {rf.status === "PENDING_APPROVAL"
                                    ? "Duyệt"
                                    : isAdminRefundManuallySettleable(rf)
                                      ? "Tất toán"
                                      : "Chi tiết"}
                                </Button>
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="md:hidden">
                    {refunds.map((rf) => {
                      const meta = getAdminRefundStatusMeta(rf.status, rf);
                      return (
                        <IndexRowCard
                          key={rf.refund_id}
                          title={rf.refund_id.slice(0, 12)}
                          badge={
                            <StatusBadge tone={refundStatusTone(rf.status)}>
                              {meta.label}
                            </StatusBadge>
                          }
                          meta={
                            <>
                              <Money vnd={rf.amount_vnd} size="sm" />
                              <span>·</span>
                              <span>
                                {localizeRefundReason(rf.reason) || rf.order_id.slice(0, 10)}
                              </span>
                            </>
                          }
                          onClick={() => {
                            setSettleRefId("");
                            setSettleNote("");
                            setApprovalNote("");
                            setRefundFormError(null);
                            setDrawerTarget({ kind: "refund", refund: rf });
                          }}
                        />
                      );
                    })}
                  </div>
                </>
              )}
            </IndexTable>
          </TabsContent>
        ) : null}

        {/* TAB 4B: SELLER PAYOUTS QUEUE (UC-PAY05) */}
        {canReadPayouts ? (
          <TabsContent value="payouts" className="space-y-4">
            <MetricStrip
              items={[
                {
                  label: "Tổng tiền chờ chi",
                  value: <Money vnd={payoutsPendingTotalVnd} size="lg" />,
                  hint: `${formatInt(payoutsPendingCount)} yêu cầu đang chờ duyệt`,
                },
                {
                  label: "Bản ghi theo bộ lọc",
                  value: <span className="tabular-nums">{formatInt(payoutsTotal)}</span>,
                  hint:
                    payoutStatus === "PENDING"
                      ? "Đang lọc trạng thái chờ duyệt"
                      : payoutStatus === "COMPLETED"
                        ? "Đang lọc trạng thái đã chi trả"
                        : payoutStatus === "REJECTED"
                          ? "Đang lọc trạng thái đã từ chối"
                          : "Hiển thị tất cả trạng thái",
                },
              ]}
            />

            <IndexTable
              toolbar={
                <div className="flex w-full flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <FilterSelect
                      ariaLabel="Lọc trạng thái chi trả người bán"
                      value={payoutStatus || ALL_PAYOUT_SENTINEL}
                      onValueChange={(nextVal) => {
                        const next = nextVal === ALL_PAYOUT_SENTINEL ? "" : nextVal;
                        setPayoutStatus(next);
                        setPayoutsPage(1);
                        void loadPayouts(next, payoutShopFilter, 1);
                      }}
                      options={[
                        { value: "PENDING", label: "Chờ duyệt" },
                        { value: "COMPLETED", label: "Đã chi trả" },
                        { value: "REJECTED", label: "Đã từ chối" },
                        { value: ALL_PAYOUT_SENTINEL, label: "Tất cả trạng thái" },
                      ]}
                    />
                    <form
                      className="flex items-center gap-1.5"
                      onSubmit={(e) => {
                        e.preventDefault();
                        setPayoutsPage(1);
                        void loadPayouts(payoutStatus, payoutShopFilter, 1);
                      }}
                    >
                      <Input
                        type="search"
                        value={payoutShopFilter}
                        onChange={(e) => setPayoutShopFilter(e.target.value)}
                        placeholder="Lọc theo mã gian hàng..."
                        aria-label="Lọc chi trả theo mã gian hàng"
                        className="h-8 w-52 bg-card"
                      />
                      <Button type="submit" variant="outline" size="xs" disabled={payoutsLoading}>
                        <Search className="size-3.5" aria-hidden="true" />
                        <span>Lọc</span>
                      </Button>
                    </form>
                  </div>

                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={payoutsLoading}
                    onClick={() => void loadPayouts(payoutStatus, payoutShopFilter, payoutsPage)}
                  >
                    <RefreshCw
                      className={cn("size-3.5", payoutsLoading && "animate-spin")}
                      aria-hidden="true"
                    />
                    <span>Làm mới</span>
                  </Button>
                </div>
              }
              footer={
                Math.ceil(payoutsTotal / PAGE_SIZE) > 1 ? (
                  <TablePagination
                    page={payoutsPage}
                    totalPages={Math.max(1, Math.ceil(payoutsTotal / PAGE_SIZE))}
                    summary={`Trang ${formatInt(payoutsPage)} / ${formatInt(Math.max(1, Math.ceil(payoutsTotal / PAGE_SIZE)))}`}
                    disabled={payoutsLoading}
                    onPageChange={(nextPage) => {
                      setPayoutsPage(nextPage);
                      void loadPayouts(payoutStatus, payoutShopFilter, nextPage);
                    }}
                  />
                ) : undefined
              }
            >
              {payoutsLoading ? (
                <div className="p-4">
                  <TableLoadingSkeleton rows={4} label="Đang tải danh sách chi trả người bán..." />
                </div>
              ) : payouts.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={Banknote}
                    title="Không có yêu cầu chi trả nào trong bộ lọc này"
                    description="Chuyển trạng thái lọc hoặc chờ người bán gửi yêu cầu rút tiền mới."
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  <div className="hidden md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Thời gian</TableHead>
                          <TableHead>Gian hàng</TableHead>
                          <TableHead className="text-right">Số tiền</TableHead>
                          <TableHead>Tài khoản nhận</TableHead>
                          <TableHead>Trạng thái</TableHead>
                          <TableHead>Mã chuyển / Lý do</TableHead>
                          <TableHead className="text-right">Thao tác</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {payouts.map((po) => {
                          const stMeta = getSellerPayoutStatusMeta(po.status);
                          const isPending = po.status.toUpperCase() === "PENDING";
                          const isRejected = po.status.toUpperCase() === "REJECTED";
                          return (
                            <TableRow key={po.id}>
                              <TableCell className="text-muted-foreground tabular-nums">
                                <div>
                                  {po.created_at
                                    ? formatShortTimeDateVi(po.created_at)
                                    : EMPTY_DASH}
                                </div>
                                {po.decided_at ? (
                                  <div className="text-xs text-muted-foreground">
                                    Duyệt: {formatShortTimeDateVi(po.decided_at)}
                                  </div>
                                ) : null}
                              </TableCell>
                              <TableCell>
                                <div className="font-medium text-foreground tabular-nums">
                                  {po.shop_id}
                                </div>
                              </TableCell>
                              <TableCell className="text-right font-semibold tabular-nums">
                                <Money vnd={po.amount_vnd} size="sm" />
                              </TableCell>
                              <TableCell className="whitespace-normal">
                                <div className="font-medium text-foreground">
                                  {po.bank_name || EMPTY_DASH} ·{" "}
                                  <span className="tabular-nums">
                                    {po.bank_account_masked || EMPTY_DASH}
                                  </span>
                                </div>
                                {po.account_holder_name ? (
                                  <div className="text-xs text-muted-foreground">
                                    {po.account_holder_name}
                                  </div>
                                ) : null}
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={stMeta.tone}>{stMeta.label}</StatusBadge>
                              </TableCell>
                              <TableCell className="max-w-[240px] whitespace-normal">
                                {isRejected ? (
                                  <span className="text-destructive">
                                    {po.reject_reason || EMPTY_DASH}
                                  </span>
                                ) : po.transfer_ref ? (
                                  <span className="text-xs text-muted-foreground tabular-nums">
                                    {po.transfer_ref}
                                  </span>
                                ) : (
                                  <span className="text-muted-foreground">{EMPTY_DASH}</span>
                                )}
                              </TableCell>
                              <TableCell className="text-right">
                                {isPending && canDecidePayouts ? (
                                  <div className="flex justify-end gap-1.5">
                                    <Button
                                      type="button"
                                      size="xs"
                                      onClick={() => {
                                        setPayoutRejectReason("");
                                        setPayoutDecisionError(null);
                                        setPayoutDecisionTarget({
                                          payout: po,
                                          decision: "APPROVE",
                                        });
                                      }}
                                    >
                                      Duyệt
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      onClick={() => {
                                        setPayoutRejectReason("");
                                        setPayoutDecisionError(null);
                                        setPayoutDecisionTarget({
                                          payout: po,
                                          decision: "REJECT",
                                        });
                                      }}
                                    >
                                      Từ chối
                                    </Button>
                                  </div>
                                ) : (
                                  <span className="text-xs text-muted-foreground">
                                    {EMPTY_DASH}
                                  </span>
                                )}
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="md:hidden">
                    {payouts.map((po) => {
                      const stMeta = getSellerPayoutStatusMeta(po.status);
                      const isPending = po.status.toUpperCase() === "PENDING";
                      return (
                        <div key={po.id} className="border-b border-border last:border-b-0">
                          <IndexRowCard
                            title={formatVnd(po.amount_vnd)}
                            badge={<StatusBadge tone={stMeta.tone}>{stMeta.label}</StatusBadge>}
                            meta={
                              <>
                                <span>{po.shop_id}</span>
                                <span>·</span>
                                <span>
                                  {po.bank_name || EMPTY_DASH} {po.bank_account_masked}
                                </span>
                                {po.reject_reason ? (
                                  <>
                                    <span>·</span>
                                    <span className="text-destructive">{po.reject_reason}</span>
                                  </>
                                ) : po.transfer_ref ? (
                                  <>
                                    <span>·</span>
                                    <span className="tabular-nums">{po.transfer_ref}</span>
                                  </>
                                ) : null}
                              </>
                            }
                          />
                          {isPending && canDecidePayouts ? (
                            <div className="flex gap-2 px-4 pb-3">
                              <Button
                                type="button"
                                size="xs"
                                onClick={() => {
                                  setPayoutRejectReason("");
                                  setPayoutDecisionError(null);
                                  setPayoutDecisionTarget({ payout: po, decision: "APPROVE" });
                                }}
                              >
                                Duyệt
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="xs"
                                onClick={() => {
                                  setPayoutRejectReason("");
                                  setPayoutDecisionError(null);
                                  setPayoutDecisionTarget({ payout: po, decision: "REJECT" });
                                }}
                              >
                                Từ chối
                              </Button>
                            </div>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </IndexTable>
          </TabsContent>
        ) : null}

        {/* TAB 5: ORDERS NEEDING MANUAL REVIEW */}
        {canReadOrders ? (
          <TabsContent value="needs-review" className="space-y-4">
            <IndexTable
              toolbar={
                <div className="flex w-full flex-wrap items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">
                    Các đơn phát sinh lỗi hủy vận đơn hoặc cần vận hành rà soát thủ công.
                  </span>
                  <div className="flex items-center gap-2">
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {formatInt(needsReviewTotal)} đơn
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      disabled={needsReviewLoading}
                      onClick={() => void loadNeedsReview(needsReviewPage)}
                    >
                      <RefreshCw className="size-3.5" aria-hidden="true" />
                      <span>Làm mới</span>
                    </Button>
                  </div>
                </div>
              }
              footer={
                Math.ceil(needsReviewTotal / PAGE_SIZE) > 1 ? (
                  <TablePagination
                    page={needsReviewPage}
                    totalPages={Math.max(1, Math.ceil(needsReviewTotal / PAGE_SIZE))}
                    summary={`Trang ${formatInt(needsReviewPage)} / ${formatInt(Math.max(1, Math.ceil(needsReviewTotal / PAGE_SIZE)))}`}
                    disabled={needsReviewLoading}
                    onPageChange={(nextPage) => {
                      setNeedsReviewPage(nextPage);
                      void loadNeedsReview(nextPage);
                    }}
                  />
                ) : undefined
              }
            >
              {needsReviewLoading ? (
                <div className="p-4">
                  <TableLoadingSkeleton rows={4} label="Đang tải đơn cần rà soát..." />
                </div>
              ) : needsReviewOrders.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={ClipboardCheck}
                    title="Hiện không có đơn hàng nào cần rà soát thủ công"
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  <div className="hidden md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Mã đơn</TableHead>
                          <TableHead>Gian hàng &amp; vận đơn</TableHead>
                          <TableHead className="text-right">Tổng tiền</TableHead>
                          <TableHead>Trạng thái</TableHead>
                          <TableHead className="text-right">Thao tác</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {needsReviewOrders.map((ord) => {
                          const stMeta = getAdminOrderStatusMeta(ord.status);
                          const openOrderSheet = () => {
                            setOrderReviewNote("");
                            setOrderReviewError(null);
                            setDrawerTarget({ kind: "needs-review", order: ord });
                          };
                          return (
                            <TableRow
                              key={ord.id}
                              className="cursor-pointer"
                              onClick={openOrderSheet}
                            >
                              <TableCell>
                                <div className="font-medium text-foreground tabular-nums">
                                  {ord.order_code}
                                </div>
                                <div className="text-xs text-muted-foreground tabular-nums">
                                  {ord.created_at
                                    ? formatShortTimeDateVi(ord.created_at)
                                    : EMPTY_DASH}
                                </div>
                              </TableCell>
                              <TableCell>
                                <div className="font-medium text-foreground">
                                  Shop {ord.shop_id.slice(0, 8)}
                                </div>
                                <div className="text-xs text-muted-foreground">
                                  {ord.tracking_code || getPaymentMethodLabel(ord.payment_method)}
                                </div>
                              </TableCell>
                              <TableCell className="text-right">
                                <Money vnd={ord.total_payable_vnd} size="sm" />
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={orderStatusTone(ord.status)}>
                                  {stMeta.label}
                                </StatusBadge>
                              </TableCell>
                              <TableCell className="text-right">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="xs"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    openOrderSheet();
                                  }}
                                >
                                  Rà soát
                                </Button>
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="md:hidden">
                    {needsReviewOrders.map((ord) => {
                      const stMeta = getAdminOrderStatusMeta(ord.status);
                      return (
                        <IndexRowCard
                          key={ord.id}
                          title={ord.order_code}
                          badge={
                            <StatusBadge tone={orderStatusTone(ord.status)}>
                              {stMeta.label}
                            </StatusBadge>
                          }
                          meta={
                            <>
                              <Money vnd={ord.total_payable_vnd} size="sm" />
                              <span>·</span>
                              <span>{ord.tracking_code || `Shop ${ord.shop_id.slice(0, 8)}`}</span>
                            </>
                          }
                          onClick={() => {
                            setOrderReviewNote("");
                            setOrderReviewError(null);
                            setDrawerTarget({ kind: "needs-review", order: ord });
                          }}
                        />
                      );
                    })}
                  </div>
                </>
              )}
            </IndexTable>
          </TabsContent>
        ) : null}
      </Tabs>

      {/* EARLY ESCROW RELEASE CARD (escrow.release) */}
      {canReleaseEscrow ? (
        <Card>
          <CardHeader>
            <CardTitle>
              <h2 className="text-sm leading-5 font-semibold">Giải ngân sớm</h2>
            </CardTitle>
            <CardDescription>
              Chuyển ngay tiền ký quỹ của đơn hàng đã hoàn tất vào số dư khả dụng của người bán mà
              không chờ hết thời gian giữ tiền.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="flex flex-col gap-3 sm:flex-row sm:items-end"
              onSubmit={handleRequestEscrowRelease}
              noValidate
            >
              <Field
                className="flex-1 gap-1.5"
                data-invalid={Boolean(escrowFormError) || undefined}
              >
                <FieldLabel htmlFor="admin-escrow-order-id">Mã đơn hàng</FieldLabel>
                <Input
                  id="admin-escrow-order-id"
                  type="text"
                  value={escrowOrderId}
                  disabled={escrowReleasing}
                  onChange={(e) => {
                    setEscrowOrderId(e.target.value);
                    if (escrowFormError) setEscrowFormError(null);
                  }}
                  placeholder="Nhập mã đơn hàng (UUID)..."
                  className="bg-card tabular-nums"
                />
              </Field>
              <Button
                type="submit"
                size="sm"
                disabled={escrowReleasing}
                className="min-h-10 shrink-0 sm:min-h-9"
              >
                {escrowReleasing ? "Đang giải ngân..." : "Giải ngân"}
              </Button>
            </form>
            {escrowFormError ? <FieldError className="mt-2">{escrowFormError}</FieldError> : null}
          </CardContent>
        </Card>
      ) : null}

      {/* EARLY ESCROW RELEASE CONFIRMATION ALERT DIALOG */}
      <AlertDialog
        open={Boolean(escrowConfirmOrderId)}
        onOpenChange={(open) => {
          if (!open && !escrowReleasing) {
            setEscrowConfirmOrderId(null);
          }
        }}
      >
        <AlertDialogContent>
          {escrowConfirmOrderId ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Xác nhận giải ngân sớm</AlertDialogTitle>
                <AlertDialogDescription>
                  Tiền đơn hàng{" "}
                  <span className="font-medium text-foreground tabular-nums">
                    {escrowConfirmOrderId}
                  </span>{" "}
                  sẽ chuyển ngay vào số dư khả dụng của người bán, không chờ hết thời gian giữ tiền.
                  Thao tác này không thể hoàn tác.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel disabled={escrowReleasing}>Hủy</AlertDialogCancel>
                <AlertDialogAction
                  disabled={escrowReleasing}
                  onClick={(e) => {
                    e.preventDefault();
                    void handleConfirmEscrowRelease();
                  }}
                >
                  {escrowReleasing ? "Đang giải ngân..." : "Xác nhận giải ngân"}
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : null}
        </AlertDialogContent>
      </AlertDialog>

      {/* COD CSV IMPORT DIALOG */}
      <CodCsvImportDialog
        open={codDialogOpen}
        onOpenChange={setCodDialogOpen}
        carrierOptions={carrierSelectOptions}
        codCarrier={codCarrier}
        onCodCarrierChange={setCodCarrier}
        codDate={codDate}
        onCodDateChange={setCodDate}
        codFile={codFile}
        onCodFileChange={(file) => {
          setCodFile(file);
          setCodError(null);
        }}
        codImporting={codImporting}
        codError={codError}
        onSubmit={(e) => void handleCodImport(e)}
      />

      {/* SELLER PAYOUT DECISION CONFIRMATION ALERT DIALOG */}
      <AlertDialog
        open={Boolean(payoutDecisionTarget)}
        onOpenChange={(open) => {
          if (!open && !payoutDecisionBusy) {
            setPayoutDecisionTarget(null);
            setPayoutDecisionError(null);
          }
        }}
      >
        <AlertDialogContent>
          {payoutDecisionTarget ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  {payoutDecisionTarget.decision === "APPROVE"
                    ? "Xác nhận duyệt chi trả người bán"
                    : "Xác nhận từ chối yêu cầu rút tiền"}
                </AlertDialogTitle>
                <AlertDialogDescription>
                  {payoutDecisionTarget.decision === "APPROVE"
                    ? `Xác nhận chi trả ${formatVnd(payoutDecisionTarget.payout.amount_vnd)} tới tài khoản ${payoutDecisionTarget.payout.bank_name} (${payoutDecisionTarget.payout.bank_account_masked} — ${payoutDecisionTarget.payout.account_holder_name}) của gian hàng ${payoutDecisionTarget.payout.shop_id}.`
                    : `Từ chối yêu cầu rút ${formatVnd(payoutDecisionTarget.payout.amount_vnd)} của gian hàng ${payoutDecisionTarget.payout.shop_id}. Số tiền tạm giữ sẽ được hoàn lại vào số dư khả dụng của người bán.`}
                </AlertDialogDescription>
              </AlertDialogHeader>

              {payoutDecisionTarget.decision === "REJECT" ? (
                <FieldGroup className="gap-2">
                  <Field data-invalid={Boolean(payoutDecisionError) || undefined}>
                    <FieldLabel htmlFor="admin-payout-reject-reason">Lý do từ chối</FieldLabel>
                    <Textarea
                      id="admin-payout-reject-reason"
                      rows={3}
                      value={payoutRejectReason}
                      disabled={payoutDecisionBusy}
                      onChange={(e) => {
                        setPayoutRejectReason(e.target.value);
                        setPayoutDecisionError(null);
                      }}
                      placeholder="Nhập lý do từ chối để hiển thị cho người bán..."
                      className="bg-card"
                    />
                    {payoutDecisionError ? <FieldError>{payoutDecisionError}</FieldError> : null}
                  </Field>
                </FieldGroup>
              ) : payoutDecisionError ? (
                <FieldError>{payoutDecisionError}</FieldError>
              ) : null}

              <AlertDialogFooter>
                <AlertDialogCancel disabled={payoutDecisionBusy}>Hủy</AlertDialogCancel>
                <AlertDialogAction
                  variant={payoutDecisionTarget.decision === "REJECT" ? "destructive" : "default"}
                  disabled={payoutDecisionBusy}
                  onClick={(e) => {
                    e.preventDefault();
                    void handleConfirmPayoutDecision();
                  }}
                >
                  {payoutDecisionBusy
                    ? "Đang xử lý..."
                    : payoutDecisionTarget.decision === "APPROVE"
                      ? "Xác nhận duyệt"
                      : "Xác nhận từ chối"}
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : null}
        </AlertDialogContent>
      </AlertDialog>

      {/* UNIFIED RIGHT SHEET */}
      <Sheet open={Boolean(drawerTarget)} onOpenChange={(open) => !open && setDrawerTarget(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {drawerTarget ? (
            <>
              <SheetHeader className="border-b border-border pb-4">
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>
                    {drawerTarget.kind === "account"
                      ? drawerTarget.account.account_code
                      : drawerTarget.kind === "recon"
                        ? drawerTarget.item.payment_id || "Chi tiết đối soát"
                        : drawerTarget.kind === "queue"
                          ? drawerTarget.item.tracking_code ||
                            drawerTarget.item.provider_tx_id ||
                            "Giao dịch không khớp"
                          : drawerTarget.kind === "refund"
                            ? `Hoàn tiền ${formatVnd(drawerTarget.refund.amount_vnd)}`
                            : drawerTarget.kind === "create-refund"
                              ? "Tạo lệnh hoàn tiền thủ công"
                              : `Đơn ${drawerTarget.order.order_code}`}
                  </SheetTitle>

                  {drawerTarget.kind === "recon" ? (
                    <StatusBadge
                      tone={pillToneToStatusTone(
                        getReconItemStatusMeta(drawerTarget.item.status).tone
                      )}
                    >
                      {getReconItemStatusMeta(drawerTarget.item.status).label}
                    </StatusBadge>
                  ) : drawerTarget.kind === "account" ? (
                    <StatusBadge tone={isBalanced ? "success" : "critical"}>
                      {isBalanced ? "Cân đối" : "Có chênh lệch"}
                    </StatusBadge>
                  ) : drawerTarget.kind === "queue" ? (
                    <StatusBadge
                      tone={pillToneToStatusTone(
                        getReconciliationQueueStatusMeta(drawerTarget.item.resolution_status).tone
                      )}
                    >
                      {getReconciliationQueueStatusMeta(drawerTarget.item.resolution_status).label}
                    </StatusBadge>
                  ) : drawerTarget.kind === "refund" ? (
                    <StatusBadge tone={refundStatusTone(drawerTarget.refund.status)}>
                      {
                        getAdminRefundStatusMeta(drawerTarget.refund.status, drawerTarget.refund)
                          .label
                      }
                    </StatusBadge>
                  ) : drawerTarget.kind === "needs-review" ? (
                    <StatusBadge tone={orderStatusTone(drawerTarget.order.status)}>
                      {getAdminOrderStatusMeta(drawerTarget.order.status).label}
                    </StatusBadge>
                  ) : null}
                </div>

                {drawerTarget.kind === "account" ? (
                  <SheetDescription>
                    {getAccountTypeLabelVi(drawerTarget.account.account_type)}
                  </SheetDescription>
                ) : drawerTarget.kind === "recon" ? (
                  <SheetDescription className="tabular-nums">
                    Phiên {drawerTarget.runId.slice(0, 12)}
                  </SheetDescription>
                ) : drawerTarget.kind === "refund" ? (
                  <SheetDescription className="tabular-nums">
                    Mã {drawerTarget.refund.refund_id}
                  </SheetDescription>
                ) : drawerTarget.kind === "needs-review" && drawerTarget.order.created_at ? (
                  <SheetDescription className="tabular-nums">
                    {formatDateTimeVi(drawerTarget.order.created_at)}
                  </SheetDescription>
                ) : null}
              </SheetHeader>

              <div className="space-y-4 px-4 pb-6">
                {drawerTarget.kind === "account" ? (
                  <DetailGrid>
                    <DetailItem label="Mã tài khoản" numeric>
                      {drawerTarget.account.account_code}
                    </DetailItem>
                    <DetailItem label="Nhóm tài khoản">
                      {getAccountTypeLabelVi(drawerTarget.account.account_type)}
                    </DetailItem>
                    <DetailItem label="Tổng Ghi nợ" numeric>
                      {formatVnd(drawerTarget.account.debit_total_vnd)}
                    </DetailItem>
                    <DetailItem label="Tổng Ghi có" numeric>
                      {formatVnd(drawerTarget.account.credit_total_vnd)}
                    </DetailItem>
                    <DetailItem label="Số dư ròng" numeric>
                      {formatVnd(drawerTarget.account.balance_vnd)}
                    </DetailItem>
                    <DetailItem label="Phạm vi lọc">{activeScopeLabel}</DetailItem>
                  </DetailGrid>
                ) : null}

                {drawerTarget.kind === "recon" ? (
                  <DetailGrid>
                    <DetailItem label="Mã giao dịch nội bộ" numeric>
                      {drawerTarget.item.payment_id || EMPTY_DASH}
                    </DetailItem>
                    <DetailItem label="Mã tham chiếu cổng" numeric>
                      {drawerTarget.item.provider_tx_id || EMPTY_DASH}
                    </DetailItem>
                    <DetailItem label="Số tiền nội bộ" numeric>
                      {formatVnd(drawerTarget.item.local_amount_vnd)}
                    </DetailItem>
                    <DetailItem label="Số tiền sao kê" numeric>
                      {formatVnd(drawerTarget.item.provider_amount_vnd)}
                    </DetailItem>
                    <DetailItem label="Kênh bán">
                      {getOptionalSalesChannelMeta(drawerTarget.item.sales_channel)?.label ?? "—"}
                    </DetailItem>
                    <DetailItem label="Ghi chú" wide>
                      {formatReconciliationNote(drawerTarget.item.note) || EMPTY_DASH}
                    </DetailItem>
                  </DetailGrid>
                ) : null}

                {drawerTarget.kind === "queue" ? (
                  <>
                    <DetailGrid>
                      <DetailItem label="Đối tác / nguồn">
                        {drawerTarget.item.provider}
                        {drawerTarget.item.source_type ? ` (${drawerTarget.item.source_type})` : ""}
                      </DetailItem>
                      <DetailItem label="Nguyên nhân">
                        {formatReconciliationReason(drawerTarget.item.reason)}
                      </DetailItem>
                      <DetailItem label="Số tiền kỳ vọng" numeric>
                        {formatVnd(drawerTarget.item.expected_amount_vnd)}
                      </DetailItem>
                      <DetailItem label="Số tiền thực nhận" numeric>
                        {formatVnd(drawerTarget.item.actual_amount_vnd)}
                      </DetailItem>
                      <DetailItem label="Mã giao dịch dự kiến" numeric>
                        {drawerTarget.item.expected_payment_id || EMPTY_DASH}
                      </DetailItem>
                      <DetailItem label="Tham chiếu đơn" numeric>
                        {drawerTarget.item.order_ref || EMPTY_DASH}
                      </DetailItem>
                      {drawerTarget.item.notes ? (
                        <DetailItem label="Ghi chú" wide>
                          {formatReconciliationNote(drawerTarget.item.notes)}
                        </DetailItem>
                      ) : null}
                    </DetailGrid>

                    {canWriteRecon &&
                    ["pending", "suspended"].includes(
                      drawerTarget.item.resolution_status.toLowerCase()
                    ) ? (
                      <form
                        className="space-y-4 rounded-xl border border-border bg-card p-4"
                        onSubmit={(e) => void handleResolveQueueItem(e, drawerTarget.item)}
                        noValidate
                      >
                        <h3 className="text-sm font-semibold text-foreground">
                          Xử lý giao dịch lệch
                        </h3>
                        <FieldGroup className="gap-3">
                          <Field className="gap-1.5">
                            <FieldLabel htmlFor="q-action">Hướng xử lý</FieldLabel>
                            <Select
                              value={queueAction}
                              disabled={queueSubmitting}
                              onValueChange={(v) => {
                                setQueueAction(v as GatewayReconciliationAction);
                                setQueueFormError(null);
                              }}
                            >
                              <SelectTrigger id="q-action" className="w-full bg-card">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="ASSIGN">
                                  Gán khớp vào giao dịch nội bộ (ASSIGN)
                                </SelectItem>
                                <SelectItem value="REFUND">Tạo lệnh hoàn tiền (REFUND)</SelectItem>
                                <SelectItem value="SUSPEND">
                                  Tạm treo chờ xác minh (SUSPEND)
                                </SelectItem>
                              </SelectContent>
                            </Select>
                          </Field>

                          {queueAction === "ASSIGN" ? (
                            <Field className="gap-1.5">
                              <FieldLabel htmlFor="q-payment-id">
                                Mã thanh toán nội bộ (payment_id)
                              </FieldLabel>
                              <Input
                                id="q-payment-id"
                                type="text"
                                value={queuePaymentId}
                                disabled={queueSubmitting}
                                onChange={(e) => {
                                  setQueuePaymentId(e.target.value);
                                  setQueueFormError(null);
                                }}
                                placeholder="Nhập payment_id cần gán khớp..."
                                className="bg-card"
                              />
                            </Field>
                          ) : null}

                          <Field className="gap-1.5">
                            <FieldLabel htmlFor="q-note">Ghi chú xử lý</FieldLabel>
                            <Textarea
                              id="q-note"
                              rows={3}
                              value={queueNote}
                              disabled={queueSubmitting}
                              onChange={(e) => setQueueNote(e.target.value)}
                              placeholder="Ghi rõ lý do gán khớp, hoàn tiền hoặc tạm treo..."
                              className="bg-card"
                            />
                          </Field>

                          <FieldError>{queueFormError}</FieldError>
                        </FieldGroup>

                        <Button type="submit" size="sm" disabled={queueSubmitting}>
                          {queueSubmitting ? "Đang xử lý..." : "Xác nhận xử lý"}
                        </Button>
                      </form>
                    ) : null}
                  </>
                ) : null}

                {drawerTarget.kind === "refund" ? (
                  <>
                    <DetailGrid>
                      <DetailItem label="Mã đơn hàng" numeric>
                        {drawerTarget.refund.order_id}
                      </DetailItem>
                      <DetailItem label="Mã thanh toán" numeric>
                        {drawerTarget.refund.payment_id}
                      </DetailItem>
                      <DetailItem label="Người khởi tạo">
                        {formatApprovalActorDisplay(drawerTarget.refund.requested_by)}
                      </DetailItem>
                      <DetailItem label="Người phê duyệt">
                        {formatApprovalActorDisplay(drawerTarget.refund.approved_by)}
                      </DetailItem>
                      {drawerTarget.refund.bank_code ? (
                        <DetailItem label="Tài khoản nhận hoàn tiền" wide>
                          {drawerTarget.refund.bank_code} ·{" "}
                          {drawerTarget.refund.bank_account_number ||
                            drawerTarget.refund.bank_account_masked ||
                            EMPTY_DASH}
                          {drawerTarget.refund.bank_account_name
                            ? ` (${drawerTarget.refund.bank_account_name})`
                            : ""}
                        </DetailItem>
                      ) : null}
                      {drawerTarget.refund.provider_refund_id ? (
                        <DetailItem label="Mã chuyển khoản tất toán" numeric>
                          {drawerTarget.refund.provider_refund_id}
                        </DetailItem>
                      ) : null}
                      {drawerTarget.refund.settled_at ? (
                        <DetailItem label="Thời gian tất toán" numeric>
                          {formatDateTimeVi(drawerTarget.refund.settled_at)}
                        </DetailItem>
                      ) : null}
                      <DetailItem label="Lý do hoàn tiền" wide>
                        {localizeRefundReason(drawerTarget.refund.reason) || EMPTY_DASH}
                      </DetailItem>
                      {drawerTarget.refund.approval_note ? (
                        <DetailItem label="Ghi chú duyệt 2 người" wide>
                          {drawerTarget.refund.approval_note}
                        </DetailItem>
                      ) : null}
                    </DetailGrid>

                    {drawerTarget.refund.status.toUpperCase() === "PENDING_APPROVAL" ? (
                      <div className="space-y-3 rounded-xl border border-border bg-card p-4">
                        <h3 className="text-sm font-semibold text-foreground">
                          Phê duyệt 2 người (Maker–Checker)
                        </h3>
                        {canDecideTwoPersonApproval({
                          actorId: user.id,
                          actorRole: user.role,
                          requestedBy: drawerTarget.refund.requested_by,
                          decideCapability: "refunds.approve",
                        }) ? (
                          <>
                            <Field className="gap-1.5">
                              <FieldLabel htmlFor="rf-approval-note">
                                Ghi chú phê duyệt (bắt buộc khi từ chối)
                              </FieldLabel>
                              <Textarea
                                id="rf-approval-note"
                                rows={3}
                                value={approvalNote}
                                disabled={refundBusy}
                                onChange={(e) => {
                                  setApprovalNote(e.target.value);
                                  setRefundFormError(null);
                                }}
                                placeholder="Ghi chú kiểm tra chứng từ trước khi duyệt hoặc từ chối..."
                                className="bg-card"
                              />
                            </Field>
                            <FieldError>{refundFormError}</FieldError>
                            <div className="flex flex-wrap items-center gap-2">
                              <Button
                                type="button"
                                size="sm"
                                disabled={refundBusy}
                                onClick={() => void handleDecideRefund(drawerTarget.refund, true)}
                              >
                                {refundBusy ? "Đang xử lý..." : "Phê duyệt hoàn tiền"}
                              </Button>
                              <Button
                                type="button"
                                variant="destructive"
                                size="sm"
                                disabled={refundBusy}
                                onClick={() => void handleDecideRefund(drawerTarget.refund, false)}
                              >
                                Từ chối
                              </Button>
                            </div>
                          </>
                        ) : (
                          <Alert>
                            <Info className="size-4" aria-hidden="true" />
                            <AlertDescription>
                              {drawerTarget.refund.requested_by &&
                              drawerTarget.refund.requested_by === user.id
                                ? "Bạn là người khởi tạo lệnh hoàn tiền này; cần quản trị viên hoặc kế toán khác phê duyệt."
                                : "Tài khoản của bạn không có quyền phê duyệt lệnh hoàn tiền 2 người."}
                            </AlertDescription>
                          </Alert>
                        )}
                      </div>
                    ) : null}

                    {isAdminWalletRefundAutoPending(drawerTarget.refund) ? (
                      <Alert>
                        <Info className="size-4" aria-hidden="true" />
                        <AlertDescription>
                          Lệnh hoàn tiền về ví ZaloPay đang được cổng thanh toán xử lý tự động;
                          không cần tất toán thủ công.
                        </AlertDescription>
                      </Alert>
                    ) : null}

                    {isAdminRefundManuallySettleable(drawerTarget.refund) && canSettleRefund ? (
                      <form
                        className="space-y-4 rounded-xl border border-border bg-card p-4"
                        onSubmit={(e) => void handleSettleRefund(e, drawerTarget.refund)}
                        noValidate
                      >
                        <h3 className="text-sm font-semibold text-foreground">
                          Xác nhận tất toán chuyển khoản
                        </h3>
                        <FieldGroup className="gap-3">
                          <Field className="gap-1.5">
                            <FieldLabel htmlFor="rf-settle-id">
                              Mã giao dịch chuyển khoản ngân hàng
                            </FieldLabel>
                            <Input
                              id="rf-settle-id"
                              type="text"
                              value={settleRefId}
                              disabled={refundBusy}
                              onChange={(e) => {
                                setSettleRefId(e.target.value);
                                setRefundFormError(null);
                              }}
                              placeholder="VD: FT2611250019283"
                              className="bg-card"
                            />
                          </Field>
                          <Field className="gap-1.5">
                            <FieldLabel htmlFor="rf-settle-note">
                              Ghi chú kế toán (tùy chọn)
                            </FieldLabel>
                            <Input
                              id="rf-settle-note"
                              type="text"
                              value={settleNote}
                              disabled={refundBusy}
                              onChange={(e) => setSettleNote(e.target.value)}
                              placeholder="Đã đối chiếu sao kê chuyển khoản hoàn tiền..."
                              className="bg-card"
                            />
                          </Field>
                          <FieldError>{refundFormError}</FieldError>
                        </FieldGroup>
                        <Button type="submit" size="sm" disabled={refundBusy}>
                          {refundBusy ? "Đang xác nhận..." : "Xác nhận đã chuyển khoản"}
                        </Button>
                      </form>
                    ) : null}
                  </>
                ) : null}

                {drawerTarget.kind === "create-refund" && canCreateRefund ? (
                  <form
                    className="space-y-4"
                    onSubmit={(e) => void handleCreateManualRefund(e)}
                    noValidate
                  >
                    <FieldGroup className="gap-3.5">
                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="new-rf-payment">Mã thanh toán (payment_id)</FieldLabel>
                        <Input
                          id="new-rf-payment"
                          type="text"
                          value={newRefundPaymentId}
                          disabled={creatingRefund}
                          onChange={(e) => setNewRefundPaymentId(e.target.value)}
                          placeholder="UUID giao dịch thanh toán..."
                          className="bg-card"
                        />
                      </Field>
                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="new-rf-order">Mã đơn hàng (order_id)</FieldLabel>
                        <Input
                          id="new-rf-order"
                          type="text"
                          value={newRefundOrderId}
                          disabled={creatingRefund}
                          onChange={(e) => setNewRefundOrderId(e.target.value)}
                          placeholder="UUID đơn hàng..."
                          className="bg-card"
                        />
                      </Field>
                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="new-rf-amount">Số tiền hoàn (VND)</FieldLabel>
                        <Input
                          id="new-rf-amount"
                          type="number"
                          min={1}
                          step={1}
                          value={newRefundAmount}
                          disabled={creatingRefund}
                          onChange={(e) => setNewRefundAmount(e.target.value)}
                          placeholder="VD: 199000"
                          className="bg-card tabular-nums"
                        />
                      </Field>
                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="new-rf-reason">Lý do hoàn tiền</FieldLabel>
                        <Textarea
                          id="new-rf-reason"
                          rows={3}
                          value={newRefundReason}
                          disabled={creatingRefund}
                          onChange={(e) => setNewRefundReason(e.target.value)}
                          placeholder="Ghi rõ căn cứ tạo lệnh hoàn tiền thủ công..."
                          className="bg-card"
                        />
                      </Field>
                      <FieldError>{createRefundError}</FieldError>
                    </FieldGroup>
                    <div className="flex flex-wrap items-center gap-2 pt-1">
                      <Button type="submit" size="sm" disabled={creatingRefund}>
                        {creatingRefund ? "Đang tạo..." : "Tạo lệnh hoàn tiền"}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={creatingRefund}
                        onClick={() => setDrawerTarget(null)}
                      >
                        Hủy
                      </Button>
                    </div>
                  </form>
                ) : null}

                {drawerTarget.kind === "needs-review" ? (
                  <>
                    <DetailGrid>
                      <DetailItem label="Mã đơn hàng" numeric>
                        {drawerTarget.order.order_code}
                      </DetailItem>
                      <DetailItem label="Tổng thanh toán" numeric>
                        {formatVnd(drawerTarget.order.total_payable_vnd)}
                      </DetailItem>
                      <DetailItem label="Phương thức thanh toán">
                        {formatAdminOrderPaymentDisplay(drawerTarget.order)}
                      </DetailItem>
                      <DetailItem label="Mã vận đơn" numeric>
                        {drawerTarget.order.tracking_code || EMPTY_DASH}
                      </DetailItem>
                      <DetailItem label="Gian hàng" numeric>
                        {drawerTarget.order.shop_id}
                      </DetailItem>
                      <DetailItem label="Người mua" numeric>
                        {drawerTarget.order.buyer_id}
                      </DetailItem>
                    </DetailGrid>

                    {canResolveOrderReview ? (
                      <form
                        className="space-y-4 rounded-xl border border-border bg-card p-4"
                        onSubmit={(e) => void handleResolveOrderReview(e, drawerTarget.order)}
                        noValidate
                      >
                        <h3 className="text-sm font-semibold text-foreground">
                          Đánh dấu đã rà soát xong
                        </h3>
                        <FieldGroup className="gap-3">
                          <Field className="gap-1.5">
                            <FieldLabel htmlFor="ord-review-note">Ghi chú vận hành</FieldLabel>
                            <Textarea
                              id="ord-review-note"
                              rows={3}
                              value={orderReviewNote}
                              disabled={orderReviewBusy}
                              onChange={(e) => setOrderReviewNote(e.target.value)}
                              placeholder="Ghi kết quả kiểm tra vận đơn và phương án đã xử lý..."
                              className="bg-card"
                            />
                          </Field>
                          <FieldError>{orderReviewError}</FieldError>
                        </FieldGroup>
                        <Button type="submit" size="sm" disabled={orderReviewBusy}>
                          {orderReviewBusy ? "Đang xác nhận..." : "Xác nhận đã xử lý"}
                        </Button>
                      </form>
                    ) : (
                      <Alert>
                        <Info className="size-4" aria-hidden="true" />
                        <AlertDescription>
                          Tài khoản hiện tại chỉ có quyền xem đơn cần rà soát.
                        </AlertDescription>
                      </Alert>
                    )}
                  </>
                ) : null}
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
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
