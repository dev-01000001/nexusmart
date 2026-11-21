// Pure buyer return request types, payload validation, photo URL sanitization, and status mapping.
// Zero Next.js or DOM imports so Node 22 runs unit tests directly.

export type ReturnStatus =
  | "PENDING"
  | "APPROVED"
  | "REJECTED"
  | "RETURNING"
  | "RETURNED"
  | "REFUNDED"
  | "ESCALATED";

export type ReturnStatusTone = "neutral" | "warn" | "pos" | "neg";

export interface ReturnStatusMeta {
  label: string;
  tone: ReturnStatusTone;
  description: string;
}

export const RETURN_STATUS_META: Record<ReturnStatus, ReturnStatusMeta> = {
  PENDING: {
    label: "Chờ gian hàng duyệt",
    tone: "warn",
    description: "Gian hàng đang xem xét thông tin và hình ảnh đổi trả bạn đã gửi.",
  },
  APPROVED: {
    label: "Đã duyệt đổi trả",
    tone: "pos",
    description: "Gian hàng đã đồng ý tiếp nhận yêu cầu đổi trả của bạn.",
  },
  REJECTED: {
    label: "Gian hàng từ chối",
    tone: "neg",
    description: "Gian hàng chưa chấp nhận yêu cầu đổi trả này.",
  },
  RETURNING: {
    label: "Đang gửi hoàn hàng",
    tone: "neutral",
    description: "Kiện hàng hoàn đang được vận chuyển về kho của gian hàng.",
  },
  RETURNED: {
    label: "Đã nhận hàng hoàn",
    tone: "pos",
    description: "Gian hàng đã nhận lại kiện hàng và đang xử lý hoàn tiền.",
  },
  REFUNDED: {
    label: "Đã hoàn tiền",
    tone: "pos",
    description: "Số tiền hoàn đã được xử lý về phương thức thanh toán của bạn.",
  },
  ESCALATED: {
    label: "Đang xem xét khiếu nại",
    tone: "warn",
    description: "Bộ phận hỗ trợ đang kiểm tra yêu cầu đổi trả và phản hồi sớm.",
  },
};

export interface ReturnRequestItem {
  order_item_id?: string;
  sku_id?: string;
  quantity: number;
}

export interface ReturnRequestView {
  id: string;
  orderId: string;
  shopId: string;
  status: ReturnStatus;
  items: ReturnRequestItem[];
  reason: string;
  photos: string[];
  sellerNote: string;
  reverseTrackingCode: string;
  reverseLabelUrl: string;
  refundAmountVnd: number;
  refundId: string;
  createdAt: string;
  updatedAt: string;
}

export interface ValidatedReturnPayload {
  items: ReturnRequestItem[];
  reason: string;
  photos: string[];
}

export type ReturnValidationResult =
  | { ok: true; data: ValidatedReturnPayload }
  | { ok: false; code: string; message: string };

export const MIN_RETURN_REASON_LEN = 5;
export const MAX_RETURN_REASON_LEN = 500;
export const MAX_RETURN_PHOTOS = 5;
export const MAX_RETURN_ITEM_QTY = 99;
export const MAX_RETURN_ITEMS = 50;

const CONTROL_CHAR_RE = /[\u0000-\u001F\u007F]/;
const MAX_URL_LENGTH = 500;

const VALID_RETURN_STATUSES = new Set<ReturnStatus>([
  "PENDING",
  "APPROVED",
  "REJECTED",
  "RETURNING",
  "RETURNED",
  "REFUNDED",
  "ESCALATED",
]);

function asRecord(val: unknown): Record<string, unknown> | null {
  return val && typeof val === "object" && !Array.isArray(val)
    ? (val as Record<string, unknown>)
    : null;
}

/**
 * Allows ONLY safe relative paths starting with a single `/` (not `//`) or valid `https://` URLs.
 * Rejects `javascript:`, `data:`, `vbscript:`, `http://`, protocol-relative `//`, backslashes,
 * whitespace in relative paths, and control characters.
 */
