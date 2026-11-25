"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Button, IconSearch, StateView } from "@/components";
import {
  ALL_USER_ROLES,
  ASSIGNABLE_USER_ROLES,
  CREATABLE_STAFF_ROLES,
  type CreatableStaffRole,
  canActOnTargetRole,
  roleHasCapability,
} from "@/lib/admin-rbac";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type {
  GatewayCreateStaffResponse,
  GatewayUser,
  GatewayUserActivitiesResponse,
  PaginatedResponse,
  UserRole,
} from "@/lib/gateway-types";
import { getPaymentMethodLabel } from "@/features/account/orderMeta";
import { EMPTY_DASH } from "./adminMeta";
import { useAdminContext } from "./AdminShell";
import { AdminConfirmDialog, AdminDrawer } from "./AdminUi";
import { useAdminUser } from "./AdminUserContext";
import { StatusPill } from "./StatusPill";
import {
  ADMIN_USER_ROLE_LABELS,
  formatUserActivityEventType,
  getAdminOrderStatusMeta,
  getAdminUserStatusMeta,
} from "./types";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/cau-hinh/page.module.css";

const PAGE_SIZE = 20;

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

  // Selected user drawer & mutations
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
    <div className={shared.sectionStack}>
      <section className={shared.panel} aria-labelledby="admin-users-heading">
        <div className={shared.panelHeader}>
          <h2 id="admin-users-heading" className={shared.panelTitle}>
            Người dùng &amp; nhân sự ({formatInt(total)})
          </h2>
          <div className={shared.detailButtons}>
            {canCreateStaff && (
              <Button
                variant={showCreateStaff ? "outline" : "primary"}
                size="sm"
                shape="app"
                onClick={() => {
                  setShowCreateStaff((v) => !v);
                  setStaffError(null);
                  setStaffSuccessMsg(null);
                }}
              >
                {showCreateStaff ? "Đóng biểu mẫu" : "Tạo nhân viên"}
              </Button>
            )}
            <Button variant="outline" size="sm" shape="app" onClick={() => void loadUsers()}>
              Làm mới
            </Button>
          </div>
        </div>

        {/* Create internal staff form — strictly super_admin, zero password input (F14) */}
        {canCreateStaff && showCreateStaff && (
          <form
            className={shared.detailWell}
            style={{ marginBottom: 16 }}
            onSubmit={(e) => void handleCreateStaff(e)}
            noValidate
          >
            <div className={shared.detailHeader}>
              <div>
                <h3 className={shared.detailHeading}>Tạo tài khoản nhân viên</h3>
                <p className={shared.cellSub}>
                  Hệ thống tự gửi email liên kết đặt mật khẩu tới nhân viên.
                </p>
              </div>
            </div>

            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <label htmlFor="staff-fullname" className={shared.fieldLabel}>
                  Họ và tên
                </label>
                <input
                  id="staff-fullname"
                  type="text"
                  className={shared.textInput}
                  value={staffFullName}
                  onChange={(e) => setStaffFullName(e.target.value)}
                  placeholder="Nguyễn Văn An"
                  required
                />
              </div>
              <div className={shared.detailField}>
                <label htmlFor="staff-email" className={shared.fieldLabel}>
                  Email công việc
                </label>
                <input
                  id="staff-email"
                  type="email"
                  className={shared.textInput}
                  value={staffEmail}
                  onChange={(e) => setStaffEmail(e.target.value)}
                  placeholder="nhanvien@nexusmart.vn"
                  required
                />
              </div>
              <div className={shared.detailField}>
                <label htmlFor="staff-phone" className={shared.fieldLabel}>
                  Số điện thoại (tuỳ chọn)
                </label>
                <input
                  id="staff-phone"
                  type="tel"
                  className={shared.textInput}
                  value={staffPhone}
                  onChange={(e) => setStaffPhone(e.target.value)}
                  placeholder="0912345678"
                />
              </div>
              <div className={shared.detailField}>
                <label htmlFor="staff-role" className={shared.fieldLabel}>
                  Vai trò nội bộ
                </label>
                <select
                  id="staff-role"
                  className={shared.formSelect}
                  value={staffRole}
                  onChange={(e) => setStaffRole(e.target.value as CreatableStaffRole)}
                >
                  {CREATABLE_STAFF_ROLES.map((r) => (
                    <option key={r} value={r}>
                      {ADMIN_USER_ROLE_LABELS[r] ?? r}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            {staffError && (
              <p className={shared.validationError} role="alert">
                {staffError}
              </p>
            )}
            {staffSuccessMsg && (
              <p className={shared.savedStamp} role="status">
                {staffSuccessMsg}
              </p>
            )}

            <div className={shared.detailButtons}>
              <Button type="submit" variant="primary" size="sm" shape="app" loading={staffBusy}>
                Tạo &amp; gửi email đặt mật khẩu
              </Button>
            </div>
          </form>
        )}

        <form
          className={shared.toolbar}
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setAppliedQuery(queryInput.trim());
          }}
        >
          <div className={shared.toolbarLeft}>
            <div className={shared.searchWrap}>
              <span className={shared.searchIcon}>
                <IconSearch size={14} />
              </span>
              <input
                type="search"
                className={shared.toolbarSearch}
                value={queryInput}
                onChange={(e) => setQueryInput(e.target.value)}
                placeholder="Tìm họ tên, email, SĐT..."
                aria-label="Tìm người dùng"
              />
            </div>
            <select
              className={shared.toolbarSelect}
              value={roleFilter}
              onChange={(e) => {
                setRoleFilter(e.target.value);
                setPage(1);
              }}
              aria-label="Lọc vai trò"
            >
              <option value="">Tất cả vai trò</option>
              {ALL_USER_ROLES.map((r) => (
                <option key={r} value={r}>
                  {ADMIN_USER_ROLE_LABELS[r] ?? r}
                </option>
              ))}
            </select>
            <select
              className={shared.toolbarSelect}
              value={statusFilter}
              onChange={(e) => {
                setStatusFilter(e.target.value);
                setPage(1);
              }}
              aria-label="Lọc trạng thái tài khoản"
            >
              <option value="">Tất cả trạng thái</option>
              <option value="ACTIVE">Hoạt động</option>
              <option value="PENDING_VERIFICATION">Chờ xác thực</option>
              <option value="LOCKED">Đã khóa</option>
            </select>
            <Button type="submit" variant="primary" size="sm" shape="app">
              Tìm
            </Button>
          </div>
          <span className={shared.resultCount}>
            Trang {formatInt(page)} / {formatInt(totalPages)}
          </span>
        </form>

        {loading ? (
          <div className={shared.stateWrap}>
            <StateView
              kind="loading"
              surface="app"
              title="Đang tải danh sách người dùng..."
              rows={4}
            />
          </div>
        ) : errorMsg ? (
          <div className={shared.stateWrap}>
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải danh sách người dùng"
              message={errorMsg}
              actionLabel="Thử lại"
              onAction={() => void loadUsers()}
            />
          </div>
        ) : users.length === 0 ? (
          <p className={shared.emptyRow}>Không tìm thấy tài khoản nào khớp bộ lọc.</p>
        ) : (
          <>
            <div className={shared.tableWrap} style={{ marginTop: 14 }}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Tài khoản</th>
                    <th>Vai trò</th>
                    <th>Trạng thái</th>
                    <th>Ngày tạo</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => {
                    const stMeta = getAdminUserStatusMeta(u.status);
                    const isSelected = selectedUser?.id === u.id;
                    const isSelf = u.id === currentUserId;
                    return (
                      <tr
                        key={u.id}
                        className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                        onClick={() => handleOpenUser(u)}
                      >
                        <td>
                          <button
                            type="button"
                            className={shared.rowTriggerBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              handleOpenUser(u);
                            }}
                          >
                            <span className={shared.cellPrimary}>
                              {u.full_name || u.email}
                              {isSelf && <span className={styles.selfTag}>Bạn</span>}
                            </span>
                            <span className={shared.cellSub}>
                              {u.email}
                              {u.phone ? ` · ${u.phone}` : ""}
                            </span>
                          </button>
                        </td>
                        <td>{ADMIN_USER_ROLE_LABELS[u.role] ?? u.role}</td>
                        <td>
                          <StatusPill label={stMeta.label} tone={stMeta.tone} />
                        </td>
                        <td>{u.created_at ? formatDateTimeVi(u.created_at) : EMPTY_DASH}</td>
                        <td className={shared.colAction}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              handleOpenUser(u);
                            }}
                          >
                            Chi tiết
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {users.map((u) => {
                const stMeta = getAdminUserStatusMeta(u.status);
                return (
                  <article key={u.id} className={shared.mobileItem}>
                    <button
                      type="button"
                      className={shared.mobileItemBtn}
                      onClick={() => handleOpenUser(u)}
                    >
                      <span className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{u.full_name || u.email}</span>
                        <StatusPill label={stMeta.label} tone={stMeta.tone} />
                      </span>
                      <span className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {u.email} · {ADMIN_USER_ROLE_LABELS[u.role] ?? u.role}
                        </span>
                      </span>
                    </button>
                  </article>
                );
              })}
            </div>

            <div className={styles.paginationRow}>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                Trang trước
              </Button>
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
      </section>

      {/* User inspection, RoleRank actions & Activity Log Drawer */}
      <AdminDrawer
        open={Boolean(selectedUser)}
        onClose={() => setSelectedUserId(null)}
        title={selectedUser?.full_name || selectedUser?.email || ""}
        subtitle={
          selectedUser
            ? `${selectedUser.email} · ${ADMIN_USER_ROLE_LABELS[selectedUser.role] ?? selectedUser.role}`
            : undefined
        }
        headerBadge={
          selectedUser ? (
            <StatusPill
              label={getAdminUserStatusMeta(selectedUser.status).label}
              tone={getAdminUserStatusMeta(selectedUser.status).tone}
            />
          ) : undefined
        }
        width="lg"
      >
        {selectedUser && (
          <>
            <div className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Mã tài khoản</span>
                  <span className={`${shared.detailValPlain} num`}>{selectedUser.id}</span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Số điện thoại</span>
                  <span className={`${shared.detailValPlain} num`}>
                    {selectedUser.phone || EMPTY_DASH}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Xác thực SĐT</span>
                  <span className={shared.detailValPlain}>
                    {selectedUser.phone_verified ? "Đã xác thực" : "Chưa xác thực"}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Từ chối nhận COD</span>
                  <span className={`${shared.detailVal} num`}>
                    {formatInt(
                      activities?.cod_rejected_count ?? selectedUser.cod_rejected_count ?? 0
                    )}
                  </span>
                </div>
              </div>

              {selectedUser.status === "LOCKED" && selectedUser.lock_reason && (
                <p className={shared.validationError}>
                  Lý do đang khóa: {selectedUser.lock_reason}
                </p>
              )}

              {canModifySelectedRole || canModifySelectedStatus ? (
                <div className={shared.detailActionBar}>
                  <div className={shared.detailGrid}>
                    {canModifySelectedRole && (
                      <div className={shared.detailField}>
                        <label htmlFor="usr-role-sel" className={shared.fieldLabel}>
                          Đổi vai trò
                        </label>
                        <select
                          id="usr-role-sel"
                          className={shared.formSelect}
                          value={targetRoleDraft}
                          disabled={actionBusy || selectedUser.role === "super_admin"}
                          onChange={(e) => setTargetRoleDraft(e.target.value as UserRole)}
                        >
                          {ASSIGNABLE_USER_ROLES.map((r) => (
                            <option key={r} value={r}>
                              {ADMIN_USER_ROLE_LABELS[r] ?? r}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}
                    <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                      <label htmlFor="usr-reason" className={shared.fieldLabel}>
                        Lý do thao tác (tối thiểu 4 ký tự)
                      </label>
                      <input
                        id="usr-reason"
                        type="text"
                        className={shared.textInput}
                        value={reasonDraft}
                        onChange={(e) => setReasonDraft(e.target.value)}
                        placeholder="Nhập lý do khóa/mở hoặc đổi vai trò..."
                      />
                    </div>
                  </div>

                  <div className={shared.detailButtons}>
                    {canModifySelectedRole && (
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={actionBusy}
                        onClick={() => void handleUpdateRole(selectedUser)}
                      >
                        Lưu vai trò mới
                      </Button>
                    )}
                    {canModifySelectedStatus &&
                      (selectedUser.status === "LOCKED" ? (
                        <Button
                          variant="primary"
                          size="sm"
                          shape="app"
                          disabled={actionBusy}
                          onClick={() => void handleUpdateStatus(selectedUser, "ACTIVE")}
                        >
                          Mở khóa tài khoản
                        </Button>
                      ) : (
                        <Button
                          variant="danger"
                          size="sm"
                          shape="app"
                          disabled={actionBusy}
                          onClick={() => handleRequestLock(selectedUser)}
                        >
                          Khóa tài khoản
                        </Button>
                      ))}
                  </div>
                </div>
              ) : (
                <p className={shared.savedStamp}>
                  {selectedUser.id === currentUserId
                    ? "Không thể tự thay đổi vai trò hoặc khóa chính tài khoản đang đăng nhập."
                    : !canActOnTargetRole(currentRole, selectedUser.role)
                      ? "Không thể thao tác với tài khoản có vai trò ngang cấp hoặc cao hơn."
                      : "Chỉ có quyền xem hồ sơ và nhật ký hoạt động."}
                </p>
              )}
            </div>

            {/* User Activity Log & Recent Orders (F13) */}
            <div className={shared.detailWellCard}>
              <div className={shared.detailHeader}>
                <h3 className={shared.detailHeading}>Nhật ký hoạt động</h3>
                <div className={shared.detailHeaderMeta}>
                  <select
                    className={shared.toolbarSelect}
                    value={activityCategory}
                    onChange={(e) => setActivityCategory(e.target.value)}
                    aria-label="Lọc nhóm hoạt động người dùng"
                  >
                    <option value="">Tất cả sự kiện</option>
                    <option value="auth">Đăng nhập &amp; mật khẩu</option>
                    <option value="profile">Hồ sơ cá nhân</option>
                    <option value="phone">Xác minh SĐT</option>
                    <option value="status">Trạng thái tài khoản</option>
                    <option value="role">Vai trò &amp; nhân sự</option>
                  </select>
                  <button
                    type="button"
                    className={shared.rowActionBtn}
                    onClick={() => void loadUserActivities(selectedUser.id, activityCategory)}
                  >
                    Tải lại
                  </button>
                </div>
              </div>

              {activitiesLoading ? (
                <p className={shared.savedStamp}>Đang tải nhật ký hoạt động...</p>
              ) : activitiesError ? (
                <p className={shared.validationError} role="alert">
                  {activitiesError}
                </p>
              ) : !activities || activities.items.length === 0 ? (
                <p className={shared.savedStamp}>Chưa ghi nhận sự kiện hoạt động nào.</p>
              ) : (
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Thời điểm</th>
                        <th>Sự kiện</th>
                        <th>IP</th>
                      </tr>
                    </thead>
                    <tbody>
                      {activities.items.map((act) => (
                        <tr key={act.id}>
                          <td className="num">
                            {act.created_at ? formatDateTimeVi(act.created_at) : EMPTY_DASH}
                          </td>
                          <td>{formatUserActivityEventType(act.event_type)}</td>
                          <td className="num">{act.ip_address || EMPTY_DASH}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              {activities &&
                Array.isArray(activities.recent_orders) &&
                activities.recent_orders.length > 0 && (
                  <div className={shared.sectionStack}>
                    <h4 className={shared.detailHeading}>
                      Đơn hàng gần đây ({formatInt(activities.recent_orders.length)})
                    </h4>
                    <div className={shared.tableWrap}>
                      <table className={shared.dataTable}>
                        <thead>
                          <tr>
                            <th>Mã đơn</th>
                            <th>Thanh toán</th>
                            <th className={shared.colNum}>Tổng tiền</th>
                            <th>Trạng thái</th>
                            <th>Ngày đặt</th>
                          </tr>
                        </thead>
                        <tbody>
                          {activities.recent_orders.map((ord) => {
                            const ordMeta = getAdminOrderStatusMeta(ord.status);
                            return (
                              <tr key={ord.id}>
                                <td className="num">{ord.order_code}</td>
                                <td>{getPaymentMethodLabel(ord.payment_method)}</td>
                                <td className={shared.colNum}>
                                  {formatVnd(ord.total_payable_vnd)}
                                </td>
                                <td>
                                  <StatusPill label={ordMeta.label} tone={ordMeta.tone} />
                                </td>
                                <td className="num">
                                  {ord.created_at ? formatDateTimeVi(ord.created_at) : EMPTY_DASH}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}
            </div>
          </>
        )}
      </AdminDrawer>

      <AdminConfirmDialog
        open={Boolean(confirmLockTarget)}
        title="Xác nhận khóa tài khoản"
        description={
          confirmLockTarget
            ? `Khóa tài khoản ${confirmLockTarget.email} và thu hồi toàn bộ phiên đăng nhập hiện tại?`
            : ""
        }
        confirmLabel="Xác nhận khóa"
        busy={actionBusy}
        onCancel={() => setConfirmLockTarget(null)}
        onConfirm={() => {
          if (confirmLockTarget) void handleUpdateStatus(confirmLockTarget, "LOCKED");
        }}
      />
    </div>
  );
}
