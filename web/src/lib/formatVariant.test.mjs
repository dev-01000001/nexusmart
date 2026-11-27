// Run: node --test web/src/lib/formatVariant.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildDynamicOptionGroups,
  formatSkuShortName,
  formatVariantAttributes,
  formatVariantLabel,
  getSkuOptionMap,
  normalizeVariantEntries,
} from "./formatVariant.ts";

test("normalizeVariantEntries maps legacy color/size keys and hides color_code & color_hex", () => {
  const entries = normalizeVariantEntries({
    color: "trang",
    color_name: "Trắng tinh",
    color_code: "TRANG",
    color_hex: "#F5F5F4",
    size: "L",
  });
  assert.deepEqual(entries, [
    { key: "color", label: "Màu sắc", value: "Trắng tinh", hex: "#F5F5F4" },
    { key: "size", label: "Kích cỡ", value: "L" },
  ]);
  assert.equal(
    formatVariantAttributes({
      color: "trang",
      color_name: "Trắng tinh",
      color_code: "TRANG",
      color_hex: "#F5F5F4",
      size: "L",
    }),
    "Màu sắc: Trắng tinh · Kích cỡ: L"
  );
});

test("formatVariantAttributes renders custom non-garment keys verbatim without uppercasing values", () => {
  const ceramic = {
    "Màu men": "Men lam",
    "Quy cách": "Bộ 7 món",
    "Dung tích": "450ml",
  };
  assert.equal(
    formatVariantAttributes(ceramic),
    "Màu men: Men lam · Quy cách: Bộ 7 món · Dung tích: 450ml"
  );
  assert.equal(formatSkuShortName(ceramic), "Men lam / Bộ 7 món / 450ml");

  // Preserves lowercase/mixed-case legacy values too (never auto-uppercases).
  assert.equal(formatVariantAttributes({ color: "Xanh rêu", size: "xl" }), "Màu sắc: Xanh rêu · Kích cỡ: xl");
});

test("formatVariantLabel falls back cleanly when attributes are empty or hidden-only", () => {
  assert.equal(
    formatVariantLabel({ attributes: {}, skuName: "Bộ tiêu chuẩn", fallback: "Mặc định" }),
    "Bộ tiêu chuẩn"
  );
  assert.equal(
    formatVariantLabel({ attributes: { color_code: "DEN", color_hex: "#111111" }, fallback: "Mặc định" }),
    "Mặc định"
  );
  assert.equal(formatVariantLabel({ attributes: null, skuName: "", fallback: "Mặc định" }), "Mặc định");
  assert.equal(formatSkuShortName({}), "Mặc định");
});

test("buildDynamicOptionGroups derives dynamic selector groups for custom, legacy, and simple SKUs", () => {
  // Custom ceramic product -> arbitrary option labels, no fake color swatches
  const ceramicGroups = buildDynamicOptionGroups([
    { attributes: { "Màu men": "Men lam", "Quy cách": "Bộ 7 món" } },
    { attributes: { "Màu men": "Men lam", "Quy cách": "Bộ 12 món" } },
    { attributes: { "Màu men": "Men rạn", "Quy cách": "Bộ 7 món" } },
  ]);
  assert.deepEqual(ceramicGroups, [
    {
      key: "Màu men",
      label: "Màu men",
      isColorSwatch: false,
      values: [{ value: "Men lam" }, { value: "Men rạn" }],
    },
    {
      key: "Quy cách",
      label: "Quy cách",
      isColorSwatch: false,
      values: [{ value: "Bộ 7 món" }, { value: "Bộ 12 món" }],
    },
  ]);

  // Legacy product with color_hex -> swatch enabled ONLY on color group
  const legacyGroups = buildDynamicOptionGroups([
    {
      attributes: { color: "trang", color_name: "Trắng tinh", color_hex: "#FFFFFF", size: "M" },
      image: "/products/a.png",
    },
    {
      attributes: { color: "den", color_name: "Đen", color_hex: "#111111", size: "L" },
      image: "/products/b.png",
    },
  ]);
  assert.equal(legacyGroups.length, 2);
  assert.equal(legacyGroups[0].label, "Màu sắc");
  assert.equal(legacyGroups[0].isColorSwatch, true);
  assert.deepEqual(legacyGroups[0].values, [
    { value: "Trắng tinh", hex: "#FFFFFF", image: "/products/a.png" },
    { value: "Đen", hex: "#111111", image: "/products/b.png" },
  ]);
  assert.equal(legacyGroups[1].label, "Kích cỡ");
  assert.equal(legacyGroups[1].isColorSwatch, false);

  // Simple 1-SKU product with empty attributes -> no option selectors
  assert.deepEqual(buildDynamicOptionGroups([{ attributes: {} }]), []);

  // Option map helper matches logical keys
  assert.deepEqual(
    getSkuOptionMap({ color: "trang", color_name: "Trắng tinh", color_code: "TRANG", size: "L" }),
    { color: "Trắng tinh", size: "L" }
  );
});
