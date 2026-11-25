// Pure account types & helpers (safe for client, server, and `node --test`).

export interface GwAddress {
  id: string;
  user_id?: string;
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
  is_default: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface AddressView {
  id: string;
  recipientName: string;
  phone: string;
  provinceCode: string;
  provinceName: string;
  wardCode: string;
  wardName: string;
  streetDetail: string;
  isDefault: boolean;
}

export interface GwProvince {
  code: string;
  name: string;
}

export interface GwWard {
  code: string;
  province_code: string;
  name: string;
}

export interface AccountProfile {
  id: string;
  fullName: string;
  email: string;
  phone: string;
  role: string;
  shopId: string;
  avatarUrl: string;
  phoneVerified: boolean;
  memberSince: string;
  coins: number;
}

export function mapGatewayAddress(a: GwAddress): AddressView {
  return {
    id: a.id,
    recipientName: a.recipient_name,
    phone: a.recipient_phone,
    provinceCode: a.province_code,
    provinceName: a.province_name,
    wardCode: a.ward_code,
    wardName: a.ward_name,
    streetDetail: a.street_address,
    isDefault: Boolean(a.is_default),
  };
}

const UNSAFE_PATH_CHAR_RE = /[\u0000-\u001F\u007F\\]/;

/**
 * Validates that a redirect target is a same-origin relative path starting with a single `/`.
 * Rejects protocol-relative `//`, any backslash `\`, and all ASCII control characters
 * (`\x00-\x1f`, `\x7f`, including `\t`/`\r`/`\n` that browsers strip when parsing URLs).
 */
export function safeInternalPath(raw: string | null | undefined, fallback = "/"): string {
  if (!raw || UNSAFE_PATH_CHAR_RE.test(raw)) return fallback;
  const trimmed = raw.trim();
  if (!trimmed.startsWith("/") || trimmed.startsWith("//")) {
    return fallback;
  }
  return trimmed;
}

/**
 * Validates a `?next=` query parameter to prevent open redirects and auth-page loops.
 * Only accepts same-origin relative paths starting with a single `/`.
 */
export function safeNextPath(raw: string | null | undefined, fallback = "/tai-khoan"): string {
  const safe = safeInternalPath(raw, "");
  if (!safe) return fallback;
  const pathname = safe.split(/[?#]/)[0] ?? "";
  if (pathname === "/dang-nhap" || pathname === "/dang-ky") {
    return fallback;
  }
  return safe;
}

/** Map backend numeric province codes to the storefront ETA/shipping preview table codes. */
const NUMERIC_TO_LEGACY_PROVINCE: Record<number, string> = {
  1: "HN",
  4: "CB",
  8: "TQ",
  11: "DB",
  12: "LCH",
  14: "SL",
  15: "LC",
  19: "TNG",
  20: "LS",
  22: "QN",
  24: "BN",
  25: "PT",
  31: "HP",
  33: "HY",
  37: "NB",
  38: "TH",
  40: "NA",
  42: "HT",
  44: "QTR",
  46: "HUE",
  48: "DN",
  51: "QNG",
  52: "GL",
  56: "KH",
  66: "DL",
  68: "LD",
  75: "DNI",
  79: "HCM",
  80: "TNI",
  82: "DT",
  86: "VL",
  91: "AG",
  92: "CT",
  96: "CM",
};

export function toStorefrontProvinceCode(code: string): string {
  const num = Number.parseInt(code, 10);
  if (!Number.isNaN(num) && num in NUMERIC_TO_LEGACY_PROVINCE) {
    return NUMERIC_TO_LEGACY_PROVINCE[num];
  }
  return code;
}
