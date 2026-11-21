// Run: cd web && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/assistant/assistant.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { createSseParser } from "./sseParser.ts";
import {
  extractToolCardData,
  normalizeDescribeProduct,
  normalizeReviewSummary,
  normalizeSellerReport,
  normalizeSuggestReply,
  sanitizeAssistantError,
  shouldDisplayReviewSummary,
  validateAssistantMessages,
  validateDescribeProductInput,
  validateSellerReportInput,
} from "./assistantTypes.ts";

// ---- 1. SSE Parser tests ----

test("createSseParser parses token, tool_call, tool_result, done, and error events", () => {
  const events = [];
  const parser = createSseParser((ev) => events.push(ev));

  const stream = [
    ": heartbeat\n\n",
    'event: tool_call\ndata: {"id":"tc_1","name":"search_products","arguments":{"q":"áo mát"}}\n\n',
    'event: tool_result\ndata: {"id":"tc_1","name":"search_products","ok":true,"data":{"products":[{"id":"p1","slug":"ao-dryfit","name":"Áo Dry-Fit","price_vnd":229000}]}}\n\n',
    'event: token\ndata: {"text":"Mình gợi ý "}\n\n',
    'event: token\ndata: {"text":"Áo Dry-Fit thoáng mát."}\n\n',
    'event: done\ndata: {"conversation_id":"conv_9","message_id":"msg_9"}\n\n',
  ].join("");

  parser.feed(stream);
  parser.flush();

  assert.equal(events.length, 5);
  assert.equal(events[0].type, "tool_call");
  assert.equal(events[0].name, "search_products");
  assert.deepEqual(events[0].arguments, { q: "áo mát" });

  assert.equal(events[1].type, "tool_result");
  assert.equal(events[1].ok, true);
  assert.equal(events[1].name, "search_products");

  assert.equal(events[2].type, "token");
  assert.equal(events[2].text, "Mình gợi ý ");
  assert.equal(events[3].type, "token");
  assert.equal(events[3].text, "Áo Dry-Fit thoáng mát.");

  assert.equal(events[4].type, "done");
  assert.equal(events[4].conversationId, "conv_9");
});

test("createSseParser handles arbitrary chunk splits, CRLF split across chunks, and multi-line data", () => {
  const events = [];
  const parser = createSseParser((ev) => events.push(ev));

  const payload =
    'event: token\r\ndata: {"text":"Xin chào"}\r\n\r\n' +
    "event: token\ndata: Dòng 1\ndata: Dòng 2\n\n" +
    'event: error\r\ndata: {"code":"RATE_LIMITED","message":"Quá nhanh"}';

  // Feed 3 characters at a time to force splits mid-CRLF, mid-field, and mid-JSON.
  for (let i = 0; i < payload.length; i += 3) {
    parser.feed(payload.slice(i, i + 3));
  }
  parser.flush();

  assert.equal(events.length, 3);
  assert.deepEqual(events[0], { type: "token", text: "Xin chào" });
  assert.deepEqual(events[1], { type: "token", text: "Dòng 1\nDòng 2" });
  assert.equal(events[2].type, "error");
  assert.equal(events[2].code, "RATE_LIMITED");
  assert.equal(events[2].message, "Quá nhanh");
});

test("createSseParser falls back gracefully for plain-text token data without explicit event header", () => {
  const events = [];
  const parser = createSseParser((ev) => events.push(ev));
  parser.feed("data: Xin chào bạn\n\ndata:  từ cửa hàng!\n\n");
  parser.flush();

  assert.deepEqual(events, [
    { type: "token", text: "Xin chào bạn" },
    { type: "token", text: " từ cửa hàng!" },
  ]);
});

// ---- 2. Tool Card Extraction tests ----

test("extractToolCardData sanitizes unsafe URLs and builds safe storefront links", () => {
  const extracted = extractToolCardData({
    type: "tool_result",
    id: "tc_prod",
    name: "search_products",
    ok: true,
    data: {
      products: [
        {
          id: "prod-1",
          slug: "ao-thun-compact",
          name: "Áo thun Cotton Compact",
          price_vnd: 199000,
          image_url: "/products/ao-thun-cotton-trang.png",
          available_stock: 14,
        },
        {
          id: "prod-xss",
          slug: "ao-polo",
          name: "Áo Polo Pique",
          price_vnd: 289000,
          image_url: "javascript:alert(1)",
          href: "javascript:alert(2)",
          available_stock: 0,
        },
      ],
    },
  });

  assert.equal(extracted.products.length, 2);
  assert.equal(extracted.products[0].href, "/san-pham/ao-thun-compact");
  assert.equal(extracted.products[0].image, "/products/ao-thun-cotton-trang.png");
  assert.equal(extracted.products[0].stockLabel, "Còn hàng");

  assert.equal(extracted.products[1].href, "/san-pham/ao-polo");
  assert.equal(extracted.products[1].image, undefined);
  assert.equal(extracted.products[1].stockLabel, "Tạm hết hàng");
});

