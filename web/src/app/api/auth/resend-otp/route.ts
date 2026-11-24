import { NextRequest, NextResponse } from "next/server";
import type { ResendOTPRequestBody, ResendOTPResponse } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { PAYLOAD_TOO_LARGE_BODY, readBoundedJson } from "@/lib/server/proxy-security";
import { extractClientIp } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  const parsed = await readBoundedJson<Partial<ResendOTPRequestBody>>(req);
  if (!parsed.ok) {
    if (parsed.status === 413) {
      return NextResponse.json(PAYLOAD_TOO_LARGE_BODY, {
        status: 413,
        headers: { "Cache-Control": "no-store" },
      });
    }
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Dữ liệu gửi lại mã không hợp lệ." } },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }
  const body = parsed.data;

  try {
    const data = await gatewayFetch<ResendOTPResponse>("/auth/resend-otp", {
      method: "POST",
      token: false,
      clientIp: extractClientIp(req.headers),
      body: {
        email: typeof body.email === "string" ? body.email.trim() : "",
      },
    });
    return NextResponse.json(data, { status: 200, headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    return gatewayErrorResponse(err);
  }
}
