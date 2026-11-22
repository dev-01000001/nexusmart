import React from "react";
import type {
  GatewayAddress,
  GatewayCoinBalance,
  GatewayNotificationPreferences,
  GatewayNotificationsResponse,
  GatewayOrder,
  GatewayShop,
  GatewayUser,
  GatewayVoucher,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import { AccountClient, type AccountTabKey } from "@/features/account/AccountClient";
import {
  fetchOrderVisualLookup,
  fetchPublicShopsMap,
  settleGateway,
} from "@/features/account/serverData";

export const dynamic = "force-dynamic";

const VALID_TABS: readonly AccountTabKey[] = [
  "tong-quan",
  "dia-chi",
  "gian-hang",
  "thong-bao",
  "voucher",
  "so-do",
  "yeu-thich",
  "gioi-thieu",
];

type AccountSearchParams = {
  tab?: string;
  state?: string;
};

function parseTab(raw?: string): AccountTabKey {
  if (raw && (VALID_TABS as readonly string[]).includes(raw)) {
    return raw as AccountTabKey;
  }
  return "tong-quan";
}

function errorMessageOf(err: unknown, fallback: string): string {
  return err instanceof GatewayError ? err.message : fallback;
}

export default async function AccountPage({
  searchParams,
}: {
  searchParams: Promise<AccountSearchParams>;
}) {
  const sp = await searchParams;
  const initialTab = parseTab(sp.tab);
  const forcedState =
    sp.state === "loading" || sp.state === "empty" || sp.state === "error" ? sp.state : null;

  const currentPath = sp.tab ? `/tai-khoan?tab=${encodeURIComponent(sp.tab)}` : "/tai-khoan";
  let user: GatewayUser;
  try {
    user = await requireUser(currentPath);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={shared.page}>
          <section className={shared.pageHeader}>
            <div className={shared.headerLeadGroup}>
              <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
              <p className={shared.pageLead}>
                Không thể đồng bộ thông tin tài khoản lúc này. Vui lòng thử lại sau ít phút.
              </p>
            </div>
          </section>
          <section className={shared.section}>
            <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
            <StateView
              kind="error"
              title="Chưa thể tải hồ sơ tài khoản"
              message={err.message}
              actionLabel="Tải lại"
              actionHref={currentPath}
            />
          </section>
        </div>
      );
    }
    throw err;
  }

  if (forcedState) {
    return (
      <AccountClient
        initialUser={user}
        initialAddresses={[]}
        initialCoinBalance={null}
        initialOrders={[]}
        initialOrdersTotal={0}
        initialVouchers={[]}
        initialGatewayShop={null}
        initialNotifications={null}
        initialNotificationPrefs={null}
        shopsMap={{}}
        visuals={{ byProductId: {}, bySkuId: {} }}
        initialTab={initialTab}
        forcedState={forcedState}
      />
    );
  }

  const [
    addressesRes,
    coinsRes,
    ordersRes,
    vouchersRes,
    myShopRes,
    notificationsRes,
    notifPrefsRes,
    shopsMap,
    visuals,
  ] = await Promise.all([
    settleGateway(gatewayFetch<{ addresses: GatewayAddress[] }>("/users/me/addresses")),
    settleGateway(gatewayFetch<GatewayCoinBalance>("/coins/balance")),
    settleGateway(gatewayFetch<PaginatedResponse<GatewayOrder>>("/orders?page=1&page_size=5")),
    settleGateway(gatewayFetch<{ vouchers: GatewayVoucher[] }>("/vouchers")),
    settleGateway(gatewayFetch<{ shop: GatewayShop }>("/shops/me")),
    settleGateway(gatewayFetch<GatewayNotificationsResponse>("/notifications?page=1&page_size=20")),
    settleGateway(
      gatewayFetch<{ preferences: GatewayNotificationPreferences }>("/notifications/preferences")
    ),
    fetchPublicShopsMap(),
    fetchOrderVisualLookup(),
  ]);

  const addresses =
    addressesRes.ok && Array.isArray(addressesRes.data?.addresses)
      ? addressesRes.data.addresses
      : [];
  const addressesError = addressesRes.ok
    ? null
    : errorMessageOf(addressesRes.error, "Chưa thể tải sổ địa chỉ lúc này.");

  const coinBalance = coinsRes.ok ? coinsRes.data : null;
  const coinsError = coinsRes.ok
    ? null
    : errorMessageOf(coinsRes.error, "Chưa thể tải số dư Xu lúc này.");

  const orders = ordersRes.ok && Array.isArray(ordersRes.data?.items) ? ordersRes.data.items : [];
  const ordersTotal = ordersRes.ok
    ? typeof ordersRes.data?.total === "number"
      ? ordersRes.data.total
      : orders.length
    : 0;
  const ordersError = ordersRes.ok
    ? null
    : errorMessageOf(ordersRes.error, "Chưa thể tải đơn hàng gần đây lúc này.");

  const vouchers =
    vouchersRes.ok && Array.isArray(vouchersRes.data?.vouchers) ? vouchersRes.data.vouchers : [];
  const vouchersError = vouchersRes.ok
    ? null
    : errorMessageOf(vouchersRes.error, "Chưa thể tải danh sách voucher lúc này.");

  let myShop: GatewayShop | null = null;
  let shopError: string | null = null;
  if (!myShopRes.ok) {
    if (!(myShopRes.error instanceof GatewayError && myShopRes.error.status === 404)) {
      shopError = errorMessageOf(
        myShopRes.error,
        "Chưa thể kiểm tra trạng thái gian hàng lúc này."
      );
    }
  } else {
    myShop = myShopRes.data?.shop ?? null;
  }

  const notifications = notificationsRes.ok ? notificationsRes.data : null;
  const notificationsError = notificationsRes.ok
    ? null
    : errorMessageOf(notificationsRes.error, "Chưa thể tải thông báo lúc này.");

  const notificationPrefs = notifPrefsRes.ok ? (notifPrefsRes.data?.preferences ?? null) : null;

  return (
    <AccountClient
      initialUser={user}
      initialAddresses={addresses}
      addressesError={addressesError}
      initialCoinBalance={coinBalance}
      coinsError={coinsError}
      initialOrders={orders}
      initialOrdersTotal={ordersTotal}
      ordersError={ordersError}
      initialVouchers={vouchers}
      vouchersError={vouchersError}
      initialGatewayShop={myShop}
      shopError={shopError}
      initialNotifications={notifications}
      notificationsError={notificationsError}
      initialNotificationPrefs={notificationPrefs}
      shopsMap={shopsMap}
      visuals={visuals}
      initialTab={initialTab}
    />
  );
}
