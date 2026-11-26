// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminFlashSaleHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  canEndFlashSaleEarly,
  collectPaginatedFlashSaleItems,
  computeFlashDiscountPercent,
  formatFlashSaleItemsNotice,
  getFlashSaleWindowMeta,
  loadAdminFlashSaleItems,
  mergeFlashSaleItems,
  validateApproveFlashSaleItemInput,
  validateCreateFlashSaleInput,
  validateFlashSaleItemStatusFilter,
} from "./AdminFlashSaleHelpers.ts";

const WEB_SRC = resolve(fileURLToPath(new URL("../..", import.meta.url)));

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

test("validateApproveFlashSaleItemInput requires a real boolean approve and route does not coerce Boolean(raw.approve)", () => {
  const approved = validateApproveFlashSaleItemInput({ approve: true });
  assert.deepEqual(approved, { ok: true, value: { approve: true, reason: "" } });

  const rejected = validateApproveFlashSaleItemInput({ approve: false, reason: "  Chưa đạt mức giảm  " });
  assert.deepEqual(rejected, {
    ok: true,
    value: { approve: false, reason: "Chưa đạt mức giảm" },
  });

  for (const bad of [
    {},
    { approve: undefined },
    { approve: null },
    { approve: "true" },
    { approve: "false" },
    { approve: 1 },
    { approve: 0 },
    null,
    [],
  ]) {
    const res = validateApproveFlashSaleItemInput(bad);
    assert.equal(res.ok, false, `expected rejection for ${JSON.stringify(bad)}`);
    if (!res.ok) assert.equal(res.code, "INVALID_ARGUMENT");
  }

  const routeSrc = readFileSync(
    resolve(WEB_SRC, "app/api/admin/flash-sale-items/[id]/approve/route.ts"),
    "utf8"
  );
  assert.equal(
    routeSrc.includes("Boolean(raw.approve)"),
    false,
    "approve route must not coerce raw.approve with Boolean()"
  );
  const validatePos = routeSrc.indexOf("validateApproveFlashSaleItemInput");
  const gwPos = routeSrc.indexOf("await gw<");
  assert.ok(
    validatePos > 0 && gwPos > validatePos,
    "approve route must validate input before calling gateway"
  );
});

test("mergeFlashSaleItems attaches PENDING and REJECTED items from ListFlashSaleItems alongside APPROVED items without duplicates", () => {
  // Real gateway JSON shape from GET /api/v1/admin/flash-sales (attachApprovedFlashItems -> APPROVED only)
  const flashSales = [
    {
      id: "fs-1",
      name: "Flash sale 12h",
      starts_at: "2026-11-26T05:00:00Z",
      ends_at: "2026-11-26T07:00:00Z",
      status: "UPCOMING",
      min_discount_percent: 15,
      category_ids: [],
      items: [
        {
          id: "fsi-approved",
          flash_sale_id: "fs-1",
          shop_id: "shop-1",
          sku_id: "sku-1",
          flash_price_vnd: 149000,
          original_price_vnd: 200000,
          total_slots: 20,
          remaining_slots: 20,
          per_user_limit: 2,
          status: "APPROVED",
          rejection_reason: "",
          created_at: "2026-11-25T10:00:00Z",
        },
      ],
    },
    {
      id: "fs-2",
      name: "Flash sale 20h",
      starts_at: "2026-11-26T13:00:00Z",
      ends_at: "2026-11-26T15:00:00Z",
      status: "UPCOMING",
      min_discount_percent: 10,
      category_ids: [],
      items: [],
    },
  ];

  // Real gateway JSON shape from GET /api/v1/admin/flash-sale-items (all statuses)
  const allItems = [
    {
      id: "fsi-pending",
      flash_sale_id: "fs-1",
      shop_id: "shop-2",
      sku_id: "sku-2",
      flash_price_vnd: 99000,
      original_price_vnd: 150000,
      total_slots: 10,
      remaining_slots: 10,
      per_user_limit: 1,
      status: "PENDING",
      rejection_reason: "",
      created_at: "2026-11-25T12:00:00Z",
    },
    {
      id: "fsi-rejected",
      flash_sale_id: "fs-2",
      shop_id: "shop-3",
      sku_id: "sku-3",
      flash_price_vnd: 180000,
      original_price_vnd: 190000,
      total_slots: 5,
      remaining_slots: 5,
      per_user_limit: 1,
      status: "REJECTED",
      rejection_reason: "Chưa đạt mức giảm tối thiểu",
      created_at: "2026-11-25T11:00:00Z",
    },
    {
      id: "fsi-approved",
      flash_sale_id: "fs-1",
      shop_id: "shop-1",
      sku_id: "sku-1",
      flash_price_vnd: 149000,
      original_price_vnd: 200000,
      total_slots: 20,
      remaining_slots: 18,
      per_user_limit: 2,
      status: "APPROVED",
      rejection_reason: "",
      created_at: "2026-11-25T10:00:00Z",
    },
    {
      id: "fsi-other-window",
      flash_sale_id: "fs-999",
      shop_id: "shop-9",
      sku_id: "sku-9",
      flash_price_vnd: 50000,
      total_slots: 5,
      remaining_slots: 5,
      per_user_limit: 1,
      status: "PENDING",
      rejection_reason: "",
    },
  ];

  const merged = mergeFlashSaleItems(flashSales, allItems);
  assert.equal(merged.length, 2);
  assert.equal(merged[0].items.length, 2);
  assert.deepEqual(
    merged[0].items.map((it) => it.id),
    ["fsi-pending", "fsi-approved"]
  );
  assert.equal(merged[0].items.find((it) => it.id === "fsi-approved")?.remaining_slots, 18);
  assert.equal(merged[1].items.length, 1);
  assert.equal(merged[1].items[0].status, "REJECTED");
  assert.equal(merged[1].items[0].rejection_reason, "Chưa đạt mức giảm tối thiểu");
});

