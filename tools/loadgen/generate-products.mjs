#!/usr/bin/env node
// Standalone local-dev bulk product generator (20,000–50,000 products) for search & load testing.
// Separate from `make seed`. Zero external dependencies (Node 24 built-ins only).
//
// Write modes:
//   --mode copy (default): PostgreSQL COPY into local compose `product_db` via temp staging tables
//                          (~150–600 products/s incl. 2–3 SKUs + sku_price_history; refuses non-localhost).
//   --mode api:            Creates & approves products through Gateway REST API (subject to gateway
//                          rate limits; useful for smaller batches or full API/outbox testing).

import { spawn, spawnSync } from "node:child_process";
import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const REPO_ROOT = resolve(__dirname, "../..");

export const DEFAULT_PRODUCT_COUNT = 20000;
export const MAX_PRODUCT_COUNT = 50000;
const ALLOWED_LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);

const GARMENT_ADJECTIVES = [
  "Thoáng khí",
  "Chống nhăn",
  "Co giãn 4 chiều",
  "Đứng phom",
  "Siêu nhẹ",
  "Kháng mùi",
  "Nhanh khô",
  "Mềm mịn",
  "Bền màu",
  "Trượt nước nhẹ",
  "Cơ bản hằng ngày",
  "Công sở tối giản",
];

const FABRICS = [
  "Cotton Compact 220gsm",
  "Cotton Heavyweight 260gsm",
  "Dry-Fit tái chế 145gsm",
  "Pique mắt chim 210gsm",
  "Viscose dệt kim mịn",
  "Cotton Twill 230gsm",
  "Polyamide 4 chiều 118gsm",
  "Linen pha Cotton 185gsm",
  "Nỉ chân cua French Terry 320gsm",
  "Nylon Tech trượt nước",
  "Sợi gỗ sồi Modal",
  "Sợi tre Bamboo tự nhiên",
];

const FITS = ["Regular", "Slim", "Relaxed", "Oversize", "Athletic"];

const COLOR_PALETTE = [
  { code: "trang", name: "Trắng tinh" },
  { code: "den", name: "Đen than" },
  { code: "xamda", name: "Xám đá" },
  { code: "xanhthan", name: "Xanh than" },
  { code: "kem", name: "Kem nhạt" },
  { code: "be", name: "Be cát" },
  { code: "covit", name: "Xanh cổ vịt" },
  { code: "reu", name: "Xanh rêu" },
];

const SIZE_TIERS = [
  { size: "M", priceDeltaVnd: 0 },
  { size: "L", priceDeltaVnd: 10000 },
  { size: "XL", priceDeltaVnd: 20000 },
];

const IMAGE_BY_CATEGORY = {
  "ao-thun": "/products/ao-thun-cotton-trang.png",
  "ao-polo": "/products/ao-polo-pique-kem.png",
  "quan-short": "/products/quan-short-daily-be.png",
  "quan-jogger": "/products/quan-jogger-terry-xam.png",
  "do-lot": "/products/do-lot-boxer-modal.png",
  "tat-phu-kien": "/products/tat-cotton-co-trung-5.png",
};

const WEIGHT_BY_CATEGORY = {
  "ao-thun": 230,
  "ao-polo": 280,
  "quan-short": 260,
  "quan-jogger": 440,
  "do-lot": 200,
  "tat-phu-kien": 180,
};

function loadDotEnv(filePath) {
  const vars = {};
  if (!existsSync(filePath)) return vars;
  const raw = readFileSync(filePath, "utf8");
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq <= 0) continue;
    const key = trimmed.slice(0, eq).trim();
    let val = trimmed.slice(eq + 1).trim();
    if (
      (val.startsWith('"') && val.endsWith('"')) ||
      (val.startsWith("'") && val.endsWith("'"))
    ) {
      val = val.slice(1, -1);
    }
    vars[key] = val;
  }
  return vars;
}

export function validateCount(raw) {
  if (raw === undefined) return DEFAULT_PRODUCT_COUNT;
  const str = String(raw).trim();
  if (!/^\d+$/.test(str)) {
    throw new Error(`Invalid --count "${raw}": must be an integer between 1 and ${MAX_PRODUCT_COUNT}`);
  }
  const n = Number(str);
  if (!Number.isSafeInteger(n) || n < 1 || n > MAX_PRODUCT_COUNT) {
    throw new Error(`Invalid --count ${n}: must be between 1 and ${MAX_PRODUCT_COUNT}`);
  }
  return n;
}

export function validatePrefix(raw) {
  const val = String(raw || "loadgen").trim().toLowerCase();
  if (!/^[a-z0-9][a-z0-9-]{0,19}$/.test(val)) {
    throw new Error(`Invalid --prefix "${raw}": must match ^[a-z0-9][a-z0-9-]{0,19}$`);
  }
  return val;
}

export function redactDatabaseUrl(rawUrl) {
  try {
    const u = new URL(rawUrl);
    if (u.password) u.password = "REDACTED";
    return u.toString();
  } catch {
    return "<invalid DATABASE_URL>";
  }
}

