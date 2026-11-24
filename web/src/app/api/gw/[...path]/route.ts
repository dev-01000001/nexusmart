import { NextRequest, NextResponse } from "next/server";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import { sanitizeGatewayError } from "@/lib/server/gateway";
import {
  PAYLOAD_TOO_LARGE_BODY,
  applyProxySecurityHeaders,
  readBoundedBody,
  resolveMaxBffBodyBytes,
} from "@/lib/server/proxy-security";
import {
  ACCESS_COOKIE,
  applyClientIpHeader,
  extractClientIp,
  getGatewayUrl,
} from "@/lib/server/session";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const DEFAULT_PROXY_TIMEOUT_MS = 15_000;
const COD_IMPORT_PROXY_TIMEOUT_MS = 120_000;
const RESCAN_PROXY_TIMEOUT_MS = 60_000;
const BLOCKED_ROOT_SEGMENTS = new Set(["webhooks", "auth"]);

function resolveProxyTimeoutMs(method: string, segments: string[]): number {
  if (method !== "POST") return DEFAULT_PROXY_TIMEOUT_MS;
  const joined = segments.join("/").toLowerCase();
  if (joined === "admin/reconciliation/cod/import") {
    return COD_IMPORT_PROXY_TIMEOUT_MS;
  }
  if (joined === "admin/banned-keywords/rescan") {
    return RESCAN_PROXY_TIMEOUT_MS;
  }
  return DEFAULT_PROXY_TIMEOUT_MS;
}
const UNAVAILABLE_BODY = {
  error: {
    code: "UNAVAILABLE",
    message: "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.",
  },
};

type RouteContext = {
  params: Promise<{ path: string[] }>;
};

function isSseRequest(req: NextRequest, segments: string[]): boolean {
  if (req.method !== "GET") return false;
  const accept = req.headers.get("accept")?.toLowerCase() ?? "";
  if (accept.includes("text/event-stream")) return true;
  return segments.length === 3 && segments[0] === "payments" && segments[2] === "events";
}

function jsonWithSecurity(
  body: unknown,
  status: number,
  extraHeaders?: HeadersInit,
  upstreamHeaders?: Headers | null
): NextResponse {
  const headers = new Headers(extraHeaders);
  if (!headers.has("Cache-Control")) {
    headers.set("Cache-Control", "no-store");
  }
  applyProxySecurityHeaders(headers, upstreamHeaders, "application/json");
  return NextResponse.json(body, { status, headers });
}

function validatePathSegments(segments: string[]): NextResponse | null {
  if (!Array.isArray(segments) || segments.length === 0) {
    return jsonWithSecurity(
      { error: { code: "INVALID_PATH", message: "Đường dẫn không hợp lệ." } },
      400
    );
  }

  for (const seg of segments) {
    if (
      !seg ||
      seg === "." ||
      seg === ".." ||
      seg.includes("/") ||
      seg.includes("\\") ||
      /[\u0000-\u001f\u007f]/.test(seg)
    ) {
      return jsonWithSecurity(
        { error: { code: "INVALID_PATH", message: "Đường dẫn không hợp lệ." } },
        400
      );
    }
  }

  const root = segments[0].toLowerCase();
  if (BLOCKED_ROOT_SEGMENTS.has(root)) {
    return jsonWithSecurity(
      { error: { code: "FORBIDDEN_PATH", message: "Đường dẫn này không được hỗ trợ trực tiếp." } },
      403
    );
  }

  return null;
}

