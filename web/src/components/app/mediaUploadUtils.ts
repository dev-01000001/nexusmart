// Pure client/server-shared media upload validation, URL construction, and list helpers.
// Zero React/Next/DOM dependencies so it runs cleanly in both browsers and `node --test`.

export const MEDIA_MAX_BYTES = 5 * 1024 * 1024; // 5 MiB (matches gateway mediaMaxUploadBytes)
export const MEDIA_MAX_PRODUCT_IMAGES = 9;

export const MEDIA_ALLOWED_MIME_TYPES = ["image/jpeg", "image/png", "image/webp"] as const;
export type AllowedMediaMime = (typeof MEDIA_ALLOWED_MIME_TYPES)[number];

export const MEDIA_ACCEPT_ATTR = "image/jpeg,image/png,image/webp";

const MEDIA_ALLOWED_MIME_SET: ReadonlySet<string> = new Set(MEDIA_ALLOWED_MIME_TYPES);

/** Matches backend gateway regex `^shops/[0-9a-f-]{36}/[0-9a-f]{64}\.(jpg|png|webp)$` with canonical UUID hyphens. */
export const MEDIA_KEY_REGEX =
  /^shops\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\/[0-9a-f]{64}\.(jpg|png|webp)$/;

export function normalizeMediaContentType(raw: string | null | undefined): AllowedMediaMime | null {
  if (!raw) return null;
  const mediaType = raw.split(";")[0]?.trim().toLowerCase() ?? "";
  if (MEDIA_ALLOWED_MIME_SET.has(mediaType)) {
    return mediaType as AllowedMediaMime;
  }
  return null;
}

function inferMimeFromFilename(name: string | undefined): AllowedMediaMime | null {
  if (!name) return null;
  const lower = name.trim().toLowerCase();
  if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) return "image/jpeg";
  if (lower.endsWith(".png")) return "image/png";
  if (lower.endsWith(".webp")) return "image/webp";
  return null;
}

export type ClientMediaValidationResult =
  | { ok: true; contentType: AllowedMediaMime }
  | {
      ok: false;
      code: "EMPTY" | "TOO_LARGE" | "UNSUPPORTED_TYPE";
      message: string;
    };

export function validateMediaFileClient(file: {
  name?: string;
  type?: string;
  size?: number;
}): ClientMediaValidationResult {
  const size = typeof file.size === "number" && Number.isFinite(file.size) ? file.size : 0;
  if (size <= 0) {
    return {
      ok: false,
      code: "EMPTY",
      message: "Tệp ảnh đang trống, vui lòng chọn tệp khác.",
    };
  }
  if (size > MEDIA_MAX_BYTES) {
    return {
      ok: false,
      code: "TOO_LARGE",
      message: "Ảnh vượt quá giới hạn 5 MB. Vui lòng chọn ảnh có dung lượng nhỏ hơn.",
    };
  }

  const rawType = (file.type ?? "").trim();
  const contentType = rawType
    ? normalizeMediaContentType(rawType)
    : inferMimeFromFilename(file.name);
  if (!contentType) {
    return {
      ok: false,
      code: "UNSUPPORTED_TYPE",
      message: "Chỉ hỗ trợ định dạng ảnh JPEG, PNG hoặc WebP (không hỗ trợ SVG, GIF).",
    };
  }

  return { ok: true, contentType };
}

export function isValidMediaKey(key: string | null | undefined): boolean {
  if (!key) return false;
  return MEDIA_KEY_REGEX.test(key);
}

export function buildMediaPublicUrl(key: string | null | undefined): string | null {
  if (!key || !isValidMediaKey(key)) return null;
  return `/media/${key}`;
}

/**
 * Validates Next.js catch-all `[...key]` route segments and returns the canonical
 * gateway path `/media/shops/<uuid>/<sha256>.<ext>` (or null if outside the strict pattern).
 */
