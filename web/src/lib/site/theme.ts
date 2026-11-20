// Pure theme token validation, WCAG 2.1 contrast calculation, URL sanitization,
// and scoped CSS custom property mapping. Zero I/O, zero DOM, safe for server + client + tests.
import { DEFAULT_SITE_THEME, type SiteFontFamily, type SiteTheme } from "./types";

const HEX_COLOR_RE = /^#[0-9a-fA-F]{6}$/;
const CONTROL_CHAR_RE = /[\u0000-\u001F\u007F]/;
const MAX_URL_LENGTH = 500;

export function isValidHexColor(value: string | undefined | null): boolean {
  return typeof value === "string" && HEX_COLOR_RE.test(value.trim());
}

export function normalizeHexColor(value: string | undefined | null, fallback: string): string {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (HEX_COLOR_RE.test(trimmed)) return trimmed.toUpperCase();
  }
  return fallback.toUpperCase();
}

function parseRgb(hex: string): [number, number, number] | null {
  const clean = hex.trim();
  if (!HEX_COLOR_RE.test(clean)) return null;
  const n = Number.parseInt(clean.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function toHex([r, g, b]: [number, number, number]): string {
  const clamp = (v: number) => Math.max(0, Math.min(255, Math.round(v)));
  return (
    "#" +
    [clamp(r), clamp(g), clamp(b)]
      .map((v) => v.toString(16).padStart(2, "0"))
      .join("")
      .toUpperCase()
  );
}

function linearizeChannel(channel: number): number {
  const s = channel / 255;
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

/** WCAG 2.1 relative luminance in [0, 1]. */
export function relativeLuminance(hex: string): number {
  const rgb = parseRgb(hex);
  if (!rgb) return 0;
  const [r, g, b] = rgb.map(linearizeChannel);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG 2.1 contrast ratio in [1, 21]. Returns 1 if either color is invalid. */
export function contrastRatio(hexA: string, hexB: string): number {
  if (!isValidHexColor(hexA) || !isValidHexColor(hexB)) return 1;
  const l1 = relativeLuminance(hexA);
  const l2 = relativeLuminance(hexB);
  const lighter = Math.max(l1, l2);
  const darker = Math.min(l1, l2);
  return Number(((lighter + 0.05) / (darker + 0.05)).toFixed(2));
}

export interface ThemeContrastCheck {
  ok: boolean;
  bgText: number;
  surfaceText: number;
  accentText: number;
}

export function checkThemeContrast(
  theme: Pick<
    SiteTheme,
    "bg_color" | "surface_color" | "text_color" | "accent_color" | "accent_text_color"
  >,
  minRatio = 4.5
): ThemeContrastCheck {
  const bgText = contrastRatio(theme.bg_color, theme.text_color);
  const surfaceText = contrastRatio(theme.surface_color, theme.text_color);
  const accentText = contrastRatio(theme.accent_color, theme.accent_text_color);
  return {
    ok: bgText >= minRatio && surfaceText >= minRatio && accentText >= minRatio,
    bgText,
    surfaceText,
    accentText,
  };
}

/** Blend two hex colors (`weightA` in [0, 1]). */
export function mixHex(hexA: string, hexB: string, weightA: number): string {
  const a = parseRgb(hexA);
  const b = parseRgb(hexB);
  if (!a || !b) return hexA;
  const w = Math.max(0, Math.min(1, weightA));
  return toHex([a[0] * w + b[0] * (1 - w), a[1] * w + b[1] * (1 - w), a[2] * w + b[2] * (1 - w)]);
}

export function isDarkHex(hex: string): boolean {
  return relativeLuminance(hex) < 0.24;
}

/**
 * Defensive frontend URL sanitizer mirroring the backend Go `SafeURL` / `SafeHref` rule.
 * Allows ONLY:
 * - empty string (returns `""`)
 * - safe relative path starting with a single `/` (not `//`, no `\`, no control chars)
 * - valid `https://` URL with non-empty hostname
 * Rejects `javascript:`, `data:`, `vbscript:`, `http:`, protocol-relative `//`, and malformed inputs.
 */
export function sanitizeSafeUrl(raw: string | undefined | null): string {
  if (typeof raw !== "string") return "";
  const trimmed = raw.trim();
  if (!trimmed) return "";
  if (trimmed.length > MAX_URL_LENGTH) return "";
  if (CONTROL_CHAR_RE.test(trimmed) || trimmed.includes("\\")) return "";

  if (trimmed.startsWith("/")) {
    if (trimmed.startsWith("//")) return "";
    // Reject embedded scheme tricks or newlines
    if (/[\s]/.test(trimmed)) return "";
    return trimmed;
  }

  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return "";
  }
  if (parsed.protocol !== "https:" || !parsed.hostname) {
    return "";
  }
  return parsed.toString();
}

/**
 * Sanitize an interactive CTA href. Same rules as `sanitizeSafeUrl`, with optional fallback
 * (defaults to `""` when invalid).
 */
export function sanitizeSafeHref(raw: string | undefined | null, fallback = ""): string {
  const clean = sanitizeSafeUrl(raw);
  return clean || fallback;
}

export function resolveSiteFontStack(fontFamily: SiteFontFamily | string | undefined): string {
  switch (fontFamily) {
    case "be-vietnam-pro":
      return 'var(--font-be-vietnam-pro, var(--font-work-sans)), "Work Sans", sans-serif';
    case "ibm-plex-sans":
      return 'var(--font-ibm-plex-sans, var(--font-work-sans)), "Work Sans", sans-serif';
    case "inter":
    case "plus-jakarta-sans":
    case "work-sans":
    default:
      return 'var(--font-work-sans), "Work Sans", sans-serif';
  }
}

export interface SiteThemeVars {
  "--site-bg": string;
  "--site-surface": string;
  "--site-surface-alt": string;
  "--site-ink": string;
  "--site-muted": string;
  "--site-accent": string;
  "--site-accent-ink": string;
  "--site-accent-soft": string;
  "--site-accent-soft-ink": string;
  "--site-divider": string;
  "--site-band-bg": string;
  "--site-band-ink": string;
  "--site-font": string;
}

/**
 * Maps validated `SiteTheme` tokens to scoped CSS custom properties applied strictly on the
 * site wrapper element (`style={buildSiteThemeVars(theme) as React.CSSProperties}`).
 * Never produces raw CSS strings or `dangerouslySetInnerHTML`.
 */
export function buildSiteThemeVars(theme: SiteTheme | undefined | null): SiteThemeVars {
  const bg = normalizeHexColor(theme?.bg_color, DEFAULT_SITE_THEME.bg_color);
  const surface = normalizeHexColor(theme?.surface_color, DEFAULT_SITE_THEME.surface_color);
  const ink = normalizeHexColor(theme?.text_color, DEFAULT_SITE_THEME.text_color);
  const accent = normalizeHexColor(theme?.accent_color, DEFAULT_SITE_THEME.accent_color);
  const accentInk = normalizeHexColor(
    theme?.accent_text_color,
    DEFAULT_SITE_THEME.accent_text_color
  );

  // Derive accessible muted text (stays >= 7:1 on typical palettes, >= 4.5:1 minimum)
  const mutedCandidate = mixHex(ink, bg, 0.8);
  const muted =
    contrastRatio(mutedCandidate, bg) >= 4.8 && contrastRatio(mutedCandidate, surface) >= 4.8
      ? mutedCandidate
      : ink;

  const darkGround = isDarkHex(bg);
  const surfaceAlt = darkGround ? mixHex(surface, ink, 0.9) : mixHex(surface, ink, 0.95);
  const divider = darkGround ? mixHex(surface, ink, 0.84) : mixHex(bg, ink, 0.88);
  const accentSoft = darkGround ? mixHex(accent, surface, 0.24) : mixHex(accent, surface, 0.12);
  const accentSoftInk =
    contrastRatio(ink, accentSoft) >= 4.8
      ? ink
      : contrastRatio(accentInk, accentSoft) >= 4.8
        ? accentInk
        : darkGround
          ? "#F7F9FA"
          : "#152229";

  // Inverted contrast band for footer / primary highlight anchor
  const bandBg = darkGround ? surface : ink;
  const bandInk = darkGround ? ink : bg;

  return {
    "--site-bg": bg,
    "--site-surface": surface,
    "--site-surface-alt": surfaceAlt,
    "--site-ink": ink,
    "--site-muted": muted,
    "--site-accent": accent,
    "--site-accent-ink": accentInk,
    "--site-accent-soft": accentSoft,
    "--site-accent-soft-ink": accentSoftInk,
    "--site-divider": divider,
    "--site-band-bg": bandBg,
    "--site-band-ink": bandInk,
    "--site-font": resolveSiteFontStack(theme?.font_family),
  };
}
