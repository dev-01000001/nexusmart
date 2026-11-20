import test from "node:test";
import assert from "node:assert/strict";
import {
  mapCartError,
  mapCheckoutError,
  mapVoucherError,
  translatePromotionWarning,
} from "./checkoutErrors.ts";
import {
  enrichCartGroups,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
} from "./cartEnrich.ts";
import { buildMarketplaceCheckoutPayload } from "./checkoutPayload.ts";
import {
  getSalesChannelMeta,
  normalizeSalesChannel,
  parseSalesChannelFilter,
} from "../../lib/salesChannel.ts";

test("translatePromotionWarning maps English promotion warnings to clean Vietnamese without raw internals", () => {
  assert.match(translatePromotionWarning("voucher ABC not found"), /không tồn tại/i);
  assert.match(translatePromotionWarning("voucher XYZ quota exhausted"), /hết lượt/i);
  assert.match(translatePromotionWarning("voucher OLD has expired"), /hết hạn/i);
  assert.match(translatePromotionWarning("per-user limit reached"), /hết lượt áp dụng/i);
  const minMsg = translatePromotionWarning("voucher requires minimum order 350000 VND");
  assert.match(minMsg, /350\.000₫/);
  assert.doesNotMatch(minMsg, /VND/);
  assert.match(translatePromotionWarning("does not belong to shop 1234"), /gian hàng/);
});

test("mapCheckoutError maps out-of-stock, COD limit, voucher exhausted, and empty cart safely", () => {
  const oos = mapCheckoutError("FAILED_PRECONDITION", "INSUFFICIENT_STOCK: SKU abc has 0 available, requested 2");
  assert.equal(oos.code, "OUT_OF_STOCK");
  assert.equal(oos.status, 409);
  assert.doesNotMatch(oos.message, /SKU|INSUFFICIENT_STOCK|abc/);

  const cod = mapCheckoutError("FAILED_PRECONDITION", "COD_LIMIT_EXCEEDED: order total 6000000 exceeds 5000000");
  assert.equal(cod.code, "COD_LIMIT_EXCEEDED");
  assert.equal(cod.status, 409);
  assert.match(cod.message, /COD/);

  const vOut = mapCheckoutError("RESOURCE_EXHAUSTED", "voucher budget exhausted");
  assert.equal(vOut.code, "VOUCHER_EXHAUSTED");
  assert.equal(vOut.status, 409);

  const empty = mapCheckoutError("FAILED_PRECONDITION", "shopping cart is empty");
  assert.equal(empty.code, "EMPTY_CART");
  assert.equal(empty.status, 409);
});

test("mapCartError and mapVoucherError return clean buyer messages and status codes", () => {
  const oos = mapCartError("FAILED_PRECONDITION", "OUT_OF_STOCK: sku 123 has 0 available stock");
  assert.equal(oos.code, "OUT_OF_STOCK");
  assert.equal(oos.status, 409);

  const exhausted = mapVoucherError("RESOURCE_EXHAUSTED", "voucher is out of quota");
  assert.equal(exhausted.code, "VOUCHER_EXHAUSTED");
  assert.equal(exhausted.status, 409);

  const notFound = mapVoucherError("NOT_FOUND", "voucher not found");
  assert.equal(notFound.code, "VOUCHER_NOT_FOUND");
  assert.equal(notFound.status, 404);
});

test("enrichCartGroups pairs backend cart groups with catalog presentation while preserving real prices and stock", () => {
  const groups = enrichCartGroups(
    [
      {
        shop_id: "shop-uuid-1",
        shop_name: "Xưởng Dệt Bạch Đằng",
        subtotal_vnd: 358000,
        total_weight_grams: 440,
        items: [
          {
            sku_id: "sku-uuid-1",
            product_id: "prod-uuid-1",
            shop_id: "shop-uuid-1",
            product_name: "Áo thun Cotton Compact",
            sku_name: "Trắng / L",
            image_url: "",
            price_vnd: 179000,
            added_price_vnd: 179000,
            price_changed: false,
            quantity: 2,
            available_stock: 1,
            weight_grams: 220,
            selected: true,
          },
        ],
      },
    ],
    [
      {
        id: "prod-at-trang",
        backendId: "prod-uuid-1",
        slug: "ao-thun-cotton-compact-trang",
        name: "Áo thun Cotton Compact",
        shortName: "Áo thun",
        categorySlug: "ao-thun",
        categoryName: "Áo thun",
        shopId: "shop-bach-dang",
        gender: "unisex",
        price: 179000,
        image: "/products/ao-thun-cotton-trang.png",
        gallery: ["/products/ao-thun-cotton-trang.png"],
        colors: [{ code: "trang", name: "Trắng tinh", hex: "#fff", image: "/products/ao-thun-cotton-trang.png" }],
        sizes: ["L"],
        skus: [{ skuId: "sku-uuid-1", skuCode: "BD-AT01-TRANG-L", colorCode: "trang", size: "L", stock: 1, price: 179000 }],
        fabric: "",
        gsm: 220,
        fit: "",
        origin: "",
        careGuide: [],
        highlights: [],
        description: "",
        sizeChart: [],
      },
    ],
    [
      {
        id: "shop-bach-dang",
        backendId: "shop-uuid-1",
        slug: "xuong-det-bach-dang",
        name: "Xưởng Dệt Bạch Đằng",
        shortName: "Bạch Đằng",
        city: "Hải Phòng",
        provinceCode: "HP",
        specialty: "",
        foundedYear: 2018,
        responseTime: "",
        fulfillmentRate: "",
        returnPolicy: "",
        warehouseAddress: "",
        story: "",
        accentDot: "#0d7480",
        commissionRate: 0.05,
        freeShipThreshold: 199000,
      },
    ]
  );

  assert.equal(groups.length, 1);
  assert.equal(groups[0].shopSlug, "xuong-det-bach-dang");
  assert.equal(groups[0].items[0].productSlug, "ao-thun-cotton-compact-trang");
  assert.equal(groups[0].items[0].variantLabel, "Màu: Trắng tinh · Size L");
  assert.equal(groups[0].items[0].image, "/products/ao-thun-cotton-trang.png");
  assert.equal(groups[0].items[0].exceedsStock, true);
  assert.equal(groups[0].items[0].isOutOfStock, false);
});

