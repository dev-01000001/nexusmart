"use client";

import React, { Suspense, useMemo, useState } from "react";
import type { AuditLogEntry } from "@/data";
import { IconSearch } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { formatShortTimeDateVi } from "@/features/admin/adminMeta";
import { useAdminAuditLogs } from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type AuditModuleTab =
  | "all"
  | "Gian hàng"
  | "Nội dung"
  | "Kiểm duyệt"
  | "Hội viên"
  | "Cấu hình"
  | "Khiếu nại"
  | "Đối soát";
type AuditRoleFilter = "all" | AuditLogEntry["role"];

function AuditLogContent({ initialQuery }: { initialQuery: string }) {
  const auditLogs = useAdminAuditLogs();
  const [activeTab, setActiveTab] = useState<AuditModuleTab>("all");
  const [roleFilter, setRoleFilter] = useState<AuditRoleFilter>("all");
  const [query, setQuery] = useState(initialQuery);

  const counts = useMemo(() => {
    const map: Record<AuditModuleTab, number> = {
      all: auditLogs.length,
      "Gian hàng": 0,
      "Nội dung": 0,
      "Kiểm duyệt": 0,
      "Hội viên": 0,
      "Cấu hình": 0,
      "Khiếu nại": 0,
      "Đối soát": 0,
    };
    for (const log of auditLogs) {
      if (log.module in map) {
        map[log.module as AuditModuleTab] += 1;
      }
    }
    return map;
  }, [auditLogs]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return auditLogs.filter((log) => {
      if (activeTab !== "all" && log.module !== activeTab) return false;
      if (roleFilter !== "all" && log.role !== roleFilter) return false;
      if (!q) return true;
      return (
        log.action.toLowerCase().includes(q) ||
        log.actor.toLowerCase().includes(q) ||
        log.role.toLowerCase().includes(q) ||
        log.target.toLowerCase().includes(q) ||
        log.changeSummary.toLowerCase().includes(q) ||
        log.traceId.toLowerCase().includes(q)
      );
    });
  }, [auditLogs, activeTab, roleFilter, query]);

  const tabs: Array<{ id: AuditModuleTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "Gian hàng", label: "Gian hàng" },
    { id: "Nội dung", label: "Nội dung" },
    { id: "Kiểm duyệt", label: "Kiểm duyệt" },
    { id: "Hội viên", label: "Hội viên" },
    { id: "Cấu hình", label: "Cấu hình" },
    { id: "Khiếu nại", label: "Khiếu nại" },
    { id: "Đối soát", label: "Đối soát" },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Nhật ký kiểm toán</h1>
          <p className={shared.pageSubtitle}>
            Ghi nhận {formatInt(auditLogs.length)} thao tác quản trị kèm mã truy vết trên toàn bộ
            phân hệ (Gian hàng, Nội dung, Kiểm duyệt, Hội viên, Cấu hình, Khiếu nại, Đối soát).
          </p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc nhật ký theo phân hệ">
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
              placeholder="Tìm hành động, đối tượng, người thực hiện, mã trace..."
              aria-label="Tìm kiếm trong nhật ký kiểm toán"
            />
          </div>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} bản ghi</span>
          <select
            className={shared.toolbarSelect}
            value={roleFilter}
            onChange={(e) => setRoleFilter(e.target.value as AuditRoleFilter)}
            aria-label="Lọc theo vai trò thực hiện"
          >
            <option value="all">Tất cả vai trò</option>
            <option value="Quản trị viên">Quản trị viên</option>
            <option value="Vận hành sàn">Vận hành sàn</option>
            <option value="Đối soát tài chính">Đối soát tài chính</option>
            <option value="Hệ thống">Hệ thống</option>
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
                  <th>Hành động & tóm tắt thay đổi</th>
                  <th className={shared.colNum}>Đối tượng & mã truy vết</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((log) => (
                  <tr key={log.id}>
                    <td className={styles.timeCol}>{formatDateTimeVi(log.timestamp)}</td>
                    <td>
                      <div className={shared.cellPrimary}>{log.actor}</div>
                      <div className={shared.cellSub}>{log.role}</div>
                    </td>
                    <td>
                      <StatusPill label={log.module} tone="neutral" />
                    </td>
                    <td className={styles.summaryCol}>
                      <div className={shared.cellPrimary}>{log.action}</div>
                      <div className={shared.cellSub}>{log.changeSummary}</div>
                    </td>
                    <td className={styles.targetCol}>
                      <div className={shared.cellPrimary}>{log.target}</div>
                      <div className={shared.cellSub}>{log.traceId}</div>
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
                  <span className={shared.mobileIdentity}>{log.action}</span>
                  <span className={shared.mobileMetric}>
                    {formatShortTimeDateVi(log.timestamp)}
                  </span>
                </div>
                <div className={shared.mobileLine2}>
                  <span className={shared.mobileMetaText}>
                    {log.actor} · {log.target} · {log.traceId}
                  </span>
                  <StatusPill label={log.module} tone="neutral" />
                </div>
                <p className={styles.mobileAuditSummary}>{log.changeSummary}</p>
              </article>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

function AuditLogRoute() {
  return (
    <AdminStateGate
      title="Nhật ký kiểm toán"
      subtitle="Theo dõi đầy đủ lịch sử thay đổi cấu hình, duyệt gian hàng, phân xử khiếu nại và đối soát."
      emptyTitle="Chưa có bản ghi kiểm toán nào"
      emptyMessage="Các sự kiện quản trị sẽ được ghi lại tự động kèm mã trace ngay khi có thao tác."
      errorTitle="Không thể tải nhật ký kiểm toán"
      errorMessage="Kho lưu trữ sự kiện kiểm toán tạm thời không phản hồi. Vui lòng thử tải lại."
      loadingRows={6}
    >
      {({ initialQuery }) => <AuditLogContent initialQuery={initialQuery} />}
    </AdminStateGate>
  );
}

export default function AdminAuditLogPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Nhật ký kiểm toán" />}>
      <AuditLogRoute />
    </Suspense>
  );
}
