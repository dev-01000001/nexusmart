// Run: node --test web/src/lib/server/sessionRefresh.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CLIENT_SESSION_SYNC_COOLDOWN_MS,
  decodeJwtClaimsForRefreshCheck,
  isAccessTokenStaleForUser,
  shouldAttemptClientSessionSync,
  shouldRefreshSellerSession,
} from "./sessionRefresh.ts";

function makeUnsignedTestJwt(payload) {
  const header = Buffer.from(JSON.stringify({ alg: "HS256", typ: "JWT" })).toString("base64url");
  const body = Buffer.from(JSON.stringify(payload)).toString("base64url");
  return `${header}.${body}.test-sig`;
}

test("decodeJwtClaimsForRefreshCheck extracts role and shop_id and rejects malformed tokens", () => {
  const tok = makeUnsignedTestJwt({ sub: "u-1", role: "buyer", shop_id: "" });
  assert.deepEqual(decodeJwtClaimsForRefreshCheck(tok), { role: "buyer", shopId: "" });

  const sellerTok = makeUnsignedTestJwt({ sub: "u-1", role: "SELLER", shop_id: "shop-99" });
  assert.deepEqual(decodeJwtClaimsForRefreshCheck(sellerTok), { role: "seller", shopId: "shop-99" });

  assert.equal(decodeJwtClaimsForRefreshCheck(undefined), null);
  assert.equal(decodeJwtClaimsForRefreshCheck("not-a-jwt"), null);
  assert.equal(decodeJwtClaimsForRefreshCheck("a.!!!invalid!!!.c"), null);
});

test("isAccessTokenStaleForUser detects stale buyer JWT after admin approves shop", () => {
  const staleBuyerJwt = makeUnsignedTestJwt({ sub: "u-1", role: "buyer", shop_id: "" });
  const sellerJwtNoShop = makeUnsignedTestJwt({ sub: "u-1", role: "seller", shop_id: "" });
  const freshSellerJwt = makeUnsignedTestJwt({ sub: "u-1", role: "seller", shop_id: "shop-1" });

  // Admin approved shop -> /users/me returns role=seller, shop_id=shop-1 while cookie still has buyer JWT
  assert.equal(
    isAccessTokenStaleForUser(staleBuyerJwt, { role: "seller", shopId: "shop-1" }),
    true
  );
  // Seller token missing updated shop_id is also stale
  assert.equal(
    isAccessTokenStaleForUser(sellerJwtNoShop, { role: "seller", shopId: "shop-1" }),
    true
  );
  // Refreshed seller token matches DB user -> fresh
  assert.equal(
    isAccessTokenStaleForUser(freshSellerJwt, { role: "seller", shopId: "shop-1" }),
    false
  );
  // Normal buyer token with buyer user -> fresh
  assert.equal(isAccessTokenStaleForUser(staleBuyerJwt, { role: "buyer", shopId: "" }), false);
});

test("shouldRefreshSellerSession allows single refresh on stale claims/ACTIVE shop and never loops", () => {
  const staleBuyerJwt = makeUnsignedTestJwt({ sub: "u-1", role: "buyer", shop_id: "" });
  const freshSellerJwt = makeUnsignedTestJwt({ sub: "u-1", role: "seller", shop_id: "shop-1" });

  // Stale JWT vs DB seller -> refresh once
  assert.equal(
    shouldRefreshSellerSession({
      accessToken: staleBuyerJwt,
      hasRefreshToken: true,
      user: { role: "seller", shopId: "shop-1" },
      attempted: false,
    }),
    true
  );

  // Already attempted in this request -> NEVER loop
  assert.equal(
    shouldRefreshSellerSession({
      accessToken: staleBuyerJwt,
      hasRefreshToken: true,
      user: { role: "seller", shopId: "shop-1" },
      shopStatus: "ACTIVE",
      attempted: true,
    }),
    false
  );

  // Missing refresh token -> cannot refresh
  assert.equal(
    shouldRefreshSellerSession({
      accessToken: staleBuyerJwt,
      hasRefreshToken: false,
      user: { role: "seller", shopId: "shop-1" },
      attempted: false,
    }),
    false
  );

  // Buyer user with ACTIVE shop -> refresh once
  assert.equal(
    shouldRefreshSellerSession({
      accessToken: staleBuyerJwt,
      hasRefreshToken: true,
      user: { role: "buyer", shopId: "" },
      shopStatus: "ACTIVE",
      attempted: false,
    }),
    true
  );

  // Buyer user with PENDING shop -> do NOT refresh unnecessarily
  assert.equal(
    shouldRefreshSellerSession({
      accessToken: staleBuyerJwt,
      hasRefreshToken: true,
      user: { role: "buyer", shopId: "" },
      shopStatus: "PENDING",
      attempted: false,
    }),
    false
  );

  // Fresh seller token + ACTIVE shop -> already synced
  assert.equal(
    shouldRefreshSellerSession({
      accessToken: freshSellerJwt,
      hasRefreshToken: true,
      user: { role: "seller", shopId: "shop-1" },
      shopStatus: "ACTIVE",
      attempted: false,
    }),
    false
  );
});

test("shouldAttemptClientSessionSync enforces cooldown to prevent browser reload loops", () => {
  const t0 = 1_700_000_000_000;
  assert.equal(shouldAttemptClientSessionSync(null, "u-1", t0), true);
  assert.equal(
    shouldAttemptClientSessionSync({ userId: "u-1", atMs: t0 }, "u-1", t0 + 5_000),
    false
  );
  assert.equal(
    shouldAttemptClientSessionSync(
      { userId: "u-1", atMs: t0 },
      "u-1",
      t0 + CLIENT_SESSION_SYNC_COOLDOWN_MS
    ),
    true
  );
  // Different user gets a fresh attempt immediately
  assert.equal(
    shouldAttemptClientSessionSync({ userId: "u-1", atMs: t0 }, "u-2", t0 + 1_000),
    true
  );
  assert.equal(shouldAttemptClientSessionSync(null, "", t0), false);
});
