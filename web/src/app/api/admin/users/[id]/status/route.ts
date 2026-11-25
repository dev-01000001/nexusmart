import type { GatewayUser } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { canModifyUserAccount } from "@/features/admin/adminComplaintsTypes";
import {
  parseEntityId,
  validateUpdateUserStatusInput,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const PATCH = apiRoute<RouteCtx>(
  async (req, ctx) => {
    const session = await assertCapability("users.status.write");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_USER_ID");
    }

    const permission = canModifyUserAccount(session.user.id, cleanId, "status");
    if (!permission.allowed) {
      throw new GatewayError("invalid", "CANNOT_MODIFY_SELF");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateUpdateUserStatusInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<{ user: GatewayUser }>(
      `/api/v1/admin/users/${encodeURIComponent(cleanId)}/status`,
      {
        method: "PATCH",
        body: {
          status: parsed.value.status,
          reason: parsed.value.reason,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_USER_ID: "Mã người dùng không hợp lệ.",
      CANNOT_MODIFY_SELF: "Không thể tự khóa hoặc thay đổi trạng thái tài khoản của chính bạn.",
      INVALID_USER_STATUS: "Trạng thái tài khoản chỉ hỗ trợ hoạt động hoặc khóa.",
    },
  }
);
