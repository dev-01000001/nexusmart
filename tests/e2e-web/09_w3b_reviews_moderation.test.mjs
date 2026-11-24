import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  createBuyerAddress,
  ensureRateBudget,
  normalizeStatus,
  pollUntil,
  postSignedPaymentWebhook,
  postSignedShippingWebhook,
  registerAndLoginBuyer,
} from "./helpers.mjs";

const SELLER_EMAIL = process.env.E2E_SELLER_EMAIL || "seller.bachdang@nexusmart.local";
const SELLER_PASSWORD = process.env.E2E_SELLER_PASSWORD || "SellerDevPassword123!";
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

function extractPdpReviewsSection(html) {
  const cleanHtml = String(html ?? "").replace(/<!--[\s\S]*?-->/g, "");
  const match = cleanHtml.match(
    /<section[^>]*aria-labelledby="pdp-reviews-heading"[^>]*>[\s\S]*?<\/section>/
  );
  return match ? match[0] : "";
}

async function createApprovedProduct(sellerClient, adminClient, tag, categoryIdOverride = null) {
  let categoryId = categoryIdOverride;
  if (!categoryId) {
    const catRes = await sellerClient.json("GET", "/api/gw/categories");
    assert.equal(catRes.status, 200, `categories failed: ${catRes.text}`);
    const cat = (catRes.data?.categories ?? []).find((c) => !c.is_prohibited);
    assert.ok(cat?.id, `no non-prohibited category available: ${catRes.text}`);
    categoryId = cat.id;
  }

  const suffix = crypto.randomBytes(4).toString("hex");
  const createRes = await sellerClient.json("POST", "/api/gw/seller/products", {
    body: {
      category_id: categoryId,
      name: `San pham W3B ${tag} ${suffix}`,
      slug: `san-pham-w3b-${tag}-${suffix}`,
      description: "Sản phẩm kiểm thử tự động đợt 3b-1.",
      images: ["/products/ao-thun-cotton-trang.png"],
      skus: [
        {
          sku_code: `W3B-${tag.toUpperCase()}-${suffix.toUpperCase()}-M`,
          name: "Trắng tinh / M",
          attributes: { color: "Trắng tinh", size: "M" },
          price_vnd: 195_000,
          stock: 15,
          weight_grams: 280,
          image_url: "/products/ao-thun-cotton-trang.png",
        },
      ],
    },
  });
  assert.equal(
    createRes.status,
    201,
    `create W3B product expected 201, got ${createRes.status}: ${createRes.text}`
  );
  const created = createRes.data?.product;
  assert.ok(created?.id && created?.skus?.[0]?.id, `missing product/sku: ${createRes.text}`);

  const reviewRes = await adminClient.json(
    "POST",
    `/api/gw/admin/products/${encodeURIComponent(created.id)}/review`,
    { body: { decision: "APPROVE", reason: "E2E W3B auto-approve" } }
  );
  assert.equal(reviewRes.status, 200, `approve W3B product failed: ${reviewRes.text}`);
  const approved = reviewRes.data?.product ?? created;
  return { product: approved, sku: approved.skus[0] };
}

