import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { payVietQR } from "./lib/bank.mjs";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, normalizeStatus, pollUntil } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  checkout,
  createShippingAddress,
  fillCart,
  newIdempotencyKey,
  orderBackendStatus,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("10. VietQR checkout on site host + HMAC payment webhook + waiting page SSR", () => {
  let siteA;
  let skuA;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("places VietQR order on site host, renders /thanh-toan/cho/[id], settles via signed webhook -> SUCCEEDED & CONFIRMED", async () => {
    const browser = new Browser("vietqr-site-a");
    const buyerA = browser.at(SITE_A_HOST);
    const rootAnon = new Browser("vietqr-root-anon").at(ROOT_HOST);

    try {
      // Prepaid VietQR does NOT require phone verification.
      await registerBuyer(buyerA, "vietqr-10");
      const address = await createShippingAddress(buyerA, "vietqr-10");
      const cart = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices } = await quoteShipping(buyerA, address, cart.shop_groups, false);

      const co = expectStatus(
        await checkout(buyerA, {
          addressId: address.id,
          shippingChoices,
          paymentMethod: "VIETQR",
          key: newIdempotencyKey("vietqr-10"),
        }),
        201,
        `POST ${SITE_A_HOST}/api/checkout (VIETQR)`
      );
      assert.ok(co.payment_id, "VietQR checkout must return payment_id");
      assert.ok(co.checkout_group_id, "VietQR checkout must return checkout_group_id");
      assert.equal(co.payment_method, "VIETQR");
      assert.equal(co.sales_channel, "STOREFRONT");
      assert.equal(co.storefront_shop_id, siteA.shopId);
      assert.equal(co.orders.length, 1);

      const orderId = co.orders[0].id;
      const paymentId = co.payment_id;
      const amountVND = Number(co.total_payable_vnd ?? 0);
      assert.ok(amountVND > 0, `expected positive total_payable_vnd, got ${amountVND}`);

      const payBefore = expectStatus(
        await buyerA.get(`/api/payments/${encodeURIComponent(paymentId)}`),
        200,
        `GET ${SITE_A_HOST}/api/payments/${paymentId}`
      );
      assert.equal(normalizeStatus(payBefore.payment?.status), "PENDING");

      // Waiting page SSR on site host renders 200; root host returns 404.
      const waitPage = await buyerA.page(`/thanh-toan/cho/${encodeURIComponent(paymentId)}`);
      expectStatus(waitPage, 200, `GET ${SITE_A_HOST}/thanh-toan/cho/${paymentId}`);
      assert.ok(
        waitPage.text.includes("VietQR") || waitPage.text.includes("Chuyển khoản"),
        "waiting page must render VietQR payment instructions"
      );
      assertNoInternalErrorLeak(waitPage.text, "VietQR wait page SSR");

      assert.equal(
        (await rootAnon.page(`/thanh-toan/cho/${encodeURIComponent(paymentId)}`)).status,
        404,
        "root host must return 404 for /thanh-toan/cho/[id]"
      );
      assert.equal(
        (await rootAnon.get(`/api/payments/${encodeURIComponent(paymentId)}`)).status,
        404,
        "root host must return 404 for /api/payments/[id]"
      );

      // Simulate external VietQR bank webhook.
      await payVietQR({ paymentId, groupId: co.checkout_group_id, amountVND });

      const paidPayment = await pollUntil(`payment ${paymentId} SUCCEEDED`, async () => {
        const r = await buyerA.get(`/api/payments/${encodeURIComponent(paymentId)}`);
        const st = normalizeStatus(r.data?.payment?.status);
        return [r.status === 200 && st === "SUCCEEDED", `http=${r.status} status=${st}`, r.data?.payment];
      });
      assert.equal(normalizeStatus(paidPayment.status), "SUCCEEDED");

      const confirmedOrder = await pollUntil(`order ${orderId} CONFIRMED`, async () => {
        const r = await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}`);
        const st = orderBackendStatus(r.data?.order);
        const paySt = normalizeStatus(r.data?.order?.paymentStatusRaw);
        return [
          r.status === 200 && st === "CONFIRMED" && paySt === "SUCCEEDED",
          `http=${r.status} status=${st} payStatus=${paySt}`,
          r.data?.order,
        ];
      });
      assert.equal(orderBackendStatus(confirmedOrder), "CONFIRMED");
      assert.equal(normalizeStatus(confirmedOrder.paymentStatusRaw), "SUCCEEDED");
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
