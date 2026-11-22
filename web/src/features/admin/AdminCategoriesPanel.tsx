"use client";

import React, { useCallback, useEffect, useState } from "react";
import type { GatewayCategory } from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import { formatInt } from "@/lib/format";
import { formatCommissionBps } from "./types";
import shared from "./adminShared.module.css";

function slugify(raw: string): string {
  return raw
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .replace(/[^a-zA-Z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .toLowerCase();
}

export function AdminCategoriesPanel() {
  const { showToast } = useMockStore();
  const [categories, setCategories] = useState<GatewayCategory[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Create form
  const [newName, setNewName] = useState("");
  const [newSlug, setNewSlug] = useState("");
  const [newParentId, setNewParentId] = useState("");
  const [newBps, setNewBps] = useState("500");
  const [newSortOrder, setNewSortOrder] = useState("10");
  const [creating, setCreating] = useState(false);

  // Inline edit state
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editSlug, setEditSlug] = useState("");
  const [editParentId, setEditParentId] = useState("");
  const [editBps, setEditBps] = useState("500");
  const [editSortOrder, setEditSortOrder] = useState("0");
  const [savingId, setSavingId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const loadCategories = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<{ categories: GatewayCategory[] }>("categories");
      setCategories(res.categories ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách danh mục ngành hàng."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadCategories();
  }, [loadCategories]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = newName.trim();
    const cleanSlug = (newSlug.trim() || slugify(cleanName)).trim();
    const bps = Math.max(0, Math.min(5000, Math.trunc(Number(newBps) || 0)));
    const sortOrder = Math.trunc(Number(newSortOrder) || 0);
    if (!cleanName || !cleanSlug) {
      showToast("Vui lòng nhập tên và đường dẫn danh mục.");
      return;
    }
    setCreating(true);
    try {
      await api<{ category: GatewayCategory }>("admin/categories", {
        method: "POST",
        json: {
          name: cleanName,
          slug: cleanSlug,
          parent_id: newParentId || "",
          commission_rate_bps: bps,
          sort_order: sortOrder,
        },
      });
      setNewName("");
      setNewSlug("");
      setNewParentId("");
      setNewBps("500");
      await loadCategories();
      showToast(`Đã tạo danh mục "${cleanName}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo danh mục mới."));
    } finally {
      setCreating(false);
    }
  };

  const startEdit = (cat: GatewayCategory) => {
    setEditingId(cat.id);
    setEditName(cat.name);
    setEditSlug(cat.slug);
    setEditParentId(cat.parent_id || "");
    setEditBps(String(cat.commission_rate_bps));
    setEditSortOrder(String(cat.sort_order));
  };

  const handleSaveEdit = async (catId: string) => {
    const cleanName = editName.trim();
    const cleanSlug = editSlug.trim();
    const bps = Math.max(0, Math.min(5000, Math.trunc(Number(editBps) || 0)));
    const sortOrder = Math.trunc(Number(editSortOrder) || 0);
    if (!cleanName || !cleanSlug) {
      showToast("Tên và đường dẫn danh mục không được bỏ trống.");
      return;
    }
    setSavingId(catId);
    try {
      await api<{ category: GatewayCategory }>(`admin/categories/${catId}`, {
        method: "PUT",
        json: {
          name: cleanName,
          slug: cleanSlug,
          parent_id: editParentId || "",
          commission_rate_bps: bps,
          sort_order: sortOrder,
        },
      });
      setEditingId(null);
      await loadCategories();
      showToast(`Đã cập nhật danh mục "${cleanName}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật danh mục."));
    } finally {
      setSavingId(null);
    }
  };

  const handleDelete = async (cat: GatewayCategory) => {
    setSavingId(cat.id);
    try {
      await api<{ ok: boolean }>(`admin/categories/${cat.id}`, { method: "DELETE" });
      setConfirmDeleteId(null);
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
      <section className={shared.panel} aria-labelledby="cat-create-heading">
        <div className={shared.panelHeader}>
          <h2 id="cat-create-heading" className={shared.panelTitle}>
            Tạo danh mục & tỷ lệ hoa hồng mới
          </h2>
          <span className={shared.panelMeta}>100 bps = 1,0% hoa hồng sàn</span>
        </div>

        <form onSubmit={(e) => void handleCreate(e)} className={shared.detailGrid}>
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
              className={shared.toolbarSelect}
              style={{ height: 38 }}
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
              step={50}
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
              className={shared.textInput}
              value={newSortOrder}
              onChange={(e) => setNewSortOrder(e.target.value)}
            />
          </div>
          <div className={shared.detailField} style={{ justifyContent: "flex-end" }}>
            <Button type="submit" variant="primary" size="sm" shape="app" disabled={creating}>
              {creating ? "Đang tạo..." : "Thêm danh mục"}
            </Button>
          </div>
        </form>
      </section>

      <section className={shared.panel} aria-labelledby="cat-list-heading">
        <div className={shared.panelHeader}>
          <h2 id="cat-list-heading" className={shared.panelTitle}>
            Danh sách danh mục hiện hành ({formatInt(categories.length)})
          </h2>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadCategories()}>
            Làm mới
          </Button>
        </div>

        <div className={shared.tableWrap}>
          <table className={shared.dataTable}>
            <thead>
              <tr>
                <th>Tên danh mục & đường dẫn</th>
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
                    <td className={shared.colNum}>
                      {isEditing ? (
                        <input
                          type="number"
                          min={0}
                          max={5000}
                          step={50}
                          className={shared.textInput}
                          style={{ width: 92, height: 34, textAlign: "right" }}
                          value={editBps}
                          onChange={(e) => setEditBps(e.target.value)}
                          aria-label="Hoa hồng bps"
                        />
                      ) : (
                        <span>
                          {formatCommissionBps(cat.commission_rate_bps)} ({cat.commission_rate_bps}{" "}
                          bps)
                        </span>
                      )}
                    </td>
                    <td className={shared.colNum}>
                      {isEditing ? (
                        <input
                          type="number"
                          className={shared.textInput}
                          style={{ width: 72, height: 34, textAlign: "right" }}
                          value={editSortOrder}
                          onChange={(e) => setEditSortOrder(e.target.value)}
                          aria-label="Thứ tự"
                        />
                      ) : (
                        formatInt(cat.sort_order)
                      )}
                    </td>
                    <td className={shared.colAction}>
                      <div style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
                        {isEditing ? (
                          <>
                            <Button
                              variant="primary"
                              size="sm"
                              shape="app"
                              disabled={isBusy}
                              onClick={() => void handleSaveEdit(cat.id)}
                            >
                              {isBusy ? "Lưu..." : "Lưu"}
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
                            {confirmDeleteId === cat.id ? (
                              <>
                                <Button
                                  variant="danger"
                                  size="sm"
                                  shape="app"
                                  disabled={isBusy}
                                  onClick={() => void handleDelete(cat)}
                                >
                                  Xác nhận xóa
                                </Button>
                                <button
                                  type="button"
                                  className={shared.rowActionBtn}
                                  onClick={() => setConfirmDeleteId(null)}
                                >
                                  Giữ lại
                                </button>
                              </>
                            ) : (
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                onClick={() => setConfirmDeleteId(cat.id)}
                              >
                                Xóa
                              </button>
                            )}
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
      </section>
    </div>
  );
}
