import type { PaymentMethod } from "./orders";

export type ShopApprovalStatus = "cho_duyet" | "bo_sung_ho_so" | "da_duyet" | "tu_choi";

export interface ShopApprovalApplication {
  id: string;
  applicationCode: string;
  shopName: string;
  ownerName: string;
  businessLicense: string;
  provinceName: string;
  wardName: string;
  primaryCategory: string;
  initialSkuCount: number;
  submittedAt: string;
  slaHoursLeft: number;
  status: ShopApprovalStatus;
  documentsVerified: boolean;
  bankVerified: boolean;
  note: string;
}

export type DisputeStatus = "moi_tiep_nhan" | "dang_xac_minh" | "cho_phan_hoi" | "da_xu_ly";
export type DisputeTier = "cap_2_tu_gian_hang" | "cap_2_san_thu_ly";
export type VulnerablePriorityGroup = "nguoi_cao_tuoi" | "phu_nu_mang_thai" | "nguoi_khuyet_tat";

export interface AdminDispute {
  id: string;
  caseCode: string;
  orderCode: string;
  buyerName: string;
  shopId: string;
  shopName: string;
  reason: string;
  amount: number; // integer VND
  status: DisputeStatus;
  openedAt: string;
  slaDeadline: string;
  evidenceNote: string;
  tier?: DisputeTier;
  escalatedReason?: string;
  vulnerableGroup?: VulnerablePriorityGroup;
  ackSlaHoursLeft?: number; // 0 when already confirmed within 24h
  ackConfirmedAt?: string;
  resolveWorkingDaysLeft?: number;
  resolveWorkingDaysTotal?: number; // 5 or 7 working days
}

export type ReconciliationStatus = "khop" | "lech_can_xu_ly" | "dang_doi_soat";

export interface PaymentReconciliationRow {
  id: string;
  batchCode: string;
  date: string; // YYYY-MM-DD
  label: string; // dd/mm
  channel: PaymentMethod;
  channelLabel: string;
  txCount: number;
  grossCollected: number; // integer VND
  gatewayFee: number; // integer VND
  platformCommission: number; // integer VND
  refunded: number; // integer VND
  sellerPayable: number; // grossCollected - gatewayFee - platformCommission - refunded
  discrepancy: number; // integer VND (0 when matched)
  status: ReconciliationStatus;
  note: string;
}

export interface CategoryCommissionSetting {
  categorySlug: string;
  categoryName: string;
  commissionPercent: number; // e.g. 5.0
  returnWindowDays: number;
}

export interface PlatformConfigState {
  freeshipMinOrder: number; // integer VND
  maxCoinRedeemPercent: number; // e.g. 20
  settlementHoldDays: number; // e.g. 3
  vietqrEnabled: boolean;
  sandboxWalletEnabled: boolean;
  redisFlashLockEnabled: boolean;
  aiSizeAssistantEnabled: boolean;
  autoApproveLowValueReturns: boolean;
  autoApproveThresholdVnd: number;
  commissions: CategoryCommissionSetting[];
}

export type AuditLogModule =
  | "Gian hàng"
  | "Cấu hình"
  | "Khiếu nại"
  | "Đối soát"
  | "Bảo mật"
  | "Nội dung"
  | "Kiểm duyệt"
  | "Hội viên";

export interface AuditLogEntry {
  id: string;
  timestamp: string;
  actor: string;
  role: "Quản trị viên" | "Vận hành sàn" | "Đối soát tài chính" | "Hệ thống";
  action: string;
  module: AuditLogModule;
  target: string;
  changeSummary: string;
  traceId: string;
}

export const ADMIN_SHOP_APPROVALS: ShopApprovalApplication[] = [
  {
    id: "app-1",
    applicationCode: "HS-261120-04",
    shopName: "Xưởng May An Phước Nam Định",
    ownerName: "Đinh Văn Cường",
    businessLicense: "0601184290",
    provinceName: "Tỉnh Ninh Bình",
    wardName: "Phường Nam Định",
    primaryCategory: "Áo thun",
    initialSkuCount: 24,
    submittedAt: "2026-11-20T00:15:00+07:00",
    slaHoursLeft: 18,
    status: "cho_duyet",
    documentsVerified: true,
    bankVerified: true,
    note: "Đủ giấy phép hộ kinh doanh và tài khoản Vietcombank chính chủ.",
  },
  {
    id: "app-2",
    applicationCode: "HS-261119-03",
    shopName: "Sợi Lanh Hội An Craft",
    ownerName: "Nguyễn Thị Hồng Nhung",
    businessLicense: "4001259831",
    provinceName: "Thành phố Đà Nẵng",
    wardName: "Phường Hội An",
    primaryCategory: "Quần short",
    initialSkuCount: 16,
    submittedAt: "2026-11-19T17:30:00+07:00",
    slaHoursLeft: 11,
    status: "cho_duyet",
    documentsVerified: true,
    bankVerified: true,
    note: "Đã kiểm tra mẫu ảnh nền phẳng và bảng thông số size.",
  },
  {
    id: "app-3",
    applicationCode: "HS-261119-02",
    shopName: "Tất Sợi Bông Bình Dương",
    ownerName: "Lê Minh Triết",
    businessLicense: "3702941105",
    provinceName: "Thành phố Hồ Chí Minh",
    wardName: "Phường Dĩ An",
    primaryCategory: "Tất & phụ kiện",
    initialSkuCount: 18,
    submittedAt: "2026-11-19T14:05:00+07:00",
    slaHoursLeft: 7,
    status: "cho_duyet",
    documentsVerified: true,
    bankVerified: false,
    note: "Chờ đối chiếu tên chủ tài khoản doanh nghiệp trên sao kê.",
  },
  {
    id: "app-4",
    applicationCode: "HS-261118-09",
    shopName: "Basic Bắc Ninh Garment",
    ownerName: "Nguyễn Hữu Thắng",
    businessLicense: "2301094822",
    provinceName: "Tỉnh Bắc Ninh",
    wardName: "Phường Từ Sơn",
    primaryCategory: "Áo polo",
    initialSkuCount: 20,
    submittedAt: "2026-11-18T11:20:00+07:00",
    slaHoursLeft: 26,
    status: "bo_sung_ho_so",
    documentsVerified: false,
    bankVerified: true,
    note: "Ảnh chụp giấy phép kinh doanh bị mờ góc mã số thuế, đã gửi yêu cầu tải lại.",
  },
  {
    id: "app-5",
    applicationCode: "HS-261116-05",
    shopName: "Dệt Kim Hà Nội",
    ownerName: "Phạm Thị Thu Hương",
    businessLicense: "0108472915",
    provinceName: "Thành phố Hà Nội",
    wardName: "Phường Hai Bà Trưng",
    primaryCategory: "Quần jogger",
    initialSkuCount: 28,
    submittedAt: "2026-11-16T09:00:00+07:00",
    slaHoursLeft: 0,
    status: "da_duyet",
    documentsVerified: true,
    bankVerified: true,
    note: "Đã kích hoạt gian hàng và mở kết nối vận chuyển 34 tỉnh.",
  },
  {
    id: "app-6",
    applicationCode: "HS-261115-01",
    shopName: "Kho Sỉ Quần Áo Tân Bình",
    ownerName: "Võ Thành Lộc",
    businessLicense: "0317920441",
    provinceName: "Thành phố Hồ Chí Minh",
    wardName: "Phường Tân Định",
    primaryCategory: "Áo thun",
    initialSkuCount: 12,
    submittedAt: "2026-11-15T15:40:00+07:00",
    slaHoursLeft: 0,
    status: "tu_choi",
    documentsVerified: false,
    bankVerified: false,
    note: "Từ chối do hình ảnh sản phẩm chứa nhãn hiệu thương mại không có giấy ủy quyền.",
  },
];

export const ADMIN_DISPUTES: AdminDispute[] = [
  {
    id: "dsp-1",
    caseCode: "KN-261119-14",
    orderCode: "NM-261115-053",
    buyerName: "Đỗ Minh Hiếu",
    shopId: "shop-bach-dang",
    shopName: "Xưởng Dệt Bạch Đằng",
    reason: "Giao nhầm cỡ 2XL thay vì XL, người mua yêu cầu đổi size 2 chiều miễn phí.",
    amount: 179000,
    status: "moi_tiep_nhan",
    openedAt: "2026-11-19T18:20:00+07:00",
    slaDeadline: "2026-11-26T18:20:00+07:00",
    evidenceNote: "Đã đính kèm 2 ảnh nhãn cổ áo và phiếu đóng gói kho.",
    tier: "cap_2_tu_gian_hang",
    escalatedReason: "Gian hàng chưa xác nhận đổi size 2 chiều sau 24 giờ khiếu nại cấp 1.",
    vulnerableGroup: "nguoi_cao_tuoi",
    ackSlaHoursLeft: 5,
    resolveWorkingDaysLeft: 4,
    resolveWorkingDaysTotal: 5,
  },
  {
    id: "dsp-2",
    caseCode: "KN-261119-11",
    orderCode: "NM-261108-044",
    buyerName: "Nguyễn Minh Quân",
    shopId: "shop-ha-noi",
    shopName: "Dệt Kim Hà Nội",
    reason: "Yêu cầu đổi size M sang L quá 24 giờ chưa thấy kho xác nhận thu hồi.",
    amount: 369000,
    status: "dang_xac_minh",
    openedAt: "2026-11-19T10:05:00+07:00",
    slaDeadline: "2026-11-26T10:05:00+07:00",
    evidenceNote: "Sàn đã nhắc gian hàng xác nhận mã vận đơn hoàn chiều về.",
    tier: "cap_2_tu_gian_hang",
    escalatedReason: "Người mua yêu cầu trọng tài sàn sau khi gian hàng từ chối đổi tận nơi.",
    ackSlaHoursLeft: 0,
    ackConfirmedAt: "2026-11-19T13:10:00+07:00",
    resolveWorkingDaysLeft: 4,
    resolveWorkingDaysTotal: 5,
  },
  {
    id: "dsp-3",
    caseCode: "KN-261118-08",
    orderCode: "NM-261114-039",
    buyerName: "Trần Thanh Tùng",
    shopId: "shop-sai-gon",
    shopName: "Kho Basic Sài Gòn",
    reason: "Thiếu 1 áo thun Dry-Fit trong kiện hàng gồm 3 sản phẩm.",
    amount: 159000,
    status: "dang_xac_minh",
    openedAt: "2026-11-18T16:40:00+07:00",
    slaDeadline: "2026-11-25T16:40:00+07:00",
    evidenceNote: "Đang đối chiếu cân nặng kiện lúc xuất kho và video đóng gói.",
    tier: "cap_2_tu_gian_hang",
    escalatedReason: "Hai bên không thống nhất biên bản đồng kiểm lúc giao hàng.",
    ackSlaHoursLeft: 0,
    ackConfirmedAt: "2026-11-18T19:05:00+07:00",
    resolveWorkingDaysLeft: 3,
    resolveWorkingDaysTotal: 5,
  },
  {
    id: "dsp-4",
    caseCode: "KN-261118-05",
    orderCode: "NM-261113-027",
    buyerName: "Lê Thị Kim Oanh",
    shopId: "shop-da-nang",
    shopName: "Sợi Tự Nhiên Đà Nẵng",
    reason: "Màu thực tế của quần short linen đậm hơn mô tả trên màn hình.",
    amount: 279000,
    status: "cho_phan_hoi",
    openedAt: "2026-11-18T09:15:00+07:00",
    slaDeadline: "2026-11-27T17:00:00+07:00",
    evidenceNote: "Gian hàng đồng ý đổi sang màu Be cát, chờ người mua xác nhận.",
    tier: "cap_2_tu_gian_hang",
    escalatedReason: "Chuyển cấp 2 để kiểm định tiêu chuẩn ảnh nền phẳng của gian hàng.",
    vulnerableGroup: "phu_nu_mang_thai",
    ackSlaHoursLeft: 0,
    ackConfirmedAt: "2026-11-18T11:00:00+07:00",
    resolveWorkingDaysLeft: 5,
    resolveWorkingDaysTotal: 7,
  },
  {
    id: "dsp-7",
    caseCode: "KN-261119-16",
    orderCode: "NM-261117-061",
    buyerName: "Bùi Thị Lan Anh",
    shopId: "shop-sai-gon",
    shopName: "Kho Basic Sài Gòn",
    reason: "Trừ Xu Hội viên Nexus nhưng đơn thanh toán lại qua VietQR báo lỗi giữ tiền.",
    amount: 420000,
    status: "moi_tiep_nhan",
    openedAt: "2026-11-19T22:10:00+07:00",
    slaDeadline: "2026-11-27T17:00:00+07:00",
    evidenceNote: "Liên quan sổ cái cổng VietQR và hoàn lại 42.000 Xu đã khóa tạm.",
    tier: "cap_2_san_thu_ly",
    escalatedReason: "Tranh chấp giao dịch thanh toán & Xu thuộc thẩm quyền trực tiếp của sàn.",
    ackSlaHoursLeft: 9,
    resolveWorkingDaysLeft: 6,
    resolveWorkingDaysTotal: 7,
  },
  {
    id: "dsp-5",
    caseCode: "KN-261116-03",
    orderCode: "NM-261111-019",
    buyerName: "Phạm Gia Bảo",
    shopId: "shop-ha-noi",
    shopName: "Dệt Kim Hà Nội",
    reason: "Lỗi tuột đường chỉ bo cổ chân quần jogger sau lần giặt đầu.",
    amount: 349000,
    status: "da_xu_ly",
    openedAt: "2026-11-16T14:50:00+07:00",
    slaDeadline: "2026-11-23T14:50:00+07:00",
    evidenceNote: "Đã hoàn 349.000₫ về Ví ZaloPay người mua và ghi nợ gian hàng.",
    tier: "cap_2_tu_gian_hang",
    escalatedReason: "Gian hàng từ chối đổi do đã giặt, sàn phân xử lỗi đường may trong 7 ngày.",
    ackSlaHoursLeft: 0,
    ackConfirmedAt: "2026-11-16T16:15:00+07:00",
    resolveWorkingDaysLeft: 0,
    resolveWorkingDaysTotal: 5,
  },
  {
    id: "dsp-6",
    caseCode: "KN-261115-02",
    orderCode: "NM-261110-011",
    buyerName: "Huỳnh Ngọc Trinh",
    shopId: "shop-bach-dang",
    shopName: "Xưởng Dệt Bạch Đằng",
    reason: "Trừ tiền VietQR hai lần do người mua quét lại mã cũ.",
    amount: 229000,
    status: "da_xu_ly",
    openedAt: "2026-11-15T11:30:00+07:00",
    slaDeadline: "2026-11-22T11:30:00+07:00",
    evidenceNote: "Hệ thống chống trùng mã giao dịch đã hoàn tự động khoản thừa.",
    tier: "cap_2_san_thu_ly",
    escalatedReason: "Giao dịch trùng xác nhận ngân hàng được sàn trực tiếp đối soát hoàn tiền.",
    ackSlaHoursLeft: 0,
    ackConfirmedAt: "2026-11-15T12:05:00+07:00",
    resolveWorkingDaysLeft: 0,
    resolveWorkingDaysTotal: 5,
  },
];

