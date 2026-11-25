// Pure validation and routing helpers for Seller BFF routes and Seller Portal UI.
// Keep this module free of server-only or React imports so unit tests can import it directly.

const RESOURCE_ID_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const TRACKING_CODE_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/;
const DATE_YYYY_MM_DD_RE = /^\d{4}-\d{2}-\d{2}$/;
const CONTROL_CHAR_RE = /[\u0000-\u001f\u007f]/;
const VN_PHONE_RE = /^(?:0|\+?84)(?:\d){8,10}$/;

export function isValidResourceId(raw: unknown): raw is string {
  if (typeof raw !== "string") return false;
  const trimmed = raw.trim();
  return RESOURCE_ID_RE.test(trimmed);
}

export function isValidTrackingCode(raw: unknown): raw is string {
  if (typeof raw !== "string") return false;
  const trimmed = raw.trim();
  return TRACKING_CODE_RE.test(trimmed);
}

export function isValidDateParam(raw: unknown): raw is string {
  if (typeof raw !== "string") return false;
  const trimmed = raw.trim();
  if (!DATE_YYYY_MM_DD_RE.test(trimmed)) return false;
  const [yStr, mStr, dStr] = trimmed.split("-");
  const y = Number(yStr);
  const m = Number(mStr);
  const d = Number(dStr);
  if (m < 1 || m > 12 || d < 1 || d > 31) return false;
  const check = new Date(Date.UTC(y, m - 1, d));
  return check.getUTCFullYear() === y && check.getUTCMonth() === m - 1 && check.getUTCDate() === d;
}

export function isSafeEvidenceUrl(raw: unknown): raw is string {
  if (typeof raw !== "string") return false;
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 500 || CONTROL_CHAR_RE.test(trimmed)) return false;
  if (trimmed.startsWith("/products/") && !trimmed.includes("..")) return true;
  try {
    const u = new URL(trimmed);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

export type EvidenceValidationResult =
  { ok: true; urls: string[] } | { ok: false; code: string; message: string };

export function validateEvidenceUrls(raw: unknown, maxCount = 5): EvidenceValidationResult {
  if (raw === undefined || raw === null) {
    return { ok: true, urls: [] };
  }
  if (!Array.isArray(raw)) {
    return {
      ok: false,
      code: "INVALID_EVIDENCE_URLS",
      message: "Danh sách liên kết bằng chứng chưa đúng định dạng.",
    };
  }
  const cleaned: string[] = [];
  for (const item of raw) {
    if (typeof item !== "string") {
      return {
        ok: false,
        code: "INVALID_EVIDENCE_URLS",
        message: "Mỗi liên kết bằng chứng phải là chuỗi hợp lệ.",
      };
    }
    const trimmed = item.trim();
    if (!trimmed) continue;
    if (!isSafeEvidenceUrl(trimmed)) {
      return {
        ok: false,
        code: "INVALID_EVIDENCE_URLS",
        message:
          "Liên kết bằng chứng cần bắt đầu bằng http:// hoặc https:// và không vượt quá 500 ký tự.",
      };
    }
    cleaned.push(trimmed);
  }
  if (cleaned.length > maxCount) {
    return {
      ok: false,
      code: "INVALID_EVIDENCE_URLS",
      message: `Tối đa ${maxCount} liên kết bằng chứng cho mỗi yêu cầu.`,
    };
  }
  return { ok: true, urls: cleaned };
}

export function validateResourceIdList(
  raw: unknown,
  maxItems = 50
): { ok: true; ids: string[] } | { ok: false; code: string } {
  if (!Array.isArray(raw)) return { ok: false, code: "INVALID_IDS" };
  const deduped: string[] = [];
  const seen = new Set<string>();
  for (const item of raw) {
    if (!isValidResourceId(item)) return { ok: false, code: "INVALID_IDS" };
    const clean = item.trim();
    if (seen.has(clean)) continue;
    seen.add(clean);
    deduped.push(clean);
    if (deduped.length > maxItems) return { ok: false, code: "TOO_MANY_IDS" };
  }
  if (deduped.length === 0) return { ok: false, code: "INVALID_IDS" };
  return { ok: true, ids: deduped };
}

export function validateTrackingCodeList(
  raw: unknown,
  maxItems = 50
): { ok: true; codes: string[] } | { ok: false; code: string } {
  if (!Array.isArray(raw)) return { ok: false, code: "INVALID_TRACKING_CODES" };
  const deduped: string[] = [];
  const seen = new Set<string>();
  for (const item of raw) {
    if (!isValidTrackingCode(item)) return { ok: false, code: "INVALID_TRACKING_CODES" };
    const clean = item.trim();
    if (seen.has(clean)) continue;
    seen.add(clean);
    deduped.push(clean);
    if (deduped.length > maxItems) return { ok: false, code: "TOO_MANY_TRACKING_CODES" };
  }
  if (deduped.length === 0) return { ok: false, code: "INVALID_TRACKING_CODES" };
  return { ok: true, codes: deduped };
}

export interface ShopRegisterPayload {
  name: string;
  slug: string;
  description: string;
  pickup_address_id: string;
  contact_phone: string;
  business_license_doc: string;
}

export type ShopRegisterValidationResult =
  { ok: true; payload: ShopRegisterPayload } | { ok: false; code: string; message: string };

export function validateShopRegisterInput(raw: unknown): ShopRegisterValidationResult {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return {
      ok: false,
      code: "INVALID_SHOP_INPUT",
      message: "Vui lòng nhập đầy đủ thông tin đăng ký gian hàng.",
    };
  }
  const obj = raw as Record<string, unknown>;
  const name = typeof obj.name === "string" ? obj.name.trim() : "";
  const slug = typeof obj.slug === "string" ? obj.slug.trim() : "";
  const description = typeof obj.description === "string" ? obj.description.trim() : "";
  const pickupAddressId =
    typeof obj.pickup_address_id === "string" ? obj.pickup_address_id.trim() : "";
  const contactPhoneRaw = typeof obj.contact_phone === "string" ? obj.contact_phone.trim() : "";
  const contactPhone = contactPhoneRaw.replace(/[\s.-]+/g, "");
  const businessLicenseDoc =
    typeof obj.business_license_doc === "string" ? obj.business_license_doc.trim() : "";

  if (name.length < 2 || name.length > 120 || CONTROL_CHAR_RE.test(name)) {
    return {
      ok: false,
      code: "INVALID_SHOP_NAME",
      message: "Tên gian hàng cần từ 2 đến 120 ký tự.",
    };
  }
  if (slug && (slug.length > 80 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/i.test(slug))) {
    return {
      ok: false,
      code: "INVALID_SHOP_SLUG",
      message: "Đường dẫn gian hàng chỉ gồm chữ cái không dấu, số và dấu gạch ngang.",
    };
  }
  if (description.length > 1000) {
    return {
      ok: false,
      code: "INVALID_SHOP_DESCRIPTION",
      message: "Mô tả gian hàng không vượt quá 1.000 ký tự.",
    };
  }
  if (!isValidResourceId(pickupAddressId)) {
    return {
      ok: false,
      code: "INVALID_PICKUP_ADDRESS",
      message: "Vui lòng chọn địa chỉ kho lấy hàng hợp lệ.",
    };
  }
  if (!VN_PHONE_RE.test(contactPhone)) {
    return {
      ok: false,
      code: "INVALID_CONTACT_PHONE",
      message: "Số điện thoại liên hệ gian hàng chưa đúng định dạng Việt Nam.",
    };
  }
  if (!businessLicenseDoc) {
    return {
      ok: false,
      code: "INVALID_BUSINESS_LICENSE",
      message: "Vui lòng nhập mã số ĐKKD hoặc liên kết giấy phép kinh doanh.",
    };
  }
  if (businessLicenseDoc.length > 500 || CONTROL_CHAR_RE.test(businessLicenseDoc)) {
    return {
      ok: false,
      code: "INVALID_BUSINESS_LICENSE",
      message: "Thông tin giấy phép kinh doanh không hợp lệ hoặc vượt quá 500 ký tự.",
    };
  }

  return {
    ok: true,
    payload: {
      name,
      slug,
      description,
      pickup_address_id: pickupAddressId,
      contact_phone: contactPhone,
      business_license_doc: businessLicenseDoc,
    },
  };
}

