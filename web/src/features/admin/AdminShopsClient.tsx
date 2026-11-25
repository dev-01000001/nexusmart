"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconChevronLeft, IconChevronRight, IconSearch, StateView } from "@/components";
import { hasOpenModal } from "@/components/modalFocus";
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
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { AdminConfirmDialog, AdminDrawer, SlidingTabBar, isEditableElement } from "./AdminUi";
import { StatusPill } from "./StatusPill";
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
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/gian-hang/page.module.css";

type ShopFilterTab =
  "all" | "PENDING" | "BANK_REVIEW_PENDING" | "ACTIVE" | "REJECTED" | "SUSPENDED";
type ShopSortKey = "pending_first" | "newest" | "name";

export interface AdminShopsClientProps {
  initialShops: GatewayShop[];
  initialBankPendingShops?: GatewayShop[];
  initialBankPendingTotal?: number;
  loadError: string | null;
}

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
  const [selectedId, setSelectedId] = useState<string | null>(
    initialSelected ?? initialShops[0]?.id ?? null
  );
  const [drawerOpen, setDrawerOpen] = useState(false);

  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [submittingByShopId, setSubmittingByShopId] = useState<Record<string, boolean>>({});
  const [conflictNoticeByShopId, setConflictNoticeByShopId] = useState<Record<string, string>>({});

  // Owner lookup for D6 expected_owner_name optimistic lock
  const [ownerByUserId, setOwnerByUserId] = useState<Record<string, AdminShopOwnerState>>({});

  // Bank account inspection & review
  const [bankByShopId, setBankByShopId] = useState<
    Record<string, { loading: boolean; account: GatewayShopBankAccount | null; error?: string }>
  >({});
  const [bankReasonDrafts, setBankReasonDrafts] = useState<Record<string, string>>({});
  const [bankSubmittingByShopId, setBankSubmittingByShopId] = useState<Record<string, boolean>>({});

  // 2-person suspension request & unsuspension
  const [suspendReasonDrafts, setSuspendReasonDrafts] = useState<Record<string, string>>({});
  const [suspendBusyByShopId, setSuspendBusyByShopId] = useState<Record<string, boolean>>({});
  const [confirmSuspendShop, setConfirmSuspendShop] = useState<GatewayShop | null>(null);

  const rowRefs = useRef<Map<string, HTMLButtonElement>>(new Map());
  const deepLinkHandledRef = useRef(false);
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

  const activeIndex = useMemo(() => {
    if (filtered.length === 0) return -1;
    if (selectedId) {
      const idx = filtered.findIndex((s) => s.id === selectedId);
      if (idx >= 0) return idx;
    }
    return 0;
  }, [filtered, selectedId]);

  const activeShop = activeIndex >= 0 ? filtered[activeIndex] : null;
  const activeShopId = activeShop?.id ?? null;
  const activeOwnerId = activeShop?.owner_user_id ?? "";

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
    if (deepLinkHandledRef.current) return;
    deepLinkHandledRef.current = true;
    if (
      initialSelected &&
      typeof window !== "undefined" &&
      window.innerWidth <= 960 &&
      initialShops.some((s) => s.id === initialSelected)
    ) {
      setDrawerOpen(true);
    }
  }, [initialSelected, initialShops]);

  useEffect(() => {
    if (!activeShop && drawerOpen) {
      setDrawerOpen(false);
    }
  }, [activeShop, drawerOpen]);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (filtered.length === 0) return;
      const cur = activeIndex >= 0 ? activeIndex : 0;
      const nextIdx = Math.max(0, Math.min(filtered.length - 1, cur + delta));
      const nextShop = filtered[nextIdx];
      if (!nextShop) return;
      setReasonErrorId(null);
      setSelectedId(nextShop.id);
      rowRefs.current.get(nextShop.id)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    },
    [filtered, activeIndex]
  );

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || isEditableElement(e.target) || hasOpenModal()) {
        return;
      }
      const key = e.key.toLowerCase();
      if (key === "j") {
        e.preventDefault();
        stepSelection(1);
      } else if (key === "k") {
        e.preventDefault();
        stepSelection(-1);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [stepSelection]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải hồ sơ gian hàng"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
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

  const handleDecision = async (
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

  const handleUnsuspendShop = async (shop: GatewayShop) => {
    if (!canUnsuspend || suspendBusyByShopId[shop.id]) return;
    const reason = (suspendReasonDrafts[shop.id] ?? "").trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do mở khóa gian hàng (tối thiểu 4 ký tự).");
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
      showToast(`Đã mở khóa gian hàng "${shop.name}".`);
      router.refresh();
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể mở khóa gian hàng lúc này."));
    } finally {
      setSuspendBusyByShopId((prev) => {
        const next = { ...prev };
        delete next[shop.id];
        return next;
      });
    }
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
      await loadShops();
      showToast(
        decision === "APPROVE"
          ? `Đã phê duyệt tài khoản ngân hàng của "${shop.name}".`
          : `Đã từ chối tài khoản ngân hàng của "${shop.name}".`
      );
      router.refresh();
    } catch (err) {
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

  const renderInspectorBody = (shop: GatewayShop, panelPrefix: "pane" | "drawer") => {
    const inDrawer = panelPrefix === "drawer";
    const draftValue = reasonDrafts[shop.id] ?? "";
    const showReasonError = reasonErrorId === shop.id;
    const meta = getAdminShopStatusMeta(shop.status);
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
    const bankStatusTone =
      bankAcc?.status === "VERIFIED"
        ? ("positive" as const)
        : bankAcc?.status === "REJECTED"
          ? ("negative" as const)
          : ("warning" as const);
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
      <div
        key={shop.id}
        className={`${inDrawer ? styles.drawerContent : shared.detailWellCard} ${shared.inspectorSlide}`}
      >
        {!inDrawer && (
          <div className={shared.detailHeader}>
            <div>
              <h2 className={shared.detailHeading}>
                {shop.name}
                {shop.slug ? ` (/${shop.slug})` : ""}
              </h2>
              <span className={shared.cellSub}>
                Mã {shop.id.slice(0, 12)}
                {shop.created_at ? `, tạo lúc ${formatDateTimeVi(shop.created_at)}` : ""}
              </span>
            </div>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
              <div className={styles.stepperBtns}>
                <button
                  type="button"
                  className={styles.stepperBtn}
                  disabled={activeIndex <= 0}
                  onClick={() => stepSelection(-1)}
                  aria-label="Hồ sơ trước (K)"
                  title="Hồ sơ trước (K)"
                >
                  <IconChevronLeft size={14} />
                </button>
                <button
                  type="button"
                  className={styles.stepperBtn}
                  disabled={activeIndex >= filtered.length - 1}
                  onClick={() => stepSelection(1)}
                  aria-label="Hồ sơ tiếp theo (J)"
                  title="Hồ sơ tiếp theo (J)"
                >
                  <IconChevronRight size={14} />
                </button>
              </div>
            </div>
          </div>
        )}

        {conflictNotice && (
          <p className={shared.validationError} role="alert">
            {conflictNotice}
          </p>
        )}

        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Chủ sở hữu</span>
            <span className={shared.detailVal}>
              {ownerState?.loading
                ? "Đang tải họ tên chủ gian hàng..."
                : displayedOwnerName
                  ? `${displayedOwnerName}${ownerUser?.email ? ` (${ownerUser.email})` : ""}`
                  : shop.owner_user_id || EMPTY_DASH}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Số điện thoại liên hệ</span>
            <span className={`${shared.detailVal} num`}>{shop.contact_phone || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Giấy phép kinh doanh / Hồ sơ</span>
            <span className={shared.detailValPlain}>{shop.business_license_doc || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã gian hàng</span>
            <span className={`${shared.detailValPlain} num`}>{shop.id}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Thời điểm đăng ký</span>
            <span className={`${shared.detailValPlain} num`}>
              {shop.created_at ? formatDateTimeVi(shop.created_at) : EMPTY_DASH}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Cập nhật gần nhất</span>
            <span className={`${shared.detailValPlain} num`}>
              {shop.updated_at ? formatDateTimeVi(shop.updated_at) : EMPTY_DASH}
            </span>
          </div>
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Mô tả gian hàng</span>
            <span className={shared.detailValPlain}>{shop.description || EMPTY_DASH}</span>
          </div>
          {shop.rejection_reason && (
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <span className={shared.detailKey}>Ghi chú thẩm định gần nhất</span>
              <span className={shared.priorityText}>{shop.rejection_reason}</span>
            </div>
          )}
          {shop.suspended_reason && (
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <span className={shared.detailKey}>Lý do tạm ngưng gian hàng</span>
              <span className={shared.validationError}>
                {shop.suspended_reason}
                {shop.suspended_at ? ` · Từ ${formatDateTimeVi(shop.suspended_at)}` : ""}
              </span>
            </div>
          )}
        </div>

        {/* Shop approval review (PENDING only, D6 expected_owner_name lock) */}
        {canReviewShop && isPendingShop && (
          <div className={shared.detailActionBar}>
            <div className={shared.detailNoteGroup}>
              <label htmlFor={`${panelPrefix}-shop-note-${shop.id}`} className={shared.fieldLabel}>
                Ghi chú thẩm định hoặc lý do từ chối
              </label>
              <input
                id={`${panelPrefix}-shop-note-${shop.id}`}
                type="text"
                className={shared.textInput}
                value={draftValue}
                onChange={(e) => {
                  setReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }));
                  if (showReasonError) setReasonErrorId(null);
                }}
                placeholder="Bắt buộc nhập lý do khi Từ chối hoặc Yêu cầu bổ sung..."
              />
              {showReasonError && (
                <span className={shared.validationError}>
                  Vui lòng nhập lý do cụ thể trước khi từ chối hoặc yêu cầu bổ sung.
                </span>
              )}
            </div>

            {!canApprove && (
              <div
                className={shared.detailHeaderMeta}
                role={ownerState?.loading ? "status" : "alert"}
              >
                <span className={ownerState?.loading ? shared.savedStamp : shared.validationError}>
                  {ownerState?.loading
                    ? "Đang tải họ tên chủ gian hàng để đối chiếu..."
                    : `${ownerState?.error ?? "Chưa có họ tên chủ gian hàng để đối chiếu."} Chưa thể phê duyệt cho tới khi tên chủ hiển thị.`}
                </span>
                {shop.owner_user_id && canReadUsers && (
                  <button
                    type="button"
                    className={shared.rowActionBtn}
                    disabled={ownerState?.loading}
                    onClick={() => void loadShopOwner(shop.owner_user_id)}
                  >
                    Tải lại
                  </button>
                )}
              </div>
            )}

            <div className={shared.detailButtons}>
              <Button
                variant="primary"
                size="sm"
                shape="app"
                disabled={isBusy || !canApprove}
                onClick={() => void handleDecision(shop, "APPROVE")}
              >
                {isBusy ? "Đang xử lý..." : "Phê duyệt"}
              </Button>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={isBusy}
                onClick={() => void handleDecision(shop, "REQUEST_INFO")}
              >
                Yêu cầu bổ sung
              </Button>
              <Button
                variant="danger"
                size="sm"
                shape="app"
                disabled={isBusy}
                onClick={() => void handleDecision(shop, "REJECT")}
              >
                Từ chối
              </Button>
            </div>
          </div>
        )}

        {/* 2-person shop suspension request */}
        {shop.status.toUpperCase() === "ACTIVE" && canRequestSuspend && (
          <div className={shared.detailActionBar}>
            <div className={shared.detailNoteGroup}>
              <label
                htmlFor={`${panelPrefix}-suspend-note-${shop.id}`}
                className={shared.fieldLabel}
              >
                Đề xuất tạm khóa gian hàng (duyệt 2 người)
              </label>
              <input
                id={`${panelPrefix}-suspend-note-${shop.id}`}
                type="text"
                className={shared.textInput}
                value={suspendReasonDrafts[shop.id] ?? ""}
                onChange={(e) =>
                  setSuspendReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }))
                }
                placeholder="Nhập lý do vi phạm cần tạm khóa..."
              />
            </div>
            <div className={shared.detailButtons}>
              <Button
                variant="danger"
                size="sm"
                shape="app"
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
          <div className={shared.detailActionBar}>
            <div className={shared.detailNoteGroup}>
              <label
                htmlFor={`${panelPrefix}-unsuspend-note-${shop.id}`}
                className={shared.fieldLabel}
              >
                Mở khóa gian hàng
              </label>
              <input
                id={`${panelPrefix}-unsuspend-note-${shop.id}`}
                type="text"
                className={shared.textInput}
                value={suspendReasonDrafts[shop.id] ?? ""}
                onChange={(e) =>
                  setSuspendReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }))
                }
                placeholder="Nhập lý do mở khóa gian hàng..."
              />
            </div>
            <div className={shared.detailButtons}>
              <Button
                variant="primary"
                size="sm"
                shape="app"
                disabled={Boolean(suspendBusyByShopId[shop.id])}
                onClick={() => void handleUnsuspendShop(shop)}
              >
                {suspendBusyByShopId[shop.id] ? "Đang xử lý..." : "Mở khóa gian hàng"}
              </Button>
            </div>
          </div>
        )}

        {/* Shop Bank Account Inspection & Approval */}
        {canReadBank && (
          <div className={styles.bankSection}>
            <div className={`${shared.detailHeader} ${styles.bankSectionHeader}`}>
              <h3 className={shared.detailHeading}>Tài khoản ngân hàng nhận giải ngân</h3>
              <div className={shared.detailHeaderMeta}>
                {bankAcc ? (
                  <StatusPill label={bankStatusLabel} tone={bankStatusTone} />
                ) : (
                  !bankState?.loading &&
                  !bankState?.error && <StatusPill label="Chưa khai báo" tone="neutral" />
                )}
                <button
                  type="button"
                  className={shared.rowActionBtn}
                  onClick={() => void loadShopBankAccount(shop.id)}
                >
                  Tải lại
                </button>
              </div>
            </div>

            {!bankState || bankState.loading ? (
              <span className={shared.savedStamp}>Đang tải thông tin tài khoản ngân hàng...</span>
            ) : bankState.error ? (
              <span className={shared.validationError} role="alert">
                {bankState.error}
              </span>
            ) : !bankAcc ? (
              <span className={shared.savedStamp}>
                Chưa khai báo tài khoản ngân hàng nhận giải ngân.
              </span>
            ) : (
              <>
                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Ngân hàng</span>
                    <span className={shared.detailVal}>
                      {bankAcc.bank_code} · {bankAcc.bank_name}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Số tài khoản</span>
                    <span className={`${shared.detailVal} num`}>
                      {bankAcc.account_number_masked}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Chủ tài khoản</span>
                    <span className={shared.detailVal}>
                      {bankAcc.account_holder_name} (
                      {bankAcc.holder_name_matched ? "Khớp tên chủ shop" : "Khác tên chủ shop"})
                    </span>
                  </div>
                  {bankAcc.payout_locked_until && (
                    <div className={shared.detailField}>
                      <span className={shared.detailKey}>Khóa giải ngân an toàn đến</span>
                      <span className={shared.detailValPlain}>
                        {formatDateTimeVi(bankAcc.payout_locked_until)}
                      </span>
                    </div>
                  )}
                  {bankAcc.review_note && (
                    <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                      <span className={shared.detailKey}>
                        {bankAcc.status === "REJECTED"
                          ? "Lý do từ chối ngân hàng"
                          : "Ghi chú thẩm định ngân hàng"}
                      </span>
                      <span
                        className={
                          bankAcc.status === "REJECTED"
                            ? shared.validationError
                            : shared.detailValPlain
                        }
                      >
                        {bankAcc.review_note}
                      </span>
                    </div>
                  )}
                </div>

                {bankAcc.status === "PENDING_ADMIN_REVIEW" && canReviewBank && (
                  <div className={shared.detailActionBar}>
                    <div className={shared.detailNoteGroup}>
                      <label
                        htmlFor={`${panelPrefix}-bank-note-${shop.id}`}
                        className={shared.fieldLabel}
                      >
                        Ghi chú thẩm định tài khoản ngân hàng
                      </label>
                      <input
                        id={`${panelPrefix}-bank-note-${shop.id}`}
                        type="text"
                        className={shared.textInput}
                        value={bankReasonDrafts[shop.id] ?? ""}
                        onChange={(e) =>
                          setBankReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }))
                        }
                        placeholder="Bắt buộc nhập lý do khi từ chối tài khoản ngân hàng..."
                      />
                    </div>
                    <div className={shared.detailButtons}>
                      <Button
                        variant="primary"
                        size="sm"
                        shape="app"
                        disabled={bankBusy}
                        onClick={() => void handleBankReview(shop, bankAcc, "APPROVE")}
                      >
                        {bankBusy ? "Đang xử lý..." : "Duyệt tài khoản ngân hàng"}
                      </Button>
                      <Button
                        variant="danger"
                        size="sm"
                        shape="app"
                        disabled={bankBusy}
                        onClick={() => void handleBankReview(shop, bankAcc, "REJECT")}
                      >
                        Từ chối ngân hàng
                      </Button>
                    </div>
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </div>
    );
  };

  const tabs: Array<{ id: ShopFilterTab; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: counts.all },
    { id: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
    ...(canReadBank
      ? [
          {
            id: "BANK_REVIEW_PENDING" as const,
            label: "Chờ duyệt tài khoản ngân hàng",
            count: counts.BANK_REVIEW_PENDING,
          },
        ]
      : []),
    { id: "ACTIVE", label: "Đang hoạt động", count: counts.ACTIVE },
    { id: "REJECTED", label: "Từ chối", count: counts.REJECTED },
    { id: "SUSPENDED", label: "Tạm ngưng", count: counts.SUSPENDED },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Duyệt gian hàng</h1>
        </div>
        <div className={shared.titleActions}>
          <span className={shared.kbdHint} aria-label="Phím tắt chuyển hồ sơ">
            <kbd className={shared.kbdKey}>J</kbd>
            <kbd className={shared.kbdKey}>K</kbd>
            <span>chuyển hồ sơ</span>
          </span>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            loading={refreshing}
            onClick={() => void handleRefreshAll()}
          >
            Làm mới
          </Button>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={activeTab}
        onChange={setActiveTab}
        ariaLabel="Lọc trạng thái hồ sơ gian hàng"
      />

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
              placeholder="Tìm tên gian hàng, SĐT, mã hồ sơ..."
              aria-label="Tìm kiếm hồ sơ gian hàng"
            />
          </div>
          <select
            className={shared.toolbarSelect}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as ShopSortKey)}
            aria-label="Sắp xếp hồ sơ gian hàng"
          >
            <option value="pending_first">Ưu tiên chờ duyệt</option>
            <option value="newest">Mới đăng ký gần nhất</option>
            <option value="name">Tên gian hàng (A đến Z)</option>
          </select>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} gian hàng</span>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không tìm thấy hồ sơ gian hàng phù hợp.</p>
      ) : (
        <div className={shared.reviewSplit}>
          <div className={shared.queueColumn}>
            <div className={shared.queueHead}>
              <span>Hàng đợi hồ sơ</span>
              <span>
                {activeIndex + 1} / {formatInt(filtered.length)}
              </span>
            </div>
            <ul className={shared.queueList} aria-label="Danh sách hồ sơ gian hàng">
              {filtered.map((shop) => {
                const meta = getAdminShopStatusMeta(shop.status);
                const isSelected = activeShop?.id === shop.id;
                return (
                  <li key={shop.id} className={shared.queueItem}>
                    <button
                      ref={(node) => {
                        if (node) rowRefs.current.set(shop.id, node);
                        else rowRefs.current.delete(shop.id);
                      }}
                      type="button"
                      className={`${shared.queueItemBtn} ${isSelected ? shared.queueItemActive : ""}`}
                      aria-current={isSelected ? "true" : undefined}
                      onClick={() => {
                        setReasonErrorId(null);
                        setSelectedId(shop.id);
                        if (typeof window !== "undefined" && window.innerWidth <= 960) {
                          setDrawerOpen(true);
                        }
                      }}
                    >
                      <span className={shared.queueItemBody}>
                        <span className={shared.queueItemTop}>
                          <span className={shared.queueItemTitle}>{shop.name}</span>
                          <span className={styles.statusPillGroup}>
                            <StatusPill label={meta.label} tone={meta.tone} />
                            {shop.bank_review_pending && (
                              <StatusPill label="Chờ duyệt NH" tone="warning" />
                            )}
                          </span>
                        </span>
                        <span className={shared.queueItemMeta}>
                          {[
                            shop.contact_phone || shop.id.slice(0, 12),
                            shop.created_at ? formatShortTimeDateVi(shop.created_at) : "",
                          ]
                            .filter(Boolean)
                            .join(", ")}
                        </span>
                        <span className={shared.queueItemMeta}>
                          {shop.business_license_doc || "Chưa đính kèm GPKD"}
                        </span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>

          {activeShop && (
            <section className={shared.inspectorPane} aria-label="Khung thẩm định hồ sơ gian hàng">
              {renderInspectorBody(activeShop, "pane")}
            </section>
          )}
        </div>
      )}

      <AdminDrawer
        open={drawerOpen && Boolean(activeShop)}
        onClose={() => setDrawerOpen(false)}
        title={activeShop?.name ?? ""}
        subtitle={activeShop?.created_at ? formatDateTimeVi(activeShop.created_at) : undefined}
        headerBadge={
          activeShop ? (
            <StatusPill
              label={getAdminShopStatusMeta(activeShop.status).label}
              tone={getAdminShopStatusMeta(activeShop.status).tone}
            />
          ) : undefined
        }
        width="lg"
      >
        {activeShop && renderInspectorBody(activeShop, "drawer")}
      </AdminDrawer>

      <AdminConfirmDialog
        open={Boolean(confirmSuspendShop)}
        title="Xác nhận đề xuất tạm khóa gian hàng"
        description={
          confirmSuspendShop
            ? `Gửi yêu cầu tạm khóa gian hàng "${confirmSuspendShop.name}" sang hàng đợi Phê duyệt 2 người?`
            : ""
        }
        confirmLabel="Gửi yêu cầu khóa"
        busy={Boolean(confirmSuspendShop && suspendBusyByShopId[confirmSuspendShop.id])}
        onCancel={() => setConfirmSuspendShop(null)}
        onConfirm={() => {
          if (confirmSuspendShop) void handleConfirmSuspendRequest(confirmSuspendShop);
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
