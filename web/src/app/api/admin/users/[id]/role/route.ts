import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import {
  ADMIN_USER_ROLES,
  canModifyUserAccount,
  type AdminUser,
} from "@/features/admin/adminComplaintsTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface UpdateUserRoleBody {
  role?: string;
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

    const permission = canModifyUserAccount(session.user.id, cleanId, "role");
    if (!permission.allowed) {
      throw new GatewayError("invalid", "CANNOT_MODIFY_SELF");
    }

    const raw = await readJson<UpdateUserRoleBody>(req);
    const role = (raw.role ?? "").trim().toLowerCase();
    const reason = (raw.reason ?? "").trim();

    if (!(ADMIN_USER_ROLES as readonly string[]).includes(role)) {
      throw new GatewayError("invalid", "INVALID_USER_ROLE");
    }

    const data = await gw<{ user: AdminUser }>(
      `/api/v1/admin/users/${encodeURIComponent(cleanId)}/role`,
      {
        method: "PATCH",
        body: { role, reason },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_USER_ID: "Mã người dùng không hợp lệ.",
      CANNOT_MODIFY_SELF: "Không thể tự thay đổi vai trò của tài khoản đang đăng nhập.",
      INVALID_USER_ROLE: "Vai trò tài khoản phải là buyer, seller, admin hoặc super_admin.",
    },
  }
);
