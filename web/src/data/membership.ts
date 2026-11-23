import type { SizeCode } from "./products";

export type MembershipTierId = "thanh_vien" | "bac" | "vang" | "bach_kim";

export interface MembershipTier {
  id: MembershipTierId;
  name: string;
  minSpend4Q: number; // integer VND over 4 rolling quarters
  cashbackRate: number; // e.g. 0.01 = 1%
  cashbackPercentLabel: string;
  perks: string[];
}

export interface XuLedgerEntry {
  id: string;
  date: string;
  title: string;
  orderCode?: string;
  deltaCoins: number; // positive = earn, negative = spend
  status: "kha_dung" | "cho_duyet" | "da_dung";
}

export interface ReferralRecord {
  id: string;
  friendName: string;
  invitedAt: string;
  orderCode?: string;
  orderValueVnd?: number;
  rewardCoins: number;
  status: "cho_dat_don" | "cho_het_han_45_ngay" | "da_cong_xu";
}

export interface YearInReviewSample {
  year: number;
  totalOrders: number;
  totalSpentVnd: number; // integer VND
  totalSavedVnd: number; // integer VND
  totalCoinsEarned: number;
  favoriteFabric: string;
  favoriteShopName: string;
  csrContributedVnd: number;
  thankYouVoucherCode: string;
  isSampleData: true;
}

export interface GiftCardItem {
  id: string;
  code: string;
  amountVnd: number; // integer VND
  remainingVnd: number; // integer VND
  recipientEmail?: string;
  message?: string;
  issuedAt: string;
  expiresAt: string; // < 12 months validity
  status: "kha_dung" | "da_dung_mot_phan" | "da_dung_het";
}

export interface MembershipProfileState {
  spend4Q: number; // integer VND
  pendingCoins: number; // integer Xu awaiting 45-day clearance
  expiringCoins: number; // integer Xu expiring within 30 days
  expiringDateLabel: string;
  retentionTargetVnd: number; // remaining spend to keep tier
  retentionDeadline: string;
  birthday: string; // YYYY-MM-DD
  birthdayGiftClaimed: boolean;
  referralCode: string;
  referrals: ReferralRecord[];
  xuHistory: XuLedgerEntry[];
  eduEmail: string | null;
  eduVerified: boolean;
  giftCards: GiftCardItem[];
  appliedGiftCardCode: string | null;
  yearInReview: YearInReviewSample;
}

export type FitFeedback = "chat" | "dung" | "rong";

export interface ProductReview {
  id: string;
  productId: string;
  authorName: string;
  rating: number; // 1..5
  fitFeedback: FitFeedback;
  buyerHeightCm: number;
  buyerWeightKg: number;
  purchasedSize: SizeCode;
  colorName: string;
  comment: string;
  createdAt: string;
  images: string[];
  shopReply?: {
    shopName: string;
    text: string;
    repliedAt: string;
  };
  verifiedPurchase: boolean;
  isSampleData: boolean;
}

export interface ProductQA {
  id: string;
  productId: string;
  askerName: string;
  question: string;
  askedAt: string;
  answer?: string;
  answeredBy?: string;
  answeredAt?: string;
  isSampleData: boolean;
}

export interface ReviewSummary {
  count: number;
  average: number;
  fitCounts: Record<FitFeedback, number>;
  fitPercentages: Record<FitFeedback, number>;
  withImagesCount: number;
  withReplyCount: number;
  starCounts: Record<1 | 2 | 3 | 4 | 5, number>;
}

export const SAMPLE_DATA_NOTICE = "Đánh giá và số liệu tham khảo tại chế độ xem thử.";

