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
      assert.equal(body.sales_channel, "STOREFRONT", "demo checkout must default to STOREFRONT channel");
      assert.ok(body.storefront_shop_id, "STOREFRONT checkout must include storefront_shop_id");
      assert.equal(
        body.storefront_shop_id,
        body.shipping_choices?.[0]?.shop_id,
        "storefront_shop_id must match shipping shop_id"
      );
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

test("W3A D1: listAllAdminCategories queries /api/v1/admin/categories with admin JWT so seed stays idempotent when public /categories omits commission_rate_bps", async () => {
  const { readFileSync } = await import("node:fs");
  const { resolve, dirname } = await import("node:path");
  const { fileURLToPath } = await import("node:url");

  const seedSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "seed.mjs"), "utf8");
  assert.equal(
    /["'`]\/api\/v1\/categories["'`]/.test(seedSource),
    false,
    "seed.mjs must NOT fetch public /api/v1/categories (which omits commission_rate_bps under D1)"
  );

  const requestedPaths = [];
  const server = http.createServer((req, res) => {
    const parsed = new URL(req.url, "http://127.0.0.1");
    requestedPaths.push(parsed.pathname);

    if (parsed.pathname === "/api/v1/categories") {
      // D1: public /categories omits commission_rate_bps completely.
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          categories: [{ id: "cat-1", name: "Áo thun", slug: "ao-thun", parent_id: "", sort_order: 1 }],
        })
      );
      return;
    }

    if (parsed.pathname === "/api/v1/admin/categories") {
      if (req.headers.authorization !== "Bearer admin-jwt-d1") {
        res.writeHead(401, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: { code: "UNAUTHENTICATED" } }));
        return;
      }
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          categories: [
            { id: "cat-1", name: "Áo thun", slug: "ao-thun", parent_id: "", commission_rate_bps: 500, sort_order: 1 },
            { id: "cat-2", name: "Áo polo", slug: "ao-polo", parent_id: "", commission_rate_bps: 550, sort_order: 2 },
          ],
        })
      );
      return;
    }

    res.writeHead(404);
    res.end();
  });

  await new Promise((resolvePromise, reject) => {
    server.on("error", reject);
    server.listen(0, "127.0.0.1", resolvePromise);
  });

  try {
    const { port } = server.address();
    process.env.SEED_GATEWAY_URL = `http://127.0.0.1:${port}`;
    const { listAllAdminCategories, buildCategoryUpdatePatch } = await import(`./seed.mjs?d1=${Date.now()}`);
    assert.equal(typeof listAllAdminCategories, "function");

    const cats = await listAllAdminCategories("admin-jwt-d1");
    assert.deepEqual(requestedPaths, ["/api/v1/admin/categories"]);
    assert.equal(cats.length, 2);
    assert.equal(cats[0].commission_rate_bps, 500);
    assert.equal(cats[1].commission_rate_bps, 550);

    // Matching commission_rate_bps must yield null patch -> zero spurious PUTs on re-seed!
    assert.equal(buildCategoryUpdatePatch(cats[0], { commission_rate_bps: 500 }), null);
    assert.equal(buildCategoryUpdatePatch(cats[1], { commission_rate_bps: 550 }), null);
  } finally {
    delete process.env.SEED_GATEWAY_URL;
    await new Promise((resolvePromise) => server.close(resolvePromise));
  }
});

