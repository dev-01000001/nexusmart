"use client";

// Dữ liệu mẫu cho các tính năng chưa có backend.

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ADMIN_DISPUTES,
  COMPLAINT_SLA,
  EDU_DISCOUNT_CONFIG,
  GIFT_CARD_POLICY,
  INITIAL_CHAT_THREADS,
  INITIAL_MEMBERSHIP_STATE,
  INITIAL_PLATFORM_CONFIG,
  SAMPLE_QAS,
  SAMPLE_REVIEWS,
  getTierBySpend,
} from "@/data";
import type {
  AdminDispute,
  ChatThread,
  FitFeedback,
  GiftCardItem,
  MembershipProfileState,
  MembershipTier,
  PlatformConfigState,
  ProductQA,
  ProductReview,
  SizeCode,
} from "@/data";
import { formatVnd } from "@/lib/format";
import { DEFAULT_BODY_PROFILE, type BodyProfile } from "@/lib/sizing";
import { useSession } from "@/context/SessionContext";
import { clearCheckoutWarnings, shouldClearUserClientState } from "@/features/checkout/helpers";

const STORAGE_KEY = "nexusmart_store_v2";

export interface WishlistProductSnapshot {
  name: string;
  slug: string;
  price: number;
  originalPrice?: number;
  image: string;
  shopName?: string;
}

export interface WishlistEntry {
  productId: string;
  skuCode?: string;
  addedAt: string;
  snapshot?: WishlistProductSnapshot;
}

export interface BackInStockAlert {
  id: string;
  productId: string;
  skuCode: string;
  colorName: string;
  size: string;
  contact: string;
  createdAt: string;
}

export interface CookiePreferences {
  essential: true;
  analytics: boolean;
  personalization: boolean;
  updatedAt?: string;
}

export interface SupportTicket {
  id: string;
  kind: "khieu_nai" | "goi_lai" | "bu_le_tat";
  orderCode?: string;
  fullName: string;
  phone: string;
  email?: string;
  subject: string;
  detail: string;
  isVulnerablePriority: boolean;
  slaLabel: string;
  status: "da_tiep_nhan" | "dang_xu_ly";
  createdAt: string;
}

export interface SubmitSupportTicketInput {
  kind: SupportTicket["kind"];
  orderCode?: string;
  fullName: string;
  phone: string;
  email?: string;
  subject: string;
  detail: string;
  isVulnerablePriority?: boolean;
}

export interface SubmitReviewInput {
  productId: string;
  authorName?: string;
  rating: number;
  fitFeedback: FitFeedback;
  buyerHeightCm: number;
  buyerWeightKg: number;
  purchasedSize: SizeCode;
  colorName: string;
  comment: string;
  images?: string[];
}

export interface SubmitQAInput {
  productId: string;
  askerName?: string;
  question: string;
}

export interface MockStoreContextValue {
  // Body profile, wishlist, recently viewed, back-in-stock
  bodyProfile: BodyProfile;
  updateBodyProfile: (next: BodyProfile) => void;
  wishlist: WishlistEntry[];
  toggleWishlist: (
    productId: string,
    skuCode?: string,
    snapshot?: WishlistProductSnapshot
  ) => boolean;
  isWishlisted: (productId: string, skuCode?: string) => boolean;
  recentlyViewed: string[];
  recordProductView: (productId: string) => void;
  clearRecentlyViewed: () => void;
  backInStockAlerts: BackInStockAlert[];
  subscribeBackInStock: (input: Omit<BackInStockAlert, "id" | "createdAt">) => BackInStockAlert;
  hasBackInStockAlert: (skuCode: string) => boolean;

  // Reviews, Q&As & Support tickets
  reviews: ProductReview[];
  submitReview: (input: SubmitReviewInput) => ProductReview;
  productQAs: ProductQA[];
  submitProductQA: (input: SubmitQAInput) => ProductQA;
  supportTickets: SupportTicket[];
  submitSupportTicket: (input: SubmitSupportTicketInput) => SupportTicket;

  // Cookie preferences & modal
  cookiePrefs: CookiePreferences;
  updateCookiePrefs: (next: Partial<CookiePreferences>) => void;
  cookieModalOpen: boolean;
  openCookieModal: () => void;
  closeCookieModal: () => void;

