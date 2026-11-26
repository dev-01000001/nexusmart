import { orderStatusTone, type StatusTone } from "@/components/app/statusTone";
import { formatDateTimeVi } from "@/lib/format";
import { type BadgeTone, normalizeEnumSuffix } from "../orderMeta";

export { orderStatusTone };

export const SITE_PAGE_CONTAINER =
  "mx-auto w-full max-w-[var(--site-max-w,1260px)] px-[clamp(1rem,2.2vw,2rem)] py-6 md:py-8";

export function badgeToneToStatusTone(tone: BadgeTone): StatusTone {
  switch (tone) {
    case "pos":
      return "success";
    case "warn":
      return "warning";
    case "neg":
      return "critical";
    default:
      return "neutral";
  }
}

export function paymentStatusTone(
  paymentStatus: string,
  paymentMethod: string,
  orderStatus?: string
): StatusTone {
  const status = (paymentStatus ?? "").trim().toUpperCase();
  const method = (paymentMethod ?? "").trim().toUpperCase();
  const ordStatus = (orderStatus ?? "").trim().toUpperCase();

  if (status === "REFUNDED") return "neutral";
  if (method === "COD" || status === "COD_PENDING") {
    if (
      ordStatus === "DELIVERED" ||
      ordStatus === "COMPLETED" ||
      ordStatus === "RETURN_REQUESTED" ||
      status === "SUCCEEDED" ||
      status === "PAID"
    ) {
      return "success";
    }
    return "neutral";
  }
  if (status === "SUCCEEDED" || status === "PAID") return "success";
  if (status === "FAILED" || status === "EXPIRED") return "critical";
  if (status === "PENDING") return "warning";
  return "neutral";
}

export function shipmentStatusTone(rawStatus: string | undefined): StatusTone {
  const key = normalizeEnumSuffix(rawStatus, "SHIPMENT_STATUS_");
  switch (key) {
    case "DELIVERED":
      return "success";
    case "PICKED_UP":
    case "IN_TRANSIT":
    case "OUT_FOR_DELIVERY":
    case "SHIPPING":
      return "info";
    case "CREATED":
    case "PENDING":
    case "READY_TO_PICK":
    case "READY_TO_SHIP":
    case "AWAITING_PICKUP":
      return "attention";
    case "FAILED":
    case "DELIVERY_FAILED":
    case "LOST":
      return "critical";
    case "RETURNED":
    case "RETURNING":
    case "CANCELLED":
    default:
      return "neutral";
  }
}

export function returnStatusTone(rawStatus: string | undefined): StatusTone {
  const key = normalizeEnumSuffix(rawStatus, "RETURN_STATUS_");
  switch (key) {
    case "PENDING":
    case "AWAITING_SHIPMENT":
    case "ESCALATED":
      return "attention";
    case "EVIDENCE_REQUESTED":
      return "warning";
    case "IN_TRANSIT":
    case "RECEIVED":
    case "REFUNDING":
      return "info";
    case "REFUNDED":
      return "success";
    case "REJECTED":
      return "critical";
    case "CLOSED":
    default:
      return "neutral";
  }
}

export function refundStatusTone(
  rawStatus: string | undefined,
  refund?: { provider_m_refund_id?: string | null } | null
): StatusTone {
  const key = normalizeEnumSuffix(rawStatus, "REFUND_STATUS_");
  switch (key) {
    case "DONE":
    case "COMPLETED":
      return "success";
    case "MANUAL_REVIEW":
      return "warning";
    case "PENDING":
      return refund?.provider_m_refund_id ? "info" : "attention";
    case "PENDING_APPROVAL":
      return "attention";
    case "FAILED":
    case "REJECTED":
      return "critical";
    default:
      return "neutral";
  }
}

export interface ReturnLifecycleStep {
  key: string;
  label: string;
}

/** Main 6 progression milestones of the 10-status return machine (dispute/closed branches shown separately). */
export const RETURN_MAIN_MILESTONES: readonly ReturnLifecycleStep[] = [
  { key: "PENDING", label: "Gửi yêu cầu" },
  { key: "AWAITING_SHIPMENT", label: "Chờ gửi hoàn" },
  { key: "IN_TRANSIT", label: "Đang chuyển về" },
  { key: "RECEIVED", label: "Shop đã nhận" },
  { key: "REFUNDING", label: "Đang hoàn tiền" },
  { key: "REFUNDED", label: "Đã hoàn tiền" },
] as const;

export function getReturnLifecycleProgress(rawStatus: string | undefined): {
  stepIndex: number;
  percent: number;
  isDisputeBranch: boolean;
  isClosed: boolean;
} {
  const key = normalizeEnumSuffix(rawStatus, "RETURN_STATUS_");
  const idx = RETURN_MAIN_MILESTONES.findIndex((m) => m.key === key);
  if (idx >= 0) {
    return {
      stepIndex: idx,
      percent: Math.round(((idx + 1) / RETURN_MAIN_MILESTONES.length) * 100),
      isDisputeBranch: false,
      isClosed: false,
    };
  }
  if (key === "REJECTED" || key === "ESCALATED" || key === "EVIDENCE_REQUESTED") {
    return { stepIndex: 0, percent: 25, isDisputeBranch: true, isClosed: false };
  }
  return { stepIndex: 5, percent: 100, isDisputeBranch: false, isClosed: key === "CLOSED" };
}

export function splitDateTimeVi(iso: string): { date: string; time: string } | null {
  const formatted = formatDateTimeVi(iso);
  if (!formatted) return null;
  const parts = formatted.includes(", ") ? formatted.split(", ") : formatted.split(" · ");
  if (parts.length !== 2) return null;
  return { date: parts[0], time: parts[1] };
}
