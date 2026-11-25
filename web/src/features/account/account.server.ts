// Server-only loader for the buyer account overview, profile, addresses, and recent orders on a shop site.
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import { VOUCHERS, type Voucher } from "@/data/vouchers";
import { buildCatalogLookups, mergeVouchers, type GwVoucher } from "@/lib/catalog-map";
import type { GatewayNotificationPreferences } from "@/lib/gateway-types";
import { gw } from "@/lib/server/gateway";
import type { GatewayUser, Session } from "@/lib/server/session";
import { belongsToStorefront } from "@/lib/server/storefront";
import {
  mapGatewayAddress,
  type AccountProfile,
  type AddressView,
  type GwAddress,
  type GwProvince,
  type GwWard,
} from "./accountTypes";
import { mapGatewayOrder, type GwOrder, type OrderView } from "./orderView";

interface FullGatewayUser extends GatewayUser {
  created_at?: string;
}

export interface AccountPageData {
  profile: AccountProfile;
  addresses: AddressView[];
  orders: OrderView[];
  totalOrders: number;
  vouchers: Voucher[];
  provinces: GwProvince[];
  initialWards: GwWard[];
  initialProvinceCode: string;
  catalogProducts: Product[];
  notificationPrefs: GatewayNotificationPreferences | null;
}

export async function fetchAccountPageData(
  session: Session,
  opts: {
    shopId: string;
    siteProducts: Product[];
    siteShop: Shop | null;
  }
): Promise<AccountPageData> {
  const { shopId, siteProducts, siteShop } = opts;

  const [meRes, addrRes, ordersRes, provRes, vouchersRes, prefsRes] = await Promise.all([
    gw<{ user?: FullGatewayUser | null }>("/api/v1/users/me").catch(() => ({ user: null })),
    gw<{ addresses?: GwAddress[] | null }>("/api/v1/users/me/addresses"),
    gw<{ items?: GwOrder[] | null; total?: number }>("/api/v1/orders", {
      query: { storefront_shop_id: shopId, page: 1, page_size: 20 },
    }),
    gw<{ provinces?: GwProvince[] | null }>("/api/v1/shipping/provinces", {
      auth: "none",
      revalidate: 3600,
    }).catch(() => ({ provinces: [] })),
    gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
      query: { shop_id: shopId, scope: "SHOP" },
    }).catch(() => ({ vouchers: [] })),
    gw<{ preferences?: GatewayNotificationPreferences | null }>(
      "/api/v1/notifications/preferences"
    ).catch(() => ({ preferences: null })),
  ]);

  const u = meRes.user;
  const profile: AccountProfile = {
    id: session.user.id,
    fullName: u?.full_name ?? session.user.fullName,
    email: u?.email ?? session.user.email,
    phone: u?.phone ?? session.user.phone,
    role: session.user.role,
    shopId: session.user.shopId,
    avatarUrl: u?.avatar_url ?? session.user.avatarUrl,
    phoneVerified: u?.phone_verified ?? session.user.phoneVerified,
    memberSince: u?.created_at ?? "",
    coins: 0,
  };

  const shops = siteShop ? [siteShop] : [];
  const lookups = buildCatalogLookups(siteProducts, shops);

  const addresses = (addrRes.addresses ?? []).map(mapGatewayAddress);
  const rawOrders = (ordersRes.items ?? []).filter((o) => belongsToStorefront(o, shopId));
  const orders = rawOrders.map((o) => mapGatewayOrder(o, lookups));
  const provinces = provRes.provinces ?? [];

  const catalogShopId = siteShop?.id ?? shopId;
  const shopIdByBackend = new Map<string, string>([[shopId, catalogShopId]]);
  const rawShopVouchers = (vouchersRes.vouchers ?? []).filter(
    (v) =>
      String(v.scope ?? "").toUpperCase() === "SHOP" &&
      v.shop_id === shopId &&
      v.is_active !== false
  );
  const vouchers = mergeVouchers(
    rawShopVouchers,
    VOUCHERS,
    shopIdByBackend,
    new Set([catalogShopId])
  ).filter((v) => v.scope === "shop" && v.shopId === catalogShopId);

  const defaultProv = provinces.find((p) => p.code === "79") ??
    provinces[0] ?? { code: "79", name: "Thành phố Hồ Chí Minh" };
  let initialWards: GwWard[] = [];
  if (provinces.length > 0) {
    const wardRes = await gw<{ wards?: GwWard[] | null }>(
      `/api/v1/shipping/provinces/${encodeURIComponent(defaultProv.code)}/wards`,
      { auth: "none", revalidate: 3600 }
    ).catch(() => ({ wards: [] }));
    initialWards = wardRes.wards ?? [];
  }

  return {
    profile,
    addresses,
    orders,
    totalOrders:
      typeof ordersRes.total === "number" && rawOrders.length === (ordersRes.items ?? []).length
        ? ordersRes.total
        : orders.length,
    vouchers,
    provinces,
    initialWards,
    initialProvinceCode: defaultProv.code,
    catalogProducts: siteProducts,
    notificationPrefs: prefsRes.preferences ?? null,
  };
}
