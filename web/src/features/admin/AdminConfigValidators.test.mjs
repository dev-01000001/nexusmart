// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminConfigValidators.test.mjs
import { describe, test } from "node:test";
import assert from "node:assert/strict";
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
  validateShopBankReviewInput,
  validateUpdateUserRoleInput,
  validateUpdateUserStatusInput,
} from "./AdminConfigValidators.ts";

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