/**
 * Strictly refuses to run against any non-localhost DATABASE_URL, production env, or non-product_db.
 */
export function assertSafeLocalDatabaseUrl(rawUrl, env = process.env) {
  for (const key of ["NODE_ENV", "NEXUSMART_ENV", "APP_ENV"]) {
    if (String(env[key] || "").trim().toLowerCase() === "production") {
      throw new Error(`Refusing to run loadgen: ${key}=production is forbidden`);
    }
  }
  if (!rawUrl || typeof rawUrl !== "string" || !rawUrl.trim()) {
    throw new Error("DATABASE_URL is required in copy mode and must point to localhost product_db");
  }

  let parsed;
  try {
    parsed = new URL(rawUrl.trim());
  } catch {
    throw new Error("Invalid DATABASE_URL format");
  }

  if (parsed.protocol !== "postgres:" && parsed.protocol !== "postgresql:") {
    throw new Error(`Unsupported DATABASE_URL protocol "${parsed.protocol}"; expected postgres://`);
  }

  const host = parsed.hostname.toLowerCase();
  if (!ALLOWED_LOCAL_HOSTS.has(host)) {
    throw new Error(
      `Refusing to run loadgen against non-localhost host "${host}". Only localhost / 127.0.0.1 / ::1 dev databases are allowed.`
    );
  }

  const database = parsed.pathname.replace(/^\/+/, "");
  if (database !== "product_db") {
    throw new Error(
      `Refusing to run loadgen against database "${database}"; expected "product_db"`
    );
  }

  const port = parsed.port ? Number(parsed.port) : 5432;
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`Invalid DATABASE_URL port "${parsed.port}"`);
  }

  return {
    url: parsed.toString(),
    host,
    port,
    user: decodeURIComponent(parsed.username || "nexusmart"),
    password: decodeURIComponent(parsed.password || ""),
    database,
  };
}

/**
 * Matches services/product/internal/store/search.go:NormalizeSearchText (Vietnamese folding + lowercase).
 */
export function normalizeSearchText(input) {
  const s = String(input || "").trim();
  if (!s) return "";
  return s
    .toLowerCase()
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d");
}

export function buildSearchText(name, description) {
  const n = normalizeSearchText(name);
  const d = normalizeSearchText(description);
  if (!d) return n;
  if (!n) return d;
  return `${n}\n${d}`;
}

