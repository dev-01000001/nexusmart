"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { StatusPill } from "./StatusPill";
import {
  getActorRoleLabelVi,
  getAuditActionLabelVi,
  getAuditTargetLabelVi,
  type GwAuditLog,
} from "./adminApiTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/audit-log/page.module.css";

type AuditTargetTab = "all" | "shop" | "site" | "user" | "other";

function bucketTarget(targetType: string): AuditTargetTab {
  const t = targetType.toLowerCase();
  if (t === "shop") return "shop";
  if (t === "site") return "site";
  if (t === "user") return "user";
  return "other";
}

export interface AdminAuditLogClientProps {
  initialLogs: GwAuditLog[];
  loadError: string | null;
}

function AuditLogContent({
  initialLogs,
  loadError,
  initialQuery,
}: {
  initialLogs: GwAuditLog[];
  loadError: string | null;
  initialQuery: string;
}) {
  const router = useRouter();
  const [activeTab, setActiveTab] = useState<AuditTargetTab>("all");
  const [roleFilter, setRoleFilter] = useState<string>("all");
  const [query, setQuery] = useState(initialQuery);

  const counts = useMemo(() => {
    const map: Record<AuditTargetTab, number> = {
      all: initialLogs.length,
      shop: 0,
      site: 0,
      user: 0,
      other: 0,
    };
    for (const log of initialLogs) {
      map[bucketTarget(log.target_type)] += 1;
    }
    return map;
  }, [initialLogs]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return initialLogs.filter((log) => {
      if (activeTab !== "all" && bucketTarget(log.target_type) !== activeTab) return false;
      if (roleFilter !== "all" && log.actor_role.toLowerCase() !== roleFilter) return false;
      if (!q) return true;
      return (
        log.action.toLowerCase().includes(q) ||
        getAuditActionLabelVi(log.action).toLowerCase().includes(q) ||
        log.actor_id.toLowerCase().includes(q) ||
        log.target_id.toLowerCase().includes(q) ||
        log.target_type.toLowerCase().includes(q) ||
        log.reason.toLowerCase().includes(q)
      );
    });
  }, [initialLogs, activeTab, roleFilter, query]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải nhật ký kiểm toán"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const tabs: Array<{ id: AuditTargetTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "shop", label: "Gian hàng" },
    { id: "site", label: "Site gian hàng" },
    { id: "user", label: "Tài khoản" },
    { id: "other", label: "Khác" },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Nhật ký kiểm toán</h1>
          <p className={shared.pageSubtitle}>
            Ghi nhận {formatInt(initialLogs.length)} thao tác quản trị thật trực tiếp từ cơ sở dữ
            liệu kiểm toán hệ thống.
          </p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc nhật ký theo đối tượng">
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
              placeholder="Tìm hành động, mã đối tượng, lý do..."
              aria-label="Tìm kiếm trong nhật ký kiểm toán"
            />
          </div>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} bản ghi</span>
          <select
            className={shared.toolbarSelect}
            value={roleFilter}
            onChange={(e) => setRoleFilter(e.target.value)}
            aria-label="Lọc theo vai trò thực hiện"
          >
            <option value="all">Tất cả vai trò</option>
            <option value="super_admin">Quản trị tối cao</option>
            <option value="admin">Quản trị viên</option>
          </select>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không tìm thấy bản ghi kiểm toán nào phù hợp với bộ lọc.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Thời gian</th>
                  <th>Người thực hiện</th>
                  <th>Phân hệ</th>
                  <th>Hành động & lý do</th>
                  <th className={shared.colNum}>Đối tượng</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((log) => (
                  <tr key={log.id}>
                    <td className={styles.timeCol}>
                      {log.created_at ? formatDateTimeVi(log.created_at) : EMPTY_DASH}
                    </td>
                    <td>
                      <div className={shared.cellPrimary}>
                        {getActorRoleLabelVi(log.actor_role)}
                      </div>
                      <div className={shared.cellSub}>{log.actor_id.slice(0, 12)}</div>
                    </td>
                    <td>
                      <StatusPill label={getAuditTargetLabelVi(log.target_type)} tone="neutral" />
                    </td>
                    <td className={styles.summaryCol}>
                      <div className={shared.cellPrimary}>{getAuditActionLabelVi(log.action)}</div>
                      <div className={shared.cellSub}>{log.reason || log.action}</div>
                    </td>
                    <td className={styles.targetCol}>
                      <div className={shared.cellPrimary}>{log.target_id.slice(0, 12)}</div>
                      <div className={shared.cellSub}>{log.ip_address || EMPTY_DASH}</div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filtered.map((log) => (
              <article key={log.id} className={shared.mobileItem}>
                <div className={shared.mobileLine1}>
                  <span className={shared.mobileIdentity}>{getAuditActionLabelVi(log.action)}</span>
                  <span className={shared.mobileMetric}>
                    {log.created_at ? formatShortTimeDateVi(log.created_at) : EMPTY_DASH}
                  </span>
                </div>
                <div className={shared.mobileLine2}>
                  <span className={shared.mobileMetaText}>
                    {getActorRoleLabelVi(log.actor_role)} · {log.target_id.slice(0, 10)}
                  </span>
                  <StatusPill label={getAuditTargetLabelVi(log.target_type)} tone="neutral" />
                </div>
                {log.reason && <p className={styles.mobileAuditSummary}>{log.reason}</p>}
              </article>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

export function AdminAuditLogClient(props: AdminAuditLogClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Nhật ký kiểm toán" />}>
      <AdminStateGate
        title="Nhật ký kiểm toán"
        subtitle="Theo dõi đầy đủ lịch sử duyệt gian hàng và cập nhật phân quyền trên hệ thống."
        emptyTitle="Chưa có bản ghi kiểm toán nào"
        emptyMessage="Các sự kiện quản trị sẽ được ghi lại tự động ngay khi có thao tác thẩm định."
        errorTitle="Không thể tải nhật ký kiểm toán"
        errorMessage="Kho lưu trữ sự kiện kiểm toán tạm thời không phản hồi. Vui lòng thử tải lại."
        loadingRows={6}
      >
        {({ initialQuery }) => (
          <AuditLogContent
            initialLogs={props.initialLogs}
            loadError={props.loadError}
            initialQuery={initialQuery}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
