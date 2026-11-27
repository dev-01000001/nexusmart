import type { GatewayErrorEnvelope } from "@/lib/gateway-types";

export const UNAVAILABLE_MESSAGE = "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.";
const CODE_PATTERN = /^[A-Z0-9_]{2,64}$/;
const CONTROL_CHAR_RE = /[\u0000-\u001f\u007f]/g;

/**
 * Real domain error prefixes emitted by services/order, services/promotion, and services/product
 * inside gRPC status messages (gateway maps gRPC FailedPrecondition -> HTTP 409 FAILED_PRECONDITION).
 */
const KNOWN_PREFIXED_ERROR_CODES = new Set<string>([
  "IDEMPOTENCY_KEY_CONFLICT",
  "CHECKOUT_FAILED",
  "SHOP_PICKUP_ADDRESS_REQUIRED",
  "OWNER_NAME_CHANGED",
  "INSUFFICIENT_STOCK",
  "SKU_INACTIVE",
  "SKU_NOT_ACTIVE",
  "STOCK_VERSION_CONFLICT",
  "COD_LIMIT_EXCEEDED",
  "COD_REJECTED_LIMIT_EXCEEDED",
  "COD_PHONE_NOT_VERIFIED",
  "COD_AREA_NOT_SUPPORTED",
  "ADDRESS_WARD_REQUIRED",
  "CART_NO_AVAILABLE_ITEMS",
  "BANNED_CONTENT",
  "PROHIBITED_CATEGORY",
  "FLASH_SALE_COD_NOT_ALLOWED",
  "FLASH_SALE_NOT_ACTIVE",
  "FLASH_SALE_SKU_MISMATCH",
  "FLASH_SALE_SHOP_MISMATCH",
  "FLASH_SALE_INVALID_PRICE",
  "FLASH_SALE_PRICE_CHANGED",
  "FLASH_UNAVAILABLE_ON_COMMIT",
  "VOUCHER_UNAVAILABLE_ON_COMMIT",
  "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT",
  "SHOP_PROGRAM_PRICE_CHANGED",
  "SHOP_PROGRAM_ITEM_UNKNOWN",
  "INSUFFICIENT_COINS",
  "PROMOTION_BREAKDOWN_MISSING",
  "RESERVATION_ALREADY_RELEASED",
  "INVALID_COMMISSION_RATE",
  "FLASH_SALE_LOOKUP_FAILED",
  "SHOP_DISCOUNT_LOOKUP_FAILED",
  "PAYMENT_FEE_CONFIG_UNAVAILABLE",
  "PAYMENT_FEE_CONFIG_FAILED",
  "PAYMENT_FEE_CONFIG_INVALID",
  "PAYMENT_FEE_CONFIG_NOT_FOUND",
  "PAYMENT_NOT_INITIALIZED",
  "SHOP_SUSPENDED",
  "PAYMENT_METHOD_DISABLED",
  "ORDER_AMOUNT_OUT_OF_RANGE",
  "VERSION_CONFLICT",
  "MAKER_CHECKER_CONFLICT",
  "REINDEX_ALREADY_RUNNING",
  "INSUFFICIENT_BALANCE",
  "PAYOUT_PENDING_EXISTS",
  "BANK_ACCOUNT_NOT_READY",
]);

function normalizeDomainErrorCode(rawCode: string, rawMessage: string): string {
  const prefixMatch = /^([A-Z][A-Z0-9_]+)(?:\s*\([^)]*\))?:/.exec(rawMessage.trim());
  if (prefixMatch && KNOWN_PREFIXED_ERROR_CODES.has(prefixMatch[1])) {
    return prefixMatch[1];
  }
  const lower = rawMessage.toLowerCase();
  if (
    lower.includes("not enough available stock") ||
    lower.includes("insufficient stock") ||
    lower.includes("out of stock")
  ) {
    return "INSUFFICIENT_STOCK";
  }
  return rawCode;
}

function defaultCodeForStatus(status: number): string {
  switch (status) {
    case 400:
      return "INVALID_ARGUMENT";
    case 401:
      return "UNAUTHENTICATED";
    case 403:
      return "PERMISSION_DENIED";
    case 404:
      return "NOT_FOUND";
    case 409:
      return "CONFLICT";
    case 412:
      return "FAILED_PRECONDITION";
    case 413:
      return "PAYLOAD_TOO_LARGE";
    case 429:
      return "RESOURCE_EXHAUSTED";
    case 499:
      return "CANCELED";
    case 504:
      return "DEADLINE_EXCEEDED";
    default:
      return status >= 500 ? "UNAVAILABLE" : "BAD_REQUEST";
  }
}

