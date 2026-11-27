// Chạy: node --test web/src/features/account/ (Node 22, type stripping).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  backendStatusesFor,
  countOrdersByBackendStatus,
  filterOrdersByBackendStatus,
  mapGatewayOrder,
  mapOrderStatus,
  mapPaymentStatus,
} from "./orderView.ts";
import {
  getOrderStatusInfo,
  getPaymentStatusInfo,
  getTimelineEventTitle,
} from "./orderMeta.ts";

const lookups = {
  product: (id) =>
    id === "p-1"
      ? {
          slug: "ao-thun-cotton-compact-trang",
          name: "Áo thun Cotton Compact 220gsm Trắng",
          image: "/products/ao-thun-cotton-trang.png",
          colors: [{ code: "TRANG", name: "Trắng tinh", image: "/products/trang.png" }],
          skus: [{ skuId: "s-1", skuCode: "BD-AT01-TRANG-L", colorCode: "TRANG", size: "L" }],
        }
      : undefined,
  shop: (id) =>
    id === "shop-1" ? { slug: "xuong-det-bach-dang", name: "Xưởng Dệt Bạch Đằng" } : undefined,
};

function gwOrder(overrides = {}) {
  return {
    id: "o-1",
    order_code: "NM-0001",
    checkout_group_id: "g-1",
    shop_id: "shop-1",
    status: "CONFIRMED",
    payment_method: "COD",
    payment_id: "",
    payment_status: "COD_PENDING",
    subtotal_vnd: 358000,
    flash_sale_discount_vnd: 0,
    shop_voucher_discount_vnd: 20000,
    platform_voucher_discount_vnd: 0,
    freeship_discount_vnd: 22000,
    coins_discount_vnd: 0,
    shipping_fee_vnd: 22000,
    total_payable_vnd: 338000,
    carrier_code: "ghn",
    tracking_code: "",
    cancel_reason: "",
    shipping_address: {
      recipient_name: "Nguyễn Minh Quân",
      recipient_phone: "0912486305",
      province_code: "79",
      province_name: "Thành phố Hồ Chí Minh",
      ward_name: "Phường Xuân Hòa",
      street_address: "Số 248 Võ Văn Tần",
    },
    items: [
      {
        id: "i-1",
        sku_id: "s-1",
        product_id: "p-1",
        product_name: "Áo thun",
        sku_name: "Trắng L",
        unit_price_vnd: 179000,
        effective_unit_price_vnd: 179000,
        quantity: 2,
        line_total_vnd: 358000,
      },
    ],
    timeline: [
      {
        id: "t-1",
        from_status: "",
        to_status: "CONFIRMED",
        actor_role: "buyer",
        note: "Order created and confirmed (COD)",
        created_at: "2026-11-20T09:00:00Z",
      },
    ],
    created_at: "2026-11-20T09:00:00Z",
    updated_at: "2026-11-20T09:00:00Z",
    ...overrides,
  };
}

test("maps a COD order with catalog presentation data", () => {
  const v = mapGatewayOrder(gwOrder(), lookups);
  assert.equal(v.status, "cho_xac_nhan");
  assert.equal(v.paymentStatus, "cod_cho_thu");
  assert.equal(v.paymentMethod, "cod");
  assert.equal(v.isPaid, false);
  assert.equal(v.shopName, "Xưởng Dệt Bạch Đằng");
  assert.equal(v.shopSlug, "xuong-det-bach-dang");
  assert.equal(v.carrier, "Giao Hàng Nhanh (GHN)");
  assert.equal(v.shippingAddress, "Số 248 Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh");
  assert.deepEqual(v.items[0], {
    skuCode: "BD-AT01-TRANG-L",
    productId: "p-1",
    productSlug: "ao-thun-cotton-compact-trang",
    productName: "Áo thun Cotton Compact 220gsm Trắng",
    colorName: "Trắng tinh",
    size: "L",
    variantLabel: "Màu sắc: Trắng tinh · Kích cỡ: L",
    image: "/products/trang.png",
    unitPrice: 179000,
    quantity: 2,
    lineTotal: 358000,
  });
  assert.equal(v.total, 338000);
  assert.equal(v.canCancel, true);
  assert.equal(v.canConfirmReceived, false);
  // Không để lộ ghi chú kỹ thuật tiếng Anh của backend.
  assert.equal(v.timeline[0].title, "Đặt hàng thành công");
  assert.doesNotMatch(v.timeline[0].note, /Order created/);
});

