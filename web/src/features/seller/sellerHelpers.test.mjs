// Run: cd web && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/seller/sellerHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  canSubmitShopApplication,
  getShopStatusInfo,
  isShopInfoRequested,
} from "../account/orderMeta.ts";
import {
  isSafeEvidenceUrl,
  isSellerRouteAllowedWhenSuspended,
  isValidDateParam,
  isValidResourceId,
  isValidTrackingCode,
  resolveLegacyInteractionTab,
  validateEvidenceUrls,
  validateResourceIdList,
  validateShopRegisterInput,
  validateTrackingCodeList,
} from "./sellerValidation.ts";
import {
  computeSellerNetReceivable,
  computeSellerOrderDeductions,
  extractSellerUnreadChatCount,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  normalizeSellerShipmentResponse,
  resolveSellerShipmentFeeVnd,
} from "./sellerApiTypes.ts";
import { canSellerConfirmOrder } from "./types.ts";

const __dirname = dirname(fileURLToPath(import.meta.url));

test("isValidResourceId and isValidTrackingCode reject traversal, spaces, and empty values", () => {
  assert.equal(isValidResourceId("550e8400-e29b-41d4-a716-446655440000"), true);
  assert.equal(isValidResourceId("ord_123-abc"), true);
  assert.equal(isValidResourceId(""), false);
  assert.equal(isValidResourceId("   "), false);
  assert.equal(isValidResourceId("../admin"), false);
  assert.equal(isValidResourceId("id/with/slash"), false);
  assert.equal(isValidResourceId("id with space"), false);
  assert.equal(isValidResourceId("a".repeat(129)), false);
  assert.equal(isValidResourceId(12345), false);

  assert.equal(isValidTrackingCode("GHN-20261125-001"), true);
  assert.equal(isValidTrackingCode("A"), false);
  assert.equal(isValidTrackingCode("../etc/passwd"), false);
  assert.equal(isValidTrackingCode("code?foo=1"), false);
  assert.equal(isValidTrackingCode("X".repeat(65)), false);
});

test("isValidDateParam validates real calendar dates and rejects impossible dates", () => {
  assert.equal(isValidDateParam("2026-11-25"), true);
  assert.equal(isValidDateParam("2024-02-29"), true);
  assert.equal(isValidDateParam("2025-02-29"), false);
  assert.equal(isValidDateParam("2026-02-30"), false);
  assert.equal(isValidDateParam("2026-13-01"), false);
  assert.equal(isValidDateParam("25-11-2026"), false);
  assert.equal(isValidDateParam(""), false);
  assert.equal(isValidDateParam(null), false);
});

test("isSafeEvidenceUrl and validateEvidenceUrls enforce protocol, length, and count limits", () => {
  assert.equal(isSafeEvidenceUrl("https://cdn.nexusmart.vn/evidence/1.jpg"), true);
  assert.equal(isSafeEvidenceUrl("http://localhost:9000/img.png"), true);
  assert.equal(isSafeEvidenceUrl("/products/ao-thun.png"), true);
  assert.equal(isSafeEvidenceUrl("/products/../secret"), false);
  assert.equal(isSafeEvidenceUrl("javascript:alert(1)"), false);
  assert.equal(isSafeEvidenceUrl("data:image/svg+xml,<svg/>"), false);
  assert.equal(isSafeEvidenceUrl("https://example.com/" + "a".repeat(500)), false);
  assert.equal(isSafeEvidenceUrl("https://example.com/a\nb"), false);

  assert.deepEqual(validateEvidenceUrls(undefined), { ok: true, urls: [] });
  assert.equal(validateEvidenceUrls("not-array").ok, false);
  assert.equal(validateEvidenceUrls([123]).ok, false);
  assert.equal(validateEvidenceUrls(["javascript:alert(1)"]).ok, false);

  const tooMany = validateEvidenceUrls(
    Array.from({ length: 6 }, (_, i) => `https://cdn.nexusmart.vn/${i}.jpg`),
    5
  );
  assert.equal(tooMany.ok, false);

  const valid = validateEvidenceUrls(["  https://cdn.nexusmart.vn/a.jpg ", "", "/products/x.png"]);
  assert.deepEqual(valid, {
    ok: true,
    urls: ["https://cdn.nexusmart.vn/a.jpg", "/products/x.png"],
  });
});

