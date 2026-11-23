"use client";

import React, { useCallback, useEffect, useState } from "react";
import type { GatewayUser, PaginatedResponse, UserRole } from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { ADMIN_USER_ROLE_LABELS, getAdminUserStatusMeta } from "./types";
import { StatusPill } from "./StatusPill";
import shared from "./adminShared.module.css";

const PAGE_SIZE = 20;

export function AdminUsersPanel() {
  const { showToast } = useMockStore();
  const [users, setUsers] = useState<GatewayUser[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [queryInput, setQueryInput] = useState("");
  const [appliedQuery, setAppliedQuery] = useState("");
  const [roleFilter, setRoleFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);
  const [reasonDraft, setReasonDraft] = useState("");
  const [targetRoleDraft, setTargetRoleDraft] = useState<UserRole>("buyer");
  const [actionBusy, setActionBusy] = useState(false);

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
      const res = await api<PaginatedResponse<GatewayUser>>(`admin/users?${qs.toString()}`);
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

  const selectedUser = users.find((u) => u.id === selectedUserId) ?? null;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const handleSelectUser = (u: GatewayUser) => {
    if (selectedUserId === u.id) {
      setSelectedUserId(null);
      return;
    }
    setSelectedUserId(u.id);
    setReasonDraft("");
    setTargetRoleDraft(
      u.role === "super_admin" || u.role === "admin" || u.role === "seller" || u.role === "buyer"
        ? u.role
        : "buyer"
    );
  };

  const handleUpdateStatus = async (user: GatewayUser, nextStatus: "ACTIVE" | "LOCKED") => {
    const reason = reasonDraft.trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do tối thiểu 4 ký tự trước khi cập nhật trạng thái tài khoản.");
      return;
    }
    setActionBusy(true);
    try {
      const res = await api<{ user: GatewayUser }>(`admin/users/${user.id}/status`, {
        method: "PATCH",
        json: { status: nextStatus, reason },
      });
      if (res.user) {
        setUsers((prev) => prev.map((item) => (item.id === user.id ? res.user : item)));
      } else {
        await loadUsers();
      }
      setReasonDraft("");
      showToast(
        nextStatus === "LOCKED"
          ? `Đã khóa tài khoản ${user.email}.`
          : `Đã mở khóa tài khoản ${user.email}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật trạng thái tài khoản."));
    } finally {
      setActionBusy(false);
    }
  };

  const handleUpdateRole = async (user: GatewayUser) => {
    const reason = reasonDraft.trim();
    if (reason.length < 4) {
      showToast("Vui lòng nhập lý do tối thiểu 4 ký tự trước khi đổi vai trò.");
      return;
    }
    setActionBusy(true);
    try {
      const res = await api<{ user: GatewayUser }>(`admin/users/${user.id}/role`, {
        method: "PATCH",
        json: { role: targetRoleDraft, reason },
      });
      if (res.user) {
        setUsers((prev) => prev.map((item) => (item.id === user.id ? res.user : item)));
      } else {
        await loadUsers();
      }
      setReasonDraft("");
      showToast(
        `Đã chuyển vai trò tài khoản ${user.email} sang ${ADMIN_USER_ROLE_LABELS[targetRoleDraft] ?? targetRoleDraft}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật vai trò người dùng."));
    } finally {
      setActionBusy(false);
    }
  };

  return (
    <div className={shared.sectionStack}>
      <section className={shared.panel} aria-labelledby="admin-users-heading">
        <div className={shared.panelHeader}>
          <h2 id="admin-users-heading" className={shared.panelTitle}>
            Quản trị tài khoản người dùng ({formatInt(total)})
          </h2>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadUsers()}>
            Làm mới
          </Button>
        </div>

        <form
          className={shared.toolbar}
          style={{ marginTop: 0 }}
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
                placeholder="Tìm họ tên, email, số điện thoại..."
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
              <option value="buyer">Người mua</option>
              <option value="seller">Người bán</option>
              <option value="admin">Quản trị viên</option>
              <option value="super_admin">Quản trị cấp cao</option>
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
          <StateView
            kind="loading"
            surface="app"
            title="Đang tải danh sách người dùng..."
            rows={4}
          />
        ) : errorMsg ? (
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải danh sách người dùng"
            message={errorMsg}
            actionLabel="Thử lại"
            onAction={() => void loadUsers()}
          />
        ) : users.length === 0 ? (
          <p className={shared.emptyRow}>Không tìm thấy tài khoản nào khớp bộ lọc.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
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
                    return (
                      <tr
                        key={u.id}
                        className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                        onClick={() => handleSelectUser(u)}
                      >
                        <td>
                          <div className={shared.cellPrimary}>{u.full_name || u.email}</div>
                          <div className={shared.cellSub}>
                            {u.email}
                            {u.phone ? ` · ${u.phone}` : ""}
                          </div>
                        </td>
                        <td>{ADMIN_USER_ROLE_LABELS[u.role] ?? u.role}</td>
                        <td>
                          <StatusPill label={stMeta.label} tone={stMeta.tone} />
                        </td>
                        <td>{u.created_at ? formatDateTimeVi(u.created_at) : "—"}</td>
                        <td className={shared.colAction}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              handleSelectUser(u);
                            }}
                          >
                            {isSelected ? "Đóng" : "Quản trị"}
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
                const isSelected = selectedUser?.id === u.id;
                return (
                  <article key={u.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{u.full_name || u.email}</span>
                      <StatusPill label={stMeta.label} tone={stMeta.tone} />
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {u.email}
                        {u.phone ? ` · ${u.phone}` : ""}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {ADMIN_USER_ROLE_LABELS[u.role] ?? u.role} ·{" "}
                        {u.created_at ? formatDateTimeVi(u.created_at) : "—"}
                      </span>
                      <button
                        type="button"
                        className={shared.rowActionBtn}
                        onClick={() => handleSelectUser(u)}
                      >
                        {isSelected ? "Đóng" : "Quản trị"}
                      </button>
                    </div>
                  </article>
                );
              })}
            </div>

            {selectedUser && (
              <div className={shared.detailWell} style={{ marginTop: 16 }}>
                <div className={shared.detailHeader}>
                  <h3 className={shared.detailHeading}>
                    Cập nhật tài khoản: {selectedUser.full_name} ({selectedUser.email})
                  </h3>
                  <StatusPill
                    label={getAdminUserStatusMeta(selectedUser.status).label}
                    tone={getAdminUserStatusMeta(selectedUser.status).tone}
                  />
                </div>

                {selectedUser.status === "LOCKED" && selectedUser.lock_reason && (
                  <p className={shared.validationError}>
                    Lý do đang khóa tài khoản: {selectedUser.lock_reason}
                  </p>
                )}

                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <label htmlFor="usr-role-sel" className={shared.fieldLabel}>
                      Đổi vai trò
                    </label>
                    <select
                      id="usr-role-sel"
                      className={shared.toolbarSelect}
                      style={{ height: 38 }}
                      value={targetRoleDraft}
                      onChange={(e) => setTargetRoleDraft(e.target.value as UserRole)}
                    >
                      <option value="buyer">Người mua (buyer)</option>
                      <option value="seller">Người bán (seller)</option>
                      <option value="admin">Quản trị viên (admin)</option>
                      <option value="super_admin">Quản trị cấp cao (super_admin)</option>
                    </select>
                  </div>
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <label htmlFor="usr-reason" className={shared.fieldLabel}>
                      Lý do thao tác (ghi vào Nhật ký kiểm toán)
                    </label>
                    <input
                      id="usr-reason"
                      type="text"
                      className={shared.textInput}
                      value={reasonDraft}
                      onChange={(e) => setReasonDraft(e.target.value)}
                      placeholder="Nhập lý do khóa/mở tài khoản hoặc thay đổi vai trò..."
                    />
                  </div>
                </div>

                <div className={shared.detailActionBar}>
                  <span className={shared.savedStamp}>Mã tài khoản: {selectedUser.id}</span>
                  <div className={shared.detailButtons}>
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      disabled={actionBusy}
                      onClick={() => void handleUpdateRole(selectedUser)}
                    >
                      Lưu vai trò mới
                    </Button>
                    {selectedUser.status === "LOCKED" ? (
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
                        onClick={() => void handleUpdateStatus(selectedUser, "LOCKED")}
                      >
                        Khóa tài khoản
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            )}

            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "flex-end",
                gap: 8,
                marginTop: 14,
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
    </div>
  );
}