test("W3A Round2 P1-1: buildCategorySeedPlan rejects unknown shops without 0.05 fallback and seedCategories re-run issues zero PUTs", async () => {
  const { buildCategorySeedPlan, seedCategories } = await import(`./seed.mjs?p11=${Date.now()}`);
  const { CATEGORIES, PRODUCTS } = await import("../../web/src/data/products.ts");
  const { SHOPS } = await import("../../web/src/data/shops.ts");

  assert.equal(typeof seedCategories, "function");

  // Unknown shop must throw explicitly — NEVER silently fall back to 0.05 (500 bps).
  assert.throws(
    () =>
      buildCategorySeedPlan(
        CATEGORIES,
        PRODUCTS,
        [...SHOPS, { id: "shop-unknown-x", name: "Unknown Shop" }]
      ),
    /shop-unknown-x/,
    "buildCategorySeedPlan must throw a clear error when a shop is missing from SEED_SHOP_COMMISSION_RATE"
  );

  // Also reject a product pointing at a shop missing from the shop list/table.
  assert.throws(
    () =>
      buildCategorySeedPlan(
        [{ slug: "ao-thun", name: "Áo thun", subCategories: [] }],
        [{ id: "prod-x", categorySlug: "ao-thun", shopId: "shop-ghost" }],
        SHOPS
      ),
    /shop-ghost/,
    "buildCategorySeedPlan must throw a clear error when a product references an unknown shop"
  );

  // Full 2-pass idempotency check against mock admin category API:
  // Run 1 creates 6 roots + 15 children (including 550 bps for shop-da-nang categories),
  // Run 2 reads /api/v1/admin/categories and issues ZERO POSTs and ZERO PUTs.
  const stored = [];
  let putCalls = 0;
  let postCalls = 0;
  let getAdminCalls = 0;

  const mockApi = async (method, path, { token, body } = {}) => {
    assert.equal(token, "admin-jwt-p11");
    if (method === "GET" && path === "/api/v1/admin/categories") {
      getAdminCalls += 1;
      return { status: 200, ok: true, data: { categories: stored.map((c) => ({ ...c })) } };
    }
    if (method === "POST" && path === "/api/v1/admin/categories") {
      postCalls += 1;
      const created = {
        id: `cat-${stored.length + 1}`,
        name: body.name,
        slug: body.slug,
        parent_id: body.parent_id || "",
        commission_rate_bps: body.commission_rate_bps,
        sort_order: body.sort_order,
      };
      stored.push(created);
      return { status: 201, ok: true, data: { category: { ...created } } };
    }
    if (method === "PUT" && path.startsWith("/api/v1/admin/categories/")) {
      putCalls += 1;
      const id = path.split("/").pop();
      const target = stored.find((c) => c.id === id);
      assert.ok(target, `PUT target category ${id} not found`);
      Object.assign(target, body);
      return { status: 200, ok: true, data: { category: { ...target } } };
    }
    throw new Error(`unexpected API call: ${method} ${path}`);
  };

  const run1 = await seedCategories({
    adminToken: "admin-jwt-p11",
    categories: CATEGORIES,
    products: PRODUCTS,
    shops: SHOPS,
    apiFn: mockApi,
  });
  assert.equal(run1.categories.created, 6);
  assert.equal(run1.childCategories.created, 15);
  assert.equal(run1.categories.updated, 0);
  assert.equal(run1.childCategories.updated, 0);
  assert.equal(putCalls, 0);
  assert.equal(postCalls, 21);

  const bySlug = new Map(stored.map((c) => [c.slug, c]));
  assert.equal(bySlug.get("ao-polo")?.commission_rate_bps, 550);
  assert.equal(bySlug.get("quan-short")?.commission_rate_bps, 550);
  assert.equal(bySlug.get("do-lot")?.commission_rate_bps, 550);
  assert.equal(bySlug.get("polo-pique")?.commission_rate_bps, 550);
  assert.equal(bySlug.get("short-linen")?.commission_rate_bps, 550);
  assert.equal(bySlug.get("brief-tre")?.commission_rate_bps, 550);
  assert.equal(bySlug.get("ao-thun")?.commission_rate_bps, 500);

  // Run 2 on already-seeded DB: ZERO POSTs, ZERO PUTs.
  postCalls = 0;
  putCalls = 0;
  const run2 = await seedCategories({
    adminToken: "admin-jwt-p11",
    categories: CATEGORIES,
    products: PRODUCTS,
    shops: SHOPS,
    apiFn: mockApi,
  });
  assert.ok(getAdminCalls >= 2);
  assert.equal(run2.categories.created, 0);
  assert.equal(run2.categories.existing, 6);
  assert.equal(run2.categories.updated, 0);
  assert.equal(run2.childCategories.created, 0);
  assert.equal(run2.childCategories.existing, 15);
  assert.equal(run2.childCategories.updated, 0);
  assert.equal(postCalls, 0, "re-running seed on seeded DB must issue zero POSTs");
  assert.equal(putCalls, 0, "re-running seed on seeded DB must issue zero PUTs");

  // Round 4 [P3]: non-number commissionRate ("" / false) on unknown shop must throw, not coerce to 0 bps.
  for (const badVal of ["", false]) {
    assert.throws(
      () =>
        buildCategorySeedPlan(
          CATEGORIES,
          PRODUCTS,
          [...SHOPS, { id: "shop-bad-rate", commissionRate: badVal }]
        ),
      /shop-bad-rate/,
      `commissionRate=${JSON.stringify(badVal)} must not coerce to 0 bps`
    );
  }

  // Round 4 [P3]: 409 conflict on POST /admin/categories must run buildCategoryUpdatePatch + PUT on refetched row.
  const raceStored = [
    { id: "cat-root-1", name: "Áo polo", slug: "ao-polo", parent_id: "", commission_rate_bps: 400, sort_order: 1 },
    { id: "cat-child-1", name: "Polo Pique", slug: "polo-pique", parent_id: "", commission_rate_bps: 400, sort_order: 101 },
  ];
  let getCount = 0;
  const racePuts = [];
  const raceApi = async (method, path, { body } = {}) => {
    if (method === "GET" && path === "/api/v1/admin/categories") {
      getCount += 1;
      // Initial list returns empty (simulating concurrent seed), subsequent refetches return drifted rows.
      return { status: 200, ok: true, data: { categories: getCount === 1 ? [] : raceStored.map((c) => ({ ...c })) } };
    }
    if (method === "POST" && path === "/api/v1/admin/categories") {
      return { status: 409, ok: false, data: { error: { code: "ALREADY_EXISTS" } } };
    }
    if (method === "PUT" && path.startsWith("/api/v1/admin/categories/")) {
      const id = path.split("/").pop();
      racePuts.push({ id, body });
      const target = raceStored.find((c) => c.id === id);
      Object.assign(target, body);
      return { status: 200, ok: true, data: { category: { ...target } } };
    }
    throw new Error(`unexpected race API call: ${method} ${path}`);
  };
  const raceRes = await seedCategories({
    adminToken: "admin-jwt-race",
    categories: [{ slug: "ao-polo", name: "Áo polo", subCategories: [{ slug: "polo-pique", name: "Polo Pique" }] }],
    products: [{ id: "p1", categorySlug: "ao-polo", shopId: "shop-da-nang", material: "pique-cotton" }],
    shops: SHOPS,
    apiFn: raceApi,
  });
  assert.equal(raceRes.categories.updated, 1);
  assert.equal(raceRes.childCategories.updated, 1);
  assert.deepEqual(racePuts, [
    { id: "cat-root-1", body: { commission_rate_bps: 550 } },
    { id: "cat-child-1", body: { parent_id: "cat-root-1", commission_rate_bps: 550 } },
  ]);
});

