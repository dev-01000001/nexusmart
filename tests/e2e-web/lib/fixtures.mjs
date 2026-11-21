import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { ROOT_HOST, siteHost } from "./config.mjs";
import { Browser, expectStatus, gw, uid } from "./http.mjs";
import { waitForOTP } from "./mailpit.mjs";

/** The four seeded shops; `make seed` publishes a site for each (contract #4). */
export const SEEDED = {
  bachDang: {
    sub: "bach-dang",
    shopName: "Xưởng Dệt Bạch Đằng",
    shopSlug: "xuong-det-bach-dang",
    productSlug: "ao-thun-cotton-compact-trang",
    voucherCode: "BACHDANG20",
    theme: { mode: "light", bg: "#F8F6F1", accent: "#1D4ED8" },
  },
  saiGon: {
    sub: "sai-gon",
    shopName: "Kho Basic Sài Gòn",
    shopSlug: "kho-basic-sai-gon",
    productSlug: "ao-thun-the-thao-dryfit-xanh-co-vit",
    voucherCode: "SAIGON25",
    theme: { mode: "dark", bg: "#14181C", accent: "#D97706" },
  },
  daNang: {
    sub: "da-nang",
    shopName: "Sợi Tự Nhiên Đà Nẵng",
    shopSlug: "soi-tu-nhien-da-nang",
    productSlug: "combo-3-quan-lot-brief-soi-tre",
    voucherCode: "DANANG30",
    theme: { mode: "light", bg: "#EEF2EA", accent: "#3F6B4F" },
  },
  haNoi: {
    sub: "ha-noi",
    shopName: "Dệt Kim Hà Nội",
    shopSlug: "det-kim-ha-noi",
    productSlug: "quan-jogger-ni-chan-cua-xam-muoi-tieu",
    voucherCode: "HANOI15",
    theme: { mode: "dark", bg: "#2B1A1E", accent: "#E9B949" },
  },
};

export const demoCredentials = {
  seller: {
    email: process.env.SEED_SELLER_EMAIL || "seller@nexusmart.local",
    password: process.env.SEED_SELLER_PASSWORD || "SellerDemo123!",
  },
  sellerHanoi: {
    email: process.env.SEED_SELLER_HANOI_EMAIL || "seller-hanoi@nexusmart.local",
    password: process.env.SEED_SELLER_PASSWORD || "SellerDemo123!",
  },
  admin: {
    email: process.env.ADMIN_BOOTSTRAP_EMAIL || "admin@nexusmart.local",
    password: process.env.ADMIN_BOOTSTRAP_PASSWORD || "AdminDevPassword123!",
  },
};

let seq = 0;

export function uniquePhone() {
  seq += 1;
  const raw = (Date.now() * 17 + Math.floor(Math.random() * 1_000_000) + seq * 9973) % 80_000_000;
  return `09${String(raw + 10_000_000).padStart(8, "0")}`;
}

export function uniqueBuyerInput(prefix = "buyer") {
  const id = uid();
  return {
    fullName: `E2E Buyer ${prefix} ${id}`,
    email: `e2e-web-${prefix}-${id}@nexusmart.local`,
    phone: uniquePhone(),
    password: `WebE2E!${id.slice(-6)}Aa1`,
  };
}

export function newIdempotencyKey(prefix = "e2e-web") {
  return `${prefix}-${randomUUID()}`;
}

/** Neither token field names nor raw JWT/cookie secrets may appear in a BFF response body. */
export function assertNoTokenLeak(text, label, extraSecrets = []) {
  assert.doesNotMatch(
    text,
    /"(?:access_token|accessToken|refresh_token|refreshToken)"/i,
    `${label}: response JSON leaked token field name`
  );
  assert.doesNotMatch(
    text,
    /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/,
    `${label}: response body leaked a raw JWT`
  );
  for (const secret of extraSecrets) {
    if (secret && secret.length >= 12) {
      assert.equal(text.includes(secret), false, `${label}: response body leaked raw auth cookie value`);
    }
  }
}

/** Gateway internals (trace_id, DB/gRPC errors, stack paths) never reach the browser. */
export function assertNoInternalErrorLeak(text, label) {
  assert.doesNotMatch(
    text,
    /trace_id|pq:|ECONNREFUSED|rpc error:|panic:|at\s+\/app\//i,
    `${label}: response leaked internal error details: ${text.slice(0, 300)}`
  );
}

