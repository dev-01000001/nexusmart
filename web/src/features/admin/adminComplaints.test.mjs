// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/admin/adminComplaints.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canModifyUserAccount,
  formatCommissionBps,
  getComplaintResolutionLabelVi,
  getComplaintStatusMeta,
  isComplaintResolved,
  normalizeComplaint,
  normalizeComplaintsListResponse,
  slugifyCategoryName,
  validateAdminVoucherInput,
  validateCategoryInput,
  validateComplaintResolution,
} from "./adminComplaintsTypes.ts";

test("normalizeComplaint sanitizes evidence photo URLs and maps gateway complaintDTO fields", () => {
  const c = normalizeComplaint({
    id: "cmp-9f8e7d6c-1234",
    return_request_id: "ret-1",
    order_id: "ord-aabbccdd",
    buyer_id: "usr-buyer-01",
    shop_id: "shop-01",
    reason: "Áo nhận được bị rách đường chỉ ở vai phải",
    evidence_urls: [
      "https://cdn.nexusmart.vn/evidence/photo-1.jpg",
      "/uploads/return-proof.png",
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "http://insecure.example.com/img.jpg",
      "//protocol-relative.evil/x.png",
    ],
    status: "OPEN",
    resolution: "",
    admin_note: "",
    resolved_by: "",
    created_at: "2026-11-21T08:30:00Z",
    resolved_at: "",
  });

  assert.equal(c.id, "cmp-9f8e7d6c-1234");
  assert.equal(c.complaint_code, "KN-CMP-9F8E");
  assert.equal(c.order_code, "DH-ORD-AABB");
  assert.equal(c.buyer_name, "Người mua usr-buye");
  assert.equal(c.shop_name, "Gian hàng shop-01");
  assert.equal(c.status, "OPEN");
  assert.equal(c.buyer_reason, "Áo nhận được bị rách đường chỉ ở vai phải");
  assert.deepEqual(c.evidence_urls, [
    "https://cdn.nexusmart.vn/evidence/photo-1.jpg",
    "/uploads/return-proof.png",
  ]);
});

test("normalizeComplaintsListResponse reads gateway `items` strictly without dual-key fallback", () => {
  const fromItems = normalizeComplaintsListResponse({
    items: [
      {
        id: "cmp-1",
        return_request_id: "ret-1",
        order_id: "ord-1",
        buyer_id: "buyer-1",
        shop_id: "shop-42",
        reason: "Defective item",
        evidence_urls: [],
        status: "RESOLVED",
        resolution: "REFUND",
        admin_note: "Approved in buyer favor",
        resolved_by: "admin-1",
        created_at: "2026-11-21T08:30:00Z",
        resolved_at: "2026-11-21T09:00:00Z",
      },
    ],
    page: 1,
    page_size: 20,
    total: 1,
  });
  assert.equal(fromItems.total, 1);
  assert.equal(fromItems.items.length, 1);
  assert.equal(fromItems.items[0].resolution, "REFUND");
  assert.equal(fromItems.items[0].resolution_note, "Approved in buyer favor");

  // Legacy `complaints` key must NOT be read as a fallback.
  const wrongKey = normalizeComplaintsListResponse({
    complaints: [{ id: "KN-101", status: "OPEN" }],
    total: 1,
  });
  assert.deepEqual(wrongKey.items, []);
});

test("getComplaintStatusMeta and resolution state helpers return Vietnamese labels & tones", () => {
  assert.deepEqual(getComplaintStatusMeta("OPEN"), { label: "Chờ xử lý", tone: "warning" });
  assert.deepEqual(getComplaintStatusMeta("RESOLVED"), {
    label: "Đã giải quyết",
    tone: "positive",
  });
  assert.deepEqual(getComplaintStatusMeta("DISMISSED"), {
    label: "Đã bác khiếu nại",
    tone: "negative",
  });

  assert.equal(isComplaintResolved("OPEN"), false);
  assert.equal(isComplaintResolved("RESOLVED"), true);
  assert.equal(isComplaintResolved("DISMISSED"), true);

  assert.equal(getComplaintResolutionLabelVi("REFUND"), "Hoàn tiền cho người mua");
  assert.equal(getComplaintResolutionLabelVi("REPLACE"), "Đổi sản phẩm thay thế");
  assert.equal(getComplaintResolutionLabelVi("DISMISS"), "Bác khiếu nại");
});

