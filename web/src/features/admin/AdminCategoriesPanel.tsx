"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Button, StateView } from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import type { GatewayCategory } from "@/lib/gateway-types";
import { useAdminContext } from "./AdminShell";
import { AdminConfirmDialog } from "./AdminUi";
import { useAdminCapability } from "./AdminUserContext";
import { StatusPill } from "./StatusPill";
import { formatCommissionBps } from "./types";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/cau-hinh/page.module.css";

function slugify(raw: string): string {
  return raw
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .replace(/[^a-zA-Z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .toLowerCase();
}

export interface AdminCategoriesPanelProps {
  initialCategories?: GatewayCategory[];
  initialError?: string | null;
}

export function AdminCategoriesPanel({
  initialCategories,
  initialError = null,
}: AdminCategoriesPanelProps = {}) {
  const { showToast } = useAdminContext();
  const canWriteCategories = useAdminCapability("categories.write");

  const hasInitial = initialCategories !== undefined;
  const [categories, setCategories] = useState<GatewayCategory[]>(initialCategories ?? []);
  const [loading, setLoading] = useState(!hasInitial);
  const [errorMsg, setErrorMsg] = useState<string | null>(initialError);

  // Create form
  const [newName, setNewName] = useState("");
  const [newSlug, setNewSlug] = useState("");
  const [newParentId, setNewParentId] = useState("");
  const [newBps, setNewBps] = useState("500");
  const [newSortOrder, setNewSortOrder] = useState("10");
  const [newIsProhibited, setNewIsProhibited] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  // Inline edit state
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editSlug, setEditSlug] = useState("");
  const [editParentId, setEditParentId] = useState("");
  const [editBps, setEditBps] = useState("500");
  const [editSortOrder, setEditSortOrder] = useState("0");
  const [editIsProhibited, setEditIsProhibited] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  const [savingId, setSavingId] = useState<string | null>(null);
  const [confirmDeleteCat, setConfirmDeleteCat] = useState<GatewayCategory | null>(null);

  const loadCategories = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<{ categories: GatewayCategory[] }>("/api/admin/categories");
      setCategories(res.categories ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách danh mục ngành hàng."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!hasInitial) {
      void loadCategories();
    }
  }, [hasInitial, loadCategories]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = newName.trim();
    const cleanSlug = (newSlug.trim() || slugify(cleanName)).trim();
    const bps = Math.max(0, Math.min(5000, Math.trunc(Number(newBps) || 0)));
    const sortOrder = Math.max(0, Math.trunc(Number(newSortOrder) || 0));
    if (!cleanName || !cleanSlug) {
      setCreateError("Vui lòng nhập tên và đường dẫn danh mục.");
      return;
    }
    setCreateError(null);
    setCreating(true);
    try {
      await api<{ category: GatewayCategory }>("/api/admin/categories", {
        method: "POST",
        json: {
          name: cleanName,
          slug: cleanSlug,
          parent_id: newParentId || "",
          commission_rate_bps: bps,
          sort_order: sortOrder,
          is_prohibited: newIsProhibited,
        },
      });
      setNewName("");
      setNewSlug("");
      setNewParentId("");
      setNewBps("500");
      setNewIsProhibited(false);
      await loadCategories();
      showToast(`Đã tạo danh mục "${cleanName}".`);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể tạo danh mục mới.");
      setCreateError(msg);
      showToast(msg);
    } finally {
      setCreating(false);
    }
  };

  const startEdit = (cat: GatewayCategory) => {
    setEditingId(cat.id);
    setEditName(cat.name);
    setEditSlug(cat.slug);
    setEditParentId(cat.parent_id || "");
    setEditBps(String(cat.commission_rate_bps ?? 0));
    setEditSortOrder(String(cat.sort_order));
    setEditIsProhibited(Boolean(cat.is_prohibited));
    setEditError(null);
  };

  const handleSaveEdit = async (catId: string) => {
    const cleanName = editName.trim();
    const cleanSlug = editSlug.trim();
    const bps = Math.max(0, Math.min(5000, Math.trunc(Number(editBps) || 0)));
    const sortOrder = Math.max(0, Math.trunc(Number(editSortOrder) || 0));
    if (!cleanName || !cleanSlug) {
      setEditError("Tên và đường dẫn danh mục không được bỏ trống.");
      return;
    }
    setEditError(null);
    setSavingId(catId);
    try {
      await api<{ category: GatewayCategory }>(
        `/api/admin/categories/${encodeURIComponent(catId)}`,
        {
          method: "PUT",
          json: {
            name: cleanName,
            slug: cleanSlug,
            parent_id: editParentId || "",
            commission_rate_bps: bps,
            sort_order: sortOrder,
            is_prohibited: editIsProhibited,
          },
        }
      );
      setEditingId(null);
      await loadCategories();
      showToast(`Đã cập nhật danh mục "${cleanName}".`);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể cập nhật danh mục.");
      setEditError(msg);
      showToast(msg);
    } finally {
      setSavingId(null);
    }
  };

  const handleDelete = async (cat: GatewayCategory) => {
    setSavingId(cat.id);
    try {
      await api<{ ok: boolean }>(`/api/admin/categories/${encodeURIComponent(cat.id)}`, {
        method: "DELETE",
      });
      setConfirmDeleteCat(null);
      await loadCategories();
      showToast(`Đã xóa danh mục "${cat.name}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xóa danh mục đang có sản phẩm hoặc danh mục con."));
    } finally {
      setSavingId(null);
    }
  };

  if (loading) {
    return (
      <div className={shared.stateWrap}>
        <StateView kind="loading" surface="app" title="Đang tải danh mục ngành hàng..." rows={4} />
      </div>
    );
  }

  if (errorMsg) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh mục"
          message={errorMsg}
          actionLabel="Thử lại"
          onAction={() => void loadCategories()}
        />
      </div>
    );
  }

  return (
    <div className={shared.sectionStack}>
      {canWriteCategories && (
        <section className={shared.panel} aria-labelledby="cat-create-heading">
          <div className={shared.panelHeader}>
            <h2 id="cat-create-heading" className={shared.panelTitle}>
              Tạo danh mục mới
            </h2>
            <span className={shared.panelMeta}>100 bps = 1,0% hoa hồng</span>
          </div>

          <form onSubmit={(e) => void handleCreate(e)} noValidate className={shared.detailGrid}>
            <div className={shared.detailField}>
              <label htmlFor="cat-new-name" className={shared.fieldLabel}>
                Tên danh mục
              </label>
              <input
                id="cat-new-name"
                type="text"
                className={shared.textInput}
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="Ví dụ: Áo khoác thể thao"
                required
              />
            </div>
            <div className={shared.detailField}>
              <label htmlFor="cat-new-slug" className={shared.fieldLabel}>
                Đường dẫn (slug)
              </label>
              <input
                id="cat-new-slug"
                type="text"
                className={shared.textInput}
                value={newSlug}
                onChange={(e) => setNewSlug(e.target.value)}
                placeholder={newName ? slugify(newName) : "ao-khoac-the-thao"}
              />
            </div>
            <div className={shared.detailField}>
              <label htmlFor="cat-new-parent" className={shared.fieldLabel}>
                Danh mục cha (tùy chọn)
              </label>
              <select
                id="cat-new-parent"
                className={shared.formSelect}
                value={newParentId}
                onChange={(e) => setNewParentId(e.target.value)}
              >
                <option value="">Danh mục gốc</option>
                {categories.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </div>
            <div className={shared.detailField}>
              <label htmlFor="cat-new-bps" className={shared.fieldLabel}>
                Hoa hồng (bps, VD: 500 = 5%)
              </label>
              <input
                id="cat-new-bps"
                type="number"
                min={0}
                max={5000}
                step={1}
                className={shared.textInput}
                value={newBps}
                onChange={(e) => setNewBps(e.target.value)}
              />
            </div>
            <div className={shared.detailField}>
              <label htmlFor="cat-new-sort" className={shared.fieldLabel}>
                Thứ tự hiển thị
              </label>
              <input
                id="cat-new-sort"
                type="number"
                min={0}
                step={1}
                className={shared.textInput}
                value={newSortOrder}
                onChange={(e) => setNewSortOrder(e.target.value)}
              />
            </div>
            <div className={shared.detailField}>
              <span className={shared.fieldLabel}>Chính sách kinh doanh</span>
              <label className={styles.checkboxLabel}>
                <input
                  id="cat-new-prohibited"
                  type="checkbox"
                  checked={newIsProhibited}
                  onChange={(e) => setNewIsProhibited(e.target.checked)}
                />
                <span>Danh mục hàng cấm</span>
              </label>
            </div>
            {createError && (
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <p className={shared.validationError} role="alert">
                  {createError}
                </p>
              </div>
            )}
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <div className={shared.detailButtons}>
                <Button type="submit" variant="primary" size="sm" shape="app" loading={creating}>
                  Thêm danh mục
                </Button>
              </div>
            </div>
          </form>
        </section>
      )}

      <section className={shared.panel} aria-labelledby="cat-list-heading">
        <div className={shared.panelHeader}>
          <h2 id="cat-list-heading" className={shared.panelTitle}>
            Danh mục hiện hành ({formatInt(categories.length)})
          </h2>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadCategories()}>
            Làm mới
          </Button>
        </div>

        {editError && (
          <p className={shared.validationError} role="alert" style={{ marginBottom: 10 }}>
            {editError}
          </p>
        )}

        <div className={shared.tableWrap}>
          <table className={shared.dataTable}>
            <thead>
              <tr>
                <th>Tên danh mục &amp; đường dẫn</th>
                <th>Trạng thái</th>
                <th className={shared.colNum}>Hoa hồng</th>
                <th className={shared.colNum}>Thứ tự</th>
                <th className={shared.colAction}>Thao tác</th>
              </tr>
            </thead>
            <tbody>
              {categories.map((cat) => {
                const isEditing = editingId === cat.id;
                const isBusy = savingId === cat.id;
                return (
                  <tr key={cat.id}>
                    <td>
                      {isEditing ? (
                        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                          <input
                            type="text"
                            className={shared.textInput}
                            style={{ maxWidth: 220, height: 34 }}
                            value={editName}
                            onChange={(e) => setEditName(e.target.value)}
                            aria-label="Tên danh mục"
                          />
                          <input
                            type="text"
                            className={shared.textInput}
                            style={{ maxWidth: 180, height: 34 }}
                            value={editSlug}
                            onChange={(e) => setEditSlug(e.target.value)}
                            aria-label="Slug danh mục"
                          />
                        </div>
                      ) : (
                        <>
                          <div className={shared.cellPrimary}>{cat.name}</div>
                          <div className={shared.cellSub}>/{cat.slug}</div>
                        </>
                      )}
                    </td>
                    <td>
                      {isEditing ? (
                        <label className={styles.checkboxLabel}>
                          <input
                            type="checkbox"
                            checked={editIsProhibited}
                            onChange={(e) => setEditIsProhibited(e.target.checked)}
                          />
                          <span>Danh mục hàng cấm</span>
                        </label>
                      ) : cat.is_prohibited ? (
                        <StatusPill label="Danh mục hàng cấm" tone="negative" />
                      ) : (
                        <StatusPill label="Cho phép bán" tone="positive" />
                      )}
                    </td>
                    <td className={shared.colNum}>
                      {isEditing ? (
                        <input
                          type="number"
                          min={0}
                          max={5000}
                          step={1}
                          className={styles.compactInput}
                          value={editBps}
                          onChange={(e) => setEditBps(e.target.value)}
                          aria-label="Hoa hồng bps"
                        />
                      ) : (
                        <span>
                          {formatCommissionBps(cat.commission_rate_bps ?? 0)} (
                          {cat.commission_rate_bps ?? 0} bps)
                        </span>
                      )}
                    </td>
                    <td className={shared.colNum}>
                      {isEditing ? (
                        <input
                          type="number"
                          min={0}
                          step={1}
                          className={styles.compactInput}
                          value={editSortOrder}
                          onChange={(e) => setEditSortOrder(e.target.value)}
                          aria-label="Thứ tự"
                        />
                      ) : (
                        formatInt(cat.sort_order)
                      )}
                    </td>
                    <td className={shared.colAction}>
                      {canWriteCategories ? (
                        <div className={styles.actionRow}>
                          {isEditing ? (
                            <>
                              <Button
                                variant="primary"
                                size="sm"
                                shape="app"
                                loading={isBusy}
                                onClick={() => void handleSaveEdit(cat.id)}
                              >
                                Lưu
                              </Button>
                              <Button
                                variant="outline"
                                size="sm"
                                shape="app"
                                onClick={() => setEditingId(null)}
                              >
                                Hủy
                              </Button>
                            </>
                          ) : (
                            <>
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                onClick={() => startEdit(cat)}
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
                            </>
                          )}
                        </div>
                      ) : (
                        <span className={shared.cellSub}>Chỉ xem</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        <div className={shared.mobileList}>
          {categories.map((cat) => {
            const isEditing = editingId === cat.id;
            const isBusy = savingId === cat.id;
            return (
              <article key={cat.id} className={shared.mobileItem}>
                {isEditing ? (
                  <div className={shared.detailGrid}>
                    <div className={shared.detailField}>
                      <label htmlFor={`m-cat-name-${cat.id}`} className={shared.fieldLabel}>
                        Tên danh mục
                      </label>
                      <input
                        id={`m-cat-name-${cat.id}`}
                        type="text"
                        className={shared.textInput}
                        value={editName}
                        onChange={(e) => setEditName(e.target.value)}
                      />
                    </div>
                    <div className={shared.detailField}>
                      <label htmlFor={`m-cat-slug-${cat.id}`} className={shared.fieldLabel}>
                        Đường dẫn (slug)
                      </label>
                      <input
                        id={`m-cat-slug-${cat.id}`}
                        type="text"
                        className={shared.textInput}
                        value={editSlug}
                        onChange={(e) => setEditSlug(e.target.value)}
                      />
                    </div>
                    <div className={shared.detailField}>
                      <label htmlFor={`m-cat-bps-${cat.id}`} className={shared.fieldLabel}>
                        Hoa hồng (bps)
                      </label>
                      <input
                        id={`m-cat-bps-${cat.id}`}
                        type="number"
                        min={0}
                        max={5000}
                        step={1}
                        className={shared.textInput}
                        value={editBps}
                        onChange={(e) => setEditBps(e.target.value)}
                      />
                    </div>
                    <div className={shared.detailField}>
                      <label htmlFor={`m-cat-order-${cat.id}`} className={shared.fieldLabel}>
                        Thứ tự hiển thị
                      </label>
                      <input
                        id={`m-cat-order-${cat.id}`}
                        type="number"
                        min={0}
                        step={1}
                        className={shared.textInput}
                        value={editSortOrder}
                        onChange={(e) => setEditSortOrder(e.target.value)}
                      />
                    </div>
                    <div className={shared.detailField}>
                      <label className={styles.checkboxLabel}>
                        <input
                          type="checkbox"
                          checked={editIsProhibited}
                          onChange={(e) => setEditIsProhibited(e.target.checked)}
                        />
                        <span>Danh mục hàng cấm</span>
                      </label>
                    </div>
                    {editError && (
                      <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                        <p className={shared.validationError} role="alert">
                          {editError}
                        </p>
                      </div>
                    )}
                    <div className={shared.detailButtons}>
                      <Button
                        variant="primary"
                        size="sm"
                        shape="app"
                        loading={isBusy}
                        onClick={() => void handleSaveEdit(cat.id)}
                      >
                        Lưu
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        onClick={() => {
                          setEditingId(null);
                          setEditError(null);
                        }}
                      >
                        Hủy
                      </Button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{cat.name}</span>
                      <span className={shared.mobileMetric}>
                        {formatCommissionBps(cat.commission_rate_bps ?? 0)}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        /{cat.slug} · {cat.commission_rate_bps ?? 0} bps · Thứ tự{" "}
                        {formatInt(cat.sort_order)}
                      </span>
                      {cat.is_prohibited && (
                        <StatusPill label="Danh mục hàng cấm" tone="negative" />
                      )}
                    </div>
                    {canWriteCategories && (
                      <div className={styles.mobileActions}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={() => startEdit(cat)}
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
                    )}
                  </>
                )}
              </article>
            );
          })}
        </div>
      </section>

      <AdminConfirmDialog
        open={Boolean(confirmDeleteCat)}
        title="Xác nhận xóa danh mục"
        description={
          confirmDeleteCat
            ? `Xóa danh mục "${confirmDeleteCat.name}" (/${confirmDeleteCat.slug})? Thao tác không thể hoàn tác.`
            : ""
        }
        confirmLabel="Xác nhận xóa"
        busy={Boolean(confirmDeleteCat && savingId === confirmDeleteCat.id)}
        onCancel={() => setConfirmDeleteCat(null)}
        onConfirm={() => {
          if (confirmDeleteCat) void handleDelete(confirmDeleteCat);
        }}
      />
    </div>
  );
}
