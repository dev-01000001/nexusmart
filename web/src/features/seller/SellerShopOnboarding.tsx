"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AlertCircle, ArrowRight, Check, CheckCircle2, Plus, RefreshCw } from "lucide-react";
import { PageHeader, StatusBadge } from "@/components/app";
import { useSession } from "@/components/SessionProvider";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import type { GwProvince, GwWard } from "@/features/commerce/types";
import type { SessionUser } from "@/lib/server/session";
import {
  canSubmitShopApplication,
  getShopStatusInfo,
  isShopInfoRequested,
} from "@/features/account/orderMeta";
import { resolveSellerShopTone } from "./ops/sellerTone";
import type { GwSellerShop } from "./sellerApiTypes";
import { validateShopRegisterInput } from "./sellerValidation";
import type { SellerAddress } from "./sellerReturns";
import { useSellerContext } from "./SellerShell";

export interface SellerShopOnboardingProps {
  user: SessionUser;
  initialShop: GwSellerShop | null;
  initialAddresses: SellerAddress[];
}

const APPROVAL_POLL_INTERVAL_MS = 30_000;
const APPROVAL_FOCUS_THROTTLE_MS = 3_000;

export function SellerShopOnboarding({
  user,
  initialShop,
  initialAddresses,
}: SellerShopOnboardingProps) {
  const router = useRouter();
  const { refreshSession } = useSession();
  const { showToast } = useSellerContext();

  const [shop, setShop] = useState<GwSellerShop | null>(initialShop);
  const [addresses, setAddresses] = useState<SellerAddress[]>(initialAddresses);

  // Registration / resubmission form fields (pre-filled from existing shop on REQUEST_INFO / REJECTED)
  const [name, setName] = useState(initialShop?.name ?? "");
  const [slug, setSlug] = useState(initialShop?.slug ?? "");
  const [contactPhone, setContactPhone] = useState(initialShop?.contact_phone || user.phone || "");
  const [pickupAddressId, setPickupAddressId] = useState(() => {
    const existing = initialShop?.pickup_address_id;
    if (existing && initialAddresses.some((a) => a.id === existing)) return existing;
    return initialAddresses.find((a) => a.is_default)?.id ?? initialAddresses[0]?.id ?? "";
  });
  const [businessLicenseDoc, setBusinessLicenseDoc] = useState(
    initialShop?.business_license_doc ?? ""
  );
  const [description, setDescription] = useState(initialShop?.description ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [checkingStatus, setCheckingStatus] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  // Inline pickup address creation when user has no address yet (or wants to add one)
  const [showAddrForm, setShowAddrForm] = useState(initialAddresses.length === 0);
  const [provinces, setProvinces] = useState<GwProvince[]>([]);
  const [wards, setWards] = useState<GwWard[]>([]);
  const [addrRecipient, setAddrRecipient] = useState(user.fullName || "");
  const [addrPhone, setAddrPhone] = useState(user.phone || "");
  const [addrProvinceCode, setAddrProvinceCode] = useState("");
  const [addrWardCode, setAddrWardCode] = useState("");
  const [addrStreet, setAddrStreet] = useState("");
  const [addrSaving, setAddrSaving] = useState(false);
  const [addrError, setAddrError] = useState<string | null>(null);

  const refreshedOnceRef = useRef(false);
  const lastPollAtRef = useRef(0);
  const syncInFlightRef = useRef(false);

  const syncApprovedSessionOnce = useCallback(
    async (opts: { notify?: boolean } = {}) => {
      if (refreshedOnceRef.current || syncInFlightRef.current) return;
      refreshedOnceRef.current = true;
      syncInFlightRef.current = true;
      try {
        await api("/api/auth/refresh", { method: "POST" });
        await refreshSession().catch(() => null);
        if (opts.notify) {
          showToast("Gian hàng đã được duyệt. Đang cập nhật Kênh người bán...");
        }
        if (typeof window !== "undefined") {
          window.location.replace("/seller?approved=1");
        } else {
          router.refresh();
        }
      } catch {
        // Keep the approved card visible so user can retry via "Bắt đầu bán hàng".
      } finally {
        syncInFlightRef.current = false;
      }
    },
    [refreshSession, router, showToast]
  );

  // If shop is ACTIVE in backend but JWT/session still lacks seller claims, refresh cookies once
  useEffect(() => {
    if (shop?.status === "ACTIVE" && (user.role !== "seller" || !user.shopId)) {
      void syncApprovedSessionOnce();
    }
  }, [shop?.status, user.role, user.shopId, syncApprovedSessionOnce]);

  // Lightweight polling while awaiting admin approval (30s interval + tab focus/visibility)
  useEffect(() => {
    if (shop?.status !== "PENDING") return;
    let active = true;

    const pollShopStatus = async (force: boolean) => {
      if (!active || syncInFlightRef.current) return;
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      const now = Date.now();
      if (!force && now - lastPollAtRef.current < APPROVAL_FOCUS_THROTTLE_MS) return;
      lastPollAtRef.current = now;
      try {
        const res = await api<{ shop: GwSellerShop | null }>("/api/shops/me");
        if (!active) return;
        const nextShop = res.shop ?? null;
        if (!nextShop) return;
        if (
          nextShop.status !== shop.status ||
          nextShop.rejection_reason !== shop.rejection_reason
        ) {
          setShop(nextShop);
        }
        if (nextShop.status === "ACTIVE") {
          await syncApprovedSessionOnce({ notify: true });
        }
      } catch {
        // Silent background check — user can still click manual refresh.
      }
    };

    const intervalId = window.setInterval(() => {
      void pollShopStatus(true);
    }, APPROVAL_POLL_INTERVAL_MS);

    const onFocus = () => {
      void pollShopStatus(false);
    };
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void pollShopStatus(false);
      }
    };

    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      active = false;
      window.clearInterval(intervalId);
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [shop?.status, shop?.rejection_reason, syncApprovedSessionOnce]);

  // Load provinces when address creation form opens
  useEffect(() => {
    if (!showAddrForm || provinces.length > 0) return;
    let active = true;
    void api<{ provinces?: GwProvince[] }>("/api/shops/locations")
      .then((res) => {
        if (active) setProvinces(res.provinces ?? []);
      })
      .catch(() => {
        if (active) setProvinces([]);
      });
    return () => {
      active = false;
    };
  }, [showAddrForm, provinces.length]);

  useEffect(() => {
    if (!addrProvinceCode) {
      setWards([]);
      setAddrWardCode("");
      return;
    }
    let active = true;
    void api<{ wards?: GwWard[] }>(
      `/api/shops/locations?province_code=${encodeURIComponent(addrProvinceCode)}`
    )
      .then((res) => {
        if (active) setWards(res.wards ?? []);
      })
      .catch(() => {
        if (active) setWards([]);
      });
    return () => {
      active = false;
    };
  }, [addrProvinceCode]);

  const handleCreateAddress = async (e: React.FormEvent) => {
    e.preventDefault();
    if (addrSaving) return;
    const province = provinces.find((p) => p.code === addrProvinceCode);
    const ward = wards.find((w) => w.code === addrWardCode);
    if (!addrRecipient.trim() || !addrPhone.trim() || !province || !ward || !addrStreet.trim()) {
      setAddrError(
        "Vui lòng nhập đầy đủ người liên hệ, số điện thoại, tỉnh/thành, phường/xã và địa chỉ kho."
      );
      return;
    }
    setAddrError(null);
    setAddrSaving(true);
    try {
      const res = await api<{ address: SellerAddress }>("/api/auth/addresses", {
        method: "POST",
        body: {
          recipient_name: addrRecipient.trim(),
          recipient_phone: addrPhone.trim(),
          province_code: province.code,
          province_name: province.name,
          ward_code: ward.code,
          ward_name: ward.name,
          street_address: addrStreet.trim(),
          is_default: addresses.length === 0,
        },
      });
      if (res.address) {
        setAddresses((prev) => [res.address, ...prev]);
        setPickupAddressId(res.address.id);
        setShowAddrForm(false);
        setAddrStreet("");
        showToast("Đã thêm địa chỉ kho lấy hàng.");
      }
    } catch (err) {
      setAddrError(getErrorMessage(err, "Không thể tạo địa chỉ kho lúc này."));
    } finally {
      setAddrSaving(false);
    }
  };

  const handleRegisterShop = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    const validated = validateShopRegisterInput({
      name,
      slug,
      description,
      pickup_address_id: pickupAddressId,
      contact_phone: contactPhone,
      business_license_doc: businessLicenseDoc,
    });
    if (!validated.ok) {
      setFormError(validated.message);
      return;
    }
    const resubmitting = Boolean(shop && canSubmitShopApplication(shop));
    setFormError(null);
    setSubmitting(true);
    try {
      const res = await api<{ shop: GwSellerShop }>("/api/shops/register", {
        method: "POST",
        body: validated.payload,
      });
      if (res.shop) {
        setShop(res.shop);
      }
      showToast(
        resubmitting
          ? "Đã nộp lại hồ sơ gian hàng. Quản trị viên sẽ xem xét phê duyệt."
          : "Đã gửi hồ sơ đăng ký gian hàng. Quản trị viên sẽ xem xét phê duyệt."
      );
      router.refresh();
    } catch (err) {
      setFormError(
        err instanceof ApiError ? err.message : "Không thể gửi đăng ký gian hàng lúc này."
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleRefreshShopStatus = useCallback(async () => {
    if (checkingStatus) return;
    setCheckingStatus(true);
    setFormError(null);
    try {
      const res = await api<{ shop: GwSellerShop | null }>("/api/shops/me");
      const nextShop = res.shop ?? null;
      setShop(nextShop);
      if (nextShop && canSubmitShopApplication(nextShop)) {
        setName((prev) => prev || nextShop.name || "");
        setSlug((prev) => prev || nextShop.slug || "");
        setContactPhone((prev) => prev || nextShop.contact_phone || "");
        setBusinessLicenseDoc((prev) => prev || nextShop.business_license_doc || "");
        setDescription((prev) => prev || nextShop.description || "");
        if (nextShop.pickup_address_id) {
          setPickupAddressId((prev) => prev || nextShop.pickup_address_id);
        }
      }
      if (nextShop?.status === "ACTIVE") {
        refreshedOnceRef.current = false;
        await syncApprovedSessionOnce({ notify: true });
      } else {
        showToast("Đã cập nhật trạng thái hồ sơ gian hàng.");
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setShop(null);
      } else {
        setFormError(getErrorMessage(err, "Không thể kiểm tra trạng thái gian hàng lúc này."));
      }
    } finally {
      setCheckingStatus(false);
    }
  }, [checkingStatus, showToast, syncApprovedSessionOnce]);

  const handleStartSelling = async () => {
    if (user.role !== "seller" || !user.shopId) {
      await api("/api/auth/refresh", { method: "POST" }).catch(() => {});
      await refreshSession().catch(() => null);
    }
    if (typeof window !== "undefined") {
      window.location.assign("/seller");
    } else {
      router.push("/seller");
    }
  };

  const infoRequested = isShopInfoRequested(shop);
  const canSubmit = canSubmitShopApplication(shop);
  const isResubmitting = Boolean(shop && canSubmit);
  const statusMeta = shop ? getShopStatusInfo(shop.status, shop.rejection_reason) : null;
  const shopTone = shop ? resolveSellerShopTone(shop.status, shop.rejection_reason) : "neutral";
  const isPlainPending = Boolean(shop && shop.status === "PENDING" && !infoRequested);
  const isRejected = shop?.status === "REJECTED";
  const isActive = shop?.status === "ACTIVE";
  const reasonText = (shop?.rejection_reason ?? "").trim();

  return (
    <div className="mx-auto max-w-[820px] space-y-6">
      <PageHeader
        title={
          isActive ? "Gian hàng đã được duyệt" : shop ? "Hồ sơ gian hàng" : "Đăng ký mở gian hàng"
        }
        subtitle={
          isActive
            ? "Gian hàng của bạn đã sẵn sàng hoạt động trên NexusMart."
            : "Hoàn tất thông tin gian hàng và địa chỉ kho lấy hàng để quản trị viên duyệt mở bán."
        }
        badges={
          shop && statusMeta ? <StatusBadge tone={shopTone}>{statusMeta.label}</StatusBadge> : null
        }
        primaryAction={
          isActive ? (
            user.role === "seller" && user.shopId ? (
              <Button size="sm" asChild>
                <Link href="/seller">
                  <span>Bắt đầu bán hàng</span>
                  <ArrowRight className="size-4" aria-hidden="true" />
                </Link>
              </Button>
            ) : (
              <Button type="button" size="sm" onClick={() => void handleStartSelling()}>
                <span>Bắt đầu bán hàng</span>
                <ArrowRight className="size-4" aria-hidden="true" />
              </Button>
            )
          ) : shop ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={checkingStatus}
              onClick={() => void handleRefreshShopStatus()}
            >
              <RefreshCw
                className={checkingStatus ? "size-4 animate-spin" : "size-4"}
                aria-hidden="true"
              />
              <span>{checkingStatus ? "Đang kiểm tra..." : "Kiểm tra lại trạng thái"}</span>
            </Button>
          ) : null
        }
      />

      {shop && statusMeta ? (
        <section aria-label="Trạng thái phê duyệt gian hàng" className="space-y-4">
          {isActive ? (
            <Alert role="status" className="border-badge-success-ink/25 bg-badge-success-bg/50">
              <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
              <AlertTitle>Gian hàng đã được duyệt</AlertTitle>
              <AlertDescription className="space-y-3 text-foreground">
                <p>
                  Gian hàng <strong>{shop.name}</strong> đã kích hoạt thành công. Bạn có thể bắt đầu
                  đăng sản phẩm, thiết lập site bán hàng và xử lý đơn ngay bây giờ.
                </p>
                <div>
                  {user.role === "seller" && user.shopId ? (
                    <Button size="sm" asChild>
                      <Link href="/seller">
                        <span>Bắt đầu bán hàng</span>
                        <ArrowRight className="size-4" aria-hidden="true" />
                      </Link>
                    </Button>
                  ) : (
                    <Button type="button" size="sm" onClick={() => void handleStartSelling()}>
                      <span>Bắt đầu bán hàng</span>
                      <ArrowRight className="size-4" aria-hidden="true" />
                    </Button>
                  )}
                </div>
              </AlertDescription>
            </Alert>
          ) : null}

          {infoRequested ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertTitle>Yêu cầu bổ sung thông tin: {reasonText}</AlertTitle>
              <AlertDescription>{statusMeta.description}</AlertDescription>
            </Alert>
          ) : null}

          {isRejected ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertTitle>
                Hồ sơ gian hàng chưa được duyệt{reasonText ? `: ${reasonText}` : "."}
              </AlertTitle>
              <AlertDescription>{statusMeta.description}</AlertDescription>
            </Alert>
          ) : null}

          <Card>
            <CardHeader className="flex-row items-center justify-between gap-2">
              <CardTitle>{shop.name}</CardTitle>
              <StatusBadge tone={shopTone}>{statusMeta.label}</StatusBadge>
            </CardHeader>
            <CardContent className="space-y-4">
              {isPlainPending ? (
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Hồ sơ mở gian hàng đã được ghi nhận và đang chờ quản trị viên xét duyệt. Trang sẽ
                  tự động cập nhật khi gian hàng được duyệt, hoặc bạn có thể bấm Kiểm tra lại trạng
                  thái.
                </p>
              ) : null}
              {isActive ? (
                <p className="text-[13px] leading-[18px] text-muted-foreground">
                  Gian hàng đã được duyệt và sẵn sàng hoạt động. Nhấn Bắt đầu bán hàng để vào bảng
                  điều khiển Kênh người bán.
                </p>
              ) : null}

              <dl className="grid grid-cols-1 gap-2.5 text-sm sm:grid-cols-2">
                <div>
                  <dt className="text-xs text-muted-foreground">Số điện thoại liên hệ</dt>
                  <dd className="mt-0.5 font-medium text-foreground tabular-nums">
                    {shop.contact_phone || "—"}
                  </dd>
                </div>
                {shop.business_license_doc ? (
                  <div>
                    <dt className="text-xs text-muted-foreground">Giấy phép kinh doanh</dt>
                    <dd className="mt-0.5 font-medium text-foreground">
                      {shop.business_license_doc}
                    </dd>
                  </div>
                ) : null}
                {shop.created_at ? (
                  <div>
                    <dt className="text-xs text-muted-foreground">Ngày gửi hồ sơ</dt>
                    <dd className="mt-0.5 text-foreground tabular-nums">
                      {formatDateTimeVi(shop.created_at)}
                    </dd>
                  </div>
                ) : null}
                {shop.description ? (
                  <div className="sm:col-span-2">
                    <dt className="text-xs text-muted-foreground">Mô tả</dt>
                    <dd className="mt-0.5 text-foreground">{shop.description}</dd>
                  </div>
                ) : null}
              </dl>

              {!canSubmit && formError ? (
                <p className="text-sm font-medium text-destructive" role="alert">
                  {formError}
                </p>
              ) : null}
            </CardContent>
          </Card>
        </section>
      ) : null}

      {canSubmit ? (
        <>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
            <div className="space-y-1">
              <h2 className="text-sm font-semibold text-foreground">
                {isResubmitting ? "Cập nhật và nộp lại hồ sơ" : "Thông tin gian hàng"}
              </h2>
              <p className="text-[13px] leading-[18px] text-muted-foreground">
                Hồ sơ sẽ được quản trị viên duyệt trước khi mở bán. Tên gian hàng và số điện thoại
                hiển thị trên hoá đơn giao hàng.
              </p>
            </div>

            <Card>
              <CardContent>
                <form
                  onSubmit={(e) => void handleRegisterShop(e)}
                  aria-label={
                    isResubmitting
                      ? "Biểu mẫu cập nhật và nộp lại hồ sơ gian hàng"
                      : "Biểu mẫu đăng ký mở gian hàng"
                  }
                  className="space-y-4"
                >
                  <FieldGroup className="gap-4">
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <Field>
                        <FieldLabel htmlFor="shop-reg-name">Tên gian hàng</FieldLabel>
                        <Input
                          id="shop-reg-name"
                          type="text"
                          value={name}
                          onChange={(e) => setName(e.target.value)}
                          placeholder="Ví dụ: Gốm Sứ Bát Tràng, Tiệm Cà Phê Mộc"
                          maxLength={120}
                          required
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="shop-reg-phone">Số điện thoại liên hệ</FieldLabel>
                        <Input
                          id="shop-reg-phone"
                          type="tel"
                          value={contactPhone}
                          onChange={(e) => setContactPhone(e.target.value)}
                          placeholder="Ví dụ: 0912345678"
                          maxLength={20}
                          required
                        />
                      </Field>
                    </div>

                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <Field>
                        <FieldLabel htmlFor="shop-reg-slug">
                          Đường dẫn gian hàng (tùy chọn)
                        </FieldLabel>
                        <Input
                          id="shop-reg-slug"
                          type="text"
                          value={slug}
                          onChange={(e) => setSlug(e.target.value)}
                          placeholder="cua-hang-cua-ban"
                          maxLength={80}
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="shop-reg-license">Giấy phép kinh doanh</FieldLabel>
                        <Input
                          id="shop-reg-license"
                          type="text"
                          value={businessLicenseDoc}
                          onChange={(e) => setBusinessLicenseDoc(e.target.value)}
                          placeholder="Mã số ĐKKD hoặc liên kết chứng từ"
                          maxLength={500}
                          required
                        />
                      </Field>
                    </div>

                    <Field>
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <FieldLabel htmlFor="shop-reg-pickup">Địa chỉ kho lấy hàng</FieldLabel>
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          onClick={() => setShowAddrForm((prev) => !prev)}
                        >
                          <Plus className="size-3.5" aria-hidden="true" />
                          <span>{showAddrForm ? "Đóng mẫu địa chỉ" : "Thêm địa chỉ kho"}</span>
                        </Button>
                      </div>
                      {addresses.length > 0 ? (
                        <Select value={pickupAddressId} onValueChange={setPickupAddressId}>
                          <SelectTrigger id="shop-reg-pickup" className="w-full bg-card">
                            <SelectValue placeholder="Chọn địa chỉ kho lấy hàng" />
                          </SelectTrigger>
                          <SelectContent>
                            {addresses.map((addr) => (
                              <SelectItem key={addr.id} value={addr.id}>
                                {addr.recipient_name} · {addr.recipient_phone} —{" "}
                                {addr.street_address}, {addr.ward_name}, {addr.province_name}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      ) : (
                        <FieldDescription>
                          Tài khoản chưa có địa chỉ kho. Vui lòng tạo địa chỉ kho lấy hàng bên dưới
                          trước khi gửi đăng ký.
                        </FieldDescription>
                      )}
                    </Field>

                    <Field>
                      <FieldLabel htmlFor="shop-reg-desc">Mô tả gian hàng (tùy chọn)</FieldLabel>
                      <Textarea
                        id="shop-reg-desc"
                        rows={3}
                        value={description}
                        onChange={(e) => setDescription(e.target.value)}
                        placeholder="Ngành hàng chính và thông tin giới thiệu ngắn..."
                        maxLength={1000}
                      />
                    </Field>
                  </FieldGroup>

                  {formError ? <FieldError role="alert">{formError}</FieldError> : null}

                  <div className="flex justify-end pt-1">
                    <Button type="submit" size="sm" disabled={submitting || !pickupAddressId}>
                      <Check className="size-4" aria-hidden="true" />
                      <span>
                        {submitting
                          ? "Đang gửi hồ sơ..."
                          : isResubmitting
                            ? "Nộp lại hồ sơ gian hàng"
                            : "Gửi đăng ký mở gian hàng"}
                      </span>
                    </Button>
                  </div>
                </form>
              </CardContent>
            </Card>
          </div>

          {showAddrForm ? (
            <>
              <Separator />
              <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                <div className="space-y-1">
                  <h2 className="text-sm font-semibold text-foreground">
                    Thêm địa chỉ kho lấy hàng
                  </h2>
                  <p className="text-[13px] leading-[18px] text-muted-foreground">
                    Dùng để hãng vận chuyển đến lấy kiện và tính cước giao hàng chính xác theo 2 cấp
                    hành chính.
                  </p>
                </div>

                <Card>
                  <CardHeader>
                    <CardTitle>Địa chỉ kho mới</CardTitle>
                    <CardDescription>
                      Điền đầy đủ người liên hệ và địa chỉ thực tế của kho.
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    <form
                      onSubmit={(e) => void handleCreateAddress(e)}
                      aria-label="Thêm địa chỉ kho lấy hàng"
                      className="space-y-4"
                    >
                      <FieldGroup className="gap-4">
                        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                          <Field>
                            <FieldLabel htmlFor="addr-name">Người liên hệ kho</FieldLabel>
                            <Input
                              id="addr-name"
                              type="text"
                              value={addrRecipient}
                              onChange={(e) => setAddrRecipient(e.target.value)}
                              required
                            />
                          </Field>
                          <Field>
                            <FieldLabel htmlFor="addr-phone">Số điện thoại kho</FieldLabel>
                            <Input
                              id="addr-phone"
                              type="tel"
                              value={addrPhone}
                              onChange={(e) => setAddrPhone(e.target.value)}
                              required
                            />
                          </Field>
                        </div>

                        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                          <Field>
                            <FieldLabel htmlFor="addr-prov">Tỉnh / Thành phố</FieldLabel>
                            <Select
                              value={addrProvinceCode || undefined}
                              onValueChange={(val) => {
                                setAddrProvinceCode(val);
                                setAddrWardCode("");
                              }}
                            >
                              <SelectTrigger id="addr-prov" className="w-full bg-card">
                                <SelectValue placeholder="Chọn tỉnh / thành phố" />
                              </SelectTrigger>
                              <SelectContent>
                                {provinces.map((p) => (
                                  <SelectItem key={p.code} value={p.code}>
                                    {p.name}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </Field>
                          <Field>
                            <FieldLabel htmlFor="addr-ward">Phường / Xã</FieldLabel>
                            <Select
                              value={addrWardCode || undefined}
                              onValueChange={setAddrWardCode}
                              disabled={!addrProvinceCode}
                            >
                              <SelectTrigger id="addr-ward" className="w-full bg-card">
                                <SelectValue placeholder="Chọn phường / xã" />
                              </SelectTrigger>
                              <SelectContent>
                                {wards.map((w) => (
                                  <SelectItem key={w.code} value={w.code}>
                                    {w.name}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </Field>
                        </div>

                        <Field>
                          <FieldLabel htmlFor="addr-street">Số nhà, tên đường</FieldLabel>
                          <Input
                            id="addr-street"
                            type="text"
                            value={addrStreet}
                            onChange={(e) => setAddrStreet(e.target.value)}
                            placeholder="Ví dụ: 120 Nguyễn Trãi"
                            required
                          />
                        </Field>
                      </FieldGroup>

                      {addrError ? <FieldError role="alert">{addrError}</FieldError> : null}

                      <div className="flex flex-wrap justify-end gap-2 pt-1">
                        {addresses.length > 0 ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() => setShowAddrForm(false)}
                          >
                            Hủy
                          </Button>
                        ) : null}
                        <Button type="submit" size="sm" disabled={addrSaving}>
                          {addrSaving ? "Đang lưu..." : "Lưu địa chỉ kho"}
                        </Button>
                      </div>
                    </form>
                  </CardContent>
                </Card>
              </div>
            </>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
