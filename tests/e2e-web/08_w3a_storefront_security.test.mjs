import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  config,
  createBuyerAddress,
  ensureRateBudget,
  extractOtp,
  listCatalogProducts,
  pickSingleShopSku,
  pollUntil,
  registerAndLoginBuyer,
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

function getBffError(res) {
  return {
    code: String(res.data?.error?.code ?? ""),
    message: String(res.data?.error?.message ?? ""),
  };
}

function hasObjectKeyDeep(value, targetKey) {
  if (!value || typeof value !== "object") return false;
  if (Array.isArray(value)) {
    return value.some((item) => hasObjectKeyDeep(item, targetKey));
  }
  for (const [k, v] of Object.entries(value)) {
    if (k === targetKey) return true;
    if (hasObjectKeyDeep(v, targetKey)) return true;
  }
  return false;
}

async function snapshotMailpitIds(email) {
  const target = email.trim().toLowerCase();
  const seen = new Set();
  const listRes = await fetch(`${config.mailpitUrl}/api/v1/messages?limit=100`, {
    signal: AbortSignal.timeout(config.requestTimeoutMs),
  });
  if (!listRes.ok) return seen;
  const list = await listRes.json();
  for (const summary of list?.messages ?? []) {
    const recipients = Array.isArray(summary?.To) ? summary.To : [];
    if (recipients.some((r) => String(r?.Address ?? "").trim().toLowerCase() === target)) {
      if (summary?.ID) seen.add(summary.ID);
    }
  }
  return seen;
}

