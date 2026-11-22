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
