import { ApiError } from "@/lib/api";
import { formatVnd, formatVoucherBenefit } from "@/lib/format";
import type { GatewayVoucher } from "@/lib/gateway-types";

export { formatVoucherBenefit };

export const DEFAULT_COD_MAX_VND = 5_000_000;
export const DEFAULT_COD_MAX_REJECTED = 3;

export function formatVoucherCondition(voucher: GatewayVoucher): string {
  if (voucher.min_order_vnd > 0) {
    return `Đơn tối thiểu ${formatVnd(voucher.min_order_vnd)}`;
  }
  return "Áp dụng cho mọi đơn hàng";
}

/**
 * Translates backend promotion eligibility / warning strings into natural Vietnamese
 * without leaking raw English messages or internal identifiers.
 */
export function translateVoucherReason(rawReason?: string): string {
  const clean = (rawReason ?? "").trim();
  if (!clean) return "";

  const lower = clean.toLowerCase();

  if (lower.includes("not found")) {
    return "Mã ưu đãi không tồn tại hoặc chưa được mở áp dụng.";
  }
  if (lower.includes("inactive")) {
    return "Mã ưu đãi hiện đang tạm ngưng.";
  }
  if (lower.includes("not started")) {
    return "Mã ưu đãi chưa đến thời gian bắt đầu.";
  }
  if (lower.includes("expired") || lower.includes("active time window")) {
    return "Mã ưu đãi đã hết hạn sử dụng.";
  }
  if (lower.includes("out of quota") || lower.includes("quota")) {
    return "Mã ưu đãi đã hết lượt sử dụng.";
  }
  if (lower.includes("budget exhausted") || lower.includes("budget")) {
    return "Ngân sách ưu đãi của mã này đã được dùng hết.";
  }
  if (lower.includes("per-user") || lower.includes("limit reached")) {
    return "Bạn đã dùng hết số lượt cho phép của mã ưu đãi này.";
  }
  if (lower.includes("minimum order") || lower.includes("shortfall")) {
    const minMatch = clean.match(/minimum order\s+(\d+)\s*VND/i);
    const shortMatch = clean.match(/shortfall\s+(\d+)\s*VND/i);
    const minVnd = minMatch ? Number.parseInt(minMatch[1], 10) : 0;
    const shortVnd = shortMatch ? Number.parseInt(shortMatch[1], 10) : 0;
    if (shortVnd > 0 && minVnd > 0) {
      return `Mua thêm ${formatVnd(shortVnd)} để đạt đơn tối thiểu ${formatVnd(minVnd)}.`;
    }
    if (minVnd > 0) {
      return `Chưa đạt giá trị đơn tối thiểu ${formatVnd(minVnd)}.`;
    }
    return "Đơn hàng chưa đạt giá trị tối thiểu để áp dụng mã ưu đãi.";
  }
  if (lower.includes("does not belong to shop") || lower.includes("not present in items")) {
    return "Mã ưu đãi không áp dụng cho gian hàng đang chọn.";
  }
  if (lower.includes("scope")) {
    return "Loại mã ưu đãi không khớp với mục đang chọn.";
  }
  if (lower.includes("coins exceeds") || lower.includes("insufficient coin")) {
    return "Số Xu áp dụng đã được điều chỉnh theo số dư khả dụng của bạn.";
  }
  if (lower.includes("flash sale")) {
    return "Ưu đãi Flash sale không còn hiệu lực cho sản phẩm đã chọn.";
  }

  // Already Vietnamese text from backend or fallback
  if (/[àáạảãâầấậẩẫăằắặẳẵèéẹẻẽêềếệểễìíịỉĩòóọỏõôồốộổỗơờớợởỡùúụủũưừứựửữỳýỵỷỹđ]/i.test(clean)) {
    return clean;
  }

  return "Mã ưu đãi chưa đủ điều kiện áp dụng cho đơn hàng hiện tại.";
}

export interface CheckoutErrorContext {
  paymentMethod?: "COD" | "VIETQR";
  totalPayableVnd?: number;
  codRejectedCount?: number;
  hasVouchers?: boolean;
  isFlashSale?: boolean;
}

