"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/Button";
import {
  filterPreparedWards,
  prepareSearchableWards,
  reconcileWardAfterFilter,
} from "./addressHelpers";
import { focusAndShakeInvalid } from "./formFeedback";
import styles from "./commerce.module.css";

export interface LocationOption {
  code: string;
  name: string;
  cod_supported?: boolean;
}

export interface AddressFieldValues {
  recipientName: string;
  phone: string;
  streetAddress: string;
  isDefault: boolean;
}

export interface AddressFieldsFormProps {
  idPrefix: string;
  fieldNames: {
    recipient: string;
    phone: string;
    province: string;
    ward: string;
    street: string;
  };
  initialValues: AddressFieldValues;
  provinces: LocationOption[];
  wards: LocationOption[];
  provinceCode: string;
  wardCode: string;
  loadingProvinces?: boolean;
  loadingWards: boolean;
  onProvinceChange: (code: string) => void;
  onWardChange: (code: string) => void;
  onWardFilterCleared?: () => void;
  legacyWardInactiveNotice?: string | null;
  minStreetLength?: number;
  formClassName?: string;
  submitVariant?: "ink" | "primary";
  submitLabel: string;
  loadingLabel: string;
  submitDisabled?: boolean;
  submitting: boolean;
  externalError?: string | null;
  errorSignal?: number;
  topSlot?: React.ReactNode;
  metaSlot?: React.ReactNode;
  onCancel?: () => void;
  onValidSubmit: (values: AddressFieldValues) => void | Promise<void>;
}

type FieldKey = "recipient" | "phone" | "location" | "street";

/**
 * Shared address fields, client validation, unified province/ward selects (3,321-ward safe:
 * never auto-selects wards[0], clears ward when excluded by search query, warns when ward
 * has cod_supported === false), and focus/shake feedback used by checkout & account forms.
 */
