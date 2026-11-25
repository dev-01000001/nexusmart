// Pure query builder, filter, and label helpers for Admin Audit Log (/admin/audit-log).
// Zero I/O, zero DOM, safe for server components, route handlers, client components, and Node tests.
import type { GwAuditLog } from "./adminApiTypes.ts";

const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Normalizes a date (`YYYY-MM-DD`, interpreted in Asia/Ho_Chi_Minh UTC+7) or RFC3339 timestamp
 * into an RFC3339 string for gateway `ListAuditLogs` (`from_time`/`to_time`).
 * Returns `""` when empty, or `null` when malformed.
 */
export function normalizeAuditTimeParam(
  raw: string | null | undefined,
  bound: "start" | "end"
): string | null {
  const trimmed = (raw ?? "").trim();
  if (!trimmed) return "";
  if (ISO_DATE_RE.test(trimmed)) {
    const suffix = bound === "start" ? "T00:00:00+07:00" : "T23:59:59+07:00";
    const candidate = `${trimmed}${suffix}`;
    const parsed = Date.parse(candidate);
    if (!Number.isFinite(parsed)) return null;
    // Verify calendar date did not roll over (e.g. 2026-02-30).
    const [y, m, d] = trimmed.split("-").map((v) => Number.parseInt(v, 10));
    const check = new Date(Date.UTC(y, m - 1, d));
    if (check.getUTCFullYear() !== y || check.getUTCMonth() + 1 !== m || check.getUTCDate() !== d) {
      return null;
    }
    return candidate;
  }
  const ms = Date.parse(trimmed);
  if (!Number.isFinite(ms)) return null;
  return new Date(ms).toISOString();
}

export interface AuditLogFilterInput {
  targetType?: string;
  actorId?: string;
  targetId?: string;
  fromDate?: string;
  toDate?: string;
  page?: number;
  pageSize?: number;
}

export function buildAuditLogsQueryParams(filters: AuditLogFilterInput): URLSearchParams {
  const qs = new URLSearchParams();
  const targetType = (filters.targetType ?? "").trim();
  const actorId = (filters.actorId ?? "").trim();
  const targetId = (filters.targetId ?? "").trim();
  if (targetType) qs.set("target_type", targetType);
  if (actorId) qs.set("actor_id", actorId);
  if (targetId) qs.set("target_id", targetId);

  const fromTime = normalizeAuditTimeParam(filters.fromDate, "start");
  if (fromTime) qs.set("from_time", fromTime);
  const toTime = normalizeAuditTimeParam(filters.toDate, "end");
  if (toTime) qs.set("to_time", toTime);

  const page = Math.max(1, Math.trunc(filters.page ?? 1));
  const pageSize = Math.max(1, Math.min(100, Math.trunc(filters.pageSize ?? 20)));
  qs.set("page", String(page));
  qs.set("page_size", String(pageSize));
  return qs;
}

export const ADMIN_AUDIT_ACTION_LABELS: Record<string, string> = {
  "admin.shop.review": "Thẩm định hồ sơ gian hàng",
  "admin.shop.suspend": "Tạm ngưng gian hàng",
  "admin.shop.unsuspend": "Mở khóa gian hàng",
  "admin.shop.bank.review": "Duyệt tài khoản ngân hàng gian hàng",
  "admin.user.status": "Cập nhật trạng thái tài khoản",
  "admin.user.role": "Thay đổi phân quyền tài khoản",
  "admin.staff.create": "Tạo tài khoản nhân viên nội bộ",
  "admin.site.suspend": "Tạm khóa site gian hàng",
  "admin.site.unsuspend": "Mở khóa site gian hàng",
  "site.save_draft": "Lưu bản nháp site",
  "site.publish": "Xuất bản site gian hàng",
  "site.rollback": "Khôi phục phiên bản site",
  "admin.product.review": "Kiểm duyệt sản phẩm",
  "admin.banned_keyword.create": "Thêm từ khoá cấm",
  "admin.banned_keyword.update": "Cập nhật từ khoá cấm",
  "admin.banned_keyword.delete": "Xóa từ khoá cấm",
  "admin.banned_keyword.rescan": "Quét lại sản phẩm theo từ khoá cấm",
  "admin.return.resolve": "Phân xử yêu cầu trả hàng / khiếu nại",
  "admin.order.resolve_review": "Xử lý đơn cần rà soát",
  "admin.reconcile.run": "Chạy đối soát giao dịch",
  "admin.reconcile.cod_import": "Nhập tệp CSV đối soát COD",
  "admin.reconcile.queue.resolve": "Xử lý giao dịch không khớp",
  "admin.refund.create": "Tạo lệnh hoàn tiền thủ công",
  "admin.refund.approve": "Phê duyệt lệnh hoàn tiền 2 người",
  "admin.refund.reject": "Từ chối lệnh hoàn tiền 2 người",
  "admin.refund.settle": "Tất toán chuyển khoản hoàn tiền",
  "admin.flash_sale.create": "Mở khung giờ Flash sale",
  "admin.flash_sale.end": "Kết thúc sớm khung giờ Flash sale",
  "admin.flash_sale_item.approve": "Duyệt suất Flash sale",
  "admin.category.create": "Tạo danh mục ngành hàng",
  "admin.category.update": "Cập nhật danh mục ngành hàng",
  "admin.voucher.create": "Tạo voucher sàn",
};

export function getAdminAuditActionLabelVi(action: string): string {
  const key = (action ?? "").trim();
  if (!key) return "Thao tác quản trị";
  return ADMIN_AUDIT_ACTION_LABELS[key] ?? key;
}

export function filterAuditLogsClientSide(
  logs: readonly GwAuditLog[],
  opts: { actionQuery?: string; roleFilter?: string }
): GwAuditLog[] {
  const actionQ = (opts.actionQuery ?? "").trim().toLowerCase();
  const role = (opts.roleFilter ?? "").trim().toLowerCase();
  return logs.filter((log) => {
    if (role && role !== "all" && log.actor_role.toLowerCase() !== role) {
      return false;
    }
    if (!actionQ) return true;
    return (
      log.action.toLowerCase().includes(actionQ) ||
      getAdminAuditActionLabelVi(log.action).toLowerCase().includes(actionQ) ||
      (log.reason ?? "").toLowerCase().includes(actionQ) ||
      log.target_id.toLowerCase().includes(actionQ) ||
      log.actor_id.toLowerCase().includes(actionQ)
    );
  });
}
