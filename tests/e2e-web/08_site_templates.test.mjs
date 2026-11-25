import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, originOf, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, pollUntil, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  assertNoTokenLeak,
  createEphemeralSeller,
  demoCredentials,
  loginOn,
  registerBuyer,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("08. Site templates discovery, apply-template, draft customization, publish, XSS/SVG/SSRF & RBAC/CSRF", () => {
  let discoveredTemplates = [];

  before(async () => {
    await resetRateLimitKeys();
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("a) Public/seller template discovery via GET /api/site-templates returns >= 8 distinct archetypes; detail & host-isolation checks", async () => {
    const anonRoot = new Browser("tpl-anon-root").at(ROOT_HOST);
    const anonSiteA = new Browser("tpl-anon-site-a").at(SITE_A_HOST);

    // 1. Public GET /api/site-templates on ROOT_HOST -> 200 with >= 8 templates and >= 8 distinct archetypes.
    const listRes = await anonRoot.get("/api/site-templates");
    const listData = expectStatus(listRes, 200, `GET ${ROOT_HOST}/api/site-templates`);
    assertNoTokenLeak(listRes.text, "GET /api/site-templates");
    assertNoInternalErrorLeak(listRes.text, "GET /api/site-templates");

    const templates = listData.templates ?? [];
    assert.ok(Array.isArray(templates), "templates must be an array");
    assert.ok(templates.length >= 8, `expected >= 8 platform site templates, got ${templates.length}`);
    assert.equal(
      listData.source,
      "gateway",
      `expected live gateway templates (source=gateway), got source=${listData.source}`
    );

    const archetypes = new Set();
    const ids = new Set();
    for (const tpl of templates) {
      assert.ok(typeof tpl.id === "string" && tpl.id.length > 0, "template must have id");
      assert.ok(typeof tpl.name === "string" && tpl.name.length > 0, `template ${tpl.id} must have name`);
      assert.ok(typeof tpl.archetype === "string" && tpl.archetype.length > 0, `template ${tpl.id} must have archetype`);
      assert.equal(tpl.config?.schema_version, 2, `template ${tpl.id} config.schema_version must be 2`);
      assert.equal(
        tpl.config?.layout?.archetype,
        tpl.archetype,
        `template ${tpl.id} layout.archetype must match template.archetype`
      );
      ids.add(tpl.id);
      archetypes.add(tpl.archetype);
    }
    assert.equal(ids.size, templates.length, "template IDs must be unique");
    assert.ok(
      archetypes.size >= 8,
      `expected >= 8 distinct template archetypes, got ${archetypes.size}: ${[...archetypes].join(", ")}`
    );
    discoveredTemplates = templates;

    // 2. Detail route GET /api/site-templates/[id] returns matching template; unknown returns 404.
    const sample = templates[0];
    const detailRes = await anonRoot.get(`/api/site-templates/${encodeURIComponent(sample.id)}`);
    const detailData = expectStatus(detailRes, 200, `GET ${ROOT_HOST}/api/site-templates/${sample.id}`);
    assert.equal(detailData.template?.id, sample.id);
    assert.equal(detailData.template?.archetype, sample.archetype);

    const missingRes = await anonRoot.get("/api/site-templates/khong-ton-tai-e2e-tpl");
    expectStatus(missingRes, 404, "GET unknown site template must return 404");

    // 3. Site hosts must block /api/site-templates and /api/seller/site/apply-template with 404.
    expectStatus(await anonSiteA.get("/api/site-templates"), 404, `GET ${SITE_A_HOST}/api/site-templates`);
    expectStatus(
      await anonSiteA.post("/api/seller/site/apply-template", { template_id: sample.id }),
      404,
      `POST ${SITE_A_HOST}/api/seller/site/apply-template`
    );
  });

  it("b) Ephemeral seller applies template, customizes draft, publishes live site, validates XSS/SVG/SSRF rejection, RBAC & CSRF (always suspends in finally)", async () => {
    if (discoveredTemplates.length < 2) {
      const res = await new Browser("tpl-fallback-list").at(ROOT_HOST).get("/api/site-templates");
      const data = expectStatus(res, 200, "GET /api/site-templates");
      discoveredTemplates = data.templates ?? [];
    }
    assert.ok(discoveredTemplates.length >= 2, "must have >= 2 templates available");

    const primaryTpl =
      discoveredTemplates.find((t) => t.id === "editorial") ?? discoveredTemplates[0];
    const secondaryTpl =
      discoveredTemplates.find((t) => t.id !== primaryTpl.id && t.archetype !== primaryTpl.archetype) ??
      discoveredTemplates[1];

    const runTag = uid().toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 10);
    const subTest = `e2e-tpl-${runTag}`;
    const testHost = siteHost(subTest);

    const sellerA = await createEphemeralSeller("tpl-owner", ROOT_HOST);
    const sellerHanoi = new Browser("tpl-seller-hanoi").at(ROOT_HOST);
    const admin = new Browser("tpl-admin").at(ROOT_HOST);
    const buyerBrowser = new Browser("tpl-buyer");
    const buyerSiteA = buyerBrowser.at(SITE_A_HOST);
    const buyerRoot = buyerBrowser.at(ROOT_HOST);

    const siteProbe = new Browser("tpl-site-probe").at(testHost);
    let createdSiteId = "";

    try {
      await loginOn(sellerHanoi, demoCredentials.sellerHanoi.email, demoCredentials.sellerHanoi.password, "tpl-seller-hanoi");
      await loginOn(admin, demoCredentials.admin.email, demoCredentials.admin.password, "tpl-admin");
      const buyer = await registerBuyer(buyerSiteA, "tpl-buyer");
      await loginOn(buyerRoot, buyer.creds.email, buyer.creds.password, "tpl-buyer-root");

      // 1. Ephemeral seller applies primaryTpl to brand-new site draft.
      const applyRes = await sellerA.client.post("/api/seller/site/apply-template", {
        template_id: primaryTpl.id,
        subdomain: subTest,
      });
      const applyData = expectStatus(applyRes, 200, `POST /api/seller/site/apply-template (${primaryTpl.id})`);
      assertNoTokenLeak(applyRes.text, "POST /api/seller/site/apply-template");
      assertNoInternalErrorLeak(applyRes.text, "POST /api/seller/site/apply-template");

      assert.ok(applyData.site?.id, "apply-template must return site.id");
      createdSiteId = applyData.site.id;
      assert.equal(applyData.template_id, primaryTpl.id);
      assert.equal(applyData.site.status, "DRAFT");
      assert.equal(applyData.site.subdomain, subTest);
      assert.equal(applyData.site.draft_config?.schema_version, 2);
      assert.equal(applyData.site.draft_config?.template_id, primaryTpl.id);
      assert.equal(applyData.site.draft_config?.layout?.archetype, primaryTpl.archetype);

      // Verify GET /api/seller/site reflects the applied template draft.
      const getSiteRes = await sellerA.client.get("/api/seller/site");
      const getSiteData = expectStatus(getSiteRes, 200, "GET /api/seller/site after apply-template");
      assert.equal(getSiteData.site?.id, createdSiteId);
      assert.equal(getSiteData.site?.draft_config?.template_id, primaryTpl.id);
      assert.equal(getSiteData.site?.draft_config?.layout?.archetype, primaryTpl.archetype);

      // 2. Customize draft via PUT /api/seller/site/draft.
      const customBrand = `Atelier Mau ${runTag}`;
      const customHeroTitle = `Tieu De Template ${runTag}`;
      const customizedConfig = structuredClone(applyData.site.draft_config);
      customizedConfig.theme.brand_name = customBrand;
      customizedConfig.theme.tagline = `Gian hang kiem thu template ${primaryTpl.archetype} ${runTag}`;
      const heroSec = (customizedConfig.sections ?? []).find((s) => s.type === "hero");
      if (heroSec && heroSec.props) {
        heroSec.props.title = customHeroTitle;
      }

      const saveRes = await sellerA.client.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: customizedConfig,
      });
      const saveData = expectStatus(saveRes, 200, "PUT /api/seller/site/draft (customized template)");
      assertNoInternalErrorLeak(saveRes.text, "PUT /api/seller/site/draft");
      assert.equal(saveData.site?.draft_config?.theme?.brand_name, customBrand);
      assert.equal(saveData.site?.draft_config?.layout?.archetype, primaryTpl.archetype);

      // 3. Publish via POST /api/seller/site/publish and verify live site renders 200 with template layout.
      const pubRes = await sellerA.client.post("/api/seller/site/publish", {
        note: `Publish template ${primaryTpl.id}`,
      });
      const pubData = expectStatus(pubRes, 200, "POST /api/seller/site/publish");
      assert.equal(pubData.site?.status, "PUBLISHED");
      const publishedVersionId = pubData.version?.id;
      assert.ok(publishedVersionId, "publish must return version.id");

      const liveHtml = await pollUntil(
        `live ${testHost} renders 200 with archetype=${primaryTpl.archetype}`,
        async () => {
          const r = await siteProbe.page("/");
          const ok =
            r.status === 200 &&
            r.text.includes(`data-site-archetype="${primaryTpl.archetype}"`) &&
            r.text.includes(customBrand);
          return [ok, `status=${r.status}`, r];
        },
        { timeoutMs: 5000, intervalMs: 250 }
      );
      assertNoTokenLeak(liveHtml.text, `GET ${testHost}/`);
      assertNoInternalErrorLeak(liveHtml.text, `GET ${testHost}/`);
      assert.ok(
        liveHtml.text.includes(`data-site-archetype="${primaryTpl.archetype}"`),
        `live site must render data-site-archetype="${primaryTpl.archetype}"`
      );
      assert.ok(
        liveHtml.text.includes(`data-site-header="${customizedConfig.layout.header_style}"`),
        `live site must render data-site-header="${customizedConfig.layout.header_style}"`
      );
      assert.ok(
        liveHtml.text.includes(`data-site-footer="${customizedConfig.layout.footer_style}"`),
        `live site must render data-site-footer="${customizedConfig.layout.footer_style}"`
      );
      assert.ok(liveHtml.text.includes(customBrand), "live site must render customized brand_name");
      if (heroSec) {
        assert.ok(liveHtml.text.includes(customHeroTitle), "live site must render customized hero title");
      }

      // Applying a second template while PUBLISHED updates only draft, never mutating live published output.
      const switchRes = await sellerA.client.post("/api/seller/site/apply-template", {
        template_id: secondaryTpl.id,
      });
      const switchData = expectStatus(switchRes, 200, `POST /api/seller/site/apply-template (${secondaryTpl.id})`);
      assert.equal(switchData.site?.status, "PUBLISHED", "apply-template must preserve PUBLISHED status");
      assert.equal(switchData.site?.draft_config?.template_id, secondaryTpl.id, "draft must switch to secondary template");

      const stillPrimaryLive = await siteProbe.page("/");
      expectStatus(stillPrimaryLive, 200, `GET ${testHost}/ after draft template switch`);
      assert.ok(
        stillPrimaryLive.text.includes(`data-site-archetype="${primaryTpl.archetype}"`),
        "unpublished template switch must NOT mutate live published layout"
      );

      // Unknown / malformed template IDs on apply-template.
      expectStatus(
        await sellerA.client.post("/api/seller/site/apply-template", { template_id: "khong-ton-tai-e2e" }),
        404,
        "apply unknown template_id must return 404"
      );
      expectStatus(
        await sellerA.client.post("/api/seller/site/apply-template", { template_id: "../etc/passwd" }),
        400,
        "apply malformed template_id must return 400"
      );

      // 4. Security validation: XSS, javascript:, onerror=, .svg, SSRF (127.0.0.1 / metadata) MUST be rejected with 400.
      const cloneWith = (mutator) => {
        const copy = structuredClone(customizedConfig);
        mutator(copy);
        return copy;
      };

      const badPayloads = [
        [
          "XSS <script> in tagline",
          cloneWith((c) => {
            c.theme.tagline = 'Gioi thieu <script>alert("xss")</script>';
          }),
        ],
        [
          "javascript: URL in theme.logo_url",
          cloneWith((c) => {
            c.theme.logo_url = "javascript:alert(1)";
          }),
        ],
        [
          "javascript: URL in hero cta_href",
          cloneWith((c) => {
            const h = c.sections.find((s) => s.type === "hero");
            if (h) h.props.cta_href = "javascript:alert(1)";
            else c.theme.banner_url = "javascript:alert(1)";
          }),
        ],
        [
          "inline event handler onerror= in tagline",
          cloneWith((c) => {
            c.theme.tagline = "Hinh anh onerror=alert(1)";
          }),
        ],
        [
          "SVG image URL (external .svg) in logo_url",
          cloneWith((c) => {
            c.theme.logo_url = "https://cdn.example.com/assets/logo.svg";
          }),
        ],
        [
          "SVG image URL (relative .svg) in banner_url",
          cloneWith((c) => {
            c.theme.banner_url = "/banners/evil.svg";
          }),
        ],
        [
          "SSRF http://127.0.0.1/... in banner_url",
          cloneWith((c) => {
            c.theme.banner_url = "http://127.0.0.1/internal/admin.png";
          }),
        ],
        [
          "SSRF https://127.0.0.1/... in logo_url",
          cloneWith((c) => {
            c.theme.logo_url = "https://127.0.0.1/secret.png";
          }),
        ],
        [
          "SSRF metadata host in banner_url",
          cloneWith((c) => {
            c.theme.banner_url = "https://169.254.169.254/latest/meta-data/img.png";
          }),
        ],
      ];

      for (const [label, badConfig] of badPayloads) {
        const res = await sellerA.client.put("/api/seller/site/draft", {
          subdomain: subTest,
          config: badConfig,
        });
        expectStatus(res, 400, `PUT /api/seller/site/draft (${label}) must return 400`);
        assertNoInternalErrorLeak(res.text, `PUT /api/seller/site/draft (${label})`);
      }

      // 5. RBAC: anon -> 401; buyer -> 403; cross-seller cannot read/hijack/rollback sellerA's site.
      const anonRoot = new Browser("tpl-rbac-anon").at(ROOT_HOST);
      expectStatus(
        await anonRoot.post("/api/seller/site/apply-template", { template_id: primaryTpl.id }),
        401,
        "anon POST /api/seller/site/apply-template"
      );
      expectStatus(
        await anonRoot.put("/api/seller/site/draft", { subdomain: subTest, config: customizedConfig }),
        401,
        "anon PUT /api/seller/site/draft"
      );
      expectStatus(
        await anonRoot.post("/api/seller/site/publish", { note: "anon" }),
        401,
        "anon POST /api/seller/site/publish"
      );

      expectStatus(
        await buyerRoot.post("/api/seller/site/apply-template", { template_id: primaryTpl.id }),
        403,
        "buyer POST /api/seller/site/apply-template"
      );
      expectStatus(
        await buyerRoot.put("/api/seller/site/draft", { subdomain: subTest, config: customizedConfig }),
        403,
        "buyer PUT /api/seller/site/draft"
      );
      expectStatus(
        await buyerRoot.post("/api/seller/site/publish", { note: "buyer" }),
        403,
        "buyer POST /api/seller/site/publish"
      );

      // Cross-seller isolation (sellerHanoi cannot view, hijack subdomain, or rollback sellerA's site).
      const otherSellerSite = expectStatus(
        await sellerHanoi.get(`/api/seller/site?shop_id=${encodeURIComponent(sellerA.shopId)}`),
        200,
        "sellerHanoi GET /api/seller/site?shop_id=sellerA"
      );
      assert.notEqual(otherSellerSite.site?.id, createdSiteId, "cross-seller GET /api/seller/site leaked sellerA site");

      expectStatus(
        await sellerHanoi.put("/api/seller/site/draft", {
          subdomain: subTest,
          config: customizedConfig,
        }),
        409,
        "cross-seller PUT /api/seller/site/draft colliding with sellerA subdomain"
      );
      expectStatus(
        await sellerHanoi.post("/api/seller/site/rollback", {
          version_id: publishedVersionId,
          note: "cross-seller rollback",
        }),
        [403, 404],
        "cross-seller POST /api/seller/site/rollback on sellerA version"
      );

      // 6. CSRF: mismatched / evil / missing Origin & cross-site Fetch-Metadata -> 403 BAD_ORIGIN.
      const csrfChecks = [
        [
          "POST /api/seller/site/apply-template (site host Origin)",
          () =>
            sellerA.client.post(
              "/api/seller/site/apply-template",
              { template_id: primaryTpl.id },
              { origin: originOf(testHost) }
            ),
        ],
        [
          "POST /api/seller/site/apply-template (evil Origin)",
          () =>
            sellerA.client.post(
              "/api/seller/site/apply-template",
              { template_id: primaryTpl.id },
              { origin: "https://evil.example" }
            ),
        ],
        [
          "PUT /api/seller/site/draft (missing Origin)",
          () =>
            sellerA.client.put(
              "/api/seller/site/draft",
              { subdomain: subTest, config: customizedConfig },
              { origin: null }
            ),
        ],
        [
          "POST /api/seller/site/publish (Sec-Fetch-Site: cross-site)",
          () =>
            sellerA.client.post(
              "/api/seller/site/publish",
              { note: "csrf publish" },
              { headers: { "sec-fetch-site": "cross-site" } }
            ),
        ],
      ];
      for (const [label, fn] of csrfChecks) {
        const res = await fn();
        expectStatus(res, 403, `CSRF ${label}`);
        assert.equal(res.data?.error?.code, "BAD_ORIGIN", `CSRF ${label} expected BAD_ORIGIN`);
        assertNoInternalErrorLeak(res.text, `CSRF ${label}`);
      }
    } finally {
      // Always suspend the ephemeral site so repeated runs never pollute the public site directory.
      if (createdSiteId) {
        await admin
          .post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
            suspend: true,
            reason: "E2E web template cleanup",
          })
          .catch(() => {});
      }
      await Promise.allSettled([
        sellerA.client.post("/api/auth/logout", {}),
        sellerHanoi.post("/api/auth/logout", {}),
        admin.post("/api/auth/logout", {}),
        buyerSiteA.post("/api/auth/logout", {}),
        buyerRoot.post("/api/auth/logout", {}),
      ]);
    }
  });
});
