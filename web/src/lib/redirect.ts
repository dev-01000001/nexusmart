const BASE_ORIGIN = "http://nexusmart.local";
const CONTROL_CHAR_RE = /[\u0000-\u001f\u007f]/;

/**
 * Normalizes and validates an internal redirect path, preventing open redirects
 * (protocol-relative URLs, dot-segment normalization tricks such as `/..//evil.com`,
 * `/.//evil.com`, `/a/..//evil.com`, `/%2e%2e//evil.com`, backslashes, absolute URLs,
 * and control characters).
 */
export function sanitizeNextPath(raw: string | null | undefined, fallback = "/"): string {
  if (!raw) return fallback;
  const trimmed = raw.trim();
  if (
    !trimmed.startsWith("/") ||
    trimmed.startsWith("//") ||
    trimmed.includes("\\") ||
    CONTROL_CHAR_RE.test(trimmed)
  ) {
    return fallback;
  }

  try {
    const parsed = new URL(trimmed, BASE_ORIGIN);
    if (parsed.origin !== BASE_ORIGIN) {
      return fallback;
    }
    if (
      !parsed.pathname.startsWith("/") ||
      parsed.pathname.startsWith("//") ||
      parsed.pathname.includes("\\") ||
      CONTROL_CHAR_RE.test(parsed.pathname)
    ) {
      return fallback;
    }
    const normalized = `${parsed.pathname}${parsed.search}${parsed.hash}`;
    if (
      !normalized.startsWith("/") ||
      normalized.startsWith("//") ||
      CONTROL_CHAR_RE.test(normalized)
    ) {
      return fallback;
    }
    return normalized;
  } catch {
    return fallback;
  }
}

export function getDefaultPathForRole(role: string | null | undefined): string {
  switch ((role ?? "").trim().toLowerCase()) {
    case "admin":
    case "super_admin":
      return "/admin";
    case "seller":
      return "/seller";
    default:
      return "/";
  }
}

/**
 * Resolves the safe post-login redirect path based on backend-verified role and optional `?next=`.
 * Without an explicit eligible `?next=`, routes seller -> `/seller`, admin/super_admin -> `/admin`,
 * and buyer -> `/`.
 */
export function resolvePostLoginRedirect(
  role: string | null | undefined,
  rawNext: string | null | undefined
): string {
  const normalizedRole = (role ?? "").trim().toLowerCase();
  const roleHome = getDefaultPathForRole(normalizedRole);
  const explicitNext = sanitizeNextPath(rawNext, "");

  if (
    !explicitNext ||
    explicitNext === "/" ||
    explicitNext === "/dang-nhap" ||
    explicitNext.startsWith("/dang-nhap?") ||
    explicitNext === "/dang-ky" ||
    explicitNext.startsWith("/dang-ky?") ||
    explicitNext === "/quen-mat-khau" ||
    explicitNext.startsWith("/quen-mat-khau?") ||
    explicitNext === "/dat-lai-mat-khau" ||
    explicitNext.startsWith("/dat-lai-mat-khau?")
  ) {
    return roleHome;
  }

  const isAdminPath = explicitNext === "/admin" || explicitNext.startsWith("/admin/");
  const isSellerPath = explicitNext === "/seller" || explicitNext.startsWith("/seller/");

  if (normalizedRole === "buyer" && (isAdminPath || isSellerPath)) {
    return roleHome;
  }
  if (normalizedRole === "seller" && isAdminPath) {
    return roleHome;
  }

  return explicitNext;
}
