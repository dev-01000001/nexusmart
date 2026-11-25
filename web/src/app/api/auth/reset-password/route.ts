import { cookies } from "next/headers";
import { gw } from "@/lib/server/gateway";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { clearAuthCookies } from "@/lib/server/tokens";

export const dynamic = "force-dynamic";

interface ResetPasswordInput {
  token?: string;
  new_password?: string;
  newPassword?: string;
}

interface GatewayResetPasswordResponse {
  reset?: boolean;
}

export const POST = apiRoute(
  async (req) => {
    const body = await readJson<ResetPasswordInput>(req);
    const token = (body.token ?? "").trim();
    const newPassword = body.new_password ?? body.newPassword ?? "";
    if (!token || !newPassword) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<GatewayResetPasswordResponse>("/api/v1/auth/reset-password", {
      method: "POST",
      auth: "none",
      body: { token, new_password: newPassword },
    });

    const store = await cookies();
    clearAuthCookies(store);

    return Response.json(
      { reset: Boolean(res.reset ?? true) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT: "Liên kết đặt lại mật khẩu không hợp lệ hoặc mật khẩu mới chưa đủ 8 ký tự.",
      FAILED_PRECONDITION:
        "Liên kết đặt lại mật khẩu không hợp lệ, đã được sử dụng hoặc đã hết hạn.",
      RESOURCE_EXHAUSTED: "Bạn thao tác quá nhanh. Vui lòng thử lại sau ít phút.",
    },
  }
);
