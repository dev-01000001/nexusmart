"use client";

import React, { useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Copy, Package, Percent, Plus, Tag, Zap } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  Money,
  PageHeader,
  StatusBadge,
  type StatusTone,
} from "@/components/app";
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
import { Progress } from "@/components/ui/progress";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { formatDateTimeVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import { useSellerContext } from "./SellerShell";
import { PromotionsDiscountProgramsTab } from "./PromotionsDiscountProgramsTab";
import type {
  GwFlashSale,
  GwFlashSaleItem,
  GwSellerProduct,
  GwSellerSku,
  GwSellerVoucher,
  GwShopDiscountProgram,
} from "./sellerApiTypes";
import {
  canEditVoucherField,
  FLASH_ITEM_STATUS_META,
  parseUiState,
  VOUCHER_STATUS_META,
} from "./types";

type PromoTab = "vouchers" | "programs" | "flash";
type FlashItemStatusFilter = "ALL" | "PENDING" | "APPROVED" | "REJECTED";

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function flashStatusOf(fs: GwFlashSale, nowMs: number): "ended" | "live" | "upcoming" {
  const start = fs.starts_at ? Date.parse(fs.starts_at) : 0;
  const end = fs.ends_at ? Date.parse(fs.ends_at) : 0;
  if (end > 0 && nowMs > end) return "ended";
  if (start > 0 && nowMs < start) return "upcoming";
  return "live";
}

function getVoucherStatusKey(v: GwSellerVoucher): string {
  if (v.status) return v.status.toUpperCase();
  return v.is_active ? "ACTIVE" : "PAUSED";
}

function voucherStatusTone(statusKey: string): StatusTone {
  switch (statusKey.toUpperCase()) {
    case "ACTIVE":
      return "success";
    case "SCHEDULED":
      return "info";
    case "PAUSED":
      return "attention";
    case "DELETED":
      return "critical";
    case "EXPIRED":
    default:
      return "neutral";
  }
}

function flashItemStatusTone(status: string): StatusTone {
  switch (status.toUpperCase()) {
    case "APPROVED":
      return "success";
    case "PENDING":
      return "attention";
    case "REJECTED":
      return "critical";
    default:
      return "neutral";
  }
}

function QuotaProgress({
  value,
  max,
  leftLabel,
  rightLabel,
}: {
  value: number;
  max: number;
  leftLabel: string;
  rightLabel: string;
}) {
  const pct = max > 0 ? Math.min(100, Math.max(0, Math.round((value / max) * 100))) : 0;
  return (
    <div className="min-w-[132px] space-y-1">
      <Progress value={pct} className="h-1.5" />
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
        <span>{leftLabel}</span>
        <span>{rightLabel}</span>
      </div>
    </div>
  );
}

export interface SellerPromotionsClientProps {
  initialVouchers: GwSellerVoucher[];
  initialFlashSales: GwFlashSale[];
  initialFlashItems: GwFlashSaleItem[];
  initialDiscountPrograms: GwShopDiscountProgram[];
  shopProducts: GwSellerProduct[];
  loadError: string | null;
  serverNowMs?: number;
}

export function SellerPromotionsClient({
  initialVouchers,
  initialFlashSales,
  initialFlashItems,
  initialDiscountPrograms,
  shopProducts,
  loadError,
  serverNowMs,
}: SellerPromotionsClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as PromoTab | null;
  const { user, shop, showToast } = useSellerContext();

  const [vouchers, setVouchers] = useState<GwSellerVoucher[]>(initialVouchers);
  const [flashSales] = useState<GwFlashSale[]>(initialFlashSales);
  const [sellerFlashItems, setSellerFlashItems] = useState<GwFlashSaleItem[]>(() => {
    if (initialFlashItems.length > 0) return initialFlashItems;
    const embedded: GwFlashSaleItem[] = [];
    for (const fs of initialFlashSales) {
      for (const item of fs.items ?? []) {
        if (item.shop_id === user.shopId) embedded.push(item);
      }
    }
    return embedded;
  });
  const [programsCount, setProgramsCount] = useState(initialDiscountPrograms.length);

  const [tab, setTab] = useState<PromoTab>(
    initialTab && ["vouchers", "programs", "flash"].includes(initialTab) ? initialTab : "vouchers"
  );
  const [showVoucherForm, setShowVoucherForm] = useState(false);
  const [editingVoucher, setEditingVoucher] = useState<GwSellerVoucher | null>(null);
  const [voucherActionBusyId, setVoucherActionBusyId] = useState<string | null>(null);
  const [confirmDeleteVoucher, setConfirmDeleteVoucher] = useState<GwSellerVoucher | null>(null);
  const [drawerVoucherId, setDrawerVoucherId] = useState<string | null>(null);

  const [showProgramForm, setShowProgramForm] = useState(false);
  const [showFlashForm, setShowFlashForm] = useState(false);
  const [selectedSlotId, setSelectedSlotId] = useState<string>("all");
  const [flashStatusFilter, setFlashStatusFilter] = useState<FlashItemStatusFilter>("ALL");

  const [nowMs] = useState<number>(() => serverNowMs ?? Date.now());
  const defaultStarts = useMemo(() => toDatetimeLocalValue(new Date(nowMs)), [nowMs]);
  const defaultEnds = useMemo(
    () => toDatetimeLocalValue(new Date(nowMs + 14 * 24 * 3600 * 1000)),
    [nowMs]
  );

  // Voucher create/edit state
  const [vCode, setVCode] = useState("");
  const [vName, setVName] = useState("");
  const [vType, setVType] = useState<"FIXED" | "PERCENT">("FIXED");
  const [vValue, setVValue] = useState("25000");
  const [vMaxDiscount, setVMaxDiscount] = useState("50000");
  const [vMinOrder, setVMinOrder] = useState("300000");
  const [vQuota, setVQuota] = useState("100");
  const [vPerUser, setVPerUser] = useState("1");
  const [vBudget, setVBudget] = useState("2500000");
  const [vStartsAt, setVStartsAt] = useState(defaultStarts);
  const [vEndsAt, setVEndsAt] = useState(defaultEnds);
  const [vBusy, setVBusy] = useState(false);
  const [vError, setVError] = useState<string | null>(null);

  const allSkus = useMemo(() => {
    const out: Array<{ product: GwSellerProduct; sku: GwSellerSku }> = [];
    for (const product of shopProducts) {
      for (const sku of product.skus ?? []) {
        out.push({ product, sku });
      }
    }
    return out;
  }, [shopProducts]);

  const selectableFlashSkus = useMemo(
    () =>
      shopProducts
        .filter((p) => p.status === "APPROVED")
        .flatMap((product) =>
          (product.skus ?? [])
            .filter((sku) => sku.is_active && sku.available_stock > 0)
            .map((sku) => ({ product, sku }))
        ),
    [shopProducts]
  );

  const skuById = useMemo(() => new Map(allSkus.map((entry) => [entry.sku.id, entry])), [allSkus]);

  const flashWindowById = useMemo(() => new Map(flashSales.map((w) => [w.id, w])), [flashSales]);

  const openFlashSales = useMemo(
    () => flashSales.filter((fs) => flashStatusOf(fs, nowMs) !== "ended"),
    [flashSales, nowMs]
  );

  const [fSlotId, setFSlotId] = useState(openFlashSales[0]?.id ?? flashSales[0]?.id ?? "");
  const [fSkuId, setFSkuId] = useState(selectableFlashSkus[0]?.sku.id ?? "");
  const [fPriceStr, setFPriceStr] = useState("149000");
  const [fQuotaStr, setFQuotaStr] = useState("20");
  const [fLimitStr, setFLimitStr] = useState("2");
  const [fBusy, setFBusy] = useState(false);
  const [fError, setFError] = useState<string | null>(null);

  const selectedFlashWindow = useMemo(
    () => flashSales.find((w) => w.id === fSlotId) ?? null,
    [flashSales, fSlotId]
  );
  const selectedFlashSkuEntry = useMemo(
    () => selectableFlashSkus.find((x) => x.sku.id === fSkuId) ?? null,
    [selectableFlashSkus, fSkuId]
  );

  const flashRows = useMemo(() => {
    return sellerFlashItems.map((item) => {
      const win = flashWindowById.get(item.flash_sale_id);
      const info = skuById.get(item.sku_id);
      return {
        item,
        windowName: win?.name ?? item.flash_sale_id,
        windowStatus: win ? flashStatusOf(win, nowMs) : ("upcoming" as const),
        productName: info?.product.name ?? `SKU ${item.sku_id}`,
        skuCode: info?.sku.sku_code ?? item.sku_id,
        originalPrice: item.original_price_vnd || info?.sku.price_vnd || 0,
        thumb: info?.sku.image_url || info?.product.images?.[0] || null,
      };
    });
  }, [sellerFlashItems, flashWindowById, skuById, nowMs]);

  const flashItemCounts = useMemo(() => {
    const counts = { ALL: sellerFlashItems.length, PENDING: 0, APPROVED: 0, REJECTED: 0 };
    for (const item of sellerFlashItems) {
      const st = item.status.toUpperCase();
      if (st === "PENDING") counts.PENDING += 1;
      else if (st === "APPROVED") counts.APPROVED += 1;
      else if (st === "REJECTED") counts.REJECTED += 1;
    }
    return counts;
  }, [sellerFlashItems]);

  const filteredFlashRows = useMemo(() => {
    return flashRows.filter((row) => {
      if (selectedSlotId !== "all" && row.item.flash_sale_id !== selectedSlotId) return false;
      if (flashStatusFilter !== "ALL" && row.item.status.toUpperCase() !== flashStatusFilter) {
        return false;
      }
      return true;
    });
  }, [flashRows, selectedSlotId, flashStatusFilter]);

  const drawerVoucher = useMemo(
    () => (drawerVoucherId ? (vouchers.find((v) => v.id === drawerVoucherId) ?? null) : null),
    [vouchers, drawerVoucherId]
  );

  const activeVoucherCount = vouchers.filter((v) => getVoucherStatusKey(v) === "ACTIVE").length;
  const voucherUsedTotal = vouchers.reduce((s, v) => s + v.used_count, 0);
  const voucherQuotaTotal = vouchers.reduce((s, v) => s + v.quota, 0);
  const voucherBudgetUsedTotal = vouchers.reduce((s, v) => s + v.used_budget_vnd, 0);

  const reloadVouchers = async () => {
    try {
      const res = await api<{ vouchers?: GwSellerVoucher[] }>(
        "/api/seller/vouchers?include_inactive=true"
      );
      if (res.vouchers) setVouchers(res.vouchers);
    } catch {
      router.refresh();
    }
  };

  const reloadFlashItems = async () => {
    try {
      const res = await api<{ items?: GwFlashSaleItem[] }>(
        "/api/seller/flash-sales/items?page=1&page_size=50"
      );
      if (Array.isArray(res.items)) setSellerFlashItems(res.items);
    } catch {
      router.refresh();
    }
  };

  const resetVoucherForm = () => {
    setEditingVoucher(null);
    setVCode("");
    setVName("");
    setVType("FIXED");
    setVValue("25000");
    setVMaxDiscount("50000");
    setVMinOrder("300000");
    setVQuota("100");
    setVPerUser("1");
    setVBudget("2500000");
    setVStartsAt(defaultStarts);
    setVEndsAt(defaultEnds);
    setVError(null);
  };

  const handleOpenEditVoucher = (v: GwSellerVoucher) => {
    setEditingVoucher(v);
    setVCode(v.code);
    setVName(v.name);
    setVType(v.discount_type.toUpperCase() === "PERCENT" ? "PERCENT" : "FIXED");
    setVValue(
      String(v.discount_type.toUpperCase() === "PERCENT" ? v.discount_percent : v.value_vnd)
    );
    setVMaxDiscount(String(v.max_discount_vnd ?? 0));
    setVMinOrder(String(v.min_order_vnd ?? 0));
    setVQuota(String(v.quota));
    setVPerUser(String(v.per_user_limit || 1));
    setVBudget(String(v.budget_vnd ?? 0));
    setVStartsAt(v.starts_at ? toDatetimeLocalValue(new Date(v.starts_at)) : defaultStarts);
    setVEndsAt(v.ends_at ? toDatetimeLocalValue(new Date(v.ends_at)) : defaultEnds);
    setVError(null);
    setShowVoucherForm(true);
  };

  const handleCopyCode = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    showToast(`Đã sao chép mã ${code}.`);
  };

  const handleSaveVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const val = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.max(0, Math.trunc(Number(vMinOrder) || 0));
    const quota = Math.trunc(Number(vQuota) || 0);
    const perUser = Math.max(1, Math.trunc(Number(vPerUser) || 1));
    const budgetVnd = Math.max(0, Math.trunc(Number(vBudget) || 0));
    const maxDisc = vType === "PERCENT" ? Math.max(0, Math.trunc(Number(vMaxDiscount) || 0)) : 0;

    if (!editingVoucher && code.length < 3) {
      setVError("Mã voucher cần tối thiểu 3 ký tự liền nhau.");
      return;
    }
    if ((!editingVoucher || editingVoucher.used_count === 0) && vType === "FIXED" && val < 1000) {
      setVError("Mức giảm cố định tối thiểu 1.000₫.");
      return;
    }
    if (
      (!editingVoucher || editingVoucher.used_count === 0) &&
      vType === "PERCENT" &&
      (val < 1 || val > 90)
    ) {
      setVError("Mức giảm phần trăm hợp lệ từ 1% đến 90%.");
      return;
    }
    if (quota < 1) {
      setVError("Tổng lượt phát hành tối thiểu 1 suất.");
      return;
    }
    if (editingVoucher && quota < editingVoucher.used_count) {
      setVError(
        `Tổng lượt mới không được nhỏ hơn số lượt đã dùng (${formatInt(editingVoucher.used_count)}).`
      );
      return;
    }
    const startDate = new Date(vStartsAt);
    const endDate = new Date(vEndsAt);
    if (
      Number.isNaN(startDate.getTime()) ||
      Number.isNaN(endDate.getTime()) ||
      endDate <= startDate
    ) {
      setVError("Thời điểm kết thúc phải sau thời điểm bắt đầu.");
      return;
    }

    setVError(null);
    setVBusy(true);
    try {
      const discountLabel = vType === "FIXED" ? formatVnd(val) : `${val}%`;
      const displayName = vName.trim() || `Giảm ${discountLabel} đơn từ ${formatVnd(minOrder)}`;

      if (editingVoucher) {
        const updatePayload: Record<string, unknown> = {
          name: displayName,
          quota,
          budget_vnd: budgetVnd,
          ends_at: endDate.toISOString(),
        };
        if (editingVoucher.used_count === 0) {
          updatePayload.discount_type = vType;
          updatePayload.value_vnd = vType === "FIXED" ? val : 0;
          updatePayload.discount_percent = vType === "PERCENT" ? val : 0;
          updatePayload.max_discount_vnd = maxDisc;
          updatePayload.min_order_vnd = minOrder;
          updatePayload.per_user_limit = perUser;
          updatePayload.starts_at = startDate.toISOString();
        }
        const res = await api<{ voucher?: GwSellerVoucher }>(
          `/api/seller/vouchers/${encodeURIComponent(editingVoucher.id)}`,
          {
            method: "PUT",
            body: updatePayload,
          }
        );
        if (res.voucher) {
          setVouchers((prev) => prev.map((it) => (it.id === res.voucher!.id ? res.voucher! : it)));
        } else {
          await reloadVouchers();
        }
        resetVoucherForm();
        setShowVoucherForm(false);
        showToast(`Đã cập nhật voucher ${editingVoucher.code}.`);
      } else {
        const res = await api<{ voucher?: GwSellerVoucher }>("/api/seller/vouchers", {
          method: "POST",
          body: {
            code,
            name: displayName,
            discount_type: vType,
            value_vnd: vType === "FIXED" ? val : 0,
            discount_percent: vType === "PERCENT" ? val : 0,
            max_discount_vnd: maxDisc,
            min_order_vnd: minOrder,
            quota,
            per_user_limit: perUser,
            budget_vnd: budgetVnd,
            starts_at: startDate.toISOString(),
            ends_at: endDate.toISOString(),
          },
        });
        if (res.voucher) {
          setVouchers((prev) => [res.voucher!, ...prev]);
        } else {
          await reloadVouchers();
        }
        resetVoucherForm();
        setShowVoucherForm(false);
        showToast(`Đã phát hành mã ${code}.`);
      }
    } catch (err) {
      setVError(
        formatPromotionErrorMessage(err, "seller_voucher", "Không thể lưu voucher lúc này.")
      );
    } finally {
      setVBusy(false);
    }
  };

  const handleToggleVoucherStatus = async (v: GwSellerVoucher) => {
    const currentStatus = getVoucherStatusKey(v);
    if (currentStatus === "DELETED") return;
    const nextStatus = currentStatus === "ACTIVE" ? "PAUSED" : "ACTIVE";
    setVoucherActionBusyId(v.id);
    try {
      const res = await api<{ voucher?: GwSellerVoucher }>(
        `/api/seller/vouchers/${encodeURIComponent(v.id)}/status`,
        {
          method: "PATCH",
          body: { status: nextStatus },
        }
      );
      if (res.voucher) {
        setVouchers((prev) => prev.map((it) => (it.id === v.id ? res.voucher! : it)));
      } else {
        await reloadVouchers();
      }
      showToast(
        nextStatus === "PAUSED"
          ? `Đã tạm dừng voucher ${v.code}.`
          : `Đã kích hoạt lại voucher ${v.code}.`
      );
    } catch (err) {
      showToast(
        formatPromotionErrorMessage(err, "seller_voucher", "Không thể cập nhật trạng thái voucher.")
      );
    } finally {
      setVoucherActionBusyId(null);
    }
  };

  const handleDeleteVoucher = async (v: GwSellerVoucher) => {
    setVoucherActionBusyId(v.id);
    try {
      await api(`/api/seller/vouchers/${encodeURIComponent(v.id)}`, { method: "DELETE" });
      setConfirmDeleteVoucher(null);
      await reloadVouchers();
      showToast(`Đã xóa voucher ${v.code}.`);
    } catch (err) {
      showToast(
        formatPromotionErrorMessage(err, "seller_voucher", "Không thể xóa voucher lúc này.")
      );
    } finally {
      setVoucherActionBusyId(null);
    }
  };

  const handleRegisterFlash = async (e: React.FormEvent) => {
    e.preventDefault();
    const entry = selectableFlashSkus.find((x) => x.sku.id === fSkuId);
    const slot = flashSales.find((s) => s.id === fSlotId);
    if (!entry || !slot) {
      setFError("Vui lòng chọn khung giờ và SKU hợp lệ.");
      return;
    }
    const flashPrice = Math.trunc(Number(fPriceStr) || 0);
    const totalSlots = Math.trunc(Number(fQuotaStr) || 0);
    const limitPerBuyer = Math.max(1, Math.trunc(Number(fLimitStr) || 1));

    if (flashPrice <= 0 || flashPrice >= entry.sku.price_vnd) {
      setFError(`Giá Flash sale phải nhỏ hơn ${formatVnd(entry.sku.price_vnd)}.`);
      return;
    }
    const minDiscountPct = slot.min_discount_percent ?? 0;
    if (minDiscountPct > 0) {
      const has30dMin =
        typeof entry.sku.min_price_30d_vnd === "number" && entry.sku.min_price_30d_vnd > 0;
      const basePrice = has30dMin
        ? Math.min(entry.sku.price_vnd, entry.sku.min_price_30d_vnd!)
        : entry.sku.price_vnd;
      const maxAllowedPrice = Math.trunc((basePrice * (100 - minDiscountPct)) / 100);
      if (flashPrice > maxAllowedPrice) {
        setFError(
          `Khung giờ yêu cầu giảm tối thiểu ${minDiscountPct}% so với giá thấp nhất 30 ngày (${formatVnd(basePrice)} → giá tối đa ${formatVnd(maxAllowedPrice)}).`
        );
        return;
      }
    }
    if (totalSlots < 1 || totalSlots > entry.sku.available_stock) {
      setFError(`Số suất từ 1 đến ${formatInt(entry.sku.available_stock)}.`);
      return;
    }

    setFError(null);
    setFBusy(true);
    try {
      await api<{ item?: GwFlashSaleItem }>(
        `/api/seller/flash-sales/${encodeURIComponent(slot.id)}/items`,
        {
          method: "POST",
          body: {
            sku_id: entry.sku.id,
            flash_price_vnd: flashPrice,
            total_slots: totalSlots,
            per_user_limit: limitPerBuyer,
          },
        }
      );
      setShowFlashForm(false);
      setSelectedSlotId(slot.id);
      showToast(`Đã gửi đăng ký ${entry.sku.sku_code} vào ${slot.name} (Chờ duyệt).`);
      await reloadFlashItems();
    } catch (err) {
      setFError(
        formatPromotionErrorMessage(
          err,
          "seller_flash_sale",
          "Không thể đăng ký Flash sale lúc này."
        )
      );
    } finally {
      setFBusy(false);
    }
  };

  const primaryAction =
    tab === "vouchers" ? (
      <Button
        type="button"
        size="sm"
        onClick={() => {
          resetVoucherForm();
          setShowVoucherForm(true);
        }}
      >
        <Plus className="size-4" aria-hidden="true" />
        <span>Tạo voucher</span>
      </Button>
    ) : tab === "programs" ? (
      <Button type="button" size="sm" onClick={() => setShowProgramForm(true)}>
        <Plus className="size-4" aria-hidden="true" />
        <span>Tạo giảm giá shop</span>
      </Button>
    ) : (
      <Button
        type="button"
        size="sm"
        disabled={openFlashSales.length === 0 || selectableFlashSkus.length === 0}
        onClick={() => {
          setFError(null);
          setShowFlashForm(true);
        }}
      >
        <Plus className="size-4" aria-hidden="true" />
        <span>Đăng ký Flash sale</span>
      </Button>
    );

  return (
    <div className="space-y-5">
      <PageHeader
        title="Khuyến mãi"
        subtitle="Quản lý mã giảm giá, chương trình giảm giá gian hàng và suất Flash sale."
        primaryAction={!uiState && !loadError ? primaryAction : undefined}
      />

      {uiState === "loading" && (
        <Card role="status" aria-label="Đang tải khuyến mãi...">
          <CardContent className="space-y-3 py-6">
            <Skeleton className="h-6 w-40" />
            {Array.from({ length: 5 }).map((_, idx) => (
              <Skeleton key={idx} className="h-10 w-full" />
            ))}
          </CardContent>
        </Card>
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <ErrorState
          title="Không thể tải khuyến mãi"
          description={loadError ?? "Hệ thống khuyến mãi tạm gián đoạn."}
          onRetry={() => router.refresh()}
        />
      )}

      {!uiState && !loadError && (
        <Tabs value={tab} onValueChange={(v) => setTab(v as PromoTab)} className="space-y-5">
          <div className="-mx-1 overflow-x-auto px-1">
            <TabsList
              aria-label="Loại khuyến mãi"
              className="h-auto w-max min-w-full justify-start border-b border-border bg-transparent pb-2"
            >
              <TabsTrigger value="vouchers" className="min-h-10 gap-1.5 md:min-h-8">
                <span>Voucher</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(vouchers.length)}
                </span>
              </TabsTrigger>
              <TabsTrigger value="programs" className="min-h-10 gap-1.5 md:min-h-8">
                <span>Giảm giá shop</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(programsCount)}
                </span>
              </TabsTrigger>
              <TabsTrigger value="flash" className="min-h-10 gap-1.5 md:min-h-8">
                <span>Flash sale</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(sellerFlashItems.length)}
                </span>
              </TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="vouchers" className="space-y-4">
            <section aria-label="Tổng hợp voucher">
              <MetricStrip
                items={[
                  { label: "Đang bật", value: formatInt(activeVoucherCount) },
                  {
                    label: "Đã dùng / Phát hành",
                    value: `${formatInt(voucherUsedTotal)} / ${formatInt(voucherQuotaTotal)}`,
                  },
                  {
                    label: "Ngân sách đã chi",
                    value: <Money vnd={voucherBudgetUsedTotal} size="lg" />,
                  },
                ]}
              />
            </section>

            <section aria-label="Danh sách voucher">
              <IndexTable>
                {vouchers.length === 0 ? (
                  <div className="p-4">
                    <EmptyState
                      icon={Tag}
                      title="Chưa có mã giảm giá nào"
                      description="Tạo mã voucher để kích cầu đơn hàng trên site gian hàng của bạn."
                      action={
                        <Button
                          size="sm"
                          onClick={() => {
                            resetVoucherForm();
                            setShowVoucherForm(true);
                          }}
                        >
                          <Plus className="size-4" aria-hidden="true" />
                          <span>Tạo voucher</span>
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
                            <TableHead>Mã voucher</TableHead>
                            <TableHead>Điều kiện</TableHead>
                            <TableHead className="text-right">Mức giảm</TableHead>
                            <TableHead>Lượt đã dùng</TableHead>
                            <TableHead className="text-right">Ngân sách</TableHead>
                            <TableHead>Trạng thái</TableHead>
                            <TableHead className="text-right">Thao tác</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {vouchers.map((v) => {
                            const statusKey = getVoucherStatusKey(v);
                            const stMeta = VOUCHER_STATUS_META[statusKey] ?? {
                              label: statusKey,
                              tone: "neutral" as const,
                            };
                            const isDeleted = statusKey === "DELETED";
                            const isBusy = voucherActionBusyId === v.id;
                            const isPct = v.discount_type.toUpperCase() === "PERCENT";
                            const discountStr = isPct
                              ? `${v.discount_percent}%`
                              : formatVnd(v.value_vnd);
                            return (
                              <TableRow
                                key={v.id}
                                className="cursor-pointer"
                                onClick={() => setDrawerVoucherId(v.id)}
                              >
                                <TableCell>
                                  <button
                                    type="button"
                                    className="text-left font-semibold text-brand hover:underline"
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      setDrawerVoucherId(v.id);
                                    }}
                                  >
                                    {v.code}
                                  </button>
                                  {v.ends_at && (
                                    <div className="text-xs text-muted-foreground tabular-nums">
                                      Đến {formatDateTimeVi(v.ends_at)}
                                    </div>
                                  )}
                                </TableCell>
                                <TableCell className="max-w-[260px] whitespace-normal">
                                  <div
                                    className="line-clamp-1 font-medium text-foreground"
                                    title={v.name}
                                  >
                                    {v.name}
                                  </div>
                                  <div className="text-xs text-muted-foreground tabular-nums">
                                    Đơn từ {formatVnd(v.min_order_vnd)}
                                    {isPct && v.max_discount_vnd > 0
                                      ? `, tối đa ${formatVnd(v.max_discount_vnd)}`
                                      : ""}
                                  </div>
                                </TableCell>
                                <TableCell className="text-right font-semibold tabular-nums">
                                  {discountStr}
                                </TableCell>
                                <TableCell>
                                  <QuotaProgress
                                    value={v.used_count}
                                    max={Math.max(1, v.quota)}
                                    leftLabel={`${formatInt(v.used_count)} / ${formatInt(v.quota)}`}
                                    rightLabel={`Còn ${formatInt(v.remaining_quota)}`}
                                  />
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  <div>{formatVnd(v.used_budget_vnd)}</div>
                                  {v.budget_vnd > 0 && (
                                    <div className="text-xs text-muted-foreground">
                                      / {formatVnd(v.budget_vnd)}
                                    </div>
                                  )}
                                </TableCell>
                                <TableCell>
                                  <StatusBadge tone={voucherStatusTone(statusKey)}>
                                    {stMeta.label}
                                  </StatusBadge>
                                </TableCell>
                                <TableCell
                                  className="text-right"
                                  onClick={(e) => e.stopPropagation()}
                                >
                                  {isDeleted ? (
                                    <span className="text-xs text-muted-foreground">—</span>
                                  ) : (
                                    <div className="inline-flex items-center justify-end gap-1">
                                      <Button
                                        type="button"
                                        variant="ghost"
                                        size="xs"
                                        disabled={isBusy}
                                        onClick={() => handleOpenEditVoucher(v)}
                                      >
                                        Sửa
                                      </Button>
                                      <Button
                                        type="button"
                                        variant="ghost"
                                        size="xs"
                                        disabled={isBusy}
                                        onClick={() => void handleToggleVoucherStatus(v)}
                                      >
                                        {statusKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                                      </Button>
                                      <Button
                                        type="button"
                                        variant="ghost"
                                        size="xs"
                                        disabled={isBusy}
                                        className="text-destructive hover:text-destructive"
                                        onClick={() => setConfirmDeleteVoucher(v)}
                                      >
                                        Xóa
                                      </Button>
                                    </div>
                                  )}
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>

                    <div className="divide-y divide-border md:hidden">
                      {vouchers.map((v) => {
                        const statusKey = getVoucherStatusKey(v);
                        const stMeta = VOUCHER_STATUS_META[statusKey] ?? {
                          label: statusKey,
                          tone: "neutral" as const,
                        };
                        const isDeleted = statusKey === "DELETED";
                        const isBusy = voucherActionBusyId === v.id;
                        return (
                          <div key={v.id} className="space-y-2.5 px-4 py-3">
                            <div className="flex items-start justify-between gap-2">
                              <button
                                type="button"
                                onClick={() => setDrawerVoucherId(v.id)}
                                className="min-w-0 text-left"
                              >
                                <div className="font-semibold text-brand">{v.code}</div>
                                <div className="truncate text-xs text-muted-foreground">
                                  {v.name}
                                </div>
                              </button>
                              <StatusBadge tone={voucherStatusTone(statusKey)}>
                                {stMeta.label}
                              </StatusBadge>
                            </div>
                            <QuotaProgress
                              value={v.used_count}
                              max={Math.max(1, v.quota)}
                              leftLabel={`${formatInt(v.used_count)}/${formatInt(v.quota)}`}
                              rightLabel={`Còn ${formatInt(v.remaining_quota)}`}
                            />
                            {!isDeleted && (
                              <div className="flex flex-wrap items-center gap-2 pt-0.5">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  className="min-h-10"
                                  disabled={isBusy}
                                  onClick={() => handleOpenEditVoucher(v)}
                                >
                                  Sửa
                                </Button>
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  className="min-h-10"
                                  disabled={isBusy}
                                  onClick={() => void handleToggleVoucherStatus(v)}
                                >
                                  {statusKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="sm"
                                  disabled={isBusy}
                                  className="min-h-10 text-destructive hover:text-destructive"
                                  onClick={() => setConfirmDeleteVoucher(v)}
                                >
                                  Xóa
                                </Button>
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  </>
                )}
              </IndexTable>
            </section>
          </TabsContent>

          <TabsContent value="programs">
            <PromotionsDiscountProgramsTab
              shopName={shop?.name || "Gian hàng"}
              initialPrograms={initialDiscountPrograms}
              products={shopProducts}
              showForm={showProgramForm}
              onOpenForm={() => setShowProgramForm(true)}
              onCloseForm={() => setShowProgramForm(false)}
              onNotify={showToast}
              onCountChange={setProgramsCount}
            />
          </TabsContent>

          <TabsContent value="flash" className="space-y-4">
            {flashSales.length > 0 && (
              <div
                className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4"
                role="tablist"
                aria-label="Khung giờ Flash sale"
              >
                {flashSales.slice(0, 4).map((slot) => {
                  const countInSlot = sellerFlashItems.filter(
                    (r) => r.flash_sale_id === slot.id
                  ).length;
                  const selected = selectedSlotId === slot.id;
                  const st = flashStatusOf(slot, nowMs);
                  const stLabel =
                    st === "live" ? "Đang mở" : st === "upcoming" ? "Sắp mở" : "Đã kết thúc";
                  return (
                    <button
                      key={slot.id}
                      type="button"
                      role="tab"
                      aria-selected={selected}
                      onClick={() =>
                        setSelectedSlotId((prev) => (prev === slot.id ? "all" : slot.id))
                      }
                      className={cn(
                        "flex flex-col items-start gap-1 rounded-xl border bg-card px-4 py-3 text-left shadow-card transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        selected
                          ? "border-brand bg-brand/5"
                          : "border-transparent hover:border-border hover:bg-accent/40"
                      )}
                    >
                      <span className="line-clamp-2 text-xs text-muted-foreground">
                        {slot.name} ({stLabel}
                        {slot.min_discount_percent ? `, ≥${slot.min_discount_percent}%` : ""})
                      </span>
                      <span className="text-lg font-semibold text-foreground tabular-nums">
                        {formatInt(countInSlot)} SKU
                      </span>
                    </button>
                  );
                })}
              </div>
            )}

            <section aria-label="SKU tham gia Flash sale">
              <IndexTable
                tabs={[
                  { key: "ALL", label: "Tất cả", count: flashItemCounts.ALL },
                  { key: "PENDING", label: "Chờ duyệt", count: flashItemCounts.PENDING },
                  { key: "APPROVED", label: "Đã duyệt", count: flashItemCounts.APPROVED },
                  { key: "REJECTED", label: "Từ chối", count: flashItemCounts.REJECTED },
                ]}
                activeTab={flashStatusFilter}
                onTabChange={(k) => setFlashStatusFilter(k as FlashItemStatusFilter)}
                toolbar={
                  selectedSlotId !== "all" ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      onClick={() => setSelectedSlotId("all")}
                    >
                      Xem tất cả khung giờ
                    </Button>
                  ) : undefined
                }
              >
                {filteredFlashRows.length === 0 ? (
                  <div className="p-4">
                    <EmptyState
                      icon={Zap}
                      title="Chưa có SKU đăng ký khớp bộ lọc hiện tại"
                      description="Chọn khung giờ đang mở và đăng ký SKU đủ tồn kho để tham gia Flash sale."
                      action={
                        openFlashSales.length > 0 && selectableFlashSkus.length > 0 ? (
                          <Button
                            size="sm"
                            onClick={() => {
                              setFError(null);
                              setShowFlashForm(true);
                            }}
                          >
                            <Plus className="size-4" aria-hidden="true" />
                            <span>Đăng ký Flash sale</span>
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
                            <TableHead>Sản phẩm &amp; SKU</TableHead>
                            <TableHead>Khung giờ</TableHead>
                            <TableHead className="text-right">Giá Flash sale</TableHead>
                            <TableHead>Suất còn lại</TableHead>
                            <TableHead>Trạng thái</TableHead>
                            <TableHead>Lý do từ chối</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {filteredFlashRows.map((row) => {
                            const stMeta = FLASH_ITEM_STATUS_META[
                              row.item.status.toUpperCase()
                            ] ?? {
                              label: row.item.status,
                              tone: "neutral" as const,
                            };
                            const used = Math.max(
                              0,
                              row.item.total_slots - row.item.remaining_slots
                            );
                            return (
                              <TableRow key={row.item.id}>
                                <TableCell className="max-w-[280px] whitespace-normal">
                                  <div className="flex items-center gap-3">
                                    {row.thumb ? (
                                      <img
                                        src={row.thumb}
                                        alt={row.productName}
                                        className="size-10 shrink-0 rounded-md border border-border bg-muted object-cover"
                                        width={40}
                                        height={40}
                                      />
                                    ) : (
                                      <span
                                        className="flex size-10 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground"
                                        aria-hidden="true"
                                      >
                                        <Package className="size-4" />
                                      </span>
                                    )}
                                    <div className="min-w-0">
                                      <div
                                        className="line-clamp-1 font-medium text-foreground"
                                        title={row.productName}
                                      >
                                        {row.productName}
                                      </div>
                                      <div className="text-xs text-muted-foreground">
                                        {row.skuCode}, tối đa {formatInt(row.item.per_user_limit)}
                                        /khách
                                      </div>
                                    </div>
                                  </div>
                                </TableCell>
                                <TableCell className="whitespace-normal text-muted-foreground">
                                  {row.windowName}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  <div className="flex items-center justify-end gap-1.5">
                                    <Money vnd={row.item.flash_price_vnd} />
                                    {row.originalPrice > row.item.flash_price_vnd && (
                                      <Badge variant="warning">
                                        {formatDiscountPercent(
                                          row.originalPrice,
                                          row.item.flash_price_vnd
                                        )}
                                      </Badge>
                                    )}
                                  </div>
                                </TableCell>
                                <TableCell>
                                  <QuotaProgress
                                    value={used}
                                    max={Math.max(1, row.item.total_slots)}
                                    leftLabel={`${formatInt(used)}/${formatInt(row.item.total_slots)}`}
                                    rightLabel={`Còn ${formatInt(row.item.remaining_slots)}`}
                                  />
                                </TableCell>
                                <TableCell>
                                  <StatusBadge tone={flashItemStatusTone(row.item.status)}>
                                    {stMeta.label}
                                  </StatusBadge>
                                </TableCell>
                                <TableCell className="max-w-[200px] whitespace-normal">
                                  {row.item.rejection_reason ? (
                                    <span className="text-xs text-destructive">
                                      {row.item.rejection_reason}
                                    </span>
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

                    <div className="md:hidden">
                      {filteredFlashRows.map((row) => {
                        const stMeta = FLASH_ITEM_STATUS_META[row.item.status.toUpperCase()] ?? {
                          label: row.item.status,
                          tone: "neutral" as const,
                        };
                        return (
                          <IndexRowCard
                            key={row.item.id}
                            media={
                              row.thumb ? (
                                <img
                                  src={row.thumb}
                                  alt={row.productName}
                                  className="size-10 rounded-md border border-border bg-muted object-cover"
                                />
                              ) : (
                                <span
                                  className="flex size-10 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground"
                                  aria-hidden="true"
                                >
                                  <Percent className="size-4" />
                                </span>
                              )
                            }
                            title={row.productName}
                            badge={
                              <StatusBadge tone={flashItemStatusTone(row.item.status)}>
                                {stMeta.label}
                              </StatusBadge>
                            }
                            meta={
                              <>
                                <Money vnd={row.item.flash_price_vnd} size="sm" />
                                <span>·</span>
                                <span className="tabular-nums">
                                  Còn {formatInt(row.item.remaining_slots)} /{" "}
                                  {formatInt(row.item.total_slots)}
                                </span>
                                <span>·</span>
                                <span>{row.windowName}</span>
                                {row.item.rejection_reason && (
                                  <span className="w-full text-destructive">
                                    {row.item.rejection_reason}
                                  </span>
                                )}
                              </>
                            }
                          />
                        );
                      })}
                    </div>
                  </>
                )}
              </IndexTable>
            </section>
          </TabsContent>
        </Tabs>
      )}

      {/* Voucher Create / Edit Sheet */}
      <Sheet
        open={showVoucherForm}
        onOpenChange={(open) => {
          if (!open) {
            resetVoucherForm();
            setShowVoucherForm(false);
          }
        }}
      >
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
          <form
            onSubmit={(e) => void handleSaveVoucher(e)}
            aria-label="Biểu mẫu voucher"
            noValidate
            className="flex min-h-full flex-col"
          >
            <SheetHeader>
              <SheetTitle>
                {editingVoucher
                  ? `Chỉnh sửa voucher ${editingVoucher.code}`
                  : "Tạo mã giảm giá mới"}
              </SheetTitle>
              <SheetDescription>
                {editingVoucher && editingVoucher.used_count > 0
                  ? `Voucher đã có ${formatInt(editingVoucher.used_count)} lượt sử dụng — chỉ được sửa tên hiển thị, tăng tổng lượt/ngân sách và gia hạn kết thúc.`
                  : "Thiết lập điều kiện áp dụng, mức giảm và ngân sách cho mã voucher gian hàng."}
              </SheetDescription>
            </SheetHeader>

            <div className="flex-1 px-4 pb-4">
              <FieldGroup className="gap-4">
                {vError ? <FieldError>{vError}</FieldError> : null}

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="v-code">Mã voucher</FieldLabel>
                    <Input
                      id="v-code"
                      type="text"
                      value={vCode}
                      onChange={(e) => setVCode(e.target.value)}
                      placeholder="SHOP30K"
                      disabled={Boolean(editingVoucher)}
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="v-name">Tên hiển thị (tùy chọn)</FieldLabel>
                    <Input
                      id="v-name"
                      type="text"
                      value={vName}
                      onChange={(e) => setVName(e.target.value)}
                      placeholder="Giảm 25.000₫ đơn từ 300k"
                    />
                  </Field>
                </div>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="v-type">Kiểu giảm</FieldLabel>
                    <Select
                      value={vType}
                      onValueChange={(val) => setVType(val as "FIXED" | "PERCENT")}
                      disabled={
                        Boolean(editingVoucher) &&
                        !canEditVoucherField(editingVoucher!, "discount_type")
                      }
                    >
                      <SelectTrigger id="v-type" className="w-full bg-card">
                        <SelectValue>
                          {vType === "FIXED" ? "Số tiền cố định (VND)" : "Theo phần trăm (%)"}
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="FIXED">Số tiền cố định (VND)</SelectItem>
                        <SelectItem value="PERCENT">Theo phần trăm (%)</SelectItem>
                      </SelectContent>
                    </Select>
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="v-val">
                      {vType === "FIXED" ? "Mức giảm (VND)" : "Phần trăm (%)"}
                    </FieldLabel>
                    <Input
                      id="v-val"
                      type="number"
                      step={1}
                      value={vValue}
                      onChange={(e) => setVValue(e.target.value)}
                      disabled={
                        Boolean(editingVoucher) &&
                        !canEditVoucherField(
                          editingVoucher!,
                          vType === "FIXED" ? "value_vnd" : "discount_percent"
                        )
                      }
                      className="tabular-nums"
                    />
                  </Field>
                </div>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  {vType === "PERCENT" && (
                    <Field>
                      <FieldLabel htmlFor="v-max">Giảm tối đa (VND)</FieldLabel>
                      <Input
                        id="v-max"
                        type="number"
                        min={0}
                        step={1}
                        value={vMaxDiscount}
                        onChange={(e) => setVMaxDiscount(e.target.value)}
                        disabled={
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(editingVoucher!, "max_discount_vnd")
                        }
                        className="tabular-nums"
                      />
                    </Field>
                  )}

                  <Field>
                    <FieldLabel htmlFor="v-min">Đơn tối thiểu (VND)</FieldLabel>
                    <Input
                      id="v-min"
                      type="number"
                      min={0}
                      step={1}
                      value={vMinOrder}
                      onChange={(e) => setVMinOrder(e.target.value)}
                      disabled={
                        Boolean(editingVoucher) &&
                        !canEditVoucherField(editingVoucher!, "min_order_vnd")
                      }
                      className="tabular-nums"
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="v-quota">Tổng lượt phát hành</FieldLabel>
                    <Input
                      id="v-quota"
                      type="number"
                      min={editingVoucher ? Math.max(1, editingVoucher.used_count) : 1}
                      step={1}
                      value={vQuota}
                      onChange={(e) => setVQuota(e.target.value)}
                      className="tabular-nums"
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="v-per-user">Giới hạn mỗi khách</FieldLabel>
                    <Input
                      id="v-per-user"
                      type="number"
                      min={1}
                      step={1}
                      value={vPerUser}
                      onChange={(e) => setVPerUser(e.target.value)}
                      disabled={
                        Boolean(editingVoucher) &&
                        !canEditVoucherField(editingVoucher!, "per_user_limit")
                      }
                      className="tabular-nums"
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="v-budget">Ngân sách tối đa (VND)</FieldLabel>
                    <Input
                      id="v-budget"
                      type="number"
                      min={0}
                      step={1}
                      value={vBudget}
                      onChange={(e) => setVBudget(e.target.value)}
                      className="tabular-nums"
                    />
                  </Field>
                </div>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="v-start">Bắt đầu áp dụng</FieldLabel>
                    <Input
                      id="v-start"
                      type="datetime-local"
                      value={vStartsAt}
                      onChange={(e) => setVStartsAt(e.target.value)}
                      disabled={
                        Boolean(editingVoucher) &&
                        !canEditVoucherField(editingVoucher!, "starts_at")
                      }
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="v-end">Kết thúc áp dụng</FieldLabel>
                    <Input
                      id="v-end"
                      type="datetime-local"
                      value={vEndsAt}
                      onChange={(e) => setVEndsAt(e.target.value)}
                    />
                  </Field>
                </div>
              </FieldGroup>
            </div>

            <SheetFooter className="flex-row justify-end gap-2 border-t border-border">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  resetVoucherForm();
                  setShowVoucherForm(false);
                }}
              >
                Hủy
              </Button>
              <Button type="submit" size="sm" disabled={vBusy}>
                {vBusy ? "Đang lưu..." : editingVoucher ? "Lưu thay đổi" : "Phát hành mã"}
              </Button>
            </SheetFooter>
          </form>
        </SheetContent>
      </Sheet>

      {/* Flash Sale Registration Sheet */}
      <Sheet open={showFlashForm} onOpenChange={(open) => !open && setShowFlashForm(false)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
          <form
            onSubmit={(e) => void handleRegisterFlash(e)}
            aria-label="Đăng ký SKU vào Flash sale"
            noValidate
            className="flex min-h-full flex-col"
          >
            <SheetHeader>
              <SheetTitle>Đăng ký suất Flash sale</SheetTitle>
              <SheetDescription>
                {selectedFlashWindow?.min_discount_percent
                  ? `Khung giờ yêu cầu giảm tối thiểu ${selectedFlashWindow.min_discount_percent}%`
                  : "Quản trị viên duyệt suất Flash sale trước khi mở bán."}
              </SheetDescription>
            </SheetHeader>

            <div className="flex-1 px-4 pb-4">
              <FieldGroup className="gap-4">
                {fError ? <FieldError>{fError}</FieldError> : null}

                <Field>
                  <FieldLabel htmlFor="f-slot">Khung giờ</FieldLabel>
                  <Select value={fSlotId || undefined} onValueChange={setFSlotId}>
                    <SelectTrigger id="f-slot" className="w-full bg-card">
                      <SelectValue placeholder="Chọn khung giờ Flash sale">
                        {(() => {
                          const slot = openFlashSales.find((s) => s.id === fSlotId);
                          if (!slot) return null;
                          return `${slot.name}${slot.min_discount_percent ? ` (giảm ≥${slot.min_discount_percent}%)` : ""}`;
                        })()}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {openFlashSales.map((slot) => (
                        <SelectItem key={slot.id} value={slot.id}>
                          {slot.name}
                          {slot.min_discount_percent
                            ? ` (giảm ≥${slot.min_discount_percent}%)`
                            : ""}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>

                <Field>
                  <FieldLabel htmlFor="f-sku">Biến thể SKU</FieldLabel>
                  <Select value={fSkuId || undefined} onValueChange={setFSkuId}>
                    <SelectTrigger id="f-sku" className="w-full bg-card">
                      <SelectValue placeholder="Chọn biến thể SKU">
                        {selectedFlashSkuEntry
                          ? `${selectedFlashSkuEntry.product.name} (${selectedFlashSkuEntry.sku.sku_code}, ${formatVnd(selectedFlashSkuEntry.sku.price_vnd)}, tồn ${selectedFlashSkuEntry.sku.available_stock})`
                          : null}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {selectableFlashSkus.map(({ product, sku }) => (
                        <SelectItem key={sku.id} value={sku.id}>
                          {product.name} ({sku.sku_code}, {formatVnd(sku.price_vnd)}, tồn{" "}
                          {sku.available_stock})
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {selectedFlashSkuEntry && (
                    <FieldDescription className="text-xs tabular-nums">
                      Giá gốc: {formatVnd(selectedFlashSkuEntry.sku.price_vnd)}
                      {selectedFlashSkuEntry.sku.min_price_30d_vnd
                        ? ` · Thấp nhất 30 ngày: ${formatVnd(selectedFlashSkuEntry.sku.min_price_30d_vnd)}`
                        : ""}
                    </FieldDescription>
                  )}
                </Field>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="f-price">Giá Flash sale (VND)</FieldLabel>
                    <Input
                      id="f-price"
                      type="number"
                      min={1}
                      step={1}
                      value={fPriceStr}
                      onChange={(e) => setFPriceStr(e.target.value)}
                      className="tabular-nums"
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="f-quota">Suất cam kết</FieldLabel>
                    <Input
                      id="f-quota"
                      type="number"
                      min={1}
                      step={1}
                      value={fQuotaStr}
                      onChange={(e) => setFQuotaStr(e.target.value)}
                      className="tabular-nums"
                    />
                  </Field>
                </div>

                <Field>
                  <FieldLabel htmlFor="f-limit">Giới hạn mỗi khách</FieldLabel>
                  <Input
                    id="f-limit"
                    type="number"
                    min={1}
                    max={10}
                    step={1}
                    value={fLimitStr}
                    onChange={(e) => setFLimitStr(e.target.value)}
                    className="tabular-nums"
                  />
                </Field>
              </FieldGroup>
            </div>

            <SheetFooter className="flex-row justify-end gap-2 border-t border-border">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setShowFlashForm(false)}
              >
                Hủy
              </Button>
              <Button type="submit" size="sm" disabled={fBusy}>
                {fBusy ? "Đang gửi..." : "Đăng ký suất"}
              </Button>
            </SheetFooter>
          </form>
        </SheetContent>
      </Sheet>

      {/* Quick Voucher Detail Sheet */}
      <Sheet
        open={Boolean(drawerVoucher)}
        onOpenChange={(open) => !open && setDrawerVoucherId(null)}
      >
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-md">
          {drawerVoucher ? (
            <>
              <SheetHeader>
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>{drawerVoucher.code}</SheetTitle>
                  <StatusBadge tone={voucherStatusTone(getVoucherStatusKey(drawerVoucher))}>
                    {
                      (
                        VOUCHER_STATUS_META[getVoucherStatusKey(drawerVoucher)] ?? {
                          label: getVoucherStatusKey(drawerVoucher),
                        }
                      ).label
                    }
                  </StatusBadge>
                </div>
                <SheetDescription>{drawerVoucher.name}</SheetDescription>
              </SheetHeader>

              <div className="px-4">
                <dl className="divide-y divide-border text-sm">
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-[13px] text-muted-foreground">Mức giảm</dt>
                    <dd className="font-semibold text-foreground tabular-nums">
                      {drawerVoucher.discount_type.toUpperCase() === "PERCENT"
                        ? `${drawerVoucher.discount_percent}%`
                        : formatVnd(drawerVoucher.value_vnd)}
                    </dd>
                  </div>
                  {drawerVoucher.max_discount_vnd > 0 && (
                    <div className="flex items-center justify-between gap-4 py-2.5">
                      <dt className="text-[13px] text-muted-foreground">Giảm tối đa</dt>
                      <dd className="text-foreground tabular-nums">
                        {formatVnd(drawerVoucher.max_discount_vnd)}
                      </dd>
                    </div>
                  )}
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-[13px] text-muted-foreground">Đơn tối thiểu</dt>
                    <dd className="text-foreground tabular-nums">
                      {formatVnd(drawerVoucher.min_order_vnd)}
                    </dd>
                  </div>
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-[13px] text-muted-foreground">Đã dùng / Phát hành</dt>
                    <dd className="text-foreground tabular-nums">
                      {formatInt(drawerVoucher.used_count)} / {formatInt(drawerVoucher.quota)}
                    </dd>
                  </div>
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-[13px] text-muted-foreground">Ngân sách đã dùng</dt>
                    <dd className="text-foreground tabular-nums">
                      {formatVnd(drawerVoucher.used_budget_vnd)}
                      {drawerVoucher.budget_vnd > 0
                        ? ` / ${formatVnd(drawerVoucher.budget_vnd)}`
                        : ""}
                    </dd>
                  </div>
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-[13px] text-muted-foreground">Giới hạn mỗi khách</dt>
                    <dd className="text-foreground tabular-nums">
                      {formatInt(drawerVoucher.per_user_limit)}
                    </dd>
                  </div>
                  {drawerVoucher.starts_at && (
                    <div className="flex items-center justify-between gap-4 py-2.5">
                      <dt className="text-[13px] text-muted-foreground">Bắt đầu</dt>
                      <dd className="text-foreground tabular-nums">
                        {formatDateTimeVi(drawerVoucher.starts_at)}
                      </dd>
                    </div>
                  )}
                  {drawerVoucher.ends_at && (
                    <div className="flex items-center justify-between gap-4 py-2.5">
                      <dt className="text-[13px] text-muted-foreground">Kết thúc</dt>
                      <dd className="text-foreground tabular-nums">
                        {formatDateTimeVi(drawerVoucher.ends_at)}
                      </dd>
                    </div>
                  )}
                </dl>
              </div>

              <SheetFooter className="flex-row justify-end gap-2 border-t border-border">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => handleCopyCode(drawerVoucher.code)}
                >
                  <Copy className="size-3.5" aria-hidden="true" />
                  <span>Sao chép mã</span>
                </Button>
                {getVoucherStatusKey(drawerVoucher) !== "DELETED" && (
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => {
                      const target = drawerVoucher;
                      setDrawerVoucherId(null);
                      handleOpenEditVoucher(target);
                    }}
                  >
                    Chỉnh sửa
                  </Button>
                )}
              </SheetFooter>
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      {/* Delete Voucher Confirmation */}
      <AlertDialog
        open={Boolean(confirmDeleteVoucher)}
        onOpenChange={(open) => !open && setConfirmDeleteVoucher(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Xóa voucher {confirmDeleteVoucher?.code}?</AlertDialogTitle>
            <AlertDialogDescription>
              Mã giảm giá sau khi xóa sẽ ngừng áp dụng trên gian hàng và không thể khôi phục.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Hủy</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={!confirmDeleteVoucher || voucherActionBusyId === confirmDeleteVoucher.id}
              onClick={() => {
                if (confirmDeleteVoucher) {
                  void handleDeleteVoucher(confirmDeleteVoucher);
                }
              }}
            >
              Xác nhận xóa
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
