"use client";

import React, { useEffect, useMemo, useState } from "react";
import { ApiError, api } from "@/lib/api";
import { useStore } from "@/context/StoreContext";
import { AddressFieldsForm, type AddressFieldValues } from "./AddressFieldsForm";
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

const CHECKOUT_FIELD_NAMES = {
  recipient: "recipient_name",
  phone: "recipient_phone",
  province: "province_code",
  ward: "ward_code",
  street: "street_address",
} as const;

export function AddressForm({
  defaultName = "",
  defaultPhone = "",
  onSaved,
  onSubmitAddress,
  onCancel,
  submitLabel = "Lưu địa chỉ nhận hàng",
}: AddressFormProps) {
  const { showToast } = useStore();
  const [provinces, setProvinces] = useState<GwProvince[]>([]);
  const [wards, setWards] = useState<GwWard[]>([]);
  const [provinceCode, setProvinceCode] = useState("");
  const [wardCode, setWardCode] = useState("");
  const [loadingProvinces, setLoadingProvinces] = useState(true);
  const [loadingWards, setLoadingWards] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [errorSignal, setErrorSignal] = useState(0);

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
  }, []);

  useEffect(() => {
    if (!provinceCode) {
      setWards([]);
      setWardCode("");
      return;
    }
    let active = true;
    setWards([]);
    setWardCode("");
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
  }, [provinceCode]);

  const selectedProvince = useMemo(
    () => provinces.find((p) => p.code === provinceCode),
    [provinces, provinceCode]
  );
  const selectedWard = useMemo(() => wards.find((w) => w.code === wardCode), [wards, wardCode]);

  const handleValidSubmit = async (values: AddressFieldValues) => {
    if (!selectedProvince || !selectedWard) return;
    const payload: AddressSubmitPayload = {
      recipient_name: values.recipientName,
      recipient_phone: values.phone,
      province_code: selectedProvince.code,
      province_name: selectedProvince.name,
      ward_code: selectedWard.code,
      ward_name: selectedWard.name,
      street_address: values.streetAddress,
      is_default: values.isDefault,
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
      showToast(`Đã lưu địa chỉ tại ${selectedWard.name}, ${selectedProvince.name}.`);
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

  return (
    <AddressFieldsForm
      idPrefix="addr"
      fieldNames={CHECKOUT_FIELD_NAMES}
      initialValues={{
        recipientName: defaultName,
        phone: defaultPhone,
        streetAddress: "",
        isDefault: true,
      }}
      provinces={provinces}
      wards={wards}
      provinceCode={provinceCode}
      wardCode={wardCode}
      loadingProvinces={loadingProvinces}
      loadingWards={loadingWards}
      onProvinceChange={setProvinceCode}
      onWardChange={setWardCode}
      minStreetLength={4}
      formClassName={styles.surfacePanel}
      submitVariant="ink"
      submitLabel={submitLabel}
      loadingLabel="Đang lưu địa chỉ"
      submitDisabled={loadingProvinces || loadingWards}
      submitting={submitting}
      externalError={formError}
      errorSignal={errorSignal}
      onCancel={onCancel}
      onValidSubmit={handleValidSubmit}
    />
  );
}
