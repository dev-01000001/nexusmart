"use client";

/**
 * Dữ liệu MẪU cho các tính năng chưa có backend (hội viên, thẻ quà tặng, .edu,
 * đánh giá, hỏi đáp, phiếu hỗ trợ, chat, hàng đợi admin mẫu).
 *
 * Tách riêng khỏi StoreContext (giỏ thật từ backend) để không trộn dữ liệu thật
 * với dữ liệu mẫu trong cùng một store. Trạng thái chỉ sống trong bộ nhớ trình
 * duyệt, tải lại trang là về mặc định; không ghi localStorage.
 */
import React, { createContext, useCallback, useContext, useMemo, useState } from "react";
import {
  ADMIN_DISPUTES,
  ADMIN_RECONCILIATION,
  ADMIN_SHOP_APPROVALS,
  COMPLAINT_SLA,
  EDU_DISCOUNT_CONFIG,
  GIFT_CARD_POLICY,
  INITIAL_CHAT_THREADS,
  INITIAL_MEMBERSHIP_STATE,
  INITIAL_PLATFORM_CONFIG,
  SAMPLE_QAS,
  SAMPLE_REVIEWS,
  SHOPS,
  getTierBySpend,
} from "@/data";
import type {
  AdminDispute,
  ChatThread,
  FitFeedback,
  GiftCardItem,
  MembershipProfileState,
  MembershipTier,
  PaymentReconciliationRow,
  PlatformConfigState,
  ProductQA,
  ProductReview,
  ShopApprovalApplication,
  SizeCode,
} from "@/data";

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

export interface BackInStockAlert {
  id: string;
  productId: string;
  skuCode: string;
  colorName: string;
  size: string;
  contact: string;
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

export interface MockStoreValue {
  // Hội viên Nexus, giới thiệu, sinh nhật, thẻ quà tặng & .edu (mẫu)
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
  applyGiftCardCode: (code: string | null) => { ok: boolean; message: string };
  verifyEduEmail: (email: string) => { ok: boolean; message: string };

  // Đánh giá, hỏi đáp, báo có hàng & phiếu hỗ trợ (mẫu)
  reviews: ProductReview[];
  submitReview: (input: SubmitReviewInput) => ProductReview;
  productQAs: ProductQA[];
  submitProductQA: (input: SubmitQAInput) => ProductQA;
  supportTickets: SupportTicket[];
  submitSupportTicket: (input: SubmitSupportTicketInput) => SupportTicket;
  backInStockAlerts: BackInStockAlert[];
  subscribeBackInStock: (input: Omit<BackInStockAlert, "id" | "createdAt">) => BackInStockAlert;
  hasBackInStockAlert: (skuCode: string) => boolean;

  // Chat người mua - gian hàng (mẫu)
  chatThreads: ChatThread[];
  sendChatMessage: (
    threadId: string,
    sender: "buyer" | "seller",
    text: string,
    buyerName?: string
  ) => void;
  markChatReadByBuyer: (shopId: string) => void;

