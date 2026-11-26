import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
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

const redirectMod = await import("../src/lib/redirect.ts");
const sellerTypesMod = await import("../src/features/seller/types.ts");
const checkoutMessagesMod = await import("../src/features/checkout/messages.ts");
const catalogMapMod = await import("../src/lib/catalog-map.ts");
const orderMetaMod = await import("../src/features/account/orderMeta.ts");

let checkoutHelpersMod = null;
try {
  checkoutHelpersMod = await import("../src/features/checkout/helpers.ts");
} catch {
  checkoutHelpersMod = null;
}

describe("Wave 1 web audit fixes — unit tests", () => {
  test("1. WEB-B2 & WEB-B3: catalog-map & storefront PDP omit out-of-scope Deal sốc / Combo / Mua 2 giảm", () => {
    const mapped = catalogMapMod.mergeProducts(
      [
        {
          id: "p-1",
          shop_id: "s-1",
          category_id: "c-1",
          name: "Combo 3 Quần lót Boxer Sợi Modal",
          slug: "combo-3-quan-lot-boxer-modal",
          description: "Hộp 3 chiếc",
          images: ["/products/do-lot-boxer-modal-3.png"],
          status: "APPROVED",
          skus: [
            {
              id: "sku-1",
              sku_code: "BD-DL14-L",
              name: "Combo Xám / Than / Đen / Size L",
              attributes: {
                color: "Combo Xám / Than / Đen",
                color_code: "combo-trungtinh",
                size: "L",
              },
              price_vnd: 229000,
              available_stock: 10,
              is_active: true,
            },
          ],
        },
      ],
      {
        content: [],
        categories: {
          categories: [
            {
              id: "c-1",
              slug: "do-lot",
              name: "Đồ lót",
              description: "",
              featuredImage: "",
              productCount: 1,
            },
          ],
          slugById: new Map([["c-1", "do-lot"]]),
        },
        shopIdByBackend: new Map(),
      }
    );
    assert.equal(mapped.length, 1);
    assert.equal(mapped[0].backendId, "p-1");
    assert.equal(mapped[0].price, 229000);

    const pdpSrc = readFileSync(resolve(WEB_SRC, "features/site/SitePdpView.tsx"), "utf8");
    assert.ok(!/deal\s*sốc/i.test(pdpSrc), "SitePdpView must not contain Deal sốc");
    assert.ok(!/tierDiscounts/.test(pdpSrc), "SitePdpView must not render tierDiscounts");
    assert.ok(!/giftThreshold/.test(pdpSrc), "SitePdpView must not render giftThreshold");
    assert.ok(!/comboSlots/.test(pdpSrc), "SitePdpView must not render comboSlots");
    assert.ok(!/PdpBundleSection/.test(pdpSrc), "SitePdpView must not render PdpBundleSection");
  });

  test("2. R7 (SHP-B1, WEB-B1): shipping calculate-fee payload omits empty from_province_code/from_ward_code", () => {
    assert.ok(
      checkoutHelpersMod && typeof checkoutHelpersMod.buildCalculateFeePayload === "function",
      "buildCalculateFeePayload helper must exist in web/src/features/checkout/helpers.ts"
    );

    const payload = checkoutHelpersMod.buildCalculateFeePayload({
      shopId: "shop-uuid-1",
      toProvinceCode: "01",
      toWardCode: "00001",
      weightGrams: 250,
      codAmountVnd: 179000,
    });

    assert.equal("from_province_code" in payload, false, "must not send from_province_code");
    assert.equal("from_ward_code" in payload, false, "must not send from_ward_code");
    assert.deepEqual(payload, {
      shop_id: "shop-uuid-1",
      to_province_code: "01",
      to_ward_code: "00001",
      weight_grams: 250,
      cod_amount_vnd: 179000,
    });

    const cartSrc = readFileSync(
      resolve(WEB_SRC, "app/(site)/s/[host]/gio-hang/CartClient.tsx"),
      "utf8"
    );
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(site)/s/[host]/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    const shippingQuoteBffSrc = readFileSync(
      resolve(WEB_SRC, "app/api/checkout/shipping-quote/route.ts"),
      "utf8"
    );
    assert.ok(
      !/from_province_code\s*:\s*""/.test(cartSrc),
      "CartClient must not send empty from_province_code"
    );
    assert.ok(
      !/from_ward_code\s*:\s*""/.test(cartSrc),
      "CartClient must not send empty from_ward_code"
    );
    assert.ok(
      !/from_province_code\s*:\s*""/.test(checkoutSrc),
      "CheckoutClient must not send empty from_province_code"
    );
    assert.ok(
      !/from_ward_code\s*:\s*""/.test(checkoutSrc),
      "CheckoutClient must not send empty from_ward_code"
    );
    assert.ok(
      !/from_province_code\s*:\s*""/.test(shippingQuoteBffSrc) &&
        !/from_ward_code\s*:\s*""/.test(shippingQuoteBffSrc),
      "shipping-quote BFF must not send empty from_province_code/from_ward_code"
    );
  });

  test("3. CAT-B5 & WEB-B6: seller edit form status options disallow HIDDEN when product is PENDING", () => {
    assert.equal(
      typeof sellerTypesMod.getAllowedSellerStatuses,
      "function",
      "getAllowedSellerStatuses must be exported from web/src/features/seller/types.ts"
    );

    const pendingOpts = sellerTypesMod.getAllowedSellerStatuses("PENDING");
    assert.deepEqual(
      pendingOpts.map((o) => o.value),
      ["PENDING"],
      "PENDING product must only allow PENDING (never HIDDEN)"
    );

    const approvedOpts = sellerTypesMod.getAllowedSellerStatuses("APPROVED");
    assert.deepEqual(
      approvedOpts.map((o) => o.value),
      ["APPROVED", "HIDDEN"]
    );

    const hiddenOpts = sellerTypesMod.getAllowedSellerStatuses("HIDDEN");
    assert.deepEqual(
      hiddenOpts.map((o) => o.value),
      ["HIDDEN", "APPROVED"]
    );

    const rejectedOpts = sellerTypesMod.getAllowedSellerStatuses("REJECTED");
    assert.deepEqual(
      rejectedOpts.map((o) => o.value),
      ["REJECTED", "PENDING"]
    );
  });

  test("4. PAY-B10 & ORD-B11: EXPIRED payment cannot be retried and copy guides user to reorder", () => {
    assert.ok(
      checkoutHelpersMod && typeof checkoutHelpersMod.canRetryPayment === "function",
      "canRetryPayment helper must exist in web/src/features/checkout/helpers.ts"
    );

    assert.equal(
      checkoutHelpersMod.canRetryPayment("EXPIRED"),
      false,
      "EXPIRED must not be retryable"
    );
    assert.equal(
      checkoutHelpersMod.canRetryPayment("PENDING", true),
      false,
      "PENDING with expired countdown must not be retryable"
    );
    assert.equal(checkoutHelpersMod.canRetryPayment("PENDING", false), true);
    assert.equal(checkoutHelpersMod.canRetryPayment("FAILED"), true);
    assert.equal(checkoutHelpersMod.canRetryPayment("CANCELLED"), false);
    assert.equal(checkoutHelpersMod.canRetryPayment("SUCCEEDED"), false);

    const expiredView = checkoutMessagesMod.getPaymentStatusView("EXPIRED");
    assert.ok(
      !/tạo lại mã|thanh toán lại/i.test(expiredView.description),
      `EXPIRED description must not suggest retrying QR: ${expiredView.description}`
    );
    assert.ok(
      /đặt lại|đơn mới/i.test(expiredView.description),
      `EXPIRED description must guide user to reorder: ${expiredView.description}`
    );

    const waitingSrc = readFileSync(
      resolve(WEB_SRC, "app/(site)/s/[host]/thanh-toan/cho/[id]/PaymentWaitingClient.tsx"),
      "utf8"
    );
    assert.ok(
      !/hết hạn[\s\S]{0,80}tạo lại mã/i.test(waitingSrc),
      "PaymentWaitingClient must not tell user to retry QR after expiry"
    );
  });

  test("5. WEB-B9: Buy Now flash sale resolves live valid items only and matches backend pricing", () => {
    assert.ok(
      checkoutHelpersMod &&
        typeof checkoutHelpersMod.resolveLiveFlashSaleItem === "function" &&
        typeof checkoutHelpersMod.computeBuyNowSlicePricing === "function",
      "resolveLiveFlashSaleItem and computeBuyNowSlicePricing must exist in checkout/helpers.ts"
    );

    const nowMs = Date.parse("2026-11-22T18:30:00Z");
    const flashSales = [
      {
        id: "fs-live",
        name: "Live Slot",
        starts_at: "2026-11-22T17:00:00Z",
        ends_at: "2026-11-23T06:00:00Z",
        items: [
          {
            id: "fi-live",
            flash_sale_id: "fs-live",
            shop_id: "shop-1",
            sku_id: "sku-1",
            flash_price_vnd: 149000,
            total_slots: 100,
            remaining_slots: 10,
            per_user_limit: 2,
            status: "APPROVED",
            rejection_reason: "",
          },
          {
            id: "fi-soldout",
            flash_sale_id: "fs-live",
            shop_id: "shop-1",
            sku_id: "sku-1",
            flash_price_vnd: 149000,
            total_slots: 10,
            remaining_slots: 0,
            per_user_limit: 2,
            status: "APPROVED",
            rejection_reason: "",
          },
        ],
      },
      {
        id: "fs-upcoming",
        name: "Upcoming Slot",
        starts_at: "2026-11-23T06:00:00Z",
        ends_at: "2026-11-23T12:00:00Z",
        items: [
          {
            id: "fi-upcoming",
            flash_sale_id: "fs-upcoming",
            shop_id: "shop-1",
            sku_id: "sku-1",
            flash_price_vnd: 129000,
            total_slots: 50,
            remaining_slots: 50,
            per_user_limit: 2,
            status: "APPROVED",
            rejection_reason: "",
          },
        ],
      },
    ];

    const liveMatch = checkoutHelpersMod.resolveLiveFlashSaleItem(
      flashSales,
      "fi-live",
      "sku-1",
      179000,
      nowMs
    );
    assert.ok(liveMatch, "valid live flash item must resolve");
    assert.equal(liveMatch.flashPriceVnd, 149000);
    assert.equal(liveMatch.perUserLimit, 2);

    assert.equal(
      checkoutHelpersMod.resolveLiveFlashSaleItem(
        flashSales,
        "fi-upcoming",
        "sku-1",
        179000,
        nowMs
      ),
      null,
      "upcoming flash sale item must NOT resolve"
    );
    assert.equal(
      checkoutHelpersMod.resolveLiveFlashSaleItem(
        flashSales,
        "fi-live",
        "sku-other",
        179000,
        nowMs
      ),
      null,
      "mismatched SKU must NOT resolve"
    );
    assert.equal(
      checkoutHelpersMod.resolveLiveFlashSaleItem(flashSales, "fi-soldout", "sku-1", 179000, nowMs),
      null,
      "sold-out flash item must NOT resolve"
    );

    const pricing = checkoutHelpersMod.computeBuyNowSlicePricing({
      unitPriceVnd: 179000,
      flashSaleItemId: "fi-live",
      flashPriceVnd: 149000,
      quantity: 2,
    });
    assert.deepEqual(pricing, {
      regularSubtotalVnd: 358000,
      flashDiscountVnd: 60000,
      effectiveUnitPriceVnd: 149000,
      effectiveSubtotalVnd: 298000,
    });
  });

  test("6. WEB-B4: post-login redirect routes seller -> /seller, admin/super_admin -> /admin, buyer -> next or /", () => {
    assert.equal(
      typeof redirectMod.resolvePostLoginRedirect,
      "function",
      "resolvePostLoginRedirect must be exported from web/src/lib/redirect.ts"
    );

    assert.equal(redirectMod.resolvePostLoginRedirect("seller", null), "/seller");
    assert.equal(redirectMod.resolvePostLoginRedirect("seller", "/"), "/seller");
    assert.equal(
      redirectMod.resolvePostLoginRedirect("seller", "/seller/don-hang"),
      "/seller/don-hang"
    );
    assert.equal(redirectMod.resolvePostLoginRedirect("seller", "//evil.com"), "/seller");

    assert.equal(redirectMod.resolvePostLoginRedirect("admin", null), "/admin");
    assert.equal(redirectMod.resolvePostLoginRedirect("super_admin", null), "/admin");
    assert.equal(
      redirectMod.resolvePostLoginRedirect("admin", "/admin/doi-soat"),
      "/admin/doi-soat"
    );
    assert.equal(redirectMod.resolvePostLoginRedirect("admin", "/"), "/admin");

    assert.equal(redirectMod.resolvePostLoginRedirect("buyer", null), "/");
    assert.equal(redirectMod.resolvePostLoginRedirect("buyer", "/thanh-toan"), "/thanh-toan");
    assert.equal(redirectMod.resolvePostLoginRedirect("buyer", "/seller"), "/");
    assert.equal(redirectMod.resolvePostLoginRedirect("buyer", "/admin"), "/");
  });

  test("7. SHP-B2: seller ship form defaults to the order's carrier_code and service_code instead of hardcoded ghn/STANDARD", () => {
    assert.equal(
      typeof sellerTypesMod.resolveOrderShippingDefaults,
      "function",
      "resolveOrderShippingDefaults must be exported from web/src/features/seller/types.ts"
    );

    assert.deepEqual(
      sellerTypesMod.resolveOrderShippingDefaults({
        carrier_code: "mock_express",
        service_code: "STANDARD",
      }),
      { carrierCode: "mock_express", serviceCode: "STANDARD" }
    );

    assert.deepEqual(
      sellerTypesMod.resolveOrderShippingDefaults({
        carrier_code: "MOCK_EXPRESS",
        service_code: "fast",
      }),
      { carrierCode: "mock_express", serviceCode: "FAST" }
    );

    assert.deepEqual(
      sellerTypesMod.resolveOrderShippingDefaults({
        carrier_code: "ghn",
        service_code: "EXPRESS",
      }),
      { carrierCode: "ghn", serviceCode: "EXPRESS" }
    );

    assert.deepEqual(
      sellerTypesMod.resolveOrderShippingDefaults({
        carrier_code: "",
        service_code: "",
      }),
      { carrierCode: "ghn", serviceCode: "STANDARD" }
    );
  });

  test("8. review-C P1-2: shop registration requires pickup_address_id + license, aligns VN phone regex, and allows REQUEST_INFO resubmission", () => {
    assert.equal(
      typeof orderMetaMod.isValidVietnamesePhone,
      "function",
      "isValidVietnamesePhone must be exported from web/src/features/account/orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.canSubmitShopApplication,
      "function",
      "canSubmitShopApplication must be exported from web/src/features/account/orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.isShopInfoRequested,
      "function",
      "isShopInfoRequested must be exported from web/src/features/account/orderMeta.ts"
    );

    // Valid Vietnamese numbers matching services/user isValidVNPhone
    for (const valid of [
      "0912345678",
      "0381234567",
      "0561234567",
      "0701234567",
      "0811234567",
      "+84912345678",
      "84912345678",
      "02438123456",
      "+842838123456",
      "0912 345-678",
      "0912.345.678",
    ]) {
      assert.equal(
        orderMetaMod.isValidVietnamesePhone(valid),
        true,
        `expected ${valid} to be a valid VN phone`
      );
    }

    // Invalid numbers that old /^0\d{9,10}$/ wrongly accepted or backend rejects
    for (const invalid of [
      "0123456789",
      "0000000000",
      "0412345678",
      "0612345678",
      "091234567",
      "09123456789",
      "0243812345",
      "",
      "abc",
    ]) {
      assert.equal(
        orderMetaMod.isValidVietnamesePhone(invalid),
        false,
        `expected ${invalid} to be rejected as invalid VN phone`
      );
    }

    // Resubmission eligibility matching services/user RegisterShop
    assert.equal(orderMetaMod.canSubmitShopApplication(null), true);
    assert.equal(
      orderMetaMod.canSubmitShopApplication({
        status: "REJECTED",
        rejection_reason: "Thiếu hồ sơ",
      }),
      true
    );
    assert.equal(
      orderMetaMod.canSubmitShopApplication({
        status: "PENDING",
        rejection_reason: "Bổ sung ảnh GPKD",
      }),
      true,
      "PENDING shop with rejection_reason (REQUEST_INFO) must be resubmittable"
    );
    assert.equal(
      orderMetaMod.canSubmitShopApplication({ status: "PENDING", rejection_reason: "" }),
      false,
      "fresh PENDING shop without rejection_reason must not show resubmission form"
    );
    assert.equal(
      orderMetaMod.canSubmitShopApplication({ status: "ACTIVE", rejection_reason: "" }),
      false
    );
    assert.equal(
      orderMetaMod.canSubmitShopApplication({ status: "SUSPENDED", rejection_reason: "Vi phạm" }),
      false
    );

    assert.equal(
      orderMetaMod.isShopInfoRequested({ status: "PENDING", rejection_reason: "Bổ sung GPKD" }),
      true
    );
    assert.equal(
      orderMetaMod.isShopInfoRequested({ status: "PENDING", rejection_reason: "   " }),
      false
    );
    assert.equal(
      orderMetaMod.isShopInfoRequested({ status: "REJECTED", rejection_reason: "Từ chối" }),
      false
    );

    const requestInfoMeta = orderMetaMod.getShopStatusInfo("PENDING", "Bổ sung GPKD");
    assert.equal(requestInfoMeta.label, "Yêu cầu bổ sung");
    const plainPendingMeta = orderMetaMod.getShopStatusInfo("PENDING", "");
    assert.equal(plainPendingMeta.label, "Đang chờ duyệt");

    const accountSrc = readFileSync(
      resolve(WEB_SRC, "features/account/AccountClientView.tsx"),
      "utf8"
    );
    assert.equal(
      accountSrc.includes("Mã giấy phép kinh doanh (không bắt buộc)"),
      false,
      "AccountClientView must not mark business_license_doc as optional"
    );
    assert.equal(
      accountSrc.includes(">Chọn sau<"),
      false,
      "AccountClientView must not offer empty 'Chọn sau' pickup_address_id option"
    );
    assert.equal(
      accountSrc.includes("Bạn vẫn có thể gửi hồ sơ"),
      false,
      "AccountClientView must not tell buyer they can submit without a pickup address"
    );
  });

  test("9. review-C P2-6: resolveOrderShippingDefaults falls back to carrier default service when service_code is empty string", () => {
    assert.deepEqual(
      sellerTypesMod.resolveOrderShippingDefaults({
        carrier_code: "ghn",
        service_code: "",
      }),
      { carrierCode: "ghn", serviceCode: "STANDARD" },
      "empty service_code with known carrier must fall back to carrier's default service"
    );

    assert.deepEqual(
      sellerTypesMod.resolveOrderShippingDefaults({
        carrier_code: "mock_express",
        service_code: "   ",
      }),
      { carrierCode: "mock_express", serviceCode: "STANDARD" }
    );

    const options = sellerTypesMod.buildCarrierSelectOptions({
      carrier_code: "ghn",
      service_code: "",
    });
    const ghn = options.find((c) => c.carrierCode === "ghn");
    assert.ok(ghn, "expected ghn carrier option");
    assert.equal(
      ghn.services.some((s) => s.serviceCode === ""),
      false,
      `buildCarrierSelectOptions must never emit an empty serviceCode option, got ${JSON.stringify(ghn.services)}`
    );
  });

  test("10. review-C P2-7: expired payment cannot be retried when FAILED or via needsVietQrPayment", () => {
    assert.equal(
      checkoutHelpersMod.canRetryPayment("FAILED", true),
      false,
      "FAILED payment with expired deadline must not be retryable"
    );
    assert.equal(
      checkoutHelpersMod.canRetryPayment("FAILED", false),
      true,
      "FAILED payment before deadline remains retryable"
    );
    assert.equal(checkoutHelpersMod.canRetryPayment("PENDING", true), false);

    assert.equal(
      typeof checkoutHelpersMod.isPaymentDeadlinePassed,
      "function",
      "isPaymentDeadlinePassed must be exported from web/src/features/checkout/helpers.ts"
    );
    const nowMs = Date.parse("2026-11-22T12:00:00Z");
    assert.equal(checkoutHelpersMod.isPaymentDeadlinePassed("2026-11-22T11:59:59Z", nowMs), true);
    assert.equal(checkoutHelpersMod.isPaymentDeadlinePassed("2026-11-22T12:00:00Z", nowMs), true);
    assert.equal(checkoutHelpersMod.isPaymentDeadlinePassed("2026-11-22T12:00:01Z", nowMs), false);
    assert.equal(checkoutHelpersMod.isPaymentDeadlinePassed("", nowMs), false);

    const baseOrder = {
      status: "PENDING",
      payment_method: "VIETQR",
      payment_id: "pay-1",
      payment_status: "PENDING",
    };
    assert.equal(
      orderMetaMod.needsVietQrPayment(
        { ...baseOrder, payment_expires_at: "2026-11-22T11:55:00Z" },
        nowMs
      ),
      false,
      "needsVietQrPayment must return false when payment_expires_at has passed"
    );
    assert.equal(
      orderMetaMod.needsVietQrPayment(
        { ...baseOrder, payment_status: "FAILED", payment_expires_at: "2026-11-22T11:55:00Z" },
        nowMs
      ),
      false,
      "needsVietQrPayment must return false for expired FAILED payment"
    );
    assert.equal(
      orderMetaMod.needsVietQrPayment(
        { ...baseOrder, payment_expires_at: "2026-11-22T12:10:00Z" },
        nowMs
      ),
      true,
      "needsVietQrPayment must return true before payment_expires_at"
    );

    const retryBoxSrc = readFileSync(
      resolve(WEB_SRC, "features/commerce/RetryPaymentBox.tsx"),
      "utf8"
    );
    assert.equal(
      /const countdownExpired = isPending &&/.test(retryBoxSrc),
      false,
      "RetryPaymentBox must track expiration for FAILED payments as well, not only isPending"
    );

    const ordersListSrc = readFileSync(
      resolve(WEB_SRC, "features/account/OrdersClientView.tsx"),
      "utf8"
    );
    assert.equal(
      ordersListSrc.includes("orders.filter(needsVietQrPayment)"),
      false,
      "OrdersClientView must not pass needsVietQrPayment directly to Array.filter (passes index as nowMs)"
    );
  });

  test("11. Round 3: unpaid multi-shop VietQR cancel warns about whole-group cancellation and ignores COD / paid orders", () => {
    assert.equal(
      typeof orderMetaMod.isUnpaidVietQrOrder,
      "function",
      "isUnpaidVietQrOrder must be exported from web/src/features/account/orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.resolveUnpaidVietQrCancelGroup,
      "function",
      "resolveUnpaidVietQrCancelGroup must be exported from web/src/features/account/orderMeta.ts"
    );
    assert.equal(
      typeof orderMetaMod.formatUnpaidGroupCancelWarning,
      "function",
      "formatUnpaidGroupCancelWarning must be exported from web/src/features/account/orderMeta.ts"
    );

    const unpaidShop1 = {
      id: "ord-1",
      order_code: "NM-001",
      checkout_group_id: "grp-multi",
      shop_id: "shop-1",
      status: "PENDING",
      payment_method: "VIETQR",
      payment_status: "PENDING",
    };
    const unpaidShop2 = {
      id: "ord-2",
      order_code: "NM-002",
      checkout_group_id: "grp-multi",
      shop_id: "shop-2",
      status: "PENDING",
      payment_method: "VIETQR",
      payment_status: "FAILED",
    };
    const cancelledSibling = {
      id: "ord-3",
      order_code: "NM-003",
      checkout_group_id: "grp-multi",
      shop_id: "shop-3",
      status: "CANCELLED",
      payment_method: "VIETQR",
      payment_status: "CANCELLED",
    };
    const otherGroup = {
      id: "ord-4",
      order_code: "NM-004",
      checkout_group_id: "grp-other",
      shop_id: "shop-4",
      status: "PENDING",
      payment_method: "VIETQR",
      payment_status: "PENDING",
    };

    assert.equal(orderMetaMod.isUnpaidVietQrOrder(unpaidShop1), true);
    assert.equal(orderMetaMod.isUnpaidVietQrOrder(unpaidShop2), true);
    assert.equal(orderMetaMod.isUnpaidVietQrOrder(cancelledSibling), false);
    assert.equal(
      orderMetaMod.isUnpaidVietQrOrder({
        status: "CONFIRMED",
        payment_method: "VIETQR",
        payment_status: "SUCCEEDED",
      }),
      false,
      "paid VietQR order must not be treated as unpaid"
    );
    assert.equal(
      orderMetaMod.isUnpaidVietQrOrder({
        status: "CONFIRMED",
        payment_method: "COD",
        payment_status: "PENDING",
      }),
      false,
      "COD order must not be treated as unpaid VietQR"
    );

    const multi = orderMetaMod.resolveUnpaidVietQrCancelGroup(unpaidShop1, [
      unpaidShop1,
      unpaidShop2,
      cancelledSibling,
      otherGroup,
    ]);
    assert.equal(multi.isMultiShopGroup, true);
    assert.equal(multi.shopCount, 2);
    assert.deepEqual(
      multi.groupOrders.map((o) => o.id),
      ["ord-1", "ord-2"]
    );

    const single = orderMetaMod.resolveUnpaidVietQrCancelGroup(otherGroup, [
      unpaidShop1,
      unpaidShop2,
      otherGroup,
    ]);
    assert.equal(single.isMultiShopGroup, false);
    assert.equal(single.shopCount, 1);

    const cod1 = {
      id: "cod-1",
      order_code: "NM-C1",
      checkout_group_id: "grp-cod",
      shop_id: "shop-1",
      status: "CONFIRMED",
      payment_method: "COD",
      payment_status: "PENDING",
    };
    const cod2 = { ...cod1, id: "cod-2", order_code: "NM-C2", shop_id: "shop-2" };
    assert.equal(
      orderMetaMod.resolveUnpaidVietQrCancelGroup(cod1, [cod1, cod2]).isMultiShopGroup,
      false,
      "COD multi-shop group keeps single sub-order cancellation"
    );

    const paid1 = {
      id: "paid-1",
      order_code: "NM-P1",
      checkout_group_id: "grp-paid",
      shop_id: "shop-1",
      status: "CONFIRMED",
      payment_method: "VIETQR",
      payment_status: "SUCCEEDED",
    };
    const paid2 = { ...paid1, id: "paid-2", order_code: "NM-P2", shop_id: "shop-2" };
    assert.equal(
      orderMetaMod.resolveUnpaidVietQrCancelGroup(paid1, [paid1, paid2]).isMultiShopGroup,
      false,
      "paid VietQR multi-shop group keeps single sub-order cancellation"
    );

    assert.equal(
      orderMetaMod.formatUnpaidGroupCancelWarning(2),
      "Hủy đơn chưa thanh toán sẽ hủy toàn bộ lần đặt hàng gồm 2 gian hàng"
    );
    assert.equal(
      orderMetaMod.formatUnpaidGroupCancelWarning(3),
      "Hủy đơn chưa thanh toán sẽ hủy toàn bộ lần đặt hàng gồm 3 gian hàng"
    );
  });

  test("12. Server pages/layouts calling requireUser or requireRole handle GatewayError (429/5xx) gracefully instead of HTTP 500", () => {
    function collectAppServerFiles(dir) {
      const results = [];
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const full = resolve(dir, entry.name);
        if (entry.isDirectory()) {
          if (entry.name === "api") continue;
          results.push(...collectAppServerFiles(full));
        } else if (entry.name === "page.tsx" || entry.name === "layout.tsx") {
          results.push(full);
        }
      }
      return results;
    }

    const appFiles = collectAppServerFiles(resolve(WEB_SRC, "app"));
    const guardedFiles = [];

    for (const filePath of appFiles) {
      const src = readFileSync(filePath, "utf8");
      if (!/\bawait\s+require(?:User|Role)\s*\(/.test(src)) continue;
      const rel = filePath.slice(WEB_SRC.length + 1);
      guardedFiles.push(rel);

      assert.ok(
        /\bGatewayError\b/.test(src),
        `${rel} calls requireUser/requireRole and must import/catch GatewayError`
      );
      assert.ok(
        /try\s*\{[\s\S]*?\bawait\s+require(?:User|Role)\s*\([\s\S]*?\}\s*catch\s*\([^)]+\)\s*\{[\s\S]*?instanceof\s+GatewayError/.test(
          src
        ),
        `${rel} must wrap await requireUser/requireRole in try/catch (err instanceof GatewayError)`
      );
    }

    assert.ok(
      guardedFiles.includes("app/(admin)/layout.tsx"),
      "expected (admin)/layout.tsx in audited server layouts"
    );
    const sellerLayoutSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/layout.tsx"), "utf8");
    assert.ok(
      /try\s*\{[\s\S]*?\bawait\s+requireSellerPortalSession\s*\([\s\S]*?\}\s*catch\s*\([^)]+\)\s*\{[\s\S]*?instanceof\s+GatewayError/.test(
        sellerLayoutSrc
      ),
      "app/(seller)/layout.tsx must wrap requireSellerPortalSession in try/catch (err instanceof GatewayError)"
    );

    // Tenant storefront checkout & waiting pages catch GatewayError gracefully
    for (const rel of [
      "app/(site)/s/[host]/thanh-toan/page.tsx",
      "app/(site)/s/[host]/thanh-toan/cho/[id]/page.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(src.includes("GatewayError"), `${rel} must catch GatewayError gracefully`);
    }

    const middlewareSrc = readFileSync(resolve(WEB_SRC, "middleware.ts"), "utf8");
    assert.ok(
      middlewareSrc.includes("rate_limited") &&
        middlewareSrc.includes("429") &&
        middlewareSrc.includes("Bạn thao tác quá nhanh"),
      "middleware.ts must preserve HTTP 429 with Vietnamese rate-limit message instead of converting to 503"
    );
  });

  test("13. W1/W5/W6/W7/W9 regressions: Node middleware, zero fake promo copy in data, CSP connect-src, admin 390px queue wrap, report >366d validation", async () => {
    // W1: middleware runs on Node runtime so globalThis refresh dedup is shared with route handlers
    const middlewareSrc = readFileSync(resolve(WEB_SRC, "middleware.ts"), "utf8");
    assert.match(middlewareSrc, /runtime:\s*["']nodejs["']/);

    // W5: data files & ProductCard do not leak legacy fake promo copy onto PDP/storefront
    const productsDataSrc = readFileSync(resolve(WEB_SRC, "data/products.ts"), "utf8");
    const vouchersDataSrc = readFileSync(resolve(WEB_SRC, "data/vouchers.ts"), "utf8");
    const flashDataSrc = readFileSync(resolve(WEB_SRC, "data/flash-sale.ts"), "utf8");
    const productCardSrc = readFileSync(resolve(WEB_SRC, "components/ProductCard.tsx"), "utf8");
    assert.equal(productsDataSrc.includes("tierDiscounts"), false);
    assert.equal(productsDataSrc.includes("Mua 2 giảm"), false);
    assert.equal(productsDataSrc.includes("giftThreshold"), false);
    assert.equal(productsDataSrc.includes("comboSlots"), false);
    assert.equal(vouchersDataSrc.includes("tierDiscounts"), false);
    assert.equal(vouchersDataSrc.includes("calculateShopTierDiscount"), false);
    assert.equal(/deal\s*sốc/i.test(flashDataSrc), false);
    assert.equal(productCardSrc.includes("Chọn size combo"), false);

    // W6: next.config.ts & runtime middleware CSP include connect-src allowing the public chat WebSocket origin
    const nextConfigMod = await import("../next.config.ts");
    const rules = await nextConfigMod.default.headers();
    const cspRule = rules.find((r) => r.headers.some((h) => h.key === "Content-Security-Policy"));
    const cspValue = cspRule?.headers.find((h) => h.key === "Content-Security-Policy")?.value ?? "";
    assert.match(cspValue, /connect-src 'self' wss?:\/\//);
    assert.equal(cspValue.includes("gateway-bff"), false);
    assert.ok(middlewareSrc.includes("buildSiteCspHeader"));

    // W7: /admin queue items wrap cleanly without horizontal overflow at 390px
    const adminOverviewSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminOverviewClient.tsx"),
      "utf8"
    );
    assert.ok(
      adminOverviewSrc.includes("whitespace-normal"),
      "/admin queue items must allow line wrapping (whitespace-normal)"
    );
    assert.ok(
      adminOverviewSrc.includes("[overflow-wrap:anywhere]") ||
        adminOverviewSrc.includes("break-words"),
      "/admin queue items must break long unbroken words without horizontal overflow"
    );

    // W9: seller & admin report JSON routes enforce validateReportDateRange (<= 366 days) & accept from/to aliases
    const formatMod = await import("../src/lib/format.ts");
    assert.equal(formatMod.validateReportDateRange("2025-01-01", "2026-01-01").ok, true);
    assert.equal(formatMod.validateReportDateRange("2025-01-01", "2026-01-02").ok, false);
    assert.equal(formatMod.validateReportDateRange("2026-02-31", "2026-03-01").ok, false);
    for (const rel of [
      "app/api/seller/reports/route.ts",
      "app/api/seller/reports/export/route.ts",
      "app/api/admin/reports/route.ts",
      "app/api/admin/reports/export/route.ts",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(
        src.includes("validateReportDateRange"),
        `${rel} must call validateReportDateRange`
      );
      assert.ok(src.includes('q.get("from")'), `${rel} must accept from/to query aliases`);
    }
  });
});
