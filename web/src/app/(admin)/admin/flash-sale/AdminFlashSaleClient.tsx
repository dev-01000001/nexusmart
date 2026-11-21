"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { PillTone } from "@/features/admin/adminMeta";
import type { GwAdminFlashSale, GwAdminFlashSaleItem } from "@/features/admin/adminApiTypes";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ItemFilter = "PENDING" | "APPROVED" | "REJECTED" | "all";

const ITEM_FILTERS: Array<{ id: ItemFilter; label: string }> = [
  { id: "PENDING", label: "Chờ duyệt" },
  { id: "APPROVED", label: "Đã duyệt" },
  { id: "REJECTED", label: "Từ chối" },
  { id: "all", label: "Tất cả" },
];

const REJECT_REASON = "Giá Flash sale hoặc số suất chưa phù hợp với khung giờ này";

export interface AdminFlashSaleClientProps {
  initialFlashSales: GwAdminFlashSale[];
  shopNames: Record<string, string>;
  loadError: string | null;
}

function defaultWindowRange(): { startLocal: string; endLocal: string } {
  const vnNow = new Date(Date.now() + 7 * 3600_000);
  const tomorrow = new Date(vnNow.getTime() + 24 * 3600_000);
  const datePart = tomorrow.toISOString().slice(0, 10);
  return {
    startLocal: `${datePart}T12:00`,
    endLocal: `${datePart}T14:00`,
  };
}

