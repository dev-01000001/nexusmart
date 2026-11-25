import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, normalizeStatus, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  cancelOrderBestEffort,
  checkout,
  createApprovedProduct,
  createShippingAddress,
  deleteProductBestEffort,
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
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);

describe("13. Audit fixes (5a–5j) migrated to site host & root host", () => {
  let siteA;
  let siteB;
  let skuA;
  let skuB;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    siteB = await resolveSite(SEEDED.saiGon.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
    skuB = await pickSku(siteB.shopId, { minStock: 5, maxPriceVnd: 900_000 });
    sellerRoot = new Browser("audit-seller").at(ROOT_HOST);
    adminRoot = new Browser("audit-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "audit-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "audit-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("5a & 5b: Site /flash-sale and PDP copy hygiene; root /flash-sale returns 404", async () => {
    const anonA = new Browser("audit-5ab-a").at(SITE_A_HOST);
    const anonRoot = new Browser("audit-5ab-root").at(ROOT_HOST);

    assert.equal((await anonRoot.page("/flash-sale")).status, 404, "root /flash-sale must return 404");

    const fsPage = await anonA.page("/flash-sale");
    expectStatus(fsPage, 200, `GET ${SITE_A_HOST}/flash-sale`);
    assert.equal(fsPage.text.includes("Deal sốc"), false, "site /flash-sale must not render fake 'Deal sốc'");
    assertNoInternalErrorLeak(fsPage.text, "site /flash-sale");

    const pdp = await anonA.page(`/san-pham/${SEEDED.bachDang.productSlug}`);
    expectStatus(pdp, 200, `GET ${SITE_A_HOST}/san-pham/${SEEDED.bachDang.productSlug}`);
    for (const bannedCopy of [
      "Mua 2 giảm",
      "Quà 499k",
      "Chọn size combo",
      "Gợi ý mua kèm",
      "Deal sốc",
    ]) {
      assert.equal(pdp.text.includes(bannedCopy), false, `site PDP leaked legacy promo copy: ${bannedCopy}`);
    }
  });

  it("5c: Login returns canonical user roles on root/site and enforces host-consistent auth redirects", async () => {
    const sBrowser = new Browser("audit-5c-s").at(ROOT_HOST);
    const sLogin = await loginOn(sBrowser, demoCredentials.seller.email, demoCredentials.seller.password, "5c-seller");
    assert.equal(sLogin.user.role, "seller");
    expectStatus(await sBrowser.page("/seller"), 200, "seller GET /seller");
    await sBrowser.post("/api/auth/logout", {}).catch(() => {});

    const aBrowser = new Browser("audit-5c-a").at(ROOT_HOST);
    const aLogin = await loginOn(aBrowser, demoCredentials.admin.email, demoCredentials.admin.password, "5c-admin");
    assert.ok(["admin", "super_admin"].includes(aLogin.user.role));
    expectStatus(await aBrowser.page("/admin"), 200, "admin GET /admin");
    await aBrowser.post("/api/auth/logout", {}).catch(() => {});
  });

  it("5d: Flash sale on site host renders approved slot & supports VietQR checkout with flash_sale_item_id", async () => {
    const buyerA = new Browser("audit-5d-buyer").at(SITE_A_HOST);
    let flashSaleId = null;
    let productId = null;
    let orderId = null;

    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `Audit 5d Flash ${uid()}`,
        priceVnd: 300_000,
        stock: 15,
      });
      productId = product.id;

      const now = Date.now();
      const fsCreate = expectStatus(
        await adminRoot.post("/api/admin/flash-sales", {
          name: `E2E 5d Flash ${uid()}`,
          starts_at: new Date(now - 60_000).toISOString(),
          ends_at: new Date(now + 2 * 3600_000).toISOString(),
        }),
        [200, 201],
        "POST /api/admin/flash-sales"
      );
      flashSaleId = fsCreate.flash_sale?.id ?? fsCreate.id;
      assert.ok(flashSaleId, "flash sale creation must return id");

      const itemAdd = expectStatus(
        await sellerRoot.post(`/api/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`, {
          sku_id: sku.id,
          flash_price_vnd: 199_000,
          total_slots: 5,
          per_user_limit: 1,
        }),
        [200, 201],
        "POST /api/seller/flash-sales/[id]/items"
      );
      const flashItemId = itemAdd.item?.id ?? itemAdd.flash_sale_item?.id ?? itemAdd.id;
      assert.ok(flashItemId, "flash sale item must return id");

      expectStatus(
        await adminRoot.post(`/api/admin/flash-sale-items/${encodeURIComponent(flashItemId)}/approve`, {
          approve: true,
          reason: "E2E 5d approved",
        }),
        200,
        "POST /api/admin/flash-sale-items/[id]/approve"
      );

      // Site /flash-sale renders 200 with the approved product.
      const fsPage = await buyerA.page("/flash-sale");
      expectStatus(fsPage, 200, `GET ${SITE_A_HOST}/flash-sale with live item`);
      assert.ok(fsPage.text.includes(product.name), "site /flash-sale must display live approved flash item");

      await registerBuyer(buyerA, "audit-5d");
      const addr = await createShippingAddress(buyerA, "audit-5d");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        addr,
        [{ shop_id: siteA.shopId, subtotal_vnd: 199_000, total_weight_grams: 300 }],
        false
      );

      const co = expectStatus(
        await buyerA.post(
          "/api/checkout",
          {
            address_id: addr.id,
            from_cart: false,
            payment_method: "VIETQR",
            items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
            shipping_choices: shippingChoices,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("audit-5d") } }
        ),
        201,
        "flash sale VietQR checkout on site"
      );
      orderId = co.orders[0].id;
      assert.ok(Number(co.orders[0].flash_sale_discount_vnd ?? 0) > 0, "flash_sale_discount_vnd must be > 0");
    } finally {
      await cancelOrderBestEffort(buyerA, orderId, "5d cleanup");
      if (flashSaleId) {
        await adminRoot.post(`/api/admin/flash-sales/${encodeURIComponent(flashSaleId)}/end`, {}).catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("5e, 5g, 5h & 5j: Shipping quote ignores client from_province_code, carrier persists on seller order, VietQR wait copy hygiene, single-shop cancel", async () => {
    const buyerA = new Browser("audit-5eghj").at(SITE_A_HOST);
    let orderId = null;

    try {
      await registerBuyer(buyerA, "audit-5eghj");
      const addr = await createShippingAddress(buyerA, "audit-5eghj");

      // 5e: Omitting vs spoofing from_province_code produces identical shop-derived shipping quote.
      const qClean = expectStatus(
        await buyerA.post("/api/checkout/shipping-quote", {
          to_province_code: addr.province_code,
          to_ward_code: addr.ward_code,
          shops: [{ shop_id: siteA.shopId, weight_grams: 500, cod_amount_vnd: 0 }],
        }),
        200,
        "shipping-quote without from_province_code"
      );
      const qSpoof = expectStatus(
        await buyerA.post("/api/checkout/shipping-quote", {
          to_province_code: addr.province_code,
          to_ward_code: addr.ward_code,
          shops: [
            { shop_id: siteA.shopId, from_province_code: "96", weight_grams: 500, cod_amount_vnd: 0 },
          ],
        }),
        200,
        "shipping-quote with spoofed from_province_code"
      );
      const quotesClean = qClean.quotes_by_shop?.[siteA.shopId] ?? [];
      const quotesSpoof = qSpoof.quotes_by_shop?.[siteA.shopId] ?? [];
      assert.ok(quotesClean.length > 0 && quotesSpoof.length > 0);
      assert.equal(
        quotesClean[0].fee_vnd,
        quotesSpoof[0].fee_vnd,
        "shipping-quote must resolve pickup province from shop record, ignoring client from_province_code"
      );

      // 5g: Explicit carrier mock_express / STANDARD persists on seller order.
      const chosen =
        quotesClean.find((q) => q.carrier_code === "mock_express") ?? quotesClean[0];
      const cart = await fillCart(buyerA, skuA.skuId, 1);
      assert.equal(cart.shop_groups.length, 1);

      const co = expectStatus(
        await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices: [
            {
              shop_id: siteA.shopId,
              carrier_code: chosen.carrier_code,
              service_code: chosen.service_code,
            },
          ],
          paymentMethod: "VIETQR",
          key: newIdempotencyKey("audit-5g"),
        }),
        201,
        "5g VietQR checkout with explicit carrier"
      );
      orderId = co.orders[0].id;
      const paymentId = co.payment_id;

      const sellerOrd = expectStatus(
        await sellerRoot.get(`/api/seller/orders/${encodeURIComponent(orderId)}`),
        200,
        "seller GET order carrier check"
      );
      assert.equal(
        sellerOrd.order?.carrier_code,
        chosen.carrier_code,
        `expected carrier_code=${chosen.carrier_code}, got ${sellerOrd.order?.carrier_code}`
      );

      // 5h: Waiting page SSR must not advise retrying an expired VietQR transfer.
      const waitPage = await buyerA.page(`/thanh-toan/cho/${encodeURIComponent(paymentId)}`);
      expectStatus(waitPage, 200, `GET ${SITE_A_HOST}/thanh-toan/cho/${paymentId}`);
      assert.equal(
        waitPage.text.includes("chuyển khoản lại"),
        false,
        "VietQR wait page must not advise re-transferring on an expired QR"
      );

      // 5j: Single-shop unpaid VietQR cancel transitions order to CANCELLED cleanly; multi-shop cart rejected.
      expectStatus(
        await buyerA.post("/api/cart/items", { sku_id: skuB.skuId, quantity: 1 }),
        [400, 404],
        "5j multi-shop cart item from shop B must be rejected on site A"
      );

      const cancelRes = expectStatus(
        await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/cancel`, {
          reason: "Audit 5j single-shop cancel",
        }),
        200,
        "5j cancel unpaid VietQR order"
      );
      assert.equal(orderBackendStatus(cancelRes.order), "CANCELLED");
      orderId = null;
    } finally {
      await cancelOrderBestEffort(buyerA, orderId, "5eghj cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("5f: Seller can edit PENDING product without breaking status or SSR /seller/san-pham", async () => {
    let productId = null;
    try {
      const cats = expectStatus(await sellerRoot.get("/api/seller/categories"), 200, "GET /api/seller/categories");
      const categoryId = (cats.categories ?? []).find((c) => !c.is_prohibited)?.id;
      assert.ok(categoryId, "expected non-prohibited category");

      const tag = uid();
      const created = expectStatus(
        await sellerRoot.post("/api/seller/products", {
          category_id: categoryId,
          name: `Audit 5f Pending ${tag}`,
          slug: `audit-5f-pending-${tag}`,
          description: "Mô tả ban đầu kiểm thử 5f",
          images: ["/products/ao-thun-cotton-trang.png"],
          skus: [
            {
              sku_code: `A5F-${tag.toUpperCase()}`,
              name: "Trắng / L",
              attributes: { color: "Trắng", size: "L" },
              price_vnd: 220_000,
              stock: 10,
              weight_grams: 300,
              image_url: "/products/ao-thun-cotton-trang.png",
            },
          ],
        }),
        [200, 201],
        "5f create PENDING product"
      );
      const prod = created.product ?? created;
      productId = prod.id;
      assert.equal(normalizeStatus(prod.status), "PENDING");

      const updatedName = `Audit 5f Updated ${tag}`;
      const updRes = expectStatus(
        await sellerRoot.put(`/api/seller/products/${encodeURIComponent(productId)}`, {
          category_id: categoryId,
          name: updatedName,
          description: "Mô tả đã cập nhật khi sản phẩm đang PENDING",
          images: ["/products/ao-thun-cotton-trang.png"],
        }),
        200,
        "5f PUT /api/seller/products/[id] while PENDING"
      );
      const updProd = updRes.product ?? updRes;
      assert.equal(updProd.name, updatedName);
      assert.equal(normalizeStatus(updProd.status), "PENDING");

      const sellerProductsPage = await sellerRoot.page("/seller/san-pham");
      expectStatus(sellerProductsPage, 200, "GET /seller/san-pham");
      assertNoInternalErrorLeak(sellerProductsPage.text, "GET /seller/san-pham");
    } finally {
      await deleteProductBestEffort(sellerRoot, productId);
    }
  });

  it("5i: Shop registration validation, REQUEST_INFO resubmission & approval on root host (with 429 Retry-After handling)", async () => {
    await resetRateLimitKeys();
    const browser = new Browser("audit-5i-buyer");
    const buyerRoot = browser.at(ROOT_HOST);

    try {
      const buyer = await registerBuyer(buyerRoot, "audit-5i");

      // Buyer creates pickup address on root host via /api/auth/addresses + /api/shops/locations.
      const locs = expectStatus(await buyerRoot.get("/api/shops/locations"), 200, "GET /api/shops/locations");
      const prov = (locs.provinces ?? [])[0];
      assert.ok(prov?.code, "expected provinces from /api/shops/locations");
      const wardsData = expectStatus(
        await buyerRoot.get(`/api/shops/locations?province_code=${encodeURIComponent(prov.code)}`),
        200,
        "GET /api/shops/locations?province_code"
      );
      const ward = (wardsData.wards ?? [])[0];
      assert.ok(ward?.code, "expected wards from /api/shops/locations");

      const addrRes = expectStatus(
        await buyerRoot.post("/api/auth/addresses", {
          recipientName: buyer.creds.fullName,
          recipientPhone: buyer.creds.phone,
          provinceCode: prov.code,
          provinceName: prov.name,
          wardCode: ward.code,
          wardName: ward.name,
          streetAddress: "12 Đường Hồ Sơ 5i",
          isDefault: true,
        }),
        [200, 201],
        "POST /api/auth/addresses (5i)"
      );
      const pickupAddressId = addrRes.address?.id;
      assert.ok(pickupAddressId, "5i pickup address id required");

      const shopTag = uid();
      // Empty business_license_doc must be rejected with 400.
      const missingLicense = await buyerRoot.post("/api/shops/register", {
        name: `Shop5i-${shopTag}`,
        description: "Thiếu giấy phép kinh doanh",
        pickup_address_id: pickupAddressId,
        contact_phone: buyer.creds.phone,
        business_license_doc: "",
      });
      expectStatus(missingLicense, 400, "POST /api/shops/register with empty business_license_doc");

      // Valid submission -> 201 PENDING.
      const reg1 = expectStatus(
        await buyerRoot.post("/api/shops/register", {
          name: `Shop5i-${shopTag}`,
          description: "Hồ sơ đăng ký gian hàng lần 1",
          pickup_address_id: pickupAddressId,
          contact_phone: buyer.creds.phone,
          business_license_doc: "https://example.com/gpkd-5i-v1.pdf",
        }),
        201,
        "POST /api/shops/register valid v1"
      );
      const shopId = reg1.shop?.id;
      assert.ok(shopId, "shop registration must return shop.id");
      assert.equal(normalizeStatus(reg1.shop?.status), "PENDING");

      // Admin sends REQUEST_INFO -> shop stays PENDING with non-empty rejection_reason.
      const reqInfo = expectStatus(
        await adminRoot.post(`/api/admin/shops/${encodeURIComponent(shopId)}/review`, {
          decision: "REQUEST_INFO",
          reason: "Vui lòng bổ sung bản chụp giấy phép kinh doanh rõ nét hơn",
        }),
        200,
        "POST /api/admin/shops/[id]/review REQUEST_INFO"
      );
      assert.equal(normalizeStatus(reqInfo.shop?.status), "PENDING");
      assert.ok(String(reqInfo.shop?.rejection_reason || "").includes("giấy phép"));

      const mePending = expectStatus(await buyerRoot.get("/api/shops/me"), 200, "GET /api/shops/me after REQUEST_INFO");
      assert.equal(normalizeStatus(mePending.shop?.status), "PENDING");
      assert.ok(String(mePending.shop?.rejection_reason || "").length > 0);

      // Buyer resubmits updated documentation -> stays PENDING with cleared rejection_reason.
      const resub = expectStatus(
        await buyerRoot.post("/api/shops/register", {
          name: `Shop5i-${shopTag}`,
          description: "Hồ sơ bổ sung lần 2",
          pickup_address_id: pickupAddressId,
          contact_phone: buyer.creds.phone,
          business_license_doc: "https://example.com/gpkd-5i-v2-clear.pdf",
        }),
        [200, 201],
        "POST /api/shops/register resubmit after REQUEST_INFO"
      );
      assert.equal(resub.shop?.id, shopId);
      assert.equal(normalizeStatus(resub.shop?.status), "PENDING");
      assert.equal(String(resub.shop?.rejection_reason ?? "").trim(), "");

      // Admin approves with expected_owner_name -> ACTIVE.
      const approved = expectStatus(
        await adminRoot.post(`/api/admin/shops/${encodeURIComponent(shopId)}/review`, {
          decision: "APPROVE",
          reason: "Hồ sơ bổ sung hợp lệ",
          expected_owner_name: buyer.creds.fullName,
        }),
        200,
        "POST /api/admin/shops/[id]/review APPROVE"
      );
      assert.equal(normalizeStatus(approved.shop?.status), "ACTIVE");
    } finally {
      await buyerRoot.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