export function escapeCopyField(val) {
  return String(val)
    .replace(/\\/g, "\\\\")
    .replace(/\t/g, "\\t")
    .replace(/\n/g, "\\n")
    .replace(/\r/g, "\\r");
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function slugifyAscii(text) {
  return normalizeSearchText(text)
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
}

/**
 * Deterministic generator for product index `i` (1-based), producing 1 product + 2..3 multi-price SKUs
 * + matching sku_price_history rows. All monetary amounts are integer VND.
 */
export function generateProductRecord(
  index,
  { prefix = "loadgen", shops, categories, createdAtIso = new Date().toISOString() }
) {
  const rng = mulberry32(index * 2654435761 + prefix.length * 97);
  const pick = (arr) => arr[Math.floor(rng() * arr.length)];

  const shop = shops[(index - 1) % shops.length];
  const category = categories[(index - 1) % categories.length];
  const adj = pick(GARMENT_ADJECTIVES);
  const fabric = pick(FABRICS);
  const fit = pick(FITS);
  const color = pick(COLOR_PALETTE);
  const padded = String(index).padStart(6, "0");

  const productId = `lg-${prefix}-p-${padded}`;
  const baseName = `${category.name} ${adj} ${fabric} ${color.name} #${padded}`;
  const slug = `lg-${prefix}-${padded}-${slugifyAscii(`${category.slug}-${color.code}-${fit}`)}`;
  const description = `${category.name} dáng ${fit}, chất liệu ${fabric}, màu ${color.name}, cung cấp bởi ${shop.name}. Sản phẩm kiểm thử tìm kiếm và hiệu năng NexusMart (mã ${padded}).`;
  const searchText = buildSearchText(baseName, description);

  const image = IMAGE_BY_CATEGORY[category.slug] || "/products/ao-thun-cotton-trang.png";
  const weightGrams = (WEIGHT_BY_CATEGORY[category.slug] || 240) + Math.floor(rng() * 80);

  // Integer VND base price: 99,000 .. 599,000 in 10,000₫ steps
  const basePriceVnd = 99000 + Math.floor(rng() * 51) * 10000;
  const variantCount = index % 2 === 0 ? 3 : 2;
  const skus = [];
  const priceHistory = [];

  let minPriceVnd = Number.MAX_SAFE_INTEGER;
  let maxPriceVnd = 0;

  for (let v = 0; v < variantCount; v += 1) {
    const tier = SIZE_TIERS[v];
    const priceVnd = basePriceVnd + tier.priceDeltaVnd;
    const stock = 10 + Math.floor(rng() * 91); // 10..100
    const skuId = `lg-${prefix}-s-${padded}-${v + 1}`;
    const skuCode = `LG-${prefix.toUpperCase()}-${padded}-${tier.size}`;
    if (priceVnd < minPriceVnd) minPriceVnd = priceVnd;
    if (priceVnd > maxPriceVnd) maxPriceVnd = priceVnd;

    skus.push({
      id: skuId,
      product_id: productId,
      sku_code: skuCode,
      name: `${color.name} / ${tier.size}`,
      attributes: {
        color: color.name,
        color_code: color.code,
        size: tier.size,
        fit,
        fabric,
      },
      price_vnd: priceVnd,
      stock,
      reserved_stock: 0,
      version: 1,
      weight_grams: weightGrams,
      image_url: image,
      status: "ACTIVE",
      created_at: createdAtIso,
      updated_at: createdAtIso,
    });

    priceHistory.push({
      id: `lg-${prefix}-ph-${padded}-${v + 1}`,
      sku_id: skuId,
      price_vnd: priceVnd,
      actor_id: "loadgen",
      reason: "loadgen initial price",
      recorded_at: createdAtIso,
    });
  }

  return {
    product: {
      id: productId,
      shop_id: shop.id,
      category_id: category.id,
      name: baseName,
      description,
      images: [image],
      status: "APPROVED",
      rejection_reason: "",
      min_price_vnd: minPriceVnd,
      max_price_vnd: maxPriceVnd,
      slug,
      search_text: searchText,
      created_at: createdAtIso,
      updated_at: createdAtIso,
    },
    skus,
    priceHistory,
  };
}

export function buildCopyBatchSql(records) {
  const lines = [
    "BEGIN;",
    "CREATE TEMP TABLE tmp_products (LIKE products INCLUDING DEFAULTS) ON COMMIT DROP;",
    "CREATE TEMP TABLE tmp_skus (LIKE skus INCLUDING DEFAULTS) ON COMMIT DROP;",
    "CREATE TEMP TABLE tmp_price_history (id TEXT, sku_id TEXT, price_vnd BIGINT, actor_id TEXT, reason TEXT, recorded_at TIMESTAMPTZ) ON COMMIT DROP;",
    "CREATE TEMP TABLE tmp_batch_counts (kind TEXT PRIMARY KEY, cnt BIGINT NOT NULL) ON COMMIT DROP;",
    "COPY tmp_products (id, shop_id, category_id, name, description, images, status, rejection_reason, min_price_vnd, max_price_vnd, slug, search_text, created_at, updated_at) FROM STDIN;",
  ];

  for (const { product: p } of records) {
    lines.push(
      [
        p.id,
        p.shop_id,
        p.category_id,
        p.name,
        p.description,
        JSON.stringify(p.images),
        p.status,
        p.rejection_reason,
        p.min_price_vnd,
        p.max_price_vnd,
        p.slug,
        p.search_text,
        p.created_at,
        p.updated_at,
      ]
        .map(escapeCopyField)
        .join("\t")
    );
  }
  lines.push("\\.");

  lines.push(
    "COPY tmp_skus (id, product_id, sku_code, name, attributes, price_vnd, stock, reserved_stock, version, weight_grams, image_url, status, created_at, updated_at) FROM STDIN;"
  );
  for (const { skus } of records) {
    for (const s of skus) {
      lines.push(
        [
          s.id,
          s.product_id,
          s.sku_code,
          s.name,
          JSON.stringify(s.attributes),
          s.price_vnd,
          s.stock,
          s.reserved_stock,
          s.version,
          s.weight_grams,
          s.image_url,
          s.status,
          s.created_at,
          s.updated_at,
        ]
          .map(escapeCopyField)
          .join("\t")
      );
    }
  }
  lines.push("\\.");

  lines.push(
    "COPY tmp_price_history (id, sku_id, price_vnd, actor_id, reason, recorded_at) FROM STDIN;"
  );
  for (const { priceHistory } of records) {
    for (const ph of priceHistory) {
      lines.push(
        [ph.id, ph.sku_id, ph.price_vnd, ph.actor_id, ph.reason, ph.recorded_at]
          .map(escapeCopyField)
          .join("\t")
      );
    }
  }
  lines.push("\\.");

  // Sequential statements inside one transaction so each INSERT sees prior statement rows
  // and each NOT EXISTS uses its own B-tree index (products_pkey / idx_products_slug_active).
  lines.push(`
WITH ins_p AS (
  INSERT INTO products (id, shop_id, category_id, name, description, images, status, rejection_reason, min_price_vnd, max_price_vnd, slug, search_text, created_at, updated_at)
  SELECT t.id, t.shop_id, t.category_id, t.name, t.description, t.images, t.status, t.rejection_reason, t.min_price_vnd, t.max_price_vnd, t.slug, t.search_text, t.created_at, t.updated_at
  FROM tmp_products t
  WHERE NOT EXISTS (SELECT 1 FROM products p WHERE p.id = t.id)
    AND NOT EXISTS (SELECT 1 FROM products p WHERE p.deleted_at IS NULL AND p.slug = t.slug)
  ON CONFLICT (id) DO NOTHING
  RETURNING id
)
INSERT INTO tmp_batch_counts (kind, cnt) SELECT 'p', count(*) FROM ins_p;

WITH ins_s AS (
  INSERT INTO skus (id, product_id, sku_code, name, attributes, price_vnd, stock, reserved_stock, version, weight_grams, image_url, status, created_at, updated_at)
  SELECT t.id, t.product_id, t.sku_code, t.name, t.attributes, t.price_vnd, t.stock, t.reserved_stock, t.version, t.weight_grams, t.image_url, t.status, t.created_at, t.updated_at
  FROM tmp_skus t
  JOIN products p ON p.id = t.product_id
  ON CONFLICT (id) DO NOTHING
  RETURNING id
)
INSERT INTO tmp_batch_counts (kind, cnt) SELECT 's', count(*) FROM ins_s;

WITH ins_ph AS (
  INSERT INTO sku_price_history (id, sku_id, price_vnd, actor_id, reason, recorded_at)
  SELECT t.id, t.sku_id, t.price_vnd, t.actor_id, t.reason, t.recorded_at
  FROM tmp_price_history t
  JOIN skus s ON s.id = t.sku_id
  ON CONFLICT (id) DO NOTHING
  RETURNING id
)
INSERT INTO tmp_batch_counts (kind, cnt) SELECT 'ph', count(*) FROM ins_ph;

SELECT
  (SELECT cnt FROM tmp_batch_counts WHERE kind = 'p'),
  (SELECT cnt FROM tmp_batch_counts WHERE kind = 's'),
  (SELECT cnt FROM tmp_batch_counts WHERE kind = 'ph');
COMMIT;
`);

  return `${lines.join("\n")}\n`;
}

export function buildCleanupSql(prefix) {
  const safePrefix = validatePrefix(prefix);
  const marker = `lg-${safePrefix}-p-`;
  return `
BEGIN;
CREATE TEMP TABLE tmp_del_products (id TEXT PRIMARY KEY) ON COMMIT DROP;
CREATE TEMP TABLE tmp_clean_counts (kind TEXT PRIMARY KEY, cnt BIGINT NOT NULL) ON COMMIT DROP;

INSERT INTO tmp_del_products (id)
SELECT p.id FROM products p
WHERE p.id LIKE '${marker}%'
  AND NOT EXISTS (
    SELECT 1 FROM skus s
    JOIN stock_reservations sr ON sr.sku_id = s.id
    WHERE s.product_id = p.id
  );

WITH deleted_skus AS (
  DELETE FROM skus WHERE product_id IN (SELECT id FROM tmp_del_products) RETURNING id
)
INSERT INTO tmp_clean_counts (kind, cnt) SELECT 's', count(*) FROM deleted_skus;

WITH deleted_products AS (
  DELETE FROM products WHERE id IN (SELECT id FROM tmp_del_products) RETURNING id
)
INSERT INTO tmp_clean_counts (kind, cnt) SELECT 'p', count(*) FROM deleted_products;

SELECT
  (SELECT cnt FROM tmp_clean_counts WHERE kind = 'p'),
  (SELECT cnt FROM tmp_clean_counts WHERE kind = 's'),
  (SELECT count(*) FROM products WHERE id LIKE '${marker}%');
COMMIT;
`;
}

function resolvePsqlTransport(dbCfg) {
  // Prefer local host `psql` if available; otherwise verify & use repo's local docker compose postgres container.
  const hostCheck = spawnSync("psql", ["--version"], { stdio: "ignore" });
  if (hostCheck.status === 0) {
    return {
      kind: "host-psql",
      cmd: "psql",
      args: [
        "-h",
        dbCfg.host.replace(/^\[|\]$/g, ""),
        "-p",
        String(dbCfg.port),
        "-U",
        dbCfg.user,
        "-d",
        dbCfg.database,
        "-v",
        "ON_ERROR_STOP=1",
        "-q",
        "-t",
        "-A",
      ],
      env: { ...process.env, PGPASSWORD: dbCfg.password },
    };
  }

  const composeFile = resolve(REPO_ROOT, "docker-compose.yml");
  if (!existsSync(composeFile)) {
    throw new Error("Host psql not found and docker-compose.yml is missing at repo root");
  }
  const portCheck = spawnSync("docker", ["compose", "port", "postgres", "5432"], {
    cwd: REPO_ROOT,
    encoding: "utf8",
  });
  if (portCheck.status !== 0) {
    throw new Error(
      `Host psql not found and local docker compose postgres is not running: ${(portCheck.stderr || "").trim()}`
    );
  }
  const binding = (portCheck.stdout || "").trim().split(/\r?\n/)[0] || "";
  const match = binding.match(/:(\d+)$/);
  const composePort = match ? Number(match[1]) : 0;
  if (composePort !== dbCfg.port) {
    throw new Error(
      `Refusing to use docker compose postgres: compose port (${composePort}) does not match DATABASE_URL port (${dbCfg.port})`
    );
  }

  return {
    kind: "docker-compose-psql",
    cmd: "docker",
    args: [
      "compose",
      "exec",
      "-T",
      "postgres",
      "psql",
      "-U",
      dbCfg.user,
      "-d",
      dbCfg.database,
      "-v",
      "ON_ERROR_STOP=1",
      "-q",
      "-t",
      "-A",
    ],
    cwd: REPO_ROOT,
    env: process.env,
  };
}

function runPsqlSql(transport, sql) {
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn(transport.cmd, transport.args, {
      cwd: transport.cwd || REPO_ROOT,
      env: transport.env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += chunk.toString("utf8");
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk.toString("utf8");
    });
    child.on("error", rejectPromise);
    child.on("close", (code) => {
      if (code !== 0) {
        rejectPromise(new Error(`psql exited with code ${code}: ${stderr.trim() || stdout.trim()}`));
        return;
      }
      resolvePromise(stdout.trim());
    });
    child.stdin.end(sql, "utf8");
  });
}

