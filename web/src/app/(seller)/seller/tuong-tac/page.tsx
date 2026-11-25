import React from "react";
import { redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import {
  normalizeSellerNotifications,
  type SellerNotification,
} from "@/features/seller/sellerReturns";
import { loadSellerProducts, requireSellerPage } from "@/features/seller/sellerServer";
import { resolveLegacyInteractionTab } from "@/features/seller/sellerValidation";
import SellerInteractionClient from "./SellerInteractionClient";

export const dynamic = "force-dynamic";

async function loadInitialNotifications(): Promise<{
  items: SellerNotification[];
  error: string | null;
}> {
  try {
    const raw = await gw<unknown>("/api/v1/notifications", {
      query: { page: 1, page_size: 50 },
    });
    const normalized = normalizeSellerNotifications(raw);
    return { items: normalized.items, error: null };
  } catch (err) {
    const message =
      err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return { items: [], error: message };
  }
}

export default async function SellerInteractionPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  await requireSellerPage();

  const sp = await searchParams;
  const rawTab = Array.isArray(sp.tab) ? sp.tab[0] : sp.tab;
  const rawSelected = Array.isArray(sp.selected) ? sp.selected[0] : sp.selected;
  const rawStatus = Array.isArray(sp.status) ? sp.status[0] : sp.status;

  const resolution = resolveLegacyInteractionTab(rawTab, {
    selected: rawSelected,
    status: rawStatus,
  });
  if (resolution.kind === "redirect") {
    redirect(resolution.href);
  }

  const [productsRes, notifRes] = await Promise.all([
    loadSellerProducts(100),
    loadInitialNotifications(),
  ]);

  return (
    <SellerInteractionClient
      initialTab={resolution.tab}
      products={productsRes.data?.items ?? []}
      productsError={productsRes.error}
      initialNotifications={notifRes.items}
      notificationsError={notifRes.error}
    />
  );
}
