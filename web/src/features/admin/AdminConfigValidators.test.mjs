// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminConfigValidators.test.mjs
import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  parseCarrierCode,
  parseEntityId,
  parseOptionalPositiveInt,
  parsePaymentMethodCode,
  validateAdminCategoryPayload,
  validateAdminShopReviewInput,
  validateCarrierUpdateInput,
  validateCategoryFeeScheduleInput,
  validateCreateStaffInput,
  validatePaymentFeeConfigInput,
  validatePaymentMethodConfigInput,
  validateSearchReindexInput,
  validateShopBankReviewInput,
  validateUpdateUserRoleInput,
  validateUpdateUserStatusInput,
} from "./AdminConfigValidators.ts";
import { getSearchReindexStatusMeta } from "./adminApiTypes.ts";
import { validateRescanBannedKeywordsInput } from "./bannedKeywordsHelper.ts";
import { sanitizeGatewayError } from "../../lib/server/error-map.ts";

const WEB_SRC = resolve(fileURLToPath(new URL("../..", import.meta.url)));

describe("AdminConfigValidators — path ID & enum guards", () => {
  test("parseEntityId accepts UUIDs and safe IDs, rejects path traversal and injection", () => {
    assert.equal(
      parseEntityId("12345678-1234-4123-8123-123456789abc"),
      "12345678-1234-4123-8123-123456789abc"
    );
    assert.equal(parseEntityId("shop_01-abc"), "shop_01-abc");
    for (const bad of [
      "",
      "   ",
      "../etc",
      "a/b",
      "id?x=1",
      "id#frag",
      "id%20x",
      "-leading-dash",
      "a".repeat(65),
      null,
      123,
    ]) {
      assert.equal(parseEntityId(bad), null, `expected null for ${String(bad)}`);
    }
  });

  test("parseCarrierCode and parsePaymentMethodCode enforce allowlists", () => {
    assert.equal(parseCarrierCode("GHN"), "ghn");
    assert.equal(parseCarrierCode("mock_express"), "mock_express");
    assert.equal(parseCarrierCode("../ghn"), null);
    assert.equal(parseCarrierCode("a"), null);

    assert.equal(parsePaymentMethodCode("cod"), "COD");
    assert.equal(parsePaymentMethodCode("VIETQR"), "VIETQR");
    assert.equal(parsePaymentMethodCode("ZaloPay"), "ZALOPAY");
    assert.equal(parsePaymentMethodCode("CREDIT_CARD"), null);
  });

  test("parseOptionalPositiveInt validates numeric bounds", () => {
    assert.equal(parseOptionalPositiveInt("1"), "1");
    assert.equal(parseOptionalPositiveInt("100"), "100");
    assert.equal(parseOptionalPositiveInt("0"), undefined);
    assert.equal(parseOptionalPositiveInt("-5"), undefined);
    assert.equal(parseOptionalPositiveInt("1.5"), undefined);
    assert.equal(parseOptionalPositiveInt("999", 500), undefined);
    assert.equal(parseOptionalPositiveInt(null), undefined);
  });
});

