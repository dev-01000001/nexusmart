import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  createBuyerAddress,
  ensureRateBudget,
  registerAndLoginBuyer,
  snapshotMailpitIds,
  waitForRecentOtp,
} from "./helpers.mjs";

const SELLER_EMAIL = process.env.E2E_SELLER_EMAIL || "seller.bachdang@nexusmart.local";
const SELLER_PASSWORD = process.env.E2E_SELLER_PASSWORD || "SellerDevPassword123!";
const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";

async function loginAccount(email, password) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `login ${email} expected 200, got ${res.status}: ${res.text}`);
  return { client, user: res.data?.user };
}

function formatVndDots(amount) {
  return String(Math.trunc(amount)).replace(/\B(?=(\d{3})+(?!\d))/g, ".");
}

function formatIsoDateOffset(deltaDays) {
  const vnMs = Date.now() + 7 * 3600 * 1000 + deltaDays * 24 * 3600 * 1000;
  const d = new Date(vnMs);
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
}

async function createApprovedSellerProduct(sellerClient, adminClient, tag) {
  const catRes = await sellerClient.json("GET", "/api/gw/categories");
  assert.equal(catRes.status, 200, `categories failed: ${catRes.text}`);
  const category = (catRes.data?.categories ?? [])[0];
  assert.ok(category?.id, `no categories available: ${catRes.text}`);

  const suffix = crypto.randomBytes(4).toString("hex");
  const createRes = await sellerClient.json("POST", "/api/gw/seller/products", {
    body: {
      category_id: category.id,
      name: `Áo thun Cotton E2E ${tag} ${suffix}`,
      slug: `ao-thun-e2e-${tag}-${suffix}`,
      description: "Sản phẩm kiểm thử khuyến mãi Wave 2b.",
      images: ["/products/ao-thun-cotton-trang.png"],
      skus: [
        {
          sku_code: `W2B-${tag.toUpperCase()}-${suffix.toUpperCase()}-M`,
          name: "Trắng tinh / M",
          attributes: { color: "Trắng tinh", color_code: "trang", size: "M" },
          price_vnd: 200_000,
          stock: 20,
          weight_grams: 250,
          image_url: "/products/ao-thun-cotton-trang.png",
        },
      ],
    },
  });
  assert.ok(
    createRes.status === 200 || createRes.status === 201,
    `create E2E product failed (${createRes.status}): ${createRes.text}`
  );
  const created = createRes.data?.product;
  assert.ok(created?.id && created?.skus?.[0]?.id, `missing product/sku: ${createRes.text}`);

  const reviewRes = await adminClient.json(
    "POST",
    `/api/gw/admin/products/${encodeURIComponent(created.id)}/review`,
    {
      body: { decision: "APPROVE", reason: "E2E Wave 2b auto-approve" },
    }
  );
  assert.equal(reviewRes.status, 200, `approve E2E product failed: ${reviewRes.text}`);
  const approved = reviewRes.data?.product ?? created;
  return { product: approved, sku: approved.skus[0] };
}

async function pickSellerActiveSku(
  sellerClient,
  adminClient,
  tag,
  minPriceVnd = 100_000,
  minStock = 4,
  excludeSkuIds = new Set(),
  excludeAnySkuProductOverlap = false
) {
  const prodRes = await sellerClient.json("GET", "/api/gw/seller/products?page=1&page_size=50");
  assert.equal(prodRes.status, 200, `seller products failed: ${prodRes.status} ${prodRes.text}`);
  const products = prodRes.data?.items ?? [];
  for (const product of products) {
    if (String(product.status).toUpperCase() !== "APPROVED") continue;
    if (
      excludeAnySkuProductOverlap &&
      (product.skus ?? []).some((s) => excludeSkuIds.has(s.id))
    ) {
      continue;
    }
    for (const sku of product.skus ?? []) {
      if (
        !excludeSkuIds.has(sku.id) &&
        sku.is_active !== false &&
        (sku.available_stock ?? 0) >= minStock &&
        (sku.price_vnd ?? 0) >= minPriceVnd
      ) {
        return { product, sku };
      }
    }
  }
  return createApprovedSellerProduct(sellerClient, adminClient, tag);
}