function makeReconRow(
  base: Omit<PaymentReconciliationRow, "sellerPayable">
): PaymentReconciliationRow {
  const sellerPayable =
    base.grossCollected - base.gatewayFee - base.platformCommission - base.refunded;
  return {
    ...base,
    sellerPayable,
  };
}

export const ADMIN_RECONCILIATION: PaymentReconciliationRow[] = [
  makeReconRow({
    id: "rc-1",
    batchCode: "DS-261119-QR",
    date: "2026-11-19",
    label: "19/11",
    channel: "vietqr",
    channelLabel: "VietQR",
    txCount: 48,
    grossCollected: 18420000,
    gatewayFee: 184200,
    platformCommission: 921000,
    refunded: 179000,
    discrepancy: 0,
    status: "khop",
    note: "Khớp 48/48 giao dịch sao kê ngân hàng.",
  }),
  makeReconRow({
    id: "rc-2",
    batchCode: "DS-261119-COD",
    date: "2026-11-19",
    label: "19/11",
    channel: "cod",
    channelLabel: "COD thu hộ",
    txCount: 31,
    grossCollected: 11260000,
    gatewayFee: 225200,
    platformCommission: 563000,
    refunded: 0,
    discrepancy: 149000,
    status: "lech_can_xu_ly",
    note: "Lệch 149.000₫ ở vận đơn NMLOG-8839201 do đối tác vận chuyển cập nhật chậm.",
  }),
  makeReconRow({
    id: "rc-3",
    batchCode: "DS-261119-WAL",
    date: "2026-11-19",
    label: "19/11",
    channel: "vi_sandbox",
    channelLabel: "Ví ZaloPay",
    txCount: 22,
    grossCollected: 7940000,
    gatewayFee: 79400,
    platformCommission: 397000,
    refunded: 0,
    discrepancy: 0,
    status: "khop",
    note: "Khớp sổ cái ghi kép tức thời.",
  }),
  makeReconRow({
    id: "rc-4",
    batchCode: "DS-261118-QR",
    date: "2026-11-18",
    label: "18/11",
    channel: "vietqr",
    channelLabel: "VietQR",
    txCount: 64,
    grossCollected: 24860000,
    gatewayFee: 248600,
    platformCommission: 1243000,
    refunded: 0,
    discrepancy: 0,
    status: "khop",
    note: "Phiên Flash sale 18/11 khớp toàn bộ sao kê xác nhận tự động.",
  }),
  makeReconRow({
    id: "rc-5",
    batchCode: "DS-261118-COD",
    date: "2026-11-18",
    label: "18/11",
    channel: "cod",
    channelLabel: "COD thu hộ",
    txCount: 39,
    grossCollected: 14190000,
    gatewayFee: 283800,
    platformCommission: 709500,
    refunded: 0,
    discrepancy: 0,
    status: "khop",
    note: "Khớp bảng kê thu hộ nhà vận chuyển.",
  }),
  makeReconRow({
    id: "rc-6",
    batchCode: "DS-261117-QR",
    date: "2026-11-17",
    label: "17/11",
    channel: "vietqr",
    channelLabel: "VietQR",
    txCount: 41,
    grossCollected: 15640000,
    gatewayFee: 156400,
    platformCommission: 782000,
    refunded: 0,
    discrepancy: 0,
    status: "khop",
    note: "Khớp 41/41 giao dịch.",
  }),
  makeReconRow({
    id: "rc-7",
    batchCode: "DS-261117-COD",
    date: "2026-11-17",
    label: "17/11",
    channel: "cod",
    channelLabel: "COD thu hộ",
    txCount: 27,
    grossCollected: 9850000,
    gatewayFee: 197000,
    platformCommission: 492500,
    refunded: 229000,
    discrepancy: 0,
    status: "khop",
    note: "Khớp sau bù trừ 1 đơn hoàn trả.",
  }),
  makeReconRow({
    id: "rc-8",
    batchCode: "DS-261116-QR",
    date: "2026-11-16",
    label: "16/11",
    channel: "vietqr",
    channelLabel: "VietQR",
    txCount: 38,
    grossCollected: 14280000,
    gatewayFee: 142800,
    platformCommission: 714000,
    refunded: 0,
    discrepancy: 0,
    status: "khop",
    note: "Đã chốt kỳ thanh toán cho 4 gian hàng.",
  }),
  makeReconRow({
    id: "rc-9",
    batchCode: "DS-261115-ALL",
    date: "2026-11-15",
    label: "15/11",
    channel: "vi_sandbox",
    channelLabel: "Ví ZaloPay",
    txCount: 35,
    grossCollected: 13120000,
    gatewayFee: 131200,
    platformCommission: 656000,
    refunded: 349000,
    discrepancy: 0,
    status: "khop",
    note: "Khớp sổ cái ghi kép, đã ghi nhận 1 giao dịch hoàn tiền.",
  }),
];

export interface AdminReconciliationSummary {
  totalGross: number;
  totalGatewayFee: number;
  totalCommission: number;
  totalRefunded: number;
  totalSellerPayable: number;
  totalDiscrepancy: number;
  totalTxCount: number;
  matchedBatches: number;
  flaggedBatches: number;
}

export function computeReconciliationSummary(
  rows: PaymentReconciliationRow[] = ADMIN_RECONCILIATION
): AdminReconciliationSummary {
  return rows.reduce<AdminReconciliationSummary>(
    (acc, r) => {
      acc.totalGross += r.grossCollected;
      acc.totalGatewayFee += r.gatewayFee;
      acc.totalCommission += r.platformCommission;
      acc.totalRefunded += r.refunded;
      acc.totalSellerPayable += r.sellerPayable;
      acc.totalDiscrepancy += r.discrepancy;
      acc.totalTxCount += r.txCount;
      if (r.status === "khop") acc.matchedBatches += 1;
      if (r.status === "lech_can_xu_ly") acc.flaggedBatches += 1;
      return acc;
    },
    {
      totalGross: 0,
      totalGatewayFee: 0,
      totalCommission: 0,
      totalRefunded: 0,
      totalSellerPayable: 0,
      totalDiscrepancy: 0,
      totalTxCount: 0,
      matchedBatches: 0,
      flaggedBatches: 0,
    }
  );
}

export interface AdminDailyPlatformPoint {
  date: string;
  label: string;
  grossCollected: number;
  sellerPayable: number;
  platformCommission: number;
  txCount: number;
}

export function computeAdminDailySeries(
  rows: PaymentReconciliationRow[] = ADMIN_RECONCILIATION
): AdminDailyPlatformPoint[] {
  const byDate = new Map<string, AdminDailyPlatformPoint>();
  for (const r of rows) {
    const cur = byDate.get(r.date) ?? {
      date: r.date,
      label: r.label,
      grossCollected: 0,
      sellerPayable: 0,
      platformCommission: 0,
      txCount: 0,
    };
    cur.grossCollected += r.grossCollected;
    cur.sellerPayable += r.sellerPayable;
    cur.platformCommission += r.platformCommission;
    cur.txCount += r.txCount;
    byDate.set(r.date, cur);
  }
  return [...byDate.values()].sort((a, b) => a.date.localeCompare(b.date));
}

export const INITIAL_PLATFORM_CONFIG: PlatformConfigState = {
  freeshipMinOrder: 249000,
  maxCoinRedeemPercent: 20,
  settlementHoldDays: 3,
  vietqrEnabled: true,
  sandboxWalletEnabled: true,
  redisFlashLockEnabled: true,
  aiSizeAssistantEnabled: true,
  autoApproveLowValueReturns: false,
  autoApproveThresholdVnd: 150000,
  commissions: [
    {
      categorySlug: "ao-thun",
      categoryName: "Áo thun",
      commissionPercent: 5.0,
      returnWindowDays: 45,
    },
    {
      categorySlug: "ao-polo",
      categoryName: "Áo polo",
      commissionPercent: 5.0,
      returnWindowDays: 45,
    },
    {
      categorySlug: "quan-short",
      categoryName: "Quần short",
      commissionPercent: 5.0,
      returnWindowDays: 45,
    },
    {
      categorySlug: "quan-jogger",
      categoryName: "Quần jogger",
      commissionPercent: 5.5,
      returnWindowDays: 45,
    },
    { categorySlug: "do-lot", categoryName: "Đồ lót", commissionPercent: 6.0, returnWindowDays: 7 },
    {
      categorySlug: "tat-phu-kien",
      categoryName: "Tất & phụ kiện",
      commissionPercent: 5.0,
      returnWindowDays: 45,
    },
  ],
};