  // Membership (Hội viên Nexus), Referrals, Birthday, Gift Cards & .edu
  membership: MembershipProfileState;
  membershipTier: MembershipTier;
  updateBirthday: (birthday: string) => void;
  claimBirthdayGift: () => void;
  inviteFriendMock: (friendName: string) => void;
  purchaseGiftCard: (input: {
    amountVnd: number;
    customCode?: string;
    recipientEmail?: string;
    message?: string;
  }) => GiftCardItem;
  verifyEduEmail: (email: string) => { ok: boolean; message: string };

  // Buyer-seller chat & AI assistant overlays
  chatOpen: boolean;
  chatShopId: string | null;
  openChat: (shopId?: string) => void;
  closeChat: () => void;
  aiOpen: boolean;
  openAi: () => void;
  closeAi: () => void;
  chatThreads: ChatThread[];
  sendChatMessage: (threadId: string, sender: "buyer" | "seller", text: string) => void;

  // Admin mock-only queues (no backend RPC yet)
  disputes: AdminDispute[];
  updateDisputeStatus: (id: string, status: AdminDispute["status"], note?: string) => void;
  platformConfig: PlatformConfigState;
  updatePlatformConfig: (next: PlatformConfigState) => void;

  // Lightweight toast notification
  toastMessage: string | null;
  showToast: (message: string) => void;
  dismissToast: () => void;
}

const INITIAL_COOKIE_PREFS: CookiePreferences = {
  essential: true,
  analytics: true,
  personalization: true,
};

const INITIAL_WISHLIST: string[] = [];
const INITIAL_WISHLIST_ENTRIES: WishlistEntry[] = INITIAL_WISHLIST.map((productId) => ({
  productId,
  addedAt: "",
}));

const INITIAL_RECENTLY_VIEWED: string[] = [];

const INITIAL_SUPPORT_TICKETS: SupportTicket[] = [];

const MockStoreContext = createContext<MockStoreContextValue | null>(null);

