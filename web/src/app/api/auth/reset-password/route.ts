import { NextRequest, NextResponse } from "next/server";
import type { ResetPasswordRequestBody, ResetPasswordResponse } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { PAYLOAD_TOO_LARGE_BODY, readBoundedJson } from "@/lib/server/proxy-security";
import { clearSessionCookies, extractClientIp } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  const parsed = await readBoundedJson<Partial<ResetPasswordRequestBody>>(req);
  if (!parsed.ok) {
    if (parsed.status === 413) {
      return NextResponse.json(PAYLOAD_TOO_LARGE_BODY, {
        status: 413,
        headers: { "Cache-Control": "no-store" },
      });
    }
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Thông tin đặt lại mật khẩu không hợp lệ." } },
      { status: 400 }
    );
  }
  const body = parsed.data;

  try {
    const data = await gatewayFetch<ResetPasswordResponse>("/auth/reset-password", {
      method: "POST",
      token: false,
      clientIp: extractClientIp(req.headers),
      body: {
        token: typeof body.token === "string" ? body.token.trim() : "",
        new_password: typeof body.new_password === "string" ? body.new_password : "",
      },
    });
    const res = NextResponse.json(data, { status: 200, headers: { "Cache-Control": "no-store" } });
    clearSessionCookies(res);
    return res;
  } catch (err) {
    return gatewayErrorResponse(err);
  }
}
