export type FlashSlotStatus = "ended" | "live" | "upcoming";

export interface FlashSaleItem {
  id: string;
  productId: string;
  flashPrice: number; // integer VND
  originalPrice: number; // integer VND
  totalQuota: number;
  remainingQuota: number;
  limitPerBuyer: number;
  itemIdBySku?: Record<string, string>; // backend flash sale item id per SKU id
}

export interface FlashSaleSlot {
  id: string;
  label: string;
  startTime: string;
  endTime: string;
  status: FlashSlotStatus;
  statusLabel: string;
  startsAt?: string; // ISO instant from the gateway
  endsAt?: string;
  maxItemsPerOrder?: number;
  items: FlashSaleItem[];
}

export interface ShockDealItem {
  id: string;
  productId: string;
  dealPrice: number; // integer VND
  originalPrice: number; // integer VND
  totalQuota: number;
  remainingQuota: number;
  limitPerOrder: number; // 1 SP / lần
  limitPerDay: number; // tối đa 2 SP / ngày
  returnEligible: boolean;
  note: string;
}

export interface ComboOffer {
  id: string;
  slug: string;
  title: string;
  subtitle: string;
  shopId: string;
  productIds: string[];
  comboPrice: number; // integer VND
  originalTotalPrice: number; // integer VND
  discountLabel: string;
  note: string;
}

export const FLASH_SALE_RULES = {
  maxItemsPerOrder: 3,
  shockDealMaxPerOrder: 1,
  shockDealMaxPerDay: 2,
  stackableWithTierDiscount: true,
  note: "Mỗi đơn Flash sale chọn tối đa 3 sản phẩm ưu đãi; Săn deal sốc giới hạn 1 sản phẩm/lần và tối đa 2 lượt/ngày.",
} as const;

