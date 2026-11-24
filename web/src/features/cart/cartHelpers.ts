import type { GatewayCartShopGroup } from "@/lib/gateway-types";

const DEFAULT_IMAGE = "/products/ao-thun-cotton-trang.png";

export interface CartItem {
  skuId: string;
  productId: string;
  shopId: string;
  shopName: string;
  productName: string;
  skuName: string;
  imageUrl: string;
  priceVnd: number;
  addedPriceVnd: number;
  priceChanged: boolean;
  quantity: number;
  availableStock: number;
  weightGrams: number;
  selected: boolean;
  unavailable: boolean;
  unavailableReason: string;
}

export interface CartShopGroup {
  shopId: string;
  shopName: string;
  items: CartItem[];
  subtotalVnd: number;
  selectedSubtotalVnd: number;
  totalWeightGrams: number;
  selectedWeightGrams: number;
}

export interface CartSummary {
  items: CartItem[];
  availableItemCount: number;
  unavailableItemCount: number;
  selectedItems: CartItem[];
  selectedCount: number;
  selectedSubtotalVnd: number;
  selectedShopGroups: CartShopGroup[];
}

export function mapGatewayCartGroups(
  rawGroups: readonly GatewayCartShopGroup[],
  prevSelected: Record<string, boolean>
): CartShopGroup[] {
  return rawGroups
    .map((group) => {
      const shopName = group.shop_name?.trim() || "Gian hàng NexusMart";
      const items: CartItem[] = (group.items ?? []).map((it) => {
        const unavailable = Boolean(it.unavailable);
        const availableStock = unavailable ? 0 : Math.max(0, Math.trunc(it.available_stock ?? 0));
        const inStock = !unavailable && availableStock > 0;
        const priceVnd = unavailable ? 0 : Math.max(0, Math.trunc(it.price_vnd ?? 0));
        const addedPriceVnd = unavailable
          ? 0
          : Math.max(0, Math.trunc(it.added_price_vnd || priceVnd));
        const hasPrev = Object.prototype.hasOwnProperty.call(prevSelected, it.sku_id);
        const selected = inStock
          ? hasPrev
            ? Boolean(prevSelected[it.sku_id])
            : it.selected !== false
          : false;

        return {
          skuId: it.sku_id,
          productId: it.product_id,
          shopId: it.shop_id || group.shop_id,
          shopName,
          productName: it.product_name,
          skuName: it.sku_name,
          imageUrl: it.image_url?.trim() || DEFAULT_IMAGE,
          priceVnd,
          addedPriceVnd,
          priceChanged: !unavailable && Boolean(it.price_changed),
          quantity: Math.max(1, Math.trunc(it.quantity)),
          availableStock,
          weightGrams: unavailable ? 0 : Math.max(0, Math.trunc(it.weight_grams ?? 0)),
          selected,
          unavailable,
          unavailableReason: unavailable ? (it.unavailable_reason ?? "").trim() : "",
        };
      });

      const availableItems = items.filter((it) => !it.unavailable);
      const selectedItems = availableItems.filter((it) => it.selected && it.availableStock > 0);
      const subtotalVnd = availableItems.reduce((sum, it) => sum + it.priceVnd * it.quantity, 0);
      const selectedSubtotalVnd = selectedItems.reduce(
        (sum, it) => sum + it.priceVnd * it.quantity,
        0
      );
      const totalWeightGrams = availableItems.reduce(
        (sum, it) => sum + it.weightGrams * it.quantity,
        0
      );
      const selectedWeightGrams = selectedItems.reduce(
        (sum, it) => sum + it.weightGrams * it.quantity,
        0
      );

      return {
        shopId: group.shop_id,
        shopName,
        items,
        subtotalVnd,
        selectedSubtotalVnd,
        totalWeightGrams,
        selectedWeightGrams,
      };
    })
    .filter((group) => group.items.length > 0);
}

export function computeCartSummary(shopGroups: readonly CartShopGroup[]): CartSummary {
  const items = shopGroups.flatMap((g) => g.items);
  const availableItemCount = items
    .filter((it) => !it.unavailable)
    .reduce((sum, it) => sum + it.quantity, 0);
  const unavailableItemCount = items.filter((it) => it.unavailable).length;
  const selectedItems = items.filter(
    (it) => !it.unavailable && it.selected && it.availableStock > 0
  );
  const selectedCount = selectedItems.reduce((sum, it) => sum + it.quantity, 0);
  const selectedSubtotalVnd = selectedItems.reduce((sum, it) => sum + it.priceVnd * it.quantity, 0);
  const selectedShopGroups = shopGroups
    .map((group) => {
      const sel = group.items.filter(
        (it) => !it.unavailable && it.selected && it.availableStock > 0
      );
      return {
        ...group,
        items: sel,
        selectedSubtotalVnd: sel.reduce((sum, it) => sum + it.priceVnd * it.quantity, 0),
        selectedWeightGrams: sel.reduce((sum, it) => sum + it.weightGrams * it.quantity, 0),
      };
    })
    .filter((group) => group.items.length > 0);

  return {
    items,
    availableItemCount,
    unavailableItemCount,
    selectedItems,
    selectedCount,
    selectedSubtotalVnd,
    selectedShopGroups,
  };
}