export type LegacyInteractionResolution =
  { kind: "redirect"; href: string } | { kind: "tab"; tab: "reviews" | "notifications" };

/**
 * Resolves old `/seller/tuong-tac?tab=...` links cleanly:
 * - Legacy return/complaint tabs redirect to `/seller/tra-hang`.
 * - Legacy waiting/restock tab redirects to `/seller/san-pham?filter=LOW_OOS`.
 * - `notifications` / `thong-bao` stays on notifications tab; everything else defaults to reviews.
 */
export function resolveLegacyInteractionTab(
  rawTab: string | null | undefined,
  extraQuery?: { selected?: string | null; status?: string | null }
): LegacyInteractionResolution {
  const key = (rawTab ?? "").trim().toLowerCase();
  if (
    key === "returns" ||
    key === "tra-hang" ||
    key === "complaints" ||
    key === "khieu-nai" ||
    key === "disputes" ||
    key === "tranh-chap"
  ) {
    const params = new URLSearchParams();
    if (extraQuery?.selected && isValidResourceId(extraQuery.selected)) {
      params.set("selected", extraQuery.selected.trim());
    }
    if (extraQuery?.status && /^[A-Z_]{2,32}$/.test(extraQuery.status.trim().toUpperCase())) {
      params.set("status", extraQuery.status.trim().toUpperCase());
    }
    const qs = params.toString();
    return { kind: "redirect", href: qs ? `/seller/tra-hang?${qs}` : "/seller/tra-hang" };
  }
  if (key === "waiting" || key === "cho-hang") {
    return { kind: "redirect", href: "/seller/san-pham?filter=LOW_OOS" };
  }
  if (key === "notifications" || key === "thong-bao") {
    return { kind: "tab", tab: "notifications" };
  }
  return { kind: "tab", tab: "reviews" };
}

/**
 * Suspended shops may only operate on overview, orders, returns, and finance (to finish in-flight orders
 * and reconcile payouts). Catalog, promotions, profile, site builder, chat, AI, and interactions are locked.
 */
export function isSellerRouteAllowedWhenSuspended(pathname: string): boolean {
  const clean = pathname.split("?")[0]?.replace(/\/+$/, "") || "/";
  if (clean === "/seller") return true;
  if (clean === "/seller/don-hang" || clean.startsWith("/seller/don-hang/")) return true;
  if (clean === "/seller/tra-hang" || clean.startsWith("/seller/tra-hang/")) return true;
  if (clean === "/seller/tai-chinh" || clean.startsWith("/seller/tai-chinh/")) return true;
  return false;
}
