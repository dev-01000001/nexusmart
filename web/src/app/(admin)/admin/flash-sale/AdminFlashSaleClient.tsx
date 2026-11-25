"use client";

import React, { Suspense, useCallback, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconPlus, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnIsoDate, formatVnd } from "@/lib/format";
import { localVnInputToIso, type PillTone } from "@/features/admin/adminMeta";
import type { GwAdminFlashSale, GwAdminFlashSaleItem } from "@/features/admin/adminApiTypes";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { AdminDrawer, SlidingTabBar } from "@/features/admin/AdminUi";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type FlashTab = "slots" | "windows";
type ItemFilter = "all" | "PENDING" | "APPROVED" | "REJECTED";

const REJECT_REASON = "Giá Flash sale hoặc số suất chưa phù hợp với khung giờ này";

export interface AdminFlashSaleClientProps {
  initialFlashSales: GwAdminFlashSale[];
  shopNames: Record<string, string>;
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

function getWindowMeta(fs: GwAdminFlashSale, now: number): { label: string; tone: PillTone } {
  const start = fs.starts_at ? Date.parse(fs.starts_at) : Number.NaN;
  const end = fs.ends_at ? Date.parse(fs.ends_at) : Number.NaN;
  if (!Number.isNaN(end) && end <= now) return { label: "Đã kết thúc", tone: "neutral" };
  if (!Number.isNaN(start) && start <= now) return { label: "Đang diễn ra", tone: "positive" };
  return { label: "Sắp diễn ra", tone: "warning" };
}

type DrawerState =
  | { kind: "create" }
  | { kind: "slot"; flashSaleId: string; itemId: string }
  | { kind: "window"; flashSaleId: string }
  | null;

function FlashSaleWorkspace({
  initialFlashSales,
  shopNames,
  loadError,
  serverNowMs,
  initialQuery,
}: AdminFlashSaleClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [flashSales, setFlashSales] = useState<GwAdminFlashSale[]>(initialFlashSales);
  const initialTotalItems = initialFlashSales.reduce((s, f) => s + (f.items?.length ?? 0), 0);
  const [activeTab, setActiveTab] = useState<FlashTab>(initialTotalItems > 0 ? "slots" : "windows");
  const [itemFilter, setItemFilter] = useState<ItemFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [now] = useState(() => serverNowMs ?? Date.now());

  const [drawer, setDrawer] = useState<DrawerState>(null);
  const [name, setName] = useState("");
  const [startLocal, setStartLocal] = useState(() => defaultWindowRange(now).startLocal);
  const [endLocal, setEndLocal] = useState(() => defaultWindowRange(now).endLocal);
  const [creating, setCreating] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [busyItemId, setBusyItemId] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<"approve" | "reject" | null>(null);
  const [rejectDraft, setRejectDraft] = useState("");

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
    const live = flashSales.filter((fs) => getWindowMeta(fs, now).label === "Đang diễn ra").length;
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

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (creating) return;
    const cleanName = name.trim();
    const startsIso = localVnInputToIso(startLocal);
    const endsIso = localVnInputToIso(endLocal);
    if (!cleanName || !startsIso || !endsIso) {
      setFormError("Nhập tên khung giờ và thời gian bắt đầu, kết thúc hợp lệ.");
      return;
    }
    if (new Date(endsIso).getTime() <= new Date(startsIso).getTime()) {
      setFormError("Thời gian kết thúc phải sau thời gian bắt đầu.");
      return;
    }
    setFormError(null);
    setCreating(true);
    try {
      const res = await api<{ flash_sale: GwAdminFlashSale }>("/api/admin/flash-sales", {
        method: "POST",
        body: { name: cleanName, starts_at: startsIso, ends_at: endsIso },
      });
      const created: GwAdminFlashSale = { ...res.flash_sale, items: res.flash_sale.items ?? [] };
      setFlashSales((prev) => [created, ...prev]);
      setName("");
      setDrawer(null);
      setActiveTab("windows");
      showToast(`Đã mở khung giờ "${created.name}".`);
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể tạo khung giờ Flash sale lúc này.";
      setFormError(msg);
      showToast(msg);
    } finally {
      setCreating(false);
    }
  };

