// Run: cd web && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/seller/sellerReturns.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readBoundedBody } from "../../lib/server/proxy-security.ts";
import {
  SELLER_CHAT_READ_EVENT,
  applySellerChatReadDelta,
  countPendingReturns,
  extractSellerChatReadDelta,
  getNotificationCategoryLabel,
  getReturnStatusMeta,
  markSellerConversationRead,
  normalizeAutoReplyConfig,
  normalizeProductReviewSummary,
  normalizeSellerNotification,
  normalizeSellerNotifications,
  normalizeSellerReturn,
  normalizeSellerReturns,
  parseOptionalMarkReadBody,
  shouldClearUnreadAfterReadSync,
  summarizeNotifications,
  validateMarkReadPayload,
  validateRejectReason,
} from "./sellerReturns.ts";
import { normalizeReviewSummary } from "../assistant/assistantTypes.ts";

const WEB_SRC = resolve(fileURLToPath(new URL("../..", import.meta.url)));

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

test("normalizeProductReviewSummary preserves camelCase BFF output from normalizeReviewSummary(gatewayJson)", () => {
  // Real gateway GET /api/v1/products/{id}/review-summary (handlers_assistant.go:631-645)
  const gatewayReady = {
    product_id: "11111111-1111-4111-8111-111111111111",
    available: true,
    review_count: 25,
    min_reviews_required: 10,
    average_rating: 4.7,
    summary: "Chất vải mềm thoáng, giữ phom tốt sau khi giặt.",
    pros: ["Vải mát", "Đường may chắc chắn"],
    cons: ["Nên tăng 1 size nếu thích mặc rộng"],
    ai_generated: true,
    updated_at: "2026-11-25T09:15:00Z",
  };
  const bffReady = normalizeReviewSummary(gatewayReady, gatewayReady.product_id);
  const clientReady = normalizeProductReviewSummary(bffReady, "fallback-id");
  assert.equal(clientReady.product_id, gatewayReady.product_id);
  assert.equal(clientReady.ready, true);
  assert.equal(clientReady.review_count, 25);
  assert.equal(clientReady.average_rating, 4.7);
  assert.equal(clientReady.updated_at, "2026-11-25T09:15:00Z");
  assert.equal(clientReady.summary, gatewayReady.summary);
  assert.deepEqual(clientReady.pros, gatewayReady.pros);
  assert.deepEqual(clientReady.cons, gatewayReady.cons);

  // Unavailable summary with fallback copy must stay ready=false and keep actual reviewCount
  const gatewayUnavailable = {
    product_id: "22222222-2222-4222-8222-222222222222",
    available: false,
    review_count: 4,
    min_reviews_required: 10,
    average_rating: 4.5,
    summary: "Chưa đủ đánh giá để tổng hợp tự động.",
    pros: [],
    cons: [],
    ai_generated: false,
    updated_at: "2026-11-25T09:20:00Z",
  };
  const bffUnavailable = normalizeReviewSummary(gatewayUnavailable, gatewayUnavailable.product_id);
  const clientUnavailable = normalizeProductReviewSummary(bffUnavailable, "fallback-2");
  assert.equal(clientUnavailable.ready, false);
  assert.equal(clientUnavailable.review_count, 4);
  assert.equal(clientUnavailable.product_id, gatewayUnavailable.product_id);
});

test("markSellerConversationRead falls back to REST POST when WebSocket is not open, and validateMarkReadPayload validates body", async () => {
  const restCalls = [];
  const wsFailed = await markSellerConversationRead({
    conversationId: "conv-1",
    lastReadSeq: 12,
    messageId: "msg-12",
    sendWsRead: () => false,
    postRestRead: async (convId, body) => {
      restCalls.push({ convId, body });
      return { conversation_id: convId, last_read_seq: 12, unread_count: 0 };
    },
  });
  assert.equal(wsFailed, "rest");
  assert.deepEqual(restCalls, [
    { convId: "conv-1", body: { last_read_seq: 12, message_id: "msg-12" } },
  ]);

  // When WS is open and sendRead returns true, do not send duplicate REST call
  const wsOk = await markSellerConversationRead({
    conversationId: "conv-1",
    lastReadSeq: 13,
    messageId: "msg-13",
    sendWsRead: () => true,
    postRestRead: async (convId, body) => {
      restCalls.push({ convId, body });
    },
  });
  assert.equal(wsOk, "ws");
  assert.equal(restCalls.length, 1);

  assert.deepEqual(validateMarkReadPayload({ last_read_seq: 8, message_id: "msg-8" }), {
    ok: true,
    value: { last_read_seq: 8, message_id: "msg-8" },
  });
  assert.deepEqual(validateMarkReadPayload({}), { ok: true, value: {} });
  assert.equal(validateMarkReadPayload({ last_read_seq: -1 }).ok, false);
  assert.equal(validateMarkReadPayload("invalid").ok, false);
});

