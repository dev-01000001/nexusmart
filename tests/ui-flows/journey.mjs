// Full end-to-end NexusMart Shopify-model browser journey (18 steps: s01..s18).
// Covers: seller register + OTP -> shop onboarding -> admin approve -> F1 auto session sync +
// media upload + Shopify-style 2-variant product -> admin approve -> Site Studio + logo upload +
// publish -> buyer register + phone OTP + COD order -> seller confirm/ship -> HMAC shipping
// webhooks + confirm received + 5-star review -> VietQR order + HMAC payment webhook -> ship ->
// deliver -> 3-step return wizard (F6 check) -> seller immediate refund -> admin reconciliation ->
// seller bank account + OTP -> admin early escrow release -> seller withdraw -> admin payout approve.
//
// Locator policy (lib.mjs): ONLY role + Vietnamese visible text (getByRole, getByLabel, getByText).
// Optional --video <dir> records per-step .webm + .srt subtitles compatible with render.sh.
import { createHmac, randomBytes, randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { deflateSync } from "node:zlib";
import {
  assert,
  CREDS,
  gotoPage,
  MAILPIT_URL,
  rootOrigin,
  siteOrigin,
} from "./lib.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "../..");

function loadRepoEnv() {
  const envPath = path.join(REPO_ROOT, ".env");
  if (!fs.existsSync(envPath)) return {};
  const out = {};
  for (const line of fs.readFileSync(envPath, "utf8").split("\n")) {
    if (!/^[A-Z0-9_]+=/.test(line)) continue;
    const eq = line.indexOf("=");
    out[line.slice(0, eq)] = line.slice(eq + 1).replace(/^"|"$/g, "");
  }
  return out;
}

const repoEnv = loadRepoEnv();
const GATEWAY_URL = (
  process.env.GATEWAY_URL || "http://127.0.0.1:48080"
).replace(/\/+$/, "");
const PAY_WEBHOOK_SECRET =
  process.env.PAYMENT_WEBHOOK_SECRET ||
  repoEnv.PAYMENT_WEBHOOK_SECRET ||
  "dev-only-payment-webhook-hmac-secret";
const SHIP_WEBHOOK_SECRET =
  process.env.SHIPPING_WEBHOOK_SECRET ||
  repoEnv.SHIPPING_WEBHOOK_SECRET ||
  "dev-only-shipping-webhook-hmac-secret";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function escapeRe(str) {
  return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function randomDigits(count) {
  const bytes = randomBytes(count);
  let out = "";
  for (let i = 0; i < count; i++) out += String(bytes[i] % 10);
  return out;
}

// ---------- Minimal valid PNG generator (>= 64x64, RGB 8-bit, zero external deps) ----------
const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  return table;
})();

function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) {
    c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  }
  return (c ^ 0xffffffff) >>> 0;
}

function pngChunk(type, data) {
  const typeBuf = Buffer.from(type, "ascii");
  const lenBuf = Buffer.alloc(4);
  lenBuf.writeUInt32BE(data.length, 0);
  const crcBuf = Buffer.alloc(4);
  crcBuf.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])), 0);
  return Buffer.concat([lenBuf, typeBuf, data, crcBuf]);
}

function makeSolidPng(width, height, rgb, accentRgb) {
  const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // color type: Truecolor RGB
  ihdr[10] = 0; // compression
  ihdr[11] = 0; // filter
  ihdr[12] = 0; // interlace

  const rowStride = 1 + width * 3;
  const raw = Buffer.alloc(rowStride * height);
  for (let y = 0; y < height; y++) {
    const rowStart = y * rowStride;
    raw[rowStart] = 0; // filter type: None
    for (let x = 0; x < width; x++) {
      const px = rowStart + 1 + x * 3;
      const inAccent =
        x >= Math.floor(width / 4) &&
        x < Math.floor((3 * width) / 4) &&
        y >= Math.floor(height / 4) &&
        y < Math.floor((3 * height) / 4);
      const c = inAccent ? accentRgb : rgb;
      raw[px] = c[0];
      raw[px + 1] = c[1];
      raw[px + 2] = c[2];
    }
  }
  const idat = deflateSync(raw, { level: 6 });
  return Buffer.concat([
    signature,
    pngChunk("IHDR", ihdr),
    pngChunk("IDAT", idat),
    pngChunk("IEND", Buffer.alloc(0)),
  ]);
}

// ---------- Subtitle segment recorder (compatible with .runs/shopify-e2e/journey/render.sh) ----------
class Seg {
  constructor(stepId, stepTitle, enabled) {
    this.stepId = stepId;
    this.stepTitle = stepTitle;
    this.enabled = enabled;
    this.t0 = Date.now();
    this.caps = [];
  }

  cap(text) {
    if (!this.enabled) return;
    const t = (Date.now() - this.t0) / 1000;
    if (this.caps.length) this.caps.at(-1).end = t;
    this.caps.push({ start: t, end: null, text });
  }

  srt(total) {
    const fmt = (s) => {
      const ms = Math.max(0, Math.round(s * 1000));
      const h = String(Math.floor(ms / 3_600_000)).padStart(2, "0");
      const m = String(Math.floor((ms % 3_600_000) / 60_000)).padStart(2, "0");
      const sec = String(Math.floor((ms % 60_000) / 1_000)).padStart(2, "0");
      return `${h}:${m}:${sec},${String(ms % 1_000).padStart(3, "0")}`;
    };
    return this.caps
      .map(
        (c, i) =>
          `${i + 1}\n${fmt(c.start)} --> ${fmt(c.end ?? total)}\n${c.text}\n`,
      )
      .join("\n");
  }
}

// ---------- Gateway API, HMAC webhook & Mailpit helpers ----------
async function gwJson(method, apiPath, { token, body } = {}) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const res = await fetch(`${GATEWAY_URL}${apiPath}`, {
      method,
      headers: {
        "content-type": "application/json",
        ...(token ? { authorization: `Bearer ${token}` } : {}),
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (res.status === 429 && attempt < 2) {
      await sleep(5_000 * (attempt + 1));
      continue;
    }
    const text = await res.text();
    let data = null;
    try {
      data = JSON.parse(text);
    } catch {
      // Non-JSON response
    }
    return { status: res.status, data, text: text.slice(0, 400) };
  }
  throw new Error(`Gateway ${method} ${apiPath} bị 429 quá 3 lần`);
}

async function gwLogin(email, password) {
  const res = await gwJson("POST", "/api/v1/auth/login", {
    body: { email, password },
  });
  assert(
    res.status === 200 && res.data?.access_token,
    `Gateway login ${email} thất bại: HTTP ${res.status} ${res.text}`,
  );
  return res.data;
}

async function pollUntil(label, fn, timeoutMs = 25_000, intervalMs = 600) {
  const deadline = Date.now() + timeoutMs;
  let lastDetail = "";
  while (Date.now() < deadline) {
    const res = await fn();
    if (res?.ok) return res.value;
    lastDetail = res?.detail ?? "";
    await sleep(intervalMs);
  }
  throw new Error(
    `Quá thời gian chờ (${timeoutMs}ms): ${label}${lastDetail ? ` — ${lastDetail}` : ""}`,
  );
}

async function signedWebhook(url, secret, payload) {
  const body = JSON.stringify(payload);
  const sig = createHmac("sha256", secret).update(body, "utf8").digest("hex");
  const res = await fetch(url, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-signature": sig,
      "x-timestamp": String(Math.floor(Date.now() / 1000)),
    },
    body,
  });
  return { status: res.status, text: (await res.text()).slice(0, 300) };
}

async function fetchMailpitOtp(
  email,
  { afterMs = 0, subjectPattern = null, timeoutMs = 30_000 } = {},
) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const searchRes = await fetch(
      `${MAILPIT_URL}/api/v1/search?query=${encodeURIComponent(`to:${email}`)}`,
    );
    if (searchRes.ok) {
      const data = await searchRes.json();
      for (const m of data.messages || []) {
        const createdMs = new Date(m.Created).getTime();
        if (Number.isFinite(createdMs) && createdMs < afterMs) continue;
        const msgRes = await fetch(
          `${MAILPIT_URL}/api/v1/message/${encodeURIComponent(m.ID)}`,
        );
        if (!msgRes.ok) continue;
        const full = await msgRes.json();
        const subject = String(full.Subject || "");
        if (subjectPattern && !subjectPattern.test(subject)) continue;
        const body = `${full.Text || ""} ${full.HTML || ""}`;
        const match = body.match(/\b(\d{6})\b/);
        if (match) return match[1];
      }
    }
    await sleep(700);
  }
  throw new Error(
    `Không thấy OTP trong Mailpit cho ${email}${subjectPattern ? ` (tiêu đề khớp ${subjectPattern})` : ""}`,
  );
}

// ---------- UI helpers (strictly role + Vietnamese text locators) ----------
async function radixSelect(page, trigger, optionName = null, idx = 0) {
  await trigger.waitFor({ state: "visible", timeout: 10_000 });
  await trigger.click();
  const options = page.getByRole("option");
  await options.first().waitFor({ state: "visible", timeout: 8_000 });
  if (optionName) {
    await page.getByRole("option", { name: optionName }).first().click();
  } else {
    await options.nth(idx).click();
  }
  await page.waitForTimeout(250);
}

async function assertMediaServed(page, url, label) {
  const res = await page.request.get(url);
  assert(
    res.status() === 200,
    `${label}: GET ${url} trả HTTP ${res.status()}, mong đợi 200`,
  );
  const ct = (res.headers()["content-type"] || "").toLowerCase();
  assert(
    ct.startsWith("image/"),
    `${label}: GET ${url} Content-Type="${ct}", mong đợi image/*`,
  );
}

