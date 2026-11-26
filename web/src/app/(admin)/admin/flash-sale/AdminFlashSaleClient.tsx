"use client";

import React, { Suspense, useCallback, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { AlertCircle, ArrowLeft, Info, Plus, RefreshCw, Search, Ticket, Zap } from "lucide-react";
import {
  DetailLayout,
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  Money,
  PageHeader,
  SaveBar,
  StatusBadge,
  type StatusTone,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import {
  formatDateTimeVi,
  formatInt,
  formatVnIsoDate,
  formatVnd,
  formatVoucherBenefit,
} from "@/lib/format";
import type { GatewayCategory, GatewayVoucher, PaginatedResponse } from "@/lib/gateway-types";
import { localVnInputToIso } from "@/features/admin/adminMeta";
import type {
  GwAdminFlashSaleExtended,
  GwAdminFlashSaleItemExtended,
} from "@/features/admin/adminApiTypes";
import { getVoucherScopeMeta } from "@/features/admin/adminComplaintsTypes";
import {
  canEndFlashSaleEarly,
  computeFlashDiscountPercent,
  formatFlashSaleItemsNotice,
  getFlashSaleWindowMeta,
  loadAdminFlashSaleItems,
  MAX_FLASH_SALE_DISCOUNT_PERCENT,
  mergeFlashSaleItems,
  validateCreateFlashSaleInput,
  type FlashSaleItemsLoadMeta,
} from "@/features/admin/AdminFlashSaleHelpers";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import {
  ConfirmActionDialog,
  DetailGrid,
  DetailItem,
  FilterSelect,
  flashItemStatusTone,
  flashWindowStateTone,
  pillToneToStatusTone,
} from "@/features/admin/config/adminUiHelpers";

type FlashTab = "slots" | "windows" | "vouchers" | "coins";
type ItemFilter = "all" | "PENDING" | "APPROVED" | "REJECTED";
type WindowStatusFilter = "" | "UPCOMING" | "ACTIVE" | "ENDED";

const REJECT_REASON = "Giá Flash sale hoặc số suất chưa phù hợp với khung giờ này";

export interface AdminFlashSaleClientProps {
  initialFlashSales: GwAdminFlashSaleExtended[];
  shopNames: Record<string, string>;
  categories?: GatewayCategory[];
  initialVouchers?: GatewayVoucher[];
  loadError: string | null;
  initialItemsError?: string | null;
  initialItemsMeta?: FlashSaleItemsLoadMeta | null;
  serverNowMs?: number;
}

interface CreateFieldErrors {
  name?: string;
  start?: string;
  end?: string;
  minDiscount?: string;
  categories?: string;
}

function defaultWindowRange(baseMs: number): { startLocal: string; endLocal: string } {
  const datePart = formatVnIsoDate(baseMs + 24 * 3600_000);
  return {
    startLocal: `${datePart}T12:00`,
    endLocal: `${datePart}T14:00`,
  };
}

function getItemStatusMeta(status: string): { label: string; tone: StatusTone } {
  const upper = status.toUpperCase();
  switch (upper) {
    case "APPROVED":
      return { label: "Đã duyệt suất", tone: flashItemStatusTone(upper) };
    case "REJECTED":
      return { label: "Đã từ chối", tone: flashItemStatusTone(upper) };
    case "PENDING":
      return { label: "Chờ duyệt", tone: flashItemStatusTone(upper) };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

function getVoucherStatusBadge(v: GatewayVoucher): { label: string; tone: StatusTone } {
  const st = (v.status ?? "").toUpperCase();
  if (st === "DELETED") return { label: "Đã gỡ", tone: "critical" };
  if (st === "PAUSED" || !v.is_active) return { label: "Tạm dừng", tone: "neutral" };
  return { label: "Đang hiệu lực", tone: "success" };
}

type SheetState =
  | { kind: "slot"; flashSaleId: string; itemId: string }
  | { kind: "window"; flashSaleId: string }
  | null;

function FlashSaleWorkspace({
  initialFlashSales,
  shopNames,
  categories = [],
  initialVouchers = [],
  loadError,
  initialItemsError = null,
  initialItemsMeta = null,
  serverNowMs,
  initialQuery,
}: AdminFlashSaleClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canWriteFlashSales = roleHasCapability(user.role, "flash_sales.write");
  const canApproveItems = roleHasCapability(user.role, "flash_sales.items.approve");
  const canReadVouchers = roleHasCapability(user.role, "vouchers.read");

  const [flashSales, setFlashSales] = useState<GwAdminFlashSaleExtended[]>(initialFlashSales);
  const [itemsError, setItemsError] = useState<string | null>(initialItemsError);
  const [itemsMeta, setItemsMeta] = useState<FlashSaleItemsLoadMeta | null>(initialItemsMeta);
  const [vouchers, setVouchers] = useState<GatewayVoucher[]>(initialVouchers);
  const [vouchersLoading, setVouchersLoading] = useState(false);

  const initialTotalItems = initialFlashSales.reduce((s, f) => s + (f.items?.length ?? 0), 0);
  const [activeTab, setActiveTab] = useState<FlashTab>(
    initialTotalItems > 0 || Boolean(initialItemsError) ? "slots" : "windows"
  );
  const [itemFilter, setItemFilter] = useState<ItemFilter>("all");
  const [windowStatusFilter, setWindowStatusFilter] = useState<WindowStatusFilter>("");
  const [windowsLoading, setWindowsLoading] = useState(false);
  const [query, setQuery] = useState(initialQuery);
  const [now] = useState(() => serverNowMs ?? Date.now());

  // A3 Create form view state (inline A3 view with SaveBar + per-field FieldError)
  const [mode, setMode] = useState<"list" | "create">("list");
  const defaultRange = useMemo(() => defaultWindowRange(now), [now]);
  const [name, setName] = useState("");
  const [startLocal, setStartLocal] = useState(defaultRange.startLocal);
  const [endLocal, setEndLocal] = useState(defaultRange.endLocal);
  const [minDiscountPercent, setMinDiscountPercent] = useState("10");
  const [selectedCategoryIds, setSelectedCategoryIds] = useState<string[]>([]);
  const [createDirty, setCreateDirty] = useState(false);
  const [creating, setCreating] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<CreateFieldErrors>({});
  const [submitError, setSubmitError] = useState<string | null>(null);

  const nameInputRef = useRef<HTMLInputElement | null>(null);
  const startInputRef = useRef<HTMLInputElement | null>(null);
  const endInputRef = useRef<HTMLInputElement | null>(null);
  const minDiscountInputRef = useRef<HTMLInputElement | null>(null);

  // Sheet inspection state
  const [sheet, setSheet] = useState<SheetState>(null);

  const [busyItemId, setBusyItemId] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<"approve" | "reject" | null>(null);
  const [rejectDraft, setRejectDraft] = useState("");

  const [confirmEndWindow, setConfirmEndWindow] = useState<GwAdminFlashSaleExtended | null>(null);
  const [endingWindow, setEndingWindow] = useState(false);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) map.set(c.id, c.name);
    return map;
  }, [categories]);

  const resetCreateForm = useCallback(() => {
    setName("");
    setStartLocal(defaultRange.startLocal);
    setEndLocal(defaultRange.endLocal);
    setMinDiscountPercent("10");
    setSelectedCategoryIds([]);
    setFieldErrors({});
    setSubmitError(null);
    setCreateDirty(false);
  }, [defaultRange]);

  const openCreateView = () => {
    resetCreateForm();
    setMode("create");
  };

  const closeCreateView = () => {
    resetCreateForm();
    setMode("list");
  };

  const reloadFlashSales = useCallback(
    async (statusFilter: WindowStatusFilter) => {
      setWindowsLoading(true);
      try {
        const qs = new URLSearchParams({ page: "1", page_size: "50" });
        if (statusFilter) qs.set("status", statusFilter);
        const [salesSettled, itemsSettled] = await Promise.allSettled([
          api<PaginatedResponse<GwAdminFlashSaleExtended>>(
            `/api/admin/flash-sales?${qs.toString()}`
          ),
          loadAdminFlashSaleItems(({ status, page, pageSize }) => {
            const itemQs = new URLSearchParams({
              page: String(page),
              page_size: String(pageSize),
            });
            if (status) itemQs.set("status", status);
            return api<PaginatedResponse<GwAdminFlashSaleItemExtended>>(
              `/api/admin/flash-sale-items?${itemQs.toString()}`
            );
          }),
        ]);

        let itemsLoaded: Awaited<
          ReturnType<typeof loadAdminFlashSaleItems<GwAdminFlashSaleItemExtended>>
        > | null = null;
        if (itemsSettled.status === "fulfilled") {
          itemsLoaded = itemsSettled.value;
          setItemsMeta(itemsLoaded.meta);
          setItemsError(itemsLoaded.error);
        } else {
          setItemsError(
            getErrorMessage(
              itemsSettled.reason,
              "Không thể tải danh sách suất đăng ký Flash sale lúc này."
            )
          );
        }

        if (salesSettled.status === "fulfilled") {
          const nextSales = salesSettled.value.items ?? [];
          setFlashSales(
            itemsLoaded ? mergeFlashSaleItems(nextSales, itemsLoaded.items) : nextSales
          );
        } else {
          showToast(
            getErrorMessage(salesSettled.reason, "Không thể tải danh sách khung giờ Flash sale.")
          );
          if (itemsLoaded && itemsLoaded.items.length > 0) {
            setFlashSales((prev) => mergeFlashSaleItems(prev, itemsLoaded.items));
          }
        }
      } finally {
        setWindowsLoading(false);
      }
    },
    [showToast]
  );

  const reloadVouchers = useCallback(async () => {
    if (!canReadVouchers) return;
    setVouchersLoading(true);
    try {
      const res = await api<{ vouchers: GatewayVoucher[] }>(
        "/api/admin/vouchers?include_inactive=true"
      );
      setVouchers(res.vouchers ?? []);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tải danh sách voucher sàn."));
    } finally {
      setVouchersLoading(false);
    }
  }, [canReadVouchers, showToast]);

  const entries = useMemo(
    () => flashSales.flatMap((fs) => (fs.items ?? []).map((item) => ({ flashSale: fs, item }))),
    [flashSales]
  );

  const counts = useMemo(() => {
    const byStatus = { PENDING: 0, APPROVED: 0, REJECTED: 0 };
    for (const { item } of entries) {
      const s = item.status.toUpperCase();
      if (s === "PENDING" || s === "APPROVED" || s === "REJECTED") byStatus[s] += 1;
    }
    const live = flashSales.filter(
      (fs) => getFlashSaleWindowMeta(fs, now).state === "ACTIVE"
    ).length;
    return { ...byStatus, all: entries.length, live };
  }, [entries, flashSales, now]);

  const shopLabel = useCallback(
    (shopId: string) => shopNames[shopId] ?? `Gian hàng ${shopId.slice(0, 8)}`,
    [shopNames]
  );

  const visibleEntries = useMemo(() => {
    const q = query.trim().toLowerCase();
    return entries.filter(({ flashSale, item }) => {
      if (itemFilter !== "all" && item.status.toUpperCase() !== itemFilter) return false;
      if (!q) return true;
      return (
        shopLabel(item.shop_id).toLowerCase().includes(q) ||
        flashSale.name.toLowerCase().includes(q) ||
        item.sku_id.toLowerCase().includes(q) ||
        item.id.toLowerCase().includes(q)
      );
    });
  }, [entries, itemFilter, query, shopLabel]);

  const visibleWindows = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return flashSales;
    return flashSales.filter(
      (fs) => fs.name.toLowerCase().includes(q) || fs.id.toLowerCase().includes(q)
    );
  }, [flashSales, query]);

  const visibleVouchers = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return vouchers;
    return vouchers.filter(
      (v) =>
        v.code.toLowerCase().includes(q) ||
        v.name.toLowerCase().includes(q) ||
        v.scope.toLowerCase().includes(q)
    );
  }, [vouchers, query]);

  const toggleCategory = (catId: string) => {
    setCreateDirty(true);
    setSelectedCategoryIds((prev) =>
      prev.includes(catId) ? prev.filter((id) => id !== catId) : [...prev, catId]
    );
    if (fieldErrors.categories) {
      setFieldErrors((prev) => ({ ...prev, categories: undefined }));
    }
  };

  const submitCreateForm = async () => {
    if (creating || !canWriteFlashSales) return;
    const startsIso = localVnInputToIso(startLocal);
    const endsIso = localVnInputToIso(endLocal);
    const nextErrors: CreateFieldErrors = {};

    const trimmedName = name.trim();
    if (trimmedName.length < 2 || trimmedName.length > 160) {
      nextErrors.name = "Tên khung giờ cần từ 2 đến 160 ký tự.";
    }
    if (!startsIso) {
      nextErrors.start = "Vui lòng chọn thời gian bắt đầu hợp lệ.";
    }
    if (!endsIso) {
      nextErrors.end = "Vui lòng chọn thời gian kết thúc hợp lệ.";
    } else if (startsIso && Date.parse(endsIso) <= Date.parse(startsIso)) {
      nextErrors.end = "Thời gian kết thúc phải sau thời gian bắt đầu.";
    }

    const parsedDiscount = minDiscountPercent === "" ? 0 : Number(minDiscountPercent);
    if (
      !Number.isInteger(parsedDiscount) ||
      parsedDiscount < 0 ||
      parsedDiscount > MAX_FLASH_SALE_DISCOUNT_PERCENT
    ) {
      nextErrors.minDiscount = `Mức giảm tối thiểu phải là số nguyên từ 0 đến ${MAX_FLASH_SALE_DISCOUNT_PERCENT}%.`;
    }

    const validated = validateCreateFlashSaleInput({
      name,
      starts_at: startsIso,
      ends_at: endsIso,
      min_discount_percent: parsedDiscount,
      category_ids: selectedCategoryIds,
    });

    if (!validated.ok) {
      if (validated.code === "INVALID_FLASH_SALE_NAME" && !nextErrors.name) {
        nextErrors.name = validated.message;
      } else if (
        validated.code === "INVALID_FLASH_SALE_WINDOW" &&
        !nextErrors.start &&
        !nextErrors.end
      ) {
        nextErrors.end = validated.message;
      } else if (validated.code === "INVALID_MIN_DISCOUNT" && !nextErrors.minDiscount) {
        nextErrors.minDiscount = validated.message;
      } else if (validated.code === "INVALID_CATEGORY_IDS") {
        nextErrors.categories = validated.message;
      }
      setFieldErrors(nextErrors);
      setSubmitError(null);
      if (nextErrors.name) {
        nameInputRef.current?.focus();
      } else if (nextErrors.start) {
        startInputRef.current?.focus();
      } else if (nextErrors.end) {
        endInputRef.current?.focus();
      } else if (nextErrors.minDiscount) {
        minDiscountInputRef.current?.focus();
      }
      return;
    }

    setFieldErrors({});
    setSubmitError(null);
    setCreating(true);
    try {
      const res = await api<{ flash_sale: GwAdminFlashSaleExtended }>("/api/admin/flash-sales", {
        method: "POST",
        body: validated.value,
      });
      const created: GwAdminFlashSaleExtended = {
        ...res.flash_sale,
        items: res.flash_sale.items ?? [],
      };
      setFlashSales((prev) => [created, ...prev]);
      resetCreateForm();
      setMode("list");
      setActiveTab("windows");
      showToast(`Đã mở khung giờ "${created.name}".`);
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể tạo khung giờ Flash sale lúc này.");
      setSubmitError(msg);
      showToast(msg);
    } finally {
      setCreating(false);
    }
  };

  const handleCreateSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    void submitCreateForm();
  };

  const handleConfirmEndEarly = async () => {
    if (!confirmEndWindow || endingWindow || !canWriteFlashSales) return;
    setEndingWindow(true);
    try {
      const res = await api<{
        flash_sale?: GwAdminFlashSaleExtended;
        window?: GwAdminFlashSaleExtended;
        released_items?: number;
      }>(`/api/admin/flash-sales/${encodeURIComponent(confirmEndWindow.id)}/end`, {
        method: "POST",
      });
      const updated = res.flash_sale ?? res.window;
      setFlashSales((prev) =>
        prev.map((fs) =>
          fs.id === confirmEndWindow.id
            ? {
                ...fs,
                ...(updated ?? {}),
                status: updated?.status ?? "ENDED",
                ends_at: updated?.ends_at ?? new Date().toISOString(),
              }
            : fs
        )
      );
      const released = res.released_items ?? 0;
      showToast(
        released > 0
          ? `Đã kết thúc sớm "${confirmEndWindow.name}" và giải phóng ${formatInt(released)} suất.`
          : `Đã kết thúc sớm khung giờ "${confirmEndWindow.name}".`
      );
      setConfirmEndWindow(null);
      router.refresh();
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể kết thúc sớm khung giờ lúc này."));
    } finally {
      setEndingWindow(false);
    }
  };

  const handleDecision = async (
    item: GwAdminFlashSaleItemExtended,
    approve: boolean,
    customReason?: string
  ) => {
    if (busyItemId || !canApproveItems) return;
    setBusyItemId(item.id);
    setBusyAction(approve ? "approve" : "reject");
    try {
      const reason = approve ? "" : customReason?.trim() || REJECT_REASON;
      const res = await api<{ item: GwAdminFlashSaleItemExtended }>(
        `/api/admin/flash-sale-items/${encodeURIComponent(item.id)}/approve`,
        { method: "POST", body: { approve, reason } }
      );
      setFlashSales((prev) =>
        prev.map((fs) => ({
          ...fs,
          items: (fs.items ?? []).map((it) => (it.id === item.id ? { ...it, ...res.item } : it)),
        }))
      );
      setRejectDraft("");
      showToast(
        approve
          ? `Đã duyệt suất của ${shopLabel(item.shop_id)}.`
          : `Đã từ chối suất của ${shopLabel(item.shop_id)}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật trạng thái suất Flash sale."));
    } finally {
      setBusyItemId(null);
      setBusyAction(null);
    }
  };

  if (loadError) {
    return (
      <div className="space-y-5">
        <PageHeader title="Flash sale" />
        <ErrorState
          title="Không thể tải khung giờ Flash sale"
          description={loadError}
          onRetry={() => router.refresh()}
        />
      </div>
    );
  }

  // --- A3: CREATE FLASH SALE WINDOW VIEW ---
  if (mode === "create" && canWriteFlashSales) {
    const startPreviewIso = localVnInputToIso(startLocal);
    const endPreviewIso = localVnInputToIso(endLocal);
    const parsedMinDiscount = Number(minDiscountPercent);

    return (
      <div className="space-y-5">
        <SaveBar
          visible={createDirty}
          saving={creating}
          message="Khung giờ Flash sale chưa lưu"
          onDiscard={closeCreateView}
          onSave={() => void submitCreateForm()}
        />

        <PageHeader
          title="Mở khung giờ Flash sale"
          subtitle="Thiết lập thời gian (Giờ Việt Nam GMT+7) và điều kiện tham gia cho gian hàng."
          secondaryActions={
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={creating}
              onClick={closeCreateView}
            >
              <ArrowLeft className="size-4" aria-hidden="true" />
              <span>Quay lại danh sách</span>
            </Button>
          }
          primaryAction={
            <Button
              type="button"
              size="sm"
              disabled={creating}
              onClick={() => void submitCreateForm()}
            >
              {creating ? "Đang tạo..." : "Mở khung giờ"}
            </Button>
          }
        />

        {submitError ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertDescription>{submitError}</AlertDescription>
          </Alert>
        ) : null}

        <form onSubmit={handleCreateSubmit} noValidate>
          <DetailLayout
            main={
              <>
                <Card>
                  <CardHeader>
                    <CardTitle>Thông tin khung giờ</CardTitle>
                    <CardDescription>
                      Tên và mốc thời gian hiển thị công khai trên trang Flash sale của gian hàng.
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    <FieldGroup className="gap-4">
                      <Field data-invalid={Boolean(fieldErrors.name)}>
                        <FieldLabel htmlFor="fs-name">Tên khung giờ</FieldLabel>
                        <Input
                          ref={nameInputRef}
                          id="fs-name"
                          type="text"
                          value={name}
                          aria-invalid={Boolean(fieldErrors.name)}
                          onChange={(e) => {
                            setName(e.target.value);
                            setCreateDirty(true);
                            if (fieldErrors.name) {
                              setFieldErrors((prev) => ({ ...prev, name: undefined }));
                            }
                            if (submitError) setSubmitError(null);
                          }}
                          placeholder="VD: Flash sale trưa 12h đến 14h"
                          className="bg-card"
                        />
                        <FieldError>{fieldErrors.name}</FieldError>
                      </Field>

                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <Field data-invalid={Boolean(fieldErrors.start)}>
                          <FieldLabel htmlFor="fs-start">Bắt đầu</FieldLabel>
                          <Input
                            ref={startInputRef}
                            id="fs-start"
                            type="datetime-local"
                            value={startLocal}
                            aria-invalid={Boolean(fieldErrors.start)}
                            onChange={(e) => {
                              setStartLocal(e.target.value);
                              setCreateDirty(true);
                              if (fieldErrors.start || fieldErrors.end) {
                                setFieldErrors((prev) => ({
                                  ...prev,
                                  start: undefined,
                                  end: undefined,
                                }));
                              }
                            }}
                            className="bg-card tabular-nums"
                          />
                          <FieldError>{fieldErrors.start}</FieldError>
                        </Field>

                        <Field data-invalid={Boolean(fieldErrors.end)}>
                          <FieldLabel htmlFor="fs-end">Kết thúc</FieldLabel>
                          <Input
                            ref={endInputRef}
                            id="fs-end"
                            type="datetime-local"
                            value={endLocal}
                            aria-invalid={Boolean(fieldErrors.end)}
                            onChange={(e) => {
                              setEndLocal(e.target.value);
                              setCreateDirty(true);
                              if (fieldErrors.end) {
                                setFieldErrors((prev) => ({ ...prev, end: undefined }));
                              }
                            }}
                            className="bg-card tabular-nums"
                          />
                          <FieldError>{fieldErrors.end}</FieldError>
                        </Field>
                      </div>
                    </FieldGroup>
                  </CardContent>
                </Card>

                <Card>
                  <CardHeader>
                    <CardTitle>Điều kiện tham gia</CardTitle>
                    <CardDescription>
                      Giới hạn mức giảm tối thiểu và danh mục được phép đăng ký SKU.
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    <FieldGroup className="gap-5">
                      <Field data-invalid={Boolean(fieldErrors.minDiscount)}>
                        <FieldLabel htmlFor="fs-min-discount">Mức giảm tối thiểu (%)</FieldLabel>
                        <Input
                          ref={minDiscountInputRef}
                          id="fs-min-discount"
                          type="number"
                          min={0}
                          max={MAX_FLASH_SALE_DISCOUNT_PERCENT}
                          step={1}
                          value={minDiscountPercent}
                          aria-invalid={Boolean(fieldErrors.minDiscount)}
                          onChange={(e) => {
                            setMinDiscountPercent(e.target.value);
                            setCreateDirty(true);
                            if (fieldErrors.minDiscount) {
                              setFieldErrors((prev) => ({ ...prev, minDiscount: undefined }));
                            }
                          }}
                          className="max-w-44 bg-card tabular-nums"
                        />
                        <FieldDescription>
                          Từ 0% đến {MAX_FLASH_SALE_DISCOUNT_PERCENT}%. Nhập 0 nếu không giới hạn
                          mức giảm tối thiểu.
                        </FieldDescription>
                        <FieldError>{fieldErrors.minDiscount}</FieldError>
                      </Field>

                      {categories.length > 0 ? (
                        <Field data-invalid={Boolean(fieldErrors.categories)}>
                          <FieldLabel>
                            Danh mục áp dụng ({formatInt(selectedCategoryIds.length)} đã chọn)
                          </FieldLabel>
                          <div className="grid max-h-56 grid-cols-1 gap-2 overflow-y-auto rounded-lg border border-border bg-muted/35 p-3 sm:grid-cols-2">
                            {categories.map((cat) => {
                              const checked = selectedCategoryIds.includes(cat.id);
                              const boxId = `fs-cat-${cat.id}`;
                              return (
                                <label
                                  key={cat.id}
                                  htmlFor={boxId}
                                  className="flex min-h-9 cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-[13px] text-foreground transition-colors hover:bg-accent/55"
                                >
                                  <Checkbox
                                    id={boxId}
                                    checked={checked}
                                    onCheckedChange={() => toggleCategory(cat.id)}
                                  />
                                  <span className="min-w-0 truncate">{cat.name}</span>
                                </label>
                              );
                            })}
                          </div>
                          <FieldDescription>
                            Để trống nếu cho phép tất cả danh mục tham gia khung giờ.
                          </FieldDescription>
                          <FieldError>{fieldErrors.categories}</FieldError>
                        </Field>
                      ) : null}
                    </FieldGroup>
                  </CardContent>
                </Card>
              </>
            }
            aside={
              <Card>
                <CardHeader>
                  <CardTitle>Tóm tắt khung giờ</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4 text-[13px]">
                  <div className="space-y-2.5">
                    <div className="flex items-start justify-between gap-2">
                      <span className="text-muted-foreground">Tên</span>
                      <span className="text-right font-medium text-foreground">
                        {name.trim() || "Chưa đặt tên"}
                      </span>
                    </div>
                    <Separator />
                    <div className="flex items-start justify-between gap-2">
                      <span className="text-muted-foreground">Bắt đầu</span>
                      <span className="text-right tabular-nums">
                        {startPreviewIso ? formatDateTimeVi(startPreviewIso) : "Chưa chọn"}
                      </span>
                    </div>
                    <div className="flex items-start justify-between gap-2">
                      <span className="text-muted-foreground">Kết thúc</span>
                      <span className="text-right tabular-nums">
                        {endPreviewIso ? formatDateTimeVi(endPreviewIso) : "Chưa chọn"}
                      </span>
                    </div>
                    <Separator />
                    <div className="flex items-start justify-between gap-2">
                      <span className="text-muted-foreground">Giảm tối thiểu</span>
                      <span className="font-medium tabular-nums">
                        {Number.isFinite(parsedMinDiscount)
                          ? `${formatInt(parsedMinDiscount)}%`
                          : "—"}
                      </span>
                    </div>
                    <div className="flex items-start justify-between gap-2">
                      <span className="text-muted-foreground">Danh mục</span>
                      <span className="text-right">
                        {selectedCategoryIds.length > 0
                          ? `${formatInt(selectedCategoryIds.length)} danh mục`
                          : "Mọi danh mục"}
                      </span>
                    </div>
                  </div>

                  <Separator />

                  <div className="flex flex-col gap-2">
                    <Button type="submit" disabled={creating} className="w-full">
                      {creating ? "Đang tạo..." : "Mở khung giờ"}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      disabled={creating}
                      onClick={closeCreateView}
                      className="w-full"
                    >
                      Hủy
                    </Button>
                  </div>
                </CardContent>
              </Card>
            }
          />
        </form>
      </div>
    );
  }

  // --- A1: LIST VIEW ---
  const decisionButtons = (item: GwAdminFlashSaleItemExtended) => {
    if (!canApproveItems) return <span className="text-xs text-muted-foreground">Chỉ xem</span>;
    const status = item.status.toUpperCase();
    const anyBusy = Boolean(busyItemId);
    return (
      <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
        <Button
          type="button"
          size="xs"
          variant="outline"
          disabled={anyBusy || status === "APPROVED"}
          onClick={(e) => {
            e.stopPropagation();
            void handleDecision(item, true);
          }}
        >
          Duyệt suất
        </Button>
        <Button
          type="button"
          size="xs"
          variant="destructive"
          disabled={anyBusy || status === "REJECTED"}
          onClick={(e) => {
            e.stopPropagation();
            void handleDecision(item, false);
          }}
        >
          Từ chối
        </Button>
      </div>
    );
  };

  const sheetSlotData =
    sheet?.kind === "slot"
      ? (entries.find((e) => e.flashSale.id === sheet.flashSaleId && e.item.id === sheet.itemId) ??
        null)
      : null;

  const sheetWindowData =
    sheet?.kind === "window"
      ? (flashSales.find((fs) => fs.id === sheet.flashSaleId) ?? null)
      : null;

  const itemsNotice = formatFlashSaleItemsNotice(itemsMeta);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Flash sale"
        subtitle="Giờ Việt Nam (GMT+7) — quản lý khung giờ, thẩm định suất đăng ký và đối chiếu khuyến mãi chỉ đọc."
        primaryAction={
          canWriteFlashSales ? (
            <Button type="button" size="sm" onClick={openCreateView}>
              <Plus className="size-4" aria-hidden="true" />
              <span>Mở khung giờ</span>
            </Button>
          ) : undefined
        }
      />

      <MetricStrip
        items={[
          {
            label: "Khung giờ",
            value: formatInt(flashSales.length),
            hint: `${formatInt(counts.live)} đang diễn ra`,
          },
          {
            label: "Suất chờ duyệt",
            value: formatInt(counts.PENDING),
            hint: "Cần thẩm định",
          },
          {
            label: "Suất đã duyệt",
            value: formatInt(counts.APPROVED),
            hint: "Hiển thị trên site",
          },
          {
            label: "Suất từ chối",
            value: formatInt(counts.REJECTED),
            hint: `Trên ${formatInt(counts.all)} đăng ký`,
          },
        ]}
      />

      <Tabs
        value={activeTab}
        onValueChange={(v) => setActiveTab(v as FlashTab)}
        className="space-y-4"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="max-w-full overflow-x-auto pb-0.5">
            <TabsList aria-label="Chuyển giữa suất đăng ký, khung giờ và chế độ chỉ đọc">
              <TabsTrigger value="slots">
                <span>Suất gian hàng đăng ký</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(entries.length)}
                </span>
              </TabsTrigger>
              <TabsTrigger value="windows">
                <span>Khung giờ Flash sale</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(flashSales.length)}
                </span>
              </TabsTrigger>
              {canReadVouchers ? (
                <TabsTrigger value="vouchers">
                  <span>Voucher sàn (chỉ đọc)</span>
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {formatInt(vouchers.length)}
                  </span>
                </TabsTrigger>
              ) : null}
              <TabsTrigger value="coins">Xu (chỉ đọc)</TabsTrigger>
            </TabsList>
          </div>
          <span className="text-xs text-muted-foreground">Giờ Việt Nam (GMT+7)</span>
        </div>

        {/* TAB 1: SLOTS */}
        <TabsContent value="slots" className="space-y-3">
          {itemsError ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription className="flex w-full flex-wrap items-center justify-between gap-2">
                <span>{itemsError}</span>
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={windowsLoading}
                  onClick={() => void reloadFlashSales(windowStatusFilter)}
                >
                  Thử lại
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}

          {itemsNotice ? (
            <Alert>
              <Info className="size-4" aria-hidden="true" />
              <AlertDescription>{itemsNotice}</AlertDescription>
            </Alert>
          ) : null}

          <IndexTable
            tabs={[
              { key: "all", label: "Tất cả", count: counts.all },
              { key: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
              { key: "APPROVED", label: "Đã duyệt", count: counts.APPROVED },
              { key: "REJECTED", label: "Từ chối", count: counts.REJECTED },
            ]}
            activeTab={itemFilter}
            onTabChange={(k) => setItemFilter(k as ItemFilter)}
            toolbar={
              <span className="text-xs text-muted-foreground tabular-nums">
                {formatInt(visibleEntries.length)} suất
              </span>
            }
          >
            <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
              <div className="relative min-w-0 flex-1 sm:max-w-xs">
                <Search
                  className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm gian hàng, khung giờ, mã SKU..."
                  aria-label="Tìm kiếm Flash sale và khuyến mãi"
                  className="h-9 bg-card pl-8"
                />
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={windowsLoading}
                onClick={() => void reloadFlashSales(windowStatusFilter)}
              >
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>Làm mới</span>
              </Button>
            </div>

            {visibleEntries.length === 0 ? (
              itemsError ? null : (
                <div className="p-4">
                  <EmptyState
                    icon={Zap}
                    title="Chưa có suất đăng ký nào phù hợp bộ lọc"
                    description="Các suất SKU do gian hàng đăng ký tham gia Flash sale sẽ xuất hiện tại đây."
                    className="shadow-none"
                  />
                </div>
              )
            ) : (
              <>
                <div className="hidden md:block">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Gian hàng &amp; SKU</TableHead>
                        <TableHead>Khung giờ</TableHead>
                        <TableHead className="text-right">Giá Flash sale</TableHead>
                        <TableHead className="text-right">Suất còn / tổng</TableHead>
                        <TableHead>Trạng thái</TableHead>
                        <TableHead className="text-right">Thao tác</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visibleEntries.map(({ flashSale, item }) => {
                        const meta = getItemStatusMeta(item.status);
                        const selected = sheet?.kind === "slot" && sheet.itemId === item.id;
                        const discountPct = computeFlashDiscountPercent(
                          item.original_price_vnd,
                          item.flash_price_vnd
                        );
                        return (
                          <TableRow
                            key={item.id}
                            data-state={selected ? "selected" : undefined}
                            className="cursor-pointer"
                            onClick={() => {
                              setRejectDraft("");
                              setSheet({
                                kind: "slot",
                                flashSaleId: flashSale.id,
                                itemId: item.id,
                              });
                            }}
                          >
                            <TableCell className="whitespace-normal">
                              <button
                                type="button"
                                className="flex flex-col items-start text-left focus-visible:underline focus-visible:outline-none"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setRejectDraft("");
                                  setSheet({
                                    kind: "slot",
                                    flashSaleId: flashSale.id,
                                    itemId: item.id,
                                  });
                                }}
                              >
                                <span className="font-medium text-foreground">
                                  {shopLabel(item.shop_id)}
                                </span>
                                <span className="text-xs text-muted-foreground tabular-nums">
                                  SKU {item.sku_id.slice(0, 8)}, tối đa{" "}
                                  {formatInt(item.per_user_limit)}/người
                                </span>
                              </button>
                            </TableCell>
                            <TableCell className="max-w-[220px] whitespace-normal">
                              {flashSale.name}
                            </TableCell>
                            <TableCell className="text-right">
                              <Money vnd={item.flash_price_vnd} />
                              {discountPct !== null ? (
                                <div className="text-xs font-medium text-badge-success-ink tabular-nums">
                                  -{discountPct}%
                                </div>
                              ) : null}
                            </TableCell>
                            <TableCell className="text-right tabular-nums">
                              {formatInt(item.remaining_slots)} / {formatInt(item.total_slots)}
                            </TableCell>
                            <TableCell>
                              <StatusBadge tone={meta.tone}>{meta.label}</StatusBadge>
                            </TableCell>
                            <TableCell className="text-right">{decisionButtons(item)}</TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                <div className="md:hidden">
                  {visibleEntries.map(({ flashSale, item }) => {
                    const meta = getItemStatusMeta(item.status);
                    return (
                      <IndexRowCard
                        key={item.id}
                        title={shopLabel(item.shop_id)}
                        badge={<StatusBadge tone={meta.tone}>{meta.label}</StatusBadge>}
                        meta={
                          <>
                            <Money vnd={item.flash_price_vnd} size="sm" />
                            <span>·</span>
                            <span>{flashSale.name}</span>
                            <span>·</span>
                            <span className="tabular-nums">
                              còn {formatInt(item.remaining_slots)}/{formatInt(item.total_slots)}{" "}
                              suất
                            </span>
                          </>
                        }
                        onClick={() => {
                          setRejectDraft("");
                          setSheet({ kind: "slot", flashSaleId: flashSale.id, itemId: item.id });
                        }}
                      />
                    );
                  })}
                </div>
              </>
            )}
          </IndexTable>
        </TabsContent>

        {/* TAB 2: WINDOWS */}
        <TabsContent value="windows" className="space-y-3">
          <IndexTable
            toolbar={
              <span className="text-xs text-muted-foreground tabular-nums">
                {windowsLoading ? "Đang tải..." : `${formatInt(visibleWindows.length)} khung giờ`}
              </span>
            }
          >
            <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
              <div className="relative min-w-0 flex-1 sm:max-w-xs">
                <Search
                  className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm tên khung giờ..."
                  aria-label="Tìm kiếm Flash sale và khuyến mãi"
                  className="h-9 bg-card pl-8"
                />
              </div>
              <div className="w-full sm:w-48">
                <FilterSelect
                  value={windowStatusFilter}
                  onValueChange={(next) => {
                    const typed = next as WindowStatusFilter;
                    setWindowStatusFilter(typed);
                    void reloadFlashSales(typed);
                  }}
                  ariaLabel="Lọc trạng thái khung giờ"
                  options={[
                    { value: "", label: "Tất cả trạng thái" },
                    { value: "ACTIVE", label: "Đang diễn ra" },
                    { value: "UPCOMING", label: "Sắp diễn ra" },
                    { value: "ENDED", label: "Đã kết thúc" },
                  ]}
                />
              </div>
            </div>

            {visibleWindows.length === 0 ? (
              <div className="p-4">
                <EmptyState
                  icon={Zap}
                  title="Chưa có khung giờ nào"
                  description="Mở khung giờ Flash sale mới để các gian hàng đăng ký sản phẩm ưu đãi."
                  action={
                    canWriteFlashSales ? (
                      <Button type="button" size="sm" onClick={openCreateView}>
                        <Plus className="size-4" aria-hidden="true" />
                        <span>Mở khung giờ</span>
                      </Button>
                    ) : undefined
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
                        <TableHead>Khung giờ &amp; điều kiện</TableHead>
                        <TableHead>Thời gian</TableHead>
                        <TableHead>Trạng thái</TableHead>
                        <TableHead className="text-right">SKU / Đã duyệt</TableHead>
                        <TableHead className="text-right">Thao tác</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visibleWindows.map((fs) => {
                        const items = fs.items ?? [];
                        const approved = items.filter(
                          (it) => it.status.toUpperCase() === "APPROVED"
                        ).length;
                        const meta = getFlashSaleWindowMeta(fs, now);
                        const canEnd = canWriteFlashSales && canEndFlashSaleEarly(fs, now);
                        const selected = sheet?.kind === "window" && sheet.flashSaleId === fs.id;
                        const catNames = (fs.category_ids ?? [])
                          .map((id) => categoryNameById.get(id) ?? id.slice(0, 8))
                          .slice(0, 3);
                        return (
                          <TableRow
                            key={fs.id}
                            data-state={selected ? "selected" : undefined}
                            className="cursor-pointer"
                            onClick={() => setSheet({ kind: "window", flashSaleId: fs.id })}
                          >
                            <TableCell className="whitespace-normal">
                              <button
                                type="button"
                                className="flex flex-col items-start text-left focus-visible:underline focus-visible:outline-none"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setSheet({ kind: "window", flashSaleId: fs.id });
                                }}
                              >
                                <span className="font-medium text-foreground">{fs.name}</span>
                                <span className="text-xs text-muted-foreground">
                                  Giảm tối thiểu {formatInt(fs.min_discount_percent ?? 0)}%
                                  {catNames.length > 0
                                    ? ` · ${catNames.join(", ")}`
                                    : " · Mọi danh mục"}
                                </span>
                              </button>
                            </TableCell>
                            <TableCell className="tabular-nums">
                              <div className="text-[13px] text-foreground">
                                {fs.starts_at ? formatDateTimeVi(fs.starts_at) : "Chưa có"}
                              </div>
                              <div className="text-xs text-muted-foreground">
                                đến {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "chưa có"}
                              </div>
                            </TableCell>
                            <TableCell>
                              <StatusBadge tone={flashWindowStateTone(meta.state)}>
                                {meta.label}
                              </StatusBadge>
                            </TableCell>
                            <TableCell className="text-right tabular-nums">
                              {formatInt(items.length)} / {formatInt(approved)}
                            </TableCell>
                            <TableCell className="text-right">
                              {canEnd ? (
                                <Button
                                  type="button"
                                  size="xs"
                                  variant="destructive"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setConfirmEndWindow(fs);
                                  }}
                                >
                                  Kết thúc sớm
                                </Button>
                              ) : (
                                <span className="text-xs text-muted-foreground">Chi tiết</span>
                              )}
                            </TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                <div className="md:hidden">
                  {visibleWindows.map((fs) => {
                    const meta = getFlashSaleWindowMeta(fs, now);
                    return (
                      <IndexRowCard
                        key={fs.id}
                        title={fs.name}
                        badge={
                          <StatusBadge tone={flashWindowStateTone(meta.state)}>
                            {meta.label}
                          </StatusBadge>
                        }
                        meta={
                          <>
                            <span>Tối thiểu {formatInt(fs.min_discount_percent ?? 0)}%</span>
                            <span>·</span>
                            <span className="tabular-nums">
                              {formatInt((fs.items ?? []).length)} SKU
                            </span>
                          </>
                        }
                        onClick={() => setSheet({ kind: "window", flashSaleId: fs.id })}
                      />
                    );
                  })}
                </div>
              </>
            )}
          </IndexTable>
        </TabsContent>

        {/* TAB 3: PLATFORM VOUCHERS (READ-ONLY) */}
        {canReadVouchers ? (
          <TabsContent value="vouchers" className="space-y-3">
            <Alert>
              <Info className="size-4" aria-hidden="true" />
              <AlertDescription>
                Voucher toàn sàn và Freeship đang ở chế độ chỉ đọc để đối chiếu lịch sử khuyến mãi.
              </AlertDescription>
            </Alert>

            <IndexTable
              toolbar={
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={vouchersLoading}
                  onClick={() => void reloadVouchers()}
                >
                  <RefreshCw className="size-3.5" aria-hidden="true" />
                  <span>{vouchersLoading ? "Đang tải..." : "Làm mới"}</span>
                </Button>
              }
            >
              <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
                <div className="relative min-w-0 flex-1 sm:max-w-xs">
                  <Search
                    className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    type="search"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Tìm mã voucher, tên chương trình..."
                    aria-label="Tìm kiếm Flash sale và khuyến mãi"
                    className="h-9 bg-card pl-8"
                  />
                </div>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(visibleVouchers.length)} voucher
                </span>
              </div>

              {visibleVouchers.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={Ticket}
                    title="Chưa có mã voucher sàn nào"
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  <div className="hidden md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Mã &amp; tên voucher</TableHead>
                          <TableHead>Phạm vi</TableHead>
                          <TableHead>Ưu đãi</TableHead>
                          <TableHead className="text-right">Đã dùng / tổng</TableHead>
                          <TableHead>Hiệu lực</TableHead>
                          <TableHead>Trạng thái</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {visibleVouchers.map((v) => {
                          const scopeMeta = getVoucherScopeMeta(v.scope);
                          const statusMeta = getVoucherStatusBadge(v);
                          return (
                            <TableRow key={v.id}>
                              <TableCell className="whitespace-normal">
                                <div className="font-semibold text-foreground">{v.code}</div>
                                <div className="text-xs text-muted-foreground">{v.name}</div>
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={pillToneToStatusTone(scopeMeta.tone)}>
                                  {scopeMeta.label}
                                </StatusBadge>
                              </TableCell>
                              <TableCell className="whitespace-normal">
                                <div className="text-[13px] font-medium text-foreground">
                                  {formatVoucherBenefit(v)}
                                </div>
                                {v.min_order_vnd > 0 ? (
                                  <div className="text-xs text-muted-foreground tabular-nums">
                                    Đơn từ {formatVnd(v.min_order_vnd)}
                                  </div>
                                ) : null}
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                {formatInt(v.used_count)} / {formatInt(v.quota)}
                              </TableCell>
                              <TableCell className="text-xs text-muted-foreground tabular-nums">
                                <div>{v.starts_at ? formatDateTimeVi(v.starts_at) : "—"}</div>
                                <div>đến {v.ends_at ? formatDateTimeVi(v.ends_at) : "—"}</div>
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={statusMeta.tone}>{statusMeta.label}</StatusBadge>
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="md:hidden">
                    {visibleVouchers.map((v) => {
                      const statusMeta = getVoucherStatusBadge(v);
                      return (
                        <IndexRowCard
                          key={v.id}
                          title={v.code}
                          badge={
                            <StatusBadge tone={statusMeta.tone}>{statusMeta.label}</StatusBadge>
                          }
                          meta={
                            <>
                              <span>{formatVoucherBenefit(v)}</span>
                              <span>·</span>
                              <span className="tabular-nums">
                                {formatInt(v.used_count)}/{formatInt(v.quota)}
                              </span>
                            </>
                          }
                        />
                      );
                    })}
                  </div>
                </>
              )}
            </IndexTable>
          </TabsContent>
        ) : null}

        {/* TAB 4: COINS (READ-ONLY) */}
        <TabsContent value="coins">
          <Card aria-labelledby="coins-readonly-heading">
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <CardTitle id="coins-readonly-heading">Xu thành viên (chỉ đọc)</CardTitle>
              <Badge variant="neutral">Chỉ đọc</Badge>
            </CardHeader>
            <CardContent>
              <Alert>
                <Info className="size-4" aria-hidden="true" />
                <AlertDescription>
                  Chức năng cộng Xu thành viên thủ công từ trang quản trị đã tạm khóa theo chính
                  sách vận hành hiện tại. Lịch sử biến động Xu được ghi nhận trong nhật ký kiểm
                  toán.
                </AlertDescription>
              </Alert>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      {/* Sheet: Inspect Slot / Inspect Window */}
      <Sheet open={Boolean(sheet)} onOpenChange={(open) => !open && setSheet(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
          {sheetSlotData ? (
            <>
              <SheetHeader className="border-b border-border pb-4">
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>{shopLabel(sheetSlotData.item.shop_id)}</SheetTitle>
                  <StatusBadge tone={getItemStatusMeta(sheetSlotData.item.status).tone}>
                    {getItemStatusMeta(sheetSlotData.item.status).label}
                  </StatusBadge>
                </div>
                <SheetDescription>{sheetSlotData.flashSale.name}</SheetDescription>
              </SheetHeader>

              <div className="space-y-4 px-4 pb-6">
                <DetailGrid>
                  <DetailItem label="Gian hàng">{shopLabel(sheetSlotData.item.shop_id)}</DetailItem>
                  <DetailItem label="Giá Flash sale" numeric>
                    {formatVnd(sheetSlotData.item.flash_price_vnd)}
                    {sheetSlotData.item.original_price_vnd
                      ? ` (gốc ${formatVnd(sheetSlotData.item.original_price_vnd)})`
                      : ""}
                  </DetailItem>
                  <DetailItem label="Mã SKU" numeric>
                    {sheetSlotData.item.sku_id}
                  </DetailItem>
                  <DetailItem label="Suất còn / tổng" numeric>
                    {formatInt(sheetSlotData.item.remaining_slots)} /{" "}
                    {formatInt(sheetSlotData.item.total_slots)}
                  </DetailItem>
                  <DetailItem label="Giới hạn mỗi người" numeric>
                    {formatInt(sheetSlotData.item.per_user_limit)} sản phẩm
                  </DetailItem>
                  <DetailItem label="Khung giờ">{sheetSlotData.flashSale.name}</DetailItem>
                  {sheetSlotData.item.rejection_reason ? (
                    <DetailItem label="Lý do từ chối" wide>
                      <span className="font-medium text-destructive">
                        {sheetSlotData.item.rejection_reason}
                      </span>
                    </DetailItem>
                  ) : null}
                </DetailGrid>

                {canApproveItems ? (
                  <div className="space-y-3 rounded-lg border border-border bg-card p-4">
                    <Field className="gap-1.5">
                      <FieldLabel htmlFor="fs-reject-reason">Lý do từ chối (tùy chọn)</FieldLabel>
                      <Input
                        id="fs-reject-reason"
                        type="text"
                        value={rejectDraft}
                        onChange={(e) => setRejectDraft(e.target.value)}
                        placeholder={REJECT_REASON}
                        className="bg-card"
                      />
                    </Field>
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        type="button"
                        size="sm"
                        disabled={
                          Boolean(busyItemId) ||
                          sheetSlotData.item.status.toUpperCase() === "APPROVED"
                        }
                        onClick={() => void handleDecision(sheetSlotData.item, true)}
                      >
                        {busyItemId === sheetSlotData.item.id && busyAction === "approve"
                          ? "Đang duyệt..."
                          : "Duyệt suất"}
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="destructive"
                        disabled={
                          Boolean(busyItemId) ||
                          sheetSlotData.item.status.toUpperCase() === "REJECTED"
                        }
                        onClick={() => void handleDecision(sheetSlotData.item, false, rejectDraft)}
                      >
                        {busyItemId === sheetSlotData.item.id && busyAction === "reject"
                          ? "Đang xử lý..."
                          : "Từ chối"}
                      </Button>
                    </div>
                  </div>
                ) : null}
              </div>
            </>
          ) : null}

          {sheetWindowData ? (
            <>
              <SheetHeader className="border-b border-border pb-4">
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>{sheetWindowData.name}</SheetTitle>
                  <StatusBadge
                    tone={flashWindowStateTone(getFlashSaleWindowMeta(sheetWindowData, now).state)}
                  >
                    {getFlashSaleWindowMeta(sheetWindowData, now).label}
                  </StatusBadge>
                </div>
                <SheetDescription className="tabular-nums">
                  {formatInt((sheetWindowData.items ?? []).length)} SKU đăng ký
                </SheetDescription>
              </SheetHeader>

              <div className="space-y-4 px-4 pb-6">
                <DetailGrid>
                  <DetailItem label="Bắt đầu" numeric>
                    {sheetWindowData.starts_at
                      ? formatDateTimeVi(sheetWindowData.starts_at)
                      : "Chưa có"}
                  </DetailItem>
                  <DetailItem label="Kết thúc" numeric>
                    {sheetWindowData.ends_at
                      ? formatDateTimeVi(sheetWindowData.ends_at)
                      : "Chưa có"}
                  </DetailItem>
                  <DetailItem label="Mức giảm tối thiểu" numeric>
                    {formatInt(sheetWindowData.min_discount_percent ?? 0)}%
                  </DetailItem>
                  <DetailItem label="Danh mục áp dụng">
                    {(sheetWindowData.category_ids?.length ?? 0) > 0
                      ? (sheetWindowData.category_ids ?? [])
                          .map((id) => categoryNameById.get(id) ?? id.slice(0, 8))
                          .join(", ")
                      : "Tất cả danh mục"}
                  </DetailItem>
                </DetailGrid>

                {canWriteFlashSales && canEndFlashSaleEarly(sheetWindowData, now) ? (
                  <div>
                    <Button
                      type="button"
                      size="sm"
                      variant="destructive"
                      onClick={() => setConfirmEndWindow(sheetWindowData)}
                    >
                      Kết thúc sớm khung giờ
                    </Button>
                  </div>
                ) : null}

                {(sheetWindowData.items ?? []).length === 0 ? (
                  <p className="rounded-lg border border-border bg-muted/35 p-4 text-center text-[13px] text-muted-foreground">
                    Chưa có gian hàng đăng ký SKU cho khung giờ này.
                  </p>
                ) : (
                  <ul className="space-y-2.5">
                    {(sheetWindowData.items ?? []).map((it) => {
                      const meta = getItemStatusMeta(it.status);
                      return (
                        <li
                          key={it.id}
                          className="flex flex-wrap items-center justify-between gap-2.5 rounded-lg border border-border bg-card p-3"
                        >
                          <div className="min-w-0 space-y-0.5">
                            <div className="font-medium text-foreground">
                              {shopLabel(it.shop_id)}
                            </div>
                            <div className="text-xs text-muted-foreground tabular-nums">
                              {formatVnd(it.flash_price_vnd)}, còn {formatInt(it.remaining_slots)}/
                              {formatInt(it.total_slots)} suất
                            </div>
                          </div>
                          <div className="flex flex-wrap items-center gap-2">
                            <StatusBadge tone={meta.tone}>{meta.label}</StatusBadge>
                            {decisionButtons(it)}
                          </div>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      <ConfirmActionDialog
        open={Boolean(confirmEndWindow)}
        title="Kết thúc sớm khung giờ Flash sale"
        description={
          confirmEndWindow
            ? `Khung giờ "${confirmEndWindow.name}" sẽ dừng ngay lập tức và giải phóng các suất còn lại. Bạn có chắc muốn tiếp tục?`
            : ""
        }
        confirmLabel="Kết thúc sớm"
        confirmVariant="destructive"
        busy={endingWindow}
        onConfirm={() => void handleConfirmEndEarly()}
        onCancel={() => {
          if (!endingWindow) setConfirmEndWindow(null);
        }}
      />
    </div>
  );
}

function AdminFlashSaleRoute(props: AdminFlashSaleClientProps) {
  return (
    <AdminStateGate
      title="Flash sale"
      emptyTitle="Chưa có khung giờ Flash sale"
      emptyMessage="Mở khung giờ đầu tiên để gian hàng đăng ký SKU."
      errorTitle="Không thể tải khung giờ Flash sale"
      errorMessage="Dịch vụ khuyến mãi tạm gián đoạn. Vui lòng thử lại."
      loadingRows={5}
    >
      {({ initialQuery }) => <FlashSaleWorkspace {...props} initialQuery={initialQuery} />}
    </AdminStateGate>
  );
}

export default function AdminFlashSaleClient(props: AdminFlashSaleClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Flash sale" />}>
      <AdminFlashSaleRoute {...props} />
    </Suspense>
  );
}
