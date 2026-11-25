import type { GatewayCreateStaffResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { validateCreateStaffInput } from "@/features/admin/AdminConfigValidators";

export const POST = apiRoute(
  async (req) => {
    await assertCapability("staff.create");
    const raw = await readJson<unknown>(req);
    const parsed = validateCreateStaffInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<GatewayCreateStaffResponse>("/api/v1/admin/staff", {
      method: "POST",
      body: {
        full_name: parsed.value.full_name,
        email: parsed.value.email,
        phone: parsed.value.phone,
        role: parsed.value.role,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      PASSWORD_NOT_ACCEPTED:
        "Không được nhập mật khẩu khi tạo nhân viên; hệ thống tự gửi email đặt mật khẩu.",
      INVALID_FULL_NAME: "Họ tên nhân viên cần từ 2 đến 120 ký tự.",
      INVALID_EMAIL: "Vui lòng nhập địa chỉ email công việc hợp lệ.",
      INVALID_PHONE: "Số điện thoại không hợp lệ.",
      INVALID_STAFF_ROLE: "Vai trò nhân viên nội bộ không hợp lệ.",
    },
  }
);