// ---------- Journey builder ----------
export async function buildJourneyFlows({ browser, port, videoDir = "" }) {
  const origin = rootOrigin(port);
  const recordVideo = Boolean(videoDir);
  if (recordVideo) {
    fs.mkdirSync(path.join(videoDir, "raw"), { recursive: true });
  }

  // Fresh unique tag per run so consecutive executions never collide.
  const tag = `${Date.now().toString(36).slice(-4)}${randomBytes(2).toString("hex")}`;
  const st = {
    tag,
    seller: {
      name: "Trần Minh Khoa",
      bankHolder: "TRAN MINH KHOA",
      bankAccountNumber: `1903${randomDigits(10)}`,
      email: `khoa.${tag}@example.com`,
      phone: `09${randomDigits(8)}`,
      password: "KhoaShop2026!",
    },
    buyer: {
      name: "Lê Thu Hà",
      email: `ha.${tag}@example.com`,
      phone: `08${randomDigits(8)}`,
      password: "ThuHaMua2026!",
    },
    shopName: `Gốm Bát Tràng ${tag.toUpperCase()}`,
    sub: `gom-${tag}`,
    productName: `Bộ ấm chén men lam ${tag.toUpperCase()}`,
    shopId: "",
    productId: "",
    productImages: [],
    logoUrl: "",
    siteUrl: siteOrigin(`gom-${tag}`, port),
    codOrderId: "",
    codTrackingCode: "",
    codCarrierCode: "mock_express",
    qrOrderId: "",
    qrTrackingCode: "",
    qrCarrierCode: "mock_express",
    releasedAmountVnd: 0,
    withdrawAmountVnd: 0,
    payoutId: "",
  };

  const tokens = {
    admin: "",
    seller: "",
    buyer: "",
  };

  // Generate 3 distinct 128x128 PNG fixtures (>= 64x64) in an isolated temp dir.
  const tmpMediaDir = fs.mkdtempSync(
    path.join(os.tmpdir(), "nm-journey-media-"),
  );
  const prodImg1Path = path.join(tmpMediaDir, `men-lam-${tag}.png`);
  const prodImg2Path = path.join(tmpMediaDir, `men-ran-${tag}.png`);
  const logoImgPath = path.join(tmpMediaDir, `logo-${tag}.png`);
  fs.writeFileSync(
    prodImg1Path,
    makeSolidPng(128, 128, [30, 92, 69], [238, 244, 240]),
  );
  fs.writeFileSync(
    prodImg2Path,
    makeSolidPng(128, 128, [56, 86, 130], [244, 236, 224]),
  );
  fs.writeFileSync(
    logoImgPath,
    makeSolidPng(128, 128, [164, 74, 46], [250, 246, 238]),
  );

  // Independent browser contexts per actor so sessions stay realistic without cross-leaking.
  const ctxs = {};
  async function getActorContext(actor) {
    if (ctxs[actor]) return ctxs[actor];
    const opts = {
      viewport: recordVideo
        ? { width: 1280, height: 800 }
        : { width: 1440, height: 900 },
      locale: "vi-VN",
      reducedMotion: recordVideo ? "no-preference" : "reduce",
      ...(recordVideo
        ? {
            recordVideo: {
              dir: path.join(videoDir, "raw"),
              size: { width: 1280, height: 800 },
            },
          }
        : {}),
    };
    ctxs[actor] = await browser.newContext(opts);
    return ctxs[actor];
  }

  async function ensureAdminToken() {
    if (!tokens.admin) {
      const data = await gwLogin(CREDS.admin.email, CREDS.admin.password);
      tokens.admin = data.access_token;
    }
    return tokens.admin;
  }

  async function ensureSellerToken(forceRefresh = false) {
    if (!tokens.seller || forceRefresh) {
      const data = await gwLogin(st.seller.email, st.seller.password);
      tokens.seller = data.access_token;
    }
    return tokens.seller;
  }

  async function ensureBuyerToken() {
    if (!tokens.buyer) {
      const data = await gwLogin(st.buyer.email, st.buyer.password);
      tokens.buyer = data.access_token;
    }
    return tokens.buyer;
  }

  async function shipOrderViaUi(page, seg, orderId) {
    const sellerTok = await ensureSellerToken();
    await gotoPage(page, `${origin}/seller/don-hang/${orderId}`);

    const confirmBtn = page
      .getByRole("button", { name: /^Xác nhận đơn$/i })
      .first();
    if (await confirmBtn.isVisible().catch(() => false)) {
      seg.cap("[Người bán] Xác nhận đơn hàng");
      await confirmBtn.click();
      await pollUntil(
        `Đơn ${orderId} chuyển sang CONFIRMED/PREPARING`,
        async () => {
          const r = await gwJson("GET", `/api/v1/seller/orders/${orderId}`, {
            token: sellerTok,
          });
          const status = (
            r.data?.order?.status ||
            r.data?.status ||
            ""
          ).toUpperCase();
          return {
            ok:
              status === "CONFIRMED" ||
              status === "PREPARING" ||
              status === "SHIPPING",
            value: r.data?.order ?? r.data,
            detail: `HTTP ${r.status} status=${status}`,
          };
        },
        20_000,
      );
      await page.reload({ waitUntil: "domcontentloaded" });
      await page
        .waitForLoadState("networkidle", { timeout: 10_000 })
        .catch(() => {});
    }

    seg.cap("[Người bán] Tạo vận đơn bàn giao hãng vận chuyển");
    const shipBtn = page
      .getByRole("button", { name: /^(Tạo vận đơn|Thử tạo lại ngay)$/i })
      .first();
    await shipBtn.waitFor({ state: "visible", timeout: 12_000 });
    await shipBtn.click();

    const order = await pollUntil(
      `Đơn ${orderId} có tracking_code`,
      async () => {
        const r = await gwJson("GET", `/api/v1/seller/orders/${orderId}`, {
          token: sellerTok,
        });
        const o = r.data?.order ?? r.data;
        return {
          ok: Boolean(o?.tracking_code),
          value: o,
          detail: `HTTP ${r.status} status=${o?.status} tracking=${o?.tracking_code || ""}`,
        };
      },
      25_000,
      800,
    );

    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByText(order.tracking_code).first().waitFor({
      state: "visible",
      timeout: 12_000,
    });
    seg.cap(`[Người bán] Đã tạo vận đơn ${order.tracking_code}`);
    return {
      trackingCode: order.tracking_code,
      carrierCode: order.carrier_code || "mock_express",
    };
  }

  async function clickAndAwaitBff(page, button, urlPart, method = "POST") {
    for (let attempt = 0; attempt < 3; attempt++) {
      const [resp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes(urlPart) && r.request().method() === method,
          { timeout: 20_000 },
        ),
        button.click(),
      ]);
      if (resp.status() === 429 && attempt < 2) {
        const ra = Number(resp.headers()["retry-after"]);
        const waitMs =
          Number.isFinite(ra) && ra > 0
            ? Math.min(ra * 1_000 + 500, 25_000)
            : 8_000 * (attempt + 1);
        await sleep(waitMs);
        continue;
      }
      return resp;
    }
    throw new Error(`${method} ${urlPart} vẫn bị 429 sau 3 lần thử`);
  }

  async function deliverViaSignedWebhook(
    page,
    seg,
    orderId,
    trackingCode,
    carrierCode,
    noteSuffix,
  ) {
    const buyerTok = await ensureBuyerToken();
    if (recordVideo) {
      await gotoPage(page, `${st.siteUrl}/don-hang/${orderId}`);
    }

    for (const [rawStatus, desc, caption] of [
      [
        "delivering",
        "Đang giao kiện hàng",
        `[Hãng VC giả lập] Webhook “đang giao” (HMAC) — ${trackingCode}`,
      ],
      [
        "delivered",
        "Giao kiện hàng thành công",
        `[Hãng VC giả lập] Webhook “đã giao thành công”${noteSuffix ? `, ${noteSuffix}` : ""}`,
      ],
    ]) {
      seg.cap(caption);
      const res = await signedWebhook(
        `${GATEWAY_URL}/webhooks/shipping/${encodeURIComponent(carrierCode)}`,
        SHIP_WEBHOOK_SECRET,
        {
          event_id: `jr-${randomUUID()}`,
          tracking_code: trackingCode,
          order_id: orderId,
          raw_status: rawStatus,
          description: desc,
          occurred_at: new Date().toISOString(),
        },
      );
      assert(
        res.status >= 200 && res.status < 300,
        `Webhook shipping (${rawStatus}) trả HTTP ${res.status}: ${res.text}`,
      );
      await sleep(1_200);
    }

    await pollUntil(
      `Đơn ${orderId} chuyển DELIVERED qua API`,
      async () => {
        const r = await gwJson("GET", `/api/v1/orders/${orderId}`, {
          token: buyerTok,
        });
        const status = (r.data?.order?.status || "").toUpperCase();
        return {
          ok: status === "DELIVERED",
          value: r.data?.order,
          detail: `HTTP ${r.status} status=${status}`,
        };
      },
      25_000,
      1_000,
    );

    if (recordVideo) {
      await page.reload({ waitUntil: "domcontentloaded" });
      await page
        .waitForLoadState("networkidle", { timeout: 10_000 })
        .catch(() => {});
    } else {
      await gotoPage(page, `${st.siteUrl}/don-hang/${orderId}`);
    }
    await page
      .getByText(/Đã giao/i)
      .first()
      .waitFor({ state: "visible", timeout: 12_000 });
    seg.cap("[Khách] Đơn hiển thị “Đã giao”");
  }

  async function selectCheckoutLocation(page) {
    const prov = page
      .getByRole("combobox", {
        name: /Chọn Tỉnh hoặc Thành phố|Tỉnh \/ Thành phố/i,
      })
      .first();
    await prov.waitFor({ state: "visible", timeout: 10_000 });
    const provTag = await prov.evaluate((e) => e.tagName);
    if (provTag === "SELECT") {
      await prov.selectOption({ index: 1 });
    } else {
      await prov.click();
      const searchInput = page.getByLabel(/Tìm tỉnh hoặc thành phố/i).first();
      if (await searchInput.isVisible({ timeout: 2_000 }).catch(() => false)) {
        await searchInput.fill("Hồ Chí Minh");
      } else {
        await page.keyboard.type("Hồ Chí Minh", { delay: 30 });
      }
      const hcm = page.getByRole("option", { name: /Hồ Chí Minh/i }).first();
      if (await hcm.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await hcm.click();
      } else {
        await page.getByRole("option").first().click();
      }
    }
    await page.waitForTimeout(500);

    const ward = page
      .getByRole("combobox", { name: /Chọn Phường hoặc Xã|Phường \/ Xã/i })
      .first();
    await ward.waitFor({ state: "visible", timeout: 10_000 });
    const wardTag = await ward.evaluate((e) => e.tagName);
    if (wardTag === "SELECT") {
      await ward.selectOption({ index: 1 });
    } else {
      await ward.click();
      await page
        .getByRole("option")
        .first()
        .waitFor({ state: "visible", timeout: 8_000 });
      await page.getByRole("option").first().click();
    }
  }

  const STEP_DEFS = [
    {
      id: "s01",
      name: "journey:s01-seller-register-otp",
      actor: "seller",
      title: "Người bán đăng ký tài khoản + OTP email",
      async fn(page, seg) {
        seg.cap("[Người bán] Vào trang chủ nền tảng NexusMart");
        await gotoPage(page, `${origin}/`);
        seg.cap("[Người bán] Mở trang đăng ký tài khoản");
        await gotoPage(page, `${origin}/dang-ky`);

        const sellerRole = page
          .getByRole("radio", { name: /Người bán/i })
          .or(page.getByRole("button", { name: /^Người bán$/i }));
        if (
          await sellerRole
            .first()
            .isVisible()
            .catch(() => false)
        ) {
          await sellerRole.first().click();
        }

        seg.cap(`[Người bán] Điền họ tên, email ${st.seller.email}, mật khẩu`);
        await page.getByLabel(/Họ và tên/i).fill(st.seller.name);
        await page.getByLabel(/^Email$/i).fill(st.seller.email);
        const phoneField = page.getByLabel(/Số điện thoại/i);
        if (
          await phoneField
            .first()
            .isVisible()
            .catch(() => false)
        ) {
          await phoneField.first().fill(st.seller.phone);
        }
        await page
          .getByLabel(/Mật khẩu \(|^Mật khẩu$/i)
          .first()
          .fill(st.seller.password);
        await page.getByLabel(/Nhập lại mật khẩu/i).fill(st.seller.password);

        const t0 = Date.now() - 5_000;
        await page
          .getByRole("button", { name: /Mở gian hàng|Tạo tài khoản|Đăng ký/i })
          .last()
          .click();

        const otpInput = page.getByLabel(/Mã xác thực 6 chữ số/i);
        await otpInput.waitFor({ state: "visible", timeout: 20_000 });

        seg.cap("[Hộp thư] Lấy mã OTP 6 chữ số từ Mailpit");
        const otp = await fetchMailpitOtp(st.seller.email, { afterMs: t0 });
        await otpInput.click();
        await page.keyboard.type(otp, { delay: 60 });
        seg.cap("[Người bán] Nhập OTP xác thực email");

        const verifyBtn = page
          .getByRole("button", { name: /Xác (thực|nhận)/i })
          .first();
        if (
          (await verifyBtn.isVisible().catch(() => false)) &&
          (await verifyBtn.isEnabled().catch(() => false))
        ) {
          await verifyBtn.click();
        }
        await page.waitForURL(/\/seller|\/dang-nhap/, { timeout: 30_000 });
        if (/\/dang-nhap/.test(page.url())) {
          seg.cap("[Người bán] Đăng nhập lần đầu");
          await page.getByLabel(/^Email$/i).fill(st.seller.email);
          await page
            .getByLabel(/Mật khẩu/i)
            .first()
            .fill(st.seller.password);
          await page
            .getByRole("button", { name: /Đăng nhập/i })
            .last()
            .click();
          await page.waitForURL(/\/seller/, { timeout: 30_000 });
        }

        const loginData = await gwLogin(st.seller.email, st.seller.password);
        tokens.seller = loginData.access_token;
        assert(
          loginData.user?.email?.toLowerCase() ===
            st.seller.email.toLowerCase(),
          `API auth/login trả email không khớp: ${loginData.user?.email}`,
        );
        seg.cap("[Người bán] Đã vào Kênh người bán");
        return `email=${st.seller.email}`;
      },
    },
    {
      id: "s02",
      name: "journey:s02-seller-shop-onboarding",
      actor: "seller",
      title: "Người bán khai hồ sơ mở gian hàng",
      async fn(page, seg) {
        await gotoPage(page, `${origin}/seller`);
        seg.cap("[Người bán] Khai hồ sơ gian hàng và kho lấy hàng");

        await page.getByLabel("Tên gian hàng").fill(st.shopName);
        await page.getByLabel("Số điện thoại liên hệ").fill(st.seller.phone);
        const slugInput = page.getByLabel(/Đường dẫn gian hàng/i);
        if (await slugInput.isVisible().catch(() => false)) {
          await slugInput.fill(st.sub);
        }
        await page
          .getByLabel("Giấy phép kinh doanh")
          .fill("0109876543 — Hộ kinh doanh gốm Bát Tràng");
        await page
          .getByLabel(/Mô tả gian hàng/i)
          .fill(
            "Gốm sứ thủ công Bát Tràng: ấm chén, bình hoa, bát đĩa men lam.",
          );

        const whForm = page.getByRole("form", {
          name: "Thêm địa chỉ kho lấy hàng",
        });
        if (!(await whForm.isVisible().catch(() => false))) {
          const addWhBtn = page.getByRole("button", {
            name: /Thêm địa chỉ kho/i,
          });
          await addWhBtn.waitFor({ state: "visible", timeout: 10_000 });
          await addWhBtn.click();
          await whForm.waitFor({ state: "visible", timeout: 10_000 });
        }
        seg.cap("[Người bán] Thêm địa chỉ kho lấy hàng");
        await radixSelect(
          page,
          whForm.getByRole("combobox", { name: "Tỉnh / Thành phố" }),
          /Hà Nội/,
        );
        await radixSelect(
          page,
          whForm.getByRole("combobox", { name: "Phường / Xã" }),
          null,
          0,
        );
        await whForm
          .getByRole("textbox", { name: "Số nhà, tên đường" })
          .fill("Xóm 3, làng gốm Bát Tràng");
        await whForm.getByRole("button", { name: "Lưu địa chỉ kho" }).click();

        const submitBtn = page.getByRole("button", {
          name: "Gửi đăng ký mở gian hàng",
        });
        await pollUntil(
          "Nút gửi hồ sơ gian hàng sẵn sàng sau khi lưu địa chỉ kho",
          async () => ({
            ok: await submitBtn.isEnabled().catch(() => false),
          }),
          15_000,
          400,
        );

        seg.cap("[Người bán] Gửi hồ sơ gian hàng chờ duyệt");
        await submitBtn.click();
        await page
          .getByText(/Chờ duyệt|Đang chờ/i)
          .first()
          .waitFor({ state: "visible", timeout: 20_000 });

        const sellerTok = await ensureSellerToken();
        const me = await gwJson("GET", "/api/v1/shops/me", {
          token: sellerTok,
        });
        assert(
          me.status === 200 && me.data?.shop?.status === "PENDING",
          `GET /api/v1/shops/me kỳ vọng PENDING, nhận HTTP ${me.status} status=${me.data?.shop?.status}`,
        );
        st.shopId = me.data.shop.id;
        assert(Boolean(st.shopId), "shop.id rỗng sau khi đăng ký gian hàng");
        seg.cap("[Người bán] Hồ sơ đang chờ quản trị duyệt");
        return `shopId=${st.shopId} status=PENDING`;
      },
    },
    {
      id: "s03",
      name: "journey:s03-admin-approve-shop",
      actor: "admin",
      title: "Admin duyệt gian hàng",
      async fn(page, seg) {
        seg.cap("[Admin] Đăng nhập cổng quản trị");
        await gotoPage(page, `${origin}/dang-nhap`);
        await page.getByLabel(/^Email$/i).fill(CREDS.admin.email);
        await page
          .getByLabel(/Mật khẩu/i)
          .first()
          .fill(CREDS.admin.password);
        await page
          .getByRole("button", { name: /Đăng nhập/i })
          .last()
          .click();
        await page.waitForURL(/\/admin/, { timeout: 30_000 });

        seg.cap("[Admin] Mở hàng đợi duyệt gian hàng");
        await gotoPage(page, `${origin}/admin/gian-hang`);
        const searchBox = page.getByRole("searchbox").first();
        await searchBox.waitFor({ state: "visible", timeout: 10_000 });
        await searchBox.fill(st.shopName);
        await page.waitForTimeout(800);

        await page.getByText(st.shopName).first().click();
        seg.cap(`[Admin] Xem hồ sơ “${st.shopName}” → Phê duyệt`);
        const dlg = page.getByRole("dialog", {
          name: new RegExp(escapeRe(st.shopName)),
        });
        await dlg.waitFor({ state: "visible", timeout: 10_000 });
        await dlg
          .getByRole("textbox", { name: /Ghi chú thẩm định/i })
          .fill("Hồ sơ đầy đủ, đồng ý mở gian hàng.");
        await dlg.getByRole("button", { name: "Phê duyệt" }).click();

        const ad = page.getByRole("alertdialog");
        await ad.waitFor({ state: "visible", timeout: 10_000 });
        await ad.getByRole("button", { name: "Xác nhận phê duyệt" }).click();
        await ad.waitFor({ state: "hidden", timeout: 15_000 });

        // Refresh gateway API token for seller so claims reflect role=seller + shop_id.
        const sellerTok = await ensureSellerToken(true);
        await ensureAdminToken();
        const me = await pollUntil(
          "Gian hàng chuyển ACTIVE qua API",
          async () => {
            const r = await gwJson("GET", "/api/v1/shops/me", {
              token: sellerTok,
            });
            return {
              ok: r.status === 200 && r.data?.shop?.status === "ACTIVE",
              value: r.data?.shop,
              detail: `HTTP ${r.status} status=${r.data?.shop?.status}`,
            };
          },
          15_000,
        );
        assert(me.id === st.shopId, `shop.id lệch: ${me.id} !== ${st.shopId}`);
        seg.cap("[Admin] Gian hàng đã ACTIVE");
        return `shop=${st.shopId} ACTIVE`;
      },
    },
    {
      id: "s04",
      name: "journey:s04-seller-sync-upload-create-product",
      actor: "seller",
      title:
        "Người bán tự đồng bộ phiên (F1), tải ảnh và đăng sản phẩm 2 biến thể",
      async fn(page, seg) {
        seg.cap(
          "[Người bán] Mở /seller — kiểm tra tự đồng bộ phiên (không đăng nhập lại)",
        );
        await page.goto(`${origin}/seller`, {
          waitUntil: "domcontentloaded",
          timeout: 30_000,
        });

        // Strict F1 gate: within <= 20s, SellerSessionSync must refresh session and render full seller nav.
        const mainNav = page.getByRole("navigation", {
          name: "Điều hướng chính",
        });
        const productsNavLink = mainNav.getByRole("link", {
          name: /^Sản phẩm$/i,
        });
        const syncErrBtn = page.getByRole("button", {
          name: /Đồng bộ lại phiên/i,
        });
        const syncDeadline = Date.now() + 20_000;
        let synced = false;
        while (Date.now() < syncDeadline) {
          if (await syncErrBtn.isVisible().catch(() => false)) {
            throw new Error(
              "F1: SellerSessionSync hiển thị lỗi thay vì tự đồng bộ phiên người bán",
            );
          }
          if (await productsNavLink.isVisible().catch(() => false)) {
            const stillOnboarding = await mainNav
              .getByText("Đăng ký gian hàng")
              .first()
              .isVisible()
              .catch(() => false);
            if (!stillOnboarding) {
              synced = true;
              break;
            }
          }
          await sleep(300);
        }
        assert(
          synced,
          "F1: Sau khi admin duyệt, mở /seller không tự đồng bộ phiên và hiện menu người bán trong ≤ 20s",
        );

        seg.cap(
          "[Người bán] Mở form thêm sản phẩm mới (trống, không còn SKU áo mẫu)",
        );
        await gotoPage(page, `${origin}/seller/san-pham/moi`);
        await page
          .getByRole("heading", { level: 1, name: /Thêm sản phẩm mới/i })
          .waitFor({ state: "visible", timeout: 15_000 });

        const nameInput = page.getByRole("textbox", { name: "Tên sản phẩm" });
        const descInput = page.getByRole("textbox", { name: "Mô tả sản phẩm" });
        assert(
          (await nameInput.inputValue()) === "",
          "F2: Tên sản phẩm mới không bắt đầu trống",
        );
        const skuRowLocator = page
          .getByRole("row")
          .filter({ has: page.getByRole("textbox", { name: /^Mã SKU / }) });
        const initialRowCount = await skuRowLocator.count();
        assert(
          initialRowCount === 1,
          `F2: Form sản phẩm mới phải bắt đầu với 1 dòng biến thể mặc định, nhưng thấy ${initialRowCount} dòng`,
        );

        await nameInput.fill(st.productName);
        await descInput.fill(
          "Bộ ấm chén men lam vẽ tay, gồm 1 ấm 6 chén, nung 1.250°C tại Bát Tràng.",
        );

        seg.cap("[Người bán] Tải 2 ảnh sản phẩm lên bằng MediaUploader");
        const imgCard = page.locator('[aria-labelledby="prod-images-heading"]');
        await imgCard.scrollIntoViewIfNeeded();
        await imgCard
          .locator('input[type="file"]')
          .first()
          .setInputFiles([prodImg1Path, prodImg2Path]);
        await page
          .getByText(/Đã có 2\/9 ảnh/)
          .waitFor({ state: "visible", timeout: 25_000 });
        assert(
          !(await page
            .getByRole("button", { name: "Đóng thông báo lỗi tải ảnh" })
            .isVisible()
            .catch(() => false)),
          "MediaUploader báo lỗi khi tải 2 ảnh sản phẩm",
        );

        seg.cap(
          "[Người bán] Tạo tùy chọn “Màu men” với 2 giá trị: Men lam, Men rạn",
        );
        await page.getByRole("button", { name: "Thêm tùy chọn" }).click();
        await page.getByLabel("Tên tùy chọn 1").fill("Màu men");
        await page
          .getByRole("textbox", { name: "Giá trị cho tùy chọn Màu men" })
          .fill("Men lam, Men rạn");
        await page.getByRole("button", { name: "Thêm giá trị" }).click();

        await pollUntil(
          "Sinh đủ 2 dòng biến thể Men lam / Men rạn",
          async () => ({
            ok: (await skuRowLocator.count()) === 2,
            detail: `count=${await skuRowLocator.count()}`,
          }),
          10_000,
          250,
        );

        const rowLam = skuRowLocator.filter({ hasText: "Men lam" }).first();
        const rowRan = skuRowLocator.filter({ hasText: "Men rạn" }).first();
        await rowLam.waitFor({ state: "visible", timeout: 5_000 });
        await rowRan.waitFor({ state: "visible", timeout: 5_000 });

        await rowLam
          .getByRole("spinbutton", { name: /^Đơn giá / })
          .fill("450000");
        await rowLam.getByRole("spinbutton", { name: /^Tồn kho / }).fill("30");
        await rowLam
          .getByRole("spinbutton", { name: /^Khối lượng / })
          .fill("1200");

        await rowRan
          .getByRole("spinbutton", { name: /^Đơn giá / })
          .fill("490000");
        await rowRan.getByRole("spinbutton", { name: /^Tồn kho / }).fill("20");
        await rowRan
          .getByRole("spinbutton", { name: /^Khối lượng / })
          .fill("1250");

        const catCombo = page.getByRole("combobox", { name: "Ngành hàng" });
        await catCombo.scrollIntoViewIfNeeded();
        await catCombo.click();
        const prefCat = page.getByRole("option", {
          name: /Gốm sứ & Đồ bếp|Gốm sứ|Nhà cửa|Gia dụng/i,
        });
        const realFallbackCat = page.getByRole("option", {
          name: /^Áo thun/i,
        });
        if (
          await prefCat
            .first()
            .isVisible({ timeout: 2_000 })
            .catch(() => false)
        ) {
          await prefCat.first().click();
        } else if (
          await realFallbackCat
            .first()
            .isVisible({ timeout: 2_000 })
            .catch(() => false)
        ) {
          await realFallbackCat.first().click();
        } else {
          throw new Error(
            "Không tìm thấy ngành hàng hợp lệ (Gốm sứ / Gia dụng / Áo thun) trong combobox Ngành hàng",
          );
        }

        seg.cap("[Người bán] Đăng bán sản phẩm (chờ duyệt)");
        await page
          .getByRole("button", { name: "Đăng bán sản phẩm" })
          .first()
          .click();
        await page.waitForURL(/\/seller\/san-pham\/(?!moi)[0-9a-f-]+/i, {
          timeout: 25_000,
        });

        const sellerTok = await ensureSellerToken();
        const created = await pollUntil(
          "Sản phẩm vừa tạo xuất hiện trên API seller/products",
          async () => {
            const r = await gwJson(
              "GET",
              "/api/v1/seller/products?page_size=20",
              {
                token: sellerTok,
              },
            );
            const pr = (r.data?.items || []).find(
              (x) => x.name === st.productName,
            );
            return {
              ok: Boolean(pr),
              value: pr,
              detail: `HTTP ${r.status} items=${(r.data?.items || []).length}`,
            };
          },
          15_000,
        );

        assert(
          created.status === "PENDING",
          `Sản phẩm mới phải PENDING, nhận ${created.status}`,
        );
        assert(
          Array.isArray(created.skus) && created.skus.length === 2,
          `Kỳ vọng 2 SKU, nhận ${created.skus?.length}`,
        );
        const skuPrices = created.skus
          .map((s) => s.price_vnd)
          .sort((a, b) => a - b);
        assert(
          skuPrices[0] === 450000 && skuPrices[1] === 490000,
          `Giá 2 SKU không khớp [450000, 490000]: ${JSON.stringify(skuPrices)}`,
        );
        const mediaRe = new RegExp(
          `^/media/shops/${escapeRe(st.shopId)}/[0-9a-f]{64}\\.(png|jpg|jpeg|webp)$`,
        );
        assert(
          Array.isArray(created.images) &&
            created.images.length >= 2 &&
            mediaRe.test(created.images[0]),
          `images[0] không đúng mẫu /media/shops/<shop_id>/<sha256>.(png|jpg): ${JSON.stringify(created.images)}`,
        );
        st.productId = created.id;
        st.productImages = created.images;

        await assertMediaServed(
          page,
          `${origin}${created.images[0]}`,
          "s04 product image (root)",
        );
        seg.cap("[Người bán] Đã tạo sản phẩm 2 biến thể + 2 ảnh media hợp lệ");
        return `productId=${st.productId} images=${created.images.length} skus=2`;
      },
    },
    {
      id: "s05",
      name: "journey:s05-admin-approve-product",
      actor: "admin",
      title: "Admin kiểm duyệt sản phẩm",
      async fn(page, seg) {
        seg.cap("[Admin] Mở hàng đợi kiểm duyệt sản phẩm");
        await gotoPage(page, `${origin}/admin/kiem-duyet`);
        const searchBox = page.getByRole("searchbox", {
          name: /Tìm kiếm trong hàng đợi/i,
        });
        await searchBox.waitFor({ state: "visible", timeout: 10_000 });
        await searchBox.fill(st.productName);
        await page.waitForTimeout(900);

        const row = page
          .getByRole("row", { name: new RegExp(escapeRe(st.productName)) })
          .first();
        await row.waitFor({ state: "visible", timeout: 12_000 });
        await row.getByRole("button", { name: "Kiểm duyệt" }).click();

        const dlg = page.getByRole("dialog", { name: st.productName });
        await dlg.waitFor({ state: "visible", timeout: 10_000 });
        seg.cap(`[Admin] Duyệt đăng bán “${st.productName}”`);
        await dlg
          .getByRole("textbox", { name: /Lý do kiểm duyệt/i })
          .fill("Đạt tiêu chuẩn đăng bán.");
        await dlg.getByRole("button", { name: "Duyệt đăng bán" }).click();

        const ad = page.getByRole("alertdialog");
        if (await ad.isVisible({ timeout: 2_500 }).catch(() => false)) {
          await ad
            .getByRole("button", { name: /Xác nhận|Duyệt/i })
            .last()
            .click();
        }

        const sellerTok = await ensureSellerToken();
        await pollUntil(
          "Sản phẩm chuyển APPROVED qua API",
          async () => {
            const r = await gwJson(
              "GET",
              "/api/v1/seller/products?page_size=20",
              {
                token: sellerTok,
              },
            );
            const pr = (r.data?.items || []).find((x) => x.id === st.productId);
            return {
              ok: pr?.status === "APPROVED",
              value: pr,
              detail: `HTTP ${r.status} status=${pr?.status}`,
            };
          },
          20_000,
        );
        seg.cap("[Admin] Sản phẩm đã APPROVED");
        return `product=${st.productId} APPROVED`;
      },
    },
    {
      id: "s06",
      name: "journey:s06-seller-site-logo-publish",
      actor: "seller",
      title: "Người bán dựng site, tải logo và xuất bản",
      async fn(page, seg) {
        seg.cap("[Người bán] Mở Site Studio");
        await gotoPage(page, `${origin}/seller/site`);
        await page
          .getByRole("heading", { level: 1, name: /Site bán hàng/i })
          .waitFor({ state: "visible", timeout: 15_000 });

        // Set subdomain in tab "Tên miền"
        await page
          .getByRole("tab", { name: /^Tên miền/i })
          .first()
          .click();
        const subInput = page.getByLabel("Tên miền phụ");
        await subInput.waitFor({ state: "visible", timeout: 10_000 });
        await subInput.fill(st.sub);

        // Apply template "Xưởng thủ công"
        seg.cap("[Người bán] Chọn mẫu “Xưởng thủ công”");
        await page.getByRole("tab", { name: /^Mẫu/i }).first().click();
        const tplPanel = page.getByRole("tabpanel").first();
        const card = tplPanel
          .locator("div")
          .filter({
            has: page.getByRole("heading", {
              level: 2,
              name: "Xưởng thủ công",
            }),
          })
          .filter({
            has: page.getByRole("button", {
              name: /Dùng mẫu này|Áp dụng lại mẫu gốc/,
            }),
          })
          .last();
        await card.scrollIntoViewIfNeeded();
        await card
          .getByRole("button", { name: /Dùng mẫu này|Áp dụng lại mẫu gốc/ })
          .first()
          .click();
        const confirmTpl = page
          .getByRole("alertdialog")
          .getByRole("button", { name: /Xác nhận/i });
        if (
          await confirmTpl
            .first()
            .isVisible({ timeout: 4_000 })
            .catch(() => false)
        ) {
          await confirmTpl.first().click();
        }
        await page
          .getByText("Mẫu: Xưởng thủ công")
          .first()
          .waitFor({ state: "visible", timeout: 15_000 });

        // Upload logo in tab "Tuỳ chỉnh" -> "Phông chữ, Thương hiệu & Ảnh" -> "Ảnh logo"
        seg.cap("[Người bán] Tải Ảnh logo lên bằng SingleMediaUploadButton");
        await page
          .getByRole("tab", { name: /^Tuỳ chỉnh/i })
          .first()
          .click();
        const logoUrlInput = page.getByLabel("Ảnh logo");
        if (
          !(await logoUrlInput.isVisible({ timeout: 3_000 }).catch(() => false))
        ) {
          await page
            .getByRole("button", { name: /Phông chữ, Thương hiệu & Ảnh/i })
            .first()
            .click();
          await logoUrlInput.waitFor({ state: "visible", timeout: 8_000 });
        }
        await logoUrlInput.scrollIntoViewIfNeeded();
        const logoField = logoUrlInput.locator(
          "xpath=ancestor::*[.//input[@type='file']][1]",
        );
        await logoField
          .locator('input[type="file"]')
          .first()
          .setInputFiles(logoImgPath);

        const mediaRe = new RegExp(
          `^/media/shops/${escapeRe(st.shopId)}/[0-9a-f]{64}\\.(png|jpg|jpeg|webp)$`,
        );
        st.logoUrl = await pollUntil(
          "Ảnh logo cập nhật sang /media/shops/<shop_id>/<sha256>",
          async () => {
            const val = (await logoUrlInput.inputValue()).trim();
            return {
              ok: mediaRe.test(val),
              value: val,
              detail: `logo_url="${val}"`,
            };
          },
          20_000,
          400,
        );

        const draftBtn = page
          .getByRole("button", { name: /^Lưu nháp$/i })
          .first();
        if (
          (await draftBtn.isVisible().catch(() => false)) &&
          (await draftBtn.isEnabled().catch(() => false))
        ) {
          await draftBtn.click();
          await page.waitForTimeout(800);
        }

        seg.cap("[Người bán] Xuất bản site gian hàng");
        await page
          .getByRole("button", { name: /^Xuất bản$/i })
          .first()
          .click();
        const pubDlg = page.getByRole("dialog", { name: /Xuất bản/i });
        await pubDlg.waitFor({ state: "visible", timeout: 10_000 });
        const noteInput = pubDlg.getByLabel(/Ghi chú/i);
        if (await noteInput.isVisible().catch(() => false)) {
          await noteInput.fill("Bản đầu tiên");
        }
        await pubDlg
          .getByRole("button", {
            name: /Xác nhận xuất bản|Lưu nháp & Xuất bản/i,
          })
          .click();
        await pubDlg.waitFor({ state: "hidden", timeout: 25_000 });

        const sellerTok = await ensureSellerToken();
        const siteData = await pollUntil(
          "Site chuyển PUBLISHED với logo_url /media/shops/...",
          async () => {
            const r = await gwJson("GET", "/api/v1/seller/site", {
              token: sellerTok,
            });
            const site = r.data?.site;
            const pubLogo =
              site?.published_config?.theme?.logo_url ||
              site?.config?.theme?.logo_url ||
              "";
            return {
              ok:
                r.status === 200 &&
                (site?.status || "").toUpperCase() === "PUBLISHED" &&
                pubLogo === st.logoUrl,
              value: site,
              detail: `HTTP ${r.status} status=${site?.status} sub=${site?.subdomain} logo=${pubLogo}`,
            };
          },
          20_000,
        );
        if (siteData.subdomain) {
          st.sub = siteData.subdomain;
          st.siteUrl = siteOrigin(siteData.subdomain, port);
        }

        // Verify both product image and logo are served from <site host>/media/... as 200 image/*
        await assertMediaServed(
          page,
          `${st.siteUrl}${st.productImages[0]}`,
          "s04/s06 product image on site host",
        );
        await assertMediaServed(
          page,
          `${st.siteUrl}${st.logoUrl}`,
          "s06 site logo on site host",
        );

        // Open published storefront and verify <img src="/media/shops/..."> renders
        await gotoPage(page, `${st.siteUrl}/`);
        const logoImg = page.getByRole("img", { name: /^Logo /i }).first();
        await logoImg.waitFor({ state: "visible", timeout: 15_000 });
        const renderedSrc = await logoImg.getAttribute("src");
        assert(
          renderedSrc === st.logoUrl && renderedSrc.startsWith("/media/shops/"),
          `Trang site không hiển thị logo vừa tải: src="${renderedSrc}", kỳ vọng "${st.logoUrl}"`,
        );
        const natWidth = await logoImg.evaluate((el) => el.naturalWidth);
        assert(
          natWidth >= 64,
          `Ảnh logo trên site chưa tải xong (naturalWidth=${natWidth})`,
        );

        seg.cap(`[Người bán] Site đã xuất bản tại ${st.siteUrl}`);
        return `site=${st.siteUrl} logo=${st.logoUrl}`;
      },
    },
    {
      id: "s07",
      name: "journey:s07-buyer-register-cod-order",
      actor: "buyer",
      title: "Khách vào site, đăng ký, xác minh SĐT và mua COD",
      async fn(page, seg) {
        seg.cap(`[Khách] Vào site gian hàng ${st.siteUrl}`);
        await gotoPage(page, `${st.siteUrl}/`);

        seg.cap("[Khách] Đăng ký tài khoản trên site");
        await gotoPage(page, `${st.siteUrl}/dang-ky`);
        await page.getByLabel(/Họ và tên/i).fill(st.buyer.name);
        await page.getByLabel(/^Email$/i).fill(st.buyer.email);
        const phoneInput = page.getByLabel(/Số điện thoại/i);
        if (
          await phoneInput
            .first()
            .isVisible()
            .catch(() => false)
        ) {
          await phoneInput.first().fill(st.buyer.phone);
        }
        await page
          .getByLabel(/Mật khẩu \(|^Mật khẩu$/i)
          .first()
          .fill(st.buyer.password);
        await page.getByLabel(/Nhập lại mật khẩu/i).fill(st.buyer.password);

        const t0 = Date.now() - 5_000;
        await page
          .getByRole("button", { name: /Tạo tài khoản|Đăng ký/i })
          .last()
          .click();
        const otpInput = page.getByLabel(/Mã xác thực 6 chữ số/i);
        await otpInput.waitFor({ state: "visible", timeout: 20_000 });

        const otp = await fetchMailpitOtp(st.buyer.email, { afterMs: t0 });
        seg.cap("[Khách] Nhập OTP xác thực email");
        await otpInput.click();
        await page.keyboard.type(otp, { delay: 60 });
        const verifyBtn = page
          .getByRole("button", { name: /Xác (thực|nhận)/i })
          .first();
        if (
          (await verifyBtn.isVisible().catch(() => false)) &&
          (await verifyBtn.isEnabled().catch(() => false))
        ) {
          await verifyBtn.click();
        }
        await page.waitForURL((u) => !/\/dang-ky/.test(u.pathname), {
          timeout: 30_000,
        });
        if (/\/dang-nhap/.test(page.url())) {
          await page.getByLabel(/^Email$/i).fill(st.buyer.email);
          await page
            .getByLabel(/Mật khẩu/i)
            .first()
            .fill(st.buyer.password);
          await page
            .getByRole("button", { name: /Đăng nhập/i })
            .last()
            .click();
          await page.waitForURL((u) => !/\/dang-nhap/.test(u.pathname), {
            timeout: 30_000,
          });
        }
        await ensureBuyerToken();

        seg.cap(`[Khách] Chọn sản phẩm “${st.productName}” biến thể Men lam`);
        await gotoPage(page, `${st.siteUrl}/san-pham`);
        await page.getByRole("main").getByText(st.productName).first().click();
        await page
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible" });
        await page
          .getByText(/Màu men:/i)
          .first()
          .waitFor({ state: "visible", timeout: 8_000 });
        const menLamBtn = page
          .getByRole("radio", { name: "Men lam" })
          .or(page.getByRole("button", { name: "Men lam" }));
        await menLamBtn.first().click();

        const [addCartRes] = await Promise.all([
          page.waitForResponse(
            (r) =>
              r.url().includes("/api/cart") && r.request().method() === "POST",
            { timeout: 15_000 },
          ),
          page
            .getByRole("button", { name: /^Thêm vào giỏ$/i })
            .first()
            .click(),
        ]);
        assert(
          addCartRes.ok(),
          `Thêm vào giỏ thất bại: HTTP ${addCartRes.status()}`,
        );

        await gotoPage(page, `${st.siteUrl}/gio-hang`);
        await gotoPage(page, `${st.siteUrl}/thanh-toan`);
        await page
          .getByRole("heading", { level: 1, name: /Thanh toán/i })
          .waitFor({ state: "visible", timeout: 15_000 });

        const addNewBtn = page.getByRole("button", {
          name: /Thêm địa chỉ mới/i,
        });
        const recipientInput = page.getByLabel(/Họ tên|Người nhận/i);
        if (
          (await addNewBtn.isVisible().catch(() => false)) ||
          (await recipientInput
            .first()
            .isVisible()
            .catch(() => false))
        ) {
          seg.cap("[Khách] Thêm địa chỉ giao hàng");
          if (await addNewBtn.isVisible().catch(() => false))
            await addNewBtn.click();
          await recipientInput.first().fill(st.buyer.name);
          await page
            .getByLabel(/Số điện thoại/i)
            .first()
            .fill(st.buyer.phone);
          await selectCheckoutLocation(page);
          await page
            .getByLabel(/Địa chỉ chi tiết|Số nhà|Đường/i)
            .first()
            .fill("25 Lê Lợi");
          await page
            .getByRole("button", {
              name: /Lưu và giao tới địa chỉ này|Lưu địa chỉ|Sử dụng địa chỉ/i,
            })
            .first()
            .click();
          await page.waitForTimeout(1_000);
        }

        const editPayBtn = page.getByRole("button", {
          name: /Sửa phương thức thanh toán/i,
        });
        if (await editPayBtn.isVisible().catch(() => false))
          await editPayBtn.click();
        seg.cap("[Khách] Chọn thanh toán khi nhận hàng (COD)");
        await page
          .getByRole("radio", { name: /Thanh toán khi nhận hàng|COD/i })
          .first()
          .check();
        await page.waitForTimeout(500);

        const verifyPhoneLink = page.getByRole("link", {
          name: /Xác minh số điện thoại/i,
        });
        if (await verifyPhoneLink.isVisible().catch(() => false)) {
          seg.cap("[Khách] Xác minh SĐT bằng OTP để bật COD");
          await verifyPhoneLink.click();
          await page
            .waitForLoadState("networkidle", { timeout: 10_000 })
            .catch(() => {});
          const reg = page.getByRole("region", {
            name: "Xác minh số điện thoại",
          });
          await reg.waitFor({ state: "visible", timeout: 10_000 });
          await reg
            .getByRole("textbox", { name: "Số điện thoại nhận mã OTP" })
            .fill(st.buyer.phone);
          const sendBtn = reg.getByRole("button", { name: /^Gửi/i });
          await pollUntil(
            "Nút gửi OTP SĐT sẵn sàng",
            async () => ({ ok: await sendBtn.isEnabled().catch(() => false) }),
            70_000,
            1_000,
          );
          const phoneOtpT0 = Date.now() - 5_000;
          await sendBtn.click();
          const phoneOtp = await fetchMailpitOtp(st.buyer.email, {
            afterMs: phoneOtpT0,
          });
          const phoneOtpInput = page.getByRole("textbox", {
            name: /^Mã OTP 6 chữ số/i,
          });
          await phoneOtpInput.waitFor({ state: "visible", timeout: 10_000 });
          await phoneOtpInput.fill(phoneOtp);
          await page.getByRole("button", { name: "Xác minh SĐT" }).click();
          await page
            .getByText(/Đã xác minh/i)
            .first()
            .waitFor({ state: "visible", timeout: 15_000 });

          await gotoPage(page, `${st.siteUrl}/thanh-toan`);
          await page
            .getByRole("heading", { level: 1, name: /Thanh toán/i })
            .waitFor({ state: "visible", timeout: 15_000 });
          if (await editPayBtn.isVisible().catch(() => false))
            await editPayBtn.click();
          await page
            .getByRole("radio", { name: /Thanh toán khi nhận hàng|COD/i })
            .first()
            .check();
          await page.waitForTimeout(500);
        }

        seg.cap("[Khách] Đặt hàng COD");
        await page
          .getByRole("button", { name: /^Đặt hàng/i })
          .first()
          .click();
        await page.waitForURL(/\/don-hang\/([0-9a-f-]+)/i, { timeout: 30_000 });
        const m = page.url().match(/\/don-hang\/([^/?#]+)/);
        assert(
          Boolean(m?.[1]),
          `Không tách được mã đơn COD từ URL: ${page.url()}`,
        );
        st.codOrderId = m[1];

        const buyerTok = await ensureBuyerToken();
        const od = await gwJson("GET", `/api/v1/orders/${st.codOrderId}`, {
          token: buyerTok,
        });
        assert(
          od.status === 200 && od.data?.order?.id === st.codOrderId,
          `GET /api/v1/orders/${st.codOrderId} thất bại: HTTP ${od.status} ${od.text}`,
        );
        assert(
          (od.data.order.payment_method || "").toUpperCase() === "COD",
          `Đơn không phải COD: ${od.data.order.payment_method}`,
        );
        seg.cap(`[Khách] Đã đặt đơn COD ${st.codOrderId}`);
        return `codOrderId=${st.codOrderId} status=${od.data.order.status}`;
      },
    },
    {
      id: "s08",
      name: "journey:s08-seller-ship-cod",
      actor: "seller",
      title: "Người bán xác nhận và tạo vận đơn đơn COD",
      async fn(page, seg) {
        const res = await shipOrderViaUi(page, seg, st.codOrderId);
        st.codTrackingCode = res.trackingCode;
        st.codCarrierCode = res.carrierCode;
        return `tracking=${st.codTrackingCode}`;
      },
    },
    {
      id: "s09",
      name: "journey:s09-cod-deliver-complete-review",
      actor: "buyer",
      title: "Webhook vận chuyển giao COD, khách nhận hàng và đánh giá 5 sao",
      async fn(page, seg) {
        await deliverViaSignedWebhook(
          page,
          seg,
          st.codOrderId,
          st.codTrackingCode,
          st.codCarrierCode,
          "thu COD",
        );

        const buyerTok = await ensureBuyerToken();
        const recvBtn = page.getByRole("button", { name: "Đã nhận được hàng" });
        await recvBtn.waitFor({ state: "visible", timeout: 12_000 });
        seg.cap("[Khách] Xác nhận “Đã nhận được hàng” → COMPLETED");
        await recvBtn.click();
        const confirmAd = page
          .getByRole("alertdialog")
          .getByRole("button", { name: /Xác nhận|Đã nhận/i });
        if (
          await confirmAd
            .first()
            .isVisible({ timeout: 2_500 })
            .catch(() => false)
        ) {
          await confirmAd.last().click();
        }

        await pollUntil(
          `Đơn COD ${st.codOrderId} chuyển COMPLETED qua API`,
          async () => {
            const r = await gwJson("GET", `/api/v1/orders/${st.codOrderId}`, {
              token: buyerTok,
            });
            const status = (r.data?.order?.status || "").toUpperCase();
            return {
              ok: status === "COMPLETED",
              value: r.data?.order,
              detail: `HTTP ${r.status} status=${status}`,
            };
          },
          20_000,
        );

        await page.reload({ waitUntil: "domcontentloaded" });
        await page
          .waitForLoadState("networkidle", { timeout: 10_000 })
          .catch(() => {});

        seg.cap("[Khách] Viết đánh giá 5 sao");
        const stars = page.getByRole("radiogroup", { name: "Chọn số sao" });
        await stars.waitFor({ state: "visible", timeout: 12_000 });
        await stars.scrollIntoViewIfNeeded();
        await stars.getByRole("radio", { name: "5 ★" }).check();
        await page
          .getByRole("textbox", { name: "Nhận xét của bạn" })
          .fill("Men lam rất đẹp, đóng gói cẩn thận, giao nhanh.");
        await page.getByRole("button", { name: "Gửi đánh giá" }).click();

        const reviews = await pollUntil(
          `Đánh giá 5 sao được ghi nhận qua GET /api/v1/orders/${st.codOrderId}/reviews`,
          async () => {
            const r = await gwJson(
              "GET",
              `/api/v1/orders/${st.codOrderId}/reviews`,
              {
                token: buyerTok,
              },
            );
            const list = r.data?.reviews || [];
            return {
              ok:
                r.status === 200 && list.some((rv) => Number(rv.rating) === 5),
              value: list,
              detail: `HTTP ${r.status} count=${list.length}`,
            };
          },
          15_000,
        );

        // Wait for payment service to credit seller pending balance after order.completed.
        const sellerTok = await ensureSellerToken();
        const bal = await pollUntil(
          "Số dư pending_vnd của người bán > 0 sau khi đơn COD COMPLETED",
          async () => {
            const r = await gwJson("GET", "/api/v1/seller/balance", {
              token: sellerTok,
            });
            return {
              ok: r.status === 200 && Number(r.data?.pending_vnd) > 0,
              value: r.data,
              detail: `HTTP ${r.status} pending=${r.data?.pending_vnd}`,
            };
          },
          25_000,
          700,
        );
        seg.cap("[Khách] Hoàn tất đơn COD và đánh giá 5 sao");
        return `reviews=${reviews.length} sellerPending=${bal.pending_vnd}`;
      },
    },
    {
      id: "s10",
      name: "journey:s10-buyer-vietqr-order-webhook",
      actor: "buyer",
      title: "Khách mua lần 2 bằng VietQR + webhook ngân hàng",
      async fn(page, seg) {
        seg.cap("[Khách] Mua biến thể Men rạn và thanh toán bằng VietQR");
        await gotoPage(page, `${st.siteUrl}/san-pham`);
        await page.getByRole("main").getByText(st.productName).first().click();
        await page
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible" });
        const menRanBtn = page
          .getByRole("radio", { name: "Men rạn" })
          .or(page.getByRole("button", { name: "Men rạn" }));
        await menRanBtn.first().click();

        const [addCartRes] = await Promise.all([
          page.waitForResponse(
            (r) =>
              r.url().includes("/api/cart") && r.request().method() === "POST",
            { timeout: 15_000 },
          ),
          page
            .getByRole("button", { name: /^Thêm vào giỏ$/i })
            .first()
            .click(),
        ]);
        assert(
          addCartRes.ok(),
          `Thêm Men rạn vào giỏ thất bại: HTTP ${addCartRes.status()}`,
        );

        await gotoPage(page, `${st.siteUrl}/thanh-toan`);
        await page
          .getByRole("heading", { level: 1, name: /Thanh toán/i })
          .waitFor({ state: "visible", timeout: 15_000 });
        const editPayBtn = page.getByRole("button", {
          name: /Sửa phương thức thanh toán/i,
        });
        if (await editPayBtn.isVisible().catch(() => false))
          await editPayBtn.click();
        await page
          .getByRole("radio", { name: /VietQR|Chuyển khoản/i })
          .first()
          .check();
        await page.waitForTimeout(400);

        seg.cap("[Khách] Đặt hàng VietQR → trang chờ mã QR");
        await page
          .getByRole("button", { name: /^Đặt hàng/i })
          .first()
          .click();
        await page.waitForURL(/\/thanh-toan\/cho\/([^/?#]+)/, {
          timeout: 30_000,
        });
        const payIdMatch = page.url().match(/\/cho\/([^/?#]+)/);
        const orderQuery = new URL(page.url()).searchParams.get("order");
        assert(
          Boolean(payIdMatch?.[1]),
          `Không lấy được payment ID từ URL ${page.url()}`,
        );
        const payId = payIdMatch[1];

        const payInfo = await page.evaluate(async (id) => {
          const r = await fetch(`/api/payments/${encodeURIComponent(id)}`);
          return { status: r.status, text: (await r.text()).slice(0, 2000) };
        }, payId);
        let pay = null;
        try {
          pay = JSON.parse(payInfo.text)?.payment;
        } catch {
          // handled below
        }
        if (pay && !pay.id) pay.id = pay.payment_id;
        assert(
          Boolean(pay?.id),
          `Không đọc được payment ${payId}: HTTP ${payInfo.status} ${payInfo.text.slice(0, 200)}`,
        );
        const groupId = pay.order_group_id ?? pay.orderGroupId;
        const amountVnd = Number(pay.amount_vnd ?? pay.amountVnd);
        assert(amountVnd > 0, `Số tiền payment không hợp lệ: ${amountVnd}`);

        seg.cap("[Ngân hàng giả lập] Gửi webhook VietQR SUCCEEDED (HMAC)");
        const wh = await signedWebhook(
          `${GATEWAY_URL}/webhooks/payment`,
          PAY_WEBHOOK_SECRET,
          {
            provider: "vietqr_mock",
            provider_tx_id: `TXN-JR-${randomUUID()}`,
            payment_id: pay.id,
            order_group_id: groupId,
            amount_vnd: amountVnd,
            status: "SUCCEEDED",
            reference: `NEXUSMART ${groupId}`,
            occurred_at: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
          },
        );
        assert(
          wh.status >= 200 && wh.status < 300,
          `Webhook payment VietQR trả HTTP ${wh.status}: ${wh.text}`,
        );

        await page.waitForURL(/\/don-hang\//, { timeout: 30_000 });
        const ordMatch = page.url().match(/\/don-hang\/([^/?#]+)/);
        st.qrOrderId = ordMatch?.[1] || orderQuery || "";
        assert(
          Boolean(st.qrOrderId),
          `Không lấy được qrOrderId từ URL ${page.url()}`,
        );

        const buyerTok = await ensureBuyerToken();
        await pollUntil(
          `Đơn VietQR ${st.qrOrderId} chuyển CONFIRMED qua API`,
          async () => {
            const r = await gwJson("GET", `/api/v1/orders/${st.qrOrderId}`, {
              token: buyerTok,
            });
            const status = (r.data?.order?.status || "").toUpperCase();
            return {
              ok: status === "CONFIRMED",
              value: r.data?.order,
              detail: `HTTP ${r.status} status=${status}`,
            };
          },
          20_000,
        );
        seg.cap("[Khách] Thanh toán VietQR xác nhận tự động, đơn CONFIRMED");
        return `qrOrderId=${st.qrOrderId} amount=${amountVnd}`;
      },
    },
    {
      id: "s11",
      name: "journey:s11-seller-ship-vietqr",
      actor: "seller",
      title: "Người bán tạo vận đơn đơn VietQR",
      async fn(page, seg) {
        const res = await shipOrderViaUi(page, seg, st.qrOrderId);
        st.qrTrackingCode = res.trackingCode;
        st.qrCarrierCode = res.carrierCode;
        return `tracking=${st.qrTrackingCode}`;
      },
    },
    {
      id: "s12",
      name: "journey:s12-buyer-return-wizard-3steps",
      actor: "buyer",
      title:
        "Webhook giao đơn VietQR và khách gửi yêu cầu đổi trả (3 bước — F6)",
      async fn(page, seg) {
        await deliverViaSignedWebhook(
          page,
          seg,
          st.qrOrderId,
          st.qrTrackingCode,
          st.qrCarrierCode,
          "",
        );

        seg.cap("[Khách] Mở wizard Yêu cầu đổi trả");
        await page
          .getByRole("button", { name: /Yêu cầu đổi trả/i })
          .first()
          .click();
        const dlg = page.getByRole("dialog", { name: /Yêu cầu đổi trả/i });
        await dlg.waitFor({ state: "visible", timeout: 10_000 });

        // Step 1: reason + detail
        await dlg
          .getByText(/Bước 1 \/ 3/)
          .waitFor({ state: "visible", timeout: 5_000 });
        await radixSelect(page, dlg.getByLabel("Lý do đổi trả"), null, 0);
        await dlg
          .getByLabel(/Mô tả chi tiết tình trạng sản phẩm/i)
          .fill("Một chén bị mẻ miệng khi nhận hàng.");
        await dlg.getByRole("button", { name: "Tiếp theo" }).click();

        // Step 2: evidence URLs (optional) -> advance to step 3
        await dlg
          .getByText(/Bước 2 \/ 3/)
          .waitFor({ state: "visible", timeout: 5_000 });
        await dlg.getByRole("button", { name: "Tiếp theo" }).click();

        // Strict F6 gate: step 3 confirmation MUST stay open and NOT auto-submit on step 2 click!
        await page.waitForTimeout(400);
        assert(
          await dlg.isVisible().catch(() => false),
          "F6: Wizard đổi trả tự đóng và gửi luôn khi bấm Tiếp theo ở bước 2 (bỏ qua bước 3)",
        );
        await dlg
          .getByText(/Bước 3 \/ 3/)
          .waitFor({ state: "visible", timeout: 5_000 });
        const submitReturnBtn = dlg.getByRole("button", {
          name: "Gửi yêu cầu đổi trả",
        });
        await submitReturnBtn.waitFor({ state: "visible", timeout: 5_000 });

        seg.cap("[Khách] Xác nhận bước 3 → Gửi yêu cầu đổi trả");
        const returnResp = await clickAndAwaitBff(
          page,
          submitReturnBtn,
          `/api/orders/${st.qrOrderId}/return`,
          "POST",
        );
        assert(
          returnResp.ok(),
          `POST /api/orders/${st.qrOrderId}/return thất bại: HTTP ${returnResp.status()} ${(await returnResp.text().catch(() => "")).slice(0, 250)}`,
        );
        await dlg.waitFor({ state: "hidden", timeout: 20_000 });

        const buyerTok = await ensureBuyerToken();
        const ret = await pollUntil(
          `Yêu cầu trả hàng của đơn ${st.qrOrderId} ghi nhận qua API`,
          async () => {
            const r = await gwJson(
              "GET",
              `/api/v1/orders/${st.qrOrderId}/return`,
              {
                token: buyerTok,
              },
            );
            const reqObj = r.data?.return_request;
            return {
              ok: r.status === 200 && Boolean(reqObj?.id),
              value: reqObj,
              detail: `HTTP ${r.status} status=${reqObj?.status}`,
            };
          },
          15_000,
        );
        seg.cap(`[Khách] Yêu cầu trả hàng trạng thái ${ret.status}`);
        return `returnId=${ret.id} status=${ret.status}`;
      },
    },
    {
      id: "s13",
      name: "journey:s13-seller-instant-refund",
      actor: "seller",
      title: "Người bán hoàn tiền ngay cho yêu cầu trả hàng",
      async fn(page, seg) {
        seg.cap("[Người bán] Mở danh sách Trả hàng → xem chi tiết");
        await gotoPage(page, `${origin}/seller/tra-hang`);
        const detailBtn = page
          .getByRole("button", { name: "Chi tiết" })
          .first();
        await detailBtn.waitFor({ state: "visible", timeout: 12_000 });
        await detailBtn.click();

        const dlg = page.getByRole("dialog", { name: /Yêu cầu trả hàng/i });
        await dlg.waitFor({ state: "visible", timeout: 10_000 });
        seg.cap("[Người bán] Phản hồi và bấm “Hoàn tiền ngay”");
        await dlg
          .getByRole("textbox", { name: /Ghi chú phản hồi/i })
          .fill("Xin lỗi chị, shop hoàn tiền ngay, không cần gửi lại hàng.");
        await dlg.getByRole("button", { name: "Hoàn tiền ngay" }).click();

        const confirmAd = page.getByRole("alertdialog");
        if (await confirmAd.isVisible({ timeout: 2_500 }).catch(() => false)) {
          await confirmAd
            .getByRole("button", { name: /Xác nhận|Hoàn tiền/i })
            .last()
            .click();
        }

        const buyerTok = await ensureBuyerToken();
        const refundedOrder = await pollUntil(
          `Đơn VietQR ${st.qrOrderId} chuyển payment_status=REFUNDED qua API`,
          async () => {
            const r = await gwJson("GET", `/api/v1/orders/${st.qrOrderId}`, {
              token: buyerTok,
            });
            const o = r.data?.order ?? {};
            return {
              ok: (o.payment_status || "").toUpperCase() === "REFUNDED",
              value: o,
              detail: `HTTP ${r.status} status=${o.status} payment_status=${o.payment_status}`,
            };
          },
          25_000,
          800,
        );
        seg.cap("[Người bán] Hoàn tiền thành công (payment_status=REFUNDED)");
        return `order=${st.qrOrderId} status=${refundedOrder.status} payment=${refundedOrder.payment_status}`;
      },
    },
    {
      id: "s14",
      name: "journey:s14-admin-reconciliation-check",
      actor: "admin",
      title: "Admin mở đối soát và kiểm tra số dư + sổ cái trước giải ngân",
      async fn(page, seg) {
        seg.cap("[Admin] Mở trang Đối soát tài chính");
        await gotoPage(page, `${origin}/admin/doi-soat`);
        await page
          .getByRole("heading", { level: 1, name: /Đối soát/i })
          .waitFor({ state: "visible", timeout: 15_000 });

        const sellerTok = await ensureSellerToken();
        const balRes = await gwJson("GET", "/api/v1/seller/balance", {
          token: sellerTok,
        });
        assert(
          balRes.status === 200 && Number(balRes.data?.pending_vnd) > 0,
          `Số dư pending_vnd trước giải ngân phải > 0: HTTP ${balRes.status} ${balRes.text}`,
        );
        assert(
          Number(balRes.data?.available_vnd) === 0 &&
            Number(balRes.data?.payout_pending_vnd) === 0,
          `Kỳ vọng available=0 & payout_pending=0 trước giải ngân: ${balRes.text}`,
        );

        const ledgerRes = await gwJson(
          "GET",
          "/api/v1/seller/ledger?page=1&page_size=20",
          {
            token: sellerTok,
          },
        );
        assert(
          ledgerRes.status === 200 &&
            Array.isArray(ledgerRes.data?.items) &&
            ledgerRes.data.items.length > 0,
          `Sổ cái người bán phải có bút toán: HTTP ${ledgerRes.status} ${ledgerRes.text}`,
        );
        return `pending=${balRes.data.pending_vnd} ledgerEntries=${ledgerRes.data.items.length}`;
      },
    },
    {
      id: "s15",
      name: "journey:s15-seller-bank-account-otp",
      actor: "seller",
      title: "Người bán khai tài khoản ngân hàng + xác thực OTP Mailpit",
      async fn(page, seg) {
        seg.cap("[Người bán] Mở Tài chính → tab Tài khoản ngân hàng");
        await gotoPage(page, `${origin}/seller/tai-chinh`);
        await page
          .getByRole("heading", { level: 1, name: /Tài chính/i })
          .waitFor({ state: "visible", timeout: 15_000 });

        await page
          .getByRole("tab", { name: /Tài khoản ngân hàng/i })
          .first()
          .click();
        await page.getByRole("button", { name: "Liên kết ngân hàng" }).click();

        const bankForm = page.getByRole("form", {
          name: "Biểu mẫu liên kết tài khoản ngân hàng",
        });
        await bankForm.waitFor({ state: "visible", timeout: 10_000 });
        seg.cap("[Người bán] Điền số tài khoản và tên chủ TK khớp chủ shop");
        await bankForm
          .getByLabel(/Số tài khoản/i)
          .fill(st.seller.bankAccountNumber);
        await bankForm
          .getByLabel(/Tên chủ tài khoản/i)
          .fill(st.seller.bankHolder);

        const t0 = Date.now() - 5_000;
        await bankForm
          .getByRole("button", { name: "Lưu & gửi mã OTP" })
          .click();

        const otpForm = page.getByRole("form", {
          name: "Xác thực OTP tài khoản ngân hàng",
        });
        await otpForm.waitFor({ state: "visible", timeout: 20_000 });

        seg.cap(
          "[Hộp thư] Lấy OTP ngân hàng từ Mailpit (tiêu đề chứa “ngân hàng”)",
        );
        const otp = await fetchMailpitOtp(st.seller.email, {
          afterMs: t0,
          subjectPattern: /ngân hàng/i,
        });
        await otpForm.getByLabel(/Mã OTP 6 chữ số/i).fill(otp);
        await otpForm.getByRole("button", { name: "Xác thực OTP" }).click();
        await otpForm.waitFor({ state: "hidden", timeout: 20_000 });

        const sellerTok = await ensureSellerToken();
        const bankRes = await gwJson("GET", "/api/v1/seller/bank-account", {
          token: sellerTok,
        });
        const ba = bankRes.data?.bank_account;
        assert(
          bankRes.status === 200 && ba?.status === "VERIFIED",
          `GET /api/v1/seller/bank-account kỳ vọng VERIFIED, nhận HTTP ${bankRes.status} status=${ba?.status} body=${bankRes.text}`,
        );
        assert(
          ba.holder_name_matched === true,
          `Kỳ vọng holder_name_matched=true cho "${st.seller.bankHolder}", nhận ${ba.holder_name_matched}`,
        );
        assert(
          !ba.payout_locked_until,
          `Tài khoản ngân hàng đầu tiên không được bị khóa 24h: payout_locked_until=${ba.payout_locked_until}`,
        );
        seg.cap("[Người bán] Tài khoản ngân hàng đã VERIFIED");
        return `bank=${ba.bank_code} ${ba.account_number_masked} VERIFIED`;
      },
    },
    {
      id: "s16",
      name: "journey:s16-admin-early-escrow-release",
      actor: "admin",
      title: "Admin giải ngân sớm đơn COD đã COMPLETED",
      async fn(page, seg) {
        const sellerTok = await ensureSellerToken();
        const beforeRes = await gwJson("GET", "/api/v1/seller/balance", {
          token: sellerTok,
        });
        assert(
          beforeRes.status === 200 && Number(beforeRes.data?.pending_vnd) > 0,
          `Số dư pending trước giải ngân không hợp lệ: ${beforeRes.text}`,
        );
        const beforeAvail = Number(beforeRes.data.available_vnd);
        const beforePending = Number(beforeRes.data.pending_vnd);

        seg.cap("[Admin] Mở Đối soát → thẻ Giải ngân sớm");
        await gotoPage(page, `${origin}/admin/doi-soat`);
        await page
          .getByRole("heading", { level: 2, name: "Giải ngân sớm" })
          .waitFor({ state: "visible", timeout: 15_000 });

        await page
          .getByLabel("Mã đơn hàng", { exact: true })
          .fill(st.codOrderId);
        await page.getByRole("button", { name: /^Giải ngân$/ }).click();

        const ad = page.getByRole("alertdialog", {
          name: "Xác nhận giải ngân sớm",
        });
        await ad.waitFor({ state: "visible", timeout: 10_000 });
        seg.cap(`[Admin] Xác nhận giải ngân sớm đơn ${st.codOrderId}`);

        const [releaseResp] = await Promise.all([
          page.waitForResponse(
            (r) =>
              r
                .url()
                .includes(
                  `/api/admin/orders/${st.codOrderId}/escrow-release`,
                ) && r.request().method() === "POST",
            { timeout: 20_000 },
          ),
          ad.getByRole("button").filter({ hasNotText: /Hủy/ }).last().click(),
        ]);
        assert(
          releaseResp.status() === 200,
          `POST escrow-release trả HTTP ${releaseResp.status()}: ${await releaseResp.text().catch(() => "")}`,
        );
        const releaseJson = await releaseResp.json();
        await ad.waitFor({ state: "hidden", timeout: 15_000 });

        const releasedAmount = Number(releaseJson?.released_amount_vnd ?? 0);
        assert(
          releaseJson?.released === true && releasedAmount > 0,
          `Kết quả giải ngân sớm không hợp lệ: ${JSON.stringify(releaseJson)}`,
        );
        st.releasedAmountVnd = releasedAmount;

        const afterRes = await gwJson("GET", "/api/v1/seller/balance", {
          token: sellerTok,
        });
        assert(
          afterRes.status === 200,
          `GET seller/balance thất bại: ${afterRes.text}`,
        );
        const afterAvail = Number(afterRes.data.available_vnd);
        const afterPending = Number(afterRes.data.pending_vnd);
        assert(
          afterAvail === beforeAvail + releasedAmount,
          `available_vnd phải tăng đúng ${releasedAmount}: trước=${beforeAvail}, sau=${afterAvail}`,
        );
        assert(
          afterPending === beforePending - releasedAmount,
          `pending_vnd phải giảm đúng ${releasedAmount}: trước=${beforePending}, sau=${afterPending}`,
        );

        const availLedger = await gwJson(
          "GET",
          "/api/v1/seller/ledger?account_filter=available&page=1&page_size=20",
          { token: sellerTok },
        );
        assert(
          availLedger.status === 200 &&
            (availLedger.data?.items || []).some(
              (e) =>
                Number(e.credit_vnd) === releasedAmount ||
                Number(e.delta_vnd) === releasedAmount,
            ),
          `Sổ cái available không có bút toán giải ngân ${releasedAmount}: ${availLedger.text}`,
        );

        seg.cap(
          `[Admin] Đã giải ngân sớm ${releasedAmount}₫ vào số dư khả dụng`,
        );
        return `released=${releasedAmount} available=${afterAvail} pending=${afterPending}`;
      },
    },
    {
      id: "s17",
      name: "journey:s17-seller-withdraw-available",
      actor: "seller",
      title: "Người bán rút toàn bộ số dư khả dụng",
      async fn(page, seg) {
        const sellerTok = await ensureSellerToken();
        const balBefore = await gwJson("GET", "/api/v1/seller/balance", {
          token: sellerTok,
        });
        const avail = Number(balBefore.data?.available_vnd ?? 0);
        assert(avail > 0, `Không có số dư khả dụng để rút: ${balBefore.text}`);
        st.withdrawAmountVnd = avail;

        seg.cap(`[Người bán] Mở Tài chính → bấm Rút tiền (${avail}₫)`);
        await gotoPage(page, `${origin}/seller/tai-chinh`);
        await page
          .getByRole("button", { name: /^Rút tiền$/ })
          .first()
          .click();

        const dlg = page.getByRole("dialog", {
          name: "Rút tiền về tài khoản ngân hàng",
        });
        await dlg.waitFor({ state: "visible", timeout: 10_000 });
        await dlg.getByLabel(/Số tiền muốn rút/i).fill(String(avail));
        await dlg.getByRole("button", { name: "Gửi yêu cầu rút tiền" }).click();
        await dlg.waitFor({ state: "hidden", timeout: 20_000 });

        const payoutsRes = await gwJson(
          "GET",
          "/api/v1/seller/payouts?page=1&page_size=10",
          {
            token: sellerTok,
          },
        );
        assert(
          payoutsRes.status === 200,
          `GET seller/payouts thất bại: ${payoutsRes.text}`,
        );
        const pendingPayout = (payoutsRes.data?.items || []).find(
          (p) =>
            (p.status || "").toUpperCase() === "PENDING" &&
            Number(p.amount_vnd) === avail,
        );
        assert(
          Boolean(pendingPayout?.id),
          `Không thấy payout PENDING ${avail}₫ trong GET /api/v1/seller/payouts: ${payoutsRes.text}`,
        );
        st.payoutId = pendingPayout.id;

        const balAfter = await gwJson("GET", "/api/v1/seller/balance", {
          token: sellerTok,
        });
        assert(
          balAfter.status === 200 &&
            Number(balAfter.data?.available_vnd) === 0 &&
            Number(balAfter.data?.payout_pending_vnd) === avail,
          `Số dư sau rút sai (cần available=0, payout_pending=${avail}): ${balAfter.text}`,
        );
        seg.cap(`[Người bán] Đã tạo lệnh rút ${avail}₫ (PENDING)`);
        return `payoutId=${st.payoutId} amount=${avail} PENDING`;
      },
    },
    {
      id: "s18",
      name: "journey:s18-admin-approve-payout",
      actor: "admin",
      title: "Admin duyệt chi trả người bán và kiểm sổ dư cuối",
      async fn(page, seg) {
        seg.cap("[Admin] Mở Đối soát → tab Chi trả người bán");
        await gotoPage(page, `${origin}/admin/doi-soat`);
        await page
          .getByRole("tab", { name: /Chi trả người bán/i })
          .first()
          .click();

        const payoutsPanel = page.getByRole("tabpanel").first();
        const shopFilter = payoutsPanel
          .getByRole("searchbox", {
            name: /Lọc chi trả theo mã gian hàng/i,
          })
          .or(
            payoutsPanel.getByRole("textbox", {
              name: /Lọc chi trả theo mã gian hàng/i,
            }),
          )
          .first();
        await shopFilter.waitFor({ state: "visible", timeout: 10_000 });
        await shopFilter.fill(st.shopId);
        await payoutsPanel.getByRole("button", { name: /^Lọc$/ }).click();

        const row = payoutsPanel
          .getByRole("row", { name: new RegExp(escapeRe(st.shopId)) })
          .first();
        await row.waitFor({ state: "visible", timeout: 12_000 });
        seg.cap(`[Admin] Duyệt chi trả cho gian hàng ${st.shopId}`);
        await row.getByRole("button", { name: /^Duyệt$/ }).click();

        const ad = page.getByRole("alertdialog", {
          name: "Xác nhận duyệt chi trả người bán",
        });
        await ad.waitFor({ state: "visible", timeout: 10_000 });
        await ad.getByRole("button", { name: "Xác nhận duyệt" }).click();
        await ad.waitFor({ state: "hidden", timeout: 20_000 });

        const adminTok = await ensureAdminToken();
        const completed = await pollUntil(
          `Payout ${st.payoutId} chuyển COMPLETED với transfer_ref MOCKBANK-*`,
          async () => {
            const r = await gwJson(
              "GET",
              `/api/v1/admin/payouts?shop_id=${encodeURIComponent(st.shopId)}&page=1&page_size=10`,
              { token: adminTok },
            );
            const po = (r.data?.items || []).find((p) => p.id === st.payoutId);
            return {
              ok:
                r.status === 200 &&
                (po?.status || "").toUpperCase() === "COMPLETED" &&
                typeof po?.transfer_ref === "string" &&
                po.transfer_ref.startsWith("MOCKBANK-"),
              value: po,
              detail: `HTTP ${r.status} status=${po?.status} ref=${po?.transfer_ref}`,
            };
          },
          20_000,
        );

        const sellerTok = await ensureSellerToken();
        const finalBal = await gwJson("GET", "/api/v1/seller/balance", {
          token: sellerTok,
        });
        assert(
          finalBal.status === 200 &&
            Number(finalBal.data?.payout_pending_vnd) === 0 &&
            Number(finalBal.data?.available_vnd) === 0,
          `Số dư cuối kỳ vọng payout_pending=0, available=0: ${finalBal.text}`,
        );

        const finalLedger = await gwJson(
          "GET",
          "/api/v1/seller/ledger?page=1&page_size=30",
          {
            token: sellerTok,
          },
        );
        assert(
          finalLedger.status === 200 &&
            (finalLedger.data?.items || []).some(
              (e) =>
                e.reference_id === st.payoutId ||
                /payout/i.test(String(e.reference_type || "")),
            ),
          `Sổ cái người bán thiếu bút toán chi trả payout ${st.payoutId}: ${finalLedger.text}`,
        );

        seg.cap(`[Admin] Chi trả COMPLETED (${completed.transfer_ref})`);
        return `payout=${st.payoutId} ref=${completed.transfer_ref} payoutPending=0`;
      },
    },
  ];

  // If an earlier journey step fails, fail fast on dependent steps instead of cascading timeouts.
  let failedStepId = null;

  const flows = STEP_DEFS.map((step) => ({
    name: step.name,
    fn: async () => {
      if (failedStepId) {
        throw new Error(
          `Dừng chuỗi vì bước trước (${failedStepId}) đã thất bại`,
        );
      }
      const ctx = await getActorContext(step.actor);
      const page = await ctx.newPage();
      const seg = new Seg(step.id, step.title, recordVideo);
      let note = "";
      try {
        note = await step.fn(page, seg);
      } catch (err) {
        failedStepId = step.id;
        seg.cap(
          `[LỖI] ${step.title}: ${String(err?.message || err).slice(0, 90)}`,
        );
        if (recordVideo) {
          await page
            .screenshot({
              path: path.join(videoDir, `fail-${step.id}.png`),
              fullPage: false,
            })
            .catch(() => {});
        }
        throw err;
      } finally {
        const totalSec = (Date.now() - seg.t0) / 1000;
        const vidObj = recordVideo ? page.video() : null;
        await page.close().catch(() => {});
        if (vidObj) {
          const vidPath = await vidObj.path().catch(() => null);
          if (vidPath && fs.existsSync(vidPath)) {
            const base = path.join(videoDir, step.id);
            fs.renameSync(vidPath, `${base}.webm`);
            fs.writeFileSync(`${base}.srt`, seg.srt(totalSec));
          }
        }
      }
      return note;
    },
  }));

  const cleanup = async () => {
    for (const c of Object.values(ctxs)) {
      await c.close().catch(() => {});
    }
    fs.rmSync(tmpMediaDir, { recursive: true, force: true });
  };

  return { flows, cleanup };
}
