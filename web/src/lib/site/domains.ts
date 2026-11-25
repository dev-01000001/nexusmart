// Pure custom-domain DTOs, hostname validation, and Vietnamese UI/error mappers.
// Zero I/O and zero Next/Node imports so it runs identically in Edge middleware, Node BFF,
// Client Components, and `node --test`.

export type DomainKind = "SUBDOMAIN" | "CUSTOM";
export type DomainStatus = "PENDING" | "ACTIVE" | "FAILED" | "REMOVING";

export interface DomainVerificationRecord {
  type: string;
  name: string;
  value: string;
}

export interface SiteDomainDTO {
  id: string;
  host: string;
  kind: DomainKind;
  status: DomainStatus;
  is_primary: boolean;
  verification: DomainVerificationRecord[];
  last_error: string;
  verified_at: string | null;
  created_at: string | null;
}

export interface RawSiteDomain {
  id?: unknown;
  host?: unknown;
  kind?: unknown;
  status?: unknown;
  is_primary?: unknown;
  verification?: unknown;
  last_error?: unknown;
  verified_at?: unknown;
  created_at?: unknown;
}

const VALID_DOMAIN_KINDS: ReadonlySet<DomainKind> = new Set(["SUBDOMAIN", "CUSTOM"]);
const VALID_DOMAIN_STATUSES: ReadonlySet<DomainStatus> = new Set([
  "PENDING",
  "ACTIVE",
  "FAILED",
  "REMOVING",
]);

function asCleanString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function asNullableString(value: unknown): string | null {
  const s = asCleanString(value);
  return s ? s : null;
}

export function normalizeVerificationRecords(raw: unknown): DomainVerificationRecord[] {
  if (!Array.isArray(raw)) return [];
  const out: DomainVerificationRecord[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object" || Array.isArray(item)) continue;
    const rec = item as Record<string, unknown>;
    const type = asCleanString(rec.type).toUpperCase();
    const name = asCleanString(rec.name);
    const value = asCleanString(rec.value);
    if (!type || !name || !value) continue;
    out.push({ type, name, value });
  }
  return out;
}

export function normalizeSiteDomain(raw: RawSiteDomain | null | undefined): SiteDomainDTO | null {
  if (!raw || typeof raw !== "object") return null;
  const id = asCleanString(raw.id);
  const host = asCleanString(raw.host).toLowerCase();
  if (!id || !host) return null;

  const rawKind = asCleanString(raw.kind).toUpperCase() as DomainKind;
  const kind: DomainKind = VALID_DOMAIN_KINDS.has(rawKind) ? rawKind : "CUSTOM";

  const rawStatus = asCleanString(raw.status).toUpperCase() as DomainStatus;
  const status: DomainStatus = VALID_DOMAIN_STATUSES.has(rawStatus) ? rawStatus : "PENDING";

  return {
    id,
    host,
    kind,
    status,
    is_primary: Boolean(raw.is_primary),
    verification: normalizeVerificationRecords(raw.verification),
    last_error: asCleanString(raw.last_error),
    verified_at: asNullableString(raw.verified_at),
    created_at: asNullableString(raw.created_at),
  };
}

export function normalizeSiteDomains(rawList: unknown): SiteDomainDTO[] {
  if (!Array.isArray(rawList)) return [];
  const out: SiteDomainDTO[] = [];
  for (const item of rawList) {
    const normalized = normalizeSiteDomain(item as RawSiteDomain);
    if (normalized) out.push(normalized);
  }
  return out;
}

export type CustomHostnameErrorReason =
  | "empty"
  | "invalid_chars"
  | "port_or_ipv6"
  | "wildcard"
  | "non_ascii"
  | "length"
  | "no_dot"
  | "ip_literal"
  | "forbidden_host"
  | "platform_domain"
  | "invalid_label";

export type CustomHostnameValidationResult =
  | { ok: true; hostname: string }
  | { ok: false; reason: CustomHostnameErrorReason; message: string };

