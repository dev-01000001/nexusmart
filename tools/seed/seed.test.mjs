import assert from "node:assert/strict";
import crypto from "node:crypto";
import http from "node:http";
import test from "node:test";

test("P1-3: listAllAdminProducts queries /api/v1/admin/products and returns non-APPROVED products across pages", async () => {
  const allProducts = [
    { id: "p-1", shop_id: "s-1", name: "Ao Thun", slug: "ao-thun", status: "APPROVED" },
    { id: "p-2", shop_id: "s-1", name: "Ao Polo", slug: "ao-polo", status: "PENDING" },
    { id: "p-3", shop_id: "s-1", name: "Quan Short", slug: "quan-short", status: "HIDDEN" },
    { id: "p-4", shop_id: "s-1", name: "Quan Jogger", slug: "quan-jogger", status: "REJECTED" },
  ];
  const requestedPaths = [];

  const server = http.createServer((req, res) => {
    const parsed = new URL(req.url, "http://127.0.0.1");
    requestedPaths.push(parsed.pathname);

    if (parsed.pathname === "/api/v1/products") {
      // CAT-B2: public endpoint strips auth and returns APPROVED/OUT_OF_STOCK only.
      const pub = allProducts.filter((p) => p.status === "APPROVED");
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ items: pub, page: 1, page_size: 100, total: pub.length }));
      return;
    }

    if (parsed.pathname === "/api/v1/admin/products") {
      if (req.headers.authorization !== "Bearer admin-jwt-test") {
        res.writeHead(401, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: { code: "UNAUTHENTICATED" } }));
        return;
      }
      const page = Number(parsed.searchParams.get("page") || "1");
      // Return 2 items per page to exercise pagination against gateway response shape.
      const slice = allProducts.slice((page - 1) * 2, page * 2);
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          items: slice,
          page,
          page_size: Number(parsed.searchParams.get("page_size") || "100"),
          total: allProducts.length,
        })
      );
      return;
    }

    res.writeHead(404);
    res.end();
  });

  await new Promise((resolve, reject) => {
    server.on("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  try {
    const { port } = server.address();
    process.env.SEED_GATEWAY_URL = `http://127.0.0.1:${port}`;
    const { listAllAdminProducts } = await import(`./seed.mjs?t=${Date.now()}`);
    const products = await listAllAdminProducts("admin-jwt-test");

    assert.ok(
      requestedPaths.every((p) => p === "/api/v1/admin/products"),
      `expected only /api/v1/admin/products calls, got: ${requestedPaths.join(", ")}`
    );
    assert.equal(
      products.length,
      4,
      `expected all 4 products including non-APPROVED, got ${products.length}`
    );
    assert.deepEqual(
      products.map((p) => p.status),
      ["APPROVED", "PENDING", "HIDDEN", "REJECTED"]
    );
  } finally {
    delete process.env.SEED_GATEWAY_URL;
    await new Promise((resolve) => server.close(resolve));
  }
});

