import { gw } from "@/lib/server/gateway";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";

export const dynamic = "force-dynamic";

interface ForgotPasswordInput {
  email?: string;
}

interface GatewayForgotPasswordResponse {
  sent?: boolean;
  message?: string;
}

export const POST = apiRoute(
  async (req) => {
    const body = await readJson<ForgotPasswordInput>(req);
    const email = (body.email ?? "").trim();
    if (!email) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<GatewayForgotPasswordResponse>("/api/v1/auth/forgot-password", {
      method: "POST",
      auth: "none",
      body: { email },
    });

    return Response.json(
      { sent: Boolean(res.sent ?? true), message: res.message ?? "" },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng nhập địa chỉ email hợp lệ.",
      RESOURCE_EXHAUSTED: "Bạn thao tác quá nhanh. Vui lòng thử lại sau ít phút.",
    },
  }
);