test("extractToolCardData pulls pending add-to-cart confirmation, orders, and safe policy citations", () => {
  const cartResult = extractToolCardData({
    type: "tool_result",
    id: "tc_cart",
    name: "add_to_cart",
    ok: true,
    requiresConfirmation: true,
    data: {
      sku_id: "sku-99",
      product_name: "Áo thun Dry-Fit Cổ Vịt",
      color_name: "Xanh cổ vịt",
      size: "L",
      quantity: 2,
      price_vnd: 249000,
    },
  });
  assert.equal(cartResult.pendingCartActions.length, 1);
  assert.equal(cartResult.pendingCartActions[0].skuId, "sku-99");
  assert.equal(cartResult.pendingCartActions[0].quantity, 2);
  assert.equal(cartResult.pendingCartActions[0].variantLabel, "Xanh cổ vịt / L");
  assert.equal(cartResult.pendingCartActions[0].status, "pending");

  const orderResult = extractToolCardData({
    type: "tool_result",
    id: "tc_order",
    name: "get_orders",
    ok: true,
    requiresAuth: true,
    data: {
      orders: [
        {
          id: "ord-42",
          order_code: "NM20260042",
          status: "SHIPPING",
          total_vnd: 398000,
        },
      ],
    },
  });
  assert.equal(orderResult.requiresAuth, true);
  assert.equal(orderResult.orders.length, 1);
  assert.equal(orderResult.orders[0].statusLabel, "Đang giao hàng");
  assert.equal(orderResult.orders[0].href, "/don-hang/ord-42");

  const policyResult = extractToolCardData({
    type: "tool_result",
    id: "tc_policy",
    name: "search_policy",
    ok: true,
    data: {
      chunks: [
        {
          doc_title: "Chính sách đổi trả",
          section: "Đổi size trong 7 ngày",
          doc_slug: "doi-tra",
        },
        {
          doc_title: "Liên kết không hợp lệ",
          citation: "javascript:void(0)",
        },
      ],
    },
  });
  assert.equal(policyResult.citations.length, 1);
  assert.equal(policyResult.citations[0].href, "/chinh-sach#doi-tra");
});

// ---- 3. Review Summary normalizer tests ----

test("normalizeReviewSummary & shouldDisplayReviewSummary enforce >= 10 reviews and non-empty content", () => {
  const valid = normalizeReviewSummary({
    product_id: "prod-1",
    available: true,
    review_count: 18,
    min_reviews_required: 10,
    average_rating: 4.76,
    summary: "Khách hàng đánh giá cao độ thoáng mát và giữ phom sau nhiều lần giặt.",
    pros: ["Vải cotton mềm mát", "Đường may chắc chắn"],
    cons: ["Form hơi rộng so với bảng size chuẩn"],
    ai_generated: true,
  });
  assert.equal(shouldDisplayReviewSummary(valid), true);
  assert.equal(valid.reviewCount, 18);
  assert.equal(valid.averageRating, 4.8);
  assert.equal(valid.pros.length, 2);
  assert.equal(valid.cons.length, 1);

  const tooFew = normalizeReviewSummary({
    product_id: "prod-2",
    available: true,
    review_count: 9,
    summary: "Chưa đủ mẫu đánh giá.",
    pros: ["Đẹp"],
  });
  assert.equal(shouldDisplayReviewSummary(tooFew), false);

  const emptyContent = normalizeReviewSummary({
    product_id: "prod-3",
    available: true,
    review_count: 25,
    summary: "",
    pros: [],
    cons: [],
  });
  assert.equal(shouldDisplayReviewSummary(emptyContent), false);
});

// ---- 4. Seller Report, Describe Product & Suggest Reply normalizers ----