export const ADMIN_AUDIT_LOGS: AuditLogEntry[] = [
  {
    id: "aud-1",
    timestamp: "2026-11-20T01:12:00+07:00",
    actor: "Trần Quốc Huy",
    role: "Đối soát tài chính",
    action: "Đánh dấu sai lệch lô COD",
    module: "Đối soát",
    target: "DS-261119-COD",
    changeSummary: "Ghi nhận chênh lệch 149.000₫ ở vận đơn NMLOG-8839201, mở phiếu kiểm tra.",
    traceId: "tr-98f1a4c2",
  },
  {
    id: "aud-2",
    timestamp: "2026-11-19T21:45:00+07:00",
    actor: "Hệ thống",
    role: "Hệ thống",
    action: "Chốt đối soát VietQR cuối ngày",
    module: "Đối soát",
    target: "DS-261119-QR",
    changeSummary: "Khớp 48/48 giao dịch, tổng thu 18.420.000₫, sai lệch 0₫.",
    traceId: "tr-98e4b710",
  },
  {
    id: "aud-3",
    timestamp: "2026-11-19T18:35:00+07:00",
    actor: "Nguyễn Phương Thảo",
    role: "Vận hành sàn",
    action: "Tiếp nhận khiếu nại sai cỡ",
    module: "Khiếu nại",
    target: "KN-261119-14",
    changeSummary: "Chuyển trạng thái sang Mới tiếp nhận, tạm giữ 179.000₫ trên sổ cái gian hàng.",
    traceId: "tr-98d03f8a",
  },
  {
    id: "aud-4",
    timestamp: "2026-11-19T16:10:00+07:00",
    actor: "Trần Quốc Huy",
    role: "Quản trị viên",
    action: "Yêu cầu bổ sung giấy phép",
    module: "Gian hàng",
    target: "HS-261118-09",
    changeSummary: "Đổi trạng thái Basic Bắc Ninh Garment sang Bổ sung hồ sơ do ảnh MST mờ.",
    traceId: "tr-98c19e42",
  },
  {
    id: "aud-5",
    timestamp: "2026-11-19T11:20:00+07:00",
    actor: "Nguyễn Phương Thảo",
    role: "Vận hành sàn",
    action: "Cập nhật ngưỡng freeship toàn sàn",
    module: "Cấu hình",
    target: "freeshipMinOrder",
    changeSummary: "Điều chỉnh từ 299.000₫ xuống 249.000₫ cho chiến dịch tháng 11.",
    traceId: "tr-98a77d19",
  },
  {
    id: "aud-6",
    timestamp: "2026-11-18T19:05:00+07:00",
    actor: "Hệ thống",
    role: "Hệ thống",
    action: "Kích hoạt khóa suất Flash sale",
    module: "Cấu hình",
    target: "slot-12",
    changeSummary: "Mở 770 suất Flash sale khung 12:00 với cơ chế khóa suất tức thời.",
    traceId: "tr-98812c04",
  },
  {
    id: "aud-7",
    timestamp: "2026-11-17T15:30:00+07:00",
    actor: "Trần Quốc Huy",
    role: "Đối soát tài chính",
    action: "Duyệt lệnh rút tiền gian hàng",
    module: "Đối soát",
    target: "PO-261117-BD",
    changeSummary: "Giải ngân 10.000.000₫ cho Xưởng Dệt Bạch Đằng qua Vietcombank *4821.",
    traceId: "tr-98605a91",
  },
  {
    id: "aud-8",
    timestamp: "2026-11-16T17:15:00+07:00",
    actor: "Nguyễn Phương Thảo",
    role: "Vận hành sàn",
    action: "Hoàn tiền khiếu nại lỗi đường may",
    module: "Khiếu nại",
    target: "KN-261116-03",
    changeSummary: "Hoàn 349.000₫ về Ví ZaloPay người mua Phạm Gia Bảo, đóng hồ sơ.",
    traceId: "tr-98491b33",
  },
  {
    id: "aud-9",
    timestamp: "2026-11-16T10:40:00+07:00",
    actor: "Trần Quốc Huy",
    role: "Quản trị viên",
    action: "Phê duyệt mở gian hàng",
    module: "Gian hàng",
    target: "HS-261116-05",
    changeSummary: "Kích hoạt Dệt Kim Hà Nội với 28 mã SKU ban đầu, phí sàn 5,0%.",
    traceId: "tr-98332e77",
  },
  {
    id: "aud-10",
    timestamp: "2026-11-15T16:05:00+07:00",
    actor: "Trần Quốc Huy",
    role: "Quản trị viên",
    action: "Từ chối hồ sơ đăng ký gian hàng",
    module: "Gian hàng",
    target: "HS-261115-01",
    changeSummary: "Từ chối Kho Sỉ Quần Áo Tân Bình do thiếu ủy quyền thương hiệu.",
    traceId: "tr-98109f5c",
  },
  {
    id: "aud-11",
    timestamp: "2026-11-19T14:20:00+07:00",
    actor: "Nguyễn Phương Thảo",
    role: "Vận hành sàn",
    action: "Ghim từ khóa chiến dịch Sợi Xanh Nexus",
    module: "Nội dung",
    target: "KW-SOI-XANH",
    changeSummary: "Ghim cụm 'soi xanh nexus' lên top 5 tìm kiếm xu hướng và cập nhật sao kê CSR.",
    traceId: "tr-98b4410e",
  },
  {
    id: "aud-12",
    timestamp: "2026-11-19T13:05:00+07:00",
    actor: "Nguyễn Phương Thảo",
    role: "Vận hành sàn",
    action: "Ẩn đánh giá nghi vấn spam đường link",
    module: "Kiểm duyệt",
    target: "DG-261119-02",
    changeSummary: "Ẩn đánh giá chứa liên kết dẫn ra ngoài sàn trên SKU BD-AT01-TRANG-L.",
    traceId: "tr-98b10c9d",
  },
  {
    id: "aud-13",
    timestamp: "2026-11-18T15:40:00+07:00",
    actor: "Trần Quốc Huy",
    role: "Đối soát tài chính",
    action: "Phát hành lô thẻ quà tặng sinh viên .edu",
    module: "Hội viên",
    target: "NXGIFT-EDU-06",
    changeSummary: "Phát hành quỹ 1.500.000₫ (hạn 10 tháng) và ghi nhận bút toán kép TK 3387.",
    traceId: "tr-987a62f1",
  },
];

/* ============================================================================
   ĐỢT 2 — NỘI DUNG, DANH MỤC, TÌM KIẾM & CHIẾN DỊCH (/admin/noi-dung)
   ============================================================================ */

export interface AdminAnnouncementConfig {
  enabled: boolean;
  message: string;
  linkLabel: string;
  linkHref: string;
  scheduleLabel: string;
}

export interface AdminMegaMenuColumn {
  id: string;
  title: string;
  items: string[];
}

export interface AdminMegaMenuBranch {
  id: string;
  label: string;
  slug: string;
  active: boolean;
  priority: number;
  bannerSlotTitle: string;
  bannerSlotCta: string;
  columns: AdminMegaMenuColumn[];
}

export interface AdminTaxonomyNode {
  id: string;
  code: string;
  level1: string;
  level2: string;
  level3: string;
  skuCount: number;
  requiredAttrCount: number;
  active: boolean;
}

export interface AdminFabricDictionaryItem {
  id: string;
  code: string;
  name: string;
  composition: string;
  gsmRange: string;
  features: string[];
  careStandard: string;
  skuCount: number;
  active: boolean;
}

export interface AdminCategoryAttributeRule {
  categorySlug: string;
  categoryName: string;
  requiredFields: string[];
  enforceBeforePublish: boolean;
}

export interface AdminSizeChartRow {
  size: string;
  heightRange: string;
  weightRange: string;
  measurement: string;
}

export interface AdminSizeChartTemplate {
  id: string;
  groupName: string;
  appliesTo: string;
  toleranceNote: string;
  rows: AdminSizeChartRow[];
}

export interface AdminTrendingKeyword {
  id: string;
  term: string;
  accentless: string;
  searches7d: number;
  ctrPercent: number;
  pinnedCampaign: boolean;
  status: "hien_thi" | "bi_chan";
  note: string;
}

export interface AdminSearchSynonym {
  id: string;
  canonical: string;
  accentless: string;
  synonyms: string[];
  active: boolean;
}

export interface AdminSearchRankingConfig {
  relevanceWeight: number; // %
  reviewFitWeight: number; // %
  slaOnTimeWeight: number; // %
  sales30dWeight: number; // %
  shopTrustWeight: number; // %
  penalizeInflatedListPrice: boolean;
  hideBaitSkuOutliers: boolean;
  boostFullSizeAvailability: boolean;
}

export interface AdminHomepageBlock {
  id: string;
  code: string;
  title: string;
  slotType: string;
  selectionRule: string;
  maxItems: number;
  order: number;
  active: boolean;
}

export interface AdminCsrStatementBatch {
  id: string;
  batchCode: string;
  date: string;
  shopName: string;
  orderCount: number;
  contributionVnd: number; // integer VND
  verified: boolean;
}

export interface AdminFabricLandingTab {
  id: string;
  title: string;
  fabricCode: string;
  skuCount: number;
  published: boolean;
}

export interface AdminCampaignCsrState {
  campaignCode: string;
  title: string;
  summary: string;
  contributionRatePercent: number;
  targetVnd: number; // integer VND
  fabricTabs: AdminFabricLandingTab[];
  statements: AdminCsrStatementBatch[];
}

export type EditorialStatus = "cho_duyet" | "da_dang" | "can_chinh_sua";

export interface AdminEditorialSubmission {
  id: string;
  code: string;
  title: string;
  shopName: string;
  category: string;
  embeddedSkuCount: number;
  submittedAt: string;
  status: EditorialStatus;
  note: string;
}

export type BadgeApprovalStatus = "cho_duyet" | "da_cap" | "tu_choi";
export type PlatformBadgeKind = "pre_order" | "gian_hang_chinh_hang" | "doi_tra_45_ngay";

export interface AdminBadgeRequest {
  id: string;
  code: string;
  shopName: string;
  badgeKind: PlatformBadgeKind;
  targetScope: string;
  evidenceSummary: string;
  submittedAt: string;
  status: BadgeApprovalStatus;
  note: string;
}

export interface AdminContentState {
  announcement: AdminAnnouncementConfig;
  megaMenu: AdminMegaMenuBranch[];
  taxonomy: AdminTaxonomyNode[];
  fabrics: AdminFabricDictionaryItem[];
  attributeRules: AdminCategoryAttributeRule[];
  sizeTemplates: AdminSizeChartTemplate[];
  trendingKeywords: AdminTrendingKeyword[];
  synonyms: AdminSearchSynonym[];
  rankingConfig: AdminSearchRankingConfig;
  homepageBlocks: AdminHomepageBlock[];
  csrCampaign: AdminCampaignCsrState;
  editorialSubmissions: AdminEditorialSubmission[];
  badgeRequests: AdminBadgeRequest[];
}

