export const MAX_REVIEW_IMAGES = 5;
export const MAX_REVIEW_IMAGE_URL_LEN = 500;
export const MAX_REVIEW_COMMENT_LEN = 2000;

const CONTROL_OR_WHITESPACE_RE = /[\u0000-\u001f\u007f]|\s/u;

/**
 * Validates a single review image URL per E7:
 * strictly `https://...` or internal `/products/...` (no `http:`, `javascript:`, `data:`,
 * protocol-relative `//`, userinfo `user:pass@`, Unicode whitespace, or path traversal `..`),
 * length <= 500 characters.
 */
export function parseSafeReviewImageUrl(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > MAX_REVIEW_IMAGE_URL_LEN) {
    return null;
  }
  if (CONTROL_OR_WHITESPACE_RE.test(trimmed)) {
    return null;
  }

  if (trimmed.startsWith("/")) {
    if (
      !trimmed.startsWith("/products/") ||
      trimmed.startsWith("//") ||
      trimmed.includes("..") ||
      trimmed.includes("\\")
    ) {
      return null;
    }
    if (trimmed.length <= "/products/".length) {
      return null;
    }
    return trimmed;
  }

  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:") {
      return null;
    }
    if (!parsed.hostname || parsed.username || parsed.password) {
      return null;
    }
    return parsed.toString();
  } catch {
    return null;
  }
}

export function validateReviewImageUrls(rawUrls: readonly string[]): {
  urls: string[];
  error: string | null;
} {
  const nonEmpty = rawUrls.map((u) => u.trim()).filter(Boolean);
  if (nonEmpty.length > MAX_REVIEW_IMAGES) {
    return {
      urls: [],
      error: `Tối đa ${MAX_REVIEW_IMAGES} liên kết ảnh cho mỗi đánh giá.`,
    };
  }

  const urls: string[] = [];
  for (const entry of nonEmpty) {
    const safe = parseSafeReviewImageUrl(entry);
    if (!safe) {
      return {
        urls: [],
        error: `Liên kết ảnh không hợp lệ: chỉ chấp nhận đường dẫn https:// hoặc ảnh nội bộ /products/... (tối đa ${MAX_REVIEW_IMAGE_URL_LEN} ký tự).`,
      };
    }
    urls.push(safe);
  }

  return { urls, error: null };
}

/**
 * Only COMPLETED orders are eligible for buyer product reviews (A4 / E5).
 */
export function canReviewOrderStatus(status: string | undefined): boolean {
  return (status ?? "").trim().toUpperCase() === "COMPLETED";
}

export function isOrderItemReviewed(
  orderItemId: string,
  reviews: ReadonlyArray<{ order_item_id?: string }>
): boolean {
  return reviews.some((r) => r.order_item_id === orderItemId);
}
