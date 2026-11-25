import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, config, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, pollUntil, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  createEphemeralSeller,
  demoCredentials,
  extractCanonicalHref,
  extractMetaContent,
  loginOn,
  makeSiteConfig,
  registerBuyer,
} from "./lib/fixtures.mjs";

const ROOT = config.siteRootDomain;
const SITE_A_SUB = SEEDED.bachDang.sub;
const SITE_B_SUB = SEEDED.saiGon.sub;
const SITE_A_HOST = siteHost(SITE_A_SUB);
const SITE_B_HOST = siteHost(SITE_B_SUB);

const SHOP_A_SLUG = SEEDED.bachDang.productSlug;
const SHOP_B_SLUG = SEEDED.saiGon.productSlug;

describe("04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO", () => {
  before(async () => {
    await resetRateLimitKeys();
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("seller draft/publish/rollback lifecycle, cross-seller RBAC, theme validation, empty-section noindex, and admin suspend/unsuspend (pages + APIs)", async () => {
    const runTag = uid().toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 10);
    const subTest = `e2e-web-${runTag}`;
    const testHost = siteHost(subTest);

    const sellerA = await createEphemeralSeller("site-owner", ROOT_HOST);
    const sellerB = new Browser("seller-b-root").at(ROOT_HOST);
    await loginOn(sellerB, demoCredentials.sellerHanoi.email, demoCredentials.sellerHanoi.password, "seller-hanoi-b");
    const admin = new Browser("admin-sites-root").at(ROOT_HOST);
    await loginOn(admin, demoCredentials.admin.email, demoCredentials.admin.password, "admin-sites");

    const siteProbe = new Browser("ephemeral-probe").at(testHost);
    let buyerOnEphemeral = null;
    let createdSiteId = "";

    try {
      // --- 1. Save draft v1 -> DRAFT host returns 404 on BOTH pages AND APIs; owner preview 200 + noindex ---
      const v1Hero = `Tieu De Ban Nhap V1 ${runTag}`;
      const v2Hero = `Tieu De Phien Ban V2 ${runTag}`;
      const draftV1Cfg = makeSiteConfig({
        brandName: `Thu Nghiem ${runTag}`,
        heroTitle: v1Hero,
        accentColor: "#1D4ED8",
      });

      const saveV1 = expectStatus(
        await sellerA.client.put("/api/seller/site/draft", { subdomain: subTest, config: draftV1Cfg }),
        200,
        "PUT /api/seller/site/draft (v1)"
      );
      assert.equal(saveV1.site.status, "DRAFT");
      assert.equal(saveV1.site.subdomain, subTest);
      createdSiteId = saveV1.site.id;

      for (const path of ["/", "/api/auth/me", "/api/cart", "/api/orders", "/api/vouchers"]) {
        const res = await siteProbe.get(path, { headers: { accept: "*/*" } });
        assert.equal(res.status, 404, `DRAFT site ${testHost}${path} must return 404, got ${res.status}`);
      }

      const previewOwner = await sellerA.client.page("/seller/site/xem-truoc");
      expectStatus(previewOwner, 200, "GET /seller/site/xem-truoc (owner)");
      const previewRobots = extractMetaContent(previewOwner.text, "robots") ?? "";
      assert.match(previewRobots, /noindex/i, `Owner preview must set noindex meta, got "${previewRobots}"`);
      assert.ok(previewOwner.text.includes(v1Hero), "Owner preview must render draft v1 hero title");

      expectStatus(await sellerA.client.page("/seller/site"), 200, "GET /seller/site (owner)");

      // Unauthenticated preview redirects to /dang-nhap.
      const anonRoot = new Browser("anon-preview").at(ROOT_HOST);
      const previewAnon = await anonRoot.page("/seller/site/xem-truoc");
      expectStatus(previewAnon, [302, 303, 307, 308], "GET /seller/site/xem-truoc (unauthenticated)");
      assert.ok(previewAnon.location.includes("/dang-nhap"), `Unauth preview must redirect to /dang-nhap, got ${previewAnon.location}`);

      // Cross-seller RBAC: Seller B cannot view or preview Seller A's site (even with ?shop_id=).
      const sellerBSite = expectStatus(await sellerB.get("/api/seller/site"), 200, "Seller B GET /api/seller/site");
      assert.notEqual(sellerBSite.site?.id, createdSiteId, "Seller B must NEVER read Seller A's site");
      assert.equal(JSON.stringify(sellerBSite).includes(v1Hero), false, "Seller B leaked Seller A's draft");

      const sellerBPreview = await sellerB.page(`/seller/site/xem-truoc?shop_id=${encodeURIComponent(sellerA.shopId)}`);
      expectStatus(sellerBPreview, 200, "Seller B preview scoped to own shop");
      assert.equal(sellerBPreview.text.includes(v1Hero), false, "Seller B preview with ?shop_id= leaked Seller A's draft");

      // --- 2. Publish v1 -> poll until public 200 with v1; Seller B cannot rollback Seller A's version ---
      const pubV1 = expectStatus(
        await sellerA.client.post("/api/seller/site/publish", { note: "Publish v1" }),
        200,
        "POST /api/seller/site/publish (v1)"
      );
      assert.equal(pubV1.site.status, "PUBLISHED");
      const v1VersionId = pubV1.version.id;
      assert.ok(v1VersionId, "publish v1 must return version.id");

      const liveV1 = await pollUntil(
        `public ${testHost} returns 200 with v1 content`,
        async () => {
          const r = await siteProbe.page("/");
          return [r.status === 200 && r.text.includes(v1Hero), `status=${r.status}`, r];
        },
        { timeoutMs: 5000, intervalMs: 250 }
      );
      assert.ok(liveV1.text.includes(v1Hero));

      const hijackRb = await sellerB.post("/api/seller/site/rollback", {
        version_id: v1VersionId,
        note: "cross-tenant rollback",
      });
      expectStatus(hijackRb, [403, 404], "Seller B rollback of Seller A version");

      // --- 3. Save draft v2 (without publish) -> public STILL serves v1 ---
      const draftV2Cfg = makeSiteConfig({
        brandName: `Thu Nghiem ${runTag}`,
        heroTitle: v2Hero,
        accentColor: "#0F766E",
      });
      expectStatus(
        await sellerA.client.put("/api/seller/site/draft", { subdomain: subTest, config: draftV2Cfg }),
        200,
        "PUT /api/seller/site/draft (v2)"
      );
      const stillV1 = await siteProbe.page("/");
      expectStatus(stillV1, 200, `GET ${testHost}/ before publishing v2`);
      assert.ok(stillV1.text.includes(v1Hero), "Public site must still render v1 before publishing v2");
      assert.equal(stillV1.text.includes(v2Hero), false, "Unpublished draft v2 must NOT leak to public host");

      // --- 4. Publish v2 -> public reflects v2; Rollback to v1 -> public returns to v1 ---
      const pubV2 = expectStatus(
        await sellerA.client.post("/api/seller/site/publish", { note: "Publish v2" }),
        200,
        "POST /api/seller/site/publish (v2)"
      );
      assert.equal(pubV2.version.version_number, 2);

      const liveV2 = await siteProbe.page("/");
      expectStatus(liveV2, 200, `GET ${testHost}/ after publish v2`);
      assert.ok(liveV2.text.includes(v2Hero), "Publish v2 must reflect on public host");
      assert.equal(liveV2.text.includes(v1Hero), false, "Old v1 title must disappear after publish v2");

      const rbV1 = expectStatus(
        await sellerA.client.post("/api/seller/site/rollback", { version_id: v1VersionId, note: "Rollback to v1" }),
        200,
        "POST /api/seller/site/rollback (v1)"
      );
      assert.equal(rbV1.site.published_version_id, v1VersionId);

      const liveAfterRb = await siteProbe.page("/");
      expectStatus(liveAfterRb, 200, `GET ${testHost}/ after rollback v1`);
      assert.ok(liveAfterRb.text.includes(v1Hero), "Rollback to v1 must reflect on public host");
      assert.equal(liveAfterRb.text.includes(v2Hero), false, "v2 title must disappear after rollback");

      const versions = expectStatus(await sellerA.client.get("/api/seller/site/versions"), 200, "GET /api/seller/site/versions");
      assert.ok((versions.versions ?? []).length >= 2, "versions history must list >= 2 versions");

      // --- 5. Bad theme/section payloads rejected (unknown fields, javascript:/data: URLs, low contrast, reserved/dup subdomains, XSS) ---
      const baseValid = makeSiteConfig({ brandName: "SecCheck", heroTitle: v1Hero });

      const unknownTheme = await sellerA.client.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: { ...baseValid, theme: { ...baseValid.theme, custom_css: "body{background:red}" } },
      });
      expectStatus(unknownTheme, 400, "PUT /api/seller/site/draft (unknown theme field)");
      assertNoInternalErrorLeak(unknownTheme.text, "unknown theme field");

      const unknownSection = await sellerA.client.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: { ...baseValid, sections: [{ ...baseValid.sections[0], raw_html: "<b>inject</b>" }] },
      });
      expectStatus(unknownSection, 400, "PUT /api/seller/site/draft (unknown section field)");

      const jsUrl = await sellerA.client.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: { ...baseValid, theme: { ...baseValid.theme, logo_url: "javascript:alert(1)" } },
      });
      expectStatus(jsUrl, 400, "PUT /api/seller/site/draft (javascript: URL)");

      const dataUrl = await sellerA.client.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: { ...baseValid, theme: { ...baseValid.theme, banner_url: "data:text/html,<script>alert(1)</script>" } },
      });
      expectStatus(dataUrl, 400, "PUT /api/seller/site/draft (data: URL)");

      const lowContrast = await sellerA.client.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: { ...baseValid, theme: { ...baseValid.theme, bg_color: "#FFFFFF", surface_color: "#FFFFFF", text_color: "#CCCCCC" } },
      });
      expectStatus(lowContrast, 400, "PUT /api/seller/site/draft (low contrast)");

      for (const reserved of ["www", "admin", "api", "nexusmart", "vietcombank-uudai"]) {
        const res = await sellerA.client.put("/api/seller/site/draft", { subdomain: reserved, config: baseValid });
        expectStatus(res, 400, `reserved subdomain ${reserved}`);
      }
      for (const dup of [SITE_A_SUB, SITE_A_SUB.toUpperCase()]) {
        const res = await sellerA.client.put("/api/seller/site/draft", { subdomain: dup, config: baseValid });
        expectStatus(res, 409, `duplicate subdomain ${dup}`);
      }

      // XSS payload must either be rejected 400 OR HTML-escaped safely.
      const xssPayload = '<script>alert("xss")</script>';
      const xssCfg = makeSiteConfig({
        brandName: `XSS Check ${runTag}`,
        tagline: `An toan ${xssPayload}`,
        heroTitle: v1Hero,
        sections: [
          {
            id: "text-xss",
            type: "text",
            visible: true,
            props: { title: "Kiem tra XSS", body: `Van ban thuan ${xssPayload}`, align: "left" },
          },
        ],
      });
      const xssDraft = await sellerA.client.put("/api/seller/site/draft", { subdomain: subTest, config: xssCfg });
      if (xssDraft.status === 200) {
        expectStatus(await sellerA.client.post("/api/seller/site/publish", { note: "XSS check" }), 200, "publish XSS check");
        const xssHtml = await siteProbe.page("/");
        expectStatus(xssHtml, 200, `GET ${testHost}/ after XSS publish`);
        assert.equal(xssHtml.text.includes(xssPayload), false, "Rendered HTML MUST NOT contain raw executable <script>!");
      } else {
        expectStatus(xssDraft, 400, "XSS payload must either be rejected 400 or safely escaped");
      }

      // --- 6. Empty sections (`sections: []`) published -> emits `noindex` meta tag ---
      const emptyCfg = makeSiteConfig({ brandName: `Empty Sections ${runTag}`, heroTitle: "", sections: [] });
      expectStatus(
        await sellerA.client.put("/api/seller/site/draft", { subdomain: subTest, config: emptyCfg }),
        200,
        "PUT /api/seller/site/draft (empty sections)"
      );
      expectStatus(
        await sellerA.client.post("/api/seller/site/publish", { note: "Publish empty sections" }),
        200,
        "POST /api/seller/site/publish (empty sections)"
      );
      const emptyHtml = await siteProbe.page("/");
      expectStatus(emptyHtml, 200, `GET ${testHost}/ (empty sections)`);
      const emptyRobots = extractMetaContent(emptyHtml.text, "robots") ?? "";
      assert.match(emptyRobots, /noindex/i, `Site with empty sections must emit noindex meta, got "${emptyRobots}"`);

      // --- 7. Admin suspend -> BOTH pages AND buyer APIs return 404; unsuspend -> restores 200 ---
      // Sign a buyer into the ephemeral site while it is PUBLISHED to prove buyer APIs work before suspend.
      const ephemeralBuyerBrowser = new Browser("ephemeral-buyer");
      buyerOnEphemeral = ephemeralBuyerBrowser.at(testHost);
      await registerBuyer(buyerOnEphemeral, "ephem-susp");

      expectStatus(await buyerOnEphemeral.get("/api/auth/me"), 200, `live ${testHost}/api/auth/me`);
      expectStatus(await buyerOnEphemeral.get("/api/cart"), 200, `live ${testHost}/api/cart`);
      expectStatus(await buyerOnEphemeral.get("/api/orders"), 200, `live ${testHost}/api/orders`);
      expectStatus(await buyerOnEphemeral.get("/api/vouchers"), 200, `live ${testHost}/api/vouchers`);

      const adminList = expectStatus(
        await admin.get(`/api/admin/sites?q=${encodeURIComponent(subTest)}`),
        200,
        "GET /api/admin/sites"
      );
      assert.ok((adminList.items ?? []).some((s) => s.id === createdSiteId), "Admin /api/admin/sites must list test site");
      expectStatus(await admin.page("/admin/sites"), 200, "GET /admin/sites");

      const suspRes = expectStatus(
        await admin.post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
          suspend: true,
          reason: "E2E web suspend check",
        }),
        200,
        "POST /api/admin/sites/[id]/suspend (true)"
      );
      assert.equal(suspRes.site.status, "SUSPENDED");

      // Checkout uses a fresh uncached lookup, so it MUST return 404 immediately on suspend.
      const immediateCheckout = await buyerOnEphemeral.post("/api/checkout", {
        address_id: "00000000-0000-4000-8000-000000000000",
        payment_method: "COD",
      });
      assert.equal(
        immediateCheckout.status,
        404,
        `SUSPENDED site POST ${testHost}/api/checkout must return 404 immediately, got ${immediateCheckout.status}`
      );

      // Wait for Edge middleware's documented <= 3s positive host cache TTL to expire so BOTH pages
      // and middleware-gated APIs return 404.
      await pollUntil(
        `suspended ${testHost} returns 404 for both / and /api/auth/me`,
        async () => {
          const [pageRes, apiRes] = await Promise.all([
            siteProbe.page("/"),
            buyerOnEphemeral.get("/api/auth/me"),
          ]);
          return [
            pageRes.status === 404 && apiRes.status === 404,
            `page=${pageRes.status}, api=${apiRes.status}`,
            { pageRes, apiRes },
          ];
        },
        { timeoutMs: 5000, intervalMs: 250 }
      );

      // While SUSPENDED, ALL buyer APIs on the site host MUST ALSO return 404 (even for a logged-in buyer!).
      const suspendedApiCalls = [
        ["GET", "/api/auth/me"],
        ["GET", "/api/cart"],
        ["POST", "/api/cart/items", { sku_id: "00000000-0000-4000-8000-000000000000", quantity: 1 }],
        ["GET", "/api/orders"],
        ["POST", "/api/checkout", { address_id: "00000000-0000-4000-8000-000000000000", payment_method: "COD" }],
        ["GET", "/api/vouchers"],
      ];
      for (const [method, path, json] of suspendedApiCalls) {
        const res = await buyerOnEphemeral.request(method, path, { json });
        assert.equal(
          res.status,
          404,
          `SUSPENDED site ${method} ${testHost}${path} must return 404, got ${res.status}: ${res.text.slice(0, 200)}`
        );
      }

      // Unsuspend -> both pages AND buyer APIs work again (after <= 1.5s negative cache TTL).
      const unsuspRes = expectStatus(
        await admin.post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
          suspend: false,
          reason: "E2E web unsuspend check",
        }),
        200,
        "POST /api/admin/sites/[id]/suspend (false)"
      );
      assert.equal(unsuspRes.site.status, "PUBLISHED");

      await pollUntil(
        `unsuspended ${testHost} returns 200 for both / and /api/auth/me`,
        async () => {
          const [pageRes, apiRes] = await Promise.all([
            siteProbe.page("/"),
            buyerOnEphemeral.get("/api/auth/me"),
          ]);
          return [
            pageRes.status === 200 && apiRes.status === 200,
            `page=${pageRes.status}, api=${apiRes.status}`,
            { pageRes, apiRes },
          ];
        },
        { timeoutMs: 5000, intervalMs: 250 }
      );

      expectStatus(await buyerOnEphemeral.get("/api/auth/me"), 200, `restored ${testHost}/api/auth/me`);
      expectStatus(await buyerOnEphemeral.get("/api/cart"), 200, `restored ${testHost}/api/cart`);
      expectStatus(await buyerOnEphemeral.get("/api/orders"), 200, `restored ${testHost}/api/orders`);
      expectStatus(await buyerOnEphemeral.get("/api/vouchers"), 200, `restored ${testHost}/api/vouchers`);
    } finally {
      // Always suspend the ephemeral site so it never pollutes the public directory across repeated runs.
      if (createdSiteId) {
        await admin
          .post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
            suspend: true,
            reason: "E2E web cleanup",
          })
          .catch(() => {});
      }
      await Promise.allSettled([
        buyerOnEphemeral?.post("/api/auth/logout", {}),
        sellerA.client.post("/api/auth/logout", {}),
        sellerB.post("/api/auth/logout", {}),
        admin.post("/api/auth/logout", {}),
      ]);
    }
  });

  it("SEO per site host: product canonical points to OWN site URL, substantive sites are indexable, host-scoped robots.txt & sitemap.xml", async () => {
    const anonA = new Browser("seo-a").at(SITE_A_HOST);
    const anonB = new Browser("seo-b").at(SITE_B_HOST);

    // Substantive seeded sites must NOT emit noindex.
    for (const [host, client] of [
      [SITE_A_HOST, anonA],
      [SITE_B_HOST, anonB],
    ]) {
      const home = await client.page("/");
      expectStatus(home, 200, `GET ${host}/`);
      const robotsMeta = extractMetaContent(home.text, "robots") ?? "";
      assert.equal(/noindex/i.test(robotsMeta), false, `Substantive site ${host} must NOT set noindex, got "${robotsMeta}"`);
    }

    // Phase 4 canonical rule: PDP canonical points to the SITE'S OWN URL (not the root platform host!).
    for (const [sub, host, client, slug] of [
      [SITE_A_SUB, SITE_A_HOST, anonA, SHOP_A_SLUG],
      [SITE_B_SUB, SITE_B_HOST, anonB, SHOP_B_SLUG],
    ]) {
      const pdp = await client.page(`/san-pham/${slug}`);
      expectStatus(pdp, 200, `GET ${host}/san-pham/${slug}`);
      const canonical = extractCanonicalHref(pdp.text);
      assert.ok(canonical, `${host}/san-pham/${slug} must emit <link rel="canonical">`);
      const canonicalUrl = new URL(canonical);
      assert.equal(
        canonicalUrl.hostname.toLowerCase(),
        `${sub}.${ROOT}`.toLowerCase(),
        `Site PDP canonical MUST point to own site host ${sub}.${ROOT}, got ${canonical}`
      );
      assert.equal(
        canonicalUrl.pathname,
        `/san-pham/${slug}`,
        `Site PDP canonical pathname must be /san-pham/${slug}, got ${canonical}`
      );
    }

    // Host-specific /robots.txt & /sitemap.xml for Site A and Site B.
    const robotsA = await anonA.get("/robots.txt", { headers: { accept: "text/plain" } });
    expectStatus(robotsA, 200, `GET ${SITE_A_HOST}/robots.txt`);
    assert.ok(
      robotsA.text.includes(`${SITE_A_SUB}.${ROOT}`) && robotsA.text.includes("/sitemap.xml"),
      `Site A /robots.txt must reference ${SITE_A_SUB}.${ROOT}/sitemap.xml, got:\n${robotsA.text}`
    );

    const sitemapA = await anonA.get("/sitemap.xml", { headers: { accept: "application/xml" } });
    expectStatus(sitemapA, 200, `GET ${SITE_A_HOST}/sitemap.xml`);
    assert.ok(
      sitemapA.text.includes(`${SITE_A_SUB}.${ROOT}`) && sitemapA.text.includes(`/san-pham/${SHOP_A_SLUG}`),
      `Site A /sitemap.xml must include ${SITE_A_SUB}.${ROOT} and ${SHOP_A_SLUG}`
    );
    assert.equal(sitemapA.text.includes(SHOP_B_SLUG), false, "Site A /sitemap.xml leaked Shop B product slug!");

    const robotsB = await anonB.get("/robots.txt", { headers: { accept: "text/plain" } });
    expectStatus(robotsB, 200, `GET ${SITE_B_HOST}/robots.txt`);
    assert.ok(
      robotsB.text.includes(`${SITE_B_SUB}.${ROOT}`) && robotsB.text.includes("/sitemap.xml"),
      `Site B /robots.txt must reference ${SITE_B_SUB}.${ROOT}/sitemap.xml, got:\n${robotsB.text}`
    );

    const sitemapB = await anonB.get("/sitemap.xml", { headers: { accept: "application/xml" } });
    expectStatus(sitemapB, 200, `GET ${SITE_B_HOST}/sitemap.xml`);
    assert.ok(
      sitemapB.text.includes(`${SITE_B_SUB}.${ROOT}`) && sitemapB.text.includes(`/san-pham/${SHOP_B_SLUG}`),
      `Site B /sitemap.xml must include ${SITE_B_SUB}.${ROOT} and ${SHOP_B_SLUG}`
    );
    assert.equal(sitemapB.text.includes(SHOP_A_SLUG), false, "Site B /sitemap.xml leaked Shop A product slug!");
  });
});
