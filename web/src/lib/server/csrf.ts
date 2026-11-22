import { NextResponse } from "next/server";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

function normalizeOrigin(value: string): string | null {
  try {
    return new URL(value).origin;
  } catch {
    return null;
  }
}

function resolveExpectedOrigin(req: Request): string | null {
  const configured = process.env.APP_ORIGIN?.trim();
  if (configured) {
    return normalizeOrigin(configured);
  }

  let proto = "http";
  try {
    proto = new URL(req.url).protocol.replace(":", "");
  } catch {
    proto = "http";
  }

  const host = req.headers.get("host")?.trim();
  if (!host) {
    return normalizeOrigin(req.url);
  }
  return normalizeOrigin(`${proto}://${host}`);
}

/**
 * Enforces same-origin `Origin` header on all mutating `/api/**` requests.
 * Returns a 403 NextResponse when missing or mismatched, or `null` if valid.
 */
export function verifyCsrfOrigin(req: Request): NextResponse | null {
  if (SAFE_METHODS.has(req.method.toUpperCase())) {
    return null;
  }

  const originHeader = req.headers.get("origin")?.trim();
  if (!originHeader) {
    return NextResponse.json(
      {
        error: {
          code: "CSRF_FORBIDDEN",
          message: "Yêu cầu không hợp lệ.",
        },
      },
      { status: 403 }
    );
  }

  const incomingOrigin = normalizeOrigin(originHeader);
  const expectedOrigin = resolveExpectedOrigin(req);

  if (!incomingOrigin || !expectedOrigin || incomingOrigin !== expectedOrigin) {
    return NextResponse.json(
      {
        error: {
          code: "CSRF_FORBIDDEN",
          message: "Yêu cầu không hợp lệ.",
        },
      },
      { status: 403 }
    );
  }

  return null;
}