export function sanitizeReturnPhotoUrl(raw: unknown): string {
  if (typeof raw !== "string") return "";
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > MAX_URL_LENGTH) return "";
  if (CONTROL_CHAR_RE.test(trimmed) || trimmed.includes("\\")) return "";

  if (trimmed.startsWith("/")) {
    if (trimmed.startsWith("//") || /\s/.test(trimmed)) return "";
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

export function isSafeReturnPhotoUrl(raw: unknown): boolean {
  return sanitizeReturnPhotoUrl(raw).length > 0;
}

export function normalizeReturnStatus(raw: unknown): ReturnStatus {
  if (typeof raw === "string") {
    const cleaned = raw
      .trim()
      .toUpperCase()
      .replace(/^RETURN_REQUEST_STATUS_/, "");
    if (VALID_RETURN_STATUSES.has(cleaned as ReturnStatus)) {
      return cleaned as ReturnStatus;
    }
  }
  return "PENDING";
}

export function validateReturnPayload(raw: unknown): ReturnValidationResult {
  const body = asRecord(raw);
  if (!body) {
    return {
      ok: false,
      code: "INVALID_PAYLOAD",
      message: "Dữ liệu yêu cầu đổi trả không hợp lệ.",
    };
  }

  const reason = typeof body.reason === "string" ? body.reason.trim() : "";
  if (reason.length < MIN_RETURN_REASON_LEN || reason.length > MAX_RETURN_REASON_LEN) {
    return {
      ok: false,
      code: "INVALID_REASON",
      message: `Vui lòng nhập lý do đổi trả từ ${MIN_RETURN_REASON_LEN} đến ${MAX_RETURN_REASON_LEN} ký tự.`,
    };
  }

  if (!Array.isArray(body.items) || body.items.length === 0) {
    return {
      ok: false,
      code: "INVALID_RETURN_ITEMS",
      message: "Vui lòng chọn ít nhất một sản phẩm cần đổi trả.",
    };
  }
  if (body.items.length > MAX_RETURN_ITEMS) {
    return {
      ok: false,
      code: "INVALID_RETURN_ITEMS",
      message: "Số lượng dòng sản phẩm đổi trả vượt quá giới hạn cho phép.",
    };
  }

  const items: ReturnRequestItem[] = [];
  for (const entry of body.items) {
    const rec = asRecord(entry);
    if (!rec) {
      return {
        ok: false,
        code: "INVALID_RETURN_ITEMS",
        message: "Thông tin sản phẩm đổi trả chưa hợp lệ.",
      };
    }
    const orderItemId =
      typeof rec.order_item_id === "string" ? rec.order_item_id.trim().slice(0, 128) : "";
    const skuId = typeof rec.sku_id === "string" ? rec.sku_id.trim().slice(0, 128) : "";
    if (!orderItemId && !skuId) {
      return {
        ok: false,
        code: "INVALID_RETURN_ITEMS",
        message: "Thiếu mã sản phẩm cần đổi trả.",
      };
    }
    const qty = rec.quantity;
    if (
      typeof qty !== "number" ||
      !Number.isInteger(qty) ||
      qty < 1 ||
      qty > MAX_RETURN_ITEM_QTY
    ) {
      return {
        ok: false,
        code: "INVALID_RETURN_QTY",
        message: `Số lượng đổi trả cho mỗi sản phẩm phải là số nguyên từ 1 đến ${MAX_RETURN_ITEM_QTY}.`,
      };
    }
    items.push({
      ...(orderItemId ? { order_item_id: orderItemId } : {}),
      ...(skuId ? { sku_id: skuId } : {}),
      quantity: qty,
    });
  }

  const photos: string[] = [];
  if (body.photos !== undefined && body.photos !== null) {
    if (!Array.isArray(body.photos)) {
      return {
        ok: false,
        code: "INVALID_PHOTO_URL",
        message: "Danh sách ảnh minh chứng chưa đúng định dạng.",
      };
    }
    if (body.photos.length > MAX_RETURN_PHOTOS) {
      return {
        ok: false,
        code: "TOO_MANY_PHOTOS",
        message: `Chỉ được đính kèm tối đa ${MAX_RETURN_PHOTOS} ảnh minh chứng.`,
      };
    }
    const seenPhotos = new Set<string>();
    for (const rawPhoto of body.photos) {
      if (typeof rawPhoto !== "string") {
        return {
          ok: false,
          code: "INVALID_PHOTO_URL",
          message: "Đường dẫn ảnh minh chứng không hợp lệ.",
        };
      }
      if (!rawPhoto.trim()) continue;
      const safe = sanitizeReturnPhotoUrl(rawPhoto);
      if (!safe) {
        return {
          ok: false,
          code: "INVALID_PHOTO_URL",
          message:
            "Đường dẫn ảnh minh chứng không hợp lệ. Chỉ chấp nhận ảnh nội bộ (/...) hoặc liên kết https://.",
        };
      }
      if (!seenPhotos.has(safe)) {
        seenPhotos.add(safe);
        photos.push(safe);
      }
    }
  }

  return {
    ok: true,
    data: { items, reason, photos },
  };
}

function normalizeReturnItem(raw: unknown): ReturnRequestItem | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const orderItemId =
    typeof rec.order_item_id === "string" ? rec.order_item_id.trim() : "";
  const skuId = typeof rec.sku_id === "string" ? rec.sku_id.trim() : "";
  const qty = Math.trunc(Number(rec.quantity ?? 0) || 0);
  if ((!orderItemId && !skuId) || qty < 1) return null;
  return {
    ...(orderItemId ? { order_item_id: orderItemId } : {}),
    ...(skuId ? { sku_id: skuId } : {}),
    quantity: Math.min(MAX_RETURN_ITEM_QTY, qty),
  };
}

