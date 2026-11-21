"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { EMPTY_DASH } from "./adminMeta";
import { getActorRoleLabelVi } from "./adminApiTypes";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { StatusPill } from "./StatusPill";
import {
  ADMIN_USER_ROLES,
  canModifyUserAccount,
  formatCommissionBps,
  getUserStatusMeta,
  getVoucherScopeMeta,
  slugifyCategoryName,
  validateAdminVoucherInput,
  validateCategoryInput,
  type AdminCategory,
  type AdminUser,
  type AdminUserRole,
  type AdminVoucher,
  type AdminVoucherDiscountType,
  type AdminVoucherScope,
} from "./adminComplaintsTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/cau-hinh/page.module.css";

type ConfigTab = "categories" | "vouchers" | "users";

export interface AdminConfigClientProps {
  initialCategories: AdminCategory[];
  categoriesError: string | null;
  initialVouchers: AdminVoucher[];
  vouchersError: string | null;
  initialUsers: AdminUser[];
  usersTotal: number;
  usersError: string | null;
}

function defaultVoucherWindow(): { startLocal: string; endLocal: string } {
  const vnNow = new Date(Date.now() + 7 * 3600_000);
  const nextWeek = new Date(vnNow.getTime() + 7 * 24 * 3600_000);
  return {
    startLocal: `${vnNow.toISOString().slice(0, 10)}T00:00`,
    endLocal: `${nextWeek.toISOString().slice(0, 10)}T23:59`,
  };
}

function localVnToIso(value: string): string {
  if (!value) return "";
  const normalized = value.length === 16 ? `${value}:00+07:00` : `${value}+07:00`;
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
}

