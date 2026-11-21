"use client";

import React, { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconArrowUpRight, IconCheck, IconRefresh, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type { SessionUser } from "@/lib/server/session";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
import { getShopStatusLabel, type GwSellerShop } from "@/features/seller/sellerApiTypes";
import {
  normalizeAutoReplyConfig,
  parseUiStateParam,
  type SellerAddress,
  type SellerAutoReplyConfig,
} from "@/features/seller/sellerReturns";
import type { SellerSiteSummary } from "@/features/seller/site/siteSummary";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type ProfileTab = "shop" | "warehouse" | "autoreply";

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
  site: SellerSiteSummary;
}

function pillCls(tone: "pos" | "warn" | "neg" | "neutral"): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
}

function siteStateLabel(state: SellerSiteSummary["state"]): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
} {
  switch (state) {
    case "published":
      return { label: "Đang mở bán", tone: "pos" };
    case "draft":
      return { label: "Bản nháp chưa xuất bản", tone: "warn" };
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
  site,
}: SellerProfileClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiStateParam(searchParams.get("state"));
  const tabParam = searchParams.get("tab") as ProfileTab | null;
  const { showToast } = useSellerContext();

  const [tab, setTab] = useState<ProfileTab>(
    tabParam && ["shop", "warehouse", "autoreply"].includes(tabParam) ? tabParam : "shop"
  );

  // Tab 1: Editable owner contact info via PATCH /api/auth/me
  const [owner, setOwner] = useState<SessionUser>(initialUser);
  const [fullName, setFullName] = useState(initialUser.fullName);
  const [phone, setPhone] = useState(initialUser.phone);
  const [profileSaving, setProfileSaving] = useState(false);
  const [profileError, setProfileError] = useState("");

  // Tab 3: Chat auto-reply via GET/PUT /api/seller/chat/auto-reply
  const [autoReplyLoaded, setAutoReplyLoaded] = useState(false);
  const [autoReplyLoading, setAutoReplyLoading] = useState(false);
  const [autoReplyUnavailable, setAutoReplyUnavailable] = useState(false);
  const [autoReplyEnabled, setAutoReplyEnabled] = useState(false);
  const [autoReplyMessage, setAutoReplyMessage] = useState("");
  const [autoReplyUpdatedAt, setAutoReplyUpdatedAt] = useState("");
  const [autoReplySaving, setAutoReplySaving] = useState(false);
  const [autoReplyError, setAutoReplyError] = useState("");

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

  const shopDisplayName = shop?.name || "Gian hàng chưa kích hoạt";
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
      setAutoReplyError("Khi bật trả lời tự động, nội dung lời chào cần tối thiểu 5 ký tự.");
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

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Hồ sơ gian hàng &amp; Vận hành</h1>
          <p className={common.finding}>
            {shopDisplayName}
            {shopStatusMeta ? ` · ${shopStatusMeta.label}` : ""}
            {` · ${formatInt(addresses.length)} địa chỉ kho đã lưu`}
            {` · Site riêng: ${siteMeta.label}`}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/site">
            <span>Tùy chỉnh site gian hàng</span>
            <IconArrowUpRight size={14} />
          </Button>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải hồ sơ gian hàng và danh sách kho lấy hàng..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải hồ sơ gian hàng"
          message={shopError ?? "Hệ thống gian hàng tạm thời chưa phản hồi. Vui lòng thử lại."}
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
          <div
            className={common.underlineTabs}
            role="tablist"
            aria-label="Phân mục hồ sơ gian hàng"
          >
            <button
              type="button"
              role="tab"
              aria-selected={tab === "shop"}
              className={`${common.underlineTab} ${tab === "shop" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("shop")}
            >
              Thông tin gian hàng
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "warehouse"}
              className={`${common.underlineTab} ${tab === "warehouse" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("warehouse")}
            >
              Kho lấy hàng &amp; Địa chỉ ({formatInt(addresses.length)})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === "autoreply"}
              className={`${common.underlineTab} ${tab === "autoreply" ? common.underlineTabActive : ""}`}
              onClick={() => setTab("autoreply")}
            >
              Trả lời tự động chat
            </button>
          </div>

          {tab === "shop" && (
            <div className={styles.twoCol}>
              <section className={common.card} aria-labelledby="shop-info-heading">
                <div className={common.cardHeader}>
                  <h2 id="shop-info-heading" className={common.cardTitle}>
                    Hồ sơ đăng ký gian hàng
                  </h2>
                  {shopStatusMeta && (
                    <span className={pillCls(shopStatusMeta.tone)}>{shopStatusMeta.label}</span>
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
                        <strong>Lý do từ chối phê duyệt gian hàng:</strong> {shop.rejection_reason}
                      </div>
                    )}

                    <div className={styles.kvList}>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Tên gian hàng</span>
                        <span className={styles.kvVal}>{shop.name}</span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Trạng thái vận hành</span>
                        <span className={styles.kvVal}>
                          {shopStatusMeta ? shopStatusMeta.label : shop.status}
                        </span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Số điện thoại liên hệ</span>
                        <span className={styles.kvVal}>
                          {shop.contact_phone || "Chưa cập nhật"}
                        </span>
                      </div>
                      <div className={styles.kvRow}>
                        <span className={styles.kvKey}>Mô tả gian hàng</span>
                        <span className={styles.kvVal}>
                          {shop.description || "Chưa có mô tả giới thiệu"}
                        </span>
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
                              Xem chứng từ đã nộp
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
                        <span className={styles.kvKey}>Kho lấy hàng mặc định</span>
                        <span className={styles.kvVal}>
                          {pickupAddress
                            ? `${pickupAddress.street_address}, ${pickupAddress.ward_name}, ${pickupAddress.province_name}`
                            : shop.pickup_address_id || "Chưa thiết lập"}
                        </span>
                      </div>
                    </div>

                    <div className={styles.cardFootNote}>
                      <span>
                        Để chỉnh sửa giao diện, giới thiệu thương hiệu và chính sách đổi trả trên
                        site riêng của shop:
                      </span>
                      <Link href="/seller/site" className={common.cardLink}>
                        Mở trình chỉnh sửa site
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
                    Tài khoản chủ gian hàng
                  </h2>
                  <span className={pillCls(owner.phoneVerified ? "pos" : "neutral")}>
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
                    <span className={common.fieldHint}>
                      Email định danh tài khoản quản trị gian hàng, không thể thay đổi trực tiếp.
                    </span>
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

          {tab === "warehouse" && (
            <div className={styles.twoCol}>
              <section className={common.card} aria-labelledby="warehouse-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="warehouse-heading" className={common.cardTitle}>
                      Danh sách kho lấy hàng &amp; địa chỉ
                    </h2>
                    <span className={common.cardMeta}>
                      Đồng bộ trực tiếp từ sổ địa chỉ tài khoản chủ gian hàng
                    </span>
                  </div>
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
                    Chưa có địa chỉ kho lấy hàng nào được lưu trong tài khoản của bạn.
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
                                  Kho lấy hàng mặc định
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
                    Site bán hàng &amp; Chính sách công khai
                  </h2>
                  <span className={pillCls(siteMeta.tone)}>{siteMeta.label}</span>
                </div>

                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Tên miền gian hàng</span>
                    <span className={styles.kvVal}>{site.host || "Chưa thiết lập tên miền"}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Trạng thái xuất bản</span>
                    <span className={styles.kvVal}>{siteMeta.label}</span>
                  </div>
                  {site.suspendedReason && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Lý do tạm khóa site</span>
                      <span className={styles.kvVal}>{site.suspendedReason}</span>
                    </div>
                  )}
                </div>

                <div className={styles.previewWell}>
                  <span className={styles.previewHeading}>Quản trị giao diện và chính sách</span>
                  <p className={styles.previewLine}>
                    Chính sách đổi trả, giới thiệu xưởng, bộ sưu tập và tên miền tùy chỉnh được quản
                    lý tập trung tại mục Site bán hàng để đồng bộ tức thì tới người mua.
                  </p>
                  <div className={styles.inlineActions}>
                    <Button variant="ink" size="sm" shape="app" href="/seller/site">
                      <span>Mở cấu hình site</span>
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
                  <div>
                    <h2 id="autoreply-heading" className={common.cardTitle}>
                      Trả lời tự động khi ngoại tuyến
                    </h2>
                    <span className={common.cardMeta}>
                      Tự động gửi lời chào tới người mua khi gian hàng chưa trực tuyến trên hộp chat
                    </span>
                  </div>
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
                    khi hộp chat hoạt động.
                  </div>
                )}

                <div className={styles.fieldStack}>
                  <div className={styles.switchRow}>
                    <div className={styles.switchText}>
                      <span className={styles.switchTitle}>Bật trả lời tự động</span>
                      <span className={styles.switchDesc}>
                        Gửi tin nhắn chào mừng ngay khi khách mở hội thoại mới ngoài giờ trực.
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
                      Nội dung tin nhắn tự động ({formatInt(autoReplyMessage.length)} / 500 ký tự)
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
                      placeholder="Nhập lời chào khi gian hàng ngoại tuyến, giờ làm việc và thời gian phản hồi dự kiến..."
                    />
                    <span className={common.fieldHint}>
                      Viết ngắn gọn, rõ khung giờ hỗ trợ để người mua yên tâm chờ phản hồi.
                    </span>
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
                        Cập nhật lúc {formatDateTimeVi(autoReplyUpdatedAt)}
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
                      <span>{autoReplySaving ? "Đang lưu..." : "Lưu cấu hình trả lời"}</span>
                    </Button>
                  </div>
                </div>
              </form>

              <section className={common.card} aria-labelledby="autoreply-preview-heading">
                <div className={common.cardHeader}>
                  <h2 id="autoreply-preview-heading" className={common.cardTitle}>
                    Xem trước hiển thị trên hộp chat
                  </h2>
                  <span className={pillCls(autoReplyEnabled ? "pos" : "neutral")}>
                    {autoReplyEnabled ? "Đang bật" : "Đang tắt"}
                  </span>
                </div>

                <div className={styles.previewWell}>
                  <span className={styles.previewHeading}>{shopDisplayName}</span>
                  <p className={styles.previewBubble}>
                    {autoReplyMessage.trim() ||
                      "Chưa có nội dung lời chào. Nhập nội dung bên trái để xem trước tin nhắn tự động."}
                  </p>
                  <span className={styles.previewLine}>
                    {autoReplyEnabled
                      ? "Khách sẽ nhận tin nhắn này tự động khi nhắn tin ngoài giờ trực."
                      : "Trả lời tự động đang tắt. Tin nhắn khách gửi sẽ chờ nhân viên phản hồi trực tiếp trong mục Tin nhắn."}
                  </span>
                </div>

                <div className={styles.cardFootNote}>
                  <span>Quản lý hội thoại trực tiếp với người mua tại hộp thư gian hàng:</span>
                  <Link href="/seller/chat" className={common.cardLink}>
                    Mở hộp Tin nhắn
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
