"use client";

import React, { Suspense, useMemo, useState } from "react";
import type { CategoryCommissionSetting } from "@/data/admin";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button } from "@/components";
import { formatInt, formatPercent, formatVnd } from "@/lib/format";
import { formatNowHHMM } from "@/features/admin/adminMeta";
import {
  updateAdminExtendedConfig,
  updateAdminPlatformConfig,
  useAdminExtendedConfig,
  useAdminPlatformConfig,
} from "@/features/admin/auditStore";
import { AdminApprovalsPanel } from "@/features/admin/AdminApprovalsPanel";
import { AdminCarriersPanel } from "@/features/admin/AdminCarriersPanel";
import { AdminCategoriesPanel } from "@/features/admin/AdminCategoriesPanel";
import { AdminFeesPanel } from "@/features/admin/AdminFeesPanel";
import { AdminPaymentMethodsPanel } from "@/features/admin/AdminPaymentMethodsPanel";
import { AdminUsersPanel } from "@/features/admin/AdminUsersPanel";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminUser } from "@/features/admin/AdminUserContext";
import { roleHasCapability } from "@/lib/admin-rbac";
import { StatusPill } from "@/features/admin/StatusPill";
import { ToggleSwitch } from "@/features/admin/ToggleSwitch";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ConfigTab =
  | "thanh_toan"
  | "duyet_hai_nguoi"
  | "phi_san"
  | "danh_muc"
  | "nguoi_dung"
  | "van_chuyen"
  | "dat_hang"
  | "doi_tra"
  | "thong_bao";

function renderNotificationPreview(template: string): string {
  return template
    .replaceAll("{{parent_code}}", "NM-261120-804")
    .replaceAll("{{sub_order_count}}", "2")
    .replaceAll("{{buyer_name}}", "Nguyễn Minh Quân")
    .replaceAll("{{grand_total}}", "564.000₫")
    .replaceAll("{{sub_tracking_list}}", "SPX-992104 (Bạch Đằng), GHN-448120 (Đà Nẵng)")
    .replaceAll("{{order_code}}", "NM-261120-804-1")
    .replaceAll("{{carrier_name}}", "SPX Express")
    .replaceAll("{{shop_name}}", "Xưởng Dệt Bạch Đằng")
    .replaceAll("{{tracking_code}}", "SPX-992104")
    .replaceAll("{{eta_date}}", "22/11/2026")
    .replaceAll("{{case_code}}", "KN-261119-14")
    .replaceAll("{{new_size}}", "XL")
    .replaceAll("{{refund_amount}}", "179.000₫");
}

