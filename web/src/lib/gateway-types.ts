/**
 * Shared DTO types matching gateway REST contracts (docs/contracts.md).
 * All monetary amounts are 64-bit integer VND (*_vnd), timestamps are RFC3339 UTC strings.
 */

export interface GatewayErrorBody {
  code: string;
  message: string;
  trace_id?: string;
}

export interface GatewayErrorEnvelope {
  error: GatewayErrorBody;
}

export interface PaginatedResponse<T> {
  items: T[];
  page: number;
  page_size: number;
  total: number;
}

// --- Section 4.1: Auth, Profile, Addresses & Shops ---

export type UserRole = "buyer" | "seller" | "admin" | "super_admin";
export type UserStatus = "PENDING_VERIFICATION" | "ACTIVE" | "LOCKED";
export type ShopStatus = "PENDING" | "ACTIVE" | "REJECTED" | "SUSPENDED";

export interface GatewayUser {
  id: string;
  full_name: string;
  email: string;
  phone: string;
  role: UserRole;
  status: UserStatus;
  shop_id: string;
  phone_verified: boolean;
  cod_rejected_count: number;
  avatar_url: string;
  lock_reason?: string;
  created_at?: string;
  updated_at?: string;
}

export interface GatewayShop {
  id: string;
  owner_user_id: string;
  name: string;
  slug?: string;
  description: string;
  pickup_address_id: string;
  contact_phone: string;
  business_license_doc: string;
  status: ShopStatus | string;
  rejection_reason: string;
  bank_review_pending?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface GatewayPublicShop {
  id: string;
  name: string;
  slug: string;
  description: string;
  status: string;
  created_at?: string;
  updated_at?: string;
}

export interface GatewayAddress {
  id: string;
  user_id: string;
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
  legacy_province_code: string;
  legacy_district_code: string;
  legacy_ward_code: string;
  is_default: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface RegisterRequestBody {
  full_name: string;
  email: string;
  phone: string;
  password: string;
}

export interface RegisterResponse {
  user_id: string;
  email: string;
  status: string;
}

export interface VerifyOTPRequestBody {
  email: string;
  otp: string;
}

export interface VerifyOTPResponse {
  verified: boolean;
  user: GatewayUser | null;
}

export interface ResendOTPRequestBody {
  email: string;
}

export interface ResendOTPResponse {
  sent: boolean;
}

export interface ForgotPasswordRequestBody {
  email: string;
}

export interface ForgotPasswordResponse {
  sent: boolean;
}

export interface ResetPasswordRequestBody {
  token: string;
  new_password: string;
}

export interface ResetPasswordResponse {
  ok: boolean;
}

export interface LoginRequestBody {
  email: string;
  password: string;
}

export interface GatewayLoginResponse {
  access_token: string;
  refresh_token: string;
  expires_in_seconds: number;
  user: GatewayUser;
}

export interface GatewayRefreshResponse {
  access_token: string;
  refresh_token: string;
  expires_in_seconds: number;
}

export interface SessionResponse {
  user: GatewayUser | null;
  can_refresh?: boolean;
}

// --- Section 4.2: Categories, Products & Inventory ---

export type ProductStatus = "PENDING" | "APPROVED" | "REJECTED" | "HIDDEN" | "OUT_OF_STOCK";

export interface GatewayCategory {
  id: string;
  name: string;
  slug: string;
  parent_id: string;
  commission_rate_bps?: number;
  sort_order: number;
  created_at?: string;
  updated_at?: string;
}

export interface GatewaySKU {
  id: string;
  product_id: string;
  shop_id: string;
  product_name: string;
  sku_code: string;
  name: string;
  attributes: Record<string, string>;
  price_vnd: number;
  min_price_30d_vnd?: number;
  stock: number;
  reserved_stock: number;
  available_stock: number;
  version: number;
  weight_grams: number;
  image_url: string;
  category_id: string;
  commission_rate_bps?: number;
  is_active: boolean;
}

export interface GatewayProduct {
  id: string;
  shop_id: string;
  category_id: string;
  name: string;
  slug?: string;
  description: string;
  images: string[];
  status: ProductStatus | string;
  rejection_reason: string;
  min_price_vnd: number;
  max_price_vnd: number;
  skus: GatewaySKU[];
  created_at?: string;
  updated_at?: string;
}

export interface GatewaySellerSKUInput {
  id?: string;
  sku_code: string;
  name: string;
  attributes: Record<string, string>;
  price_vnd: number;
  stock: number;
  weight_grams: number;
  image_url: string;
}

// --- Section 4.3: Shopping Cart ---

export interface GatewayCartItem {
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
}

export interface GatewayCartShopGroup {
  shop_id: string;
  shop_name: string;
  items: GatewayCartItem[];
  subtotal_vnd: number;
  total_weight_grams: number;
}

export interface GatewayCart {
  user_id: string;
  shop_groups: GatewayCartShopGroup[];
  total_items: number;
  total_vnd: number;
}

export interface GatewayCartResponse {
  cart: GatewayCart;
  clamped_to_stock?: boolean;
}

// --- Section 4.5: Checkout & Orders ---

export type GatewayOrderStatus =
  | "PENDING"
  | "CONFIRMED"
  | "PREPARING"
  | "SHIPPING"
  | "DELIVERED"
  | "COMPLETED"
  | "CANCELLED"
  | "RETURN_REQUESTED"
  | "RETURNED";

export type GatewayPaymentMethod = "COD" | "VIETQR";

export interface GatewayOrderItem {
  id: string;
  order_id: string;
  sku_id: string;
  product_id: string;
  product_name: string;
  sku_name: string;
  unit_price_vnd: number;
  effective_unit_price_vnd: number;
  quantity: number;
  line_total_vnd: number;
  weight_grams: number;
  flash_sale_item_id: string;
  shop_program_item_id?: string;
}

export interface GatewayOrderTimelineEvent {
  id: string;
  order_id: string;
  from_status: GatewayOrderStatus | string;
  to_status: GatewayOrderStatus | string;
  actor_id: string;
  actor_role: string;
  note: string;
  created_at?: string;
}

export interface GatewayOrderAddressSnapshot {
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
}

export type GatewayReturnStatus =
  | "PENDING"
  | "AWAITING_SHIPMENT"
  | "IN_TRANSIT"
  | "RECEIVED"
  | "REFUNDING"
  | "REFUNDED"
  | "REJECTED"
  | "ESCALATED"
  | "EVIDENCE_REQUESTED"
  | "CLOSED";

export type ReturnSellerDecision = "ACCEPT_REFUND" | "ACCEPT_RETURN" | "REJECT";
export type ReturnAdminDecision = "REFUND" | "RETURN_AND_REFUND" | "REJECT" | "REQUEST_EVIDENCE";
export type ReturnFeeBearer = "SELLER" | "BUYER" | "PLATFORM";

export interface GatewayReturnEvent {
  id: string;
  from_status: GatewayReturnStatus | string;
  to_status: GatewayReturnStatus | string;
  actor_id: string;
  actor_role: string;
  note: string;
  created_at?: string;
}

export interface GatewayReturnRequest {
  id: string;
  order_id: string;
  order_code: string;
  buyer_id: string;
  shop_id: string;
  status: GatewayReturnStatus | string;
  reason: string;
  evidence_urls: string[];
  seller_response: string;
  seller_evidence_urls: string[];
  dispute_reason: string;
  admin_note: string;
  refund_amount_vnd: number;
  return_tracking_code: string;
  return_label_url: string;
  return_fee_vnd: number;
  return_fee_bearer: ReturnFeeBearer | string;
  goods_received: boolean;
  deadline_at?: string;
  created_at?: string;
  updated_at?: string;
  events: GatewayReturnEvent[];
}

export interface GatewayOrder {
  id: string;
  order_code: string;
  checkout_group_id: string;
  buyer_id: string;
  shop_id: string;
  status: GatewayOrderStatus | string;
  payment_method: GatewayPaymentMethod | string;
  payment_id: string;
  payment_status: string;
  subtotal_vnd: number;
  flash_sale_discount_vnd: number;
  shop_program_discount_vnd?: number;
  shop_voucher_discount_vnd: number;
  shop_freeship_discount_vnd?: number;
  platform_voucher_discount_vnd: number;
  freeship_discount_vnd: number;
  coins_discount_vnd: number;
  shipping_fee_vnd: number;
  commission_vnd: number;
  payment_fee_vnd?: number;
  payment_fee_bps?: number;
  needs_manual_review?: boolean;
  total_payable_vnd: number;
  carrier_code: string;
  service_code: string;
  tracking_code: string;
  label_url: string;
  cancel_reason: string;
  shipping_address?: GatewayOrderAddressSnapshot;
  items: GatewayOrderItem[];
  timeline: GatewayOrderTimelineEvent[];
  payment_expires_at?: string;
  delivered_at?: string;
  return_request_id?: string;
  return_status?: GatewayReturnStatus | string;
  created_at?: string;
  updated_at?: string;
}

// --- Section 4.4: Promotions (Vouchers, Flash Sales, Coins & Discount Preview) ---

export type VoucherScope = "PLATFORM" | "SHOP" | "FREESHIP";
export type DiscountType = "FIXED" | "PERCENT";
export type VoucherStatus = "ACTIVE" | "PAUSED" | "DELETED";

export interface GatewayVoucher {
  id: string;
  code: string;
  name: string;
  scope: VoucherScope | string;
  shop_id: string;
  discount_type: DiscountType | string;
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
  status?: VoucherStatus | string;
  category_ids?: string[];
  saved?: boolean;
  eligible?: boolean;
  ineligible_reason?: string;
}

export interface GatewayVouchersResponse {
  vouchers: GatewayVoucher[];
}

export interface GatewaySaveVoucherResponse {
  saved: boolean;
  voucher: GatewayVoucher | null;
}

export interface GatewayFlashSaleItem {
  id: string;
  flash_sale_id: string;
  shop_id: string;
  sku_id: string;
  flash_price_vnd: number;
  original_price_vnd?: number;
  total_slots: number;
  remaining_slots: number;
  per_user_limit: number;
  status: "PENDING" | "APPROVED" | "REJECTED" | string;
  rejection_reason: string;
  created_at?: string;
}

export interface GatewayFlashSale {
  id: string;
  name: string;
  starts_at?: string;
  ends_at?: string;
  min_discount_percent?: number;
  category_ids?: string[];
  items: GatewayFlashSaleItem[];
}

export interface GatewayFlashSalesResponse {
  flash_sales: GatewayFlashSale[];
}

export type ShopDiscountProgramStatus = "SCHEDULED" | "ACTIVE" | "ENDED" | "CANCELLED";

export interface GatewayShopDiscountProgramItem {
  id: string;
  program_id: string;
  sku_id: string;
  original_price_vnd: number;
  discounted_price_vnd: number;
  discount_percent: number;
  per_user_limit: number;
  min_price_30d_vnd?: number;
  price_inflation_flagged?: boolean;
}

export interface GatewayShopDiscountProgram {
  id: string;
  shop_id: string;
  name: string;
  starts_at?: string;
  ends_at?: string;
  status: ShopDiscountProgramStatus | string;
  items: GatewayShopDiscountProgramItem[];
  created_at?: string;
  updated_at?: string;
}

export interface GatewayShopDiscountProgramResponse {
  program: GatewayShopDiscountProgram;
  warnings?: string[];
}

export interface GatewayActiveShopDiscount {
  item_id: string;
  program_id: string;
  program_name?: string;
  shop_id: string;
  sku_id: string;
  original_price_vnd: number;
  discounted_price_vnd: number;
  discount_percent: number;
  per_user_limit: number;
  starts_at?: string;
  ends_at?: string;
}

export interface GatewayActiveShopDiscountsResponse {
  discounts: GatewayActiveShopDiscount[];
}

export interface GatewayCoinTransaction {
  id: string;
  user_id: string;
  delta: number;
  reason: string;
  reference_id: string;
  created_at?: string;
}

export interface GatewayCoinBalance {
  user_id: string;
  balance: number;
  recent_transactions: GatewayCoinTransaction[];
}

export interface GatewayCreditCoinsResponse {
  user_id: string;
  balance: number;
  already_credited: boolean;
}

export interface GatewayDiscountLineItem {
  sku_id: string;
  quantity: number;
  flash_sale_item_id?: string;
}

export interface GatewayShopDiscountInput {
  shop_id: string;
  shipping_fee_vnd: number;
  items: GatewayDiscountLineItem[];
}

export interface GatewayPreviewDiscountsRequest {
  shop_items: GatewayShopDiscountInput[];
  shop_voucher_ids: Record<string, string>;
  platform_voucher_id: string;
  freeship_voucher_id: string;
  coins_to_use: number;
}

export interface GatewayShopDiscountBreakdown {
  shop_id: string;
  subtotal_vnd: number;
  flash_sale_discount_vnd: number;
  shop_program_discount_vnd?: number;
  shop_voucher_id: string;
  shop_voucher_discount_vnd: number;
  shop_freeship_discount_vnd?: number;
  platform_voucher_discount_vnd: number;
  freeship_discount_vnd: number;
  coins_discount_vnd: number;
  shipping_fee_vnd: number;
  final_shipping_fee_vnd: number;
  final_payable_vnd: number;
  applied_shop_program_item_ids?: string[];
}

export interface GatewayDiscountBreakdown {
  shops: GatewayShopDiscountBreakdown[];
  total_subtotal_vnd: number;
  total_flash_sale_discount_vnd: number;
  total_shop_program_discount_vnd?: number;
  total_shop_voucher_discount_vnd: number;
  total_shop_freeship_discount_vnd?: number;
  total_platform_voucher_discount_vnd: number;
  total_freeship_discount_vnd: number;
  total_coins_discount_vnd: number;
  total_discount_vnd: number;
  platform_cost_vnd: number;
  shop_cost_vnd: number;
  total_shipping_fee_vnd: number;
  final_payable_vnd: number;
  warnings: string[];
}

export interface GatewayPreviewDiscountsResponse {
  breakdown: GatewayDiscountBreakdown;
}

// --- Section 4.5 (cont.): Checkout Requests & Responses ---

export interface GatewayShopShippingChoice {
  shop_id: string;
  carrier_code: string;
  service_code: string;
}

export interface GatewayCheckoutItemInput {
  sku_id: string;
  quantity: number;
  flash_sale_item_id?: string;
}

export interface GatewayCheckoutRequest {
  address_id: string;
  from_cart: boolean;
  items?: GatewayCheckoutItemInput[];
  shop_voucher_ids?: Record<string, string>;
  platform_voucher_id?: string;
  freeship_voucher_id?: string;
  coins_to_use: number;
  payment_method: GatewayPaymentMethod;
  shipping_choices: GatewayShopShippingChoice[];
}

export interface GatewayCheckoutResponse {
  checkout_group_id: string;
  orders: GatewayOrder[];
  payment_id: string;
  payment_method: GatewayPaymentMethod | string;
  payment_status: string;
  qr_url: string;
  qr_code: string;
  payment_expires_at?: string;
  total_payable_vnd: number;
  idempotent_replay: boolean;
  warnings?: string[];
}

// --- Section 4.6: Payments, Ledger, Reconciliation, Refunds & Seller Balance ---

export type GatewayPaymentStatus =
  "PENDING" | "SUCCEEDED" | "FAILED" | "EXPIRED" | "CANCELLED" | "REFUNDED";

export interface GatewayShopAllocation {
  order_id: string;
  shop_id: string;
  subtotal_vnd: number;
  commission_vnd: number;
  shipping_fee_vnd: number;
  platform_discount_vnd?: number;
}

export interface GatewayPayment {
  payment_id: string;
  order_group_id: string;
  order_id: string;
  user_id: string;
  amount_vnd: number;
  method: GatewayPaymentMethod | string;
  status: GatewayPaymentStatus | string;
  qr_url: string;
  qr_code: string;
  provider: string;
  provider_ref: string;
  shop_allocations: GatewayShopAllocation[];
  expires_at?: string;
  created_at?: string;
  updated_at?: string;
}

export interface GatewayPaymentResponse {
  payment: GatewayPayment;
}

export interface GatewaySellerBalance {
  shop_id: string;
  pending_vnd: number;
  available_vnd: number;
  total_earned_vnd: number;
}

export interface GatewayLedgerAccount {
  account_code: string;
  account_type: string;
  debit_total_vnd: number;
  credit_total_vnd: number;
  balance_vnd: number;
}

export interface GatewayLedgerSummary {
  total_debits_vnd: number;
  total_credits_vnd: number;
  is_balanced: boolean;
  accounts: GatewayLedgerAccount[];
}

export interface GatewayReconcileItem {
  payment_id: string;
  provider_tx_id: string;
  local_amount_vnd: number;
  provider_amount_vnd: number;
  status: "MATCHED" | "MISSING_LOCAL" | "MISSING_PROVIDER" | "AMOUNT_MISMATCH" | string;
  note: string;
}

export interface GatewayReconcileResponse {
  run_id: string;
  matched_count: number;
  missing_local_count: number;
  missing_provider_count: number;
  amount_mismatch_count: number;
  items: GatewayReconcileItem[];
}

export interface GatewayRefund {
  refund_id: string;
  payment_id: string;
  order_id: string;
  amount_vnd: number;
  status: "PENDING" | "DONE" | "FAILED" | string;
  reason: string;
  idempotency_key: string;
  created_at?: string;
  bank_code?: string;
  bank_account_masked?: string;
  bank_account_name?: string;
  bank_account_number?: string;
  provider_refund_id?: string;
  is_late_payment?: boolean;
  settled_at?: string;
  settlement_note?: string;
}

export interface GatewayRefundResponse {
  refund: GatewayRefund;
  already_existed: boolean;
}

export interface GatewaySettleRefundResponse {
  refund: GatewayRefund;
  already_settled: boolean;
}

export type GatewayReconciliationAction = "ASSIGN" | "REFUND" | "SUSPEND";

export interface GatewayReconciliationQueueItem {
  id: string;
  webhook_event_id: string;
  provider: string;
  provider_tx_id: string;
  expected_payment_id: string;
  order_ref: string;
  expected_amount_vnd: number;
  actual_amount_vnd: number;
  reason: string;
  resolution_status: string;
  notes: string;
  created_at?: string;
  resolved_at?: string;
}

export interface GatewayResolveReconciliationResponse {
  item: GatewayReconciliationQueueItem;
  refund?: GatewayRefund | null;
  already_resolved: boolean;
}

// --- Section 4.7: Shipping (Locations, Fee Quotes & Shipments) ---

export interface GatewayProvince {
  code: string;
  name: string;
}

export interface GatewayWard {
  code: string;
  province_code: string;
  name: string;
}

export interface GatewayCarrierQuote {
  carrier_code: string;
  carrier_name: string;
  service_code: string;
  service_name: string;
  fee_vnd: number;
  estimated_days: number;
  is_fallback: boolean;
}

export interface GatewayCalculateFeeResponse {
  quotes: GatewayCarrierQuote[];
}

export interface GatewayShipment {
  shipment_id: string;
  order_id: string;
  shop_id: string;
  carrier_code: string;
  service_code: string;
  tracking_code: string;
  label_url: string;
  status: string;
  fee_vnd: number;
  cod_amount_vnd: number;
  direction?: "FORWARD" | "RETURN" | string;
  return_request_id?: string;
  redelivery_count?: number;
  created_at?: string;
  updated_at?: string;
}

export interface GatewayShipmentEvent {
  id: string;
  shipment_id: string;
  status: string;
  raw_status: string;
  description: string;
  occurred_at?: string;
}

export interface GatewayShipmentResponse {
  shipment: GatewayShipment | null;
  events: GatewayShipmentEvent[];
}

export type GatewayShippingRegion = "SAME_PROVINCE" | "SAME_REGION" | "CROSS_REGION";

export interface GatewayFallbackRate {
  region: GatewayShippingRegion | string;
  base_fee_vnd: number;
  base_weight_grams: number;
  extra_fee_vnd_per_500g: number;
}

export interface GatewayCarrierConfig {
  carrier_code: string;
  name: string;
  enabled: boolean;
  token_configured: boolean;
  fallback_rates: GatewayFallbackRate[];
  updated_at?: string;
}

export interface GatewayShopShippingSettings {
  shop_id: string;
  enabled_carrier_codes: string[];
  default_weight_grams: number;
  default_length_cm: number;
  default_width_cm: number;
  default_height_cm: number;
  free_ship_enabled?: boolean;
  free_ship_min_order_vnd?: number;
  free_ship_max_vnd?: number;
  updated_at?: string;
}

export interface GatewayAdminTestCarrierResponse {
  ok: boolean;
  message: string;
}

// --- Section 4.8: Notifications & Admin Audit Logs ---

export type GatewayNotificationCategory = "ORDER" | "PROMOTION" | "SYSTEM";

export interface GatewayNotification {
  id: string;
  user_id: string;
  category: GatewayNotificationCategory | string;
  title: string;
  body: string;
  link: string;
  event_id: string;
  is_read: boolean;
  created_at?: string;
}

export interface GatewayNotificationsResponse {
  items: GatewayNotification[];
  unread_count: number;
  page: number;
  page_size: number;
  total: number;
}

export interface GatewayNotificationPreferences {
  user_id: string;
  order_in_app: boolean;
  order_email: boolean;
  order_push: boolean;
  promotion_in_app: boolean;
  promotion_email: boolean;
  promotion_push: boolean;
  system_in_app: boolean;
  system_email: boolean;
  system_push: boolean;
}

export interface GatewayAuditLog {
  id: string;
  actor_id: string;
  actor_role: string;
  action: string;
  target_type: string;
  target_id: string;
  before_json: string;
  after_json: string;
  reason: string;
  ip_address: string;
  created_at?: string;
}

// --- Wave 2b: Shop Bank Accounts, Seller Ledger, Fee Schedules & Reports ---

export type ShopBankAccountStatus =
  "PENDING_OTP" | "PENDING_ADMIN_REVIEW" | "VERIFIED" | "REJECTED";

export interface GatewayShopBankAccount {
  id: string;
  shop_id: string;
  bank_code: string;
  bank_name: string;
  account_number_masked: string;
  account_holder_name: string;
  status: ShopBankAccountStatus | string;
  holder_name_matched: boolean;
  review_note?: string;
  verified_at?: string;
  payout_locked_until?: string;
  created_at?: string;
  updated_at?: string;
}

export interface GatewayShopBankAccountResponse {
  bank_account: GatewayShopBankAccount | null;
}

export interface GatewaySellerLedgerEntry {
  id: string;
  transaction_id: string;
  account_code: string;
  debit_vnd: number;
  credit_vnd: number;
  delta_vnd: number;
  reference_type: string;
  reference_id: string;
  description: string;
  created_at?: string;
}

export interface GatewayCategoryFeeSchedule {
  id: string;
  category_id: string;
  category_name?: string;
  commission_rate_bps: number;
  effective_from: string;
  created_by?: string;
  note?: string;
  created_at?: string;
}

export interface GatewayPaymentFeeConfigItem {
  id: string;
  payment_method: GatewayPaymentMethod | string;
  fee_bps: number;
  effective_from: string;
  created_by?: string;
  note?: string;
  created_at?: string;
}

export interface GatewayPaymentFeeConfigResponse {
  active_configs?: GatewayPaymentFeeConfigItem[];
  history?: GatewayPaymentFeeConfigItem[];
}

export interface GatewayOrderStatusCount {
  status: GatewayOrderStatus | string;
  count: number;
}

export interface GatewayDailyReportPoint {
  date: string;
  gmv_vnd: number;
  net_revenue_vnd: number;
  order_count: number;
  completed_count?: number;
  cancelled_count?: number;
  returned_count?: number;
  commission_vnd: number;
  payment_fee_vnd: number;
}

export interface GatewayTopProductStat {
  product_id: string;
  product_name: string;
  units_sold: number;
  revenue_vnd: number;
  order_count?: number;
}

export interface GatewaySellerReport {
  shop_id: string;
  from_date: string;
  to_date: string;
  gmv_vnd: number;
  net_revenue_vnd: number;
  commission_vnd: number;
  payment_fee_vnd: number;
  total_orders: number;
  completed_orders: number;
  cancelled_orders: number;
  returned_orders: number;
  cancel_rate_bps: number;
  return_rate_bps: number;
  orders_by_status: GatewayOrderStatusCount[];
  daily: GatewayDailyReportPoint[];
  top_products: GatewayTopProductStat[];
}

export interface GatewaySellerReportResponse {
  report: GatewaySellerReport;
}

export interface GatewayDailyUserShopPoint {
  date: string;
  new_users: number;
  new_shops: number;
}

export interface GatewayPlatformUserStats {
  total_users: number;
  active_users: number;
  new_users: number;
  total_shops: number;
  active_shops: number;
  pending_shops?: number;
  new_shops: number;
  daily: GatewayDailyUserShopPoint[];
}

export interface GatewayPlatformReport {
  from_date: string;
  to_date: string;
  gmv_vnd: number;
  net_revenue_vnd: number;
  commission_vnd: number;
  payment_fee_vnd: number;
  platform_subsidy_vnd: number;
  total_orders: number;
  completed_orders: number;
  cancelled_orders: number;
  returned_orders: number;
  cancel_rate_bps: number;
  return_rate_bps: number;
  orders_by_status: GatewayOrderStatusCount[];
  daily: GatewayDailyReportPoint[];
  top_products: GatewayTopProductStat[];
  user_stats?: GatewayPlatformUserStats | null;
}

export interface GatewayPlatformReportResponse {
  report: GatewayPlatformReport;
}
