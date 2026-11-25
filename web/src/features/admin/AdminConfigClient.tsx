"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnIsoDate, formatVnd } from "@/lib/format";
import { EMPTY_DASH, localVnInputToIso } from "./adminMeta";
import { getActorRoleLabelVi } from "./adminApiTypes";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import {
  AdminConfirmDialog,
  AdminDrawer,
  InlineSavedBadge,
  SlidingTabBar,
  formatNowTimeVi,
} from "./AdminUi";
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
  serverNowMs?: number;
}

function defaultVoucherWindow(baseMs: number): { startLocal: string; endLocal: string } {
  const startDate = formatVnIsoDate(baseMs);
  const endDate = formatVnIsoDate(baseMs + 7 * 24 * 3600_000);
  return {
    startLocal: `${startDate}T00:00`,
    endLocal: `${endDate}T23:59`,
  };
}

function ConfigWorkspace({
  initialCategories,
  categoriesError,
  initialVouchers,
  vouchersError,
  initialUsers,
  usersTotal,
  usersError,
  serverNowMs,
  initialQuery,
}: AdminConfigClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { user: currentUser, showToast } = useAdminContext();

  const [activeTab, setActiveTab] = useState<ConfigTab>("categories");

  // --- Block 1: Categories & Commission Rates ---
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
  const [catSavedAt, setCatSavedAt] = useState<string | null>(null);
  const [confirmDeleteCat, setConfirmDeleteCat] = useState<AdminCategory | null>(null);
  const [deletingCatId, setDeletingCatId] = useState<string | null>(null);

  // --- Block 2: Platform & Freeship Vouchers ---
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
  const [vStartsLocal, setVStartsLocal] = useState(
    () => defaultVoucherWindow(serverNowMs ?? Date.now()).startLocal
  );
  const [vEndsLocal, setVEndsLocal] = useState(
    () => defaultVoucherWindow(serverNowMs ?? Date.now()).endLocal
  );
  const [vFormError, setVFormError] = useState<string | null>(null);
  const [vBusy, setVBusy] = useState(false);
  const [voucherSavedAt, setVoucherSavedAt] = useState<string | null>(null);
  const [drawerVoucher, setDrawerVoucher] = useState<AdminVoucher | null>(null);

  // --- Block 3: Users & RBAC ---
  const [users, setUsers] = useState<AdminUser[]>(initialUsers);
  const [totalUsersCount, setTotalUsersCount] = useState<number>(usersTotal);
  const [userQuery, setUserQuery] = useState(initialQuery);
  const [userRoleFilter, setUserRoleFilter] = useState<string>("all");
  const [userStatusFilter, setUserStatusFilter] = useState<string>("all");
  const [userActionReason, setUserActionReason] = useState("");
  const [roleDrafts, setRoleDrafts] = useState<Record<string, AdminUserRole>>({});
  const [busyUserId, setBusyUserId] = useState<string | null>(null);
  const [reloadingUsers, setReloadingUsers] = useState(false);
  const [userSavedAt, setUserSavedAt] = useState<string | null>(null);
  const [drawerUserId, setDrawerUserId] = useState<string | null>(null);
  const [confirmLockUser, setConfirmLockUser] = useState<AdminUser | null>(null);
  const [lockReason, setLockReason] = useState("");

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

  const drawerUser = useMemo(
    () => (drawerUserId ? (users.find((u) => u.id === drawerUserId) ?? null) : null),
    [users, drawerUserId]
  );

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
    setConfirmDeleteCat(null);
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
      setCatSavedAt(formatNowTimeVi());
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
      setConfirmDeleteCat(null);
      setCatSavedAt(formatNowTimeVi());
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
      starts_at: localVnInputToIso(vStartsLocal),
      ends_at: localVnInputToIso(vEndsLocal),
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
      setVoucherSavedAt(formatNowTimeVi());
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

  const handleRequestToggleUserStatus = (target: AdminUser) => {
    const check = canModifyUserAccount(currentUser.id, target.id, "status");
    if (!check.allowed) {
      showToast(check.reason ?? "Không được phép thay đổi trạng thái tài khoản này.");
      return;
    }
    const isLocked =
      target.status.toUpperCase() === "LOCKED" || target.status.toUpperCase() === "SUSPENDED";
    if (!isLocked) {
      setLockReason("");
      setConfirmLockUser(target);
      return;
    }
    void executeToggleUserStatus(target);
  };

  const executeToggleUserStatus = async (target: AdminUser, overrideReason?: string) => {
    const check = canModifyUserAccount(currentUser.id, target.id, "status");
    if (!check.allowed) {
      showToast(check.reason ?? "Không được phép thay đổi trạng thái tài khoản này.");
      return;
    }
    const isLocked =
      target.status.toUpperCase() === "LOCKED" || target.status.toUpperCase() === "SUSPENDED";
    const nextStatus = isLocked ? "ACTIVE" : "LOCKED";
    const reason =
      overrideReason?.trim() ||
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
      setConfirmLockUser(null);
      setLockReason("");
      setUserSavedAt(formatNowTimeVi());
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
      setUserSavedAt(formatNowTimeVi());
      showToast(`Đã cập nhật vai trò ${target.email} thành ${getActorRoleLabelVi(nextRole)}.`);
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
    { id: "categories", label: "Danh mục & Hoa hồng", count: categories.length },
    { id: "vouchers", label: "Voucher sàn & Freeship", count: vouchers.length },
    { id: "users", label: "Người dùng & Phân quyền", count: totalUsersCount },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Cấu hình sàn</h1>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={activeTab}
        onChange={setActiveTab}
        ariaLabel="Nhóm cấu hình nền tảng"
      />

      {/* TAB 1: CATEGORIES & COMMISSION FEES */}
      {activeTab === "categories" && (
        <section className={styles.tabBody} aria-label="Quản lý danh mục và phí hoa hồng">
          {categoriesError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải danh mục sản phẩm"
              message={categoriesError}
              actionLabel="Thử lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              {/* 2-Column Label–Control Config Block */}
              <div className={styles.configRow}>
                <div className={styles.configLabelCol}>
                  <h2 className={styles.configBlockTitle}>
                    {editingCatId ? "Chỉnh sửa danh mục" : "Danh mục & phí hoa hồng"}
                  </h2>
                  <p className={styles.configBlockMeta}>
                    {formatInt(categories.length)} danh mục, 100 bps = 1,00% (tối đa 5.000 bps)
                  </p>
                  <details className={shared.policyDetails}>
                    <summary className={shared.policySummary}>
                      <span>Quy tắc hiệu lực biểu phí</span>
                      <span>+</span>
                    </summary>
                    <div className={shared.policyBody}>
                      Thay đổi phí hoa hồng chỉ áp dụng cho đơn hàng mới tạo sau thời điểm lưu,
                      không tính lại trên các đơn cũ.
                    </div>
                  </details>
                </div>

                <form className={styles.configPanel} onSubmit={(e) => void handleSaveCategory(e)}>
                  <div className={styles.twoColFields}>
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
                        className={shared.formSelect}
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
                        Phí hoa hồng (bps, tương đương {bpsPreviewLabel})
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
                    <div className={styles.formFeedback}>
                      {catFormError && (
                        <span className={shared.validationError}>{catFormError}</span>
                      )}
                      <InlineSavedBadge stamp={catSavedAt} />
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
                          Hủy sửa
                        </Button>
                      )}
                      <Button
                        type="submit"
                        variant="primary"
                        size="sm"
                        shape="app"
                        loading={catBusy}
                      >
                        {editingCatId ? "Lưu thay đổi" : "Lưu danh mục"}
                      </Button>
                    </div>
                  </div>
                </form>
              </div>

              <hr className={styles.sectionRule} />

              {sortedCategories.length === 0 ? (
                <StateView
                  kind="empty"
                  surface="app"
                  title="Chưa có danh mục nào"
                  message="Tạo danh mục đầu tiên ở khối cấu hình phía trên."
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
                                  <button
                                    type="button"
                                    className={shared.rowActionBtn}
                                    onClick={() => handleStartEditCategory(cat)}
                                  >
                                    Sửa
                                  </button>
                                  <button
                                    type="button"
                                    className={`${shared.rowActionBtn} ${shared.rowActionBtnDanger}`}
                                    onClick={() => setConfirmDeleteCat(cat)}
                                  >
                                    Xóa
                                  </button>
                                </div>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={shared.mobileList}>
                    {sortedCategories.map((cat) => (
                      <article key={cat.id} className={shared.mobileItem}>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{cat.name}</span>
                          <span className={shared.mobileMetric}>
                            {formatCommissionBps(cat.commission_rate_bps)}
                          </span>
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            /{cat.slug}, thứ tự {formatInt(cat.sort_order)}
                          </span>
                        </div>
                        <div className={styles.mobileActions}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={() => handleStartEditCategory(cat)}
                          >
                            Sửa
                          </button>
                          <button
                            type="button"
                            className={`${shared.rowActionBtn} ${shared.rowActionBtnDanger}`}
                            onClick={() => setConfirmDeleteCat(cat)}
                          >
                            Xóa
                          </button>
                        </div>
                      </article>
                    ))}
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
              title="Không thể tải voucher sàn"
              message={vouchersError}
              actionLabel="Thử lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              {/* 2-Column Label–Control Config Block */}
              <div className={styles.configRow}>
                <div className={styles.configLabelCol}>
                  <h2 className={styles.configBlockTitle}>Phát hành voucher sàn</h2>
                  <p className={styles.configBlockMeta}>
                    {formatInt(vouchers.length)} mã đang lưu, số tiền tính theo số nguyên VND
                  </p>
                </div>

                <form className={styles.configPanel} onSubmit={(e) => void handleCreateVoucher(e)}>
                  <div className={styles.twoColFields}>
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
                        className={shared.formSelect}
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
                        className={shared.formSelect}
                        value={vDiscountType}
                        onChange={(e) =>
                          setVDiscountType(e.target.value as AdminVoucherDiscountType)
                        }
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
                    <div className={styles.formFeedback}>
                      {vFormError && <span className={shared.validationError}>{vFormError}</span>}
                      <InlineSavedBadge stamp={voucherSavedAt} />
                    </div>
                    <div className={styles.formActions}>
                      <Button type="submit" variant="primary" size="sm" shape="app" loading={vBusy}>
                        Tạo voucher
                      </Button>
                    </div>
                  </div>
                </form>
              </div>

              <hr className={styles.sectionRule} />

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
                <div className={shared.toolbarRight}>
                  <span className={shared.resultCount}>
                    {formatInt(filteredVouchers.length)} voucher
                  </span>
                </div>
              </div>

              {filteredVouchers.length === 0 ? (
                <StateView
                  kind="empty"
                  surface="app"
                  title="Chưa có voucher phù hợp"
                  message="Tạo mã mới ở khối phát hành phía trên."
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
                          const ratio = v.quota > 0 ? Math.min(1, v.used_count / v.quota) : 0;
                          const discountSummary =
                            v.discount_type.toUpperCase() === "PERCENT"
                              ? `Giảm ${formatInt(v.discount_percent)}% (tối đa ${formatVnd(v.max_discount_vnd)})`
                              : `Giảm ${formatVnd(v.value_vnd)}`;

                          return (
                            <tr
                              key={v.id}
                              className={`${shared.rowInteractive} ${drawerVoucher?.id === v.id ? shared.rowSelected : ""}`}
                              onClick={() => setDrawerVoucher(v)}
                            >
                              <td>
                                <button
                                  type="button"
                                  className={shared.rowTriggerBtn}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setDrawerVoucher(v);
                                  }}
                                >
                                  <span className={shared.cellPrimary}>{v.code}</span>
                                  <span className={shared.cellSub}>{v.name}</span>
                                </button>
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
                                      style={{ transform: `scaleX(${ratio.toFixed(4)})` }}
                                    />
                                  </div>
                                </div>
                              </td>
                              <td>
                                <div className={shared.cellSub}>
                                  {v.starts_at ? `Từ ${formatDateTimeVi(v.starts_at)}` : EMPTY_DASH}
                                </div>
                                <div className={shared.cellSub}>
                                  {v.ends_at ? `Đến ${formatDateTimeVi(v.ends_at)}` : EMPTY_DASH}
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
                          <button
                            type="button"
                            className={shared.mobileItemBtn}
                            onClick={() => setDrawerVoucher(v)}
                          >
                            <span className={shared.mobileLine1}>
                              <span className={shared.mobileIdentity}>
                                {v.code}: {v.name}
                              </span>
                              <span className={shared.mobileMetric}>{discountSummary}</span>
                            </span>
                            <span className={shared.mobileLine2}>
                              <span className={shared.mobileMetaText}>
                                {formatInt(v.used_count)}/{formatInt(v.quota)} lượt, đơn từ{" "}
                                {formatVnd(v.min_order_vnd)}
                              </span>
                              <StatusPill label={scopeMeta.label} tone={scopeMeta.tone} />
                            </span>
                          </button>
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
              actionLabel="Thử lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              {/* 2-Column Label–Control Config Block for Audit Reason */}
              <div className={styles.configRow}>
                <div className={styles.configLabelCol}>
                  <h2 className={styles.configBlockTitle}>Ghi chú kiểm toán phân quyền</h2>
                  <p className={styles.configBlockMeta}>
                    Đính kèm tự động khi khóa, mở khóa hoặc đổi vai trò
                  </p>
                </div>

                <div className={styles.configPanel}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="user-audit-reason" className={shared.fieldLabel}>
                      Lý do thao tác (tùy chọn)
                    </label>
                    <input
                      id="user-audit-reason"
                      type="text"
                      className={shared.textInput}
                      value={userActionReason}
                      onChange={(e) => setUserActionReason(e.target.value)}
                      placeholder="Nhập lý do lưu vào nhật ký kiểm toán..."
                    />
                  </div>
                  <div className={styles.formFooter}>
                    <InlineSavedBadge stamp={userSavedAt} />
                    <span className={shared.resultCount}>
                      {formatInt(filteredUsers.length)} / {formatInt(totalUsersCount)} tài khoản
                    </span>
                  </div>
                </div>
              </div>

              <hr className={styles.sectionRule} />

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
                    loading={reloadingUsers}
                  >
                    Lọc từ hệ thống
                  </Button>
                </div>
              </form>

              {filteredUsers.length === 0 ? (
                <p className={shared.emptyRow}>Không tìm thấy tài khoản phù hợp.</p>
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
                            <tr
                              key={u.id}
                              className={`${shared.rowInteractive} ${drawerUserId === u.id ? shared.rowSelected : ""}`}
                              onClick={() => setDrawerUserId(u.id)}
                            >
                              <td>
                                <button
                                  type="button"
                                  className={shared.rowTriggerBtn}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setDrawerUserId(u.id);
                                  }}
                                >
                                  <span className={shared.cellPrimary}>
                                    {u.full_name || "Chưa cập nhật tên"}
                                    {isSelf && (
                                      <span className={styles.selfTag}>Tài khoản của bạn</span>
                                    )}
                                  </span>
                                  <span className={shared.cellSub}>{u.email || u.id}</span>
                                </button>
                              </td>
                              <td>{u.phone || EMPTY_DASH}</td>
                              <td onClick={(e) => e.stopPropagation()}>
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
                                      loading={isBusy}
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
                              <td className={shared.colAction} onClick={(e) => e.stopPropagation()}>
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
                                    loading={isBusy}
                                    onClick={() => handleRequestToggleUserStatus(u)}
                                  >
                                    Mở khóa
                                  </Button>
                                ) : (
                                  <Button
                                    variant="danger"
                                    size="sm"
                                    shape="app"
                                    disabled={isBusy}
                                    onClick={() => handleRequestToggleUserStatus(u)}
                                  >
                                    Khóa tài khoản
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
                          <button
                            type="button"
                            className={shared.mobileItemBtn}
                            onClick={() => setDrawerUserId(u.id)}
                          >
                            <span className={shared.mobileLine1}>
                              <span className={shared.mobileIdentity}>
                                {u.full_name || u.email || u.id}
                                {isSelf && <span className={styles.selfTag}>Bạn</span>}
                              </span>
                              <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                            </span>
                            <span className={shared.mobileLine2}>
                              <span className={shared.mobileMetaText}>
                                {[u.email || u.id, u.phone].filter(Boolean).join(", ")}
                              </span>
                            </span>
                          </button>
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
                                loading={isBusy}
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
                                onClick={() => handleRequestToggleUserStatus(u)}
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

      {/* Detail Drawer: Voucher */}
      <AdminDrawer
        open={Boolean(drawerVoucher)}
        onClose={() => setDrawerVoucher(null)}
        title={drawerVoucher?.code ?? ""}
        subtitle={drawerVoucher?.name}
        headerBadge={
          drawerVoucher ? (
            <StatusPill
              label={drawerVoucher.is_active ? "Đang hoạt động" : "Tạm ngưng"}
              tone={drawerVoucher.is_active ? "positive" : "neutral"}
            />
          ) : undefined
        }
      >
        {drawerVoucher && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Phạm vi</span>
                <span className={shared.detailVal}>
                  {getVoucherScopeMeta(drawerVoucher.scope).label}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mức giảm</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerVoucher.discount_type.toUpperCase() === "PERCENT"
                    ? `${formatInt(drawerVoucher.discount_percent)}% (tối đa ${formatVnd(drawerVoucher.max_discount_vnd)})`
                    : formatVnd(drawerVoucher.value_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Đơn tối thiểu</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerVoucher.min_order_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Lượt đã dùng / tổng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(drawerVoucher.used_count)} / {formatInt(drawerVoucher.quota)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Giới hạn mỗi tài khoản</span>
                <span className={`${shared.detailValPlain} num`}>
                  {formatInt(drawerVoucher.per_user_limit)} lượt
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Ngân sách tối đa</span>
                <span className={`${shared.detailValPlain} num`}>
                  {formatVnd(drawerVoucher.budget_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Bắt đầu</span>
                <span className={`${shared.detailValPlain} num`}>
                  {drawerVoucher.starts_at ? formatDateTimeVi(drawerVoucher.starts_at) : EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Kết thúc</span>
                <span className={`${shared.detailValPlain} num`}>
                  {drawerVoucher.ends_at ? formatDateTimeVi(drawerVoucher.ends_at) : EMPTY_DASH}
                </span>
              </div>
            </div>
          </div>
        )}
      </AdminDrawer>

      {/* Detail Drawer: User */}
      <AdminDrawer
        open={Boolean(drawerUser)}
        onClose={() => setDrawerUserId(null)}
        title={drawerUser?.full_name || drawerUser?.email || ""}
        subtitle={drawerUser?.email}
        headerBadge={
          drawerUser ? (
            <StatusPill
              label={getUserStatusMeta(drawerUser.status).label}
              tone={getUserStatusMeta(drawerUser.status).tone}
            />
          ) : undefined
        }
      >
        {drawerUser && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã tài khoản</span>
                <span className={`${shared.detailValPlain} num`}>{drawerUser.id}</span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Vai trò hiện tại</span>
                <span className={shared.detailVal}>{getActorRoleLabelVi(drawerUser.role)}</span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số điện thoại</span>
                <span className={`${shared.detailValPlain} num`}>
                  {drawerUser.phone || EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Xác thực SĐT</span>
                <span className={shared.detailValPlain}>
                  {drawerUser.phone_verified ? "Đã xác thực" : "Chưa xác thực"}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số lần bom hàng COD</span>
                <span className={`${shared.detailValPlain} num`}>
                  {formatInt(drawerUser.cod_rejected_count)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã gian hàng gắn kèm</span>
                <span className={`${shared.detailValPlain} num`}>
                  {drawerUser.shop_id || EMPTY_DASH}
                </span>
              </div>
            </div>
          </div>
        )}
      </AdminDrawer>

      {/* Strict Confirm Dialog: Delete Category */}
      <AdminConfirmDialog
        open={Boolean(confirmDeleteCat)}
        title="Xác nhận xóa danh mục"
        description={
          confirmDeleteCat
            ? `Xóa danh mục "${confirmDeleteCat.name}" (${confirmDeleteCat.slug})? Thao tác không thể hoàn tác.`
            : ""
        }
        confirmLabel="Xác nhận xóa"
        busy={Boolean(confirmDeleteCat && deletingCatId === confirmDeleteCat.id)}
        onCancel={() => setConfirmDeleteCat(null)}
        onConfirm={() => {
          if (confirmDeleteCat) void handleDeleteCategory(confirmDeleteCat);
        }}
      />

      {/* Strict Confirm Dialog: Lock User Account */}
      <AdminConfirmDialog
        open={Boolean(confirmLockUser)}
        title="Xác nhận khóa tài khoản"
        description={
          confirmLockUser
            ? `Khóa tài khoản ${confirmLockUser.email} và thu hồi toàn bộ phiên đăng nhập hiện tại?`
            : ""
        }
        confirmLabel="Xác nhận khóa"
        busy={Boolean(confirmLockUser && busyUserId === confirmLockUser.id)}
        onCancel={() => {
          setConfirmLockUser(null);
          setLockReason("");
        }}
        onConfirm={() => {
          if (confirmLockUser) void executeToggleUserStatus(confirmLockUser, lockReason);
        }}
      >
        <div className={styles.fieldGroup}>
          <label htmlFor="confirm-lock-reason" className={shared.fieldLabel}>
            Lý do khóa (lưu nhật ký kiểm toán)
          </label>
          <input
            id="confirm-lock-reason"
            type="text"
            className={shared.textInput}
            value={lockReason}
            onChange={(e) => setLockReason(e.target.value)}
            placeholder="Nhập lý do khóa tài khoản..."
          />
        </div>
      </AdminConfirmDialog>
    </div>
  );
}

export function AdminConfigClient(props: AdminConfigClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Cấu hình sàn" />}>
      <AdminStateGate
        title="Cấu hình sàn"
        emptyTitle="Chưa có dữ liệu cấu hình"
        emptyMessage="Bắt đầu tạo danh mục hoặc voucher toàn sàn."
        errorTitle="Không thể tải cấu hình nền tảng"
        errorMessage="Dịch vụ cấu hình tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery }) => <ConfigWorkspace {...props} initialQuery={initialQuery} />}
      </AdminStateGate>
    </Suspense>
  );
}
