"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button, IconSearch, StateView } from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import { ADMIN_STAFF_ROLES, getUserRoleLabel } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type { PaginatedResponse } from "@/lib/gateway-types";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { AdminDrawer } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import type { GwAuditLog } from "./adminApiTypes";
import { ADMIN_AUDIT_TARGET_TYPE_LABELS, getAdminAuditTargetTypeLabel } from "./types";
import {
  buildAuditLogsQueryParams,
  filterAuditLogsClientSide,
  getAdminAuditActionLabelVi,
} from "./AdminAuditHelpers";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/audit-log/page.module.css";

const PAGE_SIZE = 20;

const TARGET_TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "", label: "Tất cả phân hệ" },
  { value: "site", label: "Site gian hàng" },
  ...Object.entries(ADMIN_AUDIT_TARGET_TYPE_LABELS).map(([value, label]) => ({
    value,
    label,
  })),
];

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
  initialTotal?: number;
  loadError: string | null;
}

function AuditLogContent({
  initialLogs,
  initialTotal,
  loadError: initialError,
  initialQuery,
}: {
  initialLogs: GwAuditLog[];
  initialTotal: number;
  loadError: string | null;
  initialQuery: string;
}) {
  const [logs, setLogs] = useState<GwAuditLog[]>(initialLogs);
  const [total, setTotal] = useState<number>(initialTotal);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(initialError);

  // Server-side filters (forwarded to GET /api/admin/audit-logs -> gateway ListAuditLogs)
  const [targetType, setTargetType] = useState("");
  const [actorId, setActorId] = useState("");
  const [targetId, setTargetId] = useState("");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");

  // Client-side filters over returned page (gateway ListAuditLogs does not accept `action` param)
  const [actionQuery, setActionQuery] = useState(initialQuery);
  const [roleFilter, setRoleFilter] = useState<string>("all");
  const [selectedLog, setSelectedLog] = useState<GwAuditLog | null>(null);

  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  const fetchLogs = useCallback(
    async (opts: {
      targetType: string;
      actorId: string;
      targetId: string;
      fromDate: string;
      toDate: string;
      page: number;
    }) => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      setLoading(true);
      setLoadError(null);
      try {
        const qs = buildAuditLogsQueryParams({
          targetType: opts.targetType,
          actorId: opts.actorId,
          targetId: opts.targetId,
          fromDate: opts.fromDate,
          toDate: opts.toDate,
          page: opts.page,
          pageSize: PAGE_SIZE,
        });
        const res = await api<PaginatedResponse<GwAuditLog>>(
          `/api/admin/audit-logs?${qs.toString()}`,
          { signal: controller.signal }
        );
        if (controller.signal.aborted) return;
        setLogs(res.items ?? []);
        setTotal(res.total ?? (res.items ?? []).length);
      } catch (err) {
        if (controller.signal.aborted) return;
        setLoadError(getErrorMessage(err, "Không thể tải nhật ký kiểm toán lúc này."));
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    },
    []
  );

  const handleFilterSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (fromDate && toDate && fromDate > toDate) {
      setLoadError("Ngày bắt đầu phải trước hoặc bằng ngày kết thúc.");
      return;
    }
    setPage(1);
    void fetchLogs({ targetType, actorId, targetId, fromDate, toDate, page: 1 });
  };

  const handleResetFilters = () => {
    setTargetType("");
    setActorId("");
    setTargetId("");
    setFromDate("");
    setToDate("");
    setActionQuery("");
    setRoleFilter("all");
    setPage(1);
    void fetchLogs({
      targetType: "",
      actorId: "",
      targetId: "",
      fromDate: "",
      toDate: "",
      page: 1,
    });
  };

  const handlePageChange = (nextPage: number) => {
    setPage(nextPage);
    void fetchLogs({ targetType, actorId, targetId, fromDate, toDate, page: nextPage });
  };

  const filtered = useMemo(
    () => filterAuditLogsClientSide(logs, { actionQuery, roleFilter }),
    [logs, actionQuery, roleFilter]
  );

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const beforePairs = selectedLog ? parseJsonEntries(selectedLog.before_json) : null;
  const afterPairs = selectedLog ? parseJsonEntries(selectedLog.after_json) : null;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Nhật ký kiểm toán</h1>
        </div>
        <div className={shared.titleActions}>
          <span className={shared.resultCount}>{formatInt(total)} bản ghi</span>
        </div>
      </header>

      <form className={styles.filterForm} onSubmit={handleFilterSubmit} noValidate>
        <div className={styles.filterField}>
          <label htmlFor="audit-target-type" className={shared.fieldLabel}>
            Phân hệ
          </label>
          <select
            id="audit-target-type"
            className={`${shared.toolbarSelect} ${styles.selectFull}`}
            value={targetType}
            onChange={(e) => setTargetType(e.target.value)}
          >
            {TARGET_TYPE_OPTIONS.map((opt) => (
              <option key={opt.value || "__all"} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>

        <div className={styles.filterField}>
          <label htmlFor="audit-actor-id" className={shared.fieldLabel}>
            Mã người thực hiện (actor_id)
          </label>
          <input
            id="audit-actor-id"
            type="text"
            className={shared.textInput}
            value={actorId}
            onChange={(e) => setActorId(e.target.value)}
            placeholder="UUID người thao tác..."
          />
        </div>

        <div className={styles.filterField}>
          <label htmlFor="audit-from-date" className={shared.fieldLabel}>
            Từ ngày
          </label>
          <input
            id="audit-from-date"
            type="date"
            className={shared.textInput}
            value={fromDate}
            onChange={(e) => setFromDate(e.target.value)}
          />
        </div>

        <div className={styles.filterField}>
          <label htmlFor="audit-to-date" className={shared.fieldLabel}>
            Đến ngày
          </label>
          <input
            id="audit-to-date"
            type="date"
            className={shared.textInput}
            value={toDate}
            onChange={(e) => setToDate(e.target.value)}
          />
        </div>

        <div className={styles.filterField}>
          <label htmlFor="audit-target-id" className={shared.fieldLabel}>
            Mã đối tượng (target_id)
          </label>
          <input
            id="audit-target-id"
            type="text"
            className={shared.textInput}
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
            placeholder="UUID gian hàng, đơn, hoàn tiền..."
          />
        </div>

        <div className={styles.filterField}>
          <label htmlFor="audit-action-query" className={shared.fieldLabel}>
            Lọc hành động / lý do (trang hiện tại)
          </label>
          <div className={shared.searchWrap}>
            <span className={shared.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              id="audit-action-query"
              type="search"
              className={shared.toolbarSearch}
              value={actionQuery}
              onChange={(e) => setActionQuery(e.target.value)}
              placeholder="Tên hành động, lý do..."
            />
          </div>
        </div>

        <div className={styles.filterField}>
          <label htmlFor="audit-role-filter" className={shared.fieldLabel}>
            Vai trò thực hiện
          </label>
          <select
            id="audit-role-filter"
            className={`${shared.toolbarSelect} ${styles.selectFull}`}
            value={roleFilter}
            onChange={(e) => setRoleFilter(e.target.value)}
          >
            <option value="all">Tất cả vai trò</option>
            {ADMIN_STAFF_ROLES.map((r) => (
              <option key={r} value={r}>
                {getUserRoleLabel(r)}
              </option>
            ))}
          </select>
        </div>

        <div className={styles.filterActions}>
          <span className={shared.resultCount}>
            Hiển thị {formatInt(filtered.length)} / {formatInt(logs.length)} bản ghi trang này
          </span>
          <div className={styles.filterBtns}>
            <Button type="submit" variant="primary" size="sm" shape="app" loading={loading}>
              Áp dụng bộ lọc
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              shape="app"
              disabled={loading}
              onClick={handleResetFilters}
            >
              Đặt lại
            </Button>
          </div>
        </div>
      </form>

      {loadError ? (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải nhật ký kiểm toán"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => void fetchLogs({ targetType, actorId, targetId, fromDate, toDate, page })}
        />
      ) : loading ? (
        <div className={shared.stateWrap}>
          <StateView kind="loading" surface="app" title="Đang tải nhật ký kiểm toán..." rows={6} />
        </div>
      ) : filtered.length === 0 ? (
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
                  const targetLabel =
                    log.target_type.toLowerCase() === "site"
                      ? "Site gian hàng"
                      : getAdminAuditTargetTypeLabel(log.target_type);
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
                        <div className={shared.cellPrimary}>{getUserRoleLabel(log.actor_role)}</div>
                        <div className={shared.cellSub}>{log.actor_id.slice(0, 12)}</div>
                      </td>
                      <td>
                        <StatusPill label={targetLabel} tone="neutral" />
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
                            {getAdminAuditActionLabelVi(log.action)}
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
            {filtered.map((log) => {
              const targetLabel =
                log.target_type.toLowerCase() === "site"
                  ? "Site gian hàng"
                  : getAdminAuditTargetTypeLabel(log.target_type);
              return (
                <article key={log.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => setSelectedLog(log)}
                  >
                    <span className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>
                        {getAdminAuditActionLabelVi(log.action)}
                      </span>
                      <span className={shared.mobileMetric}>
                        {log.created_at ? formatShortTimeDateVi(log.created_at) : EMPTY_DASH}
                      </span>
                    </span>
                    <span className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {[getUserRoleLabel(log.actor_role), log.target_id.slice(0, 10)]
                          .filter(Boolean)
                          .join(", ")}
                      </span>
                      <StatusPill label={targetLabel} tone="neutral" />
                    </span>
                    {log.reason && <span className={styles.mobileAuditSummary}>{log.reason}</span>}
                  </button>
                </article>
              );
            })}
          </div>

          {totalPages > 1 && (
            <div className={styles.paginationRow}>
              <span className={styles.paginationInfo}>
                Trang {formatInt(page)} / {formatInt(totalPages)} ({formatInt(total)} bản ghi)
              </span>
              <div className={styles.paginationBtns}>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={page <= 1 || loading}
                  onClick={() => handlePageChange(page - 1)}
                >
                  Trang trước
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={page >= totalPages || loading}
                  onClick={() => handlePageChange(page + 1)}
                >
                  Trang sau
                </Button>
              </div>
            </div>
          )}
        </>
      )}

      <AdminDrawer
        open={Boolean(selectedLog)}
        onClose={() => setSelectedLog(null)}
        title={selectedLog ? getAdminAuditActionLabelVi(selectedLog.action) : ""}
        subtitle={selectedLog?.created_at ? formatDateTimeVi(selectedLog.created_at) : undefined}
        headerBadge={
          selectedLog ? (
            <StatusPill
              label={
                selectedLog.target_type.toLowerCase() === "site"
                  ? "Site gian hàng"
                  : getAdminAuditTargetTypeLabel(selectedLog.target_type)
              }
              tone="neutral"
            />
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
                    {getUserRoleLabel(selectedLog.actor_role)}
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
            initialTotal={props.initialTotal ?? props.initialLogs.length}
            loadError={props.loadError}
            initialQuery={initialQuery}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
