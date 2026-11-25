"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconCheck, IconPlus, IconRefresh } from "@/components";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import type { GwProvince, GwWard } from "@/features/commerce/types";
import type { SessionUser } from "@/lib/server/session";
import {
  canSubmitShopApplication,
  getShopStatusInfo,
  isShopInfoRequested,
} from "@/features/account/orderMeta";
import type { GwSellerShop } from "./sellerApiTypes";
import { validateShopRegisterInput } from "./sellerValidation";
import type { SellerAddress } from "./sellerReturns";
import { useSellerContext } from "./SellerShell";
import { statusPillClass } from "./SellerUiPrimitives";
import common from "./SellerCommon.module.css";

export interface SellerShopOnboardingProps {
  user: SessionUser;
  initialShop: GwSellerShop | null;
  initialAddresses: SellerAddress[];
}

export function SellerShopOnboarding({
  user,
  initialShop,
  initialAddresses,
}: SellerShopOnboardingProps) {
  const router = useRouter();
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

  // If shop is ACTIVE in backend but JWT still lacks shop_id / role=seller, refresh cookies once
  useEffect(() => {
    if (
      shop?.status === "ACTIVE" &&
      (user.role !== "seller" || !user.shopId) &&
      !refreshedOnceRef.current
    ) {
      refreshedOnceRef.current = true;
      void api("/api/auth/refresh", { method: "POST" })
        .then(() => {
          router.refresh();
        })
        .catch(() => {
          // User can click manual button below
        });
    }
  }, [shop?.status, user.role, user.shopId, router]);

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
        await api("/api/auth/refresh", { method: "POST" }).catch(() => {});
        showToast("Gian hàng đã được duyệt. Đang mở Kênh người bán...");
        router.refresh();
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
  }, [checkingStatus, router, showToast]);

  const infoRequested = isShopInfoRequested(shop);
  const canSubmit = canSubmitShopApplication(shop);
  const isResubmitting = Boolean(shop && canSubmit);
  const statusMeta = shop ? getShopStatusInfo(shop.status, shop.rejection_reason) : null;
  const isPlainPending = Boolean(shop && shop.status === "PENDING" && !infoRequested);
  const isRejected = shop?.status === "REJECTED";
  const isActive = shop?.status === "ACTIVE";
  const reasonText = (shop?.rejection_reason ?? "").trim();

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>{shop ? "Hồ sơ gian hàng" : "Đăng ký mở gian hàng"}</h1>
        </div>
        {shop && (
          <div className={common.headerActions}>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={checkingStatus}
              onClick={() => void handleRefreshShopStatus()}
            >
              <IconRefresh size={14} />
              <span>{checkingStatus ? "Đang kiểm tra..." : "Kiểm tra lại trạng thái"}</span>
            </Button>
          </div>
        )}
      </header>

      {shop && statusMeta && (
        <section className={common.card} aria-label="Trạng thái phê duyệt gian hàng">
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>{shop.name}</h2>
            <span className={statusPillClass(statusMeta.tone)}>{statusMeta.label}</span>
          </div>

          {infoRequested && (
            <>
              <p className={common.fieldError} role="alert" style={{ marginBottom: 8 }}>
                Yêu cầu bổ sung thông tin: {reasonText}
              </p>
              <p className={common.cardMeta} style={{ marginBottom: 12 }}>
                {statusMeta.description}
              </p>
            </>
          )}
          {isPlainPending && (
            <p className={common.cardMeta} style={{ marginBottom: 12 }}>
              Hồ sơ mở gian hàng đã được ghi nhận và đang chờ quản trị viên xét duyệt. Bạn có thể
              bấm Kiểm tra lại trạng thái sau khi nhận thông báo duyệt.
            </p>
          )}
          {isRejected && (
            <>
              <p className={common.fieldError} role="alert" style={{ marginBottom: 8 }}>
                Hồ sơ gian hàng chưa được duyệt{reasonText ? `: ${reasonText}` : "."}
              </p>
              <p className={common.cardMeta} style={{ marginBottom: 12 }}>
                {statusMeta.description}
              </p>
            </>
          )}
          {isActive && (
            <p className={common.cardMeta} style={{ marginBottom: 12 }}>
              Gian hàng đã hoạt động. Nhấn nút bên trên để đồng bộ phiên và vào bảng điều khiển.
            </p>
          )}

          <div style={{ display: "grid", gap: 8, fontSize: 14 }}>
            <div>
              <span className={common.cardMeta}>Số điện thoại liên hệ: </span>
              <strong>{shop.contact_phone || "—"}</strong>
            </div>
            {shop.business_license_doc && (
              <div>
                <span className={common.cardMeta}>Giấy phép kinh doanh: </span>
                <span>{shop.business_license_doc}</span>
              </div>
            )}
            {shop.description && (
              <div>
                <span className={common.cardMeta}>Mô tả: </span>
                <span>{shop.description}</span>
              </div>
            )}
            {shop.created_at && (
              <div>
                <span className={common.cardMeta}>Ngày gửi hồ sơ: </span>
                <span>{formatDateTimeVi(shop.created_at)}</span>
              </div>
            )}
          </div>

          {!canSubmit && formError && (
            <p className={common.fieldError} role="alert" style={{ marginTop: 12 }}>
              {formError}
            </p>
          )}
        </section>
      )}

      {canSubmit && (
        <>
          <form
            className={common.card}
            onSubmit={(e) => void handleRegisterShop(e)}
            aria-label={
              isResubmitting
                ? "Biểu mẫu cập nhật và nộp lại hồ sơ gian hàng"
                : "Biểu mẫu đăng ký mở gian hàng"
            }
          >
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>
                {isResubmitting ? "Cập nhật và nộp lại hồ sơ" : "Thông tin gian hàng"}
              </h2>
              <span className={common.cardMeta}>
                Hồ sơ sẽ được quản trị viên duyệt trước khi mở bán
              </span>
            </div>

            <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
                  gap: 14,
                }}
              >
                <div className={common.field}>
                  <label htmlFor="shop-reg-name" className={common.fieldLabel}>
                    Tên gian hàng
                  </label>
                  <input
                    id="shop-reg-name"
                    type="text"
                    className={common.textInput}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Ví dụ: Xưởng May Bạch Đằng"
                    maxLength={120}
                    required
                  />
                </div>
                <div className={common.field}>
                  <label htmlFor="shop-reg-phone" className={common.fieldLabel}>
                    Số điện thoại liên hệ
                  </label>
                  <input
                    id="shop-reg-phone"
                    type="tel"
                    className={common.textInput}
                    value={contactPhone}
                    onChange={(e) => setContactPhone(e.target.value)}
                    placeholder="Ví dụ: 0912345678"
                    maxLength={20}
                    required
                  />
                </div>
              </div>

              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
                  gap: 14,
                }}
              >
                <div className={common.field}>
                  <label htmlFor="shop-reg-slug" className={common.fieldLabel}>
                    Đường dẫn gian hàng (tùy chọn)
                  </label>
                  <input
                    id="shop-reg-slug"
                    type="text"
                    className={common.textInput}
                    value={slug}
                    onChange={(e) => setSlug(e.target.value)}
                    placeholder="xuong-may-bach-dang"
                    maxLength={80}
                  />
                </div>
                <div className={common.field}>
                  <label htmlFor="shop-reg-license" className={common.fieldLabel}>
                    Giấy phép kinh doanh
                  </label>
                  <input
                    id="shop-reg-license"
                    type="text"
                    className={common.textInput}
                    value={businessLicenseDoc}
                    onChange={(e) => setBusinessLicenseDoc(e.target.value)}
                    placeholder="Mã số ĐKKD hoặc liên kết chứng từ"
                    maxLength={500}
                    required
                  />
                </div>
              </div>

              <div className={common.field}>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 8,
                    flexWrap: "wrap",
                  }}
                >
                  <label htmlFor="shop-reg-pickup" className={common.fieldLabel}>
                    Địa chỉ kho lấy hàng
                  </label>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => setShowAddrForm((prev) => !prev)}
                  >
                    <IconPlus size={14} />
                    <span>{showAddrForm ? "Đóng mẫu địa chỉ" : "Thêm địa chỉ kho"}</span>
                  </Button>
                </div>
                {addresses.length > 0 ? (
                  <select
                    id="shop-reg-pickup"
                    className={common.textInput}
                    value={pickupAddressId}
                    onChange={(e) => setPickupAddressId(e.target.value)}
                    required
                  >
                    {addresses.map((addr) => (
                      <option key={addr.id} value={addr.id}>
                        {addr.recipient_name} · {addr.recipient_phone} — {addr.street_address},{" "}
                        {addr.ward_name}, {addr.province_name}
                      </option>
                    ))}
                  </select>
                ) : (
                  <span className={common.fieldHint}>
                    Tài khoản chưa có địa chỉ kho. Vui lòng tạo địa chỉ kho lấy hàng bên dưới trước
                    khi gửi đăng ký.
                  </span>
                )}
              </div>

              <div className={common.field}>
                <label htmlFor="shop-reg-desc" className={common.fieldLabel}>
                  Mô tả gian hàng (tùy chọn)
                </label>
                <textarea
                  id="shop-reg-desc"
                  className={common.textArea}
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Ngành hàng chính và thông tin giới thiệu ngắn..."
                  maxLength={1000}
                />
              </div>

              {formError && (
                <p className={common.fieldError} role="alert">
                  {formError}
                </p>
              )}

              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <Button
                  type="submit"
                  variant="ink"
                  size="sm"
                  shape="app"
                  disabled={submitting || !pickupAddressId}
                >
                  <IconCheck size={14} />
                  <span>
                    {submitting
                      ? "Đang gửi hồ sơ..."
                      : isResubmitting
                        ? "Nộp lại hồ sơ gian hàng"
                        : "Gửi đăng ký mở gian hàng"}
                  </span>
                </Button>
              </div>
            </div>
          </form>

          {showAddrForm && (
            <form
              className={common.card}
              onSubmit={(e) => void handleCreateAddress(e)}
              aria-label="Thêm địa chỉ kho lấy hàng"
            >
              <div className={common.cardHeader}>
                <h2 className={common.cardTitle}>Thêm địa chỉ kho lấy hàng</h2>
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
                    gap: 14,
                  }}
                >
                  <div className={common.field}>
                    <label htmlFor="addr-name" className={common.fieldLabel}>
                      Người liên hệ kho
                    </label>
                    <input
                      id="addr-name"
                      type="text"
                      className={common.textInput}
                      value={addrRecipient}
                      onChange={(e) => setAddrRecipient(e.target.value)}
                      required
                    />
                  </div>
                  <div className={common.field}>
                    <label htmlFor="addr-phone" className={common.fieldLabel}>
                      Số điện thoại kho
                    </label>
                    <input
                      id="addr-phone"
                      type="tel"
                      className={common.textInput}
                      value={addrPhone}
                      onChange={(e) => setAddrPhone(e.target.value)}
                      required
                    />
                  </div>
                </div>

                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
                    gap: 14,
                  }}
                >
                  <div className={common.field}>
                    <label htmlFor="addr-prov" className={common.fieldLabel}>
                      Tỉnh / Thành phố
                    </label>
                    <select
                      id="addr-prov"
                      className={common.textInput}
                      value={addrProvinceCode}
                      onChange={(e) => setAddrProvinceCode(e.target.value)}
                      required
                    >
                      <option value="">Chọn tỉnh / thành phố</option>
                      {provinces.map((p) => (
                        <option key={p.code} value={p.code}>
                          {p.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className={common.field}>
                    <label htmlFor="addr-ward" className={common.fieldLabel}>
                      Phường / Xã
                    </label>
                    <select
                      id="addr-ward"
                      className={common.textInput}
                      value={addrWardCode}
                      onChange={(e) => setAddrWardCode(e.target.value)}
                      disabled={!addrProvinceCode}
                      required
                    >
                      <option value="">Chọn phường / xã</option>
                      {wards.map((w) => (
                        <option key={w.code} value={w.code}>
                          {w.name}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                <div className={common.field}>
                  <label htmlFor="addr-street" className={common.fieldLabel}>
                    Số nhà, tên đường
                  </label>
                  <input
                    id="addr-street"
                    type="text"
                    className={common.textInput}
                    value={addrStreet}
                    onChange={(e) => setAddrStreet(e.target.value)}
                    placeholder="Ví dụ: 120 Nguyễn Trãi"
                    required
                  />
                </div>

                {addrError && (
                  <p className={common.fieldError} role="alert">
                    {addrError}
                  </p>
                )}

                <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
                  {addresses.length > 0 && (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      shape="app"
                      onClick={() => setShowAddrForm(false)}
                    >
                      Hủy
                    </Button>
                  )}
                  <Button type="submit" variant="ink" size="sm" shape="app" disabled={addrSaving}>
                    <span>{addrSaving ? "Đang lưu..." : "Lưu địa chỉ kho"}</span>
                  </Button>
                </div>
              </div>
            </form>
          )}
        </>
      )}
    </div>
  );
}
