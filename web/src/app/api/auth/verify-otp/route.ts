import { NextRequest, NextResponse } from "next/server";
import type { VerifyOTPRequestBody, VerifyOTPResponse } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { extractClientIp } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  let body: Partial<VerifyOTPRequestBody>;
  try {
    body = (await req.json()) as Partial<VerifyOTPRequestBody>;
  } catch {
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Dữ liệu xác thực không hợp lệ." } },
      { status: 400 }
    );
  }

  try {
    const data = await gatewayFetch<VerifyOTPResponse>("/auth/verify-otp", {
      method: "POST",
      token: false,
      clientIp: extractClientIp(req.headers),
      body: {
        email: typeof body.email === "string" ? body.email.trim() : "",
        otp: typeof body.otp === "string" ? body.otp.trim() : "",
      },
    });
    return NextResponse.json(data, { status: 200 });
  } catch (err) {
    return gatewayErrorResponse(err);
  }
}