export const MEMBERSHIP_TIERS: MembershipTier[] = [
  {
    id: "thanh_vien",
    name: "Thành viên",
    minSpend4Q: 0,
    cashbackRate: 0.01,
    cashbackPercentLabel: "1%",
    perks: [
      "Hoàn 1% Xu trên mỗi đơn hoàn tất (1 Xu = 1₫, hạn dùng 12 tháng).",
      "Nhân đôi Xu hoàn vào mỗi Thứ Sáu hằng tuần.",
      "Đổi trả miễn phí 45 ngày và lưu hồ sơ số đo toàn sàn.",
    ],
  },
  {
    id: "bac",
    name: "Bạc",
    minSpend4Q: 1000000,
    cashbackRate: 0.02,
    cashbackPercentLabel: "2%",
    perks: [
      "Hoàn 2% Xu dùng chéo cả 4 xưởng dệt, nhân đôi vào Thứ Sáu (4%).",
      "Tự động nhận quà sinh nhật trong giỏ hàng và mã giảm SINHNHAT50 (50.000₫).",
      "Ưu tiên suất đăng ký sớm các đợt Flash sale khung trưa.",
    ],
  },
  {
    id: "vang",
    name: "Vàng",
    minSpend4Q: 3000000,
    cashbackRate: 0.04,
    cashbackPercentLabel: "4%",
    perks: [
      "Hoàn 4% Xu mỗi đơn hoàn tất, nhân đôi Thứ Sáu lên tới 8%.",
      "Mở khóa mã đặc quyền NEXUSVIP12 giảm 12% tối đa 90.000₫.",
      "Miễn phí 1 lượt giao nhanh nội thành mỗi tháng tại TP.HCM, Hà Nội, Đà Nẵng.",
    ],
  },
  {
    id: "bach_kim",
    name: "Bạch Kim",
    minSpend4Q: 6000000,
    cashbackRate: 0.06,
    cashbackPercentLabel: "6%",
    perks: [
      "Hoàn 6% Xu mọi đơn hoàn tất, nhân đôi Thứ Sáu lên tới 12%.",
      "Ưu tiên đổi size 2 chiều trong ngày và tổng đài hỗ trợ riêng.",
      "Quà sinh nhật đồng tài trợ từ xưởng và mã tri ân Tổng kết năm.",
    ],
  },
];

export const XU_POLICY = {
  vndPerCoin: 1,
  expiryMonths: 12,
  fridayMultiplier: 2,
  maxOrderRatioPercent: 20,
  summary:
    "1 Xu = 1₫, thời hạn 12 tháng kể từ ngày cộng, nhân đôi tỷ lệ hoàn vào Thứ Sáu và sử dụng chéo trên toàn bộ gian hàng NexusMart.",
} as const;

export const REFERRAL_CONFIG = {
  cashbackPercent: 10,
  maxMonthlyCoins: 500000,
  maxMonthlyInvites: 10,
  clearanceDays: 45,
  ruleSummary:
    "Mời bạn bè đặt đơn đầu tiên thành công: nhận hoàn 10% giá trị đơn vào ví Xu sau khi đơn qua hạn đổi trả 45 ngày (tối đa 500.000 Xu và 10 lượt/tháng).",
} as const;

export const GIFT_CARD_DENOMINATIONS: number[] = [100000, 200000, 500000, 1000000, 2000000];

export const GIFT_CARD_POLICY = {
  validityDays: 300, // dưới 1 năm (10 tháng)
  customCodeMinLen: 4,
  customCodeMaxLen: 12,
  note: "Thẻ quà tặng dùng thanh toán chéo mọi gian hàng trên NexusMart, chia nhỏ nhiều lần tiêu, thời hạn 300 ngày (dưới 12 tháng).",
} as const;

export const EDU_DISCOUNT_CONFIG = {
  voucherCode: "EDU15",
  discountPercent: 15,
  maxDiscountVnd: 50000,
  minOrderVnd: 199000,
  allowedDomains: [".edu", ".edu.vn"],
  note: "Xác thực email sinh viên đuôi .edu hoặc .edu.vn để mở khóa ngay mã EDU15 giảm 15% (tối đa 50.000₫) cho đơn từ 199.000₫.",
} as const;

export function getTierBySpend(spend4Q: number): MembershipTier {
  let matched = MEMBERSHIP_TIERS[0];
  for (const tier of MEMBERSHIP_TIERS) {
    if (spend4Q >= tier.minSpend4Q) {
      matched = tier;
    }
  }
  return matched;
}

