import type {
  GatewayShop,
  GatewayUser,
  PaginatedResponse,
  ProductStatus,
  ShopStatus,
  UserRole,
  UserStatus,
} from "@/lib/gateway-types";
import { getErrorMessage } from "../../lib/api.ts";
import { GATEWAY_ORDER_STATUS_LABELS } from "../../lib/format.ts";
import { getAdminOrderStatusMeta as getCanonicalAdminOrderStatusMeta } from "./adminApiTypes.ts";
import type { PillTone } from "./adminMeta.ts";

export const ADMIN_SHOP_STATUS_META: Record<
  ShopStatus | string,
  { label: string; tone: PillTone }
> = {
  PENDING: { label: "Chờ duyệt", tone: "warning" },
  ACTIVE: { label: "Đang hoạt động", tone: "positive" },
  REJECTED: { label: "Từ chối", tone: "negative" },
  SUSPENDED: { label: "Tạm ngưng", tone: "negative" },
};

export const ADMIN_PRODUCT_STATUS_META: Record<
  ProductStatus | string,
  { label: string; tone: PillTone }
> = {
  PENDING: { label: "Chờ duyệt", tone: "warning" },
  APPROVED: { label: "Đã duyệt", tone: "positive" },
  REJECTED: { label: "Từ chối", tone: "negative" },
  HIDDEN: { label: "Đang ẩn", tone: "neutral" },
  OUT_OF_STOCK: { label: "Hết hàng", tone: "negative" },
};

export const ADMIN_USER_STATUS_META: Record<
  UserStatus | string,
  { label: string; tone: PillTone }
> = {
  ACTIVE: { label: "Hoạt động", tone: "positive" },
  PENDING_VERIFICATION: { label: "Chờ xác thực", tone: "warning" },
  LOCKED: { label: "Đã khóa", tone: "negative" },
};

export const ADMIN_USER_ROLE_LABELS: Record<UserRole | string, string> = {
  buyer: "Người mua",
  seller: "Người bán",
  moderator: "Kiểm duyệt viên",
  cskh: "Chăm sóc khách hàng",
  accountant: "Kế toán",
  ops: "Vận hành",
  admin: "Quản trị viên",
  super_admin: "Siêu quản trị",
};

export const ADMIN_ORDER_STATUS_META: Record<string, { label: string; tone: PillTone }> = {
  PENDING: { label: GATEWAY_ORDER_STATUS_LABELS.PENDING, tone: "warning" },
  CONFIRMED: { label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED, tone: "warning" },
  PREPARING: { label: GATEWAY_ORDER_STATUS_LABELS.PREPARING, tone: "neutral" },
  SHIPPING: { label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING, tone: "neutral" },
  DELIVERED: { label: GATEWAY_ORDER_STATUS_LABELS.DELIVERED, tone: "positive" },
  COMPLETED: { label: GATEWAY_ORDER_STATUS_LABELS.COMPLETED, tone: "positive" },
  CANCELLED: { label: GATEWAY_ORDER_STATUS_LABELS.CANCELLED, tone: "negative" },
  RETURN_REQUESTED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURN_REQUESTED, tone: "warning" },
  RETURNED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURNED, tone: "negative" },
};

export const RECONCILE_ITEM_STATUS_META: Record<string, { label: string; tone: PillTone }> = {
  MATCHED: { label: "Khớp", tone: "positive" },
  ALREADY_RECONCILED: { label: "Đã đối soát trước đó", tone: "neutral" },
  MISSING_LOCAL: { label: "Thiếu trên sàn", tone: "negative" },
  MISSING_PROVIDER: { label: "Thiếu ở đối tác", tone: "warning" },
  AMOUNT_MISMATCH: { label: "Lệch số tiền", tone: "negative" },
  NOT_DELIVERED: { label: "Chưa giao hàng", tone: "warning" },
};

export function getReconciliationQueueStatusMeta(status: string): {
  label: string;
  tone: PillTone;
} {
  switch ((status ?? "").trim().toLowerCase()) {
    case "pending":
      return { label: "Chờ xử lý", tone: "warning" };
    case "suspended":
      return { label: "Đang tạm treo", tone: "warning" };
    case "assigned":
      return { label: "Đã gán khớp", tone: "positive" };
    case "refunded":
      return { label: "Đã tạo hoàn tiền", tone: "positive" };
    default:
      return { label: "Đang cập nhật", tone: "neutral" };
  }
}

