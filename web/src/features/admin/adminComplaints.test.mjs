// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/adminComplaints.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canModifyUserAccount,
  formatCommissionBps,
  getComplaintDecisionLabelVi,
  getComplaintStatusMeta,
  isComplaintResolved,
  normalizeComplaint,
  normalizeComplaintsListResponse,
  slugifyCategoryName,
  validateAdminVoucherInput,
  validateCategoryInput,
  validateComplaintResolution,
} from "./adminComplaintsTypes.ts";

test("normalizeComplaint sanitizes evidence photo URLs and maps proto + extended DTO fields", () => {
  const c = normalizeComplaint({
    id: "cmp-9f8e7d6c-1234",
    return_request_id: "ret-1",
    order_id: "ord-aabbccdd",
    order_code: "DH-20261121-001",
    buyer_id: "usr-buyer-01",
    buyer_name: "Trần Thị Minh Anh",
    shop_id: "shop-01",
    shop_name: "Xưởng Len Hội An",
    status: "COMPLAINT_STATUS_OPEN",
    dispute_amount_vnd: 485000.9,
    reason: "Áo nhận được bị rách đường chỉ ở vai phải",
    seller_note: "Shop kiểm tra camera đóng gói thấy sản phẩm nguyên vẹn",
    evidence_urls: [
      "https://cdn.nexusmart.vn/evidence/photo-1.jpg",
      "/uploads/return-proof.png",
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "http://insecure.example.com/img.jpg",
      "//protocol-relative.evil/x.png",
    ],
    created_at: "2026-11-21T08:30:00Z",
    timeline: [
      {
        id: "t1",
        actor_role: "buyer",
        actor_name: "Trần Thị Minh Anh",
        action: "Mở khiếu nại",
        note: "Đính kèm 2 ảnh mở hộp",
        created_at: "2026-11-21T08:30:00Z",
      },
    ],
  });

  assert.equal(c.id, "cmp-9f8e7d6c-1234");
  assert.equal(c.complaint_code, "KN-CMP-9F8E");
  assert.equal(c.order_code, "DH-20261121-001");
  assert.equal(c.buyer_name, "Trần Thị Minh Anh");
  assert.equal(c.shop_name, "Xưởng Len Hội An");
  assert.equal(c.status, "OPEN");
  assert.equal(c.dispute_amount_vnd, 485000);
  assert.equal(c.buyer_reason, "Áo nhận được bị rách đường chỉ ở vai phải");
  assert.equal(
    c.seller_rejection_reason,
    "Shop kiểm tra camera đóng gói thấy sản phẩm nguyên vẹn"
  );
  assert.deepEqual(c.evidence_urls, [
    "https://cdn.nexusmart.vn/evidence/photo-1.jpg",
    "/uploads/return-proof.png",
  ]);
  assert.equal(c.timeline.length, 1);
  assert.equal(c.timeline[0].actor_name, "Trần Thị Minh Anh");
});

test("normalizeComplaintsListResponse supports items, complaints, and raw array shapes", () => {
  const fromComplaints = normalizeComplaintsListResponse({
    complaints: [{ id: "KN-101", status: "IN_REVIEW", amount_vnd: 120000 }],
    total: 14,
  });
  assert.equal(fromComplaints.items.length, 1);
  assert.equal(fromComplaints.items[0].complaint_code, "KN-101");
  assert.equal(fromComplaints.items[0].status, "IN_REVIEW");
  assert.equal(fromComplaints.items[0].dispute_amount_vnd, 120000);
  assert.equal(fromComplaints.total, 14);

  const fromItems = normalizeComplaintsListResponse({
    items: [{ id: "cmp-2", status: "RESOLVED", resolution: "COMPLAINT_RESOLUTION_REFUND" }],
  });
  assert.equal(fromItems.total, 1);
  assert.equal(fromItems.items[0].decision, "REFUND");
});

