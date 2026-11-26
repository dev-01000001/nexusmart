"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw, Search, ShieldBan } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  StatusBadge,
  productStatusTone,
} from "@/components/app";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type {
  GatewayBannedKeyword,
  GatewayBannedKeywordAction,
  GatewayPreviewBannedKeywordResponse,
  GatewayRescanProductsResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import {
  type RescanAggregateProgress,
  RescanBatchInterruptedError,
  runBannedKeywordRescanBatches,
} from "./bannedKeywordsHelper";
import { getAdminProductStatusMeta } from "./types";
import { useAdminContext } from "./AdminShell";
import {
  AdminConfirmActionDialog,
  AdminDetailField,
  AdminDetailGrid,
} from "./ops/AdminOpsPrimitives";
import { adminBannedActionTone } from "./ops/adminTones";

interface Props {
  onMutated?: () => void;
}

const KEYWORD_CATEGORIES: Array<{ value: string; label: string }> = [
  { value: "COUNTERFEIT", label: "Hàng giả / vi phạm bản quyền" },
  { value: "PROHIBITED", label: "Hàng cấm kinh doanh" },
  { value: "OFF_PLATFORM", label: "Dẫn giao dịch ngoài sàn" },
  { value: "SPAM", label: "Spam / nội dung vi phạm" },
  { value: "OTHER", label: "Nhóm khác" },
];

function formatCategoryLabel(category: string): string {
  const found = KEYWORD_CATEGORIES.find((c) => c.value === category);
  return found ? found.label : category || "Nhóm khác";
}

function formatActionLabel(action: string): string {
  if (action.toUpperCase() === "BLOCK") {
    return "Chặn (BLOCK)";
  }
  return "Gắn cờ duyệt (REVIEW)";
}

export function AdminBannedKeywordsTab({ onMutated }: Props) {
  const { user, showToast } = useAdminContext();
  const canWriteKeywords = roleHasCapability(user.role, "banned_keywords.write");

  const [items, setItems] = useState<GatewayBannedKeyword[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [actionFilter, setActionFilter] = useState<"" | GatewayBannedKeywordAction>("");
  const [includeInactive, setIncludeInactive] = useState(true);

  const loadSeqRef = useRef(0);
  const loadAbortRef = useRef<AbortController | null>(null);

  const [newTerm, setNewTerm] = useState("");
  const [newCategory, setNewCategory] = useState("COUNTERFEIT");
  const [newAction, setNewAction] = useState<GatewayBannedKeywordAction>("BLOCK");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewResult, setPreviewResult] = useState<GatewayPreviewBannedKeywordResponse | null>(
    null
  );

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editTerm, setEditTerm] = useState("");
  const [editCategory, setEditCategory] = useState("COUNTERFEIT");
  const [editAction, setEditAction] = useState<GatewayBannedKeywordAction>("BLOCK");
  const [editActive, setEditActive] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [confirmDeleteKeyword, setConfirmDeleteKeyword] = useState<GatewayBannedKeyword | null>(
    null
  );

  const [dryRun, setDryRun] = useState(true);
  const [rescanning, setRescanning] = useState(false);
  const [rescanStatus, setRescanStatus] = useState<
    "idle" | "running" | "completed" | "stopped" | "interrupted"
  >("idle");
  const [rescanFailedBatch, setRescanFailedBatch] = useState<number | null>(null);
  const [rescanError, setRescanError] = useState<string | null>(null);
  const [rescanProgress, setRescanProgress] = useState<RescanAggregateProgress | null>(null);
  const rescanAbortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(query);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    return () => {
      loadAbortRef.current?.abort();
      rescanAbortRef.current?.abort();
    };
  }, []);

  const loadKeywords = useCallback(async () => {
    const seq = ++loadSeqRef.current;
    loadAbortRef.current?.abort();
    const controller = new AbortController();
    loadAbortRef.current = controller;

    setLoading(true);
    setErrorMsg(null);
    try {
      const qs = new URLSearchParams();
      if (debouncedQuery.trim()) qs.set("query", debouncedQuery.trim());
      if (actionFilter) qs.set("action", actionFilter);
      if (includeInactive) qs.set("include_inactive", "true");
      qs.set("page", "1");
      qs.set("page_size", "100");
      const res = await api<PaginatedResponse<GatewayBannedKeyword>>(
        `/api/admin/banned-keywords?${qs.toString()}`,
        { signal: controller.signal }
      );
      if (controller.signal.aborted || loadSeqRef.current !== seq) return;
      const list = Array.isArray(res?.items) ? res.items : [];
      setItems(list);
      setTotal(res?.total ?? list.length);
    } catch (err) {
      if (controller.signal.aborted || loadSeqRef.current !== seq) return;
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách từ khoá cấm lúc này."));
    } finally {
      if (!controller.signal.aborted && loadSeqRef.current === seq) {
        setLoading(false);
      }
    }
  }, [debouncedQuery, actionFilter, includeInactive]);

  useEffect(() => {
    void loadKeywords();
  }, [loadKeywords]);

  const handlePreview = async () => {
    const clean = newTerm.trim();
    if (!clean) {
      setFormError("Vui lòng nhập cụm từ cần kiểm tra trước khi xem trước ảnh hưởng.");
      return;
    }
    setFormError(null);
    setPreviewLoading(true);
    try {
      const res = await api<GatewayPreviewBannedKeywordResponse>(
        "/api/admin/banned-keywords/preview",
        {
          method: "POST",
          body: { term: clean },
        }
      );
      setPreviewResult(res);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể xem trước sản phẩm bị ảnh hưởng."));
    } finally {
      setPreviewLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = newTerm.trim();
    if (!clean) {
      setFormError("Vui lòng nhập cụm từ cấm.");
      return;
    }
    if (clean.length > 120) {
      setFormError("Cụm từ cấm tối đa 120 ký tự.");
      return;
    }
    setFormError(null);
    setSubmitting(true);
    try {
      await api<{ keyword: GatewayBannedKeyword }>("/api/admin/banned-keywords", {
        method: "POST",
        body: {
          term: clean,
          category: newCategory,
          action: newAction,
        },
      });
      setNewTerm("");
      setPreviewResult(null);
      await loadKeywords();
      onMutated?.();
      showToast(`Đã thêm từ khoá cấm "${clean}".`);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể thêm từ khoá cấm."));
    } finally {
      setSubmitting(false);
    }
  };

  const startEdit = (kw: GatewayBannedKeyword) => {
    setEditingId(kw.id);
    setEditTerm(kw.term);
    setEditCategory(kw.category || "OTHER");
    setEditAction(kw.action.toUpperCase() === "REVIEW" ? "REVIEW" : "BLOCK");
    setEditActive(kw.is_active);
    setEditError(null);
  };

  const handleSaveEdit = async (kwId: string) => {
    const clean = editTerm.trim();
    if (!clean) {
      setEditError("Từ khoá cấm không được để trống.");
      return;
    }
    setEditError(null);
    setBusyId(kwId);
    try {
      await api<{ keyword: GatewayBannedKeyword }>(
        `/api/admin/banned-keywords/${encodeURIComponent(kwId)}`,
        {
          method: "PUT",
          body: {
            term: clean,
            category: editCategory,
            action: editAction,
            is_active: editActive,
          },
        }
      );
      setEditingId(null);
      await loadKeywords();
      onMutated?.();
      showToast(`Đã cập nhật từ khoá cấm "${clean}".`);
    } catch (err) {
      setEditError(getErrorMessage(err, "Không thể cập nhật từ khoá cấm."));
    } finally {
      setBusyId(null);
    }
  };

  const handleToggleActive = async (kw: GatewayBannedKeyword) => {
    setBusyId(kw.id);
    try {
      await api<{ keyword: GatewayBannedKeyword }>(
        `/api/admin/banned-keywords/${encodeURIComponent(kw.id)}`,
        {
          method: "PUT",
          body: {
            term: kw.term,
            category: kw.category,
            action: kw.action,
            is_active: !kw.is_active,
          },
        }
      );
      await loadKeywords();
      onMutated?.();
      showToast(
        !kw.is_active
          ? `Đã bật quy tắc từ khoá "${kw.term}".`
          : `Đã tạm tắt quy tắc từ khoá "${kw.term}".`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể thay đổi trạng thái từ khoá cấm."));
    } finally {
      setBusyId(null);
    }
  };

  const handleDelete = async (kw: GatewayBannedKeyword) => {
    setBusyId(kw.id);
    try {
      await api<{ ok: boolean }>(`/api/admin/banned-keywords/${encodeURIComponent(kw.id)}`, {
        method: "DELETE",
      });
      setConfirmDeleteKeyword(null);
      await loadKeywords();
      onMutated?.();
      showToast(`Đã xóa từ khoá cấm "${kw.term}".`);
    } catch (err) {
      setConfirmDeleteKeyword(null);
      showToast(getErrorMessage(err, "Không thể xóa từ khoá cấm."));
    } finally {
      setBusyId(null);
    }
  };

  const handleStopRescan = () => {
    rescanAbortRef.current?.abort();
  };

  const handleRescan = async () => {
    if (rescanning) return;
    rescanAbortRef.current?.abort();
    const controller = new AbortController();
    rescanAbortRef.current = controller;

    setRescanning(true);
    setRescanStatus("running");
    setRescanFailedBatch(null);
    setRescanError(null);
    setRescanProgress(null);
    try {
      const result = await runBannedKeywordRescanBatches(
        (req, signal) =>
          api<GatewayRescanProductsResponse>("/api/admin/banned-keywords/rescan", {
            method: "POST",
            body: req,
            signal,
          }),
        { dryRun, signal: controller.signal },
        (progress) => setRescanProgress(progress)
      );
      setRescanProgress(result);
      if (result.aborted) {
        setRescanStatus("stopped");
        showToast(`Đã dừng quét sau ${formatInt(result.batches)} lô.`);
      } else {
        setRescanStatus("completed");
        if (!dryRun) onMutated?.();
        showToast(
          dryRun
            ? `Đã chạy thử quét ${formatInt(result.scannedCount)} sản phẩm (${formatInt(result.blockedCount)} chặn, ${formatInt(result.flaggedCount)} gắn cờ).`
            : `Hoàn tất quét ${formatInt(result.scannedCount)} sản phẩm (${formatInt(result.blockedCount)} từ chối, ${formatInt(result.flaggedCount)} đưa về chờ duyệt).`
        );
      }
    } catch (err) {
      if (controller.signal.aborted) {
        setRescanStatus("stopped");
        return;
      }
      if (err instanceof RescanBatchInterruptedError) {
        setRescanProgress(err.partialProgress);
        setRescanFailedBatch(err.failedBatch);
        setRescanStatus("interrupted");
        setRescanError(
          getErrorMessage(err.cause, `Quá trình quét bị gián đoạn ở lô ${err.failedBatch}.`)
        );
      } else {
        setRescanStatus("interrupted");
        setRescanFailedBatch((prev) => prev ?? 1);
        setRescanError(getErrorMessage(err, "Quá trình quét lại sản phẩm gặp lỗi."));
      }
    } finally {
      if (rescanAbortRef.current === controller) {
        rescanAbortRef.current = null;
      }
      setRescanning(false);
    }
  };

  return (
    <div className="space-y-5">
      {canWriteKeywords && (
        <Card aria-labelledby="kw-create-heading">
          <CardHeader className="border-b border-border pb-3">
            <CardTitle id="kw-create-heading">
              Thêm quy tắc từ khoá cấm &amp; xem trước ảnh hưởng
            </CardTitle>
            <CardAction>
              <span className="text-xs text-muted-foreground">Tối đa 120 ký tự</span>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4 pt-4">
            <form
              onSubmit={(e) => void handleCreate(e)}
              noValidate
              className="grid grid-cols-1 gap-4 md:grid-cols-3"
            >
              <Field>
                <FieldLabel htmlFor="kw-new-term">Cụm từ cấm</FieldLabel>
                <Input
                  id="kw-new-term"
                  type="text"
                  value={newTerm}
                  onChange={(e) => {
                    setNewTerm(e.target.value);
                    if (formError) setFormError(null);
                  }}
                  placeholder="Ví dụ: hàng giả, zalo riêng, chuyển khoản ngoài..."
                  maxLength={120}
                />
              </Field>

              <Field>
                <FieldLabel htmlFor="kw-new-category">Nhóm vi phạm</FieldLabel>
                <Select value={newCategory} onValueChange={setNewCategory}>
                  <SelectTrigger id="kw-new-category" className="w-full bg-card">
                    <SelectValue>{formatCategoryLabel(newCategory)}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {KEYWORD_CATEGORIES.map((cat) => (
                      <SelectItem key={cat.value} value={cat.value}>
                        {cat.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <Field>
                <FieldLabel htmlFor="kw-new-action">Hành động áp dụng</FieldLabel>
                <Select
                  value={newAction}
                  onValueChange={(val) => setNewAction(val as GatewayBannedKeywordAction)}
                >
                  <SelectTrigger id="kw-new-action" className="w-full bg-card">
                    <SelectValue>
                      {newAction === "BLOCK"
                        ? "Chặn đăng bán & đánh giá (BLOCK)"
                        : "Gắn cờ chuyển duyệt thủ công (REVIEW)"}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="BLOCK">Chặn đăng bán &amp; đánh giá (BLOCK)</SelectItem>
                    <SelectItem value="REVIEW">Gắn cờ chuyển duyệt thủ công (REVIEW)</SelectItem>
                  </SelectContent>
                </Select>
              </Field>

              {formError && (
                <div className="md:col-span-3">
                  <FieldError>{formError}</FieldError>
                </div>
              )}

              <div className="flex flex-wrap items-center gap-2 md:col-span-3">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={previewLoading || submitting}
                  onClick={() => void handlePreview()}
                >
                  {previewLoading ? "Đang kiểm tra..." : "Xem trước ảnh hưởng"}
                </Button>
                <Button type="submit" size="sm" disabled={submitting || previewLoading}>
                  {submitting ? "Đang lưu..." : "Lưu từ khoá cấm"}
                </Button>
              </div>
            </form>

            {previewResult && (
              <div className="space-y-3 rounded-lg border border-border bg-muted/35 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="text-sm font-semibold text-foreground">
                    Kết quả xem trước: {formatInt(previewResult.affected_count)} sản phẩm bị ảnh
                    hưởng
                  </h3>
                  <span className="text-xs text-muted-foreground tabular-nums">
                    Hiển thị tối đa {formatInt(previewResult.samples?.length ?? 0)} mẫu
                  </span>
                </div>
                {(previewResult.samples?.length ?? 0) === 0 ? (
                  <p className="text-[13px] text-muted-foreground">
                    Không có sản phẩm đang bán hoặc chờ duyệt nào khớp với cụm từ này.
                  </p>
                ) : (
                  <>
                    <div className="hidden overflow-x-auto rounded-lg border border-border bg-card md:block">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>Sản phẩm</TableHead>
                            <TableHead>Trạng thái</TableHead>
                            <TableHead>Từ khớp</TableHead>
                            <TableHead>Mức xử lý</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {previewResult.samples.map((s) => {
                            const statusMeta = getAdminProductStatusMeta(s.status);
                            return (
                              <TableRow key={s.product_id}>
                                <TableCell>
                                  <div className="font-medium text-foreground">{s.name}</div>
                                  <div className="text-xs text-muted-foreground">/{s.slug}</div>
                                </TableCell>
                                <TableCell>
                                  <StatusBadge tone={productStatusTone(s.status)}>
                                    {statusMeta.label}
                                  </StatusBadge>
                                </TableCell>
                                <TableCell>{(s.matched_terms ?? []).join(", ") || "—"}</TableCell>
                                <TableCell>
                                  <StatusBadge tone={adminBannedActionTone(s.highest_action)}>
                                    {formatActionLabel(s.highest_action)}
                                  </StatusBadge>
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>
                    <div className="overflow-hidden rounded-lg border border-border bg-card md:hidden">
                      {previewResult.samples.map((s) => {
                        const statusMeta = getAdminProductStatusMeta(s.status);
                        return (
                          <IndexRowCard
                            key={s.product_id}
                            title={s.name}
                            badge={
                              <StatusBadge tone={adminBannedActionTone(s.highest_action)}>
                                {formatActionLabel(s.highest_action)}
                              </StatusBadge>
                            }
                            meta={
                              <span>
                                Trạng thái: {statusMeta.label} · Khớp:{" "}
                                {(s.matched_terms ?? []).join(", ")}
                              </span>
                            }
                          />
                        );
                      })}
                    </div>
                  </>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {canWriteKeywords && (
        <Card aria-labelledby="kw-rescan-heading">
          <CardHeader className="border-b border-border pb-3">
            <CardTitle id="kw-rescan-heading">
              Quét lại sản phẩm đang bán theo từ khoá cấm
            </CardTitle>
            <CardAction>
              <span className="text-xs text-muted-foreground">Lô 500 sản phẩm</span>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4 pt-4">
            <p className="text-[13px] text-muted-foreground">
              Quy tắc BLOCK chuyển sản phẩm sang từ chối; quy tắc REVIEW chuyển về hàng đợi chờ
              duyệt.
            </p>

            <div className="flex flex-wrap items-center gap-3">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="kw-rescan-dry-run"
                  checked={dryRun}
                  onCheckedChange={(checked) => setDryRun(Boolean(checked))}
                  disabled={rescanning}
                />
                <Label htmlFor="kw-rescan-dry-run" className="cursor-pointer font-normal">
                  Chạy thử (chỉ thống kê, không đổi trạng thái)
                </Label>
              </div>

              <Button
                type="button"
                variant={dryRun ? "outline" : "destructive"}
                size="sm"
                disabled={rescanning}
                onClick={() => void handleRescan()}
              >
                {rescanning
                  ? `Đang quét lô ${rescanProgress ? rescanProgress.batches + 1 : 1}...`
                  : dryRun
                    ? "Chạy thử quét lại"
                    : "Quét lại sản phẩm"}
              </Button>
              {rescanning && (
                <Button type="button" variant="outline" size="sm" onClick={handleStopRescan}>
                  Dừng
                </Button>
              )}
            </div>

            {rescanError && (
              <p className="text-[13px] font-medium text-destructive" role="alert">
                {rescanError}
              </p>
            )}

            {rescanProgress && (
              <div className="space-y-3">
                <AdminDetailGrid className="sm:grid-cols-3">
                  <AdminDetailField label="Tiến độ quét">
                    {rescanning
                      ? `Đang quét (${formatInt(rescanProgress.batches)} lô)`
                      : rescanStatus === "interrupted"
                        ? `Gián đoạn ở lô ${formatInt(rescanFailedBatch ?? rescanProgress.batches + 1)}`
                        : rescanStatus === "stopped"
                          ? `Đã dừng sau ${formatInt(rescanProgress.batches)} lô`
                          : `Hoàn tất (${formatInt(rescanProgress.batches)} lô)`}
                  </AdminDetailField>
                  <AdminDetailField label="Đã quét" mono>
                    {formatInt(rescanProgress.scannedCount)} sản phẩm
                  </AdminDetailField>
                  <AdminDetailField label="Chặn / Gắn cờ duyệt" mono>
                    {formatInt(rescanProgress.blockedCount)} chặn ·{" "}
                    {formatInt(rescanProgress.flaggedCount)} gắn cờ
                  </AdminDetailField>
                </AdminDetailGrid>

                {rescanProgress.samples.length > 0 && (
                  <>
                    <div className="hidden overflow-x-auto rounded-lg border border-border md:block">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>Sản phẩm (tối đa 20 mẫu)</TableHead>
                            <TableHead>Trạng thái trước quét</TableHead>
                            <TableHead>Từ khoá khớp</TableHead>
                            <TableHead>Mức xử lý</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {rescanProgress.samples.map((s) => {
                            const statusMeta = getAdminProductStatusMeta(s.status);
                            return (
                              <TableRow key={s.product_id}>
                                <TableCell>
                                  <div className="font-medium text-foreground">{s.name}</div>
                                  <div className="text-xs text-muted-foreground">/{s.slug}</div>
                                </TableCell>
                                <TableCell>
                                  <StatusBadge tone={productStatusTone(s.status)}>
                                    {statusMeta.label}
                                  </StatusBadge>
                                </TableCell>
                                <TableCell>{(s.matched_terms ?? []).join(", ") || "—"}</TableCell>
                                <TableCell>
                                  <StatusBadge tone={adminBannedActionTone(s.highest_action)}>
                                    {formatActionLabel(s.highest_action)}
                                  </StatusBadge>
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>
                    <div className="overflow-hidden rounded-lg border border-border md:hidden">
                      {rescanProgress.samples.map((s) => {
                        const statusMeta = getAdminProductStatusMeta(s.status);
                        return (
                          <IndexRowCard
                            key={s.product_id}
                            title={s.name}
                            badge={
                              <StatusBadge tone={adminBannedActionTone(s.highest_action)}>
                                {formatActionLabel(s.highest_action)}
                              </StatusBadge>
                            }
                            meta={
                              <span>
                                Trước quét: {statusMeta.label} ·{" "}
                                {(s.matched_terms ?? []).join(", ")}
                              </span>
                            }
                          />
                        );
                      })}
                    </div>
                  </>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      )}

      <section aria-labelledby="kw-list-heading">
        <IndexTable
          toolbar={
            <div className="flex w-full flex-wrap items-center justify-between gap-2">
              <h2 id="kw-list-heading" className="text-sm font-semibold text-foreground">
                Danh sách từ khoá cấm ({formatInt(total)})
              </h2>
              <Button type="button" variant="outline" size="xs" onClick={() => void loadKeywords()}>
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>Làm mới</span>
              </Button>
            </div>
          }
        >
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
              <div className="relative min-w-0 flex-1 sm:max-w-xs">
                <Search
                  className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm từ khoá cấm..."
                  aria-label="Tìm từ khoá cấm"
                  className="h-9 bg-card pl-8"
                />
              </div>

              <Select
                value={actionFilter || "ALL"}
                onValueChange={(val) =>
                  setActionFilter((val === "ALL" ? "" : val) as "" | GatewayBannedKeywordAction)
                }
              >
                <SelectTrigger className="w-full bg-card sm:w-48" aria-label="Lọc theo hành động">
                  <SelectValue>
                    {actionFilter ? formatActionLabel(actionFilter) : "Tất cả hành động"}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="ALL">Tất cả hành động</SelectItem>
                  <SelectItem value="BLOCK">Chặn (BLOCK)</SelectItem>
                  <SelectItem value="REVIEW">Gắn cờ duyệt (REVIEW)</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="flex items-center gap-2">
              <Checkbox
                id="kw-include-inactive"
                checked={includeInactive}
                onCheckedChange={(checked) => setIncludeInactive(Boolean(checked))}
              />
              <Label htmlFor="kw-include-inactive" className="cursor-pointer text-xs font-normal">
                Hiện cả quy tắc đã tắt
              </Label>
            </div>
          </div>

          {editError && (
            <div className="border-b border-border bg-badge-critical-bg/40 px-4 py-2.5">
              <p className="text-[13px] font-medium text-destructive" role="alert">
                {editError}
              </p>
            </div>
          )}

          {loading ? (
            <div className="space-y-2.5 p-5" role="status" aria-live="polite">
              <p className="text-xs text-muted-foreground">Đang tải danh sách từ khoá cấm...</p>
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full rounded-md" />
              ))}
            </div>
          ) : errorMsg ? (
            <div className="p-4">
              <ErrorState
                title="Không thể tải từ khoá cấm"
                description={errorMsg}
                onRetry={() => void loadKeywords()}
                className="shadow-none"
              />
            </div>
          ) : items.length === 0 ? (
            <div className="p-4">
              <EmptyState
                icon={ShieldBan}
                title="Chưa có từ khoá cấm nào khớp bộ lọc hiện tại"
                description="Thêm quy tắc từ khoá mới ở khung trên hoặc mở rộng bộ lọc."
                className="shadow-none"
              />
            </div>
          ) : (
            <>
              <div className="hidden md:block">
                <Table aria-labelledby="kw-list-heading">
                  <TableHeader>
                    <TableRow>
                      <TableHead>Từ khoá</TableHead>
                      <TableHead>Nhóm vi phạm</TableHead>
                      <TableHead>Hành động</TableHead>
                      <TableHead>Hiệu lực</TableHead>
                      <TableHead className="text-right">Thao tác</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {items.map((kw) => {
                      const isEditing = editingId === kw.id;
                      const isBusy = busyId === kw.id;
                      return (
                        <TableRow key={kw.id}>
                          <TableCell className="max-w-[260px] whitespace-normal">
                            {isEditing ? (
                              <Input
                                type="text"
                                className="h-8 bg-card"
                                value={editTerm}
                                onChange={(e) => setEditTerm(e.target.value)}
                                aria-label="Sửa cụm từ cấm"
                              />
                            ) : (
                              <>
                                <div className="font-medium text-foreground">{kw.term}</div>
                                <div className="text-xs text-muted-foreground tabular-nums">
                                  Chuẩn hóa: {kw.normalized_term}
                                  {kw.updated_at ? ` · ${formatDateTimeVi(kw.updated_at)}` : ""}
                                </div>
                              </>
                            )}
                          </TableCell>
                          <TableCell>
                            {isEditing ? (
                              <Select value={editCategory} onValueChange={setEditCategory}>
                                <SelectTrigger
                                  className="h-8 w-52 bg-card"
                                  aria-label="Sửa nhóm vi phạm"
                                >
                                  <SelectValue>{formatCategoryLabel(editCategory)}</SelectValue>
                                </SelectTrigger>
                                <SelectContent>
                                  {KEYWORD_CATEGORIES.map((c) => (
                                    <SelectItem key={c.value} value={c.value}>
                                      {c.label}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            ) : (
                              formatCategoryLabel(kw.category)
                            )}
                          </TableCell>
                          <TableCell>
                            {isEditing ? (
                              <Select
                                value={editAction}
                                onValueChange={(val) =>
                                  setEditAction(val as GatewayBannedKeywordAction)
                                }
                              >
                                <SelectTrigger
                                  className="h-8 w-44 bg-card"
                                  aria-label="Sửa hành động"
                                >
                                  <SelectValue>{formatActionLabel(editAction)}</SelectValue>
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="BLOCK">Chặn (BLOCK)</SelectItem>
                                  <SelectItem value="REVIEW">Gắn cờ duyệt (REVIEW)</SelectItem>
                                </SelectContent>
                              </Select>
                            ) : (
                              <StatusBadge tone={adminBannedActionTone(kw.action)}>
                                {formatActionLabel(kw.action)}
                              </StatusBadge>
                            )}
                          </TableCell>
                          <TableCell>
                            {isEditing ? (
                              <div className="flex items-center gap-2">
                                <Checkbox
                                  id={`kw-edit-active-${kw.id}`}
                                  checked={editActive}
                                  onCheckedChange={(checked) => setEditActive(Boolean(checked))}
                                />
                                <Label
                                  htmlFor={`kw-edit-active-${kw.id}`}
                                  className="cursor-pointer text-xs font-normal"
                                >
                                  {editActive ? "Đang bật" : "Đã tắt"}
                                </Label>
                              </div>
                            ) : canWriteKeywords ? (
                              <div className="flex items-center gap-2">
                                <Checkbox
                                  id={`kw-active-${kw.id}`}
                                  checked={kw.is_active}
                                  disabled={isBusy}
                                  onCheckedChange={() => void handleToggleActive(kw)}
                                  aria-label={`Bật hoặc tắt từ khoá ${kw.term}`}
                                />
                                <Label
                                  htmlFor={`kw-active-${kw.id}`}
                                  className="cursor-pointer text-xs font-normal"
                                >
                                  {kw.is_active ? "Đang bật" : "Đã tắt"}
                                </Label>
                              </div>
                            ) : (
                              <StatusBadge tone={kw.is_active ? "success" : "neutral"}>
                                {kw.is_active ? "Đang bật" : "Đã tắt"}
                              </StatusBadge>
                            )}
                          </TableCell>
                          <TableCell className="text-right">
                            {canWriteKeywords ? (
                              <div className="inline-flex items-center gap-1.5">
                                {isEditing ? (
                                  <>
                                    <Button
                                      type="button"
                                      size="xs"
                                      disabled={isBusy}
                                      onClick={() => void handleSaveEdit(kw.id)}
                                    >
                                      {isBusy ? "Lưu..." : "Lưu"}
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      onClick={() => {
                                        setEditingId(null);
                                        setEditError(null);
                                      }}
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
                                      disabled={isBusy}
                                      onClick={() => startEdit(kw)}
                                    >
                                      Sửa
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="ghost"
                                      size="xs"
                                      disabled={isBusy}
                                      className="text-destructive hover:bg-badge-critical-bg/60 hover:text-destructive"
                                      onClick={() => setConfirmDeleteKeyword(kw)}
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
                {items.map((kw) => {
                  const isEditing = editingId === kw.id;
                  const isBusy = busyId === kw.id;
                  return (
                    <article key={kw.id} className="space-y-2.5 p-4">
                      {isEditing ? (
                        <div className="space-y-3">
                          <Field>
                            <FieldLabel htmlFor={`m-kw-term-${kw.id}`}>Từ khoá cấm</FieldLabel>
                            <Input
                              id={`m-kw-term-${kw.id}`}
                              type="text"
                              value={editTerm}
                              onChange={(e) => setEditTerm(e.target.value)}
                            />
                          </Field>
                          <Field>
                            <FieldLabel htmlFor={`m-kw-cat-${kw.id}`}>Nhóm vi phạm</FieldLabel>
                            <Select value={editCategory} onValueChange={setEditCategory}>
                              <SelectTrigger id={`m-kw-cat-${kw.id}`} className="w-full bg-card">
                                <SelectValue>{formatCategoryLabel(editCategory)}</SelectValue>
                              </SelectTrigger>
                              <SelectContent>
                                {KEYWORD_CATEGORIES.map((c) => (
                                  <SelectItem key={c.value} value={c.value}>
                                    {c.label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </Field>
                          <Field>
                            <FieldLabel htmlFor={`m-kw-act-${kw.id}`}>Hành động</FieldLabel>
                            <Select
                              value={editAction}
                              onValueChange={(val) =>
                                setEditAction(val as GatewayBannedKeywordAction)
                              }
                            >
                              <SelectTrigger id={`m-kw-act-${kw.id}`} className="w-full bg-card">
                                <SelectValue>{formatActionLabel(editAction)}</SelectValue>
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="BLOCK">Chặn (BLOCK)</SelectItem>
                                <SelectItem value="REVIEW">Gắn cờ duyệt (REVIEW)</SelectItem>
                              </SelectContent>
                            </Select>
                          </Field>
                          <div className="flex items-center gap-2">
                            <Checkbox
                              id={`m-kw-edit-active-${kw.id}`}
                              checked={editActive}
                              onCheckedChange={(checked) => setEditActive(Boolean(checked))}
                            />
                            <Label
                              htmlFor={`m-kw-edit-active-${kw.id}`}
                              className="cursor-pointer font-normal"
                            >
                              {editActive ? "Đang bật" : "Đã tắt"}
                            </Label>
                          </div>
                          {editError && <FieldError>{editError}</FieldError>}
                          <div className="flex items-center gap-2">
                            <Button
                              type="button"
                              size="sm"
                              disabled={isBusy}
                              onClick={() => void handleSaveEdit(kw.id)}
                            >
                              {isBusy ? "Lưu..." : "Lưu"}
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
                          <div className="flex items-start justify-between gap-2">
                            <span className="font-medium text-foreground [overflow-wrap:anywhere]">
                              {kw.term}
                            </span>
                            <StatusBadge tone={adminBannedActionTone(kw.action)}>
                              {formatActionLabel(kw.action)}
                            </StatusBadge>
                          </div>
                          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                            <span>
                              {formatCategoryLabel(kw.category)} ·{" "}
                              {kw.is_active ? "Đang bật" : "Đã tắt"}
                            </span>
                            {canWriteKeywords && (
                              <div className="flex items-center gap-1.5">
                                <Checkbox
                                  id={`m-kw-active-${kw.id}`}
                                  checked={kw.is_active}
                                  disabled={isBusy}
                                  onCheckedChange={() => void handleToggleActive(kw)}
                                  aria-label={`Bật hoặc tắt từ khoá ${kw.term}`}
                                />
                                <Label
                                  htmlFor={`m-kw-active-${kw.id}`}
                                  className="cursor-pointer text-xs font-normal"
                                >
                                  Bật
                                </Label>
                              </div>
                            )}
                          </div>
                          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                            <span>Chuẩn hóa: {kw.normalized_term}</span>
                            {canWriteKeywords && (
                              <div className="flex items-center gap-1.5">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="xs"
                                  disabled={isBusy}
                                  onClick={() => startEdit(kw)}
                                >
                                  Sửa
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="xs"
                                  disabled={isBusy}
                                  className="text-destructive hover:bg-badge-critical-bg/60 hover:text-destructive"
                                  onClick={() => setConfirmDeleteKeyword(kw)}
                                >
                                  Xóa
                                </Button>
                              </div>
                            )}
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

      <AdminConfirmActionDialog
        open={Boolean(confirmDeleteKeyword)}
        title="Xác nhận xóa từ khoá cấm"
        description={
          confirmDeleteKeyword
            ? `Xóa quy tắc từ khoá cấm "${confirmDeleteKeyword.term}"? Sản phẩm mới sẽ không còn bị lọc theo cụm từ này.`
            : ""
        }
        confirmLabel="Xác nhận xóa"
        cancelLabel="Giữ lại"
        confirmVariant="destructive"
        busy={Boolean(confirmDeleteKeyword && busyId === confirmDeleteKeyword.id)}
        onCancel={() => setConfirmDeleteKeyword(null)}
        onConfirm={() => {
          if (confirmDeleteKeyword) void handleDelete(confirmDeleteKeyword);
        }}
      />
    </div>
  );
}
