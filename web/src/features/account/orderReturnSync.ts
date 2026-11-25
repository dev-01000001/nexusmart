import type {
  GatewayProductReview,
  GatewayRefund,
  GatewayReturnRequest,
} from "@/lib/gateway-types";

export interface OrderReturnEnvelope {
  return_request?: GatewayReturnRequest | null;
}

export interface OrderRefundsEnvelope {
  items?: GatewayRefund[] | null;
  refunds?: GatewayRefund[] | null;
}

export interface OrderReviewsEnvelope {
  reviews?: GatewayProductReview[] | null;
}

/**
 * Shared gateway reader for `GET /api/v1/orders/{id}/return`.
 * Returns `null` when gateway responds with `not_found`, and rethrows other errors.
 */
export async function fetchOrderReturnFromGateway(
  orderId: string,
  fetchPath: (path: string) => Promise<OrderReturnEnvelope>,
  isNotFoundError: (err: unknown) => boolean
): Promise<GatewayReturnRequest | null> {
  try {
    const res = await fetchPath(`/api/v1/orders/${encodeURIComponent(orderId)}/return`);
    return res?.return_request ?? null;
  } catch (err) {
    if (isNotFoundError(err)) {
      return null;
    }
    throw err;
  }
}

/**
 * Resolves the `return_request` snapshot to include in BFF `POST /api/orders/{id}/return`.
 * Uses the same GET reader when gateway POST omits `return_request`.
 * Swallows read failures so a successful return creation still responds 200 (client falls back to GET).
 */
export async function resolvePostReturnRequest(opts: {
  orderId: string;
  inlineReturnRequest?: GatewayReturnRequest | null;
  fetchOrderReturn: (orderId: string) => Promise<GatewayReturnRequest | null>;
}): Promise<GatewayReturnRequest | null> {
  if (opts.inlineReturnRequest) {
    return opts.inlineReturnRequest;
  }
  try {
    return (await opts.fetchOrderReturn(opts.orderId)) ?? null;
  } catch {
    return null;
  }
}

/**
 * Client-side resolver after creating or mutating a return request (create / escalate / add evidence).
 * Uses inline `return_request` when present; otherwise falls back to `GET /api/orders/{id}/return`
 * without turning an already-succeeded mutation into an error if the fallback read fails.
 */
export async function resolveClientReturnRequest(opts: {
  inlineReturnRequest?: GatewayReturnRequest | null;
  fetchFallback: () => Promise<OrderReturnEnvelope>;
}): Promise<GatewayReturnRequest | null> {
  if (opts.inlineReturnRequest) {
    return opts.inlineReturnRequest;
  }
  try {
    const res = await opts.fetchFallback();
    return res?.return_request ?? null;
  } catch {
    return null;
  }
}

/**
 * Updates the local refund list after submitting a buyer refund bank account,
 * falling back to `GET /api/orders/{id}/refunds` if the mutation response omitted `refund`.
 */
export async function resolveUpdatedRefunds(opts: {
  refundId: string;
  updatedRefund?: GatewayRefund | null;
  currentRefunds: readonly GatewayRefund[];
  fetchFallback: () => Promise<OrderRefundsEnvelope>;
}): Promise<GatewayRefund[]> {
  if (opts.updatedRefund) {
    const updated = opts.updatedRefund;
    let matched = false;
    const next = opts.currentRefunds.map((rf) => {
      if (rf.refund_id === opts.refundId) {
        matched = true;
        return updated;
      }
      return rf;
    });
    return matched ? next : [...next, updated];
  }
  try {
    const res = await opts.fetchFallback();
    const list = res?.items ?? res?.refunds;
    if (Array.isArray(list)) {
      return list;
    }
  } catch {
    // Keep existing list if fallback read fails after a succeeded mutation.
  }
  return [...opts.currentRefunds];
}

/**
 * Upserts a newly submitted review into the local order reviews list without duplicates.
 */
export function mergeSubmittedReview(
  currentReviews: readonly GatewayProductReview[],
  submittedReview: GatewayProductReview
): GatewayProductReview[] {
  let replaced = false;
  const next = currentReviews.map((existing) => {
    if (
      (submittedReview.id && existing.id === submittedReview.id) ||
      (submittedReview.order_item_id && existing.order_item_id === submittedReview.order_item_id)
    ) {
      replaced = true;
      return submittedReview;
    }
    return existing;
  });
  return replaced ? next : [...next, submittedReview];
}

/**
 * Updates the local reviews list after submitting a product review,
 * falling back to `GET /api/orders/{id}/reviews` if the mutation response omitted `review`.
 */
export async function resolveUpdatedReviews(opts: {
  submittedReview?: GatewayProductReview | null;
  currentReviews: readonly GatewayProductReview[];
  fetchFallback: () => Promise<OrderReviewsEnvelope>;
}): Promise<GatewayProductReview[]> {
  if (opts.submittedReview) {
    return mergeSubmittedReview(opts.currentReviews, opts.submittedReview);
  }
  try {
    const res = await opts.fetchFallback();
    if (Array.isArray(res?.reviews)) {
      return res.reviews;
    }
  } catch {
    // Keep existing list if fallback read fails after a succeeded mutation.
  }
  return [...opts.currentReviews];
}

/**
 * Reconciles a nullable server snapshot prop into client state on navigation or `router.refresh()`.
 * Resets unconditionally when `orderChanged` is true; on the same order, avoids clobbering
 * freshly-mutated client state with a transient `null` from a failed optional server sub-fetch.
 */
export function reconcileServerSnapshot<T>(opts: {
  orderChanged: boolean;
  current: T | null;
  incoming: T | null;
}): T | null {
  if (opts.orderChanged) {
    return opts.incoming;
  }
  return opts.incoming ?? opts.current;
}
