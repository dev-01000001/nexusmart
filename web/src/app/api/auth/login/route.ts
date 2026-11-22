import { NextRequest, NextResponse } from "next/server";
import type { GatewayLoginResponse, LoginRequestBody } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { extractClientIp, setSessionCookies } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  let body: Partial<LoginRequestBody>;
  try {
    body = (await req.json()) as Partial<LoginRequestBody>;
  } catch {
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Thông tin đăng nhập không hợp lệ." } },
      { status: 400 }
    );
  }

  try {
    const data = await gatewayFetch<GatewayLoginResponse>("/auth/login", {
      method: "POST",
      token: false,
      clientIp: extractClientIp(req.headers),
      body: {
        email: typeof body.email === "string" ? body.email.trim() : "",
        password: typeof body.password === "string" ? body.password : "",
      },
    });

    const res = NextResponse.json(
      { user: data.user },
      { status: 200, headers: { "Cache-Control": "no-store" } }
    );
    setSessionCookies(res, data);
    return res;
  } catch (err) {
    return gatewayErrorResponse(err);
  }
}
