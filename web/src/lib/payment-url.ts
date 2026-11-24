/**
 * Strict pay_url allowlist validator (Wave 3b-2 F8).
 * Allows ONLY:
 *   1. External HTTPS URLs on zalopay.vn or *.zalopay.vn (e.g. sb-openapi.zalopay.vn,
 *      qcgateway.zalopay.vn, sbgateway.zalopay.vn) with no embedded credentials and default port.
 *   2. Same-origin mock checkout pages under /thanh-toan/ (relative path or matching currentOrigin).
 * Rejects javascript:, data:, vbscript:, non-HTTPS external origins, spoofed hostnames,
 * protocol-relative URLs, dot-segment escapes, credentials in URL, and control characters.
 */

const CONTROL_OR_SPACE_RE = /[\u0000-\u0020\u007f]/;
const VALID_DNS_LABEL_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const FALLBACK_ORIGIN = "http://nexusmart.local";
const ALLOWED_LOCAL_PREFIX = "/thanh-toan/";

export interface SafePayUrlResult {
  safe: boolean;
  href: string | null;
  isExternal: boolean;
  reason?: string;
}

export function isAllowedZaloPayHost(rawHost: string | null | undefined): boolean {
  const host = (rawHost ?? "").trim().toLowerCase();
  if (!host || host.length > 253) return false;
  if (host !== "zalopay.vn" && !host.endsWith(".zalopay.vn")) return false;
  const labels = host.split(".");
  return labels.every((label) => VALID_DNS_LABEL_RE.test(label));
}

function isSafeLocalCheckoutPath(pathname: string): boolean {
  return (
    pathname.startsWith(ALLOWED_LOCAL_PREFIX) &&
    !pathname.startsWith("//") &&
    !pathname.includes("\\") &&
    !CONTROL_OR_SPACE_RE.test(pathname)
  );
}

export function validateSafePayUrl(
  rawUrl: string | null | undefined,
  currentOrigin?: string | null
): SafePayUrlResult {
  if (typeof rawUrl !== "string") {
    return { safe: false, href: null, isExternal: false, reason: "EMPTY_URL" };
  }
  const trimmed = rawUrl.trim();
  if (
    !trimmed ||
    trimmed.length > 2048 ||
    trimmed.includes("\\") ||
    CONTROL_OR_SPACE_RE.test(trimmed)
  ) {
    return { safe: false, href: null, isExternal: false, reason: "INVALID_CHARS" };
  }

  // Case 1: Relative same-origin mock path (must start with single "/" and normalize under /thanh-toan/)
  if (trimmed.startsWith("/")) {
    if (trimmed.startsWith("//")) {
      return { safe: false, href: null, isExternal: false, reason: "PROTOCOL_RELATIVE" };
    }
    try {
      const parsed = new URL(trimmed, FALLBACK_ORIGIN);
      if (parsed.origin !== FALLBACK_ORIGIN || parsed.username || parsed.password) {
        return { safe: false, href: null, isExternal: false, reason: "ORIGIN_ESCAPE" };
      }
      if (!isSafeLocalCheckoutPath(parsed.pathname)) {
        return { safe: false, href: null, isExternal: false, reason: "DISALLOWED_LOCAL_PATH" };
      }
      const normalized = `${parsed.pathname}${parsed.search}${parsed.hash}`;
      if (!normalized.startsWith(ALLOWED_LOCAL_PREFIX) || normalized.startsWith("//")) {
        return { safe: false, href: null, isExternal: false, reason: "DISALLOWED_LOCAL_PATH" };
      }
      return { safe: true, href: normalized, isExternal: false };
    } catch {
      return { safe: false, href: null, isExternal: false, reason: "PARSE_ERROR" };
    }
  }

  // Case 2: Absolute URL — either matching same-origin /thanh-toan/... or external https://(*.zalopay.vn)
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return { safe: false, href: null, isExternal: false, reason: "PARSE_ERROR" };
  }

  if (parsed.username || parsed.password) {
    return { safe: false, href: null, isExternal: false, reason: "USERINFO_NOT_ALLOWED" };
  }

  if (currentOrigin) {
    try {
      const originParsed = new URL(currentOrigin);
      if (
        (parsed.protocol === "http:" || parsed.protocol === "https:") &&
        parsed.origin === originParsed.origin &&
        isSafeLocalCheckoutPath(parsed.pathname)
      ) {
        return {
          safe: true,
          href: `${parsed.pathname}${parsed.search}${parsed.hash}`,
          isExternal: false,
        };
      }
    } catch {
      // Ignore invalid currentOrigin and continue to external allowlist check.
    }
  }

  if (parsed.protocol !== "https:") {
    return { safe: false, href: null, isExternal: false, reason: "HTTPS_REQUIRED" };
  }

  if (parsed.port !== "" && parsed.port !== "443") {
    return { safe: false, href: null, isExternal: false, reason: "DISALLOWED_PORT" };
  }

  if (!isAllowedZaloPayHost(parsed.hostname)) {
    return { safe: false, href: null, isExternal: false, reason: "HOST_NOT_ALLOWED" };
  }

  return { safe: true, href: parsed.toString(), isExternal: true };
}