/**
 * Access + refresh cookies are HttpOnly, SameSite=Lax, Path=/ and host-only (no Domain=).
 */
export function assertAuthCookieHeaders(res, label) {
  const parsed = res.setCookie.map((raw) => {
    const [pair, ...attrs] = raw.split(";").map((p) => p.trim());
    const eq = pair.indexOf("=");
    return {
      raw,
      name: eq >= 0 ? pair.slice(0, eq).trim() : pair,
      value: eq >= 0 ? pair.slice(eq + 1).trim() : "",
      attrs: attrs.map((a) => a.toLowerCase()),
    };
  });
  const access = parsed.find((c) => c.name === "nm_at" || c.name === "__Host-nm_at");
  const refresh = parsed.find((c) => c.name === "nm_rt" || c.name === "__Host-nm_rt");
  assert.ok(access && access.value.length > 10, `${label}: missing access cookie (nm_at)`);
  assert.ok(refresh && refresh.value.length > 10, `${label}: missing refresh cookie (nm_rt)`);
  for (const c of parsed) {
    assert.equal(
      c.attrs.some((a) => a.startsWith("domain=")),
      false,
      `${label}: cookie ${c.name} must be host-only without Domain= (${c.raw})`
    );
  }
  for (const c of [access, refresh]) {
    assert.ok(c.attrs.includes("httponly"), `${label}: cookie ${c.name} must be HttpOnly (${c.raw})`);
    assert.ok(c.attrs.includes("samesite=lax"), `${label}: cookie ${c.name} must be SameSite=Lax (${c.raw})`);
    assert.ok(c.attrs.includes("path=/"), `${label}: cookie ${c.name} must have Path=/ (${c.raw})`);
  }
  return { access, refresh };
}

/**
 * Register -> Mailpit OTP -> verify -> login, all through the BFF on `client.host`.
 * Returns the logged-in user; the session cookies land in the client's browser jar.
 */
export async function registerBuyer(client, prefix = "buyer") {
  const creds = uniqueBuyerInput(prefix);
  const regRes = await client.post("/api/auth/register", creds);
  const reg = expectStatus(regRes, 201, `POST ${client.host}/api/auth/register (${prefix})`);
  assert.ok(reg.userId, "register must return userId");
  assertNoTokenLeak(regRes.text, `register (${prefix})`);

  const otp = await waitForOTP(creds.email);
  assert.match(otp, /^\d{6}$/, `Mailpit OTP for ${creds.email} must be 6 digits`);
  const verify = expectStatus(
    await client.post("/api/auth/verify-otp", { email: creds.email, otp }),
    200,
    `POST ${client.host}/api/auth/verify-otp (${prefix})`
  );
  assert.equal(verify.verified, true);

  const login = await loginOn(client, creds.email, creds.password, prefix);
  assert.equal(login.user.role, "buyer");
  return { ...login, creds };
}

/** Log in through the BFF on `client.host` and assert the cookie/token rules. */
export async function loginOn(client, email, password, label = "login") {
  const res = await client.post("/api/auth/login", { email, password });
  const data = expectStatus(res, 200, `POST ${client.host}/api/auth/login (${label})`);
  const authCookies = assertAuthCookieHeaders(res, `login ${client.host} (${label})`);
  assertNoTokenLeak(res.text, `login (${label})`, [authCookies.access.value, authCookies.refresh.value]);
  assert.ok(data.user?.id, "login must return user.id");
  assert.equal(data.user.email, email);
  return { user: data.user, loginRes: res, authCookies };
}

/** Resolve a seeded site through the public gateway lookup (test discovery). */
export async function resolveSite(sub) {
  const host = siteHost(sub);
  const bareHost = host.replace(/:\d+$/, "");
  const res = await gw("GET", `/api/v1/sites/by-host?host=${encodeURIComponent(bareHost)}`);
  assert.equal(res.status, 200, `site ${bareHost} must be PUBLISHED (run make seed): ${res.status} ${res.text}`);
  const shopId = res.data?.shop?.id ?? res.data?.site?.shop_id;
  assert.ok(shopId, `site ${bareHost}: missing shop id`);
  return { sub, host, shopId, siteId: res.data?.site?.id, shopName: res.data?.shop?.name };
}

