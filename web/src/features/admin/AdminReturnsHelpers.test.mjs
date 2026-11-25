// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminReturnsHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canAdminResolveReturn,
  getReturnDecisionLabelVi,
  getReturnFeeBearerLabelVi,
  isValidAdminPathId,
  validateReturnResolveInput,
} from "./AdminReturnsHelpers.ts";

test("isValidAdminPathId accepts UUIDs/safe IDs and rejects traversal, slashes, spaces, and empty strings", () => {
  assert.equal(isValidAdminPathId("550e8400-e29b-41d4-a716-446655440000"), true);
  assert.equal(isValidAdminPathId("ret_01-abc"), true);
  assert.equal(isValidAdminPathId(""), false);
  assert.equal(isValidAdminPathId("   "), false);
  assert.equal(isValidAdminPathId("../admin"), false);
  assert.equal(isValidAdminPathId("abc/def"), false);
  assert.equal(isValidAdminPathId("abc%2fdef"), false);
  assert.equal(isValidAdminPathId("abc def"), false);
  assert.equal(isValidAdminPathId("-leading-dash"), false);
  assert.equal(isValidAdminPathId("a".repeat(129)), false);
});

test("canAdminResolveReturn allows only ESCALATED and EVIDENCE_REQUESTED states", () => {
  assert.equal(canAdminResolveReturn("ESCALATED"), true);
  assert.equal(canAdminResolveReturn("RETURN_STATUS_ESCALATED"), true);
  assert.equal(canAdminResolveReturn("EVIDENCE_REQUESTED"), true);
  assert.equal(canAdminResolveReturn("PENDING"), false);
  assert.equal(canAdminResolveReturn("REFUNDED"), false);
  assert.equal(canAdminResolveReturn("CLOSED"), false);
});

test("validateReturnResolveInput validates all 4 decisions, fee bearer, goods_received guard, and note rules", () => {
  const refund = validateReturnResolveInput({
    decision: "REFUND",
    return_fee_bearer: "SELLER",
    note: "Xác nhận lỗi nhà sản xuất",
  });
  assert.equal(refund.ok, true);
  if (refund.ok) {
    assert.deepEqual(refund.value, {
      decision: "REFUND",
      return_fee_bearer: "SELLER",
      note: "Xác nhận lỗi nhà sản xuất",
    });
  }

  const returnAndRefund = validateReturnResolveInput(
    {
      decision: "RETURN_AND_REFUND",
      return_fee_bearer: "PLATFORM",
      note: "",
    },
    { goodsReceived: false }
  );
  assert.equal(returnAndRefund.ok, true);
  if (returnAndRefund.ok) {
    assert.equal(returnAndRefund.value.return_fee_bearer, "PLATFORM");
  }

  const blockedAlreadyReceived = validateReturnResolveInput(
    {
      decision: "RETURN_AND_REFUND",
      return_fee_bearer: "SELLER",
      note: "Thử tạo vận đơn lại",
    },
    { goodsReceived: true }
  );
  assert.equal(blockedAlreadyReceived.ok, false);
  if (!blockedAlreadyReceived.ok) {
    assert.equal(blockedAlreadyReceived.code, "GOODS_ALREADY_RECEIVED");
  }

  const reqEvidenceShort = validateReturnResolveInput({
    decision: "REQUEST_EVIDENCE",
    note: "abc",
  });
  assert.equal(reqEvidenceShort.ok, false);
  if (!reqEvidenceShort.ok) assert.equal(reqEvidenceShort.code, "NOTE_REQUIRED");

  const reqEvidenceOk = validateReturnResolveInput({
    decision: "REQUEST_EVIDENCE",
    return_fee_bearer: "BUYER",
    note: "Cần bổ sung video mở hộp nguyên seal",
  });
  assert.equal(reqEvidenceOk.ok, true);
  if (reqEvidenceOk.ok) {
    // Fee bearer cleared for non-refund decisions
    assert.equal(reqEvidenceOk.value.return_fee_bearer, "");
  }

  const rejectNoNote = validateReturnResolveInput({
    decision: "REJECT",
    note: " ",
  });
  assert.equal(rejectNoNote.ok, false);
  if (!rejectNoNote.ok) assert.equal(rejectNoNote.code, "NOTE_REQUIRED");

  const badDecision = validateReturnResolveInput({
    decision: "REPLACE",
    note: "Old complaint decision",
  });
  assert.equal(badDecision.ok, false);
  if (!badDecision.ok) assert.equal(badDecision.code, "INVALID_DECISION");

  const badBearer = validateReturnResolveInput({
    decision: "REFUND",
    return_fee_bearer: "CARRIER",
    note: "Ghi chú hợp lệ",
  });
  assert.equal(badBearer.ok, false);
  if (!badBearer.ok) assert.equal(badBearer.code, "INVALID_FEE_BEARER");

  assert.equal(getReturnFeeBearerLabelVi("SELLER"), "Gian hàng chịu phí");
  assert.equal(getReturnFeeBearerLabelVi("BUYER"), "Người mua chịu phí");
  assert.equal(getReturnFeeBearerLabelVi("PLATFORM"), "Sàn hỗ trợ phí");
  assert.equal(getReturnDecisionLabelVi("REQUEST_EVIDENCE"), "Yêu cầu người mua bổ sung bằng chứng");
});