test("parseOptionalMarkReadBody accepts empty HTTP/2 JSON requests without Content-Length and rejects malformed JSON", async () => {
  // Chunked/HTTP2 POST with Content-Type: application/json and empty body (content-length === null)
  const emptyJsonReq = new Request("http://localhost/api/seller/chat/conversations/conv-1/read", {
    method: "POST",
    headers: { "content-type": "application/json" },
  });
  assert.equal(emptyJsonReq.headers.get("content-length"), null);
  const emptyBounded = await readBoundedBody(emptyJsonReq);
  assert.equal(emptyBounded.ok, true);
  if (emptyBounded.ok) {
    assert.deepEqual(parseOptionalMarkReadBody(emptyBounded.body), { ok: true, value: {} });
  }

  // Whitespace-only body is also treated as empty optional body
  assert.deepEqual(parseOptionalMarkReadBody(new TextEncoder().encode("   \n\t ")), {
    ok: true,
    value: {},
  });

  // Real gateway payload shape from POST /api/v1/chat/conversations/{id}/read
  const realReq = new Request("http://localhost/api/seller/chat/conversations/conv-1/read", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ last_read_seq: 4, message_id: "msg-4" }),
  });
  const realBounded = await readBoundedBody(realReq);
  assert.equal(realBounded.ok, true);
  if (realBounded.ok) {
    assert.deepEqual(parseOptionalMarkReadBody(realBounded.body), {
      ok: true,
      value: { last_read_seq: 4, message_id: "msg-4" },
    });
  }

  // Malformed JSON still fails with INVALID_JSON; invalid schema fails with INVALID_READ_PAYLOAD
  assert.deepEqual(parseOptionalMarkReadBody(new TextEncoder().encode("{not-json")), {
    ok: false,
    code: "INVALID_JSON",
  });
  assert.deepEqual(parseOptionalMarkReadBody(new TextEncoder().encode('{"last_read_seq":-5}')), {
    ok: false,
    code: "INVALID_READ_PAYLOAD",
  });

  // Route handler must use readBoundedBody + parseOptionalMarkReadBody (not content-length header heuristic)
  const routeSrc = readFileSync(
    resolve(WEB_SRC, "app/api/seller/chat/conversations/[id]/read/route.ts"),
    "utf8"
  );
  assert.ok(routeSrc.includes("readBoundedBody"));
  assert.ok(routeSrc.includes("parseOptionalMarkReadBody"));
  assert.equal(routeSrc.includes('req.headers.get("content-length")'), false);
});

test("shouldClearUnreadAfterReadSync only clears unread_count when read sync succeeded or conversation is empty", async () => {
  // When WS is closed and REST fails, markSellerConversationRead returns "failed" -> must NOT clear unread_count
  const failedResult = await markSellerConversationRead({
    conversationId: "conv-1",
    lastReadSeq: 5,
    messageId: "msg-5",
    sendWsRead: () => false,
    postRestRead: async () => {
      throw new Error("503 Service Unavailable");
    },
  });
  assert.equal(failedResult, "failed");
  assert.equal(shouldClearUnreadAfterReadSync(failedResult, true), false);
  assert.equal(shouldClearUnreadAfterReadSync("ws", true), true);
  assert.equal(shouldClearUnreadAfterReadSync("rest", true), true);
  assert.equal(shouldClearUnreadAfterReadSync(null, false), true);

  // SellerChatClient must await syncConversationRead and gate unread_count: 0 via shouldClearUnreadAfterReadSync
  const clientSrc = readFileSync(
    resolve(WEB_SRC, "app/(seller)/seller/chat/SellerChatClient.tsx"),
    "utf8"
  );
  assert.ok(clientSrc.includes("shouldClearUnreadAfterReadSync"));
  assert.equal(clientSrc.includes("void syncConversationRead("), false);
});

test("SellerOrderDetailClient passes order.payment_method into getTimelineEventTitle", () => {
  const detailSrc = readFileSync(
    resolve(WEB_SRC, "features/seller/SellerOrderDetailClient.tsx"),
    "utf8"
  );
  assert.ok(detailSrc.includes("getTimelineEventTitle(ev, order.payment_method)"));
});

test("applySellerChatReadDelta clamps unread badge non-negative and SellerChatClient syncs SellerShell via CustomEvent", () => {
  assert.equal(SELLER_CHAT_READ_EVENT, "nm:seller-chat-read");
  assert.equal(applySellerChatReadDelta(1, 1), 0);
  assert.equal(applySellerChatReadDelta(5, 2), 3);
  assert.equal(applySellerChatReadDelta(1, 4), 0);
  assert.equal(applySellerChatReadDelta(0, 2), 0);
  assert.equal(applySellerChatReadDelta(3, 0), 3);
  assert.equal(applySellerChatReadDelta(3, -2), 3);
  assert.equal(applySellerChatReadDelta(3, Number.NaN), 3);
  assert.equal(applySellerChatReadDelta(3, undefined), 3);
  assert.equal(applySellerChatReadDelta(-1, 1), 0);

  assert.equal(extractSellerChatReadDelta({ readCount: 2 }), 2);
  assert.equal(extractSellerChatReadDelta({ readCount: 2.9 }), 2);
  assert.equal(extractSellerChatReadDelta({ readCount: 0 }), 0);
  assert.equal(extractSellerChatReadDelta({ readCount: -1 }), 0);
  assert.equal(extractSellerChatReadDelta(null), 0);
  assert.equal(extractSellerChatReadDelta({}), 0);

  const chatSrc = readFileSync(
    resolve(WEB_SRC, "app/(seller)/seller/chat/SellerChatClient.tsx"),
    "utf8"
  );
  assert.ok(chatSrc.includes("SELLER_CHAT_READ_EVENT"));
  assert.match(chatSrc, /new CustomEvent(?:<[^>]+>)?\(SELLER_CHAT_READ_EVENT/);

  const shellSrc = readFileSync(resolve(WEB_SRC, "features/seller/SellerShell.tsx"), "utf8");
  assert.ok(shellSrc.includes("SELLER_CHAT_READ_EVENT"));
  assert.ok(shellSrc.includes("applySellerChatReadDelta"));
  assert.ok(shellSrc.includes("setLiveUnreadChatCount(Math.max(0, Math.trunc(unreadChatCount)))"));
});