  const handleDecision = async (
    item: GwAdminFlashSaleItem,
    approve: boolean,
    customReason?: string
  ) => {
    if (busyItemId) return;
    setBusyItemId(item.id);
    setBusyAction(approve ? "approve" : "reject");
    try {
      const reason = approve ? "" : customReason?.trim() || REJECT_REASON;
      const res = await api<{ item: GwAdminFlashSaleItem }>(
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
      const msg =
        err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái suất Flash sale.";
      showToast(msg);
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

  const decisionButtons = (item: GwAdminFlashSaleItem) => {
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

  const mainTabs: Array<{ id: FlashTab; label: string; count: number }> = [
    { id: "slots", label: "Suất gian hàng đăng ký", count: entries.length },
    { id: "windows", label: "Khung giờ Flash sale", count: flashSales.length },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Flash sale</h1>
        </div>
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
        ariaLabel="Chuyển giữa suất đăng ký và khung giờ"
        rightSlot={<span className={shared.resultCount}>Giờ Việt Nam (GMT+7)</span>}
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
              placeholder={
                activeTab === "slots"
                  ? "Tìm gian hàng, khung giờ, mã SKU..."
                  : "Tìm tên khung giờ..."
              }
              aria-label="Tìm kiếm Flash sale"
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
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>
            {activeTab === "slots"
              ? `${formatInt(visibleEntries.length)} suất`
              : `${formatInt(visibleWindows.length)} khung giờ`}
          </span>
        </div>
      </div>

      {activeTab === "slots" ? (
        visibleEntries.length === 0 ? (
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
                          <span className={shared.cellPrimary}>
                            {formatVnd(item.flash_price_vnd)}
                          </span>
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
                          {[
                            flashSale.name,
                            `còn ${formatInt(item.remaining_slots)}/${formatInt(item.total_slots)} suất`,
                          ]
                            .filter(Boolean)
                            .join(", ")}
                        </span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </span>
                    </button>
                  </div>
                );
              })}
            </div>
          </>
        )
      ) : visibleWindows.length === 0 ? (
        <p className={shared.emptyRow}>Chưa có khung giờ nào.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Khung giờ</th>
                  <th>Thời gian</th>
                  <th>Trạng thái</th>
                  <th className={shared.colNum}>SKU đăng ký</th>
                  <th className={shared.colNum}>Đã duyệt</th>
                </tr>
              </thead>
              <tbody>
                {visibleWindows.map((fs) => {
                  const items = fs.items ?? [];
                  const approved = items.filter(
                    (it) => it.status.toUpperCase() === "APPROVED"
                  ).length;
                  const meta = getWindowMeta(fs, now);
                  const selected = drawer?.kind === "window" && drawer.flashSaleId === fs.id;
                  return (
                    <tr
                      key={fs.id}
                      className={`${shared.rowInteractive} ${selected ? shared.rowSelected : ""}`}
                      onClick={() => setDrawer({ kind: "window", flashSaleId: fs.id })}
                    >
                      <td className={shared.cellPrimary}>
                        <button
                          type="button"
                          className={shared.rowTriggerBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setDrawer({ kind: "window", flashSaleId: fs.id });
                          }}
                        >
                          {fs.name}
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
                      <td className={shared.colNum}>{formatInt(items.length)}</td>
                      <td className={shared.colNum}>{formatInt(approved)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {visibleWindows.map((fs) => {
              const meta = getWindowMeta(fs, now);
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
                        {fs.starts_at ? formatDateTimeVi(fs.starts_at) : "Chưa có"} đến{" "}
                        {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "chưa có"}
                      </span>
                      <span>{formatInt((fs.items ?? []).length)} SKU</span>
                    </span>
                  </button>
                </div>
              );
            })}
          </div>
        </>
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
              label={getWindowMeta(drawerWindowData, now).label}
              tone={getWindowMeta(drawerWindowData, now).tone}
            />
          ) : undefined
        }
      >
        {drawer?.kind === "create" && (
          <form className={styles.drawerForm} onSubmit={handleCreate}>
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

            {formError && <p className={shared.validationError}>{formError}</p>}

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
              </div>
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
