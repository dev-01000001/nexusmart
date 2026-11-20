"use client";

import { useSyncExternalStore } from "react";
import {
  FLASH_SALE_SLOTS,
  INITIAL_BIRTHDAY_COFUND,
  INITIAL_CAMPAIGN_REGISTRATIONS,
  INITIAL_FACTORY_PROFILES,
  INITIAL_OPERATIONS_CONFIG,
  INITIAL_SELLER_ADDON_DEALS,
  INITIAL_SELLER_COMBOS,
  INITIAL_SELLER_FAQS,
  INITIAL_SELLER_LOOKBOOKS,
  INITIAL_SELLER_QUESTIONS,
  INITIAL_SELLER_REVIEWS,
  INITIAL_SELLER_TIER_RULES,
  INITIAL_SHOP_SIZE_CHARTS,
  ORDER_STATUS_META,
  PRODUCTS,
  SELLER_ORDER_EXTRAS,
  VOUCHERS,
  getDefaultProductMeta,
  getProductById,
  getProductsByShop,
  getSellerWorkspace,
  getShopById,
} from "@/data";
import type {
  ChatThread,
  LowStockSkuAlert,
  Order,
  OrderLineItem,
  OrderStatus,
  OrderStatusMeta,
  OrderTimelineEvent,
  Product,
  SellerAddonDeal,
  SellerBirthdayCofundConfig,
  SellerBundleCombo,
  SellerCampaignRegistration,
  SellerFactoryProfile,
  SellerLedgerEntry,
  SellerLookbookPost,
  SellerOperationsConfig,
  SellerOrderRecord,
  SellerOrderWave2Extra,
  SellerProductExtendedMeta,
  SellerProductQuestion,
  SellerPromotionRecord,
  SellerSampleReview,
  SellerShopCollection,
  SellerShopFaqItem,
  SellerShopSizeCharts,
  SellerTierDiscountRule,
  TwoWayCourierStatus,
  VatInvoiceStatus,
} from "@/data";

export function getSafeOrderStatusMeta(status: string): OrderStatusMeta {
  const known = (ORDER_STATUS_META as Record<string, OrderStatusMeta | undefined>)[status];
  if (known) return known;
  return {
    label: "Chờ thanh toán",
    tone: "warn",
    description: "Đơn đang chờ người mua hoàn tất thanh toán trực tuyến.",
  };
}

export const EMPTY_DASH = "\u2014";
const TRUE_MINUS = "\u2212";

export type UiStateMode = "loading" | "empty" | "error" | null;

export function parseUiState(value: string | null): UiStateMode {
  if (value === "loading" || value === "empty" || value === "error") {
    return value;
  }
  return null;
}

export function formatDeltaPct(
  current: number,
  prior: number
): {
  label: string;
  direction: "up" | "down" | "flat";
} {
  if (prior <= 0) {
    if (current <= 0) return { label: EMPTY_DASH, direction: "flat" };
    return { label: "Mới", direction: "up" };
  }
  const diff = current - prior;
  if (diff === 0) return { label: "0,0%", direction: "flat" };
  const pct = (Math.abs(diff) / prior) * 100;
  const formatted = pct.toFixed(1).replace(".", ",");
  return {
    label: `${diff > 0 ? "+" : TRUE_MINUS}${formatted}%`,
    direction: diff > 0 ? "up" : "down",
  };
}

export interface OrderTransitionAction {
  nextStatus: OrderStatus;
  label: string;
  shortLabel: string;
  note: string;
}

export function getNextSellerTransition(status: OrderStatus): OrderTransitionAction | null {
  switch (status) {
    case "cho_xac_nhan":
      return {
        nextStatus: "dang_chuan_bi",
        label: "Xác nhận đơn hàng",
        shortLabel: "Xác nhận",
        note: "Gian hàng đã xác nhận tồn kho SKU và bắt đầu đóng gói.",
      };
    case "dang_chuan_bi":
      return {
        nextStatus: "dang_giao",
        label: "Bàn giao vận chuyển",
        shortLabel: "Giao ĐVVC",
        note: "Đã bàn giao kiện hàng cho đơn vị vận chuyển NexusLogistics.",
      };
    case "dang_giao":
      return {
        nextStatus: "da_giao",
        label: "Xác nhận đã giao",
        shortLabel: "Đã giao",
        note: "Người mua đã nhận đủ kiện hàng và hoàn tất giao vận.",
      };
    case "yeu_cau_tra_hang":
      return {
        nextStatus: "da_giao",
        label: "Chốt đổi trả",
        shortLabel: "Chốt đổi",
        note: "Gian hàng đã hoàn tất đổi size và đóng yêu cầu đổi trả.",
      };
    default:
      return null;
  }
}

export interface SellerShopVoucher {
  id: string;
  shopId: string;
  code: string;
  title: string;
  discountType: "fixed" | "percent";
  discountValue: number;
  maxDiscount?: number;
  minOrderValue: number;
  usedQuota: number;
  totalQuota: number;
  attributedRevenue: number;
  periodLabel: string;
  active: boolean;
}

export interface SellerFlashRegistration {
  id: string;
  shopId: string;
  slotId: string;
  slotLabel: string;
  slotStatus: "ended" | "live" | "upcoming";
  productId: string;
  productName: string;
  productImage: string;
  originalPrice: number;
  flashPrice: number;
  usedQuota: number;
  totalQuota: number;
  limitPerBuyer: number;
}

export interface UnifiedSellerOrder extends SellerOrderRecord {
  storeOrderId?: string;
  shippingAddress: string;
  trackingCode?: string;
  note?: string;
  items: OrderLineItem[];
  timeline: OrderTimelineEvent[];
  wave2Extra?: SellerOrderWave2Extra;
}

export interface LedgerEntryWithBalance extends SellerLedgerEntry {
  runningBalance: number;
}

interface OrderStatusOverride {
  status: OrderStatus;
  timelineEvents: OrderTimelineEvent[];
  addressOverride?: string;
}

interface SellerSessionState {
  productOverrides: Record<string, Product>;
  addedProducts: Product[];
  productMetaOverrides: Record<string, SellerProductExtendedMeta>;
  orderOverrides: Record<string, OrderStatusOverride>;
  orderExtraOverrides: Record<string, SellerOrderWave2Extra>;
  voucherActiveOverrides: Record<string, boolean>;
  addedVouchers: SellerShopVoucher[];
  addedFlashRegs: SellerFlashRegistration[];
  addedLedgerEntries: SellerLedgerEntry[];
  extraThreads: ChatThread[];
  // Wave 2 promotions
  combos: SellerBundleCombo[];
  tierRules: SellerTierDiscountRule[];
  addonDeals: SellerAddonDeal[];
  birthdayConfigs: Record<string, SellerBirthdayCofundConfig>;
  campaignRegs: SellerCampaignRegistration[];
  // Wave 2 interaction & content
  reviews: SellerSampleReview[];
  questions: SellerProductQuestion[];
  faqs: SellerShopFaqItem[];
  lookbooks: SellerLookbookPost[];
  // Wave 2 factory profile, size charts & operations
  factoryProfiles: Record<string, SellerFactoryProfile>;
  sizeCharts: Record<string, SellerShopSizeCharts>;
  opsConfigs: Record<string, SellerOperationsConfig>;
}