export function createLocalCopyRunner(databaseUrl, env = process.env) {
  const dbCfg = assertSafeLocalDatabaseUrl(databaseUrl, env);
  const transport = resolvePsqlTransport(dbCfg);

  return {
    transportKind: transport.kind,
    redactedUrl: redactDatabaseUrl(dbCfg.url),

    async listExistingIndices(prefix) {
      const safePrefix = validatePrefix(prefix);
      const marker = `lg-${safePrefix}-p-`;
      const out = await runPsqlSql(
        transport,
        `SELECT substring(id FROM ${marker.length + 1}) FROM products WHERE id LIKE '${marker}%';\n`
      );
      const set = new Set();
      if (!out) return set;
      for (const line of out.split(/\r?\n/)) {
        const n = Number(line.trim());
        if (Number.isInteger(n) && n > 0) set.add(n);
      }
      return set;
    },

    async copyBatch(records) {
      const sql = buildCopyBatchSql(records);
      const out = await runPsqlSql(transport, sql);
      const lastLine = out
        .split(/\r?\n/)
        .map((l) => l.trim())
        .filter(Boolean)
        .pop();
      const [pCnt = "0", sCnt = "0", phCnt = "0"] = (lastLine || "").split("|");
      return {
        insertedProducts: Number(pCnt) || 0,
        insertedSkus: Number(sCnt) || 0,
        insertedPriceHistory: Number(phCnt) || 0,
      };
    },

    async cleanupPrefix(prefix) {
      const sql = buildCleanupSql(prefix);
      const out = await runPsqlSql(transport, sql);
      const lastLine = out
        .split(/\r?\n/)
        .map((l) => l.trim())
        .filter(Boolean)
        .pop();
      const [delP = "0", delS = "0", remP = "0"] = (lastLine || "").split("|");
      return {
        deletedProducts: Number(delP) || 0,
        deletedSkus: Number(delS) || 0,
        remainingProducts: Number(remP) || 0,
      };
    },
  };
}

