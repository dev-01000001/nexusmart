/**
 * Admin RBAC capability matrix, RoleRank hierarchy, route access guards, and two-person approval
 * helpers for Wave 3b-2 A7 (F9, F9-bis, F10, F11, F15) and multi-tenant Site management.
 *
 * Mirrors pkg/common/auth/capabilities.json 1:1.
 */

import type { UserRole } from "./gateway-types";

export const ADMIN_STAFF_ROLES = [
  "super_admin",
  "admin",
  "moderator",
  "cskh",
  "accountant",
  "ops",
] as const;

export type AdminStaffRole = (typeof ADMIN_STAFF_ROLES)[number];

export const CREATABLE_STAFF_ROLES = ["admin", "moderator", "cskh", "accountant", "ops"] as const;

export type CreatableStaffRole = (typeof CREATABLE_STAFF_ROLES)[number];

export const ALL_USER_ROLES: readonly UserRole[] = [
  "buyer",
  "seller",
  "moderator",
  "cskh",
  "accountant",
  "ops",
  "admin",
  "super_admin",
];

/**
 * Roles assignable via PATCH /api/v1/admin/users/{id}/role (mirrors services/user governance.go —
 * `super_admin` is explicitly forbidden as a target role).
 */
export const ASSIGNABLE_USER_ROLES: readonly UserRole[] = [
  "buyer",
  "seller",
  "moderator",
  "cskh",
  "accountant",
  "ops",
  "admin",
];

export const USER_ROLE_LABELS: Record<UserRole, string> = {
  super_admin: "Siêu quản trị",
  admin: "Quản trị viên",
  moderator: "Kiểm duyệt viên",
  cskh: "Chăm sóc khách hàng",
  accountant: "Kế toán",
  ops: "Vận hành",
  seller: "Người bán",
  buyer: "Người mua",
};

const ROLE_RANK: Record<string, number> = {
  super_admin: 40,
  admin: 30,
  moderator: 20,
  cskh: 20,
  accountant: 20,
  ops: 20,
  seller: 10,
  buyer: 10,
};

/**
 * Canonical capability table mirrored 1:1 from pkg/common/auth/capabilities.json.
 * Per F9-bis, `coins.credit` is strictly restricted to super_admin, admin, and accountant.
 */
export const CAPABILITY_MATRIX = {
  "users.read": ["super_admin", "admin", "moderator", "cskh", "accountant", "ops"],
  "users.status.write": ["super_admin", "admin", "cskh", "ops"],
  "users.role.write": ["super_admin"],
  "staff.create": ["super_admin"],
  "shops.read": ["super_admin", "admin", "moderator", "cskh", "accountant", "ops"],
  "shops.review": ["super_admin", "admin", "moderator", "ops"],
  "shops.suspend.request": ["super_admin", "admin", "moderator", "ops"],
  "shops.suspend.decide": ["super_admin", "admin", "ops"],
  "shops.unsuspend": ["super_admin", "admin", "ops"],
  "shops.approvals.read": ["super_admin", "admin", "moderator", "ops"],
  "shops.bank.read": ["super_admin", "admin", "accountant", "ops"],
  "shops.bank.review": ["super_admin", "admin", "accountant"],
  "sites.read": ["super_admin", "admin", "moderator", "cskh", "ops"],
  "sites.suspend": ["super_admin", "admin", "moderator", "ops"],
  "sites.domains.write": ["super_admin", "admin", "ops"],
  "audit_logs.read": ["super_admin", "admin", "moderator", "cskh", "accountant", "ops"],
  "categories.read": ["super_admin", "admin", "moderator", "accountant", "ops"],
  "categories.write": ["super_admin", "admin", "ops"],
  "fees.category.read": ["super_admin", "admin", "accountant", "ops"],
  "fees.category.write": ["super_admin", "admin", "accountant"],
  "products.read": ["super_admin", "admin", "moderator", "cskh", "ops"],
  "products.review": ["super_admin", "admin", "moderator"],
  "banned_keywords.read": ["super_admin", "admin", "moderator", "ops"],
  "banned_keywords.write": ["super_admin", "admin", "moderator"],
  "vouchers.read": ["super_admin", "admin", "cskh", "accountant", "ops"],
  "vouchers.write": ["super_admin", "admin", "ops"],
  "flash_sales.read": ["super_admin", "admin", "moderator", "ops"],
  "flash_sales.write": ["super_admin", "admin", "ops"],
  "flash_sales.items.approve": ["super_admin", "admin", "moderator", "ops"],
  "coins.credit": ["super_admin", "admin", "accountant"],
  "orders.read": ["super_admin", "admin", "cskh", "accountant", "ops"],
  "orders.review.resolve": ["super_admin", "admin", "ops"],
  "reports.read": ["super_admin", "admin", "accountant", "ops"],
  "returns.read": ["super_admin", "admin", "cskh", "accountant", "ops"],
  "returns.resolve": ["super_admin", "admin", "cskh", "ops"],
  "fees.payment.read": ["super_admin", "admin", "accountant", "ops"],
  "fees.payment.write": ["super_admin", "admin", "accountant"],
  "ledger.read": ["super_admin", "admin", "accountant", "ops"],
  "reconciliation.write": ["super_admin", "admin", "accountant"],
  "escrow.release": ["super_admin", "admin", "accountant"],
  "refunds.read": ["super_admin", "admin", "cskh", "accountant", "ops"],
  "refunds.create": ["super_admin", "admin", "cskh", "accountant"],
  "refunds.approve": ["super_admin", "admin", "accountant"],
  "refunds.settle": ["super_admin", "admin", "accountant"],
  "payouts.read": ["super_admin", "admin", "accountant", "ops"],
  "payouts.decide": ["super_admin", "admin", "accountant"],
  "carriers.read": ["super_admin", "admin", "ops"],
  "carriers.write": ["super_admin", "admin", "ops"],
  "payment_methods.read": ["super_admin", "admin", "accountant", "ops"],
  "payment_methods.write": ["super_admin", "admin", "ops"],
  "approvals.read": ["super_admin", "admin", "moderator", "cskh", "accountant", "ops"],
  "search.reindex": ["super_admin", "admin", "ops"],
} as const satisfies Record<string, readonly AdminStaffRole[]>;