const BANK_ACCOUNTS: Record<
  string,
  { bank: string; branch: string; mask: string; holder: string }
> = {
  "shop-bach-dang": {
    bank: "Vietcombank",
    branch: "CN Hải Phòng",
    mask: "****4821",
    holder: "CONG TY TNHH DET MAY BACH DANG",
  },
  "shop-sai-gon": {
    bank: "Techcombank",
    branch: "CN Thảo Điền",
    mask: "****7390",
    holder: "HO KINH DOANH KHO BASIC SAI GON",
  },
  "shop-da-nang": {
    bank: "ACB",
    branch: "CN Hải Châu",
    mask: "****2154",
    holder: "CONG TY TNHH SOI TU NHIEN DA NANG",
  },
  "shop-ha-noi": {
    bank: "BIDV",
    branch: "CN Hai Bà Trưng",
    mask: "****6018",
    holder: "CONG TY CP DET KIM HA NOI",
  },
};

export function getShopBankAccount(shopId: string) {
  return BANK_ACCOUNTS[shopId] ?? BANK_ACCOUNTS["shop-bach-dang"];
}

function buildInitialExtraThreads(): ChatThread[] {
  return [
    {
      id: "thr-bd-extra-1",
      shopId: "shop-bach-dang",
      shopName: "Xưởng Dệt Bạch Đằng",
      buyerName: "Trần Hải Đăng",
      productRef: {
        slug: "combo-3-quan-lot-boxer-modal",
        name: "Combo 3 Quần lót Boxer Sợi Modal",
        price: 229000,
        image: "/products/do-lot-boxer-modal-3.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-bd-e1",
          sender: "buyer",
          senderName: "Trần Hải Đăng",
          text: "Shop cho hỏi đơn NM-261120-102 gồm 2 combo Boxer Modal XL có đóng gói kín tên sản phẩm không ạ?",
          timestamp: "2026-11-20T00:58:00+07:00",
        },
        {
          id: "msg-bd-e2",
          sender: "seller",
          senderName: "Xưởng Dệt Bạch Đằng",
          text: "Dạ tất cả đơn đồ lót của xưởng đều bọc túi mờ hai lớp và chỉ ghi mã kiện trên phiếu giao hàng ạ.",
          timestamp: "2026-11-20T01:04:00+07:00",
        },
      ],
    },
    {
      id: "thr-bd-extra-2",
      shopId: "shop-bach-dang",
      shopName: "Xưởng Dệt Bạch Đằng",
      buyerName: "Lê Thị Thu Hà",
      productRef: {
        slug: "ao-thun-cotton-compact-xam-da",
        name: "Áo thun Cotton Compact 220gsm Xám đá",
        price: 189000,
        image: "/products/ao-thun-cotton-xam-da.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-bd-e3",
          sender: "buyer",
          senderName: "Lê Thị Thu Hà",
          text: "Mình vừa đặt đơn NM-261119-096 về Bãi Cháy, shop giúp mình kiểm tra kỹ đường may cổ áo trước khi gửi nhé.",
          timestamp: "2026-11-19T22:24:00+07:00",
        },
        {
          id: "msg-bd-e4",
          sender: "seller",
          senderName: "Xưởng Dệt Bạch Đằng",
          text: "Cảm ơn chị Hà, bộ phận kho sẽ kiểm tra bo cổ và đóng gói giao trong chuyến sáng nay 20/11 ạ.",
          timestamp: "2026-11-19T22:32:00+07:00",
        },
      ],
    },
    {
      id: "thr-bd-extra-3",
      shopId: "shop-bach-dang",
      shopName: "Xưởng Dệt Bạch Đằng",
      buyerName: "Đỗ Minh Hiếu",
      productRef: {
        slug: "ao-thun-cotton-compact-trang",
        name: "Áo thun Cotton Compact 220gsm Trắng",
        price: 179000,
        image: "/products/ao-thun-cotton-trang.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-bd-e5",
          sender: "buyer",
          senderName: "Đỗ Minh Hiếu",
          text: "Đơn NM-261115-053 mình muốn đổi áo Cotton Trắng từ 2XL xuống XL, áo vẫn nguyên tem mác chưa giặt.",
          timestamp: "2026-11-19T15:10:00+07:00",
        },
        {
          id: "msg-bd-e6",
          sender: "seller",
          senderName: "Xưởng Dệt Bạch Đằng",
          text: "Xưởng đã tiếp nhận yêu cầu đổi size XL miễn phí cho anh Hiếu. Bưu tá sẽ đến thu hồi tại Phường Biên Hòa trong hôm nay ạ.",
          timestamp: "2026-11-19T15:26:00+07:00",
        },
      ],
    },
    {
      id: "thr-sg-extra-1",
      shopId: "shop-sai-gon",
      shopName: "Kho Basic Sài Gòn",
      buyerName: "Phạm Quốc Khánh",
      productRef: {
        slug: "quan-short-daily-cotton-7-inch-be",
        name: "Quần Short Daily Cotton 7 inch Be cát",
        price: 249000,
        image: "/products/quan-short-daily-be.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-sg-e1",
          sender: "buyer",
          senderName: "Phạm Quốc Khánh",
          text: "Quần Short Daily 7 inch màu Be cát có túi khóa kéo phía sau không shop?",
          timestamp: "2026-11-19T18:10:00+07:00",
        },
        {
          id: "msg-sg-e2",
          sender: "seller",
          senderName: "Kho Basic Sài Gòn",
          text: "Mẫu Daily 7 inch có hai túi hông sâu và túi mổ cài cúc phía sau; nếu anh cần túi khóa kéo có thể xem mẫu Short Chạy bộ 5 inch ạ.",
          timestamp: "2026-11-19T18:19:00+07:00",
        },
      ],
    },
    {
      id: "thr-dn-extra-1",
      shopId: "shop-da-nang",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      buyerName: "Bùi Thị Thanh Mai",
      productRef: {
        slug: "quan-short-linen-pha-cotton-xam-da",
        name: "Quần Short Linen pha Cotton Xám đá",
        price: 279000,
        image: "/products/quan-short-linen-xam-da.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-dn-e1",
          sender: "buyer",
          senderName: "Bùi Thị Thanh Mai",
          text: "Vải linen pha 45% cotton có bị co sau lần giặt đầu không shop?",
          timestamp: "2026-11-19T16:05:00+07:00",
        },
        {
          id: "msg-dn-e2",
          sender: "seller",
          senderName: "Sợi Tự Nhiên Đà Nẵng",
          text: "Xưởng đã xử lý wash mềm trước khi may nên độ co dưới 1,5%. Chị giặt nước mát và phơi tự nhiên là giữ đúng form ạ.",
          timestamp: "2026-11-19T16:17:00+07:00",
        },
      ],
    },
    {
      id: "thr-hn-extra-1",
      shopId: "shop-ha-noi",
      shopName: "Dệt Kim Hà Nội",
      buyerName: "Nguyễn Minh Quân",
      productRef: {
        slug: "ao-polo-det-kim-min-den-than",
        name: "Áo Polo Dệt kim mịn Đen than",
        price: 369000,
        image: "/products/ao-polo-det-kim-den.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-hn-e1",
          sender: "buyer",
          senderName: "Nguyễn Minh Quân",
          text: "Mình đã gửi yêu cầu đổi áo Polo Dệt kim Đen than đơn NM-261108-044 từ size M sang size L, shop còn sẵn size L không?",
          timestamp: "2026-11-19T10:15:00+07:00",
        },
        {
          id: "msg-hn-e2",
          sender: "seller",
          senderName: "Dệt Kim Hà Nội",
          text: "Kho Hà Nội đang còn 18 chiếc màu Đen than size L. Xưởng đã giữ sẵn 1 chiếc cho đơn đổi của anh Quân rồi ạ.",
          timestamp: "2026-11-19T10:28:00+07:00",
        },
      ],
    },
    {
      id: "thr-hn-extra-2",
      shopId: "shop-ha-noi",
      shopName: "Dệt Kim Hà Nội",
      buyerName: "Ngô Văn Tùng",
      productRef: {
        slug: "quan-jogger-ni-chan-cua-xam-muoi-tieu",
        name: "Quần Jogger Nỉ chân cua 320gsm Xám tiêu",
        price: 349000,
        image: "/products/quan-jogger-terry-xam.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-hn-e3",
          sender: "buyer",
          senderName: "Ngô Văn Tùng",
          text: "Quần Jogger nỉ chân cua 320gsm mặc trời Hà Nội 18 độ có đủ ấm không shop?",
          timestamp: "2026-11-19T14:30:00+07:00",
        },
        {
          id: "msg-hn-e4",
          sender: "seller",
          senderName: "Dệt Kim Hà Nội",
          text: "Dạ rất phù hợp từ 16 đến 24 độ C, bề mặt trong dệt vòng chân cua thoáng khí không gây bí khi đi lại nhiều ạ.",
          timestamp: "2026-11-19T14:44:00+07:00",
        },
      ],
    },
  ];
}