/**
 * Resumable batched COPY generation workflow with progress reporting.
 */
export async function runCopyGenerate({
  count,
  batchSize = 2000,
  prefix = "loadgen",
  shops,
  categories,
  runner,
  resume = true,
  onProgress = () => {},
}) {
  const safeCount = validateCount(count);
  const safePrefix = validatePrefix(prefix);
  if (!Array.isArray(shops) || shops.length === 0) {
    throw new Error("At least one active shop is required (run `make seed` first)");
  }
  if (!Array.isArray(categories) || categories.length === 0) {
    throw new Error("At least one category is required (run `make seed` first)");
  }

  const existingIndices = resume ? await runner.listExistingIndices(safePrefix) : new Set();
  const missingIndices = [];
  for (let i = 1; i <= safeCount; i += 1) {
    if (!existingIndices.has(i)) missingIndices.push(i);
  }

  const skipped = safeCount - missingIndices.length;
  const totalBatches = Math.ceil(missingIndices.length / batchSize);
  const startMs = Date.now();
  const createdAtIso = new Date().toISOString();

  let generated = 0;
  let generatedSkus = 0;

  for (let b = 0; b < totalBatches; b += 1) {
    const slice = missingIndices.slice(b * batchSize, (b + 1) * batchSize);
    const records = slice.map((idx) =>
      generateProductRecord(idx, { prefix: safePrefix, shops, categories, createdAtIso })
    );
    const res = await runner.copyBatch(records);
    generated += res.insertedProducts;
    generatedSkus += res.insertedSkus;

    const completed = skipped + generated;
    const elapsedSec = Math.max(0.001, (Date.now() - startMs) / 1000);
    const rate = generated / elapsedSec;
    const remaining = safeCount - completed;
    const etaSec = rate > 0 ? remaining / rate : 0;

    onProgress({
      batch: b + 1,
      totalBatches,
      completed,
      total: safeCount,
      generated,
      generatedSkus,
      skipped,
      ratePerSec: rate,
      elapsedSec,
      etaSec,
    });
  }

  return {
    count: safeCount,
    prefix: safePrefix,
    generated,
    generatedSkus,
    skipped,
    elapsedSec: (Date.now() - startMs) / 1000,
  };
}

