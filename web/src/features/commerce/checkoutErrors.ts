// Pure error & warning message mappers for cart, voucher, checkout, and payment BFF routes.
// Converts internal gateway details into clean Vietnamese buyer-facing messages so raw
// gateway internals never reach the browser.

export interface MappedClientError {
  code: string;
  message: string;
  status?: number;
}

const FLASH_SALE_ITEM_WARNING_RE = /\bflash\s+sale\s+item\s+([a-zA-Z0-9_-]+)\s+(.+)$/i;
const FLASH_SALE_REJECTION_REASON_RE =
  /(?:per-user|per\s+user|limit|sold\s+out|exhausted|not\s+found|not\s+approved|rejected|not\s+active|inactive|outside\s+active|ended|expired|suspended|does\s+not\s+match|does\s+not\s+belong|removed)/i;

/**
 * Extracts opaque `flash_sale_item_id` values rejected by `services/promotion` calculator warnings
 * (per-user limit, sold out, ended/not active, not found/unapproved, shop suspended, SKU/shop mismatch)
 * before translating warnings into Vietnamese. Ignores informational "takes precedence" warnings.
 */
export function extractRejectedFlashSaleItemIds(warnings?: readonly string[] | null): string[] {
  if (!Array.isArray(warnings) || warnings.length === 0) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of warnings) {
    if (typeof raw !== "string") continue;
    const match = FLASH_SALE_ITEM_WARNING_RE.exec(raw.trim());
    if (!match) continue;
    const id = match[1].trim();
    const reason = match[2];
    if (!id || /takes\s+precedence/i.test(reason)) continue;
    if (!FLASH_SALE_REJECTION_REASON_RE.test(reason)) continue;
    if (!seen.has(id)) {
      seen.add(id);
      out.push(id);
    }
  }
  return out;
}

/** Map a promotion calculator warning string (English) to a clean Vietnamese buyer message. */
export function translatePromotionWarning(raw: string): string {
  const s = raw.toLowerCase();
  // Match Flash sale warnings BEFORE generic voucher rules (not found / per-user / limit / shop mismatch)
  if (s.includes("flash sale")) {
    if (s.includes("shop_suspended") || s.includes("is suspended")) {
      return "Gian hàng hiện đang tạm ngưng hoạt động nên ưu đãi Flash sale không được áp dụng.";
    }
    if (s.includes("takes precedence")) {
      return "Ưu đãi Flash sale đang được ưu tiên áp dụng thay cho chương trình giảm giá gian hàng.";
    }
    if (s.includes("per-user") || s.includes("per user") || s.includes("limit")) {
      return "Sản phẩm đã vượt giới hạn suất Flash sale cho mỗi tài khoản.";
    }
    if (s.includes("sold out") || s.includes("exhausted")) {
      return "Suất ưu đãi Flash sale cho một số sản phẩm đã hết lượt.";
    }
    return "Ưu đãi khung giờ Flash sale cho một số sản phẩm không còn khả dụng.";
  }
  if (s.includes("not found")) return "Mã ưu đãi không tồn tại hoặc đã bị gỡ.";
  if (s.includes("inactive")) return "Mã ưu đãi hiện không còn hiệu lực.";
  if (s.includes("expired") || s.includes("ended") || s.includes("not within active")) {
    return "Mã ưu đãi đã hết hạn hoặc chưa đến thời gian áp dụng.";
  }
  if (s.includes("not started") || s.includes("starts")) {
    return "Mã ưu đãi chưa đến thời gian áp dụng.";
  }
  if (s.includes("quota") || s.includes("exhausted") || s.includes("budget")) {
    return "Mã ưu đãi đã hết lượt sử dụng.";
  }
  if (s.includes("per-user") || s.includes("per user") || s.includes("limit")) {
    return "Bạn đã dùng hết lượt áp dụng cho mã ưu đãi này.";
  }
  if (s.includes("minimum") || s.includes("min_order") || s.includes("min order")) {
    const match = raw.match(/(\d[\d,]*)\s*vnd/i);
    if (match) {
      const amount = Number(match[1].replace(/,/g, ""));
      if (Number.isFinite(amount) && amount > 0) {
        return `Đơn hàng cần tối thiểu ${amount.toLocaleString("vi-VN")}₫ để dùng mã này.`;
      }
    }
    return "Đơn hàng chưa đạt giá trị tối thiểu để áp dụng mã này.";
  }
  if (s.includes("does not belong to shop") || s.includes("not present in items")) {
    return "Mã gian hàng không áp dụng cho sản phẩm hiện có trong giỏ.";
  }
  if (s.includes("scope")) {
    return "Loại mã ưu đãi không khớp với vị trí đang chọn.";
  }
  if (s.includes("coin")) {
    return "Số dư Xu không đủ hoặc đã được điều chỉnh theo giới hạn đơn hàng.";
  }
  return "Một mã ưu đãi chưa đủ điều kiện áp dụng cho đơn hàng hiện tại.";
}