const LABEL_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const INVALID_INPUT_CHARS_RE = /[\s/@?#\\,\u0000-\u001F\u007F]/;
const NON_ASCII_RE = /[^\x20-\x7E]/;
const FORBIDDEN_SUFFIXES = [".localhost", ".local", ".internal"] as const;

function cleanRootDomain(rootDomain: string): string {
  return rootDomain
    .trim()
    .toLowerCase()
    .replace(/^\.+|\.+$/g, "");
}

function isIpv4Pattern(host: string): boolean {
  const parts = host.split(".");
  if (parts.length !== 4) return false;
  return parts.every((p) => {
    if (!/^\d{1,3}$/.test(p)) return false;
    const n = Number(p);
    return n >= 0 && n <= 255;
  });
}

export function normalizeCustomHostnameInput(raw: string): string {
  return raw.trim().toLowerCase();
}

/**
 * Validate a custom domain hostname against the Phase 5 contract rules:
 * lowercase ASCII, 2-253 chars, labels 1-63 `[a-z0-9-]`, no IP, no port, no wildcard,
 * at least one dot, not root domain or under `rootDomain`, not `localhost`/`*.local`/`*.internal`/`*.localhost`.
 */
export function validateCustomHostname(
  raw: string,
  rootDomain = "nexusmart.localhost"
): CustomHostnameValidationResult {
  const trimmed = normalizeCustomHostnameInput(raw);
  if (!trimmed) {
    return { ok: false, reason: "empty", message: "Vui lòng nhập tên miền riêng." };
  }
  if (trimmed.includes("*")) {
    return {
      ok: false,
      reason: "wildcard",
      message: "Không hỗ trợ tên miền ký tự đại diện (*). Vui lòng nhập tên miền cụ thể.",
    };
  }
  if (INVALID_INPUT_CHARS_RE.test(trimmed)) {
    return {
      ok: false,
      reason: "invalid_chars",
      message:
        "Chỉ nhập tên miền (ví dụ: shop.thuonghieu.vn), không kèm http://, khoảng trắng hoặc đường dẫn.",
    };
  }
  if (trimmed.includes(":") || trimmed.includes("[") || trimmed.includes("]")) {
    return {
      ok: false,
      reason: "port_or_ipv6",
      message: "Không kèm số cổng hoặc địa chỉ IP. Vui lòng chỉ nhập tên miền hợp lệ.",
    };
  }
  if (NON_ASCII_RE.test(trimmed)) {
    return {
      ok: false,
      reason: "non_ascii",
      message:
        "Tên miền chỉ chấp nhận chữ cái không dấu (a-z), chữ số (0-9), dấu gạch nối (-) và dấu chấm (.).",
    };
  }

  // Strip at most one trailing DNS root dot; repeated trailing dots fail label validation below.
  const hostname =
    trimmed.endsWith(".") && !trimmed.endsWith("..") ? trimmed.slice(0, -1) : trimmed;

  if (hostname.length < 2 || hostname.length > 253) {
    return {
      ok: false,
      reason: "length",
      message: "Tên miền cần dài từ 2 đến 253 ký tự.",
    };
  }
  if (hostname === "localhost" || FORBIDDEN_SUFFIXES.some((s) => hostname.endsWith(s))) {
    return {
      ok: false,
      reason: "forbidden_host",
      message: "Tên miền nội bộ không thể dùng làm tên miền công khai cho cửa hàng.",
    };
  }
  if (!hostname.includes(".")) {
    return {
      ok: false,
      reason: "no_dot",
      message: "Tên miền cần có phần mở rộng hợp lệ (ví dụ: shop.thuonghieu.vn).",
    };
  }
  if (isIpv4Pattern(hostname)) {
    return {
      ok: false,
      reason: "ip_literal",
      message: "Không thể dùng địa chỉ IP làm tên miền riêng.",
    };
  }

  const root = cleanRootDomain(rootDomain);
  if (root && (hostname === root || hostname.endsWith(`.${root}`))) {
    return {
      ok: false,
      reason: "platform_domain",
      message: "Đây là địa chỉ thuộc hệ thống NexusMart. Hãy nhập tên miền riêng của bạn.",
    };
  }

  const labels = hostname.split(".");
  for (const label of labels) {
    if (!label || label.length > 63 || !LABEL_RE.test(label)) {
      return {
        ok: false,
        reason: "invalid_label",
        message:
          "Tên miền chưa đúng định dạng. Mỗi phần chỉ gồm chữ thường (a-z), số (0-9), dấu gạch nối (-) và không mở đầu hoặc kết thúc bằng dấu gạch nối.",
      };
    }
  }

  const tld = labels[labels.length - 1];
  if (tld.length < 2 || !/[a-z]/.test(tld)) {
    return {
      ok: false,
      reason: "ip_literal",
      message: "Phần mở rộng tên miền không hợp lệ.",
    };
  }

  return { ok: true, hostname };
}

export function isValidCustomHostname(
  hostname: string,
  rootDomain = "nexusmart.localhost"
): boolean {
  return validateCustomHostname(hostname, rootDomain).ok;
}

export function getDomainStatusMeta(status: string | null | undefined): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
  adminTone: "positive" | "warning" | "negative" | "neutral";
} {
  switch ((status ?? "").trim().toUpperCase()) {
    case "ACTIVE":
      return { label: "Đang hoạt động", tone: "pos", adminTone: "positive" };
    case "PENDING":
      return { label: "Đang chờ xác minh", tone: "warn", adminTone: "warning" };
    case "FAILED":
      return { label: "Xác minh thất bại", tone: "neg", adminTone: "negative" };
    case "REMOVING":
      return { label: "Đang gỡ bỏ", tone: "neutral", adminTone: "neutral" };
    default:
      return { label: "Đang xử lý", tone: "neutral", adminTone: "neutral" };
  }
}

/**
 * Turn a raw backend `last_error` into clear Vietnamese guidance for the seller without ever
 * leaking internal provider names, stack traces, or technical codes.
 */
