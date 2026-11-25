/**
 * Wave 3b-3 web-b1 behavioral, RBAC & contract verification tests.
 * Note: Marketplace-only search-client.ts and shop-lookup.ts were deleted per DECISIONS
 * (single-shop storefront search is covered by web/src/features/search/search.test.mjs).
 */
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(WEB_ROOT, "..");
const WEB_SRC = resolve(WEB_ROOT, "src");

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith("@/")) {
      const rel = specifier.slice(2);
      const tsPath = resolve(WEB_SRC, `${rel}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(WEB_SRC, rel, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const tsPath = resolve(parentDir, `${specifier}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(parentDir, specifier, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    return nextResolve(specifier, context);
  },
});

describe("Wave 3b-3 web-b1 — RBAC capabilities sync, loadgen assets & SKU matching", () => {
  test("1. [RBAC] admin-rbac.ts matches pkg/common/auth/capabilities.json 1:1 (all roles and all capabilities)", async () => {
    const rbac = await import("../src/lib/admin-rbac.ts");
    const canonical = JSON.parse(
      readFileSync(resolve(REPO_ROOT, "pkg/common/auth/capabilities.json"), "utf8")
    );

    assert.deepEqual(
      [...rbac.ADMIN_STAFF_ROLES].sort(),
      [...canonical.roles].sort(),
      "ADMIN_STAFF_ROLES must match canonical roles 1:1"
    );
    assert.deepEqual(
      Object.keys(rbac.CAPABILITY_MATRIX).sort(),
      Object.keys(canonical.capabilities).sort(),
      "CAPABILITY_MATRIX keys must match pkg/common/auth/capabilities.json 1:1"
    );

    for (const [cap, roles] of Object.entries(canonical.capabilities)) {
      assert.deepEqual(
        [...rbac.CAPABILITY_MATRIX[cap]].sort(),
        [...roles].sort(),
        `Capability ${cap} roles in admin-rbac.ts must match pkg/common/auth/capabilities.json 1:1`
      );
    }
  });

  test("2. [§10-2] tools/loadgen/generate-products.mjs references existing do-lot-boxer-modal-3.png and valid product images", () => {
    const loadgenSrc = readFileSync(
      resolve(REPO_ROOT, "tools/loadgen/generate-products.mjs"),
      "utf8"
    );
    assert.equal(
      loadgenSrc.includes('"/products/do-lot-boxer-modal.png"'),
      false,
      "broken image path /products/do-lot-boxer-modal.png must be removed"
    );
    assert.ok(
      loadgenSrc.includes('"/products/do-lot-boxer-modal-3.png"'),
      "do-lot must point to /products/do-lot-boxer-modal-3.png"
    );

    const imageBlockMatch = loadgenSrc.match(/const IMAGE_BY_CATEGORY = \{([\s\S]*?)\};/);
    assert.ok(imageBlockMatch, "IMAGE_BY_CATEGORY must exist in generate-products.mjs");
    const paths = Array.from(imageBlockMatch[1].matchAll(/"(\/products\/[^"]+)"/g)).map(
      (m) => m[1]
    );
    assert.ok(paths.length >= 6, `expected >= 6 category images, got ${paths.length}`);
    for (const rel of paths) {
      const fullPath = resolve(WEB_ROOT, "public", rel.slice(1));
      assert.equal(existsSync(fullPath), true, `missing product asset on disk: ${rel}`);
    }
  });

  test("9. [E2E Web] tests/e2e-web/22_search_b1.test.mjs exists and covers B1 site search, gateway suggest/history, and admin reindex RBAC", () => {
    const e2ePath = resolve(REPO_ROOT, "tests/e2e-web/22_search_b1.test.mjs");
    assert.equal(existsSync(e2ePath), true, "tests/e2e-web/22_search_b1.test.mjs must exist");
    const src = readFileSync(e2ePath, "utf8");
    assert.ok(
      src.includes("/api/search/suggest") && src.includes("/api/v1/search/suggest"),
      "E2E must test site & gateway search suggest"
    );
    assert.ok(src.includes("/api/v1/search/history"), "E2E must test gateway search history");
    assert.ok(src.includes("/api/v1/admin/search/reindex"), "E2E must test admin reindex RBAC");
  });

  test("15. [Review C1] matchGatewaySku matches color_code or folded color name and never substitutes another color/size when OOS", async () => {
    const apiMod = await import("../src/lib/api.ts");

    assert.equal(
      typeof apiMod.matchGatewaySku,
      "function",
      "api.ts must export pure matchGatewaySku helper used by resolveGatewaySkuId"
    );

    const skusFirstColorOos = [
      {
        id: "sku-den-m",
        sku_code: "POLO-DEN-M",
        available_stock: 0,
        is_active: true,
        attributes: { color_code: "den", color: "Đen than", size: "M" },
      },
      {
        id: "sku-trang-m",
        sku_code: "POLO-TRANG-M",
        available_stock: 12,
        is_active: true,
        attributes: { color_code: "trang", color: "Trắng", size: "M" },
      },
    ];

    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "denthan",
        colorName: "Đen than",
        size: "M",
      }),
      null,
      "must NOT fall back to another color when requested color + size is out of stock"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "den",
        colorName: "Đen than",
        size: "M",
      }),
      null
    );
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "trang",
        colorName: "Trắng",
        size: "M",
      }),
      "sku-trang-m"
    );

    const skusBothInStock = [
      {
        id: "sku-trang-m",
        sku_code: "POLO-TRANG-M",
        available_stock: 12,
        is_active: true,
        attributes: { color_code: "trang", color: "Trắng", size: "M" },
      },
      {
        id: "sku-den-m",
        sku_code: "POLO-DEN-M",
        available_stock: 5,
        is_active: true,
        attributes: { color_code: "den", color: "Đen than", size: "M" },
      },
      {
        id: "sku-be-l",
        sku_code: "POLO-BE-L",
        available_stock: 4,
        is_active: true,
        attributes: { color_code: "be", color: "Be cát", size: "L" },
      },
    ];
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "denthan",
        colorName: "Đen than",
        size: "M",
      }),
      "sku-den-m",
      "must match Đen than by accent-folded attributes.color when colorCode is slugified 'denthan'"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "becat",
        colorName: "Be cát",
        size: "L",
      }),
      "sku-be-l"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorName: "den than",
        size: "m",
      }),
      "sku-den-m"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "den",
        colorName: "Đen than",
        size: "L",
      }),
      null,
      "non-existent color x size pair must return null, not a different color or size"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "mac-dinh",
        colorName: "Mặc định",
        size: "L",
      }),
      "sku-be-l"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "mac-dinh",
        colorName: "Mặc định",
      }),
      "sku-trang-m"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "denthan",
        colorName: "Đen than",
      }),
      "sku-den-m"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "denthan",
        colorName: "Đen than",
      }),
      null
    );
  });

  test("18. [Review C5] tests/e2e-web/22_search_b1.test.mjs covers multi-color site PDP ?color= resolution (root quick-add UI/mock đã bỏ theo DECISIONS; Đen than SKU match covered by Test 15)", () => {
    const e2eSrc = readFileSync(resolve(REPO_ROOT, "tests/e2e-web/22_search_b1.test.mjs"), "utf8");
    assert.ok(
      e2eSrc.includes("?color=trang"),
      "22_search_b1.test.mjs must cover multi-color site PDP ?color= resolution"
    );
    assert.ok(
      e2eSrc.includes("/api/search?q="),
      "22_search_b1.test.mjs must verify site-scoped search API"
    );
  });
});