export function MockStoreProvider({ children }: { children: React.ReactNode }) {
  const [hydrated, setHydrated] = useState(false);
  const { user: sessionUser, loading: sessionLoading } = useSession();
  const prevUserIdRef = useRef<string | null>(null);

  const [bodyProfile, setBodyProfile] = useState<BodyProfile>(DEFAULT_BODY_PROFILE);
  const [wishlist, setWishlist] = useState<WishlistEntry[]>(INITIAL_WISHLIST_ENTRIES);
  const [recentlyViewed, setRecentlyViewed] = useState<string[]>(INITIAL_RECENTLY_VIEWED);
  const [backInStockAlerts, setBackInStockAlerts] = useState<BackInStockAlert[]>([]);
  const [reviews, setReviews] = useState<ProductReview[]>(SAMPLE_REVIEWS);
  const [productQAs, setProductQAs] = useState<ProductQA[]>(SAMPLE_QAS);
  const [supportTickets, setSupportTickets] = useState<SupportTicket[]>(INITIAL_SUPPORT_TICKETS);

  const [cookiePrefs, setCookiePrefs] = useState<CookiePreferences>(INITIAL_COOKIE_PREFS);
  const [cookieModalOpen, setCookieModalOpen] = useState(false);

  const [membership, setMembership] = useState<MembershipProfileState>(INITIAL_MEMBERSHIP_STATE);

  const [chatOpen, setChatOpen] = useState(false);
  const [chatShopId, setChatShopId] = useState<string | null>("shop-bach-dang");
  const [aiOpen, setAiOpen] = useState(false);
  const [chatThreads, setChatThreads] = useState<ChatThread[]>(INITIAL_CHAT_THREADS);

  const [disputes, setDisputes] = useState<AdminDispute[]>(ADMIN_DISPUTES);
  const [platformConfig, setPlatformConfig] =
    useState<PlatformConfigState>(INITIAL_PLATFORM_CONFIG);

  const [toastMessage, setToastMessage] = useState<string | null>(null);

  useEffect(() => {
    try {
      // Lý do dùng localStorage: chỉ nạp tiện ích theo người xem (số đo, yêu thích, đã xem, báo có hàng, cookie, hội viên mẫu); không lưu token/giỏ/đơn.
      const raw = window.localStorage.getItem(STORAGE_KEY);
      if (raw) {
        const parsed = JSON.parse(raw) as Partial<{
          bodyProfile: BodyProfile;
          wishlist: WishlistEntry[];
          recentlyViewed: string[];
          backInStockAlerts: BackInStockAlert[];
          cookiePrefs: CookiePreferences;
          membership: MembershipProfileState;
        }>;
        if (parsed.bodyProfile && typeof parsed.bodyProfile.heightCm === "number") {
          setBodyProfile(parsed.bodyProfile);
        }
        if (Array.isArray(parsed.wishlist)) {
          setWishlist(parsed.wishlist);
        }
        if (Array.isArray(parsed.recentlyViewed)) {
          setRecentlyViewed(parsed.recentlyViewed);
        }
        if (Array.isArray(parsed.backInStockAlerts)) {
          setBackInStockAlerts(parsed.backInStockAlerts);
        }
        if (parsed.cookiePrefs && typeof parsed.cookiePrefs.analytics === "boolean") {
          setCookiePrefs({
            essential: true,
            analytics: parsed.cookiePrefs.analytics,
            personalization: parsed.cookiePrefs.personalization,
            updatedAt: parsed.cookiePrefs.updatedAt,
          });
        }
        if (parsed.membership && typeof parsed.membership.spend4Q === "number") {
          setMembership((prev) => ({
            ...prev,
            ...parsed.membership,
          }));
        }
      }
    } catch {
      // Ignore storage read errors in restricted environments
    }
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (sessionLoading) return;
    const nextUserId = sessionUser?.id ?? null;
    const mustClear = shouldClearUserClientState(prevUserIdRef.current, nextUserId);
    prevUserIdRef.current = nextUserId;
    if (!mustClear) return;

    clearCheckoutWarnings();
    try {
      window.localStorage.removeItem(STORAGE_KEY);
    } catch {
      // Ignore storage restrictions
    }
    setBodyProfile(DEFAULT_BODY_PROFILE);
    setWishlist(INITIAL_WISHLIST_ENTRIES);
    setRecentlyViewed(INITIAL_RECENTLY_VIEWED);
    setBackInStockAlerts([]);
    setMembership(INITIAL_MEMBERSHIP_STATE);
    setChatThreads(INITIAL_CHAT_THREADS);
  }, [sessionLoading, sessionUser?.id]);

  useEffect(() => {
    if (!hydrated) return;
    try {
      // Lý do dùng localStorage: chỉ lưu tiện ích theo người xem (số đo, yêu thích, đã xem, báo có hàng, cookie, hội viên mẫu); không lưu token/giỏ/đơn.
      window.localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({
          bodyProfile,
          wishlist,
          recentlyViewed,
          backInStockAlerts,
          cookiePrefs,
          membership,
        })
      );
    } catch {
      // Ignore quota / privacy mode errors
    }
  }, [hydrated, bodyProfile, wishlist, recentlyViewed, backInStockAlerts, cookiePrefs, membership]);

  const showToast = useCallback((message: string) => {
    setToastMessage(message);
  }, []);

  const dismissToast = useCallback(() => {
    setToastMessage(null);
  }, []);

  const membershipTier = useMemo(() => getTierBySpend(membership.spend4Q), [membership.spend4Q]);

  // Body profile, wishlist, recently viewed, back-in-stock
  const updateBodyProfile = useCallback(
    (next: BodyProfile) => {
      setBodyProfile(next);
      showToast(`Đã lưu hồ sơ số đo (${next.heightCm} cm · ${next.weightKg} kg) cho toàn sàn.`);
    },
    [showToast]
  );

  const toggleWishlist = useCallback(
    (productId: string, skuCode?: string, snapshot?: WishlistProductSnapshot): boolean => {
      let added = false;
      setWishlist((prev) => {
        const exists = prev.some(
          (w) => w.productId === productId && (skuCode ? w.skuCode === skuCode : true)
        );
        if (exists) {
          added = false;
          return prev.filter(
            (w) => !(w.productId === productId && (skuCode ? w.skuCode === skuCode : true))
          );
        }
        added = true;
        return [
          {
            productId,
            skuCode,
            addedAt: new Date().toISOString(),
            ...(snapshot ? { snapshot } : {}),
          },
          ...prev,
        ];
      });
      showToast(
        added ? "Đã lưu sản phẩm vào danh sách yêu thích." : "Đã bỏ khỏi danh sách yêu thích."
      );
      return added;
    },
    [showToast]
  );

  const isWishlisted = useCallback(
    (productId: string, skuCode?: string): boolean => {
      return wishlist.some(
        (w) => w.productId === productId && (skuCode ? w.skuCode === skuCode : true)
      );
    },
    [wishlist]
  );

  const recordProductView = useCallback((productId: string) => {
    setRecentlyViewed((prev) => {
      if (prev[0] === productId) return prev;
      return [productId, ...prev.filter((id) => id !== productId)].slice(0, 12);
    });
  }, []);

  const clearRecentlyViewed = useCallback(() => {
    setRecentlyViewed([]);
  }, []);

  const subscribeBackInStock = useCallback(
    (input: Omit<BackInStockAlert, "id" | "createdAt">): BackInStockAlert => {
      const created: BackInStockAlert = {
        ...input,
        id: `bis-${Date.now()}`,
        createdAt: new Date().toISOString(),
      };
      setBackInStockAlerts((prev) => [created, ...prev.filter((a) => a.skuCode !== input.skuCode)]);
      showToast(`Đã đăng ký báo khi có hàng cho mã ${input.skuCode}.`);
      return created;
    },
    [showToast]
  );

  const hasBackInStockAlert = useCallback(
    (skuCode: string): boolean => backInStockAlerts.some((a) => a.skuCode === skuCode),
    [backInStockAlerts]
  );

  // Reviews, Q&A & Support tickets
  const submitReview = useCallback(
    (input: SubmitReviewInput): ProductReview => {
      const created: ProductReview = {
        id: `rev-${Date.now()}`,
        productId: input.productId,
        authorName: input.authorName?.trim() || sessionUser?.full_name || "Khách hàng",
        rating: Math.max(1, Math.min(5, Math.round(input.rating))),
        fitFeedback: input.fitFeedback,
        buyerHeightCm: input.buyerHeightCm,
        buyerWeightKg: input.buyerWeightKg,
        purchasedSize: input.purchasedSize,
        colorName: input.colorName,
        comment: input.comment.trim(),
        createdAt: new Date().toISOString(),
        images: input.images ?? [],
        verifiedPurchase: true,
        isSampleData: false,
      };
      setReviews((prev) => [created, ...prev]);
      showToast("Đã gửi đánh giá sản phẩm thành công.");
      return created;
    },
    [showToast, sessionUser?.full_name]
  );

  const submitProductQA = useCallback(
    (input: SubmitQAInput): ProductQA => {
      const created: ProductQA = {
        id: `qa-${Date.now()}`,
        productId: input.productId,
        askerName: input.askerName?.trim() || sessionUser?.full_name || "Khách hàng",
        question: input.question.trim(),
        askedAt: new Date().toISOString(),
        isSampleData: false,
      };
      setProductQAs((prev) => [created, ...prev]);
      showToast("Đã gửi câu hỏi công khai tới gian hàng.");
      return created;
    },
    [showToast, sessionUser?.full_name]
  );

  const submitSupportTicket = useCallback(
    (input: SubmitSupportTicketInput): SupportTicket => {
      const slaLabel =
        input.kind === "goi_lai"
          ? `Gọi lại trong vòng ${COMPLAINT_SLA.callbackMinutes} phút (${COMPLAINT_SLA.hotlineHours})`
          : input.isVulnerablePriority
            ? "Ưu tiên nhóm yếu thế · Phản hồi trong 4 giờ"
            : `Xác nhận trong ${COMPLAINT_SLA.ackHours}h · Xử lý ${COMPLAINT_SLA.resolveDaysLabel}`;
      const created: SupportTicket = {
        id: `tk-${Date.now()}`,
        kind: input.kind,
        orderCode: input.orderCode?.trim() || undefined,
        fullName: input.fullName.trim(),
        phone: input.phone.trim(),
        email: input.email?.trim() || undefined,
        subject: input.subject.trim(),
        detail: input.detail.trim(),
        isVulnerablePriority: Boolean(input.isVulnerablePriority),
        slaLabel,
        status: "da_tiep_nhan",
        createdAt: new Date().toISOString(),
      };
      setSupportTickets((prev) => [created, ...prev]);
      showToast(
        input.kind === "goi_lai"
          ? "Đã nhận yêu cầu gọi lại. Tổng đài sẽ liên hệ trong 15 phút."
          : "Đã tiếp nhận phiếu hỗ trợ. Xác nhận phản hồi trong 24 giờ."
      );
      return created;
    },
    [showToast]
  );

  // Cookie prefs
  const updateCookiePrefs = useCallback(
    (next: Partial<CookiePreferences>) => {
      setCookiePrefs((prev) => ({
        ...prev,
        ...next,
        essential: true,
        updatedAt: new Date().toISOString(),
      }));
      showToast("Đã lưu tùy chọn cookie của bạn.");
    },
    [showToast]
  );

  const openCookieModal = useCallback(() => setCookieModalOpen(true), []);
  const closeCookieModal = useCallback(() => setCookieModalOpen(false), []);

  // Membership actions
  const updateBirthday = useCallback(
    (birthday: string) => {
      setMembership((prev) => ({ ...prev, birthday }));
      showToast("Đã lưu ngày sinh để nhận quà sinh nhật Hội viên Nexus.");
    },
    [showToast]
  );

  const claimBirthdayGift = useCallback(() => {
    setMembership((prev) => ({ ...prev, birthdayGiftClaimed: true }));
    showToast("Đã kích hoạt quà sinh nhật và mã SINHNHAT50 vào giỏ hàng.");
  }, [showToast]);

  const inviteFriendMock = useCallback(
    (friendName: string) => {
      const clean = friendName.trim();
      if (!clean) return;
      setMembership((prev) => ({
        ...prev,
        referrals: [
          {
            id: `ref-${Date.now()}`,
            friendName: clean,
            invitedAt: new Date().toISOString().slice(0, 10),
            rewardCoins: 0,
            status: "cho_dat_don",
          },
          ...prev.referrals,
        ],
      }));
      showToast(`Đã tạo lời mời giới thiệu cho ${clean}.`);
    },
    [showToast]
  );

  const purchaseGiftCard = useCallback(
    (input: {
      amountVnd: number;
      customCode?: string;
      recipientEmail?: string;
      message?: string;
    }): GiftCardItem => {
      const amount = Math.max(100000, Math.trunc(input.amountVnd));
      const cleanCustom = (input.customCode ?? "")
        .toUpperCase()
        .replace(/[^A-Z0-9-]/g, "")
        .slice(0, GIFT_CARD_POLICY.customCodeMaxLen);
      const code =
        cleanCustom.length >= GIFT_CARD_POLICY.customCodeMinLen
          ? `NEXUS-${cleanCustom}`
          : `NEXUS-GC${Math.floor(1000 + Math.random() * 8999)}`;
      const issued = new Date();
      const expires = new Date(
        issued.getTime() + GIFT_CARD_POLICY.validityDays * 24 * 60 * 60 * 1000
      );
      const card: GiftCardItem = {
        id: `gc-${Date.now()}`,
        code,
        amountVnd: amount,
        remainingVnd: amount,
        recipientEmail: input.recipientEmail?.trim() || undefined,
        message: input.message?.trim() || undefined,
        issuedAt: issued.toISOString(),
        expiresAt: expires.toISOString(),
        status: "kha_dung",
      };
      setMembership((prev) => ({
        ...prev,
        giftCards: [card, ...prev.giftCards],
      }));
      showToast(`Đã phát hành thẻ quà tặng ${code} trị giá ${formatVnd(amount)}.`);
      return card;
    },
    [showToast]
  );

  const verifyEduEmail = useCallback(
    (email: string): { ok: boolean; message: string } => {
      const clean = email.trim().toLowerCase();
      const validDomain = EDU_DISCOUNT_CONFIG.allowedDomains.some((suffix) =>
        clean.endsWith(suffix)
      );
      if (!clean.includes("@") || !validDomain) {
        return {
          ok: false,
          message: "Vui lòng nhập email sinh viên có đuôi .edu hoặc .edu.vn hợp lệ.",
        };
      }
      setMembership((prev) => ({
        ...prev,
        eduEmail: clean,
        eduVerified: true,
      }));
      showToast("Xác thực email sinh viên thành công. Đã mở khóa mã EDU15 (giảm 15%).");
      return { ok: true, message: "Xác thực thành công và đã thêm mã EDU15 vào ví voucher." };
    },
    [showToast]
  );

  const openChat = useCallback((shopId?: string) => {
    setAiOpen(false);
    if (shopId) setChatShopId(shopId);
    setChatOpen(true);
    setChatThreads((prev) =>
      prev.map((t) => (shopId && t.shopId === shopId ? { ...t, unreadByBuyer: 0 } : t))
    );
  }, []);

  const closeChat = useCallback(() => setChatOpen(false), []);

  const openAi = useCallback(() => {
    setChatOpen(false);
    setAiOpen(true);
  }, []);

  const closeAi = useCallback(() => setAiOpen(false), []);

  const sendChatMessage = useCallback(
    (threadId: string, sender: "buyer" | "seller", text: string) => {
      const clean = text.trim();
      if (!clean) return;
      const nowIso = new Date().toISOString();
      setChatThreads((prev) =>
        prev.map((thread) => {
          if (thread.id !== threadId) return thread;
          const senderName = sender === "buyer" ? sessionUser?.full_name || "Bạn" : thread.shopName;
          return {
            ...thread,
            unreadByBuyer: sender === "seller" ? thread.unreadByBuyer + 1 : 0,
            unreadBySeller: sender === "buyer" ? thread.unreadBySeller + 1 : 0,
            messages: [
              ...thread.messages,
              {
                id: `msg-${Date.now()}`,
                sender,
                senderName,
                text: clean,
                timestamp: nowIso,
              },
            ],
          };
        })
      );
    },
    [sessionUser?.full_name]
  );

  const updateDisputeStatus = useCallback(
    (id: string, status: AdminDispute["status"], note?: string) => {
      setDisputes((prev) =>
        prev.map((d) => (d.id === id ? { ...d, status, evidenceNote: note ?? d.evidenceNote } : d))
      );
      showToast("Đã cập nhật tiến độ xử lý khiếu nại.");
    },
    [showToast]
  );

  const updatePlatformConfig = useCallback(
    (next: PlatformConfigState) => {
      setPlatformConfig(next);
      showToast("Đã lưu cấu hình vận hành sàn.");
    },
    [showToast]
  );

  const value: MockStoreContextValue = {
    bodyProfile,
    updateBodyProfile,
    wishlist,
    toggleWishlist,
    isWishlisted,
    recentlyViewed,
    recordProductView,
    clearRecentlyViewed,
    backInStockAlerts,
    subscribeBackInStock,
    hasBackInStockAlert,
    reviews,
    submitReview,
    productQAs,
    submitProductQA,
    supportTickets,
    submitSupportTicket,
    cookiePrefs,
    updateCookiePrefs,
    cookieModalOpen,
    openCookieModal,
    closeCookieModal,
    membership,
    membershipTier,
    updateBirthday,
    claimBirthdayGift,
    inviteFriendMock,
    purchaseGiftCard,
    verifyEduEmail,
    chatOpen,
    chatShopId,
    openChat,
    closeChat,
    aiOpen,
    openAi,
    closeAi,
    chatThreads,
    sendChatMessage,
    disputes,
    updateDisputeStatus,
    platformConfig,
    updatePlatformConfig,
    toastMessage,
    showToast,
    dismissToast,
  };

  return <MockStoreContext.Provider value={value}>{children}</MockStoreContext.Provider>;
}

export function useMockStore(): MockStoreContextValue {
  const ctx = useContext(MockStoreContext);
  if (!ctx) {
    throw new Error("useMockStore phải được gọi bên trong MockStoreProvider.");
  }
  return ctx;
}