test("validateResourceIdList and validateTrackingCodeList dedupe and enforce max 50 cap", () => {
  assert.equal(validateResourceIdList("bad").ok, false);
  assert.equal(validateResourceIdList([]).ok, false);
  assert.equal(validateResourceIdList(["ok-1", "../bad"]).ok, false);
  assert.deepEqual(validateResourceIdList(["ord-1", "ord-2", "ord-1"]), {
    ok: true,
    ids: ["ord-1", "ord-2"],
  });
  const over50 = validateResourceIdList(
    Array.from({ length: 51 }, (_, i) => `ord-${i + 1}`),
    50
  );
  assert.deepEqual(over50, { ok: false, code: "TOO_MANY_IDS" });

  assert.equal(validateTrackingCodeList([]).ok, false);
  assert.equal(validateTrackingCodeList(["GHN-1", "bad/code"]).ok, false);
  assert.deepEqual(validateTrackingCodeList(["GHN-1", "GHN-1", "GHN-2"]), {
    ok: true,
    codes: ["GHN-1", "GHN-2"],
  });
  const over50Codes = validateTrackingCodeList(
    Array.from({ length: 51 }, (_, i) => `GHN-${1000 + i}`),
    50
  );
  assert.deepEqual(over50Codes, { ok: false, code: "TOO_MANY_TRACKING_CODES" });
});

test("validateShopRegisterInput whitelists fields and strips client shop_id/user_id", () => {
  const valid = validateShopRegisterInput({
    name: "  Xưởng Dệt Bạch Đằng  ",
    slug: "xuong-det-bach-dang",
    description: "Chuyên áo thun cotton",
    pickup_address_id: "addr-123",
    contact_phone: "0901 234 567",
    business_license_doc: "GPKD-010203",
    shop_id: "injected-shop",
    user_id: "injected-user",
    role: "super_admin",
  });
  assert.equal(valid.ok, true);
  if (valid.ok) {
    assert.deepEqual(valid.payload, {
      name: "Xưởng Dệt Bạch Đằng",
      slug: "xuong-det-bach-dang",
      description: "Chuyên áo thun cotton",
      pickup_address_id: "addr-123",
      contact_phone: "0901234567",
      business_license_doc: "GPKD-010203",
    });
    assert.equal("shop_id" in valid.payload, false);
    assert.equal("user_id" in valid.payload, false);
  }

  assert.equal(validateShopRegisterInput(null).ok, false);
  assert.equal(
    validateShopRegisterInput({
      name: "A",
      pickup_address_id: "addr-1",
      contact_phone: "0901234567",
    }).ok,
    false
  );
  assert.equal(
    validateShopRegisterInput({
      name: "Shop Hợp Lệ",
      slug: "bad slug!",
      pickup_address_id: "addr-1",
      contact_phone: "0901234567",
    }).ok,
    false
  );
  assert.equal(
    validateShopRegisterInput({
      name: "Shop Hợp Lệ",
      pickup_address_id: "../bad",
      contact_phone: "0901234567",
    }).ok,
    false
  );
  assert.equal(
    validateShopRegisterInput({
      name: "Shop Hợp Lệ",
      pickup_address_id: "addr-1",
      contact_phone: "12345",
      business_license_doc: "GPKD-010203",
    }).ok,
    false
  );

  // business_license_doc is mandatory to match services/user RegisterShop
  for (const emptyLicense of [undefined, "", "   "]) {
    const missingLic = validateShopRegisterInput({
      name: "Shop Hợp Lệ",
      pickup_address_id: "addr-1",
      contact_phone: "0901234567",
      business_license_doc: emptyLicense,
    });
    assert.equal(missingLic.ok, false);
    if (!missingLic.ok) {
      assert.equal(missingLic.code, "INVALID_BUSINESS_LICENSE");
      assert.ok(missingLic.message.includes("giấy phép kinh doanh"));
    }
  }
  assert.equal(
    validateShopRegisterInput({
      name: "Shop Hợp Lệ",
      pickup_address_id: "addr-1",
      contact_phone: "0901234567",
      business_license_doc: "x".repeat(501),
    }).ok,
    false
  );
});

