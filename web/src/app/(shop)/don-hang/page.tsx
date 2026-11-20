import React from "react";
import { redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { loginPath, requireSession } from "@/lib/server/session";
import { fetchBuyerOrders } from "@/features/account/orders.server";
import { OrdersClientView } from "@/features/account/OrdersClientView";

export const dynamic = "force-dynamic";

export default async function OrdersPage() {
  await requireSession("/don-hang");

  try {
    const res = await fetchBuyerOrders({ page: 1, pageSize: 50 });
    return (
      <OrdersClientView
        initialOrders={res.items}
        totalOrders={res.total}
        shops={res.shops}
        suggestedProducts={res.suggestedProducts}
      />
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      redirect(loginPath("/don-hang"));
    }
    const msg = err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return (
      <OrdersClientView
        initialOrders={[]}
        totalOrders={0}
        shops={[]}
        suggestedProducts={[]}
        initialError={msg}
      />
    );
  }
}
