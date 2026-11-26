// Run: cd web && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/assistant/assistant.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { renderToStaticMarkup } from "react-dom/server";
import { createSseParser } from "./sseParser.ts";
import { renderAssistantMarkdown, sanitizeAssistantLinkUrl } from "./assistantMarkdown.ts";
import {
  extractToolCardData,
  getAssistantOrderStatusLabel,
  getToolActivityLabel,
  normalizeDescribeProduct,
  normalizeReviewSummary,
  normalizeSellerReport,
  normalizeSuggestReply,
  sanitizeAssistantError,
  shouldDisplayReviewSummary,
  shouldRefreshCartAfterToolResult,
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
    'event: tool_call\ndata: {"id":"tc_1","name":"search_products","arguments":{"query":"áo mát"}}\n\n',
    'event: tool_result\ndata: {"id":"tc_1","name":"search_products","ok":true,"data":{"products":[{"id":"p1","name":"Áo Dry-Fit","min_price_vnd":229000,"in_stock":true}]}}\n\n',
    'event: token\ndata: {"text":"Mình gợi ý "}\n\n',
    'event: token\ndata: {"text":"Áo Dry-Fit thoáng mát."}\n\n',
    'event: done\ndata: {"conversation_id":"conv_9","message_id":"msg_9"}\n\n',
  ].join("");

  parser.feed(stream);
  parser.flush();

  assert.equal(events.length, 5);
  assert.equal(events[0].type, "tool_call");
  assert.equal(events[0].name, "search_products");
  assert.deepEqual(events[0].arguments, { query: "áo mát" });

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

// ---- 2. Tool Card Extraction tests (aligned with real gateway DTO & assistant tools schemas) ----

test("extractToolCardData parses real search_products ProductHit & gateway searchProductHitDTO and sanitizes unsafe URLs", () => {
  // Real retriever.ProductHit shape from services/assistant/internal/tools/registry.go (search_products)
  // plus real gateway searchProductHitDTO shape (thumbnail_url, slug, default_sku_id).
  const extracted = extractToolCardData({
    type: "tool_result",
    id: "tc_prod",
    name: "search_products",
    ok: true,
    data: {
      shop_id: "shop-a",
      total: 3,
      count: 3,
      products: [
        {
          id: "11111111-1111-4111-8111-111111111111",
          shop_id: "shop-a",
          shop_name: "Xưởng Dệt Hà Nội",
          category_id: "cat-1",
          category: "Áo thun",
          name: "Áo thun Cotton Compact",
          min_price_vnd: 199000,
          max_price_vnd: 229000,
          image_url: "/products/ao-thun-cotton-trang.png",
          in_stock: true,
          score: 0.91,
          skus: [
            {
              id: "sku-cotton-m",
              sku_code: "ATC-TRG-M",
              name: "Trắng / M",
              price_vnd: 199000,
              available_stock: 14,
              attributes: { color: "Trắng", size: "M" },
            },
          ],
        },
        {
          id: "22222222-2222-4222-8222-222222222222",
          shop_id: "shop-a",
          slug: "ao-polo",
          name: "Áo Polo Pique",
          min_price_vnd: 289000,
          max_price_vnd: 289000,
          thumbnail_url: "/products/ao-polo-pique.png",
          in_stock: false,
          default_sku_id: "sku-polo-1",
        },
        {
          id: "prod-xss",
          slug: "ao-so-mi",
          name: "Áo Sơ Mi Linen",
          min_price_vnd: 349000,
          image_url: "javascript:alert(1)",
          href: "javascript:alert(2)",
          in_stock: false,
        },
      ],
    },
  });

  assert.equal(extracted.products.length, 3);
  assert.equal(extracted.products[0].href, "/san-pham/11111111-1111-4111-8111-111111111111");
  assert.equal(extracted.products[0].priceVnd, 199000);
  assert.equal(extracted.products[0].image, "/products/ao-thun-cotton-trang.png");
  assert.equal(extracted.products[0].stockLabel, "Còn hàng");
  assert.equal(extracted.products[0].skuId, "sku-cotton-m");

  assert.equal(extracted.products[1].href, "/san-pham/ao-polo");
  assert.equal(extracted.products[1].priceVnd, 289000);
  assert.equal(extracted.products[1].image, "/products/ao-polo-pique.png");
  assert.equal(extracted.products[1].stockLabel, "Tạm hết hàng");
  assert.equal(extracted.products[1].skuId, "sku-polo-1");

  assert.equal(extracted.products[2].href, "/san-pham/ao-so-mi");
  assert.equal(extracted.products[2].image, undefined);
  assert.equal(extracted.products[2].stockLabel, "Tạm hết hàng");
});