export type AdminCapability = keyof typeof CAPABILITY_MATRIX;

export function normalizeRole(role: string | null | undefined): string {
  return (role ?? "").trim().toLowerCase();
}

export function isAdminStaffRole(role: string | null | undefined): role is AdminStaffRole {
  const norm = normalizeRole(role);
  return (ADMIN_STAFF_ROLES as readonly string[]).includes(norm);
}

export function getUserRoleLabel(role: string | null | undefined): string {
  const norm = normalizeRole(role) as UserRole;
  return USER_ROLE_LABELS[norm] ?? (role || "Chưa xác định");
}

export function getRoleRank(role: string | null | undefined): number {
  return ROLE_RANK[normalizeRole(role)] ?? 0;
}

/**
 * F10 RoleRank enforcement: actor may only modify/lock targets with strictly lower rank.
 */
export function canActOnTargetRole(
  actorRole: string | null | undefined,
  targetRole: string | null | undefined
): boolean {
  const actorRank = getRoleRank(actorRole);
  const targetRank = getRoleRank(targetRole);
  return actorRank > 0 && targetRank > 0 && actorRank > targetRank;
}

export function roleHasCapability(
  role: string | null | undefined,
  capability: AdminCapability
): boolean {
  const norm = normalizeRole(role);
  if (!norm) return false;
  const allowed = CAPABILITY_MATRIX[capability] as readonly string[] | undefined;
  return Boolean(allowed && allowed.includes(norm));
}

export function roleHasAnyCapability(
  role: string | null | undefined,
  capabilities: readonly AdminCapability[]
): boolean {
  return capabilities.some((cap) => roleHasCapability(role, cap));
}

/**
 * Route-level capability check used by AdminShell BEFORE rendering children (F15 zero-403 guard).
 * Default-deny: every known route under app/(admin)/admin/ must be explicitly listed below.
 */
export function canAccessAdminPath(
  role: string | null | undefined,
  pathname: string | null | undefined
): boolean {
  if (!isAdminStaffRole(role)) return false;
  const normRole = normalizeRole(role);
  const clean = (pathname ?? "").trim().replace(/\/+$/, "") || "/admin";

  if (clean === "/admin") {
    return true;
  }
  if (clean === "/admin/gian-hang" || clean.startsWith("/admin/gian-hang/")) {
    return roleHasCapability(normRole, "shops.read");
  }
  if (clean === "/admin/sites" || clean.startsWith("/admin/sites/")) {
    return roleHasCapability(normRole, "sites.read");
  }
  if (clean === "/admin/kiem-duyet" || clean.startsWith("/admin/kiem-duyet/")) {
    return roleHasAnyCapability(normRole, ["products.read", "banned_keywords.read"]);
  }
  if (clean === "/admin/flash-sale" || clean.startsWith("/admin/flash-sale/")) {
    return roleHasAnyCapability(normRole, ["flash_sales.read", "flash_sales.items.approve"]);
  }
  if (clean === "/admin/khieu-nai" || clean.startsWith("/admin/khieu-nai/")) {
    return roleHasCapability(normRole, "returns.read");
  }
  if (clean === "/admin/doi-soat" || clean.startsWith("/admin/doi-soat/")) {
    return roleHasAnyCapability(normRole, [
      "ledger.read",
      "orders.read",
      "refunds.read",
      "payouts.read",
    ]);
  }
  if (clean === "/admin/cau-hinh" || clean.startsWith("/admin/cau-hinh/")) {
    return roleHasAnyCapability(normRole, [
      "payment_methods.read",
      "approvals.read",
      "fees.category.read",
      "fees.payment.read",
      "categories.read",
      "users.read",
      "carriers.read",
      "search.reindex",
    ]);
  }
  if (clean === "/admin/audit-log" || clean.startsWith("/admin/audit-log/")) {
    return roleHasCapability(normRole, "audit_logs.read");
  }
  return false;
}

/**
 * F11 Two-person approval (maker != checker) UI check.
 */
export function canDecideTwoPersonApproval(input: {
  actorId: string | null | undefined;
  actorRole: string | null | undefined;
  requestedBy: string | null | undefined;
  decideCapability: "shops.suspend.decide" | "refunds.approve";
}): boolean {
  const actorId = (input.actorId ?? "").trim();
  const makerId = (input.requestedBy ?? "").trim();
  if (!actorId || !roleHasCapability(input.actorRole, input.decideCapability)) {
    return false;
  }
  if (makerId && actorId === makerId) {
    return false;
  }
  return true;
}
