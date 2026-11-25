// Run: cd web && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/seller/sellerReturns.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  countPendingReturns,
  getNotificationCategoryLabel,
  getReturnStatusMeta,
  normalizeAutoReplyConfig,
  normalizeProductReviewSummary,
  normalizeSellerNotification,
  normalizeSellerNotifications,
  normalizeSellerReturn,
  normalizeSellerReturns,
  summarizeNotifications,
  validateRejectReason,
} from "./sellerReturns.ts";

test("normalizeSellerReturn parses valid payload, enforces integer VND, and sanitizes photos", () => {
  const result = normalizeSellerReturn({
    id: "ret-101",
    order_id: "ord-55",
    order_code: "NM-20261121-0055",
    buyer_id: "usr-9",
    shop_id: "shop-1",
    status: "pending",
    reason: "Sản phẩm bị lỗi đường may ở cổ áo",
    refund_amount_vnd: 249000.9,
    items: [
      {
        order_item_id: "oi-1",
        sku_id: "sku-m-navy",
        product_name: "Áo Polo Cotton 220gsm",
        sku_name: "Xanh Navy / M",
        quantity: 2,
        refund_amount_vnd: 249000,
      },
    ],
    photos: [
      "/uploads/returns/photo-1.jpg",
      "https://cdn.nexusmart.vn/returns/photo-2.jpg",
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "http://insecure.example/x.png",
      "//evil.example/leak.png",
    ],
    created_at: "2026-11-21T08:15:00Z",
  });

  assert.ok(result);
  assert.equal(result.id, "ret-101");
  assert.equal(result.status, "PENDING");
  assert.equal(result.refund_amount_vnd, 249000);
  assert.equal(result.items.length, 1);
  assert.equal(result.items[0].quantity, 2);
  assert.deepEqual(result.photos, [
    "/uploads/returns/photo-1.jpg",
    "https://cdn.nexusmart.vn/returns/photo-2.jpg",
  ]);
});

test("normalizeSellerReturn returns null for missing id or non-object", () => {
  assert.equal(normalizeSellerReturn(null), null);
  assert.equal(normalizeSellerReturn("bad"), null);
  assert.equal(normalizeSellerReturn({ order_id: "ord-1" }), null);
});

test("normalizeSellerReturns supports array, wrapped lists, and single return_request object", () => {
  const fromWrapped = normalizeSellerReturns({
    returns: [
      { id: "ret-1", status: "REQUESTED", reason: "Giao sai màu" },
      { id: "", status: "PENDING" },
    ],
  });
  assert.equal(fromWrapped.length, 1);
  assert.equal(fromWrapped[0].id, "ret-1");

  const fromSingle = normalizeSellerReturns({
    return_request: { return_id: "ret-2", status: "APPROVED", reason: "Đổi size L" },
  });
  assert.equal(fromSingle.length, 1);
  assert.equal(fromSingle[0].id, "ret-2");
  assert.equal(fromSingle[0].status, "APPROVED");

  assert.deepEqual(normalizeSellerReturns(undefined), []);
});

test("getReturnStatusMeta maps every return lifecycle status accurately", () => {
  assert.deepEqual(getReturnStatusMeta("PENDING"), {
    label: "Chờ duyệt",
    tone: "warn",
    canDecide: true,
  });
  assert.deepEqual(getReturnStatusMeta("requested"), {
    label: "Chờ duyệt",
    tone: "warn",
    canDecide: true,
  });
  assert.deepEqual(getReturnStatusMeta("APPROVED"), {
    label: "Đã duyệt",
    tone: "pos",
    canDecide: false,
  });
  assert.deepEqual(getReturnStatusMeta("REJECTED"), {
    label: "Đã từ chối",
    tone: "neg",
    canDecide: false,
  });
  assert.deepEqual(getReturnStatusMeta("RETURNING"), {
    label: "Đang hoàn hàng",
    tone: "neutral",
    canDecide: false,
  });
  assert.deepEqual(getReturnStatusMeta("REFUNDED"), {
    label: "Đã hoàn tiền",
    tone: "pos",
    canDecide: false,
  });
  assert.equal(getReturnStatusMeta("CUSTOM_STATE").canDecide, false);
});