export function buildGatewayMediaPath(segments: readonly string[] | undefined): string | null {
  if (!segments || segments.length !== 3) return null;
  for (const seg of segments) {
    if (!seg || seg.includes("/") || seg.includes("\\") || seg.includes("..")) {
      return null;
    }
  }
  const joined = segments.join("/");
  return buildMediaPublicUrl(joined);
}

export type ContentLengthValidation =
  { ok: true; length: number | null } | { ok: false; status: 400 | 413 };

export function validateContentLengthHeader(
  rawHeader: string | null | undefined,
  maxBytes: number = MEDIA_MAX_BYTES
): ContentLengthValidation {
  if (rawHeader === null || rawHeader === undefined || rawHeader.trim() === "") {
    return { ok: true, length: null };
  }
  const trimmed = rawHeader.trim();
  if (!/^\d+$/.test(trimmed)) {
    return { ok: false, status: 400 };
  }
  const parsed = Number(trimmed);
  if (!Number.isSafeInteger(parsed) || parsed < 0) {
    return { ok: false, status: 400 };
  }
  if (parsed > maxBytes) {
    return { ok: false, status: 413 };
  }
  return { ok: true, length: parsed };
}

export function appendUniqueMediaUrl(
  urls: readonly string[],
  url: string,
  maxCount: number = MEDIA_MAX_PRODUCT_IMAGES
): string[] {
  const clean = url.trim();
  if (!clean) return [...urls];
  if (urls.includes(clean)) return [...urls];
  if (urls.length >= maxCount) return [...urls];
  return [...urls, clean];
}

export function setPrimaryMediaUrl(urls: readonly string[], targetUrl: string): string[] {
  const idx = urls.indexOf(targetUrl);
  if (idx <= 0) return [...urls];
  const next = [...urls];
  const [picked] = next.splice(idx, 1);
  next.unshift(picked);
  return next;
}

export function reorderMediaUrls(
  urls: readonly string[],
  fromIndex: number,
  toIndex: number
): string[] {
  if (
    fromIndex === toIndex ||
    fromIndex < 0 ||
    toIndex < 0 ||
    fromIndex >= urls.length ||
    toIndex >= urls.length
  ) {
    return [...urls];
  }
  const next = [...urls];
  const [moved] = next.splice(fromIndex, 1);
  next.splice(toIndex, 0, moved);
  return next;
}

export function formatMediaBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${Math.trunc(bytes)} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1).replace(".", ",")} MB`;
}

export function mapMediaUploadError(status: number, code?: string): string {
  const cleanCode = (code ?? "").trim().toUpperCase();
  if (status === 413 || cleanCode === "PAYLOAD_TOO_LARGE") {
    return "Ảnh vượt quá giới hạn 5 MB. Vui lòng chọn ảnh có dung lượng nhỏ hơn.";
  }
  if (status === 415 || cleanCode === "UNSUPPORTED_MEDIA_TYPE") {
    return "Định dạng tệp không hợp lệ. Chỉ hỗ trợ ảnh JPEG, PNG hoặc WebP.";
  }
  if (status === 400 || cleanCode === "INVALID_ARGUMENT") {
    return "Ảnh bị hỏng hoặc vượt quá kích thước tối đa 6000×6000 px.";
  }
  if (status === 401 || cleanCode === "UNAUTHENTICATED") {
    return "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại trước khi tải ảnh.";
  }
  if (status === 403 || cleanCode === "PERMISSION_DENIED" || cleanCode === "BAD_ORIGIN") {
    return "Bạn không có quyền tải ảnh lên cho gian hàng này.";
  }
  if (status === 429 || cleanCode === "RATE_LIMITED" || cleanCode === "RESOURCE_EXHAUSTED") {
    return "Bạn tải ảnh hơi nhanh. Vui lòng đợi ít giây rồi thử lại.";
  }
  return "Không thể tải ảnh lên lúc này. Vui lòng thử lại sau.";
}