export const INITIAL_ADMIN_CONTENT: AdminContentState = {
  announcement: {
    enabled: true,
    message: "Miễn phí vận chuyển toàn sàn từ 249.000₫ · Đổi trả miễn phí 7 ngày tận nơi",
    linkLabel: "Xem chính sách",
    linkHref: "/chinh-sach/doi-tra",
    scheduleLabel: "Hiệu lực đến 31/12/2026",
  },
  megaMenu: [
    {
      id: "mm-nam",
      label: "Đồ nam",
      slug: "do-nam",
      active: true,
      priority: 1,
      bannerSlotTitle: "BST Cotton Compact 220gsm",
      bannerSlotCta: "Xem áo cơ bản",
      columns: [
        {
          id: "c-nam-loai",
          title: "Loại đồ",
          items: ["Áo thun cơ bản", "Áo polo pique", "Quần short", "Quần jogger", "Đồ lót", "Tất"],
        },
        {
          id: "c-nam-hc",
          title: "Hoàn cảnh mặc",
          items: ["Mặc hằng ngày", "Công sở gọn nhẹ", "Du lịch cuối tuần"],
        },
        {
          id: "c-nam-tt",
          title: "Môn thể thao",
          items: ["Chạy bộ", "Tập gym", "Cầu lông & Pickleball"],
        },
      ],
    },
    {
      id: "mm-nu",
      label: "Đồ nữ",
      slug: "do-nu",
      active: true,
      priority: 2,
      bannerSlotTitle: "Đồ cơ bản mềm nhẹ Sợi Xanh",
      bannerSlotCta: "Khám phá ngay",
      columns: [
        {
          id: "c-nu-loai",
          title: "Loại đồ",
          items: ["Áo thun unisex", "Áo polo nhẹ", "Quần short linen", "Áo khoác gió"],
        },
        {
          id: "c-nu-hc",
          title: "Hoàn cảnh mặc",
          items: ["Mặc ở nhà", "Đi làm năng động", "Vận động nhẹ"],
        },
        {
          id: "c-nu-tt",
          title: "Môn thể thao",
          items: ["Chạy bộ", "Yoga & Pilates"],
        },
      ],
    },
    {
      id: "mm-tt",
      label: "Thể thao",
      slug: "the-thao",
      active: true,
      priority: 3,
      bannerSlotTitle: "Dry-Fit Micro-mesh thoát ẩm nhanh",
      bannerSlotCta: "Chọn đồ chạy",
      columns: [
        {
          id: "c-tt-mon",
          title: "Theo bộ môn",
          items: ["Chạy bộ đường dài", "Phòng gym", "Đạp xe ngoài trời"],
        },
        {
          id: "c-tt-vai",
          title: "Công nghệ vải",
          items: ["Dry-Fit thoáng khí", "Co giãn 4 chiều", "Nhanh khô chống mùi"],
        },
      ],
    },
    {
      id: "mm-tapchi",
      label: "Sổ tay vải & CSR",
      slug: "tap-chi",
      active: true,
      priority: 4,
      bannerSlotTitle: "Chiến dịch Sợi Xanh Nexus",
      bannerSlotCta: "Xem sao kê",
      columns: [
        {
          id: "c-tc-nd",
          title: "Nội dung biên tập",
          items: ["Sổ tay vải & Phối đồ", "Hướng dẫn chọn size", "Mẹo giặt ủi bền màu"],
        },
        {
          id: "c-tc-csr",
          title: "Cộng đồng & chuẩn sàn",
          items: ["Sợi Xanh Nexus", "Đổi trả 7 ngày", "Hồ sơ xưởng đạt chuẩn"],
        },
      ],
    },
  ],
  taxonomy: [
    {
      id: "tx-1",
      code: "DM-01",
      level1: "Áo cơ bản",
      level2: "Áo thun",
      level3: "Cotton Compact · Dry-Fit · Oversize",
      skuCount: 64,
      requiredAttrCount: 7,
      active: true,
    },
    {
      id: "tx-2",
      code: "DM-02",
      level1: "Áo cơ bản",
      level2: "Áo polo",
      level3: "Pique Cotton · Dệt kim công sở",
      skuCount: 38,
      requiredAttrCount: 7,
      active: true,
    },
    {
      id: "tx-3",
      code: "DM-03",
      level1: "Quần cơ bản",
      level2: "Quần short",
      level3: "Short Daily · Short Chạy bộ · Short Linen",
      skuCount: 42,
      requiredAttrCount: 6,
      active: true,
    },
    {
      id: "tx-4",
      code: "DM-04",
      level1: "Quần cơ bản",
      level2: "Quần jogger & dài",
      level3: "French Terry · Tech Stretch",
      skuCount: 29,
      requiredAttrCount: 6,
      active: true,
    },
    {
      id: "tx-5",
      code: "DM-05",
      level1: "Nội y & Tất",
      level2: "Đồ lót nam",
      level3: "Boxer Modal · Brief Sợi Tre (Pack 3)",
      skuCount: 24,
      requiredAttrCount: 6,
      active: true,
    },
    {
      id: "tx-6",
      code: "DM-06",
      level1: "Nội y & Tất",
      level2: "Tất & phụ kiện",
      level3: "Tất Cotton cổ trung · Tất thể thao cổ thấp",
      skuCount: 31,
      requiredAttrCount: 5,
      active: true,
    },
  ],
  fabrics: [
    {
      id: "fab-1",
      code: "COT-220",
      name: "Cotton Compact 220gsm",
      composition: "100% Cotton sợi chải kỹ",
      gsmRange: "210–230 gsm",
      features: ["Ít xù lông", "Thấm hút tốt", "Đứng phom"],
      careStandard: "Giặt nước mát 30°C, phơi râm",
      skuCount: 46,
      active: true,
    },
    {
      id: "fab-2",
      code: "DRY-150",
      name: "Dry-Fit Micro-mesh",
      composition: "92% Recycled Poly · 8% Spandex",
      gsmRange: "140–160 gsm",
      features: ["Thoát ẩm nhanh", "Co giãn 4 chiều", "Nhẹ tênh"],
      careStandard: "Không dùng nước xả vải, không ủi nhiệt cao",
      skuCount: 34,
      active: true,
    },
    {
      id: "fab-3",
      code: "PIQ-210",
      name: "Pique Cotton thoáng khí",
      composition: "95% Cotton · 5% Spandex",
      gsmRange: "200–220 gsm",
      features: ["Dệt mắt chim", "Giữ cổ đứng", "Ít nhăn"],
      careStandard: "Lộn trái khi giặt máy chế độ nhẹ",
      skuCount: 28,
      active: true,
    },
    {
      id: "fab-4",
      code: "MOD-180",
      name: "Modal Sợi Sồi & Sợi Tre",
      composition: "93% Modal/Bamboo · 7% Elastane",
      gsmRange: "170–190 gsm",
      features: ["Mềm mát da", "Kháng khuẩn tự nhiên", "Đàn hồi cạp"],
      careStandard: "Giặt túi lưới, không sấy nhiệt cao",
      skuCount: 24,
      active: true,
    },
    {
      id: "fab-5",
      code: "LIN-195",
      name: "Linen pha Cotton",
      composition: "55% Linen · 45% Cotton",
      gsmRange: "185–205 gsm",
      features: ["Thoáng mát ngày hè", "Giảm nhăn tự nhiên"],
      careStandard: "Giặt nhẹ, giũ phẳng trước khi phơi",
      skuCount: 18,
      active: true,
    },
  ],
  attributeRules: [
    {
      categorySlug: "ao-thun",
      categoryName: "Áo thun",
      requiredFields: ["Chất liệu", "GSM", "Form dáng", "Hoàn cảnh", "Mã màu chuẩn", "Bảng size"],
      enforceBeforePublish: true,
    },
    {
      categorySlug: "ao-polo",
      categoryName: "Áo polo",
      requiredFields: ["Chất liệu", "GSM", "Form dáng", "Thông số mẫu mặc", "Bảng size"],
      enforceBeforePublish: true,
    },
    {
      categorySlug: "quan-short",
      categoryName: "Quần short",
      requiredFields: ["Chất liệu", "Chiều dài ống", "Hoàn cảnh / Môn thể thao", "Bảng size"],
      enforceBeforePublish: true,
    },
    {
      categorySlug: "do-lot",
      categoryName: "Đồ lót",
      requiredFields: ["Chất liệu sợi", "Quy cách pack niêm phong", "Hướng dẫn bảo quản"],
      enforceBeforePublish: true,
    },
  ],
  sizeTemplates: [
    {
      id: "sz-ao",
      groupName: "Áo cơ bản (Thun & Polo)",
      appliesTo: "XS – 4XL · Chuẩn phom Việt Nam",
      toleranceNote: "Sai số xưởng cho phép ±1,5 cm; hỗ trợ đổi size 2 chiều trong 7 ngày.",
      rows: [
        {
          size: "S",
          heightRange: "1m58 – 1m65",
          weightRange: "50 – 57 kg",
          measurement: "Ngực 94 cm",
        },
        {
          size: "M",
          heightRange: "1m64 – 1m71",
          weightRange: "58 – 65 kg",
          measurement: "Ngực 100 cm",
        },
        {
          size: "L",
          heightRange: "1m70 – 1m77",
          weightRange: "66 – 74 kg",
          measurement: "Ngực 106 cm",
        },
        {
          size: "XL",
          heightRange: "1m75 – 1m82",
          weightRange: "75 – 83 kg",
          measurement: "Ngực 112 cm",
        },
        {
          size: "2XL",
          heightRange: "1m78 – 1m86",
          weightRange: "84 – 92 kg",
          measurement: "Ngực 118 cm",
        },
      ],
    },
    {
      id: "sz-quan",
      groupName: "Quần cơ bản (Short & Jogger)",
      appliesTo: "Cỡ 29 – 34 hoặc S – 3XL",
      toleranceNote: "Cạp chun có độ giãn +4 cm; người bụng đầy ưu tiên tăng 1 cỡ.",
      rows: [
        {
          size: "29 / S",
          heightRange: "1m58 – 1m66",
          weightRange: "52 – 59 kg",
          measurement: "Eo 74 cm",
        },
        {
          size: "30 / M",
          heightRange: "1m65 – 1m72",
          weightRange: "60 – 67 kg",
          measurement: "Eo 78 cm",
        },
        {
          size: "31 / L",
          heightRange: "1m70 – 1m77",
          weightRange: "68 – 75 kg",
          measurement: "Eo 82 cm",
        },
        {
          size: "32 / XL",
          heightRange: "1m75 – 1m82",
          weightRange: "76 – 84 kg",
          measurement: "Eo 86 cm",
        },
      ],
    },
  ],
  trendingKeywords: [
    {
      id: "kw-1",
      term: "Áo thun cotton 220gsm",
      accentless: "ao thun cotton 220gsm",
      searches7d: 4820,
      ctrPercent: 18.4,
      pinnedCampaign: true,
      status: "hien_thi",
      note: "Ghim theo đợt BST Thu Đông cơ bản",
    },
    {
      id: "kw-2",
      term: "Sợi Xanh Nexus",
      accentless: "soi xanh nexus",
      searches7d: 3190,
      ctrPercent: 21.2,
      pinnedCampaign: true,
      status: "hien_thi",
      note: "Ghim chiến dịch CSR Quý 4/2026",
    },
    {
      id: "kw-3",
      term: "Quần short chạy bộ",
      accentless: "quan short chay bo",
      searches7d: 2940,
      ctrPercent: 16.5,
      pinnedCampaign: false,
      status: "hien_thi",
      note: "Tự động từ top truy vấn 7 ngày",
    },
    {
      id: "kw-4",
      term: "Combo boxer modal",
      accentless: "combo boxer modal",
      searches7d: 2410,
      ctrPercent: 19.1,
      pinnedCampaign: false,
      status: "hien_thi",
      note: "Tỷ lệ chuyển đổi cao ở nhóm mua lặp",
    },
    {
      id: "kw-5",
      term: "Áo polo công sở",
      accentless: "ao polo cong so",
      searches7d: 2150,
      ctrPercent: 15.8,
      pinnedCampaign: false,
      status: "hien_thi",
      note: "Tự động từ top truy vấn 7 ngày",
    },
    {
      id: "kw-6",
      term: "áo siêu cấp rep 1:1",
      accentless: "ao sieu cap rep 1:1",
      searches7d: 184,
      ctrPercent: 0,
      pinnedCampaign: false,
      status: "bi_chan",
      note: "Lọc vi phạm hàng giả, không gợi ý tự động",
    },
  ],
  synonyms: [
    {
      id: "syn-1",
      canonical: "áo thun",
      accentless: "ao thun",
      synonyms: ["ao phong", "t-shirt", "tee", "ao phong nam"],
      active: true,
    },
    {
      id: "syn-2",
      canonical: "quần lót",
      accentless: "quan lot",
      synonyms: ["boxer", "brief", "sip nam", "do lot"],
      active: true,
    },
    {
      id: "syn-3",
      canonical: "tất",
      accentless: "tat",
      synonyms: ["vo", "vo cotton", "socks", "tat co thap"],
      active: true,
    },
    {
      id: "syn-4",
      canonical: "quần short",
      accentless: "quan short",
      synonyms: ["quan dui", "quan ngo", "short the thao"],
      active: true,
    },
    {
      id: "syn-5",
      canonical: "đổi size 7 ngày",
      accentless: "doi size 7 ngay",
      synonyms: ["doi tra", "bao hanh size", "doi hang tan noi"],
      active: true,
    },
  ],
  rankingConfig: {
    relevanceWeight: 30,
    reviewFitWeight: 22,
    slaOnTimeWeight: 20,
    sales30dWeight: 18,
    shopTrustWeight: 10,
    penalizeInflatedListPrice: true,
    hideBaitSkuOutliers: true,
    boostFullSizeAvailability: true,
  },
  homepageBlocks: [
    {
      id: "hb-1",
      code: "CM-HERO",
      title: "Hero trang chủ & Sợi Xanh Nexus",
      slotType: "Hero banner toàn khung",
      selectionRule: "Biên tập cố định bởi Admin",
      maxItems: 3,
      order: 1,
      active: true,
    },
    {
      id: "hb-2",
      code: "CM-NEW",
      title: "Mới về tuần này (Nam · Nữ · Tất cả)",
      slotType: "Lưới sản phẩm lọc tab",
      selectionRule: "SKU mới ≤ 21 ngày, tồn ≥ 10, đủ ảnh phẳng",
      maxItems: 8,
      order: 2,
      active: true,
    },
    {
      id: "hb-3",
      code: "CM-BEST",
      title: "Bán chạy đa gian hàng",
      slotType: "Lưới sản phẩm đa shop",
      selectionRule: "Điểm xếp hạng ≥ 80, không vi phạm giá ảo",
      maxItems: 8,
      order: 3,
      active: true,
    },
    {
      id: "hb-4",
      code: "CM-FLASH",
      title: "Flash sale theo khung giờ",
      slotType: "Dải đếm ngược theo khung giờ",
      selectionRule: "Đã qua kiểm duyệt giá trung vị 30 ngày",
      maxItems: 6,
      order: 4,
      active: true,
    },
    {
      id: "hb-5",
      code: "CM-FABRIC",
      title: "Khám phá công nghệ vải & chất liệu",
      slotType: "Khối chiến dịch chất liệu",
      selectionRule: "Liên kết trực tiếp từ điển chất liệu sàn",
      maxItems: 4,
      order: 5,
      active: true,
    },
    {
      id: "hb-6",
      code: "CM-MAG",
      title: "Sổ tay vải & Phối đồ",
      slotType: "Bài viết nhúng mua nhanh",
      selectionRule: "Bài biên tập & Lookbook Seller đã duyệt",
      maxItems: 4,
      order: 6,
      active: true,
    },
  ],
  csrCampaign: {
    campaignCode: "CSR-SXN-26Q4",
    title: "Sợi Xanh Nexus · Quý 4/2026",
    summary:
      "Trích 10% doanh thu bộ sưu tập Sợi Tự Nhiên từ 4 xưởng đối tác để tài trợ tủ sách và áo ấm học sinh vùng cao.",
    contributionRatePercent: 10,
    targetVnd: 500000000,
    fabricTabs: [
      {
        id: "ft-1",
        title: "Cotton Compact 220gsm",
        fabricCode: "COT-220",
        skuCount: 14,
        published: true,
      },
      {
        id: "ft-2",
        title: "Dry-Fit Chạy bộ tái chế",
        fabricCode: "DRY-150",
        skuCount: 11,
        published: true,
      },
      {
        id: "ft-3",
        title: "Modal Sợi Sồi & Sợi Tre",
        fabricCode: "MOD-180",
        skuCount: 9,
        published: true,
      },
    ],
    statements: [
      {
        id: "csr-1",
        batchCode: "SXN-2611-01",
        date: "2026-11-18",
        shopName: "Xưởng Dệt Bạch Đằng",
        orderCount: 415,
        contributionVnd: 124500000,
        verified: true,
      },
      {
        id: "csr-2",
        batchCode: "SXN-2611-02",
        date: "2026-11-17",
        shopName: "Sợi Tự Nhiên Đà Nẵng",
        orderCount: 312,
        contributionVnd: 96200000,
        verified: true,
      },
      {
        id: "csr-3",
        batchCode: "SXN-2611-03",
        date: "2026-11-15",
        shopName: "Dệt Kim Hà Nội",
        orderCount: 228,
        contributionVnd: 68400000,
        verified: true,
      },
      {
        id: "csr-4",
        batchCode: "SXN-2611-04",
        date: "2026-11-12",
        shopName: "Kho Basic Sài Gòn",
        orderCount: 194,
        contributionVnd: 53400000,
        verified: true,
      },
    ],
  },
  editorialSubmissions: [
    {
      id: "ed-1",
      code: "BV-261119-04",
      title: "Cách chọn định lượng áo thun Cotton 220gsm cho khí hậu ẩm",
      shopName: "Xưởng Dệt Bạch Đằng",
      category: "Sổ tay vải",
      embeddedSkuCount: 4,
      submittedAt: "2026-11-19T16:30:00+07:00",
      status: "cho_duyet",
      note: "Đã gắn 4 SKU Cotton Compact có tồn kho khả dụng.",
    },
    {
      id: "ed-2",
      code: "BV-261119-02",
      title: "Lookbook chạy bộ sáng: Phối áo Dry-Fit và quần short 7 inch",
      shopName: "Kho Basic Sài Gòn",
      category: "Phối đồ thể thao",
      embeddedSkuCount: 3,
      submittedAt: "2026-11-19T10:15:00+07:00",
      status: "cho_duyet",
      note: "Cần kiểm tra ảnh bìa không dính watermark ngoài sàn.",
    },
    {
      id: "ed-3",
      code: "BV-261117-01",
      title: "Mẹo giặt giữ cổ áo Polo Pique không giãn sau 50 lần mặc",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      category: "Bảo quản",
      embeddedSkuCount: 3,
      submittedAt: "2026-11-17T09:20:00+07:00",
      status: "da_dang",
      note: "Đã xuất bản lên chuyên mục Sổ tay vải & Phối đồ.",
    },
  ],
  badgeRequests: [
    {
      id: "bg-1",
      code: "NH-261119-05",
      shopName: "Xưởng Dệt Bạch Đằng",
      badgeKind: "doi_tra_45_ngay",
      targetScope: "Toàn gian hàng (28 SKU cơ bản)",
      evidenceSummary: "Đã ký phụ lục đối tác giao đổi size 2 chiều SPX/GHN và hoàn CK T2/T5.",
      submittedAt: "2026-11-19T15:10:00+07:00",
      status: "cho_duyet",
      note: "Đạt tỷ lệ giao đúng hẹn 98,4% trong 30 ngày.",
    },
    {
      id: "bg-2",
      code: "NH-261119-03",
      shopName: "Dệt Kim Hà Nội",
      badgeKind: "pre_order",
      targetScope: "Áo khoác gió chống nước Xám tro (lô tháng 12)",
      evidenceSummary: "Lịch xuất xưởng 02/12/2026, cam kết hoàn Xu 10% nếu trễ hạn.",
      submittedAt: "2026-11-19T11:40:00+07:00",
      status: "cho_duyet",
      note: "Đề xuất hạn chuẩn bị riêng 7 ngày cho mã pre-order.",
    },
    {
      id: "bg-3",
      code: "NH-261116-01",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      badgeKind: "gian_hang_chinh_hang",
      targetScope: "Hồ sơ xưởng sản xuất trực tiếp",
      evidenceSummary: "Đủ GPKD, chứng nhận nguồn sợi Linen/Cotton và kiểm định đường may.",
      submittedAt: "2026-11-16T08:50:00+07:00",
      status: "da_cap",
      note: "Đã hiển thị huy hiệu Gian hàng chính hãng trên thẻ sản phẩm.",
    },
  ],
};

