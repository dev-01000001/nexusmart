import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  createBuyerAddress,
  ensureRateBudget,
  pickSingleShopSku,
  registerAndLoginBuyer,
  snapshotMailpitIds,
  waitForRecentOtp,
} from "./helpers.mjs";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";

async function loginAccount(email, password) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `login ${email} expected 200, got ${res.status}: ${res.text}`);
  return { client, user: res.data?.user };
}

function getBffError(res) {
  return {
    code: String(res.data?.error?.code ?? ""),
    message: String(res.data?.error?.message ?? ""),
  };
}

function getTodayVnDate() {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Ho_Chi_Minh",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
}

describe("10. Wave 3b-1 A5 (34 provinces / 3,321 wards, map-legacy, public PDP shipping, phone OTP & COD blocks, COD CSV reconciliation)", () => {
  test("1. [A5/E15-E17-bis] 34 provinces & 3,321 wards, legacy address mapping, and public anonymous PDP shipping fee quote", async () => {
    await ensureRateBudget(45, { label: "w3b-a5-geo-ship" });
    const anon = new WebClient();

    const provRes = await anon.json("GET", "/api/gw/shipping/provinces");
    assert.equal(provRes.status, 200, `provinces failed: ${provRes.text}`);
    const provinces = provRes.data?.provinces ?? [];
    assert.equal(provinces.length, 34, `expected 34 merged VN provinces, got ${provinces.length}`);

    // Map legacy 3-level address -> new 2-level province/ward
    const mapRes = await anon.json("POST", "/api/gw/shipping/addresses/map-legacy", {
      body: {
        legacy_province: "TP. Hồ Chí Minh",
        legacy_district: "Quận 1",
        legacy_ward: "Phường Bến Nghé",
      },
    });
    assert.equal(mapRes.status, 200, `map-legacy expected 200, got ${mapRes.status}: ${mapRes.text}`);
    assert.ok(mapRes.data?.province_code, `map-legacy missing province_code: ${mapRes.text}`);

    // Public anonymous PDP calculate-fee (to_ward_code="") returns quotes + is_estimated without auth
    const { product, sku } = await pickSingleShopSku(anon);
    const feeRes = await anon.json("POST", "/api/gw/shipping/calculate-fee", {
      body: {
        shop_id: product.shop_id,
        to_province_code: provinces[0].code,
        to_ward_code: "",
        items: [{ sku_id: sku.id, quantity: 1 }],
        payment_method: "VIETQR",
      },
    });
    assert.equal(feeRes.status, 200, `public calculate-fee failed: ${feeRes.text}`);
    assert.ok((feeRes.data?.quotes ?? []).length > 0, "expected >= 1 real shipping quote");
    assert.equal(feeRes.data?.is_estimated, true, "empty to_ward_code must return is_estimated=true");

    // PDP HTML must not contain removed fake carrier labels
    const pdpPage = await anon.page(`/san-pham/${encodeURIComponent(product.slug)}`);
    assert.equal(pdpPage.status, 200);
    assert.equal(pdpPage.text.includes("NexusFast"), false, "PDP must not render fake NexusFast");
  });

  test("2. [A5/E19] Unverified phone blocks COD with 409 COD_PHONE_NOT_VERIFIED; phone OTP unlocks COD", async () => {
    await ensureRateBudget(40, { label: "w3b-a5-phone-cod" });
    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w3b-cod-otp");
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const { product, sku } = await pickSingleShopSku(buyer, { maxTotalVnd: 500_000 });

    const quote = await calculateShippingQuote(buyer, {
      shopId: product.shop_id,
      province,
      ward,
      weightGrams: 300,
      codAmountVnd: sku.price_vnd,
    });

    // Attempt COD before phone OTP verification -> FailedPrecondition maps strictly to 409 COD_PHONE_NOT_VERIFIED
    const blockedRes = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `e2e-w3b-cod-block-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        payment_method: "COD",
        items: [{ sku_id: sku.id, quantity: 1 }],
        shipping_choices: [
          {
            shop_id: product.shop_id,
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        ],
      },
    });
    assert.equal(
      blockedRes.status,
      409,
      `unverified phone COD must return 409, got ${blockedRes.status}: ${blockedRes.text}`
    );
    assert.equal(getBffError(blockedRes).code, "COD_PHONE_NOT_VERIFIED");

    // Request phone OTP -> read from Mailpit -> verify phone
    const seenIds = await snapshotMailpitIds(buyerAcct.email);
    const otpReq = await buyer.json("POST", "/api/gw/users/me/phone/otp", { body: {} });
    assert.equal(otpReq.status, 200, `phone/otp failed: ${otpReq.text}`);
    assert.ok(
      typeof otpReq.data?.retry_after_seconds === "number",
      `expected retry_after_seconds in phone/otp response: ${otpReq.text}`
    );
    assert.ok(
      typeof otpReq.data?.expires_in_seconds === "number",
      `expected expires_in_seconds in phone/otp response: ${otpReq.text}`
    );

    const phoneOtp = await waitForRecentOtp(buyerAcct.email, seenIds);
    const verifyRes = await buyer.json("POST", "/api/gw/users/me/phone/verify", {
      body: { otp: phoneOtp },
    });
    assert.equal(verifyRes.status, 200, `phone/verify failed: ${verifyRes.text}`);
    assert.equal(verifyRes.data?.verified, true, `phone/verify expected verified=true: ${verifyRes.text}`);
    assert.equal(
      verifyRes.data?.user?.phone_verified,
      true,
      `phone/verify expected user.phone_verified=true: ${verifyRes.text}`
    );

    // Retry COD checkout after phone verification -> fresh Idempotency-Key returns 201 Created
    let createdOrderId = null;
    try {
      const okRes = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `e2e-w3b-cod-ok-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          payment_method: "COD",
          items: [{ sku_id: sku.id, quantity: 1 }],
          shipping_choices: [
            {
              shop_id: product.shop_id,
              carrier_code: quote.carrier_code,
              service_code: quote.service_code,
            },
          ],
        },
      });
      assert.equal(
        okRes.status,
        201,
        `COD checkout after phone verification expected 201, got ${okRes.status}: ${okRes.text}`
      );
      createdOrderId = okRes.data?.orders?.[0]?.id ?? null;
      assert.ok(createdOrderId, `COD checkout missing order id: ${okRes.text}`);
    } finally {
      if (createdOrderId) {
        await buyer
          .json("POST", `/api/gw/orders/${encodeURIComponent(createdOrderId)}/cancel`, {
            body: { reason: "e2e 10 cleanup" },
          })
          .catch(() => {});
      }
    }
  });

  test("3. [A5/E20/E20-bis/E21] Admin COD CSV reconciliation import, runs list, queue source_type & carriers API", async () => {
    await ensureRateBudget(30, { label: "w3b-a5-admin-cod" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    // Verify /admin/doi-soat page renders without old mock "Lịch sử lô (mẫu)" tab
    const doiSoatHtml = await admin.page("/admin/doi-soat");
    assert.equal(doiSoatHtml.status, 200);
    assert.equal(
      doiSoatHtml.text.includes("Lịch sử lô (mẫu)"),
      false,
      "/admin/doi-soat must not render mock reconciliation tab"
    );

    // Upload a real COD CSV with a missing tracking code -> verify MISSING_LOCAL & queue source_type="cod_file"
    const runDate = getTodayVnDate();
    const fakeTrack = `GHN-W3B-MISS-${crypto.randomBytes(4).toString("hex").toUpperCase()}`;
    const csvBody = [
      "tracking_code,cod_amount_vnd,delivered_at",
      `${fakeTrack},250000,${runDate}T10:00:00+07:00`,
    ].join("\n");

    const importRes = await admin.request(
      "POST",
      `/api/gw/admin/reconciliation/cod/import?carrier_code=ghn&file_name=e2e-w3b.csv&run_date=${encodeURIComponent(runDate)}`,
      {
        headers: { "Content-Type": "text/csv; charset=utf-8" },
        body: csvBody,
      }
    );
    assert.equal(
      importRes.status,
      200,
      `COD CSV import expected 200, got ${importRes.status}: ${importRes.text}`
    );
    const parsedImport = importRes.data ?? JSON.parse(importRes.text);
    assert.equal(parsedImport?.run?.missing_local_count, 1);

    // Runs history includes the imported COD run
    const runsRes = await admin.json(
      "GET",
      "/api/gw/admin/reconciliation/runs?run_type=cod_file&page=1&page_size=20"
    );
    assert.equal(runsRes.status, 200, `reconciliation runs failed: ${runsRes.text}`);
    assert.ok((runsRes.data?.items ?? []).length >= 1, "expected >= 1 COD reconciliation run");

    // Reconciliation queue includes the cod_file row with tracking_code
    const queueRes = await admin.json("GET", "/api/gw/admin/reconciliation/queue?page=1&page_size=50");
    assert.equal(queueRes.status, 200, `reconciliation queue failed: ${queueRes.text}`);
    const codQueueRow = (queueRes.data?.items ?? []).find((q) => q.tracking_code === fakeTrack);
    assert.ok(codQueueRow, `expected tracking_code ${fakeTrack} in reconciliation queue`);
    assert.equal(codQueueRow.source_type, "cod_file");

    // Ledger remains strictly balanced
    const ledgerRes = await admin.json("GET", "/api/gw/admin/ledger/summary");
    assert.equal(ledgerRes.status, 200);
    assert.equal(ledgerRes.data?.is_balanced, true);
    assert.equal(ledgerRes.data?.total_debits_vnd, ledgerRes.data?.total_credits_vnd);

    // E21: Carriers list loads from real API
    const carriersRes = await admin.json("GET", "/api/gw/admin/carriers");
    assert.equal(carriersRes.status, 200, `carriers list failed: ${carriersRes.text}`);
    assert.ok(Array.isArray(carriersRes.data?.carriers));
  });
});