test("SellerShopOnboarding enforces mandatory business_license_doc and handles REQUEST_INFO / REJECTED resubmission", () => {
  // Helper behavior across all shop states
  assert.equal(canSubmitShopApplication(null), true);
  assert.equal(
    isShopInfoRequested({ status: "PENDING", rejection_reason: "Cần ảnh GPKD rõ nét" }),
    true
  );
  assert.equal(
    canSubmitShopApplication({ status: "PENDING", rejection_reason: "Cần ảnh GPKD rõ nét" }),
    true
  );
  assert.equal(
    getShopStatusInfo("PENDING", "Cần ảnh GPKD rõ nét").label,
    "Yêu cầu bổ sung"
  );
  assert.equal(isShopInfoRequested({ status: "PENDING", rejection_reason: "" }), false);
  assert.equal(canSubmitShopApplication({ status: "PENDING", rejection_reason: "" }), false);
  assert.equal(isShopInfoRequested({ status: "REJECTED", rejection_reason: "Sai thông tin" }), false);
  assert.equal(canSubmitShopApplication({ status: "REJECTED", rejection_reason: "Sai thông tin" }), true);
  assert.equal(canSubmitShopApplication({ status: "REJECTED", rejection_reason: "" }), true);
  assert.equal(canSubmitShopApplication({ status: "ACTIVE", rejection_reason: "" }), false);
  assert.equal(canSubmitShopApplication({ status: "SUSPENDED", rejection_reason: "Vi phạm" }), false);

  // Component wiring assertions
  const src = readFileSync(resolve(__dirname, "SellerShopOnboarding.tsx"), "utf8");
  assert.equal(
    src.includes("Giấy phép kinh doanh (tùy chọn)"),
    false,
    "SellerShopOnboarding must not label business_license_doc as optional"
  );
  const licenseStart = src.indexOf('id="shop-reg-license"');
  assert.ok(licenseStart >= 0, "shop-reg-license input must exist");
  const licenseEnd = src.indexOf("/>", licenseStart);
  assert.ok(
    licenseEnd > licenseStart && /\brequired\b/.test(src.slice(licenseStart, licenseEnd)),
    "shop-reg-license input must have required attribute"
  );
  assert.ok(
    src.includes("validateShopRegisterInput") &&
      src.includes("isShopInfoRequested") &&
      src.includes("canSubmitShopApplication"),
    "SellerShopOnboarding must reuse validateShopRegisterInput, isShopInfoRequested, and canSubmitShopApplication"
  );
});