  // Seller/admin mẫu (màn chưa có backend)
  activeSellerShopId: string;
  setActiveSellerShopId: (shopId: string) => void;
  shopApprovals: ShopApprovalApplication[];
  updateShopApprovalStatus: (
    id: string,
    status: ShopApprovalApplication["status"],
    note?: string
  ) => void;
  disputes: AdminDispute[];
  updateDisputeStatus: (id: string, status: AdminDispute["status"], note?: string) => void;
  reconciliationRows: PaymentReconciliationRow[];
  resolveReconciliationBatch: (id: string, note?: string) => void;
  platformConfig: PlatformConfigState;
  updatePlatformConfig: (next: PlatformConfigState) => void;
}

const INITIAL_SUPPORT_TICKETS: SupportTicket[] = [
  {
    id: "tk-261119-01",
    kind: "bu_le_tat",
    orderCode: "NM-261117-082",
    fullName: "Khách hàng NexusMart",
    phone: "0908 000 000",
    subject: "Đề nghị bù lẻ 1 chiếc tất màu xám sáng",
    detail:
      "Bị thất lạc 1 chiếc tất sau lần giặt đầu, đề nghị gửi kèm đơn kế tiếp từ Dệt Kim Hà Nội.",
    isVulnerablePriority: false,
    slaLabel: "Xác nhận trong 24h · Xử lý 5-7 ngày làm việc",
    status: "dang_xu_ly",
    createdAt: "2026-11-19T10:30:00+07:00",
  },
];

const DEFAULT_PERSON_NAME = "Khách hàng NexusMart";

const MockStoreContext = createContext<MockStoreValue | null>(null);

export function MockStoreProvider({
  children,
  notify,
}: {
  children: React.ReactNode;
  notify: (message: string) => void;
}) {
  const [membership, setMembership] = useState<MembershipProfileState>(INITIAL_MEMBERSHIP_STATE);
  const [reviews, setReviews] = useState<ProductReview[]>(SAMPLE_REVIEWS);
  const [productQAs, setProductQAs] = useState<ProductQA[]>(SAMPLE_QAS);
  const [supportTickets, setSupportTickets] = useState<SupportTicket[]>(INITIAL_SUPPORT_TICKETS);
  const [backInStockAlerts, setBackInStockAlerts] = useState<BackInStockAlert[]>([]);
  const [chatThreads, setChatThreads] = useState<ChatThread[]>(INITIAL_CHAT_THREADS);
  const [activeSellerShopId, setActiveSellerShopId] = useState<string>(SHOPS[0].id);
  const [shopApprovals, setShopApprovals] =
    useState<ShopApprovalApplication[]>(ADMIN_SHOP_APPROVALS);
  const [disputes, setDisputes] = useState<AdminDispute[]>(ADMIN_DISPUTES);
  const [reconciliationRows, setReconciliationRows] =
    useState<PaymentReconciliationRow[]>(ADMIN_RECONCILIATION);
  const [platformConfig, setPlatformConfig] =
    useState<PlatformConfigState>(INITIAL_PLATFORM_CONFIG);

  const membershipTier = useMemo(() => getTierBySpend(membership.spend4Q), [membership.spend4Q]);

  const updateBirthday = useCallback(
    (birthday: string) => {
      setMembership((prev) => ({ ...prev, birthday }));
      notify("Đã lưu ngày sinh để nhận quà sinh nhật Hội viên Nexus.");
    },
    [notify]
  );

  const claimBirthdayGift = useCallback(() => {
    setMembership((prev) => ({ ...prev, birthdayGiftClaimed: true }));
    notify("Đã kích hoạt quà sinh nhật Hội viên Nexus.");
  }, [notify]);

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
      notify(`Đã tạo lời mời giới thiệu cho ${clean}.`);
    },
    [notify]
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
      setMembership((prev) => ({ ...prev, giftCards: [card, ...prev.giftCards] }));
      notify(`Đã phát hành thẻ quà tặng ${code} trị giá ${amount.toLocaleString("vi-VN")}₫.`);
      return card;
    },
    [notify]
  );

  const applyGiftCardCode = useCallback(
    (code: string | null): { ok: boolean; message: string } => {
      if (!code) {
        setMembership((prev) => ({ ...prev, appliedGiftCardCode: null }));
        return { ok: true, message: "Đã gỡ mã thẻ quà tặng." };
      }
      const clean = code.trim().toUpperCase();
      const found = membership.giftCards.find((gc) => gc.code.toUpperCase() === clean);
      if (!found) {
        return { ok: false, message: `Không tìm thấy thẻ quà tặng ${clean}.` };
      }
      if (found.remainingVnd <= 0) {
        return { ok: false, message: `Thẻ quà tặng ${clean} đã sử dụng hết số dư.` };
      }
      setMembership((prev) => ({ ...prev, appliedGiftCardCode: found.code }));
      return { ok: true, message: `Đã ghi nhận thẻ ${found.code}.` };
    },
    [membership.giftCards]
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
      setMembership((prev) => ({ ...prev, eduEmail: clean, eduVerified: true }));
      notify("Xác thực email sinh viên thành công.");
      return { ok: true, message: "Xác thực email sinh viên thành công." };
    },
    [notify]
  );

  const submitReview = useCallback(
    (input: SubmitReviewInput): ProductReview => {
      const created: ProductReview = {
        id: `rev-${Date.now()}`,
        productId: input.productId,
        authorName: input.authorName?.trim() || DEFAULT_PERSON_NAME,
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
      notify("Đã gửi đánh giá sản phẩm thành công.");
      return created;
    },
    [notify]
  );

  const submitProductQA = useCallback(
    (input: SubmitQAInput): ProductQA => {
      const created: ProductQA = {
        id: `qa-${Date.now()}`,
        productId: input.productId,
        askerName: input.askerName?.trim() || DEFAULT_PERSON_NAME,
        question: input.question.trim(),
        askedAt: new Date().toISOString(),
        isSampleData: false,
      };
      setProductQAs((prev) => [created, ...prev]);
      notify("Đã gửi câu hỏi công khai tới gian hàng.");
      return created;
    },
    [notify]
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
      notify(
        input.kind === "goi_lai"
          ? "Đã nhận yêu cầu gọi lại. Tổng đài sẽ liên hệ trong 15 phút."
          : "Đã tiếp nhận phiếu hỗ trợ. Xác nhận phản hồi trong 24 giờ."
      );
      return created;
    },
    [notify]
  );

  const subscribeBackInStock = useCallback(
    (input: Omit<BackInStockAlert, "id" | "createdAt">): BackInStockAlert => {
      const created: BackInStockAlert = {
        ...input,
        id: `bis-${Date.now()}`,
        createdAt: new Date().toISOString(),
      };
      setBackInStockAlerts((prev) => [created, ...prev.filter((a) => a.skuCode !== input.skuCode)]);
      notify(`Đã đăng ký báo khi có hàng cho mã ${input.skuCode}.`);
      return created;
    },
    [notify]
  );

  const hasBackInStockAlert = useCallback(
    (skuCode: string): boolean => backInStockAlerts.some((a) => a.skuCode === skuCode),
    [backInStockAlerts]
  );

  const sendChatMessage = useCallback(
    (threadId: string, sender: "buyer" | "seller", text: string, buyerName?: string) => {
      const clean = text.trim();
      if (!clean) return;
      const nowIso = new Date().toISOString();
      setChatThreads((prev) =>
        prev.map((thread) => {
          if (thread.id !== threadId) return thread;
          const senderName =
            sender === "buyer" ? buyerName?.trim() || DEFAULT_PERSON_NAME : thread.shopName;
          return {
            ...thread,
            unreadByBuyer: sender === "seller" ? thread.unreadByBuyer + 1 : 0,
            unreadBySeller: sender === "buyer" ? thread.unreadBySeller + 1 : 0,
            messages: [
              ...thread.messages,
              { id: `msg-${Date.now()}`, sender, senderName, text: clean, timestamp: nowIso },
            ],
          };
        })
      );
    },
    []
  );

  const markChatReadByBuyer = useCallback((shopId: string) => {
    setChatThreads((prev) =>
      prev.map((t) => (t.shopId === shopId && t.unreadByBuyer > 0 ? { ...t, unreadByBuyer: 0 } : t))
    );
  }, []);

  const updateShopApprovalStatus = useCallback(
    (id: string, status: ShopApprovalApplication["status"], note?: string) => {
      setShopApprovals((prev) =>
        prev.map((app) =>
          app.id === id
            ? {
                ...app,
                status,
                slaHoursLeft: status === "da_duyet" || status === "tu_choi" ? 0 : app.slaHoursLeft,
                note: note ?? app.note,
              }
            : app
        )
      );
      notify("Đã cập nhật trạng thái hồ sơ gian hàng.");
    },
    [notify]
  );

  const updateDisputeStatus = useCallback(
    (id: string, status: AdminDispute["status"], note?: string) => {
      setDisputes((prev) =>
        prev.map((d) => (d.id === id ? { ...d, status, evidenceNote: note ?? d.evidenceNote } : d))
      );
      notify("Đã cập nhật tiến độ xử lý khiếu nại.");
    },
    [notify]
  );

  const resolveReconciliationBatch = useCallback(
    (id: string, note?: string) => {
      setReconciliationRows((prev) =>
        prev.map((row) =>
          row.id === id
            ? {
                ...row,
                discrepancy: 0,
                status: "khop",
                note: note ?? "Đã đối chiếu thủ công và xác nhận khớp số liệu sao kê.",
              }
            : row
        )
      );
      notify("Đã xác nhận khớp lô đối soát thanh toán.");
    },
    [notify]
  );

  const updatePlatformConfig = useCallback(
    (next: PlatformConfigState) => {
      setPlatformConfig(next);
      notify("Đã lưu cấu hình vận hành sàn.");
    },
    [notify]
  );

  const value: MockStoreValue = {
    membership,
    membershipTier,
    updateBirthday,
    claimBirthdayGift,
    inviteFriendMock,
    purchaseGiftCard,
    applyGiftCardCode,
    verifyEduEmail,
    reviews,
    submitReview,
    productQAs,
    submitProductQA,
    supportTickets,
    submitSupportTicket,
    backInStockAlerts,
    subscribeBackInStock,
    hasBackInStockAlert,
    chatThreads,
    sendChatMessage,
    markChatReadByBuyer,
    activeSellerShopId,
    setActiveSellerShopId,
    shopApprovals,
    updateShopApprovalStatus,
    disputes,
    updateDisputeStatus,
    reconciliationRows,
    resolveReconciliationBatch,
    platformConfig,
    updatePlatformConfig,
  };

  return <MockStoreContext.Provider value={value}>{children}</MockStoreContext.Provider>;
}

export function useMockStore(): MockStoreValue {
  const ctx = useContext(MockStoreContext);
  if (!ctx) {
    throw new Error("useMockStore phải được gọi bên trong MockStoreProvider.");
  }
  return ctx;
}