describe("Wave 2b Web E2E — Promotions, Reports/CSV, Bank OTP, Real 404 & Favicon", () => {
  test("1. Real HTTP 404 on unknown dynamic slugs, favicon HTTP 200, and mock preview banners", async () => {
    await ensureRateBudget(20, { label: "w2b-404-favicon" });
    const anon = new WebClient();

    for (const badPath of [
      "/bo-suu-tap/khong-ton-tai-w2b-e2e",
      "/tap-chi/khong-ton-tai-w2b-e2e",
      "/chien-dich/khong-ton-tai-w2b-e2e",
    ]) {
      const res = await anon.page(badPath);
      assert.equal(
        res.status,
        404,
        `expected real HTTP 404 for ${badPath}, got ${res.status}`
      );
    }

    const icoRes = await anon.request("GET", "/favicon.ico", { accept: "*/*" });
    assert.equal(icoRes.status, 200, `expected /favicon.ico 200, got ${icoRes.status}`);
    assert.ok(icoRes.text.length > 0, "/favicon.ico body should be non-empty");

    const svgRes = await anon.request("GET", "/icon.svg", { accept: "image/svg+xml,*/*" });
    assert.equal(svgRes.status, 200, `expected /icon.svg 200, got ${svgRes.status}`);
    assert.ok(svgRes.text.includes("<svg"), "/icon.svg should return valid SVG markup");

    const hoiVienRes = await anon.page("/hoi-vien");
    assert.equal(hoiVienRes.status, 200);
    assert.ok(
      hoiVienRes.text.includes("Bản xem trước, dữ liệu mẫu"),
      "/hoi-vien must render preview notice banner"
    );
  });

  test("2. Shop discount program lifecycle: create -> PDP shows real strikethrough -> checkout computes exact discounted VND -> end program", async () => {
    await ensureRateBudget(40, { label: "w2b-shop-discount-lifecycle" });
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const shopMeRes = await seller.json("GET", "/api/gw/shops/me");
    assert.equal(shopMeRes.status, 200, `shops/me failed: ${shopMeRes.text}`);
    const sellerShopId = shopMeRes.data?.shop?.id;
    assert.ok(sellerShopId, `missing shop.id: ${shopMeRes.text}`);

    const [existingFsRes, existingDiscRes] = await Promise.all([
      seller.json("GET", "/api/gw/flash-sales?include_upcoming=true"),
      seller.json(
        "GET",
        `/api/gw/shop-discounts/active?shop_id=${encodeURIComponent(sellerShopId)}`
      ),
    ]);
    assert.equal(
      existingDiscRes.status,
      200,
      `shop-discounts/active?shop_id failed: ${existingDiscRes.status} ${existingDiscRes.text}`
    );
    const busySkuIds = new Set();
    for (const fs of existingFsRes.data?.flash_sales ?? []) {
      for (const it of fs.items ?? []) {
        if (it?.sku_id) busySkuIds.add(it.sku_id);
      }
    }
    for (const d of existingDiscRes.data?.discounts ?? []) {
      if (d?.sku_id) busySkuIds.add(d.sku_id);
    }
    const { product, sku } = await pickSellerActiveSku(
      seller,
      admin,
      "disc",
      100_000,
      4,
      busySkuIds,
      true
    );

    const regularPrice = sku.price_vnd;
    const discountedPrice = Math.trunc((regularPrice * 80) / 100);
    const expectedDiscount = regularPrice - discountedPrice;
    const now = Date.now();
    let programId = null;

    try {
      const createRes = await seller.json("POST", "/api/gw/seller/discount-programs", {
        body: {
          name: `CT Giam Gia E2E ${crypto.randomBytes(3).toString("hex")}`,
          starts_at: new Date(now - 2 * 60 * 1000).toISOString(),
          ends_at: new Date(now + 2 * 3600 * 1000).toISOString(),
          items: [
            {
              sku_id: sku.id,
              discounted_price_vnd: discountedPrice,
              per_user_limit: 5,
            },
          ],
        },
      });
      assert.ok(
        createRes.status === 200 || createRes.status === 201,
        `create shop discount program failed (${createRes.status}): ${createRes.text}`
      );
      programId = createRes.data?.program?.id;
      const programItemId = createRes.data?.program?.items?.[0]?.id;
      assert.ok(programId, `missing program.id: ${createRes.text}`);
      assert.ok(programItemId, `missing program item id: ${createRes.text}`);

      // Active shop discount endpoint must return this SKU
      const activeRes = await seller.json(
        "GET",
        `/api/gw/shop-discounts/active?sku_ids=${encodeURIComponent(sku.id)}`
      );
      assert.equal(activeRes.status, 200, `active shop discounts failed: ${activeRes.text}`);
      const activeMatch = (activeRes.data?.discounts ?? []).find((d) => d.sku_id === sku.id);
      assert.ok(activeMatch, `expected active discount for SKU ${sku.id}: ${activeRes.text}`);
      assert.equal(activeMatch.item_id, programItemId);
      assert.equal(activeMatch.discounted_price_vnd, discountedPrice);
      assert.equal(activeMatch.original_price_vnd, regularPrice);

      // PDP SSR HTML must display both discounted price and original strikethrough price
      const buyer = new WebClient();
      const pdpRes = await buyer.page(`/san-pham/${encodeURIComponent(product.slug)}`);
      assert.equal(pdpRes.status, 200, `PDP failed: ${pdpRes.status}`);
      assert.ok(
        pdpRes.text.includes(formatVndDots(discountedPrice)),
        `PDP should display discounted price ${formatVndDots(discountedPrice)}`
      );
      assert.ok(
        pdpRes.text.includes(formatVndDots(regularPrice)),
        `PDP should display original strikethrough price ${formatVndDots(regularPrice)}`
      );

      // Register fresh buyer, add SKU to cart, preview & full-cart checkout (server resolves shop discount)
      const buyerAcct = await registerAndLoginBuyer(buyer, "w2b-shopdisc", { verifyPhone: true });
      const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
      const quote = await calculateShippingQuote(buyer, {
        shopId: product.shop_id,
        province,
        ward,
        weightGrams: sku.weight_grams || 300,
        codAmountVnd: discountedPrice,
      });

      const addCartRes = await buyer.json("POST", "/api/gw/cart/items", {
        body: { sku_id: sku.id, quantity: 1 },
      });
      assert.ok(
        addCartRes.status === 200 || addCartRes.status === 201,
        `add cart failed: ${addCartRes.status} ${addCartRes.text}`
      );

      // Preview & full-cart checkout omit server-ignored fields (shop_program_item_id, unit_price_vnd, subtotal_vnd);
      // server resolves active shop discount programs authoritatively.
      const previewRes = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: {
          shop_items: [
            {
              shop_id: product.shop_id,
              shipping_fee_vnd: quote.fee_vnd,
              items: [{ sku_id: sku.id, quantity: 1 }],
            },
          ],
          shop_voucher_ids: {},
          platform_voucher_id: "",
          freeship_voucher_id: "",
          coins_to_use: 0,
        },
      });
      assert.equal(previewRes.status, 200, `preview failed: ${previewRes.text}`);
      assert.equal(
        previewRes.data?.breakdown?.total_shop_program_discount_vnd,
        expectedDiscount,
        `expected shop program discount ${expectedDiscount}, got ${previewRes.text}`
      );

      const checkoutRes = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `w2b-disc-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: true,
          coins_to_use: 0,
          payment_method: "COD",
          shipping_choices: [
            {
              shop_id: product.shop_id,
              carrier_code: quote.carrier_code,
              service_code: quote.service_code,
            },
          ],
        },
      });
      assert.ok(
        checkoutRes.status === 200 || checkoutRes.status === 201,
        `checkout failed (${checkoutRes.status}): ${checkoutRes.text}`
      );
      const order = checkoutRes.data?.orders?.[0];
      assert.ok(order, `missing order in checkout response: ${checkoutRes.text}`);
      assert.equal(order.shop_program_discount_vnd, expectedDiscount);
      const expectedPayable =
        order.subtotal_vnd -
        (order.shop_program_discount_vnd ?? 0) -
        (order.shop_freeship_discount_vnd ?? 0) +
        order.shipping_fee_vnd;
      assert.equal(
        order.total_payable_vnd,
        expectedPayable,
        `order total_payable_vnd mismatch: got ${order.total_payable_vnd}, expected ${expectedPayable}`
      );
    } finally {
      if (programId) {
        await seller
          .json("POST", `/api/gw/seller/discount-programs/${encodeURIComponent(programId)}/end`)
          .catch(() => {});
      }
    }
  });

  test("3. Flash sale in cart & checkout: live slot price, COD disallowed, VietQR succeeds with 5m expiry", async () => {
    await ensureRateBudget(45, { label: "w2b-flash-sale" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);

    // Exclude SKUs already registered in active/upcoming flash sales so we don't hit 409 overlap
    const existingFsRes = await seller.json("GET", "/api/gw/flash-sales?include_upcoming=true");
    const busySkuIds = new Set();
    for (const fs of existingFsRes.data?.flash_sales ?? []) {
      for (const it of fs.items ?? []) {
        if (it?.sku_id) busySkuIds.add(it.sku_id);
      }
    }
    const { product, sku } = await pickSellerActiveSku(
      seller,
      admin,
      "flash",
      100_000,
      4,
      busySkuIds,
      true
    );

    const regularPrice = sku.price_vnd;
    const flashPrice = Math.trunc((regularPrice * 70) / 100);
    const expectedFlashDiscount = regularPrice - flashPrice;
    const now = Date.now();

    const fsCreateRes = await admin.json("POST", "/api/gw/admin/flash-sales", {
      body: {
        name: `Flash E2E ${crypto.randomBytes(3).toString("hex")}`,
        starts_at: new Date(now - 60 * 1000).toISOString(),
        ends_at: new Date(now + 45 * 60 * 1000).toISOString(),
        min_discount_percent: 10,
      },
    });
    assert.ok(
      fsCreateRes.status === 200 || fsCreateRes.status === 201,
      `admin create flash sale failed (${fsCreateRes.status}): ${fsCreateRes.text}`
    );
    const flashSaleId = fsCreateRes.data?.flash_sale?.id;
    assert.ok(flashSaleId, `missing flash_sale.id: ${fsCreateRes.text}`);

    const regItemRes = await seller.json(
      "POST",
      `/api/gw/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`,
      {
        body: {
          sku_id: sku.id,
          flash_price_vnd: flashPrice,
          total_slots: 3,
          per_user_limit: 1,
        },
      }
    );
    assert.ok(
      regItemRes.status === 200 || regItemRes.status === 201,
      `seller register flash item failed (${regItemRes.status}): ${regItemRes.text}`
    );
    const flashItemId = regItemRes.data?.item?.id;
    assert.ok(flashItemId, `missing flash item id: ${regItemRes.text}`);

    // Seller flash items list should include this item
    const sellerItemsRes = await seller.json(
      "GET",
      `/api/gw/seller/flash-sales/items?flash_sale_id=${encodeURIComponent(flashSaleId)}&page=1&page_size=20`
    );
    assert.equal(sellerItemsRes.status, 200, `seller flash items failed: ${sellerItemsRes.text}`);
    assert.ok(
      (sellerItemsRes.data?.items ?? []).some((it) => it.id === flashItemId),
      "seller flash items list should contain newly registered item"
    );

    // Admin approves flash item
    const approveRes = await admin.json(
      "POST",
      `/api/gw/admin/flash-sale-items/${encodeURIComponent(flashItemId)}/approve`,
      {
        body: { approve: true, reason: "E2E approved" },
      }
    );
    assert.equal(approveRes.status, 200, `admin approve flash item failed: ${approveRes.text}`);

    // Buyer adds to cart & previews
    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w2b-flash", { verifyPhone: true });
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const quote = await calculateShippingQuote(buyer, {
      shopId: product.shop_id,
      province,
      ward,
      weightGrams: sku.weight_grams || 300,
      codAmountVnd: 0,
    });

    await buyer.json("POST", "/api/gw/cart/items", {
      body: { sku_id: sku.id, quantity: 1 },
    });

    // Attempt COD with flash_sale_item_id -> MUST be rejected (HTTP 409 FAILED_PRECONDITION or 400)
    const codAttempt = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `w2b-flash-cod-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: true,
        items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
        coins_to_use: 0,
        payment_method: "COD",
        shipping_choices: [
          {
            shop_id: product.shop_id,
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        ],
      },
    });
    assert.ok(
      codAttempt.status === 409 || codAttempt.status === 400,
      `COD checkout with flash sale must be rejected with 409/400, got ${codAttempt.status}: ${codAttempt.text}`
    );

    // VietQR checkout with flash_sale_item_id -> succeeds with ~5m payment expiry
    const beforeCheckoutMs = Date.now();
    const qrRes = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `w2b-flash-qr-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: true,
        items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
        coins_to_use: 0,
        payment_method: "VIETQR",
        shipping_choices: [
          {
            shop_id: product.shop_id,
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        ],
      },
    });
    assert.ok(
      qrRes.status === 200 || qrRes.status === 201,
      `VietQR flash checkout failed (${qrRes.status}): ${qrRes.text}`
    );
    const order = qrRes.data?.orders?.[0];
    assert.ok(order, `missing order: ${qrRes.text}`);
    assert.equal(order.flash_sale_discount_vnd, expectedFlashDiscount);
    if (qrRes.data?.payment_expires_at) {
      const ttlMs = Date.parse(qrRes.data.payment_expires_at) - beforeCheckoutMs;
      assert.ok(
        ttlMs > 0 && ttlMs <= 6 * 60 * 1000,
        `flash sale payment TTL should be ~5 minutes, got ${Math.round(ttlMs / 1000)}s`
      );
    }
  });

  test("4. Seller & Admin reports + CSV export (Content-Disposition forwarded) + >366d validation", async () => {
    await ensureRateBudget(25, { label: "w2b-reports-csv" });
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    const fromDate = formatIsoDateOffset(-29);
    const toDate = formatIsoDateOffset(0);

    const sellerRepRes = await seller.json(
      "GET",
      `/api/gw/seller/reports?from_date=${fromDate}&to_date=${toDate}&top_limit=5`
    );
    assert.equal(sellerRepRes.status, 200, `seller reports failed: ${sellerRepRes.text}`);
    const sellerRep = sellerRepRes.data?.report ?? sellerRepRes.data;
    assert.equal(typeof sellerRep?.gmv_vnd, "number", `missing seller gmv_vnd: ${sellerRepRes.text}`);
    assert.ok(Array.isArray(sellerRep?.daily), `missing seller daily array: ${sellerRepRes.text}`);

    const sellerCsvRes = await seller.request(
      "GET",
      `/api/gw/seller/reports/export?from_date=${fromDate}&to_date=${toDate}`,
      { accept: "text/csv,*/*" }
    );
    assert.equal(sellerCsvRes.status, 200, `seller CSV failed: ${sellerCsvRes.status}`);
    const sellerDisp = sellerCsvRes.headers.get("content-disposition") ?? "";
    assert.ok(
      sellerDisp.toLowerCase().includes("attachment"),
      `BFF must forward Content-Disposition attachment header, got: ${sellerDisp}`
    );
    assert.ok(sellerCsvRes.text.trim().length > 0, "seller CSV body must be non-empty");

    // > 366 days must return 400
    const badRangeRes = await seller.json(
      "GET",
      "/api/gw/seller/reports?from_date=2024-01-01&to_date=2026-12-31"
    );
    assert.equal(badRangeRes.status, 400, `expected 400 for >366d range, got ${badRangeRes.status}`);

    const adminRepRes = await admin.json(
      "GET",
      `/api/gw/admin/reports?from_date=${fromDate}&to_date=${toDate}&top_limit=5`
    );
    assert.equal(adminRepRes.status, 200, `admin reports failed: ${adminRepRes.text}`);
    const adminRep = adminRepRes.data?.report ?? adminRepRes.data;
    assert.equal(typeof adminRep?.gmv_vnd, "number", `missing admin gmv_vnd: ${adminRepRes.text}`);
    assert.ok(adminRep?.user_stats, `admin reports must include user_stats: ${adminRepRes.text}`);
    assert.equal(typeof adminRep.user_stats.total_users, "number");

    const adminCsvRes = await admin.request(
      "GET",
      `/api/gw/admin/reports/export?from_date=${fromDate}&to_date=${toDate}`,
      { accept: "text/csv,*/*" }
    );
    assert.equal(adminCsvRes.status, 200, `admin CSV failed: ${adminCsvRes.status}`);
    const adminDisp = adminCsvRes.headers.get("content-disposition") ?? "";
    assert.ok(
      adminDisp.toLowerCase().includes("attachment"),
      `admin CSV Content-Disposition missing attachment, got: ${adminDisp}`
    );
  });

  test("5. Seller bank account registration + OTP via Mailpit + Admin review + 24h lock on change & seller ledger", async () => {
    await ensureRateBudget(40, { label: "w2b-bank-otp" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    // Provision a dedicated fresh seller + approved shop so bank OTP quota is isolated across runs
    const seller = new WebClient();
    const sellerAcct = await registerAndLoginBuyer(seller, "w2b-bank");
    const { address: pickupAddr } = await createBuyerAddress(seller, sellerAcct);

    const shopSuffix = crypto.randomBytes(3).toString("hex");
    const regShopRes = await seller.json("POST", "/api/gw/shops/register", {
      body: {
        name: `Xưởng Bank E2E ${shopSuffix}`,
        slug: `xuong-bank-e2e-${shopSuffix}`,
        description: "Gian hàng kiểm thử tài khoản ngân hàng Wave 2b",
        pickup_address_id: pickupAddr.id,
        contact_phone: sellerAcct.phone,
        business_license_doc: `GPKD-W2B-${shopSuffix}`,
      },
    });
    assert.ok(
      regShopRes.status === 200 || regShopRes.status === 201,
      `register shop failed (${regShopRes.status}): ${regShopRes.text}`
    );
    const shopId = regShopRes.data?.shop?.id;
    assert.ok(shopId, `missing shop.id: ${regShopRes.text}`);

    const approveShopRes = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shopId)}/review`,
      { body: { decision: "APPROVE", reason: "E2E Wave 2b bank test shop" } }
    );
    assert.equal(approveShopRes.status, 200, `approve shop failed: ${approveShopRes.text}`);

    // Re-login to refresh JWT with seller role + shop_id claims
    const reloginRes = await seller.json("POST", "/api/auth/login", {
      body: { email: sellerAcct.email, password: sellerAcct.password },
    });
    assert.equal(reloginRes.status, 200, `seller relogin failed: ${reloginRes.text}`);

    // Step A: Register first bank account with mismatched holder name -> PENDING_OTP -> OTP -> PENDING_ADMIN_REVIEW -> Admin APPROVE -> VERIFIED
    const seenMailIds = await snapshotMailpitIds(sellerAcct.email);
    const saveRes1 = await seller.json("POST", "/api/gw/seller/bank-account", {
      body: {
        bank_code: "VCB",
        bank_name: "Vietcombank",
        account_number: `012345${crypto.randomInt(1000, 9999)}`,
        account_holder_name: "NGUYEN VAN E2E KHAC TEN",
      },
    });
    assert.ok(
      saveRes1.status === 200 || saveRes1.status === 201,
      `seller bank-account #1 failed (${saveRes1.status}): ${saveRes1.text}`
    );
    assert.equal(saveRes1.data?.bank_account?.status, "PENDING_OTP");

    const otp1 = await waitForRecentOtp(sellerAcct.email, seenMailIds);
    const verifyRes1 = await seller.json("POST", "/api/gw/seller/bank-account/verify-otp", {
      body: { otp: otp1 },
    });
    assert.equal(verifyRes1.status, 200, `bank verify-otp #1 failed: ${verifyRes1.text}`);
    const afterOtp1 = verifyRes1.data?.bank_account;
    assert.equal(afterOtp1?.status, "PENDING_ADMIN_REVIEW");

    // Admin shops?bank_review_pending=true filter must surface this shop with bank_review_pending=true
    const pendingBankList = await admin.json(
      "GET",
      "/api/gw/admin/shops?bank_review_pending=true&page=1&page_size=100"
    );
    assert.equal(pendingBankList.status, 200, `bank_review_pending filter failed: ${pendingBankList.text}`);
    const flaggedShop = (pendingBankList.data?.items ?? []).find((s) => s.id === shopId);
    assert.ok(flaggedShop && flaggedShop.bank_review_pending === true, `expected shop ${shopId} in bank_review_pending list`);

    const inspectRes = await admin.json(
      "GET",
      `/api/gw/admin/shops/${encodeURIComponent(shopId)}/bank-account`
    );
    assert.equal(inspectRes.status, 200, `admin inspect bank failed: ${inspectRes.text}`);
    const reviewRes1 = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shopId)}/bank-account/review`,
      {
        body: {
          bank_account_id: afterOtp1.id,
          decision: "APPROVE",
          reason: "E2E verified bank ownership",
        },
      }
    );
    assert.equal(reviewRes1.status, 200, `admin review bank #1 failed: ${reviewRes1.text}`);
    assert.equal(reviewRes1.data?.bank_account?.status, "VERIFIED");

    // Step B: Change an already-verified bank account (matching owner name) -> OTP -> VERIFIED + 24h payout_locked_until
    const saveRes2 = await seller.json("POST", "/api/gw/seller/bank-account", {
      body: {
        bank_code: "TCB",
        bank_name: "Techcombank",
        account_number: `098765${crypto.randomInt(1000, 9999)}`,
        account_holder_name: sellerAcct.fullName,
      },
    });
    assert.ok(
      saveRes2.status === 200 || saveRes2.status === 201,
      `seller bank-account #2 failed (${saveRes2.status}): ${saveRes2.text}`
    );
    const otp2 = await waitForRecentOtp(sellerAcct.email, seenMailIds);
    const verifyRes2 = await seller.json("POST", "/api/gw/seller/bank-account/verify-otp", {
      body: { otp: otp2 },
    });
    assert.equal(verifyRes2.status, 200, `bank verify-otp #2 failed: ${verifyRes2.text}`);
    let bank2 = verifyRes2.data?.bank_account;
    if (bank2?.status === "PENDING_ADMIN_REVIEW") {
      const reviewRes2 = await admin.json(
        "POST",
        `/api/gw/admin/shops/${encodeURIComponent(shopId)}/bank-account/review`,
        {
          body: {
            bank_account_id: bank2.id,
            decision: "APPROVE",
            reason: "E2E approved bank change",
          },
        }
      );
      assert.equal(reviewRes2.status, 200, `admin review bank #2 failed: ${reviewRes2.text}`);
      bank2 = reviewRes2.data?.bank_account;
    }
    assert.equal(bank2?.status, "VERIFIED");
    assert.ok(
      bank2?.payout_locked_until && Date.parse(bank2.payout_locked_until) > Date.now(),
      `changing a verified bank account must set future payout_locked_until, got: ${JSON.stringify(bank2)}`
    );

    // Seller ledger endpoint must return 200
    const ledgerRes = await seller.json("GET", "/api/gw/seller/ledger?page=1&page_size=20");
    assert.equal(ledgerRes.status, 200, `seller ledger failed: ${ledgerRes.text}`);
    assert.ok(Array.isArray(ledgerRes.data?.items));
  });

  test("6. Shop discount program per_user_limit exhausted -> preview excludes applied_shop_program_item_ids -> second order succeeds at regular price", async () => {
    await ensureRateBudget(45, { label: "w2b-shop-disc-limit" });
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { product, sku } = await createApprovedSellerProduct(seller, admin, "disclimit");

    const regularPrice = sku.price_vnd;
    const discountedPrice = Math.trunc((regularPrice * 80) / 100);
    const expectedDiscount = regularPrice - discountedPrice;
    const now = Date.now();
    let programId = null;

    try {
      const createRes = await seller.json("POST", "/api/gw/seller/discount-programs", {
        body: {
          name: `CT Gioi Han E2E ${crypto.randomBytes(3).toString("hex")}`,
          starts_at: new Date(now - 2 * 60 * 1000).toISOString(),
          ends_at: new Date(now + 2 * 3600 * 1000).toISOString(),
          items: [
            {
              sku_id: sku.id,
              discounted_price_vnd: discountedPrice,
              per_user_limit: 1,
            },
          ],
        },
      });
      assert.ok(
        createRes.status === 200 || createRes.status === 201,
        `create shop program failed (${createRes.status}): ${createRes.text}`
      );
      programId = createRes.data?.program?.id;
      const programItemId = createRes.data?.program?.items?.[0]?.id;
      assert.ok(programId && programItemId);

      const buyer = new WebClient();
      const buyerAcct = await registerAndLoginBuyer(buyer, "w2b-disclimit", { verifyPhone: true });
      const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
      // Calculate real shop-origin shipping fee (omitting from_province_code, matching CheckoutClient)
      const feeRes = await buyer.json("POST", "/api/gw/shipping/calculate-fee", {
        body: {
          shop_id: product.shop_id,
          to_province_code: province.code,
          to_ward_code: ward.code,
          weight_grams: Math.max(sku.weight_grams || 250, 100),
          cod_amount_vnd: regularPrice,
        },
      });
      assert.equal(feeRes.status, 200, `calculate-fee failed: ${feeRes.text}`);
      const quotes = feeRes.data?.quotes ?? [];
      assert.ok(quotes.length > 0, `no shipping quotes: ${feeRes.text}`);
      const quote = quotes.find((q) => String(q.carrier_code).toLowerCase() === "ghn") ?? quotes[0];

      // Order 1 (within per_user_limit=1): preview applies programItemId & checkout discounts order
      const prev1 = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: {
          shop_items: [
            {
              shop_id: product.shop_id,
              shipping_fee_vnd: quote.fee_vnd,
              items: [{ sku_id: sku.id, quantity: 1 }],
            },
          ],
          shop_voucher_ids: {},
          platform_voucher_id: "",
          freeship_voucher_id: "",
          coins_to_use: 0,
        },
      });
      assert.equal(prev1.status, 200, `preview #1 failed: ${prev1.text}`);
      const shopBd1 = (prev1.data?.breakdown?.shops ?? []).find(
        (s) => s.shop_id === product.shop_id
      );
      assert.ok(
        Array.isArray(shopBd1?.applied_shop_program_item_ids) &&
          shopBd1.applied_shop_program_item_ids.includes(programItemId),
        `expected applied_shop_program_item_ids to include ${programItemId}: ${prev1.text}`
      );
      assert.equal(prev1.data?.breakdown?.total_shop_program_discount_vnd, expectedDiscount);

      const co1 = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `w2b-dl1-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          items: [{ sku_id: sku.id, quantity: 1 }],
          coins_to_use: 0,
          payment_method: "COD",
          shipping_choices: [
            {
              shop_id: product.shop_id,
              carrier_code: quote.carrier_code,
              service_code: quote.service_code,
            },
          ],
        },
      });
      assert.ok(co1.status === 200 || co1.status === 201, `checkout #1 failed: ${co1.text}`);
      assert.equal(co1.data?.orders?.[0]?.shop_program_discount_vnd, expectedDiscount);

      // Order 2 (same buyer, limit=1 exhausted): preview excludes programItemId and returns regular price + warning
      const prev2 = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: {
          shop_items: [
            {
              shop_id: product.shop_id,
              shipping_fee_vnd: quote.fee_vnd,
              items: [{ sku_id: sku.id, quantity: 1 }],
            },
          ],
          shop_voucher_ids: {},
          platform_voucher_id: "",
          freeship_voucher_id: "",
          coins_to_use: 0,
        },
      });
      assert.equal(prev2.status, 200, `preview #2 failed: ${prev2.text}`);
      const shopBd2 = (prev2.data?.breakdown?.shops ?? []).find(
        (s) => s.shop_id === product.shop_id
      );
      assert.ok(
        Array.isArray(shopBd2?.applied_shop_program_item_ids) &&
          !shopBd2.applied_shop_program_item_ids.includes(programItemId),
        `exhausted per_user_limit must exclude ${programItemId} from applied_shop_program_item_ids: ${prev2.text}`
      );
      assert.equal(prev2.data?.breakdown?.total_shop_program_discount_vnd, 0);
      assert.ok(
        (prev2.data?.breakdown?.warnings ?? []).length > 0,
        `expected per_user_limit warning in preview #2: ${prev2.text}`
      );

      // Checkout #2 succeeds at regular price matching preview and returns warnings[]
      const co2 = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `w2b-dl2-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          items: [{ sku_id: sku.id, quantity: 1 }],
          coins_to_use: 0,
          payment_method: "COD",
          shipping_choices: [
            {
              shop_id: product.shop_id,
              carrier_code: quote.carrier_code,
              service_code: quote.service_code,
            },
          ],
        },
      });
      assert.ok(co2.status === 200 || co2.status === 201, `checkout #2 failed: ${co2.text}`);
      assert.ok(
        Array.isArray(co2.data?.warnings) && co2.data.warnings.length > 0,
        `checkout #2 exceeding shop program limit must return warnings[]: ${co2.text}`
      );
      const ord2 = co2.data?.orders?.[0];
      assert.equal(ord2?.shop_program_discount_vnd, 0);
      assert.equal(
        ord2?.total_payable_vnd,
        prev2.data?.breakdown?.final_payable_vnd,
        `order #2 total_payable_vnd must match preview #2 regular-price total`
      );
    } finally {
      if (programId) {
        await seller
          .json("POST", `/api/gw/seller/discount-programs/${encodeURIComponent(programId)}/end`)
          .catch(() => {});
      }
    }
  });

  test("7. Flash sale per_user_limit exhausted -> flash checkout rejected -> opt-out to regular price (COD unlocked & succeeds)", async () => {
    await ensureRateBudget(50, { label: "w2b-flash-limit-optout" });
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { product, sku } = await createApprovedSellerProduct(seller, admin, "flashlimit");

    const regularPrice = sku.price_vnd;
    const flashPrice = Math.trunc((regularPrice * 70) / 100);
    const expectedFlashDiscount = regularPrice - flashPrice;
    const now = Date.now();

    const fsRes = await admin.json("POST", "/api/gw/admin/flash-sales", {
      body: {
        name: `Flash Limit E2E ${crypto.randomBytes(3).toString("hex")}`,
        starts_at: new Date(now - 60 * 1000).toISOString(),
        ends_at: new Date(now + 45 * 60 * 1000).toISOString(),
        min_discount_percent: 10,
      },
    });
    assert.ok(fsRes.status === 200 || fsRes.status === 201, `create flash failed: ${fsRes.text}`);
    const flashSaleId = fsRes.data?.flash_sale?.id;

    const regRes = await seller.json(
      "POST",
      `/api/gw/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`,
      {
        body: {
          sku_id: sku.id,
          flash_price_vnd: flashPrice,
          total_slots: 5,
          per_user_limit: 1,
        },
      }
    );
    assert.ok(regRes.status === 200 || regRes.status === 201, `reg flash failed: ${regRes.text}`);
    const flashItemId = regRes.data?.item?.id;

    const appRes = await admin.json(
      "POST",
      `/api/gw/admin/flash-sale-items/${encodeURIComponent(flashItemId)}/approve`,
      { body: { approve: true, reason: "E2E approved" } }
    );
    assert.equal(appRes.status, 200, `approve flash failed: ${appRes.text}`);

    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w2b-flashlim", { verifyPhone: true });
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const quote = await calculateShippingQuote(buyer, {
      shopId: product.shop_id,
      province,
      ward,
      weightGrams: sku.weight_grams || 250,
      codAmountVnd: 0,
    });

    // Order 1 consumes per_user_limit=1 via VIETQR
    const co1 = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `w2b-fl1-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
        coins_to_use: 0,
        payment_method: "VIETQR",
        shipping_choices: [
          {
            shop_id: product.shop_id,
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        ],
      },
    });
    assert.ok(co1.status === 200 || co1.status === 201, `flash order #1 failed: ${co1.text}`);
    assert.equal(co1.data?.orders?.[0]?.flash_sale_discount_vnd, expectedFlashDiscount);

    // Order 2 WITH flash_sale_item_id: preview shows 0 flash discount (shortfall), and checkout is rejected
    const prevExhausted = await buyer.json("POST", "/api/gw/promotions/preview", {
      body: {
        shop_items: [
          {
            shop_id: product.shop_id,
            shipping_fee_vnd: quote.fee_vnd,
            items: [
              {
                sku_id: sku.id,
                quantity: 1,
                flash_sale_item_id: flashItemId,
              },
            ],
          },
        ],
        shop_voucher_ids: {},
        platform_voucher_id: "",
        freeship_voucher_id: "",
        coins_to_use: 0,
      },
    });
    assert.equal(prevExhausted.status, 200, `preview exhausted flash failed: ${prevExhausted.text}`);
    assert.equal(prevExhausted.data?.breakdown?.total_flash_sale_discount_vnd, 0);

    const coFlashRejected = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `w2b-fl2-rej-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [{ sku_id: sku.id, quantity: 1, flash_sale_item_id: flashItemId }],
        coins_to_use: 0,
        payment_method: "VIETQR",
        shipping_choices: [
          {
            shop_id: product.shop_id,
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        ],
      },
    });
    assert.ok(
      coFlashRejected.status === 409 ||
        coFlashRejected.status === 429 ||
        coFlashRejected.status === 400,
      `second flash checkout exceeding per_user_limit must be rejected, got ${coFlashRejected.status}: ${coFlashRejected.text}`
    );

    // Buyer clicks "Mua giá thường" -> omits flash_sale_item_id -> COD is unlocked and succeeds at regular price
    const coRegular = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `w2b-fl2-reg-${crypto.randomUUID()}` },
      body: {
        address_id: address.id,
        from_cart: false,
        items: [{ sku_id: sku.id, quantity: 1 }],
        coins_to_use: 0,
        payment_method: "COD",
        shipping_choices: [
          {
            shop_id: product.shop_id,
            carrier_code: quote.carrier_code,
            service_code: quote.service_code,
          },
        ],
      },
    });
    assert.ok(
      coRegular.status === 200 || coRegular.status === 201,
      `regular-price COD checkout after flash opt-out failed (${coRegular.status}): ${coRegular.text}`
    );
    const ordReg = coRegular.data?.orders?.[0];
    assert.equal(ordReg?.flash_sale_discount_vnd, 0);
    assert.equal(ordReg?.payment_method, "COD");
    assert.equal(
      ordReg?.total_payable_vnd,
      ordReg.subtotal_vnd + ordReg.shipping_fee_vnd - (ordReg.shop_freeship_discount_vnd ?? 0)
    );

    // Overlapping active shop discount program on same SKU: omitting flash_sale_item_id applies shop discount automatically
    const shopDiscPrice = Math.trunc((regularPrice * 85) / 100);
    const expectedShopDisc = regularPrice - shopDiscPrice;
    let overlapProgramId = null;
    try {
      const overlapRes = await seller.json("POST", "/api/gw/seller/discount-programs", {
        body: {
          name: `CT Fallback Flash E2E ${crypto.randomBytes(3).toString("hex")}`,
          starts_at: new Date(now - 2 * 60 * 1000).toISOString(),
          ends_at: new Date(now + 2 * 3600 * 1000).toISOString(),
          items: [{ sku_id: sku.id, discounted_price_vnd: shopDiscPrice, per_user_limit: 3 }],
        },
      });
      assert.ok(
        overlapRes.status === 200 || overlapRes.status === 201,
        `create overlapping shop discount failed (${overlapRes.status}): ${overlapRes.text}`
      );
      overlapProgramId = overlapRes.data?.program?.id;
      const overlapItemId = overlapRes.data?.program?.items?.[0]?.id;

      // Quote real shop-origin COD shipping fee via authoritative items[] (omitting from_province_code, matching CheckoutClient)
      const codFeeRes = await buyer.json("POST", "/api/gw/shipping/calculate-fee", {
        body: {
          shop_id: product.shop_id,
          to_province_code: province.code,
          to_ward_code: ward.code,
          payment_method: "COD",
          items: [{ sku_id: sku.id, quantity: 1 }],
        },
      });
      assert.equal(codFeeRes.status, 200, `COD calculate-fee failed: ${codFeeRes.text}`);
      const codQuotes = codFeeRes.data?.quotes ?? [];
      assert.ok(codQuotes.length > 0, `no COD shipping quotes: ${codFeeRes.text}`);
      const codQuote =
        codQuotes.find(
          (q) =>
            q.carrier_code === quote.carrier_code && q.service_code === quote.service_code
        ) ?? codQuotes[0];

      const prevOptOut = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: {
          shop_items: [
            {
              shop_id: product.shop_id,
              shipping_fee_vnd: codQuote.fee_vnd,
              items: [{ sku_id: sku.id, quantity: 1 }],
            },
          ],
          shop_voucher_ids: {},
          platform_voucher_id: "",
          freeship_voucher_id: "",
          coins_to_use: 0,
        },
      });
      assert.equal(prevOptOut.status, 200, `preview flash opt-out fallback failed: ${prevOptOut.text}`);
      const optOutShopBd = (prevOptOut.data?.breakdown?.shops ?? []).find(
        (s) => s.shop_id === product.shop_id
      );
      assert.equal(prevOptOut.data?.breakdown?.total_flash_sale_discount_vnd, 0);
      assert.equal(prevOptOut.data?.breakdown?.total_shop_program_discount_vnd, expectedShopDisc);
      assert.ok(
        Array.isArray(optOutShopBd?.applied_shop_program_item_ids) &&
          optOutShopBd.applied_shop_program_item_ids.includes(overlapItemId),
        `opt-out flash must apply active shop discount ${overlapItemId}: ${prevOptOut.text}`
      );

      const coOptOutShopDisc = await buyer.json("POST", "/api/gw/checkout", {
        headers: { "Idempotency-Key": `w2b-fl-shopdisc-${crypto.randomUUID()}` },
        body: {
          address_id: address.id,
          from_cart: false,
          items: [{ sku_id: sku.id, quantity: 1 }],
          coins_to_use: 0,
          payment_method: "COD",
          shipping_choices: [
            {
              shop_id: product.shop_id,
              carrier_code: codQuote.carrier_code,
              service_code: codQuote.service_code,
            },
          ],
        },
      });
      assert.ok(
        coOptOutShopDisc.status === 200 || coOptOutShopDisc.status === 201,
        `COD checkout falling back to shop discount failed (${coOptOutShopDisc.status}): ${coOptOutShopDisc.text}`
      );
      const ordOptOut = coOptOutShopDisc.data?.orders?.[0];
      assert.equal(ordOptOut?.flash_sale_discount_vnd, 0);
      assert.equal(ordOptOut?.shop_program_discount_vnd, expectedShopDisc);
      assert.equal(ordOptOut?.total_payable_vnd, prevOptOut.data?.breakdown?.final_payable_vnd);
    } finally {
      if (overlapProgramId) {
        await seller
          .json(
            "POST",
            `/api/gw/seller/discount-programs/${encodeURIComponent(overlapProgramId)}/end`
          )
          .catch(() => {});
      }
    }
  });
});
