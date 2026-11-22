import React, { Suspense } from "react";
import { randomUUID } from "node:crypto";
import type {
  GatewayAddress,
  GatewayCart,
  GatewayCoinBalance,
  GatewayFlashSalesResponse,
  GatewayProduct,
  GatewayPublicShop,
  GatewayVoucher,
  GatewayVouchersResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import type { BuyNowResolvedItem } from "@/features/checkout/types";
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

  const user = await requireUser(currentPath);

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
  } else if (addrRes.reason instanceof GatewayError && addrRes.reason.status >= 500) {
    initialError = addrRes.reason.message;
  }

  let initialCart: GatewayCart | null = null;
  if (cartRes.status === "fulfilled" && cartRes.value?.cart) {
    initialCart = cartRes.value.cart;
  } else if (
    !skuParam &&
    cartRes.status === "rejected" &&
    cartRes.reason instanceof GatewayError &&
    cartRes.reason.status >= 500
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
    } else {
      const products = allProducts;
      let foundProduct: GatewayProduct | null = null;
      let foundSku: GatewayProduct["skus"][number] | null = null;

      for (const prod of products) {
        const match = (prod.skus ?? []).find((s) => s.id === skuParam);
        if (match) {
          foundProduct = prod;
          foundSku = match;
          break;
        }
      }

      if (!foundProduct || !foundSku) {
        buyNowError = "Sản phẩm mua ngay không còn tồn tại hoặc đã ngừng kinh doanh.";
      } else if (!foundSku.is_active || foundSku.available_stock <= 0) {
        buyNowError = "Phân loại sản phẩm bạn chọn hiện đã hết hàng.";
      } else {
        let flashPriceVnd: number | undefined;
        let flashRemainingSlots: number | undefined;
        let validFlashId: string | undefined;

        if (flashParam && flashRes.status === "fulfilled" && flashRes.value?.flash_sales) {
          for (const fs of flashRes.value.flash_sales) {
            const fi = (fs.items ?? []).find((it) => it.id === flashParam);
            if (fi) {
              validFlashId = fi.id;
              flashPriceVnd = fi.flash_price_vnd;
              flashRemainingSlots = fi.remaining_slots;
              break;
            }
          }
        }

        const clampedQty = Math.min(parsedQty, foundSku.available_stock);
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
          flashSaleItemId: validFlashId || flashParam || undefined,
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