let sessionState: SellerSessionState = {
  productOverrides: {},
  addedProducts: [],
  productMetaOverrides: {},
  orderOverrides: {},
  orderExtraOverrides: {},
  voucherActiveOverrides: {},
  addedVouchers: [],
  addedFlashRegs: [],
  addedLedgerEntries: [],
  extraThreads: buildInitialExtraThreads(),
  combos: INITIAL_SELLER_COMBOS,
  tierRules: INITIAL_SELLER_TIER_RULES,
  addonDeals: INITIAL_SELLER_ADDON_DEALS,
  birthdayConfigs: INITIAL_BIRTHDAY_COFUND,
  campaignRegs: INITIAL_CAMPAIGN_REGISTRATIONS,
  reviews: INITIAL_SELLER_REVIEWS,
  questions: INITIAL_SELLER_QUESTIONS,
  faqs: INITIAL_SELLER_FAQS,
  lookbooks: INITIAL_SELLER_LOOKBOOKS,
  factoryProfiles: INITIAL_FACTORY_PROFILES,
  sizeCharts: INITIAL_SHOP_SIZE_CHARTS,
  opsConfigs: INITIAL_OPERATIONS_CONFIG,
};

const listeners = new Set<() => void>();

function emitChange() {
  for (const listener of listeners) {
    listener();
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function getSnapshot(): SellerSessionState {
  return sessionState;
}

export function useSellerSession(): SellerSessionState {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function saveSellerProduct(product: Product, isNew: boolean) {
  if (isNew) {
    sessionState = {
      ...sessionState,
      addedProducts: [product, ...sessionState.addedProducts.filter((p) => p.id !== product.id)],
    };
  } else {
    const inAdded = sessionState.addedProducts.some((p) => p.id === product.id);
    sessionState = {
      ...sessionState,
      addedProducts: inAdded
        ? sessionState.addedProducts.map((p) => (p.id === product.id ? product : p))
        : sessionState.addedProducts,
      productOverrides: {
        ...sessionState.productOverrides,
        [product.id]: product,
      },
    };
  }
  emitChange();
}

export function applySellerOrderStatusOverride(
  orderCode: string,
  nextStatus: OrderStatus,
  note: string
) {
  const nowIso = new Date().toISOString();
  const prev = sessionState.orderOverrides[orderCode];
  const event: OrderTimelineEvent = {
    timestamp: nowIso,
    status: nextStatus,
    title:
      nextStatus === "dang_chuan_bi"
        ? "Gian hàng đã xác nhận đơn"
        : nextStatus === "dang_giao"
          ? "Đã bàn giao vận chuyển"
          : nextStatus === "da_giao"
            ? "Giao hàng thành công"
            : "Cập nhật trạng thái đơn",
    note,
  };
  sessionState = {
    ...sessionState,
    orderOverrides: {
      ...sessionState.orderOverrides,
      [orderCode]: {
        status: nextStatus,
        timelineEvents: [...(prev?.timelineEvents ?? []), event],
      },
    },
  };
  emitChange();
}

export function toggleSellerVoucherActive(voucherId: string, nextActive: boolean) {
  sessionState = {
    ...sessionState,
    voucherActiveOverrides: {
      ...sessionState.voucherActiveOverrides,
      [voucherId]: nextActive,
    },
    addedVouchers: sessionState.addedVouchers.map((v) =>
      v.id === voucherId ? { ...v, active: nextActive } : v
    ),
  };
  emitChange();
}

export function createSellerVoucher(voucher: SellerShopVoucher) {
  sessionState = {
    ...sessionState,
    addedVouchers: [voucher, ...sessionState.addedVouchers],
  };
  emitChange();
}

export function registerSellerFlashSale(reg: SellerFlashRegistration) {
  sessionState = {
    ...sessionState,
    addedFlashRegs: [reg, ...sessionState.addedFlashRegs],
  };
  emitChange();
}

export function createSellerPayoutEntry(shopId: string, amount: number, bankNote: string) {
  const now = new Date();
  const y = String(now.getFullYear()).slice(-2);
  const m = now.getMonth() + 1 < 10 ? `0${now.getMonth() + 1}` : String(now.getMonth() + 1);
  const d = now.getDate() < 10 ? `0${now.getDate()}` : String(now.getDate());
  const dateStr = `${now.getFullYear()}-${m}-${d}`;
  const seq = sessionState.addedLedgerEntries.length + 1;
  const entry: SellerLedgerEntry = {
    id: `led-po-${Date.now()}`,
    entryCode: `SK-${y}${m}${d}-R${seq}`,
    shopId,
    date: dateStr,
    type: "payout_out",
    typeLabel: "Rút tiền về ngân hàng",
    referenceCode: `PO-${y}${m}${d}-${seq}`,
    description: bankNote,
    credit: 0,
    debit: Math.trunc(amount),
    state: "settled",
  };
  sessionState = {
    ...sessionState,
    addedLedgerEntries: [...sessionState.addedLedgerEntries, entry],
  };
  emitChange();
}

export function sendExtraSellerChatMessage(threadId: string, text: string) {
  const clean = text.trim();
  if (!clean) return;
  const nowIso = new Date().toISOString();
  sessionState = {
    ...sessionState,
    extraThreads: sessionState.extraThreads.map((t) =>
      t.id === threadId
        ? {
            ...t,
            unreadBySeller: 0,
            messages: [
              ...t.messages,
              {
                id: `msg-ex-${Date.now()}`,
                sender: "seller",
                senderName: t.shopName,
                text: clean,
                timestamp: nowIso,
              },
            ],
          }
        : t
    ),
  };
  emitChange();
}

/* ---------- Derived selectors (always reconciled from live state) ---------- */

export function getShopProductsWithSession(shopId: string, session: SellerSessionState): Product[] {
  const base = getProductsByShop(shopId).map((p) => session.productOverrides[p.id] ?? p);
  const added = session.addedProducts.filter((p) => p.shopId === shopId);
  return [...added, ...base];
}

export function getProductByIdWithSession(
  id: string,
  session: SellerSessionState
): Product | undefined {
  const added = session.addedProducts.find((p) => p.id === id);
  if (added) return added;
  if (session.productOverrides[id]) return session.productOverrides[id];
  return getProductById(id);
}

export function computeShopLowStockAlerts(products: Product[], threshold = 5): LowStockSkuAlert[] {
  const alerts: LowStockSkuAlert[] = [];
  for (const p of products) {
    for (const sku of p.skus) {
      if (sku.stock <= threshold) {
        const color = p.colors.find((c) => c.code === sku.colorCode);
        alerts.push({
          shopId: p.shopId,
          productId: p.id,
          productSlug: p.slug,
          productName: p.shortName,
          skuCode: sku.skuCode,
          colorName: color?.name ?? sku.colorCode,
          size: sku.size,
          stock: sku.stock,
          price: sku.price,
          image: color?.image ?? p.image,
        });
      }
    }
  }
  return alerts.sort((a, b) => a.stock - b.stock);
}

function buildLineItemFromProduct(
  product: Product,
  size: string,
  quantity: number,
  colorIdx = 0
): OrderLineItem {
  const color = product.colors[colorIdx] ?? product.colors[0];
  const matchedSku =
    product.skus.find((s) => s.colorCode === color.code && s.size === size) ?? product.skus[0];
  return {
    skuCode: matchedSku?.skuCode ?? `${product.id.toUpperCase()}-${size}`,
    productId: product.id,
    productSlug: product.slug,
    productName: product.name,
    colorName: color?.name ?? "Tiêu chuẩn",
    size,
    image: color?.image ?? product.image,
    unitPrice: product.price,
    quantity,
    lineTotal: product.price * quantity,
  };
}

function synthesizeOrderLineItems(record: SellerOrderRecord): OrderLineItem[] {
  const byId = (id: string) => PRODUCTS.find((p) => p.id === id) ?? PRODUCTS[0];
  const mapById: Record<string, OrderLineItem[]> = {
    "so-bd-1": [buildLineItemFromProduct(byId("prod-at-trang"), "L", 2)],
    "so-bd-2": [buildLineItemFromProduct(byId("prod-dolot-boxer"), "XL", 2)],
    "so-bd-3": [
      buildLineItemFromProduct(byId("prod-at-xamda"), "M", 2),
      buildLineItemFromProduct(byId("prod-at-xanhthan"), "L", 1),
    ],
    "so-bd-4": [
      buildLineItemFromProduct(byId("prod-at-trang"), "M", 1),
      buildLineItemFromProduct(byId("prod-dolot-boxer"), "L", 1),
    ],
    "so-bd-5": [buildLineItemFromProduct(byId("prod-at-xanhthan"), "XL", 2)],
    "so-bd-6": [buildLineItemFromProduct(byId("prod-at-trang"), "L", 3)],
    "so-bd-7": [buildLineItemFromProduct(byId("prod-at-xamda"), "S", 2)],
    "so-bd-8": [
      buildLineItemFromProduct(byId("prod-dolot-boxer"), "L", 2),
      buildLineItemFromProduct(byId("prod-at-trang"), "L", 1),
    ],
    "so-bd-9": [
      buildLineItemFromProduct(byId("prod-at-xanhthan"), "M", 1),
      buildLineItemFromProduct(byId("prod-at-xamda"), "M", 1),
    ],
    "so-bd-10": [buildLineItemFromProduct(byId("prod-at-trang"), "2XL", 1)],
    "so-bd-11": [buildLineItemFromProduct(byId("prod-at-xanhthan"), "S", 1)],
  };

  if (mapById[record.id]) {
    return mapById[record.id];
  }

  // Shop-specific deterministic synthesis for SG, DN, HN workspaces matching subtotal
  const shopProds = getProductsByShop(record.shopId);
  if (record.shopId === "shop-sai-gon") {
    if (record.subtotal === 318000) {
      return [buildLineItemFromProduct(byId("prod-at-dryfit"), "L", 2)];
    }
    return [buildLineItemFromProduct(byId("prod-short-daily"), "L", 1)];
  }
  if (record.shopId === "shop-da-nang") {
    if (record.subtotal === 289000) {
      return [buildLineItemFromProduct(byId("prod-polo-kem"), "L", 1)];
    }
    return [buildLineItemFromProduct(byId("prod-short-linen"), "M", 2)];
  }
  if (record.shopId === "shop-ha-noi") {
    if (record.subtotal === 349000) {
      return [buildLineItemFromProduct(byId("prod-jogger-terry"), "L", 1)];
    }
    return [buildLineItemFromProduct(byId("prod-tat-cotrung"), "L", 2)];
  }

  const fallbackProd = shopProds[0] ?? PRODUCTS[0];
  const qty = Math.max(1, record.quantity);
  const unitPrice = Math.round(record.subtotal / qty);
  return [
    {
      ...buildLineItemFromProduct(fallbackProd, "L", qty),
      unitPrice,
      lineTotal: record.subtotal,
    },
  ];
}

function synthesizeOrderTimeline(record: SellerOrderRecord): OrderTimelineEvent[] {
  const events: OrderTimelineEvent[] = [
    {
      timestamp: record.createdAt,
      status: "cho_xac_nhan",
      title: "Đặt hàng thành công",
      note: record.isPaid
        ? "Đã ghi nhận thanh toán trực tuyến và giữ tồn kho theo mã SKU."
        : "Đơn thanh toán COD đã khởi tạo, chờ gian hàng xác nhận.",
    },
  ];

  const baseMs = new Date(record.createdAt).getTime();
  const addHours = (h: number) => new Date(baseMs + h * 3600_000).toISOString();

  if (
    record.status === "dang_chuan_bi" ||
    record.status === "dang_giao" ||
    record.status === "da_giao" ||
    record.status === "yeu_cau_tra_hang"
  ) {
    events.push({
      timestamp: addHours(2),
      status: "dang_chuan_bi",
      title: "Gian hàng đã xác nhận đơn",
      note: "Kho gian hàng đã kiểm đủ SKU và in phiếu vận đơn.",
    });
  }
  if (
    record.status === "dang_giao" ||
    record.status === "da_giao" ||
    record.status === "yeu_cau_tra_hang"
  ) {
    events.push({
      timestamp: addHours(8),
      status: "dang_giao",
      title: "Đã bàn giao đơn vị vận chuyển",
      note: `Kiện hàng đang trên tuyến giao đến ${record.wardName}, ${record.provinceName}.`,
    });
  }
  if (record.status === "da_giao" || record.status === "yeu_cau_tra_hang") {
    events.push({
      timestamp: addHours(34),
      status: "da_giao",
      title: "Giao hàng thành công",
      note: `Người nhận ${record.buyerName} đã ký nhận đủ kiện hàng.`,
    });
  }
  if (record.status === "yeu_cau_tra_hang") {
    events.push({
      timestamp: addHours(52),
      status: "yeu_cau_tra_hang",
      title: "Yêu cầu đổi size 2 chiều / trả hàng",
      note: "Người mua gửi ảnh nguyên tem mác, đề nghị đổi size tận nơi theo chính sách 45 ngày.",
    });
  }
  if (record.status === "da_huy") {
    events.push({
      timestamp: addHours(1),
      status: "da_huy",
      title: "Đơn hàng đã hủy",
      note: "Đơn hủy trước khi bàn giao vận chuyển. Đã hoàn lại tồn kho SKU.",
    });
  }
  return events;
}

export function getUnifiedSellerOrders(
  shopId: string,
  storeOrders: Order[],
  session: SellerSessionState
): UnifiedSellerOrder[] {
  const workspace = getSellerWorkspace(shopId);
  const shop = getShopById(shopId);
  const commissionRate = shop?.commissionRate ?? 0.05;
  const storeByCode = new Map<string, Order>();
  for (const so of storeOrders) {
    if (so.shopId === shopId) {
      storeByCode.set(so.orderCode, so);
    }
  }

  const seenCodes = new Set<string>();
  const unified: UnifiedSellerOrder[] = workspace.orders.map((rec) => {
    seenCodes.add(rec.orderCode);
    const linked = storeByCode.get(rec.orderCode);
    const override = session.orderOverrides[rec.orderCode];
    const effectiveStatus = override?.status ?? linked?.status ?? rec.status;
    const baseTimeline = linked?.timeline ?? synthesizeOrderTimeline(rec);
    const timeline = override ? [...baseTimeline, ...override.timelineEvents] : baseTimeline;
    const wave2Extra =
      session.orderExtraOverrides[rec.orderCode] ?? SELLER_ORDER_EXTRAS[rec.orderCode];

    return {
      ...rec,
      status: effectiveStatus,
      storeOrderId: linked?.id,
      shippingAddress:
        override?.addressOverride ??
        linked?.shippingAddress ??
        `${rec.wardName}, ${rec.provinceName}`,
      trackingCode:
        linked?.trackingCode ??
        (effectiveStatus !== "cho_xac_nhan" && effectiveStatus !== "da_huy"
          ? `NMLOG-${rec.orderCode.slice(-3)}419`
          : undefined),
      note: wave2Extra?.deliveryNote ?? linked?.note,
      items: linked?.items ?? synthesizeOrderLineItems(rec),
      timeline,
      wave2Extra,
    };
  });

  // Also surface newly placed checkout orders from StoreContext that belong to this shop
  const extraFromStore: UnifiedSellerOrder[] = [];
  for (const so of storeOrders) {
    if (so.shopId !== shopId || seenCodes.has(so.orderCode)) continue;
    // Only include dynamically created checkout orders (id starts with ord-1...) so initial badge counts reconcile
    if (!/^ord-\d{10,}/.test(so.id)) continue;
    const taxable = Math.max(0, so.subtotal - so.shopVoucherDiscount);
    const platformFee = Math.round(taxable * commissionRate);
    const netReceivable = taxable - platformFee;
    const override = session.orderOverrides[so.orderCode];
    const effectiveStatus = override?.status ?? so.status;
    const parts = so.shippingAddress.split(",").map((s) => s.trim());
    const provinceName = parts.at(-1) ?? so.provinceCode;
    const wardName = parts.length >= 2 ? parts[parts.length - 2] : "Khu vực trung tâm";

    extraFromStore.push({
      id: so.id,
      orderCode: so.orderCode,
      shopId: so.shopId,
      buyerName: so.buyerName,
      buyerPhone: so.buyerPhone,
      provinceName,
      wardName,
      createdAt: so.createdAt,
      slaDeadline: new Date(new Date(so.createdAt).getTime() + 18 * 3600_000).toISOString(),
      status: effectiveStatus,
      paymentMethod: so.paymentMethod,
      isPaid: so.isPaid,
      itemSummary: so.items
        .map((it) => `${it.productName} · ${it.size} (x${it.quantity})`)
        .join(", "),
      skuCount: so.items.length,
      quantity: so.items.reduce((s, it) => s + it.quantity, 0),
      subtotal: so.subtotal,
      shopVoucherDiscount: so.shopVoucherDiscount,
      platformFee,
      netReceivable,
      storeOrderId: so.id,
      shippingAddress: so.shippingAddress,
      trackingCode: so.trackingCode,
      note: so.note,
      items: so.items,
      timeline: override ? [...so.timeline, ...override.timelineEvents] : so.timeline,
    });
  }

  return [...extraFromStore, ...unified];
}

export function findSellerOrderByAnyId(
  shopId: string,
  orderIdOrCode: string,
  storeOrders: Order[],
  session: SellerSessionState
): UnifiedSellerOrder | undefined {
  const shopOrders = getUnifiedSellerOrders(shopId, storeOrders, session);
  const direct = shopOrders.find(
    (o) =>
      o.id === orderIdOrCode || o.storeOrderId === orderIdOrCode || o.orderCode === orderIdOrCode
  );
  if (direct) return direct;

  // Fallback across all shops in case user navigated directly via URL
  const allShopIds = ["shop-bach-dang", "shop-sai-gon", "shop-da-nang", "shop-ha-noi"];
  for (const sid of allShopIds) {
    if (sid === shopId) continue;
    const list = getUnifiedSellerOrders(sid, storeOrders, session);
    const hit = list.find(
      (o) =>
        o.id === orderIdOrCode || o.storeOrderId === orderIdOrCode || o.orderCode === orderIdOrCode
    );
    if (hit) return hit;
  }
  return undefined;
}

export function getShopVouchersWithSession(
  shopId: string,
  session: SellerSessionState
): SellerShopVoucher[] {
  const workspace = getSellerWorkspace(shopId);
  const promoByCode = new Map<string, SellerPromotionRecord>();
  for (const p of workspace.promotions) {
    if (p.kind === "voucher") promoByCode.set(p.codeOrSlot, p);
  }

  const fromBase: SellerShopVoucher[] = VOUCHERS.filter(
    (v) => v.scope === "shop" && v.shopId === shopId
  ).map((v) => {
    const promo = promoByCode.get(v.code);
    const id = promo?.id ?? `sv-${v.code}`;
    const used = promo?.usedQuota ?? v.totalUses - v.remainingUses;
    const total = promo?.totalQuota ?? v.totalUses;
    const defaultActive = promo ? promo.status === "dang_chay" : true;
    const active = session.voucherActiveOverrides[id] ?? defaultActive;
    return {
      id,
      shopId,
      code: v.code,
      title: v.title,
      discountType: v.discountType,
      discountValue: v.discountValue,
      maxDiscount: v.maxDiscount,
      minOrderValue: v.minOrderValue,
      usedQuota: used,
      totalQuota: total,
      attributedRevenue: promo?.attributedRevenue ?? used * 240000,
      periodLabel: promo?.periodLabel ?? "10/11 - 28/11",
      active,
    };
  });

  // Include additional shop voucher promos from workspace that aren't in VOUCHERS yet
  const existingCodes = new Set(fromBase.map((v) => v.code));
  for (const p of workspace.promotions) {
    if (p.kind !== "voucher" || existingCodes.has(p.codeOrSlot)) continue;
    const active = session.voucherActiveOverrides[p.id] ?? p.status === "dang_chay";
    fromBase.push({
      id: p.id,
      shopId,
      code: p.codeOrSlot,
      title: p.title,
      discountType: p.discountType ?? "fixed",
      discountValue: p.discountValue ?? 35000,
      maxDiscount: p.maxDiscount,
      minOrderValue: p.minOrderValue ?? 450000,
      usedQuota: p.usedQuota,
      totalQuota: p.totalQuota,
      attributedRevenue: p.attributedRevenue,
      periodLabel: p.periodLabel,
      active,
    });
  }

  const added = session.addedVouchers.filter((v) => v.shopId === shopId);
  return [...added, ...fromBase];
}

export function getShopFlashRegistrationsWithSession(
  shopId: string,
  session: SellerSessionState
): SellerFlashRegistration[] {
  const regs: SellerFlashRegistration[] = [];
  for (const slot of FLASH_SALE_SLOTS) {
    for (const item of slot.items) {
      const prod = getProductByIdWithSession(item.productId, session);
      if (!prod || prod.shopId !== shopId) continue;
      regs.push({
        id: item.id,
        shopId,
        slotId: slot.id,
        slotLabel: `${slot.startTime} - ${slot.endTime}`,
        slotStatus: slot.status,
        productId: prod.id,
        productName: prod.shortName,
        productImage: prod.image,
        originalPrice: item.originalPrice,
        flashPrice: item.flashPrice,
        usedQuota: Math.max(0, item.totalQuota - item.remainingQuota),
        totalQuota: item.totalQuota,
        limitPerBuyer: item.limitPerBuyer,
      });
    }
  }
  const added = session.addedFlashRegs.filter((r) => r.shopId === shopId);
  return [...added, ...regs];
}

export function getShopLedgerWithSession(
  shopId: string,
  session: SellerSessionState
): {
  entries: LedgerEntryWithBalance[];
  availableBalance: number;
  pendingBalance: number;
  totalCredits: number;
  totalDebits: number;
  totalFeesAndRefunds: number;
  totalPaidOut: number;
  netBalance: number;
} {
  const workspace = getSellerWorkspace(shopId);
  const added = session.addedLedgerEntries.filter((e) => e.shopId === shopId);
  const chronological = [...workspace.ledger, ...added];

  let running = 0;
  const withRunning: LedgerEntryWithBalance[] = chronological.map((entry) => {
    running += entry.credit - entry.debit;
    return {
      ...entry,
      runningBalance: running,
    };
  });

  const availableBalance = chronological
    .filter((e) => e.state === "settled")
    .reduce((s, e) => s + e.credit - e.debit, 0);
  const pendingBalance = chronological
    .filter((e) => e.state === "pending")
    .reduce((s, e) => s + e.credit - e.debit, 0);
  const totalCredits = chronological.reduce((s, e) => s + e.credit, 0);
  const totalDebits = chronological.reduce((s, e) => s + e.debit, 0);
  const totalFeesAndRefunds = chronological
    .filter((e) => e.type === "platform_fee" || e.type === "refund_debit")
    .reduce((s, e) => s + e.debit, 0);
  const totalPaidOut = chronological
    .filter((e) => e.type === "payout_out")
    .reduce((s, e) => s + e.debit, 0);

  return {
    entries: [...withRunning].reverse(),
    availableBalance,
    pendingBalance,
    totalCredits,
    totalDebits,
    totalFeesAndRefunds,
    totalPaidOut,
    netBalance: totalCredits - totalDebits,
  };
}

export function getShopChatThreadsWithSession(
  shopId: string,
  storeThreads: ChatThread[],
  session: SellerSessionState
): Array<ChatThread & { source: "store" | "extra" }> {
  const fromStore = storeThreads
    .filter((t) => t.shopId === shopId)
    .map((t) => ({ ...t, source: "store" as const }));
  const fromExtra = session.extraThreads
    .filter((t) => t.shopId === shopId)
    .map((t) => ({ ...t, source: "extra" as const }));
  return [...fromStore, ...fromExtra];
}

/* ============================================================================
   WAVE 2 ACTIONS & SELECTORS
   ============================================================================ */

export function getProductExtendedMetaWithSession(
  productId: string,
  categorySlug: string,
  session: SellerSessionState
): SellerProductExtendedMeta {
  return session.productMetaOverrides[productId] ?? getDefaultProductMeta(productId, categorySlug);
}

export function saveSellerProductMeta(productId: string, meta: SellerProductExtendedMeta) {
  sessionState = {
    ...sessionState,
    productMetaOverrides: {
      ...sessionState.productMetaOverrides,
      [productId]: meta,
    },
  };
  emitChange();
}

export interface QuickRestockResult {
  newStock: number;
  notifiedCount: number;
}

export function quickRestockSellerSku(
  product: Product,
  skuCode: string,
  addQty: number
): QuickRestockResult {
  const delta = Math.max(1, Math.trunc(addQty));
  let updatedStock = delta;
  const updatedProduct: Product = {
    ...product,
    skus: product.skus.map((s) => {
      if (s.skuCode !== skuCode) return s;
      updatedStock = s.stock + delta;
      return { ...s, stock: updatedStock };
    }),
  };

  const currentMeta = getProductExtendedMetaWithSession(
    product.id,
    product.categorySlug,
    sessionState
  );
  const notifiedCount = currentMeta.backInStockSubscribers[skuCode] ?? 0;
  const nextSubscribers = { ...currentMeta.backInStockSubscribers };
  if (notifiedCount > 0) {
    nextSubscribers[skuCode] = 0;
  }

  const inAdded = sessionState.addedProducts.some((p) => p.id === product.id);
  sessionState = {
    ...sessionState,
    addedProducts: inAdded
      ? sessionState.addedProducts.map((p) => (p.id === product.id ? updatedProduct : p))
      : sessionState.addedProducts,
    productOverrides: {
      ...sessionState.productOverrides,
      [product.id]: updatedProduct,
    },
    productMetaOverrides: {
      ...sessionState.productMetaOverrides,
      [product.id]: {
        ...currentMeta,
        backInStockSubscribers: nextSubscribers,
      },
    },
  };
  emitChange();
  return { newStock: updatedStock, notifiedCount };
}

export interface BackInStockSkuRow {
  productId: string;
  productName: string;
  productImage: string;
  skuCode: string;
  colorName: string;
  size: string;
  stock: number;
  waitingCount: number;
  wishlistCount: number;
  price: number;
}

export function getBackInStockRowsWithSession(
  shopId: string,
  session: SellerSessionState
): BackInStockSkuRow[] {
  const products = getShopProductsWithSession(shopId, session);
  const rows: BackInStockSkuRow[] = [];
  for (const p of products) {
    const meta = getProductExtendedMetaWithSession(p.id, p.categorySlug, session);
    for (const sku of p.skus) {
      const waiting = meta.backInStockSubscribers[sku.skuCode] ?? 0;
      if (waiting > 0 || sku.stock <= 5) {
        const color = p.colors.find((c) => c.code === sku.colorCode);
        rows.push({
          productId: p.id,
          productName: p.shortName,
          productImage: color?.image ?? p.image,
          skuCode: sku.skuCode,
          colorName: color?.name ?? sku.colorCode,
          size: sku.size,
          stock: sku.stock,
          waitingCount: waiting,
          wishlistCount: meta.wishlistCount,
          price: sku.price,
        });
      }
    }
  }
  return rows.sort((a, b) => b.waitingCount - a.waitingCount || a.stock - b.stock);
}

export function getTotalWaitingSubscribers(product: Product, session: SellerSessionState): number {
  const meta = getProductExtendedMetaWithSession(product.id, product.categorySlug, session);
  return Object.values(meta.backInStockSubscribers).reduce((sum, n) => sum + n, 0);
}

/* ---------- Wave 2 Order Operations ---------- */

function getBaseOrderExtra(orderCode: string): SellerOrderWave2Extra {
  return (
    sessionState.orderExtraOverrides[orderCode] ??
    SELLER_ORDER_EXTRAS[orderCode] ?? {
      orderCode,
      giftBagRequested: false,
    }
  );
}

export function advanceOrderVatStatus(
  orderCode: string,
  currentStatus: OrderStatus,
  nextStatus: VatInvoiceStatus
) {
  const base = getBaseOrderExtra(orderCode);
  if (!base.vatRequest) return;
  const nowIso = new Date().toISOString();
  const invoiceNo =
    nextStatus === "da_phat_hanh"
      ? `HD-NM-2026-${orderCode.slice(-3)}${String(Date.now()).slice(-2)}`
      : base.vatRequest.issuedInvoiceNo;
  const prevOv = sessionState.orderOverrides[orderCode];
  const keptStatus = prevOv?.status ?? currentStatus;

  sessionState = {
    ...sessionState,
    orderExtraOverrides: {
      ...sessionState.orderExtraOverrides,
      [orderCode]: {
        ...base,
        vatRequest: {
          ...base.vatRequest,
          status: nextStatus,
          draftDeadline24h:
            nextStatus === "da_gui_nhap_24h"
              ? new Date(Date.now() + 24 * 3600_000).toISOString()
              : base.vatRequest.draftDeadline24h,
          issuedInvoiceNo: invoiceNo,
        },
      },
    },
    orderOverrides: {
      ...sessionState.orderOverrides,
      [orderCode]: {
        status: keptStatus,
        addressOverride: prevOv?.addressOverride,
        timelineEvents: [
          ...(prevOv?.timelineEvents ?? []),
          {
            timestamp: nowIso,
            status: keptStatus,
            title:
              nextStatus === "da_gui_nhap_24h"
                ? "Đã gửi bản nháp hóa đơn VAT (duyệt 24h)"
                : "Đã phát hành hóa đơn điện tử VAT",
            note:
              nextStatus === "da_gui_nhap_24h"
                ? `Đã gửi bản nháp tới ${base.vatRequest.email} (MST ${base.vatRequest.taxCode}).`
                : `Số hóa đơn ${invoiceNo} đã gửi về email kế toán ${base.vatRequest.email}.`,
          },
        ],
      },
    },
  };
  emitChange();
}

export function resolveOrderAmendment(
  orderCode: string,
  currentStatus: OrderStatus,
  decision: "da_duyet" | "tu_choi",
  note: string
) {
  const base = getBaseOrderExtra(orderCode);
  if (!base.amendment) return;
  const nowIso = new Date().toISOString();
  const isApprovedCancel = decision === "da_duyet" && base.amendment.kind === "huy_muon";
  const isApprovedAddr =
    decision === "da_duyet" &&
    base.amendment.kind === "doi_dia_chi" &&
    Boolean(base.amendment.proposedAddress);

  const nextStatus: OrderStatus = isApprovedCancel ? "da_huy" : currentStatus;
  const prevOv = sessionState.orderOverrides[orderCode];

  sessionState = {
    ...sessionState,
    orderExtraOverrides: {
      ...sessionState.orderExtraOverrides,
      [orderCode]: {
        ...base,
        amendment: {
          ...base.amendment,
          status: decision,
          resolutionNote: note,
        },
      },
    },
    orderOverrides: {
      ...sessionState.orderOverrides,
      [orderCode]: {
        status: nextStatus,
        addressOverride: isApprovedAddr ? base.amendment.proposedAddress : prevOv?.addressOverride,
        timelineEvents: [
          ...(prevOv?.timelineEvents ?? []),
          {
            timestamp: nowIso,
            status: nextStatus,
            title:
              decision === "da_duyet"
                ? base.amendment.kind === "huy_muon"
                  ? "Duyệt yêu cầu hủy muộn & hoàn tồn SKU"
                  : "Duyệt cập nhật địa chỉ giao hàng mới"
                : base.amendment.kind === "huy_muon"
                  ? "Từ chối yêu cầu hủy muộn"
                  : "Từ chối yêu cầu đổi địa chỉ",
            note,
          },
        ],
      },
    },
  };
  emitChange();
}

export function advanceOrderTwoWayExchange(
  orderCode: string,
  currentStatus: OrderStatus,
  nextCourierStatus: TwoWayCourierStatus
) {
  const base = getBaseOrderExtra(orderCode);
  if (!base.twoWayExchange) return;
  const nowIso = new Date().toISOString();
  const waybill =
    base.twoWayExchange.exchangeWaybill ??
    `NMLOG-2W-${orderCode.slice(-3)}${String(Date.now()).slice(-2)}`;
  const prevOv = sessionState.orderOverrides[orderCode];
  const nextOrderStatus: OrderStatus = nextCourierStatus === "hoan_tat" ? "da_giao" : currentStatus;

  const titleByStep: Record<TwoWayCourierStatus, string> = {
    cho_dieu_phoi: "Tiếp nhận yêu cầu đổi size 2 chiều",
    da_dat_buu_ta: "Đã điều phối bưu tá đổi size 2 chiều tận nơi",
    dang_doi_2_chieu: "Bưu tá đang giao size mới & thu hồi size cũ",
    hoan_tat: "Hoàn tất đổi size 2 chiều tận nơi (45 ngày)",
  };

  sessionState = {
    ...sessionState,
    orderExtraOverrides: {
      ...sessionState.orderExtraOverrides,
      [orderCode]: {
        ...base,
        twoWayExchange: {
          ...base.twoWayExchange,
          courierStatus: nextCourierStatus,
          exchangeWaybill: waybill,
        },
      },
    },
    orderOverrides: {
      ...sessionState.orderOverrides,
      [orderCode]: {
        status: nextOrderStatus,
        addressOverride: prevOv?.addressOverride,
        timelineEvents: [
          ...(prevOv?.timelineEvents ?? []),
          {
            timestamp: nowIso,
            status: nextOrderStatus,
            title: titleByStep[nextCourierStatus],
            note: `Mã vận đơn 2 chiều ${waybill} · Đổi từ ${base.twoWayExchange.fromVariant} sang ${base.twoWayExchange.toVariant}.`,
          },
        ],
      },
    },
  };
  emitChange();
}

export function replyOrderTier1Complaint(
  orderCode: string,
  responseText: string,
  nextStatus: "da_phan_hoi" | "da_xu_ly" | "chuyen_san"
) {
  const base = getBaseOrderExtra(orderCode);
  if (!base.tier1Complaint) return;
  const clean = responseText.trim();
  sessionState = {
    ...sessionState,
    orderExtraOverrides: {
      ...sessionState.orderExtraOverrides,
      [orderCode]: {
        ...base,
        tier1Complaint: {
          ...base.tier1Complaint,
          status: nextStatus,
          sellerResponse: clean || base.tier1Complaint.sellerResponse,
          slaRemainingLabel:
            nextStatus === "da_xu_ly"
              ? "Đã đóng trong hạn SLA"
              : nextStatus === "chuyen_san"
                ? "Đã chuyển khiếu nại cấp 2 lên Admin"
                : "Đã phản hồi đúng hạn SLA 24h",
        },
      },
    },
  };
  emitChange();
}

/* ---------- Wave 2 Promotions Actions ---------- */

export function createSellerCombo(combo: SellerBundleCombo) {
  sessionState = {
    ...sessionState,
    combos: [combo, ...sessionState.combos],
  };
  emitChange();
}

export function toggleSellerComboActive(comboId: string, active: boolean) {
  sessionState = {
    ...sessionState,
    combos: sessionState.combos.map((c) => (c.id === comboId ? { ...c, active } : c)),
  };
  emitChange();
}

export function createSellerTierRule(rule: SellerTierDiscountRule) {
  sessionState = {
    ...sessionState,
    tierRules: [rule, ...sessionState.tierRules],
  };
  emitChange();
}

export function toggleSellerTierRuleActive(ruleId: string, active: boolean) {
  sessionState = {
    ...sessionState,
    tierRules: sessionState.tierRules.map((r) => (r.id === ruleId ? { ...r, active } : r)),
  };
  emitChange();
}

export function createSellerAddonDeal(deal: SellerAddonDeal) {
  sessionState = {
    ...sessionState,
    addonDeals: [deal, ...sessionState.addonDeals],
  };
  emitChange();
}

export function toggleSellerAddonDealActive(dealId: string, active: boolean) {
  sessionState = {
    ...sessionState,
    addonDeals: sessionState.addonDeals.map((d) => (d.id === dealId ? { ...d, active } : d)),
  };
  emitChange();
}

export function updateSellerBirthdayConfig(
  shopId: string,
  patch: Partial<SellerBirthdayCofundConfig>
) {
  const prev = sessionState.birthdayConfigs[shopId] ?? INITIAL_BIRTHDAY_COFUND["shop-bach-dang"];
  sessionState = {
    ...sessionState,
    birthdayConfigs: {
      ...sessionState.birthdayConfigs,
      [shopId]: { ...prev, ...patch },
    },
  };
  emitChange();
}

export function registerSellerCampaignSlot(reg: SellerCampaignRegistration) {
  sessionState = {
    ...sessionState,
    campaignRegs: [reg, ...sessionState.campaignRegs],
  };
  emitChange();
}

/* ---------- Wave 2 Interaction & Content Actions (/seller/tuong-tac) ---------- */

export function replyToSellerReview(reviewId: string, replyText: string) {
  const clean = replyText.trim();
  if (!clean) return;
  const nowIso = new Date().toISOString();
  sessionState = {
    ...sessionState,
    reviews: sessionState.reviews.map((r) =>
      r.id === reviewId ? { ...r, reply: { text: clean, repliedAt: nowIso } } : r
    ),
  };
  emitChange();
}

export function answerSellerQuestion(questionId: string, answerText: string) {
  const clean = answerText.trim();
  if (!clean) return;
  const nowIso = new Date().toISOString();
  sessionState = {
    ...sessionState,
    questions: sessionState.questions.map((q) =>
      q.id === questionId ? { ...q, answer: clean, answeredAt: nowIso } : q
    ),
  };
  emitChange();
}

export function createSellerFaqItem(item: SellerShopFaqItem) {
  sessionState = {
    ...sessionState,
    faqs: [item, ...sessionState.faqs],
  };
  emitChange();
}

export function toggleSellerFaqActive(faqId: string, active: boolean) {
  sessionState = {
    ...sessionState,
    faqs: sessionState.faqs.map((f) => (f.id === faqId ? { ...f, active } : f)),
  };
  emitChange();
}

export function createSellerLookbookPost(post: SellerLookbookPost) {
  sessionState = {
    ...sessionState,
    lookbooks: [post, ...sessionState.lookbooks],
  };
  emitChange();
}

export function submitSellerLookbookForReview(postId: string) {
  sessionState = {
    ...sessionState,
    lookbooks: sessionState.lookbooks.map((lb) =>
      lb.id === postId
        ? {
            ...lb,
            status: "cho_duyet",
            adminNote: "Đã gửi hàng đợi kiểm duyệt nội dung sàn NexusMart.",
          }
        : lb
    ),
  };
  emitChange();
}

export function getPendingInteractionCount(shopId: string, session: SellerSessionState): number {
  const unansweredReviews = session.reviews.filter((r) => r.shopId === shopId && !r.reply).length;
  const unansweredQuestions = session.questions.filter(
    (q) => q.shopId === shopId && !q.answer
  ).length;
  return unansweredReviews + unansweredQuestions;
}

/* ---------- Wave 2 Factory Profile, Size Charts & Operations Actions (/seller/ho-so) ---------- */

export function getShopFactoryProfileWithSession(
  shopId: string,
  session: SellerSessionState
): SellerFactoryProfile {
  return session.factoryProfiles[shopId] ?? INITIAL_FACTORY_PROFILES["shop-bach-dang"];
}

export function updateShopFactoryProfile(shopId: string, patch: Partial<SellerFactoryProfile>) {
  const prev = getShopFactoryProfileWithSession(shopId, sessionState);
  sessionState = {
    ...sessionState,
    factoryProfiles: {
      ...sessionState.factoryProfiles,
      [shopId]: { ...prev, ...patch },
    },
  };
  emitChange();
}

export function addShopCollectionItem(shopId: string, collection: SellerShopCollection) {
  const prev = getShopFactoryProfileWithSession(shopId, sessionState);
  sessionState = {
    ...sessionState,
    factoryProfiles: {
      ...sessionState.factoryProfiles,
      [shopId]: {
        ...prev,
        collections: [collection, ...prev.collections],
      },
    },
  };
  emitChange();
}

export function toggleShopMenuLinkVisibility(shopId: string, menuId: string, visible: boolean) {
  const prev = getShopFactoryProfileWithSession(shopId, sessionState);
  sessionState = {
    ...sessionState,
    factoryProfiles: {
      ...sessionState.factoryProfiles,
      [shopId]: {
        ...prev,
        shopMenu: prev.shopMenu.map((m) => (m.id === menuId ? { ...m, visible } : m)),
      },
    },
  };
  emitChange();
}

export function getShopSizeChartsWithSession(
  shopId: string,
  session: SellerSessionState
): SellerShopSizeCharts {
  return session.sizeCharts[shopId] ?? INITIAL_SHOP_SIZE_CHARTS["shop-bach-dang"];
}

export function updateShopSizeCharts(shopId: string, nextCharts: SellerShopSizeCharts) {
  sessionState = {
    ...sessionState,
    sizeCharts: {
      ...sessionState.sizeCharts,
      [shopId]: nextCharts,
    },
  };
  emitChange();
}

export function getShopOperationsConfigWithSession(
  shopId: string,
  session: SellerSessionState
): SellerOperationsConfig {
  return session.opsConfigs[shopId] ?? INITIAL_OPERATIONS_CONFIG["shop-bach-dang"];
}

export function updateShopOperationsConfig(shopId: string, patch: Partial<SellerOperationsConfig>) {
  const prev = getShopOperationsConfigWithSession(shopId, sessionState);
  sessionState = {
    ...sessionState,
    opsConfigs: {
      ...sessionState.opsConfigs,
      [shopId]: { ...prev, ...patch },
    },
  };
  emitChange();
}
