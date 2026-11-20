"use client";

import React, { Suspense, useMemo, useState } from "react";
import type { ShopApprovalApplication, ShopApprovalStatus } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconCheck, IconClock, IconSearch } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH, SHOP_APPROVAL_META } from "@/features/admin/adminMeta";
import { appendAdminAuditLog } from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ShopFilterTab = "all" | ShopApprovalStatus;
type ShopSortKey = "sla" | "newest" | "sku";

function ShopApprovalContent({
  initialQuery,
  initialSelected,
}: {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const { shopApprovals, updateShopApprovalStatus } = useStore();
  const [activeTab, setActiveTab] = useState<ShopFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [sortBy, setSortBy] = useState<ShopSortKey>("sla");
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected ?? "app-3");
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(initialSelected);
  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);

  const counts = useMemo(() => {
    const map: Record<ShopFilterTab, number> = {
      all: shopApprovals.length,
      cho_duyet: 0,
      bo_sung_ho_so: 0,
      da_duyet: 0,
      tu_choi: 0,
    };
    for (const app of shopApprovals) {
      map[app.status] += 1;
    }
    return map;
  }, [shopApprovals]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = shopApprovals.filter((app) => {
      if (activeTab !== "all" && app.status !== activeTab) return false;
      if (!q) return true;
      return (
        app.shopName.toLowerCase().includes(q) ||
        app.ownerName.toLowerCase().includes(q) ||
        app.applicationCode.toLowerCase().includes(q) ||
        app.businessLicense.toLowerCase().includes(q) ||
        app.provinceName.toLowerCase().includes(q) ||
        app.primaryCategory.toLowerCase().includes(q)
      );
    });

    return [...list].sort((a, b) => {
      if (sortBy === "newest") {
        return b.submittedAt.localeCompare(a.submittedAt);
      }
      if (sortBy === "sku") {
        return b.initialSkuCount - a.initialSkuCount;
      }
      const aOpen = a.slaHoursLeft > 0 ? a.slaHoursLeft : 9999;
      const bOpen = b.slaHoursLeft > 0 ? b.slaHoursLeft : 9999;
      return aOpen - bOpen;
    });
  }, [shopApprovals, activeTab, query, sortBy]);

  const activeDesktopApp = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((a) => a.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  const handleSelectDesktop = (id: string) => {
    setReasonErrorId(null);
    setSelectedId(id);
  };

  const handleToggleMobile = (id: string) => {
    setReasonErrorId(null);
    setMobileOpenId((prev) => (prev === id ? null : id));
  };

  const handleDecision = (
    app: ShopApprovalApplication,
    nextStatus: "da_duyet" | "bo_sung_ho_so" | "tu_choi"
  ) => {
    const noteText = (reasonDrafts[app.id] ?? "").trim();
    if (nextStatus === "tu_choi" && !noteText) {
      setReasonErrorId(app.id);
      return;
    }
    setReasonErrorId(null);

    const resolvedNote =
      noteText ||
      (nextStatus === "da_duyet"
        ? `Đã phê duyệt mở gian hàng với ${app.initialSkuCount} mã SKU ban đầu.`
        : "Đã gửi yêu cầu bổ sung giấy phép kinh doanh và đối chiếu tài khoản ngân hàng.");

    updateShopApprovalStatus(app.id, nextStatus, resolvedNote);

    const actionLabel =
      nextStatus === "da_duyet"
        ? "Phê duyệt mở gian hàng"
        : nextStatus === "bo_sung_ho_so"
          ? "Yêu cầu bổ sung hồ sơ gian hàng"
          : "Từ chối hồ sơ gian hàng";

    appendAdminAuditLog({
      action: actionLabel,
      module: "Gian hàng",
      target: app.applicationCode,
      changeSummary: `${app.shopName}: ${resolvedNote}`,
    });

    setReasonDrafts((prev) => ({ ...prev, [app.id]: "" }));
  };

  const renderInspectionPanel = (app: ShopApprovalApplication, showHeading = false) => {
    const draftValue = reasonDrafts[app.id] ?? "";
    const showReasonError = reasonErrorId === app.id;
    const meta = SHOP_APPROVAL_META[app.status];

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Chi tiết hồ sơ: {app.shopName} ({app.applicationCode})
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}
        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Tên gian hàng & mã hồ sơ</span>
            <span className={shared.detailVal}>
              {app.shopName} ({app.applicationCode})
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Chủ sở hữu</span>
            <span className={shared.detailVal}>{app.ownerName}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã số thuế / GPKD</span>
            <span className={`${shared.detailVal} num`}>{app.businessLicense}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Địa chỉ kho hàng</span>
            <span className={shared.detailValPlain}>
              {app.wardName}, {app.provinceName}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Ngành hàng & SKU khởi tạo</span>
            <span className={`${shared.detailValPlain} num`}>
              {app.primaryCategory} · {formatInt(app.initialSkuCount)} SKU
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Thời điểm nộp hồ sơ</span>
            <span className={`${shared.detailValPlain} num`}>
              {formatDateTimeVi(app.submittedAt)}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Tài liệu đã nộp</span>
            <div className={styles.checklistRow}>
              <span className={app.documentsVerified ? styles.docTagOk : styles.docTagPending}>
                {app.documentsVerified ? <IconCheck size={12} /> : <IconClock size={12} />}
                {app.documentsVerified ? "GPKD đã xác minh" : "GPKD chờ bổ sung"}
              </span>
              <span className={app.bankVerified ? styles.docTagOk : styles.docTagPending}>
                {app.bankVerified ? <IconCheck size={12} /> : <IconClock size={12} />}
                {app.bankVerified ? "Ngân hàng chính chủ" : "Chờ đối chiếu sao kê"}
              </span>
            </div>
          </div>
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Ghi chú thẩm định hiện tại</span>
            <span className={shared.detailValPlain}>{app.note}</span>
          </div>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`shop-note-${app.id}`} className={shared.fieldLabel}>
              Ghi chú thẩm định hoặc lý do từ chối
            </label>
            <input
              id={`shop-note-${app.id}`}
              type="text"
              className={shared.textInput}
              value={draftValue}
              onChange={(e) => {
                setReasonDrafts((prev) => ({ ...prev, [app.id]: e.target.value }));
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
              disabled={app.status === "da_duyet"}
              onClick={() => handleDecision(app, "da_duyet")}
            >
              Phê duyệt
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={app.status === "da_duyet"}
              onClick={() => handleDecision(app, "bo_sung_ho_so")}
            >
              Yêu cầu bổ sung
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={app.status === "tu_choi"}
              onClick={() => handleDecision(app, "tu_choi")}
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
    { id: "cho_duyet", label: "Chờ duyệt" },
    { id: "bo_sung_ho_so", label: "Yêu cầu bổ sung" },
    { id: "da_duyet", label: "Đã duyệt" },
    { id: "tu_choi", label: "Từ chối" },
  ];

  const subtitle =
    counts.cho_duyet > 0
      ? `${counts.cho_duyet} hồ sơ chờ duyệt mới và ${counts.bo_sung_ho_so} hồ sơ chờ bổ sung giấy phép kinh doanh.`
      : `Không còn hồ sơ chờ duyệt mới; ${counts.da_duyet} gian hàng đã kích hoạt hoạt động.`;

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
              placeholder="Tìm tên gian hàng, chủ shop, mã HS-..."
              aria-label="Tìm kiếm hồ sơ gian hàng"
            />
          </div>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} hồ sơ</span>
          <select
            className={shared.toolbarSelect}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as ShopSortKey)}
            aria-label="Sắp xếp hồ sơ gian hàng"
          >
            <option value="sla">Hạn SLA gấp nhất</option>
            <option value="newest">Mới nộp gần nhất</option>
            <option value="sku">Số SKU nhiều nhất</option>
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
                  <th>Gian hàng & chủ sở hữu</th>
                  <th>Ngành hàng & kho</th>
                  <th className={shared.colNum}>SKU khởi tạo</th>
                  <th className={shared.colNum}>Hạn SLA</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((app) => {
                  const meta = SHOP_APPROVAL_META[app.status];
                  const isSelected = activeDesktopApp?.id === app.id;
                  return (
                    <tr
                      key={app.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => handleSelectDesktop(app.id)}
                    >
                      <td>
                        <div className={shared.cellPrimary}>{app.shopName}</div>
                        <div className={shared.cellSub}>
                          {app.applicationCode} · {app.ownerName}
                        </div>
                      </td>
                      <td>
                        <div>{app.primaryCategory}</div>
                        <div className={shared.cellSub}>{app.provinceName}</div>
                      </td>
                      <td className={shared.colNum}>{formatInt(app.initialSkuCount)}</td>
                      <td
                        className={`${shared.colNum} ${
                          app.slaHoursLeft > 0 && app.slaHoursLeft <= 12
                            ? shared.numNegative
                            : app.slaHoursLeft === 0
                              ? shared.numMuted
                              : ""
                        }`}
                      >
                        {app.slaHoursLeft > 0 ? `Còn ${app.slaHoursLeft}h` : EMPTY_DASH}
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
                            handleSelectDesktop(app.id);
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

          {activeDesktopApp && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Bảng thẩm định chi tiết hồ sơ gian hàng"
            >
              {renderInspectionPanel(activeDesktopApp, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filtered.map((app) => {
              const meta = SHOP_APPROVAL_META[app.status];
              const isSelected = mobileOpenId === app.id;
              return (
                <div key={app.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => handleToggleMobile(app.id)}
                    aria-expanded={isSelected}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{app.shopName}</span>
                      <span className={shared.mobileMetric}>
                        {app.slaHoursLeft > 0
                          ? `Còn ${app.slaHoursLeft}h`
                          : `${formatInt(app.initialSkuCount)} SKU`}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {app.applicationCode} · {app.primaryCategory} · {app.provinceName}
                      </span>
                      <span className={styles.mobilePillRow}>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </span>
                    </div>
                  </button>
                  {isSelected && (
                    <div className={shared.mobileDetailWrap}>{renderInspectionPanel(app)}</div>
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

function ShopApprovalRoute() {
  return (
    <AdminStateGate
      title="Duyệt gian hàng"
      subtitle="Thẩm định giấy phép kinh doanh, tài khoản ngân hàng và bộ ảnh sản phẩm của gian hàng đăng ký."
      emptyTitle="Hàng đợi duyệt gian hàng đang trống"
      emptyMessage="Tất cả hồ sơ đăng ký mở gian hàng mới đã được xử lý hoàn tất."
      errorTitle="Không thể tải danh sách hồ sơ gian hàng"
      errorMessage="Không thể kết nối tới hàng đợi thẩm định lúc này. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <ShopApprovalContent initialQuery={initialQuery} initialSelected={initialSelected} />
      )}
    </AdminStateGate>
  );
}

export default function AdminShopApprovalsPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Duyệt gian hàng" />}>
      <ShopApprovalRoute />
    </Suspense>
  );
}
