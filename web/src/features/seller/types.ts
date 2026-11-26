import type {
  GatewayOrder,
  GatewayOrderStatus,
  GatewayProduct,
  ProductStatus,
  ShopStatus,
} from "@/lib/gateway-types";
import {
  GATEWAY_ORDER_STATUS_LABELS,
  GATEWAY_SHIPMENT_STATUS_LABELS,
  formatCarrierLabel,
  formatVnd,
} from "../../lib/format.ts";

export const EMPTY_DASH = "—";

export type UiStateOverride = "loading" | "empty" | "error" | null;

export function parseUiState(param: string | null | undefined): UiStateOverride {
  if (param === "loading" || param === "empty" || param === "error") return param;
  return null;
}

export type SellerPillTone = "neutral" | "warn" | "pos" | "neg";

export interface StatusMeta {
  label: string;
  tone: SellerPillTone;
}

export const SELLER_SHOP_STATUS_META: Record<ShopStatus | string, StatusMeta> = {
  ACTIVE: { label: "Đang hoạt động", tone: "pos" },
  PENDING: { label: "Chờ duyệt", tone: "warn" },
  REJECTED: { label: "Bị từ chối", tone: "neg" },
  SUSPENDED: { label: "Tạm ngưng", tone: "neg" },
};

export const SELLER_PRODUCT_STATUS_META: Record<ProductStatus | string, StatusMeta> = {
  APPROVED: { label: "Đang bán", tone: "pos" },
  PENDING: { label: "Chờ duyệt", tone: "warn" },
  REJECTED: { label: "Bị từ chối", tone: "neg" },
  HIDDEN: { label: "Đang ẩn", tone: "neutral" },
  OUT_OF_STOCK: { label: "Hết hàng", tone: "neg" },
};

export const SELLER_ORDER_STATUS_META: Record<GatewayOrderStatus | string, StatusMeta> = {
  PENDING: { label: GATEWAY_ORDER_STATUS_LABELS.PENDING, tone: "warn" },
  CONFIRMED: { label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED, tone: "warn" },
  PREPARING: { label: GATEWAY_ORDER_STATUS_LABELS.PREPARING, tone: "neutral" },
  SHIPPING: { label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING, tone: "neutral" },
  DELIVERED: { label: GATEWAY_ORDER_STATUS_LABELS.DELIVERED, tone: "pos" },
  COMPLETED: { label: GATEWAY_ORDER_STATUS_LABELS.COMPLETED, tone: "pos" },
  CANCELLED: { label: GATEWAY_ORDER_STATUS_LABELS.CANCELLED, tone: "neg" },
  RETURN_REQUESTED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURN_REQUESTED, tone: "warn" },
  RETURNED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURNED, tone: "neg" },
};

export const SHIPMENT_STATUS_LABELS = GATEWAY_SHIPMENT_STATUS_LABELS;

export const PAYMENT_METHOD_TEXT: Record<string, string> = {
  COD: "Thu hộ COD",
  VIETQR: "Chuyển khoản VietQR",
};

export const CARRIER_OPTIONS: Array<{
  carrierCode: string;
  carrierLabel: string;
  services: Array<{ serviceCode: string; serviceLabel: string }>;
}> = [
  {
    carrierCode: "ghn",
    carrierLabel: formatCarrierLabel("ghn"),
    services: [
      { serviceCode: "STANDARD", serviceLabel: "GHN Tiêu chuẩn" },
      { serviceCode: "EXPRESS", serviceLabel: "GHN Nhanh" },
    ],
  },
  {
    carrierCode: "mock_express",
    carrierLabel: formatCarrierLabel("mock_express"),
    services: [
      { serviceCode: "STANDARD", serviceLabel: "Gói Tiêu chuẩn" },
      { serviceCode: "FAST", serviceLabel: "Gói Giao nhanh" },
    ],
  },
];

/**
 * Allowed seller product status transitions matching ProductService.resolveUpdatedProductStatus.
 * PENDING products cannot be hidden directly by seller (only APPROVED/OUT_OF_STOCK can be hidden).
 */