async function waitForRecentOtp(email, seenIds = new Set()) {
  const target = email.trim().toLowerCase();
  return pollUntil(
    async () => {
      const listRes = await fetch(`${config.mailpitUrl}/api/v1/messages?limit=100`, {
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      if (!listRes.ok) {
        return { done: false, detail: `mailpit list HTTP ${listRes.status}` };
      }
      const list = await listRes.json();
      const messages = Array.isArray(list?.messages) ? list.messages : [];
      for (const summary of messages) {
        if (summary?.ID && seenIds.has(summary.ID)) continue;
        const recipients = Array.isArray(summary?.To) ? summary.To : [];
        const matched = recipients.some(
          (r) => String(r?.Address ?? "").trim().toLowerCase() === target
        );
        if (!matched) continue;

        const msgRes = await fetch(
          `${config.mailpitUrl}/api/v1/message/${encodeURIComponent(summary.ID)}`,
          { signal: AbortSignal.timeout(config.requestTimeoutMs) }
        );
        if (!msgRes.ok) continue;
        const full = await msgRes.json();
        const otp = extractOtp(full?.Subject, full?.Text, full?.HTML, summary?.Snippet);
        if (otp) {
          if (summary?.ID) seenIds.add(summary.ID);
          return { done: true, value: otp };
        }
      }
      return { done: false, detail: `no new OTP email for ${target}` };
    },
    {
      timeoutMs: config.pollTimeoutMs,
      intervalMs: config.pollIntervalMs,
      label: `new OTP email for ${target}`,
    }
  );
}

async function createApprovedSellerProduct(
  sellerClient,
  adminClient,
  tag,
  priceVnd = 220_000,
  categoryIdOverride = null
) {
  let categoryId = categoryIdOverride;
  if (!categoryId) {
    const catRes = await sellerClient.json("GET", "/api/gw/categories");
    assert.equal(catRes.status, 200, `categories failed: ${catRes.text}`);
    const category = (catRes.data?.categories ?? [])[0];
    assert.ok(category?.id, `no categories available: ${catRes.text}`);
    categoryId = category.id;
  }

  const suffix = crypto.randomBytes(4).toString("hex");
  const createRes = await sellerClient.json("POST", "/api/gw/seller/products", {
    body: {
      category_id: categoryId,
      name: `San pham W3A ${tag} ${suffix}`,
      slug: `san-pham-w3a-${tag}-${suffix}`,
      description: "Sản phẩm kiểm thử tự động đợt 3a.",
      images: ["/products/ao-thun-cotton-trang.png"],
      skus: [
        {
          sku_code: `W3A-${tag.toUpperCase()}-${suffix.toUpperCase()}-M`,
          name: "Trắng tinh / M",
          attributes: { color: "Trắng tinh", color_code: "trang", size: "M" },
          price_vnd: priceVnd,
          stock: 25,
          weight_grams: 280,
          image_url: "/products/ao-thun-cotton-trang.png",
        },
      ],
    },
  });
  assert.ok(
    createRes.status === 200 || createRes.status === 201,
    `create W3A product failed (${createRes.status}): ${createRes.text}`
  );
  const created = createRes.data?.product;
  assert.ok(created?.id && created?.skus?.[0]?.id, `missing product/sku: ${createRes.text}`);

  const reviewRes = await adminClient.json(
    "POST",
    `/api/gw/admin/products/${encodeURIComponent(created.id)}/review`,
    {
      body: { decision: "APPROVE", reason: "E2E Wave 3a auto-approve" },
    }
  );
  assert.equal(reviewRes.status, 200, `approve W3A product failed: ${reviewRes.text}`);
  const approved = reviewRes.data?.product ?? created;
  return { product: approved, sku: approved.skus[0] };
}

async function deleteSellerProduct(sellerClient, productId) {
  if (!productId) return;
  await sellerClient
    .json("DELETE", `/api/gw/seller/products/${encodeURIComponent(productId)}`)
    .catch(() => {});
}

async function setSellerProductStatus(sellerClient, product, status) {
  const res = await sellerClient.json(
    "PUT",
    `/api/gw/seller/products/${encodeURIComponent(product.id)}`,
    {
      body: {
        category_id: product.category_id,
        name: product.name,
        slug: product.slug,
        description: product.description || "Sản phẩm kiểm thử tự động đợt 3a.",
        images: product.images?.length ? product.images : ["/products/ao-thun-cotton-trang.png"],
        status,
        skus: (product.skus ?? []).map((s) => ({
          id: s.id,
          sku_code: s.sku_code,
          name: s.name,
          attributes: s.attributes ?? {},
          price_vnd: s.price_vnd,
          stock: s.stock,
          weight_grams: s.weight_grams || 280,
          image_url: s.image_url || "/products/ao-thun-cotton-trang.png",
          is_active: s.is_active !== false,
        })),
      },
    }
  );
  assert.equal(
    res.status,
    200,
    `update product ${product.id} status=${status} expected 200, got ${res.status}: ${res.text}`
  );
  return res.data?.product ?? product;
}

describe("08. Wave 3a storefront, >100 products flash sale, D1/D6/D8/D11 & promo polish", () => {
  test("1. D8/A3: Flash sale & Buy Now resolve SKUs strictly outside page 1 (>100 products) via sku_ids and skip all-hidden live slots", async () => {
    await ensureRateBudget(40, { label: "w3a-d8-flash" });
    const anon = new WebClient();
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);

    const tooManySkus = Array.from({ length: 201 }, () => crypto.randomUUID()).join(",");
    const overLimitRes = await anon.json(
      "GET",
      `/api/gw/products?sku_ids=${encodeURIComponent(tooManySkus)}&page=1&page_size=10`
    );
    assert.equal(
      overLimitRes.status,
      400,
      `sku_ids > 200 must return 400, got ${overLimitRes.status}: ${overLimitRes.text}`
    );

    const shopMeRes = await seller.json("GET", "/api/gw/shops/me");
    assert.equal(shopMeRes.status, 200, `shops/me failed: ${shopMeRes.text}`);
    const sellerShopId = shopMeRes.data?.shop?.id;
    assert.ok(sellerShopId, `missing seller shop_id: ${shopMeRes.text}`);

    const catRes = await seller.json("GET", "/api/gw/categories");
    assert.equal(catRes.status, 200, `categories failed: ${catRes.text}`);
    const defaultCategoryId = (catRes.data?.categories ?? [])[0]?.id;
    assert.ok(defaultCategoryId, `missing default category: ${catRes.text}`);

    const createdTempProductIds = [];
    const pickEligibleSku = (prod, busySet) =>
      (prod.skus ?? []).find(
        (s) =>
          s.is_active !== false &&
          (s.available_stock ?? 0) >= 3 &&
          (s.price_vnd ?? 0) >= 50_000 &&
          !busySet.has(s.id)
      );

    try {
      const [initialPage1Res, existingFsRes, sellerProdsRes] = await Promise.all([
        anon.json("GET", "/api/gw/products?page=1&page_size=100"),
        seller.json("GET", "/api/gw/flash-sales?include_upcoming=true"),
        seller.json("GET", "/api/gw/seller/products?page=1&page_size=100"),
      ]);
      assert.equal(initialPage1Res.status, 200, `products page 1 failed: ${initialPage1Res.text}`);
      let catalogTotal = initialPage1Res.data?.total ?? 0;
      let page1Ids = new Set((initialPage1Res.data?.items ?? []).map((p) => p.id));
      console.log(`[w3a-d8] initial catalog total = ${catalogTotal} (page 1 = ${page1Ids.size})`);

      const busyFlashSkuIds = new Set();
      for (const fs of existingFsRes.data?.flash_sales ?? []) {
        for (const it of fs.items ?? []) {
          if (it?.sku_id) busyFlashSkuIds.add(it.sku_id);
        }
      }

      // Find an eligible seller product already outside page 1; if none (e.g. fresh seed <= 100),
      // pick the oldest eligible seller product and push it strictly onto page 2 with temporary fillers.
      const approvedSellerProds = (sellerProdsRes.data?.items ?? []).filter(
        (p) => String(p.status ?? "").toUpperCase() === "APPROVED"
      );
      let visible = null;
      for (const cand of approvedSellerProds) {
        if (page1Ids.has(cand.id)) continue;
        const sku = pickEligibleSku(cand, busyFlashSkuIds);
        if (sku) {
          visible = { product: cand, sku };
          break;
        }
      }

      if (!visible) {
        // Pick oldest eligible seller product (or create one first) so fillers push it past top 100
        const oldestSorted = [...approvedSellerProds].sort((a, b) =>
          String(a.created_at ?? "").localeCompare(String(b.created_at ?? ""))
        );
        for (const cand of oldestSorted) {
          const sku = pickEligibleSku(cand, busyFlashSkuIds);
          if (sku) {
            visible = { product: cand, sku };
            break;
          }
        }
        if (!visible) {
          const created = await createApprovedSellerProduct(
            seller,
            admin,
            "fs-vis-base",
            240_000,
            defaultCategoryId
          );
          createdTempProductIds.push(created.product.id);
          visible = created;
          catalogTotal += 1;
        }

        // Create approved filler products (created_at > visible) until visible is strictly outside page 1
        const fillersNeeded = Math.max(0, 102 - catalogTotal);
        for (let i = 0; i < fillersNeeded; i += 1) {
          if (i % 8 === 0) {
            await ensureRateBudget(35, { label: `w3a-d8-filler-${i}` });
          }
          const filler = await createApprovedSellerProduct(
            seller,
            admin,
            `fill-${i}`,
            190_000,
            defaultCategoryId
          );
          createdTempProductIds.push(filler.product.id);
        }

        const refreshedPage1 = await anon.json("GET", "/api/gw/products?page=1&page_size=100");
        assert.equal(refreshedPage1.status, 200);
        page1Ids = new Set((refreshedPage1.data?.items ?? []).map((p) => p.id));
        catalogTotal = refreshedPage1.data?.total ?? catalogTotal;

        // Top up a few extra fillers if visible was somehow still in page 1 (up to 105 safety cap)
        let extra = 0;
        while (page1Ids.has(visible.product.id) && extra < 105) {
          if (extra % 8 === 0) {
            await ensureRateBudget(35, { label: `w3a-d8-topup-${extra}` });
          }
          const filler = await createApprovedSellerProduct(
            seller,
            admin,
            `top-${extra}`,
            195_000,
            defaultCategoryId
          );
          createdTempProductIds.push(filler.product.id);
          extra += 1;
          if (extra % 10 === 0 || !page1Ids.has(visible.product.id)) {
            const p1Check = await anon.json("GET", "/api/gw/products?page=1&page_size=100");
            page1Ids = new Set((p1Check.data?.items ?? []).map((p) => p.id));
          }
        }
        const finalPage1 = await anon.json("GET", "/api/gw/products?page=1&page_size=100");
        page1Ids = new Set((finalPage1.data?.items ?? []).map((p) => p.id));
      }

      assert.equal(
        page1Ids.has(visible.product.id),
        false,
        `visible flash product ${visible.product.id} must be strictly outside page 1 (top 100) to prove sku_ids lookup`
      );

      // Create two products that will be hidden: one for an all-hidden earlier live slot, one inside the main live slot
      await ensureRateBudget(40, { label: "w3a-d8-flash-setup" });
      const hiddenOnly = await createApprovedSellerProduct(
        seller,
        admin,
        "fs-hidonly",
        255_000,
        defaultCategoryId
      );
      createdTempProductIds.push(hiddenOnly.product.id);
      const hidden = await createApprovedSellerProduct(
        seller,
        admin,
        "fs-hid",
        260_000,
        defaultCategoryId
      );
      createdTempProductIds.push(hidden.product.id);

      // Direct sku_ids lookup must return visible (outside page 1) + hidden products
      const batchRes = await anon.json(
        "GET",
        `/api/gw/products?sku_ids=${encodeURIComponent(`${visible.sku.id},${hidden.sku.id}`)}&page=1&page_size=10`
      );
      assert.equal(batchRes.status, 200, `products?sku_ids failed: ${batchRes.text}`);
      const batchIds = new Set((batchRes.data?.items ?? []).map((p) => p.id));
      assert.ok(batchIds.has(visible.product.id), "sku_ids query must return page-2 visible product");
      assert.ok(batchIds.has(hidden.product.id), "sku_ids query must return hidden product");

      const now = Date.now();
      // Live slot #1 (earlier starts_at): contains ONLY a product that will be hidden -> tests P2-13 empty live slot skipping
      const emptyLiveFsRes = await admin.json("POST", "/api/gw/admin/flash-sales", {
        body: {
          name: `Flash EmptyHidden W3A ${crypto.randomBytes(3).toString("hex")}`,
          starts_at: new Date(now - 10 * 60 * 1000).toISOString(),
          ends_at: new Date(now + 40 * 60 * 1000).toISOString(),
          min_discount_percent: 10,
        },
      });
      assert.ok(
        emptyLiveFsRes.status === 200 || emptyLiveFsRes.status === 201,
        `create empty-live flash sale failed (${emptyLiveFsRes.status}): ${emptyLiveFsRes.text}`
      );
      const emptyLiveFsId = emptyLiveFsRes.data?.flash_sale?.id;

      const regHidOnly = await seller.json(
        "POST",
        `/api/gw/seller/flash-sales/${encodeURIComponent(emptyLiveFsId)}/items`,
        {
          body: {
            sku_id: hiddenOnly.sku.id,
            flash_price_vnd: 178_000,
            total_slots: 5,
            per_user_limit: 2,
          },
        }
      );
      assert.ok(
        regHidOnly.status === 200 || regHidOnly.status === 201,
        `register hiddenOnly flash item failed: ${regHidOnly.status} ${regHidOnly.text}`
      );
      const hiddenOnlyFlashItemId = regHidOnly.data?.item?.id;

      // Live slot #2 (later starts_at): contains visible (outside top 100) + hidden
      const fsCreateRes = await admin.json("POST", "/api/gw/admin/flash-sales", {
        body: {
          name: `Flash W3A ${crypto.randomBytes(3).toString("hex")}`,
          starts_at: new Date(now - 60 * 1000).toISOString(),
          ends_at: new Date(now + 45 * 60 * 1000).toISOString(),
          min_discount_percent: 10,
        },
      });
      assert.ok(
        fsCreateRes.status === 200 || fsCreateRes.status === 201,
        `create flash sale failed (${fsCreateRes.status}): ${fsCreateRes.text}`
      );
      const flashSaleId = fsCreateRes.data?.flash_sale?.id;
      assert.ok(flashSaleId, `missing flash_sale.id: ${fsCreateRes.text}`);

      const visibleBasePrice = Math.min(
        visible.sku.price_vnd,
        visible.sku.min_price_30d_vnd || visible.sku.price_vnd
      );
      const visibleFlashPrice = Math.max(1_000, Math.trunc((visibleBasePrice * 70) / 100));
      const regVis = await seller.json(
        "POST",
        `/api/gw/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`,
        {
          body: {
            sku_id: visible.sku.id,
            flash_price_vnd: visibleFlashPrice,
            total_slots: Math.min(3, Math.max(1, visible.sku.available_stock ?? 3)),
            per_user_limit: 2,
          },
        }
      );
      assert.ok(
        regVis.status === 200 || regVis.status === 201,
        `register visible flash item failed: ${regVis.status} ${regVis.text}`
      );
      const visibleFlashItemId = regVis.data?.item?.id;
      assert.ok(visibleFlashItemId, `missing visibleFlashItemId: ${regVis.text}`);

      const regHid = await seller.json(
        "POST",
        `/api/gw/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`,
        {
          body: {
            sku_id: hidden.sku.id,
            flash_price_vnd: 182_000,
            total_slots: 5,
            per_user_limit: 2,
          },
        }
      );
      assert.ok(
        regHid.status === 200 || regHid.status === 201,
        `register hidden flash item failed: ${regHid.status} ${regHid.text}`
      );
      const hiddenFlashItemId = regHid.data?.item?.id;
      assert.ok(hiddenFlashItemId, `missing hiddenFlashItemId: ${regHid.text}`);

      for (const itemId of [hiddenOnlyFlashItemId, visibleFlashItemId, hiddenFlashItemId]) {
        const approveRes = await admin.json(
          "POST",
          `/api/gw/admin/flash-sale-items/${encodeURIComponent(itemId)}/approve`,
          { body: { approve: true, reason: "W3A E2E approved" } }
        );
        assert.equal(approveRes.status, 200, `approve flash item ${itemId} failed: ${approveRes.text}`);
      }

      // Hide both hidden products so slot #1 becomes an all-hidden empty live slot and slot #2 drops hidden
      await setSellerProductStatus(seller, hiddenOnly.product, "HIDDEN");
      await setSellerProductStatus(seller, hidden.product, "HIDDEN");

      // /flash-sale SSR must skip the earlier all-hidden live slot, default to slot with sellable items,
      // render the page-2 visible product via sku_ids, and omit hidden products
      const flashPageRes = await anon.page("/flash-sale");
      assert.equal(flashPageRes.status, 200, `/flash-sale expected 200, got ${flashPageRes.status}`);
      assert.ok(
        flashPageRes.text.includes(visible.product.name),
        `/flash-sale SSR must render beyond-page-1 flash product ${visible.product.name} via sku_ids`
      );
      assert.ok(
        !flashPageRes.text.includes(hidden.product.name) &&
          !flashPageRes.text.includes(hiddenOnly.product.name),
        "/flash-sale SSR must drop hidden flash products"
      );
      assert.ok(
        !flashPageRes.text.includes("Chưa có sản phẩm nào được mở bán trong khung giờ này"),
        "/flash-sale default active slot must skip earlier live slot whose SKUs are all hidden"
      );

      // Homepage SSR must skip the empty live slot, never include hidden products, and never show 0 items
      const homeRes = await anon.page("/");
      assert.equal(homeRes.status, 200, `/ expected 200, got ${homeRes.status}`);
      assert.ok(
        !homeRes.text.includes(hidden.product.name) &&
          !homeRes.text.includes(hiddenOnly.product.name),
        "homepage SSR must not include hidden flash products"
      );
      assert.ok(
        !homeRes.text.includes("0 sản phẩm ưu đãi"),
        "homepage live flash sale band must never display 0 sản phẩm ưu đãi"
      );

      // Buy Now SSR on /thanh-toan?sku=<beyond-page-1>&qty=1&flash=<item_id> must resolve SKU via sku_ids
      const buyer = new WebClient();
      await registerAndLoginBuyer(buyer, "w3a-buynow");
      const buyNowRes = await buyer.page(
        `/thanh-toan?sku=${encodeURIComponent(visible.sku.id)}&qty=1&flash=${encodeURIComponent(visibleFlashItemId)}`
      );
      assert.equal(buyNowRes.status, 200, `Buy Now /thanh-toan expected 200, got ${buyNowRes.status}`);
      assert.ok(
        buyNowRes.text.includes(visible.product.name),
        `Buy Now /thanh-toan must resolve beyond-page-1 product ${visible.product.name} via sku_ids`
      );
      assert.ok(
        !buyNowRes.text.includes("Sản phẩm mua ngay không còn tồn tại") &&
          !buyNowRes.text.includes("Sản phẩm chưa sẵn sàng để thanh toán"),
        "Buy Now /thanh-toan must not show not-found/unavailable error for valid active SKU outside page 1"
      );
    } finally {
      for (let i = 0; i < createdTempProductIds.length; i += 1) {
        if (i > 0 && i % 15 === 0) {
          await ensureRateBudget(25, { label: `w3a-d8-cleanup-${i}` });
        }
        await deleteSellerProduct(seller, createdTempProductIds[i]);
      }
    }
  });

  test("2. D1: Public categories/products & storefront SSR omit commission_rate_bps while /admin/categories keeps it for admin only", async () => {
    await ensureRateBudget(30, { label: "w3a-d1-commission" });
    const anon = new WebClient();
    const buyer = new WebClient();
    await registerAndLoginBuyer(buyer, "w3a-d1-buyer");
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    // Public categories & products must strip commission_rate_bps completely
    const pubCats = await anon.json("GET", "/api/gw/categories");
    assert.equal(pubCats.status, 200, `public categories failed: ${pubCats.text}`);
    assert.equal(
      hasObjectKeyDeep(pubCats.data, "commission_rate_bps"),
      false,
      "GET /api/gw/categories must not expose commission_rate_bps"
    );

    const pubProds = await anon.json("GET", "/api/gw/products?page=1&page_size=10");
    assert.equal(pubProds.status, 200, `public products failed: ${pubProds.text}`);
    assert.equal(
      hasObjectKeyDeep(pubProds.data, "commission_rate_bps"),
      false,
      "GET /api/gw/products must not expose commission_rate_bps"
    );

    const sampleProd = (pubProds.data?.items ?? [])[0];
    assert.ok(sampleProd?.id && sampleProd?.slug, "expected at least one public product");

    const byId = await anon.json("GET", `/api/gw/products/${encodeURIComponent(sampleProd.id)}`);
    assert.equal(byId.status, 200, `public product by id failed: ${byId.text}`);
    assert.equal(
      hasObjectKeyDeep(byId.data, "commission_rate_bps"),
      false,
      "GET /api/gw/products/:id must not expose commission_rate_bps"
    );

    const bySlug = await anon.json(
      "GET",
      `/api/gw/products/slug/${encodeURIComponent(sampleProd.slug)}`
    );
    assert.equal(bySlug.status, 200, `public product by slug failed: ${bySlug.text}`);
    assert.equal(
      hasObjectKeyDeep(bySlug.data, "commission_rate_bps"),
      false,
      "GET /api/gw/products/slug/:slug must not expose commission_rate_bps"
    );

    // RBAC on /admin/categories: anon=401, buyer=403, seller=403, admin=200 with commission_rate_bps
    const anonAdminCats = await anon.json("GET", "/api/gw/admin/categories");
    assert.equal(anonAdminCats.status, 401, `anon /admin/categories expected 401, got ${anonAdminCats.status}`);

    const buyerAdminCats = await buyer.json("GET", "/api/gw/admin/categories");
    assert.equal(buyerAdminCats.status, 403, `buyer /admin/categories expected 403, got ${buyerAdminCats.status}`);

    const sellerAdminCats = await seller.json("GET", "/api/gw/admin/categories");
    assert.equal(sellerAdminCats.status, 403, `seller /admin/categories expected 403, got ${sellerAdminCats.status}`);

    const adminCats = await admin.json("GET", "/api/gw/admin/categories");
    assert.equal(adminCats.status, 200, `admin /admin/categories failed: ${adminCats.status} ${adminCats.text}`);
    const adminCatItems = adminCats.data?.categories ?? [];
    assert.ok(adminCatItems.length > 0, "admin /admin/categories must return categories");
    assert.ok(
      adminCatItems.some((c) => typeof c.commission_rate_bps === "number"),
      "admin /admin/categories must include numeric commission_rate_bps"
    );

    // Storefront SSR HTML must never leak commission fields
    const [homeHtml, pdpHtml] = await Promise.all([
      anon.page("/"),
      anon.page(`/san-pham/${encodeURIComponent(sampleProd.slug)}`),
    ]);
    assert.equal(homeHtml.status, 200);
    assert.equal(pdpHtml.status, 200);
    for (const [route, html] of [
      ["/", homeHtml.text],
      [`/san-pham/${sampleProd.slug}`, pdpHtml.text],
    ]) {
      assert.equal(
        /commission_rate_bps|commissionRate/i.test(html),
        false,
        `${route} SSR HTML must not contain commission_rate_bps or commissionRate`
      );
    }
  });

  test("3. P2-3 #3: Manual voucher preview supports PLATFORM, FREESHIP & SHOP scopes and hides PAUSED codes publicly", async () => {
    await ensureRateBudget(35, { label: "w3a-p23-vouchers" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);
    const buyer = new WebClient();
    await registerAndLoginBuyer(buyer, "w3a-voucher-buyer");

    const shopMe = await seller.json("GET", "/api/gw/shops/me");
    assert.equal(shopMe.status, 200, `shops/me failed: ${shopMe.text}`);
    const sellerShopId = shopMe.data?.shop?.id;
    assert.ok(sellerShopId, `missing seller shop_id: ${shopMe.text}`);

    const { product, sku } = await createApprovedSellerProduct(seller, admin, "vch", 300_000);
    const tag = crypto.randomBytes(3).toString("hex").toUpperCase();
    const now = Date.now();
    const startsAt = new Date(now - 5 * 60 * 1000).toISOString();
    const endsAt = new Date(now + 24 * 3600 * 1000).toISOString();

    const createdAdminIds = [];
    const createdSellerIds = [];

    try {
      const platCode = `W3APLAT${tag}`;
      const shipCode = `W3ASHIP${tag}`;
      const shopCode = `W3ASHOP${tag}`;
      const pausedCode = `W3APAUS${tag}`;

      const platRes = await admin.json("POST", "/api/gw/admin/vouchers", {
        body: {
          code: platCode,
          name: `Toàn sàn W3A ${tag}`,
          scope: "PLATFORM",
          discount_type: "FIXED",
          value_vnd: 25_000,
          min_order_vnd: 100_000,
          quota: 20,
          per_user_limit: 2,
          budget_vnd: 500_000,
          starts_at: startsAt,
          ends_at: endsAt,
        },
      });
      assert.ok(platRes.status === 200 || platRes.status === 201, `create platform voucher failed: ${platRes.text}`);
      createdAdminIds.push(platRes.data?.voucher?.id);

      const shipRes = await admin.json("POST", "/api/gw/admin/vouchers", {
        body: {
          code: shipCode,
          name: `Freeship W3A ${tag}`,
          scope: "FREESHIP",
          discount_type: "FIXED",
          value_vnd: 20_000,
          min_order_vnd: 100_000,
          quota: 20,
          per_user_limit: 2,
          budget_vnd: 500_000,
          starts_at: startsAt,
          ends_at: endsAt,
        },
      });
      assert.ok(shipRes.status === 200 || shipRes.status === 201, `create freeship voucher failed: ${shipRes.text}`);
      createdAdminIds.push(shipRes.data?.voucher?.id);

      const shopRes = await seller.json("POST", "/api/gw/seller/vouchers", {
        body: {
          code: shopCode,
          name: `Shop W3A ${tag}`,
          discount_type: "FIXED",
          value_vnd: 15_000,
          min_order_vnd: 100_000,
          quota: 20,
          per_user_limit: 2,
          budget_vnd: 500_000,
          starts_at: startsAt,
          ends_at: endsAt,
        },
      });
      assert.ok(shopRes.status === 200 || shopRes.status === 201, `create shop voucher failed: ${shopRes.text}`);
      createdSellerIds.push(shopRes.data?.voucher?.id);

      const pausedRes = await seller.json("POST", "/api/gw/seller/vouchers", {
        body: {
          code: pausedCode,
          name: `Paused W3A ${tag}`,
          discount_type: "FIXED",
          value_vnd: 30_000,
          min_order_vnd: 100_000,
          quota: 20,
          per_user_limit: 2,
          budget_vnd: 500_000,
          starts_at: startsAt,
          ends_at: endsAt,
        },
      });
      assert.ok(pausedRes.status === 200 || pausedRes.status === 201, `create paused voucher failed: ${pausedRes.text}`);
      const pausedId = pausedRes.data?.voucher?.id;
      createdSellerIds.push(pausedId);

      const pausePatch = await seller.json(
        "PATCH",
        `/api/gw/seller/vouchers/${encodeURIComponent(pausedId)}/status`,
        { body: { status: "PAUSED" } }
      );
      assert.equal(pausePatch.status, 200, `pause voucher failed: ${pausePatch.text}`);

      // Public /vouchers must omit PAUSED voucher
      const pubVouchers = await buyer.json("GET", "/api/gw/vouchers");
      assert.equal(pubVouchers.status, 200, `GET /vouchers failed: ${pubVouchers.text}`);
      const pubCodes = new Set((pubVouchers.data?.vouchers ?? []).map((v) => v.code));
      assert.equal(pubCodes.has(pausedCode), false, `PAUSED voucher ${pausedCode} must not appear publicly`);

      const baseShopItems = [
        {
          shop_id: sellerShopId,
          shipping_fee_vnd: 30_000,
          items: [{ sku_id: sku.id, quantity: 1 }],
        },
      ];

      // Probe PLATFORM, FREESHIP, and SHOP scopes individually
      const platPrev = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: { shop_items: baseShopItems, platform_voucher_id: platCode, coins_to_use: 0 },
      });
      assert.equal(platPrev.status, 200);
      assert.equal(platPrev.data?.breakdown?.total_platform_voucher_discount_vnd, 25_000);

      const shipPrev = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: { shop_items: baseShopItems, freeship_voucher_id: shipCode, coins_to_use: 0 },
      });
      assert.equal(shipPrev.status, 200);
      assert.equal(shipPrev.data?.breakdown?.total_freeship_discount_vnd, 20_000);

      const shopPrev = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: {
          shop_items: baseShopItems,
          shop_voucher_ids: { [sellerShopId]: shopCode },
          coins_to_use: 0,
        },
      });
      assert.equal(shopPrev.status, 200);
      assert.equal(shopPrev.data?.breakdown?.total_shop_voucher_discount_vnd, 15_000);

      // PAUSED voucher yields 0 discount when probed
      const pausedPrev = await buyer.json("POST", "/api/gw/promotions/preview", {
        body: {
          shop_items: baseShopItems,
          shop_voucher_ids: { [sellerShopId]: pausedCode },
          coins_to_use: 0,
        },
      });
      assert.equal(pausedPrev.status, 200);
      assert.equal(pausedPrev.data?.breakdown?.total_shop_voucher_discount_vnd ?? 0, 0);
    } finally {
      for (const id of createdAdminIds.filter(Boolean)) {
        await admin.json("DELETE", `/api/gw/admin/vouchers/${encodeURIComponent(id)}`).catch(() => {});
      }
      for (const id of createdSellerIds.filter(Boolean)) {
        await seller.json("DELETE", `/api/gw/seller/vouchers/${encodeURIComponent(id)}`).catch(() => {});
      }
      await deleteSellerProduct(seller, product.id);
    }
  });

  test("4. P3-1 & P3-2: Search & cross-shop active shop discounts map exact discounted price onto target product across shops", async () => {
    await ensureRateBudget(35, { label: "w3a-p3-discounts" });
    const anon = new WebClient();
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { client: seller } = await loginAccount(SELLER_EMAIL, SELLER_PASSWORD);

    const shopMe = await seller.json("GET", "/api/gw/shops/me");
    const sellerShopId = shopMe.data?.shop?.id;
    assert.ok(sellerShopId, `missing seller shop_id: ${shopMe.text}`);

    const catalog = await listCatalogProducts(anon);
    const otherShopProd = catalog.find(
      (p) => p.shop_id && p.shop_id !== sellerShopId && p.skus?.[0]?.id
    );
    assert.ok(otherShopProd, "expected at least one product from a second shop");

    // Create target product in the same category with distinctive non-round prices so assertions never false-pass on unrelated items
    const regularPrice = 257_000;
    const discountedPrice = 173_000;
    const { product, sku } = await createApprovedSellerProduct(
      seller,
      admin,
      "disc-p3",
      regularPrice,
      otherShopProd.category_id || null
    );
    const now = Date.now();
    let programId = null;

    try {
      const createRes = await seller.json("POST", "/api/gw/seller/discount-programs", {
        body: {
          name: `CT W3A P3 ${crypto.randomBytes(3).toString("hex")}`,
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
        `create shop discount failed (${createRes.status}): ${createRes.text}`
      );
      programId = createRes.data?.program?.id;

      // P3-1: Search query + active shop discount enrichment for returned SKU yields exact discounted & original prices
      const keyword = product.name.split(" ").slice(-1)[0];
      const searchRes = await anon.json(
        "GET",
        `/api/gw/products?keyword=${encodeURIComponent(keyword)}&page=1&page_size=6`
      );
      assert.equal(searchRes.status, 200, `search failed: ${searchRes.text}`);
      const found = (searchRes.data?.items ?? []).find((p) => p.id === product.id);
      assert.ok(found, `search keyword ${keyword} must find created product`);

      const multiShopDiscRes = await anon.json(
        "GET",
        `/api/gw/shop-discounts/active?sku_ids=${encodeURIComponent(`${sku.id},${otherShopProd.skus[0].id}`)}`
      );
      assert.equal(multiShopDiscRes.status, 200, `multi-shop active discounts failed: ${multiShopDiscRes.text}`);
      const discHit = (multiShopDiscRes.data?.discounts ?? []).find((d) => d.sku_id === sku.id);
      assert.ok(discHit, "batch sku_ids discount query must return active discount across shops");
      assert.equal(discHit.discounted_price_vnd, discountedPrice);
      assert.equal(discHit.original_price_vnd, regularPrice);

      // P3-2: Verify other-shop PDP RSC payload maps OUR EXACT target product (by id & slug) to price=173000 & originalPrice=257000
      const otherPdp = await anon.page(`/san-pham/${encodeURIComponent(otherShopProd.slug)}`);
      assert.equal(otherPdp.status, 200, `other shop PDP expected 200, got ${otherPdp.status}`);
      const normalizedRsc = otherPdp.text.replace(/\\"/g, '"');
      const slugNeedle = `"slug":"${product.slug}"`;
      const slugIdx = normalizedRsc.indexOf(slugNeedle);
      assert.ok(
        slugIdx >= 0,
        `other-shop PDP (${otherShopProd.slug}) must include target product ${product.slug} in catalog payload`
      );
      const productSlice = normalizedRsc.slice(Math.max(0, slugIdx - 300), slugIdx + 1200);
      assert.ok(
        productSlice.includes(`"price":${discountedPrice}`),
        `target product ${product.slug} on other-shop PDP must carry discounted price ${discountedPrice}`
      );
      assert.ok(
        productSlice.includes(`"originalPrice":${regularPrice}`),
        `target product ${product.slug} on other-shop PDP must carry originalPrice ${regularPrice}`
      );

      // Also verify /tim-kiem SSR for this keyword renders discounted & original price
      const searchPage = await anon.page(`/tim-kiem?q=${encodeURIComponent(keyword)}`);
      assert.equal(searchPage.status, 200, `/tim-kiem expected 200, got ${searchPage.status}`);
      assert.ok(searchPage.text.includes(product.name), "/tim-kiem must render target product");
      assert.ok(
        searchPage.text.includes(formatVndDots(discountedPrice)),
        `/tim-kiem must render discounted price ${formatVndDots(discountedPrice)}`
      );
    } finally {
      if (programId) {
        await seller
          .json("POST", `/api/gw/seller/discount-programs/${encodeURIComponent(programId)}/end`)
          .catch(() => {});
      }
      await deleteSellerProduct(seller, product.id);
    }
  });

  test("5. D6: Admin shop review enforces expected_owner_name (409 OWNER_NAME_CHANGED) & bank review requires valid pending bank_account_id (400/409)", async () => {
    await ensureRateBudget(40, { label: "w3a-d6-concurrency" });
    const { client: admin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    const ownerClient = new WebClient();
    const initialName = `Chu Shop Cu ${crypto.randomBytes(3).toString("hex")}`;
    const updatedName = `Chu Shop Moi ${crypto.randomBytes(3).toString("hex")}`;
    const owner = await registerAndLoginBuyer(ownerClient, "w3a-d6-owner", {
      fullName: initialName,
    });
    const { address: pickupAddress } = await createBuyerAddress(ownerClient, owner);

    const shopTag = crypto.randomBytes(3).toString("hex");
    const regShopRes = await ownerClient.json("POST", "/api/gw/shops/register", {
      body: {
        name: `Xuong W3A D6 ${shopTag}`,
        description: "Gian hàng kiểm thử xung đột duyệt đợt 3a.",
        pickup_address_id: pickupAddress.id,
        contact_phone: owner.phone,
        business_license_doc: `GPKD-W3A-${shopTag.toUpperCase()}`,
      },
    });
    assert.ok(
      regShopRes.status === 200 || regShopRes.status === 201,
      `register shop failed (${regShopRes.status}): ${regShopRes.text}`
    );
    const shop = regShopRes.data?.shop;
    assert.ok(shop?.id, `missing shop.id: ${regShopRes.text}`);

    // Admin opens shop detail panel -> looks up single owner via exact-ID query= (never bulk pagination)
    const ownerLookup1 = await admin.json(
      "GET",
      `/api/gw/admin/users?query=${encodeURIComponent(owner.userId)}&page=1&page_size=1`
    );
    assert.equal(
      ownerLookup1.status,
      200,
      `single-owner lookup expected 200, got ${ownerLookup1.status}: ${ownerLookup1.text}`
    );
    const matchedOwner1 = (ownerLookup1.data?.items ?? []).find((u) => u.id === owner.userId);
    assert.ok(
      matchedOwner1,
      `GET /admin/users?query=<owner_user_id>&page=1&page_size=1 must return exact owner ${owner.userId}: ${ownerLookup1.text}`
    );
    assert.equal(matchedOwner1.full_name, initialName);
    const displayedOwnerNameBeforeChange = matchedOwner1.full_name;

    // Owner changes full_name after admin opened the review screen
    const patchName = await ownerClient.json("PATCH", "/api/gw/users/me", {
      body: { full_name: updatedName },
    });
    assert.equal(patchName.status, 200, `PATCH /users/me failed: ${patchName.text}`);

    // Admin tries APPROVE with the name displayed on screen (stale) -> 409 OWNER_NAME_CHANGED
    const staleApprove = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shop.id)}/review`,
      {
        body: {
          decision: "APPROVE",
          reason: "Duyệt thử với tên cũ",
          expected_owner_name: displayedOwnerNameBeforeChange,
        },
      }
    );
    assert.equal(
      staleApprove.status,
      409,
      `stale expected_owner_name must return 409, got ${staleApprove.status}: ${staleApprove.text}`
    );
    const staleApproveErr = getBffError(staleApprove);
    assert.equal(staleApproveErr.code, "OWNER_NAME_CHANGED");
    assert.ok(staleApproveErr.message.length > 0, `expected non-empty error message: ${staleApprove.text}`);
    assert.match(staleApproveErr.message, /thay đổi/i);

    // After 409, admin reloads single owner via exact-ID query= and sees updatedName
    const ownerLookup2 = await admin.json(
      "GET",
      `/api/gw/admin/users?query=${encodeURIComponent(owner.userId)}&page=1&page_size=1`
    );
    assert.equal(ownerLookup2.status, 200, `reload owner lookup failed: ${ownerLookup2.text}`);
    const matchedOwner2 = (ownerLookup2.data?.items ?? []).find((u) => u.id === owner.userId);
    assert.ok(matchedOwner2, `reloaded owner must match exact id ${owner.userId}`);
    assert.equal(matchedOwner2.full_name, updatedName);

    // Admin inspects new displayed name and retries APPROVE -> 200 ACTIVE
    const freshApprove = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shop.id)}/review`,
      {
        body: {
          decision: "APPROVE",
          reason: "Duyệt sau khi tải lại tên mới",
          expected_owner_name: matchedOwner2.full_name,
        },
      }
    );
    assert.equal(
      freshApprove.status,
      200,
      `fresh expected_owner_name must succeed with 200, got ${freshApprove.status}: ${freshApprove.text}`
    );

    // Re-login owner to get seller JWT with shop_id, then register mismatched-name bank account
    const reLogin = await ownerClient.json("POST", "/api/auth/login", {
      body: { email: owner.email, password: owner.password },
    });
    assert.equal(reLogin.status, 200, `seller re-login failed: ${reLogin.text}`);

    const seenMailIds = await snapshotMailpitIds(owner.email);
    const submitBank = await ownerClient.json("POST", "/api/gw/seller/bank-account", {
      body: {
        bank_code: "VCB",
        bank_name: "Vietcombank",
        account_number: "102938475601",
        account_holder_name: "CONG TY TNHH KHAC TEN E2E",
      },
    });
    assert.ok(
      submitBank.status === 200 || submitBank.status === 201,
      `submit bank account failed (${submitBank.status}): ${submitBank.text}`
    );

    const bankOtp = await waitForRecentOtp(owner.email, seenMailIds);
    const verifyBank = await ownerClient.json("POST", "/api/gw/seller/bank-account/verify-otp", {
      body: { otp: bankOtp },
    });
    assert.equal(verifyBank.status, 200, `verify bank OTP failed: ${verifyBank.text}`);
    const bankAcc = verifyBank.data?.bank_account;
    assert.ok(bankAcc?.id, `missing bank_account.id: ${verifyBank.text}`);
    assert.ok(
      String(bankAcc.status ?? "").toUpperCase().endsWith("PENDING_ADMIN_REVIEW"),
      `expected PENDING_ADMIN_REVIEW, got ${bankAcc.status}`
    );

    // Missing bank_account_id -> 400 with specific missing-id Vietnamese message (P2-10)
    const missingBankId = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shop.id)}/bank-account/review`,
      { body: { decision: "APPROVE", reason: "missing bank_account_id" } }
    );
    assert.equal(
      missingBankId.status,
      400,
      `missing bank_account_id must return 400, got ${missingBankId.status}: ${missingBankId.text}`
    );
    const missingBankIdErr = getBffError(missingBankId);
    assert.ok(missingBankIdErr.message.length > 0, `expected non-empty 400 message: ${missingBankId.text}`);
    assert.match(missingBankIdErr.message, /Thiếu mã bản ghi tài khoản ngân hàng/i);

    // Valid bank_account_id -> 200 VERIFIED
    const approveBank = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shop.id)}/bank-account/review`,
      {
        body: {
          bank_account_id: bankAcc.id,
          decision: "APPROVE",
          reason: "Đã đối chiếu hồ sơ",
        },
      }
    );
    assert.equal(approveBank.status, 200, `approve bank account failed: ${approveBank.text}`);

    // Re-reviewing the same bank_account_id (no longer PENDING_ADMIN_REVIEW) -> 409 sanitized message
    const staleBankReview = await admin.json(
      "POST",
      `/api/gw/admin/shops/${encodeURIComponent(shop.id)}/bank-account/review`,
      {
        body: {
          bank_account_id: bankAcc.id,
          decision: "REJECT",
          reason: "Thử duyệt lại bản ghi đã xong",
        },
      }
    );
    assert.equal(
      staleBankReview.status,
      409,
      `stale bank_account_id review must return 409, got ${staleBankReview.status}: ${staleBankReview.text}`
    );
    const staleBankErr = getBffError(staleBankReview);
    assert.ok(staleBankErr.message.length > 0, `expected non-empty 409 message: ${staleBankReview.text}`);
    assert.match(staleBankErr.message, /thay đổi|trạng thái/i);
    assert.equal(
      staleBankErr.message.includes("PENDING_ADMIN_REVIEW"),
      false,
      "409 bank review message must be sanitized in Vietnamese"
    );
  });

  test("6. D11 & P2-6: Pre-persist failure retries same key, completed-key conflict returns sanitized 409, and cancelled-order replay returns CANCELLED status", async () => {
    await ensureRateBudget(35, { label: "w3a-d11-checkout" });
    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w3a-d11-buyer");
    const { address, province, ward } = await createBuyerAddress(buyer, buyerAcct);
    const picked = await pickSingleShopSku(buyer, { minStock: 2 });
    const quote = await calculateShippingQuote(buyer, {
      shopId: picked.product.shop_id,
      province,
      ward,
      weightGrams: picked.sku.weight_grams || 300,
      codAmountVnd: picked.sku.price_vnd,
    });

    const idemKey = `w3a-d11-${crypto.randomUUID()}`;
    const validCheckoutBody = {
      address_id: address.id,
      from_cart: false,
      items: [{ sku_id: picked.sku.id, quantity: 1 }],
      payment_method: "COD",
      shipping_choices: [
        {
          shop_id: picked.product.shop_id,
          carrier_code: quote.carrier_code,
          service_code: quote.service_code,
        },
      ],
    };

    // Attempt 1: absurd quantity triggers pre-persist failure (0 orders persisted)
    const prePersistFail = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": idemKey },
      body: {
        ...validCheckoutBody,
        items: [{ sku_id: picked.sku.id, quantity: 999_999 }],
      },
    });
    assert.ok(
      prePersistFail.status === 409 || prePersistFail.status === 400,
      `excessive quantity checkout must fail with 409/400, got ${prePersistFail.status}: ${prePersistFail.text}`
    );

    // Attempt 2: same Idempotency-Key with valid quantity succeeds (D11 pre-persist retry)
    const retryValid = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": idemKey },
      body: validCheckoutBody,
    });
    assert.ok(
      retryValid.status === 200 || retryValid.status === 201,
      `same-key retry after pre-persist failure must succeed, got ${retryValid.status}: ${retryValid.text}`
    );
    const createdOrderId = retryValid.data?.orders?.[0]?.id;
    assert.ok(createdOrderId, `missing order id: ${retryValid.text}`);

    // Attempt 3: reusing completed Idempotency-Key with DIFFERENT payload returns 409 sanitized error
    const conflictRes = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": idemKey },
      body: {
        ...validCheckoutBody,
        items: [{ sku_id: picked.sku.id, quantity: 2 }],
      },
    });
    assert.equal(
      conflictRes.status,
      409,
      `reusing completed Idempotency-Key with different payload must return 409, got ${conflictRes.status}: ${conflictRes.text}`
    );
    const conflictErr = getBffError(conflictRes);
    assert.equal(conflictErr.code, "IDEMPOTENCY_KEY_CONFLICT");
    assert.ok(conflictErr.message.length > 0, `expected non-empty 409 message: ${conflictRes.text}`);
    assert.equal(
      conflictErr.message.toLowerCase().includes("idempotency"),
      false,
      "409 conflict message must be natural Vietnamese without raw idempotency jargon"
    );

    // Attempt 4 (P2-6): cancel the created order and replay the same Idempotency-Key with identical payload;
    // backend replays 200 with CANCELLED order(s) (which CheckoutClient intercepts via isTerminalCancelledCheckoutResponse).
    // Note: 409 CHECKOUT_FAILED only occurs when Payment.CreatePayment fails after order persistence or an in-flight
    // checkout expires before completion, which cannot be deterministically induced via public HTTP on a healthy stack
    // (covered in services/order Go tests and web/scripts/w3a-web.test.mjs Test 6).
    const cancelRes = await buyer.json(
      "POST",
      `/api/gw/orders/${encodeURIComponent(createdOrderId)}/cancel`,
      { body: { reason: "W3A E2E cancel before replay check" } }
    );
    assert.equal(cancelRes.status, 200, `cancel order failed (${cancelRes.status}): ${cancelRes.text}`);

    const cancelledReplay = await buyer.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": idemKey },
      body: validCheckoutBody,
    });
    assert.ok(
      cancelledReplay.status === 200 || cancelledReplay.status === 201,
      `cancelled order idempotent replay expected 200/201, got ${cancelledReplay.status}: ${cancelledReplay.text}`
    );
    assert.equal(cancelledReplay.data?.idempotent_replay, true);
    const replayedOrders = cancelledReplay.data?.orders ?? [];
    assert.ok(replayedOrders.length > 0, `expected replayed orders: ${cancelledReplay.text}`);
    assert.ok(
      replayedOrders.every((o) => String(o?.status ?? "").toUpperCase().endsWith("CANCELLED")),
      `replayed cancelled order must report CANCELLED status: ${cancelledReplay.text}`
    );
  });
});
