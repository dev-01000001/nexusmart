"use client";

import React, { Suspense, useCallback, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconPlus, IconSearch, StateView } from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import {
  formatDateTimeVi,
  formatInt,
  formatVnIsoDate,
  formatVnd,
  formatVoucherBenefit,
} from "@/lib/format";
import type { GatewayCategory, GatewayVoucher, PaginatedResponse } from "@/lib/gateway-types";
import { localVnInputToIso, type PillTone } from "@/features/admin/adminMeta";
import type {
  GwAdminFlashSaleExtended,
  GwAdminFlashSaleItemExtended,
} from "@/features/admin/adminApiTypes";
import { getVoucherScopeMeta } from "@/features/admin/adminComplaintsTypes";
import {
  canEndFlashSaleEarly,
  computeFlashDiscountPercent,
  getFlashSaleWindowMeta,
  MAX_FLASH_SALE_DISCOUNT_PERCENT,
  validateCreateFlashSaleInput,
} from "@/features/admin/AdminFlashSaleHelpers";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { AdminConfirmDialog, AdminDrawer, SlidingTabBar } from "@/features/admin/AdminUi";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type FlashTab = "slots" | "windows" | "vouchers" | "coins";
type ItemFilter = "all" | "PENDING" | "APPROVED" | "REJECTED";
type WindowStatusFilter = "" | "UPCOMING" | "ACTIVE" | "ENDED";

const REJECT_REASON = "Giá Flash sale hoặc số suất chưa phù hợp với khung giờ này";

export interface AdminFlashSaleClientProps {
  initialFlashSales: GwAdminFlashSaleExtended[];
  shopNames: Record<string, string>;
  categories?: GatewayCategory[];
  initialVouchers?: GatewayVoucher[];
  loadError: string | null;
  serverNowMs?: number;
}

function defaultWindowRange(baseMs: number): { startLocal: string; endLocal: string } {
  const datePart = formatVnIsoDate(baseMs + 24 * 3600_000);
  return {
    startLocal: `${datePart}T12:00`,
    endLocal: `${datePart}T14:00`,
  };
}

function getItemStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "APPROVED":
      return { label: "Đã duyệt suất", tone: "positive" };
    case "REJECTED":
      return { label: "Đã từ chối", tone: "negative" };
    case "PENDING":
      return { label: "Chờ duyệt", tone: "warning" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

function getVoucherStatusPill(v: GatewayVoucher): { label: string; tone: PillTone } {
  const st = (v.status ?? "").toUpperCase();
  if (st === "DELETED") return { label: "Đã gỡ", tone: "negative" };
  if (st === "PAUSED" || !v.is_active) return { label: "Tạm dừng", tone: "neutral" };
  return { label: "Đang hiệu lực", tone: "positive" };
}

type DrawerState =
  | { kind: "create" }
  | { kind: "slot"; flashSaleId: string; itemId: string }
  | { kind: "window"; flashSaleId: string }
  | null;

function FlashSaleWorkspace({
  initialFlashSales,
  shopNames,
  categories = [],
  initialVouchers = [],
  loadError,
  serverNowMs,
  initialQuery,
}: AdminFlashSaleClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canWriteFlashSales = roleHasCapability(user.role, "flash_sales.write");
  const canApproveItems = roleHasCapability(user.role, "flash_sales.items.approve");
  const canReadVouchers = roleHasCapability(user.role, "vouchers.read");

  const [flashSales, setFlashSales] = useState<GwAdminFlashSaleExtended[]>(initialFlashSales);
  const [vouchers, setVouchers] = useState<GatewayVoucher[]>(initialVouchers);
  const [vouchersLoading, setVouchersLoading] = useState(false);

  const initialTotalItems = initialFlashSales.reduce((s, f) => s + (f.items?.length ?? 0), 0);
  const [activeTab, setActiveTab] = useState<FlashTab>(initialTotalItems > 0 ? "slots" : "windows");
  const [itemFilter, setItemFilter] = useState<ItemFilter>("all");
  const [windowStatusFilter, setWindowStatusFilter] = useState<WindowStatusFilter>("");
  const [windowsLoading, setWindowsLoading] = useState(false);
  const [query, setQuery] = useState(initialQuery);
  const [now] = useState(() => serverNowMs ?? Date.now());

  const [drawer, setDrawer] = useState<DrawerState>(null);
  const [name, setName] = useState("");
  const [startLocal, setStartLocal] = useState(() => defaultWindowRange(now).startLocal);
  const [endLocal, setEndLocal] = useState(() => defaultWindowRange(now).endLocal);
  const [minDiscountPercent, setMinDiscountPercent] = useState("10");
  const [selectedCategoryIds, setSelectedCategoryIds] = useState<string[]>([]);
  const [creating, setCreating] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const [busyItemId, setBusyItemId] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<"approve" | "reject" | null>(null);
  const [rejectDraft, setRejectDraft] = useState("");

  const [confirmEndWindow, setConfirmEndWindow] = useState<GwAdminFlashSaleExtended | null>(null);
  const [endingWindow, setEndingWindow] = useState(false);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) map.set(c.id, c.name);
    return map;
  }, [categories]);

  const reloadFlashSales = useCallback(
    async (statusFilter: WindowStatusFilter) => {
      setWindowsLoading(true);
      try {
        const qs = new URLSearchParams({ page: "1", page_size: "50" });
        if (statusFilter) qs.set("status", statusFilter);
        const res = await api<PaginatedResponse<GwAdminFlashSaleExtended>>(
          `/api/admin/flash-sales?${qs.toString()}`
        );
        setFlashSales(res.items ?? []);
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể tải danh sách khung giờ Flash sale."));
      } finally {
        setWindowsLoading(false);
      }
    },
    [showToast]
  );

  const reloadVouchers = useCallback(async () => {
    if (!canReadVouchers) return;
    setVouchersLoading(true);
    try {
      const res = await api<{ vouchers: GatewayVoucher[] }>(
        "/api/admin/vouchers?include_inactive=true"
      );
      setVouchers(res.vouchers ?? []);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tải danh sách voucher sàn."));
    } finally {
      setVouchersLoading(false);
    }
  }, [canReadVouchers, showToast]);

  const entries = useMemo(
    () => flashSales.flatMap((fs) => (fs.items ?? []).map((item) => ({ flashSale: fs, item }))),
    [flashSales]
  );

  const counts = useMemo(() => {
    const byStatus = { PENDING: 0, APPROVED: 0, REJECTED: 0 };
    for (const { item } of entries) {
      const s = item.status.toUpperCase();
      if (s === "PENDING" || s === "APPROVED" || s === "REJECTED") byStatus[s] += 1;
    }
    const live = flashSales.filter(
      (fs) => getFlashSaleWindowMeta(fs, now).state === "ACTIVE"
    ).length;
    return { ...byStatus, all: entries.length, live };
  }, [entries, flashSales, now]);

  const shopLabel = useCallback(
    (shopId: string) => shopNames[shopId] ?? `Gian hàng ${shopId.slice(0, 8)}`,
    [shopNames]
  );

  const visibleEntries = useMemo(() => {
    const q = query.trim().toLowerCase();
    return entries.filter(({ flashSale, item }) => {
      if (itemFilter !== "all" && item.status.toUpperCase() !== itemFilter) return false;
      if (!q) return true;
      return (
        shopLabel(item.shop_id).toLowerCase().includes(q) ||
        flashSale.name.toLowerCase().includes(q) ||
        item.sku_id.toLowerCase().includes(q) ||
        item.id.toLowerCase().includes(q)
      );
    });
  }, [entries, itemFilter, query, shopLabel]);

  const visibleWindows = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return flashSales;
    return flashSales.filter(
      (fs) => fs.name.toLowerCase().includes(q) || fs.id.toLowerCase().includes(q)
    );
  }, [flashSales, query]);

  const visibleVouchers = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return vouchers;
    return vouchers.filter(
      (v) =>
        v.code.toLowerCase().includes(q) ||
        v.name.toLowerCase().includes(q) ||
        v.scope.toLowerCase().includes(q)
    );
  }, [vouchers, query]);

  const toggleCategory = (catId: string) => {
    setSelectedCategoryIds((prev) =>
      prev.includes(catId) ? prev.filter((id) => id !== catId) : [...prev, catId]
    );
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (creating || !canWriteFlashSales) return;
    const startsIso = localVnInputToIso(startLocal);
    const endsIso = localVnInputToIso(endLocal);
    const validated = validateCreateFlashSaleInput({
      name,
      starts_at: startsIso,
      ends_at: endsIso,
      min_discount_percent: minDiscountPercent === "" ? 0 : Number(minDiscountPercent),
      category_ids: selectedCategoryIds,
    });
    if (!validated.ok) {
      setFormError(validated.message);
      return;
    }

    setFormError(null);
    setCreating(true);
    try {
      const res = await api<{ flash_sale: GwAdminFlashSaleExtended }>("/api/admin/flash-sales", {
        method: "POST",
        body: validated.value,
      });
      const created: GwAdminFlashSaleExtended = {
        ...res.flash_sale,
        items: res.flash_sale.items ?? [],
      };
      setFlashSales((prev) => [created, ...prev]);
      setName("");
      setMinDiscountPercent("10");
      setSelectedCategoryIds([]);
      setDrawer(null);
      setActiveTab("windows");
      showToast(`Đã mở khung giờ "${created.name}".`);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể tạo khung giờ Flash sale lúc này.");
      setFormError(msg);
      showToast(msg);
    } finally {
      setCreating(false);
    }
  };

  const handleConfirmEndEarly = async () => {
    if (!confirmEndWindow || endingWindow || !canWriteFlashSales) return;
    setEndingWindow(true);
    try {
      const res = await api<{
        flash_sale?: GwAdminFlashSaleExtended;
        window?: GwAdminFlashSaleExtended;
        released_items?: number;
      }>(`/api/admin/flash-sales/${encodeURIComponent(confirmEndWindow.id)}/end`, {
        method: "POST",
      });
      const updated = res.flash_sale ?? res.window;
      setFlashSales((prev) =>
        prev.map((fs) =>
          fs.id === confirmEndWindow.id
            ? {
                ...fs,
                ...(updated ?? {}),
                status: updated?.status ?? "ENDED",
                ends_at: updated?.ends_at ?? new Date().toISOString(),
              }
            : fs
        )
      );
      const released = res.released_items ?? 0;
      showToast(
        released > 0
          ? `Đã kết thúc sớm "${confirmEndWindow.name}" và giải phóng ${formatInt(released)} suất.`
          : `Đã kết thúc sớm khung giờ "${confirmEndWindow.name}".`
      );
      setConfirmEndWindow(null);
      router.refresh();
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể kết thúc sớm khung giờ lúc này."));
    } finally {
      setEndingWindow(false);
    }
  };

  const handleDecision = async (
    item: GwAdminFlashSaleItemExtended,
    approve: boolean,
    customReason?: string
  ) => {
    if (busyItemId || !canApproveItems) return;
    setBusyItemId(item.id);
    setBusyAction(approve ? "approve" : "reject");
    try {
      const reason = approve ? "" : customReason?.trim() || REJECT_REASON;
      const res = await api<{ item: GwAdminFlashSaleItemExtended }>(
        `/api/admin/flash-sale-items/${encodeURIComponent(item.id)}/approve`,
        { method: "POST", body: { approve, reason } }
      );
      setFlashSales((prev) =>
        prev.map((fs) => ({
          ...fs,
          items: (fs.items ?? []).map((it) => (it.id === item.id ? { ...it, ...res.item } : it)),
        }))
      );
      setRejectDraft("");
      showToast(
        approve
          ? `Đã duyệt suất của ${shopLabel(item.shop_id)}.`
          : `Đã từ chối suất của ${shopLabel(item.shop_id)}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật trạng thái suất Flash sale."));
    } finally {
      setBusyItemId(null);
      setBusyAction(null);
    }
  };

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải khung giờ Flash sale"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const decisionButtons = (item: GwAdminFlashSaleItemExtended) => {
    if (!canApproveItems) return <span className={shared.cellSub}>Chỉ xem</span>;
    const status = item.status.toUpperCase();
    const anyBusy = Boolean(busyItemId);
    return (
      <div className={styles.actionInline}>
        <button
          type="button"
          className={shared.rowActionBtn}
          disabled={anyBusy || status === "APPROVED"}
          onClick={(e) => {
            e.stopPropagation();
            void handleDecision(item, true);
          }}
        >
          Duyệt suất
        </button>
        <button
          type="button"
          className={`${shared.rowActionBtn} ${shared.rowActionBtnDanger}`}
          disabled={anyBusy || status === "REJECTED"}
          onClick={(e) => {
            e.stopPropagation();
            void handleDecision(item, false);
          }}
        >
          Từ chối
        </button>
      </div>
    );
  };

  const drawerSlotData =
    drawer?.kind === "slot"
      ? (entries.find(
          (e) => e.flashSale.id === drawer.flashSaleId && e.item.id === drawer.itemId
        ) ?? null)
      : null;

  const drawerWindowData =
    drawer?.kind === "window"
      ? (flashSales.find((fs) => fs.id === drawer.flashSaleId) ?? null)
      : null;

  const mainTabs: Array<{ id: FlashTab; label: string; count?: number }> = [
    { id: "slots", label: "Suất gian hàng đăng ký", count: entries.length },
    { id: "windows", label: "Khung giờ Flash sale", count: flashSales.length },
    ...(canReadVouchers
      ? [{ id: "vouchers" as const, label: "Voucher sàn (chỉ đọc)", count: vouchers.length }]
      : []),
    { id: "coins", label: "Xu (chỉ đọc)" },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Flash sale</h1>
        </div>
        {canWriteFlashSales && (
          <div className={shared.titleActions}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              onClick={() => {
                setFormError(null);
                setDrawer({ kind: "create" });
              }}
            >
              <IconPlus size={14} />
              <span>Mở khung giờ</span>
            </Button>
          </div>
        )}
      </header>

      <section className={shared.kpiStrip} aria-label="Tổng hợp khung giờ Flash sale">
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Khung giờ</span>
          <span className={shared.kpiValue}>{formatInt(flashSales.length)}</span>
          <span className={shared.kpiCaption}>{formatInt(counts.live)} đang diễn ra</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Suất chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.PENDING)}</span>
          <span className={shared.kpiCaption}>Cần thẩm định</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Suất đã duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.APPROVED)}</span>
          <span className={shared.kpiCaption}>Hiển thị trên site</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Suất từ chối</span>
          <span className={shared.kpiValue}>{formatInt(counts.REJECTED)}</span>
          <span className={shared.kpiCaption}>Trên {formatInt(counts.all)} đăng ký</span>
        </div>
      </section>

      <SlidingTabBar
        tabs={mainTabs}
        activeId={activeTab}
        onChange={setActiveTab}
        ariaLabel="Chuyển giữa suất đăng ký, khung giờ và chế độ chỉ đọc"
        rightSlot={<span className={shared.resultCount}>Giờ Việt Nam (GMT+7)</span>}
      />

      {activeTab !== "coins" && (
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
                placeholder={
                  activeTab === "slots"
                    ? "Tìm gian hàng, khung giờ, mã SKU..."
                    : activeTab === "vouchers"
                      ? "Tìm mã voucher, tên chương trình..."
                      : "Tìm tên khung giờ..."
                }
                aria-label="Tìm kiếm Flash sale và khuyến mãi"
              />
            </div>

            {activeTab === "slots" && (
              <select
                className={shared.toolbarSelect}
                value={itemFilter}
                onChange={(e) => setItemFilter(e.target.value as ItemFilter)}
                aria-label="Lọc trạng thái suất"
              >
                <option value="all">Tất cả ({formatInt(counts.all)})</option>
                <option value="PENDING">Chờ duyệt ({formatInt(counts.PENDING)})</option>
                <option value="APPROVED">Đã duyệt ({formatInt(counts.APPROVED)})</option>
                <option value="REJECTED">Từ chối ({formatInt(counts.REJECTED)})</option>
              </select>
            )}

            {activeTab === "windows" && (
              <select
                className={shared.toolbarSelect}
                value={windowStatusFilter}
                onChange={(e) => {
                  const next = e.target.value as WindowStatusFilter;
                  setWindowStatusFilter(next);
                  void reloadFlashSales(next);
                }}
                aria-label="Lọc trạng thái khung giờ"
              >
                <option value="">Tất cả trạng thái</option>
                <option value="ACTIVE">Đang diễn ra</option>
                <option value="UPCOMING">Sắp diễn ra</option>
                <option value="ENDED">Đã kết thúc</option>
              </select>
            )}
          </div>

          <div className={shared.toolbarRight}>
            {activeTab === "vouchers" ? (
              <Button
                variant="outline"
                size="sm"
                shape="app"
                loading={vouchersLoading}
                onClick={() => void reloadVouchers()}
              >
                Làm mới
              </Button>
            ) : (
              <span className={shared.resultCount}>
                {activeTab === "slots"
                  ? `${formatInt(visibleEntries.length)} suất`
                  : windowsLoading
                    ? "Đang tải..."
                    : `${formatInt(visibleWindows.length)} khung giờ`}
              </span>
            )}
          </div>
        </div>
      )}

      {/* TAB 1: SLOTS */}
      {activeTab === "slots" &&
        (visibleEntries.length === 0 ? (
          <p className={shared.emptyRow}>Chưa có suất đăng ký nào phù hợp bộ lọc.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Gian hàng &amp; SKU</th>
                    <th>Khung giờ</th>
                    <th className={shared.colNum}>Giá Flash sale</th>
                    <th className={shared.colNum}>Suất còn / tổng</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleEntries.map(({ flashSale, item }) => {
                    const meta = getItemStatusMeta(item.status);
                    const selected = drawer?.kind === "slot" && drawer.itemId === item.id;
                    const discountPct = computeFlashDiscountPercent(
                      item.original_price_vnd,
                      item.flash_price_vnd
                    );
                    return (
                      <tr
                        key={item.id}
                        className={`${shared.rowInteractive} ${selected ? shared.rowSelected : ""}`}
                        onClick={() => {
                          setRejectDraft("");
                          setDrawer({ kind: "slot", flashSaleId: flashSale.id, itemId: item.id });
                        }}
                      >
                        <td>
                          <button
                            type="button"
                            className={shared.rowTriggerBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              setRejectDraft("");
                              setDrawer({
                                kind: "slot",
                                flashSaleId: flashSale.id,
                                itemId: item.id,
                              });
                            }}
                          >
                            <span className={shared.cellPrimary}>{shopLabel(item.shop_id)}</span>
                            <span className={shared.cellSub}>
                              SKU {item.sku_id.slice(0, 8)}, tối đa {formatInt(item.per_user_limit)}
                              /người
                            </span>
                          </button>
                        </td>
                        <td>{flashSale.name}</td>
                        <td className={shared.colNum}>
                          <div className={shared.cellPrimary}>
                            {formatVnd(item.flash_price_vnd)}
                          </div>
                          {discountPct !== null && (
                            <div className={styles.discountTag}>-{discountPct}%</div>
                          )}
                        </td>
                        <td className={shared.colNum}>
                          {formatInt(item.remaining_slots)} / {formatInt(item.total_slots)}
                        </td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                        <td className={shared.colAction}>{decisionButtons(item)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {visibleEntries.map(({ flashSale, item }) => {
                const meta = getItemStatusMeta(item.status);
                return (
                  <div key={item.id} className={shared.mobileItem}>
                    <button
                      type="button"
                      className={shared.mobileItemBtn}
                      onClick={() => {
                        setRejectDraft("");
                        setDrawer({ kind: "slot", flashSaleId: flashSale.id, itemId: item.id });
                      }}
                    >
                      <span className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{shopLabel(item.shop_id)}</span>
                        <span className={shared.mobileMetric}>
                          {formatVnd(item.flash_price_vnd)}
                        </span>
                      </span>
                      <span className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {flashSale.name}, còn {formatInt(item.remaining_slots)}/
                          {formatInt(item.total_slots)} suất
                        </span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </span>
                    </button>
                  </div>
                );
              })}
            </div>
          </>
        ))}

      {/* TAB 2: WINDOWS */}
      {activeTab === "windows" &&
        (visibleWindows.length === 0 ? (
          <p className={shared.emptyRow}>Chưa có khung giờ nào.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Khung giờ &amp; điều kiện</th>
                    <th>Thời gian</th>
                    <th>Trạng thái</th>
                    <th className={shared.colNum}>SKU / Đã duyệt</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleWindows.map((fs) => {
                    const items = fs.items ?? [];
                    const approved = items.filter(
                      (it) => it.status.toUpperCase() === "APPROVED"
                    ).length;
                    const meta = getFlashSaleWindowMeta(fs, now);
                    const canEnd = canWriteFlashSales && canEndFlashSaleEarly(fs, now);
                    const selected = drawer?.kind === "window" && drawer.flashSaleId === fs.id;
                    const catNames = (fs.category_ids ?? [])
                      .map((id) => categoryNameById.get(id) ?? id.slice(0, 8))
                      .slice(0, 3);
                    return (
                      <tr
                        key={fs.id}
                        className={`${shared.rowInteractive} ${selected ? shared.rowSelected : ""}`}
                        onClick={() => setDrawer({ kind: "window", flashSaleId: fs.id })}
                      >
                        <td>
                          <button
                            type="button"
                            className={shared.rowTriggerBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              setDrawer({ kind: "window", flashSaleId: fs.id });
                            }}
                          >
                            <span className={shared.cellPrimary}>{fs.name}</span>
                            <span className={shared.cellSub}>
                              Giảm tối thiểu {formatInt(fs.min_discount_percent ?? 0)}%
                              {catNames.length > 0
                                ? ` · ${catNames.join(", ")}`
                                : " · Mọi danh mục"}
                            </span>
                          </button>
                        </td>
                        <td>
                          <div>{fs.starts_at ? formatDateTimeVi(fs.starts_at) : "Chưa có"}</div>
                          <div className={shared.cellSub}>
                            đến {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "chưa có"}
                          </div>
                        </td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                        <td className={shared.colNum}>
                          {formatInt(items.length)} / {formatInt(approved)}
                        </td>
                        <td className={shared.colAction}>
                          {canEnd ? (
                            <button
                              type="button"
                              className={`${shared.rowActionBtn} ${shared.rowActionBtnDanger}`}
                              onClick={(e) => {
                                e.stopPropagation();
                                setConfirmEndWindow(fs);
                              }}
                            >
                              Kết thúc sớm
                            </button>
                          ) : (
                            <span className={shared.cellSub}>Chi tiết</span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {visibleWindows.map((fs) => {
                const meta = getFlashSaleWindowMeta(fs, now);
                return (
                  <div key={fs.id} className={shared.mobileItem}>
                    <button
                      type="button"
                      className={shared.mobileItemBtn}
                      onClick={() => setDrawer({ kind: "window", flashSaleId: fs.id })}
                    >
                      <span className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{fs.name}</span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </span>
                      <span className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          Tối thiểu {formatInt(fs.min_discount_percent ?? 0)}% ·{" "}
                          {formatInt((fs.items ?? []).length)} SKU
                        </span>
                      </span>
                    </button>
                  </div>
                );
              })}
            </div>
          </>
        ))}

      {/* TAB 3: PLATFORM VOUCHERS (READ-ONLY) */}
      {activeTab === "vouchers" && canReadVouchers && (
        <>
          <div className={styles.readOnlyNotice}>
            Voucher toàn sàn và Freeship đang ở chế độ chỉ đọc để đối chiếu lịch sử khuyến mãi.
          </div>
          {visibleVouchers.length === 0 ? (
            <p className={shared.emptyRow}>Chưa có mã voucher sàn nào.</p>
          ) : (
            <>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã &amp; tên voucher</th>
                      <th>Phạm vi</th>
                      <th>Ưu đãi</th>
                      <th className={shared.colNum}>Đã dùng / tổng</th>
                      <th>Hiệu lực</th>
                      <th>Trạng thái</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleVouchers.map((v) => {
                      const scopeMeta = getVoucherScopeMeta(v.scope);
                      const statusMeta = getVoucherStatusPill(v);
                      return (
                        <tr key={v.id}>
                          <td>
                            <div className={shared.cellPrimary}>{v.code}</div>
                            <div className={shared.cellSub}>{v.name}</div>
                          </td>
                          <td>
                            <StatusPill label={scopeMeta.label} tone={scopeMeta.tone} />
                          </td>
                          <td>
                            <div>{formatVoucherBenefit(v)}</div>
                            {v.min_order_vnd > 0 && (
                              <div className={shared.cellSub}>
                                Đơn từ {formatVnd(v.min_order_vnd)}
                              </div>
                            )}
                          </td>
                          <td className={shared.colNum}>
                            {formatInt(v.used_count)} / {formatInt(v.quota)}
                          </td>
                          <td>
                            <div className={shared.cellSub}>
                              {v.starts_at ? formatDateTimeVi(v.starts_at) : "—"}
                            </div>
                            <div className={shared.cellSub}>
                              đến {v.ends_at ? formatDateTimeVi(v.ends_at) : "—"}
                            </div>
                          </td>
                          <td>
                            <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {visibleVouchers.map((v) => {
                  const statusMeta = getVoucherStatusPill(v);
                  return (
                    <article key={v.id} className={shared.mobileItem}>
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{v.code}</span>
                        <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {formatVoucherBenefit(v)} · {formatInt(v.used_count)}/{formatInt(v.quota)}
                        </span>
                      </div>
                    </article>
                  );
                })}
              </div>
            </>
          )}
        </>
      )}

      {/* TAB 4: COINS (READ-ONLY) */}
      {activeTab === "coins" && (
        <section className={shared.panel} aria-labelledby="coins-readonly-heading">
          <div className={shared.panelHeader}>
            <h2 id="coins-readonly-heading" className={shared.panelTitle}>
              Xu thành viên (chỉ đọc)
            </h2>
            <StatusPill label="Chỉ đọc" tone="neutral" />
          </div>
          <div className={styles.readOnlyNotice}>
            Chức năng cộng Xu thành viên thủ công từ trang quản trị đã tạm khóa theo chính sách vận
            hành hiện tại. Lịch sử biến động Xu được ghi nhận trong nhật ký kiểm toán.
          </div>
        </section>
      )}

      {/* Drawer: Create Window / Inspect Slot / Inspect Window */}
      <AdminDrawer
        open={Boolean(drawer)}
        onClose={() => setDrawer(null)}
        title={
          drawer?.kind === "create"
            ? "Mở khung giờ Flash sale"
            : drawerSlotData
              ? shopLabel(drawerSlotData.item.shop_id)
              : drawerWindowData
                ? drawerWindowData.name
                : ""
        }
        subtitle={
          drawer?.kind === "create"
            ? "Giờ Việt Nam (GMT+7)"
            : drawerSlotData
              ? drawerSlotData.flashSale.name
              : drawerWindowData
                ? `${formatInt((drawerWindowData.items ?? []).length)} SKU đăng ký`
                : undefined
        }
        headerBadge={
          drawerSlotData ? (
            <StatusPill
              label={getItemStatusMeta(drawerSlotData.item.status).label}
              tone={getItemStatusMeta(drawerSlotData.item.status).tone}
            />
          ) : drawerWindowData ? (
            <StatusPill
              label={getFlashSaleWindowMeta(drawerWindowData, now).label}
              tone={getFlashSaleWindowMeta(drawerWindowData, now).tone}
            />
          ) : undefined
        }
      >
        {drawer?.kind === "create" && canWriteFlashSales && (
          <form className={styles.drawerForm} onSubmit={(e) => void handleCreate(e)} noValidate>
            <div className={styles.fieldGroup}>
              <label htmlFor="fs-name" className={shared.fieldLabel}>
                Tên khung giờ
              </label>
              <input
                id="fs-name"
                type="text"
                data-autofocus="true"
                className={shared.textInput}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (formError) setFormError(null);
                }}
                placeholder="VD: Flash sale trưa 12h đến 14h"
              />
            </div>

            <div className={styles.fieldGroup}>
              <label htmlFor="fs-start" className={shared.fieldLabel}>
                Bắt đầu
              </label>
              <input
                id="fs-start"
                type="datetime-local"
                className={shared.textInput}
                value={startLocal}
                onChange={(e) => setStartLocal(e.target.value)}
              />
            </div>

            <div className={styles.fieldGroup}>
              <label htmlFor="fs-end" className={shared.fieldLabel}>
                Kết thúc
              </label>
              <input
                id="fs-end"
                type="datetime-local"
                className={shared.textInput}
                value={endLocal}
                onChange={(e) => setEndLocal(e.target.value)}
              />
            </div>

            <div className={styles.fieldGroup}>
              <label htmlFor="fs-min-discount" className={shared.fieldLabel}>
                Mức giảm tối thiểu (%)
              </label>
              <input
                id="fs-min-discount"
                type="number"
                min={0}
                max={MAX_FLASH_SALE_DISCOUNT_PERCENT}
                step={1}
                className={shared.textInput}
                value={minDiscountPercent}
                onChange={(e) => setMinDiscountPercent(e.target.value)}
              />
              <span className={styles.fieldHint}>
                Từ 0% đến {MAX_FLASH_SALE_DISCOUNT_PERCENT}%. Nhập 0 nếu không giới hạn mức giảm tối
                thiểu.
              </span>
            </div>

            {categories.length > 0 && (
              <div className={styles.fieldGroup}>
                <span className={shared.fieldLabel}>
                  Danh mục áp dụng ({formatInt(selectedCategoryIds.length)} đã chọn)
                </span>
                <div className={styles.categoryList}>
                  {categories.map((cat) => (
                    <label key={cat.id} className={styles.categoryOption}>
                      <input
                        type="checkbox"
                        checked={selectedCategoryIds.includes(cat.id)}
                        onChange={() => toggleCategory(cat.id)}
                      />
                      <span>{cat.name}</span>
                    </label>
                  ))}
                </div>
                <span className={styles.fieldHint}>
                  Để trống nếu cho phép tất cả danh mục tham gia khung giờ.
                </span>
              </div>
            )}

            {formError && (
              <p className={shared.validationError} role="alert">
                {formError}
              </p>
            )}

            <div className={shared.detailButtons}>
              <Button type="submit" variant="primary" size="sm" shape="app" loading={creating}>
                Mở khung giờ
              </Button>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={creating}
                onClick={() => setDrawer(null)}
              >
                Hủy
              </Button>
            </div>
          </form>
        )}

        {drawerSlotData && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Gian hàng</span>
                <span className={shared.detailVal}>{shopLabel(drawerSlotData.item.shop_id)}</span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Giá Flash sale</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerSlotData.item.flash_price_vnd)}
                  {drawerSlotData.item.original_price_vnd
                    ? ` (gốc ${formatVnd(drawerSlotData.item.original_price_vnd)})`
                    : ""}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã SKU</span>
                <span className={`${shared.detailValPlain} num`}>{drawerSlotData.item.sku_id}</span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Suất còn / tổng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(drawerSlotData.item.remaining_slots)} /{" "}
                  {formatInt(drawerSlotData.item.total_slots)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Giới hạn mỗi người</span>
                <span className={`${shared.detailValPlain} num`}>
                  {formatInt(drawerSlotData.item.per_user_limit)} sản phẩm
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Khung giờ</span>
                <span className={shared.detailValPlain}>{drawerSlotData.flashSale.name}</span>
              </div>
              {drawerSlotData.item.rejection_reason && (
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Lý do từ chối</span>
                  <span className={shared.priorityText}>
                    {drawerSlotData.item.rejection_reason}
                  </span>
                </div>
              )}
            </div>

            {canApproveItems && (
              <div className={shared.detailActionBar}>
                <div className={shared.detailNoteGroup}>
                  <label htmlFor="fs-reject-reason" className={shared.fieldLabel}>
                    Lý do từ chối (tùy chọn)
                  </label>
                  <input
                    id="fs-reject-reason"
                    type="text"
                    className={shared.textInput}
                    value={rejectDraft}
                    onChange={(e) => setRejectDraft(e.target.value)}
                    placeholder={REJECT_REASON}
                  />
                </div>
                <div className={shared.detailButtons}>
                  <Button
                    variant="primary"
                    size="sm"
                    shape="app"
                    loading={busyItemId === drawerSlotData.item.id && busyAction === "approve"}
                    disabled={
                      Boolean(busyItemId) || drawerSlotData.item.status.toUpperCase() === "APPROVED"
                    }
                    onClick={() => void handleDecision(drawerSlotData.item, true)}
                  >
                    Duyệt suất
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    shape="app"
                    loading={busyItemId === drawerSlotData.item.id && busyAction === "reject"}
                    disabled={
                      Boolean(busyItemId) || drawerSlotData.item.status.toUpperCase() === "REJECTED"
                    }
                    onClick={() => void handleDecision(drawerSlotData.item, false, rejectDraft)}
                  >
                    Từ chối
                  </Button>
                </div>
              </div>
            )}
          </div>
        )}

        {drawerWindowData && (
          <>
            <div className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Bắt đầu</span>
                  <span className={`${shared.detailVal} num`}>
                    {drawerWindowData.starts_at
                      ? formatDateTimeVi(drawerWindowData.starts_at)
                      : "Chưa có"}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Kết thúc</span>
                  <span className={`${shared.detailVal} num`}>
                    {drawerWindowData.ends_at
                      ? formatDateTimeVi(drawerWindowData.ends_at)
                      : "Chưa có"}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mức giảm tối thiểu</span>
                  <span className={`${shared.detailVal} num`}>
                    {formatInt(drawerWindowData.min_discount_percent ?? 0)}%
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Danh mục áp dụng</span>
                  <span className={shared.detailValPlain}>
                    {(drawerWindowData.category_ids?.length ?? 0) > 0
                      ? (drawerWindowData.category_ids ?? [])
                          .map((id) => categoryNameById.get(id) ?? id.slice(0, 8))
                          .join(", ")
                      : "Tất cả danh mục"}
                  </span>
                </div>
              </div>

              {canWriteFlashSales && canEndFlashSaleEarly(drawerWindowData, now) && (
                <div className={shared.detailActionBar}>
                  <Button
                    variant="danger"
                    size="sm"
                    shape="app"
                    onClick={() => setConfirmEndWindow(drawerWindowData)}
                  >
                    Kết thúc sớm khung giờ
                  </Button>
                </div>
              )}
            </div>

            {(drawerWindowData.items ?? []).length === 0 ? (
              <p className={shared.emptyRow}>Chưa có gian hàng đăng ký SKU cho khung giờ này.</p>
            ) : (
              <ul className={styles.windowSlotList}>
                {(drawerWindowData.items ?? []).map((it) => {
                  const meta = getItemStatusMeta(it.status);
                  return (
                    <li key={it.id} className={styles.windowSlotRow}>
                      <div className={styles.windowSlotInfo}>
                        <span className={shared.cellPrimary}>{shopLabel(it.shop_id)}</span>
                        <span className={shared.cellSub}>
                          {formatVnd(it.flash_price_vnd)}, còn {formatInt(it.remaining_slots)}/
                          {formatInt(it.total_slots)} suất
                        </span>
                      </div>
                      <div className={styles.windowSlotRight}>
                        <StatusPill label={meta.label} tone={meta.tone} />
                        {decisionButtons(it)}
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </>
        )}
      </AdminDrawer>

      <AdminConfirmDialog
        open={Boolean(confirmEndWindow)}
        title="Kết thúc sớm khung giờ Flash sale"
        description={
          confirmEndWindow
            ? `Khung giờ "${confirmEndWindow.name}" sẽ dừng ngay lập tức và giải phóng các suất còn lại. Bạn có chắc muốn tiếp tục?`
            : ""
        }
        confirmLabel="Kết thúc sớm"
        busy={endingWindow}
        onConfirm={() => void handleConfirmEndEarly()}
        onCancel={() => {
          if (!endingWindow) setConfirmEndWindow(null);
        }}
      />
    </div>
  );
}

function AdminFlashSaleRoute(props: AdminFlashSaleClientProps) {
  return (
    <AdminStateGate
      title="Flash sale"
      emptyTitle="Chưa có khung giờ Flash sale"
      emptyMessage="Mở khung giờ đầu tiên để gian hàng đăng ký SKU."
      errorTitle="Không thể tải khung giờ Flash sale"
      errorMessage="Dịch vụ khuyến mãi tạm gián đoạn. Vui lòng thử lại."
      loadingRows={5}
    >
      {({ initialQuery }) => <FlashSaleWorkspace {...props} initialQuery={initialQuery} />}
    </AdminStateGate>
  );
}

export default function AdminFlashSaleClient(props: AdminFlashSaleClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Flash sale" />}>
      <AdminFlashSaleRoute {...props} />
    </Suspense>
  );
}