/** Every PUBLISHED site from the public directory REST (contract #1), all pages. */
export async function listPublishedSites() {
  const sites = [];
  let total = Infinity;
  for (let page = 1; sites.length < total && page <= 50; page += 1) {
    const res = await gw("GET", `/api/v1/sites?page=${page}&page_size=48`);
    assert.equal(res.status, 200, `GET /api/v1/sites page ${page}: ${res.status} ${res.text}`);
    total = Number(res.data.total ?? 0);
    const items = res.data.sites ?? [];
    sites.push(...items);
    if (items.length === 0) break;
  }
  return { sites, total };
}

/**
 * An active in-stock SKU of `shopId` (most stock first) via the public catalog, so repeated
 * runs never exhaust one SKU and nothing depends on seed ordering.
 */
export async function pickSku(shopId, { minStock = 5, exclude = [] } = {}) {
  const res = await gw("GET", `/api/v1/products?shop_id=${encodeURIComponent(shopId)}&page=1&page_size=50`);
  assert.equal(res.status, 200, `GET /api/v1/products?shop_id=${shopId}: ${res.text}`);
  const skus = [];
  for (const p of res.data?.items ?? []) {
    if (p.shop_id && p.shop_id !== shopId) continue;
    for (const k of p.skus ?? []) {
      if (k.is_active === false || exclude.includes(k.id)) continue;
      if (k.available_stock >= minStock && k.price_vnd >= 50_000 && k.price_vnd <= 1_500_000) {
        skus.push({ skuId: k.id, skuCode: k.sku_code, price: k.price_vnd, stock: k.available_stock, productId: p.id });
      }
    }
  }
  skus.sort((a, b) => b.stock - a.stock);
  assert.ok(skus.length > 0, `shop ${shopId} must have an in-stock SKU (stock >= ${minStock})`);
  return skus[0];
}

/** An active SHOP voucher of `shopId` with quota left; prefers `preferredCode`. */
export async function pickShopVoucher(shopId, preferredCode) {
  const res = await gw("GET", `/api/v1/vouchers?shop_id=${encodeURIComponent(shopId)}`);
  assert.equal(res.status, 200, `GET /api/v1/vouchers?shop_id=${shopId}: ${res.text}`);
  const usable = (res.data?.vouchers ?? []).filter(
    (v) =>
      String(v.scope).toUpperCase() === "SHOP" &&
      v.shop_id === shopId &&
      v.is_active &&
      v.remaining_quota > 0 &&
      (!v.ends_at || Date.parse(v.ends_at) > Date.now() + 60_000) &&
      v.min_order_vnd <= 2_000_000
  );
  usable.sort(
    (a, b) =>
      Number(b.code.toUpperCase() === preferredCode) - Number(a.code.toUpperCase() === preferredCode) ||
      b.remaining_quota - a.remaining_quota
  );
  assert.ok(usable.length > 0, `shop ${shopId} must have an active SHOP voucher with quota (seed ${preferredCode})`);
  return usable[0];
}

let cachedLocation = null;

/** Create a default shipping address for the logged-in buyer through the BFF. */
export async function createShippingAddress(client, label = "buyer") {
  if (!cachedLocation) {
    const prov = expectStatus(await client.get("/api/checkout/locations"), 200, "GET /api/checkout/locations");
    assert.ok(prov.provinces?.length > 0, "provinces list must be non-empty");
    const province = prov.provinces.find((p) => p.code === "01") ?? prov.provinces[0];
    const wards = expectStatus(
      await client.get(`/api/checkout/locations?province_code=${encodeURIComponent(province.code)}`),
      200,
      "GET /api/checkout/locations?province_code"
    );
    assert.ok(wards.wards?.length > 0, "wards list must be non-empty");
    cachedLocation = { province, ward: wards.wards[0] };
  }
  const { province, ward } = cachedLocation;
  const res = await client.post("/api/checkout/addresses", {
    recipient_name: `Người nhận ${label}`,
    recipient_phone: uniquePhone(),
    province_code: province.code,
    province_name: province.name,
    ward_code: ward.code,
    ward_name: ward.name,
    street_address: `Số 18 Đường E2E ${uid().slice(-4)}`,
    is_default: true,
  });
  const data = expectStatus(res, 201, `POST ${client.host}/api/checkout/addresses (${label})`);
  assert.ok(data.address?.id, "created address must have id");
  return data.address;
}

