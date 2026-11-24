import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  ACCESS_COOKIE,
  COD_SAFE_SUBTOTAL_CAP_VND,
  RATE_LIMIT_MAX_WAIT_MS,
  RATE_LIMIT_SSR_DEFAULT_WAIT_MS,
  RATE_LIMIT_SSR_TEXT,
  REFRESH_COOKIE,
  WebClient,
  calculateShippingQuote,
  config,
  createApprovedCodProduct,
  createBuyerAddress,
  ensureRateBudget,
  fetchWith429Retry,
  normalizeStatus,
  parseRetryAfterMs,
  pollUntil,
  postSignedPaymentWebhook,
  registerAndLoginBuyer,
} from "./helpers.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "..", "..");

function readDotEnvKey(name, fallback = "") {
  if (process.env[name]) return process.env[name];
  const envPath = path.join(REPO_ROOT, ".env");
  if (!fs.existsSync(envPath)) return fallback;
  const lines = fs.readFileSync(envPath, "utf8").split(/\r?\n/);
  for (const raw of lines) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq <= 0) continue;
    const k = line.slice(0, eq).trim();
    if (k !== name) continue;
    let v = line.slice(eq + 1).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    return v || fallback;
  }
  return fallback;
}

const SELLER_EMAIL = process.env.E2E_SELLER_EMAIL || "seller.bachdang@nexusmart.local";
const SELLER_PASSWORD = process.env.E2E_SELLER_PASSWORD || "SellerDevPassword123!";
const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";
const SHIPPING_WEBHOOK_SECRET = readDotEnvKey(
  "SHIPPING_WEBHOOK_SECRET",
  "dev-only-shipping-webhook-hmac-secret"
);

function normalizeReturnStatus(raw) {
  return String(raw ?? "")
    .trim()
    .toUpperCase()
    .replace(/^RETURN_STATUS_/, "");
}

function normalizeRefundStatus(raw) {
  return String(raw ?? "")
    .trim()
    .toUpperCase()
    .replace(/^REFUND_STATUS_/, "");
}

async function postSignedShippingWebhook(carrier, payload) {
  const rawBody = JSON.stringify(payload);
  const signature = crypto
    .createHmac("sha256", SHIPPING_WEBHOOK_SECRET)
    .update(rawBody, "utf8")
    .digest("hex");
  const timestamp = String(Math.floor(Date.now() / 1000));

  const res = await fetchWith429Retry(
    `${config.gatewayUrl}/webhooks/shipping/${encodeURIComponent(carrier || "ghn")}`,
    () => ({
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Signature": signature,
        "X-Timestamp": timestamp,
      },
      body: rawBody,
      signal: AbortSignal.timeout(config.requestTimeoutMs),
    })
  );
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  return { status: res.status, text, data };
}