export function getAllowedSellerStatuses(
  currentStatus: string
): Array<{ value: string; label: string }> {
  switch (currentStatus) {
    case "APPROVED":
      return [
        { value: "APPROVED", label: "Đang bán" },
        { value: "HIDDEN", label: "Tạm ẩn khỏi sàn" },
      ];
    case "OUT_OF_STOCK":
      return [
        { value: "OUT_OF_STOCK", label: "Hết hàng" },
        { value: "HIDDEN", label: "Tạm ẩn khỏi sàn" },
      ];
    case "HIDDEN":
      return [
        { value: "HIDDEN", label: "Đang ẩn" },
        { value: "APPROVED", label: "Mở bán lại" },
      ];
    case "PENDING":
      return [{ value: "PENDING", label: "Chờ duyệt" }];
    case "REJECTED":
      return [
        { value: "REJECTED", label: "Bị từ chối" },
        { value: "PENDING", label: "Gửi duyệt lại" },
      ];
    default:
      return [{ value: currentStatus, label: getProductStatusMeta(currentStatus).label }];
  }
}

export function resolveOrderShippingDefaults(
  order?: { carrier_code?: string; service_code?: string } | null
): { carrierCode: string; serviceCode: string } {
  const rawCarrier = (order?.carrier_code ?? "").trim();
  const rawService = (order?.service_code ?? "").trim();

  if (!rawCarrier) {
    return {
      carrierCode: CARRIER_OPTIONS[0].carrierCode,
      serviceCode: rawService || CARRIER_OPTIONS[0].services[0].serviceCode,
    };
  }

  const matchedCarrier = CARRIER_OPTIONS.find(
    (c) => c.carrierCode.toLowerCase() === rawCarrier.toLowerCase()
  );
  if (!matchedCarrier) {
    return {
      carrierCode: rawCarrier,
      serviceCode: rawService || "STANDARD",
    };
  }

  const matchedService = rawService
    ? matchedCarrier.services.find((s) => s.serviceCode.toLowerCase() === rawService.toLowerCase())
    : undefined;

  return {
    carrierCode: matchedCarrier.carrierCode,
    serviceCode:
      matchedService?.serviceCode || rawService || matchedCarrier.services[0].serviceCode,
  };
}

export function buildCarrierSelectOptions(
  order?: { carrier_code?: string; service_code?: string } | null
): typeof CARRIER_OPTIONS {
  const defaults = resolveOrderShippingDefaults(order);
  const hasCarrier = CARRIER_OPTIONS.some((c) => c.carrierCode === defaults.carrierCode);
  if (!hasCarrier) {
    return [
      ...CARRIER_OPTIONS,
      {
        carrierCode: defaults.carrierCode,
        carrierLabel: formatCarrierLabel(defaults.carrierCode),
        services: [{ serviceCode: defaults.serviceCode, serviceLabel: defaults.serviceCode }],
      },
    ];
  }
  return CARRIER_OPTIONS.map((c) => {
    if (
      c.carrierCode === defaults.carrierCode &&
      Boolean(defaults.serviceCode) &&
      !c.services.some((s) => s.serviceCode === defaults.serviceCode)
    ) {
      return {
        ...c,
        services: [
          ...c.services,
          { serviceCode: defaults.serviceCode, serviceLabel: defaults.serviceCode },
        ],
      };
    }
    return c;
  });
}

