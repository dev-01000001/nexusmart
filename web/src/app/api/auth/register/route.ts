import { NextRequest, NextResponse } from "next/server";
import type { RegisterRequestBody, RegisterResponse } from "@/lib/gateway-types";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import { PAYLOAD_TOO_LARGE_BODY, readBoundedJson } from "@/lib/server/proxy-security";
import { extractClientIp } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  const parsed = await readBoundedJson<Partial<RegisterRequestBody>>(req);
  if (!parsed.ok) {
    if (parsed.status === 413) {
      return NextResponse.json(PAYLOAD_TOO_LARGE_BODY, {
        status: 413,
        headers: { "Cache-Control": "no-store" },
      });
    }
    return NextResponse.json(
      { error: { code: "INVALID_JSON", message: "Dữ liệu đăng ký không hợp lệ." } },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }
  const body = parsed.data;

  try {
    const data = await gatewayFetch<RegisterResponse>("/auth/register", {
      method: "POST",
      token: false,
      clientIp: extractClientIp(req.headers),
      body: {
        full_name: typeof body.full_name === "string" ? body.full_name.trim() : "",
        email: typeof body.email === "string" ? body.email.trim() : "",
        phone: typeof body.phone === "string" ? body.phone.trim() : "",
        password: typeof body.password === "string" ? body.password : "",
      },
    });
    return NextResponse.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    return gatewayErrorResponse(err);
  }
}
