import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, config, siteHost, withPort } from "./lib/config.mjs";
import { Browser, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  demoCredentials,
  listPublishedSites,
  loginOn,
  primaryHostOf,
  primarySiteHost,
  registerBuyer,
} from "./lib/fixtures.mjs";

const ROOT = config.siteRootDomain;
const LOCALHOST_HOST = withPort("localhost");
const FAKE_ID = "00000000-0000-4000-8000-000000000000";
// "189.000₫", "189.000 đ", "189,000 VND": any rendered price.
const PRICE_RE = /\d{1,3}(?:[.,]\d{3})+\s?(?:₫|đ(?![a-zà-ỹ])|VND)/i;

const bare = (host) => host.replace(/:\d+$/, "");

/** Hostnames (`<label>.<root>` and custom domains from `href="https?://<host>..."`) linked in HTML/RSC. */
function linkedSiteHosts(html) {
  const hosts = new Set();
  const subRe = new RegExp(`([a-z0-9](?:[a-z0-9-]*[a-z0-9])?)\\.${ROOT.replace(/\./g, "\\.")}(?![a-z0-9.-])`, "gi");
  for (const m of html.matchAll(subRe)) {
    hosts.add(`${m[1].toLowerCase()}.${ROOT}`);
  }
  const hrefRe = /\bhref=["']https?:\/\/([a-z0-9.-]+)(?::\d+)?(?:[/?#"']|$)/gi;
  for (const m of html.matchAll(hrefRe)) {
    const h = m[1].toLowerCase().replace(/\.+$/, "");
    if (h && h !== ROOT && h !== "localhost") {
      hosts.add(h);
    }
  }
  return hosts;
}

describe("01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO)", () => {
  const root = new Browser("root-anon").at(ROOT_HOST);
  let directory;

  before(async () => {
    await resetRateLimitKeys();
    directory = await listPublishedSites();
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("public directory REST lists only PUBLISHED sites with the contract fields and capped page size", async () => {
    assert.ok(directory.total >= 4, `directory total must be >= 4 seeded sites, got ${directory.total}`);
    assert.equal(directory.sites.length, directory.total, "walking all pages must return exactly `total` sites");
    const subs = new Set(directory.sites.map((s) => s.subdomain));
    assert.equal(subs.size, directory.sites.length, "directory must not repeat a site across pages");
    for (const shop of Object.values(SEEDED)) {
      assert.ok(subs.has(shop.sub), `seeded site ${shop.sub} must be PUBLISHED and listed`);
    }
    const seededBySub = new Map(Object.values(SEEDED).map((shop) => [shop.sub, shop]));
    for (const s of directory.sites) {
      for (const field of ["site_id", "shop_id", "shop_name", "subdomain", "primary_host", "logo_url", "cover_url", "tagline"]) {
        assert.ok(field in s, `directory site ${s.subdomain} missing field ${field}`);
      }
      const matchedShop = seededBySub.get(s.subdomain);
      if (matchedShop) {
        assert.equal(s.primary_host, primaryHostOf(matchedShop, ROOT), `primary_host of ${s.subdomain}`);
      } else {
        assert.ok(
          typeof s.primary_host === "string" && s.primary_host.trim().length > 0,
          `primary_host of ${s.subdomain} must be non-empty`
        );
      }
    }
    const capped = await fetch(`${config.gatewayURL}/api/v1/sites?page=1&page_size=500`);
    assert.equal(capped.status, 200);
    const cappedJson = await capped.json();
    assert.ok(cappedJson.page_size <= 48 && cappedJson.sites.length <= 48, "page_size must be capped at 48");

    // Every seeded listed site is actually served by the web app.
    for (const shop of Object.values(SEEDED)) {
      const host = primarySiteHost(shop);
      const home = await new Browser("dir-probe").at(host).page("/");
      assert.equal(home.status, 200, `listed site ${host} must serve its home page`);
    }
  });

  it("root / (both SITE_ROOT_DOMAIN and localhost) is the NexusMart intro: 200, real site directory, no products or prices", async () => {
    for (const host of [ROOT_HOST, LOCALHOST_HOST]) {
      const client = new Browser(`root-intro-${host}`).at(host);
      const res = await client.page("/");
      expectStatus(res, 200, `GET ${host}/`);
      assert.ok(res.text.includes("NexusMart"), `${host}/ must mention NexusMart`);
      assert.ok(res.text.includes("Mở gian hàng"), `${host}/ must show primary CTA 'Mở gian hàng'`);

      assert.doesNotMatch(res.text, PRICE_RE, `${host}/ must not render prices: ${res.text.match(PRICE_RE)?.[0]}`);
      assert.equal(/href="\/san-pham\//.test(res.text), false, `${host}/ must not link product pages`);
      assert.equal(
        /href="\/(gio-hang|thanh-toan|tim-kiem|danh-muc|flash-sale|voucher)/.test(res.text),
        false,
        `${host}/ must not link old marketplace buyer pages`
      );
      for (const shop of Object.values(SEEDED)) {
        assert.equal(res.text.includes(shop.productSlug), false, `${host}/ leaked product ${shop.productSlug}`);
      }
      assert.equal(res.text.includes("Thêm vào giỏ"), false, `${host}/ must not offer add-to-cart`);
    }

    // Directory is real backend data: newest published sites appear on page 1, all 4 seeded sites
    // appear across directory pages, and no unpublished host is linked.
    const page1 = await root.page("/");
    expectStatus(page1, 200, `GET ${ROOT_HOST}/`);
    const linkedPage1 = linkedSiteHosts(page1.text);
    const published = new Set(directory.sites.map((s) => s.primary_host));
    for (const s of directory.sites.slice(0, Math.min(8, directory.sites.length))) {
      assert.ok(linkedPage1.has(s.primary_host), `root / directory must link newest published site ${s.primary_host}`);
      assert.ok(page1.text.includes(s.shop_name), `root / directory must show shop name ${s.shop_name}`);
    }
    for (const host of linkedPage1) {
      assert.ok(published.has(host), `root / links ${host}, which is not a PUBLISHED site`);
    }

    const allLinked = new Set(linkedPage1);
    let combinedHtml = page1.text;
    const totalPages = Math.min(5, Math.max(1, Math.ceil(directory.total / 24)));
    for (let p = 2; p <= totalPages; p += 1) {
      const next = await root.page(`/?trang=${p}`);
      expectStatus(next, 200, `GET ${ROOT_HOST}/?trang=${p}`);
      for (const h of linkedSiteHosts(next.text)) allLinked.add(h);
      combinedHtml += "\n" + next.text;
    }
    for (const shop of Object.values(SEEDED)) {
      const expectedHost = primaryHostOf(shop, ROOT);
      assert.ok(allLinked.has(expectedHost), `root directory must list seeded PUBLISHED site ${expectedHost}`);
      assert.ok(combinedHtml.includes(shop.shopName), `root directory must show shop name ${shop.shopName}`);
    }
  });

  it("platform pages stay on root; every old marketplace buyer URL is 404", async () => {
    for (const path of ["/dang-nhap", "/dang-ky", "/ve-nexusmart", "/tro-giup"]) {
      expectStatus(await root.page(path), 200, `GET ${ROOT_HOST}${path}`);
    }
    const gone = [
      "/danh-muc",
      "/danh-muc?cat=ao-thun",
      "/tim-kiem",
      "/tim-kiem?q=ao",
      "/san-pham",
      "/flash-sale",
      "/bo-suu-tap",
      "/bo-suu-tap/mua-he",
      "/chien-dich",
      "/chien-dich/sale-11-11",
      "/gian-hang",
      "/gio-hang",
      "/thanh-toan",
      "/don-hang",
      `/don-hang/${FAKE_ID}`,
      "/tai-khoan",
      "/hoi-vien",
      "/the-qua-tang",
      "/tap-chi",
      "/voucher",
      "/tra-cuu-don",
      "/tra-cuu-don-hang",
    ];
    for (const path of gone) {
      const res = await root.page(path);
      assert.equal(res.status, 404, `old buyer URL ${ROOT_HOST}${path} must be 404, got ${res.status} ${res.location}`);
    }
  });

  it("/gian-hang/<slug> and /san-pham/<slug> 308 to the shop's published site; unknown slugs 404", async () => {
    const shop = await root.page(`/gian-hang/${SEEDED.bachDang.shopSlug}`);
    assert.equal(shop.status, 308, `/gian-hang/${SEEDED.bachDang.shopSlug} must 308, got ${shop.status}`);
    const shopLoc = new URL(shop.location, `http://${ROOT_HOST}`);
    assert.equal(shopLoc.hostname, `${SEEDED.bachDang.sub}.${ROOT}`, `308 target host: ${shop.location}`);
    assert.equal(shopLoc.pathname, "/", `308 target path: ${shop.location}`);

    const pdp = await root.page(`/san-pham/${SEEDED.saiGon.productSlug}`);
    assert.equal(pdp.status, 308, `/san-pham/${SEEDED.saiGon.productSlug} must 308, got ${pdp.status}`);
    const pdpLoc = new URL(pdp.location, `http://${ROOT_HOST}`);
    assert.equal(pdpLoc.hostname, `${SEEDED.saiGon.sub}.${ROOT}`, `308 target host: ${pdp.location}`);
    assert.equal(pdpLoc.pathname, `/san-pham/${SEEDED.saiGon.productSlug}`, `308 target path: ${pdp.location}`);

    // The redirect target really serves the product.
    const landed = await new Browser("pdp-follow").at(siteHost(SEEDED.saiGon.sub)).page(pdpLoc.pathname);
    assert.equal(landed.status, 200, `redirect target ${pdp.location} must serve 200`);

    for (const path of ["/gian-hang/khong-ton-tai-e2e", "/san-pham/khong-ton-tai-e2e"]) {
      const res = await root.page(path);
      assert.equal(res.status, 404, `${ROOT_HOST}${path} must be 404, got ${res.status} ${res.location}`);
    }
  });

  it("buyer APIs (cart, checkout, orders, payments, vouchers) are 404 on the root host, anonymous or logged in", async () => {
    // A real buyer account (registered on a site) logging in on root: auth works, buyer APIs do not.
    const buyerBrowser = new Browser("root-buyer");
    const { creds } = await registerBuyer(buyerBrowser.at(siteHost(SEEDED.bachDang.sub)), "root-buyer");
    const buyerRoot = buyerBrowser.at(ROOT_HOST);
    const { user } = await loginOn(buyerRoot, creds.email, creds.password, "root-buyer");
    assert.equal(user.role, "buyer", "buyer login on root must report role=buyer so the UI sends them to the directory");

    const localhostAnon = new Browser("localhost-anon").at(LOCALHOST_HOST);
    const calls = [
      ["GET", "/api/cart"],
      ["POST", "/api/cart/items", { sku_id: FAKE_ID, quantity: 1 }],
      ["DELETE", "/api/cart"],
      ["POST", "/api/checkout", { address_id: FAKE_ID, payment_method: "COD" }],
      ["POST", "/api/checkout/shipping-quote", { shops: [] }],
      ["POST", "/api/checkout/addresses", { recipient_name: "Root" }],
      ["GET", "/api/checkout/locations"],
      ["GET", "/api/orders"],
      ["GET", `/api/orders/${FAKE_ID}`],
      ["POST", `/api/orders/${FAKE_ID}/cancel`, { reason: "x" }],
      ["GET", `/api/payments/${FAKE_ID}`],
      ["GET", "/api/vouchers"],
      ["POST", "/api/vouchers/validate", { code: SEEDED.bachDang.voucherCode }],
      ["POST", "/api/vouchers/preview", {}],
    ];
    try {
      for (const client of [root, localhostAnon, buyerRoot]) {
        for (const [method, path, json] of calls) {
          const res = await client.request(method, path, { json });
          assert.equal(
            res.status,
            404,
            `${method} ${client.host}${path} (${client.browser.label}) must be 404, got ${res.status}: ${res.text.slice(0, 200)}`
          );
          assertNoInternalErrorLeak(res.text, `${method} ${path}`);
        }
      }
    } finally {
      await buyerRoot.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("RBAC on root: anonymous redirected to /dang-nhap; buyer, seller, admin only reach their own area", async () => {
    for (const path of ["/seller", "/admin"]) {
      const res = await root.page(path);
      expectStatus(res, [302, 303, 307, 308], `anonymous GET ${path}`);
      const loc = new URL(res.location, `http://${ROOT_HOST}`);
      assert.equal(loc.pathname, "/dang-nhap", `anonymous ${path} must redirect to /dang-nhap, got ${res.location}`);
      assert.ok(loc.searchParams.get("next"), `anonymous ${path} redirect must carry next=`);
      assert.equal(bare(loc.host), bare(ROOT_HOST), `anonymous ${path} must stay on root host`);
    }

    const buyerBrowser = new Browser("rbac-buyer");
    const buyer = await registerBuyer(buyerBrowser.at(siteHost(SEEDED.saiGon.sub)), "rbac-buyer");
    const buyerRoot = buyerBrowser.at(ROOT_HOST);
    await loginOn(buyerRoot, buyer.creds.email, buyer.creds.password, "rbac-buyer-root");
    try {
      for (const path of ["/seller", "/admin"]) {
        const res = await buyerRoot.page(path);
        expectStatus(res, [302, 303, 307, 403, 404], `buyer GET ${path}`);
        assert.notEqual(res.status, 200, `buyer must not get 200 on ${path}`);
      }
      expectStatus(await buyerRoot.get("/api/admin/orders"), 403, "buyer GET /api/admin/orders");
      expectStatus(await buyerRoot.get("/api/seller/orders"), 403, "buyer GET /api/seller/orders");
    } finally {
      await buyerRoot.post("/api/auth/logout", {}).catch(() => {});
    }

    const sellerRoot = new Browser("rbac-seller").at(ROOT_HOST);
    const seller = await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "seller");
    try {
      assert.equal(seller.user.role, "seller");
      expectStatus(await sellerRoot.page("/seller"), 200, "seller GET /seller");
      expectStatus(await sellerRoot.page("/seller/site"), 200, "seller GET /seller/site");
      expectStatus(await sellerRoot.page("/seller/don-hang"), 200, "seller GET /seller/don-hang");
      const shop = expectStatus(await sellerRoot.get("/api/seller/shop"), 200, "seller GET /api/seller/shop");
      assert.ok(shop.shop?.id, "seller shop API must return the owned shop");
      expectStatus(await sellerRoot.get("/api/seller/orders?page=1&page_size=10"), 200, "seller GET /api/seller/orders");
      expectStatus(await sellerRoot.page("/admin"), [403, 404], "seller GET /admin");
      expectStatus(await sellerRoot.get("/api/admin/orders"), 403, "seller GET /api/admin/orders");
    } finally {
      await sellerRoot.post("/api/auth/logout", {}).catch(() => {});
    }

    const adminRoot = new Browser("rbac-admin").at(ROOT_HOST);
    const admin = await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "admin");
    try {
      assert.ok(["admin", "super_admin"].includes(admin.user.role), `admin role, got ${admin.user.role}`);
      expectStatus(await adminRoot.page("/admin"), 200, "admin GET /admin");
      expectStatus(await adminRoot.page("/admin/sites"), 200, "admin GET /admin/sites");
      expectStatus(await adminRoot.page("/admin/doi-soat"), 200, "admin GET /admin/doi-soat");
      const ledger = expectStatus(await adminRoot.get("/api/admin/ledger"), 200, "admin GET /api/admin/ledger");
      assert.equal(ledger.is_balanced, true, "double-entry ledger must be balanced");
      // Admin can still read legacy MARKETPLACE & STOREFRONT orders.
      const mktOrders = expectStatus(
        await adminRoot.get("/api/admin/orders?sales_channel=MARKETPLACE&page=1&page_size=10"),
        200,
        "admin GET /api/admin/orders?sales_channel=MARKETPLACE"
      );
      for (const ord of mktOrders.items ?? []) assert.equal(ord.sales_channel, "MARKETPLACE");
      const sfOrders = expectStatus(
        await adminRoot.get("/api/admin/orders?sales_channel=STOREFRONT&page=1&page_size=10"),
        200,
        "admin GET /api/admin/orders?sales_channel=STOREFRONT"
      );
      for (const ord of sfOrders.items ?? []) assert.equal(ord.sales_channel, "STOREFRONT");
      expectStatus(
        await adminRoot.get("/api/admin/orders?sales_channel=BOGUS"),
        400,
        "admin GET /api/admin/orders?sales_channel=BOGUS"
      );
      expectStatus(await adminRoot.page("/seller"), [403, 404], "admin GET /seller");
      expectStatus(await adminRoot.get("/api/seller/orders"), 403, "admin GET /api/seller/orders");
    } finally {
      await adminRoot.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("root robots.txt / sitemap.xml list the intro and published sites, never product or removed buyer URLs", async () => {
    const robots = await root.get("/robots.txt", { headers: { accept: "text/plain" } });
    expectStatus(robots, 200, `GET ${ROOT_HOST}/robots.txt`);
    assert.match(robots.text, /sitemap\.xml/i, "root robots.txt must reference its sitemap");

    const sitemap = await root.get("/sitemap.xml", { headers: { accept: "application/xml" } });
    expectStatus(sitemap, 200, `GET ${ROOT_HOST}/sitemap.xml`);
    assert.ok(sitemap.text.includes(ROOT), "root sitemap must include root intro URL");
    for (const shop of Object.values(SEEDED)) {
      const expectedHost = primaryHostOf(shop, ROOT);
      assert.ok(sitemap.text.includes(expectedHost), `root sitemap must link site ${expectedHost}`);
    }
    for (const removed of ["/san-pham/", "/danh-muc", "/flash-sale", "/voucher", "/bo-suu-tap"]) {
      assert.equal(sitemap.text.includes(removed), false, `root sitemap must not list removed marketplace URL ${removed}`);
    }
  });
});
