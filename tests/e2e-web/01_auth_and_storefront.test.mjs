import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { WebSession, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  assertAuthCookieHeaders,
  assertNoTokenLeak,
  cacheSkusFromPdpHtml,
  registerVerifyLoginBuyer,
} from "./lib/fixtures.mjs";

const HTML_HEADERS = { accept: "text/html,application/xhtml+xml" };

describe("01. Buyer Auth Lifecycle, Unauthenticated Page Guards & Seeded Storefront SSR", () => {
  before(async () => {
    await resetRateLimitKeys();
  });

  after(async () => {
    await resetRateLimitKeys();
  });
  it("(a) registers a fresh buyer, verifies Mailpit OTP, logs in with HttpOnly host-only cookies and no token leak, refreshes, and logs out", async () => {
    const { session, user, authCookies } = await registerVerifyLoginBuyer("auth");

    // Confirm refresh token rotation works via HttpOnly cookie without leaking tokens.
    const refreshRes = await session.post("/api/auth/refresh", {});
    const refreshData = expectStatus(refreshRes, 200, "POST /api/auth/refresh");
    assert.equal(refreshData.ok, true);
    const rotated = assertAuthCookieHeaders(refreshRes, "POST /api/auth/refresh");
    assertNoTokenLeak(refreshRes.text, "POST /api/auth/refresh", [
      rotated.access.value,
      rotated.refresh.value,
    ]);
    assert.notEqual(
      rotated.refresh.value,
      authCookies.refresh.value,
      "refresh token must rotate on /api/auth/refresh"
    );

    // Session must remain authenticated after rotation.
    const meAfterRefresh = await session.get("/api/auth/me");
    const meAfterRefreshData = expectStatus(meAfterRefresh, 200, "GET /api/auth/me after refresh");
    assert.equal(meAfterRefreshData.user?.id, user.id);

    // Logout must clear cookies and end the session.
    const logoutRes = await session.post("/api/auth/logout", {});
    const logoutData = expectStatus(logoutRes, 200, "POST /api/auth/logout");
    assert.equal(logoutData.ok, true);
    assert.equal(session.jar.size, 0, "logout must clear all auth cookies from jar");

    const meAfterLogout = await session.get("/api/auth/me");
    const meAfterLogoutData = expectStatus(meAfterLogout, 200, "GET /api/auth/me after logout");
    assert.equal(meAfterLogoutData.user, null, "GET /api/auth/me must return user=null after logout");
  });

  it("(j) redirects unauthenticated visitors away from protected pages to /dang-nhap", async () => {
    const anon = new WebSession("anon-guard");
    for (const path of ["/don-hang", "/tai-khoan", "/seller", "/admin"]) {
      const res = await anon.get(path, { headers: HTML_HEADERS });
      expectStatus(res, [302, 303, 307, 308], `Unauthenticated GET ${path}`);
      const location = res.headers.get("location") ?? "";
      assert.ok(
        location.includes("/dang-nhap"),
        `GET ${path} unauthenticated must redirect to /dang-nhap, got Location: ${location}`
      );
      assert.ok(
        location.includes("next="),
        `GET ${path} redirect should include next= query param, got Location: ${location}`
      );
    }
  });

  it("(j) renders seeded storefront pages with HTTP 200 and real Vietnamese catalog data", async () => {
    const anon = new WebSession("storefront");

    const homeRes = await anon.get("/", { headers: HTML_HEADERS });
    expectStatus(homeRes, 200, "GET /");
    assert.ok(homeRes.text.includes("NexusMart"), "home page must contain NexusMart");
    assert.ok(
      homeRes.text.includes("Xưởng Dệt Bạch Đằng") || homeRes.text.includes("Cotton Compact"),
      "home page must render real seeded shop/product content"
    );

    const catRes = await anon.get("/danh-muc?cat=ao-thun", { headers: HTML_HEADERS });
    expectStatus(catRes, 200, "GET /danh-muc?cat=ao-thun");
    assert.ok(
      catRes.text.includes("Áo thun") || catRes.text.includes("Cotton Compact"),
      "category page must render real seeded category/products"
    );

    const pdpRes = await anon.get("/san-pham/ao-thun-cotton-compact-trang", {
      headers: HTML_HEADERS,
    });
    expectStatus(pdpRes, 200, "GET /san-pham/ao-thun-cotton-compact-trang");
    assert.ok(pdpRes.text.includes("Cotton Compact"), "PDP must render seeded product title");
    assert.match(
      pdpRes.text,
      /skuId\\?":\\?"[0-9a-f-]{36}/i,
      "PDP must hydrate real backend SKU UUIDs from gateway"
    );
    // Reuse hydrated multi-shop SKUs in subsequent test files for this run.
    cacheSkusFromPdpHtml(pdpRes.text);

    const shopRes = await anon.get("/gian-hang/xuong-det-bach-dang", { headers: HTML_HEADERS });
    expectStatus(shopRes, 200, "GET /gian-hang/xuong-det-bach-dang");
    assert.ok(shopRes.text.includes("Xưởng Dệt Bạch Đằng"), "shop page must render real seeded shop name");

    const flashRes = await anon.get("/flash-sale", { headers: HTML_HEADERS });
    expectStatus(flashRes, 200, "GET /flash-sale");

    const voucherRes = await anon.get("/voucher", { headers: HTML_HEADERS });
    expectStatus(voucherRes, 200, "GET /voucher");
    assert.ok(
      voucherRes.text.includes("NEXUS30") || voucherRes.text.includes("FREESHIP"),
      "voucher page must render real seeded voucher codes"
    );
  });
});
