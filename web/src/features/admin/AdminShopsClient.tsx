"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconChevronLeft, IconChevronRight, IconSearch, StateView } from "@/components";
import { hasOpenModal } from "@/components/modalFocus";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { AdminDrawer, SlidingTabBar, isEditableElement } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import { getAdminShopStatusMeta, type GwAdminShop } from "./adminApiTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/gian-hang/page.module.css";

type ShopFilterTab = "all" | "PENDING" | "ACTIVE" | "REJECTED" | "SUSPENDED";
type ShopSortKey = "pending_first" | "newest" | "name";

export interface AdminShopsClientProps {
  initialShops: GwAdminShop[];
  loadError: string | null;
}

function ShopApprovalContent({
  initialShops,
  loadError,
  initialQuery,
  initialSelected,
}: {
  initialShops: GwAdminShop[];
  loadError: string | null;
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [shops, setShops] = useState<GwAdminShop[]>(initialShops);
  const [activeTab, setActiveTab] = useState<ShopFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [sortBy, setSortBy] = useState<ShopSortKey>("pending_first");
  const [selectedId, setSelectedId] = useState<string | null>(
    initialSelected ?? initialShops[0]?.id ?? null
  );
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<"APPROVE" | "REQUEST_INFO" | "REJECT" | null>(null);

  const rowRefs = useRef<Map<string, HTMLButtonElement>>(new Map());
  const deepLinkHandledRef = useRef(false);

  const counts = useMemo(() => {
    const map: Record<ShopFilterTab, number> = {
      all: shops.length,
      PENDING: 0,
      ACTIVE: 0,
      REJECTED: 0,
      SUSPENDED: 0,
    };
    for (const s of shops) {
      const st = s.status.toUpperCase() as ShopFilterTab;
      if (st in map) map[st] += 1;
    }
    return map;
  }, [shops]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = shops.filter((s) => {
      if (activeTab !== "all" && s.status.toUpperCase() !== activeTab) return false;
      if (!q) return true;
      return (
        s.name.toLowerCase().includes(q) ||
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
  }, [shops, activeTab, query, sortBy]);

  const activeIndex = useMemo(() => {
    if (filtered.length === 0) return -1;
    if (selectedId) {
      const idx = filtered.findIndex((s) => s.id === selectedId);
      if (idx >= 0) return idx;
    }
    return 0;
  }, [filtered, selectedId]);

  const activeShop = activeIndex >= 0 ? filtered[activeIndex] : null;

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

  const pickNextAfterDecision = (currentId: string, updatedShop?: GwAdminShop): string | null => {
    const currentIdx = filtered.findIndex((s) => s.id === currentId);
    const resolveItem = (s: GwAdminShop) =>
      updatedShop && s.id === updatedShop.id ? updatedShop : s;
    const remaining = filtered
      .map(resolveItem)
      .filter((s) => activeTab === "all" || s.status.toUpperCase() === activeTab);

    if (remaining.length === 0) return null;

    // Prefer the next pending shop after current position
    for (let i = Math.max(0, currentIdx + 1); i < filtered.length; i++) {
      const cand = resolveItem(filtered[i]);
      if (cand.id !== currentId && cand.status.toUpperCase() === "PENDING") {
        return cand.id;
      }
    }
    const afterSame = filtered[currentIdx + 1];
    if (afterSame && remaining.some((s) => s.id === afterSame.id)) {
      return afterSame.id;
    }
    const other = remaining.find((s) => s.id !== currentId);
    return other?.id ?? remaining[0].id;
  };

  const handleDecision = async (
    shop: GwAdminShop,
    decision: "APPROVE" | "REQUEST_INFO" | "REJECT"
  ) => {
    if (busyId) return;
    const reason = (reasonDrafts[shop.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setReasonErrorId(shop.id);
      return;
    }
    setReasonErrorId(null);
    setBusyId(shop.id);
    setBusyAction(decision);
    try {
      const res = await api<{ shop: GwAdminShop }>(
        `/api/admin/shops/${encodeURIComponent(shop.id)}/review`,
        {
          method: "POST",
          body: { decision, reason },
        }
      );
      if (res.shop) {
        setShops((prev) => prev.map((s) => (s.id === res.shop.id ? res.shop : s)));
      }
      setReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      const nextId = pickNextAfterDecision(shop.id, res.shop);
      if (nextId) {
        setSelectedId(nextId);
        window.requestAnimationFrame(() => {
          rowRefs.current.get(nextId)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
        });
      }
      const msg =
        decision === "APPROVE"
          ? `Đã phê duyệt gian hàng "${shop.name}".`
          : decision === "REQUEST_INFO"
            ? `Đã yêu cầu bổ sung hồ sơ cho "${shop.name}".`
            : `Đã từ chối hồ sơ "${shop.name}".`;
      showToast(msg);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật thẩm định gian hàng.");
    } finally {
      setBusyId(null);
      setBusyAction(null);
    }
  };

  const renderInspectorBody = (shop: GwAdminShop, inDrawer = false) => {
    const draftValue = reasonDrafts[shop.id] ?? "";
    const showReasonError = reasonErrorId === shop.id;
    const meta = getAdminShopStatusMeta(shop.status);
    const anyBusy = Boolean(busyId);
    const isThisBusy = busyId === shop.id;
    const noteInputId = `shop-note-${inDrawer ? "drawer" : "pane"}-${shop.id}`;

    return (
      <div
        key={shop.id}
        className={`${inDrawer ? styles.drawerContent : shared.detailWellCard} ${shared.inspectorSlide}`}
      >
        {!inDrawer && (
          <div className={shared.detailHeader}>
            <div>
              <h2 className={shared.detailHeading}>{shop.name}</h2>
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

        <div className={shared.detailGrid}>
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
            <span className={shared.detailKey}>Mã tài khoản chủ shop</span>
            <span className={`${shared.detailValPlain} num`}>
              {shop.owner_user_id || EMPTY_DASH}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Thời điểm đăng ký</span>
            <span className={`${shared.detailValPlain} num`}>
              {shop.created_at ? formatDateTimeVi(shop.created_at) : EMPTY_DASH}
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
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={noteInputId} className={shared.fieldLabel}>
              Ghi chú thẩm định hoặc lý do từ chối
            </label>
            <input
              id={noteInputId}
              type="text"
              className={shared.textInput}
              value={draftValue}
              onChange={(e) => {
                setReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }));
                if (showReasonError) setReasonErrorId(null);
              }}
              placeholder="Bắt buộc nhập lý do khi Từ chối..."
            />
            {showReasonError && (
              <span className={shared.validationError}>
                Vui lòng nhập lý do cụ thể trước khi từ chối hồ sơ.
              </span>
            )}
          </div>

          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              loading={isThisBusy && busyAction === "APPROVE"}
              disabled={anyBusy || shop.status.toUpperCase() === "ACTIVE"}
              onClick={() => void handleDecision(shop, "APPROVE")}
            >
              Phê duyệt
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              loading={isThisBusy && busyAction === "REQUEST_INFO"}
              disabled={anyBusy || shop.status.toUpperCase() === "ACTIVE"}
              onClick={() => void handleDecision(shop, "REQUEST_INFO")}
            >
              Yêu cầu bổ sung
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              loading={isThisBusy && busyAction === "REJECT"}
              disabled={anyBusy || shop.status.toUpperCase() === "REJECTED"}
              onClick={() => void handleDecision(shop, "REJECT")}
            >
              Từ chối
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const tabs: Array<{ id: ShopFilterTab; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: counts.all },
    { id: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
    { id: "ACTIVE", label: "Đã duyệt", count: counts.ACTIVE },
    { id: "REJECTED", label: "Từ chối", count: counts.REJECTED },
    { id: "SUSPENDED", label: "Tạm khóa", count: counts.SUSPENDED },
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
                          <StatusPill label={meta.label} tone={meta.tone} />
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
              {renderInspectorBody(activeShop, false)}
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
      >
        {activeShop && renderInspectorBody(activeShop, true)}
      </AdminDrawer>
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
            loadError={props.loadError}
            initialQuery={initialQuery}
            initialSelected={initialSelected}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