async function handleProxy(
  req: NextRequest,
  context: RouteContext
): Promise<NextResponse | Response> {
  const { path: segments } = await context.params;

  const pathErr = validatePathSegments(segments);
  if (pathErr) return pathErr;

  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) {
    applyProxySecurityHeaders(csrfErr.headers, null, "application/json");
    return csrfErr;
  }

  const encodedPath = segments.map((s) => encodeURIComponent(s)).join("/");
  const upstreamUrl = `${getGatewayUrl()}/api/v1/${encodedPath}${req.nextUrl.search}`;

  const upstreamHeaders = new Headers();
  const contentType = req.headers.get("content-type");
  if (contentType) upstreamHeaders.set("Content-Type", contentType);

  const accept = req.headers.get("accept");
  if (accept) upstreamHeaders.set("Accept", accept);

  const idempotencyKey = req.headers.get("idempotency-key");
  if (idempotencyKey) upstreamHeaders.set("Idempotency-Key", idempotencyKey);

  const lastEventId = req.headers.get("last-event-id");
  if (lastEventId) upstreamHeaders.set("Last-Event-ID", lastEventId);

  for (const extraHeader of ["x-carrier-code", "x-run-date", "x-file-name"]) {
    const val = req.headers.get(extraHeader);
    if (val) upstreamHeaders.set(extraHeader, val);
  }

  applyClientIpHeader(upstreamHeaders, extractClientIp(req.headers));

  const accessToken = req.cookies.get(ACCESS_COOKIE)?.value;
  if (accessToken) {
    upstreamHeaders.set("Authorization", `Bearer ${accessToken}`);
  }

  const sse = isSseRequest(req, segments);
  let body: ArrayBuffer | undefined;
  if (req.method !== "GET" && req.method !== "HEAD") {
    const maxBodyBytes = resolveMaxBffBodyBytes(req.method, segments, contentType);
    const bounded = await readBoundedBody(req, maxBodyBytes);
    if (!bounded.ok) {
      return jsonWithSecurity(PAYLOAD_TOO_LARGE_BODY, 413);
    }
    if (bounded.body.byteLength > 0) {
      body = bounded.body.buffer.slice(
        bounded.body.byteOffset,
        bounded.body.byteOffset + bounded.body.byteLength
      ) as ArrayBuffer;
    }
  }

  const timeoutMs = resolveProxyTimeoutMs(req.method, segments);
  const signal = sse ? req.signal : AbortSignal.any([req.signal, AbortSignal.timeout(timeoutMs)]);

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, {
      method: req.method,
      headers: upstreamHeaders,
      body,
      cache: "no-store",
      redirect: "manual",
      signal,
    });
  } catch {
    return jsonWithSecurity(UNAVAILABLE_BODY, 503);
  }

  const upstreamContentType = upstream.headers.get("content-type") ?? "";
  if (upstream.ok && (sse || upstreamContentType.toLowerCase().includes("text/event-stream"))) {
    const streamContentType = upstreamContentType || "text/event-stream; charset=utf-8";
    const streamHeaders = new Headers({
      "Content-Type": streamContentType,
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    });
    const traceId = upstream.headers.get("x-trace-id");
    if (traceId) streamHeaders.set("X-Trace-Id", traceId);
    applyProxySecurityHeaders(streamHeaders, upstream.headers, streamContentType);

    return new Response(upstream.body, {
      status: upstream.status,
      headers: streamHeaders,
    });
  }

  if (upstream.status >= 500) {
    return jsonWithSecurity(UNAVAILABLE_BODY, 503, undefined, upstream.headers);
  }

  if (!upstream.ok) {
    const rawText = await upstream.text().catch(() => "");
    let parsed: unknown = null;
    if (rawText) {
      try {
        parsed = JSON.parse(rawText);
      } catch {
        parsed = null;
      }
    }
    const sanitized = sanitizeGatewayError(upstream.status, parsed);
    const errHeaders = new Headers({ "Cache-Control": "no-store" });
    for (const h of ["x-ratelimit-limit", "x-ratelimit-remaining", "retry-after"]) {
      const val = upstream.headers.get(h);
      if (val) errHeaders.set(h, val);
    }
    return jsonWithSecurity(
      { error: { code: sanitized.code, message: sanitized.message } },
      sanitized.status,
      errHeaders,
      upstream.headers
    );
  }

  const resHeaders = new Headers({ "Cache-Control": "no-store" });
  if (upstreamContentType) {
    resHeaders.set("Content-Type", upstreamContentType);
  }
  for (const h of [
    "x-trace-id",
    "x-ratelimit-limit",
    "x-ratelimit-remaining",
    "content-disposition",
  ]) {
    const val = upstream.headers.get(h);
    if (val) resHeaders.set(h, val);
  }
  applyProxySecurityHeaders(resHeaders, upstream.headers, upstreamContentType);

  if (upstream.status === 204) {
    return new Response(null, { status: 204, headers: resHeaders });
  }

  const payload = await upstream.arrayBuffer();
  return new Response(payload, {
    status: upstream.status,
    headers: resHeaders,
  });
}

export async function GET(req: NextRequest, context: RouteContext) {
  return handleProxy(req, context);
}

export async function POST(req: NextRequest, context: RouteContext) {
  return handleProxy(req, context);
}

export async function PUT(req: NextRequest, context: RouteContext) {
  return handleProxy(req, context);
}

export async function PATCH(req: NextRequest, context: RouteContext) {
  return handleProxy(req, context);
}

export async function DELETE(req: NextRequest, context: RouteContext) {
  return handleProxy(req, context);
}
