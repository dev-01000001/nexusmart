import React from "react";
import { randomUUID } from "node:crypto";
import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { getSession } from "@/lib/server/session";
import type { GwAddress, GwCoinBalance, GwVoucher } from "@/features/commerce/types";
import { CheckoutClient } from "./CheckoutClient";

export const dynamic = "force-dynamic";

export default async function CheckoutPage() {
  const initialIdempotencyKey = randomUUID();

  let session = null;
  let initialError: string | null = null;
  try {
    session = await getSession();
  } catch (err) {
    initialError =
      err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
  }

  const catalogRes = await loadCatalogLookups().catch(() => ({
    products: [],
    shops: [],
    error: null,
  }));

  let initialAddresses: GwAddress[] = [];
  let initialVouchers: GwVoucher[] = [];
  let initialCoinBalance = 0;

  if (session) {
    const [addrRes, vRes, cRes] = await Promise.allSettled([
      gw<{ addresses: GwAddress[] }>("/api/v1/users/me/addresses"),
      gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers"),
      gw<GwCoinBalance>("/api/v1/coins/balance"),
    ]);
    if (addrRes.status === "fulfilled") initialAddresses = addrRes.value.addresses ?? [];
    if (vRes.status === "fulfilled") initialVouchers = vRes.value.vouchers ?? [];
    if (cRes.status === "fulfilled") initialCoinBalance = Math.max(0, cRes.value.balance ?? 0);
  }

  return (
    <CheckoutClient
      initialIdempotencyKey={initialIdempotencyKey}
      catalogProducts={catalogRes.products}
      catalogShops={catalogRes.shops}
      initialAddresses={initialAddresses}
      initialVouchers={initialVouchers}
      initialCoinBalance={initialCoinBalance}
      initialError={initialError}
    />
  );
}
