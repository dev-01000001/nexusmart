import React, { Suspense } from "react";
import { randomUUID } from "node:crypto";
import type {
  GatewayAddress,
  GatewayCart,
  GatewayCoinBalance,
  GatewayFlashSalesResponse,
  GatewayProduct,
  GatewayPublicShop,
  GatewayUser,
  GatewayVoucher,
  GatewayVouchersResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import type { BuyNowResolvedItem } from "@/features/checkout/types";
import { resolveLiveFlashSaleItem } from "@/features/checkout/helpers";
import { CheckoutClient } from "./CheckoutClient";

export const dynamic = "force-dynamic";

interface CheckoutSearchParams {
  sku?: string | string[];
  qty?: string | string[];
  flash?: string | string[];
  state?: string | string[];
}

function firstParam(val: string | string[] | undefined): string {
  if (Array.isArray(val)) return val[0]?.trim() ?? "";
  return val?.trim() ?? "";
}

function findSkuInProducts(
  list: GatewayProduct[],
  skuId: string
): { product: GatewayProduct; sku: GatewayProduct["skus"][number] } | null {
  for (const product of list) {
    const sku = (product.skus ?? []).find((s) => s.id === skuId);
    if (sku) return { product, sku };
  }
  return null;
}

export default async function CheckoutPage({
  searchParams,
}: {
  searchParams: Promise<CheckoutSearchParams>;
}) {
  const params = await searchParams;
  const skuParam = firstParam(params.sku);
  const qtyParam = firstParam(params.qty);
  const flashParam = firstParam(params.flash);

  const qs = new URLSearchParams();
  if (skuParam) qs.set("sku", skuParam);
  if (qtyParam) qs.set("qty", qtyParam);
  if (flashParam) qs.set("flash", flashParam);
  const currentPath = qs.toString() ? `/thanh-toan?${qs.toString()}` : "/thanh-toan";

  let user: GatewayUser;
  try {
    user = await requireUser(currentPath);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={shared.page}>
          <section className={shared.pageHeader}>
            <div className={shared.headerLeadGroup}>
              <h1 className={shared.pageTitle}>Thanh toán đơn hàng</h1>
              <p className={shared.pageLead}>
                Không thể đồng bộ thông tin tài khoản lúc này. Vui lòng thử lại sau ít phút.
              </p>
            </div>
          </section>
          <section className={shared.section}>
            <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
            <StateView
              kind="error"
              title="Chưa thể tải trang thanh toán"
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

  // Server-generated Idempotency-Key per checkout render; reused across retries on network errors.
  const idempotencyKey = `nm-checkout-${randomUUID()}`;

  const [addrRes, coinsRes, vouchersRes, shopsRes, cartRes, productsRes] = await Promise.allSettled(
    [
      gatewayFetch<{ addresses: GatewayAddress[] }>("/users/me/addresses", { cache: "no-store" }),
      gatewayFetch<GatewayCoinBalance>("/coins/balance", { cache: "no-store" }),
      gatewayFetch<GatewayVouchersResponse>("/vouchers", { cache: "no-store" }),
      gatewayFetch<PaginatedResponse<GatewayPublicShop>>("/shops?page_size=50", {
        cache: "no-store",
      }),
      skuParam
        ? Promise.resolve(null)
        : gatewayFetch<{ cart: GatewayCart }>("/cart", { cache: "no-store" }),
      gatewayFetch<PaginatedResponse<GatewayProduct>>(
        skuParam ? "/products?page_size=100" : "/products?page_size=8",
        { cache: "no-store" }
      ),
    ]
  );

  let initialError: string | null = null;
  let initialAddresses: GatewayAddress[] = [];
  if (addrRes.status === "fulfilled") {
    initialAddresses = addrRes.value?.addresses ?? [];
  } else if (
    addrRes.reason instanceof GatewayError &&
    (addrRes.reason.status === 429 || addrRes.reason.status >= 500)
  ) {
    initialError = addrRes.reason.message;
  }

  let initialCart: GatewayCart | null = null;
  if (cartRes.status === "fulfilled" && cartRes.value?.cart) {
    initialCart = cartRes.value.cart;
  } else if (
    !skuParam &&
    cartRes.status === "rejected" &&
    cartRes.reason instanceof GatewayError &&
    (cartRes.reason.status === 429 || cartRes.reason.status >= 500)
  ) {
    initialError = initialError ?? cartRes.reason.message;
  }

  const initialCoinBalance =
    coinsRes.status === "fulfilled" && typeof coinsRes.value?.balance === "number"
      ? Math.max(0, Math.trunc(coinsRes.value.balance))
      : 0;

  const initialVouchers: GatewayVoucher[] =
    vouchersRes.status === "fulfilled" ? (vouchersRes.value?.vouchers ?? []) : [];

  const initialShops: GatewayPublicShop[] =
    shopsRes.status === "fulfilled" ? (shopsRes.value?.items ?? []) : [];

  const shopNameById = new Map<string, string>();
  for (const s of initialShops) {
    shopNameById.set(s.id, s.name);
  }

  const allProducts: GatewayProduct[] =
    productsRes.status === "fulfilled" ? (productsRes.value?.items ?? []) : [];

  // Contract 2: Buy Now / Flash Sale direct mode `/thanh-toan?sku=<sku_id>&qty=<n>[&flash=<flash_sale_item_id>]`
  let buyNowItem: BuyNowResolvedItem | null = null;
  let buyNowError: string | null = null;

  if (skuParam) {
    const parsedQty = Math.max(1, Math.min(99, Number.parseInt(qtyParam || "1", 10) || 1));
    const [flashRes] = await Promise.allSettled([
      flashParam
        ? gatewayFetch<GatewayFlashSalesResponse>("/flash-sales?include_upcoming=true", {
            cache: "no-store",
          })
        : Promise.resolve(null),
    ]);

    if (productsRes.status === "rejected") {
      buyNowError =
        productsRes.reason instanceof GatewayError
          ? productsRes.reason.message
          : "Chưa thể tải thông tin sản phẩm mua ngay lúc này.";
    } else if (
      flashParam &&
      flashRes.status === "rejected" &&
      flashRes.reason instanceof GatewayError &&
      (flashRes.reason.status === 429 || flashRes.reason.status >= 500)
    ) {
      buyNowError = flashRes.reason.message;
    } else {
      let matched = findSkuInProducts(allProducts, skuParam);
      if (!matched) {
        const total = productsRes.value?.total ?? allProducts.length;
        const pageSize = 100;
        const maxPages = Math.min(Math.ceil(total / pageSize), 10);
        for (let page = 2; page <= maxPages; page += 1) {
          try {
            const nextPage = await gatewayFetch<PaginatedResponse<GatewayProduct>>(
              `/products?page=${page}&page_size=${pageSize}`,
              { cache: "no-store" }
            );
            const items = nextPage?.items ?? [];
            if (items.length === 0) break;
            matched = findSkuInProducts(items, skuParam);
            if (matched) break;
          } catch {
            break;
          }
        }
      }
      const foundProduct = matched?.product ?? null;
      const foundSku = matched?.sku ?? null;

      if (!foundProduct || !foundSku) {
        buyNowError = "Sản phẩm mua ngay không còn tồn tại hoặc đã ngừng kinh doanh.";
      } else if (!foundSku.is_active || foundSku.available_stock <= 0) {
        buyNowError = "Phân loại sản phẩm bạn chọn hiện đã hết hàng.";
      } else {
        let flashPriceVnd: number | undefined;
        let flashRemainingSlots: number | undefined;
        let validFlashId: string | undefined;
        let maxBuyNowQty = foundSku.available_stock;

        if (flashParam && flashRes.status === "fulfilled" && flashRes.value?.flash_sales) {
          const resolvedFlash = resolveLiveFlashSaleItem(
            flashRes.value.flash_sales,
            flashParam,
            foundSku.id,
            foundSku.price_vnd
          );
          if (resolvedFlash) {
            validFlashId = resolvedFlash.flashSaleItemId;
            flashPriceVnd = resolvedFlash.flashPriceVnd;
            flashRemainingSlots = resolvedFlash.remainingSlots;
            maxBuyNowQty = Math.min(maxBuyNowQty, resolvedFlash.remainingSlots);
            if (resolvedFlash.perUserLimit > 0) {
              maxBuyNowQty = Math.min(maxBuyNowQty, resolvedFlash.perUserLimit);
            }
          }
        }

        const clampedQty = Math.max(1, Math.min(parsedQty, Math.max(1, maxBuyNowQty)));
        buyNowItem = {
          skuId: foundSku.id,
          productId: foundProduct.id,
          productSlug: foundProduct.slug,
          productName: foundProduct.name,
          skuName: foundSku.name,
          shopId: foundSku.shop_id || foundProduct.shop_id,
          shopName:
            shopNameById.get(foundSku.shop_id || foundProduct.shop_id) ?? "Gian hàng NexusMart",
          imageUrl:
            foundSku.image_url || foundProduct.images?.[0] || "/products/ao-thun-cotton-trang.png",
          unitPriceVnd: foundSku.price_vnd,
          flashSaleItemId: validFlashId,
          flashPriceVnd,
          flashRemainingSlots,
          quantity: clampedQty,
          availableStock: foundSku.available_stock,
          weightGrams: Math.max(100, foundSku.weight_grams || 300),
        };
      }
    }
  }

  const suggestedProducts = allProducts.slice(0, 4);

  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang chuẩn bị trang thanh toán..." rows={4} />
        </div>
      }
    >
      <CheckoutClient
        user={user}
        idempotencyKey={idempotencyKey}
        initialAddresses={initialAddresses}
        initialCart={initialCart}
        initialCoinBalance={initialCoinBalance}
        initialVouchers={initialVouchers}
        initialShops={initialShops}
        initialError={initialError}
        buyNowItem={buyNowItem}
        buyNowError={buyNowError}
        suggestedProducts={suggestedProducts}
      />
    </Suspense>
  );
}