describe("AdminConfigValidators — users & staff governance", () => {
  test("validateCreateStaffInput rejects password fields and forbids super_admin/buyer/seller", () => {
    const ok = validateCreateStaffInput({
      full_name: "Trần Thị Minh",
      email: "MINH@nexusmart.vn",
      phone: "0912345678",
      role: "ops",
    });
    assert.equal(ok.ok, true);
    if (ok.ok) {
      assert.deepEqual(ok.value, {
        full_name: "Trần Thị Minh",
        email: "minh@nexusmart.vn",
        phone: "0912345678",
        role: "ops",
      });
    }

    const withPw = validateCreateStaffInput({
      full_name: "Trần Thị Minh",
      email: "minh@nexusmart.vn",
      role: "ops",
      password: "secret",
    });
    assert.equal(withPw.ok, false);
    if (!withPw.ok) assert.equal(withPw.code, "PASSWORD_NOT_ACCEPTED");

    const withTempPw = validateCreateStaffInput({
      full_name: "Trần Thị Minh",
      email: "minh@nexusmart.vn",
      role: "ops",
      temporary_password: "secret",
    });
    assert.equal(withTempPw.ok, false);
    if (!withTempPw.ok) assert.equal(withTempPw.code, "PASSWORD_NOT_ACCEPTED");

    for (const forbiddenRole of ["super_admin", "buyer", "seller", "unknown"]) {
      const res = validateCreateStaffInput({
        full_name: "Trần Thị Minh",
        email: "minh@nexusmart.vn",
        role: forbiddenRole,
      });
      assert.equal(res.ok, false, `role ${forbiddenRole} must be rejected`);
    }
  });

  test("validateUpdateUserRoleInput allows 7 assignable roles and rejects super_admin", () => {
    for (const role of ["buyer", "seller", "moderator", "cskh", "accountant", "ops", "admin"]) {
      const res = validateUpdateUserRoleInput({ role, reason: "Phân công mới" });
      assert.equal(res.ok, true, `role ${role} should be assignable`);
    }
    const superAdmin = validateUpdateUserRoleInput({ role: "super_admin", reason: "test" });
    assert.equal(superAdmin.ok, false);
  });

  test("validateUpdateUserStatusInput accepts ACTIVE and LOCKED, rejects SUSPENDED", () => {
    assert.equal(validateUpdateUserStatusInput({ status: "ACTIVE", reason: "ok" }).ok, true);
    assert.equal(validateUpdateUserStatusInput({ status: "LOCKED", reason: "vi phạm" }).ok, true);
    const suspended = validateUpdateUserStatusInput({ status: "SUSPENDED", reason: "test" });
    assert.equal(suspended.ok, false);
    if (!suspended.ok) assert.equal(suspended.code, "INVALID_USER_STATUS");
  });
});

