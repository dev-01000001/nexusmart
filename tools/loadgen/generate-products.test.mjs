import assert from "node:assert/strict";
import test from "node:test";

test("loadgen validateCount enforces default 20000 and bounds 1..50000", async () => {
  const { validateCount, DEFAULT_PRODUCT_COUNT, MAX_PRODUCT_COUNT } = await import(
    "./generate-products.mjs"
  );
  assert.equal(DEFAULT_PRODUCT_COUNT, 20000);
  assert.equal(MAX_PRODUCT_COUNT, 50000);

  assert.equal(validateCount(undefined), 20000);
  assert.equal(validateCount("1"), 1);
  assert.equal(validateCount("20000"), 20000);
  assert.equal(validateCount("50000"), 50000);

  for (const bad of ["0", "-1", "50001", "999999", "abc", "12.5", ""]) {
    assert.throws(() => validateCount(bad), /count/i, `expected count=${bad} to throw`);
  }
});

test("loadgen assertSafeLocalDatabaseUrl accepts localhost product_db and rejects non-localhost or production", async () => {
  const { assertSafeLocalDatabaseUrl, redactDatabaseUrl } = await import(
    "./generate-products.mjs"
  );

  // Valid localhost variants targeting product_db
  for (const okUrl of [
    "postgres://nexusmart:secret@localhost:45432/product_db?sslmode=disable",
    "postgresql://nexusmart:secret@127.0.0.1:5432/product_db",
    "postgres://nexusmart:secret@[::1]:45432/product_db",
  ]) {
    const parsed = assertSafeLocalDatabaseUrl(okUrl, {});
    assert.equal(parsed.database, "product_db");
    assert.ok(!redactDatabaseUrl(okUrl).includes("secret"), "password must be redacted");
  }

  // Must REJECT remote hosts, container service names, LAN IPs, non-product_db, bad schemes
  for (const badUrl of [
    "postgres://nexusmart:pw@db.prod.nexusmart.vn:5432/product_db",
    "postgres://nexusmart:pw@192.168.1.50:5432/product_db",
    "postgres://nexusmart:pw@10.0.0.12:5432/product_db",
    "postgres://nexusmart:pw@postgres:5432/product_db",
    "postgres://nexusmart:pw@localhost.evil.com:5432/product_db",
    "postgres://nexusmart:pw@localhost:5432/order_db",
    "http://localhost:5432/product_db",
    "",
  ]) {
    assert.throws(
      () => assertSafeLocalDatabaseUrl(badUrl, {}),
      /localhost|product_db|DATABASE_URL|protocol/i,
      `expected ${badUrl} to be rejected`
    );
  }

  // Must REJECT when NODE_ENV / NEXUSMART_ENV / APP_ENV is production
  assert.throws(
    () =>
      assertSafeLocalDatabaseUrl("postgres://nexusmart:pw@127.0.0.1:45432/product_db", {
        NODE_ENV: "production",
      }),
    /production/i
  );
  assert.throws(
    () =>
      assertSafeLocalDatabaseUrl("postgres://nexusmart:pw@127.0.0.1:45432/product_db", {
        NEXUSMART_ENV: "production",
      }),
    /production/i
  );
});

test("loadgen generateProductRecord is deterministic, folds Vietnamese search_text, and produces multi-price integer VND SKUs", async () => {
  const { generateProductRecord, normalizeSearchText, escapeCopyField } = await import(
    "./generate-products.mjs"
  );

  assert.equal(normalizeSearchText("Áo Thun Đen Cotton — Đặc biệt"), "ao thun den cotton — dac biet");
  assert.equal(escapeCopyField("a\\b\tc\nd\re"), "a\\\\b\\tc\\nd\\re");

  const shops = [{ id: "shop-1", name: "Xưởng Dệt Bạch Đằng" }];
  const categories = [{ id: "cat-1", slug: "ao-thun", name: "Áo thun" }];
  const recA = generateProductRecord(42, {
    prefix: "lgtest",
    shops,
    categories,
    createdAtIso: "2026-11-23T10:00:00.000Z",
  });
  const recB = generateProductRecord(42, {
    prefix: "lgtest",
    shops,
    categories,
    createdAtIso: "2026-11-23T10:00:00.000Z",
  });

  assert.deepEqual(recA, recB);
  assert.equal(recA.product.id, "lg-lgtest-p-000042");
  assert.ok(recA.product.slug.startsWith("lg-lgtest-000042-"));
  assert.equal(recA.product.status, "APPROVED");
  assert.ok(recA.skus.length >= 2, "must generate multiple SKU variants");
  assert.ok(
    recA.product.min_price_vnd < recA.product.max_price_vnd,
    `expected multi-price SKU range min < max, got ${recA.product.min_price_vnd}..${recA.product.max_price_vnd}`
  );
  for (const sku of recA.skus) {
    assert.ok(Number.isInteger(sku.price_vnd) && sku.price_vnd > 0);
    assert.ok(Number.isInteger(sku.stock) && sku.stock > 0);
    assert.equal(sku.reserved_stock, 0);
    assert.equal(sku.product_id, recA.product.id);
  }
  assert.equal(recA.priceHistory.length, recA.skus.length);
  assert.ok(recA.product.search_text.includes("\n"));
  assert.ok(!/[áàảãạăâđéêíóôơúưý]/i.test(recA.product.search_text));
});