export const FLASH_SALE_SLOTS: FlashSaleSlot[] = [
  {
    id: "slot-09",
    label: "09:00",
    startTime: "09:00",
    endTime: "12:00",
    status: "ended",
    statusLabel: "Đã kết thúc",
    maxItemsPerOrder: 3,
    items: [
      {
        id: "fs-9-1",
        productId: "prod-at-xamda",
        flashPrice: 149000,
        originalPrice: 189000,
        totalQuota: 60,
        remainingQuota: 0,
        limitPerBuyer: 2,
      },
      {
        id: "fs-9-2",
        productId: "prod-short-run",
        flashPrice: 179000,
        originalPrice: 219000,
        totalQuota: 50,
        remainingQuota: 0,
        limitPerBuyer: 2,
      },
      {
        id: "fs-9-3",
        productId: "prod-tat-cothap",
        flashPrice: 89000,
        originalPrice: 109000,
        totalQuota: 80,
        remainingQuota: 0,
        limitPerBuyer: 3,
      },
      {
        id: "fs-9-4",
        productId: "prod-dolot-brief",
        flashPrice: 169000,
        originalPrice: 209000,
        totalQuota: 70,
        remainingQuota: 0,
        limitPerBuyer: 2,
      },
    ],
  },
  {
    id: "slot-12",
    label: "12:00",
    startTime: "12:00",
    endTime: "18:00",
    status: "live",
    statusLabel: "Đang mở bán",
    maxItemsPerOrder: 3,
    items: [
      {
        id: "fs-12-1",
        productId: "prod-at-trang",
        flashPrice: 149000,
        originalPrice: 219000,
        totalQuota: 120,
        remainingQuota: 28,
        limitPerBuyer: 2,
      },
      {
        id: "fs-12-2",
        productId: "prod-at-dryfit",
        flashPrice: 139000,
        originalPrice: 199000,
        totalQuota: 100,
        remainingQuota: 41,
        limitPerBuyer: 2,
      },
      {
        id: "fs-12-3",
        productId: "prod-polo-kem",
        flashPrice: 249000,
        originalPrice: 339000,
        totalQuota: 80,
        remainingQuota: 19,
        limitPerBuyer: 2,
      },
      {
        id: "fs-12-4",
        productId: "prod-short-daily",
        flashPrice: 209000,
        originalPrice: 289000,
        totalQuota: 90,
        remainingQuota: 33,
        limitPerBuyer: 2,
      },
      {
        id: "fs-12-5",
        productId: "prod-jogger-terry",
        flashPrice: 299000,
        originalPrice: 399000,
        totalQuota: 70,
        remainingQuota: 14,
        limitPerBuyer: 1,
      },
      {
        id: "fs-12-6",
        productId: "prod-dolot-boxer",
        flashPrice: 189000,
        originalPrice: 269000,
        totalQuota: 150,
        remainingQuota: 47,
        limitPerBuyer: 3,
      },
      {
        id: "fs-12-7",
        productId: "prod-tat-cotrung",
        flashPrice: 99000,
        originalPrice: 159000,
        totalQuota: 160,
        remainingQuota: 52,
        limitPerBuyer: 3,
      },
      {
        id: "fs-12-8",
        productId: "prod-polo-xanhda",
        flashPrice: 239000,
        originalPrice: 299000,
        totalQuota: 75,
        remainingQuota: 22,
        limitPerBuyer: 2,
      },
    ],
  },
  {
    id: "slot-18",
    label: "18:00",
    startTime: "18:00",
    endTime: "21:00",
    status: "upcoming",
    statusLabel: "Sắp mở bán",
    maxItemsPerOrder: 3,
    items: [
      {
        id: "fs-18-1",
        productId: "prod-polo-detkim",
        flashPrice: 319000,
        originalPrice: 369000,
        totalQuota: 60,
        remainingQuota: 60,
        limitPerBuyer: 2,
      },
      {
        id: "fs-18-2",
        productId: "prod-short-linen",
        flashPrice: 229000,
        originalPrice: 279000,
        totalQuota: 70,
        remainingQuota: 70,
        limitPerBuyer: 2,
      },
      {
        id: "fs-18-3",
        productId: "prod-jogger-tech",
        flashPrice: 329000,
        originalPrice: 389000,
        totalQuota: 50,
        remainingQuota: 50,
        limitPerBuyer: 1,
      },
      {
        id: "fs-18-4",
        productId: "prod-aokhoac-gio",
        flashPrice: 369000,
        originalPrice: 499000,
        totalQuota: 45,
        remainingQuota: 45,
        limitPerBuyer: 1,
      },
    ],
  },
  {
    id: "slot-21",
    label: "21:00",
    startTime: "21:00",
    endTime: "23:59",
    status: "upcoming",
    statusLabel: "Sắp mở bán",
    maxItemsPerOrder: 3,
    items: [
      {
        id: "fs-21-1",
        productId: "prod-at-oversize",
        flashPrice: 199000,
        originalPrice: 239000,
        totalQuota: 80,
        remainingQuota: 80,
        limitPerBuyer: 2,
      },
      {
        id: "fs-21-2",
        productId: "prod-polo-xanhda",
        flashPrice: 249000,
        originalPrice: 299000,
        totalQuota: 75,
        remainingQuota: 75,
        limitPerBuyer: 2,
      },
      {
        id: "fs-21-3",
        productId: "prod-dolot-brief",
        flashPrice: 169000,
        originalPrice: 209000,
        totalQuota: 100,
        remainingQuota: 100,
        limitPerBuyer: 3,
      },
      {
        id: "fs-21-4",
        productId: "prod-at-xanhthan",
        flashPrice: 149000,
        originalPrice: 229000,
        totalQuota: 90,
        remainingQuota: 90,
        limitPerBuyer: 2,
      },
    ],
  },
];

