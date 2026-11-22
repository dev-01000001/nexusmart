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
import { PromotionsBundleTab } from "@/features/seller/PromotionsBundleTab";
import { PromotionsCampaignTab } from "@/features/seller/PromotionsCampaignTab";
import { getProductCoverImage } from "@/features/seller/types";
import { parseUiState, useSellerSession } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type PromoTab = "vouchers" | "flash" | "combo" | "campaign";

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

function SellerPromotionsInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as PromoTab | null;
  const { showToast } = useMockStore();
  const { mockShopKey } = useSellerContext();
  const session = useSellerSession();

  const [tab, setTab] = useState<PromoTab>(
    initialTab && ["vouchers", "flash", "combo", "campaign"].includes(initialTab)
      ? initialTab
      : "vouchers"
  );

  const [vouchers, setVouchers] = useState<GatewayVoucher[]>([]);
  const [flashSales, setFlashSales] = useState<GatewayFlashSale[]>([]);
  const [products, setProducts] = useState<GatewayProduct[]>([]);
  const [submittedPendingItems, setSubmittedPendingItems] = useState<
    Array<{ windowName: string; item: GatewayFlashSaleItem }>
  >([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [showVoucherForm, setShowVoucherForm] = useState(false);
  const [showFlashForm, setShowFlashForm] = useState(false);
  const [showBundleForm, setShowBundleForm] = useState(false);
  const [showCampaignForm, setShowCampaignForm] = useState(false);
  const [selectedWindowId, setSelectedWindowId] = useState<string>("all");

  // Voucher create form
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
      const [voucherRes, flashRes, prodRes] = await Promise.all([
        api<{ vouchers: GatewayVoucher[] }>("seller/vouchers"),
        api<{ flash_sales: GatewayFlashSale[] }>("flash-sales?include_upcoming=true"),
        api<PaginatedResponse<GatewayProduct>>("seller/products?page=1&page_size=100"),
      ]);
      const nextVouchers = voucherRes.vouchers ?? [];
      const nextFlash = flashRes.flash_sales ?? [];
      const nextProducts = prodRes.items ?? [];
      setVouchers(nextVouchers);
      setFlashSales(nextFlash);
      setProducts(nextProducts);
      if (nextFlash.length > 0) {
        setFWindowId((prev) => prev || nextFlash[0].id);
      }
      const firstSku = nextProducts.flatMap((p) => p.skus ?? []).find((s) => s.is_active);
      if (firstSku) {
        setFSkuId((prev) => prev || firstSku.id);
      }
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải dữ liệu khuyến mãi lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

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

  const selectableSkus = useMemo(
    () =>
      products
        .filter((p) => p.status === "APPROVED" || p.status === "PENDING")
        .flatMap((p) =>
          (p.skus ?? [])
            .filter((s) => s.is_active && s.available_stock > 0)
            .map((sku) => ({ product: p, sku }))
        ),
    [products]
  );

  const shopFlashEntries = useMemo(() => {
    const rows: Array<{
      id: string;
      windowId: string;
      windowName: string;
      windowStatus: "ended" | "live" | "upcoming";
      item: GatewayFlashSaleItem;
    }> = [];
    for (const win of flashSales) {
      const st = getWindowStatus(win);
      for (const item of win.items ?? []) {
        if (item.shop_id === shop.id) {
          rows.push({
            id: item.id,
            windowId: win.id,
            windowName: win.name,
            windowStatus: st,
            item,
          });
        }
      }
    }
    return rows;
  }, [flashSales, shop.id]);

  const filteredFlashEntries = useMemo(() => {
    if (selectedWindowId === "all") return shopFlashEntries;
    return shopFlashEntries.filter((e) => e.windowId === selectedWindowId);
  }, [shopFlashEntries, selectedWindowId]);

  const activeVoucherCount = vouchers.filter((v) => v.is_active).length;
  const voucherUsedTotal = vouchers.reduce((s, v) => s + v.used_count, 0);
  const voucherQuotaTotal = vouchers.reduce((s, v) => s + v.quota, 0);
  const voucherBudgetUsedTotal = vouchers.reduce((s, v) => s + v.used_budget_vnd, 0);

  const mockProducts = useMemo(
    () => PRODUCTS.filter((p) => p.shopId === mockShopKey),
    [mockShopKey]
  );

  const handleCreateVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const rawVal = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.max(0, Math.trunc(Number(vMinOrder) || 0));
    const quota = Math.trunc(Number(vQuota) || 0);
    const perUser = Math.max(1, Math.trunc(Number(vPerUser) || 1));
    const budgetVnd = Math.max(0, Math.trunc(Number(vBudget) || 0));
    const maxDiscountVnd =
      vType === "PERCENT" ? Math.max(0, Math.trunc(Number(vMaxDiscount) || 0)) : 0;

    if (code.length < 3) {
      setVError("Mã voucher cần tối thiểu 3 ký tự liền nhau.");
      return;
    }
    if (vType === "FIXED" && rawVal < 1000) {
      setVError("Mức giảm cố định tối thiểu 1.000₫.");
      return;
    }
    if (vType === "PERCENT" && (rawVal < 1 || rawVal > 90)) {
      setVError("Mức giảm phần trăm hợp lệ từ 1% đến 90%.");
      return;
    }
    if (quota < 1) {
      setVError("Tổng lượt phát hành tối thiểu 1 suất.");
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
      setVCode("");
      setVName("");
      setShowVoucherForm(false);
      showToast(`Đã phát hành mã voucher ${code} cho gian hàng.`);
    } catch (err) {
      setVError(getErrorMessage(err, "Không thể tạo voucher lúc này."));
    } finally {
      setVSubmitting(false);
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
    if (totalSlots < 1 || totalSlots > skuEntry.sku.available_stock) {
      setFError(
        `Số suất đăng ký hợp lệ từ 1 đến ${formatInt(skuEntry.sku.available_stock)} (tồn khả dụng hiện tại).`
      );
      return;
    }

    setFError(null);
    setFSubmitting(true);
    try {
      const res = await api<{ item: GatewayFlashSaleItem }>(
        `seller/flash-sales/${targetWin.id}/items`,
        {
          method: "POST",
          json: {
            sku_id: skuEntry.sku.id,
            flash_price_vnd: flashPrice,
            total_slots: totalSlots,
            per_user_limit: perUserLimit,
          },
        }
      );
      if (res.item) {
        setSubmittedPendingItems((prev) => [
          { windowName: targetWin.name, item: res.item },
          ...prev,
        ]);
      }
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

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Khuyến mãi gian hàng</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(activeVoucherCount)} voucher đang hoạt động,{" "}
            {formatInt(shopFlashEntries.length)} suất Flash sale đã duyệt
          </p>
        </div>
        <div className={common.headerActions}>
          {tab === "vouchers" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowVoucherForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showVoucherForm ? "Đóng biểu mẫu" : "Tạo voucher mới"}</span>
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
          {tab === "combo" && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              onClick={() => setShowBundleForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showBundleForm ? "Đóng biểu mẫu" : "Tạo gói Bundle mẫu"}</span>
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
          message="Tạo mã giảm giá gian hàng hoặc đăng ký SKU vào các khung giờ Flash sale đang mở."
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
              aria-selected={tab === "flash"}
              className={`${common.underlineTab} ${tab === "flash" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("flash")}
            >
              Đăng ký Flash sale ({formatInt(shopFlashEntries.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "combo"}
              className={`${common.underlineTab} ${tab === "combo" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("combo")}
            >
              Combo & Mua kèm (
              {formatInt(session.combos.filter((c) => c.shopId === mockShopKey).length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "campaign"}
              className={`${common.underlineTab} ${tab === "campaign" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("campaign")}
            >
              Chiến dịch & BST sàn (
              {formatInt(session.campaignRegs.filter((r) => r.shopId === mockShopKey).length)})
            </button>
          </div>

          {tab === "vouchers" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp voucher gian hàng">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Voucher đang hoạt động</span>
                  <span
                    className={`${common.balanceValue} ${activeVoucherCount === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatInt(activeVoucherCount)}
                  </span>
                  <span className={common.balanceCaption}>
                    Trên tổng số {formatInt(vouchers.length)} mã đã tạo
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
                  onSubmit={(e) => void handleCreateVoucher(e)}
                  aria-label="Biểu mẫu tạo voucher gian hàng"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>Tạo mã giảm giá gian hàng mới</h2>
                    <span className={common.cardMeta}>
                      Áp dụng riêng cho sản phẩm của {shop.name}
                    </span>
                  </div>

                  {vError && (
                    <p className={common.fieldError} style={{ marginBottom: 12 }}>
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
                          step={5000}
                          className={common.textInput}
                          value={vMaxDiscount}
                          onChange={(e) => setVMaxDiscount(e.target.value)}
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
                        step={10000}
                        className={common.textInput}
                        value={vMinOrder}
                        onChange={(e) => setVMinOrder(e.target.value)}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-quota" className={common.fieldLabel}>
                        Tổng số lượt phát hành
                      </label>
                      <input
                        id="v-quota"
                        type="number"
                        min={1}
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
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-budget" className={common.fieldLabel}>
                        Ngân sách tối đa (VND)
                      </label>
                      <input
                        id="v-budget"
                        type="number"
                        step={100000}
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
                      variant="outline"
                      size="sm"
                      shape="app"
                      onClick={() => setShowVoucherForm(false)}
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
                      {vSubmitting ? "Đang phát hành..." : "Lưu & phát hành mã"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách voucher gian hàng">
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>Danh sách voucher của {shop.name}</h2>
                  <span className={common.cardMeta}>
                    Hiển thị các mã giảm giá phạm vi gian hàng từ hệ thống khuyến mãi
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
                          </tr>
                        </thead>
                        <tbody>
                          {vouchers.map((v) => {
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
                                  <span
                                    className={
                                      v.is_active ? `${common.pill} ${common.pillPos}` : common.pill
                                    }
                                  >
                                    {v.is_active ? "Đang chạy" : "Không hoạt động"}
                                  </span>
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>

                    <div className={common.mobileList}>
                      {vouchers.map((v) => (
                        <div key={v.id} className={common.mobileRow}>
                          <div className={common.mobileRowTop}>
                            <div>
                              <span className={styles.codeStrong}>{v.code}</span>
                              <span className={common.cellSub}>{v.name}</span>
                            </div>
                            <span
                              className={
                                v.is_active ? `${common.pill} ${common.pillPos}` : common.pill
                              }
                            >
                              {v.is_active ? "Đang chạy" : "Tạm dừng"}
                            </span>
                          </div>
                          <ProgressBar
                            value={v.used_count}
                            max={Math.max(1, v.quota)}
                            leftLabel={`Đã dùng ${formatInt(v.used_count)}/${formatInt(v.quota)}`}
                            rightLabel={`Tối thiểu ${formatVnd(v.min_order_vnd)}`}
                          />
                        </div>
                      ))}
                    </div>
                  </>
                )}
              </section>
            </>
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
                    const countInWin = shopFlashEntries.filter((e) => e.windowId === win.id).length;
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
                        </span>
                        <span className={common.countCardValue}>
                          {formatInt(countInWin)} SKU đã duyệt
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
                      Sau khi gửi, quản trị viên sẽ duyệt suất Flash sale trước khi mở bán
                    </span>
                  </div>

                  {fError && (
                    <p className={common.fieldError} style={{ marginBottom: 12 }}>
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
                        </div>
                        <div className={common.field}>
                          <label htmlFor="f-price" className={common.fieldLabel}>
                            Giá Flash sale (VND)
                          </label>
                          <input
                            id="f-price"
                            type="number"
                            step={1000}
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

              {submittedPendingItems.length > 0 && (
                <section className={common.card} aria-label="Đăng ký Flash sale vừa gửi">
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>
                      Suất Flash sale vừa đăng ký (Chờ quản trị viên duyệt)
                    </h2>
                    <span className={common.cardMeta}>
                      Dùng mã đăng ký bên dưới để quản trị viên duyệt nhanh
                    </span>
                  </div>
                  <div className={common.tableWrap}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Mã đăng ký</th>
                          <th>Khung giờ</th>
                          <th>SKU</th>
                          <th className={common.numCol}>Giá Flash sale</th>
                          <th className={common.numCol}>Số suất</th>
                          <th>Trạng thái</th>
                        </tr>
                      </thead>
                      <tbody>
                        {submittedPendingItems.map(({ windowName, item }) => {
                          const found = skuLookup.get(item.sku_id);
                          return (
                            <tr key={item.id}>
                              <td>
                                <span className={styles.codeStrong}>{item.id}</span>
                              </td>
                              <td>{windowName}</td>
                              <td>
                                {found
                                  ? `${found.product.name} · ${found.sku.sku_code}`
                                  : item.sku_id}
                              </td>
                              <td className={common.numCol}>{formatVnd(item.flash_price_vnd)}</td>
                              <td className={common.numCol}>{formatInt(item.total_slots)}</td>
                              <td>
                                <span className={`${common.pill} ${common.pillWarn}`}>
                                  Chờ duyệt
                                </span>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                </section>
              )}

              <section
                className={common.card}
                aria-label="Danh sách SKU đã duyệt tham gia Flash sale"
              >
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>
                    Biến thể SKU đã được duyệt tham gia Flash sale
                  </h2>
                  {selectedWindowId !== "all" && (
                    <button
                      type="button"
                      className={common.cardLink}
                      onClick={() => setSelectedWindowId("all")}
                    >
                      Xem tất cả khung giờ
                    </button>
                  )}
                </div>

                {filteredFlashEntries.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Gian hàng chưa có suất Flash sale nào đã được duyệt trong khung giờ này.
                  </div>
                ) : (
                  <div className={common.tableWrap}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Sản phẩm & SKU</th>
                          <th>Khung giờ</th>
                          <th className={common.numCol}>Giá Flash sale</th>
                          <th>Suất còn lại</th>
                          <th>Trạng thái khung</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredFlashEntries.map((entry) => {
                          const found = skuLookup.get(entry.item.sku_id);
                          const sold = Math.max(
                            0,
                            entry.item.total_slots - entry.item.remaining_slots
                          );
                          const pill = slotStatusPill(entry.windowStatus);
                          return (
                            <tr key={entry.id}>
                              <td>
                                <div className={styles.flashProdCell}>
                                  <img
                                    src={
                                      found
                                        ? found.sku.image_url || getProductCoverImage(found.product)
                                        : "/products/ao-thun-cotton-trang.png"
                                    }
                                    alt={found?.product.name ?? entry.item.sku_id}
                                    className={styles.flashThumb}
                                    width={34}
                                    height={45}
                                  />
                                  <div>
                                    <strong>
                                      {found ? found.product.name : `SKU ${entry.item.sku_id}`}
                                    </strong>
                                    <span className={common.cellSub}>
                                      {found ? found.sku.sku_code : entry.item.sku_id} · Tối đa{" "}
                                      {formatInt(entry.item.per_user_limit)}/khách
                                    </span>
                                  </div>
                                </div>
                              </td>
                              <td>{entry.windowName}</td>
                              <td className={common.numCol}>
                                <strong>{formatVnd(entry.item.flash_price_vnd)}</strong>
                                {found && found.sku.price_vnd > entry.item.flash_price_vnd && (
                                  <span className={styles.discountTag}>
                                    {formatDiscountPercent(
                                      found.sku.price_vnd,
                                      entry.item.flash_price_vnd
                                    )}
                                  </span>
                                )}
                              </td>
                              <td className={styles.quotaCell}>
                                <ProgressBar
                                  value={sold}
                                  max={Math.max(1, entry.item.total_slots)}
                                  leftLabel={`Đã bán ${formatInt(sold)} / ${formatInt(entry.item.total_slots)}`}
                                  rightLabel={`Còn ${formatInt(entry.item.remaining_slots)}`}
                                />
                              </td>
                              <td>
                                <span className={pill.cls}>{pill.label}</span>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </section>
            </>
          )}

          {tab === "combo" && (
            <PromotionsBundleTab
              shopId={mockShopKey}
              shopShortName={shop.name}
              shopProducts={mockProducts}
              showBundleForm={showBundleForm}
              onCloseBundleForm={() => setShowBundleForm(false)}
              onNotify={showToast}
            />
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
