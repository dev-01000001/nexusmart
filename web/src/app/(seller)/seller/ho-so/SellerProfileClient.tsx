"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { AlertTriangle, ArrowUpRight, Check, Info, RefreshCw, Store } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  PageHeader,
  SaveBar,
  StatusBadge,
  siteStatusTone,
  type StatusTone,
} from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { SessionUser } from "@/lib/server/session";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { cn } from "@/lib/utils";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { useSellerContext } from "@/features/seller/SellerShell";
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

type ProfileTab = "shop" | "shipping" | "warehouse" | "autoreply";

interface ShippingSnapshot {
  enabledCarriers: string[];
  defaultWeightStr: string;
  defaultLengthStr: string;
  defaultWidthStr: string;
  defaultHeightStr: string;
  freeShipEnabled: boolean;
  freeShipMinOrderStr: string;
  freeShipMaxStr: string;
}

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

function shopStatusTone(status?: string | null): StatusTone {
  const s = (status ?? "").trim().toUpperCase();
  if (s === "ACTIVE" || s === "APPROVED") return "success";
  if (s === "PENDING" || s === "PENDING_REVIEW") return "attention";
  if (s === "SUSPENDED" || s === "REJECTED") return "critical";
  return "neutral";
}

function siteStateLabel(state: SellerSiteSummary["state"]): {
  label: string;
  tone: StatusTone;
} {
  switch (state) {
    case "published":
      return { label: "Đang mở bán", tone: siteStatusTone("PUBLISHED") };
    case "draft":
      return { label: "Bản nháp", tone: siteStatusTone("DRAFT") };
    case "suspended":
      return { label: "Đang tạm khóa", tone: siteStatusTone("SUSPENDED") };
    default:
      return { label: "Chưa khởi tạo", tone: "neutral" };
  }
}

function buildShippingSnapshot(settings: GwShopShippingSettings | null): ShippingSnapshot {
  return {
    enabledCarriers: settings?.enabled_carrier_codes?.length
      ? settings.enabled_carrier_codes
      : ["ghn", "mock_express"],
    defaultWeightStr: String(settings?.default_weight_grams || 500),
    defaultLengthStr: String(settings?.default_length_cm || 25),
    defaultWidthStr: String(settings?.default_width_cm || 20),
    defaultHeightStr: String(settings?.default_height_cm || 10),
    freeShipEnabled: Boolean(settings?.free_ship_enabled),
    freeShipMinOrderStr: String(settings?.free_ship_min_order_vnd ?? 199000),
    freeShipMaxStr: String(settings?.free_ship_max_vnd ?? 25000),
  };
}

