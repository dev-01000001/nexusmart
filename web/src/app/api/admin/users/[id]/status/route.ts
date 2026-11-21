import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import {
  ADMIN_USER_MUTABLE_STATUSES,
  canModifyUserAccount,
  type AdminUser,
} from "@/features/admin/adminComplaintsTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface UpdateUserStatusBody {
  status?: string;
  reason?: string;
}

export const PATCH = apiRoute<RouteCtx>(
  async (req, ctx) => {
    const session = await assertAdminSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_USER_ID");
    }

    const permission = canModifyUserAccount(session.user.id, cleanId, "status");
    if (!permission.allowed) {
      throw new GatewayError("invalid", "CANNOT_MODIFY_SELF");
    }

    const raw = await readJson<UpdateUserStatusBody>(req);
    const status = (raw.status ?? "").trim().toUpperCase();
    const reason = (raw.reason ?? "").trim();

    if (!(ADMIN_USER_MUTABLE_STATUSES as readonly string[]).includes(status)) {
      throw new GatewayError("invalid", "INVALID_USER_STATUS");
    }

    const data = await gw<{ user: AdminUser }>(
      `/api/v1/admin/users/${encodeURIComponent(cleanId)}/status`,
      {
        method: "PATCH",
        body: { status, reason },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_USER_ID: "Mã người dùng không hợp lệ.",
      CANNOT_MODIFY_SELF: "Không thể tự khóa hoặc thay đổi trạng thái tài khoản của chính bạn.",
      INVALID_USER_STATUS: "Trạng thái tài khoản phải là ACTIVE, LOCKED hoặc SUSPENDED.",
    },
  }
);
