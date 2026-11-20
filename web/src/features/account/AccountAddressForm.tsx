"use client";

import React, { useMemo, useState } from "react";
import { ApiError, api } from "@/lib/api";
import { getProvinceByCode } from "@/lib/provinces";
import { normalizeVietnamese } from "@/lib/search";
import { formatVnd } from "@/lib/format";
import { Button } from "@/components/Button";
import styles from "@/features/commerce/commerce.module.css";
import {
  toStorefrontProvinceCode,
  type AddressView,
  type GwProvince,
  type GwWard,
} from "./accountTypes";

interface BffAddressItem {
  id: string;
  recipientName: string;
  recipientPhone: string;
  provinceCode: string;
  provinceName: string;
  wardCode: string;
  wardName: string;
  streetAddress: string;
  isDefault: boolean;
}

export interface AccountAddressFormProps {
  defaultRecipientName: string;
  defaultPhone: string;
  provinces: GwProvince[];
  initialProvinceCode: string;
  initialWards: GwWard[];
  onSaved: (created: AddressView) => void;
  onCancel?: () => void;
  submitLabel?: string;
}

export function AccountAddressForm({
  defaultRecipientName,
  defaultPhone,
  provinces,
  initialProvinceCode,
  initialWards,
  onSaved,
  onCancel,
  submitLabel = "Lưu địa chỉ nhận hàng",
}: AccountAddressFormProps) {
  const [recipientName, setRecipientName] = useState(defaultRecipientName);
  const [phone, setPhone] = useState(defaultPhone);
  const [provinceCode, setProvinceCode] = useState(
    initialProvinceCode || provinces[0]?.code || "79"
  );
  const [wards, setWards] = useState<GwWard[]>(initialWards);
  const [wardCode, setWardCode] = useState(initialWards[0]?.code ?? "");
  const [loadingWards, setLoadingWards] = useState(false);
  const [streetDetail, setStreetDetail] = useState("");
  const [isDefault, setIsDefault] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [provinceQuery, setProvinceQuery] = useState("");
  const [wardQuery, setWardQuery] = useState("");

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode) ?? provinces[0],
    [provinces, provinceCode]
  );

  const selectedWard = useMemo(
    () => wards.find((w) => w.code === wardCode) ?? wards[0],
    [wards, wardCode]
  );

  const previewProvince = useMemo(
    () => getProvinceByCode(toStorefrontProvinceCode(provinceCode)),
    [provinceCode]
  );

  const provinceSuggestions = useMemo(() => {
    const q = normalizeVietnamese(provinceQuery);
    if (!q) return [];
    return provinces
      .filter((p) => normalizeVietnamese(`${p.name} ${p.code}`).includes(q))
      .slice(0, 6);
  }, [provinceQuery, provinces]);

  const wardSuggestions = useMemo(() => {
    const q = normalizeVietnamese(wardQuery);
    if (!q) return [];
    return wards.filter((w) => normalizeVietnamese(w.name).includes(q)).slice(0, 6);
  }, [wardQuery, wards]);

  const handleProvinceChange = async (nextCode: string) => {
    setProvinceCode(nextCode);
    setWardQuery("");
    setError(null);
    setLoadingWards(true);
    try {
      const res = await api<{ wards: GwWard[] }>(
        `/api/checkout/locations?province_code=${encodeURIComponent(nextCode)}`
      );
      const nextWards = res.wards ?? [];
      setWards(nextWards);
      setWardCode(nextWards[0]?.code ?? "");
    } catch (err) {
      setWards([]);
      setWardCode("");
      setError(
        err instanceof ApiError
          ? err.message
          : "Không tải được danh sách Phường/Xã. Vui lòng thử lại."
      );
    } finally {
      setLoadingWards(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = recipientName.trim();
    const cleanPhone = phone.replace(/\s+/g, "").trim();
    const cleanStreet = streetDetail.trim();

    if (cleanName.length < 2) {
      setError("Vui lòng nhập họ và tên người nhận đầy đủ.");
      return;
    }
    if (!/^0\d{9,10}$/.test(cleanPhone)) {
      setError("Số điện thoại cần gồm 10 hoặc 11 chữ số và bắt đầu bằng số 0.");
      return;
    }
    if (!selectedProvince || !selectedWard) {
      setError("Vui lòng chọn đầy đủ Tỉnh/Thành phố và Phường/Xã.");
      return;
    }
    if (cleanStreet.length < 5) {
      setError("Vui lòng nhập số nhà, tên đường hoặc ngõ cụ thể để giao hàng chính xác.");
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      const res = await api<{ address: BffAddressItem }>("/api/auth/addresses", {
        method: "POST",
        body: {
          recipientName: cleanName,
          recipientPhone: cleanPhone,
          provinceCode: selectedProvince.code,
          provinceName: selectedProvince.name,
          wardCode: selectedWard.code,
          wardName: selectedWard.name,
          streetAddress: cleanStreet,
          isDefault,
        },
      });
      const a = res.address;
      onSaved({
        id: a.id,
        recipientName: a.recipientName,
        phone: a.recipientPhone,
        provinceCode: a.provinceCode,
        provinceName: a.provinceName,
        wardCode: a.wardCode,
        wardName: a.wardName,
        streetDetail: a.streetAddress,
        isDefault: a.isDefault,
      });
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Chưa lưu được địa chỉ lúc này. Vui lòng thử lại."
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form className={styles.whitePanel} onSubmit={handleSubmit} noValidate>
      <div className={styles.formGrid}>
        <div className={styles.field}>
          <label htmlFor="acc-addr-recipient" className={styles.fieldLabel}>
            Họ và tên người nhận
          </label>
          <input
            id="acc-addr-recipient"
            type="text"
            className={styles.input}
            value={recipientName}
            onChange={(e) => setRecipientName(e.target.value)}
            placeholder="Nguyễn Minh Quân"
            required
          />
        </div>

        <div className={styles.field}>
          <label htmlFor="acc-addr-phone" className={styles.fieldLabel}>
            Số điện thoại nhận hàng
          </label>
          <input
            id="acc-addr-phone"
            type="tel"
            className={styles.input}
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            placeholder="0912486305"
            required
          />
        </div>

        <div className={styles.field}>
          <label htmlFor="acc-addr-province-search" className={styles.fieldLabel}>
            Tỉnh / Thành phố
          </label>
          <input
            id="acc-addr-province-search"
            type="search"
            className={styles.input}
            value={provinceQuery}
            onChange={(e) => setProvinceQuery(e.target.value)}
            placeholder="Tìm nhanh: Hà Nội, Đà Nẵng, TP. Hồ Chí Minh..."
          />
          {provinceSuggestions.length > 0 && (
            <div className={styles.quickSuggestList} role="list" aria-label="Gợi ý tỉnh thành">
              {provinceSuggestions.map((p) => (
                <button
                  key={p.code}
                  type="button"
                  className={`${styles.quickSuggestChip} ${
                    p.code === provinceCode ? styles.quickSuggestChipActive : ""
                  }`}
                  onClick={() => {
                    void handleProvinceChange(p.code);
                    setProvinceQuery("");
                  }}
                >
                  {p.name}
                </button>
              ))}
            </div>
          )}
          <select
            id="acc-addr-province"
            aria-label="Chọn Tỉnh hoặc Thành phố"
            className={styles.select}
            value={provinceCode}
            onChange={(e) => void handleProvinceChange(e.target.value)}
            disabled={provinces.length === 0}
          >
            {provinces.map((p) => (
              <option key={p.code} value={p.code}>
                {p.name}
              </option>
            ))}
          </select>
        </div>

        <div className={styles.field}>
          <label htmlFor="acc-addr-ward-search" className={styles.fieldLabel}>
            Phường / Xã
          </label>
          <input
            id="acc-addr-ward-search"
            type="search"
            className={styles.input}
            value={wardQuery}
            onChange={(e) => setWardQuery(e.target.value)}
            placeholder={
              selectedProvince ? `Tìm nhanh trong ${selectedProvince.name}...` : "Tìm Phường/Xã..."
            }
            disabled={loadingWards}
          />
          {wardSuggestions.length > 0 && (
            <div className={styles.quickSuggestList} role="list" aria-label="Gợi ý phường xã">
              {wardSuggestions.map((w) => (
                <button
                  key={w.code}
                  type="button"
                  className={`${styles.quickSuggestChip} ${
                    w.code === wardCode ? styles.quickSuggestChipActive : ""
                  }`}
                  onClick={() => {
                    setWardCode(w.code);
                    setWardQuery("");
                  }}
                >
                  {w.name}
                </button>
              ))}
            </div>
          )}
          <select
            id="acc-addr-ward"
            aria-label="Chọn Phường hoặc Xã"
            className={styles.select}
            value={wardCode}
            onChange={(e) => setWardCode(e.target.value)}
            disabled={loadingWards || wards.length === 0}
          >
            {loadingWards ? (
              <option value="">Đang tải danh sách Phường/Xã...</option>
            ) : wards.length === 0 ? (
              <option value="">Chưa có dữ liệu Phường/Xã</option>
            ) : (
              wards.map((ward) => (
                <option key={ward.code} value={ward.code}>
                  {ward.name}
                </option>
              ))
            )}
          </select>
        </div>

        <div className={`${styles.field} ${styles.formFull}`}>
          <label htmlFor="acc-addr-street" className={styles.fieldLabel}>
            Số nhà, tên đường / ngõ
          </label>
          <input
            id="acc-addr-street"
            type="text"
            className={styles.input}
            value={streetDetail}
            onChange={(e) => setStreetDetail(e.target.value)}
            placeholder="Ví dụ: Số 248 đường Võ Văn Tần"
            required
          />
          <span className={styles.fieldHint}>
            Chọn trực tiếp Tỉnh/Thành phố và Phường/Xã nhận hàng, không cần nhập quận/huyện.
          </span>
        </div>

        {selectedProvince && (
          <div className={styles.formFull}>
            <div className={styles.addressMetaBar}>
              <span>
                Tuyến giao tới <strong>{selectedProvince.name}</strong> (
                {previewProvince?.etaDays ?? "1–3 ngày"})
              </span>
              <span className="num">
                Phí vận chuyển tham khảo:{" "}
                <strong>{formatVnd(previewProvince?.shippingFee ?? 28000)}</strong>
              </span>
            </div>
          </div>
        )}

        <div className={styles.formFull}>
          <label className={styles.checkboxRow}>
            <input
              type="checkbox"
              className={styles.checkbox}
              checked={isDefault}
              onChange={(e) => setIsDefault(e.target.checked)}
            />
            <span>Đặt làm địa chỉ giao hàng mặc định</span>
          </label>
        </div>

        {error && (
          <p className={`${styles.fieldError} ${styles.formFull}`} role="alert">
            {error}
          </p>
        )}

        <div className={`${styles.formActions} ${styles.formFull}`}>
          <Button type="submit" variant="ink" size="md" disabled={submitting || loadingWards}>
            {submitting ? "Đang lưu địa chỉ..." : submitLabel}
          </Button>
          {onCancel && (
            <Button type="button" variant="secondary" size="md" onClick={onCancel}>
              Hủy bỏ
            </Button>
          )}
        </div>
      </div>
    </form>
  );
}
