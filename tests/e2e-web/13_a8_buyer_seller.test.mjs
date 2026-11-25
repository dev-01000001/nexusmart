/**
 * Wave 3b-3 Web E2E — Agent `web-a8` (A8 Buyer/Seller Flows, B15 Voucher Cleanup, Responsive 390/768/1440).
 *
 * Covers:
 *   1. B15: EDU15 / SINHNHAT50 / NEXUSVIP12 removed from gateway vouchers & /hoi-vien.
 *   2. UC-B08 (G7): Public POST /api/gw/orders/lookup (masked PII, uniform 404, 429 rate limit, no mock codes).
 *   3. UC-B26 (G1–G3): Buyer PATCH /api/gw/orders/{id}/address (COD fee increase 2-step + stale quote 409,
 *      prepaid increase blocked 409, prepaid same-province keeps total unchanged, non-owner 403).
 *   4. S04 & UC-S15 (G4–G5, G8): /seller/don-hang server pagination + status_counts + filters + picking-list
 *      + batch-ship (QUEUED/SKIPPED) + merged PDF labels (POST /api/gw/shipping/labels/batch -> %PDF-).
 *   5. S03 & UC-S13 (G6, G8): /seller/san-pham server pagination + SKU stock history + 2-step CSV import
 *      (template, upload valid+invalid, READY, errors_only + errors.csv formula guard, idempotent confirm, PENDING).
 *   6. Real Headless Chrome viewport layout checks at 390x844, 768x1024, and 1440x900 across all A8 screens.
 */
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, test } from "node:test";
import {
  ACCESS_COOKIE,
  COD_SAFE_SUBTOTAL_CAP_VND,
  RATE_LIMIT_MAX_WAIT_MS,
  RATE_LIMIT_SSR_DEFAULT_WAIT_MS,
  RATE_LIMIT_SSR_TEXT,
  REFRESH_COOKIE,
  WebClient,
  config,
  createApprovedCodProduct,
  ensureRateBudget,
  normalizeStatus,
  parseRetryAfterMs,
  pollUntil,
  postSignedPaymentWebhook,
  registerAndLoginBuyer,
} from "./helpers.mjs";

const SELLER_EMAIL = process.env.E2E_SELLER_EMAIL || "seller.bachdang@nexusmart.local";
const SELLER_PASSWORD = process.env.E2E_SELLER_PASSWORD || "SellerDevPassword123!";
const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";
const RETIRED_VOUCHERS = ["EDU15", "SINHNHAT50", "NEXUSVIP12"];

// ============================================================================
// Local Helpers (kept strictly inside 13_a8_buyer_seller.test.mjs; helpers.mjs untouched)
// ============================================================================

async function loginSeededAccount(email, password, label) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `${label} login expected 200, got ${res.status}: ${res.text}`);
  return { client, user: res.data?.user };
}

async function cancelOrderBestEffort(client, orderId, reason) {
  if (!orderId) return;
  try {
    await client.json("POST", `/api/gw/orders/${encodeURIComponent(orderId)}/cancel`, {
      body: { reason },
    });
  } catch {
    // Ignore cleanup errors (order already cancelled/shipped/completed).
  }
}

async function deleteProductBestEffort(sellerClient, productId) {
  if (!productId) return;
  try {
    await sellerClient.json("DELETE", `/api/gw/seller/products/${encodeURIComponent(productId)}`);
  } catch {
    // Ignore cleanup errors.
  }
}

async function pickCodWardForProvince(client, provinceCode) {
  const res = await client.json(
    "GET",
    `/api/gw/shipping/provinces/${encodeURIComponent(provinceCode)}/wards`
  );
  assert.equal(
    res.status,
    200,
    `wards for ${provinceCode} expected 200, got ${res.status}: ${res.text}`
  );
  const wards = (res.data?.wards ?? []).filter((w) => w.cod_supported !== false);
  assert.ok(wards.length > 0, `no cod_supported ward found for province ${provinceCode}`);
  return wards[0];
}

async function quoteShippingForLocation(
  client,
  { shopId, toProvinceCode, toWardCode, skuId, paymentMethod }
) {
  const res = await client.json("POST", "/api/gw/shipping/calculate-fee", {
    body: {
      shop_id: shopId,
      to_province_code: toProvinceCode,
      to_ward_code: toWardCode,
      items: [{ sku_id: skuId, quantity: 1 }],
      payment_method: paymentMethod,
    },
  });
  assert.equal(
    res.status,
    200,
    `calculate-fee (${toProvinceCode}) expected 200, got ${res.status}: ${res.text}`
  );
  const quotes = res.data?.quotes ?? [];
  assert.ok(quotes.length > 0, `calculate-fee (${toProvinceCode}) returned 0 quotes: ${res.text}`);
  const picked =
    quotes.find((q) => String(q.carrier_code).toLowerCase() === "mock_express") ??
    quotes.find((q) => String(q.carrier_code).toLowerCase() === "ghn") ??
    quotes[0];
  return { quote: picked, quotes };
}

/**
 * Resolves two buyer addresses (lowFee & highFee) against `shopId` + `skuId` such that
 * changing from `lowFee` to `highFee` produces a strictly positive shipping fee delta.
 */
async function resolveLowAndHighFeeLocations(client, { shopId, skuId }) {
  const provRes = await client.json("GET", "/api/gw/shipping/provinces");
  assert.equal(provRes.status, 200, `provinces expected 200: ${provRes.text}`);
  const provinces = provRes.data?.provinces ?? [];
  assert.ok(provinces.length >= 34, `expected >= 34 provinces, got ${provinces.length}`);

  // Sample geographically diverse provinces (North, Central, South)
  const preferredCodes = ["01", "79", "48", "92", "31"];
  const candidates = [];
  for (const code of preferredCodes) {
    const p = provinces.find((item) => item.code === code);
    if (p) candidates.push(p);
  }
  for (const p of [provinces[0], provinces[Math.floor(provinces.length / 2)], provinces[provinces.length - 1]]) {
    if (p && !candidates.some((c) => c.code === p.code)) {
      candidates.push(p);
    }
  }

  const evaluated = [];
  for (const province of candidates) {
    const ward = await pickCodWardForProvince(client, province.code);
    const { quote } = await quoteShippingForLocation(client, {
      shopId,
      toProvinceCode: province.code,
      toWardCode: ward.code,
      skuId,
      paymentMethod: "COD",
    });
    evaluated.push({ province, ward, quote });
  }

  evaluated.sort((a, b) => a.quote.fee_vnd - b.quote.fee_vnd);
  const low = evaluated[0];
  const high = evaluated[evaluated.length - 1];
  assert.ok(
    high.quote.fee_vnd > low.quote.fee_vnd,
    `expected distinct shipping fee zones, got low=${low.province.code}(${low.quote.fee_vnd}) high=${high.province.code}(${high.quote.fee_vnd})`
  );
  return { low, high };
}