export function computeCsrProgress(campaign: AdminCampaignCsrState) {
  const totalVnd = campaign.statements.reduce((sum, s) => sum + s.contributionVnd, 0);
  const totalOrders = campaign.statements.reduce((sum, s) => sum + s.orderCount, 0);
  const percent = campaign.targetVnd > 0 ? Math.min(100, (totalVnd / campaign.targetVnd) * 100) : 0;
  return { totalVnd, totalOrders, percent };
}

/* ============================================================================
   ĐỢT 2 — KIỂM DUYỆT ĐÁNH GIÁ, HỎI ĐÁP & MEDIA (/admin/kiem-duyet)
   ============================================================================ */

export type ModerationStatus = "cho_duyet" | "da_duyet" | "da_an" | "canh_bao";

export interface AdminReviewModerationItem {
  id: string;
  code: string;
  productName: string;
  skuCode: string;
  shopName: string;
  buyerName: string;
  orderCode: string;
  rating: number;
  fitFeedback: "Đúng cỡ" | "Hơi chật" | "Hơi rộng";
  buyerMeasurements: string;
  content: string;
  flagReason: string;
  thumbnail: string;
  submittedAt: string;
  status: ModerationStatus;
  moderatorNote: string;
}

export interface AdminQaModerationItem {
  id: string;
  code: string;
  productName: string;
  shopName: string;
  askerName: string;
  question: string;
  sellerReply: string;
  matchedBannedTerms: string[];
  submittedAt: string;
  status: ModerationStatus;
  moderatorNote: string;
}

export interface AdminBannedKeyword {
  id: string;
  term: string;
  category: "Link ngoài sàn" | "Ngôn từ công kích" | "Cam kết sai lệch" | "Nhãn hiệu cấm";
  actionMode: "chan_tu_dong" | "gan_co_duyet";
  hitCount30d: number;
  active: boolean;
}

export interface AdminMediaModerationItem {
  id: string;
  code: string;
  productName: string;
  skuCode: string;
  colorName: string;
  shopName: string;
  mediaType: "Ảnh nền phẳng" | "Video sản phẩm";
  previewUrl: string;
  resolution: string;
  flatBgPassed: boolean;
  noExternalLogoPassed: boolean;
  flagReason: string;
  submittedAt: string;
  status: ModerationStatus;
  moderatorNote: string;
}

export interface AdminModerationState {
  reviews: AdminReviewModerationItem[];
  qaItems: AdminQaModerationItem[];
  bannedKeywords: AdminBannedKeyword[];
  mediaItems: AdminMediaModerationItem[];
}