test("falls back to backend names when the product is unknown to the catalog", () => {
  const v = mapGatewayOrder(
    gwOrder({
      shop_id: "shop-x",
      items: [{ ...gwOrder().items[0], product_id: "p-x", sku_id: "s-x" }],
    }),
    lookups
  );
  assert.equal(v.shopName, "Gian hàng NexusMart");
  assert.equal(v.items[0].productName, "Áo thun");
  assert.equal(v.items[0].colorName, "Trắng L");
  assert.equal(v.items[0].variantLabel, "Trắng L");
  assert.equal(v.items[0].productSlug, "");
  assert.equal(v.items[0].image, "");
});

test("formats custom non-garment SKU attributes via shared variant formatter without uppercasing", () => {
  const customLookups = {
    product: () => undefined,
    shop: () => ({ slug: "gom-bat-trang", name: "Gốm Bát Tràng" }),
    skuAttributes: (skuId) =>
      skuId === "s-ceramic" ? { "Màu men": "Men lam", "Quy cách": "Bộ 7 món" } : undefined,
  };
  const v = mapGatewayOrder(
    gwOrder({
      items: [
        {
          ...gwOrder().items[0],
          sku_id: "s-ceramic",
          product_id: "p-ceramic",
          product_name: "Ấm trà gốm vẽ tay",
          sku_name: "Men lam / Bộ 7 món",
        },
      ],
    }),
    customLookups
  );
  assert.equal(v.items[0].variantLabel, "Màu men: Men lam · Quy cách: Bộ 7 món");
});

test("subtotal shown excludes the flash sale discount already applied to line prices", () => {
  const v = mapGatewayOrder(
    gwOrder({ subtotal_vnd: 400000, flash_sale_discount_vnd: 42000 }),
    lookups
  );
  assert.equal(v.subtotal, 358000);
});

test("VietQR pending order exposes payment due time and retry state", () => {
  const v = mapGatewayOrder(
    gwOrder({
      status: "PENDING",
      payment_method: "VIETQR",
      payment_id: "pay-1",
      payment_status: "PENDING",
      payment_expires_at: "2026-11-20T09:15:00Z",
      timeline: [{ id: "t", from_status: "", to_status: "PENDING", actor_role: "buyer", note: "" }],
    }),
    lookups
  );
  assert.equal(v.paymentStatus, "cho_thanh_toan");
  assert.equal(v.paymentDueAt, "2026-11-20T09:15:00Z");
  assert.equal(v.paymentId, "pay-1");
  assert.equal(v.timeline[0].timestamp, "2026-11-20T09:00:00Z");
});

test("cancel reason is shown only when the buyer cancelled", () => {
  const buyer = mapGatewayOrder(
    gwOrder({
      status: "CANCELLED",
      timeline: [
        {
          id: "t",
          from_status: "CONFIRMED",
          to_status: "CANCELLED",
          actor_role: "buyer",
          note: "Đặt trùng",
        },
      ],
    }),
    lookups
  );
  assert.equal(buyer.status, "da_huy");
  assert.equal(buyer.cancelReason, "Lý do: Đặt trùng");
  assert.equal(buyer.canCancel, false);

  const system = mapGatewayOrder(
    gwOrder({
      status: "CANCELLED",
      payment_method: "VIETQR",
      timeline: [
        {
          id: "t",
          from_status: "PENDING",
          to_status: "CANCELLED",
          actor_role: "system",
          note: "payment expired",
        },
      ],
    }),
    lookups
  );
  assert.doesNotMatch(system.cancelReason, /payment expired/);
});

