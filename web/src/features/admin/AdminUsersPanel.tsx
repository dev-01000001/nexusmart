"use client";

import React, { useCallback, useEffect, useState } from "react";
import { AlertCircle, CheckCircle2, Info, RefreshCw, Search, UserPlus, Users } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  Money,
  StatusBadge,
  orderStatusTone,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
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
import {
  ALL_USER_ROLES,
  ASSIGNABLE_USER_ROLES,
  CREATABLE_STAFF_ROLES,
  type CreatableStaffRole,
  canActOnTargetRole,
  roleHasCapability,
} from "@/lib/admin-rbac";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type {
  GatewayCreateStaffResponse,
  GatewayUser,
  GatewayUserActivitiesResponse,
  PaginatedResponse,
  UserRole,
} from "@/lib/gateway-types";
import { getPaymentMethodLabel } from "@/features/account/orderMeta";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { useAdminContext } from "./AdminShell";
import { useAdminUser } from "./AdminUserContext";
import {
  ADMIN_USER_ROLE_LABELS,
  formatUserActivityEventType,
  getAdminOrderStatusMeta,
  getAdminUserStatusMeta,
} from "./types";
import {
  ConfirmActionDialog,
  DetailGrid,
  DetailItem,
  FilterSelect,
  TableLoadingSkeleton,
  TablePagination,
  adminUserStatusTone,
  formatPageCountSummary,
} from "./config/adminUiHelpers";

const PAGE_SIZE = 20;
const ALL_ACT_SENTINEL = "__all_act__";

export interface AdminUsersPanelProps {
  initialQuery?: string;
}

