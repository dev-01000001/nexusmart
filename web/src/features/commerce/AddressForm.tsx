"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { ArrowRightLeft, ChevronDown, ChevronUp, Loader2 } from "lucide-react";
import type { GatewayMapLegacyAddressResponse } from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { normalizeVietnamese } from "@/lib/search";
import { useStore } from "@/context/StoreContext";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { AddressFieldsForm, type AddressFieldValues } from "./AddressFieldsForm";
import {
  type MappedLegacyAddressCodes,
  resolveLegacyCodesForPayload,
  resolveLoadedWardSelection,
} from "./addressHelpers";
import type { GwAddress, GwProvince, GwWard } from "./types";

export interface AddressSubmitPayload {
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
  is_default: boolean;
  legacy_province_code?: string;
  legacy_district_code?: string;
  legacy_ward_code?: string;
}

export interface AddressFormProps {
  initial?: GwAddress;
  defaultName?: string;
  defaultPhone?: string;
  onSaved?: (created: GwAddress) => void;
  onSubmitAddress?: (payload: AddressSubmitPayload) => Promise<GwAddress>;
  onCancel?: () => void;
  submitLabel?: string;
}

const CHECKOUT_FIELD_NAMES = {
  recipient: "recipient_name",
  phone: "recipient_phone",
  province: "province_code",
  ward: "ward_code",
  street: "street_address",
} as const;

