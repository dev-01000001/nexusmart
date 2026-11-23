"use client";

import { useSyncExternalStore } from "react";
import {
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
  getDefaultProductMeta,
  getProductsByShop,
} from "@/data";
import type {
  ChatThread,
  Product,
  SellerAddonDeal,
  SellerBirthdayCofundConfig,
  SellerBundleCombo,
  SellerCampaignRegistration,
  SellerFactoryProfile,
  SellerLookbookPost,
  SellerOperationsConfig,
  SellerProductExtendedMeta,
  SellerProductQuestion,
  SellerSampleReview,
  SellerShopCollection,
  SellerShopFaqItem,
  SellerShopSizeCharts,
  SellerTierDiscountRule,
} from "@/data";

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

interface SellerSessionState {
  productOverrides: Record<string, Product>;
  productMetaOverrides: Record<string, SellerProductExtendedMeta>;
  extraThreads: ChatThread[];
  combos: SellerBundleCombo[];
  tierRules: SellerTierDiscountRule[];
  addonDeals: SellerAddonDeal[];
  birthdayConfigs: Record<string, SellerBirthdayCofundConfig>;
  campaignRegs: SellerCampaignRegistration[];
  reviews: SellerSampleReview[];
  questions: SellerProductQuestion[];
  faqs: SellerShopFaqItem[];
  lookbooks: SellerLookbookPost[];
  factoryProfiles: Record<string, SellerFactoryProfile>;
  sizeCharts: Record<string, SellerShopSizeCharts>;
  opsConfigs: Record<string, SellerOperationsConfig>;
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
          text: "Shop cho hỏi đơn gồm 2 combo Boxer Modal XL có đóng gói kín tên sản phẩm không ạ?",
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
          text: "Shop giúp mình kiểm tra kỹ đường may cổ áo trước khi gửi nhé.",
          timestamp: "2026-11-19T22:24:00+07:00",
        },
        {
          id: "msg-bd-e4",
          sender: "seller",
          senderName: "Xưởng Dệt Bạch Đằng",
          text: "Cảm ơn chị Hà, bộ phận kho sẽ kiểm tra bo cổ và đóng gói giao trong chuyến sáng nay ạ.",
          timestamp: "2026-11-19T22:32:00+07:00",
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
          text: "Mẫu Daily 7 inch có hai túi hông sâu và túi mổ cài cúc phía sau ạ.",
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
          text: "Xưởng đã xử lý wash mềm trước khi may nên độ co dưới 1,5% ạ.",
          timestamp: "2026-11-19T16:17:00+07:00",
        },
      ],
    },
    {
      id: "thr-hn-extra-1",
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
          text: "Dạ rất phù hợp từ 16 đến 24 độ C, bề mặt trong dệt vòng chân cua thoáng khí ạ.",
          timestamp: "2026-11-19T14:44:00+07:00",
        },
      ],
    },
  ];
}

let sessionState: SellerSessionState = {
  productOverrides: {},
  productMetaOverrides: {},
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

/* ---------- Mock helpers kept only for demo tabs (/seller/tuong-tac, /seller/ho-so, /seller/khuyen-mai combo/campaign) ---------- */

export function getShopProductsWithSession(shopId: string, session: SellerSessionState): Product[] {
  return getProductsByShop(shopId).map((p) => session.productOverrides[p.id] ?? p);
}

export function getProductExtendedMetaWithSession(
  productId: string,
  categorySlug: string,
  session: SellerSessionState
): SellerProductExtendedMeta {
  return session.productMetaOverrides[productId] ?? getDefaultProductMeta(productId, categorySlug);
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

  sessionState = {
    ...sessionState,
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

export function createSellerCombo(combo: SellerBundleCombo) {
  sessionState = { ...sessionState, combos: [combo, ...sessionState.combos] };
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
  sessionState = { ...sessionState, tierRules: [rule, ...sessionState.tierRules] };
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
  sessionState = { ...sessionState, addonDeals: [deal, ...sessionState.addonDeals] };
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
  sessionState = { ...sessionState, campaignRegs: [reg, ...sessionState.campaignRegs] };
  emitChange();
}

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
  sessionState = { ...sessionState, faqs: [item, ...sessionState.faqs] };
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
  sessionState = { ...sessionState, lookbooks: [post, ...sessionState.lookbooks] };
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
