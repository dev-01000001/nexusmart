// Zero-dependency strict validator, line/column JSON source mapper, XSS/SSRF/SVG defense,
// and lossless v1 -> v2 converter for Declarative Theme JSON v2 (ADR-0001).
// Pure TS (zero DOM/Node-only APIs) — safe for RSC, client components, and Node 22 unit tests.

import { ALLOWED_SITE_FONTS, DEFAULT_SITE_THEME, type SiteFontFamily } from "../../lib/site/types.ts";
import { checkThemeContrast, isValidHexColor } from "../../lib/site/theme.ts";
import {
  ALLOWED_ARCHETYPES,
  ALLOWED_BLOCK_TYPES,
  ALLOWED_CARD_STYLES,
  ALLOWED_CART_LAYOUTS,
  ALLOWED_CATALOG_LAYOUTS,
  ALLOWED_CONTAINER_WIDTHS,
  ALLOWED_DENSITIES,
  ALLOWED_FOOTER_STYLES,
  ALLOWED_HEADER_STYLES,
  ALLOWED_INFO_LAYOUTS,
  ALLOWED_PDP_GALLERY_STYLES,
  ALLOWED_PDP_LAYOUTS,
  ALLOWED_RADII,
  ALLOWED_SECTION_PADDINGS,
  ALLOWED_SECTION_SURFACES,
  ALLOWED_V2_SECTION_TYPES,
  MAX_BLOCKS_PER_SECTION,
  MAX_CONFIG_BYTES,
  MAX_HOME_SECTIONS,
  MAX_JSON_DEPTH,
  MAX_PAGE_SECTIONS,
  MAX_TOTAL_SECTIONS,
  MAX_URL_LENGTH,
  MIN_WCAG_CONTRAST,
  SECTION_VARIANTS_BY_TYPE,
  SITE_SCHEMA_VERSION_V2,
  type CartPageConfig,
  type CatalogPageConfig,
  type InfoPageConfig,
  type PdpPageConfig,
  type SectionSettings,
  type SiteBlockType,
  type SiteLayoutConfig,
  type SitePagesConfig,
  type SiteSectionBlock,
  type SiteSectionV2,
  type SiteSectionV2Type,
  type SiteTemplateConfig,
  type SiteThemeV2,
  type TemplateValidationIssue,
  type TemplateValidationResult,
} from "./templateTypes.ts";

const TEMPLATE_ID_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;
const CONTROL_CHAR_NO_NL_RE = /[\u0000-\u001F\u007F]/;
const CONTROL_CHAR_ALLOW_NL_RE = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/;

