import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { postSignedZaloPayCallback } from "./lib/bank.mjs";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, getBffError, gw, hasObjectKeyDeep, normalizeStatus, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  cancelOrderBestEffort,
  createApprovedProduct,
  createShippingAddress,
  deleteProductBestEffort,
  demoCredentials,
  loginOn,
  newIdempotencyKey,
  orderBackendStatus,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

function isAllowedPayUrl(rawUrl) {
  const v = String(rawUrl ?? "").trim();
  if (!v) return false;
  if (v.startsWith("/thanh-toan/mock-zalopay")) return true;
  try {
    const parsed = new URL(v);
    if (parsed.protocol !== "https:") return false;
    const host = parsed.hostname.toLowerCase();
    return host === "zalopay.vn" || host.endsWith(".zalopay.vn");
  } catch {
    return false;
  }
}

describe("19. Wave 3b-2 A6 (ZaloPay checkout, pay_url, method switch, payment-methods config lock, fees, 0 VND order & wallet refund UI)", () => {
  let siteA;
  let skuA;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 500_000 });
    sellerRoot = new Browser("zp19-seller").at(ROOT_HOST);
    adminRoot = new Browser("zp19-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "zp19-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "zp19-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("1. [A6/F6/F7] Admin /admin/cau-hinh config, optimistic lock 409 VERSION_CONFLICT, site public /api/payment-methods & audit log", async () => {
    const configPage = await adminRoot.page("/admin/cau-hinh");
    expectStatus(configPage, 200, "GET /admin/cau-hinh");
    assert.equal(configPage.text.includes("Ví NexusPay"), false, "/admin/cau-hinh must not render legacy Ví NexusPay");

    const listRes = expectStatus(await adminRoot.get("/api/admin/payment-methods"), 200, "GET /api/admin/payment-methods");
    const byCode = new Map((listRes.methods ?? []).map((m) => [normalizeStatus(m.method), m]));
    assert.ok(byCode.has("COD") && byCode.has("VIETQR") && byCode.has("ZALOPAY"), "must list COD, VIETQR, ZALOPAY");

    const origCod = byCode.get("COD");
    const targetMax = origCod.max_order_vnd === 2_000_000 ? 2_500_000 : 2_000_000;
    let currentVersion = origCod.version;
    let mutated = false;

    try {
      const upd = expectStatus(
        await adminRoot.put("/api/admin/payment-methods/COD", {
          enabled: origCod.enabled,
          min_order_vnd: origCod.min_order_vnd,
          max_order_vnd: targetMax,
          expiry_minutes: origCod.expiry_minutes,
          note: "E2E 19 COD config update",
          expected_version: origCod.version,
        }),
        200,
        "PUT /api/admin/payment-methods/COD"
      );
      mutated = true;
      const updatedCod = upd.method ?? upd;
      assert.equal(updatedCod.max_order_vnd, targetMax);
      assert.ok(updatedCod.version > origCod.version);
      currentVersion = updatedCod.version;

      // Stale expected_version -> 409 VERSION_CONFLICT.
      const stale = await adminRoot.put("/api/admin/payment-methods/COD", {
        enabled: origCod.enabled,
        min_order_vnd: origCod.min_order_vnd,
        max_order_vnd: 1_500_000,
        expiry_minutes: origCod.expiry_minutes,
        note: "stale write",
        expected_version: origCod.version,
      });
      expectStatus(stale, 409, "stale expected_version must return 409");
      assert.equal(getBffError(stale).code, "VERSION_CONFLICT");

      // Public GET /api/payment-methods on site host persists value without credential_preview; root host returns 404.
      const anonRoot = new Browser("zp19-pub-root").at(ROOT_HOST);
      assert.equal((await anonRoot.get("/api/payment-methods")).status, 404, "root /api/payment-methods must be 404");

      const anonSite = new Browser("zp19-pub-site").at(SITE_A_HOST);
      const pub = expectStatus(await anonSite.get("/api/payment-methods"), 200, "site GET /api/payment-methods");
      const pubCod = (pub.methods ?? []).find((m) => normalizeStatus(m.method) === "COD");
      assert.equal(pubCod?.max_order_vnd, targetMax);
      assert.equal(hasObjectKeyDeep(pub, "credential_preview"), false, "public /api/payment-methods must not expose credential_preview");

      expectStatus(await adminRoot.get("/api/admin/audit-logs?page=1&page_size=20"), 200, "GET /api/admin/audit-logs");
    } finally {
      if (mutated) {
        const latest = await adminRoot.get("/api/admin/payment-methods").catch(() => null);
        const cur = (latest?.data?.methods ?? []).find((m) => normalizeStatus(m.method) === "COD");
        await adminRoot
          .put("/api/admin/payment-methods/COD", {
            enabled: origCod.enabled,
            min_order_vnd: origCod.min_order_vnd,
            max_order_vnd: origCod.max_order_vnd,
            expiry_minutes: origCod.expiry_minutes,
            note: origCod.note ?? "",
            expected_version: cur?.version ?? currentVersion,
          })
          .catch(() => {});
      }
    }
  });

  it("2. [A6/F1/F2/F3] ZaloPay checkout on site, pay_url allowlist, /thanh-toan/mock-zalopay notice & ZALOPAY->VIETQR->COD switch", async () => {
    const buyerA = new Browser("zp19-switch").at(SITE_A_HOST);
    let orderId = null;

    try {
      await registerBuyer(buyerA, "zp19-sw", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "zp19-sw");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: skuA.price, total_weight_grams: 300 }],
        false
      );

      const co = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "ZALOPAY",
            items: [{ sku_id: skuA.skuId, quantity: 1 }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("zp19-sw") } }
        ),
        201,
        "site ZALOPAY checkout"
      );
      orderId = co.orders[0].id;
      const zpPaymentId = co.payment_id;
      assert.ok(zpPaymentId, "ZALOPAY checkout must return payment_id");

      const pay1 = expectStatus(
        await buyerA.get(`/api/payments/${encodeURIComponent(zpPaymentId)}`),
        200,
        "GET ZALOPAY payment"
      );
      const payUrl = pay1.payment?.pay_url ?? pay1.payment?.qr_url ?? "";
      assert.ok(isAllowedPayUrl(payUrl), `ZALOPAY pay_url must satisfy allowlist, got: ${payUrl}`);

      expectStatus(await buyerA.page(`/thanh-toan/cho/${encodeURIComponent(zpPaymentId)}`), 200, "ZALOPAY wait page SSR");

      const mockPath = payUrl.startsWith("/") ? payUrl : "/thanh-toan/mock-zalopay";
      const mockPage = await buyerA.page(mockPath);
      expectStatus(mockPage, 200, `GET ${SITE_A_HOST}${mockPath}`);
      assert.ok(
        mockPage.text.includes("Trang mô phỏng, không phải trang ZaloPay thật."),
        "mock ZaloPay page must show neutral simulation notice"
      );

      // Switch ZALOPAY -> VIETQR.
      const toVq = expectStatus(
        await buyerA.post(
          `/api/payments/${encodeURIComponent(zpPaymentId)}/retry`,
          { method: "VIETQR" },
          { headers: { "Idempotency-Key": newIdempotencyKey("zp19-to-vq") } }
        ),
        200,
        "retry ZALOPAY -> VIETQR"
      );
      const vqPaymentId = toVq.payment?.payment_id || toVq.payment?.id || "";
      assert.ok(vqPaymentId && vqPaymentId !== zpPaymentId, `switching to VIETQR must create new payment_id: ${JSON.stringify(toVq)}`);
      assert.equal(normalizeStatus(toVq.payment?.method), "VIETQR");
      assert.equal(Boolean(toVq.switched_to_cod), false);

      // Switch VIETQR -> COD (buyer phone is verified).
      const toCod = expectStatus(
        await buyerA.post(
          `/api/payments/${encodeURIComponent(vqPaymentId)}/retry`,
          { method: "COD" },
          { headers: { "Idempotency-Key": newIdempotencyKey("zp19-to-cod") } }
        ),
        200,
        "retry VIETQR -> COD"
      );
      assert.equal(Boolean(toCod.switched_to_cod), true, "expected switched_to_cod=true");

      const afterSwitch = expectStatus(
        await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}`),
        200,
        "GET order after VIETQR->COD switch"
      );
      assert.equal(orderBackendStatus(afterSwitch.order), "CONFIRMED");
      assert.equal(normalizeStatus(afterSwitch.order?.paymentMethodRaw ?? afterSwitch.order?.paymentMethod), "COD");
    } finally {
      await cancelOrderBestEffort(buyerA, orderId, "zp19 switch cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("3. [A6/F4] Admin ZALOPAY payment fee config (150 bps) & history persistence", async () => {
    const futureFrom = new Date(Date.now() + 7 * 86_400_000).toISOString();
    expectStatus(
      await adminRoot.post("/api/admin/fees/payment", {
        payment_method: "ZALOPAY",
        fee_bps: 150,
        effective_from: futureFrom,
        note: "E2E 19 ZALOPAY fee 1.5%",
      }),
      201,
      "POST /api/admin/fees/payment ZALOPAY 150 bps"
    );

    const after = expectStatus(
      await adminRoot.get("/api/admin/fees/payment?include_history=true"),
      200,
      "GET /api/admin/fees/payment after update"
    );
    const configs = [
      ...(Array.isArray(after.active_configs) ? after.active_configs : []),
      ...(Array.isArray(after.history) ? after.history : []),
    ];
    assert.ok(
      configs.some((c) => normalizeStatus(c.payment_method) === "ZALOPAY" && Number(c.fee_bps) === 150),
      `expected ZALOPAY 150 bps fee config in list: ${JSON.stringify(after)}`
    );
  });

  it("4. [A6/F5] Storefront R>=0 freeship clamp evidence + 0 VND MARKETPLACE order exempt from VIETQR min_order_vnd > 0", async () => {
    const buyerA = new Browser("zp19-zero").at(SITE_A_HOST);
    const pmList = expectStatus(await adminRoot.get("/api/admin/payment-methods"), 200, "GET admin payment-methods");
    const origVqr = (pmList.methods ?? []).find((m) => normalizeStatus(m.method) === "VIETQR");
    assert.ok(origVqr, "VIETQR method config required");

    const origShipSetRes = expectStatus(
      await sellerRoot.get("/api/seller/shipping-settings"),
      200,
      "GET /api/seller/shipping-settings"
    );
    const origShipSet = origShipSetRes.settings;

    let vqrMutated = false;
    let shipMutated = false;
    let productId = null;
    let shopVoucherId = null;
    const createdAdminVoucherIds = [];
    let mpOrderId = null;
    let buyerGwToken = "";
    let adminGwToken = "";

    try {
      // Enable shop freeship covering up to 150,000 VND for orders from 0 VND.
      expectStatus(
        await sellerRoot.put("/api/seller/shipping-settings", {
          enabled_carrier_codes:
            origShipSet?.enabled_carrier_codes?.length > 0
              ? origShipSet.enabled_carrier_codes
              : ["MOCK_EXPRESS"],
          default_weight_grams: origShipSet?.default_weight_grams || 300,
          default_length_cm: origShipSet?.default_length_cm || 20,
          default_width_cm: origShipSet?.default_width_cm || 15,
          default_height_cm: origShipSet?.default_height_cm || 5,
          free_ship_enabled: true,
          free_ship_min_order_vnd: 0,
          free_ship_max_vnd: 150_000,
        }),
        200,
        "enable temp shop freeship"
      );
      shipMutated = true;

      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `Zero VND SP ${uid()}`,
        priceVnd: 100_000,
        stock: 10,
      });
      productId = product.id;

      const now = Date.now();
      const startsAt = new Date(now - 5 * 60_000).toISOString();
      const endsAt = new Date(now + 2 * 3600_000).toISOString();

      const svRes = expectStatus(
        await sellerRoot.post("/api/seller/vouchers", {
          code: `ZERO${uid().slice(-6).toUpperCase()}`,
          name: "100% Shop Voucher E2E 19",
          discount_type: "FIXED",
          value_vnd: 100_000,
          min_order_vnd: 0,
          quota: 5,
          per_user_limit: 2,
          budget_vnd: 500_000,
          starts_at: startsAt,
          ends_at: endsAt,
        }),
        201,
        "create 100% SHOP voucher"
      );
      shopVoucherId = svRes.voucher?.id;
      assert.ok(shopVoucherId);

      const buyerAcct = await registerBuyer(buyerA, "zp19-zero");
      const addr = await createShippingAddress(buyerA, "zp19-zero");
      expectStatus(await buyerA.post(`/api/vouchers/${encodeURIComponent(shopVoucherId)}/save`, {}), 200, "save SHOP voucher");

      const { shippingChoices, shippingFees } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: 100_000, total_weight_grams: 300 }],
        false
      );
      const quoteFee = Number(shippingFees[siteA.shopId] ?? 0);
      assert.ok(quoteFee > 0, `expected positive shipping quote fee, got ${quoteFee}`);

      // Part A: Verify storefront seller-revenue invariant (R >= 0): 100% SHOP voucher makes merchAfterShopDisc=0,
      // so shop_freeship_discount_vnd clamps to 0 and storefront final_payable_vnd == shipping_fee_vnd > 0.
      expectStatus(await buyerA.post("/api/cart/items", { sku_id: sku.id, quantity: 1 }), 200, "add to cart for preview");
      const sfPreview = expectStatus(
        await buyerA.post("/api/vouchers/preview", {
          shipping_fees: shippingFees,
          shop_voucher_ids: { [siteA.shopId]: shopVoucherId },
        }),
        200,
        "storefront preview with 100% SHOP voucher"
      );
      assert.equal(sfPreview.breakdown.total_shop_voucher_discount_vnd, 100_000);
      assert.equal(sfPreview.breakdown.final_payable_vnd, quoteFee, "storefront R>=0 clamp keeps final_payable_vnd == shipping_fee_vnd");

      // Part B (F5): Raise VIETQR min_order_vnd strictly above normal order total; a true 0 VND order
      // (PLATFORM + FREESHIP vouchers via gateway MARKETPLACE channel) must still succeed with CONFIRMED + SUCCEEDED.
      const raisedMinVnd = 100_000 + quoteFee + 100_000;
      const raisedMaxVnd =
        origVqr.max_order_vnd > 0 && origVqr.max_order_vnd < raisedMinVnd
          ? raisedMinVnd + 1_000_000
          : origVqr.max_order_vnd;
      expectStatus(
        await adminRoot.put("/api/admin/payment-methods/VIETQR", {
          enabled: true,
          min_order_vnd: raisedMinVnd,
          max_order_vnd: raisedMaxVnd,
          expiry_minutes: origVqr.expiry_minutes ?? 15,
          note: "E2E 19 0 VND min_order_vnd exemption check",
          expected_version: origVqr.version,
        }),
        200,
        "raise VIETQR min_order_vnd"
      );
      vqrMutated = true;

      const tag = uid().slice(-6).toUpperCase();
      const platRes = expectStatus(
        await adminRoot.post("/api/admin/vouchers", {
          code: `W3B2ZPLAT${tag}`,
          name: `0 VND Platform ${tag}`,
          scope: "PLATFORM",
          discount_type: "FIXED",
          value_vnd: 100_000,
          min_order_vnd: 0,
          quota: 5,
          per_user_limit: 2,
          budget_vnd: 500_000,
          starts_at: startsAt,
          ends_at: endsAt,
        }),
        201,
        "create PLATFORM voucher for 0 VND test"
      );
      const platVoucherId = platRes.voucher?.id;
      assert.ok(platVoucherId);
      createdAdminVoucherIds.push(platVoucherId);

      const shipFeeCap = Math.max(quoteFee + 10_000, 50_000);
      const shipRes = expectStatus(
        await adminRoot.post("/api/admin/vouchers", {
          code: `W3B2ZSHIP${tag}`,
          name: `0 VND Freeship ${tag}`,
          scope: "FREESHIP",
          discount_type: "FIXED",
          value_vnd: shipFeeCap,
          min_order_vnd: 0,
          quota: 5,
          per_user_limit: 2,
          budget_vnd: shipFeeCap * 5,
          starts_at: startsAt,
          ends_at: endsAt,
        }),
        201,
        "create FREESHIP voucher for 0 VND test"
      );
      const shipVoucherId = shipRes.voucher?.id;
      assert.ok(shipVoucherId);
      createdAdminVoucherIds.push(shipVoucherId);

      const buyerLoginGw = await gw("POST", "/api/v1/auth/login", {
        body: { email: buyerAcct.creds.email, password: buyerAcct.creds.password },
      });
      assert.equal(buyerLoginGw.status, 200);
      buyerGwToken = buyerLoginGw.data.access_token;

      const adminLoginGw = await gw("POST", "/api/v1/auth/login", {
        body: { email: demoCredentials.admin.email, password: demoCredentials.admin.password },
      });
      assert.equal(adminLoginGw.status, 200);
      adminGwToken = adminLoginGw.data.access_token;

      for (const vid of [platVoucherId, shipVoucherId]) {
        const sv = await gw("POST", `/api/v1/vouchers/${encodeURIComponent(vid)}/save`, {
          token: buyerGwToken,
          body: {},
        });
        assert.equal(sv.status, 200, `gateway save voucher ${vid} failed: ${sv.text}`);
      }

      const coGw = await gw("POST", "/api/v1/checkout", {
        token: buyerGwToken,
        idempotencyKey: newIdempotencyKey("zp19-zero-mp"),
        body: {
          sales_channel: "MARKETPLACE",
          address_id: addr.id,
          from_cart: false,
          payment_method: "VIETQR",
          items: [{ sku_id: sku.id, quantity: 1 }],
          platform_voucher_id: platVoucherId,
          freeship_voucher_id: shipVoucherId,
          shipping_choices: shippingChoices,
        },
      });
      assert.equal(
        coGw.status,
        201,
        `0 VND checkout while VIETQR min_order_vnd > 0 expected 201, got ${coGw.status}: ${coGw.text}`
      );
      assert.equal(coGw.data?.total_payable_vnd, 0, `expected total_payable_vnd=0: ${coGw.text}`);
      assert.equal(normalizeStatus(coGw.data?.payment_status), "SUCCEEDED");
      mpOrderId = coGw.data?.orders?.[0]?.id ?? null;
      assert.ok(mpOrderId);
      assert.equal(normalizeStatus(coGw.data?.orders?.[0]?.status), "CONFIRMED");

      // Channel isolation: legacy MARKETPLACE order is not exposed on tenant site host (404).
      assert.equal(
        (await buyerA.page(`/don-hang/${encodeURIComponent(mpOrderId)}`)).status,
        404,
        "MARKETPLACE 0 VND order must remain isolated (404) on tenant site host"
      );
    } finally {
      if (mpOrderId && buyerGwToken) {
        await gw("POST", `/api/v1/orders/${encodeURIComponent(mpOrderId)}/cancel`, {
          token: buyerGwToken,
          body: { reason: "zp19 zero cleanup" },
        }).catch(() => {});
      }
      for (const vid of createdAdminVoucherIds) {
        if (adminGwToken) {
          await gw("DELETE", `/api/v1/admin/vouchers/${encodeURIComponent(vid)}`, {
            token: adminGwToken,
          }).catch(() => {});
        }
      }
      if (shopVoucherId) {
        await sellerRoot
          .patch(`/api/seller/vouchers/${encodeURIComponent(shopVoucherId)}/status`, { status: "PAUSED" })
          .catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
      if (shipMutated && origShipSet) {
        await sellerRoot
          .put("/api/seller/shipping-settings", {
            enabled_carrier_codes: origShipSet.enabled_carrier_codes?.length
              ? origShipSet.enabled_carrier_codes
              : ["MOCK_EXPRESS"],
            default_weight_grams: origShipSet.default_weight_grams || 300,
            default_length_cm: origShipSet.default_length_cm || 20,
            default_width_cm: origShipSet.default_width_cm || 15,
            default_height_cm: origShipSet.default_height_cm || 5,
            free_ship_enabled: Boolean(origShipSet.free_ship_enabled),
            free_ship_min_order_vnd: origShipSet.free_ship_min_order_vnd ?? 0,
            free_ship_max_vnd: origShipSet.free_ship_max_vnd ?? 0,
          })
          .catch(() => {});
      }
      if (vqrMutated) {
        const latest = await adminRoot.get("/api/admin/payment-methods").catch(() => null);
        const cur = (latest?.data?.methods ?? []).find((m) => normalizeStatus(m.method) === "VIETQR");
        await adminRoot
          .put("/api/admin/payment-methods/VIETQR", {
            enabled: origVqr.enabled,
            min_order_vnd: origVqr.min_order_vnd,
            max_order_vnd: origVqr.max_order_vnd,
            expiry_minutes: origVqr.expiry_minutes,
            note: origVqr.note ?? "",
            expected_version: cur?.version ?? origVqr.version,
          })
          .catch(() => {});
      }
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("5. [A6/web-r2] Late ZaloPay callback creates PENDING wallet refund and hides bank-account form on site /don-hang/[id]", async () => {
    const buyerA = new Browser("zp19-walletrf").at(SITE_A_HOST);
    let orderId = null;

    try {
      await registerBuyer(buyerA, "zp19-rf", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "zp19-rf");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: skuA.price, total_weight_grams: 300 }],
        false
      );

      const co = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "ZALOPAY",
            items: [{ sku_id: skuA.skuId, quantity: 1 }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("zp19-rf") } }
        ),
        201,
        "ZALOPAY checkout for wallet refund"
      );
      orderId = co.orders[0].id;
      const oldPaymentId = co.payment_id;
      const totalPayableVnd = Number(co.total_payable_vnd ?? 0);

      const payRes = expectStatus(
        await buyerA.get(`/api/payments/${encodeURIComponent(oldPaymentId)}`),
        200,
        "GET ZALOPAY payment provider_ref"
      );
      const appTransId = String(payRes.payment?.provider_ref ?? "").trim();
      assert.ok(appTransId, "ZALOPAY payment missing provider_ref");

      // Switch ZALOPAY -> VIETQR so oldPaymentId becomes SUPERSEDED.
      expectStatus(
        await buyerA.post(
          `/api/payments/${encodeURIComponent(oldPaymentId)}/retry`,
          { method: "VIETQR" },
          { headers: { "Idempotency-Key": newIdempotencyKey("zp19-rf-sw") } }
        ),
        200,
        "retry ZALOPAY -> VIETQR"
      );

      // Signed late ZaloPay callback synchronously creates PENDING wallet refund.
      const cb = await postSignedZaloPayCallback({ appTransId, amountVnd: totalPayableVnd });
      assert.equal(cb.status, 200, `late ZaloPay callback failed: ${cb.text}`);
      assert.equal(cb.data?.return_code, 1, `expected return_code=1: ${cb.text}`);

      const refunds = expectStatus(
        await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}/refunds?page=1&page_size=20`),
        200,
        "GET /api/orders/[id]/refunds"
      );
      const walletRf = (refunds.items ?? refunds.refunds ?? []).find((r) => r.payment_id === oldPaymentId);
      assert.ok(walletRf, `expected late ZaloPay wallet refund: ${JSON.stringify(refunds)}`);
      assert.equal(normalizeStatus(walletRf.status), "PENDING");
      assert.ok(String(walletRf.provider_m_refund_id ?? "").trim().length > 0, "expected non-empty provider_m_refund_id");

      const orderPage = await buyerA.page(`/don-hang/${encodeURIComponent(orderId)}`);
      expectStatus(orderPage, 200, `SSR /don-hang/${orderId}`);
      assert.ok(orderPage.text.includes("Hoàn tiền"), "order SSR must render refund section");
      assert.ok(orderPage.text.includes("Đang hoàn về ví ZaloPay"), "order SSR must label PENDING ZaloPay wallet refund");
      assert.ok(
        orderPage.text.includes("Khoản hoàn đang được xử lý tự động về ví ZaloPay gốc."),
        "order SSR must inform buyer wallet refund is automatic"
      );
      assert.equal(
        orderPage.text.includes(`refund-bank-${walletRf.refund_id}`),
        false,
        "order SSR must NOT render bank-account form for PENDING ZaloPay wallet refund"
      );
    } finally {
      await cancelOrderBestEffort(buyerA, orderId, "zp19 wallet refund cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
