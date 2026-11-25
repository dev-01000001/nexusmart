"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { AdminDrawer, SlidingTabBar } from "./AdminUi";
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

function parseJsonEntries(raw: string): Array<[string, string]> | null {
  const trimmed = raw.trim();
  if (!trimmed || trimmed === "{}" || trimmed === "null") return null;
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
    const entries = Object.entries(parsed as Record<string, unknown>);
    if (entries.length === 0) return null;
    return entries.map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)]);
  } catch {
    return null;
  }
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
  const [selectedLog, setSelectedLog] = useState<GwAuditLog | null>(null);

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
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const tabs: Array<{ id: AuditTargetTab; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: counts.all },
    { id: "shop", label: "Gian hàng", count: counts.shop },
    { id: "site", label: "Site gian hàng", count: counts.site },
    { id: "user", label: "Tài khoản", count: counts.user },
    { id: "other", label: "Khác", count: counts.other },
  ];

  const beforePairs = selectedLog ? parseJsonEntries(selectedLog.before_json) : null;
  const afterPairs = selectedLog ? parseJsonEntries(selectedLog.after_json) : null;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Nhật ký kiểm toán</h1>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={activeTab}
        onChange={setActiveTab}
        ariaLabel="Lọc nhật ký theo đối tượng"
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
              placeholder="Tìm hành động, mã đối tượng, lý do..."
              aria-label="Tìm kiếm trong nhật ký kiểm toán"
            />
          </div>
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

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} bản ghi</span>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không tìm thấy bản ghi kiểm toán phù hợp.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Thời gian</th>
                  <th>Người thực hiện</th>
                  <th>Phân hệ</th>
                  <th>Hành động &amp; lý do</th>
                  <th className={shared.colNum}>Đối tượng</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((log) => {
                  const selected = selectedLog?.id === log.id;
                  return (
                    <tr
                      key={log.id}
                      className={`${shared.rowInteractive} ${selected ? shared.rowSelected : ""}`}
                      onClick={() => setSelectedLog(log)}
                    >
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
                        <button
                          type="button"
                          className={shared.rowTriggerBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setSelectedLog(log);
                          }}
                        >
                          <span className={shared.cellPrimary}>
                            {getAuditActionLabelVi(log.action)}
                          </span>
                          <span className={shared.cellSub}>{log.reason || log.action}</span>
                        </button>
                      </td>
                      <td className={styles.targetCol}>
                        <div className={shared.cellPrimary}>{log.target_id.slice(0, 12)}</div>
                        <div className={shared.cellSub}>{log.ip_address || EMPTY_DASH}</div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filtered.map((log) => (
              <article key={log.id} className={shared.mobileItem}>
                <button
                  type="button"
                  className={shared.mobileItemBtn}
                  onClick={() => setSelectedLog(log)}
                >
                  <span className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>
                      {getAuditActionLabelVi(log.action)}
                    </span>
                    <span className={shared.mobileMetric}>
                      {log.created_at ? formatShortTimeDateVi(log.created_at) : EMPTY_DASH}
                    </span>
                  </span>
                  <span className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {[getActorRoleLabelVi(log.actor_role), log.target_id.slice(0, 10)]
                        .filter(Boolean)
                        .join(", ")}
                    </span>
                    <StatusPill label={getAuditTargetLabelVi(log.target_type)} tone="neutral" />
                  </span>
                  {log.reason && <span className={styles.mobileAuditSummary}>{log.reason}</span>}
                </button>
              </article>
            ))}
          </div>
        </>
      )}

      <AdminDrawer
        open={Boolean(selectedLog)}
        onClose={() => setSelectedLog(null)}
        title={selectedLog ? getAuditActionLabelVi(selectedLog.action) : ""}
        subtitle={selectedLog?.created_at ? formatDateTimeVi(selectedLog.created_at) : undefined}
        headerBadge={
          selectedLog ? (
            <StatusPill label={getAuditTargetLabelVi(selectedLog.target_type)} tone="neutral" />
          ) : undefined
        }
      >
        {selectedLog && (
          <>
            <div className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã hành động</span>
                  <span className={`${shared.detailVal} num`}>{selectedLog.action}</span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Vai trò thực hiện</span>
                  <span className={shared.detailVal}>
                    {getActorRoleLabelVi(selectedLog.actor_role)}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã người thực hiện</span>
                  <span className={`${shared.detailValPlain} num`}>{selectedLog.actor_id}</span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã đối tượng</span>
                  <span className={`${shared.detailValPlain} num`}>{selectedLog.target_id}</span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Địa chỉ IP</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {selectedLog.ip_address || EMPTY_DASH}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã bản ghi</span>
                  <span className={`${shared.detailValPlain} num`}>{selectedLog.id}</span>
                </div>
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Lý do ghi nhận</span>
                  <span className={shared.detailValPlain}>{selectedLog.reason || EMPTY_DASH}</span>
                </div>
              </div>
            </div>

            {beforePairs && (
              <div className={styles.diffBox}>
                <span className={shared.fieldLabel}>Trạng thái trước thay đổi</span>
                <dl className={styles.diffList}>
                  {beforePairs.map(([k, v]) => (
                    <div key={k} className={styles.diffRow}>
                      <dt className={styles.diffKey}>{k}</dt>
                      <dd className={`${styles.diffVal} num`}>{v}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}

            {afterPairs && (
              <div className={styles.diffBox}>
                <span className={shared.fieldLabel}>Trạng thái sau thay đổi</span>
                <dl className={styles.diffList}>
                  {afterPairs.map(([k, v]) => (
                    <div key={k} className={styles.diffRow}>
                      <dt className={styles.diffKey}>{k}</dt>
                      <dd className={`${styles.diffVal} num`}>{v}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}
          </>
        )}
      </AdminDrawer>
    </div>
  );
}

export function AdminAuditLogClient(props: AdminAuditLogClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Nhật ký kiểm toán" />}>
      <AdminStateGate
        title="Nhật ký kiểm toán"
        emptyTitle="Chưa có bản ghi kiểm toán"
        emptyMessage="Các thao tác quản trị sẽ tự động ghi nhận tại đây."
        errorTitle="Không thể tải nhật ký kiểm toán"
        errorMessage="Kho lưu trữ kiểm toán tạm gián đoạn. Vui lòng thử lại."
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
