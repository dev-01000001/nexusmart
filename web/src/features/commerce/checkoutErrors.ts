// Pure error & warning message mappers for cart, voucher, checkout, and payment BFF routes.
// Converts internal gateway details into clean Vietnamese buyer-facing messages so raw
// gateway internals never reach the browser.

export interface MappedClientError {
  code: string;
  message: string;
  status?: number;
}

/** Map a promotion calculator warning string (English) to a clean Vietnamese buyer message. */
export function translatePromotionWarning(raw: string): string {
  const s = raw.toLowerCase();
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
  if (s.includes("flash sale")) {
    return "Ưu đãi khung giờ Flash sale cho một số sản phẩm không còn khả dụng.";
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
  if (d.includes("cod_limit_exceeded")) {
    return {
      code: "COD_LIMIT_EXCEEDED",
      status: 409,
      message:
        "Giá trị đơn hàng vượt hạn mức thanh toán khi nhận hàng (COD). Vui lòng chọn chuyển khoản VietQR.",
    };
  }
  if (d.includes("cod_rejected_limit_exceeded")) {
    return {
      code: "COD_REJECTED_LIMIT",
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
    d.includes("voucher") ||
    code === "RESOURCE_EXHAUSTED" ||
    d.includes("quota") ||
    d.includes("budget")
  ) {
    return mapVoucherError(code, detail);
  }
  if (d.includes("coin")) {
    return {
      code: "INSUFFICIENT_COINS",
      status: 409,
      message: "Số dư Xu không đủ để áp dụng cho đơn hàng này.",
    };
  }
  if (d.includes("flash sale")) {
    return {
      code: "FLASH_SALE_EXHAUSTED",
      status: 409,
      message: "Suất ưu đãi Flash sale đã hết hoặc vượt giới hạn mỗi người mua.",
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
  return {
    code: code || "CHECKOUT_FAILED",
    message: "Không thể hoàn tất đặt hàng lúc này. Bạn vui lòng kiểm tra lại giỏ hàng và thử lại.",
  };
}
