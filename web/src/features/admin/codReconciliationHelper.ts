import { formatCarrierLabel, formatVnd } from "../../lib/format.ts";

export const MAX_COD_CSV_BYTES = 2 * 1024 * 1024;
export const MAX_COD_CSV_ROWS = 5000;
export const COD_RESULT_PAGE_SIZE = 50;

export interface CodCarrierOption {
  code: string;
  label: string;
}

/**
 * Fallback active carrier codes matching services/shipping/migrations/000003_shipping_ops.sql
 * when the current admin role (e.g. accountant) has `reconciliation.write` without `carriers.read`.
 */
export const DEFAULT_COD_FALLBACK_CARRIER_CODES = ["ghn", "ghtk", "mock_express"] as const;

const DEFAULT_COD_FALLBACK_CARRIER_NAMES: Record<
  (typeof DEFAULT_COD_FALLBACK_CARRIER_CODES)[number],
  string | undefined
> = {
  ghn: undefined,
  ghtk: "Giao Hàng Tiết Kiệm (GHTK)",
  mock_express: undefined,
};

export function buildCodCarrierOptions(
  carriers?: readonly { carrier_code: string; name?: string | null }[]
): CodCarrierOption[] {
  if (carriers && carriers.length > 0) {
    return carriers.map((c) => ({
      code: c.carrier_code,
      label: formatCarrierLabel(c.carrier_code, c.name),
    }));
  }
  return DEFAULT_COD_FALLBACK_CARRIER_CODES.map((code) => ({
    code,
    label: formatCarrierLabel(code, DEFAULT_COD_FALLBACK_CARRIER_NAMES[code]),
  }));
}

export function validateCodCsvFile(file: { name: string; size: number } | null | undefined): {
  error: string | null;
} {
  if (!file) {
    return { error: "Vui lòng chọn tệp CSV đối soát COD từ hãng vận chuyển." };
  }
  const lowerName = file.name.trim().toLowerCase();
  if (!lowerName.endsWith(".csv")) {
    return { error: "Tệp đối soát COD phải có định dạng .csv (UTF-8)." };
  }
  if (file.size <= 0) {
    return { error: "Tệp CSV đang trống, không có dữ liệu đối soát." };
  }
  if (file.size > MAX_COD_CSV_BYTES) {
    return { error: "Dung lượng tệp CSV vượt quá giới hạn tối đa 2 MiB." };
  }
  return { error: null };
}

/**
 * Lightweight pre-upload guard for COD CSV text.
 * Header and per-row RFC 4180 CSV validation (including quoted headers/cells) is delegated
 * to the gateway's stdlib `encoding/csv` parser so valid quoted files are never rejected early.
 */
export function validateCodCsvText(rawText: string): { error: string | null; rowCount: number } {
  const cleaned = rawText.replace(/^\uFEFF/, "").trim();
  if (!cleaned) {
    return { error: "Tệp CSV đang trống, không có dữ liệu đối soát.", rowCount: 0 };
  }
  const lines = cleaned
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
  if (lines.length < 2) {
    return { error: "Tệp CSV cần có dòng tiêu đề và ít nhất 1 dòng dữ liệu COD.", rowCount: 0 };
  }
  const rowCount = lines.length - 1;
  if (rowCount > MAX_COD_CSV_ROWS) {
    return {
      error: `Tệp CSV vượt quá tối đa ${MAX_COD_CSV_ROWS.toLocaleString("vi-VN")} dòng dữ liệu (đang có ${rowCount.toLocaleString("vi-VN")} dòng).`,
      rowCount,
    };
  }
  return { error: null, rowCount };
}

export function paginateCodItems<T>(
  items: readonly T[],
  page: number,
  pageSize: number = COD_RESULT_PAGE_SIZE
): {
  page: number;
  totalPages: number;
  startIndex: number;
  endIndex: number;
  slice: T[];
} {
  const safeSize = Math.max(1, Math.trunc(pageSize));
  const totalPages = Math.max(1, Math.ceil(items.length / safeSize));
  const safePage = Math.max(1, Math.min(totalPages, Math.trunc(page) || 1));
  const startIndex = (safePage - 1) * safeSize;
  const endIndex = Math.min(items.length, startIndex + safeSize);
  return {
    page: safePage,
    totalPages,
    startIndex,
    endIndex,
    slice: items.slice(startIndex, endIndex),
  };
}