test("PRM-D1: computeFlashSaleRegistration selects highest available_stock active SKU and clamps total_slots <= available_stock", async () => {
  const { computeFlashSaleRegistration } = await import(`./seed.mjs?prm=${Date.now()}`);
  assert.equal(typeof computeFlashSaleRegistration, "function");

  const product = {
    id: "prod-1",
    skus: [
      { id: "sku-2xl", price_vnd: 179000, min_price_30d_vnd: 179000, stock: 5, reserved_stock: 1, available_stock: 4, is_active: true },
      { id: "sku-inactive", price_vnd: 179000, min_price_30d_vnd: 179000, stock: 100, reserved_stock: 0, available_stock: 100, is_active: false },
      { id: "sku-m", price_vnd: 179000, min_price_30d_vnd: 179000, stock: 34, reserved_stock: 4, available_stock: 30, is_active: true },
      { id: "sku-l", price_vnd: 179000, min_price_30d_vnd: 179000, stock: 26, reserved_stock: 0, available_stock: 26, is_active: true },
    ],
  };

  // Quota exceeds stock (120 > 30): must pick sku-m and clamp total_slots to 30.
  const reg = computeFlashSaleRegistration(product, {
    flashPrice: 149000,
    totalQuota: 120,
    limitPerBuyer: 2,
  });
  assert.equal(reg.sku.id, "sku-m");
  assert.equal(reg.total_slots, 30);
  assert.equal(reg.per_user_limit, 2);
  assert.equal(reg.flash_price_vnd, 149000);

  // Low stock SKU where per_user_limit > available_stock and flashPrice >= min_price_30d_vnd.
  const lowStockProd = {
    id: "prod-2",
    skus: [
      { id: "sku-low", price_vnd: 180000, min_price_30d_vnd: 150000, stock: 2, reserved_stock: 0, available_stock: 2, is_active: true },
    ],
  };
  const regLow = computeFlashSaleRegistration(lowStockProd, {
    flashPrice: 155000,
    totalQuota: 50,
    limitPerBuyer: 5,
  });
  assert.equal(regLow.sku.id, "sku-low");
  assert.equal(regLow.total_slots, 2);
  assert.equal(regLow.per_user_limit, 2);
  assert.ok(regLow.flash_price_vnd < 150000, `expected flash_price_vnd < 150000, got ${regLow.flash_price_vnd}`);
  assert.ok(Number.isInteger(regLow.flash_price_vnd) && regLow.flash_price_vnd > 0);
});

test("CAT-D3: buildCategorySeedPlan aligns root commission_rate_bps with shops and includes all 15 child categories", async () => {
  const { buildCategorySeedPlan } = await import(`./seed.mjs?cat=${Date.now()}`);
  assert.equal(typeof buildCategorySeedPlan, "function");

  const { CATEGORIES, PRODUCTS } = await import("../../web/src/data/products.ts");
  const { SHOPS } = await import("../../web/src/data/shops.ts");

  const plan = buildCategorySeedPlan(CATEGORIES, PRODUCTS, SHOPS);
  assert.equal(plan.roots.length, 6);
  assert.equal(plan.children.length, 15);

  const rootBySlug = new Map(plan.roots.map((r) => [r.slug, r]));
  assert.equal(rootBySlug.get("ao-thun")?.commission_rate_bps, 500);
  assert.equal(rootBySlug.get("ao-polo")?.commission_rate_bps, 550);
  assert.equal(rootBySlug.get("quan-short")?.commission_rate_bps, 550);
  assert.equal(rootBySlug.get("do-lot")?.commission_rate_bps, 550);
  assert.equal(rootBySlug.get("quan-jogger")?.commission_rate_bps, 500);
  assert.equal(rootBySlug.get("tat-phu-kien")?.commission_rate_bps, 500);

  const childBySlug = new Map(plan.children.map((c) => [c.slug, c]));
  assert.equal(childBySlug.get("polo-pique")?.parentSlug, "ao-polo");
  assert.equal(childBySlug.get("polo-pique")?.commission_rate_bps, 550);
  assert.equal(childBySlug.get("polo-det-kim")?.commission_rate_bps, 500);
  assert.equal(childBySlug.get("short-linen")?.parentSlug, "quan-short");
  assert.equal(childBySlug.get("short-linen")?.commission_rate_bps, 550);
  assert.equal(childBySlug.get("brief-tre")?.parentSlug, "do-lot");
  assert.equal(childBySlug.get("brief-tre")?.commission_rate_bps, 550);
  assert.equal(childBySlug.get("boxer-modal")?.commission_rate_bps, 500);
});