function localVnInputToIso(value: string): string {
  if (!value) return "";
  const normalized = value.length === 16 ? `${value}:00+07:00` : `${value}+07:00`;
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
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

function FlashSaleWorkspace({
  initialFlashSales,
  shopNames,
  loadError,
}: AdminFlashSaleClientProps) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [flashSales, setFlashSales] = useState<GwAdminFlashSale[]>(initialFlashSales);
  const [itemFilter, setItemFilter] = useState<ItemFilter>("PENDING");
  const [now] = useState(() => Date.now());

  const [name, setName] = useState("");
  const [startLocal, setStartLocal] = useState(() => defaultWindowRange().startLocal);
  const [endLocal, setEndLocal] = useState(() => defaultWindowRange().endLocal);
  const [creating, setCreating] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [busyItemId, setBusyItemId] = useState<string | null>(null);

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

  const visibleEntries = useMemo(
    () =>
      itemFilter === "all"
        ? entries
        : entries.filter(({ item }) => item.status.toUpperCase() === itemFilter),
    [entries, itemFilter]
  );

  const shopLabel = (shopId: string) => shopNames[shopId] ?? `Gian hàng ${shopId.slice(0, 8)}`;

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
      showToast(`Đã mở khung giờ "${created.name}" cho gian hàng đăng ký.`);
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể tạo khung giờ Flash sale lúc này.";
      setFormError(msg);
      showToast(msg);
    } finally {
      setCreating(false);
    }
  };

  const handleDecision = async (item: GwAdminFlashSaleItem, approve: boolean) => {
    if (busyItemId) return;
    setBusyItemId(item.id);
    try {
      const res = await api<{ item: GwAdminFlashSaleItem }>(
        `/api/admin/flash-sale-items/${encodeURIComponent(item.id)}/approve`,
        { method: "POST", body: { approve, reason: approve ? "" : REJECT_REASON } }
      );
      setFlashSales((prev) =>
        prev.map((fs) => ({
          ...fs,
          items: (fs.items ?? []).map((it) => (it.id === item.id ? { ...it, ...res.item } : it)),
        }))
      );
      showToast(
        approve
          ? `Đã duyệt suất của ${shopLabel(item.shop_id)}. Suất sẽ hiện trên site của gian hàng khi khung giờ bắt đầu.`
          : `Đã từ chối suất của ${shopLabel(item.shop_id)}.`
      );
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái suất Flash sale.";
      showToast(msg);
    } finally {
      setBusyItemId(null);
    }
  };

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách khung giờ Flash sale"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const decisionButtons = (item: GwAdminFlashSaleItem) => {
    const status = item.status.toUpperCase();
    const isBusy = busyItemId === item.id;
    return (
      <div className={styles.actionInline}>
        <button
          type="button"
          className={shared.rowActionBtn}
          disabled={isBusy || status === "APPROVED"}
          onClick={() => void handleDecision(item, true)}
        >
          Duyệt suất
        </button>
        <button
          type="button"
          className={shared.rowActionBtn}
          disabled={isBusy || status === "REJECTED"}
          onClick={() => void handleDecision(item, false)}
        >
          Từ chối
        </button>
      </div>
    );
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Flash sale</h1>
          <p className={shared.pageSubtitle}>
            Mở khung giờ chung để gian hàng đăng ký SKU giảm giá. Suất được duyệt chỉ hiển thị trên
            site riêng của gian hàng đã đăng ký.
          </p>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp khung giờ Flash sale"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Khung giờ</span>
          <span className={shared.kpiValue}>{formatInt(flashSales.length)}</span>
          <span className={shared.kpiCaption}>{formatInt(counts.live)} khung đang diễn ra</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Suất chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.PENDING)}</span>
          <span className={shared.kpiCaption}>Gian hàng đã gửi đăng ký</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Suất đã duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.APPROVED)}</span>
          <span className={shared.kpiCaption}>Hiển thị trên site gian hàng</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Suất bị từ chối</span>
          <span className={shared.kpiValue}>{formatInt(counts.REJECTED)}</span>
          <span className={shared.kpiCaption}>Trên tổng {formatInt(counts.all)} đăng ký</span>
        </div>
      </section>

      <div className={styles.stack}>
        <section className={shared.panel} aria-labelledby="fs-window-heading">
          <div className={shared.panelHeader}>
            <h2 id="fs-window-heading" className={shared.panelTitle}>
              Khung giờ Flash sale
            </h2>
            <span className={shared.panelMeta}>Giờ Việt Nam (GMT+7)</span>
          </div>

          <form className={styles.windowForm} onSubmit={handleCreate}>
            <div className={styles.fieldGroup}>
              <label htmlFor="fs-name" className={shared.fieldLabel}>
                Tên khung giờ
              </label>
              <input
                id="fs-name"
                type="text"
                className={shared.textInput}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (formError) setFormError(null);
                }}
                placeholder="VD: Flash sale trưa 12h–14h"
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
            <Button type="submit" variant="primary" size="sm" shape="app" disabled={creating}>
              {creating ? "Đang mở..." : "Mở khung giờ"}
            </Button>
          </form>
          {formError && <p className={shared.validationError}>{formError}</p>}

          {flashSales.length === 0 ? (
            <p className={shared.emptyRow}>Chưa có khung giờ nào đang chạy hoặc sắp diễn ra.</p>
          ) : (
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
                  {flashSales.map((fs) => {
                    const items = fs.items ?? [];
                    const approved = items.filter(
                      (it) => it.status.toUpperCase() === "APPROVED"
                    ).length;
                    const meta = getWindowMeta(fs, now);
                    return (
                      <tr key={fs.id}>
                        <td className={shared.cellPrimary}>{fs.name}</td>
                        <td>
                          <div>{fs.starts_at ? formatDateTimeVi(fs.starts_at) : "Chưa đặt"}</div>
                          <div className={shared.cellSub}>
                            đến {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "chưa đặt"}
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
          )}

          {flashSales.length > 0 && (
            <div className={shared.mobileList}>
              {flashSales.map((fs) => {
                const meta = getWindowMeta(fs, now);
                return (
                  <div key={fs.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{fs.name}</span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {fs.starts_at ? formatDateTimeVi(fs.starts_at) : "Chưa đặt"} –{" "}
                        {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "chưa đặt"}
                      </span>
                      <span className={shared.mobileMetaText}>
                        {formatInt((fs.items ?? []).length)} SKU
                      </span>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>

        <section className={shared.panel} aria-labelledby="fs-items-heading">
          <div className={shared.panelHeader}>
            <h2 id="fs-items-heading" className={shared.panelTitle}>
              Suất gian hàng đăng ký
            </h2>
            <span className={shared.panelMeta}>{formatInt(visibleEntries.length)} suất</span>
          </div>

          <div className={shared.tabsBar}>
            <div className={shared.tabList} role="tablist" aria-label="Lọc suất theo trạng thái">
              {ITEM_FILTERS.map((tab) => {
                const active = itemFilter === tab.id;
                return (
                  <button
                    key={tab.id}
                    type="button"
                    role="tab"
                    aria-selected={active}
                    className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                    onClick={() => setItemFilter(tab.id)}
                  >
                    <span>{tab.label}</span>
                    <span className={shared.tabCount}>{counts[tab.id]}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {visibleEntries.length === 0 ? (
            <p className={shared.emptyRow}>
              {itemFilter === "PENDING"
                ? "Không còn suất nào chờ duyệt."
                : "Chưa có suất nào ở trạng thái này."}
            </p>
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
                      return (
                        <tr key={item.id}>
                          <td>
                            <div className={shared.cellPrimary}>{shopLabel(item.shop_id)}</div>
                            <div className={shared.cellSub}>
                              SKU {item.sku_id.slice(0, 8)}, tối đa {formatInt(item.per_user_limit)}{" "}
                              sản phẩm mỗi người
                            </div>
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
                            {item.rejection_reason && (
                              <div className={shared.cellSub}>{item.rejection_reason}</div>
                            )}
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
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{shopLabel(item.shop_id)}</span>
                        <span className={shared.mobileMetric}>
                          {formatVnd(item.flash_price_vnd)}
                        </span>
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {flashSale.name}, còn {formatInt(item.remaining_slots)}/
                          {formatInt(item.total_slots)} suất
                        </span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </div>
                      <div className={shared.mobileLine2}>{decisionButtons(item)}</div>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </section>
      </div>
    </div>
  );
}

function AdminFlashSaleRoute(props: AdminFlashSaleClientProps) {
  return (
    <AdminStateGate
      title="Flash sale"
      subtitle="Mở khung giờ Flash sale và duyệt suất gian hàng đăng ký."
      emptyTitle="Chưa có khung giờ Flash sale"
      emptyMessage="Mở khung giờ đầu tiên để gian hàng bắt đầu đăng ký SKU."
      errorTitle="Không thể tải khung giờ Flash sale"
      errorMessage="Dịch vụ khuyến mãi tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {() => <FlashSaleWorkspace {...props} />}
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
