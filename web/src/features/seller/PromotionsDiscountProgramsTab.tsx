"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, Percent, Plus, Trash2 } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexTable,
  MetricStrip,
  StatusBadge,
  type StatusTone,
} from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import type { GwSellerProduct, GwSellerSku, GwShopDiscountProgram } from "./sellerApiTypes";
import { DISCOUNT_PROGRAM_STATUS_META, translateShopDiscountWarning } from "./types";

interface Props {
  shopName: string;
  initialPrograms: GwShopDiscountProgram[];
  products: GwSellerProduct[];
  showForm: boolean;
  onOpenForm: () => void;
  onCloseForm: () => void;
  onNotify: (msg: string) => void;
  onCountChange?: (count: number) => void;
}

interface DraftItemRow {
  sku_id: string;
  discounted_price_vnd: string;
  per_user_limit: string;
}

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function programStatusTone(status: string): StatusTone {
  switch (status.toUpperCase()) {
    case "ACTIVE":
      return "success";
    case "SCHEDULED":
      return "info";
    case "ENDED":
    case "CANCELLED":
    default:
      return "neutral";
  }
}

export function PromotionsDiscountProgramsTab({
  shopName,
  initialPrograms,
  products,
  showForm,
  onOpenForm,
  onCloseForm,
  onNotify,
  onCountChange,
}: Props) {
  const [programs, setPrograms] = useState<GwShopDiscountProgram[]>(initialPrograms);
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<"ALL" | "ACTIVE" | "SCHEDULED" | "ENDED">("ALL");
  const [serverWarnings, setServerWarnings] = useState<string[]>([]);

  // Create / Edit state
  const [editingId, setEditingId] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [startsAt, setStartsAt] = useState(() => toDatetimeLocalValue(new Date()));
  const [endsAt, setEndsAt] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 7 * 24 * 3600 * 1000))
  );
  const [draftItems, setDraftItems] = useState<DraftItemRow[]>([]);
  const [quickPct, setQuickPct] = useState("15");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // End confirmation dialog
  const [confirmEndProgram, setConfirmEndProgram] = useState<GwShopDiscountProgram | null>(null);
  const [endingId, setEndingId] = useState<string | null>(null);

  const skuLookup = useMemo(() => {
    const map = new Map<string, { product: GwSellerProduct; sku: GwSellerSku }>();
    for (const product of products) {
      for (const sku of product.skus ?? []) {
        map.set(sku.id, { product, sku });
      }
    }
    return map;
  }, [products]);

  const selectableSkus = useMemo(
    () =>
      products
        .filter((p) => p.status === "APPROVED" || p.status === "PENDING")
        .flatMap((product) =>
          (product.skus ?? []).filter((s) => s.is_active).map((sku) => ({ product, sku }))
        ),
    [products]
  );

  const loadPrograms = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<{ items?: GwShopDiscountProgram[] }>(
        "/api/seller/discount-programs?page=1&page_size=50"
      );
      const list = res.items ?? [];
      setPrograms(list);
      onCountChange?.(list.length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách chương trình giảm giá."));
    } finally {
      setLoading(false);
    }
  }, [onCountChange]);

  useEffect(() => {
    if (showForm && !editingId && draftItems.length === 0 && selectableSkus.length > 0) {
      const first = selectableSkus[0].sku;
      const suggested = Math.max(1000, Math.trunc((first.price_vnd * 85) / 100));
      setDraftItems([
        {
          sku_id: first.id,
          discounted_price_vnd: String(suggested),
          per_user_limit: "0",
        },
      ]);
    }
  }, [showForm, editingId, draftItems.length, selectableSkus]);

  const resetForm = useCallback(() => {
    setEditingId(null);
    setName("");
    setStartsAt(toDatetimeLocalValue(new Date()));
    setEndsAt(toDatetimeLocalValue(new Date(Date.now() + 7 * 24 * 3600 * 1000)));
    setDraftItems([]);
    setFormError(null);
  }, []);

  useEffect(() => {
    if (!showForm) {
      resetForm();
    }
  }, [showForm, resetForm]);

  const handleStartEdit = (program: GwShopDiscountProgram) => {
    setEditingId(program.id);
    setName(program.name);
    setStartsAt(
      program.starts_at
        ? toDatetimeLocalValue(new Date(program.starts_at))
        : toDatetimeLocalValue(new Date())
    );
    setEndsAt(
      program.ends_at
        ? toDatetimeLocalValue(new Date(program.ends_at))
        : toDatetimeLocalValue(new Date(Date.now() + 7 * 24 * 3600 * 1000))
    );
    setDraftItems(
      (program.items ?? []).map((it) => ({
        sku_id: it.sku_id,
        discounted_price_vnd: String(it.discounted_price_vnd),
        per_user_limit: String(it.per_user_limit ?? 0),
      }))
    );
    setFormError(null);
    onOpenForm();
  };

  const handleAddDraftRow = () => {
    const usedIds = new Set(draftItems.map((r) => r.sku_id));
    const next = selectableSkus.find((x) => !usedIds.has(x.sku.id)) ?? selectableSkus[0];
    if (!next) return;
    const pct = Math.max(1, Math.min(90, Math.trunc(Number(quickPct) || 15)));
    const discounted = Math.max(1000, Math.trunc((next.sku.price_vnd * (100 - pct)) / 100));
    setDraftItems((prev) => [
      ...prev,
      {
        sku_id: next.sku.id,
        discounted_price_vnd: String(discounted),
        per_user_limit: "0",
      },
    ]);
  };

  const handleApplyQuickPct = () => {
    const pct = Math.max(1, Math.min(90, Math.trunc(Number(quickPct) || 15)));
    setDraftItems((prev) =>
      prev.map((row) => {
        const found = skuLookup.get(row.sku_id);
        if (!found) return row;
        const discounted = Math.max(1000, Math.trunc((found.sku.price_vnd * (100 - pct)) / 100));
        return { ...row, discounted_price_vnd: String(discounted) };
      })
    );
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = name.trim();
    if (cleanName.length < 3) {
      setFormError("Tên chương trình giảm giá cần tối thiểu 3 ký tự.");
      return;
    }
    const startDate = new Date(startsAt);
    const endDate = new Date(endsAt);
    if (
      Number.isNaN(startDate.getTime()) ||
      Number.isNaN(endDate.getTime()) ||
      endDate <= startDate
    ) {
      setFormError("Thời điểm kết thúc phải sau thời điểm bắt đầu.");
      return;
    }
    if (draftItems.length === 0) {
      setFormError("Vui lòng chọn ít nhất 1 biến thể SKU tham gia chương trình.");
      return;
    }

    const seenSkus = new Set<string>();
    const payloadItems: Array<{
      sku_id: string;
      discounted_price_vnd: number;
      per_user_limit: number;
    }> = [];

    for (const row of draftItems) {
      if (!row.sku_id) {
        setFormError("Có dòng chưa chọn biến thể SKU.");
        return;
      }
      if (seenSkus.has(row.sku_id)) {
        setFormError("Mỗi biến thể SKU chỉ được khai báo một lần trong cùng chương trình.");
        return;
      }
      seenSkus.add(row.sku_id);

      const discounted = Math.trunc(Number(row.discounted_price_vnd) || 0);
      const perUser = Math.max(0, Math.trunc(Number(row.per_user_limit) || 0));
      const found = skuLookup.get(row.sku_id);
      if (discounted <= 0) {
        setFormError("Giá sau giảm phải là số nguyên VND lớn hơn 0.");
        return;
      }
      if (found && discounted >= found.sku.price_vnd) {
        setFormError(
          `Giá giảm của ${found.sku.sku_code} phải thấp hơn giá gốc (${formatVnd(found.sku.price_vnd)}).`
        );
        return;
      }
      payloadItems.push({
        sku_id: row.sku_id,
        discounted_price_vnd: discounted,
        per_user_limit: perUser,
      });
    }

    setFormError(null);
    setSubmitting(true);
    try {
      const res = editingId
        ? await api<{ program?: GwShopDiscountProgram; warnings?: string[] }>(
            `/api/seller/discount-programs/${encodeURIComponent(editingId)}`,
            {
              method: "PUT",
              body: {
                name: cleanName,
                starts_at: startDate.toISOString(),
                ends_at: endDate.toISOString(),
                items: payloadItems,
                replace_items: true,
              },
            }
          )
        : await api<{ program?: GwShopDiscountProgram; warnings?: string[] }>(
            "/api/seller/discount-programs",
            {
              method: "POST",
              body: {
                name: cleanName,
                starts_at: startDate.toISOString(),
                ends_at: endDate.toISOString(),
                items: payloadItems,
              },
            }
          );

      setServerWarnings(res.warnings ?? []);
      resetForm();
      onCloseForm();
      await loadPrograms();
      onNotify(
        editingId
          ? `Đã cập nhật chương trình "${cleanName}".`
          : `Đã tạo chương trình "${cleanName}" cho ${formatInt(payloadItems.length)} SKU.`
      );
    } catch (err) {
      setFormError(
        formatPromotionErrorMessage(
          err,
          "seller_discount_program",
          "Không thể lưu chương trình giảm giá lúc này."
        )
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleEndProgram = async (program: GwShopDiscountProgram) => {
    setEndingId(program.id);
    try {
      await api<{ program?: GwShopDiscountProgram }>(
        `/api/seller/discount-programs/${encodeURIComponent(program.id)}/end`,
        { method: "POST" }
      );
      setConfirmEndProgram(null);
      await loadPrograms();
      onNotify(`Đã kết thúc chương trình "${program.name}".`);
    } catch (err) {
      onNotify(
        formatPromotionErrorMessage(
          err,
          "seller_discount_program",
          "Không thể kết thúc chương trình giảm giá."
        )
      );
    } finally {
      setEndingId(null);
    }
  };

  const filteredPrograms = useMemo(() => {
    if (statusFilter === "ALL") return programs;
    if (statusFilter === "ENDED") {
      return programs.filter((p) => p.status === "ENDED" || p.status === "CANCELLED");
    }
    return programs.filter((p) => p.status === statusFilter);
  }, [programs, statusFilter]);

  const activeCount = programs.filter((p) => p.status === "ACTIVE").length;
  const scheduledCount = programs.filter((p) => p.status === "SCHEDULED").length;
  const totalSkuItems = programs
    .filter((p) => p.status === "ACTIVE" || p.status === "SCHEDULED")
    .reduce((sum, p) => sum + (p.items?.length ?? 0), 0);

  if (loading && programs.length === 0) {
    return (
      <Card role="status" aria-label="Đang tải chương trình giảm giá gian hàng...">
        <CardContent className="space-y-3 py-6">
          <Skeleton className="h-5 w-48" />
          {Array.from({ length: 4 }).map((_, idx) => (
            <Skeleton key={idx} className="h-10 w-full" />
          ))}
        </CardContent>
      </Card>
    );
  }

  if (errorMsg) {
    return (
      <ErrorState
        title="Không thể tải chương trình giảm giá"
        description={errorMsg}
        onRetry={() => void loadPrograms()}
      />
    );
  }

  return (
    <div className="space-y-4">
      <section aria-label="Tổng hợp chương trình giảm giá shop">
        <MetricStrip
          items={[
            { label: "Đang chạy", value: formatInt(activeCount) },
            { label: "Đã lên lịch", value: formatInt(scheduledCount) },
            { label: "SKU đang tham gia", value: formatInt(totalSkuItems) },
          ]}
        />
      </section>

      {serverWarnings.length > 0 && (
        <Alert>
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertTitle className="flex flex-wrap items-center justify-between gap-2">
            <span>Cảnh báo đối chiếu giá 30 ngày</span>
            <Button type="button" variant="ghost" size="xs" onClick={() => setServerWarnings([])}>
              Ẩn thông báo
            </Button>
          </AlertTitle>
          <AlertDescription>
            <ul className="list-disc space-y-1 pl-4 text-xs">
              {serverWarnings.map((w, idx) => (
                <li key={idx}>{translateShopDiscountWarning(w, skuLookup)}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <section aria-labelledby="shop-discount-list-heading" className="space-y-2.5">
        <h2 id="shop-discount-list-heading" className="text-sm font-semibold text-foreground">
          Chương trình giảm giá của {shopName}
        </h2>

        <IndexTable
          tabs={[
            { key: "ALL", label: "Tất cả", count: programs.length },
            { key: "ACTIVE", label: "Đang chạy" },
            { key: "SCHEDULED", label: "Chờ chạy" },
            { key: "ENDED", label: "Đã kết thúc" },
          ]}
          activeTab={statusFilter}
          onTabChange={(k) => setStatusFilter(k as "ALL" | "ACTIVE" | "SCHEDULED" | "ENDED")}
        >
          {filteredPrograms.length === 0 ? (
            <div className="p-4">
              <EmptyState
                icon={Percent}
                title="Chưa có chương trình giảm giá nào ở trạng thái đã chọn"
                description="Tạo chương trình giảm giá theo SKU để áp giá ưu đãi trực tiếp trên site gian hàng."
                action={
                  <Button size="sm" onClick={onOpenForm}>
                    <Plus className="size-4" aria-hidden="true" />
                    <span>Tạo giảm giá shop</span>
                  </Button>
                }
                className="shadow-none"
              />
            </div>
          ) : (
            <>
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Tên chương trình</TableHead>
                      <TableHead>Thời gian áp dụng</TableHead>
                      <TableHead>SKU &amp; mức giảm</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead className="text-right">Thao tác</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filteredPrograms.map((program) => {
                      const meta = DISCOUNT_PROGRAM_STATUS_META[program.status] ?? {
                        label: program.status,
                        tone: "neutral" as const,
                      };
                      const canModify =
                        program.status === "ACTIVE" || program.status === "SCHEDULED";
                      const hasInflationFlag = (program.items ?? []).some(
                        (it) => it.price_inflation_flagged
                      );
                      return (
                        <TableRow key={program.id}>
                          <TableCell className="max-w-[220px] whitespace-normal">
                            <div className="font-semibold text-foreground">{program.name}</div>
                            <div className="text-xs text-muted-foreground tabular-nums">
                              {formatInt(program.items?.length ?? 0)} biến thể SKU
                            </div>
                            {hasInflationFlag && (
                              <Badge variant="warning" className="mt-1">
                                Cảnh báo tăng giá 30 ngày
                              </Badge>
                            )}
                          </TableCell>
                          <TableCell className="tabular-nums">
                            <div>
                              {program.starts_at ? formatDateTimeVi(program.starts_at) : "—"}
                            </div>
                            <div className="text-xs text-muted-foreground">
                              đến {program.ends_at ? formatDateTimeVi(program.ends_at) : "—"}
                            </div>
                          </TableCell>
                          <TableCell className="max-w-[300px] whitespace-normal">
                            <div className="space-y-1.5">
                              {(program.items ?? []).slice(0, 3).map((it) => {
                                const found = skuLookup.get(it.sku_id);
                                const thumb =
                                  found?.sku.image_url || found?.product.images?.[0] || null;
                                return (
                                  <div key={it.id || it.sku_id} className="flex items-center gap-2">
                                    {thumb && (
                                      <img
                                        src={thumb}
                                        alt={found?.product.name ?? it.sku_id}
                                        width={28}
                                        height={28}
                                        className="size-7 shrink-0 rounded border border-border bg-muted object-cover"
                                      />
                                    )}
                                    <div className="min-w-0 text-xs">
                                      <div className="flex flex-wrap items-center gap-1.5">
                                        <span className="font-medium text-foreground">
                                          {found ? found.sku.sku_code : it.sku_id}:{" "}
                                          <strong className="tabular-nums">
                                            {formatVnd(it.discounted_price_vnd)}
                                          </strong>
                                        </span>
                                        <Badge variant="warning">
                                          {formatDiscountPercent(
                                            it.original_price_vnd,
                                            it.discounted_price_vnd
                                          )}
                                        </Badge>
                                      </div>
                                      <div className="text-muted-foreground tabular-nums">
                                        Gốc {formatVnd(it.original_price_vnd)}
                                        {it.per_user_limit > 0
                                          ? ` · Tối đa ${formatInt(it.per_user_limit)}/khách`
                                          : ""}
                                      </div>
                                    </div>
                                  </div>
                                );
                              })}
                              {(program.items?.length ?? 0) > 3 && (
                                <div className="text-xs text-muted-foreground">
                                  + {formatInt((program.items?.length ?? 0) - 3)} SKU khác
                                </div>
                              )}
                            </div>
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={programStatusTone(program.status)}>
                              {meta.label}
                            </StatusBadge>
                          </TableCell>
                          <TableCell className="text-right">
                            {canModify ? (
                              <div className="inline-flex items-center justify-end gap-1">
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="xs"
                                  onClick={() => handleStartEdit(program)}
                                >
                                  Sửa
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="xs"
                                  className="text-destructive hover:text-destructive"
                                  disabled={endingId === program.id}
                                  onClick={() => setConfirmEndProgram(program)}
                                >
                                  Kết thúc
                                </Button>
                              </div>
                            ) : (
                              <span className="text-xs text-muted-foreground">—</span>
                            )}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              <div className="divide-y divide-border md:hidden">
                {filteredPrograms.map((program) => {
                  const meta = DISCOUNT_PROGRAM_STATUS_META[program.status] ?? {
                    label: program.status,
                    tone: "neutral" as const,
                  };
                  const canModify = program.status === "ACTIVE" || program.status === "SCHEDULED";
                  return (
                    <div key={program.id} className="space-y-2 px-4 py-3">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <div className="font-semibold text-foreground">{program.name}</div>
                          <div className="text-xs text-muted-foreground tabular-nums">
                            {formatInt(program.items?.length ?? 0)} SKU ·{" "}
                            {program.starts_at ? formatDateTimeVi(program.starts_at) : "—"}
                          </div>
                        </div>
                        <StatusBadge tone={programStatusTone(program.status)}>
                          {meta.label}
                        </StatusBadge>
                      </div>
                      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                        <span className="tabular-nums">
                          Hết hạn: {program.ends_at ? formatDateTimeVi(program.ends_at) : "—"}
                        </span>
                        {canModify && (
                          <div className="flex items-center gap-2">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              className="min-h-10"
                              onClick={() => handleStartEdit(program)}
                            >
                              Sửa
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="min-h-10 text-destructive hover:text-destructive"
                              disabled={endingId === program.id}
                              onClick={() => setConfirmEndProgram(program)}
                            >
                              Kết thúc
                            </Button>
                          </div>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </IndexTable>
      </section>

      {/* Create / Edit Discount Program Sheet */}
      <Sheet
        open={showForm}
        onOpenChange={(open) => {
          if (!open) {
            resetForm();
            onCloseForm();
          }
        }}
      >
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-2xl">
          <form
            onSubmit={(e) => void handleSubmit(e)}
            aria-label="Biểu mẫu chương trình giảm giá shop"
            noValidate
            className="flex min-h-full flex-col"
          >
            <SheetHeader>
              <SheetTitle>
                {editingId ? "Chỉnh sửa chương trình giảm giá" : "Tạo chương trình giảm giá shop"}
              </SheetTitle>
              <SheetDescription>
                Mỗi SKU chỉ thuộc 1 chương trình đang chạy tại một thời điểm.
              </SheetDescription>
            </SheetHeader>

            <div className="flex-1 space-y-5 px-4 pb-4">
              {formError ? <FieldError>{formError}</FieldError> : null}

              {selectableSkus.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  Chưa có biến thể SKU đang mở bán để đưa vào chương trình giảm giá.
                </p>
              ) : (
                <>
                  <FieldGroup className="gap-4">
                    <Field>
                      <FieldLabel htmlFor="dp-name">Tên chương trình</FieldLabel>
                      <Input
                        id="dp-name"
                        type="text"
                        value={name}
                        onChange={(e) => setName(e.target.value)}
                        placeholder="Ưu đãi cuối tuần giảm 15%"
                        required
                      />
                    </Field>

                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <Field>
                        <FieldLabel htmlFor="dp-start">Bắt đầu áp dụng</FieldLabel>
                        <Input
                          id="dp-start"
                          type="datetime-local"
                          value={startsAt}
                          onChange={(e) => setStartsAt(e.target.value)}
                          required
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="dp-end">Kết thúc áp dụng</FieldLabel>
                        <Input
                          id="dp-end"
                          type="datetime-local"
                          value={endsAt}
                          onChange={(e) => setEndsAt(e.target.value)}
                          required
                        />
                      </Field>
                    </div>
                  </FieldGroup>

                  <div className="space-y-3 border-t border-border pt-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="text-sm font-semibold text-foreground">
                        Danh sách SKU ({formatInt(draftItems.length)})
                      </span>
                      <div className="flex flex-wrap items-center gap-2">
                        <Input
                          type="number"
                          min={1}
                          max={90}
                          step={1}
                          value={quickPct}
                          onChange={(e) => setQuickPct(e.target.value)}
                          aria-label="Phần trăm giảm nhanh"
                          className="h-8 w-20 text-right tabular-nums"
                        />
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={handleApplyQuickPct}
                        >
                          Tính nhanh -{quickPct}%
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={handleAddDraftRow}
                        >
                          <Plus className="size-3.5" aria-hidden="true" />
                          <span>Thêm SKU</span>
                        </Button>
                      </div>
                    </div>

                    <div className="space-y-3">
                      {draftItems.map((row, idx) => {
                        const found = skuLookup.get(row.sku_id);
                        const regPrice = found?.sku.price_vnd ?? 0;
                        const min30d = found?.sku.min_price_30d_vnd ?? 0;
                        const discVal = Math.trunc(Number(row.discounted_price_vnd) || 0);
                        return (
                          <div
                            key={`${row.sku_id}-${idx}`}
                            className="space-y-3 rounded-lg border border-border bg-muted/35 p-3"
                          >
                            <div className="flex items-start justify-between gap-2">
                              <Field className="flex-1">
                                <FieldLabel htmlFor={`dp-sku-${idx}`}>Biến thể SKU</FieldLabel>
                                <Select
                                  value={row.sku_id}
                                  onValueChange={(nextSkuId) => {
                                    const nextFound = skuLookup.get(nextSkuId);
                                    const pct = Math.max(
                                      1,
                                      Math.min(90, Math.trunc(Number(quickPct) || 15))
                                    );
                                    const suggested = nextFound
                                      ? Math.max(
                                          1000,
                                          Math.trunc((nextFound.sku.price_vnd * (100 - pct)) / 100)
                                        )
                                      : discVal;
                                    setDraftItems((prev) =>
                                      prev.map((item, i) =>
                                        i === idx
                                          ? {
                                              ...item,
                                              sku_id: nextSkuId,
                                              discounted_price_vnd: String(suggested),
                                            }
                                          : item
                                      )
                                    );
                                  }}
                                >
                                  <SelectTrigger id={`dp-sku-${idx}`} className="w-full bg-card">
                                    <SelectValue>
                                      {found
                                        ? `${found.product.name} · ${found.sku.sku_code} (${formatVnd(found.sku.price_vnd)})`
                                        : row.sku_id}
                                    </SelectValue>
                                  </SelectTrigger>
                                  <SelectContent>
                                    {selectableSkus.map(({ product, sku }) => (
                                      <SelectItem key={sku.id} value={sku.id}>
                                        {product.name} · {sku.sku_code} ({formatVnd(sku.price_vnd)})
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                                {found && (
                                  <FieldDescription className="text-xs tabular-nums">
                                    Giá gốc: {formatVnd(regPrice)}
                                    {min30d > 0 ? ` · Thấp nhất 30 ngày: ${formatVnd(min30d)}` : ""}
                                  </FieldDescription>
                                )}
                              </Field>

                              <Button
                                type="button"
                                variant="ghost"
                                size="icon-sm"
                                disabled={draftItems.length <= 1}
                                aria-label="Bỏ SKU"
                                className="mt-6 text-muted-foreground hover:text-destructive"
                                onClick={() =>
                                  setDraftItems((prev) => prev.filter((_, i) => i !== idx))
                                }
                              >
                                <Trash2 className="size-4" aria-hidden="true" />
                              </Button>
                            </div>

                            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                              <Field>
                                <FieldLabel htmlFor={`dp-price-${idx}`}>
                                  Giá sau giảm (VND)
                                </FieldLabel>
                                <Input
                                  id={`dp-price-${idx}`}
                                  type="number"
                                  min={1}
                                  step={1}
                                  value={row.discounted_price_vnd}
                                  onChange={(e) =>
                                    setDraftItems((prev) =>
                                      prev.map((item, i) =>
                                        i === idx
                                          ? { ...item, discounted_price_vnd: e.target.value }
                                          : item
                                      )
                                    )
                                  }
                                  required
                                  className="bg-card tabular-nums"
                                />
                                {regPrice > 0 && discVal > 0 && discVal < regPrice && (
                                  <FieldDescription className="text-xs tabular-nums">
                                    Giảm {formatDiscountPercent(regPrice, discVal)} (tiết kiệm{" "}
                                    {formatVnd(regPrice - discVal)})
                                  </FieldDescription>
                                )}
                              </Field>

                              <Field>
                                <FieldLabel htmlFor={`dp-limit-${idx}`}>
                                  Giới hạn / khách (0 = không giới hạn)
                                </FieldLabel>
                                <Input
                                  id={`dp-limit-${idx}`}
                                  type="number"
                                  min={0}
                                  step={1}
                                  value={row.per_user_limit}
                                  onChange={(e) =>
                                    setDraftItems((prev) =>
                                      prev.map((item, i) =>
                                        i === idx
                                          ? { ...item, per_user_limit: e.target.value }
                                          : item
                                      )
                                    )
                                  }
                                  className="bg-card tabular-nums"
                                />
                              </Field>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                </>
              )}
            </div>

            <SheetFooter className="flex-row justify-end gap-2 border-t border-border">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  resetForm();
                  onCloseForm();
                }}
              >
                Hủy
              </Button>
              {selectableSkus.length > 0 && (
                <Button type="submit" size="sm" disabled={submitting}>
                  {submitting
                    ? "Đang lưu..."
                    : editingId
                      ? "Cập nhật chương trình"
                      : "Lưu chương trình"}
                </Button>
              )}
            </SheetFooter>
          </form>
        </SheetContent>
      </Sheet>

      {/* End Program Confirmation */}
      <AlertDialog
        open={Boolean(confirmEndProgram)}
        onOpenChange={(open) => !open && setConfirmEndProgram(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Kết thúc chương trình &ldquo;{confirmEndProgram?.name}&rdquo;?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Các SKU trong chương trình sẽ quay về giá bán thường ngay lập tức.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Hủy</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={!confirmEndProgram || endingId === confirmEndProgram.id}
              onClick={() => {
                if (confirmEndProgram) {
                  void handleEndProgram(confirmEndProgram);
                }
              }}
            >
              {confirmEndProgram && endingId === confirmEndProgram.id
                ? "Đang dừng..."
                : "Xác nhận dừng"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