describe("09. Wave 3b-1 A4 (reviews, banned keywords, prohibited categories, discontinued PDP) & Cart unavailable", () => {
  test("1. [A4/E5-E8/E14] Verified COMPLETED order review, duplicate 409, invalid image URL 400, banned keyword 400, E6-bis redaction & PDP SSR", async () => {
    await ensureRateBudget(50, { label: "w3b-a4-reviews" });
    const anon = new WebClient();
    const buyer = new WebClient();
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);

    const buyerAcct = await registerAndLoginBuyer(buyer, "w3b-rev");
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const { product, sku } = await createApprovedProduct(seller, admin, "rev");

    let createdKeywordId = null;
    let createdOrderId = null;
    let orderCompleted = false;
    try {
      // Fresh product must report 0 reviews and PDP reviews section must show "Chưa có đánh giá"
      const initialRevRes = await anon.json(
        "GET",
        `/api/gw/products/${encodeURIComponent(product.id)}/reviews?page=1&page_size=10`
      );
      assert.equal(initialRevRes.status, 200, `initial reviews failed: ${initialRevRes.text}`);
      assert.equal(initialRevRes.data?.total ?? 0, 0);

      const initialPdp = await anon.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      assert.equal(initialPdp.status, 200, `initial PDP expected 200, got ${initialPdp.status}`);
      const initialReviewsHtml = extractPdpReviewsSection(initialPdp.text);
      assert.ok(initialReviewsHtml, "PDP SSR must render reviews section");
      assert.ok(
        initialReviewsHtml.includes("Chưa có đánh giá"),
        "PDP with 0 reviews must render 'Chưa có đánh giá'"
      );

      // Complete a VietQR order through payment webhook -> ship -> shipping webhooks -> confirm-received
      const quote = await calculateShippingQuote(buyer, {
        shopId: product.shop_id,
        province,
        ward,
        weightGrams: 280,
        codAmountVnd: 0,
      });

      const checkoutRes = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `e2e-w3b-rev-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          coins_to_use: 0,
          payment_method: "VIETQR",
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
        `checkout expected 201, got ${checkoutRes.status}: ${checkoutRes.text}`
      );
      const order = checkoutRes.data?.orders?.[0];
      const paymentId = checkoutRes.data?.payment_id;
      const checkoutGroupId = checkoutRes.data?.checkout_group_id;
      assert.ok(
        order?.id && paymentId && checkoutGroupId,
        `missing order/payment/group: ${checkoutRes.text}`
      );
      createdOrderId = order.id;

      const webhookRes = await postSignedPaymentWebhook({
        provider: "vietqr_mock",
        provider_tx_id: `TXN-W3B-REV-${crypto.randomUUID()}`,
        payment_id: paymentId,
        order_group_id: checkoutGroupId,
        amount_vnd: checkoutRes.data.total_payable_vnd,
        status: "SUCCEEDED",
        reference: `NEXUSMART ${checkoutGroupId}`,
        occurred_at: new Date().toISOString(),
      });
      assert.equal(webhookRes.status, 200, `payment webhook failed: ${webhookRes.text}`);

      await pollUntil(
        async () => {
          const r = await buyer.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
          const st = normalizeStatus(r.data?.order?.status);
          return { done: st === "CONFIRMED", detail: `order status=${st}` };
        },
        { label: "order CONFIRMED for review" }
      );

      const shipRes = await seller.json(
        "POST",
        `/api/gw/seller/orders/${encodeURIComponent(order.id)}/ship`,
        {
          body: {
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        }
      );
      assert.equal(
        shipRes.status,
        200,
        `seller ship expected 200, got ${shipRes.status}: ${shipRes.text}`
      );

      let trackingCode = shipRes.data?.tracking_code || shipRes.data?.order?.tracking_code || "";
      if (!trackingCode && shipRes.data?.queued) {
        trackingCode = await pollUntil(
          async () => {
            const r = await seller.json(
              "GET",
              `/api/gw/seller/orders/${encodeURIComponent(order.id)}`
            );
            const tc = r.data?.order?.tracking_code || "";
            return {
              done: Boolean(tc),
              value: tc,
              detail: `shipment_job_status=${r.data?.order?.shipment_job_status ?? ""}`,
            };
          },
          { label: "queued shipment tracking_code" }
        );
      }
      assert.ok(trackingCode, `seller ship missing tracking_code: ${shipRes.text}`);

      const detailBefore = await buyer.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
      assert.equal(
        detailBefore.status,
        200,
        `GET order expected 200, got ${detailBefore.status}: ${detailBefore.text}`
      );
      const orderItemId = detailBefore.data?.order?.items?.[0]?.id;
      assert.ok(orderItemId, `missing order_item_id: ${detailBefore.text}`);

      // Reviewing before COMPLETED must fail with 409 (FailedPrecondition)
      const prematureRes = await buyer.json(
        "POST",
        `/api/gw/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          body: {
            order_item_id: orderItemId,
            rating: 5,
            comment: "Thử đánh giá trước khi hoàn tất đơn.",
            image_urls: [],
          },
        }
      );
      assert.equal(
        prematureRes.status,
        409,
        `review before COMPLETED must return 409, got ${prematureRes.status}: ${prematureRes.text}`
      );

      // Carrier FORWARD webhooks: delivering -> delivered (same flow as 06_returns.test.mjs)
      const whDelivering = await postSignedShippingWebhook(quote.carrier_code, {
        event_id: `e2e-w3b-rev-deliv-${crypto.randomUUID()}`,
        tracking_code: trackingCode,
        order_id: order.id,
        raw_status: "delivering",
        description: "Đang giao hàng tới người mua",
        occurred_at: new Date().toISOString(),
      });
      assert.equal(
        whDelivering.status,
        200,
        `FORWARD delivering webhook failed: ${whDelivering.text}`
      );

      const whDelivered = await postSignedShippingWebhook(quote.carrier_code, {
        event_id: `e2e-w3b-rev-done-${crypto.randomUUID()}`,
        tracking_code: trackingCode,
        order_id: order.id,
        raw_status: "delivered",
        description: "Giao hàng thành công",
        occurred_at: new Date().toISOString(),
      });
      assert.equal(
        whDelivered.status,
        200,
        `FORWARD delivered webhook failed: ${whDelivered.text}`
      );

      await pollUntil(
        async () => {
          const r = await buyer.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
          const st = normalizeStatus(r.data?.order?.status);
          return { done: st === "DELIVERED", detail: `order status=${st}` };
        },
        { label: "order DELIVERED for review" }
      );

      // Buyer confirms received -> COMPLETED
      const recvRes = await buyer.json(
        "POST",
        `/api/gw/orders/${encodeURIComponent(order.id)}/confirm-received`
      );
      assert.equal(
        recvRes.status,
        200,
        `confirm-received expected 200, got ${recvRes.status}: ${recvRes.text}`
      );

      await pollUntil(
        async () => {
          const r = await buyer.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
          const st = normalizeStatus(r.data?.order?.status);
          return { done: st === "COMPLETED", detail: `order status=${st}` };
        },
        { label: "order COMPLETED for review" }
      );
      orderCompleted = true;

      // E7: http:// image URL must be rejected with 400
      const badImgRes = await buyer.json(
        "POST",
        `/api/gw/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          body: {
            order_item_id: orderItemId,
            rating: 5,
            comment: "Ảnh không hợp lệ",
            image_urls: ["http://insecure.example.com/1.jpg"],
          },
        }
      );
      assert.equal(
        badImgRes.status,
        400,
        `http:// review image must return 400, got ${badImgRes.status}: ${badImgRes.text}`
      );

      // E9/E10/E10-bis: Create a BLOCK banned keyword and verify 400 BANNED_CONTENT on review without leaking keyword
      const uniqueTerm = `tukhoacamw3b${crypto.randomBytes(3).toString("hex")}`;
      const kwRes = await admin.json("POST", "/api/gw/admin/banned-keywords", {
        body: { term: uniqueTerm, category: "SPAM", action: "BLOCK" },
      });
      assert.equal(
        kwRes.status,
        201,
        `create banned keyword expected 201, got ${kwRes.status}: ${kwRes.text}`
      );
      createdKeywordId = kwRes.data?.keyword?.id ?? null;
      assert.ok(createdKeywordId, `missing keyword.id: ${kwRes.text}`);

      const bannedRevRes = await buyer.json(
        "POST",
        `/api/gw/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          body: {
            order_item_id: orderItemId,
            rating: 4,
            comment: `Nội dung chứa ${uniqueTerm} cần bị chặn`,
            image_urls: [],
          },
        }
      );
      assert.equal(
        bannedRevRes.status,
        400,
        `review with BLOCK keyword must return 400, got ${bannedRevRes.status}: ${bannedRevRes.text}`
      );
      assert.equal(getBffError(bannedRevRes).code, "BANNED_CONTENT");
      assert.equal(
        bannedRevRes.text.toLowerCase().includes(uniqueTerm.toLowerCase()),
        false,
        `E10-bis violation: review BANNED_CONTENT error leaked keyword ${uniqueTerm}`
      );

      // Submit valid verified review with safe /products/... image URL
      const reviewComment = "Vải cotton mềm, đường may đều và giao đúng hẹn.";
      const reviewImageUrl = "/products/ao-thun-cotton-trang.png";
      const okRevRes = await buyer.json(
        "POST",
        `/api/gw/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          body: {
            order_item_id: orderItemId,
            rating: 5,
            comment: reviewComment,
            image_urls: [reviewImageUrl],
          },
        }
      );
      assert.equal(
        okRevRes.status,
        201,
        `valid review expected 201, got ${okRevRes.status}: ${okRevRes.text}`
      );
      assert.deepEqual(okRevRes.data?.review?.image_urls, [reviewImageUrl]);

      // Duplicate review on same order_item_id must return 409
      const dupRevRes = await buyer.json(
        "POST",
        `/api/gw/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          body: {
            order_item_id: orderItemId,
            rating: 4,
            comment: "Đánh giá trùng lặp",
            image_urls: [],
          },
        }
      );
      assert.equal(
        dupRevRes.status,
        409,
        `duplicate review must return 409, got ${dupRevRes.status}: ${dupRevRes.text}`
      );

      // E6-bis: Public GET /products/{id}/reviews must strip buyer_id/order_id/order_item_id/sku_id,
      // expose image_urls, and mask buyer_name
      const pubRevRes = await anon.json(
        "GET",
        `/api/gw/products/${encodeURIComponent(product.id)}/reviews?page=1&page_size=10`
      );
      assert.equal(pubRevRes.status, 200, `public reviews failed: ${pubRevRes.text}`);
      const pubItem = (pubRevRes.data?.items ?? [])[0];
      assert.ok(pubItem, `expected >= 1 public review: ${pubRevRes.text}`);
      assert.equal(pubItem.rating, 5);
      assert.equal(pubItem.comment, reviewComment);
      assert.deepEqual(pubItem.image_urls, [reviewImageUrl]);
      assert.equal("buyer_id" in pubItem, false, `E6-bis violation: buyer_id leaked (${pubItem.buyer_id})`);
      assert.equal("order_id" in pubItem, false, `E6-bis violation: order_id leaked (${pubItem.order_id})`);
      assert.equal(
        "order_item_id" in pubItem,
        false,
        `E6-bis violation: order_item_id leaked (${pubItem.order_item_id})`
      );
      assert.equal("sku_id" in pubItem, false, `E6-bis violation: sku_id leaked (${pubItem.sku_id})`);
      assert.ok(
        typeof pubItem.buyer_name === "string" && pubItem.buyer_name.length > 0,
        `public review missing buyer_name: ${JSON.stringify(pubItem)}`
      );
      assert.notEqual(
        pubItem.buyer_name,
        buyerAcct.fullName,
        `buyer_name must be masked, got full name: ${pubItem.buyer_name}`
      );
      assert.equal(
        pubItem.buyer_name.includes(buyerAcct.email),
        false,
        `buyer_name must not leak email: ${pubItem.buyer_name}`
      );
      assert.match(
        pubItem.buyer_name,
        /^\p{Lu}\.\s+\S+$|^\p{Lu}\*\*\*$|^Người mua đã xác thực$/u,
        `buyer_name does not match E5 masked format: ${pubItem.buyer_name}`
      );

      // E14: Product rating_avg_x100 & review_count updated
      const prodLookup = await anon.json(
        "GET",
        `/api/gw/products/slug/${encodeURIComponent(product.slug)}`
      );
      assert.equal(prodLookup.status, 200);
      assert.equal(prodLookup.data?.product?.rating_avg_x100, 500);
      assert.equal(prodLookup.data?.product?.review_count, 1);

      // SSR /san-pham/{slug} renders submitted review comment and no longer shows "Chưa có đánh giá"
      const afterPdp = await anon.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      assert.equal(afterPdp.status, 200, `after-review PDP expected 200, got ${afterPdp.status}`);
      const afterReviewsHtml = extractPdpReviewsSection(afterPdp.text);
      assert.ok(afterReviewsHtml, "after-review PDP SSR must render reviews section");
      assert.ok(
        afterReviewsHtml.includes(reviewComment),
        "after-review PDP SSR must include submitted review comment"
      );
      assert.ok(
        afterReviewsHtml.includes("1 đánh giá đã xác thực"),
        "after-review PDP SSR must show verified review count"
      );
      assert.equal(
        afterReviewsHtml.includes("Chưa có đánh giá"),
        false,
        "after-review PDP SSR reviews section must no longer show 'Chưa có đánh giá'"
      );
    } finally {
      if (createdOrderId && !orderCompleted) {
        await buyer
          .json("POST", `/api/gw/orders/${encodeURIComponent(createdOrderId)}/cancel`, {
            body: { reason: "e2e 09 cleanup" },
          })
          .catch(() => {});
      }
      if (createdKeywordId) {
        await admin
          .json("DELETE", `/api/gw/admin/banned-keywords/${encodeURIComponent(createdKeywordId)}`)
          .catch(() => {});
      }
      await seller
        .json("DELETE", `/api/gw/seller/products/${encodeURIComponent(product.id)}`)
        .catch(() => {});
    }
  });

  test("2. [A4/E9-E12] Admin banned keywords CRUD + preview + batch rescan & prohibited category blocking seller", async () => {
    await ensureRateBudget(35, { label: "w3b-a4-mod" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);

    const suffix = crypto.randomBytes(4).toString("hex");
    let kwId = null;
    let prohibitedCatId = null;
    try {
      // Create REVIEW keyword, preview & dry-run rescan
      const kwTerm = `camtest${suffix}`;
      const createKw = await admin.json("POST", "/api/gw/admin/banned-keywords", {
        body: { term: kwTerm, category: "COUNTERFEIT", action: "REVIEW" },
      });
      assert.equal(
        createKw.status,
        201,
        `create banned keyword expected 201, got ${createKw.status}: ${createKw.text}`
      );
      kwId = createKw.data?.keyword?.id ?? null;
      assert.ok(kwId, `missing keyword.id: ${createKw.text}`);

      const previewRes = await admin.json("POST", "/api/gw/admin/banned-keywords/preview", {
        body: { term: kwTerm },
      });
      assert.equal(previewRes.status, 200, `preview failed: ${previewRes.text}`);
      assert.ok(Array.isArray(previewRes.data?.samples));

      const rescanRes = await admin.json("POST", "/api/gw/admin/banned-keywords/rescan", {
        body: { limit: 500, cursor: "", dry_run: true },
      });
      assert.equal(rescanRes.status, 200, `rescan failed: ${rescanRes.text}`);
      assert.equal(typeof rescanRes.data?.scanned_count, "number");

      // E12: Create prohibited category and ensure seller product creation returns 400 PROHIBITED_CATEGORY
      const catRes = await admin.json("POST", "/api/gw/admin/categories", {
        body: {
          name: `Danh muc cam E2E ${suffix}`,
          slug: `danh-muc-cam-e2e-${suffix}`,
          sort_order: 99,
          is_prohibited: true,
        },
      });
      assert.equal(
        catRes.status,
        201,
        `create prohibited category expected 201, got ${catRes.status}: ${catRes.text}`
      );
      prohibitedCatId = catRes.data?.category?.id ?? null;
      assert.ok(prohibitedCatId, `missing prohibited category.id: ${catRes.text}`);

      const blockedProdRes = await seller.json("POST", "/api/gw/seller/products", {
        body: {
          category_id: prohibitedCatId,
          name: `San pham danh muc cam ${suffix}`,
          slug: `san-pham-danh-muc-cam-${suffix}`,
          description: "Kiểm tra chặn danh mục cấm",
          images: ["/products/ao-thun-cotton-trang.png"],
          skus: [
            {
              sku_code: `PROHIB-${suffix.toUpperCase()}`,
              name: "Mặc định",
              attributes: {},
              price_vnd: 120_000,
              stock: 5,
              weight_grams: 200,
            },
          ],
        },
      });
      assert.equal(
        blockedProdRes.status,
        400,
        `product in prohibited category must return 400, got ${blockedProdRes.status}: ${blockedProdRes.text}`
      );
      assert.equal(getBffError(blockedProdRes).code, "PROHIBITED_CATEGORY");
    } finally {
      if (kwId) {
        await admin
          .json("DELETE", `/api/gw/admin/banned-keywords/${encodeURIComponent(kwId)}`)
          .catch(() => {});
      }
      if (prohibitedCatId) {
        await admin
          .json("DELETE", `/api/gw/admin/categories/${encodeURIComponent(prohibitedCatId)}`)
          .catch(() => {});
      }
    }
  });

  test("3. [A4/E13 + Cart/E3-E4] Discontinued PDP renders notice + noindex + similar products; Cart marks unavailable SKU and allows removal", async () => {
    await ensureRateBudget(35, { label: "w3b-discontinued-cart" });
    const anon = new WebClient();
    const buyer = new WebClient();
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);

    await registerAndLoginBuyer(buyer, "w3b-cart-gone");
    const { product, sku } = await createApprovedProduct(seller, admin, "gone");

    let productDeleted = false;
    try {
      // Add SKU to buyer cart while active
      const addRes = await buyer.json("POST", "/api/gw/cart/items", {
        body: { sku_id: sku.id, quantity: 2 },
      });
      assert.equal(
        addRes.status,
        200,
        `add to cart expected 200, got ${addRes.status}: ${addRes.text}`
      );

      // Soft-delete / discontinue product
      const delRes = await seller.json(
        "DELETE",
        `/api/gw/seller/products/${encodeURIComponent(product.id)}`
      );
      assert.equal(delRes.status, 200, `soft-delete product failed: ${delRes.text}`);
      productDeleted = true;

      // E13: API returns { discontinued: true, category_id } and SSR page renders 200 + noindex + notice
      const slugApi = await anon.json(
        "GET",
        `/api/gw/products/slug/${encodeURIComponent(product.slug)}`
      );
      assert.equal(slugApi.status, 200, `discontinued slug lookup expected 200, got ${slugApi.status}`);
      assert.equal(slugApi.data?.discontinued, true);

      const pdpRes = await anon.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      assert.equal(pdpRes.status, 200, `discontinued PDP must return HTTP 200, got ${pdpRes.status}`);
      assert.ok(
        pdpRes.text.includes("Sản phẩm đã ngừng kinh doanh"),
        "discontinued PDP must render 'Sản phẩm đã ngừng kinh doanh'"
      );
      assert.ok(
        /name="robots"[^>]*noindex/i.test(pdpRes.text),
        "discontinued PDP must emit robots noindex meta tag"
      );

      // E3/E4: Buyer cart retains line with unavailable=true and zero price, and allows DELETE
      const cartRes = await buyer.json("GET", "/api/gw/cart");
      assert.equal(cartRes.status, 200, `get cart failed: ${cartRes.text}`);
      const allItems = (cartRes.data?.cart?.shop_groups ?? []).flatMap((g) => g.items ?? []);
      const goneItem = allItems.find((it) => it.sku_id === sku.id);
      assert.ok(
        goneItem,
        `expected unavailable item ${sku.id} to remain in cart for buyer cleanup: ${cartRes.text}`
      );
      assert.equal(goneItem.unavailable, true);
      assert.equal(goneItem.price_vnd ?? 0, 0);

      const removeRes = await buyer.json(
        "DELETE",
        `/api/gw/cart/items/${encodeURIComponent(sku.id)}`
      );
      assert.equal(
        removeRes.status,
        200,
        `delete unavailable cart item expected 200, got ${removeRes.status}: ${removeRes.text}`
      );
    } finally {
      if (!productDeleted) {
        await seller
          .json("DELETE", `/api/gw/seller/products/${encodeURIComponent(product.id)}`)
          .catch(() => {});
      }
    }
  });
});
