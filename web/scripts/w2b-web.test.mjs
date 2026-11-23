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
const apiMod = await import("../src/lib/api.ts");
const catalogMapperMod = await import("../src/lib/catalog-mapper.ts");
const checkoutHelpersMod = await import("../src/features/checkout/helpers.ts");
const checkoutMessagesMod = await import("../src/features/checkout/messages.ts");
const orderMetaMod = await import("../src/features/account/orderMeta.ts");
const sellerTypesMod = await import("../src/features/seller/types.ts");
const errorMapMod = await import("../src/lib/server/error-map.ts");

function makeSampleGatewayProduct(overrides = {}) {
  return {
    id: "prod-uuid-1",
    shop_id: "shop-uuid-1",
    category_id: "cat-uuid-1",
    name: "Áo thun Cotton Compact 220gsm Trắng",
    slug: "ao-thun-cotton-compact-trang",
    description: "Áo thun cotton",
    images: ["/products/ao-thun-cotton-trang.png"],
    status: "APPROVED",
    rejection_reason: "",
    min_price_vnd: 179000,
    max_price_vnd: 219000,
    skus: [
      {
        id: "sku-m",
        product_id: "prod-uuid-1",
        shop_id: "shop-uuid-1",
        product_name: "Áo thun Cotton Compact 220gsm Trắng",
        sku_code: "BD-AT01-TRANG-M",
        name: "Trắng tinh / M",
        attributes: { color: "Trắng tinh", color_code: "trang", size: "M" },
        price_vnd: 179000,
        stock: 20,
        reserved_stock: 0,
        available_stock: 20,
        version: 1,
        weight_grams: 220,
        image_url: "/products/ao-thun-cotton-trang.png",
        category_id: "cat-uuid-1",
        commission_rate_bps: 500,
        is_active: true,
      },
      {
        id: "sku-xl",
        product_id: "prod-uuid-1",
        shop_id: "shop-uuid-1",
        product_name: "Áo thun Cotton Compact 220gsm Trắng",
        sku_code: "BD-AT01-TRANG-XL",
        name: "Trắng tinh / XL",
        attributes: { color: "Trắng tinh", color_code: "trang", size: "XL" },
        price_vnd: 219000,
        stock: 15,
        reserved_stock: 0,
        available_stock: 15,
        version: 1,
        weight_grams: 250,
        image_url: "/products/ao-thun-cotton-trang.png",
        category_id: "cat-uuid-1",
        commission_rate_bps: 500,
        is_active: true,
      },
    ],
    ...overrides,
  };
}