export function translateCheckoutError(err: unknown, ctx?: CheckoutErrorContext): string {
  if (!(err instanceof ApiError)) {
    return "Không thể hoàn tất đặt hàng lúc này. Vui lòng thử lại sau ít phút.";
  }

  if (err.status === 401 || err.code === "UNAUTHENTICATED") {
    return "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại để tiếp tục đặt hàng.";
  }

  if (err.status >= 500 || err.code === "UNAVAILABLE") {
    return "Hệ thống đặt hàng đang tạm bận. Đơn hàng chưa bị trừ tiền, bạn có thể bấm đặt lại sau ít phút.";
  }

  if (err.code === "INSUFFICIENT_STOCK") {
    return "Một hoặc nhiều sản phẩm trong đơn vừa hết hàng hoặc không đủ số lượng tồn kho. Vui lòng kiểm tra lại giỏ hàng.";
  }

  if (err.code === "RESOURCE_EXHAUSTED") {
    if (ctx?.isFlashSale) {
      return "Suất ưu đãi Flash sale đã hết lượt. Vui lòng quay lại kiểm tra giá thường hoặc chọn sản phẩm khác.";
    }
    if (ctx?.hasVouchers) {
      return "Mã ưu đãi bạn chọn vừa hết lượt sử dụng. Vui lòng bỏ chọn mã hoặc chọn mã khác.";
    }
    return "Ưu đãi hoặc suất mua đã hết lượt. Vui lòng thử lại sau ít phút.";
  }

  if (err.code === "FAILED_PRECONDITION") {
    if (ctx?.paymentMethod === "COD") {
      if ((ctx.codRejectedCount ?? 0) >= DEFAULT_COD_MAX_REJECTED) {
        return "Tài khoản tạm không áp dụng thanh toán khi nhận hàng (COD). Vui lòng chọn chuyển khoản VietQR.";
      }
      if ((ctx.totalPayableVnd ?? 0) > DEFAULT_COD_MAX_VND) {
        return `Đơn hàng vượt quá hạn mức thanh toán khi nhận hàng (tối đa ${formatVnd(DEFAULT_COD_MAX_VND)}). Vui lòng chọn chuyển khoản VietQR.`;
      }
      return "Không thể đặt đơn COD lúc này (có thể do vượt hạn mức COD, tồn kho thay đổi hoặc ưu đãi không còn hiệu lực). Bạn có thể thử chuyển sang VietQR hoặc kiểm tra lại ưu đãi.";
    }
    if (ctx?.isFlashSale) {
      return "Suất Flash sale đã kết thúc hoặc bạn đã đạt giới hạn mua cho sản phẩm này.";
    }
    if (ctx?.hasVouchers) {
      return "Mã ưu đãi hoặc số lượng sản phẩm không còn đủ điều kiện đặt hàng. Vui lòng kiểm tra lại.";
    }
    return "Không thể tạo đơn ở trạng thái hiện tại. Vui lòng kiểm tra lại địa chỉ, tồn kho và ưu đãi.";
  }

  if (err.code === "ABORTED") {
    return "Tồn kho hoặc trạng thái đơn hàng vừa thay đổi. Vui lòng thử đặt lại.";
  }

  if (err.code === "NOT_FOUND") {
    return "Địa chỉ giao hàng hoặc sản phẩm không còn tồn tại. Vui lòng tải lại trang và chọn lại.";
  }

  return err.message || "Không thể đặt hàng lúc này. Vui lòng kiểm tra lại thông tin.";
}

export interface PaymentStatusView {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
  description: string;
}

export function getPaymentStatusView(rawStatus: string): PaymentStatusView {
  switch (rawStatus.toUpperCase()) {
    case "SUCCEEDED":
      return {
        label: "Đã thanh toán",
        tone: "pos",
        description:
          "Thanh toán đã được ghi nhận thành công. Đơn hàng đang được chuyển tới gian hàng để chuẩn bị.",
      };
    case "PENDING":
      return {
        label: "Chờ thanh toán",
        tone: "warn",
        description:
          "Mở ứng dụng ngân hàng để quét mã VietQR bên dưới. Hệ thống sẽ tự động xác nhận ngay khi nhận được chuyển khoản.",
      };
    case "EXPIRED":
      return {
        label: "Hết hạn thanh toán",
        tone: "neg",
        description:
          "Giao dịch chuyển khoản đã quá thời hạn giữ đơn và đơn hàng đã tự động hủy. Vui lòng đặt lại đơn mới từ giỏ hàng hoặc trang sản phẩm.",
      };
    case "FAILED":
      return {
        label: "Thanh toán chưa thành công",
        tone: "neg",
        description:
          "Giao dịch trước đó chưa hoàn tất. Bạn có thể tạo mã VietQR mới để thanh toán lại mà không cần đặt lại giỏ hàng.",
      };
    case "CANCELLED":
      return {
        label: "Đã hủy giao dịch",
        tone: "neutral",
        description: "Giao dịch thanh toán này đã được hủy hoặc thay thế bằng giao dịch mới.",
      };
    case "REFUNDED":
      return {
        label: "Đã hoàn tiền",
        tone: "neutral",
        description: "Khoản thanh toán của giao dịch này đã được hoàn lại.",
      };
    default:
      return {
        label: "Đang xử lý",
        tone: "neutral",
        description: "Hệ thống đang cập nhật trạng thái thanh toán của đơn hàng.",
      };
  }
}

export function formatCarrierDisplay(carrierName: string, carrierCode: string): string {
  const raw = (carrierName || carrierCode || "").trim();
  if (!raw || raw.toLowerCase().includes("mock")) {
    return "Chuyển phát Nexus";
  }
  return raw;
}

export function formatServiceDisplay(serviceName: string, serviceCode: string): string {
  const raw = (serviceName || serviceCode || "").trim();
  const lower = raw.toLowerCase();
  const code = serviceCode.trim().toUpperCase();
  if (!raw || lower.includes("mock")) {
    if (
      code === "STANDARD" ||
      lower.includes("tiêu chuẩn") ||
      lower.includes("tieu chuan") ||
      lower.includes("standard")
    ) {
      return "Giao tiêu chuẩn";
    }
    if (code === "EXPRESS" || lower.includes("nhanh") || lower.includes("fast")) {
      return "Giao nhanh";
    }
    return "Giao tiêu chuẩn";
  }
  return raw;
}

export function formatCountdown(remainingSeconds: number): string {
  const clamped = Math.max(0, Math.floor(remainingSeconds));
  const mins = Math.floor(clamped / 60);
  const secs = clamped % 60;
  const mm = mins < 10 ? `0${mins}` : String(mins);
  const ss = secs < 10 ? `0${secs}` : String(secs);
  return `${mm}:${ss}`;
}