export async function runCopyCleanup({ prefix = "loadgen", runner }) {
  const safePrefix = validatePrefix(prefix);
  return runner.cleanupPrefix(safePrefix);
}

// --- Gateway API helpers (used for fetching real shops/categories AND for --mode api) ---

async function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

async function gwFetch(gatewayUrl, method, path, { token, body, allowStatuses = [] } = {}) {
  const url = `${gatewayUrl}${path}`;
  for (let attempt = 0; attempt < 6; attempt += 1) {
    const headers = { Accept: "application/json" };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(url, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (res.status === 429 && attempt < 5) {
      const retrySec = Number(res.headers.get("retry-after") || "5");
      const waitMs = Math.max(1000, (Number.isFinite(retrySec) ? retrySec : 5) * 1000);
      await sleep(waitMs);
      continue;
    }
    const text = await res.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { raw: text };
      }
    }
    if (res.ok || allowStatuses.includes(res.status)) {
      return { status: res.status, ok: res.ok, data };
    }
    const msg = data?.error?.message || text || res.statusText;
    throw new Error(`${method} ${path} failed (HTTP ${res.status}): ${msg}`);
  }
  throw new Error(`${method} ${path} exceeded retry budget`);
}

async function fetchLiveShopsAndCategories(gatewayUrl) {
  const [shopsRes, catsRes] = await Promise.all([
    gwFetch(gatewayUrl, "GET", "/api/v1/shops?page=1&page_size=100"),
    gwFetch(gatewayUrl, "GET", "/api/v1/categories"),
  ]);
  const shops = Array.isArray(shopsRes.data?.items) ? shopsRes.data.items : [];
  const categories = Array.isArray(catsRes.data?.categories) ? catsRes.data.categories : [];
  if (shops.length === 0) {
    throw new Error(`No active shops found at ${gatewayUrl}/api/v1/shops — run \`make seed\` first`);
  }
  if (categories.length === 0) {
    throw new Error(`No categories found at ${gatewayUrl}/api/v1/categories — run \`make seed\` first`);
  }
  return { shops, categories };
}

