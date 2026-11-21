"use client";

/**
 * In-memory mock state for Seller Center tabs/screens that do NOT have backend RPCs yet:
 * - /seller/tuong-tac (reviews, Q&A, FAQs)
 * - /seller/ho-so (factory QC profile, custom size charts, local ops config)
 * - /seller/chat (1-1 mock chat threads)
 *
 * Real backend entities (products, SKU stock, orders, shop vouchers, flash sale registrations,
 * shop profile, site config, seller balances) are NEVER stored here and NEVER stored in localStorage.
 */
import { useSyncExternalStore } from "react";
import {
  INITIAL_FACTORY_PROFILES,
  INITIAL_OPERATIONS_CONFIG,
  INITIAL_SELLER_FAQS,
  INITIAL_SELLER_QUESTIONS,
  INITIAL_SELLER_REVIEWS,
  INITIAL_SHOP_SIZE_CHARTS,
} from "@/data/seller";
import type {
  SellerFactoryProfile,
  SellerOperationsConfig,
  SellerProductQuestion,
  SellerSampleReview,
  SellerShopFaqItem,
  SellerShopSizeCharts,
} from "@/data/seller";

export interface ChatMessage {
  id: string;
  sender: "buyer" | "seller";
  senderName: string;
  text: string;
  timestamp: string;
}

export interface ChatThread {
  id: string;
  shopId: string;
  shopName: string;
  buyerName: string;
  productRef?: {
    slug: string;
    name: string;
    price: number;
    image: string;
  };
  unreadByBuyer: number;
  unreadBySeller: number;
  messages: ChatMessage[];
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
  reviews: SellerSampleReview[];
  questions: SellerProductQuestion[];
  faqs: SellerShopFaqItem[];
  factoryProfiles: Record<string, SellerFactoryProfile>;
  sizeCharts: Record<string, SellerShopSizeCharts>;
  opsConfigs: Record<string, SellerOperationsConfig>;
}

function buildInitialMockChatThreads(): ChatThread[] {
  return [
    {
      id: "thr-bach-dang",
      shopId: "shop-bach-dang",
      shopName: "Xưởng Dệt Bạch Đằng",
      buyerName: "Nguyễn Minh Quân",
      productRef: {
        slug: "ao-thun-cotton-compact-trang",
        name: "Áo thun Cotton Compact 220gsm Trắng",
        price: 179000,
        image: "/products/ao-thun-cotton-trang.png",
      },
      unreadByBuyer: 1,
      unreadBySeller: 0,
      messages: [
        {
          id: "msg-1",
          sender: "buyer",
          senderName: "Nguyễn Minh Quân",
          text: "Chào shop, mình cao 1m74 nặng 71 kg thì mặc áo thun Cotton 220gsm size L có bị rộng vai không?",
          timestamp: "2026-11-20T01:12:00+07:00",
        },
        {
          id: "msg-2",
          sender: "seller",
          senderName: "Xưởng Dệt Bạch Đằng",
          text: "Chào anh Quân, thông số 1m74 và 71 kg mặc size L vừa gọn vai, chiều dài áo 71 cm. Nếu thích mặc ôm sát hơn thì mới cần xuống size M ạ.",
          timestamp: "2026-11-20T01:18:00+07:00",
        },
      ],
    },
    {
      id: "thr-da-nang",
      shopId: "shop-da-nang",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      buyerName: "Nguyễn Minh Quân",
      productRef: {
        slug: "ao-polo-pique-cotton-kem-nhat",
        name: "Áo Polo Pique Cotton Kem nhạt",
        price: 289000,
        image: "/products/ao-polo-pique-kem.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 1,
      messages: [
        {
          id: "msg-3",
          sender: "buyer",
          senderName: "Nguyễn Minh Quân",
          text: "Đơn NM-261119-098 của mình dự kiến khi nào bàn giao đơn vị vận chuyển vậy shop?",
          timestamp: "2026-11-19T19:40:00+07:00",
        },
      ],
    },
    {
      id: "thr-sai-gon",
      shopId: "shop-sai-gon",
      shopName: "Kho Basic Sài Gòn",
      buyerName: "Trần Hải Đăng",
      productRef: {
        slug: "ao-thun-the-thao-dryfit-xanh-co-vit",
        name: "Áo thun Thể thao Dry-Fit Xanh cổ vịt",
        price: 159000,
        image: "/products/ao-thun-dryfit-co-vit.png",
      },
      unreadByBuyer: 0,
      unreadBySeller: 1,
      messages: [
        {
          id: "msg-4",
          sender: "buyer",
          senderName: "Trần Hải Đăng",
          text: "Áo Dry-Fit xanh cổ vịt giao về Phường Đống Đa, Hà Nội mất mấy ngày ạ?",
          timestamp: "2026-11-19T21:05:00+07:00",
        },
        {
          id: "msg-5",
          sender: "seller",
          senderName: "Kho Basic Sài Gòn",
          text: "Đơn đi Thành phố Hà Nội giao tiêu chuẩn trong 1-2 ngày, phí vận chuyển 22.000₫ hoặc miễn phí khi đạt từ 199.000₫ tại cửa hàng ạ.",
          timestamp: "2026-11-19T21:14:00+07:00",
        },
      ],
    },
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
  reviews: INITIAL_SELLER_REVIEWS,
  questions: INITIAL_SELLER_QUESTIONS,
  faqs: INITIAL_SELLER_FAQS,
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

/* ---------- Interaction Actions (/seller/tuong-tac) ---------- */

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

export function getPendingInteractionCount(shopKey: string, session: SellerMockState): number {
  const unansweredReviews = session.reviews.filter((r) => r.shopId === shopKey && !r.reply).length;
  const unansweredQuestions = session.questions.filter(
    (q) => q.shopId === shopKey && !q.answer
  ).length;
  return unansweredReviews + unansweredQuestions;
}

/* ---------- Factory Profile, Size Charts & Operations (/seller/ho-so) ---------- */

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