export function AddressForm({
  initial,
  defaultName = "",
  defaultPhone = "",
  onSaved,
  onSubmitAddress,
  onCancel,
  submitLabel,
}: AddressFormProps) {
  const { showToast } = useStore();
  const isEditing = Boolean(initial?.id);

  const [provinces, setProvinces] = useState<GwProvince[]>([]);
  const [wards, setWards] = useState<GwWard[]>([]);
  const [provinceCode, setProvinceCode] = useState(initial?.province_code ?? "");
  const [wardCode, setWardCode] = useState(initial?.ward_code ?? "");
  const [loadingProvinces, setLoadingProvinces] = useState(true);
  const [loadingWards, setLoadingWards] = useState(false);
  const [legacyWardInactiveNotice, setLegacyWardInactiveNotice] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [errorSignal, setErrorSignal] = useState(0);

  // Legacy 3-level address mapping state
  const [legacyPanelOpen, setLegacyPanelOpen] = useState(false);
  const [legacyProvince, setLegacyProvince] = useState("");
  const [legacyDistrict, setLegacyDistrict] = useState("");
  const [legacyWard, setLegacyWard] = useState("");
  const [mappedLegacyCodes, setMappedLegacyCodes] = useState<MappedLegacyAddressCodes | null>(null);
  const [mappingLegacy, setMappingLegacy] = useState(false);
  const [legacyMapError, setLegacyMapError] = useState<string | null>(null);
  const [legacyMapNotice, setLegacyMapNotice] = useState<string | null>(null);
  const pendingMappedWardRef = useRef<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoadingProvinces(true);
    api<{ provinces: GwProvince[] }>("/api/checkout/locations", { retryOn401: false })
      .then((res) => {
        if (!active) return;
        const list = res.provinces ?? [];
        setProvinces(list);
        setProvinceCode((prev) => {
          if (prev && list.some((p) => p.code === prev)) return prev;
          if (initial?.province_code && list.some((p) => p.code === initial.province_code)) {
            return initial.province_code;
          }
          const preferred =
            list.find(
              (p) => p.code === "79" || normalizeVietnamese(p.name).includes("ho chi minh")
            ) ?? list[0];
          return preferred?.code ?? "";
        });
      })
      .catch((err) => {
        if (!active) return;
        setFormError(
          err instanceof ApiError ? err.message : "Không tải được danh sách tỉnh thành."
        );
      })
      .finally(() => {
        if (active) setLoadingProvinces(false);
      });
    return () => {
      active = false;
    };
  }, [initial?.province_code]);

  useEffect(() => {
    if (!provinceCode) {
      setWards([]);
      setWardCode("");
      return;
    }
    let active = true;
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
  }, [provinceCode, initial?.province_code, initial?.ward_code]);

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode),
    [provinces, provinceCode]
  );
  const selectedWard = useMemo(() => wards.find((w) => w.code === wardCode), [wards, wardCode]);

  const handleProvinceChange = (nextCode: string) => {
    pendingMappedWardRef.current = null;
    setMappedLegacyCodes(null);
    setLegacyWardInactiveNotice(null);
    setProvinceCode(nextCode);
    setWardCode("");
  };

  const handleWardChange = (nextWardCode: string) => {
    setWardCode(nextWardCode);
    if (mappedLegacyCodes && nextWardCode !== mappedLegacyCodes.targetWardCode) {
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

  const handleValidSubmit = async (values: AddressFieldValues) => {
    if (!selectedProvince || !selectedWard) return;
    const legacyCodesPayload = resolveLegacyCodesForPayload(
      mappedLegacyCodes,
      selectedProvince.code,
      selectedWard.code
    );
    const payload: AddressSubmitPayload = {
      recipient_name: values.recipientName,
      recipient_phone: values.phone,
      province_code: selectedProvince.code,
      province_name: selectedProvince.name,
      ward_code: selectedWard.code,
      ward_name: selectedWard.name,
      street_address: values.streetAddress,
      is_default: values.isDefault,
      ...legacyCodesPayload,
    };

    setFormError(null);
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
      showToast(
        isEditing
          ? `Đã cập nhật địa chỉ tại ${selectedWard.name}, ${selectedProvince.name}.`
          : `Đã lưu địa chỉ tại ${selectedWard.name}, ${selectedProvince.name}.`
      );
      onSaved?.(created);
    } catch (err) {
      setFormError(
        err instanceof ApiError ? err.message : "Chưa thể lưu địa chỉ lúc này. Thử lại sau."
      );
      setErrorSignal((n) => n + 1);
    } finally {
      setSubmitting(false);
    }
  };

  const legacySlot = (
    <div className="space-y-3">
      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="min-h-10 whitespace-normal text-left shadow-none sm:min-h-9"
          disabled={submitting}
          aria-expanded={legacyPanelOpen}
          onClick={() => {
            setLegacyPanelOpen((v) => !v);
            setLegacyMapError(null);
          }}
        >
          <ArrowRightLeft className="size-3.5 shrink-0" aria-hidden="true" />
          <span>
            {legacyPanelOpen
              ? "Ẩn công cụ quy đổi địa chỉ cũ"
              : "Quy đổi địa chỉ cũ (Tỉnh / Quận / Phường)"}
          </span>
          {legacyPanelOpen ? (
            <ChevronUp className="size-3.5 shrink-0" aria-hidden="true" />
          ) : (
            <ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
          )}
        </Button>
      </div>

      {legacyPanelOpen && (
        <div className="space-y-3 rounded-lg border border-border bg-muted/40 p-3.5 sm:p-4">
          <p className="text-xs leading-relaxed text-muted-foreground">
            Nhập địa chỉ hành chính cũ (trước sáp nhập 34 tỉnh/thành) để hệ thống tự điền Tỉnh/Thành
            phố và Phường/Xã mới.
          </p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field className="gap-1.5">
              <FieldLabel htmlFor="addr-legacy-province">Tỉnh / Thành phố cũ</FieldLabel>
              <Input
                id="addr-legacy-province"
                type="text"
                className="min-h-10 bg-card"
                value={legacyProvince}
                onChange={(e) => setLegacyProvince(e.target.value)}
                placeholder="Ví dụ: TP. Hồ Chí Minh, Bình Dương..."
                disabled={mappingLegacy || submitting}
              />
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="addr-legacy-district">Quận / Huyện cũ</FieldLabel>
              <Input
                id="addr-legacy-district"
                type="text"
                className="min-h-10 bg-card"
                value={legacyDistrict}
                onChange={(e) => setLegacyDistrict(e.target.value)}
                placeholder="Ví dụ: Quận 1, Thành phố Thủ Đức..."
                disabled={mappingLegacy || submitting}
              />
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="addr-legacy-ward">Phường / Xã cũ</FieldLabel>
              <Input
                id="addr-legacy-ward"
                type="text"
                className="min-h-10 bg-card"
                value={legacyWard}
                onChange={(e) => setLegacyWard(e.target.value)}
                placeholder="Ví dụ: Phường Bến Nghé..."
                disabled={mappingLegacy || submitting}
              />
            </Field>
          </div>

          {legacyMapError ? <FieldError>{legacyMapError}</FieldError> : null}

          {legacyMapNotice ? (
            <Alert className="border-badge-success-ink/20 bg-badge-success-bg text-badge-success-ink shadow-none">
              <AlertDescription className="text-xs text-badge-success-ink">
                {legacyMapNotice}
              </AlertDescription>
            </Alert>
          ) : null}

          <div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="min-h-10 shadow-none sm:min-h-9"
              disabled={mappingLegacy || submitting}
              onClick={() => void handleMapLegacyAddress()}
            >
              {mappingLegacy ? (
                <>
                  <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                  <span>Đang quy đổi...</span>
                </>
              ) : (
                "Quy đổi sang địa chỉ mới"
              )}
            </Button>
          </div>
        </div>
      )}
    </div>
  );

  return (
    <AddressFieldsForm
      idPrefix="addr"
      fieldNames={CHECKOUT_FIELD_NAMES}
      initialValues={{
        recipientName: initial?.recipient_name ?? defaultName,
        phone: initial?.recipient_phone ?? defaultPhone,
        streetAddress: initial?.street_address ?? "",
        isDefault: initial ? initial.is_default : true,
      }}
      provinces={provinces}
      wards={wards}
      provinceCode={provinceCode}
      wardCode={wardCode}
      loadingProvinces={loadingProvinces}
      loadingWards={loadingWards}
      onProvinceChange={handleProvinceChange}
      onWardChange={handleWardChange}
      onWardFilterCleared={() => setMappedLegacyCodes(null)}
      legacyWardInactiveNotice={legacyWardInactiveNotice}
      minStreetLength={4}
      formClassName="rounded-xl border border-border bg-card p-4 sm:p-5"
      submitVariant="ink"
      submitLabel={submitLabel ?? (isEditing ? "Cập nhật địa chỉ" : "Lưu địa chỉ nhận hàng")}
      loadingLabel="Đang lưu"
      submitDisabled={loadingProvinces || loadingWards}
      submitting={submitting}
      externalError={formError}
      errorSignal={errorSignal}
      topSlot={legacySlot}
      onCancel={onCancel}
      onValidSubmit={handleValidSubmit}
    />
  );
}