test("extractToolCardData handles real add_to_cart confirmation, completed cartDTO, and errors", () => {
  // 1. Unconfirmed add_to_cart from services/assistant/internal/tools/registry.go
  const confirmResult = extractToolCardData({
    type: "tool_result",
    id: "tc_cart_confirm",
    name: "add_to_cart",
    ok: true,
    requiresConfirmation: true,
    data: {
      action: "add_to_cart",
      sku_id: "sku-99",
      quantity: 2,
      product_name: "Áo thun Dry-Fit Cổ Vịt",
      price_vnd: 249000,
      message: "Vui lòng xác nhận thêm 2 sản phẩm (SKU sku-99) vào giỏ hàng của bạn.",
    },
  });
  assert.equal(confirmResult.pendingCartActions.length, 1);
  assert.equal(confirmResult.pendingCartActions[0].skuId, "sku-99");
  assert.equal(confirmResult.pendingCartActions[0].quantity, 2);
  assert.equal(confirmResult.pendingCartActions[0].priceVnd, 249000);
  assert.equal(confirmResult.pendingCartActions[0].status, "pending");

  // 2. Confirmed add_to_cart returning real gateway cartDTO (handlers_cart.go)
  const addedResult = extractToolCardData({
    type: "tool_result",
    id: "tc_cart_added",
    name: "add_to_cart",
    ok: true,
    requiresConfirmation: false,
    data: {
      sku_id: "sku-99",
      quantity: 5,
      clamped_to_stock: true,
      cart_url: "/cart",
      cart: {
        user_id: "buyer-1",
        total_items: 3,
        total_vnd: 747000,
        shop_groups: [
          {
            shop_id: "shop-a",
            shop_name: "Xưởng Dệt Hà Nội",
            subtotal_vnd: 747000,
            total_weight_grams: 750,
            items: [
              {
                sku_id: "sku-99",
                product_id: "prod-99",
                shop_id: "shop-a",
                product_name: "Áo thun Dry-Fit Cổ Vịt",
                sku_name: "Xanh cổ vịt / L",
                image_url: "/products/ao-dryfit.png",
                price_vnd: 249000,
                quantity: 3,
                available_stock: 3,
                selected: true,
                unavailable: false,
              },
            ],
          },
        ],
      },
    },
  });
  assert.equal(addedResult.pendingCartActions.length, 1);
  assert.equal(addedResult.pendingCartActions[0].status, "added");
  assert.equal(addedResult.pendingCartActions[0].productId, "prod-99");
  assert.equal(addedResult.pendingCartActions[0].productName, "Áo thun Dry-Fit Cổ Vịt");
  assert.equal(addedResult.pendingCartActions[0].variantLabel, "Xanh cổ vịt / L");
  assert.equal(addedResult.pendingCartActions[0].priceVnd, 249000);
  assert.equal(addedResult.pendingCartActions[0].quantity, 3);
  assert.match(addedResult.pendingCartActions[0].statusMessage ?? "", /tối đa/);

  // 3. Failed add_to_cart must NOT render a pending confirmation card
  const failedCart = extractToolCardData({
    type: "tool_result",
    id: "tc_cart_fail",
    name: "add_to_cart",
    ok: false,
    error: "sản phẩm tạm hết hàng",
    data: { sku_id: "sku-99", quantity: 1 },
  });
  assert.equal(failedCart.pendingCartActions.length, 0);
});

