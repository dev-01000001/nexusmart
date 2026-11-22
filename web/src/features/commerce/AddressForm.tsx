"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import type { GatewayAddress, GatewayProvince, GatewayWard } from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { normalizeVietnamese } from "@/lib/search";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button } from "@/components/Button";
import styles from "./commerce.module.css";

export interface AddressFormProps {
  initial?: GatewayAddress;
  onSaved: (address: GatewayAddress) => void;
  onCancel?: () => void;
  submitLabel?: string;
}

function mapAddressApiError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "ALREADY_EXISTS") {
      return "Địa chỉ này đã tồn tại trong sổ địa chỉ của bạn.";
    }
    if (err.code === "INVALID_ARGUMENT") {
      return "Thông tin địa chỉ chưa hợp lệ. Vui lòng kiểm tra lại người nhận, số điện thoại và khu vực.";
    }
    if (err.code === "PERMISSION_DENIED") {
      return "Bạn không có quyền chỉnh sửa địa chỉ này.";
    }
  }
  return getErrorMessage(err, "Chưa thể lưu địa chỉ lúc này. Vui lòng thử lại sau ít phút.");
}

export function AddressForm({ initial, onSaved, onCancel, submitLabel }: AddressFormProps) {
  const { user } = useSession();
  const { showToast } = useMockStore();
  const isEditing = Boolean(initial?.id);

  const [recipientName, setRecipientName] = useState(
    initial?.recipient_name ?? user?.full_name ?? ""
  );
  const [recipientPhone, setRecipientPhone] = useState(
    initial?.recipient_phone ?? user?.phone ?? ""
  );
  const [provinceCode, setProvinceCode] = useState(initial?.province_code ?? "");
  const [wardCode, setWardCode] = useState(initial?.ward_code ?? "");
  const [streetAddress, setStreetAddress] = useState(initial?.street_address ?? "");
  const [isDefault, setIsDefault] = useState(initial ? initial.is_default : true);

  const [provinces, setProvinces] = useState<GatewayProvince[]>([]);
  const [loadingProvinces, setLoadingProvinces] = useState(true);
  const [provincesError, setProvincesError] = useState<string | null>(null);

  const [wards, setWards] = useState<GatewayWard[]>([]);
  const [loadingWards, setLoadingWards] = useState(false);
  const [wardsError, setWardsError] = useState<string | null>(null);

  const [provinceQuery, setProvinceQuery] = useState("");
  const [wardQuery, setWardQuery] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadProvinces = useCallback(async () => {
    setLoadingProvinces(true);
    setProvincesError(null);
    try {
      const data = await api<{ provinces: GatewayProvince[] }>("/shipping/provinces");
      const list = Array.isArray(data?.provinces) ? data.provinces : [];
      setProvinces(list);
      setProvinceCode((prev) => {
        if (prev && list.some((p) => p.code === prev)) return prev;
        if (initial?.province_code && list.some((p) => p.code === initial.province_code)) {
          return initial.province_code;
        }
        const hcm = list.find(
          (p) => p.code === "79" || normalizeVietnamese(p.name).includes("ho chi minh")
        );
        return hcm?.code ?? list[0]?.code ?? "";
      });
    } catch (err) {
      setProvincesError(
        getErrorMessage(err, "Không thể tải danh sách Tỉnh/Thành phố. Vui lòng thử lại.")
      );
    } finally {
      setLoadingProvinces(false);
    }
  }, [initial?.province_code]);

  useEffect(() => {
    void loadProvinces();
  }, [loadProvinces]);

  useEffect(() => {
    if (!provinceCode) {
      setWards([]);
      setWardCode("");
      return;
    }

    let cancelled = false;
    const loadWards = async () => {
      setLoadingWards(true);
      setWardsError(null);
      try {
        const data = await api<{ wards: GatewayWard[] }>(
          `/shipping/provinces/${encodeURIComponent(provinceCode)}/wards`
        );
        if (cancelled) return;
        const list = Array.isArray(data?.wards) ? data.wards : [];
        setWards(list);
        setWardCode((prev) => {
          if (prev && list.some((w) => w.code === prev)) return prev;
          if (
            initial?.ward_code &&
            initial.province_code === provinceCode &&
            list.some((w) => w.code === initial.ward_code)
          ) {
            return initial.ward_code;
          }
          return list[0]?.code ?? "";
        });
      } catch (err) {
        if (cancelled) return;
        setWards([]);
        setWardCode("");
        setWardsError(getErrorMessage(err, "Không thể tải danh sách Phường/Xã cho khu vực này."));
      } finally {
        if (!cancelled) {
          setLoadingWards(false);
        }
      }
    };

    void loadWards();
    return () => {
      cancelled = true;
    };
  }, [provinceCode, initial?.province_code, initial?.ward_code]);

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode) ?? null,
    [provinces, provinceCode]
  );

  const selectedWard = useMemo(
    () => wards.find((w) => w.code === wardCode) ?? null,
    [wards, wardCode]
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

  const handleProvinceChange = (nextCode: string) => {
    setProvinceCode(nextCode);
    setWardCode("");
    setWardQuery("");
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;

    const cleanName = recipientName.trim();
    const cleanPhone = recipientPhone.replace(/\s+/g, "").trim();
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
    if (cleanStreet.length < 5) {
      setError("Vui lòng nhập số nhà, tên đường hoặc ngõ cụ thể để giao hàng chính xác.");
      return;
    }

    setError(null);
    setSubmitting(true);

    const payload = {
      recipient_name: cleanName,
      recipient_phone: cleanPhone,
      province_code: selectedProvince.code,
      province_name: selectedProvince.name,
      ward_code: selectedWard.code,
      ward_name: selectedWard.name,
      street_address: cleanStreet,
      is_default: isDefault,
    };

    try {
      const res = isEditing
        ? await api<{ address: GatewayAddress }>(
            `/users/me/addresses/${encodeURIComponent(initial!.id)}`,
            {
              method: "PUT",
              json: payload,
            }
          )
        : await api<{ address: GatewayAddress }>("/users/me/addresses", {
            method: "POST",
            json: payload,
          });

      showToast(
        isEditing
          ? `Đã cập nhật địa chỉ tại ${selectedWard.name}, ${selectedProvince.name}.`
          : `Đã lưu địa chỉ tại ${selectedWard.name}, ${selectedProvince.name}.`
      );
      onSaved(res.address);
    } catch (err) {
      setError(mapAddressApiError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const effectiveSubmitLabel =
    submitLabel ?? (isEditing ? "Cập nhật địa chỉ" : "Lưu địa chỉ nhận hàng");

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
            disabled={submitting}
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
            value={recipientPhone}
            onChange={(e) => setRecipientPhone(e.target.value)}
            placeholder="0912486305"
            disabled={submitting}
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
            placeholder="Tìm nhanh: Hà Nội, Đà Nẵng, Hồ Chí Minh..."
            disabled={loadingProvinces || submitting}
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
            disabled={loadingProvinces || provinces.length === 0 || submitting}
          >
            {loadingProvinces && <option value="">Đang tải danh sách Tỉnh/Thành phố...</option>}
            {!loadingProvinces && provinces.length === 0 && (
              <option value="">Chưa có dữ liệu Tỉnh/Thành phố</option>
            )}
            {provinces.map((p) => (
              <option key={p.code} value={p.code}>
                {p.name}
              </option>
            ))}
          </select>
          {provincesError && (
            <div className={styles.formActions}>
              <span className={styles.fieldError} role="alert">
                {provincesError}
              </span>
              <Button variant="ghost" size="sm" onClick={() => void loadProvinces()}>
                Thử tải lại
              </Button>
            </div>
          )}
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
                : "Chọn Tỉnh/Thành phố trước..."
            }
            disabled={loadingWards || wards.length === 0 || submitting}
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
            disabled={loadingWards || wards.length === 0 || submitting}
          >
            {loadingWards && <option value="">Đang tải Phường/Xã...</option>}
            {!loadingWards && wards.length === 0 && (
              <option value="">Chưa có dữ liệu Phường/Xã</option>
            )}
            {wards.map((ward) => (
              <option key={ward.code} value={ward.code}>
                {ward.name}
              </option>
            ))}
          </select>
          {wardsError && (
            <span className={styles.fieldError} role="alert">
              {wardsError}
            </span>
          )}
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
            disabled={submitting}
            required
          />
          <span className={styles.fieldHint}>
            Chọn trực tiếp Tỉnh/Thành phố và Phường/Xã nhận hàng, không cần nhập quận/huyện.
          </span>
        </div>

        {selectedProvince && selectedWard && (
          <div className={styles.formFull}>
            <div className={styles.addressMetaBar}>
              <span>
                Khu vực giao nhận: <strong>{selectedWard.name}</strong>,{" "}
                <strong>{selectedProvince.name}</strong>
              </span>
              <span>Phí giao hàng sẽ được tính chính xác khi đặt đơn</span>
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
              disabled={submitting}
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
          <Button
            type="submit"
            variant="ink"
            size="md"
            disabled={submitting || loadingProvinces || loadingWards}
          >
            {submitting ? "Đang lưu địa chỉ..." : effectiveSubmitLabel}
          </Button>
          {onCancel && (
            <Button
              type="button"
              variant="secondary"
              size="md"
              onClick={onCancel}
              disabled={submitting}
            >
              Hủy bỏ
            </Button>
          )}
        </div>
      </div>
    </form>
  );
}
