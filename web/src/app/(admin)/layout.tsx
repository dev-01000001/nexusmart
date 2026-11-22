import React from "react";
import { requireRole } from "@/lib/server/auth";
import { AdminShell } from "./AdminShell";

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  const user = await requireRole(["admin", "super_admin"], "/admin");
  return <AdminShell user={user}>{children}</AdminShell>;
}