test("extractToolCardData parses real gateway orderDTO with total_payable_vnd, canonical statuses, and valid site routes", () => {
  // Real gateway orderDTO from services/gateway/internal/router/handlers_order.go
  // returned by get_order_status (both list `orders` and single `order`).
  const listResult = extractToolCardData({
    type: "tool_result",
    id: "tc_order_list",
    name: "get_order_status",
    ok: true,
    requiresAuth: true,
    data: {
      count: 2,
      orders: [
        {
          id: "ord-42",
          order_code: "NM20260042",
          shop_id: "shop-a",
          sales_channel: "STOREFRONT",
          storefront_shop_id: "shop-a",
          status: "PREPARING",
          payment_method: "VIETQR",
          subtotal_vnd: 300000,
          shop_voucher_discount_vnd: 50000,
          shipping_fee_vnd: 30000,
          total_payable_vnd: 280000,
          tracking_code: "GHN88291",
          updated_at: "2026-11-26T01:00:00Z",
        },
        {
          id: "",
          order_code: "NM20260043",
          status: "RETURNED",
          subtotal_vnd: 199000,
          total_payable_vnd: 0,
        },
      ],
    },
  });
  assert.equal(listResult.requiresAuth, true);
  assert.equal(listResult.orders.length, 2);
  assert.equal(listResult.orders[0].totalVnd, 280000);
  assert.equal(listResult.orders[0].statusLabel, "Đang chuẩn bị hàng");
  assert.equal(listResult.orders[0].trackingCode, "GHN88291");
  assert.equal(listResult.orders[0].href, "/don-hang/ord-42");

  // Zero payable order (fully discounted) must display 0, not subtotal_vnd; fallback route must be real `/don-hang`.
  assert.equal(listResult.orders[1].totalVnd, 0);
  assert.equal(listResult.orders[1].statusLabel, "Đã trả hàng");
  assert.equal(listResult.orders[1].href, "/don-hang");

  // Single order shape (`data.order`) with PENDING status
  const singleResult = extractToolCardData({
    type: "tool_result",
    id: "tc_order_one",
    name: "get_order_status",
    ok: true,
    data: {
      order: {
        id: "ord-99",
        order_code: "NM20260099",
        status: "PENDING",
        subtotal_vnd: 450000,
        shipping_fee_vnd: 25000,
        total_payable_vnd: 475000,
      },
    },
  });
  assert.equal(singleResult.orders.length, 1);
  assert.equal(singleResult.orders[0].totalVnd, 475000);
  assert.equal(singleResult.orders[0].statusLabel, "Chờ thanh toán");
  assert.equal(singleResult.orders[0].href, "/don-hang/ord-99");

  assert.equal(getAssistantOrderStatusLabel("PENDING"), "Chờ thanh toán");
  assert.equal(getAssistantOrderStatusLabel("PREPARING"), "Đang chuẩn bị hàng");
  assert.equal(getAssistantOrderStatusLabel("RETURNED"), "Đã trả hàng");
});

