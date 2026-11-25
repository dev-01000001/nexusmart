import { gw } from "@/lib/server/gateway";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";

export const dynamic = "force-dynamic";

interface ResendOtpInput {
  email?: string;
}

interface GatewayResendOtpResponse {
  sent?: boolean;
}

export const POST = apiRoute(
  async (req) => {
    const body = await readJson<ResendOtpInput>(req);
    const email = (body.email ?? "").trim();
    if (!email) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<GatewayResendOtpResponse>("/api/v1/auth/resend-otp", {
      method: "POST",
      auth: "none",
      body: { email },
    });

    return Response.json({ sent: Boolean(res.sent) }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng nhập địa chỉ email hợp lệ.",
      NOT_FOUND: "Không tìm thấy tài khoản với email này.",
      FAILED_PRECONDITION: "Tài khoản này đã được xác thực.",
      RESOURCE_EXHAUSTED: "Bạn đã yêu cầu gửi lại mã quá số lần cho phép. Vui lòng thử lại sau.",
    },
  }
);