test("return request reason strips the backend prefix; delivered allows confirm/return", () => {
  const delivered = mapGatewayOrder(gwOrder({ status: "DELIVERED" }), lookups);
  assert.equal(delivered.status, "da_giao");
  assert.equal(delivered.canConfirmReceived, true);
  assert.equal(delivered.canRequestReturn, true);

  const ret = mapGatewayOrder(
    gwOrder({
      status: "RETURN_REQUESTED",
      timeline: [
        {
          id: "t",
          from_status: "DELIVERED",
          to_status: "RETURN_REQUESTED",
          actor_role: "buyer",
          note: "Return requested: Cần đổi size",
        },
      ],
    }),
    lookups
  );
  assert.equal(ret.status, "yeu_cau_tra_hang");
  assert.equal(ret.returnReason, "Cần đổi size");
});

test("status helpers", () => {
  assert.equal(mapOrderStatus("SHIPPING"), "dang_giao");
  assert.equal(mapOrderStatus("completed"), "da_giao");
  assert.equal(mapPaymentStatus("EXPIRED"), "thanh_toan_that_bai");
  assert.equal(mapPaymentStatus("REFUNDED"), "da_hoan_tien");
  assert.deepEqual(backendStatusesFor("da_giao"), ["DELIVERED", "COMPLETED"]);
});

test("preserves sales_channel and storefront_shop_id with legacy MARKETPLACE default", () => {
  const legacy = mapGatewayOrder(gwOrder(), lookups);
  assert.equal(legacy.salesChannel, "MARKETPLACE");
  assert.equal(legacy.sales_channel, "MARKETPLACE");
  assert.equal(legacy.storefrontShopId, "");
  assert.equal(legacy.storefront_shop_id, "");

  const storefront = mapGatewayOrder(
    gwOrder({ sales_channel: "STOREFRONT", storefront_shop_id: "shop-1" }),
    lookups
  );
  assert.equal(storefront.salesChannel, "STOREFRONT");
  assert.equal(storefront.sales_channel, "STOREFRONT");
  assert.equal(storefront.storefrontShopId, "shop-1");
  assert.equal(storefront.storefront_shop_id, "shop-1");
});

test("maps real gateway COD payment_status (PENDING/CANCELLED/DELIVERED), carriers (GHN/GHTK/mock_express), and shared labels", () => {
  // Real gateway COD checkout creates status=CONFIRMED, payment_status=PENDING (checkout.go:460-462)
  const codNew = mapGatewayOrder(
    gwOrder({
      status: "CONFIRMED",
      payment_method: "COD",
      payment_status: "PENDING",
      carrier_code: "ghtk",
    }),
    lookups
  );
  assert.equal(codNew.paymentStatus, "cod_cho_thu");
  assert.equal(codNew.paymentDueAt, undefined);
  assert.equal(codNew.isPaid, false);
  assert.equal(codNew.carrier, "GHTK");
  assert.deepEqual(
    getPaymentStatusInfo(codNew.paymentStatusRaw, codNew.paymentMethodRaw, codNew.backendStatus),
    { label: "Thu hộ khi giao", tone: "neutral" }
  );
  assert.equal(getOrderStatusInfo(codNew.backendStatus).label, "Đã xác nhận");

  // Real gateway cancelled COD sets payment_status=CANCELLED (postgres.go:1418-1425)
  const codCancelled = mapGatewayOrder(
    gwOrder({
      status: "CANCELLED",
      payment_method: "COD",
      payment_status: "CANCELLED",
      carrier_code: "mock_express",
    }),
    lookups
  );
  assert.equal(codCancelled.paymentStatus, undefined);
  assert.equal(codCancelled.carrier, "Chuyển phát Nexus");
  assert.deepEqual(
    getPaymentStatusInfo(
      codCancelled.paymentStatusRaw,
      codCancelled.paymentMethodRaw,
      codCancelled.backendStatus
    ),
    { label: "Không thu hộ", tone: "neutral" }
  );

  // Real gateway delivered/completed COD + GHN carrier label
  const codCompleted = mapGatewayOrder(
    gwOrder({
      status: "COMPLETED",
      payment_method: "COD",
      payment_status: "PENDING",
      carrier_code: "ghn",
    }),
    lookups
  );
  assert.equal(codCompleted.isPaid, true);
  assert.equal(codCompleted.carrier, "Giao Hàng Nhanh (GHN)");
  assert.equal(getOrderStatusInfo(codCompleted.backendStatus).label, "Hoàn tất");
  assert.deepEqual(
    getPaymentStatusInfo(
      codCompleted.paymentStatusRaw,
      codCompleted.paymentMethodRaw,
      codCompleted.backendStatus
    ),
    { label: "Đã thu hộ COD", tone: "pos" }
  );

  // Fallback carrier code is hidden from buyer UI
  const fallbackCarrier = mapGatewayOrder(gwOrder({ carrier_code: "fallback" }), lookups);
  assert.equal(fallbackCarrier.carrier, undefined);

  // RETURNED and SHIPPING use unified GATEWAY_ORDER_STATUS_LABELS
  assert.equal(getOrderStatusInfo("RETURNED").label, "Đã trả hàng");
  assert.equal(getOrderStatusInfo("SHIPPING").label, "Đang giao");
  assert.equal(getOrderStatusInfo("DELIVERED").label, "Đã giao");
});

