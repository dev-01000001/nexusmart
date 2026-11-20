"use client";

/**
 * In-memory mock state for Seller Center tabs/screens that do NOT have backend RPCs yet:
 * - /seller/tuong-tac (reviews, Q&A, FAQs, lookbooks)
 * - /seller/ho-so (factory QC profile, custom size charts, local ops config)
 * - /seller/khuyen-mai mock-only tabs (Combo & Mua kèm, Chiến dịch & BST sàn)
 * - /seller/chat (1-1 mock chat threads)
 *
 * Real backend entities (products, SKU stock, orders, shop vouchers, flash sale registrations,
 * shop profile, seller balances) are NEVER stored here and NEVER stored in localStorage.
 */
import { useSyncExternalStore } from "react";
import {
  INITIAL_BIRTHDAY_COFUND,
  INITIAL_CAMPAIGN_REGISTRATIONS,
  INITIAL_CHAT_THREADS,
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
} from "@/data";
import type {
  ChatThread,
  OrderStatusMeta,
  SellerAddonDeal,
  SellerBirthdayCofundConfig,
  SellerBundleCombo,
  SellerCampaignRegistration,
  SellerFactoryProfile,
  SellerLookbookPost,
  SellerOperationsConfig,
  SellerProductQuestion,
  SellerSampleReview,
  SellerShopCollection,
  SellerShopFaqItem,
  SellerShopSizeCharts,
  SellerTierDiscountRule,
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

interface SellerMockState {
  chatThreads: ChatThread[];
  // Wave 2 mock-only promotions
  combos: SellerBundleCombo[];
  tierRules: SellerTierDiscountRule[];
  addonDeals: SellerAddonDeal[];
  birthdayConfigs: Record<string, SellerBirthdayCofundConfig>;
  campaignRegs: SellerCampaignRegistration[];
  // Wave 2 mock-only interaction & content
  reviews: SellerSampleReview[];
  questions: SellerProductQuestion[];
  faqs: SellerShopFaqItem[];
  lookbooks: SellerLookbookPost[];
  // Wave 2 mock-only factory profile, size charts & operations
  factoryProfiles: Record<string, SellerFactoryProfile>;
  sizeCharts: Record<string, SellerShopSizeCharts>;
  opsConfigs: Record<string, SellerOperationsConfig>;
}

function buildInitialMockChatThreads(): ChatThread[] {
  return [
    ...INITIAL_CHAT_THREADS,
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
          text: "Shop cho hỏi đơn combo Boxer Modal XL có đóng gói kín tên sản phẩm không ạ?",
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
          text: "Áo Polo Dệt kim Đen than còn sẵn size L không shop?",
          timestamp: "2026-11-19T10:15:00+07:00",
        },
        {
          id: "msg-hn-e2",
          sender: "seller",
          senderName: "Dệt Kim Hà Nội",
          text: "Kho Hà Nội đang còn sẵn màu Đen than size L để giao trong ngày ạ.",
          timestamp: "2026-11-19T10:28:00+07:00",
        },
      ],
    },
  ];
}

let sessionState: SellerMockState = {
  chatThreads: buildInitialMockChatThreads(),
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

function getSnapshot(): SellerMockState {
  return sessionState;
}

export function useSellerSession(): SellerMockState {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

/* ---------- Mock Chat ---------- */

export function sendMockSellerChatMessage(threadId: string, text: string) {
  const clean = text.trim();
  if (!clean) return;
  const nowIso = new Date().toISOString();
  sessionState = {
    ...sessionState,
    chatThreads: sessionState.chatThreads.map((t) =>
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

export function getShopMockChatThreads(shopKey: string, session: SellerMockState): ChatThread[] {
  return session.chatThreads.filter((t) => t.shopId === shopKey);
}

/* ---------- Wave 2 Mock-Only Promotions Actions ---------- */

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
  shopKey: string,
  patch: Partial<SellerBirthdayCofundConfig>
) {
  const prev = sessionState.birthdayConfigs[shopKey] ?? INITIAL_BIRTHDAY_COFUND["shop-bach-dang"];
  sessionState = {
    ...sessionState,
    birthdayConfigs: {
      ...sessionState.birthdayConfigs,
      [shopKey]: { ...prev, ...patch },
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

/* ---------- Wave 2 Mock-Only Interaction & Content Actions (/seller/tuong-tac) ---------- */

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

export function getPendingInteractionCount(shopKey: string, session: SellerMockState): number {
  const unansweredReviews = session.reviews.filter((r) => r.shopId === shopKey && !r.reply).length;
  const unansweredQuestions = session.questions.filter(
    (q) => q.shopId === shopKey && !q.answer
  ).length;
  return unansweredReviews + unansweredQuestions;
}

/* ---------- Wave 2 Mock-Only Factory Profile, Size Charts & Operations (/seller/ho-so) ---------- */

export function getShopFactoryProfileWithSession(
  shopKey: string,
  session: SellerMockState
): SellerFactoryProfile {
  return session.factoryProfiles[shopKey] ?? INITIAL_FACTORY_PROFILES["shop-bach-dang"];
}

export function updateShopFactoryProfile(shopKey: string, patch: Partial<SellerFactoryProfile>) {
  const prev = getShopFactoryProfileWithSession(shopKey, sessionState);
  sessionState = {
    ...sessionState,
    factoryProfiles: {
      ...sessionState.factoryProfiles,
      [shopKey]: { ...prev, ...patch },
    },
  };
  emitChange();
}

export function addShopCollectionItem(shopKey: string, collection: SellerShopCollection) {
  const prev = getShopFactoryProfileWithSession(shopKey, sessionState);
  sessionState = {
    ...sessionState,
    factoryProfiles: {
      ...sessionState.factoryProfiles,
      [shopKey]: {
        ...prev,
        collections: [collection, ...prev.collections],
      },
    },
  };
  emitChange();
}

export function toggleShopMenuLinkVisibility(shopKey: string, menuId: string, visible: boolean) {
  const prev = getShopFactoryProfileWithSession(shopKey, sessionState);
  sessionState = {
    ...sessionState,
    factoryProfiles: {
      ...sessionState.factoryProfiles,
      [shopKey]: {
        ...prev,
        shopMenu: prev.shopMenu.map((m) => (m.id === menuId ? { ...m, visible } : m)),
      },
    },
  };
  emitChange();
}

export function getShopSizeChartsWithSession(
  shopKey: string,
  session: SellerMockState
): SellerShopSizeCharts {
  return session.sizeCharts[shopKey] ?? INITIAL_SHOP_SIZE_CHARTS["shop-bach-dang"];
}

export function updateShopSizeCharts(shopKey: string, nextCharts: SellerShopSizeCharts) {
  sessionState = {
    ...sessionState,
    sizeCharts: {
      ...sessionState.sizeCharts,
      [shopKey]: nextCharts,
    },
  };
  emitChange();
}

export function getShopOperationsConfigWithSession(
  shopKey: string,
  session: SellerMockState
): SellerOperationsConfig {
  return session.opsConfigs[shopKey] ?? INITIAL_OPERATIONS_CONFIG["shop-bach-dang"];
}

export function updateShopOperationsConfig(
  shopKey: string,
  patch: Partial<SellerOperationsConfig>
) {
  const prev = getShopOperationsConfigWithSession(shopKey, sessionState);
  sessionState = {
    ...sessionState,
    opsConfigs: {
      ...sessionState.opsConfigs,
      [shopKey]: { ...prev, ...patch },
    },
  };
  emitChange();
}