test("voucher formatting and eligibility reasons handle fixed, percent, exhausted, and min-order vouchers", () => {
  const pct = {
    id: "v1",
    code: "SAVE10",
    name: "Giảm 10%",
    scope: "PLATFORM",
    shop_id: "",
    discount_type: "PERCENT",
    value_vnd: 0,
    discount_percent: 10,
    max_discount_vnd: 50000,
    min_order_vnd: 300000,
    quota: 100,
    used_count: 10,
    remaining_quota: 90,
    per_user_limit: 1,
    budget_vnd: 0,
    used_budget_vnd: 0,
    is_active: true,
    saved: false,
    eligible: true,
    ineligible_reason: "",
  };
  assert.equal(formatVoucherHeadline(pct), "Giảm 10% (tối đa 50.000₫)");
  assert.match(formatVoucherIneligibleReason(pct, 200000) ?? "", /100\.000₫/);
  assert.equal(formatVoucherIneligibleReason(pct, 350000), null);
  assert.match(
    formatVoucherIneligibleReason({ ...pct, remaining_quota: 0 }, 350000) ?? "",
    /hết lượt/i
  );
});

test("buildMarketplaceCheckoutPayload strips client sales_channel and storefront_shop_id and always sends MARKETPLACE", () => {
  const res = buildMarketplaceCheckoutPayload({
    address_id: " addr-1 ",
    from_cart: true,
    payment_method: "cod",
    sales_channel: "STOREFRONT",
    storefront_shop_id: "shop-evil-123",
    coins_to_use: 5000,
    platform_voucher_id: " v-plat ",
  });
  assert.equal(res.ok, true);
  assert.equal(res.payload.address_id, "addr-1");
  assert.equal(res.payload.payment_method, "COD");
  assert.equal(res.payload.sales_channel, "MARKETPLACE");
  assert.equal("storefront_shop_id" in res.payload, false);
  assert.equal(res.payload.coins_to_use, 5000);
  assert.equal(res.payload.platform_voucher_id, "v-plat");

  const missingAddr = buildMarketplaceCheckoutPayload({
    payment_method: "VIETQR",
    sales_channel: "STOREFRONT",
  });
  assert.deepEqual(missingAddr, { ok: false, code: "MISSING_ADDRESS" });

  const badMethod = buildMarketplaceCheckoutPayload({
    address_id: "addr-1",
    payment_method: "CRYPTO",
  });
  assert.deepEqual(badMethod, { ok: false, code: "INVALID_PAYMENT_METHOD" });
});

test("salesChannel helpers normalize, validate filters, and provide clean Vietnamese labels", () => {
  assert.equal(normalizeSalesChannel(undefined), "MARKETPLACE");
  assert.equal(normalizeSalesChannel(""), "MARKETPLACE");
  assert.equal(normalizeSalesChannel("marketplace"), "MARKETPLACE");
  assert.equal(normalizeSalesChannel("storefront"), "STOREFRONT");
  assert.equal(normalizeSalesChannel("UNKNOWN"), "MARKETPLACE");

  assert.equal(parseSalesChannelFilter(null), "");
  assert.equal(parseSalesChannelFilter(""), "");
  assert.equal(parseSalesChannelFilter("marketplace"), "MARKETPLACE");
  assert.equal(parseSalesChannelFilter("STOREFRONT"), "STOREFRONT");
  assert.equal(parseSalesChannelFilter("ALL"), null);
  assert.equal(parseSalesChannelFilter("DIRECT"), null);

  assert.equal(getSalesChannelMeta("MARKETPLACE").label, "Sàn NexusMart");
  assert.equal(getSalesChannelMeta("MARKETPLACE").isStorefront, false);
  assert.equal(getSalesChannelMeta("STOREFRONT").label, "Site riêng");
  assert.equal(getSalesChannelMeta("STOREFRONT").isStorefront, true);
});