test("loadgen copy workflow supports progress, resume skipping existing indices, and cleanup", async () => {
  const { runCopyGenerate, runCopyCleanup } = await import("./generate-products.mjs");

  const storedProducts = new Map();
  const storedSkus = new Map();
  const storedPriceHistory = new Map();

  const mockRunner = {
    async listExistingIndices(prefix) {
      const indices = new Set();
      const marker = `lg-${prefix}-p-`;
      for (const id of storedProducts.keys()) {
        if (id.startsWith(marker)) {
          indices.add(Number(id.slice(marker.length)));
        }
      }
      return indices;
    },
    async copyBatch(records) {
      let insertedProducts = 0;
      let insertedSkus = 0;
      for (const rec of records) {
        if (!storedProducts.has(rec.product.id)) {
          storedProducts.set(rec.product.id, rec.product);
          insertedProducts += 1;
        }
        for (const sku of rec.skus) {
          if (!storedSkus.has(sku.id)) {
            storedSkus.set(sku.id, sku);
            insertedSkus += 1;
          }
        }
        for (const ph of rec.priceHistory) {
          if (!storedPriceHistory.has(ph.id)) {
            storedPriceHistory.set(ph.id, ph);
          }
        }
      }
      return { insertedProducts, insertedSkus };
    },
    async cleanupPrefix(prefix) {
      const marker = `lg-${prefix}-p-`;
      let deletedProducts = 0;
      let deletedSkus = 0;
      for (const id of [...storedProducts.keys()]) {
        if (id.startsWith(marker)) {
          storedProducts.delete(id);
          deletedProducts += 1;
        }
      }
      for (const [id, sku] of [...storedSkus.entries()]) {
        if (sku.product_id.startsWith(marker)) {
          storedSkus.delete(id);
          deletedSkus += 1;
        }
      }
      for (const [id, ph] of [...storedPriceHistory.entries()]) {
        if (ph.id.startsWith(`lg-${prefix}-ph-`)) {
          storedPriceHistory.delete(id);
        }
      }
      return { deletedProducts, deletedSkus, remainingProducts: 0 };
    },
  };

  // Seed one unrelated product to ensure cleanup NEVER touches non-loadgen rows.
  storedProducts.set("demo-prod-1", { id: "demo-prod-1" });

  const shops = [{ id: "shop-1", name: "Xưởng Dệt Bạch Đằng" }];
  const categories = [{ id: "cat-1", slug: "ao-thun", name: "Áo thun" }];
  const progressEvents = [];

  // First run: 5 products in batches of 2
  const r1 = await runCopyGenerate({
    count: 5,
    batchSize: 2,
    prefix: "t1",
    shops,
    categories,
    runner: mockRunner,
    onProgress: (ev) => progressEvents.push(ev),
  });
  assert.equal(r1.generated, 5);
  assert.equal(r1.skipped, 0);
  assert.equal(progressEvents.length, 3); // 2 + 2 + 1
  assert.equal(storedProducts.size, 6); // 1 demo + 5 loadgen

  // Resume run up to 8 products: must skip 1..5 and only generate 6..8!
  const r2 = await runCopyGenerate({
    count: 8,
    batchSize: 2,
    prefix: "t1",
    shops,
    categories,
    runner: mockRunner,
    resume: true,
    onProgress: () => {},
  });
  assert.equal(r2.skipped, 5);
  assert.equal(r2.generated, 3);
  assert.equal(storedProducts.size, 9); // 1 demo + 8 loadgen

  // Cleanup removes all 8 loadgen products while preserving demo-prod-1.
  const c = await runCopyCleanup({ prefix: "t1", runner: mockRunner });
  assert.equal(c.deletedProducts, 8);
  assert.equal(c.remainingProducts, 0);
  assert.equal(storedProducts.size, 1);
  assert.ok(storedProducts.has("demo-prod-1"));
});

test("loadgen SQL builders use separate sequential statements (avoid CTE snapshot visibility bugs) and indexed NOT EXISTS", async () => {
  const { buildCopyBatchSql, buildCleanupSql, generateProductRecord } = await import(
    "./generate-products.mjs"
  );
  const rec = generateProductRecord(1, {
    prefix: "sqlchk",
    shops: [{ id: "shop-1", name: "Shop 1" }],
    categories: [{ id: "cat-1", slug: "ao-thun", name: "Áo thun" }],
  });

  const copySql = buildCopyBatchSql([rec]);
  // Separate INSERT statements for products, skus, sku_price_history (not chained CTEs).
  assert.match(copySql, /INSERT INTO tmp_batch_counts \(kind, cnt\) SELECT 'p'/);
  assert.match(copySql, /INSERT INTO tmp_batch_counts \(kind, cnt\) SELECT 's'/);
  assert.match(copySql, /INSERT INTO tmp_batch_counts \(kind, cnt\) SELECT 'ph'/);
  // Avoid OR across id/slug in a single NOT EXISTS (keeps B-tree index usage).
  assert.ok(!copySql.includes("p.id = t.id OR"), "must not combine id and slug with OR in NOT EXISTS");

  const cleanSql = buildCleanupSql("sqlchk");
  assert.match(cleanSql, /INSERT INTO tmp_clean_counts \(kind, cnt\) SELECT 's'/);
  assert.match(cleanSql, /INSERT INTO tmp_clean_counts \(kind, cnt\) SELECT 'p'/);
  // Final count query runs in a separate statement after both DELETE statements.
  const lastDelIdx = cleanSql.lastIndexOf("DELETE FROM products");
  const finalSelIdx = cleanSql.lastIndexOf("SELECT count(*) FROM products WHERE id LIKE");
  assert.ok(finalSelIdx > lastDelIdx, "remaining count must run after DELETE statement");
});
