"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { AlertTriangle, ChevronsUpDown, Loader2 } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { filterPreparedWards, prepareSearchableWards } from "./addressHelpers";
import { focusAndShakeInvalid } from "./formFeedback";
import type { LocationComboboxProps } from "./ui/LocationCombobox";

const LocationCombobox = dynamic<LocationComboboxProps>(
  () => import("./ui/LocationCombobox").then((m) => m.LocationCombobox),
  {
    ssr: false,
    loading: () => (
      <div className="flex min-h-10 w-full items-center justify-between rounded-md border border-input bg-card px-3 text-sm text-muted-foreground">
        <span>Đang tải danh sách...</span>
        <ChevronsUpDown className="size-4 shrink-0 opacity-50" aria-hidden="true" />
      </div>
    ),
  }
);

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
 * Shared address fields, client validation, searchable 2-level province/ward pickers
 * (3,321-ward safe: never auto-selects wards[0], diacritic-insensitive search via Popover + Command,
 * warns when ward has cod_supported === false), and focus/shake feedback used by checkout & account forms.
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
  onWardFilterCleared: _onWardFilterCleared,
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
  void _onWardFilterCleared;
  const formRef = useRef<HTMLFormElement | null>(null);
  const [recipientName, setRecipientName] = useState(initialValues.recipientName);
  const [phone, setPhone] = useState(initialValues.phone);
  const [streetAddress, setStreetAddress] = useState(initialValues.streetAddress);
  const [isDefault, setIsDefault] = useState(initialValues.isDefault);
  const [wardQuery, setWardQuery] = useState("");
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
    clearFieldError("location");
    onProvinceChange(nextCode);
  };

  const handleWardSelect = (nextCode: string) => {
    onWardChange(nextCode);
    setWardQuery("");
    clearFieldError("location");
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
    if (!selectedProvince || !selectedWard) {
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
  const defaultId = `${idPrefix}-default`;
  const locErrId = `${idPrefix}-location-error`;

  return (
    <form ref={formRef} className={cn("w-full", formClassName)} onSubmit={handleSubmit} noValidate>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Field
          className="gap-1.5"
          data-invalid={Boolean(fieldErrors.recipient) || undefined}
          data-shake-target=""
        >
          <FieldLabel htmlFor={recId}>Họ và tên người nhận</FieldLabel>
          <Input
            id={recId}
            name={fieldNames.recipient}
            type="text"
            autoComplete="name"
            className="min-h-10 bg-card"
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
          {fieldErrors.recipient ? (
            <FieldError id={`${recId}-error`}>{fieldErrors.recipient}</FieldError>
          ) : null}
        </Field>

        <Field
          className="gap-1.5"
          data-invalid={Boolean(fieldErrors.phone) || undefined}
          data-shake-target=""
        >
          <FieldLabel htmlFor={phoneId}>Số điện thoại</FieldLabel>
          <Input
            id={phoneId}
            name={fieldNames.phone}
            type="tel"
            autoComplete="tel"
            className="min-h-10 bg-card tabular-nums"
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
          {fieldErrors.phone ? (
            <FieldError id={`${phoneId}-error`}>{fieldErrors.phone}</FieldError>
          ) : null}
        </Field>

        {topSlot ? <div className="sm:col-span-2">{topSlot}</div> : null}

        <Field
          className="gap-1.5"
          data-invalid={Boolean(fieldErrors.location) || undefined}
          data-shake-target=""
        >
          <FieldLabel htmlFor={provId}>
            Tỉnh / Thành phố ({provinces.length > 0 ? `${provinces.length} tỉnh/thành` : "mới"})
          </FieldLabel>
          <input type="hidden" name={fieldNames.province} value={provinceCode} />
          <LocationCombobox
            id={provId}
            options={provinces}
            value={provinceCode}
            onChange={handleProvinceSelect}
            placeholder="Chọn tỉnh/thành phố"
            searchPlaceholder="Tìm tỉnh hoặc thành phố..."
            emptyMessage="Không tìm thấy tỉnh/thành phố phù hợp."
            ariaLabel="Chọn Tỉnh hoặc Thành phố"
            ariaInvalid={Boolean(fieldErrors.location)}
            ariaDescribedBy={fieldErrors.location ? locErrId : undefined}
            disabled={submitting}
            loading={loadingProvinces}
            loadingLabel="Đang tải tỉnh thành..."
          />
        </Field>

        <Field
          className="gap-1.5"
          data-invalid={Boolean(fieldErrors.location) || undefined}
          data-shake-target=""
        >
          <div className="flex items-center justify-between gap-2">
            <FieldLabel htmlFor={wardId}>
              Phường / Xã
              {!loadingWards && wards.length > 0 && (
                <span className="font-normal text-muted-foreground tabular-nums" aria-live="polite">
                  {" "}
                  ({filteredWards.length}/{wards.length})
                </span>
              )}
            </FieldLabel>
          </div>
          <input type="hidden" name={fieldNames.ward} value={wardCode} />
          <LocationCombobox
            id={wardId}
            searchInputId={`${idPrefix}-ward-search`}
            options={wards}
            value={wardCode}
            onChange={handleWardSelect}
            placeholder="Chọn phường/xã"
            searchPlaceholder={
              selectedProvince
                ? `Tìm phường/xã tại ${selectedProvince.name}...`
                : "Tìm phường/xã..."
            }
            emptyMessage="Không tìm thấy phường/xã phù hợp."
            ariaLabel="Chọn Phường hoặc Xã"
            ariaInvalid={Boolean(fieldErrors.location)}
            ariaDescribedBy={fieldErrors.location ? locErrId : undefined}
            disabled={submitting}
            loading={loadingWards}
            loadingLabel="Đang tải phường xã..."
            query={wardQuery}
            onQueryChange={setWardQuery}
          />
          {legacyWardInactiveNotice ? (
            <p className="text-xs leading-relaxed text-muted-foreground" role="status">
              {legacyWardInactiveNotice}
            </p>
          ) : null}
          {selectedWard?.cod_supported === false ? (
            <Alert className="mt-1 border-badge-warning-ink/20 bg-badge-warning-bg text-badge-warning-ink shadow-none">
              <AlertTriangle className="size-4" aria-hidden="true" />
              <AlertDescription className="text-xs text-badge-warning-ink">
                Khu vực này hiện chưa hỗ trợ thanh toán khi nhận hàng (COD). Bạn vẫn có thể thanh
                toán trực tuyến bằng VietQR hoặc Ví ZaloPay.
              </AlertDescription>
            </Alert>
          ) : null}
        </Field>

        {fieldErrors.location ? (
          <FieldError id={locErrId} className="sm:col-span-2">
            {fieldErrors.location}
          </FieldError>
        ) : null}

        <Field
          className="gap-1.5 sm:col-span-2"
          data-invalid={Boolean(fieldErrors.street) || undefined}
          data-shake-target=""
        >
          <FieldLabel htmlFor={streetId}>Số nhà, tên đường</FieldLabel>
          <Input
            id={streetId}
            name={fieldNames.street}
            type="text"
            autoComplete="street-address"
            className="min-h-10 bg-card"
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
          {fieldErrors.street ? (
            <FieldError id={`${streetId}-error`}>{fieldErrors.street}</FieldError>
          ) : null}
        </Field>

        {metaSlot ? <div className="sm:col-span-2">{metaSlot}</div> : null}

        <div className="sm:col-span-2">
          <label
            htmlFor={defaultId}
            className="inline-flex min-h-10 cursor-pointer items-center gap-2.5 text-sm text-foreground select-none"
          >
            <Checkbox
              id={defaultId}
              checked={isDefault}
              onCheckedChange={(checked) => setIsDefault(checked === true)}
            />
            <span>Đặt làm địa chỉ mặc định</span>
          </label>
        </div>

        {externalError ? (
          <Alert variant="destructive" className="shadow-none sm:col-span-2" data-shake-alert="">
            <AlertDescription>{externalError}</AlertDescription>
          </Alert>
        ) : null}

        <div className="flex flex-wrap items-center gap-2.5 pt-1 sm:col-span-2">
          <Button
            type="submit"
            variant={submitVariant === "ink" || submitVariant === "primary" ? "default" : "default"}
            className="min-h-10 shadow-none"
            disabled={submitDisabled || submitting}
          >
            {submitting ? (
              <>
                <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                <span>{loadingLabel}</span>
              </>
            ) : (
              submitLabel
            )}
          </Button>
          {onCancel ? (
            <Button
              type="button"
              variant="outline"
              className="min-h-10 shadow-none"
              onClick={onCancel}
            >
              Hủy
            </Button>
          ) : null}
        </div>
      </div>
    </form>
  );
}
