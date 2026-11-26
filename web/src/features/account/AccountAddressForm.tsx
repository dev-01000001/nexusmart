"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import type { GatewayMapLegacyAddressResponse } from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { getProvinceByCode } from "@/lib/provinces";
import { Money } from "@/components/app/Money";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { AddressFieldsForm, type AddressFieldValues } from "@/features/commerce/AddressFieldsForm";
import {
  type MappedLegacyAddressCodes,
  resolveLegacyCodesForPayload,
  resolveLoadedWardSelection,
} from "@/features/commerce/addressHelpers";
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
  editingAddress?: AddressView | null;
  onSaved: (saved: AddressView) => void;
  onCancel?: () => void;
  submitLabel?: string;
}

const ACCOUNT_FIELD_NAMES = {
  recipient: "recipientName",
  phone: "recipientPhone",
  province: "provinceCode",
  ward: "wardCode",
  street: "streetAddress",
} as const;

export function AccountAddressForm({
  defaultRecipientName,
  defaultPhone,
  provinces,
  initialProvinceCode,
  initialWards,
  editingAddress = null,
  onSaved,
  onCancel,
  submitLabel,
}: AccountAddressFormProps) {
  const isEditing = Boolean(editingAddress);
  const resolvedProvinceInit =
    editingAddress?.provinceCode || initialProvinceCode || provinces[0]?.code || "79";
  const hasPreloadedWards =
    !(editingAddress && editingAddress.provinceCode !== initialProvinceCode) &&
    initialWards.length > 0;
  const initialResolution = hasPreloadedWards
    ? resolveLoadedWardSelection({
        wards: initialWards,
        currentWardCode: "",
        initialProvinceCode: editingAddress?.provinceCode,
        initialWardCode: editingAddress?.wardCode,
        provinceCode: resolvedProvinceInit,
      })
    : { wardCode: "", legacyWardInactive: false };

  const [provinceCode, setProvinceCode] = useState(resolvedProvinceInit);
  const [wards, setWards] = useState<GwWard[]>(hasPreloadedWards ? initialWards : []);
  const [wardCode, setWardCode] = useState(initialResolution.wardCode);
  const [legacyWardInactiveNotice, setLegacyWardInactiveNotice] = useState<string | null>(
    initialResolution.legacyWardInactive
      ? "Phường/xã cũ không còn trong danh mục mới, vui lòng chọn lại."
      : null
  );
  const [loadingWards, setLoadingWards] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [errorSignal, setErrorSignal] = useState(0);

  // Legacy 3-level address mapping
  const [legacyPanelOpen, setLegacyPanelOpen] = useState(false);
  const [legacyProvince, setLegacyProvince] = useState("");
  const [legacyDistrict, setLegacyDistrict] = useState("");
  const [legacyWard, setLegacyWard] = useState("");
  const [mappedLegacyCodes, setMappedLegacyCodes] = useState<MappedLegacyAddressCodes | null>(null);
  const [mappingLegacy, setMappingLegacy] = useState(false);
  const [legacyMapError, setLegacyMapError] = useState<string | null>(null);
  const [legacyMapNotice, setLegacyMapNotice] = useState<string | null>(null);

  const skipInitialFetchRef = useRef(hasPreloadedWards);
  const pendingMappedWardRef = useRef<string | null>(null);

  useEffect(() => {
    if (!provinceCode) {
      setWards([]);
      setWardCode("");
      return;
    }
    if (skipInitialFetchRef.current) {
      skipInitialFetchRef.current = false;
      return;
    }
    let active = true;
    setWards([]);
    setWardCode("");
    setFormError(null);
    setLoadingWards(true);
    api<{ wards: GwWard[] }>(
      `/api/checkout/locations?province_code=${encodeURIComponent(provinceCode)}`,
      { retryOn401: false }
    )
      .then((res) => {
        if (!active) return;
        const list = res.wards ?? [];
        setWards(list);
        const mappedWard = pendingMappedWardRef.current;
        pendingMappedWardRef.current = null;
        setWardCode((prev) => {
          const resolved = resolveLoadedWardSelection({
            wards: list,
            mappedWardCode: mappedWard,
            currentWardCode: prev,
            initialProvinceCode: editingAddress?.provinceCode,
            initialWardCode: editingAddress?.wardCode,
            provinceCode,
          });
          setLegacyWardInactiveNotice(
            resolved.legacyWardInactive
              ? "Phường/xã cũ không còn trong danh mục mới, vui lòng chọn lại."
              : null
          );
          return resolved.wardCode;
        });
      })
      .catch((err) => {
        if (!active) return;
        setWards([]);
        setWardCode("");
        setFormError(err instanceof ApiError ? err.message : "Không tải được danh sách phường xã.");
      })
      .finally(() => {
        if (active) setLoadingWards(false);
      });
    return () => {
      active = false;
    };
  }, [provinceCode, editingAddress?.provinceCode, editingAddress?.wardCode]);

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode) ?? provinces[0],
    [provinces, provinceCode]
  );

  const selectedWard = useMemo(() => wards.find((w) => w.code === wardCode), [wards, wardCode]);

  const previewProvince = useMemo(
    () => getProvinceByCode(toStorefrontProvinceCode(provinceCode)),
    [provinceCode]
  );

  const handleProvinceChange = (nextCode: string) => {
    pendingMappedWardRef.current = null;
    setMappedLegacyCodes(null);
    setLegacyWardInactiveNotice(null);
    setProvinceCode(nextCode);
    setWardCode("");
  };

  const handleWardChange = (nextWard: string) => {
    setWardCode(nextWard);
    if (mappedLegacyCodes && nextWard !== mappedLegacyCodes.targetWardCode) {
      setMappedLegacyCodes(null);
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
      const res = await api<GatewayMapLegacyAddressResponse>("/api/shipping/addresses/map-legacy", {
        method: "POST",
        body: {
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
      setLegacyWardInactiveNotice(null);
      if (res.province_code === provinceCode) {
        setWardCode(res.ward_code);
        pendingMappedWardRef.current = null;
      } else {
        setProvinceCode(res.province_code);
      }
      setLegacyMapNotice(
        `Đã quy đổi sang địa chỉ mới: ${res.ward_name}, ${res.province_name}. Bạn có thể kiểm tra lại trước khi lưu.`
      );
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

  const handleValidSubmit = async (values: AddressFieldValues) => {
    if (!selectedProvince || !selectedWard) return;
    setFormError(null);
    setSubmitting(true);
    try {
      const endpoint = editingAddress
        ? `/api/auth/addresses/${encodeURIComponent(editingAddress.id)}`
        : "/api/auth/addresses";
      const method = editingAddress ? "PUT" : "POST";
      const legacyCodes = resolveLegacyCodesForPayload(
        mappedLegacyCodes,
        selectedProvince.code,
        selectedWard.code
      );

      const res = await api<{ address: BffAddressItem }>(endpoint, {
        method,
        body: {
          recipientName: values.recipientName,
          recipientPhone: values.phone,
          provinceCode: selectedProvince.code,
          provinceName: selectedProvince.name,
          wardCode: selectedWard.code,
          wardName: selectedWard.name,
          streetAddress: values.streetAddress,
          isDefault: values.isDefault,
          ...legacyCodes,
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
      setFormError(err instanceof ApiError ? err.message : "Chưa lưu được địa chỉ lúc này.");
      setErrorSignal((n) => n + 1);
    } finally {
      setSubmitting(false);
    }
  };

  const buttonText = submitLabel ?? (isEditing ? "Lưu thay đổi" : "Lưu địa chỉ");

  const legacySlot = (
    <div className="col-span-full space-y-3">
      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={submitting}
          onClick={() => {
            setLegacyPanelOpen((v) => !v);
            setLegacyMapError(null);
          }}
          className="h-auto min-h-10 py-2 text-left whitespace-normal shadow-none sm:min-h-8"
        >
          {legacyPanelOpen
            ? "Ẩn công cụ quy đổi địa chỉ cũ"
            : "Quy đổi địa chỉ cũ (Tỉnh / Quận / Phường)"}
        </Button>
      </div>
      {legacyPanelOpen && (
        <div className="space-y-3 rounded-lg border border-border bg-background p-4">
          <FieldDescription className="text-xs">
            Nhập địa chỉ hành chính cũ để hệ thống tự điền Tỉnh/Thành phố và Phường/Xã mới.
          </FieldDescription>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field>
              <FieldLabel htmlFor="acc-legacy-province">Tỉnh / Thành phố cũ</FieldLabel>
              <Input
                id="acc-legacy-province"
                type="text"
                value={legacyProvince}
                onChange={(e) => setLegacyProvince(e.target.value)}
                placeholder="TP. Hồ Chí Minh, Bình Dương..."
                disabled={mappingLegacy || submitting}
                className="h-10 bg-card"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="acc-legacy-district">Quận / Huyện cũ</FieldLabel>
              <Input
                id="acc-legacy-district"
                type="text"
                value={legacyDistrict}
                onChange={(e) => setLegacyDistrict(e.target.value)}
                placeholder="Quận 1, TP. Thủ Đức..."
                disabled={mappingLegacy || submitting}
                className="h-10 bg-card"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="acc-legacy-ward">Phường / Xã cũ</FieldLabel>
              <Input
                id="acc-legacy-ward"
                type="text"
                value={legacyWard}
                onChange={(e) => setLegacyWard(e.target.value)}
                placeholder="Phường Bến Nghé..."
                disabled={mappingLegacy || submitting}
                className="h-10 bg-card"
              />
            </Field>
          </div>
          {legacyMapError && <FieldError>{legacyMapError}</FieldError>}
          {legacyMapNotice && (
            <Alert className="shadow-none" role="status">
              <AlertDescription>{legacyMapNotice}</AlertDescription>
            </Alert>
          )}
          <div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              disabled={mappingLegacy || submitting}
              onClick={() => void handleMapLegacyAddress()}
              className="min-h-10 sm:min-h-8"
            >
              {mappingLegacy ? "Đang quy đổi..." : "Quy đổi sang địa chỉ mới"}
            </Button>
          </div>
        </div>
      )}
    </div>
  );

  const metaSlot = selectedProvince ? (
    <div className="col-span-full">
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-background px-3.5 py-2.5 text-xs">
        <span className="flex flex-wrap items-center gap-2">
          <span className="font-medium text-foreground">{selectedProvince.name}</span>
          <span className="text-muted-foreground">{previewProvince?.etaDays ?? "1–3 ngày"}</span>
        </span>
        <span className="inline-flex items-baseline gap-1 text-muted-foreground">
          <span>Phí tham khảo:</span>
          <Money vnd={previewProvince?.shippingFee ?? 28000} size="sm" />
        </span>
      </div>
    </div>
  ) : null;

  return (
    <AddressFieldsForm
      idPrefix="acc-addr"
      fieldNames={ACCOUNT_FIELD_NAMES}
      initialValues={{
        recipientName: editingAddress?.recipientName ?? defaultRecipientName,
        phone: editingAddress?.phone ?? defaultPhone,
        streetAddress: editingAddress?.streetDetail ?? "",
        isDefault: editingAddress ? editingAddress.isDefault : true,
      }}
      provinces={provinces}
      wards={wards}
      provinceCode={provinceCode}
      wardCode={wardCode}
      loadingWards={loadingWards}
      onProvinceChange={handleProvinceChange}
      onWardChange={handleWardChange}
      onWardFilterCleared={() => setMappedLegacyCodes(null)}
      legacyWardInactiveNotice={legacyWardInactiveNotice}
      minStreetLength={5}
      submitVariant="primary"
      submitLabel={buttonText}
      loadingLabel="Đang lưu"
      submitDisabled={loadingWards}
      submitting={submitting}
      externalError={formError}
      errorSignal={errorSignal}
      topSlot={legacySlot}
      metaSlot={metaSlot}
      onCancel={onCancel}
      onValidSubmit={handleValidSubmit}
    />
  );
}
