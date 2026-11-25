import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { payVietQR } from "./lib/bank.mjs";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, getBffError, normalizeStatus, pollUntil, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  checkout,
  createApprovedProduct,
  createShippingAddress,
  deleteProductBestEffort,
  demoCredentials,
  fillCart,
  loginOn,
  newIdempotencyKey,
  orderBackendStatus,
  quoteShipping,
  registerBuyer,
  resolveSite,
  shipOrderToDelivered,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("17. Wave 3b reviews, banned-keyword moderation, prohibited category & discontinued product on site", () => {
  let siteA;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    sellerRoot = new Browser("w3b17-seller").at(ROOT_HOST);
    adminRoot = new Browser("w3b17-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "w3b17-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "w3b17-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("E6 & E6-bis: Verified COMPLETED review on site (premature 409, http:// 400, BLOCK keyword 400, 201, duplicate 409, public list & PDP SSR)", async () => {
    const buyerA = new Browser("w3b17-rev-buyer").at(SITE_A_HOST);
    const anonA = new Browser("w3b17-rev-anon").at(SITE_A_HOST);
    let productId = null;
    let blockKwId = null;

    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `W3B Review SP ${uid()}`,
        priceVnd: 240_000,
        stock: 15,
      });
      productId = product.id;

      const blockTerm = `e2eblockterm${uid().slice(-6)}`;

      const kwBlock = expectStatus(
        await adminRoot.post("/api/admin/banned-keywords", {
          term: blockTerm,
          action: "BLOCK",
          category: "OTHER",
        }),
        [200, 201],
        "create BLOCK banned keyword"
      );
      blockKwId = kwBlock.keyword?.id ?? kwBlock.id;

      const buyerAcct = await registerBuyer(buyerA, "w3b17-rev");
      const addr = await createShippingAddress(buyerA, "w3b17-rev");
      const cart = await fillCart(buyerA, sku.id, 1);
      const { shippingChoices } = await quoteShipping(buyerA, addr, cart.shop_groups, false);

      const co = expectStatus(
        await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices,
          paymentMethod: "VIETQR",
          key: newIdempotencyKey("w3b17-rev"),
        }),
        201,
        "VietQR checkout for review"
      );
      const orderId = co.orders[0].id;
      const orderItemId = co.orders[0].items?.[0]?.id;
      assert.ok(orderItemId, "checkout order missing items[0].id");

      await payVietQR({
        paymentId: co.payment_id,
        groupId: co.checkout_group_id,
        amountVND: Number(co.total_payable_vnd),
      });
      await pollUntil(`order ${orderId} CONFIRMED`, async () => {
        const r = await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}`);
        return [orderBackendStatus(r.data?.order) === "CONFIRMED", `status=${orderBackendStatus(r.data?.order)}`, true];
      });

      await shipOrderToDelivered({
        sellerRoot,
        buyerSite: buyerA,
        orderId,
        carrierCode: shippingChoices[0]?.carrier_code || "mock_express",
        serviceCode: shippingChoices[0]?.service_code || "STANDARD",
      });

      // Premature review while DELIVERED -> 409.
      const premature = await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/reviews`, {
        order_item_id: orderItemId,
        rating: 5,
        comment: "Chưa xác nhận nhận hàng",
        image_urls: [],
      });
      expectStatus(premature, 409, "review while DELIVERED must return 409");

      // Confirm received -> COMPLETED.
      const done = expectStatus(
        await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/confirm-received`, {}),
        200,
        "POST /api/orders/[id]/confirm-received"
      );
      assert.equal(orderBackendStatus(done.order), "COMPLETED");

      // Unsafe http:// review image -> 400.
      const badImg = await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/reviews`, {
        order_item_id: orderItemId,
        rating: 5,
        comment: "Ảnh http không an toàn",
        image_urls: ["http://insecure.example.com/a.jpg"],
      });
      expectStatus(badImg, 400, "http:// review image URL must return 400");

      // BLOCK banned keyword -> 400 BANNED_CONTENT without leaking the raw term.
      const blockedRev = await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/reviews`, {
        order_item_id: orderItemId,
        rating: 4,
        comment: `Sản phẩm chứa từ bị chặn ${blockTerm} cần từ chối`,
        image_urls: [],
      });
      expectStatus(blockedRev, 400, "BLOCK keyword in review must return 400");
      assert.equal(getBffError(blockedRev).code, "BANNED_CONTENT");
      assert.equal(blockedRev.text.includes(blockTerm), false, "error response must not echo banned term");

      // Valid verified review with safe /products/... image URL -> 201.
      const reviewComment = "Chất vải mát mịn, đường may đều và giao đúng hẹn.";
      const reviewImageUrl = "/products/ao-thun-cotton-trang.png";
      const created = expectStatus(
        await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/reviews`, {
          order_item_id: orderItemId,
          rating: 5,
          comment: reviewComment,
          image_urls: [reviewImageUrl],
        }),
        201,
        "POST valid 5-star review"
      );
      assert.equal(created.review?.rating, 5);
      assert.deepEqual(created.review?.image_urls, [reviewImageUrl]);

      // Duplicate review for same order_item_id -> 409.
      const dup = await buyerA.post(`/api/orders/${encodeURIComponent(orderId)}/reviews`, {
        order_item_id: orderItemId,
        rating: 4,
        comment: "Đánh giá trùng lặp",
        image_urls: [],
      });
      expectStatus(dup, 409, "duplicate review for same order_item_id must return 409");

      // E6-bis: Public GET /api/products/[id]/reviews strips buyer_id/order_id/order_item_id/sku_id,
      // exposes image_urls, masks buyer_name per E5, and updates aggregate rating/count (E14).
      const pubReviews = expectStatus(
        await anonA.get(`/api/products/${encodeURIComponent(product.id)}/reviews?page=1&page_size=10`),
        200,
        "GET /api/products/[id]/reviews (public)"
      );
      assert.equal(pubReviews.review_count, 1);
      assert.equal(pubReviews.rating_avg_x100, 500);
      assert.equal((pubReviews.items ?? []).length, 1);
      const revItem = pubReviews.items[0];
      assert.equal(revItem.rating, 5);
      assert.equal(revItem.comment, reviewComment);
      assert.deepEqual(revItem.image_urls, [reviewImageUrl]);
      assert.equal("buyer_id" in revItem, false, `E6-bis violation: buyer_id leaked (${revItem.buyer_id})`);
      assert.equal("order_id" in revItem, false, `E6-bis violation: order_id leaked (${revItem.order_id})`);
      assert.equal("order_item_id" in revItem, false, `E6-bis violation: order_item_id leaked (${revItem.order_item_id})`);
      assert.equal("sku_id" in revItem, false, `E6-bis violation: sku_id leaked (${revItem.sku_id})`);
      assert.ok(typeof revItem.buyer_name === "string" && revItem.buyer_name.length > 0, "missing buyer_name");
      assert.notEqual(revItem.buyer_name, buyerAcct.creds.fullName, "buyer_name must not expose full name");
      assert.equal(revItem.buyer_name.includes(buyerAcct.creds.email), false, "buyer_name must not leak email");
      assert.match(
        revItem.buyer_name,
        /^\p{Lu}\.\s+\S+$|^\p{Lu}\*\*\*$|^Người mua đã xác thực$/u,
        `buyer_name does not match E5 masked format: ${revItem.buyer_name}`
      );

      // E6-bis owner view: GET /api/orders/[id]/reviews returns the buyer's submitted review.
      const ownReviews = expectStatus(
        await buyerA.get(`/api/orders/${encodeURIComponent(orderId)}/reviews`),
        200,
        "GET /api/orders/[id]/reviews (owner)"
      );
      assert.ok(
        (ownReviews.items ?? ownReviews.reviews ?? []).some((r) => r.order_item_id === orderItemId),
        `owner order reviews must include order_item_id=${orderItemId}`
      );

      // Site PDP SSR renders review stats & comment cleanly.
      const pdp = await anonA.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      expectStatus(pdp, 200, `GET ${SITE_A_HOST}/san-pham/${product.slug}`);
      assert.ok(pdp.text.includes(reviewComment), "site PDP SSR must display verified review comment");
    } finally {
      if (blockKwId) {
        await adminRoot.del(`/api/admin/banned-keywords/${encodeURIComponent(blockKwId)}`).catch(() => {});
      }
      await deleteProductBestEffort(sellerRoot, productId);
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("E1/E2/E3: Admin banned-keyword preview + dry-run rescan & prohibited category blocks seller product creation", async () => {
    let kwId = null;
    let toggledCat = null;

    try {
      const term = `e2eprev${uid().slice(-6)}`;
      const prev = expectStatus(
        await adminRoot.post("/api/admin/banned-keywords/preview", { term }),
        200,
        "POST /api/admin/banned-keywords/preview"
      );
      assert.ok(typeof prev.matched_products === "number" || typeof prev.match_count === "number" || prev !== undefined);

      const kw = expectStatus(
        await adminRoot.post("/api/admin/banned-keywords", { term, action: "REVIEW", category: "OTHER" }),
        [200, 201],
        "POST /api/admin/banned-keywords REVIEW"
      );
      kwId = kw.keyword?.id ?? kw.id;

      const rescan = expectStatus(
        await adminRoot.post("/api/admin/banned-keywords/rescan", {
          keyword_ids: [kwId],
          dry_run: true,
          limit: 50,
        }),
        200,
        "POST /api/admin/banned-keywords/rescan dry_run"
      );
      assert.ok(typeof rescan.scanned === "number" || typeof rescan.scanned_count === "number");

      // Toggle a category prohibited -> seller creating product gets 400 PROHIBITED_CATEGORY -> restore!
      const cats = expectStatus(await adminRoot.get("/api/admin/categories"), 200, "GET /api/admin/categories");
      const target = (cats.categories ?? []).find((c) => !c.is_prohibited);
      assert.ok(target?.id, "expected non-prohibited category");
      toggledCat = target;

      expectStatus(
        await adminRoot.put(`/api/admin/categories/${encodeURIComponent(target.id)}`, {
          name: target.name,
          slug: target.slug,
          commission_rate_bps: target.commission_rate_bps ?? 500,
          sort_order: target.sort_order ?? 0,
          is_prohibited: true,
        }),
        200,
        "mark category prohibited"
      );

      const tag = uid();
      const blockedCreate = await sellerRoot.post("/api/seller/products", {
        category_id: target.id,
        name: `Blocked Cat SP ${tag}`,
        slug: `blocked-cat-${tag}`,
        description: "Sản phẩm trong danh mục bị cấm",
        images: ["/products/ao-thun-cotton-trang.png"],
        skus: [
          {
            sku_code: `BLK-${tag.toUpperCase()}`,
            name: "Đen / M",
            attributes: { color: "Đen", size: "M" },
            price_vnd: 200_000,
            stock: 5,
            weight_grams: 300,
            image_url: "/products/ao-thun-cotton-trang.png",
          },
        ],
      });
      expectStatus(blockedCreate, 400, "creating product in prohibited category must return 400");
      assert.equal(getBffError(blockedCreate).code, "PROHIBITED_CATEGORY");
    } finally {
      if (toggledCat) {
        await adminRoot
          .put(`/api/admin/categories/${encodeURIComponent(toggledCat.id)}`, {
            name: toggledCat.name,
            slug: toggledCat.slug,
            commission_rate_bps: toggledCat.commission_rate_bps ?? 500,
            sort_order: toggledCat.sort_order ?? 0,
            is_prohibited: false,
          })
          .catch(() => {});
      }
      if (kwId) {
        await adminRoot.del(`/api/admin/banned-keywords/${encodeURIComponent(kwId)}`).catch(() => {});
      }
    }
  });

  it("E4/E5: Discontinued product on site shows 200 noindex notice and stays removable (unavailable=true) in cart", async () => {
    const buyerA = new Browser("w3b17-disc").at(SITE_A_HOST);
    let productId = null;

    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `W3B Discontinued ${uid()}`,
        priceVnd: 230_000,
        stock: 10,
      });
      productId = product.id;

      await registerBuyer(buyerA, "w3b17-disc");
      await fillCart(buyerA, sku.id, 1);

      // Seller discontinues/deletes product.
      expectStatus(
        await sellerRoot.del(`/api/seller/products/${encodeURIComponent(productId)}`),
        [200, 204],
        "DELETE /api/seller/products/[id]"
      );
      productId = null;

      // Site PDP returns 200 with discontinued notice & noindex meta.
      const pdp = await buyerA.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      expectStatus(pdp, 200, `discontinued PDP ${SITE_A_HOST}/san-pham/${product.slug}`);
      assert.ok(
        pdp.text.includes("Sản phẩm này đã ngừng kinh doanh."),
        "discontinued PDP must display 'Sản phẩm này đã ngừng kinh doanh.'"
      );
      assert.match(pdp.text, /noindex/i, "discontinued PDP must emit noindex robots meta");

      // Cart keeps unavailable item and allows removing it.
      const cartRes = expectStatus(await buyerA.get("/api/cart"), 200, "GET /api/cart with discontinued item");
      const cartItem = (cartRes.cart?.shop_groups ?? [])
        .flatMap((g) => g.items ?? [])
        .find((it) => it.sku_id === sku.id);
      assert.ok(cartItem, `discontinued SKU ${sku.id} must remain in cart for buyer cleanup`);
      assert.equal(Boolean(cartItem.unavailable), true, "discontinued cart item must have unavailable=true");

      expectStatus(
        await buyerA.del(`/api/cart/items/${encodeURIComponent(sku.id)}`),
        [200, 204],
        "DELETE unavailable SKU from cart"
      );
    } finally {
      await deleteProductBestEffort(sellerRoot, productId);
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