export function normalizeReturnRequest(raw: unknown): ReturnRequestView | null {
  const rec = asRecord(raw);
  if (!rec) return null;
  const id = typeof rec.id === "string" ? rec.id.trim() : "";
  if (!id) return null;

  const rawItems = Array.isArray(rec.items) ? rec.items : [];
  const items = rawItems
    .map(normalizeReturnItem)
    .filter((it): it is ReturnRequestItem => it !== null);

  const rawPhotos = Array.isArray(rec.photos)
    ? rec.photos
    : Array.isArray(rec.evidence_urls)
      ? rec.evidence_urls
      : [];
  const photos = Array.from(
    new Set(rawPhotos.map(sanitizeReturnPhotoUrl).filter(Boolean))
  ).slice(0, MAX_RETURN_PHOTOS);

  const rawRefund = Number(rec.refund_amount_vnd ?? rec.refundAmountVnd ?? 0);
  const refundAmountVnd =
    Number.isFinite(rawRefund) && rawRefund > 0 ? Math.trunc(rawRefund) : 0;

  return {
    id,
    orderId: typeof rec.order_id === "string" ? rec.order_id.trim() : "",
    shopId: typeof rec.shop_id === "string" ? rec.shop_id.trim() : "",
    status: normalizeReturnStatus(rec.status),
    items,
    reason: typeof rec.reason === "string" ? rec.reason.trim() : "",
    photos,
    sellerNote:
      typeof rec.seller_note === "string"
        ? rec.seller_note.trim()
        : typeof rec.sellerNote === "string"
          ? rec.sellerNote.trim()
          : "",
    reverseTrackingCode:
      typeof rec.reverse_tracking_code === "string"
        ? rec.reverse_tracking_code.trim()
        : typeof rec.reverseTrackingCode === "string"
          ? rec.reverseTrackingCode.trim()
          : "",
    reverseLabelUrl: sanitizeReturnPhotoUrl(
      rec.reverse_label_url ?? rec.reverseLabelUrl ?? ""
    ),
    refundAmountVnd,
    refundId:
      typeof rec.refund_id === "string"
        ? rec.refund_id.trim()
        : typeof rec.refundId === "string"
          ? rec.refundId.trim()
          : "",
    createdAt:
      typeof rec.created_at === "string"
        ? rec.created_at
        : typeof rec.createdAt === "string"
          ? rec.createdAt
          : "",
    updatedAt:
      typeof rec.updated_at === "string"
        ? rec.updated_at
        : typeof rec.updatedAt === "string"
          ? rec.updatedAt
          : "",
  };
}

export function normalizeReturnRequests(raw: unknown): ReturnRequestView[] {
  const rec = asRecord(raw);
  const list = Array.isArray(raw)
    ? raw
    : Array.isArray(rec?.returns)
      ? rec.returns
      : Array.isArray(rec?.items)
        ? rec.items
        : rec?.return_request
          ? [rec.return_request]
          : [];

  return list
    .map(normalizeReturnRequest)
    .filter((entry): entry is ReturnRequestView => entry !== null)
    .sort((a, b) => {
      const ta = a.createdAt ? Date.parse(a.createdAt) || 0 : 0;
      const tb = b.createdAt ? Date.parse(b.createdAt) || 0 : 0;
      return tb - ta;
    });
}