function sameCarriers(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const sa = [...a].sort();
  const sb = [...b].sort();
  return sa.every((v, idx) => v === sb[idx]);
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
  const initialShipSnap = useMemo(
    () => buildShippingSnapshot(initialShippingSettings),
    [initialShippingSettings]
  );
  const [savedShipping, setSavedShipping] = useState<ShippingSnapshot>(initialShipSnap);
  const [shipSettingsLoading, setShipSettingsLoading] = useState(false);
  const [shipSettingsSaving, setShipSettingsSaving] = useState(false);
  const [shipSettingsError, setShipSettingsError] = useState<string | null>(shippingSettingsError);
  const [shipSettingsUpdatedAt, setShipSettingsUpdatedAt] = useState<string | undefined>(
    initialShippingSettings?.updated_at
  );
  const [enabledCarriers, setEnabledCarriers] = useState<string[]>(initialShipSnap.enabledCarriers);
  const [defaultWeightStr, setDefaultWeightStr] = useState(initialShipSnap.defaultWeightStr);
  const [defaultLengthStr, setDefaultLengthStr] = useState(initialShipSnap.defaultLengthStr);
  const [defaultWidthStr, setDefaultWidthStr] = useState(initialShipSnap.defaultWidthStr);
  const [defaultHeightStr, setDefaultHeightStr] = useState(initialShipSnap.defaultHeightStr);
  const [freeShipEnabled, setFreeShipEnabled] = useState(initialShipSnap.freeShipEnabled);
  const [freeShipMinOrderStr, setFreeShipMinOrderStr] = useState(
    initialShipSnap.freeShipMinOrderStr
  );
  const [freeShipMaxStr, setFreeShipMaxStr] = useState(initialShipSnap.freeShipMaxStr);

  // Auto-reply state
  const [autoReplyLoaded, setAutoReplyLoaded] = useState(false);
  const [autoReplyLoading, setAutoReplyLoading] = useState(false);
  const [autoReplyUnavailable, setAutoReplyUnavailable] = useState(false);
  const [savedAutoReply, setSavedAutoReply] = useState<{ enabled: boolean; message: string }>({
    enabled: false,
    message: "",
  });
  const [autoReplyEnabled, setAutoReplyEnabled] = useState(false);
  const [autoReplyMessage, setAutoReplyMessage] = useState("");
  const [autoReplyUpdatedAt, setAutoReplyUpdatedAt] = useState("");
  const [autoReplySaving, setAutoReplySaving] = useState(false);
  const [autoReplyError, setAutoReplyError] = useState("");

  const applyShippingSettings = useCallback((settings: GwShopShippingSettings) => {
    const snap: ShippingSnapshot = {
      enabledCarriers: settings.enabled_carrier_codes?.length
        ? settings.enabled_carrier_codes
        : ["ghn", "mock_express"],
      defaultWeightStr: String(settings.default_weight_grams || 500),
      defaultLengthStr: String(settings.default_length_cm || 25),
      defaultWidthStr: String(settings.default_width_cm || 20),
      defaultHeightStr: String(settings.default_height_cm || 10),
      freeShipEnabled: Boolean(settings.free_ship_enabled),
      freeShipMinOrderStr: String(settings.free_ship_min_order_vnd ?? 0),
      freeShipMaxStr: String(settings.free_ship_max_vnd ?? 0),
    };
    setSavedShipping(snap);
    setEnabledCarriers(snap.enabledCarriers);
    setDefaultWeightStr(snap.defaultWeightStr);
    setDefaultLengthStr(snap.defaultLengthStr);
    setDefaultWidthStr(snap.defaultWidthStr);
    setDefaultHeightStr(snap.defaultHeightStr);
    setFreeShipEnabled(snap.freeShipEnabled);
    setFreeShipMinOrderStr(snap.freeShipMinOrderStr);
    setFreeShipMaxStr(snap.freeShipMaxStr);
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
      setSavedAutoReply({ enabled: normalized.enabled, message: normalized.message });
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

  const submitShippingSettings = async () => {
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
      } else {
        setSavedShipping({
          enabledCarriers: [...enabledCarriers],
          defaultWeightStr: String(weight),
          defaultLengthStr: String(length),
          defaultWidthStr: String(width),
          defaultHeightStr: String(height),
          freeShipEnabled,
          freeShipMinOrderStr: String(freeshipParsed.minOrderVnd),
          freeShipMaxStr: String(freeshipParsed.maxVnd),
        });
      }
      showToast("Đã lưu cài đặt vận chuyển và Freeship gian hàng.");
    } catch (err) {
      setShipSettingsError(getErrorMessage(err, "Không thể lưu cài đặt vận chuyển lúc này."));
    } finally {
      setShipSettingsSaving(false);
    }
  };

  const handleSaveShippingSettings = async (e: React.FormEvent) => {
    e.preventDefault();
    await submitShippingSettings();
  };

  const shopDisplayName = shop?.name || "Gian hàng";
  const shopStatusMeta = shop ? getShopStatusLabel(shop.status) : null;
  const siteMeta = siteStateLabel(site.state);
  const safeLicenseUrl = sanitizeSafeUrl(shop?.business_license_doc);
  const pickupAddress = addresses.find(
    (a) => shop?.pickup_address_id && a.id === shop.pickup_address_id
  );

  const submitOwnerContact = async () => {
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

  const handleSaveOwnerContact = async (e: React.FormEvent) => {
    e.preventDefault();
    await submitOwnerContact();
  };

  const submitAutoReply = async () => {
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
      const nextMsg = normalized.message || trimmedMsg;
      setSavedAutoReply({ enabled: normalized.enabled, message: nextMsg });
      setAutoReplyEnabled(normalized.enabled);
      setAutoReplyMessage(nextMsg);
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

  const handleSaveAutoReply = async (e: React.FormEvent) => {
    e.preventDefault();
    await submitAutoReply();
  };

  const shopDirty = fullName !== owner.fullName || phone !== owner.phone;
  const shippingDirty =
    !sameCarriers(enabledCarriers, savedShipping.enabledCarriers) ||
    defaultWeightStr !== savedShipping.defaultWeightStr ||
    defaultLengthStr !== savedShipping.defaultLengthStr ||
    defaultWidthStr !== savedShipping.defaultWidthStr ||
    defaultHeightStr !== savedShipping.defaultHeightStr ||
    freeShipEnabled !== savedShipping.freeShipEnabled ||
    freeShipMinOrderStr !== savedShipping.freeShipMinOrderStr ||
    freeShipMaxStr !== savedShipping.freeShipMaxStr;
  const autoReplyDirty =
    autoReplyLoaded &&
    (autoReplyEnabled !== savedAutoReply.enabled || autoReplyMessage !== savedAutoReply.message);

  const activeDirty =
    (tab === "shop" && shopDirty) ||
    (tab === "shipping" && shippingDirty) ||
    (tab === "autoreply" && autoReplyDirty);

  const activeSaving =
    (tab === "shop" && profileSaving) ||
    (tab === "shipping" && shipSettingsSaving) ||
    (tab === "autoreply" && autoReplySaving);

  const handleDiscardActiveTab = () => {
    if (tab === "shop") {
      setFullName(owner.fullName);
      setPhone(owner.phone);
      setProfileError("");
    } else if (tab === "shipping") {
      setEnabledCarriers(savedShipping.enabledCarriers);
      setDefaultWeightStr(savedShipping.defaultWeightStr);
      setDefaultLengthStr(savedShipping.defaultLengthStr);
      setDefaultWidthStr(savedShipping.defaultWidthStr);
      setDefaultHeightStr(savedShipping.defaultHeightStr);
      setFreeShipEnabled(savedShipping.freeShipEnabled);
      setFreeShipMinOrderStr(savedShipping.freeShipMinOrderStr);
      setFreeShipMaxStr(savedShipping.freeShipMaxStr);
      setShipSettingsError(null);
    } else if (tab === "autoreply") {
      setAutoReplyEnabled(savedAutoReply.enabled);
      setAutoReplyMessage(savedAutoReply.message);
      setAutoReplyError("");
    }
  };

  const handleSaveActiveTab = () => {
    if (tab === "shop") {
      void submitOwnerContact();
    } else if (tab === "shipping") {
      void submitShippingSettings();
    } else if (tab === "autoreply") {
      void submitAutoReply();
    }
  };

  return (
    <div className="mx-auto w-full max-w-[800px] space-y-6">
      <SaveBar
        visible={Boolean(!uiState && activeDirty)}
        saving={activeSaving}
        onDiscard={handleDiscardActiveTab}
        onSave={handleSaveActiveTab}
      />

      <PageHeader
        title="Hồ sơ"
        subtitle="Quản lý thông tin gian hàng, cấu hình vận chuyển, kho lấy hàng và trả lời tự động."
        primaryAction={
          <Button variant="outline" size="sm" asChild>
            <Link href="/seller/site">
              <span>Site bán hàng</span>
              <ArrowUpRight className="size-4" aria-hidden="true" />
            </Link>
          </Button>
        }
      />

      {uiState === "loading" && (
        <Card role="status" aria-label="Đang tải hồ sơ gian hàng...">
          <CardContent className="space-y-4 py-6">
            <Skeleton className="h-5 w-44" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-2/3" />
          </CardContent>
        </Card>
      )}

      {uiState === "error" && (
        <ErrorState
          title="Không thể tải hồ sơ gian hàng"
          description={shopError ?? "Hệ thống gian hàng tạm thời chưa phản hồi."}
          onRetry={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <EmptyState
          icon={Store}
          title="Chưa có dữ liệu hồ sơ gian hàng"
          description="Hoàn thiện thông tin gian hàng và kho lấy hàng để bắt đầu vận hành."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/site">Mở cấu hình site</Link>
            </Button>
          }
        />
      )}

      {!uiState && (
        <Tabs value={tab} onValueChange={(v) => setTab(v as ProfileTab)} className="space-y-6">
          <div className="-mx-1 overflow-x-auto px-1">
            <TabsList
              aria-label="Phân mục hồ sơ gian hàng"
              className="h-auto w-max min-w-full justify-start border-b border-border bg-transparent pb-2"
            >
              <TabsTrigger value="shop" className="min-h-10 md:min-h-8">
                Gian hàng
              </TabsTrigger>
              <TabsTrigger value="shipping" className="min-h-10 md:min-h-8">
                Vận chuyển &amp; Freeship
              </TabsTrigger>
              <TabsTrigger value="warehouse" className="min-h-10 gap-1.5 md:min-h-8">
                <span>Kho lấy hàng</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(addresses.length)}
                </span>
              </TabsTrigger>
              <TabsTrigger value="autoreply" className="min-h-10 md:min-h-8">
                Trả lời tự động
              </TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="shop" className="space-y-6">
            <section
              aria-labelledby="shop-info-heading"
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
            >
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <h2 id="shop-info-heading" className="text-sm font-semibold text-foreground">
                    Hồ sơ gian hàng
                  </h2>
                  {shopStatusMeta && (
                    <StatusBadge tone={shopStatusTone(shop?.status)}>
                      {shopStatusMeta.label}
                    </StatusBadge>
                  )}
                </div>
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Thông tin định danh, chứng từ kinh doanh và kho mặc định của gian hàng.
                </p>
              </div>

              <Card>
                {shopError && !shop ? (
                  <CardContent className="pt-4">
                    <ErrorState
                      title="Không thể tải thông tin gian hàng"
                      description={shopError}
                      onRetry={() => router.refresh()}
                      className="shadow-none"
                    />
                  </CardContent>
                ) : !shop ? (
                  <CardContent className="py-6 text-[13px] text-muted-foreground">
                    Tài khoản hiện chưa gắn hồ sơ gian hàng trên hệ thống.
                  </CardContent>
                ) : (
                  <>
                    <CardContent className="space-y-4 pt-4">
                      {shop.rejection_reason && (
                        <Alert variant="destructive">
                          <AlertTriangle className="size-4" aria-hidden="true" />
                          <AlertTitle>Lý do từ chối</AlertTitle>
                          <AlertDescription>{shop.rejection_reason}</AlertDescription>
                        </Alert>
                      )}

                      <dl className="divide-y divide-border text-sm">
                        <div className="flex flex-col gap-1 py-2.5 first:pt-0 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">Tên gian hàng</dt>
                          <dd className="font-medium text-foreground sm:text-right">{shop.name}</dd>
                        </div>
                        <div className="flex flex-col gap-1 py-2.5 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">Trạng thái</dt>
                          <dd className="text-foreground sm:text-right">
                            {shopStatusMeta ? shopStatusMeta.label : shop.status}
                          </dd>
                        </div>
                        <div className="flex flex-col gap-1 py-2.5 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">Số điện thoại</dt>
                          <dd className="text-foreground tabular-nums sm:text-right">
                            {shop.contact_phone || "Chưa cập nhật"}
                          </dd>
                        </div>
                        <div className="flex flex-col gap-1 py-2.5 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">Mô tả</dt>
                          <dd className="text-foreground sm:max-w-[65%] sm:text-right">
                            {shop.description || "Chưa cập nhật"}
                          </dd>
                        </div>
                        <div className="flex flex-col gap-1 py-2.5 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">
                            Giấy phép kinh doanh
                          </dt>
                          <dd className="text-foreground sm:text-right">
                            {safeLicenseUrl ? (
                              <a
                                href={safeLicenseUrl}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="font-medium text-brand underline underline-offset-4 hover:text-brand/80"
                              >
                                Xem chứng từ
                              </a>
                            ) : (
                              shop.business_license_doc || "Chưa đính kèm"
                            )}
                          </dd>
                        </div>
                        <div className="flex flex-col gap-1 py-2.5 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">Ngày khởi tạo</dt>
                          <dd className="text-foreground tabular-nums sm:text-right">
                            {shop.created_at ? formatDateTimeVi(shop.created_at) : "Chưa cập nhật"}
                          </dd>
                        </div>
                        <div className="flex flex-col gap-1 py-2.5 last:pb-0 sm:flex-row sm:justify-between sm:gap-4">
                          <dt className="text-[13px] text-muted-foreground">Kho mặc định</dt>
                          <dd className="text-foreground sm:max-w-[65%] sm:text-right">
                            {pickupAddress
                              ? `${pickupAddress.street_address}, ${pickupAddress.ward_name}, ${pickupAddress.province_name}`
                              : shop.pickup_address_id || "Chưa thiết lập"}
                          </dd>
                        </div>
                      </dl>
                    </CardContent>
                    <CardFooter className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-[13px]">
                      <span className="text-muted-foreground">
                        Chỉnh giao diện &amp; chính sách trên site riêng
                      </span>
                      <Link href="/seller/site" className="font-medium text-brand hover:underline">
                        Mở Site Studio
                      </Link>
                    </CardFooter>
                  </>
                )}
              </Card>
            </section>

            <Separator />

            <section
              aria-labelledby="owner-contact-heading"
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
            >
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <h2 id="owner-contact-heading" className="text-sm font-semibold text-foreground">
                    Chủ gian hàng
                  </h2>
                  <StatusBadge tone={owner.phoneVerified ? "success" : "neutral"}>
                    {owner.phoneVerified ? "Đã xác thực SĐT" : "Chưa xác thực SĐT"}
                  </StatusBadge>
                </div>
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Thông tin liên hệ của người phụ trách tài khoản quản trị gian hàng.
                </p>
              </div>

              <Card>
                <form onSubmit={(e) => void handleSaveOwnerContact(e)}>
                  <CardContent className="pt-4">
                    <FieldGroup className="gap-4">
                      <Field>
                        <FieldLabel htmlFor="owner-email">Email đăng nhập</FieldLabel>
                        <Input
                          id="owner-email"
                          type="email"
                          value={owner.email}
                          disabled
                          readOnly
                          className="bg-muted/60"
                        />
                      </Field>

                      <Field data-invalid={Boolean(profileError) || undefined}>
                        <FieldLabel htmlFor="owner-fullname">Họ và tên người phụ trách</FieldLabel>
                        <Input
                          id="owner-fullname"
                          type="text"
                          value={fullName}
                          onChange={(e) => {
                            setFullName(e.target.value);
                            if (profileError) setProfileError("");
                          }}
                          placeholder="Nhập họ tên chủ gian hàng"
                          maxLength={120}
                          aria-invalid={Boolean(profileError)}
                        />
                      </Field>

                      <Field>
                        <FieldLabel htmlFor="owner-phone">Số điện thoại tài khoản</FieldLabel>
                        <Input
                          id="owner-phone"
                          type="tel"
                          value={phone}
                          onChange={(e) => setPhone(e.target.value)}
                          placeholder="Ví dụ: 0901234567"
                          maxLength={20}
                        />
                      </Field>

                      {profileError ? <FieldError>{profileError}</FieldError> : null}
                    </FieldGroup>
                  </CardContent>
                  <CardFooter className="justify-end border-t border-border pt-3">
                    <Button type="submit" size="sm" disabled={profileSaving}>
                      <Check className="size-4" aria-hidden="true" />
                      <span>{profileSaving ? "Đang lưu..." : "Lưu thông tin liên hệ"}</span>
                    </Button>
                  </CardFooter>
                </form>
              </Card>
            </section>
          </TabsContent>

          <TabsContent value="shipping">
            <form
              onSubmit={(e) => void handleSaveShippingSettings(e)}
              aria-labelledby="shipping-settings-heading"
              noValidate
              className="space-y-6"
            >
              {shipSettingsLoading ? (
                <Card role="status" aria-label="Đang tải cài đặt vận chuyển...">
                  <CardContent className="space-y-3 py-6">
                    <Skeleton className="h-5 w-48" />
                    <Skeleton className="h-12 w-full" />
                    <Skeleton className="h-12 w-full" />
                  </CardContent>
                </Card>
              ) : (
                <>
                  <section className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                    <div className="space-y-2">
                      <h2
                        id="shipping-settings-heading"
                        className="text-sm font-semibold text-foreground"
                      >
                        Cài đặt vận chuyển &amp; Freeship shop
                      </h2>
                      <p className="text-[13px] leading-[18px] text-muted-foreground">
                        {shipSettingsUpdatedAt
                          ? `Cập nhật lúc ${formatDateTimeVi(shipSettingsUpdatedAt)}`
                          : "Đồng bộ với tính phí vận chuyển và tạo vận đơn."}
                      </p>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={shipSettingsLoading}
                        onClick={() => void loadShippingSettings()}
                      >
                        <RefreshCw className="size-3.5" aria-hidden="true" />
                        <span>{shipSettingsLoading ? "Đang tải..." : "Tải lại"}</span>
                      </Button>
                    </div>

                    <Card>
                      <CardHeader>
                        <CardTitle>Hãng vận chuyển cho phép (bật ít nhất 1 hãng)</CardTitle>
                      </CardHeader>
                      <CardContent className="divide-y divide-border pt-0">
                        {carrierChoices.map((car) => {
                          const checked = enabledCarriers.includes(car.code);
                          return (
                            <div
                              key={car.code}
                              className="flex min-h-12 items-center justify-between gap-4 py-3 first:pt-0 last:pb-0"
                            >
                              <div className="min-w-0">
                                <div className="text-sm font-medium text-foreground">
                                  {car.label}
                                </div>
                                <div className="text-xs text-muted-foreground">
                                  Mã hãng: {car.code} · Báo giá khi cả sàn và shop đều bật
                                </div>
                              </div>
                              <Switch
                                checked={checked}
                                onCheckedChange={(val) => toggleCarrierCode(car.code, val)}
                                aria-label={`Bật hoặc tắt hãng ${car.label}`}
                                disabled={shipSettingsSaving}
                              />
                            </div>
                          );
                        })}
                      </CardContent>
                    </Card>
                  </section>

                  <Separator />

                  <section className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                    <div className="space-y-1">
                      <h3 className="text-sm font-semibold text-foreground">
                        Quy cách kiện mặc định
                      </h3>
                      <p className="text-[13px] leading-[18px] text-muted-foreground">
                        Áp dụng tính cước khi sản phẩm chưa có khối lượng hoặc kích thước riêng.
                      </p>
                    </div>

                    <Card>
                      <CardContent className="pt-4">
                        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                          <Field>
                            <FieldLabel htmlFor="ship-def-weight">
                              Khối lượng mặc định (gram)
                            </FieldLabel>
                            <Input
                              id="ship-def-weight"
                              type="number"
                              min={1}
                              max={50000}
                              step={1}
                              value={defaultWeightStr}
                              onChange={(e) => setDefaultWeightStr(e.target.value)}
                              disabled={shipSettingsSaving}
                              required
                              className="tabular-nums"
                            />
                          </Field>
                          <Field>
                            <FieldLabel htmlFor="ship-def-length">
                              Chiều dài kiện mặc định (cm)
                            </FieldLabel>
                            <Input
                              id="ship-def-length"
                              type="number"
                              min={1}
                              max={200}
                              step={1}
                              value={defaultLengthStr}
                              onChange={(e) => setDefaultLengthStr(e.target.value)}
                              disabled={shipSettingsSaving}
                              required
                              className="tabular-nums"
                            />
                          </Field>
                          <Field>
                            <FieldLabel htmlFor="ship-def-width">
                              Chiều rộng kiện mặc định (cm)
                            </FieldLabel>
                            <Input
                              id="ship-def-width"
                              type="number"
                              min={1}
                              max={200}
                              step={1}
                              value={defaultWidthStr}
                              onChange={(e) => setDefaultWidthStr(e.target.value)}
                              disabled={shipSettingsSaving}
                              required
                              className="tabular-nums"
                            />
                          </Field>
                          <Field>
                            <FieldLabel htmlFor="ship-def-height">
                              Chiều cao kiện mặc định (cm)
                            </FieldLabel>
                            <Input
                              id="ship-def-height"
                              type="number"
                              min={1}
                              max={200}
                              step={1}
                              value={defaultHeightStr}
                              onChange={(e) => setDefaultHeightStr(e.target.value)}
                              disabled={shipSettingsSaving}
                              required
                              className="tabular-nums"
                            />
                          </Field>
                        </div>
                      </CardContent>
                    </Card>
                  </section>

                  <Separator />

                  <section className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                    <div className="space-y-1">
                      <h3 className="text-sm font-semibold text-foreground">Freeship gian hàng</h3>
                      <p className="text-[13px] leading-[18px] text-muted-foreground">
                        Tự động trừ phí ship cho đơn đạt ngưỡng và tính vào chi phí khuyến mãi của
                        gian hàng.
                      </p>
                    </div>

                    <Card>
                      <CardContent className="space-y-4 pt-4">
                        <div className="flex min-h-11 items-center justify-between gap-4">
                          <div className="min-w-0">
                            <div className="text-sm font-medium text-foreground">
                              Hỗ trợ miễn phí vận chuyển (Freeship shop)
                            </div>
                            <div className="text-xs text-muted-foreground">
                              Áp dụng tự động tại bước thanh toán khi đơn đạt điều kiện.
                            </div>
                          </div>
                          <Switch
                            checked={freeShipEnabled}
                            onCheckedChange={(next) => {
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
                          <div className="grid grid-cols-1 gap-4 border-t border-border pt-4 sm:grid-cols-2">
                            <Field>
                              <FieldLabel htmlFor="ship-fs-min">
                                Đơn tối thiểu áp dụng Freeship (VND)
                              </FieldLabel>
                              <Input
                                id="ship-fs-min"
                                type="number"
                                min={0}
                                step={1}
                                value={freeShipMinOrderStr}
                                onChange={(e) => setFreeShipMinOrderStr(e.target.value)}
                                disabled={shipSettingsSaving}
                                required
                                className="tabular-nums"
                              />
                              <FieldDescription className="text-xs">
                                {Math.max(0, Math.trunc(Number(freeShipMinOrderStr) || 0)) === 0
                                  ? "Áp dụng cho mọi đơn hàng (0₫ = không yêu cầu giá trị tối thiểu)."
                                  : `Áp dụng cho đơn từ ${formatVnd(Math.max(0, Math.trunc(Number(freeShipMinOrderStr) || 0)))}.`}
                              </FieldDescription>
                            </Field>

                            <Field>
                              <FieldLabel htmlFor="ship-fs-max">
                                Hỗ trợ phí ship tối đa / đơn (VND)
                              </FieldLabel>
                              <Input
                                id="ship-fs-max"
                                type="number"
                                min={1}
                                step={1}
                                value={freeShipMaxStr}
                                onChange={(e) => setFreeShipMaxStr(e.target.value)}
                                disabled={shipSettingsSaving}
                                required
                                className="tabular-nums"
                              />
                              <FieldDescription className="text-xs">
                                Bắt buộc lớn hơn 0₫ khi bật Freeship gian hàng.
                              </FieldDescription>
                            </Field>
                          </div>
                        )}

                        {shipSettingsError ? <FieldError>{shipSettingsError}</FieldError> : null}
                      </CardContent>
                      <CardFooter className="justify-end border-t border-border pt-3">
                        <Button type="submit" size="sm" disabled={shipSettingsSaving}>
                          <Check className="size-4" aria-hidden="true" />
                          <span>
                            {shipSettingsSaving ? "Đang lưu..." : "Lưu cài đặt vận chuyển"}
                          </span>
                        </Button>
                      </CardFooter>
                    </Card>
                  </section>
                </>
              )}
            </form>
          </TabsContent>

          <TabsContent value="warehouse" className="space-y-6">
            <section
              aria-labelledby="warehouse-heading"
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
            >
              <div className="space-y-1">
                <h2 id="warehouse-heading" className="text-sm font-semibold text-foreground">
                  Kho lấy hàng ({formatInt(addresses.length)})
                </h2>
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Địa chỉ lấy hàng để tính cước vận chuyển và in thông tin người gửi trên nhãn A6.
                </p>
              </div>

              <Card>
                <CardContent className="pt-4">
                  {addressesError ? (
                    <ErrorState
                      title="Không thể tải danh sách địa chỉ"
                      description={addressesError}
                      onRetry={() => router.refresh()}
                      className="shadow-none"
                    />
                  ) : addresses.length === 0 ? (
                    <p className="text-[13px] text-muted-foreground">
                      Chưa có địa chỉ kho lấy hàng nào được lưu.
                    </p>
                  ) : (
                    <div className="space-y-3">
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
                            className={cn(
                              "rounded-lg border p-3.5",
                              isPickup ? "border-brand/40 bg-brand/5" : "border-border bg-card"
                            )}
                          >
                            <div className="flex flex-wrap items-start justify-between gap-2">
                              <div className="min-w-0">
                                <div className="text-sm font-semibold text-foreground">
                                  {addr.recipient_name}
                                </div>
                                <div className="text-xs text-muted-foreground tabular-nums">
                                  {addr.recipient_phone}
                                </div>
                              </div>
                              <div className="flex flex-wrap items-center gap-1.5">
                                {isPickup && <StatusBadge tone="success">Kho mặc định</StatusBadge>}
                                {addr.is_default && (
                                  <Badge variant="neutral">Mặc định tài khoản</Badge>
                                )}
                              </div>
                            </div>
                            <p className="mt-2 text-[13px] text-muted-foreground">{fullLine}</p>
                          </article>
                        );
                      })}
                    </div>
                  )}
                </CardContent>
              </Card>
            </section>

            <Separator />

            <section
              aria-labelledby="site-policy-heading"
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
            >
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <h2 id="site-policy-heading" className="text-sm font-semibold text-foreground">
                    Site bán hàng
                  </h2>
                  <StatusBadge tone={siteMeta.tone}>{siteMeta.label}</StatusBadge>
                </div>
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Tên miền phụ, trạng thái xuất bản và đường dẫn trực tiếp tới gian hàng.
                </p>
              </div>

              <Card>
                <CardContent className="pt-4">
                  <dl className="divide-y divide-border text-sm">
                    <div className="flex flex-col gap-1 py-2.5 first:pt-0 sm:flex-row sm:justify-between sm:gap-4">
                      <dt className="text-[13px] text-muted-foreground">Tên miền</dt>
                      <dd className="font-medium text-foreground sm:text-right">
                        {site.host || "Chưa thiết lập"}
                      </dd>
                    </div>
                    <div className="flex flex-col gap-1 py-2.5 last:pb-0 sm:flex-row sm:justify-between sm:gap-4">
                      <dt className="text-[13px] text-muted-foreground">Trạng thái</dt>
                      <dd className="text-foreground sm:text-right">{siteMeta.label}</dd>
                    </div>
                    {site.suspendedReason && (
                      <div className="flex flex-col gap-1 py-2.5 last:pb-0 sm:flex-row sm:justify-between sm:gap-4">
                        <dt className="text-[13px] text-muted-foreground">Lý do tạm khóa</dt>
                        <dd className="text-destructive sm:text-right">{site.suspendedReason}</dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
                <CardFooter className="flex flex-wrap items-center gap-3 border-t border-border pt-3">
                  <Button size="sm" asChild>
                    <Link href="/seller/site">
                      <span>Mở Site Studio</span>
                      <ArrowUpRight className="size-4" aria-hidden="true" />
                    </Link>
                  </Button>
                  {site.url && (
                    <a
                      href={site.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-[13px] font-medium text-brand hover:underline"
                    >
                      Xem site đang chạy
                    </a>
                  )}
                </CardFooter>
              </Card>
            </section>
          </TabsContent>

          <TabsContent value="autoreply" className="space-y-6">
            <section
              aria-labelledby="autoreply-heading"
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
            >
              <div className="space-y-2">
                <h2 id="autoreply-heading" className="text-sm font-semibold text-foreground">
                  Trả lời tự động khi ngoại tuyến
                </h2>
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Gửi lời chào ngay khi khách mở hội thoại ngoài giờ trực.
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={autoReplyLoading}
                  onClick={() => void loadAutoReply()}
                >
                  <RefreshCw className="size-3.5" aria-hidden="true" />
                  <span>{autoReplyLoading ? "Đang tải..." : "Tải lại"}</span>
                </Button>
              </div>

              <Card>
                <form onSubmit={(e) => void handleSaveAutoReply(e)}>
                  <CardContent className="space-y-4 pt-4">
                    {autoReplyUnavailable && (
                      <Alert>
                        <Info className="size-4" aria-hidden="true" />
                        <AlertDescription>
                          Dịch vụ chat hiện chưa phản hồi. Bạn vẫn có thể soạn sẵn lời chào và thử
                          lưu lại sau.
                        </AlertDescription>
                      </Alert>
                    )}

                    <div className="flex min-h-11 items-center justify-between gap-4">
                      <div className="min-w-0">
                        <div className="text-sm font-medium text-foreground">
                          Bật trả lời tự động
                        </div>
                        <div className="text-xs text-muted-foreground">
                          Gửi lời chào ngay khi khách mở hội thoại ngoài giờ trực.
                        </div>
                      </div>
                      <Switch
                        checked={autoReplyEnabled}
                        onCheckedChange={setAutoReplyEnabled}
                        aria-label="Bật hoặc tắt trả lời tự động khi ngoại tuyến"
                      />
                    </div>

                    <Field data-invalid={Boolean(autoReplyError) || undefined}>
                      <FieldLabel htmlFor="auto-reply-msg">
                        Nội dung lời chào ({formatInt(autoReplyMessage.length)} / 500)
                      </FieldLabel>
                      <Textarea
                        id="auto-reply-msg"
                        rows={4}
                        value={autoReplyMessage}
                        onChange={(e) => {
                          setAutoReplyMessage(e.target.value);
                          if (autoReplyError) setAutoReplyError("");
                        }}
                        maxLength={500}
                        placeholder="Nhập lời chào khi ngoại tuyến và giờ làm việc..."
                        aria-invalid={Boolean(autoReplyError)}
                      />
                    </Field>

                    <div className="flex flex-wrap gap-2">
                      {AUTO_REPLY_PRESETS.map((preset) => (
                        <Button
                          key={preset.label}
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            setAutoReplyMessage(preset.text);
                            setAutoReplyError("");
                          }}
                        >
                          Dùng {preset.label.toLowerCase()}
                        </Button>
                      ))}
                    </div>

                    {autoReplyError ? <FieldError>{autoReplyError}</FieldError> : null}
                  </CardContent>
                  <CardFooter className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
                    {autoReplyUpdatedAt ? (
                      <span className="text-xs text-muted-foreground tabular-nums">
                        Cập nhật {formatDateTimeVi(autoReplyUpdatedAt)}
                      </span>
                    ) : (
                      <span />
                    )}
                    <Button type="submit" size="sm" disabled={autoReplySaving || autoReplyLoading}>
                      <Check className="size-4" aria-hidden="true" />
                      <span>{autoReplySaving ? "Đang lưu..." : "Lưu cấu hình"}</span>
                    </Button>
                  </CardFooter>
                </form>
              </Card>
            </section>

            <Separator />

            <section
              aria-labelledby="autoreply-preview-heading"
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
            >
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <h2
                    id="autoreply-preview-heading"
                    className="text-sm font-semibold text-foreground"
                  >
                    Xem trước hộp chat
                  </h2>
                  <StatusBadge tone={autoReplyEnabled ? "success" : "neutral"}>
                    {autoReplyEnabled ? "Đang bật" : "Đang tắt"}
                  </StatusBadge>
                </div>
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Hiển thị giống tin nhắn tự động khách hàng nhận được trong hộp chat.
                </p>
              </div>

              <Card>
                <CardContent className="pt-4">
                  <div className="rounded-lg border border-border bg-muted/50 p-3.5">
                    <div className="mb-1 text-xs font-semibold text-muted-foreground">
                      {shopDisplayName}
                    </div>
                    <p className="text-sm leading-5 whitespace-pre-line text-foreground">
                      {autoReplyMessage.trim() ||
                        "Nhập lời chào bên trái để xem trước tin nhắn tự động."}
                    </p>
                  </div>
                </CardContent>
                <CardFooter className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-[13px]">
                  <span className="text-muted-foreground">Hội thoại trực tiếp với khách hàng</span>
                  <Link href="/seller/chat" className="font-medium text-brand hover:underline">
                    Mở Tin nhắn
                  </Link>
                </CardFooter>
              </Card>
            </section>
          </TabsContent>
        </Tabs>
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
