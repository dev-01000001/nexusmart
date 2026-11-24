"use client";

import React, { createContext, useContext } from "react";
import type { GatewayUser } from "@/lib/gateway-types";
import { type AdminCapability, roleHasCapability } from "@/lib/admin-rbac";

const AdminUserContext = createContext<GatewayUser | null>(null);

export function AdminUserProvider({
  user,
  children,
}: {
  user: GatewayUser;
  children: React.ReactNode;
}) {
  return <AdminUserContext.Provider value={user}>{children}</AdminUserContext.Provider>;
}

export function useAdminUser(): GatewayUser | null {
  return useContext(AdminUserContext);
}

export function useAdminCapability(capability: AdminCapability): boolean {
  const user = useContext(AdminUserContext);
  return roleHasCapability(user?.role, capability);
}
