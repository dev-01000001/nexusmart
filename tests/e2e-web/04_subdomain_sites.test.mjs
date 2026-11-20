import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import https from "node:https";
import { config } from "./lib/config.mjs";
import { WebSession, expectStatus, pollUntil, uid } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import { waitForOTP } from "./lib/mailpit.mjs";
import {
  assertNoInternalErrorLeak,
  createShippingAddress,
  demoCredentials,
  getShippingChoicesForCart,
  loginAccount,
  newIdempotencyKey,
  registerVerifyLoginBuyer,
  uniqueBuyerInput,
} from "./lib/fixtures.mjs";

const HTML_HEADERS = { accept: "text/html,application/xhtml+xml" };
const ROOT_DOMAIN = config.siteRootDomain;
const SITE_A_SUB = "bach-dang";
const SITE_B_SUB = "sai-gon";
const SITE_A_HOST = `${SITE_A_SUB}.${ROOT_DOMAIN}`;
const SITE_B_HOST = `${SITE_B_SUB}.${ROOT_DOMAIN}`;

const SHOP_A_SLUG = "ao-thun-cotton-compact-trang";
const SHOP_B_SLUG = "ao-thun-the-thao-dryfit-xanh-co-vit";
const SHOP_B_SLUG_2 = "quan-short-daily-cotton-7-inch-be";

const SELLER_HANOI_EMAIL = process.env.SEED_SELLER_HANOI_EMAIL || "seller-hanoi@nexusmart.local";
const SELLER_PASSWORD = process.env.SEED_SELLER_PASSWORD || "SellerDemo123!";

/**
 * Raw HTTP(S) client using `node:http` / `node:https` so a custom `Host` header
 * is transmitted verbatim (Node's built-in undici `fetch` overwrites `Host`).
 */
function requestWithHost(hostHeader, path, { method = "GET", headers = {}, body } = {}) {
  const target = new URL(path, config.webURL);
  const lib = target.protocol === "https:" ? https : http;
  const reqHeaders = {
    accept: "text/html,application/xhtml+xml,*/*",
    ...headers,
  };
  if (hostHeader) {
    reqHeaders.Host = hostHeader;
  }
  let payload;
  if (body !== undefined) {
    payload = typeof body === "string" ? body : JSON.stringify(body);
    if (!reqHeaders["content-type"]) reqHeaders["content-type"] = "application/json";
    reqHeaders["content-length"] = Buffer.byteLength(payload);
  }

  return new Promise((resolve, reject) => {
    const req = lib.request(
      {
        protocol: target.protocol,
        hostname: target.hostname,
        port: target.port || (target.protocol === "https:" ? 443 : 80),
        path: `${target.pathname}${target.search}`,
        method,
        headers: reqHeaders,
        timeout: config.httpTimeoutMs,
      },
      (res) => {
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          const text = Buffer.concat(chunks).toString("utf8");
          let data;
          try {
            data = text ? JSON.parse(text) : undefined;
          } catch {
            data = undefined;
          }
          resolve({
            status: res.statusCode ?? 0,
            headers: res.headers,
            text,
            data,
          });
        });
      }
    );
    req.on("timeout", () => req.destroy(new Error(`requestWithHost(${hostHeader}, ${path}) timed out`)));
    req.on("error", reject);
    if (payload !== undefined) req.write(payload);
    req.end();
  });
}

async function gwJson(method, path, { token, body } = {}) {
  const headers = { accept: "application/json" };
  if (token) headers.authorization = `Bearer ${token}`;
  let payload;
  if (body !== undefined) {
    headers["content-type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${config.gatewayURL}${path}`, {
    method,
    headers,
    body: payload,
    signal: AbortSignal.timeout(config.httpTimeoutMs),
  });
  const text = await res.text();
  let data;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  return { status: res.status, text, data };
}

function extractMetaContent(html, name) {
  const metas = html.match(/<meta\b[^>]*>/gi) ?? [];
  for (const tag of metas) {
    const nameMatch = tag.match(/\bname=["']([^"']+)["']/i);
    if (nameMatch && nameMatch[1].toLowerCase() === name.toLowerCase()) {
      const contentMatch = tag.match(/\bcontent=["']([^"']*)["']/i);
      if (contentMatch) return contentMatch[1];
    }
  }
  return null;
}