async function runApiMode({
  gatewayUrl,
  count,
  prefix,
  cleanup,
  resume,
  dotEnv,
}) {
  const adminEmail =
    process.env.ADMIN_BOOTSTRAP_EMAIL || dotEnv.ADMIN_BOOTSTRAP_EMAIL || "admin@nexusmart.local";
  const adminPassword =
    process.env.ADMIN_BOOTSTRAP_PASSWORD || dotEnv.ADMIN_BOOTSTRAP_PASSWORD || "AdminDevPassword123!";
  const sellerEmails = [
    "seller.bachdang@nexusmart.local",
    "seller.saigon@nexusmart.local",
    "seller.danang@nexusmart.local",
    "seller.hanoi@nexusmart.local",
  ];
  const sellerPassword = process.env.LOADGEN_SELLER_PASSWORD || "SellerDevPassword123!";
  const checkpointPath = resolve(__dirname, `.loadgen-${prefix}.checkpoint.json`);

  const adminLogin = await gwFetch(gatewayUrl, "POST", "/api/v1/auth/login", {
    body: { email: adminEmail, password: adminPassword },
  });
  const adminToken = adminLogin.data.access_token;

  const sellers = [];
  for (const email of sellerEmails) {
    const res = await gwFetch(gatewayUrl, "POST", "/api/v1/auth/login", {
      body: { email, password: sellerPassword },
      allowStatuses: [401],
    });
    if (res.ok && res.data?.user?.shop_id) {
      sellers.push({
        id: res.data.user.shop_id,
        name: res.data.user.full_name || email,
        token: res.data.access_token,
      });
    }
  }
  if (sellers.length === 0) {
    throw new Error("No demo sellers logged in for API mode — run `make seed` first");
  }
  const sellerTokenByShopId = new Map(sellers.map((s) => [s.id, s.token]));

  if (cleanup) {
    let deleted = 0;
    const keyword = `lg-${prefix}-`;
    while (true) {
      const listRes = await gwFetch(
        gatewayUrl,
        "GET",
        `/api/v1/admin/products?keyword=${encodeURIComponent(prefix)}&page=1&page_size=100`,
        { token: adminToken }
      );
      const items = (listRes.data?.items || []).filter((p) => String(p.slug || "").startsWith(keyword));
      if (items.length === 0) break;
      for (const p of items) {
        const sTok = sellerTokenByShopId.get(p.shop_id);
        if (!sTok) continue;
        await gwFetch(gatewayUrl, "DELETE", `/api/v1/seller/products/${p.id}`, { token: sTok });
        deleted += 1;
      }
    }
    if (existsSync(checkpointPath)) rmSync(checkpointPath);
    console.log(`API cleanup complete: deleted=${deleted} products (prefix=${prefix})`);
    return;
  }

  const { categories } = await fetchLiveShopsAndCategories(gatewayUrl);
  let lastDone = 0;
  if (resume && existsSync(checkpointPath)) {
    try {
      const parsed = JSON.parse(readFileSync(checkpointPath, "utf8"));
      lastDone = Number(parsed.lastCompletedIndex) || 0;
    } catch {
      lastDone = 0;
    }
  }

  console.log(
    `API mode starting: count=${count}, resumeFrom=${lastDone + 1}, prefix=${prefix} (note: gateway rate limits apply; use --mode copy for 20k–50k bulk runs)`
  );
  const startMs = Date.now();
  let created = 0;
  let skipped = lastDone;

  for (let i = lastDone + 1; i <= count; i += 1) {
    const rec = generateProductRecord(i, { prefix, shops: sellers, categories });
    const sellerToken = sellerTokenByShopId.get(rec.product.shop_id);
    const createRes = await gwFetch(gatewayUrl, "POST", "/api/v1/seller/products", {
      token: sellerToken,
      body: {
        category_id: rec.product.category_id,
        name: rec.product.name,
        slug: rec.product.slug,
        description: rec.product.description,
        images: rec.product.images,
        skus: rec.skus.map((s) => ({
          sku_code: s.sku_code,
          name: s.name,
          attributes: s.attributes,
          price_vnd: s.price_vnd,
          stock: s.stock,
          weight_grams: s.weight_grams,
          image_url: s.image_url,
        })),
      },
      allowStatuses: [409],
    });
    if (createRes.status === 409) {
      skipped += 1;
    } else {
      const prodId = createRes.data?.product?.id;
      if (prodId) {
        await gwFetch(gatewayUrl, "POST", `/api/v1/admin/products/${prodId}/review`, {
          token: adminToken,
          body: { decision: "APPROVE", reason: "Loadgen API approval" },
        });
      }
      created += 1;
    }

    writeFileSync(checkpointPath, JSON.stringify({ prefix, lastCompletedIndex: i }, null, 2));
    if (i % 10 === 0 || i === count) {
      const elapsedSec = Math.max(0.001, (Date.now() - startMs) / 1000);
      const pct = ((i / count) * 100).toFixed(1);
      console.log(
        `  [api] ${i}/${count} (${pct}%) | created=${created} skipped=${skipped} | ${(created / elapsedSec).toFixed(1)} prod/s`
      );
    }
  }
}

function printHelp() {
  console.log(`NexusMart Bulk Product Load Generator (separate from \`make seed\`)

Usage:
  node tools/loadgen/generate-products.mjs [options]

Options:
  -n, --count <N>         Number of products to generate (default: ${DEFAULT_PRODUCT_COUNT}, max: ${MAX_PRODUCT_COUNT})
  --mode <copy|api>       Write mode (default: copy)
                            copy: Bulk PostgreSQL COPY into local compose product_db (~150–600 prod/s incl. SKUs/history)
                            api:  Gateway REST API create+approve (subject to gateway rate limits)
  --batch-size <B>        Products per COPY transaction batch (default: 2000)
  --prefix <tag>          Deterministic ID/slug tag for resume & cleanup (default: loadgen)
  --database-url <url>    Local PostgreSQL URL for copy mode (MUST be localhost/127.0.0.1/::1 + product_db)
  --gateway-url <url>     Gateway URL for fetching shops/categories and api mode
  --no-resume             Do not query/skip existing indices before running COPY (still ON CONFLICT safe)
  --cleanup               Delete all generated products/SKUs/price-history matching --prefix
  --dry-run               Validate guardrails and print execution plan without writing
  -h, --help              Show this help message

Safety Guards (copy mode):
  - Strictly refuses non-localhost DATABASE_URL hosts (only localhost, 127.0.0.1, ::1 allowed)
  - Strictly refuses NODE_ENV=production / NEXUSMART_ENV=production / APP_ENV=production
  - Strictly targets database "product_db" and verifies local docker compose port binding
`);
}

