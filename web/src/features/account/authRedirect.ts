// Pure post-login destination rules shared by the site and platform auth forms.
// Safe for client, server and `node --test`.
import { isAdminStaffRole } from "../../lib/admin-rbac.ts";
import { safeNextPath } from "./accountTypes.ts";

export type AuthVariant = "site" | "platform";

type AuthRole =
  | "buyer"
  | "seller"
  | "admin"
  | "super_admin"
  | "moderator"
  | "cskh"
  | "accountant"
  | "ops"
  | string;

function pathOf(target: string): string {
  return target.split(/[?#]/)[0] ?? "";
}

function within(pathname: string, prefix: string): boolean {
  return pathname === prefix || pathname.startsWith(`${prefix}/`);
}

export interface ResolvePostLoginOptions {
  /** True when the account owns a shop or has an in-progress shop onboarding application. */
  hasShop?: boolean;
}

const BUYER_PLATFORM_EXACT_PATHS = new Set([
  "/",
  "/cua-hang",
  "/ve-nexusmart",
  "/tro-giup",
  "/seller",
]);
const BUYER_PLATFORM_PREFIXES = ["/gian-hang", "/san-pham"] as const;

function isAllowedBuyerPlatformNext(pathname: string): boolean {
  if (BUYER_PLATFORM_EXACT_PATHS.has(pathname)) return true;
  return BUYER_PLATFORM_PREFIXES.some((prefix) => within(pathname, prefix));
}

/**
 * Where to send a user right after a successful login, OTP verification, or when visiting
 * `/dang-nhap` while already authenticated.
 *
 * - `site` (a shop's own host): the safe `next` path, else the buyer account page.
 * - `platform` (root host):
 *   - Admin staff roles -> safe `/admin/*` next, else `/admin`.
 *   - Sellers (or buyers with an active/pending/rejected shop onboarding) -> safe `/seller/*` next,
 *     else `/seller`.
 *   - Plain buyers -> safe same-host root-platform `next` (excluding `/admin*`, deep `/seller/*`,
 *     and site-only paths like `/tai-khoan`), else `buyerHref` (default `/`).
 */
export function resolvePostLoginPath(
  variant: AuthVariant,
  role: AuthRole,
  next: string | null | undefined,
  buyerHref = "/",
  opts?: ResolvePostLoginOptions
): string {
  if (variant === "site") {
    return safeNextPath(next, "/tai-khoan");
  }

  const safe = safeNextPath(next, "");
  const pathname = pathOf(safe);
  if (isAdminStaffRole(role)) {
    return safe && within(pathname, "/admin") ? safe : "/admin";
  }
  if (role === "seller" || Boolean(opts?.hasShop)) {
    return safe && within(pathname, "/seller") ? safe : "/seller";
  }
  if (safe && isAllowedBuyerPlatformNext(pathname)) {
    return safe;
  }
  return buyerHref;
}
