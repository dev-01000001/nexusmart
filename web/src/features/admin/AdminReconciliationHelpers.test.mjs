// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminReconciliationHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  buildAdminMoneyFingerprint,
  isDefinitiveAdminMoneyError,
  nextAdminMoneyIdempotencyCacheAfterError,
  resolveAdminMoneyIdempotencyKey,
  validateIdempotencyKeyHeader,
  validateManualRefundInput,
  validateReconciliationQueueResolveInput,
  validateRefundApprovalDecisionInput,
  validateSettleRefundInput,
} from "./AdminReconciliationHelpers.ts";
import {
  buildCodCarrierOptions,
  DEFAULT_COD_FALLBACK_CARRIER_CODES,
} from "./codReconciliationHelper.ts";
import { getOptionalSalesChannelMeta } from "../../lib/salesChannel.ts";

const WEB_SRC = resolve(fileURLToPath(new URL("../..", import.meta.url)));

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

test("resolveAdminMoneyIdempotencyKey pins key across ambiguous/5xx retries, rotates on payload change, and clears on definitive 4xx or success", () => {
  let seq = 0;
  const uuidFn = () => `uuid-${++seq}`;

  const fp1 = buildAdminMoneyFingerprint("refund", {
    payment_id: "pay-100",
    order_id: "ord-100",
    amount_vnd: 200000,
    reason: "Hoàn một phần",
  });

  // First click mints key #1
  const first = resolveAdminMoneyIdempotencyKey({
    prefix: "refund",
    fingerprint: fp1,
    cache: null,
    uuidFn,
  });
  assert.equal(first.key, "refund-uuid-1");
  assert.equal(validateIdempotencyKeyHeader(first.key), first.key);

  // Ambiguous failures (504 timeout, 503 unavailable, 500 internal, 409 ABORTED, 408, 429, network error) KEEP pinned key
  const nonDefinitiveErrors = [
    { status: 504, code: "DEADLINE_EXCEEDED" },
    { status: 503, code: "UNAVAILABLE" },
    { status: 500, code: "INTERNAL_ERROR" },
    { status: 499, code: "CANCELED" },
    { status: 409, code: "ABORTED" },
    { status: 408, code: "TIMEOUT" },
    { status: 429, code: "RESOURCE_EXHAUSTED" },
    { status: 0, code: "NETWORK" },
    new TypeError("Failed to fetch"),
  ];

  let cache = first.cache;
  for (const err of nonDefinitiveErrors) {
    assert.equal(
      isDefinitiveAdminMoneyError(err),
      false,
      `expected non-definitive for ${JSON.stringify(err)}`
    );
    cache = nextAdminMoneyIdempotencyCacheAfterError(cache, err);
    const retry = resolveAdminMoneyIdempotencyKey({
      prefix: "refund",
      fingerprint: fp1,
      cache,
      uuidFn,
    });
    assert.equal(retry.key, "refund-uuid-1", "must reuse pinned key on ambiguous retry");
    cache = retry.cache;
  }

  // Changing payload (e.g. amount_vnd 200000 -> 250000) MUST rotate key immediately
  const fp2 = buildAdminMoneyFingerprint("refund", {
    payment_id: "pay-100",
    order_id: "ord-100",
    amount_vnd: 250000,
    reason: "Hoàn một phần",
  });
  assert.notEqual(fp1, fp2);
  const changed = resolveAdminMoneyIdempotencyKey({
    prefix: "refund",
    fingerprint: fp2,
    cache,
    uuidFn,
  });
  assert.equal(changed.key, "refund-uuid-2");

  // Definitive 4xx errors (400, 403, 404, 409 FAILED_PRECONDITION, 422) CLEAR cache so next retry gets fresh key
  const definitive4xxErrors = [
    { status: 400, code: "INVALID_ARGUMENT" },
    { status: 403, code: "PERMISSION_DENIED" },
    { status: 404, code: "NOT_FOUND" },
    { status: 409, code: "FAILED_PRECONDITION" },
    { status: 422, code: "UNPROCESSABLE_ENTITY" },
  ];
  for (const err of definitive4xxErrors) {
    assert.equal(
      isDefinitiveAdminMoneyError(err),
      true,
      `expected definitive 4xx for ${JSON.stringify(err)}`
    );
    assert.equal(nextAdminMoneyIdempotencyCacheAfterError(changed.cache, err), null);
  }

  // After definitive 4xx cleared cache (null), retrying same payload mints new key #3
  const after4xxCache = nextAdminMoneyIdempotencyCacheAfterError(changed.cache, {
    status: 400,
    code: "INVALID_ARGUMENT",
  });
  const after4xx = resolveAdminMoneyIdempotencyKey({
    prefix: "refund",
    fingerprint: fp2,
    cache: after4xxCache,
    uuidFn,
  });
  assert.equal(after4xx.key, "refund-uuid-3");

  // Fingerprint key order does not matter (canonical)
  const fpOrderA = buildAdminMoneyFingerprint("settle", {
    refund_id: "rf-1",
    provider_refund_id: "FT01",
    note: "ok",
  });
  const fpOrderB = buildAdminMoneyFingerprint("settle", {
    note: "ok",
    refund_id: "rf-1",
    provider_refund_id: "FT01",
  });
  assert.equal(fpOrderA, fpOrderB);
});

