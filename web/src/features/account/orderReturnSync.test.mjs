// Run: node --test web/src/features/account/orderReturnSync.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  fetchOrderReturnFromGateway,
  mergeSubmittedReview,
  reconcileServerSnapshot,
  resolveClientReturnRequest,
  resolvePostReturnRequest,
  resolveUpdatedRefunds,
  resolveUpdatedReviews,
} from "./orderReturnSync.ts";

function sampleReturnRequest(overrides = {}) {
  return {
    id: "ret-1",
    order_id: "ord-1",
    order_code: "NM-0001",
    buyer_id: "buyer-1",
    shop_id: "shop-1",
    status: "PENDING",
    reason: "Đổi size khác. Áo bị chật vai",
    evidence_urls: ["https://example.com/photo.jpg"],
    seller_response: "",
    seller_evidence_urls: [],
    dispute_reason: "",
    admin_note: "",
    refund_amount_vnd: 199000,
    return_tracking_code: "",
    return_label_url: "",
    return_fee_vnd: 0,
    return_fee_bearer: "",
    goods_received: false,
    events: [],
    ...overrides,
  };
}

function sampleRefund(overrides = {}) {
  return {
    refund_id: "rf-1",
    payment_id: "pay-1",
    order_id: "ord-1",
    amount_vnd: 199000,
    status: "MANUAL_REVIEW",
    reason: "order return refund",
    ...overrides,
  };
}

function sampleReview(overrides = {}) {
  return {
    id: "rev-1",
    product_id: "prod-1",
    order_id: "ord-1",
    order_item_id: "item-1",
    buyer_id: "buyer-1",
    shop_id: "shop-1",
    rating: 5,
    comment: "Chất vải mát",
    image_urls: [],
    ...overrides,
  };
}

test("fetchOrderReturnFromGateway reads /api/v1/orders/{id}/return and maps not_found to null", async () => {
  const seenPaths = [];
  const rr = sampleReturnRequest();

  const found = await fetchOrderReturnFromGateway(
    "ord/1",
    async (path) => {
      seenPaths.push(path);
      return { return_request: rr };
    },
    () => false
  );
  assert.deepEqual(found, rr);
  assert.deepEqual(seenPaths, ["/api/v1/orders/ord%2F1/return"]);

  const notFoundErr = { kind: "not_found" };
  const missing = await fetchOrderReturnFromGateway(
    "ord-2",
    async () => {
      throw notFoundErr;
    },
    (err) => err === notFoundErr
  );
  assert.equal(missing, null);

  const serverErr = new Error("gateway 503");
  await assert.rejects(
    () =>
      fetchOrderReturnFromGateway(
        "ord-3",
        async () => {
          throw serverErr;
        },
        () => false
      ),
    /gateway 503/
  );
});

test("resolvePostReturnRequest fetches via shared GET when POST omits return_request and swallows read errors", async () => {
  const created = sampleReturnRequest();
  let getCalls = 0;

  // Gateway POST returns only order + return_request_id -> BFF calls GET and returns snapshot.
  const fetched = await resolvePostReturnRequest({
    orderId: "ord-1",
    inlineReturnRequest: undefined,
    fetchOrderReturn: async (id) => {
      getCalls += 1;
      assert.equal(id, "ord-1");
      return created;
    },
  });
  assert.equal(getCalls, 1);
  assert.deepEqual(fetched, created);

  // If inline return_request is already present, skip follow-up GET.
  const inline = await resolvePostReturnRequest({
    orderId: "ord-1",
    inlineReturnRequest: created,
    fetchOrderReturn: async () => {
      getCalls += 1;
      return null;
    },
  });
  assert.equal(getCalls, 1);
  assert.deepEqual(inline, created);

  // If follow-up GET fails, return null instead of failing the succeeded POST.
  const degraded = await resolvePostReturnRequest({
    orderId: "ord-1",
    inlineReturnRequest: null,
    fetchOrderReturn: async () => {
      getCalls += 1;
      throw new Error("upstream timeout");
    },
  });
  assert.equal(getCalls, 2);
  assert.equal(degraded, null);
});