/** Map a voucher save/apply gateway error detail into a safe Vietnamese message. */
export function mapVoucherError(code: string, detail: string): MappedClientError {
  const d = detail.toLowerCase();
  if (code === "RESOURCE_EXHAUSTED" || d.includes("quota") || d.includes("budget")) {
    return {
      code: "VOUCHER_EXHAUSTED",
      status: 409,
      message: "Mã ưu đãi đã hết lượt sử dụng.",
    };
  }
  if (code === "NOT_FOUND" || d.includes("not found")) {
    return {
      code: "VOUCHER_NOT_FOUND",
      status: 404,
      message: "Không tìm thấy mã ưu đãi này. Bạn kiểm tra lại ký tự nhé.",
    };
  }
  if (d.includes("expired") || d.includes("active time window")) {
    return {
      code: "VOUCHER_EXPIRED",
      status: 409,
      message: "Mã ưu đãi đã hết hạn sử dụng.",
    };
  }
  if (d.includes("inactive")) {
    return {
      code: "VOUCHER_INACTIVE",
      status: 409,
      message: "Mã ưu đãi hiện không còn hiệu lực.",
    };
  }
  if (d.includes("per-user") || d.includes("limit")) {
    return {
      code: "VOUCHER_LIMIT_REACHED",
      status: 409,
      message: "Bạn đã dùng hết lượt áp dụng cho mã ưu đãi này.",
    };
  }
  if (d.includes("minimum")) {
    return {
      code: "VOUCHER_MIN_ORDER",
      status: 409,
      message: "Đơn hàng chưa đạt giá trị tối thiểu để áp dụng mã này.",
    };
  }
  return {
    code: code || "VOUCHER_INVALID",
    message: "Mã ưu đãi chưa đủ điều kiện áp dụng lúc này.",
  };
}

/** Map a cart mutation gateway error detail into a safe Vietnamese message. */
export function mapCartError(code: string, detail: string): MappedClientError {
  const d = detail.toLowerCase();
  if (d.includes("out_of_stock") || d.includes("0 available stock") || d.includes("insufficient")) {
    return {
      code: "OUT_OF_STOCK",
      status: 409,
      message: "Phân loại sản phẩm này hiện đã hết hàng.",
    };
  }
  if (d.includes("sku_not_active") || d.includes("not active")) {
    return {
      code: "SKU_INACTIVE",
      status: 409,
      message: "Sản phẩm này hiện tạm ngừng bán.",
    };
  }
  if (code === "NOT_FOUND") {
    return {
      code: "NOT_FOUND",
      status: 404,
      message: "Không tìm thấy sản phẩm trong kho.",
    };
  }
  return {
    code: code || "CART_ERROR",
    message: "Chưa thể cập nhật giỏ hàng lúc này. Bạn vui lòng thử lại.",
  };
}