test("countOrdersByBackendStatus and filterOrdersByBackendStatus separate PENDING from CONFIRMED without double-counting", () => {
  const orders = [
    mapGatewayOrder(
      gwOrder({
        id: "o-pending-qr",
        status: "PENDING",
        payment_method: "VIETQR",
        payment_id: "pay-1",
        payment_status: "PENDING",
      }),
      lookups
    ),
    mapGatewayOrder(
      gwOrder({
        id: "o-confirmed-cod",
        status: "CONFIRMED",
        payment_method: "COD",
        payment_status: "PENDING",
      }),
      lookups
    ),
    mapGatewayOrder(gwOrder({ id: "o-delivered", status: "DELIVERED" }), lookups),
    mapGatewayOrder(gwOrder({ id: "o-completed", status: "COMPLETED" }), lookups),
    mapGatewayOrder(gwOrder({ id: "o-returned", status: "RETURNED" }), lookups),
  ];

  const counts = countOrdersByBackendStatus(orders);
  assert.equal(counts[""], 5);
  assert.equal(counts.PENDING, 1);
  assert.equal(counts.CONFIRMED, 1);
  assert.equal(counts.DELIVERED, 1);
  assert.equal(counts.COMPLETED, 1);
  assert.equal(counts.RETURNED, 1);
  assert.equal(counts.RETURN_REQUESTED, 0);

  // Sum of individual status buckets equals total (zero double-counting)
  const bucketSum =
    counts.PENDING +
    counts.CONFIRMED +
    counts.PREPARING +
    counts.SHIPPING +
    counts.DELIVERED +
    counts.COMPLETED +
    counts.RETURN_REQUESTED +
    counts.RETURNED +
    counts.CANCELLED;
  assert.equal(bucketSum, counts[""]);

  assert.deepEqual(
    filterOrdersByBackendStatus(orders, "PENDING").map((o) => o.id),
    ["o-pending-qr"]
  );
  assert.deepEqual(
    filterOrdersByBackendStatus(orders, "CONFIRMED").map((o) => o.id),
    ["o-confirmed-cod"]
  );
  assert.deepEqual(
    filterOrdersByBackendStatus(orders, "COMPLETED").map((o) => o.id),
    ["o-completed"]
  );
  assert.deepEqual(
    filterOrdersByBackendStatus(orders, "RETURNED").map((o) => o.id),
    ["o-returned"]
  );
});

