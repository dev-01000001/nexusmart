"use client";

import React, { useEffect, useMemo, useState } from "react";
import { ApiError, api } from "@/lib/api";
import { normalizeVietnamese } from "@/lib/search";
import { useStore } from "@/context/StoreContext";
import { Button } from "@/components/Button";
import type { GwAddress, GwProvince, GwWard } from "./types";
import styles from "./commerce.module.css";

export interface AddressSubmitPayload {
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
  is_default: boolean;
}

export interface AddressFormProps {
  defaultName?: string;
  defaultPhone?: string;
  onSaved?: (created: GwAddress) => void;
  onSubmitAddress?: (payload: AddressSubmitPayload) => Promise<GwAddress>;
  onCancel?: () => void;
  submitLabel?: string;
}

export function AddressForm({
  defaultName = "",
  defaultPhone = "",
  onSaved,
  onSubmitAddress,
  onCancel,
  submitLabel = "Lưu địa chỉ nhận hàng",
}: AddressFormProps) {
  const { showToast } = useStore();
  const [recipientName, setRecipientName] = useState(defaultName);
  const [phone, setPhone] = useState(defaultPhone);
  const [provinces, setProvinces] = useState<GwProvince[]>([]);
  const [wards, setWards] = useState<GwWard[]>([]);
  const [provinceCode, setProvinceCode] = useState("");
  const [wardCode, setWardCode] = useState("");
  const [streetAddress, setStreetAddress] = useState("");
  const [isDefault, setIsDefault] = useState(true);
  const [loadingProvinces, setLoadingProvinces] = useState(true);
  const [loadingWards, setLoadingWards] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [provinceQuery, setProvinceQuery] = useState("");
  const [wardQuery, setWardQuery] = useState("");

  useEffect(() => {
    let active = true;
    setLoadingProvinces(true);
    api<{ provinces: GwProvince[] }>("/api/checkout/locations")
      .then((res) => {
        if (!active) return;
        const list = res.provinces ?? [];
        setProvinces(list);
        if (list.length > 0) {
          const preferred =
            list.find((p) => p.code === "79") ??
            list.find((p) => p.name.includes("Hà Nội")) ??
            list[0];
          setProvinceCode(preferred.code);
        }
      })
      .catch((err) => {
        if (!active) return;
        setError(
          err instanceof ApiError ? err.message : "Không tải được danh sách Tỉnh/Thành phố lúc này."
        );
      })
      .finally(() => {
        if (active) setLoadingProvinces(false);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!provinceCode) {
      setWards([]);
      setWardCode("");
      return;
    }
    let active = true;
    setLoadingWards(true);
    api<{ wards: GwWard[] }>(
      `/api/checkout/locations?province_code=${encodeURIComponent(provinceCode)}`
    )
      .then((res) => {
        if (!active) return;
        const list = res.wards ?? [];
        setWards(list);
        setWardCode(list[0]?.code ?? "");
      })
      .catch((err) => {
        if (!active) return;
        setError(
          err instanceof ApiError ? err.message : "Không tải được danh sách Phường/Xã lúc này."
        );
      })
      .finally(() => {
        if (active) setLoadingWards(false);
      });
    return () => {
      active = false;
    };
  }, [provinceCode]);

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode),
    [provinces, provinceCode]
  );

  const selectedWard = useMemo(() => wards.find((w) => w.code === wardCode), [wards, wardCode]);

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

  const handleProvinceChange = (nextCode: string) => {
    setProvinceCode(nextCode);
    setWardQuery("");
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    const cleanName = recipientName.trim();
    const cleanPhone = phone.replace(/\s+/g, "").trim();
    const cleanStreet = streetAddress.trim();

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
    if (cleanStreet.length < 4) {
      setError("Vui lòng nhập số nhà, tên đường hoặc ngõ cụ thể để giao hàng chính xác.");
      return;
    }

    const payload: AddressSubmitPayload = {
      recipient_name: cleanName,
      recipient_phone: cleanPhone,
      province_code: selectedProvince.code,
      province_name: selectedProvince.name,
      ward_code: selectedWard.code,
      ward_name: selectedWard.name,
      street_address: cleanStreet,
      is_default: isDefault,
    };

    setError(null);
    setSubmitting(true);
    try {
      const created = onSubmitAddress
        ? await onSubmitAddress(payload)
        : (
            await api<{ address: GwAddress }>("/api/checkout/addresses", {
              method: "POST",
              body: payload,
            })
          ).address;
      showToast(`Đã lưu địa chỉ tại ${selectedWard.name}, ${selectedProvince.name}.`);
      onSaved?.(created);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Chưa thể lưu địa chỉ lúc này. Bạn vui lòng thử lại."
      );
    } finally {
      setSubmitting(false);
    }
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
            disabled={loadingProvinces}
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
                  {p.name}
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
            disabled={loadingProvinces || provinces.length === 0}
          >
            {loadingProvinces && <option value="">Đang tải danh sách tỉnh thành...</option>}
            {provinces.map((p) => (
              <option key={p.code} value={p.code}>
                {p.name}
              </option>
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
            placeholder={
              selectedProvince
                ? `Tìm nhanh trong ${selectedProvince.name}...`
                : "Tìm Phường / Xã..."
            }
            disabled={loadingWards || wards.length === 0}
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
            id="addr-ward"
            aria-label="Chọn Phường hoặc Xã"
            className={styles.select}
            value={wardCode}
            onChange={(e) => setWardCode(e.target.value)}
            disabled={loadingWards || wards.length === 0}
          >
            {loadingWards && <option value="">Đang tải Phường / Xã...</option>}
            {wards.map((w) => (
              <option key={w.code} value={w.code}>
                {w.name}
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
            value={streetAddress}
            onChange={(e) => setStreetAddress(e.target.value)}
            placeholder="Ví dụ: Số 248 đường Võ Văn Tần"
            required
          />
          <span className={styles.fieldHint}>
            Chọn trực tiếp Tỉnh/Thành phố và Phường/Xã nhận hàng để tính đúng phí vận chuyển.
          </span>
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
          <Button type="submit" variant="ink" size="md" disabled={submitting || loadingProvinces}>
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
