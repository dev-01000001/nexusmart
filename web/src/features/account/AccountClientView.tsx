"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Heart, LogOut, MapPin, Package, Plus, Ticket } from "lucide-react";
import type { Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { MODEL_SHOTS } from "@/data/media";
import { ApiError, api } from "@/lib/api";
import type { GatewayNotificationPreferences } from "@/lib/gateway-types";
import { getProvinceByCode } from "@/lib/provinces";
import { formatDateVi, formatVnd } from "@/lib/format";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatAddressLine } from "@/features/commerce/cartEnrich";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import { SiteProductCard } from "@/features/site/SiteProductCard";
import { cn } from "@/lib/utils";
import { getOrderStatusInfo, isValidVietnamesePhone, normalizeVietnamesePhone } from "./orderMeta";
import { AccountAddressForm } from "./AccountAddressForm";
import {
  toStorefrontProvinceCode,
  type AccountProfile,
  type AddressView,
  type GwProvince,
  type GwWard,
} from "./accountTypes";
import type { OrderView } from "./orderView";
import { SITE_PAGE_CONTAINER, orderStatusTone } from "./ui/siteStatus";

type AccountTabKey = "tong-quan" | "dia-chi" | "voucher" | "yeu-thich";

const ACCOUNT_TABS: Array<{ key: AccountTabKey; label: string }> = [
  { key: "tong-quan", label: "Hồ sơ" },
  { key: "dia-chi", label: "Sổ địa chỉ" },
  { key: "voucher", label: "Mã giảm giá" },
  { key: "yeu-thich", label: "Yêu thích" },
];

const VALID_TABS = new Set<AccountTabKey>(ACCOUNT_TABS.map((t) => t.key));

export interface AccountClientViewProps {
  brandName: string;
  initialProfile: AccountProfile;
  initialAddresses: AddressView[];
  initialOrders: OrderView[];
  totalOrders: number;
  initialVouchers: Voucher[];
  provinces: GwProvince[];
  initialWards: GwWard[];
  initialProvinceCode: string;
  catalogProducts: Product[];
  initialNotificationPrefs?: GatewayNotificationPreferences | null;
  initialError?: string | null;
}

const DEFAULT_NOTIF_PREFS: Omit<GatewayNotificationPreferences, "user_id"> = {
  order_in_app: true,
  order_email: true,
  order_push: false,
  promotion_in_app: true,
  promotion_email: false,
  promotion_push: false,
  system_in_app: true,
  system_email: true,
  system_push: false,
};

function AccountLoadingSkeleton() {
  return (
    <div
      className="mx-auto max-w-[960px] space-y-6"
      aria-busy="true"
      aria-label="Đang tải tài khoản"
    >
      <div className="flex gap-2">
        <Skeleton className="h-9 w-24" />
        <Skeleton className="h-9 w-28" />
        <Skeleton className="h-9 w-28" />
        <Skeleton className="h-9 w-24" />
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <div className="space-y-2">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-4 w-48" />
        </div>
        <Skeleton className="h-48 w-full rounded-xl md:col-span-2" />
      </div>
    </div>
  );
}

function AccountSettingsRow({
  id,
  title,
  description,
  badge,
  action,
  children,
}: {
  id?: string;
  title: string;
  description: string;
  badge?: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section
      id={id}
      aria-labelledby={id ? `${id}-title` : undefined}
      className="grid scroll-mt-24 grid-cols-1 gap-4 md:grid-cols-12 md:items-start"
    >
      <div className="space-y-1.5 md:col-span-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 id={id ? `${id}-title` : undefined} className="text-sm font-semibold text-foreground">
            {title}
          </h2>
          {badge}
        </div>
        <p className="text-[13px] leading-[18px] text-muted-foreground">{description}</p>
        {action ? <div className="pt-1">{action}</div> : null}
      </div>
      <div className="min-w-0 md:col-span-8">{children}</div>
    </section>
  );
}

