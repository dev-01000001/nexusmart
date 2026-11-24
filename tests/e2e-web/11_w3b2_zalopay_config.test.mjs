import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  config,
  createBuyerAddress,
  ensureRateBudget,
  normalizeStatus,
  pickSingleShopSku,
  registerAndLoginBuyer,
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

describe("11. Wave 3b-2 A6 (ZaloPay checkout, pay_url validation, payment method switching, admin payment-methods config & optimistic lock)", () => {
  test("1. [A6/F6/F7] Admin /admin/cau-hinh real payment-methods config, optimistic lock 409, persistence, audit log, and public view", async () => {
    await ensureRateBudget(40, { label: "w3b2-a6-admin-config" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    // /admin/cau-hinh SSR must not render removed mock "Ví NexusPay" label
    const configPage = await admin.page("/admin/cau-hinh");
    assert.equal(configPage.status, 200, `GET /admin/cau-hinh expected 200, got ${configPage.status}`);
    assert.equal(
      configPage.text.includes("Ví NexusPay"),
      false,
      "/admin/cau-hinh must not contain legacy mock Ví NexusPay"
    );

    const listRes = await admin.json("GET", "/api/gw/admin/payment-methods");
    assert.equal(
      listRes.status,
      200,
      `GET /api/gw/admin/payment-methods failed (${listRes.status}): ${listRes.text}`
    );
    const methods = Array.isArray(listRes.data?.methods) ? listRes.data.methods : [];
    const byCode = new Map(methods.map((m) => [normalizeStatus(m.method), m]));
    assert.ok(byCode.has("COD"), `missing COD in admin payment-methods: ${listRes.text}`);
    assert.ok(byCode.has("VIETQR"), `missing VIETQR in admin payment-methods: ${listRes.text}`);
    assert.ok(byCode.has("ZALOPAY"), `missing ZALOPAY in admin payment-methods: ${listRes.text}`);

    const origCod = byCode.get("COD");
    assert.ok(
      typeof origCod.version === "number" && origCod.version >= 1,
      `expected numeric version >= 1 on COD config: ${JSON.stringify(origCod)}`
    );

    const targetMaxVnd = origCod.max_order_vnd === 2_000_000 ? 2_500_000 : 2_000_000;
    let currentVersion = origCod.version;
    let mutated = false;

    try {
      const updateRes = await admin.json("PUT", "/api/gw/admin/payment-methods/COD", {
        body: {
          enabled: origCod.enabled,
          min_order_vnd: origCod.min_order_vnd,
          max_order_vnd: targetMaxVnd,
          expiry_minutes: origCod.expiry_minutes,
          note: "E2E 11 test COD limit update",
          expected_version: origCod.version,
        },
      });
      assert.equal(
        updateRes.status,
        200,
        `PUT /api/gw/admin/payment-methods/COD expected 200, got ${updateRes.status}: ${updateRes.text}`
      );
      mutated = true;
      const updatedCod = updateRes.data?.method ?? updateRes.data;
      assert.equal(updatedCod.max_order_vnd, targetMaxVnd);
      assert.ok(
        updatedCod.version > origCod.version,
        `expected version to increment from ${origCod.version}, got ${updatedCod.version}`
      );
      currentVersion = updatedCod.version;

      // Stale expected_version must be rejected with 409 VERSION_CONFLICT (F7)
      const staleRes = await admin.json("PUT", "/api/gw/admin/payment-methods/COD", {
        body: {
          enabled: origCod.enabled,
          min_order_vnd: origCod.min_order_vnd,
          max_order_vnd: 1_500_000,
          expiry_minutes: origCod.expiry_minutes,
          note: "stale write should conflict",
          expected_version: origCod.version,
        },
      });
      assert.equal(
        staleRes.status,
        409,
        `stale expected_version must return 409, got ${staleRes.status}: ${staleRes.text}`
      );
      const staleErr = getBffError(staleRes);
      assert.equal(
        staleErr.code,
        "VERSION_CONFLICT",
        `expected exact VERSION_CONFLICT error code, got ${staleRes.text}`
      );

      // Reload persisted value via public GET /api/gw/payment-methods (F6/F8: no credential fields)
      const anon = new WebClient();
      const pubRes = await anon.json("GET", "/api/gw/payment-methods");
      assert.equal(pubRes.status, 200, `public GET /payment-methods failed: ${pubRes.text}`);
      const pubMethods = Array.isArray(pubRes.data?.methods) ? pubRes.data.methods : [];
      const pubCod = pubMethods.find((m) => normalizeStatus(m.method) === "COD");
      assert.ok(pubCod, `public payment-methods missing COD: ${pubRes.text}`);
      assert.equal(pubCod.max_order_vnd, targetMaxVnd, "public COD max_order_vnd did not persist");
      assert.equal(
        "credential_preview" in pubCod,
        false,
        "public payment-methods must not expose credential_preview"
      );

      // Verify audit log recorded the payment method configuration change
      const auditRes = await admin.json(
        "GET",
        "/api/gw/admin/audit-logs?page=1&page_size=20"
      );
      assert.equal(auditRes.status, 200, `audit-logs failed: ${auditRes.text}`);
      const auditItems = Array.isArray(auditRes.data?.items) ? auditRes.data.items : [];
      const foundAudit = auditItems.some(
        (entry) =>
          String(entry.target_type ?? "").toLowerCase().includes("payment") ||
          String(entry.action ?? "").toLowerCase().includes("payment") ||
          String(entry.reason ?? "").includes("E2E 11")
      );
      assert.ok(foundAudit, `expected payment_method audit log entry in recent audit logs`);
    } finally {
      if (mutated) {
        // Restore original COD config so subsequent tests start with baseline values
        const latestRes = await admin.json("GET", "/api/gw/admin/payment-methods").catch(() => null);
        const latestCod = (latestRes?.data?.methods ?? []).find(
          (m) => normalizeStatus(m.method) === "COD"
        );
        const restoreVersion = latestCod?.version ?? currentVersion;
        await admin
          .json("PUT", "/api/gw/admin/payment-methods/COD", {
            body: {
              enabled: origCod.enabled,
              min_order_vnd: origCod.min_order_vnd,
              max_order_vnd: origCod.max_order_vnd,
              expiry_minutes: origCod.expiry_minutes,
              note: origCod.note ?? "",
              expected_version: restoreVersion,
            },
          })
          .catch(() => {});
      }
    }
  });

  test("2. [A6/F5/F8] Buyer checkout with ZaloPay, safe pay_url, waiting page, and switch ZALOPAY -> VIETQR -> COD", async () => {
    await ensureRateBudget(45, { label: "w3b2-a6-buyer-zalopay" });
    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w3b2-zp", { verifyPhone: true });
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const { product, sku } = await pickSingleShopSku(buyer, { maxTotalVnd: 1_500_000 });

    const quote = await calculateShippingQuote(buyer, {
      shopId: product.shop_id,
      province,
      ward,
      weightGrams: 300,
      codAmountVnd: sku.price_vnd,
    });

    // Public payment-methods endpoint exposes ZALOPAY
    const pmRes = await buyer.json("GET", "/api/gw/payment-methods");
    assert.equal(pmRes.status, 200, `GET /payment-methods failed: ${pmRes.text}`);
    const zpCfg = (pmRes.data?.methods ?? []).find(
      (m) => normalizeStatus(m.method) === "ZALOPAY"
    );
    assert.ok(zpCfg && zpCfg.enabled === true, `expected enabled ZALOPAY method: ${pmRes.text}`);

    let createdOrderId = null;
    try {
      // Checkout with ZALOPAY
      const checkoutRes = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `e2e-w3b2-zp-co-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          payment_method: "ZALOPAY",
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
        checkoutRes.status,
        201,
        `ZALOPAY checkout expected 201, got ${checkoutRes.status}: ${checkoutRes.text}`
      );
      const paymentId = checkoutRes.data?.payment_id;
      const payUrl = checkoutRes.data?.pay_url;
      createdOrderId = checkoutRes.data?.orders?.[0]?.id ?? null;
      assert.ok(paymentId, `ZALOPAY checkout missing payment_id: ${checkoutRes.text}`);
      assert.ok(createdOrderId, `ZALOPAY checkout missing order id: ${checkoutRes.text}`);
      assert.equal(normalizeStatus(checkoutRes.data?.payment_method), "ZALOPAY");
      assert.ok(
        isAllowedPayUrl(payUrl),
        `ZALOPAY pay_url must be safe allowlisted URL or same-origin mock path, got: ${payUrl}`
      );

      // Waiting page renders SSR 200, and same-origin mock ZaloPay simulator renders 200
      const waitingPage = await buyer.page(
        `/thanh-toan/cho-thanh-toan/${encodeURIComponent(paymentId)}`
      );
      assert.equal(
        waitingPage.status,
        200,
        `waiting page expected 200, got ${waitingPage.status}`
      );

      if (String(payUrl).startsWith("/thanh-toan/mock-zalopay")) {
        const mockPage = await buyer.page(payUrl);
        assert.equal(mockPage.status, 200, `mock-zalopay page expected 200, got ${mockPage.status}`);
        assert.ok(
          mockPage.text.includes("Trang mô phỏng, không phải trang ZaloPay thật"),
          "mock-zalopay page must display neutral mock disclaimer"
        );
      }

      // UC-B25: Switch ZALOPAY -> VIETQR via retry with Idempotency-Key
      const toVietQrRes = await buyer.json(
        "POST",
        `/api/gw/payments/${encodeURIComponent(paymentId)}/retry`,
        {
          headers: { "Idempotency-Key": `e2e-w3b2-switch-vqr-${crypto.randomUUID()}` },
          body: { method: "VIETQR" },
        }
      );
      assert.equal(
        toVietQrRes.status,
        200,
        `switch ZALOPAY->VIETQR expected 200, got ${toVietQrRes.status}: ${toVietQrRes.text}`
      );
      const vietqrPaymentId = toVietQrRes.data?.payment?.payment_id || "";
      assert.ok(vietqrPaymentId, `switch->VIETQR missing new payment.payment_id: ${toVietQrRes.text}`);
      assert.notEqual(vietqrPaymentId, paymentId, "switch->VIETQR should create a new payment");
      assert.equal(normalizeStatus(toVietQrRes.data?.payment?.method), "VIETQR");
      assert.equal(Boolean(toVietQrRes.data?.switched_to_cod), false);

      // UC-B25: Switch VIETQR -> COD via retry with Idempotency-Key
      const toCodRes = await buyer.json(
        "POST",
        `/api/gw/payments/${encodeURIComponent(vietqrPaymentId)}/retry`,
        {
          headers: { "Idempotency-Key": `e2e-w3b2-switch-cod-${crypto.randomUUID()}` },
          body: { method: "COD" },
        }
      );
      assert.equal(
        toCodRes.status,
        200,
        `switch VIETQR->COD expected 200, got ${toCodRes.status}: ${toCodRes.text}`
      );
      assert.equal(
        toCodRes.data?.switched_to_cod,
        true,
        `switch->COD expected switched_to_cod=true: ${toCodRes.text}`
      );

      // Verify order is now CONFIRMED with payment_method COD
      const orderRes = await buyer.json(
        "GET",
        `/api/gw/orders/${encodeURIComponent(createdOrderId)}`
      );
      assert.equal(orderRes.status, 200, `GET order failed: ${orderRes.text}`);
      const orderObj = orderRes.data?.order ?? orderRes.data;
      assert.equal(normalizeStatus(orderObj?.status), "CONFIRMED");
      assert.equal(normalizeStatus(orderObj?.payment_method), "COD");
    } finally {
      if (createdOrderId) {
        await buyer
          .json("POST", `/api/gw/orders/${encodeURIComponent(createdOrderId)}/cancel`, {
            body: { reason: "e2e 11 cleanup" },
          })
          .catch(() => {});
      }
    }
  });

  test("3. [A6] Admin payment fee config accepts ZALOPAY method", async () => {
    await ensureRateBudget(20, { label: "w3b2-a6-zalopay-fee" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    const futureFrom = new Date(Date.now() + 7 * 86_400_000).toISOString();
    const setRes = await admin.json("POST", "/api/gw/admin/fees/payment", {
      body: {
        payment_method: "ZALOPAY",
        fee_bps: 150,
        effective_from: futureFrom,
      },
    });
    assert.equal(
      setRes.status,
      201,
      `POST /api/gw/admin/fees/payment (ZALOPAY) expected 201, got ${setRes.status}: ${setRes.text}`
    );

    const listRes = await admin.json("GET", "/api/gw/admin/fees/payment?include_history=true");
    assert.equal(listRes.status, 200, `GET fees/payment failed: ${listRes.text}`);
    const configs = [
      ...(Array.isArray(listRes.data?.active_configs) ? listRes.data.active_configs : []),
      ...(Array.isArray(listRes.data?.history) ? listRes.data.history : []),
    ];
    const hasZaloPay = configs.some(
      (c) => normalizeStatus(c.payment_method) === "ZALOPAY" && Number(c.fee_bps) === 150
    );
    assert.ok(hasZaloPay, `expected ZALOPAY 150 bps fee config in list: ${listRes.text}`);
  });

  test("4. [A6/web-r3] 0 VND order via vouchers while VIETQR min_order_vnd > 0 skips waiting page and renders confirmed & paid order detail", async () => {
    await ensureRateBudget(55, { label: "w3b2-a6-zero-vnd-order" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w3b2-zero", { verifyPhone: true });
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const { product, sku } = await pickSingleShopSku(buyer, { maxTotalVnd: 500_000 });

    const quote = await calculateShippingQuote(buyer, {
      shopId: product.shop_id,
      province,
      ward,
      weightGrams: 300,
      codAmountVnd: 0,
    });

    const listRes = await admin.json("GET", "/api/gw/admin/payment-methods");
    assert.equal(listRes.status, 200, `GET payment-methods failed: ${listRes.text}`);
    const origVietQr = (listRes.data?.methods ?? []).find(
      (m) => normalizeStatus(m.method) === "VIETQR"
    );
    assert.ok(origVietQr, `missing VIETQR config: ${listRes.text}`);

    const tag = crypto.randomBytes(3).toString("hex").toUpperCase();
    const startsAt = new Date(Date.now() - 5 * 60_000).toISOString();
    const endsAt = new Date(Date.now() + 2 * 3_600_000).toISOString();
    const createdVoucherIds = [];
    let vietQrMutated = false;
    let vietQrVersion = origVietQr.version;
    let createdOrderId = null;

    try {
      // Raise VIETQR min_order_vnd strictly above normal order total; 0 VND order must still pass.
      const raisedMinVnd = sku.price_vnd + quote.fee_vnd + 100_000;
      const raisedMaxVnd =
        origVietQr.max_order_vnd > 0 && origVietQr.max_order_vnd < raisedMinVnd
          ? raisedMinVnd + 1_000_000
          : origVietQr.max_order_vnd;
      const minRes = await admin.json("PUT", "/api/gw/admin/payment-methods/VIETQR", {
        body: {
          enabled: true,
          min_order_vnd: raisedMinVnd,
          max_order_vnd: raisedMaxVnd,
          expiry_minutes: origVietQr.expiry_minutes,
          note: "E2E 11 0 VND min_order_vnd exemption check",
          expected_version: origVietQr.version,
        },
      });
      assert.equal(
        minRes.status,
        200,
        `PUT VIETQR min_order_vnd expected 200, got ${minRes.status}: ${minRes.text}`
      );
      vietQrMutated = true;
      vietQrVersion = minRes.data?.method?.version ?? vietQrVersion + 1;

      const platRes = await admin.json("POST", "/api/gw/admin/vouchers", {
        body: {
          code: `W3B2ZPLAT${tag}`,
          name: `0 VND Platform ${tag}`,
          scope: "PLATFORM",
          discount_type: "FIXED",
          value_vnd: sku.price_vnd,
          min_order_vnd: 0,
          quota: 5,
          per_user_limit: 2,
          budget_vnd: sku.price_vnd * 5,
          starts_at: startsAt,
          ends_at: endsAt,
        },
      });
      assert.equal(
        platRes.status,
        201,
        `create PLATFORM voucher expected 201, got ${platRes.status}: ${platRes.text}`
      );
      const platVoucherId = platRes.data?.voucher?.id;
      assert.ok(platVoucherId, `missing platform voucher id: ${platRes.text}`);
      createdVoucherIds.push(platVoucherId);

      const shipFeeCap = Math.max(quote.fee_vnd + 10_000, 50_000);
      const shipRes = await admin.json("POST", "/api/gw/admin/vouchers", {
        body: {
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
        },
      });
      assert.equal(
        shipRes.status,
        201,
        `create FREESHIP voucher expected 201, got ${shipRes.status}: ${shipRes.text}`
      );
      const shipVoucherId = shipRes.data?.voucher?.id;
      assert.ok(shipVoucherId, `missing freeship voucher id: ${shipRes.text}`);
      createdVoucherIds.push(shipVoucherId);

      for (const vid of [platVoucherId, shipVoucherId]) {
        const saveRes = await buyer.json(
          "POST",
          `/api/gw/vouchers/${encodeURIComponent(vid)}/save`,
          { body: {} }
        );
        assert.equal(saveRes.status, 200, `save voucher ${vid} expected 200: ${saveRes.text}`);
      }

      const coRes = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `e2e-w3b2-zero-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          payment_method: "VIETQR",
          items: [{ sku_id: sku.id, quantity: 1 }],
          platform_voucher_id: platVoucherId,
          freeship_voucher_id: shipVoucherId,
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
        coRes.status,
        201,
        `0 VND checkout while min_order_vnd > 0 expected 201, got ${coRes.status}: ${coRes.text}`
      );
      assert.equal(
        coRes.data?.total_payable_vnd,
        0,
        `expected total_payable_vnd=0, got ${coRes.text}`
      );
      assert.equal(
        normalizeStatus(coRes.data?.payment_status),
        "SUCCEEDED",
        `0 VND checkout payment_status must be SUCCEEDED: ${coRes.text}`
      );
      const zeroOrder = coRes.data?.orders?.[0];
      createdOrderId = zeroOrder?.id ?? null;
      assert.ok(createdOrderId, `0 VND checkout missing order id: ${coRes.text}`);
      assert.equal(
        normalizeStatus(zeroOrder?.status),
        "CONFIRMED",
        `0 VND checkout order status must be CONFIRMED: ${coRes.text}`
      );

      // Post-checkout destination goes straight to /don-hang/{id} (never to /thanh-toan/cho-thanh-toan/...)
      // and order detail SSR displays confirmed & paid state with no waiting-payment link.
      const orderPage = await buyer.page(`/don-hang/${encodeURIComponent(createdOrderId)}`);
      assert.equal(
        orderPage.status,
        200,
        `GET /don-hang/${createdOrderId} expected 200, got ${orderPage.status}`
      );
      assert.equal(
        orderPage.location,
        null,
        `0 VND order detail must not redirect, got location=${orderPage.location}`
      );
      assert.ok(
        orderPage.text.includes("Đã xác nhận"),
        "0 VND order detail SSR must display 'Đã xác nhận'"
      );
      assert.ok(
        orderPage.text.includes("Đã thanh toán"),
        "0 VND order detail SSR must display 'Đã thanh toán'"
      );
      assert.equal(
        orderPage.text.includes("/thanh-toan/cho-thanh-toan/"),
        false,
        "0 VND order detail SSR must not link to /thanh-toan/cho-thanh-toan/"
      );
      assert.equal(
        orderPage.text.includes("Chờ thanh toán"),
        false,
        "0 VND order detail SSR must not render 'Chờ thanh toán'"
      );
    } finally {
      if (createdOrderId) {
        await buyer
          .json("POST", `/api/gw/orders/${encodeURIComponent(createdOrderId)}/cancel`, {
            body: { reason: "e2e 11 cleanup 0 VND order" },
          })
          .catch(() => {});
      }
      for (const vid of createdVoucherIds) {
        await admin
          .json("DELETE", `/api/gw/admin/vouchers/${encodeURIComponent(vid)}`)
          .catch(() => {});
      }
      if (vietQrMutated) {
        const latestRes = await admin.json("GET", "/api/gw/admin/payment-methods").catch(() => null);
        const latestVqr = (latestRes?.data?.methods ?? []).find(
          (m) => normalizeStatus(m.method) === "VIETQR"
        );
        await admin
          .json("PUT", "/api/gw/admin/payment-methods/VIETQR", {
            body: {
              enabled: origVietQr.enabled,
              min_order_vnd: origVietQr.min_order_vnd,
              max_order_vnd: origVietQr.max_order_vnd,
              expiry_minutes: origVietQr.expiry_minutes,
              note: origVietQr.note ?? "",
              expected_version: latestVqr?.version ?? vietQrVersion,
            },
          })
          .catch(() => {});
      }
    }
  });

  test("5. [A6/web-r2] ZaloPay PENDING wallet refund hides bank-account form on buyer order detail page", async () => {
    await ensureRateBudget(45, { label: "w3b2-a6-wallet-refund-ui" });
    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w3b2-zprf", { verifyPhone: true });
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const { product, sku } = await pickSingleShopSku(buyer, { maxTotalVnd: 500_000 });

    const quote = await calculateShippingQuote(buyer, {
      shopId: product.shop_id,
      province,
      ward,
      weightGrams: 300,
      codAmountVnd: 0,
    });

    let createdOrderId = null;
    try {
      const coRes = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `e2e-w3b2-zprf-co-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          payment_method: "ZALOPAY",
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
        coRes.status,
        201,
        `ZALOPAY checkout expected 201, got ${coRes.status}: ${coRes.text}`
      );
      const oldPaymentId = coRes.data?.payment_id;
      const totalPayableVnd = Number(coRes.data?.total_payable_vnd ?? 0);
      createdOrderId = coRes.data?.orders?.[0]?.id ?? null;
      assert.ok(oldPaymentId && createdOrderId && totalPayableVnd > 0, `unexpected checkout: ${coRes.text}`);

      const payRes = await buyer.json(
        "GET",
        `/api/gw/payments/${encodeURIComponent(oldPaymentId)}`
      );
      assert.equal(payRes.status, 200, `GET ZALOPAY payment failed: ${payRes.text}`);
      const appTransId = String(payRes.data?.payment?.provider_ref ?? "").trim();
      assert.ok(appTransId, `ZALOPAY payment missing provider_ref (app_trans_id): ${payRes.text}`);

      // Switch ZALOPAY -> VIETQR so oldPaymentId becomes SUPERSEDED.
      const switchRes = await buyer.json(
        "POST",
        `/api/gw/payments/${encodeURIComponent(oldPaymentId)}/retry`,
        {
          headers: { "Idempotency-Key": `e2e-w3b2-zprf-retry-${crypto.randomUUID()}` },
          body: { method: "VIETQR" },
        }
      );
      assert.equal(
        switchRes.status,
        200,
        `retry ZALOPAY->VIETQR expected 200, got ${switchRes.status}: ${switchRes.text}`
      );

      // Send a signed late ZaloPay callback for the superseded payment: synchronously creates a
      // PENDING wallet refund with non-empty provider_m_refund_id before the 15s background poller runs.
      const nowMs = Date.now();
      const zpTransId = nowMs * 1000 + crypto.randomInt(1, 999);
      const dataStr = JSON.stringify({
        app_id: config.zaloPayAppId,
        app_trans_id: appTransId,
        app_time: nowMs,
        app_user: "nexusmart-e2e-web",
        amount: totalPayableVnd,
        embed_data: "{}",
        item: "[]",
        zp_trans_id: zpTransId,
        server_time: nowMs,
        channel: 38,
        merchant_user_id: "",
        user_fee_amount: 0,
        discount_amount: 0,
      });
      const mac = crypto
        .createHmac("sha256", config.zaloPayKey2)
        .update(dataStr)
        .digest("hex");
      const cbRes = await fetch(`${config.gatewayUrl}/webhooks/zalopay`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ data: dataStr, mac, type: 1 }),
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      const cbBody = await cbRes.json();
      assert.equal(
        cbRes.status,
        200,
        `late ZaloPay callback expected HTTP 200, got ${cbRes.status}: ${JSON.stringify(cbBody)}`
      );
      assert.equal(
        cbBody?.return_code,
        1,
        `late ZaloPay callback expected return_code=1, got ${JSON.stringify(cbBody)}`
      );

      // Immediately verify buyer refunds API sees PENDING wallet refund with provider_m_refund_id.
      const refundsRes = await buyer.json(
        "GET",
        `/api/gw/orders/${encodeURIComponent(createdOrderId)}/refunds?page=1&page_size=20`
      );
      assert.equal(
        refundsRes.status,
        200,
        `GET order refunds expected 200, got ${refundsRes.status}: ${refundsRes.text}`
      );
      const walletRf = (refundsRes.data?.items ?? []).find(
        (rf) => rf.payment_id === oldPaymentId
      );
      assert.ok(walletRf, `expected late ZaloPay refund for payment ${oldPaymentId}: ${refundsRes.text}`);
      assert.equal(
        normalizeStatus(walletRf.status),
        "PENDING",
        `expected PENDING wallet refund before poller tick, got ${JSON.stringify(walletRf)}`
      );
      assert.ok(
        String(walletRf.provider_m_refund_id ?? "").trim().length > 0,
        `expected non-empty provider_m_refund_id on ZaloPay wallet refund: ${JSON.stringify(walletRf)}`
      );

      // Immediately verify buyer order detail SSR renders wallet refund status WITHOUT bank account form.
      const orderPage = await buyer.page(`/don-hang/${encodeURIComponent(createdOrderId)}`);
      assert.equal(
        orderPage.status,
        200,
        `GET /don-hang/${createdOrderId} expected 200, got ${orderPage.status}`
      );
      assert.ok(
        orderPage.text.includes("Thông tin hoàn tiền"),
        "buyer order detail SSR must render refund section"
      );
      assert.ok(
        orderPage.text.includes("Đang hoàn về ví ZaloPay"),
        "buyer order detail SSR must label PENDING ZaloPay wallet refund as 'Đang hoàn về ví ZaloPay'"
      );
      assert.ok(
        orderPage.text.includes("Bạn không cần nhập tài khoản ngân hàng"),
        "buyer order detail SSR must inform buyer no bank account input is needed for wallet refund"
      );
      assert.equal(
        orderPage.text.includes(`bank-code-${walletRf.refund_id}`),
        false,
        "buyer order detail SSR must NOT render bank-code select for PENDING ZaloPay wallet refund"
      );
      assert.equal(
        orderPage.text.includes("Ngân hàng nhận"),
        false,
        "buyer order detail SSR must NOT render bank account form label for PENDING ZaloPay wallet refund"
      );
      assert.equal(
        orderPage.text.includes("Số tài khoản (6–19 chữ số)"),
        false,
        "buyer order detail SSR must NOT render bank account number input for PENDING ZaloPay wallet refund"
      );
    } finally {
      if (createdOrderId) {
        await buyer
          .json("POST", `/api/gw/orders/${encodeURIComponent(createdOrderId)}/cancel`, {
            body: { reason: "e2e 11 cleanup wallet refund test order" },
          })
          .catch(() => {});
      }
    }
  });
});