function extractCanonicalHref(html) {
  const links = html.match(/<link\b[^>]*>/gi) ?? [];
  for (const tag of links) {
    if (/\brel=["']canonical["']/i.test(tag)) {
      const hrefMatch = tag.match(/\bhref=["']([^"']+)["']/i);
      if (hrefMatch) return hrefMatch[1];
    }
  }
  return null;
}

function makeSiteConfig({ brandName, tagline, heroTitle, accentColor = "#1D4ED8", sections }) {
  return {
    theme: {
      bg_color: "#F8F6F1",
      surface_color: "#FFFFFF",
      text_color: "#182026",
      accent_color: accentColor,
      accent_text_color: "#FFFFFF",
      font_family: "work-sans",
      logo_url: "/banners/vietnam-workshop.jpg",
      banner_url: "/banners/vietnam-workshop.jpg",
      brand_name: brandName,
      tagline: tagline ?? "Gian hàng chính hãng vận hành trên NexusMart",
    },
    sections:
      sections ??
      [
        {
          id: "hero-e2e",
          type: "hero",
          visible: true,
          props: {
            eyebrow: "E2E SITE",
            title: heroTitle,
            subtitle: "Nội dung kiểm thử vòng đời xuất bản site riêng",
            image_url: "/banners/vietnam-workshop.jpg",
            cta_label: "Xem sản phẩm",
            cta_href: "/san-pham",
          },
        },
        {
          id: "text-e2e",
          type: "text",
          visible: true,
          props: {
            title: "Giới thiệu gian hàng",
            body: "Sản phẩm minh bạch tồn kho và thanh toán tập trung trên NexusMart.",
            align: "left",
          },
        },
      ],
  };
}

/**
 * Registers & approves an ephemeral seller + ACTIVE shop via Gateway + Mailpit,
 * then signs into the Web BFF so every lifecycle test starts with a brand-new shop
 * that has ZERO existing site rows (100% idempotent across repeated `make e2e-web` runs).
 */
async function createEphemeralSellerViaBff(label) {
  const creds = uniqueBuyerInput(label);
  const regRes = await gwJson("POST", "/api/v1/auth/register", {
    body: {
      full_name: creds.fullName,
      email: creds.email,
      phone: creds.phone,
      password: creds.password,
    },
  });
  assert.ok([200, 201].includes(regRes.status), `register ephemeral seller: ${regRes.status} ${regRes.text}`);

  const otp = await waitForOTP(creds.email);
  const verifyRes = await gwJson("POST", "/api/v1/auth/verify-otp", {
    body: { email: creds.email, otp },
  });
  assert.equal(verifyRes.status, 200, `verify ephemeral seller OTP: ${verifyRes.text}`);

  const login1 = await gwJson("POST", "/api/v1/auth/login", {
    body: { email: creds.email, password: creds.password },
  });
  assert.equal(login1.status, 200, `gateway login ephemeral seller: ${login1.text}`);
  const userToken = login1.data.access_token;

  const provRes = await gwJson("GET", "/api/v1/shipping/provinces");
  assert.equal(provRes.status, 200);
  const prov = provRes.data.provinces[0];
  const wardRes = await gwJson("GET", `/api/v1/shipping/provinces/${encodeURIComponent(prov.code)}/wards`);
  assert.equal(wardRes.status, 200);
  const ward = wardRes.data.wards[0];

  const addrRes = await gwJson("POST", "/api/v1/users/me/addresses", {
    token: userToken,
    body: {
      recipient_name: creds.fullName,
      recipient_phone: creds.phone,
      province_code: prov.code,
      province_name: prov.name,
      ward_code: ward.code,
      ward_name: ward.name,
      street_address: "18 Đường Kiểm Thử Site",
      is_default: true,
    },
  });
  assert.ok([200, 201].includes(addrRes.status), `create pickup address: ${addrRes.text}`);

  const shopName = `SiteE2E-${uid()}`;
  const shopRes = await gwJson("POST", "/api/v1/shops/register", {
    token: userToken,
    body: {
      name: shopName,
      description: "Ephemeral shop for Phase 2 Web E2E site lifecycle",
      pickup_address_id: addrRes.data.address.id,
      contact_phone: creds.phone,
      business_license_doc: "https://sandbox.nexusmart.local/docs/e2e-site.pdf",
    },
  });
  assert.ok([200, 201].includes(shopRes.status), `register ephemeral shop: ${shopRes.text}`);
  const shopId = shopRes.data.shop.id;

  const adminGw = await gwJson("POST", "/api/v1/auth/login", {
    body: { email: demoCredentials.admin.email, password: demoCredentials.admin.password },
  });
  assert.equal(adminGw.status, 200, `admin gateway login: ${adminGw.text}`);

  const approveRes = await gwJson("POST", `/api/v1/admin/shops/${encodeURIComponent(shopId)}/review`, {
    token: adminGw.data.access_token,
    body: { decision: "APPROVE", reason: "E2E web ephemeral site shop approval" },
  });
  assert.equal(approveRes.status, 200, `approve ephemeral shop: ${approveRes.text}`);

  // Log in via Web BFF so session has fresh seller cookies carrying role="seller" + shop_id.
  const bff = await loginAccount(creds.email, creds.password, `ephemeral-${label}`);
  assert.equal(bff.user.role, "seller", "ephemeral seller must have role=seller");
  assert.equal(bff.user.shopId || bff.user.shop_id, shopId, "ephemeral seller must have matching shopId");

  return { session: bff.session, user: bff.user, shopId, shopName, creds };
}

describe("04. Phase 2 Multi-Tenant Subdomain Sites E2E (Isolation, Lifecycle, Security, Checkout Handoff & SEO)", () => {
  let siteABackend;
  let siteBBackend;
  let daNangShopId;
  let shopASkuId;

  before(async () => {
    await resetRateLimitKeys();

    // Discover real backend IDs for seeded sites (bach-dang & sai-gon) and unpublished shop (Đà Nẵng).
    const [resA, resB, shopsRes] = await Promise.all([
      gwJson("GET", `/api/v1/sites/by-host?host=${encodeURIComponent(SITE_A_HOST)}`),
      gwJson("GET", `/api/v1/sites/by-host?host=${encodeURIComponent(SITE_B_HOST)}`),
      gwJson("GET", "/api/v1/shops?page=1&page_size=100"),
    ]);
    assert.equal(
      resA.status,
      200,
      `Seeded site ${SITE_A_HOST} must be PUBLISHED (run make seed first): ${resA.status} ${resA.text}`
    );
    assert.equal(
      resB.status,
      200,
      `Seeded site ${SITE_B_HOST} must be PUBLISHED (run make seed first): ${resB.status} ${resB.text}`
    );
    siteABackend = resA.data;
    siteBBackend = resB.data;

    const daNangShop = (shopsRes.data?.items ?? []).find((s) => s.name === "Sợi Tự Nhiên Đà Nẵng");
    assert.ok(daNangShop?.id, "Seeded shop 'Sợi Tự Nhiên Đà Nẵng' must exist");
    daNangShopId = daNangShop.id;

    // Pick an in-stock SKU belonging strictly to Shop A (bach-dang).
    const prodARes = await gwJson(
      "GET",
      `/api/v1/products?shop_id=${encodeURIComponent(siteABackend.shop.id)}&page=1&page_size=20`
    );
    assert.equal(prodARes.status, 200);
    for (const p of prodARes.data?.items ?? []) {
      const sku = (p.skus ?? []).find((k) => k.is_active !== false && k.available_stock >= 2);
      if (sku) {
        shopASkuId = sku.id;
        break;
      }
    }
    assert.ok(shopASkuId, "Shop A (bach-dang) must have at least one in-stock SKU");
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("1. Unknown, foreign, reserved, and multi-level hosts return HTTP 404", async () => {
    const badHosts = [
      `unknown-shop.${ROOT_DOMAIN}`,
      "evil.example.com",
      `a.b.${ROOT_DOMAIN}`,
      `admin.${ROOT_DOMAIN}`,
      `www.${ROOT_DOMAIN}`,
      `vietcombank-uudai.${ROOT_DOMAIN}`,
    ];
    for (const host of badHosts) {
      const res = await requestWithHost(host, "/");
      assert.equal(res.status, 404, `Host ${host} must return 404, got ${res.status}`);
    }
  });

  it("2. Site A & Site B isolate products/vouchers, enforce 404 on cross-shop PDP, and render distinct themes", async () => {
    // Site A (bach-dang): homepage, /san-pham, PDP, /chinh-sach, /gioi-thieu all 200.
    const homeA = await requestWithHost(SITE_A_HOST, "/");
    assert.equal(homeA.status, 200, `Site A / expected 200, got ${homeA.status}`);
    assert.match(
      homeA.text,
      /Vận hành trên[\s\S]{0,120}NexusMart/,
      "Site A footer must display 'Vận hành trên NexusMart'"
    );
    assert.ok(homeA.text.includes("Xưởng Dệt Bạch Đằng"), "Site A homepage must render Shop A brand");
    assert.ok(homeA.text.includes("BACHDANG20"), "Site A homepage must display Shop A SHOP voucher BACHDANG20");

    // Site A must NEVER leak Shop B products, Shop B vouchers, or platform/freeship vouchers.
    for (const forbidden of [
      SHOP_B_SLUG,
      SHOP_B_SLUG_2,
      "Kho Basic Sài Gòn",
      "SAIGON25",
      "DANANG30",
      "HANOI15",
      "NEXUS30",
      "FREESHIP25",
    ]) {
      assert.equal(
        homeA.text.includes(forbidden),
        false,
        `Site A homepage leaked forbidden foreign/platform item: ${forbidden}`
      );
    }

    const listA = await requestWithHost(SITE_A_HOST, "/san-pham");
    assert.equal(listA.status, 200, `Site A /san-pham expected 200, got ${listA.status}`);
    assert.ok(listA.text.includes(SHOP_A_SLUG), "Site A /san-pham must include Shop A product slug");
    assert.equal(listA.text.includes(SHOP_B_SLUG), false, "Site A /san-pham must NOT leak Shop B product");

    const pdpA = await requestWithHost(SITE_A_HOST, `/san-pham/${SHOP_A_SLUG}`);
    assert.equal(pdpA.status, 200, `Site A PDP expected 200, got ${pdpA.status}`);
    assert.ok(pdpA.text.includes("Xưởng Dệt Bạch Đằng"), "Site A PDP must show Shop A branding");
    assert.ok(pdpA.text.includes("channel=storefront"), "Site A PDP must include storefront cart handoff link");

    const policiesA = await requestWithHost(SITE_A_HOST, "/chinh-sach");
    assert.equal(policiesA.status, 200, `Site A /chinh-sach expected 200, got ${policiesA.status}`);
    const aboutA = await requestWithHost(SITE_A_HOST, "/gioi-thieu");
    assert.equal(aboutA.status, 200, `Site A /gioi-thieu expected 200, got ${aboutA.status}`);

    // Cross-shop PDP isolation: Shop B slug on Site A -> 404!
    const crossAtoB = await requestWithHost(SITE_A_HOST, `/san-pham/${SHOP_B_SLUG}`);
    assert.equal(crossAtoB.status, 404, `Shop B slug on Site A must return 404, got ${crossAtoB.status}`);

    // Site B (sai-gon): 200, own branding & vouchers, zero Shop A leakage, cross-shop PDP -> 404.
    const homeB = await requestWithHost(SITE_B_HOST, "/");
    assert.equal(homeB.status, 200, `Site B / expected 200, got ${homeB.status}`);
    assert.ok(homeB.text.includes("Kho Basic Sài Gòn"), "Site B homepage must render Shop B brand");
    assert.ok(homeB.text.includes("SAIGON25"), "Site B homepage must render Shop B voucher SAIGON25");
    assert.equal(homeB.text.includes("BACHDANG20"), false, "Site B must NOT leak Shop A voucher");
    assert.equal(homeB.text.includes(SHOP_A_SLUG), false, "Site B must NOT leak Shop A product");

    const pdpB = await requestWithHost(SITE_B_HOST, `/san-pham/${SHOP_B_SLUG}`);
    assert.equal(pdpB.status, 200, `Site B PDP expected 200, got ${pdpB.status}`);

    const crossBtoA = await requestWithHost(SITE_B_HOST, `/san-pham/${SHOP_A_SLUG}`);
    assert.equal(crossBtoA.status, 404, `Shop A slug on Site B must return 404, got ${crossBtoA.status}`);

    // Distinct theme tokens/colors between Site A (light #F8F6F1 / #1D4ED8) and Site B (dark #14181C / #D97706).
    assert.ok(
      homeA.text.includes('data-site-theme-mode="light"') && homeA.text.includes("#F8F6F1") && homeA.text.includes("#1D4ED8"),
      "Site A must render light theme tokens (#F8F6F1 / #1D4ED8)"
    );
    assert.ok(
      homeB.text.includes('data-site-theme-mode="dark"') && homeB.text.includes("#14181C") && homeB.text.includes("#D97706"),
      "Site B must render dark theme tokens (#14181C / #D97706)"
    );
  });

  it("3 & 4. Direct /s/<host> returns 404 on every host; /admin, /seller, and /api/* return 404 on site host", async () => {
    // Direct /s/... on root host AND on site host -> 404.
    const rootInternalPaths = ["/s", `/s/${SITE_A_HOST}`, `/s/${SITE_A_HOST}/san-pham`];
    for (const p of rootInternalPaths) {
      const res = await requestWithHost(null, p);
      assert.equal(res.status, 404, `Direct root access to ${p} must return 404, got ${res.status}`);
    }
    const siteInternal = await requestWithHost(SITE_A_HOST, `/s/${SITE_A_HOST}`);
    assert.equal(siteInternal.status, 404, `Direct site access to /s/${SITE_A_HOST} must return 404`);

    // Blocked routes on site host -> 404.
    const blockedOnSite = [
      "/admin",
      "/admin/sites",
      "/seller",
      "/seller/site",
      "/api/auth/me",
      "/api/cart",
      "/api/seller/site",
    ];
    for (const p of blockedOnSite) {
      const res = await requestWithHost(SITE_A_HOST, p);
      assert.equal(res.status, 404, `Blocked path ${p} on ${SITE_A_HOST} must return 404, got ${res.status}`);
    }
  });

  it("5, 6, 7 & 9b. Ephemeral shop draft lifecycle, preview RBAC, security validation, XSS escaping, admin suspend/unsuspend, and empty-sections noindex", async () => {
    await resetRateLimitKeys();

    const runTag = uid().toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 10);
    const subTest = `e2e-web-${runTag}`;
    const testHost = `${subTest}.${ROOT_DOMAIN}`;

    const sellerA = await createEphemeralSellerViaBff("site-owner");
    const sellerB = await loginAccount(SELLER_HANOI_EMAIL, SELLER_PASSWORD, "seller-hanoi-b");
    const admin = await loginAccount(demoCredentials.admin.email, demoCredentials.admin.password, "admin-sites");
    let createdSiteId = "";

    try {
      // --- 5a. Save draft v1 -> public host returns 404 (not leaked), owner preview returns 200 + noindex ---
      const v1Hero = `Tieu De Ban Nhap V1 ${runTag}`;
      const v2Hero = `Tieu De Phien Ban V2 ${runTag}`;
      const draftV1Cfg = makeSiteConfig({
        brandName: `Thu Nghiem ${runTag}`,
        heroTitle: v1Hero,
        accentColor: "#1D4ED8",
      });

      const saveV1Res = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: draftV1Cfg,
      });
      const saveV1 = expectStatus(saveV1Res, 200, "PUT /api/seller/site/draft (v1)");
      assert.equal(saveV1.site.status, "DRAFT");
      assert.equal(saveV1.site.subdomain, subTest);
      createdSiteId = saveV1.site.id;

      // Public host MUST return 404 while site is DRAFT.
      const pubDraftRes = await requestWithHost(testHost, "/");
      assert.equal(pubDraftRes.status, 404, `Unpublished draft host ${testHost} must return 404, got ${pubDraftRes.status}`);

      // Owner preview /seller/site/xem-truoc -> 200 with noindex meta + draft v1 title!
      const previewOwner = await sellerA.session.get("/seller/site/xem-truoc", { headers: HTML_HEADERS });
      expectStatus(previewOwner, 200, "GET /seller/site/xem-truoc (owner)");
      const previewRobots = extractMetaContent(previewOwner.text, "robots") ?? "";
      assert.match(previewRobots, /noindex/i, `Owner preview must set noindex meta, got "${previewRobots}"`);
      assert.ok(previewOwner.text.includes(v1Hero), "Owner preview must render draft v1 hero title");

      // Seller site editor page /seller/site -> 200.
      const editorPage = await sellerA.session.get("/seller/site", { headers: HTML_HEADERS });
      expectStatus(editorPage, 200, "GET /seller/site (owner)");

      // Unauthenticated preview redirects to /dang-nhap.
      const anon = new WebSession("anon-preview");
      const previewAnon = await anon.get("/seller/site/xem-truoc", { headers: HTML_HEADERS });
      expectStatus(previewAnon, [302, 303, 307, 308], "GET /seller/site/xem-truoc (unauthenticated)");
      const redirLoc = previewAnon.headers.get("location") ?? "";
      assert.ok(redirLoc.includes("/dang-nhap"), `Unauth preview must redirect to /dang-nhap, got ${redirLoc}`);

      // Seller B isolation: cannot view Seller A's draft via BFF or preview (even with ?shop_id=).
      const sellerBSite = expectStatus(await sellerB.session.get("/api/seller/site"), 200, "Seller B GET /api/seller/site");
      assert.notEqual(sellerBSite.site?.id, createdSiteId, "Seller B must NEVER read Seller A's site");
      assert.equal(
        JSON.stringify(sellerBSite).includes(v1Hero),
        false,
        "Seller B /api/seller/site leaked Seller A draft content"
      );
      const sellerBPreview = await sellerB.session.get(
        `/seller/site/xem-truoc?shop_id=${encodeURIComponent(sellerA.shopId)}`,
        { headers: HTML_HEADERS }
      );
      expectStatus(sellerBPreview, 200, "Seller B preview scoped to own shop");
      assert.equal(
        sellerBPreview.text.includes(v1Hero),
        false,
        "Seller B preview with ?shop_id= leaked Seller A's draft!"
      );

      // --- 5b. Publish v1 -> poll gently (negative cache <= 1500ms) -> public 200 with v1 content ---
      const pubV1 = expectStatus(
        await sellerA.session.post("/api/seller/site/publish", { note: "Publish v1" }),
        200,
        "POST /api/seller/site/publish (v1)"
      );
      assert.equal(pubV1.site.status, "PUBLISHED");
      const v1VersionId = pubV1.version.id;
      assert.ok(v1VersionId, "publish v1 must return version.id");

      const liveV1 = await pollUntil(
        `public ${testHost} returns 200 with v1 content`,
        async () => {
          const r = await requestWithHost(testHost, "/");
          return [r.status === 200 && r.text.includes(v1Hero), `status=${r.status}`, r];
        },
        { timeoutMs: 4500, intervalMs: 250 }
      );
      assert.equal(liveV1.status, 200);
      assert.ok(liveV1.text.includes(v1Hero), "Published site must render v1 title");

      // Seller B cannot rollback Seller A's version -> 404/403.
      const hijackRb = await sellerB.session.post("/api/seller/site/rollback", {
        version_id: v1VersionId,
        note: "cross-tenant rollback",
      });
      expectStatus(hijackRb, [403, 404], "Seller B rollback of Seller A version");

      // --- 5c. Save draft v2 (without publish) -> public STILL shows v1 ---
      const draftV2Cfg = makeSiteConfig({
        brandName: `Thu Nghiem ${runTag}`,
        heroTitle: v2Hero,
        accentColor: "#0F766E",
      });
      expectStatus(
        await sellerA.session.put("/api/seller/site/draft", { subdomain: subTest, config: draftV2Cfg }),
        200,
        "PUT /api/seller/site/draft (v2)"
      );
      const stillV1 = await requestWithHost(testHost, "/");
      assert.equal(stillV1.status, 200);
      assert.ok(stillV1.text.includes(v1Hero), "Public site must still render v1 before publishing v2");
      assert.equal(stillV1.text.includes(v2Hero), false, "Unpublished draft v2 must NOT leak to public host");

      // --- 5d. Publish v2 -> public IMMEDIATELY reflects v2 via revalidateTag ---
      const pubV2 = expectStatus(
        await sellerA.session.post("/api/seller/site/publish", { note: "Publish v2" }),
        200,
        "POST /api/seller/site/publish (v2)"
      );
      assert.equal(pubV2.version.version_number, 2);

      const liveV2 = await requestWithHost(testHost, "/");
      assert.equal(liveV2.status, 200);
      assert.ok(liveV2.text.includes(v2Hero), "Publish v2 must reflect immediately via revalidateTag");
      assert.equal(liveV2.text.includes(v1Hero), false, "Public site must no longer render old v1 title after v2 publish");

      // --- 5e. Rollback to v1 -> public IMMEDIATELY returns to v1 via revalidateTag ---
      const rbV1 = expectStatus(
        await sellerA.session.post("/api/seller/site/rollback", {
          version_id: v1VersionId,
          note: "Rollback to v1",
        }),
        200,
        "POST /api/seller/site/rollback (v1)"
      );
      assert.equal(rbV1.site.published_version_id, v1VersionId);

      const liveAfterRb = await requestWithHost(testHost, "/");
      assert.equal(liveAfterRb.status, 200);
      assert.ok(liveAfterRb.text.includes(v1Hero), "Rollback to v1 must reflect immediately via revalidateTag");
      assert.equal(liveAfterRb.text.includes(v2Hero), false, "v2 title must disappear after rollback to v1");

      const versionsList = expectStatus(
        await sellerA.session.get("/api/seller/site/versions"),
        200,
        "GET /api/seller/site/versions"
      );
      assert.ok((versionsList.versions ?? []).length >= 2, "versions history must list >= 2 versions");

      // --- 6. Security payload validation via BFF PUT /api/seller/site/draft ---
      const baseValid = makeSiteConfig({ brandName: "SecCheck", heroTitle: v1Hero });

      // Unknown field in theme & section -> 400.
      const unknownTheme = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: {
          ...baseValid,
          theme: { ...baseValid.theme, custom_css: "body{background:red}" },
        },
      });
      expectStatus(unknownTheme, 400, "PUT /api/seller/site/draft (unknown theme field)");
      assertNoInternalErrorLeak(unknownTheme.text, "unknown theme field");

      const unknownSection = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: {
          ...baseValid,
          sections: [{ ...baseValid.sections[0], raw_html: "<b>inject</b>" }],
        },
      });
      expectStatus(unknownSection, 400, "PUT /api/seller/site/draft (unknown section field)");

      // javascript: & data: URLs -> 400.
      const jsUrlRes = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: {
          ...baseValid,
          theme: { ...baseValid.theme, logo_url: "javascript:alert(1)" },
        },
      });
      expectStatus(jsUrlRes, 400, "PUT /api/seller/site/draft (javascript: URL)");

      const dataUrlRes = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: {
          ...baseValid,
          theme: { ...baseValid.theme, banner_url: "data:text/html,<script>alert(1)</script>" },
        },
      });
      expectStatus(dataUrlRes, 400, "PUT /api/seller/site/draft (data: URL)");

      // Low contrast colors (#CCCCCC on #FFFFFF) -> 400.
      const lowContrastRes = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: {
          ...baseValid,
          theme: { ...baseValid.theme, bg_color: "#FFFFFF", surface_color: "#FFFFFF", text_color: "#CCCCCC" },
        },
      });
      expectStatus(lowContrastRes, 400, "PUT /api/seller/site/draft (low contrast)");

      // Reserved subdomains -> 400; duplicate subdomain -> 409.
      for (const reserved of ["www", "admin", "api", "nexusmart", "vietcombank-uudai"]) {
        const res = await sellerA.session.put("/api/seller/site/draft", {
          subdomain: reserved,
          config: baseValid,
        });
        expectStatus(res, 400, `reserved subdomain ${reserved}`);
      }
      for (const dup of [SITE_A_SUB, SITE_A_SUB.toUpperCase()]) {
        const res = await sellerA.session.put("/api/seller/site/draft", {
          subdomain: dup,
          config: baseValid,
        });
        expectStatus(res, 409, `duplicate subdomain ${dup}`);
      }

      // <script>alert("xss")</script> in text section/tagline -> rejected 400 OR safely escaped in HTML (0 raw executable script!).
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
            props: {
              title: "Kiem tra XSS",
              body: `Van ban thuan ${xssPayload}`,
              align: "left",
            },
          },
        ],
      });
      const xssDraftRes = await sellerA.session.put("/api/seller/site/draft", {
        subdomain: subTest,
        config: xssCfg,
      });
      if (xssDraftRes.status === 200) {
        expectStatus(
          await sellerA.session.post("/api/seller/site/publish", { note: "Publish XSS escape verification" }),
          200,
          "publish XSS text test"
        );
        const xssHtml = await requestWithHost(testHost, "/");
        assert.equal(xssHtml.status, 200);
        assert.equal(
          xssHtml.text.includes('<script>alert("xss")</script>'),
          false,
          "Rendered HTML MUST NOT contain raw unescaped <script>alert(...) tag!"
        );
        assert.ok(
          xssHtml.text.includes("&lt;script&gt;alert(") || xssHtml.text.includes("\\u003cscript"),
          "XSS payload must be HTML-escaped as inert plain text"
        );
      } else {
        expectStatus(xssDraftRes, 400, "XSS payload must either be rejected 400 or escaped in HTML");
      }

      // --- 9b. Empty sections (`sections: []`) published on test shop renders `noindex` meta tag ---
      const emptyCfg = makeSiteConfig({
        brandName: `Empty Sections ${runTag}`,
        heroTitle: "",
        sections: [],
      });
      expectStatus(
        await sellerA.session.put("/api/seller/site/draft", { subdomain: subTest, config: emptyCfg }),
        200,
        "PUT /api/seller/site/draft (empty sections)"
      );
      expectStatus(
        await sellerA.session.post("/api/seller/site/publish", { note: "Publish empty sections for noindex check" }),
        200,
        "POST /api/seller/site/publish (empty sections)"
      );
      const emptySiteHtml = await requestWithHost(testHost, "/");
      assert.equal(emptySiteHtml.status, 200);
      const emptyRobots = extractMetaContent(emptySiteHtml.text, "robots") ?? "";
      assert.match(emptyRobots, /noindex/i, `Site with empty sections must emit noindex meta, got "${emptyRobots}"`);

      // --- 7. Admin suspend -> public host returns 404; unsuspend -> restores 200 ---
      const adminList = expectStatus(
        await admin.session.get(`/api/admin/sites?q=${encodeURIComponent(subTest)}`),
        200,
        "GET /api/admin/sites"
      );
      assert.ok(
        (adminList.items ?? []).some((s) => s.id === createdSiteId),
        "Admin /api/admin/sites must list the test shop site"
      );
      const adminPage = await admin.session.get("/admin/sites", { headers: HTML_HEADERS });
      expectStatus(adminPage, 200, "GET /admin/sites");

      const suspRes = expectStatus(
        await admin.session.post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
          suspend: true,
          reason: "E2E web suspend check",
        }),
        200,
        "POST /api/admin/sites/[id]/suspend (true)"
      );
      assert.equal(suspRes.site.status, "SUSPENDED");

      await pollUntil(
        `suspended ${testHost} returns 404`,
        async () => {
          const r = await requestWithHost(testHost, "/");
          return [r.status === 404, `status=${r.status}`, r];
        },
        { timeoutMs: 4500, intervalMs: 250 }
      );

      const unsuspRes = expectStatus(
        await admin.session.post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
          suspend: false,
          reason: "E2E web unsuspend check",
        }),
        200,
        "POST /api/admin/sites/[id]/suspend (false)"
      );
      assert.equal(unsuspRes.site.status, "PUBLISHED");

      await pollUntil(
        `unsuspended ${testHost} returns 200`,
        async () => {
          const r = await requestWithHost(testHost, "/");
          return [r.status === 200, `status=${r.status}`, r];
        },
        { timeoutMs: 4500, intervalMs: 250 }
      );
    } finally {
      // Cleanly suspend the ephemeral test shop's site so it never stays live after the test.
      if (createdSiteId) {
        await admin.session
          .post(`/api/admin/sites/${encodeURIComponent(createdSiteId)}/suspend`, {
            suspend: true,
            reason: "E2E web cleanup",
          })
          .catch(() => {});
      }
      await Promise.allSettled([
        sellerA.session.post("/api/auth/logout", {}),
        sellerB.session.post("/api/auth/logout", {}),
        admin.session.post("/api/auth/logout", {}),
      ]);
    }
  });

  it("8. Buy from site -> storefront cart/checkout handoff creates STOREFRONT order; unpublished shop hint rejected 400", async () => {
    await resetRateLimitKeys();

    const shopAId = siteABackend.shop.id;
    const buyer = await registerVerifyLoginBuyer("site-checkout");
    const addr = await createShippingAddress(buyer.session, "site-checkout");

    try {
      // Storefront cart & checkout handoff pages return 200 with Shop A branding.
      const cartPage = await buyer.session.get(
        `/gio-hang?shop=${encodeURIComponent(shopAId)}&channel=storefront&site=${encodeURIComponent(SITE_A_HOST)}`,
        { headers: HTML_HEADERS }
      );
      expectStatus(cartPage, 200, "GET /gio-hang storefront handoff");
      assert.ok(cartPage.text.includes("Xưởng Dệt Bạch Đằng"), "Storefront cart handoff must render Shop A brand");

      const checkoutPage = await buyer.session.get(
        `/thanh-toan?shop=${encodeURIComponent(shopAId)}&channel=storefront&site=${encodeURIComponent(SITE_A_HOST)}`,
        { headers: HTML_HEADERS }
      );
      expectStatus(checkoutPage, 200, "GET /thanh-toan storefront handoff");
      assert.ok(checkoutPage.text.includes("Xưởng Dệt Bạch Đằng"), "Storefront checkout handoff must render Shop A brand");

      // Add Shop A SKU to cart and complete server-verified STOREFRONT checkout via BFF POST /api/checkout.
      const addCart = expectStatus(
        await buyer.session.post("/api/cart/items", { sku_id: shopASkuId, quantity: 1 }),
        200,
        "POST /api/cart/items (Shop A)"
      );
      const shopAGroup = addCart.cart.shop_groups.find((g) => g.shop_id === shopAId);
      assert.ok(shopAGroup, "Shop A group must be in buyer cart");

      const { shippingChoices } = await getShippingChoicesForCart(
        buyer.session,
        addr.address,
        [shopAGroup],
        true
      );

      // Passing a shop WITHOUT a published site (Đà Nẵng) as storefront checkout hint -> rejected 400 SITE_NOT_PUBLISHED!
      const unpubCheckout = await buyer.session.post(
        "/api/checkout",
        {
          address_id: addr.address.id,
          from_cart: true,
          payment_method: "COD",
          shipping_choices: shippingChoices,
          checkout_channel_hint: "storefront",
          storefront_shop_id: daNangShopId,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("sf-unpub-rej") } }
      );
      expectStatus(unpubCheckout, 400, "POST /api/checkout with unpublished shop storefront hint");
      assert.equal(unpubCheckout.data?.error?.code, "SITE_NOT_PUBLISHED");

      // Passing mismatched storefront_host (sai-gon for bach-dang shop) -> rejected 400 SITE_NOT_PUBLISHED!
      const wrongHostCheckout = await buyer.session.post(
        "/api/checkout",
        {
          address_id: addr.address.id,
          from_cart: true,
          payment_method: "COD",
          shipping_choices: shippingChoices,
          checkout_channel_hint: "storefront",
          storefront_shop_id: shopAId,
          storefront_host: SITE_B_HOST,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("sf-wronghost-rej") } }
      );
      expectStatus(wrongHostCheckout, 400, "POST /api/checkout with mismatched storefront_host");
      assert.equal(wrongHostCheckout.data?.error?.code, "SITE_NOT_PUBLISHED");

      // Valid verified storefront checkout for published Shop A -> creates real STOREFRONT order!
      const sfCheckout = expectStatus(
        await buyer.session.post(
          "/api/checkout",
          {
            address_id: addr.address.id,
            from_cart: true,
            payment_method: "COD",
            shipping_choices: shippingChoices,
            checkout_channel_hint: "storefront",
            storefront_shop_id: shopAId,
            storefront_host: SITE_A_HOST,
          },
          { headers: { "Idempotency-Key": newIdempotencyKey("sf-verified-order") } }
        ),
        201,
        "POST /api/checkout (verified STOREFRONT)"
      );
      assert.equal(sfCheckout.sales_channel, "STOREFRONT", "BFF verified storefront checkout must set sales_channel=STOREFRONT");
      assert.equal(sfCheckout.storefront_shop_id, shopAId, "BFF verified storefront checkout must set storefront_shop_id=shopA.id");
      assert.equal(sfCheckout.orders.length, 1);
      assert.equal(sfCheckout.orders[0].sales_channel, "STOREFRONT");
      assert.equal(sfCheckout.orders[0].storefront_shop_id, shopAId);

      // Confirm persisted order detail via GET /api/orders/[id].
      const orderDetail = expectStatus(
        await buyer.session.get(`/api/orders/${encodeURIComponent(sfCheckout.orders[0].id)}`),
        200,
        "GET /api/orders/[id] (STOREFRONT)"
      );
      assert.equal(orderDetail.order.sales_channel, "STOREFRONT");
      assert.equal(orderDetail.order.storefront_shop_id, shopAId);
    } finally {
      await buyer.session.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("9a & 9c. SEO rules: marketplace canonical on site PDP, indexable seeded site (no noindex), and host-specific robots.txt & sitemap.xml", async () => {
    // Seeded Site A homepage has substantive sections -> MUST NOT have noindex!
    const homeA = await requestWithHost(SITE_A_HOST, "/");
    assert.equal(homeA.status, 200);
    const homeARobots = extractMetaContent(homeA.text, "robots") ?? "";
    assert.equal(
      /noindex/i.test(homeARobots),
      false,
      `Seeded Site A with content sections must NOT set noindex, got "${homeARobots}"`
    );

    // Site A PDP canonical points to marketplace host `/san-pham/<shopA-slug>`, NOT `bach-dang.<rootDomain>`.
    const pdpA = await requestWithHost(SITE_A_HOST, `/san-pham/${SHOP_A_SLUG}`);
    assert.equal(pdpA.status, 200);
    const canonical = extractCanonicalHref(pdpA.text);
    assert.ok(canonical, "Site A PDP must emit <link rel='canonical' href='...'>");
    const canonicalUrl = new URL(canonical);
    assert.equal(
      canonicalUrl.pathname,
      `/san-pham/${SHOP_A_SLUG}`,
      `Canonical pathname must be /san-pham/${SHOP_A_SLUG}, got ${canonical}`
    );
    assert.notEqual(
      canonicalUrl.hostname.toLowerCase(),
      SITE_A_HOST.toLowerCase(),
      `Site PDP canonical MUST point to marketplace host, NOT subdomain ${SITE_A_HOST}: got ${canonical}`
    );

    // Host-specific /robots.txt for Site A.
    const robotsA = await requestWithHost(SITE_A_HOST, "/robots.txt", { headers: { accept: "text/plain" } });
    assert.equal(robotsA.status, 200, `Site A /robots.txt expected 200, got ${robotsA.status}`);
    assert.ok(
      robotsA.text.includes(SITE_A_HOST) && robotsA.text.includes("/sitemap.xml"),
      `Site A /robots.txt must reference ${SITE_A_HOST}/sitemap.xml, got:\n${robotsA.text}`
    );

    // Host-specific /sitemap.xml for Site A: includes ONLY Shop A URLs (zero Shop B slugs).
    const sitemapA = await requestWithHost(SITE_A_HOST, "/sitemap.xml", { headers: { accept: "application/xml" } });
    assert.equal(sitemapA.status, 200, `Site A /sitemap.xml expected 200, got ${sitemapA.status}`);
    assert.ok(
      sitemapA.text.includes(`${SITE_A_HOST}`) && sitemapA.text.includes(`/san-pham/${SHOP_A_SLUG}`),
      `Site A /sitemap.xml must include ${SITE_A_HOST} and Shop A slug ${SHOP_A_SLUG}`
    );
    assert.equal(
      sitemapA.text.includes(SHOP_B_SLUG) || sitemapA.text.includes(SHOP_B_SLUG_2),
      false,
      "Site A /sitemap.xml leaked Shop B product slugs!"
    );

    // Host-specific /sitemap.xml for Site B: includes ONLY Shop B URLs (zero Shop A slugs).
    const sitemapB = await requestWithHost(SITE_B_HOST, "/sitemap.xml", { headers: { accept: "application/xml" } });
    assert.equal(sitemapB.status, 200, `Site B /sitemap.xml expected 200, got ${sitemapB.status}`);
    assert.ok(
      sitemapB.text.includes(`${SITE_B_HOST}`) && sitemapB.text.includes(`/san-pham/${SHOP_B_SLUG}`),
      `Site B /sitemap.xml must include ${SITE_B_HOST} and Shop B slug ${SHOP_B_SLUG}`
    );
    assert.equal(
      sitemapB.text.includes(SHOP_A_SLUG),
      false,
      "Site B /sitemap.xml leaked Shop A product slug!"
    );
  });
});