test("COD payment status labels accurately reflect RETURN_REQUESTED and RETURNED states", () => {
  // Real gateway COD order in RETURN_REQUESTED (only allowed from DELIVERED -> cash already collected)
  const codReturnRequested = mapGatewayOrder(
    gwOrder({
      status: "RETURN_REQUESTED",
      payment_method: "COD",
      payment_status: "PENDING",
    }),
    lookups
  );
  assert.equal(codReturnRequested.paymentStatus, "da_thanh_toan");
  assert.equal(codReturnRequested.isPaid, true);
  assert.deepEqual(
    getPaymentStatusInfo(
      codReturnRequested.paymentStatusRaw,
      codReturnRequested.paymentMethodRaw,
      codReturnRequested.backendStatus
    ),
    { label: "Đã thu hộ COD", tone: "pos" }
  );

  // Legacy COD_PENDING in RETURN_REQUESTED
  assert.equal(mapPaymentStatus("COD_PENDING", "COD", "RETURN_REQUESTED"), "da_thanh_toan");
  assert.deepEqual(getPaymentStatusInfo("COD_PENDING", "COD", "RETURN_REQUESTED"), {
    label: "Đã thu hộ COD",
    tone: "pos",
  });

  // Post-delivery returned COD where COD settlement already succeeded (awaiting bank refund)
  const codReturnedCollected = mapGatewayOrder(
    gwOrder({
      status: "RETURNED",
      payment_method: "COD",
      payment_status: "SUCCEEDED",
    }),
    lookups
  );
  assert.equal(codReturnedCollected.paymentStatus, "da_thanh_toan");
  assert.equal(codReturnedCollected.isPaid, true);
  assert.deepEqual(
    getPaymentStatusInfo(
      codReturnedCollected.paymentStatusRaw,
      codReturnedCollected.paymentMethodRaw,
      codReturnedCollected.backendStatus
    ),
    { label: "Đã thu hộ COD", tone: "pos" }
  );

  // Forward delivery failure SHIPPING -> RETURNED sets payment_status = CANCELLED (never collected)
  const codReturnedFailedDelivery = mapGatewayOrder(
    gwOrder({
      status: "RETURNED",
      payment_method: "COD",
      payment_status: "CANCELLED",
    }),
    lookups
  );
  assert.equal(codReturnedFailedDelivery.paymentStatus, undefined);
  assert.equal(codReturnedFailedDelivery.isPaid, false);
  assert.deepEqual(
    getPaymentStatusInfo(
      codReturnedFailedDelivery.paymentStatusRaw,
      codReturnedFailedDelivery.paymentMethodRaw,
      codReturnedFailedDelivery.backendStatus
    ),
    { label: "Không thu hộ", tone: "neutral" }
  );

  // Post-delivery returned COD after bank refund finished
  const codReturnedRefunded = mapGatewayOrder(
    gwOrder({
      status: "RETURNED",
      payment_method: "COD",
      payment_status: "REFUNDED",
    }),
    lookups
  );
  assert.equal(codReturnedRefunded.paymentStatus, "da_hoan_tien");
  assert.deepEqual(
    getPaymentStatusInfo(
      codReturnedRefunded.paymentStatusRaw,
      codReturnedRefunded.paymentMethodRaw,
      codReturnedRefunded.backendStatus
    ),
    { label: "Đã hoàn tiền", tone: "neutral" }
  );
});

