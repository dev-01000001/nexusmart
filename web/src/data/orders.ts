export type OrderStatus =
  "cho_xac_nhan" | "dang_chuan_bi" | "dang_giao" | "da_giao" | "yeu_cau_tra_hang" | "da_huy";

export type PaymentStatus =
  "cho_thanh_toan" | "thanh_toan_that_bai" | "da_thanh_toan" | "cod_cho_thu" | "da_hoan_tien";

export type PaymentMethod = "cod" | "vietqr" | "vi_sandbox";
export const WALLET_PAYMENT_METHOD: PaymentMethod = "vi_sandbox";
export type DeliveryMethod = "standard" | "fast";
export type ShippingCarrier = "SPX" | "GHN" | "NexusFast";
export type StatusTone = "neutral" | "warn" | "pos" | "neg";

export interface OrderStatusMeta {
  label: string;
  tone: StatusTone;
  description: string;
}

export const ORDER_STATUS_META: Record<OrderStatus, OrderStatusMeta> = {
  cho_xac_nhan: {
    label: "Chờ xác nhận",
    tone: "warn",
    description: "Gian hàng đang kiểm tra hàng sẵn có và xác nhận đơn.",
  },
  dang_chuan_bi: {
    label: "Đang chuẩn bị",
    tone: "neutral",
    description: "Đang đóng gói tại kho và chờ đơn vị vận chuyển lấy hàng.",
  },
  dang_giao: {
    label: "Đang giao hàng",
    tone: "neutral",
    description: "Kiện hàng đang trên tuyến giao đến địa chỉ người nhận.",
  },
  da_giao: {
    label: "Đã giao",
    tone: "pos",
    description: "Giao thành công. Có thể yêu cầu đổi size 2 chiều hoặc trả hàng trong 45 ngày.",
  },
  yeu_cau_tra_hang: {
    label: "Yêu cầu trả hàng",
    tone: "warn",
    description: "Đang xác minh lý do đổi trả 45 ngày và điều phối bưu tá 2 chiều.",
  },
  da_huy: {
    label: "Đã hủy",
    tone: "neg",
    description: "Đơn hàng đã hủy trước khi bàn giao vận chuyển.",
  },
};

export const PAYMENT_STATUS_META: Record<PaymentStatus, { label: string; tone: StatusTone }> = {
  cho_thanh_toan: { label: "Chờ thanh toán", tone: "warn" },
  thanh_toan_that_bai: { label: "Thanh toán gián đoạn", tone: "neg" },
  da_thanh_toan: { label: "Đã thanh toán", tone: "pos" },
  cod_cho_thu: { label: "Thu hộ khi nhận hàng", tone: "neutral" },
  da_hoan_tien: { label: "Đã hoàn tiền", tone: "pos" },
};

export const ALLOWED_ORDER_TRANSITIONS: Record<OrderStatus, OrderStatus[]> = {
  cho_xac_nhan: ["dang_chuan_bi", "da_huy"],
  dang_chuan_bi: ["dang_giao", "da_huy"],
  dang_giao: ["da_giao"],
  da_giao: ["yeu_cau_tra_hang"],
  yeu_cau_tra_hang: ["da_giao", "da_huy"],
  da_huy: [],
};

export const PAYMENT_METHOD_LABELS: Record<PaymentMethod, string> = {
  cod: "Thanh toán khi nhận hàng (COD)",
  vietqr: "Chuyển khoản nhanh VietQR",
  vi_sandbox: "Ví điện tử NexusPay",
};

export const DELIVERY_METHOD_LABELS: Record<DeliveryMethod, string> = {
  standard: "Giao tiêu chuẩn 1-4 ngày (SPX / GHN · đồng kiểm)",
  fast: "Giao nhanh nội thành 2-4 giờ (theo kho xưởng)",
};

export interface UserAddress {
  id: string;
  recipientName: string;
  phone: string;
  provinceCode: string;
  provinceName: string;
  wardName: string;
  streetDetail: string;
  isDefault: boolean;
}

