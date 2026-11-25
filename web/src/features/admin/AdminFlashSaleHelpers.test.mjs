// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminFlashSaleHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canEndFlashSaleEarly,
  computeFlashDiscountPercent,
  getFlashSaleWindowMeta,
  validateCreateFlashSaleInput,
} from "./AdminFlashSaleHelpers.ts";

test("validateCreateFlashSaleInput validates min_discount_percent, category_ids, and window bounds", () => {
  const ok = validateCreateFlashSaleInput({
    name: "Flash sale trưa 12h",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T07:00:00Z",
    min_discount_percent: 15,
    category_ids: ["cat-1", "cat-2", "cat-1"],
  });
  assert.equal(ok.ok, true);
  if (ok.ok) {
    assert.equal(ok.value.min_discount_percent, 15);
    assert.deepEqual(ok.value.category_ids, ["cat-1", "cat-2"]);
  }

  const tooHighDiscount = validateCreateFlashSaleInput({
    name: "Giảm quá trần",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T07:00:00Z",
    min_discount_percent: 95,
  });
  assert.equal(tooHighDiscount.ok, false);
  if (!tooHighDiscount.ok) assert.equal(tooHighDiscount.code, "INVALID_MIN_DISCOUNT");

  const floatDiscount = validateCreateFlashSaleInput({
    name: "Giảm số lẻ",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T07:00:00Z",
    min_discount_percent: 12.5,
  });
  assert.equal(floatDiscount.ok, false);

  const badCatId = validateCreateFlashSaleInput({
    name: "Danh mục độc hại",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T07:00:00Z",
    category_ids: ["../etc/passwd"],
  });
  assert.equal(badCatId.ok, false);
  if (!badCatId.ok) assert.equal(badCatId.code, "INVALID_CATEGORY_IDS");

  const invertedWindow = validateCreateFlashSaleInput({
    name: "Sai khung giờ",
    starts_at: "2026-11-26T08:00:00Z",
    ends_at: "2026-11-26T07:00:00Z",
  });
  assert.equal(invertedWindow.ok, false);
});

test("getFlashSaleWindowMeta and canEndFlashSaleEarly respect explicit ENDED status over timestamps", () => {
  const now = Date.parse("2026-11-26T06:00:00Z");

  const endedEarly = {
    status: "ENDED",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T09:00:00Z",
  };
  assert.equal(getFlashSaleWindowMeta(endedEarly, now).state, "ENDED");
  assert.equal(canEndFlashSaleEarly(endedEarly, now), false);

  const activeWindow = {
    status: "ACTIVE",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T09:00:00Z",
  };
  assert.equal(getFlashSaleWindowMeta(activeWindow, now).state, "ACTIVE");
  assert.equal(canEndFlashSaleEarly(activeWindow, now), true);

  const upcomingWindow = {
    starts_at: "2026-11-26T08:00:00Z",
    ends_at: "2026-11-26T10:00:00Z",
  };
  assert.equal(getFlashSaleWindowMeta(upcomingWindow, now).state, "UPCOMING");
  assert.equal(canEndFlashSaleEarly(upcomingWindow, now), true);

  assert.equal(computeFlashDiscountPercent(200000, 150000), 25);
  assert.equal(computeFlashDiscountPercent(100000, 100000), null);
  assert.equal(computeFlashDiscountPercent(undefined, 90000), null);
});