test("resolveClientReturnRequest uses inline return_request or falls back to GET /api/orders/{id}/return", async () => {
  const created = sampleReturnRequest();
  let fallbackCalls = 0;

  // Normal path: BFF POST already included return_request -> no extra client GET.
  const direct = await resolveClientReturnRequest({
    inlineReturnRequest: created,
    fetchFallback: async () => {
      fallbackCalls += 1;
      return { return_request: null };
    },
  });
  assert.equal(fallbackCalls, 0);
  assert.deepEqual(direct, created);

  // Degraded BFF path: BFF returned return_request: null -> client fetches GET /api/orders/{id}/return.
  const recovered = await resolveClientReturnRequest({
    inlineReturnRequest: null,
    fetchFallback: async () => {
      fallbackCalls += 1;
      return { return_request: created };
    },
  });
  assert.equal(fallbackCalls, 1);
  assert.deepEqual(recovered, created);

  // Escalate/evidence transition returned inline -> reflects new status immediately.
  const escalated = sampleReturnRequest({ status: "ESCALATED", dispute_reason: "Không đồng ý" });
  const afterEscalate = await resolveClientReturnRequest({
    inlineReturnRequest: escalated,
    fetchFallback: async () => {
      fallbackCalls += 1;
      return { return_request: null };
    },
  });
  assert.equal(fallbackCalls, 1);
  assert.equal(afterEscalate?.status, "ESCALATED");

  // If fallback GET also fails, resolve to null without throwing (mutation still succeeded).
  const failedFallback = await resolveClientReturnRequest({
    inlineReturnRequest: null,
    fetchFallback: async () => {
      fallbackCalls += 1;
      throw new Error("network error");
    },
  });
  assert.equal(fallbackCalls, 2);
  assert.equal(failedFallback, null);
});

test("resolveUpdatedRefunds updates inline or falls back to GET /api/orders/{id}/refunds", async () => {
  const initial = [sampleRefund()];
  const updated = sampleRefund({
    status: "PENDING",
    bank_code: "VCB",
    bank_account_masked: "******6789",
    bank_account_name: "NGUYEN VAN A",
  });

  let fallbackCalls = 0;
  const inlineResult = await resolveUpdatedRefunds({
    refundId: "rf-1",
    updatedRefund: updated,
    currentRefunds: initial,
    fetchFallback: async () => {
      fallbackCalls += 1;
      return { items: [] };
    },
  });
  assert.equal(fallbackCalls, 0);
  assert.equal(inlineResult[0].status, "PENDING");
  assert.equal(inlineResult[0].bank_code, "VCB");

  const fallbackResult = await resolveUpdatedRefunds({
    refundId: "rf-1",
    updatedRefund: null,
    currentRefunds: initial,
    fetchFallback: async () => {
      fallbackCalls += 1;
      return { items: [updated] };
    },
  });
  assert.equal(fallbackCalls, 1);
  assert.equal(fallbackResult[0].status, "PENDING");

  const failedFallback = await resolveUpdatedRefunds({
    refundId: "rf-1",
    updatedRefund: null,
    currentRefunds: initial,
    fetchFallback: async () => {
      throw new Error("unavailable");
    },
  });
  assert.deepEqual(failedFallback, initial);
});

test("mergeSubmittedReview and resolveUpdatedReviews upsert cleanly and fallback when review omitted", async () => {
  const rev1 = sampleReview();
  const rev1Edited = sampleReview({ rating: 4, comment: "Cập nhật" });
  const rev2 = sampleReview({ id: "rev-2", order_item_id: "item-2" });

  assert.equal(mergeSubmittedReview([], rev1).length, 1);
  assert.deepEqual(mergeSubmittedReview([rev1], rev1Edited), [rev1Edited]);
  assert.equal(mergeSubmittedReview([rev1], rev2).length, 2);

  let fallbackCalls = 0;
  const fromFallback = await resolveUpdatedReviews({
    submittedReview: null,
    currentReviews: [],
    fetchFallback: async () => {
      fallbackCalls += 1;
      return { reviews: [rev1] };
    },
  });
  assert.equal(fallbackCalls, 1);
  assert.deepEqual(fromFallback, [rev1]);
});

test("reconcileServerSnapshot resets on order change and preserves client state on transient null refresh", () => {
  const rrPending = sampleReturnRequest({ status: "PENDING" });
  const rrEscalated = sampleReturnRequest({ status: "ESCALATED" });

  // Same order, transient null from server refresh -> keep freshly-created local returnRequest.
  assert.deepEqual(
    reconcileServerSnapshot({ orderChanged: false, current: rrPending, incoming: null }),
    rrPending
  );

  // Same order, server refresh brings fresher non-null snapshot -> adopt incoming.
  assert.deepEqual(
    reconcileServerSnapshot({ orderChanged: false, current: rrPending, incoming: rrEscalated }),
    rrEscalated
  );

  // Navigating to a different order with no return request -> reset cleanly to null.
  assert.equal(
    reconcileServerSnapshot({ orderChanged: true, current: rrPending, incoming: null }),
    null
  );
});