async function waitForPasswordResetToken(email, options = {}) {
  const target = email.trim().toLowerCase();
  const minCreatedMs = options.afterMs ?? 0;

  return pollUntil(
    async () => {
      const listRes = await fetch(`${config.mailpitUrl}/api/v1/messages?limit=100`, {
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      if (!listRes.ok) {
        return { done: false, detail: `mailpit HTTP ${listRes.status}` };
      }
      const list = await listRes.json();
      const messages = Array.isArray(list?.messages) ? list.messages : [];
      for (const summary of messages) {
        const recipients = Array.isArray(summary?.To) ? summary.To : [];
        const matched = recipients.some(
          (r) => String(r?.Address ?? "").trim().toLowerCase() === target
        );
        if (!matched) continue;
        const createdMs = summary?.Created ? Date.parse(summary.Created) : 0;
        if (minCreatedMs > 0 && createdMs > 0 && createdMs + 2000 < minCreatedMs) continue;

        const msgRes = await fetch(
          `${config.mailpitUrl}/api/v1/message/${encodeURIComponent(summary.ID)}`,
          { signal: AbortSignal.timeout(config.requestTimeoutMs) }
        );
        if (!msgRes.ok) continue;
        const full = await msgRes.json();
        const combined = [full?.Subject, full?.Text, full?.HTML, summary?.Snippet]
          .filter(Boolean)
          .join("\n");
        const urlMatch =
          /dat-lai-mat-khau\?[^"'\s<>]*token=([A-Za-z0-9._~%-]+)/i.exec(combined) ||
          /reset-password\?[^"'\s<>]*token=([A-Za-z0-9._~%-]+)/i.exec(combined);
        if (urlMatch && urlMatch[1]) {
          return { done: true, value: decodeURIComponent(urlMatch[1]) };
        }
      }
      return {
        done: false,
        detail: `no password reset email for ${target} among ${messages.length} messages`,
      };
    },
    {
      timeoutMs: options.timeoutMs ?? config.pollTimeoutMs,
      intervalMs: options.intervalMs ?? config.pollIntervalMs,
      label: `password reset email for ${target}`,
    }
  );
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
    assert.ok(chromeBin, "Headless Chrome binary not found for 390px overflow E2E checks");

    const userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), "nm-e2e-w2a-"));
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
    await browser.send("Log.enable");
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
    // Unload any live page first so in-flight /api/gw calls cannot 401 after cookies are cleared
    // and trigger SessionContext's /dang-nhap?expired=1&next=... redirect racing the next navigation.
    await this.send("Page.navigate", { url: "about:blank" });
    await pollUntil(
      async () => {
        const done = await this.eval(
          `window.location.href === "about:blank" && document.readyState === "complete"`
        ).catch(() => false);
        return { done: Boolean(done), detail: "about:blank" };
      },
      { timeoutMs: 15_000, intervalMs: 50, label: "unload page to about:blank before clearCookies" }
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
      if (!isRateLimited) {
        return;
      }

      const rawWaitMs = this.lastRateLimit?.retryAfterMs ?? RATE_LIMIT_SSR_DEFAULT_WAIT_MS;
      const waitMs = rawWaitMs + 250;
      const elapsedMs = Date.now() - startMs;
      if (elapsedMs + waitMs > RATE_LIMIT_MAX_WAIT_MS) {
        return;
      }
      const reason = this.lastRateLimit
        ? `HTTP 429 (${this.lastRateLimit.url})`
        : `SSR "${RATE_LIMIT_SSR_TEXT}"`;
      console.log(
        `[429] browser GET ${reqPath} rate limited (${reason}); waiting ${Math.ceil(waitMs / 1000)}s before reload (elapsed ${Math.ceil((elapsedMs + waitMs) / 1000)}s/${RATE_LIMIT_MAX_WAIT_MS / 1000}s)`
      );
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

async function loginSeededAccount(email, password, label) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `${label} login expected 200, got ${res.status}: ${res.text}`);
  return { client, user: res.data?.user };
}

async function cancelOrderBestEffort(buyerClient, orderId, reason) {
  if (!orderId) return;
  try {
    await buyerClient.json("POST", `/api/gw/orders/${encodeURIComponent(orderId)}/cancel`, {
      body: { reason },
    });
  } catch {
    // Ignore cleanup errors (e.g. order already completed/returned/cancelled).
  }
}

async function createSellerShopOrder(seller, buyerClient, buyer, { paymentMethod, tag }) {
  const { address, province, ward } = await createBuyerAddress(buyerClient, buyer);

  const shopRes = await seller.json("GET", "/api/gw/seller/shop");
  assert.equal(shopRes.status, 200, `seller/shop expected 200: ${shopRes.text}`);
  const shopId = shopRes.data?.shop?.id;
  assert.ok(shopId, "seller shop_id missing");

  const prodRes = await buyerClient.json(
    "GET",
    `/api/gw/products?shop_id=${encodeURIComponent(shopId)}&page=1&page_size=100`
  );
  assert.equal(prodRes.status, 200, `list shop products expected 200: ${prodRes.text}`);
  const products = prodRes.data?.items ?? [];
  const maxSkuPrice = paymentMethod === "COD" ? COD_SAFE_SUBTOTAL_CAP_VND : Number.POSITIVE_INFINITY;
  const eligibleSkus = [];
  for (const p of products) {
    for (const s of p.skus ?? []) {
      if (
        s.is_active !== false &&
        (s.available_stock ?? 0) >= 2 &&
        (s.price_vnd ?? 0) > 0 &&
        s.price_vnd <= maxSkuPrice
      ) {
        eligibleSkus.push(s);
      }
    }
  }
  eligibleSkus.sort((a, b) => a.price_vnd - b.price_vnd);
  let chosenSku = eligibleSkus[0] ?? null;
  if (!chosenSku) {
    const created = await createApprovedCodProduct(seller, {
      priceVnd: 250_000,
      stock: 20,
      tag: `ret-${tag}`,
    });
    chosenSku = created.sku;
  }
  assert.ok(chosenSku, `expected at least 1 active in-stock SKU for shop ${shopId}`);

  const quote = await calculateShippingQuote(buyerClient, {
    shopId,
    province,
    ward,
    weightGrams: chosenSku.weight_grams || 300,
    codAmountVnd: paymentMethod === "COD" ? chosenSku.price_vnd : 0,
  });

  const checkoutRes = await buyerClient.json("POST", "/api/gw/checkout", {
    headers: { "Idempotency-Key": `e2e-w2a-${tag}-${crypto.randomUUID()}` },
    body: {
      address_id: address.id,
      from_cart: false,
      items: [{ sku_id: chosenSku.id, quantity: 1 }],
      coins_to_use: 0,
      payment_method: paymentMethod,
      shipping_choices: [
        {
          shop_id: shopId,
          carrier_code: quote.carrier_code,
          service_code: quote.service_code,
        },
      ],
    },
  });
  assert.ok(
    checkoutRes.status === 201 || checkoutRes.status === 200,
    `checkout expected 201/200, got ${checkoutRes.status}: ${checkoutRes.text}`
  );
  const order = checkoutRes.data?.orders?.[0];
  assert.ok(order?.id, `checkout missing order: ${checkoutRes.text}`);
  return { shopId, chosenSku, quote, checkoutRes, order };
}

describe("06. Wave 2a returns, refund settlement, forgot password, and 390px layout", () => {
  test("6a. Return lifecycle: buyer request -> seller accept return -> RETURN delivered -> confirm received -> buyer bank account -> admin settle", async () => {
    await ensureRateBudget(45, { label: "6a" });
    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "ret-flow");

    const { quote, checkoutRes, order } = await createSellerShopOrder(seller, buyerClient, buyer, {
      paymentMethod: "VIETQR",
      tag: "ret",
    });

    let orderFinished = false;
    try {
      // Pay via VietQR webhook
      if ((checkoutRes.data?.total_payable_vnd ?? 0) > 0) {
        const payWebhookRes = await postSignedPaymentWebhook({
          provider: "vietqr_mock",
          provider_tx_id: `TXN-RET-${crypto.randomUUID()}`,
          payment_id: checkoutRes.data.payment_id,
          order_group_id: checkoutRes.data.checkout_group_id,
          amount_vnd: checkoutRes.data.total_payable_vnd,
          status: "SUCCEEDED",
          reference: `NEXUSMART ${checkoutRes.data.checkout_group_id}`,
          occurred_at: new Date().toISOString(),
        });
        assert.equal(payWebhookRes.status, 200, `payment webhook failed: ${payWebhookRes.text}`);
      }

      await pollUntil(
        async () => {
          const res = await buyerClient.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
          const st = normalizeStatus(res.data?.order?.status);
          return { done: st === "CONFIRMED", detail: `order status=${st}` };
        },
        { label: "order CONFIRMED" }
      );

      // Seller creates shipment -> stays PREPARING with tracking_code (Wave 2a ship flow)
      const shipRes = await seller.json(
        "POST",
        `/api/gw/seller/orders/${encodeURIComponent(order.id)}/ship`,
        {
          body: {
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        }
      );
      assert.equal(shipRes.status, 200, `seller ship expected 200, got ${shipRes.status}: ${shipRes.text}`);
      const trackingCode = shipRes.data?.tracking_code || shipRes.data?.order?.tracking_code;
      assert.ok(trackingCode, `seller ship missing tracking_code: ${shipRes.text}`);
      assert.equal(
        normalizeStatus(shipRes.data?.order?.status),
        "PREPARING",
        `Wave 2a ship must keep order in PREPARING awaiting pickup, got ${shipRes.data?.order?.status}`
      );

    // [Review #1] Shipping label via BFF forwards single sandbox CSP and nosniff
    const fwdLabelRes = await seller.request(
      "GET",
      `/api/gw/shipping/labels/${encodeURIComponent(trackingCode)}`
    );
    assert.equal(
      fwdLabelRes.status,
      200,
      `seller GET BFF shipping label expected 200, got ${fwdLabelRes.status}: ${fwdLabelRes.text}`
    );
    assert.ok(
      (fwdLabelRes.headers.get("content-type") || "").toLowerCase().includes("text/html"),
      `BFF shipping label expected text/html, got ${fwdLabelRes.headers.get("content-type")}`
    );
    assert.equal(
      fwdLabelRes.headers.get("x-content-type-options"),
      "nosniff",
      "BFF shipping label must set X-Content-Type-Options: nosniff"
    );
    const fwdCsp = fwdLabelRes.headers.get("content-security-policy") || "";
    assert.ok(
      fwdCsp.includes("sandbox"),
      `BFF shipping label CSP must contain sandbox, got: ${fwdCsp}`
    );
    assert.equal(
      fwdCsp.includes(","),
      false,
      `BFF shipping label must emit a single CSP header (no comma-joined duplicates), got: ${fwdCsp}`
    );

    // Carrier FORWARD webhooks: delivering -> delivered
    const whDelivering = await postSignedShippingWebhook(quote.carrier_code, {
      event_id: `e2e-fwd-deliv-${crypto.randomUUID()}`,
      tracking_code: trackingCode,
      order_id: order.id,
      raw_status: "delivering",
      description: "Đang giao hàng tới người mua",
      occurred_at: new Date().toISOString(),
    });
    assert.equal(whDelivering.status, 200, `FORWARD delivering webhook failed: ${whDelivering.text}`);

    const whDelivered = await postSignedShippingWebhook(quote.carrier_code, {
      event_id: `e2e-fwd-done-${crypto.randomUUID()}`,
      tracking_code: trackingCode,
      order_id: order.id,
      raw_status: "delivered",
      description: "Giao hàng thành công",
      occurred_at: new Date().toISOString(),
    });
    assert.equal(whDelivered.status, 200, `FORWARD delivered webhook failed: ${whDelivered.text}`);

    await pollUntil(
      async () => {
        const res = await buyerClient.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
        const st = normalizeStatus(res.data?.order?.status);
        return { done: st === "DELIVERED", detail: `order status=${st}` };
      },
      { label: "order DELIVERED" }
    );

    // Buyer submits return request with evidence URL
    const reqRetRes = await buyerClient.json(
      "POST",
      `/api/gw/orders/${encodeURIComponent(order.id)}/return`,
      {
        body: {
          reason: "Sản phẩm sai kích cỡ cần đổi trả hoàn tiền",
          evidence_urls: ["https://example.com/e2e-return-proof.jpg"],
        },
      }
    );
    assert.equal(
      reqRetRes.status,
      200,
      `buyer request return expected 200, got ${reqRetRes.status}: ${reqRetRes.text}`
    );
    const returnRequestId = reqRetRes.data?.return_request_id;
    assert.ok(returnRequestId, `request return missing return_request_id: ${reqRetRes.text}`);

    // Buyer SSR page shows return progress + external evidence hostname with nofollow
    const buyerOrderPage = await buyerClient.page(`/don-hang/${encodeURIComponent(order.id)}`);
    assert.equal(buyerOrderPage.status, 200, `SSR /don-hang/${order.id} expected 200`);
    const buyerOrderHtml = buyerOrderPage.text.replace(/<!--[\s\S]*?-->/g, "");
    assert.ok(
      buyerOrderHtml.includes("Tiến trình trả hàng"),
      "Buyer /don-hang/[id] must render return progress section"
    );
    assert.ok(
      buyerOrderHtml.includes("(example.com)") &&
        buyerOrderHtml.includes('rel="noopener noreferrer nofollow"'),
      "Buyer /don-hang/[id] must display evidence hostname and rel=noopener noreferrer nofollow"
    );

    // Seller accepts return (creates RETURN shipment)
    const sellerRespRes = await seller.json(
      "POST",
      `/api/gw/seller/returns/${encodeURIComponent(returnRequestId)}/respond`,
      {
        body: {
          decision: "ACCEPT_RETURN",
          note: "Gian hàng đồng ý nhận lại sản phẩm để kiểm tra hoàn tiền",
          evidence_urls: [],
        },
      }
    );
    assert.equal(
      sellerRespRes.status,
      200,
      `seller accept return expected 200, got ${sellerRespRes.status}: ${sellerRespRes.text}`
    );
    assert.equal(
      normalizeReturnStatus(sellerRespRes.data?.return_request?.status),
      "AWAITING_SHIPMENT",
      `expected AWAITING_SHIPMENT after ACCEPT_RETURN, got ${sellerRespRes.text}`
    );
    const returnTrackingCode = sellerRespRes.data?.return_request?.return_tracking_code;
    assert.ok(returnTrackingCode, `expected return_tracking_code after ACCEPT_RETURN: ${sellerRespRes.text}`);

    // [Review #1 & #2] Buyer can fetch RETURN label via BFF with single sandbox CSP + SSR links to BFF path
    const retLabelPath = `/api/gw/shipping/labels/${encodeURIComponent(returnTrackingCode)}`;
    const buyerRetLabelRes = await buyerClient.request("GET", retLabelPath);
    assert.equal(
      buyerRetLabelRes.status,
      200,
      `buyer GET BFF return label expected 200, got ${buyerRetLabelRes.status}: ${buyerRetLabelRes.text}`
    );
    assert.equal(buyerRetLabelRes.headers.get("x-content-type-options"), "nosniff");
    const retCsp = buyerRetLabelRes.headers.get("content-security-policy") || "";
    assert.ok(retCsp.includes("sandbox") && !retCsp.includes(","), `unexpected return label CSP: ${retCsp}`);

    const buyerOrderAfterAccept = await buyerClient.page(`/don-hang/${encodeURIComponent(order.id)}`);
    assert.equal(buyerOrderAfterAccept.status, 200);
    assert.ok(
      buyerOrderAfterAccept.text.includes(retLabelPath),
      `Buyer /don-hang/[id] must link return label via BFF path ${retLabelPath}`
    );

    // Webhook RETURN shipment delivered
    const whRetDelivered = await postSignedShippingWebhook(quote.carrier_code, {
      event_id: `e2e-ret-done-${crypto.randomUUID()}`,
      tracking_code: returnTrackingCode,
      order_id: order.id,
      raw_status: "delivered",
      description: "Đã hoàn hàng về kho gian hàng",
      occurred_at: new Date().toISOString(),
    });
    assert.equal(
      whRetDelivered.status,
      200,
      `RETURN delivered webhook failed: ${whRetDelivered.text}`
    );

    await pollUntil(
      async () => {
        const res = await seller.json(
          "GET",
          `/api/gw/seller/returns/${encodeURIComponent(returnRequestId)}`
        );
        const st = normalizeReturnStatus(res.data?.return_request?.status);
        return { done: st === "RECEIVED", detail: `return status=${st}` };
      },
      { label: "return request RECEIVED" }
    );

    // Seller confirms received -> triggers REFUNDING -> REFUNDED + pending refund record
    const confirmRetRes = await seller.json(
      "POST",
      `/api/gw/seller/returns/${encodeURIComponent(returnRequestId)}/confirm-received`
    );
    assert.equal(
      confirmRetRes.status,
      200,
      `seller confirm-received expected 200, got ${confirmRetRes.status}: ${confirmRetRes.text}`
    );

    // Buyer polls /orders/{id}/refunds until refund record appears
    const buyerRefund = await pollUntil(
      async () => {
        const res = await buyerClient.json(
          "GET",
          `/api/gw/orders/${encodeURIComponent(order.id)}/refunds`
        );
        const items = res.data?.items ?? [];
        if (items.length > 0) {
          return { done: true, value: items[0] };
        }
        return { done: false, detail: `refunds count=0 (status=${res.status})` };
      },
      { label: "buyer order refund created" }
    );
    assert.ok(buyerRefund?.refund_id, "expected refund_id on buyer refund");
    assert.equal(normalizeRefundStatus(buyerRefund.status), "PENDING");

    // Buyer submits receiving bank account
    const bankRes = await buyerClient.json(
      "POST",
      `/api/gw/refunds/${encodeURIComponent(buyerRefund.refund_id)}/bank-account`,
      {
        body: {
          bank_code: "VCB",
          bank_account_number: "0123456789",
          bank_account_name: "NGUYEN E2E BUYER",
        },
      }
    );
    assert.equal(
      bankRes.status,
      200,
      `submit bank account expected 200, got ${bankRes.status}: ${bankRes.text}`
    );
    assert.ok(
      bankRes.data?.refund?.bank_account_masked,
      `buyer refund response should include masked bank account: ${bankRes.text}`
    );
    assert.equal(
      bankRes.data?.refund?.bank_account_number || "",
      "",
      "buyer refund response must NOT expose unmasked bank_account_number"
    );

    // Admin sees full bank_account_number in /admin/refunds and settles transfer
    const adminRefundsRes = await admin.json(
      "GET",
      `/api/gw/admin/refunds?status=pending&order_id=${encodeURIComponent(order.id)}`
    );
    assert.equal(
      adminRefundsRes.status,
      200,
      `admin/refunds expected 200, got ${adminRefundsRes.status}: ${adminRefundsRes.text}`
    );
    const adminRefundRow = (adminRefundsRes.data?.items ?? []).find(
      (r) => r.refund_id === buyerRefund.refund_id
    );
    assert.ok(adminRefundRow, `admin/refunds missing refund ${buyerRefund.refund_id}`);
    assert.equal(
      adminRefundRow.bank_account_number,
      "0123456789",
      "admin/refunds must include full bank_account_number for settlement"
    );

    const providerRef = `FT-E2E-${Date.now()}`;
    const settleRes = await admin.json(
      "POST",
      `/api/gw/admin/refunds/${encodeURIComponent(buyerRefund.refund_id)}/settle`,
      {
        headers: { "Idempotency-Key": `e2e-settle-${crypto.randomUUID()}` },
        body: {
          provider_refund_id: providerRef,
          note: "Đã chuyển khoản hoàn tiền E2E",
        },
      }
    );
    assert.equal(
      settleRes.status,
      200,
      `admin settle refund expected 200, got ${settleRes.status}: ${settleRes.text}`
    );
    const settledStatus = normalizeRefundStatus(settleRes.data?.refund?.status);
    assert.ok(
      settledStatus === "DONE" || settledStatus === "COMPLETED",
      `expected settled refund status DONE/COMPLETED, got ${settledStatus}`
    );

    // Buyer verifies refund is now settled
    const finalRefundsRes = await buyerClient.json(
      "GET",
      `/api/gw/orders/${encodeURIComponent(order.id)}/refunds`
    );
    const finalRefund = (finalRefundsRes.data?.items ?? []).find(
      (r) => r.refund_id === buyerRefund.refund_id
    );
    assert.ok(finalRefund, "buyer final refunds check missing refund");
    const finalStatus = normalizeRefundStatus(finalRefund.status);
      assert.ok(
        finalStatus === "DONE" || finalStatus === "COMPLETED",
        `buyer should see settled refund, got ${finalStatus}`
      );
      orderFinished = true;
    } finally {
      if (!orderFinished) {
        await cancelOrderBestEffort(buyerClient, order.id, "e2e 6a cleanup on failure");
      }
    }
  });

  test("6b. Forgot password & reset password via Mailpit + /quen-mat-khau & /dat-lai-mat-khau", async () => {
    await ensureRateBudget(25, { label: "6b" });
    const anon = new WebClient();
    const loginPage = await anon.page("/dang-nhap");
    assert.equal(loginPage.status, 200, `GET /dang-nhap expected 200, got ${loginPage.status}`);

    const forgotPage = await anon.page("/quen-mat-khau");
    assert.equal(forgotPage.status, 200, `GET /quen-mat-khau expected 200, got ${forgotPage.status}`);
    assert.equal(forgotPage.headers.get("x-content-type-options"), "nosniff");
    assert.equal(forgotPage.headers.get("x-frame-options"), "DENY");
    assert.equal(forgotPage.headers.get("referrer-policy"), "strict-origin-when-cross-origin");

    const resetPage = await anon.page("/dat-lai-mat-khau?token=preview-token");
    assert.equal(
      resetPage.status,
      200,
      `GET /dat-lai-mat-khau expected 200, got ${resetPage.status}`
    );
    assert.equal(resetPage.headers.get("x-content-type-options"), "nosniff");
    assert.equal(resetPage.headers.get("x-frame-options"), "DENY");
    assert.equal(
      resetPage.headers.get("referrer-policy"),
      "no-referrer",
      "/dat-lai-mat-khau must send Referrer-Policy: no-referrer"
    );

    // Register a fresh buyer and run forgot -> Mailpit -> reset -> login
    const client = new WebClient();
    const buyer = await registerAndLoginBuyer(client, "forgot-pw");
    const startedAt = Date.now();

    const forgotRes = await anon.json("POST", "/api/auth/forgot-password", {
      body: { email: buyer.email },
    });
    assert.equal(
      forgotRes.status,
      200,
      `POST /api/auth/forgot-password expected 200, got ${forgotRes.status}: ${forgotRes.text}`
    );
    assert.equal(forgotRes.data?.sent, true);

    const token = await waitForPasswordResetToken(buyer.email, { afterMs: startedAt });
    assert.ok(token.length >= 16, `expected password reset token from Mailpit, got ${token}`);

    const newPassword = "NewNexusPassword456!";
    const resetRes = await anon.json("POST", "/api/auth/reset-password", {
      body: { token, new_password: newPassword },
    });
    assert.equal(
      resetRes.status,
      200,
      `POST /api/auth/reset-password expected 200, got ${resetRes.status}: ${resetRes.text}`
    );
    assert.equal(resetRes.data?.ok, true);

    // Old password rejected, new password succeeds
    const oldLoginRes = await anon.json("POST", "/api/auth/login", {
      body: { email: buyer.email, password: buyer.password },
    });
    assert.equal(oldLoginRes.status, 401, "login with old password must fail after reset");

    const newLoginRes = await anon.json("POST", "/api/auth/login", {
      body: { email: buyer.email, password: newPassword },
    });
    assert.equal(
      newLoginRes.status,
      200,
      `login with new password expected 200, got ${newLoginRes.status}: ${newLoginRes.text}`
    );
  });

  test("6c. Seller shipping settings, Admin carriers & reconciliation queue BFF API smoke check", async () => {
    await ensureRateBudget(15, { label: "6c" });
    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");

    const shipSettings = await seller.json("GET", "/api/gw/seller/shipping-settings");
    assert.equal(
      shipSettings.status,
      200,
      `GET seller/shipping-settings expected 200, got ${shipSettings.status}: ${shipSettings.text}`
    );

    const carriers = await admin.json("GET", "/api/gw/admin/carriers");
    assert.equal(
      carriers.status,
      200,
      `GET admin/carriers expected 200, got ${carriers.status}: ${carriers.text}`
    );

    const reconQueue = await admin.json("GET", "/api/gw/admin/reconciliation/queue");
    assert.equal(
      reconQueue.status,
      200,
      `GET admin/reconciliation/queue expected 200, got ${reconQueue.status}: ${reconQueue.text}`
    );

    const adminReturns = await admin.json("GET", "/api/gw/admin/returns");
    assert.equal(
      adminReturns.status,
      200,
      `GET admin/returns expected 200, got ${adminReturns.status}: ${adminReturns.text}`
    );
  });

  test("6d. 390px mobile horizontal overflow check across new and updated Wave 2a pages", async () => {
    await ensureRateBudget(60, { label: "6d" });
    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const { client: admin } = await loginSeededAccount(ADMIN_EMAIL, ADMIN_PASSWORD, "admin");
    const buyer = new WebClient();
    const buyerProfile = await registerAndLoginBuyer(buyer, "overflow-390");

    // Create a real order owned by this exact buyer so /don-hang/[id] renders the actual order detail page (not 404).
    const { order: ownedOrder } = await createSellerShopOrder(seller, buyer, buyerProfile, {
      paymentMethod: "COD",
      tag: "overflow-390",
    });

    const browser = await HeadlessBrowser.launch();
    try {
      await browser.setViewport(390, 844, true);

      const checkRoute = async (routePath) => {
        await browser.goto(routePath);
        const metrics = await browser.eval(`(() => ({
          pathname: window.location.pathname,
          innerWidth: window.innerWidth,
          scrollWidth: document.documentElement.scrollWidth,
          bodyText: (document.body?.innerText || "").slice(0, 4000)
        }))()`);
        assert.ok(
          metrics.scrollWidth <= metrics.innerWidth,
          `390px horizontal overflow on ${routePath} (resolved ${metrics.pathname}): scrollWidth=${metrics.scrollWidth} > innerWidth=${metrics.innerWidth}`
        );
        assert.equal(
          /RETURN_STATUS_|REFUND_STATUS_|Order created awaiting payment|seller_confirm_timeout/.test(
            metrics.bodyText
          ),
          false,
          `Raw internal enum/English text leaked on ${routePath}`
        );
        return metrics;
      };

      // Public / auth pages
      await browser.clearCookies();
      for (const p of ["/dang-nhap", "/dang-ky", "/quen-mat-khau", "/dat-lai-mat-khau?token=demo"]) {
        await checkRoute(p);
        if (p === "/dang-nhap") {
          const hasForgotLink = await browser.eval(
            `Boolean(document.querySelector('a[href="/quen-mat-khau"]'))`
          );
          assert.equal(hasForgotLink, true, "/dang-nhap must render link to /quen-mat-khau");
        }
        if (p.startsWith("/dat-lai-mat-khau")) {
          const resetCheck = await browser.eval(`(() => ({
            search: window.location.search,
            hasPasswordInput: Boolean(document.querySelector('#reset-new-password'))
          }))()`);
          assert.equal(
            resetCheck.search,
            "",
            `/dat-lai-mat-khau must strip token from URL via history.replaceState, got search=${resetCheck.search}`
          );
          assert.equal(
            resetCheck.hasPasswordInput,
            true,
            "/dat-lai-mat-khau must keep token in memory and keep reset password form rendered"
          );
        }
      }

      // Buyer pages (using the actual owner of ownedOrder so /don-hang/[id] is NOT 404)
      await ensureRateBudget(25, { label: "6d-buyer" });
      await browser.setCookiesFromClient(buyer);
      await checkRoute("/don-hang");
      const orderDetailMetrics = await checkRoute(`/don-hang/${encodeURIComponent(ownedOrder.id)}`);
      assert.ok(
        ownedOrder.order_code && orderDetailMetrics.bodyText.includes(ownedOrder.order_code),
        `expected /don-hang/${ownedOrder.id} to render real order ${ownedOrder.order_code} (not 404), got: ${orderDetailMetrics.bodyText.slice(0, 300)}`
      );
      assert.equal(
        /không tìm thấy trang|404/i.test(orderDetailMetrics.bodyText),
        false,
        `expected /don-hang/${ownedOrder.id} not to render 404 page`
      );

      // Seller pages
      await ensureRateBudget(25, { label: "6d-seller" });
      await browser.clearCookies();
      await browser.setCookiesFromClient(seller);
      for (const p of ["/seller/don-hang", "/seller/tra-hang", "/seller/ho-so", "/seller/tai-chinh"]) {
        await checkRoute(p);
      }

      // Admin pages
      await ensureRateBudget(25, { label: "6d-admin" });
      await browser.clearCookies();
      await browser.setCookiesFromClient(admin);
      for (const p of ["/admin/khieu-nai", "/admin/doi-soat", "/admin/cau-hinh", "/admin/audit-log"]) {
        await checkRoute(p);
      }
    } finally {
      await browser.close();
      await cancelOrderBestEffort(buyer, ownedOrder.id, "e2e 6d cleanup");
    }
  });

  test("6e. [PLT-B6] BFF rejects > 2 MiB request bodies (Content-Length & chunked) with 413 + nosniff", async () => {
    const webUrl = new URL(config.webUrl);
    const overLimitBytes = 2 * 1024 * 1024 + 1024;
    const largePayload = `{"pad":"${"a".repeat(overLimitBytes)}"}`;

    // 1. Fixed Content-Length > 2 MiB
    const fixedClient = new WebClient();
    const fixedRes = await fixedClient.request("POST", "/api/gw/cart/items", {
      headers: { "Content-Type": "application/json" },
      body: largePayload,
    });
    assert.equal(
      fixedRes.status,
      413,
      `BFF POST with > 2 MiB Content-Length expected 413, got ${fixedRes.status}: ${fixedRes.text}`
    );
    assert.equal(fixedRes.headers.get("x-content-type-options"), "nosniff");

    // 2. Chunked Transfer-Encoding (no Content-Length) > 2 MiB
    const chunkedRes = await new Promise((resolve, reject) => {
      const req = http.request(
        {
          hostname: webUrl.hostname,
          port: webUrl.port || (webUrl.protocol === "https:" ? 443 : 80),
          path: "/api/gw/cart/items",
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Origin: config.webOrigin,
            "Transfer-Encoding": "chunked",
          },
        },
        (res) => {
          let body = "";
          res.on("data", (d) => {
            body += d.toString("utf8");
          });
          res.on("end", () => {
            resolve({ status: res.statusCode, headers: res.headers, body });
          });
        }
      );
      req.on("error", (err) => {
        // EPIPE / ECONNRESET can happen if server closes socket immediately upon returning 413
        if (err && (err.code === "EPIPE" || err.code === "ECONNRESET")) return;
        reject(err);
      });
      const piece = Buffer.alloc(512 * 1024, 0x61); // 512 KiB
      req.write('{"pad":"');
      for (let i = 0; i < 5; i++) {
        req.write(piece);
      }
      req.end('"}');
    });

    assert.equal(
      chunkedRes.status,
      413,
      `BFF chunked POST > 2 MiB expected 413, got ${chunkedRes.status}: ${chunkedRes.body}`
    );
    assert.equal(chunkedRes.headers["x-content-type-options"], "nosniff");
  });

  test("6f. Real Chromium opens sandboxed BFF shipping label without CSP style violations and prints to PDF via CDP Page.printToPDF", async () => {
    await ensureRateBudget(25, { label: "6f" });
    const { client: seller } = await loginSeededAccount(SELLER_EMAIL, SELLER_PASSWORD, "seller");
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "label-6f");

    // Create a self-contained COD order + shipment so 6f does not depend on 6a state.
    const { quote, order } = await createSellerShopOrder(seller, buyerClient, buyer, {
      paymentMethod: "COD",
      tag: "label-6f",
    });

    let browser = null;
    try {
      await pollUntil(
        async () => {
          const res = await buyerClient.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
          const st = normalizeStatus(res.data?.order?.status);
          return { done: st === "CONFIRMED", detail: `order status=${st}` };
        },
        { label: "6f COD order CONFIRMED" }
      );

      const shipRes = await seller.json(
        "POST",
        `/api/gw/seller/orders/${encodeURIComponent(order.id)}/ship`,
        {
          body: {
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        }
      );
      assert.equal(shipRes.status, 200, `6f seller ship expected 200, got ${shipRes.status}: ${shipRes.text}`);
      const trackingToInspect = shipRes.data?.tracking_code || shipRes.data?.order?.tracking_code;
      assert.ok(trackingToInspect, `6f seller ship missing tracking_code: ${shipRes.text}`);

      browser = await HeadlessBrowser.launch();
      await browser.setViewport(1280, 900, false);
      await browser.setCookiesFromClient(seller);

      const cspErrors = [];
      browser.on("Log.entryAdded", (params) => {
        const text = String(params?.entry?.text ?? "");
        if (/content security policy|refused to apply inline style|csp/i.test(text)) {
          cspErrors.push(`Log: ${text}`);
        }
      });
      browser.on("Runtime.consoleAPICalled", (params) => {
        const text = (params?.args ?? [])
          .map((a) => String(a?.value ?? a?.description ?? ""))
          .join(" ");
        if (/content security policy|refused to apply inline style|csp/i.test(text)) {
          cspErrors.push(`Console: ${text}`);
        }
      });

      const labelPath = `/api/gw/shipping/labels/${encodeURIComponent(trackingToInspect)}`;
      await browser.goto(labelPath);

      const inspected = await browser.eval(`(() => {
        const card = document.querySelector(".card");
        const style = card ? getComputedStyle(card) : null;
        return {
          title: document.title,
          bodyText: document.body?.innerText || "",
          hasCard: Boolean(card),
          borderTopWidth: style ? style.borderTopWidth : "",
          paddingTop: style ? style.paddingTop : "",
        };
      })()`);

      assert.equal(inspected.hasCard, true, "shipping label must render .card container");
      assert.ok(
        inspected.bodyText.includes("NexusMart") &&
          inspected.bodyText.includes("Phiếu vận đơn") &&
          inspected.bodyText.includes(trackingToInspect) &&
          inspected.bodyText.includes("Ctrl+P"),
        `shipping label missing expected content, got: ${inspected.bodyText.slice(0, 400)}`
      );
      assert.equal(
        inspected.borderTopWidth,
        "2px",
        `inline <style> must not be blocked by CSP (expected .card borderTopWidth=2px, got ${inspected.borderTopWidth})`
      );
      assert.equal(
        inspected.paddingTop,
        "20px",
        `inline <style> must apply .card padding=20px, got ${inspected.paddingTop}`
      );
      assert.deepEqual(
        cspErrors,
        [],
        `expected zero CSP violations on shipping label, got: ${cspErrors.join("; ")}`
      );

      // Verify printing via Chromium CDP Page.printToPDF succeeds despite sandbox CSP
      const pdfRes = await browser.send("Page.printToPDF", {
        printBackground: true,
        preferCSSPageSize: false,
      });
      assert.ok(pdfRes?.data && typeof pdfRes.data === "string", "Page.printToPDF must return base64 data");
      const pdfBuf = Buffer.from(pdfRes.data, "base64");
      assert.equal(
        pdfBuf.subarray(0, 5).toString("ascii"),
        "%PDF-",
        "Page.printToPDF output must start with valid %PDF- header"
      );
      assert.ok(
        pdfBuf.byteLength > 1000,
        `expected non-trivial PDF output (> 1000 bytes), got ${pdfBuf.byteLength} bytes`
      );
    } finally {
      if (browser) {
        await browser.close();
      }
      await cancelOrderBestEffort(buyerClient, order.id, "e2e 6f cleanup");
    }
  });
});
