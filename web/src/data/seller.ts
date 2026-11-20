import { PRODUCTS, getProductsByShop } from "./products";
import type { OrderStatus, PaymentMethod } from "./orders";

export interface SellerDailyMetric {
  date: string; // YYYY-MM-DD
  label: string; // dd/mm
  gmv: number; // integer VND
  orders: number;
  netRevenue: number; // integer VND (after 5% platform fee & refunds)
  visitors: number;
  refundAmount: number; // integer VND
  annotation?: string;
}

export interface SellerOrderRecord {
  id: string;
  orderCode: string;
  shopId: string;
  buyerName: string;
  buyerPhone: string;
  provinceName: string;
  wardName: string;
  createdAt: string;
  slaDeadline: string;
  status: OrderStatus;
  paymentMethod: PaymentMethod;
  isPaid: boolean;
  itemSummary: string;
  skuCount: number;
  quantity: number;
  subtotal: number;
  shopVoucherDiscount: number;
  platformFee: number; // 5% of (subtotal - shopVoucherDiscount)
  netReceivable: number; // subtotal - shopVoucherDiscount - platformFee
}

export type LedgerEntryType = "order_credit" | "platform_fee" | "refund_debit" | "payout_out";

export type LedgerSettlementState = "settled" | "pending";

export interface SellerLedgerEntry {
  id: string;
  entryCode: string;
  shopId: string;
  date: string;
  type: LedgerEntryType;
  typeLabel: string;
  referenceCode: string;
  description: string;
  debit: number; // integer VND >= 0 (tiền ra / khấu trừ)
  credit: number; // integer VND >= 0 (tiền vào)
  state: LedgerSettlementState;
}

export interface SellerPromotionRecord {
  id: string;
  shopId: string;
  kind: "voucher" | "flash_sale";
  codeOrSlot: string;
  title: string;
  discountLabel: string;
  discountType?: "fixed" | "percent";
  discountValue?: number;
  maxDiscount?: number;
  minOrderValue?: number;
  status: "dang_chay" | "sap_dien_ra" | "da_ket_thuc";
  usedQuota: number;
  totalQuota: number;
  attributedRevenue: number; // integer VND
  periodLabel: string;
}

export interface LowStockSkuAlert {
  shopId: string;
  productId: string;
  productSlug: string;
  productName: string;
  skuCode: string;
  colorName: string;
  size: string;
  stock: number;
  price: number;
  image: string;
}

export interface SellerWorkspaceMetrics {
  shopId: string;
  daily7d: SellerDailyMetric[];
  prior7dGmv: number;
  prior7dOrders: number;
  prior7dNetRevenue: number;
  total7dGmv: number;
  total7dOrders: number;
  total7dNetRevenue: number;
  total7dVisitors: number;
  conversionRate: number; // orders / visitors
  avgOrderValue: number; // Math.round(total7dGmv / total7dOrders)
  gmvDelta: number;
  ordersDelta: number;
  netDelta: number;
  orders: SellerOrderRecord[];
  ledger: SellerLedgerEntry[];
  availableBalance: number; // sum(settled credit - settled debit)
  pendingBalance: number; // sum(pending credit - pending debit)
  totalPaidOut: number; // sum(payout_out debit)
  totalFees: number; // sum(platform_fee debit)
  promotions: SellerPromotionRecord[];
  lowStockAlerts: LowStockSkuAlert[];
}

function makeSellerOrder(
  base: Omit<SellerOrderRecord, "platformFee" | "netReceivable">,
  commissionRate = 0.05
): SellerOrderRecord {
  const taxable = Math.max(0, base.subtotal - base.shopVoucherDiscount);
  const platformFee = Math.round(taxable * commissionRate);
  const netReceivable = taxable - platformFee;
  return {
    ...base,
    platformFee,
    netReceivable,
  };
}

const BACH_DANG_DAILY_7D: SellerDailyMetric[] = [
  {
    date: "2026-11-14",
    label: "14/11",
    gmv: 5420000,
    orders: 19,
    netRevenue: 5149000,
    visitors: 612,
    refundAmount: 0,
  },
  {
    date: "2026-11-15",
    label: "15/11",
    gmv: 6180000,
    orders: 22,
    netRevenue: 5642000,
    visitors: 694,
    refundAmount: 229000,
  },
  {
    date: "2026-11-16",
    label: "16/11",
    gmv: 5890000,
    orders: 21,
    netRevenue: 5595500,
    visitors: 655,
    refundAmount: 0,
  },
  {
    date: "2026-11-17",
    label: "17/11",
    gmv: 6740000,
    orders: 24,
    netRevenue: 6403000,
    visitors: 742,
    refundAmount: 0,
  },
  {
    date: "2026-11-18",
    label: "18/11",
    gmv: 9320000,
    orders: 34,
    netRevenue: 8854000,
    visitors: 1024,
    refundAmount: 0,
    annotation: "Flash sale 12:00",
  },
  {
    date: "2026-11-19",
    label: "19/11",
    gmv: 7450000,
    orders: 26,
    netRevenue: 6898500,
    visitors: 818,
    refundAmount: 179000,
  },
  {
    date: "2026-11-20",
    label: "20/11",
    gmv: 5680000,
    orders: 20,
    netRevenue: 5396000,
    visitors: 645,
    refundAmount: 0,
  },
];

const BACH_DANG_ORDERS: SellerOrderRecord[] = [
  makeSellerOrder({
    id: "so-bd-1",
    orderCode: "NM-261120-104",
    shopId: "shop-bach-dang",
    buyerName: "Nguyễn Minh Quân",
    buyerPhone: "0912 486 305",
    provinceName: "Thành phố Hồ Chí Minh",
    wardName: "Phường Xuân Hòa",
    createdAt: "2026-11-20T01:40:00+07:00",
    slaDeadline: "2026-11-20T18:00:00+07:00",
    status: "cho_xac_nhan",
    paymentMethod: "vietqr",
    isPaid: true,
    itemSummary: "Áo thun Cotton Trắng · L (x2)",
    skuCount: 1,
    quantity: 2,
    subtotal: 358000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-2",
    orderCode: "NM-261120-102",
    shopId: "shop-bach-dang",
    buyerName: "Trần Hải Đăng",
    buyerPhone: "0936 512 884",
    provinceName: "Thành phố Hà Nội",
    wardName: "Phường Đống Đa",
    createdAt: "2026-11-20T00:55:00+07:00",
    slaDeadline: "2026-11-20T18:00:00+07:00",
    status: "cho_xac_nhan",
    paymentMethod: "cod",
    isPaid: false,
    itemSummary: "Combo 3 Boxer Modal · XL (x2)",
    skuCount: 1,
    quantity: 2,
    subtotal: 458000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-3",
    orderCode: "NM-261119-096",
    shopId: "shop-bach-dang",
    buyerName: "Lê Thị Thu Hà",
    buyerPhone: "0978 204 119",
    provinceName: "Tỉnh Quảng Ninh",
    wardName: "Phường Bãi Cháy",
    createdAt: "2026-11-19T22:18:00+07:00",
    slaDeadline: "2026-11-20T12:00:00+07:00",
    status: "cho_xac_nhan",
    paymentMethod: "vi_sandbox",
    isPaid: true,
    itemSummary: "Áo thun Cotton Xám đá · M (x2), Áo thun Xanh than · L (x1)",
    skuCount: 2,
    quantity: 3,
    subtotal: 567000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-4",
    orderCode: "NM-261119-091",
    shopId: "shop-bach-dang",
    buyerName: "Phạm Quốc Khánh",
    buyerPhone: "0904 673 921",
    provinceName: "Thành phố Hải Phòng",
    wardName: "Phường Hồng Bàng",
    createdAt: "2026-11-19T16:40:00+07:00",
    slaDeadline: "2026-11-20T12:00:00+07:00",
    status: "dang_chuan_bi",
    paymentMethod: "vietqr",
    isPaid: true,
    itemSummary: "Áo thun Cotton Trắng · M (x1), Combo 3 Boxer Modal · L (x1)",
    skuCount: 2,
    quantity: 2,
    subtotal: 408000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-5",
    orderCode: "NM-261119-087",
    shopId: "shop-bach-dang",
    buyerName: "Vũ Đức Mạnh",
    buyerPhone: "0981 340 552",
    provinceName: "Tỉnh Bắc Ninh",
    wardName: "Phường Kinh Bắc",
    createdAt: "2026-11-19T11:15:00+07:00",
    slaDeadline: "2026-11-20T09:00:00+07:00",
    status: "dang_chuan_bi",
    paymentMethod: "cod",
    isPaid: false,
    itemSummary: "Áo thun Xanh than · XL (x2)",
    skuCount: 1,
    quantity: 2,
    subtotal: 378000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-6",
    orderCode: "NM-261118-079",
    shopId: "shop-bach-dang",
    buyerName: "Đặng Hoàng Nam",
    buyerPhone: "0915 882 410",
    provinceName: "Thành phố Đà Nẵng",
    wardName: "Phường Hải Châu",
    createdAt: "2026-11-18T13:02:00+07:00",
    slaDeadline: "2026-11-19T12:00:00+07:00",
    status: "dang_giao",
    paymentMethod: "vietqr",
    isPaid: true,
    itemSummary: "Áo thun Cotton Trắng · L (x3)",
    skuCount: 1,
    quantity: 3,
    subtotal: 537000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-7",
    orderCode: "NM-261117-071",
    shopId: "shop-bach-dang",
    buyerName: "Bùi Thị Thanh Mai",
    buyerPhone: "0963 729 104",
    provinceName: "Thành phố Cần Thơ",
    wardName: "Phường Ninh Kiều",
    createdAt: "2026-11-17T09:44:00+07:00",
    slaDeadline: "2026-11-18T12:00:00+07:00",
    status: "dang_giao",
    paymentMethod: "vi_sandbox",
    isPaid: true,
    itemSummary: "Áo thun Cotton Xám đá · S (x2)",
    skuCount: 1,
    quantity: 2,
    subtotal: 378000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-8",
    orderCode: "NM-261116-064",
    shopId: "shop-bach-dang",
    buyerName: "Ngô Văn Tùng",
    buyerPhone: "0909 418 776",
    provinceName: "Tỉnh Khánh Hòa",
    wardName: "Phường Nha Trang",
    createdAt: "2026-11-16T15:20:00+07:00",
    slaDeadline: "2026-11-17T12:00:00+07:00",
    status: "da_giao",
    paymentMethod: "vietqr",
    isPaid: true,
    itemSummary: "Combo 3 Boxer Modal · L (x2), Áo thun Trắng · L (x1)",
    skuCount: 2,
    quantity: 3,
    subtotal: 637000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-9",
    orderCode: "NM-261115-058",
    shopId: "shop-bach-dang",
    buyerName: "Hoàng Anh Tuấn",
    buyerPhone: "0945 190 362",
    provinceName: "Tỉnh Nghệ An",
    wardName: "Phường Trường Vinh",
    createdAt: "2026-11-15T18:05:00+07:00",
    slaDeadline: "2026-11-16T12:00:00+07:00",
    status: "da_giao",
    paymentMethod: "cod",
    isPaid: true,
    itemSummary: "Áo thun Xanh than · M (x1), Áo thun Xám đá · M (x1)",
    skuCount: 2,
    quantity: 2,
    subtotal: 378000,
    shopVoucherDiscount: 20000,
  }),
  makeSellerOrder({
    id: "so-bd-10",
    orderCode: "NM-261115-053",
    shopId: "shop-bach-dang",
    buyerName: "Đỗ Minh Hiếu",
    buyerPhone: "0932 611 408",
    provinceName: "Tỉnh Đồng Nai",
    wardName: "Phường Biên Hòa",
    createdAt: "2026-11-15T08:50:00+07:00",
    slaDeadline: "2026-11-16T09:00:00+07:00",
    status: "yeu_cau_tra_hang",
    paymentMethod: "vietqr",
    isPaid: true,
    itemSummary: "Áo thun Cotton Trắng · 2XL (x1)",
    skuCount: 1,
    quantity: 1,
    subtotal: 179000,
    shopVoucherDiscount: 0,
  }),
  makeSellerOrder({
    id: "so-bd-11",
    orderCode: "NM-261114-049",
    shopId: "shop-bach-dang",
    buyerName: "Phan Gia Huy",
    buyerPhone: "0971 504 298",
    provinceName: "Tỉnh Ninh Bình",
    wardName: "Phường Hoa Lư",
    createdAt: "2026-11-14T20:10:00+07:00",
    slaDeadline: "2026-11-15T12:00:00+07:00",
    status: "da_huy",
    paymentMethod: "cod",
    isPaid: false,
    itemSummary: "Áo thun Xanh than · S (x1)",
    skuCount: 1,
    quantity: 1,
    subtotal: 189000,
    shopVoucherDiscount: 0,
  }),
];

