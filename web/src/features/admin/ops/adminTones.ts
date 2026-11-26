import type { StatusTone } from "@/components/app";

function norm(status?: string | null): string {
  return typeof status === "string" ? status.trim().toUpperCase() : "";
}

export function adminShopStatusTone(status?: string | null): StatusTone {
  switch (norm(status)) {
    case "ACTIVE":
      return "success";
    case "PENDING":
      return "attention";
    case "REJECTED":
    case "SUSPENDED":
      return "critical";
    default:
      return "neutral";
  }
}

export function adminReturnStatusTone(status?: string | null): StatusTone {
  const key = norm(status).replace(/^RETURN_STATUS_/, "");
  switch (key) {
    case "PENDING":
    case "AWAITING_SHIPMENT":
    case "REFUNDING":
      return "attention";
    case "EVIDENCE_REQUESTED":
      return "warning";
    case "IN_TRANSIT":
    case "RECEIVED":
      return "info";
    case "REFUNDED":
      return "success";
    case "REJECTED":
    case "ESCALATED":
      return "critical";
    case "CLOSED":
    default:
      return "neutral";
  }
}

export function adminDomainStatusTone(status?: string | null): StatusTone {
  switch (norm(status)) {
    case "ACTIVE":
      return "success";
    case "PENDING":
    case "PENDING_VERIFY":
    case "VERIFYING":
      return "attention";
    case "FAILED":
      return "critical";
    default:
      return "neutral";
  }
}

export function adminBannedActionTone(action?: string | null): StatusTone {
  switch (norm(action)) {
    case "BLOCK":
      return "critical";
    case "REVIEW":
      return "warning";
    default:
      return "neutral";
  }
}
