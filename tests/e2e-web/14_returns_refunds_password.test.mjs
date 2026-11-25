import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { deliverShipmentViaWebhook, payVietQR } from "./lib/bank.mjs";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, normalizeStatus, pollUntil } from "./lib/http.mjs";
import { snapshotMailpitIds, waitForPasswordResetToken } from "./lib/mailpit.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  checkout,
  createShippingAddress,
  demoCredentials,
  fillCart,
  loginOn,
  newIdempotencyKey,
  orderBackendStatus,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

function assertSingleSandboxLabelHeaders(res, label) {
  expectStatus(res, 200, label);
  assert.ok(
    String(res.headers["content-type"] || "").toLowerCase().includes("text/html"),
    `${label}: expected text/html, got ${res.headers["content-type"]}`
  );
  assert.equal(res.headers["x-content-type-options"], "nosniff", `${label}: missing nosniff`);
  const csp = String(res.headers["content-security-policy"] || "");
  assert.ok(csp.includes("sandbox"), `${label}: CSP must include sandbox, got: ${csp}`);
  assert.equal(csp.includes(","), false, `${label}: CSP must be single header (no comma duplicate): ${csp}`);
}

describe("14. Returns 10-state + refund settlement, password reset security, carrier/reconciliation smoke & 2 MiB guard", () => {
  let siteA;
  let skuA;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
    sellerRoot = new Browser("ret14-seller").at(ROOT_HOST);
    adminRoot = new Browser("ret14-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "ret14-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "ret14-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("6a: Full VietQR -> ship (PREPARING + sandbox label) -> return -> return label -> confirm-received -> bank-account -> admin settle -> DONE", async () => {
    const buyerA = new Browser("ret14-buyer").at(SITE_A_HOST);

    try {
      await registerBuyer(buyerA, "ret14-full");
      const addr = await createShippingAddress(buyerA, "ret14-full");
      const cart = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices } = await quoteShipping(buyerA, addr, cart.shop_groups, false);

      const co = expectStatus(
        await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices,
          paymentMethod: "VIETQR",
          key: newIdempotencyKey("ret14-vq"),
        }),
        201,
        "VietQR checkout for return flow"
      );
      const orderId = co.orders[0].id;
      const paymentId = co.payment_id;
      await payVietQR({
        paymentId,
        groupId: co.checkout_group_id,
        amountVND: Number(co.total_payable_vnd),
      });

      await pollUntil(`order ${orderId} CONFIRMED`, async () => {
        const r = await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}`);
        const st = orderBackendStatus(r.data?.order);
        return [r.status === 200 && st === "CONFIRMED", `status=${st}`, r.data?.order];
      });

      // Seller ships -> PREPARING + tracking_code.
      const carrierCode = shippingChoices[0]?.carrier_code || "mock_express";
      const serviceCode = shippingChoices[0]?.service_code || "STANDARD";
      const shipRes = expectStatus(
        await sellerRoot.post(`/api/seller/orders/${encodeURIComponent(orderId)}/ship`, {
          carrier_code: carrierCode,
          service_code: serviceCode,
        }),
        200,
        "seller ship order"
      );
      assert.equal(normalizeStatus(shipRes.order?.status), "PREPARING");
      const fwdTracking = shipRes.tracking_code || shipRes.order?.tracking_code;
      assert.ok(fwdTracking, "seller ship must return tracking_code");

      // Seller forward label via BFF has single sandbox CSP & nosniff.
      const fwdLabel = await sellerRoot.get(`/api/shipping/labels/${encodeURIComponent(fwdTracking)}`, {
        headers: { accept: "text/html" },
      });
      assertSingleSandboxLabelHeaders(fwdLabel, "seller forward shipping label");

      // Deliver forward shipment via signed shipping webhooks.
      await deliverShipmentViaWebhook({ carrierCode, trackingCode: fwdTracking, orderId });
      await pollUntil(`order ${orderId} DELIVERED`, async () => {
        const r = await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}`);
        const st = orderBackendStatus(r.data?.order);
        return [r.status === 200 && st === "DELIVERED", `status=${st}`, r.data?.order];
      });

      // Buyer creates return request with external evidence URL.
      const retCreate = expectStatus(
        await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/return`, {
          reason: "Sản phẩm sai kích cỡ cần đổi trả hoàn tiền",
          evidence_urls: ["https://example.com/e2e-return-proof.jpg"],
        }),
        200,
        "POST /api/orders/[id]/return"
      );
      const returnId = retCreate.return_request_id || retCreate.returnRequestId;
      assert.ok(returnId, "return_request_id required");

      // Buyer SSR /don-hang/[id] renders return section + external evidence hostname + nofollow.
      const orderPage = await buyerA.page(`/don-hang/${encodeURIComponent(orderId)}`);
      expectStatus(orderPage, 200, `SSR /don-hang/${orderId}`);
      const cleanHtml = orderPage.text.replace(/<!--[\s\S]*?-->/g, "");
      assert.ok(cleanHtml.includes("Yêu cầu đổi trả"), "SSR order detail must render return section");
      assert.ok(
        cleanHtml.includes("(example.com)") && cleanHtml.includes('rel="noopener noreferrer nofollow"'),
        "SSR order detail must show external evidence hostname with nofollow"
      );

      // Seller accepts return -> generates return tracking code.
      const acceptRes = expectStatus(
        await sellerRoot.post(`/api/seller/returns/${encodeURIComponent(returnId)}/respond`, {
          decision: "ACCEPT_RETURN",
          note: "Đồng ý nhận lại hàng để kiểm tra hoàn tiền",
          evidence_urls: [],
        }),
        200,
        "seller ACCEPT_RETURN"
      );
      const returnTracking = acceptRes.return_request?.return_tracking_code;
      assert.ok(returnTracking, `expected return_tracking_code: ${JSON.stringify(acceptRes)}`);

      // Site shipping label requires ?order_id=... (W5 tenant isolation; returns 404 to avoid leaking existence).
      const missingOrderLabel = await buyerA.get(
        `/api/shipping/labels/${encodeURIComponent(returnTracking)}`,
        { headers: { accept: "text/html" } }
      );
      expectStatus(missingOrderLabel, 404, "site label without order_id must return 404");

      const retLabel = await buyerA.get(
        `/api/shipping/labels/${encodeURIComponent(returnTracking)}?order_id=${encodeURIComponent(orderId)}`,
        { headers: { accept: "text/html" } }
      );
      assertSingleSandboxLabelHeaders(retLabel, "buyer site RETURN shipping label");

      // Deliver return shipment via webhook -> seller confirms receipt.
      await deliverShipmentViaWebhook({
        carrierCode: acceptRes.return_request?.return_carrier_code || carrierCode,
        trackingCode: returnTracking,
        orderId,
      });

      await pollUntil(`return ${returnId} IN_TRANSIT or RECEIVED`, async () => {
        const r = await sellerRoot.get(`/api/seller/returns/${encodeURIComponent(returnId)}`);
        const st = normalizeStatus(r.data?.return_request?.status);
        return [
          r.status === 200 && ["IN_TRANSIT", "RECEIVED", "AWAITING_SHIPMENT"].includes(st),
          `http=${r.status} status=${st}`,
          r.data?.return_request,
        ];
      });

      expectStatus(
        await sellerRoot.post(`/api/seller/returns/${encodeURIComponent(returnId)}/confirm-received`, {}),
        200,
        "seller confirm-received"
      );

      // Buyer polls for PENDING refund, submits bank account (with order_id).
      const pendingRefund = await pollUntil(`PENDING refund for order ${orderId}`, async () => {
        const r = await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}/refunds?page=1&page_size=20`);
        const list = r.data?.items ?? r.data?.refunds ?? [];
        const rf = list.find((x) => normalizeStatus(x.status) === "PENDING" || normalizeStatus(x.status) === "MANUAL_REVIEW");
        return [Boolean(rf), `count=${list.length}`, rf];
      });

      const fullAcctNum = "01234567891234";
      expectStatus(
        await buyerA.post(`/api/refunds/${encodeURIComponent(pendingRefund.refund_id)}/bank-account`, {
          order_id: orderId,
          bank_code: "VCB",
          bank_account_number: fullAcctNum,
          bank_account_name: "NGUYEN VAN HOAN TIEN",
        }),
        200,
        "buyer submit refund bank-account"
      );

      // Admin sees full account number in /api/admin/refunds and settles refund with Idempotency-Key.
      const adminRefunds = expectStatus(
        await adminRoot.get("/api/admin/refunds?page=1&page_size=50"),
        200,
        "GET /api/admin/refunds"
      );
      const adminRf = (adminRefunds.items ?? []).find((x) => x.refund_id === pendingRefund.refund_id);
      assert.ok(adminRf, `admin refunds missing ${pendingRefund.refund_id}`);
      assert.equal(adminRf.bank_account_number, fullAcctNum, "admin refund must expose full account number");

      const settleRes = expectStatus(
        await adminRoot.post(
          `/api/admin/refunds/${encodeURIComponent(pendingRefund.refund_id)}/settle`,
          {
            provider_refund_id: `FT-E2E-${Date.now()}`,
            note: "Đã chuyển khoản hoàn tiền E2E 14",
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("settle-14") } }
        ),
        200,
        "POST /api/admin/refunds/[id]/settle"
      );
      assert.equal(normalizeStatus(settleRes.refund?.status ?? settleRes.status), "DONE");

      // Buyer sees DONE refund.
      const buyerRefundsAfter = expectStatus(
        await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}/refunds?page=1&page_size=20`),
        200,
        "buyer GET order refunds after settle"
      );
      const settledRf = (buyerRefundsAfter.items ?? buyerRefundsAfter.refunds ?? []).find(
        (x) => x.refund_id === pendingRefund.refund_id
      );
      assert.equal(normalizeStatus(settledRf?.status), "DONE");
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("6b: Forgot & reset password flow on site and root hosts with security headers (Referrer-Policy, XFO, nosniff)", async () => {
    const browser = new Browser("ret14-pwreset");
    const buyerA = browser.at(SITE_A_HOST);
    const anonRoot = new Browser("ret14-pwreset-root").at(ROOT_HOST);

    try {
      const acct = await registerBuyer(buyerA, "pwreset");
      await buyerA.post("/api/auth/logout", {});

      for (const client of [buyerA, anonRoot]) {
        expectStatus(await client.page("/quen-mat-khau"), 200, `GET ${client.host}/quen-mat-khau`);
        const resetPage = await client.page("/dat-lai-mat-khau?token=dummy-header-check");
        expectStatus(resetPage, 200, `GET ${client.host}/dat-lai-mat-khau`);
        assert.equal(
          String(resetPage.headers["referrer-policy"] || "").toLowerCase(),
          "no-referrer",
          `${client.host}/dat-lai-mat-khau must set Referrer-Policy: no-referrer`
        );
        assert.equal(
          String(resetPage.headers["x-frame-options"] || "").toUpperCase(),
          "DENY",
          `${client.host}/dat-lai-mat-khau must set X-Frame-Options: DENY`
        );
        assert.equal(
          String(resetPage.headers["x-content-type-options"] || "").toLowerCase(),
          "nosniff",
          `${client.host}/dat-lai-mat-khau must set X-Content-Type-Options: nosniff`
        );
      }

      const seenIds = await snapshotMailpitIds(acct.creds.email);
      expectStatus(
        await buyerA.post("/api/auth/forgot-password", { email: acct.creds.email }),
        200,
        "POST /api/auth/forgot-password on site"
      );
      const token = await waitForPasswordResetToken(acct.creds.email, seenIds);
      const newPassword = `${acct.creds.password}New9!`;

      expectStatus(
        await buyerA.post("/api/auth/reset-password", { token, new_password: newPassword }),
        200,
        "POST /api/auth/reset-password on site"
      );

      // Old password must fail; new password must succeed.
      const oldLogin = await buyerA.post("/api/auth/login", {
        email: acct.creds.email,
        password: acct.creds.password,
      });
      assert.equal(oldLogin.status, 401, "old password must fail 401 after reset");

      await loginOn(buyerA, acct.creds.email, newPassword, "after-pw-reset");
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("6c & 6e: Seller/Admin carrier & reconciliation smoke + 2 MiB body limit (Content-Length & chunked) -> 413 nosniff", async () => {
    expectStatus(await sellerRoot.get("/api/seller/shipping-settings"), 200, "GET /api/seller/shipping-settings");
    expectStatus(await sellerRoot.page("/seller/don-hang"), 200, "GET /seller/don-hang");
    expectStatus(await sellerRoot.page("/seller/tra-hang"), 200, "GET /seller/tra-hang");
    expectStatus(await sellerRoot.page("/seller/ho-so"), 200, "GET /seller/ho-so");
    expectStatus(await adminRoot.get("/api/admin/carriers"), 200, "GET /api/admin/carriers");
    expectStatus(await adminRoot.get("/api/admin/reconciliation/queue"), 200, "GET /api/admin/reconciliation/queue");
    expectStatus(await adminRoot.get("/api/admin/returns"), 200, "GET /api/admin/returns");
    expectStatus(await adminRoot.page("/admin/doi-soat"), 200, "GET /admin/doi-soat");
    expectStatus(await adminRoot.page("/admin/khieu-nai"), 200, "GET /admin/khieu-nai");

    // 6e: >2 MiB body on site BFF returns 413 + X-Content-Type-Options: nosniff for BOTH Content-Length and chunked.
    const buyerA = new Browser("ret14-bigbody").at(SITE_A_HOST);
    try {
      await registerBuyer(buyerA, "bigbody");
      const pad = "a".repeat(2 * 1024 * 1024 + 4096);
      const hugePayload = JSON.stringify({ sku_id: skuA.skuId, quantity: 1, pad });

      const clRes = await buyerA.request("POST", "/api/cart/items", {
        rawBody: hugePayload,
        contentType: "application/json",
      });
      expectStatus(clRes, 413, "Content-Length >2 MiB must return 413");
      assert.equal(clRes.headers["x-content-type-options"], "nosniff", "413 must include nosniff");

      const chunkedRes = await buyerA.request("POST", "/api/cart/items", {
        rawBody: hugePayload,
        contentType: "application/json",
        chunked: true,
      });
      expectStatus(chunkedRes, 413, "Chunked >2 MiB must return 413");
      assert.equal(chunkedRes.headers["x-content-type-options"], "nosniff", "chunked 413 must include nosniff");
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
