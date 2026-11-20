import { cookies } from "next/headers";
import { gw } from "@/lib/server/gateway";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { type GatewayUser, toSessionUser } from "@/lib/server/session";
import { setAuthCookies } from "@/lib/server/tokens";

interface LoginInput {
  email?: string;
  password?: string;
}

interface GatewayLoginResponse {
  access_token?: string;
  refresh_token?: string;
  expires_in_seconds?: number;
  user?: GatewayUser | null;
}

export const POST = apiRoute(
  async (req) => {
    const body = await readJson<LoginInput>(req);
    const email = (body.email ?? "").trim();
    const password = body.password ?? "";
    if (!email || !password) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<GatewayLoginResponse>("/api/v1/auth/login", {
      method: "POST",
      auth: "none",
      body: { email, password },
    });

    if (!res.access_token || !res.refresh_token || !res.user?.id) {
      throw new GatewayError("unavailable");
    }

    const store = await cookies();
    setAuthCookies(store, {
      accessToken: res.access_token,
      refreshToken: res.refresh_token,
      expiresIn: Number(res.expires_in_seconds) || 900,
    });

    return Response.json(
      { user: toSessionUser(res.user) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng nhập đầy đủ email và mật khẩu hợp lệ.",
      UNAUTHENTICATED: "Email hoặc mật khẩu chưa chính xác.",
      FAILED_PRECONDITION:
        "Tài khoản chưa xác thực OTP. Bạn vui lòng kiểm tra hộp thư để xác thực.",
      PERMISSION_DENIED: "Tài khoản hiện đang tạm khóa. Vui lòng liên hệ hỗ trợ.",
      RESOURCE_EXHAUSTED: "Bạn đã đăng nhập sai quá nhiều lần. Vui lòng thử lại sau ít phút.",
    },
  }
);
