"use client";

import React, { Suspense, useCallback, useEffect, useState } from "react";
import type { GatewayAuditLog, PaginatedResponse } from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import {
  ADMIN_AUDIT_TARGET_TYPE_LABELS,
  getAdminAuditTargetTypeLabel,
} from "@/features/admin/types";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

const QUICK_TARGET_TABS: Array<{ id: string; label: string }> = [
  { id: "all", label: "Tất cả đối tượng" },
  { id: "shop", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.shop },
  { id: "user", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.user },
  { id: "return_request", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.return_request },
  { id: "refund", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.refund },
  { id: "reconciliation_item", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.reconciliation_item },
  { id: "product", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.product },
  { id: "carrier", label: ADMIN_AUDIT_TARGET_TYPE_LABELS.carrier },
];

const ALL_TARGET_FILTER_OPTIONS = Object.entries(ADMIN_AUDIT_TARGET_TYPE_LABELS).filter(
  ([key]) => key !== "admin"
);

const PAGE_SIZE = 20;

function AuditLogContent({ initialQuery }: { initialQuery: string }) {
  const [targetType, setTargetType] = useState<string>("all");
  const [actorIdInput, setActorIdInput] = useState("");
  const [targetIdInput, setTargetIdInput] = useState(initialQuery);
  const [fromDateInput, setFromDateInput] = useState("");
  const [toDateInput, setToDateInput] = useState("");
  const [appliedFilters, setAppliedFilters] = useState({
    actorId: "",
    targetId: initialQuery,
    fromDate: "",
    toDate: "",
  });
  const [page, setPage] = useState(1);

  const [logs, setLogs] = useState<GatewayAuditLog[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const loadLogs = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", String(page));
      qs.set("page_size", String(PAGE_SIZE));
      if (targetType !== "all") qs.set("target_type", targetType);
      if (appliedFilters.actorId.trim()) qs.set("actor_id", appliedFilters.actorId.trim());
      if (appliedFilters.targetId.trim()) qs.set("target_id", appliedFilters.targetId.trim());
      if (appliedFilters.fromDate) {
        qs.set("from_time", `${appliedFilters.fromDate}T00:00:00+07:00`);
      }
      if (appliedFilters.toDate) {
        qs.set("to_time", `${appliedFilters.toDate}T23:59:59+07:00`);
      }

      const res = await api<PaginatedResponse<GatewayAuditLog>>(
        `admin/audit-logs?${qs.toString()}`
      );
      setLogs(res.items ?? []);
      setTotal(res.total ?? (res.items ?? []).length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải nhật ký kiểm toán lúc này."));
    } finally {
      setLoading(false);
    }
  }, [page, targetType, appliedFilters]);

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const handleApplyFilters = (e: React.FormEvent) => {
    e.preventDefault();
    setPage(1);
    setAppliedFilters({
      actorId: actorIdInput.trim(),
      targetId: targetIdInput.trim(),
      fromDate: fromDateInput,
      toDate: toDateInput,
    });
  };

  const handleResetFilters = () => {
    setTargetType("all");
    setActorIdInput("");
    setTargetIdInput("");
    setFromDateInput("");
    setToDateInput("");
    setPage(1);
    setAppliedFilters({ actorId: "", targetId: "", fromDate: "", toDate: "" });
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Nhật ký kiểm toán</h1>
          <p className={shared.pageSubtitle}>
            Ghi nhận {formatInt(total)} bản ghi thao tác quản trị (gian hàng, tài khoản, sản phẩm,
            trả hàng, hoàn tiền, đối soát và vận chuyển) trực tiếp từ hệ thống.
          </p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadLogs()}>
            Làm mới
          </Button>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc nhật ký theo loại đối tượng">
          {QUICK_TARGET_TABS.map((tab) => {
            const active = targetType === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => {
                  setTargetType(tab.id);
                  setPage(1);
                }}
              >
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      <form className={shared.toolbar} onSubmit={handleApplyFilters}>
        <div className={shared.toolbarLeft}>
          <select
            className={shared.toolbarSelect}
            value={targetType}
            onChange={(e) => {
              setTargetType(e.target.value);
              setPage(1);
            }}
            aria-label="Chọn loại đối tượng kiểm toán"
          >
            <option value="all">Tất cả loại đối tượng</option>
            {ALL_TARGET_FILTER_OPTIONS.map(([key, label]) => (
              <option key={key} value={key}>
                {label}
              </option>
            ))}
          </select>
          <div className={shared.searchWrap}>
            <span className={shared.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              type="search"
              className={shared.toolbarSearch}
              value={targetIdInput}
              onChange={(e) => setTargetIdInput(e.target.value)}
              placeholder="Lọc theo mã đối tượng (target ID)..."
              aria-label="Lọc theo mã đối tượng"
            />
          </div>
          <div className={shared.searchWrap}>
            <span className={shared.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              type="search"
              className={shared.toolbarSearch}
              value={actorIdInput}
              onChange={(e) => setActorIdInput(e.target.value)}
              placeholder="Lọc theo mã người thực hiện (actor ID)..."
              aria-label="Lọc theo mã người thực hiện"
            />
          </div>
          <label className={styles.dateFilterField}>
            <span className={styles.dateFilterLabel}>Từ ngày</span>
            <input
              type="date"
              className={styles.dateFilterInput}
              value={fromDateInput}
              onChange={(e) => setFromDateInput(e.target.value)}
              aria-label="Lọc từ ngày"
            />
          </label>
          <label className={styles.dateFilterField}>
            <span className={styles.dateFilterLabel}>Đến ngày</span>
            <input
              type="date"
              className={styles.dateFilterInput}
              value={toDateInput}
              onChange={(e) => setToDateInput(e.target.value)}
              aria-label="Lọc đến ngày"
            />
          </label>
          <Button type="submit" variant="primary" size="sm" shape="app">
            Lọc
          </Button>
          {(appliedFilters.actorId ||
            appliedFilters.targetId ||
            appliedFilters.fromDate ||
            appliedFilters.toDate ||
            targetType !== "all") && (
            <Button variant="outline" size="sm" shape="app" onClick={handleResetFilters}>
              Đặt lại
            </Button>
          )}
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>
            Trang {formatInt(page)} / {formatInt(totalPages)} ({formatInt(total)} bản ghi)
          </span>
        </div>
      </form>

      {loading && (
        <div className={shared.stateWrap}>
          <StateView kind="loading" surface="app" title="Đang tải nhật ký kiểm toán..." rows={5} />
        </div>
      )}

      {!loading && errorMsg && (
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải nhật ký kiểm toán"
            message={errorMsg}
            actionLabel="Thử lại"
            onAction={() => void loadLogs()}
          />
        </div>
      )}

      {!loading && !errorMsg && logs.length === 0 && (
        <p className={shared.emptyRow}>Không tìm thấy bản ghi kiểm toán nào phù hợp với bộ lọc.</p>
      )}

      {!loading && !errorMsg && logs.length > 0 && (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Thời gian</th>
                  <th>Người thực hiện</th>
                  <th>Loại đối tượng</th>
                  <th>Hành động & lý do</th>
                  <th className={shared.colNum}>Đối tượng</th>
                </tr>
              </thead>
              <tbody>
                {logs.map((log) => {
                  const isOpen = expandedId === log.id;
                  return (
                    <React.Fragment key={log.id}>
                      <tr
                        className={`${shared.rowInteractive} ${isOpen ? shared.rowSelected : ""}`}
                        onClick={() => setExpandedId((prev) => (prev === log.id ? null : log.id))}
                      >
                        <td className={styles.timeCol}>
                          {log.created_at ? formatDateTimeVi(log.created_at) : "—"}
                        </td>
                        <td>
                          <div className={shared.cellPrimary}>{log.actor_role || "Quản trị"}</div>
                          <div className={shared.cellSub}>{log.actor_id}</div>
                        </td>
                        <td>
                          <StatusPill
                            label={getAdminAuditTargetTypeLabel(log.target_type)}
                            tone="neutral"
                          />
                        </td>
                        <td className={styles.summaryCol}>
                          <div className={shared.cellPrimary}>{log.action}</div>
                          <div className={shared.cellSub}>{log.reason || "Không có ghi chú"}</div>
                        </td>
                        <td className={styles.targetCol}>
                          <div className={shared.cellPrimary}>{log.target_id}</div>
                          <div className={shared.cellSub}>{log.ip_address || "—"}</div>
                        </td>
                      </tr>
                      {isOpen && (
                        <tr className={shared.detailRow}>
                          <td colSpan={5}>
                            <div className={shared.detailWell}>
                              <div className={shared.detailGrid}>
                                <div className={shared.detailField}>
                                  <span className={shared.detailKey}>Trạng thái trước</span>
                                  <span className={shared.detailValPlain}>
                                    {log.before_json || "{}"}
                                  </span>
                                </div>
                                <div className={shared.detailField}>
                                  <span className={shared.detailKey}>Trạng thái sau</span>
                                  <span className={shared.detailValPlain}>
                                    {log.after_json || "{}"}
                                  </span>
                                </div>
                                <div className={shared.detailField}>
                                  <span className={shared.detailKey}>Địa chỉ nguồn</span>
                                  <span className={shared.detailValPlain}>
                                    {log.ip_address || "Không ghi nhận"}
                                  </span>
                                </div>
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </React.Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {logs.map((log) => (
              <article key={log.id} className={shared.mobileItem}>
                <div className={shared.mobileLine1}>
                  <span className={shared.mobileIdentity}>{log.action}</span>
                  <span className={shared.mobileMetric}>
                    {log.created_at ? formatDateTimeVi(log.created_at) : "—"}
                  </span>
                </div>
                <div className={shared.mobileLine2}>
                  <span className={shared.mobileMetaText}>
                    {log.actor_role} · {log.target_id}
                  </span>
                  <StatusPill
                    label={getAdminAuditTargetTypeLabel(log.target_type)}
                    tone="neutral"
                  />
                </div>
                {log.reason && <p className={styles.mobileAuditSummary}>{log.reason}</p>}
              </article>
            ))}
          </div>

          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-end",
              gap: 8,
              marginTop: 16,
            }}
          >
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              Trang trước
            </Button>
            <span className={shared.resultCount}>
              {formatInt(page)} / {formatInt(totalPages)}
            </span>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={page >= totalPages}
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            >
              Trang sau
            </Button>
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
      subtitle="Theo dõi đầy đủ lịch sử duyệt gian hàng, cập nhật tài khoản và thay đổi phân quyền."
      emptyTitle="Chưa có bản ghi kiểm toán nào"
      emptyMessage="Các sự kiện quản trị sẽ được ghi lại tự động ngay khi có thao tác."
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