export const INITIAL_ADMIN_MODERATION: AdminModerationState = {
  reviews: [
    {
      id: "rev-1",
      code: "DG-261120-01",
      productName: "Áo thun Cotton Compact 220gsm Trắng",
      skuCode: "BD-AT01-TRANG-L",
      shopName: "Xưởng Dệt Bạch Đằng",
      buyerName: "Lê Minh Hoàng",
      orderCode: "NM-261114-032",
      rating: 5,
      fitFeedback: "Đúng cỡ",
      buyerMeasurements: "1m72 · 68 kg · Cỡ L",
      content:
        "Vải dày mịn không lộ, giặt máy 3 lần cổ áo vẫn giữ form. Ảnh chụp ánh sáng hơi tối.",
      flagReason: "Cần xác nhận ảnh đính kèm đạt chuẩn hiển thị",
      thumbnail: "/products/ao-thun-cotton-trang.png",
      submittedAt: "2026-11-20T00:40:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Đơn đã hoàn tất giao nhận, nội dung thực tế.",
    },
    {
      id: "rev-2",
      code: "DG-261119-09",
      productName: "Quần Short Chạy Bộ 7 inch Xám đậm",
      skuCode: "SG-QS02-XAM-M",
      shopName: "Kho Basic Sài Gòn",
      buyerName: "Nguyễn Văn Thành",
      orderCode: "NM-261112-018",
      rating: 1,
      fitFeedback: "Hơi chật",
      buyerMeasurements: "1m70 · 71 kg · Cỡ M",
      content: "Nhắn Zalo 0909xxx để mua giá sỉ rẻ hơn một nửa, không nên mua trên này.",
      flagReason: "Chứa số điện thoại và dẫn giao dịch ngoài sàn",
      thumbnail: "/products/quan-short-run-xam-dam.png",
      submittedAt: "2026-11-19T20:15:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Bộ lọc tự động gắn cờ nghi vấn spam kéo khách ngoài sàn.",
    },
    {
      id: "rev-3",
      code: "DG-261119-06",
      productName: "Áo Polo Pique Cotton Kem nhạt",
      skuCode: "DN-PL06-KEM-XL",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      buyerName: "Phạm Quốc Dũng",
      orderCode: "NM-261111-025",
      rating: 4,
      fitFeedback: "Hơi rộng",
      buyerMeasurements: "1m74 · 73 kg · Cỡ XL",
      content: "Mặc mát nhưng form hơi rộng vai, chắc đổi xuống L qua dịch vụ đổi 7 ngày.",
      flagReason: "Nghi vấn trùng ảnh với đánh giá trước (kiểm tra thủ công)",
      thumbnail: "/products/ao-polo-pique-kem.png",
      submittedAt: "2026-11-19T15:50:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Đánh giá độ vừa hữu ích cho người mua chọn size.",
    },
    {
      id: "rev-4",
      code: "DG-261118-04",
      productName: "Combo 3 Quần Lót Boxer Modal",
      skuCode: "HN-DL01-DEN-L",
      shopName: "Dệt Kim Hà Nội",
      buyerName: "Trần Đức Mạnh",
      orderCode: "NM-261109-014",
      rating: 5,
      fitFeedback: "Đúng cỡ",
      buyerMeasurements: "1m75 · 70 kg · Cỡ L",
      content: "Cạp chun êm không hằn, đóng hộp niêm phong sạch sẽ, giao hàng 2 ngày tới Hà Nội.",
      flagReason: "Đã kiểm tra tự động",
      thumbnail: "/products/do-lot-boxer-modal-3.png",
      submittedAt: "2026-11-18T11:20:00+07:00",
      status: "da_duyet",
      moderatorNote: "Đã duyệt hiển thị công khai trên trang sản phẩm.",
    },
  ],
  qaItems: [
    {
      id: "qa-1",
      code: "QA-261120-03",
      productName: "Áo thun Dry-Fit Cổ Vịt",
      shopName: "Kho Basic Sài Gòn",
      askerName: "Vũ Hải Đăng",
      question: "1m76 nặng 78 kg mặc chạy bộ chọn XL hay 2XL cho thoải mái?",
      sellerReply: "Anh nhắn Zalo shop 0912xxx gửi link giảm thêm 30k ngoài sàn nhé ạ.",
      matchedBannedTerms: ["Zalo", "ngoài sàn"],
      submittedAt: "2026-11-20T00:10:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Phản hồi của gian hàng vi phạm quy tắc chống dẫn giao dịch ngoài sàn.",
    },
    {
      id: "qa-2",
      code: "QA-261119-08",
      productName: "Combo 5 Đôi Tất Cotton Cổ Trung",
      shopName: "Xưởng Dệt Bạch Đằng",
      askerName: "Đào Thu Trang",
      question: "Bị thất lạc 1 chiếc tất khi giặt có được áp dụng chính sách bù lẻ không?",
      sellerReply:
        "Có ạ, gian hàng tham gia chuẩn 7 ngày của NexusMart, hỗ trợ gửi bù lẻ 1 chiếc miễn phí.",
      matchedBannedTerms: [],
      submittedAt: "2026-11-19T17:45:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Câu hỏi thường gặp rõ ràng, nên duyệt công khai.",
    },
    {
      id: "qa-3",
      code: "QA-261118-05",
      productName: "Quần Jogger French Terry Xám",
      shopName: "Dệt Kim Hà Nội",
      askerName: "Ngô Gia Huy",
      question: "Vải 320gsm giặt máy có bị xù mặt trong không shop?",
      sellerReply:
        "Dạ không ạ, anh lộn trái giặt nước mát 30 độ để giữ bề mặt sợi bền màu lâu hơn.",
      matchedBannedTerms: [],
      submittedAt: "2026-11-18T14:05:00+07:00",
      status: "da_duyet",
      moderatorNote: "Đã duyệt hiển thị trong khối Hỏi đáp.",
    },
  ],
  bannedKeywords: [
    {
      id: "bk-1",
      term: "nhắn zalo / chuyển khoản riêng",
      category: "Link ngoài sàn",
      actionMode: "chan_tu_dong",
      hitCount30d: 42,
      active: true,
    },
    {
      id: "bk-2",
      term: "rep 1:1 / hàng siêu cấp / fake",
      category: "Nhãn hiệu cấm",
      actionMode: "chan_tu_dong",
      hitCount30d: 19,
      active: true,
    },
    {
      id: "bk-3",
      term: "lừa đảo / rác rưởi",
      category: "Ngôn từ công kích",
      actionMode: "gan_co_duyet",
      hitCount30d: 11,
      active: true,
    },
    {
      id: "bk-4",
      term: "chữa bệnh / diệt khuẩn 100% tuyệt đối",
      category: "Cam kết sai lệch",
      actionMode: "gan_co_duyet",
      hitCount30d: 6,
      active: true,
    },
  ],
  mediaItems: [
    {
      id: "md-1",
      code: "MD-261120-04",
      productName: "Áo Polo Dệt Kim Đen",
      skuCode: "DN-PL08-DEN",
      colorName: "Đen tuyền",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      mediaType: "Ảnh nền phẳng",
      previewUrl: "/products/ao-polo-det-kim-den.png",
      resolution: "1600 × 2000 px",
      flatBgPassed: true,
      noExternalLogoPassed: true,
      flagReason: "Tải lên biến thể màu mới, chờ xác nhận màu swatch",
      submittedAt: "2026-11-20T00:25:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Đạt chuẩn nền phẳng sáng đều, không chèn chữ quảng cáo.",
    },
    {
      id: "md-2",
      code: "MD-261119-11",
      productName: "Áo Thun Cotton Compact Xám Đá",
      skuCode: "BD-AT02-XAM",
      colorName: "Xám đá",
      shopName: "Xưởng Dệt Bạch Đằng",
      mediaType: "Video sản phẩm",
      previewUrl: "/atmosphere/jacquard-loom-poster.jpg",
      resolution: "1920 × 1080 · 12 giây",
      flatBgPassed: true,
      noExternalLogoPassed: true,
      flagReason: "Video quay cận bề mặt dệt sợi, chờ duyệt gắn trang sản phẩm",
      submittedAt: "2026-11-19T19:10:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Clip cận cảnh khung dệt không chứa nhạc bản quyền hay logo ngoài.",
    },
    {
      id: "md-3",
      code: "MD-261119-07",
      productName: "Quần Short Linen Xám Đá",
      skuCode: "DN-QS03-XAM",
      colorName: "Xám đá",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      mediaType: "Ảnh nền phẳng",
      previewUrl: "/products/quan-short-linen-xam-da.png",
      resolution: "1200 × 1500 px",
      flatBgPassed: false,
      noExternalLogoPassed: true,
      flagReason: "Cân bằng sáng nền hơi tối hơn chuẩn Frost, cần kiểm tra màu thực tế",
      submittedAt: "2026-11-19T14:55:00+07:00",
      status: "cho_duyet",
      moderatorNote: "Đối chiếu tránh khiếu nại lệch màu thực tế.",
    },
    {
      id: "md-4",
      code: "MD-261118-02",
      productName: "Áo Khoác Gió Xám Tro",
      skuCode: "HN-AK01-XAM",
      colorName: "Xám tro",
      shopName: "Dệt Kim Hà Nội",
      mediaType: "Ảnh nền phẳng",
      previewUrl: "/products/ao-khoac-gio-xam-tro.png",
      resolution: "1600 × 2000 px",
      flatBgPassed: true,
      noExternalLogoPassed: true,
      flagReason: "Đã kiểm tra tự động",
      submittedAt: "2026-11-18T10:30:00+07:00",
      status: "da_duyet",
      moderatorNote: "Đã đồng bộ lên trang danh mục và trang sản phẩm.",
    },
  ],
};

/* ============================================================================
   ĐỢT 2 — HỘI VIÊN NEXUS, THẺ QUÀ TẶNG & KHUYẾN MÃI SÀN (/admin/thanh-vien)
   ============================================================================ */

export interface AdminLoyaltyTierConfig {
  id: string;
  tierName: "Thành viên" | "Bạc" | "Vàng" | "Bạch Kim";
  minSpend4QuartersVnd: number; // integer VND
  cashbackPercent: number; // 1–6%
  platformCoFundPercent: number;
  shopCoFundPercent: number;
  memberCount: number;
  perksSummary: string;
}

export interface AdminReferralPolicy {
  enabled: boolean;
  rewardPercent: number; // e.g. 10%
  maxRewardPerOrderVnd: number; // integer VND
  maxReferralsPerMonth: number;
  minInviteeOrderVnd: number;
  unlockAfterReturnWindow: boolean;
  antiAbuseSameDeviceOrPhone: boolean;
}

export interface AdminStudentVerificationItem {
  id: string;
  code: string;
  studentName: string;
  eduEmail: string;
  university: string;
  submittedAt: string;
  status: "cho_duyet" | "da_xac_thuc" | "tu_choi";
}

export interface AdminStudentPolicy {
  enabled: boolean;
  allowedDomains: string;
  discountPercent: number;
  maxDiscountVnd: number;
  monthlyOrderLimit: number;
  reverifyMonths: number;
  verifications: AdminStudentVerificationItem[];
}

export type GiftCardStatus = "dang_luu_hanh" | "da_quy_doi_het" | "het_han";

export interface AdminGiftCardRecord {
  id: string;
  code: string;
  purpose: string;
  issuedAt: string;
  expiresAt: string;
  issuedAmount: number; // integer VND
  redeemedAmount: number; // integer VND
  expiredAmount: number; // integer VND
  remainingAmount: number; // issuedAmount - redeemedAmount - expiredAmount
  status: GiftCardStatus;
}

export interface AdminGiftCardLedgerEntry {
  id: string;
  entryCode: string;
  date: string;
  cardCode: string;
  entryType: "phat_hanh" | "quy_doi" | "het_han";
  debitAccount: string;
  creditAccount: string;
  creditVnd: number; // Tăng quỹ phát hành (TK 3387 Có)
  debitVnd: number; // Giảm quỹ do quy đổi hoặc hết hạn (TK 3387 Nợ)
  note: string;
}

export type FlashProposalStatus = "cho_duyet" | "da_duyet" | "tu_choi";

export interface AdminFlashSaleProposal {
  id: string;
  code: string;
  slotLabel: string;
  campaignType: "Flash sale 3h" | "Deal sốc ngày";
  shopName: string;
  productName: string;
  medianPrice30d: number; // integer VND
  declaredOriginalPrice: number; // integer VND
  flashPrice: number; // integer VND
  quotaSlots: number;
  maxQtyPerOrder: number;
  priceInflationAlert: boolean;
  status: FlashProposalStatus;
  note: string;
}

