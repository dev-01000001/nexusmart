import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_ROOT = resolve(__dirname, "..");
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

const formatMod = await import("../src/lib/format.ts");
const redirectMod = await import("../src/lib/redirect.ts");
const orderMetaMod = await import("../src/features/account/orderMeta.ts");
const sellerTypesMod = await import("../src/features/seller/types.ts");
const adminTypesMod = await import("../src/features/admin/types.ts");

let errorMapMod = null;
try {
  errorMapMod = await import("../src/lib/server/error-map.ts");
} catch {
  errorMapMod = null;
}

let proxySecMod = null;
try {
  proxySecMod = await import("../src/lib/server/proxy-security.ts");
} catch {
  proxySecMod = null;
}

describe("Wave 2a web features — unit tests", () => {
  test("1. Buyer return helpers: evidence URL validation, return/refund/bearer labels, bank account & redelivery", () => {
    assert.equal(
      typeof orderMetaMod.validateEvidenceUrls,
      "function",
      "validateEvidenceUrls must be exported from orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.getReturnStatusInfo,
      "function",
      "getReturnStatusInfo must be exported from orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.getReturnFeeBearerLabel,
      "function",
      "getReturnFeeBearerLabel must be exported from orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.getRefundStatusInfo,
      "function",
      "getRefundStatusInfo must be exported from orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.validateBankAccountNumber,
      "function",
      "validateBankAccountNumber must be exported from orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.canRequestRedelivery,
      "function",
      "canRequestRedelivery must be exported from orderMeta.ts"
    );

    // Evidence URL validation: up to 5 valid http(s) URLs <= 500 chars
    const okUrls = orderMetaMod.validateEvidenceUrls([
      "https://example.com/a.jpg",
      "  http://example.com/b.png  ",
      "",
    ]);
    assert.equal(okUrls.ok, true);
    assert.deepEqual(okUrls.urls, ["https://example.com/a.jpg", "http://example.com/b.png"]);

    const tooMany = orderMetaMod.validateEvidenceUrls([
      "https://a.test/1",
      "https://a.test/2",
      "https://a.test/3",
      "https://a.test/4",
      "https://a.test/5",
      "https://a.test/6",
    ]);
    assert.equal(tooMany.ok, false, "more than 5 evidence URLs must be rejected");

    const badScheme = orderMetaMod.validateEvidenceUrls(["javascript:alert(1)"]);
    assert.equal(badScheme.ok, false, "non-http(s) URL must be rejected");

    const tooLong = orderMetaMod.validateEvidenceUrls([`https://example.com/${"a".repeat(490)}`]);
    assert.equal(tooLong.ok, false, "URL > 500 chars must be rejected");

    // Return statuses: every status maps to Vietnamese (never returns raw enum)
    const statuses = [
      "PENDING",
      "AWAITING_SHIPMENT",
      "IN_TRANSIT",
      "RECEIVED",
      "REFUNDING",
      "REFUNDED",
      "REJECTED",
      "ESCALATED",
      "EVIDENCE_REQUESTED",
      "CLOSED",
    ];
    for (const st of statuses) {
      const info = orderMetaMod.getReturnStatusInfo(st);
      assert.ok(info.label && info.label !== st, `return status ${st} must have Vietnamese label`);
      assert.ok(
        !/^[A-Z_]+$/.test(info.label),
        `return status label for ${st} must not be raw enum: ${info.label}`
      );
    }

    // Fee bearer labels
    assert.notEqual(orderMetaMod.getReturnFeeBearerLabel("SELLER"), "SELLER");
    assert.notEqual(orderMetaMod.getReturnFeeBearerLabel("BUYER"), "BUYER");
    assert.notEqual(orderMetaMod.getReturnFeeBearerLabel("PLATFORM"), "PLATFORM");

    // Refund status labels (handles both proto uppercase and lowercase)
    assert.equal(orderMetaMod.getRefundStatusInfo("PENDING").tone, "warn");
    assert.equal(orderMetaMod.getRefundStatusInfo("pending").tone, "warn");
    assert.equal(orderMetaMod.getRefundStatusInfo("DONE").tone, "pos");
    assert.equal(orderMetaMod.getRefundStatusInfo("completed").tone, "pos");
    assert.equal(orderMetaMod.getRefundStatusInfo("FAILED").tone, "neg");

    // Bank account number validation (6-19 digits)
    assert.equal(orderMetaMod.validateBankAccountNumber("123456"), true);
    assert.equal(orderMetaMod.validateBankAccountNumber("1234567890123456789"), true);
    assert.equal(orderMetaMod.validateBankAccountNumber("12345"), false);
    assert.equal(orderMetaMod.validateBankAccountNumber("12345678901234567890"), false);
    assert.equal(orderMetaMod.validateBankAccountNumber("12345ABC"), false);

    // Redelivery eligibility
    assert.equal(
      orderMetaMod.canRequestRedelivery({
        status: "FAILED",
        direction: "FORWARD",
        redelivery_count: 0,
      }),
      true
    );
    assert.equal(
      orderMetaMod.canRequestRedelivery({
        status: "FAILED",
        direction: "FORWARD",
        redelivery_count: 2,
      }),
      true
    );
    assert.equal(
      orderMetaMod.canRequestRedelivery({
        status: "FAILED",
        direction: "FORWARD",
        redelivery_count: 3,
      }),
      false,
      "max 3 redelivery attempts reached"
    );
    assert.equal(
      orderMetaMod.canRequestRedelivery({
        status: "DELIVERING",
        direction: "FORWARD",
        redelivery_count: 0,
      }),
      false
    );
    assert.equal(
      orderMetaMod.canRequestRedelivery({
        status: "FAILED",
        direction: "RETURN",
        redelivery_count: 0,
      }),
      false,
      "redelivery applies only to FORWARD shipments"
    );

    const detailSrc = readFileSync(
      resolve(WEB_SRC, "features/account/OrderDetailClient.tsx"),
      "utf8"
    );
    assert.ok(
      /chưa hỗ trợ tải ảnh trực tiếp/i.test(detailSrc),
      "OrderDetailClient must explicitly note direct image upload is not yet supported"
    );
    assert.ok(detailSrc.includes("/escalate"), "OrderDetailClient must wire buyer escalate action");
    assert.ok(detailSrc.includes("/evidence"), "OrderDetailClient must wire buyer add evidence action");
    assert.ok(detailSrc.includes("/redelivery"), "OrderDetailClient must wire buyer redelivery action");
    assert.ok(
      detailSrc.includes("/bank-account"),
      "OrderDetailClient must wire buyer refund bank account submission"
    );
  });

  test("2. Seller new shipment flow, /seller/tra-hang, /seller/ho-so shipping settings, and PAY-B12 finance formula", () => {
    assert.equal(
      typeof sellerTypesMod.getSellerOrderDisplayMeta,
      "function",
      "getSellerOrderDisplayMeta must be exported from seller/types.ts"
    );
    assert.equal(
      typeof sellerTypesMod.computeOrderDeductionsVnd,
      "function",
      "computeOrderDeductionsVnd must be exported from seller/types.ts"
    );

    // PREPARING with tracking_code shows "Chờ hãng lấy hàng"; without tracking shows "Đang chuẩn bị"
    const awaitingPickup = sellerTypesMod.getSellerOrderDisplayMeta({
      status: "PREPARING",
      tracking_code: "GHN-12345",
    });
    assert.equal(awaitingPickup.label, "Chờ hãng lấy hàng");

    const preparingNoTrack = sellerTypesMod.getSellerOrderDisplayMeta({
      status: "PREPARING",
      tracking_code: "",
    });
    assert.equal(preparingNoTrack.label, "Đang chuẩn bị");

    const shippingMeta = sellerTypesMod.getSellerOrderDisplayMeta({
      status: "SHIPPING",
      tracking_code: "GHN-12345",
    });
    assert.equal(shippingMeta.label, "Đang giao");

    // Once shipment created (tracking_code present), seller cannot create another shipment
    assert.equal(sellerTypesMod.canSellerShipOrder("CONFIRMED", ""), true);
    assert.equal(sellerTypesMod.canSellerShipOrder("PREPARING", ""), true);
    assert.equal(
      sellerTypesMod.canSellerShipOrder("PREPARING", "GHN-12345"),
      false,
      "PREPARING order that already has tracking_code must not show create shipment action again"
    );

    // PAY-B12: deductions = flash + shop voucher + commission, and subtotal - deductions === net
    const sampleOrder = {
      subtotal_vnd: 500000,
      flash_sale_discount_vnd: 50000,
      shop_voucher_discount_vnd: 30000,
      commission_vnd: 21000,
    };
    const deductions = sellerTypesMod.computeOrderDeductionsVnd(sampleOrder);
    const net = sellerTypesMod.computeOrderNetVnd(sampleOrder);
    assert.equal(deductions, 101000);
    assert.equal(sampleOrder.subtotal_vnd - deductions, net);

    const financeSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/tai-chinh/page.tsx"),
      "utf8"
    );
    assert.ok(
      financeSrc.includes("visibleDeductions"),
      "seller/tai-chinh must sum visibleDeductions instead of only visibleCommission"
    );
    assert.equal(
      financeSrc.includes("Tạm giữ cho các đơn đang xử lý & giao"),
      false,
      "seller/tai-chinh must not misdescribe pending_vnd as orders in progress/shipping"
    );
    assert.ok(
      /72\s*giờ|giữ\s*tiền|thời\s*gian\s*giữ/i.test(financeSrc),
      "seller/tai-chinh pending_vnd must describe the 72-hour hold period"
    );

    // /seller/tra-hang page exists and SellerShell links to it
    const sellerReturnsPath = resolve(WEB_SRC, "app/(seller)/seller/tra-hang/page.tsx");
    assert.equal(existsSync(sellerReturnsPath), true, "/seller/tra-hang/page.tsx must exist");
    const sellerReturnsSrc = readFileSync(sellerReturnsPath, "utf8");
    assert.ok(sellerReturnsSrc.includes("seller/returns"), "seller/tra-hang must fetch seller/returns");
    assert.ok(sellerReturnsSrc.includes("/respond"), "seller/tra-hang must wire respond endpoint");
    assert.ok(
      sellerReturnsSrc.includes("/confirm-received"),
      "seller/tra-hang must wire confirm-received endpoint"
    );
    assert.ok(sellerReturnsSrc.includes("/dispute"), "seller/tra-hang must wire dispute endpoint");

    const sellerShellSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/SellerShell.tsx"), "utf8");
    assert.ok(
      sellerShellSrc.includes("/seller/tra-hang"),
      "SellerShell navigation must include /seller/tra-hang"
    );

    // /seller/ho-so wires real seller/shipping-settings
    const sellerProfileSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/ho-so/page.tsx"),
      "utf8"
    );
    assert.ok(
      sellerProfileSrc.includes("seller/shipping-settings"),
      "seller/ho-so must wire real GET/PUT seller/shipping-settings"
    );
  });

  test("3. Admin /admin/khieu-nai real returns, /admin/doi-soat queues + VN date, /admin/cau-hinh carriers", () => {
    // VN date boundary (01:00 VN = 18:00 UTC previous day)
    assert.equal(
      formatMod.formatVnDayKey("2026-11-22T18:00:00Z"),
      "2026-11-23",
      "formatVnDayKey must evaluate calendar date in Asia/Ho_Chi_Minh (UTC+7)"
    );
    assert.equal(formatMod.formatVnDayKey("2026-11-22T16:59:59Z"), "2026-11-22");

    assert.equal(
      typeof adminTypesMod.getReconciliationQueueStatusMeta,
      "function",
      "getReconciliationQueueStatusMeta must be exported from admin/types.ts"
    );
    for (const st of ["pending", "suspended", "assigned", "refunded"]) {
      const meta = adminTypesMod.getReconciliationQueueStatusMeta(st);
      assert.ok(meta.label && meta.label.toLowerCase() !== st, `queue status ${st} must be localized`);
    }

    // /admin/khieu-nai uses real admin/returns API, no mock disputes / NexusPay
    const disputesSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/khieu-nai/page.tsx"),
      "utf8"
    );
    assert.ok(disputesSrc.includes("admin/returns"), "/admin/khieu-nai must use admin/returns API");
    assert.ok(disputesSrc.includes("/resolve"), "/admin/khieu-nai must wire admin resolve endpoint");
    assert.ok(
      disputesSrc.includes("return_fee_bearer"),
      "/admin/khieu-nai must send return_fee_bearer"
    );
    assert.equal(
      disputesSrc.includes("NexusPay"),
      false,
      "/admin/khieu-nai must not contain mock NexusPay copy"
    );

    // /admin/doi-soat includes reconciliation queue + pending refunds queue with Idempotency-Key
    const doiSoatSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/doi-soat/page.tsx"),
      "utf8"
    );
    assert.ok(
      doiSoatSrc.includes("admin/reconciliation/queue"),
      "/admin/doi-soat must load admin/reconciliation/queue"
    );
    assert.ok(
      doiSoatSrc.includes("admin/refunds"),
      "/admin/doi-soat must load admin/refunds queue"
    );
    assert.ok(doiSoatSrc.includes("/settle"), "/admin/doi-soat must wire refund settle endpoint");

    // /admin/cau-hinh wires AdminCarriersPanel using real admin/carriers API
    const carriersPanelPath = resolve(WEB_SRC, "features/admin/AdminCarriersPanel.tsx");
    assert.equal(existsSync(carriersPanelPath), true, "AdminCarriersPanel.tsx must exist");
    const carriersSrc = readFileSync(carriersPanelPath, "utf8");
    assert.ok(carriersSrc.includes("admin/carriers"), "AdminCarriersPanel must use admin/carriers API");
    assert.ok(carriersSrc.includes("/test"), "AdminCarriersPanel must wire carrier test endpoint");

    const cauHinhSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/cau-hinh/page.tsx"),
      "utf8"
    );
    assert.ok(
      cauHinhSrc.includes("AdminCarriersPanel"),
      "/admin/cau-hinh must render AdminCarriersPanel"
    );
  });

  test("4. Account security: /quen-mat-khau, /dat-lai-mat-khau, locked reason, and 401 clean logout", () => {
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/api/auth/forgot-password/route.ts")),
      true,
      "BFF route /api/auth/forgot-password must exist"
    );
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/api/auth/reset-password/route.ts")),
      true,
      "BFF route /api/auth/reset-password must exist"
    );
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/(shop)/quen-mat-khau/page.tsx")),
      true,
      "/quen-mat-khau page must exist"
    );
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/(shop)/dat-lai-mat-khau/page.tsx")),
      true,
      "/dat-lai-mat-khau page must exist"
    );

    const loginSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/dang-nhap/page.tsx"), "utf8");
    assert.ok(
      loginSrc.includes("/quen-mat-khau"),
      "/dang-nhap must include a link to /quen-mat-khau"
    );

    // Error mapping: account locked preserves reason, 409 carrier picked up gives clear Vietnamese message
    assert.ok(
      errorMapMod && typeof errorMapMod.sanitizeGatewayError === "function",
      "sanitizeGatewayError must be exported from pure module web/src/lib/server/error-map.ts"
    );
    const lockedRes = errorMapMod.sanitizeGatewayError(403, {
      error: {
        code: "PERMISSION_DENIED",
        message: "account is locked: Vi phạm quy định giao dịch",
      },
    });
    assert.ok(
      lockedRes.message.includes("Vi phạm quy định giao dịch"),
      `locked error must preserve lock reason, got: ${lockedRes.message}`
    );
    assert.ok(
      /liên hệ/i.test(lockedRes.message),
      `locked error must include contact guidance, got: ${lockedRes.message}`
    );

    const pickedUpRes = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "cannot cancel shipment: parcel already picked up by carrier",
      },
    });
    assert.ok(
      /đã lấy/i.test(pickedUpRes.message),
      `409 picked-up error must explain carrier already picked up parcel, got: ${pickedUpRes.message}`
    );

    // Revoked token 401 triggers clean logout + redirect to /dang-nhap in SessionContext
    const sessionCtxSrc = readFileSync(resolve(WEB_SRC, "context/SessionContext.tsx"), "utf8");
    assert.ok(
      sessionCtxSrc.includes("/api/auth/logout") && sessionCtxSrc.includes("/dang-nhap"),
      "SessionContext must call /api/auth/logout and redirect to /dang-nhap on SESSION_EXPIRED_EVENT"
    );
  });

  test("5. ORD-B12 & USR-B10: Vietnamese timeline notes, RETURNED filter tab, real seller cover, super_admin & audit time filters", () => {
    // ORD-B12 timeline localization
    const noteAwaiting = orderMetaMod.localizeTimelineNote("Order created awaiting payment");
    assert.ok(!/order created/i.test(noteAwaiting), `must translate awaiting payment: ${noteAwaiting}`);

    const noteShipped = orderMetaMod.localizeTimelineNote(
      "Shipped via ghn (tracking: GHN-1E8BD14AE3)"
    );
    assert.ok(!/shipped via/i.test(noteShipped), `must translate shipped via: ${noteShipped}`);
    assert.ok(noteShipped.includes("GHN-1E8BD14AE3"), "must keep tracking code");
    assert.ok(noteShipped.includes("Giao Hàng Nhanh"), "must localize carrier label");

    const noteTimeout = orderMetaMod.localizeTimelineNote("seller_confirm_timeout");
    assert.ok(!/seller_confirm_timeout/i.test(noteTimeout), `must localize timeout: ${noteTimeout}`);

    // R11: payment timeout & FAILED cancel_reason localization + wiring in buyer & seller order detail
    assert.equal(
      orderMetaMod.localizeTimelineNote("payment_timeout_expired"),
      "Đơn hàng tự động hủy do quá hạn thanh toán."
    );
    assert.equal(
      orderMetaMod.localizeTimelineNote("Payment expired after timeout"),
      "Đơn hàng tự động hủy do quá hạn thanh toán."
    );
    assert.equal(
      orderMetaMod.localizeTimelineNote("payment expired"),
      "Đơn hàng tự động hủy do quá hạn thanh toán."
    );
    assert.equal(
      orderMetaMod.localizeTimelineNote("FAILED"),
      "Đơn hàng bị hủy do thanh toán không thành công."
    );
    assert.equal(
      orderMetaMod.localizeTimelineNote("failed delivery attempt"),
      "failed delivery attempt",
      "exact FAILED match must not match unrelated prose"
    );
    const buyerDetailSrc = readFileSync(
      resolve(WEB_SRC, "features/account/OrderDetailClient.tsx"),
      "utf8"
    );
    assert.ok(
      buyerDetailSrc.includes("localizeTimelineNote(order.cancel_reason)"),
      "OrderDetailClient must pass order.cancel_reason through localizeTimelineNote"
    );
    const sellerOrderDetailSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/don-hang/[id]/page.tsx"),
      "utf8"
    );
    assert.ok(
      sellerOrderDetailSrc.includes("localizeTimelineNote(order.cancel_reason)"),
      "Seller order detail must pass order.cancel_reason through localizeTimelineNote"
    );

    const noteSellerRetRecv = orderMetaMod.localizeTimelineNote(
      "Seller confirmed return goods received"
    );
    assert.ok(
      /hàng trả/i.test(noteSellerRetRecv) && !/đóng gói/i.test(noteSellerRetRecv),
      `return receipt confirmation must not map to order packing note: ${noteSellerRetRecv}`
    );

    // ORD-B12 RETURNED filter tab on /don-hang
    assert.equal(
      orderMetaMod.ORDER_FILTER_TABS.some((t) => t.key === "RETURNED"),
      true,
      "ORDER_FILTER_TABS must include RETURNED tab"
    );

    // ORD-B12 seller order detail no hardcoded cover image
    const sellerDetailSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/don-hang/[id]/page.tsx"),
      "utf8"
    );
    assert.equal(
      sellerDetailSrc.includes('src="/products/ao-thun-cotton-trang.png"'),
      false,
      "/seller/don-hang/[id] must not hardcode /products/ao-thun-cotton-trang.png"
    );

    // USR-B10 super_admin in AdminUsersPanel + time filters in audit-log + redirect guards
    const usersPanelSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminUsersPanel.tsx"),
      "utf8"
    );
    assert.ok(
      usersPanelSrc.includes('"super_admin"'),
      "AdminUsersPanel must support super_admin role"
    );

    const auditLogSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/audit-log/page.tsx"),
      "utf8"
    );
    assert.ok(
      auditLogSrc.includes("from_time") && auditLogSrc.includes("to_time"),
      "/admin/audit-log must support from_time and to_time filters"
    );

    assert.equal(redirectMod.resolvePostLoginRedirect("seller", "/quen-mat-khau"), "/seller");
    assert.equal(redirectMod.resolvePostLoginRedirect("admin", "/dat-lai-mat-khau"), "/admin");
    assert.equal(orderMetaMod.normalizeNotificationHref("/verify-otp"), "/dang-ky");
    assert.equal(orderMetaMod.normalizeNotificationHref("/seller/dashboard"), "/seller");
    assert.equal(orderMetaMod.normalizeNotificationHref("/seller/orders/abc"), "/seller/don-hang/abc");
  });

  test("6. [Review #1] BFF proxy forwards CSP & X-Content-Type-Options, sandboxes text/html lacking CSP, and always sets nosniff", () => {
    assert.ok(
      proxySecMod && typeof proxySecMod.applyProxySecurityHeaders === "function",
      "applyProxySecurityHeaders must be exported from web/src/lib/server/proxy-security.ts"
    );
    const wantFallback =
      "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'";
    assert.equal(proxySecMod.FALLBACK_HTML_SANDBOX_CSP, wantFallback);

    // 1. Upstream text/html with gateway sandbox CSP -> forwarded as single CSP + nosniff
    const htmlUp = new Headers({
      "Content-Type": "text/html; charset=utf-8",
      "Content-Security-Policy": wantFallback,
      "X-Content-Type-Options": "nosniff",
    });
    const htmlOut = new Headers();
    proxySecMod.applyProxySecurityHeaders(htmlOut, htmlUp, "text/html; charset=utf-8");
    assert.equal(htmlOut.get("content-security-policy"), wantFallback);
    assert.equal(htmlOut.get("x-content-type-options"), "nosniff");

    // 2. Upstream text/html missing CSP -> injects fallback sandbox CSP + nosniff
    const bareHtmlOut = new Headers();
    proxySecMod.applyProxySecurityHeaders(bareHtmlOut, new Headers(), "text/html; charset=utf-8");
    assert.equal(bareHtmlOut.get("content-security-policy"), wantFallback);
    assert.equal(bareHtmlOut.get("x-content-type-options"), "nosniff");

    // 3. Upstream JSON with API CSP -> forwards CSP + always sets nosniff
    const jsonUp = new Headers({
      "Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
    });
    const jsonOut = new Headers();
    proxySecMod.applyProxySecurityHeaders(jsonOut, jsonUp, "application/json");
    assert.equal(jsonOut.get("content-security-policy"), "default-src 'none'; frame-ancestors 'none'");
    assert.equal(jsonOut.get("x-content-type-options"), "nosniff");

    // 4. Upstream response without headers -> still sets nosniff
    const emptyOut = new Headers();
    proxySecMod.applyProxySecurityHeaders(emptyOut, null, "application/json");
    assert.equal(emptyOut.get("x-content-type-options"), "nosniff");

    const gwRouteSrc = readFileSync(resolve(WEB_SRC, "app/api/gw/[...path]/route.ts"), "utf8");
    assert.ok(
      gwRouteSrc.includes("applyProxySecurityHeaders"),
      "/api/gw/[...path]/route.ts must call applyProxySecurityHeaders"
    );
  });

  test("7. [Review #2] Label print links use BFF /api/gw/shipping/labels/<tracking_code>, open new tab, and show Ctrl+P hint", () => {
    assert.equal(
      typeof orderMetaMod.buildShippingLabelHref,
      "function",
      "buildShippingLabelHref must be exported from orderMeta.ts"
    );
    assert.equal(
      orderMetaMod.buildShippingLabelHref("GHN-ABC 123"),
      "/api/gw/shipping/labels/GHN-ABC%20123"
    );
    assert.equal(orderMetaMod.buildShippingLabelHref("   "), null);
    assert.equal(orderMetaMod.buildShippingLabelHref(undefined), null);

    const files = [
      "features/account/OrderDetailClient.tsx",
      "app/(seller)/seller/don-hang/page.tsx",
      "app/(seller)/seller/don-hang/[id]/page.tsx",
      "app/(seller)/seller/tra-hang/page.tsx",
      "app/(admin)/admin/khieu-nai/page.tsx",
    ];
    for (const rel of files) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(src.includes("buildShippingLabelHref"), `${rel} must use buildShippingLabelHref`);
      assert.equal(
        /href=\{(?:shipment\.label_url|returnRequest\.return_label_url|record\.return_label_url)\}/.test(
          src
        ),
        false,
        `${rel} must never link directly to gateway label_url`
      );
      assert.ok(src.includes("Ctrl+P"), `${rel} must include Ctrl+P print instruction`);
    }
  });

  test("8. [Review #3 / PLT-B6] BFF enforces 2 MiB body size limit (Content-Length & chunked stream) with 413", async () => {
    assert.ok(
      proxySecMod && typeof proxySecMod.readBoundedBody === "function",
      "readBoundedBody must be exported from proxy-security.ts"
    );
    assert.equal(proxySecMod.MAX_BFF_BODY_BYTES, 2 * 1024 * 1024);

    // Within limit succeeds
    const smallReq = new Request("http://localhost:3000/api/gw/test", {
      method: "POST",
      body: JSON.stringify({ hello: "world" }),
    });
    const smallRes = await proxySecMod.readBoundedBody(smallReq);
    assert.equal(smallRes.ok, true);
    assert.equal(new TextDecoder().decode(smallRes.body), '{"hello":"world"}');

    // Content-Length > 2 MiB rejected with 413
    const bigHeaderReq = new Request("http://localhost:3000/api/gw/test", {
      method: "POST",
      headers: { "Content-Length": String(2 * 1024 * 1024 + 1) },
      body: "x",
    });
    const bigHeaderRes = await proxySecMod.readBoundedBody(bigHeaderReq);
    assert.equal(bigHeaderRes.ok, false);
    assert.equal(bigHeaderRes.status, 413);

    // Chunked stream without Content-Length exceeding 2 MiB rejected with 413
    const chunk = new Uint8Array(512 * 1024);
    let sent = 0;
    const stream = new ReadableStream({
      pull(controller) {
        if (sent >= 5) {
          controller.close();
          return;
        }
        sent += 1;
        controller.enqueue(chunk);
      },
    });
    const chunkedReq = new Request("http://localhost:3000/api/gw/test", {
      method: "POST",
      duplex: "half",
      body: stream,
    });
    const chunkedRes = await proxySecMod.readBoundedBody(chunkedReq);
    assert.equal(chunkedRes.ok, false);
    assert.equal(chunkedRes.status, 413);

    const gwRouteSrc = readFileSync(resolve(WEB_SRC, "app/api/gw/[...path]/route.ts"), "utf8");
    assert.ok(
      gwRouteSrc.includes("readBoundedBody") && gwRouteSrc.includes("413"),
      "/api/gw/[...path]/route.ts must enforce readBoundedBody and return 413"
    );
  });

  test("9. [Review #4] next.config.ts security headers + /dat-lai-mat-khau strips token via history.replaceState", () => {
    const nextCfgSrc = readFileSync(resolve(WEB_ROOT, "next.config.ts"), "utf8");
    assert.ok(nextCfgSrc.includes("async headers()"), "next.config.ts must define async headers()");
    assert.ok(
      nextCfgSrc.includes("X-Content-Type-Options") && nextCfgSrc.includes("nosniff"),
      "next.config.ts must set X-Content-Type-Options: nosniff"
    );
    assert.ok(
      nextCfgSrc.includes("X-Frame-Options") && nextCfgSrc.includes("DENY"),
      "next.config.ts must set X-Frame-Options: DENY"
    );
    assert.ok(
      nextCfgSrc.includes("Referrer-Policy") &&
        nextCfgSrc.includes("strict-origin-when-cross-origin"),
      "next.config.ts must set Referrer-Policy: strict-origin-when-cross-origin"
    );
    assert.ok(
      nextCfgSrc.includes("/dat-lai-mat-khau") && nextCfgSrc.includes("no-referrer"),
      "next.config.ts must set Referrer-Policy: no-referrer on /dat-lai-mat-khau"
    );
    assert.equal(
      nextCfgSrc.includes("Content-Security-Policy"),
      false,
      "next.config.ts must NOT add site-wide Content-Security-Policy in Wave 2a"
    );

    const resetSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/dat-lai-mat-khau/page.tsx"),
      "utf8"
    );
    assert.ok(
      resetSrc.includes("history.replaceState"),
      "/dat-lai-mat-khau must strip token from URL via history.replaceState"
    );
    assert.equal(
      /console\.(log|info|warn|error|debug)\s*\([^)]*token/i.test(resetSrc),
      false,
      "/dat-lai-mat-khau must never log reset token"
    );
  });

  test("10. [Review #5] External evidence URLs allow only http/https, use noopener noreferrer nofollow, and display hostname", () => {
    assert.equal(
      typeof orderMetaMod.parseSafeEvidenceUrl,
      "function",
      "parseSafeEvidenceUrl must be exported from orderMeta.ts"
    );
    assert.deepEqual(orderMetaMod.parseSafeEvidenceUrl("https://CDN.Example.com/img/1.jpg"), {
      href: "https://cdn.example.com/img/1.jpg",
      hostname: "cdn.example.com",
    });
    assert.deepEqual(orderMetaMod.parseSafeEvidenceUrl("http://files.test:8080/a.png"), {
      href: "http://files.test:8080/a.png",
      hostname: "files.test",
    });
    for (const bad of [
      "javascript:alert(1)",
      "data:text/html,<script>",
      "ftp://example.com/a.jpg",
      "//example.com/a.jpg",
      "not-a-url",
      "",
    ]) {
      assert.equal(
        orderMetaMod.parseSafeEvidenceUrl(bad),
        null,
        `unsafe evidence URL must return null: ${bad}`
      );
    }

    for (const rel of [
      "features/account/OrderDetailClient.tsx",
      "app/(seller)/seller/tra-hang/page.tsx",
      "app/(admin)/admin/khieu-nai/page.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(src.includes("parseSafeEvidenceUrl"), `${rel} must use parseSafeEvidenceUrl`);
      assert.ok(
        src.includes('rel="noopener noreferrer nofollow"'),
        `${rel} evidence links must set rel="noopener noreferrer nofollow"`
      );
      assert.ok(src.includes(".hostname"), `${rel} must display evidence URL hostname`);
    }
  });

  test("11. [Edge #6] error-map accurately maps redelivery states, invalid or expired otp, and empty lock reason", () => {
    // 1. Redelivery: not in failed status (409) must NOT be misreported as max 3 attempts
    const notFailed = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "shipment is not in failed status for redelivery",
      },
    });
    assert.ok(
      /thất bại/i.test(notFailed.message) && !/tối đa/i.test(notFailed.message),
      `409 non-failed redelivery must explain failed-status requirement, got: ${notFailed.message}`
    );

    // 2. Redelivery: already requested for current failed attempt (Vietnamese message from shipping)
    const alreadyReq = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "đã có yêu cầu giao lại cho lần giao thất bại hiện tại",
      },
    });
    assert.ok(
      /đã.*yêu cầu giao lại/i.test(alreadyReq.message),
      `already-requested redelivery must not fall back to generic error, got: ${alreadyReq.message}`
    );

    // 3. Redelivery: max attempts reached
    const maxAttempts = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "redelivery max attempts limit reached (3)",
      },
    });
    assert.ok(
      /tối đa.*3/i.test(maxAttempts.message),
      `max-attempts redelivery must mention max 3 attempts, got: ${maxAttempts.message}`
    );

    // 4. Verify-OTP: "invalid or expired otp"
    const badOtp = errorMapMod.sanitizeGatewayError(400, {
      error: { code: "INVALID_ARGUMENT", message: "invalid or expired otp" },
    });
    assert.ok(
      /mã xác thực/i.test(badOtp.message) && /hết hạn/i.test(badOtp.message),
      `invalid or expired otp must map to OTP message, got: ${badOtp.message}`
    );

    // 5. Locked account without lock_reason must NOT treat contact boilerplate as "Lý do:"
    const emptyLock = errorMapMod.sanitizeGatewayError(403, {
      error: {
        code: "PERMISSION_DENIED",
        message: "Tài khoản đã bị khóa. Vui lòng liên hệ hỗ trợ support@nexusmart.vn để được xử lý.",
      },
    });
    assert.equal(
      /Lý do:/i.test(emptyLock.message),
      false,
      `empty lock reason must not show 'Lý do: Vui lòng liên hệ...', got: ${emptyLock.message}`
    );
    assert.ok(
      /bị khóa/i.test(emptyLock.message) && /liên hệ/i.test(emptyLock.message),
      `empty lock reason must still state locked + contact guidance, got: ${emptyLock.message}`
    );
  });

  test("12. [Edge #9] /dang-nhap?expired=1 waits for SessionProvider loading before calling /api/auth/logout", () => {
    const loginSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/dang-nhap/page.tsx"), "utf8");
    assert.ok(
      /useSession\(\)/.test(loginSrc) && /loading/.test(loginSrc),
      "/dang-nhap must read loading from useSession()"
    );
    assert.ok(
      /if\s*\(\s*!expiredParam\s*\|\|\s*(?:loading|sessionLoading)\s*\|\|\s*isAuthenticated\s*\)\s*return;/.test(
        loginSrc
      ),
      "/dang-nhap?expired=1 effect must guard with loading before calling /api/auth/logout"
    );
  });

  test("13. [Edge #13] /admin/audit-log provides Vietnamese labels and filters for all gateway AdminAudit target_types", () => {
    assert.equal(
      typeof adminTypesMod.getAdminAuditTargetTypeLabel,
      "function",
      "getAdminAuditTargetTypeLabel must be exported from admin/types.ts"
    );
    const allTargetTypes = [
      "shop",
      "user",
      "product",
      "category",
      "voucher",
      "flash_sale",
      "flash_sale_item",
      "coin",
      "payment",
      "refund",
      "reconciliation_item",
      "return_request",
      "carrier",
    ];
    for (const tt of allTargetTypes) {
      const label = adminTypesMod.getAdminAuditTargetTypeLabel(tt);
      assert.ok(label && label !== tt, `target_type ${tt} must have a Vietnamese label, got ${label}`);
      assert.equal(label.includes("_"), false, `label for ${tt} must not be raw snake_case: ${label}`);
    }

    const auditSrc = readFileSync(resolve(WEB_SRC, "app/(admin)/admin/audit-log/page.tsx"), "utf8");
    assert.ok(
      auditSrc.includes("getAdminAuditTargetTypeLabel") &&
        auditSrc.includes("ADMIN_AUDIT_TARGET_TYPE_LABELS"),
      "/admin/audit-log must use shared Vietnamese target_type labels and filter options"
    );
  });

  test("14. [Ponytail web] removes dead queue status aliases and duplicate mock carriers table in /admin/cau-hinh", () => {
    // Dead aliases must fall back to neutral default instead of matching invented states
    for (const dead of ["open", "unresolved", "suspend", "assign", "resolved", "refund"]) {
      const meta = adminTypesMod.getReconciliationQueueStatusMeta(dead);
      assert.equal(
        meta.label,
        "Đang cập nhật",
        `dead alias "${dead}" must be removed from getReconciliationQueueStatusMeta`
      );
    }

    const doiSoatSrc = readFileSync(resolve(WEB_SRC, "app/(admin)/admin/doi-soat/page.tsx"), "utf8");
    assert.equal(
      /s\s*===\s*"open"|s\s*===\s*"unresolved"|s\s*===\s*"suspend"/.test(doiSoatSrc),
      false,
      "isQueueItemActionable in /admin/doi-soat must not check nonexistent status aliases"
    );

    const cauHinhSrc = readFileSync(resolve(WEB_SRC, "app/(admin)/admin/cau-hinh/page.tsx"), "utf8");
    assert.equal(
      cauHinhSrc.includes("cfg-carriers-heading") || cauHinhSrc.includes("carriersDraft"),
      false,
      "/admin/cau-hinh must remove duplicate mock carriers section in tab dat_hang"
    );
  });
});