export function AddressFieldsForm({
  idPrefix,
  fieldNames,
  initialValues,
  provinces,
  wards,
  provinceCode,
  wardCode,
  loadingProvinces = false,
  loadingWards,
  onProvinceChange,
  onWardChange,
  onWardFilterCleared,
  legacyWardInactiveNotice = null,
  minStreetLength = 4,
  formClassName,
  submitVariant = "ink",
  submitLabel,
  loadingLabel,
  submitDisabled = false,
  submitting,
  externalError = null,
  errorSignal = 0,
  topSlot,
  metaSlot,
  onCancel,
  onValidSubmit,
}: AddressFieldsFormProps) {
  const formRef = useRef<HTMLFormElement | null>(null);
  const [recipientName, setRecipientName] = useState(initialValues.recipientName);
  const [phone, setPhone] = useState(initialValues.phone);
  const [streetAddress, setStreetAddress] = useState(initialValues.streetAddress);
  const [isDefault, setIsDefault] = useState(initialValues.isDefault);
  const [wardQuery, setWardQuery] = useState("");
  const [wardFilterClearedNotice, setWardFilterClearedNotice] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<FieldKey, string>>>({});
  const [errorPulse, setErrorPulse] = useState(0);

  useEffect(() => {
    if (errorPulse > 0 || errorSignal > 0) {
      focusAndShakeInvalid(formRef.current);
    }
  }, [errorPulse, errorSignal]);

  // Reset ward search query when province changes
  useEffect(() => {
    setWardQuery("");
    setWardFilterClearedNotice(null);
  }, [provinceCode]);

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode),
    [provinces, provinceCode]
  );

  const selectedWard = useMemo(() => wards.find((w) => w.code === wardCode), [wards, wardCode]);

  const preparedWards = useMemo(() => prepareSearchableWards(wards), [wards]);
  const filteredWards = useMemo(
    () => filterPreparedWards(preparedWards, wardQuery),
    [preparedWards, wardQuery]
  );

  const hasWardFilterMiss = Boolean(wardQuery.trim()) && filteredWards.length === 0;

  const clearFieldError = (key: FieldKey) => {
    setFieldErrors((prev) => {
      if (!prev[key]) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const handleProvinceSelect = (nextCode: string) => {
    setWardQuery("");
    setWardFilterClearedNotice(null);
    clearFieldError("location");
    onProvinceChange(nextCode);
  };

  const handleWardFilterChange = (nextQuery: string) => {
    setWardQuery(nextQuery);
    const nextFiltered = filterPreparedWards(preparedWards, nextQuery);
    const reconciled = reconcileWardAfterFilter(nextFiltered, wardCode);
    if (reconciled.clearedByFilter) {
      onWardChange("");
      onWardFilterCleared?.();
      setWardFilterClearedNotice(
        "Phường/xã đang chọn không nằm trong kết quả lọc mới — vui lòng chọn lại phường/xã bên dưới."
      );
    } else if (wardFilterClearedNotice && nextFiltered.length > 0 && !nextQuery.trim()) {
      setWardFilterClearedNotice(null);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    const cleanName = recipientName.trim();
    const cleanPhone = phone.replace(/\s+/g, "").trim();
    const cleanStreet = streetAddress.trim();

    const nextErrors: Partial<Record<FieldKey, string>> = {};
    if (cleanName.length < 2) {
      nextErrors.recipient = "Nhập họ và tên người nhận.";
    }
    if (!/^0\d{9,10}$/.test(cleanPhone)) {
      nextErrors.phone = "Số điện thoại cần 10 hoặc 11 chữ số, bắt đầu bằng 0.";
    }
    if (!selectedProvince || !selectedWard || hasWardFilterMiss) {
      nextErrors.location = "Chọn đầy đủ Tỉnh/Thành phố và Phường/Xã.";
    }
    if (cleanStreet.length < minStreetLength) {
      nextErrors.street = "Nhập số nhà, tên đường cụ thể.";
    }

    if (Object.keys(nextErrors).length > 0) {
      setFieldErrors(nextErrors);
      setErrorPulse((n) => n + 1);
      return;
    }

    setFieldErrors({});
    await onValidSubmit({
      recipientName: cleanName,
      phone: cleanPhone,
      streetAddress: cleanStreet,
      isDefault,
    });
  };

  const recId = `${idPrefix}-recipient`;
  const phoneId = `${idPrefix}-phone`;
  const provId = `${idPrefix}-province`;
  const wardId = `${idPrefix}-ward`;
  const streetId = `${idPrefix}-street`;
  const locErrId = `${idPrefix}-location-error`;

  return (
    <form ref={formRef} className={formClassName} onSubmit={handleSubmit} noValidate>
      <div className={styles.formGrid}>
        <div className={styles.field} data-shake-target="">
          <label htmlFor={recId} className={styles.fieldLabel}>
            Họ và tên người nhận
          </label>
          <input
            id={recId}
            name={fieldNames.recipient}
            type="text"
            autoComplete="name"
            className={`${styles.input} ${fieldErrors.recipient ? styles.inputInvalid : ""}`}
            aria-invalid={Boolean(fieldErrors.recipient) || undefined}
            aria-describedby={fieldErrors.recipient ? `${recId}-error` : undefined}
            value={recipientName}
            onChange={(e) => {
              setRecipientName(e.target.value);
              clearFieldError("recipient");
            }}
            placeholder="Nguyễn Minh Quân"
            required
          />
          {fieldErrors.recipient && (
            <p id={`${recId}-error`} className={styles.fieldError} role="alert">
              {fieldErrors.recipient}
            </p>
          )}
        </div>

        <div className={styles.field} data-shake-target="">
          <label htmlFor={phoneId} className={styles.fieldLabel}>
            Số điện thoại
          </label>
          <input
            id={phoneId}
            name={fieldNames.phone}
            type="tel"
            autoComplete="tel"
            className={`${styles.input} ${fieldErrors.phone ? styles.inputInvalid : ""}`}
            aria-invalid={Boolean(fieldErrors.phone) || undefined}
            aria-describedby={fieldErrors.phone ? `${phoneId}-error` : undefined}
            value={phone}
            onChange={(e) => {
              setPhone(e.target.value);
              clearFieldError("phone");
            }}
            placeholder="0912 486 305"
            required
          />
          {fieldErrors.phone && (
            <p id={`${phoneId}-error`} className={styles.fieldError} role="alert">
              {fieldErrors.phone}
            </p>
          )}
        </div>

        {topSlot}

        <div className={styles.field} data-shake-target="">
          <label htmlFor={provId} className={styles.fieldLabel}>
            Tỉnh / Thành phố ({provinces.length > 0 ? `${provinces.length} tỉnh/thành` : "mới"})
          </label>
          <select
            id={provId}
            name={fieldNames.province}
            autoComplete="address-level1"
            aria-label="Chọn Tỉnh hoặc Thành phố"
            aria-invalid={Boolean(fieldErrors.location) || undefined}
            aria-describedby={fieldErrors.location ? locErrId : undefined}
            className={`${styles.select} ${fieldErrors.location ? styles.selectInvalid : ""}`}
            value={provinceCode}
            onChange={(e) => handleProvinceSelect(e.target.value)}
            disabled={loadingProvinces || provinces.length === 0}
          >
            {loadingProvinces && <option value="">Đang tải tỉnh thành...</option>}
            {!loadingProvinces && provinces.length === 0 && (
              <option value="">Chưa có dữ liệu</option>
            )}
            {provinces.map((p) => (
              <option key={p.code} value={p.code}>
                {p.name}
              </option>
            ))}
          </select>
        </div>

        <div className={styles.field} data-shake-target="">
          <div className={styles.fieldLabelRow}>
            <label htmlFor={wardId} className={styles.fieldLabel}>
              Phường / Xã
              {!loadingWards && wards.length > 0 && (
                <span className="num" aria-live="polite">
                  {" "}
                  ({filteredWards.length}/{wards.length})
                </span>
              )}
            </label>
            {wardQuery && (
              <button
                type="button"
                className={styles.fieldInlineAction}
                onClick={() => handleWardFilterChange("")}
              >
                Xóa lọc
              </button>
            )}
          </div>
          <div
            className={`${styles.unifiedSelectBox} ${
              fieldErrors.location ? styles.unifiedSelectBoxInvalid : ""
            }`}
          >
            {!loadingWards && wards.length > 10 && (
              <input
                id={`${idPrefix}-ward-search`}
                type="search"
                aria-label="Lọc danh sách phường xã"
                className={styles.unifiedFilterInput}
                value={wardQuery}
                onChange={(e) => handleWardFilterChange(e.target.value)}
                placeholder={
                  selectedProvince
                    ? `Gõ tên phường/xã tại ${selectedProvince.name}…`
                    : "Gõ tên phường/xã…"
                }
                disabled={loadingWards || wards.length === 0 || submitting}
              />
            )}
            <select
              id={wardId}
              name={fieldNames.ward}
              autoComplete="address-level2"
              aria-label="Chọn Phường hoặc Xã"
              aria-invalid={Boolean(fieldErrors.location) || undefined}
              aria-describedby={fieldErrors.location ? locErrId : undefined}
              className={styles.unifiedSelect}
              value={hasWardFilterMiss ? "" : wardCode}
              onChange={(e) => {
                onWardChange(e.target.value);
                setWardFilterClearedNotice(null);
                clearFieldError("location");
              }}
              disabled={loadingWards || wards.length === 0 || submitting}
            >
              {loadingWards ? (
                <option value="">Đang tải phường xã...</option>
              ) : wards.length === 0 ? (
                <option value="">Chưa có dữ liệu</option>
              ) : hasWardFilterMiss ? (
                <option value="" disabled>
                  Không tìm thấy phường/xã phù hợp
                </option>
              ) : (
                <>
                  <option value="">Chọn phường/xã</option>
                  {filteredWards.map((w) => (
                    <option key={w.code} value={w.code}>
                      {w.name}
                    </option>
                  ))}
                </>
              )}
            </select>
          </div>
          {legacyWardInactiveNotice && (
            <p className={styles.fieldHint} role="status">
              {legacyWardInactiveNotice}
            </p>
          )}
          {wardFilterClearedNotice && (
            <p className={styles.fieldHint} role="status">
              {wardFilterClearedNotice}
            </p>
          )}
          {selectedWard?.cod_supported === false && (
            <p className={`${styles.feedbackBox} ${styles.feedbackWarn}`} role="status">
              Khu vực này hiện chưa hỗ trợ thanh toán khi nhận hàng (COD). Bạn vẫn có thể thanh toán
              trực tuyến bằng VietQR hoặc Ví ZaloPay.
            </p>
          )}
        </div>

        {fieldErrors.location && (
          <p id={locErrId} className={`${styles.fieldError} ${styles.formFull}`} role="alert">
            {fieldErrors.location}
          </p>
        )}

        <div className={`${styles.field} ${styles.formFull}`} data-shake-target="">
          <label htmlFor={streetId} className={styles.fieldLabel}>
            Số nhà, tên đường
          </label>
          <input
            id={streetId}
            name={fieldNames.street}
            type="text"
            autoComplete="street-address"
            className={`${styles.input} ${fieldErrors.street ? styles.inputInvalid : ""}`}
            aria-invalid={Boolean(fieldErrors.street) || undefined}
            aria-describedby={fieldErrors.street ? `${streetId}-error` : undefined}
            value={streetAddress}
            onChange={(e) => {
              setStreetAddress(e.target.value);
              clearFieldError("street");
            }}
            placeholder="Số 248 Võ Văn Tần"
            required
          />
          {fieldErrors.street && (
            <p id={`${streetId}-error`} className={styles.fieldError} role="alert">
              {fieldErrors.street}
            </p>
          )}
        </div>

        {metaSlot}

        <div className={styles.formFull}>
          <label className={styles.checkboxRow}>
            <input
              type="checkbox"
              className={styles.checkbox}
              checked={isDefault}
              onChange={(e) => setIsDefault(e.target.checked)}
            />
            <span>Đặt làm địa chỉ mặc định</span>
          </label>
        </div>

        {externalError && (
          <p className={`${styles.fieldError} ${styles.formFull}`} data-shake-alert="" role="alert">
            {externalError}
          </p>
        )}

        <div className={`${styles.formActions} ${styles.formFull}`}>
          <Button
            type="submit"
            variant={submitVariant}
            size="md"
            className={styles.wrapBtn}
            loading={submitting}
            loadingLabel={loadingLabel}
            disabled={submitDisabled}
          >
            {submitLabel}
          </Button>
          {onCancel && (
            <Button type="button" variant="secondary" size="md" onClick={onCancel}>
              Hủy
            </Button>
          )}
        </div>
      </div>
    </form>
  );
}
