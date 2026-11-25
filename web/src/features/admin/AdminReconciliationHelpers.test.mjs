// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminReconciliationHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  validateIdempotencyKeyHeader,
  validateManualRefundInput,
  validateReconciliationQueueResolveInput,
  validateRefundApprovalDecisionInput,
  validateSettleRefundInput,
} from "./AdminReconciliationHelpers.ts";

test("validateIdempotencyKeyHeader accepts safe tokens and rejects blank/whitespace", () => {
  assert.equal(validateIdempotencyKeyHeader("settle-550e8400"), "settle-550e8400");
  assert.equal(validateIdempotencyKeyHeader("abc"), null);
  assert.equal(validateIdempotencyKeyHeader("has space"), null);
  assert.equal(validateIdempotencyKeyHeader(""), null);
});

test("validateReconciliationQueueResolveInput enforces payment_id for ASSIGN only", () => {
  const missingPayment = validateReconciliationQueueResolveInput({
    action: "ASSIGN",
    payment_id: "",
    note: "Gán khớp",
  });
  assert.equal(missingPayment.ok, false);
  if (!missingPayment.ok) assert.equal(missingPayment.code, "INVALID_PAYMENT_ID");

  const validAssign = validateReconciliationQueueResolveInput({
    action: "ASSIGN",
    payment_id: "pay-123",
    note: "Khớp giao dịch",
  });
  assert.equal(validAssign.ok, true);

  const validRefund = validateReconciliationQueueResolveInput({
    action: "REFUND",
    note: "Hoàn tiền chênh lệch",
  });
  assert.equal(validRefund.ok, true);
  if (validRefund.ok) assert.equal(validRefund.value.payment_id, "");

  const badAction = validateReconciliationQueueResolveInput({ action: "DELETE" });
  assert.equal(badAction.ok, false);
});

test("validateManualRefundInput requires positive integer VND and safe IDs", () => {
  const ok = validateManualRefundInput({
    payment_id: "pay-01",
    order_id: "ord-01",
    amount_vnd: 199000,
    reason: "Hoàn tiền đơn bị hủy",
  });
  assert.equal(ok.ok, true);

  const floatAmt = validateManualRefundInput({
    payment_id: "pay-01",
    order_id: "ord-01",
    amount_vnd: 199000.5,
    reason: "Số thực không hợp lệ",
  });
  assert.equal(floatAmt.ok, false);
  if (!floatAmt.ok) assert.equal(floatAmt.code, "INVALID_AMOUNT_VND");

  const badPathId = validateManualRefundInput({
    payment_id: "../other",
    order_id: "ord-01",
    amount_vnd: 50000,
    reason: "Chống path traversal",
  });
  assert.equal(badPathId.ok, false);
  if (!badPathId.ok) assert.equal(badPathId.code, "INVALID_PAYMENT_ID");
});

test("validateRefundApprovalDecisionInput and validateSettleRefundInput enforce required fields", () => {
  const rejectShort = validateRefundApprovalDecisionInput({ approve: false, note: "no" });
  assert.equal(rejectShort.ok, false);
  if (!rejectShort.ok) assert.equal(rejectShort.code, "NOTE_REQUIRED");

  const approveEmptyNote = validateRefundApprovalDecisionInput({ approve: true, note: "" });
  assert.equal(approveEmptyNote.ok, true);

  const badSettle = validateSettleRefundInput({ provider_refund_id: "X" });
  assert.equal(badSettle.ok, false);

  const okSettle = validateSettleRefundInput({
    provider_refund_id: "FT2611250001",
    note: "Đã chuyển khoản",
  });
  assert.equal(okSettle.ok, true);
});
