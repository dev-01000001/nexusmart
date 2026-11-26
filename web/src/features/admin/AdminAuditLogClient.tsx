"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { FileSearch, RotateCcw, Search } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  PageHeader,
  StatusBadge,
} from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, getErrorMessage } from "@/lib/api";
import { ADMIN_STAFF_ROLES, getUserRoleLabel } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type { PaginatedResponse } from "@/lib/gateway-types";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import type { GwAuditLog } from "./adminApiTypes";
import { ADMIN_AUDIT_TARGET_TYPE_LABELS, getAdminAuditTargetTypeLabel } from "./types";
import {
  buildAuditLogsQueryParams,
  filterAuditLogsClientSide,
  getAdminAuditActionLabelVi,
} from "./AdminAuditHelpers";
import {
  DetailGrid,
  DetailItem,
  FilterSelect,
  TableLoadingSkeleton,
  TablePagination,
  formatPageCountSummary,
} from "./config/adminUiHelpers";

const PAGE_SIZE = 20;

const TARGET_TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "", label: "Tất cả phân hệ" },
  ...Object.entries(ADMIN_AUDIT_TARGET_TYPE_LABELS).map(([value, label]) => ({
    value,
    label,
  })),
];

const ROLE_FILTER_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "all", label: "Tất cả vai trò" },
  ...ADMIN_STAFF_ROLES.map((r) => ({
    value: r,
    label: getUserRoleLabel(r),
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
    <div className="space-y-5">
      <PageHeader
        title="Nhật ký kiểm toán"
        badges={
          <Badge variant="neutral" className="tabular-nums">
            {formatInt(total)} bản ghi
          </Badge>
        }
        subtitle="Chế độ chỉ đọc — tra cứu lịch sử thao tác quản trị theo phân hệ, thời gian và người thực hiện."
      />

      <Card>
        <CardContent className="pt-4">
          <form
            className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4"
            onSubmit={handleFilterSubmit}
            noValidate
          >
            <Field className="gap-1.5">
              <FieldLabel htmlFor="audit-target-type">Phân hệ</FieldLabel>
              <FilterSelect
                id="audit-target-type"
                value={targetType}
                onValueChange={setTargetType}
                options={TARGET_TYPE_OPTIONS}
                ariaLabel="Phân hệ"
              />
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="audit-actor-id">Mã người thực hiện (actor_id)</FieldLabel>
              <Input
                id="audit-actor-id"
                type="text"
                value={actorId}
                onChange={(e) => setActorId(e.target.value)}
                placeholder="UUID người thao tác..."
                className="bg-card"
              />
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="audit-from-date">Từ ngày</FieldLabel>
              <Input
                id="audit-from-date"
                type="date"
                value={fromDate}
                onChange={(e) => setFromDate(e.target.value)}
                className="bg-card tabular-nums"
              />
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="audit-to-date">Đến ngày</FieldLabel>
              <Input
                id="audit-to-date"
                type="date"
                value={toDate}
                onChange={(e) => setToDate(e.target.value)}
                className="bg-card tabular-nums"
              />
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="audit-target-id">Mã đối tượng (target_id)</FieldLabel>
              <Input
                id="audit-target-id"
                type="text"
                value={targetId}
                onChange={(e) => setTargetId(e.target.value)}
                placeholder="UUID gian hàng, đơn, hoàn tiền..."
                className="bg-card"
              />
            </Field>

            <Field className="gap-1.5 sm:col-span-2 lg:col-span-2">
              <FieldLabel htmlFor="audit-action-query">
                Lọc hành động / lý do (trang hiện tại)
              </FieldLabel>
              <div className="relative">
                <Search
                  className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  id="audit-action-query"
                  type="search"
                  value={actionQuery}
                  onChange={(e) => setActionQuery(e.target.value)}
                  placeholder="Tên hành động, lý do..."
                  className="bg-card pl-8"
                />
              </div>
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="audit-role-filter">Vai trò thực hiện</FieldLabel>
              <FilterSelect
                id="audit-role-filter"
                value={roleFilter}
                onValueChange={setRoleFilter}
                options={ROLE_FILTER_OPTIONS}
                ariaLabel="Vai trò thực hiện"
              />
            </Field>

            <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 sm:col-span-2 lg:col-span-4">
              <span className="text-[13px] text-muted-foreground tabular-nums">
                Hiển thị {formatInt(filtered.length)} / {formatInt(logs.length)} bản ghi trang này
              </span>
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={loading}
                  onClick={handleResetFilters}
                >
                  <RotateCcw className="size-3.5" aria-hidden="true" />
                  <span>Đặt lại</span>
                </Button>
                <Button type="submit" size="sm" disabled={loading}>
                  {loading ? "Đang lọc..." : "Áp dụng bộ lọc"}
                </Button>
              </div>
            </div>
          </form>
        </CardContent>
      </Card>

      {loadError ? (
        <ErrorState
          title="Không thể tải nhật ký kiểm toán"
          description={loadError}
          onRetry={() => void fetchLogs({ targetType, actorId, targetId, fromDate, toDate, page })}
        />
      ) : loading ? (
        <TableLoadingSkeleton rows={6} label="Đang tải nhật ký kiểm toán..." />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={FileSearch}
          title="Không tìm thấy bản ghi kiểm toán phù hợp"
          description="Thử thay đổi khoảng ngày, phân hệ hoặc từ khóa tìm kiếm để xem thêm bản ghi."
          action={
            <Button type="button" variant="outline" size="sm" onClick={handleResetFilters}>
              Đặt lại
            </Button>
          }
        />
      ) : (
        <IndexTable
          footer={
            totalPages > 1 ? (
              <TablePagination
                page={page}
                totalPages={totalPages}
                summary={formatPageCountSummary(page, totalPages, total, "bản ghi")}
                disabled={loading}
                onPageChange={handlePageChange}
              />
            ) : (
              <span className="tabular-nums">
                Hiển thị {formatInt(filtered.length)} bản ghi (tổng {formatInt(total)})
              </span>
            )
          }
        >
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Thời gian</TableHead>
                  <TableHead>Người thực hiện</TableHead>
                  <TableHead>Phân hệ</TableHead>
                  <TableHead>Hành động &amp; lý do</TableHead>
                  <TableHead className="text-right">Đối tượng</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((log) => {
                  const selected = selectedLog?.id === log.id;
                  const targetLabel = getAdminAuditTargetTypeLabel(log.target_type);
                  return (
                    <TableRow
                      key={log.id}
                      data-state={selected ? "selected" : undefined}
                      className="cursor-pointer"
                      onClick={() => setSelectedLog(log)}
                    >
                      <TableCell className="text-[13px] text-muted-foreground tabular-nums">
                        {log.created_at ? formatDateTimeVi(log.created_at) : EMPTY_DASH}
                      </TableCell>
                      <TableCell>
                        <div className="font-medium text-foreground">
                          {getUserRoleLabel(log.actor_role)}
                        </div>
                        <div className="text-xs text-muted-foreground tabular-nums">
                          {log.actor_id.slice(0, 12)}
                        </div>
                      </TableCell>
                      <TableCell>
                        <StatusBadge tone="neutral">{targetLabel}</StatusBadge>
                      </TableCell>
                      <TableCell className="max-w-[440px] whitespace-normal">
                        <button
                          type="button"
                          className="flex flex-col items-start text-left focus-visible:underline focus-visible:outline-none"
                          onClick={(e) => {
                            e.stopPropagation();
                            setSelectedLog(log);
                          }}
                        >
                          <span className="font-medium text-foreground">
                            {getAdminAuditActionLabelVi(log.action)}
                          </span>
                          <span className="line-clamp-2 text-xs text-muted-foreground">
                            {log.reason || log.action}
                          </span>
                        </button>
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        <div className="font-medium text-foreground">
                          {log.target_id.slice(0, 12)}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {log.ip_address || EMPTY_DASH}
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          <div className="md:hidden">
            {filtered.map((log) => {
              const targetLabel = getAdminAuditTargetTypeLabel(log.target_type);
              return (
                <IndexRowCard
                  key={log.id}
                  title={getAdminAuditActionLabelVi(log.action)}
                  badge={<StatusBadge tone="neutral">{targetLabel}</StatusBadge>}
                  meta={
                    <div className="flex w-full flex-col gap-0.5">
                      <div className="flex flex-wrap items-center gap-x-2">
                        <span>{getUserRoleLabel(log.actor_role)}</span>
                        <span>·</span>
                        <span className="tabular-nums">
                          {log.created_at ? formatShortTimeDateVi(log.created_at) : EMPTY_DASH}
                        </span>
                        {log.target_id ? (
                          <>
                            <span>·</span>
                            <span className="tabular-nums">{log.target_id.slice(0, 10)}</span>
                          </>
                        ) : null}
                      </div>
                      {log.reason ? (
                        <span className="line-clamp-2 text-foreground/85">{log.reason}</span>
                      ) : null}
                    </div>
                  }
                  onClick={() => setSelectedLog(log)}
                />
              );
            })}
          </div>
        </IndexTable>
      )}

      <Sheet open={Boolean(selectedLog)} onOpenChange={(open) => !open && setSelectedLog(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
          {selectedLog ? (
            <>
              <SheetHeader className="border-b border-border pb-4">
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>{getAdminAuditActionLabelVi(selectedLog.action)}</SheetTitle>
                  <StatusBadge tone="neutral">
                    {getAdminAuditTargetTypeLabel(selectedLog.target_type)}
                  </StatusBadge>
                </div>
                {selectedLog.created_at ? (
                  <SheetDescription className="tabular-nums">
                    {formatDateTimeVi(selectedLog.created_at)}
                  </SheetDescription>
                ) : null}
              </SheetHeader>

              <div className="space-y-4 px-4 pb-6">
                <DetailGrid>
                  <DetailItem label="Mã hành động" numeric>
                    {selectedLog.action}
                  </DetailItem>
                  <DetailItem label="Vai trò thực hiện">
                    {getUserRoleLabel(selectedLog.actor_role)}
                  </DetailItem>
                  <DetailItem label="Mã người thực hiện" numeric>
                    {selectedLog.actor_id}
                  </DetailItem>
                  <DetailItem label="Mã đối tượng" numeric>
                    {selectedLog.target_id}
                  </DetailItem>
                  <DetailItem label="Địa chỉ IP" numeric>
                    {selectedLog.ip_address || EMPTY_DASH}
                  </DetailItem>
                  <DetailItem label="Mã bản ghi" numeric>
                    {selectedLog.id}
                  </DetailItem>
                  <DetailItem label="Lý do ghi nhận" wide>
                    {selectedLog.reason || EMPTY_DASH}
                  </DetailItem>
                </DetailGrid>

                {beforePairs ? (
                  <div className="space-y-2 rounded-lg border border-border bg-muted/35 p-3.5">
                    <h3 className="text-xs font-semibold text-foreground">
                      Trạng thái trước thay đổi
                    </h3>
                    <dl className="divide-y divide-border">
                      {beforePairs.map(([k, v]) => (
                        <div
                          key={k}
                          className="grid grid-cols-1 gap-1 py-1.5 text-[13px] first:pt-0 last:pb-0 sm:grid-cols-[minmax(110px,140px)_minmax(0,1fr)] sm:gap-2.5"
                        >
                          <dt className="text-muted-foreground [overflow-wrap:anywhere]">{k}</dt>
                          <dd className="text-foreground tabular-nums [overflow-wrap:anywhere]">
                            {v}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  </div>
                ) : null}

                {afterPairs ? (
                  <div className="space-y-2 rounded-lg border border-border bg-muted/35 p-3.5">
                    <h3 className="text-xs font-semibold text-foreground">
                      Trạng thái sau thay đổi
                    </h3>
                    <dl className="divide-y divide-border">
                      {afterPairs.map(([k, v]) => (
                        <div
                          key={k}
                          className="grid grid-cols-1 gap-1 py-1.5 text-[13px] first:pt-0 last:pb-0 sm:grid-cols-[minmax(110px,140px)_minmax(0,1fr)] sm:gap-2.5"
                        >
                          <dt className="text-muted-foreground [overflow-wrap:anywhere]">{k}</dt>
                          <dd className="text-foreground tabular-nums [overflow-wrap:anywhere]">
                            {v}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  </div>
                ) : null}
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
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
