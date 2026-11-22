// View model riêng cho hiển thị đơn hàng ở khu vực tài khoản; các DTO gateway nằm ở `@/lib/gateway-types`.

export interface OrderProductVisual {
  slug?: string;
  image?: string;
}

export interface OrderVisualLookup {
  byProductId: Record<string, OrderProductVisual>;
  bySkuId: Record<string, string>;
}