export function formatDomainErrorForSeller(lastError: string | null | undefined): string {
  const raw = (lastError ?? "").trim();
  if (!raw) return "";
  const lower = raw.toLowerCase();

  if (lower.includes("7 day") || lower.includes("expired") || lower.includes("timeout")) {
    return "Quá thời hạn xác minh (7 ngày) mà chưa nhận thấy bản ghi DNS hợp lệ. Hãy gỡ tên miền này, kiểm tra lại cấu hình DNS rồi thêm lại.";
  }
  if (
    lower.includes("ssl") ||
    lower.includes("cert") ||
    lower.includes("tls") ||
    lower.includes("validation")
  ) {
    return "Chưa cấp được chứng chỉ bảo mật cho tên miền. Hãy giữ nguyên đủ các bản ghi DNS bên dưới rồi bấm Kiểm tra lại sau vài phút.";
  }
  if (
    lower.includes("dns") ||
    lower.includes("txt") ||
    lower.includes("cname") ||
    lower.includes("ownership") ||
    lower.includes("verify")
  ) {
    return "Chưa tìm thấy bản ghi DNS đúng như hướng dẫn. Hãy kiểm tra lại mục Tên và Giá trị tại nhà cung cấp tên miền rồi bấm Kiểm tra lại.";
  }
  if (lower.includes("remove") || lower.includes("delete")) {
    return "Hệ thống đang tiếp tục gỡ bỏ tên miền. Bạn vui lòng tải lại sau ít phút.";
  }
  return "Chưa thể xác minh tên miền lúc này. Vui lòng kiểm tra lại các bản ghi DNS bên dưới rồi bấm Kiểm tra lại.";
}

export interface DomainGatewayErrorInfo {
  status: number;
  code: string;
  message: string;
}

/**
 * Map a gateway error (kind, code, detail) on domain endpoints to safe Vietnamese messages and
 * contract-aligned error codes (`invalid_hostname`, `domain_taken`, `domain_limit`).
 */
export function resolveDomainGatewayError(err: {
  kind: string;
  status: number;
  code: string;
  detail: string;
}): DomainGatewayErrorInfo {
  const codeLower = (err.code || "").trim().toLowerCase();
  const detailLower = (err.detail || "").trim().toLowerCase();

  if (
    codeLower === "invalid_hostname" ||
    detailLower.includes("invalid_hostname") ||
    detailLower.includes("invalid hostname") ||
    (err.kind === "invalid" && detailLower.includes("host"))
  ) {
    return {
      status: 400,
      code: "invalid_hostname",
      message:
        "Tên miền không hợp lệ. Vui lòng nhập đúng tên miền (ví dụ: shop.thuonghieu.vn), không kèm http://, cổng hoặc ký tự đặc biệt.",
    };
  }

  if (
    codeLower === "domain_limit" ||
    detailLower.includes("domain_limit") ||
    detailLower.includes("at most 1") ||
    detailLower.includes("limit") ||
    detailLower.includes("already has a custom domain")
  ) {
    return {
      status: 409,
      code: "domain_limit",
      message:
        "Mỗi cửa hàng chỉ gắn tối đa 1 tên miền riêng. Hãy gỡ tên miền hiện tại trước khi thêm tên miền mới.",
    };
  }

  if (
    codeLower === "domain_taken" ||
    codeLower === "already_exists" ||
    detailLower.includes("domain_taken") ||
    detailLower.includes("already taken") ||
    detailLower.includes("already exists") ||
    detailLower.includes("duplicate")
  ) {
    return {
      status: 409,
      code: "domain_taken",
      message: "Tên miền này đã được cửa hàng khác sử dụng. Vui lòng chọn tên miền khác.",
    };
  }

  if (err.kind === "not_found") {
    return {
      status: 404,
      code: "not_found",
      message: "Không tìm thấy tên miền hoặc cửa hàng yêu cầu.",
    };
  }

  if (err.kind === "conflict") {
    return {
      status: 409,
      code: codeLower || "conflict",
      message: "Không thể thực hiện thao tác với tên miền ở trạng thái hiện tại. Vui lòng thử lại.",
    };
  }

  if (err.kind === "invalid") {
    return {
      status: 400,
      code: codeLower || "invalid_hostname",
      message: "Thông tin tên miền chưa hợp lệ. Vui lòng kiểm tra lại.",
    };
  }

  if (err.kind === "unauthenticated") {
    return {
      status: 401,
      code: "UNAUTHENTICATED",
      message: "Phiên đăng nhập đã hết hạn. Bạn vui lòng đăng nhập lại.",
    };
  }

  if (err.kind === "forbidden") {
    return {
      status: 403,
      code: err.code || "FORBIDDEN",
      message: "Bạn không có quyền thực hiện thao tác này.",
    };
  }

  if (err.kind === "rate_limited") {
    return {
      status: 429,
      code: "RATE_LIMITED",
      message: "Bạn thao tác hơi nhanh. Vui lòng thử lại sau ít phút.",
    };
  }

  return {
    status: 503,
    code: "UNAVAILABLE",
    message: "Hệ thống đang bận. Bạn vui lòng thử lại sau.",
  };
}
