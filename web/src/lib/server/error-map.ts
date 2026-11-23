import type { GatewayErrorEnvelope } from "@/lib/gateway-types";

export const UNAVAILABLE_MESSAGE = "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.";
const CODE_PATTERN = /^[A-Z0-9_]{2,64}$/;
const CONTROL_CHAR_RE = /[\u0000-\u001f\u007f]/g;

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

  // Refund & Reconciliation domain mappings
  if (lower.includes("bank_account") || lower.includes("bank account")) {
    return "Thông tin tài khoản nhận hoàn tiền chưa hợp lệ (số tài khoản gồm 6–19 chữ số).";
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

  // Commerce domain mappings
  if (
    code === "INSUFFICIENT_STOCK" ||
    lower.includes("insufficient stock") ||
    lower.includes("out of stock")
  ) {
    return "Sản phẩm không đủ số lượng tồn kho.";
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
  if (status >= 500 || status <= 0) {
    return { status: 503, code: "UNAVAILABLE", message: UNAVAILABLE_MESSAGE };
  }

  const envelope = body as Partial<GatewayErrorEnvelope> | null;
  const rawCode = typeof envelope?.error?.code === "string" ? envelope.error.code.trim() : "";
  const code = CODE_PATTERN.test(rawCode) ? rawCode : defaultCodeForStatus(status);
  const rawMessage = typeof envelope?.error?.message === "string" ? envelope.error.message : "";

  return {
    status,
    code,
    message: toSafeVietnameseMessage(status, code, rawMessage),
  };
}
