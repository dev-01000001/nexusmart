import { getShippingFeeForProvince } from "../lib/provinces";
import { formatVnd } from "../lib/format";
import { getProductById } from "./products";
import { getShopById } from "./shops";

export type VoucherScope = "platform" | "shop" | "freeship";
export type VoucherDiscountType = "fixed" | "percent";
export type MembershipTierRequirement = "thanh_vien" | "bac" | "vang" | "bach_kim";

export interface Voucher {
  code: string;
  title: string;
  description: string;
  scope: VoucherScope;
  shopId?: string;
  discountType: VoucherDiscountType;
  discountValue: number; // integer VND for fixed, integer percent (e.g. 10) for percent
  maxDiscount?: number; // integer VND cap for percent vouchers
  minOrderValue: number; // integer VND
  expiresAt: string;
  remainingUses: number;
  totalUses: number;
  // Wave 2 conditions & validation metadata
  excludeOutlet?: boolean;
  maxPromoItemsPerOrder?: number;
  minTierRequired?: MembershipTierRequirement;
  eduOnly?: boolean;
  conditions?: string[];
}

export interface VoucherValidationResult {
  valid: boolean;
  code: string;
  discount: number; // integer VND
  reason?: string;
  voucher?: Voucher;
}

export interface CartComboSelection {
  slotId: string;
  label: string;
  size: string;
}

export interface CartItem {
  skuCode: string;
  productId: string;
  productSlug: string;
  productName: string;
  shopId: string;
  colorCode: string;
  colorName: string;
  size: string;
  image: string;
  unitPrice: number; // integer VND
  originalPrice?: number;
  quantity: number;
  maxStock: number;
  selected: boolean;
  // Wave 2 optional fields
  isShockDeal?: boolean;
  isFlashSaleItem?: boolean;
  comboSelections?: CartComboSelection[];
}

export interface CartFreeGift {
  id: string;
  title: string;
  reason: string;
  image: string;
  valueVnd: number; // integer VND
}

export interface CartShopGroup {
  shopId: string;
  items: CartItem[];
  selectedSubtotal: number;
  shopVoucherCode: string | null;
  shopVoucherDiscount: number;
  // Wave 2 optional breakdown
  tierDiscount?: number;
  freeShipThreshold?: number;
  freeShipRemaining?: number;
  freeShipUnlocked?: boolean;
}

export interface CartSummary {
  groups: CartShopGroup[];
  selectedCount: number;
  subtotal: number;
  shippingFee: number;
  shopVoucherTotal: number;
  platformVoucherDiscount: number;
  freeshipDiscount: number;
  maxUsableCoins: number;
  coinDiscount: number;
  totalDiscount: number;
  grandTotal: number;
  // Wave 2 optional summary fields
  tierDiscountTotal?: number;
  giftCardDiscount?: number;
  platformFreeShipThreshold?: number;
  platformFreeShipRemaining?: number;
  platformFreeShipUnlocked?: boolean;
  freeGifts?: CartFreeGift[];
  estimatedCashbackCoins?: number;
}

export const INITIAL_USER_COINS = 42000; // 1 xu = 1₫
export const COIN_MAX_RATIO = 0.2; // tối đa 20% giá trị hàng sau voucher
export const PLATFORM_FREESHIP_THRESHOLD = 249000; // Miễn phí vận chuyển toàn sàn từ 249.000₫
export const GIFT_ORDER_THRESHOLD = 499000; // Quà tặng ngưỡng đơn từ 499.000₫

const TIER_ORDER: Record<MembershipTierRequirement, number> = {
  thanh_vien: 0,
  bac: 1,
  vang: 2,
  bach_kim: 3,
};

const TIER_LABELS_SHORT: Record<MembershipTierRequirement, string> = {
  thanh_vien: "Thành viên",
  bac: "Hạng Bạc",
  vang: "Hạng Vàng",
  bach_kim: "Hạng Bạch Kim",
};