describe("Wave 2b web features — unit tests", () => {
  test("1. Real strikethrough price (no mock editorial.originalPrice), single-price rule (Flash > Shop Discount), and CAT-B13 price range", () => {
    const raw = makeSampleGatewayProduct();

    // Without active shop discount or live flash sale, editorial.originalPrice MUST NEVER be used
    const plain = catalogMapperMod.mapGatewayProduct(raw, {
      categoriesById: new Map(),
      shopsById: new Map(),
    });
    assert.equal(
      plain.originalPrice,
      undefined,
      "mapGatewayProduct must NOT fall back to mock editorial.originalPrice"
    );
    assert.equal(plain.minPriceVnd, 179000);
    assert.equal(plain.maxPriceVnd, 219000);

    // With active shop discount program on sku-m (179,000 -> 149,000)
    const activeShopDiscountsBySkuId = new Map([
      [
        "sku-m",
        {
          item_id: "prog-item-1",
          program_id: "prog-1",
          shop_id: "shop-uuid-1",
          sku_id: "sku-m",
          original_price_vnd: 179000,
          discounted_price_vnd: 149000,
          discount_percent: 17,
          per_user_limit: 2,
          ends_at: "2026-12-01T00:00:00Z",
        },
      ],
    ]);
    const withShopDiscount = catalogMapperMod.mapGatewayProduct(raw, {
      categoriesById: new Map(),
      shopsById: new Map(),
      activeShopDiscountsBySkuId,
    });
    assert.equal(withShopDiscount.price, 149000);
    assert.equal(withShopDiscount.minPriceVnd, 149000);
    assert.equal(withShopDiscount.maxPriceVnd, 219000);
    assert.equal(
      withShopDiscount.originalPrice,
      179000,
      "active shop discount must expose real originalPrice"
    );

    // Single-price rule: Live Flash Sale (129,000) wins over active shop discount (149,000)
    const liveFlashItemsBySkuId = new Map([
      [
        "sku-m",
        {
          id: "fi-live-1",
          flash_sale_id: "fs-1",
          shop_id: "shop-uuid-1",
          sku_id: "sku-m",
          flash_price_vnd: 129000,
          original_price_vnd: 179000,
          total_slots: 20,
          remaining_slots: 5,
          per_user_limit: 1,
          status: "APPROVED",
          rejection_reason: "",
        },
      ],
    ]);
    const withBoth = catalogMapperMod.mapGatewayProduct(raw, {
      categoriesById: new Map(),
      shopsById: new Map(),
      activeShopDiscountsBySkuId,
      liveFlashItemsBySkuId,
    });
    assert.equal(withBoth.price, 129000, "live flash price must override shop discount");
    assert.equal(withBoth.originalPrice, 179000);

    // Price.tsx supports maxAmount & ProductCard.tsx passes maxAmount for price range
    const priceSrc = readFileSync(resolve(WEB_SRC, "components/Price.tsx"), "utf8");
    assert.ok(priceSrc.includes("maxAmount"), "Price.tsx must support maxAmount price range");
    const cardSrc = readFileSync(resolve(WEB_SRC, "components/ProductCard.tsx"), "utf8");
    assert.ok(
      cardSrc.includes("maxAmount"),
      "ProductCard.tsx must pass maxAmount for multi-price SKUs"
    );
    assert.equal(
      cardSrc.includes("getFallbackSampleRating"),
      false,
      "ProductCard.tsx must not fabricate fallback star ratings on real products"
    );
  });

  test("2. Flash sale & shop discount checkout helpers: live-only check, sold-out fallback, single-price rule, COD block, voucher reasons", () => {
    assert.equal(typeof checkoutHelpersMod.resolveLiveFlashSaleForSku, "function");
    assert.equal(typeof checkoutHelpersMod.inspectFlashSaleStatusForSku, "function");
    assert.equal(typeof checkoutHelpersMod.resolveSkuEffectivePromotion, "function");
    assert.equal(typeof checkoutHelpersMod.isCodAllowedForCheckout, "function");

    const nowMs = Date.parse("2026-11-23T05:00:00Z");
    const flashSales = [
      {
        id: "fs-live",
        name: "Live",
        starts_at: "2026-11-23T04:00:00Z",
        ends_at: "2026-11-23T06:00:00Z",
        items: [
          {
            id: "fi-ok",
            flash_sale_id: "fs-live",
            shop_id: "s1",
            sku_id: "sku-ok",
            flash_price_vnd: 139000,
            original_price_vnd: 179000,
            total_slots: 10,
            remaining_slots: 3,
            per_user_limit: 2,
            status: "APPROVED",
            rejection_reason: "",
          },
          {
            id: "fi-soldout",
            flash_sale_id: "fs-live",
            shop_id: "s1",
            sku_id: "sku-soldout",
            flash_price_vnd: 139000,
            original_price_vnd: 179000,
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
        name: "Upcoming",
        starts_at: "2026-11-23T08:00:00Z",
        ends_at: "2026-11-23T10:00:00Z",
        items: [
          {
            id: "fi-up",
            flash_sale_id: "fs-upcoming",
            shop_id: "s1",
            sku_id: "sku-up",
            flash_price_vnd: 119000,
            original_price_vnd: 179000,
            total_slots: 10,
            remaining_slots: 10,
            per_user_limit: 2,
            status: "APPROVED",
            rejection_reason: "",
          },
        ],
      },
    ];

    const live = checkoutHelpersMod.resolveLiveFlashSaleForSku(flashSales, "sku-ok", 179000, nowMs);
    assert.ok(live);
    assert.equal(live.flashSaleItemId, "fi-ok");
    assert.equal(live.flashPriceVnd, 139000);

    assert.equal(
      checkoutHelpersMod.resolveLiveFlashSaleForSku(flashSales, "sku-soldout", 179000, nowMs),
      null
    );
    assert.equal(
      checkoutHelpersMod.resolveLiveFlashSaleForSku(flashSales, "sku-up", 179000, nowMs),
      null
    );

    assert.equal(
      checkoutHelpersMod.inspectFlashSaleStatusForSku(flashSales, "sku-soldout", undefined, nowMs)
        .state,
      "sold_out"
    );
    assert.equal(
      checkoutHelpersMod.inspectFlashSaleStatusForSku(flashSales, "sku-up", "fi-up", nowMs).state,
      "upcoming"
    );

    // Single-price rule (strict contract activeShopDiscountDTO with item_id)
    const shopDiscount = {
      item_id: "pi-1",
      program_id: "p1",
      shop_id: "s1",
      sku_id: "sku-ok",
      original_price_vnd: 179000,
      discounted_price_vnd: 155000,
      discount_percent: 13,
      per_user_limit: 5,
    };
    const winnerFlash = checkoutHelpersMod.resolveSkuEffectivePromotion({
      regularPriceVnd: 179000,
      liveFlash: live,
      activeShopDiscount: shopDiscount,
    });
    assert.equal(winnerFlash.kind, "flash");
    assert.equal(winnerFlash.effectiveUnitPriceVnd, 139000);
    assert.equal(winnerFlash.flashSaleItemId, "fi-ok");
    assert.equal(winnerFlash.shopProgramItemId, undefined);

    const winnerShop = checkoutHelpersMod.resolveSkuEffectivePromotion({
      regularPriceVnd: 179000,
      liveFlash: null,
      activeShopDiscount: shopDiscount,
    });
    assert.equal(winnerShop.kind, "shop_discount");
    assert.equal(winnerShop.effectiveUnitPriceVnd, 155000);
    assert.equal(winnerShop.shopProgramItemId, "pi-1");

    // Explicit opt-out of Flash Sale skips flash ONLY: falls back to active shop discount if present
    const optedOutWithShopDisc = checkoutHelpersMod.resolveSkuEffectivePromotion({
      regularPriceVnd: 179000,
      liveFlash: live,
      activeShopDiscount: shopDiscount,
      optOutFlash: true,
    });
    assert.equal(optedOutWithShopDisc.kind, "shop_discount");
    assert.equal(optedOutWithShopDisc.effectiveUnitPriceVnd, 155000);
    assert.equal(optedOutWithShopDisc.flashSaleItemId, undefined);
    assert.equal(optedOutWithShopDisc.shopProgramItemId, "pi-1");

    // Without active shop discount, opting out of Flash Sale reverts to regular price
    const optedOutPlain = checkoutHelpersMod.resolveSkuEffectivePromotion({
      regularPriceVnd: 179000,
      liveFlash: live,
      activeShopDiscount: null,
      optOutFlash: true,
    });
    assert.equal(optedOutPlain.kind, "none");
    assert.equal(optedOutPlain.effectiveUnitPriceVnd, 179000);
    assert.equal(optedOutPlain.flashSaleItemId, undefined);
    assert.equal(optedOutPlain.shopProgramItemId, undefined);

    // COD disallowed when any item has Flash Sale
    const codFlash = checkoutHelpersMod.isCodAllowedForCheckout({
      hasFlashSaleItem: true,
      totalPayableVnd: 200000,
      codRejectedCount: 0,
    });
    assert.equal(codFlash.allowed, false);
    assert.ok(/vietqr/i.test(codFlash.reason) && /5\s*phút/i.test(codFlash.reason));

    const codNormal = checkoutHelpersMod.isCodAllowedForCheckout({
      hasFlashSaleItem: false,
      totalPayableVnd: 200000,
      codRejectedCount: 0,
    });
    assert.equal(codNormal.allowed, true);

    // Voucher ineligible reasons
    assert.ok(/tạm dừng/i.test(checkoutMessagesMod.translateVoucherReason("voucher is PAUSED")));
    assert.ok(
      /danh mục/i.test(
        checkoutMessagesMod.translateVoucherReason("voucher not applicable for product category")
      )
    );
  });

  test("3. Seller Wave 2b helpers: deductions with shop program/freeship/payment fee, voucher edit lock, report date range, bps format, bank validation", () => {
    // Extended deductions formula (backward-compatible)
    const fullOrder = {
      subtotal_vnd: 500000,
      flash_sale_discount_vnd: 0,
      shop_program_discount_vnd: 40000,
      shop_voucher_discount_vnd: 20000,
      shop_freeship_discount_vnd: 15000,
      commission_vnd: 22000,
      payment_fee_vnd: 4400,
    };
    assert.equal(sellerTypesMod.computeOrderDeductionsVnd(fullOrder), 101400);
    assert.equal(sellerTypesMod.computeOrderNetVnd(fullOrder), 398600);

    // Voucher edit rules when used_count > 0
    assert.equal(typeof sellerTypesMod.canEditVoucherField, "function");
    const unusedVoucher = { used_count: 0 };
    const usedVoucher = { used_count: 3 };
    assert.equal(sellerTypesMod.canEditVoucherField(unusedVoucher, "code"), false);
    assert.equal(sellerTypesMod.canEditVoucherField(unusedVoucher, "value_vnd"), true);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "value_vnd"), false);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "discount_type"), false);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "min_order_vnd"), false);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "starts_at"), false);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "name"), true);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "quota"), true);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "budget_vnd"), true);
    assert.equal(sellerTypesMod.canEditVoucherField(usedVoucher, "ends_at"), true);

    // Report date range validation (<= 366 days)
    assert.equal(typeof formatMod.validateReportDateRange, "function");
    assert.equal(typeof formatMod.formatBpsPercent, "function");
    assert.equal(formatMod.validateReportDateRange("2026-01-01", "2026-01-31").ok, true);
    assert.equal(formatMod.validateReportDateRange("2026-02-01", "2026-01-31").ok, false);
    assert.equal(formatMod.validateReportDateRange("2025-01-01", "2026-01-02").ok, false);
    assert.equal(formatMod.validateReportDateRange("2025-01-01", "2026-01-01").ok, true);
    assert.equal(formatMod.formatBpsPercent(500), "5%");
    assert.equal(formatMod.formatBpsPercent(125), "1,25%");
    assert.equal(formatMod.formatBpsPercent(0), "0%");

    // Bank account validation
    assert.equal(typeof sellerTypesMod.validateBankAccountInput, "function");
    assert.equal(
      sellerTypesMod.validateBankAccountInput({
        bankCode: "VCB",
        bankName: "Vietcombank",
        accountNumber: "0123456789",
        accountHolderName: "NGUYEN VAN A",
      }).ok,
      true
    );
    assert.equal(
      sellerTypesMod.validateBankAccountInput({
        bankCode: "VCB",
        bankName: "Vietcombank",
        accountNumber: "123",
        accountHolderName: "NGUYEN VAN A",
      }).ok,
      false
    );
  });

  test("4. BFF proxy forwards Content-Disposition for CSV export and error-map covers Wave 2b errors", () => {
    const gwRouteSrc = readFileSync(resolve(WEB_SRC, "app/api/gw/[...path]/route.ts"), "utf8");
    assert.ok(
      gwRouteSrc.includes("content-disposition"),
      "/api/gw/[...path]/route.ts must forward content-disposition for CSV downloads"
    );

    const flashCodErr = errorMapMod.sanitizeGatewayError(400, {
      error: { code: "INVALID_ARGUMENT", message: "COD is not allowed for flash sale orders" },
    });
    assert.ok(/vietqr/i.test(flashCodErr.message), `got: ${flashCodErr.message}`);

    const rangeErr = errorMapMod.sanitizeGatewayError(400, {
      error: { code: "INVALID_ARGUMENT", message: "date range exceeds 366 days" },
    });
    assert.ok(/366/.test(rangeErr.message), `got: ${rangeErr.message}`);

    const payoutLockErr = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "payout locked for 24h after bank account change",
      },
    });
    assert.ok(/24\s*giờ|tạm khóa/i.test(payoutLockErr.message), `got: ${payoutLockErr.message}`);

    const bankOtpLimitErr = errorMapMod.sanitizeGatewayError(429, {
      error: {
        code: "RESOURCE_EXHAUSTED",
        message: "bank account otp resend limit exceeded",
      },
    });
    assert.ok(
      /quá số lần|thử lại/i.test(bankOtpLimitErr.message),
      `got: ${bankOtpLimitErr.message}`
    );
  });

  test("5. Buyer UX & cleanup: notification bell, real instant search, real 404, favicon, mock cleanup & RetryPaymentBox", () => {
    // Favicon & not-found page
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/icon.svg")),
      true,
      "web/src/app/icon.svg must exist"
    );
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/favicon.ico")),
      true,
      "web/src/app/favicon.ico must exist"
    );
    assert.equal(
      existsSync(resolve(WEB_SRC, "app/not-found.tsx")),
      true,
      "web/src/app/not-found.tsx must exist"
    );

    // Real 404 for unknown dynamic editorial slugs
    for (const rel of [
      "app/(shop)/bo-suu-tap/[slug]/page.tsx",
      "app/(shop)/tap-chi/[slug]/page.tsx",
      "app/(shop)/chien-dich/[slug]/page.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(src.includes("notFound()"), `${rel} must call notFound() for nonexistent slugs`);
    }

    // ShopHeader: notification bell + real instant search + no mock links in primary nav
    const headerSrc = readFileSync(resolve(WEB_SRC, "components/ShopHeader.tsx"), "utf8");
    assert.ok(headerSrc.includes("IconBell"), "ShopHeader must render notification bell icon");
    assert.ok(
      headerSrc.includes("/notifications"),
      "ShopHeader must fetch real /notifications unread count"
    );
    assert.ok(
      headerSrc.includes("products?keyword=") || headerSrc.includes('"/products?'),
      "ShopHeader instant search must query real /api/gw/products"
    );
    assert.equal(
      headerSrc.includes("getInstantSearchSuggestions"),
      false,
      "ShopHeader must not use static mock getInstantSearchSuggestions"
    );
    assert.equal(
      headerSrc.includes('href: "/hoi-vien"') || headerSrc.includes('href="/hoi-vien"'),
      false,
      "ShopHeader must remove mock /hoi-vien from primary nav"
    );
    assert.equal(
      headerSrc.includes('href: "/the-qua-tang"') || headerSrc.includes('href="/the-qua-tang"'),
      false,
      "ShopHeader must remove mock /the-qua-tang from primary nav"
    );

    // Mock pages labeled clearly & no fake balances/tiers alongside real account
    const hoiVienSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/hoi-vien/page.tsx"), "utf8");
    assert.ok(
      hoiVienSrc.includes("Bản xem trước, dữ liệu mẫu"),
      "/hoi-vien must display 'Bản xem trước, dữ liệu mẫu'"
    );
    assert.ok(
      hoiVienSrc.includes("coins/balance"),
      "/hoi-vien must fetch real coins/balance instead of showing mock 42.000 Xu"
    );

    const giftSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/the-qua-tang/page.tsx"), "utf8");
    assert.ok(
      giftSrc.includes("Bản xem trước, dữ liệu mẫu"),
      "/the-qua-tang must display 'Bản xem trước, dữ liệu mẫu'"
    );

    const aiSrc = readFileSync(resolve(WEB_SRC, "features/commerce/AiAssistantPanel.tsx"), "utf8");
    assert.ok(
      aiSrc.includes("Bản xem trước, dữ liệu mẫu"),
      "AiAssistantPanel must display 'Bản xem trước, dữ liệu mẫu'"
    );
    assert.equal(
      aiSrc.includes("href={`/don-hang/${o.id}`}"),
      false,
      "AiAssistantPanel must not link to mock ord-* IDs"
    );

    const mockCtxSrc = readFileSync(resolve(WEB_SRC, "mock/MockStoreContext.tsx"), "utf8");
    assert.ok(
      /INITIAL_WISHLIST:\s*string\[\]\s*=\s*\[\s*\]/.test(mockCtxSrc),
      "MockStoreContext must start with empty wishlist"
    );
    assert.ok(
      /INITIAL_SUPPORT_TICKETS:\s*SupportTicket\[\]\s*=\s*\[\s*\]/.test(mockCtxSrc),
      "MockStoreContext must start with empty support tickets"
    );

    const accountSrc = readFileSync(resolve(WEB_SRC, "features/account/AccountClient.tsx"), "utf8");
    assert.equal(
      accountSrc.includes("membership.spend4Q"),
      false,
      "AccountClient must not display mock membership spend alongside real user account"
    );

    // Cart & Checkout flash sale + shop discount wiring
    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    assert.ok(
      cartSrc.includes("shop-discounts/active"),
      "/gio-hang must fetch active shop discounts"
    );
    assert.ok(cartSrc.includes("flash-sales"), "/gio-hang must fetch live flash sales");

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(
      checkoutSrc.includes("isCodAllowedForCheckout"),
      "CheckoutClient must enforce COD block for flash sale orders"
    );
    assert.ok(
      checkoutSrc.includes("shop_program_discount_vnd"),
      "CheckoutClient must display shop_program_discount_vnd breakdown"
    );

    const retrySrc = readFileSync(
      resolve(WEB_SRC, "features/commerce/RetryPaymentBox.tsx"),
      "utf8"
    );
    assert.equal(
      retrySrc.includes("15 phút"),
      false,
      "RetryPaymentBox must not hardcode 15 phút (flash sale payments expire in 5 phút)"
    );
  });

  test("6. Seller portal Wave 2b wiring: vouchers, discount programs, flash items, freeship, bank+OTP, ledger, reports+CSV, mock nav cleanup", () => {
    const sellerShellSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/SellerShell.tsx"), "utf8");
    assert.equal(
      sellerShellSrc.includes("/seller/tuong-tac"),
      false,
      "SellerShell primary nav must remove mock /seller/tuong-tac"
    );
    assert.equal(
      sellerShellSrc.includes("/seller/chat"),
      false,
      "SellerShell primary nav must remove mock /seller/chat"
    );

    const chatSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/seller/chat/page.tsx"), "utf8");
    assert.ok(chatSrc.includes("Bản xem trước, dữ liệu mẫu"));
    assert.equal(
      chatSrc.includes("/seller/don-hang/${"),
      false,
      "seller/chat must not link to mock ord-* IDs"
    );

    const tuongTacSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/tuong-tac/page.tsx"),
      "utf8"
    );
    assert.ok(tuongTacSrc.includes("Bản xem trước, dữ liệu mẫu"));
    assert.equal(
      tuongTacSrc.includes("/seller/san-pham/${"),
      false,
      "seller/tuong-tac must not link to mock prod-* IDs"
    );

    const promoSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/khuyen-mai/page.tsx"),
      "utf8"
    );
    assert.ok(promoSrc.includes("include_inactive=true"));
    assert.ok(promoSrc.includes("seller/flash-sales/items"));
    assert.ok(
      promoSrc.includes("seller/discount-programs") ||
        promoSrc.includes("PromotionsDiscountProgramsTab"),
      "seller/khuyen-mai must wire real shop discount programs"
    );
    assert.equal(
      promoSrc.includes("COMBO_OFFERS"),
      false,
      "seller/khuyen-mai must replace mock Combo tab"
    );

    const profileSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/seller/ho-so/page.tsx"), "utf8");
    assert.ok(profileSrc.includes("free_ship_enabled"));
    assert.ok(profileSrc.includes("free_ship_min_order_vnd"));
    assert.ok(profileSrc.includes("free_ship_max_vnd"));

    const financeSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/tai-chinh/page.tsx"),
      "utf8"
    );
    assert.ok(financeSrc.includes("seller/bank-account"));
    assert.ok(financeSrc.includes("verify-otp"));
    assert.ok(financeSrc.includes("seller/ledger"));
    assert.equal(
      financeSrc.includes("getShopBankAccount"),
      false,
      "seller/tai-chinh must replace mock getShopBankAccount with real bank API"
    );

    const sellerHomeSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/seller/page.tsx"), "utf8");
    assert.ok(sellerHomeSrc.includes("seller/reports"));
    assert.ok(sellerHomeSrc.includes("seller/reports/export"));
  });

  test("7. Admin portal Wave 2b wiring: vouchers+categories, flash queue, fees, bank review, orphan review, mobile ledger, reports+CSV, kiem-duyet fallback", () => {
    const adminPromoSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminPromoCoinsPanel.tsx"),
      "utf8"
    );
    assert.ok(adminPromoSrc.includes("include_inactive=true"));
    assert.ok(adminPromoSrc.includes("category_ids"));
    assert.ok(adminPromoSrc.includes("admin/flash-sale-items"));
    assert.ok(adminPromoSrc.includes("min_discount_percent"));

    const feesPanelPath = resolve(WEB_SRC, "features/admin/AdminFeesPanel.tsx");
    assert.equal(existsSync(feesPanelPath), true, "AdminFeesPanel.tsx must exist");
    const feesSrc = readFileSync(feesPanelPath, "utf8");
    assert.ok(feesSrc.includes("admin/fees/categories"));
    assert.ok(feesSrc.includes("admin/fees/payment"));

    const cauHinhSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/cau-hinh/page.tsx"),
      "utf8"
    );
    assert.ok(cauHinhSrc.includes("AdminFeesPanel"));

    const gianHangSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    assert.ok(gianHangSrc.includes("/bank-account"));
    assert.ok(gianHangSrc.includes("/bank-account/review"));

    const doiSoatSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/doi-soat/page.tsx"),
      "utf8"
    );
    assert.ok(doiSoatSrc.includes("admin/orders/needs-review"));
    assert.ok(doiSoatSrc.includes("/resolve-review"));
    // Mobile cards for ledger table (390px bug fix)
    const mobileListMatches = doiSoatSrc.match(/shared\.mobileList/g) ?? [];
    assert.ok(
      mobileListMatches.length >= 4,
      `admin/doi-soat must render shared.mobileList for ledger, reconcile, orders, and queues (found ${mobileListMatches.length})`
    );

    const adminHomeSrc = readFileSync(resolve(WEB_SRC, "app/(admin)/admin/page.tsx"), "utf8");
    assert.ok(adminHomeSrc.includes("admin/reports"));
    assert.ok(adminHomeSrc.includes("admin/reports/export"));
    assert.ok(adminHomeSrc.includes("user_stats"));

    const kiemDuyetSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/kiem-duyet/page.tsx"),
      "utf8"
    );
    assert.ok(kiemDuyetSrc.includes('title="Kiểm duyệt sản phẩm & nội dung"'));
  });

  test("8. [Review R2 #1] /shop-discounts/active requires filter (no call when empty) and batches sku_ids <= 200", async () => {
    assert.equal(typeof checkoutHelpersMod.buildActiveShopDiscountBatches, "function");
    assert.equal(typeof checkoutHelpersMod.fetchActiveShopDiscountsWithFetcher, "function");

    // No filter -> 0 batches and fetcher is never called
    assert.deepEqual(checkoutHelpersMod.buildActiveShopDiscountBatches(), []);
    assert.deepEqual(
      checkoutHelpersMod.buildActiveShopDiscountBatches({ skuIds: ["", "  "], shopId: "  " }),
      []
    );
    let emptyCalls = 0;
    const emptyRes = await checkoutHelpersMod.fetchActiveShopDiscountsWithFetcher({}, async () => {
      emptyCalls += 1;
      return [];
    });
    assert.equal(emptyCalls, 0, "must not call fetcher when no skuIds or shopId provided");
    assert.deepEqual(emptyRes, []);

    // 450 unique SKUs (+ duplicates & blanks) -> 3 chunks: 200, 200, 50
    const manySkus = [
      "  ",
      ...Array.from({ length: 450 }, (_, i) => `sku-${i + 1}`),
      "sku-1",
      "sku-200",
    ];
    const batches = checkoutHelpersMod.buildActiveShopDiscountBatches({ skuIds: manySkus });
    assert.equal(batches.length, 3);
    assert.equal(batches[0].skuIds.length, 200);
    assert.equal(batches[1].skuIds.length, 200);
    assert.equal(batches[2].skuIds.length, 50);

    const calledSizes = [];
    const merged = await checkoutHelpersMod.fetchActiveShopDiscountsWithFetcher(
      { skuIds: manySkus },
      async (batch) => {
        calledSizes.push(batch.skuIds.length);
        return [
          {
            item_id: `item-${batch.skuIds[0]}`,
            program_id: "p1",
            shop_id: "s1",
            sku_id: batch.skuIds[0],
            original_price_vnd: 200000,
            discounted_price_vnd: 150000,
            discount_percent: 25,
            per_user_limit: 2,
          },
        ];
      }
    );
    assert.deepEqual(calledSizes, [200, 200, 50]);
    assert.equal(merged.length, 3);

    // Source checks: catalog.ts and thanh-toan/page.tsx never call /shop-discounts/active unfiltered
    const catalogSrc = readFileSync(resolve(WEB_SRC, "lib/catalog.ts"), "utf8");
    assert.equal(
      /fetchActiveShopDiscounts\(\s*\)/.test(catalogSrc),
      false,
      "catalog.ts must never call fetchActiveShopDiscounts() without skuIds or shopId"
    );
    const checkoutPageSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/page.tsx"),
      "utf8"
    );
    assert.equal(
      checkoutPageSrc.includes('"/shop-discounts/active"'),
      false,
      "thanh-toan/page.tsx must not call unfiltered /shop-discounts/active"
    );
  });

  test("9. [Review R2 #2 & #3] Flash limit opt-out ('Mua giá thường') and shop program applied_shop_program_item_ids", () => {
    assert.equal(typeof checkoutHelpersMod.evaluateShopFlashLines, "function");
    assert.equal(typeof checkoutHelpersMod.isShopProgramItemApplied, "function");
    assert.equal(typeof checkoutHelpersMod.isFlashLimitOrSlotError, "function");

    // Qty exceeds per_user_limit -> flagged for explicit opt-out
    const qtyEval = checkoutHelpersMod.evaluateShopFlashLines({
      lines: [
        {
          skuId: "sku-f1",
          quantity: 2,
          regularUnitPriceVnd: 200000,
          flashSaleItemId: "fi-1",
          flashUnitPriceVnd: 150000,
          flashPerUserLimit: 1,
        },
      ],
      previewShopFlashDiscountVnd: 0,
      previewWarnings: ["flash sale item fi-1 per-user limit (1) exceeded"],
    });
    assert.equal(qtyEval["sku-f1"].exceedsQtyLimit, true);
    assert.equal(qtyEval["sku-f1"].needsRegularPriceChoice, true);

    // Qty within limit, but user already exhausted per_user_limit on previous order -> preview returns 0 flash discount
    const exhaustedEval = checkoutHelpersMod.evaluateShopFlashLines({
      lines: [
        {
          skuId: "sku-f2",
          quantity: 1,
          regularUnitPriceVnd: 200000,
          flashSaleItemId: "fi-2",
          flashUnitPriceVnd: 150000,
          flashPerUserLimit: 1,
        },
      ],
      previewShopFlashDiscountVnd: 0,
      previewWarnings: [],
    });
    assert.equal(exhaustedEval["sku-f2"].exceedsQtyLimit, false);
    assert.equal(exhaustedEval["sku-f2"].previewShortfall, true);
    assert.equal(exhaustedEval["sku-f2"].needsRegularPriceChoice, true);

    // Shop program applied_shop_program_item_ids determines whether line gets discounted price or regular price
    assert.equal(
      checkoutHelpersMod.isShopProgramItemApplied({
        shopProgramItemId: "spi-1",
        shopBreakdown: {
          applied_shop_program_item_ids: ["spi-1"],
          shop_program_discount_vnd: 40000,
        },
      }),
      true
    );
    assert.equal(
      checkoutHelpersMod.isShopProgramItemApplied({
        shopProgramItemId: "spi-2",
        shopBreakdown: {
          applied_shop_program_item_ids: ["spi-1"],
          shop_program_discount_vnd: 40000,
        },
      }),
      false
    );
    assert.equal(
      checkoutHelpersMod.isShopProgramItemApplied({
        shopProgramItemId: "spi-2",
        shopBreakdown: { applied_shop_program_item_ids: [], shop_program_discount_vnd: 0 },
      }),
      false
    );

    // Warning translation distinguishes shop program limit & flash limit
    assert.ok(
      /chương trình.*giá thường|giới hạn/i.test(
        checkoutMessagesMod.translateVoucherReason(
          "shop discount program item spi-2 per-user limit (1) exceeded"
        )
      )
    );
    assert.ok(
      /flash.*giới hạn|mua giá thường/i.test(
        checkoutMessagesMod.translateVoucherReason(
          "flash sale item fi-2 per-user limit (1) exceeded"
        )
      )
    );

    // UI wiring checks for "Mua giá thường" & applied_shop_program_item_ids
    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    assert.ok(
      cartSrc.includes("Mua giá thường"),
      "/gio-hang must render Mua giá thường opt-out button"
    );
    assert.ok(
      cartSrc.includes("isShopProgramItemApplied"),
      "/gio-hang must check applied_shop_program_item_ids"
    );

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(
      checkoutSrc.includes("Mua giá thường"),
      "CheckoutClient must render Mua giá thường opt-out button"
    );
    assert.ok(
      checkoutSrc.includes("isShopProgramItemApplied"),
      "CheckoutClient must check applied_shop_program_item_ids"
    );
  });

  test("10. [Review R2 #4, #5, #6, #7] Freeship >0, admin bank_review_pending filter, strict contract DTO keys, buyer no commission/payment_fee", () => {
    // #4 Seller freeship max_vnd > 0
    const hoSoSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/seller/ho-so/page.tsx"), "utf8");
    assert.equal(
      hoSoSrc.includes("Nhập 0 để miễn toàn bộ phí giao hàng"),
      false,
      "seller/ho-so must remove misleading 0-for-unlimited freeship copy"
    );
    assert.ok(
      hoSoSrc.includes("parseSellerFreeshipSettings"),
      "seller/ho-so must validate freeship via parseSellerFreeshipSettings"
    );
    assert.equal(
      sellerTypesMod.parseSellerFreeshipSettings({
        enabled: true,
        minOrderRaw: "0",
        maxVndRaw: "0",
      }).ok,
      false,
      "parseSellerFreeshipSettings must reject free_ship_max_vnd <= 0 when freeship is enabled"
    );

    // #5 Admin gian-hang bank_review_pending filter, label, no account_number
    const gianHangSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    assert.ok(
      gianHangSrc.includes("bank_review_pending=true"),
      "admin/gian-hang must support bank_review_pending=true filter"
    );
    assert.ok(
      gianHangSrc.includes("Chờ duyệt tài khoản ngân hàng"),
      "admin/gian-hang must show Chờ duyệt tài khoản ngân hàng filter"
    );
    assert.equal(
      gianHangSrc.includes("Khác tên gian hàng"),
      false,
      "admin/gian-hang must rename Khác tên gian hàng -> Khác tên chủ shop"
    );
    assert.ok(gianHangSrc.includes("Khác tên chủ shop"));
    assert.equal(
      /\bbankAcc\.account_number\b/.test(gianHangSrc),
      false,
      "admin/gian-hang must not read bankAcc.account_number"
    );

    // #6 Strict contract keys removed from gateway-types and pages
    const gwTypesSrc = readFileSync(resolve(WEB_SRC, "lib/gateway-types.ts"), "utf8");
    for (const deadKey of [
      "program_item_id",
      "orders_count",
      "new_users_in_range",
      "new_shops_in_range",
      "entry_id",
    ]) {
      assert.equal(
        new RegExp(`\\b${deadKey}\\b`).test(gwTypesSrc),
        false,
        `gateway-types.ts must remove non-contract fallback key ${deadKey}`
      );
    }
    const bankIfaceMatch = gwTypesSrc.match(/interface GatewayShopBankAccount \{[\s\S]*?\}/);
    assert.ok(bankIfaceMatch);
    assert.equal(
      bankIfaceMatch[0].includes("rejection_reason"),
      false,
      "GatewayShopBankAccount must remove rejection_reason"
    );
    assert.equal(
      bankIfaceMatch[0].includes("account_number?:"),
      false,
      "GatewayShopBankAccount must remove account_number"
    );

    // #7 Buyer UI does not reference commission_* or payment_fee_*
    for (const rel of [
      "app/(shop)/gio-hang/page.tsx",
      "app/(shop)/thanh-toan/CheckoutClient.tsx",
      "app/(shop)/don-hang/page.tsx",
      "app/(shop)/don-hang/[id]/page.tsx",
      "lib/catalog-types.ts",
      "lib/catalog-mapper.ts",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.equal(
        /commission_vnd|commission_rate_bps|payment_fee_vnd|payment_fee_bps|commissionRateBps/.test(
          src
        ),
        false,
        `buyer-facing ${rel} must not use commission_* or payment_fee_*`
      );
    }
  });

  test("11. [Review R3 #1] optOutFlash skips flash only (falls back to active shop discount) and uses accurate copy", () => {
    const liveFlash = {
      flashSaleItemId: "fi-r3",
      flashPriceVnd: 120000,
      remainingSlots: 5,
      perUserLimit: 1,
    };
    const activeShopDiscount = {
      item_id: "spi-r3",
      program_id: "prog-r3",
      program_name: "Ưu đãi cuối tuần",
      shop_id: "shop-1",
      sku_id: "sku-r3",
      original_price_vnd: 200000,
      discounted_price_vnd: 170000,
      discount_percent: 15,
      per_user_limit: 3,
    };

    // With active shop discount: optOutFlash falls back to shop_discount (final application still checked via applied_shop_program_item_ids)
    const fallbackShop = checkoutHelpersMod.resolveSkuEffectivePromotion({
      regularPriceVnd: 200000,
      liveFlash,
      activeShopDiscount,
      optOutFlash: true,
    });
    assert.equal(fallbackShop.kind, "shop_discount");
    assert.equal(fallbackShop.effectiveUnitPriceVnd, 170000);
    assert.equal(fallbackShop.flashSaleItemId, undefined);
    assert.equal(fallbackShop.shopProgramItemId, "spi-r3");

    // Final price decision obeys preview applied_shop_program_item_ids
    assert.equal(
      checkoutHelpersMod.isShopProgramItemApplied({
        shopProgramItemId: fallbackShop.shopProgramItemId,
        shopBreakdown: {
          applied_shop_program_item_ids: ["spi-r3"],
          shop_program_discount_vnd: 30000,
        },
      }),
      true
    );
    assert.equal(
      checkoutHelpersMod.isShopProgramItemApplied({
        shopProgramItemId: fallbackShop.shopProgramItemId,
        shopBreakdown: { applied_shop_program_item_ids: [], shop_program_discount_vnd: 0 },
      }),
      false
    );

    // Without active shop discount: optOutFlash reverts to regular price
    const fallbackNone = checkoutHelpersMod.resolveSkuEffectivePromotion({
      regularPriceVnd: 200000,
      liveFlash,
      activeShopDiscount: null,
      optOutFlash: true,
    });
    assert.equal(fallbackNone.kind, "none");
    assert.equal(fallbackNone.effectiveUnitPriceVnd, 200000);

    // Reason text distinguishes shop discount fallback vs regular price
    const evalWithShopDisc = checkoutHelpersMod.evaluateShopFlashLines({
      lines: [
        {
          skuId: "sku-r3",
          quantity: 2,
          regularUnitPriceVnd: 200000,
          flashSaleItemId: "fi-r3",
          flashUnitPriceVnd: 120000,
          flashPerUserLimit: 1,
          hasActiveShopDiscount: true,
        },
      ],
      previewShopFlashDiscountVnd: 0,
    });
    assert.ok(
      /giá chương trình gian hàng/i.test(evalWithShopDisc["sku-r3"].reason),
      `expected shop discount wording, got: ${evalWithShopDisc["sku-r3"].reason}`
    );

    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    const checkoutPageSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/page.tsx"),
      "utf8"
    );
    assert.ok(
      cartSrc.includes("giá chương trình gian hàng"),
      "/gio-hang must use 'giá chương trình gian hàng' when SKU has active shop discount"
    );
    assert.ok(
      checkoutSrc.includes("giá chương trình gian hàng"),
      "CheckoutClient must use 'giá chương trình gian hàng' when SKU has active shop discount"
    );
    assert.equal(
      /if\s*\(!validFlashId\)\s*\{\s*const\s+matchedDisc/.test(checkoutPageSrc),
      false,
      "thanh-toan/page.tsx must retain active shop discount on buyNowItem even when validFlashId is present"
    );
  });

  test("12. [Review R3 #2] Slim checkout and promotions/preview payloads omit server-ignored fields", () => {
    const gwTypesSrc = readFileSync(resolve(WEB_SRC, "lib/gateway-types.ts"), "utf8");
    const checkoutItemIface = gwTypesSrc.match(/interface GatewayCheckoutItemInput \{[\s\S]*?\}/);
    assert.ok(checkoutItemIface);
    assert.equal(
      checkoutItemIface[0].includes("shop_program_item_id"),
      false,
      "GatewayCheckoutItemInput must omit shop_program_item_id"
    );

    const previewLineIface = gwTypesSrc.match(/interface GatewayDiscountLineItem \{[\s\S]*?\}/);
    assert.ok(previewLineIface);
    for (const ignored of ["shop_discount_program_item_id", "unit_price_vnd", "category_id"]) {
      assert.equal(
        previewLineIface[0].includes(ignored),
        false,
        `GatewayDiscountLineItem must omit server-ignored ${ignored}`
      );
    }

    const previewShopIface = gwTypesSrc.match(/interface GatewayShopDiscountInput \{[\s\S]*?\}/);
    assert.ok(previewShopIface);
    assert.equal(
      previewShopIface[0].includes("subtotal_vnd"),
      false,
      "GatewayShopDiscountInput must omit server-ignored subtotal_vnd"
    );

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.equal(
      checkoutSrc.includes("shop_program_item_id"),
      false,
      "CheckoutClient must not send shop_program_item_id in POST /checkout"
    );
    assert.equal(
      checkoutSrc.includes("shop_discount_program_item_id"),
      false,
      "CheckoutClient must not send shop_discount_program_item_id in POST /promotions/preview"
    );

    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    assert.equal(
      cartSrc.includes("shop_discount_program_item_id"),
      false,
      "gio-hang must not send shop_discount_program_item_id in POST /promotions/preview"
    );
  });

  test("13. [Review R3 #3] POST /checkout warnings[] persist across redirect, translate via translateVoucherReason, and result pages show server amounts", () => {
    assert.equal(typeof checkoutHelpersMod.translatePromotionWarnings, "function");
    assert.equal(typeof checkoutHelpersMod.saveCheckoutWarnings, "function");
    assert.equal(typeof checkoutHelpersMod.readCheckoutWarnings, "function");
    assert.equal(typeof checkoutHelpersMod.clearCheckoutWarnings, "function");

    const mem = new Map();
    const mockStorage = {
      getItem: (k) => (mem.has(k) ? mem.get(k) : null),
      setItem: (k, v) => {
        mem.set(k, String(v));
      },
      removeItem: (k) => {
        mem.delete(k);
      },
    };

    const rawWarnings = [
      "shop discount program item spi-1 per-user limit (1) exceeded",
      "shop discount program item spi-1 per-user limit (1) exceeded",
      "voucher is PAUSED",
    ];
    const translated = checkoutHelpersMod.translatePromotionWarnings(rawWarnings);
    assert.equal(translated.length, 2, "must deduplicate translated warnings");
    assert.ok(/chương trình.*giá thường|giới hạn/i.test(translated[0]));
    assert.ok(/tạm dừng/i.test(translated[1]));

    checkoutHelpersMod.saveCheckoutWarnings(
      {
        paymentId: "pay-123",
        orderIds: ["ord-1", "ord-2"],
        checkoutGroupId: "grp-1",
        warnings: rawWarnings,
      },
      mockStorage
    );

    // Readable by paymentId on /thanh-toan/cho-thanh-toan/[paymentId]
    assert.deepEqual(
      checkoutHelpersMod.readCheckoutWarnings({ paymentId: "pay-123" }, mockStorage),
      translated
    );
    // Still readable by orderId after navigating to /don-hang/[id]
    assert.deepEqual(
      checkoutHelpersMod.readCheckoutWarnings({ orderId: "ord-2" }, mockStorage),
      translated
    );
    // Readable on /don-hang list when matching recent orderIds
    assert.deepEqual(
      checkoutHelpersMod.readCheckoutWarnings({ orderIds: ["ord-999", "ord-1"] }, mockStorage),
      translated
    );
    // Does not leak onto unrelated paymentId/orderId
    assert.deepEqual(
      checkoutHelpersMod.readCheckoutWarnings({ paymentId: "pay-other" }, mockStorage),
      []
    );
    assert.deepEqual(
      checkoutHelpersMod.readCheckoutWarnings({ orderId: "ord-other" }, mockStorage),
      []
    );

    checkoutHelpersMod.clearCheckoutWarnings(mockStorage);
    assert.deepEqual(
      checkoutHelpersMod.readCheckoutWarnings({ paymentId: "pay-123" }, mockStorage),
      []
    );

    // DTO & UI wiring checks
    const gwTypesSrc = readFileSync(resolve(WEB_SRC, "lib/gateway-types.ts"), "utf8");
    const checkoutRespIface = gwTypesSrc.match(/interface GatewayCheckoutResponse \{[\s\S]*?\}/);
    assert.ok(checkoutRespIface && checkoutRespIface[0].includes("warnings?: string[]"));

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(
      checkoutSrc.includes("saveCheckoutWarnings"),
      "CheckoutClient must persist res.warnings before redirect"
    );

    for (const rel of [
      "app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx",
      "features/account/OrderDetailClient.tsx",
      "features/account/OrdersListClient.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(
        src.includes("readCheckoutWarnings"),
        `${rel} must read and display persisted checkout warnings`
      );
    }
  });

  test("14. [Review R3 #4 & #5 + R4 P1-5] Freeship parser rejects negative/NaN, allows 0, enforces max>0; Seller bank terminal OTP 409 returns to entry form", () => {
    // Real unit test on parseSellerFreeshipSettings (eliminates P1-5 dead-branch false positive)
    assert.equal(typeof sellerTypesMod.parseSellerFreeshipSettings, "function");
    assert.equal(
      sellerTypesMod.parseSellerFreeshipSettings({
        enabled: true,
        minOrderRaw: "-50000",
        maxVndRaw: "25000",
      }).ok,
      false,
      "negative free_ship_min_order_vnd must be rejected, not coerced to 0"
    );
    assert.equal(
      sellerTypesMod.parseSellerFreeshipSettings({
        enabled: true,
        minOrderRaw: "abc",
        maxVndRaw: "25000",
      }).ok,
      false,
      "non-numeric free_ship_min_order_vnd must be rejected"
    );
    assert.equal(
      sellerTypesMod.parseSellerFreeshipSettings({
        enabled: true,
        minOrderRaw: "",
        maxVndRaw: "25000",
      }).ok,
      false,
      "empty free_ship_min_order_vnd must be rejected when freeship enabled"
    );
    const zeroMin = sellerTypesMod.parseSellerFreeshipSettings({
      enabled: true,
      minOrderRaw: "0",
      maxVndRaw: "25000",
    });
    assert.equal(zeroMin.ok, true);
    assert.equal(zeroMin.minOrderVnd, 0);
    assert.equal(zeroMin.maxVnd, 25000);
    assert.equal(
      sellerTypesMod.parseSellerFreeshipSettings({
        enabled: true,
        minOrderRaw: "199000",
        maxVndRaw: "0",
      }).ok,
      false,
      "free_ship_max_vnd <= 0 must be rejected when freeship enabled"
    );
    const disabled = sellerTypesMod.parseSellerFreeshipSettings({
      enabled: false,
      minOrderRaw: "-10",
      maxVndRaw: "0",
    });
    assert.equal(disabled.ok, true);
    assert.equal(disabled.minOrderVnd, 0);
    assert.equal(disabled.maxVnd, 0);

    const hoSoSrc = readFileSync(resolve(WEB_SRC, "app/(seller)/seller/ho-so/page.tsx"), "utf8");
    assert.ok(
      hoSoSrc.includes("parseSellerFreeshipSettings"),
      "seller/ho-so must validate freeship via parseSellerFreeshipSettings"
    );

    // #5 Terminal bank OTP error detection & error-map coverage
    assert.equal(typeof sellerTypesMod.isTerminalBankOtpError, "function");
    assert.equal(
      sellerTypesMod.isTerminalBankOtpError({
        status: 409,
        code: "FAILED_PRECONDITION",
        message: "otp expired, please resubmit bank account",
      }),
      true
    );
    assert.equal(
      sellerTypesMod.isTerminalBankOtpError({
        status: 409,
        code: "FAILED_PRECONDITION",
        message: "too many invalid otp attempts",
      }),
      true
    );
    assert.equal(
      sellerTypesMod.isTerminalBankOtpError({
        status: 409,
        code: "FAILED_PRECONDITION",
        message: "no bank account pending otp verification",
      }),
      true
    );
    assert.equal(
      sellerTypesMod.isTerminalBankOtpError({
        status: 400,
        code: "INVALID_ARGUMENT",
        message: "Mã xác thực chưa chính xác. Vui lòng kiểm tra lại hộp thư.",
      }),
      false
    );

    const noPendingErr = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "no bank account pending otp verification",
      },
    });
    assert.ok(
      /không còn hiệu lực|hết hạn|nhập lại/i.test(noPendingErr.message),
      `got: ${noPendingErr.message}`
    );

    const taiChinhSrc = readFileSync(
      resolve(WEB_SRC, "app/(seller)/seller/tai-chinh/page.tsx"),
      "utf8"
    );
    assert.ok(
      taiChinhSrc.includes("isTerminalBankOtpError"),
      "seller/tai-chinh must handle terminal OTP errors by returning to bank account form"
    );
    assert.ok(
      /isTerminalBankOtpError[\s\S]*?setShowBankForm\(true\)/.test(taiChinhSrc),
      "seller/tai-chinh must reopen bank form when OTP verification terminates"
    );
    assert.equal(
      taiChinhSrc.includes("khác tên gian hàng"),
      false,
      "seller/tai-chinh should refer to tên chủ shop lúc được duyệt, not tên gian hàng"
    );
  });

  test("15. [Review R3 #6] Multi-level categories: root-only home tiles & recursive descendant filtering/facets", () => {
    assert.equal(typeof catalogMapperMod.getRootCategories, "function");
    assert.equal(typeof catalogMapperMod.buildCategoryDescendantSlugMap, "function");

    const cats = [
      { id: "c-ao", slug: "ao", parentId: null },
      { id: "c-ao-thun", slug: "ao-thun", parentId: "c-ao" },
      { id: "c-ao-thun-co-tron", slug: "ao-thun-co-tron", parentId: "c-ao-thun" },
      { id: "c-quan", slug: "quan", parentId: "" },
      { id: "c-quan-short", slug: "quan-short", parentId: "c-quan" },
    ];

    const roots = catalogMapperMod.getRootCategories(cats);
    assert.deepEqual(
      roots.map((c) => c.slug),
      ["ao", "quan"]
    );

    const descMap = catalogMapperMod.buildCategoryDescendantSlugMap(cats);
    assert.deepEqual(Array.from(descMap.get("ao") ?? []).sort(), [
      "ao",
      "ao-thun",
      "ao-thun-co-tron",
    ]);
    assert.deepEqual(Array.from(descMap.get("ao-thun") ?? []).sort(), [
      "ao-thun",
      "ao-thun-co-tron",
    ]);
    assert.deepEqual(Array.from(descMap.get("ao-thun-co-tron") ?? []), ["ao-thun-co-tron"]);
    assert.deepEqual(Array.from(descMap.get("quan") ?? []).sort(), ["quan", "quan-short"]);

    // Cycle safety check
    const cyclic = [
      { id: "x", slug: "x", parentId: "y" },
      { id: "y", slug: "y", parentId: "x" },
    ];
    const cyclicMap = catalogMapperMod.buildCategoryDescendantSlugMap(cyclic);
    assert.deepEqual(Array.from(cyclicMap.get("x") ?? []).sort(), ["x", "y"]);

    const homeSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/HomeClient.tsx"), "utf8");
    assert.ok(
      homeSrc.includes("getRootCategories"),
      "HomeClient must filter home category tiles to root categories only"
    );

    const explorerSrc = readFileSync(
      resolve(WEB_SRC, "features/catalog/CatalogExplorer.tsx"),
      "utf8"
    );
    assert.ok(
      explorerSrc.includes("buildCategoryDescendantSlugMap"),
      "CatalogExplorer must use descendant category map for parent category filtering and facets"
    );
  });

  test("16. [Review R3 #7 & #8 + R4 P1-1/P2-4] Real gateway FAILED_PRECONDITION + prefixed codes flow through sanitizeGatewayError -> translateCheckoutError & isFlashLimitOrSlotError; zero buyer commission/payment_fee", () => {
    // Real gateway ALWAYS returns code="FAILED_PRECONDITION" for gRPC FailedPrecondition with prefixed messages!
    const stockSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "INSUFFICIENT_STOCK: SKU sku-1 has 1 available, requested 3",
      },
    });
    assert.equal(stockSanitized.code, "INSUFFICIENT_STOCK");
    const stockApiErr = new apiMod.ApiError(
      stockSanitized.status,
      stockSanitized.code,
      stockSanitized.message
    );
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(stockApiErr, true, "VIETQR"),
      false,
      "real gateway INSUFFICIENT_STOCK must NOT trigger flash opt-out"
    );
    const stockMsg = checkoutMessagesMod.translateCheckoutError(stockApiErr, {
      paymentMethod: "VIETQR",
      isFlashSale: true,
    });
    assert.ok(/tồn kho|hết hàng/i.test(stockMsg), `wrong stock message: ${stockMsg}`);
    assert.equal(
      /flash/i.test(stockMsg),
      false,
      `must not overwrite stock error with flash copy: ${stockMsg}`
    );

    const idempSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message:
          "IDEMPOTENCY_KEY_CONFLICT: idempotency_key was already used with a different checkout payload",
      },
    });
    assert.equal(idempSanitized.code, "IDEMPOTENCY_KEY_CONFLICT");
    const idempApiErr = new apiMod.ApiError(
      idempSanitized.status,
      idempSanitized.code,
      idempSanitized.message
    );
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(idempApiErr, true, "VIETQR"),
      false,
      "real gateway IDEMPOTENCY_KEY_CONFLICT must NOT trigger flash opt-out"
    );
    const idempMsg = checkoutMessagesMod.translateCheckoutError(idempApiErr, {
      paymentMethod: "VIETQR",
      isFlashSale: true,
    });
    assert.ok(
      /đã tạo đơn|đơn hàng của tôi|trùng mã/i.test(idempMsg),
      `wrong idempotency message: ${idempMsg}`
    );

    // Non-flash 429 rate-limit and non-flash FAILED_PRECONDITION (e.g. voucher exhausted) MUST return false
    const rateLimitSanitized = errorMapMod.sanitizeGatewayError(429, {
      error: { code: "RESOURCE_EXHAUSTED", message: "rate limit exceeded" },
    });
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(
        new apiMod.ApiError(
          rateLimitSanitized.status,
          rateLimitSanitized.code,
          rateLimitSanitized.message
        ),
        true,
        "VIETQR"
      ),
      false,
      "generic 429 IP rate-limit must NOT trigger flash opt-out"
    );

    const voucherSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "VOUCHER_UNAVAILABLE_ON_COMMIT: voucher quota exhausted",
      },
    });
    assert.equal(voucherSanitized.code, "VOUCHER_UNAVAILABLE_ON_COMMIT");
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(
        new apiMod.ApiError(
          voucherSanitized.status,
          voucherSanitized.code,
          voucherSanitized.message
        ),
        true,
        "VIETQR"
      ),
      false,
      "VOUCHER_UNAVAILABLE_ON_COMMIT must NOT trigger flash opt-out"
    );

    // Real gateway flash failure MUST return true
    const flashCommitSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "FLASH_UNAVAILABLE_ON_COMMIT: flash sale item fi-1 per-user limit exceeded",
      },
    });
    assert.equal(flashCommitSanitized.code, "FLASH_UNAVAILABLE_ON_COMMIT");
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(
        new apiMod.ApiError(
          flashCommitSanitized.status,
          flashCommitSanitized.code,
          flashCommitSanitized.message
        ),
        true,
        "VIETQR"
      ),
      true
    );

    // #8 Buyer payment/order views never reference commission_vnd or payment_fee_vnd
    for (const rel of [
      "app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/page.tsx",
      "app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx",
      "features/commerce/RetryPaymentBox.tsx",
      "features/account/OrderDetailClient.tsx",
      "features/account/OrdersListClient.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.equal(
        /\b(commission_vnd|payment_fee_vnd|payment_fee_bps)\b/.test(src),
        false,
        `buyer-facing ${rel} must not use commission_vnd or payment_fee_*`
      );
    }
  });

  test("17. [Review R4 #1 P0-1] Shipping calculate-fee payload sends items + payment_method without client-guessed weight_grams/cod_amount_vnd", () => {
    const payload = checkoutHelpersMod.buildCalculateFeePayload({
      shopId: "shop-uuid-1",
      toProvinceCode: "79",
      toWardCode: "26734",
      items: [
        { skuId: "sku-regular", quantity: 2 },
        { skuId: "sku-flash", quantity: 1 },
      ],
      paymentMethod: "COD",
    });
    assert.deepEqual(payload, {
      shop_id: "shop-uuid-1",
      to_province_code: "79",
      to_ward_code: "26734",
      items: [
        { sku_id: "sku-regular", quantity: 2 },
        { sku_id: "sku-flash", quantity: 1 },
      ],
      payment_method: "COD",
    });
    assert.equal("weight_grams" in payload, false);
    assert.equal("cod_amount_vnd" in payload, false);
    assert.equal("from_province_code" in payload, false);

    // Verify Cart, Checkout, Buy Now removed 300g fallback and 100g floor
    for (const rel of [
      "app/(shop)/thanh-toan/CheckoutClient.tsx",
      "app/(shop)/thanh-toan/page.tsx",
      "app/(shop)/gio-hang/page.tsx",
      "context/CartContext.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.equal(
        /weightGrams\s*\|\|\s*300|weight_grams\s*\|\|\s*300/.test(src),
        false,
        `${rel} must not use 300g fallback`
      );
    }
  });

  test("18. [Review R4 #2 P0-2] Admin coin credit reference_id is stable across retries for same (user, amount, reason) and rotates on change/success", () => {
    assert.equal(typeof checkoutHelpersMod.resolveStableReferenceKey, "function");
    let uuidSeq = 0;
    const fakeUuid = () => `uuid-${++uuidSeq}`;

    // First submit generates UUID
    const r1 = checkoutHelpersMod.resolveStableReferenceKey({
      prefix: "admin-credit",
      manualRef: "",
      fingerprint: "u1|50000|hoan-xu",
      cache: null,
      uuidFn: fakeUuid,
    });
    assert.equal(r1.key, "admin-credit-uuid-1");

    // Retry after timeout with same inputs MUST reuse exact same reference_id
    const r2 = checkoutHelpersMod.resolveStableReferenceKey({
      prefix: "admin-credit",
      manualRef: "",
      fingerprint: "u1|50000|hoan-xu",
      cache: r1.cache,
      uuidFn: fakeUuid,
    });
    assert.equal(r2.key, "admin-credit-uuid-1");

    // Changing input (amount/user/reason) mints a new UUID
    const r3 = checkoutHelpersMod.resolveStableReferenceKey({
      prefix: "admin-credit",
      manualRef: "",
      fingerprint: "u1|60000|hoan-xu",
      cache: r2.cache,
      uuidFn: fakeUuid,
    });
    assert.equal(r3.key, "admin-credit-uuid-2");

    // After success (cache cleared to null), even identical inputs mint a new UUID
    const r4 = checkoutHelpersMod.resolveStableReferenceKey({
      prefix: "admin-credit",
      manualRef: "",
      fingerprint: "u1|60000|hoan-xu",
      cache: null,
      uuidFn: fakeUuid,
    });
    assert.equal(r4.key, "admin-credit-uuid-3");

    // Explicit manual reference_id is preserved verbatim
    const rManual = checkoutHelpersMod.resolveStableReferenceKey({
      prefix: "admin-credit",
      manualRef: "TICKET-2026-99",
      fingerprint: "u1|60000|hoan-xu",
      cache: r4.cache,
      uuidFn: fakeUuid,
    });
    assert.equal(rManual.key, "TICKET-2026-99");
  });

  test("19. [Review R4 #3 P1-1 & #4 P1-2] Gateway prefixed codes normalize accurately across all domain errors; SSE backoff sequence 5s->10s->20s->40s->60s", () => {
    const cases = [
      [
        "COD_LIMIT_EXCEEDED: order total 6000000 VND exceeds COD maximum 5000000 VND",
        "COD_LIMIT_EXCEEDED",
        /hạn mức.*cod|vietqr/i,
      ],
      [
        "COD_REJECTED_LIMIT_EXCEEDED: account has 3 rejected COD deliveries",
        "COD_REJECTED_LIMIT_EXCEEDED",
        /từ chối|cod|vietqr/i,
      ],
      [
        "FLASH_SALE_COD_NOT_ALLOWED: đơn hàng có sản phẩm Flash Sale không hỗ trợ COD",
        "FLASH_SALE_COD_NOT_ALLOWED",
        /flash.*cod|vietqr/i,
      ],
      [
        "FLASH_SALE_NOT_ACTIVE: khung giờ Flash Sale đã kết thúc",
        "FLASH_SALE_NOT_ACTIVE",
        /flash/i,
      ],
      [
        "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT: program limit exceeded",
        "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT",
        /chương trình giảm giá/i,
      ],
      ["INSUFFICIENT_COINS: insufficient coin balance", "INSUFFICIENT_COINS", /xu/i],
      [
        "PROMOTION_BREAKDOWN_MISSING: missing per-shop breakdown",
        "PROMOTION_BREAKDOWN_MISSING",
        /khuyến mãi|đặt lại/i,
      ],
    ];
    for (const [rawMsg, expectedCode, pattern] of cases) {
      const s = errorMapMod.sanitizeGatewayError(409, {
        error: { code: "FAILED_PRECONDITION", message: rawMsg },
      });
      assert.equal(
        s.code,
        expectedCode,
        `expected code ${expectedCode} for "${rawMsg}", got ${s.code}`
      );
      const translated = checkoutMessagesMod.translateCheckoutError(
        new apiMod.ApiError(s.status, s.code, s.message),
        { paymentMethod: "VIETQR", isFlashSale: true }
      );
      assert.ok(
        pattern.test(translated),
        `expected ${pattern} for ${expectedCode}, got: ${translated}`
      );
    }

    // SSE reconnect backoff: 5s, 10s, 20s, 40s, max 60s
    assert.equal(typeof checkoutHelpersMod.getSseReconnectDelayMs, "function");
    assert.equal(checkoutHelpersMod.getSseReconnectDelayMs(0), 5000);
    assert.equal(checkoutHelpersMod.getSseReconnectDelayMs(1), 10000);
    assert.equal(checkoutHelpersMod.getSseReconnectDelayMs(2), 20000);
    assert.equal(checkoutHelpersMod.getSseReconnectDelayMs(3), 40000);
    assert.equal(checkoutHelpersMod.getSseReconnectDelayMs(4), 60000);
    assert.equal(checkoutHelpersMod.getSseReconnectDelayMs(10), 60000);
  });

  test("20. [Review R4 #5 P1-3] Pure buyer & seller order money breakdowns sum strictly to totals; PaymentWaiting fallback subtracts platform_discount_vnd", () => {
    assert.equal(typeof orderMetaMod.buildBuyerOrderBreakdownLines, "function");
    assert.equal(typeof sellerTypesMod.buildSellerOrderBreakdownLines, "function");
    assert.equal(typeof checkoutHelpersMod.computeShopAllocationPayableVnd, "function");

    const sampleOrder = {
      subtotal_vnd: 500000,
      flash_sale_discount_vnd: 0,
      shop_program_discount_vnd: 30000,
      shop_voucher_discount_vnd: 20000,
      platform_voucher_discount_vnd: 25000,
      shipping_fee_vnd: 30000,
      shop_freeship_discount_vnd: 15000,
      freeship_discount_vnd: 10000,
      coins_discount_vnd: 5000,
      commission_vnd: 22500,
      payment_fee_vnd: 4250,
      total_payable_vnd: 425000, // 500k - 30k - 20k - 25k + 30k - 15k - 10k - 5k = 425k
    };

    const buyerLines = orderMetaMod.buildBuyerOrderBreakdownLines(sampleOrder);
    assert.ok(buyerLines.some((l) => l.key === "shop_program" && l.amountVnd === -30000));
    assert.ok(buyerLines.some((l) => l.key === "shop_freeship" && l.amountVnd === -15000));
    const buyerSum = Math.max(
      0,
      buyerLines.reduce((s, l) => s + l.amountVnd, 0)
    );
    assert.equal(buyerSum, sampleOrder.total_payable_vnd);

    const sellerLines = sellerTypesMod.buildSellerOrderBreakdownLines(sampleOrder);
    assert.ok(sellerLines.some((l) => l.key === "shop_program" && l.amountVnd === -30000));
    assert.ok(sellerLines.some((l) => l.key === "shop_freeship" && l.amountVnd === -15000));
    assert.ok(sellerLines.some((l) => l.key === "payment_fee" && l.amountVnd === -4250));
    const sellerSum = Math.max(
      0,
      sellerLines.reduce((s, l) => s + l.amountVnd, 0)
    );
    assert.equal(sellerSum, sellerTypesMod.computeOrderNetVnd(sampleOrder));

    // PaymentWaiting fallback allocation payable subtracts platform_discount_vnd
    assert.equal(
      checkoutHelpersMod.computeShopAllocationPayableVnd({
        subtotal_vnd: 300000,
        shipping_fee_vnd: 25000,
        platform_discount_vnd: 40000,
      }),
      285000
    );
  });

  test("21. [Review R4 #6 P1-4 & #9 P1-7] PDP qty cap ignores shop program per_user_limit & handles flash perUserLimit=0; session state clear matrix", () => {
    assert.equal(typeof checkoutHelpersMod.computePdpQuantityCap, "function");
    assert.equal(typeof checkoutHelpersMod.shouldClearUserClientState, "function");

    // Shop discount per_user_limit must NOT cap qty (buyer can buy above limit at regular price)
    assert.equal(
      checkoutHelpersMod.computePdpQuantityCap({
        skuStock: 12,
        liveFlash: null,
      }),
      12
    );

    // Flash perUserLimit === 0 means unlimited per user, still capped by remainingSlots & skuStock
    assert.equal(
      checkoutHelpersMod.computePdpQuantityCap({
        skuStock: 20,
        liveFlash: { perUserLimit: 0, remainingSlots: 2 },
      }),
      2
    );
    // Flash perUserLimit > 0 caps by min(perUserLimit, remainingSlots, skuStock)
    assert.equal(
      checkoutHelpersMod.computePdpQuantityCap({
        skuStock: 20,
        liveFlash: { perUserLimit: 1, remainingSlots: 5 },
      }),
      1
    );
    assert.equal(checkoutHelpersMod.computePdpQuantityCap({ skuStock: 0, liveFlash: null }), 0);

    // Session transition clear rules (guest -> login must NOT clear!)
    assert.equal(checkoutHelpersMod.shouldClearUserClientState(null, "user-a"), false);
    assert.equal(checkoutHelpersMod.shouldClearUserClientState(null, null), false);
    assert.equal(checkoutHelpersMod.shouldClearUserClientState("user-a", "user-a"), false);
    assert.equal(checkoutHelpersMod.shouldClearUserClientState("user-a", null), true);
    assert.equal(checkoutHelpersMod.shouldClearUserClientState("user-a", "user-b"), true);
  });

  test("22. [Review R4 #10 P1-8 & #12 P2-8] Multi-SKU flash shortfall attributes per flash_sale_item_id without blaming sibling SKU; shop discount warnings translated with SKU name", () => {
    // 2 flash SKUs in same shop; only fi-1 exceeded per-user limit -> fi-2 MUST NOT get previewShortfall
    const twoLineEval = checkoutHelpersMod.evaluateShopFlashLines({
      lines: [
        {
          skuId: "sku-f1",
          quantity: 1,
          regularUnitPriceVnd: 200000,
          flashSaleItemId: "fi-1",
          flashUnitPriceVnd: 150000,
          flashPerUserLimit: 1,
        },
        {
          skuId: "sku-f2",
          quantity: 1,
          regularUnitPriceVnd: 180000,
          flashSaleItemId: "fi-2",
          flashUnitPriceVnd: 140000,
          flashPerUserLimit: 2,
        },
      ],
      previewShopFlashDiscountVnd: 40000, // fi-2 applied (40k), fi-1 dropped (50k shortfall)
      previewWarnings: ["flash sale item fi-1 per-user limit (1) exceeded"],
    });
    assert.equal(twoLineEval["sku-f1"].previewShortfall, true);
    assert.equal(twoLineEval["sku-f1"].needsRegularPriceChoice, true);
    assert.equal(
      twoLineEval["sku-f2"].previewShortfall,
      false,
      "valid sibling flash SKU must not be blamed"
    );
    assert.equal(twoLineEval["sku-f2"].needsRegularPriceChoice, false);

    // 2 flash SKUs in same shop with unexplained shortfall (no item ID in warnings) -> ambiguousShopShortfall=true, neither falsely blamed individually
    const ambiguousEval = checkoutHelpersMod.evaluateShopFlashLines({
      lines: [
        {
          skuId: "sku-a",
          quantity: 1,
          regularUnitPriceVnd: 200000,
          flashSaleItemId: "fi-a",
          flashUnitPriceVnd: 150000,
          flashPerUserLimit: 2,
        },
        {
          skuId: "sku-b",
          quantity: 1,
          regularUnitPriceVnd: 200000,
          flashSaleItemId: "fi-b",
          flashUnitPriceVnd: 150000,
          flashPerUserLimit: 2,
        },
      ],
      previewShopFlashDiscountVnd: 50000,
      previewWarnings: [],
    });
    assert.equal(ambiguousEval["sku-a"].ambiguousShopShortfall, true);
    assert.equal(ambiguousEval["sku-a"].previewShortfall, false);
    assert.equal(ambiguousEval["sku-b"].ambiguousShopShortfall, true);
    assert.equal(ambiguousEval["sku-b"].previewShortfall, false);

    // P2-8: Shop discount warning translator replaces raw UUID with product & SKU code
    assert.equal(typeof sellerTypesMod.translateShopDiscountWarning, "function");
    const lookup = new Map([
      [
        "11111111-2222-3333-4444-555555555555",
        { product: { name: "Áo thun Cotton" }, sku: { sku_code: "AT-TRANG-M", name: "Trắng / M" } },
      ],
    ]);
    const wOverlap = sellerTypesMod.translateShopDiscountWarning(
      "SKU 11111111-2222-3333-4444-555555555555 overlaps with an approved flash sale window; flash sale price will take precedence during the flash window",
      lookup
    );
    assert.ok(wOverlap.includes("Áo thun Cotton") && wOverlap.includes("AT-TRANG-M"));
    assert.equal(wOverlap.includes("11111111-2222-3333-4444-555555555555"), false);

    const wInflation = sellerTypesMod.translateShopDiscountWarning(
      "SKU 11111111-2222-3333-4444-555555555555 had a lower price (150000 VND) in the last 30 days than current price (179000 VND); flagged for price inflation",
      lookup
    );
    assert.ok(wInflation.includes("Áo thun Cotton") && /30 ngày/i.test(wInflation));
    assert.equal(wInflation.includes("11111111-2222-3333-4444-555555555555"), false);
  });

  test("23. [Review R4 #8, #11, #12-P2-5, #14] Dead code removed, mobile admin lists & VietQR error state & apiBlob CSV helper & idempotency conflict UX", () => {
    // Dead code removed
    assert.equal(existsSync(resolve(WEB_SRC, "features/seller/PromotionsBundleTab.tsx")), false);
    const sellerStoreSrc = readFileSync(resolve(WEB_SRC, "features/seller/sellerStore.ts"), "utf8");
    assert.equal(sellerStoreSrc.includes("getShopBankAccount"), false);
    assert.equal(sellerStoreSrc.includes("BANK_ACCOUNTS"), false);
    const searchSrc = readFileSync(resolve(WEB_SRC, "lib/search.ts"), "utf8");
    assert.equal(searchSrc.includes("getInstantSearchSuggestions"), false);
    assert.equal(searchSrc.includes("InstantSearchResult"), false);
    const mockStoreSrc = readFileSync(resolve(WEB_SRC, "mock/MockStoreContext.tsx"), "utf8");
    assert.equal(mockStoreSrc.includes("appliedGiftCardCode"), false);

    // VietQrPreview has no hardcoded fake ACB account or decodeURIComponent bug
    const vietQrSrc = readFileSync(resolve(WEB_SRC, "features/commerce/VietQrPreview.tsx"), "utf8");
    assert.equal(vietQrSrc.includes("2026 8841 9001"), false);
    assert.equal(vietQrSrc.includes("decodeURIComponent"), false);

    // P1-6 mobileList on 3 admin panels + gian-hang mobileOpenId priority
    for (const rel of [
      "features/admin/AdminProductModerationTab.tsx",
      "features/admin/AdminUsersPanel.tsx",
      "features/admin/AdminCategoriesPanel.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(
        src.includes("shared.mobileList"),
        `${rel} must render shared.mobileList for <=720px`
      );
    }
    const gianHangSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    assert.ok(
      /inspectedShopId\s*=\s*mobileOpenId\s*\?\?\s*activeDesktopShop\?\.id/.test(gianHangSrc),
      "admin/gian-hang must prioritize mobileOpenId over fallback activeDesktopShop"
    );

    // P1-11 apiBlob helper with 401 refresh
    assert.equal(typeof apiMod.apiBlob, "function");

    // P1-1 CheckoutClient shows explicit recovery choice on IDEMPOTENCY_KEY_CONFLICT (no auto-rotate!)
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(checkoutSrc.includes("Vẫn đặt đơn mới"));
    assert.equal(
      /IDEMPOTENCY_KEY_CONFLICT[\s\S]{0,120}rotateCheckoutIdempotencyKey/.test(checkoutSrc),
      false,
      "CheckoutClient must NOT auto-rotate key on IDEMPOTENCY_KEY_CONFLICT"
    );
  });

  test("24. [Review R5 #1, #2, #3] PAYMENT_NOT_INITIALIZED ship error, forward-recovery checkout error classification, and token-based CSS Modules without inline styles", () => {
    // #1 PAYMENT_NOT_INITIALIZED from order r6 (409 FAILED_PRECONDITION + prefixed message)
    const payNotInitSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "PAYMENT_NOT_INITIALIZED: order group payment has not finished initializing",
      },
    });
    assert.equal(payNotInitSanitized.status, 409);
    assert.equal(payNotInitSanitized.code, "PAYMENT_NOT_INITIALIZED");
    assert.equal(
      payNotInitSanitized.message,
      "Đơn đang khởi tạo thanh toán, vui lòng thử lại sau ít phút."
    );

    assert.equal(typeof sellerTypesMod.translateSellerShipError, "function");
    assert.equal(
      sellerTypesMod.translateSellerShipError(
        new apiMod.ApiError(409, "PAYMENT_NOT_INITIALIZED", "raw fallback")
      ),
      "Đơn đang khởi tạo thanh toán, vui lòng thử lại sau ít phút."
    );

    for (const rel of [
      "app/(seller)/seller/don-hang/page.tsx",
      "app/(seller)/seller/don-hang/[id]/page.tsx",
    ]) {
      const src = readFileSync(resolve(WEB_SRC, rel), "utf8");
      assert.ok(
        src.includes("translateSellerShipError"),
        `${rel} must translate seller ship errors via translateSellerShipError`
      );
    }

    // #2 Forward recovery pure classifier & BFF status preservation
    const err504 = errorMapMod.sanitizeGatewayError(504, {
      error: { code: "DEADLINE_EXCEEDED", message: "upstream service timed out" },
    });
    assert.equal(err504.status, 504);
    assert.equal(err504.code, "DEADLINE_EXCEEDED");

    assert.equal(typeof checkoutHelpersMod.isAmbiguousCheckoutSubmitError, "function");
    assert.equal(typeof checkoutHelpersMod.classifyCheckoutSubmitError, "function");

    const expectedAmbiguousMsg =
      "Đơn có thể đã được ghi nhận. Vui lòng xem Đơn hàng của tôi trước khi đặt lại.";

    // Gateway gRPC -> HTTP real mappings:
    // Unavailable -> 503 UNAVAILABLE
    // DeadlineExceeded -> 504 DEADLINE_EXCEEDED
    // Aborted -> 409 ABORTED
    // Canceled -> 500 INTERNAL_ERROR (gateway default) -> BFF 503 UNAVAILABLE; or direct 499 CANCELED
    // Network fetch rejection -> TypeError / non-ApiError
    const ambiguousCases = [
      new apiMod.ApiError(
        503,
        "UNAVAILABLE",
        "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."
      ),
      new apiMod.ApiError(
        504,
        "DEADLINE_EXCEEDED",
        "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."
      ),
      new apiMod.ApiError(409, "ABORTED", "Dữ liệu vừa thay đổi, vui lòng tải lại và thử lại."),
      new apiMod.ApiError(500, "INTERNAL_ERROR", "internal server error"),
      new apiMod.ApiError(499, "CANCELED", "request canceled"),
      new apiMod.ApiError(499, "CANCELLED", "request cancelled"),
      new TypeError("Failed to fetch"),
      Object.assign(new Error("flash network abort"), { name: "AbortError" }),
    ];

    for (const err of ambiguousCases) {
      assert.equal(
        checkoutHelpersMod.isAmbiguousCheckoutSubmitError(err),
        true,
        `must treat ${String(err)} as ambiguous_created`
      );
      assert.equal(
        checkoutHelpersMod.classifyCheckoutSubmitError(err, {
          hasFlashSaleItem: true,
          paymentMethod: "VIETQR",
        }),
        "ambiguous_created",
        `must classify ${String(err)} as ambiguous_created even with flash item`
      );
      assert.equal(
        checkoutHelpersMod.isFlashLimitOrSlotError(err, true, "VIETQR"),
        false,
        `must NOT offer key-rotating flash opt-out on ambiguous error ${String(err)}`
      );
      assert.equal(
        checkoutMessagesMod.translateCheckoutError(err, {
          hasVouchers: true,
          isFlashSale: true,
          paymentMethod: "VIETQR",
        }),
        expectedAmbiguousMsg
      );
    }

    // Deterministic errors keep exact non-ambiguous classifications
    const flashErr = new apiMod.ApiError(
      409,
      "FLASH_UNAVAILABLE_ON_COMMIT",
      "Suất Flash sale đã hết lượt."
    );
    assert.equal(checkoutHelpersMod.isAmbiguousCheckoutSubmitError(flashErr), false);
    assert.equal(
      checkoutHelpersMod.classifyCheckoutSubmitError(flashErr, {
        hasFlashSaleItem: true,
        paymentMethod: "VIETQR",
      }),
      "flash_blocked"
    );
    assert.equal(checkoutHelpersMod.isFlashLimitOrSlotError(flashErr, true, "VIETQR"), true);

    const idempErr = new apiMod.ApiError(409, "IDEMPOTENCY_KEY_CONFLICT", "conflict");
    assert.equal(checkoutHelpersMod.isAmbiguousCheckoutSubmitError(idempErr), false);
    assert.equal(
      checkoutHelpersMod.classifyCheckoutSubmitError(idempErr, { hasFlashSaleItem: true }),
      "idempotency_conflict"
    );

    const stockErr = new apiMod.ApiError(409, "INSUFFICIENT_STOCK", "hết hàng");
    assert.equal(checkoutHelpersMod.isAmbiguousCheckoutSubmitError(stockErr), false);
    assert.equal(
      checkoutHelpersMod.classifyCheckoutSubmitError(stockErr, { hasFlashSaleItem: true }),
      "definite_failure"
    );

    const authErr = new apiMod.ApiError(401, "UNAUTHENTICATED", "expired");
    assert.equal(checkoutHelpersMod.isAmbiguousCheckoutSubmitError(authErr), false);
    assert.equal(checkoutHelpersMod.classifyCheckoutSubmitError(authErr), "unauthenticated");

    // #3 No hard-coded inline styles in VietQrPreview and CheckoutClient recovery actions
    const vietQrSrc = readFileSync(resolve(WEB_SRC, "features/commerce/VietQrPreview.tsx"), "utf8");
    assert.equal(vietQrSrc.includes("style={{"), false, "VietQrPreview must not use inline styles");
    assert.ok(vietQrSrc.includes("styles.qrFallbackBox"));
    assert.ok(vietQrSrc.includes("styles.qrFallbackText"));

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.equal(
      /style=\{\{\s*margin(?:Top|Bottom)\s*:/.test(checkoutSrc),
      false,
      "CheckoutClient must not hard-code marginTop/marginBottom inline styles"
    );
    assert.ok(
      checkoutSrc.includes("classifyCheckoutSubmitError"),
      "CheckoutClient must classify submit errors via classifyCheckoutSubmitError"
    );
  });
});