test("W2b seed helpers: shop discount programs and fee schedules are valid and idempotent", async () => {
  const { seedShopDiscountPrograms, seedFeeSchedules } = await import(`./seed.mjs?promo=${Date.now()}`);
  assert.equal(typeof seedShopDiscountPrograms, "function");
  assert.equal(typeof seedFeeSchedules, "function");

  const createdPrograms = [];
  const createdCatFees = [];
  const createdPayFees = [];

  const mockApi = async (method, path, { body } = {}) => {
    if (method === "GET" && path.startsWith("/api/v1/seller/discount-programs")) {
      return { status: 200, ok: true, data: { items: createdPrograms, total: createdPrograms.length } };
    }
    if (method === "POST" && path === "/api/v1/seller/discount-programs") {
      // Enforce UC-S18 XOR rule: each item must set exactly one of discounted_price_vnd or discount_percent.
      for (const it of body.items || []) {
        const hasPrice = Number(it.discounted_price_vnd || 0) > 0;
        const hasPct = Number(it.discount_percent || 0) > 0;
        assert.notEqual(hasPrice, hasPct, `item ${it.sku_id} must set exactly one of discounted_price_vnd or discount_percent`);
      }
      const prog = { id: `prog-${createdPrograms.length + 1}`, name: body.name, status: "ACTIVE", items: body.items };
      createdPrograms.push(prog);
      return { status: 201, ok: true, data: { program: prog, warnings: [] } };
    }
    if (method === "GET" && path.startsWith("/api/v1/admin/fees/categories")) {
      const catId = new URL(path, "http://x").searchParams.get("category_id");
      const items = createdCatFees.filter((f) => f.category_id === catId);
      return { status: 200, ok: true, data: { items, total: items.length } };
    }
    if (method === "POST" && path.startsWith("/api/v1/admin/fees/categories/")) {
      const catId = path.split("/").pop();
      assert.ok(Date.parse(body.effective_from) > Date.now(), "category fee schedule effective_from must be in the future");
      const sched = { id: `cf-${createdCatFees.length + 1}`, category_id: catId, ...body };
      createdCatFees.push(sched);
      return { status: 201, ok: true, data: { schedule: sched } };
    }
    if (method === "GET" && path.startsWith("/api/v1/admin/fees/payment")) {
      return {
        status: 200,
        ok: true,
        data: {
          active_configs: [
            { payment_method: "COD", fee_bps: 0, note: "Default baseline payment fee (0 bps)" },
            { payment_method: "VIETQR", fee_bps: 0, note: "Default baseline payment fee (0 bps)" },
          ],
          history: createdPayFees,
        },
      };
    }
    if (method === "POST" && path === "/api/v1/admin/fees/payment") {
      assert.ok(Date.parse(body.effective_from) > Date.now(), "payment fee schedule effective_from must be in the future so active baseline stays 0 bps");
      const cfg = { id: `pf-${createdPayFees.length + 1}`, ...body };
      createdPayFees.push(cfg);
      return { status: 201, ok: true, data: { config: cfg } };
    }
    throw new Error(`unexpected mock call: ${method} ${path}`);
  };

  const sellerByStaticShopId = new Map([
    ["shop-bach-dang", { token: "tok-bd", backendShop: { id: "s-bd" } }],
    ["shop-sai-gon", { token: "tok-sg", backendShop: { id: "s-sg" } }],
    ["shop-da-nang", { token: "tok-dn", backendShop: { id: "s-dn" } }],
    ["shop-ha-noi", { token: "tok-hn", backendShop: { id: "s-hn" } }],
  ]);
  const productByStaticId = new Map([
    ["prod-at-xamda", { skus: [{ id: "sku-x1", price_vnd: 189000, available_stock: 20, is_active: true }, { id: "sku-x2", price_vnd: 189000, available_stock: 15, is_active: true }] }],
    ["prod-short-run", { skus: [{ id: "sku-r1", price_vnd: 219000, available_stock: 25, is_active: true }] }],
    ["prod-short-linen", { skus: [{ id: "sku-l1", price_vnd: 279000, available_stock: 18, is_active: true }] }],
    ["prod-tat-cothap", { skus: [{ id: "sku-t1", price_vnd: 109000, available_stock: 30, is_active: true }] }],
  ]);

  const nowMs = Date.parse("2026-11-23T10:00:00.000Z");
  const expectedEffectiveIso = new Date(nowMs + 365 * 24 * 60 * 60 * 1000).toISOString();
  const pRun1 = await seedShopDiscountPrograms({
    sellerByStaticShopId,
    productByStaticId,
    liveFlashSkuIds: new Set(["sku-x2"]), // sku-x2 excluded because it is in a live flash sale
    nowMs,
    apiFn: mockApi,
  });
  assert.equal(pRun1.created, 4);
  assert.equal(pRun1.existing, 0);
  assert.ok(createdPrograms[0].items.every((it) => it.sku_id !== "sku-x2"));

  // Second run must be 100% idempotent.
  const pRun2 = await seedShopDiscountPrograms({
    sellerByStaticShopId,
    productByStaticId,
    liveFlashSkuIds: new Set(["sku-x2"]),
    nowMs,
    apiFn: mockApi,
  });
  assert.equal(pRun2.created, 0);
  assert.equal(pRun2.existing, 4);

  const categoryBySlug = new Map([
    ["ao-thun", { id: "cat-at", slug: "ao-thun" }],
    ["ao-polo", { id: "cat-ap", slug: "ao-polo" }],
    ["tat-phu-kien", { id: "cat-tp", slug: "tat-phu-kien" }],
  ]);
  const fRun1 = await seedFeeSchedules({ adminToken: "admin-tok", categoryBySlug, nowMs, apiFn: mockApi });
  assert.equal(fRun1.categoryFees.created, 3);
  assert.equal(fRun1.paymentFees.created, 2);
  for (const sched of createdCatFees) {
    assert.equal(sched.effective_from, expectedEffectiveIso, "category fee effective_from must be now + 365d");
  }
  for (const cfg of createdPayFees) {
    assert.equal(cfg.effective_from, expectedEffectiveIso, "payment fee effective_from must be now + 365d");
  }

  // Re-run at a later timestamp must still skip by note without duplicating.
  const fRun2 = await seedFeeSchedules({
    adminToken: "admin-tok",
    categoryBySlug,
    nowMs: nowMs + 7 * 24 * 60 * 60 * 1000,
    apiFn: mockApi,
  });
  assert.equal(fRun2.categoryFees.created, 0);
  assert.equal(fRun2.categoryFees.existing, 3);
  assert.equal(fRun2.paymentFees.created, 0);
  assert.equal(fRun2.paymentFees.existing, 2);
});