export const VOUCHERS: Voucher[] = [
  // Platform vouchers
  {
    code: "NEXUS30",
    title: "Giảm 30.000₫ toàn sàn",
    description: "Áp dụng cho đơn hàng từ 350.000₫ trên toàn sàn NexusMart.",
    scope: "platform",
    discountType: "fixed",
    discountValue: 30000,
    minOrderValue: 350000,
    expiresAt: "2026-11-30T23:59:59+07:00",
    remainingUses: 148,
    totalUses: 500,
    excludeOutlet: false,
    maxPromoItemsPerOrder: 3,
    conditions: [
      "Đơn tối thiểu 350.000₫ sau khi trừ mã gian hàng.",
      "Có thể dùng chung 1 mã gian hàng và 1 mã freeship.",
    ],
  },
  {
    code: "NEXUS10",
    title: "Giảm 10% tối đa 60.000₫",
    description: "Áp dụng cho đơn hàng từ 500.000₫, gộp được cùng mã gian hàng.",
    scope: "platform",
    discountType: "percent",
    discountValue: 10,
    maxDiscount: 60000,
    minOrderValue: 500000,
    expiresAt: "2026-11-30T23:59:59+07:00",
    remainingUses: 92,
    totalUses: 300,
    excludeOutlet: true,
    maxPromoItemsPerOrder: 3,
    conditions: [
      "Đơn tối thiểu 500.000₫, giảm tối đa 60.000₫.",
      "Không áp dụng cho sản phẩm gắn nhãn Outlet.",
    ],
  },
  // Freeship vouchers
  {
    code: "FREESHIP25",
    title: "Giảm 25.000₫ phí vận chuyển",
    description: "Cho đơn từ 249.000₫ giao toàn quốc 34 tỉnh thành.",
    scope: "freeship",
    discountType: "fixed",
    discountValue: 25000,
    minOrderValue: 249000,
    expiresAt: "2026-11-30T23:59:59+07:00",
    remainingUses: 310,
    totalUses: 1000,
    conditions: ["Áp dụng cho đơn từ 249.000₫ trên toàn quốc 34 tỉnh."],
  },
  {
    code: "FREESHIP40",
    title: "Miễn phí vận chuyển tối đa 40.000₫",
    description: "Cho đơn từ 499.000₫ kể cả đơn gộp nhiều gian hàng.",
    scope: "freeship",
    discountType: "fixed",
    discountValue: 40000,
    minOrderValue: 499000,
    expiresAt: "2026-11-30T23:59:59+07:00",
    remainingUses: 175,
    totalUses: 600,
    conditions: ["Áp dụng cho đơn từ 499.000₫, hỗ trợ cả đơn gộp nhiều xưởng."],
  },
  // Shop vouchers
  {
    code: "BACHDANG20",
    title: "Giảm 20.000₫ Xưởng Dệt Bạch Đằng",
    description: "Áp dụng cho đơn từ 300.000₫ tại Xưởng Dệt Bạch Đằng.",
    scope: "shop",
    shopId: "shop-bach-dang",
    discountType: "fixed",
    discountValue: 20000,
    minOrderValue: 300000,
    expiresAt: "2026-11-28T23:59:59+07:00",
    remainingUses: 44,
    totalUses: 120,
    conditions: ["Áp dụng riêng cho sản phẩm thuộc Xưởng Dệt Bạch Đằng từ 300.000₫."],
  },
  {
    code: "SAIGON25",
    title: "Giảm 25.000₫ Kho Basic Sài Gòn",
    description: "Áp dụng cho đơn từ 350.000₫ tại Kho Basic Sài Gòn.",
    scope: "shop",
    shopId: "shop-sai-gon",
    discountType: "fixed",
    discountValue: 25000,
    minOrderValue: 350000,
    expiresAt: "2026-11-28T23:59:59+07:00",
    remainingUses: 61,
    totalUses: 150,
    conditions: ["Áp dụng riêng cho sản phẩm thuộc Kho Basic Sài Gòn từ 350.000₫."],
  },
  {
    code: "DANANG30",
    title: "Giảm 30.000₫ Sợi Tự Nhiên Đà Nẵng",
    description: "Áp dụng cho đơn từ 450.000₫ tại Sợi Tự Nhiên Đà Nẵng.",
    scope: "shop",
    shopId: "shop-da-nang",
    discountType: "fixed",
    discountValue: 30000,
    minOrderValue: 450000,
    expiresAt: "2026-11-28T23:59:59+07:00",
    remainingUses: 28,
    totalUses: 100,
    conditions: ["Áp dụng riêng cho sản phẩm thuộc Sợi Tự Nhiên Đà Nẵng từ 450.000₫."],
  },
  {
    code: "HANOI15",
    title: "Giảm 15.000₫ Dệt Kim Hà Nội",
    description: "Áp dụng cho đơn từ 220.000₫ tại Dệt Kim Hà Nội.",
    scope: "shop",
    shopId: "shop-ha-noi",
    discountType: "fixed",
    discountValue: 15000,
    minOrderValue: 220000,
    expiresAt: "2026-11-28T23:59:59+07:00",
    remainingUses: 53,
    totalUses: 120,
    conditions: ["Áp dụng riêng cho sản phẩm thuộc Dệt Kim Hà Nội từ 220.000₫."],
  },
];