function ConfigWorkspace({
  initialCategories,
  categoriesError,
  initialVouchers,
  vouchersError,
  initialUsers,
  usersTotal,
  usersError,
  initialQuery,
}: AdminConfigClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { user: currentUser, showToast } = useAdminContext();

  const [activeTab, setActiveTab] = useState<ConfigTab>("categories");

  // --- Tab 1: Categories & Commission Rates ---
  const [categories, setCategories] = useState<AdminCategory[]>(initialCategories);
  const [editingCatId, setEditingCatId] = useState<string | null>(null);
  const [catName, setCatName] = useState("");
  const [catSlug, setCatSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [catParentId, setCatParentId] = useState("");
  const [catCommissionBps, setCatCommissionBps] = useState("500");
  const [catSortOrder, setCatSortOrder] = useState("0");
  const [catFormError, setCatFormError] = useState<string | null>(null);
  const [catBusy, setCatBusy] = useState(false);
  const [confirmDeleteCatId, setConfirmDeleteCatId] = useState<string | null>(null);
  const [deletingCatId, setDeletingCatId] = useState<string | null>(null);

  // --- Tab 2: Platform & Freeship Vouchers ---
  const [vouchers, setVouchers] = useState<AdminVoucher[]>(initialVouchers);
  const [voucherScopeFilter, setVoucherScopeFilter] = useState<"all" | AdminVoucherScope>("all");
  const [voucherQuery, setVoucherQuery] = useState("");
  const [vCode, setVCode] = useState("");
  const [vName, setVName] = useState("");
  const [vScope, setVScope] = useState<AdminVoucherScope>("PLATFORM");
  const [vDiscountType, setVDiscountType] = useState<AdminVoucherDiscountType>("FIXED");
  const [vValueVnd, setVValueVnd] = useState("30000");
  const [vDiscountPercent, setVDiscountPercent] = useState("10");
  const [vMaxDiscountVnd, setVMaxDiscountVnd] = useState("50000");
  const [vMinOrderVnd, setVMinOrderVnd] = useState("150000");
  const [vQuota, setVQuota] = useState("500");
  const [vPerUserLimit, setVPerUserLimit] = useState("1");
  const [vBudgetVnd, setVBudgetVnd] = useState("15000000");
  const [vStartsLocal, setVStartsLocal] = useState(() => defaultVoucherWindow().startLocal);
  const [vEndsLocal, setVEndsLocal] = useState(() => defaultVoucherWindow().endLocal);
  const [vFormError, setVFormError] = useState<string | null>(null);
  const [vBusy, setVBusy] = useState(false);

  // --- Tab 3: Users & RBAC ---
  const [users, setUsers] = useState<AdminUser[]>(initialUsers);
  const [totalUsersCount, setTotalUsersCount] = useState<number>(usersTotal);
  const [userQuery, setUserQuery] = useState(initialQuery);
  const [userRoleFilter, setUserRoleFilter] = useState<string>("all");
  const [userStatusFilter, setUserStatusFilter] = useState<string>("all");
  const [userActionReason, setUserActionReason] = useState("");
  const [roleDrafts, setRoleDrafts] = useState<Record<string, AdminUserRole>>({});
  const [busyUserId, setBusyUserId] = useState<string | null>(null);
  const [reloadingUsers, setReloadingUsers] = useState(false);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) map.set(c.id, c.name);
    return map;
  }, [categories]);

  const sortedCategories = useMemo(
    () =>
      [...categories].sort((a, b) => {
        if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order;
        return a.name.localeCompare(b.name, "vi");
      }),
    [categories]
  );

  const filteredVouchers = useMemo(() => {
    const q = voucherQuery.trim().toLowerCase();
    return vouchers.filter((v) => {
      if (voucherScopeFilter !== "all" && v.scope.toUpperCase() !== voucherScopeFilter) {
        return false;
      }
      if (!q) return true;
      return v.code.toLowerCase().includes(q) || v.name.toLowerCase().includes(q);
    });
  }, [vouchers, voucherScopeFilter, voucherQuery]);

  const filteredUsers = useMemo(() => {
    const q = userQuery.trim().toLowerCase();
    return users.filter((u) => {
      if (userRoleFilter !== "all" && u.role.toLowerCase() !== userRoleFilter) return false;
      if (userStatusFilter !== "all" && u.status.toUpperCase() !== userStatusFilter) return false;
      if (!q) return true;
      return (
        u.full_name.toLowerCase().includes(q) ||
        u.email.toLowerCase().includes(q) ||
        u.phone.toLowerCase().includes(q) ||
        u.id.toLowerCase().includes(q)
      );
    });
  }, [users, userQuery, userRoleFilter, userStatusFilter]);

  const resetCategoryForm = () => {
    setEditingCatId(null);
    setCatName("");
    setCatSlug("");
    setSlugTouched(false);
    setCatParentId("");
    setCatCommissionBps("500");
    setCatSortOrder("0");
    setCatFormError(null);
  };

  const handleStartEditCategory = (cat: AdminCategory) => {
    setEditingCatId(cat.id);
    setCatName(cat.name);
    setCatSlug(cat.slug);
    setSlugTouched(true);
    setCatParentId(cat.parent_id || "");
    setCatCommissionBps(String(cat.commission_rate_bps));
    setCatSortOrder(String(cat.sort_order));
    setCatFormError(null);
    setConfirmDeleteCatId(null);
  };

  const handleSaveCategory = async (e: React.FormEvent) => {
    e.preventDefault();
    if (catBusy) return;

    const parsedBps = catCommissionBps.trim() === "" ? Number.NaN : Number(catCommissionBps.trim());
    const parsedSort = catSortOrder.trim() === "" ? 0 : Number(catSortOrder.trim());
    const validated = validateCategoryInput({
      name: catName,
      slug: catSlug,
      parent_id: catParentId,
      commission_rate_bps: parsedBps,
      sort_order: parsedSort,
    });
    if (!validated.ok) {
      setCatFormError(validated.message);
      return;
    }

    setCatFormError(null);
    setCatBusy(true);
    try {
      if (editingCatId) {
        const res = await api<{ category: AdminCategory }>(
          `/api/admin/categories/${encodeURIComponent(editingCatId)}`,
          {
            method: "PUT",
            body: validated.value,
          }
        );
        if (res.category) {
          setCategories((prev) =>
            prev.map((item) => (item.id === res.category.id ? res.category : item))
          );
        }
        showToast(
          `Đã cập nhật danh mục "${validated.value.name}" (${formatCommissionBps(validated.value.commission_rate_bps)}).`
        );
      } else {
        const res = await api<{ category: AdminCategory }>("/api/admin/categories", {
          method: "POST",
          body: validated.value,
        });
        if (res.category) {
          setCategories((prev) => [...prev, res.category]);
        }
        showToast(`Đã tạo danh mục "${validated.value.name}".`);
      }
      resetCategoryForm();
      router.refresh();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Không thể lưu danh mục lúc này.";
      setCatFormError(msg);
      showToast(msg);
    } finally {
      setCatBusy(false);
    }
  };

  const handleDeleteCategory = async (cat: AdminCategory) => {
    if (deletingCatId) return;
    setDeletingCatId(cat.id);
    try {
      await api<{ ok: boolean }>(`/api/admin/categories/${encodeURIComponent(cat.id)}`, {
        method: "DELETE",
      });
      setCategories((prev) => prev.filter((item) => item.id !== cat.id));
      if (editingCatId === cat.id) resetCategoryForm();
      setConfirmDeleteCatId(null);
      showToast(`Đã xóa danh mục "${cat.name}".`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể xóa danh mục này.");
    } finally {
      setDeletingCatId(null);
    }
  };

  const handleCreateVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    if (vBusy) return;

    const validated = validateAdminVoucherInput({
      code: vCode,
      name: vName,
      scope: vScope,
      discount_type: vDiscountType,
      value_vnd: vDiscountType === "FIXED" ? Number(vValueVnd.trim()) : 0,
      discount_percent: vDiscountType === "PERCENT" ? Number(vDiscountPercent.trim()) : 0,
      max_discount_vnd: vDiscountType === "PERCENT" ? Number(vMaxDiscountVnd.trim()) : 0,
      min_order_vnd: vMinOrderVnd.trim() === "" ? 0 : Number(vMinOrderVnd.trim()),
      quota: Number(vQuota.trim()),
      per_user_limit: vPerUserLimit.trim() === "" ? 1 : Number(vPerUserLimit.trim()),
      budget_vnd: vBudgetVnd.trim() === "" ? 0 : Number(vBudgetVnd.trim()),
      starts_at: localVnToIso(vStartsLocal),
      ends_at: localVnToIso(vEndsLocal),
    });

    if (!validated.ok) {
      setVFormError(validated.message);
      return;
    }

    setVFormError(null);
    setVBusy(true);
    try {
      const res = await api<{ voucher: AdminVoucher }>("/api/admin/vouchers", {
        method: "POST",
        body: validated.value,
      });
      if (res.voucher) {
        setVouchers((prev) => [res.voucher, ...prev]);
      }
      setVCode("");
      setVName("");
      showToast(`Đã phát hành mã ${validated.value.code} (${validated.value.scope}).`);
      router.refresh();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Không thể tạo voucher lúc này.";
      setVFormError(msg);
      showToast(msg);
    } finally {
      setVBusy(false);
    }
  };

  const handleReloadUsersFromServer = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    if (reloadingUsers) return;
    setReloadingUsers(true);
    try {
      const params = new URLSearchParams();
      params.set("page", "1");
      params.set("page_size", "100");
      if (userQuery.trim()) params.set("q", userQuery.trim());
      if (userRoleFilter !== "all") params.set("role", userRoleFilter);
      if (userStatusFilter !== "all") params.set("status", userStatusFilter);

      const res = await api<{ items: AdminUser[]; total: number }>(
        `/api/admin/users?${params.toString()}`
      );
      setUsers(res.items ?? []);
      setTotalUsersCount(res.total ?? (res.items ?? []).length);
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể tải lại danh sách người dùng.");
    } finally {
      setReloadingUsers(false);
    }
  };

  const handleToggleUserStatus = async (target: AdminUser) => {
    const check = canModifyUserAccount(currentUser.id, target.id, "status");
    if (!check.allowed) {
      showToast(check.reason ?? "Không được phép thay đổi trạng thái tài khoản này.");
      return;
    }
    const isLocked =
      target.status.toUpperCase() === "LOCKED" || target.status.toUpperCase() === "SUSPENDED";
    const nextStatus = isLocked ? "ACTIVE" : "LOCKED";
    const reason =
      userActionReason.trim() ||
      (nextStatus === "LOCKED"
        ? "Quản trị viên tạm khóa tài khoản từ Cấu hình sàn"
        : "Quản trị viên mở khóa tài khoản");

    setBusyUserId(target.id);
    try {
      const res = await api<{ user: AdminUser }>(
        `/api/admin/users/${encodeURIComponent(target.id)}/status`,
        {
          method: "PATCH",
          body: { status: nextStatus, reason },
        }
      );
      if (res.user) {
        setUsers((prev) => prev.map((u) => (u.id === res.user.id ? res.user : u)));
      }
      showToast(
        nextStatus === "LOCKED"
          ? `Đã khóa tài khoản ${target.email} và thu hồi phiên đăng nhập.`
          : `Đã mở khóa tài khoản ${target.email}.`
      );
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái tài khoản.");
    } finally {
      setBusyUserId(null);
    }
  };

  const handleUpdateUserRole = async (target: AdminUser, nextRole: AdminUserRole) => {
    const check = canModifyUserAccount(currentUser.id, target.id, "role");
    if (!check.allowed) {
      showToast(check.reason ?? "Không được phép thay đổi vai trò tài khoản này.");
      return;
    }
    if (nextRole === target.role.toLowerCase()) return;

    const reason =
      userActionReason.trim() ||
      `Cập nhật vai trò từ ${target.role} sang ${nextRole} qua Cấu hình sàn`;

    setBusyUserId(target.id);
    try {
      const res = await api<{ user: AdminUser }>(
        `/api/admin/users/${encodeURIComponent(target.id)}/role`,
        {
          method: "PATCH",
          body: { role: nextRole, reason },
        }
      );
      if (res.user) {
        setUsers((prev) => prev.map((u) => (u.id === res.user.id ? res.user : u)));
      }
      setRoleDrafts((prev) => {
        const next = { ...prev };
        delete next[target.id];
        return next;
      });
      showToast(
        `Đã cập nhật vai trò tài khoản ${target.email} thành ${getActorRoleLabelVi(nextRole)}.`
      );
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật vai trò tài khoản.");
    } finally {
      setBusyUserId(null);
    }
  };

  const bpsPreviewNumber = Number(catCommissionBps.trim());
  const bpsPreviewLabel =
    catCommissionBps.trim() !== "" && Number.isInteger(bpsPreviewNumber) && bpsPreviewNumber >= 0
      ? formatCommissionBps(bpsPreviewNumber)
      : "0,00%";

  const tabs: Array<{ id: ConfigTab; label: string; count: number }> = [
    { id: "categories", label: "Danh mục & Phí hoa hồng", count: categories.length },
    { id: "vouchers", label: "Voucher sàn & Freeship", count: vouchers.length },
    { id: "users", label: "Người dùng & Phân quyền", count: totalUsersCount },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Cấu hình sàn</h1>
          <p className={shared.pageSubtitle}>
            Quản trị cây danh mục, biểu phí hoa hồng, voucher toàn sàn và phân quyền tài khoản
            NexusMart.
          </p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Nhóm cấu hình nền tảng">
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
                <span className={shared.tabCount}>{tab.count}</span>
              </button>
            );
          })}
        </div>
      </div>

      {/* TAB 1: CATEGORIES & COMMISSION FEES */}
      {activeTab === "categories" && (
        <section className={styles.tabBody} aria-label="Quản lý danh mục và phí hoa hồng">
          {categoriesError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải danh mục sản phẩm"
              message={categoriesError}
              actionLabel="Thử tải lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              <div className={styles.policyNotice} role="note">
                Quy tắc biểu phí (UC-A09): Mọi thay đổi về phí hoa hồng theo danh mục chỉ áp dụng
                cho các đơn hàng mới tạo sau thời điểm lưu, không tính lại trên các đơn hàng cũ đã
                đặt trước đó.
              </div>

              <form className={styles.formWell} onSubmit={(e) => void handleSaveCategory(e)}>
                <div className={styles.formWellHeader}>
                  <h2 className={styles.formWellTitle}>
                    {editingCatId ? "Chỉnh sửa danh mục & phí hoa hồng" : "Thêm danh mục mới"}
                  </h2>
                  <span className={styles.formWellMeta}>
                    Đơn vị bps: 100 bps = 1,00% (giới hạn 0 đến 5.000 bps = 50,00%)
                  </span>
                </div>

                <div className={styles.categoryFormGrid}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="cat-name" className={shared.fieldLabel}>
                      Tên danh mục
                    </label>
                    <input
                      id="cat-name"
                      type="text"
                      className={shared.textInput}
                      value={catName}
                      onChange={(e) => {
                        const val = e.target.value;
                        setCatName(val);
                        if (!slugTouched && !editingCatId) {
                          setCatSlug(slugifyCategoryName(val));
                        }
                        setCatFormError(null);
                      }}
                      placeholder="Ví dụ: Áo thun basic"
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="cat-slug" className={shared.fieldLabel}>
                      Slug đường dẫn
                    </label>
                    <input
                      id="cat-slug"
                      type="text"
                      className={shared.textInput}
                      value={catSlug}
                      onChange={(e) => {
                        setCatSlug(e.target.value);
                        setSlugTouched(true);
                        setCatFormError(null);
                      }}
                      placeholder="ao-thun-basic"
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="cat-parent" className={shared.fieldLabel}>
                      Danh mục cha
                    </label>
                    <select
                      id="cat-parent"
                      className={styles.formSelect}
                      value={catParentId}
                      onChange={(e) => setCatParentId(e.target.value)}
                    >
                      <option value="">Danh mục gốc</option>
                      {sortedCategories
                        .filter((c) => c.id !== editingCatId)
                        .map((c) => (
                          <option key={c.id} value={c.id}>
                            {c.name}
                          </option>
                        ))}
                    </select>
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="cat-bps" className={shared.fieldLabel}>
                      Phí hoa hồng (bps)
                    </label>
                    <input
                      id="cat-bps"
                      type="number"
                      inputMode="numeric"
                      min={0}
                      max={5000}
                      step={1}
                      className={shared.textInput}
                      value={catCommissionBps}
                      onChange={(e) => {
                        setCatCommissionBps(e.target.value);
                        setCatFormError(null);
                      }}
                    />
                    <span className={styles.fieldHint}>Tương đương {bpsPreviewLabel}</span>
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="cat-sort" className={shared.fieldLabel}>
                      Thứ tự hiển thị
                    </label>
                    <input
                      id="cat-sort"
                      type="number"
                      inputMode="numeric"
                      min={0}
                      step={1}
                      className={shared.textInput}
                      value={catSortOrder}
                      onChange={(e) => {
                        setCatSortOrder(e.target.value);
                        setCatFormError(null);
                      }}
                    />
                  </div>
                </div>

                <div className={styles.formFooter}>
                  <div>
                    {catFormError && <span className={shared.validationError}>{catFormError}</span>}
                  </div>
                  <div className={styles.formActions}>
                    {editingCatId && (
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        onClick={resetCategoryForm}
                        disabled={catBusy}
                      >
                        Hủy chỉnh sửa
                      </Button>
                    )}
                    <Button
                      type="submit"
                      variant="primary"
                      size="sm"
                      shape="app"
                      disabled={catBusy}
                    >
                      {catBusy ? "Đang lưu..." : editingCatId ? "Lưu thay đổi" : "Tạo danh mục"}
                    </Button>
                  </div>
                </div>
              </form>

              {sortedCategories.length === 0 ? (
                <StateView
                  kind="empty"
                  surface="app"
                  title="Chưa có danh mục nào"
                  message="Tạo danh mục đầu tiên và thiết lập mức phí hoa hồng ở biểu mẫu phía trên."
                />
              ) : (
                <>
                  <div className={shared.tableWrap}>
                    <table className={shared.dataTable}>
                      <thead>
                        <tr>
                          <th>Danh mục</th>
                          <th>Slug</th>
                          <th>Danh mục cha</th>
                          <th className={shared.colNum}>Phí hoa hồng</th>
                          <th className={shared.colNum}>Thứ tự</th>
                          <th className={shared.colAction}>Thao tác</th>
                        </tr>
                      </thead>
                      <tbody>
                        {sortedCategories.map((cat) => {
                          const parentLabel = cat.parent_id
                            ? (categoryNameById.get(cat.parent_id) ?? cat.parent_id.slice(0, 8))
                            : "Danh mục gốc";
                          const isConfirmingDelete = confirmDeleteCatId === cat.id;
                          const isDeleting = deletingCatId === cat.id;

                          return (
                            <tr
                              key={cat.id}
                              className={editingCatId === cat.id ? shared.rowSelected : undefined}
                            >
                              <td>
                                <div className={shared.cellPrimary}>{cat.name}</div>
                                <div className={shared.cellSub}>{cat.id.slice(0, 12)}</div>
                              </td>
                              <td>{cat.slug}</td>
                              <td>{parentLabel}</td>
                              <td className={shared.colNum}>
                                <div className={shared.cellPrimary}>
                                  {formatCommissionBps(cat.commission_rate_bps)}
                                </div>
                                <div className={shared.cellSub}>
                                  {formatInt(cat.commission_rate_bps)} bps
                                </div>
                              </td>
                              <td className={shared.colNum}>{formatInt(cat.sort_order)}</td>
                              <td className={shared.colAction}>
                                <div className={styles.actionRow}>
                                  {isConfirmingDelete ? (
                                    <>
                                      <Button
                                        variant="danger"
                                        size="sm"
                                        shape="app"
                                        disabled={isDeleting}
                                        onClick={() => void handleDeleteCategory(cat)}
                                      >
                                        {isDeleting ? "Đang xóa..." : "Xác nhận xóa"}
                                      </Button>
                                      <Button
                                        variant="outline"
                                        size="sm"
                                        shape="app"
                                        disabled={isDeleting}
                                        onClick={() => setConfirmDeleteCatId(null)}
                                      >
                                        Hủy
                                      </Button>
                                    </>
                                  ) : (
                                    <>
                                      <button
                                        type="button"
                                        className={shared.rowActionBtn}
                                        onClick={() => handleStartEditCategory(cat)}
                                      >
                                        Sửa
                                      </button>
                                      <button
                                        type="button"
                                        className={shared.rowActionBtn}
                                        onClick={() => setConfirmDeleteCatId(cat.id)}
                                      >
                                        Xóa
                                      </button>
                                    </>
                                  )}
                                </div>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={shared.mobileList}>
                    {sortedCategories.map((cat) => {
                      const isConfirmingDelete = confirmDeleteCatId === cat.id;
                      const isDeleting = deletingCatId === cat.id;
                      return (
                        <article key={cat.id} className={shared.mobileItem}>
                          <div className={shared.mobileLine1}>
                            <span className={shared.mobileIdentity}>{cat.name}</span>
                            <span className={shared.mobileMetric}>
                              {formatCommissionBps(cat.commission_rate_bps)}
                            </span>
                          </div>
                          <div className={shared.mobileLine2}>
                            <span className={shared.mobileMetaText}>
                              /{cat.slug} · Thứ tự {formatInt(cat.sort_order)}
                            </span>
                          </div>
                          <div className={styles.mobileActions}>
                            {isConfirmingDelete ? (
                              <>
                                <Button
                                  variant="danger"
                                  size="sm"
                                  shape="app"
                                  disabled={isDeleting}
                                  onClick={() => void handleDeleteCategory(cat)}
                                >
                                  {isDeleting ? "Đang xóa..." : "Xác nhận xóa"}
                                </Button>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  disabled={isDeleting}
                                  onClick={() => setConfirmDeleteCatId(null)}
                                >
                                  Hủy
                                </Button>
                              </>
                            ) : (
                              <>
                                <button
                                  type="button"
                                  className={shared.rowActionBtn}
                                  onClick={() => handleStartEditCategory(cat)}
                                >
                                  Sửa
                                </button>
                                <button
                                  type="button"
                                  className={shared.rowActionBtn}
                                  onClick={() => setConfirmDeleteCatId(cat.id)}
                                >
                                  Xóa
                                </button>
                              </>
                            )}
                          </div>
                        </article>
                      );
                    })}
                  </div>
                </>
              )}
            </>
          )}
        </section>
      )}

      {/* TAB 2: PLATFORM & FREESHIP VOUCHERS */}
      {activeTab === "vouchers" && (
        <section className={styles.tabBody} aria-label="Quản lý voucher toàn sàn và freeship">
          {vouchersError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải danh sách voucher sàn"
              message={vouchersError}
              actionLabel="Thử tải lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              <form className={styles.formWell} onSubmit={(e) => void handleCreateVoucher(e)}>
                <div className={styles.formWellHeader}>
                  <h2 className={styles.formWellTitle}>Phát hành voucher sàn hoặc mã Freeship</h2>
                  <span className={styles.formWellMeta}>
                    Mọi mức tiền đều tính bằng số nguyên VND
                  </span>
                </div>

                <div className={styles.voucherFormGrid}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-code" className={shared.fieldLabel}>
                      Mã voucher
                    </label>
                    <input
                      id="v-code"
                      type="text"
                      className={shared.textInput}
                      value={vCode}
                      onChange={(e) => {
                        setVCode(e.target.value.toUpperCase());
                        setVFormError(null);
                      }}
                      placeholder="NEXUS50K"
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-name" className={shared.fieldLabel}>
                      Tên chương trình
                    </label>
                    <input
                      id="v-name"
                      type="text"
                      className={shared.textInput}
                      value={vName}
                      onChange={(e) => {
                        setVName(e.target.value);
                        setVFormError(null);
                      }}
                      placeholder="Giảm 30.000đ toàn sàn"
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-scope" className={shared.fieldLabel}>
                      Phạm vi áp dụng
                    </label>
                    <select
                      id="v-scope"
                      className={styles.formSelect}
                      value={vScope}
                      onChange={(e) => setVScope(e.target.value as AdminVoucherScope)}
                    >
                      <option value="PLATFORM">Voucher sàn (PLATFORM)</option>
                      <option value="FREESHIP">Mã Freeship (FREESHIP)</option>
                    </select>
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-type" className={shared.fieldLabel}>
                      Kiểu giảm giá
                    </label>
                    <select
                      id="v-type"
                      className={styles.formSelect}
                      value={vDiscountType}
                      onChange={(e) => setVDiscountType(e.target.value as AdminVoucherDiscountType)}
                    >
                      <option value="FIXED">Giảm trực tiếp (VND)</option>
                      <option value="PERCENT">Giảm theo phần trăm (%)</option>
                    </select>
                  </div>

                  {vDiscountType === "FIXED" ? (
                    <div className={styles.fieldGroup}>
                      <label htmlFor="v-value" className={shared.fieldLabel}>
                        Mức giảm (VND)
                      </label>
                      <input
                        id="v-value"
                        type="number"
                        inputMode="numeric"
                        min={1}
                        step={1}
                        className={shared.textInput}
                        value={vValueVnd}
                        onChange={(e) => {
                          setVValueVnd(e.target.value);
                          setVFormError(null);
                        }}
                      />
                    </div>
                  ) : (
                    <>
                      <div className={styles.fieldGroup}>
                        <label htmlFor="v-pct" className={shared.fieldLabel}>
                          Phần trăm giảm (1 đến 100)
                        </label>
                        <input
                          id="v-pct"
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={100}
                          step={1}
                          className={shared.textInput}
                          value={vDiscountPercent}
                          onChange={(e) => {
                            setVDiscountPercent(e.target.value);
                            setVFormError(null);
                          }}
                        />
                      </div>
                      <div className={styles.fieldGroup}>
                        <label htmlFor="v-max-discount" className={shared.fieldLabel}>
                          Giảm tối đa (VND)
                        </label>
                        <input
                          id="v-max-discount"
                          type="number"
                          inputMode="numeric"
                          min={1}
                          step={1}
                          className={shared.textInput}
                          value={vMaxDiscountVnd}
                          onChange={(e) => {
                            setVMaxDiscountVnd(e.target.value);
                            setVFormError(null);
                          }}
                        />
                      </div>
                    </>
                  )}

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-min-order" className={shared.fieldLabel}>
                      Đơn tối thiểu (VND)
                    </label>
                    <input
                      id="v-min-order"
                      type="number"
                      inputMode="numeric"
                      min={0}
                      step={1}
                      className={shared.textInput}
                      value={vMinOrderVnd}
                      onChange={(e) => {
                        setVMinOrderVnd(e.target.value);
                        setVFormError(null);
                      }}
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-quota" className={shared.fieldLabel}>
                      Tổng số lượt
                    </label>
                    <input
                      id="v-quota"
                      type="number"
                      inputMode="numeric"
                      min={1}
                      step={1}
                      className={shared.textInput}
                      value={vQuota}
                      onChange={(e) => {
                        setVQuota(e.target.value);
                        setVFormError(null);
                      }}
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-per-user" className={shared.fieldLabel}>
                      Giới hạn mỗi tài khoản
                    </label>
                    <input
                      id="v-per-user"
                      type="number"
                      inputMode="numeric"
                      min={1}
                      step={1}
                      className={shared.textInput}
                      value={vPerUserLimit}
                      onChange={(e) => {
                        setVPerUserLimit(e.target.value);
                        setVFormError(null);
                      }}
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-budget" className={shared.fieldLabel}>
                      Ngân sách tối đa (VND)
                    </label>
                    <input
                      id="v-budget"
                      type="number"
                      inputMode="numeric"
                      min={0}
                      step={1}
                      className={shared.textInput}
                      value={vBudgetVnd}
                      onChange={(e) => {
                        setVBudgetVnd(e.target.value);
                        setVFormError(null);
                      }}
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-start" className={shared.fieldLabel}>
                      Bắt đầu hiệu lực
                    </label>
                    <input
                      id="v-start"
                      type="datetime-local"
                      className={shared.textInput}
                      value={vStartsLocal}
                      onChange={(e) => {
                        setVStartsLocal(e.target.value);
                        setVFormError(null);
                      }}
                    />
                  </div>

                  <div className={styles.fieldGroup}>
                    <label htmlFor="v-end" className={shared.fieldLabel}>
                      Kết thúc hiệu lực
                    </label>
                    <input
                      id="v-end"
                      type="datetime-local"
                      className={shared.textInput}
                      value={vEndsLocal}
                      onChange={(e) => {
                        setVEndsLocal(e.target.value);
                        setVFormError(null);
                      }}
                    />
                  </div>
                </div>

                <div className={styles.formFooter}>
                  <div>
                    {vFormError && <span className={shared.validationError}>{vFormError}</span>}
                  </div>
                  <div className={styles.formActions}>
                    <Button type="submit" variant="primary" size="sm" shape="app" disabled={vBusy}>
                      {vBusy ? "Đang phát hành..." : "Tạo voucher"}
                    </Button>
                  </div>
                </div>
              </form>

              <div className={shared.toolbar}>
                <div className={shared.toolbarLeft}>
                  <div className={shared.searchWrap}>
                    <span className={shared.searchIcon}>
                      <IconSearch size={14} />
                    </span>
                    <input
                      type="search"
                      className={shared.toolbarSearch}
                      value={voucherQuery}
                      onChange={(e) => setVoucherQuery(e.target.value)}
                      placeholder="Tìm mã hoặc tên voucher..."
                      aria-label="Tìm voucher sàn và freeship"
                    />
                  </div>
                </div>
                <div className={shared.toolbarRight}>
                  <span className={shared.resultCount}>
                    {formatInt(filteredVouchers.length)} voucher
                  </span>
                  <select
                    className={shared.toolbarSelect}
                    value={voucherScopeFilter}
                    onChange={(e) =>
                      setVoucherScopeFilter(e.target.value as "all" | AdminVoucherScope)
                    }
                    aria-label="Lọc phạm vi voucher"
                  >
                    <option value="all">Tất cả phạm vi</option>
                    <option value="PLATFORM">Voucher sàn</option>
                    <option value="FREESHIP">Freeship</option>
                  </select>
                </div>
              </div>

              {filteredVouchers.length === 0 ? (
                <StateView
                  kind="empty"
                  surface="app"
                  title="Chưa có voucher sàn hoặc mã Freeship nào"
                  message="Tạo mã khuyến mãi mới ở biểu mẫu phía trên để kích hoạt cho toàn sàn."
                />
              ) : (
                <>
                  <div className={shared.tableWrap}>
                    <table className={shared.dataTable}>
                      <thead>
                        <tr>
                          <th>Mã &amp; chương trình</th>
                          <th>Phạm vi</th>
                          <th className={shared.colNum}>Mức giảm &amp; đơn tối thiểu</th>
                          <th>Lượt sử dụng</th>
                          <th>Hiệu lực</th>
                          <th>Trạng thái</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredVouchers.map((v) => {
                          const scopeMeta = getVoucherScopeMeta(v.scope);
                          const ratio =
                            v.quota > 0
                              ? Math.min(100, Math.round((v.used_count / v.quota) * 100))
                              : 0;
                          const discountSummary =
                            v.discount_type.toUpperCase() === "PERCENT"
                              ? `Giảm ${formatInt(v.discount_percent)}% (tối đa ${formatVnd(v.max_discount_vnd)})`
                              : `Giảm ${formatVnd(v.value_vnd)}`;

                          return (
                            <tr key={v.id}>
                              <td>
                                <div className={shared.cellPrimary}>{v.code}</div>
                                <div className={shared.cellSub}>{v.name}</div>
                              </td>
                              <td>
                                <StatusPill label={scopeMeta.label} tone={scopeMeta.tone} />
                              </td>
                              <td className={shared.colNum}>
                                <div className={shared.cellPrimary}>{discountSummary}</div>
                                <div className={shared.cellSub}>
                                  Đơn từ {formatVnd(v.min_order_vnd)}
                                </div>
                              </td>
                              <td>
                                <div className={styles.quotaCell}>
                                  <span className={shared.cellSub}>
                                    {formatInt(v.used_count)} / {formatInt(v.quota)} lượt
                                  </span>
                                  <div className={shared.progressTrack} aria-hidden="true">
                                    <div
                                      className={shared.progressFill}
                                      style={{ width: `${ratio}%` }}
                                    />
                                  </div>
                                </div>
                              </td>
                              <td>
                                <div className={shared.cellSub}>
                                  Từ {v.starts_at ? formatDateTimeVi(v.starts_at) : EMPTY_DASH}
                                </div>
                                <div className={shared.cellSub}>
                                  Đến {v.ends_at ? formatDateTimeVi(v.ends_at) : EMPTY_DASH}
                                </div>
                              </td>
                              <td>
                                <StatusPill
                                  label={v.is_active ? "Đang hoạt động" : "Tạm ngưng"}
                                  tone={v.is_active ? "positive" : "neutral"}
                                />
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={shared.mobileList}>
                    {filteredVouchers.map((v) => {
                      const scopeMeta = getVoucherScopeMeta(v.scope);
                      const discountSummary =
                        v.discount_type.toUpperCase() === "PERCENT"
                          ? `-${formatInt(v.discount_percent)}%`
                          : `-${formatVnd(v.value_vnd)}`;
                      return (
                        <article key={v.id} className={shared.mobileItem}>
                          <div className={shared.mobileLine1}>
                            <span className={shared.mobileIdentity}>
                              {v.code} · {v.name}
                            </span>
                            <span className={shared.mobileMetric}>{discountSummary}</span>
                          </div>
                          <div className={shared.mobileLine2}>
                            <span className={shared.mobileMetaText}>
                              {formatInt(v.used_count)}/{formatInt(v.quota)} lượt · Đơn từ{" "}
                              {formatVnd(v.min_order_vnd)}
                            </span>
                            <StatusPill label={scopeMeta.label} tone={scopeMeta.tone} />
                          </div>
                        </article>
                      );
                    })}
                  </div>
                </>
              )}
            </>
          )}
        </section>
      )}

      {/* TAB 3: USERS & ROLES */}
      {activeTab === "users" && (
        <section className={styles.tabBody} aria-label="Quản lý người dùng và phân quyền">
          {usersError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải danh sách tài khoản"
              message={usersError}
              actionLabel="Thử tải lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              <form
                className={shared.toolbar}
                onSubmit={(e) => void handleReloadUsersFromServer(e)}
              >
                <div className={shared.toolbarLeft}>
                  <div className={shared.searchWrap}>
                    <span className={shared.searchIcon}>
                      <IconSearch size={14} />
                    </span>
                    <input
                      type="search"
                      className={shared.toolbarSearch}
                      value={userQuery}
                      onChange={(e) => setUserQuery(e.target.value)}
                      placeholder="Tìm theo email, họ tên, SĐT..."
                      aria-label="Tìm kiếm tài khoản người dùng"
                    />
                  </div>

                  <select
                    className={shared.toolbarSelect}
                    value={userRoleFilter}
                    onChange={(e) => setUserRoleFilter(e.target.value)}
                    aria-label="Lọc theo vai trò"
                  >
                    <option value="all">Tất cả vai trò</option>
                    {ADMIN_USER_ROLES.map((role) => (
                      <option key={role} value={role}>
                        {getActorRoleLabelVi(role)}
                      </option>
                    ))}
                  </select>

                  <select
                    className={shared.toolbarSelect}
                    value={userStatusFilter}
                    onChange={(e) => setUserStatusFilter(e.target.value)}
                    aria-label="Lọc theo trạng thái tài khoản"
                  >
                    <option value="all">Tất cả trạng thái</option>
                    <option value="ACTIVE">Đang hoạt động</option>
                    <option value="LOCKED">Đã khóa</option>
                    <option value="SUSPENDED">Tạm đình chỉ</option>
                    <option value="PENDING_VERIFICATION">Chờ xác thực</option>
                  </select>

                  <Button
                    type="submit"
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={reloadingUsers}
                  >
                    {reloadingUsers ? "Đang tải..." : "Lọc từ hệ thống"}
                  </Button>
                </div>

                <div className={shared.toolbarRight}>
                  <span className={shared.resultCount}>
                    {formatInt(filteredUsers.length)} / {formatInt(totalUsersCount)} tài khoản
                  </span>
                </div>
              </form>

              <div className={styles.auditReasonBar}>
                <label htmlFor="user-audit-reason" className={styles.auditReasonLabel}>
                  Lý do kiểm toán (tùy chọn):
                </label>
                <div className={styles.auditReasonInputWrap}>
                  <input
                    id="user-audit-reason"
                    type="text"
                    className={shared.textInput}
                    value={userActionReason}
                    onChange={(e) => setUserActionReason(e.target.value)}
                    placeholder="Ghi chú sẽ lưu vào nhật ký kiểm toán khi khóa, mở khóa hoặc đổi vai trò..."
                  />
                </div>
              </div>

              {filteredUsers.length === 0 ? (
                <p className={shared.emptyRow}>
                  Không tìm thấy tài khoản người dùng phù hợp với bộ lọc hiện tại.
                </p>
              ) : (
                <>
                  <div className={shared.tableWrap}>
                    <table className={shared.dataTable}>
                      <thead>
                        <tr>
                          <th>Tài khoản</th>
                          <th>Số điện thoại</th>
                          <th>Vai trò</th>
                          <th>Trạng thái</th>
                          <th className={shared.colAction}>Thao tác</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredUsers.map((u) => {
                          const isSelf = u.id === currentUser.id;
                          const statusPermission = canModifyUserAccount(
                            currentUser.id,
                            u.id,
                            "status"
                          );
                          const rolePermission = canModifyUserAccount(currentUser.id, u.id, "role");
                          const statusMeta = getUserStatusMeta(u.status);
                          const isLocked =
                            u.status.toUpperCase() === "LOCKED" ||
                            u.status.toUpperCase() === "SUSPENDED";
                          const currentRole = (u.role || "buyer").toLowerCase() as AdminUserRole;
                          const draftRole = roleDrafts[u.id] ?? currentRole;
                          const roleChanged = draftRole !== currentRole;
                          const isBusy = busyUserId === u.id;

                          return (
                            <tr key={u.id}>
                              <td>
                                <div className={shared.cellPrimary}>
                                  {u.full_name || "Chưa cập nhật tên"}
                                  {isSelf && (
                                    <span className={styles.selfTag}>Tài khoản của bạn</span>
                                  )}
                                </div>
                                <div className={shared.cellSub}>{u.email || u.id}</div>
                              </td>
                              <td>{u.phone || EMPTY_DASH}</td>
                              <td>
                                <div className={styles.roleEditor}>
                                  <select
                                    className={shared.toolbarSelect}
                                    value={draftRole}
                                    disabled={!rolePermission.allowed || isBusy}
                                    aria-label={`Chọn vai trò cho ${u.email}`}
                                    onChange={(e) =>
                                      setRoleDrafts((prev) => ({
                                        ...prev,
                                        [u.id]: e.target.value as AdminUserRole,
                                      }))
                                    }
                                  >
                                    {ADMIN_USER_ROLES.map((role) => (
                                      <option key={role} value={role}>
                                        {getActorRoleLabelVi(role)}
                                      </option>
                                    ))}
                                  </select>
                                  {roleChanged && rolePermission.allowed && (
                                    <Button
                                      variant="primary"
                                      size="sm"
                                      shape="app"
                                      disabled={isBusy}
                                      onClick={() => void handleUpdateUserRole(u, draftRole)}
                                    >
                                      Lưu vai trò
                                    </Button>
                                  )}
                                </div>
                              </td>
                              <td>
                                <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                              </td>
                              <td className={shared.colAction}>
                                {!statusPermission.allowed ? (
                                  <span
                                    className={styles.selfLockedLabel}
                                    title={statusPermission.reason}
                                  >
                                    Không thể tự khóa
                                  </span>
                                ) : isLocked ? (
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    shape="app"
                                    disabled={isBusy}
                                    onClick={() => void handleToggleUserStatus(u)}
                                  >
                                    {isBusy ? "Đang xử lý..." : "Mở khóa"}
                                  </Button>
                                ) : (
                                  <Button
                                    variant="danger"
                                    size="sm"
                                    shape="app"
                                    disabled={isBusy}
                                    onClick={() => void handleToggleUserStatus(u)}
                                  >
                                    {isBusy ? "Đang xử lý..." : "Khóa tài khoản"}
                                  </Button>
                                )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={shared.mobileList}>
                    {filteredUsers.map((u) => {
                      const isSelf = u.id === currentUser.id;
                      const statusPermission = canModifyUserAccount(currentUser.id, u.id, "status");
                      const rolePermission = canModifyUserAccount(currentUser.id, u.id, "role");
                      const statusMeta = getUserStatusMeta(u.status);
                      const isLocked =
                        u.status.toUpperCase() === "LOCKED" ||
                        u.status.toUpperCase() === "SUSPENDED";
                      const currentRole = (u.role || "buyer").toLowerCase() as AdminUserRole;
                      const draftRole = roleDrafts[u.id] ?? currentRole;
                      const roleChanged = draftRole !== currentRole;
                      const isBusy = busyUserId === u.id;

                      return (
                        <article key={u.id} className={shared.mobileItem}>
                          <div className={shared.mobileLine1}>
                            <span className={shared.mobileIdentity}>
                              {u.full_name || u.email}
                              {isSelf && <span className={styles.selfTag}>Bạn</span>}
                            </span>
                            <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                          </div>
                          <div className={shared.mobileLine2}>
                            <span className={shared.mobileMetaText}>
                              {u.email} · {u.phone || EMPTY_DASH}
                            </span>
                          </div>
                          <div className={styles.mobileActions}>
                            <select
                              className={shared.toolbarSelect}
                              value={draftRole}
                              disabled={!rolePermission.allowed || isBusy}
                              aria-label={`Vai trò của ${u.email}`}
                              onChange={(e) =>
                                setRoleDrafts((prev) => ({
                                  ...prev,
                                  [u.id]: e.target.value as AdminUserRole,
                                }))
                              }
                            >
                              {ADMIN_USER_ROLES.map((role) => (
                                <option key={role} value={role}>
                                  {getActorRoleLabelVi(role)}
                                </option>
                              ))}
                            </select>
                            {roleChanged && rolePermission.allowed && (
                              <Button
                                variant="primary"
                                size="sm"
                                shape="app"
                                disabled={isBusy}
                                onClick={() => void handleUpdateUserRole(u, draftRole)}
                              >
                                Lưu vai trò
                              </Button>
                            )}
                            {!statusPermission.allowed ? (
                              <span className={styles.selfLockedLabel}>Không thể tự khóa</span>
                            ) : (
                              <Button
                                variant={isLocked ? "outline" : "danger"}
                                size="sm"
                                shape="app"
                                disabled={isBusy}
                                onClick={() => void handleToggleUserStatus(u)}
                              >
                                {isLocked ? "Mở khóa" : "Khóa tài khoản"}
                              </Button>
                            )}
                          </div>
                        </article>
                      );
                    })}
                  </div>
                </>
              )}
            </>
          )}
        </section>
      )}
    </div>
  );
}

export function AdminConfigClient(props: AdminConfigClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Cấu hình sàn" />}>
      <AdminStateGate
        title="Cấu hình sàn"
        subtitle="Quản trị cây danh mục, biểu phí hoa hồng, voucher toàn sàn và phân quyền tài khoản NexusMart."
        emptyTitle="Chưa có dữ liệu cấu hình"
        emptyMessage="Bắt đầu tạo danh mục hoặc voucher toàn sàn để vận hành hệ thống."
        errorTitle="Không thể tải cấu hình nền tảng"
        errorMessage="Các dịch vụ cấu hình tạm thời không phản hồi. Vui lòng thử tải lại."
        loadingRows={5}
      >
        {({ initialQuery }) => <ConfigWorkspace {...props} initialQuery={initialQuery} />}
      </AdminStateGate>
    </Suspense>
  );
}
