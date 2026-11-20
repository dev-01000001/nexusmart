"use client";

import React, { useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { FLASH_SALE_SLOTS, SHOPS, getShopById, getTotalStock } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconPlus, ProgressBar, StateView } from "@/components";
import { formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { PromotionsBundleTab } from "@/features/seller/PromotionsBundleTab";
import { PromotionsCampaignTab } from "@/features/seller/PromotionsCampaignTab";
import {
  createSellerVoucher,
  getShopFlashRegistrationsWithSession,
  getShopProductsWithSession,
  getShopVouchersWithSession,
  parseUiState,
  registerSellerFlashSale,
  toggleSellerVoucherActive,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type PromoTab = "vouchers" | "flash" | "combo" | "campaign";

function SellerPromotionsContent() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as PromoTab | null;
  const { activeSellerShopId, showToast } = useStore();
  const session = useSellerSession();
  const activeShop = getShopById(activeSellerShopId) ?? SHOPS[0];

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

  // New voucher form state
  const [vCode, setVCode] = useState("");
  const [vType, setVType] = useState<"fixed" | "percent">("fixed");
  const [vValue, setVValue] = useState("25000");
  const [vMinOrder, setVMinOrder] = useState("350000");
  const [vQuota, setVQuota] = useState("100");
  const [vPeriod, setVPeriod] = useState("21/11 - 30/11");
  const [vError, setVError] = useState<string | null>(null);

  // New flash sale registration state
  const shopProducts = useMemo(
    () => getShopProductsWithSession(activeShop.id, session),
    [activeShop.id, session]
  );
  const upcomingSlots = useMemo(() => FLASH_SALE_SLOTS.filter((s) => s.status === "upcoming"), []);
  const [fSlotId, setFSlotId] = useState(upcomingSlots[0]?.id ?? "slot-18");
  const [fProductId, setFProductId] = useState(shopProducts[0]?.id ?? "");
  const [fPriceStr, setFPriceStr] = useState("149000");
  const [fQuotaStr, setFQuotaStr] = useState("40");
  const [fLimitStr, setFLimitStr] = useState("2");
  const [fError, setFError] = useState<string | null>(null);

  const vouchers = useMemo(
    () => getShopVouchersWithSession(activeShop.id, session),
    [activeShop.id, session]
  );
  const flashRegs = useMemo(
    () => getShopFlashRegistrationsWithSession(activeShop.id, session),
    [activeShop.id, session]
  );

  const activeVoucherCount = vouchers.filter((v) => v.active).length;
  const voucherUsedTotal = vouchers.reduce((s, v) => s + v.usedQuota, 0);
  const voucherQuotaTotal = vouchers.reduce((s, v) => s + v.totalQuota, 0);
  const voucherRevenueTotal = vouchers.reduce((s, v) => s + v.attributedRevenue, 0);

  const liveFlashCount = flashRegs.filter((r) => r.slotStatus === "live").length;

  const filteredFlashRegs = useMemo(() => {
    if (selectedSlotId === "all") return flashRegs;
    return flashRegs.filter((r) => r.slotId === selectedSlotId);
  }, [flashRegs, selectedSlotId]);

  const effectiveFlashProductId = shopProducts.some((p) => p.id === fProductId)
    ? fProductId
    : (shopProducts[0]?.id ?? "");

  const handleCreateVoucher = (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const value = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.trunc(Number(vMinOrder) || 0);
    const quota = Math.trunc(Number(vQuota) || 0);

    if (code.length < 4) {
      setVError("Mã voucher cần tối thiểu 4 ký tự liền nhau.");
      return;
    }
    if (vouchers.some((v) => v.code === code)) {
      setVError(`Mã ${code} đã tồn tại trong gian hàng.`);
      return;
    }
    if (vType === "fixed" && value < 5000) {
      setVError("Mức giảm cố định tối thiểu 5.000₫.");
      return;
    }
    if (vType === "percent" && (value < 5 || value > 50)) {
      setVError("Mức giảm phần trăm hợp lệ từ 5% đến 50%.");
      return;
    }
    if (quota < 10) {
      setVError("Tổng lượt phát hành tối thiểu 10 suất.");
      return;
    }
    setVError(null);

    const discountText = vType === "fixed" ? formatVnd(value) : `${value}%`;
    createSellerVoucher({
      id: `sv-custom-${Date.now()}`,
      shopId: activeShop.id,
      code,
      title: `Giảm ${discountText} đơn từ ${formatVnd(minOrder)}`,
      discountType: vType,
      discountValue: value,
      maxDiscount: vType === "percent" ? 50000 : undefined,
      minOrderValue: minOrder,
      usedQuota: 0,
      totalQuota: quota,
      attributedRevenue: 0,
      periodLabel: vPeriod.trim() || "21/11 - 30/11",
      active: true,
    });
    setVCode("");
    setShowVoucherForm(false);
    showToast(`Đã phát hành mã voucher ${code} cho ${activeShop.shortName}.`);
  };

  const handleRegisterFlash = (e: React.FormEvent) => {
    e.preventDefault();
    const product = shopProducts.find((p) => p.id === effectiveFlashProductId);
    const slot = FLASH_SALE_SLOTS.find((s) => s.id === fSlotId);
    if (!product || !slot) {
      setFError("Vui lòng chọn sản phẩm và khung giờ hợp lệ.");
      return;
    }
    const flashPrice = Math.trunc(Number(fPriceStr) || 0);
    const quota = Math.trunc(Number(fQuotaStr) || 0);
    const limitPerBuyer = Math.max(1, Math.trunc(Number(fLimitStr) || 2));
    const maxStock = getTotalStock(product);

    if (flashPrice <= 0 || flashPrice >= product.price) {
      setFError(`Giá Flash sale phải nhỏ hơn giá bán hiện tại (${formatVnd(product.price)}).`);
      return;
    }
    if (quota < 5 || quota > maxStock) {
      setFError(`Suất cam kết hợp lệ từ 5 đến ${formatInt(maxStock)} (tổng tồn kho hiện tại).`);
      return;
    }
    setFError(null);

    registerSellerFlashSale({
      id: `fs-reg-${Date.now()}`,
      shopId: activeShop.id,
      slotId: slot.id,
      slotLabel: `${slot.startTime} - ${slot.endTime}`,
      slotStatus: slot.status,
      productId: product.id,
      productName: product.shortName,
      productImage: product.image,
      originalPrice: product.price,
      flashPrice,
      usedQuota: 0,
      totalQuota: quota,
      limitPerBuyer,
    });
    setShowFlashForm(false);
    setSelectedSlotId(slot.id);
    showToast(
      `Đã đăng ký ${product.shortName} vào khung giờ Flash sale ${slot.label} (${formatInt(quota)} suất).`
    );
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
            {activeShop.name} · {formatInt(activeVoucherCount)} mã giảm giá đang bật,{" "}
            {formatInt(liveFlashCount)} suất Flash sale đang mở bán · Doanh thu qua voucher{" "}
            {formatVnd(voucherRevenueTotal)}
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

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu khuyến mãi"
          message="Hệ thống kiểm tra suất Flash sale tạm gián đoạn. Vui lòng thử lại."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/khuyen-mai"
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa có chương trình khuyến mãi nào"
          message="Tạo mã giảm giá gian hàng hoặc đăng ký SKU vào khung giờ Flash sale 18:00 và 21:00."
          actionLabel="Tạo voucher đầu tiên"
          onAction={() => {
            setTab("vouchers");
            setShowVoucherForm(true);
          }}
        />
      )}

      {!uiState && (
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
              Đăng ký Flash sale ({formatInt(flashRegs.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "combo"}
              className={`${common.underlineTab} ${tab === "combo" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("combo")}
            >
              Combo & Mua kèm (
              {formatInt(session.combos.filter((c) => c.shopId === activeShop.id).length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "campaign"}
              className={`${common.underlineTab} ${tab === "campaign" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("campaign")}
            >
              Chiến dịch & BST sàn (
              {formatInt(session.campaignRegs.filter((r) => r.shopId === activeShop.id).length)})
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
                    Trên tổng số {formatInt(vouchers.length)} mã đã thiết lập
                  </span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Lượt đã áp dụng</span>
                  <span className={common.balanceValue}>
                    {formatInt(voucherUsedTotal)} / {formatInt(voucherQuotaTotal)}
                  </span>
                  <span className={common.balanceCaption}>Tự động trừ suất ngay khi có đơn</span>
                </div>
                <div className={common.balanceCell}>
                  <span className={common.balanceLabel}>Doanh thu qua voucher</span>
                  <span
                    className={`${common.balanceValue} ${voucherRevenueTotal === 0 ? common.balanceValueZero : ""}`}
                  >
                    {formatVnd(voucherRevenueTotal)}
                  </span>
                  <span className={common.balanceCaption}>Tính trên các đơn hợp lệ trong kỳ</span>
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
                    <span className={common.cardMeta}>Gộp được cùng mã sàn và mã freeship</span>
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
                        placeholder="Ví dụ: BACHDANG30"
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
                        onChange={(e) => setVType(e.target.value as "fixed" | "percent")}
                      >
                        <option value="fixed">Số tiền cố định (VND)</option>
                        <option value="percent">Theo phần trăm (%)</option>
                      </select>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-val" className={common.fieldLabel}>
                        {vType === "fixed" ? "Mức giảm (VND)" : "Phần trăm giảm (%)"}
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
                      <label htmlFor="v-quota" className={common.fieldLabel}>
                        Tổng số lượt phát hành
                      </label>
                      <input
                        id="v-quota"
                        type="number"
                        min={10}
                        className={common.textInput}
                        value={vQuota}
                        onChange={(e) => setVQuota(e.target.value)}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="v-period" className={common.fieldLabel}>
                        Thời gian áp dụng
                      </label>
                      <input
                        id="v-period"
                        type="text"
                        className={common.textInput}
                        value={vPeriod}
                        onChange={(e) => setVPeriod(e.target.value)}
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
                    <Button type="submit" variant="ink" size="sm" shape="app">
                      Lưu & phát hành mã
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách voucher gian hàng">
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>Danh sách voucher của {activeShop.shortName}</h2>
                  <span className={common.cardMeta}>
                    Bật hoặc tạm dừng áp dụng theo thời gian thực
                  </span>
                </div>

                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã voucher</th>
                        <th>Điều kiện áp dụng</th>
                        <th className={common.numCol}>Mức giảm</th>
                        <th>Lượt đã dùng</th>
                        <th className={common.numCol}>Doanh thu</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Kích hoạt</th>
                      </tr>
                    </thead>
                    <tbody>
                      {vouchers.map((v) => {
                        const discountStr =
                          v.discountType === "fixed"
                            ? formatVnd(v.discountValue)
                            : `${v.discountValue}%`;
                        return (
                          <tr key={v.id}>
                            <td>
                              <span className={styles.codeStrong}>{v.code}</span>
                              <span className={common.cellSub}>{v.periodLabel}</span>
                            </td>
                            <td>
                              <span>{v.title}</span>
                              <span className={common.cellSub}>
                                Đơn tối thiểu {formatVnd(v.minOrderValue)}
                              </span>
                            </td>
                            <td className={common.numCol}>
                              <strong>{discountStr}</strong>
                            </td>
                            <td className={styles.quotaCell}>
                              <ProgressBar
                                value={v.usedQuota}
                                max={v.totalQuota}
                                leftLabel={`${formatInt(v.usedQuota)} / ${formatInt(v.totalQuota)}`}
                                rightLabel={`${Math.round((v.usedQuota / Math.max(1, v.totalQuota)) * 100)}%`}
                              />
                            </td>
                            <td className={common.numCol}>{formatVnd(v.attributedRevenue)}</td>
                            <td>
                              <span
                                className={
                                  v.active ? `${common.pill} ${common.pillPos}` : common.pill
                                }
                              >
                                {v.active ? "Đang chạy" : "Tạm dừng"}
                              </span>
                            </td>
                            <td className={common.numCol}>
                              <div className={styles.switchWrap}>
                                <input
                                  type="checkbox"
                                  className={common.toggleSwitch}
                                  checked={v.active}
                                  onChange={(e) => {
                                    toggleSellerVoucherActive(v.id, e.target.checked);
                                    showToast(
                                      e.target.checked
                                        ? `Đã kích hoạt mã ${v.code}.`
                                        : `Đã tạm dừng mã ${v.code}.`
                                    );
                                  }}
                                  aria-label={`Bật hoặc tắt mã ${v.code}`}
                                />
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
                          <span className={common.cellSub}>{v.title}</span>
                        </div>
                        <input
                          type="checkbox"
                          className={common.toggleSwitch}
                          checked={v.active}
                          onChange={(e) => {
                            toggleSellerVoucherActive(v.id, e.target.checked);
                            showToast(
                              e.target.checked
                                ? `Đã kích hoạt mã ${v.code}.`
                                : `Đã tạm dừng mã ${v.code}.`
                            );
                          }}
                          aria-label={`Bật hoặc tắt mã ${v.code}`}
                        />
                      </div>
                      <ProgressBar
                        value={v.usedQuota}
                        max={v.totalQuota}
                        leftLabel={`Đã dùng ${formatInt(v.usedQuota)}/${formatInt(v.totalQuota)}`}
                        rightLabel={formatVnd(v.attributedRevenue)}
                      />
                    </div>
                  ))}
                </div>
              </section>
            </>
          )}

          {tab === "flash" && (
            <>
              {/* 4 Flash Sale Slot filter cards */}
              <div className={common.countCards4} role="tablist" aria-label="Khung giờ Flash sale">
                {FLASH_SALE_SLOTS.map((slot) => {
                  const countInSlot = flashRegs.filter((r) => r.slotId === slot.id).length;
                  const selected = selectedSlotId === slot.id;
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
                        {slot.label} · {slot.statusLabel}
                      </span>
                      <span className={common.countCardValue}>
                        {formatInt(countInSlot)} sản phẩm
                      </span>
                    </button>
                  );
                })}
              </div>

              <div className={common.chipScroller} role="tablist" aria-label="Khung giờ Flash sale">
                <button
                  type="button"
                  role="tab"
                  aria-selected={selectedSlotId === "all"}
                  className={`${common.chipBtn} ${selectedSlotId === "all" ? common.chipBtnActive : ""}`}
                  onClick={() => setSelectedSlotId("all")}
                >
                  <span>Tất cả khung</span>
                  <span className={common.chipCount}>{formatInt(flashRegs.length)}</span>
                </button>
                {FLASH_SALE_SLOTS.map((slot) => {
                  const countInSlot = flashRegs.filter((r) => r.slotId === slot.id).length;
                  const selected = selectedSlotId === slot.id;
                  return (
                    <button
                      key={slot.id}
                      type="button"
                      role="tab"
                      aria-selected={selected}
                      className={`${common.chipBtn} ${selected ? common.chipBtnActive : ""}`}
                      onClick={() => setSelectedSlotId(slot.id)}
                    >
                      <span>{slot.label}</span>
                      <span className={common.chipCount}>{formatInt(countInSlot)}</span>
                    </button>
                  );
                })}
              </div>

              {showFlashForm && (
                <form
                  className={styles.inlineFormCard}
                  onSubmit={handleRegisterFlash}
                  aria-label="Đăng ký sản phẩm vào khung giờ Flash sale"
                >
                  <div className={common.cardHeader}>
                    <h2 className={common.cardTitle}>Đăng ký SKU vào khung giờ sắp mở</h2>
                    <span className={common.cardMeta}>
                      Hệ thống khóa suất cam kết theo thời gian thực khi mở bán
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
                        Khung giờ sắp mở
                      </label>
                      <select
                        id="f-slot"
                        className={common.textInput}
                        value={fSlotId}
                        onChange={(e) => setFSlotId(e.target.value)}
                      >
                        {upcomingSlots.map((slot) => (
                          <option key={slot.id} value={slot.id}>
                            {slot.startTime} - {slot.endTime} ({slot.statusLabel})
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="f-prod" className={common.fieldLabel}>
                        Sản phẩm tham gia
                      </label>
                      <select
                        id="f-prod"
                        className={common.textInput}
                        value={effectiveFlashProductId}
                        onChange={(e) => setFProductId(e.target.value)}
                      >
                        {shopProducts.map((p) => (
                          <option key={p.id} value={p.id}>
                            {p.shortName} ({formatVnd(p.price)})
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
                        min={5}
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
                    <Button type="submit" variant="ink" size="sm" shape="app">
                      Xác nhận đăng ký suất
                    </Button>
                  </div>
                </form>
              )}

              <section className={common.card} aria-label="Danh sách SKU đăng ký Flash sale">
                <div className={common.cardHeader}>
                  <h2 className={common.cardTitle}>
                    Sản phẩm tham gia Flash sale hôm nay
                    {selectedSlotId !== "all" ? " (đang lọc khung giờ)" : ""}
                  </h2>
                  {selectedSlotId !== "all" && (
                    <button
                      type="button"
                      className={common.cardLink}
                      onClick={() => setSelectedSlotId("all")}
                    >
                      Xem cả 4 khung giờ
                    </button>
                  )}
                </div>

                {filteredFlashRegs.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Gian hàng chưa đăng ký sản phẩm nào trong khung giờ này. Chọn Đăng ký suất Flash
                    sale để bổ sung SKU.
                  </div>
                ) : (
                  <>
                    <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            <th>Khung giờ</th>
                            <th>Sản phẩm</th>
                            <th className={common.numCol}>Giá gốc</th>
                            <th className={common.numCol}>Giá Flash sale</th>
                            <th>Suất đã bán / Cam kết</th>
                            <th>Trạng thái</th>
                          </tr>
                        </thead>
                        <tbody>
                          {filteredFlashRegs.map((reg) => {
                            const st = slotStatusPill(reg.slotStatus);
                            return (
                              <tr key={reg.id}>
                                <td>
                                  <strong>{reg.slotLabel}</strong>
                                  <span className={common.cellSub}>
                                    Tối đa {formatInt(reg.limitPerBuyer)}/khách
                                  </span>
                                </td>
                                <td>
                                  <div className={styles.flashProdCell}>
                                    <img
                                      src={reg.productImage}
                                      alt={reg.productName}
                                      className={styles.flashThumb}
                                      width={34}
                                      height={45}
                                    />
                                    <span>{reg.productName}</span>
                                  </div>
                                </td>
                                <td className={common.numCol}>{formatVnd(reg.originalPrice)}</td>
                                <td className={common.numCol}>
                                  <strong>{formatVnd(reg.flashPrice)}</strong>
                                  <span className={styles.discountTag}>
                                    {formatDiscountPercent(reg.originalPrice, reg.flashPrice)}
                                  </span>
                                </td>
                                <td className={styles.quotaCell}>
                                  <ProgressBar
                                    value={reg.usedQuota}
                                    max={reg.totalQuota}
                                    leftLabel={`${formatInt(reg.usedQuota)} / ${formatInt(reg.totalQuota)}`}
                                    rightLabel={`${Math.round((reg.usedQuota / Math.max(1, reg.totalQuota)) * 100)}%`}
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

                    <div className={common.mobileList}>
                      {filteredFlashRegs.map((reg) => {
                        const st = slotStatusPill(reg.slotStatus);
                        return (
                          <div key={reg.id} className={common.mobileRow}>
                            <div className={common.mobileRowTop}>
                              <div className={styles.flashProdCell}>
                                <img
                                  src={reg.productImage}
                                  alt={reg.productName}
                                  className={styles.flashThumb}
                                  width={34}
                                  height={45}
                                />
                                <div>
                                  <strong>{reg.productName}</strong>
                                  <span className={common.cellSub}>{reg.slotLabel}</span>
                                </div>
                              </div>
                              <span className={st.cls}>{st.label}</span>
                            </div>
                            <ProgressBar
                              value={reg.usedQuota}
                              max={reg.totalQuota}
                              leftLabel={`${formatVnd(reg.flashPrice)} (${formatDiscountPercent(reg.originalPrice, reg.flashPrice)})`}
                              rightLabel={`${formatInt(reg.usedQuota)}/${formatInt(reg.totalQuota)} suất`}
                            />
                          </div>
                        );
                      })}
                    </div>
                  </>
                )}
              </section>
            </>
          )}

          {tab === "combo" && (
            <PromotionsBundleTab
              shopId={activeShop.id}
              shopShortName={activeShop.shortName}
              shopProducts={shopProducts}
              showBundleForm={showBundleForm}
              onCloseBundleForm={() => setShowBundleForm(false)}
              onNotify={showToast}
            />
          )}

          {tab === "campaign" && (
            <PromotionsCampaignTab
              shopId={activeShop.id}
              shopShortName={activeShop.shortName}
              shopProducts={shopProducts}
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

export default function SellerPromotionsPage() {
  return (
    <SellerSuspense>
      <SellerPromotionsContent />
    </SellerSuspense>
  );
}