/**
 * Target types aligned 1:1 with `auditTargetType()` in `services/gateway/internal/middleware/audit.go`
 * and transactional `InsertAuditLog` writes in `services/user`.
 */
export const ADMIN_AUDIT_TARGET_TYPE_LABELS: Record<string, string> = {
  shop: "Gian hàng",
  site: "Site gian hàng",
  domain: "Tên miền riêng",
  shop_bank_account: "Tài khoản ngân hàng gian hàng",
  shop_suspension_approval: "Phê duyệt khóa gian hàng",
  user: "Người dùng & nhân sự",
  product: "Sản phẩm",
  category: "Danh mục",
  banned_keyword: "Từ khóa cấm",
  voucher: "Mã giảm giá",
  flash_sale: "Flash sale",
  flash_sale_item: "Sản phẩm Flash sale",
  coin: "Xu thành viên",
  order: "Đơn hàng",
  payment: "Thanh toán",
  refund: "Hoàn tiền",
  reconciliation_item: "Đối soát giao dịch",
  reconciliation_run: "Phiên đối soát COD",
  return_request: "Trả hàng / khiếu nại",
  carrier: "Hãng vận chuyển",
  payment_method_config: "Phương thức thanh toán",
  category_fee_schedule: "Biểu phí ngành hàng",
  payment_fee_config: "Cấu hình phí thanh toán",
  search_reindex: "Đồng bộ chỉ mục tìm kiếm",
  admin: "Hệ thống quản trị",
};

const LEGACY_AUDIT_TARGET_TYPE_LABELS: Record<string, string> = {
  payment_method: "Phương thức thanh toán",
  shop_approval: "Phê duyệt khóa gian hàng",
  staff: "Nhân sự nội bộ",
};

export function getAdminAuditTargetTypeLabel(targetType: string): string {
  const key = (targetType ?? "").trim().toLowerCase();
  if (!key) return "Hệ thống quản trị";
  return (
    ADMIN_AUDIT_TARGET_TYPE_LABELS[key] ??
    LEGACY_AUDIT_TARGET_TYPE_LABELS[key] ??
    "Hệ thống quản trị"
  );
}

export function getAdminReturnStatusMeta(status: string): { label: string; tone: PillTone } {
  const key = (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^RETURN_STATUS_/, "");
  switch (key) {
    case "PENDING":
      return { label: "Chờ gian hàng", tone: "warning" };
    case "AWAITING_SHIPMENT":
      return { label: "Chờ gửi trả", tone: "warning" };
    case "IN_TRANSIT":
      return { label: "Đang hoàn về", tone: "neutral" };
    case "RECEIVED":
      return { label: "Shop đã nhận", tone: "neutral" };
    case "REFUNDING":
      return { label: "Đang hoàn tiền", tone: "warning" };
    case "REFUNDED":
      return { label: "Đã hoàn tiền", tone: "positive" };
    case "REJECTED":
      return { label: "Shop từ chối", tone: "negative" };
    case "ESCALATED":
      return { label: "Cần phân xử", tone: "negative" };
    case "EVIDENCE_REQUESTED":
      return { label: "Chờ bằng chứng", tone: "warning" };
    case "CLOSED":
      return { label: "Đã đóng", tone: "neutral" };
    default:
      return { label: "Đang xử lý", tone: "neutral" };
  }
}

function normalizeAdminRefundStatus(status: string | null | undefined): string {
  return (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^REFUND_STATUS_/, "");
}

export function isAdminWalletRefund(
  refund?: { provider_m_refund_id?: string | null } | null
): boolean {
  return Boolean((refund?.provider_m_refund_id ?? "").trim());
}

export function isAdminWalletRefundAutoPending(
  refund?: { status?: string | null; provider_m_refund_id?: string | null } | null
): boolean {
  return normalizeAdminRefundStatus(refund?.status) === "PENDING" && isAdminWalletRefund(refund);
}

