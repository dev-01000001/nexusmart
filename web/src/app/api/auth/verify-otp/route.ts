import { gw } from "@/lib/server/gateway";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { type GatewayUser, toSessionUser } from "@/lib/server/session";

interface VerifyOtpInput {
  email?: string;
  otp?: string;
}

interface GatewayVerifyOtpResponse {
  verified?: boolean;
  user?: GatewayUser | null;
}

export const POST = apiRoute(
  async (req) => {
    const body = await readJson<VerifyOtpInput>(req);
    const email = (body.email ?? "").trim();
    const otp = (body.otp ?? "").trim();
    if (!email || !otp) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<GatewayVerifyOtpResponse>("/api/v1/auth/verify-otp", {
      method: "POST",
      auth: "none",
      body: { email, otp },
    });

    return Response.json(
      {
        verified: Boolean(res.verified),
        user: res.user?.id ? toSessionUser(res.user) : null,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT: "Mã xác thực OTP không đúng hoặc đã hết hạn.",
      NOT_FOUND: "Không tìm thấy tài khoản với email này.",
      FAILED_PRECONDITION: "Tài khoản này đã được xác thực trước đó.",
      RESOURCE_EXHAUSTED: "Bạn đã nhập sai mã OTP quá nhiều lần. Vui lòng gửi lại mã mới.",
    },
  }
);
