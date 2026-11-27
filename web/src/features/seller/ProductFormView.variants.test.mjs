// Run: node --test web/src/features/seller/ProductFormView.variants.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_VARIANT_NAME,
  MAX_VARIANT_OPTIONS,
  buildSkuPayloadName,
  createEmptyDefaultSkuRow,
  extractOptionsFromSkus,
  generateVariantCombinations,
  parseOptionValuesInput,
  toEditableFormState,
} from "./ProductFormView.variants.ts";

test("create mode starts blank with 1 default SKU row and zero options", () => {
  const state = toEditableFormState(null, "4242");
  assert.deepEqual(state.options, []);
  assert.equal(state.skuRows.length, 1);
  assert.equal(state.skuRows[0].name, DEFAULT_VARIANT_NAME);
  assert.deepEqual(state.skuRows[0].attributes, {});
  assert.equal(state.skuRows[0].priceVnd, 0);
  assert.equal(state.skuRows[0].stock, 0);
  assert.equal(state.skuRows[0].imageUrl, "");
});

test("parseOptionValuesInput splits commas, deduplicates case-insensitively, and preserves exact casing", () => {
  const first = parseOptionValuesInput("Men lam, Men rạn, men lam, ", []);
  assert.deepEqual(first, ["Men lam", "Men rạn"]);
  const second = parseOptionValuesInput("MEN RẠN, Men ngọc", first);
  assert.deepEqual(second, ["Men lam", "Men rạn", "Men ngọc"]);
});

test("generateVariantCombinations builds Cartesian combinations for up to 3 custom options without uppercasing", () => {
  const res = generateVariantCombinations({
    options: [
      { id: "o1", name: "Màu men", values: ["Men lam", "Men rạn"] },
      { id: "o2", name: "Quy cách", values: ["Bộ 7 món", "Bộ 12 món"] },
    ],
    existingRows: [createEmptyDefaultSkuRow("8888")],
    skuStamp: "8888",
    defaultPriceVnd: 450000,
    defaultStock: 10,
  });

  assert.equal(res.truncated, false);
  assert.equal(res.totalPossible, 4);
  assert.equal(res.rows.length, 4);
  assert.deepEqual(res.rows[0].attributes, { "Màu men": "Men lam", "Quy cách": "Bộ 7 món" });
  assert.equal(res.rows[0].name, "Màu men: Men lam · Quy cách: Bộ 7 món");
  assert.equal(res.rows[0].priceVnd, 450000);
  assert.equal(res.rows[0].stock, 10);

  // Unique SKU codes across all 4 rows
  const codes = new Set(res.rows.map((r) => r.skuCode));
  assert.equal(codes.size, 4);

  // 3 options (max) -> 2 * 2 * 2 = 8 combinations; 4th option is ignored
  const threeRes = generateVariantCombinations({
    options: [
      { id: "o1", name: "Màu men", values: ["Men lam", "Men rạn"] },
      { id: "o2", name: "Quy cách", values: ["Bộ 7 món", "Bộ 12 món"] },
      { id: "o3", name: "Dung tích", values: ["350ml", "500ml"] },
      { id: "o4", name: "Phụ kiện", values: ["Có khay", "Không khay"] },
    ],
    existingRows: [],
    skuStamp: "7777",
  });
  assert.equal(MAX_VARIANT_OPTIONS, 3);
  assert.equal(threeRes.rows.length, 8);
  assert.equal("Phụ kiện" in threeRes.rows[0].attributes, false);
});

