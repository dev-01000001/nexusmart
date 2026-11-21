import { test } from "node:test";
import assert from "node:assert/strict";
import {
  RETURN_STATUS_META,
  isSafeReturnPhotoUrl,
  normalizeReturnRequest,
  normalizeReturnRequests,
  normalizeReturnStatus,
  sanitizeReturnPhotoUrl,
  validateReturnPayload,
} from "./returnsTypes.ts";

test("sanitizeReturnPhotoUrl allows relative / paths and https:// and blocks unsafe URLs", () => {
  assert.equal(sanitizeReturnPhotoUrl("/products/ao-thun.png"), "/products/ao-thun.png");
  assert.equal(
    sanitizeReturnPhotoUrl("https://cdn.nexusmart.vn/returns/photo-1.jpg"),
    "https://cdn.nexusmart.vn/returns/photo-1.jpg"
  );
  assert.equal(isSafeReturnPhotoUrl("javascript:alert(1)"), false);
  assert.equal(isSafeReturnPhotoUrl("data:image/png;base64,iVBORw0KGgo="), false);
  assert.equal(isSafeReturnPhotoUrl("http://insecure.example/a.jpg"), false);
  assert.equal(isSafeReturnPhotoUrl("//protocol-relative.example/a.jpg"), false);
  assert.equal(isSafeReturnPhotoUrl("/bad path with spaces.jpg"), false);
  assert.equal(isSafeReturnPhotoUrl("/bad\\backslash.jpg"), false);
});

test("validateReturnPayload accepts valid items, reason and safe photos", () => {
  const res = validateReturnPayload({
    items: [
      { order_item_id: "oi-1", sku_id: "sku-1", quantity: 2 },
      { sku_id: "sku-2", quantity: 1 },
    ],
    reason: "  Áo hơi kích vai, cần đổi sang size L.  ",
    photos: ["/uploads/a.jpg", "https://cdn.nexusmart.vn/b.jpg"],
  });
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.equal(res.data.reason, "Áo hơi kích vai, cần đổi sang size L.");
    assert.equal(res.data.items.length, 2);
    assert.equal(res.data.items[0].quantity, 2);
    assert.deepEqual(res.data.photos, ["/uploads/a.jpg", "https://cdn.nexusmart.vn/b.jpg"]);
  }
});

test("validateReturnPayload rejects short/long reason, empty items, invalid quantities and unsafe photos", () => {
  const shortReason = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 1 }],
    reason: "ngắn",
  });
  assert.equal(shortReason.ok, false);
  if (!shortReason.ok) assert.equal(shortReason.code, "INVALID_REASON");

  const longReason = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 1 }],
    reason: "x".repeat(501),
  });
  assert.equal(longReason.ok, false);
  if (!longReason.ok) assert.equal(longReason.code, "INVALID_REASON");

  const emptyItems = validateReturnPayload({
    items: [],
    reason: "Lý do hợp lệ đủ dài",
  });
  assert.equal(emptyItems.ok, false);
  if (!emptyItems.ok) assert.equal(emptyItems.code, "INVALID_RETURN_ITEMS");

  const badQty = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 0 }],
    reason: "Lý do hợp lệ đủ dài",
  });
  assert.equal(badQty.ok, false);
  if (!badQty.ok) assert.equal(badQty.code, "INVALID_RETURN_QTY");

  const floatQty = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 1.5 }],
    reason: "Lý do hợp lệ đủ dài",
  });
  assert.equal(floatQty.ok, false);

  const tooManyQty = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 100 }],
    reason: "Lý do hợp lệ đủ dài",
  });
  assert.equal(tooManyQty.ok, false);

  const xssPhoto = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 1 }],
    reason: "Lý do hợp lệ đủ dài",
    photos: ["javascript:alert(1)"],
  });
  assert.equal(xssPhoto.ok, false);
  if (!xssPhoto.ok) assert.equal(xssPhoto.code, "INVALID_PHOTO_URL");

  const httpPhoto = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 1 }],
    reason: "Lý do hợp lệ đủ dài",
    photos: ["http://external.example/photo.jpg"],
  });
  assert.equal(httpPhoto.ok, false);
  if (!httpPhoto.ok) assert.equal(httpPhoto.code, "INVALID_PHOTO_URL");

  const tooManyPhotos = validateReturnPayload({
    items: [{ sku_id: "s1", quantity: 1 }],
    reason: "Lý do hợp lệ đủ dài",
    photos: ["/1.jpg", "/2.jpg", "/3.jpg", "/4.jpg", "/5.jpg", "/6.jpg"],
  });
  assert.equal(tooManyPhotos.ok, false);
  if (!tooManyPhotos.ok) assert.equal(tooManyPhotos.code, "TOO_MANY_PHOTOS");
});

test("normalizeReturnStatus handles proto enum prefixes and maps Vietnamese metadata", () => {
  assert.equal(normalizeReturnStatus("RETURN_REQUEST_STATUS_APPROVED"), "APPROVED");
  assert.equal(normalizeReturnStatus("rejected"), "REJECTED");
  assert.equal(normalizeReturnStatus("REFUNDED"), "REFUNDED");
  assert.equal(normalizeReturnStatus("unknown"), "PENDING");

  assert.equal(RETURN_STATUS_META.PENDING.label, "Chờ gian hàng duyệt");
  assert.equal(RETURN_STATUS_META.APPROVED.label, "Đã duyệt đổi trả");
  assert.equal(RETURN_STATUS_META.REJECTED.label, "Gian hàng từ chối");
  assert.equal(RETURN_STATUS_META.RETURNING.label, "Đang gửi hoàn hàng");
  assert.equal(RETURN_STATUS_META.REFUNDED.label, "Đã hoàn tiền");
});

test("normalizeReturnRequests sorts newest first and strips unsafe photos", () => {
  const list = normalizeReturnRequests({
    returns: [
      {
        id: "ret-old",
        order_id: "o-1",
        status: "RETURN_REQUEST_STATUS_REJECTED",
        items: [{ order_item_id: "oi-1", sku_id: "s-1", quantity: 1 }],
        reason: "Cần đổi size",
        photos: ["javascript:evil()", "/products/a.png"],
        seller_note: "Sản phẩm đã cắt tem mác.",
        refund_amount_vnd: 0,
        created_at: "2026-11-18T08:00:00Z",
      },
      {
        id: "ret-new",
        order_id: "o-1",
        status: "REFUNDED",
        items: [{ sku_id: "s-1", quantity: 2 }],
        reason: "Lỗi đường may",
        evidence_urls: ["https://cdn.nexusmart.vn/proof.jpg"],
        refund_amount_vnd: 358000,
        reverse_tracking_code: "REV-9910",
        created_at: "2026-11-20T10:00:00Z",
      },
    ],
  });

  assert.equal(list.length, 2);
  assert.equal(list[0].id, "ret-new");
  assert.equal(list[0].status, "REFUNDED");
  assert.equal(list[0].refundAmountVnd, 358000);
  assert.equal(list[0].reverseTrackingCode, "REV-9910");
  assert.deepEqual(list[0].photos, ["https://cdn.nexusmart.vn/proof.jpg"]);

  assert.equal(list[1].id, "ret-old");
  assert.equal(list[1].status, "REJECTED");
  assert.equal(list[1].sellerNote, "Sản phẩm đã cắt tem mác.");
  assert.deepEqual(list[1].photos, ["/products/a.png"]);

  assert.equal(normalizeReturnRequest(null), null);
});