/** Quote shipping for the cart's shop groups through the BFF; picks the first service. */
export async function quoteShipping(client, address, shopGroups, cod) {
  const res = await client.post("/api/checkout/shipping-quote", {
    to_province_code: address.province_code,
    to_ward_code: address.ward_code,
    shops: shopGroups.map((g) => ({
      shop_id: g.shop_id,
      weight_grams: Math.max(200, g.total_weight_grams || 400),
      cod_amount_vnd: cod ? g.subtotal_vnd : 0,
    })),
  });
  const data = expectStatus(res, 200, `POST ${client.host}/api/checkout/shipping-quote`);
  const quotesByShop = data.quotes_by_shop ?? {};
  const shippingChoices = [];
  const shippingFees = {};
  for (const g of shopGroups) {
    const quotes = quotesByShop[g.shop_id] ?? [];
    assert.ok(quotes.length > 0, `expected shipping quotes for shop ${g.shop_id}`);
    shippingChoices.push({ shop_id: g.shop_id, carrier_code: quotes[0].carrier_code, service_code: quotes[0].service_code });
    shippingFees[g.shop_id] = quotes[0].fee_vnd;
  }
  return { shippingChoices, shippingFees };
}

/** Empty the cart on this host, then add `quantity` of `skuId`; returns the cart. */
export async function fillCart(client, skuId, quantity = 1) {
  expectStatus(await client.del("/api/cart"), [200, 204], `DELETE ${client.host}/api/cart`);
  const data = expectStatus(
    await client.post("/api/cart/items", { sku_id: skuId, quantity }),
    200,
    `POST ${client.host}/api/cart/items`
  );
  return data.cart;
}

/** COD/VietQR checkout of the whole cart on this host. */
export function checkout(client, { addressId, shippingChoices, paymentMethod = "COD", extra = {}, key }) {
  return client.post(
    "/api/checkout",
    { address_id: addressId, from_cart: true, payment_method: paymentMethod, shipping_choices: shippingChoices, ...extra },
    { headers: { "Idempotency-Key": key ?? newIdempotencyKey(paymentMethod.toLowerCase()) } }
  );
}

/** All orders visible to the buyer on this host (walks pagination). */
export async function listOrderIds(client) {
  const ids = [];
  const items = [];
  let total = Infinity;
  for (let page = 1; ids.length < total && page <= 20; page += 1) {
    const data = expectStatus(
      await client.get(`/api/orders?page=${page}&page_size=50`),
      200,
      `GET ${client.host}/api/orders`
    );
    total = Number(data.total ?? 0);
    const pageItems = data.items ?? [];
    items.push(...pageItems);
    ids.push(...pageItems.map((o) => o.id));
    if (pageItems.length === 0) break;
  }
  return { ids, items, total };
}

/** Cart line SKU ids. */
export function cartSkuIds(cart) {
  return (cart?.shop_groups ?? []).flatMap((g) => (g.items ?? []).map((i) => i.sku_id));
}

/** An active PLATFORM voucher from the backend (for verifying sites reject platform vouchers). */
export async function pickPlatformVoucher(preferredCode = "NEXUS30") {
  const res = await gw("GET", "/api/v1/vouchers?scope=PLATFORM");
  assert.equal(res.status, 200, `GET /api/v1/vouchers?scope=PLATFORM: ${res.text}`);
  const usable = (res.data?.vouchers ?? []).filter(
    (v) =>
      String(v.scope).toUpperCase() === "PLATFORM" &&
      v.is_active &&
      v.remaining_quota > 0 &&
      (!v.ends_at || Date.parse(v.ends_at) > Date.now() + 60_000)
  );
  usable.sort(
    (a, b) =>
      Number(b.code.toUpperCase() === preferredCode) - Number(a.code.toUpperCase() === preferredCode) ||
      b.remaining_quota - a.remaining_quota
  );
  return usable[0] ?? null;
}