export function getVoucherByCode(code: string | null | undefined): Voucher | undefined {
  if (!code) return undefined;
  return VOUCHERS.find((v) => v.code.toUpperCase() === code.trim().toUpperCase());
}

export function getShopVouchers(shopId: string): Voucher[] {
  return VOUCHERS.filter((v) => v.scope === "shop" && v.shopId === shopId);
}

/**
 * Validates a voucher code against the current cart/order context and returns
 * a specific Vietnamese rejection reason when ineligible.
 */
export function validateVoucher(
  code: string,
  context: {
    eligibleAmount: number;
    shopId?: string;
    hasOutletItems?: boolean;
    eduVerified?: boolean;
    memberTier?: MembershipTierRequirement;
    capAmount?: number;
  }
): VoucherValidationResult {
  const cleanCode = code.trim().toUpperCase();
  if (!cleanCode) {
    return { valid: false, code: cleanCode, discount: 0, reason: "Vui lòng nhập mã ưu đãi." };
  }
  const voucher = getVoucherByCode(cleanCode);
  if (!voucher) {
    return {
      valid: false,
      code: cleanCode,
      discount: 0,
      reason: `Mã ${cleanCode} không tồn tại hoặc đã ngừng phát hành.`,
    };
  }
  if (voucher.remainingUses <= 0) {
    return {
      valid: false,
      code: voucher.code,
      discount: 0,
      voucher,
      reason: `Mã ${voucher.code} đã hết lượt sử dụng trong đợt này.`,
    };
  }
  if (voucher.scope === "shop" && context.shopId && voucher.shopId !== context.shopId) {
    const ownerShop = voucher.shopId ? getShopById(voucher.shopId) : undefined;
    return {
      valid: false,
      code: voucher.code,
      discount: 0,
      voucher,
      reason: `Mã ${voucher.code} chỉ áp dụng cho ${ownerShop?.name ?? "gian hàng tương ứng"}.`,
    };
  }
  if (voucher.eduOnly && !context.eduVerified) {
    return {
      valid: false,
      code: voucher.code,
      discount: 0,
      voucher,
      reason: "Mã ưu đãi sinh viên yêu cầu xác thực email đuôi .edu trước khi áp dụng.",
    };
  }
  if (voucher.minTierRequired) {
    const currentRank = TIER_ORDER[context.memberTier ?? "thanh_vien"];
    const requiredRank = TIER_ORDER[voucher.minTierRequired];
    if (currentRank < requiredRank) {
      return {
        valid: false,
        code: voucher.code,
        discount: 0,
        voucher,
        reason: `Mã ${voucher.code} chỉ áp dụng cho Hội viên Nexus từ ${TIER_LABELS_SHORT[voucher.minTierRequired]} trở lên.`,
      };
    }
  }
  if (voucher.excludeOutlet && context.hasOutletItems) {
    return {
      valid: false,
      code: voucher.code,
      discount: 0,
      voucher,
      reason: `Mã ${voucher.code} không áp dụng đồng thời với sản phẩm Outlet trong giỏ.`,
    };
  }
  if (context.eligibleAmount < voucher.minOrderValue) {
    const missing = voucher.minOrderValue - context.eligibleAmount;
    return {
      valid: false,
      code: voucher.code,
      discount: 0,
      voucher,
      reason: `Cần mua thêm ${formatVnd(missing)} để đạt ngưỡng tối thiểu ${formatVnd(voucher.minOrderValue)}.`,
    };
  }
  const discount = calculateVoucherDiscount(voucher, context.eligibleAmount, context.capAmount);
  return { valid: true, code: voucher.code, discount, voucher };
}

