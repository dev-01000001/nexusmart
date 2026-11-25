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

/**
 * Where to send a user right after a successful login or OTP verification.
 *
 * - `site` (a shop's own host): the safe `next` path, else the buyer account page.
 * - `platform` (root host): only seller and admin work happens here. All 6 admin staff roles go to
 *   `/admin`, sellers to `/seller` (a `next` inside their own area is kept). Buyers have nothing
 *   to do on the root host, so they are sent to `buyerHref` (the shop directory) whatever `next` says.
 */
export function resolvePostLoginPath(
  variant: AuthVariant,
  role: AuthRole,
  next: string | null | undefined,
  buyerHref = "/"
): string {
  if (variant === "site") {
    return safeNextPath(next, "/tai-khoan");
  }

  const safe = safeNextPath(next, "");
  const pathname = pathOf(safe);
  if (isAdminStaffRole(role)) {
    return safe && within(pathname, "/admin") ? safe : "/admin";
  }
  if (role === "seller") {
    return safe && within(pathname, "/seller") ? safe : "/seller";
  }
  return buyerHref;
}