// Double-entry ledger for Xưởng Dệt Bạch Đằng:
// Available balance = sum(settled credit) - sum(settled debit)
// Pending balance = sum(pending credit) - sum(pending debit)
const BACH_DANG_LEDGER: SellerLedgerEntry[] = [
  {
    id: "led-1",
    entryCode: "SK-261114-01",
    shopId: "shop-bach-dang",
    date: "2026-11-14",
    type: "order_credit",
    typeLabel: "Doanh thu quyết toán",
    referenceCode: "LO-261114",
    description: "Quyết toán 19 đơn hoàn tất ngày 14/11",
    credit: 5420000,
    debit: 0,
    state: "settled",
  },
  {
    id: "led-2",
    entryCode: "SK-261114-02",
    shopId: "shop-bach-dang",
    date: "2026-11-14",
    type: "platform_fee",
    typeLabel: "Phí sàn 5%",
    referenceCode: "LO-261114",
    description: "Khấu trừ phí dịch vụ sàn kỳ 14/11",
    credit: 0,
    debit: 271000,
    state: "settled",
  },
  {
    id: "led-3",
    entryCode: "SK-261115-01",
    shopId: "shop-bach-dang",
    date: "2026-11-15",
    type: "order_credit",
    typeLabel: "Doanh thu quyết toán",
    referenceCode: "LO-261115",
    description: "Quyết toán 22 đơn hoàn tất ngày 15/11",
    credit: 6180000,
    debit: 0,
    state: "settled",
  },
  {
    id: "led-4",
    entryCode: "SK-261115-02",
    shopId: "shop-bach-dang",
    date: "2026-11-15",
    type: "platform_fee",
    typeLabel: "Phí sàn 5%",
    referenceCode: "LO-261115",
    description: "Khấu trừ phí dịch vụ sàn kỳ 15/11",
    credit: 0,
    debit: 309000,
    state: "settled",
  },
  {
    id: "led-5",
    entryCode: "SK-261115-03",
    shopId: "shop-bach-dang",
    date: "2026-11-15",
    type: "refund_debit",
    typeLabel: "Hoàn tiền đổi trả",
    referenceCode: "NM-261112-041",
    description: "Hoàn trả Combo 3 Boxer Modal sai cỡ",
    credit: 0,
    debit: 229000,
    state: "settled",
  },
  {
    id: "led-6",
    entryCode: "SK-261116-01",
    shopId: "shop-bach-dang",
    date: "2026-11-16",
    type: "order_credit",
    typeLabel: "Doanh thu quyết toán",
    referenceCode: "LO-261116",
    description: "Quyết toán 21 đơn hoàn tất ngày 16/11",
    credit: 5890000,
    debit: 0,
    state: "settled",
  },
  {
    id: "led-7",
    entryCode: "SK-261116-02",
    shopId: "shop-bach-dang",
    date: "2026-11-16",
    type: "platform_fee",
    typeLabel: "Phí sàn 5%",
    referenceCode: "LO-261116",
    description: "Khấu trừ phí dịch vụ sàn kỳ 16/11",
    credit: 0,
    debit: 294500,
    state: "settled",
  },
  {
    id: "led-7b",
    entryCode: "SK-261117-00",
    shopId: "shop-bach-dang",
    date: "2026-11-17",
    type: "order_credit",
    typeLabel: "Doanh thu quyết toán",
    referenceCode: "LO-261117",
    description: "Quyết toán 24 đơn hoàn tất ngày 17/11",
    credit: 6740000,
    debit: 0,
    state: "settled",
  },
  {
    id: "led-7c",
    entryCode: "SK-261117-00B",
    shopId: "shop-bach-dang",
    date: "2026-11-17",
    type: "platform_fee",
    typeLabel: "Phí sàn 5%",
    referenceCode: "LO-261117",
    description: "Khấu trừ phí dịch vụ sàn kỳ 17/11",
    credit: 0,
    debit: 337000,
    state: "settled",
  },
  {
    id: "led-8",
    entryCode: "SK-261117-01",
    shopId: "shop-bach-dang",
    date: "2026-11-17",
    type: "payout_out",
    typeLabel: "Rút tiền về ngân hàng",
    referenceCode: "PO-261117-BD",
    description: "Chuyển khoản Vietcombank *4821 kỳ đối soát đầu tuần",
    credit: 0,
    debit: 10000000,
    state: "settled",
  },
  {
    id: "led-9",
    entryCode: "SK-261118-01",
    shopId: "shop-bach-dang",
    date: "2026-11-18",
    type: "order_credit",
    typeLabel: "Doanh thu chờ đối soát",
    referenceCode: "LO-261118",
    description: "34 đơn phiên Flash sale 18/11 (giữ 3 ngày sau giao)",
    credit: 9320000,
    debit: 0,
    state: "pending",
  },
  {
    id: "led-10",
    entryCode: "SK-261118-02",
    shopId: "shop-bach-dang",
    date: "2026-11-18",
    type: "platform_fee",
    typeLabel: "Phí sàn 5% dự tính",
    referenceCode: "LO-261118",
    description: "Phí dịch vụ sàn cho kỳ 18/11",
    credit: 0,
    debit: 466000,
    state: "pending",
  },
  {
    id: "led-11",
    entryCode: "SK-261119-01",
    shopId: "shop-bach-dang",
    date: "2026-11-19",
    type: "order_credit",
    typeLabel: "Doanh thu chờ đối soát",
    referenceCode: "LO-261119",
    description: "26 đơn phát sinh ngày 19/11",
    credit: 7450000,
    debit: 0,
    state: "pending",
  },
  {
    id: "led-12",
    entryCode: "SK-261119-02",
    shopId: "shop-bach-dang",
    date: "2026-11-19",
    type: "platform_fee",
    typeLabel: "Phí sàn 5% dự tính",
    referenceCode: "LO-261119",
    description: "Phí dịch vụ sàn cho kỳ 19/11",
    credit: 0,
    debit: 372500,
    state: "pending",
  },
  {
    id: "led-13",
    entryCode: "SK-261119-03",
    shopId: "shop-bach-dang",
    date: "2026-11-19",
    type: "refund_debit",
    typeLabel: "Tạm giữ hoàn tiền",
    referenceCode: "NM-261115-053",
    description: "Tạm giữ chờ xác minh đơn đổi size 2XL",
    credit: 0,
    debit: 179000,
    state: "pending",
  },
];

const BACH_DANG_PROMOS: SellerPromotionRecord[] = [
  {
    id: "sp-1",
    shopId: "shop-bach-dang",
    kind: "voucher",
    codeOrSlot: "BACHDANG20",
    title: "Giảm 20.000₫ đơn từ 300.000₫",
    discountLabel: "20.000₫",
    discountType: "fixed",
    discountValue: 20000,
    minOrderValue: 300000,
    status: "dang_chay",
    usedQuota: 76,
    totalQuota: 120,
    attributedRevenue: 19480000,
    periodLabel: "10/11 - 28/11",
  },
  {
    id: "sp-5",
    shopId: "shop-bach-dang",
    kind: "voucher",
    codeOrSlot: "BACHDANG10P",
    title: "Giảm 10% (tối đa 40.000₫) dòng Cotton Compact",
    discountLabel: "10%",
    discountType: "percent",
    discountValue: 10,
    maxDiscount: 40000,
    minOrderValue: 399000,
    status: "dang_chay",
    usedQuota: 48,
    totalQuota: 100,
    attributedRevenue: 14820000,
    periodLabel: "12/11 - 30/11",
  },
  {
    id: "sp-6",
    shopId: "shop-bach-dang",
    kind: "voucher",
    codeOrSlot: "BACHDANGNEW",
    title: "Giảm 15.000₫ cho khách mua lần đầu tại xưởng",
    discountLabel: "15.000₫",
    discountType: "fixed",
    discountValue: 15000,
    minOrderValue: 199000,
    status: "dang_chay",
    usedQuota: 64,
    totalQuota: 150,
    attributedRevenue: 11960000,
    periodLabel: "01/11 - 30/11",
  },
  {
    id: "sp-7",
    shopId: "shop-bach-dang",
    kind: "voucher",
    codeOrSlot: "BACHDANGVIP50",
    title: "Giảm 50.000₫ đơn từ 699.000₫ cho khách quay lại",
    discountLabel: "50.000₫",
    discountType: "fixed",
    discountValue: 50000,
    minOrderValue: 699000,
    status: "dang_chay",
    usedQuota: 19,
    totalQuota: 60,
    attributedRevenue: 13680000,
    periodLabel: "15/11 - 28/11",
  },
  {
    id: "sp-2",
    shopId: "shop-bach-dang",
    kind: "flash_sale",
    codeOrSlot: "12:00 · 20/11",
    title: "Flash sale Áo thun Cotton Compact Trắng",
    discountLabel: "149.000₫ (-32%)",
    status: "dang_chay",
    usedQuota: 92,
    totalQuota: 120,
    attributedRevenue: 13708000,
    periodLabel: "12:00 - 18:00 hôm nay",
  },
  {
    id: "sp-3",
    shopId: "shop-bach-dang",
    kind: "flash_sale",
    codeOrSlot: "12:00 · 20/11",
    title: "Flash sale Combo 3 Boxer Modal",
    discountLabel: "189.000₫ (-30%)",
    status: "dang_chay",
    usedQuota: 103,
    totalQuota: 150,
    attributedRevenue: 19467000,
    periodLabel: "12:00 - 18:00 hôm nay",
  },
  {
    id: "sp-4",
    shopId: "shop-bach-dang",
    kind: "voucher",
    codeOrSlot: "BACHDANGCOMBO",
    title: "Giảm 35.000₫ khi mua từ 3 sản phẩm",
    discountLabel: "35.000₫",
    discountType: "fixed",
    discountValue: 35000,
    minOrderValue: 450000,
    status: "sap_dien_ra",
    usedQuota: 0,
    totalQuota: 100,
    attributedRevenue: 0,
    periodLabel: "22/11 - 30/11",
  },
];