export const SHOCK_DEALS: ShockDealItem[] = [
  {
    id: "shock-1",
    productId: "prod-at-dryfit",
    dealPrice: 99000,
    originalPrice: 199000,
    totalQuota: 40,
    remainingQuota: 9,
    limitPerOrder: 1,
    limitPerDay: 2,
    returnEligible: false,
    note: "Giới hạn 1 chiếc/đơn · Không áp dụng đổi trả.",
  },
  {
    id: "shock-2",
    productId: "prod-tat-cothap",
    dealPrice: 69000,
    originalPrice: 109000,
    totalQuota: 60,
    remainingQuota: 17,
    limitPerOrder: 1,
    limitPerDay: 2,
    returnEligible: false,
    note: "Giới hạn 1 bộ/đơn · Không áp dụng đổi trả.",
  },
  {
    id: "shock-3",
    productId: "prod-short-run",
    dealPrice: 149000,
    originalPrice: 259000,
    totalQuota: 35,
    remainingQuota: 6,
    limitPerOrder: 1,
    limitPerDay: 2,
    returnEligible: false,
    note: "Suất chạy bộ giới hạn 1 chiếc/đơn · Không đổi trả.",
  },
  {
    id: "shock-4",
    productId: "prod-at-xanhthan",
    dealPrice: 129000,
    originalPrice: 219000,
    totalQuota: 50,
    remainingQuota: 14,
    limitPerOrder: 1,
    limitPerDay: 2,
    returnEligible: false,
    note: "Suất khung trưa giới hạn 1 chiếc/đơn · Không đổi trả.",
  },
];

export const COMBO_OFFERS: ComboOffer[] = [
  {
    id: "combo-basic-duo",
    slug: "combo-2-ao-cotton-220gsm",
    title: "Combo 2 Áo thun Cotton Compact 220gsm",
    subtitle: "Trắng tinh + Xám đá · Xưởng Dệt Bạch Đằng",
    shopId: "shop-bach-dang",
    productIds: ["prod-at-trang", "prod-at-xamda"],
    comboPrice: 329000,
    originalTotalPrice: 368000,
    discountLabel: "Tiết kiệm 39.000₫",
    note: "Giao chung 1 kiện từ Hải Phòng, đủ mốc freeship toàn sàn.",
  },
  {
    id: "combo-run-kit",
    slug: "bo-chay-bo-dryfit-short-5-inch",
    title: "Bộ Chạy bộ Dry-Fit + Quần Short 5 inch",
    subtitle: "Siêu nhẹ thoát ẩm nhanh · Kho Basic Sài Gòn",
    shopId: "shop-sai-gon",
    productIds: ["prod-at-dryfit", "prod-short-run"],
    comboPrice: 339000,
    originalTotalPrice: 378000,
    discountLabel: "Tiết kiệm 39.000₫",
    note: "Giao nhanh 2–4 giờ nội thành TP.HCM, đủ mốc freeship.",
  },
  {
    id: "combo-office-smart",
    slug: "bo-cong-so-polo-pique-short-linen",
    title: "Bộ Công sở Polo Pique + Short Linen",
    subtitle: "Sợi thực vật thoáng mát · Sợi Tự Nhiên Đà Nẵng",
    shopId: "shop-da-nang",
    productIds: ["prod-polo-kem", "prod-short-linen"],
    comboPrice: 519000,
    originalTotalPrice: 568000,
    discountLabel: "Tiết kiệm 49.000₫",
    note: "Dùng thêm mã DANANG30 và NEXUS30 khi đặt hàng.",
  },
  {
    id: "combo-terry-daily",
    slug: "bo-jogger-terry-tat-cotton",
    title: "Bộ Jogger Nỉ Terry + 5 đôi Tất Cotton",
    subtitle: "Nỉ chân cua 320gsm giữ form · Dệt Kim Hà Nội",
    shopId: "shop-ha-noi",
    productIds: ["prod-jogger-terry", "prod-tat-cotrung"],
    comboPrice: 429000,
    originalTotalPrice: 478000,
    discountLabel: "Tiết kiệm 49.000₫",
    note: "Giao chung 1 kiện từ Hà Nội, dùng thêm mã HANOI15.",
  },
];

export function getLiveFlashSlot(): FlashSaleSlot {
  return FLASH_SALE_SLOTS.find((s) => s.status === "live") ?? FLASH_SALE_SLOTS[0];
}