test("W2b seedDemoOrders: skips 409 IDEMPOTENCY_KEY_CONFLICT with warning as existing, still throws other 409/5xx errors", async () => {
  const { seedDemoOrders } = await import(`./seed.mjs?conflict=${Date.now()}`);

  const sellerByStaticShopId = new Map([
    ["shop-bach-dang", { token: "tok-bd", backendShop: { id: "s-bd" } }],
    ["shop-sai-gon", { token: "tok-sg", backendShop: { id: "s-sg" } }],
    ["shop-da-nang", { token: "tok-dn", backendShop: { id: "s-dn" } }],
    ["shop-ha-noi", { token: "tok-hn", backendShop: { id: "s-hn" } }],
  ]);
  const makeProd = (skuCode) => ({
    skus: [{ id: `sku-${skuCode}`, sku_code: skuCode, stock: 25, available_stock: 25, is_active: true }],
  });
  const productByStaticId = new Map([
    ["prod-at-xamda", makeProd("BD-AT02-XAMDA-L")],
    ["prod-tat-cothap", makeProd("HN-TT17-COMBO-5MAU-L")],
    ["prod-short-run", makeProd("SG-SR10-XAMDAM-L")],
    ["prod-aokhoac-gio", makeProd("SG-AK18-XAMTRO-L")],
    ["prod-dolot-brief", makeProd("DN-DL15-COMBO-3MAU-L")],
    ["prod-jogger-tech", makeProd("HN-JK13-DEN-L")],
  ]);

  const warnings = [];
  const origWarn = console.warn;
  console.warn = (...args) => warnings.push(args.join(" "));
  try {
    // 1. Order 1 returns Gateway 409 FAILED_PRECONDITION + IDEMPOTENCY_KEY_CONFLICT message,
    //    Order 2 returns direct 409 IDEMPOTENCY_KEY_CONFLICT code, remaining 4 succeed already at target.
    const targetByIdem = {
      "seed:demo-order:completed:v1": "COMPLETED",
      "seed:demo-order:cancelled:v1": "CANCELLED",
      "seed:demo-order:return-requested:v1": "RETURN_REQUESTED",
      "seed:demo-order:returned:v1": "RETURNED",
    };
    const conflictMockApi = async (method, path, { headers = {} } = {}) => {
      if (method === "POST" && path === "/api/v1/checkout") {
        const idem = headers["Idempotency-Key"];
        if (idem === "seed:demo-order:preparing:v1") {
          return {
            status: 409,
            ok: false,
            data: {
              error: {
                code: "FAILED_PRECONDITION",
                message: "IDEMPOTENCY_KEY_CONFLICT: idempotency_key was already used with a different checkout payload",
              },
            },
          };
        }
        if (idem === "seed:demo-order:shipping:v1") {
          return {
            status: 409,
            ok: false,
            data: { error: { code: "IDEMPOTENCY_KEY_CONFLICT", message: "conflict" } },
          };
        }
        return {
          status: 200,
          ok: true,
          data: { idempotent_replay: true, orders: [{ id: `ord-${idem}`, status: targetByIdem[idem] }] },
        };
      }
      if (method === "GET" && path.startsWith("/api/v1/orders/")) {
        const id = decodeURIComponent(path.split("/").pop());
        const idem = id.replace(/^ord-/, "");
        return { status: 200, ok: true, data: { order: { id, status: targetByIdem[idem] } } };
      }
      throw new Error(`unexpected call: ${method} ${path}`);
    };

    const res = await seedDemoOrders({
      buyerToken: "buyer-tok",
      buyerAddress: { id: "addr-1" },
      sellerByStaticShopId,
      productByStaticId,
      paymentWebhookSecret: "s1",
      shippingWebhookSecret: "s2",
      apiFn: conflictMockApi,
      pollIntervalMs: 1,
    });
    assert.equal(res.created, 0);
    assert.equal(res.existing, 6);
    assert.equal(warnings.length, 2, `expected 2 conflict warnings, got: ${warnings.join(" | ")}`);
    assert.ok(warnings.every((w) => w.includes("IDEMPOTENCY_KEY_CONFLICT")));
  } finally {
    console.warn = origWarn;
  }

  // 2. Non-idempotency 409 (e.g. INSUFFICIENT_STOCK) MUST still throw!
  const other409Api = async (method, path) => {
    if (method === "POST" && path === "/api/v1/checkout") {
      return {
        status: 409,
        ok: false,
        data: { error: { code: "INSUFFICIENT_STOCK", message: "not enough stock" } },
      };
    }
    throw new Error(`unexpected call: ${method} ${path}`);
  };
  await assert.rejects(
    () =>
      seedDemoOrders({
        buyerToken: "buyer-tok",
        buyerAddress: { id: "addr-1" },
        sellerByStaticShopId,
        productByStaticId,
        paymentWebhookSecret: "s1",
        shippingWebhookSecret: "s2",
        apiFn: other409Api,
        pollIntervalMs: 1,
      }),
    /INSUFFICIENT_STOCK/
  );
});

