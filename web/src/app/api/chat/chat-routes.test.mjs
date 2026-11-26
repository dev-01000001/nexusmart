// Run: node --test src/app/api/chat/chat-routes.test.mjs
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../../..");

const mockState = {
  accessToken: "user-token",
  headers: new Headers(),
  user: { id: "user-1", role: "buyer", shop_id: "", status: "ACTIVE" },
};
globalThis.__NM_CHAT_ROUTES_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_CHAT_ROUTES_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_CHAT_ROUTES_TEST__.accessToken;
          if (!tok) return undefined;
          if (name === "nm_at" || name === "__Host-nm_at") return { value: tok };
          return undefined;
        },
        set() { throw new Error("read-only cookies"); },
      };
    }
  `,
  "virtual:next-navigation": `
    export function notFound() { throw new Error("NEXT_NOT_FOUND"); }
    export function redirect(url) { throw new Error("NEXT_REDIRECT:" + url); }
  `,
};

function resolveTsFile(basePath) {
  for (const candidate of [
    `${basePath}.ts`,
    `${basePath}.tsx`,
    resolve(basePath, "index.ts"),
    resolve(basePath, "index.tsx"),
  ]) {
    if (existsSync(candidate)) return pathToFileURL(candidate).href;
  }
  return null;
}

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "react") return { shortCircuit: true, url: "virtual:react" };
    if (specifier === "next/headers") return { shortCircuit: true, url: "virtual:next-headers" };
    if (specifier === "next/navigation")
      return { shortCircuit: true, url: "virtual:next-navigation" };
    if (specifier.startsWith("@/")) {
      const resolved = resolveTsFile(resolve(WEB_SRC, specifier.slice(2)));
      if (resolved) return nextResolve(resolved, context);
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL?.startsWith("file:")
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const resolved = resolveTsFile(resolve(parentDir, specifier));
      if (resolved) return nextResolve(resolved, context);
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (url in VIRTUAL_MODULES) {
      return { shortCircuit: true, format: "module", source: VIRTUAL_MODULES[url] };
    }
    return nextLoad(url, context);
  },
});

const buyerMessages = await import("./conversations/[id]/messages/route.ts");
const sellerMessages = await import("../seller/chat/conversations/[id]/messages/route.ts");
const buyerConversations = await import("./conversations/route.ts");

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function makeNextReq(url, init = {}) {
  const req = new Request(url, init);
  return Object.assign(req, { nextUrl: new URL(url) });
}

const SITE_A_HOST = "shop-a.nexusmart.localhost";
const ROOT_HOST = "nexusmart.localhost";

function installChatGatewayMock(conversationsById) {
  const calls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({ method, pathname: url.pathname, search: url.search });

    if (url.pathname === "/api/v1/sites/by-host") {
      return jsonRes({
        site: {
          id: "site-a",
          shop_id: "shop-a",
          status: "PUBLISHED",
          primary_host: SITE_A_HOST,
        },
        shop: { status: "ACTIVE" },
      });
    }
    if (url.pathname === "/api/v1/users/me") {
      return jsonRes({ user: mockState.user });
    }
    if (url.pathname === "/api/v1/chat/conversations" && method === "POST") {
      return jsonRes({
        conversation: conversationsById["created-override"] ?? {
          id: "conv-new",
          buyer_id: mockState.user.id,
          shop_id: "shop-a",
        },
        created: true,
      });
    }
    const msgMatch = url.pathname.match(/^\/api\/v1\/chat\/conversations\/([^/]+)\/messages$/);
    if (msgMatch) {
      const convId = decodeURIComponent(msgMatch[1]);
      if (method === "GET") {
        const conv = conversationsById[convId];
        if (!conv) {
          return jsonRes({ error: { code: "NOT_FOUND", message: "conversation not found" } }, 404);
        }
        return jsonRes({
          conversation: conv,
          items: [
            {
              id: "m-1",
              conversation_id: convId,
              sender_id: "buyer-1",
              sender_role: "buyer",
              kind: "text",
              body: "Xin chào",
              created_at: "2026-11-21T07:00:00Z",
            },
          ],
          next_before: "",
          latest_cursor: "m-1",
          has_more: false,
        });
      }
      if (method === "POST") {
        return jsonRes(
          {
            message: {
              id: "m-sent",
              conversation_id: convId,
              sender_id: mockState.user.id,
              sender_role: mockState.user.role,
              kind: "text",
              body: "Đã gửi",
              created_at: "2026-11-21T07:01:00Z",
            },
            auto_reply: {
              id: "m-auto",
              conversation_id: convId,
              sender_id: "shop-b-bot",
              sender_role: "seller",
              kind: "text",
              body: "Auto reply Shop B",
              is_auto_reply: true,
              created_at: "2026-11-21T07:01:01Z",
            },
          },
          201
        );
      }
    }
    return jsonRes({ error: { code: "NOT_FOUND", message: `unmocked ${url.pathname}` } }, 404);
  };
  return {
    calls,
    restore() {
      globalThis.fetch = origFetch;
    },
  };
}

test("buyer chat POST blocks foreign shop conversation (404) without calling gateway POST, and allows own site with 1 check call", async () => {
  mockState.user = { id: "buyer-1", role: "buyer", shop_id: "", status: "ACTIVE" };
  const mock = installChatGatewayMock({
    "conv-shop-a": { id: "conv-shop-a", buyer_id: "buyer-1", shop_id: "shop-a" },
    "conv-shop-b": { id: "conv-shop-b", buyer_id: "buyer-1", shop_id: "shop-b" },
    "conv-empty-shop": { id: "conv-empty-shop", buyer_id: "buyer-1", shop_id: "" },
  });
  try {
    // 1. POST to Shop B's conversation from Shop A host -> 404, NEVER calls gateway POST.
    const foreignPost = makeNextReq(
      `http://${SITE_A_HOST}/api/chat/conversations/conv-shop-b/messages`,
      {
        method: "POST",
        headers: {
          host: SITE_A_HOST,
          origin: `http://${SITE_A_HOST}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ kind: "text", body: "Xin chào Shop B từ domain Shop A" }),
      }
    );
    const foreignRes = await buyerMessages.POST(foreignPost, {
      params: Promise.resolve({ id: "conv-shop-b" }),
    });
    assert.equal(foreignRes.status, 404);
    const chatCalls = mock.calls.filter((c) => c.pathname.startsWith("/api/v1/chat/"));
    assert.equal(
      chatCalls.some((c) => c.method === "POST"),
      false,
      "must not POST to gateway when conversation belongs to another shop"
    );
    assert.equal(chatCalls.length, 1, "expected at most 1 gateway check call on rejected POST");

    // 2. GET on conversation with empty shop_id fails closed -> 404.
    mock.calls.length = 0;
    const emptyShopGet = makeNextReq(
      `http://${SITE_A_HOST}/api/chat/conversations/conv-empty-shop/messages`,
      { headers: { host: SITE_A_HOST } }
    );
    const emptyShopRes = await buyerMessages.GET(emptyShopGet, {
      params: Promise.resolve({ id: "conv-empty-shop" }),
    });
    assert.equal(emptyShopRes.status, 404);

    // 3. GET on Shop A conversation -> 200 in 1 chat gateway call (no redundant check call).
    mock.calls.length = 0;
    const ownGet = makeNextReq(
      `http://${SITE_A_HOST}/api/chat/conversations/conv-shop-a/messages?limit=20`,
      { headers: { host: SITE_A_HOST } }
    );
    const ownGetRes = await buyerMessages.GET(ownGet, {
      params: Promise.resolve({ id: "conv-shop-a" }),
    });
    assert.equal(ownGetRes.status, 200);
    const ownGetChatCalls = mock.calls.filter((c) => c.pathname.startsWith("/api/v1/chat/"));
    assert.equal(ownGetChatCalls.length, 1);

    // 4. POST to Shop A conversation -> 201 with exactly 1 GET check + 1 POST.
    mock.calls.length = 0;
    const ownPost = makeNextReq(
      `http://${SITE_A_HOST}/api/chat/conversations/conv-shop-a/messages`,
      {
        method: "POST",
        headers: {
          host: SITE_A_HOST,
          origin: `http://${SITE_A_HOST}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ kind: "text", body: "Xin chào Shop A" }),
      }
    );
    const ownPostRes = await buyerMessages.POST(ownPost, {
      params: Promise.resolve({ id: "conv-shop-a" }),
    });
    assert.equal(ownPostRes.status, 201);
    const ownPostChatCalls = mock.calls.filter((c) => c.pathname.startsWith("/api/v1/chat/"));
    assert.equal(ownPostChatCalls.length, 2);
    assert.equal(ownPostChatCalls[0].method, "GET");
    assert.equal(ownPostChatCalls[0].search, "?limit=1");
    assert.equal(ownPostChatCalls[1].method, "POST");
  } finally {
    mock.restore();
  }
});

test("seller chat POST blocks foreign shop conversation (e.g. seller acting as buyer at Shop B) with 404 and allows own shop", async () => {
  mockState.user = { id: "seller-a", role: "seller", shop_id: "shop-a", status: "ACTIVE" };
  const mock = installChatGatewayMock({
    "conv-shop-a": { id: "conv-shop-a", buyer_id: "buyer-9", shop_id: "shop-a" },
    "conv-seller-a-buying-shop-b": {
      id: "conv-seller-a-buying-shop-b",
      buyer_id: "seller-a",
      shop_id: "shop-b",
    },
    "conv-empty-shop": { id: "conv-empty-shop", buyer_id: "buyer-9", shop_id: "" },
  });
  try {
    // 1. Seller A tries POST on conversation with Shop B -> 404, NEVER calls gateway POST.
    const foreignPost = makeNextReq(
      `http://${ROOT_HOST}/api/seller/chat/conversations/conv-seller-a-buying-shop-b/messages`,
      {
        method: "POST",
        headers: {
          host: ROOT_HOST,
          origin: `http://${ROOT_HOST}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ kind: "text", body: "Phản hồi nhầm shop" }),
      }
    );
    const foreignRes = await sellerMessages.POST(foreignPost, {
      params: Promise.resolve({ id: "conv-seller-a-buying-shop-b" }),
    });
    assert.equal(foreignRes.status, 404);
    const chatCalls = mock.calls.filter((c) => c.pathname.startsWith("/api/v1/chat/"));
    assert.equal(
      chatCalls.some((c) => c.method === "POST"),
      false,
      "seller BFF must not POST to gateway when conversation belongs to another shop"
    );
    assert.equal(chatCalls.length, 1);

    // 2. Seller GET on conversation with empty shop_id fails closed -> 404.
    mock.calls.length = 0;
    const emptyShopGet = makeNextReq(
      `http://${ROOT_HOST}/api/seller/chat/conversations/conv-empty-shop/messages`,
      { headers: { host: ROOT_HOST } }
    );
    const emptyShopRes = await sellerMessages.GET(emptyShopGet, {
      params: Promise.resolve({ id: "conv-empty-shop" }),
    });
    assert.equal(emptyShopRes.status, 404);

    // 3. Seller POST on own shop's conversation -> 201 with 1 check GET + 1 POST.
    mock.calls.length = 0;
    const ownPost = makeNextReq(
      `http://${ROOT_HOST}/api/seller/chat/conversations/conv-shop-a/messages`,
      {
        method: "POST",
        headers: {
          host: ROOT_HOST,
          origin: `http://${ROOT_HOST}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({ kind: "text", body: "Dạ shop hỗ trợ ngay ạ" }),
      }
    );
    const ownRes = await sellerMessages.POST(ownPost, {
      params: Promise.resolve({ id: "conv-shop-a" }),
    });
    assert.equal(ownRes.status, 201);
    const ownChatCalls = mock.calls.filter((c) => c.pathname.startsWith("/api/v1/chat/"));
    assert.equal(ownChatCalls.length, 2);
    assert.equal(ownChatCalls[0].method, "GET");
    assert.equal(ownChatCalls[0].search, "?limit=1");
    assert.equal(ownChatCalls[1].method, "POST");

    // 4. Seller GET on own shop's conversation -> 200 in 1 chat gateway call.
    mock.calls.length = 0;
    const ownGet = makeNextReq(
      `http://${ROOT_HOST}/api/seller/chat/conversations/conv-shop-a/messages`,
      { headers: { host: ROOT_HOST } }
    );
    const ownGetRes = await sellerMessages.GET(ownGet, {
      params: Promise.resolve({ id: "conv-shop-a" }),
    });
    assert.equal(ownGetRes.status, 200);
    assert.equal(mock.calls.filter((c) => c.pathname.startsWith("/api/v1/chat/")).length, 1);
  } finally {
    mock.restore();
  }
});

test("buyer chat POST /conversations fails closed (404) when returned conversation has missing shop_id", async () => {
  mockState.user = { id: "buyer-1", role: "buyer", shop_id: "", status: "ACTIVE" };
  const mock = installChatGatewayMock({
    "created-override": { id: "conv-no-shop", buyer_id: "buyer-1", shop_id: "" },
  });
  try {
    const req = makeNextReq(`http://${SITE_A_HOST}/api/chat/conversations`, {
      method: "POST",
      headers: {
        host: SITE_A_HOST,
        origin: `http://${SITE_A_HOST}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({ initial_message: "Chào shop" }),
    });
    const res = await buyerConversations.POST(req, {});
    assert.equal(res.status, 404);
  } finally {
    mock.restore();
  }
});