function extractSafeLockReason(rawMessage: string): string {
  const cleaned = rawMessage.replace(CONTROL_CHAR_RE, " ").replace(/\s+/g, " ").trim();
  if (!cleaned) return "";

  // Match common lock-reason formats from services/user (English or Vietnamese).
  const reasonMatch =
    /(?:lý\s*do|reason)\s*[:\-]\s*(.+)$/i.exec(cleaned) ??
    /account\s+is\s+locked\s*[:\-]\s*(.+)$/i.exec(cleaned) ??
    /tài\s*khoản\s+(?:đang\s+|đã\s+)?bị\s+khóa\s*[:.\-]\s*(.+)$/i.exec(cleaned);

  if (!reasonMatch) return "";
  let reason = reasonMatch[1].trim();
  // Strip trailing parentheses or punctuation and ignore pure contact-support boilerplate.
  reason = reason
    .replace(/^[:.\-\s]+/, "")
    .replace(/\)+$/, "")
    .trim();
  if (
    !reason ||
    /^(?:vui\s*lòng\s+)?liên\s*hệ|^please\s+contact|^contact\s+support/i.test(reason)
  ) {
    return "";
  }
  if (reason.length > 260) {
    reason = `${reason.slice(0, 260).trim()}...`;
  }
  return reason;
}

function extractSafeBannedDetail(rawMessage: string): string {
  const cleaned = rawMessage.replace(CONTROL_CHAR_RE, " ").replace(/\s+/g, " ").trim();
  if (!cleaned) return "";
  const match =
    /banned\s+keywords?\s*[:\-]\s*(.+)$/i.exec(cleaned) ??
    /matched\s+banned\s+(?:keyword|term)s?\s*[:\-]\s*(.+)$/i.exec(cleaned) ??
    /^BANNED_CONTENT\s*[:\-]\s*matched\s*[:\-]?\s*(.+)$/i.exec(cleaned);
  if (!match) return "";
  const detail = match[1].trim();
  if (!detail || /^(?:review|product)\s+contains\b/i.test(detail)) return "";
  return detail.length > 140 ? `${detail.slice(0, 140).trim()}...` : detail;
}

