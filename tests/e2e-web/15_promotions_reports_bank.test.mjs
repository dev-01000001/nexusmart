import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, normalizeStatus, uid } from "./lib/http.mjs";
import { snapshotMailpitIds, waitForRecentOTP } from "./lib/mailpit.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  cancelOrderBestEffort,
  createApprovedProduct,
  createEphemeralSeller,
  createShippingAddress,
  deleteProductBestEffort,
  demoCredentials,
  loginOn,
  newIdempotencyKey,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("15. Shop discount programs, flash sale rules, reports/CSV, seller bank OTP/lock & per-user limit exhaustion", () => {
  let siteA;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    sellerRoot = new Browser("promo15-seller").at(ROOT_HOST);
    adminRoot = new Browser("promo15-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "promo15-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "promo15-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("7a: Retired marketplace content routes return 404 on root; site icon asset returns 200", async () => {
    const anonRoot = new Browser("promo15-7a").at(ROOT_HOST);
    for (const removed of ["/bo-suu-tap", "/bo-suu-tap/thu-dong", "/tap-chi", "/chien-dich", "/hoi-vien"]) {
      const r = await anonRoot.page(removed);
      assert.equal(r.status, 404, `removed marketplace page ${ROOT_HOST}${removed} must return 404, got ${r.status}`);
    }
    const icon = await anonRoot.get("/icon.svg", { headers: { accept: "*/*" } });
    expectStatus(icon, 200, "GET /icon.svg");
  });

  it("7b & 7f: Shop discount program lifecycle, site PDP strikethrough, preview/checkout & per_user_limit=1 exhaustion", async () => {
    const buyerA = new Browser("promo15-7bf").at(SITE_A_HOST);
    let productId = null;
    let programId = null;
    let order1Id = null;
    let order2Id = null;

    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `Promo15 Discount ${uid()}`,
        priceVnd: 300_000,
        stock: 20,
      });
      productId = product.id;

      const now = Date.now();
      const progCreate = expectStatus(
        await sellerRoot.post("/api/seller/discount-programs", {
          name: `Ưu đãi xưởng E2E ${uid()}`,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
          items: [
            {
              sku_id: sku.id,
              discounted_price_vnd: 250_000,
              per_user_limit: 1,
            },
          ],
        }),
        [200, 201],
        "POST /api/seller/discount-programs"
      );
      programId = progCreate.program?.id ?? progCreate.id;
      assert.ok(programId, "discount program id required");

      // Active shop discount visible on site API & site PDP SSR.
      const active = expectStatus(
        await buyerA.get("/api/shop-discounts/active"),
        200,
        `GET ${SITE_A_HOST}/api/shop-discounts/active`
      );
      assert.ok(
        (active.discounts ?? []).some((d) => d.sku_id === sku.id),
        `site /api/shop-discounts/active must include SKU ${sku.id}`
      );

      const pdp = await buyerA.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      expectStatus(pdp, 200, `GET ${SITE_A_HOST}/san-pham/${product.slug}`);
      assert.ok(pdp.text.includes("250.000") || pdp.text.includes("250,000"), "PDP must show discounted price 250.000đ");

      await registerBuyer(buyerA, "promo15-disc", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "promo15-disc");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: 250_000, total_weight_grams: 300 }],
        true
      );

      // 1st order gets full 50,000 VND shop_program_discount_vnd.
      const co1 = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "COD",
            items: [{ sku_id: sku.id, quantity: 1 }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("promo15-d1") } }
        ),
        201,
        "1st COD checkout under shop discount"
      );
      order1Id = co1.orders[0].id;
      assert.equal(Number(co1.orders[0].shop_program_discount_vnd ?? 0), 50_000);

      // 2nd order exhausts per_user_limit=1 -> 0 shop_program_discount_vnd.
      const co2 = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "COD",
            items: [{ sku_id: sku.id, quantity: 1 }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("promo15-d2") } }
        ),
        201,
        "2nd COD checkout after per_user_limit exhaustion"
      );
      order2Id = co2.orders[0].id;
      assert.equal(Number(co2.orders[0].shop_program_discount_vnd ?? 0), 0);

      // End discount program cleanly.
      expectStatus(
        await sellerRoot.post(`/api/seller/discount-programs/${encodeURIComponent(programId)}/end`, {}),
        200,
        "POST /api/seller/discount-programs/[id]/end"
      );
      programId = null;
    } finally {
      await cancelOrderBestEffort(buyerA, order1Id, "15_7bf cleanup 1");
      await cancelOrderBestEffort(buyerA, order2Id, "15_7bf cleanup 2");
      if (programId) {
        await sellerRoot.post(`/api/seller/discount-programs/${encodeURIComponent(programId)}/end`, {}).catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("7c & 7g: Flash sale disallows COD, enforces ~5m VietQR TTL, and per_user_limit=1 opt-out falls back to shop discount + COD", async () => {
    const buyerA = new Browser("promo15-7cg").at(SITE_A_HOST);
    let productId = null;
    let flashSaleId = null;
    let programId = null;
    let order1Id = null;
    let order2Id = null;

    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `Promo15 Flash ${uid()}`,
        priceVnd: 320_000,
        stock: 20,
      });
      productId = product.id;

      const now = Date.now();
      const progCreate = expectStatus(
        await sellerRoot.post("/api/seller/discount-programs", {
          name: `Fallback Discount ${uid()}`,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
          items: [
            {
              sku_id: sku.id,
              discounted_price_vnd: 290_000,
              per_user_limit: 5,
            },
          ],
        }),
        [200, 201],
        "create fallback shop discount"
      );
      programId = progCreate.program?.id ?? progCreate.id;

      const fsCreate = expectStatus(
        await adminRoot.post("/api/admin/flash-sales", {
          name: `E2E 7c Flash ${uid()}`,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
          min_discount_percent: 10,
        }),
        [200, 201],
        "create flash sale 7c"
      );
      flashSaleId = fsCreate.flash_sale?.id ?? fsCreate.id;

      const itemAdd = expectStatus(
        await sellerRoot.post(`/api/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`, {
          sku_id: sku.id,
          flash_price_vnd: 200_000,
          total_slots: 5,
          per_user_limit: 1,
        }),
        [200, 201],
        "add flash sale item 7c"
      );
      const flashItemId = itemAdd.item?.id ?? itemAdd.flash_sale_item?.id ?? itemAdd.id;
      expectStatus(
        await adminRoot.post(`/api/admin/flash-sale-items/${encodeURIComponent(flashItemId)}/approve`, {
          approve: true,
          reason: "E2E 7c approved",
        }),
        200,
        "approve flash sale item 7c"
      );

      await registerBuyer(buyerA, "promo15-fs", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "promo15-fs");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: 200_000, total_weight_grams: 300 }],
        false
      );

      // COD must be rejected for flash sale items (400/409).
      const codAttempt = await buyerA.post(
        "/api/checkout",
        {
          address_id: addr.id,
          from_cart: false,
          payment_method: "COD",
          items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
          shipping_choices: shippingChoices,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("promo15-fscod") } }
      );
      expectStatus(codAttempt, [400, 409], "COD must be disallowed for flash sale item");

      // VietQR succeeds with ~5 minute expiry TTL.
      const vqCo = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "VIETQR",
            items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("promo15-fsvq") } }
        ),
        201,
        "VietQR flash sale checkout"
      );
      order1Id = vqCo.orders[0].id;
      assert.equal(Number(vqCo.orders[0].flash_sale_discount_vnd ?? 0), 120_000);
      if (vqCo.payment_expires_at) {
        const ttlSec = (Date.parse(vqCo.payment_expires_at) - Date.now()) / 1000;
        assert.ok(ttlSec > 60 && ttlSec <= 420, `expected flash sale ~5m TTL, got ${Math.round(ttlSec)}s`);
      }

      // 7g: After per_user_limit=1 is consumed, reusing flash_sale_item_id is rejected (409),
      // while opting out of flash_sale_item_id permits COD & applies shop discount.
      const exhaustedAttempt = await buyerA.post(
        "/api/checkout",
        {
          address_id: addr.id,
          from_cart: false,
          payment_method: "VIETQR",
          items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
          shipping_choices: shippingChoices,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("promo15-fsexhausted") } }
      );
      expectStatus(exhaustedAttempt, [400, 409], "7g 2nd flash checkout exceeding per_user_limit=1 must fail");

      const co2 = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "COD",
            items: [{ sku_id: sku.id, quantity: 1 }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("promo15-fsoptout") } }
        ),
        201,
        "7g opt-out COD checkout falls back to shop discount"
      );
      order2Id = co2.orders[0].id;
      assert.equal(Number(co2.orders[0].flash_sale_discount_vnd ?? 0), 0);
      assert.equal(Number(co2.orders[0].shop_program_discount_vnd ?? 0), 30_000);
    } finally {
      await cancelOrderBestEffort(buyerA, order1Id, "15_7cg cleanup 1");
      await cancelOrderBestEffort(buyerA, order2Id, "15_7cg cleanup 2");
      if (flashSaleId) {
        await adminRoot.post(`/api/admin/flash-sales/${encodeURIComponent(flashSaleId)}/end`, {}).catch(() => {});
      }
      if (programId) {
        await sellerRoot.post(`/api/seller/discount-programs/${encodeURIComponent(programId)}/end`, {}).catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("7d: Seller & Admin reports + CSV export headers + >366 day validation", async () => {
    const today = new Date().toISOString().slice(0, 10);
    const weekAgo = new Date(Date.now() - 7 * 86400_000).toISOString().slice(0, 10);
    const twoYearsAgo = new Date(Date.now() - 400 * 86400_000).toISOString().slice(0, 10);

    expectStatus(
      await sellerRoot.get(`/api/seller/reports?from_date=${weekAgo}&to_date=${today}`),
      200,
      "GET /api/seller/reports"
    );
    const sCsv = await sellerRoot.get(`/api/seller/reports/export?from_date=${weekAgo}&to_date=${today}`, {
      headers: { accept: "text/csv" },
    });
    expectStatus(sCsv, 200, "GET /api/seller/reports/export");
    assert.ok(
      String(sCsv.headers["content-disposition"] || "").toLowerCase().includes("attachment"),
      "seller CSV export must set Content-Disposition: attachment"
    );
    expectStatus(
      await sellerRoot.get(`/api/seller/reports?from_date=${twoYearsAgo}&to_date=${today}`),
      400,
      "seller reports >366d must return 400"
    );

    expectStatus(
      await adminRoot.get(`/api/admin/reports?from_date=${weekAgo}&to_date=${today}`),
      200,
      "GET /api/admin/reports"
    );
    const aCsv = await adminRoot.get(`/api/admin/reports/export?from_date=${weekAgo}&to_date=${today}`, {
      headers: { accept: "text/csv" },
    });
    expectStatus(aCsv, 200, "GET /api/admin/reports/export");
    assert.ok(
      String(aCsv.headers["content-disposition"] || "").toLowerCase().includes("attachment"),
      "admin CSV export must set Content-Disposition: attachment"
    );
    expectStatus(
      await adminRoot.get(`/api/admin/reports?from_date=${twoYearsAgo}&to_date=${today}`),
      400,
      "admin reports >366d must return 400"
    );
  });

  it("7e: Ephemeral seller bank account (mismatch -> PENDING_ADMIN_REVIEW -> approve; change -> VERIFIED + 24h lock) & seller ledger", async () => {
    const eph = await createEphemeralSeller("bank15");
    try {
      // Step A: Mismatched holder name -> PENDING_OTP -> OTP -> PENDING_ADMIN_REVIEW -> Admin APPROVE -> VERIFIED
      const seenIds = await snapshotMailpitIds(eph.creds.email);
      const submit1 = expectStatus(
        await eph.client.post("/api/seller/bank-account", {
          bank_code: "VCB",
          bank_name: "Vietcombank",
          account_number: "01234567891234",
          account_holder_name: "NGUYEN VAN E2E KHAC TEN",
        }),
        [200, 201],
        "POST /api/seller/bank-account #1 (mismatched holder)"
      );
      assertNoInternalErrorLeak(JSON.stringify(submit1), "seller bank-account #1");
      assert.equal(normalizeStatus(submit1.bank_account?.status), "PENDING_OTP");

      const otp1 = await waitForRecentOTP(eph.creds.email, seenIds, {
        label: `seller bank OTP #1 for ${eph.creds.email}`,
      });
      const ver1 = expectStatus(
        await eph.client.post("/api/seller/bank-account/verify-otp", { otp: otp1 }),
        200,
        "POST /api/seller/bank-account/verify-otp #1"
      );
      const afterOtp1 = ver1.bank_account;
      assert.equal(normalizeStatus(afterOtp1?.status), "PENDING_ADMIN_REVIEW");

      const pendingList = expectStatus(
        await adminRoot.get("/api/admin/shops?bank_review_pending=true&page=1&page_size=100"),
        200,
        "GET /api/admin/shops?bank_review_pending=true"
      );
      const flaggedShop = (pendingList.items ?? []).find((s) => s.id === eph.shopId);
      assert.ok(
        flaggedShop && flaggedShop.bank_review_pending === true,
        `shop ${eph.shopId} must appear in bank_review_pending=true`
      );

      const bankDetail = expectStatus(
        await adminRoot.get(`/api/admin/shops/${encodeURIComponent(eph.shopId)}/bank-account`),
        200,
        "GET /api/admin/shops/[id]/bank-account"
      );
      const bankAccountId = bankDetail.bank_account?.id ?? afterOtp1?.id;
      assert.ok(bankAccountId, "admin shop bank-account must return id");

      const review1 = expectStatus(
        await adminRoot.post(`/api/admin/shops/${encodeURIComponent(eph.shopId)}/bank-account/review`, {
          bank_account_id: bankAccountId,
          decision: "APPROVE",
          reason: "E2E 15 bank account ownership verified",
        }),
        200,
        "POST /api/admin/shops/[id]/bank-account/review #1"
      );
      assert.equal(normalizeStatus(review1.bank_account?.status), "VERIFIED");

      // Step B: Change already-verified bank account (matching owner name) -> OTP -> VERIFIED + 24h payout_locked_until
      const submit2 = expectStatus(
        await eph.client.post("/api/seller/bank-account", {
          bank_code: "TCB",
          bank_name: "Techcombank",
          account_number: "09876543210987",
          account_holder_name: eph.creds.fullName,
        }),
        [200, 201],
        "POST /api/seller/bank-account #2 (change verified account)"
      );
      assert.equal(normalizeStatus(submit2.bank_account?.status), "PENDING_OTP");

      const otp2 = await waitForRecentOTP(eph.creds.email, seenIds, {
        label: `seller bank OTP #2 for ${eph.creds.email}`,
      });
      const ver2 = expectStatus(
        await eph.client.post("/api/seller/bank-account/verify-otp", { otp: otp2 }),
        200,
        "POST /api/seller/bank-account/verify-otp #2"
      );
      let bank2 = ver2.bank_account;
      if (normalizeStatus(bank2?.status) === "PENDING_ADMIN_REVIEW") {
        const review2 = expectStatus(
          await adminRoot.post(`/api/admin/shops/${encodeURIComponent(eph.shopId)}/bank-account/review`, {
            bank_account_id: bank2.id,
            decision: "APPROVE",
            reason: "E2E 15 approved bank change",
          }),
          200,
          "POST /api/admin/shops/[id]/bank-account/review #2"
        );
        bank2 = review2.bank_account;
      }
      assert.equal(normalizeStatus(bank2?.status), "VERIFIED");
      assert.ok(
        bank2?.payout_locked_until && Date.parse(bank2.payout_locked_until) > Date.now() + 20 * 3600_000,
        `changing a verified bank account must set ~24h payout_locked_until, got: ${JSON.stringify(bank2)}`
      );

      const ledger = expectStatus(await eph.client.get("/api/seller/ledger?page=1&page_size=20"), 200, "GET /api/seller/ledger");
      assert.ok(Array.isArray(ledger.items), "seller ledger must return items array");
    } finally {
      await eph.client.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