export function getNextTierInfo(spend4Q: number): {
  current: MembershipTier;
  next: MembershipTier | null;
  remainingVnd: number;
  progressPercent: number;
} {
  const current = getTierBySpend(spend4Q);
  const idx = MEMBERSHIP_TIERS.findIndex((t) => t.id === current.id);
  const next = idx >= 0 && idx < MEMBERSHIP_TIERS.length - 1 ? MEMBERSHIP_TIERS[idx + 1] : null;
  if (!next) {
    return { current, next: null, remainingVnd: 0, progressPercent: 100 };
  }
  const span = Math.max(1, next.minSpend4Q - current.minSpend4Q);
  const gained = Math.max(0, spend4Q - current.minSpend4Q);
  return {
    current,
    next,
    remainingVnd: Math.max(0, next.minSpend4Q - spend4Q),
    progressPercent: Math.min(100, Math.round((gained / span) * 100)),
  };
}

export const INITIAL_GIFT_CARDS: GiftCardItem[] = [
  {
    id: "gc-demo-1",
    code: "NEXUS-GIFT100",
    amountVnd: 100000,
    remainingVnd: 100000,
    recipientEmail: "minhquan.nguyen@nexusmart.vn",
    message: "Quà tặng mua đồ basic từ đồng nghiệp",
    issuedAt: "2026-10-15T09:00:00+07:00",
    expiresAt: "2027-08-11T23:59:59+07:00",
    status: "kha_dung",
  },
  {
    id: "gc-demo-2",
    code: "NEXUS-BANBE200",
    amountVnd: 200000,
    remainingVnd: 150000,
    recipientEmail: "minhquan.nguyen@nexusmart.vn",
    message: "Dùng mua áo polo hoặc quần jogger mùa thu",
    issuedAt: "2026-09-20T14:00:00+07:00",
    expiresAt: "2027-07-17T23:59:59+07:00",
    status: "da_dung_mot_phan",
  },
];

export const INITIAL_MEMBERSHIP_STATE: MembershipProfileState = {
  spend4Q: 2350000, // Hạng Bạc, còn 650.000₫ lên hạng Vàng
  pendingCoins: 18500,
  expiringCoins: 6000,
  expiringDateLabel: "18/12/2026",
  retentionTargetVnd: 350000,
  retentionDeadline: "31/12/2026",
  birthday: "1996-11-24",
  birthdayGiftClaimed: false,
  referralCode: "NEXUS-QUAN26",
  referrals: [
    {
      id: "ref-1",
      friendName: "Lê Hoàng Nam",
      invitedAt: "2026-10-02",
      orderCode: "NM-261004-312",
      orderValueVnd: 480000,
      rewardCoins: 48000,
      status: "da_cong_xu",
    },
    {
      id: "ref-2",
      friendName: "Phạm Thu Hà",
      invitedAt: "2026-11-11",
      orderCode: "NM-261113-408",
      orderValueVnd: 390000,
      rewardCoins: 39000,
      status: "cho_het_han_45_ngay",
    },
    {
      id: "ref-3",
      friendName: "Đặng Quốc Bảo",
      invitedAt: "2026-11-18",
      rewardCoins: 0,
      status: "cho_dat_don",
    },
  ],
  xuHistory: [
    {
      id: "xu-1",
      date: "2026-11-14",
      title: "Hoàn Xu 2% đơn hàng NM-261112-061 (nhân đôi Thứ Sáu)",
      orderCode: "NM-261112-061",
      deltaCoins: 14000,
      status: "cho_duyet",
    },
    {
      id: "xu-2",
      date: "2026-11-08",
      title: "Dùng Xu thanh toán đơn NM-261120-104",
      orderCode: "NM-261120-104",
      deltaCoins: -10000,
      status: "da_dung",
    },
    {
      id: "xu-3",
      date: "2026-10-20",
      title: "Thưởng giới thiệu bạn bè (Lê Hoàng Nam)",
      orderCode: "NM-261004-312",
      deltaCoins: 48000,
      status: "kha_dung",
    },
    {
      id: "xu-4",
      date: "2026-10-05",
      title: "Hoàn Xu 2% đơn hàng tháng 10",
      orderCode: "NM-261001-119",
      deltaCoins: 12000,
      status: "kha_dung",
    },
  ],
  eduEmail: null,
  eduVerified: false,
  giftCards: INITIAL_GIFT_CARDS,
  appliedGiftCardCode: null,
  yearInReview: {
    year: 2026,
    totalOrders: 9,
    totalSpentVnd: 3420000,
    totalSavedVnd: 584000,
    totalCoinsEarned: 112000,
    favoriteFabric: "Cotton Compact 220gsm & Dry-Fit 145gsm",
    favoriteShopName: "Xưởng Dệt Bạch Đằng",
    csrContributedVnd: 164000,
    thankYouVoucherCode: "NEXUS30",
    isSampleData: true,
  },
};

