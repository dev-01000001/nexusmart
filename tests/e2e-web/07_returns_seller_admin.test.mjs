import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, originOf, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, normalizeStatus, uid } from "./lib/http.mjs";
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
  shipOrderToDelivered,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);
const FAKE_ID = "00000000-0000-4000-8000-000000000000";
const bare = (host) => host.replace(/:\d+$/, "");

/** Places a COD order on bach-dang, ships via seller BFF (PREPARING), and delivers via webhook. */
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

  const carrierCode = shippingChoices[0]?.carrier_code || "mock_express";
  const serviceCode = shippingChoices[0]?.service_code || "STANDARD";
  await shipOrderToDelivered({
    sellerRoot: sellerClient,
    buyerSite: buyerClient,
    orderId: order.id,
    carrierCode,
    serviceCode,
  });

  return order;
}

describe("07. Buyer returns, Seller real pages + AI BFF & Admin returns/config on ROOT_HOST", () => {
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

    buyer1 = await registerBuyer(buyer1A, "ret-b1", { verifyPhone: true });
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

  it("a) Buyer return lifecycle on bach-dang: COD -> ship/deliver -> POST/GET /api/orders/[id]/return, isolation, seller respond & buyer escalate", async () => {
    const buyerSecrets = [buyer1.authCookies.access.value, buyer1.authCookies.refresh.value];

    // 1. Place & deliver Order #1 (seller will ACCEPT_RETURN).
    const order1 = await createAndDeliverCodOrder({
      buyerClient: buyer1A,
      sellerClient: sellerRoot,
      address: buyer1Address,
      sku: skuA,
      tag: "approve",
    });
    approvedOrderId = order1.id;

    // Validation & CSRF negative checks before submitting valid return.
    const badEvidenceRes = await buyer1A.post(`/api/orders/${encodeURIComponent(order1.id)}/return`, {
      reason: "Sản phẩm bị lỗi đường may cần đổi trả",
      evidence_urls: ["javascript:alert(1)"],
    });
    expectStatus(badEvidenceRes, 400, "POST return with javascript: evidence URL must return 400");
    assertNoInternalErrorLeak(badEvidenceRes.text, "bad evidence return");

    const csrfRetRes = await buyer1A.post(
      `/api/orders/${encodeURIComponent(order1.id)}/return`,
      {
        reason: "Kiểm tra CSRF chéo domain",
        evidence_urls: [],
      },
      { origin: originOf(SITE_B_HOST) }
    );
    expectStatus(csrfRetRes, 403, "CSRF POST /api/orders/[id]/return from site B Origin");
    assert.equal(csrfRetRes.data?.error?.code, "BAD_ORIGIN");

    // 2. Buyer submits valid return request on bach-dang -> 200.
    const createRet1Res = await buyer1A.post(`/api/orders/${encodeURIComponent(order1.id)}/return`, {
      reason: `Sản phẩm giao sai kích cỡ, yêu cầu đổi trả E2E ${uid()}`,
      evidence_urls: ["https://example.com/e2e-return-1.jpg"],
    });
    const createRet1 = expectStatus(
      createRet1Res,
      200,
      `POST ${SITE_A_HOST}/api/orders/${order1.id}/return`
    );
    assertNoTokenLeak(createRet1Res.text, "POST /api/orders/[id]/return", buyerSecrets);
    assertNoInternalErrorLeak(createRet1Res.text, "POST /api/orders/[id]/return");
    approvedReturnId = createRet1.return_request_id || createRet1.returnRequestId;
    assert.ok(approvedReturnId, "POST /api/orders/[id]/return must return return_request_id");

    // 3. Buyer reads GET /api/orders/[id]/return on bach-dang -> 200.
    const getRet1Res = await buyer1A.get(`/api/orders/${encodeURIComponent(order1.id)}/return`);
    const getRet1 = expectStatus(
      getRet1Res,
      200,
      `GET ${SITE_A_HOST}/api/orders/${order1.id}/return`
    );
    assertNoTokenLeak(getRet1Res.text, "GET /api/orders/[id]/return", buyerSecrets);
    assert.equal(getRet1.return_request?.id, approvedReturnId);
    assert.equal(normalizeStatus(getRet1.return_request?.status), "PENDING");

    // 4. Cross-site (sai-gon) and root host isolation for same buyer1 -> 404.
    expectStatus(
      await buyer1B.get(`/api/orders/${encodeURIComponent(order1.id)}/return`),
      404,
      `same buyer GET ${SITE_B_HOST}/api/orders/${order1.id}/return`
    );
    expectStatus(
      await buyer1B.post(`/api/orders/${encodeURIComponent(order1.id)}/return`, {
        reason: "Cross-site return attempt",
        evidence_urls: [],
      }),
      404,
      `same buyer POST ${SITE_B_HOST}/api/orders/${order1.id}/return`
    );
    expectStatus(
      await buyer1Root.get(`/api/orders/${encodeURIComponent(order1.id)}/return`),
      404,
      `GET ${ROOT_HOST}/api/orders/${order1.id}/return`
    );

    // 5. Cross-buyer isolation: buyer2 on bach-dang gets 403/404.
    const buyer2A = new Browser("ret-buyer2").at(SITE_A_HOST);
    await registerBuyer(buyer2A, "ret-b2");
    try {
      const b2Get = await buyer2A.get(`/api/orders/${encodeURIComponent(order1.id)}/return`);
      expectStatus(b2Get, [403, 404], "buyer2 GET buyer1 order return");
      assertNoInternalErrorLeak(b2Get.text, "buyer2 GET order return");

      const b2Post = await buyer2A.post(`/api/orders/${encodeURIComponent(order1.id)}/return`, {
        reason: "IDOR return request by buyer2",
        evidence_urls: [],
      });
      expectStatus(b2Post, [403, 404], "buyer2 POST buyer1 order return");
    } finally {
      await buyer2A.post("/api/auth/logout", {}).catch(() => {});
    }

    // 6. Seller on ROOT_HOST views returns & accepts return #1; unrelated sellerHanoi gets 403/404.
    const sellerListRes = await sellerRoot.get("/api/seller/returns?page=1&page_size=50");
    const sellerList = expectStatus(sellerListRes, 200, "GET /api/seller/returns");
    assertNoInternalErrorLeak(sellerListRes.text, "GET /api/seller/returns");
    assert.ok(
      (sellerList.items ?? []).some((r) => r.id === approvedReturnId),
      `seller returns list must contain ${approvedReturnId}`
    );

    expectStatus(
      await sellerHanoiRoot.get(`/api/seller/returns/${encodeURIComponent(approvedReturnId)}`),
      [403, 404],
      "unrelated sellerHanoi GET bach-dang return"
    );
    expectStatus(
      await sellerHanoiRoot.post(`/api/seller/returns/${encodeURIComponent(approvedReturnId)}/respond`, {
        decision: "ACCEPT_RETURN",
        note: "cross-seller respond",
      }),
      [403, 404],
      "unrelated sellerHanoi respond bach-dang return"
    );

    const acceptRes = await sellerRoot.post(
      `/api/seller/returns/${encodeURIComponent(approvedReturnId)}/respond`,
      {
        decision: "ACCEPT_RETURN",
        note: "Đồng ý tiếp nhận yêu cầu đổi trả kiểm thử E2E",
        evidence_urls: [],
      }
    );
    const acceptData = expectStatus(
      acceptRes,
      200,
      `POST /api/seller/returns/${approvedReturnId}/respond (ACCEPT_RETURN)`
    );
    assertNoInternalErrorLeak(acceptRes.text, "POST /api/seller/returns/[id]/respond");
    assert.ok(
      ["AWAITING_SHIPMENT", "IN_TRANSIT", "REFUNDING", "REFUNDED"].includes(
        normalizeStatus(acceptData.return_request?.status)
      ),
      `expected accepted return status AWAITING_SHIPMENT/IN_TRANSIT/REFUNDING/REFUNDED, got ${acceptData.return_request?.status}`
    );

    // 7. Place & deliver Order #2 -> buyer submits return -> seller REJECTS -> buyer ESCALATES for Admin test.
    const order2 = await createAndDeliverCodOrder({
      buyerClient: buyer1A,
      sellerClient: sellerRoot,
      address: buyer1Address,
      sku: skuA,
      tag: "reject",
    });
    rejectedOrderId = order2.id;

    const createRet2 = expectStatus(
      await buyer1A.post(`/api/orders/${encodeURIComponent(order2.id)}/return`, {
        reason: `Sản phẩm có vết bẩn nhỏ, yêu cầu đổi trả E2E ${uid()}`,
        evidence_urls: ["https://example.com/e2e-return-2.jpg"],
      }),
      200,
      `POST ${SITE_A_HOST}/api/orders/${order2.id}/return (order2)`
    );
    rejectedReturnId = createRet2.return_request_id || createRet2.returnRequestId;
    assert.ok(rejectedReturnId, "order2 return_request_id must be present");

    const rejectRes = await sellerRoot.post(
      `/api/seller/returns/${encodeURIComponent(rejectedReturnId)}/respond`,
      {
        decision: "REJECT",
        note: "Sản phẩm đã cắt tem mác và có dấu hiệu sử dụng thực tế",
        evidence_urls: [],
      }
    );
    const rejectData = expectStatus(
      rejectRes,
      200,
      `POST /api/seller/returns/${rejectedReturnId}/respond (REJECT)`
    );
    assertNoInternalErrorLeak(rejectRes.text, "POST /api/seller/returns/[id]/respond (REJECT)");
    assert.equal(normalizeStatus(rejectData.return_request?.status), "REJECTED");

    // Cross-site escalation on sai-gon must be 404.
    expectStatus(
      await buyer1B.post(`/api/returns/${encodeURIComponent(rejectedReturnId)}/escalate`, {
        reason: "Cross-site escalate attempt should fail",
      }),
      404,
      `POST ${SITE_B_HOST}/api/returns/${rejectedReturnId}/escalate`
    );

    // Buyer escalates on bach-dang -> ESCALATED for Admin resolution.
    const escRes = await buyer1A.post(`/api/returns/${encodeURIComponent(rejectedReturnId)}/escalate`, {
      reason: "Sản phẩm còn nguyên tem mác, đề nghị quản trị viên xem xét phân xử",
      evidence_urls: ["https://example.com/e2e-escalate.jpg"],
    });
    const escData = expectStatus(
      escRes,
      200,
      `POST ${SITE_A_HOST}/api/returns/${rejectedReturnId}/escalate`
    );
    assert.equal(normalizeStatus(escData.return_request?.status), "ESCALATED");
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
    const targetReturnId = approvedReturnId || FAKE_ID;
    const sellerProtectedCalls = [
      ["POST", "/api/seller/assistant/report", { question: "Doanh thu", period: "7d" }],
      ["POST", "/api/seller/assistant/describe-product", { name: "Áo thun test", category_name: "Áo thun" }],
      ["POST", "/api/seller/assistant/suggest-reply", { customer_message: "Còn size M không?" }],
      ["GET", "/api/seller/returns"],
      [
        "POST",
        `/api/seller/returns/${encodeURIComponent(targetReturnId)}/respond`,
        { decision: "ACCEPT_RETURN", note: "buyer hijack" },
      ],
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
        "POST /api/seller/returns/[id]/respond",
        () =>
          sellerRoot.post(
            `/api/seller/returns/${encodeURIComponent(targetReturnId)}/respond`,
            { decision: "ACCEPT_RETURN", note: "csrf" },
            { origin: null }
          ),
      ],
    ]) {
      const r = await fn();
      expectStatus(r, 403, `CSRF ${label}`);
      assert.equal(r.data?.error?.code, "BAD_ORIGIN");
    }
  });

  it("c) Admin returns & config on ROOT_HOST: SSR 200, RBAC for buyer/seller, returns list & resolve, categories/users/vouchers, CSRF", async () => {
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

    for (const path of ["/api/admin/returns", "/api/admin/categories", "/api/admin/users", "/api/admin/vouchers"]) {
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

    // 3. GET /api/admin/returns -> 200, find escalated return from order2 and resolve it.
    const returnsRes = await adminRoot.get("/api/admin/returns?status=ESCALATED&page=1&page_size=50");
    const returnsData = expectStatus(returnsRes, 200, "GET /api/admin/returns");
    assertNoTokenLeak(returnsRes.text, "GET /api/admin/returns");
    assertNoInternalErrorLeak(returnsRes.text, "GET /api/admin/returns");
    assert.ok(Array.isArray(returnsData.items), "GET /api/admin/returns must return items array");

    const escalated = returnsData.items.find(
      (r) =>
        (rejectedReturnId && r.id === rejectedReturnId) ||
        (rejectedOrderId && r.order_id === rejectedOrderId)
    );
    assert.ok(
      escalated?.id,
      `escalated return ${rejectedReturnId} (order ${rejectedOrderId}) must be visible in GET /api/admin/returns?status=ESCALATED`
    );
    assert.equal(normalizeStatus(escalated.status), "ESCALATED");

    // Resolve escalated return via Admin BFF (contract: { decision, note, return_fee_bearer }).
    const resolveRes = await adminRoot.post(`/api/admin/returns/${encodeURIComponent(escalated.id)}/resolve`, {
      decision: "REJECT",
      note: `Đã kiểm tra đầy đủ bằng chứng từ hai bên (E2E ${uid()})`,
    });
    const resolveData = expectStatus(
      resolveRes,
      200,
      `POST /api/admin/returns/${escalated.id}/resolve`
    );
    assertNoTokenLeak(resolveRes.text, "POST /api/admin/returns/[id]/resolve");
    assertNoInternalErrorLeak(resolveRes.text, "POST /api/admin/returns/[id]/resolve");
    assert.equal(
      normalizeStatus(resolveData.return_request?.status),
      "CLOSED",
      `resolved REJECT return status must be CLOSED, got ${resolveData.return_request?.status}`
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
        ["PLATFORM", "FREESHIP", "SHOP"].includes(String(v.scope || "").toUpperCase()),
        `admin /api/admin/vouchers returned unknown scope ${v.scope}`
      );
    }

    const platVouchersData = expectStatus(
      await adminRoot.get("/api/admin/vouchers?scope=PLATFORM"),
      200,
      "GET /api/admin/vouchers?scope=PLATFORM"
    );
    assert.ok(Array.isArray(platVouchersData.vouchers));
    for (const v of platVouchersData.vouchers) {
      assert.equal(
        String(v.scope || "").toUpperCase(),
        "PLATFORM",
        `GET /api/admin/vouchers?scope=PLATFORM leaked scope ${v.scope}`
      );
    }

    const shopVouchersData = expectStatus(
      await adminRoot.get("/api/admin/vouchers?scope=SHOP"),
      200,
      "GET /api/admin/vouchers?scope=SHOP"
    );
    assert.ok(Array.isArray(shopVouchersData.vouchers));
    for (const v of shopVouchersData.vouchers) {
      assert.equal(
        String(v.scope || "").toUpperCase(),
        "SHOP",
        `GET /api/admin/vouchers?scope=SHOP leaked scope ${v.scope}`
      );
    }

    expectStatus(
      await adminRoot.get("/api/admin/vouchers?scope=BOGUS"),
      400,
      "GET /api/admin/vouchers?scope=BOGUS must return 400"
    );

    // 5. CSRF enforced on admin writes -> 403 BAD_ORIGIN.
    const adminCsrfChecks = [
      [
        "POST /api/admin/returns/[id]/resolve (evil Origin)",
        () =>
          adminRoot.post(
            `/api/admin/returns/${encodeURIComponent(escalated.id)}/resolve`,
            { decision: "REJECT", note: "CSRF attempt" },
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