export interface DemoUser {
  id: string;
  fullName: string;
  email: string;
  phone: string;
  memberSince: string;
  coins: number;
  birthday?: string; // YYYY-MM-DD
}

export interface OrderLineItem {
  skuCode: string;
  productId: string;
  productSlug: string;
  productName: string;
  colorName: string;
  size: string;
  image: string;
  unitPrice: number;
  quantity: number;
  lineTotal: number;
}

export interface OrderTimelineEvent {
  timestamp: string;
  status: OrderStatus;
  title: string;
  note: string;
}

export interface OrderVatInvoice {
  requested: boolean;
  companyName: string;
  taxCode: string;
  email: string;
  companyAddress?: string;
  status: "cho_duyet_nhap_24h" | "da_phat_hanh";
  requestedAt: string;
}

export interface OrderExchangeDetail {
  type: "doi_size_2_chieu" | "tra_hang_hoan_tien" | "bu_le_tat";
  reason: string;
  fromSize?: string;
  toSize?: string;
  pickupAddress?: string;
  refundScheduleNote?: string;
  status: "da_tiep_nhan" | "dang_dieu_phoi_buu_ta" | "hoan_tat";
  requestedAt: string;
}

export interface OrderAddressEditRequest {
  requestedAddress: string;
  reason: string;
  status: "cho_shop_duyet" | "da_cap_nhat";
  requestedAt: string;
}

export interface Order {
  id: string;
  orderCode: string;
  parentOrderCode?: string;
  salesChannel?: "MARKETPLACE" | "STOREFRONT";
  storefrontShopId?: string;
  shopId: string;
  shopName: string;
  buyerName: string;
  buyerPhone: string;
  buyerEmail?: string;
  isGuestOrder?: boolean;
  shippingAddress: string;
  provinceCode: string;
  status: OrderStatus;
  paymentStatus?: PaymentStatus;
  paymentMethod: PaymentMethod;
  paymentDueAt?: string;
  isPaid: boolean;
  deliveryMethod?: DeliveryMethod;
  carrier?: ShippingCarrier;
  coInspectionEnabled?: boolean;
  createdAt: string;
  updatedAt: string;
  items: OrderLineItem[];
  subtotal: number;
  shippingFee: number;
  shopVoucherDiscount: number;
  platformVoucherDiscount: number;
  freeshipDiscount: number;
  coinDiscount: number;
  tierDiscount?: number;
  giftCardDiscount?: number;
  total: number;
  note?: string;
  giftWrap?: boolean;
  giftCardMessage?: string;
  vatInvoice?: OrderVatInvoice;
  exchangeRequest?: OrderExchangeDetail;
  addressEditRequest?: OrderAddressEditRequest;
  cancelReason?: string;
  returnReason?: string;
  trackingCode?: string;
  notificationChannels?: string[];
  timeline: OrderTimelineEvent[];
}

export const DEMO_USER: DemoUser = {
  id: "usr-minh-quan",
  fullName: "Nguyễn Minh Quân",
  email: "minhquan.nguyen@nexusmart.vn",
  phone: "0912 486 305",
  memberSince: "2025-04-12",
  coins: 42000,
  birthday: "1996-11-24",
};

export const INITIAL_ADDRESSES: UserAddress[] = [
  {
    id: "addr-hcm-1",
    recipientName: "Nguyễn Minh Quân",
    phone: "0912 486 305",
    provinceCode: "HCM",
    provinceName: "Thành phố Hồ Chí Minh",
    wardName: "Phường Xuân Hòa",
    streetDetail: "Số 248 đường Võ Văn Tần",
    isDefault: true,
  },
  {
    id: "addr-hn-2",
    recipientName: "Nguyễn Thị Lan Anh",
    phone: "0984 219 740",
    provinceCode: "HN",
    provinceName: "Thành phố Hà Nội",
    wardName: "Phường Cầu Giấy",
    streetDetail: "Ngõ 85 phố Xuân Thủy, nhà số 14",
    isDefault: false,
  },
];

