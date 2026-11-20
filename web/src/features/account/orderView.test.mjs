// Chạy: node --test web/src/features/account/ (Node 22, type stripping).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  backendStatusesFor,
  mapGatewayOrder,
  mapOrderStatus,
  mapPaymentStatus,
} from "./orderView.ts";

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
  shop: (id) => (id === "shop-1" ? { slug: "xuong-det-bach-dang", name: "Xưởng Dệt Bạch Đằng" } : undefined),
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
  assert.equal(v.carrier, "GHN");
  assert.equal(v.shippingAddress, "Số 248 Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh");
  assert.deepEqual(v.items[0], {
    skuCode: "BD-AT01-TRANG-L",
    productId: "p-1",
    productSlug: "ao-thun-cotton-compact-trang",
    productName: "Áo thun Cotton Compact 220gsm Trắng",
    colorName: "Trắng tinh",
    size: "L",
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
  assert.equal(v.items[0].productSlug, "");
  assert.equal(v.items[0].image, "");
});

test("subtotal shown excludes the flash sale discount already applied to line prices", () => {
  const v = mapGatewayOrder(gwOrder({ subtotal_vnd: 400000, flash_sale_discount_vnd: 42000 }), lookups);
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
        { id: "t", from_status: "CONFIRMED", to_status: "CANCELLED", actor_role: "buyer", note: "Đặt trùng" },
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
        { id: "t", from_status: "PENDING", to_status: "CANCELLED", actor_role: "system", note: "payment expired" },
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
