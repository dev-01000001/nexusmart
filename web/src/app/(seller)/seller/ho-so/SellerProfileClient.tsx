"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconArrowUpRight, IconCheck, IconRefresh, StateView } from "@/components";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { SessionUser } from "@/lib/server/session";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
import { SellerSlidingTabs, statusPillClass } from "@/features/seller/SellerUiPrimitives";
import {
  getShopStatusLabel,
  type GwSellerShop,
  type GwShopShippingSettings,
} from "@/features/seller/sellerApiTypes";
import {
  normalizeAutoReplyConfig,
  parseUiStateParam,
  type SellerAddress,
  type SellerAutoReplyConfig,
} from "@/features/seller/sellerReturns";
import { CARRIER_OPTIONS, parseSellerFreeshipSettings } from "@/features/seller/types";
import type { SellerSiteSummary } from "@/features/seller/site/siteSummary";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type ProfileTab = "shop" | "shipping" | "warehouse" | "autoreply";

const AUTO_REPLY_PRESETS = [
  {
    label: "Mẫu giờ làm việc",
    text: "Cảm ơn bạn đã nhắn tin cho gian hàng. Hiện shop đang ngoài giờ hỗ trợ (8:00 - 21:00) và sẽ phản hồi ngay khi mở ca làm việc tiếp theo.",
  },
  {
    label: "Mẫu phản hồi nhanh",
    text: "Chào bạn, gian hàng đã nhận được tin nhắn. Nếu bạn cần tư vấn size hoặc kiểm tra đơn hàng, vui lòng để lại chiều cao, cân nặng hoặc mã đơn để shop hỗ trợ nhanh hơn.",
  },
];

export interface SellerProfileClientProps {
  initialUser: SessionUser;
  shop: GwSellerShop | null;
  shopError: string | null;
  addresses: SellerAddress[];
  addressesError: string | null;
  initialShippingSettings: GwShopShippingSettings | null;
  shippingSettingsError?: string | null;
  site: SellerSiteSummary;
}

function siteStateLabel(state: SellerSiteSummary["state"]): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
} {
  switch (state) {
    case "published":
      return { label: "Đang mở bán", tone: "pos" };
    case "draft":
      return { label: "Bản nháp", tone: "warn" };
    case "suspended":
      return { label: "Đang tạm khóa", tone: "neg" };
    default:
      return { label: "Chưa khởi tạo", tone: "neutral" };
  }
}