export function getShopStatusMeta(status: string): StatusMeta {
  return SELLER_SHOP_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getProductStatusMeta(status: string): StatusMeta {
  return SELLER_PRODUCT_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getOrderStatusMeta(status: string): StatusMeta {
  return SELLER_ORDER_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

/**
 * Seller-facing order status label honoring Wave 2a §1.1 & Wave 3b-1 E18:
 * - No tracking_code + shipment_job_status QUEUED -> "Đang chờ tạo lại với hãng"
 * - No tracking_code + shipment_job_status FAILED -> "Tạo vận đơn thất bại"
 * - PREPARING with tracking_code -> "Chờ hãng lấy hàng" (awaiting carrier pickup)
 */
export function getSellerOrderDisplayMeta(
  order: Pick<GatewayOrder, "status" | "tracking_code"> & {
    shipment_job_status?: string;
  }
): StatusMeta {
  const normStatus = (order.status ?? "").trim().toUpperCase();
  const hasTracking = Boolean((order.tracking_code ?? "").trim());
  const jobStatus = (order.shipment_job_status ?? "").trim().toUpperCase();
  if ((normStatus === "CONFIRMED" || normStatus === "PREPARING") && !hasTracking) {
    if (jobStatus === "QUEUED") {
      return { label: "Đang chờ tạo lại với hãng", tone: "warn" };
    }
    if (jobStatus === "FAILED") {
      return { label: "Tạo vận đơn thất bại", tone: "neg" };
    }
  }
  if (normStatus === "PREPARING" && hasTracking) {
    return { label: "Chờ hãng lấy hàng", tone: "warn" };
  }
  return getOrderStatusMeta(normStatus);
}

/**
 * Valid seller actions by order state machine (docs/contracts.md §6 & E18):
 * - PENDING: cancel
 * - CONFIRMED: confirm (→ PREPARING, unless async shipment job is already QUEUED/FAILED), ship (creates shipment, stays PREPARING with tracking_code), cancel
 * - PREPARING: ship (if no tracking_code yet — allowed even when QUEUED so orders never get stuck; server is idempotent), cancel
 */
export function canSellerConfirmOrder(status: string, shipmentJobStatus?: string): boolean {
  if ((status ?? "").trim().toUpperCase() !== "CONFIRMED") return false;
  const job = (shipmentJobStatus ?? "").trim().toUpperCase();
  return job !== "QUEUED" && job !== "FAILED";
}

export function canSellerShipOrder(status: string, trackingCode?: string): boolean {
  if (Boolean((trackingCode ?? "").trim())) {
    return false;
  }
  return status === "CONFIRMED" || status === "PREPARING";
}

export function canSellerCancelOrder(status: string): boolean {
  return status === "PENDING" || status === "CONFIRMED" || status === "PREPARING";
}

/**
 * Computes seller-borne deductions for a sub-order:
 * flash sale + shop discount program + shop voucher + shop freeship + platform commission + payment fee.
 */
export function computeOrderDeductionsVnd(
  order: Pick<
    GatewayOrder,
    "flash_sale_discount_vnd" | "shop_voucher_discount_vnd" | "commission_vnd"
  > &
    Partial<
      Pick<
        GatewayOrder,
        "shop_program_discount_vnd" | "shop_freeship_discount_vnd" | "payment_fee_vnd"
      >
    >
): number {
  return Math.trunc(
    (order.flash_sale_discount_vnd ?? 0) +
      (order.shop_program_discount_vnd ?? 0) +
      (order.shop_voucher_discount_vnd ?? 0) +
      (order.shop_freeship_discount_vnd ?? 0) +
      (order.commission_vnd ?? 0) +
      (order.payment_fee_vnd ?? 0)
  );
}

/**
 * Computes integer VND net receivable for a shop sub-order: subtotal - deductions.
 */
export function computeOrderNetVnd(
  order: Pick<
    GatewayOrder,
    "subtotal_vnd" | "flash_sale_discount_vnd" | "shop_voucher_discount_vnd" | "commission_vnd"
  > &
    Partial<
      Pick<
        GatewayOrder,
        "shop_program_discount_vnd" | "shop_freeship_discount_vnd" | "payment_fee_vnd"
      >
    >
): number {
  const net = order.subtotal_vnd - computeOrderDeductionsVnd(order);
  return Math.max(0, Math.trunc(net));
}

export interface SellerOrderBreakdownLine {
  key: string;
  label: string;
  /** Positive for gross merchandise subtotal; negative for seller-funded discounts & fees. */
  amountVnd: number;
  isDeduction: boolean;
}

/**
 * Pure seller settlement breakdown lines that sum strictly to `computeOrderNetVnd(order)`
 * (clamped >= 0), including Wave 2b shop_program_discount_vnd, shop_freeship_discount_vnd, and payment_fee_vnd.
 */
export function buildSellerOrderBreakdownLines(
  order: Pick<
    GatewayOrder,
    "subtotal_vnd" | "flash_sale_discount_vnd" | "shop_voucher_discount_vnd" | "commission_vnd"
  > &
    Partial<
      Pick<
        GatewayOrder,
        "shop_program_discount_vnd" | "shop_freeship_discount_vnd" | "payment_fee_vnd"
      >
    >
): SellerOrderBreakdownLine[] {
  const lines: SellerOrderBreakdownLine[] = [
    {
      key: "subtotal",
      label: "Tổng tiền hàng",
      amountVnd: Math.trunc(order.subtotal_vnd ?? 0),
      isDeduction: false,
    },
  ];
  const flash = Math.trunc(order.flash_sale_discount_vnd ?? 0);
  if (flash > 0) {
    lines.push({
      key: "flash_sale",
      label: "Giảm giá Flash sale",
      amountVnd: -flash,
      isDeduction: true,
    });
  }
  const shopProg = Math.trunc(order.shop_program_discount_vnd ?? 0);
  if (shopProg > 0) {
    lines.push({
      key: "shop_program",
      label: "Giảm giá chương trình gian hàng",
      amountVnd: -shopProg,
      isDeduction: true,
    });
  }
  lines.push({
    key: "shop_voucher",
    label: "Voucher gian hàng tài trợ",
    amountVnd: -Math.trunc(order.shop_voucher_discount_vnd ?? 0),
    isDeduction: true,
  });
  const shopFreeship = Math.trunc(order.shop_freeship_discount_vnd ?? 0);
  if (shopFreeship > 0) {
    lines.push({
      key: "shop_freeship",
      label: "Gian hàng hỗ trợ phí ship",
      amountVnd: -shopFreeship,
      isDeduction: true,
    });
  }
  lines.push({
    key: "commission",
    label: "Phí hoa hồng sàn",
    amountVnd: -Math.trunc(order.commission_vnd ?? 0),
    isDeduction: true,
  });
  const payFee = Math.trunc(order.payment_fee_vnd ?? 0);
  if (payFee > 0) {
    lines.push({
      key: "payment_fee",
      label: "Phí thanh toán",
      amountVnd: -payFee,
      isDeduction: true,
    });
  }
  return lines;
}

export function parseSellerFreeshipSettings(input: {
  enabled: boolean;
  minOrderRaw: string;
  maxVndRaw: string;
}): { ok: true; minOrderVnd: number; maxVnd: number } | { ok: false; message: string } {
  if (!input.enabled) {
    return { ok: true, minOrderVnd: 0, maxVnd: 0 };
  }
  const minTrim = input.minOrderRaw.trim();
  const minNum = Number(minTrim);
  if (minTrim === "" || !Number.isFinite(minNum) || !Number.isInteger(minNum) || minNum < 0) {
    return {
      ok: false,
      message:
        "Đơn tối thiểu áp dụng Freeship gian hàng phải là số nguyên không âm (0₫ áp dụng cho mọi đơn).",
    };
  }
  const maxTrim = input.maxVndRaw.trim();
  const maxNum = Number(maxTrim);
  if (maxTrim === "" || !Number.isFinite(maxNum) || !Number.isInteger(maxNum) || maxNum <= 0) {
    return {
      ok: false,
      message:
        "Mức hỗ trợ phí ship tối đa mỗi đơn phải là số nguyên lớn hơn 0₫ khi bật Freeship gian hàng.",
    };
  }
  return { ok: true, minOrderVnd: minNum, maxVnd: maxNum };
}

/**
 * Translates English backend warnings from PromotionService shop discount create/update
 * into natural Vietnamese with product & SKU names instead of raw UUIDs.
 */
export function translateShopDiscountWarning(
  rawWarning: string,
  skuLookup?: Map<string, { product: { name: string }; sku: { sku_code: string; name?: string } }>
): string {
  const clean = (rawWarning ?? "").trim();
  if (!clean) return "";

  const skuMatch = /^SKU\s+([0-9a-f-]{8,})\s+/i.exec(clean);
  const skuId = skuMatch?.[1] ?? "";
  const matched = skuId && skuLookup ? skuLookup.get(skuId) : undefined;
  const skuLabel = matched
    ? `${matched.product.name} (${matched.sku.sku_code})`
    : skuId
      ? `SKU ${skuId.slice(0, 8)}`
      : "Một SKU";

  const inflationMatch =
    /had a lower price\s*\((\d+)\s*VND\)\s*in the last 30 days than current price\s*\((\d+)\s*VND\)/i.exec(
      clean
    );
  if (inflationMatch) {
    const min30d = Number.parseInt(inflationMatch[1], 10);
    const orig = Number.parseInt(inflationMatch[2], 10);
    return `${skuLabel} từng có giá thấp hơn trong 30 ngày qua (${formatVnd(min30d)} so với giá gốc hiện tại ${formatVnd(orig)}) và đã được đánh dấu cảnh báo tăng giá gốc.`;
  }

  if (/overlaps with an approved flash sale/i.test(clean)) {
    return `${skuLabel} trùng lịch với một khung giờ Flash sale đã duyệt; giá Flash sale sẽ tự động được ưu tiên trong khung giờ đó.`;
  }

  return matched ? clean.replace(skuId, skuLabel) : clean;
}

export const PAYMENT_NOT_INITIALIZED_SHIP_MESSAGE =
  "Đơn đang khởi tạo thanh toán, vui lòng thử lại sau ít phút.";

export const SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE =
  "Gian hàng chưa cấu hình địa chỉ lấy hàng hợp lệ. Vui lòng cập nhật địa chỉ lấy hàng trong Hồ sơ gian hàng trước khi tạo vận đơn.";

/**
 * Maps seller order ship errors into natural Vietnamese, explicitly handling
 * PAYMENT_NOT_INITIALIZED and SHOP_PICKUP_ADDRESS_REQUIRED (409 FAILED_PRECONDITION).
 */
export function translateSellerShipError(err: unknown): string {
  if (err && typeof err === "object") {
    const e = err as { code?: unknown; message?: unknown };
    const code = typeof e.code === "string" ? e.code.trim().toUpperCase() : "";
    if (code === "PAYMENT_NOT_INITIALIZED") {
      return PAYMENT_NOT_INITIALIZED_SHIP_MESSAGE;
    }
    if (code === "SHOP_PICKUP_ADDRESS_REQUIRED") {
      return SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE;
    }
    if (typeof e.message === "string" && e.message.trim().length > 0) {
      return e.message.trim();
    }
  }
  return "Không thể tạo vận đơn giao hàng lúc này.";
}

export const VOUCHER_STATUS_META: Record<string, StatusMeta> = {
  ACTIVE: { label: "Đang bật", tone: "pos" },
  PAUSED: { label: "Tạm dừng", tone: "warn" },
  DELETED: { label: "Đã xóa", tone: "neg" },
};

export const DISCOUNT_PROGRAM_STATUS_META: Record<string, StatusMeta> = {
  SCHEDULED: { label: "Chờ chạy", tone: "warn" },
  ACTIVE: { label: "Đang chạy", tone: "pos" },
  ENDED: { label: "Đã kết thúc", tone: "neutral" },
  CANCELLED: { label: "Đã hủy", tone: "neg" },
};

export const FLASH_ITEM_STATUS_META: Record<string, StatusMeta> = {
  PENDING: { label: "Chờ duyệt", tone: "warn" },
  APPROVED: { label: "Đã duyệt", tone: "pos" },
  REJECTED: { label: "Từ chối", tone: "neg" },
};

export const BANK_ACCOUNT_STATUS_META: Record<string, StatusMeta> = {
  PENDING_OTP: { label: "Chờ xác thực OTP", tone: "warn" },
  PENDING_ADMIN_REVIEW: { label: "Chờ quản trị viên duyệt", tone: "warn" },
  VERIFIED: { label: "Đã xác minh", tone: "pos" },
  REJECTED: { label: "Bị từ chối", tone: "neg" },
};

/**
 * Returns true when verify-otp fails with a terminal 409/FAILED_PRECONDITION
 * (OTP expired, too many invalid attempts, or no bank account pending OTP),
 * meaning the pending bank record has ended and the seller must re-submit bank details.
 */
export function isTerminalBankOtpError(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const e = err as { status?: number; code?: string; message?: string };
  const code = (e.code ?? "").toUpperCase();
  const msg = (e.message ?? "").toLowerCase();
  if (e.status === 409 || code === "FAILED_PRECONDITION") return true;
  return /otp expired|too many invalid otp|no bank account pending otp|không còn hiệu lực|đã hết hạn|nhập sai.*quá nhiều lần/.test(
    msg
  );
}

export type EditableVoucherField =
  | "code"
  | "name"
  | "discount_type"
  | "value_vnd"
  | "discount_percent"
  | "max_discount_vnd"
  | "min_order_vnd"
  | "quota"
  | "budget_vnd"
  | "per_user_limit"
  | "starts_at"
  | "ends_at"
  | "category_ids";

const USED_VOUCHER_EDITABLE_FIELDS = new Set<EditableVoucherField>([
  "name",
  "quota",
  "budget_vnd",
  "ends_at",
]);

/**
 * Matches PromotionService.UpdateVoucher rules:
 * - `code` is always immutable.
 * - Once `used_count > 0`, only `name`, raising `quota`/`budget_vnd`, and extending `ends_at` are allowed.
 */
export function canEditVoucherField(
  voucher: { used_count?: number },
  field: EditableVoucherField
): boolean {
  if (field === "code") return false;
  if ((voucher.used_count ?? 0) > 0) {
    return USED_VOUCHER_EDITABLE_FIELDS.has(field);
  }
  return true;
}

const BANK_ACCOUNT_NUMBER_RE = /^[0-9]{6,19}$/;

export function validateBankAccountInput(input: {
  bankCode: string;
  bankName: string;
  accountNumber: string;
  accountHolderName: string;
}): { ok: true } | { ok: false; message: string } {
  if (!input.bankCode.trim()) {
    return { ok: false, message: "Vui lòng chọn hoặc nhập mã ngân hàng." };
  }
  if (!input.bankName.trim()) {
    return { ok: false, message: "Vui lòng nhập tên ngân hàng." };
  }
  if (!BANK_ACCOUNT_NUMBER_RE.test(input.accountNumber.trim())) {
    return { ok: false, message: "Số tài khoản ngân hàng phải gồm 6 đến 19 chữ số." };
  }
  if (input.accountHolderName.trim().length < 2) {
    return { ok: false, message: "Vui lòng nhập đầy đủ họ tên chủ tài khoản." };
  }
  return { ok: true };
}

export function getProductCoverImage(product: GatewayProduct): string {
  return (
    product.images?.[0] ||
    product.skus?.find((s) => Boolean(s.image_url))?.image_url ||
    "/products/ao-thun-cotton-trang.png"
  );
}

export function getProductTotalStock(product: GatewayProduct): number {
  return (product.skus ?? []).reduce((sum, s) => sum + (s.is_active ? Math.max(0, s.stock) : 0), 0);
}

export function getProductAvailableStock(product: GatewayProduct): number {
  return (product.skus ?? []).reduce(
    (sum, s) => sum + (s.is_active ? Math.max(0, s.available_stock) : 0),
    0
  );
}

export function mapShopSlugToMockId(slug?: string): string {
  switch (slug) {
    case "xuong-det-bach-dang":
      return "shop-bach-dang";
    case "kho-basic-sai-gon":
      return "shop-sai-gon";
    case "soi-tu-nhien-da-nang":
      return "shop-da-nang";
    case "det-kim-ha-noi":
      return "shop-ha-noi";
    default:
      return "shop-bach-dang";
  }
}