/**
 * Manual accountant settlement is allowed ONLY when:
 * - status is MANUAL_REVIEW (any method, including failed wallet auto-refunds), OR
 * - status is PENDING and NOT a ZaloPay wallet refund (provider_m_refund_id is empty).
 */
export function isAdminRefundManuallySettleable(
  refund?: { status?: string | null; provider_m_refund_id?: string | null } | null
): boolean {
  const key = normalizeAdminRefundStatus(refund?.status);
  if (key === "MANUAL_REVIEW") {
    return true;
  }
  if (key === "PENDING") {
    return !isAdminWalletRefund(refund);
  }
  return false;
}

export function getAdminRefundStatusMeta(
  status: string,
  refund?: { provider_m_refund_id?: string | null; bank_code?: string | null } | null
): { label: string; tone: PillTone } {
  const key = normalizeAdminRefundStatus(status);
  const wallet = isAdminWalletRefund(refund);
  const settledViaBank = Boolean((refund?.bank_code ?? "").trim());
  if (key === "DONE" || key === "COMPLETED") {
    if (wallet && !settledViaBank) {
      return { label: "Đã hoàn về ví ZaloPay", tone: "positive" };
    }
    return { label: "Đã tất toán", tone: "positive" };
  }
  if (key === "FAILED") {
    return { label: "Thất bại", tone: "negative" };
  }
  if (key === "PENDING_APPROVAL") {
    return { label: "Chờ duyệt 2 người", tone: "warning" };
  }
  if (key === "REJECTED") {
    return { label: "Từ chối duyệt", tone: "negative" };
  }
  if (key === "MANUAL_REVIEW") {
    return { label: "Cần xử lý tay", tone: "warning" };
  }
  if (key === "PENDING") {
    if (wallet) {
      return { label: "ZaloPay đang hoàn tự động", tone: "warning" };
    }
    return { label: "Chờ chuyển khoản", tone: "warning" };
  }
  return { label: "Đang cập nhật", tone: "neutral" };
}

export const SHIPPING_REGION_LABELS: Record<string, string> = {
  SAME_PROVINCE: "Nội tỉnh / thành phố",
  SAME_REGION: "Nội miền",
  CROSS_REGION: "Liên miền",
};

export function getAdminShopStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_SHOP_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getAdminProductStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_PRODUCT_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getAdminUserStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_USER_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getAdminOrderStatusMeta(status: string): { label: string; tone: PillTone } {
  return getCanonicalAdminOrderStatusMeta(status);
}

export function formatCommissionBps(bps?: number): string {
  const safe = typeof bps === "number" && Number.isFinite(bps) ? bps : 0;
  const pct = safe / 100;
  return `${pct.toFixed(1).replace(".", ",")}%`;
}

export const ADMIN_DATA_CHANGED_MESSAGE =
  "Dữ liệu đã thay đổi. Hệ thống đã tải lại thông tin mới nhất, vui lòng kiểm tra lại trước khi thao tác.";

/**
 * Returns true when an admin shop or bank-account review fails with an optimistic-concurrency
 * conflict (HTTP 409, OWNER_NAME_CHANGED, or stale PENDING/PENDING_ADMIN_REVIEW row).
 */
export function isAdminReviewConflictError(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const e = err as { status?: unknown; code?: unknown };
  if (e.status === 409) return true;
  const code = typeof e.code === "string" ? e.code.trim().toUpperCase() : "";
  return (
    code === "OWNER_NAME_CHANGED" ||
    code === "FAILED_PRECONDITION" ||
    code === "CONFLICT" ||
    code === "ABORTED"
  );
}

export interface AdminShopOwnerState {
  loading: boolean;
  user: GatewayUser | null;
  error?: string;
}

export function getDisplayedOwnerFullName(state?: AdminShopOwnerState | null): string {
  return (state?.user?.full_name ?? "").trim();
}

/**
 * Matches backend `services/user/internal/service/server.go` (`AdminReviewShop`):
 * only shops in `PENDING` status can be reviewed (APPROVE, REJECT, or REQUEST_INFO).
 */
export function canReviewShopStatus(shopStatus: string): boolean {
  return (shopStatus ?? "").trim().toUpperCase() === "PENDING";
}

