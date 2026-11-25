import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayVoucher } from "@/lib/gateway-types";
import {
  validateAdminVoucherInput,
  type AdminVoucher,
} from "@/features/admin/adminComplaintsTypes";

const ALLOWED_SCOPES = new Set(["PLATFORM", "FREESHIP", "SHOP"]);
const ALLOWED_STATUSES = new Set(["ACTIVE", "PAUSED", "DELETED"]);

export const GET = apiRoute(
  async (req) => {
    await assertCapability("vouchers.read");
    const q = req.nextUrl.searchParams;
    const rawScope = (q.get("scope") ?? "").trim().toUpperCase();
    if (rawScope && !ALLOWED_SCOPES.has(rawScope)) {
      throw new GatewayError("invalid", "INVALID_VOUCHER_SCOPE");
    }
    const rawStatus = (q.get("status") ?? "").trim().toUpperCase();
    if (rawStatus && !ALLOWED_STATUSES.has(rawStatus)) {
      throw new GatewayError("invalid", "INVALID_VOUCHER_STATUS");
    }
    const includeInactive = (q.get("include_inactive") ?? "").trim().toLowerCase();

    const data = await gw<{ vouchers: GatewayVoucher[] }>("/api/v1/admin/vouchers", {
      query: {
        scope: rawScope || undefined,
        status: rawStatus || undefined,
        include_inactive:
          includeInactive === "true" || includeInactive === "1" ? "true" : undefined,
      },
    });
    return Response.json(
      { vouchers: data.vouchers ?? [] },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_VOUCHER_SCOPE: "Phạm vi voucher không hợp lệ.",
      INVALID_VOUCHER_STATUS: "Trạng thái voucher không hợp lệ.",
    },
  }
);

export const POST = apiRoute(
  async (req) => {
    await assertCapability("vouchers.write");
    const raw = await readJson<unknown>(req);
    const parsed = validateAdminVoucherInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code);
    }

    const data = await gw<{ voucher: AdminVoucher }>("/api/v1/admin/vouchers", {
      method: "POST",
      body: parsed.value,
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_VOUCHER_CODE:
        "Mã voucher cần từ 3 đến 32 ký tự gồm chữ in hoa, số, gạch ngang hoặc gạch dưới.",
      INVALID_VOUCHER_NAME: "Tên chương trình voucher cần từ 2 đến 160 ký tự.",
      INVALID_VOUCHER_SCOPE: "Phạm vi voucher quản trị chỉ hỗ trợ PLATFORM hoặc FREESHIP.",
      INVALID_DISCOUNT_TYPE: "Loại giảm giá phải là FIXED hoặc PERCENT.",
      INVALID_VALUE_VND: "Mức giảm cố định phải là số nguyên VND lớn hơn 0.",
      INVALID_DISCOUNT_PERCENT: "Phần trăm giảm giá phải là số nguyên từ 1 đến 100.",
      INVALID_MAX_DISCOUNT_VND: "Mức giảm tối đa phải là số nguyên VND hợp lệ.",
      INVALID_MIN_ORDER_VND: "Giá trị đơn tối thiểu phải là số nguyên VND không âm.",
      INVALID_QUOTA: "Tổng số lượt sử dụng phải là số nguyên lớn hơn hoặc bằng 1.",
      INVALID_PER_USER_LIMIT: "Giới hạn mỗi người phải nằm trong khoảng từ 1 đến tổng lượt.",
      INVALID_BUDGET_VND: "Ngân sách tối đa phải là số nguyên VND không âm.",
      INVALID_VOUCHER_WINDOW: "Thời gian bắt đầu và kết thúc voucher chưa hợp lệ.",
    },
  }
);