function SellerProfileContent({
  initialUser,
  shop,
  shopError,
  addresses,
  addressesError,
  initialShippingSettings,
  shippingSettingsError = null,
  site,
}: SellerProfileClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiStateParam(searchParams.get("state"));
  const tabParam = searchParams.get("tab") as ProfileTab | null;
  const { showToast } = useSellerContext();

  const [tab, setTab] = useState<ProfileTab>(
    tabParam && ["shop", "shipping", "warehouse", "autoreply"].includes(tabParam)
      ? tabParam
      : "shop"
  );

  // Owner contact state
  const [owner, setOwner] = useState<SessionUser>(initialUser);
  const [fullName, setFullName] = useState(initialUser.fullName);
  const [phone, setPhone] = useState(initialUser.phone);
  const [profileSaving, setProfileSaving] = useState(false);
  const [profileError, setProfileError] = useState("");

  // Real shipping & freeship settings state (/api/seller/shipping-settings)
  const [shipSettingsLoading, setShipSettingsLoading] = useState(false);
  const [shipSettingsSaving, setShipSettingsSaving] = useState(false);
  const [shipSettingsError, setShipSettingsError] = useState<string | null>(shippingSettingsError);
  const [shipSettingsUpdatedAt, setShipSettingsUpdatedAt] = useState<string | undefined>(
    initialShippingSettings?.updated_at
  );
  const [enabledCarriers, setEnabledCarriers] = useState<string[]>(
    initialShippingSettings?.enabled_carrier_codes?.length
      ? initialShippingSettings.enabled_carrier_codes
      : ["ghn", "mock_express"]
  );
  const [defaultWeightStr, setDefaultWeightStr] = useState(
    String(initialShippingSettings?.default_weight_grams || 500)
  );
  const [defaultLengthStr, setDefaultLengthStr] = useState(
    String(initialShippingSettings?.default_length_cm || 25)
  );
  const [defaultWidthStr, setDefaultWidthStr] = useState(
    String(initialShippingSettings?.default_width_cm || 20)
  );
  const [defaultHeightStr, setDefaultHeightStr] = useState(
    String(initialShippingSettings?.default_height_cm || 10)
  );
  const [freeShipEnabled, setFreeShipEnabled] = useState(
    Boolean(initialShippingSettings?.free_ship_enabled)
  );
  const [freeShipMinOrderStr, setFreeShipMinOrderStr] = useState(
    String(initialShippingSettings?.free_ship_min_order_vnd ?? 199000)
  );
  const [freeShipMaxStr, setFreeShipMaxStr] = useState(
    String(initialShippingSettings?.free_ship_max_vnd ?? 25000)
  );

  // Auto-reply state
  const [autoReplyLoaded, setAutoReplyLoaded] = useState(false);
  const [autoReplyLoading, setAutoReplyLoading] = useState(false);
  const [autoReplyUnavailable, setAutoReplyUnavailable] = useState(false);
  const [autoReplyEnabled, setAutoReplyEnabled] = useState(false);
  const [autoReplyMessage, setAutoReplyMessage] = useState("");
  const [autoReplyUpdatedAt, setAutoReplyUpdatedAt] = useState("");
  const [autoReplySaving, setAutoReplySaving] = useState(false);
  const [autoReplyError, setAutoReplyError] = useState("");

  const applyShippingSettings = useCallback((settings: GwShopShippingSettings) => {
    setEnabledCarriers(
      settings.enabled_carrier_codes?.length
        ? settings.enabled_carrier_codes
        : ["ghn", "mock_express"]
    );
    setDefaultWeightStr(String(settings.default_weight_grams || 500));
    setDefaultLengthStr(String(settings.default_length_cm || 25));
    setDefaultWidthStr(String(settings.default_width_cm || 20));
    setDefaultHeightStr(String(settings.default_height_cm || 10));
    setFreeShipEnabled(Boolean(settings.free_ship_enabled));
    setFreeShipMinOrderStr(String(settings.free_ship_min_order_vnd ?? 0));
    setFreeShipMaxStr(String(settings.free_ship_max_vnd ?? 0));
    setShipSettingsUpdatedAt(settings.updated_at);
  }, []);

  const loadShippingSettings = useCallback(async () => {
    setShipSettingsLoading(true);
    setShipSettingsError(null);
    try {
      const res = await api<{ settings?: GwShopShippingSettings | null }>(
        "/api/seller/shipping-settings"
      );
      if (res?.settings) {
        applyShippingSettings(res.settings);
      }
    } catch (err) {
      setShipSettingsError(getErrorMessage(err, "Chưa thể tải cấu hình vận chuyển lúc này."));
    } finally {
      setShipSettingsLoading(false);
    }
  }, [applyShippingSettings]);

  const loadAutoReply = useCallback(async (signal?: AbortSignal) => {
    setAutoReplyLoading(true);
    setAutoReplyUnavailable(false);
    setAutoReplyError("");
    try {
      const res = await api<unknown>("/api/seller/chat/auto-reply", { signal });
      const normalized: SellerAutoReplyConfig = normalizeAutoReplyConfig(res);
      setAutoReplyEnabled(normalized.enabled);
      setAutoReplyMessage(normalized.message);
      setAutoReplyUpdatedAt(normalized.updated_at);
      setAutoReplyLoaded(true);
    } catch (err) {
      if (signal?.aborted) return;
      if (
        err instanceof ApiError &&
        (err.status === 404 || err.status === 503 || err.status === 0)
      ) {
        setAutoReplyUnavailable(true);
      } else {
        setAutoReplyError(
          err instanceof ApiError ? err.message : "Không thể tải cấu hình trả lời tự động."
        );
      }
      setAutoReplyLoaded(true);
    } finally {
      if (!signal?.aborted) {
        setAutoReplyLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    if (tab !== "autoreply" || autoReplyLoaded) return;
    const ctrl = new AbortController();
    void loadAutoReply(ctrl.signal);
    return () => ctrl.abort();
  }, [tab, autoReplyLoaded, loadAutoReply]);

  const carrierChoices = useMemo(() => {
    const known = new Map(CARRIER_OPTIONS.map((c) => [c.carrierCode, c.carrierLabel]));
    for (const code of enabledCarriers) {
      if (!known.has(code)) {
        known.set(code, code.toUpperCase());
      }
    }
    return Array.from(known.entries()).map(([code, label]) => ({ code, label }));
  }, [enabledCarriers]);

  const toggleCarrierCode = (code: string, checked: boolean) => {
    setEnabledCarriers((prev) => {
      if (checked) return prev.includes(code) ? prev : [...prev, code];
      return prev.filter((c) => c !== code);
    });
  };

  const handleSaveShippingSettings = async (e: React.FormEvent) => {
    e.preventDefault();
    if (shipSettingsSaving) return;
    if (enabledCarriers.length === 0) {
      setShipSettingsError("Gian hàng cần bật ít nhất một hãng vận chuyển.");
      return;
    }
    const weight = Math.trunc(Number(defaultWeightStr) || 0);
    const length = Math.trunc(Number(defaultLengthStr) || 0);
    const width = Math.trunc(Number(defaultWidthStr) || 0);
    const height = Math.trunc(Number(defaultHeightStr) || 0);
    if (weight < 1 || weight > 50000) {
      setShipSettingsError("Khối lượng mặc định hợp lệ từ 1 đến 50.000 gram.");
      return;
    }
    if (length < 1 || length > 200 || width < 1 || width > 200 || height < 1 || height > 200) {
      setShipSettingsError("Kích thước kiện mặc định hợp lệ từ 1 đến 200 cm.");
      return;
    }
    const freeshipParsed = parseSellerFreeshipSettings({
      enabled: freeShipEnabled,
      minOrderRaw: freeShipMinOrderStr,
      maxVndRaw: freeShipMaxStr,
    });
    if (!freeshipParsed.ok) {
      setShipSettingsError(freeshipParsed.message);
      return;
    }
    setShipSettingsError(null);
    setShipSettingsSaving(true);
    try {
      const res = await api<{ settings?: GwShopShippingSettings | null }>(
        "/api/seller/shipping-settings",
        {
          method: "PUT",
          body: {
            enabled_carrier_codes: enabledCarriers,
            default_weight_grams: weight,
            default_length_cm: length,
            default_width_cm: width,
            default_height_cm: height,
            free_ship_enabled: freeShipEnabled,
            free_ship_min_order_vnd: freeshipParsed.minOrderVnd,
            free_ship_max_vnd: freeshipParsed.maxVnd,
          },
        }
      );
      if (res?.settings) {
        applyShippingSettings(res.settings);
      }
      showToast("Đã lưu cài đặt vận chuyển và Freeship gian hàng.");
    } catch (err) {
      setShipSettingsError(getErrorMessage(err, "Không thể lưu cài đặt vận chuyển lúc này."));
    } finally {
      setShipSettingsSaving(false);
    }
  };

  const shopDisplayName = shop?.name || "Gian hàng";
  const shopStatusMeta = shop ? getShopStatusLabel(shop.status) : null;
  const siteMeta = siteStateLabel(site.state);
  const safeLicenseUrl = sanitizeSafeUrl(shop?.business_license_doc);
  const pickupAddress = addresses.find(
    (a) => shop?.pickup_address_id && a.id === shop.pickup_address_id
  );

  const handleSaveOwnerContact = async (e: React.FormEvent) => {
    e.preventDefault();
    if (profileSaving) return;
    const trimmedName = fullName.trim();
    const trimmedPhone = phone.trim();
    if (trimmedName.length < 2) {
      setProfileError("Họ tên người phụ trách cần tối thiểu 2 ký tự.");
      return;
    }
    setProfileError("");
    setProfileSaving(true);
    try {
      const res = await api<{ user: SessionUser }>("/api/auth/me", {
        method: "PATCH",
        body: {
          full_name: trimmedName,
          phone: trimmedPhone,
          avatar_url: owner.avatarUrl,
        },
      });
      if (res.user) {
        setOwner(res.user);
        setFullName(res.user.fullName);
        setPhone(res.user.phone);
      }
      showToast("Đã cập nhật thông tin liên hệ chủ gian hàng.");
      router.refresh();
    } catch (err) {
      setProfileError(
        err instanceof ApiError ? err.message : "Không thể lưu thông tin liên hệ lúc này."
      );
    } finally {
      setProfileSaving(false);
    }
  };

  const handleSaveAutoReply = async (e: React.FormEvent) => {
    e.preventDefault();
    if (autoReplySaving) return;
    const trimmedMsg = autoReplyMessage.trim();
    if (autoReplyEnabled && trimmedMsg.length < 5) {
      setAutoReplyError("Khi bật trả lời tự động, lời chào cần tối thiểu 5 ký tự.");
      return;
    }
    if (trimmedMsg.length > 500) {
      setAutoReplyError("Nội dung trả lời tự động không vượt quá 500 ký tự.");
      return;
    }
    setAutoReplyError("");
    setAutoReplySaving(true);
    try {
      const res = await api<unknown>("/api/seller/chat/auto-reply", {
        method: "PUT",
        body: {
          enabled: autoReplyEnabled,
          message: trimmedMsg,
        },
      });
      const normalized = normalizeAutoReplyConfig(res);
      setAutoReplyEnabled(normalized.enabled);
      setAutoReplyMessage(normalized.message || trimmedMsg);
      setAutoReplyUpdatedAt(normalized.updated_at);
      setAutoReplyUnavailable(false);
      showToast("Đã lưu cấu hình trả lời tự động chat.");
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Không thể lưu cấu hình trả lời tự động. Dịch vụ chat có thể chưa sẵn sàng.";
      setAutoReplyError(msg);
      showToast(msg);
    } finally {
      setAutoReplySaving(false);
    }
  };

  const profileTabs: ReadonlyArray<{ key: ProfileTab; label: string; count?: number }> = [
    { key: "shop", label: "Gian hàng" },
    { key: "shipping", label: "Vận chuyển & Freeship" },
    { key: "warehouse", label: "Kho lấy hàng", count: addresses.length },
    { key: "autoreply", label: "Trả lời tự động" },
  ];

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Hồ sơ</h1>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/site">
            <span>Site bán hàng</span>
            <IconArrowUpRight size={14} />
          </Button>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải hồ sơ gian hàng..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải hồ sơ gian hàng"
          message={shopError ?? "Hệ thống gian hàng tạm thời chưa phản hồi."}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có dữ liệu hồ sơ gian hàng"
          message="Hoàn thiện thông tin gian hàng và kho lấy hàng để bắt đầu vận hành."
          actionLabel="Mở cấu hình site"
          actionHref="/seller/site"
        />
      )}

      {!uiState && (
        <>
          <SellerSlidingTabs
            tabs={profileTabs}
            value={tab}
            onChange={setTab}
            ariaLabel="Phân mục hồ sơ gian hàng"
          />

          {tab === "shop" && (
            <div className={styles.twoCol}>
              <section className={common.card} aria-labelledby="shop-info-heading">
                <div className={common.cardHeader}>
                  <h2 id="shop-info-heading" className={common.cardTitle}>
                    Hồ sơ gian hàng
                  </h2>
                  {shopStatusMeta && (
                    <span className={statusPillClass(shopStatusMeta.tone)}>
                      {shopStatusMeta.label}
                    </span>
                  )}
                </div>

                {shopError && !shop ? (
                  <StateView
                    kind="error"
                    surface="app"
                    title="Không thể tải thông tin gian hàng"
                    message={shopError}
                    actionLabel="Tải lại"
                    onAction={() => router.refresh()}
                  />
                ) : !shop ? (
                  <div className={common.inlineEmpty}>
                    Tài khoản hiện chưa gắn hồ sơ gian hàng trên hệ thống.
                  </div>
                ) : (
                  <>
                    {shop.rejection_reason && (
                      <div className={styles.alertBanner} role="alert">
                        <strong>Lý do từ chối:</strong> {shop.rejection_reason}
                      </div>
                    )}

                    <div className={styles.kvList}>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Tên gian hàng</span>
                        <span className={styles.kvVal}>{shop.name}</span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Trạng thái</span>
                        <span className={styles.kvVal}>
                          {shopStatusMeta ? shopStatusMeta.label : shop.status}
                        </span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Số điện thoại</span>
                        <span className={styles.kvVal}>
                          {shop.contact_phone || "Chưa cập nhật"}
                        </span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Mô tả</span>
                        <span className={styles.kvVal}>{shop.description || "Chưa cập nhật"}</span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Giấy phép kinh doanh</span>
                        <span className={styles.kvVal}>
                          {safeLicenseUrl ? (
                            <a
                              href={safeLicenseUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                              className={common.cellLink}
                            >
                              Xem chứng từ
                            </a>
                          ) : (
                            shop.business_license_doc || "Chưa đính kèm"
                          )}
                        </span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Ngày khởi tạo</span>
                        <span className={styles.kvVal}>
                          {shop.created_at ? formatDateTimeVi(shop.created_at) : "Chưa cập nhật"}
                        </span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Kho mặc định</span>
                        <span className={styles.kvVal}>
                          {pickupAddress
                            ? `${pickupAddress.street_address}, ${pickupAddress.ward_name}, ${pickupAddress.province_name}`
                            : shop.pickup_address_id || "Chưa thiết lập"}
                        </span>
                      </div>
                    </div>

                    <div className={styles.cardFootNote}>
                      <span>Chỉnh giao diện &amp; chính sách trên site riêng</span>
                      <Link href="/seller/site" className={common.cardLink}>
                        Mở Site Studio
                      </Link>
                    </div>
                  </>
                )}
              </section>

              <form
                className={common.card}
                onSubmit={(e) => void handleSaveOwnerContact(e)}
                aria-labelledby="owner-contact-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="owner-contact-heading" className={common.cardTitle}>
                    Chủ gian hàng
                  </h2>
                  <span className={statusPillClass(owner.phoneVerified ? "pos" : "neutral")}>
                    {owner.phoneVerified ? "Đã xác thực SĐT" : "Chưa xác thực SĐT"}
                  </span>
                </div>

                <div className={styles.fieldStack}>
                  <div className={common.field}>
                    <label htmlFor="owner-email" className={common.fieldLabel}>
                      Email đăng nhập
                    </label>
                    <input
                      id="owner-email"
                      type="email"
                      className={common.textInput}
                      value={owner.email}
                      disabled
                      readOnly
                    />
                  </div>

                  <div className={common.field}>
                    <label htmlFor="owner-fullname" className={common.fieldLabel}>
                      Họ và tên người phụ trách
                    </label>
                    <input
                      id="owner-fullname"
                      type="text"
                      className={common.textInput}
                      value={fullName}
                      onChange={(e) => setFullName(e.target.value)}
                      placeholder="Nhập họ tên chủ gian hàng"
                      maxLength={120}
                    />
                  </div>

                  <div className={common.field}>
                    <label htmlFor="owner-phone" className={common.fieldLabel}>
                      Số điện thoại tài khoản
                    </label>
                    <input
                      id="owner-phone"
                      type="tel"
                      className={common.textInput}
                      value={phone}
                      onChange={(e) => setPhone(e.target.value)}
                      placeholder="Ví dụ: 0901234567"
                      maxLength={20}
                    />
                  </div>

                  {profileError && (
                    <p className={common.fieldError} role="alert">
                      {profileError}
                    </p>
                  )}

                  <div className={styles.formActions}>
                    <Button
                      type="submit"
                      variant="ink"
                      size="sm"
                      shape="app"
                      disabled={profileSaving}
                    >
                      <IconCheck size={14} />
                      <span>{profileSaving ? "Đang lưu..." : "Lưu thông tin liên hệ"}</span>
                    </Button>
                  </div>
                </div>
              </form>
            </div>
          )}

          {tab === "shipping" && (
            <form
              className={`${common.card} ${styles.shippingCard}`}
              onSubmit={(e) => void handleSaveShippingSettings(e)}
              aria-labelledby="shipping-settings-heading"
              noValidate
            >
              <div className={common.cardHeader}>
                <div>
                  <h2 id="shipping-settings-heading" className={common.cardTitle}>
                    Cài đặt vận chuyển &amp; Freeship shop
                  </h2>
                  <span className={common.cardMeta}>
                    {shipSettingsUpdatedAt
                      ? `Cập nhật lúc ${formatDateTimeVi(shipSettingsUpdatedAt)}`
                      : "Đồng bộ với tính phí vận chuyển và tạo vận đơn"}
                  </span>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={shipSettingsLoading}
                  onClick={() => void loadShippingSettings()}
                >
                  <IconRefresh size={14} />
                  <span>{shipSettingsLoading ? "Đang tải..." : "Tải lại"}</span>
                </Button>
              </div>

              {shipSettingsLoading ? (
                <StateView
                  kind="loading"
                  surface="app"
                  title="Đang tải cài đặt vận chuyển..."
                  rows={3}
                />
              ) : (
                <div className={styles.fieldStack}>
                  <div>
                    <span className={common.fieldLabel}>
                      Hãng vận chuyển cho phép (bật ít nhất 1 hãng)
                    </span>
                    {carrierChoices.map((car) => {
                      const checked = enabledCarriers.includes(car.code);
                      return (
                        <div key={car.code} className={styles.switchRow}>
                          <div className={styles.switchText}>
                            <span className={styles.switchTitle}>{car.label}</span>
                            <span className={styles.switchDesc}>
                              Mã hãng: {car.code} · Báo giá khi cả sàn và shop đều bật
                            </span>
                          </div>
                          <input
                            type="checkbox"
                            className={common.toggleSwitch}
                            checked={checked}
                            onChange={(e) => toggleCarrierCode(car.code, e.target.checked)}
                            aria-label={`Bật hoặc tắt hãng ${car.label}`}
                            disabled={shipSettingsSaving}
                          />
                        </div>
                      );
                    })}
                  </div>

                  <div className={styles.fields2}>
                    <div className={common.field}>
                      <label htmlFor="ship-def-weight" className={common.fieldLabel}>
                        Khối lượng mặc định (gram)
                      </label>
                      <input
                        id="ship-def-weight"
                        type="number"
                        min={1}
                        max={50000}
                        step={1}
                        className={common.textInput}
                        value={defaultWeightStr}
                        onChange={(e) => setDefaultWeightStr(e.target.value)}
                        disabled={shipSettingsSaving}
                        required
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="ship-def-length" className={common.fieldLabel}>
                        Chiều dài kiện mặc định (cm)
                      </label>
                      <input
                        id="ship-def-length"
                        type="number"
                        min={1}
                        max={200}
                        step={1}
                        className={common.textInput}
                        value={defaultLengthStr}
                        onChange={(e) => setDefaultLengthStr(e.target.value)}
                        disabled={shipSettingsSaving}
                        required
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="ship-def-width" className={common.fieldLabel}>
                        Chiều rộng kiện mặc định (cm)
                      </label>
                      <input
                        id="ship-def-width"
                        type="number"
                        min={1}
                        max={200}
                        step={1}
                        className={common.textInput}
                        value={defaultWidthStr}
                        onChange={(e) => setDefaultWidthStr(e.target.value)}
                        disabled={shipSettingsSaving}
                        required
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="ship-def-height" className={common.fieldLabel}>
                        Chiều cao kiện mặc định (cm)
                      </label>
                      <input
                        id="ship-def-height"
                        type="number"
                        min={1}
                        max={200}
                        step={1}
                        className={common.textInput}
                        value={defaultHeightStr}
                        onChange={(e) => setDefaultHeightStr(e.target.value)}
                        disabled={shipSettingsSaving}
                        required
                      />
                    </div>
                  </div>

                  <div>
                    <div className={styles.switchRow}>
                      <div className={styles.switchText}>
                        <span className={styles.switchTitle}>
                          Hỗ trợ miễn phí vận chuyển (Freeship shop)
                        </span>
                        <span className={styles.switchDesc}>
                          Tự động trừ phí ship cho đơn đạt ngưỡng và tính vào chi phí khuyến mãi của
                          gian hàng.
                        </span>
                      </div>
                      <input
                        type="checkbox"
                        className={common.toggleSwitch}
                        checked={freeShipEnabled}
                        onChange={(e) => {
                          const next = e.target.checked;
                          setFreeShipEnabled(next);
                          if (next) {
                            if (
                              freeShipMinOrderStr.trim() === "" ||
                              Math.trunc(Number(freeShipMinOrderStr)) < 0
                            ) {
                              setFreeShipMinOrderStr("199000");
                            }
                            if (Math.trunc(Number(freeShipMaxStr) || 0) <= 0) {
                              setFreeShipMaxStr("25000");
                            }
                          }
                        }}
                        aria-label="Bật hoặc tắt Freeship gian hàng"
                        disabled={shipSettingsSaving}
                      />
                    </div>

                    {freeShipEnabled && (
                      <div className={styles.fields2} style={{ marginTop: 12 }}>
                        <div className={common.field}>
                          <label htmlFor="ship-fs-min" className={common.fieldLabel}>
                            Đơn tối thiểu áp dụng Freeship (VND)
                          </label>
                          <input
                            id="ship-fs-min"
                            type="number"
                            min={0}
                            step={1}
                            className={common.textInput}
                            value={freeShipMinOrderStr}
                            onChange={(e) => setFreeShipMinOrderStr(e.target.value)}
                            disabled={shipSettingsSaving}
                            required
                          />
                          <span className={common.fieldHint}>
                            {Math.max(0, Math.trunc(Number(freeShipMinOrderStr) || 0)) === 0
                              ? "Áp dụng cho mọi đơn hàng (0₫ = không yêu cầu giá trị tối thiểu)."
                              : `Áp dụng cho đơn từ ${formatVnd(Math.max(0, Math.trunc(Number(freeShipMinOrderStr) || 0)))}.`}
                          </span>
                        </div>
                        <div className={common.field}>
                          <label htmlFor="ship-fs-max" className={common.fieldLabel}>
                            Hỗ trợ phí ship tối đa / đơn (VND)
                          </label>
                          <input
                            id="ship-fs-max"
                            type="number"
                            min={1}
                            step={1}
                            className={common.textInput}
                            value={freeShipMaxStr}
                            onChange={(e) => setFreeShipMaxStr(e.target.value)}
                            disabled={shipSettingsSaving}
                            required
                          />
                          <span className={common.fieldHint}>
                            Bắt buộc lớn hơn 0₫ khi bật Freeship gian hàng.
                          </span>
                        </div>
                      </div>
                    )}
                  </div>

                  {shipSettingsError && (
                    <p className={common.fieldError} role="alert">
                      {shipSettingsError}
                    </p>
                  )}

                  <div className={styles.formActions}>
                    <Button
                      type="submit"
                      variant="ink"
                      size="sm"
                      shape="app"
                      disabled={shipSettingsSaving}
                    >
                      <IconCheck size={14} />
                      <span>{shipSettingsSaving ? "Đang lưu..." : "Lưu cài đặt vận chuyển"}</span>
                    </Button>
                  </div>
                </div>
              )}
            </form>
          )}

          {tab === "warehouse" && (
            <div className={styles.twoCol}>
              <section className={common.card} aria-labelledby="warehouse-heading">
                <div className={common.cardHeader}>
                  <h2 id="warehouse-heading" className={common.cardTitle}>
                    Kho lấy hàng ({formatInt(addresses.length)})
                  </h2>
                </div>

                {addressesError ? (
                  <StateView
                    kind="error"
                    surface="app"
                    title="Không thể tải danh sách địa chỉ"
                    message={addressesError}
                    actionLabel="Tải lại"
                    onAction={() => router.refresh()}
                  />
                ) : addresses.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Chưa có địa chỉ kho lấy hàng nào được lưu.
                  </div>
                ) : (
                  <div className={styles.addressList}>
                    {addresses.map((addr) => {
                      const isPickup = Boolean(
                        shop?.pickup_address_id && addr.id === shop.pickup_address_id
                      );
                      const fullLine = [addr.street_address, addr.ward_name, addr.province_name]
                        .filter(Boolean)
                        .join(", ");
                      return (
                        <article
                          key={addr.id}
                          className={`${styles.addressItem} ${isPickup ? styles.addressItemPickup : ""}`}
                        >
                          <div className={styles.addressHead}>
                            <div className={styles.addressIdentity}>
                              <strong className={styles.addressName}>{addr.recipient_name}</strong>
                              <span className={styles.addressPhone}>{addr.recipient_phone}</span>
                            </div>
                            <div className={styles.badgeRow}>
                              {isPickup && (
                                <span className={`${common.pill} ${common.pillPos}`}>
                                  Kho mặc định
                                </span>
                              )}
                              {addr.is_default && (
                                <span className={common.pill}>Mặc định tài khoản</span>
                              )}
                            </div>
                          </div>
                          <p className={styles.addressLine}>{fullLine}</p>
                        </article>
                      );
                    })}
                  </div>
                )}
              </section>

              <section className={common.card} aria-labelledby="site-policy-heading">
                <div className={common.cardHeader}>
                  <h2 id="site-policy-heading" className={common.cardTitle}>
                    Site bán hàng
                  </h2>
                  <span className={statusPillClass(siteMeta.tone)}>{siteMeta.label}</span>
                </div>

                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Tên miền</span>
                    <span className={styles.kvVal}>{site.host || "Chưa thiết lập"}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Trạng thái</span>
                    <span className={styles.kvVal}>{siteMeta.label}</span>
                  </div>
                  {site.suspendedReason && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Lý do tạm khóa</span>
                      <span className={styles.kvVal}>{site.suspendedReason}</span>
                    </div>
                  )}
                </div>

                <div className={styles.inlineActions}>
                  <Button variant="ink" size="sm" shape="app" href="/seller/site">
                    <span>Mở Site Studio</span>
                    <IconArrowUpRight size={14} />
                  </Button>
                  {site.url && (
                    <a
                      href={site.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className={common.cardLink}
                    >
                      Xem site đang chạy
                    </a>
                  )}
                </div>
              </section>
            </div>
          )}

          {tab === "autoreply" && (
            <div className={styles.twoCol}>
              <form
                className={common.card}
                onSubmit={(e) => void handleSaveAutoReply(e)}
                aria-labelledby="autoreply-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="autoreply-heading" className={common.cardTitle}>
                    Trả lời tự động khi ngoại tuyến
                  </h2>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={autoReplyLoading}
                    onClick={() => void loadAutoReply()}
                  >
                    <IconRefresh size={14} />
                    <span>{autoReplyLoading ? "Đang tải..." : "Tải lại"}</span>
                  </Button>
                </div>

                {autoReplyUnavailable && (
                  <div className={styles.noticeWell} role="status">
                    Dịch vụ chat hiện chưa phản hồi. Bạn vẫn có thể soạn sẵn lời chào và thử lưu lại
                    sau.
                  </div>
                )}

                <div className={styles.fieldStack}>
                  <div className={styles.switchRow}>
                    <div className={styles.switchText}>
                      <span className={styles.switchTitle}>Bật trả lời tự động</span>
                      <span className={styles.switchDesc}>
                        Gửi lời chào ngay khi khách mở hội thoại ngoài giờ trực.
                      </span>
                    </div>
                    <input
                      type="checkbox"
                      className={common.toggleSwitch}
                      checked={autoReplyEnabled}
                      onChange={(e) => setAutoReplyEnabled(e.target.checked)}
                      aria-label="Bật hoặc tắt trả lời tự động khi ngoại tuyến"
                    />
                  </div>

                  <div className={common.field}>
                    <label htmlFor="auto-reply-msg" className={common.fieldLabel}>
                      Nội dung lời chào ({formatInt(autoReplyMessage.length)} / 500)
                    </label>
                    <textarea
                      id="auto-reply-msg"
                      className={common.textArea}
                      value={autoReplyMessage}
                      onChange={(e) => {
                        setAutoReplyMessage(e.target.value);
                        if (autoReplyError) setAutoReplyError("");
                      }}
                      maxLength={500}
                      placeholder="Nhập lời chào khi ngoại tuyến và giờ làm việc..."
                    />
                  </div>

                  <div className={styles.presetRow}>
                    {AUTO_REPLY_PRESETS.map((preset) => (
                      <button
                        key={preset.label}
                        type="button"
                        className={styles.presetBtn}
                        onClick={() => {
                          setAutoReplyMessage(preset.text);
                          setAutoReplyError("");
                        }}
                      >
                        Dùng {preset.label.toLowerCase()}
                      </button>
                    ))}
                  </div>

                  {autoReplyError && (
                    <p className={common.fieldError} role="alert">
                      {autoReplyError}
                    </p>
                  )}

                  <div className={styles.formActions}>
                    {autoReplyUpdatedAt && (
                      <span className={common.cardMeta}>
                        Cập nhật {formatDateTimeVi(autoReplyUpdatedAt)}
                      </span>
                    )}
                    <Button
                      type="submit"
                      variant="ink"
                      size="sm"
                      shape="app"
                      disabled={autoReplySaving || autoReplyLoading}
                    >
                      <IconCheck size={14} />
                      <span>{autoReplySaving ? "Đang lưu..." : "Lưu cấu hình"}</span>
                    </Button>
                  </div>
                </div>
              </form>

              <section className={common.card} aria-labelledby="autoreply-preview-heading">
                <div className={common.cardHeader}>
                  <h2 id="autoreply-preview-heading" className={common.cardTitle}>
                    Xem trước hộp chat
                  </h2>
                  <span className={statusPillClass(autoReplyEnabled ? "pos" : "neutral")}>
                    {autoReplyEnabled ? "Đang bật" : "Đang tắt"}
                  </span>
                </div>

                <div className={styles.previewWell}>
                  <span className={styles.previewHeading}>{shopDisplayName}</span>
                  <p className={styles.previewBubble}>
                    {autoReplyMessage.trim() ||
                      "Nhập lời chào bên trái để xem trước tin nhắn tự động."}
                  </p>
                </div>

                <div className={styles.cardFootNote}>
                  <span>Hội thoại trực tiếp với khách hàng</span>
                  <Link href="/seller/chat" className={common.cardLink}>
                    Mở Tin nhắn
                  </Link>
                </div>
              </section>
            </div>
          )}
        </>
      )}
    </div>
  );
}

export default function SellerProfileClient(props: SellerProfileClientProps) {
  return (
    <SellerSuspense>
      <SellerProfileContent {...props} />
    </SellerSuspense>
  );
}
