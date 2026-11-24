import React from "react";
import type { GatewayUser } from "@/lib/gateway-types";
import { ADMIN_STAFF_ROLES } from "@/lib/admin-rbac";
import { requireRole } from "@/lib/server/auth";
import { GatewayError } from "@/lib/server/gateway";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import { AdminShell } from "./AdminShell";

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  let user: GatewayUser;
  try {
    user = await requireRole([...ADMIN_STAFF_ROLES], "/admin");
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={shared.page}>
          <section className={shared.pageHeader}>
            <div className={shared.headerLeadGroup}>
              <h1 className={shared.pageTitle}>Quản trị NexusMart</h1>
              <p className={shared.pageLead}>
                Không thể xác minh phiên quản trị lúc này. Vui lòng thử lại sau ít phút.
              </p>
            </div>
          </section>
          <section className={shared.section}>
            <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
            <StateView
              kind="error"
              title="Chưa thể mở trang Quản trị"
              message={err.message}
              actionLabel="Tải lại"
              actionHref="/admin"
            />
          </section>
        </div>
      );
    }
    throw err;
  }
  return <AdminShell user={user}>{children}</AdminShell>;
}