test("CAT-D3: buildCategoryUpdatePatch sends only changed fields so gateway has_* flags preserve untouched fields", async () => {
  const { buildCategoryUpdatePatch } = await import(`./seed.mjs?catpatch=${Date.now()}`);
  assert.equal(typeof buildCategoryUpdatePatch, "function");

  // Unchanged root -> null (no PUT needed)
  assert.equal(
    buildCategoryUpdatePatch(
      { id: "c1", name: "Áo polo", slug: "ao-polo", parent_id: "", commission_rate_bps: 550, sort_order: 2 },
      { commission_rate_bps: 550 }
    ),
    null
  );

  // Root commission change ONLY -> must NOT send parent_id, name, slug, or sort_order
  assert.deepEqual(
    buildCategoryUpdatePatch(
      { id: "c1", name: "Áo polo", slug: "ao-polo", parent_id: "existing-parent", commission_rate_bps: 500, sort_order: 2 },
      { commission_rate_bps: 550 }
    ),
    { commission_rate_bps: 550 }
  );

  // Child parent_id change ONLY -> must NOT send commission_rate_bps, name, slug, or sort_order
  assert.deepEqual(
    buildCategoryUpdatePatch(
      { id: "c2", name: "Polo Pique", slug: "polo-pique", parent_id: "", commission_rate_bps: 550, sort_order: 201 },
      { parent_id: "c1", commission_rate_bps: 550 }
    ),
    { parent_id: "c1" }
  );

  // Both parent_id and commission_rate_bps changed -> sends both, still omits name/slug/sort_order
  assert.deepEqual(
    buildCategoryUpdatePatch(
      { id: "c2", name: "Polo Pique", slug: "polo-pique", parent_id: "", commission_rate_bps: 500, sort_order: 201 },
      { parent_id: "c1", commission_rate_bps: 550 }
    ),
    { parent_id: "c1", commission_rate_bps: 550 }
  );
});

