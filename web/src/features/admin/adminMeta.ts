import type {
  BadgeApprovalStatus,
  DisputeStatus,
  DisputeTier,
  EditorialStatus,
  FlashProposalStatus,
  GiftCardStatus,
  ModerationStatus,
  PaymentMethod,
  PaymentReconciliationRow,
  PlatformBadgeKind,
  ReconciliationStatus,
  ShopApprovalStatus,
  VulnerablePriorityGroup,
} from "@/data";

export type PillTone = "positive" | "negative" | "warning" | "neutral";

export type AdminViewState = "loading" | "empty" | "error";

export const EMPTY_DASH = "\u2014";

export function parseStateParam(raw: string | null): AdminViewState | null {
  if (raw === "loading" || raw === "empty" || raw === "error") {
    return raw;
  }
  return null;
}

export const SHOP_APPROVAL_META: Record<ShopApprovalStatus, { label: string; tone: PillTone }> = {
  cho_duyet: { label: "Chờ duyệt", tone: "warning" },
  bo_sung_ho_so: { label: "Yêu cầu bổ sung", tone: "neutral" },
  da_duyet: { label: "Đã duyệt", tone: "positive" },
  tu_choi: { label: "Từ chối", tone: "negative" },
};

export const DISPUTE_STATUS_META: Record<DisputeStatus, { label: string; tone: PillTone }> = {
  moi_tiep_nhan: { label: "Chờ phân xử", tone: "warning" },
  dang_xac_minh: { label: "Đang xác minh", tone: "neutral" },
  cho_phan_hoi: { label: "Chờ phản hồi", tone: "neutral" },
  da_xu_ly: { label: "Đã giải quyết", tone: "positive" },
};

export const RECON_STATUS_META: Record<ReconciliationStatus, { label: string; tone: PillTone }> = {
  khop: { label: "Đã khớp", tone: "positive" },
  lech_can_xu_ly: { label: "Có chênh lệch", tone: "negative" },
  dang_doi_soat: { label: "Đang đối chiếu", tone: "warning" },
};

export const CHANNEL_META: Record<PaymentMethod, string> = {
  vietqr: "VietQR",
  cod: "COD thu hộ",
  vi_sandbox: "Ví NexusPay",
};

export interface ChannelReconciliationSummary {
  channel: PaymentMethod;
  channelLabel: string;
  batchCount: number;
  txCount: number;
  expectedVnd: number;
  settledVnd: number;
  discrepancyVnd: number;
  sellerPayableVnd: number;
}

const CHANNEL_ORDER: PaymentMethod[] = ["vietqr", "cod", "vi_sandbox"];

export function computeChannelBreakdown(
  rows: PaymentReconciliationRow[]
): ChannelReconciliationSummary[] {
  const map = new Map<PaymentMethod, ChannelReconciliationSummary>();

  for (const ch of CHANNEL_ORDER) {
    map.set(ch, {
      channel: ch,
      channelLabel: CHANNEL_META[ch],
      batchCount: 0,
      txCount: 0,
      expectedVnd: 0,
      settledVnd: 0,
      discrepancyVnd: 0,
      sellerPayableVnd: 0,
    });
  }

  for (const row of rows) {
    const entry = map.get(row.channel);
    if (!entry) continue;
    entry.batchCount += 1;
    entry.txCount += row.txCount;
    entry.expectedVnd += row.grossCollected;
    entry.settledVnd += row.grossCollected - row.discrepancy;
    entry.discrepancyVnd += row.discrepancy;
    entry.sellerPayableVnd += row.sellerPayable;
  }

  return CHANNEL_ORDER.map((ch) => map.get(ch)!).filter((item) => item.batchCount > 0);
}

export type AdminRangeKey = "7d" | "14d" | "30d";

export const RANGE_LABELS: Record<AdminRangeKey, string> = {
  "7d": "7 ngày qua",
  "14d": "14 ngày qua",
  "30d": "30 ngày qua",
};

