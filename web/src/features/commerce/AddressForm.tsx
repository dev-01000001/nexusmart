"use client";

import React, { useMemo, useState } from "react";
import { PROVINCES, REGION_LABELS, getProvinceByCode } from "@/lib/provinces";
import { normalizeVietnamese } from "@/lib/search";
import { formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import type { UserAddress } from "@/data";
import { Button } from "@/components/Button";
import styles from "./commerce.module.css";

export interface AddressFormProps {
  onSaved?: (created: UserAddress) => void;
  onCancel?: () => void;
  submitLabel?: string;
}

export function AddressForm({
  onSaved,
  onCancel,
  submitLabel = "Lưu địa chỉ nhận hàng",
}: AddressFormProps) {
  const { addAddress, user, showToast } = useStore();
  const [recipientName, setRecipientName] = useState(user.fullName);
  const [phone, setPhone] = useState(user.phone);
  const [provinceCode, setProvinceCode] = useState("HCM");
  const initialProvince = getProvinceByCode("HCM") ?? PROVINCES[0];
  const [wardName, setWardName] = useState(initialProvince.wards[0] ?? "");
  const [streetDetail, setStreetDetail] = useState("");
  const [isDefault, setIsDefault] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [provinceQuery, setProvinceQuery] = useState("");
  const [wardQuery, setWardQuery] = useState("");

  const selectedProvince = useMemo(
    () => getProvinceByCode(provinceCode) ?? PROVINCES[0],
    [provinceCode]
  );

  const provinceSuggestions = useMemo(() => {
    const q = normalizeVietnamese(provinceQuery);
    if (!q) return [];
    return PROVINCES.filter((p) => {
      const hay = normalizeVietnamese(`${p.name} ${p.shortName} ${p.code}`);
      return hay.includes(q);
    }).slice(0, 6);
  }, [provinceQuery]);

  const wardSuggestions = useMemo(() => {
    const q = normalizeVietnamese(wardQuery);
    if (!q) return [];
    return selectedProvince.wards.filter((w) => normalizeVietnamese(w).includes(q)).slice(0, 6);
  }, [wardQuery, selectedProvince]);

  const handleProvinceChange = (nextCode: string) => {
    setProvinceCode(nextCode);
    const nextProvince = getProvinceByCode(nextCode);
    setWardName(nextProvince?.wards[0] ?? "");
    setWardQuery("");
  };

  const handleSubmit = (e: React.FormEvent) => {
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
    if (!selectedProvince || !wardName) {
      setError("Vui lòng chọn đầy đủ Tỉnh/Thành phố và Phường/Xã.");
      return;
    }
    if (cleanStreet.length < 5) {
      setError("Vui lòng nhập số nhà, tên đường hoặc ngõ cụ thể để giao hàng chính xác.");
      return;
    }

    setError(null);
    const created = addAddress({
      recipientName: cleanName,
      phone: phone.trim(),
      provinceCode: selectedProvince.code,
      provinceName: selectedProvince.name,
      wardName,
      streetDetail: cleanStreet,
      isDefault,
    });
    showToast(`Đã lưu địa chỉ tại ${wardName}, ${selectedProvince.shortName}.`);
    onSaved?.(created);
  };

  return (
    <form className={styles.whitePanel} onSubmit={handleSubmit} noValidate>
      <div className={styles.formGrid}>
        <div className={styles.field}>
          <label htmlFor="addr-recipient" className={styles.fieldLabel}>
            Họ và tên người nhận
          </label>
          <input
            id="addr-recipient"
            type="text"
            className={styles.input}
            value={recipientName}
            onChange={(e) => setRecipientName(e.target.value)}
            placeholder="Nguyễn Minh Quân"
            required
          />
        </div>

        <div className={styles.field}>
          <label htmlFor="addr-phone" className={styles.fieldLabel}>
            Số điện thoại nhận hàng
          </label>
          <input
            id="addr-phone"
            type="tel"
            className={styles.input}
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            placeholder="0912 486 305"
            required
          />
        </div>

        <div className={styles.field}>
          <label htmlFor="addr-province-search" className={styles.fieldLabel}>
            Tỉnh / Thành phố
          </label>
          <input
            id="addr-province-search"
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
                    handleProvinceChange(p.code);
                    setProvinceQuery("");
                  }}
                >
                  {p.shortName}
                </button>
              ))}
            </div>
          )}
          <select
            id="addr-province"
            aria-label="Chọn Tỉnh hoặc Thành phố"
            className={styles.select}
            value={provinceCode}
            onChange={(e) => handleProvinceChange(e.target.value)}
          >
            {(["bac", "trung", "nam"] as const).map((region) => (
              <optgroup key={region} label={REGION_LABELS[region]}>
                {PROVINCES.filter((p) => p.region === region).map((p) => (
                  <option key={p.code} value={p.code}>
                    {p.name}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </div>

        <div className={styles.field}>
          <label htmlFor="addr-ward-search" className={styles.fieldLabel}>
            Phường / Xã
          </label>
          <input
            id="addr-ward-search"
            type="search"
            className={styles.input}
            value={wardQuery}
            onChange={(e) => setWardQuery(e.target.value)}
            placeholder={`Tìm nhanh trong ${selectedProvince.shortName}...`}
          />
          {wardSuggestions.length > 0 && (
            <div className={styles.quickSuggestList} role="list" aria-label="Gợi ý phường xã">
              {wardSuggestions.map((w) => (
                <button
                  key={w}
                  type="button"
                  className={`${styles.quickSuggestChip} ${
                    w === wardName ? styles.quickSuggestChipActive : ""
                  }`}
                  onClick={() => {
                    setWardName(w);
                    setWardQuery("");
                  }}
                >
                  {w}
                </button>
              ))}
            </div>
          )}
          <select
            id="addr-ward"
            aria-label="Chọn Phường hoặc Xã"
            className={styles.select}
            value={wardName}
            onChange={(e) => setWardName(e.target.value)}
          >
            {selectedProvince.wards.map((ward) => (
              <option key={ward} value={ward}>
                {ward}
              </option>
            ))}
          </select>
        </div>

        <div className={`${styles.field} ${styles.formFull}`}>
          <label htmlFor="addr-street" className={styles.fieldLabel}>
            Số nhà, tên đường / ngõ
          </label>
          <input
            id="addr-street"
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

        <div className={styles.formFull}>
          <div className={styles.addressMetaBar}>
            <span>
              Tuyến giao tới <strong>{selectedProvince.name}</strong> ({selectedProvince.etaDays})
            </span>
            <span className="num">
              Phí vận chuyển chuẩn: <strong>{formatVnd(selectedProvince.shippingFee)}</strong>
            </span>
          </div>
        </div>

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
          <Button type="submit" variant="ink" size="md">
            {submitLabel}
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