/**
 * Pure integer VND discount calculation for a single voucher against an eligible amount.
 */
export function calculateVoucherDiscount(
  voucher: Voucher | undefined,
  eligibleAmount: number,
  capAmount?: number
): number {
  if (!voucher || eligibleAmount < voucher.minOrderValue || eligibleAmount <= 0) {
    return 0;
  }
  let raw = 0;
  if (voucher.discountType === "fixed") {
    raw = voucher.discountValue;
  } else {
    raw = Math.floor((eligibleAmount * voucher.discountValue) / 100);
    if (voucher.maxDiscount != null) {
      raw = Math.min(raw, voucher.maxDiscount);
    }
  }
  const upperBound = capAmount != null ? Math.min(eligibleAmount, capAmount) : eligibleAmount;
  return Math.max(0, Math.min(Math.trunc(raw), Math.trunc(upperBound)));
}

/**
 * Calculates quantity-based tier discount (e.g. buy 2 save 10%, buy 3 save 15%) for selected items of a shop.
 */
export function calculateShopTierDiscount(selectedItems: CartItem[]): number {
  let discount = 0;
  const qtyByProduct = new Map<string, { qty: number; subtotal: number }>();
  for (const item of selectedItems) {
    if (item.isShockDeal) continue;
    const prev = qtyByProduct.get(item.productId) ?? { qty: 0, subtotal: 0 };
    qtyByProduct.set(item.productId, {
      qty: prev.qty + item.quantity,
      subtotal: prev.subtotal + item.unitPrice * item.quantity,
    });
  }
  for (const [productId, agg] of qtyByProduct.entries()) {
    const product = getProductById(productId);
    const tiers = product?.tierDiscounts;
    if (!tiers || tiers.length === 0) continue;
    let bestPct = 0;
    for (const tier of tiers) {
      if (agg.qty >= tier.minQty && tier.discountPercent > bestPct) {
        bestPct = tier.discountPercent;
      }
    }
    if (bestPct > 0) {
      discount += Math.floor((agg.subtotal * bestPct) / 100);
    }
  }
  return Math.trunc(discount);
}

/**
 * Pure deterministic cart summary calculation grouped by shop, reconciling all integer VND discounts.
 * Backward compatible: optional Wave 2 inputs default so existing Wave 1 calls yield identical totals.
 */