export function computeOrderTotals(input: {
  items: Array<Omit<OrderLineItem, "lineTotal">>;
  shippingFee: number;
  shopVoucherDiscount?: number;
  platformVoucherDiscount?: number;
  freeshipDiscount?: number;
  coinDiscount?: number;
  tierDiscount?: number;
  giftCardDiscount?: number;
}) {
  const items: OrderLineItem[] = input.items.map((it) => ({
    ...it,
    lineTotal: it.unitPrice * it.quantity,
  }));
  const subtotal = items.reduce((sum, it) => sum + it.lineTotal, 0);
  const shopVoucherDiscount = input.shopVoucherDiscount ?? 0;
  const platformVoucherDiscount = input.platformVoucherDiscount ?? 0;
  const freeshipDiscount = input.freeshipDiscount ?? 0;
  const coinDiscount = input.coinDiscount ?? 0;
  const tierDiscount = input.tierDiscount ?? 0;
  const giftCardDiscount = input.giftCardDiscount ?? 0;
  const total = Math.max(
    0,
    subtotal +
      input.shippingFee -
      shopVoucherDiscount -
      platformVoucherDiscount -
      freeshipDiscount -
      coinDiscount -
      tierDiscount -
      giftCardDiscount
  );
  return {
    items,
    subtotal,
    shippingFee: input.shippingFee,
    shopVoucherDiscount,
    platformVoucherDiscount,
    freeshipDiscount,
    coinDiscount,
    ...(tierDiscount > 0 ? { tierDiscount } : {}),
    ...(giftCardDiscount > 0 ? { giftCardDiscount } : {}),
    total,
  };
}

function makeOrder(
  base: Omit<
    Order,
    | "items"
    | "subtotal"
    | "shippingFee"
    | "shopVoucherDiscount"
    | "platformVoucherDiscount"
    | "freeshipDiscount"
    | "coinDiscount"
    | "total"
  >,
  pricing: Parameters<typeof computeOrderTotals>[0]
): Order {
  return {
    ...base,
    ...computeOrderTotals(pricing),
  };
}

