/**
 * Wave 3b-3 web behavioral & contract verification tests for Agent `web-a8`
 * (A8 Buyer/Seller features, B15 voucher cleanup, §10 pricing UI, and 3b-2 AdminApprovalsPanel fix).
 * Enforces G1–G8 from .agents/briefs/fix/w3b-plan.md §5 and docs/contracts.md.
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
    if (specifier === "next/navigation") {
      return {
        shortCircuit: true,
        url: "data:text/javascript,export function useSearchParams(){return new URLSearchParams();}",
      };
    }
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

function readSrc(relPath) {
  return readFileSync(resolve(WEB_SRC, relPath), "utf8");
}

describe("Wave 3b-3 web-a8 — 1. AdminApprovalsPanel effect re-run fix", () => {
  test("1.1 resolvePendingActorIds only skips settled IDs; cancelled effect run never permanently drops actor lookup", async () => {
    const a8 = await import("../src/lib/a8-types.ts");
    const u1 = "11111111-1111-4111-8111-111111111111";
    const u2 = "22222222-2222-4222-8222-222222222222";
    const settled = new Set();

    // Simulate React StrictMode / effect re-run:
    // Run 1 starts fetching u1, then gets cancelled before resolving.
    const batch1 = a8.filterUnresolvedActorIds([u1, u2, "not-a-uuid", ""], settled);
    assert.deepEqual(batch1, [u1, u2]);

    // Because Run 1 was cancelled before completing, `settled` still does not contain u1/u2.
    const batch2 = a8.filterUnresolvedActorIds([u1, u2], settled);
    assert.deepEqual(batch2, [u1, u2], "cancelled run must not poison settled set");

    // Once Run 2 resolves u1 successfully, u1 is marked settled and skipped on Run 3,
    // while u2 (if transient error occurred) remains eligible to retry.
    settled.add(u1);
    const batch3 = a8.filterUnresolvedActorIds([u1, u2], settled);
    assert.deepEqual(batch3, [u2]);
  });

  test("1.2 AdminApprovalsPanel.tsx does not permanently mark IDs fetched before fetchShopOwnerById settles", () => {
    const src = readSrc("features/admin/AdminApprovalsPanel.tsx");
    const eagerLoopPattern =
      /for\s*\(\s*const\s+id\s+of\s+toFetch\s*\)\s*\{\s*fetchedUserIdsRef\.current\.add\(id\)/;
    assert.equal(
      eagerLoopPattern.test(src),
      false,
      "AdminApprovalsPanel must not eagerly add to fetchedUserIdsRef before fetch resolves without rollback"
    );
    assert.ok(
      src.includes("fetchedUserIdsRef.current.add(id)") ||
        src.includes("fetchedUserIdsRef.current.delete(id)"),
      "AdminApprovalsPanel must only settle IDs after completion or roll back on cancel/error"
    );
  });
});

describe("Wave 3b-3 web-a8 — 2. UC-B08 /tra-cuu-don-hang (G7)", () => {
  test("2.1 OrderLookupView removes guestOrderLookup mock and 4 fake NM-261120-* codes, wires POST /orders/lookup", () => {
    assert.equal(
      existsSync(resolve(WEB_SRC, "mock/guestOrderLookup.ts")),
      false,
      "mock/guestOrderLookup.ts must be deleted"
    );
    const src = readSrc("features/commerce/OrderLookupView.tsx");
    assert.equal(src.includes("guestOrderLookup"), false, "must remove guestOrderLookup import");
    assert.equal(src.includes("NM-261120-"), false, "must remove hardcoded fake order codes");
    assert.equal(
      src.includes("GUEST_LOOKUP_SAMPLES"),
      false,
      "must remove sample guest order buttons"
    );
    assert.ok(src.includes("orders/lookup"), "must call POST /api/gw/orders/lookup");
    assert.ok(src.includes("429"), "must handle 429 rate limit explicitly");
    assert.ok(src.includes("404"), "must handle 404 uniform not-found explicitly");
    assert.equal(src.includes("street_address"), false, "must never render street_address");
    assert.equal(src.includes("label_url"), false, "must never render label_url");
    assert.ok(
      src.includes("ward_name") && src.includes("province_name"),
      "must display only ward and province area"
    );
    assert.equal(
      src.includes("errBody?.error?.message"),
      false,
      "W4: OrderLookupView must never render raw backend English error.message"
    );
    assert.ok(
      src.includes("translateOrderLookupError"),
      "W4: OrderLookupView must translate error codes to Vietnamese"
    );
  });

  test("2.2 formatPublicOrderArea, formatLookupRateLimitMessage & W4 translateOrderLookupError", async () => {
    const a8 = await import("../src/lib/a8-types.ts");
    assert.equal(
      a8.formatPublicOrderArea("Phường Bến Nghé", "TP. Hồ Chí Minh"),
      "Phường Bến Nghé, TP. Hồ Chí Minh"
    );
    assert.equal(a8.formatPublicOrderArea("", "Hà Nội"), "Hà Nội");
    assert.equal(a8.formatPublicOrderArea("", ""), "Đang cập nhật khu vực giao hàng");
    assert.ok(a8.formatLookupRateLimitMessage("45").includes("45 giây"));
    assert.ok(a8.formatLookupRateLimitMessage(null).includes("quá số lần"));
    assert.equal(
      a8.translateOrderLookupError(404, "ORDER_LOOKUP_NOT_FOUND"),
      a8.ORDER_LOOKUP_NOT_FOUND_MESSAGE
    );
    assert.ok(a8.translateOrderLookupError(429, "RATE_LIMITED", "30").includes("30 giây"));
    assert.ok(a8.translateOrderLookupError(400, "INVALID_ARGUMENT").includes("chưa hợp lệ"));
    assert.ok(a8.translateOrderLookupError(503, "UNAVAILABLE").includes("tạm bận"));
  });
});

describe("Wave 3b-3 web-a8 — 3. UC-B26 OrderDetailClient address update (G1–G3)", () => {
  test("3.1 canBuyerUpdateOrderAddress & classifyAddressUpdatePreview enforce G1–G2 rules", async () => {
    const a8 = await import("../src/lib/a8-types.ts");

    assert.equal(
      a8.canBuyerUpdateOrderAddress({ status: "CONFIRMED", shipment_job_status: "" }),
      true
    );
    assert.equal(
      a8.canBuyerUpdateOrderAddress({ status: "PREPARING", shipment_job_status: "" }),
      true
    );
    assert.equal(
      a8.canBuyerUpdateOrderAddress({ status: "PREPARING", shipment_job_status: "QUEUED" }),
      false
    );
    for (const st of [
      "PENDING",
      "SHIPPING",
      "DELIVERED",
      "COMPLETED",
      "CANCELLED",
      "RETURN_REQUESTED",
      "RETURNED",
    ]) {
      assert.equal(
        a8.canBuyerUpdateOrderAddress({ status: st, shipment_job_status: "" }),
        false,
        `status ${st} must not allow address update`
      );
    }

    // COD increase -> requires confirmation
    assert.equal(
      a8.classifyAddressUpdatePreview("COD", {
        fee_delta_vnd: 15000,
        requires_fee_confirmation: true,
        blocked_prepaid_increase: false,
      }),
      "cod_increase"
    );
    // COD decrease -> adjusted immediately
    assert.equal(
      a8.classifyAddressUpdatePreview("COD", {
        fee_delta_vnd: -10000,
        requires_fee_confirmation: false,
        blocked_prepaid_increase: false,
      }),
      "cod_decrease"
    );
    // Prepaid VietQR/ZaloPay increase -> blocked (keep old or cancel/reorder, no surcharge)
    assert.equal(
      a8.classifyAddressUpdatePreview("VIETQR", {
        fee_delta_vnd: 12000,
        requires_fee_confirmation: false,
        blocked_prepaid_increase: true,
      }),
      "prepaid_increase_blocked"
    );
    assert.equal(
      a8.classifyAddressUpdatePreview("ZALOPAY", {
        fee_delta_vnd: 8000,
        requires_fee_confirmation: false,
        blocked_prepaid_increase: false,
      }),
      "prepaid_increase_blocked"
    );
    // Prepaid equal or decrease -> paid total unchanged
    assert.equal(
      a8.classifyAddressUpdatePreview("VIETQR", {
        fee_delta_vnd: -5000,
        requires_fee_confirmation: false,
        blocked_prepaid_increase: false,
      }),
      "prepaid_unchanged"
    );
    assert.equal(
      a8.classifyAddressUpdatePreview("ZALOPAY", {
        fee_delta_vnd: 0,
        requires_fee_confirmation: false,
        blocked_prepaid_increase: false,
      }),
      "prepaid_unchanged"
    );
  });

  test("3.2 translateAddressUpdateError, W3 stale-preview/in-progress & top-level quote extraction", async () => {
    const a8 = await import("../src/lib/a8-types.ts");
    assert.ok(
      a8.translateAddressUpdateError({ code: "ADDRESS_FEE_INCREASE_PREPAID" }).includes("hủy")
    );
    assert.ok(a8.translateAddressUpdateError({ code: "ADDRESS_FEE_CHANGED" }).includes("thay đổi"));
    assert.ok(
      a8
        .translateAddressUpdateError({ code: "ADDRESS_UPDATE_CARRIER_PICKED_UP" })
        .includes("lấy hàng")
    );
    assert.ok(
      a8
        .translateAddressUpdateError({ code: "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS" })
        .includes("đang xử lý")
    );
    assert.equal(
      a8.isAddressUpdateInProgressError({ code: "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS" }),
      true
    );
    assert.equal(a8.isAddressUpdateInProgressError({ code: "ADDRESS_FEE_CHANGED" }), false);
    assert.equal(a8.isAddressPreviewStale("addr-1", "addr-1"), false);
    assert.equal(a8.isAddressPreviewStale("addr-1", "addr-2"), true);
    assert.equal(a8.isAddressPreviewStale(null, "addr-1"), true);

    const basePreview = {
      old_shipping_fee_vnd: 25000,
      new_shipping_fee_vnd: 35000,
      fee_delta_vnd: 10000,
      old_total_payable_vnd: 225000,
      new_total_payable_vnd: 235000,
      preview_only: true,
      fee_adjusted: false,
      requires_fee_confirmation: true,
      blocked_prepaid_increase: false,
      shipment_recreated: false,
      shipment_queued: false,
      shipment_job_id: "",
    };
    const refreshed = a8.applyTopLevelAddressFeeQuote(
      {
        error: { code: "ADDRESS_FEE_CHANGED", message: "fee changed" },
        quote: {
          old_shipping_fee_vnd: 25000,
          new_shipping_fee_vnd: 42000,
          fee_delta_vnd: 17000,
          new_total_payable_vnd: 242000,
        },
      },
      basePreview,
      "COD"
    );
    assert.ok(refreshed, "must parse top-level quote from 409 ADDRESS_FEE_CHANGED");
    assert.equal(refreshed.new_shipping_fee_vnd, 42000);
    assert.equal(refreshed.fee_delta_vnd, 17000);
    assert.equal(refreshed.new_total_payable_vnd, 242000);
    assert.equal(refreshed.requires_fee_confirmation, true);
    assert.equal(
      a8.applyTopLevelAddressFeeQuote(
        { error: { code: "ADDRESS_FEE_CHANGED", details: { new_shipping_fee_vnd: 99999 } } },
        basePreview,
        "COD"
      ),
      null,
      "must only read top-level quote, never error.details"
    );

    // R1: extractSafeAddressFeeQuote strictly whitelists 4 safe integers and drops entire quote on bad/missing field
    assert.deepEqual(
      a8.extractSafeAddressFeeQuote({
        quote: {
          old_shipping_fee_vnd: 20000,
          new_shipping_fee_vnd: 35000,
          fee_delta_vnd: 15000,
          new_total_payable_vnd: 235000,
          extra_debug_field: "must-be-stripped",
        },
      }),
      {
        old_shipping_fee_vnd: 20000,
        new_shipping_fee_vnd: 35000,
        fee_delta_vnd: 15000,
        new_total_payable_vnd: 235000,
      }
    );
    assert.equal(
      a8.extractSafeAddressFeeQuote({
        quote: {
          old_shipping_fee_vnd: 20000,
          new_shipping_fee_vnd: 35000.5,
          fee_delta_vnd: 15000,
          new_total_payable_vnd: 235000,
        },
      }),
      null,
      "R1: float field must drop entire quote"
    );
    assert.equal(
      a8.extractSafeAddressFeeQuote({
        quote: {
          old_shipping_fee_vnd: 20000,
          new_shipping_fee_vnd: "35000",
          fee_delta_vnd: 15000,
          new_total_payable_vnd: 235000,
        },
      }),
      null,
      "R1: string field must drop entire quote"
    );
    assert.equal(
      a8.extractSafeAddressFeeQuote({
        quote: {
          old_shipping_fee_vnd: 20000,
          new_shipping_fee_vnd: 35000,
          fee_delta_vnd: 15000,
        },
      }),
      null,
      "R1: missing field must drop entire quote"
    );
    assert.equal(
      a8.extractSafeAddressFeeQuote({
        quote: {
          old_shipping_fee_vnd: Number.MAX_SAFE_INTEGER + 1,
          new_shipping_fee_vnd: 35000,
          fee_delta_vnd: 15000,
          new_total_payable_vnd: 235000,
        },
      }),
      null,
      "R1: unsafe integer must drop entire quote"
    );
    assert.equal(a8.extractSafeAddressFeeQuote({ quote: [1, 2, 3, 4] }), null);
    assert.equal(a8.extractSafeAddressFeeQuote(null), null);
  });

  test("3.3 OrderDetailClient.tsx + BFF route.ts + api.ts enforce R1–R4, W3 & saved-address-only", () => {
    const src = readSrc("features/account/OrderDetailClient.tsx");
    const typesSrc = readSrc("lib/a8-types.ts");
    const bffSrc = readSrc("app/api/gw/[...path]/route.ts");
    const apiSrc = readSrc("lib/api.ts");
    const cssSrc = readSrc("app/(shop)/don-hang/[id]/page.module.css");

    // R1: BFF forwards top-level safe quote on 409 ADDRESS_FEE_CHANGED; ApiError carries quote; OrderDetailClient uses api()
    assert.ok(
      bffSrc.includes('sanitized.status === 409 && sanitized.code === "ADDRESS_FEE_CHANGED"') &&
        bffSrc.includes("extractSafeAddressFeeQuote(parsed)"),
      "R1: BFF must forward safe top-level quote on 409 ADDRESS_FEE_CHANGED"
    );
    assert.ok(
      apiSrc.includes("readonly quote?: AddressFeeChangedQuote") &&
        apiSrc.includes("extractSafeAddressFeeQuote(data)"),
      "R1: ApiError must carry optional quote parsed on 409 ADDRESS_FEE_CHANGED"
    );
    assert.equal(
      src.includes("refreshSessionOnce"),
      false,
      "R1: OrderDetailClient must use api(), not raw fetch + manual refreshSessionOnce"
    );
    assert.equal(
      src.includes("FAILED_PRECONDITION"),
      false,
      "R1: must remove guesswork FAILED_PRECONDITION fallback preview retry"
    );

    assert.equal(
      src.includes("custom_address"),
      false,
      "must remove custom_address from OrderDetailClient"
    );
    assert.equal(
      typesSrc.includes("custom_address"),
      false,
      "must remove custom_address from a8-types"
    );
    assert.ok(src.includes("/tai-khoan?tab=dia-chi"), "must link to saved address book");
    assert.ok(src.includes("Sửa địa chỉ"), "must include Sửa địa chỉ trigger");
    assert.ok(src.includes("/address"), "must call PATCH /orders/{id}/address");
    assert.ok(src.includes("preview_only"), "must run step-1 preview_only");
    assert.ok(src.includes("accept_fee_increase"), "must send accept_fee_increase on COD increase");
    assert.ok(
      src.includes("expected_new_shipping_fee_vnd"),
      "must send expected_new_shipping_fee_vnd"
    );
    assert.ok(src.includes("ADDRESS_FEE_CHANGED"), "must handle 409 ADDRESS_FEE_CHANGED");
    assert.ok(src.includes("applyTopLevelAddressFeeQuote"), "must read top-level quote on 409");
    assert.ok(
      src.includes("isAddressPreviewStale"),
      "W3: must invalidate stale preview when address changes"
    );
    assert.ok(
      src.includes("isAddressUpdateInProgressError") && src.includes("addressInProgressLock"),
      "W3: must handle 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS"
    );
    // R2: addressInProgressLock must reset when reopening modal or refreshing order after closing
    assert.ok(
      src.includes("setAddressInProgressLock(false)") && src.includes("closeAddressModal"),
      "R2: must unlock addressInProgressLock on reopen/refresh after closing modal"
    );
    // R3: must not swallow saved addresses fetch errors into empty list
    assert.equal(
      src.includes(".catch(() => ({ addresses: [] }))"),
      false,
      "R3: handleOpenAddressModal must not swallow saved-addresses fetch error"
    );
    assert.ok(
      src.includes("addrListError") && src.includes("Thử lại"),
      "R3: address modal must display error + retry button when loading addresses fails"
    );
    // R4: address modal styles must live in CSS module using tokens, not hardcoded inline px
    assert.ok(
      src.includes("styles.addressDialog") && src.includes("styles.addressOption"),
      "R4: address modal must use CSS module classes"
    );
    assert.equal(
      src.includes('padding: "8px 10px"'),
      false,
      "R4: must not use hardcoded inline px padding in address modal"
    );
    assert.ok(
      cssSrc.includes(".addressDialog") && cssSrc.includes("--space-"),
      "R4: page.module.css must style address modal with design tokens"
    );
    assert.ok(src.includes("Giữ địa chỉ cũ"), "must include keep-old-address button");
    assert.ok(src.includes("Xác nhận trả thêm"), "must include confirm-extra-fee button");
  });
});

describe("Wave 3b-3 web-a8 — 4. S04 & UC-S15 /seller/don-hang (G4–G5, G8)", () => {
  test("4.1 buildSellerOrdersQuery, aggregateSellerOrderStatusCounts & batch helpers", async () => {
    const a8 = await import("../src/lib/a8-types.ts");

    const qs = a8.buildSellerOrdersQuery({
      page: 2,
      pageSize: 20,
      filter: "DONE",
      carrierCode: "ghn",
      paymentMethod: "COD",
      keyword: "NM-123",
      dueBeforeDate: "2026-11-30",
    });
    const params = new URLSearchParams(qs);
    assert.equal(params.get("page"), "2");
    assert.equal(params.get("page_size"), "20");
    assert.equal(params.get("statuses"), "DELIVERED,COMPLETED");
    assert.equal(params.get("carrier_code"), "ghn");
    assert.equal(params.get("payment_method"), "COD");
    assert.equal(params.get("keyword"), "NM-123");
    assert.equal(params.get("due_before"), "2026-11-30T23:59:59+07:00");
    assert.equal(params.get("include_status_counts"), "true");

    const agg = a8.aggregateSellerOrderStatusCounts([
      { status: "CONFIRMED", count: 7 },
      { status: "PREPARING", count: 4 },
      { status: "SHIPPING", count: 3 },
      { status: "DELIVERED", count: 10 },
      { status: "COMPLETED", count: 15 },
      { status: "CANCELLED", count: 2 },
      { status: "PENDING", count: 1 },
    ]);
    assert.equal(agg.all, 42);
    assert.equal(agg.CONFIRMED, 7);
    assert.equal(agg.PREPARING, 4);
    assert.equal(agg.SHIPPING, 3);
    assert.equal(agg.DONE, 25);
    assert.equal(agg.OTHER, 3);

    // Max 50 selection cap
    const fifty = Array.from({ length: 50 }, (_, i) => `ord-${i}`);
    const capped = a8.toggleBatchSelection(fifty, "ord-51", a8.MAX_BATCH_SHIP_ORDERS);
    assert.equal(capped.capped, true);
    assert.equal(capped.next.length, 50);

    // Tracking code dedupe
    const codes = a8.collectBatchTrackingCodes([
      { tracking_code: "TRK-1" },
      { tracking_code: " " },
      { tracking_code: "TRK-1" },
      { tracking_code: "TRK-2" },
    ]);
    assert.deepEqual(codes, ["TRK-1", "TRK-2"]);

    // R5: Out-of-range page auto-clamp when items empty while total > 0 and page > 1
    assert.equal(a8.resolveClampedPageOnEmpty(3, 0, 25, 20), 2);
    assert.equal(a8.resolveClampedPageOnEmpty(2, 0, 20, 20), 1);
    assert.equal(a8.resolveClampedPageOnEmpty(5, 0, 1, 20), 1);
    assert.equal(a8.resolveClampedPageOnEmpty(2, 5, 25, 20), null);
    assert.equal(a8.resolveClampedPageOnEmpty(1, 0, 25, 20), null);
    assert.equal(a8.resolveClampedPageOnEmpty(2, 0, 0, 20), null);

    // R6: Batch-ship reason translation covers real backend codes & never leaks raw English/code
    assert.ok(a8.translateBatchShipReasonCode("ORDER_NOT_FOUND").includes("Không tìm thấy"));
    assert.ok(a8.translateBatchShipReasonCode("FORBIDDEN").includes("gian hàng"));
    assert.ok(a8.translateBatchShipReasonCode("ORDER_CANCELLED").includes("hủy"));
    assert.equal(
      a8.translateBatchShipReasonCode("UNKNOWN_BACKEND_CODE"),
      "Chưa thể tạo vận đơn cho đơn này"
    );
    assert.equal(a8.translateBatchShipReasonCode(""), "Chưa thể tạo vận đơn cho đơn này");

    // R7: Off-page batch-ship polling selects up to maxPerTick round-robin & skips on-page/settled/failed/terminal
    const twentyResults = Array.from({ length: 20 }, (_, i) => ({
      order_id: `ord-${i + 1}`,
      outcome: "QUEUED",
      tracking_code: "",
    }));
    twentyResults.push({ order_id: "ord-skip", outcome: "SKIPPED", tracking_code: "" });
    const onPageMap = new Map([
      ["ord-1", { tracking_code: "", status: "PREPARING", shipment_job_status: "QUEUED" }],
    ]);
    const tracked = {
      "ord-2": { tracking_code: "TRK-2", status: "PREPARING", shipment_job_status: "SUCCEEDED" },
      "ord-3": { tracking_code: "", status: "PREPARING", shipment_job_status: "FAILED" },
      "ord-4": { tracking_code: "", status: "CANCELLED", shipment_job_status: "" },
    };
    const tick1 = a8.selectOffPageOrdersToPoll(twentyResults, onPageMap, tracked, 0, 5);
    assert.equal(tick1.pendingCount, 16);
    assert.deepEqual(tick1.ids, ["ord-5", "ord-6", "ord-7", "ord-8", "ord-9"]);
    assert.equal(tick1.nextCursor, 5);
    const tick2 = a8.selectOffPageOrdersToPoll(
      twentyResults,
      onPageMap,
      tracked,
      tick1.nextCursor,
      5
    );
    assert.deepEqual(tick2.ids, ["ord-10", "ord-11", "ord-12", "ord-13", "ord-14"]);
    const tick3 = a8.selectOffPageOrdersToPoll(
      twentyResults,
      onPageMap,
      tracked,
      tick2.nextCursor,
      5
    );
    assert.deepEqual(tick3.ids, ["ord-15", "ord-16", "ord-17", "ord-18", "ord-19"]);
    const tick4 = a8.selectOffPageOrdersToPoll(
      twentyResults,
      onPageMap,
      tracked,
      tick3.nextCursor,
      5
    );
    assert.deepEqual(tick4.ids, ["ord-20", "ord-5", "ord-6", "ord-7", "ord-8"]);
  });

  test("4.2 seller/don-hang/page.tsx enforces server pagination, R5 clamp, R6 clean VI labels, R7 off-page polling & W1 PDF", () => {
    const src = readSrc("app/(seller)/seller/don-hang/page.tsx");
    assert.equal(src.includes("page_size=100"), false, "must remove hardcoded page_size=100");
    assert.ok(src.includes("status_counts"), "must read real status_counts from API");
    assert.ok(src.includes("resolveClampedPageOnEmpty"), "R5: must auto-clamp out-of-range page");
    assert.ok(src.includes("seller/orders/batch-ship"), "must call batch-ship endpoint");
    assert.ok(src.includes("seller/orders/picking-list"), "must call picking-list endpoint");
    assert.ok(src.includes("shipping/labels/batch"), "must call batch PDF label endpoint");
    assert.ok(src.includes("application/pdf"), "must request/handle application/pdf");
    assert.equal(
      src.includes("window.open("),
      false,
      "W1: handleBatchLabelsPdf must download via <a download> only, never window.open"
    );
    // R6: no internal enum/English text leaked to seller UI
    assert.equal(src.includes("QUEUED ·"), false, "R6: must not leak 'QUEUED ·' label");
    assert.equal(src.includes("SKIPPED ·"), false, "R6: must not leak 'SKIPPED ·' label");
    assert.equal(src.includes("(QUEUED)"), false, "R6: must not leak '(QUEUED)' label");
    assert.equal(
      src.includes("item.message"),
      false,
      "R6: must not render raw backend item.message"
    );
    // R7: off-page polling via GET seller/orders/{id}
    assert.ok(
      src.includes("selectOffPageOrdersToPoll") &&
        src.includes("seller/orders/${encodeURIComponent("),
      "R7: must poll off-page QUEUED batch-ship orders via GET seller/orders/{id}"
    );
    assert.ok(src.includes("Tạo vận đơn hàng loạt"));
    assert.ok(src.includes("In danh sách nhặt hàng"));
    assert.ok(src.includes("In nhãn gộp PDF"));
  });
});

describe("Wave 3b-3 web-a8 — 5. S03 & UC-S13 /seller/san-pham (G6, G8)", () => {
  test("5.1 CSV file validation, import polling status & stock history query helpers", async () => {
    const a8 = await import("../src/lib/a8-types.ts");

    assert.equal(a8.validateImportCsvFile({ name: "items.csv", size: 1024 }).ok, true);
    assert.equal(a8.validateImportCsvFile({ name: "items.csv", size: 0 }).ok, false);
    assert.equal(
      a8.validateImportCsvFile({ name: "items.csv", size: 1024 * 1024 + 1 }).ok,
      false,
      "must reject > 1 MiB file"
    );
    assert.equal(
      a8.validateImportCsvFile({ name: "items.xlsx", size: 2048 }).ok,
      false,
      "must reject non-CSV file"
    );

    assert.equal(a8.isImportJobPollingStatus("QUEUED"), true);
    assert.equal(a8.isImportJobPollingStatus("VALIDATING"), true);
    assert.equal(a8.isImportJobPollingStatus("CONFIRMING"), true);
    assert.equal(a8.isImportJobPollingStatus("READY"), false);
    assert.equal(a8.isImportJobPollingStatus("COMPLETED"), false);
    assert.equal(a8.isImportJobPollingStatus("FAILED"), false);

    const hq = new URLSearchParams(
      a8.buildStockHistoryQuery({
        page: 2,
        pageSize: 20,
        changeType: "MANUAL_UPDATE",
        fromDate: "2026-11-01",
        toDate: "2026-11-25",
      })
    );
    assert.equal(hq.get("page"), "2");
    assert.equal(hq.get("page_size"), "20");
    assert.equal(hq.get("change_type"), "MANUAL_UPDATE");
    assert.equal(hq.get("from_time"), "2026-11-01T00:00:00+07:00");
    assert.equal(hq.get("to_time"), "2026-11-25T23:59:59+07:00");

    // W5: 409 IMPORT_JOB_IN_PROGRESS -> Vietnamese
    assert.ok(
      a8
        .translateProductImportError({ status: 409, code: "IMPORT_JOB_IN_PROGRESS" })
        .includes("Gian hàng đang có một tệp nhập đang xử lý")
    );

    // W7: all 18 CSV row error codes map to Vietnamese (error_message is last resort only)
    const all18Codes = [
      "INVALID_PRODUCT_NAME",
      "INVALID_PRODUCT_TEXT",
      "INVALID_SKU_CODE",
      "INVALID_SKU_TEXT",
      "DUPLICATE_SKU_IN_FILE",
      "DUPLICATE_SKU_IN_SHOP",
      "INVALID_CATEGORY",
      "CATEGORY_NOT_FOUND",
      "PROHIBITED_CATEGORY",
      "INVALID_PRICE",
      "INVALID_STOCK",
      "INVALID_WEIGHT",
      "INVALID_SLUG",
      "DUPLICATE_SLUG",
      "INVALID_ATTRIBUTES",
      "BANNED_CONTENT",
      "SHOP_SUSPENDED",
      "INTERNAL_ERROR",
    ];
    for (const code of all18Codes) {
      const vi = a8.translateProductImportRowError(code, "raw english backend error");
      assert.notEqual(vi, "raw english backend error", `W7: ${code} must map to Vietnamese`);
      assert.ok(vi.length >= 10, `W7: ${code} Vietnamese message must be descriptive`);
    }
    assert.equal(
      a8.translateProductImportRowError("UNKNOWN_CODE", "Fallback custom text"),
      "Fallback custom text"
    );
    assert.ok(
      a8
        .translateProductImportJobError("IMPORT_JOB_IN_PROGRESS: shop busy")
        .includes("Gian hàng đang có một tệp nhập đang xử lý")
    );
    assert.ok(
      a8.translateProductImportJobError("SHOP_SUSPENDED: shop suspended").includes("tạm ngưng")
    );

    // R6: Stock history backend technical English reasons translate cleanly to Vietnamese
    assert.equal(
      a8.formatStockHistoryReason("MANUAL_UPDATE", "seller product update"),
      "Cập nhật tồn kho từ trang chỉnh sửa sản phẩm"
    );
    assert.equal(
      a8.formatStockHistoryReason("RESERVE", "order stock reservation"),
      "Giữ tồn kho cho đơn hàng mới"
    );
    assert.equal(
      a8.formatStockHistoryReason("RELEASE", "order stock release"),
      "Hoàn tồn kho khi hủy đơn hàng"
    );
    assert.equal(
      a8.formatStockHistoryReason("CONFIRM", "order stock confirmation"),
      "Xuất kho sau khi xác nhận đơn hàng"
    );
    assert.equal(
      a8.formatStockHistoryReason("RESTOCK", "order item restock"),
      "Nhập lại kho từ đơn trả hàng"
    );
    assert.equal(
      a8.formatStockHistoryReason("MANUAL_UPDATE", "some internal english reason"),
      "Cập nhật tồn kho thủ công"
    );
    assert.equal(
      a8.formatStockHistoryReason("MANUAL_UPDATE", "Nhập thêm lô hàng mới"),
      "Nhập thêm lô hàng mới"
    );

    // R8: LOW_OOS filter sends low_stock=true and omits status
    const lowQs = new URLSearchParams(
      a8.buildSellerProductsQuery({ page: 1, filter: "LOW_OOS", keyword: "SKU-01" })
    );
    assert.equal(lowQs.get("low_stock"), "true");
    assert.equal(lowQs.get("status"), null);
    assert.equal(lowQs.get("keyword"), "SKU-01");
    const appQs = new URLSearchParams(a8.buildSellerProductsQuery({ page: 1, filter: "APPROVED" }));
    assert.equal(appQs.get("status"), "APPROVED");
    assert.equal(appQs.get("low_stock"), null);

    // S1: LOW_OOS tab count is fetched alongside status counts via low_stock=true&page=1&page_size=1;
    // rejected calls resolve to null so UI hides the number instead of showing a fake 0
    assert.ok(a8.SELLER_PRODUCT_TAB_FILTER_KEYS.includes("LOW_OOS"));
    const lowCountQs = new URLSearchParams(
      a8.buildSellerProductsQuery({ page: 1, pageSize: 1, filter: "LOW_OOS" })
    );
    assert.equal(lowCountQs.get("low_stock"), "true");
    assert.equal(lowCountQs.get("page"), "1");
    assert.equal(lowCountQs.get("page_size"), "1");
    assert.equal(lowCountQs.get("status"), null);

    const settledOk = a8.settleSellerProductTabCounts([
      { key: "all", result: { status: "fulfilled", value: 18 } },
      { key: "APPROVED", result: { status: "fulfilled", value: 12 } },
      { key: "PENDING", result: { status: "fulfilled", value: 2 } },
      { key: "REJECTED", result: { status: "fulfilled", value: 1 } },
      { key: "HIDDEN", result: { status: "fulfilled", value: 3 } },
      { key: "LOW_OOS", result: { status: "fulfilled", value: 4 } },
    ]);
    assert.equal(settledOk.LOW_OOS, 4);
    assert.equal(
      a8.formatSellerProductsHeaderSummary("Shop A", settledOk),
      "Shop A · 18 sản phẩm · 12 đang bán · 2 chờ duyệt · 4 hết / sắp hết"
    );

    const settledLowFailed = a8.settleSellerProductTabCounts([
      { key: "all", result: { status: "fulfilled", value: 18 } },
      { key: "APPROVED", result: { status: "fulfilled", value: 12 } },
      { key: "PENDING", result: { status: "fulfilled", value: 0 } },
      { key: "REJECTED", result: { status: "rejected", reason: new Error("timeout") } },
      { key: "HIDDEN", result: { status: "fulfilled", value: 3 } },
      { key: "LOW_OOS", result: { status: "rejected", reason: new Error("503") } },
    ]);
    assert.equal(settledLowFailed.LOW_OOS, null, "S1: failed LOW_OOS count must be null, not 0");
    assert.equal(settledLowFailed.REJECTED, null, "S1: failed tab count must be null, not 0");
    assert.equal(settledLowFailed.PENDING, 0, "S1: real zero count must still render as 0");
    assert.equal(
      a8.formatSellerProductsHeaderSummary("Shop A", settledLowFailed),
      "Shop A · 18 sản phẩm · 12 đang bán · 0 chờ duyệt"
    );

    // R9: IMPORT_INVALID_UTF8 translates to Vietnamese UTF-8 message
    const utf8Msg = a8.translateProductImportError({ status: 400, code: "IMPORT_INVALID_UTF8" });
    assert.ok(utf8Msg.includes("UTF-8") && utf8Msg.includes("ký tự lạ"));
    assert.ok(a8.translateProductImportJobError("IMPORT_INVALID_UTF8").includes("UTF-8"));

    // R10: Finished import jobs reset on modal reopen; READY/polling jobs are preserved
    assert.equal(a8.shouldResetImportJobOnModalOpen("COMPLETED"), true);
    assert.equal(a8.shouldResetImportJobOnModalOpen("FAILED"), true);
    assert.equal(a8.shouldResetImportJobOnModalOpen("READY"), false);
    assert.equal(a8.shouldResetImportJobOnModalOpen("QUEUED"), false);
    assert.equal(a8.shouldResetImportJobOnModalOpen("VALIDATING"), false);
    assert.equal(a8.shouldResetImportJobOnModalOpen("CONFIRMING"), false);
  });

  test("5.2 seller/san-pham/page.tsx enforces R5–R10, W2 seq guards, stock history & CSV import", () => {
    const src = readSrc("app/(seller)/seller/san-pham/page.tsx");
    assert.equal(src.includes("page_size=100"), false, "must remove hardcoded page_size=100");
    assert.ok(src.includes("productsLoadSeqRef"), "W2: loadProducts must guard response ordering");
    assert.ok(
      src.includes("historyLoadSeqRef"),
      "W2: loadStockHistory must guard response ordering"
    );
    assert.ok(src.includes("resolveClampedPageOnEmpty"), "R5: must auto-clamp out-of-range page");
    assert.ok(src.includes("stock-history"), "must wire stock-history endpoint");
    assert.ok(src.includes("formatStockHistoryReason"), "R6: must translate stock history reasons");
    assert.equal(src.includes("1 MiB"), false, "R6: must not display '1 MiB'");
    assert.equal(
      src.includes("Tải file lỗi (errors.csv)"),
      false,
      "R6: must not expose 'errors.csv' filename on button label"
    );
    assert.ok(
      src.includes("Tải tệp danh sách lỗi"),
      "R6: must use clean Vietnamese download label"
    );
    assert.ok(
      src.includes("Hết / Sắp hết") && src.includes('"LOW_OOS"'),
      "R8: must restore Hết / Sắp hết tab mapped to LOW_OOS (low_stock=true)"
    );
    assert.ok(
      src.includes("SELLER_PRODUCT_TAB_FILTER_KEYS") &&
        src.includes("settleSellerProductTabCounts") &&
        src.includes("formatSellerProductsHeaderSummary") &&
        src.includes("c.count !== null"),
      "S1: must load LOW_OOS count alongside other tabs via page_size=1 and hide failed counts"
    );
    assert.ok(
      src.includes("shouldResetImportJobOnModalOpen"),
      "R10: reopening CSV modal after COMPLETED/FAILED must reset to Step 1"
    );
    assert.ok(src.includes("Lịch sử tồn"), "must have Lịch sử tồn drawer trigger");
    assert.ok(src.includes("Nhập file CSV"), "must have Nhập file CSV modal trigger");
    assert.ok(src.includes("seller/products/import/template"), "must support template download");
    assert.ok(src.includes("errors.csv"), "must support errors.csv download endpoint");
    assert.ok(src.includes("/confirm"), "must support two-step confirm");
    assert.ok(src.includes("Xác nhận nhập dòng hợp lệ"));
    assert.ok(src.includes("translateProductImportError"), "W5/R9: must map import job errors");
    assert.ok(src.includes("translateProductImportRowError"), "W7: must map CSV row error_code");
    assert.ok(
      src.includes("translateProductImportJobError"),
      "W7: must map importJob.error_message"
    );
  });
});

describe("Wave 3b-3 web-a8 — 6. B15 remove 3 false-promise vouchers (G8)", () => {
  test("6.1 EDU15, SINHNHAT50, NEXUSVIP12 removed from VOUCHERS, membership, mock tabs, hoi-vien & seed", async () => {
    const { VOUCHERS } = await import("../src/data/vouchers.ts");
    const { MEMBERSHIP_TIERS, EDU_DISCOUNT_CONFIG } = await import("../src/data/membership.ts");
    const forbidden = ["EDU15", "SINHNHAT50", "NEXUSVIP12"];

    for (const code of forbidden) {
      assert.equal(
        VOUCHERS.some((v) => v.code.toUpperCase() === code),
        false,
        `VOUCHERS must not contain ${code}`
      );
      assert.equal(
        MEMBERSHIP_TIERS.some((t) => t.perks.some((p) => p.includes(code))),
        false,
        `MEMBERSHIP_TIERS must not mention ${code}`
      );
    }
    assert.equal(EDU_DISCOUNT_CONFIG.voucherCode.includes("EDU15"), false);
    assert.equal(EDU_DISCOUNT_CONFIG.note.includes("EDU15"), false);

    for (const relPath of [
      "features/account/AccountMockTabs.tsx",
      "mock/MockStoreContext.tsx",
      "app/(shop)/hoi-vien/page.tsx",
    ]) {
      const content = readSrc(relPath);
      for (const code of forbidden) {
        assert.equal(content.includes(code), false, `${relPath} must not contain ${code}`);
      }
    }

    const seedSrc = readFileSync(resolve(REPO_ROOT, "tools/seed/seed.mjs"), "utf8");
    assert.ok(
      seedSrc.includes("DELETE") && seedSrc.includes("/api/v1/admin/vouchers/"),
      "tools/seed/seed.mjs must clean up retired vouchers via DELETE /api/v1/admin/vouchers/{id}"
    );
  });
});

describe("Wave 3b-3 web-a8 — 7. §10 pricing UI in CheckoutSuggestionStrip & SizeFinder", () => {
  test("7.1 enrichProductsWithPromotions & resolveSizeFinderProductPricing handle Flash Sale > Shop Discount > regular", async () => {
    const a8 = await import("../src/lib/a8-types.ts");
    const searchClient = await import("../src/lib/search-client.ts");
    const baseProd = {
      id: "p1",
      min_price_vnd: 200000,
      max_price_vnd: 200000,
      skus: [{ id: "sku-1", price_vnd: 200000, is_active: true, available_stock: 10 }],
    };

    // Regular price
    const [reg] = searchClient.enrichProductsWithPromotions([baseProd]);
    assert.equal(reg.effective_price_vnd, 200000);
    assert.equal(reg.original_price_vnd, undefined);
    assert.equal(reg.promo_kind, "none");

    // Shop discount map
    const shopDiscMap = new Map([
      [
        "sku-1",
        {
          sku_id: "sku-1",
          original_price_vnd: 200000,
          discounted_price_vnd: 169000,
          discount_percent: 15,
        },
      ],
    ]);
    const [withShopDisc] = searchClient.enrichProductsWithPromotions(
      [baseProd],
      new Map(),
      shopDiscMap
    );
    assert.equal(withShopDisc.effective_price_vnd, 169000);
    assert.equal(withShopDisc.original_price_vnd, 200000);
    assert.equal(withShopDisc.promo_kind, "shop_discount");

    // Flash sale beats shop discount (single-price rule)
    const flashMap = new Map([
      [
        "sku-1",
        {
          sku_id: "sku-1",
          flash_price_vnd: 139000,
          original_price_vnd: 200000,
          remaining_slots: 5,
        },
      ],
    ]);
    const [withFlash] = searchClient.enrichProductsWithPromotions(
      [baseProd],
      flashMap,
      shopDiscMap
    );
    assert.equal(withFlash.effective_price_vnd, 139000);
    assert.equal(withFlash.original_price_vnd, 200000);
    assert.equal(withFlash.promo_kind, "flash");

    // SizeFinder display pricing helper
    const sfDiscounted = a8.resolveSizeFinderProductPricing({
      price: 159000,
      originalPrice: 200000,
    });
    assert.equal(sfDiscounted.displayPriceVnd, 159000);
    assert.equal(sfDiscounted.originalPriceVnd, 200000);
    const sfRegular = a8.resolveSizeFinderProductPricing({ price: 200000 });
    assert.equal(sfRegular.displayPriceVnd, 200000);
    assert.equal(sfRegular.originalPriceVnd, undefined);
  });

  test("7.2 SizeFinder.tsx renders original strikethrough price via Price component (W6: CheckoutSuggestionStrip owned by web-b1)", () => {
    const sizeSrc = readSrc("features/catalog/SizeFinder.tsx");
    assert.ok(sizeSrc.includes("Price"), "SizeFinder must use Price component");
    assert.ok(
      sizeSrc.includes("originalAmount"),
      "SizeFinder must pass originalAmount for strikethrough price"
    );
  });
});

describe("Wave 3b-3 Round 6 — W1–W4 real-browser QA fixes", () => {
  function createFakeRevealEnv({ prefersReduced = false, withIntersectionObserver = true } = {}) {
    const makeEl = (top, { reveal = true, children = [] } = {}) => {
      const classes = new Set();
      const attrs = new Map();
      if (reveal) attrs.set("data-reveal", "");
      const el = {
        nodeType: 1,
        children,
        classList: {
          add(cls) {
            classes.add(cls);
          },
          remove(cls) {
            classes.delete(cls);
          },
          contains(cls) {
            return classes.has(cls);
          },
        },
        hasAttribute(name) {
          return attrs.has(name);
        },
        getBoundingClientRect() {
          return { top };
        },
        querySelectorAll(selector) {
          if (selector !== "[data-reveal]") return [];
          const found = [];
          const walk = (nodes) => {
            for (const n of nodes) {
              if (n.hasAttribute("data-reveal")) found.push(n);
              walk(n.children);
            }
          };
          walk(children);
          return found;
        },
      };
      return el;
    };

    const initialNodes = [];
    const ioObserved = new Set();
    let ioCallback = null;
    let ioDisconnected = false;
    let moCallback = null;
    let moOptions = null;
    let moDisconnected = false;

    class FakeIntersectionObserver {
      constructor(cb) {
        ioCallback = cb;
      }
      observe(el) {
        ioObserved.add(el);
      }
      unobserve(el) {
        ioObserved.delete(el);
      }
      disconnect() {
        ioDisconnected = true;
        ioObserved.clear();
      }
    }

    class FakeMutationObserver {
      constructor(cb) {
        moCallback = cb;
      }
      observe(_target, opts) {
        moOptions = opts;
      }
      disconnect() {
        moDisconnected = true;
      }
    }

    const fakeDoc = {
      body: { nodeType: 1 },
      documentElement: { nodeType: 1 },
      querySelectorAll(selector) {
        if (selector !== "[data-reveal]") return [];
        return initialNodes.filter((n) => n.hasAttribute("data-reveal"));
      },
    };

    const fakeWin = {
      innerHeight: 900,
      matchMedia() {
        return { matches: prefersReduced };
      },
      IntersectionObserver: withIntersectionObserver ? FakeIntersectionObserver : undefined,
      MutationObserver: FakeMutationObserver,
    };

    return {
      makeEl,
      initialNodes,
      fakeDoc,
      fakeWin,
      ioObserved,
      emitMutations(mutations) {
        moCallback?.(mutations);
      },
      emitIntersect(el) {
        ioCallback?.([{ target: el, isIntersecting: true }], {
          unobserve(target) {
            ioObserved.delete(target);
          },
        });
      },
      getState() {
        return { ioDisconnected, moDisconnected, moOptions };
      },
    };
  }

  test("W1.1 observeRevealElements reveals dynamic [data-reveal] nodes via MutationObserver, heals React class resets & disconnects cleanly", async () => {
    const { observeRevealElements } = await import("../src/features/commerce/useRevealOnScroll.ts");
    const env = createFakeRevealEnv();
    const aboveFold = env.makeEl(200);
    const belowFold = env.makeEl(1400);
    env.initialNodes.push(aboveFold, belowFold);

    const cleanup = observeRevealElements(env.fakeDoc, env.fakeWin);

    assert.equal(aboveFold.classList.contains("is-in"), true, "above-fold node reveals on mount");
    assert.equal(belowFold.classList.contains("is-in"), false, "below-fold node waits for IO");
    assert.equal(env.ioObserved.has(belowFold), true, "below-fold node is observed by IO");
    assert.equal(env.getState().moOptions?.childList, true);
    assert.equal(env.getState().moOptions?.subtree, true);

    // Simulate dynamically added timeline <li> inside a wrapper after address update (no depKey change!)
    const dynamicAbove = env.makeEl(320);
    const dynamicBelow = env.makeEl(1600);
    const wrapper = env.makeEl(0, { reveal: false, children: [dynamicAbove, dynamicBelow] });
    env.emitMutations([{ type: "childList", addedNodes: [wrapper] }]);

    assert.equal(
      dynamicAbove.classList.contains("is-in"),
      true,
      "dynamically mounted above-fold [data-reveal] must reveal immediately"
    );
    assert.equal(
      dynamicBelow.classList.contains("is-in"),
      false,
      "dynamically mounted below-fold [data-reveal] preserves scroll reveal"
    );
    assert.equal(
      env.ioObserved.has(dynamicBelow),
      true,
      "dynamically mounted below-fold [data-reveal] must be observed by IO"
    );

    // Intersection reveals dynamic below-fold node
    env.emitIntersect(dynamicBelow);
    assert.equal(dynamicBelow.classList.contains("is-in"), true);
    assert.equal(env.ioObserved.has(dynamicBelow), false);

    // Simulate React overwriting className on already-revealed node (e.g. idx === 0 -> idx === 1)
    aboveFold.classList.remove("is-in");
    env.emitMutations([{ type: "attributes", attributeName: "class", target: aboveFold }]);
    assert.equal(
      aboveFold.classList.contains("is-in"),
      true,
      "already-revealed node must restore .is-in if React overwrites className"
    );

    // Unrevealed below-fold node class mutation must NOT prematurely reveal
    env.emitMutations([{ type: "attributes", attributeName: "class", target: belowFold }]);
    assert.equal(belowFold.classList.contains("is-in"), false);

    cleanup();
    assert.equal(env.getState().ioDisconnected, true);
    assert.equal(env.getState().moDisconnected, true);
  });

  test("W1.2 observeRevealElements honors prefers-reduced-motion and missing IntersectionObserver for initial & dynamic nodes", async () => {
    const { observeRevealElements } = await import("../src/features/commerce/useRevealOnScroll.ts");

    for (const opts of [
      { prefersReduced: true, withIntersectionObserver: true },
      { prefersReduced: false, withIntersectionObserver: false },
    ]) {
      const env = createFakeRevealEnv(opts);
      const initialBelow = env.makeEl(1800);
      env.initialNodes.push(initialBelow);
      const cleanup = observeRevealElements(env.fakeDoc, env.fakeWin);

      assert.equal(initialBelow.classList.contains("is-in"), true);
      const lateNode = env.makeEl(2200);
      env.emitMutations([{ type: "childList", addedNodes: [lateNode] }]);
      assert.equal(lateNode.classList.contains("is-in"), true);

      cleanup();
      assert.equal(env.getState().moDisconnected, true);
    }
  });

  test("W2.1 localizeTimelineNote & getTimelineEventTitle handle all 4 address-update templates + shipment recreation notes", async () => {
    const orderMeta = await import("../src/features/account/orderMeta.ts");

    // 4 templates from services/order/internal/store/store.go:381-392
    assert.equal(
      orderMeta.localizeTimelineNote(
        "Buyer updated shipping address (Phường Bến Nghé, TP. Hồ Chí Minh); shipping fee adjusted from 24000 to 40000 VND"
      ),
      "Người mua đã cập nhật địa chỉ nhận hàng (Phường Bến Nghé, TP. Hồ Chí Minh); phí vận chuyển điều chỉnh từ 24.000₫ sang 40.000₫."
    );
    assert.equal(
      orderMeta.localizeTimelineNote(
        "Buyer updated shipping address; shipping fee adjusted from 24000 to 40000 VND"
      ),
      "Người mua đã cập nhật địa chỉ nhận hàng; phí vận chuyển điều chỉnh từ 24.000₫ sang 40.000₫."
    );
    assert.equal(
      orderMeta.localizeTimelineNote(
        "Buyer updated shipping address (Phường Bến Nghé, TP. Hồ Chí Minh)"
      ),
      "Người mua đã cập nhật địa chỉ nhận hàng (Phường Bến Nghé, TP. Hồ Chí Minh)."
    );
    assert.equal(
      orderMeta.localizeTimelineNote("Buyer updated shipping address"),
      "Người mua đã cập nhật địa chỉ nhận hàng."
    );

    // Bonus: address-update shipment recreation / restoration notes (store.go:357-370)
    assert.equal(
      orderMeta.localizeTimelineNote(
        "Shipment recreation done after address update via ghn (tracking: GHN-123)"
      ),
      "Đã tạo lại vận đơn sau khi cập nhật địa chỉ qua Giao Hàng Nhanh (GHN) (mã vận đơn: GHN-123)."
    );
    assert.equal(
      orderMeta.localizeTimelineNote(
        "Shipment recreation queued for async retry after address update"
      ),
      "Đang xếp hàng tạo lại vận đơn sau khi cập nhật địa chỉ."
    );
    assert.equal(
      orderMeta.localizeTimelineNote(
        "Shipment recreation failed after address update; seller can recreate shipment"
      ),
      "Tạo lại vận đơn sau khi cập nhật địa chỉ thất bại; gian hàng có thể tạo lại vận đơn."
    );

    // getTimelineEventTitle returns "Đã cập nhật địa chỉ nhận hàng" even when from_status == to_status
    for (const status of ["CONFIRMED", "PREPARING"]) {
      assert.equal(
        orderMeta.getTimelineEventTitle({
          from_status: status,
          to_status: status,
          note: "Buyer updated shipping address (Phường 1, TP. Hồ Chí Minh); shipping fee adjusted from 24000 to 40000 VND",
        }),
        "Đã cập nhật địa chỉ nhận hàng"
      );
      assert.equal(
        orderMeta.getTimelineEventTitle({
          from_status: status,
          to_status: status,
          note: "Buyer updated shipping address",
        }),
        "Đã cập nhật địa chỉ nhận hàng"
      );
    }

    // Normal events retain standard titles
    assert.equal(
      orderMeta.getTimelineEventTitle({
        from_status: "",
        to_status: "CONFIRMED",
        note: "Order created and confirmed (COD)",
      }),
      "Đặt hàng thành công · Đã xác nhận"
    );
    assert.equal(
      orderMeta.getTimelineEventTitle({
        from_status: "CONFIRMED",
        to_status: "PREPARING",
        note: "Seller confirmed order",
      }),
      "Gian hàng đang chuẩn bị đơn hàng"
    );
  });

  test("W2.2 buyer, seller, admin timelines wire localized address-update title/note; guest lookup omits timeline notes", () => {
    const buyerSrc = readSrc("features/account/OrderDetailClient.tsx");
    const sellerDetailSrc = readSrc("app/(seller)/seller/don-hang/[id]/page.tsx");
    const adminDisputeSrc = readSrc("app/(admin)/admin/khieu-nai/page.tsx");
    const guestLookupSrc = readSrc("features/commerce/OrderLookupView.tsx");

    assert.ok(
      buyerSrc.includes("getTimelineEventTitle(ev)") &&
        buyerSrc.includes("localizeTimelineNote(ev.note)"),
      "buyer OrderDetailClient must use getTimelineEventTitle + localizeTimelineNote"
    );
    assert.ok(
      sellerDetailSrc.includes("isAddressUpdatedTimelineNote(ev.note)") &&
        sellerDetailSrc.includes("getTimelineEventTitle(ev)") &&
        sellerDetailSrc.includes("localizeTimelineNote(ev.note)"),
      "seller order detail must use getTimelineEventTitle + localizeTimelineNote for address updates"
    );
    assert.ok(
      adminDisputeSrc.includes("isAddressUpdatedTimelineNote(ev.note)") &&
        adminDisputeSrc.includes("getTimelineEventTitle(ev)") &&
        adminDisputeSrc.includes("localizeTimelineNote(ev.note)"),
      "admin timeline must use getTimelineEventTitle + localizeTimelineNote"
    );
    assert.equal(
      guestLookupSrc.includes("ev.note"),
      false,
      "guest OrderLookupView must never display private timeline notes"
    );
  });

  test("W3–W6: seller/don-hang recipient >= 150px, sticky actionCol + compact <=1366, wrapped tracking meta & CSV 'Dòng dữ liệu'", () => {
    const donHangCss = readSrc("app/(seller)/seller/don-hang/page.module.css");
    const donHangTsx = readSrc("app/(seller)/seller/don-hang/page.tsx");
    const sanPhamTsx = readSrc("app/(seller)/seller/san-pham/page.tsx");

    assert.ok(
      donHangCss.includes(".recipientCol") &&
        /\.recipientCol[\s\S]*?min-width:\s*1[5-9]\dpx/.test(donHangCss),
      "W3: seller/don-hang .recipientCol must set min-width >= 150px"
    );
    assert.ok(
      /\.actionCol[\s\S]*?position:\s*sticky;[\s\S]*?right:\s*0;[\s\S]*?background:\s*var\(--card\);[\s\S]*?border-left:\s*1px\s+solid\s+var\(--border\)/.test(
        donHangCss
      ),
      "W5: .actionCol must be sticky right:0 with --card background and 1px left border"
    );
    assert.ok(
      donHangCss.includes("@media (max-width: 1366px)") &&
        donHangCss.includes(".qtyCol") &&
        donHangCss.includes(".subtotalCol") &&
        donHangCss.includes(".compactQtyBadge"),
      "W5: <=1366px must hide SL/Tiền hàng columns and reveal inline ×n badge"
    );
    assert.ok(
      /\.itemMetaSub[\s\S]*?-webkit-line-clamp:\s*[23];[\s\S]*?white-space:\s*normal/.test(
        donHangCss
      ),
      "W6: .itemMetaSub must wrap up to 2–3 lines instead of single-line ellipsis"
    );
    assert.ok(
      /\.itemSummaryCell[\s\S]*?text-overflow:\s*ellipsis;[\s\S]*?white-space:\s*nowrap/.test(
        donHangCss
      ),
      "W6: .itemSummaryCell must remain single-line truncated"
    );
    assert.ok(
      donHangTsx.includes("styles.ordersTable") &&
        donHangTsx.includes("styles.recipientCol") &&
        donHangTsx.includes("styles.productCol") &&
        donHangTsx.includes("styles.actionCol") &&
        donHangTsx.includes("styles.qtyCol") &&
        donHangTsx.includes("styles.subtotalCol") &&
        donHangTsx.includes("styles.compactQtyBadge") &&
        donHangTsx.includes("styles.itemMetaSub"),
      "W3/W5/W6: seller/don-hang table must apply sticky action, responsive column, and wrapped meta classes"
    );
    assert.ok(
      sanPhamTsx.includes("<th>Dòng dữ liệu</th>"),
      "W4: CSV error table must label row_number column as 'Dòng dữ liệu'"
    );
    assert.equal(
      sanPhamTsx.includes("<th>Dòng</th>"),
      false,
      "W4: CSV error table must not use ambiguous 'Dòng' header"
    );
  });
});