test("getComplaintStatusMeta and resolution state helpers return Vietnamese labels & tones", () => {
  assert.deepEqual(getComplaintStatusMeta("OPEN"), { label: "Chờ xử lý", tone: "warning" });
  assert.deepEqual(getComplaintStatusMeta("IN_REVIEW"), {
    label: "Đang xem xét",
    tone: "neutral",
  });
  assert.deepEqual(getComplaintStatusMeta("RESOLVED"), {
    label: "Đã giải quyết",
    tone: "positive",
  });
  assert.deepEqual(getComplaintStatusMeta("COMPLAINT_STATUS_DISMISSED"), {
    label: "Đã bác khiếu nại",
    tone: "negative",
  });

  assert.equal(isComplaintResolved("OPEN"), false);
  assert.equal(isComplaintResolved("IN_REVIEW"), false);
  assert.equal(isComplaintResolved("RESOLVED"), true);
  assert.equal(isComplaintResolved("DISMISSED"), true);

  assert.equal(getComplaintDecisionLabelVi("REFUND_BUYER"), "Hoàn toàn bộ tiền cho người mua");
  assert.equal(getComplaintDecisionLabelVi("APPROVE_RETURN"), "Chấp thuận trả hàng");
  assert.equal(getComplaintDecisionLabelVi("PARTIAL_REFUND"), "Hoàn một phần tiền");
  assert.equal(getComplaintDecisionLabelVi("REJECT_COMPLAINT"), "Bác khiếu nại");
});

test("validateComplaintResolution enforces decisions, note length, and integer VND partial refund", () => {
  const validRefund = validateComplaintResolution({
    decision: "REFUND_BUYER",
    note: "Ảnh mở hộp xác nhận sản phẩm rách từ trước khi giao.",
  });
  assert.equal(validRefund.ok, true);
  if (validRefund.ok) {
    assert.equal(validRefund.value.decision, "REFUND_BUYER");
    assert.equal(validRefund.value.refund_amount_vnd, undefined);
  }

  const validPartialZero = validateComplaintResolution({
    decision: "PARTIAL_REFUND",
    refund_amount_vnd: 0,
    note: "Hai bên đồng thuận giảm 0đ và giữ voucher hỗ trợ.",
  });
  assert.equal(validPartialZero.ok, true);

  const validPartial = validateComplaintResolution({
    decision: "PARTIAL_REFUND",
    refund_amount_vnd: 125000,
    note: "Hoàn một phần tiền lỗi đường may nhỏ theo thỏa thuận.",
  });
  assert.equal(validPartial.ok, true);
  if (validPartial.ok) {
    assert.equal(validPartial.value.refund_amount_vnd, 125000);
  }

  const missingPartial = validateComplaintResolution({
    decision: "PARTIAL_REFUND",
    note: "Thiếu số tiền hoàn trả một phần.",
  });
  assert.equal(missingPartial.ok, false);
  if (!missingPartial.ok) assert.equal(missingPartial.code, "INVALID_REFUND_AMOUNT");

  const floatPartial = validateComplaintResolution({
    decision: "PARTIAL_REFUND",
    refund_amount_vnd: 50000.5,
    note: "Không được nhập số thập phân VND.",
  });
  assert.equal(floatPartial.ok, false);

  const negativePartial = validateComplaintResolution({
    decision: "PARTIAL_REFUND",
    refund_amount_vnd: -1000,
    note: "Không được nhập số âm VND.",
  });
  assert.equal(negativePartial.ok, false);

  const shortNote = validateComplaintResolution({
    decision: "REJECT_COMPLAINT",
    note: "Ok",
  });
  assert.equal(shortNote.ok, false);
  if (!shortNote.ok) assert.equal(shortNote.code, "NOTE_TOO_SHORT");

  const badDecision = validateComplaintResolution({
    decision: "FREE_MONEY",
    note: "Quyết định không có trong danh sách hợp lệ.",
  });
  assert.equal(badDecision.ok, false);
  if (!badDecision.ok) assert.equal(badDecision.code, "INVALID_DECISION");
});