test("resolveLegacyInteractionTab redirects legacy return/complaint/waiting tabs cleanly", () => {
  assert.deepEqual(resolveLegacyInteractionTab("returns"), {
    kind: "redirect",
    href: "/seller/tra-hang",
  });
  assert.deepEqual(
    resolveLegacyInteractionTab("complaints", { selected: "ret-123", status: "DISPUTED" }),
    {
      kind: "redirect",
      href: "/seller/tra-hang?selected=ret-123&status=DISPUTED",
    }
  );
  // Rejects invalid selected/status query values when redirecting
  assert.deepEqual(
    resolveLegacyInteractionTab("khieu-nai", { selected: "../evil", status: "bad status!" }),
    {
      kind: "redirect",
      href: "/seller/tra-hang",
    }
  );
  assert.deepEqual(resolveLegacyInteractionTab("waiting"), {
    kind: "redirect",
    href: "/seller/san-pham?filter=LOW_OOS",
  });
  assert.deepEqual(resolveLegacyInteractionTab("notifications"), {
    kind: "tab",
    tab: "notifications",
  });
  assert.deepEqual(resolveLegacyInteractionTab("reviews"), {
    kind: "tab",
    tab: "reviews",
  });
  assert.deepEqual(resolveLegacyInteractionTab(undefined), {
    kind: "tab",
    tab: "reviews",
  });
});

test("isSellerRouteAllowedWhenSuspended allows only overview, orders, returns, and finance", () => {
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller"), true);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/"), true);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/don-hang"), true);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/don-hang/ord-123"), true);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/tra-hang"), true);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/tai-chinh"), true);

  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/san-pham"), false);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/khuyen-mai"), false);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/ho-so"), false);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/site"), false);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/chat"), false);
  assert.equal(isSellerRouteAllowedWhenSuspended("/seller/tuong-tac"), false);
});

test("computeSellerNetReceivable deducts all 6 seller costs and prefers server field when present", () => {
  const order = {
    subtotal_vnd: 500_000,
    flash_sale_discount_vnd: 20_000,
    shop_program_discount_vnd: 15_000,
    shop_voucher_discount_vnd: 30_000,
    shop_freeship_discount_vnd: 10_000,
    commission_vnd: 25_000,
    payment_fee_vnd: 12_500,
  };
  assert.equal(computeSellerOrderDeductions(order), 112_500);
  assert.equal(computeSellerNetReceivable(order), 387_500);

  // Server-provided seller_net_receivable_vnd overrides client arithmetic
  assert.equal(
    computeSellerNetReceivable({ ...order, seller_net_receivable_vnd: 390_000 }),
    390_000
  );

  // Floors at 0 VND if deductions exceed subtotal
  assert.equal(computeSellerNetReceivable({ ...order, subtotal_vnd: 50_000 }), 0);
});