test("extractToolCardData maps real search_policies /policies/* citations to real site /chinh-sach route", () => {
  // Real policy.Chunk + citations[] from services/assistant/internal/tools/registry.go (search_policies)
  // and services/assistant/internal/policy/rag.go.
  const policyResult = extractToolCardData({
    type: "tool_result",
    id: "tc_policy",
    name: "search_policies",
    ok: true,
    data: {
      count: 3,
      citations: [
        "/policies/returns#thoi-han-va-dieu-kien-doi-tra",
        "/policies/shipping#hang-van-chuyen-tich-hop-ghn-va-ghtk",
      ],
      chunks: [
        {
          id: "returns#thoi-han-va-dieu-kien-doi-tra",
          doc_slug: "returns",
          doc_title: "Chính sách Đổi trả & Hoàn tiền",
          section: "Thời hạn và điều kiện đổi trả",
          citation: "/policies/returns#thoi-han-va-dieu-kien-doi-tra",
          content: "Người mua có quyền gửi yêu cầu đổi trả trong vòng 7 ngày.",
          score: 0.0328,
        },
        {
          id: "shipping#hang-van-chuyen-tich-hop-ghn-va-ghtk",
          doc_slug: "shipping",
          doc_title: "Chính sách Vận chuyển & Địa chỉ",
          section: "Hãng vận chuyển tích hợp (GHN và GHTK)",
          citation: "/policies/shipping#hang-van-chuyen-tich-hop-ghn-va-ghtk",
          content: "NexusMart hợp tác với GHN và GHTK.",
          score: 0.025,
        },
        {
          doc_title: "Liên kết không hợp lệ",
          citation: "javascript:void(0)",
          doc_slug: "returns",
        },
        {
          doc_title: "Route không tồn tại",
          citation: "/unknown-404-route",
        },
      ],
    },
  });
  assert.equal(policyResult.citations.length, 2);
  assert.equal(policyResult.citations[0].href, "/chinh-sach#info-policies");
  assert.equal(policyResult.citations[0].title, "Chính sách Đổi trả & Hoàn tiền");
  assert.equal(policyResult.citations[1].href, "/chinh-sach#info-policies");
  assert.equal(policyResult.citations[1].title, "Chính sách Vận chuyển & Địa chỉ");

  assert.equal(getToolActivityLabel("search_policies"), "Đang kiểm tra chính sách của cửa hàng...");
  assert.equal(getToolActivityLabel("handoff_to_agent"), "Đang kết nối với nhân viên cửa hàng...");
  assert.equal(getToolActivityLabel("get_seller_report"), "Đang tổng hợp báo cáo kinh doanh...");
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

test("normalizeSellerReport maps real gateway/assistant metrics, stays idempotent across BFF+client, and handles sellerReportDTO", () => {
  // Real POST /api/v1/seller/assistant/report response (handlers_assistant.go:443-458)
  const protoStyle = normalizeSellerReport(
    {
      shop_id: "shop-a",
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
        pending_balance_vnd: 2300000,
        top_products: [
          {
            product_id: "p1",
            product_name: "Áo thun Cotton Compact",
            units_sold: 24,
            revenue_vnd: 4776000,
          },
        ],
      },
      usage: { input_tokens: 120, output_tokens: 180, total_tokens: 300, cost_vnd: 12 },
    },
    { question: "Sản phẩm bán chạy nhất 7 ngày qua" }
  );

  assert.equal(protoStyle.period, "7d");
  assert.equal(protoStyle.kpis.length, 4);
  assert.equal(protoStyle.kpis[0].value, "12.540.000₫");
  assert.equal(protoStyle.kpis[3].value, "9.800.000₫");
  assert.equal(protoStyle.kpis[3].caption, "Chờ đối soát: 2.300.000₫");
  assert.equal(protoStyle.rows.length, 1);
  assert.equal(protoStyle.rows[0].unitsSold, 24);
  assert.equal(protoStyle.rows[0].revenueVnd, 4776000);

  // Double-normalization: SellerAiReportClient calls normalizeSellerReport on JSON from BFF
  const bffJson = JSON.parse(JSON.stringify(protoStyle));
  const reNormalized = normalizeSellerReport(bffJson, {
    period: "7d",
    question: "Sản phẩm bán chạy nhất 7 ngày qua",
  });
  assert.deepEqual(reNormalized.rows, protoStyle.rows);
  assert.equal(reNormalized.genericRows, undefined);
  assert.equal(reNormalized.tableColumns, undefined);
  assert.deepEqual(reNormalized.kpis, protoStyle.kpis);

  // Real GET /api/v1/seller/reports shape (handlers_order.go:1543-1560: gmv_vnd + cancel_rate_bps)
  const orderReportStyle = normalizeSellerReport({
    report: {
      shop_id: "shop-a",
      from_date: "2026-11-20",
      to_date: "2026-11-26",
      gmv_vnd: 15450000,
      total_orders: 18,
      completed_orders: 14,
      cancelled_orders: 3,
      cancel_rate_bps: 1666,
      top_products: [
        {
          product_id: "prod-1",
          product_name: "Áo Linen",
          units_sold: 12,
          revenue_vnd: 8400000,
          order_count: 9,
        },
      ],
    },
  });
  assert.equal(orderReportStyle.kpis[0].value, "15.450.000₫");
  assert.equal(orderReportStyle.kpis[2].value, "16,7%");
  assert.equal(orderReportStyle.kpis[2].tone, "neg");
  assert.equal(orderReportStyle.rows.length, 1);
  assert.equal(orderReportStyle.rows[0].unitsSold, 12);

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
  assert.match(sanitizeAssistantError("BUDGET_EXCEEDED"), /giới hạn/);
});

test("extractToolCardData handles gateway productDTO (images + skus stock) and normalizeSellerReport preserves genericRows on double-normalization", () => {
  const fromProductDto = extractToolCardData({
    type: "tool_result",
    id: "tc_product_dto",
    name: "search_products",
    ok: true,
    data: {
      products: [
        {
          id: "33333333-3333-4333-8333-333333333333",
          shop_id: "shop-a",
          name: "Quần Short Thể Thao",
          slug: "quan-short-the-thao",
          images: ["/products/quan-short.png"],
          status: "APPROVED",
          min_price_vnd: 179000,
          max_price_vnd: 199000,
          skus: [
            {
              id: "sku-qs-1",
              name: "Đen / L",
              price_vnd: 179000,
              available_stock: 8,
              is_active: true,
            },
          ],
        },
        {
          id: "44444444-4444-4444-8444-444444444444",
          shop_id: "shop-a",
          name: "Áo Khoác Gió",
          slug: "ao-khoac-gio",
          images: [],
          status: "OUT_OF_STOCK",
          min_price_vnd: 420000,
          skus: [],
        },
      ],
    },
  });
  assert.equal(fromProductDto.products.length, 2);
  assert.equal(fromProductDto.products[0].image, "/products/quan-short.png");
  assert.equal(fromProductDto.products[0].stockLabel, "Còn hàng");
  assert.equal(fromProductDto.products[0].skuId, "sku-qs-1");
  assert.equal(fromProductDto.products[0].href, "/san-pham/quan-short-the-thao");
  assert.equal(fromProductDto.products[1].stockLabel, "Tạm hết hàng");

  const firstPass = normalizeSellerReport({
    period: "30d",
    answer: "Tổng hợp tồn kho theo danh mục.",
    rows: [{ category: "Áo thun", low_stock_skus: 3 }],
  });
  assert.deepEqual(firstPass.tableColumns, ["category", "low_stock_skus"]);
  assert.deepEqual(firstPass.genericRows, [{ category: "Áo thun", low_stock_skus: 3 }]);

  const secondPass = normalizeSellerReport(firstPass, { period: "30d" });
  assert.deepEqual(secondPass.tableColumns, firstPass.tableColumns);
  assert.deepEqual(secondPass.genericRows, firstPass.genericRows);
});

test("Mục 5: shouldRefreshCartAfterToolResult returns true only when add_to_cart returns status added and SiteAssistantWidget calls refreshCart", () => {
  // Real confirmed add_to_cart tool_result SSE payload (with gateway cartDTO)
  const addedExtracted = extractToolCardData({
    type: "tool_result",
    id: "tc_add_1",
    name: "add_to_cart",
    ok: true,
    requiresConfirmation: false,
    data: {
      sku_id: "sku-99",
      quantity: 2,
      clamped_to_stock: false,
      cart: {
        user_id: "buyer-1",
        total_items: 2,
        total_vnd: 498000,
        shop_groups: [
          {
            shop_id: "shop-a",
            shop_name: "Shop A",
            subtotal_vnd: 498000,
            total_weight_grams: 400,
            items: [
              {
                sku_id: "sku-99",
                product_id: "prod-99",
                shop_id: "shop-a",
                product_name: "Áo thun Dry-Fit",
                sku_name: "Trắng / L",
                price_vnd: 249000,
                quantity: 2,
                available_stock: 10,
                selected: true,
              },
            ],
          },
        ],
      },
    },
  });
  assert.equal(shouldRefreshCartAfterToolResult(addedExtracted), true);

  // Unconfirmed add_to_cart (status: "pending") must NOT trigger refreshCart
  const pendingExtracted = extractToolCardData({
    type: "tool_result",
    id: "tc_add_2",
    name: "add_to_cart",
    ok: true,
    requiresConfirmation: true,
    data: { sku_id: "sku-99", quantity: 1, product_name: "Áo thun Dry-Fit" },
  });
  assert.equal(shouldRefreshCartAfterToolResult(pendingExtracted), false);

  // Failed add_to_cart or unrelated tool must NOT trigger refreshCart
  const failedExtracted = extractToolCardData({
    type: "tool_result",
    id: "tc_add_3",
    name: "add_to_cart",
    ok: false,
    error: "out of stock",
    data: { sku_id: "sku-99", quantity: 1 },
  });
  assert.equal(shouldRefreshCartAfterToolResult(failedExtracted), false);

  // Verify SiteAssistantWidget wires refreshCart from useStore() in tool_result handler
  const widgetPath = resolve(
    fileURLToPath(new URL(".", import.meta.url)),
    "SiteAssistantWidget.tsx"
  );
  const widgetSrc = readFileSync(widgetPath, "utf8");
  assert.match(widgetSrc, /useStore\(\)/);
  assert.match(widgetSrc, /refreshCart/);
  assert.ok(widgetSrc.includes("shouldRefreshCartAfterToolResult"));
  assert.match(widgetSrc, /void\s+refreshCart\(\)/);
});

// ---- 6. Assistant markdown parser & safe React renderer tests ----

test("renderAssistantMarkdown formats bold, bullet lists, plain inline code, and strips buyer UUIDs", () => {
  const input = [
    "Dưới đây là các sản phẩm phù hợp:",
    "- **Áo thun Cotton Compact 220gsm Trắng** (Mã: `6b57d22d-1234-4567-89ab-cdef01234567`) — Giá: 199.000₫ (Còn hàng) | Phân loại: Trắng / M [11111111-2222-4333-8444-555555555555, tồn: 14]",
    "- **Áo Polo Pique** — Mã đơn `NM20260042` và mã SKU `ATC-M`",
    "- Mã SKU: `aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee`",
    "Liên hệ trực tiếp hoặc xem mã 99999999-8888-4777-8666-555555555555 nếu cần hỗ trợ.",
  ].join("\n");

  const html = renderToStaticMarkup(renderAssistantMarkdown(input, { stripUuids: true }));

  assert.match(html, /<p>Dưới đây là các sản phẩm phù hợp:<\/p>/);
  assert.match(
    html,
    /<ul><li><strong>Áo thun Cotton Compact 220gsm Trắng<\/strong> — Giá: 199\.000₫ \(Còn hàng\) \| Phân loại: Trắng \/ M \[tồn: 14\]<\/li>/
  );
  // Inline `code` becomes plain text (no <code> tag and no backticks)
  assert.match(
    html,
    /<li><strong>Áo Polo Pique<\/strong> — Mã đơn NM20260042 và mã SKU ATC-M<\/li><\/ul>/
  );
  assert.equal(html.includes("<code"), false);
  assert.equal(html.includes("`"), false);
  // Empty bullet after stripping UUID-only line is omitted
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
  // Bare UUID and (Mã: `uuid`) are completely removed
  assert.equal(html.includes("6b57d22d"), false);
  assert.equal(html.includes("11111111"), false);
  assert.equal(html.includes("aaaaaaaa"), false);
  assert.equal(html.includes("99999999"), false);
  assert.equal(html.includes("(Mã:"), false);
  assert.match(html, /<p>Liên hệ trực tiếp hoặc xem mã nếu cần hỗ trợ\.<\/p>/);

  // With stripUuids: false, UUIDs are preserved while markdown still renders cleanly
  const sellerHtml = renderToStaticMarkup(
    renderAssistantMarkdown("Gian hàng **Xưởng Dệt** (`6b57d22d-1234-4567-89ab-cdef01234567`)", {
      stripUuids: false,
    })
  );
  assert.match(
    sellerHtml,
    /<p>Gian hàng <strong>Xưởng Dệt<\/strong> \(6b57d22d-1234-4567-89ab-cdef01234567\)<\/p>/
  );
});

test("renderAssistantMarkdown allows only internal /... links, preserves UUID in href, and renders https/XSS/javascript: as plain text", () => {
  assert.equal(sanitizeAssistantLinkUrl("/san-pham/ao-thun"), "/san-pham/ao-thun");
  assert.equal(sanitizeAssistantLinkUrl("/chinh-sach#doi-tra"), "/chinh-sach#doi-tra");
  assert.equal(sanitizeAssistantLinkUrl("/p/ok?a=1#b"), "/p/ok?a=1#b");
  // External URLs (including https), dot-normalization bypasses, and unsafe schemes are rejected
  for (const bad of [
    "https://nexusmart.vn/huong-dan?a=1#size",
    "http://insecure.example.com",
    "javascript:alert(1)",
    "JaVaScRiPt:alert(1)",
    "java\tscript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "vbscript:msgbox(1)",
    "//evil.com",
    "/.//evil.com",
    "/a/..//evil.com",
    "/%2e//evil.com",
    "/../admin",
    "/\\evil.com",
    "/\t/evil.com",
  ]) {
    assert.equal(sanitizeAssistantLinkUrl(bad), "", `expected unsafe URL rejected: ${bad}`);
  }

  const safeInput =
    "Xem [Áo thun (Mã: `6b57d22d-1234-4567-89ab-cdef01234567`)](/san-pham/6b57d22d-1234-4567-89ab-cdef01234567) hoặc [Link ngoài](https://phish.example.com/login).";
  const safeHtml = renderToStaticMarkup(renderAssistantMarkdown(safeInput, { stripUuids: true }));
  assert.match(safeHtml, /<a href="\/san-pham\/6b57d22d-1234-4567-89ab-cdef01234567">Áo thun<\/a>/);
  // https link rendered as plain text (no <a>, no URL leak)
  assert.match(safeHtml, /hoặc Link ngoài\.<\/p>/);
  assert.equal(safeHtml.includes("phish.example.com"), false);

  const xssInput = [
    '<script>alert("xss")</script> <img src=x onerror=alert(1)>',
    "- [Nhấn ngay](javascript:void0)",
    "- [Link ngoặc lồng](javascript:alert(1))",
    "- [Data link](data:text/html,<h1>x</h1>)",
    "- [Data ngoặc](data:text/html,<script>alert(3)</script>)",
    "- [HTTPS ngoài](https://evil.example/pay) và [Proto-rel](//evil.com/x)",
    "- [Chuẩn hoá lách](/.//evil.com)",
  ].join("\n");
  const xssHtml = renderToStaticMarkup(renderAssistantMarkdown(xssInput));

  assert.equal(xssHtml.includes("<script>"), false);
  assert.equal(xssHtml.includes("<img"), false);
  assert.equal(xssHtml.includes("<a "), false);
  assert.equal(xssHtml.includes("evil.example"), false);
  assert.match(xssHtml, /&lt;script&gt;alert\(&quot;xss&quot;\)&lt;\/script&gt;/);
  assert.match(xssHtml, /<li>Nhấn ngay<\/li>/);
  // Nested-paren link stays plain escaped text (no <a>)
  assert.match(xssHtml, /<li>\[Link ngoặc lồng\]\(javascript:alert\(1\)\)<\/li>/);
  assert.match(xssHtml, /<li>Data link<\/li>/);
  assert.match(
    xssHtml,
    /<li>\[Data ngoặc\]\(data:text\/html,&lt;script&gt;alert\(3\)&lt;\/script&gt;\)<\/li>/
  );
  assert.match(xssHtml, /<li>HTTPS ngoài và Proto-rel<\/li>/);
  assert.match(xssHtml, /<li>Chuẩn hoá lách<\/li>/);
});

test("renderAssistantMarkdown runs in linear time (< 500ms) on 20,000-char adversarial ReDoS inputs", () => {
  const n = 20_000;
  const cases = [
    ["unclosed link", `[x](${"a".repeat(n)}`],
    ["unclosed bold", `**${"a".repeat(n)}`],
    ["unclosed backtick", `\`${"a".repeat(n)}`],
    ["open brackets", "[".repeat(n)],
    ["open paren spaces", `(${" ".repeat(n)}`],
    ["partial id label", `(mã: ${"a".repeat(n)}`],
    ["partial id spaces", `(mã: ${" ".repeat(n)}z`],
    ["repeated bullet star", "* ".repeat(n / 2)],
    ["repeated trailing stars", `${"*".repeat(n)}a`],
    ["repeated bold token", "**a".repeat(Math.ceil(n / 3)).slice(0, n)],
  ];

  for (const [name, sample] of cases) {
    for (const streaming of [true, false]) {
      const t0 = performance.now();
      renderAssistantMarkdown(sample, { stripUuids: true, streaming });
      const elapsedMs = performance.now() - t0;
      assert.ok(
        elapsedMs < 500,
        `ReDoS check failed for "${name}" (streaming=${streaming}): ${elapsedMs.toFixed(1)}ms`
      );
    }
  }
});

test("renderAssistantMarkdown strips unclosed ** and streaming partial markers without flicker", () => {
  // Unclosed ** strips raw symbols instead of leaking **
  const unclosedHtml = renderToStaticMarkup(
    renderAssistantMarkdown("Gợi ý: **Áo thun Cotton Compact")
  );
  assert.equal(unclosedHtml, "<p>Gợi ý: Áo thun Cotton Compact</p>");
  assert.equal(unclosedHtml.includes("**"), false);

  // Streaming trailing markers (*, **, closing single *, unclosed backtick, unclosed link URL)
  assert.equal(
    renderToStaticMarkup(renderAssistantMarkdown("Gợi ý: *", { streaming: true })),
    "<p>Gợi ý: </p>"
  );
  assert.equal(
    renderToStaticMarkup(renderAssistantMarkdown("Gợi ý: **", { streaming: true })),
    "<p>Gợi ý: </p>"
  );
  assert.equal(
    renderToStaticMarkup(renderAssistantMarkdown("Gợi ý: **Áo thun*", { streaming: true })),
    "<p>Gợi ý: Áo thun</p>"
  );
  assert.equal(
    renderToStaticMarkup(renderAssistantMarkdown("Mã: `ATC-M", { streaming: true })),
    "<p>Mã: ATC-M</p>"
  );
  assert.equal(
    renderToStaticMarkup(renderAssistantMarkdown("Xem [Giỏ hàng](/gio-ha", { streaming: true })),
    "<p>Xem Giỏ hàng</p>"
  );

  // Prefix-by-prefix streaming sweep on real assistant product line:
  // never throws, never flickers raw **, `, or UUID across any chunk boundary.
  const fullLine =
    "- **Áo thun Cotton Compact** (Mã: `6b57d22d-1234-4567-89ab-cdef01234567`) — Giá: 199.000₫";
  for (let k = 1; k <= fullLine.length; k++) {
    const prefix = fullLine.slice(0, k);
    const rendered = renderToStaticMarkup(
      renderAssistantMarkdown(prefix, { stripUuids: true, streaming: true })
    );
    assert.equal(rendered.includes("**"), false, `raw ** leaked at prefix ${k}: ${rendered}`);
    assert.equal(rendered.includes("`"), false, `raw backtick leaked at prefix ${k}: ${rendered}`);
    assert.equal(rendered.includes("6b57d22d"), false, `UUID leaked at prefix ${k}: ${rendered}`);
  }
});

test("SiteAssistantWidget & SellerAiReportClient share renderAssistantMarkdown safely without dangerouslySetInnerHTML", () => {
  const dir = fileURLToPath(new URL(".", import.meta.url));
  const widgetSrc = readFileSync(resolve(dir, "SiteAssistantWidget.tsx"), "utf8");
  const sellerReportSrc = readFileSync(
    resolve(dir, "../../app/(seller)/seller/bao-cao-ai/SellerAiReportClient.tsx"),
    "utf8"
  );

  assert.equal(widgetSrc.includes("dangerouslySetInnerHTML"), false);
  assert.equal(sellerReportSrc.includes("dangerouslySetInnerHTML"), false);
  assert.ok(widgetSrc.includes("renderAssistantMarkdown"));
  assert.ok(sellerReportSrc.includes("renderAssistantMarkdown"));
  // Raw msg.content still passed unchanged to backend history and aria-live="polite" preserved
  assert.match(widgetSrc, /aria-live="polite"/);
  assert.match(widgetSrc, /content:\s*m\.content\.trim\(\)/);
});
