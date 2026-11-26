"use client";

import React, { useCallback, useEffect, useState } from "react";
import { FolderTree, Plus, RefreshCw } from "lucide-react";
import { EmptyState, ErrorState, IndexTable, StatusBadge } from "@/components/app";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, getErrorMessage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import type { GatewayCategory } from "@/lib/gateway-types";
import { useAdminContext } from "./AdminShell";
import { useAdminCapability } from "./AdminUserContext";
import { formatCommissionBps } from "./types";
import { ConfirmActionDialog, FilterSelect, TableLoadingSkeleton } from "./config/adminUiHelpers";

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
    return <TableLoadingSkeleton rows={4} label="Đang tải danh mục ngành hàng..." />;
  }

  if (errorMsg) {
    return (
      <ErrorState
        title="Không thể tải danh mục"
        description={errorMsg}
        onRetry={() => void loadCategories()}
      />
    );
  }

  return (
    <div className="space-y-5">
      {canWriteCategories ? (
        <Card aria-labelledby="cat-create-heading">
          <CardHeader>
            <CardTitle id="cat-create-heading">Tạo danh mục mới</CardTitle>
            <CardDescription>100 bps = 1,0% hoa hồng</CardDescription>
          </CardHeader>
          <form onSubmit={(e) => void handleCreate(e)} noValidate>
            <CardContent>
              <FieldGroup className="gap-4">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="cat-new-name">Tên danh mục</FieldLabel>
                    <Input
                      id="cat-new-name"
                      type="text"
                      value={newName}
                      onChange={(e) => setNewName(e.target.value)}
                      placeholder="Ví dụ: Áo khoác thể thao"
                      required
                      className="bg-card"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="cat-new-slug">Đường dẫn (slug)</FieldLabel>
                    <Input
                      id="cat-new-slug"
                      type="text"
                      value={newSlug}
                      onChange={(e) => setNewSlug(e.target.value)}
                      placeholder={newName ? slugify(newName) : "ao-khoac-the-thao"}
                      className="bg-card"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="cat-new-parent">Danh mục cha (tùy chọn)</FieldLabel>
                    <FilterSelect
                      id="cat-new-parent"
                      value={newParentId}
                      onValueChange={setNewParentId}
                      ariaLabel="Danh mục cha"
                      options={[
                        { value: "", label: "Danh mục gốc" },
                        ...categories.map((c) => ({ value: c.id, label: c.name })),
                      ]}
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="cat-new-bps">Hoa hồng (bps, VD: 500 = 5%)</FieldLabel>
                    <Input
                      id="cat-new-bps"
                      type="number"
                      min={0}
                      max={5000}
                      step={1}
                      value={newBps}
                      onChange={(e) => setNewBps(e.target.value)}
                      className="bg-card tabular-nums"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel htmlFor="cat-new-sort">Thứ tự hiển thị</FieldLabel>
                    <Input
                      id="cat-new-sort"
                      type="number"
                      min={0}
                      step={1}
                      value={newSortOrder}
                      onChange={(e) => setNewSortOrder(e.target.value)}
                      className="bg-card tabular-nums"
                    />
                  </Field>

                  <Field className="gap-1.5">
                    <FieldLabel>Chính sách kinh doanh</FieldLabel>
                    <label
                      htmlFor="cat-new-prohibited"
                      className="flex min-h-9 cursor-pointer items-center gap-2 rounded-md border border-border bg-muted/35 px-3 text-sm text-foreground"
                    >
                      <Checkbox
                        id="cat-new-prohibited"
                        checked={newIsProhibited}
                        onCheckedChange={(checked) => setNewIsProhibited(Boolean(checked))}
                      />
                      <span>Danh mục hàng cấm</span>
                    </label>
                  </Field>
                </div>
                <FieldError>{createError}</FieldError>
              </FieldGroup>
            </CardContent>
            <CardFooter className="justify-end border-t border-border pt-3.5">
              <Button type="submit" size="sm" disabled={creating}>
                <Plus className="size-4" aria-hidden="true" />
                <span>{creating ? "Đang thêm..." : "Thêm danh mục"}</span>
              </Button>
            </CardFooter>
          </form>
        </Card>
      ) : null}

      <section className="space-y-3" aria-labelledby="cat-list-heading">
        <IndexTable
          toolbar={
            <div className="flex w-full flex-wrap items-center justify-between gap-2">
              <h2 id="cat-list-heading" className="text-sm font-semibold text-foreground">
                Danh mục hiện hành ({formatInt(categories.length)})
              </h2>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => void loadCategories()}
              >
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>Làm mới</span>
              </Button>
            </div>
          }
        >
          {editError ? (
            <div className="border-b border-border bg-badge-critical-bg/40 px-4 py-2.5">
              <FieldError>{editError}</FieldError>
            </div>
          ) : null}

          {categories.length === 0 ? (
            <div className="p-4">
              <EmptyState
                icon={FolderTree}
                title="Chưa có danh mục ngành hàng nào"
                className="shadow-none"
              />
            </div>
          ) : (
            <>
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Tên danh mục &amp; đường dẫn</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead className="text-right">Hoa hồng</TableHead>
                      <TableHead className="text-right">Thứ tự</TableHead>
                      <TableHead className="text-right">Thao tác</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {categories.map((cat) => {
                      const isEditing = editingId === cat.id;
                      const isBusy = savingId === cat.id;
                      return (
                        <TableRow key={cat.id}>
                          <TableCell className="whitespace-normal">
                            {isEditing ? (
                              <div className="flex flex-wrap gap-2">
                                <Input
                                  type="text"
                                  value={editName}
                                  onChange={(e) => setEditName(e.target.value)}
                                  aria-label="Tên danh mục"
                                  className="h-8 max-w-56 bg-card"
                                />
                                <Input
                                  type="text"
                                  value={editSlug}
                                  onChange={(e) => setEditSlug(e.target.value)}
                                  aria-label="Slug danh mục"
                                  className="h-8 max-w-44 bg-card"
                                />
                              </div>
                            ) : (
                              <>
                                <div className="font-medium text-foreground">{cat.name}</div>
                                <div className="text-xs text-muted-foreground">/{cat.slug}</div>
                              </>
                            )}
                          </TableCell>
                          <TableCell>
                            {isEditing ? (
                              <label className="inline-flex cursor-pointer items-center gap-2 text-[13px] text-foreground">
                                <Checkbox
                                  checked={editIsProhibited}
                                  onCheckedChange={(checked) =>
                                    setEditIsProhibited(Boolean(checked))
                                  }
                                />
                                <span>Danh mục hàng cấm</span>
                              </label>
                            ) : cat.is_prohibited ? (
                              <StatusBadge tone="critical">Danh mục hàng cấm</StatusBadge>
                            ) : (
                              <StatusBadge tone="success">Cho phép bán</StatusBadge>
                            )}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            {isEditing ? (
                              <Input
                                type="number"
                                min={0}
                                max={5000}
                                step={1}
                                value={editBps}
                                onChange={(e) => setEditBps(e.target.value)}
                                aria-label="Hoa hồng bps"
                                className="ml-auto h-8 w-24 bg-card text-right tabular-nums"
                              />
                            ) : (
                              <span>
                                {formatCommissionBps(cat.commission_rate_bps ?? 0)} (
                                {cat.commission_rate_bps ?? 0} bps)
                              </span>
                            )}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            {isEditing ? (
                              <Input
                                type="number"
                                min={0}
                                step={1}
                                value={editSortOrder}
                                onChange={(e) => setEditSortOrder(e.target.value)}
                                aria-label="Thứ tự"
                                className="ml-auto h-8 w-20 bg-card text-right tabular-nums"
                              />
                            ) : (
                              formatInt(cat.sort_order)
                            )}
                          </TableCell>
                          <TableCell className="text-right">
                            {canWriteCategories ? (
                              <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
                                {isEditing ? (
                                  <>
                                    <Button
                                      type="button"
                                      size="xs"
                                      disabled={isBusy}
                                      onClick={() => void handleSaveEdit(cat.id)}
                                    >
                                      {isBusy ? "Đang lưu..." : "Lưu"}
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      onClick={() => setEditingId(null)}
                                    >
                                      Hủy
                                    </Button>
                                  </>
                                ) : (
                                  <>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      onClick={() => startEdit(cat)}
                                    >
                                      Sửa
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="destructive"
                                      size="xs"
                                      onClick={() => setConfirmDeleteCat(cat)}
                                    >
                                      Xóa
                                    </Button>
                                  </>
                                )}
                              </div>
                            ) : (
                              <span className="text-xs text-muted-foreground">Chỉ xem</span>
                            )}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              <div className="divide-y divide-border md:hidden">
                {categories.map((cat) => {
                  const isEditing = editingId === cat.id;
                  const isBusy = savingId === cat.id;
                  return (
                    <article key={cat.id} className="space-y-3 p-4">
                      {isEditing ? (
                        <div className="space-y-3">
                          <Field className="gap-1">
                            <FieldLabel htmlFor={`m-cat-name-${cat.id}`}>Tên danh mục</FieldLabel>
                            <Input
                              id={`m-cat-name-${cat.id}`}
                              type="text"
                              value={editName}
                              onChange={(e) => setEditName(e.target.value)}
                              className="bg-card"
                            />
                          </Field>
                          <Field className="gap-1">
                            <FieldLabel htmlFor={`m-cat-slug-${cat.id}`}>
                              Đường dẫn (slug)
                            </FieldLabel>
                            <Input
                              id={`m-cat-slug-${cat.id}`}
                              type="text"
                              value={editSlug}
                              onChange={(e) => setEditSlug(e.target.value)}
                              className="bg-card"
                            />
                          </Field>
                          <div className="grid grid-cols-2 gap-3">
                            <Field className="gap-1">
                              <FieldLabel htmlFor={`m-cat-bps-${cat.id}`}>
                                Hoa hồng (bps)
                              </FieldLabel>
                              <Input
                                id={`m-cat-bps-${cat.id}`}
                                type="number"
                                min={0}
                                max={5000}
                                step={1}
                                value={editBps}
                                onChange={(e) => setEditBps(e.target.value)}
                                className="bg-card tabular-nums"
                              />
                            </Field>
                            <Field className="gap-1">
                              <FieldLabel htmlFor={`m-cat-order-${cat.id}`}>
                                Thứ tự hiển thị
                              </FieldLabel>
                              <Input
                                id={`m-cat-order-${cat.id}`}
                                type="number"
                                min={0}
                                step={1}
                                value={editSortOrder}
                                onChange={(e) => setEditSortOrder(e.target.value)}
                                className="bg-card tabular-nums"
                              />
                            </Field>
                          </div>
                          <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm text-foreground">
                            <Checkbox
                              checked={editIsProhibited}
                              onCheckedChange={(checked) => setEditIsProhibited(Boolean(checked))}
                            />
                            <span>Danh mục hàng cấm</span>
                          </label>
                          <FieldError>{editError}</FieldError>
                          <div className="flex items-center gap-2">
                            <Button
                              type="button"
                              size="sm"
                              disabled={isBusy}
                              onClick={() => void handleSaveEdit(cat.id)}
                            >
                              {isBusy ? "Đang lưu..." : "Lưu"}
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
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
                          <div className="flex flex-wrap items-start justify-between gap-2">
                            <div className="min-w-0">
                              <div className="font-medium text-foreground">{cat.name}</div>
                              <div className="text-xs text-muted-foreground tabular-nums">
                                /{cat.slug} · {cat.commission_rate_bps ?? 0} bps · Thứ tự{" "}
                                {formatInt(cat.sort_order)}
                              </div>
                            </div>
                            <span className="shrink-0 text-sm font-semibold text-foreground tabular-nums">
                              {formatCommissionBps(cat.commission_rate_bps ?? 0)}
                            </span>
                          </div>
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            {cat.is_prohibited ? (
                              <StatusBadge tone="critical">Danh mục hàng cấm</StatusBadge>
                            ) : (
                              <StatusBadge tone="success">Cho phép bán</StatusBadge>
                            )}
                            {canWriteCategories ? (
                              <div className="flex items-center gap-2">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  onClick={() => startEdit(cat)}
                                >
                                  Sửa
                                </Button>
                                <Button
                                  type="button"
                                  variant="destructive"
                                  size="sm"
                                  onClick={() => setConfirmDeleteCat(cat)}
                                >
                                  Xóa
                                </Button>
                              </div>
                            ) : null}
                          </div>
                        </>
                      )}
                    </article>
                  );
                })}
              </div>
            </>
          )}
        </IndexTable>
      </section>

      <ConfirmActionDialog
        open={Boolean(confirmDeleteCat)}
        title="Xác nhận xóa danh mục"
        description={
          confirmDeleteCat
            ? `Xóa danh mục "${confirmDeleteCat.name}" (/${confirmDeleteCat.slug})? Thao tác không thể hoàn tác.`
            : ""
        }
        confirmLabel="Xác nhận xóa"
        confirmVariant="destructive"
        busy={Boolean(confirmDeleteCat && savingId === confirmDeleteCat.id)}
        onCancel={() => setConfirmDeleteCat(null)}
        onConfirm={() => {
          if (confirmDeleteCat) void handleDelete(confirmDeleteCat);
        }}
      />
    </div>
  );
}
