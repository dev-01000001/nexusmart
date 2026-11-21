"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
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
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(initialSelected);
  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

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

  const activeDesktopShop = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((s) => s.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách hồ sơ gian hàng"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const handleDecision = async (
    shop: GwAdminShop,
    decision: "APPROVE" | "REQUEST_INFO" | "REJECT"
  ) => {
    const reason = (reasonDrafts[shop.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setReasonErrorId(shop.id);
      return;
    }
    setReasonErrorId(null);
    setBusyId(shop.id);
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
      const msg =
        decision === "APPROVE"
          ? `Đã phê duyệt mở gian hàng "${shop.name}" và ghi nhật ký kiểm toán.`
          : decision === "REQUEST_INFO"
            ? `Đã gửi yêu cầu bổ sung hồ sơ cho "${shop.name}".`
            : `Đã từ chối hồ sơ gian hàng "${shop.name}".`;
      showToast(msg);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật thẩm định gian hàng.");
    } finally {
      setBusyId(null);
    }
  };

  const renderInspectionPanel = (shop: GwAdminShop, showHeading = false) => {
    const draftValue = reasonDrafts[shop.id] ?? "";
    const showReasonError = reasonErrorId === shop.id;
    const meta = getAdminShopStatusMeta(shop.status);
    const isBusy = busyId === shop.id;

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>Chi tiết hồ sơ: {shop.name}</h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}
        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Tên gian hàng</span>
            <span className={shared.detailVal}>{shop.name}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã gian hàng</span>
            <span className={`${shared.detailValPlain} num`}>{shop.id}</span>
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
              <span className={shared.detailValPlain}>{shop.rejection_reason}</span>
            </div>
          )}
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`shop-note-${shop.id}`} className={shared.fieldLabel}>
              Ghi chú thẩm định hoặc lý do từ chối
            </label>
            <input
              id={`shop-note-${shop.id}`}
              type="text"
              className={shared.textInput}
              value={draftValue}
              onChange={(e) => {
                setReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }));
                if (showReasonError) setReasonErrorId(null);
              }}
              placeholder="Nhập ghi chú gửi gian hàng (bắt buộc khi Từ chối)..."
            />
            {showReasonError && (
              <span className={styles.validationError}>
                Vui lòng nhập lý do cụ thể trước khi từ chối hồ sơ.
              </span>
            )}
          </div>

          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={isBusy || shop.status.toUpperCase() === "ACTIVE"}
              onClick={() => void handleDecision(shop, "APPROVE")}
            >
              {isBusy ? "Đang xử lý..." : "Phê duyệt"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={isBusy || shop.status.toUpperCase() === "ACTIVE"}
              onClick={() => void handleDecision(shop, "REQUEST_INFO")}
            >
              Yêu cầu bổ sung
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={isBusy || shop.status.toUpperCase() === "REJECTED"}
              onClick={() => void handleDecision(shop, "REJECT")}
            >
              Từ chối
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const tabs: Array<{ id: ShopFilterTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "PENDING", label: "Chờ duyệt" },
    { id: "ACTIVE", label: "Đã duyệt" },
    { id: "REJECTED", label: "Từ chối" },
    { id: "SUSPENDED", label: "Tạm khóa" },
  ];

  const subtitle =
    counts.PENDING > 0
      ? `${formatInt(counts.PENDING)} hồ sơ đang chờ thẩm định và ${formatInt(counts.ACTIVE)} gian hàng đang hoạt động.`
      : `Không còn hồ sơ chờ duyệt mới; ${formatInt(counts.ACTIVE)} gian hàng đã kích hoạt hoạt động.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Duyệt gian hàng</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái hồ sơ gian hàng">
          {tabs.map((tab) => {
            const active = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.id)}
              >
                <span>{tab.label}</span>
                <span className={shared.tabCount}>{counts[tab.id]}</span>
              </button>
            );
          })}
        </div>
      </div>

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
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} gian hàng</span>
          <select
            className={shared.toolbarSelect}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as ShopSortKey)}
            aria-label="Sắp xếp hồ sơ gian hàng"
          >
            <option value="pending_first">Ưu tiên hồ sơ chờ duyệt</option>
            <option value="newest">Mới đăng ký gần nhất</option>
            <option value="name">Tên gian hàng A-Z</option>
          </select>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>
          Không tìm thấy hồ sơ gian hàng phù hợp với bộ lọc hiện tại.
        </p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Gian hàng & mô tả</th>
                  <th>Liên hệ & hồ sơ</th>
                  <th>Ngày đăng ký</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((shop) => {
                  const meta = getAdminShopStatusMeta(shop.status);
                  const isSelected = activeDesktopShop?.id === shop.id;
                  return (
                    <tr
                      key={shop.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => {
                        setReasonErrorId(null);
                        setSelectedId(shop.id);
                      }}
                    >
                      <td>
                        <div className={shared.cellPrimary}>{shop.name}</div>
                        <div className={shared.cellSub}>
                          {shop.description || shop.id.slice(0, 12)}
                        </div>
                      </td>
                      <td>
                        <div>{shop.contact_phone || EMPTY_DASH}</div>
                        <div className={shared.cellSub}>
                          {shop.business_license_doc || "Chưa đính kèm GPKD"}
                        </div>
                      </td>
                      <td>{shop.created_at ? formatDateTimeVi(shop.created_at) : EMPTY_DASH}</td>
                      <td>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </td>
                      <td className={shared.colAction}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setReasonErrorId(null);
                            setSelectedId(shop.id);
                          }}
                        >
                          {isSelected ? "Đang xem" : "Xem hồ sơ"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {activeDesktopShop && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Bảng thẩm định chi tiết hồ sơ gian hàng"
            >
              {renderInspectionPanel(activeDesktopShop, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filtered.map((shop) => {
              const meta = getAdminShopStatusMeta(shop.status);
              const isSelected = mobileOpenId === shop.id;
              return (
                <div key={shop.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => {
                      setReasonErrorId(null);
                      setMobileOpenId((prev) => (prev === shop.id ? null : shop.id));
                    }}
                    aria-expanded={isSelected}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{shop.name}</span>
                      <span className={styles.mobilePillRow}>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {shop.contact_phone || shop.id.slice(0, 8)}
                      </span>
                    </div>
                  </button>
                  {isSelected && (
                    <div className={shared.mobileDetailWrap}>{renderInspectionPanel(shop)}</div>
                  )}
                </div>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}

export function AdminShopsClient(props: AdminShopsClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Duyệt gian hàng" />}>
      <AdminStateGate
        title="Duyệt gian hàng"
        subtitle="Thẩm định giấy phép kinh doanh và kích hoạt gian hàng đăng ký trên NexusMart."
        emptyTitle="Hàng đợi duyệt gian hàng đang trống"
        emptyMessage="Tất cả hồ sơ đăng ký mở gian hàng mới đã được xử lý hoàn tất."
        errorTitle="Không thể tải danh sách hồ sơ gian hàng"
        errorMessage="Không thể kết nối tới dịch vụ gian hàng lúc này. Vui lòng thử tải lại."
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