export const SAMPLE_REVIEWS: ProductReview[] = [
  {
    id: "rev-1",
    productId: "prod-at-trang",
    authorName: "Trần Minh Đức (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 174,
    buyerWeightKg: 71,
    purchasedSize: "L",
    colorName: "Trắng tinh",
    comment:
      "Vải 220gsm cầm chắc tay, mặc màu trắng ra nắng không bị mỏng. Giặt máy 5 lần cổ áo vẫn phẳng gọn.",
    createdAt: "2026-11-15T10:20:00+07:00",
    images: ["/products/ao-thun-cotton-trang.png"],
    shopReply: {
      shopName: "Xưởng Dệt Bạch Đằng",
      text: "Cảm ơn anh Đức đã phản hồi thực tế về độ dày 220gsm và bo cổ. Xưởng rất vui khi áo vừa đúng phom.",
      repliedAt: "2026-11-15T14:05:00+07:00",
    },
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-2",
    productId: "prod-at-trang",
    authorName: "Nguyễn Phương Thảo (mẫu)",
    rating: 5,
    fitFeedback: "rong",
    buyerHeightCm: 162,
    buyerWeightKg: 51,
    purchasedSize: "M",
    colorName: "Xám đá",
    comment:
      "Mình chọn size M để mặc rộng chút, vai xuôi nhẹ rất thoải mái. Nếu nữ thích mặc ôm gọn nên lấy size S.",
    createdAt: "2026-11-12T18:10:00+07:00",
    images: ["/products/ao-thun-cotton-xam-da.png"],
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-3",
    productId: "prod-at-trang",
    authorName: "Lê Anh Tuấn (mẫu)",
    rating: 4,
    fitFeedback: "dung",
    buyerHeightCm: 170,
    buyerWeightKg: 65,
    purchasedSize: "M",
    colorName: "Xanh than",
    comment: "Chất vải mịn và thoáng, màu xanh than thực tế trầm đẹp. Giao về Đà Nẵng mất 2 ngày.",
    createdAt: "2026-11-08T09:40:00+07:00",
    images: [],
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-4",
    productId: "prod-at-dryfit",
    authorName: "Vũ Quốc Khánh (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 175,
    buyerWeightKg: 69,
    purchasedSize: "L",
    colorName: "Xanh cổ vịt",
    comment:
      "Mặc chạy 10 km buổi sáng thấm mồ hôi tốt, không cọ rát nách. Phơi gió khoảng 1 tiếng là khô ráo.",
    createdAt: "2026-11-16T08:15:00+07:00",
    images: ["/products/ao-thun-dryfit-co-vit.png"],
    shopReply: {
      shopName: "Kho Basic Sài Gòn",
      text: "Cảm ơn anh Khánh. Lưu ý giặt nước lạnh và không dùng nước xả để sợi micro-mesh giữ độ thoát ẩm lâu nhé.",
      repliedAt: "2026-11-16T11:20:00+07:00",
    },
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-5",
    productId: "prod-polo-kem",
    authorName: "Phạm Quang Huy (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 176,
    buyerWeightKg: 73,
    purchasedSize: "L",
    colorName: "Kem nhạt",
    comment: "Cổ polo đứng nếp, vải pique thoáng không hầm lưng khi đi gặp khách cả ngày.",
    createdAt: "2026-11-14T16:45:00+07:00",
    images: ["/products/ao-polo-pique-kem.png"],
    shopReply: {
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      text: "Cảm ơn anh Huy đã tin tưởng Sợi Tự Nhiên Đà Nẵng. Chúc anh có thêm nhiều ngày làm việc thoải mái.",
      repliedAt: "2026-11-14T19:00:00+07:00",
    },
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-6",
    productId: "prod-polo-detkim",
    authorName: "Đỗ Lan Chi (mẫu)",
    rating: 4,
    fitFeedback: "chat",
    buyerHeightCm: 165,
    buyerWeightKg: 57,
    purchasedSize: "S",
    colorName: "Đen than",
    comment:
      "Vải dệt kim mịn sang, nhưng form Dệt Kim Hà Nội khá ôm nên mình đã dùng đổi size 2 chiều lên M rất nhanh.",
    createdAt: "2026-11-13T11:30:00+07:00",
    images: ["/products/ao-polo-det-kim-den.png"],
    shopReply: {
      shopName: "Dệt Kim Hà Nội",
      text: "Cảm ơn chị Chi đã chia sẻ để các khách hàng sau lưu ý tăng 1 cỡ khi muốn mặc thoải mái với dòng dệt kim ạ.",
      repliedAt: "2026-11-13T15:10:00+07:00",
    },
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-7",
    productId: "prod-short-daily",
    authorName: "Hoàng Gia Bảo (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 172,
    buyerWeightKg: 68,
    purchasedSize: "L",
    colorName: "Be cát",
    comment:
      "Túi sâu đựng điện thoại không trượt ra khi chạy xe máy. Chiều dài 7 inch trên gối vừa đẹp.",
    createdAt: "2026-11-10T15:05:00+07:00",
    images: ["/products/quan-short-daily-be.png"],
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-8",
    productId: "prod-jogger-terry",
    authorName: "Ngô Thành Công (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 177,
    buyerWeightKg: 74,
    purchasedSize: "L",
    colorName: "Xám muối tiêu",
    comment:
      "Nỉ chân cua 320gsm mặc mát hơn nỉ bông, bo gấu gọn không bị bai. Rất ưng trong tầm giá.",
    createdAt: "2026-11-11T20:10:00+07:00",
    images: ["/products/quan-jogger-terry-xam.png"],
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-9",
    productId: "prod-dolot-boxer",
    authorName: "Bùi Trung Kiên (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 173,
    buyerWeightKg: 70,
    purchasedSize: "L",
    colorName: "Combo Xám / Than / Đen",
    comment:
      "Vải modal mềm mát, cạp chun không hằn bụng và đường may phẳng không cộm đùi khi ngồi lâu.",
    createdAt: "2026-11-09T13:25:00+07:00",
    images: ["/products/do-lot-boxer-modal-3.png"],
    verifiedPurchase: true,
    isSampleData: true,
  },
  {
    id: "rev-10",
    productId: "prod-tat-cotrung",
    authorName: "Đinh Hải Yến (mẫu)",
    rating: 5,
    fitFeedback: "dung",
    buyerHeightCm: 161,
    buyerWeightKg: 50,
    purchasedSize: "M",
    colorName: "5 tông Trắng đến Đen",
    comment:
      "Mũi tất liền không cộm ngón chân khi đi sneaker cả ngày, 5 màu dễ chia theo lịch làm việc.",
    createdAt: "2026-11-07T17:40:00+07:00",
    images: ["/products/tat-cotton-co-trung-5.png"],
    verifiedPurchase: true,
    isSampleData: true,
  },
];

