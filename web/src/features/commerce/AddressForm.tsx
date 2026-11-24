"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  GatewayAddress,
  GatewayMapLegacyAddressResponse,
  GatewayProvince,
  GatewayWard,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { normalizeVietnamese } from "@/lib/search";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button } from "@/components/Button";
import {
  type MappedLegacyAddressCodes,
  filterPreparedWards,
  prepareSearchableWards,
  reconcileWardAfterFilter,
  resolveLegacyCodesForPayload,
  resolveLoadedWardSelection,
} from "./addressHelpers";
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

  // Legacy 3-level address mapping state (E15/E16)
  const [legacyPanelOpen, setLegacyPanelOpen] = useState(false);
  const [legacyProvince, setLegacyProvince] = useState("");
  const [legacyDistrict, setLegacyDistrict] = useState("");
  const [legacyWard, setLegacyWard] = useState("");
  const [mappedLegacyCodes, setMappedLegacyCodes] = useState<MappedLegacyAddressCodes | null>(null);
  const [mappingLegacy, setMappingLegacy] = useState(false);
  const [legacyMapError, setLegacyMapError] = useState<string | null>(null);
  const [legacyMapNotice, setLegacyMapNotice] = useState<string | null>(null);
  const pendingMappedWardRef = useRef<string | null>(null);

  const [provinces, setProvinces] = useState<GatewayProvince[]>([]);
  const [loadingProvinces, setLoadingProvinces] = useState(true);
  const [provincesError, setProvincesError] = useState<string | null>(null);

  const [wards, setWards] = useState<GatewayWard[]>([]);
  const [loadingWards, setLoadingWards] = useState(false);
  const [wardsError, setWardsError] = useState<string | null>(null);

  const [provinceQuery, setProvinceQuery] = useState("");
  const [wardQuery, setWardQuery] = useState("");
  const [legacyWardInactiveNotice, setLegacyWardInactiveNotice] = useState<string | null>(null);
  const [wardFilterClearedNotice, setWardFilterClearedNotice] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadProvinces = useCallback(async () => {
    setLoadingProvinces(true);
    setProvincesError(null);
    try {
      const data = await api<{ provinces: GatewayProvince[] }>("/shipping/provinces", {
        retryOn401: false,
      });
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
          `/shipping/provinces/${encodeURIComponent(provinceCode)}/wards`,
          { retryOn401: false }
        );
        if (cancelled) return;
        const list = Array.isArray(data?.wards) ? data.wards : [];
        setWards(list);
        const mappedWard = pendingMappedWardRef.current;
        pendingMappedWardRef.current = null;
        setWardCode((prev) => {
          const resolved = resolveLoadedWardSelection({
            wards: list,
            mappedWardCode: mappedWard,
            currentWardCode: prev,
            initialProvinceCode: initial?.province_code,
            initialWardCode: initial?.ward_code,
            provinceCode,
          });
          setLegacyWardInactiveNotice(
            resolved.legacyWardInactive
              ? "Phường/xã cũ không còn trong danh mục mới, chọn lại."
              : null
          );
          return resolved.wardCode;
        });
        setWardFilterClearedNotice(null);
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
      .slice(0, 8);
  }, [provinceQuery, provinces]);

  const preparedWards = useMemo(() => prepareSearchableWards(wards), [wards]);
  const filteredWards = useMemo(
    () => filterPreparedWards(preparedWards, wardQuery),
    [preparedWards, wardQuery]
  );

  const handleProvinceChange = (nextCode: string) => {
    pendingMappedWardRef.current = null;
    setMappedLegacyCodes(null);
    setProvinceCode(nextCode);
    setWardCode("");
    setWardQuery("");
    setLegacyWardInactiveNotice(null);
    setWardFilterClearedNotice(null);
  };

  const handleWardQueryChange = (nextQuery: string) => {
    setWardQuery(nextQuery);
    const nextFiltered = filterPreparedWards(preparedWards, nextQuery);
    const reconciled = reconcileWardAfterFilter(nextFiltered, wardCode);
    if (reconciled.clearedByFilter) {
      setMappedLegacyCodes(null);
      setWardCode("");
      setWardFilterClearedNotice(
        "Phường/xã đang chọn không nằm trong kết quả lọc mới — vui lòng chọn lại phường/xã bên dưới."
      );
    } else if (wardFilterClearedNotice && nextFiltered.length > 0 && !nextQuery.trim()) {
      setWardFilterClearedNotice(null);
    }
  };

  const handleMapLegacyAddress = async () => {
    const cleanProv = legacyProvince.trim();
    const cleanDist = legacyDistrict.trim();
    const cleanWard = legacyWard.trim();
    if (!cleanProv || !cleanWard) {
      setLegacyMapError("Vui lòng nhập ít nhất Tỉnh/Thành phố cũ và Phường/Xã cũ để quy đổi.");
      setLegacyMapNotice(null);
      return;
    }

    setLegacyMapError(null);
    setLegacyMapNotice(null);
    setMappingLegacy(true);
    try {
      const res = await api<GatewayMapLegacyAddressResponse>("shipping/addresses/map-legacy", {
        method: "POST",
        json: {
          legacy_province: cleanProv,
          legacy_district: cleanDist,
          legacy_ward: cleanWard,
        },
        retryOn401: false,
      });

      if (!res?.found || !res.province_code || !res.ward_code) {
        setMappedLegacyCodes(null);
        setLegacyMapError(
          "Chưa tìm thấy địa chỉ tương ứng trong bảng quy đổi. Vui lòng chọn trực tiếp Tỉnh/Thành phố và Phường/Xã mới bên dưới."
        );
        return;
      }

      setMappedLegacyCodes({
        provinceCode: (res.legacy_province_code ?? "").trim(),
        districtCode: (res.legacy_district_code ?? "").trim(),
        wardCode: (res.legacy_ward_code ?? "").trim(),
        targetProvinceCode: res.province_code,
        targetWardCode: res.ward_code,
      });
      pendingMappedWardRef.current = res.ward_code;
      setWardQuery("");
      setLegacyWardInactiveNotice(null);
      setWardFilterClearedNotice(null);
      if (res.province_code === provinceCode) {
        setWardCode(res.ward_code);
        pendingMappedWardRef.current = null;
      } else {
        setProvinceCode(res.province_code);
      }
      setLegacyMapNotice(
        `Đã quy đổi sang địa chỉ mới: ${res.ward_name}, ${res.province_name}. Bạn có thể kiểm tra lại trước khi lưu.`
      );
      showToast(`Đã quy đổi sang ${res.ward_name}, ${res.province_name}.`);
    } catch (err) {
      setMappedLegacyCodes(null);
      setLegacyMapError(
        getErrorMessage(
          err,
          "Không thể tra cứu quy đổi địa chỉ cũ lúc này. Vui lòng chọn trực tiếp Tỉnh/Phường mới."
        )
      );
    } finally {
      setMappingLegacy(false);
    }
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

    const legacyCodesPayload = resolveLegacyCodesForPayload(
      mappedLegacyCodes,
      selectedProvince.code,
      selectedWard.code
    );

    const payload = {
      recipient_name: cleanName,
      recipient_phone: cleanPhone,
      province_code: selectedProvince.code,
      province_name: selectedProvince.name,
      ward_code: selectedWard.code,
      ward_name: selectedWard.name,
      street_address: cleanStreet,
      is_default: isDefault,
      ...legacyCodesPayload,
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

        <div className={`${styles.field} ${styles.formFull}`}>
          <div className={styles.formActions} style={{ marginTop: 0 }}>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              disabled={submitting}
              onClick={() => {
                setLegacyPanelOpen((v) => !v);
                setLegacyMapError(null);
              }}
            >
              {legacyPanelOpen
                ? "Ẩn công cụ quy đổi địa chỉ cũ"
                : "Quy đổi địa chỉ cũ (3 cấp Tỉnh / Quận / Phường)"}
            </Button>
          </div>

          {legacyPanelOpen && (
            <div className={styles.legacyMapBox}>
              <p className={styles.fieldHint}>
                Nhập địa chỉ hành chính cũ (trước sáp nhập 34 tỉnh/thành) để hệ thống tự điền
                Tỉnh/Thành phố và Phường/Xã mới.
              </p>
              <div className={styles.legacyMapGrid}>
                <div className={styles.field}>
                  <label htmlFor="addr-legacy-province" className={styles.fieldLabel}>
                    Tỉnh / Thành phố cũ
                  </label>
                  <input
                    id="addr-legacy-province"
                    type="text"
                    className={styles.input}
                    value={legacyProvince}
                    onChange={(e) => setLegacyProvince(e.target.value)}
                    placeholder="Ví dụ: TP. Hồ Chí Minh, Bình Dương..."
                    disabled={mappingLegacy || submitting}
                  />
                </div>
                <div className={styles.field}>
                  <label htmlFor="addr-legacy-district" className={styles.fieldLabel}>
                    Quận / Huyện cũ
                  </label>
                  <input
                    id="addr-legacy-district"
                    type="text"
                    className={styles.input}
                    value={legacyDistrict}
                    onChange={(e) => setLegacyDistrict(e.target.value)}
                    placeholder="Ví dụ: Quận 1, Thành phố Thủ Đức..."
                    disabled={mappingLegacy || submitting}
                  />
                </div>
                <div className={styles.field}>
                  <label htmlFor="addr-legacy-ward" className={styles.fieldLabel}>
                    Phường / Xã cũ
                  </label>
                  <input
                    id="addr-legacy-ward"
                    type="text"
                    className={styles.input}
                    value={legacyWard}
                    onChange={(e) => setLegacyWard(e.target.value)}
                    placeholder="Ví dụ: Phường Bến Nghé..."
                    disabled={mappingLegacy || submitting}
                  />
                </div>
              </div>
              {legacyMapError && (
                <p className={styles.fieldError} role="alert">
                  {legacyMapError}
                </p>
              )}
              {legacyMapNotice && (
                <p className={`${styles.feedbackBox} ${styles.feedbackSuccess}`} role="status">
                  {legacyMapNotice}
                </p>
              )}
              <div className={styles.formActions} style={{ marginTop: 0 }}>
                <Button
                  type="button"
                  variant="ink"
                  size="sm"
                  disabled={mappingLegacy || submitting}
                  onClick={() => void handleMapLegacyAddress()}
                >
                  {mappingLegacy ? "Đang quy đổi..." : "Quy đổi sang địa chỉ mới"}
                </Button>
              </div>
            </div>
          )}
        </div>

        <div className={styles.field}>
          <label htmlFor="addr-province-search" className={styles.fieldLabel}>
            Tỉnh / Thành phố ({provinces.length > 0 ? `${provinces.length} tỉnh/thành` : "mới"})
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
            Phường / Xã {wards.length > 0 ? `(${wards.length} đơn vị)` : ""}
          </label>
          <input
            id="addr-ward-search"
            type="search"
            className={styles.input}
            value={wardQuery}
            onChange={(e) => handleWardQueryChange(e.target.value)}
            placeholder={
              selectedProvince
                ? `Lọc nhanh phường/xã trong ${selectedProvince.name}...`
                : "Chọn Tỉnh/Thành phố trước..."
            }
            disabled={loadingWards || wards.length === 0 || submitting}
          />
          <select
            id="addr-ward"
            aria-label="Chọn Phường hoặc Xã"
            className={styles.select}
            value={wardCode}
            onChange={(e) => {
              setMappedLegacyCodes(null);
              setWardCode(e.target.value);
              if (e.target.value) {
                setLegacyWardInactiveNotice(null);
                setWardFilterClearedNotice(null);
              }
            }}
            disabled={loadingWards || wards.length === 0 || submitting}
            required
          >
            {loadingWards ? (
              <option value="">Đang tải Phường/Xã...</option>
            ) : wards.length === 0 ? (
              <option value="">Chưa có dữ liệu Phường/Xã</option>
            ) : (
              <option value="">Chọn phường/xã</option>
            )}
            {filteredWards.map((ward) => (
              <option key={ward.code} value={ward.code}>
                {ward.cod_supported === false ? `${ward.name} (Chưa hỗ trợ COD)` : ward.name}
              </option>
            ))}
          </select>
          {!loadingWards && wards.length > 0 && (
            <span className={styles.fieldHint} aria-live="polite">
              {wardQuery.trim()
                ? filteredWards.length === 0
                  ? `Không tìm thấy phường/xã khớp "${wardQuery.trim()}".`
                  : `Tìm thấy ${filteredWards.length}/${wards.length} phường/xã phù hợp.`
                : `${wards.length} phường/xã sẵn có.`}
            </span>
          )}
          {legacyWardInactiveNotice && (
            <span className={styles.fieldError} role="alert">
              {legacyWardInactiveNotice}
            </span>
          )}
          {wardFilterClearedNotice && (
            <span className={styles.fieldError} role="alert">
              {wardFilterClearedNotice}
            </span>
          )}
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
            Chọn trực tiếp Tỉnh/Thành phố và Phường/Xã theo danh mục hành chính 2 cấp mới (hoặc dùng
            công cụ quy đổi địa chỉ cũ phía trên).
          </span>
        </div>

        {selectedProvince && selectedWard && (
          <div className={styles.formFull}>
            <div className={styles.addressMetaBar}>
              <span>
                Khu vực giao nhận: <strong>{selectedWard.name}</strong>,{" "}
                <strong>{selectedProvince.name}</strong>
              </span>
              <span>
                {selectedWard.cod_supported === false
                  ? "Khu vực này hiện chỉ hỗ trợ thanh toán VietQR (chưa hỗ trợ COD)"
                  : "Phí giao hàng sẽ được tính chính xác khi đặt đơn"}
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
