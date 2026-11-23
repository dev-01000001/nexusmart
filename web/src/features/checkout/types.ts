// View model riêng của luồng mua ngay (checkout); các DTO gateway nằm ở `@/lib/gateway-types`.

export interface BuyNowResolvedItem {
  skuId: string;
  productId: string;
  productSlug?: string;
  productName: string;
  skuName: string;
  shopId: string;
  shopName: string;
  imageUrl: string;
  unitPriceVnd: number;
  flashSaleItemId?: string;
  flashPriceVnd?: number;
  flashRemainingSlots?: number;
  flashPerUserLimit?: number;
  flashFallbackNotice?: string;
  shopProgramItemId?: string;
  shopDiscountedPriceVnd?: number;
  shopProgramName?: string;
  quantity: number;
  availableStock: number;
  weightGrams: number;
}