export const INITIAL_ORDERS: Order[] = [
  // Đơn mẫu chờ thanh toán (cho demo Thanh toán lại)
  makeOrder(
    {
      id: "ord-261120-109",
      orderCode: "NM-261120-109",
      parentOrderCode: "NMP-261120-88",
      shopId: "shop-sai-gon",
      shopName: "Kho Basic Sài Gòn",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Số 248 đường Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh",
      provinceCode: "HCM",
      status: "cho_xac_nhan",
      paymentStatus: "cho_thanh_toan",
      paymentMethod: "vietqr",
      paymentDueAt: "2026-11-20T06:00:00+07:00",
      isPaid: false,
      deliveryMethod: "fast",
      carrier: "NexusFast",
      coInspectionEnabled: true,
      createdAt: "2026-11-20T02:15:00+07:00",
      updatedAt: "2026-11-20T02:16:00+07:00",
      note: "Giao nhanh buổi sáng trước 11h30.",
      giftWrap: true,
      giftCardMessage: "Chúc mừng sinh nhật, mặc thật vừa và thoải mái nhé.",
      notificationChannels: ["Email xác nhận", "Zalo ZNS"],
      timeline: [
        {
          timestamp: "2026-11-20T02:15:00+07:00",
          status: "cho_xac_nhan",
          title: "Giữ đơn chờ thanh toán lại",
          note: "Giao dịch chuyển khoản VietQR chưa hoàn tất. Sản phẩm trong đơn vẫn được giữ sẵn để bạn thanh toán lại hoặc đổi sang COD / Ví NexusPay.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "SG-SD09-BE-L",
          productId: "prod-short-daily",
          productSlug: "quan-short-daily-cotton-7-inch-be",
          productName: "Quần Short Daily Cotton 7 inch Be cát",
          colorName: "Be cát",
          size: "L",
          image: "/products/quan-short-daily-be.png",
          unitPrice: 249000,
          quantity: 1,
        },
        {
          skuCode: "SG-OS05-THANCHI-L",
          productId: "prod-at-oversize",
          productSlug: "ao-thun-oversize-heavyweight-than-chi",
          productName: "Áo thun Oversize 260gsm Than chì",
          colorName: "Than chì",
          size: "L",
          image: "/products/ao-thun-oversize-than-chi.png",
          unitPrice: 239000,
          quantity: 1,
        },
      ],
      shippingFee: 37000,
      shopVoucherDiscount: 25000,
      platformVoucherDiscount: 30000,
      freeshipDiscount: 25000,
      coinDiscount: 0,
    }
  ),
  makeOrder(
    {
      id: "ord-261120-104",
      orderCode: "NM-261120-104",
      parentOrderCode: "NMP-261120-84",
      shopId: "shop-bach-dang",
      shopName: "Xưởng Dệt Bạch Đằng",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Số 248 đường Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh",
      provinceCode: "HCM",
      status: "cho_xac_nhan",
      paymentStatus: "da_thanh_toan",
      paymentMethod: "vietqr",
      isPaid: true,
      deliveryMethod: "standard",
      carrier: "SPX",
      coInspectionEnabled: true,
      createdAt: "2026-11-20T01:40:00+07:00",
      updatedAt: "2026-11-20T01:42:00+07:00",
      note: "Giao giờ hành chính, gọi trước 15 phút.",
      vatInvoice: {
        requested: true,
        companyName: "Công ty TNHH Kiến trúc Đông Nam",
        taxCode: "0316482910",
        email: "ketoan@dongnam-arch.vn",
        status: "cho_duyet_nhap_24h",
        requestedAt: "2026-11-20T01:40:00+07:00",
      },
      notificationChannels: ["Email xác nhận", "Zalo ZNS"],
      timeline: [
        {
          timestamp: "2026-11-20T01:40:00+07:00",
          status: "cho_xac_nhan",
          title: "Đặt hàng thành công",
          note: "Đã xác nhận thanh toán qua VietQR và chuyển đơn đến xưởng chuẩn bị.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "BD-AT01-TRANG-L",
          productId: "prod-at-trang",
          productSlug: "ao-thun-cotton-compact-trang",
          productName: "Áo thun Cotton Compact 220gsm Trắng",
          colorName: "Trắng tinh",
          size: "L",
          image: "/products/ao-thun-cotton-trang.png",
          unitPrice: 179000,
          quantity: 2,
        },
      ],
      shippingFee: 22000,
      shopVoucherDiscount: 20000,
      platformVoucherDiscount: 0,
      freeshipDiscount: 22000,
      coinDiscount: 10000,
    }
  ),
  makeOrder(
    {
      id: "ord-261119-098",
      orderCode: "NM-261119-098",
      parentOrderCode: "NMP-261119-71",
      shopId: "shop-da-nang",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Số 248 đường Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh",
      provinceCode: "HCM",
      status: "dang_chuan_bi",
      paymentStatus: "da_thanh_toan",
      paymentMethod: "vi_sandbox",
      isPaid: true,
      deliveryMethod: "standard",
      carrier: "GHN",
      coInspectionEnabled: true,
      createdAt: "2026-11-19T14:20:00+07:00",
      updatedAt: "2026-11-19T16:05:00+07:00",
      trackingCode: "NMLOG-8841920",
      notificationChannels: ["Email xác nhận", "Zalo ZNS"],
      timeline: [
        {
          timestamp: "2026-11-19T14:20:00+07:00",
          status: "cho_xac_nhan",
          title: "Đặt hàng thành công",
          note: "Thanh toán thành công qua Ví NexusPay.",
        },
        {
          timestamp: "2026-11-19T16:05:00+07:00",
          status: "dang_chuan_bi",
          title: "Gian hàng đã xác nhận",
          note: "Kho Đà Nẵng đang đóng gói và in phiếu giao hàng.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "DN-PL06-KEM-L",
          productId: "prod-polo-kem",
          productSlug: "ao-polo-pique-cotton-kem-nhat",
          productName: "Áo Polo Pique Cotton Kem nhạt",
          colorName: "Kem nhạt",
          size: "L",
          image: "/products/ao-polo-pique-kem.png",
          unitPrice: 289000,
          quantity: 1,
        },
        {
          skuCode: "DN-SL11-XAMDA-L",
          productId: "prod-short-linen",
          productSlug: "quan-short-linen-pha-cotton-xam-da",
          productName: "Quần Short Linen pha Cotton Xám đá",
          colorName: "Xám đá",
          size: "L",
          image: "/products/quan-short-linen-xam-da.png",
          unitPrice: 279000,
          quantity: 1,
        },
      ],
      shippingFee: 22000,
      shopVoucherDiscount: 30000,
      platformVoucherDiscount: 30000,
      freeshipDiscount: 22000,
      coinDiscount: 0,
    }
  ),
  makeOrder(
    {
      id: "ord-261117-082",
      orderCode: "NM-261117-082",
      parentOrderCode: "NMP-261117-55",
      shopId: "shop-ha-noi",
      shopName: "Dệt Kim Hà Nội",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Số 248 đường Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh",
      provinceCode: "HCM",
      status: "dang_giao",
      paymentStatus: "cod_cho_thu",
      paymentMethod: "cod",
      isPaid: false,
      deliveryMethod: "standard",
      carrier: "SPX",
      coInspectionEnabled: true,
      createdAt: "2026-11-17T10:12:00+07:00",
      updatedAt: "2026-11-19T08:30:00+07:00",
      trackingCode: "NMLOG-8834011",
      notificationChannels: ["Email xác nhận", "Zalo ZNS"],
      timeline: [
        {
          timestamp: "2026-11-17T10:12:00+07:00",
          status: "cho_xac_nhan",
          title: "Đặt hàng thành công",
          note: "Thanh toán khi nhận hàng (COD), hỗ trợ đồng kiểm.",
        },
        {
          timestamp: "2026-11-17T13:45:00+07:00",
          status: "dang_chuan_bi",
          title: "Đã đóng gói tại kho Hà Nội",
          note: "Bàn giao cho đơn vị vận chuyển tuyến Bắc - Nam.",
        },
        {
          timestamp: "2026-11-19T08:30:00+07:00",
          status: "dang_giao",
          title: "Đã đến kho trung chuyển TP. Hồ Chí Minh",
          note: "Bưu tá sẽ giao hàng trong hôm nay.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "HN-JT12-XAM-L",
          productId: "prod-jogger-terry",
          productSlug: "quan-jogger-ni-chan-cua-xam-muoi-tieu",
          productName: "Quần Jogger Nỉ chân cua 320gsm Xám tiêu",
          colorName: "Xám muối tiêu",
          size: "L",
          image: "/products/quan-jogger-terry-xam.png",
          unitPrice: 349000,
          quantity: 1,
        },
        {
          skuCode: "HN-TT16-COMBO-5MAU-L",
          productId: "prod-tat-cotrung",
          productSlug: "combo-5-doi-tat-cotton-gan-co-trung",
          productName: "Combo 5 đôi Tất Cotton gân cổ trung",
          colorName: "5 tông Trắng đến Đen",
          size: "L",
          image: "/products/tat-cotton-co-trung-5.png",
          unitPrice: 129000,
          quantity: 1,
        },
      ],
      shippingFee: 22000,
      shopVoucherDiscount: 15000,
      platformVoucherDiscount: 30000,
      freeshipDiscount: 22000,
      coinDiscount: 0,
    }
  ),
  makeOrder(
    {
      id: "ord-261112-061",
      orderCode: "NM-261112-061",
      parentOrderCode: "NMP-261112-39",
      shopId: "shop-sai-gon",
      shopName: "Kho Basic Sài Gòn",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Số 248 đường Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh",
      provinceCode: "HCM",
      status: "da_giao",
      paymentStatus: "da_thanh_toan",
      paymentMethod: "vietqr",
      isPaid: true,
      deliveryMethod: "fast",
      carrier: "NexusFast",
      coInspectionEnabled: true,
      createdAt: "2026-11-12T19:05:00+07:00",
      updatedAt: "2026-11-14T11:22:00+07:00",
      trackingCode: "NMLOG-8812095",
      notificationChannels: ["Email xác nhận", "Zalo ZNS"],
      timeline: [
        {
          timestamp: "2026-11-12T19:05:00+07:00",
          status: "cho_xac_nhan",
          title: "Đặt hàng thành công",
          note: "Đã xác nhận thanh toán chuyển khoản VietQR.",
        },
        {
          timestamp: "2026-11-13T09:10:00+07:00",
          status: "dang_chuan_bi",
          title: "Kho Basic Sài Gòn xác nhận",
          note: "Đóng gói hoàn tất.",
        },
        {
          timestamp: "2026-11-13T15:40:00+07:00",
          status: "dang_giao",
          title: "Đang giao nội thành",
          note: "Bưu tá xuất kho Thảo Điền.",
        },
        {
          timestamp: "2026-11-14T11:22:00+07:00",
          status: "da_giao",
          title: "Giao hàng thành công",
          note: "Người nhận Nguyễn Minh Quân đã nhận đủ kiện hàng.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "SG-DF04-COVIT-L",
          productId: "prod-at-dryfit",
          productSlug: "ao-thun-the-thao-dryfit-xanh-co-vit",
          productName: "Áo thun Thể thao Dry-Fit Xanh cổ vịt",
          colorName: "Xanh cổ vịt",
          size: "L",
          image: "/products/ao-thun-dryfit-co-vit.png",
          unitPrice: 159000,
          quantity: 1,
        },
        {
          skuCode: "SG-SR10-XAMDAM-L",
          productId: "prod-short-run",
          productSlug: "quan-short-chay-bo-5-inch-xam-dam",
          productName: "Quần Short Chạy bộ 5 inch Xám đậm",
          colorName: "Xám đậm",
          size: "L",
          image: "/products/quan-short-run-xam-dam.png",
          unitPrice: 219000,
          quantity: 1,
        },
      ],
      shippingFee: 22000,
      shopVoucherDiscount: 25000,
      platformVoucherDiscount: 0,
      freeshipDiscount: 22000,
      coinDiscount: 0,
    }
  ),
  makeOrder(
    {
      id: "ord-261108-044",
      orderCode: "NM-261108-044",
      parentOrderCode: "NMP-261108-22",
      shopId: "shop-ha-noi",
      shopName: "Dệt Kim Hà Nội",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Ngõ 85 phố Xuân Thủy, nhà số 14, Phường Cầu Giấy, Thành phố Hà Nội",
      provinceCode: "HN",
      status: "yeu_cau_tra_hang",
      paymentStatus: "da_thanh_toan",
      paymentMethod: "vietqr",
      isPaid: true,
      deliveryMethod: "standard",
      carrier: "GHN",
      coInspectionEnabled: true,
      createdAt: "2026-11-08T11:30:00+07:00",
      updatedAt: "2026-11-11T09:15:00+07:00",
      trackingCode: "NMLOG-8790432",
      returnReason: "Cần đổi từ size M sang size L vì hơi kích vai.",
      exchangeRequest: {
        type: "doi_size_2_chieu",
        reason: "Cần đổi từ size M sang size L vì hơi kích vai.",
        fromSize: "M",
        toSize: "L",
        pickupAddress: "Ngõ 85 phố Xuân Thủy, nhà số 14, Phường Cầu Giấy, Thành phố Hà Nội",
        refundScheduleNote: "Đổi size 2 chiều miễn phí tận nơi trong 45 ngày.",
        status: "dang_dieu_phoi_buu_ta",
        requestedAt: "2026-11-11T09:15:00+07:00",
      },
      notificationChannels: ["Email xác nhận", "Zalo ZNS"],
      timeline: [
        {
          timestamp: "2026-11-08T11:30:00+07:00",
          status: "cho_xac_nhan",
          title: "Đặt hàng thành công",
          note: "Thanh toán qua VietQR.",
        },
        {
          timestamp: "2026-11-10T10:15:00+07:00",
          status: "da_giao",
          title: "Giao hàng thành công",
          note: "Đã giao tại Phường Cầu Giấy, Hà Nội.",
        },
        {
          timestamp: "2026-11-11T09:15:00+07:00",
          status: "yeu_cau_tra_hang",
          title: "Yêu cầu đổi size 2 chiều M sang L",
          note: "Đã gửi yêu cầu trong hạn 45 ngày, bưu tá sẽ mang áo size L tới đổi trực tiếp.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "HN-PK08-DEN-M",
          productId: "prod-polo-detkim",
          productSlug: "ao-polo-det-kim-min-den-than",
          productName: "Áo Polo Dệt kim mịn Đen than",
          colorName: "Đen than",
          size: "M",
          image: "/products/ao-polo-det-kim-den.png",
          unitPrice: 369000,
          quantity: 1,
        },
      ],
      shippingFee: 22000,
      shopVoucherDiscount: 15000,
      platformVoucherDiscount: 0,
      freeshipDiscount: 22000,
      coinDiscount: 0,
    }
  ),
  makeOrder(
    {
      id: "ord-261105-029",
      orderCode: "NM-261105-029",
      parentOrderCode: "NMP-261105-14",
      shopId: "shop-sai-gon",
      shopName: "Kho Basic Sài Gòn",
      buyerName: DEMO_USER.fullName,
      buyerPhone: DEMO_USER.phone,
      buyerEmail: DEMO_USER.email,
      shippingAddress: "Số 248 đường Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh",
      provinceCode: "HCM",
      status: "da_huy",
      paymentStatus: "cod_cho_thu",
      paymentMethod: "cod",
      isPaid: false,
      deliveryMethod: "standard",
      carrier: "SPX",
      coInspectionEnabled: true,
      createdAt: "2026-11-05T21:10:00+07:00",
      updatedAt: "2026-11-05T21:28:00+07:00",
      cancelReason: "Thay đổi địa chỉ nhận hàng sang Hà Nội.",
      notificationChannels: ["Email xác nhận"],
      timeline: [
        {
          timestamp: "2026-11-05T21:10:00+07:00",
          status: "cho_xac_nhan",
          title: "Đặt hàng thành công",
          note: "Đơn thanh toán COD.",
        },
        {
          timestamp: "2026-11-05T21:28:00+07:00",
          status: "da_huy",
          title: "Người mua hủy đơn",
          note: "Lý do: Thay đổi địa chỉ nhận hàng sang Hà Nội.",
        },
      ],
    },
    {
      items: [
        {
          skuCode: "SG-AK18-XAMTRO-L",
          productId: "prod-aokhoac-gio",
          productSlug: "ao-khoac-gio-nhe-gap-gon-xam-tro",
          productName: "Áo khoác Gió nhẹ gấp gọn Xám tro",
          colorName: "Xám tro",
          size: "L",
          image: "/products/ao-khoac-gio-xam-tro.png",
          unitPrice: 429000,
          quantity: 1,
        },
      ],
      shippingFee: 22000,
      shopVoucherDiscount: 0,
      platformVoucherDiscount: 0,
      freeshipDiscount: 22000,
      coinDiscount: 0,
    }
  ),
];
