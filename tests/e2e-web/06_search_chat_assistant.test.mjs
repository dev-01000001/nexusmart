import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, originOf, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, gw, pollUntil, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  assertNoTokenLeak,
  demoCredentials,
  loginOn,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);
const FAKE_ID = "00000000-0000-4000-8000-000000000000";

function stripVietnameseDiacritics(input) {
  return String(input || "")
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/g, "d")
    .replace(/Đ/g, "D");
}

function hasVietnameseDiacritics(input) {
  return stripVietnameseDiacritics(input) !== String(input || "");
}

async function listShopProducts(shopId) {
  const res = await gw("GET", `/api/v1/products?shop_id=${encodeURIComponent(shopId)}&page=1&page_size=50`);
  assert.equal(res.status, 200, `GET /api/v1/products?shop_id=${shopId}: ${res.status} ${res.text}`);
  return (res.data?.items ?? []).filter((p) => !p.shop_id || p.shop_id === shopId);
}

describe("06. Site search, real-time chat (buyer + seller BFF) & AI assistant (SSE, review summary, handoff)", () => {
  let siteA;
  let siteB;
  let productsA;
  let productsB;
  let productIdsB;
  let targetProductA;
  let accentedQuery;
  let unaccentedQuery;
  let suggestPrefix;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    siteB = await resolveSite(SEEDED.saiGon.sub);
    [productsA, productsB] = await Promise.all([
      listShopProducts(siteA.shopId),
      listShopProducts(siteB.shopId),
    ]);
    assert.ok(productsA.length > 0, `shop ${siteA.shopId} must have seeded products`);
    assert.ok(productsB.length > 0, `shop ${siteB.shopId} must have seeded products`);

    productIdsB = new Set(productsB.map((p) => p.id));
    targetProductA =
      productsA.find((p) => hasVietnameseDiacritics(p.name)) ?? productsA[0];

    const words = String(targetProductA.name || "")
      .trim()
      .split(/\s+/)
      .filter(Boolean);
    accentedQuery = words.slice(0, Math.min(3, words.length)).join(" ") || "Áo thun";
    if (!hasVietnameseDiacritics(accentedQuery)) {
      accentedQuery = "Áo thun";
    }
    unaccentedQuery = stripVietnameseDiacritics(accentedQuery);
    suggestPrefix = words.slice(0, Math.min(2, words.length)).join(" ") || "Áo";
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("a) Site host search (bach-dang) scopes strictly to shopId, matches accented & unaccented queries, SSR /tim-kiem 200, root host 404", async () => {
    const anonA = new Browser("search-anon-a").at(SITE_A_HOST);
    const anonRoot = new Browser("search-anon-root").at(ROOT_HOST);

    // 1. Root host /api/search & /api/search/suggest must return 404.
    for (const path of [
      "/api/search",
      `/api/search?q=${encodeURIComponent(unaccentedQuery)}`,
      `/api/search/suggest?q=${encodeURIComponent(suggestPrefix)}`,
    ]) {
      const res = await anonRoot.get(path);
      assert.equal(res.status, 404, `GET ${ROOT_HOST}${path} must return 404, got ${res.status}`);
      assertNoInternalErrorLeak(res.text, `root ${path}`);
    }

    // 2. Site host GET /api/search with accented query (plus tampered shop_id=siteB.shopId ignored).
    const accentedRes = await pollUntil(
      `search on ${SITE_A_HOST} returns results for "${accentedQuery}"`,
      async () => {
        const r = await anonA.get(
          `/api/search?q=${encodeURIComponent(accentedQuery)}&shop_id=${encodeURIComponent(siteB.shopId)}&page=1&page_size=20`
        );
        const ok = r.status === 200 && Array.isArray(r.data?.items) && r.data.items.length > 0;
        return [ok, `status=${r.status} items=${r.data?.items?.length ?? -1}`, r];
      },
      { timeoutMs: 10_000, intervalMs: 500 }
    );
    const accentedData = expectStatus(accentedRes, 200, `GET ${SITE_A_HOST}/api/search (accented)`);
    assertNoTokenLeak(accentedRes.text, "GET /api/search (accented)");
    assertNoInternalErrorLeak(accentedRes.text, "GET /api/search (accented)");
    assert.ok(accentedData.items.length > 0, `accented query "${accentedQuery}" must return >= 1 item`);
    assert.ok(accentedData.total >= accentedData.items.length, "search total must be >= items.length");
    assert.ok(accentedData.facets && Array.isArray(accentedData.facets.categories), "search response must include facets.categories");

    for (const item of accentedData.items) {
      assert.equal(
        item.shopId,
        siteA.shopId,
        `site ${SITE_A_HOST} search leaked foreign shop item ${item.id} (shopId=${item.shopId})`
      );
      assert.equal(productIdsB.has(item.id), false, `site ${SITE_A_HOST} search leaked sai-gon product ${item.id}`);
      assert.notEqual(item.slug, SEEDED.saiGon.productSlug, "site search leaked sai-gon product slug");
    }

    // 3. Unaccented query must also return 200 with overlapping bach-dang products and zero sai-gon leaks.
    const unaccentedRes = await anonA.get(
      `/api/search?q=${encodeURIComponent(unaccentedQuery)}&shop_id=${encodeURIComponent(siteB.shopId)}&page=1&page_size=20`
    );
    const unaccentedData = expectStatus(unaccentedRes, 200, `GET ${SITE_A_HOST}/api/search (unaccented)`);
    assertNoInternalErrorLeak(unaccentedRes.text, "GET /api/search (unaccented)");
    assert.ok(unaccentedData.items.length > 0, `unaccented query "${unaccentedQuery}" must return >= 1 item`);

    const accentedIds = new Set(accentedData.items.map((it) => it.id));
    const unaccentedIds = new Set(unaccentedData.items.map((it) => it.id));
    for (const item of unaccentedData.items) {
      assert.equal(item.shopId, siteA.shopId, `unaccented search leaked foreign shop item ${item.id}`);
      assert.equal(productIdsB.has(item.id), false, `unaccented search leaked sai-gon product ${item.id}`);
    }
    const overlap = [...accentedIds].filter((id) => unaccentedIds.has(id));
    assert.ok(
      overlap.length > 0,
      `accented ("${accentedQuery}") and unaccented ("${unaccentedQuery}") queries must match overlapping bach-dang products`
    );

    // 4. GET /api/search/suggest scoped strictly to bach-dang (tampered shop_id ignored).
    const suggestRes = await anonA.get(
      `/api/search/suggest?q=${encodeURIComponent(suggestPrefix)}&shop_id=${encodeURIComponent(siteB.shopId)}`
    );
    const suggestData = expectStatus(suggestRes, 200, `GET ${SITE_A_HOST}/api/search/suggest`);
    assertNoTokenLeak(suggestRes.text, "GET /api/search/suggest");
    assertNoInternalErrorLeak(suggestRes.text, "GET /api/search/suggest");
    assert.ok(Array.isArray(suggestData.suggestions), "suggest must return suggestions array");
    for (const s of suggestData.suggestions) {
      if (s.productId) {
        assert.equal(productIdsB.has(s.productId), false, `suggest leaked sai-gon productId ${s.productId}`);
      }
      if (s.slug) {
        assert.notEqual(s.slug, SEEDED.saiGon.productSlug, "suggest leaked sai-gon slug");
      }
    }

    // 5. SSR GET /tim-kiem?q=... renders 200 on bach-dang host.
    const ssrRes = await anonA.page(`/tim-kiem?q=${encodeURIComponent(accentedQuery)}`);
    expectStatus(ssrRes, 200, `GET ${SITE_A_HOST}/tim-kiem?q=...`);
    assert.ok(ssrRes.text.includes(SEEDED.bachDang.shopName), `/tim-kiem must render ${SEEDED.bachDang.shopName}`);
    assertNoInternalErrorLeak(ssrRes.text, `GET ${SITE_A_HOST}/tim-kiem`);
  });

  it("b) Site host chat (bach-dang) & seller chat BFF on root: 401/404 guards, conversation/messages/ws-ticket, CSRF, cross-buyer & cross-seller isolation, auto-reply", async () => {
    const anonA = new Browser("chat-anon-a").at(SITE_A_HOST);
    const anonRoot = new Browser("chat-anon-root").at(ROOT_HOST);

    // 1. Anon on bach-dang -> 401 on chat endpoints.
    const anonSiteChecks = [
      ["GET /api/chat/conversations", () => anonA.get("/api/chat/conversations")],
      ["POST /api/chat/conversations", () => anonA.post("/api/chat/conversations", {})],
      ["POST /api/chat/ws-ticket", () => anonA.post("/api/chat/ws-ticket", {})],
      ["GET /api/chat/conversations/[id]/messages", () => anonA.get(`/api/chat/conversations/${FAKE_ID}/messages`)],
      [
        "POST /api/chat/conversations/[id]/messages",
        () => anonA.post(`/api/chat/conversations/${FAKE_ID}/messages`, { kind: "text", body: "anon" }),
      ],
    ];
    for (const [label, fn] of anonSiteChecks) {
      const res = await fn();
      expectStatus(res, 401, `anon ${SITE_A_HOST} ${label}`);
      assertNoInternalErrorLeak(res.text, `anon ${label}`);
    }

    // 2. Root host /api/chat/* -> 404; site host /api/seller/chat/* -> 404.
    const rootBlockedChat = [
      ["GET", "/api/chat/conversations"],
      ["POST", "/api/chat/conversations", {}],
      ["GET", `/api/chat/conversations/${FAKE_ID}/messages`],
      ["POST", `/api/chat/conversations/${FAKE_ID}/messages`, { kind: "text", body: "root" }],
      ["POST", "/api/chat/ws-ticket", {}],
    ];
    for (const [method, path, json] of rootBlockedChat) {
      const res = await anonRoot.request(method, path, { json });
      assert.equal(res.status, 404, `${method} ${ROOT_HOST}${path} must return 404, got ${res.status}`);
    }
    for (const path of ["/api/seller/chat/conversations", "/api/seller/chat/auto-reply", "/api/seller/chat/ws-ticket"]) {
      const res = await anonA.get(path);
      assert.equal(res.status, 404, `GET ${SITE_A_HOST}${path} must return 404, got ${res.status}`);
    }

    // 3. Register buyer1 & buyer2 on bach-dang, plus login sellerA (bach-dang), sellerHanoi, buyer1 on sai-gon & root.
    const buyer1Browser = new Browser("chat-buyer1");
    const buyer1A = buyer1Browser.at(SITE_A_HOST);
    const buyer1B = buyer1Browser.at(SITE_B_HOST);
    const buyer1Root = buyer1Browser.at(ROOT_HOST);

    const buyer2Browser = new Browser("chat-buyer2");
    const buyer2A = buyer2Browser.at(SITE_A_HOST);

    const sellerRoot = new Browser("chat-seller-a").at(ROOT_HOST);
    const sellerHanoiRoot = new Browser("chat-seller-hanoi").at(ROOT_HOST);

    let origAutoReply = null;
    let sellerLoggedIn = false;

    try {
      const buyer1 = await registerBuyer(buyer1A, "chat-b1");
      const buyer1Secrets = [buyer1.authCookies.access.value, buyer1.authCookies.refresh.value];
      await registerBuyer(buyer2A, "chat-b2");

      // Logged-in buyer on ROOT_HOST still gets 404 on /api/chat/* and 403 on /api/seller/chat/*.
      await loginOn(buyer1Root, buyer1.creds.email, buyer1.creds.password, "chat-b1-root");
      expectStatus(await buyer1Root.get("/api/chat/conversations"), 404, "logged-in buyer GET ROOT/api/chat/conversations");
      expectStatus(await buyer1Root.post("/api/chat/ws-ticket", {}), 404, "logged-in buyer POST ROOT/api/chat/ws-ticket");
      expectStatus(await buyer1Root.get("/api/seller/chat/conversations"), 403, "buyer GET /api/seller/chat/conversations");
      expectStatus(await buyer1Root.get("/api/seller/chat/auto-reply"), 403, "buyer GET /api/seller/chat/auto-reply");

      // 4. Buyer1 creates conversation on bach-dang (passing foreign shop_id must be ignored -> locked to siteA.shopId).
      const createRes = await buyer1A.post("/api/chat/conversations", { shop_id: siteB.shopId });
      const createData = expectStatus(createRes, [200, 201], `POST ${SITE_A_HOST}/api/chat/conversations`);
      assertNoTokenLeak(createRes.text, "POST /api/chat/conversations", buyer1Secrets);
      assertNoInternalErrorLeak(createRes.text, "POST /api/chat/conversations");
      const convId = createData.conversation?.id;
      assert.ok(convId, "POST /api/chat/conversations must return conversation.id");
      assert.equal(
        createData.conversation.shop_id,
        siteA.shopId,
        "conversation must be locked strictly to bach-dang shopId"
      );

      // 5. Buyer1 sends message via BFF (and invalid image URL is rejected 400).
      const buyerMsgBody = `Xin chào Xưởng Dệt Bạch Đằng kiểm thử E2E ${uid()}`;
      const sendRes = await buyer1A.post(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`, {
        kind: "text",
        body: buyerMsgBody,
        client_msg_id: `web-e2e-${uid()}`,
      });
      const sendData = expectStatus(sendRes, 201, `POST ${SITE_A_HOST}/api/chat/conversations/${convId}/messages`);
      assertNoTokenLeak(sendRes.text, "POST chat message", buyer1Secrets);
      assertNoInternalErrorLeak(sendRes.text, "POST chat message");
      assert.ok(sendData.message?.id, "sent message must return message.id");
      assert.equal(sendData.message.body, buyerMsgBody);

      const badImgMsg = await buyer1A.post(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`, {
        kind: "image",
        body: "javascript:alert(1)",
      });
      expectStatus(badImgMsg, 400, "chat message with javascript: image URL must return 400");

      // 6. Buyer1 lists conversations and fetches messages on bach-dang.
      const listConvRes = await buyer1A.get("/api/chat/conversations");
      const listConvData = expectStatus(listConvRes, 200, `GET ${SITE_A_HOST}/api/chat/conversations`);
      assertNoTokenLeak(listConvRes.text, "GET /api/chat/conversations", buyer1Secrets);
      assert.ok(Array.isArray(listConvData.conversations), "GET /api/chat/conversations must return conversations array");
      for (const c of listConvData.conversations) {
        assert.equal(c.shop_id, siteA.shopId, "buyer /api/chat/conversations leaked foreign shop conversation");
      }
      assert.ok(
        listConvData.conversations.some((c) => c.id === convId),
        `GET /api/chat/conversations must contain created conversation ${convId}`
      );

      const listMsgRes = await buyer1A.get(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`);
      const listMsgData = expectStatus(
        listMsgRes,
        200,
        `GET ${SITE_A_HOST}/api/chat/conversations/${convId}/messages`
      );
      assertNoTokenLeak(listMsgRes.text, "GET chat messages", buyer1Secrets);
      assert.ok(Array.isArray(listMsgData.messages), "GET messages must return messages array");
      assert.ok(
        listMsgData.messages.some((m) => m.id === sendData.message.id || m.body === buyerMsgBody),
        "GET chat messages must include buyer1's sent message"
      );

      // 7. Buyer1 obtains WS ticket on bach-dang.
      const ticketRes = await buyer1A.post("/api/chat/ws-ticket", {});
      const ticketData = expectStatus(ticketRes, 200, `POST ${SITE_A_HOST}/api/chat/ws-ticket`);
      assertNoTokenLeak(ticketRes.text, "POST /api/chat/ws-ticket", buyer1Secrets);
      assertNoInternalErrorLeak(ticketRes.text, "POST /api/chat/ws-ticket");
      assert.ok(typeof ticketData.ticket === "string" && ticketData.ticket.length >= 8, "ws-ticket must return non-empty ticket");
      assert.match(ticketData.ws_url, /^wss?:\/\/.*ticket=/i, `ws_url must be ws(s)://...ticket=..., got ${ticketData.ws_url}`);

      // 8. Cross-site CSRF checks -> 403 BAD_ORIGIN.
      const csrfOrigins = [originOf(SITE_B_HOST), originOf(ROOT_HOST), "https://evil.example", "null", null];
      for (const badOrigin of csrfOrigins) {
        const label = `origin=${String(badOrigin)}`;
        for (const [epLabel, fn] of [
          ["POST /api/chat/conversations", () => buyer1A.post("/api/chat/conversations", {}, { origin: badOrigin })],
          [
            "POST /api/chat/conversations/[id]/messages",
            () =>
              buyer1A.post(
                `/api/chat/conversations/${encodeURIComponent(convId)}/messages`,
                { kind: "text", body: "csrf" },
                { origin: badOrigin }
              ),
          ],
          ["POST /api/chat/ws-ticket", () => buyer1A.post("/api/chat/ws-ticket", {}, { origin: badOrigin })],
        ]) {
          const r = await fn();
          expectStatus(r, 403, `CSRF ${epLabel} (${label})`);
          assert.equal(r.data?.error?.code, "BAD_ORIGIN", `CSRF ${epLabel} (${label}) expected BAD_ORIGIN`);
          assertNoInternalErrorLeak(r.text, `CSRF ${epLabel} (${label})`);
        }
      }

      // 9. Cross-buyer isolation: buyer2 on bach-dang cannot read or send into buyer1's conversation.
      const b2Read = await buyer2A.get(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`);
      expectStatus(b2Read, [403, 404], "buyer2 GET buyer1 conversation messages");
      assert.equal(b2Read.text.includes(buyerMsgBody), false, "cross-buyer error leaked buyer1 message");
      assertNoInternalErrorLeak(b2Read.text, "buyer2 GET buyer1 chat");

      const b2Send = await buyer2A.post(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`, {
        kind: "text",
        body: "intruder message",
      });
      expectStatus(b2Send, [403, 404], "buyer2 POST buyer1 conversation messages");
      assertNoInternalErrorLeak(b2Send.text, "buyer2 POST buyer1 chat");

      // 10. Cross-site isolation for same buyer1 logged into sai-gon: bach-dang conversation is 404 on sai-gon.
      await loginOn(buyer1B, buyer1.creds.email, buyer1.creds.password, "chat-b1-site-b");
      const crossSiteRead = await buyer1B.get(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`);
      expectStatus(crossSiteRead, 404, `same buyer GET ${SITE_B_HOST}/api/chat/conversations/${convId}/messages`);
      assert.equal(crossSiteRead.text.includes(buyerMsgBody), false, "cross-site 404 leaked message");

      // 11. Seller of bach-dang on ROOT_HOST lists conversation, reads/replies, obtains WS ticket, and gets/updates auto-reply.
      const seller = await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "chat-seller-a");
      sellerLoggedIn = true;
      const sellerSecrets = [seller.authCookies.access.value, seller.authCookies.refresh.value];

      const sellerConvsRes = await sellerRoot.get("/api/seller/chat/conversations");
      const sellerConvs = expectStatus(sellerConvsRes, 200, "GET /api/seller/chat/conversations");
      assertNoTokenLeak(sellerConvsRes.text, "GET /api/seller/chat/conversations", sellerSecrets);
      assert.ok(Array.isArray(sellerConvs.conversations), "seller conversations must be an array");
      for (const c of sellerConvs.conversations) {
        assert.equal(c.shop_id, siteA.shopId, "seller conversations leaked another shop's thread");
      }
      assert.ok(
        sellerConvs.conversations.some((c) => c.id === convId),
        `seller /api/seller/chat/conversations must include buyer1's conversation ${convId}`
      );

      const sellerMsgsRes = await sellerRoot.get(`/api/seller/chat/conversations/${encodeURIComponent(convId)}/messages`);
      const sellerMsgs = expectStatus(sellerMsgsRes, 200, `GET /api/seller/chat/conversations/${convId}/messages`);
      assertNoTokenLeak(sellerMsgsRes.text, "GET seller chat messages", sellerSecrets);
      assert.ok(
        (sellerMsgs.messages ?? []).some((m) => m.body === buyerMsgBody),
        "seller must see buyer1's message in conversation"
      );

      const sellerReplyBody = `Shop Bạch Đằng đã nhận tin nhắn, hỗ trợ ngay ${uid()}`;
      const sellerSendRes = await sellerRoot.post(`/api/seller/chat/conversations/${encodeURIComponent(convId)}/messages`, {
        kind: "text",
        body: sellerReplyBody,
      });
      const sellerSendData = expectStatus(sellerSendRes, 201, `POST /api/seller/chat/conversations/${convId}/messages`);
      assertNoTokenLeak(sellerSendRes.text, "POST seller chat reply", sellerSecrets);
      assert.equal(sellerSendData.message?.body, sellerReplyBody);

      // Buyer1 sees seller's reply on bach-dang.
      const afterReplyRes = await buyer1A.get(`/api/chat/conversations/${encodeURIComponent(convId)}/messages`);
      const afterReply = expectStatus(afterReplyRes, 200, "buyer1 GET messages after seller reply");
      assert.ok(
        (afterReply.messages ?? []).some((m) => m.body === sellerReplyBody),
        "buyer1 must see seller's reply"
      );

      // Seller WS ticket.
      const sellerTicketRes = await sellerRoot.post("/api/seller/chat/ws-ticket", {});
      const sellerTicket = expectStatus(sellerTicketRes, 200, "POST /api/seller/chat/ws-ticket");
      assertNoTokenLeak(sellerTicketRes.text, "POST /api/seller/chat/ws-ticket", sellerSecrets);
      assert.ok(sellerTicket.ticket && /^wss?:\/\//i.test(sellerTicket.ws_url));

      // Seller auto-reply GET -> PUT -> GET (snapshot original and restore in finally for idempotency).
      const autoGetBefore = await sellerRoot.get("/api/seller/chat/auto-reply");
      origAutoReply = expectStatus(autoGetBefore, 200, "GET /api/seller/chat/auto-reply");
      assertNoTokenLeak(autoGetBefore.text, "GET /api/seller/chat/auto-reply", sellerSecrets);

      const testGreeting = `Cảm ơn bạn đã nhắn cho Bạch Đằng (E2E ${uid()})`;
      const autoPutRes = await sellerRoot.put("/api/seller/chat/auto-reply", {
        enabled: true,
        message: testGreeting,
      });
      const autoPutData = expectStatus(autoPutRes, 200, "PUT /api/seller/chat/auto-reply");
      assert.equal(autoPutData.enabled, true);
      assert.equal(autoPutData.message, testGreeting);

      const autoGetAfter = expectStatus(
        await sellerRoot.get("/api/seller/chat/auto-reply"),
        200,
        "GET /api/seller/chat/auto-reply after PUT"
      );
      assert.equal(autoGetAfter.enabled, true);
      assert.equal(autoGetAfter.message, testGreeting);

      // Seller CSRF check on PUT /api/seller/chat/auto-reply.
      const sellerCsrf = await sellerRoot.put(
        "/api/seller/chat/auto-reply",
        { enabled: false, message: "csrf" },
        { origin: originOf(SITE_A_HOST) }
      );
      expectStatus(sellerCsrf, 403, "CSRF PUT /api/seller/chat/auto-reply with site A Origin");
      assert.equal(sellerCsrf.data?.error?.code, "BAD_ORIGIN");

      // Cross-seller isolation: sellerHanoi cannot read or reply to bach-dang's conversation.
      await loginOn(
        sellerHanoiRoot,
        demoCredentials.sellerHanoi.email,
        demoCredentials.sellerHanoi.password,
        "chat-seller-hanoi"
      );
      expectStatus(
        await sellerHanoiRoot.get(`/api/seller/chat/conversations/${encodeURIComponent(convId)}/messages`),
        [403, 404],
        "unrelated sellerHanoi GET bach-dang conversation messages"
      );
      expectStatus(
        await sellerHanoiRoot.post(`/api/seller/chat/conversations/${encodeURIComponent(convId)}/messages`, {
          kind: "text",
          body: "cross-seller hijack",
        }),
        [403, 404],
        "unrelated sellerHanoi POST bach-dang conversation messages"
      );
    } finally {
      if (sellerLoggedIn && origAutoReply) {
        const restoreEnabled = Boolean(origAutoReply.enabled);
        const restoreMessage =
          origAutoReply.message || (restoreEnabled ? "Xin chào, gian hàng sẽ phản hồi sớm!" : "");
        await sellerRoot
          .put("/api/seller/chat/auto-reply", { enabled: restoreEnabled, message: restoreMessage })
          .catch(() => {});
      }
      await Promise.allSettled([
        buyer1A.post("/api/auth/logout", {}),
        buyer1B.post("/api/auth/logout", {}),
        buyer1Root.post("/api/auth/logout", {}),
        buyer2A.post("/api/auth/logout", {}),
        sellerRoot.post("/api/auth/logout", {}),
        sellerHanoiRoot.post("/api/auth/logout", {}),
      ]);
    }
  });

  it("c) Site host assistant (bach-dang): SSE streaming, review-summary, authenticated handoff, root host 404 & CSRF 403", async () => {
    const anonA = new Browser("assistant-anon-a").at(SITE_A_HOST);
    const anonRoot = new Browser("assistant-anon-root").at(ROOT_HOST);

    // 1. Root host /api/assistant/* and /api/products/[id]/review-summary must return 404.
    const rootBlocked = [
      ["POST", "/api/assistant/chat", { messages: [{ role: "user", content: "xin chào" }] }],
      ["POST", "/api/assistant/handoff", { reason: "hỗ trợ" }],
      ["GET", `/api/products/${encodeURIComponent(targetProductA.id)}/review-summary`],
    ];
    for (const [method, path, json] of rootBlocked) {
      const res = await anonRoot.request(method, path, { json });
      assert.equal(res.status, 404, `${method} ${ROOT_HOST}${path} must return 404, got ${res.status}`);
      assertNoInternalErrorLeak(res.text, `root ${method} ${path}`);
    }

    // 2. Anon SSE chat on bach-dang: 200 text/event-stream, contains event: token and event: done.
    const anonSseRes = await anonA.post(
      "/api/assistant/chat",
      {
        messages: [{ role: "user", content: "Cửa hàng có mẫu áo thun cotton nào không?" }],
        shop_id: siteB.shopId, // tampered shop_id must be ignored by BFF
      },
      { headers: { accept: "text/event-stream" } }
    );
    expectStatus(anonSseRes, 200, `POST ${SITE_A_HOST}/api/assistant/chat (anon SSE)`);
    assert.match(
      String(anonSseRes.headers["content-type"] || ""),
      /text\/event-stream/i,
      `expected text/event-stream, got ${anonSseRes.headers["content-type"]}`
    );
    assert.match(anonSseRes.text, /event:\s*token\b/, `SSE stream must contain event: token:\n${anonSseRes.text.slice(0, 600)}`);
    assert.match(anonSseRes.text, /event:\s*done\b/, `SSE stream must contain event: done:\n${anonSseRes.text.slice(0, 600)}`);
    assertNoTokenLeak(anonSseRes.text, "POST /api/assistant/chat (anon)");
    assertNoInternalErrorLeak(anonSseRes.text, "POST /api/assistant/chat (anon)");

    // 3. GET /api/products/[id]/review-summary on bach-dang -> 200.
    const revRes = await anonA.get(`/api/products/${encodeURIComponent(targetProductA.id)}/review-summary`);
    const revData = expectStatus(
      revRes,
      200,
      `GET ${SITE_A_HOST}/api/products/${targetProductA.id}/review-summary`
    );
    assertNoTokenLeak(revRes.text, "GET /api/products/[id]/review-summary");
    assertNoInternalErrorLeak(revRes.text, "GET /api/products/[id]/review-summary");
    assert.equal(revData.productId, targetProductA.id);
    assert.equal(typeof revData.available, "boolean");
    assert.equal(typeof revData.reviewCount, "number");
    assert.ok(Array.isArray(revData.pros), "review-summary pros must be an array");
    assert.ok(Array.isArray(revData.cons), "review-summary cons must be an array");

    // 4. Anon POST /api/assistant/handoff -> 401.
    const anonHandoff = await anonA.post("/api/assistant/handoff", {
      messages: [{ role: "user", content: "Tôi muốn gặp nhân viên" }],
      reason: "Cần hỗ trợ trực tiếp",
    });
    expectStatus(anonHandoff, 401, `anon POST ${SITE_A_HOST}/api/assistant/handoff`);
    assertNoInternalErrorLeak(anonHandoff.text, "anon POST /api/assistant/handoff");

    // 5. Authenticated buyer SSE chat + handoff + cross-host CSRF checks.
    const buyerBrowser = new Browser("assistant-buyer");
    const buyerA = buyerBrowser.at(SITE_A_HOST);
    try {
      const buyer = await registerBuyer(buyerA, "asst-buyer");
      const secrets = [buyer.authCookies.access.value, buyer.authCookies.refresh.value];

      const authSseRes = await buyerA.post(
        "/api/assistant/chat",
        {
          messages: [{ role: "user", content: "Chính sách đổi trả hàng của cửa hàng như thế nào?" }],
        },
        { headers: { accept: "text/event-stream" } }
      );
      expectStatus(authSseRes, 200, `POST ${SITE_A_HOST}/api/assistant/chat (authed SSE)`);
      assert.match(String(authSseRes.headers["content-type"] || ""), /text\/event-stream/i);
      assert.match(authSseRes.text, /event:\s*token\b/, "authed SSE stream must contain event: token");
      assert.match(authSseRes.text, /event:\s*done\b/, "authed SSE stream must contain event: done");
      assertNoTokenLeak(authSseRes.text, "POST /api/assistant/chat (authed)", secrets);
      assertNoInternalErrorLeak(authSseRes.text, "POST /api/assistant/chat (authed)");

      const handoffRes = await buyerA.post("/api/assistant/handoff", {
        messages: [
          { role: "user", content: "Tôi muốn đổi size áo thun đã đặt" },
          { role: "assistant", content: "Mình có thể chuyển bạn tới nhân viên gian hàng ngay." },
        ],
        reason: "Cần nhân viên gian hàng hỗ trợ đổi size",
      });
      const handoffData = expectStatus(handoffRes, 200, `POST ${SITE_A_HOST}/api/assistant/handoff`);
      assertNoTokenLeak(handoffRes.text, "POST /api/assistant/handoff", secrets);
      assertNoInternalErrorLeak(handoffRes.text, "POST /api/assistant/handoff");
      assert.ok(
        typeof handoffData.summary === "string" && handoffData.summary.trim().length > 0,
        "handoff must return non-empty summary"
      );
      assert.ok(
        (typeof handoffData.conversation_id === "string" && handoffData.conversation_id.trim().length > 0) ||
          (typeof handoffData.ticket_id === "string" && handoffData.ticket_id.trim().length > 0),
        `handoff must create/link a chat conversation_id or ticket_id, got ${handoffRes.text}`
      );

      // 6. Cross-host CSRF rejected 403 BAD_ORIGIN on both /api/assistant/chat and /api/assistant/handoff.
      for (const badOrigin of [originOf(SITE_B_HOST), originOf(ROOT_HOST), "https://evil.example", "null", null]) {
        const label = `origin=${String(badOrigin)}`;
        const chatCsrf = await buyerA.post(
          "/api/assistant/chat",
          { messages: [{ role: "user", content: "csrf" }] },
          { origin: badOrigin }
        );
        expectStatus(chatCsrf, 403, `CSRF POST /api/assistant/chat (${label})`);
        assert.equal(chatCsrf.data?.error?.code, "BAD_ORIGIN");
        assertNoInternalErrorLeak(chatCsrf.text, `CSRF assistant chat (${label})`);

        const handoffCsrf = await buyerA.post(
          "/api/assistant/handoff",
          { reason: "csrf handoff" },
          { origin: badOrigin }
        );
        expectStatus(handoffCsrf, 403, `CSRF POST /api/assistant/handoff (${label})`);
        assert.equal(handoffCsrf.data?.error?.code, "BAD_ORIGIN");
        assertNoInternalErrorLeak(handoffCsrf.text, `CSRF assistant handoff (${label})`);
      }
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
