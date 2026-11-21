#!/usr/bin/env node
import http from "node:http";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";

const PORT = 3300;
const OUT = resolve(".taste-skill/inspect-p4");

function req(host, method, path, { cookie = "", body = null, headers = {} } = {}) {
  return new Promise((resolveReq, rejectReq) => {
    const payload = body == null ? null : JSON.stringify(body);
    const h = {
      host: `${host}:${PORT}`,
      accept: "application/json",
      ...headers,
    };
    if (method !== "GET" && method !== "HEAD") {
      h.origin = `http://${host}:${PORT}`;
      h.referer = `http://${host}:${PORT}/`;
    }
    if (cookie) h.cookie = cookie;
    if (payload != null) {
      h["content-type"] = "application/json";
      h["content-length"] = String(Buffer.byteLength(payload));
    }
    const r = http.request({ host: "127.0.0.1", port: PORT, method, path, headers: h }, (res) => {
      const chunks = [];
      res.on("data", (c) => chunks.push(c));
      res.on("end", () => {
        const text = Buffer.concat(chunks).toString("utf8");
        let data = null;
        try {
          data = JSON.parse(text);
        } catch {}
        const setCookies = [].concat(res.headers["set-cookie"] || []);
        const nextCookie = mergeCookies(cookie, setCookies);
        resolveReq({ status: res.statusCode, headers: res.headers, text, data, cookie: nextCookie });
      });
    });
    r.on("error", rejectReq);
    if (payload != null) r.write(payload);
    r.end();
  });
}

function mergeCookies(existing, setCookies) {
  const m = new Map(
    String(existing || "")
      .split(/;\s*/)
      .filter(Boolean)
      .map((kv) => {
        const i = kv.indexOf("=");
        return [kv.slice(0, i), kv.slice(i + 1)];
      }),
  );
  for (const sc of setCookies) {
    const [kv] = sc.split(";");
    const i = kv.indexOf("=");
    if (i > 0) {
      const k = kv.slice(0, i);
      const v = kv.slice(i + 1);
      if (/max-age=0/i.test(sc) || !v) m.delete(k);
      else m.set(k, v);
    }
  }
  return [...m].map(([k, v]) => `${k}=${v}`).join("; ");
}

async function login(host, email, password) {
  const res = await req(host, "POST", "/api/auth/login", { body: { email, password } });
  if (res.status !== 200 || !res.cookie) {
    throw new Error(`Login failed on ${host} for ${email}: HTTP ${res.status} ${res.text}`);
  }
  return res.cookie;
}

const SITES = [
  {
    host: "bach-dang.nexusmart.localhost",
    skus: [
      "0b2f2784-4018-4f73-9e0b-e90dad77c92f", // Áo thun Cotton Compact 220gsm Trắng
      "5102d364-6f46-4993-8a04-81f43c246352", // Combo 3 Quần lót Boxer Sợi Modal
    ],
  },
  {
    host: "sai-gon.nexusmart.localhost",
    skus: [
      "0679d181-ea3f-4b51-92f2-16016f1e1fbc", // Áo thun Thể thao Dry-Fit Xanh cổ vịt
      "1d90dc13-a687-4f04-bf92-a555adc13991", // Áo khoác Gió nhẹ gấp gọn Xám tro
    ],
  },
];

const buyerJar = {};

for (const s of SITES) {
  let cookie = await login(s.host, "buyer@nexusmart.local", "BuyerDemo123!");
  const ordersRes = await req(s.host, "GET", "/api/orders", { cookie });
  cookie = ordersRes.cookie;
  const orderList = ordersRes.data?.orders || ordersRes.data?.items || [];
  console.log(`[${s.host}] existing orders: ${orderList.length} (HTTP ${ordersRes.status})`);

  if (orderList.length === 0) {
    // Ensure address exists
    const addrRes = await req(s.host, "GET", "/api/checkout/addresses", { cookie });
    cookie = addrRes.cookie;
    const addresses = addrRes.data?.addresses || addrRes.data?.items || [];
    let addressId = addresses[0]?.id;
    if (!addressId) {
      const createAddr = await req(s.host, "POST", "/api/auth/addresses", {
        cookie,
        body: {
          recipient_name: "Nguyễn Minh Quân",
          phone: "0901234501",
          province_code: "79",
          province_name: "TP. Hồ Chí Minh",
          ward_code: "26734",
          ward_name: "Phường Bến Nghé",
          street_line: "45 Lê Lợi",
          is_default: true,
        },
      });
      cookie = createAddr.cookie;
      addressId = createAddr.data?.address?.id || createAddr.data?.id;
      console.log(`[${s.host}] created address:`, createAddr.status, addressId);
    }

    // Add 1 item and place 1 real COD order so /don-hang shows real order data
    const addRes = await req(s.host, "POST", "/api/cart/items", {
      cookie,
      body: { sku_id: s.skus[0], quantity: 1 },
    });
    cookie = addRes.cookie;
    const idem = `inspect-p4-${s.host.split(".")[0]}-${Date.now()}`;
    const coRes = await req(s.host, "POST", "/api/checkout", {
      cookie,
      headers: { "idempotency-key": idem },
      body: {
        address_id: addressId,
        payment_method: "COD",
        shipping_choice: "STANDARD",
      },
    });
    cookie = coRes.cookie;
    console.log(`[${s.host}] placed 1 COD order:`, coRes.status, coRes.text.slice(0, 200));
  }

  // Ensure cart has 2 items for /gio-hang and /thanh-toan screenshots
  const cartBefore = await req(s.host, "GET", "/api/cart", { cookie });
  cookie = cartBefore.cookie;
  const existingItems = cartBefore.data?.items || [];
  console.log(`[${s.host}] cart before add: ${existingItems.length} item(s)`);
  for (const skuId of s.skus) {
    if (!existingItems.some((it) => (it.sku_id || it.skuId) === skuId)) {
      const addRes = await req(s.host, "POST", "/api/cart/items", {
        cookie,
        body: { sku_id: skuId, quantity: 1 },
      });
      cookie = addRes.cookie;
      console.log(`[${s.host}] added SKU ${skuId.slice(0, 8)}:`, addRes.status);
    }
  }
  const cartAfter = await req(s.host, "GET", "/api/cart", { cookie });
  cookie = cartAfter.cookie;
  const ordersAfter = await req(s.host, "GET", "/api/orders", { cookie });
  cookie = ordersAfter.cookie;
  console.log(
    `[${s.host}] FINAL cart items=${(cartAfter.data?.items || []).length}, orders=${(ordersAfter.data?.orders || ordersAfter.data?.items || []).length}`,
  );
  buyerJar[s.host] = cookie;
}

const sellerCookie = await login("nexusmart.localhost", "seller@nexusmart.local", "SellerDemo123!");
const adminCookie = await login("nexusmart.localhost", "admin@nexusmart.local", "AdminDevPassword123!");

writeFileSync(`${OUT}/jar-buyer.json`, JSON.stringify(buyerJar, null, 2));
writeFileSync(`${OUT}/jar-seller.json`, JSON.stringify({ "nexusmart.localhost": sellerCookie }, null, 2));
writeFileSync(`${OUT}/jar-admin.json`, JSON.stringify({ "nexusmart.localhost": adminCookie }, null, 2));
console.log("Wrote jar-buyer.json, jar-seller.json, jar-admin.json");
