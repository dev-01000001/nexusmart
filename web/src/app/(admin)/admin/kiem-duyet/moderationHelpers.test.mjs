// Run: node --test web/src/app/(admin)/admin/kiem-duyet/moderationHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ADMIN_MODERATION_PAGE_SIZE,
  buildAdminProductsQuery,
  clampModerationPage,
  createEmptyProductTabCounts,
  normalizeProductTabCounts,
  pickNextProductIdOnPage,
} from "./moderationHelpers.ts";

test("buildAdminProductsQuery builds server-side status, keyword, and pagination query params", () => {
  assert.equal(
    buildAdminProductsQuery({ status: "all", keyword: "", page: 1 }),
    `page=1&page_size=${ADMIN_MODERATION_PAGE_SIZE}`
  );
  assert.equal(
    buildAdminProductsQuery({ status: "PENDING", keyword: "  gốm sứ  ", page: 8, pageSize: 15 }),
    "page=8&page_size=15&status=PENDING&keyword=g%E1%BB%91m+s%E1%BB%A9"
  );
  // Clamps invalid pages and oversized keywords safely
  const clamped = buildAdminProductsQuery({
    status: "APPROVED",
    keyword: "a".repeat(200),
    page: -3,
    pageSize: 500,
  });
  const sp = new URLSearchParams(clamped);
  assert.equal(sp.get("page"), "1");
  assert.equal(sp.get("page_size"), "100");
  assert.equal(sp.get("status"), "APPROVED");
  assert.equal(sp.get("keyword")?.length, 120);
});

test("clampModerationPage handles large server totals beyond 100 items accurately", () => {
  // Real audit dataset: 24,034 total products / 2,974 pending
  assert.deepEqual(clampModerationPage(1, 24034, 15), { page: 1, totalPages: 1603 });
  assert.deepEqual(clampModerationPage(199, 2974, 15), { page: 199, totalPages: 199 });
  assert.deepEqual(clampModerationPage(500, 2974, 15), { page: 199, totalPages: 199 });
  assert.deepEqual(clampModerationPage(0, 0, 15), { page: 1, totalPages: 1 });
});

test("normalizeProductTabCounts preserves real API totals and ignores invalid values", () => {
  const counts = normalizeProductTabCounts(
    {
      all: 24034,
      PENDING: 2974,
      APPROVED: 20500,
      REJECTED: -5,
    },
    { ...createEmptyProductTabCounts(), REJECTED: 120 }
  );
  assert.deepEqual(counts, {
    all: 24034,
    PENDING: 2974,
    APPROVED: 20500,
    REJECTED: 120,
    OUT_OF_STOCK: 0,
    HIDDEN: 0,
  });
});

test("pickNextProductIdOnPage prefers the next PENDING product on the current page", () => {
  const pageItems = [
    { id: "p-1", status: "APPROVED" },
    { id: "p-2", status: "REJECTED" },
    { id: "p-3", status: "PENDING" },
    { id: "p-4", status: "PENDING" },
  ];
  assert.equal(pickNextProductIdOnPage(pageItems, "p-1", "all"), "p-3");
  assert.equal(pickNextProductIdOnPage(pageItems, "p-3", "PENDING"), "p-4");
  assert.equal(pickNextProductIdOnPage([{ id: "p-1", status: "APPROVED" }], "p-1", "PENDING"), null);
});