describe("AdminConfigValidators — shop review, bank review, config & fees", () => {
  test("validateAdminShopReviewInput requires expected_owner_name on APPROVE and reason on REJECT/REQUEST_INFO", () => {
    const missingOwner = validateAdminShopReviewInput({
      decision: "APPROVE",
      reason: "Đủ hồ sơ",
      expected_owner_name: "",
    });
    assert.equal(missingOwner.ok, false);
    if (!missingOwner.ok) assert.equal(missingOwner.code, "EXPECTED_OWNER_NAME_REQUIRED");

    const okApprove = validateAdminShopReviewInput({
      decision: "APPROVE",
      reason: "",
      expected_owner_name: "Nguyễn Văn A",
    });
    assert.equal(okApprove.ok, true);
    if (okApprove.ok) assert.equal(okApprove.value.expected_owner_name, "Nguyễn Văn A");

    const rejectNoReason = validateAdminShopReviewInput({ decision: "REJECT", reason: "   " });
    assert.equal(rejectNoReason.ok, false);
    if (!rejectNoReason.ok) assert.equal(rejectNoReason.code, "REASON_REQUIRED");

    const reqInfoOk = validateAdminShopReviewInput({
      decision: "REQUEST_INFO",
      reason: "Cần bổ sung GPKD rõ nét",
      expected_owner_name: "ignored",
    });
    assert.equal(reqInfoOk.ok, true);
    if (reqInfoOk.ok) assert.equal(reqInfoOk.value.expected_owner_name, "");
  });

  test("validateShopBankReviewInput requires valid bank_account_id and reason on REJECT", () => {
    assert.equal(
      validateShopBankReviewInput({ bank_account_id: "../bad", decision: "APPROVE", reason: "" })
        .ok,
      false
    );
    assert.equal(
      validateShopBankReviewInput({ bank_account_id: "bank-1", decision: "REJECT", reason: "ab" })
        .ok,
      false
    );
    assert.equal(
      validateShopBankReviewInput({ bank_account_id: "bank-1", decision: "APPROVE", reason: "" })
        .ok,
      true
    );
  });

  test("validatePaymentMethodConfigInput enforces integer VND, bounds, and expected_version", () => {
    const ok = validatePaymentMethodConfigInput({
      enabled: true,
      min_order_vnd: 0,
      max_order_vnd: 5_000_000,
      expiry_minutes: 15,
      note: "Cập nhật hạn mức",
      expected_version: 2,
    });
    assert.equal(ok.ok, true);

    const floatVnd = validatePaymentMethodConfigInput({
      enabled: true,
      min_order_vnd: 100.5,
      max_order_vnd: 500000,
      expiry_minutes: 15,
      expected_version: 1,
    });
    assert.equal(floatVnd.ok, false);

    const badRange = validatePaymentMethodConfigInput({
      enabled: true,
      min_order_vnd: 500_000,
      max_order_vnd: 100_000,
      expiry_minutes: 15,
      expected_version: 1,
    });
    assert.equal(badRange.ok, false);

    const missingVer = validatePaymentMethodConfigInput({
      enabled: true,
      min_order_vnd: 0,
      max_order_vnd: 0,
      expiry_minutes: 15,
    });
    assert.equal(missingVer.ok, false);
  });

  test("validateAdminCategoryPayload supports is_prohibited and rejects bad bps or non-boolean", () => {
    const ok = validateAdminCategoryPayload({
      name: "Vũ khí thô sơ",
      slug: "vu-khi-tho-so",
      parent_id: "",
      commission_rate_bps: 500,
      sort_order: 1,
      is_prohibited: true,
    });
    assert.equal(ok.ok, true);
    if (ok.ok) assert.equal(ok.value.is_prohibited, true);

    const badFlag = validateAdminCategoryPayload({
      name: "Danh mục",
      slug: "danh-muc",
      commission_rate_bps: 500,
      is_prohibited: "yes",
    });
    assert.equal(badFlag.ok, false);
  });

  test("validateCategoryFeeScheduleInput, validatePaymentFeeConfigInput, and validateCarrierUpdateInput enforce bounds", () => {
    assert.equal(
      validateCategoryFeeScheduleInput({
        commission_rate_bps: 600,
        effective_from: "2026-12-01T00:00:00Z",
        note: "Q4",
      }).ok,
      true
    );
    assert.equal(
      validateCategoryFeeScheduleInput({
        commission_rate_bps: 5001,
        effective_from: "2026-12-01T00:00:00Z",
      }).ok,
      false
    );

    assert.equal(
      validatePaymentFeeConfigInput({
        payment_method: "ZALOPAY",
        fee_bps: 150,
        effective_from: "2026-12-01T00:00:00Z",
      }).ok,
      true
    );
    assert.equal(
      validatePaymentFeeConfigInput({
        payment_method: "ZALOPAY",
        fee_bps: 2001,
        effective_from: "2026-12-01T00:00:00Z",
      }).ok,
      false
    );

    const carrierOk = validateCarrierUpdateInput({
      enabled: true,
      api_token: "  tok_live_123  ",
      fallback_rates: [
        {
          region: "SAME_PROVINCE",
          base_fee_vnd: 22000,
          base_weight_grams: 1000,
          extra_fee_vnd_per_500g: 3000,
        },
      ],
    });
    assert.equal(carrierOk.ok, true);
    if (carrierOk.ok) assert.equal(carrierOk.value.api_token, "tok_live_123");

    const badWeight = validateCarrierUpdateInput({
      enabled: true,
      fallback_rates: [
        {
          region: "SAME_PROVINCE",
          base_fee_vnd: 22000,
          base_weight_grams: 0,
          extra_fee_vnd_per_500g: 3000,
        },
      ],
    });
    assert.equal(badWeight.ok, false);
  });
});

