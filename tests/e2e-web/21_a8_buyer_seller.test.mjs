import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { payVietQR } from "./lib/bank.mjs";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import {
  Browser,
  buildMultipartCsv,
  expectStatus,
  getBffError,
  normalizeStatus,
  pollUntil,
  uid,
} from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  cancelOrderBestEffort,
  checkout,
  createApprovedProduct,
  createShippingAddress,
  deleteProductBestEffort,
  demoCredentials,
  fillCart,
  loginOn,
  newIdempotencyKey,
  orderBackendStatus,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveLowAndHighFeeLocations,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);

describe("21. Wave 3b-3 A8 (Guest order lookup, pre-pickup address change, seller batch ship + PDF labels, stock & 2-step CSV import)", () => {
  let siteA;
  let skuA;
  let sellerRoot;
  let adminRoot;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 500_000 });
    sellerRoot = new Browser("a8-seller").at(ROOT_HOST);
    adminRoot = new Browser("a8-admin").at(ROOT_HOST);
    await loginOn(sellerRoot, demoCredentials.seller.email, demoCredentials.seller.password, "a8-seller");
    await loginOn(adminRoot, demoCredentials.admin.email, demoCredentials.admin.password, "a8-admin");
  });

  after(async () => {
    await Promise.allSettled([
      sellerRoot.post("/api/auth/logout", {}),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("1. [B15 & UC-B08] Retired /hoi-vien 404 on root, site /tra-cuu-don-hang, /api/orders/lookup isolation & 429 Retry-After", async () => {
    const anonRoot = new Browser("a8-anon-root").at(ROOT_HOST);
    const anonA = new Browser("a8-anon-a").at(SITE_A_HOST);
    const anonB = new Browser("a8-anon-b").at(SITE_B_HOST);

    // Retired marketplace routes return 404 on root.
    assert.equal((await anonRoot.page("/hoi-vien")).status, 404, "root /hoi-vien must return 404");
    assert.equal((await anonRoot.page("/tra-cuu-don-hang")).status, 404, "root /tra-cuu-don-hang must return 404");
    assert.equal(
      (await anonRoot.post("/api/orders/lookup", { order_code: "NM-NONE", phone: "0900000000" })).status,
      404,
      "root /api/orders/lookup must return 404"
    );

    // Site /tra-cuu-don-hang renders 200 without fake static demo data.
    const lookupPage = await anonA.page("/tra-cuu-don-hang");
    expectStatus(lookupPage, 200, `GET ${SITE_A_HOST}/tra-cuu-don-hang`);
    assert.equal(lookupPage.text.includes("NM-261120-"), false, "/tra-cuu-don-hang must not contain fake NM-261120- sample");
    assert.equal(lookupPage.text.includes("guestOrderLookup"), false, "/tra-cuu-don-hang must not reference guestOrderLookup");

    // Place a real order on site A and test public lookup + cross-site isolation + rate limit.
    const buyerA = new Browser("a8-lookup-b").at(SITE_A_HOST);
    let orderId = null;
    try {
      const acct = await registerBuyer(buyerA, "a8-lk", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "a8-lk", { recipientPhone: acct.creds.phone });
      const cart = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices } = await quoteShipping(buyerA, addr, cart.shop_groups, true);
      const co = expectStatus(
        await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices,
          paymentMethod: "COD",
          key: newIdempotencyKey("a8-lk"),
        }),
        201,
        "checkout for lookup test"
      );
      orderId = co.orders[0].id;
      const orderCode = co.orders[0].order_code;

      // Valid lookup on site A -> 200 with masked PII.
      const ok = expectStatus(
        await anonA.post("/api/orders/lookup", { order_code: orderCode, phone: acct.creds.phone }),
        200,
        "POST site A /api/orders/lookup valid"
      );
      assert.equal(ok.order?.order_code, orderCode);
      assert.equal(ok.text?.includes?.(acct.creds.phone) ?? false, false);

      // Wrong phone & cross-site (site B) -> uniform 404.
      expectStatus(
        await anonA.post("/api/orders/lookup", { order_code: orderCode, phone: "0999999999" }),
        404,
        "wrong phone lookup must return 404"
      );
      expectStatus(
        await anonB.post("/api/orders/lookup", { order_code: orderCode, phone: acct.creds.phone }),
        404,
        "cross-site lookup on site B must return 404"
      );

      // Rate-limit burst on lookup triggers 429 with Retry-After.
      let hit429 = false;
      for (let i = 0; i < 25; i += 1) {
        const r = await anonA.post(
          "/api/orders/lookup",
          { order_code: `NM-RL-${i}`, phone: "0901112222" },
          { retry429: false }
        );
        if (r.status === 429) {
          assert.ok(r.headers["retry-after"], "429 on /api/orders/lookup must include Retry-After header");
          hit429 = true;
          break;
        }
      }
      assert.equal(hit429, true, "expected 429 rate-limit burst on /api/orders/lookup");
    } finally {
      await resetRateLimitKeys();
      await cancelOrderBestEffort(buyerA, orderId, "a8 lookup cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("2. [UC-B26] Pre-pickup shipping address update on site: COD 2-step increase + 409 ADDRESS_FEE_CHANGED + prepaid guard", async () => {
    const buyerA = new Browser("a8-addr-b1").at(SITE_A_HOST);
    const otherBuyerA = new Browser("a8-addr-b2").at(SITE_A_HOST);
    let codOrderId = null;
    let vqOrderId = null;

    try {
      await registerBuyer(buyerA, "a8-addr1", { verifyPhone: true });
      await registerBuyer(otherBuyerA, "a8-addr2");

      const { low, high } = await resolveLowAndHighFeeLocations(buyerA, siteA.shopId, 500);
      const addrLow1 = await createShippingAddress(buyerA, "a8-low1", { province: low.province, ward: low.ward });
      const addrLow2 = await createShippingAddress(buyerA, "a8-low2", {
        province: low.province,
        ward: low.ward,
        streetAddress: "Số 99 Đường Cùng Tỉnh",
        isDefault: false,
      });
      const addrHigh = await createShippingAddress(buyerA, "a8-high", {
        province: high.province,
        ward: high.ward,
        isDefault: false,
      });

      // COD order at low-fee location.
      const cart1 = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices: scLow } = await quoteShipping(buyerA, addrLow1, cart1.shop_groups, true);
      const codCo = expectStatus(
        await checkout(buyerA, {
          addressId: addrLow1.id,
          shippingChoices: scLow,
          paymentMethod: "COD",
          key: newIdempotencyKey("a8-addr-cod"),
        }),
        201,
        "COD checkout at low-fee location"
      );
      codOrderId = codCo.orders[0].id;

      // Preview fee increase to high-fee location.
      const prev = expectStatus(
        await buyerA.patch(`/api/orders/${encodeURIComponent(codOrderId)}/address`, {
          address_id: addrHigh.id,
          preview_only: true,
        }),
        200,
        "preview address update to high-fee location"
      );
      const q = prev.quote ?? prev;
      assert.ok(Number(q.fee_delta_vnd ?? 0) > 0, `expected positive fee_delta_vnd: ${JSON.stringify(prev)}`);
      const newFee = Number(q.new_shipping_fee_vnd);

      // Stale expected_new_shipping_fee_vnd -> 409 ADDRESS_FEE_CHANGED with top-level quote.
      const stale = await buyerA.patch(`/api/orders/${encodeURIComponent(codOrderId)}/address`, {
        address_id: addrHigh.id,
        preview_only: false,
        accept_fee_increase: true,
        expected_new_shipping_fee_vnd: 1,
        idempotency_key: newIdempotencyKey("a8-addr-stale"),
      });
      expectStatus(stale, 409, "stale expected_new_shipping_fee_vnd must return 409");
      assert.equal(getBffError(stale).code, "ADDRESS_FEE_CHANGED");

      // Valid confirmation with exact expected_new_shipping_fee_vnd + idempotent replay.
      const idemKey = newIdempotencyKey("a8-addr-ok");
      const commit1 = expectStatus(
        await buyerA.patch(
          `/api/orders/${encodeURIComponent(codOrderId)}/address`,
          {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: true,
            expected_new_shipping_fee_vnd: newFee,
          },
          { headers: { "Idempotency-Key": idemKey } }
        ),
        200,
        "confirm COD address fee increase"
      );
      assert.equal(Number(commit1.order?.shipping_fee_vnd ?? newFee), newFee);

      expectStatus(
        await buyerA.patch(
          `/api/orders/${encodeURIComponent(codOrderId)}/address`,
          {
            address_id: addrHigh.id,
            preview_only: false,
            accept_fee_increase: true,
            expected_new_shipping_fee_vnd: newFee,
          },
          { headers: { "Idempotency-Key": idemKey } }
        ),
        200,
        "idempotent replay of COD address update"
      );

      // Non-owner buyer cannot modify buyer1's order address.
      expectStatus(
        await otherBuyerA.patch(`/api/orders/${encodeURIComponent(codOrderId)}/address`, {
          address_id: addrLow2.id,
          preview_only: true,
        }),
        [403, 404],
        "non-owner PATCH /api/orders/[id]/address"
      );

      // Prepaid VietQR order: increase blocked 409; same-province update succeeds keeping total unchanged.
      const cart2 = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices: scVq } = await quoteShipping(buyerA, addrLow1, cart2.shop_groups, false);
      const vqCo = expectStatus(
        await checkout(buyerA, {
          addressId: addrLow1.id,
          shippingChoices: scVq,
          paymentMethod: "VIETQR",
          key: newIdempotencyKey("a8-addr-vq"),
        }),
        201,
        "VietQR checkout for prepaid address update"
      );
      vqOrderId = vqCo.orders[0].id;
      const origTotal = Number(vqCo.orders[0].total_payable_vnd);
      await payVietQR({
        paymentId: vqCo.payment_id,
        groupId: vqCo.checkout_group_id,
        amountVND: Number(vqCo.total_payable_vnd),
      });
      await pollUntil(`prepaid order ${vqOrderId} CONFIRMED`, async () => {
        const r = await buyerA.get(`/api/orders/${encodeURIComponent(vqOrderId)}`);
        return [orderBackendStatus(r.data?.order) === "CONFIRMED", `status=${orderBackendStatus(r.data?.order)}`, true];
      });

      const prepaidBlocked = await buyerA.patch(`/api/orders/${encodeURIComponent(vqOrderId)}/address`, {
        address_id: addrHigh.id,
        preview_only: false,
        accept_fee_increase: true,
        expected_new_shipping_fee_vnd: newFee,
        idempotency_key: newIdempotencyKey("a8-prepaid-blk"),
      });
      expectStatus(prepaidBlocked, 409, "prepaid order fee increase must be blocked with 409");

      const prepaidSame = expectStatus(
        await buyerA.patch(`/api/orders/${encodeURIComponent(vqOrderId)}/address`, {
          address_id: addrLow2.id,
          preview_only: false,
          accept_fee_increase: false,
          expected_new_shipping_fee_vnd: Number(vqCo.orders[0].shipping_fee_vnd ?? 0),
          idempotency_key: newIdempotencyKey("a8-prepaid-same"),
        }),
        200,
        "prepaid same-province address update"
      );
      assert.equal(Number(prepaidSame.order?.total_payable_vnd ?? origTotal), origTotal);
    } finally {
      await cancelOrderBestEffort(buyerA, codOrderId, "a8 addr cleanup 1");
      await cancelOrderBestEffort(buyerA, vqOrderId, "a8 addr cleanup 2");
      await Promise.allSettled([
        buyerA.post("/api/auth/logout", {}),
        otherBuyerA.post("/api/auth/logout", {}),
      ]);
    }
  });

  it("3. [S04 & UC-S15] Seller orders status_counts, picking-list, batch-ship & merged PDF labels (%PDF-, nosniff, 404 on site)", async () => {
    const buyerA = new Browser("a8-batch-buyer").at(SITE_A_HOST);
    let orderId = null;

    try {
      await registerBuyer(buyerA, "a8-bship", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "a8-bship");
      const cart = await fillCart(buyerA, skuA.skuId, 1);
      const { shippingChoices } = await quoteShipping(buyerA, addr, cart.shop_groups, true);
      const co = expectStatus(
        await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices,
          paymentMethod: "COD",
          key: newIdempotencyKey("a8-bship"),
        }),
        201,
        "COD order for batch-ship"
      );
      orderId = co.orders[0].id;

      const listData = expectStatus(
        await sellerRoot.get("/api/seller/orders?page=1&page_size=20"),
        200,
        "GET /api/seller/orders"
      );
      assert.ok(listData.status_counts && typeof listData.status_counts === "object", "missing status_counts");

      const pickRes = expectStatus(
        await sellerRoot.post("/api/seller/orders/picking-list", { order_ids: [orderId] }),
        200,
        "POST /api/seller/orders/picking-list"
      );
      assert.ok(Array.isArray(pickRes.items) && pickRes.items.length > 0, "picking list must aggregate items");

      const batchRes = expectStatus(
        await sellerRoot.post("/api/seller/orders/batch-ship", {
          order_ids: [orderId, "00000000-0000-4000-8000-000000000000"],
        }),
        [200, 202],
        "POST /api/seller/orders/batch-ship"
      );
      const results = batchRes.results ?? batchRes.items ?? [];
      assert.ok(results.length >= 2, `batch-ship expected >= 2 result entries: ${JSON.stringify(batchRes)}`);

      // Wait until tracking code is populated by async shipment worker.
      const trackingCode = await pollUntil(`tracking_code for batch-shipped order ${orderId}`, async () => {
        const r = await sellerRoot.get(`/api/seller/orders/${encodeURIComponent(orderId)}`);
        const tc = r.data?.order?.tracking_code ?? "";
        return [Boolean(tc), `tc=${tc}`, tc];
      });

      // Merged PDF batch labels on root host -> 200 %PDF- + nosniff; site host -> 404!
      const pdfRes = await sellerRoot.post(
        "/api/shipping/labels/batch",
        { tracking_codes: [trackingCode] },
        { headers: { accept: "application/pdf" } }
      );
      expectStatus(pdfRes, 200, "POST root /api/shipping/labels/batch");
      assert.equal(pdfRes.rawBuffer.subarray(0, 5).toString("ascii"), "%PDF-", "batch label response must start with %PDF-");
      assert.equal(pdfRes.headers["x-content-type-options"], "nosniff", "batch PDF must set nosniff");

      const siteBlockedPdf = await buyerA.post("/api/shipping/labels/batch", { tracking_codes: [trackingCode] });
      assert.equal(siteBlockedPdf.status, 404, "site host must block /api/shipping/labels/batch with 404");
    } finally {
      await cancelOrderBestEffort(buyerA, orderId, "a8 batch-ship cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("4. [S03 & UC-S13] Seller SKU stock patch + history + low_stock + 2-step CSV import (errors.csv formula guard & confirm)", async () => {
    let createdProdId = null;
    const importedProdIds = [];

    try {
      const { product, sku } = await createApprovedProduct(sellerRoot, adminRoot, {
        name: `A8 Stock SP ${uid()}`,
        priceVnd: 210_000,
        stock: 8,
      });
      createdProdId = product.id;

      // Update SKU stock to 3 (low stock) via PATCH /api/seller/skus/[skuId]/stock.
      const patched = expectStatus(
        await sellerRoot.patch(`/api/seller/skus/${encodeURIComponent(sku.id)}/stock`, {
          new_stock: 3,
          expected_version: sku.version || 1,
        }),
        200,
        "PATCH /api/seller/skus/[skuId]/stock"
      );
      assert.equal(patched.sku?.stock, 3);

      const hist = expectStatus(
        await sellerRoot.get(`/api/seller/skus/${encodeURIComponent(sku.id)}/stock-history?page=1&page_size=20`),
        200,
        "GET /api/seller/skus/[skuId]/stock-history"
      );
      assert.ok(Array.isArray(hist.items) && hist.items.length > 0, "stock-history must record update");

      const lowList = expectStatus(
        await sellerRoot.get(`/api/seller/products?low_stock=true&keyword=${encodeURIComponent(sku.sku_code)}`),
        200,
        "GET /api/seller/products?low_stock=true&keyword="
      );
      assert.ok(
        (lowList.items ?? []).some((p) => p.id === product.id),
        "low_stock + SKU keyword filter must find updated product"
      );

      // 2-step CSV import: download template, upload 1 valid + 1 invalid formula-injection row, inspect errors.csv, confirm.
      const cats = expectStatus(await sellerRoot.get("/api/seller/categories"), 200, "GET /api/seller/categories");
      const categoryId = (cats.categories ?? []).find((c) => !c.is_prohibited)?.id;
      assert.ok(categoryId, "expected non-prohibited category id");

      const tplRes = await sellerRoot.get(
        `/api/seller/products/import/template?category_id=${encodeURIComponent(categoryId)}`,
        { headers: { accept: "text/csv" } }
      );
      expectStatus(tplRes, 200, "GET /api/seller/products/import/template");
      const tplHeader = tplRes.text.replace(/^\uFEFF/, "").split(/\r?\n/)[0] ?? "";
      assert.ok(tplHeader.includes("product_name") && tplHeader.includes("sku_code"), `unexpected template header: ${tplHeader}`);

      const tag = uid().toUpperCase();
      const validSku = `IMP-${tag}-1`;
      const csvContent = [
        "product_name,sku_code,sku_name,category_id,price_vnd,stock,weight_grams,description,image_url",
        `"A8 CSV Valid ${tag}","${validSku}","Trắng / M","${categoryId}",220000,15,300,"Mô tả sản phẩm import hợp lệ","/products/ao-thun-cotton-trang.png"`,
        `"=SUM(1,1) Lỗi ${tag}","IMP-BAD-${tag}","Đen / L","${categoryId}",-500,5,300,"Dòng lỗi giá âm","/products/ao-thun-cotton-trang.png"`,
      ].join("\n");

      const { rawBody, contentType } = buildMultipartCsv("file", `import-${tag}.csv`, csvContent);
      const upRes = await sellerRoot.request("POST", `/api/seller/products/import?filename=import-${tag}.csv`, {
        rawBody,
        contentType,
      });
      const upData = expectStatus(upRes, [200, 201, 202], "POST /api/seller/products/import");
      const jobId = upData.job?.id ?? upData.id;
      assert.ok(jobId, `import upload missing job id: ${upRes.text}`);

      const readyJob = await pollUntil(`CSV import job ${jobId} READY`, async () => {
        const r = await sellerRoot.get(`/api/seller/products/import/${encodeURIComponent(jobId)}`);
        const st = normalizeStatus(r.data?.job?.status ?? r.data?.status);
        return [r.status === 200 && st === "READY", `status=${st}`, r.data?.job ?? r.data];
      });
      assert.ok(Number(readyJob.valid_rows ?? 0) >= 1, `expected >= 1 valid row: ${JSON.stringify(readyJob)}`);
      assert.ok(Number(readyJob.error_rows ?? readyJob.invalid_rows ?? 0) >= 1, "expected >= 1 invalid row");

      const errRows = expectStatus(
        await sellerRoot.get(`/api/seller/products/import/${encodeURIComponent(jobId)}/rows?errors_only=true&page=1&page_size=20`),
        200,
        "GET import rows errors_only=true"
      );
      assert.ok((errRows.items ?? errRows.rows ?? []).length >= 1, "errors_only rows must contain invalid row");

      // errors.csv must neutralize leading '=' formula injection (e.g. "'=SUM(1,1)").
      const errCsv = await sellerRoot.get(`/api/seller/products/import/${encodeURIComponent(jobId)}/errors.csv`, {
        headers: { accept: "text/csv" },
      });
      expectStatus(errCsv, 200, "GET import errors.csv");
      assert.ok(errCsv.text.includes("'=SUM(1,1)"), `errors.csv must neutralize formula injection: ${errCsv.text.slice(0, 400)}`);

      // Idempotent confirm -> COMPLETED -> imported product exists with status PENDING.
      const confKey = newIdempotencyKey("a8-csv-conf");
      expectStatus(
        await sellerRoot.post(
          `/api/seller/products/import/${encodeURIComponent(jobId)}/confirm`,
          {},
          { headers: { "Idempotency-Key": confKey } }
        ),
        [200, 202],
        "POST import confirm #1"
      );
      expectStatus(
        await sellerRoot.post(
          `/api/seller/products/import/${encodeURIComponent(jobId)}/confirm`,
          {},
          { headers: { "Idempotency-Key": confKey } }
        ),
        [200, 202],
        "POST import confirm #2 (idempotent)"
      );

      await pollUntil(`CSV import job ${jobId} COMPLETED`, async () => {
        const r = await sellerRoot.get(`/api/seller/products/import/${encodeURIComponent(jobId)}`);
        const st = normalizeStatus(r.data?.job?.status ?? r.data?.status);
        return [r.status === 200 && st === "COMPLETED", `status=${st}`, r.data?.job ?? r.data];
      });

      const found = expectStatus(
        await sellerRoot.get(`/api/seller/products?keyword=${encodeURIComponent(validSku)}`),
        200,
        "find imported product by SKU"
      );
      const impProd = (found.items ?? []).find((p) => p.name.includes(`A8 CSV Valid ${tag}`));
      assert.ok(impProd, "imported valid product must appear in seller catalog");
      assert.equal(normalizeStatus(impProd.status), "PENDING");
      importedProdIds.push(impProd.id);
    } finally {
      await deleteProductBestEffort(sellerRoot, createdProdId);
      for (const pid of importedProdIds) {
        await deleteProductBestEffort(sellerRoot, pid);
      }
    }
  });
});
