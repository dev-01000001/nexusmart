"use client";

import React, { useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconCopy, IconPackage, IconPlus, ProgressBar, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { useSellerContext } from "./SellerShell";
import { SellerDrawer, SellerSlidingTabs } from "./SellerUiPrimitives";
import type {
  GwFlashSale,
  GwFlashSaleItem,
  GwSellerProduct,
  GwSellerSku,
  GwSellerVoucher,
} from "./sellerApiTypes";
import { parseUiState } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/khuyen-mai/page.module.css";

type PromoTab = "vouchers" | "flash";

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
  serverNowMs?: number;
}

export function SellerPromotionsClient({
  initialVouchers,
  initialFlashSales,
  shopProducts,
  loadError,
  serverNowMs,
}: SellerPromotionsClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as PromoTab | null;
  const { user, showToast } = useSellerContext();

  const [vouchers, setVouchers] = useState<GwSellerVoucher[]>(initialVouchers);
  const [flashSales, setFlashSales] = useState<GwFlashSale[]>(initialFlashSales);

  const [tab, setTab] = useState<PromoTab>(initialTab === "flash" ? "flash" : "vouchers");
  const [showVoucherForm, setShowVoucherForm] = useState(false);
  const [showFlashForm, setShowFlashForm] = useState(false);
  const [selectedSlotId, setSelectedSlotId] = useState<string>("all");
  const [drawerVoucherId, setDrawerVoucherId] = useState<string | null>(null);

  const [vCode, setVCode] = useState("");
  const [vName, setVName] = useState("");
  const [vType, setVType] = useState<"FIXED" | "PERCENT">("FIXED");
  const [vValue, setVValue] = useState("25000");
  const [vMaxDiscount, setVMaxDiscount] = useState("50000");
  const [vMinOrder, setVMinOrder] = useState("300000");
  const [vQuota, setVQuota] = useState("100");
  const [vBusy, setVBusy] = useState(false);
  const [vError, setVError] = useState<string | null>(null);

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

  const [nowMs] = useState<number>(() => serverNowMs ?? Date.now());
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
      thumb: string | null;
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
          thumb: info?.sku.image_url || info?.product.images?.[0] || null,
        });
      }
    }
    return rows;
  }, [flashSales, user.shopId, skuById, nowMs]);

  const filteredFlashRegs = useMemo(() => {
    if (selectedSlotId === "all") return shopFlashRegs;
    return shopFlashRegs.filter((r) => r.flashSale.id === selectedSlotId);
  }, [shopFlashRegs, selectedSlotId]);

  const drawerVoucher = useMemo(
    () => (drawerVoucherId ? (vouchers.find((v) => v.id === drawerVoucherId) ?? null) : null),
    [vouchers, drawerVoucherId]
  );

  const activeVoucherCount = vouchers.filter((v) => v.is_active).length;
  const voucherUsedTotal = vouchers.reduce((s, v) => s + v.used_count, 0);
  const voucherQuotaTotal = vouchers.reduce((s, v) => s + v.quota, 0);
  const voucherRemainingTotal = vouchers.reduce((s, v) => s + Math.max(0, v.remaining_quota), 0);

  const handleCopyCode = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    showToast(`Đã sao chép mã ${code}.`);
  };

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
      showToast(`Đã phát hành mã ${res.voucher.code}.`);
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
    if (totalSlots < 1 || totalSlots > entry.sku.available_stock) {
      setFError(`Số suất từ 1 đến ${formatInt(entry.sku.available_stock)}.`);
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
      showToast(`Đã đăng ký ${entry.sku.sku_code} vào ${slot.name}.`);
      router.refresh();
    } catch (err) {
      setFError(err instanceof ApiError ? err.message : "Không thể đăng ký Flash sale.");
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
              onClick={() => setShowVoucherForm((prev) => !prev)}
            >
              <IconPlus size={14} />
              <span>{showVoucherForm ? "Đóng biểu mẫu" : "Tạo voucher"}</span>
            </Button>
          )}
          {tab === "flash" && (
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={openFlashSales.length === 0 || flatSkus.length === 0}
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
              { key: "flash", label: "Flash sale", count: shopFlashRegs.length },
            ]}
          />

          {tab === "vouchers" && (
            <>
              <section className={common.balanceStrip3} aria-label="Tổng hợp voucher">
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đang chạy</span>
                  <span
                    className={`${common.balanceValue} num ${activeVoucherCount === 0 ? common.balanceValueZero : ""}`}
                    data-reveal="count"
                    data-count-to={activeVoucherCount}
                  >
                    {formatInt(activeVoucherCount)}
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Đã dùng</span>
                  <span className={common.balanceValue}>
                    {formatInt(voucherUsedTotal)} / {formatInt(voucherQuotaTotal)}
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Lượt còn lại</span>
                  <span
                    className={`${common.balanceValue} num`}
                    data-reveal="count"
                    data-count-to={voucherRemainingTotal}
                  >
                    {formatInt(voucherRemainingTotal)}
                  </span>
                </div>
              </section>

              {showVoucherForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={handleCreateVoucher}
                  aria-label="Biểu mẫu tạo voucher"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>Tạo mã giảm giá mới</h2>
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
                        placeholder="SHOP30K"
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
                        {vType === "FIXED" ? "Mức giảm (VND)" : "Phần trăm (%)"}
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
                        Tổng lượt phát hành
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
                    <Button type="submit" variant="primary" size="sm" shape="app" disabled={vBusy}>
                      {vBusy ? "Đang lưu..." : "Phát hành mã"}
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
                            <th>Hiệu lực</th>
                            <th>Trạng thái</th>
                            <th className={common.numCol}>Thao tác</th>
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
                                  <button
                                    type="button"
                                    className={common.rowTriggerBtn}
                                    onClick={() => setDrawerVoucherId(v.id)}
                                  >
                                    {v.code}
                                  </button>
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
                                <td className={common.numCol}>
                                  <div className={common.rowHoverActions}>
                                    <button
                                      type="button"
                                      className={styles.inlineBtn}
                                      onClick={() => handleCopyCode(v.code)}
                                    >
                                      <IconCopy size={13} />
                                      <span>Chép mã</span>
                                    </button>
                                  </div>
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
                            leftLabel={`${formatInt(v.used_count)}/${formatInt(v.quota)}`}
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
                        {slot.name} ({stLabel})
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
                  aria-label="Đăng ký SKU vào Flash sale"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>Đăng ký suất Flash sale</h2>
                  </div>

                  {fError && (
                    <p className={common.fieldError} style={{ marginBottom: 12 }}>
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
                        {flatSkus.map(({ product, sku }) => (
                          <option key={sku.id} value={sku.id}>
                            {product.name} ({sku.sku_code}, {formatVnd(sku.price_vnd)}, tồn{" "}
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
                        Giới hạn mỗi khách
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
                    <Button type="submit" variant="primary" size="sm" shape="app" disabled={fBusy}>
                      {fBusy ? "Đang gửi..." : "Đăng ký suất"}
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="SKU tham gia Flash sale">
                {selectedSlotId !== "all" && (
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>SKU đăng ký</h2>
                    <button
                      type="button"
                      className={common.cardLink}
                      onClick={() => setSelectedSlotId("all")}
                    >
                      Tất cả khung giờ
                    </button>
                  </div>
                )}

                {filteredFlashRegs.length === 0 ? (
                  <div className={common.inlineEmpty}>Chưa có SKU đăng ký trong khung giờ này.</div>
                ) : (
                  <div className={common.tableWrap}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Sản phẩm &amp; SKU</th>
                          <th>Khung giờ</th>
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
                                    <span className={styles.flashProdName} title={row.productName}>
                                      {row.productName}
                                    </span>
                                    <span className={common.cellSub}>
                                      {row.skuCode}, tối đa {formatInt(row.item.per_user_limit)}
                                      /khách
                                    </span>
                                  </div>
                                </div>
                              </td>
                              <td>{row.flashSale.name}</td>
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
              className={drawerVoucher.is_active ? `${common.pill} ${common.pillPos}` : common.pill}
            >
              {drawerVoucher.is_active ? "Đang chạy" : "Tạm dừng"}
            </span>
          ) : null
        }
        footer={
          drawerVoucher ? (
            <Button
              variant="outline"
              size="sm"
              shape="app"
              onClick={() => handleCopyCode(drawerVoucher.code)}
            >
              <IconCopy size={14} />
              <span>Sao chép mã</span>
            </Button>
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
              <span className={common.drawerKvKey}>Giới hạn mỗi khách</span>
              <span className={common.drawerKvVal}>{formatInt(drawerVoucher.per_user_limit)}</span>
            </div>
            {drawerVoucher.ends_at && (
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Hết hạn</span>
                <span className={common.drawerKvVal}>{formatDateVi(drawerVoucher.ends_at)}</span>
              </div>
            )}
          </div>
        )}
      </SellerDrawer>
    </div>
  );
}