test("W3B E19: ensureBuyerPhoneVerified verifies once in order, skips when verified, and unlocks COD orders", async () => {
  const { ensureBuyerPhoneVerified, seedDemoOrders } = await import(`./seed.mjs?e19=${Date.now()}`);
  const buyerEmail = "minhquan.nguyen@nexusmart.vn";
  const seenSnapshot = new Set(["msg-reg-1"]);
  const authCalls = [];
  const orders = new Map();
  const returns = new Map();
  let phoneVerified = false;
  let seq = 0;
  const ok = (data, status = 200) => ({ status, ok: true, data });

  const mockApi = async (method, path, { body } = {}) => {
    if (path.startsWith("/api/v1/users/me")) authCalls.push(`${method} ${path}`);
    if (method === "GET" && path === "/api/v1/users/me") return ok({ user: { email: buyerEmail, phone_verified: phoneVerified } });
    if (method === "POST" && path === "/api/v1/users/me/phone/otp") { assert.deepEqual(body, {}); return ok({ sent: true }); }
    if (method === "POST" && path === "/api/v1/users/me/phone/verify") {
      assert.equal(body.otp, "748291");
      phoneVerified = true;
      return ok({ verified: true, user: { phone_verified: true } });
    }
    if (method === "POST" && path === "/api/v1/checkout") {
      if (body.payment_method === "COD" && !phoneVerified) {
        return { status: 409, ok: false, data: { error: { code: "COD_PHONE_NOT_VERIFIED", message: "phone unverified" } } };
      }
      const id = `ord-${++seq}`;
      const ord = { id, checkout_group_id: `grp-${seq}`, payment_id: `pay-${seq}`, status: body.payment_method === "COD" ? "CONFIRMED" : "PENDING", total_payable_vnd: 200000, tracking_code: "", return_request_id: "" };
      orders.set(id, ord);
      return ok({ orders: [ord], idempotent_replay: false }, 201);
    }
    if (method === "GET" && path.startsWith("/api/v1/orders/")) return ok({ order: { ...orders.get(path.split("/").pop()) } });
    if (method === "POST" && path === "/webhooks/payment") {
      for (const o of orders.values()) if (o.checkout_group_id === body.order_group_id) o.status = "CONFIRMED";
      return ok({ ok: true });
    }
    if (method === "POST" && path === "/webhooks/shipping/ghn") {
      orders.get(body.order_id).status = body.raw_status === "delivering" ? "SHIPPING" : "DELIVERED";
      return ok({ ok: true });
    }
    if (method === "POST" && path.endsWith("/confirm")) { const o = orders.get(path.split("/")[5]); o.status = "PREPARING"; return ok({ order: { ...o } }); }
    if (method === "POST" && path.endsWith("/ship")) { const o = orders.get(path.split("/")[5]); o.tracking_code = `GHN-${o.id}`; return ok({ order: { ...o }, tracking_code: o.tracking_code }); }
    if (method === "POST" && path.endsWith("/confirm-received")) { const o = orders.get(path.split("/")[4]); o.status = "COMPLETED"; return ok({ order: { ...o } }); }
    if (method === "POST" && path.endsWith("/cancel")) { const o = orders.get(path.split("/")[4]); o.status = "CANCELLED"; return ok({ order: { ...o } }); }
    if (method === "POST" && path.endsWith("/return")) {
      const o = orders.get(path.split("/")[4]);
      o.status = "RETURN_REQUESTED";
      o.return_request_id = `ret-${o.id}`;
      returns.set(o.return_request_id, o.id);
      return ok({ order: { ...o }, return_request_id: o.return_request_id });
    }
    if (method === "POST" && path.endsWith("/respond")) { orders.get(returns.get(path.split("/")[5])).status = "RETURNED"; return ok({ ok: true }); }
    throw new Error(`unexpected call: ${method} ${path}`);
  };

  const sellerByStaticShopId = new Map(["shop-bach-dang", "shop-sai-gon", "shop-da-nang", "shop-ha-noi"].map((id) => [id, { token: `t-${id}`, backendShop: { id: `b-${id}` } }]));
  const productByStaticId = new Map(["prod-at-xamda", "prod-tat-cothap", "prod-short-run", "prod-aokhoac-gio", "prod-dolot-brief", "prod-jogger-tech"].map((id) => [id, { skus: [{ id: `sku-${id}`, stock: 20, available_stock: 20, is_active: true }] }]));
  const runOrders = () => seedDemoOrders({ buyerToken: "buyer-tok", buyerAddress: { id: "addr-1" }, sellerByStaticShopId, productByStaticId, paymentWebhookSecret: "s1", shippingWebhookSecret: "s2", apiFn: mockApi, pollIntervalMs: 1 });

  // (c) COD is blocked before verification, then all 6 demo orders succeed after verify.
  await assert.rejects(() => runOrders(), /COD_PHONE_NOT_VERIFIED/);
  orders.clear();

  // (a) Unverified buyer -> GET me -> POST otp -> POST verify (once each, passing seenIds).
  let receivedSeen = null;
  const first = await ensureBuyerPhoneVerified({
    buyerToken: "buyer-tok",
    buyerEmail,
    apiFn: mockApi,
    listSeenFn: async () => seenSnapshot,
    waitForOtpFn: async (email, _since, _timeout, seen) => {
      assert.equal(email, buyerEmail);
      receivedSeen = seen;
      return "748291";
    },
  });
  assert.equal(first, "created");
  assert.equal(receivedSeen, seenSnapshot);
  assert.deepEqual(authCalls, ["GET /api/v1/users/me", "POST /api/v1/users/me/phone/otp", "POST /api/v1/users/me/phone/verify"]);
  assert.equal((await runOrders()).created, 6);

  // (b) Already verified -> only GET me, zero otp/verify/Mailpit calls.
  authCalls.length = 0;
  const second = await ensureBuyerPhoneVerified({
    buyerToken: "buyer-tok",
    buyerEmail,
    apiFn: mockApi,
    listSeenFn: async () => assert.fail("must not read Mailpit when already verified"),
    waitForOtpFn: async () => assert.fail("must not wait for OTP when already verified"),
  });
  assert.equal(second, "existing");
  assert.deepEqual(authCalls, ["GET /api/v1/users/me"]);
});

