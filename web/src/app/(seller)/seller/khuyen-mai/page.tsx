"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { PRODUCTS } from "@/data";
import type {
  GatewayFlashSale,
  GatewayFlashSaleItem,
  GatewayProduct,
  GatewayShop,
  GatewaySKU,
  GatewayVoucher,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPlus, ProgressBar, StateView } from "@/components";
import { formatDateTimeVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import { PromotionsDiscountProgramsTab } from "@/features/seller/PromotionsDiscountProgramsTab";
import { PromotionsCampaignTab } from "@/features/seller/PromotionsCampaignTab";
import {
  canEditVoucherField,
  FLASH_ITEM_STATUS_META,
  getProductCoverImage,
  VOUCHER_STATUS_META,
} from "@/features/seller/types";
import { parseUiState, useSellerSession } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type PromoTab = "vouchers" | "programs" | "flash" | "campaign";
type FlashItemStatusFilter = "ALL" | "PENDING" | "APPROVED" | "REJECTED";

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function getWindowStatus(win: GatewayFlashSale): "ended" | "live" | "upcoming" {
  const now = Date.now();
  const start = win.starts_at ? Date.parse(win.starts_at) : 0;
  const end = win.ends_at ? Date.parse(win.ends_at) : 0;
  if (end > 0 && now > end) return "ended";
  if (start > 0 && now < start) return "upcoming";
  return "live";
}

function getVoucherStatusKey(v: GatewayVoucher): string {
  if (v.status) return v.status.toUpperCase();
  return v.is_active ? "ACTIVE" : "PAUSED";
}

function pillClassForTone(tone: "neutral" | "warn" | "pos" | "neg"): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
}

function SellerPromotionsInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as PromoTab | null;
  const { showToast } = useMockStore();
  const { mockShopKey } = useSellerContext();
  const session = useSellerSession();

  const [tab, setTab] = useState<PromoTab>(
    initialTab && ["vouchers", "programs", "flash", "campaign"].includes(initialTab)
      ? initialTab
      : "vouchers"
  );

  const [vouchers, setVouchers] = useState<GatewayVoucher[]>([]);
  const [flashSales, setFlashSales] = useState<GatewayFlashSale[]>([]);
  const [sellerFlashItems, setSellerFlashItems] = useState<GatewayFlashSaleItem[]>([]);
  const [products, setProducts] = useState<GatewayProduct[]>([]);
  const [programsCount, setProgramsCount] = useState<number>(0);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [showVoucherForm, setShowVoucherForm] = useState(false);
  const [editingVoucher, setEditingVoucher] = useState<GatewayVoucher | null>(null);
  const [voucherActionBusyId, setVoucherActionBusyId] = useState<string | null>(null);
  const [confirmDeleteVoucherId, setConfirmDeleteVoucherId] = useState<string | null>(null);

  const [showProgramForm, setShowProgramForm] = useState(false);
  const [showFlashForm, setShowFlashForm] = useState(false);
  const [showCampaignForm, setShowCampaignForm] = useState(false);
  const [selectedWindowId, setSelectedWindowId] = useState<string>("all");
  const [flashStatusFilter, setFlashStatusFilter] = useState<FlashItemStatusFilter>("ALL");

  // Voucher create/edit form
  const defaultStarts = useMemo(() => toDatetimeLocalValue(new Date()), []);
  const defaultEnds = useMemo(
    () => toDatetimeLocalValue(new Date(Date.now() + 14 * 24 * 3600 * 1000)),
    []
  );
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
  const [vError, setVError] = useState<string | null>(null);
  const [vSubmitting, setVSubmitting] = useState(false);

  // Flash sale item registration form
  const [fWindowId, setFWindowId] = useState("");
  const [fSkuId, setFSkuId] = useState("");
  const [fPriceStr, setFPriceStr] = useState("149000");
  const [fSlotsStr, setFSlotsStr] = useState("20");
  const [fLimitStr, setFLimitStr] = useState("2");
  const [fError, setFError] = useState<string | null>(null);
  const [fSubmitting, setFSubmitting] = useState(false);

  const loadData = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [voucherRes, flashRes, flashItemsRes, prodRes] = await Promise.all([
        api<{ vouchers: GatewayVoucher[] }>("seller/vouchers?include_inactive=true"),
        api<{ flash_sales?: GatewayFlashSale[]; items?: GatewayFlashSale[] }>(
          "seller/flash-sales?page=1&page_size=50"
        ).catch(() =>
          api<{ flash_sales: GatewayFlashSale[] }>("flash-sales?include_upcoming=true")
        ),
        api<{ items?: GatewayFlashSaleItem[] }>(
          "seller/flash-sales/items?page=1&page_size=50"
        ).catch(() => null),
        api<PaginatedResponse<GatewayProduct>>("seller/products?page=1&page_size=100"),
      ]);
      const nextVouchers = voucherRes.vouchers ?? [];
      const nextFlash =
        ("flash_sales" in flashRes ? flashRes.flash_sales : undefined) ??
        ("items" in flashRes ? flashRes.items : undefined) ??
        [];
      const nextProducts = prodRes.items ?? [];

      setVouchers(nextVouchers);
      setFlashSales(nextFlash);
      setProducts(nextProducts);

      if (flashItemsRes && Array.isArray(flashItemsRes.items)) {
        setSellerFlashItems(flashItemsRes.items);
      } else {
        const embedded: GatewayFlashSaleItem[] = [];
        for (const win of nextFlash) {
          for (const item of win.items ?? []) {
            if (item.shop_id === shop.id) {
              embedded.push(item);
            }
          }
        }
        setSellerFlashItems(embedded);
      }

      if (nextFlash.length > 0) {
        setFWindowId((prev) => prev || nextFlash[0].id);
      }
      const firstSku = nextProducts
        .filter((p) => p.status === "APPROVED")
        .flatMap((p) => p.skus ?? [])
        .find((s) => s.is_active && s.available_stock > 0);
      if (firstSku) {
        setFSkuId((prev) => prev || firstSku.id);
      }
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải dữ liệu khuyến mãi lúc này."));
    } finally {
      setLoading(false);
    }
  }, [shop.id]);

  useEffect(() => {
    if (!uiState) {
      void loadData();
    }
  }, [uiState, loadData]);

  const skuLookup = useMemo(() => {
    const map = new Map<string, { sku: GatewaySKU; product: GatewayProduct }>();
    for (const p of products) {
      for (const s of p.skus ?? []) {
        map.set(s.id, { sku: s, product: p });
      }
    }
    return map;
  }, [products]);

  const flashWindowById = useMemo(() => {
    const map = new Map<string, GatewayFlashSale>();
    for (const w of flashSales) {
      map.set(w.id, w);
    }
    return map;
  }, [flashSales]);

  const selectableSkus = useMemo(
    () =>
      products
        .filter((p) => p.status === "APPROVED")
        .flatMap((p) =>
          (p.skus ?? [])
            .filter((s) => s.is_active && s.available_stock > 0)
            .map((sku) => ({ product: p, sku }))
        ),
    [products]
  );

  const selectedFlashWindow = useMemo(
    () => flashSales.find((w) => w.id === fWindowId) ?? null,
    [flashSales, fWindowId]
  );

  const selectedFlashSkuEntry = useMemo(
    () => selectableSkus.find((x) => x.sku.id === fSkuId) ?? null,
    [selectableSkus, fSkuId]
  );

  const flashItemRows = useMemo(() => {
    return sellerFlashItems.map((item) => {
      const win = flashWindowById.get(item.flash_sale_id);
      return {
        item,
        windowName: win?.name ?? item.flash_sale_id,
        windowStatus: win ? getWindowStatus(win) : ("upcoming" as const),
      };
    });
  }, [sellerFlashItems, flashWindowById]);

  const filteredFlashRows = useMemo(() => {
    return flashItemRows.filter((row) => {
      if (selectedWindowId !== "all" && row.item.flash_sale_id !== selectedWindowId) {
        return false;
      }
      if (flashStatusFilter !== "ALL" && row.item.status.toUpperCase() !== flashStatusFilter) {
        return false;
      }
      return true;
    });
  }, [flashItemRows, selectedWindowId, flashStatusFilter]);

  const activeVoucherCount = vouchers.filter((v) => getVoucherStatusKey(v) === "ACTIVE").length;
  const voucherUsedTotal = vouchers.reduce((s, v) => s + v.used_count, 0);
  const voucherQuotaTotal = vouchers.reduce((s, v) => s + v.quota, 0);
  const voucherBudgetUsedTotal = vouchers.reduce((s, v) => s + v.used_budget_vnd, 0);

  const mockProducts = useMemo(
    () => PRODUCTS.filter((p) => p.shopId === mockShopKey),
    [mockShopKey]
  );

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

  const handleOpenEditVoucher = (v: GatewayVoucher) => {
    setEditingVoucher(v);
    setVCode(v.code);
    setVName(v.name);
    setVType(v.discount_type === "PERCENT" ? "PERCENT" : "FIXED");
    setVValue(String(v.discount_type === "PERCENT" ? v.discount_percent : v.value_vnd));
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

  const handleSaveVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const rawVal = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.max(0, Math.trunc(Number(vMinOrder) || 0));
    const quota = Math.trunc(Number(vQuota) || 0);
    const perUser = Math.max(1, Math.trunc(Number(vPerUser) || 1));
    const budgetVnd = Math.max(0, Math.trunc(Number(vBudget) || 0));
    const maxDiscountVnd =
      vType === "PERCENT" ? Math.max(0, Math.trunc(Number(vMaxDiscount) || 0)) : 0;

    if (!editingVoucher && code.length < 3) {
      setVError("Mã voucher cần tối thiểu 3 ký tự liền nhau.");
      return;
    }
    if (
      (!editingVoucher || editingVoucher.used_count === 0) &&
      vType === "FIXED" &&
      rawVal < 1000
    ) {
      setVError("Mức giảm cố định tối thiểu 1.000₫.");
      return;
    }
    if (
      (!editingVoucher || editingVoucher.used_count === 0) &&
      vType === "PERCENT" &&
      (rawVal < 1 || rawVal > 90)
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
        `Tổng lượt mới không được thấp hơn số lượt đã dùng (${formatInt(editingVoucher.used_count)}).`
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
    setVSubmitting(true);
    try {
      const displayLabel =
        vName.trim() ||
        `Giảm ${vType === "FIXED" ? formatVnd(rawVal) : `${rawVal}%`} đơn từ ${formatVnd(minOrder)}`;

      if (editingVoucher) {
        const updatePayload: Record<string, unknown> = {
          name: displayLabel,
          quota,
          budget_vnd: budgetVnd,
          ends_at: endDate.toISOString(),
        };
        if (editingVoucher.used_count === 0) {
          updatePayload.discount_type = vType;
          updatePayload.value_vnd = vType === "FIXED" ? rawVal : 0;
          updatePayload.discount_percent = vType === "PERCENT" ? rawVal : 0;
          updatePayload.max_discount_vnd = maxDiscountVnd;
          updatePayload.min_order_vnd = minOrder;
          updatePayload.per_user_limit = perUser;
          updatePayload.starts_at = startDate.toISOString();
        }
        const res = await api<{ voucher: GatewayVoucher }>(
          `seller/vouchers/${encodeURIComponent(editingVoucher.id)}`,
          {
            method: "PUT",
            json: updatePayload,
          }
        );
        if (res.voucher) {
          setVouchers((prev) => prev.map((it) => (it.id === res.voucher.id ? res.voucher : it)));
        } else {
          await loadData();
        }
        resetVoucherForm();
        setShowVoucherForm(false);
        showToast(`Đã cập nhật voucher ${editingVoucher.code}.`);
      } else {
        const res = await api<{ voucher: GatewayVoucher }>("seller/vouchers", {
          method: "POST",
          json: {
            code,
            name: displayLabel,
            discount_type: vType,
            value_vnd: vType === "FIXED" ? rawVal : 0,
            discount_percent: vType === "PERCENT" ? rawVal : 0,
            max_discount_vnd: maxDiscountVnd,
            min_order_vnd: minOrder,
            quota,
            per_user_limit: perUser,
            budget_vnd: budgetVnd,
            starts_at: startDate.toISOString(),
            ends_at: endDate.toISOString(),
          },
        });
        if (res.voucher) {
          setVouchers((prev) => [res.voucher, ...prev]);
        } else {
          await loadData();
        }
        resetVoucherForm();
        setShowVoucherForm(false);
        showToast(`Đã phát hành mã voucher ${code} cho gian hàng.`);
      }
    } catch (err) {
      setVError(getErrorMessage(err, "Không thể lưu voucher lúc này."));
    } finally {
      setVSubmitting(false);
    }
  };

  const handleToggleVoucherStatus = async (v: GatewayVoucher) => {
    const currentStatus = getVoucherStatusKey(v);
    if (currentStatus === "DELETED") return;
    const nextStatus = currentStatus === "ACTIVE" ? "PAUSED" : "ACTIVE";
    setVoucherActionBusyId(v.id);
    try {
      const res = await api<{ voucher: GatewayVoucher }>(
        `seller/vouchers/${encodeURIComponent(v.id)}/status`,
        {
          method: "PATCH",
          json: { status: nextStatus },
        }
      );
      if (res.voucher) {
        setVouchers((prev) => prev.map((it) => (it.id === v.id ? res.voucher : it)));
      } else {
        await loadData();
      }
      showToast(
        nextStatus === "PAUSED"
          ? `Đã tạm dừng voucher ${v.code}.`
          : `Đã kích hoạt lại voucher ${v.code}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật trạng thái voucher."));
    } finally {
      setVoucherActionBusyId(null);
    }
  };

  const handleDeleteVoucher = async (v: GatewayVoucher) => {
    setVoucherActionBusyId(v.id);
    try {
      await api(`seller/vouchers/${encodeURIComponent(v.id)}`, { method: "DELETE" });
      setConfirmDeleteVoucherId(null);
      await loadData();
      showToast(`Đã xóa mềm voucher ${v.code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xóa voucher lúc này."));
    } finally {
      setVoucherActionBusyId(null);
    }
  };

  const handleRegisterFlash = async (e: React.FormEvent) => {
    e.preventDefault();
    const targetWin = flashSales.find((w) => w.id === fWindowId);
    const skuEntry = selectableSkus.find((x) => x.sku.id === fSkuId);
    if (!targetWin || !skuEntry) {
      setFError("Vui lòng chọn khung giờ và biến thể SKU hợp lệ.");
      return;
    }
    const flashPrice = Math.trunc(Number(fPriceStr) || 0);
    const totalSlots = Math.trunc(Number(fSlotsStr) || 0);
    const perUserLimit = Math.max(1, Math.trunc(Number(fLimitStr) || 1));

    if (flashPrice <= 0 || flashPrice >= skuEntry.sku.price_vnd) {
      setFError(
        `Giá Flash sale phải nhỏ hơn giá hiện tại của SKU (${formatVnd(skuEntry.sku.price_vnd)}).`
      );
      return;
    }
    const minDiscountPct = targetWin.min_discount_percent ?? 0;
    if (minDiscountPct > 0) {
      const has30dMin =
        typeof skuEntry.sku.min_price_30d_vnd === "number" && skuEntry.sku.min_price_30d_vnd > 0;
      const basePrice = has30dMin
        ? Math.min(skuEntry.sku.price_vnd, skuEntry.sku.min_price_30d_vnd!)
        : skuEntry.sku.price_vnd;
      const maxAllowedPrice = Math.trunc((basePrice * (100 - minDiscountPct)) / 100);
      if (flashPrice > maxAllowedPrice) {
        setFError(
          `Khung giờ này yêu cầu giảm tối thiểu ${minDiscountPct}% so với giá thấp nhất 30 ngày (${formatVnd(basePrice)} → giá tối đa ${formatVnd(maxAllowedPrice)}).`
        );
        return;
      }
    }
    if (totalSlots < 1 || totalSlots > skuEntry.sku.available_stock) {
      setFError(
        `Số suất đăng ký hợp lệ từ 1 đến ${formatInt(skuEntry.sku.available_stock)} (tồn khả dụng hiện tại).`
      );
      return;
    }

    setFError(null);
    setFSubmitting(true);
    try {
      await api<{ item: GatewayFlashSaleItem }>(`seller/flash-sales/${targetWin.id}/items`, {
        method: "POST",
        json: {
          sku_id: skuEntry.sku.id,
          flash_price_vnd: flashPrice,
          total_slots: totalSlots,
          per_user_limit: perUserLimit,
        },
      });
      setShowFlashForm(false);
      showToast(
        `Đã gửi đăng ký Flash sale cho ${skuEntry.sku.sku_code} (Chờ quản trị viên duyệt).`
      );
      await loadData();
    } catch (err) {
      setFError(getErrorMessage(err, "Không thể đăng ký suất Flash sale lúc này."));
    } finally {
      setFSubmitting(false);
    }
  };

  const slotStatusPill = (st: "ended" | "live" | "upcoming") =>
    st === "live"
      ? { label: "Đang mở bán", cls: `${common.pill} ${common.pillPos}` }
      : st === "upcoming"
        ? { label: "Sắp mở bán", cls: `${common.pill} ${common.pillWarn}` }
        : { label: "Đã kết thúc", cls: common.pill };

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

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Khuyến mãi gian hàng</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(activeVoucherCount)} voucher đang bật,{" "}
            {formatInt(flashItemCounts.APPROVED)} suất Flash sale đã duyệt (
            {formatInt(flashItemCounts.PENDING)} chờ duyệt)
          </p>
        </div>
        <div className={common.headerActions}>
          {tab === "vouchers" && (
            <Button
              variant="ink"
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
              <span>{showVoucherForm ? "Đóng biểu mẫu" : "Tạo voucher mới"}</span>
            </Button>
          )}
          {tab === "programs" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowProgramForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showProgramForm ? "Đóng biểu mẫu" : "Tạo chương trình giảm giá"}</span>
            </Button>
          )}
          {tab === "flash" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowFlashForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showFlashForm ? "Đóng biểu mẫu" : "Đăng ký suất Flash sale"}</span>
            </Button>
          )}
          {tab === "campaign" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowCampaignForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showCampaignForm ? "Đóng biểu mẫu" : "Đăng ký chiến dịch mẫu"}</span>
            </Button>
          )}
        </div>
      </header>

      {(uiState === "loading" || (!uiState && loading)) && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải danh sách voucher và lịch khung giờ Flash sale..."
          rows={4}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu khuyến mãi"
          message={errorMsg ?? "Dịch vụ khuyến mãi tạm gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => void loadData()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa có chương trình khuyến mãi nào"
          message="Tạo mã giảm giá gian hàng, chương trình giảm giá SKU hoặc đăng ký vào khung giờ Flash sale."
          actionLabel="Tạo voucher đầu tiên"
          onAction={() => {
            setTab("vouchers");
            setShowVoucherForm(true);
          }}
        />
      )}

      {!uiState && !loading && !errorMsg && (
        <>
          <div className={common.underlineTabs} role="tablist" aria-label="Loại khuyến mãi">
            <button
              type="button"
              role="tab"
              aria-selected={tab === "vouchers"}
              className={`${common.underlineTab} ${tab === "vouchers" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("vouchers")}
            >
              Voucher gian hàng ({formatInt(vouchers.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "programs"}
              className={`${common.underlineTab} ${tab === "programs" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("programs")}
            >
              Chương trình giảm giá shop{programsCount > 0 ? ` (${formatInt(programsCount)})` : ""}
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "flash"}
              className={`${common.underlineTab} ${tab === "flash" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("flash")}
            >
              Đăng ký Flash sale ({formatInt(sellerFlashItems.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "campaign"}
              className={`${common.underlineTab} ${tab === "campaign" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("campaign")}
            >
              Chiến dịch sàn (mẫu ·{" "}
              {formatInt(session.campaignRegs.filter((r) => r.shopId === mockShopKey).length)})
            </button>
          </div>

          {tab === "vouchers" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp voucher gian hàng">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Voucher đang bật</span>
                  <span
                    className={`${common.balanceValue} ${activeVoucherCount === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(activeVoucherCount)}
                  </span>
                  <span className={common.balanceCaption}>
                    Trên tổng số {formatInt(vouchers.length)} mã (bao gồm tạm dừng / đã xóa)
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Lượt đã sử dụng</span>
                  <span className={common.balanceValue}>
                    {formatInt(voucherUsedTotal)} / {formatInt(voucherQuotaTotal)}
                  </span>
                  <span className={common.balanceCaption}>Tự động trừ suất ngay khi có đơn</span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Ngân sách giảm giá đã chi</span>
                  <span
                    className={`${common.balanceValue} ${voucherBudgetUsedTotal === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatVnd(voucherBudgetUsedTotal)}
                  </span>
                  <span className={common.balanceCaption}>Ghi nhận theo các đơn áp mã hợp lệ</span>
                </div>
              </section>

              {showVoucherForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={(e) => void handleSaveVoucher(e)}
                  aria-label="Biểu mẫu voucher gian hàng"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>
                      {editingVoucher
                        ? `Chỉnh sửa voucher ${editingVoucher.code}`
                        : "Tạo mã giảm giá gian hàng mới"}
                    </h2>
                    <span className={common.cardMeta}>
                      Áp dụng riêng cho sản phẩm của {shop.name}
                    </span>
                  </div>

                  {editingVoucher && editingVoucher.used_count > 0 && (
                    <p className={common.fieldHint} style={{ marginBottom: 12 }}>
                      Voucher đã có {formatInt(editingVoucher.used_count)} lượt sử dụng — chỉ được
                      chỉnh tên hiển thị, tăng tổng lượt/ngân sách và gia hạn thời điểm kết thúc.
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
                        placeholder="Ví dụ: SHOP25K"
                        disabled={Boolean(editingVoucher)}
                        required
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
                        {vType === "FIXED" ? "Mức giảm (VND)" : "Phần trăm giảm (%)"}
                      </label>
                      <input
                        id="v-val"
                        type="number"
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
                        Tổng số lượt phát hành
                      </label>
                      <input
                        id="v-quota"
                        type="number"
                        min={editingVoucher ? Math.max(1, editingVoucher.used_count) : 1}
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
                    <Button
                      type="submit"
                      variant="ink"
                      size="sm"
                      shape="app"
                      disabled={vSubmitting}
                    >
                      {vSubmitting
                        ? "Đang lưu..."
                        : editingVoucher
                          ? "Lưu thay đổi"
                          : "Lưu & phát hành mã"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách voucher gian hàng">
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>Danh sách voucher của {shop.name}</h2>
                  <span className={common.cardMeta}>
                    Hiển thị toàn bộ voucher gồm đang bật, tạm dừng và đã xóa mềm
                  </span>
                </div>

                {vouchers.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Gian hàng chưa có mã giảm giá nào. Nhấn Tạo voucher mới để phát hành mã đầu
                    tiên.
                  </div>
                ) : (
                  <>
                    <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            <th>Mã voucher</th>
                            <th>Điều kiện áp dụng</th>
                            <th className={common.numCol}>Mức giảm</th>
                            <th>Lượt đã dùng</th>
                            <th className={common.numCol}>Ngân sách đã dùng</th>
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
                            const discountStr =
                              v.discount_type === "FIXED"
                                ? formatVnd(v.value_vnd)
                                : `${v.discount_percent}%${v.max_discount_vnd > 0 ? ` (tối đa ${formatVnd(v.max_discount_vnd)})` : ""}`;
                            return (
                              <tr key={v.id}>
                                <td>
                                  <span className={styles.codeStrong}>{v.code}</span>
                                  {v.ends_at && (
                                    <span className={common.cellSub}>
                                      Hết hạn: {formatDateTimeVi(v.ends_at)}
                                    </span>
                                  )}
                                </td>
                                <td>
                                  <span>{v.name}</span>
                                  <span className={common.cellSub}>
                                    Đơn tối thiểu {formatVnd(v.min_order_vnd)} · Giới hạn{" "}
                                    {formatInt(v.per_user_limit)} lần/khách
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
                                  <span className={pillClassForTone(stMeta.tone)}>
                                    {stMeta.label}
                                  </span>
                                </td>
                                <td className={common.numCol}>
                                  {isDeleted ? (
                                    <span className={common.cardMeta}>—</span>
                                  ) : (
                                    <div className={styles.switchWrap}>
                                      <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        shape="app"
                                        disabled={isBusy}
                                        onClick={() => handleOpenEditVoucher(v)}
                                      >
                                        Sửa
                                      </Button>
                                      <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        shape="app"
                                        disabled={isBusy}
                                        onClick={() => void handleToggleVoucherStatus(v)}
                                      >
                                        {statusKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                                      </Button>
                                      {confirmDeleteVoucherId === v.id ? (
                                        <>
                                          <Button
                                            type="button"
                                            variant="danger"
                                            size="sm"
                                            shape="app"
                                            disabled={isBusy}
                                            onClick={() => void handleDeleteVoucher(v)}
                                          >
                                            Xác nhận xóa
                                          </Button>
                                          <Button
                                            type="button"
                                            variant="outline"
                                            size="sm"
                                            shape="app"
                                            onClick={() => setConfirmDeleteVoucherId(null)}
                                          >
                                            Hủy
                                          </Button>
                                        </>
                                      ) : (
                                        <Button
                                          type="button"
                                          variant="outline"
                                          size="sm"
                                          shape="app"
                                          disabled={isBusy}
                                          onClick={() => setConfirmDeleteVoucherId(v.id)}
                                        >
                                          Xóa
                                        </Button>
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
                              <span className={pillClassForTone(stMeta.tone)}>{stMeta.label}</span>
                            </div>
                            <ProgressBar
                              value={v.used_count}
                              max={Math.max(1, v.quota)}
                              leftLabel={`Đã dùng ${formatInt(v.used_count)}/${formatInt(v.quota)}`}
                              rightLabel={`Tối thiểu ${formatVnd(v.min_order_vnd)}`}
                            />
                            {!isDeleted && (
                              <div className={common.mobileRowMeta}>
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  disabled={isBusy}
                                  onClick={() => handleOpenEditVoucher(v)}
                                >
                                  Sửa
                                </Button>
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  disabled={isBusy}
                                  onClick={() => void handleToggleVoucherStatus(v)}
                                >
                                  {statusKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                                </Button>
                                {confirmDeleteVoucherId === v.id ? (
                                  <>
                                    <Button
                                      type="button"
                                      variant="danger"
                                      size="sm"
                                      shape="app"
                                      disabled={isBusy}
                                      onClick={() => void handleDeleteVoucher(v)}
                                    >
                                      Xác nhận xóa
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="sm"
                                      shape="app"
                                      onClick={() => setConfirmDeleteVoucherId(null)}
                                    >
                                      Hủy
                                    </Button>
                                  </>
                                ) : (
                                  <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    shape="app"
                                    disabled={isBusy}
                                    onClick={() => setConfirmDeleteVoucherId(v.id)}
                                  >
                                    Xóa
                                  </Button>
                                )}
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
              shopName={shop.name}
              products={products}
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
                  {flashSales.slice(0, 4).map((win) => {
                    const countInWin = sellerFlashItems.filter(
                      (it) => it.flash_sale_id === win.id
                    ).length;
                    const selected = selectedWindowId === win.id;
                    const st = slotStatusPill(getWindowStatus(win));
                    return (
                      <button
                        key={win.id}
                        type="button"
                        role="tab"
                        aria-selected={selected}
                        className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                        onClick={() =>
                          setSelectedWindowId((prev) => (prev === win.id ? "all" : win.id))
                        }
                      >
                        <span className={common.countCardLabel}>
                          {win.name} · {st.label}
                          {win.min_discount_percent ? ` (≥${win.min_discount_percent}%)` : ""}
                        </span>
                        <span className={common.countCardValue}>
                          {formatInt(countInWin)} suất đã gửi
                        </span>
                      </button>
                    );
                  })}
                </div>
              )}

              {showFlashForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={(e) => void handleRegisterFlash(e)}
                  aria-label="Đăng ký sản phẩm vào khung giờ Flash sale"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>
                      Đăng ký biến thể SKU vào khung giờ Flash sale
                    </h2>
                    <span className={common.cardMeta}>
                      {selectedFlashWindow?.min_discount_percent
                        ? `Khung giờ yêu cầu giảm tối thiểu ${selectedFlashWindow.min_discount_percent}%`
                        : "Sau khi gửi, quản trị viên sẽ duyệt suất Flash sale trước khi mở bán"}
                    </span>
                  </div>

                  {fError && (
                    <p className={common.fieldError} role="alert" style={{ marginBottom: 12 }}>
                      {fError}
                    </p>
                  )}

                  {flashSales.length === 0 || selectableSkus.length === 0 ? (
                    <p className={common.cardMeta}>
                      {flashSales.length === 0
                        ? "Hiện chưa có khung giờ Flash sale nào đang mở đăng ký."
                        : "Gian hàng chưa có biến thể SKU còn tồn kho khả dụng để đăng ký."}
                    </p>
                  ) : (
                    <>
                      <div className={styles.formGrid4}>
                        <div className={common.field}>
                          <label htmlFor="f-slot" className={common.fieldLabel}>
                            Khung giờ Flash sale
                          </label>
                          <select
                            id="f-slot"
                            className={common.textInput}
                            value={fWindowId}
                            onChange={(e) => setFWindowId(e.target.value)}
                          >
                            {flashSales.map((win) => (
                              <option key={win.id} value={win.id}>
                                {win.name}
                                {win.min_discount_percent
                                  ? ` (giảm ≥${win.min_discount_percent}%)`
                                  : ""}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className={common.field}>
                          <label htmlFor="f-sku" className={common.fieldLabel}>
                            Biến thể SKU tham gia
                          </label>
                          <select
                            id="f-sku"
                            className={common.textInput}
                            value={fSkuId}
                            onChange={(e) => setFSkuId(e.target.value)}
                          >
                            {selectableSkus.map(({ product, sku }) => (
                              <option key={sku.id} value={sku.id}>
                                {product.name} · {sku.sku_code} ({formatVnd(sku.price_vnd)} · Tồn{" "}
                                {sku.available_stock})
                              </option>
                            ))}
                          </select>
                          {selectedFlashSkuEntry && (
                            <span className={common.fieldHint}>
                              Giá niêm yết: {formatVnd(selectedFlashSkuEntry.sku.price_vnd)}
                              {selectedFlashSkuEntry.sku.min_price_30d_vnd
                                ? ` · Giá thấp nhất 30 ngày: ${formatVnd(selectedFlashSkuEntry.sku.min_price_30d_vnd)}`
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
                          <span className={common.fieldHint}>
                            Mức giảm tối thiểu được đối chiếu theo giá thấp nhất 30 ngày của SKU.
                          </span>
                        </div>
                        <div className={common.field}>
                          <label htmlFor="f-quota" className={common.fieldLabel}>
                            Suất cam kết
                          </label>
                          <input
                            id="f-quota"
                            type="number"
                            min={1}
                            className={common.textInput}
                            value={fSlotsStr}
                            onChange={(e) => setFSlotsStr(e.target.value)}
                          />
                        </div>
                      </div>

                      <div className={styles.formGrid3}>
                        <div className={common.field}>
                          <label htmlFor="f-limit" className={common.fieldLabel}>
                            Giới hạn mỗi người mua
                          </label>
                          <input
                            id="f-limit"
                            type="number"
                            min={1}
                            max={10}
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
                        <Button
                          type="submit"
                          variant="ink"
                          size="sm"
                          shape="app"
                          disabled={fSubmitting}
                        >
                          {fSubmitting ? "Đang gửi đăng ký..." : "Gửi đăng ký duyệt"}
                        </Button>
                      </div>
                    </>
                  )}
                </form>
              )}

              <section className={common.card} aria-label="Danh sách suất Flash sale của gian hàng">
                <div className={common.cardHeader}>
                  <div>
                    <h2 className={common.cardTitle}>
                      Suất Flash sale đã đăng ký ({formatInt(filteredFlashRows.length)})
                    </h2>
                    <span className={common.cardMeta}>
                      Theo dõi trạng thái chờ duyệt, đã duyệt và lý do từ chối từ sàn
                    </span>
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

                {selectedWindowId !== "all" && (
                  <div style={{ marginBottom: 12 }}>
                    <button
                      type="button"
                      className={common.cardLink}
                      onClick={() => setSelectedWindowId("all")}
                    >
                      Bỏ lọc khung giờ (xem tất cả)
                    </button>
                  </div>
                )}

                {filteredFlashRows.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Gian hàng chưa có suất Flash sale nào phù hợp với bộ lọc hiện tại.
                  </div>
                ) : (
                  <>
                    <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            <th>Sản phẩm & SKU</th>
                            <th>Khung giờ</th>
                            <th className={common.numCol}>Giá Flash sale</th>
                            <th>Suất còn lại</th>
                            <th>Trạng thái duyệt</th>
                            <th>Ghi chú / Lý do từ chối</th>
                          </tr>
                        </thead>
                        <tbody>
                          {filteredFlashRows.map(({ item, windowName, windowStatus }) => {
                            const found = skuLookup.get(item.sku_id);
                            const origPrice = item.original_price_vnd || found?.sku.price_vnd || 0;
                            const sold = Math.max(0, item.total_slots - item.remaining_slots);
                            const itemStatusKey = item.status.toUpperCase();
                            const itemStatusMeta = FLASH_ITEM_STATUS_META[itemStatusKey] ?? {
                              label: item.status,
                              tone: "neutral" as const,
                            };
                            const winPill = slotStatusPill(windowStatus);
                            return (
                              <tr key={item.id}>
                                <td>
                                  <div className={styles.flashProdCell}>
                                    <img
                                      src={
                                        found
                                          ? found.sku.image_url ||
                                            getProductCoverImage(found.product)
                                          : "/products/ao-thun-cotton-trang.png"
                                      }
                                      alt={found?.product.name ?? item.sku_id}
                                      className={styles.flashThumb}
                                      width={34}
                                      height={45}
                                    />
                                    <div>
                                      <strong>
                                        {found ? found.product.name : `SKU ${item.sku_id}`}
                                      </strong>
                                      <span className={common.cellSub}>
                                        {found ? found.sku.sku_code : item.sku_id} · Tối đa{" "}
                                        {formatInt(item.per_user_limit)}/khách
                                      </span>
                                    </div>
                                  </div>
                                </td>
                                <td>
                                  <div>{windowName}</div>
                                  <span className={common.cellSub}>{winPill.label}</span>
                                </td>
                                <td className={common.numCol}>
                                  <strong>{formatVnd(item.flash_price_vnd)}</strong>
                                  {origPrice > item.flash_price_vnd && (
                                    <>
                                      <span className={styles.discountTag}>
                                        {formatDiscountPercent(origPrice, item.flash_price_vnd)}
                                      </span>
                                      <span className={common.cellSub}>
                                        Gốc {formatVnd(origPrice)}
                                      </span>
                                    </>
                                  )}
                                </td>
                                <td className={styles.quotaCell}>
                                  <ProgressBar
                                    value={sold}
                                    max={Math.max(1, item.total_slots)}
                                    leftLabel={`Đã bán ${formatInt(sold)} / ${formatInt(item.total_slots)}`}
                                    rightLabel={`Còn ${formatInt(item.remaining_slots)}`}
                                  />
                                </td>
                                <td>
                                  <span className={pillClassForTone(itemStatusMeta.tone)}>
                                    {itemStatusMeta.label}
                                  </span>
                                </td>
                                <td>
                                  {item.rejection_reason ? (
                                    <span className={common.fieldError}>
                                      {item.rejection_reason}
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
                      {filteredFlashRows.map(({ item, windowName }) => {
                        const found = skuLookup.get(item.sku_id);
                        const itemStatusMeta = FLASH_ITEM_STATUS_META[
                          item.status.toUpperCase()
                        ] ?? {
                          label: item.status,
                          tone: "neutral" as const,
                        };
                        return (
                          <div key={item.id} className={common.mobileRow}>
                            <div className={common.mobileRowTop}>
                              <div>
                                <strong>
                                  {found
                                    ? `${found.product.name} · ${found.sku.sku_code}`
                                    : item.sku_id}
                                </strong>
                                <span className={common.cellSub}>
                                  {windowName} · {formatVnd(item.flash_price_vnd)}
                                </span>
                              </div>
                              <span className={pillClassForTone(itemStatusMeta.tone)}>
                                {itemStatusMeta.label}
                              </span>
                            </div>
                            <div className={common.mobileRowMeta}>
                              <span>
                                Còn {formatInt(item.remaining_slots)}/{formatInt(item.total_slots)}{" "}
                                suất
                              </span>
                              <span>Tối đa {formatInt(item.per_user_limit)}/khách</span>
                            </div>
                            {item.rejection_reason && (
                              <p className={common.fieldError}>Lý do: {item.rejection_reason}</p>
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

          {tab === "campaign" && (
            <PromotionsCampaignTab
              shopId={mockShopKey}
              shopShortName={shop.name}
              shopProducts={mockProducts}
              showCampaignForm={showCampaignForm}
              onCloseCampaignForm={() => setShowCampaignForm(false)}
              onNotify={showToast}
            />
          )}
        </>
      )}
    </div>
  );
}

function SellerPromotionsContent() {
  return (
    <SellerShopGate pageTitle="Khuyến mãi gian hàng">
      {(shop) => <SellerPromotionsInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerPromotionsPage() {
  return (
    <SellerSuspense>
      <SellerPromotionsContent />
    </SellerSuspense>
  );
}