test("generateVariantCombinations respects maxCombinations cap and preserves edited existing rows", () => {
  const capped = generateVariantCombinations({
    options: [
      { id: "o1", name: "A", values: ["1", "2", "3", "4"] },
      { id: "o2", name: "B", values: ["x", "y", "z"] },
    ],
    existingRows: [],
    skuStamp: "1111",
    maxCombinations: 5,
  });
  assert.equal(capped.totalPossible, 12);
  assert.equal(capped.truncated, true);
  assert.equal(capped.rows.length, 5);

  const initial = generateVariantCombinations({
    options: [
      { id: "o1", name: "Màu men", values: ["Men lam"] },
      { id: "o2", name: "Quy cách", values: ["Bộ 7 món"] },
    ],
    existingRows: [],
    skuStamp: "5555",
  });
  const editedRow = {
    ...initial.rows[0],
    id: "sku-existing-1",
    version: 3,
    reservedStock: 2,
    skuCode: "GOM-LAM-7",
    priceVnd: 680000,
    stock: 15,
    weightGrams: "1200",
    imageUrl: "/products/custom.png",
  };
  const regenerated = generateVariantCombinations({
    options: [
      { id: "o1", name: "Màu men", values: ["Men lam", "Men ngọc"] },
      { id: "o2", name: "Quy cách", values: ["Bộ 7 món"] },
    ],
    existingRows: [editedRow],
    skuStamp: "5555",
  });

  assert.equal(regenerated.rows.length, 2);
  assert.equal(regenerated.rows[0].id, "sku-existing-1");
  assert.equal(regenerated.rows[0].version, 3);
  assert.equal(regenerated.rows[0].reservedStock, 2);
  assert.equal(regenerated.rows[0].skuCode, "GOM-LAM-7");
  assert.equal(regenerated.rows[0].priceVnd, 680000);
  assert.equal(regenerated.rows[0].stock, 15);
  assert.equal(regenerated.rows[0].weightGrams, "1200");
  assert.equal(regenerated.rows[0].imageUrl, "/products/custom.png");
});

test("editing a legacy product preserves color/color_code/color_hex/size keys", () => {
  const legacyProduct = {
    id: "prod-1",
    shop_id: "shop-1",
    category_id: "cat-1",
    name: "Sản phẩm cũ",
    description: "",
    images: [],
    status: "APPROVED",
    min_price_vnd: 189000,
    max_price_vnd: 189000,
    skus: [
      {
        id: "sku-1",
        product_id: "prod-1",
        sku_code: "OLD-TRANG-M",
        name: "Trắng tinh / M",
        attributes: {
          color: "trang",
          color_name: "Trắng tinh",
          color_code: "TRANG",
          color_hex: "#FFFFFF",
          size: "M",
        },
        price_vnd: 189000,
        stock: 20,
        reserved_stock: 1,
        available_stock: 19,
        weight_grams: 220,
        version: 2,
        is_active: true,
        image_url: "",
      },
    ],
  };

  const hydrated = toEditableFormState(legacyProduct, "9999");
  assert.equal(hydrated.options.length, 2);
  assert.equal(hydrated.options[0].storageKey, "color");
  assert.equal(hydrated.options[0].name, "Màu sắc");
  assert.equal(hydrated.options[1].storageKey, "size");
  assert.equal(hydrated.options[1].name, "Kích cỡ");

  // Adding a new size L keeps legacy storage keys (`color`, `color_name`, `color_code`, `color_hex`, `size`)
  const updatedOptions = [
    hydrated.options[0],
    { ...hydrated.options[1], values: ["M", "L"] },
  ];
  const next = generateVariantCombinations({
    options: updatedOptions,
    existingRows: hydrated.skuRows,
    skuStamp: "9999",
  });
  assert.equal(next.rows.length, 2);
  assert.equal(next.rows[0].id, "sku-1");
  assert.equal(next.rows[0].attributes.color, "trang");
  assert.equal(next.rows[0].attributes.color_code, "TRANG");
  assert.equal(next.rows[0].attributes.color_hex, "#FFFFFF");
  assert.equal(next.rows[0].attributes.size, "M");
  assert.equal(next.rows[1].attributes.color, "trang");
  assert.equal(next.rows[1].attributes.color_name, "Trắng tinh");
  assert.equal(next.rows[1].attributes.color_code, "TRANG");
  assert.equal(next.rows[1].attributes.size, "L");
  assert.equal(buildSkuPayloadName({}), DEFAULT_VARIANT_NAME);
  assert.equal(extractOptionsFromSkus([{ attributes: {} }]).length, 0);
});
