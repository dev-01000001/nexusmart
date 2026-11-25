"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { ApiError, api } from "@/lib/api";
import { getProvinceByCode } from "@/lib/provinces";
import { formatVnd } from "@/lib/format";
import { AddressFieldsForm, type AddressFieldValues } from "@/features/commerce/AddressFieldsForm";
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
  const resolvedWardInit = hasPreloadedWards
    ? (initialWards.find((w) => w.code === editingAddress?.wardCode)?.code ??
      initialWards[0]?.code ??
      "")
    : "";

  const [provinceCode, setProvinceCode] = useState(resolvedProvinceInit);
  const [wards, setWards] = useState<GwWard[]>(hasPreloadedWards ? initialWards : []);
  const [wardCode, setWardCode] = useState(resolvedWardInit);
  const [loadingWards, setLoadingWards] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [errorSignal, setErrorSignal] = useState(0);

  const skipInitialFetchRef = useRef(hasPreloadedWards);
  const preferredWardRef = useRef<string | null>(editingAddress?.wardCode ?? null);

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
      `/api/checkout/locations?province_code=${encodeURIComponent(provinceCode)}`
    )
      .then((res) => {
        if (!active) return;
        const list = res.wards ?? [];
        setWards(list);
        const preferred = preferredWardRef.current;
        preferredWardRef.current = null;
        const matched = preferred ? list.find((w) => w.code === preferred) : undefined;
        setWardCode(matched?.code ?? list[0]?.code ?? "");
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
    () => provinces.find((p) => p.code === provinceCode) ?? provinces[0],
    [provinces, provinceCode]
  );

  const selectedWard = useMemo(() => wards.find((w) => w.code === wardCode), [wards, wardCode]);

  const previewProvince = useMemo(
    () => getProvinceByCode(toStorefrontProvinceCode(provinceCode)),
    [provinceCode]
  );

  const handleProvinceChange = (nextCode: string) => {
    preferredWardRef.current = null;
    setProvinceCode(nextCode);
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

  const metaSlot = selectedProvince ? (
    <div className={styles.formFull}>
      <div className={styles.addressMetaBar}>
        <span className={styles.addressMetaPair}>
          <span>{selectedProvince.name}</span>
          <span className={styles.addressMetaMuted}>{previewProvince?.etaDays ?? "1–3 ngày"}</span>
        </span>
        <span className="num">
          Phí tham khảo: <strong>{formatVnd(previewProvince?.shippingFee ?? 28000)}</strong>
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
      onWardChange={setWardCode}
      minStreetLength={5}
      submitVariant="primary"
      submitLabel={buttonText}
      loadingLabel="Đang lưu"
      submitDisabled={loadingWards}
      submitting={submitting}
      externalError={formError}
      errorSignal={errorSignal}
      metaSlot={metaSlot}
      onCancel={onCancel}
      onValidSubmit={handleValidSubmit}
    />
  );
}
