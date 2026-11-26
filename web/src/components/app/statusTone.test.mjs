// Run: cd web && node --test src/components/app/statusTone.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bankAccountStatusTone,
  orderStatusTone,
  productStatusTone,
  shipmentJobTone,
  siteStatusTone,
} from "./statusTone.ts";

test("orderStatusTone maps documented order statuses case-insensitively with neutral fallback", () => {
  assert.equal(orderStatusTone("PENDING"), "warning");
  assert.equal(orderStatusTone("CONFIRMED"), "attention");
  assert.equal(orderStatusTone("PREPARING"), "attention");
  assert.equal(orderStatusTone("READY_TO_SHIP"), "info");
  assert.equal(orderStatusTone("SHIPPING"), "info");
  assert.equal(orderStatusTone("DELIVERED"), "success");
  assert.equal(orderStatusTone("COMPLETED"), "success");
  assert.equal(orderStatusTone("DELIVERY_FAILED"), "critical");
  assert.equal(orderStatusTone("CANCELLED"), "critical");
  assert.equal(orderStatusTone("RETURN_REQUESTED"), "warning");
  assert.equal(orderStatusTone("RETURNED"), "neutral");
  assert.equal(orderStatusTone("REFUNDED"), "neutral");

  assert.equal(orderStatusTone("shipping"), "info");
  assert.equal(orderStatusTone("  completed "), "success");
  assert.equal(orderStatusTone(null), "neutral");
  assert.equal(orderStatusTone(undefined), "neutral");
  assert.equal(orderStatusTone(""), "neutral");
  assert.equal(orderStatusTone("FOO"), "neutral");
});

test("shipmentJobTone maps QUEUED and FAILED with neutral fallback", () => {
  assert.equal(shipmentJobTone("QUEUED"), "info");
  assert.equal(shipmentJobTone("FAILED"), "critical");
  assert.equal(shipmentJobTone("queued"), "info");
  assert.equal(shipmentJobTone("failed"), "critical");
  assert.equal(shipmentJobTone(null), "neutral");
  assert.equal(shipmentJobTone(""), "neutral");
  assert.equal(shipmentJobTone("UNKNOWN"), "neutral");
});

test("productStatusTone maps seller product statuses with neutral fallback", () => {
  assert.equal(productStatusTone("APPROVED"), "success");
  assert.equal(productStatusTone("PENDING"), "attention");
  assert.equal(productStatusTone("REJECTED"), "critical");
  assert.equal(productStatusTone("OUT_OF_STOCK"), "warning");
  assert.equal(productStatusTone("HIDDEN"), "neutral");
  assert.equal(productStatusTone("approved"), "success");
  assert.equal(productStatusTone(null), "neutral");
  assert.equal(productStatusTone(""), "neutral");
  assert.equal(productStatusTone("FOO"), "neutral");
});

test("bankAccountStatusTone maps verification statuses with neutral fallback", () => {
  assert.equal(bankAccountStatusTone("VERIFIED"), "success");
  assert.equal(bankAccountStatusTone("PENDING_OTP"), "attention");
  assert.equal(bankAccountStatusTone("PENDING_ADMIN_REVIEW"), "attention");
  assert.equal(bankAccountStatusTone("REJECTED"), "critical");
  assert.equal(bankAccountStatusTone("verified"), "success");
  assert.equal(bankAccountStatusTone(null), "neutral");
  assert.equal(bankAccountStatusTone(""), "neutral");
  assert.equal(bankAccountStatusTone("FOO"), "neutral");
});

test("siteStatusTone maps SiteStatus values with neutral fallback", () => {
  assert.equal(siteStatusTone("PUBLISHED"), "success");
  assert.equal(siteStatusTone("DRAFT"), "info");
  assert.equal(siteStatusTone("SUSPENDED"), "critical");
  assert.equal(siteStatusTone("published"), "success");
  assert.equal(siteStatusTone(null), "neutral");
  assert.equal(siteStatusTone(""), "neutral");
  assert.equal(siteStatusTone("FOO"), "neutral");
});
