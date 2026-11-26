"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronLeft, ChevronRight, RefreshCw, Search, Store } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  PageHeader,
  StatusBadge,
  bankAccountStatusTone,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
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
import { roleHasCapability } from "@/lib/admin-rbac";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type {
  GatewayShop,
  GatewayShopBankAccount,
  GatewayShopBankAccountResponse,
  GatewayUser,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { cn } from "@/lib/utils";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import {
  AdminConfirmActionDialog,
  AdminDetailField,
  AdminDetailGrid,
  AdminKbdHint,
  AdminPaginationBar,
  hasOpenRadixDialog,
  isEditableTarget,
} from "./ops/AdminOpsPrimitives";
import { adminShopStatusTone } from "./ops/adminTones";
import {
  type AdminShopOwnerState,
  buildAdminBankReviewPayload,
  canApproveShop,
  canReviewShopStatus,
  fetchShopOwnerById,
  getAdminShopStatusMeta,
  getDisplayedOwnerFullName,
  isAdminReviewConflictError,
  submitAdminShopReviewDecision,
} from "./types";

type ShopFilterTab =
  "all" | "PENDING" | "BANK_REVIEW_PENDING" | "ACTIVE" | "REJECTED" | "SUSPENDED";
type ShopSortKey = "pending_first" | "newest" | "name";

const SHOPS_PAGE_SIZE = 15;

export interface AdminShopsClientProps {
  initialShops: GatewayShop[];
  initialBankPendingShops?: GatewayShop[];
  initialBankPendingTotal?: number;
  loadError: string | null;
}

type PendingShopDecisionConfirm = {
  shop: GatewayShop;
  decision: "APPROVE" | "REJECT";
};

type PendingBankDecisionConfirm = {
  shop: GatewayShop;
  bankAccount: GatewayShopBankAccount;
  decision: "APPROVE" | "REJECT";
};

function ShopApprovalContent({
  initialShops,
  initialBankPendingShops = [],
  initialBankPendingTotal = 0,
  loadError,
  initialQuery,
  initialSelected,
}: AdminShopsClientProps & {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();
  const role = user.role;

  const canReviewShop = roleHasCapability(role, "shops.review");
  const canReadBank = roleHasCapability(role, "shops.bank.read");
  const canReviewBank = roleHasCapability(role, "shops.bank.review");
  const canRequestSuspend = roleHasCapability(role, "shops.suspend.request");
  const canUnsuspend = roleHasCapability(role, "shops.unsuspend");
  const canReadUsers = roleHasCapability(role, "users.read");

  const [shops, setShops] = useState<GatewayShop[]>(initialShops);
  const [bankPendingShops, setBankPendingShops] = useState<GatewayShop[]>(initialBankPendingShops);
  const [bankPendingTotal, setBankPendingTotal] = useState<number>(initialBankPendingTotal);
  const [refreshing, setRefreshing] = useState(false);

  const [activeTab, setActiveTab] = useState<ShopFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [sortBy, setSortBy] = useState<ShopSortKey>("pending_first");
  const [page, setPage] = useState(1);
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [drawerOpen, setDrawerOpen] = useState(
    Boolean(initialSelected && initialShops.some((s) => s.id === initialSelected))
  );

  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [submittingByShopId, setSubmittingByShopId] = useState<Record<string, boolean>>({});
  const [conflictNoticeByShopId, setConflictNoticeByShopId] = useState<Record<string, string>>({});
  const [confirmShopDecision, setConfirmShopDecision] = useState<PendingShopDecisionConfirm | null>(
    null
  );

  // Owner lookup for D6 expected_owner_name optimistic lock
  const [ownerByUserId, setOwnerByUserId] = useState<Record<string, AdminShopOwnerState>>({});

  // Bank account inspection & review
  const [bankByShopId, setBankByShopId] = useState<
    Record<string, { loading: boolean; account: GatewayShopBankAccount | null; error?: string }>
  >({});
  const [bankReasonDrafts, setBankReasonDrafts] = useState<Record<string, string>>({});
  const [bankSubmittingByShopId, setBankSubmittingByShopId] = useState<Record<string, boolean>>({});
  const [confirmBankDecision, setConfirmBankDecision] = useState<PendingBankDecisionConfirm | null>(
    null
  );

  // 2-person suspension request & unsuspension
  const [suspendReasonDrafts, setSuspendReasonDrafts] = useState<Record<string, string>>({});
  const [suspendBusyByShopId, setSuspendBusyByShopId] = useState<Record<string, boolean>>({});
  const [confirmSuspendShop, setConfirmSuspendShop] = useState<GatewayShop | null>(null);
  const [confirmUnsuspendShop, setConfirmUnsuspendShop] = useState<GatewayShop | null>(null);

  const ownerReqSeqRef = useRef<Record<string, number>>({});
  const bankReqSeqRef = useRef<Record<string, number>>({});
  const ownerByUserIdRef = useRef(ownerByUserId);
  const bankByShopIdRef = useRef(bankByShopId);
  ownerByUserIdRef.current = ownerByUserId;
  bankByShopIdRef.current = bankByShopId;

  const loadShops = useCallback(async () => {
    setRefreshing(true);
    try {
      const [allRes, bankRes] = await Promise.all([
        api<PaginatedResponse<GatewayShop>>("/api/admin/shops?page=1&page_size=100"),
        canReadBank
          ? api<PaginatedResponse<GatewayShop>>(
              "/api/admin/shops?bank_review_pending=true&page=1&page_size=100"
            )
          : Promise.resolve({ items: [] as GatewayShop[], page: 1, page_size: 100, total: 0 }),
      ]);
      setShops(allRes.items ?? []);
      setBankPendingShops(bankRes.items ?? []);
      setBankPendingTotal(bankRes.total ?? (bankRes.items ?? []).length);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tải lại danh sách gian hàng."));
    } finally {
      setRefreshing(false);
    }
  }, [canReadBank, showToast]);

  const loadShopOwner = useCallback(
    async (ownerUserId: string) => {
      const cleanId = (ownerUserId ?? "").trim();
      if (!cleanId || !canReadUsers) return;
      const seq = (ownerReqSeqRef.current[cleanId] ?? 0) + 1;
      ownerReqSeqRef.current[cleanId] = seq;
      setOwnerByUserId((prev) => ({
        ...prev,
        [cleanId]: { loading: true, user: prev[cleanId]?.user ?? null },
      }));
      try {
        const matched = await fetchShopOwnerById(cleanId, (path) =>
          api<PaginatedResponse<GatewayUser>>(`/api/${path.replace(/^\/+/, "")}`)
        );
        if (ownerReqSeqRef.current[cleanId] !== seq) return;
        if (matched && matched.full_name.trim()) {
          setOwnerByUserId((prev) => ({
            ...prev,
            [cleanId]: { loading: false, user: matched },
          }));
        } else {
          setOwnerByUserId((prev) => ({
            ...prev,
            [cleanId]: {
              loading: false,
              user: null,
              error: "Chưa tra cứu được họ tên chủ gian hàng để đối chiếu.",
            },
          }));
        }
      } catch (err) {
        if (ownerReqSeqRef.current[cleanId] !== seq) return;
        setOwnerByUserId((prev) => ({
          ...prev,
          [cleanId]: {
            loading: false,
            user: null,
            error: getErrorMessage(err, "Không thể tải họ tên chủ gian hàng lúc này."),
          },
        }));
      }
    },
    [canReadUsers]
  );

  const loadShopBankAccount = useCallback(async (shopId: string) => {
    const seq = (bankReqSeqRef.current[shopId] ?? 0) + 1;
    bankReqSeqRef.current[shopId] = seq;
    setBankByShopId((prev) => ({
      ...prev,
      [shopId]: { loading: true, account: prev[shopId]?.account ?? null },
    }));
    try {
      const res = await api<GatewayShopBankAccountResponse>(
        `/api/admin/shops/${encodeURIComponent(shopId)}/bank-account`
      );
      if (bankReqSeqRef.current[shopId] !== seq) return;
      setBankByShopId((prev) => ({
        ...prev,
        [shopId]: { loading: false, account: res?.bank_account ?? null },
      }));
    } catch (err) {
      if (bankReqSeqRef.current[shopId] !== seq) return;
      if (err instanceof ApiError && err.status === 404) {
        setBankByShopId((prev) => ({
          ...prev,
          [shopId]: { loading: false, account: null },
        }));
        return;
      }
      setBankByShopId((prev) => ({
        ...prev,
        [shopId]: {
          loading: false,
          account: null,
          error: getErrorMessage(err, "Chưa thể tải thông tin ngân hàng gian hàng."),
        },
      }));
    }
  }, []);

  const counts = useMemo(() => {
    const map: Record<ShopFilterTab, number> = {
      all: shops.length,
      PENDING: 0,
      BANK_REVIEW_PENDING: bankPendingTotal,
      ACTIVE: 0,
      REJECTED: 0,
      SUSPENDED: 0,
    };
    for (const s of shops) {
      const st = s.status.toUpperCase() as ShopFilterTab;
      if (st in map && st !== "BANK_REVIEW_PENDING") map[st] += 1;
    }
    return map;
  }, [shops, bankPendingTotal]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const source = activeTab === "BANK_REVIEW_PENDING" ? bankPendingShops : shops;
    const list = source.filter((s) => {
      if (
        activeTab !== "all" &&
        activeTab !== "BANK_REVIEW_PENDING" &&
        s.status.toUpperCase() !== activeTab
      ) {
        return false;
      }
      if (!q) return true;
      return (
        s.name.toLowerCase().includes(q) ||
        (s.slug ?? "").toLowerCase().includes(q) ||
        s.id.toLowerCase().includes(q) ||
        s.contact_phone.toLowerCase().includes(q) ||
        s.business_license_doc.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q)
      );
    });

    const rank = (status: string) => {
      switch (status.toUpperCase()) {
        case "PENDING":
          return 0;
        case "REJECTED":
          return 1;
        case "SUSPENDED":
          return 2;
        default:
          return 3;
      }
    };

    return [...list].sort((a, b) => {
      if (sortBy === "newest") {
        return (b.created_at ?? "").localeCompare(a.created_at ?? "");
      }
      if (sortBy === "name") {
        return a.name.localeCompare(b.name, "vi");
      }
      const d = rank(a.status) - rank(b.status);
      return d !== 0 ? d : (b.created_at ?? "").localeCompare(a.created_at ?? "");
    });
  }, [shops, bankPendingShops, activeTab, query, sortBy]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / SHOPS_PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pagedShops = useMemo(() => {
    const start = (safePage - 1) * SHOPS_PAGE_SIZE;
    return filtered.slice(start, start + SHOPS_PAGE_SIZE);
  }, [filtered, safePage]);

  const activeIndex = useMemo(() => {
    if (!selectedId) return -1;
    return filtered.findIndex((s) => s.id === selectedId);
  }, [filtered, selectedId]);

  const activeShop = useMemo(() => {
    if (!selectedId) return null;
    return (
      filtered.find((s) => s.id === selectedId) ??
      shops.find((s) => s.id === selectedId) ??
      bankPendingShops.find((s) => s.id === selectedId) ??
      null
    );
  }, [filtered, shops, bankPendingShops, selectedId]);

  const activeShopId = drawerOpen ? (activeShop?.id ?? null) : null;
  const activeOwnerId = drawerOpen ? (activeShop?.owner_user_id ?? "") : "";

  useEffect(() => {
    if (!activeShopId || !canReadBank) return;
    const existing = bankByShopIdRef.current[activeShopId];
    if (existing && !existing.error) return;
    void loadShopBankAccount(activeShopId);
  }, [activeShopId, canReadBank, loadShopBankAccount]);

  useEffect(() => {
    if (!activeOwnerId || !canReadUsers) return;
    const existing = ownerByUserIdRef.current[activeOwnerId];
    if (existing && !existing.error) return;
    void loadShopOwner(activeOwnerId);
  }, [activeOwnerId, canReadUsers, loadShopOwner]);

  useEffect(() => {
    if (!activeShop && drawerOpen) {
      setDrawerOpen(false);
    }
  }, [activeShop, drawerOpen]);

  const openShopSheet = useCallback((shop: GatewayShop) => {
    setReasonErrorId(null);
    setSelectedId(shop.id);
    setDrawerOpen(true);
  }, []);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (filtered.length === 0) return;
      const cur = activeIndex >= 0 ? activeIndex : 0;
      const nextIdx = Math.max(0, Math.min(filtered.length - 1, cur + delta));
      const nextShop = filtered[nextIdx];
      if (!nextShop) return;
      setReasonErrorId(null);
      setSelectedId(nextShop.id);
      const nextPage = Math.floor(nextIdx / SHOPS_PAGE_SIZE) + 1;
      setPage(nextPage);
    },
    [filtered, activeIndex]
  );

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        isEditableTarget(e.target) ||
        hasOpenRadixDialog()
      ) {
        return;
      }
      const key = e.key.toLowerCase();
      if (key === "j") {
        e.preventDefault();
        stepSelection(1);
        setDrawerOpen(true);
      } else if (key === "k") {
        e.preventDefault();
        stepSelection(-1);
        setDrawerOpen(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [stepSelection]);

  if (loadError) {
    return (
      <div className="space-y-5">
        <PageHeader title="Duyệt gian hàng" />
        <ErrorState
          title="Không thể tải hồ sơ gian hàng"
          description={loadError}
          onRetry={() => router.refresh()}
        />
      </div>
    );
  }

  const handleRefreshAll = async () => {
    const tasks: Array<Promise<unknown>> = [loadShops()];
    if (activeShopId && canReadBank) tasks.push(loadShopBankAccount(activeShopId));
    if (activeOwnerId && canReadUsers) tasks.push(loadShopOwner(activeOwnerId));
    await Promise.all(tasks);
  };

  const handleDecisionRequest = (
    shop: GatewayShop,
    decision: "APPROVE" | "REQUEST_INFO" | "REJECT"
  ) => {
    if (!canReviewShop || submittingByShopId[shop.id]) return;
    if (!canReviewShopStatus(shop.status)) return;
    const noteText = (reasonDrafts[shop.id] ?? "").trim();
    if ((decision === "REJECT" || decision === "REQUEST_INFO") && !noteText) {
      setReasonErrorId(shop.id);
      return;
    }
    const ownerState = shop.owner_user_id ? ownerByUserId[shop.owner_user_id] : undefined;
    if (decision === "APPROVE" && !canApproveShop(shop.status, ownerState)) {
      return;
    }
    setReasonErrorId(null);
    if (decision === "APPROVE" || decision === "REJECT") {
      setConfirmShopDecision({ shop, decision });
      return;
    }
    void handleDecision(shop, decision);
  };

  const handleDecision = async (
    shop: GatewayShop,
    decision: "APPROVE" | "REQUEST_INFO" | "REJECT"
  ) => {
    if (!canReviewShop || submittingByShopId[shop.id]) return;
    if (!canReviewShopStatus(shop.status)) return;
    const noteText = (reasonDrafts[shop.id] ?? "").trim();
    if ((decision === "REJECT" || decision === "REQUEST_INFO") && !noteText) {
      setReasonErrorId(shop.id);
      setConfirmShopDecision(null);
      return;
    }
    const ownerState = shop.owner_user_id ? ownerByUserId[shop.owner_user_id] : undefined;
    if (decision === "APPROVE" && !canApproveShop(shop.status, ownerState)) {
      setConfirmShopDecision(null);
      return;
    }
    const displayedOwnerName = getDisplayedOwnerFullName(ownerState);
    setReasonErrorId(null);
    setConflictNoticeByShopId((prev) => {
      if (!prev[shop.id]) return prev;
      const next = { ...prev };
      delete next[shop.id];
      return next;
    });
    setSubmittingByShopId((prev) => ({ ...prev, [shop.id]: true }));
    try {
      const outcome = await submitAdminShopReviewDecision({
        shopId: shop.id,
        decision,
        reason: noteText,
        displayedOwnerName,
        postReview: (path, payload) =>
          api<{ shop: GatewayShop }>(`/api/${path.replace(/^\/+/, "")}`, {
            method: "POST",
            json: payload,
          }),
        onConflictReload: async () => {
          const tasks: Array<Promise<unknown>> = [loadShops()];
          if (canReadBank) tasks.push(loadShopBankAccount(shop.id));
          if (shop.owner_user_id && canReadUsers) tasks.push(loadShopOwner(shop.owner_user_id));
          await Promise.all(tasks);
        },
      });
      setConfirmShopDecision(null);
      if (outcome.status === "blocked_missing_owner") return;
      if (outcome.status === "conflict") {
        setConflictNoticeByShopId((prev) => ({ ...prev, [shop.id]: outcome.message }));
        showToast(outcome.message);
        return;
      }
      if (outcome.shop) {
        setShops((prev) => prev.map((item) => (item.id === shop.id ? outcome.shop! : item)));
      } else {
        await loadShops();
      }
      setReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      const msg =
        decision === "APPROVE"
          ? `Đã phê duyệt gian hàng "${shop.name}".`
          : decision === "REQUEST_INFO"
            ? `Đã yêu cầu bổ sung hồ sơ cho "${shop.name}".`
            : `Đã từ chối hồ sơ "${shop.name}".`;
      showToast(msg);
      router.refresh();
    } catch (err) {
      setConfirmShopDecision(null);
      showToast(getErrorMessage(err, "Không thể cập nhật thẩm định gian hàng."));
    } finally {
      setSubmittingByShopId((prev) => {
        const next = { ...prev };
        delete next[shop.id];
        return next;
      });
    }
  };

  const handleRequestSuspendClick = (shop: GatewayShop) => {
    if (!canRequestSuspend || suspendBusyByShopId[shop.id]) return;
    const reason = (suspendReasonDrafts[shop.id] ?? "").trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do đề xuất khóa gian hàng (tối thiểu 4 ký tự).");
      return;
    }
    setConfirmSuspendShop(shop);
  };

  const handleConfirmSuspendRequest = async (shop: GatewayShop) => {
    if (!canRequestSuspend || suspendBusyByShopId[shop.id]) return;
    const reason = (suspendReasonDrafts[shop.id] ?? "").trim();
    if (reason.length < 4) {
      setConfirmSuspendShop(null);
      return;
    }
    setSuspendBusyByShopId((prev) => ({ ...prev, [shop.id]: true }));
    try {
      await api(`/api/admin/shops/${encodeURIComponent(shop.id)}/suspend`, {
        method: "POST",
        json: { reason },
      });
      setSuspendReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      setConfirmSuspendShop(null);
      showToast(
        `Đã gửi yêu cầu tạm khóa gian hàng "${shop.name}" sang hàng đợi Phê duyệt 2 người.`
      );
      await loadShops();
      router.refresh();
    } catch (err) {
      if (
        err instanceof ApiError &&
        (err.status === 409 || err.code === "ALREADY_EXISTS") &&
        (!err.message || err.message === "Thông tin đã tồn tại trong hệ thống.")
      ) {
        showToast("Gian hàng này đã có yêu cầu khóa đang chờ duyệt.");
      } else {
        showToast(getErrorMessage(err, "Không thể tạo yêu cầu khóa gian hàng lúc này."));
      }
      setConfirmSuspendShop(null);
    } finally {
      setSuspendBusyByShopId((prev) => {
        const next = { ...prev };
        delete next[shop.id];
        return next;
      });
    }
  };

  const handleRequestUnsuspendClick = (shop: GatewayShop) => {
    if (!canUnsuspend || suspendBusyByShopId[shop.id]) return;
    const reason = (suspendReasonDrafts[shop.id] ?? "").trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do mở khóa gian hàng (tối thiểu 4 ký tự).");
      return;
    }
    setConfirmUnsuspendShop(shop);
  };

  const handleUnsuspendShop = async (shop: GatewayShop) => {
    if (!canUnsuspend || suspendBusyByShopId[shop.id]) return;
    const reason = (suspendReasonDrafts[shop.id] ?? "").trim();
    if (reason.length < 4) {
      setConfirmUnsuspendShop(null);
      return;
    }
    setSuspendBusyByShopId((prev) => ({ ...prev, [shop.id]: true }));
    try {
      const res = await api<{ shop?: GatewayShop }>(
        `/api/admin/shops/${encodeURIComponent(shop.id)}/unsuspend`,
        {
          method: "POST",
          json: { reason },
        }
      );
      if (res?.shop) {
        setShops((prev) => prev.map((item) => (item.id === shop.id ? res.shop! : item)));
      } else {
        await loadShops();
      }
      setSuspendReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      setConfirmUnsuspendShop(null);
      showToast(`Đã mở khóa gian hàng "${shop.name}".`);
      router.refresh();
    } catch (err) {
      setConfirmUnsuspendShop(null);
      showToast(getErrorMessage(err, "Không thể mở khóa gian hàng lúc này."));
    } finally {
      setSuspendBusyByShopId((prev) => {
        const next = { ...prev };
        delete next[shop.id];
        return next;
      });
    }
  };

  const handleBankReviewRequest = (
    shop: GatewayShop,
    bankAccount: GatewayShopBankAccount,
    decision: "APPROVE" | "REJECT"
  ) => {
    if (!canReviewBank || bankSubmittingByShopId[shop.id]) return;
    const reason = (bankReasonDrafts[shop.id] ?? "").trim();
    if (decision === "REJECT" && reason.length < 3) {
      showToast("Vui lòng nhập lý do từ chối tài khoản ngân hàng (tối thiểu 3 ký tự).");
      return;
    }
    setConfirmBankDecision({ shop, bankAccount, decision });
  };

  const handleBankReview = async (
    shop: GatewayShop,
    bankAccount: GatewayShopBankAccount,
    decision: "APPROVE" | "REJECT"
  ) => {
    if (!canReviewBank || bankSubmittingByShopId[shop.id]) return;
    const reason = (bankReasonDrafts[shop.id] ?? "").trim();
    if (decision === "REJECT" && reason.length < 3) {
      showToast("Vui lòng nhập lý do từ chối tài khoản ngân hàng (tối thiểu 3 ký tự).");
      setConfirmBankDecision(null);
      return;
    }
    let payload: ReturnType<typeof buildAdminBankReviewPayload>;
    try {
      payload = buildAdminBankReviewPayload({
        bankAccountId: bankAccount.id,
        decision,
        reason,
      });
    } catch {
      const staleMsg =
        "Dữ liệu đã thay đổi — thiếu mã bản ghi tài khoản ngân hàng. Hệ thống đã tải lại thông tin mới nhất.";
      setConflictNoticeByShopId((prev) => ({ ...prev, [shop.id]: staleMsg }));
      showToast(staleMsg);
      setConfirmBankDecision(null);
      await loadShopBankAccount(shop.id);
      return;
    }
    setConflictNoticeByShopId((prev) => {
      if (!prev[shop.id]) return prev;
      const next = { ...prev };
      delete next[shop.id];
      return next;
    });
    setBankSubmittingByShopId((prev) => ({ ...prev, [shop.id]: true }));
    try {
      const res = await api<GatewayShopBankAccountResponse>(
        `/api/admin/shops/${encodeURIComponent(shop.id)}/bank-account/review`,
        {
          method: "POST",
          json: payload,
        }
      );
      bankReqSeqRef.current[shop.id] = (bankReqSeqRef.current[shop.id] ?? 0) + 1;
      setBankByShopId((prev) => ({
        ...prev,
        [shop.id]: { loading: false, account: res?.bank_account ?? null },
      }));
      setBankReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      setConfirmBankDecision(null);
      await loadShops();
      showToast(
        decision === "APPROVE"
          ? `Đã phê duyệt tài khoản ngân hàng của "${shop.name}".`
          : `Đã từ chối tài khoản ngân hàng của "${shop.name}".`
      );
      router.refresh();
    } catch (err) {
      setConfirmBankDecision(null);
      if (isAdminReviewConflictError(err)) {
        const conflictMsg =
          "Dữ liệu đã thay đổi — tài khoản ngân hàng vừa được cập nhật hoặc không còn chờ duyệt. Hệ thống đã tải lại thông tin mới nhất, vui lòng kiểm tra lại.";
        setConflictNoticeByShopId((prev) => ({ ...prev, [shop.id]: conflictMsg }));
        showToast(conflictMsg);
        await Promise.all([loadShopBankAccount(shop.id), loadShops()]);
      } else {
        showToast(getErrorMessage(err, "Không thể xử lý thẩm định tài khoản ngân hàng."));
      }
    } finally {
      setBankSubmittingByShopId((prev) => {
        const next = { ...prev };
        delete next[shop.id];
        return next;
      });
    }
  };

  const renderInspectorBody = (shop: GatewayShop) => {
    const draftValue = reasonDrafts[shop.id] ?? "";
    const showReasonError = reasonErrorId === shop.id;
    const isBusy = Boolean(submittingByShopId[shop.id]);
    const ownerState = shop.owner_user_id ? ownerByUserId[shop.owner_user_id] : undefined;
    const ownerUser = ownerState?.user ?? null;
    const displayedOwnerName = getDisplayedOwnerFullName(ownerState);
    const isPendingShop = canReviewShopStatus(shop.status);
    const canApprove = canApproveShop(shop.status, ownerState);
    const conflictNotice = conflictNoticeByShopId[shop.id];

    const bankState = bankByShopId[shop.id];
    const bankAcc = bankState?.account ?? null;
    const bankBusy = Boolean(bankSubmittingByShopId[shop.id]);
    const bankStatusLabel =
      bankAcc?.status === "VERIFIED"
        ? "Đã xác minh"
        : bankAcc?.status === "PENDING_ADMIN_REVIEW"
          ? "Chờ sàn duyệt"
          : bankAcc?.status === "PENDING_OTP"
            ? "Chờ shop nhập OTP"
            : bankAcc?.status === "REJECTED"
              ? "Bị từ chối"
              : (bankAcc?.status ?? "");

    return (
      <div key={shop.id} className="space-y-5 px-4 pb-6">
        {conflictNotice && (
          <Alert variant="destructive">
            <AlertDescription>{conflictNotice}</AlertDescription>
          </Alert>
        )}

        <AdminDetailGrid>
          <AdminDetailField label="Chủ sở hữu">
            {ownerState?.loading
              ? "Đang tải họ tên chủ gian hàng..."
              : displayedOwnerName
                ? `${displayedOwnerName}${ownerUser?.email ? ` (${ownerUser.email})` : ""}`
                : shop.owner_user_id || EMPTY_DASH}
          </AdminDetailField>
          <AdminDetailField label="Số điện thoại liên hệ" mono>
            {shop.contact_phone || EMPTY_DASH}
          </AdminDetailField>
          <AdminDetailField label="Giấy phép kinh doanh / Hồ sơ">
            {shop.business_license_doc || EMPTY_DASH}
          </AdminDetailField>
          <AdminDetailField label="Mã gian hàng" mono>
            {shop.id}
          </AdminDetailField>
          <AdminDetailField label="Thời điểm đăng ký" mono>
            {shop.created_at ? formatDateTimeVi(shop.created_at) : EMPTY_DASH}
          </AdminDetailField>
          <AdminDetailField label="Cập nhật gần nhất" mono>
            {shop.updated_at ? formatDateTimeVi(shop.updated_at) : EMPTY_DASH}
          </AdminDetailField>
          <AdminDetailField label="Mô tả gian hàng" wide>
            {shop.description || EMPTY_DASH}
          </AdminDetailField>
          {shop.rejection_reason && (
            <AdminDetailField label="Ghi chú thẩm định gần nhất" wide>
              <span className="font-medium text-badge-warning-ink">{shop.rejection_reason}</span>
            </AdminDetailField>
          )}
          {shop.suspended_reason && (
            <AdminDetailField label="Lý do tạm ngưng gian hàng" wide>
              <span className="font-medium text-destructive">
                {shop.suspended_reason}
                {shop.suspended_at ? ` · Từ ${formatDateTimeVi(shop.suspended_at)}` : ""}
              </span>
            </AdminDetailField>
          )}
        </AdminDetailGrid>

        {/* Shop approval review (PENDING only, D6 expected_owner_name lock) */}
        {canReviewShop && isPendingShop && (
          <div className="space-y-3 rounded-lg border border-border bg-card p-4">
            <Field>
              <FieldLabel htmlFor={`drawer-shop-note-${shop.id}`}>
                Ghi chú thẩm định hoặc lý do từ chối
              </FieldLabel>
              <Input
                id={`drawer-shop-note-${shop.id}`}
                type="text"
                value={draftValue}
                onChange={(e) => {
                  setReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }));
                  if (showReasonError) setReasonErrorId(null);
                }}
                placeholder="Bắt buộc nhập lý do khi Từ chối hoặc Yêu cầu bổ sung..."
              />
              {showReasonError && (
                <FieldError>
                  Vui lòng nhập lý do cụ thể trước khi từ chối hoặc yêu cầu bổ sung.
                </FieldError>
              )}
            </Field>

            {!canApprove && (
              <div
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/40 px-3 py-2 text-xs"
                role={ownerState?.loading ? "status" : "alert"}
              >
                <span
                  className={ownerState?.loading ? "text-muted-foreground" : "text-destructive"}
                >
                  {ownerState?.loading
                    ? "Đang tải họ tên chủ gian hàng để đối chiếu..."
                    : `${ownerState?.error ?? "Chưa có họ tên chủ gian hàng để đối chiếu."} Chưa thể phê duyệt cho tới khi tên chủ hiển thị.`}
                </span>
                {shop.owner_user_id && canReadUsers && (
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={ownerState?.loading}
                    onClick={() => void loadShopOwner(shop.owner_user_id)}
                  >
                    Tải lại
                  </Button>
                )}
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                disabled={isBusy || !canApprove}
                onClick={() => handleDecisionRequest(shop, "APPROVE")}
              >
                {isBusy ? "Đang xử lý..." : "Phê duyệt"}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={isBusy}
                onClick={() => handleDecisionRequest(shop, "REQUEST_INFO")}
              >
                Yêu cầu bổ sung
              </Button>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={isBusy}
                onClick={() => handleDecisionRequest(shop, "REJECT")}
              >
                Từ chối
              </Button>
            </div>
          </div>
        )}

        {/* 2-person shop suspension request */}
        {shop.status.toUpperCase() === "ACTIVE" && canRequestSuspend && (
          <div className="space-y-3 rounded-lg border border-border bg-card p-4">
            <Field>
              <FieldLabel htmlFor={`drawer-suspend-note-${shop.id}`}>
                Đề xuất tạm khóa gian hàng (duyệt 2 người)
              </FieldLabel>
              <Input
                id={`drawer-suspend-note-${shop.id}`}
                type="text"
                value={suspendReasonDrafts[shop.id] ?? ""}
                onChange={(e) =>
                  setSuspendReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }))
                }
                placeholder="Nhập lý do vi phạm cần tạm khóa..."
              />
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={Boolean(suspendBusyByShopId[shop.id])}
                onClick={() => handleRequestSuspendClick(shop)}
              >
                {suspendBusyByShopId[shop.id] ? "Đang gửi..." : "Yêu cầu khóa gian hàng"}
              </Button>
            </div>
          </div>
        )}

        {/* Unsuspend shop */}
        {shop.status.toUpperCase() === "SUSPENDED" && canUnsuspend && (
          <div className="space-y-3 rounded-lg border border-border bg-card p-4">
            <Field>
              <FieldLabel htmlFor={`drawer-unsuspend-note-${shop.id}`}>
                Mở khóa gian hàng
              </FieldLabel>
              <Input
                id={`drawer-unsuspend-note-${shop.id}`}
                type="text"
                value={suspendReasonDrafts[shop.id] ?? ""}
                onChange={(e) =>
                  setSuspendReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }))
                }
                placeholder="Nhập lý do mở khóa gian hàng..."
              />
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                disabled={Boolean(suspendBusyByShopId[shop.id])}
                onClick={() => handleRequestUnsuspendClick(shop)}
              >
                {suspendBusyByShopId[shop.id] ? "Đang xử lý..." : "Mở khóa gian hàng"}
              </Button>
            </div>
          </div>
        )}

        {/* Shop Bank Account Inspection & Approval */}
        {canReadBank && (
          <>
            <Separator />
            <div className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="text-sm font-semibold text-foreground">
                  Tài khoản ngân hàng nhận giải ngân
                </h3>
                <div className="flex items-center gap-2">
                  {bankAcc ? (
                    <StatusBadge tone={bankAccountStatusTone(bankAcc.status)}>
                      {bankStatusLabel}
                    </StatusBadge>
                  ) : (
                    !bankState?.loading &&
                    !bankState?.error && <StatusBadge tone="neutral">Chưa khai báo</StatusBadge>
                  )}
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    onClick={() => void loadShopBankAccount(shop.id)}
                  >
                    Tải lại
                  </Button>
                </div>
              </div>

              {!bankState || bankState.loading ? (
                <p className="text-xs text-muted-foreground">
                  Đang tải thông tin tài khoản ngân hàng...
                </p>
              ) : bankState.error ? (
                <p className="text-xs text-destructive" role="alert">
                  {bankState.error}
                </p>
              ) : !bankAcc ? (
                <p className="text-xs text-muted-foreground">
                  Chưa khai báo tài khoản ngân hàng nhận giải ngân.
                </p>
              ) : (
                <div className="space-y-3">
                  <AdminDetailGrid>
                    <AdminDetailField label="Ngân hàng">
                      {bankAcc.bank_code} · {bankAcc.bank_name}
                    </AdminDetailField>
                    <AdminDetailField label="Số tài khoản" mono>
                      {bankAcc.account_number_masked}
                    </AdminDetailField>
                    <AdminDetailField label="Chủ tài khoản" wide>
                      {bankAcc.account_holder_name} (
                      {bankAcc.holder_name_matched ? "Khớp tên chủ shop" : "Khác tên chủ shop"})
                    </AdminDetailField>
                    {bankAcc.payout_locked_until && (
                      <AdminDetailField label="Khóa giải ngân an toàn đến" mono wide>
                        {formatDateTimeVi(bankAcc.payout_locked_until)}
                      </AdminDetailField>
                    )}
                    {bankAcc.review_note && (
                      <AdminDetailField
                        label={
                          bankAcc.status === "REJECTED"
                            ? "Lý do từ chối ngân hàng"
                            : "Ghi chú thẩm định ngân hàng"
                        }
                        wide
                      >
                        <span
                          className={
                            bankAcc.status === "REJECTED"
                              ? "font-medium text-destructive"
                              : "text-foreground"
                          }
                        >
                          {bankAcc.review_note}
                        </span>
                      </AdminDetailField>
                    )}
                  </AdminDetailGrid>

                  {bankAcc.status === "PENDING_ADMIN_REVIEW" && canReviewBank && (
                    <div className="space-y-3 rounded-lg border border-border bg-card p-4">
                      <Field>
                        <FieldLabel htmlFor={`drawer-bank-note-${shop.id}`}>
                          Ghi chú thẩm định tài khoản ngân hàng
                        </FieldLabel>
                        <Input
                          id={`drawer-bank-note-${shop.id}`}
                          type="text"
                          value={bankReasonDrafts[shop.id] ?? ""}
                          onChange={(e) =>
                            setBankReasonDrafts((prev) => ({
                              ...prev,
                              [shop.id]: e.target.value,
                            }))
                          }
                          placeholder="Bắt buộc nhập lý do khi từ chối tài khoản ngân hàng..."
                        />
                      </Field>
                      <div className="flex flex-wrap items-center gap-2">
                        <Button
                          type="button"
                          size="sm"
                          disabled={bankBusy}
                          onClick={() => handleBankReviewRequest(shop, bankAcc, "APPROVE")}
                        >
                          {bankBusy ? "Đang xử lý..." : "Duyệt tài khoản ngân hàng"}
                        </Button>
                        <Button
                          type="button"
                          variant="destructive"
                          size="sm"
                          disabled={bankBusy}
                          onClick={() => handleBankReviewRequest(shop, bankAcc, "REJECT")}
                        >
                          Từ chối ngân hàng
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          </>
        )}
      </div>
    );
  };

  const tabs = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
    ...(canReadBank
      ? [
          {
            key: "BANK_REVIEW_PENDING",
            label: "Chờ duyệt tài khoản ngân hàng",
            count: counts.BANK_REVIEW_PENDING,
          },
        ]
      : []),
    { key: "ACTIVE", label: "Đang hoạt động", count: counts.ACTIVE },
    { key: "REJECTED", label: "Từ chối", count: counts.REJECTED },
    { key: "SUSPENDED", label: "Tạm ngưng", count: counts.SUSPENDED },
  ];

  const activeShopOwnerName = activeShop?.owner_user_id
    ? getDisplayedOwnerFullName(ownerByUserId[activeShop.owner_user_id])
    : "";

  const sortByLabel =
    sortBy === "newest"
      ? "Mới đăng ký gần nhất"
      : sortBy === "name"
        ? "Tên gian hàng (A đến Z)"
        : "Ưu tiên chờ duyệt";

  return (
    <div className="space-y-5">
      <PageHeader
        title="Duyệt gian hàng"
        subtitle="Thẩm định hồ sơ mở gian hàng, tài khoản ngân hàng và yêu cầu tạm khóa 2 người."
        primaryAction={
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={refreshing}
            onClick={() => void handleRefreshAll()}
          >
            <RefreshCw className={cn("size-4", refreshing && "animate-spin")} aria-hidden="true" />
            <span>{refreshing ? "Đang tải..." : "Làm mới"}</span>
          </Button>
        }
      />

      <MetricStrip
        className="md:grid-flow-row md:grid-cols-2 md:divide-x-0 lg:grid-flow-col lg:auto-cols-fr lg:divide-x"
        items={[
          { label: "Tổng gian hàng", value: formatInt(counts.all) },
          {
            label: "Chờ duyệt hồ sơ",
            value: (
              <span className={counts.PENDING > 0 ? "text-badge-attention-ink" : undefined}>
                {formatInt(counts.PENDING)}
              </span>
            ),
          },
          ...(canReadBank
            ? [
                {
                  label: "Chờ duyệt ngân hàng",
                  value: formatInt(counts.BANK_REVIEW_PENDING),
                },
              ]
            : []),
          { label: "Đang hoạt động", value: formatInt(counts.ACTIVE) },
        ]}
      />

      <IndexTable
        tabs={tabs}
        activeTab={activeTab}
        onTabChange={(next) => {
          setActiveTab(next as ShopFilterTab);
          setPage(1);
        }}
        toolbar={
          <div className="flex items-center gap-3">
            <AdminKbdHint label="chuyển hồ sơ" />
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatInt(filtered.length)} gian hàng
            </span>
          </div>
        }
        footer={
          filtered.length > 0 ? (
            <AdminPaginationBar
              page={safePage}
              totalPages={totalPages}
              totalItems={filtered.length}
              itemLabel="gian hàng"
              onPageChange={setPage}
            />
          ) : undefined
        }
      >
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
          <div className="relative min-w-0 flex-1 sm:max-w-sm">
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              type="search"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPage(1);
              }}
              placeholder="Tìm tên gian hàng, SĐT, mã hồ sơ..."
              aria-label="Tìm kiếm hồ sơ gian hàng"
              className="h-9 bg-card pl-8"
            />
          </div>

          <Select
            value={sortBy}
            onValueChange={(val) => {
              setSortBy(val as ShopSortKey);
              setPage(1);
            }}
          >
            <SelectTrigger className="w-full bg-card sm:w-52" aria-label="Sắp xếp hồ sơ gian hàng">
              <SelectValue>{sortByLabel}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="pending_first">Ưu tiên chờ duyệt</SelectItem>
              <SelectItem value="newest">Mới đăng ký gần nhất</SelectItem>
              <SelectItem value="name">Tên gian hàng (A đến Z)</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {filtered.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Store}
              title="Không tìm thấy hồ sơ gian hàng phù hợp"
              description="Thử đổi bộ lọc trạng thái hoặc từ khóa tìm kiếm."
              className="shadow-none"
            />
          </div>
        ) : (
          <>
            <div className="hidden md:block">
              <Table aria-label="Danh sách hồ sơ gian hàng">
                <TableHeader>
                  <TableRow>
                    <TableHead>Gian hàng</TableHead>
                    <TableHead className="hidden lg:table-cell">Liên hệ &amp; GPKD</TableHead>
                    <TableHead>Trạng thái</TableHead>
                    <TableHead className="hidden lg:table-cell">Đăng ký</TableHead>
                    <TableHead className="text-right">Thao tác</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pagedShops.map((shop) => {
                    const meta = getAdminShopStatusMeta(shop.status);
                    const isSelected = drawerOpen && activeShop?.id === shop.id;
                    return (
                      <TableRow
                        key={shop.id}
                        data-state={isSelected ? "selected" : undefined}
                        className="cursor-pointer"
                        onClick={() => openShopSheet(shop)}
                      >
                        <TableCell className="max-w-[280px] whitespace-normal">
                          <div className="font-medium text-foreground">{shop.name}</div>
                          <div className="text-xs text-muted-foreground tabular-nums">
                            {shop.slug ? `/${shop.slug} · ` : ""}
                            {shop.id.slice(0, 12)}
                          </div>
                        </TableCell>
                        <TableCell className="hidden max-w-[260px] whitespace-normal lg:table-cell">
                          <div className="text-[13px] text-foreground tabular-nums">
                            {shop.contact_phone || EMPTY_DASH}
                          </div>
                          <div className="truncate text-xs text-muted-foreground">
                            {shop.business_license_doc || "Chưa đính kèm GPKD"}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <StatusBadge tone={adminShopStatusTone(shop.status)}>
                              {meta.label}
                            </StatusBadge>
                            {shop.bank_review_pending && (
                              <StatusBadge tone="attention">Chờ duyệt NH</StatusBadge>
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="hidden text-xs text-muted-foreground tabular-nums lg:table-cell">
                          {shop.created_at ? formatShortTimeDateVi(shop.created_at) : EMPTY_DASH}
                        </TableCell>
                        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={() => openShopSheet(shop)}
                          >
                            Xem chi tiết
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>

            <div className="md:hidden">
              {pagedShops.map((shop) => {
                const meta = getAdminShopStatusMeta(shop.status);
                return (
                  <IndexRowCard
                    key={shop.id}
                    title={shop.name}
                    badge={
                      <div className="flex flex-wrap items-center gap-1">
                        <StatusBadge tone={adminShopStatusTone(shop.status)}>
                          {meta.label}
                        </StatusBadge>
                        {shop.bank_review_pending && (
                          <StatusBadge tone="attention">Chờ duyệt NH</StatusBadge>
                        )}
                      </div>
                    }
                    meta={
                      <>
                        <span className="tabular-nums">
                          {shop.contact_phone || shop.id.slice(0, 12)}
                        </span>
                        {shop.created_at && (
                          <>
                            <span>·</span>
                            <span className="tabular-nums">
                              {formatShortTimeDateVi(shop.created_at)}
                            </span>
                          </>
                        )}
                        <span>·</span>
                        <span>{shop.business_license_doc || "Chưa đính kèm GPKD"}</span>
                      </>
                    }
                    onClick={() => openShopSheet(shop)}
                  />
                );
              })}
            </div>
          </>
        )}
      </IndexTable>

      {/* Detail Sheet */}
      <Sheet open={drawerOpen && Boolean(activeShop)} onOpenChange={setDrawerOpen}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {activeShop ? (
            <>
              <SheetHeader className="border-b border-border pr-12">
                <div className="flex flex-wrap items-center gap-2">
                  <SheetTitle>
                    {activeShop.name}
                    {activeShop.slug ? ` (/${activeShop.slug})` : ""}
                  </SheetTitle>
                  <StatusBadge tone={adminShopStatusTone(activeShop.status)}>
                    {getAdminShopStatusMeta(activeShop.status).label}
                  </StatusBadge>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
                  <SheetDescription className="tabular-nums">
                    Mã {activeShop.id.slice(0, 12)}
                    {activeShop.created_at
                      ? ` · Tạo lúc ${formatDateTimeVi(activeShop.created_at)}`
                      : ""}
                  </SheetDescription>
                  {activeIndex >= 0 && (
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={activeIndex <= 0}
                        onClick={() => stepSelection(-1)}
                        aria-label="Hồ sơ trước (K)"
                      >
                        <ChevronLeft className="size-3.5" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={activeIndex >= filtered.length - 1}
                        onClick={() => stepSelection(1)}
                        aria-label="Hồ sơ tiếp theo (J)"
                      >
                        <ChevronRight className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  )}
                </div>
              </SheetHeader>
              {renderInspectorBody(activeShop)}
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      {/* AlertDialog: Confirm shop APPROVE / REJECT */}
      <AdminConfirmActionDialog
        open={Boolean(confirmShopDecision)}
        title={
          confirmShopDecision?.decision === "APPROVE"
            ? "Xác nhận phê duyệt gian hàng"
            : "Xác nhận từ chối hồ sơ gian hàng"
        }
        description={
          confirmShopDecision?.decision === "APPROVE"
            ? `Phê duyệt gian hàng "${confirmShopDecision.shop.name}"${activeShopOwnerName ? ` (chủ sở hữu: ${activeShopOwnerName})` : ""}? Gian hàng sẽ chuyển sang trạng thái Đang hoạt động.`
            : confirmShopDecision
              ? `Từ chối hồ sơ gian hàng "${confirmShopDecision.shop.name}"? Lý do từ chối sẽ được gửi tới người bán.`
              : ""
        }
        confirmLabel={
          confirmShopDecision?.decision === "APPROVE" ? "Xác nhận phê duyệt" : "Xác nhận từ chối"
        }
        confirmVariant={confirmShopDecision?.decision === "APPROVE" ? "default" : "destructive"}
        busy={Boolean(confirmShopDecision && submittingByShopId[confirmShopDecision.shop.id])}
        onCancel={() => setConfirmShopDecision(null)}
        onConfirm={() => {
          if (confirmShopDecision) {
            void handleDecision(confirmShopDecision.shop, confirmShopDecision.decision);
          }
        }}
      >
        {confirmShopDecision && reasonDrafts[confirmShopDecision.shop.id]?.trim() ? (
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs">
            Ghi chú: {reasonDrafts[confirmShopDecision.shop.id].trim()}
          </div>
        ) : null}
      </AdminConfirmActionDialog>

      {/* AlertDialog: Confirm bank account APPROVE / REJECT */}
      <AdminConfirmActionDialog
        open={Boolean(confirmBankDecision)}
        title={
          confirmBankDecision?.decision === "APPROVE"
            ? "Xác nhận duyệt tài khoản ngân hàng"
            : "Xác nhận từ chối tài khoản ngân hàng"
        }
        description={
          confirmBankDecision
            ? confirmBankDecision.decision === "APPROVE"
              ? `Phê duyệt tài khoản ${confirmBankDecision.bankAccount.bank_code} (${confirmBankDecision.bankAccount.account_number_masked}) cho gian hàng "${confirmBankDecision.shop.name}"?`
              : `Từ chối tài khoản ngân hàng của gian hàng "${confirmBankDecision.shop.name}"?`
            : ""
        }
        confirmLabel={
          confirmBankDecision?.decision === "APPROVE" ? "Xác nhận duyệt" : "Xác nhận từ chối"
        }
        confirmVariant={confirmBankDecision?.decision === "APPROVE" ? "default" : "destructive"}
        busy={Boolean(confirmBankDecision && bankSubmittingByShopId[confirmBankDecision.shop.id])}
        onCancel={() => setConfirmBankDecision(null)}
        onConfirm={() => {
          if (confirmBankDecision) {
            void handleBankReview(
              confirmBankDecision.shop,
              confirmBankDecision.bankAccount,
              confirmBankDecision.decision
            );
          }
        }}
      />

      {/* AlertDialog: Confirm 2-person suspend request */}
      <AdminConfirmActionDialog
        open={Boolean(confirmSuspendShop)}
        title="Xác nhận đề xuất tạm khóa gian hàng"
        description={
          confirmSuspendShop
            ? `Gửi yêu cầu tạm khóa gian hàng "${confirmSuspendShop.name}" sang hàng đợi Phê duyệt 2 người?`
            : ""
        }
        confirmLabel="Gửi yêu cầu khóa"
        confirmVariant="destructive"
        busy={Boolean(confirmSuspendShop && suspendBusyByShopId[confirmSuspendShop.id])}
        onCancel={() => setConfirmSuspendShop(null)}
        onConfirm={() => {
          if (confirmSuspendShop) void handleConfirmSuspendRequest(confirmSuspendShop);
        }}
      >
        {confirmSuspendShop && suspendReasonDrafts[confirmSuspendShop.id]?.trim() ? (
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs">
            Lý do: {suspendReasonDrafts[confirmSuspendShop.id].trim()}
          </div>
        ) : null}
      </AdminConfirmActionDialog>

      {/* AlertDialog: Confirm unsuspend */}
      <AdminConfirmActionDialog
        open={Boolean(confirmUnsuspendShop)}
        title="Xác nhận mở khóa gian hàng"
        description={
          confirmUnsuspendShop
            ? `Mở khóa hoạt động trở lại cho gian hàng "${confirmUnsuspendShop.name}"?`
            : ""
        }
        confirmLabel="Xác nhận mở khóa"
        confirmVariant="default"
        busy={Boolean(confirmUnsuspendShop && suspendBusyByShopId[confirmUnsuspendShop.id])}
        onCancel={() => setConfirmUnsuspendShop(null)}
        onConfirm={() => {
          if (confirmUnsuspendShop) void handleUnsuspendShop(confirmUnsuspendShop);
        }}
      />
    </div>
  );
}

export function AdminShopsClient(props: AdminShopsClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Duyệt gian hàng" />}>
      <AdminStateGate
        title="Duyệt gian hàng"
        emptyTitle="Hàng đợi duyệt gian hàng đang trống"
        emptyMessage="Tất cả hồ sơ đăng ký mở gian hàng đã được xử lý."
        errorTitle="Không thể tải hồ sơ gian hàng"
        errorMessage="Không thể kết nối tới dịch vụ gian hàng. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery, initialSelected }) => (
          <ShopApprovalContent
            initialShops={props.initialShops}
            initialBankPendingShops={props.initialBankPendingShops}
            initialBankPendingTotal={props.initialBankPendingTotal}
            loadError={props.loadError}
            initialQuery={initialQuery}
            initialSelected={initialSelected}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
