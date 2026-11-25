import { formatVnd } from "../lib/format";
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
  id?: string; // backend voucher id (save / apply)
  saved?: boolean; // saved to the signed-in buyer wallet
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

export const INITIAL_USER_COINS = 42000; // 1 xu = 1₫
export const COIN_MAX_RATIO = 0.2; // tối đa 20% giá trị hàng sau voucher
export const PLATFORM_FREESHIP_THRESHOLD = 249000; // Miễn phí vận chuyển toàn sàn từ 249.000₫

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
