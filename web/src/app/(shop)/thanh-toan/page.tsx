import React, { Suspense } from "react";
import { randomUUID } from "node:crypto";
import type {
  GatewayActiveShopDiscount,
  GatewayAddress,
  GatewayCart,
  GatewayCoinBalance,
  GatewayFlashSale,
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
import { fetchActiveShopDiscounts } from "@/lib/catalog";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import type { BuyNowResolvedItem } from "@/features/checkout/types";
import {
  inspectFlashSaleStatusForSku,
  resolveLiveFlashSaleForSku,
  resolveLiveFlashSaleItem,
} from "@/features/checkout/helpers";
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

  const [
    addrRes,
    coinsRes,
    vouchersRes,
    shopsRes,
    cartRes,
    productsRes,
    buyNowProductRes,
    flashSalesRes,
  ] = await Promise.allSettled([
    gatewayFetch<{ addresses: GatewayAddress[] }>("/users/me/addresses", { cache: "no-store" }),
    gatewayFetch<GatewayCoinBalance>("/coins/balance", { cache: "no-store" }),
    gatewayFetch<GatewayVouchersResponse>("/vouchers", { cache: "no-store" }),
    gatewayFetch<PaginatedResponse<GatewayPublicShop>>("/shops?page_size=50", {
      cache: "no-store",
    }),
    skuParam
      ? Promise.resolve(null)
      : gatewayFetch<{ cart: GatewayCart }>("/cart", { cache: "no-store" }),
    gatewayFetch<PaginatedResponse<GatewayProduct>>("/products?page_size=8", {
      cache: "no-store",
    }),
    skuParam
      ? gatewayFetch<PaginatedResponse<GatewayProduct>>(
          `/products?sku_ids=${encodeURIComponent(skuParam)}&page_size=10`,
          { cache: "no-store" }
        )
      : Promise.resolve(null),
    gatewayFetch<GatewayFlashSalesResponse>("/flash-sales?include_upcoming=true", {
      cache: "no-store",
    }),
  ]);

  const initialFlashSales: GatewayFlashSale[] =
    flashSalesRes.status === "fulfilled" && Array.isArray(flashSalesRes.value?.flash_sales)
      ? flashSalesRes.value.flash_sales
      : [];

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

  // Gateway requires sku_ids (<= 200) or shop_id on GET /shop-discounts/active;
  // fetch only for the target Buy Now SKU or SKUs present in the loaded cart.
  const targetDiscountSkuIds: string[] = skuParam
    ? [skuParam]
    : Array.from(
        new Set(
          (initialCart?.shop_groups ?? [])
            .flatMap((g) => (g.items ?? []).map((it) => it.sku_id))
            .filter(Boolean)
        )
      );
  const initialActiveShopDiscounts: GatewayActiveShopDiscount[] =
    targetDiscountSkuIds.length > 0
      ? await fetchActiveShopDiscounts({ skuIds: targetDiscountSkuIds }).catch(
          () => [] as GatewayActiveShopDiscount[]
        )
      : [];

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

    const buyNowLookupFailedHard =
      buyNowProductRes.status === "rejected" &&
      !(buyNowProductRes.reason instanceof GatewayError && buyNowProductRes.reason.status === 400);

    if (buyNowLookupFailedHard) {
      buyNowError =
        buyNowProductRes.reason instanceof GatewayError
          ? buyNowProductRes.reason.message
          : "Chưa thể tải thông tin sản phẩm mua ngay lúc này.";
    } else if (
      flashParam &&
      flashSalesRes.status === "rejected" &&
      flashSalesRes.reason instanceof GatewayError &&
      (flashSalesRes.reason.status === 429 || flashSalesRes.reason.status >= 500)
    ) {
      buyNowError = flashSalesRes.reason.message;
    } else {
      const targetedProducts =
        buyNowProductRes.status === "fulfilled" ? (buyNowProductRes.value?.items ?? []) : [];
      const matched =
        findSkuInProducts(targetedProducts, skuParam) ?? findSkuInProducts(allProducts, skuParam);
      const foundProduct = matched?.product ?? null;
      const foundSku = matched?.sku ?? null;

      if (!foundProduct || !foundSku) {
        buyNowError = "Sản phẩm mua ngay không còn tồn tại hoặc đã ngừng kinh doanh.";
      } else if (!foundSku.is_active || foundSku.available_stock <= 0) {
        buyNowError = "Phân loại sản phẩm bạn chọn hiện đã hết hàng.";
      } else {
        let flashPriceVnd: number | undefined;
        let flashRemainingSlots: number | undefined;
        let flashPerUserLimit: number | undefined;
        let validFlashId: string | undefined;
        let flashFallbackNotice: string | undefined;
        let maxBuyNowQty = foundSku.available_stock;

        const resolvedFlash = flashParam
          ? resolveLiveFlashSaleItem(initialFlashSales, flashParam, foundSku.id, foundSku.price_vnd)
          : resolveLiveFlashSaleForSku(initialFlashSales, foundSku.id, foundSku.price_vnd);

        if (resolvedFlash) {
          validFlashId = resolvedFlash.flashSaleItemId;
          flashPriceVnd = resolvedFlash.flashPriceVnd;
          flashRemainingSlots = resolvedFlash.remainingSlots;
          flashPerUserLimit = resolvedFlash.perUserLimit;
          maxBuyNowQty = Math.min(maxBuyNowQty, resolvedFlash.remainingSlots);
          if (resolvedFlash.perUserLimit > 0) {
            maxBuyNowQty = Math.min(maxBuyNowQty, resolvedFlash.perUserLimit);
          }
        } else if (flashParam) {
          const inspect = inspectFlashSaleStatusForSku(initialFlashSales, foundSku.id, flashParam);
          if (inspect.message) {
            flashFallbackNotice = inspect.message;
          }
        }

        // Retain any active shop discount even when a live flash sale exists:
        // Flash wins while active, and if buyer explicitly opts out of Flash, server applies the shop discount.
        let shopProgramItemId: string | undefined;
        let shopDiscountedPriceVnd: number | undefined;
        let shopProgramName: string | undefined;
        const matchedDisc = initialActiveShopDiscounts.find(
          (d) =>
            d.sku_id === foundSku.id &&
            d.discounted_price_vnd > 0 &&
            d.discounted_price_vnd < foundSku.price_vnd
        );
        if (matchedDisc) {
          shopProgramItemId = matchedDisc.item_id;
          shopDiscountedPriceVnd = Math.trunc(matchedDisc.discounted_price_vnd);
          shopProgramName = matchedDisc.program_name || "Giảm giá gian hàng";
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
          flashPerUserLimit,
          flashFallbackNotice,
          shopProgramItemId,
          shopDiscountedPriceVnd,
          shopProgramName,
          quantity: clampedQty,
          availableStock: foundSku.available_stock,
          weightGrams: Math.max(0, Math.trunc(foundSku.weight_grams || 0)),
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
        initialFlashSales={initialFlashSales}
        initialActiveShopDiscounts={initialActiveShopDiscounts}
        initialError={initialError}
        buyNowItem={buyNowItem}
        buyNowError={buyNowError}
        suggestedProducts={suggestedProducts}
      />
    </Suspense>
  );
}