export function calculateCartSummary(params: {
  items: CartItem[];
  provinceCode: string;
  selectedShopVouchers: Record<string, string | null>;
  platformVoucherCode: string | null;
  freeshipVoucherCode: string | null;
  useCoins: boolean;
  availableCoins: number;
  // Optional Wave 2 parameters
  applyTierDiscounts?: boolean;
  giftCardDiscount?: number;
  deliveryMethod?: "standard" | "fast";
  birthdayGiftEligible?: boolean;
  cashbackRate?: number; // e.g. 0.02 = 2%
  eduVerified?: boolean;
  memberTier?: MembershipTierRequirement;
}): CartSummary {
  const {
    items,
    provinceCode,
    selectedShopVouchers,
    platformVoucherCode,
    freeshipVoucherCode,
    useCoins,
    availableCoins,
    applyTierDiscounts = false,
    giftCardDiscount: rawGiftCardDiscount = 0,
    deliveryMethod = "standard",
    birthdayGiftEligible = false,
    cashbackRate = 0.02,
    eduVerified = false,
    memberTier = "bac",
  } = params;

  const shopOrder: string[] = [];
  const byShop = new Map<string, CartItem[]>();
  for (const item of items) {
    if (!byShop.has(item.shopId)) {
      shopOrder.push(item.shopId);
      byShop.set(item.shopId, []);
    }
    byShop.get(item.shopId)!.push(item);
  }

  const groups: CartShopGroup[] = [];
  let subtotal = 0;
  let selectedCount = 0;
  let shopVoucherTotal = 0;
  let tierDiscountTotal = 0;
  let selectedShopCount = 0;
  let hasOutletSelected = false;

  for (const shopId of shopOrder) {
    const shopItems = byShop.get(shopId)!;
    const selectedItems = shopItems.filter((it) => it.selected && it.quantity > 0);
    const selectedSubtotal = selectedItems.reduce((sum, it) => sum + it.unitPrice * it.quantity, 0);
    const count = selectedItems.reduce((sum, it) => sum + it.quantity, 0);
    if (count > 0) selectedShopCount += 1;
    selectedCount += count;
    subtotal += selectedSubtotal;

    for (const it of selectedItems) {
      const prod = getProductById(it.productId);
      if (prod?.badges?.includes("outlet")) {
        hasOutletSelected = true;
      }
    }

    const tierDiscount = applyTierDiscounts ? calculateShopTierDiscount(selectedItems) : 0;
    tierDiscountTotal += tierDiscount;

    const afterTierSubtotal = Math.max(0, selectedSubtotal - tierDiscount);

    const code = selectedShopVouchers[shopId] ?? null;
    const voucher = getVoucherByCode(code);
    const validShopVoucher =
      voucher && voucher.scope === "shop" && voucher.shopId === shopId ? voucher : undefined;
    const shopDiscount = calculateVoucherDiscount(validShopVoucher, afterTierSubtotal);
    shopVoucherTotal += shopDiscount;

    const shopMeta = getShopById(shopId);
    const shopFreeShipThreshold = shopMeta?.freeShipThreshold ?? 199000;
    const shopFreeShipRemaining = Math.max(0, shopFreeShipThreshold - selectedSubtotal);

    groups.push({
      shopId,
      items: shopItems,
      selectedSubtotal,
      shopVoucherCode: shopDiscount > 0 && validShopVoucher ? validShopVoucher.code : null,
      shopVoucherDiscount: shopDiscount,
      tierDiscount,
      freeShipThreshold: shopFreeShipThreshold,
      freeShipRemaining: shopFreeShipRemaining,
      freeShipUnlocked: selectedSubtotal >= shopFreeShipThreshold && count > 0,
    });
  }

  const baseShippingFee =
    selectedCount > 0 ? getShippingFeeForProvince(provinceCode, selectedShopCount) : 0;
  const fastSurcharge = selectedCount > 0 && deliveryMethod === "fast" ? 15000 : 0;
  const shippingFee = baseShippingFee + fastSurcharge;

  const afterShopAndTier = Math.max(0, subtotal - tierDiscountTotal - shopVoucherTotal);

  const platformVoucher = getVoucherByCode(platformVoucherCode);
  let platformVoucherDiscount = 0;
  if (platformVoucher && platformVoucher.scope === "platform") {
    const check = validateVoucher(platformVoucher.code, {
      eligibleAmount: afterShopAndTier,
      hasOutletItems: hasOutletSelected,
      eduVerified,
      memberTier,
    });
    if (check.valid) {
      platformVoucherDiscount = check.discount;
    }
  }

  const freeshipVoucher = getVoucherByCode(freeshipVoucherCode);
  const validFreeshipVoucher =
    freeshipVoucher && freeshipVoucher.scope === "freeship" ? freeshipVoucher : undefined;
  const freeshipDiscount =
    shippingFee > 0 && validFreeshipVoucher && subtotal >= validFreeshipVoucher.minOrderValue
      ? Math.min(shippingFee, validFreeshipVoucher.discountValue)
      : 0;

  const afterAllVouchers = Math.max(0, afterShopAndTier - platformVoucherDiscount);
  // Coins rounded down to nearest 1.000 xu, capped at 20% of merchandise value after vouchers
  const maxCoinByOrder = Math.floor((afterAllVouchers * COIN_MAX_RATIO) / 1000) * 1000;
  const maxUsableCoins = Math.max(
    0,
    Math.min(Math.floor(availableCoins / 1000) * 1000, maxCoinByOrder)
  );
  const coinDiscount = useCoins && selectedCount > 0 ? maxUsableCoins : 0;

  const payableBeforeGiftCard = Math.max(
    0,
    subtotal +
      shippingFee -
      tierDiscountTotal -
      shopVoucherTotal -
      platformVoucherDiscount -
      freeshipDiscount -
      coinDiscount
  );
  const giftCardDiscount =
    selectedCount > 0
      ? Math.max(0, Math.min(Math.trunc(rawGiftCardDiscount), payableBeforeGiftCard))
      : 0;

  const totalDiscount =
    tierDiscountTotal +
    shopVoucherTotal +
    platformVoucherDiscount +
    freeshipDiscount +
    coinDiscount +
    giftCardDiscount;
  const grandTotal = Math.max(0, subtotal + shippingFee - totalDiscount);

  const freeGifts: CartFreeGift[] = [];
  if (selectedCount > 0 && subtotal >= GIFT_ORDER_THRESHOLD) {
    freeGifts.push({
      id: "gift-threshold-sock",
      title: "1 đôi Tất Cotton gân cổ trung (quà mốc đơn)",
      reason: `Tự động tặng cho đơn từ ${formatVnd(GIFT_ORDER_THRESHOLD)}`,
      image: "/products/tat-cotton-co-trung-5.png",
      valueVnd: 39000,
    });
  }
  if (selectedCount > 0 && birthdayGiftEligible) {
    freeGifts.push({
      id: "gift-birthday-nexus",
      title: "Túi vải rút Sợi Xanh Nexus (quà sinh nhật Hội viên)",
      reason: "Quà sinh nhật dành cho Hội viên Nexus từ hạng Bạc",
      image: "/products/ao-thun-cotton-trang.png",
      valueVnd: 65000,
    });
  }

  const estimatedCashbackCoins =
    selectedCount > 0
      ? Math.floor((Math.max(0, grandTotal - shippingFee) * cashbackRate) / 100) * 100
      : 0;

  return {
    groups,
    selectedCount,
    subtotal,
    shippingFee,
    shopVoucherTotal,
    platformVoucherDiscount,
    freeshipDiscount,
    maxUsableCoins,
    coinDiscount,
    totalDiscount,
    grandTotal,
    tierDiscountTotal,
    giftCardDiscount,
    platformFreeShipThreshold: PLATFORM_FREESHIP_THRESHOLD,
    platformFreeShipRemaining: Math.max(0, PLATFORM_FREESHIP_THRESHOLD - subtotal),
    platformFreeShipUnlocked: selectedCount > 0 && subtotal >= PLATFORM_FREESHIP_THRESHOLD,
    freeGifts,
    estimatedCashbackCoins,
  };
}