const RECONCILIATION_REASON_LABELS: Record<string, string> = {
  cod_missing_local: "Không tìm thấy đơn COD trên hệ thống",
  cod_amount_mismatch: "Lệch số tiền COD giữa sàn và file hãng",
  cod_not_delivered: "Đơn COD chưa giao thành công",
  cod_order_cancelled: "Đơn COD đã hủy, không thể đối soát",
  cod_carrier_mismatch: "Sai hãng vận chuyển so với đơn trên sàn",
  cod_invalid_row: "Dòng CSV thiếu mã vận đơn",
  amount_mismatch: "Lệch số tiền thanh toán",
  missing_local: "Thiếu giao dịch trên hệ thống",
  late_payment: "Thanh toán sau khi đơn hết hạn",
  orphan_payment: "Thanh toán chưa gán đơn hàng",
};

const RECONCILIATION_NOTE_LABELS: Record<string, string> = {
  "auto_closed:cod_file_matched": "Tự đóng: đã khớp đối soát COD từ tệp hãng",
  "auto_closed:cod_already_reconciled": "Tự đóng: đơn COD đã được đối soát trước đó",
  "auto_closed:webhook_confirm": "Tự đóng: đã xác nhận qua webhook thanh toán",
  "auto_closed:webhook_late_refund": "Tự đóng: đã tạo hoàn tiền cho thanh toán trễ",
  "auto_closed:expiry_scan_confirm": "Tự đóng: đã xác nhận khi quét đơn hết hạn",
  "auto_closed:expiry_scan_late_refund": "Tự đóng: đã hoàn tiền thanh toán trễ khi quét hết hạn",
  "auto_closed:reconciliation_auto_heal": "Tự đóng: hệ thống tự đồng bộ khi đối soát",
  "auto_closed:reconciliation_late_refund": "Tự đóng: đã hoàn tiền thanh toán trễ khi đối soát",
  "matched and cleared cod_receivable into gateway_clearing":
    "Khớp số tiền — đã kết chuyển phải thu COD sang tài khoản trung gian",
  "cod allocation already reconciled previously":
    "Đơn COD đã được đối soát trước đó (không ghi thêm bút toán)",
  "tracking_code not found in local cod allocations":
    "Không tìm thấy mã vận đơn trong danh sách đơn COD trên sàn",
  "invalid row: tracking_code is empty": "Dòng không hợp lệ: thiếu mã vận đơn",
  "cod order allocation is cancelled; cannot reconcile": "Đơn COD đã hủy, không thể đối soát",
  "cod order not yet delivered (awaiting_collection)":
    "Đơn COD chưa giao thành công (đang chờ thu hộ)",
  "amount mismatch between local payment and provider": "Lệch số tiền giữa sàn và cổng thanh toán",
};

export function formatReconciliationReason(reason: string | undefined): string {
  const trimmed = (reason ?? "").trim();
  if (!trimmed) return "—";
  return RECONCILIATION_REASON_LABELS[trimmed.toLowerCase()] ?? trimmed;
}

export function formatReconciliationNote(note: string | undefined): string {
  const trimmed = (note ?? "").trim();
  if (!trimmed) return "";
  const exact = RECONCILIATION_NOTE_LABELS[trimmed.toLowerCase()];
  if (exact) return exact;

  const amountMatch = /^COD amount mismatch:\s*local=(\d+)\s*VND\s*vs\s*carrier=(\d+)\s*VND$/i.exec(
    trimmed
  );
  if (amountMatch) {
    return `Lệch tiền COD: trên sàn ${formatVnd(Number(amountMatch[1]))} · file hãng ${formatVnd(Number(amountMatch[2]))}`;
  }

  const carrierMatch =
    /^carrier mismatch:\s*allocation carrier='([^']+)'\s*vs\s*file carrier='([^']+)'$/i.exec(
      trimmed
    );
  if (carrierMatch) {
    return `Sai hãng vận chuyển: trên đơn là ${formatCarrierLabel(carrierMatch[1])} · tệp tải lên là ${formatCarrierLabel(carrierMatch[2])}`;
  }

  return trimmed;
}