export const SAMPLE_QAS: ProductQA[] = [
  {
    id: "qa-1",
    productId: "prod-at-trang",
    askerName: "Nguyễn Hải (mẫu)",
    question: "Mình cao 1m71 nặng 66 kg, thích mặc hơi rộng vai một chút thì lấy M hay L ạ?",
    askedAt: "2026-11-17T09:10:00+07:00",
    answer:
      "Chào anh Hải, số đo 1m71 và 66 kg mặc M sẽ vừa vặn gọn vai. Nếu anh thích cảm giác thoáng vai nhẹ có thể chọn L (dài hơn 2 cm, rộng ngực thêm 6 cm).",
    answeredBy: "Xưởng Dệt Bạch Đằng",
    answeredAt: "2026-11-17T09:28:00+07:00",
    isSampleData: true,
  },
  {
    id: "qa-2",
    productId: "prod-at-trang",
    askerName: "Trần Thanh Tú (mẫu)",
    question: "Áo Cotton Compact 220gsm giặt máy nhiều lần có bị giãn cổ hay co rút không?",
    askedAt: "2026-11-18T14:22:00+07:00",
    answer:
      "Vải đã qua xử lý tiền co rút và bo cổ rib 1x1 280gsm nên giữ phom ổn định. Anh nên lộn trái khi giặt máy ở chế độ nhẹ và phơi ngang bóng râm.",
    answeredBy: "Xưởng Dệt Bạch Đằng",
    answeredAt: "2026-11-18T14:35:00+07:00",
    isSampleData: true,
  },
  {
    id: "qa-3",
    productId: "prod-polo-kem",
    askerName: "Lý Văn Long (mẫu)",
    question: "Mình cao 1m74 nặng 72kg mặc đi làm công sở nên chọn size L hay XL?",
    askedAt: "2026-11-15T11:00:00+07:00",
    answer:
      "Với số đo 1m74 – 72kg, anh chọn size L sẽ vừa gọn vai và ngực theo dáng regular. Nếu thích mặc rộng thoải mái hơn, anh có thể tăng lên size XL.",
    answeredBy: "Sợi Tự Nhiên Đà Nẵng",
    answeredAt: "2026-11-15T11:18:00+07:00",
    isSampleData: true,
  },
  {
    id: "qa-4",
    productId: "prod-dolot-boxer",
    askerName: "Phan Minh Khôi (mẫu)",
    question:
      "Hộp 3 quần lót boxer sợi Modal gồm những màu nào và có bị cuộn ống khi ngồi lâu không?",
    askedAt: "2026-11-16T20:05:00+07:00",
    answer:
      "Mỗi hộp gồm 3 màu trung tính (Xám nhạt, Xám chì, Đen than) cùng một kích cỡ đã chọn, đường may flatlock phẳng và ống dài vừa phải hạn chế cuộn khi vận động.",
    answeredBy: "Xưởng Dệt Bạch Đằng",
    answeredAt: "2026-11-16T20:20:00+07:00",
    isSampleData: true,
  },
];

