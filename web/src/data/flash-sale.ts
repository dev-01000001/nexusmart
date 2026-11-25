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

export const FLASH_SALE_RULES = {
  maxItemsPerOrder: 3,
  note: "Mỗi đơn Flash sale chọn tối đa 3 sản phẩm ưu đãi.",
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

export function getLiveFlashSlot(): FlashSaleSlot {
  return FLASH_SALE_SLOTS.find((s) => s.status === "live") ?? FLASH_SALE_SLOTS[0];
}