export interface AdminPlatformVoucher {
  id: string;
  code: string;
  title: string;
  scope: "Giảm đơn sàn" | "Freeship sàn" | "Hội viên Nexus" | "Sinh viên .edu";
  discountVnd: number;
  minOrderVnd: number;
  stackableWithShop: boolean;
  usedQuota: number;
  totalQuota: number;
  active: boolean;
}

export interface AdminLoyaltyState {
  fridayDoubleEnabled: boolean;
  coinExpiryMonths: number;
  clawbackOnReturnEnabled: boolean;
  tiers: AdminLoyaltyTierConfig[];
  referral: AdminReferralPolicy;
  student: AdminStudentPolicy;
  giftCards: AdminGiftCardRecord[];
  giftCardLedger: AdminGiftCardLedgerEntry[];
  flashProposals: AdminFlashSaleProposal[];
  platformVouchers: AdminPlatformVoucher[];
}

function makeGiftCard(base: Omit<AdminGiftCardRecord, "remainingAmount">): AdminGiftCardRecord {
  return {
    ...base,
    remainingAmount: base.issuedAmount - base.redeemedAmount - base.expiredAmount,
  };
}

export const INITIAL_ADMIN_LOYALTY: AdminLoyaltyState = {
  fridayDoubleEnabled: true,
  coinExpiryMonths: 12,
  clawbackOnReturnEnabled: true,
  tiers: [
    {
      id: "tier-tv",
      tierName: "Thành viên",
      minSpend4QuartersVnd: 0,
      cashbackPercent: 1,
      platformCoFundPercent: 1.0,
      shopCoFundPercent: 0.0,
      memberCount: 14820,
      perksSummary: "Hoàn 1% Xu (1 Xu = 1₫), nhân đôi Thứ Sáu, hạn 12 tháng.",
    },
    {
      id: "tier-bac",
      tierName: "Bạc",
      minSpend4QuartersVnd: 1000000,
      cashbackPercent: 2,
      platformCoFundPercent: 1.2,
      shopCoFundPercent: 0.8,
      memberCount: 5340,
      perksSummary: "Hoàn 2% Xu, quà sinh nhật tự động trong giỏ, ưu tiên đổi size.",
    },
    {
      id: "tier-vang",
      tierName: "Vàng",
      minSpend4QuartersVnd: 3000000,
      cashbackPercent: 4,
      platformCoFundPercent: 2.2,
      shopCoFundPercent: 1.8,
      memberCount: 1890,
      perksSummary: "Hoàn 4% Xu, 2 mã freeship toàn sàn/tháng, giữ hạng 4 quý.",
    },
    {
      id: "tier-bk",
      tierName: "Bạch Kim",
      minSpend4QuartersVnd: 6000000,
      cashbackPercent: 6,
      platformCoFundPercent: 3.0,
      shopCoFundPercent: 3.0,
      memberCount: 620,
      perksSummary: "Hoàn 6% Xu, freeship không giới hạn, quà sinh nhật đồng tài trợ.",
    },
  ],
  referral: {
    enabled: true,
    rewardPercent: 10,
    maxRewardPerOrderVnd: 100000,
    maxReferralsPerMonth: 10,
    minInviteeOrderVnd: 249000,
    unlockAfterReturnWindow: true,
    antiAbuseSameDeviceOrPhone: true,
  },
  student: {
    enabled: true,
    allowedDomains: ".edu.vn, .edu",
    discountPercent: 10,
    maxDiscountVnd: 60000,
    monthlyOrderLimit: 2,
    reverifyMonths: 12,
    verifications: [
      {
        id: "edu-1",
        code: "SV-261119-08",
        studentName: "Nguyễn Hải Nam",
        eduEmail: "nam.nh22@sis.hust.edu.vn",
        university: "Đại học Bách khoa Hà Nội",
        submittedAt: "2026-11-19T18:10:00+07:00",
        status: "cho_duyet",
      },
      {
        id: "edu-2",
        code: "SV-261119-05",
        studentName: "Trần Ngọc Mai",
        eduEmail: "22520814@gm.uit.edu.vn",
        university: "Đại học Công nghệ Thông tin TP.HCM",
        submittedAt: "2026-11-19T14:25:00+07:00",
        status: "cho_duyet",
      },
      {
        id: "edu-3",
        code: "SV-261117-01",
        studentName: "Lê Hoàng Anh",
        eduEmail: "anhlh.3122@st.ueh.edu.vn",
        university: "Đại học Kinh tế TP.HCM",
        submittedAt: "2026-11-17T09:40:00+07:00",
        status: "da_xac_thuc",
      },
    ],
  },
  giftCards: [
    makeGiftCard({
      id: "gc-1",
      code: "NXGIFT-TET26-01",
      purpose: "Lô bán lẻ mệnh giá 500.000₫ (tùy biến mã)",
      issuedAt: "2026-09-15",
      expiresAt: "2027-08-15",
      issuedAmount: 5000000,
      redeemedAmount: 3250000,
      expiredAmount: 0,
      status: "dang_luu_hanh",
    }),
    makeGiftCard({
      id: "gc-2",
      code: "NXGIFT-BDAY-02",
      purpose: "Quà sinh nhật Hội viên Bạc · Vàng · Bạch Kim",
      issuedAt: "2026-10-01",
      expiresAt: "2027-09-01",
      issuedAmount: 3000000,
      redeemedAmount: 2400000,
      expiredAmount: 0,
      status: "dang_luu_hanh",
    }),
    makeGiftCard({
      id: "gc-3",
      code: "NXGIFT-CORP-03",
      purpose: "Thẻ quà tặng đối tác nội bộ xưởng may",
      issuedAt: "2026-08-10",
      expiresAt: "2027-07-10",
      issuedAmount: 4000000,
      redeemedAmount: 4000000,
      expiredAmount: 0,
      status: "da_quy_doi_het",
    }),
    makeGiftCard({
      id: "gc-4",
      code: "NXGIFT-RUN-04",
      purpose: "Giải chạy cộng đồng Sợi Xanh Nexus",
      issuedAt: "2026-10-20",
      expiresAt: "2027-09-20",
      issuedAmount: 2500000,
      redeemedAmount: 1650000,
      expiredAmount: 0,
      status: "dang_luu_hanh",
    }),
    makeGiftCard({
      id: "gc-5",
      code: "NXGIFT-Q1-05",
      purpose: "Đợt phát hành Quý 1/2026 (hạn 9 tháng)",
      issuedAt: "2026-02-10",
      expiresAt: "2026-11-10",
      issuedAmount: 2000000,
      redeemedAmount: 1700000,
      expiredAmount: 300000,
      status: "het_han",
    }),
    makeGiftCard({
      id: "gc-6",
      code: "NXGIFT-EDU-06",
      purpose: "Gói quà chào tân sinh viên .edu",
      issuedAt: "2026-11-05",
      expiresAt: "2027-09-05",
      issuedAmount: 1500000,
      redeemedAmount: 900000,
      expiredAmount: 0,
      status: "dang_luu_hanh",
    }),
  ],
  giftCardLedger: [
    {
      id: "gcl-1",
      entryCode: "BT-GC-011",
      date: "2026-11-18",
      cardCode: "NXGIFT-EDU-06",
      entryType: "quy_doi",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 331 Phải trả gian hàng",
      creditVnd: 0,
      debitVnd: 900000,
      note: "Quy đổi trên 4 đơn đa gian hàng",
    },
    {
      id: "gcl-2",
      entryCode: "BT-GC-010",
      date: "2026-11-10",
      cardCode: "NXGIFT-Q1-05",
      entryType: "het_han",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 711 Thu nhập thẻ hết hạn",
      creditVnd: 0,
      debitVnd: 300000,
      note: "Tất toán số dư chưa dùng sau ngày 10/11/2026",
    },
    {
      id: "gcl-3",
      entryCode: "BT-GC-009",
      date: "2026-11-05",
      cardCode: "NXGIFT-EDU-06",
      entryType: "phat_hanh",
      debitAccount: "TK 112 Tiền thu trước",
      creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditVnd: 1500000,
      debitVnd: 0,
      note: "Phát hành gói quà sinh viên .edu (hạn 10 tháng)",
    },
    {
      id: "gcl-4",
      entryCode: "BT-GC-008",
      date: "2026-11-02",
      cardCode: "NXGIFT-RUN-04",
      entryType: "quy_doi",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 331 Phải trả gian hàng",
      creditVnd: 0,
      debitVnd: 1650000,
      note: "Quy đổi mua đồ chạy bộ Dry-Fit",
    },
    {
      id: "gcl-5",
      entryCode: "BT-GC-007",
      date: "2026-10-20",
      cardCode: "NXGIFT-RUN-04",
      entryType: "phat_hanh",
      debitAccount: "TK 112 Tiền thu trước",
      creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditVnd: 2500000,
      debitVnd: 0,
      note: "Phát hành giải chạy cộng đồng Sợi Xanh",
    },
    {
      id: "gcl-6",
      entryCode: "BT-GC-006",
      date: "2026-10-18",
      cardCode: "NXGIFT-BDAY-02",
      entryType: "quy_doi",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 331 Phải trả gian hàng",
      creditVnd: 0,
      debitVnd: 2400000,
      note: "Quy đổi thẻ sinh nhật Hội viên",
    },
    {
      id: "gcl-7",
      entryCode: "BT-GC-005",
      date: "2026-10-01",
      cardCode: "NXGIFT-BDAY-02",
      entryType: "phat_hanh",
      debitAccount: "TK 641 Ngân sách Hội viên",
      creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditVnd: 3000000,
      debitVnd: 0,
      note: "Phát hành thẻ quà sinh nhật tháng 10–11",
    },
    {
      id: "gcl-8",
      entryCode: "BT-GC-004",
      date: "2026-09-28",
      cardCode: "NXGIFT-TET26-01",
      entryType: "quy_doi",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 331 Phải trả gian hàng",
      creditVnd: 0,
      debitVnd: 3250000,
      note: "Quy đổi tiêu toàn sàn (13 đơn hàng)",
    },
    {
      id: "gcl-9",
      entryCode: "BT-GC-003",
      date: "2026-09-15",
      cardCode: "NXGIFT-TET26-01",
      entryType: "phat_hanh",
      debitAccount: "TK 112 Tiền thu trước",
      creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditVnd: 5000000,
      debitVnd: 0,
      note: "Phát hành 10 thẻ quà tặng mệnh giá 500.000₫",
    },
    {
      id: "gcl-10",
      entryCode: "BT-GC-002",
      date: "2026-09-02",
      cardCode: "NXGIFT-CORP-03",
      entryType: "quy_doi",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 331 Phải trả gian hàng",
      creditVnd: 0,
      debitVnd: 4000000,
      note: "Đã quy đổi toàn bộ 4.000.000₫",
    },
    {
      id: "gcl-11",
      entryCode: "BT-GC-001",
      date: "2026-08-10",
      cardCode: "NXGIFT-CORP-03",
      entryType: "phat_hanh",
      debitAccount: "TK 112 Tiền thu trước",
      creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditVnd: 4000000,
      debitVnd: 0,
      note: "Phát hành hợp đồng thẻ quà đối tác",
    },
    {
      id: "gcl-12",
      entryCode: "BT-GC-000",
      date: "2026-06-15",
      cardCode: "NXGIFT-Q1-05",
      entryType: "quy_doi",
      debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditAccount: "TK 331 Phải trả gian hàng",
      creditVnd: 0,
      debitVnd: 1700000,
      note: "Quy đổi trong kỳ Quý 2/2026",
    },
    {
      id: "gcl-13",
      entryCode: "BT-GC-00A",
      date: "2026-02-10",
      cardCode: "NXGIFT-Q1-05",
      entryType: "phat_hanh",
      debitAccount: "TK 112 Tiền thu trước",
      creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
      creditVnd: 2000000,
      debitVnd: 0,
      note: "Phát hành đợt Quý 1/2026 (hạn 9 tháng)",
    },
  ],
  flashProposals: [
    {
      id: "fp-1",
      code: "FS-261120-12H",
      slotLabel: "20/11 · 12:00 – 15:00",
      campaignType: "Flash sale 3h",
      shopName: "Xưởng Dệt Bạch Đằng",
      productName: "Áo thun Cotton Compact 220gsm Trắng",
      medianPrice30d: 179000,
      declaredOriginalPrice: 179000,
      flashPrice: 139000,
      quotaSlots: 150,
      maxQtyPerOrder: 3,
      priceInflationAlert: false,
      status: "cho_duyet",
      note: "Giá gốc khớp trung vị 30 ngày, giảm thực 22%, mở quỹ 150 suất.",
    },
    {
      id: "fp-2",
      code: "FS-261120-18H",
      slotLabel: "20/11 · 18:00 – 21:00",
      campaignType: "Deal sốc ngày",
      shopName: "Kho Basic Sài Gòn",
      productName: "Quần Short Daily Be Cát",
      medianPrice30d: 219000,
      declaredOriginalPrice: 299000,
      flashPrice: 199000,
      quotaSlots: 80,
      maxQtyPerOrder: 1,
      priceInflationAlert: true,
      status: "cho_duyet",
      note: "Cảnh báo nâng giá gốc ảo +36,5% so với trung vị 30 ngày (219.000₫).",
    },
    {
      id: "fp-3",
      code: "FS-261120-21H",
      slotLabel: "20/11 · 21:00 – 24:00",
      campaignType: "Flash sale 3h",
      shopName: "Sợi Tự Nhiên Đà Nẵng",
      productName: "Áo Polo Pique Cotton Kem nhạt",
      medianPrice30d: 289000,
      declaredOriginalPrice: 289000,
      flashPrice: 229000,
      quotaSlots: 120,
      maxQtyPerOrder: 2,
      priceInflationAlert: false,
      status: "cho_duyet",
      note: "Giảm thực 21%, đủ tồn kho cả 5 cỡ S–2XL.",
    },
    {
      id: "fp-4",
      code: "FS-261120-09H",
      slotLabel: "20/11 · 09:00 – 12:00",
      campaignType: "Deal sốc ngày",
      shopName: "Dệt Kim Hà Nội",
      productName: "Combo 5 Đôi Tất Cotton Cổ Trung",
      medianPrice30d: 129000,
      declaredOriginalPrice: 129000,
      flashPrice: 89000,
      quotaSlots: 200,
      maxQtyPerOrder: 1,
      priceInflationAlert: false,
      status: "da_duyet",
      note: "Đã duyệt và khóa trần 1 SP/lần, tối đa 2 lần/ngày.",
    },
  ],
  platformVouchers: [
    {
      id: "pv-1",
      code: "NEXUS30",
      title: "Giảm 30.000₫ đơn đa gian hàng",
      scope: "Giảm đơn sàn",
      discountVnd: 30000,
      minOrderVnd: 399000,
      stackableWithShop: true,
      usedQuota: 642,
      totalQuota: 1000,
      active: true,
    },
    {
      id: "pv-2",
      code: "FREESHIP25",
      title: "Hỗ trợ phí vận chuyển tối đa 25.000₫",
      scope: "Freeship sàn",
      discountVnd: 25000,
      minOrderVnd: 249000,
      stackableWithShop: true,
      usedQuota: 1840,
      totalQuota: 3000,
      active: true,
    },
    {
      id: "pv-3",
      code: "HOIVIEN50",
      title: "Ưu đãi Hội viên Vàng & Bạch Kim",
      scope: "Hội viên Nexus",
      discountVnd: 50000,
      minOrderVnd: 599000,
      stackableWithShop: true,
      usedQuota: 318,
      totalQuota: 600,
      active: true,
    },
    {
      id: "pv-4",
      code: "SINHVIEN60",
      title: "Ưu đãi xác thực email .edu (10% tối đa 60k)",
      scope: "Sinh viên .edu",
      discountVnd: 60000,
      minOrderVnd: 249000,
      stackableWithShop: true,
      usedQuota: 215,
      totalQuota: 500,
      active: true,
    },
    {
      id: "pv-5",
      code: "SOIXANH20",
      title: "Đồng hành BST Sợi Xanh Nexus",
      scope: "Giảm đơn sàn",
      discountVnd: 20000,
      minOrderVnd: 299000,
      stackableWithShop: true,
      usedQuota: 410,
      totalQuota: 800,
      active: false,
    },
  ],
};

