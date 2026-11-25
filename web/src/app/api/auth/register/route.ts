import { gw } from "@/lib/server/gateway";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";

export const dynamic = "force-dynamic";

interface RegisterInput {
  fullName?: string;
  full_name?: string;
  email?: string;
  phone?: string;
  password?: string;
}

interface GatewayRegisterResponse {
  user_id?: string;
  email?: string;
  status?: string;
}

export const POST = apiRoute(
  async (req) => {
    const body = await readJson<RegisterInput>(req);
    const fullName = (body.fullName ?? body.full_name ?? "").trim();
    const email = (body.email ?? "").trim();
    const phone = (body.phone ?? "").trim();
    const password = body.password ?? "";

    if (!fullName || !email || !password) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const res = await gw<GatewayRegisterResponse>("/api/v1/auth/register", {
      method: "POST",
      auth: "none",
      body: {
        full_name: fullName,
        email,
        phone,
        password,
      },
    });

    return Response.json(
      {
        userId: res.user_id ?? "",
        email: res.email ?? email,
        status: res.status ?? "PENDING_VERIFICATION",
      },
      { status: 201, headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      ALREADY_EXISTS: "Email hoặc số điện thoại này đã được đăng ký.",
      INVALID_ARGUMENT:
        "Thông tin đăng ký chưa hợp lệ. Email cần đúng định dạng và mật khẩu tối thiểu 8 ký tự.",
      RESOURCE_EXHAUSTED: "Bạn thao tác hơi nhanh. Vui lòng thử lại sau ít phút.",
    },
  }
);
