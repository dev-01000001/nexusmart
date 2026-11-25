// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminAuditHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildAuditLogsQueryParams,
  filterAuditLogsClientSide,
  normalizeAuditTimeParam,
} from "./AdminAuditHelpers.ts";

test("normalizeAuditTimeParam converts YYYY-MM-DD in UTC+7 and rejects invalid calendar dates", () => {
  assert.equal(normalizeAuditTimeParam("2026-11-25", "start"), "2026-11-25T00:00:00+07:00");
  assert.equal(normalizeAuditTimeParam("2026-11-25", "end"), "2026-11-25T23:59:59+07:00");
  assert.equal(
    normalizeAuditTimeParam("2026-11-25T08:00:00Z", "start"),
    "2026-11-25T08:00:00.000Z"
  );
  assert.equal(normalizeAuditTimeParam("", "start"), "");
  assert.equal(normalizeAuditTimeParam("2026-02-30", "start"), null);
  assert.equal(normalizeAuditTimeParam("not-a-date", "end"), null);
});

test("buildAuditLogsQueryParams populates server filter params cleanly", () => {
  const qs = buildAuditLogsQueryParams({
    targetType: "refund",
    actorId: "usr-acc-1",
    targetId: "rf-99",
    fromDate: "2026-11-01",
    toDate: "2026-11-25",
    page: 2,
    pageSize: 20,
  });
  assert.equal(qs.get("target_type"), "refund");
  assert.equal(qs.get("actor_id"), "usr-acc-1");
  assert.equal(qs.get("target_id"), "rf-99");
  assert.equal(qs.get("from_time"), "2026-11-01T00:00:00+07:00");
  assert.equal(qs.get("to_time"), "2026-11-25T23:59:59+07:00");
  assert.equal(qs.get("page"), "2");
  assert.equal(qs.get("page_size"), "20");
});

test("filterAuditLogsClientSide filters by action/reason and staff role", () => {
  const sample = [
    {
      id: "1",
      actor_id: "a1",
      actor_role: "accountant",
      action: "admin.refund.approve",
      target_type: "refund",
      target_id: "rf-1",
      before_json: "",
      after_json: "",
      reason: "Đã kiểm tra sao kê",
      ip_address: "127.0.0.1",
    },
    {
      id: "2",
      actor_id: "a2",
      actor_role: "moderator",
      action: "admin.product.review",
      target_type: "product",
      target_id: "p-1",
      before_json: "",
      after_json: "",
      reason: "Đạt chuẩn",
      ip_address: "127.0.0.1",
    },
  ];
  assert.equal(
    filterAuditLogsClientSide(sample, { actionQuery: "hoàn tiền", roleFilter: "all" }).length,
    1
  );
  assert.equal(
    filterAuditLogsClientSide(sample, { actionQuery: "", roleFilter: "moderator" }).length,
    1
  );
  assert.equal(
    filterAuditLogsClientSide(sample, { actionQuery: "product", roleFilter: "accountant" }).length,
    0
  );
});