export function computeReviewSummary(reviews: ProductReview[], productId?: string): ReviewSummary {
  const list = productId ? reviews.filter((r) => r.productId === productId) : reviews;
  const count = list.length;
  const fitCounts: Record<FitFeedback, number> = { chat: 0, dung: 0, rong: 0 };
  const starCounts: Record<1 | 2 | 3 | 4 | 5, number> = { 1: 0, 2: 0, 3: 0, 4: 0, 5: 0 };
  let sum = 0;
  let withImagesCount = 0;
  let withReplyCount = 0;

  for (const r of list) {
    sum += r.rating;
    fitCounts[r.fitFeedback] += 1;
    const star = Math.max(1, Math.min(5, Math.round(r.rating))) as 1 | 2 | 3 | 4 | 5;
    starCounts[star] += 1;
    if (r.images.length > 0) withImagesCount += 1;
    if (r.shopReply) withReplyCount += 1;
  }

  const average = count > 0 ? Math.round((sum / count) * 10) / 10 : 0;
  const fitPercentages: Record<FitFeedback, number> = {
    chat: count > 0 ? Math.round((fitCounts.chat / count) * 100) : 0,
    dung: count > 0 ? Math.round((fitCounts.dung / count) * 100) : 0,
    rong: count > 0 ? Math.round((fitCounts.rong / count) * 100) : 0,
  };

  return {
    count,
    average,
    fitCounts,
    fitPercentages,
    withImagesCount,
    withReplyCount,
    starCounts,
  };
}