/**
 * APPROVE is only enabled for `PENDING` shops once the shop owner's full_name has been fetched
 * and displayed on the review panel (D6), preventing approval with an empty `expected_owner_name`.
 */
export function canApproveShop(
  shopStatus: string,
  ownerState?: AdminShopOwnerState | null
): boolean {
  if (!canReviewShopStatus(shopStatus)) return false;
  if (ownerState?.loading) return false;
  return getDisplayedOwnerFullName(ownerState).length > 0;
}

export interface AdminShopReviewPayload {
  decision: "APPROVE" | "REQUEST_INFO" | "REJECT";
  reason: string;
  expected_owner_name: string;
}

/**
 * Builds the POST /admin/shops/{id}/review request body.
 * APPROVE strictly requires non-empty `expectedOwnerName` matching the name displayed to the admin
 * (D6 optimistic guard); REJECT and REQUEST_INFO do not require an owner name.
 */
export function buildAdminShopReviewPayload(input: {
  decision: "APPROVE" | "REQUEST_INFO" | "REJECT";
  reason: string;
  expectedOwnerName?: string;
}): AdminShopReviewPayload {
  const note = input.reason.trim();
  const trimmedOwner = (input.expectedOwnerName ?? "").trim();
  if (input.decision === "APPROVE" && !trimmedOwner) {
    throw new Error("expected_owner_name is required when approving a shop");
  }
  return {
    decision: input.decision,
    reason:
      note ||
      (input.decision === "APPROVE" ? "Hồ sơ gian hàng đầy đủ, đã phê duyệt hoạt động." : ""),
    expected_owner_name: input.decision === "APPROVE" ? trimmedOwner : "",
  };
}

export type AdminShopReviewOutcome =
  | { status: "blocked_missing_owner" }
  | { status: "ok"; shop?: GatewayShop }
  | { status: "conflict"; message: string };

/**
 * Coordinates a single admin shop review submission without re-fetching owner details right before
 * posting: APPROVE sends the exact owner name currently displayed on the panel, blocks with zero
 * network calls if missing, and on 409 reloads fresh data so the admin must inspect and click again.
 */
export async function submitAdminShopReviewDecision(input: {
  shopId: string;
  decision: "APPROVE" | "REQUEST_INFO" | "REJECT";
  reason: string;
  displayedOwnerName: string;
  postReview: (path: string, payload: AdminShopReviewPayload) => Promise<{ shop?: GatewayShop }>;
  onConflictReload: () => Promise<void>;
}): Promise<AdminShopReviewOutcome> {
  const trimmedOwner = (input.displayedOwnerName ?? "").trim();
  if (input.decision === "APPROVE" && !trimmedOwner) {
    return { status: "blocked_missing_owner" };
  }

  const payload = buildAdminShopReviewPayload({
    decision: input.decision,
    reason: input.reason,
    expectedOwnerName: input.decision === "APPROVE" ? trimmedOwner : "",
  });

  try {
    const res = await input.postReview(
      `admin/shops/${encodeURIComponent(input.shopId)}/review`,
      payload
    );
    return { status: "ok", shop: res?.shop };
  } catch (err) {
    if (isAdminReviewConflictError(err)) {
      const rawMsg = getErrorMessage(err, ADMIN_DATA_CHANGED_MESSAGE);
      const message = rawMsg.toLowerCase().includes("dữ liệu đã thay đổi")
        ? rawMsg
        : "Dữ liệu đã thay đổi — họ tên chủ gian hàng hoặc trạng thái hồ sơ vừa được cập nhật. Hệ thống đã tải lại thông tin mới nhất, vui lòng kiểm tra lại.";
      await input.onConflictReload();
      return { status: "conflict", message };
    }
    throw err;
  }
}

export interface AdminBankReviewPayload {
  bank_account_id: string;
  decision: "APPROVE" | "REJECT";
  reason: string;
}

/**
 * Builds the POST /admin/shops/{id}/bank-account/review request body, enforcing required
 * `bank_account_id` (D6 guard against approving a superseded bank account row).
 */
