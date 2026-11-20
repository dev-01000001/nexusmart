import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { WebSession, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  assertNoInternalErrorLeak,
  createShippingAddress,
  demoCredentials,
  discoverTwoShopSkus,
  getShippingChoicesForCart,
  loginAccount,
  newIdempotencyKey,
  registerVerifyLoginBuyer,
} from "./lib/fixtures.mjs";

const HTML_HEADERS = { accept: "text/html,application/xhtml+xml" };

describe("03. Phase 1 Sales Channel BFF Contract, Channel Filters & Proxy Header Hardening", () => {
  let sku1;
  let sku2;
  let buyer;
  let addr;

  before(async () => {
    await resetRateLimitKeys();
    [sku1, sku2] = await discoverTwoShopSkus({ minStock: 5 });
    buyer = await registerVerifyLoginBuyer("channel-bff");
    addr = await createShippingAddress(buyer.session, "channel-bff");
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("(a) BFF POST /api/checkout ignores client sales_channel=STOREFRONT and forces MARKETPLACE; GET /api/cart?shop_id filters without mutating cart", async () => {
    const { session } = buyer;

    // Add items from 2 different shops into the single user cart.
    expectStatus(
      await session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (shop 1)"
    );
    const fullCartData = expectStatus(
      await session.post("/api/cart/items", { sku_id: sku2.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (shop 2)"
    );
    assert.equal(fullCartData.cart.shop_groups.length, 2, "full cart must have 2 shop groups");
    const [shop1Group, shop2Group] = fullCartData.cart.shop_groups;

    // GET /api/cart?shop_id=<shop1> filters read view without mutating Redis cart.
    const filteredData = expectStatus(
      await session.get(`/api/cart?shop_id=${encodeURIComponent(shop1Group.shop_id)}`),
      200,
      "GET /api/cart?shop_id=<shop1>"
    );
    assert.equal(filteredData.cart.shop_groups.length, 1, "filtered cart must return only 1 shop group");
    assert.equal(filteredData.cart.shop_groups[0].shop_id, shop1Group.shop_id);
    assert.equal(filteredData.cart.total_items, 1);

    // Unfiltered cart still contains both shops.
    const stillFull = expectStatus(await session.get("/api/cart"), 200, "GET /api/cart after filtered read");
    assert.equal(stillFull.cart.shop_groups.length, 2, "unfiltered cart must remain untouched");

    // Clear shop2 only via DELETE /api/cart?shop_id=<shop2> so checkout proceeds with shop1.
    expectStatus(
      await session.del(`/api/cart?shop_id=${encodeURIComponent(shop2Group.shop_id)}`),
      200,
      "DELETE /api/cart?shop_id=<shop2>"
    );
    const afterShopClear = expectStatus(await session.get("/api/cart"), 200, "GET /api/cart after shop2 clear");
    assert.equal(afterShopClear.cart.shop_groups.length, 1, "only shop1 should remain in cart");
    assert.equal(afterShopClear.cart.shop_groups[0].shop_id, shop1Group.shop_id);

    const { shippingChoices } = await getShippingChoicesForCart(
      session,
      addr.address,
      afterShopClear.cart.shop_groups,
      true
    );

    // Attempt to pass sales_channel="STOREFRONT" and storefront_shop_id in BFF checkout body.
    // Phase 1 BFF MUST ignore client-supplied channel and force MARKETPLACE.
    const checkout = expectStatus(
      await session.post(
        "/api/checkout",
        {
          address_id: addr.address.id,
          from_cart: true,
          payment_method: "COD",
          shipping_choices: shippingChoices,
          sales_channel: "STOREFRONT",
          storefront_shop_id: shop1Group.shop_id,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("bff-channel-lock") } }
      ),
      201,
      "POST /api/checkout with client-supplied STOREFRONT"
    );

    assert.equal(checkout.sales_channel, "MARKETPLACE", "BFF checkout response must force MARKETPLACE");
    assert.equal(checkout.storefront_shop_id || "", "", "BFF checkout response must clear storefront_shop_id");
    assert.equal(checkout.orders.length, 1);
    const created = checkout.orders[0];
    assert.equal(created.sales_channel, "MARKETPLACE", "created order must be MARKETPLACE");
    assert.equal(created.storefront_shop_id || "", "", "created order storefront_shop_id must be empty");

    // Verify buyer GET /api/orders/[id] & GET /api/orders?sales_channel=...
    const detail = expectStatus(
      await session.get(`/api/orders/${encodeURIComponent(created.id)}`),
      200,
      "GET /api/orders/[id]"
    );
    assert.equal(detail.order.sales_channel, "MARKETPLACE");
    assert.equal(detail.order.salesChannel, "MARKETPLACE");
    assert.equal(detail.order.storefront_shop_id || "", "");
    assert.equal(detail.order.storefrontShopId || "", "");

    const mktOrders = expectStatus(
      await session.get("/api/orders?sales_channel=MARKETPLACE&page=1&page_size=20"),
      200,
      "GET /api/orders?sales_channel=MARKETPLACE"
    );
    assert.ok(
      (mktOrders.items ?? []).some((o) => o.id === created.id),
      "MARKETPLACE filter must include BFF-created order"
    );

    const sfOrders = expectStatus(
      await session.get("/api/orders?sales_channel=STOREFRONT&page=1&page_size=20"),
      200,
      "GET /api/orders?sales_channel=STOREFRONT"
    );
    assert.equal(
      (sfOrders.items ?? []).some((o) => o.id === created.id),
      false,
      "STOREFRONT filter must NOT include BFF-created MARKETPLACE order"
    );

    const badFilter = await session.get("/api/orders?sales_channel=INVALID_CHANNEL");
    expectStatus(badFilter, 400, "GET /api/orders?sales_channel=INVALID_CHANNEL");
    assert.equal(badFilter.data?.error?.code, "INVALID_SALES_CHANNEL");
  });

  it("(b) Seller orders and Admin ledger/reconciliation channel filter endpoints & SSR pages work via BFF", async () => {
    // Seller orders channel filter & SSR page.
    const seller = await loginAccount(
      demoCredentials.seller.email,
      demoCredentials.seller.password,
      "seller-channel"
    );
    try {
      const sellerOrders = expectStatus(
        await seller.session.get("/api/seller/orders?page=1&page_size=20"),
        200,
        "GET /api/seller/orders"
      );
      for (const ord of sellerOrders.items ?? []) {
        assert.ok(
          ord.sales_channel === "MARKETPLACE" || ord.sales_channel === "STOREFRONT",
          `seller order ${ord.id} missing valid sales_channel: ${ord.sales_channel}`
        );
      }

      const sellerMkt = expectStatus(
        await seller.session.get("/api/seller/orders?sales_channel=MARKETPLACE&page=1&page_size=20"),
        200,
        "GET /api/seller/orders?sales_channel=MARKETPLACE"
      );
      for (const ord of sellerMkt.items ?? []) {
        assert.equal(ord.sales_channel, "MARKETPLACE");
      }

      const sellerSf = expectStatus(
        await seller.session.get("/api/seller/orders?sales_channel=STOREFRONT&page=1&page_size=20"),
        200,
        "GET /api/seller/orders?sales_channel=STOREFRONT"
      );
      for (const ord of sellerSf.items ?? []) {
        assert.equal(ord.sales_channel, "STOREFRONT");
      }

      const sellerBad = await seller.session.get("/api/seller/orders?sales_channel=BOGUS");
      expectStatus(sellerBad, 400, "GET /api/seller/orders?sales_channel=BOGUS");
      assert.equal(sellerBad.data?.error?.code, "INVALID_SALES_CHANNEL");

      const sellerPage = await seller.session.get("/seller/don-hang", { headers: HTML_HEADERS });
      expectStatus(sellerPage, 200, "GET /seller/don-hang");
    } finally {
      await seller.session.post("/api/auth/logout", {});
    }

    // Admin orders, ledger summary, reconciliation channel filters & SSR page.
    const admin = await loginAccount(
      demoCredentials.admin.email,
      demoCredentials.admin.password,
      "admin-channel"
    );
    try {
      const adminMktOrders = expectStatus(
        await admin.session.get("/api/admin/orders?sales_channel=MARKETPLACE&page=1&page_size=20"),
        200,
        "GET /api/admin/orders?sales_channel=MARKETPLACE"
      );
      for (const ord of adminMktOrders.items ?? []) {
        assert.equal(ord.sales_channel, "MARKETPLACE");
      }

      const adminSfOrders = expectStatus(
        await admin.session.get("/api/admin/orders?sales_channel=STOREFRONT&page=1&page_size=20"),
        200,
        "GET /api/admin/orders?sales_channel=STOREFRONT"
      );
      for (const ord of adminSfOrders.items ?? []) {
        assert.equal(ord.sales_channel, "STOREFRONT");
      }

      const adminBadOrders = await admin.session.get("/api/admin/orders?sales_channel=BOGUS");
      expectStatus(adminBadOrders, 400, "GET /api/admin/orders?sales_channel=BOGUS");
      assert.equal(adminBadOrders.data?.error?.code, "INVALID_SALES_CHANNEL");

      // Admin ledger summary channel filter: ALL == MARKETPLACE + STOREFRONT, all balanced.
      const ledgerAll = expectStatus(
        await admin.session.get("/api/admin/ledger"),
        200,
        "GET /api/admin/ledger"
      );
      const ledgerMkt = expectStatus(
        await admin.session.get("/api/admin/ledger?sales_channel=MARKETPLACE"),
        200,
        "GET /api/admin/ledger?sales_channel=MARKETPLACE"
      );
      const ledgerSf = expectStatus(
        await admin.session.get("/api/admin/ledger?sales_channel=STOREFRONT"),
        200,
        "GET /api/admin/ledger?sales_channel=STOREFRONT"
      );

      for (const [label, l] of [
        ["ALL", ledgerAll],
        ["MARKETPLACE", ledgerMkt],
        ["STOREFRONT", ledgerSf],
      ]) {
        assert.equal(l.is_balanced, true, `admin ledger [${label}] must be balanced`);
        assert.equal(
          l.total_debits_vnd,
          l.total_credits_vnd,
          `admin ledger [${label}] debits must equal credits`
        );
      }
      assert.equal(
        ledgerMkt.total_debits_vnd + ledgerSf.total_debits_vnd,
        ledgerAll.total_debits_vnd,
        "admin ledger MARKETPLACE + STOREFRONT debits must equal unfiltered total"
      );
      assert.equal(
        ledgerMkt.total_credits_vnd + ledgerSf.total_credits_vnd,
        ledgerAll.total_credits_vnd,
        "admin ledger MARKETPLACE + STOREFRONT credits must equal unfiltered total"
      );

      const badLedger = await admin.session.get("/api/admin/ledger?sales_channel=BOGUS");
      expectStatus(badLedger, 400, "GET /api/admin/ledger?sales_channel=BOGUS");
      assert.equal(badLedger.data?.error?.code, "INVALID_SALES_CHANNEL");

      // Admin reconciliation channel filter: ALL == MARKETPLACE + STOREFRONT.
      const today = new Date().toISOString().slice(0, 10);
      const recAll = expectStatus(
        await admin.session.post("/api/admin/reconcile", { date: today }),
        200,
        "POST /api/admin/reconcile (ALL)"
      );
      const recMkt = expectStatus(
        await admin.session.post("/api/admin/reconcile", { date: today, sales_channel: "MARKETPLACE" }),
        200,
        "POST /api/admin/reconcile (MARKETPLACE)"
      );
      const recSf = expectStatus(
        await admin.session.post("/api/admin/reconcile", { date: today, sales_channel: "STOREFRONT" }),
        200,
        "POST /api/admin/reconcile (STOREFRONT)"
      );

      assert.equal(
        (recMkt.items?.length ?? 0) + (recSf.items?.length ?? 0),
        recAll.items?.length ?? 0,
        "reconciliation item counts across MARKETPLACE + STOREFRONT must equal ALL"
      );
      assert.equal(
        recMkt.matched_count + recSf.matched_count,
        recAll.matched_count,
        "reconciliation matched_count across MARKETPLACE + STOREFRONT must equal ALL"
      );

      const badRec = await admin.session.post("/api/admin/reconcile", {
        date: today,
        sales_channel: "BOGUS",
      });
      expectStatus(badRec, 400, "POST /api/admin/reconcile (BOGUS)");
      assert.equal(badRec.data?.error?.code, "INVALID_SALES_CHANNEL");

      const recPage = await admin.session.get("/admin/doi-soat", { headers: HTML_HEADERS });
      expectStatus(recPage, 200, "GET /admin/doi-soat");
      assert.ok(
        recPage.text.includes("Sàn NexusMart") && recPage.text.includes("Site riêng"),
        "/admin/doi-soat must render channel filter labels"
      );
    } finally {
      await admin.session.post("/api/auth/logout", {});
    }
  });

  it("(c) B5 proxy header hardening: spoofed X-Forwarded-Host / X-Forwarded-Proto cannot bypass CSRF or hijack redirects", async () => {
    // 1. CSRF check ignores spoofed X-Forwarded-Host matching evil Origin.
    const evilOrigin = "https://evil.example";
    const csrfSpoof = await buyer.session.post(
      "/api/cart/items",
      { sku_id: sku1.skuId, quantity: 1 },
      {
        origin: evilOrigin,
        headers: {
          "x-forwarded-host": "evil.example",
          "x-forwarded-proto": "https",
        },
      }
    );
    expectStatus(csrfSpoof, 403, "CSRF with spoofed X-Forwarded-Host/Proto");
    assert.equal(csrfSpoof.data?.error?.code, "BAD_ORIGIN");
    assertNoInternalErrorLeak(csrfSpoof.text, "CSRF spoofed proxy headers");

    // 2. Middleware redirect for unauthenticated protected route ignores spoofed X-Forwarded-Host.
    const anon = new WebSession("proxy-spoof");
    const redir = await anon.get("/don-hang", {
      headers: {
        ...HTML_HEADERS,
        "x-forwarded-host": "evil.example",
        "x-forwarded-proto": "https",
      },
    });
    expectStatus(redir, [302, 303, 307, 308], "Unauth redirect with spoofed X-Forwarded-Host");
    const loc = redir.headers.get("location") ?? "";
    assert.equal(
      loc.includes("evil.example"),
      false,
      `login redirect must ignore spoofed X-Forwarded-Host, got Location: ${loc}`
    );
  });
});
