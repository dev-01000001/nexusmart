// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/AdminAuditHelpers.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildAuditLogsQueryParams,
  filterAuditLogsClientSide,
  getAdminAuditActionLabelVi,
  normalizeAuditTimeParam,
} from "./AdminAuditHelpers.ts";
import {
  ADMIN_AUDIT_TARGET_TYPE_LABELS,
  getAdminAuditTargetTypeLabel,
} from "./types.ts";

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

test("ADMIN_AUDIT_TARGET_TYPE_LABELS matches real gateway auditTargetType and services/user target_types without dead filter keys", () => {
  // Dead keys that gateway & services/user never write (previously caused 0-row filter results)
  for (const deadKey of ["payment_method", "shop_approval", "staff"]) {
    assert.equal(
      Object.prototype.hasOwnProperty.call(ADMIN_AUDIT_TARGET_TYPE_LABELS, deadKey),
      false,
      `dead target_type key "${deadKey}" must not be in filter options`
    );
  }

  // Every real target_type written by gateway middleware/audit.go:346-406 and services/user
  const realTargetTypes = [
    "shop",
    "site",
    "domain",
    "user",
    "product",
    "category",
    "banned_keyword",
    "voucher",
    "flash_sale",
    "flash_sale_item",
    "coin",
    "order",
    "payment",
    "refund",
    "reconciliation_item",
    "reconciliation_run",
    "return_request",
    "carrier",
    "payment_method_config",
    "shop_suspension_approval",
    "shop_bank_account",
    "category_fee_schedule",
    "payment_fee_config",
    "search_reindex",
    "admin",
  ];
  for (const t of realTargetTypes) {
    assert.ok(
      ADMIN_AUDIT_TARGET_TYPE_LABELS[t],
      `missing target_type "${t}" in ADMIN_AUDIT_TARGET_TYPE_LABELS`
    );
    if (t !== "admin") {
      assert.notEqual(
        getAdminAuditTargetTypeLabel(t),
        "Hệ thống quản trị",
        `target_type "${t}" must not fall back to generic "Hệ thống quản trị"`
      );
    }
  }
});

test("getAdminAuditActionLabelVi localizes real gateway METHOD /api/v1/admin/... actions and services/user actions", () => {
  // Real gateway middleware/audit.go:227 action strings
  const gatewayActions = [
    "POST /api/v1/admin/approvals/refund/{id}/decide",
    "POST /api/v1/admin/categories",
    "PUT /api/v1/admin/categories/{id}",
    "DELETE /api/v1/admin/categories/{id}",
    "POST /api/v1/admin/fees/categories/{id}",
    "POST /api/v1/admin/products/{id}/review",
    "POST /api/v1/admin/banned-keywords",
    "PUT /api/v1/admin/banned-keywords/{id}",
    "DELETE /api/v1/admin/banned-keywords/{id}",
    "POST /api/v1/admin/banned-keywords/preview",
    "POST /api/v1/admin/banned-keywords/rescan",
    "POST /api/v1/admin/vouchers",
    "PUT /api/v1/admin/vouchers/{id}",
    "PATCH /api/v1/admin/vouchers/{id}/status",
    "DELETE /api/v1/admin/vouchers/{id}",
    "POST /api/v1/admin/flash-sales",
    "POST /api/v1/admin/flash-sales/{id}/end",
    "POST /api/v1/admin/flash-sale-items/{item_id}/approve",
    "POST /api/v1/admin/coins/credit",
    "POST /api/v1/admin/orders/{id}/resolve-review",
    "POST /api/v1/admin/returns/{id}/resolve",
    "PUT /api/v1/admin/payment-methods/{method}",
    "POST /api/v1/admin/fees/payment",
    "POST /api/v1/admin/payments/{id}/refund",
    "POST /api/v1/admin/refunds/{id}/settle",
    "POST /api/v1/admin/payments/reconcile",
    "POST /api/v1/admin/reconciliation/cod/import",
    "POST /api/v1/admin/reconciliation/queue/{id}/resolve",
    "PUT /api/v1/admin/carriers/{code}",
    "POST /api/v1/admin/carriers/{code}/test",
    "POST /api/v1/admin/search/reindex",
  ];
  for (const act of gatewayActions) {
    const label = getAdminAuditActionLabelVi(act);
    assert.notEqual(label, act, `gateway action "${act}" must be translated to Vietnamese`);
    assert.equal(label.includes("/api/v1/"), false, `label for "${act}" must not leak raw route`);
  }

  // Real services/user InsertAuditLog action strings
  const userServiceActions = [
    "admin.user.update_status",
    "admin.user.update_role",
    "admin.staff.create",
    "admin.shop.review",
    "admin.shop.suspend_request",
    "admin.shop.suspend_approve",
    "admin.shop.suspend_reject",
    "admin.shop.unsuspend",
    "admin.shop_bank_account.review",
    "admin.site.suspend",
    "admin.site.unsuspend",
    "site.save_draft",
    "site.publish",
    "site.rollback",
    "site.apply_template",
    "site.domain.add",
    "site.domain.check",
    "site.domain.activated",
    "site.domain.remove",
    "seller.shop_bank_account.add",
    "seller.shop_bank_account.verify_otp",
    "user.phone.verify_otp",
  ];
  for (const act of userServiceActions) {
    const label = getAdminAuditActionLabelVi(act);
    assert.notEqual(label, act, `user service action "${act}" must be translated to Vietnamese`);
  }

  // Real gateway JSON audit log fixture searchable by Vietnamese label
  const gwLogs = [
    {
      id: "al-1",
      actor_id: "usr-mod-1",
      actor_role: "moderator",
      action: "POST /api/v1/admin/banned-keywords",
      target_type: "banned_keyword",
      target_id: "kw-1",
      before_json: "{}",
      after_json: '{"keyword":"fake"}',
      reason: "",
      ip_address: "10.0.0.1",
      created_at: "2026-11-25T10:00:00Z",
    },
    {
      id: "al-2",
      actor_id: "usr-acc-1",
      actor_role: "accountant",
      action: "admin.shop_bank_account.review",
      target_type: "shop_bank_account",
      target_id: "ba-1",
      before_json: "{}",
      after_json: '{"status":"VERIFIED"}',
      reason: "",
      ip_address: "10.0.0.2",
      created_at: "2026-11-25T11:00:00Z",
    },
  ];
  assert.equal(
    filterAuditLogsClientSide(gwLogs, { actionQuery: "từ khóa cấm", roleFilter: "all" }).length,
    1
  );
  assert.equal(
    filterAuditLogsClientSide(gwLogs, { actionQuery: "ngân hàng", roleFilter: "accountant" })
      .length,
    1
  );
});
