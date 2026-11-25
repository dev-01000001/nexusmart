import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, config, originOf, siteHost, withPort } from "./lib/config.mjs";
import { Browser, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  demoCredentials,
  extractCanonicalHref,
  loginOn,
  pickSku,
  primaryHostOf,
  primarySiteHost,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const ROOT = config.siteRootDomain;
const HANOI_CUSTOM_BARE = SEEDED.haNoi.customDomain;
const HANOI_CUSTOM_3300 = `${HANOI_CUSTOM_BARE}:3300`;
const HANOI_CUSTOM_HOST = withPort(HANOI_CUSTOM_BARE);
const HANOI_SUB_BARE = `${SEEDED.haNoi.sub}.${ROOT}`;
const HANOI_SUB_3300 = `${HANOI_SUB_BARE}:3300`;
const HANOI_SUB_HOST = siteHost(SEEDED.haNoi.sub);

const DANANG_PENDING_BARE = SEEDED.daNang.pendingCustomDomain;
const DANANG_PENDING_3300 = `${DANANG_PENDING_BARE}:3300`;
const DANANG_SUB_HOST = siteHost(SEEDED.daNang.sub);

function findCookie(browser, host, names) {
  return browser.cookiesFor(host).find((c) => names.includes(c.name))?.value ?? "";
}

describe("05. Phase 5 custom domains (Host routing, 308 primary redirect, cookie isolation, SEO & BFF domains)", () => {
  let hanoiSite;
  let hanoiSku;

  before(async () => {
    await resetRateLimitKeys();
    hanoiSite = await resolveSite(SEEDED.haNoi.sub);
    hanoiSku = await pickSku(hanoiSite.shopId, { minStock: 1 });
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("a) ACTIVE custom domain renders Ha Noi site, scopes vouchers, and blocks seller/admin/internal routes with 404", async () => {
    const customHosts = [...new Set([HANOI_CUSTOM_3300, HANOI_CUSTOM_HOST])];
    assert.equal(primaryHostOf(SEEDED.haNoi, ROOT), HANOI_CUSTOM_BARE);
    assert.equal(primarySiteHost(SEEDED.haNoi), HANOI_CUSTOM_HOST);

    for (const host of customHosts) {
      const client = new Browser(`custom-hanoi-${host}`).at(host);

      const home = await client.page("/");
      expectStatus(home, 200, `GET ${host}/`);
      assert.ok(home.text.includes(SEEDED.haNoi.shopName), `${host}/ must render ${SEEDED.haNoi.shopName}`);
      assert.ok(
        home.text.includes(`data-site-theme-mode="${SEEDED.haNoi.theme.mode}"`) &&
          home.text.includes(SEEDED.haNoi.theme.bg) &&
          home.text.includes(SEEDED.haNoi.theme.accent),
        `${host}/ must render Ha Noi theme tokens (${SEEDED.haNoi.theme.mode}, ${SEEDED.haNoi.theme.bg}, ${SEEDED.haNoi.theme.accent})`
      );

      const pdp = await client.page(`/san-pham/${SEEDED.haNoi.productSlug}`);
      expectStatus(pdp, 200, `GET ${host}/san-pham/${SEEDED.haNoi.productSlug}`);
      assert.ok(pdp.text.includes(SEEDED.haNoi.shopName), `${host} PDP must render ${SEEDED.haNoi.shopName}`);

      const foreignPdp = await client.page(`/san-pham/${SEEDED.bachDang.productSlug}`);
      assert.equal(
        foreignPdp.status,
        404,
        `Foreign shop PDP on ${host} must return 404, got ${foreignPdp.status}`
      );

      const vRes = await client.get("/api/vouchers");
      const vData = expectStatus(vRes, 200, `GET ${host}/api/vouchers`);
      const vouchers = vData.vouchers ?? [];
      assert.ok(vouchers.length > 0, `${host}/api/vouchers must return Ha Noi shop vouchers`);
      for (const v of vouchers) {
        assert.equal(
          String(v.scope).toUpperCase(),
          "SHOP",
          `${host}/api/vouchers leaked non-SHOP voucher ${v.code} (${v.scope})`
        );
        assert.equal(
          v.shop_id,
          hanoiSite.shopId,
          `${host}/api/vouchers leaked foreign shop voucher ${v.code}`
        );
      }
      assert.ok(
        vouchers.some((v) => String(v.code).toUpperCase() === SEEDED.haNoi.voucherCode),
        `${host}/api/vouchers must include ${SEEDED.haNoi.voucherCode}`
      );

      const blockedPaths = [
        "/seller",
        "/seller/site",
        "/admin",
        "/admin/sites",
        "/api/seller/site",
        "/api/seller/site/domains",
        "/api/admin/sites",
        "/s",
        `/s/${HANOI_CUSTOM_BARE}`,
        `/s/${HANOI_CUSTOM_BARE}/san-pham`,
      ];
      for (const path of blockedPaths) {
        const res = await client.get(path, { headers: { accept: "*/*" } });
        assert.equal(res.status, 404, `Blocked path ${host}${path} must return 404, got ${res.status}`);
      }
    }
  });

  it("b) Subdomain page requests 308 to ACTIVE custom primary host preserving scheme, port, path & query", async () => {
    const sub3300 = new Browser("hanoi-sub-3300").at(HANOI_SUB_3300);

    const rootRedir = await sub3300.page("/");
    assert.equal(rootRedir.status, 308, `GET ${HANOI_SUB_3300}/ must 308, got ${rootRedir.status}`);
    assert.match(rootRedir.location, /^https?:\/\//i, `308 Location must be absolute: ${rootRedir.location}`);
    const rootLoc = new URL(rootRedir.location);
    assert.equal(rootLoc.protocol, `${config.webProtocol}:`, `308 must preserve scheme: ${rootRedir.location}`);
    assert.equal(rootLoc.host, HANOI_CUSTOM_3300, `308 must target ${HANOI_CUSTOM_3300}: ${rootRedir.location}`);
    assert.equal(rootLoc.pathname, "/", `308 pathname must be /: ${rootRedir.location}`);
    assert.equal(rootLoc.search, "", `308 search must be empty: ${rootRedir.location}`);
    assert.equal(
      rootLoc.href,
      `${config.webProtocol}://${HANOI_CUSTOM_3300}/`,
      `308 href mismatch: ${rootRedir.location}`
    );

    const queryRedir = await sub3300.page("/san-pham?q=jogger");
    assert.equal(
      queryRedir.status,
      308,
      `GET ${HANOI_SUB_3300}/san-pham?q=jogger must 308, got ${queryRedir.status}`
    );
    assert.match(queryRedir.location, /^https?:\/\//i, `308 Location must be absolute: ${queryRedir.location}`);
    const queryLoc = new URL(queryRedir.location);
    assert.equal(queryLoc.protocol, `${config.webProtocol}:`, `308 must preserve scheme: ${queryRedir.location}`);
    assert.equal(queryLoc.host, HANOI_CUSTOM_3300, `308 must preserve port :3300: ${queryRedir.location}`);
    assert.equal(queryLoc.pathname, "/san-pham", `308 must preserve pathname: ${queryRedir.location}`);
    assert.equal(queryLoc.search, "?q=jogger", `308 must preserve query string: ${queryRedir.location}`);
    assert.equal(
      queryLoc.href,
      `${config.webProtocol}://${HANOI_CUSTOM_3300}/san-pham?q=jogger`,
      `308 href mismatch: ${queryRedir.location}`
    );

    // Also verify default siteHost("ha-noi") redirects 308 to hanoi-knit.verified.test.
    const subDefault = new Browser("hanoi-sub-default").at(HANOI_SUB_HOST);
    const defaultRedir = await subDefault.page("/");
    assert.equal(defaultRedir.status, 308, `GET ${HANOI_SUB_HOST}/ must 308, got ${defaultRedir.status}`);
    const defaultLoc = new URL(defaultRedir.location, originOf(HANOI_SUB_HOST));
    assert.equal(
      defaultLoc.hostname.toLowerCase(),
      HANOI_CUSTOM_BARE,
      `GET ${HANOI_SUB_HOST}/ must 308 to ${HANOI_CUSTOM_BARE}, got ${defaultRedir.location}`
    );
    assert.equal(defaultLoc.pathname, "/", `GET ${HANOI_SUB_HOST}/ 308 path: ${defaultRedir.location}`);
    if (config.hostPort) {
      assert.equal(defaultLoc.port, config.hostPort, `308 must preserve HOST_PORT: ${defaultRedir.location}`);
    }
  });

  it("c) Subdomain /api/cart (anon -> 401, authed -> 200) and /api/auth/me are NEVER 308-redirected", async () => {
    const browser = new Browser("hanoi-sub-api");
    const subClient = browser.at(HANOI_SUB_3300);

    try {
      const anonCart = await subClient.get("/api/cart");
      assert.notEqual(anonCart.status, 308, `anon GET ${HANOI_SUB_3300}/api/cart must not 308`);
      expectStatus(anonCart, 401, `anon GET ${HANOI_SUB_3300}/api/cart`);

      const anonMe = await subClient.get("/api/auth/me");
      assert.notEqual(anonMe.status, 308, `anon GET ${HANOI_SUB_3300}/api/auth/me must not 308`);
      expectStatus(anonMe, [200, 401], `anon GET ${HANOI_SUB_3300}/api/auth/me`);
      if (anonMe.status === 200) {
        assert.equal(anonMe.data?.user ?? null, null, "anon /api/auth/me must return user=null");
      }

      const buyer = await registerBuyer(subClient, "sub-no308");

      const authedMe = await subClient.get("/api/auth/me");
      assert.notEqual(authedMe.status, 308, `authed GET ${HANOI_SUB_3300}/api/auth/me must not 308`);
      const meData = expectStatus(authedMe, 200, `authed GET ${HANOI_SUB_3300}/api/auth/me`);
      assert.equal(meData.user?.id, buyer.user.id);

      const authedCart = await subClient.get("/api/cart");
      assert.notEqual(authedCart.status, 308, `authed GET ${HANOI_SUB_3300}/api/cart must not 308`);
      expectStatus(authedCart, 200, `authed GET ${HANOI_SUB_3300}/api/cart`);
    } finally {
      await subClient.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("d) PENDING custom domain (danang.pending.test) returns 404 on pages & APIs while da-nang subdomain stays 200", async () => {
    for (const host of [DANANG_PENDING_BARE, DANANG_PENDING_3300]) {
      const client = new Browser(`pending-${host}`).at(host);
      for (const path of ["/", "/san-pham", "/api/auth/me", "/api/cart"]) {
        const res = await client.get(path, { headers: { accept: "*/*" } });
        assert.equal(
          res.status,
          404,
          `PENDING custom host ${host}${path} must return 404, got ${res.status}`
        );
      }
    }

    const daNangClient = new Browser("danang-sub").at(DANANG_SUB_HOST);
    const daNangHome = await daNangClient.page("/");
    assert.notEqual(daNangHome.status, 308, `${DANANG_SUB_HOST}/ must not 308 to a PENDING custom domain`);
    expectStatus(daNangHome, 200, `GET ${DANANG_SUB_HOST}/`);
    assert.ok(daNangHome.text.includes(SEEDED.daNang.shopName), `${DANANG_SUB_HOST}/ must render ${SEEDED.daNang.shopName}`);

    const daNangPdp = await daNangClient.page(`/san-pham/${SEEDED.daNang.productSlug}`);
    expectStatus(daNangPdp, 200, `GET ${DANANG_SUB_HOST}/san-pham/${SEEDED.daNang.productSlug}`);
  });

  it("e) Unknown and malformed custom hosts return 404 for / and /api/cart", async () => {
    const badHosts = [
      "unknown-custom.verified.test",
      "not-found.example.test",
      "evil.local",
      "evil.internal",
      "bad$host.test",
      "*.hanoi-knit.verified.test",
    ];
    for (const host of badHosts) {
      const client = new Browser(`bad-custom-${host}`).at(host);
      for (const path of ["/", "/api/cart"]) {
        const res = await client.get(path, { headers: { accept: "*/*" } });
        assert.equal(res.status, 404, `Bad/unknown host ${host}${path} must return 404, got ${res.status}`);
      }
    }
  });

  it("f) Host-only cookies do not leak between custom host and subdomain; cross-host CSRF rejected with 403 BAD_ORIGIN", async () => {
    const browser = new Browser("custom-sub-isolation");
    const customClient = browser.at(HANOI_CUSTOM_3300);
    const subClient = browser.at(HANOI_SUB_3300);

    try {
      // registerBuyer asserts HttpOnly, SameSite=Lax, Path=/, and NO Domain= attribute.
      const buyer = await registerBuyer(customClient, "custom-cookie");

      assert.ok(
        browser.cookiesFor(HANOI_CUSTOM_3300).length >= 2,
        `browser must hold >= 2 host-only cookies for ${HANOI_CUSTOM_3300}`
      );
      assert.equal(
        browser.cookiesFor(HANOI_SUB_3300).length,
        0,
        `cookies of ${HANOI_CUSTOM_3300} must NEVER match ${HANOI_SUB_3300}`
      );
      assert.equal(
        browser.cookiesFor(ROOT_HOST).length,
        0,
        `cookies of ${HANOI_CUSTOM_3300} must NEVER match ${ROOT_HOST}`
      );

      // Same browser calling subdomain APIs without subdomain cookies is unauthenticated and never 308.
      const subMeAnon = await subClient.get("/api/auth/me");
      assert.notEqual(subMeAnon.status, 308, `GET ${HANOI_SUB_3300}/api/auth/me must not 308`);
      expectStatus(subMeAnon, [200, 401], `GET ${HANOI_SUB_3300}/api/auth/me without subdomain session`);
      if (subMeAnon.status === 200) {
        assert.equal(
          subMeAnon.data?.user ?? null,
          null,
          "subdomain /api/auth/me must return user=null with only custom-host cookies"
        );
      }

      const subCartAnon = await subClient.get("/api/cart");
      assert.notEqual(subCartAnon.status, 308, `GET ${HANOI_SUB_3300}/api/cart must not 308`);
      expectStatus(subCartAnon, 401, `GET ${HANOI_SUB_3300}/api/cart without subdomain session`);

      // CSRF: write to custom domain carrying Origin of its own subdomain is rejected with 403 BAD_ORIGIN.
      const csrfRes = await customClient.post(
        "/api/cart/items",
        { sku_id: hanoiSku.skuId, quantity: 1 },
        { origin: originOf(HANOI_SUB_3300) }
      );
      expectStatus(csrfRes, 403, `CSRF ${originOf(HANOI_SUB_3300)} -> ${HANOI_CUSTOM_3300}/api/cart/items`);
      assert.equal(csrfRes.data?.error?.code, "BAD_ORIGIN", "CSRF cross-host Origin must return BAD_ORIGIN");
      assertNoInternalErrorLeak(csrfRes.text, "CSRF subdomain origin -> custom domain");

      // Log the same buyer onto the subdomain via POST /api/auth/login (must not 308) and verify separate jars.
      const subLogin = await loginOn(subClient, buyer.creds.email, buyer.creds.password, "hanoi-sub-login");
      assert.equal(subLogin.user.id, buyer.user.id);

      assert.ok(
        browser.cookiesFor(HANOI_SUB_3300).length >= 2,
        `browser must hold >= 2 host-only cookies for ${HANOI_SUB_3300}`
      );
      assert.equal(
        browser.cookiesFor(ROOT_HOST).length,
        0,
        "logging into custom and subdomain hosts must not set cookies on root host"
      );

      const customRt = findCookie(browser, HANOI_CUSTOM_3300, ["nm_rt", "__Host-nm_rt"]);
      const subRt = findCookie(browser, HANOI_SUB_3300, ["nm_rt", "__Host-nm_rt"]);
      assert.ok(customRt && subRt, "both custom host and subdomain must hold independent refresh cookies");
      assert.notEqual(customRt, subRt, "custom host and subdomain must hold distinct refresh tokens");

      const subCartAuthed = await subClient.get("/api/cart");
      assert.notEqual(subCartAuthed.status, 308, `authed GET ${HANOI_SUB_3300}/api/cart must not 308`);
      expectStatus(subCartAuthed, 200, `authed GET ${HANOI_SUB_3300}/api/cart`);

      const customMe = expectStatus(await customClient.get("/api/auth/me"), 200, `GET ${HANOI_CUSTOM_3300}/api/auth/me`);
      assert.equal(customMe.user?.id, buyer.user.id);
    } finally {
      await Promise.allSettled([
        customClient.post("/api/auth/logout", {}),
        subClient.post("/api/auth/logout", {}),
      ]);
    }
  });

  it("g) Canonical, robots.txt, sitemap.xml and root legacy 308 redirects all use custom host hanoi-knit.verified.test", async () => {
    const customClient = new Browser("seo-custom-hanoi").at(HANOI_CUSTOM_3300);

    const home = await customClient.page("/");
    expectStatus(home, 200, `GET ${HANOI_CUSTOM_3300}/`);
    const homeCanonical = extractCanonicalHref(home.text);
    assert.ok(homeCanonical, `${HANOI_CUSTOM_3300}/ must emit <link rel="canonical">`);
    const homeCanonUrl = new URL(homeCanonical);
    assert.equal(
      homeCanonUrl.hostname.toLowerCase(),
      HANOI_CUSTOM_BARE,
      `Home canonical must use custom host ${HANOI_CUSTOM_BARE}, got ${homeCanonical}`
    );
    assert.equal(homeCanonical.includes(HANOI_SUB_BARE), false, `Home canonical leaked subdomain: ${homeCanonical}`);

    const pdpPath = `/san-pham/${SEEDED.haNoi.productSlug}`;
    const pdp = await customClient.page(pdpPath);
    expectStatus(pdp, 200, `GET ${HANOI_CUSTOM_3300}${pdpPath}`);
    const pdpCanonical = extractCanonicalHref(pdp.text);
    assert.ok(pdpCanonical, `${HANOI_CUSTOM_3300}${pdpPath} must emit <link rel="canonical">`);
    const pdpCanonUrl = new URL(pdpCanonical);
    assert.equal(
      pdpCanonUrl.hostname.toLowerCase(),
      HANOI_CUSTOM_BARE,
      `PDP canonical must use custom host ${HANOI_CUSTOM_BARE}, got ${pdpCanonical}`
    );
    assert.equal(pdpCanonUrl.pathname, pdpPath, `PDP canonical pathname mismatch: ${pdpCanonical}`);
    assert.equal(pdpCanonical.includes(HANOI_SUB_BARE), false, `PDP canonical leaked subdomain: ${pdpCanonical}`);

    const robots = await customClient.get("/robots.txt", { headers: { accept: "text/plain" } });
    expectStatus(robots, 200, `GET ${HANOI_CUSTOM_3300}/robots.txt`);
    assert.ok(
      robots.text.includes(HANOI_CUSTOM_BARE) && robots.text.includes("/sitemap.xml"),
      `Custom host /robots.txt must reference ${HANOI_CUSTOM_BARE}/sitemap.xml, got:\n${robots.text}`
    );
    assert.equal(
      robots.text.includes(HANOI_SUB_BARE),
      false,
      `Custom host /robots.txt must not reference subdomain ${HANOI_SUB_BARE}`
    );

    const sitemap = await customClient.get("/sitemap.xml", { headers: { accept: "application/xml" } });
    expectStatus(sitemap, 200, `GET ${HANOI_CUSTOM_3300}/sitemap.xml`);
    assert.ok(
      sitemap.text.includes(HANOI_CUSTOM_BARE) && sitemap.text.includes(pdpPath),
      `Custom host /sitemap.xml must include ${HANOI_CUSTOM_BARE} and ${pdpPath}`
    );
    assert.equal(
      sitemap.text.includes(HANOI_SUB_BARE),
      false,
      `Custom host /sitemap.xml must not reference subdomain ${HANOI_SUB_BARE}`
    );
    assert.equal(
      sitemap.text.includes(SEEDED.bachDang.productSlug),
      false,
      "Custom host /sitemap.xml leaked foreign shop product slug"
    );

    // Root legacy /gian-hang/<slug> and /san-pham/<slug> 308 directly to Ha Noi's custom primary host.
    const rootClient = new Browser("root-legacy-hanoi").at(ROOT_HOST);

    const legacyShop = await rootClient.page(`/gian-hang/${SEEDED.haNoi.shopSlug}`);
    assert.equal(
      legacyShop.status,
      308,
      `GET ${ROOT_HOST}/gian-hang/${SEEDED.haNoi.shopSlug} must 308, got ${legacyShop.status}`
    );
    const shopLoc = new URL(legacyShop.location, originOf(ROOT_HOST));
    assert.equal(
      shopLoc.hostname.toLowerCase(),
      HANOI_CUSTOM_BARE,
      `Root /gian-hang/${SEEDED.haNoi.shopSlug} must 308 to ${HANOI_CUSTOM_BARE}, got ${legacyShop.location}`
    );
    assert.equal(shopLoc.pathname, "/", `Root /gian-hang/${SEEDED.haNoi.shopSlug} 308 path: ${legacyShop.location}`);

    const legacyPdp = await rootClient.page(pdpPath);
    assert.equal(legacyPdp.status, 308, `GET ${ROOT_HOST}${pdpPath} must 308, got ${legacyPdp.status}`);
    const pdpLoc = new URL(legacyPdp.location, originOf(ROOT_HOST));
    assert.equal(
      pdpLoc.hostname.toLowerCase(),
      HANOI_CUSTOM_BARE,
      `Root ${pdpPath} must 308 to ${HANOI_CUSTOM_BARE}, got ${legacyPdp.location}`
    );
    assert.equal(pdpLoc.pathname, pdpPath, `Root ${pdpPath} 308 path: ${legacyPdp.location}`);
  });

  it("h) Seller and Admin domain management BFF routes on ROOT_HOST enforce RBAC, CSRF, and list ACTIVE custom domain", async () => {
    const anonRoot = new Browser("anon-root-domains").at(ROOT_HOST);
    expectStatus(await anonRoot.get("/api/seller/site/domains"), 401, "anon GET /api/seller/site/domains");
    expectStatus(
      await anonRoot.get(`/api/admin/sites/${encodeURIComponent(hanoiSite.shopId)}/domains`),
      401,
      `anon GET /api/admin/sites/${hanoiSite.shopId}/domains`
    );

    const sellerRoot = new Browser("seller-hanoi-domains").at(ROOT_HOST);
    const adminRoot = new Browser("admin-hanoi-domains").at(ROOT_HOST);

    try {
      await loginOn(
        sellerRoot,
        demoCredentials.sellerHanoi.email,
        demoCredentials.sellerHanoi.password,
        "seller-hanoi-domains"
      );

      const sellerRes = await sellerRoot.get("/api/seller/site/domains");
      const sellerData = expectStatus(sellerRes, 200, "seller GET /api/seller/site/domains");
      assertNoInternalErrorLeak(sellerRes.text, "seller GET /api/seller/site/domains");
      const sellerDomains = sellerData.domains ?? [];
      assert.ok(Array.isArray(sellerDomains) && sellerDomains.length >= 1, "seller domains must be non-empty");
      const sellerCustom = sellerDomains.find((d) => String(d.host || "").toLowerCase() === HANOI_CUSTOM_BARE);
      assert.ok(sellerCustom, `seller /api/seller/site/domains must include ${HANOI_CUSTOM_BARE}`);
      assert.equal(String(sellerCustom.status || "").toUpperCase(), "ACTIVE", "custom domain status must be ACTIVE");
      assert.equal(sellerCustom.is_primary, true, "ACTIVE custom domain must be primary");

      const sellerSub = sellerDomains.find((d) => String(d.host || "").toLowerCase() === HANOI_SUB_BARE);
      if (sellerSub) {
        assert.equal(sellerSub.is_primary, false, "subdomain must not be primary when custom domain is ACTIVE");
      }

      // CSRF on POST /api/seller/site/domains with mismatched Origin -> 403 BAD_ORIGIN.
      for (const badOrigin of ["https://evil.example", originOf(HANOI_CUSTOM_3300)]) {
        const csrfRes = await sellerRoot.post(
          "/api/seller/site/domains",
          { hostname: "csrf-rejected.verified.test" },
          { origin: badOrigin }
        );
        expectStatus(csrfRes, 403, `CSRF ${badOrigin} -> POST ${ROOT_HOST}/api/seller/site/domains`);
        assert.equal(csrfRes.data?.error?.code, "BAD_ORIGIN", "CSRF POST /api/seller/site/domains must return BAD_ORIGIN");
        assertNoInternalErrorLeak(csrfRes.text, "CSRF POST /api/seller/site/domains");
      }

      // Seller calling admin domains endpoint -> 403.
      const sellerHitAdmin = await sellerRoot.get(`/api/admin/sites/${encodeURIComponent(hanoiSite.shopId)}/domains`);
      expectStatus(sellerHitAdmin, 403, `seller GET /api/admin/sites/${hanoiSite.shopId}/domains`);
      assertNoInternalErrorLeak(sellerHitAdmin.text, "seller GET /api/admin/sites/[shopId]/domains");

      // Admin reads Ha Noi site domains on ROOT_HOST -> 200 with ACTIVE primary custom domain.
      await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "admin-hanoi-domains");
      const adminRes = await adminRoot.get(`/api/admin/sites/${encodeURIComponent(hanoiSite.shopId)}/domains`);
      const adminData = expectStatus(adminRes, 200, `admin GET /api/admin/sites/${hanoiSite.shopId}/domains`);
      assertNoInternalErrorLeak(adminRes.text, "admin GET /api/admin/sites/[shopId]/domains");
      const adminDomains = adminData.domains ?? [];
      const adminCustom = adminDomains.find((d) => String(d.host || "").toLowerCase() === HANOI_CUSTOM_BARE);
      assert.ok(adminCustom, `admin /api/admin/sites/${hanoiSite.shopId}/domains must include ${HANOI_CUSTOM_BARE}`);
      assert.equal(String(adminCustom.status || "").toUpperCase(), "ACTIVE", "admin custom domain status must be ACTIVE");
      assert.equal(adminCustom.is_primary, true, "admin custom domain must be primary");
    } finally {
      await Promise.allSettled([
        sellerRoot.post("/api/auth/logout", {}),
        adminRoot.post("/api/auth/logout", {}),
      ]);
    }
  });
});
