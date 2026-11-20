// Server-only loader for the buyer account overview, profile, addresses and recent orders.
import type { Product, Shop, Voucher } from "@/data";
import { loadCatalogLookups, loadVouchers } from "@/lib/catalog";
import { gw } from "@/lib/server/gateway";
import type { GatewayUser, Session } from "@/lib/server/session";
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
  shops: Shop[];
  catalogProducts: Product[];
  suggestedProducts: Product[];
}

export async function fetchAccountPageData(session: Session): Promise<AccountPageData> {
  const [meRes, addrRes, ordersRes, coinsRes, provRes, catalog, voucherData] = await Promise.all([
    gw<{ user?: FullGatewayUser | null }>("/api/v1/users/me").catch(() => ({ user: null })),
    gw<{ addresses?: GwAddress[] | null }>("/api/v1/users/me/addresses"),
    gw<{ items?: GwOrder[] | null; total?: number }>("/api/v1/orders", {
      query: { page: 1, page_size: 20 },
    }),
    gw<{ balance?: number }>("/api/v1/coins/balance").catch(() => ({ balance: 0 })),
    gw<{ provinces?: GwProvince[] | null }>("/api/v1/shipping/provinces", {
      auth: "none",
      revalidate: 3600,
    }).catch(() => ({ provinces: [] })),
    loadCatalogLookups(),
    loadVouchers(),
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
    coins: typeof coinsRes.balance === "number" ? coinsRes.balance : 0,
  };

  const addresses = (addrRes.addresses ?? []).map(mapGatewayAddress);
  const orders = (ordersRes.items ?? []).map((o) => mapGatewayOrder(o, catalog.lookups));
  const provinces = provRes.provinces ?? [];

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
    totalOrders: ordersRes.total ?? orders.length,
    vouchers: voucherData.vouchers,
    provinces,
    initialWards,
    initialProvinceCode: defaultProv.code,
    shops: catalog.shops,
    catalogProducts: catalog.products,
    suggestedProducts: catalog.products.slice(0, 4),
  };
}