/** Extract `content` from `<meta name="..." content="...">` in HTML. */
export function extractMetaContent(html, name) {
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

/** Extract `href` from `<link rel="canonical" href="...">` in HTML. */
export function extractCanonicalHref(html) {
  const links = html.match(/<link\b[^>]*>/gi) ?? [];
  for (const tag of links) {
    if (/\brel=["']canonical["']/i.test(tag)) {
      const hrefMatch = tag.match(/\bhref=["']([^"']+)["']/i);
      if (hrefMatch) return hrefMatch[1];
    }
  }
  return null;
}

/** Build a WCAG-compliant site draft config for seller lifecycle & SEO tests. */
export function makeSiteConfig({ brandName, tagline, heroTitle, accentColor = "#1D4ED8", sections }) {
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
            body: "Sản phẩm minh bạch tồn kho và thanh toán trực tiếp trên site gian hàng.",
            align: "left",
          },
        },
      ],
  };
}

/**
 * Registers & approves an ephemeral seller + ACTIVE shop via Gateway + Mailpit,
 * then signs into the root host BFF so lifecycle tests start with a brand-new shop
 * that has ZERO existing site rows (100% idempotent across repeated runs).
 */
export async function createEphemeralSeller(label = "site-owner", rootHost = ROOT_HOST) {
  const creds = uniqueBuyerInput(label);
  const regRes = await gw("POST", "/api/v1/auth/register", {
    body: {
      full_name: creds.fullName,
      email: creds.email,
      phone: creds.phone,
      password: creds.password,
    },
  });
  assert.ok([200, 201].includes(regRes.status), `register ephemeral seller: ${regRes.status} ${regRes.text}`);

  const otp = await waitForOTP(creds.email);
  const verifyRes = await gw("POST", "/api/v1/auth/verify-otp", {
    body: { email: creds.email, otp },
  });
  assert.equal(verifyRes.status, 200, `verify ephemeral seller OTP: ${verifyRes.text}`);

  const login1 = await gw("POST", "/api/v1/auth/login", {
    body: { email: creds.email, password: creds.password },
  });
  assert.equal(login1.status, 200, `gateway login ephemeral seller: ${login1.text}`);
  const userToken = login1.data.access_token;

  const provRes = await gw("GET", "/api/v1/shipping/provinces");
  assert.equal(provRes.status, 200);
  const prov = provRes.data.provinces[0];
  const wardRes = await gw("GET", `/api/v1/shipping/provinces/${encodeURIComponent(prov.code)}/wards`);
  assert.equal(wardRes.status, 200);
  const ward = wardRes.data.wards[0];

  const addrRes = await gw("POST", "/api/v1/users/me/addresses", {
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
  const shopRes = await gw("POST", "/api/v1/shops/register", {
    token: userToken,
    body: {
      name: shopName,
      description: "Ephemeral shop for Phase 4 Web E2E site lifecycle",
      pickup_address_id: addrRes.data.address.id,
      contact_phone: creds.phone,
      business_license_doc: "https://sandbox.nexusmart.local/docs/e2e-site.pdf",
    },
  });
  assert.ok([200, 201].includes(shopRes.status), `register ephemeral shop: ${shopRes.text}`);
  const shopId = shopRes.data.shop.id;

  const adminGw = await gw("POST", "/api/v1/auth/login", {
    body: { email: demoCredentials.admin.email, password: demoCredentials.admin.password },
  });
  assert.equal(adminGw.status, 200, `admin gateway login: ${adminGw.text}`);

  const approveRes = await gw("POST", `/api/v1/admin/shops/${encodeURIComponent(shopId)}/review`, {
    token: adminGw.data.access_token,
    body: { decision: "APPROVE", reason: "E2E web ephemeral site shop approval" },
  });
  assert.equal(approveRes.status, 200, `approve ephemeral shop: ${approveRes.text}`);

  const browser = new Browser(`ephemeral-${label}`);
  const client = browser.at(rootHost);
  const login = await loginOn(client, creds.email, creds.password, `ephemeral-${label}`);
  assert.equal(login.user.role, "seller", "ephemeral seller must have role=seller");
  assert.equal(login.user.shopId || login.user.shop_id, shopId, "ephemeral seller must have matching shopId");

  return { browser, client, user: login.user, shopId, shopName, creds };
}
