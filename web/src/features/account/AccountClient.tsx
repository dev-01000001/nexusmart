"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCTS } from "@/data";
import type {
  GatewayAddress,
  GatewayCoinBalance,
  GatewayNotification,
  GatewayNotificationPreferences,
  GatewayNotificationsResponse,
  GatewayOrder,
  GatewayPublicShop,
  GatewayRequestPhoneOTPResponse,
  GatewayShop,
  GatewayUser,
  GatewayVerifyPhoneOTPResponse,
  GatewayVoucher,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import {
  formatDateTimeVi,
  formatDateVi,
  formatInt,
  formatSignedInt,
  formatVnd,
  formatVoucherBenefit,
} from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPackage, ProductCard, StateView } from "@/components";
import { AddressForm } from "@/features/commerce/AddressForm";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/tai-khoan/page.module.css";
import {
  AccountMockBodyProfileTab,
  AccountMockGiftCardsPanel,
  AccountMockOverviewPanels,
  AccountMockReferralTab,
  AccountMockWishlistTab,
} from "./AccountMockTabs";
import {
  type BadgeTone,
  canSubmitShopApplication,
  getNotificationCategoryLabel,
  getOrderStatusInfo,
  getShopStatusInfo,
  getVoucherScopeLabel,
  isShopInfoRequested,
  isValidVietnamesePhone,
  normalizeNotificationHref,
  normalizeVietnamesePhone,
} from "./orderMeta";
import type { OrderVisualLookup } from "./types";

export type AccountTabKey =
  | "tong-quan"
  | "dia-chi"
  | "gian-hang"
  | "thong-bao"
  | "voucher"
  | "so-do"
  | "yeu-thich"
  | "gioi-thieu";

const ACCOUNT_TABS: Array<{ key: AccountTabKey; label: string }> = [
  { key: "tong-quan", label: "Tổng quan & Hồ sơ" },
  { key: "dia-chi", label: "Địa chỉ nhận hàng" },
  { key: "gian-hang", label: "Mở gian hàng" },
  { key: "thong-bao", label: "Thông báo" },
  { key: "voucher", label: "Ví voucher" },
  { key: "so-do", label: "Hồ sơ số đo" },
  { key: "yeu-thich", label: "Yêu thích" },
  { key: "gioi-thieu", label: "Giới thiệu" },
];

type NotificationFilterKey = "ALL" | "UNREAD" | "ORDER" | "PROMOTION" | "SYSTEM";

const NOTIFICATION_FILTERS: Array<{ key: NotificationFilterKey; label: string }> = [
  { key: "ALL", label: "Tất cả" },
  { key: "UNREAD", label: "Chưa đọc" },
  { key: "ORDER", label: "Đơn hàng" },
  { key: "PROMOTION", label: "Khuyến mãi" },
  { key: "SYSTEM", label: "Hệ thống" },
];

const DEFAULT_NOTIFICATION_PREFS: GatewayNotificationPreferences = {
  user_id: "",
  order_in_app: true,
  order_email: true,
  order_push: true,
  promotion_in_app: true,
  promotion_email: false,
  promotion_push: false,
  system_in_app: true,
  system_email: true,
  system_push: false,
};

export interface AccountClientProps {
  initialUser: GatewayUser;
  initialAddresses: GatewayAddress[];
  addressesError?: string | null;
  initialCoinBalance: GatewayCoinBalance | null;
  coinsError?: string | null;
  initialOrders: GatewayOrder[];
  initialOrdersTotal: number;
  ordersError?: string | null;
  initialVouchers: GatewayVoucher[];
  vouchersError?: string | null;
  initialGatewayShop: GatewayShop | null;
  shopError?: string | null;
  initialNotifications: GatewayNotificationsResponse | null;
  notificationsError?: string | null;
  initialNotificationPrefs: GatewayNotificationPreferences | null;
  shopsMap: Record<string, GatewayPublicShop>;
  visuals: OrderVisualLookup;
  initialTab?: AccountTabKey;
  forcedState?: "loading" | "empty" | "error" | null;
}

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function roleLabel(role: string): string {
  switch (role) {
    case "seller":
      return "Người bán";
    case "super_admin":
      return "Siêu quản trị";
    case "admin":
      return "Quản trị viên";
    case "moderator":
      return "Kiểm duyệt viên";
    case "cskh":
      return "Chăm sóc khách hàng";
    case "accountant":
      return "Kế toán";
    case "ops":
      return "Vận hành";
    default:
      return "Người mua";
  }
}