test("timeline titles distinguish switched-to-COD confirmations and shipment creation events for both buyer and seller", () => {
  // Real gateway JSON for ZaloPay -> COD switch followed by seller confirm and shipment creation
  const switchedCodOrder = gwOrder({
    status: "PREPARING",
    payment_method: "COD",
    payment_status: "PENDING",
    carrier_code: "ghn",
    tracking_code: "GHN-20261126-001",
    timeline: [
      {
        id: "t-1",
        from_status: "",
        to_status: "PENDING",
        actor_role: "buyer",
        note: "Order created awaiting payment",
        created_at: "2026-11-26T02:00:00Z",
      },
      {
        id: "t-2",
        from_status: "PENDING",
        to_status: "CONFIRMED",
        actor_role: "buyer",
        note: "Switched payment method from ZALOPAY to COD; order confirmed",
        created_at: "2026-11-26T02:03:00Z",
      },
      {
        id: "t-3",
        from_status: "CONFIRMED",
        to_status: "PREPARING",
        actor_role: "seller",
        note: "Seller confirmed order and began preparation",
        created_at: "2026-11-26T02:05:00Z",
      },
      {
        id: "t-4",
        from_status: "PREPARING",
        to_status: "PREPARING",
        actor_role: "seller",
        note: "Shipment created via ghn (tracking: GHN-20261126-001)",
        created_at: "2026-11-26T02:06:00Z",
      },
    ],
  });

  // Seller / shared getTimelineEventTitle
  assert.equal(
    getTimelineEventTitle(switchedCodOrder.timeline[1], switchedCodOrder.payment_method),
    "Đơn hàng đã xác nhận"
  );
  // Even without payment_method arg, switch-to-COD note prevents misleading "Thanh toán thành công"
  assert.equal(getTimelineEventTitle(switchedCodOrder.timeline[1]), "Đơn hàng đã xác nhận");
  // Even with empty note, payment_method="COD" prevents misleading "Thanh toán thành công"
  assert.equal(
    getTimelineEventTitle({ from_status: "PENDING", to_status: "CONFIRMED", note: "" }, "COD"),
    "Đơn hàng đã xác nhận"
  );
  assert.equal(
    getTimelineEventTitle(switchedCodOrder.timeline[2], switchedCodOrder.payment_method),
    "Gian hàng đang chuẩn bị đơn hàng"
  );
  assert.equal(
    getTimelineEventTitle(switchedCodOrder.timeline[3], switchedCodOrder.payment_method),
    "Đã tạo vận đơn"
  );

  // Async shipment created and queued/failed distinction
  assert.equal(
    getTimelineEventTitle(
      {
        from_status: "PREPARING",
        to_status: "PREPARING",
        note: "Shipment created asynchronously via ghtk (tracking: GHTK-99)",
      },
      "COD"
    ),
    "Đã tạo vận đơn"
  );
  assert.equal(
    getTimelineEventTitle(
      {
        from_status: "PREPARING",
        to_status: "PREPARING",
        note: "Shipment creation queued for async retry",
      },
      "COD"
    ),
    "Gian hàng đang chuẩn bị đơn hàng"
  );

  // Buyer mapGatewayOrder timeline
  const buyerView = mapGatewayOrder(switchedCodOrder, lookups);
  assert.equal(buyerView.timeline[1].title, "Đơn hàng đã xác nhận");
  assert.match(buyerView.timeline[1].note, /COD/);
  assert.doesNotMatch(buyerView.timeline[1].note, /Thanh toán thành công/);
  assert.equal(buyerView.timeline[2].title, "Gian hàng đã xác nhận");
  assert.equal(buyerView.timeline[3].title, "Đã tạo vận đơn");
  assert.match(buyerView.timeline[3].note, /GHN-20261126-001/);

  // Online payment (VietQR / ZaloPay) keeps existing "Thanh toán thành công" titles
  const paidOnline = gwOrder({
    status: "CONFIRMED",
    payment_method: "VIETQR",
    payment_status: "SUCCEEDED",
    timeline: [
      {
        id: "t-1",
        from_status: "",
        to_status: "PENDING",
        actor_role: "buyer",
        note: "Order created awaiting payment",
        created_at: "2026-11-26T02:00:00Z",
      },
      {
        id: "t-2",
        from_status: "PENDING",
        to_status: "CONFIRMED",
        actor_role: "system",
        note: "Payment succeeded",
        created_at: "2026-11-26T02:02:00Z",
      },
    ],
  });
  assert.equal(
    getTimelineEventTitle(paidOnline.timeline[1], paidOnline.payment_method),
    "Thanh toán thành công · Đơn hàng đã xác nhận"
  );
  assert.equal(
    getTimelineEventTitle(paidOnline.timeline[1], "ZALOPAY"),
    "Thanh toán thành công · Đơn hàng đã xác nhận"
  );
  const buyerOnline = mapGatewayOrder(paidOnline, lookups);
  assert.equal(buyerOnline.timeline[1].title, "Đã nhận thanh toán");
});