export function toSafeVietnameseMessage(status: number, code: string, rawMessage: string): string {
  const lower = rawMessage.toLowerCase();

  // Auth, lock reason, password reset & OTP domain mappings (services/user)
  if (lower.includes("invalid email or password")) {
    return "Email hoặc mật khẩu chưa đúng.";
  }
  if (lower.includes("too many failed login")) {
    return "Tài khoản tạm thời bị khóa do đăng nhập sai nhiều lần. Vui lòng thử lại sau ít phút.";
  }
  if (
    lower.includes("account is locked") ||
    lower.includes("tài khoản đang bị khóa") ||
    lower.includes("tài khoản đã bị khóa") ||
    lower.includes("tài khoản bị khóa")
  ) {
    const reason = extractSafeLockReason(rawMessage);
    if (reason) {
      if (/liên\s*hệ/i.test(reason)) {
        return `Tài khoản đang bị khóa. Lý do: ${reason}`;
      }
      return `Tài khoản đang bị khóa. Lý do: ${reason}. Vui lòng liên hệ bộ phận hỗ trợ NexusMart để được kiểm tra.`;
    }
    return "Tài khoản đang bị khóa. Vui lòng liên hệ bộ phận hỗ trợ NexusMart.";
  }
  if (lower.includes("account pending verification")) {
    return "Tài khoản chưa xác thực email. Vui lòng nhập mã OTP đã gửi tới hộp thư của bạn.";
  }
  if (lower.includes("account is not pending verification")) {
    return "Tài khoản này đã được xác thực trước đó.";
  }
  if (lower.includes("account is not active")) {
    return "Tài khoản hiện chưa hoạt động.";
  }
  if (lower.includes("email is already registered")) {
    return "Địa chỉ email này đã được đăng ký. Vui lòng đăng nhập hoặc dùng email khác.";
  }
  if (lower.includes("phone is already registered") || lower.includes("phone is already in use")) {
    return "Số điện thoại này đã được sử dụng cho tài khoản khác.";
  }
  if (lower.includes("email or phone already registered")) {
    return "Email hoặc số điện thoại này đã được đăng ký.";
  }
  if (
    lower.includes("reset token") ||
    (lower.includes("reset") &&
      (lower.includes("expired") || lower.includes("invalid") || lower.includes("used")))
  ) {
    return "Liên kết đặt lại mật khẩu không hợp lệ, đã được sử dụng hoặc đã hết hạn. Vui lòng yêu cầu liên kết mới.";
  }
  if (lower.includes("no bank account pending otp")) {
    return "Yêu cầu xác thực OTP không còn hiệu lực hoặc đã hết hạn. Vui lòng nhập lại thông tin tài khoản ngân hàng để nhận mã mới.";
  }
  if (lower.includes("invalid or expired otp")) {
    return "Mã xác thực không đúng hoặc đã hết hạn. Vui lòng kiểm tra lại hộp thư hoặc yêu cầu gửi mã mới.";
  }
  if (lower.includes("otp not found") || lower.includes("otp expired")) {
    return "Mã xác thực đã hết hạn hoặc không tồn tại. Vui lòng yêu cầu gửi lại mã mới.";
  }
  if (lower.includes("too many invalid otp")) {
    return "Bạn đã nhập sai mã xác thực quá nhiều lần. Vui lòng yêu cầu gửi lại mã mới.";
  }
  if (lower.includes("invalid otp")) {
    return "Mã xác thực chưa chính xác. Vui lòng kiểm tra lại hộp thư.";
  }
  if (lower.includes("otp resend limit")) {
    return "Bạn đã gửi lại mã quá nhiều lần. Vui lòng đợi ít phút rồi thử lại.";
  }
  if (lower.includes("password must be at least")) {
    return "Mật khẩu cần có tối thiểu 8 ký tự.";
  }
  if (lower.includes("valid email is required")) {
    return "Địa chỉ email chưa đúng định dạng.";
  }
  if (lower.includes("full_name is required")) {
    return "Vui lòng nhập họ và tên đầy đủ.";
  }
  if (lower.includes("refresh token reuse")) {
    return "Phiên đăng nhập đã bị thu hồi để bảo vệ tài khoản. Vui lòng đăng nhập lại.";
  }
  if (lower.includes("refresh token expired") || lower.includes("invalid refresh token")) {
    return "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.";
  }

  // Shipping & Cancel shipment domain mappings
  if (
    lower.includes("already picked") ||
    lower.includes("picked up") ||
    lower.includes("carrier has") ||
    lower.includes("đã lấy hàng") ||
    (lower.includes("cancel") && lower.includes("shipment") && status === 409)
  ) {
    return "Đơn vị vận chuyển đã lấy kiện hàng nên không thể hủy đơn lúc này.";
  }
  if (
    lower.includes("đã có yêu cầu giao lại") ||
    ((lower.includes("redelivery") || lower.includes("giao lại")) && lower.includes("already"))
  ) {
    return "Bạn đã gửi yêu cầu giao lại cho lần giao thất bại hiện tại. Vui lòng chờ hãng vận chuyển xử lý.";
  }
  if (
    (lower.includes("redelivery") || lower.includes("giao lại")) &&
    (lower.includes("max") ||
      lower.includes("limit") ||
      lower.includes("tối đa") ||
      lower.includes("exceeded"))
  ) {
    return "Đơn hàng đã đạt số lần yêu cầu giao lại tối đa (3 lần).";
  }
  if (lower.includes("redelivery") || lower.includes("giao lại")) {
    return "Chỉ có thể yêu cầu giao lại khi vận đơn đang ở trạng thái giao thất bại.";
  }
  if (lower.includes("at least one") && lower.includes("carrier")) {
    return "Gian hàng cần bật ít nhất một hãng vận chuyển.";
  }
  if (lower.includes("carrier") && (lower.includes("token") || lower.includes("connection"))) {
    return "Kiểm tra kết nối hãng vận chuyển thất bại hoặc mã kết nối chưa hợp lệ.";
  }

  // Return request domain mappings
  if (lower.includes("return window") || (lower.includes("return") && lower.includes("7 day"))) {
    return "Đã quá thời hạn 7 ngày kể từ khi nhận hàng để tạo yêu cầu trả hàng.";
  }
  if (lower.includes("return request already exists") || lower.includes("already has a return")) {
    return "Đơn hàng này đã có yêu cầu trả hàng trước đó.";
  }
  if (lower.includes("goods already received") || lower.includes("already received")) {
    return "Hàng trả đã được tiếp nhận, không thể chọn yêu cầu gửi trả hàng thêm lần nữa.";
  }
  if (lower.includes("evidence") && (lower.includes("url") || lower.includes("5"))) {
    return "Tối đa 5 liên kết bằng chứng hợp lệ (bắt đầu bằng http:// hoặc https://, tối đa 500 ký tự mỗi liên kết).";
  }

  // Phone OTP rate-limit / cooldown (services/user phone_otp.go) — must run before bank OTP 429 branch
  if (
    (status === 429 || code === "RESOURCE_EXHAUSTED") &&
    lower.includes("phone") &&
    lower.includes("otp")
  ) {
    const secMatch = /please\s+wait\s+(\d+)\s+seconds?/i.exec(rawMessage);
    if (secMatch) {
      return `Vui lòng đợi ${secMatch[1]} giây trước khi yêu cầu gửi lại mã OTP xác minh số điện thoại.`;
    }
    if (lower.includes("too many")) {
      return "Bạn đã yêu cầu gửi mã OTP xác minh số điện thoại quá số lần cho phép trong giờ. Vui lòng thử lại sau.";
    }
    return "Vui lòng đợi hết thời gian chờ trước khi yêu cầu gửi lại mã OTP xác minh số điện thoại.";
  }
  if (lower.includes("valid vietnamese phone is required")) {
    return "Vui lòng nhập số điện thoại Việt Nam hợp lệ (ví dụ: 0912345678).";
  }
  if (lower.includes("user email is required for phone otp delivery")) {
    return "Tài khoản cần có địa chỉ email hợp lệ để nhận mã OTP xác minh số điện thoại.";
  }
  if (lower.includes("phone verification service unavailable")) {
    return "Dịch vụ xác minh số điện thoại đang tạm bận. Vui lòng thử lại sau ít phút.";
  }
  if (lower.includes("otp is required")) {
    return "Vui lòng nhập mã OTP gồm 6 chữ số.";
  }

  // Refund, Bank Account & Reconciliation domain mappings
  if (
    (status === 429 || code === "RESOURCE_EXHAUSTED") &&
    (lower.includes("bank") || lower.includes("otp"))
  ) {
    return "Bạn đã gửi yêu cầu OTP tài khoản ngân hàng quá số lần cho phép. Vui lòng thử lại sau.";
  }
  if (
    lower.includes("payout locked") ||
    lower.includes("payout_locked") ||
    (lower.includes("24h") && lower.includes("bank"))
  ) {
    return "Tài khoản nhận tiền đang trong thời gian tạm khóa bảo mật 24 giờ sau khi thay đổi thông tin ngân hàng.";
  }
  if (code === "BANK_ACCOUNT_NOT_READY" || lower.includes("bank_account_not_ready")) {
    return "Tài khoản ngân hàng nhận tiền chưa được xác minh hoặc đang trong thời gian khóa bảo mật 24 giờ.";
  }
  if (code === "PAYOUT_PENDING_EXISTS" || lower.includes("payout_pending_exists")) {
    return "Gian hàng đang có một yêu cầu rút tiền chờ duyệt. Vui lòng chờ xử lý xong trước khi tạo yêu cầu mới.";
  }
  if (code === "INSUFFICIENT_BALANCE" || lower.includes("insufficient_balance")) {
    return "Số dư khả dụng hiện không đủ cho số tiền rút bạn yêu cầu.";
  }
  if (lower.includes("bank_account_id is required")) {
    return "Thiếu mã bản ghi tài khoản ngân hàng cần thẩm định. Vui lòng tải lại trang và thử lại.";
  }
  if (
    (status === 400 || code === "INVALID_ARGUMENT") &&
    (lower.includes("bank_account") || lower.includes("bank account"))
  ) {
    return "Thông tin tài khoản ngân hàng chưa hợp lệ (số tài khoản gồm 6–19 chữ số).";
  }
  if (
    lower.includes("underpayment") ||
    lower.includes("less than expected") ||
    lower.includes("thiếu tiền")
  ) {
    return "Số tiền giao dịch thấp hơn giá trị đơn hàng nên không thể gán khớp.";
  }
  if (lower.includes("already resolved")) {
    return "Mục đối soát này đã được kết luận trước đó.";
  }
  if (
    lower.includes("cổng chưa có dữ liệu") ||
    lower.includes("payos") ||
    lower.includes("not supported")
  ) {
    return "Cổng thanh toán hiện chưa có dữ liệu giao dịch để chạy đối soát tự động.";
  }
  if (
    lower.includes("cannot be in the future") ||
    lower.includes("future date") ||
    (lower.includes("run_date") && lower.includes("yyyy-mm-dd")) ||
    (lower.includes("date") && lower.includes("invalid"))
  ) {
    return "Ngày đối soát không hợp lệ hoặc nằm trong tương lai.";
  }

  // Wave 2b: Reports, Fees, Discount Programs & Flash Sale rules
  if (lower.includes("366") || (lower.includes("date range") && lower.includes("exceed"))) {
    return "Khoảng thời gian báo cáo tối đa là 366 ngày và ngày bắt đầu phải trước hoặc bằng ngày kết thúc.";
  }
  if (lower.includes("cod") && lower.includes("flash")) {
    return "Đơn hàng có sản phẩm Flash sale không hỗ trợ COD. Vui lòng chọn chuyển khoản VietQR.";
  }
  if (
    lower.includes("min_discount_percent") ||
    (lower.includes("discount") && lower.includes("minimum"))
  ) {
    return "Mức giảm giá chưa đạt tỷ lệ giảm tối thiểu của khung giờ Flash sale.";
  }
  if (lower.includes("overlap") && lower.includes("flash")) {
    return "Biến thể SKU này đã nằm trong khung giờ Flash sale bị trùng thời gian.";
  }
  if (
    lower.includes("overlap") &&
    (lower.includes("program") || lower.includes("discount") || lower.includes("sku"))
  ) {
    return "Một hoặc nhiều SKU đang nằm trong chương trình giảm giá khác bị trùng thời gian.";
  }
  if (
    (lower.includes("voucher") &&
      (lower.includes("used_count") || lower.includes("already used"))) ||
    lower.includes("cannot decrease quota") ||
    lower.includes("cannot shorten")
  ) {
    return "Voucher đã có lượt sử dụng nên chỉ được đổi tên, tăng hạn mức/ngân sách hoặc kéo dài thời gian kết thúc.";
  }
  if (lower.includes("effective_from") && (lower.includes("past") || lower.includes("future"))) {
    return "Thời điểm áp dụng biểu phí phải hợp lệ và không nằm ở quá khứ.";
  }

  // Wave 3a: D6 optimistic review conflicts & D11 checkout failure / shop pickup address
  if (code === "OWNER_NAME_CHANGED" || lower.includes("owner_name_changed")) {
    return "Dữ liệu đã thay đổi — họ tên chủ gian hàng vừa được cập nhật. Vui lòng tải lại và kiểm tra trước khi phê duyệt.";
  }
  if (lower.includes("only pending_admin_review bank accounts can be reviewed")) {
    return "Dữ liệu đã thay đổi — tài khoản ngân hàng này không còn ở trạng thái chờ duyệt. Vui lòng tải lại để xem bản ghi mới nhất.";
  }
  if (lower.includes("only pending shops can be reviewed")) {
    return "Dữ liệu đã thay đổi — hồ sơ gian hàng này không còn ở trạng thái chờ duyệt. Vui lòng tải lại danh sách.";
  }

  // Wave 3b-1: Banned content, prohibited categories, phone OTP, COD eligibility & COD CSV import
  if (code === "BANNED_CONTENT" || lower.includes("banned_content")) {
    const matched = extractSafeBannedDetail(rawMessage);
    if (matched) {
      return `Nội dung chứa từ khoá không được phép (${matched}). Vui lòng chỉnh sửa trước khi gửi.`;
    }
    return "Nội dung chứa từ khoá không được phép trên sàn. Vui lòng chỉnh sửa trước khi gửi.";
  }
  if (code === "PROHIBITED_CATEGORY" || lower.includes("prohibited_category")) {
    return "Danh mục sản phẩm thuộc nhóm hàng bị cấm kinh doanh trên sàn.";
  }
  if (code === "COD_PHONE_NOT_VERIFIED" || lower.includes("cod_phone_not_verified")) {
    return "Vui lòng xác minh số điện thoại trong mục Tài khoản trước khi đặt hàng thanh toán khi nhận hàng (COD).";
  }
  if (code === "COD_AREA_NOT_SUPPORTED" || lower.includes("cod_area_not_supported")) {
    return "Khu vực nhận hàng này hiện chưa hỗ trợ thanh toán khi nhận hàng (COD). Vui lòng đổi địa chỉ hoặc chọn chuyển khoản VietQR.";
  }
  if (code === "ADDRESS_WARD_REQUIRED" || lower.includes("address_ward_required")) {
    return "Địa chỉ nhận hàng cần chọn đầy đủ Tỉnh/Thành phố và Phường/Xã cụ thể.";
  }
  if (code === "CART_NO_AVAILABLE_ITEMS" || lower.includes("cart_no_available_items")) {
    return "Không còn sản phẩm khả dụng trong giỏ hàng để đặt đơn.";
  }
  if (lower.includes("only completed orders can be reviewed")) {
    return "Chỉ có thể đánh giá sản phẩm sau khi đơn hàng đã hoàn tất.";
  }
  if (lower.includes("already been reviewed")) {
    return "Bạn đã đánh giá sản phẩm này trong đơn hàng rồi.";
  }
  if (lower.includes("rating must be between 1 and 5")) {
    return "Điểm đánh giá phải từ 1 đến 5 sao.";
  }
  if (lower.includes("comment exceeds maximum length")) {
    return "Nội dung đánh giá vượt quá giới hạn 2.000 ký tự.";
  }
  if (lower.includes("image_urls[") || (lower.includes("at most") && lower.includes("image url"))) {
    return "Liên kết ảnh đánh giá chưa hợp lệ (tối đa 5 ảnh https:// hoặc /products/..., mỗi liên kết tối đa 500 ký tự).";
  }
  if (lower.includes("term must contain at least 2 alphanumeric")) {
    return "Cụm từ cấm cần có ít nhất 2 ký tự chữ hoặc số.";
  }
  if (lower.includes("banned keyword already exists")) {
    return "Từ khoá cấm này đã tồn tại trong danh sách.";
  }
  if (lower.includes("banned keyword not found")) {
    return "Không tìm thấy từ khoá cấm cần thao tác.";
  }
  if (
    lower.includes("action must be block or review") ||
    lower.includes("invalid banned keyword action")
  ) {
    return "Hành động xử lý từ khoá cấm chỉ chấp nhận BLOCK hoặc REVIEW.";
  }
  if (lower.includes("legacy_province is required")) {
    return "Vui lòng nhập Tỉnh/Thành phố cũ để quy đổi địa chỉ.";
  }
  if (lower.includes("phone") && (lower.includes("changed") || lower.includes("mismatch"))) {
    return "Số điện thoại không khớp với mã OTP đã gửi. Vui lòng yêu cầu gửi lại mã mới.";
  }
  if (lower.includes("phone") && lower.includes("cooldown")) {
    return "Vui lòng đợi hết thời gian chờ trước khi yêu cầu gửi lại mã OTP xác minh số điện thoại.";
  }
  const csvLineMatch = /^line\s+(\d+)\s*:\s*(.+)$/i.exec(rawMessage.trim());
  if (csvLineMatch) {
    const lineNo = csvLineMatch[1];
    const detailLower = csvLineMatch[2].toLowerCase();
    if (detailLower.includes("cod_amount_vnd")) {
      if (detailLower.includes("range")) {
        return `Dòng ${lineNo}: số tiền thu hộ (cod_amount_vnd) vượt quá giới hạn cho phép.`;
      }
      return `Dòng ${lineNo}: số tiền thu hộ (cod_amount_vnd) phải là số nguyên VND không âm (chỉ gồm chữ số).`;
    }
    if (detailLower.includes("tracking_code")) {
      return `Dòng ${lineNo}: mã vận đơn (tracking_code) không hợp lệ, để trống hoặc chứa ký tự không được phép.`;
    }
    if (detailLower.includes("delivered_at")) {
      return `Dòng ${lineNo}: thời gian giao hàng (delivered_at) không hợp lệ hoặc vượt quá độ dài cho phép.`;
    }
    if (detailLower.includes("order_code")) {
      return `Dòng ${lineNo}: mã đơn hàng (order_code) không hợp lệ hoặc vượt quá độ dài cho phép.`;
    }
    if (detailLower.includes("column count")) {
      return `Dòng ${lineNo}: số lượng cột không khớp với dòng tiêu đề CSV.`;
    }
    return `Dòng ${lineNo}: dữ liệu CSV không hợp lệ, vui lòng kiểm tra lại.`;
  }
  if (lower.includes("invalid carrier_code")) {
    return "Mã hãng vận chuyển không hợp lệ.";
  }
  if (lower.includes("invalid file_name")) {
    return "Tên tệp CSV không hợp lệ.";
  }
  if (
    lower.includes("csv") &&
    (lower.includes("empty") ||
      lower.includes("no data") ||
      lower.includes("5000") ||
      lower.includes("row") ||
      lower.includes("column") ||
      lower.includes("header") ||
      lower.includes("malformed") ||
      lower.includes("utf-8") ||
      lower.includes("formula") ||
      lower.includes("amount") ||
      lower.includes("2 mib") ||
      lower.includes("exceeds") ||
      lower.includes("content-type") ||
      lower.includes("multipart"))
  ) {
    return "Tệp CSV đối soát COD chưa đúng định dạng (UTF-8, tối đa 2 MiB và 5.000 dòng, cột tracking_code,cod_amount_vnd,delivered_at với số tiền nguyên VND).";
  }

  // Commerce domain mappings (prioritize normalized domain codes from services/order, promotion, product)
  if (code === "CHECKOUT_FAILED") {
    return "Lần đặt hàng trước không hoàn tất và các đơn liên quan đã bị hủy. Bạn có thể kiểm tra Đơn hàng của tôi hoặc thử đặt lại.";
  }
  if (code === "SHOP_PICKUP_ADDRESS_REQUIRED") {
    return "Gian hàng chưa cấu hình đầy đủ địa chỉ lấy hàng hợp lệ nên chưa thể tính phí hoặc tạo vận đơn lúc này.";
  }
  if (code === "IDEMPOTENCY_KEY_CONFLICT" || lower.includes("idempotency")) {
    return "Lần đặt trước có thể đã tạo đơn hàng rồi. Vui lòng kiểm tra mục Đơn hàng của tôi trước khi đặt lại.";
  }
  if (
    code === "INSUFFICIENT_STOCK" ||
    lower.includes("insufficient stock") ||
    lower.includes("not enough available stock") ||
    lower.includes("out of stock")
  ) {
    return "Sản phẩm không đủ số lượng tồn kho.";
  }
  if (code === "SKU_INACTIVE" || code === "SKU_NOT_ACTIVE") {
    return "Phân loại sản phẩm hiện đã ngừng kinh doanh hoặc không khả dụng.";
  }
  if (code === "STOCK_VERSION_CONFLICT") {
    return "Tồn kho sản phẩm vừa thay đổi. Vui lòng tải lại và thử lại.";
  }
  if (code === "COD_REJECTED_LIMIT_EXCEEDED") {
    return "Tài khoản tạm không áp dụng thanh toán khi nhận hàng (COD) do vượt giới hạn từ chối nhận hàng. Vui lòng chọn VietQR.";
  }
  if (code === "COD_LIMIT_EXCEEDED") {
    return "Đơn hàng vượt quá hạn mức thanh toán khi nhận hàng (COD). Vui lòng chọn chuyển khoản VietQR.";
  }
  if (code === "FLASH_SALE_COD_NOT_ALLOWED") {
    return "Đơn hàng có sản phẩm Flash sale không hỗ trợ COD. Vui lòng chọn chuyển khoản VietQR.";
  }
  if (
    code === "FLASH_SALE_NOT_ACTIVE" ||
    code === "FLASH_UNAVAILABLE_ON_COMMIT" ||
    code === "FLASH_SALE_PRICE_CHANGED" ||
    code === "FLASH_SALE_SKU_MISMATCH" ||
    code === "FLASH_SALE_SHOP_MISMATCH" ||
    code === "FLASH_SALE_INVALID_PRICE"
  ) {
    return "Suất Flash sale đã hết lượt, kết thúc hoặc bạn đã đạt giới hạn mua.";
  }
  if (code === "VOUCHER_UNAVAILABLE_ON_COMMIT") {
    return "Mã ưu đãi bạn chọn vừa hết lượt hoặc không còn hiệu lực. Vui lòng bỏ chọn mã hoặc chọn mã khác.";
  }
  if (
    code === "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT" ||
    code === "SHOP_PROGRAM_PRICE_CHANGED" ||
    code === "SHOP_PROGRAM_ITEM_UNKNOWN"
  ) {
    return "Chương trình giảm giá của gian hàng vừa thay đổi hoặc hết lượt. Vui lòng kiểm tra lại giá và thử đặt lại.";
  }
  if (code === "INSUFFICIENT_COINS") {
    return "Số dư Xu hiện không đủ để áp dụng cho đơn hàng này.";
  }
  if (code === "PAYMENT_NOT_INITIALIZED") {
    return "Đơn đang khởi tạo thanh toán, vui lòng thử lại sau ít phút.";
  }
  if (
    lower.includes("pending suspension approval already exists") ||
    (lower.includes("suspension approval") && lower.includes("already exists"))
  ) {
    return "Gian hàng này đã có yêu cầu khoá đang chờ duyệt.";
  }
  if (
    code === "SHOP_SUSPENDED" ||
    lower.includes("shop is currently suspended") ||
    lower.includes("shop is suspended") ||
    lower.includes("shop_suspended")
  ) {
    return "Gian hàng hiện đang tạm ngưng hoạt động nên không thể tiếp tục thao tác.";
  }
  if (
    code === "PAYMENT_METHOD_DISABLED" ||
    lower.includes("payment_method_disabled") ||
    (lower.includes("payment method") && lower.includes("disabled"))
  ) {
    return "Phương thức thanh toán bạn chọn hiện đang tạm tắt. Vui lòng chọn phương thức khác.";
  }
  if (
    code === "ORDER_AMOUNT_OUT_OF_RANGE" ||
    lower.includes("order_amount_out_of_range") ||
    lower.includes("exceeds max_order_vnd") ||
    lower.includes("below min_order_vnd")
  ) {
    return "Giá trị đơn hàng nằm ngoài hạn mức cho phép của phương thức thanh toán đã chọn.";
  }
  if (
    code === "VERSION_CONFLICT" ||
    lower.includes("version_conflict") ||
    lower.includes("version conflict") ||
    lower.includes("expected_version")
  ) {
    return "Cấu hình đã được người khác cập nhật trước đó. Vui lòng tải lại dữ liệu mới nhất trước khi lưu.";
  }
  if (
    code === "REINDEX_ALREADY_RUNNING" ||
    lower.includes("reindex_already_running") ||
    lower.includes("another reindex")
  ) {
    return "Đang có tiến trình dựng lại chỉ mục tìm kiếm. Vui lòng chờ hoàn tất.";
  }
  if (
    code === "MAKER_CHECKER_CONFLICT" ||
    lower.includes("maker_checker_conflict") ||
    lower.includes("cannot approve own") ||
    lower.includes("self-approval") ||
    (lower.includes("maker") && lower.includes("checker"))
  ) {
    return "Người lập yêu cầu không thể tự phê duyệt (quy tắc duyệt 2 người).";
  }
  if (
    (lower.includes("cannot disable") || lower.includes("at least one")) &&
    lower.includes("payment method")
  ) {
    return "Phải giữ ít nhất một phương thức thanh toán đang bật.";
  }
  if (
    lower.includes("higher or equal role") ||
    lower.includes("insufficient role rank") ||
    lower.includes("cannot modify own")
  ) {
    return "Bạn không có quyền thao tác với tài khoản có vai trò ngang cấp hoặc cao hơn.";
  }
  if (code === "PROMOTION_BREAKDOWN_MISSING" || code === "RESERVATION_ALREADY_RELEASED") {
    return "Hệ thống khuyến mãi vừa cập nhật lại phiên tính giá. Vui lòng kiểm tra lại và đặt lại đơn.";
  }
  if (
    lower.includes("flash") &&
    (lower.includes("limit") ||
      lower.includes("exhausted") ||
      lower.includes("slot") ||
      lower.includes("ended") ||
      lower.includes("not active") ||
      lower.includes("not started"))
  ) {
    return "Suất Flash sale đã hết lượt, kết thúc hoặc bạn đã đạt giới hạn mua.";
  }
  if (lower.includes("voucher")) {
    return "Mã ưu đãi không hợp lệ hoặc đã hết lượt sử dụng.";
  }

  // Generic safe fallbacks by status/code — never echo raw internal gateway messages
  switch (code) {
    case "UNAUTHENTICATED":
      return "Vui lòng đăng nhập để tiếp tục.";
    case "PERMISSION_DENIED":
      return "Bạn không có quyền thực hiện thao tác này.";
    case "NOT_FOUND":
      return "Không tìm thấy thông tin yêu cầu.";
    case "ALREADY_EXISTS":
      return "Thông tin đã tồn tại trong hệ thống.";
    case "FAILED_PRECONDITION":
      return "Không thể thực hiện thao tác ở trạng thái hiện tại.";
    case "ABORTED":
      return "Dữ liệu vừa thay đổi, vui lòng tải lại và thử lại.";
    case "CANCELED":
    case "CANCELLED":
      return "Yêu cầu bị gián đoạn giữa chừng, vui lòng kiểm tra trạng thái trước khi thử lại.";
    case "RESOURCE_EXHAUSTED":
      return "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút.";
    case "INVALID_ARGUMENT":
    case "INVALID_JSON":
      return "Thông tin chưa hợp lệ, vui lòng kiểm tra lại.";
    default:
      if (status === 401) return "Vui lòng đăng nhập để tiếp tục.";
      if (status === 403) return "Bạn không có quyền thực hiện thao tác này.";
      if (status === 404) return "Không tìm thấy thông tin yêu cầu.";
      if (status === 409) return "Yêu cầu bị xung đột với trạng thái hiện tại.";
      if (status === 429) return "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút.";
      return "Yêu cầu chưa thể xử lý, vui lòng kiểm tra lại thông tin.";
  }
}

export function sanitizeGatewayError(
  status: number,
  body: unknown
): { status: number; code: string; message: string } {
  if (status === 504) {
    return { status: 504, code: "DEADLINE_EXCEEDED", message: UNAVAILABLE_MESSAGE };
  }
  if (status >= 500 || status <= 0) {
    return { status: 503, code: "UNAVAILABLE", message: UNAVAILABLE_MESSAGE };
  }

  const envelope = body as Partial<GatewayErrorEnvelope> | null;
  const rawCode = typeof envelope?.error?.code === "string" ? envelope.error.code.trim() : "";
  const baseCode = CODE_PATTERN.test(rawCode) ? rawCode : defaultCodeForStatus(status);
  const rawMessage = typeof envelope?.error?.message === "string" ? envelope.error.message : "";
  const code = normalizeDomainErrorCode(baseCode, rawMessage);

  return {
    status,
    code,
    message: toSafeVietnameseMessage(status, code, rawMessage),
  };
}