test("getSellerOrderStatusMeta, getNextBackendOrderAction, and formatPaymentStatusVi align with real backend order lifecycle", () => {
  // Real backend keeps CONFIRMED when async shipment creation queues/fails (lifecycle.go:852-891)
  const confirmedQueued = getSellerOrderStatusMeta("CONFIRMED", "QUEUED", "");
  assert.equal(confirmedQueued.tone, "warn");
  assert.equal(confirmedQueued.label, "Đang chờ tạo vận đơn");
  assert.equal(getNextBackendOrderAction("CONFIRMED", "", "QUEUED")?.kind, "ship");
  assert.equal(getNextBackendOrderAction("CONFIRMED", "", "QUEUED")?.label, "Thử tạo lại ngay");
  assert.equal(canSellerConfirmOrder("CONFIRMED", "QUEUED"), false);

  const confirmedFailed = getSellerOrderStatusMeta("CONFIRMED", "FAILED", "");
  assert.equal(confirmedFailed.tone, "neg");
  assert.equal(confirmedFailed.label, "Tạo vận đơn thất bại");
  assert.equal(getNextBackendOrderAction("CONFIRMED", "", "FAILED")?.kind, "ship");
  assert.equal(getNextBackendOrderAction("CONFIRMED", "", "FAILED")?.label, "Thử tạo lại ngay");
  assert.equal(canSellerConfirmOrder("CONFIRMED", "FAILED"), false);

  // Normal CONFIRMED uses shared GATEWAY_ORDER_STATUS_LABELS.CONFIRMED
  const confirmedPlain = getSellerOrderStatusMeta("CONFIRMED", "", "");
  assert.equal(confirmedPlain.label, "Đã xác nhận");
  assert.equal(canSellerConfirmOrder("CONFIRMED", ""), true);
  assert.equal(getNextBackendOrderAction("CONFIRMED", "", "")?.kind, "confirm");

  // PREPARING async job states
  const queued = getSellerOrderStatusMeta("PREPARING", "QUEUED", "");
  assert.equal(queued.tone, "warn");
  assert.equal(queued.label, "Đang chờ tạo vận đơn");

  const failed = getSellerOrderStatusMeta("PREPARING", "FAILED", "");
  assert.equal(failed.tone, "neg");
  assert.equal(failed.label, "Tạo vận đơn thất bại");

  // Real backend sets PREPARING + tracking_code when shipment created (no READY_TO_SHIP)
  const withTracking = getSellerOrderStatusMeta("PREPARING", "", "GHN-001");
  assert.equal(withTracking.tone, "warn");
  assert.equal(withTracking.label, "Chờ hãng lấy hàng");
  assert.equal(getSellerOrderStatusMeta("PREPARING", "QUEUED", "GHN-001").label, "Chờ hãng lấy hàng");

  assert.equal(getNextBackendOrderAction("PREPARING", "", "")?.label, "Tạo vận đơn");
  assert.equal(getNextBackendOrderAction("PREPARING", "", "FAILED")?.label, "Thử tạo lại ngay");
  assert.equal(getNextBackendOrderAction("PREPARING", "", "QUEUED")?.label, "Thử tạo lại ngay");
  assert.equal(getNextBackendOrderAction("PREPARING", "GHN-001", "QUEUED"), null);

  assert.equal(formatPaymentMethodVi("ZALOPAY"), "Ví ZaloPay");
  assert.equal(formatPaymentMethodVi("VIETQR"), "VietQR");
  assert.equal(formatPaymentMethodVi("COD"), "Thu hộ COD");

  // Shared getPaymentStatusInfo semantics for real backend COD & online payment_status
  assert.equal(formatPaymentStatusVi("PENDING", "COD", "CONFIRMED"), "Thu hộ khi giao");
  assert.equal(formatPaymentStatusVi("CANCELLED", "COD", "CANCELLED"), "Không thu hộ");
  assert.equal(formatPaymentStatusVi("PENDING", "COD", "DELIVERED"), "Đã thu hộ COD");
  assert.equal(formatPaymentStatusVi("PENDING", "COD", "RETURN_REQUESTED"), "Đã thu hộ COD");
  assert.equal(formatPaymentStatusVi("COD_PENDING", "COD", "RETURN_REQUESTED"), "Đã thu hộ COD");
  assert.equal(formatPaymentStatusVi("SUCCEEDED", "COD", "RETURNED"), "Đã thu hộ COD");
  assert.equal(formatPaymentStatusVi("CANCELLED", "COD", "RETURNED"), "Không thu hộ");
  assert.equal(formatPaymentStatusVi("REFUNDED", "COD", "RETURNED"), "Đã hoàn tiền");
  assert.equal(formatPaymentStatusVi("SUCCEEDED", "COD", "CONFIRMED"), "Đã thanh toán");
  assert.equal(formatPaymentStatusVi("PENDING", "VIETQR", "PENDING"), "Chờ thanh toán");
  assert.equal(formatPaymentStatusVi("SUCCEEDED", "VIETQR", "CONFIRMED"), "Đã thanh toán");
  assert.equal(formatPaymentStatusVi("EXPIRED", "VIETQR", "CANCELLED"), "Hết hạn thanh toán");
});

