import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { createHmac, randomUUID } from "node:crypto";
import { ROOT_HOST, config, originOf, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, pollUntil, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  assertNoTokenLeak,
  checkout,
  createShippingAddress,
  demoCredentials,
  fillCart,
  loginOn,
  newIdempotencyKey,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);
const FAKE_ID = "00000000-0000-4000-8000-000000000000";
const SHIPPING_WEBHOOK_SECRET =
  (process.env.SHIPPING_WEBHOOK_SECRET ?? "").trim() || "dev-only-shipping-webhook-hmac-secret";
const bare = (host) => host.replace(/:\d+$/, "");

/**
 * Plays the external shipping carrier: posts HMAC-SHA256 signed delivering + delivered webhooks
 * to the gateway so the order transitions SHIPPING -> DELIVERED cleanly.
 */
async function deliverShipmentViaWebhook({ carrierCode, trackingCode, orderId }) {
  const carrier = (carrierCode || "ghn").trim().toLowerCase();
  const nowMs = Date.now();
  const steps = [
    { raw_status: "delivering", description: "Shipper is out for delivery", atMs: nowMs - 2000 },
    { raw_status: "delivered", description: "Package delivered successfully", atMs: nowMs },
  ];
  for (const step of steps) {
    const body = JSON.stringify({
      event_id: `e2e-web-ship-${randomUUID()}`,
      tracking_code: trackingCode,
      order_id: orderId,
      raw_status: step.raw_status,
      description: step.description,
      occurred_at: new Date(step.atMs).toISOString().replace(/\.\d{3}Z$/, "Z"),
    });
    const sig = createHmac("sha256", SHIPPING_WEBHOOK_SECRET).update(body).digest("hex");
    const res = await fetch(`${config.gatewayURL}/webhooks/shipping/${encodeURIComponent(carrier)}`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-signature": sig,
        "x-timestamp": String(Math.floor(Date.now() / 1000)),
      },
      body,
      signal: AbortSignal.timeout(config.httpTimeoutMs),
    });
    const text = await res.text();
    if (res.status !== 200) {
      throw new Error(`POST /webhooks/shipping/${carrier} (${step.raw_status}): HTTP ${res.status}: ${text}`);
    }
  }
}

/** Places a COD order on bach-dang, confirms + ships via seller BFF, and delivers via webhook. */
async function createAndDeliverCodOrder({ buyerClient, sellerClient, address, sku, tag }) {
  const cart = await fillCart(buyerClient, sku.skuId, 1);
  const { shippingChoices } = await quoteShipping(buyerClient, address, cart.shop_groups, true);
  const cod = expectStatus(
    await checkout(buyerClient, {
      addressId: address.id,
      shippingChoices,
      paymentMethod: "COD",
      key: newIdempotencyKey(`ret-${tag}`),
    }),
    201,
    `POST ${SITE_A_HOST}/api/checkout (${tag})`
  );
  const order = cod.orders[0];
  assert.ok(order?.id, `checkout (${tag}) must return order.id`);

  const confirmed = expectStatus(
    await sellerClient.post(`/api/seller/orders/${encodeURIComponent(order.id)}/confirm`, {}),
    200,
    `POST /api/seller/orders/${order.id}/confirm (${tag})`
  );
  assert.equal(String(confirmed.order?.status || "").toUpperCase(), "PREPARING");

  const carrierCode = shippingChoices[0]?.carrier_code || "ghn";
  const serviceCode = shippingChoices[0]?.service_code || "standard";
  const shipped = expectStatus(
    await sellerClient.post(`/api/seller/orders/${encodeURIComponent(order.id)}/ship`, {
      carrier_code: carrierCode,
      service_code: serviceCode,
    }),
    200,
    `POST /api/seller/orders/${order.id}/ship (${tag})`
  );
  assert.equal(String(shipped.order?.status || "").toUpperCase(), "SHIPPING");
  const trackingCode = shipped.tracking_code || shipped.order?.tracking_code;
  assert.ok(trackingCode, `ship (${tag}) must return tracking_code`);

  await deliverShipmentViaWebhook({ carrierCode, trackingCode, orderId: order.id });

  await pollUntil(`order ${order.id} (${tag}) status DELIVERED`, async () => {
    const res = await buyerClient.get(`/api/orders/${encodeURIComponent(order.id)}`);
    const data = expectStatus(res, 200, `poll GET /api/orders/${order.id}`);
    const st = String(data.order?.backendStatus || "").toUpperCase();
    return [st === "DELIVERED", `backendStatus=${st}`, data.order];
  });

  return order;
}

