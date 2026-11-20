"use client";

import React, { useState } from "react";
import type { Product } from "@/data";
import { INITIAL_BIRTHDAY_COFUND } from "@/data";
import { Button, IconPlus, ProgressBar } from "@/components";
import { formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import {
  createSellerAddonDeal,
  createSellerCombo,
  createSellerTierRule,
  toggleSellerAddonDealActive,
  toggleSellerComboActive,
  toggleSellerTierRuleActive,
  updateSellerBirthdayConfig,
  useSellerSession,
} from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "./PromotionsExtra.module.css";

interface Props {
  shopId: string;
  shopShortName: string;
  shopProducts: Product[];
  showBundleForm: boolean;
  onCloseBundleForm: () => void;
  onNotify: (msg: string) => void;
}

export function PromotionsBundleTab({
  shopId,
  shopShortName,
  shopProducts,
  showBundleForm,
  onCloseBundleForm,
  onNotify,
}: Props) {
  const session = useSellerSession();

  const combos = session.combos.filter((c) => c.shopId === shopId);
  const tierRules = session.tierRules.filter((r) => r.shopId === shopId);
  const addonDeals = session.addonDeals.filter((d) => d.shopId === shopId);
  const birthdayCfg = session.birthdayConfigs[shopId] ?? INITIAL_BIRTHDAY_COFUND["shop-bach-dang"];

  // New bundle form state
  const [bundleName, setBundleName] = useState("");
  const [prodAId, setProdAId] = useState(shopProducts[0]?.id ?? "");
  const [prodBId, setProdBId] = useState(shopProducts[1]?.id ?? shopProducts[0]?.id ?? "");
  const [bundlePriceStr, setBundlePriceStr] = useState("319000");
  const [bundleErr, setBundleErr] = useState<string | null>(null);

  // New tier rule inline toggle
  const [showTierForm, setShowTierForm] = useState(false);
  const [tierTitle, setTierTitle] = useState("");
  const [tierScope, setTierScope] = useState("Toàn bộ áo thun & đồ cơ bản của xưởng");
  const [tier2Pct, setTier2Pct] = useState("10");
  const [tier3Pct, setTier3Pct] = useState("15");

  // New add-on deal inline toggle
  const [showAddonForm, setShowAddonForm] = useState(false);
  const [addonMainId, setAddonMainId] = useState(shopProducts[0]?.id ?? "");
  const [addonItemId, setAddonItemId] = useState(shopProducts[1]?.id ?? shopProducts[0]?.id ?? "");
  const [addonDealPriceStr, setAddonDealPriceStr] = useState("149000");

  // Birthday co-fund editor state
  const [bdGiftName, setBdGiftName] = useState(birthdayCfg.giftName);
  const [bdValueStr, setBdValueStr] = useState(String(birthdayCfg.giftValueVnd));
  const [bdShopShareStr, setBdShopShareStr] = useState(String(birthdayCfg.shopSharePct));
  const [bdQuotaStr, setBdQuotaStr] = useState(String(birthdayCfg.monthlyQuota));

  const effectiveProdA = shopProducts.find((p) => p.id === prodAId) ?? shopProducts[0];
  const effectiveProdB =
    shopProducts.find((p) => p.id === prodBId) ?? shopProducts[1] ?? shopProducts[0];

  const handleCreateBundle = (e: React.FormEvent) => {
    e.preventDefault();
    if (!effectiveProdA || !effectiveProdB) return;
    const origTotal = effectiveProdA.price + effectiveProdB.price;
    const comboPrice = Math.trunc(Number(bundlePriceStr) || 0);
    if (bundleName.trim().length < 5) {
      setBundleErr("Tên gói combo cần tối thiểu 5 ký tự.");
      return;
    }
    if (comboPrice <= 0 || comboPrice >= origTotal) {
      setBundleErr(`Giá gói combo phải nhỏ hơn tổng giá lẻ (${formatVnd(origTotal)}).`);
      return;
    }
    setBundleErr(null);

    createSellerCombo({
      id: `cb-custom-${Date.now()}`,
      shopId,
      name: bundleName.trim(),
      skuSummary: `Trừ tồn tự động: ${effectiveProdA.shortName} + ${effectiveProdB.shortName}`,
      componentProductIds: [effectiveProdA.id, effectiveProdB.id],
      originalTotal: origTotal,
      comboPrice,
      deductChildSku: true,
      soldCount: 0,
      active: true,
    });
    setBundleName("");
    onCloseBundleForm();
    onNotify(`Đã tạo gói combo "${bundleName.trim()}" với cơ chế trừ tồn SKU con tự động.`);
  };

  const handleCreateTier = (e: React.FormEvent) => {
    e.preventDefault();
    const p2 = Math.max(3, Math.min(40, Math.trunc(Number(tier2Pct) || 10)));
    const p3 = Math.max(p2 + 1, Math.min(50, Math.trunc(Number(tier3Pct) || 15)));
    createSellerTierRule({
      id: `tr-custom-${Date.now()}`,
      shopId,
      title: tierTitle.trim() || `Mua 2 giảm ${p2}%, mua 3 giảm ${p3}%`,
      scopeLabel: tierScope.trim() || `Sản phẩm của ${shopShortName}`,
      tiers: [
        { minQty: 2, discountPct: p2 },
        { minQty: 3, discountPct: p3 },
      ],
      appliedOrders: 0,
      active: true,
    });
    setTierTitle("");
    setShowTierForm(false);
    onNotify(`Đã kích hoạt quy tắc giảm theo tầng Mua 2 giảm ${p2}% / Mua 3 giảm ${p3}%.`);
  };

  const handleCreateAddon = (e: React.FormEvent) => {
    e.preventDefault();
    const mainP = shopProducts.find((p) => p.id === addonMainId) ?? shopProducts[0];
    const addP =
      shopProducts.find((p) => p.id === addonItemId) ?? shopProducts[1] ?? shopProducts[0];
    if (!mainP || !addP) return;
    const dealPrice = Math.max(
      10000,
      Math.min(addP.price - 10000, Math.trunc(Number(addonDealPriceStr) || 0))
    );
    createSellerAddonDeal({
      id: `ad-custom-${Date.now()}`,
      shopId,
      mainProductName: mainP.shortName,
      addonProductName: `${addP.shortName} (mua kèm gộp ship)`,
      addonOriginalPrice: addP.price,
      addonDealPrice: dealPrice,
      combinedShip: true,
      maxPerOrder: 1,
      soldCount: 0,
      active: true,
    });
    setShowAddonForm(false);
    onNotify(`Đã tạo deal mua kèm gộp ship cho ${addP.shortName} giá ${formatVnd(dealPrice)}.`);
  };

  const handleSaveBirthday = (e: React.FormEvent) => {
    e.preventDefault();
    const shopShare = Math.max(20, Math.min(80, Math.trunc(Number(bdShopShareStr) || 50)));
    const giftValueVnd = Math.max(20000, Math.trunc(Number(bdValueStr) || 50000));
    const monthlyQuota = Math.max(10, Math.trunc(Number(bdQuotaStr) || 100));
    updateSellerBirthdayConfig(shopId, {
      giftName: bdGiftName.trim() || birthdayCfg.giftName,
      giftValueVnd,
      shopSharePct: shopShare,
      platformSharePct: 100 - shopShare,
      monthlyQuota,
    });
    onNotify(
      `Đã lưu gói quà sinh nhật Hội viên Nexus (${shopShare}% xưởng / ${100 - shopShare}% sàn đồng tài trợ).`
    );
  };

  return (
    <div className={styles.sectionStack}>
      {/* Inline Create Bundle Form */}
      {showBundleForm && (
        <form
          className={styles.inlineFormCard}
          onSubmit={handleCreateBundle}
          aria-label="Tạo gói bundle mới"
        >
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Tạo gói Bundle (trừ tồn SKU con tự động)</h2>
            <span className={common.cardMeta}>
              Người mua có thể chọn cỡ từng món trong gói tại trang sản phẩm
            </span>
          </div>

          {bundleErr && (
            <p className={common.fieldError} style={{ marginBottom: 12 }}>
              {bundleErr}
            </p>
          )}

          <div className={styles.formGrid4}>
            <div className={common.field}>
              <label htmlFor="cb-name" className={common.fieldLabel}>
                Tên gói combo
              </label>
              <input
                id="cb-name"
                type="text"
                className={common.textInput}
                value={bundleName}
                onChange={(e) => setBundleName(e.target.value)}
                placeholder="Ví dụ: Bộ đôi Áo thun công sở"
              />
            </div>
            <div className={common.field}>
              <label htmlFor="cb-prod-a" className={common.fieldLabel}>
                Sản phẩm con 1
              </label>
              <select
                id="cb-prod-a"
                className={common.textInput}
                value={effectiveProdA?.id ?? ""}
                onChange={(e) => setProdAId(e.target.value)}
              >
                {shopProducts.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.shortName} ({formatVnd(p.price)})
                  </option>
                ))}
              </select>
            </div>
            <div className={common.field}>
              <label htmlFor="cb-prod-b" className={common.fieldLabel}>
                Sản phẩm con 2
              </label>
              <select
                id="cb-prod-b"
                className={common.textInput}
                value={effectiveProdB?.id ?? ""}
                onChange={(e) => setProdBId(e.target.value)}
              >
                {shopProducts.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.shortName} ({formatVnd(p.price)})
                  </option>
                ))}
              </select>
            </div>
            <div className={common.field}>
              <label htmlFor="cb-price" className={common.fieldLabel}>
                Giá gói combo (VND)
              </label>
              <input
                id="cb-price"
                type="number"
                step={1000}
                className={common.textInput}
                value={bundlePriceStr}
                onChange={(e) => setBundlePriceStr(e.target.value)}
              />
              {effectiveProdA && effectiveProdB && (
                <span className={common.fieldHint}>
                  Tổng giá lẻ: {formatVnd(effectiveProdA.price + effectiveProdB.price)}
                </span>
              )}
            </div>
          </div>

          <div className={styles.formActions}>
            <Button variant="outline" size="sm" shape="app" onClick={onCloseBundleForm}>
              Hủy
            </Button>
            <Button type="submit" variant="ink" size="sm" shape="app">
              Tạo gói combo
            </Button>
          </div>
        </form>
      )}

      {/* 1. Bundle list */}
      <section className={common.card} aria-labelledby="combo-list-heading">
        <div className={common.cardHeader}>
          <h2 id="combo-list-heading" className={common.cardTitle}>
            Gói Bundle trừ tồn SKU con ({formatInt(combos.length)})
          </h2>
          <span className={common.cardMeta}>
            Tự đồng bộ tồn kho từng SKU thành phần khi khách chốt đơn
          </span>
        </div>

        <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
          <table className={common.dataTable}>
            <thead>
              <tr>
                <th>Tên gói combo & SKU thành phần</th>
                <th className={common.numCol}>Tổng giá lẻ</th>
                <th className={common.numCol}>Giá combo</th>
                <th className={common.numCol}>Đã bán</th>
                <th>Trạng thái</th>
                <th className={common.numCol}>Bật / tắt</th>
              </tr>
            </thead>
            <tbody>
              {combos.map((cb) => (
                <tr key={cb.id}>
                  <td>
                    <span>{cb.name}</span>
                    <span className={common.cellSub}>{cb.skuSummary}</span>
                  </td>
                  <td className={common.numCol}>{formatVnd(cb.originalTotal)}</td>
                  <td className={common.numCol}>
                    <strong>{formatVnd(cb.comboPrice)}</strong>
                    <span className={styles.discountTag}>
                      {formatDiscountPercent(cb.originalTotal, cb.comboPrice)}
                    </span>
                  </td>
                  <td className={common.numCol}>{formatInt(cb.soldCount)} gói</td>
                  <td>
                    <span className={cb.active ? `${common.pill} ${common.pillPos}` : common.pill}>
                      {cb.active ? "Đang chạy" : "Tạm dừng"}
                    </span>
                  </td>
                  <td className={common.numCol}>
                    <div className={styles.switchWrap}>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={cb.active}
                        onChange={(e) => {
                          toggleSellerComboActive(cb.id, e.target.checked);
                          onNotify(
                            e.target.checked
                              ? `Đã bật gói combo "${cb.name}".`
                              : `Đã tạm dừng gói combo "${cb.name}".`
                          );
                        }}
                        aria-label={`Bật hoặc tắt gói combo ${cb.name}`}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className={common.mobileList}>
          {combos.map((cb) => (
            <div key={cb.id} className={common.mobileRow}>
              <div className={common.mobileRowTop}>
                <div>
                  <strong>{cb.name}</strong>
                  <span className={common.cellSub}>{cb.skuSummary}</span>
                </div>
                <input
                  type="checkbox"
                  className={common.toggleSwitch}
                  checked={cb.active}
                  onChange={(e) => toggleSellerComboActive(cb.id, e.target.checked)}
                  aria-label={`Bật tắt combo ${cb.name}`}
                />
              </div>
              <div className={common.mobileRowMeta}>
                <span>
                  {formatVnd(cb.comboPrice)} (gốc {formatVnd(cb.originalTotal)})
                </span>
                <span>{formatInt(cb.soldCount)} gói đã bán</span>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 2. Tiered Quantity Discounts */}
      <section className={common.card} aria-labelledby="tier-rules-heading">
        <div className={common.cardHeader}>
          <div>
            <h2 id="tier-rules-heading" className={common.cardTitle}>
              Giảm giá theo tầng số lượng (Mua nhiều giảm sâu)
            </h2>
            <span className={common.cardMeta}>
              Hiển thị trực tiếp dưới giá bán trên trang sản phẩm và tự tính trong giỏ hàng
            </span>
          </div>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => setShowTierForm((prev) => !prev)}
          >
            <IconPlus size={14} />
            <span>{showTierForm ? "Đóng" : "Thêm bậc giảm"}</span>
          </Button>
        </div>

        {showTierForm && (
          <form onSubmit={handleCreateTier} style={{ marginBottom: 16 }}>
            <div className={styles.formGrid4}>
              <div className={common.field}>
                <label htmlFor="tr-title" className={common.fieldLabel}>
                  Tên chương trình
                </label>
                <input
                  id="tr-title"
                  type="text"
                  className={common.textInput}
                  value={tierTitle}
                  onChange={(e) => setTierTitle(e.target.value)}
                  placeholder="Mua nhiều giảm thêm dòng Basic"
                />
              </div>
              <div className={common.field}>
                <label htmlFor="tr-scope" className={common.fieldLabel}>
                  Phạm vi áp dụng
                </label>
                <input
                  id="tr-scope"
                  type="text"
                  className={common.textInput}
                  value={tierScope}
                  onChange={(e) => setTierScope(e.target.value)}
                />
              </div>
              <div className={common.field}>
                <label htmlFor="tr-p2" className={common.fieldLabel}>
                  Mua 2 giảm (%)
                </label>
                <input
                  id="tr-p2"
                  type="number"
                  min={3}
                  max={40}
                  className={common.textInput}
                  value={tier2Pct}
                  onChange={(e) => setTier2Pct(e.target.value)}
                />
              </div>
              <div className={common.field}>
                <label htmlFor="tr-p3" className={common.fieldLabel}>
                  Mua 3 giảm (%)
                </label>
                <input
                  id="tr-p3"
                  type="number"
                  min={5}
                  max={50}
                  className={common.textInput}
                  value={tier3Pct}
                  onChange={(e) => setTier3Pct(e.target.value)}
                />
              </div>
            </div>
            <div className={styles.formActions}>
              <Button type="submit" variant="ink" size="sm" shape="app">
                Lưu bậc giảm giá
              </Button>
            </div>
          </form>
        )}

        <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
          <table className={common.dataTable}>
            <thead>
              <tr>
                <th>Chương trình & phạm vi</th>
                <th>Các tầng giảm</th>
                <th className={common.numCol}>Đơn đã áp dụng</th>
                <th className={common.numCol}>Kích hoạt</th>
              </tr>
            </thead>
            <tbody>
              {tierRules.map((rule) => (
                <tr key={rule.id}>
                  <td>
                    <span>{rule.title}</span>
                    <span className={common.cellSub}>{rule.scopeLabel}</span>
                  </td>
                  <td>
                    <div className={styles.tierPills}>
                      {rule.tiers.map((t) => (
                        <span key={t.minQty} className={styles.tierTag}>
                          Mua {t.minQty} giảm {t.discountPct}%
                        </span>
                      ))}
                    </div>
                  </td>
                  <td className={common.numCol}>{formatInt(rule.appliedOrders)} đơn</td>
                  <td className={common.numCol}>
                    <div className={styles.switchWrap}>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={rule.active}
                        onChange={(e) => {
                          toggleSellerTierRuleActive(rule.id, e.target.checked);
                          onNotify(`Đã cập nhật trạng thái quy tắc "${rule.title}".`);
                        }}
                        aria-label={`Bật tắt ${rule.title}`}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className={common.mobileList}>
          {tierRules.map((rule) => (
            <div key={rule.id} className={common.mobileRow}>
              <div className={common.mobileRowTop}>
                <div>
                  <strong>{rule.title}</strong>
                  <span className={common.cellSub}>{rule.scopeLabel}</span>
                </div>
                <input
                  type="checkbox"
                  className={common.toggleSwitch}
                  checked={rule.active}
                  onChange={(e) => toggleSellerTierRuleActive(rule.id, e.target.checked)}
                  aria-label={`Bật tắt ${rule.title}`}
                />
              </div>
              <div className={styles.tierPills}>
                {rule.tiers.map((t) => (
                  <span key={t.minQty} className={styles.tierTag}>
                    Mua {t.minQty} giảm {t.discountPct}%
                  </span>
                ))}
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 3. Add-on Deals (Mua kèm gộp ship) */}
      <section className={common.card} aria-labelledby="addon-deals-heading">
        <div className={common.cardHeader}>
          <div>
            <h2 id="addon-deals-heading" className={common.cardTitle}>
              Deal mua kèm gộp ship trong giỏ hàng
            </h2>
            <span className={common.cardMeta}>
              Gợi ý tại trang sản phẩm và bước thanh toán để khách dễ đạt ngưỡng freeship xưởng
            </span>
          </div>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => setShowAddonForm((prev) => !prev)}
          >
            <IconPlus size={14} />
            <span>{showAddonForm ? "Đóng" : "Tạo deal mua kèm"}</span>
          </Button>
        </div>

        {showAddonForm && (
          <form onSubmit={handleCreateAddon} style={{ marginBottom: 16 }}>
            <div className={styles.formGrid3}>
              <div className={common.field}>
                <label htmlFor="ad-main" className={common.fieldLabel}>
                  Sản phẩm chính
                </label>
                <select
                  id="ad-main"
                  className={common.textInput}
                  value={addonMainId}
                  onChange={(e) => setAddonMainId(e.target.value)}
                >
                  {shopProducts.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.shortName}
                    </option>
                  ))}
                </select>
              </div>
              <div className={common.field}>
                <label htmlFor="ad-item" className={common.fieldLabel}>
                  Sản phẩm mua kèm
                </label>
                <select
                  id="ad-item"
                  className={common.textInput}
                  value={addonItemId}
                  onChange={(e) => setAddonItemId(e.target.value)}
                >
                  {shopProducts.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.shortName} ({formatVnd(p.price)})
                    </option>
                  ))}
                </select>
              </div>
              <div className={common.field}>
                <label htmlFor="ad-price" className={common.fieldLabel}>
                  Giá mua kèm ưu đãi (VND)
                </label>
                <input
                  id="ad-price"
                  type="number"
                  step={1000}
                  className={common.textInput}
                  value={addonDealPriceStr}
                  onChange={(e) => setAddonDealPriceStr(e.target.value)}
                />
              </div>
            </div>
            <div className={styles.formActions}>
              <Button type="submit" variant="ink" size="sm" shape="app">
                Kích hoạt deal mua kèm
              </Button>
            </div>
          </form>
        )}

        <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
          <table className={common.dataTable}>
            <thead>
              <tr>
                <th>Sản phẩm chính</th>
                <th>Sản phẩm mua kèm (gộp ship)</th>
                <th className={common.numCol}>Giá gốc</th>
                <th className={common.numCol}>Giá mua kèm</th>
                <th className={common.numCol}>Đã bán</th>
                <th className={common.numCol}>Bật / tắt</th>
              </tr>
            </thead>
            <tbody>
              {addonDeals.map((deal) => (
                <tr key={deal.id}>
                  <td>{deal.mainProductName}</td>
                  <td>
                    <span>{deal.addonProductName}</span>
                    <span className={common.cellSub}>
                      Gộp 1 kiện vận chuyển · Tối đa {formatInt(deal.maxPerOrder)} món/đơn
                    </span>
                  </td>
                  <td className={common.numCol}>{formatVnd(deal.addonOriginalPrice)}</td>
                  <td className={common.numCol}>
                    <strong>{formatVnd(deal.addonDealPrice)}</strong>
                    <span className={styles.discountTag}>
                      {formatDiscountPercent(deal.addonOriginalPrice, deal.addonDealPrice)}
                    </span>
                  </td>
                  <td className={common.numCol}>{formatInt(deal.soldCount)}</td>
                  <td className={common.numCol}>
                    <div className={styles.switchWrap}>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={deal.active}
                        onChange={(e) => {
                          toggleSellerAddonDealActive(deal.id, e.target.checked);
                          onNotify("Đã cập nhật trạng thái deal mua kèm gộp ship.");
                        }}
                        aria-label={`Bật tắt deal mua kèm ${deal.addonProductName}`}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className={common.mobileList}>
          {addonDeals.map((deal) => (
            <div key={deal.id} className={common.mobileRow}>
              <div className={common.mobileRowTop}>
                <div>
                  <strong>{deal.addonProductName}</strong>
                  <span className={common.cellSub}>Kèm: {deal.mainProductName}</span>
                </div>
                <input
                  type="checkbox"
                  className={common.toggleSwitch}
                  checked={deal.active}
                  onChange={(e) => toggleSellerAddonDealActive(deal.id, e.target.checked)}
                  aria-label={`Bật tắt deal mua kèm ${deal.addonProductName}`}
                />
              </div>
              <div className={common.mobileRowMeta}>
                <span>
                  {formatVnd(deal.addonDealPrice)} (gốc {formatVnd(deal.addonOriginalPrice)})
                </span>
                <span>{formatInt(deal.soldCount)} lượt mua kèm</span>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 4. Co-funded Birthday Gift Package for Hội viên Nexus */}
      <section className={common.card} aria-labelledby="birthday-cofund-heading">
        <div className={styles.switchRow}>
          <div>
            <h2 id="birthday-cofund-heading" className={common.cardTitle}>
              Gói quà sinh nhật đồng tài trợ cho Hội viên Nexus
            </h2>
            <span className={common.cardMeta}>
              Tự động hiển thị quà sinh nhật trong giỏ hàng của {birthdayCfg.eligibleTiers}
            </span>
          </div>
          <input
            type="checkbox"
            className={common.toggleSwitch}
            checked={birthdayCfg.enabled}
            onChange={(e) => {
              updateSellerBirthdayConfig(shopId, { enabled: e.target.checked });
              onNotify(
                e.target.checked
                  ? "Đã bật tham gia gói quà sinh nhật Hội viên Nexus."
                  : "Đã tạm dừng gói quà sinh nhật đồng tài trợ."
              );
            }}
            aria-label="Bật hoặc tắt gói quà sinh nhật đồng tài trợ"
          />
        </div>

        <div className={styles.cofundGrid}>
          <form onSubmit={handleSaveBirthday}>
            <div className={styles.formGrid2}>
              <div className={common.field}>
                <label htmlFor="bd-gift" className={common.fieldLabel}>
                  Nội dung quà / voucher sinh nhật
                </label>
                <input
                  id="bd-gift"
                  type="text"
                  className={common.textInput}
                  value={bdGiftName}
                  onChange={(e) => setBdGiftName(e.target.value)}
                />
              </div>
              <div className={common.field}>
                <label htmlFor="bd-val" className={common.fieldLabel}>
                  Giá trị quà (VND)
                </label>
                <input
                  id="bd-val"
                  type="number"
                  step={5000}
                  className={common.textInput}
                  value={bdValueStr}
                  onChange={(e) => setBdValueStr(e.target.value)}
                />
              </div>
            </div>
            <div className={styles.formGrid2}>
              <div className={common.field}>
                <label htmlFor="bd-share" className={common.fieldLabel}>
                  Tỷ lệ gian hàng đồng tài trợ (%)
                </label>
                <input
                  id="bd-share"
                  type="number"
                  min={20}
                  max={80}
                  className={common.textInput}
                  value={bdShopShareStr}
                  onChange={(e) => setBdShopShareStr(e.target.value)}
                />
                <span className={common.fieldHint}>
                  Sàn NexusMart đồng tài trợ phần còn lại (
                  {100 - Math.max(20, Math.min(80, Math.trunc(Number(bdShopShareStr) || 50)))}%).
                </span>
              </div>
              <div className={common.field}>
                <label htmlFor="bd-quota" className={common.fieldLabel}>
                  Hạn mức suất quà / tháng
                </label>
                <input
                  id="bd-quota"
                  type="number"
                  min={10}
                  className={common.textInput}
                  value={bdQuotaStr}
                  onChange={(e) => setBdQuotaStr(e.target.value)}
                />
              </div>
            </div>
            <div className={styles.formActions}>
              <Button type="submit" variant="ink" size="sm" shape="app">
                Lưu cấu hình đồng tài trợ
              </Button>
            </div>
          </form>

          <div className={styles.previewCard}>
            <span className={styles.previewTitle}>
              Hiển thị trong giỏ hàng Hội viên Nexus (tháng sinh nhật)
            </span>
            <p className={styles.previewBody}>{birthdayCfg.giftName}</p>
            <div className={styles.kvList}>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Hạng áp dụng</span>
                <span className={styles.kvVal}>{birthdayCfg.eligibleTiers}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Chi phí mỗi suất xưởng chịu</span>
                <span className={styles.kvVal}>
                  {formatVnd(
                    Math.round((birthdayCfg.giftValueVnd * birthdayCfg.shopSharePct) / 100)
                  )}{" "}
                  ({birthdayCfg.shopSharePct}%)
                </span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Sàn đồng tài trợ</span>
                <span className={styles.kvVal}>
                  {formatVnd(
                    birthdayCfg.giftValueVnd -
                      Math.round((birthdayCfg.giftValueVnd * birthdayCfg.shopSharePct) / 100)
                  )}{" "}
                  ({birthdayCfg.platformSharePct}%)
                </span>
              </div>
            </div>
            <ProgressBar
              value={birthdayCfg.claimedCount}
              max={birthdayCfg.monthlyQuota}
              leftLabel={`Đã phát ${formatInt(birthdayCfg.claimedCount)} / ${formatInt(birthdayCfg.monthlyQuota)} suất tháng 11`}
              rightLabel={`${Math.round((birthdayCfg.claimedCount / Math.max(1, birthdayCfg.monthlyQuota)) * 100)}%`}
            />
          </div>
        </div>
      </section>
    </div>
  );
}