// Explicit save-time XSS, style injection, and SVG payload patterns.
const UNSAFE_TEXT_PATTERNS: ReadonlyArray<{ re: RegExp; reason: string }> = [
  { re: /<\s*\/?\s*script\b/i, reason: "chứa thẻ <script> không được phép" },
  { re: /<\s*\/?\s*style\b/i, reason: "chứa thẻ <style> không được phép" },
  { re: /<\s*\/?\s*svg\b/i, reason: "chứa thẻ <svg> không được phép" },
  { re: /<\s*\/?\s*(iframe|object|embed|link|meta)\b/i, reason: "chứa thẻ HTML nhúng không an toàn" },
  { re: /\bon[a-z]{2,20}\s*=/i, reason: "chứa thuộc tính sự kiện HTML (on*=)" },
  { re: /\b(javascript|vbscript)\s*:/i, reason: "chứa giao thức thực thi mã (javascript:/vbscript:)" },
  { re: /\bdata\s*:/i, reason: "chứa URI data: không được phép" },
  { re: /\bexpression\s*\(/i, reason: "chứa biểu thức CSS expression()" },
  { re: /\burl\s*\(/i, reason: "chứa hàm CSS url()" },
  { re: /@import\b/i, reason: "chứa chỉ thị CSS @import" },
  { re: /-moz-binding\b/i, reason: "chứa thuộc tính CSS không an toàn" },
];

const IPV4_RE = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
const DECIMAL_OR_HEX_IP_RE = /^(?:0x[0-9a-f]+|0[0-7]+|\d+)$/i;

export interface SourceLoc {
  line: number;
  column: number;
}

interface ParsedJsonWithLocations {
  ok: boolean;
  value: unknown;
  locations: Map<string, SourceLoc>;
  issues: TemplateValidationIssue[];
}

function offsetToLoc(src: string, offset: number): SourceLoc {
  const clamped = Math.max(0, Math.min(offset, src.length));
  let line = 1;
  let col = 1;
  for (let i = 0; i < clamped; i++) {
    if (src.charCodeAt(i) === 10) {
      line++;
      col = 1;
    } else {
      col++;
    }
  }
  return { line, column: col };
}

/**
 * Recursive-descent JSON parser (RFC 8259) that simultaneously:
 * 1. Records exact (line, column) for every JSON path (e.g. `sections[0].props.title`).
 * 2. Enforces MAX_JSON_DEPTH (<= 8) pre-validation.
 * 3. Rejects duplicate object keys (preventing Go/JS parser smuggling).
 * 4. Reports exact (line, column) on syntax errors.
 */
export function parseJsonWithLocations(src: string): ParsedJsonWithLocations {
  const locations = new Map<string, SourceLoc>();
  const issues: TemplateValidationIssue[] = [];
  const len = src.length;
  let pos = 0;

  const byteLen = new TextEncoder().encode(src).length;
  if (byteLen > MAX_CONFIG_BYTES) {
    return {
      ok: false,
      value: null,
      locations,
      issues: [
        {
          path: "$",
          line: 1,
          column: 1,
          code: "CONFIG_TOO_LARGE",
          message: `Cấu hình vượt quá giới hạn ${MAX_CONFIG_BYTES} byte (${byteLen} byte).`,
        },
      ],
    };
  }

  function skipWs(): void {
    while (pos < len) {
      const c = src.charCodeAt(pos);
      if (c === 32 || c === 9 || c === 10 || c === 13) pos++;
      else break;
    }
  }

  function fail(path: string, code: string, message: string, at = pos): never {
    const loc = offsetToLoc(src, at);
    issues.push({ path, line: loc.line, column: loc.column, code, message });
    throw new Error(code);
  }

  function parseString(path: string): string {
    const start = pos;
    if (src.charCodeAt(pos) !== 34) fail(path, "JSON_SYNTAX", "Thiếu dấu ngoặc kép mở chuỗi.");
    pos++;
    let out = "";
    while (pos < len) {
      const ch = src.charCodeAt(pos);
      if (ch === 34) {
        pos++;
        return out;
      }
      if (ch < 0x20) {
        fail(path, "JSON_SYNTAX", "Ký tự điều khiển chưa thoát trong chuỗi JSON.");
      }
      if (ch === 92) {
        pos++;
        if (pos >= len) fail(path, "JSON_SYNTAX", "Chuỗi JSON kết thúc đột ngột sau dấu \\.");
        const esc = src[pos++];
        switch (esc) {
          case '"':
            out += '"';
            break;
          case "\\":
            out += "\\";
            break;
          case "/":
            out += "/";
            break;
          case "b":
            out += "\b";
            break;
          case "f":
            out += "\f";
            break;
          case "n":
            out += "\n";
            break;
          case "r":
            out += "\r";
            break;
          case "t":
            out += "\t";
            break;
          case "u": {
            const hex = src.slice(pos, pos + 4);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
              fail(path, "JSON_SYNTAX", "Mã thoát \\uXXXX không hợp lệ.", pos - 2);
            }
            out += String.fromCharCode(Number.parseInt(hex, 16));
            pos += 4;
            break;
          }
          default:
            fail(path, "JSON_SYNTAX", `Ký tự thoát \\${esc} không hợp lệ.`, pos - 2);
        }
      } else {
        out += src[pos++];
      }
    }
    fail(path, "JSON_SYNTAX", "Chuỗi JSON chưa được đóng bằng dấu ngoặc kép.", start);
  }

  function parseNumber(path: string): number {
    const start = pos;
    if (src[pos] === "-") pos++;
    if (pos >= len) fail(path, "JSON_SYNTAX", "Số JSON không hợp lệ.", start);
    if (src[pos] === "0") {
      pos++;
    } else if (src[pos] >= "1" && src[pos] <= "9") {
      while (pos < len && src[pos] >= "0" && src[pos] <= "9") pos++;
    } else {
      fail(path, "JSON_SYNTAX", "Số JSON không hợp lệ.", start);
    }
    if (src[pos] === ".") {
      pos++;
      if (pos >= len || src[pos] < "0" || src[pos] > "9") {
        fail(path, "JSON_SYNTAX", "Phần thập phân của số JSON không hợp lệ.", start);
      }
      while (pos < len && src[pos] >= "0" && src[pos] <= "9") pos++;
    }
    if (src[pos] === "e" || src[pos] === "E") {
      pos++;
      if (src[pos] === "+" || src[pos] === "-") pos++;
      if (pos >= len || src[pos] < "0" || src[pos] > "9") {
        fail(path, "JSON_SYNTAX", "Số mũ JSON không hợp lệ.", start);
      }
      while (pos < len && src[pos] >= "0" && src[pos] <= "9") pos++;
    }
    const num = Number(src.slice(start, pos));
    if (!Number.isFinite(num)) fail(path, "JSON_SYNTAX", "Giá trị số không hữu hạn.", start);
    return num;
  }

  function parseValue(path: string, depth: number): unknown {
    skipWs();
    if (pos >= len) fail(path, "JSON_SYNTAX", "Nội dung JSON kết thúc sớm.");
    const start = pos;
    locations.set(path, offsetToLoc(src, start));
    const ch = src[pos];

    if (ch === "{") {
      const nextDepth = depth + 1;
      if (nextDepth > MAX_JSON_DEPTH) {
        fail(
          path,
          "MAX_DEPTH_EXCEEDED",
          `Độ sâu lồng nhau JSON vượt quá giới hạn tối đa ${MAX_JSON_DEPTH}.`,
          start
        );
      }
      pos++;
      skipWs();
      const obj: Record<string, unknown> = {};
      if (src[pos] === "}") {
        pos++;
        return obj;
      }
      const seenKeys = new Set<string>();
      while (true) {
        skipWs();
        const keyStart = pos;
        if (src[pos] !== '"') {
          fail(path, "JSON_SYNTAX", "Tên khóa trong đối tượng JSON phải nằm trong dấu ngoặc kép.");
        }
        const key = parseString(path);
        const childPath = path === "$" ? key : `${path}.${key}`;
        if (seenKeys.has(key)) {
          const loc = offsetToLoc(src, keyStart);
          issues.push({
            path: childPath,
            line: loc.line,
            column: loc.column,
            code: "DUPLICATE_KEY",
            message: `Khóa "${key}" bị trùng lặp trong cùng đối tượng JSON.`,
          });
        }
        seenKeys.add(key);
        skipWs();
        if (src[pos] !== ":") fail(childPath, "JSON_SYNTAX", `Thiếu dấu ':' sau khóa "${key}".`);
        pos++;
        const val = parseValue(childPath, nextDepth);
        locations.set(childPath, offsetToLoc(src, keyStart));
        obj[key] = val;
        skipWs();
        if (src[pos] === ",") {
          pos++;
          continue;
        }
        if (src[pos] === "}") {
          pos++;
          return obj;
        }
        fail(path, "JSON_SYNTAX", "Thiếu dấu ',' hoặc '}' trong đối tượng JSON.");
      }
    }

    if (ch === "[") {
      const nextDepth = depth + 1;
      if (nextDepth > MAX_JSON_DEPTH) {
        fail(
          path,
          "MAX_DEPTH_EXCEEDED",
          `Độ sâu lồng nhau JSON vượt quá giới hạn tối đa ${MAX_JSON_DEPTH}.`,
          start
        );
      }
      pos++;
      skipWs();
      const arr: unknown[] = [];
      if (src[pos] === "]") {
        pos++;
        return arr;
      }
      let idx = 0;
      while (true) {
        const childPath = `${path}[${idx}]`;
        arr.push(parseValue(childPath, nextDepth));
        idx++;
        skipWs();
        if (src[pos] === ",") {
          pos++;
          continue;
        }
        if (src[pos] === "]") {
          pos++;
          return arr;
        }
        fail(path, "JSON_SYNTAX", "Thiếu dấu ',' hoặc ']' trong mảng JSON.");
      }
    }

    if (ch === '"') return parseString(path);
    if (ch === "-" || (ch >= "0" && ch <= "9")) return parseNumber(path);
    if (src.startsWith("true", pos)) {
      pos += 4;
      return true;
    }
    if (src.startsWith("false", pos)) {
      pos += 5;
      return false;
    }
    if (src.startsWith("null", pos)) {
      pos += 4;
      return null;
    }
    fail(path, "JSON_SYNTAX", `Ký tự không hợp lệ "${ch}" trong JSON.`);
  }

  try {
    const value = parseValue("$", 0);
    skipWs();
    if (pos < len) {
      const loc = offsetToLoc(src, pos);
      issues.push({
        path: "$",
        line: loc.line,
        column: loc.column,
        code: "TRAILING_TOKEN",
        message: "Có dữ liệu thừa sau đối tượng JSON chính.",
      });
    }
    return { ok: issues.length === 0, value, locations, issues };
  } catch {
    return { ok: false, value: null, locations, issues };
  }
}

export function checkUnsafeText(raw: string): string | null {
  for (const { re, reason } of UNSAFE_TEXT_PATTERNS) {
    if (re.test(raw)) return reason;
  }
  return null;
}

export function isPrivateOrInternalHost(rawHost: string): boolean {
  let host = rawHost.trim().toLowerCase();
  if (!host) return true;
  if (host.startsWith("[") && host.endsWith("]")) {
    host = host.slice(1, -1);
  }
  host = host.replace(/\.+$/, "");
  if (!host) return true;

  // Reject any IPv6 literal (including ::1, ::ffff:127.0.0.1, fe80::, fc00::/7)
  if (host.includes(":")) return true;

  // Reject localhost and internal/reserved suffixes
  if (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host.endsWith(".local") ||
    host.endsWith(".internal") ||
    host.endsWith(".localdomain") ||
    host.endsWith(".home.arpa") ||
    host === "metadata.google.internal"
  ) {
    return true;
  }

  // Reject single-label hostnames or integer/hex/octal IP encodings (e.g. 2130706433, 0x7f000001)
  if (!host.includes(".") || DECIMAL_OR_HEX_IP_RE.test(host)) {
    return true;
  }

  // Reject any IPv4 literal (public or private) on image URLs to eliminate SSRF IP tricks
  const ipv4 = IPV4_RE.exec(host);
  if (ipv4) {
    return true;
  }

  // Reject numeric-only labels (e.g. 0177.0.0.1 or 127.1)
  const parts = host.split(".");
  if (parts.every((p) => DECIMAL_OR_HEX_IP_RE.test(p))) {
    return true;
  }

  return false;
}

function isSvgUrl(raw: string): boolean {
  const lower = raw.toLowerCase();
  if (lower.includes("image/svg") || lower.includes("svg+xml")) return true;

  let decoded = lower;
  try {
    decoded = decodeURIComponent(lower);
  } catch {
    // Keep raw lower if malformed percent-encoding
  }
  if (decoded.includes("image/svg") || decoded.includes("svg+xml")) return true;

  const withoutHash = decoded.split("#")[0] ?? decoded;
  const pathOnly = (withoutHash.split("?")[0] ?? withoutHash).trim();
  if (pathOnly.endsWith(".svg") || pathOnly.endsWith(".svgz")) return true;

  return false;
}

/** Validates CTA/navigation href (empty, single-slash relative `/...`, or `https://...`). */
export function validateSafeHrefStrict(raw: unknown): string | null {
  if (raw === undefined || raw === null || raw === "") return null;
  if (typeof raw !== "string") return "Đường dẫn phải là chuỗi ký tự.";
  if (raw.length > MAX_URL_LENGTH) return `Đường dẫn tối đa ${MAX_URL_LENGTH} ký tự.`;
  if (CONTROL_CHAR_NO_NL_RE.test(raw) || raw.includes("\\")) {
    return "Đường dẫn chứa ký tự điều khiển hoặc dấu \\ không được phép.";
  }
  const s = raw.trim();
  if (!s) return null;
  if (/\s/.test(s)) return "Đường dẫn không được chứa khoảng trắng.";

  const lower = s.toLowerCase();
  for (const bad of ["javascript:", "data:", "vbscript:", "http:", "file:", "blob:", "ftp:"]) {
    if (lower.startsWith(bad)) {
      return `Giao thức ${bad} bị chặn vì lý do bảo mật.`;
    }
  }
  if (s.startsWith("/")) {
    if (s.startsWith("//")) return "Không cho phép đường dẫn dạng //.";
    return null;
  }
  try {
    const parsed = new URL(s);
    if (parsed.protocol !== "https:" || !parsed.hostname || parsed.username || parsed.password) {
      return "Chỉ chấp nhận đường dẫn nội bộ (/) hoặc https:// hợp lệ.";
    }
    return null;
  } catch {
    return "Đường dẫn không hợp lệ.";
  }
}

/** Validates image URL with SVG blocking and SSRF defense. */
export function validateImageUrlStrict(raw: unknown, required = false): string | null {
  if (raw === undefined || raw === null || raw === "") {
    return required ? "Đường dẫn ảnh là bắt buộc." : null;
  }
  if (typeof raw !== "string") return "Đường dẫn ảnh phải là chuỗi ký tự.";
  const hrefErr = validateSafeHrefStrict(raw);
  if (hrefErr) return hrefErr;

  const s = raw.trim();
  if (!s) return required ? "Đường dẫn ảnh là bắt buộc." : null;

  if (isSvgUrl(s)) {
    return "Không cho phép ảnh định dạng SVG (.svg/.svgz) để chống XSS.";
  }

  if (!s.startsWith("/")) {
    try {
      const parsed = new URL(s);
      if (isPrivateOrInternalHost(parsed.hostname)) {
        return "URL ảnh trỏ tới địa chỉ nội bộ/IP không được phép (chống SSRF).";
      }
    } catch {
      return "Đường dẫn ảnh không hợp lệ.";
    }
  }
  return null;
}

/** Defensive runtime sanitizer for image URLs in React renderers. */
export function sanitizeSafeImageUrl(raw: string | undefined | null): string {
  if (typeof raw !== "string") return "";
  const trimmed = raw.trim();
  if (!trimmed) return "";
  return validateImageUrlStrict(trimmed, false) === null ? trimmed : "";
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function createDefaultLayoutConfig(): SiteLayoutConfig {
  return {
    archetype: "classic",
    header_style: "minimal-split",
    footer_style: "minimal-row",
    container_width: "standard",
    card_style: "minimal",
    show_announcement: false,
    announcement_text: "",
    slots: {
      search: true,
      chat: true,
      assistant: true,
    },
  };
}

export function createDefaultPagesConfig(): SitePagesConfig {
  return {
    catalog: {
      layout: "top-bar",
      columns: 4,
      show_filters: true,
      show_search: true,
      show_sku_matrix: false,
      intro_title: "",
      intro_subtitle: "",
      sections: [],
    },
    pdp: {
      layout: "classic-split",
      gallery_style: "thumbnails",
      show_vouchers: true,
      show_sku_table: false,
      show_story_band: true,
      show_related: true,
      sections: [],
    },
    cart: {
      layout: "split-summary",
      show_vouchers: true,
      show_addons: true,
      trust_note: "",
      sections: [],
    },
    info: {
      layout: "split-hero",
      show_factory_profile: true,
      show_toc: false,
      show_showcase: true,
      sections: [],
    },
  };
}

/**
 * Strict schema + security validator for Declarative Theme JSON v2 with exact line/column diagnostics.
 * Accepts either a raw JSON string (giving real line/column numbers) or a JS object.
 */
export function validateSiteTemplateJson(input: string | unknown): TemplateValidationResult {
  let rootVal: unknown;
  let locations = new Map<string, SourceLoc>();
  const issues: TemplateValidationIssue[] = [];

  if (typeof input === "string") {
    const parsed = parseJsonWithLocations(input);
    locations = parsed.locations;
    issues.push(...parsed.issues);
    if (!parsed.ok) {
      return { ok: false, config: null, issues };
    }
    rootVal = parsed.value;
  } else {
    const serialized = JSON.stringify(input, null, 2);
    if (!serialized) {
      return {
        ok: false,
        config: null,
        issues: [{ path: "$", line: 1, column: 1, code: "INVALID_JSON", message: "Cấu hình rỗng." }],
      };
    }
    return validateSiteTemplateJson(serialized);
  }

  function locOf(path: string): SourceLoc {
    return locations.get(path) ?? locations.get("$") ?? { line: 1, column: 1 };
  }

  function addIssue(path: string, code: string, message: string): void {
    const loc = locOf(path);
    issues.push({ path, line: loc.line, column: loc.column, code, message });
  }

  function checkAllowedKeys(obj: Record<string, unknown>, path: string, allowed: readonly string[]): void {
    const set = new Set(allowed);
    for (const k of Object.keys(obj)) {
      if (!set.has(k)) {
        const childPath = path === "$" ? k : `${path}.${k}`;
        addIssue(childPath, "UNKNOWN_FIELD", `Trường "${k}" không thuộc schema được phép.`);
      }
    }
  }

  function checkText(
    val: unknown,
    path: string,
    minLen: number,
    maxLen: number,
    allowNewlines: boolean
  ): string {
    if (val === undefined) {
      if (minLen > 0) addIssue(path, "REQUIRED", "Trường văn bản này là bắt buộc.");
      return "";
    }
    if (typeof val !== "string") {
      addIssue(path, "INVALID_TYPE", "Giá trị phải là chuỗi văn bản.");
      return "";
    }
    const ctrlRe = allowNewlines ? CONTROL_CHAR_ALLOW_NL_RE : CONTROL_CHAR_NO_NL_RE;
    if (ctrlRe.test(val)) {
      addIssue(path, "INVALID_CHARS", "Chuỗi chứa ký tự điều khiển không hợp lệ.");
      return "";
    }
    const unsafe = checkUnsafeText(val);
    if (unsafe) {
      addIssue(path, "XSS_REJECTED", `Nội dung bị từ chối vì ${unsafe}.`);
      return "";
    }
    const trimmed = val.trim();
    const chars = Array.from(trimmed).length;
    if (chars < minLen) {
      addIssue(path, "TOO_SHORT", `Cần tối thiểu ${minLen} ký tự.`);
    } else if (chars > maxLen) {
      addIssue(path, "TOO_LONG", `Vượt quá giới hạn tối đa ${maxLen} ký tự.`);
    }
    return trimmed;
  }

  function checkEnum<T extends string>(
    val: unknown,
    path: string,
    allowed: readonly T[],
    fallback: T,
    required = false
  ): T {
    if (val === undefined || val === "") {
      if (required && !allowed.includes("" as T)) {
        addIssue(path, "REQUIRED", `Cần chọn một trong các giá trị: ${allowed.filter(Boolean).join(", ")}.`);
      }
      return fallback;
    }
    if (typeof val !== "string" || !allowed.includes(val as T)) {
      addIssue(
        path,
        "INVALID_ENUM",
        `Giá trị "${String(val)}" không hợp lệ. Chỉ chấp nhận: ${allowed.filter(Boolean).join(", ")}.`
      );
      return fallback;
    }
    return val as T;
  }

  function checkBool(val: unknown, path: string, fallback: boolean): boolean {
    if (val === undefined) return fallback;
    if (typeof val !== "boolean") {
      addIssue(path, "INVALID_TYPE", "Giá trị phải là true hoặc false.");
      return fallback;
    }
    return val;
  }

  function checkInt(val: unknown, path: string, min: number, max: number, fallback: number): number {
    if (val === undefined) return fallback;
    if (typeof val !== "number" || !Number.isInteger(val)) {
      addIssue(path, "INVALID_TYPE", "Giá trị phải là số nguyên.");
      return fallback;
    }
    if (val < min || val > max) {
      addIssue(path, "OUT_OF_RANGE", `Giá trị phải nằm trong khoảng ${min}..${max}.`);
      return fallback;
    }
    return val;
  }

  function validateBlock(raw: unknown, path: string): SiteSectionBlock | null {
    if (!isRecord(raw)) {
      addIssue(path, "INVALID_TYPE", "Mỗi block phải là một đối tượng JSON.");
      return null;
    }
    checkAllowedKeys(raw, path, ["id", "type", "props"]);
    const id = checkText(raw.id, `${path}.id`, 1, 64, false);
    const type = checkEnum<SiteBlockType>(raw.type, `${path}.type`, ALLOWED_BLOCK_TYPES, "paragraph", true);
    if (!isRecord(raw.props)) {
      addIssue(`${path}.props`, "INVALID_TYPE", "block.props phải là một đối tượng JSON.");
      return null;
    }
    const p = raw.props;
    const pp = `${path}.props`;

    switch (type) {
      case "heading": {
        checkAllowedKeys(p, pp, ["text", "level"]);
        const text = checkText(p.text, `${pp}.text`, 1, 120, false);
        const level = checkEnum(p.level, `${pp}.level`, ["", "h2", "h3"] as const, "h3");
        return { id, type, props: { text, level } };
      }
      case "paragraph": {
        checkAllowedKeys(p, pp, ["text"]);
        const text = checkText(p.text, `${pp}.text`, 1, 800, true);
        return { id, type, props: { text } };
      }
      case "badge": {
        checkAllowedKeys(p, pp, ["label", "tone"]);
        const label = checkText(p.label, `${pp}.label`, 1, 48, false);
        const tone = checkEnum(p.tone, `${pp}.tone`, ["", "neutral", "accent", "band"] as const, "neutral");
        return { id, type, props: { label, tone } };
      }
      case "cta": {
        checkAllowedKeys(p, pp, ["label", "href", "style"]);
        const label = checkText(p.label, `${pp}.label`, 1, 48, false);
        const hrefErr = validateSafeHrefStrict(p.href);
        if (hrefErr) addIssue(`${pp}.href`, "UNSAFE_URL", hrefErr);
        const href = typeof p.href === "string" ? p.href.trim() : "";
        if (!href) addIssue(`${pp}.href`, "REQUIRED", "Đường dẫn nút CTA là bắt buộc.");
        const style = checkEnum(p.style, `${pp}.style`, ["", "primary", "secondary", "link"] as const, "primary");
        return { id, type, props: { label, href, style } };
      }
      case "image": {
        checkAllowedKeys(p, pp, ["image_url", "alt", "caption", "aspect"]);
        const imgErr = validateImageUrlStrict(p.image_url, true);
        if (imgErr) addIssue(`${pp}.image_url`, "UNSAFE_IMAGE_URL", imgErr);
        const image_url = typeof p.image_url === "string" ? p.image_url.trim() : "";
        const alt = checkText(p.alt, `${pp}.alt`, 0, 140, false);
        const caption = checkText(p.caption, `${pp}.caption`, 0, 200, false);
        const aspect = checkEnum(p.aspect, `${pp}.aspect`, ["", "16:9", "4:3", "3:4", "1:1"] as const, "4:3");
        return { id, type, props: { image_url, alt, caption, aspect } };
      }
      case "stat": {
        checkAllowedKeys(p, pp, ["value", "label", "note"]);
        const value = checkText(p.value, `${pp}.value`, 1, 32, false);
        const label = checkText(p.label, `${pp}.label`, 1, 60, false);
        const note = checkText(p.note, `${pp}.note`, 0, 140, false);
        return { id, type, props: { value, label, note } };
      }
      case "policy": {
        checkAllowedKeys(p, pp, ["heading", "detail"]);
        const heading = checkText(p.heading, `${pp}.heading`, 1, 80, false);
        const detail = checkText(p.detail, `${pp}.detail`, 1, 400, true);
        return { id, type, props: { heading, detail } };
      }
      case "quote": {
        checkAllowedKeys(p, pp, ["body", "author", "role"]);
        const body = checkText(p.body, `${pp}.body`, 1, 400, true);
        const author = checkText(p.author, `${pp}.author`, 1, 80, false);
        const role = checkText(p.role, `${pp}.role`, 0, 80, false);
        return { id, type, props: { body, author, role } };
      }
      case "feature": {
        checkAllowedKeys(p, pp, ["title", "detail", "tag"]);
        const title = checkText(p.title, `${pp}.title`, 1, 80, false);
        const detail = checkText(p.detail, `${pp}.detail`, 1, 300, true);
        const tag = checkText(p.tag, `${pp}.tag`, 0, 40, false);
        return { id, type, props: { title, detail, tag } };
      }
    }
  }

  function validateSection(raw: unknown, path: string): SiteSectionV2 | null {
    if (!isRecord(raw)) {
      addIssue(path, "INVALID_TYPE", "Mỗi section phải là một đối tượng JSON.");
      return null;
    }
    checkAllowedKeys(raw, path, ["id", "type", "visible", "variant", "settings", "props", "blocks"]);
    const id = checkText(raw.id, `${path}.id`, 1, 64, false);
    const type = checkEnum<SiteSectionV2Type>(
      raw.type,
      `${path}.type`,
      ALLOWED_V2_SECTION_TYPES,
      "text",
      true
    );
    const visible = checkBool(raw.visible, `${path}.visible`, true);

    const allowedVariants = SECTION_VARIANTS_BY_TYPE[type] ?? [""];
    const variant =
      raw.variant !== undefined
        ? checkEnum(raw.variant, `${path}.variant`, allowedVariants, "")
        : undefined;

    let settings: SectionSettings | undefined;
    if (raw.settings !== undefined) {
      if (!isRecord(raw.settings)) {
        addIssue(`${path}.settings`, "INVALID_TYPE", "section.settings phải là đối tượng JSON.");
      } else {
        const sp = `${path}.settings`;
        checkAllowedKeys(raw.settings, sp, ["surface", "padding", "columns", "align", "max_items"]);
        settings = {
          surface: checkEnum(raw.settings.surface, `${sp}.surface`, ALLOWED_SECTION_SURFACES, ""),
          padding: checkEnum(raw.settings.padding, `${sp}.padding`, ALLOWED_SECTION_PADDINGS, ""),
          columns: checkInt(raw.settings.columns, `${sp}.columns`, 0, 5, 0),
          align: checkEnum(raw.settings.align, `${sp}.align`, ["", "left", "center"] as const, ""),
          max_items: checkInt(raw.settings.max_items, `${sp}.max_items`, 0, 24, 0),
        };
      }
    }

    let blocks: SiteSectionBlock[] | undefined;
    if (raw.blocks !== undefined) {
      if (!Array.isArray(raw.blocks)) {
        addIssue(`${path}.blocks`, "INVALID_TYPE", "section.blocks phải là một mảng.");
      } else {
        if (raw.blocks.length > MAX_BLOCKS_PER_SECTION) {
          addIssue(
            `${path}.blocks`,
            "TOO_MANY_BLOCKS",
            `Mỗi section tối đa ${MAX_BLOCKS_PER_SECTION} block.`
          );
        }
        blocks = [];
        for (let bi = 0; bi < Math.min(raw.blocks.length, MAX_BLOCKS_PER_SECTION); bi++) {
          const blk = validateBlock(raw.blocks[bi], `${path}.blocks[${bi}]`);
          if (blk) blocks.push(blk);
        }
      }
    }

    if (!isRecord(raw.props)) {
      addIssue(`${path}.props`, "INVALID_TYPE", "section.props phải là một đối tượng JSON.");
      return null;
    }
    const p = raw.props;
    const pp = `${path}.props`;
    const base = {
      id,
      visible,
      ...(variant !== undefined ? { variant } : {}),
      ...(settings !== undefined ? { settings } : {}),
      ...(blocks !== undefined ? { blocks } : {}),
    };

    switch (type) {
      case "hero": {
        checkAllowedKeys(p, pp, ["eyebrow", "title", "subtitle", "image_url", "cta_label", "cta_href"]);
        const imgErr = validateImageUrlStrict(p.image_url, false);
        if (imgErr) addIssue(`${pp}.image_url`, "UNSAFE_IMAGE_URL", imgErr);
        const hrefErr = validateSafeHrefStrict(p.cta_href);
        if (hrefErr) addIssue(`${pp}.cta_href`, "UNSAFE_URL", hrefErr);
        return {
          ...base,
          type,
          props: {
            eyebrow: checkText(p.eyebrow, `${pp}.eyebrow`, 0, 60, false),
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 400, true),
            image_url: typeof p.image_url === "string" ? p.image_url.trim() : "",
            cta_label: checkText(p.cta_label, `${pp}.cta_label`, 0, 40, false),
            cta_href: typeof p.cta_href === "string" ? p.cta_href.trim() : "",
          },
        };
      }
      case "featured_products": {
        checkAllowedKeys(p, pp, ["title", "subtitle", "product_ids", "limit"]);
        const product_ids: string[] = [];
        if (p.product_ids !== undefined) {
          if (!Array.isArray(p.product_ids)) {
            addIssue(`${pp}.product_ids`, "INVALID_TYPE", "product_ids phải là mảng chuỗi.");
          } else {
            if (p.product_ids.length > 12) {
              addIssue(`${pp}.product_ids`, "TOO_MANY_ITEMS", "Chọn tối đa 12 sản phẩm.");
            }
            for (let i = 0; i < Math.min(p.product_ids.length, 12); i++) {
              product_ids.push(checkText(p.product_ids[i], `${pp}.product_ids[${i}]`, 1, 64, false));
            }
          }
        }
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
            product_ids,
            limit: checkInt(p.limit, `${pp}.limit`, 0, 12, 8),
          },
        };
      }
      case "collection": {
        checkAllowedKeys(p, pp, [
          "title",
          "subtitle",
          "collection_slug",
          "category_slug",
          "image_url",
          "limit",
        ]);
        const imgErr = validateImageUrlStrict(p.image_url, false);
        if (imgErr) addIssue(`${pp}.image_url`, "UNSAFE_IMAGE_URL", imgErr);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
            collection_slug: checkText(p.collection_slug, `${pp}.collection_slug`, 0, 80, false),
            category_slug: checkText(p.category_slug, `${pp}.category_slug`, 0, 80, false),
            image_url: typeof p.image_url === "string" ? p.image_url.trim() : "",
            limit: checkInt(p.limit, `${pp}.limit`, 0, 12, 4),
          },
        };
      }
      case "text": {
        checkAllowedKeys(p, pp, ["title", "body", "align"]);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            body: checkText(p.body, `${pp}.body`, 1, 2000, true),
            align: checkEnum(p.align, `${pp}.align`, ["", "left", "center"] as const, ""),
          },
        };
      }
      case "banner": {
        checkAllowedKeys(p, pp, ["title", "subtitle", "image_url", "cta_label", "cta_href"]);
        const imgErr = validateImageUrlStrict(p.image_url, false);
        if (imgErr) addIssue(`${pp}.image_url`, "UNSAFE_IMAGE_URL", imgErr);
        const hrefErr = validateSafeHrefStrict(p.cta_href);
        if (hrefErr) addIssue(`${pp}.cta_href`, "UNSAFE_URL", hrefErr);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
            image_url: typeof p.image_url === "string" ? p.image_url.trim() : "",
            cta_label: checkText(p.cta_label, `${pp}.cta_label`, 0, 40, false),
            cta_href: typeof p.cta_href === "string" ? p.cta_href.trim() : "",
          },
        };
      }
      case "voucher_strip": {
        checkAllowedKeys(p, pp, ["title", "subtitle", "limit"]);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
            limit: checkInt(p.limit, `${pp}.limit`, 0, 6, 4),
          },
        };
      }
      case "policies": {
        checkAllowedKeys(p, pp, ["title", "items"]);
        const items: Array<{ heading: string; detail: string }> = [];
        if (!Array.isArray(p.items)) {
          addIssue(`${pp}.items`, "INVALID_TYPE", "policies.items phải là mảng.");
        } else {
          if (p.items.length < 1 || p.items.length > 8) {
            addIssue(`${pp}.items`, "INVALID_LENGTH", "Cần từ 1 đến 8 mục chính sách.");
          }
          for (let i = 0; i < Math.min(p.items.length, 8); i++) {
            const it = p.items[i];
            const ip = `${pp}.items[${i}]`;
            if (!isRecord(it)) {
              addIssue(ip, "INVALID_TYPE", "Mỗi mục chính sách phải là đối tượng JSON.");
              continue;
            }
            checkAllowedKeys(it, ip, ["heading", "detail"]);
            items.push({
              heading: checkText(it.heading, `${ip}.heading`, 1, 80, false),
              detail: checkText(it.detail, `${ip}.detail`, 1, 400, true),
            });
          }
        }
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            items,
          },
        };
      }
      case "lookbook": {
        checkAllowedKeys(p, pp, ["title", "subtitle", "cta_label", "cta_href"]);
        const hrefErr = validateSafeHrefStrict(p.cta_href);
        if (hrefErr) addIssue(`${pp}.cta_href`, "UNSAFE_URL", hrefErr);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
            cta_label: checkText(p.cta_label, `${pp}.cta_label`, 0, 40, false),
            cta_href: typeof p.cta_href === "string" ? p.cta_href.trim() : "",
          },
        };
      }
      case "metrics_strip": {
        checkAllowedKeys(p, pp, ["title", "subtitle"]);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
          },
        };
      }
      case "sku_matrix": {
        checkAllowedKeys(p, pp, ["title", "subtitle", "category_slug", "limit", "show_out_of_stock"]);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
            category_slug: checkText(p.category_slug, `${pp}.category_slug`, 0, 80, false),
            limit: checkInt(p.limit, `${pp}.limit`, 0, 24, 12),
            show_out_of_stock: checkBool(p.show_out_of_stock, `${pp}.show_out_of_stock`, true),
          },
        };
      }
      case "testimonials": {
        checkAllowedKeys(p, pp, ["title", "subtitle"]);
        return {
          ...base,
          type,
          props: {
            title: checkText(p.title, `${pp}.title`, 1, 120, false),
            subtitle: checkText(p.subtitle, `${pp}.subtitle`, 0, 300, true),
          },
        };
      }
    }
  }

  function validateSectionList(raw: unknown, path: string, maxLen: number): SiteSectionV2[] {
    if (raw === undefined) return [];
    if (!Array.isArray(raw)) {
      addIssue(path, "INVALID_TYPE", "sections phải là một mảng.");
      return [];
    }
    if (raw.length > maxLen) {
      addIssue(path, "TOO_MANY_SECTIONS", `Tối đa ${maxLen} section được phép tại đây.`);
    }
    const out: SiteSectionV2[] = [];
    for (let i = 0; i < Math.min(raw.length, maxLen); i++) {
      const sec = validateSection(raw[i], `${path}[${i}]`);
      if (sec) out.push(sec);
    }
    return out;
  }

  if (!isRecord(rootVal)) {
    addIssue("$", "INVALID_TYPE", "Cấu hình template phải là đối tượng JSON.");
    return { ok: false, config: null, issues };
  }

  const sv = rootVal.schema_version;
  const isV1 = sv === undefined || sv === 1;
  if (!isV1 && sv !== SITE_SCHEMA_VERSION_V2) {
    addIssue("schema_version", "UNSUPPORTED_VERSION", "schema_version phải là 2 (hoặc bỏ trống cho v1).");
  }

  if (isV1) {
    checkAllowedKeys(rootVal, "$", ["schema_version", "theme", "sections"]);
  } else {
    checkAllowedKeys(rootVal, "$", [
      "schema_version",
      "template_id",
      "theme",
      "layout",
      "sections",
      "pages",
    ]);
  }

  let templateId = "legacy-v1";
  if (!isV1) {
    const rawTid = checkText(rootVal.template_id, "template_id", 1, 64, false);
    if (rawTid && !TEMPLATE_ID_RE.test(rawTid)) {
      addIssue("template_id", "INVALID_TEMPLATE_ID", "template_id chỉ gồm chữ thường, số và dấu gạch nối.");
    }
    templateId = rawTid || "custom";
  }

  // Validate theme
  let theme: SiteThemeV2 = { ...DEFAULT_SITE_THEME };
  if (!isRecord(rootVal.theme)) {
    addIssue("theme", "REQUIRED", "Khối theme là bắt buộc.");
  } else {
    const rt = rootVal.theme;
    const allowedThemeKeys = isV1
      ? [
          "bg_color",
          "surface_color",
          "text_color",
          "accent_color",
          "accent_text_color",
          "font_family",
          "logo_url",
          "banner_url",
          "brand_name",
          "tagline",
        ]
      : [
          "bg_color",
          "surface_color",
          "text_color",
          "accent_color",
          "accent_text_color",
          "font_family",
          "logo_url",
          "banner_url",
          "brand_name",
          "tagline",
          "radius",
          "density",
        ];
    checkAllowedKeys(rt, "theme", allowedThemeKeys);

    const checkColor = (key: string): string => {
      const v = typeof rt[key] === "string" ? (rt[key] as string).trim() : "";
      if (!isValidHexColor(v)) {
        addIssue(`theme.${key}`, "INVALID_COLOR", `theme.${key} phải có định dạng màu #RRGGBB.`);
        return "#000000";
      }
      return v.toUpperCase();
    };

    const bg_color = checkColor("bg_color");
    const surface_color = checkColor("surface_color");
    const text_color = checkColor("text_color");
    const accent_color = checkColor("accent_color");
    const accent_text_color = checkColor("accent_text_color");

    if (
      isValidHexColor(bg_color) &&
      isValidHexColor(surface_color) &&
      isValidHexColor(text_color) &&
      isValidHexColor(accent_color) &&
      isValidHexColor(accent_text_color)
    ) {
      const contrast = checkThemeContrast(
        { bg_color, surface_color, text_color, accent_color, accent_text_color },
        MIN_WCAG_CONTRAST
      );
      if (contrast.bgText < MIN_WCAG_CONTRAST) {
        addIssue(
          "theme.text_color",
          "LOW_CONTRAST",
          `Tương phản nền trang / chữ chính là ${contrast.bgText}:1, cần >= ${MIN_WCAG_CONTRAST}:1.`
        );
      }
      if (contrast.surfaceText < MIN_WCAG_CONTRAST) {
        addIssue(
          "theme.surface_color",
          "LOW_CONTRAST",
          `Tương phản bề mặt thẻ / chữ chính là ${contrast.surfaceText}:1, cần >= ${MIN_WCAG_CONTRAST}:1.`
        );
      }
      if (contrast.accentText < MIN_WCAG_CONTRAST) {
        addIssue(
          "theme.accent_color",
          "LOW_CONTRAST",
          `Tương phản màu nhấn / chữ nút là ${contrast.accentText}:1, cần >= ${MIN_WCAG_CONTRAST}:1.`
        );
      }
    }

    const font_family = checkEnum<SiteFontFamily>(
      rt.font_family,
      "theme.font_family",
      ALLOWED_SITE_FONTS,
      "work-sans",
      true
    );
    const logoErr = validateImageUrlStrict(rt.logo_url, false);
    if (logoErr) addIssue("theme.logo_url", "UNSAFE_IMAGE_URL", logoErr);
    const bannerErr = validateImageUrlStrict(rt.banner_url, false);
    if (bannerErr) addIssue("theme.banner_url", "UNSAFE_IMAGE_URL", bannerErr);

    theme = {
      bg_color,
      surface_color,
      text_color,
      accent_color,
      accent_text_color,
      font_family,
      logo_url: typeof rt.logo_url === "string" ? rt.logo_url.trim() : "",
      banner_url: typeof rt.banner_url === "string" ? rt.banner_url.trim() : "",
      brand_name: checkText(rt.brand_name, "theme.brand_name", 1, 80, false),
      tagline: checkText(rt.tagline, "theme.tagline", 0, 160, true),
      ...(isV1
        ? {}
        : {
            radius: checkEnum(rt.radius, "theme.radius", ALLOWED_RADII, ""),
            density: checkEnum(rt.density, "theme.density", ALLOWED_DENSITIES, ""),
          }),
    };
  }

  // Validate layout
  let layout: SiteLayoutConfig = createDefaultLayoutConfig();
  if (!isV1) {
    if (!isRecord(rootVal.layout)) {
      addIssue("layout", "REQUIRED", "Khối layout là bắt buộc ở schema v2.");
    } else {
      const rl = rootVal.layout;
      checkAllowedKeys(rl, "layout", [
        "archetype",
        "header_style",
        "footer_style",
        "container_width",
        "card_style",
        "show_announcement",
        "announcement_text",
        "slots",
      ]);
      let slots = { search: true, chat: true, assistant: true };
      if (rl.slots !== undefined) {
        if (!isRecord(rl.slots)) {
          addIssue("layout.slots", "INVALID_TYPE", "layout.slots phải là đối tượng JSON.");
        } else {
          checkAllowedKeys(rl.slots, "layout.slots", ["search", "chat", "assistant"]);
          slots = {
            search: checkBool(rl.slots.search, "layout.slots.search", true),
            chat: checkBool(rl.slots.chat, "layout.slots.chat", true),
            assistant: checkBool(rl.slots.assistant, "layout.slots.assistant", true),
          };
        }
      }
      layout = {
        archetype: checkEnum(rl.archetype, "layout.archetype", ALLOWED_ARCHETYPES, "classic", true),
        header_style: checkEnum(
          rl.header_style,
          "layout.header_style",
          ALLOWED_HEADER_STYLES,
          "minimal-split",
          true
        ),
        footer_style: checkEnum(
          rl.footer_style,
          "layout.footer_style",
          ALLOWED_FOOTER_STYLES,
          "minimal-row",
          true
        ),
        container_width: checkEnum(
          rl.container_width,
          "layout.container_width",
          ALLOWED_CONTAINER_WIDTHS,
          "standard",
          true
        ),
        card_style: checkEnum(
          rl.card_style,
          "layout.card_style",
          ALLOWED_CARD_STYLES,
          "minimal",
          true
        ),
        show_announcement: checkBool(rl.show_announcement, "layout.show_announcement", false),
        announcement_text: checkText(rl.announcement_text, "layout.announcement_text", 0, 140, false),
        slots,
      };
    }
  }

  // Validate home sections
  const sections = validateSectionList(rootVal.sections, "sections", MAX_HOME_SECTIONS);

  // Validate pages
  let pages: SitePagesConfig = createDefaultPagesConfig();
  if (!isV1) {
    if (!isRecord(rootVal.pages)) {
      addIssue("pages", "REQUIRED", "Khối pages (catalog, pdp, cart, info) là bắt buộc ở schema v2.");
    } else {
      const rp = rootVal.pages;
      checkAllowedKeys(rp, "pages", ["catalog", "pdp", "cart", "info"]);

      let catalog: CatalogPageConfig = pages.catalog;
      if (!isRecord(rp.catalog)) {
        addIssue("pages.catalog", "REQUIRED", "pages.catalog là bắt buộc.");
      } else {
        const c = rp.catalog;
        checkAllowedKeys(c, "pages.catalog", [
          "layout",
          "columns",
          "show_filters",
          "show_search",
          "show_sku_matrix",
          "intro_title",
          "intro_subtitle",
          "sections",
        ]);
        catalog = {
          layout: checkEnum(c.layout, "pages.catalog.layout", ALLOWED_CATALOG_LAYOUTS, "top-bar", true),
          columns: checkInt(c.columns, "pages.catalog.columns", 1, 5, 4),
          show_filters: checkBool(c.show_filters, "pages.catalog.show_filters", true),
          show_search: checkBool(c.show_search, "pages.catalog.show_search", true),
          show_sku_matrix: checkBool(c.show_sku_matrix, "pages.catalog.show_sku_matrix", false),
          intro_title: checkText(c.intro_title, "pages.catalog.intro_title", 0, 120, false),
          intro_subtitle: checkText(c.intro_subtitle, "pages.catalog.intro_subtitle", 0, 300, true),
          sections: validateSectionList(c.sections, "pages.catalog.sections", MAX_PAGE_SECTIONS),
        };
      }

      let pdp: PdpPageConfig = pages.pdp;
      if (!isRecord(rp.pdp)) {
        addIssue("pages.pdp", "REQUIRED", "pages.pdp là bắt buộc.");
      } else {
        const d = rp.pdp;
        checkAllowedKeys(d, "pages.pdp", [
          "layout",
          "gallery_style",
          "show_vouchers",
          "show_sku_table",
          "show_story_band",
          "show_related",
          "sections",
        ]);
        pdp = {
          layout: checkEnum(d.layout, "pages.pdp.layout", ALLOWED_PDP_LAYOUTS, "classic-split", true),
          gallery_style: checkEnum(
            d.gallery_style,
            "pages.pdp.gallery_style",
            ALLOWED_PDP_GALLERY_STYLES,
            "thumbnails",
            true
          ),
          show_vouchers: checkBool(d.show_vouchers, "pages.pdp.show_vouchers", true),
          show_sku_table: checkBool(d.show_sku_table, "pages.pdp.show_sku_table", false),
          show_story_band: checkBool(d.show_story_band, "pages.pdp.show_story_band", true),
          show_related: checkBool(d.show_related, "pages.pdp.show_related", true),
          sections: validateSectionList(d.sections, "pages.pdp.sections", MAX_PAGE_SECTIONS),
        };
      }

      let cart: CartPageConfig = pages.cart;
      if (!isRecord(rp.cart)) {
        addIssue("pages.cart", "REQUIRED", "pages.cart là bắt buộc.");
      } else {
        const ct = rp.cart;
        checkAllowedKeys(ct, "pages.cart", [
          "layout",
          "show_vouchers",
          "show_addons",
          "trust_note",
          "sections",
        ]);
        cart = {
          layout: checkEnum(ct.layout, "pages.cart.layout", ALLOWED_CART_LAYOUTS, "split-summary", true),
          show_vouchers: checkBool(ct.show_vouchers, "pages.cart.show_vouchers", true),
          show_addons: checkBool(ct.show_addons, "pages.cart.show_addons", true),
          trust_note: checkText(ct.trust_note, "pages.cart.trust_note", 0, 240, true),
          sections: validateSectionList(ct.sections, "pages.cart.sections", MAX_PAGE_SECTIONS),
        };
      }

      let info: InfoPageConfig = pages.info;
      if (!isRecord(rp.info)) {
        addIssue("pages.info", "REQUIRED", "pages.info là bắt buộc.");
      } else {
        const inf = rp.info;
        checkAllowedKeys(inf, "pages.info", [
          "layout",
          "show_factory_profile",
          "show_toc",
          "show_showcase",
          "sections",
        ]);
        info = {
          layout: checkEnum(inf.layout, "pages.info.layout", ALLOWED_INFO_LAYOUTS, "split-hero", true),
          show_factory_profile: checkBool(inf.show_factory_profile, "pages.info.show_factory_profile", true),
          show_toc: checkBool(inf.show_toc, "pages.info.show_toc", false),
          show_showcase: checkBool(inf.show_showcase, "pages.info.show_showcase", true),
          sections: validateSectionList(inf.sections, "pages.info.sections", MAX_PAGE_SECTIONS),
        };
      }

      pages = { catalog, pdp, cart, info };
    }
  }

  const totalSections =
    sections.length +
    (pages.catalog.sections?.length ?? 0) +
    (pages.pdp.sections?.length ?? 0) +
    (pages.cart.sections?.length ?? 0) +
    (pages.info.sections?.length ?? 0);
  if (totalSections > MAX_TOTAL_SECTIONS) {
    addIssue(
      "sections",
      "TOO_MANY_SECTIONS",
      `Tổng số section trên toàn site (${totalSections}) vượt quá giới hạn tối đa ${MAX_TOTAL_SECTIONS}.`
    );
  }

  if (issues.length > 0) {
    return { ok: false, config: null, issues };
  }

  return {
    ok: true,
    config: {
      schema_version: SITE_SCHEMA_VERSION_V2,
      template_id: templateId,
      theme,
      layout,
      sections,
      pages,
    },
    issues: [],
  };
}