describe("07. Buyer returns, Seller real pages + AI BFF & Admin complaints/config on ROOT_HOST", () => {
  let siteA;
  let skuA;

  let buyer1Browser;
  let buyer1A;
  let buyer1B;
  let buyer1Root;
  let buyer1;
  let buyer1Address;

  let sellerRoot;
  let sellerHanoiRoot;
  let adminRoot;

  let approvedOrderId = "";
  let approvedReturnId = "";
  let rejectedOrderId = "";
  let rejectedReturnId = "";

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10 });

    buyer1Browser = new Browser("ret-buyer1");
    buyer1A = buyer1Browser.at(SITE_A_HOST);
    buyer1B = buyer1Browser.at(SITE_B_HOST);
    buyer1Root = buyer1Browser.at(ROOT_HOST);

    sellerRoot = new Browser("ret-seller-a").at(ROOT_HOST);
    sellerHanoiRoot = new Browser("ret-seller-hanoi").at(ROOT_HOST);
    adminRoot = new Browser("ret-admin").at(ROOT_HOST);

    buyer1 = await registerBuyer(buyer1A, "ret-b1");
    buyer1Address = await createShippingAddress(buyer1A, "ret-b1");
    await loginOn(buyer1B, buyer1.creds.email, buyer1.creds.password, "ret-b1-site-b");
    await loginOn(buyer1Root, buyer1.creds.email, buyer1.creds.password, "ret-b1-root");
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "ret-seller-a");
    await loginOn(
      sellerHanoiRoot,
      demoCredentials.sellerHanoi.email,
      demoCredentials.sellerHanoi.password,
      "ret-seller-hanoi"
    );
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "ret-admin");
  });

  after(async () => {
    await Promise.allSettled([
      buyer1A.post("/api/auth/logout", {}),
      buyer1B.post("/api/auth/logout", {}),
      buyer1Root.post("/api/auth/logout", {}),
      sellerRoot.post("/api/auth/logout", {}),
      sellerHanoiRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("a) Buyer return lifecycle on bach-dang: COD -> confirm/ship/deliver -> POST/GET returns, cross-buyer/cross-site isolation, seller approve & reject", async () => {
    const buyerSecrets = [buyer1.authCookies.access.value, buyer1.authCookies.refresh.value];

    // 1. Place & deliver Order #1 (seller will APPROVE this return).
    const order1 = await createAndDeliverCodOrder({
      buyerClient: buyer1A,
      sellerClient: sellerRoot,
      address: buyer1Address,
      sku: skuA,
      tag: "approve",
    });
    approvedOrderId = order1.id;

    // Validation & CSRF negative checks before submitting valid return.
    const badPhotoRes = await buyer1A.post(`/api/orders/${encodeURIComponent(order1.id)}/returns`, {
      items: [{ sku_id: skuA.skuId, quantity: 1 }],
      reason: "Sản phẩm bị lỗi đường may cần đổi trả",
      photos: ["javascript:alert(1)"],
    });
    expectStatus(badPhotoRes, 400, "POST return with javascript: photo URL must return 400");
    assertNoInternalErrorLeak(badPhotoRes.text, "bad photo return");

    const csrfRetRes = await buyer1A.post(
      `/api/orders/${encodeURIComponent(order1.id)}/returns`,
      {
        items: [{ sku_id: skuA.skuId, quantity: 1 }],
        reason: "Kiểm tra CSRF chéo domain",
        photos: [],
      },
      { origin: originOf(SITE_B_HOST) }
    );
    expectStatus(csrfRetRes, 403, "CSRF POST /api/orders/[id]/returns from site B Origin");
    assert.equal(csrfRetRes.data?.error?.code, "BAD_ORIGIN");

    // 2. Buyer submits valid return request on bach-dang -> 201.
    const createRet1Res = await buyer1A.post(`/api/orders/${encodeURIComponent(order1.id)}/returns`, {
      items: [{ sku_id: skuA.skuId, quantity: 1 }],
      reason: `Sản phẩm giao sai kích cỡ, yêu cầu đổi trả E2E ${uid()}`,
      photos: ["/banners/vietnam-workshop.jpg"],
    });
    const createRet1 = expectStatus(
      createRet1Res,
      201,
      `POST ${SITE_A_HOST}/api/orders/${order1.id}/returns`
    );
    assertNoTokenLeak(createRet1Res.text, "POST /api/orders/[id]/returns", buyerSecrets);
    assertNoInternalErrorLeak(createRet1Res.text, "POST /api/orders/[id]/returns");
    approvedReturnId = createRet1.return_request?.id;
    assert.ok(approvedReturnId, "POST /api/orders/[id]/returns must return return_request.id");
    assert.equal(createRet1.return_request.status, "PENDING");

    // 3. Buyer reads GET /api/orders/[id]/returns on bach-dang -> 200.
    const listRet1Res = await buyer1A.get(`/api/orders/${encodeURIComponent(order1.id)}/returns`);
    const listRet1 = expectStatus(
      listRet1Res,
      200,
      `GET ${SITE_A_HOST}/api/orders/${order1.id}/returns`
    );
    assertNoTokenLeak(listRet1Res.text, "GET /api/orders/[id]/returns", buyerSecrets);
    assert.ok(
      (listRet1.returns ?? []).some((r) => r.id === approvedReturnId),
      `GET /api/orders/${order1.id}/returns must contain return ${approvedReturnId}`
    );

    // 4. Cross-site (sai-gon) and root host isolation for same buyer1 -> 404.
    expectStatus(
      await buyer1B.get(`/api/orders/${encodeURIComponent(order1.id)}/returns`),
      404,
      `same buyer GET ${SITE_B_HOST}/api/orders/${order1.id}/returns`
    );
    expectStatus(
      await buyer1B.post(`/api/orders/${encodeURIComponent(order1.id)}/returns`, {
        items: [{ sku_id: skuA.skuId, quantity: 1 }],
        reason: "Cross-site return attempt",
      }),
      404,
      `same buyer POST ${SITE_B_HOST}/api/orders/${order1.id}/returns`
    );
    expectStatus(
      await buyer1Root.get(`/api/orders/${encodeURIComponent(order1.id)}/returns`),
      404,
      `GET ${ROOT_HOST}/api/orders/${order1.id}/returns`
    );

    // 5. Cross-buyer isolation: buyer2 on bach-dang gets 403/404.
    const buyer2A = new Browser("ret-buyer2").at(SITE_A_HOST);
    await registerBuyer(buyer2A, "ret-b2");
    try {
      const b2Get = await buyer2A.get(`/api/orders/${encodeURIComponent(order1.id)}/returns`);
      expectStatus(b2Get, [403, 404], "buyer2 GET buyer1 order returns");
      assertNoInternalErrorLeak(b2Get.text, "buyer2 GET order returns");

      const b2Post = await buyer2A.post(`/api/orders/${encodeURIComponent(order1.id)}/returns`, {
        items: [{ sku_id: skuA.skuId, quantity: 1 }],
        reason: "IDOR return request by buyer2",
      });
      expectStatus(b2Post, [403, 404], "buyer2 POST buyer1 order returns");
    } finally {
      await buyer2A.post("/api/auth/logout", {}).catch(() => {});
    }

    // 6. Seller on ROOT_HOST views returns & approves return #1; unrelated sellerHanoi gets 403/404.
    const sellerListRes = await sellerRoot.get(`/api/seller/orders/${encodeURIComponent(order1.id)}/returns`);
    const sellerList = expectStatus(
      sellerListRes,
      200,
      `GET /api/seller/orders/${order1.id}/returns`
    );
    assertNoInternalErrorLeak(sellerListRes.text, "GET /api/seller/orders/[id]/returns");
    assert.ok(
      (sellerList.returns ?? []).some((r) => r.id === approvedReturnId),
      `seller returns list must contain ${approvedReturnId}`
    );

    expectStatus(
      await sellerHanoiRoot.get(`/api/seller/orders/${encodeURIComponent(order1.id)}/returns`),
      [403, 404],
      "unrelated sellerHanoi GET bach-dang order returns"
    );
    expectStatus(
      await sellerHanoiRoot.post(`/api/seller/returns/${encodeURIComponent(approvedReturnId)}/approve`, {
        note: "cross-seller approve",
      }),
      [403, 404],
      "unrelated sellerHanoi approve bach-dang return"
    );

    const approveRes = await sellerRoot.post(
      `/api/seller/returns/${encodeURIComponent(approvedReturnId)}/approve`,
      { note: "Đồng ý tiếp nhận yêu cầu đổi trả kiểm thử E2E" }
    );
    const approveData = expectStatus(
      approveRes,
      200,
      `POST /api/seller/returns/${approvedReturnId}/approve`
    );
    assertNoInternalErrorLeak(approveRes.text, "POST /api/seller/returns/[id]/approve");
    assert.equal(approveData.ok, true);
    assert.ok(
      ["APPROVED", "RETURNING", "REFUNDED"].includes(String(approveData.return_request?.status || "").toUpperCase()),
      `expected approved return status APPROVED/RETURNING/REFUNDED, got ${approveData.return_request?.status}`
    );

    // 7. Place & deliver Order #2 -> buyer submits return -> seller REJECTS (auto-escalates complaint for Admin test).
    const order2 = await createAndDeliverCodOrder({
      buyerClient: buyer1A,
      sellerClient: sellerRoot,
      address: buyer1Address,
      sku: skuA,
      tag: "reject",
    });
    rejectedOrderId = order2.id;

    const createRet2 = expectStatus(
      await buyer1A.post(`/api/orders/${encodeURIComponent(order2.id)}/returns`, {
        items: [{ sku_id: skuA.skuId, quantity: 1 }],
        reason: `Sản phẩm có vết bẩn nhỏ, yêu cầu đổi trả E2E ${uid()}`,
        photos: ["/banners/vietnam-workshop.jpg"],
      }),
      201,
      `POST ${SITE_A_HOST}/api/orders/${order2.id}/returns (order2)`
    );
    rejectedReturnId = createRet2.return_request?.id;
    assert.ok(rejectedReturnId, "order2 return_request.id must be present");

    const rejectRes = await sellerRoot.post(
      `/api/seller/returns/${encodeURIComponent(rejectedReturnId)}/reject`,
      { reason: "Sản phẩm đã cắt tem mác và có dấu hiệu sử dụng thực tế" }
    );
    const rejectData = expectStatus(
      rejectRes,
      200,
      `POST /api/seller/returns/${rejectedReturnId}/reject`
    );
    assertNoInternalErrorLeak(rejectRes.text, "POST /api/seller/returns/[id]/reject");
    assert.equal(rejectData.ok, true);
    assert.equal(String(rejectData.return_request?.status || "").toUpperCase(), "REJECTED");
  });

  it("b) Seller real pages (/seller/chat, /ho-so, /tuong-tac, /bao-cao-ai) & Seller AI BFF on ROOT_HOST + RBAC/CSRF", async () => {
    const anonRoot = new Browser("seller-pages-anon").at(ROOT_HOST);
    const sellerPages = ["/seller/chat", "/seller/ho-so", "/seller/tuong-tac", "/seller/bao-cao-ai"];

    // 1. Unauthenticated requests to seller pages redirect to /dang-nhap on ROOT_HOST.
    for (const path of sellerPages) {
      const res = await anonRoot.page(path);
      expectStatus(res, [302, 303, 307, 308], `unauth GET ${ROOT_HOST}${path}`);
      const loc = new URL(res.location, `http://${ROOT_HOST}`);
      assert.equal(loc.pathname, "/dang-nhap", `unauth ${path} must redirect to /dang-nhap, got ${res.location}`);
      assert.ok(loc.searchParams.get("next"), `unauth ${path} redirect must include next=`);
      assert.equal(bare(loc.host), bare(ROOT_HOST), `unauth ${path} must stay on ROOT_HOST`);
    }

    // 2. Logged-in seller SSR renders 200 on all 4 real screens.
    for (const path of sellerPages) {
      const res = await sellerRoot.page(path);
      expectStatus(res, 200, `seller GET ${ROOT_HOST}${path}`);
      assertNoInternalErrorLeak(res.text, `seller GET ${path}`);
    }

    // 3. Seller AI BFF routes return 200 with expected structure.
    const reportRes = await sellerRoot.post("/api/seller/assistant/report", {
      question: "Tổng quan doanh thu và đơn hàng của gian hàng trong 30 ngày qua",
      period: "30d",
    });
    const reportData = expectStatus(reportRes, 200, "POST /api/seller/assistant/report");
    assertNoTokenLeak(reportRes.text, "POST /api/seller/assistant/report");
    assertNoInternalErrorLeak(reportRes.text, "POST /api/seller/assistant/report");
    assert.ok(
      typeof reportData.answer === "string" && reportData.answer.trim().length > 0,
      "seller report must include non-empty answer"
    );
    assert.ok(Array.isArray(reportData.kpis), "seller report must include kpis array");
    assert.ok(Array.isArray(reportData.rows), "seller report must include rows array");

    const describeRes = await sellerRoot.post("/api/seller/assistant/describe-product", {
      name: "Áo thun cotton compact 220gsm Xưởng Bạch Đằng",
      category_name: "Áo thun",
      attributes: { material: "100% Cotton 220gsm", fit: "Regular fit" },
      colors: ["Trắng", "Đen"],
      sizes: ["M", "L", "XL"],
      price_vnd: 249_000,
      tone: "practical",
    });
    const describeData = expectStatus(describeRes, 200, "POST /api/seller/assistant/describe-product");
    assertNoTokenLeak(describeRes.text, "POST /api/seller/assistant/describe-product");
    assertNoInternalErrorLeak(describeRes.text, "POST /api/seller/assistant/describe-product");
    assert.ok(
      typeof describeData.description === "string" && describeData.description.trim().length > 0,
      "describe-product must return non-empty description"
    );
    assert.ok(Array.isArray(describeData.highlights), "describe-product must return highlights array");
    assert.equal(describeData.aiGenerated, true);

    const suggestRes = await sellerRoot.post("/api/seller/assistant/suggest-reply", {
      customer_message: "Shop ơi áo thun cotton này cao 1m72 nặng 68kg mặc size gì vừa ạ?",
      messages: [
        { role: "buyer", content: "Shop ơi áo thun cotton này cao 1m72 nặng 68kg mặc size gì vừa ạ?" },
      ],
      product_name: "Áo thun cotton compact",
    });
    const suggestData = expectStatus(suggestRes, 200, "POST /api/seller/assistant/suggest-reply");
    assertNoTokenLeak(suggestRes.text, "POST /api/seller/assistant/suggest-reply");
    assertNoInternalErrorLeak(suggestRes.text, "POST /api/seller/assistant/suggest-reply");
    assert.ok(
      Array.isArray(suggestData.suggestions) && suggestData.suggestions.length > 0,
      "suggest-reply must return non-empty suggestions array"
    );
    for (const s of suggestData.suggestions) {
      assert.ok(typeof s === "string" && s.trim().length > 0, "each reply suggestion must be a non-empty string");
    }

    // Seller notifications BFF used by /seller/tuong-tac.
    const notifRes = await sellerRoot.get("/api/seller/notifications");
    const notifData = expectStatus(notifRes, 200, "GET /api/seller/notifications");
    assert.ok(Array.isArray(notifData.items), "seller notifications must return items array");

    // 4. Buyer receives 403 on all seller AI & returns BFFs; anon receives 401.
    const targetOrderId = approvedOrderId || FAKE_ID;
    const targetReturnId = approvedReturnId || FAKE_ID;
    const sellerProtectedCalls = [
      ["POST", "/api/seller/assistant/report", { question: "Doanh thu", period: "7d" }],
      ["POST", "/api/seller/assistant/describe-product", { name: "Áo thun test", category_name: "Áo thun" }],
      ["POST", "/api/seller/assistant/suggest-reply", { customer_message: "Còn size M không?" }],
      ["GET", `/api/seller/orders/${encodeURIComponent(targetOrderId)}/returns`],
      ["POST", `/api/seller/returns/${encodeURIComponent(targetReturnId)}/approve`, { note: "buyer hijack" }],
      ["POST", `/api/seller/returns/${encodeURIComponent(targetReturnId)}/reject`, { reason: "buyer hijack reason" }],
    ];
    for (const [method, path, json] of sellerProtectedCalls) {
      const buyerRes = await buyer1Root.request(method, path, { json });
      expectStatus(buyerRes, 403, `buyer ${method} ${path}`);
      assertNoInternalErrorLeak(buyerRes.text, `buyer ${method} ${path}`);

      const anonRes = await anonRoot.request(method, path, { json });
      expectStatus(anonRes, 401, `anon ${method} ${path}`);
    }

    // 5. CSRF enforced on seller AI & returns writes.
    for (const [label, fn] of [
      [
        "POST /api/seller/assistant/report",
        () =>
          sellerRoot.post(
            "/api/seller/assistant/report",
            { question: "Doanh thu", period: "7d" },
            { origin: originOf(SITE_A_HOST) }
          ),
      ],
      [
        "POST /api/seller/assistant/describe-product",
        () =>
          sellerRoot.post(
            "/api/seller/assistant/describe-product",
            { name: "Áo thun", category_name: "Áo thun" },
            { origin: "https://evil.example" }
          ),
      ],
      [
        "POST /api/seller/returns/[id]/approve",
        () =>
          sellerRoot.post(
            `/api/seller/returns/${encodeURIComponent(targetReturnId)}/approve`,
            { note: "csrf" },
            { origin: null }
          ),
      ],
    ]) {
      const r = await fn();
      expectStatus(r, 403, `CSRF ${label}`);
      assert.equal(r.data?.error?.code, "BAD_ORIGIN");
    }
  });

  it("c) Admin complaints & config on ROOT_HOST: SSR 200, RBAC for buyer/seller, complaints list & resolve, categories/users/vouchers, CSRF", async () => {
    const anonRoot = new Browser("admin-pages-anon").at(ROOT_HOST);
    const adminPages = ["/admin/khieu-nai", "/admin/cau-hinh"];

    // 1. Unauthenticated redirected to /dang-nhap; buyer & seller denied (never 200).
    for (const path of adminPages) {
      const anonRes = await anonRoot.page(path);
      expectStatus(anonRes, [302, 303, 307, 308], `anon GET ${ROOT_HOST}${path}`);
      assert.ok(anonRes.location.includes("/dang-nhap"), `anon ${path} must redirect to /dang-nhap`);

      const buyerPageRes = await buyer1Root.page(path);
      expectStatus(buyerPageRes, [302, 303, 307, 403, 404], `buyer GET ${ROOT_HOST}${path}`);
      assert.notEqual(buyerPageRes.status, 200);

      const sellerPageRes = await sellerRoot.page(path);
      expectStatus(sellerPageRes, [302, 303, 307, 403, 404], `seller GET ${ROOT_HOST}${path}`);
      assert.notEqual(sellerPageRes.status, 200);
    }

    for (const path of ["/api/admin/complaints", "/api/admin/categories", "/api/admin/users", "/api/admin/vouchers"]) {
      expectStatus(await anonRoot.get(path), 401, `anon GET ${path}`);
      expectStatus(await buyer1Root.get(path), 403, `buyer GET ${path}`);
      expectStatus(await sellerRoot.get(path), 403, `seller GET ${path}`);
    }

    // 2. Logged-in admin SSR renders 200 on /admin/khieu-nai and /admin/cau-hinh.
    for (const path of adminPages) {
      const res = await adminRoot.page(path);
      expectStatus(res, 200, `admin GET ${ROOT_HOST}${path}`);
      assertNoInternalErrorLeak(res.text, `admin GET ${path}`);
    }

    // 3. GET /api/admin/complaints -> 200, find escalated complaint from order2 rejection and resolve it.
    const complaintsRes = await adminRoot.get("/api/admin/complaints?page=1&page_size=50");
    const complaintsData = expectStatus(complaintsRes, 200, "GET /api/admin/complaints");
    assertNoTokenLeak(complaintsRes.text, "GET /api/admin/complaints");
    assertNoInternalErrorLeak(complaintsRes.text, "GET /api/admin/complaints");
    assert.ok(Array.isArray(complaintsData.items), "GET /api/admin/complaints must return items array");

    const escalated = complaintsData.items.find(
      (c) =>
        (rejectedOrderId && c.order_id === rejectedOrderId) ||
        (rejectedReturnId && c.return_request_id === rejectedReturnId)
    );
    assert.ok(
      escalated?.id,
      `rejecting return ${rejectedReturnId} (order ${rejectedOrderId}) must escalate an OPEN complaint visible in GET /api/admin/complaints`
    );
    assert.equal(escalated.status, "OPEN");

    // Resolve escalated complaint via Admin BFF (contract: { resolution, note, require_return_shipping }).
    const resolveRes = await adminRoot.post(`/api/admin/complaints/${encodeURIComponent(escalated.id)}/resolve`, {
      resolution: "DISMISS",
      note: `Đã kiểm tra đầy đủ bằng chứng từ hai bên (E2E ${uid()})`,
      require_return_shipping: false,
    });
    const resolveData = expectStatus(
      resolveRes,
      200,
      `POST /api/admin/complaints/${escalated.id}/resolve`
    );
    assertNoTokenLeak(resolveRes.text, "POST /api/admin/complaints/[id]/resolve");
    assertNoInternalErrorLeak(resolveRes.text, "POST /api/admin/complaints/[id]/resolve");
    assert.ok(
      ["RESOLVED", "DISMISSED"].includes(String(resolveData.complaint?.status || "").toUpperCase()),
      `resolved complaint status must be RESOLVED or DISMISSED, got ${resolveData.complaint?.status}`
    );

    // 4. GET /api/admin/categories, /api/admin/users, /api/admin/vouchers -> 200.
    const catRes = await adminRoot.get("/api/admin/categories");
    const catData = expectStatus(catRes, 200, "GET /api/admin/categories");
    assertNoInternalErrorLeak(catRes.text, "GET /api/admin/categories");
    assert.ok(Array.isArray(catData.categories) && catData.categories.length > 0, "admin categories must be non-empty");

    const usersRes = await adminRoot.get("/api/admin/users?page=1&page_size=20");
    const usersData = expectStatus(usersRes, 200, "GET /api/admin/users");
    assertNoTokenLeak(usersRes.text, "GET /api/admin/users");
    assertNoInternalErrorLeak(usersRes.text, "GET /api/admin/users");
    assert.ok(Array.isArray(usersData.items) && usersData.items.length > 0, "admin users must be non-empty");
    assert.ok(usersData.total >= usersData.items.length);

    const vouchersRes = await adminRoot.get("/api/admin/vouchers");
    const vouchersData = expectStatus(vouchersRes, 200, "GET /api/admin/vouchers");
    assertNoInternalErrorLeak(vouchersRes.text, "GET /api/admin/vouchers");
    assert.ok(Array.isArray(vouchersData.vouchers), "admin vouchers must return vouchers array");
    for (const v of vouchersData.vouchers) {
      assert.ok(
        ["PLATFORM", "FREESHIP"].includes(String(v.scope || "").toUpperCase()),
        `admin /api/admin/vouchers leaked non-platform scope ${v.scope}`
      );
    }

    // 5. CSRF enforced on admin writes -> 403 BAD_ORIGIN.
    const adminCsrfChecks = [
      [
        "POST /api/admin/complaints/[id]/resolve (evil Origin)",
        () =>
          adminRoot.post(
            `/api/admin/complaints/${encodeURIComponent(escalated.id)}/resolve`,
            { resolution: "DISMISS", note: "CSRF attempt", require_return_shipping: false },
            { origin: "https://evil.example" }
          ),
      ],
      [
        "POST /api/admin/categories (site A Origin)",
        () =>
          adminRoot.post(
            "/api/admin/categories",
            { name: "CSRF Cat", slug: "csrf-cat", commission_rate_bps: 500 },
            { origin: originOf(SITE_A_HOST) }
          ),
      ],
      [
        "POST /api/admin/vouchers (missing Origin)",
        () =>
          adminRoot.post(
            "/api/admin/vouchers",
            { code: "CSRFVOUCHER", name: "CSRF", scope: "PLATFORM" },
            { origin: null }
          ),
      ],
      [
        "PATCH /api/admin/users/[id]/status (Sec-Fetch-Site: cross-site)",
        () =>
          adminRoot.patch(
            `/api/admin/users/${encodeURIComponent(buyer1.user.id)}/status`,
            { status: "LOCKED", reason: "CSRF" },
            { headers: { "sec-fetch-site": "cross-site" } }
          ),
      ],
    ];
    for (const [label, fn] of adminCsrfChecks) {
      const r = await fn();
      expectStatus(r, 403, `CSRF ${label}`);
      assert.equal(r.data?.error?.code, "BAD_ORIGIN", `CSRF ${label} expected BAD_ORIGIN`);
      assertNoInternalErrorLeak(r.text, `CSRF ${label}`);
    }
  });
});