test("W2b seedDemoOrders: creates multi-state orders via API + signed webhooks and is idempotent on replay", async () => {
  const { signWebhookPayload, seedDemoOrders } = await import(`./seed.mjs?ord=${Date.now()}`);
  assert.equal(typeof signWebhookPayload, "function");
  assert.equal(typeof seedDemoOrders, "function");

  const paySecret = "test-pay-secret";
  const shipSecret = "test-ship-secret";
  const sigHeaders = signWebhookPayload(paySecret, '{"ok":true}', 1700000000);
  const expectedSig = crypto.createHmac("sha256", paySecret).update('{"ok":true}').digest("hex");
  assert.equal(sigHeaders["X-Signature"], expectedSig);
  assert.equal(sigHeaders["X-Timestamp"], "1700000000");

  // Stateful mock gateway simulating checkout idempotency + order/return state transitions.
  const ordersById = new Map();
  const checkoutByIdem = new Map();
  const returnsById = new Map();
  let seq = 0;

  const mockApi = async (method, path, { body, headers = {} } = {}) => {
    if (method === "POST" && path === "/api/v1/checkout") {
      const idem = headers["Idempotency-Key"];
      assert.ok(idem, "checkout must send Idempotency-Key");
      assert.equal(body.from_cart, false);
      if (checkoutByIdem.has(idem)) {
        return { status: 200, ok: true, data: { ...checkoutByIdem.get(idem), idempotent_replay: true } };
      }
      seq += 1;
      const ordId = `ord-${seq}`;
      const grpId = `grp-${seq}`;
      const payId = `pay-${seq}`;
      const initialStatus = body.payment_method === "COD" ? "CONFIRMED" : "PENDING";
      const ord = {
        id: ordId,
        checkout_group_id: grpId,
        payment_id: payId,
        payment_method: body.payment_method,
        status: initialStatus,
        total_payable_vnd: 220000,
        tracking_code: "",
        return_request_id: "",
      };
      ordersById.set(ordId, ord);
      const resp = {
        checkout_group_id: grpId,
        payment_id: payId,
        total_payable_vnd: 220000,
        orders: [ord],
        idempotent_replay: false,
      };
      checkoutByIdem.set(idem, resp);
      return { status: 201, ok: true, data: resp };
    }
    if (method === "GET" && path.startsWith("/api/v1/orders/")) {
      const id = path.split("/").pop();
      return { status: 200, ok: true, data: { order: { ...ordersById.get(id) } } };
    }
    if (method === "POST" && path === "/webhooks/payment") {
      assert.ok(headers["X-Signature"] && headers["X-Timestamp"], "payment webhook must be signed");
      for (const ord of ordersById.values()) {
        if (ord.checkout_group_id === body.order_group_id && ord.status === "PENDING") {
          ord.status = "CONFIRMED";
        }
      }
      return { status: 200, ok: true, data: { ok: true } };
    }
    if (method === "POST" && path.endsWith("/confirm")) {
      const id = path.split("/")[5];
      ordersById.get(id).status = "PREPARING";
      return { status: 200, ok: true, data: { order: { ...ordersById.get(id) } } };
    }
    if (method === "POST" && path.endsWith("/ship")) {
      const id = path.split("/")[5];
      const ord = ordersById.get(id);
      ord.tracking_code = `GHN-${id}`;
      return { status: 200, ok: true, data: { order: { ...ord }, tracking_code: ord.tracking_code } };
    }
    if (method === "POST" && path === "/webhooks/shipping/ghn") {
      assert.ok(headers["X-Signature"] && headers["X-Timestamp"], "shipping webhook must be signed");
      const ord = ordersById.get(body.order_id);
      if (body.raw_status === "delivering") ord.status = "SHIPPING";
      if (body.raw_status === "delivered") ord.status = "DELIVERED";
      return { status: 200, ok: true, data: { ok: true } };
    }
    if (method === "POST" && path.endsWith("/confirm-received")) {
      const id = path.split("/")[4];
      ordersById.get(id).status = "COMPLETED";
      return { status: 200, ok: true, data: { order: { ...ordersById.get(id) } } };
    }
    if (method === "POST" && path.endsWith("/cancel")) {
      const id = path.split("/")[4];
      ordersById.get(id).status = "CANCELLED";
      return { status: 200, ok: true, data: { order: { ...ordersById.get(id) } } };
    }
    if (method === "POST" && path.endsWith("/return")) {
      for (const u of body.evidence_urls || []) {
        assert.match(u, /^https?:\/\//, `evidence URL must be http(s): ${u}`);
      }
      const id = path.split("/")[4];
      const ord = ordersById.get(id);
      const retId = `ret-${id}`;
      ord.status = "RETURN_REQUESTED";
      ord.return_request_id = retId;
      returnsById.set(retId, { id: retId, order_id: id, status: "PENDING" });
      return { status: 200, ok: true, data: { order: { ...ord }, return_request_id: retId } };
    }
    if (method === "POST" && path.endsWith("/respond")) {
      const retId = path.split("/")[5];
      const ret = returnsById.get(retId);
      ret.status = "REFUNDED";
      ordersById.get(ret.order_id).status = "RETURNED";
      return { status: 200, ok: true, data: { return_request: { ...ret } } };
    }
    throw new Error(`unexpected mock order call: ${method} ${path}`);
  };

  const sellerByStaticShopId = new Map([
    ["shop-bach-dang", { token: "tok-bd", backendShop: { id: "s-bd" } }],
    ["shop-sai-gon", { token: "tok-sg", backendShop: { id: "s-sg" } }],
    ["shop-da-nang", { token: "tok-dn", backendShop: { id: "s-dn" } }],
    ["shop-ha-noi", { token: "tok-hn", backendShop: { id: "s-hn" } }],
  ]);
  const makeProd = (skuCode) => ({
    skus: [{ id: `sku-${skuCode}`, sku_code: skuCode, stock: 25, available_stock: 25, is_active: true }],
  });
  const productByStaticId = new Map([
    ["prod-at-xamda", makeProd("BD-AT02-XAMDA-L")],
    ["prod-tat-cothap", makeProd("HN-TT17-COMBO-5MAU-L")],
    ["prod-short-run", makeProd("SG-SR10-XAMDAM-L")],
    ["prod-aokhoac-gio", makeProd("SG-AK18-XAMTRO-L")],
    ["prod-dolot-brief", makeProd("DN-DL15-COMBO-3MAU-L")],
    ["prod-jogger-tech", makeProd("HN-JK13-DEN-L")],
  ]);

  const run1 = await seedDemoOrders({
    buyerToken: "buyer-tok",
    buyerAddress: { id: "addr-1" },
    sellerByStaticShopId,
    productByStaticId,
    paymentWebhookSecret: paySecret,
    shippingWebhookSecret: shipSecret,
    apiFn: mockApi,
    pollIntervalMs: 1,
  });
  assert.equal(run1.created, 6);
  assert.equal(run1.existing, 0);

  const statuses = new Set([...ordersById.values()].map((o) => o.status));
  for (const want of ["PREPARING", "SHIPPING", "COMPLETED", "CANCELLED", "RETURN_REQUESTED", "RETURNED"]) {
    assert.ok(statuses.has(want), `expected seeded order in status ${want}, got: ${[...statuses].join(", ")}`);
  }

  // Re-run must be 100% idempotent with zero new orders created.
  const run2 = await seedDemoOrders({
    buyerToken: "buyer-tok",
    buyerAddress: { id: "addr-1" },
    sellerByStaticShopId,
    productByStaticId,
    paymentWebhookSecret: paySecret,
    shippingWebhookSecret: shipSecret,
    apiFn: mockApi,
    pollIntervalMs: 1,
  });
  assert.equal(run2.created, 0);
  assert.equal(run2.existing, 6);
  assert.equal(ordersById.size, 6);
});
