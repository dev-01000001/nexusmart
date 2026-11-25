"use client";

import React, { useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconCopy, IconPackage, IconPlus, ProgressBar, StateView } from "@/components";
import { api } from "@/lib/api";
import { formatDateTimeVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import { useSellerContext } from "./SellerShell";
import { SellerDrawer, SellerSlidingTabs, statusPillClass } from "./SellerUiPrimitives";
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
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/khuyen-mai/page.module.css";

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
  const [confirmDeleteVoucherId, setConfirmDeleteVoucherId] = useState<string | null>(null);
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
      setConfirmDeleteVoucherId(null);
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

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <h1 className={common.title}>Khuyến mãi</h1>
        <div className={common.headerActions}>
          {tab === "vouchers" && (
            <Button
              variant="primary"
              size="sm"
              shape="app"
              onClick={() => {
                if (showVoucherForm) {
                  resetVoucherForm();
                  setShowVoucherForm(false);
                } else {
                  resetVoucherForm();
                  setShowVoucherForm(true);
                }
              }}
            >
              <IconPlus size={14} />
              <span>{showVoucherForm ? "Đóng biểu mẫu" : "Tạo voucher"}</span>
            </Button>
          )}
          {tab === "programs" && (
            <Button
              variant="primary"
              size="sm"
              shape="app"
              onClick={() => setShowProgramForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showProgramForm ? "Đóng biểu mẫu" : "Tạo giảm giá shop"}</span>
            </Button>
          )}
          {tab === "flash" && (
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={openFlashSales.length === 0 || selectableFlashSkus.length === 0}
              onClick={() => setShowFlashForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showFlashForm ? "Đóng biểu mẫu" : "Đăng ký Flash sale"}</span>
            </Button>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải khuyến mãi..."
          rows={5}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải khuyến mãi"
          message={loadError ?? "Hệ thống khuyến mãi tạm gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {!uiState && !loadError && (
        <>
          <SellerSlidingTabs
            ariaLabel="Loại khuyến mãi"
            value={tab}
            onChange={setTab}
            tabs={[
              { key: "vouchers", label: "Voucher", count: vouchers.length },
              { key: "programs", label: "Giảm giá shop", count: programsCount },
              { key: "flash", label: "Flash sale", count: sellerFlashItems.length },
            ]}
          />

          {tab === "vouchers" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp voucher">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đang bật</span>
                  <span
                    className={`${common.balanceValue} num ${activeVoucherCount === 0 ? common.balanceValueZero : ""}`}
                    data-reveal="count"
                    data-count-to={activeVoucherCount}
                  >
                    {formatInt(activeVoucherCount)}
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đã dùng / Phát hành</span>
                  <span className={`${common.balanceValue} num`}>
                    {formatInt(voucherUsedTotal)} / {formatInt(voucherQuotaTotal)}
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Ngân sách đã chi</span>
                  <span
                    className={`${common.balanceValue} num ${voucherBudgetUsedTotal === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatVnd(voucherBudgetUsedTotal)}
                  </span>
                </div>
              </section>

              {showVoucherForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={(e) => void handleSaveVoucher(e)}
                  aria-label="Biểu mẫu voucher"
                  noValidate
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>
                      {editingVoucher
                        ? `Chỉnh sửa voucher ${editingVoucher.code}`
                        : "Tạo mã giảm giá mới"}
                    </h2>
                  </div>

                  {editingVoucher && editingVoucher.used_count > 0 && (
                    <p className={common.fieldHint} style={{ marginBottom: 12 }}>
                      Voucher đã có {formatInt(editingVoucher.used_count)} lượt sử dụng — chỉ được
                      sửa tên hiển thị, tăng tổng lượt/ngân sách và gia hạn kết thúc.
                    </p>
                  )}

                  {vError && (
                    <p className={common.fieldError} role="alert" style={{ marginBottom: 12 }}>
                      {vError}
                    </p>
                  )}

                  <div className={styles.formGrid4}>
                    <div className={common.field}>
                      <label htmlFor="v-code" className={common.fieldLabel}>
                        Mã voucher
                      </label>
                      <input
                        id="v-code"
                        type="text"
                        className={common.textInput}
                        value={vCode}
                        onChange={(e) => setVCode(e.target.value)}
                        placeholder="SHOP30K"
                        disabled={Boolean(editingVoucher)}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-name" className={common.fieldLabel}>
                        Tên hiển thị (tùy chọn)
                      </label>
                      <input
                        id="v-name"
                        type="text"
                        className={common.textInput}
                        value={vName}
                        onChange={(e) => setVName(e.target.value)}
                        placeholder="Giảm 25.000₫ đơn từ 300k"
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-type" className={common.fieldLabel}>
                        Kiểu giảm
                      </label>
                      <select
                        id="v-type"
                        className={common.textInput}
                        value={vType}
                        onChange={(e) => setVType(e.target.value as "FIXED" | "PERCENT")}
                        disabled={
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(editingVoucher!, "discount_type")
                        }
                      >
                        <option value="FIXED">Số tiền cố định (VND)</option>
                        <option value="PERCENT">Theo phần trăm (%)</option>
                      </select>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-val" className={common.fieldLabel}>
                        {vType === "FIXED" ? "Mức giảm (VND)" : "Phần trăm (%)"}
                      </label>
                      <input
                        id="v-val"
                        type="number"
                        step={1}
                        className={common.textInput}
                        value={vValue}
                        onChange={(e) => setVValue(e.target.value)}
                        disabled={
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(
                            editingVoucher!,
                            vType === "FIXED" ? "value_vnd" : "discount_percent"
                          )
                        }
                      />
                    </div>
                  </div>

                  <div className={styles.formGrid4}>
                    {vType === "PERCENT" && (
                      <div className={common.field}>
                        <label htmlFor="v-max" className={common.fieldLabel}>
                          Giảm tối đa (VND)
                        </label>
                        <input
                          id="v-max"
                          type="number"
                          min={0}
                          step={1}
                          className={common.textInput}
                          value={vMaxDiscount}
                          onChange={(e) => setVMaxDiscount(e.target.value)}
                          disabled={
                            Boolean(editingVoucher) &&
                            !canEditVoucherField(editingVoucher!, "max_discount_vnd")
                          }
                        />
                      </div>
                    )}
                    <div className={common.field}>
                      <label htmlFor="v-min" className={common.fieldLabel}>
                        Đơn tối thiểu (VND)
                      </label>
                      <input
                        id="v-min"
                        type="number"
                        min={0}
                        step={1}
                        className={common.textInput}
                        value={vMinOrder}
                        onChange={(e) => setVMinOrder(e.target.value)}
                        disabled={
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(editingVoucher!, "min_order_vnd")
                        }
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-quota" className={common.fieldLabel}>
                        Tổng lượt phát hành
                      </label>
                      <input
                        id="v-quota"
                        type="number"
                        min={editingVoucher ? Math.max(1, editingVoucher.used_count) : 1}
                        step={1}
                        className={common.textInput}
                        value={vQuota}
                        onChange={(e) => setVQuota(e.target.value)}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-per-user" className={common.fieldLabel}>
                        Giới hạn mỗi khách
                      </label>
                      <input
                        id="v-per-user"
                        type="number"
                        min={1}
                        step={1}
                        className={common.textInput}
                        value={vPerUser}
                        onChange={(e) => setVPerUser(e.target.value)}
                        disabled={
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(editingVoucher!, "per_user_limit")
                        }
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-budget" className={common.fieldLabel}>
                        Ngân sách tối đa (VND)
                      </label>
                      <input
                        id="v-budget"
                        type="number"
                        min={0}
                        step={1}
                        className={common.textInput}
                        value={vBudget}
                        onChange={(e) => setVBudget(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className={styles.formGrid3}>
                    <div className={common.field}>
                      <label htmlFor="v-start" className={common.fieldLabel}>
                        Bắt đầu áp dụng
                      </label>
                      <input
                        id="v-start"
                        type="datetime-local"
                        className={common.textInput}
                        value={vStartsAt}
                        onChange={(e) => setVStartsAt(e.target.value)}
                        disabled={
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(editingVoucher!, "starts_at")
                        }
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-end" className={common.fieldLabel}>
                        Kết thúc áp dụng
                      </label>
                      <input
                        id="v-end"
                        type="datetime-local"
                        className={common.textInput}
                        value={vEndsAt}
                        onChange={(e) => setVEndsAt(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className={styles.formActions}>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      shape="app"
                      onClick={() => {
                        resetVoucherForm();
                        setShowVoucherForm(false);
                      }}
                    >
                      Hủy
                    </Button>
                    <Button type="submit" variant="primary" size="sm" shape="app" disabled={vBusy}>
                      {vBusy ? "Đang lưu..." : editingVoucher ? "Lưu thay đổi" : "Phát hành mã"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách voucher">
                {vouchers.length === 0 ? (
                  <div className={common.inlineEmpty}>Chưa có mã giảm giá nào.</div>
                ) : (
                  <>
                    <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            <th>Mã voucher</th>
                            <th>Điều kiện</th>
                            <th className={common.numCol}>Mức giảm</th>
                            <th>Lượt đã dùng</th>
                            <th className={common.numCol}>Ngân sách</th>
                            <th>Trạng thái</th>
                            <th className={common.numCol}>Thao tác</th>
                          </tr>
                        </thead>
                        <tbody>
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
                              <tr key={v.id}>
                                <td>
                                  <button
                                    type="button"
                                    className={common.rowTriggerBtn}
                                    onClick={() => setDrawerVoucherId(v.id)}
                                  >
                                    {v.code}
                                  </button>
                                  {v.ends_at && (
                                    <span className={common.cellSub}>
                                      Đến {formatDateTimeVi(v.ends_at)}
                                    </span>
                                  )}
                                </td>
                                <td className={styles.condCell}>
                                  <span className={styles.condTitle} title={v.name}>
                                    {v.name}
                                  </span>
                                  <span className={common.cellSub}>
                                    Đơn từ {formatVnd(v.min_order_vnd)}
                                    {isPct && v.max_discount_vnd > 0
                                      ? `, tối đa ${formatVnd(v.max_discount_vnd)}`
                                      : ""}
                                  </span>
                                </td>
                                <td className={common.numCol}>
                                  <strong>{discountStr}</strong>
                                </td>
                                <td className={styles.quotaCell}>
                                  <ProgressBar
                                    value={v.used_count}
                                    max={Math.max(1, v.quota)}
                                    leftLabel={`${formatInt(v.used_count)} / ${formatInt(v.quota)}`}
                                    rightLabel={`Còn ${formatInt(v.remaining_quota)}`}
                                  />
                                </td>
                                <td className={common.numCol}>
                                  {formatVnd(v.used_budget_vnd)}
                                  {v.budget_vnd > 0 && (
                                    <span className={common.cellSub}>
                                      / {formatVnd(v.budget_vnd)}
                                    </span>
                                  )}
                                </td>
                                <td>
                                  <span className={statusPillClass(stMeta.tone)}>
                                    {stMeta.label}
                                  </span>
                                </td>
                                <td className={common.numCol}>
                                  {isDeleted ? (
                                    <span className={common.cardMeta}>—</span>
                                  ) : (
                                    <div className={styles.switchWrap}>
                                      <button
                                        type="button"
                                        className={styles.inlineBtn}
                                        disabled={isBusy}
                                        onClick={() => handleOpenEditVoucher(v)}
                                      >
                                        Sửa
                                      </button>
                                      <button
                                        type="button"
                                        className={styles.inlineBtn}
                                        disabled={isBusy}
                                        onClick={() => void handleToggleVoucherStatus(v)}
                                      >
                                        {statusKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                                      </button>
                                      {confirmDeleteVoucherId === v.id ? (
                                        <>
                                          <button
                                            type="button"
                                            className={`${styles.inlineBtn} ${styles.inlineDangerBtn}`}
                                            disabled={isBusy}
                                            onClick={() => void handleDeleteVoucher(v)}
                                          >
                                            Xác nhận xóa
                                          </button>
                                          <button
                                            type="button"
                                            className={styles.inlineBtn}
                                            onClick={() => setConfirmDeleteVoucherId(null)}
                                          >
                                            Hủy
                                          </button>
                                        </>
                                      ) : (
                                        <button
                                          type="button"
                                          className={styles.inlineBtn}
                                          disabled={isBusy}
                                          onClick={() => setConfirmDeleteVoucherId(v.id)}
                                        >
                                          Xóa
                                        </button>
                                      )}
                                    </div>
                                  )}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>

                    <div className={common.mobileList}>
                      {vouchers.map((v) => {
                        const statusKey = getVoucherStatusKey(v);
                        const stMeta = VOUCHER_STATUS_META[statusKey] ?? {
                          label: statusKey,
                          tone: "neutral" as const,
                        };
                        const isDeleted = statusKey === "DELETED";
                        const isBusy = voucherActionBusyId === v.id;
                        return (
                          <div key={v.id} className={common.mobileRow}>
                            <div className={common.mobileRowTop}>
                              <div>
                                <span className={styles.codeStrong}>{v.code}</span>
                                <span className={common.cellSub}>{v.name}</span>
                              </div>
                              <span className={statusPillClass(stMeta.tone)}>{stMeta.label}</span>
                            </div>
                            <ProgressBar
                              value={v.used_count}
                              max={Math.max(1, v.quota)}
                              leftLabel={`${formatInt(v.used_count)}/${formatInt(v.quota)}`}
                              rightLabel={`Còn ${formatInt(v.remaining_quota)}`}
                            />
                            {!isDeleted && (
                              <div className={common.mobileRowMeta}>
                                <div className={styles.actionPair}>
                                  <button
                                    type="button"
                                    className={styles.inlineBtn}
                                    disabled={isBusy}
                                    onClick={() => handleOpenEditVoucher(v)}
                                  >
                                    Sửa
                                  </button>
                                  <button
                                    type="button"
                                    className={styles.inlineBtn}
                                    disabled={isBusy}
                                    onClick={() => void handleToggleVoucherStatus(v)}
                                  >
                                    {statusKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                                  </button>
                                  {confirmDeleteVoucherId === v.id ? (
                                    <>
                                      <button
                                        type="button"
                                        className={`${styles.inlineBtn} ${styles.inlineDangerBtn}`}
                                        disabled={isBusy}
                                        onClick={() => void handleDeleteVoucher(v)}
                                      >
                                        Xác nhận xóa
                                      </button>
                                      <button
                                        type="button"
                                        className={styles.inlineBtn}
                                        onClick={() => setConfirmDeleteVoucherId(null)}
                                      >
                                        Hủy
                                      </button>
                                    </>
                                  ) : (
                                    <button
                                      type="button"
                                      className={styles.inlineBtn}
                                      disabled={isBusy}
                                      onClick={() => setConfirmDeleteVoucherId(v.id)}
                                    >
                                      Xóa
                                    </button>
                                  )}
                                </div>
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  </>
                )}
              </section>
            </>
          )}

          {tab === "programs" && (
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
          )}

          {tab === "flash" && (
            <>
              {flashSales.length > 0 && (
                <div
                  className={common.countCards4}
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
                        className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                        onClick={() =>
                          setSelectedSlotId((prev) => (prev === slot.id ? "all" : slot.id))
                        }
                      >
                        <span className={common.countCardLabel}>
                          {slot.name} ({stLabel}
                          {slot.min_discount_percent ? `, ≥${slot.min_discount_percent}%` : ""})
                        </span>
                        <span className={common.countCardValue}>{formatInt(countInSlot)} SKU</span>
                      </button>
                    );
                  })}
                </div>
              )}

              {showFlashForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={(e) => void handleRegisterFlash(e)}
                  aria-label="Đăng ký SKU vào Flash sale"
                  noValidate
                >
                  <div className={common.cardHeader}>
                    <div>
                      <h2 className={common.cardTitle}>Đăng ký suất Flash sale</h2>
                      <span className={common.cardMeta}>
                        {selectedFlashWindow?.min_discount_percent
                          ? `Khung giờ yêu cầu giảm tối thiểu ${selectedFlashWindow.min_discount_percent}%`
                          : "Quản trị viên duyệt suất Flash sale trước khi mở bán"}
                      </span>
                    </div>
                  </div>

                  {fError && (
                    <p className={common.fieldError} role="alert" style={{ marginBottom: 12 }}>
                      {fError}
                    </p>
                  )}

                  <div className={styles.formGrid4}>
                    <div className={common.field}>
                      <label htmlFor="f-slot" className={common.fieldLabel}>
                        Khung giờ
                      </label>
                      <select
                        id="f-slot"
                        className={common.textInput}
                        value={fSlotId}
                        onChange={(e) => setFSlotId(e.target.value)}
                      >
                        {openFlashSales.map((slot) => (
                          <option key={slot.id} value={slot.id}>
                            {slot.name}
                            {slot.min_discount_percent
                              ? ` (giảm ≥${slot.min_discount_percent}%)`
                              : ""}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="f-sku" className={common.fieldLabel}>
                        Biến thể SKU
                      </label>
                      <select
                        id="f-sku"
                        className={common.textInput}
                        value={fSkuId}
                        onChange={(e) => setFSkuId(e.target.value)}
                      >
                        {selectableFlashSkus.map(({ product, sku }) => (
                          <option key={sku.id} value={sku.id}>
                            {product.name} ({sku.sku_code}, {formatVnd(sku.price_vnd)}, tồn{" "}
                            {sku.available_stock})
                          </option>
                        ))}
                      </select>
                      {selectedFlashSkuEntry && (
                        <span className={common.fieldHint}>
                          Giá gốc: {formatVnd(selectedFlashSkuEntry.sku.price_vnd)}
                          {selectedFlashSkuEntry.sku.min_price_30d_vnd
                            ? ` · Thấp nhất 30 ngày: ${formatVnd(selectedFlashSkuEntry.sku.min_price_30d_vnd)}`
                            : ""}
                        </span>
                      )}
                    </div>
                    <div className={common.field}>
                      <label htmlFor="f-price" className={common.fieldLabel}>
                        Giá Flash sale (VND)
                      </label>
                      <input
                        id="f-price"
                        type="number"
                        min={1}
                        step={1}
                        className={common.textInput}
                        value={fPriceStr}
                        onChange={(e) => setFPriceStr(e.target.value)}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="f-quota" className={common.fieldLabel}>
                        Suất cam kết
                      </label>
                      <input
                        id="f-quota"
                        type="number"
                        min={1}
                        step={1}
                        className={common.textInput}
                        value={fQuotaStr}
                        onChange={(e) => setFQuotaStr(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className={styles.formGrid3}>
                    <div className={common.field}>
                      <label htmlFor="f-limit" className={common.fieldLabel}>
                        Giới hạn mỗi khách
                      </label>
                      <input
                        id="f-limit"
                        type="number"
                        min={1}
                        max={10}
                        step={1}
                        className={common.textInput}
                        value={fLimitStr}
                        onChange={(e) => setFLimitStr(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className={styles.formActions}>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      shape="app"
                      onClick={() => setShowFlashForm(false)}
                    >
                      Hủy
                    </Button>
                    <Button type="submit" variant="primary" size="sm" shape="app" disabled={fBusy}>
                      {fBusy ? "Đang gửi..." : "Đăng ký suất"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="SKU tham gia Flash sale">
                <div className={common.cardHeader}>
                  <div>
                    <h2 className={common.cardTitle}>
                      Suất Flash sale đã đăng ký ({formatInt(filteredFlashRows.length)})
                    </h2>
                    {selectedSlotId !== "all" && (
                      <button
                        type="button"
                        className={common.cardLink}
                        onClick={() => setSelectedSlotId("all")}
                      >
                        Xem tất cả khung giờ
                      </button>
                    )}
                  </div>
                  <div
                    className={common.segmented}
                    role="tablist"
                    aria-label="Lọc trạng thái duyệt"
                  >
                    {(
                      [
                        ["ALL", `Tất cả (${formatInt(flashItemCounts.ALL)})`],
                        ["PENDING", `Chờ duyệt (${formatInt(flashItemCounts.PENDING)})`],
                        ["APPROVED", `Đã duyệt (${formatInt(flashItemCounts.APPROVED)})`],
                        ["REJECTED", `Từ chối (${formatInt(flashItemCounts.REJECTED)})`],
                      ] as const
                    ).map(([st, lbl]) => (
                      <button
                        key={st}
                        type="button"
                        role="tab"
                        aria-selected={flashStatusFilter === st}
                        className={`${common.segmentedBtn} ${flashStatusFilter === st ? common.segmentedBtnActive : ""}`}
                        onClick={() => setFlashStatusFilter(st)}
                      >
                        {lbl}
                      </button>
                    ))}
                  </div>
                </div>

                {filteredFlashRows.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Chưa có SKU đăng ký khớp bộ lọc hiện tại.
                  </div>
                ) : (
                  <>
                    <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            <th>Sản phẩm &amp; SKU</th>
                            <th>Khung giờ</th>
                            <th className={common.numCol}>Giá Flash sale</th>
                            <th>Suất còn lại</th>
                            <th>Trạng thái</th>
                            <th>Lý do từ chối</th>
                          </tr>
                        </thead>
                        <tbody>
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
                              <tr key={row.item.id}>
                                <td>
                                  <div className={styles.flashProdCell}>
                                    {row.thumb ? (
                                      <img
                                        src={row.thumb}
                                        alt={row.productName}
                                        className={styles.flashThumb}
                                        width={34}
                                        height={44}
                                      />
                                    ) : (
                                      <span
                                        className={`${common.thumbPlaceholder} ${styles.flashThumb}`}
                                        aria-hidden="true"
                                      >
                                        <IconPackage size={15} />
                                      </span>
                                    )}
                                    <div className={styles.flashProdText}>
                                      <span
                                        className={styles.flashProdName}
                                        title={row.productName}
                                      >
                                        {row.productName}
                                      </span>
                                      <span className={common.cellSub}>
                                        {row.skuCode}, tối đa {formatInt(row.item.per_user_limit)}
                                        /khách
                                      </span>
                                    </div>
                                  </div>
                                </td>
                                <td>{row.windowName}</td>
                                <td className={common.numCol}>
                                  <strong>{formatVnd(row.item.flash_price_vnd)}</strong>
                                  {row.originalPrice > row.item.flash_price_vnd && (
                                    <span className={styles.discountTag}>
                                      {formatDiscountPercent(
                                        row.originalPrice,
                                        row.item.flash_price_vnd
                                      )}
                                    </span>
                                  )}
                                </td>
                                <td className={styles.quotaCell}>
                                  <ProgressBar
                                    value={used}
                                    max={Math.max(1, row.item.total_slots)}
                                    leftLabel={`${formatInt(used)}/${formatInt(row.item.total_slots)}`}
                                    rightLabel={`Còn ${formatInt(row.item.remaining_slots)}`}
                                  />
                                </td>
                                <td>
                                  <span className={statusPillClass(stMeta.tone)}>
                                    {stMeta.label}
                                  </span>
                                </td>
                                <td>
                                  {row.item.rejection_reason ? (
                                    <span className={common.fieldError}>
                                      {row.item.rejection_reason}
                                    </span>
                                  ) : (
                                    <span className={common.cardMeta}>—</span>
                                  )}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>

                    <div className={common.mobileList}>
                      {filteredFlashRows.map((row) => {
                        const stMeta = FLASH_ITEM_STATUS_META[row.item.status.toUpperCase()] ?? {
                          label: row.item.status,
                          tone: "neutral" as const,
                        };
                        return (
                          <div key={row.item.id} className={common.mobileRow}>
                            <div className={common.mobileRowTop}>
                              <div>
                                <strong>{row.productName}</strong>
                                <span className={common.cellSub}>
                                  {row.skuCode} · {row.windowName}
                                </span>
                              </div>
                              <span className={statusPillClass(stMeta.tone)}>{stMeta.label}</span>
                            </div>
                            <div className={common.mobileRowMeta}>
                              <strong className="num">{formatVnd(row.item.flash_price_vnd)}</strong>
                              <span className="num">
                                Còn {formatInt(row.item.remaining_slots)} /{" "}
                                {formatInt(row.item.total_slots)}
                              </span>
                            </div>
                            {row.item.rejection_reason && (
                              <span className={common.fieldError}>{row.item.rejection_reason}</span>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  </>
                )}
              </section>
            </>
          )}
        </>
      )}

      {/* Quick Voucher Detail Drawer */}
      <SellerDrawer
        open={Boolean(drawerVoucher)}
        onClose={() => setDrawerVoucherId(null)}
        title={drawerVoucher?.code ?? ""}
        subtitle={drawerVoucher?.name}
        badge={
          drawerVoucher ? (
            <span
              className={statusPillClass(
                (
                  VOUCHER_STATUS_META[getVoucherStatusKey(drawerVoucher)] ?? {
                    tone: "neutral" as const,
                  }
                ).tone
              )}
            >
              {
                (
                  VOUCHER_STATUS_META[getVoucherStatusKey(drawerVoucher)] ?? {
                    label: getVoucherStatusKey(drawerVoucher),
                  }
                ).label
              }
            </span>
          ) : null
        }
        footer={
          drawerVoucher ? (
            <>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => handleCopyCode(drawerVoucher.code)}
              >
                <IconCopy size={14} />
                <span>Sao chép mã</span>
              </Button>
              {getVoucherStatusKey(drawerVoucher) !== "DELETED" && (
                <Button
                  variant="primary"
                  size="sm"
                  shape="app"
                  onClick={() => {
                    const target = drawerVoucher;
                    setDrawerVoucherId(null);
                    handleOpenEditVoucher(target);
                  }}
                >
                  Chỉnh sửa
                </Button>
              )}
            </>
          ) : null
        }
      >
        {drawerVoucher && (
          <div className={common.drawerKvList}>
            <div className={common.drawerKvRow}>
              <span className={common.drawerKvKey}>Mức giảm</span>
              <span className={common.drawerKvVal}>
                {drawerVoucher.discount_type.toUpperCase() === "PERCENT"
                  ? `${drawerVoucher.discount_percent}%`
                  : formatVnd(drawerVoucher.value_vnd)}
              </span>
            </div>
            {drawerVoucher.max_discount_vnd > 0 && (
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Giảm tối đa</span>
                <span className={common.drawerKvVal}>
                  {formatVnd(drawerVoucher.max_discount_vnd)}
                </span>
              </div>
            )}
            <div className={common.drawerKvRow}>
              <span className={common.drawerKvKey}>Đơn tối thiểu</span>
              <span className={common.drawerKvVal}>{formatVnd(drawerVoucher.min_order_vnd)}</span>
            </div>
            <div className={common.drawerKvRow}>
              <span className={common.drawerKvKey}>Đã dùng / Phát hành</span>
              <span className={common.drawerKvVal}>
                {formatInt(drawerVoucher.used_count)} / {formatInt(drawerVoucher.quota)}
              </span>
            </div>
            <div className={common.drawerKvRow}>
              <span className={common.drawerKvKey}>Ngân sách đã dùng</span>
              <span className={common.drawerKvVal}>
                {formatVnd(drawerVoucher.used_budget_vnd)}
                {drawerVoucher.budget_vnd > 0 ? ` / ${formatVnd(drawerVoucher.budget_vnd)}` : ""}
              </span>
            </div>
            <div className={common.drawerKvRow}>
              <span className={common.drawerKvKey}>Giới hạn mỗi khách</span>
              <span className={common.drawerKvVal}>{formatInt(drawerVoucher.per_user_limit)}</span>
            </div>
            {drawerVoucher.starts_at && (
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Bắt đầu</span>
                <span className={common.drawerKvVal}>
                  {formatDateTimeVi(drawerVoucher.starts_at)}
                </span>
              </div>
            )}
            {drawerVoucher.ends_at && (
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Kết thúc</span>
                <span className={common.drawerKvVal}>
                  {formatDateTimeVi(drawerVoucher.ends_at)}
                </span>
              </div>
            )}
          </div>
        )}
      </SellerDrawer>
    </div>
  );
}