export function formatShortTimeDateVi(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const hh = d.getHours() < 10 ? `0${d.getHours()}` : String(d.getHours());
  const mm = d.getMinutes() < 10 ? `0${d.getMinutes()}` : String(d.getMinutes());
  const dd = d.getDate() < 10 ? `0${d.getDate()}` : String(d.getDate());
  const mo = d.getMonth() + 1 < 10 ? `0${d.getMonth() + 1}` : String(d.getMonth() + 1);
  return `${hh}:${mm} · ${dd}/${mo}`;
}

export function formatNowHHMM(): string {
  const now = new Date();
  const hh = now.getHours() < 10 ? `0${now.getHours()}` : String(now.getHours());
  const mm = now.getMinutes() < 10 ? `0${now.getMinutes()}` : String(now.getMinutes());
  return `${hh}:${mm}`;
}

export const MODERATION_STATUS_META: Record<ModerationStatus, { label: string; tone: PillTone }> = {
  cho_duyet: { label: "Chờ kiểm duyệt", tone: "warning" },
  da_duyet: { label: "Đã duyệt", tone: "positive" },
  da_an: { label: "Đã ẩn", tone: "negative" },
  canh_bao: { label: "Đã cảnh báo", tone: "negative" },
};

export const EDITORIAL_STATUS_META: Record<EditorialStatus, { label: string; tone: PillTone }> = {
  cho_duyet: { label: "Chờ duyệt đăng", tone: "warning" },
  da_dang: { label: "Đã xuất bản", tone: "positive" },
  can_chinh_sua: { label: "Yêu cầu sửa", tone: "neutral" },
};

export const BADGE_STATUS_META: Record<BadgeApprovalStatus, { label: string; tone: PillTone }> = {
  cho_duyet: { label: "Chờ thẩm định", tone: "warning" },
  da_cap: { label: "Đã cấp nhãn", tone: "positive" },
  tu_choi: { label: "Từ chối", tone: "negative" },
};

export const BADGE_KIND_LABELS: Record<PlatformBadgeKind, string> = {
  pre_order: "Pre-order",
  gian_hang_chinh_hang: "Gian hàng chính hãng",
  doi_tra_45_ngay: "Đổi trả 45 ngày",
};

export const GIFT_CARD_STATUS_META: Record<GiftCardStatus, { label: string; tone: PillTone }> = {
  dang_luu_hanh: { label: "Đang lưu hành", tone: "positive" },
  da_quy_doi_het: { label: "Đã quy đổi hết", tone: "neutral" },
  het_han: { label: "Đã hết hạn", tone: "warning" },
};

export const FLASH_PROPOSAL_STATUS_META: Record<
  FlashProposalStatus,
  { label: string; tone: PillTone }
> = {
  cho_duyet: { label: "Chờ duyệt suất", tone: "warning" },
  da_duyet: { label: "Đã duyệt suất", tone: "positive" },
  tu_choi: { label: "Từ chối", tone: "negative" },
};

export const STUDENT_VERIF_STATUS_META: Record<
  "cho_duyet" | "da_xac_thuc" | "tu_choi",
  { label: string; tone: PillTone }
> = {
  cho_duyet: { label: "Chờ xác thực", tone: "warning" },
  da_xac_thuc: { label: "Đã xác thực .edu", tone: "positive" },
  tu_choi: { label: "Từ chối", tone: "negative" },
};

export const DISPUTE_TIER_LABELS: Record<DisputeTier, string> = {
  cap_2_tu_gian_hang: "Cấp 2 · Từ gian hàng chuyển lên",
  cap_2_san_thu_ly: "Cấp 2 · Sàn thụ lý trực tiếp",
};

export const VULNERABLE_GROUP_LABELS: Record<VulnerablePriorityGroup, string> = {
  nguoi_cao_tuoi: "Người cao tuổi",
  phu_nu_mang_thai: "Phụ nữ mang thai",
  nguoi_khuyet_tat: "Người khuyết tật",
};
