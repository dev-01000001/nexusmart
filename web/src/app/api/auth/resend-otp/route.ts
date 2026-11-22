import { NextRequest, NextResponse } from "next/server";
import type { ResendOTPRequestBody, ResendOTPResponse } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { extractClientIp } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  let body: Partial<ResendOTPRequestBody>;
  try {
    body = (await req.json()) as Partial<ResendOTPRequestBody>;
  } catch {
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Dữ liệu gửi lại mã không hợp lệ." } },
      { status: 400 }
    );
  }

  try {
    const data = await gatewayFetch<ResendOTPResponse>("/auth/resend-otp", {
      method: "POST",
      token: false,
      clientIp: extractClientIp(req.headers),
      body: {
        email: typeof body.email === "string" ? body.email.trim() : "",
      },
    });
    return NextResponse.json(data, { status: 200 });
  } catch (err) {
    return gatewayErrorResponse(err);
  }
}