test("formatCommissionBps formats basis points as Vietnamese percentage strings", () => {
  assert.equal(formatCommissionBps(0), "0,00%");
  assert.equal(formatCommissionBps(500), "5,00%");
  assert.equal(formatCommissionBps(1250), "12,50%");
  assert.equal(formatCommissionBps(5000), "50,00%");
  assert.equal(formatCommissionBps(-200), "0,00%");
  assert.equal(formatCommissionBps(Number.NaN), "0,00%");
});

test("validateCategoryInput and slugifyCategoryName validate category creation and fee bounds", () => {
  assert.equal(slugifyCategoryName("Áo Thun Đồng Phục Đẹp"), "ao-thun-dong-phuc-dep");

  const ok = validateCategoryInput({
    name: "Áo khoác gió",
    slug: "ao-khoac-gio",
    parent_id: "cat-root-1",
    commission_rate_bps: 650,
    sort_order: 3,
  });
  assert.equal(ok.ok, true);
  if (ok.ok) {
    assert.deepEqual(ok.value, {
      name: "Áo khoác gió",
      slug: "ao-khoac-gio",
      parent_id: "cat-root-1",
      commission_rate_bps: 650,
      sort_order: 3,
    });
  }

  const maxEdge = validateCategoryInput({
    name: "Hàng cao cấp",
    slug: "hang-cao-cap",
    commission_rate_bps: 5000,
  });
  assert.equal(maxEdge.ok, true);
  if (maxEdge.ok) assert.equal(maxEdge.value.sort_order, 0);

  const tooHighBps = validateCategoryInput({
    name: "Danh mục vượt trần",
    slug: "vuot-tran",
    commission_rate_bps: 5001,
  });
  assert.equal(tooHighBps.ok, false);
  if (!tooHighBps.ok) assert.equal(tooHighBps.code, "INVALID_COMMISSION_BPS");

  const floatBps = validateCategoryInput({
    name: "Danh mục lẻ",
    slug: "danh-muc-le",
    commission_rate_bps: 500.5,
  });
  assert.equal(floatBps.ok, false);

  const badSlug = validateCategoryInput({
    name: "Áo sơ mi",
    slug: "Áo Sơ Mi!",
    commission_rate_bps: 400,
  });
  assert.equal(badSlug.ok, false);
  if (!badSlug.ok) assert.equal(badSlug.code, "INVALID_CATEGORY_SLUG");

  const negativeSort = validateCategoryInput({
    name: "Áo sơ mi",
    slug: "ao-so-mi",
    commission_rate_bps: 400,
    sort_order: -1,
  });
  assert.equal(negativeSort.ok, false);
});

