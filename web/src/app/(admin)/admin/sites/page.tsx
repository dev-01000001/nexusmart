import React from "react";
import { headers } from "next/headers";
import { StateView } from "@/components";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { requireCapability } from "@/lib/server/session";
import { loadAdminSitesServer } from "@/features/admin/sites/adminSitesServer";
import { AdminSitesClient } from "@/features/admin/sites/AdminSitesClient";
import shared from "@/features/admin/adminShared.module.css";

export const dynamic = "force-dynamic";

export default async function AdminSitesPage() {
  try {
    await requireCapability("sites.read");
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={shared.page}>
          <header className={shared.titleRow}>
            <div className={shared.titleBlock}>
              <h1 className={shared.pageTitle}>Site gian hàng</h1>
            </div>
          </header>
          <div className={shared.stateWrap}>
            <StateView
              kind="error"
              surface="app"
              title="Chưa thể xác minh phiên quản trị"
              message={SAFE_MESSAGES[err.kind]}
              actionLabel="Tải lại"
              actionHref="/admin/sites"
            />
          </div>
        </div>
      );
    }
    throw err;
  }
  const [res, h] = await Promise.all([loadAdminSitesServer(undefined, undefined, 100), headers()]);
  const requestHost = resolveRequestHost(h, "");
  const requestProto = resolveRequestProto(h, `http://${requestHost || "localhost"}`);

  return (
    <AdminSitesClient
      initialSites={res.items}
      rootDomain={res.rootDomain}
      requestHost={requestHost}
      requestProto={requestProto}
      shopNameById={res.shopNameById}
      loadError={res.error}
    />
  );
}