test("AdminReconciliationClient pins Idempotency-Key for refund, queue resolve, and settle (no generateIdempotencyKey per click)", () => {
  const clientSrc = readFileSync(
    resolve(WEB_SRC, "features/admin/AdminReconciliationClient.tsx"),
    "utf8"
  );
  assert.equal(
    clientSrc.includes("generateIdempotencyKey"),
    false,
    "must not mint a fresh random Idempotency-Key on every button click"
  );
  const resolveCalls = clientSrc.match(/resolveAdminMoneyIdempotencyKey\(/g) ?? [];
  assert.equal(
    resolveCalls.length,
    3,
    "must pin Idempotency-Key in all 3 admin money operations (queue resolve, settle refund, create manual refund)"
  );
  const errClears = clientSrc.match(/nextAdminMoneyIdempotencyCacheAfterError\(/g) ?? [];
  assert.equal(
    errClears.length,
    3,
    "must retain/clear Idempotency-Key cache according to error definitiveness in all 3 operations"
  );
});

test("buildCodCarrierOptions fallback includes ghn, ghtk, and mock_express for accountant without carriers.read", () => {
  assert.deepEqual(DEFAULT_COD_FALLBACK_CARRIER_CODES, ["ghn", "ghtk", "mock_express"]);

  const fallback = buildCodCarrierOptions([]);
  assert.deepEqual(
    fallback.map((c) => c.code),
    ["ghn", "ghtk", "mock_express"]
  );
  assert.equal(fallback.find((c) => c.code === "ghtk")?.label, "Giao Hàng Tiết Kiệm (GHTK)");
  assert.equal(fallback.find((c) => c.code === "ghn")?.label, "Giao Hàng Nhanh (GHN)");
  assert.equal(fallback.find((c) => c.code === "mock_express")?.label, "Chuyển phát Nexus");

  // Real gateway JSON shape from GET /api/v1/admin/carriers
  const fromApi = buildCodCarrierOptions([
    { carrier_code: "ghn", name: "Giao Hang Nhanh (GHN)" },
    { carrier_code: "ghtk", name: "Giao Hang Tiet Kiem (GHTK)" },
    { carrier_code: "mock_express", name: "Mock Express" },
  ]);
  assert.deepEqual(
    fromApi.map((c) => c.code),
    ["ghn", "ghtk", "mock_express"]
  );

  const clientSrc = readFileSync(
    resolve(WEB_SRC, "features/admin/AdminReconciliationClient.tsx"),
    "utf8"
  );
  assert.ok(clientSrc.includes("buildCodCarrierOptions(carriers)"));
});

test("getOptionalSalesChannelMeta keeps empty channel unattributed for provider-only reconciliation items", () => {
  assert.equal(getOptionalSalesChannelMeta(""), null);
  assert.equal(getOptionalSalesChannelMeta("   "), null);
  assert.equal(getOptionalSalesChannelMeta(undefined), null);
  assert.equal(getOptionalSalesChannelMeta("STOREFRONT")?.label, "Site riêng");
  assert.equal(getOptionalSalesChannelMeta("MARKETPLACE")?.label, "Sàn NexusMart");

  const clientSrc = readFileSync(
    resolve(WEB_SRC, "features/admin/AdminReconciliationClient.tsx"),
    "utf8"
  );
  assert.ok(clientSrc.includes("getOptionalSalesChannelMeta("));
  assert.equal(clientSrc.includes("getSalesChannelMeta("), false);
});
