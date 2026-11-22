import { NextRequest, NextResponse } from "next/server";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import {
  REFRESH_COOKIE,
  clearSessionCookies,
  extractClientIp,
  refreshTokensWithGateway,
  setSessionCookies,
} from "@/lib/server/session";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  const refreshToken = req.cookies.get(REFRESH_COOKIE)?.value;
  if (!refreshToken) {
    const res = NextResponse.json(
      {
        error: {
          code: "UNAUTHENTICATED",
          message: "Phiên đăng nhập đã hết hạn, vui lòng đăng nhập lại.",
        },
      },
      { status: 401, headers: { "Cache-Control": "no-store" } }
    );
    clearSessionCookies(res);
    return res;
  }

  const result = await refreshTokensWithGateway(refreshToken, {
    clientIp: extractClientIp(req.headers),
  });
  if (!result.ok || !result.tokens) {
    const isAuthFailure = result.status === 401 || result.status === 403;
    const res = NextResponse.json(
      {
        error: {
          code: result.code ?? (isAuthFailure ? "UNAUTHENTICATED" : "UNAVAILABLE"),
          message: isAuthFailure
            ? "Phiên đăng nhập đã hết hạn, vui lòng đăng nhập lại."
            : "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.",
        },
      },
      { status: result.status, headers: { "Cache-Control": "no-store" } }
    );
    if (isAuthFailure) {
      clearSessionCookies(res);
    }
    return res;
  }

  const res = NextResponse.json(
    { ok: true },
    { status: 200, headers: { "Cache-Control": "no-store" } }
  );
  setSessionCookies(res, result.tokens);
  return res;
}