test("normalizeSellerShipmentResponse preserves root events & fee_vnd from real gateway GET /api/v1/shipping/shipments/{id}", () => {
  // Real gateway payload shape (handlers_shipping.go:43-59, 90-97, 595-598)
  const gatewayPayload = {
    shipment: {
      shipment_id: "ship-001",
      order_id: "ord-001",
      shop_id: "shop-001",
      carrier_code: "ghn",
      service_code: "STANDARD",
      tracking_code: "GHN-20261125-001",
      label_url: "/api/v1/shipping/labels/GHN-20261125-001",
      status: "DELIVERING",
      fee_vnd: 28000,
      cod_amount_vnd: 338000,
      direction: "FORWARD",
      return_request_id: "",
      redelivery_count: 0,
      created_at: "2026-11-25T08:00:00Z",
      updated_at: "2026-11-25T10:30:00Z",
    },
    events: [
      {
        id: "ev-1",
        shipment_id: "ship-001",
        status: "CREATED",
        raw_status: "ready_to_pick",
        description: "Đã tiếp nhận vận đơn",
        occurred_at: "2026-11-25T08:00:00Z",
      },
      {
        id: "ev-2",
        shipment_id: "ship-001",
        status: "DELIVERING",
        raw_status: "delivering",
        description: "Bưu tá đang giao hàng",
        occurred_at: "2026-11-25T10:30:00Z",
      },
    ],
  };

  const normalized = normalizeSellerShipmentResponse(gatewayPayload);
  assert.ok(normalized.shipment);
  assert.equal(normalized.shipment.id, "ship-001");
  assert.equal(normalized.shipment.fee_vnd, 28000);
  assert.equal(resolveSellerShipmentFeeVnd(normalized.shipment), 28000);
  assert.equal(normalized.events.length, 2);
  assert.equal(normalized.events[1].status, "DELIVERING");
  assert.equal(normalized.events[1].description, "Bưu tá đang giao hàng");
  assert.deepEqual(normalized.shipment.events, normalized.events);

  // Handles null shipment cleanly
  assert.deepEqual(normalizeSellerShipmentResponse({ shipment: null, events: [] }), {
    shipment: null,
    events: [],
  });

  // SellerOrderDetailClient wires root events, fee_vnd helper, and pollTick loop for QUEUED polling
  const detailSrc = readFileSync(resolve(__dirname, "SellerOrderDetailClient.tsx"), "utf8");
  assert.ok(detailSrc.includes("initialShipmentEvents"), "must accept initialShipmentEvents prop");
  assert.ok(detailSrc.includes("resolveSellerShipmentFeeVnd"), "must read fee_vnd via helper");
  assert.ok(
    detailSrc.includes("setPollTick") && detailSrc.includes("[isShipmentQueued, reloadDetail, pollTick]"),
    "QUEUED polling effect must re-trigger via pollTick"
  );
});

test("extractSellerUnreadChatCount reads gateway total_unread and SellerLayout uses /api/v1/seller/chat/conversations", () => {
  // Real gateway GET /api/v1/seller/chat/conversations?page=1&page_size=1 response (handlers_chat.go:872-878)
  assert.equal(
    extractSellerUnreadChatCount({
      items: [{ id: "conv-1", unread_count: 2 }],
      total: 12,
      total_unread: 7,
      page: 1,
      page_size: 1,
    }),
    7
  );
  // Zero total_unread overrides stale/empty items
  assert.equal(
    extractSellerUnreadChatCount({
      items: [],
      total: 0,
      total_unread: 0,
      page: 1,
      page_size: 1,
    }),
    0
  );
  // Falls back to summing items when total_unread is absent
  assert.equal(
    extractSellerUnreadChatCount({
      items: [{ unread_count: 3 }, { unread_count: 1 }],
    }),
    4
  );

  const layoutSrc = readFileSync(resolve(__dirname, "../../app/(seller)/layout.tsx"), "utf8");
  assert.ok(
    layoutSrc.includes("/api/v1/seller/chat/conversations"),
    "SellerLayout must query /api/v1/seller/chat/conversations instead of unscoped /api/v1/chat/conversations"
  );
  assert.equal(
    layoutSrc.includes('role: "seller"'),
    false,
    "SellerLayout must not pass ignored query param role=seller"
  );
});