async function main() {
  const { values } = parseArgs({
    options: {
      count: { type: "string", short: "n" },
      mode: { type: "string", default: "copy" },
      "batch-size": { type: "string", default: "2000" },
      prefix: { type: "string", default: "loadgen" },
      "database-url": { type: "string" },
      "gateway-url": { type: "string" },
      "no-resume": { type: "boolean", default: false },
      cleanup: { type: "boolean", default: false },
      "dry-run": { type: "boolean", default: false },
      help: { type: "boolean", short: "h", default: false },
    },
    strict: true,
  });

  if (values.help) {
    printHelp();
    return;
  }

  const mode = String(values.mode || "copy").toLowerCase();
  if (mode !== "copy" && mode !== "api") {
    throw new Error(`Invalid --mode "${mode}": expected "copy" or "api"`);
  }

  const dotEnv = loadDotEnv(resolve(REPO_ROOT, ".env"));
  const prefix = validatePrefix(values.prefix);
  const count = values.cleanup ? DEFAULT_PRODUCT_COUNT : validateCount(values.count);
  const batchSize = Math.max(1, Math.min(10000, Math.trunc(Number(values["batch-size"]) || 2000)));
  const resume = !values["no-resume"];
  const gatewayUrl = (
    values["gateway-url"] ||
    process.env.SEED_GATEWAY_URL ||
    `http://localhost:${dotEnv.GATEWAY_PORT || "8080"}`
  ).replace(/\/+$/, "");

  if (mode === "api") {
    if (values["dry-run"]) {
      console.log(`[dry-run] mode=api gateway=${gatewayUrl} count=${count} prefix=${prefix} cleanup=${values.cleanup}`);
      return;
    }
    await runApiMode({
      gatewayUrl,
      count,
      prefix,
      cleanup: values.cleanup,
      resume,
      dotEnv,
    });
    return;
  }

  // Copy mode: build default localhost DATABASE_URL from .env if not explicitly provided.
  const pgUser = dotEnv.POSTGRES_USER || "nexusmart";
  const pgPass = dotEnv.POSTGRES_PASSWORD || "nexusmart_dev_password";
  const pgPort = dotEnv.POSTGRES_PORT || "5432";
  const defaultDbUrl = `postgres://${encodeURIComponent(pgUser)}:${encodeURIComponent(pgPass)}@127.0.0.1:${pgPort}/product_db?sslmode=disable`;
  const databaseUrl = values["database-url"] || process.env.DATABASE_URL || defaultDbUrl;

  const dbCfg = assertSafeLocalDatabaseUrl(databaseUrl, process.env);
  if (values["dry-run"]) {
    console.log(
      `[dry-run] mode=copy db=${redactDatabaseUrl(dbCfg.url)} gateway=${gatewayUrl} count=${count} batchSize=${batchSize} prefix=${prefix} resume=${resume} cleanup=${values.cleanup}`
    );
    return;
  }

  const runner = createLocalCopyRunner(databaseUrl, process.env);

  if (values.cleanup) {
    console.log(
      `Loadgen cleanup starting (mode=copy via ${runner.transportKind}, db=${runner.redactedUrl}, prefix=${prefix})...`
    );
    const cleanRes = await runCopyCleanup({ prefix, runner });
    console.log(
      `Cleanup complete: deleted_products=${cleanRes.deletedProducts} deleted_skus=${cleanRes.deletedSkus} remaining_loadgen_products=${cleanRes.remainingProducts}`
    );
    return;
  }

  const { shops, categories } = await fetchLiveShopsAndCategories(gatewayUrl);
  console.log(
    `Loadgen starting: count=${count}, batchSize=${batchSize}, prefix=${prefix}, mode=copy (${runner.transportKind}), db=${runner.redactedUrl}, shops=${shops.length}, categories=${categories.length}`
  );

  const summary = await runCopyGenerate({
    count,
    batchSize,
    prefix,
    shops,
    categories,
    runner,
    resume,
    onProgress(ev) {
      const pct = ((ev.completed / ev.total) * 100).toFixed(1);
      console.log(
        `  [batch ${ev.batch}/${ev.totalBatches}] ${ev.completed.toLocaleString("en-US")}/${ev.total.toLocaleString("en-US")} products (${pct}%) | +${ev.generatedSkus.toLocaleString("en-US")} SKUs | ${Math.round(ev.ratePerSec).toLocaleString("en-US")} prod/s | elapsed ${ev.elapsedSec.toFixed(1)}s | ETA ${ev.etaSec.toFixed(1)}s`
      );
    },
  });

  console.log("\n=== Loadgen Summary ===");
  console.log(`  Prefix:     ${summary.prefix}`);
  console.log(`  Target:     ${summary.count.toLocaleString("en-US")} products`);
  console.log(`  Generated:  ${summary.generated.toLocaleString("en-US")} products (${summary.generatedSkus.toLocaleString("en-US")} SKUs + price history)`);
  console.log(`  Skipped:    ${summary.skipped.toLocaleString("en-US")} existing (resumed)`);
  console.log(
    `  Duration:   ${summary.elapsedSec.toFixed(2)}s (${summary.elapsedSec > 0 ? Math.round(summary.generated / summary.elapsedSec).toLocaleString("en-US") : "0"} prod/s)`
  );
  console.log(`  Cleanup:    node tools/loadgen/generate-products.mjs --prefix ${summary.prefix} --cleanup`);
}

if (process.argv[1] && resolve(process.argv[1]) === __filename) {
  main().catch((err) => {
    console.error("Loadgen failed:", err.message || err);
    process.exit(1);
  });
}
