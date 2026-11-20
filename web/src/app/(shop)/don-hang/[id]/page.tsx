import React from "react";
import { notFound, redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { loginPath, requireSession } from "@/lib/server/session";
import { fetchBuyerOrder } from "@/features/account/orders.server";
import { OrderDetailClientView } from "@/features/account/OrderDetailClientView";

export const dynamic = "force-dynamic";

export default async function OrderDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const session = await requireSession(`/don-hang/${encodeURIComponent(id)}`);

  try {
    const res = await fetchBuyerOrder(id, session);
    return (
      <OrderDetailClientView
        orderId={id}
        initialOrder={res.order}
        shop={res.shop}
        suggestedProducts={res.suggestedProducts}
      />
    );
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.kind === "unauthenticated") {
        redirect(loginPath(`/don-hang/${encodeURIComponent(id)}`));
      }
      if (err.kind === "not_found" || err.kind === "forbidden") {
        notFound();
      }
      return (
        <OrderDetailClientView
          orderId={id}
          initialOrder={null}
          suggestedProducts={[]}
          initialError={SAFE_MESSAGES[err.kind]}
        />
      );
    }
    return (
      <OrderDetailClientView
        orderId={id}
        initialOrder={null}
        suggestedProducts={[]}
        initialError={SAFE_MESSAGES.unavailable}
      />
    );
  }
}