export interface GiftCardLedgerSummary {
  totalIssued: number;
  totalRedeemed: number;
  totalExpired: number;
  totalRemaining: number;
  ledgerCreditTotal: number;
  ledgerDebitTotal: number;
  isBalanced: boolean;
}

export function computeGiftCardSummary(
  cards: AdminGiftCardRecord[],
  ledger: AdminGiftCardLedgerEntry[]
): GiftCardLedgerSummary {
  const totalIssued = cards.reduce((s, c) => s + c.issuedAmount, 0);
  const totalRedeemed = cards.reduce((s, c) => s + c.redeemedAmount, 0);
  const totalExpired = cards.reduce((s, c) => s + c.expiredAmount, 0);
  const totalRemaining = cards.reduce((s, c) => s + c.remainingAmount, 0);
  const ledgerCreditTotal = ledger.reduce((s, e) => s + e.creditVnd, 0);
  const ledgerDebitTotal = ledger.reduce((s, e) => s + e.debitVnd, 0);
  const isBalanced =
    totalIssued - totalRedeemed - totalExpired === totalRemaining &&
    ledgerCreditTotal === totalIssued &&
    ledgerDebitTotal === totalRedeemed + totalExpired &&
    ledgerCreditTotal - ledgerDebitTotal === totalRemaining;
  return {
    totalIssued,
    totalRedeemed,
    totalExpired,
    totalRemaining,
    ledgerCreditTotal,
    ledgerDebitTotal,
    isBalanced,
  };
}

/* ============================================================================
   ĐỢT 2 — CẤU HÌNH MỞ RỘNG (/admin/cau-hinh)
   ============================================================================ */

export interface AdminShippingCarrierConfig {
  id: string;
  code: "SPX" | "GHN" | "GIAO_NHANH";
  name: string;
  zones: string;
  slaDays: string;
  baseFeeVnd: number;
  coCheckAllowed: boolean;
  enabled: boolean;
}

export interface AdminReturnExceptionRule {
  id: string;
  label: string;
  reason: string;
  active: boolean;
}

export interface AdminNotificationTemplate {
  id: string;
  channel: "Email" | "Zalo ZNS";
  eventCode: string;
  title: string;
  subjectOrHeader: string;
  bodyTemplate: string;
  enabled: boolean;
}

export interface AdminExtendedPlatformConfig {
  // Guest checkout & gateways
  guestCheckoutEnabled: boolean;
  guestLookupAndCancelEnabled: boolean;
  codEnabled: boolean;
  retryPaymentWindowMinutes: number;
  // Shipping carriers & zones
  carriers: AdminShippingCarrierConfig[];
  // Return standard & exceptions
  standardReturnDays: number;
  maxExchangeCount: number;
  twoWayCourierExchangeEnabled: boolean;
  missingSingleSockPolicyEnabled: boolean;
  bankRefundSchedule: string;
  returnExceptions: AdminReturnExceptionRule[];
  // Notification templates (Email & Zalo ZNS)
  groupParentOrderNotification: boolean;
  notificationTemplates: AdminNotificationTemplate[];
}

export const INITIAL_EXTENDED_PLATFORM_CONFIG: AdminExtendedPlatformConfig = {
  guestCheckoutEnabled: true,
  guestLookupAndCancelEnabled: true,
  codEnabled: true,
  retryPaymentWindowMinutes: 30,
  carriers: [
    {
      id: "car-spx",
      code: "SPX",
      name: "SPX Express",
      zones: "Toàn quốc 34 tỉnh thành",
      slaDays: "1 – 3 ngày",
      baseFeeVnd: 25000,
      coCheckAllowed: true,
      enabled: true,
    },
    {
      id: "car-ghn",
      code: "GHN",
      name: "Giao Hàng Nhanh (GHN)",
      zones: "Toàn quốc 34 tỉnh thành · Hỗ trợ đổi 2 chiều",
      slaDays: "1 – 4 ngày",
      baseFeeVnd: 25000,
      coCheckAllowed: true,
      enabled: true,
    },
    {
      id: "car-fast",
      code: "GIAO_NHANH",
      name: "Giao nhanh trong ngày",
      zones: "Nội thành Hà Nội, TP.HCM, Đà Nẵng",
      slaDays: "Trong ngày (4 – 6 giờ)",
      baseFeeVnd: 38000,
      coCheckAllowed: true,
      enabled: true,
    },
  ],
  standardReturnDays: 45,
  maxExchangeCount: 3,
  twoWayCourierExchangeEnabled: true,
  missingSingleSockPolicyEnabled: true,
  bankRefundSchedule: "Thứ 2 & Thứ 5 hằng tuần (hoặc tức thời về Ví ZaloPay)",
  returnExceptions: [
    {
      id: "exc-1",
      label: "Hàng Outlet xả kho",
      reason: "Giảm sâu cuối mùa, hiển thị nhãn Không áp dụng đổi trả trên trang sản phẩm.",
      active: true,
    },
    {
      id: "exc-2",
      label: "Sản phẩm săn Deal sốc",
      reason: "Giới hạn 1 SP/lần, chỉ hỗ trợ hoàn khi lỗi sản xuất hoặc giao sai mẫu.",
      active: true,
    },
    {
      id: "exc-3",
      label: "Pack niêm phong đã bóc tem",
      reason: "Đồ lót và combo hộp niêm phong đã mở không đáp ứng tiêu chuẩn vệ sinh.",
      active: true,
    },
  ],
  groupParentOrderNotification: true,
  notificationTemplates: [
    {
      id: "tpl-email-order",
      channel: "Email",
      eventCode: "ORDER_CONFIRMED",
      title: "Xác nhận đơn hàng gộp (đa gian hàng)",
      subjectOrHeader: "[NexusMart] Xác nhận đơn cha {{parent_code}} gồm {{sub_order_count}} kiện",
      bodyTemplate:
        "Chào {{buyer_name}}, đơn hàng {{parent_code}} trị giá {{grand_total}} đã được ghi nhận. Mã vận đơn riêng: {{sub_tracking_list}}. Tra cứu nhanh không cần đăng nhập tại nexusmart.vn/tra-cuu.",
      enabled: true,
    },
    {
      id: "tpl-zns-ship",
      channel: "Zalo ZNS",
      eventCode: "SHIPPING_UPDATED",
      title: "Cập nhật hành trình giao & đồng kiểm",
      subjectOrHeader: "Đơn con {{order_code}} đã giao cho {{carrier_name}}",
      bodyTemplate:
        "Kiện hàng từ {{shop_name}} (mã vận đơn {{tracking_code}}) dự kiến giao ngày {{eta_date}}. Bạn được đồng kiểm khi nhận và đổi size tận nơi trong 7 ngày.",
      enabled: true,
    },
    {
      id: "tpl-zns-refund",
      channel: "Zalo ZNS",
      eventCode: "EXCHANGE_REFUND",
      title: "Xác nhận đổi size 2 chiều / hoàn tiền",
      subjectOrHeader: "Yêu cầu {{case_code}} đã được xác nhận",
      bodyTemplate:
        "Bưu tá {{carrier_name}} sẽ mang cỡ {{new_size}} đến đổi trực tiếp tại địa chỉ của bạn. Khoản hoàn {{refund_amount}} (nếu có) được chuyển vào lịch Thứ 2/Thứ 5.",
      enabled: true,
    },
  ],
};