test("Merge: seedShowcaseSites and seedChatSampleData publish idempotently and skip cleanly when unavailable", async () => {
  const { seedShowcaseSites, seedChatSampleData } = await import(`./seed.mjs?sites=${Date.now()}`);
  assert.equal(typeof seedShowcaseSites, "function");
  assert.equal(typeof seedChatSampleData, "function");

  const sellerByStaticShopId = new Map([
    ["shop-bach-dang", { token: "tok-bd", backendShop: { id: "s-bd" } }],
    ["shop-sai-gon", { token: "tok-sg", backendShop: { id: "s-sg" } }],
    ["shop-da-nang", { token: "tok-dn", backendShop: { id: "s-dn" } }],
    ["shop-ha-noi", { token: "tok-hn", backendShop: { id: "s-hn" } }],
  ]);
  const productByStaticId = new Map([
    ["prod-at-trang", { id: "p-at-trang", name: "Áo thun Trắng", min_price_vnd: 179000, images: ["/p.jpg"] }],
    ["prod-at-dryfit", { id: "p-at-dryfit", name: "Áo Dry-Fit", min_price_vnd: 159000, images: ["/d.jpg"] }],
  ]);

  const sitesByToken = new Map();
  const domainsByToken = new Map();
  let publishCount = 0;

  const siteApi = async (method, path, { token, body } = {}) => {
    if (method === "GET" && path === "/api/v1/site-templates") {
      return {
        status: 200,
        ok: true,
        data: {
          templates: [
            {
              id: "tpl-craft",
              slug: "craft-atelier",
              config: { schema_version: 2, layout: { archetype: "editorial" }, pages: { home: {} }, theme: { radius: "sm" } },
            },
          ],
        },
      };
    }
    if (method === "GET" && path === "/api/v1/seller/site") {
      const existing = sitesByToken.get(token);
      if (!existing) return { status: 404, ok: false, data: null };
      return { status: 200, ok: true, data: { site: existing } };
    }
    if (method === "GET" && path.startsWith("/api/v1/sites/by-host?host=")) {
      const host = decodeURIComponent(path.split("host=")[1]);
      for (const s of sitesByToken.values()) {
        if (s.status === "PUBLISHED" && (`${s.subdomain}.nexusmart.localhost` === host || s.primary_host === host)) {
          return { status: 200, ok: true, data: { site: s } };
        }
      }
      return { status: 404, ok: false, data: null };
    }
    if (method === "PUT" && path === "/api/v1/seller/site/draft") {
      const prev = sitesByToken.get(token) || {};
      sitesByToken.set(token, { ...prev, subdomain: body.subdomain, config: body.config, status: prev.status || "DRAFT" });
      return { status: 200, ok: true, data: { site: sitesByToken.get(token) } };
    }
    if (method === "POST" && path === "/api/v1/seller/site/publish") {
      publishCount += 1;
      const cur = sitesByToken.get(token);
      cur.status = "PUBLISHED";
      cur.primary_host = `${cur.subdomain}.nexusmart.localhost`;
      return { status: 200, ok: true, data: { site: cur } };
    }
    if (method === "GET" && path === "/api/v1/seller/site/domains") {
      return { status: 200, ok: true, data: { domains: domainsByToken.get(token) || [] } };
    }
    if (method === "POST" && path === "/api/v1/seller/site/domains") {
      const list = domainsByToken.get(token) || [];
      const dom = { id: `dom-${list.length + 1}`, host: body.hostname, kind: "CUSTOM", status: "PENDING" };
      list.push(dom);
      domainsByToken.set(token, list);
      return { status: 201, ok: true, data: { domain: dom } };
    }
    if (method === "POST" && path.endsWith("/check")) {
      const dom = (domainsByToken.get(token) || [])[0];
      dom.status = "ACTIVE";
      sitesByToken.get(token).primary_host = dom.host;
      return { status: 200, ok: true, data: { domain: dom } };
    }
    throw new Error(`unexpected siteApi call: ${method} ${path}`);
  };

  const s1 = await seedShowcaseSites({ sellerByStaticShopId, productByStaticId, apiFn: siteApi });
  assert.equal(s1.created, 4);
  assert.equal(s1.existing, 0);
  assert.equal(s1.templates.applied, 1);
  assert.equal(s1.sites["ha-noi"].custom_domain_status, "ACTIVE");
  assert.equal(s1.sites["da-nang"].custom_domain_status, "PENDING");
  assert.equal(publishCount, 4);

  // Second run reuses all 4 sites without republishing.
  const s2 = await seedShowcaseSites({ sellerByStaticShopId, productByStaticId, apiFn: siteApi });
  assert.equal(s2.created, 0);
  assert.equal(s2.existing, 4);
  assert.equal(publishCount, 4);

  // Chat skips gracefully when service returns 503.
  const chatSkipped = await seedChatSampleData({
    buyerToken: "buyer-tok",
    sellerByStaticShopId,
    productByStaticId,
    apiFn: async () => ({ status: 503, ok: false, data: null }),
  });
  assert.equal(chatSkipped.skipped, true);

  // Chat seeds auto-reply + 3 messages per shop and is idempotent on replay.
  const autoReplies = new Map();
  const messagesByConv = new Map();
  const chatApi = async (method, path, { token, body } = {}) => {
    if (method === "GET" && path.startsWith("/api/v1/chat/conversations?")) {
      return { status: 200, ok: true, data: { items: [] } };
    }
    if (method === "GET" && path === "/api/v1/seller/chat/auto-reply") {
      return { status: 200, ok: true, data: { config: { enabled: Boolean(autoReplies.get(token)) } } };
    }
    if (method === "PUT" && path === "/api/v1/seller/chat/auto-reply") {
      autoReplies.set(token, body);
      return { status: 200, ok: true, data: { config: body } };
    }
    if (method === "POST" && path === "/api/v1/chat/conversations") {
      return { status: 200, ok: true, data: { conversation: { id: `conv-${body.shop_id}` } } };
    }
    if (method === "GET" && path.includes("/messages?limit=")) {
      const convId = path.split("/")[5];
      return { status: 200, ok: true, data: { items: messagesByConv.get(convId) || [] } };
    }
    if (method === "POST" && path.endsWith("/messages")) {
      const convId = path.split("/")[5];
      const list = messagesByConv.get(convId) || [];
      list.push(body);
      messagesByConv.set(convId, list);
      return { status: 201, ok: true, data: { message: body } };
    }
    throw new Error(`unexpected chatApi call: ${method} ${path}`);
  };
  const c1 = await seedChatSampleData({ buyerToken: "buyer-tok", sellerByStaticShopId, productByStaticId, apiFn: chatApi });
  assert.equal(c1.skipped, false);
  assert.equal(c1.created, 2);
  assert.equal(c1.messagesSent, 6);
  const c2 = await seedChatSampleData({ buyerToken: "buyer-tok", sellerByStaticShopId, productByStaticId, apiFn: chatApi });
  assert.equal(c2.created, 0);
  assert.equal(c2.existing, 2);
  assert.equal(c2.messagesSent, 0);
});
