import test from "node:test";
import assert from "node:assert/strict";
import {
  evaluateSellerPayoutReadiness,
  getSellerPayoutStatusMeta,
  mintClientPayoutIdempotencyKey,
  parseWithdrawAmountVnd,
  validateWithdrawAmountInput,
} from "./SellerFinancePayoutHelpers.ts";

test("parseWithdrawAmountVnd accepts positive integers and rejects decimals/negatives/empty", () => {
  assert.equal(parseWithdrawAmountVnd("150000"), 150000);
  assert.equal(parseWithdrawAmountVnd(" 50000 "), 50000);
  assert.equal(parseWithdrawAmountVnd("0"), null);
  assert.equal(parseWithdrawAmountVnd("-1000"), null);
  assert.equal(parseWithdrawAmountVnd("100.5"), null);
  assert.equal(parseWithdrawAmountVnd("100,000"), null);
  assert.equal(parseWithdrawAmountVnd(""), null);
});

test("validateWithdrawAmountInput enforces integer VND > 0 and <= availableVnd", () => {
  assert.deepEqual(validateWithdrawAmountInput("250000", 250000), {
    ok: true,
    amountVnd: 250000,
  });
  assert.equal(validateWithdrawAmountInput("250001", 250000).ok, false);
  assert.equal(validateWithdrawAmountInput("0", 250000).ok, false);
  assert.equal(validateWithdrawAmountInput("10000", 0).ok, false);
});

test("evaluateSellerPayoutReadiness prioritizes bank status, 24h lock, pending payout, and balance", () => {
  assert.deepEqual(
    evaluateSellerPayoutReadiness({
      bankAccountStatus: null,
      isPayoutLocked: false,
      availableVnd: 200000,
      payoutPendingVnd: 0,
      hasPendingPayoutRow: false,
    }),
    { ready: false, reason: "no_bank" }
  );

  assert.deepEqual(
    evaluateSellerPayoutReadiness({
      bankAccountStatus: "PENDING_OTP",
      isPayoutLocked: false,
      availableVnd: 200000,
      payoutPendingVnd: 0,
      hasPendingPayoutRow: false,
    }),
    { ready: false, reason: "bank_unverified" }
  );

  assert.deepEqual(
    evaluateSellerPayoutReadiness({
      bankAccountStatus: "VERIFIED",
      isPayoutLocked: true,
      availableVnd: 200000,
      payoutPendingVnd: 0,
      hasPendingPayoutRow: false,
    }),
    { ready: false, reason: "bank_locked" }
  );

  assert.deepEqual(
    evaluateSellerPayoutReadiness({
      bankAccountStatus: "VERIFIED",
      isPayoutLocked: false,
      availableVnd: 200000,
      payoutPendingVnd: 100000,
      hasPendingPayoutRow: false,
    }),
    { ready: false, reason: "pending_exists" }
  );

  assert.deepEqual(
    evaluateSellerPayoutReadiness({
      bankAccountStatus: "VERIFIED",
      isPayoutLocked: false,
      availableVnd: 0,
      payoutPendingVnd: 0,
      hasPendingPayoutRow: false,
    }),
    { ready: false, reason: "zero_balance" }
  );

  assert.deepEqual(
    evaluateSellerPayoutReadiness({
      bankAccountStatus: "VERIFIED",
      isPayoutLocked: false,
      availableVnd: 150000,
      payoutPendingVnd: 0,
      hasPendingPayoutRow: false,
    }),
    { ready: true }
  );
});

test("getSellerPayoutStatusMeta and mintClientPayoutIdempotencyKey", () => {
  assert.deepEqual(getSellerPayoutStatusMeta("PENDING"), {
    label: "Chờ duyệt",
    tone: "attention",
  });
  assert.deepEqual(getSellerPayoutStatusMeta("COMPLETED"), {
    label: "Đã chi trả",
    tone: "success",
  });
  assert.deepEqual(getSellerPayoutStatusMeta("REJECTED"), {
    label: "Từ chối",
    tone: "critical",
  });
  assert.equal(
    mintClientPayoutIdempotencyKey(() => "abc-123"),
    "payout-abc-123"
  );
});