export function computeLowStockAlerts(shopId?: string, threshold = 5): LowStockSkuAlert[] {
  const source = shopId ? getProductsByShop(shopId) : PRODUCTS;
  const alerts: LowStockSkuAlert[] = [];
  for (const p of source) {
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

function buildWorkspaceMetrics(
  shopId: string,
  daily7d: SellerDailyMetric[],
  prior7dGmv: number,
  prior7dOrders: number,
  prior7dNetRevenue: number,
  orders: SellerOrderRecord[],
  ledger: SellerLedgerEntry[],
  promotions: SellerPromotionRecord[]
): SellerWorkspaceMetrics {
  const total7dGmv = daily7d.reduce((s, d) => s + d.gmv, 0);
  const total7dOrders = daily7d.reduce((s, d) => s + d.orders, 0);
  const total7dNetRevenue = daily7d.reduce((s, d) => s + d.netRevenue, 0);
  const total7dVisitors = daily7d.reduce((s, d) => s + d.visitors, 0);
  const conversionRate = total7dVisitors > 0 ? total7dOrders / total7dVisitors : 0;
  const avgOrderValue = total7dOrders > 0 ? Math.round(total7dGmv / total7dOrders) : 0;

  const availableBalance = ledger
    .filter((e) => e.state === "settled")
    .reduce((s, e) => s + e.credit - e.debit, 0);
  const pendingBalance = ledger
    .filter((e) => e.state === "pending")
    .reduce((s, e) => s + e.credit - e.debit, 0);
  const totalPaidOut = ledger
    .filter((e) => e.type === "payout_out")
    .reduce((s, e) => s + e.debit, 0);
  const totalFees = ledger
    .filter((e) => e.type === "platform_fee")
    .reduce((s, e) => s + e.debit, 0);

  return {
    shopId,
    daily7d,
    prior7dGmv,
    prior7dOrders,
    prior7dNetRevenue,
    total7dGmv,
    total7dOrders,
    total7dNetRevenue,
    total7dVisitors,
    conversionRate,
    avgOrderValue,
    gmvDelta: total7dGmv - prior7dGmv,
    ordersDelta: total7dOrders - prior7dOrders,
    netDelta: total7dNetRevenue - prior7dNetRevenue,
    orders,
    ledger,
    availableBalance,
    pendingBalance,
    totalPaidOut,
    totalFees,
    promotions,
    lowStockAlerts: computeLowStockAlerts(shopId),
  };
}

function scaleDaily(factor: number): SellerDailyMetric[] {
  return BACH_DANG_DAILY_7D.map((d) => {
    const gmv = Math.round((d.gmv * factor) / 1000) * 1000;
    const orders = Math.max(1, Math.round(d.orders * factor));
    const refundAmount =
      d.refundAmount > 0 ? Math.round((d.refundAmount * factor) / 1000) * 1000 : 0;
    const netRevenue = Math.round(gmv * 0.95) - refundAmount;
    const visitors = Math.max(orders * 10, Math.round(d.visitors * factor));
    return { ...d, gmv, orders, netRevenue, visitors, refundAmount };
  });
}

export const SELLER_WORKSPACES: Record<string, SellerWorkspaceMetrics> = {
  "shop-bach-dang": buildWorkspaceMetrics(
    "shop-bach-dang",
    BACH_DANG_DAILY_7D,
    41250000,
    148,
    38920000,
    BACH_DANG_ORDERS,
    BACH_DANG_LEDGER,
    BACH_DANG_PROMOS
  ),
  "shop-sai-gon": buildWorkspaceMetrics(
    "shop-sai-gon",
    scaleDaily(1.18),
    49800000,
    176,
    46940000,
    BACH_DANG_ORDERS.slice(0, 6).map((o, idx) =>
      makeSellerOrder({
        ...o,
        id: `so-sg-${idx + 1}`,
        orderCode: o.orderCode.replace("NM-", "SG-"),
        shopId: "shop-sai-gon",
        itemSummary:
          idx % 2 === 0 ? "Áo thun Dry-Fit Cổ vịt · L (x2)" : "Quần Short Daily Be · L (x1)",
        subtotal: idx % 2 === 0 ? 318000 : 249000,
        shopVoucherDiscount: idx % 2 === 0 ? 0 : 0,
      })
    ),
    BACH_DANG_LEDGER.map((e, idx) => ({
      ...e,
      id: `led-sg-${idx + 1}`,
      shopId: "shop-sai-gon",
      credit: Math.round((e.credit * 1.15) / 1000) * 1000,
      debit: Math.round((e.debit * 1.15) / 1000) * 1000,
    })),
    [
      {
        id: "sp-sg-1",
        shopId: "shop-sai-gon",
        kind: "voucher",
        codeOrSlot: "SAIGON25",
        title: "Giảm 25.000₫ đơn từ 350.000₫",
        discountLabel: "25.000₫",
        discountType: "fixed",
        discountValue: 25000,
        minOrderValue: 350000,
        status: "dang_chay",
        usedQuota: 89,
        totalQuota: 150,
        attributedRevenue: 26420000,
        periodLabel: "10/11 - 28/11",
      },
      {
        id: "sp-sg-3",
        shopId: "shop-sai-gon",
        kind: "voucher",
        codeOrSlot: "SAIGONRUN10",
        title: "Giảm 10% (tối đa 35.000₫) nhóm đồ chạy bộ Dry-Fit",
        discountLabel: "10%",
        discountType: "percent",
        discountValue: 10,
        maxDiscount: 35000,
        minOrderValue: 299000,
        status: "dang_chay",
        usedQuota: 54,
        totalQuota: 120,
        attributedRevenue: 15940000,
        periodLabel: "12/11 - 30/11",
      },
      {
        id: "sp-sg-4",
        shopId: "shop-sai-gon",
        kind: "voucher",
        codeOrSlot: "SAIGONCOMBO40",
        title: "Giảm 40.000₫ khi mua từ 3 sản phẩm thể thao",
        discountLabel: "40.000₫",
        discountType: "fixed",
        discountValue: 40000,
        minOrderValue: 550000,
        status: "dang_chay",
        usedQuota: 27,
        totalQuota: 80,
        attributedRevenue: 16470000,
        periodLabel: "14/11 - 30/11",
      },
      {
        id: "sp-sg-2",
        shopId: "shop-sai-gon",
        kind: "flash_sale",
        codeOrSlot: "12:00 · 20/11",
        title: "Flash sale Áo thun Dry-Fit Cổ vịt",
        discountLabel: "139.000₫ (-30%)",
        status: "dang_chay",
        usedQuota: 59,
        totalQuota: 100,
        attributedRevenue: 8201000,
        periodLabel: "12:00 - 18:00 hôm nay",
      },
    ]
  ),
  "shop-da-nang": buildWorkspaceMetrics(
    "shop-da-nang",
    scaleDaily(0.86),
    36100000,
    129,
    33950000,
    BACH_DANG_ORDERS.slice(0, 5).map((o, idx) =>
      makeSellerOrder({
        ...o,
        id: `so-dn-${idx + 1}`,
        orderCode: o.orderCode.replace("NM-", "DN-"),
        shopId: "shop-da-nang",
        itemSummary:
          idx % 2 === 0 ? "Áo Polo Pique Kem · L (x1)" : "Quần Short Linen Xám đá · M (x2)",
        subtotal: idx % 2 === 0 ? 289000 : 558000,
        shopVoucherDiscount: idx % 2 === 0 ? 0 : 30000,
      })
    ),
    BACH_DANG_LEDGER.map((e, idx) => ({
      ...e,
      id: `led-dn-${idx + 1}`,
      shopId: "shop-da-nang",
      credit: Math.round((e.credit * 0.85) / 1000) * 1000,
      debit: Math.round((e.debit * 0.85) / 1000) * 1000,
    })),
    [
      {
        id: "sp-dn-1",
        shopId: "shop-da-nang",
        kind: "voucher",
        codeOrSlot: "DANANG30",
        title: "Giảm 30.000₫ đơn từ 450.000₫",
        discountLabel: "30.000₫",
        discountType: "fixed",
        discountValue: 30000,
        minOrderValue: 450000,
        status: "dang_chay",
        usedQuota: 72,
        totalQuota: 100,
        attributedRevenue: 24180000,
        periodLabel: "10/11 - 28/11",
      },
      {
        id: "sp-dn-2",
        shopId: "shop-da-nang",
        kind: "voucher",
        codeOrSlot: "DANANGLINEN10",
        title: "Giảm 10% (tối đa 45.000₫) nhóm Polo & Linen",
        discountLabel: "10%",
        discountType: "percent",
        discountValue: 10,
        maxDiscount: 45000,
        minOrderValue: 399000,
        status: "dang_chay",
        usedQuota: 38,
        totalQuota: 90,
        attributedRevenue: 13260000,
        periodLabel: "12/11 - 30/11",
      },
      {
        id: "sp-dn-3",
        shopId: "shop-da-nang",
        kind: "voucher",
        codeOrSlot: "DANANGNEW20",
        title: "Giảm 20.000₫ cho khách lần đầu mua tại xưởng",
        discountLabel: "20.000₫",
        discountType: "fixed",
        discountValue: 20000,
        minOrderValue: 250000,
        status: "sap_dien_ra",
        usedQuota: 0,
        totalQuota: 100,
        attributedRevenue: 0,
        periodLabel: "22/11 - 30/11",
      },
    ]
  ),
  "shop-ha-noi": buildWorkspaceMetrics(
    "shop-ha-noi",
    scaleDaily(0.94),
    40400000,
    144,
    38120000,
    BACH_DANG_ORDERS.slice(0, 6).map((o, idx) =>
      makeSellerOrder({
        ...o,
        id: `so-hn-${idx + 1}`,
        orderCode: o.orderCode.replace("NM-", "HN-"),
        shopId: "shop-ha-noi",
        itemSummary:
          idx % 2 === 0 ? "Quần Jogger Terry Xám · L (x1)" : "Combo 5 Tất cổ trung · L (x2)",
        subtotal: idx % 2 === 0 ? 349000 : 258000,
        shopVoucherDiscount: 15000,
      })
    ),
    BACH_DANG_LEDGER.map((e, idx) => ({
      ...e,
      id: `led-hn-${idx + 1}`,
      shopId: "shop-ha-noi",
      credit: Math.round((e.credit * 0.92) / 1000) * 1000,
      debit: Math.round((e.debit * 0.92) / 1000) * 1000,
    })),
    [
      {
        id: "sp-hn-1",
        shopId: "shop-ha-noi",
        kind: "voucher",
        codeOrSlot: "HANOI15",
        title: "Giảm 15.000₫ đơn từ 220.000₫",
        discountLabel: "15.000₫",
        discountType: "fixed",
        discountValue: 15000,
        minOrderValue: 220000,
        status: "dang_chay",
        usedQuota: 67,
        totalQuota: 120,
        attributedRevenue: 16890000,
        periodLabel: "10/11 - 28/11",
      },
      {
        id: "sp-hn-2",
        shopId: "shop-ha-noi",
        kind: "voucher",
        codeOrSlot: "HANOIWARM35",
        title: "Giảm 35.000₫ cho đơn Jogger & Áo khoác gió",
        discountLabel: "35.000₫",
        discountType: "fixed",
        discountValue: 35000,
        minOrderValue: 499000,
        status: "dang_chay",
        usedQuota: 41,
        totalQuota: 90,
        attributedRevenue: 18450000,
        periodLabel: "14/11 - 30/11",
      },
      {
        id: "sp-hn-3",
        shopId: "shop-ha-noi",
        kind: "voucher",
        codeOrSlot: "HANOITAT10",
        title: "Giảm 10% (tối đa 25.000₫) khi mua từ 2 pack tất",
        discountLabel: "10%",
        discountType: "percent",
        discountValue: 10,
        maxDiscount: 25000,
        minOrderValue: 199000,
        status: "sap_dien_ra",
        usedQuota: 0,
        totalQuota: 100,
        attributedRevenue: 0,
        periodLabel: "22/11 - 30/11",
      },
    ]
  ),
};

export function getSellerWorkspace(shopId: string): SellerWorkspaceMetrics {
  return SELLER_WORKSPACES[shopId] ?? SELLER_WORKSPACES["shop-bach-dang"];
}

/* ============================================================================
   WAVE 2 SELLER CENTER EXTENSIONS (100% additive — existing ledger math intact)
   ============================================================================ */

export type SellerProductKind = "single_pack" | "combo_multi_size";

export interface SellerModelInfo {
  genderLabel: string;
  heightCm: number;
  weightKg: number;
  measurements: string;
  wearingSize: string;
  secondaryModel?: string;
}

export interface SellerProductExtendedMeta {
  productId: string;
  categoryPath: [string, string, string];
  shopCollection: string;
  materialGroup: string;
  fitType: string;
  occasions: string[];
  sport: string;
  fabricFeatures: string[];
  modelInfo: SellerModelInfo;
  carePresetIds: string[];
  productKind: SellerProductKind;
  comboSlots: string[];
  maxQtyPerOrder: number;
  isPreOrder: boolean;
  preOrderShipDate?: string;
  nonReturnable: boolean;
  nonReturnReason?: string;
  wishlistCount: number;
  backInStockSubscribers: Record<string, number>;
}

export interface CarePresetItem {
  id: string;
  code: string;
  label: string;
  detail: string;
}

export const TAXONOMY_L1_L3: Record<string, Array<[string, string, string]>> = {
  "ao-thun": [
    ["Đồ nam & Unisex", "Áo mặc hằng ngày", "Áo thun cổ tròn basic"],
    ["Đồ nam & Unisex", "Áo thể thao", "Áo thun dry-fit vận động"],
    ["Đồ nam & Unisex", "Áo mặc hằng ngày", "Áo thun phom rộng oversize"],
  ],
  "ao-polo": [
    ["Đồ nam & Unisex", "Áo công sở & thường ngày", "Áo polo pique mắt chim"],
    ["Đồ nam & Unisex", "Áo công sở & thường ngày", "Áo polo dệt kim mịn"],
  ],
  "quan-short": [
    ["Đồ nam & Unisex", "Quần ngắn", "Quần short cotton mặc hằng ngày"],
    ["Đồ nam & Unisex", "Quần thể thao", "Quần short chạy bộ 5 inch"],
    ["Đồ nam & Unisex", "Quần ngắn", "Quần short linen pha thoáng mát"],
  ],
  "quan-jogger": [
    ["Đồ nam & Unisex", "Quần dài", "Quần jogger nỉ chân cua"],
    ["Đồ nam & Unisex", "Quần dài", "Quần jogger vải co giãn công nghệ"],
  ],
  "do-lot": [
    ["Đồ nam & Unisex", "Đồ lót nam", "Combo boxer sợi tự nhiên"],
    ["Đồ nam & Unisex", "Đồ lót nam", "Combo brief sợi tre thoáng khí"],
  ],
  "tat-phu-kien": [
    ["Đồ nam & Unisex", "Tất & phụ kiện", "Combo tất cotton cổ trung"],
    ["Đồ nam & Unisex", "Tất & phụ kiện", "Combo tất thể thao cổ thấp"],
    ["Đồ nam & Unisex", "Áo khoác ngoài", "Áo khoác gió chống gió nhẹ"],
  ],
};

export const MATERIAL_OPTIONS = [
  "Cotton Compact chải kỹ",
  "Sợi Modal gỗ sồi",
  "Dry-Fit Microfiber",
  "Pique Cotton USA",
  "Linen pha Cotton",
  "Nỉ chân cua 320gsm",
  "Sợi tre Bamboo",
  "Vải dù trượt nước tái chế",
];

export const FIT_OPTIONS = [
  "Ôm gọn (Slim fit)",
  "Vừa vặn (Regular fit)",
  "Thoải mái (Relaxed fit)",
  "Rộng vai (Oversize)",
];

export const OCCASION_OPTIONS = [
  "Mặc hằng ngày",
  "Đi làm công sở",
  "Tập luyện thể thao",
  "Du lịch & dã ngoại",
  "Mặc nhà",
];

export const SPORT_OPTIONS = [
  "Không chuyên thể thao",
  "Chạy bộ",
  "Gym & Training",
  "Cầu lông / Pickleball",
  "Đạp xe",
];

export const FABRIC_FEATURE_OPTIONS = [
  "Thoáng khí",
  "Thấm hút mồ hôi",
  "Khô nhanh",
  "Co giãn 4 chiều",
  "Ít nhăn giữ phom",
  "Kháng mùi tự nhiên",
  "Chống nắng UPF 40+",
  "Chống gió nhẹ",
];

export const CARE_STANDARD_PRESETS: CarePresetItem[] = [
  {
    id: "wash_30",
    code: "30°C",
    label: "Giặt máy nhẹ ≤ 30°C",
    detail: "Chọn chu trình giặt nhẹ nước mát dưới 30 độ C để giữ độ bền sợi.",
  },
  {
    id: "turn_inside_out",
    code: "Lộn trái",
    label: "Lộn trái khi giặt",
    detail: "Lộn mặt trái sản phẩm trước khi giặt máy giúp giảm xù lông bề mặt.",
  },
  {
    id: "no_bleach",
    code: "Không tẩy",
    label: "Không dùng thuốc tẩy",
    detail: "Tránh chất tẩy clo mạnh làm bạc màu và suy giảm độ đàn hồi spandex.",
  },
  {
    id: "no_softener",
    code: "Không nước xả",
    label: "Không dùng nước xả vải",
    detail: "Nước xả bám màng lên sợi thể thao/modal làm giảm khả năng thoát ẩm.",
  },
  {
    id: "shade_dry",
    code: "Phơi râm",
    label: "Phơi nơi râm thoáng",
    detail: "Phơi ngang hoặc treo móc rộng vai trong bóng râm, tránh nắng gắt trực tiếp.",
  },
  {
    id: "iron_low",
    code: "Ủi ≤ 110°C",
    label: "Ủi nhiệt thấp ≤ 110°C",
    detail: "Ủi mặt trái ở mức nhiệt thấp hoặc dùng bàn ủi hơi nước.",
  },
];

export const SELLER_PRODUCT_META: Record<string, SellerProductExtendedMeta> = {
  "prod-at-trang": {
    productId: "prod-at-trang",
    categoryPath: ["Đồ nam & Unisex", "Áo mặc hằng ngày", "Áo thun cổ tròn basic"],
    shopCollection: "Cotton Compact 220gsm",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Mặc hằng ngày", "Đi làm công sở"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Thấm hút mồ hôi", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam & nữ (Unisex)",
      heightCm: 178,
      weightKg: 72,
      measurements: "96-78-94 cm",
      wearingSize: "L",
      secondaryModel: "Mẫu nữ 1m64 · 52kg · 82-64-88 cm · mặc M",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "no_bleach", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 10,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 184,
    backInStockSubscribers: {
      "BD-AT01-XANHTHAN-2XL": 27,
      "BD-AT01-TRANG-2XL": 11,
      "BD-AT01-XAMDA-2XL": 8,
    },
  },
  "prod-at-xamda": {
    productId: "prod-at-xamda",
    categoryPath: ["Đồ nam & Unisex", "Áo mặc hằng ngày", "Áo thun cổ tròn basic"],
    shopCollection: "Cotton Compact 220gsm",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Mặc hằng ngày", "Du lịch & dã ngoại"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Thấm hút mồ hôi", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 176,
      weightKg: 69,
      measurements: "95-76-92 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "no_bleach", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 10,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 132,
    backInStockSubscribers: {
      "BD-AT02-XANHTHAN-2XL": 15,
      "BD-AT02-TRANG-2XL": 6,
    },
  },
  "prod-at-xanhthan": {
    productId: "prod-at-xanhthan",
    categoryPath: ["Đồ nam & Unisex", "Áo mặc hằng ngày", "Áo thun cổ tròn basic"],
    shopCollection: "Cotton Compact 220gsm",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Mặc hằng ngày", "Đi làm công sở"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 178,
      weightKg: 72,
      measurements: "96-78-94 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "no_bleach", "shade_dry"],
    productKind: "combo_multi_size",
    comboSlots: ["Áo 1 (chọn màu & size)", "Áo 2 (chọn màu & size)"],
    maxQtyPerOrder: 5,
    isPreOrder: true,
    preOrderShipDate: "26/11/2026",
    nonReturnable: false,
    wishlistCount: 118,
    backInStockSubscribers: {
      "BD-AT03-XAMDA-2XL": 9,
    },
  },
  "prod-dolot-boxer": {
    productId: "prod-dolot-boxer",
    categoryPath: ["Đồ nam & Unisex", "Đồ lót nam", "Combo boxer sợi tự nhiên"],
    shopCollection: "Đồ lót Sợi Modal",
    materialGroup: "Sợi Modal gỗ sồi",
    fitType: "Ôm gọn (Slim fit)",
    occasions: ["Mặc hằng ngày", "Mặc nhà"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Co giãn 4 chiều", "Kháng mùi tự nhiên"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 177,
      weightKg: 71,
      measurements: "Eo 80 cm · Mông 94 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "no_bleach", "no_softener", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 4,
    isPreOrder: false,
    nonReturnable: true,
    nonReturnReason: "Pack đồ lót niêm phong vệ sinh, chỉ hỗ trợ khi còn nguyên seal",
    wishlistCount: 215,
    backInStockSubscribers: {},
  },
  "prod-at-dryfit": {
    productId: "prod-at-dryfit",
    categoryPath: ["Đồ nam & Unisex", "Áo thể thao", "Áo thun dry-fit vận động"],
    shopCollection: "Dry-Fit Chạy bộ",
    materialGroup: "Dry-Fit Microfiber",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Tập luyện thể thao", "Mặc hằng ngày"],
    sport: "Chạy bộ",
    fabricFeatures: ["Thoáng khí", "Khô nhanh", "Co giãn 4 chiều"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 175,
      weightKg: 67,
      measurements: "94-75-92 cm",
      wearingSize: "M",
    },
    carePresetIds: ["wash_30", "no_bleach", "no_softener", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 6,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 164,
    backInStockSubscribers: {
      "SG-DF04-THANCHI-2XL": 14,
    },
  },
  "prod-at-oversize": {
    productId: "prod-at-oversize",
    categoryPath: ["Đồ nam & Unisex", "Áo mặc hằng ngày", "Áo thun phom rộng oversize"],
    shopCollection: "Basic Phom Rộng",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Rộng vai (Oversize)",
    occasions: ["Mặc hằng ngày", "Du lịch & dã ngoại"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam & nữ (Unisex)",
      heightCm: 180,
      weightKg: 74,
      measurements: "98-80-95 cm",
      wearingSize: "XL",
      secondaryModel: "Mẫu nữ 1m65 · 50kg · mặc M",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 196,
    backInStockSubscribers: {
      "SG-OS05-TRANG-2XL": 31,
      "SG-OS05-THANCHI-2XL": 9,
    },
  },
  "prod-short-daily": {
    productId: "prod-short-daily",
    categoryPath: ["Đồ nam & Unisex", "Quần ngắn", "Quần short cotton mặc hằng ngày"],
    shopCollection: "Basic Sài Gòn Daily",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Mặc hằng ngày", "Du lịch & dã ngoại"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Co giãn 4 chiều"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 176,
      weightKg: 70,
      measurements: "Eo 81 cm · Mông 95 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "no_bleach", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 141,
    backInStockSubscribers: {
      "SG-SD09-XAMDA-2XL": 12,
    },
  },
  "prod-short-run": {
    productId: "prod-short-run",
    categoryPath: ["Đồ nam & Unisex", "Quần thể thao", "Quần short chạy bộ 5 inch"],
    shopCollection: "Dry-Fit Chạy bộ",
    materialGroup: "Dry-Fit Microfiber",
    fitType: "Ôm gọn (Slim fit)",
    occasions: ["Tập luyện thể thao"],
    sport: "Chạy bộ",
    fabricFeatures: ["Khô nhanh", "Thoáng khí", "Co giãn 4 chiều"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 175,
      weightKg: 66,
      measurements: "Eo 77 cm · Mông 92 cm",
      wearingSize: "M",
    },
    carePresetIds: ["wash_30", "no_softener", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 109,
    backInStockSubscribers: {},
  },
  "prod-aokhoac-gio": {
    productId: "prod-aokhoac-gio",
    categoryPath: ["Đồ nam & Unisex", "Áo khoác ngoài", "Áo khoác gió chống gió nhẹ"],
    shopCollection: "Dry-Fit Chạy bộ",
    materialGroup: "Vải dù trượt nước tái chế",
    fitType: "Thoải mái (Relaxed fit)",
    occasions: ["Tập luyện thể thao", "Du lịch & dã ngoại"],
    sport: "Chạy bộ",
    fabricFeatures: ["Chống gió nhẹ", "Chống nắng UPF 40+", "Khô nhanh"],
    modelInfo: {
      genderLabel: "Mẫu nam & nữ (Unisex)",
      heightCm: 178,
      weightKg: 72,
      measurements: "96-78-94 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "no_bleach", "no_softener", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 3,
    isPreOrder: true,
    preOrderShipDate: "29/11/2026",
    nonReturnable: false,
    wishlistCount: 152,
    backInStockSubscribers: {
      "SG-AK18-XAMTRO-2XL": 19,
    },
  },
  "prod-polo-kem": {
    productId: "prod-polo-kem",
    categoryPath: ["Đồ nam & Unisex", "Áo công sở & thường ngày", "Áo polo pique mắt chim"],
    shopCollection: "Polo & Linen Tự Nhiên",
    materialGroup: "Pique Cotton USA",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Đi làm công sở", "Mặc hằng ngày"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Thấm hút mồ hôi", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 177,
      weightKg: 70,
      measurements: "95-78-93 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "no_bleach", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 6,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 147,
    backInStockSubscribers: {
      "DN-PL06-DEN-2XL": 16,
      "DN-PL06-XANHDA-2XL": 10,
    },
  },
  "prod-polo-xanhda": {
    productId: "prod-polo-xanhda",
    categoryPath: ["Đồ nam & Unisex", "Áo công sở & thường ngày", "Áo polo pique mắt chim"],
    shopCollection: "Polo & Linen Tự Nhiên",
    materialGroup: "Pique Cotton USA",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Đi làm công sở", "Mặc hằng ngày"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 177,
      weightKg: 70,
      measurements: "95-78-93 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 6,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 121,
    backInStockSubscribers: {
      "DN-PL07-KEM-2XL": 11,
    },
  },
  "prod-short-linen": {
    productId: "prod-short-linen",
    categoryPath: ["Đồ nam & Unisex", "Quần ngắn", "Quần short linen pha thoáng mát"],
    shopCollection: "Polo & Linen Tự Nhiên",
    materialGroup: "Linen pha Cotton",
    fitType: "Thoải mái (Relaxed fit)",
    occasions: ["Du lịch & dã ngoại", "Mặc hằng ngày"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Thấm hút mồ hôi"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 176,
      weightKg: 68,
      measurements: "Eo 79 cm · Mông 93 cm",
      wearingSize: "M",
    },
    carePresetIds: ["wash_30", "no_bleach", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 134,
    backInStockSubscribers: {
      "DN-SL11-XAMDA-2XL": 13,
      "DN-SL11-BE-2XL": 7,
    },
  },
  "prod-dolot-brief": {
    productId: "prod-dolot-brief",
    categoryPath: ["Đồ nam & Unisex", "Đồ lót nam", "Combo brief sợi tre thoáng khí"],
    shopCollection: "Sợi Tre Thiên Nhiên",
    materialGroup: "Sợi tre Bamboo",
    fitType: "Ôm gọn (Slim fit)",
    occasions: ["Mặc hằng ngày", "Mặc nhà"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Kháng mùi tự nhiên", "Thoáng khí", "Co giãn 4 chiều"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 175,
      weightKg: 68,
      measurements: "Eo 78 cm · Mông 92 cm",
      wearingSize: "M",
    },
    carePresetIds: ["wash_30", "no_bleach", "no_softener", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 4,
    isPreOrder: false,
    nonReturnable: true,
    nonReturnReason: "Pack đồ lót niêm phong không hỗ trợ đổi trả sau khi bóc hộp",
    wishlistCount: 112,
    backInStockSubscribers: {},
  },
  "prod-polo-detkim": {
    productId: "prod-polo-detkim",
    categoryPath: ["Đồ nam & Unisex", "Áo công sở & thường ngày", "Áo polo dệt kim mịn"],
    shopCollection: "Thu Đông Dệt Kim",
    materialGroup: "Pique Cotton USA",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Đi làm công sở"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Ít nhăn giữ phom", "Thoáng khí"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 179,
      weightKg: 73,
      measurements: "97-79-95 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "shade_dry", "iron_low"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 176,
    backInStockSubscribers: {
      "HN-PK08-XANHDA-2XL": 18,
      "HN-PK08-DEN-2XL": 14,
    },
  },
  "prod-jogger-terry": {
    productId: "prod-jogger-terry",
    categoryPath: ["Đồ nam & Unisex", "Quần dài", "Quần jogger nỉ chân cua"],
    shopCollection: "Thu Đông Dệt Kim",
    materialGroup: "Nỉ chân cua 320gsm",
    fitType: "Thoải mái (Relaxed fit)",
    occasions: ["Mặc hằng ngày", "Mặc nhà"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Ít nhăn giữ phom"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 178,
      weightKg: 72,
      measurements: "Eo 81 cm · Mông 95 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "no_bleach", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 168,
    backInStockSubscribers: {
      "HN-JT12-DEN-2XL": 12,
    },
  },
  "prod-jogger-tech": {
    productId: "prod-jogger-tech",
    categoryPath: ["Đồ nam & Unisex", "Quần dài", "Quần jogger vải co giãn công nghệ"],
    shopCollection: "Thu Đông Dệt Kim",
    materialGroup: "Dry-Fit Microfiber",
    fitType: "Ôm gọn (Slim fit)",
    occasions: ["Tập luyện thể thao", "Du lịch & dã ngoại"],
    sport: "Gym & Training",
    fabricFeatures: ["Co giãn 4 chiều", "Khô nhanh", "Chống gió nhẹ"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 178,
      weightKg: 72,
      measurements: "Eo 80 cm · Mông 94 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "no_softener", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 5,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 159,
    backInStockSubscribers: {
      "HN-JK13-XAM-2XL": 22,
    },
  },
  "prod-tat-cotrung": {
    productId: "prod-tat-cotrung",
    categoryPath: ["Đồ nam & Unisex", "Tất & phụ kiện", "Combo tất cotton cổ trung"],
    shopCollection: "Phụ kiện Cotton Gân",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Vừa vặn (Regular fit)",
    occasions: ["Mặc hằng ngày", "Đi làm công sở"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thấm hút mồ hôi", "Kháng mùi tự nhiên"],
    modelInfo: {
      genderLabel: "Unisex (cỡ chân 38-44)",
      heightCm: 174,
      weightKg: 66,
      measurements: "Cỡ giày 41",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "no_bleach", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 6,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 104,
    backInStockSubscribers: {},
  },
  "prod-tat-cothap": {
    productId: "prod-tat-cothap",
    categoryPath: ["Đồ nam & Unisex", "Tất & phụ kiện", "Combo tất thể thao cổ thấp"],
    shopCollection: "Phụ kiện Cotton Gân",
    materialGroup: "Cotton Compact chải kỹ",
    fitType: "Ôm gọn (Slim fit)",
    occasions: ["Tập luyện thể thao", "Mặc hằng ngày"],
    sport: "Chạy bộ",
    fabricFeatures: ["Thấm hút mồ hôi", "Khô nhanh"],
    modelInfo: {
      genderLabel: "Unisex (cỡ chân 38-44)",
      heightCm: 174,
      weightKg: 66,
      measurements: "Cỡ giày 41",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "no_bleach", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 6,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 92,
    backInStockSubscribers: {},
  },
};

export function getDefaultProductMeta(
  productId: string,
  categorySlug = "ao-thun"
): SellerProductExtendedMeta {
  const found = SELLER_PRODUCT_META[productId];
  if (found) return found;
  const catPaths = TAXONOMY_L1_L3[categorySlug] ?? TAXONOMY_L1_L3["ao-thun"];
  return {
    productId,
    categoryPath: catPaths[0],
    shopCollection: "Bộ sưu tập cơ bản",
    materialGroup: MATERIAL_OPTIONS[0],
    fitType: FIT_OPTIONS[1],
    occasions: ["Mặc hằng ngày"],
    sport: "Không chuyên thể thao",
    fabricFeatures: ["Thoáng khí", "Thấm hút mồ hôi"],
    modelInfo: {
      genderLabel: "Mẫu nam",
      heightCm: 176,
      weightKg: 70,
      measurements: "95-78-93 cm",
      wearingSize: "L",
    },
    carePresetIds: ["wash_30", "turn_inside_out", "no_bleach", "shade_dry"],
    productKind: "single_pack",
    comboSlots: [],
    maxQtyPerOrder: 10,
    isPreOrder: false,
    nonReturnable: false,
    wishlistCount: 0,
    backInStockSubscribers: {},
  };
}

/* ---------- Wave 2 Order Operations: Gift notes, VAT, Late Cancel/Address, 2-Way Exchange, Tier-1 Complaints ---------- */

export type VatInvoiceStatus =
  "chua_yeu_cau" | "cho_duyet_nhap" | "da_gui_nhap_24h" | "da_phat_hanh";

export interface SellerOrderVatRequest {
  companyName: string;
  taxCode: string;
  email: string;
  companyAddress: string;
  requestedAt: string;
  status: VatInvoiceStatus;
  draftDeadline24h?: string;
  issuedInvoiceNo?: string;
}

export interface SellerOrderAmendmentRequest {
  id: string;
  kind: "huy_muon" | "doi_dia_chi";
  requestedAt: string;
  reason: string;
  proposedAddress?: string;
  status: "cho_duyet" | "da_duyet" | "tu_choi";
  resolutionNote?: string;
}

export type TwoWayCourierStatus =
  "cho_dieu_phoi" | "da_dat_buu_ta" | "dang_doi_2_chieu" | "hoan_tat";

export interface SellerTwoWayExchange {
  id: string;
  requestedAt: string;
  fromVariant: string;
  toVariant: string;
  reason: string;
  courierStatus: TwoWayCourierStatus;
  courierPartner: string;
  exchangeWaybill?: string;
  pickupWindow: string;
  policyNote: string;
}

export interface SellerTier1Complaint {
  id: string;
  ticketCode: string;
  openedAt: string;
  slaDeadline: string;
  slaRemainingLabel: string;
  category: string;
  buyerMessage: string;
  status: "cho_phan_hoi" | "da_phan_hoi" | "da_xu_ly" | "chuyen_san";
  sellerResponse?: string;
}

export interface SellerOrderWave2Extra {
  orderCode: string;
  deliveryNote?: string;
  giftCardMessage?: string;
  giftBagRequested: boolean;
  vatRequest?: SellerOrderVatRequest;
  amendment?: SellerOrderAmendmentRequest;
  twoWayExchange?: SellerTwoWayExchange;
  tier1Complaint?: SellerTier1Complaint;
}

export const SELLER_ORDER_EXTRAS: Record<string, SellerOrderWave2Extra> = {
  "NM-261120-104": {
    orderCode: "NM-261120-104",
    deliveryNote: "Giao giờ hành chính 9:00-17:00, gọi trước 15 phút khi tới sảnh tòa nhà.",
    giftCardMessage: "Chúc anh Minh sinh nhật nhiều sức khỏe và luôn gọn gàng, thoải mái mỗi ngày.",
    giftBagRequested: true,
    vatRequest: {
      companyName: "Công ty TNHH Giải pháp Phần mềm Nam Việt",
      taxCode: "0316482910",
      email: "ketoan@namvietsoft.vn",
      companyAddress: "Tầng 6, 128 Nguyễn Thị Minh Khai, Phường Xuân Hòa, TP. Hồ Chí Minh",
      requestedAt: "2026-11-20T01:45:00+07:00",
      status: "cho_duyet_nhap",
      draftDeadline24h: "2026-11-21T01:45:00+07:00",
    },
  },
  "NM-261119-096": {
    orderCode: "NM-261119-096",
    deliveryNote: "Kiểm tra kỹ đường may cổ áo trước khi niêm phong gói hàng.",
    giftCardMessage: "Gửi tặng anh trai nhân dịp chuyển công tác mới.",
    giftBagRequested: true,
  },
  "NM-261119-091": {
    orderCode: "NM-261119-091",
    deliveryNote: "Bọc túi chống nước vì khu vực cảng đang mưa.",
    giftBagRequested: false,
    vatRequest: {
      companyName: "Công ty Cổ phần Logistics Hải Đăng",
      taxCode: "0201945821",
      email: "hoadon@haidanglog.vn",
      companyAddress: "Số 45 Điện Biên Phủ, Phường Hồng Bàng, TP. Hải Phòng",
      requestedAt: "2026-11-19T17:10:00+07:00",
      status: "da_gui_nhap_24h",
      draftDeadline24h: "2026-11-20T17:10:00+07:00",
    },
    amendment: {
      id: "amd-bd-091",
      kind: "doi_dia_chi",
      requestedAt: "2026-11-19T20:15:00+07:00",
      reason: "Khách đi công tác đột xuất, nhờ chuyển địa chỉ giao về văn phòng gần nhà.",
      proposedAddress: "Số 88 Trần Phú, Phường Ngô Quyền, Thành phố Hải Phòng",
      status: "cho_duyet",
    },
  },
  "NM-261119-087": {
    orderCode: "NM-261119-087",
    deliveryNote: "Giao buổi tối sau 18:30.",
    giftBagRequested: false,
    amendment: {
      id: "amd-bd-087",
      kind: "huy_muon",
      requestedAt: "2026-11-19T21:40:00+07:00",
      reason: "Khách đặt nhầm size XL sang 2XL và muốn đặt lại đơn mới có áp mã Hội viên Nexus.",
      status: "cho_duyet",
    },
  },
  "NM-261116-064": {
    orderCode: "NM-261116-064",
    deliveryNote: "Gói kín tên sản phẩm đồ lót trên vận đơn.",
    giftBagRequested: false,
    vatRequest: {
      companyName: "Công ty TNHH Thương mại Biển Xanh Khánh Hòa",
      taxCode: "4201827364",
      email: "ketoan@bienxanhkh.vn",
      companyAddress: "Số 14 Lê Thánh Tôn, Phường Nha Trang, Tỉnh Khánh Hòa",
      requestedAt: "2026-11-16T16:00:00+07:00",
      status: "da_phat_hanh",
      issuedInvoiceNo: "HD-BD-2026-00418",
    },
  },
  "NM-261115-058": {
    orderCode: "NM-261115-058",
    deliveryNote: "Giao giờ hành chính.",
    giftBagRequested: false,
    tier1Complaint: {
      id: "cmp-bd-058",
      ticketCode: "KN1-BD-1158",
      openedAt: "2026-11-19T14:20:00+07:00",
      slaDeadline: "2026-11-20T14:20:00+07:00",
      slaRemainingLabel: "Còn 11 giờ phản hồi (SLA 24h)",
      category: "Đường may & hoàn thiện",
      buyerMessage:
        "Áo màu Xám đá size M có đoạn chỉ thừa dài ở gấu áo bên trái, mong xưởng hỗ trợ đổi chiếc mới hoặc hoàn Xu.",
      status: "cho_phan_hoi",
    },
  },
  "NM-261115-053": {
    orderCode: "NM-261115-053",
    deliveryNote: "Gọi trước khi giao.",
    giftBagRequested: false,
    twoWayExchange: {
      id: "ex-bd-053",
      requestedAt: "2026-11-19T15:10:00+07:00",
      fromVariant: "Áo thun Cotton Trắng · Size 2XL (BD-AT01-TRANG-2XL)",
      toVariant: "Áo thun Cotton Trắng · Size XL (BD-AT01-TRANG-XL)",
      reason: "Mặc hơi rộng vai, áo còn nguyên tem mác chưa giặt (trong hạn đổi trả 45 ngày).",
      courierStatus: "cho_dieu_phoi",
      courierPartner: "NexusLogistics Đổi 2 chiều",
      pickupWindow: "14:00 - 17:30 · 20/11/2026",
      policyNote: "Miễn phí vận chuyển 2 chiều theo cam kết đổi trả 45 ngày tận nơi.",
    },
    tier1Complaint: {
      id: "cmp-bd-053",
      ticketCode: "KN1-BD-1153",
      openedAt: "2026-11-19T15:18:00+07:00",
      slaDeadline: "2026-11-20T15:18:00+07:00",
      slaRemainingLabel: "Còn 12 giờ phản hồi (SLA 24h)",
      category: "Hỗ trợ đổi size 2 chiều",
      buyerMessage:
        "Nhờ xưởng xác nhận giữ sẵn size XL và hẹn bưu tá thu hồi buổi chiều giúp mình.",
      status: "da_phan_hoi",
      sellerResponse:
        "Xưởng đã giữ sẵn 1 áo Cotton Trắng size XL tại kho và đang đặt chuyến bưu tá đổi 2 chiều trong khung 14:00-17:30 hôm nay.",
    },
  },
  "SG-261120-104": {
    orderCode: "SG-261120-104",
    deliveryNote: "Giao nhanh nội thành Thảo Điền trước 16:00.",
    giftCardMessage: "Chúc anh tập luyện đạt PR mới cuối tuần này.",
    giftBagRequested: true,
    vatRequest: {
      companyName: "Công ty TNHH Thể thao Sông Xanh",
      taxCode: "0317720194",
      email: "ketoan@songxanhsport.vn",
      companyAddress: "24 Song Hành, Phường An Phú, TP. Hồ Chí Minh",
      requestedAt: "2026-11-20T01:55:00+07:00",
      status: "cho_duyet_nhap",
      draftDeadline24h: "2026-11-21T01:55:00+07:00",
    },
  },
  "DN-261119-091": {
    orderCode: "DN-261119-091",
    deliveryNote: "Gói quà phẳng không gập cổ áo polo.",
    giftCardMessage: "Cảm ơn đối tác đồng hành quý 4.",
    giftBagRequested: true,
    amendment: {
      id: "amd-dn-091",
      kind: "doi_dia_chi",
      requestedAt: "2026-11-19T19:00:00+07:00",
      reason: "Chuyển địa chỉ nhận sang chi nhánh mới cùng phường.",
      proposedAddress: "102 Nguyễn Văn Linh, Phường Hải Châu, TP. Đà Nẵng",
      status: "cho_duyet",
    },
  },
  "HN-261119-091": {
    orderCode: "HN-261119-091",
    deliveryNote: "Giao giờ hành chính.",
    giftBagRequested: false,
    twoWayExchange: {
      id: "ex-hn-091",
      requestedAt: "2026-11-19T10:20:00+07:00",
      fromVariant: "Áo Polo Dệt kim Đen · Size M",
      toVariant: "Áo Polo Dệt kim Đen · Size L",
      reason: "Khách đổi lên size L cho rộng vai, sản phẩm nguyên tem.",
      courierStatus: "da_dat_buu_ta",
      courierPartner: "NexusLogistics Đổi 2 chiều",
      exchangeWaybill: "NMLOG-EX-88412",
      pickupWindow: "09:00 - 12:00 · 20/11/2026",
      policyNote: "Miễn phí đổi size 2 chiều tận nơi trong 45 ngày.",
    },
  },
};

/* ---------- Wave 2 Promotions: Combos, Tier Discounts, Add-on Deals, Birthday Co-fund, Campaigns ---------- */

export interface SellerBundleCombo {
  id: string;
  shopId: string;
  name: string;
  skuSummary: string;
  componentProductIds: string[];
  originalTotal: number;
  comboPrice: number;
  deductChildSku: boolean;
  soldCount: number;
  active: boolean;
}

export interface SellerTierDiscountRule {
  id: string;
  shopId: string;
  title: string;
  scopeLabel: string;
  tiers: Array<{ minQty: number; discountPct: number }>;
  appliedOrders: number;
  active: boolean;
}

export interface SellerAddonDeal {
  id: string;
  shopId: string;
  mainProductName: string;
  addonProductName: string;
  addonOriginalPrice: number;
  addonDealPrice: number;
  combinedShip: boolean;
  maxPerOrder: number;
  soldCount: number;
  active: boolean;
}

export interface SellerBirthdayCofundConfig {
  shopId: string;
  enabled: boolean;
  eligibleTiers: string;
  giftName: string;
  giftValueVnd: number;
  shopSharePct: number;
  platformSharePct: number;
  monthlyQuota: number;
  claimedCount: number;
}

export type CampaignSlotKind = "soi_xanh_nexus" | "chien_dich_san" | "quang_ba_bst";

export interface SellerCampaignRegistration {
  id: string;
  shopId: string;
  kind: CampaignSlotKind;
  campaignName: string;
  slotLabel: string;
  productId: string;
  productName: string;
  commitmentNote: string;
  status: "dang_chay" | "da_duyet" | "cho_duyet";
  periodLabel: string;
}

export const INITIAL_SELLER_COMBOS: SellerBundleCombo[] = [
  {
    id: "cb-bd-1",
    shopId: "shop-bach-dang",
    name: "Bộ đôi Áo thun Cotton Compact Trắng + Xám đá (chọn cỡ từng chiếc)",
    skuSummary: "Trừ tồn tự động: BD-AT01 + BD-AT02",
    componentProductIds: ["prod-at-trang", "prod-at-xamda"],
    originalTotal: 368000,
    comboPrice: 329000,
    deductChildSku: true,
    soldCount: 64,
    active: true,
  },
  {
    id: "cb-bd-2",
    shopId: "shop-bach-dang",
    name: "Gói tuần cơ bản: 2 Áo thun Cotton + 1 Combo 3 Boxer Modal",
    skuSummary: "Trừ tồn tự động: BD-AT01 (x2) + BD-DL14",
    componentProductIds: ["prod-at-trang", "prod-dolot-boxer"],
    originalTotal: 587000,
    comboPrice: 519000,
    deductChildSku: true,
    soldCount: 41,
    active: true,
  },
  {
    id: "cb-sg-1",
    shopId: "shop-sai-gon",
    name: "Bộ chạy bộ cuối tuần: Áo Dry-Fit + Quần Short Run 5 inch",
    skuSummary: "Trừ tồn tự động: SG-DF04 + SG-SR10",
    componentProductIds: ["prod-at-dryfit", "prod-short-run"],
    originalTotal: 378000,
    comboPrice: 339000,
    deductChildSku: true,
    soldCount: 53,
    active: true,
  },
  {
    id: "cb-dn-1",
    shopId: "shop-da-nang",
    name: "Bộ công sở thoáng mát: Áo Polo Pique + Quần Short Linen",
    skuSummary: "Trừ tồn tự động: DN-PL06 + DN-SL11",
    componentProductIds: ["prod-polo-kem", "prod-short-linen"],
    originalTotal: 568000,
    comboPrice: 509000,
    deductChildSku: true,
    soldCount: 38,
    active: true,
  },
  {
    id: "cb-hn-1",
    shopId: "shop-ha-noi",
    name: "Bộ thu đông tối giản: Polo Dệt kim + Jogger Nỉ chân cua",
    skuSummary: "Trừ tồn tự động: HN-PK08 + HN-JT12",
    componentProductIds: ["prod-polo-detkim", "prod-jogger-terry"],
    originalTotal: 718000,
    comboPrice: 649000,
    deductChildSku: true,
    soldCount: 47,
    active: true,
  },
];

export const INITIAL_SELLER_TIER_RULES: SellerTierDiscountRule[] = [
  {
    id: "tr-bd-1",
    shopId: "shop-bach-dang",
    title: "Mua nhiều giảm theo tầng dòng Cotton Compact",
    scopeLabel: "Áp dụng toàn bộ Áo thun Cotton 220gsm cùng gian hàng",
    tiers: [
      { minQty: 2, discountPct: 10 },
      { minQty: 3, discountPct: 15 },
    ],
    appliedOrders: 88,
    active: true,
  },
  {
    id: "tr-bd-2",
    shopId: "shop-bach-dang",
    title: "Ưu đãi tủ đồ lót Modal theo lốc",
    scopeLabel: "Combo 3 Boxer Sợi Modal (trộn size tự do)",
    tiers: [
      { minQty: 2, discountPct: 8 },
      { minQty: 3, discountPct: 12 },
    ],
    appliedOrders: 52,
    active: true,
  },
  {
    id: "tr-sg-1",
    shopId: "shop-sai-gon",
    title: "Mua nhiều giảm thêm nhóm đồ chạy bộ Dry-Fit",
    scopeLabel: "Áo thun Dry-Fit và Quần Short Run",
    tiers: [
      { minQty: 2, discountPct: 10 },
      { minQty: 3, discountPct: 15 },
    ],
    appliedOrders: 74,
    active: true,
  },
  {
    id: "tr-dn-1",
    shopId: "shop-da-nang",
    title: "Giảm theo bậc áo Polo Pique & Linen",
    scopeLabel: "Áo Polo Pique Kem, Xanh đá và Short Linen",
    tiers: [
      { minQty: 2, discountPct: 10 },
      { minQty: 3, discountPct: 15 },
    ],
    appliedOrders: 49,
    active: true,
  },
  {
    id: "tr-hn-1",
    shopId: "shop-ha-noi",
    title: "Mua 2 giảm 10% dòng Dệt kim & Jogger",
    scopeLabel: "Áo Polo Dệt kim, Jogger Terry & Jogger Tech",
    tiers: [
      { minQty: 2, discountPct: 10 },
      { minQty: 3, discountPct: 15 },
    ],
    appliedOrders: 61,
    active: true,
  },
];

export const INITIAL_SELLER_ADDON_DEALS: SellerAddonDeal[] = [
  {
    id: "ad-bd-1",
    shopId: "shop-bach-dang",
    mainProductName: "Áo thun Cotton Compact Trắng / Xám đá",
    addonProductName: "Combo 3 Boxer Sợi Modal (mua kèm gộp ship)",
    addonOriginalPrice: 229000,
    addonDealPrice: 189000,
    combinedShip: true,
    maxPerOrder: 1,
    soldCount: 73,
    active: true,
  },
  {
    id: "ad-bd-2",
    shopId: "shop-bach-dang",
    mainProductName: "Mọi đơn hàng từ 299.000₫ của xưởng",
    addonProductName: "Áo thun Cotton Compact Xanh than (ưu đãi mua kèm)",
    addonOriginalPrice: 189000,
    addonDealPrice: 149000,
    combinedShip: true,
    maxPerOrder: 1,
    soldCount: 44,
    active: true,
  },
  {
    id: "ad-sg-1",
    shopId: "shop-sai-gon",
    mainProductName: "Áo khoác Gió Xám tro / Quần Short Daily",
    addonProductName: "Áo thun Dry-Fit Cổ vịt (mua kèm gộp ship)",
    addonOriginalPrice: 159000,
    addonDealPrice: 129000,
    combinedShip: true,
    maxPerOrder: 1,
    soldCount: 58,
    active: true,
  },
  {
    id: "ad-dn-1",
    shopId: "shop-da-nang",
    mainProductName: "Áo Polo Pique Cotton",
    addonProductName: "Combo 3 Brief Sợi tre (mua kèm gộp ship)",
    addonOriginalPrice: 209000,
    addonDealPrice: 169000,
    combinedShip: true,
    maxPerOrder: 1,
    soldCount: 39,
    active: true,
  },
  {
    id: "ad-hn-1",
    shopId: "shop-ha-noi",
    mainProductName: "Quần Jogger Terry / Polo Dệt kim",
    addonProductName: "Combo 5 Tất cổ trung Cotton (mua kèm gộp ship)",
    addonOriginalPrice: 129000,
    addonDealPrice: 95000,
    combinedShip: true,
    maxPerOrder: 1,
    soldCount: 82,
    active: true,
  },
];

export const INITIAL_BIRTHDAY_COFUND: Record<string, SellerBirthdayCofundConfig> = {
  "shop-bach-dang": {
    shopId: "shop-bach-dang",
    enabled: true,
    eligibleTiers: "Hội viên Nexus hạng Bạc, Vàng, Bạch Kim",
    giftName: "Tặng kèm 1 Áo thun Cotton Compact hoặc mã giảm 50.000₫ tháng sinh nhật",
    giftValueVnd: 50000,
    shopSharePct: 50,
    platformSharePct: 50,
    monthlyQuota: 120,
    claimedCount: 48,
  },
  "shop-sai-gon": {
    shopId: "shop-sai-gon",
    enabled: true,
    eligibleTiers: "Hội viên Nexus hạng Bạc, Vàng, Bạch Kim",
    giftName: "Mã sinh nhật 45.000₫ cho đơn từ 249.000₫",
    giftValueVnd: 45000,
    shopSharePct: 50,
    platformSharePct: 50,
    monthlyQuota: 100,
    claimedCount: 41,
  },
  "shop-da-nang": {
    shopId: "shop-da-nang",
    enabled: true,
    eligibleTiers: "Hội viên Nexus hạng Vàng, Bạch Kim",
    giftName: "Mã sinh nhật 60.000₫ dòng Linen & Polo",
    giftValueVnd: 60000,
    shopSharePct: 50,
    platformSharePct: 50,
    monthlyQuota: 80,
    claimedCount: 29,
  },
  "shop-ha-noi": {
    shopId: "shop-ha-noi",
    enabled: true,
    eligibleTiers: "Hội viên Nexus hạng Bạc, Vàng, Bạch Kim",
    giftName: "Tặng kèm Combo 4 Tất cổ thấp cho đơn sinh nhật từ 350.000₫",
    giftValueVnd: 109000,
    shopSharePct: 60,
    platformSharePct: 40,
    monthlyQuota: 90,
    claimedCount: 37,
  },
};

export const INITIAL_CAMPAIGN_REGISTRATIONS: SellerCampaignRegistration[] = [
  {
    id: "cmp-reg-1",
    shopId: "shop-bach-dang",
    kind: "soi_xanh_nexus",
    campaignName: "Chiến dịch cộng đồng Sợi Xanh Nexus · Quý 4/2026",
    slotLabel: "BST Sợi Xanh Nexus",
    productId: "prod-at-trang",
    productName: "Áo thun Cotton Trắng",
    commitmentNote:
      "Trích 5% doanh thu mỗi sản phẩm vào quỹ dệt sợi tái chế & tủ áo học sinh vùng cao",
    status: "dang_chay",
    periodLabel: "01/11 - 31/12/2026",
  },
  {
    id: "cmp-reg-2",
    shopId: "shop-bach-dang",
    kind: "quang_ba_bst",
    campaignName: "Suất quảng bá Mega-menu · BST Cotton Compact 220gsm",
    slotLabel: "Cột BST nổi bật trên Mega-menu",
    productId: "prod-at-xamda",
    productName: "Áo thun Cotton Xám đá",
    commitmentNote: "Đảm bảo tồn kho tối thiểu 80 chiếc/size và giao trong 24h",
    status: "da_duyet",
    periodLabel: "15/11 - 30/11/2026",
  },
  {
    id: "cmp-reg-3",
    shopId: "shop-bach-dang",
    kind: "chien_dich_san",
    campaignName: "Tuần lễ Đồ cơ bản cuối năm · Hoàn Xu x2 Thứ Sáu",
    slotLabel: "Trang chủ · Khối Bán chạy",
    productId: "prod-dolot-boxer",
    productName: "Combo 3 Boxer Modal",
    commitmentNote: "Đồng tài trợ thêm 1% hoàn Xu cho Hội viên Nexus",
    status: "cho_duyet",
    periodLabel: "22/11 - 05/12/2026",
  },
  {
    id: "cmp-reg-4",
    shopId: "shop-sai-gon",
    kind: "chien_dich_san",
    campaignName: "Trang công nghệ vải Dry-Fit Chạy bộ 2026",
    slotLabel: "Landing Công nghệ vải",
    productId: "prod-at-dryfit",
    productName: "Áo thun Dry-Fit Cổ vịt",
    commitmentNote: "Cung cấp chứng nhận định lượng & thử nghiệm thoát ẩm nhanh",
    status: "dang_chay",
    periodLabel: "05/11 - 30/11/2026",
  },
  {
    id: "cmp-reg-5",
    shopId: "shop-da-nang",
    kind: "soi_xanh_nexus",
    campaignName: "Chiến dịch cộng đồng Sợi Xanh Nexus · Sợi thực vật",
    slotLabel: "BST Sợi Xanh Nexus",
    productId: "prod-short-linen",
    productName: "Quần Short Linen Xám đá",
    commitmentNote: "Trích 5% doanh thu đóng góp minh bạch theo sao kê tháng",
    status: "dang_chay",
    periodLabel: "01/11 - 31/12/2026",
  },
  {
    id: "cmp-reg-6",
    shopId: "shop-ha-noi",
    kind: "quang_ba_bst",
    campaignName: "Suất quảng bá BST Thu Đông Miền Bắc",
    slotLabel: "Mega-menu · Hoàn cảnh mặc",
    productId: "prod-jogger-terry",
    productName: "Quần Jogger Terry Xám",
    commitmentNote: "Cam kết đủ 5 size S-2XL suốt chiến dịch",
    status: "da_duyet",
    periodLabel: "10/11 - 15/12/2026",
  },
];

/* ---------- Wave 2 Interaction & Content (/seller/tuong-tac) ---------- */

export type FitFeedbackKind = "chat" | "dung" | "rong";

export interface SellerSampleReview {
  id: string;
  shopId: string;
  productId: string;
  productName: string;
  productImage: string;
  skuLabel: string;
  buyerName: string;
  buyerMeasurements: string;
  rating: number; // 1..5
  fitFeedback: FitFeedbackKind;
  comment: string;
  verifiedOrderCode: string;
  createdAt: string;
  hasPhoto: boolean;
  reply?: {
    text: string;
    repliedAt: string;
  };
}

export interface SellerProductQuestion {
  id: string;
  shopId: string;
  productId: string;
  productName: string;
  buyerName: string;
  question: string;
  askedAt: string;
  answer?: string;
  answeredAt?: string;
  moderationStatus: "da_duyet" | "cho_duyet";
}

export interface SellerShopFaqItem {
  id: string;
  shopId: string;
  topic: string;
  question: string;
  answer: string;
  updatedAt: string;
  active: boolean;
}

export type LookbookStatus = "nhap" | "cho_duyet" | "da_dang";

export interface SellerLookbookPost {
  id: string;
  shopId: string;
  title: string;
  category: string;
  excerpt: string;
  coverImage: string;
  taggedProductIds: string[];
  status: LookbookStatus;
  submittedAt: string;
  adminNote?: string;
  views: number;
}

export const INITIAL_SELLER_REVIEWS: SellerSampleReview[] = [
  {
    id: "rev-bd-1",
    shopId: "shop-bach-dang",
    productId: "prod-at-trang",
    productName: "Áo thun Cotton Trắng",
    productImage: "/products/ao-thun-cotton-trang.png",
    skuLabel: "Trắng tinh · Size L",
    buyerName: "Ngô Văn Tùng",
    buyerMeasurements: "1m76 · 71kg · Mua L",
    rating: 5,
    fitFeedback: "dung",
    comment:
      "Vải cotton 220gsm đứng dáng, giặt máy 3 lần chưa thấy xù hay bai cổ. Mặc đi làm dưới áo sơ mi hoặc mặc lẻ đều gọn.",
    verifiedOrderCode: "NM-261116-064",
    createdAt: "2026-11-19T09:15:00+07:00",
    hasPhoto: true,
  },
  {
    id: "rev-bd-2",
    shopId: "shop-bach-dang",
    productId: "prod-dolot-boxer",
    productName: "Combo 3 Boxer Modal",
    productImage: "/products/do-lot-boxer-modal-3.png",
    skuLabel: "Xám đá / Đen · Size L",
    buyerName: "Nguyễn Minh Quân",
    buyerMeasurements: "1m74 · 73kg · Mua L",
    rating: 5,
    fitFeedback: "dung",
    comment:
      "Cạp mềm không hằn bụng khi ngồi văn phòng cả ngày, giao gói kín đáo. Đã đặt thêm lốc thứ hai.",
    verifiedOrderCode: "NM-261110-031",
    createdAt: "2026-11-18T20:40:00+07:00",
    hasPhoto: false,
    reply: {
      text: "Cảm ơn anh Quân đã tin tưởng dòng Modal của Xưởng Dệt Bạch Đằng. Rất mong tiếp tục phục vụ anh ở những đơn kế tiếp ạ.",
      repliedAt: "2026-11-18T21:05:00+07:00",
    },
  },
  {
    id: "rev-bd-3",
    shopId: "shop-bach-dang",
    productId: "prod-at-xamda",
    productName: "Áo thun Cotton Xám đá",
    productImage: "/products/ao-thun-cotton-xam-da.png",
    skuLabel: "Xám đá · Size M",
    buyerName: "Hoàng Anh Tuấn",
    buyerMeasurements: "1m70 · 65kg · Mua M",
    rating: 4,
    fitFeedback: "chat",
    comment:
      "Màu xám đá bên ngoài đẹp và lì vải, tuy nhiên phần vai hơi ôm nhẹ với người tập gym. Ai thích rộng nên tăng 1 size.",
    verifiedOrderCode: "NM-261115-058",
    createdAt: "2026-11-19T14:12:00+07:00",
    hasPhoto: true,
  },
  {
    id: "rev-bd-4",
    shopId: "shop-bach-dang",
    productId: "prod-at-trang",
    productName: "Áo thun Cotton Trắng",
    productImage: "/products/ao-thun-cotton-trang.png",
    skuLabel: "Trắng tinh · Size 2XL",
    buyerName: "Đỗ Minh Hiếu",
    buyerMeasurements: "1m81 · 82kg · Mua 2XL",
    rating: 4,
    fitFeedback: "rong",
    comment:
      "Vải dày mịn nhưng chọn 2XL bị hơi rộng thân áo, mình đã tạo yêu cầu đổi 2 chiều xuống XL theo chính sách 45 ngày.",
    verifiedOrderCode: "NM-261115-053",
    createdAt: "2026-11-19T15:02:00+07:00",
    hasPhoto: false,
  },
  {
    id: "rev-sg-1",
    shopId: "shop-sai-gon",
    productId: "prod-at-dryfit",
    productName: "Áo thun Dry-Fit Cổ vịt",
    productImage: "/products/ao-thun-dryfit-co-vit.png",
    skuLabel: "Xanh cổ vịt · Size L",
    buyerName: "Trần Hải Đăng",
    buyerMeasurements: "1m75 · 70kg · Mua L",
    rating: 5,
    fitFeedback: "dung",
    comment: "Chạy 10km sáng sớm thấm hút rất nhanh, không dính bết vào lưng.",
    verifiedOrderCode: "SG-261115-042",
    createdAt: "2026-11-18T08:30:00+07:00",
    hasPhoto: true,
  },
  {
    id: "rev-dn-1",
    shopId: "shop-da-nang",
    productId: "prod-polo-kem",
    productName: "Áo Polo Pique Kem",
    productImage: "/products/ao-polo-pique-kem.png",
    skuLabel: "Kem nhạt · Size L",
    buyerName: "Phạm Quốc Khánh",
    buyerMeasurements: "1m77 · 72kg · Mua L",
    rating: 5,
    fitFeedback: "dung",
    comment: "Bo cổ dệt đứng dáng, vải mắt chim thoáng mặc trời Đà Nẵng dễ chịu.",
    verifiedOrderCode: "DN-261114-039",
    createdAt: "2026-11-18T11:20:00+07:00",
    hasPhoto: false,
  },
  {
    id: "rev-hn-1",
    shopId: "shop-ha-noi",
    productId: "prod-jogger-terry",
    productName: "Quần Jogger Terry Xám",
    productImage: "/products/quan-jogger-terry-xam.png",
    skuLabel: "Xám tiêu · Size L",
    buyerName: "Vũ Đức Mạnh",
    buyerMeasurements: "1m78 · 74kg · Mua L",
    rating: 5,
    fitFeedback: "dung",
    comment: "Nỉ chân cua 320gsm dày vừa đủ ấm tiết trời 18 độ mà không bí chân.",
    verifiedOrderCode: "HN-261114-028",
    createdAt: "2026-11-18T19:15:00+07:00",
    hasPhoto: true,
  },
];

export const INITIAL_SELLER_QUESTIONS: SellerProductQuestion[] = [
  {
    id: "qa-bd-1",
    shopId: "shop-bach-dang",
    productId: "prod-at-trang",
    productName: "Áo thun Cotton Trắng",
    buyerName: "Lê Minh Hoàng",
    question:
      "Áo màu Trắng 220gsm mặc có bị lộ mờ bên trong không shop? Mình cao 1m73 nặng 68kg nên chọn M hay L?",
    askedAt: "2026-11-20T00:40:00+07:00",
    moderationStatus: "da_duyet",
  },
  {
    id: "qa-bd-2",
    shopId: "shop-bach-dang",
    productId: "prod-dolot-boxer",
    productName: "Combo 3 Boxer Modal",
    buyerName: "Phạm Tuấn Anh",
    question: "Combo 3 chiếc có thể chọn trộn 2 size khác nhau trong cùng một hộp không ạ?",
    askedAt: "2026-11-19T18:15:00+07:00",
    answer:
      "Hiện combo 3 chiếc đóng pack cố định cùng 1 size để đảm bảo niêm phong. Anh có thể chọn mua 2 pack với 2 size khác nhau để được giảm tầng 8% ạ.",
    answeredAt: "2026-11-19T18:32:00+07:00",
    moderationStatus: "da_duyet",
  },
  {
    id: "qa-bd-3",
    shopId: "shop-bach-dang",
    productId: "prod-at-xanhthan",
    productName: "Áo thun Xanh than",
    buyerName: "Đinh Quang Hải",
    question:
      "Màu Xanh than có bị ra màu ở lần giặt đầu tiên nếu giặt chung với đồ sáng màu không?",
    askedAt: "2026-11-19T23:10:00+07:00",
    moderationStatus: "da_duyet",
  },
  {
    id: "qa-sg-1",
    shopId: "shop-sai-gon",
    productId: "prod-short-run",
    productName: "Quần Short Run Xám đậm",
    buyerName: "Trịnh Xuân Trường",
    question: "Túi khóa kéo phía sau có đựng vừa điện thoại 6.7 inch khi chạy bộ không shop?",
    askedAt: "2026-11-19T21:05:00+07:00",
    moderationStatus: "da_duyet",
  },
  {
    id: "qa-dn-1",
    shopId: "shop-da-nang",
    productId: "prod-short-linen",
    productName: "Quần Short Linen Xám đá",
    buyerName: "Nguyễn Hữu Nghĩa",
    question: "Lưng quần có dây rút bên trong hay bên ngoài và có đỉa đeo thắt lưng không?",
    askedAt: "2026-11-19T16:40:00+07:00",
    moderationStatus: "da_duyet",
  },
  {
    id: "qa-hn-1",
    shopId: "shop-ha-noi",
    productId: "prod-polo-detkim",
    productName: "Áo Polo Dệt kim Đen",
    buyerName: "Cao Văn Bình",
    question: "Áo dệt kim có cần giặt tay hay cho vào túi lưới giặt máy được không shop?",
    askedAt: "2026-11-19T17:25:00+07:00",
    moderationStatus: "da_duyet",
  },
];

export const INITIAL_SELLER_FAQS: SellerShopFaqItem[] = [
  {
    id: "faq-bd-1",
    shopId: "shop-bach-dang",
    topic: "Chọn size",
    question: "Nếu số đo nằm giữa hai size M và L thì nên chọn thế nào?",
    answer:
      "Dòng Cotton Compact 220gsm dệt theo phom Regular gọn vai. Nếu thích mặc ôm vừa văn phòng chọn size nhỏ hơn; nếu thích mặc thoáng hoặc vai rộng nên tăng lên 1 size.",
    updatedAt: "2026-11-15",
    active: true,
  },
  {
    id: "faq-bd-2",
    shopId: "shop-bach-dang",
    topic: "Giao hàng & đổi trả 45 ngày",
    question: "Xưởng hỗ trợ đổi size tận nơi ra sao?",
    answer:
      "Xưởng tham gia cam kết đổi trả 45 ngày và đổi size 2 chiều tận nơi trên NexusMart. Bưu tá mang size mới tới và thu hồi áo nguyên tem trong cùng một chuyến, miễn phí cước vận chuyển.",
    updatedAt: "2026-11-18",
    active: true,
  },
  {
    id: "faq-bd-3",
    shopId: "shop-bach-dang",
    topic: "Hóa đơn VAT",
    question: "Đặt áo đồng phục hoặc đơn doanh nghiệp có xuất hóa đơn điện tử VAT không?",
    answer:
      "Có. Quý khách điền mã số thuế và email nhận hóa đơn ngay tại bước thanh toán; xưởng gửi bản nháp kiểm tra trong 24 giờ và phát hành HĐĐT sau khi hoàn tất giao hàng.",
    updatedAt: "2026-11-19",
    active: true,
  },
  {
    id: "faq-sg-1",
    shopId: "shop-sai-gon",
    topic: "Chất liệu & giặt ủi",
    question: "Vải Dry-Fit có nên dùng nước xả làm mềm vải không?",
    answer:
      "Không nên dùng nước xả vải vì lớp màng silicone sẽ giảm tốc độ thấm hút và thoát mồ hôi của sợi microfiber.",
    updatedAt: "2026-11-16",
    active: true,
  },
  {
    id: "faq-dn-1",
    shopId: "shop-da-nang",
    topic: "Chất liệu & giặt ủi",
    question: "Vải Linen pha Cotton có nhăn nhiều không?",
    answer:
      "Tỷ lệ 55% Linen và 45% Cotton đã xử lý wash mềm giúp giảm nhăn khoảng 40% so với linen nguyên chất và giữ độ thoáng mát tự nhiên.",
    updatedAt: "2026-11-16",
    active: true,
  },
  {
    id: "faq-hn-1",
    shopId: "shop-ha-noi",
    topic: "Giao hàng & đổi trả 45 ngày",
    question: "Đơn nội thành Hà Nội có hỗ trợ giao nhanh trong ngày không?",
    answer:
      "Các đơn đặt trước 15:00 tại khu vực nội thành Hà Nội đều hỗ trợ giao nhanh 2-4 giờ qua đội ngũ đối tác vận chuyển.",
    updatedAt: "2026-11-17",
    active: true,
  },
];

export const INITIAL_SELLER_LOOKBOOKS: SellerLookbookPost[] = [
  {
    id: "lb-bd-1",
    shopId: "shop-bach-dang",
    title: "3 cách phối Áo thun Cotton 220gsm cho cả tuần công sở lẫn cuối tuần",
    category: "Phối đồ hằng ngày",
    excerpt:
      "Giữ bảng màu trung tính Trắng tinh, Xám đá và Xanh than để xoay vòng nhanh mỗi sáng mà vẫn gọn gàng, lịch sự.",
    coverImage: "/products/ao-thun-cotton-trang.png",
    taggedProductIds: ["prod-at-trang", "prod-at-xamda", "prod-at-xanhthan"],
    status: "da_dang",
    submittedAt: "2026-11-12",
    adminNote: "Đã duyệt hiển thị trên Sổ tay vải & Phối đồ.",
    views: 428,
  },
  {
    id: "lb-bd-2",
    shopId: "shop-bach-dang",
    title: "Cách phân biệt Cotton chải kỹ Compact và Cotton thường sau 20 lần giặt",
    category: "Bảo quản vải",
    excerpt:
      "Ghi nhận thực tế độ co rút, độ đứng bo cổ và bề mặt sợi từ phòng kiểm định QC của Xưởng Dệt Bạch Đằng.",
    coverImage: "/products/ao-thun-cotton-xam-da.png",
    taggedProductIds: ["prod-at-xamda", "prod-dolot-boxer"],
    status: "cho_duyet",
    submittedAt: "2026-11-19",
    adminNote: "Đang chờ biên tập viên sàn duyệt ảnh gắn thẻ SKU.",
    views: 0,
  },
  {
    id: "lb-bd-3",
    shopId: "shop-bach-dang",
    title: "Gợi ý chuẩn bị hành lý 4 ngày chỉ với 3 món đồ cơ bản",
    category: "Phối đồ hằng ngày",
    excerpt:
      "Bản nháp lookbook du lịch gọn nhẹ, dự kiến gắn thêm combo đa size trước khi gửi duyệt.",
    coverImage: "/products/ao-thun-cotton-xanh-than.png",
    taggedProductIds: ["prod-at-xanhthan"],
    status: "nhap",
    submittedAt: "2026-11-20",
    views: 0,
  },
  {
    id: "lb-sg-1",
    shopId: "shop-sai-gon",
    title: "Phối đồ chạy bộ buổi sáng trời ẩm Sài Gòn: nhẹ, thoáng và nhanh khô",
    category: "Chạy bộ & thể thao",
    excerpt: "Kết hợp áo Dry-Fit 145gsm cùng quần Short Run 5 inch cho cự ly 5-15km.",
    coverImage: "/products/ao-thun-dryfit-co-vit.png",
    taggedProductIds: ["prod-at-dryfit", "prod-short-run"],
    status: "da_dang",
    submittedAt: "2026-11-14",
    views: 315,
  },
  {
    id: "lb-dn-1",
    shopId: "shop-da-nang",
    title: "Mặc Polo Pique và Short Linen sao cho gọn gàng khi đi biển hoặc gặp đối tác",
    category: "Công sở tối giản",
    excerpt: "Lựa chọn bảng màu Kem nhạt - Xám đá hài hòa dưới nắng miền Trung.",
    coverImage: "/products/ao-polo-pique-kem.png",
    taggedProductIds: ["prod-polo-kem", "prod-short-linen"],
    status: "da_dang",
    submittedAt: "2026-11-13",
    views: 274,
  },
  {
    id: "lb-hn-1",
    shopId: "shop-ha-noi",
    title: "Layering 18 độ Hà Nội với Polo Dệt kim và Quần Jogger Nỉ chân cua",
    category: "Công sở tối giản",
    excerpt: "Cân bằng giữa giữ ấm nhẹ và sự linh hoạt khi di chuyển trong ngày.",
    coverImage: "/products/quan-jogger-terry-xam.png",
    taggedProductIds: ["prod-polo-detkim", "prod-jogger-terry"],
    status: "da_dang",
    submittedAt: "2026-11-15",
    views: 362,
  },
];

/* ---------- Wave 2 Factory Profile, Size Charts & Operations (/seller/ho-so) ---------- */

export interface SellerQcStandard {
  code: string;
  title: string;
  detail: string;
}

export interface SellerShopMenuLink {
  id: string;
  label: string;
  targetCollection: string;
  visible: boolean;
}

export interface SellerShopCollection {
  id: string;
  name: string;
  slug: string;
  description: string;
  productCount: number;
  featured: boolean;
}

export interface SellerFactoryProfile {
  shopId: string;
  factoryName: string;
  establishedYear: number;
  factoryAddress: string;
  monthlyCapacity: string;
  yarnSources: string[];
  qcStandards: SellerQcStandard[];
  shopMenu: SellerShopMenuLink[];
  collections: SellerShopCollection[];
}

export interface SellerSizeChartRow {
  size: "S" | "M" | "L" | "XL" | "2XL";
  heightRange: string;
  weightRange: string;
  chestOrWaistCm: string;
  shoulderOrHipCm: string;
  lengthCm: string;
}

export interface SellerShopSizeCharts {
  shopId: string;
  topsNote: string;
  tops: SellerSizeChartRow[];
  bottomsNote: string;
  bottoms: SellerSizeChartRow[];
  updatedAt: string;
}

export interface SellerOperationsConfig {
  shopId: string;
  freeShipThresholdVnd: number;
  standardShipFeeVnd: number;
  prepSlaHours: number;
  fastDeliveryEnabled: boolean;
  fastDeliveryArea: string;
  fastDeliveryCutoff: string;
  return45DaysEnabled: boolean;
  twoWayExchangeEnabled: boolean;
  verifiedWorkshopBadge: boolean;
  returnExceptionNote: string;
}

export const INITIAL_FACTORY_PROFILES: Record<string, SellerFactoryProfile> = {
  "shop-bach-dang": {
    shopId: "shop-bach-dang",
    factoryName: "Xưởng Dệt Kim & May Bạch Đằng (Hải Phòng)",
    establishedYear: 2019,
    factoryAddress: "Lô B4 Cụm công nghiệp Vĩnh Niệm, Phường Ngô Quyền, TP. Hải Phòng",
    monthlyCapacity: "38.000 sản phẩm/tháng",
    yarnSources: [
      "Bông chải kỹ Compact nhập khẩu định lượng 220gsm",
      "Sợi gỗ sồi Modal dệt kim tròn chống xù bề mặt",
      "Bo cổ rib 1x1 pha 5% spandex giữ phom sau 40 lần giặt",
    ],
    qcStandards: [
      {
        code: "QC-CO-RUT",
        title: "Kiểm thử co rút trước cắt rập",
        detail: "Giặt thử mẫu từng cuộn vải, sai số chiều dài sau giặt không vượt 2%.",
      },
      {
        code: "QC-DUONG-MAY",
        title: "Kiểm tra 100% đường may vai & cổ áo",
        detail: "Mật độ mũi kim 5 mũi/cm, gia cố dây viền sau gáy chống giãn cổ.",
      },
      {
        code: "QC-BEN-MAU",
        title: "Kiểm định độ bền màu mức 4/5",
        detail: "Nhuộm hoạt tính thân thiện da nhạy cảm, không phai sang đồ sáng màu.",
      },
    ],
    shopMenu: [
      {
        id: "sm-bd-1",
        label: "Cotton Compact 220gsm",
        targetCollection: "cotton-compact-220gsm",
        visible: true,
      },
      {
        id: "sm-bd-2",
        label: "Đồ lót Sợi Modal",
        targetCollection: "do-lot-soi-modal",
        visible: true,
      },
      {
        id: "sm-bd-3",
        label: "Combo tiết kiệm theo tuần",
        targetCollection: "combo-tiet-kiem",
        visible: true,
      },
      {
        id: "sm-bd-4",
        label: "Sợi Xanh Nexus",
        targetCollection: "soi-xanh-nexus",
        visible: true,
      },
    ],
    collections: [
      {
        id: "col-bd-1",
        name: "Cotton Compact 220gsm",
        slug: "cotton-compact-220gsm",
        description: "Bộ áo thun cổ tròn 3 màu trung tính Trắng tinh, Xám đá và Xanh than.",
        productCount: 3,
        featured: true,
      },
      {
        id: "col-bd-2",
        name: "Đồ lót Sợi Modal",
        slug: "do-lot-soi-modal",
        description: "Combo 3 boxer cạp phẳng không hằn, thoáng mát cả ngày.",
        productCount: 1,
        featured: true,
      },
      {
        id: "col-bd-3",
        name: "Combo tiết kiệm theo tuần",
        slug: "combo-tiet-kiem",
        description: "Gói phối sẵn áo thun và đồ lót với ưu đãi giảm tầng.",
        productCount: 2,
        featured: false,
      },
    ],
  },
  "shop-sai-gon": {
    shopId: "shop-sai-gon",
    factoryName: "Xưởng Hoàn thiện Đồ Vận động Basic Sài Gòn",
    establishedYear: 2020,
    factoryAddress: "Khu chế xuất Tân Thuận, Phường Tân Thuận Đông, TP. Hồ Chí Minh",
    monthlyCapacity: "44.000 sản phẩm/tháng",
    yarnSources: [
      "Sợi Microfiber Dry-Fit dệt lỗ thoáng khí 145gsm",
      "Vải dù trượt nước tái chế chuẩn GRS cho áo khoác gió",
    ],
    qcStandards: [
      {
        code: "QC-THOAT-AM",
        title: "Kiểm thử tốc độ thấm hút & khô nhanh",
        detail: "Thời gian tán ẩm bề mặt dưới 4 giây, trọng lượng nhẹ khi ra mồ hôi.",
      },
      {
        code: "QC-FLATLOCK",
        title: "Đường may phẳng giảm ma sát",
        detail: "Kiểm tra đường may nách và sườn chống cọ xát khi chạy cự ly dài.",
      },
    ],
    shopMenu: [
      {
        id: "sm-sg-1",
        label: "Dry-Fit Chạy bộ",
        targetCollection: "dryfit-chay-bo",
        visible: true,
      },
      {
        id: "sm-sg-2",
        label: "Basic Phom Rộng",
        targetCollection: "basic-phom-rong",
        visible: true,
      },
    ],
    collections: [
      {
        id: "col-sg-1",
        name: "Dry-Fit Chạy bộ",
        slug: "dryfit-chay-bo",
        description: "Trang phục chạy bộ thoát ẩm nhanh cho khí hậu nóng ẩm.",
        productCount: 3,
        featured: true,
      },
      {
        id: "col-sg-2",
        name: "Basic Phom Rộng",
        slug: "basic-phom-rong",
        description: "Áo thun oversize và quần short mặc hằng ngày.",
        productCount: 2,
        featured: true,
      },
    ],
  },
  "shop-da-nang": {
    shopId: "shop-da-nang",
    factoryName: "Xưởng Dệt Sợi Tự Nhiên Đà Nẵng",
    establishedYear: 2021,
    factoryAddress: "KCN Hòa Khánh, Phường Liên Chiểu, TP. Đà Nẵng",
    monthlyCapacity: "26.000 sản phẩm/tháng",
    yarnSources: [
      "Pique Cotton mắt chim 210gsm thoáng khí",
      "Sợi lanh (Linen) pha Cotton wash mềm chống co",
      "Sợi tre Bamboo kháng mùi tự nhiên",
    ],
    qcStandards: [
      {
        code: "QC-WASH-MEM",
        title: "Xử lý wash enzyme trước hoàn thiện",
        detail: "Làm mềm sợi linen tự nhiên và ổn định kích thước dưới 1,5%.",
      },
      {
        code: "QC-CO-POLO",
        title: "Kiểm tra độ đứng bo cổ Polo",
        detail: "Bo cổ dệt riêng theo mật độ cao giúp không quăn mép sau giặt.",
      },
    ],
    shopMenu: [
      {
        id: "sm-dn-1",
        label: "Polo & Linen Tự Nhiên",
        targetCollection: "polo-linen",
        visible: true,
      },
      {
        id: "sm-dn-2",
        label: "Sợi Tre Thiên Nhiên",
        targetCollection: "soi-tre",
        visible: true,
      },
    ],
    collections: [
      {
        id: "col-dn-1",
        name: "Polo & Linen Tự Nhiên",
        slug: "polo-linen",
        description: "Phong cách công sở thoáng nhẹ từ sợi thực vật.",
        productCount: 3,
        featured: true,
      },
      {
        id: "col-dn-2",
        name: "Sợi Tre Thiên Nhiên",
        slug: "soi-tre",
        description: "Đồ lót sợi tre mềm mịn, thấm hút cao.",
        productCount: 1,
        featured: false,
      },
    ],
  },
  "shop-ha-noi": {
    shopId: "shop-ha-noi",
    factoryName: "Xưởng Dệt Kim Hà Nội",
    establishedYear: 2018,
    factoryAddress: "Cụm sản xuất Minh Khai, Phường Hai Bà Trưng, TP. Hà Nội",
    monthlyCapacity: "32.000 sản phẩm/tháng",
    yarnSources: [
      "Nỉ chân cua 320gsm cotton 2 da giữ ấm thoáng khí",
      "Sợi dệt kim mịn cổ polo & tất cotton chải kỹ",
    ],
    qcStandards: [
      {
        code: "QC-BO-GAU",
        title: "Kiểm định độ đàn hồi bo gấu & cổ tất",
        detail: "Thử nghiệm kéo giãn 500 chu kỳ không bai nhão bo gấu jogger.",
      },
    ],
    shopMenu: [
      {
        id: "sm-hn-1",
        label: "Thu Đông Dệt Kim",
        targetCollection: "thu-dong-det-kim",
        visible: true,
      },
      {
        id: "sm-hn-2",
        label: "Phụ kiện Cotton Gân",
        targetCollection: "phu-kien-cotton",
        visible: true,
      },
    ],
    collections: [
      {
        id: "col-hn-1",
        name: "Thu Đông Dệt Kim",
        slug: "thu-dong-det-kim",
        description: "Áo polo dệt kim và quần jogger cho tiết trời se lạnh.",
        productCount: 3,
        featured: true,
      },
      {
        id: "col-hn-2",
        name: "Phụ kiện Cotton Gân",
        slug: "phu-kien-cotton",
        description: "Combo tất cotton cổ trung và tất thể thao cổ thấp.",
        productCount: 2,
        featured: true,
      },
    ],
  },
};

const DEFAULT_TOPS_ROWS: SellerSizeChartRow[] = [
  {
    size: "S",
    heightRange: "160-166",
    weightRange: "52-59",
    chestOrWaistCm: "94",
    shoulderOrHipCm: "43",
    lengthCm: "67",
  },
  {
    size: "M",
    heightRange: "166-172",
    weightRange: "60-67",
    chestOrWaistCm: "100",
    shoulderOrHipCm: "45",
    lengthCm: "69",
  },
  {
    size: "L",
    heightRange: "172-177",
    weightRange: "68-75",
    chestOrWaistCm: "106",
    shoulderOrHipCm: "47",
    lengthCm: "71",
  },
  {
    size: "XL",
    heightRange: "177-182",
    weightRange: "76-83",
    chestOrWaistCm: "112",
    shoulderOrHipCm: "49",
    lengthCm: "73",
  },
  {
    size: "2XL",
    heightRange: "182-188",
    weightRange: "84-92",
    chestOrWaistCm: "118",
    shoulderOrHipCm: "51",
    lengthCm: "75",
  },
];

const DEFAULT_BOTTOMS_ROWS: SellerSizeChartRow[] = [
  {
    size: "S",
    heightRange: "160-166",
    weightRange: "52-59",
    chestOrWaistCm: "72-76",
    shoulderOrHipCm: "94",
    lengthCm: "44",
  },
  {
    size: "M",
    heightRange: "166-172",
    weightRange: "60-67",
    chestOrWaistCm: "77-81",
    shoulderOrHipCm: "98",
    lengthCm: "46",
  },
  {
    size: "L",
    heightRange: "172-177",
    weightRange: "68-75",
    chestOrWaistCm: "82-86",
    shoulderOrHipCm: "102",
    lengthCm: "48",
  },
  {
    size: "XL",
    heightRange: "177-182",
    weightRange: "76-83",
    chestOrWaistCm: "87-92",
    shoulderOrHipCm: "106",
    lengthCm: "50",
  },
  {
    size: "2XL",
    heightRange: "182-188",
    weightRange: "84-92",
    chestOrWaistCm: "93-98",
    shoulderOrHipCm: "110",
    lengthCm: "51",
  },
];

export const INITIAL_SHOP_SIZE_CHARTS: Record<string, SellerShopSizeCharts> = {
  "shop-bach-dang": {
    shopId: "shop-bach-dang",
    topsNote:
      "Form Regular gọn vai chuẩn vóc dáng Việt Nam. Sai số đo thủ công ±1cm; widget gợi ý size toàn sàn tự đọc trực tiếp từ bảng này.",
    tops: DEFAULT_TOPS_ROWS,
    bottomsNote:
      "Áp dụng cho nhóm Quần và Đồ lót Modal (cạp co giãn thoải mái theo vòng eo chuẩn).",
    bottoms: DEFAULT_BOTTOMS_ROWS,
    updatedAt: "2026-11-18",
  },
  "shop-sai-gon": {
    shopId: "shop-sai-gon",
    topsNote: "Form vận động ôm vừa thân trên, thoáng nách khi chạy bộ.",
    tops: DEFAULT_TOPS_ROWS,
    bottomsNote: "Chiều dài ống quần short 5-7 inch, lưng thun kèm dây rút.",
    bottoms: DEFAULT_BOTTOMS_ROWS,
    updatedAt: "2026-11-17",
  },
  "shop-da-nang": {
    shopId: "shop-da-nang",
    topsNote: "Form Polo công sở đứng vai, giữ phom cổ dệt.",
    tops: DEFAULT_TOPS_ROWS,
    bottomsNote: "Form Short Linen thoải mái phần đùi, đã xử lý chống co.",
    bottoms: DEFAULT_BOTTOMS_ROWS,
    updatedAt: "2026-11-16",
  },
  "shop-ha-noi": {
    shopId: "shop-ha-noi",
    topsNote: "Form Polo Dệt kim co giãn nhẹ, phù hợp mặc lớp trong hoặc mặc lẻ.",
    tops: DEFAULT_TOPS_ROWS,
    bottomsNote: "Quần Jogger dài ống 94-102cm bo gấu gọn cổ chân.",
    bottoms: DEFAULT_BOTTOMS_ROWS.map((r, i) => ({ ...r, lengthCm: String(94 + i * 2) })),
    updatedAt: "2026-11-18",
  },
};

export const INITIAL_OPERATIONS_CONFIG: Record<string, SellerOperationsConfig> = {
  "shop-bach-dang": {
    shopId: "shop-bach-dang",
    freeShipThresholdVnd: 199000,
    standardShipFeeVnd: 25000,
    prepSlaHours: 12,
    fastDeliveryEnabled: true,
    fastDeliveryArea: "Nội thành Hải Phòng (giao 2-4 giờ)",
    fastDeliveryCutoff: "Đặt trước 15:00 (Thứ 2 - Thứ 7)",
    return45DaysEnabled: true,
    twoWayExchangeEnabled: true,
    verifiedWorkshopBadge: true,
    returnExceptionNote:
      "Tuân thủ ngoại lệ sàn: không đổi trả hàng Outlet, deal sốc và pack đồ lót đã mở niêm phong.",
  },
  "shop-sai-gon": {
    shopId: "shop-sai-gon",
    freeShipThresholdVnd: 199000,
    standardShipFeeVnd: 22000,
    prepSlaHours: 8,
    fastDeliveryEnabled: true,
    fastDeliveryArea: "Nội thành TP. Hồ Chí Minh (giao 2-4 giờ)",
    fastDeliveryCutoff: "Đặt trước 16:00 hằng ngày",
    return45DaysEnabled: true,
    twoWayExchangeEnabled: true,
    verifiedWorkshopBadge: true,
    returnExceptionNote: "Áp dụng cam kết đổi trả 45 ngày miễn phí trừ sản phẩm Outlet xả kho.",
  },
  "shop-da-nang": {
    shopId: "shop-da-nang",
    freeShipThresholdVnd: 249000,
    standardShipFeeVnd: 25000,
    prepSlaHours: 12,
    fastDeliveryEnabled: false,
    fastDeliveryArea: "Nội thành Đà Nẵng (đang thử nghiệm)",
    fastDeliveryCutoff: "Đặt trước 14:00",
    return45DaysEnabled: true,
    twoWayExchangeEnabled: true,
    verifiedWorkshopBadge: true,
    returnExceptionNote: "Sản phẩm linen còn nguyên tem mác, chưa qua giặt ủi.",
  },
  "shop-ha-noi": {
    shopId: "shop-ha-noi",
    freeShipThresholdVnd: 199000,
    standardShipFeeVnd: 22000,
    prepSlaHours: 12,
    fastDeliveryEnabled: true,
    fastDeliveryArea: "Nội thành Hà Nội (giao 2-4 giờ)",
    fastDeliveryCutoff: "Đặt trước 15:00 hằng ngày",
    return45DaysEnabled: true,
    twoWayExchangeEnabled: true,
    verifiedWorkshopBadge: true,
    returnExceptionNote: "Hỗ trợ bù lẻ chiếc tất thất lạc và đổi size jogger tận nơi 45 ngày.",
  },
};
