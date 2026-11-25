import React, { Suspense } from "react";
import { notFound, redirect } from "next/navigation";
import type {
  GatewayActiveShopDiscount,
  GatewayFlashSale,
  GatewayFlashSalesResponse,
  GatewayOrder,
  GatewayPayment,
  GatewayPaymentResponse,
  GatewayProduct,
  GatewayPublicShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { fetchActiveShopDiscounts } from "@/lib/catalog";
import { buildActiveShopDiscountsMap, buildLiveFlashItemsBySkuId } from "@/lib/catalog-mapper";
import { enrichProductsWithPromotions } from "@/lib/search-client";
import { DEFAULT_SHOP_LOOKUP_CONCURRENCY, lookupShopsByIdsWithFetcher } from "@/lib/shop-lookup";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import { PaymentWaitingClient } from "./PaymentWaitingClient";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ paymentId: string }>;
}

export default async function PaymentWaitingPage({ params }: PageProps) {
  const { paymentId } = await params;
  const cleanId = paymentId?.trim();
  if (!cleanId) {
    notFound();
  }

  const currentPath = `/thanh-toan/cho-thanh-toan/${encodeURIComponent(cleanId)}`;
  try {
    await requireUser(currentPath);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <Suspense
          fallback={
            <div className={shared.page}>
              <StateView kind="loading" title="Đang tải thông tin thanh toán..." rows={4} />
            </div>
          }
        >
          <PaymentWaitingClient
            initialPayment={null}
            initialOrders={[]}
            initialError={err.message}
            suggestedProducts={[]}
            shopNameById={{}}
          />
        </Suspense>
      );
    }
    throw err;
  }

  let payment: GatewayPayment | null = null;
  let loadError: string | null = null;

  try {
    const res = await gatewayFetch<GatewayPaymentResponse>(
      `/payments/${encodeURIComponent(cleanId)}`,
      { cache: "no-store" }
    );
    payment = res?.payment ?? null;
    if (!payment) {
      notFound();
    }
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.status === 401) {
        redirect(`/dang-nhap?next=${encodeURIComponent(currentPath)}`);
      }
      if (err.status === 403 || err.status === 404) {
        notFound();
      }
      loadError = err.message;
    } else {
      loadError = "Hệ thống thanh toán đang tạm bận, vui lòng thử lại sau ít phút.";
    }
  }

  // Best-effort fetch of sub-orders belonging to this payment for order codes & direct links
  const allocatedIds = Array.from(
    new Set(
      (payment?.shop_allocations?.map((a) => a.order_id) ?? []).filter((id): id is string =>
        Boolean(id)
      )
    )
  );
  const orderIds =
    allocatedIds.length > 0 ? allocatedIds : payment?.order_id ? [payment.order_id] : [];

  const [productsRes, shopsRes, flashSalesRes, ...orderResults] = await Promise.allSettled([
    gatewayFetch<PaginatedResponse<GatewayProduct>>("/products?page_size=4", {
      token: false,
      cache: "no-store",
    }),
    gatewayFetch<PaginatedResponse<GatewayPublicShop>>("/shops?page_size=100", {
      token: false,
      cache: "no-store",
    }),
    gatewayFetch<GatewayFlashSalesResponse>("/flash-sales?include_upcoming=false", {
      token: false,
      cache: "no-store",
    }),
    ...orderIds.map((orderId) =>
      gatewayFetch<{ order: GatewayOrder }>(`/orders/${encodeURIComponent(orderId)}`, {
        cache: "no-store",
      })
    ),
  ]);

  const initialOrders: GatewayOrder[] = [];
  for (const res of orderResults) {
    if (res.status === "fulfilled" && res.value?.order) {
      initialOrders.push(res.value.order);
    }
  }

  const rawSuggestedProducts: GatewayProduct[] =
    productsRes.status === "fulfilled" ? (productsRes.value?.items ?? []).slice(0, 4) : [];
  const rawFlashSales: GatewayFlashSale[] =
    flashSalesRes.status === "fulfilled" && Array.isArray(flashSalesRes.value?.flash_sales)
      ? flashSalesRes.value.flash_sales
      : [];

  const existingShopMap: Record<string, GatewayPublicShop> = {};
  if (shopsRes.status === "fulfilled" && Array.isArray(shopsRes.value?.items)) {
    for (const s of shopsRes.value.items) {
      if (s?.id) existingShopMap[s.id] = s;
    }
  }

  // §10-3 + §10: hydrate shop #101+ names via GET /shops/{id} (concurrency <= 8)
  // and fetch active shop discounts for suggested strip SKUs.
  const neededShopIds = Array.from(
    new Set([
      ...(payment?.shop_allocations ?? []).map((a) => a.shop_id),
      ...initialOrders.map((o) => o.shop_id),
      ...rawSuggestedProducts.map((p) => p.shop_id),
    ])
  ).filter(Boolean);

  const suggestedSkuIds = Array.from(
    new Set(
      rawSuggestedProducts.flatMap((p) =>
        (p.skus ?? []).filter((s) => s.is_active !== false && s.id).map((s) => s.id)
      )
    )
  );

  const [resolvedShopMap, activeShopDiscounts] = await Promise.all([
    lookupShopsByIdsWithFetcher(
      neededShopIds,
      async (id) => {
        const res = await gatewayFetch<{ shop?: GatewayPublicShop }>(
          `/shops/${encodeURIComponent(id)}`,
          { token: false, cache: "no-store" }
        );
        return res?.shop ?? null;
      },
      { existing: existingShopMap, concurrency: DEFAULT_SHOP_LOOKUP_CONCURRENCY }
    ),
    suggestedSkuIds.length > 0
      ? fetchActiveShopDiscounts({ skuIds: suggestedSkuIds }).catch(
          () => [] as GatewayActiveShopDiscount[]
        )
      : Promise.resolve([] as GatewayActiveShopDiscount[]),
  ]);

  const shopNameById: Record<string, string> = {};
  for (const [id, shop] of Object.entries(resolvedShopMap)) {
    shopNameById[id] = shop.name;
  }

  const suggestedProducts = enrichProductsWithPromotions(
    rawSuggestedProducts,
    buildLiveFlashItemsBySkuId(rawFlashSales, Date.now()),
    buildActiveShopDiscountsMap(activeShopDiscounts)
  );

  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải thông tin thanh toán..." rows={4} />
        </div>
      }
    >
      <PaymentWaitingClient
        initialPayment={payment}
        initialOrders={initialOrders}
        initialError={loadError}
        suggestedProducts={suggestedProducts}
        shopNameById={shopNameById}
      />
    </Suspense>
  );
}
