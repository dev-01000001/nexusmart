import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, test } from "node:test";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  WebClient,
  config,
  createBuyerAddress,
  ensureRateBudget,
  pickSingleShopSku,
  pickTwoShopSkus,
  registerAndLoginBuyer,
} from "./helpers.mjs";

// Dev-only seeded credentials (documented in tools/seed/seed.mjs).
const SELLER_EMAIL = process.env.E2E_SELLER_EMAIL || "seller.bachdang@nexusmart.local";
const SELLER_PASSWORD = process.env.E2E_SELLER_PASSWORD || "SellerDevPassword123!";
const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";

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

const BROWSER_RATE_LIMIT_DEFAULT_WAIT_MS = 60_000;
const BROWSER_RATE_LIMIT_MAX_TOTAL_MS = 150_000;
const BROWSER_RATE_LIMIT_TEXT = "Bạn thao tác quá nhanh";

function parseBrowserRetryAfterMs(headerVal) {
  const v = String(headerVal ?? "").trim();
  if (!v) return null;
  if (/^-?\d+$/.test(v)) {
    const sec = Number.parseInt(v, 10);
    return sec > 0 ? sec * 1000 : null;
  }
  const ts = Date.parse(v);
  if (!Number.isNaN(ts)) {
    const delta = ts - Date.now();
    if (delta > 0) return delta;
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
      const retryAfterMs = parseBrowserRetryAfterMs(retryRaw);
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
    assert.ok(chromeBin, "Headless Chrome binary not found for browser E2E checks");

    const userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), "nm-e2e-chrome-"));
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
    assert.ok(pageTarget?.webSocketDebuggerUrl, "Could not find Chrome page DevTools target");

    const ws = new WebSocket(pageTarget.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("Timed out connecting to CDP WebSocket")), 10_000);
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
    await this.send("Network.clearBrowserCookies");
  }

  async goto(reqPath) {
    const url = reqPath.startsWith("http")
      ? reqPath
      : `${config.webUrl}${reqPath.startsWith("/") ? reqPath : `/${reqPath}`}`;
    this.lastRateLimit = null;
    await this.send("Page.navigate", { url });
    await this.waitFor(`document.readyState === "complete"`, 15_000, { reloadOnRateLimit: false });
  }

  async reload() {
    this.lastRateLimit = null;
    const loaded = new Promise((resolve) => {
      const listener = () => {
        clearTimeout(timer);
        off();
        resolve();
      };
      const off = () => {
        const list = this.eventListeners.get("Page.loadEventFired");
        if (!list) return;
        const idx = list.indexOf(listener);
        if (idx !== -1) list.splice(idx, 1);
      };
      const timer = setTimeout(() => {
        off();
        resolve();
      }, 15_000);
      this.on("Page.loadEventFired", listener);
    });
    await this.send("Page.reload", { ignoreCache: true });
    await loaded;
    await this.waitFor(`document.readyState === "complete"`, 15_000, { reloadOnRateLimit: false });
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

  async waitFor(condition, timeoutMs = 15_000, { reloadOnRateLimit = true } = {}) {
    const startMs = Date.now();
    const maxTotalMs = Math.max(timeoutMs, BROWSER_RATE_LIMIT_MAX_TOTAL_MS);
    const label = typeof condition === "function" ? condition.toString() : condition;
    const check =
      typeof condition === "function"
        ? () => Promise.resolve(Boolean(condition()))
        : () => this.eval(`Boolean(${condition})`);
    let attemptStart = startMs;
    let attemptDeadline = startMs + timeoutMs;

    while (true) {
      let rateLimited = false;
      let checkedTextAt = 0;

      while (Date.now() < attemptDeadline) {
        const val = await check();
        if (val) return true;

        if (reloadOnRateLimit && Date.now() - attemptStart >= 2_000) {
          if (this.lastRateLimit) {
            rateLimited = true;
            break;
          }
          if (Date.now() - checkedTextAt >= 500) {
            checkedTextAt = Date.now();
            const hasText = await this.eval(
              `Boolean(document.body?.innerText?.includes(${JSON.stringify(BROWSER_RATE_LIMIT_TEXT)}))`
            ).catch(() => false);
            if (hasText) {
              rateLimited = true;
              break;
            }
          }
        }
        await new Promise((r) => setTimeout(r, 120));
      }

      if (!reloadOnRateLimit) break;

      if (!rateLimited) {
        const hasText = await this.eval(
          `Boolean(document.body?.innerText?.includes(${JSON.stringify(BROWSER_RATE_LIMIT_TEXT)}))`
        ).catch(() => false);
        rateLimited = Boolean(this.lastRateLimit) || hasText;
      }
      if (!rateLimited) break;

      const rateLimitHit = this.lastRateLimit;
      const waitMs = rateLimitHit?.retryAfterMs ?? BROWSER_RATE_LIMIT_DEFAULT_WAIT_MS;
      const elapsedMs = Date.now() - startMs;
      if (elapsedMs + waitMs > maxTotalMs) break;

      const loc = await this.eval("window.location.pathname").catch(() => "unknown");
      const reason = rateLimitHit ? "HTTP 429" : `text "${BROWSER_RATE_LIMIT_TEXT}"`;
      console.log(
        `[429] browser ${loc} rate limited (${reason}); waiting ${Math.ceil(waitMs / 1000)}s before reload (elapsed ${Math.ceil((elapsedMs + waitMs) / 1000)}s/${Math.ceil(maxTotalMs / 1000)}s)`
      );
      await new Promise((r) => setTimeout(r, waitMs));
      await this.reload();
      attemptStart = Date.now();
      attemptDeadline = Math.min(attemptStart + timeoutMs, startMs + maxTotalMs);
    }

    const href = await this.eval("window.location.href").catch(() => "unknown");
    throw new Error(`Timed out after ${Date.now() - startMs}ms waiting for (${label}) at ${href}`);
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

async function submitLoginFormInBrowser(browser, { email, password, query = "" }) {
  await ensureRateBudget(15, { label: "5c-login" });
  await browser.clearCookies();
  await browser.goto(`/dang-nhap${query}`);
  await browser.waitFor(`Boolean(document.querySelector('#login-email'))`);

  const setOk = await browser.eval(`(() => {
    const emailEl = document.querySelector('#login-email');
    const passEl = document.querySelector('#login-password');
    const btn = document.querySelector('form button[type="submit"]');
    if (!emailEl || !passEl || !btn) return false;
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    setter.call(emailEl, ${JSON.stringify(email)});
    emailEl.dispatchEvent(new Event('input', { bubbles: true }));
    setter.call(passEl, ${JSON.stringify(password)});
    passEl.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`);
  assert.equal(setOk, true, "login form inputs must be present");

  await browser.eval(`document.querySelector('form button[type="submit"]').click()`);
  await browser.waitFor(`window.location.pathname !== "/dang-nhap"`, 15_000);
  return browser.eval(`window.location.pathname + window.location.search`);
}

async function fetchPageWithRateLimitRetry(client, reqPath) {
  const startMs = Date.now();
  while (true) {
    const res = await client.page(reqPath);
    if (res.status !== 200 || !res.text.includes(BROWSER_RATE_LIMIT_TEXT)) {
      return res;
    }
    const elapsedMs = Date.now() - startMs;
    const waitMs = 15_000;
    if (elapsedMs + waitMs > BROWSER_RATE_LIMIT_MAX_TOTAL_MS) {
      return res;
    }
    console.log(
      `[429] SSR ${reqPath} rendered "${BROWSER_RATE_LIMIT_TEXT}"; waiting ${Math.ceil(waitMs / 1000)}s before retry`
    );
    await new Promise((r) => setTimeout(r, waitMs));
  }
}

describe("05. Wave 1 business audit fixes (WEB-B1..B9, CAT-B5, PAY-B10, SHP-B2)", () => {
  test("5a. [WEB-B2] /flash-sale keeps real Flash sale and removes Deal sốc & Combo sections", async () => {
    const anon = new WebClient();
    const res = await anon.page("/flash-sale");
    assert.equal(res.status, 200, `GET /flash-sale expected 200, got ${res.status}`);

    assert.ok(
      res.text.includes("flash-slot-heading") || res.text.includes("Flash sale"),
      "/flash-sale must keep real Flash sale section"
    );
    assert.equal(
      /deal\s*sốc/i.test(res.text),
      false,
      "/flash-sale HTML must not contain 'Deal sốc'"
    );
    assert.equal(
      res.text.includes("Combo &amp; Mua nhiều giảm thêm") ||
        res.text.includes("Combo & Mua nhiều giảm thêm") ||
        res.text.includes("Combo tiết kiệm") ||
        res.text.includes("combo-offers-heading") ||
        res.text.includes("shock-deals-heading"),
      false,
      "/flash-sale HTML must not contain Combo or Shock Deal blocks"
    );
  });

  test("5b. [WEB-B3] Product pages remove Mua 2 giảm, Quà 499k, Chọn size combo, and mua kèm bundle promises", async () => {
    const anon = new WebClient();
    for (const slug of ["ao-thun-cotton-compact-trang", "combo-3-quan-lot-boxer-modal"]) {
      const res = await anon.page(`/san-pham/${slug}`);
      assert.equal(res.status, 200, `GET /san-pham/${slug} expected 200, got ${res.status}`);

      assert.equal(
        /mua\s*2\s*giảm/i.test(res.text),
        false,
        `/san-pham/${slug} must not contain 'Mua 2 giảm'`
      );
      assert.equal(
        /mua nhiều giảm thêm/i.test(res.text),
        false,
        `/san-pham/${slug} must not contain 'Mua nhiều giảm thêm'`
      );
      assert.equal(
        /quà tặng ngưỡng đơn/i.test(res.text),
        false,
        `/san-pham/${slug} must not contain fake 499k gift threshold`
      );
      assert.equal(
        /chọn cỡ từng món trong combo|chọn size combo/i.test(res.text),
        false,
        `/san-pham/${slug} must not contain combo per-item size selector`
      );
      assert.equal(
        /pdp-bundle-heading|gợi ý mua kèm/i.test(res.text),
        false,
        `/san-pham/${slug} must not contain out-of-scope mua kèm bundle section`
      );
      assert.equal(
        /deal\s*sốc/i.test(res.text),
        false,
        `/san-pham/${slug} must not contain 'Deal sốc'`
      );
    }
  });

  test("5c. [WEB-B4] Login redirects by backend role: seller -> /seller, admin -> /admin, buyer -> / or ?next=", async () => {
    await ensureRateBudget(35, { label: "5c" });
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "login-role");

    const browser = await HeadlessBrowser.launch();
    try {
      const sellerDest = await submitLoginFormInBrowser(browser, {
        email: SELLER_EMAIL,
        password: SELLER_PASSWORD,
      });
      assert.equal(sellerDest, "/seller", `seller login without ?next= must redirect to /seller, got ${sellerDest}`);

      const adminDest = await submitLoginFormInBrowser(browser, {
        email: ADMIN_EMAIL,
        password: ADMIN_PASSWORD,
      });
      assert.equal(adminDest, "/admin", `admin login without ?next= must redirect to /admin, got ${adminDest}`);

      const buyerDest = await submitLoginFormInBrowser(browser, {
        email: buyer.email,
        password: buyer.password,
      });
      assert.equal(buyerDest, "/", `buyer login without ?next= must redirect to /, got ${buyerDest}`);

      const buyerNextDest = await submitLoginFormInBrowser(browser, {
        email: buyer.email,
        password: buyer.password,
        query: "?next=%2Ftai-khoan",
      });
      assert.equal(
        buyerNextDest,
        "/tai-khoan",
        `buyer login with ?next=/tai-khoan must redirect to /tai-khoan, got ${buyerNextDest}`
      );
    } finally {
      await browser.close();
    }
  });

  test("5d. [WEB-B9] Flash sale Buy Now displays live flash price matching backend and ignores upcoming flash IDs", async () => {
    await ensureRateBudget(20, { label: "5d" });
    const client = new WebClient();
    await registerAndLoginBuyer(client, "flash-buynow");

    const fsRes = await client.json("GET", "/api/gw/flash-sales?include_upcoming=true");
    assert.equal(fsRes.status, 200);
    const sales = fsRes.data?.flash_sales ?? [];
    const nowMs = Date.now();

    let liveItem = null;
    let upcomingItem = null;
    for (const fs of sales) {
      const startMs = fs.starts_at ? Date.parse(fs.starts_at) : 0;
      const endMs = fs.ends_at ? Date.parse(fs.ends_at) : 0;
      const items = (fs.items ?? []).filter(
        (it) => (!it.status || it.status === "APPROVED") && it.remaining_slots > 0
      );
      if (nowMs >= startMs && nowMs <= endMs && !liveItem && items.length > 0) {
        liveItem = items[0];
      } else if (startMs > nowMs && !upcomingItem && items.length > 0) {
        upcomingItem = items[0];
      }
    }

    assert.ok(liveItem, "expected at least 1 live flash sale item on stack");
    const liveUrl = `/thanh-toan?sku=${encodeURIComponent(liveItem.sku_id)}&qty=1&flash=${encodeURIComponent(liveItem.id)}`;
    const livePage = await fetchPageWithRateLimitRetry(client, liveUrl);
    assert.equal(livePage.status, 200, `GET ${liveUrl} expected 200, got ${livePage.status}`);

    // Format integer VND matching formatVnd (e.g. 149000 -> 149.000₫)
    const fmtVnd = (n) => `${Math.trunc(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ".")}₫`;
    assert.ok(
      livePage.text.includes("Ưu đãi Flash sale"),
      "live flash buy-now SSR HTML must show 'Ưu đãi Flash sale' discount line"
    );
    assert.ok(
      livePage.text.includes(fmtVnd(liveItem.flash_price_vnd)),
      `live flash buy-now SSR HTML must show flash price ${fmtVnd(liveItem.flash_price_vnd)}`
    );

    if (upcomingItem) {
      const upUrl = `/thanh-toan?sku=${encodeURIComponent(upcomingItem.sku_id)}&qty=1&flash=${encodeURIComponent(upcomingItem.id)}`;
      const upPage = await fetchPageWithRateLimitRetry(client, upUrl);
      assert.equal(upPage.status, 200);
      assert.equal(
        upPage.text.includes("Ưu đãi Flash sale"),
        false,
        "upcoming flash sale ID must not display active flash discount in Buy Now"
      );
    }
  });

  test("5e. [R7 / WEB-B1] /gio-hang and /thanh-toan omit from_province_code/from_ward_code in calculate-fee requests", async () => {
    await ensureRateBudget(30, { label: "5e" });
    const client = new WebClient();
    const buyer = await registerAndLoginBuyer(client, "ship-payload");
    await createBuyerAddress(client, buyer);
    const picked = await pickSingleShopSku(client, { minStock: 2 });

    const addRes = await client.json("POST", "/api/gw/cart/items", {
      body: { sku_id: picked.sku.id, quantity: 1 },
    });
    assert.equal(addRes.status, 200, `add cart item failed: ${addRes.text}`);

    const browser = await HeadlessBrowser.launch();
    try {
      await browser.setCookiesFromClient(client);
      const feeRequests = [];
      browser.on("Network.requestWillBeSent", (params) => {
        if (
          params.request?.method === "POST" &&
          String(params.request.url).includes("/api/gw/shipping/calculate-fee")
        ) {
          try {
            feeRequests.push({
              url: params.documentURL || "",
              body: JSON.parse(params.request.postData || "{}"),
            });
          } catch {
            feeRequests.push({ url: params.documentURL || "", body: { raw: params.request.postData } });
          }
        }
      });

      // Check /gio-hang calculate-fee payload
      await browser.goto("/gio-hang");
      await browser.waitFor(() => feeRequests.length > 0, 12_000);
      assert.ok(feeRequests.length > 0, "expected /gio-hang to call POST /api/gw/shipping/calculate-fee");
      const cartBody = feeRequests[0].body;
      assert.equal(cartBody.shop_id, picked.product.shop_id, "/gio-hang calculate-fee must include shop_id");
      assert.equal(
        "from_province_code" in cartBody,
        false,
        `/gio-hang calculate-fee must not send from_province_code, got ${JSON.stringify(cartBody)}`
      );
      assert.equal(
        "from_ward_code" in cartBody,
        false,
        `/gio-hang calculate-fee must not send from_ward_code, got ${JSON.stringify(cartBody)}`
      );

      // Check /thanh-toan calculate-fee payload
      const beforeCheckoutCount = feeRequests.length;
      await browser.goto(`/thanh-toan?sku=${encodeURIComponent(picked.sku.id)}&qty=1`);
      await browser.waitFor(() => feeRequests.length > beforeCheckoutCount, 12_000);
      assert.ok(
        feeRequests.length > beforeCheckoutCount,
        "expected /thanh-toan to call POST /api/gw/shipping/calculate-fee"
      );
      const checkoutBody = feeRequests[beforeCheckoutCount].body;
      assert.equal(
        checkoutBody.shop_id,
        picked.product.shop_id,
        "/thanh-toan calculate-fee must include shop_id"
      );
      assert.equal(
        "from_province_code" in checkoutBody,
        false,
        `/thanh-toan calculate-fee must not send from_province_code, got ${JSON.stringify(checkoutBody)}`
      );
      assert.equal(
        "from_ward_code" in checkoutBody,
        false,
        `/thanh-toan calculate-fee must not send from_ward_code, got ${JSON.stringify(checkoutBody)}`
      );
    } finally {
      await browser.close();
    }
  });

  test("5f. [CAT-B5 / WEB-B6] Seller edit form for PENDING product does not allow selecting HIDDEN", async () => {
    await ensureRateBudget(25, { label: "5f" });
    const sellerClient = new WebClient();
    const loginRes = await sellerClient.json("POST", "/api/auth/login", {
      body: { email: SELLER_EMAIL, password: SELLER_PASSWORD },
    });
    assert.equal(loginRes.status, 200, `seller login failed: ${loginRes.text}`);

    const catRes = await sellerClient.json("GET", "/api/gw/categories");
    assert.equal(catRes.status, 200);
    const categoryId = catRes.data?.categories?.[0]?.id;
    assert.ok(categoryId, "expected at least one category");

    const tag = crypto.randomBytes(4).toString("hex");
    const createRes = await sellerClient.json("POST", "/api/gw/seller/products", {
      body: {
        category_id: categoryId,
        name: `E2E Web Audit Pending ${tag}`,
        description: "Temporary test product for CAT-B5",
        images: ["/products/ao-thun-cotton-trang.png"],
        skus: [
          {
            sku_code: `E2E-WEB-${tag.toUpperCase()}`,
            name: "Trắng / M",
            attributes: { color: "Trắng", size: "M" },
            price_vnd: 199000,
            stock: 5,
            weight_grams: 250,
            image_url: "/products/ao-thun-cotton-trang.png",
          },
        ],
      },
    });
    assert.ok(
      createRes.status === 200 || createRes.status === 201,
      `create PENDING product failed (${createRes.status}): ${createRes.text}`
    );
    const productId = createRes.data?.product?.id;
    assert.ok(productId, "created product must have id");

    const browser = await HeadlessBrowser.launch();
    try {
      await browser.setCookiesFromClient(sellerClient);
      await browser.goto(`/seller/san-pham/${encodeURIComponent(productId)}`);
      await browser.waitFor(`Boolean(document.querySelector('#prod-status'))`, 15_000);

      const options = await browser.eval(`Array.from(document.querySelectorAll('#prod-status option')).map(o => ({ value: o.value, text: o.textContent.trim() }))`);
      assert.ok(
        !options.some((o) => o.value === "HIDDEN"),
        `PENDING product status dropdown must not include HIDDEN, got ${JSON.stringify(options)}`
      );
      assert.deepEqual(
        options.map((o) => o.value),
        ["PENDING"],
        `PENDING product status dropdown should only allow PENDING, got ${JSON.stringify(options)}`
      );
    } finally {
      await browser.close();
      await sellerClient.json("DELETE", `/api/gw/seller/products/${encodeURIComponent(productId)}`);
    }
  });

  test("5g. [SHP-B2] Seller ship form defaults to the order's chosen carrier_code/service_code (mock_express/STANDARD)", async () => {
    await ensureRateBudget(35, { label: "5g" });
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "shp-b2-ui");
    const { address } = await createBuyerAddress(buyerClient, buyer);

    const sellerClient = new WebClient();
    const sellerLogin = await sellerClient.json("POST", "/api/auth/login", {
      body: { email: SELLER_EMAIL, password: SELLER_PASSWORD },
    });
    assert.equal(sellerLogin.status, 200);
    const sellerShopId = sellerLogin.data?.user?.shop_id;
    assert.ok(sellerShopId, "seller must have shop_id");

    // Pick an in-stock SKU belonging to seller.bachdang's shop.
    const prodRes = await buyerClient.json(
      "GET",
      `/api/gw/products?shop_id=${encodeURIComponent(sellerShopId)}&page=1&page_size=20`
    );
    assert.equal(prodRes.status, 200);
    let pickedSku = null;
    for (const p of prodRes.data?.items ?? []) {
      const sku = (p.skus ?? []).find((s) => s.is_active !== false && (s.available_stock ?? 0) >= 2);
      if (sku) {
        pickedSku = sku;
        break;
      }
    }
    assert.ok(pickedSku, "expected active in-stock SKU for seller.bachdang");

    // Checkout COD explicitly choosing mock_express / STANDARD (not ghn / STANDARD).
    const checkoutRes = await buyerClient.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-shpb2-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [{ sku_id: pickedSku.id, quantity: 1 }],
        payment_method: "COD",
        shipping_choices: [
          {
            shop_id: sellerShopId,
            carrier_code: "mock_express",
            service_code: "STANDARD",
          },
        ],
      },
    });
    assert.ok(
      checkoutRes.status === 200 || checkoutRes.status === 201,
      `checkout failed (${checkoutRes.status}): ${checkoutRes.text}`
    );
    const order = checkoutRes.data?.orders?.[0];
    assert.ok(order?.id, "expected created order");
    assert.equal(order.carrier_code, "mock_express");
    assert.equal(order.service_code, "STANDARD");

    const browser = await HeadlessBrowser.launch();
    try {
      await browser.setCookiesFromClient(sellerClient);
      await browser.goto(`/seller/don-hang/${encodeURIComponent(order.id)}`);
      await browser.waitFor(
        `Array.from(document.querySelectorAll('button')).some(b => b.textContent.includes('Tạo vận đơn'))`,
        15_000
      );
      await browser.eval(`(() => {
        const btn = Array.from(document.querySelectorAll('button')).find(b => b.textContent.includes('Tạo vận đơn'));
        btn.click();
      })()`);
      await browser.waitFor(`Boolean(document.querySelector('#ship-carrier'))`, 10_000);

      const defaults = await browser.eval(`({
        carrier: document.querySelector('#ship-carrier')?.value,
        service: document.querySelector('#ship-service')?.value
      })`);
      assert.equal(
        defaults.carrier,
        "mock_express",
        `seller detail ship form must default to order carrier mock_express, got ${JSON.stringify(defaults)}`
      );
      assert.equal(
        defaults.service,
        "STANDARD",
        `seller detail ship form must default to order service STANDARD, got ${JSON.stringify(defaults)}`
      );

      // Also verify /seller/don-hang list page inline ship drawer defaults to mock_express/STANDARD.
      await browser.goto("/seller/don-hang");
      await browser.waitFor(
        `document.body.innerText.includes(${JSON.stringify(order.order_code)})`,
        15_000
      );
      const opened = await browser.eval(`(() => {
        const rows = Array.from(document.querySelectorAll('tbody tr'));
        const row = rows.find(r => r.textContent && r.textContent.includes(${JSON.stringify(order.order_code)}));
        if (!row) return false;
        const btn = Array.from(row.querySelectorAll('button')).find(b => b.textContent.trim() === 'Tạo vận đơn');
        if (!btn) return false;
        btn.click();
        return true;
      })()`);
      assert.equal(opened, true, "expected to open inline ship drawer on /seller/don-hang");
      await browser.waitFor(
        `Boolean(document.querySelector('select[aria-label="Chọn đơn vị vận chuyển"]'))`,
        10_000
      );
      const listDefaults = await browser.eval(`({
        carrier: document.querySelector('select[aria-label="Chọn đơn vị vận chuyển"]')?.value,
        service: document.querySelector('select[aria-label="Chọn gói dịch vụ vận chuyển"]')?.value
      })`);
      assert.equal(
        listDefaults.carrier,
        "mock_express",
        `seller list ship drawer must default to mock_express, got ${JSON.stringify(listDefaults)}`
      );
      assert.equal(
        listDefaults.service,
        "STANDARD",
        `seller list ship drawer must default to STANDARD, got ${JSON.stringify(listDefaults)}`
      );
    } finally {
      await browser.close();
      // Cancel temporary test order to keep seller's queue clean.
      await buyerClient.json("POST", `/api/gw/orders/${encodeURIComponent(order.id)}/cancel`, {
        body: { reason: "Clean up E2E SHP-B2 test order" },
      });
    }
  });

  test("5h. [PAY-B10 / ORD-B11] Payment waiting page copy does not instruct retrying expired VietQR", async () => {
    await ensureRateBudget(20, { label: "5h" });
    const client = new WebClient();
    const buyer = await registerAndLoginBuyer(client, "pay-b10");
    const { address } = await createBuyerAddress(client, buyer);
    const picked = await pickSingleShopSku(client, { minStock: 2 });

    const checkoutRes = await client.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-payb10-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [{ sku_id: picked.sku.id, quantity: 1 }],
        payment_method: "VIETQR",
        shipping_choices: [
          {
            shop_id: picked.product.shop_id,
            carrier_code: "ghn",
            service_code: "STANDARD",
          },
        ],
      },
    });
    assert.ok(checkoutRes.status === 200 || checkoutRes.status === 201);
    const paymentId = checkoutRes.data?.payment_id;
    const orderId = checkoutRes.data?.orders?.[0]?.id;
    assert.ok(paymentId, "expected VietQR payment_id");

    try {
      const pageRes = await client.page(`/thanh-toan/cho-thanh-toan/${encodeURIComponent(paymentId)}`);
      assert.equal(pageRes.status, 200);
      assert.equal(
        /hết hạn[\s\S]{0,80}tạo lại mã/i.test(pageRes.text),
        false,
        "Payment waiting page must not suggest retrying QR after expiry"
      );
    } finally {
      if (orderId) {
        await client.json("POST", `/api/gw/orders/${encodeURIComponent(orderId)}/cancel`, {
          body: { reason: "Clean up E2E PAY-B10 test order" },
        });
      }
    }
  });

  test("5i. [review-C P1-2] Shop registration enforces required fields & VN phone and supports REQUEST_INFO resubmission", async () => {
    await ensureRateBudget(45, { label: "5i" });
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "shop-reg");
    const adminClient = new WebClient();
    const adminLogin = await adminClient.json("POST", "/api/auth/login", {
      body: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    assert.equal(adminLogin.status, 200, `admin login failed: ${adminLogin.text}`);

    const tag = crypto.randomBytes(4).toString("hex");
    const shopName = `Xưởng Dệt Audit ${tag}`;
    const validPhone = `09${String(Math.floor(10000000 + Math.random() * 89999999))}`;
    const requestInfoReason = `Vui lòng bổ sung mã GPKD bản mới ${tag}`;

    const browser = await HeadlessBrowser.launch();
    try {
      await browser.setCookiesFromClient(buyerClient);

      // Step 1: Without any pickup address, form must require address and license (no optional copy)
      await browser.goto("/tai-khoan?tab=gian-hang");
      await browser.waitFor(`Boolean(document.querySelector('#shop-name'))`, 15_000);

      const noAddrState = await browser.eval(`({
        bodyText: document.body.innerText,
        licenseRequired: Boolean(document.querySelector('#shop-license')?.required),
        licenseLabel: document.querySelector('label[for="shop-license"]')?.textContent?.trim() || '',
        hasEmptyPickupOption: Array.from(document.querySelectorAll('#shop-pickup-addr option')).some(o => o.value === ''),
      })`);
      assert.equal(noAddrState.licenseRequired, true, "#shop-license must be required");
      assert.equal(
        noAddrState.licenseLabel.includes("không bắt buộc"),
        false,
        `#shop-license label must not say '(không bắt buộc)', got: ${noAddrState.licenseLabel}`
      );
      assert.equal(
        noAddrState.bodyText.includes("Bạn vẫn có thể gửi hồ sơ"),
        false,
        "Shop tab must not tell buyers without an address that they can still submit"
      );
      assert.equal(
        noAddrState.hasEmptyPickupOption,
        false,
        "Shop pickup select must not offer an empty 'Chọn sau' option"
      );

      // Step 2: Create a pickup address, reload, verify client validation rejects invalid VN phone 0123456789
      await createBuyerAddress(buyerClient, buyer);
      await browser.goto("/tai-khoan?tab=gian-hang");
      await browser.waitFor(`Boolean(document.querySelector('#shop-pickup-addr'))`, 15_000);

      const pickupOpts = await browser.eval(
        `Array.from(document.querySelectorAll('#shop-pickup-addr option')).map(o => ({ value: o.value, text: o.textContent.trim() }))`
      );
      assert.ok(pickupOpts.length >= 1, "expected pickup address option after creating address");
      assert.equal(
        pickupOpts.some((o) => o.value === "" || o.text === "Chọn sau"),
        false,
        `pickup select must not have empty/Chọn sau option, got ${JSON.stringify(pickupOpts)}`
      );

      // Fill invalid VN phone 0123456789 -> client validation must block submission
      await browser.eval(`(() => {
        const setVal = (sel, val) => {
          const el = document.querySelector(sel);
          const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
          Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, val);
          el.dispatchEvent(new Event('input', { bubbles: true }));
        };
        setVal('#shop-name', ${JSON.stringify(shopName)});
        setVal('#shop-phone', '0123456789');
        setVal('#shop-license', ${JSON.stringify(`GPKD-${tag}`)});
        setVal('#shop-desc', 'Xưởng may đồng phục cotton');
        document.querySelector('form button[type="submit"]').click();
      })()`);
      await browser.waitFor(`Boolean(document.querySelector('[role="alert"]'))`, 8_000);
      const invalidPhoneAlert = await browser.eval(
        `document.querySelector('[role="alert"]')?.textContent?.trim() || ''`
      );
      assert.ok(
        /số điện thoại/i.test(invalidPhoneAlert),
        `expected VN phone validation alert, got: ${invalidPhoneAlert}`
      );

      // Step 3: Fix phone to valid VN number and submit initial registration
      await browser.eval(`(() => {
        const el = document.querySelector('#shop-phone');
        Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(el, ${JSON.stringify(validPhone)});
        el.dispatchEvent(new Event('input', { bubbles: true }));
        document.querySelector('form button[type="submit"]').click();
      })()`);
      await browser.waitFor(
        `document.body.innerText.includes('Đang chờ duyệt') && !document.querySelector('#shop-name')`,
        15_000
      );

      // Step 4: Admin requests additional info (REQUEST_INFO -> PENDING + rejection_reason)
      const meShopRes = await buyerClient.json("GET", "/api/gw/shops/me");
      assert.equal(meShopRes.status, 200, `GET /shops/me failed: ${meShopRes.text}`);
      const shopId = meShopRes.data?.shop?.id;
      assert.ok(shopId, "expected created shop id");

      const reviewRes = await adminClient.json(
        "POST",
        `/api/gw/admin/shops/${encodeURIComponent(shopId)}/review`,
        {
          body: { decision: "REQUEST_INFO", reason: requestInfoReason },
        }
      );
      assert.equal(reviewRes.status, 200, `admin REQUEST_INFO failed: ${reviewRes.text}`);
      assert.equal(reviewRes.data?.shop?.status, "PENDING");
      assert.equal(reviewRes.data?.shop?.rejection_reason, requestInfoReason);

      // Step 5: Buyer reloads /tai-khoan?tab=gian-hang -> sees "Yêu cầu bổ sung" + resubmission form
      await browser.goto("/tai-khoan?tab=gian-hang");
      await browser.waitFor(`Boolean(document.querySelector('#shop-name'))`, 15_000);

      const reqInfoState = await browser.eval(`({
        bodyText: document.body.innerText,
        prefilledName: document.querySelector('#shop-name')?.value || '',
        prefilledLicense: document.querySelector('#shop-license')?.value || '',
      })`);
      assert.ok(
        reqInfoState.bodyText.includes("Yêu cầu bổ sung"),
        `REQUEST_INFO shop must display 'Yêu cầu bổ sung', got: ${reqInfoState.bodyText.slice(0, 600)}`
      );
      assert.ok(
        reqInfoState.bodyText.includes(requestInfoReason),
        "REQUEST_INFO shop must display admin's request reason"
      );
      assert.equal(
        reqInfoState.bodyText.includes("Lý do từ chối:"),
        false,
        "REQUEST_INFO shop must not mislabel reason as 'Lý do từ chối:'"
      );
      assert.equal(reqInfoState.prefilledName, shopName, "resubmission form should pre-fill shop name");
      assert.equal(
        reqInfoState.prefilledLicense,
        `GPKD-${tag}`,
        "resubmission form should pre-fill business license"
      );

      // Step 6: Update license doc and resubmit -> returns to plain PENDING without form
      await browser.eval(`(() => {
        const el = document.querySelector('#shop-license');
        Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(el, ${JSON.stringify(`GPKD-${tag}-V2`)});
        el.dispatchEvent(new Event('input', { bubbles: true }));
        document.querySelector('form button[type="submit"]').click();
      })()`);
      await browser.waitFor(
        `document.body.innerText.includes('Đang chờ duyệt') && !document.querySelector('#shop-name')`,
        15_000
      );

      const afterResubmit = await buyerClient.json("GET", "/api/gw/shops/me");
      assert.equal(afterResubmit.status, 200);
      assert.equal(afterResubmit.data?.shop?.status, "PENDING");
      assert.equal(afterResubmit.data?.shop?.rejection_reason || "", "");
      assert.equal(afterResubmit.data?.shop?.business_license_doc, `GPKD-${tag}-V2`);
    } finally {
      await browser.close();
    }
  });

  test("5j. [Round 3] Cancelling unpaid multi-shop VietQR order confirms full-group cancel and reloads all cancelled sub-orders; COD cancels singly", async () => {
    await ensureRateBudget(50, { label: "5j" });
    const buyerClient = new WebClient();
    const buyer = await registerAndLoginBuyer(buyerClient, "grp-cancel");
    const { address } = await createBuyerAddress(buyerClient, buyer);
    const [shopA, shopB] = await pickTwoShopSkus(buyerClient, { minStock: 3 });
    assert.notEqual(
      shopA.product.shop_id,
      shopB.product.shop_id,
      "expected two distinct shops for multi-shop group cancel test"
    );

    // 1. Create a 2-shop unpaid VIETQR checkout group
    const vietqrRes = await buyerClient.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-r3-vqr-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [
          { sku_id: shopA.sku.id, quantity: 1 },
          { sku_id: shopB.sku.id, quantity: 1 },
        ],
        payment_method: "VIETQR",
        shipping_choices: [
          { shop_id: shopA.product.shop_id, carrier_code: "ghn", service_code: "STANDARD" },
          { shop_id: shopB.product.shop_id, carrier_code: "ghn", service_code: "STANDARD" },
        ],
      },
    });
    assert.ok(
      vietqrRes.status === 200 || vietqrRes.status === 201,
      `2-shop VIETQR checkout failed (${vietqrRes.status}): ${vietqrRes.text}`
    );
    const vqrOrders = vietqrRes.data?.orders ?? [];
    assert.equal(vqrOrders.length, 2, `expected 2 VietQR sub-orders, got ${vqrOrders.length}`);
    assert.equal(vqrOrders[0].checkout_group_id, vqrOrders[1].checkout_group_id);
    assert.equal(vqrOrders[0].status, "PENDING");
    assert.equal(vqrOrders[1].status, "PENDING");

    // 2. Also create a 2-shop COD checkout group to verify COD keeps single sub-order cancellation
    const codRes = await buyerClient.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-r3-cod-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [
          { sku_id: shopA.sku.id, quantity: 1 },
          { sku_id: shopB.sku.id, quantity: 1 },
        ],
        payment_method: "COD",
        shipping_choices: [
          { shop_id: shopA.product.shop_id, carrier_code: "ghn", service_code: "STANDARD" },
          { shop_id: shopB.product.shop_id, carrier_code: "ghn", service_code: "STANDARD" },
        ],
      },
    });
    assert.ok(
      codRes.status === 200 || codRes.status === 201,
      `2-shop COD checkout failed (${codRes.status}): ${codRes.text}`
    );
    const codOrders = codRes.data?.orders ?? [];
    assert.equal(codOrders.length, 2, `expected 2 COD sub-orders, got ${codOrders.length}`);

    const expectedWarning =
      "Hủy đơn chưa thanh toán sẽ hủy toàn bộ lần đặt hàng gồm 2 gian hàng";

    const browser = await HeadlessBrowser.launch();
    try {
      await browser.setCookiesFromClient(buyerClient);

      // Step A: On /don-hang/[id] detail page for unpaid VietQR sub-order, verify warning & confirmation dialog
      await browser.goto(`/don-hang/${encodeURIComponent(vqrOrders[0].id)}`);
      await browser.waitFor(
        `document.body.innerText.includes(${JSON.stringify(vqrOrders[0].order_code)})`,
        15_000
      );
      const detailTextBefore = await browser.eval(`document.body.innerText`);
      assert.ok(
        detailTextBefore.includes(expectedWarning),
        `order detail cancel panel must display "${expectedWarning}"`
      );
      // Click "Xác nhận hủy đơn" -> must open confirmation dialog (not cancel immediately)
      await browser.eval(`(() => {
        const btn = Array.from(document.querySelectorAll('form button[type="submit"]'))
          .find(b => b.textContent && b.textContent.includes('Xác nhận hủy đơn'));
        if (btn) btn.click();
      })()`);
      await browser.waitFor(`Boolean(document.querySelector('[role="dialog"]'))`, 10_000);
      const detailDialogText = await browser.eval(
        `document.querySelector('[role="dialog"]')?.innerText || ''`
      );
      assert.ok(
        detailDialogText.includes(expectedWarning),
        `detail confirmation dialog must contain "${expectedWarning}", got: ${detailDialogText}`
      );
      assert.ok(
        detailDialogText.includes(vqrOrders[0].order_code) &&
          detailDialogText.includes(vqrOrders[1].order_code),
        `detail confirmation dialog should list both sibling order codes, got: ${detailDialogText}`
      );
      // Dismiss dialog -> order must remain PENDING
      await browser.eval(`(() => {
        const btn = Array.from(document.querySelectorAll('[role="dialog"] button'))
          .find(b => b.textContent && b.textContent.includes('Quay lại'));
        if (btn) btn.click();
      })()`);
      await browser.waitFor(`!document.querySelector('[role="dialog"]')`, 8_000);

      // Step B: On /don-hang list page, click "Hủy đơn" on the unpaid VietQR sub-order -> confirm dialog -> reloads list with BOTH sub-orders cancelled
      await browser.goto("/don-hang");
      await browser.waitFor(
        `document.body.innerText.includes(${JSON.stringify(vqrOrders[0].order_code)}) && document.body.innerText.includes(${JSON.stringify(vqrOrders[1].order_code)})`,
        15_000
      );
      const clickedListCancel = await browser.eval(`(() => {
        const cards = Array.from(document.querySelectorAll('article'));
        const card = cards.find(c => c.innerText.includes(${JSON.stringify(vqrOrders[0].order_code)}));
        if (!card) return false;
        const btn = Array.from(card.querySelectorAll('button')).find(b => b.textContent && b.textContent.trim() === 'Hủy đơn');
        if (!btn) return false;
        btn.click();
        return true;
      })()`);
      assert.equal(clickedListCancel, true, "expected 'Hủy đơn' button on unpaid VietQR order card");

      await browser.waitFor(`Boolean(document.querySelector('[role="dialog"]'))`, 10_000);
      const listDialogText = await browser.eval(
        `document.querySelector('[role="dialog"]')?.innerText || ''`
      );
      assert.ok(
        listDialogText.includes(expectedWarning),
        `/don-hang confirmation dialog must contain "${expectedWarning}", got: ${listDialogText}`
      );

      // Confirm group cancel in dialog
      await browser.eval(`(() => {
        const btn = Array.from(document.querySelectorAll('[role="dialog"] button'))
          .find(b => b.textContent && b.textContent.includes('hủy cả 2 đơn'));
        if (btn) btn.click();
      })()`);
      await browser.waitFor(`!document.querySelector('[role="dialog"]')`, 15_000);

      // Wait for reloaded list to show BOTH VietQR sub-orders as "Đã hủy"
      await browser.waitFor(
        `(() => {
          const cards = Array.from(document.querySelectorAll('article'));
          const c0 = cards.find(c => c.innerText.includes(${JSON.stringify(vqrOrders[0].order_code)}));
          const c1 = cards.find(c => c.innerText.includes(${JSON.stringify(vqrOrders[1].order_code)}));
          return Boolean(c0 && c1 && c0.innerText.includes('Đã hủy') && c1.innerText.includes('Đã hủy'));
        })()`,
        15_000
      );

      // Verify via API both VietQR sub-orders are CANCELLED
      const [checkVqr0, checkVqr1] = await Promise.all([
        buyerClient.json("GET", `/api/gw/orders/${encodeURIComponent(vqrOrders[0].id)}`),
        buyerClient.json("GET", `/api/gw/orders/${encodeURIComponent(vqrOrders[1].id)}`),
      ]);
      assert.equal(checkVqr0.data?.order?.status, "CANCELLED", "VietQR order 0 must be CANCELLED");
      assert.equal(
        checkVqr1.data?.order?.status,
        "CANCELLED",
        "VietQR sibling order 1 must also be CANCELLED"
      );

      // Step C: Verify COD multi-shop order cancel does NOT warn about full group and only cancels 1 sub-order
      const clickedCodCancel = await browser.eval(`(() => {
        const cards = Array.from(document.querySelectorAll('article'));
        const card = cards.find(c => c.innerText.includes(${JSON.stringify(codOrders[0].order_code)}));
        if (!card) return false;
        const btn = Array.from(card.querySelectorAll('button')).find(b => b.textContent && b.textContent.trim() === 'Hủy đơn');
        if (!btn) return false;
        btn.click();
        return true;
      })()`);
      assert.equal(clickedCodCancel, true, "expected 'Hủy đơn' button on COD order card");
      await browser.waitFor(`Boolean(document.querySelector('[role="dialog"]'))`, 10_000);
      const codDialogText = await browser.eval(
        `document.querySelector('[role="dialog"]')?.innerText || ''`
      );
      assert.equal(
        codDialogText.includes("hủy toàn bộ lần đặt hàng"),
        false,
        `COD cancel dialog must NOT warn about cancelling entire group, got: ${codDialogText}`
      );
      await browser.eval(`(() => {
        const btn = Array.from(document.querySelectorAll('[role="dialog"] button'))
          .find(b => b.textContent && b.textContent.includes('Xác nhận hủy đơn'));
        if (btn) btn.click();
      })()`);
      await browser.waitFor(`!document.querySelector('[role="dialog"]')`, 15_000);

      await browser.waitFor(
        `(() => {
          const cards = Array.from(document.querySelectorAll('article'));
          const c0 = cards.find(c => c.innerText.includes(${JSON.stringify(codOrders[0].order_code)}));
          const c1 = cards.find(c => c.innerText.includes(${JSON.stringify(codOrders[1].order_code)}));
          return Boolean(c0 && c1 && c0.innerText.includes('Đã hủy') && !c1.innerText.includes('Đã hủy'));
        })()`,
        15_000
      );
    } finally {
      await browser.close();
      // Clean up remaining COD sibling order
      for (const ord of [...vqrOrders, ...codOrders]) {
        await buyerClient
          .json("POST", `/api/gw/orders/${encodeURIComponent(ord.id)}/cancel`, {
            body: { reason: "Clean up Round 3 E2E test order" },
          })
          .catch(() => {});
      }
    }
  });
});
