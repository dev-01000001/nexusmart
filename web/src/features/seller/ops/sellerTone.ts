import { orderStatusTone, shipmentJobTone, type StatusTone } from "@/components/app";
import { getPaymentStatusInfo, isShopInfoRequested } from "@/features/account/orderMeta";

export function resolveSellerOrderTone(
  status?: string | null,
  shipmentJobStatus?: string | null,
  trackingCode?: string | null
): StatusTone {
  const normalized = (status ?? "").trim().toUpperCase();
  const job = (shipmentJobStatus ?? "").trim().toUpperCase();
  const hasTracking = Boolean((trackingCode ?? "").trim());

  if ((normalized === "CONFIRMED" || normalized === "PREPARING") && !hasTracking) {
    if (job === "QUEUED" || job === "FAILED") {
      return shipmentJobTone(job);
    }
  }
  if (normalized === "PREPARING" && hasTracking) {
    return orderStatusTone("READY_TO_SHIP");
  }
  if (normalized === "PENDING_PAYMENT") {
    return orderStatusTone("PENDING");
  }
  return orderStatusTone(normalized);
}

export function resolveSellerReturnTone(status?: string | null): StatusTone {
  const normalized = (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^RETURN_STATUS_/, "");

  switch (normalized) {
    case "PENDING":
    case "REQUESTED":
    case "RETURN_REQUESTED":
    case "OPEN":
    case "RECEIVED":
    case "REFUNDING":
      return "attention";
    case "AWAITING_SHIPMENT":
    case "EVIDENCE_REQUESTED":
      return "warning";
    case "IN_TRANSIT":
    case "RETURNING":
    case "SHIPPING_BACK":
    case "PICKUP_SCHEDULED":
      return "info";
    case "APPROVED":
    case "ACCEPTED":
    case "REFUNDED":
    case "COMPLETED":
    case "RETURNED":
      return "success";
    case "REJECTED":
    case "DECLINED":
    case "ESCALATED":
    case "DISPUTED":
      return "critical";
    case "CLOSED":
    case "CANCELLED":
    case "CANCELED":
    default:
      return "neutral";
  }
}

export function resolveSellerShopTone(
  status?: string | null,
  rejectionReason?: string | null
): StatusTone {
  const normalized = (status ?? "").trim().toUpperCase();
  if (normalized === "ACTIVE") return "success";
  if (normalized === "PENDING") {
    return isShopInfoRequested({ status: normalized, rejection_reason: rejectionReason ?? "" })
      ? "warning"
      : "attention";
  }
  if (normalized === "REJECTED" || normalized === "SUSPENDED") return "critical";
  return "neutral";
}

export function resolvePaymentStatusTone(
  paymentStatus: string,
  paymentMethod: string,
  orderStatus?: string
): StatusTone {
  const info = getPaymentStatusInfo(paymentStatus, paymentMethod, orderStatus);
  if (info.tone === "pos") return "success";
  if (info.tone === "neg") return "critical";
  if (info.tone === "warn") return "warning";
  return "neutral";
}
