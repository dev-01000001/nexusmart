"use client";

import React, { createContext, useCallback, useContext, useMemo } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { AppShell, PageHeader, type AppAccount } from "@/components/app";
import { useSession } from "@/components/SessionProvider";
import { Button } from "@/components/ui/button";
import { canAccessAdminPath, getUserRoleLabel, roleHasCapability } from "@/lib/admin-rbac";
import type { GatewayUser, UserStatus } from "@/lib/gateway-types";
import type { SessionUser } from "@/lib/server/session";
import { AdminUserProvider } from "./AdminUserContext";
import {
  ADMIN_SIDEBAR_STORAGE_KEY,
  buildAdminCommandItems,
  buildAdminNavGroups,
} from "./shell/nav";

interface AdminContextValue {
  user: SessionUser;
  showToast: (message: string) => void;
}

const AdminContext = createContext<AdminContextValue | null>(null);

export function useAdminContext(): AdminContextValue {
  const ctx = useContext(AdminContext);
  if (!ctx) {
    throw new Error("useAdminContext phải được gọi bên trong AdminShell.");
  }
  return ctx;
}

function sessionUserToGatewayUser(user: SessionUser): GatewayUser {
  const statusUpper = (user.status ?? "").trim().toUpperCase();
  const status: UserStatus =
    statusUpper === "LOCKED" || statusUpper === "PENDING_VERIFICATION" ? statusUpper : "ACTIVE";
  return {
    id: user.id,
    full_name: user.fullName,
    email: user.email,
    phone: user.phone,
    role: user.role,
    status,
    shop_id: user.shopId,
    phone_verified: user.phoneVerified,
    cod_rejected_count: user.codRejectedCount,
    avatar_url: user.avatarUrl,
  };
}

export interface AdminShellProps {
  user: SessionUser;
  pendingShopApprovals: number;
  pendingProductReviews: number;
  openComplaintsCount?: number;
  ledgerUnbalanced: boolean;
  pendingApprovalsCount?: number;
  children: React.ReactNode;
}

export function AdminShell({
  user,
  pendingShopApprovals,
  pendingProductReviews,
  openComplaintsCount = 0,
  ledgerUnbalanced,
  pendingApprovalsCount = 0,
  children,
}: AdminShellProps) {
  const pathname = usePathname();
  const { logout } = useSession();

  const gatewayUser = useMemo(() => sessionUserToGatewayUser(user), [user]);
  const hasPathAccess = canAccessAdminPath(user.role, pathname);
  const canReadApprovals = roleHasCapability(user.role, "approvals.read");
  const canReadAuditLogs = canAccessAdminPath(user.role, "/admin/audit-log");

  const showToast = useCallback((message: string) => {
    toast(message);
  }, []);

  const navGroups = useMemo(
    () =>
      buildAdminNavGroups(user.role, {
        pendingShopApprovals,
        pendingProductReviews,
        openComplaintsCount,
        ledgerUnbalanced,
        pendingApprovalsCount: canReadApprovals ? pendingApprovalsCount : 0,
      }),
    [
      user.role,
      pendingShopApprovals,
      pendingProductReviews,
      openComplaintsCount,
      ledgerUnbalanced,
      canReadApprovals,
      pendingApprovalsCount,
    ]
  );

  const commandItems = useMemo(() => buildAdminCommandItems(user.role), [user.role]);

  const displayName = user.fullName || user.email || "Quản trị viên";
  const roleLabel = getUserRoleLabel(user.role);

  const account = useMemo<AppAccount>(() => {
    const menu: AppAccount["menu"] = [];
    if (canReadAuditLogs) {
      menu.push({ label: "Nhật ký kiểm toán", href: "/admin/audit-log" });
    }
    menu.push({ label: "Về trang chủ NexusMart", href: "/" });
    menu.push({
      label: "Đăng xuất",
      destructive: true,
      onSelect: () => {
        void logout("/dang-nhap");
      },
    });

    return {
      name: displayName,
      subtitle: user.email ? `${roleLabel} · ${user.email}` : roleLabel,
      menu,
    };
  }, [displayName, roleLabel, user.email, canReadAuditLogs, logout]);

  const contextValue = useMemo<AdminContextValue>(() => ({ user, showToast }), [user, showToast]);

  return (
    <AdminContext.Provider value={contextValue}>
      <AdminUserProvider user={gatewayUser}>
        <AppShell
          area="admin"
          areaLabel="Quản trị"
          homeHref="/admin"
          navGroups={navGroups}
          commandItems={commandItems}
          account={account}
          storageKey={ADMIN_SIDEBAR_STORAGE_KEY}
        >
          {hasPathAccess ? (
            children
          ) : (
            <div className="space-y-4">
              <PageHeader title="Không đủ quyền truy cập" />
              <div
                role="alert"
                className="flex flex-col items-center justify-center rounded-xl bg-card px-6 py-10 text-center shadow-card"
              >
                <div className="mb-3 flex size-10 items-center justify-center rounded-full bg-badge-critical-bg text-badge-critical-ink">
                  <ShieldAlert className="size-5" aria-hidden="true" />
                </div>
                <h2 className="text-sm font-semibold text-foreground">
                  Không đủ quyền truy cập mục này
                </h2>
                <p className="mt-1.5 max-w-md text-[13px] leading-[18px] text-muted-foreground">
                  Vai trò {roleLabel} không có quyền truy cập trang này.
                </p>
                <div className="mt-4">
                  <Button variant="outline" size="sm" asChild>
                    <Link href="/admin">Về Tổng quan</Link>
                  </Button>
                </div>
              </div>
            </div>
          )}
        </AppShell>
      </AdminUserProvider>
    </AdminContext.Provider>
  );
}