async function createAddressAtLocation(client, buyer, location, labelSuffix, isDefault = false) {
  const res = await client.json("POST", "/api/gw/users/me/addresses", {
    body: {
      recipient_name: buyer.fullName,
      recipient_phone: buyer.phone,
      province_code: location.province.code,
      province_name: location.province.name,
      ward_code: location.ward.code,
      ward_name: location.ward.name,
      street_address: `42 Đường E2E A8 ${labelSuffix} ${crypto.randomBytes(2).toString("hex")}`,
      is_default: isDefault,
    },
  });
  assert.ok(
    res.status === 201 || res.status === 200,
    `createAddress (${labelSuffix}) expected 201/200, got ${res.status}: ${res.text}`
  );
  assert.ok(res.data?.address?.id, `createAddress missing id: ${res.text}`);
  return res.data.address;
}

async function checkoutSingleShopOrder(
  buyerClient,
  { addressId, shopId, skuId, carrierCode, serviceCode, paymentMethod, quantity = 1 }
) {
  const idempotencyKey = `e2e-a8-chk-${crypto.randomUUID()}`;
  const res = await buyerClient.json("POST", "/api/gw/checkout", {
    headers: { "Idempotency-Key": idempotencyKey },
    body: {
      address_id: addressId,
      from_cart: false,
      items: [{ sku_id: skuId, quantity }],
      payment_method: paymentMethod,
      shipping_choices: [
        {
          shop_id: shopId,
          carrier_code: carrierCode,
          service_code: serviceCode || "standard",
        },
      ],
    },
  });
  assert.ok(
    res.status === 201 || res.status === 200,
    `checkout (${paymentMethod}) expected 201/200, got ${res.status}: ${res.text}`
  );
  const order = res.data?.orders?.[0];
  assert.ok(order?.id && order?.order_code, `checkout missing order: ${res.text}`);

  if (paymentMethod === "VIETQR") {
    const paymentId = res.data?.payment_id;
    const groupId = res.data?.checkout_group_id;
    const totalVnd = res.data?.total_payable_vnd ?? order.total_payable_vnd;
    assert.ok(paymentId && groupId, `VIETQR checkout missing payment_id/group_id: ${res.text}`);
    const whRes = await postSignedPaymentWebhook({
      provider: "vietqr_mock",
      provider_tx_id: `E2E-A8-VQR-${crypto.randomBytes(5).toString("hex")}`,
      payment_id: paymentId,
      order_group_id: groupId,
      amount_vnd: totalVnd,
      status: "SUCCEEDED",
      reference: `NEXUSMART ${groupId}`,
      occurred_at: new Date().toISOString(),
    });
    assert.equal(whRes.status, 200, `payment webhook expected 200: ${whRes.text}`);

    const confirmed = await pollUntil(
      async () => {
        const g = await buyerClient.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
        const st = normalizeStatus(g.data?.order?.status);
        return {
          done: g.status === 200 && st === "CONFIRMED",
          value: g.data?.order,
          detail: `status=${st}`,
        };
      },
      { timeoutMs: config.pollTimeoutMs, intervalMs: config.pollIntervalMs, label: "VIETQR order CONFIRMED" }
    );
    return { order: confirmed, checkout: res.data };
  }

  return { order, checkout: res.data };
}

function findChromeBinary() {
  const candidates = [
    process.env.CHROME_PATH,
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ].filter(Boolean);
  for (const bin of candidates) {
    if (fs.existsSync(bin)) return bin;
  }
  return null;
}

