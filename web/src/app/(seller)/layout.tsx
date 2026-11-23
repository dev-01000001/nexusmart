import React from "react";
import type { GatewayUser } from "@/lib/gateway-types";
import { requireRole } from "@/lib/server/auth";
import { GatewayError } from "@/lib/server/gateway";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import { SellerShell } from "./SellerShell";

export default async function SellerLayout({ children }: { children: React.ReactNode }) {
  let user: GatewayUser;
  try {
    user = await requireRole(["seller"], "/seller");
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={shared.page}>
          <section className={shared.pageHeader}>
            <div className={shared.headerLeadGroup}>
              <h1 className={shared.pageTitle}>Kênh Người Bán</h1>
              <p className={shared.pageLead}>
                Không thể xác minh phiên làm việc lúc này. Vui lòng thử lại sau ít phút.
              </p>
            </div>
          </section>
          <section className={shared.section}>
            <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
            <StateView
              kind="error"
              title="Chưa thể mở Kênh Người Bán"
              message={err.message}
              actionLabel="Tải lại"
              actionHref="/seller"
            />
          </section>
        </div>
      );
    }
    throw err;
  }
  return <SellerShell user={user}>{children}</SellerShell>;
}
