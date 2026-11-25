// Server-side address DTO & mapper between gateway (/api/v1/users/me/addresses) and BFF/UI.

export interface GatewayAddress {
  id: string;
  user_id?: string;
  recipient_name?: string;
  recipient_phone?: string;
  province_code?: string;
  province_name?: string;
  ward_code?: string;
  ward_name?: string;
  street_address?: string;
  legacy_province_code?: string;
  legacy_district_code?: string;
  legacy_ward_code?: string;
  is_default?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface AddressItem {
  id: string;
  userId: string;
  recipientName: string;
  recipientPhone: string;
  provinceCode: string;
  provinceName: string;
  wardCode: string;
  wardName: string;
  streetAddress: string;
  legacyProvinceCode: string;
  legacyDistrictCode: string;
  legacyWardCode: string;
  isDefault: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface AddressPayloadInput {
  recipientName?: string;
  recipient_name?: string;
  recipientPhone?: string;
  recipient_phone?: string;
  provinceCode?: string;
  province_code?: string;
  provinceName?: string;
  province_name?: string;
  wardCode?: string;
  ward_code?: string;
  wardName?: string;
  ward_name?: string;
  streetAddress?: string;
  street_address?: string;
  legacyProvinceCode?: string;
  legacy_province_code?: string;
  legacyDistrictCode?: string;
  legacy_district_code?: string;
  legacyWardCode?: string;
  legacy_ward_code?: string;
  isDefault?: boolean;
  is_default?: boolean;
}

export function toAddressItem(a: GatewayAddress): AddressItem {
  return {
    id: a.id,
    userId: a.user_id ?? "",
    recipientName: a.recipient_name ?? "",
    recipientPhone: a.recipient_phone ?? "",
    provinceCode: a.province_code ?? "",
    provinceName: a.province_name ?? "",
    wardCode: a.ward_code ?? "",
    wardName: a.ward_name ?? "",
    streetAddress: a.street_address ?? "",
    legacyProvinceCode: a.legacy_province_code ?? "",
    legacyDistrictCode: a.legacy_district_code ?? "",
    legacyWardCode: a.legacy_ward_code ?? "",
    isDefault: Boolean(a.is_default),
    createdAt: a.created_at ?? "",
    updatedAt: a.updated_at ?? "",
  };
}

/** Normalize camelCase/snake_case BFF input into the gateway snake_case payload. Never passes user_id. */
export function toGatewayAddressBody(input: AddressPayloadInput) {
  return {
    recipient_name: (input.recipientName ?? input.recipient_name ?? "").trim(),
    recipient_phone: (input.recipientPhone ?? input.recipient_phone ?? "").trim(),
    province_code: (input.provinceCode ?? input.province_code ?? "").trim(),
    province_name: (input.provinceName ?? input.province_name ?? "").trim(),
    ward_code: (input.wardCode ?? input.ward_code ?? "").trim(),
    ward_name: (input.wardName ?? input.ward_name ?? "").trim(),
    street_address: (input.streetAddress ?? input.street_address ?? "").trim(),
    legacy_province_code: (input.legacyProvinceCode ?? input.legacy_province_code ?? "").trim(),
    legacy_district_code: (input.legacyDistrictCode ?? input.legacy_district_code ?? "").trim(),
    legacy_ward_code: (input.legacyWardCode ?? input.legacy_ward_code ?? "").trim(),
    is_default: Boolean(input.isDefault ?? input.is_default),
  };
}