test("validateRejectReason enforces trimmed length between 5 and 500 chars", () => {
  const empty = validateRejectReason("   ");
  assert.equal(empty.ok, false);
  if (!empty.ok) assert.equal(empty.code, "INVALID_REASON");

  const tooShort = validateRejectReason("  1234 ");
  assert.equal(tooShort.ok, false);

  const nonString = validateRejectReason(123456);
  assert.equal(nonString.ok, false);

  const tooLong = validateRejectReason("a".repeat(501));
  assert.equal(tooLong.ok, false);

  const valid = validateRejectReason("  Sản phẩm đã qua sử dụng và mất tem mác.  ");
  assert.equal(valid.ok, true);
  if (valid.ok) {
    assert.equal(valid.value, "Sản phẩm đã qua sử dụng và mất tem mác.");
  }
});

test("countPendingReturns counts pending orders and return requests only", () => {
  const count = countPendingReturns([
    { status: "CONFIRMED" },
    { status: "RETURN_REQUESTED" },
    { status: "return_requested" },
    { status: "PENDING" },
    { status: "REQUESTED" },
    { status: "RETURNED" },
    { status: "APPROVED" },
    { status: "REJECTED" },
    { status: null },
  ]);
  assert.equal(count, 4);
  assert.equal(countPendingReturns(null), 0);
});

test("normalizeSellerNotifications sanitizes links and summarizeNotifications aggregates accurately", () => {
  const normalized = normalizeSellerNotifications({
    items: [
      {
        id: "n-1",
        category: "ORDER",
        title: "Đơn mới chờ xác nhận",
        body: "Đơn NM-001 vừa được thanh toán",
        link: "/seller/don-hang/ord-1",
        is_read: false,
        created_at: "2026-11-21T07:00:00Z",
      },
      {
        id: "n-2",
        category: "PROMOTION",
        title: "Flash sale sắp bắt đầu",
        body: "Khung giờ 12h sắp diễn ra",
        link: "javascript:alert(1)",
        is_read: true,
        created_at: "2026-11-21T06:00:00Z",
      },
      {
        id: "n-3",
        category: "SYSTEM",
        title: "Cập nhật lịch đối soát",
        body: "Đối soát tuần này hoàn tất",
        link: "https://nexusmart.vn/seller/tai-chinh",
        is_read: false,
        created_at: "2026-11-20T18:00:00Z",
      },
    ],
    unread_count: 2,
    total: 3,
  });

  assert.equal(normalized.items.length, 3);
  assert.equal(normalized.items[0].link, "/seller/don-hang/ord-1");
  assert.equal(normalized.items[1].link, "");
  assert.equal(normalized.items[2].link, "https://nexusmart.vn/seller/tai-chinh");
  assert.equal(normalizeSellerNotification({}), null);

  const summary = summarizeNotifications(normalized.items);
  assert.deepEqual(summary, {
    total: 3,
    unread: 2,
    read: 1,
    byCategory: {
      order: 1,
      promotion: 1,
      system: 1,
      other: 0,
    },
  });
  assert.equal(getNotificationCategoryLabel("ORDER"), "Đơn hàng");
  assert.equal(getNotificationCategoryLabel("PROMOTION"), "Khuyến mãi");
  assert.equal(getNotificationCategoryLabel("SYSTEM"), "Hệ thống");
});

test("normalizeProductReviewSummary and normalizeAutoReplyConfig handle ready and insufficient payloads", () => {
  const ready = normalizeProductReviewSummary(
    {
      product_id: "prod-1",
      review_count: 24,
      average_rating: 4.8,
      summary: "Vải mát, giữ phom sau nhiều lần giặt.",
      pros: ["Vải cotton dày dặn", "Đường may gọn"],
      cons: ["Phom hơi ôm ở vai"],
    },
    "prod-fallback"
  );
  assert.equal(ready.ready, true);
  assert.equal(ready.review_count, 24);
  assert.equal(ready.pros.length, 2);
  assert.equal(ready.cons.length, 1);

  const insufficient = normalizeProductReviewSummary(
    {
      review_count: 4,
      summary: "",
      pros: [],
      cons: [],
    },
    "prod-2"
  );
  assert.equal(insufficient.ready, false);
  assert.equal(insufficient.product_id, "prod-2");

  const autoReply = normalizeAutoReplyConfig({
    auto_reply: {
      enabled: true,
      message: "Cảm ơn bạn đã nhắn tin, shop sẽ phản hồi trong ít phút.",
      updated_at: "2026-11-21T08:00:00Z",
    },
  });
  assert.equal(autoReply.enabled, true);
  assert.equal(autoReply.message, "Cảm ơn bạn đã nhắn tin, shop sẽ phản hồi trong ít phút.");
});