function AccountClientInner({
  brandName,
  initialProfile,
  initialAddresses,
  initialOrders,
  totalOrders,
  initialVouchers,
  provinces,
  initialWards,
  initialProvinceCode,
  catalogProducts,
  initialNotificationPrefs = null,
  initialError = null,
}: AccountClientViewProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const viewState = usePageStateParam();
  const { user: sessionUser, setUser, logout } = useSession();
  const {
    addToCart,
    saveVoucher,
    setSelectedVoucher,
    wishlist,
    toggleWishlist,
    recentlyViewed,
    clearRecentlyViewed,
    showToast,
  } = useStore();

  const tabParam = searchParams.get("tab") as AccountTabKey | null;
  const [activeTab, setActiveTab] = useState<AccountTabKey>(
    tabParam && VALID_TABS.has(tabParam) ? tabParam : "tong-quan"
  );

  useEffect(() => {
    if (tabParam && VALID_TABS.has(tabParam)) {
      setActiveTab(tabParam);
    } else if (!tabParam) {
      setActiveTab("tong-quan");
    }
  }, [tabParam]);

  const handleTabChange = useCallback((next: string) => {
    if (!VALID_TABS.has(next as AccountTabKey)) return;
    const typed = next as AccountTabKey;
    setActiveTab(typed);
    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", `?tab=${encodeURIComponent(typed)}`);
    }
  }, []);

  const [profile, setProfile] = useState<AccountProfile>(initialProfile);
  const [editingProfile, setEditingProfile] = useState(false);
  const [fullNameDraft, setFullNameDraft] = useState(initialProfile.fullName);
  const [phoneDraft, setPhoneDraft] = useState(initialProfile.phone);
  const [savingProfile, setSavingProfile] = useState(false);
  const [profileError, setProfileError] = useState<string | null>(null);

  const [addresses, setAddresses] = useState<AddressView[]>(initialAddresses);
  const [addressSheetMode, setAddressSheetMode] = useState<"closed" | "add" | "edit">("closed");
  const [editingAddress, setEditingAddress] = useState<AddressView | null>(null);
  const [busyAddressId, setBusyAddressId] = useState<string | null>(null);
  const [addressError, setAddressError] = useState<string | null>(null);

  const [vouchers, setVouchers] = useState<Voucher[]>(
    initialVouchers.filter((v) => v.scope === "shop")
  );
  const [savingVoucherCode, setSavingVoucherCode] = useState<string | null>(null);

  // Phone OTP verification state (#xac-minh-sdt)
  const [otpPhoneInput, setOtpPhoneInput] = useState<string>(initialProfile.phone || "");
  const [otpCodeInput, setOtpCodeInput] = useState<string>("");
  const [otpSent, setOtpSent] = useState<boolean>(false);
  const [otpCooldown, setOtpCooldown] = useState<number>(0);
  const [otpExpiresSec, setOtpExpiresSec] = useState<number>(0);
  const [sendingOtp, setSendingOtp] = useState<boolean>(false);
  const [verifyingOtp, setVerifyingOtp] = useState<boolean>(false);
  const [otpFeedback, setOtpFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  // Notification preferences state
  const [notifPrefs, setNotifPrefs] = useState<Omit<GatewayNotificationPreferences, "user_id">>(
    () =>
      initialNotificationPrefs
        ? {
            order_in_app: initialNotificationPrefs.order_in_app,
            order_email: initialNotificationPrefs.order_email,
            order_push: initialNotificationPrefs.order_push,
            promotion_in_app: initialNotificationPrefs.promotion_in_app,
            promotion_email: initialNotificationPrefs.promotion_email,
            promotion_push: initialNotificationPrefs.promotion_push,
            system_in_app: initialNotificationPrefs.system_in_app,
            system_email: initialNotificationPrefs.system_email,
            system_push: initialNotificationPrefs.system_push,
          }
        : DEFAULT_NOTIF_PREFS
  );
  const [savingNotifPrefs, setSavingNotifPrefs] = useState<boolean>(false);
  const [notifFeedback, setNotifFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const otpTimerActive = otpCooldown > 0 || otpExpiresSec > 0;
  useEffect(() => {
    if (!otpTimerActive) return;
    const timer = window.setInterval(() => {
      setOtpCooldown((prev) => (prev > 1 ? prev - 1 : 0));
      setOtpExpiresSec((prev) => (prev > 1 ? prev - 1 : 0));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [otpTimerActive]);

  useEffect(() => {
    if (typeof window !== "undefined" && window.location.hash === "#xac-minh-sdt") {
      setActiveTab("tong-quan");
      window.setTimeout(() => {
        document.getElementById("xac-minh-sdt")?.scrollIntoView({ behavior: "smooth" });
      }, 120);
    }
  }, []);

  const handleRequestPhoneOtp = async () => {
    const cleanPhone = normalizeVietnamesePhone(otpPhoneInput || profile.phone || "");
    if (!isValidVietnamesePhone(cleanPhone)) {
      setOtpFeedback({
        ok: false,
        message: "Số điện thoại Việt Nam chưa hợp lệ (ví dụ: 0912345678).",
      });
      return;
    }
    setOtpFeedback(null);
    setSendingOtp(true);
    try {
      const res = await api<{
        sent?: boolean;
        retry_after_seconds?: number;
        cooldown_seconds?: number;
        expires_in_seconds?: number;
        phone_masked?: string;
      }>("/api/users/me/phone/otp", {
        method: "POST",
        body: { phone: cleanPhone },
      });
      setOtpPhoneInput(cleanPhone);
      setOtpSent(true);
      setOtpCooldown(
        Math.max(15, Math.trunc(res.retry_after_seconds ?? res.cooldown_seconds ?? 60))
      );
      setOtpExpiresSec(Math.max(60, Math.trunc(res.expires_in_seconds ?? 300)));
      setOtpFeedback({
        ok: true,
        message: `Đã gửi mã xác minh 6 số tới ${res.phone_masked || cleanPhone}. Nhập mã bên dưới để hoàn tất.`,
      });
    } catch (err) {
      setOtpFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa thể gửi mã OTP lúc này.",
      });
    } finally {
      setSendingOtp(false);
    }
  };

  const handleVerifyPhoneOtp = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanPhone = normalizeVietnamesePhone(otpPhoneInput || profile.phone || "");
    const cleanOtp = otpCodeInput.replace(/\D/g, "").trim();
    if (!isValidVietnamesePhone(cleanPhone)) {
      setOtpFeedback({ ok: false, message: "Số điện thoại chưa hợp lệ." });
      return;
    }
    if (!/^\d{6}$/.test(cleanOtp)) {
      setOtpFeedback({ ok: false, message: "Nhập đủ 6 chữ số mã OTP xác minh." });
      return;
    }
    setOtpFeedback(null);
    setVerifyingOtp(true);
    try {
      const res = await api<{ verified?: boolean; user?: SessionUser }>(
        "/api/users/me/phone/verify",
        {
          method: "POST",
          body: { phone: cleanPhone, otp: cleanOtp },
        }
      );
      setProfile((prev) => ({
        ...prev,
        phone: res.user?.phone || cleanPhone,
        phoneVerified: true,
      }));
      if (res.user) {
        setUser(res.user);
      } else if (sessionUser) {
        setUser({ ...sessionUser, phone: cleanPhone, phoneVerified: true });
      }
      setOtpCodeInput("");
      setOtpSent(false);
      setOtpFeedback({ ok: true, message: "Đã xác minh số điện thoại thành công." });
      showToast("Đã xác minh số điện thoại.");
      router.refresh();
    } catch (err) {
      setOtpFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Mã OTP chưa đúng hoặc đã hết hạn.",
      });
    } finally {
      setVerifyingOtp(false);
    }
  };

  const handleSaveNotificationPrefs = async (e: React.FormEvent) => {
    e.preventDefault();
    setNotifFeedback(null);
    setSavingNotifPrefs(true);
    try {
      const res = await api<{ preferences?: GatewayNotificationPreferences }>(
        "/api/notifications/preferences",
        {
          method: "PUT",
          body: notifPrefs,
        }
      );
      if (res.preferences) {
        setNotifPrefs({
          order_in_app: res.preferences.order_in_app,
          order_email: res.preferences.order_email,
          order_push: res.preferences.order_push,
          promotion_in_app: res.preferences.promotion_in_app,
          promotion_email: res.preferences.promotion_email,
          promotion_push: res.preferences.promotion_push,
          system_in_app: res.preferences.system_in_app,
          system_email: res.preferences.system_email,
          system_push: res.preferences.system_push,
        });
      }
      setNotifFeedback({ ok: true, message: "Đã lưu cài đặt thông báo." });
      showToast("Đã lưu cài đặt thông báo.");
    } catch (err) {
      setNotifFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa thể lưu cài đặt thông báo lúc này.",
      });
    } finally {
      setSavingNotifPrefs(false);
    }
  };

  const productById = useMemo(() => {
    const map = new Map<string, Product>();
    for (const p of catalogProducts) {
      map.set(p.id, p);
      if (p.backendId) map.set(p.backendId, p);
    }
    return map;
  }, [catalogProducts]);

  const recentOrders = useMemo(() => initialOrders.slice(0, 4), [initialOrders]);

  const wishlistedProducts = useMemo(
    () =>
      wishlist
        .map((entry) => {
          const prod = productById.get(entry.productId);
          return prod ? { entry, prod } : null;
        })
        .filter((x): x is NonNullable<typeof x> => x !== null),
    [wishlist, productById]
  );

  const recentlyViewedProducts = useMemo(
    () =>
      recentlyViewed
        .map((id) => productById.get(id))
        .filter((p): p is NonNullable<typeof p> => Boolean(p))
        .slice(0, 4),
    [recentlyViewed, productById]
  );

  const handleSaveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fullNameDraft.trim();
    const cleanPhone = phoneDraft.replace(/\s+/g, "").trim();

    if (cleanName.length < 2) {
      setProfileError("Nhập họ và tên tối thiểu 2 ký tự.");
      return;
    }
    if (cleanPhone && !/^0\d{9,10}$/.test(cleanPhone)) {
      setProfileError("Số điện thoại cần 10 hoặc 11 chữ số, bắt đầu bằng 0.");
      return;
    }

    setProfileError(null);
    setSavingProfile(true);
    try {
      const res = await api<{ user: SessionUser }>("/api/auth/me", {
        method: "PATCH",
        body: { fullName: cleanName, phone: cleanPhone },
      });
      setProfile((prev) => ({
        ...prev,
        fullName: res.user.fullName || cleanName,
        phone: res.user.phone || cleanPhone,
      }));
      setUser(res.user);
      setEditingProfile(false);
      showToast("Đã cập nhật hồ sơ.");
      router.refresh();
    } catch (err) {
      setProfileError(err instanceof ApiError ? err.message : "Chưa cập nhật được lúc này.");
    } finally {
      setSavingProfile(false);
    }
  };

  const handleSetDefaultAddress = async (addr: AddressView) => {
    setAddressError(null);
    setBusyAddressId(addr.id);
    try {
      await api<{ address: { id: string } }>(`/api/auth/addresses/${encodeURIComponent(addr.id)}`, {
        method: "PUT",
        body: {
          recipientName: addr.recipientName,
          recipientPhone: addr.phone,
          provinceCode: addr.provinceCode,
          provinceName: addr.provinceName,
          wardCode: addr.wardCode,
          wardName: addr.wardName,
          streetAddress: addr.streetDetail,
          isDefault: true,
        },
      });
      setAddresses((prev) => prev.map((item) => ({ ...item, isDefault: item.id === addr.id })));
      showToast("Đã đặt làm địa chỉ mặc định.");
      router.refresh();
    } catch (err) {
      setAddressError(
        err instanceof ApiError ? err.message : "Chưa cập nhật được địa chỉ mặc định."
      );
    } finally {
      setBusyAddressId(null);
    }
  };

  const handleDeleteAddress = async (addrId: string) => {
    setAddressError(null);
    setBusyAddressId(addrId);
    try {
      await api<{ ok: boolean }>(`/api/auth/addresses/${encodeURIComponent(addrId)}`, {
        method: "DELETE",
      });
      setAddresses((prev) => prev.filter((item) => item.id !== addrId));
      showToast("Đã xóa địa chỉ.");
      router.refresh();
    } catch (err) {
      setAddressError(err instanceof ApiError ? err.message : "Chưa xóa được địa chỉ.");
    } finally {
      setBusyAddressId(null);
    }
  };

  const handleSaveVoucher = async (voucher: Voucher) => {
    setSavingVoucherCode(voucher.code);
    const res = await saveVoucher(voucher.id ?? voucher.code);
    setSavingVoucherCode(null);
    if (res.ok) {
      setVouchers((prev) => prev.map((v) => (v.code === voucher.code ? { ...v, saved: true } : v)));
    }
  };

  const handleApplyVoucher = (voucher: Voucher) => {
    setSelectedVoucher(voucher.id ?? voucher.code);
    showToast(`Đã chọn mã ${voucher.code}.`);
    router.push("/gio-hang");
  };

  const handleAddWishlistToCart = async (prod: Product) => {
    const defaultVariant =
      prod.skus.find((s) => s.size === "L" && s.stock > 0 && s.skuId) ??
      prod.skus.find((s) => s.stock > 0 && s.skuId);
    if (!defaultVariant?.skuId) {
      router.push(`/san-pham/${prod.slug}`);
      return;
    }
    const res = await addToCart({
      skuId: defaultVariant.skuId,
      quantity: 1,
      productName: prod.name,
      size: defaultVariant.size,
    });
    if (res.unauthenticated) {
      router.push("/dang-nhap?next=%2Ftai-khoan%3Ftab%3Dyeu-thich");
    }
  };

  if (viewState === "loading") {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
        <header className="mx-auto max-w-[960px] border-b border-border pb-4">
          <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground sm:text-2xl sm:leading-8">
            Tài khoản
          </h1>
        </header>
        <AccountLoadingSkeleton />
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
        <header className="mx-auto max-w-[960px] border-b border-border pb-4">
          <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground sm:text-2xl sm:leading-8">
            Tài khoản
          </h1>
        </header>
        <div className="mx-auto max-w-[960px]">
          <ErrorState
            title="Chưa thể tải hồ sơ tài khoản."
            description={initialError ?? "Thử lại sau ít giây."}
            retryHref="/tai-khoan"
            className="border border-border shadow-none"
          />
        </div>
      </div>
    );
  }

  return (
    <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
      <div className="mx-auto max-w-[960px] space-y-6">
        <header className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-end sm:justify-between">
          <div className="min-w-0 space-y-1">
            <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground sm:text-2xl sm:leading-8">
              Tài khoản
            </h1>
            <p className="truncate text-[13px] leading-[18px] text-muted-foreground">
              {profile.fullName || profile.email} · {brandName}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" asChild className="min-h-10 shadow-none sm:min-h-9">
              <Link href="/don-hang">
                <Package className="size-4" aria-hidden="true" />
                <span>Đơn hàng</span>
                <Badge variant="secondary" className="ml-0.5 px-1.5 py-0 text-[11px] tabular-nums">
                  {totalOrders}
                </Badge>
              </Link>
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => void logout("/dang-nhap")}
              className="min-h-10 text-destructive hover:text-destructive sm:min-h-9"
            >
              <LogOut className="size-4" aria-hidden="true" />
              <span>Đăng xuất</span>
            </Button>
          </div>
        </header>

        <Tabs value={activeTab} onValueChange={handleTabChange} className="gap-6">
          <div className="-mx-1 overflow-x-auto px-1">
            <TabsList
              variant="line"
              aria-label="Danh mục tài khoản"
              className="inline-flex h-auto w-max min-w-full justify-start gap-1 border-b border-border"
            >
              {ACCOUNT_TABS.map((tab) => {
                const count =
                  tab.key === "dia-chi"
                    ? addresses.length
                    : tab.key === "voucher"
                      ? vouchers.length
                      : tab.key === "yeu-thich"
                        ? wishlistedProducts.length
                        : null;
                return (
                  <TabsTrigger
                    key={tab.key}
                    value={tab.key}
                    className="min-h-10 px-3 py-2 text-sm sm:min-h-9"
                  >
                    <span>{tab.label}</span>
                    {count !== null && (
                      <span className="ml-1 rounded-full bg-muted px-1.5 py-0.5 text-[11px] leading-none text-muted-foreground tabular-nums">
                        {count}
                      </span>
                    )}
                  </TabsTrigger>
                );
              })}
            </TabsList>
          </div>

          {/* Tab 1: Hồ sơ (A5 grouped rows) */}
          <TabsContent value="tong-quan" className="space-y-6">
            <AccountSettingsRow
              id="thong-tin-ho-so"
              title="Hồ sơ"
              description="Thông tin liên hệ được dùng khi đặt hàng và nhận thông báo giao vận."
            >
              <Card className="border border-border shadow-none">
                <CardHeader className="border-b border-border pb-4">
                  <CardTitle>Thông tin tài khoản</CardTitle>
                  <CardAction>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="min-h-10 shadow-none sm:min-h-8"
                      onClick={() => {
                        setFullNameDraft(profile.fullName);
                        setPhoneDraft(profile.phone);
                        setProfileError(null);
                        setEditingProfile((v) => !v);
                      }}
                    >
                      {editingProfile ? "Đóng" : "Sửa"}
                    </Button>
                  </CardAction>
                </CardHeader>
                <CardContent>
                  {editingProfile ? (
                    <form onSubmit={handleSaveProfile} noValidate className="space-y-4">
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <Field data-invalid={Boolean(profileError) || undefined}>
                          <FieldLabel htmlFor="acc-profile-name">Họ và tên</FieldLabel>
                          <Input
                            id="acc-profile-name"
                            type="text"
                            autoComplete="name"
                            aria-describedby={profileError ? "acc-profile-err" : undefined}
                            value={fullNameDraft}
                            onChange={(e) => setFullNameDraft(e.target.value)}
                            disabled={savingProfile}
                            required
                            className="h-10 bg-background"
                          />
                        </Field>
                        <Field data-invalid={Boolean(profileError) || undefined}>
                          <FieldLabel htmlFor="acc-profile-phone">Số điện thoại</FieldLabel>
                          <Input
                            id="acc-profile-phone"
                            type="tel"
                            autoComplete="tel"
                            aria-describedby={profileError ? "acc-profile-err" : undefined}
                            value={phoneDraft}
                            onChange={(e) => setPhoneDraft(e.target.value)}
                            placeholder="0912 486 305"
                            disabled={savingProfile}
                            className="h-10 bg-background"
                          />
                        </Field>
                      </div>
                      {profileError && <FieldError id="acc-profile-err">{profileError}</FieldError>}
                      <div className="flex flex-wrap items-center gap-2">
                        <Button
                          type="submit"
                          size="sm"
                          disabled={savingProfile}
                          className="min-h-10 shadow-none sm:min-h-9"
                        >
                          {savingProfile ? "Đang lưu..." : "Lưu thay đổi"}
                        </Button>
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          onClick={() => setEditingProfile(false)}
                          className="min-h-10 sm:min-h-9"
                        >
                          Hủy
                        </Button>
                      </div>
                    </form>
                  ) : (
                    <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <div>
                        <dt className="text-xs font-medium text-muted-foreground">Họ và tên</dt>
                        <dd className="mt-1 text-sm font-medium text-foreground">
                          {profile.fullName || "Chưa cập nhật"}
                        </dd>
                      </div>
                      <div>
                        <dt className="text-xs font-medium text-muted-foreground">Email</dt>
                        <dd className="mt-1 text-sm font-medium break-all text-foreground">
                          {profile.email}
                        </dd>
                      </div>
                      <div>
                        <dt className="text-xs font-medium text-muted-foreground">Số điện thoại</dt>
                        <dd className="mt-1 text-sm font-medium text-foreground tabular-nums">
                          {profile.phone || "Chưa cập nhật"}
                        </dd>
                      </div>
                      <div>
                        <dt className="text-xs font-medium text-muted-foreground">Tham gia</dt>
                        <dd className="mt-1 text-sm font-medium text-foreground tabular-nums">
                          {profile.memberSince ? formatDateVi(profile.memberSince) : "Đã kích hoạt"}
                        </dd>
                      </div>
                    </dl>
                  )}
                </CardContent>
              </Card>
            </AccountSettingsRow>

            <Separator />

            <AccountSettingsRow
              id="xac-minh-sdt"
              title="Xác minh số điện thoại"
              description="Cần thiết khi thanh toán khi nhận hàng (COD) và nhận thông báo giao vận."
              badge={
                <StatusBadge tone={profile.phoneVerified ? "success" : "warning"}>
                  {profile.phoneVerified ? "Đã xác minh" : "Chưa xác minh"}
                </StatusBadge>
              }
            >
              <Card className="border border-border shadow-none">
                <CardContent className="space-y-4">
                  {profile.phoneVerified ? (
                    <p className="text-sm leading-5 text-foreground">
                      Số điện thoại <strong className="tabular-nums">{profile.phone}</strong> đã
                      được xác minh. Tài khoản đủ điều kiện xác thực khi đặt hàng thanh toán khi
                      nhận hàng (COD).
                    </p>
                  ) : (
                    <form onSubmit={handleVerifyPhoneOtp} noValidate className="space-y-4">
                      <p className="text-sm leading-5 text-muted-foreground">
                        Xác minh số điện thoại qua mã OTP 6 chữ số để bật thanh toán khi nhận hàng
                        (COD) và nhận thông báo giao vận (SMS mô phỏng, xem trong email tài khoản).
                      </p>

                      <Field>
                        <FieldLabel htmlFor="acc-otp-phone">Số điện thoại nhận mã OTP</FieldLabel>
                        <div className="flex flex-col gap-2 sm:flex-row">
                          <Input
                            id="acc-otp-phone"
                            type="tel"
                            autoComplete="tel"
                            value={otpPhoneInput}
                            onChange={(e) => setOtpPhoneInput(e.target.value)}
                            placeholder="0912345678"
                            disabled={sendingOtp || verifyingOtp}
                            className="h-10 bg-background"
                          />
                          <Button
                            type="button"
                            variant="secondary"
                            disabled={sendingOtp || otpCooldown > 0 || verifyingOtp}
                            onClick={() => void handleRequestPhoneOtp()}
                            className="min-h-10 shrink-0"
                          >
                            {sendingOtp
                              ? "Đang gửi..."
                              : otpCooldown > 0
                                ? `Gửi lại (${otpCooldown}s)`
                                : "Gửi mã OTP"}
                          </Button>
                        </div>
                      </Field>

                      {otpSent && (
                        <Field>
                          <FieldLabel htmlFor="acc-otp-code">
                            Mã OTP 6 chữ số
                            {otpExpiresSec > 0 && (
                              <span className="font-normal text-muted-foreground tabular-nums">
                                {" "}
                                (còn {otpExpiresSec}s)
                              </span>
                            )}
                          </FieldLabel>
                          <div className="flex flex-col gap-2 sm:flex-row">
                            <Input
                              id="acc-otp-code"
                              type="text"
                              inputMode="numeric"
                              autoComplete="one-time-code"
                              maxLength={6}
                              value={otpCodeInput}
                              onChange={(e) =>
                                setOtpCodeInput(e.target.value.replace(/\D/g, "").slice(0, 6))
                              }
                              placeholder="Nhập 6 chữ số"
                              disabled={verifyingOtp}
                              className="h-10 bg-background tabular-nums"
                            />
                            <Button
                              type="submit"
                              disabled={verifyingOtp}
                              className="min-h-10 shrink-0 shadow-none"
                            >
                              {verifyingOtp ? "Đang kiểm tra..." : "Xác minh SĐT"}
                            </Button>
                          </div>
                        </Field>
                      )}
                    </form>
                  )}

                  {otpFeedback && (
                    <Alert
                      variant={otpFeedback.ok ? "default" : "destructive"}
                      className="shadow-none"
                    >
                      <AlertDescription>{otpFeedback.message}</AlertDescription>
                    </Alert>
                  )}
                </CardContent>
              </Card>
            </AccountSettingsRow>

            <Separator />

            <AccountSettingsRow
              id="cai-dat-thong-bao"
              title="Cài đặt thông báo"
              description="Chọn kênh nhận cập nhật về đơn hàng, ưu đãi và bảo mật tài khoản."
            >
              <Card className="border border-border shadow-none">
                <CardContent>
                  <form onSubmit={handleSaveNotificationPrefs} className="space-y-4">
                    <div className="divide-y divide-border">
                      {(
                        [
                          {
                            title: "Đơn hàng & giao vận",
                            inApp: "order_in_app",
                            email: "order_email",
                            push: "order_push",
                          },
                          {
                            title: "Khuyến mãi & mã giảm giá",
                            inApp: "promotion_in_app",
                            email: "promotion_email",
                            push: "promotion_push",
                          },
                          {
                            title: "Hệ thống & bảo mật",
                            inApp: "system_in_app",
                            email: "system_email",
                            push: "system_push",
                          },
                        ] as const
                      ).map((row) => (
                        <div key={row.title} className="space-y-2.5 py-3 first:pt-0 last:pb-0">
                          <p className="text-sm font-medium text-foreground">{row.title}</p>
                          <div className="flex flex-wrap gap-x-6 gap-y-2">
                            <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm text-foreground">
                              <Checkbox
                                checked={notifPrefs[row.inApp]}
                                disabled={savingNotifPrefs}
                                onCheckedChange={(checked) =>
                                  setNotifPrefs((prev) => ({
                                    ...prev,
                                    [row.inApp]: checked === true,
                                  }))
                                }
                              />
                              <span>Trong ứng dụng</span>
                            </label>
                            <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm text-foreground">
                              <Checkbox
                                checked={notifPrefs[row.email]}
                                disabled={savingNotifPrefs}
                                onCheckedChange={(checked) =>
                                  setNotifPrefs((prev) => ({
                                    ...prev,
                                    [row.email]: checked === true,
                                  }))
                                }
                              />
                              <span>Email</span>
                            </label>
                            <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm text-foreground">
                              <Checkbox
                                checked={notifPrefs[row.push]}
                                disabled={savingNotifPrefs}
                                onCheckedChange={(checked) =>
                                  setNotifPrefs((prev) => ({
                                    ...prev,
                                    [row.push]: checked === true,
                                  }))
                                }
                              />
                              <span>Thông báo đẩy</span>
                            </label>
                          </div>
                        </div>
                      ))}
                    </div>

                    {notifFeedback && (
                      <Alert
                        variant={notifFeedback.ok ? "default" : "destructive"}
                        className="shadow-none"
                      >
                        <AlertDescription>{notifFeedback.message}</AlertDescription>
                      </Alert>
                    )}

                    <Button
                      type="submit"
                      variant="secondary"
                      size="sm"
                      disabled={savingNotifPrefs}
                      className="min-h-10 sm:min-h-9"
                    >
                      {savingNotifPrefs ? "Đang lưu..." : "Lưu cài đặt thông báo"}
                    </Button>
                  </form>
                </CardContent>
              </Card>
            </AccountSettingsRow>

            <Separator />

            <AccountSettingsRow
              id="don-gan-day"
              title="Đơn gần đây"
              description={`Tổng cộng ${totalOrders} đơn hàng đã đặt tại ${brandName}.`}
              action={
                <Link
                  href="/don-hang"
                  className="text-xs font-medium text-brand hover:underline tabular-nums"
                >
                  Xem tất cả ({totalOrders})
                </Link>
              }
            >
              {recentOrders.length === 0 ? (
                <EmptyState
                  icon={Package}
                  title="Chưa có đơn hàng nào."
                  description="Bắt đầu mua sắm để theo dõi tiến trình giao hàng tại đây."
                  action={
                    <Button asChild size="sm" className="min-h-10 shadow-none sm:min-h-9">
                      <Link href="/san-pham">Xem sản phẩm</Link>
                    </Button>
                  }
                  className="border border-border shadow-none"
                />
              ) : (
                <Card className="border border-border py-2 shadow-none">
                  <CardContent className="divide-y divide-border px-4 md:px-5">
                    {recentOrders.map((order) => {
                      const meta = getOrderStatusInfo(order.backendStatus);
                      const firstItem = order.items[0];
                      return (
                        <Link
                          key={order.id}
                          href={`/don-hang/${order.id}`}
                          className="flex items-center gap-3 py-3 transition-colors hover:opacity-85"
                        >
                          <img
                            src={firstItem?.image || "/products/ao-thun-cotton-trang.png"}
                            alt={firstItem?.productName ?? order.orderCode}
                            width={896}
                            height={1200}
                            loading="lazy"
                            className="h-14 w-11 shrink-0 rounded-md border border-border bg-muted object-cover"
                          />
                          <div className="min-w-0 flex-1 space-y-0.5">
                            <p className="truncate text-sm font-semibold text-foreground tabular-nums">
                              {order.orderCode}
                            </p>
                            <p className="text-xs text-muted-foreground tabular-nums">
                              {order.items.length} sản phẩm
                              {order.createdAt ? ` · ${formatDateVi(order.createdAt)}` : ""}
                            </p>
                          </div>
                          <div className="flex shrink-0 flex-col items-end gap-1">
                            <Money vnd={order.total} size="sm" />
                            <StatusBadge tone={orderStatusTone(order.backendStatus)}>
                              {meta.label}
                            </StatusBadge>
                          </div>
                        </Link>
                      );
                    })}
                  </CardContent>
                </Card>
              )}
            </AccountSettingsRow>
          </TabsContent>

          {/* Tab 2: Sổ địa chỉ (A5 + Sheet) */}
          <TabsContent value="dia-chi" className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 className="text-base font-semibold text-foreground">Sổ địa chỉ</h2>
                <p className="text-[13px] text-muted-foreground">
                  Quản lý địa chỉ nhận hàng đã lưu để điền nhanh khi thanh toán.
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                className="min-h-10 shadow-none sm:min-h-9"
                onClick={() => {
                  setEditingAddress(null);
                  setAddressSheetMode("add");
                }}
              >
                <Plus className="size-4" aria-hidden="true" />
                <span>Thêm địa chỉ</span>
              </Button>
            </div>

            {addressError && (
              <Alert variant="destructive" className="shadow-none">
                <AlertDescription>{addressError}</AlertDescription>
              </Alert>
            )}

            {addresses.length === 0 ? (
              <EmptyState
                icon={MapPin}
                title="Chưa có địa chỉ nhận hàng."
                description="Thêm địa chỉ giao hàng mặc định để tiết kiệm thời gian khi đặt đơn."
                action={
                  <Button
                    type="button"
                    size="sm"
                    className="min-h-10 shadow-none sm:min-h-9"
                    onClick={() => {
                      setEditingAddress(null);
                      setAddressSheetMode("add");
                    }}
                  >
                    Thêm địa chỉ
                  </Button>
                }
                className="border border-border shadow-none"
              />
            ) : (
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                {addresses.map((addr) => {
                  const previewProv = getProvinceByCode(
                    toStorefrontProvinceCode(addr.provinceCode)
                  );
                  const isBusy = busyAddressId === addr.id;
                  return (
                    <Card
                      key={addr.id}
                      className={cn(
                        "justify-between border bg-card shadow-none",
                        addr.isDefault ? "border-brand" : "border-border"
                      )}
                    >
                      <CardContent className="space-y-2">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-baseline gap-2">
                            <span className="text-sm font-semibold text-foreground">
                              {addr.recipientName}
                            </span>
                            <span className="text-xs text-muted-foreground tabular-nums">
                              {addr.phone}
                            </span>
                          </div>
                          {addr.isDefault && <StatusBadge tone="success">Mặc định</StatusBadge>}
                        </div>
                        <p className="text-sm leading-5 text-foreground">
                          {formatAddressLine(addr.streetDetail, addr.wardName, addr.provinceName)}
                        </p>
                        <p className="text-xs text-muted-foreground">
                          Dự kiến {previewProv?.etaDays ?? "1–3 ngày"}
                        </p>

                        <div className="flex flex-wrap items-center gap-2 pt-2">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={isBusy}
                            className="min-h-10 shadow-none sm:min-h-8"
                            onClick={() => {
                              setEditingAddress(addr);
                              setAddressSheetMode("edit");
                            }}
                          >
                            Sửa
                          </Button>
                          {!addr.isDefault && (
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              disabled={isBusy}
                              className="min-h-10 sm:min-h-8"
                              onClick={() => void handleSetDefaultAddress(addr)}
                            >
                              Đặt mặc định
                            </Button>
                          )}
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={isBusy}
                            className="min-h-10 text-destructive hover:text-destructive sm:min-h-8"
                            onClick={() => void handleDeleteAddress(addr.id)}
                          >
                            Xóa
                          </Button>
                        </div>
                      </CardContent>
                    </Card>
                  );
                })}
              </div>
            )}
          </TabsContent>

          {/* Tab 3: Mã giảm giá */}
          <TabsContent value="voucher" className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <h2 className="text-base font-semibold text-foreground">Mã giảm giá</h2>
                <p className="text-[13px] text-muted-foreground">
                  Ưu đãi riêng của {brandName}, áp dụng trực tiếp tại giỏ hàng.
                </p>
              </div>
              <Badge variant="secondary" className="tabular-nums">
                {vouchers.length} mã
              </Badge>
            </div>

            {vouchers.length === 0 ? (
              <EmptyState
                icon={Ticket}
                title="Chưa có mã ưu đãi khả dụng."
                description={`Theo dõi trang ưu đãi của ${brandName} để lưu mã giảm giá mới nhất.`}
                action={
                  <Button asChild size="sm" className="min-h-10 shadow-none sm:min-h-9">
                    <Link href="/san-pham">Xem sản phẩm</Link>
                  </Button>
                }
                className="border border-border shadow-none"
              />
            ) : (
              <div className="space-y-3">
                {vouchers.map((voucher) => {
                  const isSaved = Boolean(voucher.saved);
                  const remaining = voucher.remainingUses ?? 0;
                  const exhausted = remaining <= 0;
                  return (
                    <Card key={voucher.code} className="border border-border py-4 shadow-none">
                      <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                        <div className="min-w-0 space-y-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="rounded-md border border-border bg-background px-2 py-0.5 font-mono text-xs font-semibold text-foreground">
                              {voucher.code}
                            </span>
                            {isSaved && <StatusBadge tone="success">Đã lưu</StatusBadge>}
                            {exhausted && <StatusBadge tone="critical">Hết lượt</StatusBadge>}
                          </div>
                          <p className="text-sm font-medium text-foreground">{voucher.title}</p>
                          <p className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
                            <span>Tối thiểu {formatVnd(voucher.minOrderValue)}</span>
                            {voucher.expiresAt && (
                              <span>HSD {formatDateVi(voucher.expiresAt)}</span>
                            )}
                          </p>
                        </div>

                        <div className="flex shrink-0 flex-wrap items-center gap-2">
                          {!isSaved && !exhausted && (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              disabled={savingVoucherCode === voucher.code}
                              onClick={() => void handleSaveVoucher(voucher)}
                              className="min-h-10 shadow-none sm:min-h-8"
                            >
                              {savingVoucherCode === voucher.code ? "Đang lưu..." : "Lưu mã"}
                            </Button>
                          )}
                          <Button
                            type="button"
                            size="sm"
                            disabled={exhausted}
                            onClick={() => handleApplyVoucher(voucher)}
                            className="min-h-10 shadow-none sm:min-h-8"
                          >
                            Dùng ngay
                          </Button>
                        </div>
                      </CardContent>
                    </Card>
                  );
                })}
              </div>
            )}
          </TabsContent>

          {/* Tab 4: Yêu thích */}
          <TabsContent value="yeu-thich" className="space-y-8">
            <section className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h2 className="text-base font-semibold text-foreground">Yêu thích</h2>
                  <p className="text-[13px] text-muted-foreground">
                    Sản phẩm bạn đã đánh dấu để xem lại hoặc thêm nhanh vào giỏ.
                  </p>
                </div>
                <Link href="/san-pham" className="text-xs font-medium text-brand hover:underline">
                  Xem thêm
                </Link>
              </div>

              {wishlistedProducts.length === 0 ? (
                <EmptyState
                  icon={Heart}
                  title="Chưa có sản phẩm yêu thích."
                  description="Nhấn biểu tượng tim trên trang sản phẩm để lưu lại những mẫu bạn ưng ý."
                  action={
                    <Button asChild size="sm" className="min-h-10 shadow-none sm:min-h-9">
                      <Link href="/san-pham">Xem sản phẩm</Link>
                    </Button>
                  }
                  className="border border-border shadow-none"
                />
              ) : (
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  {wishlistedProducts.map(({ entry, prod }) => {
                    const totalStock = prod.skus.reduce((s, sk) => s + sk.stock, 0);
                    const defaultVariant =
                      prod.skus.find((s) => s.size === "L" && s.stock > 0) ??
                      prod.skus.find((s) => s.stock > 0);
                    const shot = MODEL_SHOTS[prod.id];

                    return (
                      <Card
                        key={entry.productId}
                        className="overflow-hidden border border-border py-0 shadow-none"
                      >
                        <Link
                          href={`/san-pham/${prod.slug}`}
                          className="block aspect-[4/5] w-full overflow-hidden bg-muted"
                        >
                          <img
                            src={shot?.src ?? prod.image}
                            alt={prod.name}
                            width={896}
                            height={1200}
                            loading="lazy"
                            className="size-full object-cover object-top"
                          />
                        </Link>
                        <CardContent className="space-y-2.5 p-4">
                          <Link
                            href={`/san-pham/${prod.slug}`}
                            className="line-clamp-2 text-sm font-semibold text-foreground hover:text-brand hover:underline"
                          >
                            {prod.name}
                          </Link>
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <Money vnd={prod.price} compareAtVnd={prod.originalPrice} size="sm" />
                            {totalStock === 0 ? (
                              <StatusBadge tone="critical">Hết hàng</StatusBadge>
                            ) : totalStock <= 5 ? (
                              <StatusBadge tone="warning">Còn {totalStock}</StatusBadge>
                            ) : null}
                          </div>
                          <div className="flex flex-wrap items-center gap-2 pt-1">
                            {defaultVariant && (
                              <Button
                                type="button"
                                size="sm"
                                className="min-h-10 flex-1 shadow-none sm:min-h-8"
                                onClick={() => void handleAddWishlistToCart(prod)}
                              >
                                Thêm ({defaultVariant.size})
                              </Button>
                            )}
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              className="min-h-10 shadow-none sm:min-h-8"
                              onClick={() => toggleWishlist(prod.id)}
                            >
                              Bỏ
                            </Button>
                          </div>
                        </CardContent>
                      </Card>
                    );
                  })}
                </div>
              )}
            </section>

            {recentlyViewedProducts.length > 0 && (
              <section className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h2 className="text-base font-semibold text-foreground">Đã xem gần đây</h2>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={clearRecentlyViewed}
                    className="min-h-10 sm:min-h-8"
                  >
                    Xóa lịch sử
                  </Button>
                </div>
                <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
                  {recentlyViewedProducts.map((product, index) => (
                    <SiteProductCard key={product.id} product={product} index={index} />
                  ))}
                </div>
              </section>
            )}
          </TabsContent>
        </Tabs>
      </div>

      {/* Radix Sheet for adding or editing an address */}
      <Sheet
        open={addressSheetMode !== "closed"}
        onOpenChange={(open) => {
          if (!open) {
            setAddressSheetMode("closed");
            setEditingAddress(null);
          }
        }}
      >
        <SheetContent side="right" className="w-full gap-0 p-0 sm:max-w-xl">
          <SheetHeader className="border-b border-border px-5 py-4">
            <SheetTitle>{addressSheetMode === "edit" ? "Sửa địa chỉ" : "Thêm địa chỉ"}</SheetTitle>
            <SheetDescription>
              Địa chỉ mới sẽ được lưu vào sổ địa chỉ để dùng nhanh khi thanh toán.
            </SheetDescription>
          </SheetHeader>
          <div className="flex-1 overflow-y-auto px-5 py-5">
            {addressSheetMode !== "closed" && (
              <AccountAddressForm
                key={editingAddress?.id ?? "new"}
                defaultRecipientName={profile.fullName}
                defaultPhone={profile.phone}
                provinces={provinces}
                initialProvinceCode={initialProvinceCode}
                initialWards={initialWards}
                editingAddress={editingAddress}
                onSaved={(saved) => {
                  setAddresses((prev) => {
                    const withoutCurrent = prev
                      .filter((a) => a.id !== saved.id)
                      .map((a) => (saved.isDefault ? { ...a, isDefault: false } : a));
                    return [saved, ...withoutCurrent];
                  });
                  setAddressSheetMode("closed");
                  setEditingAddress(null);
                  showToast(
                    addressSheetMode === "edit"
                      ? "Đã cập nhật địa chỉ."
                      : `Đã lưu địa chỉ tại ${saved.wardName}, ${saved.provinceName}.`
                  );
                  router.refresh();
                }}
                onCancel={() => {
                  setAddressSheetMode("closed");
                  setEditingAddress(null);
                }}
              />
            )}
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}

export function AccountClientView(props: AccountClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
          <AccountLoadingSkeleton />
        </div>
      }
    >
      <AccountClientInner {...props} />
    </Suspense>
  );
}