export function buildAdminBankReviewPayload(input: {
  bankAccountId: string;
  decision: "APPROVE" | "REJECT";
  reason: string;
}): AdminBankReviewPayload {
  const bankAccountId = (input.bankAccountId ?? "").trim();
  if (!bankAccountId) {
    throw new Error("bank_account_id is required");
  }
  const note = input.reason.trim();
  return {
    bank_account_id: bankAccountId,
    decision: input.decision,
    reason:
      note || (input.decision === "APPROVE" ? "Đã đối chiếu giấy tờ chủ tài khoản ngân hàng." : ""),
  };
}

/**
 * Real activity event codes emitted by services/user/internal/service/governance.go (F13),
 * mapped to Vietnamese labels for AdminUsersPanel.
 */
export const USER_ACTIVITY_EVENT_LABELS: Record<string, string> = {
  login_succeeded: "Đăng nhập thành công",
  login_success: "Đăng nhập thành công",
  login_failed: "Đăng nhập thất bại",
  logout: "Đăng xuất",
  password_changed: "Đổi mật khẩu",
  password_reset_requested: "Yêu cầu đặt lại mật khẩu",
  password_reset_sent: "Đã gửi thư đặt lại mật khẩu",
  password_reset_failed: "Gửi thư đặt lại mật khẩu thất bại",
  password_reset_completed: "Hoàn tất đặt lại mật khẩu",
  profile_updated: "Cập nhật hồ sơ",
  phone_otp_requested: "Yêu cầu mã OTP xác minh SĐT",
  phone_otp_verified: "Xác minh OTP SĐT thành công",
  phone_otp_failed: "Xác minh OTP SĐT thất bại",
  phone_verified: "Xác minh số điện thoại",
  status_changed: "Thay đổi trạng thái tài khoản",
  role_changed: "Thay đổi vai trò",
  staff_created: "Tạo tài khoản nhân viên",
  shop_suspended: "Tạm ngưng gian hàng",
  shop_unsuspended: "Mở khóa gian hàng",
};

export function formatUserActivityEventType(raw: string | null | undefined): string {
  const key = (raw ?? "").trim().toLowerCase();
  if (!key) return "Hoạt động tài khoản";
  return USER_ACTIVITY_EVENT_LABELS[key] ?? (raw || "Hoạt động tài khoản");
}

export interface ApprovalActorLookupEntry {
  full_name?: string | null;
  email?: string | null;
  role?: string | null;
}

/**
 * Formats a two-person approval maker/checker actor for display.
 * Uses resolved user full_name (or email) + Vietnamese role label when available,
 * and falls back to an 8-character short ID + role label so raw 36-char UUIDs never leak to UI.
 */
export function formatApprovalActorDisplay(
  userId: string | null | undefined,
  roleHint?: string | null,
  userById?: Record<string, ApprovalActorLookupEntry | null | undefined>
): string {
  const cleanId = (userId ?? "").trim();
  if (!cleanId) return "—";

  const resolved = userById?.[cleanId] ?? null;
  const resolvedRole = (roleHint ?? resolved?.role ?? "").trim().toLowerCase();
  const roleLabel = resolvedRole
    ? (ADMIN_USER_ROLE_LABELS[resolvedRole] ?? roleHint ?? resolved?.role ?? "")
    : "";

  const displayName = (resolved?.full_name ?? "").trim() || (resolved?.email ?? "").trim();
  if (displayName) {
    return roleLabel ? `${displayName} (${roleLabel})` : displayName;
  }

  const shortCode = cleanId.length > 12 ? `#${cleanId.slice(0, 8)}` : cleanId;
  return roleLabel ? `${shortCode} (${roleLabel})` : shortCode;
}

/**
 * Looks up a single shop owner by exact user ID via `GET /admin/users?query=<id>&page=1&page_size=1`,
 * verifying `item.id === ownerUserId` before returning.
 */
export async function fetchShopOwnerById(
  ownerUserId: string,
  fetchUserByQuery: (path: string) => Promise<PaginatedResponse<GatewayUser>>
): Promise<GatewayUser | null> {
  const cleanId = (ownerUserId ?? "").trim();
  if (!cleanId) return null;

  const path = `admin/users?query=${encodeURIComponent(cleanId)}&page=1&page_size=1`;
  const res = await fetchUserByQuery(path);
  const items = Array.isArray(res?.items) ? res.items : [];
  const exact = items.find((u) => u?.id === cleanId);
  return exact ?? null;
}