describe("AdminConfigValidators — banned-keywords rescan & search reindex", () => {
  test("validateRescanBannedKeywordsInput requires boolean dry_run and route does not use Boolean(raw.dry_run)", () => {
    assert.deepEqual(validateRescanBannedKeywordsInput({ dry_run: true, limit: 100 }), {
      ok: true,
      value: { keyword_ids: [], dry_run: true, cursor: "", limit: 100 },
    });
    assert.deepEqual(validateRescanBannedKeywordsInput({ dry_run: false, keyword_ids: ["kw-1"] }), {
      ok: true,
      value: { keyword_ids: ["kw-1"], dry_run: false, cursor: "", limit: 500 },
    });

    for (const bad of [
      {},
      { dry_run: undefined },
      { dry_run: null },
      { dry_run: "true" },
      { dry_run: "false" },
      { dry_run: 0 },
      { dry_run: 1 },
    ]) {
      const res = validateRescanBannedKeywordsInput(bad);
      assert.equal(res.ok, false, `expected rejection for ${JSON.stringify(bad)}`);
      if (!res.ok) assert.equal(res.code, "INVALID_ARGUMENT");
    }

    const rescanRouteSrc = readFileSync(
      resolve(WEB_SRC, "app/api/admin/banned-keywords/rescan/route.ts"),
      "utf8"
    );
    assert.equal(
      rescanRouteSrc.includes("Boolean(raw.dry_run)"),
      false,
      "rescan route must not coerce raw.dry_run with Boolean()"
    );
    const validatePos = rescanRouteSrc.indexOf("validateRescanBannedKeywordsInput");
    const gwPos = rescanRouteSrc.indexOf("await gwRaw(");
    assert.ok(validatePos > 0 && gwPos > validatePos);
  });

  test("validateSearchReindexInput validates optional booleans and batch_size", () => {
    assert.deepEqual(validateSearchReindexInput(undefined), { ok: true, value: {} });
    assert.deepEqual(validateSearchReindexInput({}), { ok: true, value: {} });
    assert.deepEqual(
      validateSearchReindexInput({
        reindex_products: true,
        reindex_embeddings: false,
        batch_size: 200,
      }),
      {
        ok: true,
        value: { reindex_products: true, reindex_embeddings: false, batch_size: 200 },
      }
    );

    for (const bad of [
      null,
      [],
      "invalid",
      { reindex_products: "true" },
      { reindex_shops: 1 },
      { batch_size: 0 },
      { batch_size: -5 },
      { batch_size: 501 },
      { batch_size: 12.5 },
      { batch_size: "200" },
    ]) {
      const res = validateSearchReindexInput(bad);
      assert.equal(res.ok, false, `expected rejection for ${JSON.stringify(bad)}`);
      if (!res.ok) assert.equal(res.code, "INVALID_ARGUMENT");
    }
  });

  test("search reindex BFF routes, status meta, error map, and /admin/cau-hinh capability gating", () => {
    assert.equal(getSearchReindexStatusMeta("RUNNING").state, "RUNNING");
    assert.equal(getSearchReindexStatusMeta("COMPLETED").state, "COMPLETED");
    assert.equal(getSearchReindexStatusMeta("FAILED").state, "FAILED");

    const conflictErr = sanitizeGatewayError(409, {
      error: { code: "FAILED_PRECONDITION", message: "REINDEX_ALREADY_RUNNING: busy" },
    });
    assert.equal(conflictErr.code, "REINDEX_ALREADY_RUNNING");
    assert.match(conflictErr.message, /dựng lại chỉ mục/i);

    const postRoutePath = resolve(WEB_SRC, "app/api/admin/search/reindex/route.ts");
    const getRoutePath = resolve(WEB_SRC, "app/api/admin/search/reindex/[jobId]/route.ts");
    assert.equal(existsSync(postRoutePath), true, "POST /api/admin/search/reindex must exist");
    assert.equal(
      existsSync(getRoutePath),
      true,
      "GET /api/admin/search/reindex/[jobId] must exist"
    );

    const postSrc = readFileSync(postRoutePath, "utf8");
    assert.ok(
      postSrc.includes("apiRoute("),
      "POST reindex BFF must wrap handler in apiRoute (CSRF)"
    );
    assert.ok(postSrc.includes('assertCapability("search.reindex")'));
    assert.ok(postSrc.includes("/api/v1/admin/search/reindex"));

    const getSrc = readFileSync(getRoutePath, "utf8");
    assert.ok(getSrc.includes("apiRoute<"), "GET reindex status BFF must wrap handler in apiRoute");
    assert.ok(getSrc.includes('assertCapability("search.reindex")'));
    assert.ok(getSrc.includes("parseEntityId(jobId)"));
    assert.ok(getSrc.includes("/api/v1/admin/search/reindex/"));

    const configClientSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminConfigClient.tsx"),
      "utf8"
    );
    const reindexBlockSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminSearchReindexBlock.tsx"),
      "utf8"
    );
    assert.ok(configClientSrc.includes('roleHasCapability(role, "search.reindex")'));
    assert.ok(configClientSrc.includes("AdminSearchReindexBlock"));
    assert.ok(reindexBlockSrc.includes('useAdminCapability("search.reindex")'));
    assert.ok(
      reindexBlockSrc.includes("ConfirmActionDialog") || reindexBlockSrc.includes("AlertDialog"),
      "AdminSearchReindexBlock must require confirmation dialog before triggering reindex"
    );
    assert.ok(reindexBlockSrc.includes("Dựng lại chỉ mục"));
  });
});
