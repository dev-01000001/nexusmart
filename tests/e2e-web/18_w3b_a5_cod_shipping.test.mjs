import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, getBffError, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  cancelOrderBestEffort,
  checkout,
  createShippingAddress,
  demoCredentials,
  fillCart,
  loginOn,
  newIdempotencyKey,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
  verifyBuyerPhone,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("18. Wave 3b A5 (34 provinces, legacy map, public shipping quote, E19 COD phone OTP, COD CSV reconciliation & ledger)", () => {
  let siteA;
  let skuA;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
    adminRoot = new Browser("a5-admin").at(ROOT_HOST);
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "a5-admin");
  });

  after(async () => {
    await adminRoot.post("/api/auth/logout", {}).catch(() => {});
    await resetRateLimitKeys();
  });

  it("E17/E18: 34 provinces on site /api/checkout/locations, legacy address mapping, anonymous estimated quote & PDP copy hygiene", async () => {
    const anonA = new Browser("a5-anon-a").at(SITE_A_HOST);

    const locData = expectStatus(await anonA.get("/api/checkout/locations"), 200, "GET site /api/checkout/locations");
    const provinces = locData.provinces ?? [];
    assert.ok(provinces.length >= 34, `expected >= 34 provinces, got ${provinces.length}`);

    const firstProv = provinces[0];
    const wardsData = expectStatus(
      await anonA.get(`/api/checkout/locations?province_code=${encodeURIComponent(firstProv.code)}`),
      200,
      "GET site wards"
    );
    assert.ok((wardsData.wards ?? []).length > 0, `expected wards for province ${firstProv.code}`);

    // Legacy 63-province mapping endpoint on site host.
    const mapRes = expectStatus(
      await anonA.post("/api/shipping/addresses/map-legacy", {
        legacy_province: "Bình Dương",
        legacy_district: "Thủ Dầu Một",
        legacy_ward: "Phú Hòa",
      }),
      200,
      "POST /api/shipping/addresses/map-legacy"
    );
    assert.ok(mapRes.new_province_code || mapRes.province_code || mapRes.mapped, `unexpected map-legacy: ${JSON.stringify(mapRes)}`);

    // Anonymous shipping quote with empty to_ward_code -> response-level is_estimated=true.
    const estRes = expectStatus(
      await anonA.post("/api/checkout/shipping-quote", {
        to_province_code: firstProv.code,
        to_ward_code: "",
        shops: [{ shop_id: siteA.shopId, weight_grams: 400, cod_amount_vnd: 0 }],
      }),
      200,
      "anonymous shipping-quote with empty ward"
    );
    const estQuotes = estRes.quotes_by_shop?.[siteA.shopId] ?? estRes.quotes ?? [];
    assert.ok(estQuotes.length > 0, "expected estimated shipping quotes");
    assert.equal(
      Boolean(estRes.is_estimated),
      true,
      `quote with empty to_ward_code must set top-level is_estimated=true: ${JSON.stringify(estRes)}`
    );

    // Site PDP must not display retired "NexusFast" carrier copy.
    const pdp = await anonA.page(`/san-pham/${SEEDED.bachDang.productSlug}`);
    expectStatus(pdp, 200, "site PDP SSR");
    assert.equal(pdp.text.includes("NexusFast"), false, "site PDP must not contain legacy NexusFast text");
  });

  it("E19: Unverified phone blocks COD with 409 COD_PHONE_NOT_VERIFIED; phone OTP via Mailpit unlocks COD", async () => {
    const buyerA = new Browser("a5-e19-buyer").at(SITE_A_HOST);
    let createdOrderId = null;

    try {
      // Register WITHOUT verifyPhone so phone_verified starts false.
      const acct = await registerBuyer(buyerA, "a5-e19", { verifyPhone: false });
      const addr = await createShippingAddress(buyerA, "a5-e19");
      const cart = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices } = await quoteShipping(buyerA, addr, cart.shop_groups, true);

      const blocked = await checkout(buyerA, {
        addressId: addr.id,
        shippingChoices,
        paymentMethod: "COD",
        key: newIdempotencyKey("a5-e19-block"),
      });
      expectStatus(blocked, 409, "unverified phone COD checkout must return 409");
      assert.equal(
        getBffError(blocked).code,
        "COD_PHONE_NOT_VERIFIED",
        `expected COD_PHONE_NOT_VERIFIED, got ${blocked.text}`
      );

      // Verify phone via real Mailpit OTP through BFF.
      await verifyBuyerPhone(buyerA, acct);

      // Re-attempt COD checkout -> succeeds (201).
      const unlocked = expectStatus(
        await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices,
          paymentMethod: "COD",
          key: newIdempotencyKey("a5-e19-ok"),
        }),
        201,
        "verified phone COD checkout must return 201"
      );
      createdOrderId = unlocked.orders[0].id;
      assert.equal(unlocked.payment_method, "COD");
    } finally {
      await cancelOrderBestEffort(buyerA, createdOrderId, "18_e19 cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("E20/E21: Admin /admin/doi-soat (no mock tab), COD CSV import, runs, queue (cod_file), balanced ledger & carriers", async () => {
    const reconPage = await adminRoot.page("/admin/doi-soat");
    expectStatus(reconPage, 200, "GET /admin/doi-soat");
    assert.equal(
      reconPage.text.includes("Chạy đối soát mô phỏng"),
      false,
      "/admin/doi-soat must not render removed mock reconciliation tab"
    );
    assertNoInternalErrorLeak(reconPage.text, "GET /admin/doi-soat");

    const runDate = new Date().toISOString().slice(0, 10);
    const tracking = `GHN-W3B-MISS-${uid().toUpperCase()}`;
    const csv = [
      "tracking_code,cod_amount_vnd,delivered_at",
      `${tracking},250000,${runDate}T10:00:00+07:00`,
    ].join("\n");

    const importRes = await adminRoot.request(
      "POST",
      `/api/admin/reconciliation/cod/import?carrier_code=ghn&run_date=${encodeURIComponent(runDate)}&file_name=e2e-cod.csv`,
      {
        rawBody: csv,
        contentType: "text/csv; charset=utf-8",
      }
    );
    const importData = expectStatus(importRes, [200, 201], "POST /api/admin/reconciliation/cod/import");
    assert.equal(importData?.run?.missing_local_count, 1, `expected missing_local_count=1: ${importRes.text}`);

    const runs = expectStatus(
      await adminRoot.get("/api/admin/reconciliation/runs?run_type=cod_file&page=1&page_size=20"),
      200,
      "GET /api/admin/reconciliation/runs"
    );
    assert.ok((runs.items ?? runs.runs ?? []).length >= 1, "expected >= 1 COD reconciliation run");

    const queue = expectStatus(
      await adminRoot.get("/api/admin/reconciliation/queue?page=1&page_size=50"),
      200,
      "GET /api/admin/reconciliation/queue"
    );
    const codRow = (queue.items ?? []).find((q) => q.tracking_code === tracking);
    assert.ok(codRow, `expected tracking_code ${tracking} in reconciliation queue`);
    assert.equal(codRow.source_type, "cod_file");

    const ledger = expectStatus(await adminRoot.get("/api/admin/ledger"), 200, "GET /api/admin/ledger");
    assert.equal(ledger.is_balanced, true, "double-entry ledger must remain balanced");

    const carriers = expectStatus(await adminRoot.get("/api/admin/carriers"), 200, "GET /api/admin/carriers");
    assert.ok(Array.isArray(carriers.carriers) && carriers.carriers.length > 0, "admin carriers must be non-empty");
  });
});
