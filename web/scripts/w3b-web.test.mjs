/**
 * Wave 3b-1 web behavioral & contract verification tests (updated for Manager r4 + revC A–N).
 * Tests real exported pure functions for review URL validation & COMPLETED gating, banned keyword
 * rescan batching/abort/interruption, cart unavailable exclusion, 3,321-ward filtering & selection
 * guards, error-map translations, COD eligibility, seller QUEUED retryability, COD CSV validation &
 * pagination, BFF multipart body cap, and minimal CSP headers.
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

describe("Wave 3b-1 web — behavioral tests (A–N)", () => {
  test("1. [A4/E7/E14/L] parseSafeReviewImageUrl rejects userinfo & Unicode whitespace; rating_avg_x100 formats integer tenths", async () => {
    const reviewHelpers = await import("../src/features/product/reviewHelpers.ts");
    const formatMod = await import("../src/lib/format.ts");

    // E7 + L: strictly https:// or internal /products/..., no userinfo, no Unicode whitespace
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("https://cdn.example.com/reviews/1.jpg"),
      "https://cdn.example.com/reviews/1.jpg"
    );
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("/products/ao-thun-cotton-trang.png"),
      "/products/ao-thun-cotton-trang.png"
    );
    assert.equal(reviewHelpers.parseSafeReviewImageUrl("http://cdn.example.com/1.jpg"), null);
    assert.equal(reviewHelpers.parseSafeReviewImageUrl("javascript:alert(1)"), null);
    assert.equal(reviewHelpers.parseSafeReviewImageUrl("data:image/png;base64,AAA"), null);
    assert.equal(reviewHelpers.parseSafeReviewImageUrl("//evil.example.com/a.jpg"), null);
    assert.equal(reviewHelpers.parseSafeReviewImageUrl("/products/../etc/passwd"), null);
    assert.equal(reviewHelpers.parseSafeReviewImageUrl("/admin/secret.png"), null);
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("https://user:pass@cdn.example.com/a.jpg"),
      null,
      "must reject URL with username & password"
    );
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("https://user@cdn.example.com/a.jpg"),
      null,
      "must reject URL with username"
    );
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("https://cdn.example.com/a\u00A0b.jpg"),
      null,
      "must reject NBSP Unicode whitespace"
    );
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("https://cdn.example.com/a\u2003b.jpg"),
      null,
      "must reject EM SPACE Unicode whitespace"
    );
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl("/products/ao\u00A0thun.png"),
      null,
      "must reject Unicode whitespace in internal /products/ path"
    );
    assert.equal(
      reviewHelpers.parseSafeReviewImageUrl(`https://cdn.example.com/${"a".repeat(500)}`),
      null
    );

    const okUrls = reviewHelpers.validateReviewImageUrls([
      "https://cdn.example.com/1.jpg",
      "/products/2.png",
    ]);
    assert.equal(okUrls.error, null);
    assert.deepEqual(okUrls.urls, ["https://cdn.example.com/1.jpg", "/products/2.png"]);

    const tooMany = reviewHelpers.validateReviewImageUrls(
      Array.from({ length: 6 }, (_, i) => `https://cdn.example.com/${i + 1}.jpg`)
    );
    assert.ok(tooMany.error, "more than 5 review image URLs must fail validation");
    assert.ok(
      reviewHelpers.validateReviewImageUrls(["https://user:pass@cdn.example.com/1.jpg"]).error,
      "userinfo URL must fail validateReviewImageUrls"
    );

    // E14: integer rating_avg_x100 formatting
    assert.equal(formatMod.formatRatingFromX100(500), "5,0");
    assert.equal(formatMod.formatRatingFromX100(485), "4,9");
    assert.equal(formatMod.formatRatingFromX100(474), "4,7");
    assert.equal(formatMod.formatRatingFromX100(0), "0,0");
  });

  test("2. [A4/E6-bis/E14/N] Dead mock review/bundle code removed; SitePdpView reviews are E6-bis safe", () => {
    assert.equal(
      existsSync(resolve(WEB_SRC, "mock/MockStoreContext.tsx")),
      false,
      "MockStoreContext.tsx dead mock file must be removed"
    );

    const cardSrc = readSrc("features/site/SiteProductCard.tsx");
    assert.equal(cardSrc.includes("computeReviewSummary"), false);
    assert.equal(/\breviews\b/.test(cardSrc), false);

    const pdpViewSrc = readSrc("features/site/SitePdpView.tsx");
    assert.equal(pdpViewSrc.includes("SAMPLE_REVIEWS"), false);
    assert.ok(
      pdpViewSrc.includes("formatRatingFromX100") && pdpViewSrc.includes("rating_avg_x100")
    );
    assert.ok(pdpViewSrc.includes('referrerPolicy="no-referrer"'));
    assert.equal(
      pdpViewSrc.includes(".buyer_id") ||
        pdpViewSrc.includes(".order_id") ||
        pdpViewSrc.includes(".order_item_id") ||
        pdpViewSrc.includes(".sku_id"),
      false,
      "SitePdpView reviews must not depend on E6-bis stripped fields"
    );
  });

  test("3. [A4/E5-E8] Buyer review eligibility strictly requires COMPLETED status & blocks duplicate order_item_id", async () => {
    const reviewHelpers = await import("../src/features/product/reviewHelpers.ts");

    assert.equal(reviewHelpers.canReviewOrderStatus("COMPLETED"), true);
    assert.equal(reviewHelpers.canReviewOrderStatus("completed"), true);
    for (const nonCompleted of [
      "PENDING",
      "CONFIRMED",
      "PREPARING",
      "SHIPPING",
      "DELIVERED",
      "CANCELLED",
      "RETURN_REQUESTED",
      "RETURNED",
      "",
      undefined,
    ]) {
      assert.equal(
        reviewHelpers.canReviewOrderStatus(nonCompleted),
        false,
        `order status ${String(nonCompleted)} must NOT be eligible for review`
      );
    }

    assert.equal(reviewHelpers.isOrderItemReviewed("item-1", [{ order_item_id: "item-1" }]), true);
    assert.equal(reviewHelpers.isOrderItemReviewed("item-2", [{ order_item_id: "item-1" }]), false);

    // Verify OrderDetailClientView gates review UI & submission through canReviewOrderStatus
    const detailSrc = readSrc("features/account/OrderDetailClientView.tsx");
    assert.ok(
      detailSrc.includes("canReviewOrderStatus(order.backendStatus)"),
      "OrderDetailClientView must gate review form and submission via canReviewOrderStatus(order.backendStatus)"
    );
    assert.ok(detailSrc.includes("validateReviewImageUrls"));
  });

  test("4. [A4/E13/N] Discontinued PDP sets robots noindex and displays discontinued state", () => {
    const slugPageSrc = readSrc("app/(site)/s/[host]/san-pham/[slug]/page.tsx");
    assert.ok(slugPageSrc.includes("generateMetadata"));
    assert.ok(slugPageSrc.includes("index: false"));
    assert.ok(slugPageSrc.includes("ngừng kinh doanh"));
  });

  test("5. [A4/E9-E12/H/J/M] Rescan batching supports AbortController stop & mid-loop interruption; seller/admin moderation checks", async () => {
    const kwHelper = await import("../src/features/admin/bannedKeywordsHelper.ts");

    // 5a. Normal 2-batch cursor loop with sample cap
    const calls = [];
    const res = await kwHelper.runBannedKeywordRescanBatches(
      async (req) => {
        calls.push(req);
        if (calls.length === 1) {
          return {
            scanned_count: 500,
            blocked_count: 3,
            flagged_count: 2,
            samples: [
              {
                product_id: "p1",
                shop_id: "s1",
                name: "SP 1",
                slug: "sp-1",
                status: "REJECTED",
                matched_terms: ["hang gia"],
                highest_action: "BLOCK",
              },
            ],
            has_more: true,
            next_cursor: "cur-500",
          };
        }
        return {
          scanned_count: 120,
          blocked_count: 1,
          flagged_count: 4,
          samples: [
            {
              product_id: "p2",
              shop_id: "s2",
              name: "SP 2",
              slug: "sp-2",
              status: "PENDING",
              matched_terms: ["sao chep"],
              highest_action: "REVIEW",
            },
          ],
          has_more: false,
          next_cursor: "",
        };
      },
      { dryRun: false }
    );
    assert.equal(calls.length, 2);
    assert.equal(calls[0].limit, 500);
    assert.equal(calls[1].cursor, "cur-500");
    assert.equal(res.scannedCount, 620);
    assert.equal(res.aborted, false);

    // 5b. AbortController stops loop immediately after batch 1 (never invokes batch 2)
    const abortCtrl = new AbortController();
    let abortCalls = 0;
    const abortedRes = await kwHelper.runBannedKeywordRescanBatches(
      async () => {
        abortCalls += 1;
        abortCtrl.abort();
        return {
          scanned_count: 500,
          blocked_count: 1,
          flagged_count: 0,
          samples: [],
          has_more: true,
          next_cursor: "cur-2",
        };
      },
      { dryRun: true, signal: abortCtrl.signal }
    );
    assert.equal(abortCalls, 1, "aborted rescan must not execute batch 2");
    assert.equal(abortedRes.aborted, true);
    assert.equal(abortedRes.batches, 1);

    // 5c. Error on batch 2 throws RescanBatchInterruptedError with failedBatch=2 and partial progress
    let failCalls = 0;
    await assert.rejects(
      () =>
        kwHelper.runBannedKeywordRescanBatches(async () => {
          failCalls += 1;
          if (failCalls === 1) {
            return {
              scanned_count: 500,
              blocked_count: 2,
              flagged_count: 1,
              samples: [],
              has_more: true,
              next_cursor: "cur-fail",
            };
          }
          throw new Error("gateway 503");
        }),
      (err) => {
        assert.ok(err instanceof kwHelper.RescanBatchInterruptedError);
        assert.equal(err.failedBatch, 2);
        assert.equal(err.partialProgress.batches, 1);
        assert.equal(err.partialProgress.scannedCount, 500);
        return true;
      }
    );

    // 5d. Independent assertions (no || mask) for seller form, admin banned tab, kiem-duyet KPI & mock labels, categories
    const sellerFormSrc = readSrc("features/seller/ProductFormView.tsx");
    assert.ok(
      sellerFormSrc.includes("flagged_keywords"),
      "ProductFormView must render flagged_keywords"
    );
    assert.ok(
      sellerFormSrc.includes("is_prohibited"),
      "ProductFormView must disable is_prohibited categories"
    );

    const kwTabSrc = readSrc("features/admin/AdminBannedKeywordsTab.tsx");
    assert.ok(
      kwTabSrc.includes("AbortController"),
      "AdminBannedKeywordsTab must abort on stop/unmount"
    );
    assert.ok(
      kwTabSrc.includes("Gián đoạn ở lô"),
      "AdminBannedKeywordsTab must report mid-loop interruption"
    );
    assert.ok(
      kwTabSrc.includes("m-kw-edit-active-"),
      "Mobile edit card must include active toggle"
    );

    const kiemDuyetSrc =
      readSrc("app/(admin)/admin/kiem-duyet/page.tsx") +
      "\n" +
      readSrc("app/(admin)/admin/kiem-duyet/AdminModerationClient.tsx");
    assert.ok(
      kiemDuyetSrc.includes("/api/admin/banned-keywords?page=1&page_size=1") ||
        kiemDuyetSrc.includes("/api/v1/admin/banned-keywords"),
      "admin/kiem-duyet must load active banned keyword KPI independently in parent"
    );
    // Mock tabs ("Đánh giá sản phẩm (mẫu)", "Media biến thể (mẫu)") removed per DECISIONS.

    const catsSrc = readSrc("features/admin/AdminCategoriesPanel.tsx");
    assert.ok(catsSrc.includes("is_prohibited"));
    assert.ok(catsSrc.includes("Danh mục hàng cấm"));
  });

  test("6. [Cart/E3-E4/M] Unavailable cart items are flagged by cartEnrich and excluded from selection/totals in CartClient", async () => {
    const cartEnrich = await import("../src/features/commerce/cartEnrich.ts");

    const groups = cartEnrich.enrichCartGroups(
      [
        {
          shop_id: "shop-1",
          shop_name: "Shop A",
          subtotal_vnd: 400000,
          total_weight_grams: 600,
          items: [
            {
              sku_id: "sku-ok",
              product_id: "p1",
              shop_id: "shop-1",
              product_name: "Áo khả dụng",
              sku_name: "Trắng / M",
              image_url: "/products/1.png",
              price_vnd: 200000,
              added_price_vnd: 200000,
              price_changed: false,
              quantity: 2,
              available_stock: 5,
              weight_grams: 300,
              selected: true,
              unavailable: false,
            },
            {
              sku_id: "sku-gone",
              product_id: "p2",
              shop_id: "shop-1",
              product_name: "Áo ngừng bán",
              sku_name: "Đen / L",
              image_url: "/products/2.png",
              price_vnd: 150000,
              added_price_vnd: 150000,
              price_changed: false,
              quantity: 3,
              available_stock: 0,
              weight_grams: 300,
              selected: false,
              unavailable: true,
              unavailable_reason: "SKU_INACTIVE",
            },
          ],
        },
      ],
      [],
      []
    );

    const [okItem, goneItem] = groups[0].items;
    assert.equal(okItem.isUnavailable, false);
    assert.equal(okItem.isOutOfStock, false);
    assert.equal(goneItem.isUnavailable, true);
    assert.equal(goneItem.unavailableReason, "SKU_INACTIVE");
    assert.equal(goneItem.isOutOfStock, false);

    const cartPageSrc = readSrc("app/(site)/s/[host]/gio-hang/CartClient.tsx");
    assert.ok(
      cartPageSrc.includes("availableItems") && cartPageSrc.includes("unavailableCount"),
      "CartClient must separate availableItems and unavailableCount"
    );
    assert.equal(
      cartPageSrc.includes('Chọn tất cả (<strong className="num">{cartItems.length}'),
      false,
      "Select-All label must not count unavailable items"
    );
  });

  test("7. [A5/E15-E16/C] Pre-normalized 3,321-ward filtering, no silent wards[0] default, legacy inactive warning & filter clear", async () => {
    const addrHelpers = await import("../src/features/commerce/addressHelpers.ts");

    // Pre-normalize & filter across 3,321 wards
    const bigWards = Array.from({ length: 3321 }, (_, idx) => ({
      code: String(10000 + idx),
      name: idx === 42 ? "Phường Bến Nghé" : idx === 2033 ? "Đặc khu Hoàng Sa" : `Xã Mẫu Số ${idx}`,
      province_code: "79",
      cod_supported: idx !== 2033,
    }));
    const prepared = addrHelpers.prepareSearchableWards(bigWards);
    const benNghe = addrHelpers.filterPreparedWards(prepared, "ben nghe");
    assert.equal(benNghe.length, 1);
    assert.equal(benNghe[0].code, "10042");
    const hoangSa = addrHelpers.filterPreparedWards(prepared, "hoang sa");
    assert.equal(hoangSa.length, 1);
    assert.equal(hoangSa[0].cod_supported, false);

    // Never silently default to wards[0] when creating new address
    const newAddrSel = addrHelpers.resolveLoadedWardSelection({
      wards: bigWards.slice(0, 5),
      currentWardCode: "",
      provinceCode: "79",
    });
    assert.equal(newAddrSel.wardCode, "", "must never silently pick wards[0]");
    assert.equal(newAddrSel.legacyWardInactive, false);

    // Editing old address whose ward is no longer active -> empty + legacyWardInactive flag
    const inactiveSel = addrHelpers.resolveLoadedWardSelection({
      wards: bigWards.slice(0, 5),
      currentWardCode: "OLD-INACTIVE-999",
      initialProvinceCode: "79",
      initialWardCode: "OLD-INACTIVE-999",
      provinceCode: "79",
    });
    assert.equal(inactiveSel.wardCode, "");
    assert.equal(inactiveSel.legacyWardInactive, true);

    // Editing address whose ward is still active -> keep it
    const activeSel = addrHelpers.resolveLoadedWardSelection({
      wards: bigWards.slice(0, 5),
      currentWardCode: "",
      initialProvinceCode: "79",
      initialWardCode: "10003",
      provinceCode: "79",
    });
    assert.equal(activeSel.wardCode, "10003");
    assert.equal(activeSel.legacyWardInactive, false);

    // Filtering out the currently selected ward clears wardCode so form cannot submit stale hidden ward
    const cleared = addrHelpers.reconcileWardAfterFilter(benNghe, "10000");
    assert.equal(cleared.wardCode, "");
    assert.equal(cleared.clearedByFilter, true);

    const kept = addrHelpers.reconcileWardAfterFilter(benNghe, "10042");
    assert.equal(kept.wardCode, "10042");
    assert.equal(kept.clearedByFilter, false);

    // r9 #5: Legacy address codes are sent ONLY when map-legacy succeeded AND province/ward unchanged
    const mappedCodes = {
      provinceCode: "74",
      districtCode: "718",
      wardCode: "25741",
      targetProvinceCode: "79",
      targetWardCode: "25741",
    };
    assert.deepEqual(addrHelpers.resolveLegacyCodesForPayload(mappedCodes, "79", "25741"), {
      legacy_province_code: "74",
      legacy_district_code: "718",
      legacy_ward_code: "25741",
    });
    assert.deepEqual(
      addrHelpers.resolveLegacyCodesForPayload(mappedCodes, "01", "25741"),
      {},
      "changing province after map-legacy must clear legacy_*_code"
    );
    assert.deepEqual(
      addrHelpers.resolveLegacyCodesForPayload(mappedCodes, "79", "99999"),
      {},
      "changing ward after map-legacy must clear legacy_*_code"
    );
    assert.deepEqual(addrHelpers.resolveLegacyCodesForPayload(null, "79", "25741"), {});

    // Native <select> without custom combobox (AddressForm delegates to AddressFieldsForm with idPrefix="addr")
    const addrFormSrc = readSrc("features/commerce/AddressForm.tsx");
    const addrFieldsSrc = readSrc("features/commerce/AddressFieldsForm.tsx");
    const combinedAddrSrc = `${addrFormSrc}\n${addrFieldsSrc}`;
    assert.equal(
      combinedAddrSrc.includes('role="combobox"'),
      false,
      "custom combobox must be removed"
    );
    assert.equal(
      combinedAddrSrc.includes('role="listbox"'),
      false,
      "custom listbox must be removed"
    );
    assert.ok(addrFormSrc.includes('idPrefix="addr"'));
    assert.ok(addrFieldsSrc.includes("`${idPrefix}-ward-search`"));
    assert.ok(addrFieldsSrc.includes("`${idPrefix}-ward`"));
    assert.ok(addrFieldsSrc.includes('aria-live="polite"'));
    assert.ok(addrFieldsSrc.includes("Chọn phường/xã"));
    assert.ok(addrFormSrc.includes("Phường/xã cũ không còn trong danh mục mới, chọn lại."));
    assert.ok(addrFormSrc.includes("resolveLegacyCodesForPayload"));
    assert.equal(
      addrFormSrc.includes("{ legacy_province:"),
      false,
      "AddressForm submit must send legacy_*_code from map-legacy, never raw typed legacy_province name"
    );
  });

  test("8. [A5/E17-E17-bis/K] PDP shipping-quote uses retryOn401: isAuthenticated without fake carriers", () => {
    const pdpClientSrc = readSrc("features/site/SitePdpView.tsx");
    assert.equal(pdpClientSrc.includes("estimateDelivery"), false);
    assert.equal(pdpClientSrc.includes("NexusFast") || pdpClientSrc.includes('"SPX"'), false);
    assert.ok(pdpClientSrc.includes("/api/checkout/shipping-quote"));
    assert.ok(
      pdpClientSrc.includes("retryOn401: isAuthenticated"),
      "PDP shipping-quote must pass retryOn401: isAuthenticated so logged-in users refresh expired tokens"
    );
  });

  test("9. [A5/E19/B/D/G] error-map translates phone OTP 429, phone-in-use, BANNED_CONTENT, CSV lines, future date, ADDRESS_WARD_REQUIRED; server decides COD", async () => {
    const errMap = await import("../src/lib/server/error-map.ts");
    const checkoutHelpers = await import("../src/features/checkout/helpers.ts");

    // Phone OTP 429 cooldown with seconds — must NOT fall into bank OTP message
    const otpCooldown = errMap.sanitizeGatewayError(429, {
      error: {
        code: "RESOURCE_EXHAUSTED",
        message: "please wait 45 seconds before requesting another phone otp",
      },
    });
    assert.ok(otpCooldown.message.includes("45 giây"), `got: ${otpCooldown.message}`);
    assert.equal(
      otpCooldown.message.includes("ngân hàng"),
      false,
      `leaked bank message: ${otpCooldown.message}`
    );

    // Phone OTP 429 hourly limit — must NOT fall into bank OTP message
    const otpHourly = errMap.sanitizeGatewayError(429, {
      error: {
        code: "RESOURCE_EXHAUSTED",
        message: "too many phone otp requests; please try again later",
      },
    });
    assert.ok(otpHourly.message.includes("số điện thoại"), `got: ${otpHourly.message}`);
    assert.equal(
      otpHourly.message.includes("ngân hàng"),
      false,
      `leaked bank message: ${otpHourly.message}`
    );

    // Phone already in use
    const phoneInUse = errMap.sanitizeGatewayError(409, {
      error: { code: "ALREADY_EXISTS", message: "phone is already in use" },
    });
    assert.ok(
      phoneInUse.message.includes("Số điện thoại này đã được sử dụng"),
      `got: ${phoneInUse.message}`
    );

    // BANNED_CONTENT on review — zero English leaked
    const bannedRev = errMap.sanitizeGatewayError(400, {
      error: {
        code: "INVALID_ARGUMENT",
        message: "BANNED_CONTENT: review contains prohibited content",
      },
    });
    assert.equal(bannedRev.code, "BANNED_CONTENT");
    assert.equal(
      /review contains|prohibited content/i.test(bannedRev.message),
      false,
      `leaked English: ${bannedRev.message}`
    );

    // BANNED_CONTENT on product — keeps keyword list ONLY, zero English sentence prefix
    const bannedProd = errMap.sanitizeGatewayError(400, {
      error: {
        code: "INVALID_ARGUMENT",
        message: "BANNED_CONTENT: product contains banned keywords: hang gia, zalo rieng",
      },
    });
    assert.ok(bannedProd.message.includes("hang gia, zalo rieng"), `got: ${bannedProd.message}`);
    assert.equal(
      /product contains/i.test(bannedProd.message),
      false,
      `leaked English: ${bannedProd.message}`
    );

    // Per-line CSV errors -> "Dòng N: ..."
    const csvAmountErr = errMap.sanitizeGatewayError(400, {
      error: {
        code: "INVALID_ARGUMENT",
        message: "line 2: cod_amount_vnd must contain digits only",
      },
    });
    assert.ok(csvAmountErr.message.startsWith("Dòng 2:"), `got: ${csvAmountErr.message}`);
    assert.ok(csvAmountErr.message.includes("số nguyên VND"), `got: ${csvAmountErr.message}`);

    const csvTrackErr = errMap.sanitizeGatewayError(400, {
      error: { code: "INVALID_ARGUMENT", message: "line 7: invalid tracking_code" },
    });
    assert.ok(csvTrackErr.message.startsWith("Dòng 7:"), `got: ${csvTrackErr.message}`);

    // Future reconciliation date
    const futureDateErr = errMap.sanitizeGatewayError(400, {
      error: {
        code: "INVALID_ARGUMENT",
        message:
          "reconciliation date 2099-01-01 cannot be in the future (today in Asia/Ho_Chi_Minh is 2026-11-24)",
      },
    });
    assert.ok(futureDateErr.message.includes("tương lai"), `got: ${futureDateErr.message}`);

    // ADDRESS_WARD_REQUIRED code normalization
    const wardReq = errMap.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message:
          "ADDRESS_WARD_REQUIRED: shipping address must specify both province_code and ward_code",
      },
    });
    assert.equal(wardReq.code, "ADDRESS_WARD_REQUIRED");
    assert.ok(wardReq.message.includes("Phường/Xã"), `got: ${wardReq.message}`);

    // COD helper advisory checks
    assert.equal(
      checkoutHelpers.isCodAllowedForCheckout({
        totalPayableVnd: 200000,
        hasFlashSaleItem: false,
        codRejectedCount: 0,
        phoneVerified: false,
        codAreaSupported: true,
      }).blockCode,
      "PHONE_NOT_VERIFIED"
    );
    assert.equal(
      checkoutHelpers.isCodAllowedForCheckout({
        totalPayableVnd: 200000,
        hasFlashSaleItem: false,
        codRejectedCount: 0,
        phoneVerified: true,
        codAreaSupported: false,
      }).blockCode,
      "AREA_NOT_SUPPORTED"
    );

    // B: CheckoutClient reads live session user and never client-blocks non-Flash COD in handlePlaceOrder
    const checkoutSrc = readSrc("app/(site)/s/[host]/thanh-toan/CheckoutClient.tsx");
    assert.ok(
      checkoutSrc.includes("useSession()"),
      "CheckoutClient must sync phone_verified from useSession()"
    );
    assert.equal(
      checkoutSrc.includes('codCheck.blockCode === "PHONE_NOT_VERIFIED"'),
      false,
      "CheckoutClient handlePlaceOrder must not hard-return on PHONE_NOT_VERIFIED (server is source of truth)"
    );

    // G & r9 #4: AccountClientView uses expires_in_seconds, accepts +84, sets one-time-code & SMS-mock hint
    const accountSrc = readSrc("features/account/AccountClientView.tsx");
    assert.ok(accountSrc.includes("expires_in_seconds"));
    assert.ok(accountSrc.includes("isValidVietnamesePhone(cleanPhone)"));
    assert.ok(accountSrc.includes('autoComplete="one-time-code"'));
    assert.ok(accountSrc.includes("SMS mô phỏng, xem trong email tài khoản"));
  });

  test("10. [A5/E18/A] canSellerShipOrder never locks QUEUED orders and shows retry-now label", async () => {
    const sellerTypes = await import("../src/features/seller/types.ts");

    assert.equal(
      sellerTypes.canSellerShipOrder("CONFIRMED", ""),
      true,
      "CONFIRMED without tracking must be shippable"
    );
    assert.equal(
      sellerTypes.canSellerShipOrder("PREPARING", ""),
      true,
      "PREPARING without tracking (including QUEUED) must remain retryable"
    );
    assert.equal(
      sellerTypes.canSellerShipOrder("PREPARING", "GHN123456"),
      false,
      "once tracking_code exists, ship button must hide"
    );
    assert.equal(sellerTypes.canSellerShipOrder("CANCELLED", ""), false);

    const queuedMeta = sellerTypes.getSellerOrderDisplayMeta({
      status: "PREPARING",
      tracking_code: "",
      shipment_job_status: "QUEUED",
    });
    assert.equal(queuedMeta.label, "Đang chờ tạo lại với hãng");
    assert.equal(queuedMeta.tone, "warn");

    const sellerDetailSrc = readSrc("features/seller/SellerOrderDetailClient.tsx");
    assert.ok(
      sellerDetailSrc.includes("Đơn đang chờ tạo vận đơn với hãng vận chuyển") ||
        sellerDetailSrc.includes("Đang chờ tạo lại với hãng"),
      "SellerOrderDetailClient must show queued shipment banner"
    );
    assert.ok(sellerDetailSrc.includes("Thử tạo lại ngay"));
    // r9 #6: QUEUED card must NOT duplicate the "Thử tạo lại ngay" button (only header button)
    assert.equal(
      sellerDetailSrc.match(/"Thử tạo lại ngay"/g)?.length ?? 0,
      1,
      "seller order detail must render only one 'Thử tạo lại ngay' button in top header"
    );

    const sellerListSrc = readSrc("features/seller/SellerOrdersClient.tsx");
    assert.ok(sellerListSrc.includes("Thử tạo lại ngay"));
  });

  test("11. [A5/E20/E20-bis/E/F/J] COD CSV validation tolerates quoted headers, paginates 50 rows, hides mock_express, and BFF grants +64 KiB multipart headroom", async () => {
    const codHelper = await import("../src/features/admin/codReconciliationHelper.ts");
    const proxySec = await import("../src/lib/server/proxy-security.ts");
    const adminTypes = await import("../src/features/admin/types.ts");

    // Quoted RFC 4180 headers & UTF-8 BOM must pass client pre-check
    const quotedCsv =
      '\uFEFF"tracking_code","cod_amount_vnd","delivered_at"\n"GHN001","250000","2026-11-24T10:00:00+07:00"\n';
    const quotedRes = codHelper.validateCodCsvText(quotedCsv);
    assert.equal(quotedRes.error, null, `quoted RFC 4180 CSV must pass, got: ${quotedRes.error}`);
    assert.equal(quotedRes.rowCount, 1);

    // Header-only or > 5,000 rows rejected
    assert.ok(codHelper.validateCodCsvText("tracking_code,cod_amount_vnd,delivered_at\n").error);
    const over5000 = [
      "tracking_code,cod_amount_vnd,delivered_at",
      ...Array.from({ length: 5001 }, (_, i) => `GHN${i},100000,2026-11-24`),
    ].join("\n");
    assert.ok(codHelper.validateCodCsvText(over5000).error, ">5000 rows must fail");

    // Pagination 50 rows per page
    const rows5000 = Array.from({ length: 5000 }, (_, i) => i + 1);
    const p1 = codHelper.paginateCodItems(rows5000, 1, 50);
    assert.equal(p1.totalPages, 100);
    assert.equal(p1.slice.length, 50);
    assert.equal(p1.slice[0], 1);
    const p100 = codHelper.paginateCodItems(rows5000, 999, 50);
    assert.equal(p100.page, 100);
    assert.equal(p100.slice[49], 5000);

    // r9 #2 & r10 E.2: Single shared carrier display function across buyer, seller & admin
    const fmtMod = await import("../src/lib/format.ts");
    assert.equal(
      fmtMod.formatCarrierLabel("ghn", "Giao Hang Nhanh (GHN)"),
      "Giao Hàng Nhanh (GHN)"
    );
    assert.equal(fmtMod.formatCarrierLabel("mock_express", "Mock Express"), "Chuyển phát Nexus");
    assert.equal(fmtMod.formatCarrierLabel("ghtk", "Giao Hàng Tiết Kiệm"), "Giao Hàng Tiết Kiệm");
    assert.equal(/mock_express/i.test(fmtMod.formatCarrierLabel("mock_express")), false);
    assert.equal(fmtMod.formatCarrierLabel("custom_carrier", "Đơn vị mới"), "Đơn vị mới");

    // r9 #3: COD reconciliation notes & queue reasons translated to Vietnamese (unknown verbatim)
    assert.equal(
      codHelper.formatReconciliationReason("cod_missing_local"),
      "Không tìm thấy đơn COD trên hệ thống"
    );
    assert.equal(
      codHelper.formatReconciliationReason("cod_amount_mismatch"),
      "Lệch số tiền COD giữa sàn và file hãng"
    );
    assert.equal(
      codHelper.formatReconciliationReason("cod_not_delivered"),
      "Đơn COD chưa giao thành công"
    );
    assert.equal(
      codHelper.formatReconciliationReason("cod_order_cancelled"),
      "Đơn COD đã hủy, không thể đối soát"
    );
    assert.equal(
      codHelper.formatReconciliationReason("cod_carrier_mismatch"),
      "Sai hãng vận chuyển so với đơn trên sàn"
    );
    assert.equal(
      codHelper.formatReconciliationReason("cod_invalid_row"),
      "Dòng CSV thiếu mã vận đơn"
    );
    assert.equal(
      codHelper.formatReconciliationReason("unknown_custom_code"),
      "unknown_custom_code"
    );
    assert.ok(
      codHelper
        .formatReconciliationNote("matched and cleared cod_receivable into gateway_clearing")
        .includes("Khớp số tiền")
    );
    const mismatchNote = codHelper.formatReconciliationNote(
      "COD amount mismatch: local=250000 VND vs carrier=240000 VND"
    );
    assert.ok(mismatchNote.includes("Lệch tiền COD"), `got: ${mismatchNote}`);
    assert.equal(/COD amount mismatch/i.test(mismatchNote), false);
    assert.ok(
      codHelper.formatReconciliationNote("auto_closed:cod_file_matched").includes("Tự đóng")
    );
    assert.equal(
      codHelper.formatReconciliationNote("Kế toán đã đối chiếu sao kê thủ công"),
      "Kế toán đã đối chiếu sao kê thủ công"
    );

    // E: BFF +64 KiB headroom ONLY for POST admin/reconciliation/cod/import with multipart/form-data
    const mpLimit = proxySec.resolveMaxBffBodyBytes(
      "POST",
      ["admin", "reconciliation", "cod", "import"],
      "multipart/form-data; boundary=----abc"
    );
    assert.equal(mpLimit, 2 * 1024 * 1024 + 64 * 1024);
    assert.equal(
      proxySec.resolveMaxBffBodyBytes(
        "POST",
        ["admin", "reconciliation", "cod", "import"],
        "text/csv"
      ),
      2 * 1024 * 1024
    );
    assert.equal(
      proxySec.resolveMaxBffBodyBytes("POST", ["checkout"], "multipart/form-data; boundary=x"),
      2 * 1024 * 1024
    );

    // Behavioral readBoundedBody check with 2 MiB + 500 B multipart payload
    const nearLimitPayload = new Uint8Array(2 * 1024 * 1024 + 500);
    const mpReq = new Request("http://localhost/api/gw/admin/reconciliation/cod/import", {
      method: "POST",
      headers: { "Content-Type": "multipart/form-data; boundary=x" },
      body: nearLimitPayload,
    });
    const okRead = await proxySec.readBoundedBody(mpReq, mpLimit);
    assert.equal(okRead.ok, true, "2 MiB + 500 B multipart must pass +64 KiB cap");

    const overPayload = new Uint8Array(mpLimit + 1);
    const overReq = new Request("http://localhost/api/gw/admin/reconciliation/cod/import", {
      method: "POST",
      headers: { "Content-Type": "multipart/form-data; boundary=x" },
      body: overPayload,
    });
    const failRead = await proxySec.readBoundedBody(overReq, mpLimit);
    assert.equal(failRead.ok, false);
    if (!failRead.ok) assert.equal(failRead.status, 413);

    assert.ok(adminTypes.RECONCILE_ITEM_STATUS_META.ALREADY_RECONCILED);
    assert.ok(adminTypes.RECONCILE_ITEM_STATUS_META.NOT_DELIVERED);

    const doiSoatSrc =
      readSrc("app/(admin)/admin/doi-soat/page.tsx") +
      "\n" +
      readSrc("features/admin/AdminReconciliationClient.tsx");
    assert.equal(doiSoatSrc.includes("phien_mau"), false);
    assert.equal(doiSoatSrc.includes("ADMIN_RECONCILIATION"), false);
    assert.equal(doiSoatSrc.includes("(mock_express)"), false);
    assert.ok(
      doiSoatSrc.includes("/api/v1/admin/carriers") || doiSoatSrc.includes("/api/admin/carriers"),
      "doi-soat must load carriers from GET /admin/carriers"
    );
    assert.ok(doiSoatSrc.includes("paginateCodItems"));
    assert.ok(
      /\bmd:hidden\b/.test(doiSoatSrc) &&
        doiSoatSrc.includes("<IndexRowCard") &&
        doiSoatSrc.includes("<Sheet") &&
        doiSoatSrc.includes("setDrawerTarget"),
      "doi-soat must support mobile list (md:hidden + IndexRowCard) & detail Sheet inspection"
    );
  });

  test("12. [A5/E21/I/J + Form rules] Minimal CSP in next.config.ts, no hardcoded carrier rates, full-width detailField select, no step>1", async () => {
    const nextConfigMod = await import("../next.config.ts");
    const cfg = nextConfigMod.default;
    const headerRules = await cfg.headers();
    const globalRule = headerRules.find((r) => r.source === "/:path*");
    assert.ok(globalRule, "next.config.ts must define /:path* header rule");
    assert.equal(
      globalRule.headers.some((h) => h.key === "Content-Security-Policy"),
      false,
      "next.config.ts /:path* must NOT set Content-Security-Policy (would overwrite BFF sandbox CSP on /api/gw/shipping/labels/*)"
    );
    const cspRule = headerRules.find((r) =>
      r.headers.some((h) => h.key === "Content-Security-Policy")
    );
    assert.ok(cspRule, "next.config.ts must define a dedicated non-/api CSP rule");
    assert.ok(
      cspRule.source.includes("(?!api"),
      `CSP rule source must exclude /api/* routes, got: ${cspRule.source}`
    );
    const cspHeader = cspRule.headers.find((h) => h.key === "Content-Security-Policy");
    assert.ok(cspHeader, "next.config.ts must set Content-Security-Policy");
    for (const directive of [
      "object-src 'none'",
      "base-uri 'self'",
      "frame-ancestors 'none'",
      "form-action 'self'",
      "img-src 'self' https: data: blob:",
    ]) {
      assert.ok(cspHeader.value.includes(directive), `CSP missing directive: ${directive}`);
    }

    const carriersSrc = readSrc("features/admin/AdminCarriersPanel.tsx");
    for (const forbiddenDefault of ["18000", "25000", "32000", "5000"]) {
      assert.equal(carriersSrc.includes(forbiddenDefault), false);
    }

    for (const [rel, triggerPattern] of [
      ["features/admin/AdminReturnsClient.tsx", 'id={feeId} className="w-full'],
      ["features/admin/AdminUsersPanel.tsx", 'id="usr-role-sel" className="w-full'],
      ["features/admin/AdminReconciliationClient.tsx", 'id="q-action" className="w-full'],
    ]) {
      const src = readSrc(rel);
      assert.ok(
        src.includes(triggerPattern),
        `${rel} detail panel SelectTrigger must span 100% width (w-full) on mobile`
      );
    }

    const filesToCheckStep = [
      "features/admin/AdminBannedKeywordsTab.tsx",
      "features/admin/AdminCategoriesPanel.tsx",
      "features/admin/AdminCarriersPanel.tsx",
      "app/(admin)/admin/doi-soat/page.tsx",
      "features/account/OrderDetailClientView.tsx",
      "features/account/AccountClientView.tsx",
      "features/commerce/AddressForm.tsx",
      "features/commerce/AddressFieldsForm.tsx",
    ];
    const badStepRe = /step=\{\s*(\d+)\s*\}/g;
    for (const rel of filesToCheckStep) {
      const src = readSrc(rel);
      for (const match of src.matchAll(badStepRe)) {
        assert.equal(Number(match[1]), 1, `${rel} must not use step={${match[1]}} > 1`);
      }
    }
  });
});