test("normalizeSellerReport maps proto metrics and generic rows into clean KPIs and tables", () => {
  const protoStyle = normalizeSellerReport(
    {
      period: "7d",
      answer:
        "Doanh thu 7 ngày qua tăng ổn định.\n- Áo thun dẫn đầu lượt bán.\n- Tỷ lệ hủy ở mức thấp.",
      metrics: {
        total_orders: 42,
        completed_orders: 38,
        cancelled_orders: 2,
        cancel_rate_pct: 4.8,
        gross_revenue_vnd: 12540000,
        available_balance_vnd: 9800000,
        top_products: [
          {
            product_id: "p1",
            product_name: "Áo thun Cotton Compact",
            units_sold: 24,
            revenue_vnd: 4776000,
          },
        ],
      },
    },
    { question: "Sản phẩm bán chạy nhất 7 ngày qua" }
  );

  assert.equal(protoStyle.period, "7d");
  assert.equal(protoStyle.kpis.length, 4);
  assert.equal(protoStyle.kpis[0].value, "12.540.000₫");
  assert.equal(protoStyle.rows.length, 1);
  assert.equal(protoStyle.rows[0].unitsSold, 24);
  assert.equal(protoStyle.rows[0].revenueVnd, 4776000);

  const genericStyle = normalizeSellerReport({
    period: "invalid-period",
    narrative: "Tồn kho 2 SKU đang thấp.",
    kpis: [{ label: "SKU sắp hết", value: 2, tone: "warn" }],
    rows: [{ name: "Áo Polo Kem / L", quantity: 3, revenue_vnd: 867000, status: "Cần nhập" }],
  });
  assert.equal(genericStyle.period, "30d");
  assert.equal(genericStyle.kpis[0].value, "2");
  assert.equal(genericStyle.rows[0].extra, "Cần nhập");
});

test("normalizeDescribeProduct & normalizeSuggestReply handle both object and string payloads", () => {
  const desc = normalizeDescribeProduct({
    title_suggestion: "Áo thun Cotton Compact 220gsm mặc mát hằng ngày",
    description: "Vải cotton chải kỹ 220gsm giúp đứng phom và thấm hút mồ hôi tốt.",
    highlights: ["Cotton 220gsm thoáng khí", "Bo cổ bền dáng", "Cotton 220gsm thoáng khí"],
    ai_generated: true,
  });
  assert.equal(desc.highlights.length, 2);
  assert.equal(desc.aiGenerated, true);

  const repliesFromObjects = normalizeSuggestReply({
    suggestions: [
      { id: "1", text: "Chào bạn, mẫu này hiện còn đủ size M và L ạ.", tone: "friendly" },
      { id: "2", text: "Bạn cao 1m70 nặng 65kg mặc size L sẽ thoải mái ạ.", tone: "helpful" },
      { id: "3", text: "Chào bạn, mẫu này hiện còn đủ size M và L ạ.", tone: "dup" },
    ],
  });
  assert.equal(repliesFromObjects.suggestions.length, 2);

  const repliesFromStrings = normalizeSuggestReply({
    suggestions: ["Cảm ơn bạn đã nhắn tin.", "Đơn của bạn đang được đóng gói."],
  });
  assert.equal(repliesFromStrings.suggestions.length, 2);
});

// ---- 5. Validators & buyer-safe error sanitizer ----

test("validators reject malformed inputs and sanitizeAssistantError hides technical jargon", () => {
  assert.equal(validateAssistantMessages([]).ok, false);
  assert.equal(validateAssistantMessages([{ role: "system", content: "hack" }]).ok, false);
  assert.equal(validateAssistantMessages([{ role: "user", content: "   " }]).ok, false);
  assert.equal(validateAssistantMessages([{ role: "user", content: "a".repeat(2001) }]).ok, false);
  const okMsgs = validateAssistantMessages([{ role: "user", content: "  Áo nào mặc mát? " }]);
  assert.equal(okMsgs.ok, true);
  if (okMsgs.ok) {
    assert.equal(okMsgs.messages[0].content, "Áo nào mặc mát?");
  }

  assert.equal(validateDescribeProductInput({ name: "Áo" }).ok, false);
  const okDesc = validateDescribeProductInput({
    name: "Áo thun Cotton",
    category_name: "Áo thun",
    colors: ["Trắng", "Xám đá"],
    sizes: ["M", "L"],
    price_vnd: 199000.9,
  });
  assert.equal(okDesc.ok, true);
  if (okDesc.ok) {
    assert.equal(okDesc.data.price_vnd, 199000);
    assert.equal(okDesc.data.attributes.colors, "Trắng, Xám đá");
  }

  assert.equal(validateSellerReportInput({ question: "ab", period: "7d" }).ok, false);
  const okRep = validateSellerReportInput({ question: "Doanh thu 7 ngày", period: "7d" });
  assert.equal(okRep.ok, true);

  const sanitized = sanitizeAssistantError("INTERNAL", "pgvector RAG SSE token error");
  assert.equal(/jwt|sse|rag|pgvector|token/i.test(sanitized), false);
});
