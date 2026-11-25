import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
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
  pickPlatformVoucher,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);

describe("16. Wave 3a storefront security & correctness (D1, D6, D8/A3, D11, P2-3, P2-6, P3)", () => {
  let siteA;
  let siteB;
  let skuA;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    siteB = await resolveSite(SEEDED.saiGon.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
    sellerRoot = new Browser("w3a-seller").at(ROOT_HOST);
    adminRoot = new Browser("w3a-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "w3a-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "w3a-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("D8/A3: Flash sale filters hidden/deleted products on site /flash-sale & gateway sku_ids >200 returns 400", async () => {
    const anonA = new Browser("w3a-d8").at(SITE_A_HOST);
    const overLimitIds = Array.from({ length: 201 }, (_, i) => `00000000-0000-4000-8000-${String(i).padStart(12, "0")}`).join(",");
    const badBatch = await gw("GET", `/api/v1/products?sku_ids=${encodeURIComponent(overLimitIds)}`);
    assert.equal(badBatch.status, 400, `sku_ids > 200 must return 400, got ${badBatch.status}`);

    let productId = null;
    let flashSaleId = null;
    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `W3A Hidden Flash ${uid()}`,
        priceVnd: 280_000,
        stock: 10,
      });
      productId = product.id;

      const now = Date.now();
      const fsCreate = expectStatus(
        await adminRoot.post("/api/admin/flash-sales", {
          name: `W3A D8 Flash ${uid()}`,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
        }),
        [200, 201],
        "create D8 flash sale"
      );
      flashSaleId = fsCreate.flash_sale?.id ?? fsCreate.id;

      const itemAdd = expectStatus(
        await sellerRoot.post(`/api/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`, {
          sku_id: sku.id,
          flash_price_vnd: 190_000,
          total_slots: 5,
          per_user_limit: 1,
        }),
        [200, 201],
        "add D8 flash item"
      );
      const itemId = itemAdd.item?.id ?? itemAdd.flash_sale_item?.id ?? itemAdd.id;
      expectStatus(
        await adminRoot.post(`/api/admin/flash-sale-items/${encodeURIComponent(itemId)}/approve`, {
          approve: true,
          reason: "E2E D8 approved",
        }),
        200,
        "approve D8 flash item"
      );

      // Before hiding: visible on site /flash-sale.
      const beforePage = await anonA.page("/flash-sale");
      expectStatus(beforePage, 200, "site /flash-sale before hide");
      assert.ok(beforePage.text.includes(product.name), "approved flash item must appear on /flash-sale");

      // Delete/hide product -> must disappear from site /flash-sale.
      expectStatus(
        await sellerRoot.del(`/api/seller/products/${encodeURIComponent(productId)}`),
        [200, 204],
        "delete product to hide from flash sale"
      );
      productId = null;

      const afterPage = await anonA.page("/flash-sale");
      expectStatus(afterPage, 200, "site /flash-sale after delete");
      assert.equal(afterPage.text.includes(product.name), false, "deleted product must not appear on /flash-sale");
    } finally {
      if (flashSaleId) {
        await adminRoot.post(`/api/admin/flash-sales/${encodeURIComponent(flashSaleId)}/end`, {}).catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
    }
  });

  it("D1: commission_rate_bps stripped from public catalog/search/site SSR; /api/admin/categories enforces RBAC", async () => {
    const anonA = new Browser("w3a-d1-a").at(SITE_A_HOST);
    const anonRoot = new Browser("w3a-d1-root").at(ROOT_HOST);
    const buyerRoot = new Browser("w3a-d1-buyer").at(ROOT_HOST);

    // Public gateway categories & products + site search & SSR must NEVER expose commission_rate_bps.
    const pubCats = await gw("GET", "/api/v1/categories");
    assert.equal(pubCats.status, 200);
    assert.equal(hasObjectKeyDeep(pubCats.data, "commission_rate_bps"), false, "public /categories leaked commission_rate_bps");

    const pubProds = await gw("GET", `/api/v1/products?shop_id=${encodeURIComponent(siteA.shopId)}&page=1&page_size=20`);
    assert.equal(pubProds.status, 200);
    assert.equal(hasObjectKeyDeep(pubProds.data, "commission_rate_bps"), false, "public /products leaked commission_rate_bps");

    const siteSearch = expectStatus(await anonA.get("/api/search?q=ao"), 200, "site /api/search");
    assert.equal(hasObjectKeyDeep(siteSearch, "commission_rate_bps"), false, "site /api/search leaked commission_rate_bps");

    const sitePdp = await anonA.page(`/san-pham/${SEEDED.bachDang.productSlug}`);
    expectStatus(sitePdp, 200, "site PDP SSR");
    assert.equal(sitePdp.text.includes("commission_rate_bps"), false, "site PDP SSR leaked commission_rate_bps");

    // Admin categories RBAC: anon 401, buyer 403, admin 200 with numeric commission_rate_bps.
    expectStatus(await anonRoot.get("/api/admin/categories"), 401, "anon GET /api/admin/categories");
    await registerBuyer(buyerRoot, "w3a-d1-b");
    try {
      expectStatus(await buyerRoot.get("/api/admin/categories"), 403, "buyer GET /api/admin/categories");
    } finally {
      await buyerRoot.post("/api/auth/logout", {}).catch(() => {});
    }

    const adminCats = expectStatus(await adminRoot.get("/api/admin/categories"), 200, "admin GET /api/admin/categories");
    assert.ok(
      (adminCats.categories ?? []).some((c) => typeof c.commission_rate_bps === "number"),
      "admin categories must expose commission_rate_bps"
    );
  });

  it("P2-3 & P3: SHOP voucher active/paused toggle, platform voucher rejection on site, and site-scoped active shop discounts", async () => {
    const buyerA = new Browser("w3a-p23-a").at(SITE_A_HOST);
    const anonB = new Browser("w3a-p3-b").at(SITE_B_HOST);
    let voucherId = null;
    let productId = null;
    let programId = null;

    try {
      const code = `W3A${uid().slice(-6).toUpperCase()}`;
      const now = Date.now();
      const vCreate = expectStatus(
        await sellerRoot.post("/api/seller/vouchers", {
          code,
          name: `Voucher P2-3 ${code}`,
          discount_type: "FIXED",
          value_vnd: 20_000,
          min_order_vnd: 100_000,
          quota: 20,
          per_user_limit: 2,
          budget_vnd: 400_000,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
        }),
        [200, 201],
        "POST /api/seller/vouchers"
      );
      voucherId = vCreate.voucher?.id ?? vCreate.id;
      assert.ok(voucherId, "created shop voucher id required");

      const listActive = expectStatus(await buyerA.get("/api/vouchers"), 200, "GET site /api/vouchers (active)");
      assert.ok((listActive.vouchers ?? []).some((v) => v.id === voucherId), "active SHOP voucher must appear on site");

      // Pause voucher -> absent from active site list.
      expectStatus(
        await sellerRoot.patch(`/api/seller/vouchers/${encodeURIComponent(voucherId)}/status`, {
          status: "PAUSED",
        }),
        200,
        "pause shop voucher"
      );
      const listPaused = expectStatus(await buyerA.get("/api/vouchers"), 200, "GET site /api/vouchers (paused)");
      assert.equal(
        (listPaused.vouchers ?? []).some((v) => v.id === voucherId && v.is_active),
        false,
        "paused SHOP voucher must not appear as active on site"
      );

      // Platform voucher rejected on site checkout with 400 PLATFORM_PROMO_NOT_ALLOWED.
      const platVoucher = await pickPlatformVoucher("NEXUS30");
      await registerBuyer(buyerA, "w3a-p23", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "w3a-p23");
      if (platVoucher) {
        const prevPlat = await buyerA.post("/api/vouchers/preview", {
          platform_voucher_id: platVoucher.id,
        });
        expectStatus(prevPlat, [400, 404], "site /api/vouchers/preview must reject platform_voucher_id");
        if (prevPlat.status === 400) {
          assert.equal(getBffError(prevPlat).code, "PLATFORM_PROMO_NOT_ALLOWED");
        }
      }

      // P3: Active shop discount enrichment scoped strictly to site A (never leaks onto site B).
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `W3A P3 Discount ${uid()}`,
        priceVnd: 260_000,
        stock: 10,
      });
      productId = product.id;
      const progCreate = expectStatus(
        await sellerRoot.post("/api/seller/discount-programs", {
          name: `P3 Scope ${uid()}`,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
          items: [{ sku_id: sku.id, discounted_price_vnd: 220_000, per_user_limit: 2 }],
        }),
        [200, 201],
        "create P3 shop discount"
      );
      programId = progCreate.program?.id ?? progCreate.id;

      const discA = expectStatus(await buyerA.get("/api/shop-discounts/active"), 200, "site A active discounts");
      assert.ok((discA.discounts ?? []).some((d) => d.sku_id === sku.id), "site A must see its own active discount");
      const discB = expectStatus(await anonB.get("/api/shop-discounts/active"), 200, "site B active discounts");
      assert.equal(
        (discB.discounts ?? []).some((d) => d.sku_id === sku.id),
        false,
        "site B must NEVER see site A's active shop discount"
      );
      void addr;
    } finally {
      if (programId) {
        await sellerRoot.post(`/api/seller/discount-programs/${encodeURIComponent(programId)}/end`, {}).catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("D6: Admin shop review expected_owner_name 409 OWNER_NAME_CHANGED & bank review bank_account_id guards", async () => {
    const buyerRoot = new Browser("w3a-d6-owner").at(ROOT_HOST);
    try {
      const buyer = await registerBuyer(buyerRoot, "w3a-d6");
      const locs = expectStatus(await buyerRoot.get("/api/shops/locations"), 200, "GET /api/shops/locations");
      const prov = locs.provinces[0];
      const wards = expectStatus(
        await buyerRoot.get(`/api/shops/locations?province_code=${encodeURIComponent(prov.code)}`),
        200,
        "GET /api/shops/locations?province_code"
      );
      const addrRes = expectStatus(
        await buyerRoot.post("/api/auth/addresses", {
          recipientName: buyer.creds.fullName,
          recipientPhone: buyer.creds.phone,
          provinceCode: prov.code,
          provinceName: prov.name,
          wardCode: wards.wards[0].code,
          wardName: wards.wards[0].name,
          streetAddress: "66 Đường D6 Guard",
          isDefault: true,
        }),
        [200, 201],
        "create pickup address D6"
      );

      const shopReg = expectStatus(
        await buyerRoot.post("/api/shops/register", {
          name: `ShopD6-${uid()}`,
          description: "Kiểm thử D6 expected_owner_name",
          pickup_address_id: addrRes.address.id,
          contact_phone: buyer.creds.phone,
          business_license_doc: "https://example.com/d6-license.pdf",
        }),
        201,
        "register shop D6"
      );
      const shopId = shopReg.shop.id;

      // Owner renames profile before admin approves.
      const newOwnerName = `Tên Mới D6 ${uid().slice(-4)}`;
      expectStatus(
        await buyerRoot.patch("/api/auth/me", { fullName: newOwnerName, phone: buyer.creds.phone }),
        200,
        "owner PATCH /api/auth/me"
      );

      // Stale expected_owner_name -> 409 OWNER_NAME_CHANGED.
      const staleApprove = await adminRoot.post(`/api/admin/shops/${encodeURIComponent(shopId)}/review`, {
        decision: "APPROVE",
        reason: "Approve with stale name",
        expected_owner_name: buyer.creds.fullName,
      });
      expectStatus(staleApprove, 409, "stale expected_owner_name must return 409");
      assert.equal(getBffError(staleApprove).code, "OWNER_NAME_CHANGED");

      // Fresh expected_owner_name -> 200 ACTIVE.
      const freshApprove = expectStatus(
        await adminRoot.post(`/api/admin/shops/${encodeURIComponent(shopId)}/review`, {
          decision: "APPROVE",
          reason: "Approve with fresh owner name",
          expected_owner_name: newOwnerName,
        }),
        200,
        "fresh expected_owner_name approve"
      );
      assert.equal(normalizeStatus(freshApprove.shop?.status), "ACTIVE");

      // Bank review without bank_account_id -> 400; with non-pending bogus bank_account_id -> 404/409.
      const missingBankId = await adminRoot.post(`/api/admin/shops/${encodeURIComponent(shopId)}/bank-account/review`, {
        decision: "APPROVE",
        reason: "missing id",
      });
      expectStatus(missingBankId, 400, "bank review missing bank_account_id must return 400");

      const bogusBankId = await adminRoot.post(`/api/admin/shops/${encodeURIComponent(shopId)}/bank-account/review`, {
        bank_account_id: "00000000-0000-4000-8000-000000000099",
        decision: "APPROVE",
        reason: "stale id",
      });
      expectStatus(bogusBankId, [404, 409], "bank review bogus bank_account_id must return 404/409");
    } finally {
      await buyerRoot.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("D11 & P2-6: Pre-persist validation retry reuses Idempotency-Key; payload mismatch -> 409 IDEMPOTENCY_KEY_CONFLICT; cancelled order replay safe", async () => {
    const buyerA = new Browser("w3a-d11").at(SITE_A_HOST);
    let orderId = null;

    try {
      await registerBuyer(buyerA, "w3a-d11", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "w3a-d11");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: skuA.price, total_weight_grams: 300 }],
        true
      );

      const idemKey = newIdempotencyKey("w3a-d11");

      // 1. Pre-persist failure (missing address_id) does NOT poison the Idempotency-Key.
      const badReq = await buyerA.post(
        "/api/checkout",
        {
          address_id: "",
          from_cart: false,
          payment_method: "COD",
          items: [{ sku_id: skuA.skuId, quantity: 1 }],
          shipping_choices: shippingChoices,
        },
        { headers: { "Idempotency-Key": idemKey } }
      );
      expectStatus(badReq, 400, "pre-persist missing address_id must return 400");

      // 2. Retrying with the SAME Idempotency-Key and valid payload succeeds (201).
      const validPayload = {
        address_id: addr.id,
        from_cart: false,
        payment_method: "COD",
        items: [{ sku_id: skuA.skuId, quantity: 1 }],
        shipping_choices: shippingChoices,
      };
      const ok1 = expectStatus(
        await buyerA.post("/api/checkout", validPayload, { headers: { "Idempotency-Key": idemKey } }),
        201,
        "valid retry with same Idempotency-Key after 400 must return 201"
      );
      orderId = ok1.orders[0].id;

      // 3. Reusing completed key with DIFFERENT payload (qty=2) -> 409 IDEMPOTENCY_KEY_CONFLICT.
      const conflict = await buyerA.post(
        "/api/checkout",
        { ...validPayload, items: [{ sku_id: skuA.skuId, quantity: 2 }] },
        { headers: { "Idempotency-Key": idemKey } }
      );
      expectStatus(conflict, 409, "reused Idempotency-Key with altered payload must return 409");
      assert.equal(getBffError(conflict).code, "IDEMPOTENCY_KEY_CONFLICT");

      // 4. Cancel order, then replay exact original payload + key -> 200 idempotent_replay=true returning same order (no duplicate).
      expectStatus(
        await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/cancel`, { reason: "P2-6 cancel replay check" }),
        200,
        "cancel order before replay"
      );
      const replayAfterCancel = expectStatus(
        await buyerA.post("/api/checkout", validPayload, { headers: { "Idempotency-Key": idemKey } }),
        200,
        "replay same key after cancel must return 200 idempotent_replay=true"
      );
      assert.equal(replayAfterCancel.idempotent_replay, true);
      assert.equal(replayAfterCancel.orders[0].id, orderId);
      assert.equal(orderBackendStatus(replayAfterCancel.orders[0]), "CANCELLED");
      orderId = null;
    } finally {
      await cancelOrderBestEffort(buyerA, orderId, "w3a-d11 cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
