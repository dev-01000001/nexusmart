/**
 * Wave 3b-2 web behavioral & contract verification tests (A6 ZaloPay + A7 Internal Staff Governance).
 * Enforces F1–F15 from .agents/briefs/fix/w3b-plan.md §4 and contracts in docs/contracts.md + pkg/common/auth/capabilities.json.
 */
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(WEB_ROOT, "..");
const WEB_SRC = resolve(WEB_ROOT, "src");

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith("@/")) {
      const rel = specifier.slice(2);
      const tsPath = resolve(WEB_SRC, `${rel}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(WEB_SRC, rel, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const tsPath = resolve(parentDir, `${specifier}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(parentDir, specifier, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    return nextResolve(specifier, context);
  },
});

function readSrc(relPath) {
  return readFileSync(resolve(WEB_SRC, relPath), "utf8");
}

describe("Wave 3b-2 web — A6 ZaloPay & Payment Method Config", () => {
  test("1. [A6/F8] validateSafePayUrl strictly allows https://(*.zalopay.vn) or same-origin /thanh-toan/ mock paths", async () => {
    const payUrlMod = await import("../src/lib/payment-url.ts");

    // Allowed ZaloPay HTTPS hosts
    for (const url of [
      "https://zalopay.vn/pay?token=abc",
      "https://sb-openapi.zalopay.vn/v2/create?order=123",
      "https://qcgateway.zalopay.vn/openinapp?order=eyJ0eXAiOiJKV1QifQ",
      "https://sbgateway.zalopay.vn/pay?zp_trans_token=zpt_123",
      "https://open.zalopay.vn/pay/123",
    ]) {
      const res = payUrlMod.validateSafePayUrl(url, "http://localhost:3000");
      assert.equal(res.safe, true, `expected allowed ZaloPay URL: ${url}`);
      assert.equal(res.isExternal, true);
      assert.ok(res.href && res.href.startsWith("https://"));
    }

    // Allowed same-origin mock URL (relative or matching origin under /thanh-toan/)
    const relMock = payUrlMod.validateSafePayUrl(
      "/thanh-toan/mock-zalopay?app_trans_id=261124_abc&amount=250000",
      "http://localhost:3000"
    );
    assert.equal(relMock.safe, true);
    assert.equal(relMock.isExternal, false);
    assert.equal(relMock.href, "/thanh-toan/mock-zalopay?app_trans_id=261124_abc&amount=250000");

    const absMock = payUrlMod.validateSafePayUrl(
      "http://localhost:3000/thanh-toan/mock-zalopay?app_trans_id=261124_abc&amount=250000",
      "http://localhost:3000"
    );
    assert.equal(absMock.safe, true);
    assert.equal(absMock.isExternal, false);
    assert.equal(absMock.href, "/thanh-toan/mock-zalopay?app_trans_id=261124_abc&amount=250000");

    // Rejected vectors (XSS, open redirect, host spoofing, non-HTTPS external, userinfo, non-checkout internal path)
    const malicious = [
      "",
      "   ",
      null,
      undefined,
      "javascript:alert(1)",
      "JAVASCRIPT:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "vbscript:msgbox(1)",
      "http://sb-openapi.zalopay.vn/pay",
      "http://zalopay.vn/pay",
      "https://evilzalopay.vn/pay",
      "https://zalopay.vn.evil.com/pay",
      "https://evil.com/zalopay.vn",
      "https://user:pass@zalopay.vn/pay",
      "https://zalopay.vn@evil.com/pay",
      "//evil.com/pay",
      "//zalopay.vn/pay",
      "/admin/cau-hinh",
      "/api/auth/logout",
      "/thanh-toan/../../admin/cau-hinh",
      "/thanh-toan/mock-zalopay\r\nSet-Cookie: x=1",
      "http://evil.local:3000/thanh-toan/mock-zalopay?amount=1000",
      "https://sbgateway.zalopay.vn:8443/pay",
    ];
    for (const bad of malicious) {
      const res = payUrlMod.validateSafePayUrl(bad, "http://localhost:3000");
      assert.equal(res.safe, false, `must reject unsafe pay_url: ${JSON.stringify(bad)}`);
      assert.equal(res.href, null);
    }
  });

  test("2. [A6/F6] Dynamic payment-methods checkout evaluation, online payment helpers, refund statuses, and error-map", async () => {
    const checkoutHelpers = await import("../src/features/checkout/helpers.ts");
    const checkoutMsgs = await import("../src/features/checkout/messages.ts");
    const orderMeta = await import("../src/features/account/orderMeta.ts");
    const formatMod = await import("../src/lib/format.ts");
    const errMap = await import("../src/lib/server/error-map.ts");

    // Label for ZALOPAY
    assert.equal(formatMod.GATEWAY_PAYMENT_METHOD_LABELS.ZALOPAY, "Ví điện tử ZaloPay");

    // Dynamic limits from GET /payment-methods (no hardcoded 5M cap when max_order_vnd === 0)
    const methods = [
      {
        method: "COD",
        enabled: true,
        min_order_vnd: 50000,
        max_order_vnd: 2000000,
        expiry_minutes: 0,
        version: 2,
      },
      {
        method: "VIETQR",
        enabled: false,
        min_order_vnd: 10000,
        max_order_vnd: 0,
        expiry_minutes: 15,
        version: 1,
      },
      {
        method: "ZALOPAY",
        enabled: true,
        min_order_vnd: 10000,
        max_order_vnd: 0,
        expiry_minutes: 15,
        version: 1,
      },
    ];

    const codOver = checkoutHelpers.evaluatePaymentMethodForCheckout(methods[0], {
      totalPayableVnd: 2500000,
      hasFlashSaleItem: false,
      codRejectedCount: 0,
      phoneVerified: true,
      codAreaSupported: true,
    });
    assert.equal(codOver.allowed, false);
    assert.equal(codOver.blockCode, "AMOUNT_EXCEEDED");

    const codUnder = checkoutHelpers.evaluatePaymentMethodForCheckout(methods[0], {
      totalPayableVnd: 30000,
      hasFlashSaleItem: false,
      codRejectedCount: 0,
      phoneVerified: true,
      codAreaSupported: true,
    });
    assert.equal(codUnder.allowed, false);
    assert.equal(codUnder.blockCode, "AMOUNT_BELOW_MIN");

    // COD unlimited when max_order_vnd === 0
    const codUnlimited = checkoutHelpers.evaluatePaymentMethodForCheckout(
      { ...methods[0], max_order_vnd: 0 },
      {
        totalPayableVnd: 25000000,
        hasFlashSaleItem: false,
        codRejectedCount: 0,
        phoneVerified: true,
        codAreaSupported: true,
      }
    );
    assert.equal(codUnlimited.allowed, true);

    // Disabled VIETQR blocked
    const vietqrDisabled = checkoutHelpers.evaluatePaymentMethodForCheckout(methods[1], {
      totalPayableVnd: 200000,
      hasFlashSaleItem: false,
      codRejectedCount: 0,
    });
    assert.equal(vietqrDisabled.allowed, false);
    assert.equal(vietqrDisabled.blockCode, "METHOD_DISABLED");

    // Flash sale allows ZALOPAY while blocking COD
    const zpFlash = checkoutHelpers.evaluatePaymentMethodForCheckout(methods[2], {
      totalPayableVnd: 200000,
      hasFlashSaleItem: true,
      codRejectedCount: 0,
    });
    assert.equal(zpFlash.allowed, true);

    // Default method resolution skips disabled VIETQR and picks ZALOPAY for flash sale
    assert.equal(
      checkoutHelpers.resolveDefaultCheckoutMethod(methods, { hasFlashSaleItem: true }),
      "ZALOPAY"
    );
    assert.equal(
      checkoutHelpers.resolveDefaultCheckoutMethod(methods, {
        hasFlashSaleItem: false,
        currentMethod: "VIETQR",
      }),
      "COD"
    );

    // Online payment helpers recognize BOTH VIETQR and ZALOPAY
    const nowMs = Date.parse("2026-11-24T12:00:00Z");
    const unpaidZalo = {
      status: "PENDING",
      payment_method: "ZALOPAY",
      payment_id: "pay-zp-1",
      payment_status: "PENDING",
      payment_expires_at: "2026-11-24T12:15:00Z",
    };
    assert.equal(orderMeta.isOnlinePaymentMethod("ZALOPAY"), true);
    assert.equal(orderMeta.isOnlinePaymentMethod("VIETQR"), true);
    assert.equal(orderMeta.isOnlinePaymentMethod("COD"), false);
    assert.equal(orderMeta.isUnpaidVietQrOrder(unpaidZalo), true);
    assert.equal(orderMeta.needsVietQrPayment(unpaidZalo, nowMs), true);
    assert.equal(
      orderMeta.needsVietQrPayment(
        { ...unpaidZalo, payment_expires_at: "2026-11-24T11:59:00Z" },
        nowMs
      ),
      false
    );

    // Refund statuses (F4/F11)
    assert.equal(orderMeta.getRefundStatusInfo("PENDING_APPROVAL").tone, "warn");
    assert.equal(orderMeta.getRefundStatusInfo("REJECTED").tone, "neg");
    assert.equal(orderMeta.getRefundStatusInfo("MANUAL_REVIEW").tone, "warn");

    // Error-map translations for A6 + A7 codes
    const shopSusp = errMap.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "SHOP_SUSPENDED: shop is currently suspended",
      },
    });
    assert.equal(shopSusp.code, "SHOP_SUSPENDED");
    assert.ok(shopSusp.message.includes("tạm ngưng"), `got: ${shopSusp.message}`);

    const pmDisabled = errMap.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "PAYMENT_METHOD_DISABLED: ZALOPAY is disabled",
      },
    });
    assert.equal(pmDisabled.code, "PAYMENT_METHOD_DISABLED");
    assert.ok(
      pmDisabled.message.includes("tạm tắt") || pmDisabled.message.includes("không khả dụng")
    );

    const outOfRange = errMap.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "ORDER_AMOUNT_OUT_OF_RANGE: exceeds max_order_vnd",
      },
    });
    assert.equal(outOfRange.code, "ORDER_AMOUNT_OUT_OF_RANGE");
    assert.ok(outOfRange.message.includes("hạn mức"));

    const versionConflict = errMap.sanitizeGatewayError(409, {
      error: { code: "VERSION_CONFLICT", message: "VERSION_CONFLICT: expected version 1 got 2" },
    });
    assert.equal(versionConflict.code, "VERSION_CONFLICT");
    assert.ok(versionConflict.message.includes("tải lại"));

    const makerChecker = errMap.sanitizeGatewayError(403, {
      error: {
        code: "MAKER_CHECKER_CONFLICT",
        message: "MAKER_CHECKER_CONFLICT: cannot approve own request",
      },
    });
    assert.equal(makerChecker.code, "MAKER_CHECKER_CONFLICT");
    assert.ok(
      makerChecker.message.includes("2 người") || makerChecker.message.includes("tự phê duyệt")
    );

    // Dynamic checkout message translation
    const apiMod = await import("../src/lib/api.ts");
    const msgDisabled = checkoutMsgs.translateCheckoutError(
      new apiMod.ApiError(409, "PAYMENT_METHOD_DISABLED", "disabled")
    );
    assert.ok(msgDisabled.includes("tạm tắt") || msgDisabled.includes("phương thức"));
    const msgCodDynamic = checkoutMsgs.translateCheckoutError(
      new apiMod.ApiError(409, "COD_LIMIT_EXCEEDED", "exceeded"),
      { codMaxVnd: 2000000 }
    );
    assert.ok(
      msgCodDynamic.includes("2.000.000₫"),
      `expected dynamic limit 2.000.000₫, got: ${msgCodDynamic}`
    );
  });

  test("3. [A6/F6/F7/F8] Source invariants: no hardcoded DEFAULT_COD_MAX_VND, no Ví NexusPay, real admin/payment-methods, mock-zalopay route", () => {
    const checkoutPageSrc = readSrc("app/(shop)/thanh-toan/page.tsx");
    assert.ok(
      checkoutPageSrc.includes("/payment-methods"),
      "checkout page must fetch /payment-methods"
    );

    const checkoutClientSrc = readSrc("app/(shop)/thanh-toan/CheckoutClient.tsx");
    assert.equal(
      checkoutClientSrc.includes("DEFAULT_COD_MAX_VND"),
      false,
      "CheckoutClient must not hardcode DEFAULT_COD_MAX_VND (F6)"
    );
    assert.ok(checkoutClientSrc.includes("ZALOPAY"), "CheckoutClient must support ZALOPAY");
    assert.ok(
      checkoutClientSrc.includes("resolvePostCheckoutHref"),
      "CheckoutClient must route online vs 0 VND / COD checkout via resolvePostCheckoutHref"
    );

    const retryBoxSrc = readSrc("features/commerce/RetryPaymentBox.tsx");
    assert.ok(
      retryBoxSrc.includes("validateSafePayUrl"),
      "RetryPaymentBox must validate pay_url via F8 allowlist"
    );
    assert.ok(
      retryBoxSrc.includes("switched_to_cod"),
      "RetryPaymentBox must handle switched_to_cod"
    );
    assert.ok(retryBoxSrc.includes("Idempotency-Key"), "RetryPaymentBox must send Idempotency-Key");

    const cauHinhSrc = readSrc("app/(admin)/admin/cau-hinh/page.tsx");
    assert.ok(
      cauHinhSrc.includes("AdminPaymentMethodsPanel"),
      "cau-hinh must render AdminPaymentMethodsPanel"
    );
    assert.equal(
      cauHinhSrc.includes("Ví NexusPay"),
      false,
      "cau-hinh must remove Ví NexusPay mock"
    );
    assert.equal(
      cauHinhSrc.includes("sandboxWalletEnabled"),
      false,
      "cau-hinh must remove fake sandboxWalletEnabled"
    );
    assert.equal(
      cauHinhSrc.includes("Cổng thanh toán (mẫu)"),
      false,
      "tab thanh_toan is real, not mock"
    );

    const adminMetaSrc = readSrc("features/admin/adminMeta.ts");
    assert.equal(adminMetaSrc.includes("Ví NexusPay"), false, "adminMeta must remove Ví NexusPay");

    const pmPanelPath = resolve(WEB_SRC, "features/admin/AdminPaymentMethodsPanel.tsx");
    assert.equal(existsSync(pmPanelPath), true, "AdminPaymentMethodsPanel.tsx must exist");
    const pmPanelSrc = readFileSync(pmPanelPath, "utf8");
    assert.ok(pmPanelSrc.includes("admin/payment-methods"), "must call admin/payment-methods");
    assert.ok(
      pmPanelSrc.includes("expected_version"),
      "must send expected_version optimistic lock (F7)"
    );
    assert.ok(pmPanelSrc.includes("VERSION_CONFLICT"), "must handle 409 VERSION_CONFLICT");

    const feesSrc = readSrc("features/admin/AdminFeesPanel.tsx");
    assert.ok(feesSrc.includes("ZALOPAY"), "AdminFeesPanel must support ZALOPAY payment fee");

    assert.equal(
      existsSync(resolve(WEB_SRC, "app/(shop)/thanh-toan/mock-zalopay/page.tsx")),
      true,
      "same-origin mock ZaloPay simulator page must exist at /thanh-toan/mock-zalopay"
    );
  });
});

