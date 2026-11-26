export type StatusTone = "success" | "attention" | "warning" | "info" | "critical" | "neutral";

function normalizeStatus(status?: string | null): string {
  return typeof status === "string" ? status.trim().toUpperCase() : "";
}

export function orderStatusTone(status?: string | null): StatusTone {
  switch (normalizeStatus(status)) {
    case "PENDING":
    case "RETURN_REQUESTED":
      return "warning";
    case "CONFIRMED":
    case "PREPARING":
      return "attention";
    case "READY_TO_SHIP":
    case "SHIPPING":
      return "info";
    case "DELIVERED":
    case "COMPLETED":
      return "success";
    case "DELIVERY_FAILED":
    case "CANCELLED":
      return "critical";
    case "RETURNED":
    case "REFUNDED":
    default:
      return "neutral";
  }
}

export function shipmentJobTone(status?: string | null): StatusTone {
  switch (normalizeStatus(status)) {
    case "QUEUED":
      return "info";
    case "FAILED":
      return "critical";
    default:
      return "neutral";
  }
}

export function productStatusTone(status?: string | null): StatusTone {
  switch (normalizeStatus(status)) {
    case "APPROVED":
      return "success";
    case "PENDING":
      return "attention";
    case "REJECTED":
      return "critical";
    case "OUT_OF_STOCK":
      return "warning";
    case "HIDDEN":
    default:
      return "neutral";
  }
}

export function bankAccountStatusTone(status?: string | null): StatusTone {
  switch (normalizeStatus(status)) {
    case "VERIFIED":
      return "success";
    case "PENDING_OTP":
    case "PENDING_ADMIN_REVIEW":
      return "attention";
    case "REJECTED":
      return "critical";
    default:
      return "neutral";
  }
}

export function siteStatusTone(status?: string | null): StatusTone {
  switch (normalizeStatus(status)) {
    case "PUBLISHED":
      return "success";
    case "DRAFT":
      return "info";
    case "SUSPENDED":
      return "critical";
    default:
      return "neutral";
  }
}
