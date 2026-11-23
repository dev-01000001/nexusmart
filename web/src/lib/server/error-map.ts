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
  "INSUFFICIENT_STOCK",
  "SKU_INACTIVE",
  "SKU_NOT_ACTIVE",
  "STOCK_VERSION_CONFLICT",
  "COD_LIMIT_EXCEEDED",
  "COD_REJECTED_LIMIT_EXCEEDED",
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
]);

function normalizeDomainErrorCode(rawCode: string, rawMessage: string): string {
  const prefixMatch = /^([A-Z][A-Z0-9_]+):/.exec(rawMessage.trim());
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
  if (lower.includes("phone is already registered")) {
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
  if (lower.includes("future date") || (lower.includes("date") && lower.includes("invalid"))) {
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

  // Commerce domain mappings (prioritize normalized domain codes from services/order, promotion, product)
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