test("validateComplaintResolution enforces gateway {resolution, note, require_return_shipping} contract", () => {
  const immediateRefund = validateComplaintResolution({
    resolution: "REFUND",
    note: "Ảnh mở hộp xác nhận sản phẩm rách từ trước khi giao.",
    require_return_shipping: false,
  });
  assert.equal(immediateRefund.ok, true);
  if (immediateRefund.ok) {
    assert.deepEqual(immediateRefund.value, {
      resolution: "REFUND",
      note: "Ảnh mở hộp xác nhận sản phẩm rách từ trước khi giao.",
      require_return_shipping: false,
    });
  }

  const returnThenRefund = validateComplaintResolution({
    resolution: "refund",
    note: "Người mua cần gửi trả hàng trước khi hoàn tiền.",
    require_return_shipping: true,
  });
  assert.equal(returnThenRefund.ok, true);
  if (returnThenRefund.ok) {
    assert.deepEqual(returnThenRefund.value, {
      resolution: "REFUND",
      note: "Người mua cần gửi trả hàng trước khi hoàn tiền.",
      require_return_shipping: true,
    });
  }

  const replace = validateComplaintResolution({
    resolution: "REPLACE",
    note: "Đổi sản phẩm mới đúng kích cỡ đã đặt.",
    require_return_shipping: true,
  });
  assert.equal(replace.ok, true);
  if (replace.ok) {
    assert.deepEqual(replace.value, {
      resolution: "REPLACE",
      note: "Đổi sản phẩm mới đúng kích cỡ đã đặt.",
      require_return_shipping: true,
    });
  }

  const dismiss = validateComplaintResolution({
    resolution: "DISMISS",
    note: "Bằng chứng không đủ cơ sở để chấp thuận khiếu nại.",
    require_return_shipping: true,
  });
  assert.equal(dismiss.ok, true);
  if (dismiss.ok) {
    assert.deepEqual(dismiss.value, {
      resolution: "DISMISS",
      note: "Bằng chứng không đủ cơ sở để chấp thuận khiếu nại.",
      require_return_shipping: false,
    });
  }

  // Unsupported / legacy decisions & dual-key `decision` must be rejected.
  for (const invalidInput of [
    { resolution: "PARTIAL_REFUND", note: "Không hỗ trợ hoàn một phần trên gateway." },
    { resolution: "REFUND_BUYER", note: "Legacy enum không được hỗ trợ." },
    { resolution: "APPROVE_RETURN", note: "Legacy enum không được hỗ trợ." },
    { resolution: "REJECT_COMPLAINT", note: "Legacy enum không được hỗ trợ." },
    { decision: "DISMISS", note: "Không dùng khóa decision cũ." },
    { resolution: "FREE_MONEY", note: "Quyết định không có trong danh sách hợp lệ." },
  ]) {
    const res = validateComplaintResolution(invalidInput);
    assert.equal(res.ok, false);
    if (!res.ok) assert.equal(res.code, "INVALID_RESOLUTION");
  }

  const badReturnFlag = validateComplaintResolution({
    resolution: "REFUND",
    note: "Kiểm tra kiểu boolean của require_return_shipping.",
    require_return_shipping: "yes",
  });
  assert.equal(badReturnFlag.ok, false);
  if (!badReturnFlag.ok) assert.equal(badReturnFlag.code, "INVALID_RETURN_SHIPPING");

  const shortNote = validateComplaintResolution({
    resolution: "DISMISS",
    note: "Ok",
  });
  assert.equal(shortNote.ok, false);
  if (!shortNote.ok) assert.equal(shortNote.code, "NOTE_TOO_SHORT");
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