function PlatformConfigContent() {
  const { showToast } = useMockStore();
  const adminUser = useAdminUser();
  const role = adminUser?.role ?? "";

  const canReadPaymentMethods = roleHasCapability(role, "payment_methods.read");
  const canReadApprovals = roleHasCapability(role, "approvals.read");
  const canReadFees =
    roleHasCapability(role, "fees.category.read") || roleHasCapability(role, "fees.payment.read");
  const canReadCategories = roleHasCapability(role, "categories.read");
  const canReadUsers = roleHasCapability(role, "users.read");
  const canReadCarriers = roleHasCapability(role, "carriers.read");
  const canSeeMockTabs = role === "super_admin" || role === "admin" || role === "ops";

  const availableTabs = useMemo<Array<{ id: ConfigTab; label: string }>>(() => {
    const list: Array<{ id: ConfigTab; label: string }> = [];
    if (canReadPaymentMethods) list.push({ id: "thanh_toan", label: "Cổng thanh toán" });
    if (canReadApprovals) list.push({ id: "duyet_hai_nguoi", label: "Chờ duyệt 2 người" });
    if (canReadFees) list.push({ id: "phi_san", label: "Phí sàn & Thanh toán" });
    if (canReadCategories) list.push({ id: "danh_muc", label: "Danh mục ngành hàng" });
    if (canReadUsers) list.push({ id: "nguoi_dung", label: "Quản trị người dùng" });
    if (canReadCarriers) list.push({ id: "van_chuyen", label: "Hãng vận chuyển" });
    if (canSeeMockTabs) {
      list.push({ id: "dat_hang", label: "Đặt hàng & Freeship (mẫu)" });
      list.push({ id: "doi_tra", label: "Đổi trả & Hoa hồng (mẫu)" });
      list.push({ id: "thong_bao", label: "Email & Zalo ZNS (mẫu)" });
    }
    return list;
  }, [
    canReadPaymentMethods,
    canReadApprovals,
    canReadFees,
    canReadCategories,
    canReadUsers,
    canReadCarriers,
    canSeeMockTabs,
  ]);

  const platformConfig = useAdminPlatformConfig();
  const updatePlatformConfig = (next: typeof platformConfig) => {
    updateAdminPlatformConfig(next);
    showToast("Đã lưu cấu hình vận hành sàn.");
  };
  const extendedConfig = useAdminExtendedConfig();

  const [selectedTab, setSelectedTab] = useState<ConfigTab>(availableTabs[0]?.id ?? "nguoi_dung");
  const activeTab: ConfigTab = availableTabs.some((t) => t.id === selectedTab)
    ? selectedTab
    : (availableTabs[0]?.id ?? "nguoi_dung");
  const setActiveTab = (next: ConfigTab) => setSelectedTab(next);
  const [savedNotice, setSavedNotice] = useState<Record<string, string>>({});

  // Tab 1: Guest checkout, freeship, coins, shipping carriers
  const [guestCheckoutEnabled, setGuestCheckoutEnabled] = useState(
    extendedConfig.guestCheckoutEnabled
  );
  const [guestLookupAndCancelEnabled, setGuestLookupAndCancelEnabled] = useState(
    extendedConfig.guestLookupAndCancelEnabled
  );
  const [freeshipMinOrder, setFreeshipMinOrder] = useState(platformConfig.freeshipMinOrder);
  const [maxCoinRedeemPercent, setMaxCoinRedeemPercent] = useState(
    platformConfig.maxCoinRedeemPercent
  );
  const [settlementHoldDays, setSettlementHoldDays] = useState(platformConfig.settlementHoldDays);

  // Tab 2: Return standard, exceptions & commissions
  const [standardReturnDays, setStandardReturnDays] = useState(extendedConfig.standardReturnDays);
  const [maxExchangeCount, setMaxExchangeCount] = useState(extendedConfig.maxExchangeCount);
  const [twoWayExchange, setTwoWayExchange] = useState(extendedConfig.twoWayCourierExchangeEnabled);
  const [missingSockPolicy, setMissingSockPolicy] = useState(
    extendedConfig.missingSingleSockPolicyEnabled
  );
  const [bankRefundSchedule, setBankRefundSchedule] = useState(extendedConfig.bankRefundSchedule);
  const [autoApproveLowValueReturns, setAutoApproveLowValueReturns] = useState(
    platformConfig.autoApproveLowValueReturns
  );
  const [autoApproveThresholdVnd, setAutoApproveThresholdVnd] = useState(
    platformConfig.autoApproveThresholdVnd
  );
  const [exceptionsDraft, setExceptionsDraft] = useState(extendedConfig.returnExceptions);
  const [newExcLabel, setNewExcLabel] = useState("");
  const [newExcReason, setNewExcReason] = useState("");
  const [commissions, setCommissions] = useState<CategoryCommissionSetting[]>(
    platformConfig.commissions
  );

  // Tab 4: Email & Zalo ZNS templates (sample preview)
  const [groupParentNotif, setGroupParentNotif] = useState(
    extendedConfig.groupParentOrderNotification
  );
  const [templatesDraft, setTemplatesDraft] = useState(extendedConfig.notificationTemplates);
  const [activeTemplateId, setActiveTemplateId] = useState(
    extendedConfig.notificationTemplates[0]?.id ?? "tpl-email-order"
  );

  const markSectionSaved = (sectionKey: string) => {
    setSavedNotice((prev) => ({ ...prev, [sectionKey]: `Đã lưu lúc ${formatNowHHMM()}` }));
  };

  const commissionStats = useMemo(() => {
    const avg =
      commissions.reduce((s, c) => s + c.commissionPercent, 0) / Math.max(1, commissions.length);
    const min = Math.min(...commissions.map((c) => c.commissionPercent));
    const max = Math.max(...commissions.map((c) => c.commissionPercent));
    return { avg, min, max };
  }, [commissions]);

  const activeTemplate = useMemo(
    () => templatesDraft.find((t) => t.id === activeTemplateId) ?? templatesDraft[0],
    [templatesDraft, activeTemplateId]
  );

  const sampleOrderVnd = 350000;
  const sampleMaxCoins = Math.round((sampleOrderVnd * maxCoinRedeemPercent) / 100);
  const sampleFreeshipEligible = sampleOrderVnd >= freeshipMinOrder;

  // --- Save handlers ---
  const handleSaveCheckoutLogistics = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanFreeship = Math.max(0, Math.trunc(freeshipMinOrder));
    const cleanCoinCap = Math.max(0, Math.min(50, Math.trunc(maxCoinRedeemPercent)));
    const cleanHold = Math.max(1, Math.min(14, Math.trunc(settlementHoldDays)));

    updatePlatformConfig({
      ...platformConfig,
      freeshipMinOrder: cleanFreeship,
      maxCoinRedeemPercent: cleanCoinCap,
      settlementHoldDays: cleanHold,
    });
    updateAdminExtendedConfig((prev) => ({
      ...prev,
      guestCheckoutEnabled,
      guestLookupAndCancelEnabled,
    }));
    markSectionSaved("logistics");
  };

  const handleAddReturnException = () => {
    const label = newExcLabel.trim();
    if (!label) return;
    setExceptionsDraft((prev) => [
      ...prev,
      {
        id: `exc-${Date.now()}`,
        label,
        reason: newExcReason.trim() || "Ngoại lệ không áp dụng đổi trả chuẩn 7 ngày.",
        active: true,
      },
    ]);
    setNewExcLabel("");
    setNewExcReason("");
  };

  const handleSaveReturns = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanDays = Math.max(7, Math.min(90, Math.trunc(standardReturnDays)));
    const cleanCount = Math.max(1, Math.min(5, Math.trunc(maxExchangeCount)));
    const cleanThreshold = Math.max(0, Math.trunc(autoApproveThresholdVnd));

    updatePlatformConfig({
      ...platformConfig,
      autoApproveLowValueReturns,
      autoApproveThresholdVnd: cleanThreshold,
    });
    updateAdminExtendedConfig((prev) => ({
      ...prev,
      standardReturnDays: cleanDays,
      maxExchangeCount: cleanCount,
      twoWayCourierExchangeEnabled: twoWayExchange,
      missingSingleSockPolicyEnabled: missingSockPolicy,
      bankRefundSchedule: bankRefundSchedule.trim() || prev.bankRefundSchedule,
      returnExceptions: exceptionsDraft,
    }));
    markSectionSaved("returns");
  };

  const handleCommissionChange = (
    slug: string,
    field: "commissionPercent" | "returnWindowDays",
    value: number
  ) => {
    setCommissions((prev) =>
      prev.map((item) =>
        item.categorySlug === slug
          ? {
              ...item,
              [field]:
                field === "commissionPercent"
                  ? Math.max(0, Math.min(25, value))
                  : Math.max(1, Math.min(90, Math.trunc(value))),
            }
          : item
      )
    );
  };

  const handleSaveCommissions = (e: React.FormEvent) => {
    e.preventDefault();
    updatePlatformConfig({
      ...platformConfig,
      commissions,
    });
    markSectionSaved("commissions");
  };

  const handleSaveNotifications = (e: React.FormEvent) => {
    e.preventDefault();
    updateAdminExtendedConfig((prev) => ({
      ...prev,
      groupParentOrderNotification: groupParentNotif,
      notificationTemplates: templatesDraft,
    }));
    showToast("Đã lưu mẫu thông báo Email & Zalo ZNS.");
    markSectionSaved("notifications");
  };

  return (
    <div className={styles.settingsContainer}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Cấu hình hệ thống & Quản trị sàn</h1>
          <p className={shared.pageSubtitle}>
            Quản trị danh mục ngành hàng, hoa hồng, tài khoản người dùng và các cấu hình vận hành
            toàn sàn.
          </p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phân nhóm cấu hình sàn">
          {availableTabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              aria-selected={activeTab === tab.id}
              className={`${shared.tabBtn} ${activeTab === tab.id ? shared.tabBtnActive : ""}`}
              onClick={() => setActiveTab(tab.id)}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      {activeTab === "thanh_toan" && canReadPaymentMethods && <AdminPaymentMethodsPanel />}
      {activeTab === "duyet_hai_nguoi" && canReadApprovals && <AdminApprovalsPanel />}
      {activeTab === "phi_san" && canReadFees && <AdminFeesPanel />}
      {activeTab === "danh_muc" && canReadCategories && <AdminCategoriesPanel />}
      {activeTab === "nguoi_dung" && canReadUsers && <AdminUsersPanel />}
      {activeTab === "van_chuyen" && canReadCarriers && <AdminCarriersPanel />}

      {(activeTab === "dat_hang" || activeTab === "doi_tra" || activeTab === "thong_bao") && (
        <section className={shared.panel} role="note" style={{ marginTop: 16 }}>
          <StatusPill label="Bản xem trước, dữ liệu mẫu" tone="warning" />
          <p className={shared.pageSubtitle} style={{ marginTop: 8 }}>
            Mục cấu hình này hiện chỉ lưu tạm trên phiên trình duyệt. Các cấu hình đang đồng bộ máy
            chủ thật nằm tại các tab: Cổng thanh toán, Chờ duyệt 2 người, Phí sàn &amp; Thanh toán,
            Danh mục ngành hàng, Quản trị người dùng và Hãng vận chuyển.
          </p>
        </section>
      )}

      {/* TAB 1: ĐẶT HÀNG VÃNG LAI, FREESHIP & HÃNG VẬN CHUYỂN */}
      {activeTab === "dat_hang" && (
        <div className={styles.sectionList}>
          <section className={styles.sectionRow} aria-labelledby="cfg-guest-heading">
            <div className={styles.labelCol}>
              <h2 id="cfg-guest-heading" className={styles.sectionTitle}>
                Đặt hàng vãng lai, freeship & trần trừ Xu
              </h2>
              <p className={styles.sectionDesc}>
                Bật đặt hàng không cần đăng nhập (Guest checkout), quy định ngưỡng freeship toàn
                sàn, mức trần trừ Xu và thời gian giữ tiền trước giải ngân.
              </p>

              <dl className={styles.summaryList}>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Đặt hàng vãng lai</dt>
                  <dd className={styles.summaryVal}>
                    {guestCheckoutEnabled ? "Đang bật" : "Yêu cầu đăng nhập"}
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Ngưỡng miễn ship toàn sàn</dt>
                  <dd className={styles.summaryVal}>
                    {formatVnd(platformConfig.freeshipMinOrder)}
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Trần trừ Xu tối đa</dt>
                  <dd className={styles.summaryVal}>
                    {platformConfig.maxCoinRedeemPercent}% giá trị đơn
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Giải ngân sau giao thành công</dt>
                  <dd className={styles.summaryVal}>{platformConfig.settlementHoldDays} ngày</dd>
                </div>
              </dl>
              <p className={styles.previewNote}>
                Xem trước trên đơn {formatVnd(sampleOrderVnd)}:{" "}
                {sampleFreeshipEligible ? "đủ điều kiện miễn ship" : "chưa đạt ngưỡng miễn ship"},
                trừ tối đa {formatVnd(sampleMaxCoins)} bằng Xu.
              </p>
            </div>

            <form className={styles.settingsPanel} onSubmit={handleSaveCheckoutLogistics}>
              <div className={styles.switchList}>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <div className={styles.switchTitleLine}>
                      <label htmlFor="sw-guest" className={styles.switchTitle}>
                        Cho phép đặt hàng vãng lai (Guest checkout)
                      </label>
                      <StatusPill
                        label={guestCheckoutEnabled ? "Đang bật" : "Đang tắt"}
                        tone={guestCheckoutEnabled ? "positive" : "neutral"}
                      />
                    </div>
                    <p className={styles.switchDesc}>
                      Người mua lần đầu thanh toán ngay trên 1 trang bằng SĐT và địa chỉ nhận hàng
                      mà không cần tạo tài khoản trước.
                    </p>
                  </div>
                  <ToggleSwitch
                    id="sw-guest"
                    checked={guestCheckoutEnabled}
                    label="Bật hoặc tắt đặt hàng vãng lai"
                    onChange={setGuestCheckoutEnabled}
                  />
                </div>

                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <div className={styles.switchTitleLine}>
                      <label htmlFor="sw-guest-lookup" className={styles.switchTitle}>
                        Tra cứu & tự hủy đơn vãng lai qua mã đơn + SĐT
                      </label>
                      <StatusPill
                        label={guestLookupAndCancelEnabled ? "Đang bật" : "Đang tắt"}
                        tone={guestLookupAndCancelEnabled ? "positive" : "neutral"}
                      />
                    </div>
                    <p className={styles.switchDesc}>
                      Cho phép tự hủy từng đơn con trước trạng thái đóng gói để nhả tồn kho SKU.
                    </p>
                  </div>
                  <ToggleSwitch
                    id="sw-guest-lookup"
                    checked={guestLookupAndCancelEnabled}
                    label="Bật hoặc tắt tra cứu và tự hủy đơn vãng lai"
                    onChange={setGuestLookupAndCancelEnabled}
                  />
                </div>
              </div>

              <div className={styles.fieldGrid}>
                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-freeship" className={styles.fieldLabel}>
                    Ngưỡng freeship toàn sàn (VND)
                  </label>
                  <input
                    id="cfg-freeship"
                    type="number"
                    min={0}
                    step={1000}
                    className={`${styles.settingInput} ${styles.settingInputNum}`}
                    value={freeshipMinOrder}
                    onChange={(e) => setFreeshipMinOrder(Number(e.target.value) || 0)}
                  />
                  <span className={styles.fieldHint}>Chuẩn sàn 249.000₫ (shop từ 199.000₫).</span>
                </div>

                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-coin-cap" className={styles.fieldLabel}>
                    Trần trừ Xu thành viên (%)
                  </label>
                  <input
                    id="cfg-coin-cap"
                    type="number"
                    min={0}
                    max={50}
                    step={1}
                    className={`${styles.settingInput} ${styles.settingInputNum}`}
                    value={maxCoinRedeemPercent}
                    onChange={(e) => setMaxCoinRedeemPercent(Number(e.target.value) || 0)}
                  />
                  <span className={styles.fieldHint}>Giới hạn từ 0% đến 50% tổng tiền hàng.</span>
                </div>

                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-hold-days" className={styles.fieldLabel}>
                    Chu kỳ tạm giữ đối soát (ngày)
                  </label>
                  <input
                    id="cfg-hold-days"
                    type="number"
                    min={1}
                    max={14}
                    step={1}
                    className={`${styles.settingInput} ${styles.settingInputNum}`}
                    value={settlementHoldDays}
                    onChange={(e) => setSettlementHoldDays(Number(e.target.value) || 1)}
                  />
                  <span className={styles.fieldHint}>
                    Số ngày giữ trên sổ cái trước khi mở số dư khả dụng.
                  </span>
                </div>
              </div>

              <div className={styles.panelFooter}>
                <span className={styles.savedStamp}>
                  {savedNotice.logistics ?? "Hiệu lực tức thời trên bước thanh toán người mua."}
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app">
                  Lưu thay đổi
                </Button>
              </div>
            </form>
          </section>
        </div>
      )}

      {/* TAB 2: CHUẨN ĐỔI TRẢ 7 NGÀY, NGOẠI LỆ & HOA HỒNG */}
      {activeTab === "doi_tra" && (
        <div className={styles.sectionList}>
          <section className={styles.sectionRow} aria-labelledby="cfg-return-heading">
            <div className={styles.labelCol}>
              <h2 id="cfg-return-heading" className={styles.sectionTitle}>
                Chuẩn đổi trả sàn 7 ngày & danh sách ngoại lệ
              </h2>
              <p className={styles.sectionDesc}>
                Định nghĩa chính sách đổi size 2 chiều tận nơi của NexusMart, lịch hoàn chuyển khoản
                định kỳ và danh sách nhóm hàng ngoại lệ không áp dụng đổi trả.
              </p>

              <dl className={styles.summaryList}>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Chuẩn đổi trả toàn sàn</dt>
                  <dd className={styles.summaryVal}>
                    {standardReturnDays} ngày · tối đa {maxExchangeCount} lần
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Đổi size 2 chiều tận nơi</dt>
                  <dd className={styles.summaryVal}>
                    {twoWayExchange ? "Bưu tá đổi trực tiếp" : "Tạm tắt"}
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Nhóm ngoại lệ đang bật</dt>
                  <dd className={styles.summaryVal}>
                    {exceptionsDraft.filter((x) => x.active).length} nhóm hàng
                  </dd>
                </div>
              </dl>
            </div>

            <form className={styles.settingsPanel} onSubmit={handleSaveReturns}>
              <div className={styles.fieldGrid}>
                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-ret-days" className={styles.fieldLabel}>
                    Số ngày đổi trả chuẩn sàn
                  </label>
                  <input
                    id="cfg-ret-days"
                    type="number"
                    min={7}
                    max={90}
                    className={`${styles.settingInput} ${styles.settingInputNum}`}
                    value={standardReturnDays}
                    onChange={(e) => setStandardReturnDays(Number(e.target.value) || 45)}
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-ret-max" className={styles.fieldLabel}>
                    Số lần đổi tối đa / đơn
                  </label>
                  <input
                    id="cfg-ret-max"
                    type="number"
                    min={1}
                    max={5}
                    className={`${styles.settingInput} ${styles.settingInputNum}`}
                    value={maxExchangeCount}
                    onChange={(e) => setMaxExchangeCount(Number(e.target.value) || 3)}
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-auto-return-vnd" className={styles.fieldLabel}>
                    Mức duyệt hoàn tự động (VND)
                  </label>
                  <input
                    id="cfg-auto-return-vnd"
                    type="number"
                    min={0}
                    step={10000}
                    className={`${styles.settingInput} ${styles.settingInputNum}`}
                    value={autoApproveThresholdVnd}
                    onChange={(e) => setAutoApproveThresholdVnd(Number(e.target.value) || 0)}
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="cfg-refund-sched" className={styles.fieldLabel}>
                    Lịch hoàn chuyển khoản ngân hàng
                  </label>
                  <input
                    id="cfg-refund-sched"
                    type="text"
                    className={styles.settingInput}
                    value={bankRefundSchedule}
                    onChange={(e) => setBankRefundSchedule(e.target.value)}
                  />
                </div>
              </div>

              <div className={styles.switchList}>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <div className={styles.switchTitleLine}>
                      <label htmlFor="sw-2way" className={styles.switchTitle}>
                        Shipper đổi size 2 chiều tận nơi
                      </label>
                      <StatusPill
                        label={twoWayExchange ? "Đang bật" : "Đang tắt"}
                        tone={twoWayExchange ? "positive" : "neutral"}
                      />
                    </div>
                    <p className={styles.switchDesc}>
                      Bưu tá mang size mới đến giao và thu hồi cỡ cũ trong cùng một lượt.
                    </p>
                  </div>
                  <ToggleSwitch
                    id="sw-2way"
                    checked={twoWayExchange}
                    label="Bật hoặc tắt đổi size 2 chiều tận nơi"
                    onChange={setTwoWayExchange}
                  />
                </div>

                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <div className={styles.switchTitleLine}>
                      <label htmlFor="sw-sock" className={styles.switchTitle}>
                        Chính sách bù lẻ chiếc tất thất lạc
                      </label>
                      <StatusPill
                        label={missingSockPolicy ? "Đang bật" : "Đang tắt"}
                        tone={missingSockPolicy ? "positive" : "neutral"}
                      />
                    </div>
                    <p className={styles.switchDesc}>
                      Áp dụng cho gian hàng mang huy hiệu Đổi trả 7 ngày trên dòng Tất cơ bản.
                    </p>
                  </div>
                  <ToggleSwitch
                    id="sw-sock"
                    checked={missingSockPolicy}
                    label="Bật hoặc tắt chính sách bù lẻ chiếc tất"
                    onChange={setMissingSockPolicy}
                  />
                </div>

                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <div className={styles.switchTitleLine}>
                      <label htmlFor="sw-auto-return" className={styles.switchTitle}>
                        Tự động duyệt trả hàng giá trị nhỏ
                      </label>
                      <StatusPill
                        label={autoApproveLowValueReturns ? "Đang bật" : "Đang tắt"}
                        tone={autoApproveLowValueReturns ? "positive" : "neutral"}
                      />
                    </div>
                    <p className={styles.switchDesc}>
                      Hoàn tiền tức thời qua kênh thanh toán gốc cho đơn dưới{" "}
                      {formatVnd(autoApproveThresholdVnd)}.
                    </p>
                  </div>
                  <ToggleSwitch
                    id="sw-auto-return"
                    checked={autoApproveLowValueReturns}
                    label="Bật hoặc tắt tự động duyệt trả hàng giá trị nhỏ"
                    onChange={setAutoApproveLowValueReturns}
                  />
                </div>
              </div>

              <div className={styles.switchList}>
                <span className={styles.fieldLabel}>
                  Danh sách ngoại lệ không áp dụng đổi trả 7 ngày
                </span>
                {exceptionsDraft.map((exc) => (
                  <div key={exc.id} className={styles.switchRow}>
                    <div className={styles.switchCopy}>
                      <span className={styles.switchTitle}>{exc.label}</span>
                      <span className={styles.switchDesc}>{exc.reason}</span>
                    </div>
                    <ToggleSwitch
                      id={`sw-exc-${exc.id}`}
                      checked={exc.active}
                      label={`Bật hoặc tắt ngoại lệ ${exc.label}`}
                      onChange={(next) =>
                        setExceptionsDraft((prev) =>
                          prev.map((x) => (x.id === exc.id ? { ...x, active: next } : x))
                        )
                      }
                    />
                  </div>
                ))}

                <div className={styles.inlineAddRow}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="exc-label" className={styles.fieldHint}>
                      Nhóm ngoại lệ mới
                    </label>
                    <input
                      id="exc-label"
                      type="text"
                      className={styles.settingInput}
                      value={newExcLabel}
                      onChange={(e) => setNewExcLabel(e.target.value)}
                      placeholder="VD: Sản phẩm in tên riêng"
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="exc-reason" className={styles.fieldHint}>
                      Mô tả hiển thị trên trang sản phẩm
                    </label>
                    <input
                      id="exc-reason"
                      type="text"
                      className={styles.settingInput}
                      value={newExcReason}
                      onChange={(e) => setNewExcReason(e.target.value)}
                      placeholder="Chỉ đổi nếu có lỗi in ấn từ xưởng"
                    />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={handleAddReturnException}
                  >
                    Thêm ngoại lệ
                  </Button>
                </div>
              </div>

              <div className={styles.panelFooter}>
                <span className={styles.savedStamp}>
                  {savedNotice.returns ??
                    "Hiển thị trực tiếp tại khối cam kết trên trang sản phẩm."}
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app">
                  Lưu chuẩn đổi trả
                </Button>
              </div>
            </form>
          </section>

          <section className={styles.sectionRow} aria-labelledby="cfg-commissions-heading">
            <div className={styles.labelCol}>
              <h2 id="cfg-commissions-heading" className={styles.sectionTitle}>
                Phí hoa hồng sàn & hạn đổi trả theo ngành hàng
              </h2>
              <p className={styles.sectionDesc}>
                Mức chiết khấu khấu trừ tự động trên sổ cái ghi kép cho 6 ngành đồ cơ bản.
              </p>

              <dl className={styles.summaryList}>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Số ngành hàng cấu hình</dt>
                  <dd className={styles.summaryVal}>{formatInt(commissions.length)} ngành</dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Phí sàn trung bình</dt>
                  <dd className={styles.summaryVal}>
                    {formatPercent(commissionStats.avg, 2, false)}
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Biên độ phí</dt>
                  <dd className={styles.summaryVal}>
                    {formatPercent(commissionStats.min, 1, false)} –{" "}
                    {formatPercent(commissionStats.max, 1, false)}
                  </dd>
                </div>
              </dl>
            </div>

            <form className={styles.settingsPanel} onSubmit={handleSaveCommissions}>
              <div className={styles.commissionTableWrap}>
                <table className={styles.commissionTable}>
                  <thead>
                    <tr>
                      <th>Ngành hàng</th>
                      <th className={shared.colNum}>Phí hoa hồng (%)</th>
                      <th className={shared.colNum}>Hạn đổi trả (ngày)</th>
                    </tr>
                  </thead>
                  <tbody>
                    {commissions.map((item) => (
                      <tr key={item.categorySlug}>
                        <td>{item.categoryName}</td>
                        <td className={shared.colNum}>
                          <input
                            type="number"
                            min={0}
                            max={25}
                            step={0.5}
                            className={styles.compactInput}
                            value={item.commissionPercent}
                            onChange={(e) =>
                              handleCommissionChange(
                                item.categorySlug,
                                "commissionPercent",
                                Number(e.target.value) || 0
                              )
                            }
                            aria-label={`Phí hoa hồng ngành ${item.categoryName}`}
                          />
                        </td>
                        <td className={shared.colNum}>
                          <input
                            type="number"
                            min={1}
                            max={90}
                            step={1}
                            className={styles.compactInput}
                            value={item.returnWindowDays}
                            onChange={(e) =>
                              handleCommissionChange(
                                item.categorySlug,
                                "returnWindowDays",
                                Number(e.target.value) || 1
                              )
                            }
                            aria-label={`Hạn đổi trả ngành ${item.categoryName}`}
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className={styles.commissionMobileList}>
                {commissions.map((item) => (
                  <div key={item.categorySlug} className={styles.commissionMobileCard}>
                    <span className={styles.fieldLabel}>{item.categoryName}</span>
                    <div className={styles.commissionMobileInputs}>
                      <div className={styles.fieldGroup}>
                        <label
                          htmlFor={`mob-fee-${item.categorySlug}`}
                          className={styles.fieldHint}
                        >
                          Phí sàn (%)
                        </label>
                        <input
                          id={`mob-fee-${item.categorySlug}`}
                          type="number"
                          min={0}
                          max={25}
                          step={0.5}
                          className={`${styles.settingInput} ${styles.settingInputNum}`}
                          value={item.commissionPercent}
                          onChange={(e) =>
                            handleCommissionChange(
                              item.categorySlug,
                              "commissionPercent",
                              Number(e.target.value) || 0
                            )
                          }
                        />
                      </div>
                      <div className={styles.fieldGroup}>
                        <label
                          htmlFor={`mob-ret-${item.categorySlug}`}
                          className={styles.fieldHint}
                        >
                          Đổi trả (ngày)
                        </label>
                        <input
                          id={`mob-ret-${item.categorySlug}`}
                          type="number"
                          min={1}
                          max={90}
                          step={1}
                          className={`${styles.settingInput} ${styles.settingInputNum}`}
                          value={item.returnWindowDays}
                          onChange={(e) =>
                            handleCommissionChange(
                              item.categorySlug,
                              "returnWindowDays",
                              Number(e.target.value) || 1
                            )
                          }
                        />
                      </div>
                    </div>
                  </div>
                ))}
              </div>

              <div className={styles.panelFooter}>
                <span className={styles.savedStamp}>
                  {savedNotice.commissions ?? "Áp dụng cho đơn hàng mới tạo sau thời điểm lưu."}
                </span>
                <Button type="submit" variant="ink" size="sm" shape="app">
                  Lưu biểu phí ngành hàng
                </Button>
              </div>
            </form>
          </section>
        </div>
      )}

      {/* TAB 4: MẪU THÔNG BÁO EMAIL & ZALO ZNS */}
      {activeTab === "thong_bao" && activeTemplate && (
        <div className={styles.sectionList}>
          <section className={styles.sectionRow} aria-labelledby="cfg-notif-heading">
            <div className={styles.labelCol}>
              <h2 id="cfg-notif-heading" className={styles.sectionTitle}>
                Mẫu thông báo Email & Zalo ZNS
              </h2>
              <p className={styles.sectionDesc}>
                Gửi thông báo gộp đơn cha khi người mua đặt đa gian hàng nhưng hiển thị rõ từng mã
                vận đơn con qua Email và Zalo OA / ZNS.
              </p>

              <div className={styles.previewBox} aria-label="Xem trước tin nhắn thực tế">
                <span className={styles.previewChannel}>
                  Xem trước · {activeTemplate.channel} ({activeTemplate.eventCode})
                </span>
                <span className={styles.previewSubject}>
                  {renderNotificationPreview(activeTemplate.subjectOrHeader)}
                </span>
                <p className={styles.previewBody}>
                  {renderNotificationPreview(activeTemplate.bodyTemplate)}
                </p>
              </div>
            </div>

            <form className={styles.settingsPanel} onSubmit={handleSaveNotifications}>
              <div className={styles.switchList}>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <div className={styles.switchTitleLine}>
                      <label htmlFor="sw-grp-notif" className={styles.switchTitle}>
                        Gộp thông báo đơn cha (kèm danh sách vận đơn đơn con)
                      </label>
                      <StatusPill
                        label={groupParentNotif ? "Đang bật" : "Đang tắt"}
                        tone={groupParentNotif ? "positive" : "neutral"}
                      />
                    </div>
                    <p className={styles.switchDesc}>
                      Tránh gửi nhiều email rời rạc khi giỏ hàng tách đơn qua nhiều gian hàng.
                    </p>
                  </div>
                  <ToggleSwitch
                    id="sw-grp-notif"
                    checked={groupParentNotif}
                    label="Bật hoặc tắt gộp thông báo đơn cha"
                    onChange={setGroupParentNotif}
                  />
                </div>
              </div>

              <div className={styles.templateTabRow}>
                {templatesDraft.map((tpl) => (
                  <button
                    key={tpl.id}
                    type="button"
                    className={`${styles.templateBtn} ${
                      tpl.id === activeTemplate.id ? styles.templateBtnActive : ""
                    }`}
                    onClick={() => setActiveTemplateId(tpl.id)}
                  >
                    {tpl.channel}: {tpl.title}
                  </button>
                ))}
              </div>

              <div className={styles.switchRow}>
                <div className={styles.switchCopy}>
                  <span className={styles.switchTitle}>
                    Kích hoạt mẫu {activeTemplate.channel} · {activeTemplate.eventCode}
                  </span>
                  <span className={styles.switchDesc}>
                    Biến hỗ trợ: {"{{parent_code}}"}, {"{{order_code}}"}, {"{{carrier_name}}"},{" "}
                    {"{{tracking_code}}"}, {"{{grand_total}}"}.
                  </span>
                </div>
                <ToggleSwitch
                  id={`sw-tpl-${activeTemplate.id}`}
                  checked={activeTemplate.enabled}
                  label={`Bật hoặc tắt mẫu ${activeTemplate.title}`}
                  onChange={(next) =>
                    setTemplatesDraft((prev) =>
                      prev.map((t) => (t.id === activeTemplate.id ? { ...t, enabled: next } : t))
                    )
                  }
                />
              </div>

              <div className={styles.fieldGroup}>
                <label htmlFor="tpl-subj" className={styles.fieldLabel}>
                  Tiêu đề Email / Header ZNS
                </label>
                <input
                  id="tpl-subj"
                  type="text"
                  className={styles.settingInput}
                  value={activeTemplate.subjectOrHeader}
                  onChange={(e) =>
                    setTemplatesDraft((prev) =>
                      prev.map((t) =>
                        t.id === activeTemplate.id ? { ...t, subjectOrHeader: e.target.value } : t
                      )
                    )
                  }
                />
              </div>

              <div className={styles.fieldGroup}>
                <label htmlFor="tpl-body" className={styles.fieldLabel}>
                  Nội dung mẫu thông báo
                </label>
                <textarea
                  id="tpl-body"
                  className={shared.textareaInput}
                  value={activeTemplate.bodyTemplate}
                  onChange={(e) =>
                    setTemplatesDraft((prev) =>
                      prev.map((t) =>
                        t.id === activeTemplate.id ? { ...t, bodyTemplate: e.target.value } : t
                      )
                    )
                  }
                />
              </div>

              <div className={styles.panelFooter}>
                <span className={styles.savedStamp}>
                  {savedNotice.notifications ?? "Đồng bộ tới dịch vụ thông báo Email và Zalo OA."}
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app">
                  Lưu mẫu thông báo
                </Button>
              </div>
            </form>
          </section>
        </div>
      )}
    </div>
  );
}

function PlatformConfigRoute() {
  return (
    <AdminStateGate
      title="Cấu hình hệ thống"
      subtitle="Quản lý đặt hàng vãng lai, hãng vận chuyển, chuẩn đổi trả 7 ngày, cổng thanh toán và mẫu Email/Zalo ZNS."
      emptyTitle="Chưa có bản ghi cấu hình hệ thống"
      emptyMessage="Không tải được tập cấu hình vận hành mặc định cho sàn."
      errorTitle="Không thể tải cấu hình hệ thống"
      errorMessage="Dịch vụ cấu hình tạm thời không phản hồi. Dữ liệu thay đổi chưa được áp dụng."
      loadingRows={4}
    >
      {() => <PlatformConfigContent />}
    </AdminStateGate>
  );
}

export default function AdminConfigPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Cấu hình hệ thống" />}>
      <PlatformConfigRoute />
    </Suspense>
  );
}