test("collectPaginatedFlashSaleItems loads multiple pages and validateFlashSaleItemStatusFilter guards query values", async () => {
  const seenPages = [];
  const collected = await collectPaginatedFlashSaleItems(async (page, pageSize) => {
    seenPages.push([page, pageSize]);
    if (page === 1) {
      return { items: [{ id: "a" }, { id: "b" }], page: 1, page_size: 2, total: 3 };
    }
    return { items: [{ id: "c" }], page: 2, page_size: 2, total: 3 };
  }, { pageSize: 2, maxPages: 5 });

  assert.deepEqual(seenPages, [
    [1, 2],
    [2, 2],
  ]);
  assert.deepEqual(
    collected.map((x) => x.id),
    ["a", "b", "c"]
  );

  assert.equal(validateFlashSaleItemStatusFilter(""), "");
  assert.equal(validateFlashSaleItemStatusFilter("pending"), "PENDING");
  assert.equal(validateFlashSaleItemStatusFilter("APPROVED"), "APPROVED");
  assert.equal(validateFlashSaleItemStatusFilter("rejected"), "REJECTED");
  assert.equal(validateFlashSaleItemStatusFilter("UNKNOWN"), null);
});

test("admin flash-sale BFF, SSR page, and client wire GET /flash-sale-items and mergeFlashSaleItems", () => {
  const bffSrc = readFileSync(
    resolve(WEB_SRC, "app/api/admin/flash-sale-items/route.ts"),
    "utf8"
  );
  assert.ok(bffSrc.includes('assertCapability("flash_sales.read")'));
  assert.ok(bffSrc.includes('"/api/v1/admin/flash-sale-items"'));

  const pageSrc = readFileSync(
    resolve(WEB_SRC, "app/(admin)/admin/flash-sale/page.tsx"),
    "utf8"
  );
  assert.ok(pageSrc.includes("/api/v1/admin/flash-sale-items"));
  assert.ok(pageSrc.includes("mergeFlashSaleItems"));
  assert.ok(pageSrc.includes("loadAdminFlashSaleItems"));
  assert.equal(
    pageSrc.includes("loadError={flashSalesRes.error ?? flashItemsRes.error}"),
    false,
    "page.tsx must not merge flash-sale-items sub-fetch error into full-page loadError"
  );
  assert.ok(pageSrc.includes("initialItemsError={flashItemsRes.error}"));

  const clientSrc = readFileSync(
    resolve(WEB_SRC, "app/(admin)/admin/flash-sale/AdminFlashSaleClient.tsx"),
    "utf8"
  );
  assert.ok(clientSrc.includes("/api/admin/flash-sale-items"));
  assert.ok(clientSrc.includes("mergeFlashSaleItems"));
  assert.ok(clientSrc.includes("loadAdminFlashSaleItems"));
  assert.ok(clientSrc.includes("Promise.allSettled"));
});