describe("Wave 3b-2 web — A7 Internal Staff Governance & RBAC", () => {
  test("4. [A7/F9/F9-bis/F10/F11] admin-rbac mirrors pkg/common/auth/capabilities.json 1:1, enforces RoleRank, route guards, and maker!=checker", async () => {
    const rbac = await import("../src/lib/admin-rbac.ts");
    const redirectMod = await import("../src/lib/redirect.ts");

    const canonical = JSON.parse(
      readFileSync(resolve(REPO_ROOT, "pkg/common/auth/capabilities.json"), "utf8")
    );

    // Exact 6 internal staff roles parity
    assert.deepEqual([...rbac.ADMIN_STAFF_ROLES].sort(), [...canonical.roles].sort());

    // Exact 1:1 capability matrix parity (including F9-bis coins.credit)
    const webCaps = Object.keys(rbac.CAPABILITY_MATRIX).sort();
    const goCaps = Object.keys(canonical.capabilities).sort();
    assert.deepEqual(
      webCaps,
      goCaps,
      "web CAPABILITY_MATRIX keys must match capabilities.json 1:1"
    );
    for (const cap of goCaps) {
      assert.deepEqual(
        [...rbac.CAPABILITY_MATRIX[cap]].sort(),
        [...canonical.capabilities[cap]].sort(),
        `capability ${cap} role list must match capabilities.json`
      );
    }
    // Explicit F9-bis assertion: coins.credit strictly super_admin, admin, accountant
    assert.deepEqual([...rbac.CAPABILITY_MATRIX["coins.credit"]].sort(), [
      "accountant",
      "admin",
      "super_admin",
    ]);

    // isAdminStaffRole
    for (const role of canonical.roles) {
      assert.equal(rbac.isAdminStaffRole(role), true);
      assert.equal(redirectMod.resolvePostLoginRedirect(role, null), "/admin");
    }
    for (const nonStaff of ["buyer", "seller", "", null, undefined]) {
      assert.equal(rbac.isAdminStaffRole(nonStaff), false);
    }

    // F10 RoleRank hierarchy: strictly greater required
    assert.equal(rbac.canActOnTargetRole("super_admin", "admin"), true);
    assert.equal(rbac.canActOnTargetRole("super_admin", "super_admin"), false);
    assert.equal(rbac.canActOnTargetRole("admin", "ops"), true);
    assert.equal(rbac.canActOnTargetRole("admin", "admin"), false);
    assert.equal(rbac.canActOnTargetRole("admin", "super_admin"), false);
    assert.equal(rbac.canActOnTargetRole("cskh", "buyer"), true);
    assert.equal(rbac.canActOnTargetRole("cskh", "seller"), true);
    assert.equal(rbac.canActOnTargetRole("cskh", "ops"), false);
    assert.equal(rbac.canActOnTargetRole("cskh", "cskh"), false);
    assert.equal(rbac.canActOnTargetRole("cskh", "admin"), false);
    assert.equal(rbac.canActOnTargetRole("ops", "buyer"), true);
    assert.equal(rbac.canActOnTargetRole("ops", "moderator"), false);

    // Route-level access guards (F15 zero 403)
    assert.equal(rbac.canAccessAdminPath("moderator", "/admin"), true);
    assert.equal(rbac.canAccessAdminPath("moderator", "/admin/gian-hang"), true);
    assert.equal(rbac.canAccessAdminPath("moderator", "/admin/kiem-duyet"), true);
    assert.equal(rbac.canAccessAdminPath("moderator", "/admin/audit-log"), true);
    assert.equal(rbac.canAccessAdminPath("moderator", "/admin/doi-soat"), false);
    assert.equal(rbac.canAccessAdminPath("moderator", "/admin/khieu-nai"), false);

    assert.equal(rbac.canAccessAdminPath("accountant", "/admin/doi-soat"), true);
    assert.equal(rbac.canAccessAdminPath("accountant", "/admin/kiem-duyet"), false);
    assert.equal(rbac.canAccessAdminPath("accountant", "/admin/noi-dung"), false);

    assert.equal(rbac.canAccessAdminPath("cskh", "/admin/khieu-nai"), true);
    assert.equal(rbac.canAccessAdminPath("cskh", "/admin/doi-soat"), true);
    assert.equal(rbac.canAccessAdminPath("cskh", "/admin/noi-dung"), false);

    assert.equal(rbac.canAccessAdminPath("buyer", "/admin"), false);
    assert.equal(rbac.canAccessAdminPath("seller", "/admin/gian-hang"), false);

    // F11 Maker != Checker helper
    assert.equal(
      rbac.canDecideTwoPersonApproval({
        actorId: "user-ops-1",
        actorRole: "ops",
        requestedBy: "user-ops-1",
        decideCapability: "shops.suspend.decide",
      }),
      false,
      "maker cannot self-approve"
    );
    assert.equal(
      rbac.canDecideTwoPersonApproval({
        actorId: "user-ops-2",
        actorRole: "ops",
        requestedBy: "user-ops-1",
        decideCapability: "shops.suspend.decide",
      }),
      true
    );
    assert.equal(
      rbac.canDecideTwoPersonApproval({
        actorId: "user-mod-1",
        actorRole: "moderator",
        requestedBy: "user-ops-1",
        decideCapability: "shops.suspend.decide",
      }),
      false,
      "moderator lacks shops.suspend.decide"
    );
  });

  test("5. [A7/F11/F13/F14/F15] Source invariants: 6-role layout, zero-403 gating, passwordless staff creation, activities, suspend/unsuspend, 2-person approvals", () => {
    const adminLayoutSrc = readSrc("app/(admin)/layout.tsx");
    assert.ok(
      adminLayoutSrc.includes("ADMIN_STAFF_ROLES"),
      "(admin)/layout.tsx must accept all 6 ADMIN_STAFF_ROLES"
    );

    const shellSrc = readSrc("app/(admin)/AdminShell.tsx");
    assert.ok(
      shellSrc.includes("canAccessAdminPath"),
      "AdminShell must guard disallowed routes before rendering children"
    );
    assert.ok(
      shellSrc.includes("roleHasCapability"),
      "AdminShell must filter nav & badge fetches by capability"
    );

    const usersPanelSrc = readSrc("features/admin/AdminUsersPanel.tsx");
    assert.ok(
      usersPanelSrc.includes("admin/staff"),
      "AdminUsersPanel must support POST admin/staff"
    );
    assert.equal(
      /type=["']password["']/.test(usersPanelSrc),
      false,
      "AdminUsersPanel staff creation form must NOT contain password input (F14)"
    );
    assert.ok(
      usersPanelSrc.includes("/activities"),
      "AdminUsersPanel must fetch user activities (F13)"
    );
    assert.ok(
      usersPanelSrc.includes("canActOnTargetRole"),
      "AdminUsersPanel must enforce F10 RoleRank"
    );
    assert.ok(
      usersPanelSrc.includes("staff.create"),
      "AdminUsersPanel must gate staff creation on staff.create"
    );

    const gianHangSrc = readSrc("app/(admin)/admin/gian-hang/page.tsx");
    assert.ok(gianHangSrc.includes("/suspend"), "gian-hang must support shop suspension request");
    assert.ok(gianHangSrc.includes("/unsuspend"), "gian-hang must support shop unsuspension");
    assert.ok(
      gianHangSrc.includes("shops.bank.read"),
      "gian-hang must gate bank-account calls on shops.bank.read"
    );

    const approvalsPanelPath = resolve(WEB_SRC, "features/admin/AdminApprovalsPanel.tsx");
    assert.equal(existsSync(approvalsPanelPath), true, "AdminApprovalsPanel.tsx must exist");
    const approvalsSrc = readFileSync(approvalsPanelPath, "utf8");
    assert.ok(
      approvalsSrc.includes("admin/approvals"),
      "AdminApprovalsPanel must fetch admin/approvals"
    );
    assert.ok(
      approvalsSrc.includes("admin/approvals/shop/"),
      "AdminApprovalsPanel must decide shop approvals"
    );
    assert.ok(
      approvalsSrc.includes("admin/approvals/refund/"),
      "AdminApprovalsPanel must decide refund approvals"
    );
    assert.ok(
      approvalsSrc.includes("canDecideTwoPersonApproval"),
      "AdminApprovalsPanel must enforce maker != checker"
    );

    const doiSoatSrc = readSrc("app/(admin)/admin/doi-soat/page.tsx");
    assert.ok(
      doiSoatSrc.includes("PENDING_APPROVAL"),
      "doi-soat must support PENDING_APPROVAL refund status"
    );
    assert.ok(
      doiSoatSrc.includes("MANUAL_REVIEW"),
      "doi-soat must support MANUAL_REVIEW refund status"
    );
    assert.ok(
      doiSoatSrc.includes("AdminApprovalsPanel"),
      "doi-soat must embed AdminApprovalsPanel"
    );
    assert.ok(
      doiSoatSrc.includes("carriers.read"),
      "doi-soat must gate admin/carriers call on carriers.read"
    );
    assert.ok(
      doiSoatSrc.includes("ledger.read"),
      "doi-soat must gate admin/ledger/summary call on ledger.read"
    );

    const adminOverviewSrc = readSrc("app/(admin)/admin/page.tsx");
    assert.ok(
      adminOverviewSrc.includes("reports.read"),
      "admin overview must gate admin/reports on reports.read"
    );
    assert.ok(
      adminOverviewSrc.includes("ledger.read"),
      "admin overview must gate admin/ledger/summary on ledger.read"
    );
    assert.ok(
      adminOverviewSrc.includes("products.read"),
      "admin overview must gate admin/products on products.read"
    );
    assert.ok(
      adminOverviewSrc.includes("orders.read"),
      "admin overview must gate admin/orders/needs-review on orders.read"
    );

    const promoCoinsSrc = readSrc("features/admin/AdminPromoCoinsPanel.tsx");
    assert.ok(
      promoCoinsSrc.includes("vouchers.read"),
      "AdminPromoCoinsPanel must gate vouchers on vouchers.read"
    );
    assert.ok(
      promoCoinsSrc.includes("flash_sales.read"),
      "AdminPromoCoinsPanel must gate flash sales on flash_sales.read"
    );
    assert.ok(
      promoCoinsSrc.includes("coins.credit"),
      "AdminPromoCoinsPanel must gate coin credit on coins.credit"
    );
  });

  test("6. [Review #1, #2, #3, #6] Approvals DTO parity (decided_note), status filters, persistent toolbar on reload, and default-deny canAccessAdminPath", async () => {
    const rbac = await import("../src/lib/admin-rbac.ts");

    // #1: Exact DTO parity with gateway shopSuspensionApprovalDTO & retryPaymentResponse
    const gwTypesSrc = readSrc("lib/gateway-types.ts");
    const approvalsSrc = readSrc("features/admin/AdminApprovalsPanel.tsx");
    assert.equal(
      gwTypesSrc.includes("decision_note"),
      false,
      "GatewayShopSuspensionApproval must use decided_note, not decision_note"
    );
    assert.equal(
      gwTypesSrc.includes("decided_role"),
      false,
      "GatewayShopSuspensionApproval must not declare nonexistent decided_role"
    );
    assert.ok(
      gwTypesSrc.includes("decided_note?: string"),
      "GatewayShopSuspensionApproval must declare decided_note?: string"
    );
    assert.ok(
      approvalsSrc.includes("item.decided_note"),
      "AdminApprovalsPanel must render item.decided_note"
    );
    assert.equal(
      approvalsSrc.includes("decision_note"),
      false,
      "AdminApprovalsPanel must not reference decision_note"
    );
    assert.equal(
      approvalsSrc.includes("decided_role"),
      false,
      "AdminApprovalsPanel must not reference decided_role"
    );

    // #2: No fake empty 'Tất cả trạng thái' filter; APPROVED refund section guides to refund tab
    assert.equal(
      approvalsSrc.includes("Tất cả trạng thái"),
      false,
      "AdminApprovalsPanel must not offer empty 'Tất cả trạng thái' option (gateway maps empty to PENDING)"
    );
    assert.ok(
      approvalsSrc.includes('statusFilter === "APPROVED"') &&
        approvalsSrc.includes("/admin/doi-soat"),
      "AdminApprovalsPanel must explain APPROVED refund lifecycle and guide to /admin/doi-soat"
    );

    // #3: Toolbar stays mounted while loading/reloading so keyboard focus is preserved
    const toolbarPos = approvalsSrc.indexOf("shared.toolbar");
    const loadingViewPos = approvalsSrc.indexOf('kind="loading"');
    assert.ok(
      toolbarPos > 0 && loadingViewPos > toolbarPos,
      "AdminApprovalsPanel must render filter toolbar before loading StateView"
    );

    // #6: canAccessAdminPath must default-deny unknown admin routes while allowing all 9 known pages
    const knownAdminRoutes = [
      "/admin",
      "/admin/gian-hang",
      "/admin/kiem-duyet",
      "/admin/khieu-nai",
      "/admin/doi-soat",
      "/admin/thanh-vien",
      "/admin/noi-dung",
      "/admin/cau-hinh",
      "/admin/audit-log",
    ];
    for (const route of knownAdminRoutes) {
      assert.equal(
        rbac.canAccessAdminPath("super_admin", route),
        true,
        `super_admin must be allowed on known route ${route}`
      );
    }
    for (const unknownRoute of [
      "/admin/unknown",
      "/admin/secret",
      "/admin/bao-cao-noi-bo",
      "/admin/settings",
      "/khong-phai-admin",
    ]) {
      assert.equal(
        rbac.canAccessAdminPath("super_admin", unknownRoute),
        false,
        `canAccessAdminPath must default-deny unknown route ${unknownRoute} even for super_admin`
      );
      assert.equal(
        rbac.canAccessAdminPath("moderator", unknownRoute),
        false,
        `canAccessAdminPath must default-deny unknown route ${unknownRoute} for moderator`
      );
    }
  });

  test("7. [Review #4, #5, #7] COD switch confirmation dialog + sync lock, neutral mock-zalopay, and isolated E2E shop", () => {
    // #4: RetryPaymentBox COD confirmation modal with amount & synchronous double-submit lock
    const retryBoxSrc = readSrc("features/commerce/RetryPaymentBox.tsx");
    assert.ok(
      retryBoxSrc.includes('role="dialog"') && retryBoxSrc.includes("confirmDialog"),
      "RetryPaymentBox must render an accessible confirmation dialog for irreversible COD switch"
    );
    assert.ok(
      retryBoxSrc.includes("codConfirmOpen") && retryBoxSrc.includes("retryingRef"),
      "RetryPaymentBox must gate COD switch with confirmation dialog and synchronous ref lock"
    );
    assert.ok(
      retryBoxSrc.includes("Số tiền thanh toán khi nhận hàng"),
      "COD switch confirmation dialog must explicitly state payable-on-delivery amount"
    );

    // #7: /thanh-toan/mock-zalopay must NOT reflect untrusted query amount/app_trans_id as confirmed info
    const mockZpSrc = readSrc("app/(shop)/thanh-toan/mock-zalopay/page.tsx");
    assert.equal(
      mockZpSrc.includes("searchParams"),
      false,
      "mock-zalopay page must not reflect untrusted query params (amount/app_trans_id) as confirmed payment info"
    );
    assert.ok(
      mockZpSrc.includes("Trang mô phỏng, không phải trang ZaloPay thật"),
      "mock-zalopay page must display neutral disclaimer: Trang mô phỏng, không phải trang ZaloPay thật"
    );

    // #5 & bonus 11_ retry DTO parity
    const e2e11Src = readFileSync(
      resolve(REPO_ROOT, "tests/e2e-web/11_w3b2_zalopay_config.test.mjs"),
      "utf8"
    );
    assert.ok(
      e2e11Src.includes(".payment?.payment_id"),
      "e2e 11_ must read payment.payment_id matching gateway paymentDTO"
    );

    const e2e12Src = readFileSync(
      resolve(REPO_ROOT, "tests/e2e-web/12_w3b2_admin_governance.test.mjs"),
      "utf8"
    );
    assert.ok(
      e2e12Src.includes("/api/gw/shops/register") && e2e12Src.includes("/review"),
      "e2e 12_ test 3 must register & approve its own isolated shop instead of suspending a seeded shop"
    );
  });

  test("8. [Review r2 #1–#4] Wallet refund UX (buyer & accountant), MANUAL_REVIEW bank/settle, password_reset_failed label, and SHOP_SUSPENDED promotion messages", async () => {
    const orderMeta = await import("../src/features/account/orderMeta.ts");
    const adminTypes = await import("../src/features/admin/types.ts");
    const checkoutMsgs = await import("../src/features/checkout/messages.ts");
    const errorMap = await import("../src/lib/server/error-map.ts");
    const apiMod = await import("../src/lib/api.ts");

    // #1: Buyer wallet refund (provider_m_refund_id non-empty) vs VietQR/COD vs MANUAL_REVIEW
    assert.equal(orderMeta.isWalletRefund({ provider_m_refund_id: "261124_2553_abc" }), true);
    assert.equal(orderMeta.isWalletRefund({ provider_m_refund_id: "" }), false);
    assert.equal(orderMeta.isWalletRefund({}), false);

    // PENDING wallet refund -> "Đang hoàn về ví ZaloPay", NO bank form
    const zpPending = orderMeta.getRefundStatusInfo("PENDING", {
      provider_m_refund_id: "261124_2553_abc",
    });
    assert.equal(zpPending.label, "Đang hoàn về ví ZaloPay");
    assert.equal(
      orderMeta.canSubmitBuyerRefundBankAccount({
        status: "PENDING",
        provider_m_refund_id: "261124_2553_abc",
      }),
      false,
      "wallet PENDING refund must NOT allow bank account submission"
    );

    // DONE wallet refund -> "Đã hoàn về ví ZaloPay"; DONE settled manually after MANUAL_REVIEW (has bank_code) -> bank transfer label
    const zpDoneAuto = orderMeta.getRefundStatusInfo("DONE", {
      provider_m_refund_id: "261124_2553_abc",
      bank_code: "",
    });
    assert.equal(zpDoneAuto.label, "Đã hoàn về ví ZaloPay");
    const zpDoneManual = orderMeta.getRefundStatusInfo("DONE", {
      provider_m_refund_id: "261124_2553_abc",
      bank_code: "VCB",
    });
    assert.equal(zpDoneManual.label, "Đã chuyển khoản hoàn tiền");

    // MANUAL_REVIEW (both wallet and non-wallet) -> opens bank account form
    assert.equal(
      orderMeta.canSubmitBuyerRefundBankAccount({
        status: "MANUAL_REVIEW",
        provider_m_refund_id: "261124_2553_abc",
      }),
      true,
      "wallet MANUAL_REVIEW refund must allow bank account submission"
    );
    assert.equal(
      orderMeta.canSubmitBuyerRefundBankAccount({
        status: "MANUAL_REVIEW",
        provider_m_refund_id: "",
      }),
      true,
      "non-wallet MANUAL_REVIEW refund must allow bank account submission"
    );

    // VietQR/COD PENDING unchanged
    const vqrPending = orderMeta.getRefundStatusInfo("PENDING", { provider_m_refund_id: "" });
    assert.equal(vqrPending.label, "Chờ chuyển khoản hoàn tiền");
    assert.equal(
      orderMeta.canSubmitBuyerRefundBankAccount({ status: "PENDING", provider_m_refund_id: "" }),
      true
    );

    const orderDetailSrc = readSrc("features/account/OrderDetailClient.tsx");
    assert.ok(
      orderDetailSrc.includes("canSubmitBuyerRefundBankAccount"),
      "OrderDetailClient must use canSubmitBuyerRefundBankAccount for bank form & change-bank button"
    );
    assert.ok(
      orderDetailSrc.includes("ví ZaloPay"),
      "OrderDetailClient must show ZaloPay-specific copy when refund is initializing or wallet-refunding"
    );

    // #2: Accountant manual settle rules (PENDING non-wallet OR MANUAL_REVIEW any method)
    assert.equal(
      adminTypes.isAdminRefundManuallySettleable({
        status: "PENDING",
        provider_m_refund_id: "261124_2553_abc",
      }),
      false,
      "accountant must NOT manually settle PENDING ZaloPay wallet refund"
    );
    assert.equal(
      adminTypes.isAdminWalletRefundAutoPending({
        status: "PENDING",
        provider_m_refund_id: "261124_2553_abc",
      }),
      true
    );
    assert.equal(
      adminTypes.isAdminRefundManuallySettleable({
        status: "PENDING",
        provider_m_refund_id: "",
      }),
      true
    );
    assert.equal(
      adminTypes.isAdminRefundManuallySettleable({
        status: "MANUAL_REVIEW",
        provider_m_refund_id: "261124_2553_abc",
      }),
      true,
      "accountant MUST be able to settle MANUAL_REVIEW wallet refund"
    );
    assert.equal(
      adminTypes.isAdminRefundManuallySettleable({
        status: "MANUAL_REVIEW",
        provider_m_refund_id: "",
      }),
      true
    );
    assert.equal(
      adminTypes.isAdminRefundManuallySettleable({
        status: "DONE",
        provider_m_refund_id: "",
      }),
      false
    );
    assert.equal(
      adminTypes.getAdminRefundStatusMeta("PENDING", {
        provider_m_refund_id: "261124_2553_abc",
      }).label,
      "ZaloPay đang hoàn tự động"
    );

    const doiSoatSrc = readSrc("app/(admin)/admin/doi-soat/page.tsx");
    assert.ok(
      doiSoatSrc.includes("isAdminRefundManuallySettleable") &&
        doiSoatSrc.includes("ZaloPay đang hoàn tự động"),
      "doi-soat must block manual settlement on PENDING wallet refund and show ZaloPay auto-refund status"
    );
    assert.ok(
      doiSoatSrc.includes("status=MANUAL_REVIEW"),
      "doi-soat must include MANUAL_REVIEW in badge count & filter visibility"
    );

    // #3: password_reset_failed activity label
    const usersPanelSrc = readSrc("features/admin/AdminUsersPanel.tsx");
    assert.equal(
      adminTypes.formatUserActivityEventType("password_reset_failed"),
      "Gửi thư đặt lại mật khẩu thất bại",
      "USER_ACTIVITY_EVENT_LABELS must map password_reset_failed to Vietnamese label"
    );
    assert.ok(
      usersPanelSrc.includes("formatUserActivityEventType"),
      "AdminUsersPanel must render activity events via formatUserActivityEventType"
    );

    // #4: SHOP_SUSPENDED clear Vietnamese messages (error-map, translateVoucherReason, formatPromotionErrorMessage)
    const sanSusp = errorMap.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "SHOP_SUSPENDED: shop is currently suspended",
      },
    });
    assert.equal(sanSusp.code, "SHOP_SUSPENDED");
    assert.equal(sanSusp.message.includes("SHOP_SUSPENDED"), false);
    assert.ok(sanSusp.message.includes("tạm ngưng"));

    const warnVoucher = checkoutMsgs.translateVoucherReason(
      "SHOP_SUSPENDED: shop for voucher SAVE10 is suspended"
    );
    assert.equal(warnVoucher.includes("SHOP_SUSPENDED"), false);
    assert.ok(warnVoucher.includes("tạm ngưng"));

    const warnFlash = checkoutMsgs.translateVoucherReason(
      "SHOP_SUSPENDED: shop for flash sale item fi-1 is suspended"
    );
    assert.equal(warnFlash.includes("SHOP_SUSPENDED"), false);
    assert.ok(warnFlash.includes("tạm ngưng") && warnFlash.includes("Flash sale"));

    const errRaw = new apiMod.ApiError(
      409,
      "SHOP_SUSPENDED",
      "SHOP_SUSPENDED: shop is currently suspended"
    );
    for (const action of [
      "save_voucher",
      "seller_voucher",
      "seller_discount_program",
      "seller_flash_sale",
    ]) {
      const msg = checkoutMsgs.formatPromotionErrorMessage(errRaw, action, "fallback");
      assert.equal(msg.includes("SHOP_SUSPENDED"), false, `raw code leaked for ${action}: ${msg}`);
      assert.ok(
        msg.includes("tạm ngưng"),
        `expected Vietnamese suspended message for ${action}: ${msg}`
      );
    }
  });

  test("9. [Review r3] Zero-VND checkout bypasses min_order_vnd, skips ZaloPay/VietQR waiting page, and lands on CONFIRMED order result", async () => {
    const checkoutHelpers = await import("../src/features/checkout/helpers.ts");
    const orderMeta = await import("../src/features/account/orderMeta.ts");

    // 0 VND order must NOT be blocked by min_order_vnd for ZALOPAY, VIETQR, or COD (matches backend payment_config.go:117)
    for (const method of ["ZALOPAY", "VIETQR", "COD"]) {
      const zeroEval = checkoutHelpers.evaluatePaymentMethodForCheckout(
        { method, enabled: true, min_order_vnd: 50000, max_order_vnd: 5000000 },
        {
          totalPayableVnd: 0,
          hasFlashSaleItem: false,
          codRejectedCount: 0,
          phoneVerified: true,
          codAreaSupported: true,
        }
      );
      assert.equal(
        zeroEval.allowed,
        true,
        `${method} with min_order_vnd=50000 must allow totalPayableVnd=0, got: ${zeroEval.reason}`
      );

      // Positive amount still strictly enforced against min_order_vnd
      const belowMinEval = checkoutHelpers.evaluatePaymentMethodForCheckout(
        { method, enabled: true, min_order_vnd: 50000, max_order_vnd: 5000000 },
        {
          totalPayableVnd: 10000,
          hasFlashSaleItem: false,
          codRejectedCount: 0,
          phoneVerified: true,
          codAreaSupported: true,
        }
      );
      assert.equal(belowMinEval.allowed, false);
      assert.equal(belowMinEval.blockCode, "AMOUNT_BELOW_MIN");
    }

    // Post-checkout routing: 0 VND with ZALOPAY/VIETQR (even with payment_id) must NOT open waiting page
    assert.equal(
      checkoutHelpers.resolvePostCheckoutHref({
        payment_method: "ZALOPAY",
        payment_id: "pay-zero-1",
        payment_status: "SUCCEEDED",
        total_payable_vnd: 0,
        orders: [{ id: "ord-zero-1" }],
      }),
      "/don-hang/ord-zero-1"
    );
    assert.equal(
      checkoutHelpers.resolvePostCheckoutHref({
        payment_method: "VIETQR",
        payment_id: "pay-zero-2",
        payment_status: "SUCCEEDED",
        total_payable_vnd: 0,
        orders: [{ id: "ord-a" }, { id: "ord-b" }],
      }),
      "/don-hang"
    );
    assert.equal(
      checkoutHelpers.resolvePostCheckoutHref({
        payment_method: "ZALOPAY",
        payment_id: "pay-pos-1",
        payment_status: "PENDING",
        total_payable_vnd: 150000,
        orders: [{ id: "ord-pos-1" }],
      }),
      "/thanh-toan/cho-thanh-toan/pay-pos-1"
    );

    // Result page status for 0 VND CONFIRMED + SUCCEEDED orders (ZALOPAY, VIETQR, COD)
    assert.equal(orderMeta.getOrderStatusInfo("CONFIRMED").label, "Đã xác nhận");
    for (const method of ["ZALOPAY", "VIETQR", "COD"]) {
      const payInfo = orderMeta.getPaymentStatusInfo("SUCCEEDED", method, "CONFIRMED");
      assert.equal(
        payInfo.label,
        "Đã thanh toán",
        `0 VND ${method} CONFIRMED+SUCCEEDED order should display 'Đã thanh toán', got: ${payInfo.label}`
      );
      assert.equal(
        orderMeta.needsVietQrPayment({
          status: "CONFIRMED",
          payment_method: method,
          payment_status: "SUCCEEDED",
          payment_id: "pay-zero-1",
        }),
        false
      );
    }

    const checkoutClientSrc = readSrc("app/(shop)/thanh-toan/CheckoutClient.tsx");
    assert.ok(
      checkoutClientSrc.includes("resolvePostCheckoutHref"),
      "CheckoutClient must route post-checkout via resolvePostCheckoutHref"
    );
  });

  test("10. [Review r4] localizeTimelineNote & localizeRefundReason translate all order/payment system strings to Vietnamese while preserving human text", async () => {
    const orderMeta = await import("../src/features/account/orderMeta.ts");

    assert.equal(
      typeof orderMeta.localizeRefundReason,
      "function",
      "orderMeta must export shared localizeRefundReason helper"
    );

    // Table-driven check for every backend timeline note emitted by services/order/internal/{service,store}
    const timelineCases = [
      {
        input: "Switched payment method from ZALOPAY to VIETQR",
        mustInclude: ["Ví điện tử ZaloPay", "Chuyển khoản VietQR"],
        mustExclude: ["Switched payment method", "from ZALOPAY"],
      },
      {
        input: "Switched payment method from VIETQR to ZALOPAY",
        mustInclude: ["Chuyển khoản VietQR", "Ví điện tử ZaloPay"],
        mustExclude: ["Switched payment method"],
      },
      {
        input: "Switched payment method from ZALOPAY to COD; order confirmed",
        mustInclude: ["Ví điện tử ZaloPay", "COD", "xác nhận"],
        mustExclude: ["Switched payment method", "order confirmed"],
      },
      {
        input: "Switched payment method from VIETQR to COD; order confirmed",
        mustInclude: ["Chuyển khoản VietQR", "COD", "xác nhận"],
        mustExclude: ["Switched payment method", "order confirmed"],
      },
      {
        input: "Order created and confirmed (ZALOPAY)",
        mustInclude: ["ZaloPay", "xác nhận"],
        mustExclude: ["Order created"],
      },
      {
        input: "Seller confirmed order and began preparation",
        mustInclude: ["Gian hàng", "xác nhận"],
        mustExclude: ["Seller confirmed", "preparation"],
      },
      {
        input: "Shipment creation queued for async retry",
        mustInclude: ["vận đơn"],
        mustExclude: ["Shipment creation", "async retry"],
      },
      {
        input: "Async shipment creation failed after 5 attempts",
        mustInclude: ["5", "vận đơn", "thất bại"],
        mustExclude: ["Async shipment", "attempts"],
      },
      {
        input: "Shipment created via GHN (tracking: GHN-001)",
        mustInclude: ["GHN-001", "vận đơn"],
        mustExclude: ["Shipment created via", "tracking:"],
      },
      {
        input: "Shipment created asynchronously via GHTK (tracking: GHTK-999)",
        mustInclude: ["GHTK-999", "vận đơn"],
        mustExclude: ["Shipment created asynchronously", "tracking:"],
      },
      {
        input: "Synced live forward shipment after pending_shipment_cancel TTL expiry",
        mustInclude: ["đồng bộ", "vận đơn"],
        mustExclude: ["Synced live", "TTL expiry"],
      },
      {
        input: "Shipment in transit (IN_TRANSIT)",
        mustInclude: ["vận chuyển"],
        mustExclude: ["Shipment in transit", "IN_TRANSIT"],
      },
      {
        input: "Shipment update: DELIVERED",
        mustInclude: ["vận chuyển"],
        mustExclude: ["Shipment update", "DELIVERED"],
      },
      {
        input: "Return shipment update: IN_TRANSIT",
        mustInclude: ["hoàn trả"],
        mustExclude: ["Return shipment update", "IN_TRANSIT"],
      },
      {
        input: "[PICKING] Shipment update: PICKING",
        mustInclude: ["vận chuyển"],
        mustExclude: ["[PICKING]", "Shipment update"],
      },
      {
        input: "Return requested: Hàng bị móp hộp",
        mustInclude: ["Lý do yêu cầu trả hàng", "Hàng bị móp hộp"],
        mustExclude: ["Return requested:"],
      },
      {
        input: "Buyer submitted additional evidence",
        mustInclude: ["Người mua", "bằng chứng"],
        mustExclude: ["Buyer submitted"],
      },
      {
        input: "Seller accepted refund without return shipment",
        mustInclude: ["Gian hàng", "hoàn tiền"],
        mustExclude: ["Seller accepted", "without return"],
      },
      {
        input: "Seller accepted return; awaiting buyer return shipment",
        mustInclude: ["Gian hàng", "trả hàng"],
        mustExclude: ["Seller accepted", "awaiting buyer"],
      },
      {
        input: "Seller confirmed returned goods received in good condition",
        mustInclude: ["Gian hàng", "hàng trả"],
        mustExclude: ["Seller confirmed", "good condition"],
      },
      {
        input: "Admin requested additional evidence from buyer",
        mustInclude: ["Quản trị viên", "bằng chứng"],
        mustExclude: ["Admin requested"],
      },
      {
        input: "Admin approved refund",
        mustInclude: ["Quản trị viên", "hoàn tiền"],
        mustExclude: ["Admin approved"],
      },
      {
        input: "Admin approved return and refund",
        mustInclude: ["Quản trị viên", "trả hàng", "hoàn tiền"],
        mustExclude: ["Admin approved"],
      },
      {
        input: "Admin rejected return dispute",
        mustInclude: ["Quản trị viên", "từ chối"],
        mustExclude: ["Admin rejected"],
      },
      {
        input:
          "Admin rejected return dispute (vận đơn hoàn trả quá hạn vận chuyển 14 ngày, đã đóng mà không huỷ vận đơn đã lấy hàng)",
        mustInclude: ["Quản trị viên", "từ chối", "14 ngày"],
        mustExclude: ["Admin rejected"],
      },
      {
        input: "Return refund and compensations completed",
        mustInclude: ["hoàn tiền"],
        mustExclude: ["Return refund", "compensations completed"],
      },
      {
        input: "Large refund approval rejected: Không đủ bằng chứng",
        mustInclude: ["hoàn tiền lớn", "từ chối", "Không đủ bằng chứng"],
        mustExclude: ["Large refund approval rejected"],
      },
      {
        input: "Large refund approval rejected: Large refund approval rejected by checker",
        mustInclude: ["hoàn tiền lớn", "từ chối"],
        mustExclude: ["Large refund approval rejected", "by checker"],
      },
      {
        input: "Auto-accepted return after seller response deadline expired",
        mustInclude: ["tự động", "trả hàng"],
        mustExclude: ["Auto-accepted", "deadline expired"],
      },
      {
        input: "Auto-confirmed returned goods after seller inspection deadline expired",
        mustInclude: ["tự động", "hàng trả"],
        mustExclude: ["Auto-confirmed", "inspection deadline"],
      },
      {
        input: "Escalated automatically: unable to create return shipment before deadline",
        mustInclude: ["khiếu nại", "vận đơn hoàn trả"],
        mustExclude: ["Escalated automatically", "before deadline"],
      },
      {
        input: "Closed automatically: buyer did not ship return within deadline",
        mustInclude: ["đóng yêu cầu", "gửi hàng trả"],
        mustExclude: ["Closed automatically", "within deadline"],
      },
      {
        input:
          "Escalated automatically: return shipment could not be cancelled and remained unsynced after 3 deadline extensions",
        mustInclude: ["khiếu nại", "3"],
        mustExclude: ["Escalated automatically", "remained unsynced"],
      },
      {
        input: "Escalated automatically: return shipment exceeded transit deadline (14 days)",
        mustInclude: ["khiếu nại", "14 ngày"],
        mustExclude: ["Escalated automatically", "transit deadline"],
      },
      {
        input: "Closed automatically: buyer did not escalate rejection within deadline",
        mustInclude: ["đóng yêu cầu"],
        mustExclude: ["Closed automatically", "escalate rejection"],
      },
      {
        input: "Closed automatically: buyer did not provide requested evidence within deadline",
        mustInclude: ["đóng yêu cầu", "bằng chứng"],
        mustExclude: ["Closed automatically", "requested evidence"],
      },
      {
        input: "Refund rf-123 processed: 150000 VND (late_payment)",
        mustInclude: ["hoàn tiền", "Thanh toán đến trễ"],
        mustExclude: ["processed:", "late_payment"],
      },
      {
        input:
          "MANUAL_REVIEW_REQUIRED: orphan forward shipment could not be cancelled because carrier already picked up (buyer cancel: err)",
        mustInclude: ["rà soát", "lấy hàng"],
        mustExclude: ["MANUAL_REVIEW_REQUIRED", "orphan forward shipment"],
      },
      {
        input: "Order returned to seller",
        mustInclude: ["hoàn trả", "gian hàng"],
        mustExclude: ["Order returned to seller"],
      },
      {
        input: "Cancelled by buyer",
        mustInclude: ["Người mua", "hủy đơn"],
        mustExclude: ["Cancelled by"],
      },
    ];

    for (const tc of timelineCases) {
      const out = orderMeta.localizeTimelineNote(tc.input);
      for (const kw of tc.mustInclude) {
        assert.ok(
          out.includes(kw),
          `localizeTimelineNote(${JSON.stringify(tc.input)}) expected to include ${JSON.stringify(kw)}, got: ${JSON.stringify(out)}`
        );
      }
      for (const bad of tc.mustExclude) {
        assert.equal(
          out.includes(bad),
          false,
          `localizeTimelineNote(${JSON.stringify(tc.input)}) leaked ${JSON.stringify(bad)} in: ${JSON.stringify(out)}`
        );
      }
    }

    // Table-driven check for localizeRefundReason
    const refundReasonCases = [
      ["late_payment", "Thanh toán đến trễ"],
      ["Late payment succeeded for superseded checkout payment", "Thanh toán đến trễ"],
      ["Order return refund", "trả hàng"],
      ["Order returned to seller", "gian hàng"],
      ["order_cancelled_auto_refund", "hủy"],
      ["Cancelled by buyer", "Người mua hủy đơn"],
      ["Cancelled by seller: Hết hàng trong kho", "Gian hàng hủy đơn: Hết hàng trong kho"],
      ["reconciliation_overpayment", "đối soát"],
      ["reconciliation_refund", "đối soát"],
      ["payment_timeout_expired", "hạn thanh toán"],
    ];
    for (const [raw, expectedSub] of refundReasonCases) {
      const out = orderMeta.localizeRefundReason(raw);
      assert.ok(
        out.includes(expectedSub),
        `localizeRefundReason(${JSON.stringify(raw)}) expected to include ${JSON.stringify(expectedSub)}, got: ${JSON.stringify(out)}`
      );
      assert.notEqual(
        out,
        raw,
        `localizeRefundReason(${JSON.stringify(raw)}) must not return raw system code/English`
      );
    }
    // User-typed free text MUST be preserved verbatim
    assert.equal(
      orderMeta.localizeRefundReason("Khách muốn đổi sang màu khác"),
      "Khách muốn đổi sang màu khác"
    );
  });

  test("11. [Review r4] Items 1, 3–10 behavioral & UI invariants (suspended seller gate, activities, role select, 409 suspend, payment waiting, order CTA, COD phone link, approvals actor display)", async () => {
    const rbac = await import("../src/lib/admin-rbac.ts");
    const errorMap = await import("../src/lib/server/error-map.ts");
    const adminTypes = await import("../src/features/admin/types.ts");

    // #1: SellerShopGate allowSuspended prop, suspended_reason/suspended_at, allowed vs blocked pages
    const sellerCtxSrc = readSrc("features/seller/SellerContext.tsx");
    assert.ok(
      sellerCtxSrc.includes("allowSuspended"),
      "SellerShopGate must accept allowSuspended prop"
    );
    assert.ok(
      sellerCtxSrc.includes("shop.suspended_reason") && sellerCtxSrc.includes("shop.suspended_at"),
      "SellerShopGate must display shop.suspended_reason and shop.suspended_at (not rejection_reason)"
    );
    for (const relPath of [
      "app/(seller)/seller/page.tsx",
      "app/(seller)/seller/don-hang/page.tsx",
      "app/(seller)/seller/don-hang/[id]/page.tsx",
      "app/(seller)/seller/tra-hang/page.tsx",
      "app/(seller)/seller/tai-chinh/page.tsx",
    ]) {
      const src = readSrc(relPath);
      assert.ok(
        src.includes("allowSuspended"),
        `${relPath} must pass allowSuspended to SellerShopGate per F12`
      );
    }
    for (const relPath of [
      "app/(seller)/seller/san-pham/page.tsx",
      "features/seller/ProductFormView.tsx",
      "app/(seller)/seller/khuyen-mai/page.tsx",
      "app/(seller)/seller/ho-so/page.tsx",
      "app/(seller)/seller/chat/page.tsx",
      "app/(seller)/seller/tuong-tac/page.tsx",
    ]) {
      const src = readSrc(relPath);
      assert.equal(
        src.includes("allowSuspended"),
        false,
        `${relPath} must stay blocked when shop is SUSPENDED`
      );
    }

    // #3: localizeRefundReason wired into buyer OrderDetailClient, admin doi-soat, and AdminApprovalsPanel
    const orderDetailSrc = readSrc("features/account/OrderDetailClient.tsx");
    const doiSoatSrc = readSrc("app/(admin)/admin/doi-soat/page.tsx");
    const approvalsSrc = readSrc("features/admin/AdminApprovalsPanel.tsx");
    assert.ok(orderDetailSrc.includes("localizeRefundReason"));
    assert.ok(doiSoatSrc.includes("localizeRefundReason"));
    assert.ok(approvalsSrc.includes("localizeRefundReason"));

    // #4: Real activity event labels & all 5 categories from services/user/internal/service/governance.go
    assert.equal(
      typeof adminTypes.formatUserActivityEventType,
      "function",
      "features/admin/types.ts must export formatUserActivityEventType"
    );
    const realUserEvents = [
      "login_succeeded",
      "login_failed",
      "logout",
      "password_changed",
      "password_reset_requested",
      "password_reset_completed",
      "password_reset_failed",
      "password_reset_sent",
      "profile_updated",
      "phone_otp_requested",
      "phone_otp_verified",
      "phone_otp_failed",
      "phone_verified",
      "status_changed",
      "role_changed",
      "staff_created",
      "shop_suspended",
      "shop_unsuspended",
    ];
    for (const ev of realUserEvents) {
      const label = adminTypes.formatUserActivityEventType(ev);
      assert.notEqual(
        label,
        ev,
        `activity event ${ev} must map to a Vietnamese label, got raw code`
      );
      assert.equal(
        adminTypes.formatUserActivityEventType(ev.toUpperCase()),
        label,
        `activity event lookup must be case-insensitive for ${ev}`
      );
    }
    const usersPanelSrc = readSrc("features/admin/AdminUsersPanel.tsx");
    assert.ok(
      usersPanelSrc.includes('value="phone"') && usersPanelSrc.includes('value="role"'),
      "AdminUsersPanel activity category filter must include phone and role categories"
    );

    // #5: Role change select MUST exclude super_admin and lock super_admin targets
    assert.ok(
      Array.isArray(rbac.ASSIGNABLE_USER_ROLES) &&
        !rbac.ASSIGNABLE_USER_ROLES.includes("super_admin"),
      "ASSIGNABLE_USER_ROLES must exclude super_admin per governance.go:554"
    );
    assert.ok(
      usersPanelSrc.includes("ASSIGNABLE_USER_ROLES"),
      "AdminUsersPanel role-change select must use ASSIGNABLE_USER_ROLES"
    );

    // #6: Duplicate shop suspension 409 returns specific Vietnamese copy without changing generic 409
    const dupSusp = errorMap.sanitizeGatewayError(409, {
      error: {
        code: "ALREADY_EXISTS",
        message: "a pending suspension approval already exists for this shop",
      },
    });
    assert.equal(dupSusp.message, "Gian hàng này đã có yêu cầu khoá đang chờ duyệt.");
    const genericDup = errorMap.sanitizeGatewayError(409, {
      error: { code: "ALREADY_EXISTS", message: "duplicate record" },
    });
    assert.equal(genericDup.message, "Thông tin đã tồn tại trong hệ thống.");

    // #7: Payment waiting page neutral loading, steps hidden after paid, paid total label, ZaloPay hold copy
    const waitPageSrc = readSrc("app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/page.tsx");
    const waitClientSrc = readSrc(
      "app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx"
    );
    assert.equal(
      waitPageSrc.includes("Đang tải thông tin thanh toán VietQR"),
      false,
      "Payment waiting page loading copy must be neutral (not VietQR-only)"
    );
    assert.equal(
      waitClientSrc.includes("Đang tải mã chuyển khoản VietQR"),
      false,
      "PaymentWaitingClient loading copy must be neutral"
    );
    assert.ok(
      waitClientSrc.includes("Tổng đã thanh toán"),
      "PaymentWaitingClient must switch total label to 'Tổng đã thanh toán' when paid"
    );
    assert.ok(
      waitClientSrc.includes("thời gian chờ thanh toán"),
      "PaymentWaitingClient must use 'thời gian chờ thanh toán' for ZaloPay"
    );

    // #8: OrderDetailClient header CTA must be neutral "Tiếp tục thanh toán"
    assert.equal(
      orderDetailSrc.includes(">\n              Thanh toán VietQR\n            <"),
      false,
      "OrderDetailClient header button must not hardcode 'Thanh toán VietQR'"
    );
    assert.ok(
      orderDetailSrc.includes("Tiếp tục thanh toán"),
      "OrderDetailClient header CTA must use 'Tiếp tục thanh toán'"
    );

    // #9: RetryPaymentBox shows phone verification link on COD_PHONE_NOT_VERIFIED
    const retryBoxSrc = readSrc("features/commerce/RetryPaymentBox.tsx");
    assert.ok(
      retryBoxSrc.includes("COD_PHONE_NOT_VERIFIED") &&
        retryBoxSrc.includes("/tai-khoan#xac-minh-sdt"),
      "RetryPaymentBox must render phone verification link when switching to COD fails with COD_PHONE_NOT_VERIFIED"
    );

    // #10: AdminApprovalsPanel resolves maker/checker names via admin/users and formats short 8-char fallback (no raw UUID)
    assert.equal(
      typeof adminTypes.formatApprovalActorDisplay,
      "function",
      "features/admin/types.ts must export formatApprovalActorDisplay"
    );
    const uuidSample = "12345678-1234-4123-8123-123456789abc";
    const resolvedDisplay = adminTypes.formatApprovalActorDisplay(uuidSample, "moderator", {
      [uuidSample]: { full_name: "Trần Kiểm Duyệt", email: "mod@nexusmart.vn", role: "moderator" },
    });
    assert.equal(resolvedDisplay, "Trần Kiểm Duyệt (Kiểm duyệt viên)");
    const fallbackDisplay = adminTypes.formatApprovalActorDisplay(uuidSample, "moderator", {});
    assert.equal(
      fallbackDisplay.includes(uuidSample),
      false,
      "Fallback actor display must NOT leak raw 36-char UUID"
    );
    assert.ok(
      fallbackDisplay.includes("12345678") && fallbackDisplay.includes("Kiểm duyệt viên"),
      `Fallback actor display should include 8-char short code and role label, got: ${fallbackDisplay}`
    );
    assert.ok(
      approvalsSrc.includes("formatApprovalActorDisplay"),
      "AdminApprovalsPanel must format maker/checker via formatApprovalActorDisplay"
    );
  });
});