export function AdminUsersPanel({ initialQuery = "" }: AdminUsersPanelProps) {
  const { showToast } = useAdminContext();
  const currentUser = useAdminUser();
  const currentRole = currentUser?.role ?? "";
  const currentUserId = currentUser?.id ?? "";

  const canCreateStaff =
    roleHasCapability(currentRole, "staff.create") && currentRole.toLowerCase() === "super_admin";
  const canWriteRole = roleHasCapability(currentRole, "users.role.write");
  const canWriteStatus = roleHasCapability(currentRole, "users.status.write");

  const [users, setUsers] = useState<GatewayUser[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [queryInput, setQueryInput] = useState(initialQuery);
  const [appliedQuery, setAppliedQuery] = useState(initialQuery.trim());
  const [roleFilter, setRoleFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

  // Create internal staff form (super_admin only; strictly NO password input per F14)
  const [showCreateStaff, setShowCreateStaff] = useState(false);
  const [staffFullName, setStaffFullName] = useState("");
  const [staffEmail, setStaffEmail] = useState("");
  const [staffPhone, setStaffPhone] = useState("");
  const [staffRole, setStaffRole] = useState<CreatableStaffRole>("ops");
  const [staffBusy, setStaffBusy] = useState(false);
  const [staffError, setStaffError] = useState<string | null>(null);
  const [staffSuccessMsg, setStaffSuccessMsg] = useState<string | null>(null);

  // Selected user Sheet & mutations
  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);
  const [reasonDraft, setReasonDraft] = useState("");
  const [targetRoleDraft, setTargetRoleDraft] = useState<UserRole>("buyer");
  const [actionBusy, setActionBusy] = useState(false);
  const [confirmLockTarget, setConfirmLockTarget] = useState<GatewayUser | null>(null);

  // User activity log state (F13)
  const [activities, setActivities] = useState<GatewayUserActivitiesResponse | null>(null);
  const [activitiesLoading, setActivitiesLoading] = useState(false);
  const [activitiesError, setActivitiesError] = useState<string | null>(null);
  const [activityCategory, setActivityCategory] = useState("");

  const loadUsers = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", String(page));
      qs.set("page_size", String(PAGE_SIZE));
      if (appliedQuery) qs.set("query", appliedQuery);
      if (roleFilter) qs.set("role", roleFilter);
      if (statusFilter) qs.set("status", statusFilter);
      const res = await api<PaginatedResponse<GatewayUser>>(`/api/admin/users?${qs.toString()}`);
      setUsers(res.items ?? []);
      setTotal(res.total ?? (res.items ?? []).length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách người dùng lúc này."));
    } finally {
      setLoading(false);
    }
  }, [page, appliedQuery, roleFilter, statusFilter]);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const loadUserActivities = useCallback(async (userId: string, category: string) => {
    if (!userId) return;
    setActivitiesLoading(true);
    setActivitiesError(null);
    try {
      const qs = new URLSearchParams({ page: "1", page_size: "20" });
      if (category) qs.set("category", category);
      const res = await api<GatewayUserActivitiesResponse>(
        `/api/admin/users/${encodeURIComponent(userId)}/activities?${qs.toString()}`
      );
      setActivities(res);
    } catch (err) {
      setActivities(null);
      setActivitiesError(getErrorMessage(err, "Không thể tải lịch sử hoạt động tài khoản này."));
    } finally {
      setActivitiesLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!selectedUserId) {
      setActivities(null);
      setActivitiesError(null);
      return;
    }
    void loadUserActivities(selectedUserId, activityCategory);
  }, [selectedUserId, activityCategory, loadUserActivities]);

  const selectedUser = users.find((u) => u.id === selectedUserId) ?? null;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const handleOpenUser = (u: GatewayUser) => {
    setSelectedUserId(u.id);
    setActivityCategory("");
    setReasonDraft("");
    setTargetRoleDraft((ASSIGNABLE_USER_ROLES.includes(u.role) ? u.role : "buyer") as UserRole);
  };

  const handleCreateStaff = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canCreateStaff || staffBusy) return;
    const fullName = staffFullName.trim();
    const email = staffEmail.trim().toLowerCase();
    const phone = staffPhone.trim();

    if (fullName.length < 2) {
      setStaffError("Vui lòng nhập họ tên nhân viên (tối thiểu 2 ký tự).");
      return;
    }
    if (!email.includes("@")) {
      setStaffError("Vui lòng nhập địa chỉ email hợp lệ.");
      return;
    }
    setStaffError(null);
    setStaffSuccessMsg(null);
    setStaffBusy(true);
    try {
      const res = await api<GatewayCreateStaffResponse>("/api/admin/staff", {
        method: "POST",
        json: {
          full_name: fullName,
          email,
          phone,
          role: staffRole,
        },
      });
      setStaffFullName("");
      setStaffEmail("");
      setStaffPhone("");
      setStaffSuccessMsg(
        `Đã tạo tài khoản ${ADMIN_USER_ROLE_LABELS[staffRole] ?? staffRole} (${res?.user?.email || email}) và gửi email đặt mật khẩu.`
      );
      showToast("Đã tạo tài khoản nhân viên và gửi email đặt mật khẩu.");
      await loadUsers();
    } catch (err) {
      setStaffError(getErrorMessage(err, "Không thể tạo tài khoản nhân viên lúc này."));
    } finally {
      setStaffBusy(false);
    }
  };

  const handleUpdateStatus = async (user: GatewayUser, nextStatus: "ACTIVE" | "LOCKED") => {
    if (
      !canWriteStatus ||
      !canActOnTargetRole(currentRole, user.role) ||
      user.id === currentUserId
    ) {
      return;
    }
    const reason = reasonDraft.trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do tối thiểu 4 ký tự trước khi cập nhật trạng thái tài khoản.");
      return;
    }
    setActionBusy(true);
    try {
      const res = await api<{ user: GatewayUser }>(
        `/api/admin/users/${encodeURIComponent(user.id)}/status`,
        {
          method: "PATCH",
          json: { status: nextStatus, reason },
        }
      );
      if (res.user) {
        setUsers((prev) => prev.map((item) => (item.id === user.id ? res.user : item)));
      } else {
        await loadUsers();
      }
      setReasonDraft("");
      setConfirmLockTarget(null);
      showToast(
        nextStatus === "LOCKED"
          ? `Đã khóa tài khoản ${user.email}.`
          : `Đã mở khóa tài khoản ${user.email}.`
      );
      void loadUserActivities(user.id, activityCategory);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật trạng thái tài khoản."));
    } finally {
      setActionBusy(false);
    }
  };

  const handleRequestLock = (user: GatewayUser) => {
    if (reasonDraft.trim().length < 4) {
      showToast("Vui lòng nhập lý do tối thiểu 4 ký tự trước khi khóa tài khoản.");
      return;
    }
    setConfirmLockTarget(user);
  };

  const handleUpdateRole = async (user: GatewayUser) => {
    if (
      !canWriteRole ||
      user.id === currentUserId ||
      user.role === "super_admin" ||
      targetRoleDraft === "super_admin" ||
      !canActOnTargetRole(currentRole, user.role)
    ) {
      return;
    }
    const reason = reasonDraft.trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do tối thiểu 4 ký tự trước khi đổi vai trò.");
      return;
    }
    setActionBusy(true);
    try {
      const res = await api<{ user: GatewayUser }>(
        `/api/admin/users/${encodeURIComponent(user.id)}/role`,
        {
          method: "PATCH",
          json: { role: targetRoleDraft, reason },
        }
      );
      if (res.user) {
        setUsers((prev) => prev.map((item) => (item.id === user.id ? res.user : item)));
      } else {
        await loadUsers();
      }
      setReasonDraft("");
      showToast(
        `Đã chuyển vai trò ${user.email} sang ${ADMIN_USER_ROLE_LABELS[targetRoleDraft] ?? targetRoleDraft}.`
      );
      void loadUserActivities(user.id, activityCategory);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật vai trò người dùng."));
    } finally {
      setActionBusy(false);
    }
  };

  const canModifySelectedStatus =
    Boolean(selectedUser) &&
    canWriteStatus &&
    selectedUser!.id !== currentUserId &&
    canActOnTargetRole(currentRole, selectedUser!.role);

  const canModifySelectedRole =
    Boolean(selectedUser) &&
    canWriteRole &&
    selectedUser!.id !== currentUserId &&
    selectedUser!.role !== "super_admin" &&
    canActOnTargetRole(currentRole, selectedUser!.role);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="admin-users-heading" className="text-sm font-semibold text-foreground">
          Người dùng &amp; nhân sự ({formatInt(total)})
        </h2>
        <div className="flex flex-wrap items-center gap-2">
          {canCreateStaff ? (
            <Button
              type="button"
              variant={showCreateStaff ? "outline" : "default"}
              size="sm"
              onClick={() => {
                setShowCreateStaff((v) => !v);
                setStaffError(null);
                setStaffSuccessMsg(null);
              }}
            >
              <UserPlus className="size-4" aria-hidden="true" />
              <span>{showCreateStaff ? "Đóng biểu mẫu" : "Tạo nhân viên"}</span>
            </Button>
          ) : null}
          <Button type="button" variant="outline" size="sm" onClick={() => void loadUsers()}>
            <RefreshCw className="size-3.5" aria-hidden="true" />
            <span>Làm mới</span>
          </Button>
        </div>
      </div>

      {/* Create internal staff form — strictly super_admin, zero password input (F14) */}
      {canCreateStaff && showCreateStaff ? (
        <Card>
          <CardHeader>
            <CardTitle>Tạo tài khoản nhân viên</CardTitle>
            <CardDescription>
              Hệ thống tự gửi email liên kết đặt mật khẩu tới nhân viên.
            </CardDescription>
          </CardHeader>
          <form onSubmit={(e) => void handleCreateStaff(e)} noValidate>
            <CardContent className="space-y-4">
              <FieldGroup className="gap-4">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="staff-fullname">Họ và tên</FieldLabel>
                    <Input
                      id="staff-fullname"
                      type="text"
                      value={staffFullName}
                      onChange={(e) => setStaffFullName(e.target.value)}
                      placeholder="Nguyễn Văn An"
                      required
                      className="bg-card"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="staff-email">Email công việc</FieldLabel>
                    <Input
                      id="staff-email"
                      type="email"
                      value={staffEmail}
                      onChange={(e) => setStaffEmail(e.target.value)}
                      placeholder="nhanvien@nexusmart.vn"
                      required
                      className="bg-card"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="staff-phone">Số điện thoại (tuỳ chọn)</FieldLabel>
                    <Input
                      id="staff-phone"
                      type="tel"
                      value={staffPhone}
                      onChange={(e) => setStaffPhone(e.target.value)}
                      placeholder="0912345678"
                      className="bg-card tabular-nums"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="staff-role">Vai trò nội bộ</FieldLabel>
                    <Select
                      value={staffRole}
                      onValueChange={(v) => setStaffRole(v as CreatableStaffRole)}
                    >
                      <SelectTrigger id="staff-role" className="w-full bg-card">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {CREATABLE_STAFF_ROLES.map((r) => (
                          <SelectItem key={r} value={r}>
                            {ADMIN_USER_ROLE_LABELS[r] ?? r}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                </div>

                <FieldError>{staffError}</FieldError>

                {staffSuccessMsg ? (
                  <Alert role="status">
                    <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
                    <AlertDescription>{staffSuccessMsg}</AlertDescription>
                  </Alert>
                ) : null}
              </FieldGroup>
            </CardContent>
            <CardFooter className="justify-end border-t border-border pt-3.5">
              <Button type="submit" size="sm" disabled={staffBusy}>
                {staffBusy ? "Đang tạo..." : "Tạo & gửi email đặt mật khẩu"}
              </Button>
            </CardFooter>
          </form>
        </Card>
      ) : null}

      <IndexTable
        footer={
          users.length > 0 ? (
            <TablePagination
              page={page}
              totalPages={totalPages}
              summary={formatPageCountSummary(page, totalPages, total, "tài khoản")}
              disabled={loading}
              onPageChange={(nextPage) => setPage(nextPage)}
            />
          ) : undefined
        }
      >
        <form
          className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5"
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setAppliedQuery(queryInput.trim());
          }}
        >
          <div className="relative min-w-0 flex-1 sm:max-w-xs">
            <Search
              className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              type="search"
              value={queryInput}
              onChange={(e) => setQueryInput(e.target.value)}
              placeholder="Tìm họ tên, email, SĐT..."
              aria-label="Tìm người dùng"
              className="h-9 bg-card pl-8"
            />
          </div>

          <div className="w-full sm:w-44">
            <FilterSelect
              value={roleFilter}
              onValueChange={(v) => {
                setRoleFilter(v);
                setPage(1);
              }}
              ariaLabel="Lọc vai trò"
              options={[
                { value: "", label: "Tất cả vai trò" },
                ...ALL_USER_ROLES.map((r) => ({
                  value: r,
                  label: ADMIN_USER_ROLE_LABELS[r] ?? r,
                })),
              ]}
            />
          </div>

          <div className="w-full sm:w-44">
            <FilterSelect
              value={statusFilter}
              onValueChange={(v) => {
                setStatusFilter(v);
                setPage(1);
              }}
              ariaLabel="Lọc trạng thái tài khoản"
              options={[
                { value: "", label: "Tất cả trạng thái" },
                { value: "ACTIVE", label: "Hoạt động" },
                { value: "PENDING_VERIFICATION", label: "Chờ xác thực" },
                { value: "LOCKED", label: "Đã khóa" },
              ]}
            />
          </div>

          <Button type="submit" size="sm">
            Tìm
          </Button>
        </form>

        {loading ? (
          <div className="p-4">
            <TableLoadingSkeleton rows={4} label="Đang tải danh sách người dùng..." />
          </div>
        ) : errorMsg ? (
          <div className="p-4">
            <ErrorState
              title="Không thể tải danh sách người dùng"
              description={errorMsg}
              onRetry={() => void loadUsers()}
              className="shadow-none"
            />
          </div>
        ) : users.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Users}
              title="Không tìm thấy tài khoản nào khớp bộ lọc"
              className="shadow-none"
            />
          </div>
        ) : (
          <>
            <div className="hidden md:block">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Tài khoản</TableHead>
                    <TableHead>Vai trò</TableHead>
                    <TableHead>Trạng thái</TableHead>
                    <TableHead>Ngày tạo</TableHead>
                    <TableHead className="text-right">Thao tác</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((u) => {
                    const stMeta = getAdminUserStatusMeta(u.status);
                    const isSelected = selectedUser?.id === u.id;
                    const isSelf = u.id === currentUserId;
                    return (
                      <TableRow
                        key={u.id}
                        data-state={isSelected ? "selected" : undefined}
                        className="cursor-pointer"
                        onClick={() => handleOpenUser(u)}
                      >
                        <TableCell className="whitespace-normal">
                          <button
                            type="button"
                            className="flex flex-col items-start text-left focus-visible:underline focus-visible:outline-none"
                            onClick={(e) => {
                              e.stopPropagation();
                              handleOpenUser(u);
                            }}
                          >
                            <span className="inline-flex flex-wrap items-center gap-1.5 font-medium text-foreground">
                              <span>{u.full_name || u.email}</span>
                              {isSelf ? (
                                <Badge variant="info" className="px-2 py-0 text-[11px]">
                                  Bạn
                                </Badge>
                              ) : null}
                            </span>
                            <span className="text-xs text-muted-foreground tabular-nums">
                              {u.email}
                              {u.phone ? ` · ${u.phone}` : ""}
                            </span>
                          </button>
                        </TableCell>
                        <TableCell>{ADMIN_USER_ROLE_LABELS[u.role] ?? u.role}</TableCell>
                        <TableCell>
                          <StatusBadge tone={adminUserStatusTone(u.status)}>
                            {stMeta.label}
                          </StatusBadge>
                        </TableCell>
                        <TableCell className="text-[13px] text-muted-foreground tabular-nums">
                          {u.created_at ? formatDateTimeVi(u.created_at) : EMPTY_DASH}
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={(e) => {
                              e.stopPropagation();
                              handleOpenUser(u);
                            }}
                          >
                            Chi tiết
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>

            <div className="md:hidden">
              {users.map((u) => {
                const stMeta = getAdminUserStatusMeta(u.status);
                return (
                  <IndexRowCard
                    key={u.id}
                    title={u.full_name || u.email}
                    badge={
                      <StatusBadge tone={adminUserStatusTone(u.status)}>{stMeta.label}</StatusBadge>
                    }
                    meta={
                      <>
                        <span>{u.email}</span>
                        <span>·</span>
                        <span>{ADMIN_USER_ROLE_LABELS[u.role] ?? u.role}</span>
                      </>
                    }
                    onClick={() => handleOpenUser(u)}
                  />
                );
              })}
            </div>
          </>
        )}
      </IndexTable>

      {/* User inspection, RoleRank actions & Activity Log Sheet */}
      <Sheet open={Boolean(selectedUser)} onOpenChange={(open) => !open && setSelectedUserId(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {selectedUser ? (
            <>
              <SheetHeader className="border-b border-border pb-4">
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>{selectedUser.full_name || selectedUser.email}</SheetTitle>
                  <StatusBadge tone={adminUserStatusTone(selectedUser.status)}>
                    {getAdminUserStatusMeta(selectedUser.status).label}
                  </StatusBadge>
                </div>
                <SheetDescription>
                  {selectedUser.email} ·{" "}
                  {ADMIN_USER_ROLE_LABELS[selectedUser.role] ?? selectedUser.role}
                </SheetDescription>
              </SheetHeader>

              <div className="space-y-4 px-4 pb-6">
                <DetailGrid>
                  <DetailItem label="Mã tài khoản" numeric>
                    {selectedUser.id}
                  </DetailItem>
                  <DetailItem label="Số điện thoại" numeric>
                    {selectedUser.phone || EMPTY_DASH}
                  </DetailItem>
                  <DetailItem label="Xác thực SĐT">
                    {selectedUser.phone_verified ? "Đã xác thực" : "Chưa xác thực"}
                  </DetailItem>
                  <DetailItem label="Từ chối nhận COD" numeric>
                    {formatInt(
                      activities?.cod_rejected_count ?? selectedUser.cod_rejected_count ?? 0
                    )}
                  </DetailItem>
                </DetailGrid>

                {selectedUser.status === "LOCKED" && selectedUser.lock_reason ? (
                  <Alert variant="destructive">
                    <AlertCircle className="size-4" aria-hidden="true" />
                    <AlertDescription>Lý do đang khóa: {selectedUser.lock_reason}</AlertDescription>
                  </Alert>
                ) : null}

                {canModifySelectedRole || canModifySelectedStatus ? (
                  <div className="space-y-3 rounded-lg border border-border bg-card p-4">
                    <FieldGroup className="gap-3">
                      {canModifySelectedRole ? (
                        <Field className="gap-1.5">
                          <FieldLabel htmlFor="usr-role-sel">Đổi vai trò</FieldLabel>
                          <Select
                            value={targetRoleDraft}
                            disabled={actionBusy || selectedUser.role === "super_admin"}
                            onValueChange={(v) => setTargetRoleDraft(v as UserRole)}
                          >
                            <SelectTrigger id="usr-role-sel" className="w-full bg-card">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {ASSIGNABLE_USER_ROLES.map((r) => (
                                <SelectItem key={r} value={r}>
                                  {ADMIN_USER_ROLE_LABELS[r] ?? r}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>
                      ) : null}

                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="usr-reason">
                          Lý do thao tác (tối thiểu 4 ký tự)
                        </FieldLabel>
                        <Input
                          id="usr-reason"
                          type="text"
                          value={reasonDraft}
                          onChange={(e) => setReasonDraft(e.target.value)}
                          placeholder="Nhập lý do khóa/mở hoặc đổi vai trò..."
                          className="bg-card"
                        />
                      </Field>
                    </FieldGroup>

                    <div className="flex flex-wrap items-center gap-2 pt-1">
                      {canModifySelectedRole ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={actionBusy}
                          onClick={() => void handleUpdateRole(selectedUser)}
                        >
                          Lưu vai trò mới
                        </Button>
                      ) : null}
                      {canModifySelectedStatus ? (
                        selectedUser.status === "LOCKED" ? (
                          <Button
                            type="button"
                            size="sm"
                            disabled={actionBusy}
                            onClick={() => void handleUpdateStatus(selectedUser, "ACTIVE")}
                          >
                            Mở khóa tài khoản
                          </Button>
                        ) : (
                          <Button
                            type="button"
                            variant="destructive"
                            size="sm"
                            disabled={actionBusy}
                            onClick={() => handleRequestLock(selectedUser)}
                          >
                            Khóa tài khoản
                          </Button>
                        )
                      ) : null}
                    </div>
                  </div>
                ) : (
                  <Alert>
                    <Info className="size-4" aria-hidden="true" />
                    <AlertDescription>
                      {selectedUser.id === currentUserId
                        ? "Không thể tự thay đổi vai trò hoặc khóa chính tài khoản đang đăng nhập."
                        : !canActOnTargetRole(currentRole, selectedUser.role)
                          ? "Không thể thao tác với tài khoản có vai trò ngang cấp hoặc cao hơn."
                          : "Chỉ có quyền xem hồ sơ và nhật ký hoạt động."}
                    </AlertDescription>
                  </Alert>
                )}

                <Separator />

                {/* User Activity Log & Recent Orders (F13) */}
                <div className="space-y-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h3 className="text-sm font-semibold text-foreground">Nhật ký hoạt động</h3>
                    <div className="flex flex-wrap items-center gap-2">
                      <Select
                        value={activityCategory || ALL_ACT_SENTINEL}
                        onValueChange={(v) => setActivityCategory(v === ALL_ACT_SENTINEL ? "" : v)}
                      >
                        <SelectTrigger
                          size="sm"
                          aria-label="Lọc nhóm hoạt động người dùng"
                          className="w-44 bg-card"
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value={ALL_ACT_SENTINEL}>Tất cả sự kiện</SelectItem>
                          <SelectItem value="auth">Đăng nhập &amp; mật khẩu</SelectItem>
                          <SelectItem value="profile">Hồ sơ cá nhân</SelectItem>
                          <SelectItem value="phone">Xác minh SĐT</SelectItem>
                          <SelectItem value="status">Trạng thái tài khoản</SelectItem>
                          <SelectItem value="role">Vai trò &amp; nhân sự</SelectItem>
                        </SelectContent>
                      </Select>
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        onClick={() => void loadUserActivities(selectedUser.id, activityCategory)}
                      >
                        Tải lại
                      </Button>
                    </div>
                  </div>

                  {activitiesLoading ? (
                    <p className="text-[13px] text-muted-foreground">
                      Đang tải nhật ký hoạt động...
                    </p>
                  ) : activitiesError ? (
                    <FieldError>{activitiesError}</FieldError>
                  ) : !activities || activities.items.length === 0 ? (
                    <p className="rounded-lg border border-border bg-muted/35 p-4 text-center text-[13px] text-muted-foreground">
                      Chưa ghi nhận sự kiện hoạt động nào.
                    </p>
                  ) : (
                    <>
                      <div className="hidden overflow-hidden rounded-lg border border-border sm:block">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Thời điểm</TableHead>
                              <TableHead>Sự kiện</TableHead>
                              <TableHead>IP</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {activities.items.map((act) => (
                              <TableRow key={act.id}>
                                <TableCell className="text-xs text-muted-foreground tabular-nums">
                                  {act.created_at ? formatDateTimeVi(act.created_at) : EMPTY_DASH}
                                </TableCell>
                                <TableCell className="whitespace-normal">
                                  {formatUserActivityEventType(act.event_type)}
                                </TableCell>
                                <TableCell className="text-xs text-muted-foreground tabular-nums">
                                  {act.ip_address || EMPTY_DASH}
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      </div>

                      <div className="divide-y divide-border rounded-lg border border-border sm:hidden">
                        {activities.items.map((act) => (
                          <div key={act.id} className="space-y-0.5 p-3">
                            <div className="flex items-start justify-between gap-2 text-[13px]">
                              <span className="font-medium text-foreground">
                                {formatUserActivityEventType(act.event_type)}
                              </span>
                              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                                {act.created_at
                                  ? formatShortTimeDateVi(act.created_at)
                                  : EMPTY_DASH}
                              </span>
                            </div>
                            <div className="text-xs text-muted-foreground tabular-nums">
                              IP {act.ip_address || EMPTY_DASH}
                            </div>
                          </div>
                        ))}
                      </div>
                    </>
                  )}

                  {activities &&
                  Array.isArray(activities.recent_orders) &&
                  activities.recent_orders.length > 0 ? (
                    <div className="space-y-2.5 pt-2">
                      <h4 className="text-sm font-semibold text-foreground">
                        Đơn hàng gần đây ({formatInt(activities.recent_orders.length)})
                      </h4>
                      <div className="hidden overflow-hidden rounded-lg border border-border sm:block">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Mã đơn</TableHead>
                              <TableHead>Thanh toán</TableHead>
                              <TableHead className="text-right">Tổng tiền</TableHead>
                              <TableHead>Trạng thái</TableHead>
                              <TableHead>Ngày đặt</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {activities.recent_orders.map((ord) => {
                              const ordMeta = getAdminOrderStatusMeta(ord.status);
                              return (
                                <TableRow key={ord.id}>
                                  <TableCell className="font-medium tabular-nums">
                                    {ord.order_code}
                                  </TableCell>
                                  <TableCell>{getPaymentMethodLabel(ord.payment_method)}</TableCell>
                                  <TableCell className="text-right">
                                    <Money vnd={ord.total_payable_vnd} size="sm" />
                                  </TableCell>
                                  <TableCell>
                                    <StatusBadge tone={orderStatusTone(ord.status)}>
                                      {ordMeta.label}
                                    </StatusBadge>
                                  </TableCell>
                                  <TableCell className="text-xs text-muted-foreground tabular-nums">
                                    {ord.created_at ? formatDateTimeVi(ord.created_at) : EMPTY_DASH}
                                  </TableCell>
                                </TableRow>
                              );
                            })}
                          </TableBody>
                        </Table>
                      </div>

                      <div className="divide-y divide-border rounded-lg border border-border sm:hidden">
                        {activities.recent_orders.map((ord) => {
                          const ordMeta = getAdminOrderStatusMeta(ord.status);
                          return (
                            <div key={ord.id} className="space-y-1 p-3">
                              <div className="flex items-center justify-between gap-2 text-[13px]">
                                <span className="font-semibold text-foreground tabular-nums">
                                  {ord.order_code}
                                </span>
                                <Money vnd={ord.total_payable_vnd} size="sm" />
                              </div>
                              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                                <span>
                                  {getPaymentMethodLabel(ord.payment_method)}
                                  {ord.created_at
                                    ? ` · ${formatShortTimeDateVi(ord.created_at)}`
                                    : ""}
                                </span>
                                <StatusBadge tone={orderStatusTone(ord.status)}>
                                  {ordMeta.label}
                                </StatusBadge>
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  ) : null}
                </div>
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      <ConfirmActionDialog
        open={Boolean(confirmLockTarget)}
        title="Xác nhận khóa tài khoản"
        description={
          confirmLockTarget
            ? `Khóa tài khoản ${confirmLockTarget.email} và thu hồi toàn bộ phiên đăng nhập hiện tại?`
            : ""
        }
        confirmLabel="Xác nhận khóa"
        confirmVariant="destructive"
        busy={actionBusy}
        onCancel={() => setConfirmLockTarget(null)}
        onConfirm={() => {
          if (confirmLockTarget) void handleUpdateStatus(confirmLockTarget, "LOCKED");
        }}
      />
    </div>
  );
}