test("loadAdminFlashSaleItems fetches all status=PENDING pages separately, reports unloaded count, and isolates sub-fetch errors", async () => {
  // Real gateway JSON response shape from GET /api/v1/admin/flash-sale-items:
  // { items: FlashSaleItemDTO[], page, page_size, total } ordered created_at DESC.
  const makeItem = (id, status, flashSaleId = "fs-1") => ({
    id,
    flash_sale_id: flashSaleId,
    shop_id: "shop-1",
    sku_id: `sku-${id}`,
    flash_price_vnd: 99000,
    original_price_vnd: 150000,
    total_slots: 10,
    remaining_slots: 10,
    per_user_limit: 1,
    status,
    rejection_reason: "",
    created_at: "2026-11-25T10:00:00Z",
  });

  // 12 total items: 4 APPROVED (recent) + 5 PENDING across 3 pages (pageSize=2) + 3 REJECTED (older).
  // With allMaxPages=2 (4 items max), older PENDING items are pushed past page 2 of global list,
  // but separate status=PENDING query must load ALL 5 PENDING items.
  const allDb = [
    makeItem("a-1", "APPROVED"),
    makeItem("a-2", "APPROVED"),
    makeItem("a-3", "APPROVED"),
    makeItem("p-1", "PENDING"),
    makeItem("r-1", "REJECTED"),
    makeItem("r-2", "REJECTED"),
    makeItem("r-3", "REJECTED"),
    makeItem("p-2", "PENDING"),
    makeItem("p-3", "PENDING"),
    makeItem("p-4", "PENDING"),
    makeItem("p-5", "PENDING"),
    makeItem("a-4", "APPROVED"),
  ];
  const pendingDb = allDb.filter((it) => it.status === "PENDING");
  const queries = [];

  const loaded = await loadAdminFlashSaleItems(
    async ({ status, page, pageSize }) => {
      queries.push({ status: status ?? "", page, pageSize });
      const src = status === "PENDING" ? pendingDb : allDb;
      const start = (page - 1) * pageSize;
      return {
        items: src.slice(start, start + pageSize),
        page,
        page_size: pageSize,
        total: src.length,
      };
    },
    { pageSize: 2, allMaxPages: 2, pendingMaxPages: 10 }
  );

  assert.equal(loaded.error, null);
  assert.ok(queries.some((q) => q.status === "PENDING" && q.page === 3));
  for (const id of ["p-1", "p-2", "p-3", "p-4", "p-5"]) {
    assert.ok(
      loaded.items.some((it) => it.id === id),
      `expected older PENDING item ${id} to be loaded`
    );
  }
  // 5 unique PENDING + 3 recent APPROVED = 8 unique items out of 12 total -> 4 unloaded
  assert.equal(loaded.items.length, 8);
  assert.equal(loaded.meta.totalCount, 12);
  assert.equal(loaded.meta.loadedCount, 8);
  assert.equal(loaded.meta.unloadedCount, 4);
  assert.equal(loaded.meta.pendingUnloadedCount, 0);
  const notice = formatFlashSaleItemsNotice(loaded.meta);
  assert.ok(notice && notice.includes("4"));
  assert.equal(
    formatFlashSaleItemsNotice({
      loadedCount: 5,
      totalCount: 5,
      unloadedCount: 0,
      pendingUnloadedCount: 0,
    }),
    null
  );

  // Partial failure: global list fails while status=PENDING succeeds -> keep PENDING items + local warning
  const partial = await loadAdminFlashSaleItems(
    async ({ status, page, pageSize }) => {
      if (!status) throw new Error("promotion temporarily unavailable");
      const start = (page - 1) * pageSize;
      return {
        items: pendingDb.slice(start, start + pageSize),
        page,
        page_size: pageSize,
        total: pendingDb.length,
      };
    },
    { pageSize: 2, allMaxPages: 2, pendingMaxPages: 10 }
  );
  assert.equal(partial.items.length, 5);
  assert.ok(partial.error && partial.error.length > 0);

  // Full failure: both fail -> empty list + local error (never throws)
  const failed = await loadAdminFlashSaleItems(async () => {
    throw new Error("503");
  });
  assert.deepEqual(failed.items, []);
  assert.ok(failed.error && failed.error.length > 0);
});