/** Map a checkout gateway error detail into a safe Vietnamese message. */
export function mapCheckoutError(code: string, detail: string): MappedClientError {
  const d = detail.toLowerCase();
  if (code === "COD_PHONE_NOT_VERIFIED" || d.includes("cod_phone_not_verified")) {
    return {
      code: "COD_PHONE_NOT_VERIFIED",
      status: 409,
      message:
        "Vui lòng xác minh số điện thoại trong mục Tài khoản trước khi đặt hàng thanh toán khi nhận hàng (COD).",
    };
  }
  if (code === "COD_AREA_NOT_SUPPORTED" || d.includes("cod_area_not_supported")) {
    return {
      code: "COD_AREA_NOT_SUPPORTED",
      status: 409,
      message:
        "Khu vực nhận hàng này hiện chưa hỗ trợ thanh toán khi nhận hàng (COD). Vui lòng đổi địa chỉ hoặc chọn chuyển khoản VietQR.",
    };
  }
  if (
    code === "FLASH_SALE_COD_NOT_ALLOWED" ||
    d.includes("flash_sale_cod_not_allowed") ||
    (d.includes("cod") && d.includes("flash"))
  ) {
    return {
      code: "FLASH_SALE_COD_NOT_ALLOWED",
      status: 400,
      message:
        "Đơn hàng có sản phẩm Flash sale không hỗ trợ COD. Vui lòng chọn chuyển khoản VietQR hoặc Ví ZaloPay.",
    };
  }
  if (code === "ADDRESS_WARD_REQUIRED" || d.includes("address_ward_required")) {
    return {
      code: "ADDRESS_WARD_REQUIRED",
      status: 400,
      message: "Địa chỉ nhận hàng cần chọn đầy đủ Tỉnh/Thành phố và Phường/Xã cụ thể.",
    };
  }
  if (code === "SHOP_PICKUP_ADDRESS_REQUIRED" || d.includes("shop_pickup_address_required")) {
    return {
      code: "SHOP_PICKUP_ADDRESS_REQUIRED",
      status: 409,
      message:
        "Gian hàng chưa cấu hình đầy đủ địa chỉ lấy hàng hợp lệ nên chưa thể tính phí hoặc tạo đơn lúc này.",
    };
  }
  if (
    code === "SHOP_SUSPENDED" ||
    d.includes("shop_suspended") ||
    d.includes("shop is suspended")
  ) {
    return {
      code: "SHOP_SUSPENDED",
      status: 409,
      message: "Gian hàng hiện đang tạm ngưng hoạt động nên không thể tiếp tục đặt hàng.",
    };
  }
  if (code === "SHOP_NOT_ACTIVE" || d.includes("shop_not_active")) {
    return {
      code: "SHOP_NOT_ACTIVE",
      status: 409,
      message: "Gian hàng hiện không hoạt động nên không thể đặt hàng.",
    };
  }
  if (code === "PAYMENT_METHOD_DISABLED" || d.includes("payment_method_disabled")) {
    return {
      code: "PAYMENT_METHOD_DISABLED",
      status: 409,
      message: "Phương thức thanh toán bạn chọn hiện đang tạm tắt. Vui lòng chọn phương thức khác.",
    };
  }
  if (
    code === "ORDER_AMOUNT_OUT_OF_RANGE" ||
    d.includes("order_amount_out_of_range") ||
    d.includes("exceeds max_order_vnd") ||
    d.includes("below min_order_vnd")
  ) {
    return {
      code: "ORDER_AMOUNT_OUT_OF_RANGE",
      status: 409,
      message: "Giá trị đơn hàng nằm ngoài hạn mức cho phép của phương thức thanh toán đã chọn.",
    };
  }
  if (code === "CART_NO_AVAILABLE_ITEMS" || d.includes("cart_no_available_items")) {
    return {
      code: "CART_NO_AVAILABLE_ITEMS",
      status: 409,
      message: "Không còn sản phẩm khả dụng trong giỏ hàng để đặt đơn.",
    };
  }
  if (
    code === "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT" ||
    code === "SHOP_PROGRAM_PRICE_CHANGED" ||
    d.includes("shop_program_")
  ) {
    return {
      code: code || "SHOP_PROGRAM_PRICE_CHANGED",
      status: 409,
      message:
        "Chương trình giảm giá của gian hàng vừa thay đổi hoặc hết lượt. Vui lòng kiểm tra lại giá và thử đặt lại.",
    };
  }
  if (
    d.includes("insufficient_stock") ||
    d.includes("available, requested") ||
    d.includes("out_of_stock")
  ) {
    return {
      code: "OUT_OF_STOCK",
      status: 409,
      message:
        "Một số sản phẩm trong giỏ vừa hết hàng hoặc không đủ số lượng. Bạn vui lòng kiểm tra lại giỏ hàng.",
    };
  }
  if (d.includes("sku_inactive") || d.includes("not available for purchase")) {
    return {
      code: "SKU_INACTIVE",
      status: 409,
      message: "Có sản phẩm trong giỏ hiện đã ngừng bán. Bạn vui lòng gỡ ra trước khi đặt lại.",
    };
  }
  if (code === "COD_LIMIT_EXCEEDED" || d.includes("cod_limit_exceeded")) {
    return {
      code: "COD_LIMIT_EXCEEDED",
      status: 409,
      message:
        "Giá trị đơn hàng vượt hạn mức thanh toán khi nhận hàng (COD). Vui lòng chọn chuyển khoản VietQR.",
    };
  }
  if (code === "COD_REJECTED_LIMIT_EXCEEDED" || d.includes("cod_rejected_limit_exceeded")) {
    return {
      code: "COD_REJECTED_LIMIT_EXCEEDED",
      status: 409,
      message: "Tài khoản tạm hạn chế đặt COD. Vui lòng chọn chuyển khoản VietQR để tiếp tục.",
    };
  }
  if (d.includes("shopping cart is empty") || d.includes("at least one item")) {
    return {
      code: "EMPTY_CART",
      status: 409,
      message: "Giỏ hàng đang trống. Vui lòng chọn ít nhất một sản phẩm để đặt hàng.",
    };
  }
  if (d.includes("address")) {
    return {
      code: "INVALID_ADDRESS",
      status: 400,
      message: "Địa chỉ nhận hàng chưa hợp lệ hoặc không tồn tại. Vui lòng chọn lại địa chỉ.",
    };
  }
  if (
    code.startsWith("FLASH_") ||
    d.includes("flash_sold_out") ||
    d.includes("flash_sale_sold_out") ||
    d.includes("flash sale")
  ) {
    return {
      code: code.startsWith("FLASH_") ? code : "FLASH_SALE_EXHAUSTED",
      status: 409,
      message: "Suất ưu đãi Flash sale đã hết hoặc vượt giới hạn mỗi người mua.",
    };
  }
  if (d.includes("voucher") || d.includes("quota") || d.includes("budget")) {
    return mapVoucherError(code, detail);
  }
  if (code === "RATE_LIMITED" || code === "RATE_LIMIT_EXCEEDED" || code === "RESOURCE_EXHAUSTED") {
    return {
      code,
      status: 429,
      message: "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút.",
    };
  }
  if (d.includes("coin")) {
    return {
      code: "INSUFFICIENT_COINS",
      status: 409,
      message: "Số dư Xu không đủ để áp dụng cho đơn hàng này.",
    };
  }
  if (d.includes("concurrent checkout")) {
    return {
      code: "CHECKOUT_IN_PROGRESS",
      status: 409,
      message:
        "Đơn hàng của bạn đang được xử lý. Vui lòng đợi vài giây rồi kiểm tra danh sách đơn.",
    };
  }
  if (d.includes("buyer account is not active")) {
    return {
      code: "ACCOUNT_INACTIVE",
      status: 403,
      message: "Tài khoản chưa sẵn sàng đặt hàng. Vui lòng xác thực tài khoản hoặc liên hệ hỗ trợ.",
    };
  }
  if (d.includes("storefront_")) {
    return {
      code: "INVALID_SALES_CHANNEL",
      status: 400,
      message: "Ưu đãi hoặc sản phẩm chưa hợp lệ cho kênh đặt hàng hiện tại.",
    };
  }
  return {
    code: code || "CHECKOUT_FAILED",
    message: "Không thể hoàn tất đặt hàng lúc này. Bạn vui lòng kiểm tra lại giỏ hàng và thử lại.",
  };
}

