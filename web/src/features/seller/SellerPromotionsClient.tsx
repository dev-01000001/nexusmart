"use client";

import React, { useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { getProductsByShop } from "@/data";
import { Button, IconPlus, ProgressBar, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { PromotionsBundleTab } from "./PromotionsBundleTab";
import { PromotionsCampaignTab } from "./PromotionsCampaignTab";
import { useSellerContext } from "./SellerShell";
import type {
  GwFlashSale,
  GwFlashSaleItem,
  GwSellerProduct,
  GwSellerSku,
  GwSellerVoucher,
} from "./sellerApiTypes";
import { parseUiState, useSellerSession } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/khuyen-mai/page.module.css";

type PromoTab = "vouchers" | "flash" | "combo" | "campaign";

function flashStatusOf(fs: GwFlashSale, nowMs: number): "ended" | "live" | "upcoming" {
  const start = fs.starts_at ? Date.parse(fs.starts_at) : 0;
  const end = fs.ends_at ? Date.parse(fs.ends_at) : 0;
  if (nowMs < start) return "upcoming";
  if (nowMs < end) return "live";
  return "ended";
}

function itemStatusPill(status: string): { label: string; cls: string } {
  switch (status.toUpperCase()) {
    case "APPROVED":
      return { label: "Đã duyệt", cls: `${common.pill} ${common.pillPos}` };
    case "REJECTED":
      return { label: "Từ chối", cls: `${common.pill} ${common.pillNeg}` };
    default:
      return { label: "Chờ duyệt", cls: `${common.pill} ${common.pillWarn}` };
  }
}

export interface SellerPromotionsClientProps {
  initialVouchers: GwSellerVoucher[];
  initialFlashSales: GwFlashSale[];
  shopProducts: GwSellerProduct[];
  loadError: string | null;
}

export function SellerPromotionsClient({
  initialVouchers,
  initialFlashSales,
  shopProducts,
  loadError,
}: SellerPromotionsClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as PromoTab | null;
  const { user, shop, presentationShopKey, showToast } = useSellerContext();
  const mockSession = useSellerSession();

  const [vouchers, setVouchers] = useState<GwSellerVoucher[]>(initialVouchers);
  const [flashSales, setFlashSales] = useState<GwFlashSale[]>(initialFlashSales);

  const [tab, setTab] = useState<PromoTab>(
    initialTab && ["vouchers", "flash", "combo", "campaign"].includes(initialTab)
      ? initialTab
      : "vouchers"
  );
  const [showVoucherForm, setShowVoucherForm] = useState(false);
  const [showFlashForm, setShowFlashForm] = useState(false);
  const [showBundleForm, setShowBundleForm] = useState(false);
  const [showCampaignForm, setShowCampaignForm] = useState(false);
  const [selectedSlotId, setSelectedSlotId] = useState<string>("all");

  // Real voucher creation form
  const [vCode, setVCode] = useState("");
  const [vName, setVName] = useState("");
  const [vType, setVType] = useState<"FIXED" | "PERCENT">("FIXED");
  const [vValue, setVValue] = useState("25000");
  const [vMaxDiscount, setVMaxDiscount] = useState("50000");
  const [vMinOrder, setVMinOrder] = useState("300000");
  const [vQuota, setVQuota] = useState("100");
  const [vBusy, setVBusy] = useState(false);
  const [vError, setVError] = useState<string | null>(null);

  // Real flash sale SKU registration form
  const flatSkus = useMemo(() => {
    const out: Array<{ product: GwSellerProduct; sku: GwSellerSku }> = [];
    for (const product of shopProducts) {
      for (const sku of product.skus ?? []) {
        if (sku.is_active) out.push({ product, sku });
      }
    }
    return out;
  }, [shopProducts]);

  const skuById = useMemo(
    () => new Map(flatSkus.map((entry) => [entry.sku.id, entry])),
    [flatSkus]
  );

  const nowMs = Date.now();
  const openFlashSales = useMemo(
    () => flashSales.filter((fs) => flashStatusOf(fs, nowMs) !== "ended"),
    [flashSales, nowMs]
  );

  const [fSlotId, setFSlotId] = useState(openFlashSales[0]?.id ?? flashSales[0]?.id ?? "");
  const [fSkuId, setFSkuId] = useState(flatSkus[0]?.sku.id ?? "");
  const [fPriceStr, setFPriceStr] = useState("149000");
  const [fQuotaStr, setFQuotaStr] = useState("20");
  const [fLimitStr, setFLimitStr] = useState("2");
  const [fBusy, setFBusy] = useState(false);
  const [fError, setFError] = useState<string | null>(null);

  const shopFlashRegs = useMemo(() => {
    const rows: Array<{
      flashSale: GwFlashSale;
      slotStatus: "ended" | "live" | "upcoming";
      item: GwFlashSaleItem;
      productName: string;
      skuCode: string;
      originalPrice: number;
    }> = [];
    for (const fs of flashSales) {
      const st = flashStatusOf(fs, nowMs);
      for (const item of fs.items ?? []) {
        if (item.shop_id !== user.shopId) continue;
        const info = skuById.get(item.sku_id);
        rows.push({
          flashSale: fs,
          slotStatus: st,
          item,
          productName: info?.product.name ?? item.sku_id,
          skuCode: info?.sku.sku_code ?? item.sku_id,
          originalPrice: info?.sku.price_vnd ?? item.flash_price_vnd,
        });
      }
    }
    return rows;
  }, [flashSales, user.shopId, skuById, nowMs]);

  const filteredFlashRegs = useMemo(() => {
    if (selectedSlotId === "all") return shopFlashRegs;
    return shopFlashRegs.filter((r) => r.flashSale.id === selectedSlotId);
  }, [shopFlashRegs, selectedSlotId]);

  const activeVoucherCount = vouchers.filter((v) => v.is_active).length;
  const voucherUsedTotal = vouchers.reduce((s, v) => s + v.used_count, 0);
  const voucherQuotaTotal = vouchers.reduce((s, v) => s + v.quota, 0);
  const liveFlashCount = shopFlashRegs.filter((r) => r.slotStatus === "live").length;

  const mockProductsForTab = useMemo(
    () => getProductsByShop(presentationShopKey),
    [presentationShopKey]
  );

  const handleCreateVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const val = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.max(0, Math.trunc(Number(vMinOrder) || 0));
    const quota = Math.trunc(Number(vQuota) || 0);
    const maxDisc = Math.max(0, Math.trunc(Number(vMaxDiscount) || 0));

    if (code.length < 4) {
      setVError("Mã voucher cần tối thiểu 4 ký tự liền nhau.");
      return;
    }
    if (vType === "FIXED" && val < 5000) {
      setVError("Mức giảm cố định tối thiểu 5.000₫.");
      return;
    }
    if (vType === "PERCENT" && (val < 1 || val > 80)) {
      setVError("Mức giảm phần trăm hợp lệ từ 1% đến 80%.");
      return;
    }
    if (quota < 1) {
      setVError("Tổng lượt phát hành phải lớn hơn 0.");
      return;
    }

    setVError(null);
    setVBusy(true);
    try {
      const discountLabel = vType === "FIXED" ? formatVnd(val) : `${val}%`;
      const res = await api<{ voucher: GwSellerVoucher }>("/api/seller/vouchers", {
        method: "POST",
        body: {
          code,
          name: vName.trim() || `Giảm ${discountLabel} đơn từ ${formatVnd(minOrder)}`,
          discount_type: vType,
          value_vnd: vType === "FIXED" ? val : 0,
          discount_percent: vType === "PERCENT" ? val : 0,
          max_discount_vnd: vType === "PERCENT" ? maxDisc : 0,
          min_order_vnd: minOrder,
          quota,
          per_user_limit: 1,
        },
      });
      setVouchers((prev) => [res.voucher, ...prev]);
      setVCode("");
      setVName("");
      setShowVoucherForm(false);
      showToast(`Đã phát hành mã voucher ${res.voucher.code} thành công.`);
      router.refresh();
    } catch (err) {
      setVError(err instanceof ApiError ? err.message : "Không thể tạo voucher lúc này.");
    } finally {
      setVBusy(false);
    }
  };

  const handleRegisterFlash = async (e: React.FormEvent) => {
    e.preventDefault();
    const entry = skuById.get(fSkuId) ?? flatSkus[0];
    const slot = flashSales.find((s) => s.id === fSlotId) ?? openFlashSales[0];
    if (!entry || !slot) {
      setFError("Vui lòng chọn khung giờ Flash sale và biến thể SKU hợp lệ.");
      return;
    }
    const flashPrice = Math.trunc(Number(fPriceStr) || 0);
    const totalSlots = Math.trunc(Number(fQuotaStr) || 0);
    const limitPerBuyer = Math.max(1, Math.trunc(Number(fLimitStr) || 1));

    if (flashPrice <= 0 || flashPrice >= entry.sku.price_vnd) {
      setFError(
        `Giá Flash sale phải nhỏ hơn giá bán hiện tại (${formatVnd(entry.sku.price_vnd)}).`
      );
      return;
    }
    if (totalSlots < 1 || totalSlots > entry.sku.available_stock) {
      setFError(
        `Số suất cam kết phải từ 1 đến ${formatInt(entry.sku.available_stock)} (tồn khả dụng của SKU).`
      );
      return;
    }

    setFError(null);
    setFBusy(true);
    try {
      const res = await api<{ item: GwFlashSaleItem }>(
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
      setFlashSales((prev) =>
        prev.map((fs) =>
          fs.id === slot.id ? { ...fs, items: [res.item, ...(fs.items ?? [])] } : fs
        )
      );
      setShowFlashForm(false);
      setSelectedSlotId(slot.id);
      showToast(
        `Đã gửi đăng ký ${entry.sku.sku_code} vào khung ${slot.name} (${formatInt(totalSlots)} suất).`
      );
      router.refresh();
    } catch (err) {
      setFError(err instanceof ApiError ? err.message : "Không thể đăng ký suất Flash sale.");
    } finally {
      setFBusy(false);
    }
  };

  const shopShortName = shop?.name ?? "Gian hàng";

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Khuyến mãi gian hàng</h1>
          <p className={common.finding}>
            {shopShortName} · {formatInt(activeVoucherCount)} mã giảm giá đang hoạt động,{" "}
            {formatInt(liveFlashCount)} biến thể đang mở bán Flash sale
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
              disabled={openFlashSales.length === 0 || flatSkus.length === 0}
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
              <span>{showBundleForm ? "Đóng biểu mẫu" : "Tạo gói Bundle mới"}</span>
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
              <span>{showCampaignForm ? "Đóng biểu mẫu" : "Đăng ký chiến dịch sàn"}</span>
            </Button>
          )}
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải danh sách voucher và lịch khung giờ Flash sale..."
          rows={4}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu khuyến mãi"
          message={loadError ?? "Hệ thống khuyến mãi tạm gián đoạn. Vui lòng thử lại."}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {!uiState && !loadError && (
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
              Đăng ký Flash sale ({formatInt(shopFlashRegs.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "combo"}
              className={`${common.underlineTab} ${tab === "combo" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("combo")}
            >
              Combo & Mua kèm (
              {formatInt(mockSession.combos.filter((c) => c.shopId === presentationShopKey).length)}
              )
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "campaign"}
              className={`${common.underlineTab} ${tab === "campaign" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("campaign")}
            >
              Chiến dịch & BST sàn (
              {formatInt(
                mockSession.campaignRegs.filter((r) => r.shopId === presentationShopKey).length
              )}
              )
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
                    Trên tổng số {formatInt(vouchers.length)} mã của gian hàng
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Lượt đã áp dụng</span>
                  <span className={common.balanceValue}>
                    {formatInt(voucherUsedTotal)} / {formatInt(voucherQuotaTotal)}
                  </span>
                  <span className={common.balanceCaption}>Trừ suất tự động khi khách chốt đơn</span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Lượt còn lại</span>
                  <span className={common.balanceValue}>
                    {formatInt(vouchers.reduce((s, v) => s + Math.max(0, v.remaining_quota), 0))}
                  </span>
                  <span className={common.balanceCaption}>Đồng bộ thời gian thực từ máy chủ</span>
                </div>
              </section>

              {showVoucherForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={handleCreateVoucher}
                  aria-label="Biểu mẫu tạo voucher gian hàng"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>Tạo mã giảm giá gian hàng mới</h2>
                    <span className={common.cardMeta}>Áp dụng riêng cho gian hàng của bạn</span>
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
                        placeholder="Ví dụ: SHOP30K"
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
                  </div>

                  <div className={styles.formGrid3}>
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
                    <Button type="submit" variant="ink" size="sm" shape="app" disabled={vBusy}>
                      {vBusy ? "Đang lưu..." : "Lưu & phát hành mã"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách voucher gian hàng">
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>Danh sách voucher của {shopShortName}</h2>
                  <span className={common.cardMeta}>Dữ liệu thật từ dịch vụ khuyến mãi</span>
                </div>

                {vouchers.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Gian hàng chưa có mã giảm giá nào. Bấm Tạo voucher mới để phát hành mã đầu tiên.
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
                            <th>Hiệu lực</th>
                            <th>Trạng thái</th>
                          </tr>
                        </thead>
                        <tbody>
                          {vouchers.map((v) => {
                            const isPct = v.discount_type.toUpperCase() === "PERCENT";
                            const discountStr = isPct
                              ? `${v.discount_percent}%`
                              : formatVnd(v.value_vnd);
                            return (
                              <tr key={v.id}>
                                <td>
                                  <span className={styles.codeStrong}>{v.code}</span>
                                </td>
                                <td>
                                  <span>{v.name}</span>
                                  <span className={common.cellSub}>
                                    Đơn tối thiểu {formatVnd(v.min_order_vnd)}
                                    {isPct && v.max_discount_vnd > 0
                                      ? ` · Tối đa ${formatVnd(v.max_discount_vnd)}`
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
                                <td>
                                  <span className={common.cellSub}>
                                    {v.ends_at ? `Đến ${formatDateVi(v.ends_at)}` : "Đang mở"}
                                  </span>
                                </td>
                                <td>
                                  <span
                                    className={
                                      v.is_active ? `${common.pill} ${common.pillPos}` : common.pill
                                    }
                                  >
                                    {v.is_active ? "Đang chạy" : "Tạm dừng"}
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
                            rightLabel={`Còn ${formatInt(v.remaining_quota)}`}
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
              <div className={common.countCards4} role="tablist" aria-label="Khung giờ Flash sale">
                {flashSales.map((slot) => {
                  const countInSlot = shopFlashRegs.filter(
                    (r) => r.flashSale.id === slot.id
                  ).length;
                  const selected = selectedSlotId === slot.id;
                  const st = flashStatusOf(slot, nowMs);
                  const stLabel =
                    st === "live"
                      ? "Đang mở bán"
                      : st === "upcoming"
                        ? "Sắp mở bán"
                        : "Đã kết thúc";
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
                        {slot.name} · {stLabel}
                      </span>
                      <span className={common.countCardValue}>{formatInt(countInSlot)} SKU</span>
                    </button>
                  );
                })}
              </div>

              {showFlashForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={handleRegisterFlash}
                  aria-label="Đăng ký SKU vào khung giờ Flash sale"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>
                      Đăng ký biến thể SKU vào khung giờ Flash sale
                    </h2>
                    <span className={common.cardMeta}>
                      Suất đăng ký sẽ chuyển quản trị phê duyệt
                    </span>
                  </div>

                  {fError && (
                    <p className={common.fieldError} style={{ marginBottom: 12 }}>
                      {fError}
                    </p>
                  )}

                  <div className={styles.formGrid4}>
                    <div className={common.field}>
                      <label htmlFor="f-slot" className={common.fieldLabel}>
                        Khung giờ Flash sale
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
                        {flatSkus.map(({ product, sku }) => (
                          <option key={sku.id} value={sku.id}>
                            {product.name} · {sku.sku_code} ({formatVnd(sku.price_vnd)}, tồn{" "}
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
                        value={fQuotaStr}
                        onChange={(e) => setFQuotaStr(e.target.value)}
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
                        max={5}
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
                    <Button type="submit" variant="ink" size="sm" shape="app" disabled={fBusy}>
                      {fBusy ? "Đang gửi..." : "Xác nhận đăng ký suất"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách SKU đăng ký Flash sale">
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>SKU gian hàng tham gia Flash sale</h2>
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

                {filteredFlashRegs.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Gian hàng chưa có SKU nào đăng ký trong khung giờ này.
                  </div>
                ) : (
                  <div className={common.tableWrap}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Khung giờ</th>
                          <th>Sản phẩm & SKU</th>
                          <th className={common.numCol}>Giá Flash sale</th>
                          <th>Suất còn lại</th>
                          <th>Trạng thái</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredFlashRegs.map((row) => {
                          const st = itemStatusPill(row.item.status);
                          const used = Math.max(0, row.item.total_slots - row.item.remaining_slots);
                          return (
                            <tr key={row.item.id}>
                              <td>{row.flashSale.name}</td>
                              <td>
                                <span>{row.productName}</span>
                                <span className={common.cellSub}>
                                  {row.skuCode} · Tối đa {formatInt(row.item.per_user_limit)}/khách
                                </span>
                              </td>
                              <td className={common.numCol}>
                                <strong>{formatVnd(row.item.flash_price_vnd)}</strong>
                                {row.originalPrice > row.item.flash_price_vnd && (
                                  <span className={common.cellSub}>
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
                                  leftLabel={`Đã bán ${formatInt(used)}/${formatInt(row.item.total_slots)}`}
                                  rightLabel={`Còn ${formatInt(row.item.remaining_slots)}`}
                                />
                              </td>
                              <td>
                                <span className={st.cls}>{st.label}</span>
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
            <>
              <p className={common.cardMeta} style={{ padding: "0 var(--card-pad)" }}>
                Dữ liệu mẫu cục bộ: cấu hình Combo & Mua kèm chưa nối dịch vụ khuyến mãi nâng cao.
              </p>
              <PromotionsBundleTab
                shopId={presentationShopKey}
                shopShortName={shopShortName}
                shopProducts={mockProductsForTab}
                showBundleForm={showBundleForm}
                onCloseBundleForm={() => setShowBundleForm(false)}
                onNotify={showToast}
              />
            </>
          )}

          {tab === "campaign" && (
            <>
              <p className={common.cardMeta} style={{ padding: "0 var(--card-pad)" }}>
                Dữ liệu mẫu cục bộ: đăng ký chiến dịch cộng đồng và BST sàn chưa nối dịch vụ CMS.
              </p>
              <PromotionsCampaignTab
                shopId={presentationShopKey}
                shopShortName={shopShortName}
                shopProducts={mockProductsForTab}
                showCampaignForm={showCampaignForm}
                onCloseCampaignForm={() => setShowCampaignForm(false)}
                onNotify={showToast}
              />
            </>
          )}
        </>
      )}
    </div>
  );
}