/**
 * Lossless parser/normalizer for rendering both v1 legacy configs (e.g. the 4 seeded sites)
 * and v2 template configs without ever throwing.
 */
export function normalizeSiteTemplateConfig(
  raw: unknown,
  fallbackBrandName = ""
): SiteTemplateConfig | null {
  let obj: unknown = raw;
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) return null;
    try {
      obj = JSON.parse(trimmed);
    } catch {
      return null;
    }
  }
  if (!isRecord(obj)) return null;

  // Try strict v2 / v1 validation first so canonical valid configs preserve everything cleanly.
  const strict = validateSiteTemplateJson(JSON.stringify(obj));
  if (strict.ok && strict.config) {
    if (!strict.config.theme.brand_name && fallbackBrandName) {
      strict.config.theme.brand_name = fallbackBrandName;
    }
    return strict.config;
  }

  // Graceful fallback normalization for older persisted drafts with minor defaults missing.
  const rawTheme = isRecord(obj.theme) ? obj.theme : {};
  const fontRaw = typeof rawTheme.font_family === "string" ? rawTheme.font_family.trim().toLowerCase() : "";
  const font_family: SiteFontFamily = (ALLOWED_SITE_FONTS as readonly string[]).includes(fontRaw)
    ? (fontRaw as SiteFontFamily)
    : "work-sans";

  const theme: SiteThemeV2 = {
    bg_color: typeof rawTheme.bg_color === "string" && isValidHexColor(rawTheme.bg_color)
      ? rawTheme.bg_color.toUpperCase()
      : DEFAULT_SITE_THEME.bg_color,
    surface_color: typeof rawTheme.surface_color === "string" && isValidHexColor(rawTheme.surface_color)
      ? rawTheme.surface_color.toUpperCase()
      : DEFAULT_SITE_THEME.surface_color,
    text_color: typeof rawTheme.text_color === "string" && isValidHexColor(rawTheme.text_color)
      ? rawTheme.text_color.toUpperCase()
      : DEFAULT_SITE_THEME.text_color,
    accent_color: typeof rawTheme.accent_color === "string" && isValidHexColor(rawTheme.accent_color)
      ? rawTheme.accent_color.toUpperCase()
      : DEFAULT_SITE_THEME.accent_color,
    accent_text_color:
      typeof rawTheme.accent_text_color === "string" && isValidHexColor(rawTheme.accent_text_color)
        ? rawTheme.accent_text_color.toUpperCase()
        : DEFAULT_SITE_THEME.accent_text_color,
    font_family,
    logo_url: sanitizeSafeImageUrl(typeof rawTheme.logo_url === "string" ? rawTheme.logo_url : ""),
    banner_url: sanitizeSafeImageUrl(typeof rawTheme.banner_url === "string" ? rawTheme.banner_url : ""),
    brand_name:
      (typeof rawTheme.brand_name === "string" && rawTheme.brand_name.trim()) ||
      fallbackBrandName ||
      DEFAULT_SITE_THEME.brand_name,
    tagline: typeof rawTheme.tagline === "string" ? rawTheme.tagline.trim() : "",
    radius:
      typeof rawTheme.radius === "string" && (ALLOWED_RADII as readonly string[]).includes(rawTheme.radius)
        ? (rawTheme.radius as SiteThemeV2["radius"])
        : "",
    density:
      typeof rawTheme.density === "string" && (ALLOWED_DENSITIES as readonly string[]).includes(rawTheme.density)
        ? (rawTheme.density as SiteThemeV2["density"])
        : "",
  };

  const defaultLayout = createDefaultLayoutConfig();
  const rl = isRecord(obj.layout) ? obj.layout : {};
  const rSlots = isRecord(rl.slots) ? rl.slots : {};
  const layout: SiteLayoutConfig = {
    archetype:
      typeof rl.archetype === "string" && (ALLOWED_ARCHETYPES as readonly string[]).includes(rl.archetype)
        ? (rl.archetype as SiteLayoutConfig["archetype"])
        : defaultLayout.archetype,
    header_style:
      typeof rl.header_style === "string" &&
      (ALLOWED_HEADER_STYLES as readonly string[]).includes(rl.header_style)
        ? (rl.header_style as SiteLayoutConfig["header_style"])
        : defaultLayout.header_style,
    footer_style:
      typeof rl.footer_style === "string" &&
      (ALLOWED_FOOTER_STYLES as readonly string[]).includes(rl.footer_style)
        ? (rl.footer_style as SiteLayoutConfig["footer_style"])
        : defaultLayout.footer_style,
    container_width:
      typeof rl.container_width === "string" &&
      (ALLOWED_CONTAINER_WIDTHS as readonly string[]).includes(rl.container_width)
        ? (rl.container_width as SiteLayoutConfig["container_width"])
        : defaultLayout.container_width,
    card_style:
      typeof rl.card_style === "string" &&
      (ALLOWED_CARD_STYLES as readonly string[]).includes(rl.card_style)
        ? (rl.card_style as SiteLayoutConfig["card_style"])
        : defaultLayout.card_style,
    show_announcement: typeof rl.show_announcement === "boolean" ? rl.show_announcement : false,
    announcement_text: typeof rl.announcement_text === "string" ? rl.announcement_text.trim() : "",
    slots: {
      search: typeof rSlots.search === "boolean" ? rSlots.search : true,
      chat: typeof rSlots.chat === "boolean" ? rSlots.chat : true,
      assistant: typeof rSlots.assistant === "boolean" ? rSlots.assistant : true,
    },
  };

  const sections: SiteSectionV2[] = Array.isArray(obj.sections)
    ? obj.sections
        .slice(0, MAX_HOME_SECTIONS)
        .filter((s): s is SiteSectionV2 => isRecord(s) && typeof s.type === "string" && isRecord(s.props))
    : [];

  return {
    schema_version: SITE_SCHEMA_VERSION_V2,
    template_id:
      typeof obj.template_id === "string" && TEMPLATE_ID_RE.test(obj.template_id)
        ? obj.template_id
        : "legacy-v1",
    theme,
    layout,
    sections,
    pages: createDefaultPagesConfig(),
  };
}

/** Deterministic lossless converter from v1 SiteConfig to canonical v2 SiteTemplateConfig. */
export function convertV1ToV2(raw: string | unknown): TemplateValidationResult {
  const res = validateSiteTemplateJson(raw);
  if (!res.ok || !res.config) return res;
  return {
    ok: true,
    config: {
      ...res.config,
      schema_version: SITE_SCHEMA_VERSION_V2,
    },
    issues: [],
  };
}