export type CheckoutRouteErrorDecision =
  | { action: "rethrow" }
  | {
      action: "respond";
      status: number;
      code: string;
      message: string;
      retryAfter?: string;
    };

/**
 * Pure decision helper for POST /api/checkout BFF error handling:
 * - Preserves HTTP 429 (and Retry-After via apiRoute) for rate-limit responses instead of
 *   misclassifying them as 409 VOUCHER_EXHAUSTED.
 * - Keeps 429 with flash-specific Vietnamese copy when PromotionService Lua returns FLASH_SOLD_OUT.
 * - Maps conflict/invalid/not_found errors via `mapCheckoutError`.
 */
export function resolveCheckoutRouteError(err: {
  kind: string;
  status: number;
  code: string;
  detail: string;
  retryAfter?: string;
}): CheckoutRouteErrorDecision {
  if (err.kind === "rate_limited") {
    if (/flash/i.test(err.detail)) {
      return {
        action: "respond",
        status: 429,
        code: "RESOURCE_EXHAUSTED",
        message:
          "Suất ưu đãi Flash sale đã hết lượt. Vui lòng bỏ suất Flash hoặc chọn sản phẩm khác.",
        retryAfter: err.retryAfter,
      };
    }
    const normCode = (err.code ?? "").trim().toUpperCase();
    if (
      normCode !== "RATE_LIMITED" &&
      normCode !== "RATE_LIMIT_EXCEEDED" &&
      /voucher|quota|budget/i.test(err.detail)
    ) {
      const mapped = mapVoucherError(err.code, err.detail);
      return {
        action: "respond",
        status: mapped.status ?? 409,
        code: mapped.code,
        message: mapped.message,
      };
    }
    return { action: "rethrow" };
  }
  if (err.kind === "conflict" || err.kind === "invalid" || err.kind === "not_found") {
    const mapped = mapCheckoutError(err.code, err.detail);
    return {
      action: "respond",
      status: mapped.status ?? err.status,
      code: mapped.code,
      message: mapped.message,
    };
  }
  return { action: "rethrow" };
}