export function AccountClient({
  initialUser,
  initialAddresses,
  addressesError,
  initialCoinBalance,
  coinsError,
  initialOrders,
  initialOrdersTotal,
  ordersError,
  initialVouchers,
  vouchersError,
  initialGatewayShop,
  shopError,
  initialNotifications,
  notificationsError,
  initialNotificationPrefs,
  shopsMap,
  visuals,
  initialTab = "tong-quan",
  forcedState,
}: AccountClientProps) {
  const router = useRouter();
  const { logout, setUser: setSessionUser } = useSession();
  const { wishlist, showToast } = useMockStore();

  const [activeTab, setActiveTab] = useState<AccountTabKey>(initialTab);

  // Real profile state
  const [profileUser, setProfileUser] = useState<GatewayUser>(initialUser);
  const [editingProfile, setEditingProfile] = useState(false);
  const [profileName, setProfileName] = useState(initialUser.full_name);
  const [profilePhone, setProfilePhone] = useState(initialUser.phone);
  const [profileAvatarUrl, setProfileAvatarUrl] = useState(initialUser.avatar_url ?? "");
  const [savingProfile, setSavingProfile] = useState(false);
  const [profileError, setProfileError] = useState<string | null>(null);

  // Real phone OTP verification state (E19)
  const [phoneOtpOpen, setPhoneOtpOpen] = useState(false);
  const [otpPhoneInput, setOtpPhoneInput] = useState(initialUser.phone ?? "");
  const [otpCode, setOtpCode] = useState("");
  const [otpSending, setOtpSending] = useState(false);
  const [otpVerifying, setOtpVerifying] = useState(false);
  const [otpSentMasked, setOtpSentMasked] = useState<string | null>(null);
  const [otpCooldownSec, setOtpCooldownSec] = useState(0);
  const [otpExpiresSec, setOtpExpiresSec] = useState(0);
  const [otpError, setOtpError] = useState<string | null>(null);

  useEffect(() => {
    if (otpCooldownSec <= 0) return;
    const timer = window.setInterval(() => {
      setOtpCooldownSec((prev) => (prev <= 1 ? 0 : prev - 1));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [otpCooldownSec]);

  useEffect(() => {
    if (otpExpiresSec <= 0) return;
    const timer = window.setInterval(() => {
      setOtpExpiresSec((prev) => (prev <= 1 ? 0 : prev - 1));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [otpExpiresSec]);

  useEffect(() => {
    if (
      typeof window !== "undefined" &&
      window.location.hash === "#xac-minh-sdt" &&
      !profileUser.phone_verified
    ) {
      setActiveTab("tong-quan");
      setPhoneOtpOpen(true);
    }
  }, [profileUser.phone_verified]);

  // Real addresses state
  const [addresses, setAddresses] = useState<GatewayAddress[]>(initialAddresses);
  const [addrFetchError, setAddrFetchError] = useState<string | null>(addressesError ?? null);
  const [showAddAddress, setShowAddAddress] = useState(false);
  const [editingAddress, setEditingAddress] = useState<GatewayAddress | null>(null);
  const [addrBusyId, setAddrBusyId] = useState<string | null>(null);
  const [addrActionError, setAddrActionError] = useState<string | null>(null);

  // Real coins state
  const [coinBalance, setCoinBalance] = useState<GatewayCoinBalance | null>(initialCoinBalance);

  // Real vouchers state
  const [vouchers, setVouchers] = useState<GatewayVoucher[]>(initialVouchers);
  const [voucherFilter, setVoucherFilter] = useState<"all" | "saved">("all");
  const [savingVoucherId, setSavingVoucherId] = useState<string | null>(null);
  const [voucherActionError, setVoucherActionError] = useState<string | null>(null);

  // Real shop state (pre-fill when resubmitting after REJECTED or REQUEST_INFO)
  const resubmittableInitialShop =
    initialGatewayShop && canSubmitShopApplication(initialGatewayShop) ? initialGatewayShop : null;
  const defaultPickupAddrId =
    initialAddresses.find((a) => a.is_default)?.id ?? initialAddresses[0]?.id ?? "";
  const initialShopPickupAddrId =
    resubmittableInitialShop?.pickup_address_id &&
    initialAddresses.some((a) => a.id === resubmittableInitialShop.pickup_address_id)
      ? resubmittableInitialShop.pickup_address_id
      : defaultPickupAddrId;

  const [myShop, setGatewayShop] = useState<GatewayShop | null>(initialGatewayShop);
  const [shopName, setShopName] = useState(resubmittableInitialShop?.name ?? "");
  const [shopSlug, setShopSlug] = useState(resubmittableInitialShop?.slug ?? "");
  const [shopDesc, setShopDesc] = useState(resubmittableInitialShop?.description ?? "");
  const [shopPhone, setShopPhone] = useState(
    resubmittableInitialShop?.contact_phone || initialUser.phone || ""
  );
  const [shopPickupAddrId, setShopPickupAddrId] = useState(initialShopPickupAddrId);
  const [shopLicenseDoc, setShopLicenseDoc] = useState(
    resubmittableInitialShop?.business_license_doc ?? ""
  );
  const [submittingShop, setSubmittingShop] = useState(false);
  const [shopFormError, setShopFormError] = useState<string | null>(null);

  // Real notifications & preferences state
  const [notifications, setNotifications] = useState<GatewayNotification[]>(
    initialNotifications?.items ?? []
  );
  const [unreadCount, setUnreadCount] = useState<number>(initialNotifications?.unread_count ?? 0);
  const [notifFilter, setNotifFilter] = useState<NotificationFilterKey>("ALL");
  const [notifLoading, setNotifLoading] = useState(false);
  const [notifError, setNotifError] = useState<string | null>(notificationsError ?? null);
  const [markingNotif, setMarkingNotif] = useState(false);
  const [notifPrefs, setNotifPrefs] = useState<GatewayNotificationPreferences>(
    initialNotificationPrefs ?? { ...DEFAULT_NOTIFICATION_PREFS, user_id: initialUser.id }
  );
  const [savingPrefs, setSavingPrefs] = useState(false);
  const [prefsError, setPrefsError] = useState<string | null>(null);

  useRevealOnScroll(
    `${forcedState ?? "live"}-${activeTab}-${addresses.length}-${notifications.length}-${myShop?.status ?? "none"}`
  );

  const suggestedProducts = useMemo(() => PRODUCTS.slice(4, 8), []);

  const inProgressOrders = useMemo(
    () =>
      initialOrders.filter(
        (o) =>
          o.status === "PENDING" ||
          o.status === "CONFIRMED" ||
          o.status === "PREPARING" ||
          o.status === "SHIPPING"
      ).length,
    [initialOrders]
  );

  const savedVouchersCount = useMemo(() => vouchers.filter((v) => v.saved).length, [vouchers]);
  const displayedVouchers = useMemo(
    () => (voucherFilter === "saved" ? vouchers.filter((v) => v.saved) : vouchers),
    [vouchers, voucherFilter]
  );

  const refreshAddresses = async () => {
    try {
      const res = await api<{ addresses: GatewayAddress[] }>("/users/me/addresses");
      const list = Array.isArray(res?.addresses) ? res.addresses : [];
      setAddresses(list);
      setAddrFetchError(null);
      if (list.length === 0) {
        setShopPickupAddrId("");
      } else if (!list.some((a) => a.id === shopPickupAddrId)) {
        setShopPickupAddrId(list.find((a) => a.is_default)?.id ?? list[0].id);
      }
    } catch (err) {
      setAddrFetchError(getErrorMessage(err, "Không thể tải lại danh sách địa chỉ."));
    }
  };

  const handleSaveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (savingProfile) return;
    const cleanName = profileName.trim();
    const cleanPhone = normalizeVietnamesePhone(profilePhone);
    const cleanAvatar = profileAvatarUrl.trim();

    if (cleanName.length < 2) {
      setProfileError("Vui lòng nhập họ và tên đầy đủ (tối thiểu 2 ký tự).");
      return;
    }
    if (cleanPhone && !isValidVietnamesePhone(cleanPhone)) {
      setProfileError(
        "Vui lòng nhập số điện thoại Việt Nam hợp lệ (ví dụ: 0912345678 hoặc +84912345678)."
      );
      return;
    }

    setProfileError(null);
    setSavingProfile(true);
    try {
      const res = await api<{ user: GatewayUser }>("/users/me", {
        method: "PATCH",
        json: {
          full_name: cleanName,
          phone: cleanPhone,
          avatar_url: cleanAvatar,
        },
      });
      if (res?.user) {
        setProfileUser(res.user);
        setSessionUser(res.user);
        setProfileName(res.user.full_name);
        setProfilePhone(res.user.phone);
        setProfileAvatarUrl(res.user.avatar_url ?? "");
        setOtpPhoneInput(res.user.phone ?? "");
        setOtpSentMasked(null);
        setOtpCode("");
        setOtpExpiresSec(0);
        setOtpError(null);
      }
      setEditingProfile(false);
      showToast("Đã cập nhật thông tin hồ sơ cá nhân.");
    } catch (err) {
      if (err instanceof ApiError && err.code === "ALREADY_EXISTS") {
        setProfileError("Số điện thoại này đã được sử dụng cho tài khoản khác.");
      } else {
        setProfileError(getErrorMessage(err, "Chưa thể cập nhật hồ sơ lúc này."));
      }
    } finally {
      setSavingProfile(false);
    }
  };

  const handleRequestPhoneOtp = async () => {
    if (otpSending || otpCooldownSec > 0) return;
    const cleanPhone = normalizeVietnamesePhone(otpPhoneInput || profileUser.phone || "");
    if (!isValidVietnamesePhone(cleanPhone)) {
      setOtpError("Vui lòng nhập số điện thoại Việt Nam hợp lệ (ví dụ: 0912345678).");
      return;
    }
    setOtpError(null);
    setOtpSending(true);
    try {
      const res = await api<GatewayRequestPhoneOTPResponse>("users/me/phone/otp", {
        method: "POST",
        json: { phone: cleanPhone },
      });
      setOtpPhoneInput(cleanPhone);
      setOtpSentMasked(res?.phone_masked || cleanPhone);
      setOtpCooldownSec(Math.max(1, Math.trunc(res?.retry_after_seconds ?? 60)));
      setOtpExpiresSec(Math.max(1, Math.trunc(res?.expires_in_seconds ?? 300)));
      showToast(`Đã gửi mã OTP 6 chữ số tới ${res?.phone_masked || cleanPhone}.`);
    } catch (err) {
      setOtpError(getErrorMessage(err, "Chưa thể gửi mã OTP lúc này. Vui lòng thử lại sau."));
    } finally {
      setOtpSending(false);
    }
  };

  const handleVerifyPhoneOtp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (otpVerifying) return;
    const cleanPhone = normalizeVietnamesePhone(otpPhoneInput || profileUser.phone || "");
    const cleanOtp = otpCode.replace(/\s+/g, "").trim();
    if (!isValidVietnamesePhone(cleanPhone)) {
      setOtpError("Số điện thoại không hợp lệ.");
      return;
    }
    if (!/^\d{6}$/.test(cleanOtp)) {
      setOtpError("Vui lòng nhập đúng mã OTP gồm 6 chữ số.");
      return;
    }
    setOtpError(null);
    setOtpVerifying(true);
    try {
      const res = await api<GatewayVerifyPhoneOTPResponse>("users/me/phone/verify", {
        method: "POST",
        json: { phone: cleanPhone, otp: cleanOtp },
      });
      if (res?.user) {
        setProfileUser(res.user);
        setSessionUser(res.user);
        setProfilePhone(res.user.phone);
      } else {
        const updated = { ...profileUser, phone: cleanPhone, phone_verified: true };
        setProfileUser(updated);
        setSessionUser(updated);
      }
      setOtpCode("");
      setOtpSentMasked(null);
      setOtpExpiresSec(0);
      setPhoneOtpOpen(false);
      showToast("Đã xác minh số điện thoại thành công. Bạn đã có thể sử dụng thanh toán COD.");
    } catch (err) {
      setOtpError(getErrorMessage(err, "Mã OTP chưa đúng hoặc đã hết hạn. Vui lòng kiểm tra lại."));
    } finally {
      setOtpVerifying(false);
    }
  };

  const handleSetDefaultAddress = async (addr: GatewayAddress) => {
    if (addrBusyId) return;
    setAddrActionError(null);
    setAddrBusyId(addr.id);
    try {
      await api<{ address: GatewayAddress }>(`/users/me/addresses/${encodeURIComponent(addr.id)}`, {
        method: "PUT",
        json: {
          recipient_name: addr.recipient_name,
          recipient_phone: addr.recipient_phone,
          province_code: addr.province_code,
          province_name: addr.province_name,
          ward_code: addr.ward_code,
          ward_name: addr.ward_name,
          street_address: addr.street_address,
          is_default: true,
        },
      });
      await refreshAddresses();
      showToast("Đã đặt làm địa chỉ nhận hàng mặc định.");
    } catch (err) {
      setAddrActionError(getErrorMessage(err, "Chưa thể cập nhật địa chỉ mặc định."));
    } finally {
      setAddrBusyId(null);
    }
  };

  const handleDeleteAddress = async (addr: GatewayAddress) => {
    if (addrBusyId) return;
    setAddrActionError(null);
    setAddrBusyId(addr.id);
    try {
      await api<{ ok: boolean }>(`/users/me/addresses/${encodeURIComponent(addr.id)}`, {
        method: "DELETE",
      });
      await refreshAddresses();
      if (editingAddress?.id === addr.id) {
        setEditingAddress(null);
      }
      showToast("Đã xóa địa chỉ nhận hàng.");
    } catch (err) {
      setAddrActionError(getErrorMessage(err, "Chưa thể xóa địa chỉ này lúc này."));
    } finally {
      setAddrBusyId(null);
    }
  };

  const handleRegisterShop = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submittingShop) return;

    const cleanName = shopName.trim();
    const cleanPhone = normalizeVietnamesePhone(shopPhone);
    const cleanLicense = shopLicenseDoc.trim();
    const cleanPickupAddrId = shopPickupAddrId.trim();
    const wasInfoRequested = isShopInfoRequested(myShop);

    if (cleanName.length < 3) {
      setShopFormError("Vui lòng nhập tên gian hàng tối thiểu 3 ký tự.");
      return;
    }
    if (!isValidVietnamesePhone(cleanPhone)) {
      setShopFormError(
        "Số điện thoại liên hệ không hợp lệ. Vui lòng nhập số điện thoại Việt Nam (ví dụ: 0912345678 hoặc +84912345678)."
      );
      return;
    }
    if (!cleanLicense) {
      setShopFormError("Vui lòng nhập mã giấy phép kinh doanh hoặc giấy phép hộ kinh doanh.");
      return;
    }
    if (!cleanPickupAddrId) {
      setShopFormError("Vui lòng thêm và chọn địa chỉ lấy hàng từ sổ địa chỉ trước khi gửi hồ sơ.");
      return;
    }

    setShopFormError(null);
    setSubmittingShop(true);
    try {
      const res = await api<{ shop: GatewayShop }>("/shops/register", {
        method: "POST",
        json: {
          name: cleanName,
          slug: shopSlug.trim(),
          description: shopDesc.trim(),
          pickup_address_id: cleanPickupAddrId,
          contact_phone: cleanPhone,
          business_license_doc: cleanLicense,
        },
      });
      if (res?.shop) {
        setGatewayShop(res.shop);
      }
      setShopName("");
      setShopSlug("");
      setShopDesc("");
      setShopLicenseDoc("");
      showToast(
        wasInfoRequested
          ? "Đã gửi lại hồ sơ bổ sung. Hồ sơ đang chờ quản trị viên xét duyệt."
          : "Đã gửi hồ sơ đăng ký gian hàng. Hồ sơ đang chờ xét duyệt."
      );
    } catch (err) {
      if (err instanceof ApiError && err.code === "ALREADY_EXISTS") {
        setShopFormError(
          "Tên gian hàng, đường dẫn này đã có người sử dụng hoặc tài khoản của bạn đã có hồ sơ đang chờ duyệt."
        );
      } else {
        setShopFormError(getErrorMessage(err, "Chưa thể gửi hồ sơ đăng ký gian hàng lúc này."));
      }
    } finally {
      setSubmittingShop(false);
    }
  };

  const handleSaveVoucher = async (voucher: GatewayVoucher) => {
    if (savingVoucherId) return;
    setVoucherActionError(null);
    setSavingVoucherId(voucher.id);
    try {
      const res = await api<{ saved: boolean; voucher?: GatewayVoucher }>(
        `/vouchers/${encodeURIComponent(voucher.id)}/save`,
        {
          method: "POST",
          json: { code: voucher.code },
        }
      );
      setVouchers((prev) =>
        prev.map((item) =>
          item.id === voucher.id ? { ...(res?.voucher ?? item), saved: res?.saved ?? true } : item
        )
      );
      showToast(`Đã lưu mã ${voucher.code} vào ví voucher.`);
    } catch (err) {
      setVoucherActionError(
        formatPromotionErrorMessage(err, "save_voucher", "Chưa thể lưu mã ưu đãi này lúc này.")
      );
    } finally {
      setSavingVoucherId(null);
    }
  };

  const handleUseVoucherInCart = async (code: string) => {
    try {
      await navigator.clipboard.writeText(code);
      showToast(`Đã sao chép mã ${code}. Bạn có thể chọn hoặc dán mã này tại giỏ hàng.`);
    } catch {
      showToast(`Mã ưu đãi ${code} sẵn sàng áp dụng trong giỏ hàng.`);
    }
    router.push("/gio-hang");
  };

  const loadNotifications = async (filter: NotificationFilterKey) => {
    setNotifFilter(filter);
    setNotifLoading(true);
    setNotifError(null);
    const params = new URLSearchParams({ page: "1", page_size: "20" });
    if (filter === "UNREAD") {
      params.set("unread_only", "true");
    } else if (filter !== "ALL") {
      params.set("category", filter);
    }
    try {
      const res = await api<GatewayNotificationsResponse>(`/notifications?${params.toString()}`);
      setNotifications(Array.isArray(res?.items) ? res.items : []);
      if (typeof res?.unread_count === "number") {
        setUnreadCount(res.unread_count);
      }
    } catch (err) {
      setNotifError(getErrorMessage(err, "Không thể tải danh sách thông báo lúc này."));
    } finally {
      setNotifLoading(false);
    }
  };

  const handleMarkNotificationsRead = async (ids: string[], markAll: boolean) => {
    if (markingNotif) return;
    setMarkingNotif(true);
    setNotifError(null);
    try {
      const res = await api<{ updated_count: number; unread_count: number }>(
        "/notifications/read",
        {
          method: "POST",
          json: { notification_ids: ids, mark_all: markAll },
        }
      );
      if (typeof res?.unread_count === "number") {
        setUnreadCount(res.unread_count);
      }
      setNotifications((prev) =>
        prev.map((n) => (markAll || ids.includes(n.id) ? { ...n, is_read: true } : n))
      );
      if (notifFilter === "UNREAD") {
        setNotifications((prev) => prev.filter((n) => !n.is_read));
      }
      showToast(
        markAll ? "Đã đánh dấu tất cả thông báo là đã đọc." : "Đã đánh dấu thông báo đã đọc."
      );
    } catch (err) {
      setNotifError(getErrorMessage(err, "Chưa thể cập nhật trạng thái thông báo."));
    } finally {
      setMarkingNotif(false);
    }
  };

  const handleSaveNotifPrefs = async (e: React.FormEvent) => {
    e.preventDefault();
    if (savingPrefs) return;
    setPrefsError(null);
    setSavingPrefs(true);
    try {
      const res = await api<{ preferences: GatewayNotificationPreferences }>(
        "/notifications/preferences",
        {
          method: "PUT",
          json: {
            order_in_app: notifPrefs.order_in_app,
            order_email: notifPrefs.order_email,
            order_push: notifPrefs.order_push,
            promotion_in_app: notifPrefs.promotion_in_app,
            promotion_email: notifPrefs.promotion_email,
            promotion_push: notifPrefs.promotion_push,
            system_in_app: notifPrefs.system_in_app,
            system_email: notifPrefs.system_email,
            system_push: notifPrefs.system_push,
          },
        }
      );
      if (res?.preferences) {
        setNotifPrefs(res.preferences);
      }
      showToast("Đã lưu tùy chọn nhận thông báo.");
    } catch (err) {
      setPrefsError(getErrorMessage(err, "Chưa thể lưu tùy chọn thông báo lúc này."));
    } finally {
      setSavingPrefs(false);
    }
  };

  const refreshCoins = async () => {
    try {
      const res = await api<GatewayCoinBalance>("/coins/balance");
      setCoinBalance(res);
    } catch {
      // Keep current balance display
    }
  };

  const renderSuggestedShelf = (title: string, subtitle?: string) => (
    <section className={`${shared.section} ${shared.sectionGap}`} data-reveal="">
      <div className={shared.sectionHead}>
        <h2 className={shared.sectionTitle}>{title}</h2>
        {subtitle && <p className={styles.shelfSub}>{subtitle}</p>}
      </div>
      <div className={styles.productGrid}>
        {suggestedProducts.map((product) => (
          <div key={product.id} data-reveal="">
            <ProductCard product={product} />
          </div>
        ))}
      </div>
    </section>
  );

  if (forcedState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Đang tải thông tin tài khoản, sổ địa chỉ, số dư Xu và đơn hàng gần đây.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Hồ sơ thành viên</h2>
          <StateView kind="loading" title="Đang đồng bộ tài khoản NexusMart..." rows={4} />
        </section>
        {renderSuggestedShelf("Gợi ý cho tủ đồ cơ bản")}
      </div>
    );
  }

  if (forcedState === "error") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Không thể đồng bộ thông tin tài khoản lúc này. Vui lòng thử lại.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
          <StateView
            kind="error"
            title="Chưa thể tải hồ sơ tài khoản"
            message="Không thể đọc số dư Xu và danh sách địa chỉ lúc này. Vui lòng tải lại trang."
            actionLabel="Tải lại"
            onAction={() => router.refresh()}
          />
        </section>
        {renderSuggestedShelf("Sản phẩm nổi bật")}
      </div>
    );
  }

  if (forcedState === "empty") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Chưa có hoạt động mua hàng hay địa chỉ lưu sẵn trên tài khoản này.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Bắt đầu mua sắm</h2>
          <StateView
            kind="empty"
            title="Tài khoản chưa có đơn hàng hay địa chỉ nào"
            message="Thêm địa chỉ nhận hàng và khám phá danh mục sản phẩm cơ bản trên NexusMart."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        {renderSuggestedShelf("Khám phá đồ cơ bản trên NexusMart")}
      </div>
    );
  }

  const realCoins = coinBalance?.balance ?? 0;
  const coinTxs = coinBalance?.recent_transactions ?? [];
  const shopInfoRequested = isShopInfoRequested(myShop);
  const showShopForm = canSubmitShopApplication(myShop);
  const shopStatusMeta = myShop ? getShopStatusInfo(myShop.status, myShop.rejection_reason) : null;
  const canAccessSeller = myShop?.status === "ACTIVE" || profileUser.role === "seller";

  return (
    <div className={shared.page}>
      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${shared.pageTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Tài khoản của tôi
              </span>
            </span>
          </h1>
          <p className={styles.leadCopy} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {profileUser.full_name} · {roleLabel(profileUser.role)} · Quản lý hồ sơ, sổ địa chỉ, đơn
            hàng và thông báo.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          {canAccessSeller && (
            <Button variant="primary" size="md" href="/seller">
              Kênh Người Bán
            </Button>
          )}
          <Button variant="secondary" size="md" href="/don-hang">
            Đơn hàng ({initialOrdersTotal})
          </Button>
          <Button
            variant="danger"
            size="md"
            onClick={() => {
              void logout().then(() => {
                window.location.assign("/");
              });
            }}
          >
            Đăng xuất
          </Button>
        </div>
      </section>

      {/* Asymmetric Hero Summary: Dark Xu & Tier Band + Crisp White Activity & Shop Status Panel */}
      <section className={shared.section}>
        <h2 className="sr-only">Tổng quan tài khoản và số dư Xu</h2>
        <div className={styles.heroBand}>
          <div
            className={styles.memberDarkCard}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.tierHeadRow}>
              <div>
                <p className={styles.tierName}>Ví Xu NexusMart</p>
                <p className={styles.tierSub}>1 Xu = 1₫ · Dùng trực tiếp khi thanh toán đơn hàng</p>
              </div>
              <span className={styles.tierBadge}>{roleLabel(profileUser.role)}</span>
            </div>

            <div className={styles.xuRow}>
              <div
                className={styles.xuStat}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <span className={`${styles.xuBig} num`}>{formatInt(realCoins)} Xu</span>
                <span className={styles.xuCaption}>Số dư Xu hiện tại</span>
              </div>
              <div
                className={styles.xuStat}
                data-rise=""
                style={{ "--i": 4 } as React.CSSProperties}
              >
                <span className={`${styles.xuMed} num`}>{coinTxs.length}</span>
                <span className={styles.xuCaption}>Giao dịch Xu gần đây</span>
              </div>
              <div
                className={styles.xuStat}
                data-rise=""
                style={{ "--i": 5 } as React.CSSProperties}
              >
                <span className={`${styles.xuMed} num`}>{savedVouchersCount} mã</span>
                <span className={styles.xuCaption}>Voucher đã lưu</span>
              </div>
            </div>
          </div>

          <div
            className={styles.activityCard}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.activityStats}>
              <div className={styles.activityItem}>
                <span className={`${styles.activityNum} num`}>{initialOrdersTotal}</span>
                <span className={styles.activityLabel}>
                  Đơn hàng{inProgressOrders > 0 ? ` (${inProgressOrders} đang xử lý)` : ""}
                </span>
              </div>
              <div className={styles.activityItem}>
                <span className={`${styles.activityNum} num`}>{addresses.length}</span>
                <span className={styles.activityLabel}>Địa chỉ đã lưu</span>
              </div>
              <div className={styles.activityItem}>
                <span className={`${styles.activityNum} num`}>{unreadCount}</span>
                <span className={styles.activityLabel}>Thông báo chưa đọc</span>
              </div>
            </div>

            <div className={styles.retentionBlock}>
              <div className={styles.addressHead}>
                <p className={styles.retentionTitle}>Trạng thái gian hàng</p>
                {shopStatusMeta && (
                  <span className={`${shared.statusTag} ${toneClass(shopStatusMeta.tone)}`}>
                    {shopStatusMeta.label}
                  </span>
                )}
              </div>
              {myShop ? (
                <p className={styles.bodyCopy}>
                  <strong>{myShop.name}</strong>: {shopStatusMeta?.description}
                </p>
              ) : (
                <p className={styles.bodyCopy}>
                  Bạn muốn bán hàng trên NexusMart? Gửi hồ sơ đăng ký mở gian hàng trực tuyến và
                  theo dõi tiến độ duyệt ngay tại đây.
                </p>
              )}
              <div className={shared.formActions}>
                {canAccessSeller ? (
                  <Button variant="primary" size="sm" href="/seller">
                    Mở Kênh Người Bán
                  </Button>
                ) : (
                  <Button variant="ink" size="sm" onClick={() => setActiveTab("gian-hang")}>
                    {myShop ? "Xem hồ sơ gian hàng" : "Đăng ký mở gian hàng"}
                  </Button>
                )}
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Account section tabs */}
      <section className={shared.section}>
        <div
          className={styles.tabBar}
          role="tablist"
          aria-label="Danh mục quản lý tài khoản"
          data-rise=""
          style={{ "--i": 6 } as React.CSSProperties}
        >
          {ACCOUNT_TABS.map((tab) => {
            const active = activeTab === tab.key;
            const badge =
              tab.key === "dia-chi"
                ? addresses.length
                : tab.key === "thong-bao"
                  ? unreadCount > 0
                    ? unreadCount
                    : null
                  : tab.key === "voucher"
                    ? vouchers.length
                    : tab.key === "yeu-thich"
                      ? wishlist.length > 0
                        ? wishlist.length
                        : null
                      : null;
            return (
              <button
                key={tab.key}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${styles.tabBtn} ${active ? styles.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.key)}
              >
                <span>{tab.label}</span>
                {badge !== null && <span className={`${styles.tabCount} num`}>{badge}</span>}
              </button>
            );
          })}
        </div>

        {/* TAB 1: Tổng quan & Hồ sơ */}
        {activeTab === "tong-quan" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              {/* Real Profile (GET/PATCH /users/me) */}
              <div className={styles.unboxedSection} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Thông tin cá nhân</h2>
                  <div className={shared.formActions}>
                    <Button
                      variant="ink"
                      size="sm"
                      onClick={() => {
                        setEditingProfile((v) => !v);
                        setProfileError(null);
                      }}
                    >
                      {editingProfile ? "Đóng chỉnh sửa" : "Chỉnh sửa hồ sơ"}
                    </Button>
                  </div>
                </div>

                {editingProfile ? (
                  <form
                    className={styles.yirPanel}
                    onSubmit={(e) => void handleSaveProfile(e)}
                    noValidate
                  >
                    <div className={shared.formGrid}>
                      <div className={shared.field}>
                        <label htmlFor="profile-fullname" className={shared.fieldLabel}>
                          Họ và tên
                        </label>
                        <input
                          id="profile-fullname"
                          type="text"
                          className={shared.input}
                          value={profileName}
                          onChange={(e) => setProfileName(e.target.value)}
                          disabled={savingProfile}
                          required
                        />
                      </div>
                      <div className={shared.field}>
                        <label htmlFor="profile-phone" className={shared.fieldLabel}>
                          Số điện thoại
                        </label>
                        <input
                          id="profile-phone"
                          type="tel"
                          className={shared.input}
                          value={profilePhone}
                          onChange={(e) => setProfilePhone(e.target.value)}
                          placeholder="0912486305"
                          disabled={savingProfile}
                        />
                        {profileUser.phone_verified &&
                          profilePhone.replace(/\s+/g, "").trim() !==
                            (profileUser.phone ?? "").replace(/\s+/g, "").trim() && (
                            <span className={shared.fieldHint}>
                              Lưu ý: thay đổi số điện thoại sẽ cần xác minh lại OTP trước khi đặt
                              đơn COD.
                            </span>
                          )}
                      </div>
                      <div className={`${shared.field} ${shared.formFull}`}>
                        <label htmlFor="profile-avatar" className={shared.fieldLabel}>
                          Liên kết ảnh đại diện (không bắt buộc)
                        </label>
                        <input
                          id="profile-avatar"
                          type="url"
                          className={shared.input}
                          value={profileAvatarUrl}
                          onChange={(e) => setProfileAvatarUrl(e.target.value)}
                          placeholder="https://..."
                          disabled={savingProfile}
                        />
                      </div>
                    </div>
                    {profileError && (
                      <p className={shared.fieldError} role="alert">
                        {profileError}
                      </p>
                    )}
                    <div className={shared.formActions}>
                      <Button type="submit" variant="primary" size="md" disabled={savingProfile}>
                        {savingProfile ? "Đang lưu..." : "Lưu thay đổi"}
                      </Button>
                      <Button
                        type="button"
                        variant="secondary"
                        size="md"
                        disabled={savingProfile}
                        onClick={() => {
                          setEditingProfile(false);
                          setProfileName(profileUser.full_name);
                          setProfilePhone(profileUser.phone);
                          setProfileAvatarUrl(profileUser.avatar_url ?? "");
                          setProfileError(null);
                        }}
                      >
                        Hủy
                      </Button>
                    </div>
                  </form>
                ) : (
                  <dl className={styles.profileGrid}>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Họ và tên</dt>
                      <dd className={styles.profileVal}>{profileUser.full_name}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Địa chỉ email</dt>
                      <dd className={styles.profileVal}>{profileUser.email}</dd>
                    </div>
                    <div className={styles.profileItem} id="xac-minh-sdt">
                      <dt className={styles.profileKey}>Số điện thoại</dt>
                      <dd className={`${styles.profileVal} num`}>
                        <span>{profileUser.phone || "Chưa cập nhật"}</span>{" "}
                        <span
                          className={`${shared.statusTag} ${
                            profileUser.phone_verified ? shared.tonePos : shared.toneWarn
                          }`}
                        >
                          {profileUser.phone_verified ? "Đã xác minh" : "Chưa xác minh"}
                        </span>
                      </dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Ngày tham gia</dt>
                      <dd className={`${styles.profileVal} num`}>
                        {profileUser.created_at ? formatDateVi(profileUser.created_at) : "—"}
                      </dd>
                    </div>
                  </dl>
                )}

                {!editingProfile && !profileUser.phone_verified && (
                  <div className={styles.yirPanel} style={{ marginTop: 14 }}>
                    <div className={styles.addressHead}>
                      <p className={styles.retentionTitle}>Xác minh số điện thoại cho COD</p>
                      <Button
                        type="button"
                        variant="ink"
                        size="sm"
                        onClick={() => {
                          setPhoneOtpOpen((v) => !v);
                          setOtpPhoneInput(profileUser.phone || "");
                          setOtpError(null);
                        }}
                      >
                        {phoneOtpOpen ? "Đóng xác minh" : "Xác minh SĐT"}
                      </Button>
                    </div>
                    <p className={styles.bodyCopy}>
                      Xác minh số điện thoại bằng mã OTP 6 chữ số để bật phương thức thanh toán khi
                      nhận hàng (COD).
                    </p>

                    {phoneOtpOpen && (
                      <form onSubmit={(e) => void handleVerifyPhoneOtp(e)} noValidate>
                        <div className={shared.formGrid}>
                          <div className={shared.field}>
                            <label htmlFor="otp-phone-input" className={shared.fieldLabel}>
                              Số điện thoại xác minh
                            </label>
                            <div className={shared.codeInputRow}>
                              <input
                                id="otp-phone-input"
                                type="tel"
                                className={shared.input}
                                value={otpPhoneInput}
                                onChange={(e) => {
                                  setOtpPhoneInput(e.target.value);
                                  setOtpSentMasked(null);
                                  setOtpCode("");
                                  setOtpExpiresSec(0);
                                  if (otpError) setOtpError(null);
                                }}
                                placeholder="0912345678"
                                disabled={otpSending || otpVerifying}
                              />
                              <Button
                                type="button"
                                variant="secondary"
                                size="md"
                                disabled={otpSending || otpVerifying || otpCooldownSec > 0}
                                onClick={() => void handleRequestPhoneOtp()}
                              >
                                {otpSending
                                  ? "Đang gửi..."
                                  : otpCooldownSec > 0
                                    ? `Gửi lại (${otpCooldownSec}s)`
                                    : otpSentMasked
                                      ? "Gửi lại OTP"
                                      : "Gửi mã OTP"}
                              </Button>
                            </div>
                            {otpSentMasked && (
                              <span className={shared.fieldHint} aria-live="polite">
                                {otpExpiresSec > 0
                                  ? `Mã OTP gồm 6 chữ số đã được gửi tới ${otpSentMasked} (SMS mô phỏng, xem trong email tài khoản — còn hiệu lực ${Math.floor(otpExpiresSec / 60)}:${String(otpExpiresSec % 60).padStart(2, "0")}).`
                                  : `Mã OTP gửi tới ${otpSentMasked} đã hết hạn. Vui lòng bấm Gửi lại OTP.`}
                              </span>
                            )}
                          </div>

                          <div className={shared.field}>
                            <label htmlFor="otp-code-input" className={shared.fieldLabel}>
                              Mã OTP (6 chữ số)
                            </label>
                            <input
                              id="otp-code-input"
                              type="text"
                              inputMode="numeric"
                              autoComplete="one-time-code"
                              maxLength={6}
                              className={`${shared.input} num`}
                              value={otpCode}
                              onChange={(e) => {
                                setOtpCode(e.target.value.replace(/[^\d]/g, "").slice(0, 6));
                                if (otpError) setOtpError(null);
                              }}
                              placeholder="Nhập 6 chữ số"
                              disabled={otpVerifying}
                            />
                          </div>
                        </div>

                        {otpError && (
                          <p className={shared.fieldError} role="alert" style={{ marginTop: 8 }}>
                            {otpError}
                          </p>
                        )}

                        <div className={shared.formActions}>
                          <Button
                            type="submit"
                            variant="primary"
                            size="md"
                            disabled={otpVerifying || otpCode.length !== 6}
                          >
                            {otpVerifying ? "Đang xác minh..." : "Xác nhận OTP"}
                          </Button>
                        </div>
                      </form>
                    )}
                  </div>
                )}
              </div>

              {/* Real Recent Orders (GET /orders?page_size=5) */}
              <div className={styles.unboxedSection} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Đơn hàng gần đây</h2>
                  <Link href="/don-hang" className={shared.breadcrumbLink}>
                    Xem tất cả ({initialOrdersTotal})
                  </Link>
                </div>

                {ordersError ? (
                  <StateView
                    kind="error"
                    title="Chưa thể tải đơn hàng gần đây"
                    message={ordersError}
                    actionLabel="Mở trang đơn hàng"
                    actionHref="/don-hang"
                  />
                ) : initialOrders.length === 0 ? (
                  <StateView
                    kind="empty"
                    title="Bạn chưa có đơn hàng nào"
                    message="Mọi đơn đặt mua trên NexusMart sẽ xuất hiện tại đây để bạn theo dõi nhanh."
                    actionLabel="Khám phá danh mục"
                    actionHref="/danh-muc"
                  />
                ) : (
                  <div className={styles.orderList}>
                    {initialOrders.map((order) => {
                      const meta = getOrderStatusInfo(order.status);
                      const firstItem = order.items[0];
                      const thumbUrl = firstItem
                        ? (visuals.bySkuId[firstItem.sku_id] ??
                          visuals.byProductId[firstItem.product_id]?.image)
                        : undefined;
                      const shop = shopsMap[order.shop_id];

                      return (
                        <Link
                          key={order.id}
                          href={`/don-hang/${order.id}`}
                          className={styles.orderRow}
                          data-reveal=""
                        >
                          {thumbUrl ? (
                            <img
                              src={thumbUrl}
                              alt={firstItem?.product_name ?? order.order_code}
                              width={56}
                              height={75}
                              className={styles.orderThumb}
                            />
                          ) : (
                            <span className={styles.orderThumbPlaceholder} aria-hidden="true">
                              <IconPackage size={20} />
                            </span>
                          )}
                          <div className={styles.orderInfo}>
                            <span className={`${styles.orderCode} num`}>{order.order_code}</span>
                            <span className={styles.orderSub}>
                              {shop?.name ?? "Gian hàng NexusMart"} ·{" "}
                              <span className="num">{order.items.length}</span> sản phẩm
                              {order.created_at ? (
                                <>
                                  {" "}
                                  · <span className="num">{formatDateVi(order.created_at)}</span>
                                </>
                              ) : null}
                            </span>
                          </div>
                          <div className={styles.orderRight}>
                            <span className={`${styles.orderTotal} num`}>
                              {formatVnd(order.total_payable_vnd)}
                            </span>
                            <span className={`${shared.statusTag} ${toneClass(meta.tone)}`}>
                              {meta.label}
                            </span>
                          </div>
                        </Link>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* Real Coin Balance & Recent Transactions (GET /coins/balance) */}
              <div className={styles.unboxedSectionLast} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Lịch sử Xu NexusMart</h2>
                  <Button variant="ghost" size="sm" onClick={() => void refreshCoins()}>
                    Cập nhật số dư
                  </Button>
                </div>

                {coinsError ? (
                  <StateView
                    kind="error"
                    title="Chưa thể tải lịch sử Xu"
                    message={coinsError}
                    actionLabel="Thử lại"
                    onAction={() => void refreshCoins()}
                  />
                ) : coinTxs.length === 0 ? (
                  <p className={styles.bodyCopy}>
                    Số dư hiện tại là <strong className="num">{formatInt(realCoins)} Xu</strong>.
                    Chưa có giao dịch cộng hoặc trừ Xu nào gần đây.
                  </p>
                ) : (
                  <div className={styles.voucherList}>
                    {coinTxs.map((tx) => (
                      <div key={tx.id} className={styles.voucherRow}>
                        <div className={styles.voucherInfo}>
                          <span className={styles.voucherTitle}>{tx.reason || "Giao dịch Xu"}</span>
                          <span className={styles.metaNote}>
                            {tx.created_at ? formatDateTimeVi(tx.created_at) : "—"}
                          </span>
                        </div>
                        <span
                          className={`${styles.coinDelta} ${
                            tx.delta >= 0 ? styles.coinPlus : styles.coinMinus
                          } num`}
                        >
                          {formatSignedInt(tx.delta)} Xu
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>

            {/* Right column: Clearly separated Mock Membership Panels */}
            <div className={styles.colStack}>
              <AccountMockOverviewPanels />
            </div>
          </div>
        )}

        {/* TAB 2: Địa chỉ nhận hàng (100% REAL CRUD) */}
        {activeTab === "dia-chi" && (
          <div className={styles.colStack}>
            <div className={shared.sectionHead}>
              <h2 className={shared.sectionTitle}>Sổ địa chỉ nhận hàng</h2>
              <Button
                variant="ink"
                size="sm"
                onClick={() => {
                  setEditingAddress(null);
                  setShowAddAddress((prev) => !prev);
                }}
              >
                {showAddAddress ? "Đóng biểu mẫu" : "Thêm địa chỉ mới"}
              </Button>
            </div>

            {addrActionError && (
              <p className={shared.fieldError} role="alert">
                {addrActionError}
              </p>
            )}

            {showAddAddress && (
              <AddressForm
                submitLabel="Lưu địa chỉ nhận hàng"
                onSaved={() => {
                  setShowAddAddress(false);
                  void refreshAddresses();
                }}
                onCancel={() => setShowAddAddress(false)}
              />
            )}

            {addrFetchError ? (
              <StateView
                kind="error"
                title="Chưa thể tải sổ địa chỉ"
                message={addrFetchError}
                actionLabel="Thử tải lại"
                onAction={() => void refreshAddresses()}
              />
            ) : addresses.length === 0 ? (
              <StateView
                kind="empty"
                title="Bạn chưa lưu địa chỉ nhận hàng nào"
                message="Thêm địa chỉ Tỉnh/Thành phố và Phường/Xã để tính phí vận chuyển và đặt hàng nhanh hơn."
                actionLabel="Thêm địa chỉ đầu tiên"
                onAction={() => setShowAddAddress(true)}
              />
            ) : (
              <div className={styles.addressStack}>
                {addresses.map((addr) => {
                  const isEditingThis = editingAddress?.id === addr.id;
                  const busy = addrBusyId === addr.id;
                  return (
                    <article
                      key={addr.id}
                      className={`${styles.addressRow} ${
                        addr.is_default ? styles.addressDefault : ""
                      }`}
                    >
                      {isEditingThis ? (
                        <AddressForm
                          initial={addr}
                          submitLabel="Lưu cập nhật"
                          onSaved={() => {
                            setEditingAddress(null);
                            void refreshAddresses();
                          }}
                          onCancel={() => setEditingAddress(null)}
                        />
                      ) : (
                        <>
                          <div className={styles.addressHead}>
                            <span className={styles.addressRecipient}>
                              {addr.recipient_name} ·{" "}
                              <span className="num">{addr.recipient_phone}</span>
                            </span>
                            {addr.is_default && (
                              <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                Mặc định
                              </span>
                            )}
                          </div>
                          <p className={styles.bodyCopy}>
                            {[addr.street_address, addr.ward_name, addr.province_name]
                              .filter(Boolean)
                              .join(", ")}
                          </p>
                          <div className={styles.addressActions}>
                            <Button
                              variant="secondary"
                              size="sm"
                              disabled={busy}
                              onClick={() => {
                                setShowAddAddress(false);
                                setEditingAddress(addr);
                              }}
                            >
                              Chỉnh sửa
                            </Button>
                            {!addr.is_default && (
                              <Button
                                variant="secondary"
                                size="sm"
                                disabled={busy}
                                onClick={() => void handleSetDefaultAddress(addr)}
                              >
                                {busy ? "Đang cập nhật..." : "Đặt mặc định"}
                              </Button>
                            )}
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={busy}
                              onClick={() => void handleDeleteAddress(addr)}
                            >
                              {busy ? "Đang xóa..." : "Xóa địa chỉ"}
                            </Button>
                          </div>
                        </>
                      )}
                    </article>
                  );
                })}
              </div>
            )}
          </div>
        )}

        {/* TAB 3: Đăng ký mở gian hàng & Trạng thái duyệt (100% REAL) */}
        {activeTab === "gian-hang" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              {shopError && !myShop ? (
                <StateView
                  kind="error"
                  title="Chưa thể kiểm tra trạng thái gian hàng"
                  message={shopError}
                  actionLabel="Tải lại"
                  onAction={() => router.refresh()}
                />
              ) : null}

              {myShop && (
                <div className={styles.yirPanel}>
                  <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                    <h2 className={shared.sectionTitle}>Hồ sơ gian hàng của bạn</h2>
                    {shopStatusMeta && (
                      <span className={`${shared.statusTag} ${toneClass(shopStatusMeta.tone)}`}>
                        {shopStatusMeta.label}
                      </span>
                    )}
                  </div>
                  <p className={styles.bodyCopy}>{shopStatusMeta?.description}</p>

                  <dl className={styles.profileGrid}>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Tên gian hàng</dt>
                      <dd className={styles.profileVal}>{myShop.name}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Đường dẫn gian hàng</dt>
                      <dd className={styles.profileVal}>/gian-hang/{myShop.slug}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Số điện thoại liên hệ</dt>
                      <dd className={`${styles.profileVal} num`}>{myShop.contact_phone || "—"}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Ngày gửi hồ sơ</dt>
                      <dd className={`${styles.profileVal} num`}>
                        {myShop.created_at ? formatDateVi(myShop.created_at) : "—"}
                      </dd>
                    </div>
                  </dl>

                  {myShop.description && (
                    <p className={styles.bodyCopy}>
                      <strong>Giới thiệu:</strong> {myShop.description}
                    </p>
                  )}

                  {myShop.rejection_reason && (
                    <div
                      className={`${shared.feedbackBox} ${
                        shopInfoRequested ? shared.feedbackInfo : shared.feedbackError
                      }`}
                    >
                      {shopInfoRequested ? "Yêu cầu bổ sung:" : "Lý do từ chối:"}{" "}
                      {myShop.rejection_reason}
                    </div>
                  )}

                  <div className={shared.formActions}>
                    {myShop.status === "ACTIVE" && (
                      <>
                        <Button variant="primary" size="md" href="/seller">
                          Vào Kênh Người Bán
                        </Button>
                        <Button variant="secondary" size="md" href={`/gian-hang/${myShop.slug}`}>
                          Xem trang gian hàng
                        </Button>
                      </>
                    )}
                  </div>
                </div>
              )}

              {showShopForm && (
                <form
                  className={styles.yirPanel}
                  onSubmit={(e) => void handleRegisterShop(e)}
                  noValidate
                >
                  <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                    <h2 className={shared.sectionTitle}>
                      {shopInfoRequested
                        ? "Bổ sung hồ sơ đăng ký gian hàng"
                        : myShop?.status === "REJECTED"
                          ? "Gửi lại hồ sơ mở gian hàng"
                          : "Đăng ký mở gian hàng trên NexusMart"}
                    </h2>
                  </div>
                  <p className={styles.bodyCopy}>
                    {shopInfoRequested
                      ? "Cập nhật thông tin theo yêu cầu của quản trị viên rồi gửi lại để tiếp tục xét duyệt."
                      : "Điền thông tin xưởng may hoặc thương hiệu của bạn. Sau khi quản trị viên duyệt, tài khoản sẽ mở quyền truy cập Kênh Người Bán."}
                  </p>

                  <div className={shared.formGrid}>
                    <div className={shared.field}>
                      <label htmlFor="shop-name" className={shared.fieldLabel}>
                        Tên gian hàng
                      </label>
                      <input
                        id="shop-name"
                        type="text"
                        className={shared.input}
                        value={shopName}
                        onChange={(e) => setShopName(e.target.value)}
                        placeholder="Ví dụ: Xưởng Dệt Nam Định"
                        disabled={submittingShop}
                        required
                      />
                    </div>
                    <div className={shared.field}>
                      <label htmlFor="shop-phone" className={shared.fieldLabel}>
                        Số điện thoại liên hệ
                      </label>
                      <input
                        id="shop-phone"
                        type="tel"
                        className={shared.input}
                        value={shopPhone}
                        onChange={(e) => setShopPhone(e.target.value)}
                        placeholder="0912345678"
                        disabled={submittingShop}
                        required
                      />
                    </div>
                    <div className={shared.field}>
                      <label htmlFor="shop-slug" className={shared.fieldLabel}>
                        Đường dẫn mong muốn (không bắt buộc)
                      </label>
                      <input
                        id="shop-slug"
                        type="text"
                        className={shared.input}
                        value={shopSlug}
                        onChange={(e) => setShopSlug(e.target.value)}
                        placeholder="xuong-det-nam-dinh"
                        disabled={submittingShop}
                      />
                    </div>
                    <div className={shared.field}>
                      <label htmlFor="shop-license" className={shared.fieldLabel}>
                        Mã giấy phép kinh doanh
                      </label>
                      <input
                        id="shop-license"
                        type="text"
                        className={shared.input}
                        value={shopLicenseDoc}
                        onChange={(e) => setShopLicenseDoc(e.target.value)}
                        placeholder="Số GPKD hoặc hộ kinh doanh"
                        disabled={submittingShop}
                        required
                      />
                    </div>
                    <div className={`${shared.field} ${shared.formFull}`}>
                      <label htmlFor="shop-pickup-addr" className={shared.fieldLabel}>
                        Địa chỉ lấy hàng (từ sổ địa chỉ của bạn)
                      </label>
                      {addresses.length > 0 ? (
                        <select
                          id="shop-pickup-addr"
                          className={shared.select}
                          value={shopPickupAddrId}
                          onChange={(e) => setShopPickupAddrId(e.target.value)}
                          disabled={submittingShop}
                          required
                        >
                          {addresses.map((a) => (
                            <option key={a.id} value={a.id}>
                              {a.recipient_name}: {a.street_address}, {a.ward_name},{" "}
                              {a.province_name}
                            </option>
                          ))}
                        </select>
                      ) : (
                        <div className={styles.formHintRow}>
                          <span className={styles.metaNote}>
                            Bạn cần thêm ít nhất một địa chỉ lấy hàng trong sổ địa chỉ trước khi gửi
                            hồ sơ mở gian hàng.
                          </span>
                          <Button variant="ghost" size="sm" onClick={() => setActiveTab("dia-chi")}>
                            Thêm địa chỉ
                          </Button>
                        </div>
                      )}
                    </div>
                    <div className={`${shared.field} ${shared.formFull}`}>
                      <label htmlFor="shop-desc" className={shared.fieldLabel}>
                        Giới thiệu gian hàng
                      </label>
                      <textarea
                        id="shop-desc"
                        className={shared.textarea}
                        value={shopDesc}
                        onChange={(e) => setShopDesc(e.target.value)}
                        placeholder="Mô tả ngắn sản phẩm chủ lực, chất liệu vải và quy trình kiểm hàng..."
                        disabled={submittingShop}
                      />
                    </div>
                  </div>

                  {shopFormError && (
                    <p className={shared.fieldError} role="alert">
                      {shopFormError}
                    </p>
                  )}

                  <div className={shared.formActions}>
                    <Button type="submit" variant="primary" size="md" disabled={submittingShop}>
                      {submittingShop
                        ? "Đang gửi hồ sơ..."
                        : shopInfoRequested
                          ? "Gửi lại hồ sơ bổ sung"
                          : myShop?.status === "REJECTED"
                            ? "Gửi lại hồ sơ mở gian hàng"
                            : "Gửi hồ sơ mở gian hàng"}
                    </Button>
                  </div>
                </form>
              )}
            </div>

            <div className={styles.colStack}>
              <div className={styles.birthdayBand}>
                <h2 className={shared.sectionTitle}>Quy trình xét duyệt gian hàng</h2>
                <p className={styles.bodyCopy}>
                  1. Gửi tên gian hàng, số điện thoại liên hệ, mã giấy phép kinh doanh và địa chỉ
                  lấy hàng từ sổ địa chỉ của bạn.
                </p>
                <p className={styles.bodyCopy}>
                  2. Hồ sơ ở trạng thái <strong>Đang chờ duyệt</strong> trong lúc quản trị viên xác
                  minh thông tin.
                </p>
                <p className={styles.bodyCopy}>
                  3. Khi được duyệt, trang Tài khoản sẽ mở nút đi thẳng sang{" "}
                  <strong>Kênh Người Bán</strong> để đăng sản phẩm và xử lý đơn hàng.
                </p>
              </div>
            </div>
          </div>
        )}

        {/* TAB 4: Thông báo & Tùy chọn thông báo (100% REAL) */}
        {activeTab === "thong-bao" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                <h2 className={shared.sectionTitle}>Hộp thư thông báo</h2>
                {unreadCount > 0 && (
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={markingNotif}
                    onClick={() => void handleMarkNotificationsRead([], true)}
                  >
                    Đánh dấu tất cả đã đọc ({unreadCount})
                  </Button>
                )}
              </div>

              <div className={styles.filterPills} role="group" aria-label="Lọc thông báo">
                {NOTIFICATION_FILTERS.map((f) => (
                  <button
                    key={f.key}
                    type="button"
                    className={`${styles.filterPill} ${
                      notifFilter === f.key ? styles.filterPillActive : ""
                    }`}
                    onClick={() => void loadNotifications(f.key)}
                  >
                    {f.label}
                  </button>
                ))}
              </div>

              {notifLoading ? (
                <StateView kind="loading" title="Đang tải thông báo..." rows={3} />
              ) : notifError ? (
                <StateView
                  kind="error"
                  title="Chưa thể tải thông báo"
                  message={notifError}
                  actionLabel="Thử lại"
                  onAction={() => void loadNotifications(notifFilter)}
                />
              ) : notifications.length === 0 ? (
                <StateView
                  kind="empty"
                  title="Không có thông báo nào"
                  message="Mọi cập nhật về đơn hàng, giao nhận và ưu đãi của bạn sẽ hiển thị tại đây."
                />
              ) : (
                <div className={styles.notifList}>
                  {notifications.map((item) => {
                    const href = normalizeNotificationHref(item.link);
                    return (
                      <article
                        key={item.id}
                        className={`${styles.notifCard} ${!item.is_read ? styles.notifUnread : ""}`}
                      >
                        <div className={styles.addressHead}>
                          <div className={styles.voucherHeadLine}>
                            <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                              {getNotificationCategoryLabel(item.category)}
                            </span>
                            {!item.is_read && (
                              <span className={`${shared.statusTag} ${shared.toneWarn}`}>Mới</span>
                            )}
                          </div>
                          {item.created_at && (
                            <span className={`${styles.metaNote} num`}>
                              {formatDateTimeVi(item.created_at)}
                            </span>
                          )}
                        </div>
                        <p className={styles.addressRecipient}>{item.title}</p>
                        {item.body && <p className={styles.bodyCopy}>{item.body}</p>}
                        <div className={styles.addressActions}>
                          {href && (
                            <Button variant="secondary" size="sm" href={href}>
                              Xem chi tiết
                            </Button>
                          )}
                          {!item.is_read && (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={markingNotif}
                              onClick={() => void handleMarkNotificationsRead([item.id], false)}
                            >
                              Đánh dấu đã đọc
                            </Button>
                          )}
                        </div>
                      </article>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Real Notification Preferences (GET/PUT /notifications/preferences) */}
            <div className={styles.colStack}>
              <form
                className={styles.yirPanel}
                onSubmit={(e) => void handleSaveNotifPrefs(e)}
                noValidate
              >
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Tùy chọn nhận thông báo</h2>
                </div>
                <p className={styles.bodyCopy}>
                  Chọn kênh nhận thông báo phù hợp cho từng nhóm cập nhật trên tài khoản của bạn.
                </p>

                <div className={styles.prefGroupList}>
                  <fieldset className={styles.prefGroup}>
                    <legend className={styles.prefLegend}>Cập nhật đơn hàng</legend>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.order_in_app}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, order_in_app: e.target.checked }))
                        }
                      />
                      <span>Thông báo trên web</span>
                    </label>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.order_email}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, order_email: e.target.checked }))
                        }
                      />
                      <span>Email xác nhận & giao hàng</span>
                    </label>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.order_push}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, order_push: e.target.checked }))
                        }
                      />
                      <span>Thông báo đẩy</span>
                    </label>
                  </fieldset>

                  <fieldset className={styles.prefGroup}>
                    <legend className={styles.prefLegend}>Khuyến mãi & Ưu đãi</legend>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.promotion_in_app}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, promotion_in_app: e.target.checked }))
                        }
                      />
                      <span>Thông báo trên web</span>
                    </label>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.promotion_email}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, promotion_email: e.target.checked }))
                        }
                      />
                      <span>Email mã ưu đãi mới</span>
                    </label>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.promotion_push}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, promotion_push: e.target.checked }))
                        }
                      />
                      <span>Thông báo đẩy</span>
                    </label>
                  </fieldset>

                  <fieldset className={styles.prefGroup}>
                    <legend className={styles.prefLegend}>Hệ thống & Tài khoản</legend>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.system_in_app}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, system_in_app: e.target.checked }))
                        }
                      />
                      <span>Thông báo trên web</span>
                    </label>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.system_email}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, system_email: e.target.checked }))
                        }
                      />
                      <span>Email bảo mật & hồ sơ</span>
                    </label>
                    <label className={shared.checkboxRow}>
                      <input
                        type="checkbox"
                        className={shared.checkbox}
                        checked={notifPrefs.system_push}
                        onChange={(e) =>
                          setNotifPrefs((p) => ({ ...p, system_push: e.target.checked }))
                        }
                      />
                      <span>Thông báo đẩy</span>
                    </label>
                  </fieldset>
                </div>

                {prefsError && (
                  <p className={shared.fieldError} role="alert">
                    {prefsError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button type="submit" variant="primary" size="md" disabled={savingPrefs}>
                    {savingPrefs ? "Đang lưu..." : "Lưu tùy chọn thông báo"}
                  </Button>
                </div>
              </form>
            </div>
          </div>
        )}

        {/* TAB 5: Ví voucher thật + Thẻ quà tặng & .edu (mock) */}
        {activeTab === "voucher" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              <div>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Ví voucher NexusMart</h2>
                  <span className={`${shared.sectionNote} num`}>
                    Đã lưu {savedVouchersCount} / {vouchers.length} mã
                  </span>
                </div>

                <div className={styles.filterPills} role="group" aria-label="Lọc ví voucher">
                  <button
                    type="button"
                    className={`${styles.filterPill} ${
                      voucherFilter === "all" ? styles.filterPillActive : ""
                    }`}
                    onClick={() => setVoucherFilter("all")}
                  >
                    Tất cả ưu đãi ({vouchers.length})
                  </button>
                  <button
                    type="button"
                    className={`${styles.filterPill} ${
                      voucherFilter === "saved" ? styles.filterPillActive : ""
                    }`}
                    onClick={() => setVoucherFilter("saved")}
                  >
                    Đã lưu ({savedVouchersCount})
                  </button>
                </div>

                {voucherActionError && (
                  <p className={shared.fieldError} role="alert" style={{ marginTop: 12 }}>
                    {voucherActionError}
                  </p>
                )}

                {vouchersError ? (
                  <StateView
                    kind="error"
                    title="Chưa thể tải danh sách voucher"
                    message={vouchersError}
                    actionLabel="Tải lại"
                    onAction={() => router.refresh()}
                  />
                ) : displayedVouchers.length === 0 ? (
                  <StateView
                    kind="empty"
                    title={
                      voucherFilter === "saved"
                        ? "Bạn chưa lưu mã ưu đãi nào"
                        : "Hiện chưa có mã ưu đãi khả dụng"
                    }
                    message={
                      voucherFilter === "saved"
                        ? "Chuyển sang mục Tất cả ưu đãi để lưu các mã giảm giá vào ví của bạn."
                        : "Quay lại sau để nhận thêm mã giảm giá từ sàn và các gian hàng."
                    }
                    actionLabel={voucherFilter === "saved" ? "Xem tất cả ưu đãi" : undefined}
                    onAction={voucherFilter === "saved" ? () => setVoucherFilter("all") : undefined}
                  />
                ) : (
                  <div className={styles.voucherList}>
                    {displayedVouchers.map((voucher) => {
                      const shop = voucher.shop_id ? shopsMap[voucher.shop_id] : undefined;
                      const scopeLabel = getVoucherScopeLabel(voucher.scope, shop?.name);
                      const busy = savingVoucherId === voucher.id;

                      return (
                        <div key={voucher.id} className={styles.voucherRow}>
                          <div className={styles.voucherInfo}>
                            <div className={styles.voucherHeadLine}>
                              <span className={styles.voucherCode}>{voucher.code}</span>
                              <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                                {scopeLabel}
                              </span>
                              {voucher.saved && (
                                <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                  Đã lưu
                                </span>
                              )}
                            </div>
                            <span className={styles.voucherTitle}>
                              {voucher.name || formatVoucherBenefit(voucher)}
                            </span>
                            <span className={styles.metaNote}>
                              {formatVoucherBenefit(voucher)} · Đơn tối thiểu{" "}
                              <span className="num">{formatVnd(voucher.min_order_vnd)}</span>
                              {voucher.ends_at ? (
                                <>
                                  {" "}
                                  · HSD <span className="num">{formatDateVi(voucher.ends_at)}</span>
                                </>
                              ) : null}
                            </span>
                          </div>
                          <div className={shared.formActions}>
                            {!voucher.saved && (
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={busy}
                                onClick={() => void handleSaveVoucher(voucher)}
                              >
                                {busy ? "Đang lưu..." : "Lưu mã"}
                              </Button>
                            )}
                            <Button
                              variant="secondary"
                              size="sm"
                              onClick={() => void handleUseVoucherInCart(voucher.code)}
                            >
                              Dùng trong giỏ
                            </Button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            <div className={styles.colStack}>
              <AccountMockGiftCardsPanel />
            </div>
          </div>
        )}

        {/* TAB 6: Hồ sơ số đo (mock) */}
        {activeTab === "so-do" && <AccountMockBodyProfileTab />}

        {/* TAB 7: Sản phẩm yêu thích & đã xem (mock) */}
        {activeTab === "yeu-thich" && <AccountMockWishlistTab />}

        {/* TAB 8: Giới thiệu bạn bè (mock) */}
        {activeTab === "gioi-thieu" && <AccountMockReferralTab />}
      </section>

      {renderSuggestedShelf(
        "Đồ cơ bản gợi ý cho tủ đồ của bạn",
        "Bốn lựa chọn phổ biến từ các xưởng dệt Việt Nam trên NexusMart"
      )}
    </div>
  );
}
