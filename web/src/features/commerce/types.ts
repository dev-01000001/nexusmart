// Shared gateway DTOs for Cart, Vouchers/Coins, Checkout, Shipping, and Payments.
// These mirror services/gateway/internal/router/handlers_{cart,promotion,order,payment,shipping,user}.go.

export interface GwCartItem {
  sku_id: string;
  product_id: string;
  shop_id: string;
  product_name: string;
  sku_name: string;
  image_url: string;
  price_vnd: number;
  added_price_vnd: number;
  price_changed: boolean;
  quantity: number;
  available_stock: number;
  weight_grams: number;
  selected: boolean;
  unavailable?: boolean;
  unavailable_reason?: string;
}

export interface GwCartShopGroup {
  shop_id: string;
  shop_name: string;
  items: GwCartItem[];
  subtotal_vnd: number;
  total_weight_grams: number;
}

export interface GwCart {
  user_id: string;
  shop_groups: GwCartShopGroup[];
  total_items: number;
  total_vnd: number;
}

export interface GwVoucher {
  id: string;
  code: string;
  name: string;
  scope: "PLATFORM" | "SHOP" | "FREESHIP" | string;
  shop_id: string;
  discount_type: "FIXED" | "PERCENT" | string;
  value_vnd: number;
  discount_percent: number;
  max_discount_vnd: number;
  min_order_vnd: number;
  quota: number;
  used_count: number;
  remaining_quota: number;
  per_user_limit: number;
  budget_vnd: number;
  used_budget_vnd: number;
  starts_at?: string;
  ends_at?: string;
  is_active: boolean;
  saved: boolean;
  eligible: boolean;
  ineligible_reason: string;
}

export interface GwCoinTransaction {
  id: string;
  user_id: string;
  delta: number;
  reason: string;
  reference_id: string;
  created_at?: string;
}

export interface GwCoinBalance {
  user_id: string;
  balance: number;
  recent_transactions: GwCoinTransaction[];
}

export interface GwShopDiscountBreakdown {
  shop_id: string;
  subtotal_vnd: number;
  flash_sale_discount_vnd: number;
  shop_program_discount_vnd?: number;
  shop_voucher_id: string;
  shop_voucher_discount_vnd: number;
  platform_voucher_discount_vnd: number;
  freeship_discount_vnd: number;
  shop_freeship_discount_vnd?: number;
  coins_discount_vnd: number;
  shipping_fee_vnd: number;
  final_shipping_fee_vnd: number;
  final_payable_vnd: number;
  applied_shop_program_item_ids?: string[];
}

export interface GwDiscountBreakdown {
  shops: GwShopDiscountBreakdown[];
  total_subtotal_vnd: number;
  total_flash_sale_discount_vnd: number;
  total_shop_program_discount_vnd?: number;
  total_shop_voucher_discount_vnd: number;
  total_platform_voucher_discount_vnd: number;
  total_freeship_discount_vnd: number;
  total_shop_freeship_discount_vnd?: number;
  total_coins_discount_vnd: number;
  total_discount_vnd: number;
  platform_cost_vnd: number;
  shop_cost_vnd: number;
  total_shipping_fee_vnd: number;
  final_payable_vnd: number;
  warnings: string[];
}

export interface GwAddress {
  id: string;
  user_id?: string;
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
  is_default: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface GwProvince {
  code: string;
  name: string;
}

export interface GwWard {
  code: string;
  province_code: string;
  name: string;
  cod_supported?: boolean;
}

export interface GwCarrierQuote {
  carrier_code: string;
  carrier_name: string;
  service_code: string;
  service_name: string;
  fee_vnd: number;
  estimated_days: number;
  is_fallback: boolean;
  is_estimated?: boolean;
  cod_supported?: boolean;
}

export interface GwShopAllocation {
  order_id: string;
  shop_id: string;
  subtotal_vnd: number;
  shipping_fee_vnd: number;
  platform_discount_vnd?: number;
  sales_channel?: string;
  storefront_shop_id?: string;
}

export interface GwPayment {
  payment_id: string;
  order_group_id: string;
  order_id: string;
  user_id: string;
  amount_vnd: number;
  method: "COD" | "VIETQR" | "ZALOPAY" | string;
  status: "PENDING" | "SUCCEEDED" | "FAILED" | "EXPIRED" | "CANCELLED" | "REFUNDED" | string;
  qr_url: string;
  qr_code: string;
  pay_url?: string;
  app_trans_id?: string;
  provider: string;
  provider_ref: string;
  sales_channel?: string;
  storefront_shop_id?: string;
  shop_allocations: GwShopAllocation[];
  expires_at?: string;
  created_at?: string;
  updated_at?: string;
}

export interface GwCheckoutOrderSummary {
  id: string;
  order_code: string;
  checkout_group_id: string;
  shop_id: string;
  status: string;
  payment_method: string;
  payment_id: string;
  payment_status: string;
  total_payable_vnd: number;
  sales_channel?: string;
  storefront_shop_id?: string;
}

export interface GwCheckoutResponse {
  checkout_group_id: string;
  orders: GwCheckoutOrderSummary[];
  payment_id: string;
  payment_method: "COD" | "VIETQR" | "ZALOPAY" | string;
  payment_status: string;
  qr_url: string;
  qr_code: string;
  pay_url?: string;
  payment_expires_at?: string;
  total_payable_vnd: number;
  idempotent_replay: boolean;
  idempotency_key?: string;
  warnings?: string[];
  sales_channel?: string;
  storefront_shop_id?: string;
}