test("validateAdminVoucherInput validates PLATFORM and FREESHIP integer-VND vouchers", () => {
  const fixedPlatform = validateAdminVoucherInput({
    code: "nexus50k",
    name: "Giảm 50.000đ đơn từ 300.000đ",
    scope: "PLATFORM",
    discount_type: "FIXED",
    value_vnd: 50000,
    min_order_vnd: 300000,
    quota: 1000,
    per_user_limit: 2,
    budget_vnd: 50000000,
    starts_at: "2026-11-21T00:00:00Z",
    ends_at: "2026-12-01T23:59:59Z",
  });
  assert.equal(fixedPlatform.ok, true);
  if (fixedPlatform.ok) {
    assert.equal(fixedPlatform.value.code, "NEXUS50K");
    assert.equal(fixedPlatform.value.value_vnd, 50000);
    assert.equal(fixedPlatform.value.scope, "PLATFORM");
  }

  const percentFreeship = validateAdminVoucherInput({
    code: "FREESHIP20",
    name: "Giảm 20% phí vận chuyển tối đa 25.000đ",
    scope: "FREESHIP",
    discount_type: "PERCENT",
    discount_percent: 20,
    max_discount_vnd: 25000,
    min_order_vnd: 99000,
    quota: 500,
    per_user_limit: 1,
    starts_at: "2026-11-21T00:00:00Z",
    ends_at: "2026-11-30T23:59:59Z",
  });
  assert.equal(percentFreeship.ok, true);
  if (percentFreeship.ok) {
    assert.equal(percentFreeship.value.discount_percent, 20);
    assert.equal(percentFreeship.value.max_discount_vnd, 25000);
  }

  const rejectedShopScope = validateAdminVoucherInput({
    code: "SHOP10K",
    name: "Voucher riêng của shop",
    scope: "SHOP",
    discount_type: "FIXED",
    value_vnd: 10000,
    quota: 100,
    starts_at: "2026-11-21T00:00:00Z",
    ends_at: "2026-11-25T00:00:00Z",
  });
  assert.equal(rejectedShopScope.ok, false);
  if (!rejectedShopScope.ok) assert.equal(rejectedShopScope.code, "INVALID_VOUCHER_SCOPE");

  const floatVnd = validateAdminVoucherInput({
    code: "FLOAT10",
    name: "Sai định dạng VND",
    scope: "PLATFORM",
    discount_type: "FIXED",
    value_vnd: 10000.25,
    quota: 100,
    starts_at: "2026-11-21T00:00:00Z",
    ends_at: "2026-11-25T00:00:00Z",
  });
  assert.equal(floatVnd.ok, false);
  if (!floatVnd.ok) assert.equal(floatVnd.code, "INVALID_VALUE_VND");

  const percentWithoutCap = validateAdminVoucherInput({
    code: "PCTNOCAP",
    name: "Thiếu trần giảm tối đa",
    scope: "PLATFORM",
    discount_type: "PERCENT",
    discount_percent: 15,
    max_discount_vnd: 0,
    quota: 50,
    starts_at: "2026-11-21T00:00:00Z",
    ends_at: "2026-11-25T00:00:00Z",
  });
  assert.equal(percentWithoutCap.ok, false);
  if (!percentWithoutCap.ok) assert.equal(percentWithoutCap.code, "INVALID_MAX_DISCOUNT_VND");

  const invertedWindow = validateAdminVoucherInput({
    code: "BADTIME",
    name: "Hết hạn trước khi bắt đầu",
    scope: "PLATFORM",
    discount_type: "FIXED",
    value_vnd: 20000,
    quota: 50,
    starts_at: "2026-11-25T00:00:00Z",
    ends_at: "2026-11-21T00:00:00Z",
  });
  assert.equal(invertedWindow.ok, false);
  if (!invertedWindow.ok) assert.equal(invertedWindow.code, "INVALID_VOUCHER_WINDOW");

  const perUserExceedsQuota = validateAdminVoucherInput({
    code: "LIMITERR",
    name: "Giới hạn mỗi người vượt tổng lượt",
    scope: "FREESHIP",
    discount_type: "FIXED",
    value_vnd: 15000,
    quota: 5,
    per_user_limit: 10,
    starts_at: "2026-11-21T00:00:00Z",
    ends_at: "2026-11-25T00:00:00Z",
  });
  assert.equal(perUserExceedsQuota.ok, false);
  if (!perUserExceedsQuota.ok) assert.equal(perUserExceedsQuota.code, "INVALID_PER_USER_LIMIT");
});

test("canModifyUserAccount blocks self-lock and self-role edits while permitting other accounts", () => {
  const selfStatus = canModifyUserAccount("usr-admin-1", "usr-admin-1", "status");
  assert.equal(selfStatus.allowed, false);
  assert.ok(selfStatus.reason && selfStatus.reason.includes("tự khóa"));

  const selfRole = canModifyUserAccount("usr-admin-1", "usr-admin-1", "role");
  assert.equal(selfRole.allowed, false);
  assert.ok(selfRole.reason && selfRole.reason.includes("vai trò"));

  const emptyTarget = canModifyUserAccount("usr-admin-1", "  ", "status");
  assert.equal(emptyTarget.allowed, false);

  const otherUser = canModifyUserAccount("usr-admin-1", "usr-buyer-99", "status");
  assert.deepEqual(otherUser, { allowed: true });
});
