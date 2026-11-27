// Run: node --test web/src/features/account/orderReturnSync.test.mjs
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  resolveListMutation,
  resolveReturnRequest,
  resolveReturnWizardSubmitAction,
  upsertItem,
} from "./orderReturnSync.ts";

const __dirname = dirname(fileURLToPath(import.meta.url));

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

test("resolveReturnRequest uses inline return_request or falls back to GET without throwing", async () => {
  const created = sampleReturnRequest();
  let fallbackCalls = 0;

  const direct = await resolveReturnRequest(created, async () => {
    fallbackCalls += 1;
    return { return_request: null };
  });
  assert.equal(fallbackCalls, 0);
  assert.deepEqual(direct, created);

  const recovered = await resolveReturnRequest(null, async () => {
    fallbackCalls += 1;
    return { return_request: created };
  });
  assert.equal(fallbackCalls, 1);
  assert.deepEqual(recovered, created);

  const failedFallback = await resolveReturnRequest(undefined, async () => {
    fallbackCalls += 1;
    throw new Error("network error");
  });
  assert.equal(fallbackCalls, 2);
  assert.equal(failedFallback, null);
});

test("upsertItem replaces matching entries and appends new entries without mutating input", () => {
  const rev1 = sampleReview();
  const rev1Edited = sampleReview({ rating: 4, comment: "Cập nhật" });
  const rev2 = sampleReview({ id: "rev-2", order_item_id: "item-2" });
  const matchReview = (candidate) => (existing) =>
    Boolean(
      (candidate.id && existing.id === candidate.id) ||
        (candidate.order_item_id && existing.order_item_id === candidate.order_item_id)
    );

  assert.deepEqual(upsertItem([], rev1, matchReview(rev1)), [rev1]);
  assert.deepEqual(upsertItem([rev1], rev1Edited, matchReview(rev1Edited)), [rev1Edited]);
  assert.equal(upsertItem([rev1], rev2, matchReview(rev2)).length, 2);
});

test("resolveListMutation updates inline, falls back when omitted, and keeps current list on error", async () => {
  const initial = [sampleRefund()];
  const updated = sampleRefund({
    status: "PENDING",
    bank_code: "VCB",
    bank_account_masked: "******6789",
    bank_account_name: "NGUYEN VAN A",
  });

  let fallbackCalls = 0;
  const inlineResult = await resolveListMutation(
    updated,
    initial,
    (rf) => rf.refund_id === "rf-1",
    async () => {
      fallbackCalls += 1;
      return [];
    }
  );
  assert.equal(fallbackCalls, 0);
  assert.equal(inlineResult[0].status, "PENDING");
  assert.equal(inlineResult[0].bank_code, "VCB");

  const fallbackResult = await resolveListMutation(
    null,
    initial,
    (rf) => rf.refund_id === "rf-1",
    async () => {
      fallbackCalls += 1;
      return [updated];
    }
  );
  assert.equal(fallbackCalls, 1);
  assert.equal(fallbackResult[0].status, "PENDING");

  const failedFallback = await resolveListMutation(
    null,
    initial,
    (rf) => rf.refund_id === "rf-1",
    async () => {
      throw new Error("unavailable");
    }
  );
  assert.deepEqual(failedFallback, initial);
});

test("resolveReturnWizardSubmitAction only permits submission on step 3 (Xác nhận)", () => {
  assert.equal(resolveReturnWizardSubmitAction(1), "advance");
  assert.equal(resolveReturnWizardSubmitAction(2), "advance");
  assert.equal(resolveReturnWizardSubmitAction(3), "submit");

  const src = readFileSync(resolve(__dirname, "OrderDetailClientView.tsx"), "utf8");
  assert.ok(
    src.includes('key="return-wizard-next"') && src.includes('key="return-wizard-submit"'),
    "Return wizard next & submit buttons must use distinct React keys to prevent DOM button reuse"
  );
  assert.ok(
    src.includes('resolveReturnWizardSubmitAction(returnWizardStep) !== "submit"'),
    "handleReturnSubmit must block submission before step 3"
  );
  assert.ok(
    src.includes('"Sản phẩm lỗi hoặc hư hỏng"') &&
      src.includes('"Giao sai sản phẩm hoặc phân loại"') &&
      src.includes('"Không đúng mô tả"') &&
      src.includes('"Thiếu hàng hoặc phụ kiện"') &&
      src.includes('"Lý do khác"'),
    "Return reasons must be industry-neutral"
  );
});
