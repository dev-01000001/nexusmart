import React from "react";
import { headers } from "next/headers";
import { ErrorState, PageHeader } from "@/components/app";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { requireCapability } from "@/lib/server/session";
import { loadAdminSitesServer } from "@/features/admin/sites/adminSitesServer";
import { AdminSitesClient } from "@/features/admin/sites/AdminSitesClient";

export const dynamic = "force-dynamic";

export default async function AdminSitesPage() {
  try {
    await requireCapability("sites.read");
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className="space-y-5">
          <PageHeader title="Site gian hàng" />
          <ErrorState
            title="Chưa thể xác minh phiên quản trị"
            description={SAFE_MESSAGES[err.kind]}
            retryHref="/admin/sites"
          />
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
