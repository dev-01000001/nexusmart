import { NextRequest, NextResponse } from "next/server";
import type { GatewayLoginResponse, LoginRequestBody } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { PAYLOAD_TOO_LARGE_BODY, readBoundedJson } from "@/lib/server/proxy-security";
import { extractClientIp, setSessionCookies } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  const parsed = await readBoundedJson<Partial<LoginRequestBody>>(req);
  if (!parsed.ok) {
    if (parsed.status === 413) {
      return NextResponse.json(PAYLOAD_TOO_LARGE_BODY, {
        status: 413,
        headers: { "Cache-Control": "no-store" },
      });
    }
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Thông tin đăng nhập không hợp lệ." } },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }
  const body = parsed.data;

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