class HeadlessBrowser {
  constructor(proc, ws, userDataDir) {
    this.proc = proc;
    this.ws = ws;
    this.userDataDir = userDataDir;
    this.nextId = 1;
    this.pending = new Map();
    this.eventListeners = new Map();
    this.lastRateLimit = null;

    this.on("Network.responseReceived", (params) => {
      const res = params?.response;
      if (res?.status !== 429) return;
      const headers = res.headers ?? {};
      let retryRaw = null;
      for (const [k, v] of Object.entries(headers)) {
        if (k.toLowerCase() === "retry-after") {
          retryRaw = v;
          break;
        }
      }
      const retryAfterMs = retryRaw ? parseRetryAfterMs(retryRaw) : null;
      if (!this.lastRateLimit || (retryAfterMs ?? 0) >= (this.lastRateLimit.retryAfterMs ?? 0)) {
        this.lastRateLimit = { retryAfterMs, url: res.url ?? "" };
      }
    });

    this.ws.addEventListener("message", (event) => {
      let msg;
      try {
        msg = JSON.parse(String(event.data));
      } catch {
        return;
      }
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) {
          reject(new Error(`CDP error (${msg.error.code}): ${msg.error.message}`));
        } else {
          resolve(msg.result);
        }
        return;
      }
      if (msg.method && this.eventListeners.has(msg.method)) {
        for (const fn of this.eventListeners.get(msg.method)) {
          fn(msg.params);
        }
      }
    });
  }

  static async launch() {
    const chromeBin = findChromeBinary();
    assert.ok(chromeBin, "Headless Chrome binary not found for 390/768/1440 viewport E2E checks");

    const userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), "nm-e2e-w3b3-a8-"));
    const proc = spawn(
      chromeBin,
      [
        "--headless=new",
        "--no-sandbox",
        "--disable-gpu",
        "--disable-dev-shm-usage",
        "--disable-extensions",
        "--remote-debugging-port=0",
        `--user-data-dir=${userDataDir}`,
        "about:blank",
      ],
      { stdio: ["ignore", "pipe", "pipe"] }
    );

    const wsEndpoint = await new Promise((resolve, reject) => {
      let stderrBuf = "";
      const timeout = setTimeout(() => {
        reject(new Error(`Timed out waiting for Chrome DevTools endpoint. stderr: ${stderrBuf}`));
      }, 15_000);
      proc.stderr.on("data", (chunk) => {
        stderrBuf += chunk.toString("utf8");
        const m = stderrBuf.match(/DevTools listening on (ws:\/\/[^\s]+)/);
        if (m) {
          clearTimeout(timeout);
          resolve(m[1]);
        }
      });
      proc.on("exit", (code) => {
        clearTimeout(timeout);
        reject(new Error(`Chrome exited early (${code}). stderr: ${stderrBuf}`));
      });
    });

    const parsedUrl = new URL(wsEndpoint);
    const listRes = await fetch(`http://${parsedUrl.host}/json/list`, {
      signal: AbortSignal.timeout(5_000),
    });
    const targets = await listRes.json();
    const pageTarget = targets.find((t) => t.type === "page" && t.webSocketDebuggerUrl);
    assert.ok(pageTarget?.webSocketDebuggerUrl, "Could not find Chrome page target");

    const ws = new WebSocket(pageTarget.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("CDP WebSocket connect timeout")), 10_000);
      ws.addEventListener("open", () => {
        clearTimeout(timeout);
        resolve();
      });
      ws.addEventListener("error", (err) => {
        clearTimeout(timeout);
        reject(err);
      });
    });

    const browser = new HeadlessBrowser(proc, ws, userDataDir);
    await browser.send("Page.enable");
    await browser.send("Runtime.enable");
    await browser.send("Network.enable");
    return browser;
  }

  on(method, fn) {
    const list = this.eventListeners.get(method) ?? [];
    list.push(fn);
    this.eventListeners.set(method, list);
  }

  send(method, params = {}) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP timeout waiting for ${method}`));
      }, 15_000);
      this.pending.set(id, {
        resolve: (val) => {
          clearTimeout(timeout);
          resolve(val);
        },
        reject: (err) => {
          clearTimeout(timeout);
          reject(err);
        },
      });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  async setViewport(width, height, mobile = width <= 500) {
    await this.send("Emulation.setDeviceMetricsOverride", {
      width,
      height,
      deviceScaleFactor: mobile ? 2 : 1,
      mobile,
    });
  }

  async setCookiesFromClient(webClient) {
    const originUrl = new URL(config.webUrl);
    for (const name of [ACCESS_COOKIE, REFRESH_COOKIE]) {
      const val = webClient.getCookie(name);
      if (!val) continue;
      await this.send("Network.setCookie", {
        name,
        value: val,
        domain: originUrl.hostname,
        path: "/",
        httpOnly: true,
        sameSite: "Lax",
      });
    }
  }

  async clearCookies() {
    await this.send("Page.navigate", { url: "about:blank" });
    await pollUntil(
      async () => {
        const done = await this.eval(
          `window.location.href === "about:blank" && document.readyState === "complete"`
        ).catch(() => false);
        return { done: Boolean(done), detail: "about:blank" };
      },
      { timeoutMs: 10_000, intervalMs: 50, label: "unload page before clearCookies" }
    );
    await this.send("Network.clearBrowserCookies");
  }

  async goto(reqPath) {
    const url = reqPath.startsWith("http")
      ? reqPath
      : `${config.webUrl}${reqPath.startsWith("/") ? reqPath : `/${reqPath}`}`;
    const startMs = Date.now();
    while (true) {
      this.lastRateLimit = null;
      await this.send("Page.navigate", { url });
      await pollUntil(
        async () => {
          const ready = await this.eval(`document.readyState === "complete"`).catch(() => false);
          return { done: Boolean(ready), detail: "readyState" };
        },
        { timeoutMs: 15_000, intervalMs: 120, label: `page load ${reqPath}` }
      );
      await new Promise((r) => setTimeout(r, 900));

      const bodyText = await this.eval(`document.body?.innerText || ""`).catch(() => "");
      const isRateLimited =
        Boolean(this.lastRateLimit) ||
        bodyText.includes(RATE_LIMIT_SSR_TEXT) ||
        bodyText.includes('"RATE_LIMITED"');
      if (!isRateLimited) return bodyText;

      const rawWaitMs = this.lastRateLimit?.retryAfterMs ?? RATE_LIMIT_SSR_DEFAULT_WAIT_MS;
      const waitMs = rawWaitMs + 250;
      const elapsedMs = Date.now() - startMs;
      if (elapsedMs + waitMs > RATE_LIMIT_MAX_WAIT_MS) return bodyText;
      await new Promise((r) => setTimeout(r, waitMs));
    }
  }

  async eval(expression) {
    const res = await this.send("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true,
    });
    if (res.exceptionDetails) {
      throw new Error(
        `Browser JS exception: ${res.exceptionDetails.text || JSON.stringify(res.exceptionDetails)}`
      );
    }
    return res.result?.value;
  }

  async close() {
    try {
      this.ws.close();
    } catch {
      // ignore
    }
    try {
      this.proc.kill("SIGTERM");
    } catch {
      // ignore
    }
    try {
      fs.rmSync(this.userDataDir, { recursive: true, force: true });
    } catch {
      // ignore
    }
  }
}

// ============================================================================
// E2E Test Suites
// ============================================================================

describe("Wave 3b-3 web-a8 — B15, UC-B08, UC-B26, S04/UC-S15, S03/UC-S13 & Viewports", () => {
  test("1. B15 — Retired false-promise vouchers (EDU15, SINHNHAT50, NEXUSVIP12) absent from API and /hoi-vien", async () => {
    await ensureRateBudget(15, { label: "b15-vouchers" });
    const guest = new WebClient();

    const vRes = await guest.json("GET", "/api/gw/vouchers");
    assert.equal(vRes.status, 200, `GET /api/gw/vouchers expected 200: ${vRes.text}`);
    const vouchers = vRes.data?.vouchers ?? [];
    const codes = new Set(vouchers.map((v) => String(v.code ?? "").trim().toUpperCase()));
    for (const retired of RETIRED_VOUCHERS) {
      assert.equal(codes.has(retired), false, `GET /api/gw/vouchers must not contain ${retired}`);
    }

    const pageRes = await guest.page("/hoi-vien");
    assert.equal(pageRes.status, 200, `GET /hoi-vien expected 200, got ${pageRes.status}`);
    for (const retired of RETIRED_VOUCHERS) {
      assert.equal(
        pageRes.text.includes(retired),
        false,
        `/hoi-vien HTML must not mention ${retired}`
      );
    }
  });

  test("2. UC-B08 (G7) — Public guest order lookup returns masked PII, uniform 404, and 429 rate limit", async () => {
    await ensureRateBudget(35, { label: "uc-b08-lookup" });

    const guest = new WebClient();
    const lookupPage = await guest.page("/tra-cuu-don-hang");
    assert.equal(lookupPage.status, 200, `GET /tra-cuu-don-hang expected 200`);
    assert.equal(
      lookupPage.text.includes("NM-261120-"),
      false,
      "/tra-cuu-don-hang must not contain removed mock NM-261120-* codes"
    );
    assert.equal(
      lookupPage.text.includes("guestOrderLookup"),
      false,
      "/tra-cuu-don-hang must not reference guestOrderLookup"
    );

    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");
    const shopRes = await seller.json("GET", "/api/gw/seller/shop");
    assert.equal(shopRes.status, 200);
    const shopId = shopRes.data?.shop?.id;
    assert.ok(shopId);

    const created = await createApprovedCodProduct(seller, {
      priceVnd: 220_000,
      stock: 15,
      tag: "a8-b08",
      adminClient: admin,
    });

    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "a8-b08", { verifyPhone: true });
    let createdOrderId = null;

    try {
      const { low } = await resolveLowAndHighFeeLocations(buyerClient, {
        shopId,
        skuId: created.sku.id,
      });
      const addr = await createAddressAtLocation(buyerClient, buyer, low, "b08", true);
      const { order } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addr.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "COD",
      });
      createdOrderId = order.id;

      // Real anonymous lookup succeeds with masked PII only
      const okRes = await guest.json("POST", "/api/gw/orders/lookup", {
        body: { order_code: order.order_code, phone: buyer.phone },
      });
      assert.equal(okRes.status, 200, `POST /orders/lookup expected 200: ${okRes.text}`);
      const summary = okRes.data?.order;
      assert.ok(summary, `lookup missing order summary: ${okRes.text}`);
      assert.equal(summary.order_code, order.order_code);
      assert.ok(summary.province_name, "lookup must return province_name");
      assert.ok(summary.ward_name, "lookup must return ward_name");
      assert.ok(summary.recipient_phone_masked, "lookup must return masked recipient phone");
      assert.notEqual(
        summary.recipient_phone_masked,
        buyer.phone,
        "recipient_phone_masked must not expose full phone"
      );
      assert.equal(summary.street_address, undefined, "lookup must never expose street_address");
      assert.equal(summary.buyer_id, undefined, "lookup must never expose buyer_id");
      assert.equal(summary.label_url, undefined, "lookup must never expose label_url");
      assert.equal(summary.email, undefined, "lookup must never expose email");
      assert.ok(Array.isArray(summary.items) && summary.items.length > 0, "lookup must include items");
      assert.ok(Array.isArray(summary.timeline) && summary.timeline.length > 0, "lookup must include timeline");

      // Uniform 404 for wrong phone vs unknown order_code
      const wrongPhoneRes = await guest.json("POST", "/api/gw/orders/lookup", {
        body: { order_code: order.order_code, phone: "0900000001" },
        retry429: false,
      });
      const unknownCodeRes = await guest.json("POST", "/api/gw/orders/lookup", {
        body: { order_code: `NM-UNKNOWN-${crypto.randomBytes(3).toString("hex")}`, phone: buyer.phone },
        retry429: false,
      });
      assert.equal(wrongPhoneRes.status, 404, `wrong phone expected 404: ${wrongPhoneRes.text}`);
      assert.equal(unknownCodeRes.status, 404, `unknown code expected 404: ${unknownCodeRes.text}`);
      assert.equal(
        wrongPhoneRes.data?.error?.code,
        unknownCodeRes.data?.error?.code,
        "wrong phone and unknown code must return identical error code"
      );

      // Rate-limit burst on a dedicated code (per-order_code limit = 5 / 10min)
      const burstCode = `NM-RL-${crypto.randomBytes(4).toString("hex").toUpperCase()}`;
      let hit429 = false;
      let retryAfter = null;
      for (let i = 0; i < 7; i += 1) {
        const r = await guest.json("POST", "/api/gw/orders/lookup", {
          body: { order_code: burstCode, phone: "0912345678" },
          retry429: false,
        });
        if (r.status === 429) {
          hit429 = true;
          retryAfter = r.headers.get("retry-after");
          break;
        }
      }
      assert.equal(hit429, true, "expected 429 rate limit after exceeding per-code lookup quota");
      assert.ok(retryAfter, "expected Retry-After header on 429 lookup response");
    } finally {
      await cancelOrderBestEffort(buyerClient, createdOrderId, "E2E A8 B08 cleanup");
      await deleteProductBestEffort(seller, created.product.id);
    }
  });

  test("3. UC-B26 (G1–G3) — Buyer address update: COD increase 2-step + stale quote 409, prepaid increase blocked", async () => {
    await ensureRateBudget(40, { label: "uc-b26-address" });

    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");
    const shopRes = await seller.json("GET", "/api/gw/seller/shop");
    assert.equal(shopRes.status, 200);
    const shopId = shopRes.data?.shop?.id;
    assert.ok(shopId);

    const created = await createApprovedCodProduct(seller, {
      priceVnd: 240_000,
      stock: 20,
      tag: "a8-b26",
      adminClient: admin,
    });

    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "a8-b26", { verifyPhone: true });
    const otherBuyerClient = new WebClient();
    await registerAndLoginBuyer(otherBuyerClient, "a8-b26-other");

    let codOrderId = null;
    let prepaidOrderId = null;

    try {
      const { low, high } = await resolveLowAndHighFeeLocations(buyerClient, {
        shopId,
        skuId: created.sku.id,
      });
      const addrLow1 = await createAddressAtLocation(buyerClient, buyer, low, "low1", true);
      const addrLow2 = await createAddressAtLocation(buyerClient, buyer, low, "low2", false);
      const addrHigh = await createAddressAtLocation(buyerClient, buyer, high, "high", false);

      // --- Case 3A: COD fee increase 2-step ---
      const { order: codOrder } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addrLow1.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "COD",
      });
      codOrderId = codOrder.id;

      // Step 1 preview
      const previewRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(codOrder.id)}/address`,
        { body: { address_id: addrHigh.id, preview_only: true } }
      );
      assert.equal(previewRes.status, 200, `COD preview expected 200: ${previewRes.text}`);
      const preview = previewRes.data;
      assert.equal(preview.preview_only, true);
      assert.ok(preview.fee_delta_vnd > 0, `expected positive fee_delta_vnd: ${previewRes.text}`);
      assert.equal(preview.requires_fee_confirmation, true);

      // Apply without accept_fee_increase -> 409
      const noConfirmRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(codOrder.id)}/address`,
        {
          body: {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: false,
          },
        }
      );
      assert.equal(noConfirmRes.status, 409, `COD increase without confirmation expected 409: ${noConfirmRes.text}`);

      // Apply with stale expected_new_shipping_fee_vnd -> 409 with top-level quote forwarded through BFF (R1)
      const staleRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(codOrder.id)}/address`,
        {
          body: {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: true,
            expected_new_shipping_fee_vnd: Math.max(0, preview.new_shipping_fee_vnd - 1000),
          },
        }
      );
      assert.equal(staleRes.status, 409, `stale expected fee expected 409: ${staleRes.text}`);
      assert.equal(
        staleRes.data?.error?.code,
        "ADDRESS_FEE_CHANGED",
        `stale expected fee expected ADDRESS_FEE_CHANGED: ${staleRes.text}`
      );
      assert.ok(
        staleRes.data?.quote &&
          Number.isSafeInteger(staleRes.data.quote.old_shipping_fee_vnd) &&
          Number.isSafeInteger(staleRes.data.quote.new_shipping_fee_vnd) &&
          Number.isSafeInteger(staleRes.data.quote.fee_delta_vnd) &&
          Number.isSafeInteger(staleRes.data.quote.new_total_payable_vnd),
        `R1: BFF must forward top-level 4-field integer quote on 409 ADDRESS_FEE_CHANGED: ${staleRes.text}`
      );
      assert.equal(staleRes.data.quote.new_shipping_fee_vnd, preview.new_shipping_fee_vnd);

      // Non-owner buyer -> 403
      const foreignRes = await otherBuyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(codOrder.id)}/address`,
        { body: { address_id: addrHigh.id, preview_only: true } }
      );
      assert.equal(foreignRes.status, 403, `non-owner address update expected 403: ${foreignRes.text}`);

      // Step 2 confirm with exact expected_new_shipping_fee_vnd + idempotency_key
      const applyKey = `e2e-a8-addr-${crypto.randomUUID()}`;
      const applyRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(codOrder.id)}/address`,
        {
          headers: { "Idempotency-Key": applyKey },
          body: {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: true,
            expected_new_shipping_fee_vnd: preview.new_shipping_fee_vnd,
            idempotency_key: applyKey,
          },
        }
      );
      assert.equal(applyRes.status, 200, `COD apply expected 200: ${applyRes.text}`);
      assert.equal(applyRes.data?.order?.shipping_fee_vnd, preview.new_shipping_fee_vnd);
      assert.equal(applyRes.data?.order?.total_payable_vnd, preview.new_total_payable_vnd);
      assert.equal(applyRes.data?.order?.shipping_address?.province_code, high.province.code);

      // Idempotent replay with same key
      const replayRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(codOrder.id)}/address`,
        {
          headers: { "Idempotency-Key": applyKey },
          body: {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: true,
            expected_new_shipping_fee_vnd: preview.new_shipping_fee_vnd,
            idempotency_key: applyKey,
          },
        }
      );
      assert.equal(replayRes.status, 200, `COD address replay expected 200: ${replayRes.text}`);
      assert.equal(replayRes.data?.order?.total_payable_vnd, preview.new_total_payable_vnd);

      // --- Case 3B: Prepaid VietQR increase blocked; same-province keeps total unchanged ---
      const { order: prepaidOrder } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addrLow1.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "VIETQR",
      });
      prepaidOrderId = prepaidOrder.id;

      const prepaidPreviewRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(prepaidOrder.id)}/address`,
        { body: { address_id: addrHigh.id, preview_only: true } }
      );
      assert.equal(prepaidPreviewRes.status, 200, `prepaid preview expected 200: ${prepaidPreviewRes.text}`);
      assert.equal(prepaidPreviewRes.data?.blocked_prepaid_increase, true);

      const prepaidBlockedRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(prepaidOrder.id)}/address`,
        {
          body: {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: true,
            expected_new_shipping_fee_vnd: prepaidPreviewRes.data.new_shipping_fee_vnd,
          },
        }
      );
      assert.equal(
        prepaidBlockedRes.status,
        409,
        `prepaid increase apply expected 409, got ${prepaidBlockedRes.status}: ${prepaidBlockedRes.text}`
      );

      // Same province (delta <= 0) succeeds and keeps paid total unchanged
      const prepaidSameRes = await buyerClient.json(
        "PATCH",
        `/api/gw/orders/${encodeURIComponent(prepaidOrder.id)}/address`,
        { body: { address_id: addrLow2.id, preview_only: false } }
      );
      assert.equal(prepaidSameRes.status, 200, `prepaid same-province expected 200: ${prepaidSameRes.text}`);
      assert.equal(
        prepaidSameRes.data?.order?.total_payable_vnd,
        prepaidOrder.total_payable_vnd,
        "prepaid total_payable_vnd must remain unchanged on equal/decrease fee"
      );
      assert.equal(
        prepaidSameRes.data?.order?.shipping_address?.street_address,
        addrLow2.street_address
      );
    } finally {
      await cancelOrderBestEffort(buyerClient, codOrderId, "E2E A8 B26 COD cleanup");
      await cancelOrderBestEffort(buyerClient, prepaidOrderId, "E2E A8 B26 VietQR cleanup");
      await deleteProductBestEffort(seller, created.product.id);
    }
  });

  test("4. S04 & UC-S15 (G4–G5, G8) — Seller orders pagination, status_counts, picking-list, batch-ship & batch PDF labels", async () => {
    await ensureRateBudget(40, { label: "s04-ucs15-seller-orders" });

    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");
    const shopRes = await seller.json("GET", "/api/gw/seller/shop");
    assert.equal(shopRes.status, 200);
    const shopId = shopRes.data?.shop?.id;
    assert.ok(shopId);

    const created = await createApprovedCodProduct(seller, {
      priceVnd: 190_000,
      stock: 25,
      tag: "a8-s15",
      adminClient: admin,
    });

    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "a8-s15", { verifyPhone: true });
    const createdOrderIds = [];

    try {
      const { low } = await resolveLowAndHighFeeLocations(buyerClient, {
        shopId,
        skuId: created.sku.id,
      });
      const addr = await createAddressAtLocation(buyerClient, buyer, low, "s15", true);

      const { order: ord1 } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addr.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "COD",
      });
      createdOrderIds.push(ord1.id);

      const { order: ord2 } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addr.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "COD",
        quantity: 2,
      });
      createdOrderIds.push(ord2.id);

      const { order: ordCancelled } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addr.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "COD",
      });
      createdOrderIds.push(ordCancelled.id);
      await cancelOrderBestEffort(buyerClient, ordCancelled.id, "Intentional cancel for batch-ship SKIPPED");

      // Server pagination + status_counts + keyword/payment filter
      const listRes = await seller.json(
        "GET",
        "/api/gw/seller/orders?page=1&page_size=20&include_status_counts=true"
      );
      assert.equal(listRes.status, 200, `seller/orders expected 200: ${listRes.text}`);
      assert.equal(listRes.data?.page, 1);
      assert.equal(listRes.data?.page_size, 20);
      assert.ok((listRes.data?.total ?? 0) >= 3);
      assert.ok(
        Array.isArray(listRes.data?.status_counts) && listRes.data.status_counts.length > 0,
        `expected status_counts array: ${listRes.text}`
      );

      const kwRes = await seller.json(
        "GET",
        `/api/gw/seller/orders?page=1&page_size=20&payment_method=COD&keyword=${encodeURIComponent(ord1.order_code)}`
      );
      assert.equal(kwRes.status, 200, `seller/orders keyword filter expected 200: ${kwRes.text}`);
      assert.ok(
        (kwRes.data?.items ?? []).some((o) => o.id === ord1.id),
        `keyword filter did not return ${ord1.order_code}`
      );

      // Picking list aggregation
      const pickRes = await seller.json("POST", "/api/gw/seller/orders/picking-list", {
        body: { order_ids: [ord1.id, ord2.id] },
      });
      assert.equal(pickRes.status, 200, `picking-list expected 200: ${pickRes.text}`);
      assert.equal(pickRes.data?.order_count, 2);
      assert.equal(pickRes.data?.total_units, 3);
      assert.ok(
        (pickRes.data?.items ?? []).some((it) => it.sku_id === created.sku.id && it.total_quantity === 3),
        `picking-list did not aggregate sku ${created.sku.id} to qty 3: ${pickRes.text}`
      );

      // Batch ship: 2 QUEUED + 1 SKIPPED (cancelled)
      const batchRes = await seller.json("POST", "/api/gw/seller/orders/batch-ship", {
        body: {
          order_ids: [ord1.id, ord2.id, ordCancelled.id],
          carrier_code: "mock_express",
          service_code: "standard",
        },
      });
      assert.equal(batchRes.status, 200, `batch-ship expected 200: ${batchRes.text}`);
      assert.equal(batchRes.data?.queued_count, 2, `expected queued_count=2: ${batchRes.text}`);
      assert.equal(batchRes.data?.skipped_count, 1, `expected skipped_count=1: ${batchRes.text}`);
      const skippedRow = (batchRes.data?.results ?? []).find((r) => r.order_id === ordCancelled.id);
      assert.equal(skippedRow?.outcome, "SKIPPED");
      assert.ok(skippedRow?.reason_code, "SKIPPED row must include reason_code");

      // Poll until async shipping worker assigns tracking codes to ord1 & ord2
      const trackingCodes = [];
      for (const targetId of [ord1.id, ord2.id]) {
        const shipped = await pollUntil(
          async () => {
            const g = await seller.json("GET", `/api/gw/seller/orders/${encodeURIComponent(targetId)}`);
            const code = String(g.data?.order?.tracking_code ?? "").trim();
            return { done: g.status === 200 && Boolean(code), value: code, detail: `tracking=${code}` };
          },
          {
            timeoutMs: 30_000,
            intervalMs: 500,
            label: `tracking_code for batch-shipped order ${targetId}`,
          }
        );
        trackingCodes.push(shipped);
      }

      // Merged PDF labels via POST /api/gw/shipping/labels/batch (raw fetch to verify PDF bytes)
      const pdfRes = await fetch(`${config.webUrl}/api/gw/shipping/labels/batch`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/pdf",
          Origin: seller.defaultOrigin,
          Cookie: seller.buildCookieHeader(),
        },
        body: JSON.stringify({ tracking_codes: trackingCodes }),
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      assert.equal(pdfRes.status, 200, `labels/batch expected 200, got ${pdfRes.status}`);
      assert.ok(
        (pdfRes.headers.get("content-type") ?? "").toLowerCase().includes("application/pdf"),
        `labels/batch expected application/pdf, got ${pdfRes.headers.get("content-type")}`
      );
      assert.equal(
        (pdfRes.headers.get("x-content-type-options") ?? "").toLowerCase(),
        "nosniff"
      );
      const pdfBuf = new Uint8Array(await pdfRes.arrayBuffer());
      const pdfMagic = Buffer.from(pdfBuf.slice(0, 5)).toString("ascii");
      assert.equal(pdfMagic, "%PDF-", `labels/batch body must start with %PDF-, got ${pdfMagic}`);
      assert.ok(pdfBuf.byteLength > 500, `labels/batch PDF too small: ${pdfBuf.byteLength} bytes`);
    } finally {
      for (const oid of createdOrderIds) {
        await cancelOrderBestEffort(buyerClient, oid, "E2E A8 S15 cleanup");
      }
      await deleteProductBestEffort(seller, created.product.id);
    }
  });

  test("5. S03 & UC-S13 (G6, G8) — Seller products pagination, SKU stock history & 2-step CSV import", async () => {
    await ensureRateBudget(35, { label: "s03-ucs13-seller-products" });

    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");

    // Server pagination check
    const pageRes = await seller.json("GET", "/api/gw/seller/products?page=1&page_size=20");
    assert.equal(pageRes.status, 200, `seller/products expected 200: ${pageRes.text}`);
    assert.equal(pageRes.data?.page, 1);
    assert.equal(pageRes.data?.page_size, 20);
    assert.ok((pageRes.data?.total ?? 0) >= 1);

    const catRes = await seller.json("GET", "/api/gw/categories");
    assert.equal(catRes.status, 200);
    const categoryId = (catRes.data?.categories ?? []).find((c) => !c.is_prohibited)?.id;
    assert.ok(categoryId, "missing non-prohibited category for CSV import");

    const created = await createApprovedCodProduct(seller, {
      priceVnd: 185_000,
      stock: 12,
      tag: "a8-s03",
      adminClient: admin,
    });
    let importedProductId = null;

    try {
      // S03: Manual stock update -> stock-history record
      const oldStock = created.sku.stock;
      const newStock = oldStock + 7;
      const patchRes = await seller.json(
        "PATCH",
        `/api/gw/seller/skus/${encodeURIComponent(created.sku.id)}/stock`,
        { body: { new_stock: newStock, expected_version: created.sku.version } }
      );
      assert.equal(patchRes.status, 200, `update stock expected 200: ${patchRes.text}`);

      const histRes = await seller.json(
        "GET",
        `/api/gw/seller/skus/${encodeURIComponent(created.sku.id)}/stock-history?change_type=MANUAL_UPDATE&page=1&page_size=20`
      );
      assert.equal(histRes.status, 200, `stock-history expected 200: ${histRes.text}`);
      const histItems = histRes.data?.items ?? [];
      assert.ok(
        histItems.some(
          (h) =>
            h.change_type === "MANUAL_UPDATE" &&
            h.delta === 7 &&
            h.stock_before === oldStock &&
            h.stock_after === newStock
        ),
        `stock-history missing expected MANUAL_UPDATE +7 row: ${histRes.text}`
      );

      // R8: low_stock=true filter and keyword search by SKU code
      const lowStockRes = await seller.json(
        "GET",
        "/api/gw/seller/products?low_stock=true&page=1&page_size=20"
      );
      assert.equal(lowStockRes.status, 200, `seller/products low_stock=true expected 200: ${lowStockRes.text}`);
      const skuSearchRes = await seller.json(
        "GET",
        `/api/gw/seller/products?page=1&page_size=20&keyword=${encodeURIComponent(created.sku.sku_code)}`
      );
      assert.equal(skuSearchRes.status, 200, `seller/products keyword by SKU expected 200: ${skuSearchRes.text}`);
      assert.ok(
        (skuSearchRes.data?.items ?? []).some((p) => p.id === created.product.id),
        `R8: keyword=${created.sku.sku_code} must find product ${created.product.id}: ${skuSearchRes.text}`
      );

      // UC-S13: Download template
      const tplRes = await seller.request(
        "GET",
        `/api/gw/seller/products/import/template?category_id=${encodeURIComponent(categoryId)}`,
        { accept: "text/csv" }
      );
      assert.equal(tplRes.status, 200, `import template expected 200: ${tplRes.text}`);
      const tplText = tplRes.text.replace(/^\uFEFF/, "");
      const headerLine = tplText.split(/\r?\n/)[0] ?? "";
      assert.ok(headerLine.includes("product_name"), `template missing product_name header: ${headerLine}`);
      assert.ok(headerLine.includes("sku_code"), `template missing sku_code header: ${headerLine}`);

      // Step 1: Upload CSV with 1 valid row + 1 invalid row containing formula prefix (`=SUM(...)`)
      const tag = crypto.randomBytes(4).toString("hex").toUpperCase();
      const validSkuCode = `CSV-A8-${tag}`;
      const validProdName = `Áo thun CSV E2E ${tag}`;
      const csvBody = [
        "product_name,sku_code,sku_name,category_id,price_vnd,stock,weight_grams,description,image_url",
        `"${validProdName}","${validSkuCode}","Trắng / M","${categoryId}",199000,15,300,"Sản phẩm nhập thử từ CSV E2E","/products/ao-thun-cotton-trang.png"`,
        `"=SUM(1,1) Lỗi ${tag}","BAD-${tag}","Đen / L","${categoryId}",-500,10,300,"Dòng lỗi giá âm","/products/ao-thun-cotton-trang.png"`,
      ].join("\n");

      const form = new FormData();
      form.append("file", new Blob([csvBody], { type: "text/csv;charset=utf-8" }), `import-${tag}.csv`);
      const uploadRes = await fetch(`${config.webUrl}/api/gw/seller/products/import`, {
        method: "POST",
        headers: {
          Accept: "application/json",
          Origin: seller.defaultOrigin,
          Cookie: seller.buildCookieHeader(),
        },
        body: form,
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      const uploadText = await uploadRes.text();
      assert.equal(uploadRes.status, 202, `import upload expected 202, got ${uploadRes.status}: ${uploadText}`);
      const uploadData = JSON.parse(uploadText);
      const jobId = uploadData?.job?.id;
      assert.ok(jobId, `import upload missing job.id: ${uploadText}`);

      // Poll until READY
      const readyJob = await pollUntil(
        async () => {
          const g = await seller.json("GET", `/api/gw/seller/products/import/${encodeURIComponent(jobId)}`);
          const st = String(g.data?.job?.status ?? "").toUpperCase();
          return {
            done: g.status === 200 && (st === "READY" || st === "FAILED"),
            value: g.data?.job,
            detail: `status=${st}`,
          };
        },
        { timeoutMs: 30_000, intervalMs: 400, label: `import job ${jobId} READY` }
      );
      assert.equal(readyJob.status, "READY", `expected import job READY, got ${JSON.stringify(readyJob)}`);
      assert.equal(readyJob.total_rows, 2);
      assert.equal(readyJob.valid_rows, 1);
      assert.equal(readyJob.invalid_rows, 1);

      // Verify errors_only rows and errors.csv formula-injection guard
      const errRowsRes = await seller.json(
        "GET",
        `/api/gw/seller/products/import/${encodeURIComponent(jobId)}/rows?errors_only=true&page=1&page_size=20`
      );
      assert.equal(errRowsRes.status, 200, `import error rows expected 200: ${errRowsRes.text}`);
      assert.equal((errRowsRes.data?.items ?? []).length, 1);
      assert.equal(errRowsRes.data.items[0].status, "INVALID");

      const errCsvRes = await seller.request(
        "GET",
        `/api/gw/seller/products/import/${encodeURIComponent(jobId)}/errors.csv`,
        { accept: "text/csv" }
      );
      assert.equal(errCsvRes.status, 200, `errors.csv expected 200: ${errCsvRes.text}`);
      assert.ok(
        errCsvRes.text.includes("'=SUM(1,1)"),
        `errors.csv must neutralize formula prefix with leading single quote: ${errCsvRes.text}`
      );

      // Step 2: Confirm twice (idempotent)
      const confirm1 = await seller.json(
        "POST",
        `/api/gw/seller/products/import/${encodeURIComponent(jobId)}/confirm`
      );
      assert.equal(confirm1.status, 200, `first confirm expected 200: ${confirm1.text}`);
      assert.equal(confirm1.data?.already_confirmed, false);

      const confirm2 = await seller.json(
        "POST",
        `/api/gw/seller/products/import/${encodeURIComponent(jobId)}/confirm`
      );
      assert.equal(confirm2.status, 200, `second confirm expected 200: ${confirm2.text}`);
      assert.equal(confirm2.data?.already_confirmed, true);

      // Poll until COMPLETED
      const doneJob = await pollUntil(
        async () => {
          const g = await seller.json("GET", `/api/gw/seller/products/import/${encodeURIComponent(jobId)}`);
          const st = String(g.data?.job?.status ?? "").toUpperCase();
          return {
            done: g.status === 200 && (st === "COMPLETED" || st === "FAILED"),
            value: g.data?.job,
            detail: `status=${st}`,
          };
        },
        { timeoutMs: 30_000, intervalMs: 400, label: `import job ${jobId} COMPLETED` }
      );
      assert.equal(doneJob.status, "COMPLETED", `expected COMPLETED: ${JSON.stringify(doneJob)}`);
      assert.equal(doneJob.created_rows, 1);
      assert.equal(doneJob.failed_rows, 0);

      // Verify created product lands in PENDING tab
      const pendingRes = await seller.json(
        "GET",
        `/api/gw/seller/products?status=PENDING&keyword=${encodeURIComponent(validProdName)}&page=1&page_size=20`
      );
      assert.equal(pendingRes.status, 200, `list PENDING imported product expected 200: ${pendingRes.text}`);
      const found = (pendingRes.data?.items ?? []).find((p) => p.name === validProdName);
      assert.ok(found?.id, `imported product "${validProdName}" not found under status=PENDING`);
      assert.equal(found.status, "PENDING");
      importedProductId = found.id;
    } finally {
      await deleteProductBestEffort(seller, created.product.id);
      await deleteProductBestEffort(seller, importedProductId);
    }
  });

  test("6. Viewport layout & UI hygiene across 390 / 768 / 1440 for all A8 buyer & seller routes", async () => {
    await ensureRateBudget(40, { label: "a8-viewports" });

    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");
    const shopRes = await seller.json("GET", "/api/gw/seller/shop");
    assert.equal(shopRes.status, 200);
    const shopId = shopRes.data?.shop?.id;
    assert.ok(shopId);

    const created = await createApprovedCodProduct(seller, {
      priceVnd: 210_000,
      stock: 10,
      tag: "a8-vp",
      adminClient: admin,
    });
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "a8-vp", { verifyPhone: true });
    let orderId = null;

    const browser = await HeadlessBrowser.launch();
    const viewports = [
      { width: 390, height: 844, mobile: true },
      { width: 768, height: 1024, mobile: false },
      { width: 1440, height: 900, mobile: false },
    ];

    const assertNoOverflow = async (route, vp) => {
      const metrics = await browser.eval(
        `(() => ({ scrollW: document.documentElement.scrollWidth, innerW: window.innerWidth, text: document.body?.innerText || "" }))()`
      );
      assert.ok(
        metrics.scrollW <= metrics.innerW + 2,
        `Horizontal overflow on ${route} at ${vp.width}x${vp.height}: scrollWidth=${metrics.scrollW} > innerWidth=${metrics.innerW}`
      );
      return metrics.text;
    };

    try {
      const { low } = await resolveLowAndHighFeeLocations(buyerClient, {
        shopId,
        skuId: created.sku.id,
      });
      const addr = await createAddressAtLocation(buyerClient, buyer, low, "vp", true);
      const { order } = await checkoutSingleShopOrder(buyerClient, {
        addressId: addr.id,
        shopId,
        skuId: created.sku.id,
        carrierCode: low.quote.carrier_code,
        serviceCode: low.quote.service_code,
        paymentMethod: "COD",
      });
      orderId = order.id;

      // 1. Public routes: /tra-cuu-don-hang, /hoi-vien, /gio-hang
      for (const route of ["/tra-cuu-don-hang", "/hoi-vien", "/gio-hang"]) {
        for (const vp of viewports) {
          await browser.setViewport(vp.width, vp.height, vp.mobile);
          await browser.goto(route);
          const text = await assertNoOverflow(route, vp);
          if (route === "/tra-cuu-don-hang") {
            assert.equal(text.includes("NM-261120-"), false, "no fake order codes on /tra-cuu-don-hang");
          }
          if (route === "/hoi-vien") {
            for (const retired of RETIRED_VOUCHERS) {
              assert.equal(text.includes(retired), false, `no ${retired} on /hoi-vien`);
            }
          }
        }
      }

      // 2. Buyer route: /don-hang/{id} shows "Sửa địa chỉ" on CONFIRMED order
      await browser.clearCookies();
      await browser.setCookiesFromClient(buyerClient);
      for (const vp of viewports) {
        await browser.setViewport(vp.width, vp.height, vp.mobile);
        await browser.goto(`/don-hang/${encodeURIComponent(order.id)}`);
        const text = await assertNoOverflow(`/don-hang/${order.id}`, vp);
        assert.ok(text.includes("Sửa địa chỉ"), `expected "Sửa địa chỉ" button at ${vp.width}px`);
      }

      // 3. Seller routes: /seller/don-hang & /seller/san-pham
      await browser.clearCookies();
      await browser.setCookiesFromClient(seller);
      for (const vp of viewports) {
        await browser.setViewport(vp.width, vp.height, vp.mobile);
        await browser.goto("/seller/don-hang");
        await assertNoOverflow("/seller/don-hang", vp);
      }
      for (const vp of viewports) {
        await browser.setViewport(vp.width, vp.height, vp.mobile);
        await browser.goto("/seller/san-pham");
        const prodText = await assertNoOverflow("/seller/san-pham", vp);
        assert.ok(prodText.includes("Nhập file CSV"), `expected "Nhập file CSV" button at ${vp.width}px`);
      }
    } finally {
      await browser.close();
      await cancelOrderBestEffort(buyerClient, orderId, "E2E A8 viewport cleanup");
      await deleteProductBestEffort(seller, created.product.id);
    }
  });
});
