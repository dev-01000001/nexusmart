import { cookies, headers } from "next/headers";
import {
  MEDIA_MAX_BYTES,
  mapMediaUploadError,
  normalizeMediaContentType,
  validateContentLengthHeader,
} from "@/components/app/mediaUploadUtils";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { ACCESS_COOKIE, GATEWAY_URL } from "@/lib/server/config";
import { GatewayError, logServer } from "@/lib/server/errors";
import { apiRoute, resolveForwardedClientIp } from "@/lib/server/http";
import { resolveTraceContext } from "@/lib/server/trace";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const UPLOAD_TIMEOUT_MS = 30_000;

function mediaErrorJson(status: number, code: string, message?: string): Response {
  return Response.json(
    {
      error: {
        code,
        message: message ?? mapMediaUploadError(status, code),
      },
    },
    {
      status,
      headers: { "Cache-Control": "no-store" },
    }
  );
}

/**
 * Seller media upload BFF (`POST /api/seller/media`).
 * Enforces CSRF (`apiRoute` same-origin check), seller session + shop ownership, Content-Type
 * allowlist, and <= 5 MiB Content-Length cap, then streams `req.body` directly to gateway
 * (`duplex: "half"`) without buffering the image into Node.js memory.
 */
export const POST = apiRoute(async (req) => {
  await assertSellerSession();

  const contentType = normalizeMediaContentType(req.headers.get("content-type"));
  if (!contentType) {
    await req.body?.cancel().catch(() => {});
    return mediaErrorJson(415, "UNSUPPORTED_MEDIA_TYPE");
  }

  const lenCheck = validateContentLengthHeader(req.headers.get("content-length"), MEDIA_MAX_BYTES);
  if (!lenCheck.ok) {
    await req.body?.cancel().catch(() => {});
    if (lenCheck.status === 413) {
      return mediaErrorJson(413, "PAYLOAD_TOO_LARGE");
    }
    return mediaErrorJson(400, "INVALID_ARGUMENT");
  }

  if (!req.body) {
    return mediaErrorJson(400, "INVALID_ARGUMENT", "Tệp ảnh đang trống, vui lòng chọn tệp khác.");
  }

  const store = await cookies();
  const accessToken = store.get(ACCESS_COOKIE)?.value;
  if (!accessToken) {
    await req.body.cancel().catch(() => {});
    throw new GatewayError("unauthenticated");
  }

  const h = await headers();
  const trace = resolveTraceContext(h);
  const clientIp = resolveForwardedClientIp(h);
  const upstreamHeaders: Record<string, string> = {
    Accept: "application/json",
    Authorization: `Bearer ${accessToken}`,
    "Content-Type": contentType,
    traceparent: trace.traceparent,
  };
  if (trace.tracestate) upstreamHeaders["tracestate"] = trace.tracestate;
  if (clientIp) upstreamHeaders["X-Forwarded-For"] = clientIp;
  if (lenCheck.length !== null) {
    upstreamHeaders["Content-Length"] = String(lenCheck.length);
  }

  const init: RequestInit & { duplex: "half" } = {
    method: "POST",
    headers: upstreamHeaders,
    body: req.body,
    duplex: "half",
    cache: "no-store",
    signal: AbortSignal.timeout(UPLOAD_TIMEOUT_MS),
  };

  let upstream: Response;
  try {
    upstream = await fetch(`${GATEWAY_URL}/api/v1/seller/media`, init);
  } catch {
    logServer("warn", "bff_media_upload_gateway_unavailable", { trace_id: trace.traceId });
    return mediaErrorJson(503, "UNAVAILABLE");
  }

  if (upstream.ok) {
    try {
      const data = (await upstream.json()) as unknown;
      return Response.json(data, {
        status: upstream.status,
        headers: { "Cache-Control": "no-store" },
      });
    } catch {
      return mediaErrorJson(503, "UNAVAILABLE");
    }
  }

  let upstreamCode = "";
  try {
    const body = (await upstream.json()) as { error?: { code?: string } } | null;
    upstreamCode = body?.error?.code ?? "";
  } catch {
    // Non-JSON error body from gateway/proxy.
  }

  const status = [400, 401, 403, 413, 415, 429].includes(upstream.status) ? upstream.status : 503;
  const code =
    status === 503
      ? "UNAVAILABLE"
      : upstreamCode ||
        (status === 413
          ? "PAYLOAD_TOO_LARGE"
          : status === 415
            ? "UNSUPPORTED_MEDIA_TYPE"
            : status === 429
              ? "RATE_LIMITED"
              : "INVALID_ARGUMENT");

  const resHeaders: Record<string, string> = { "Cache-Control": "no-store" };
  const retryAfter = upstream.headers.get("retry-after")?.trim();
  if (retryAfter && (status === 429 || status === 503)) {
    resHeaders["Retry-After"] = retryAfter;
  }

  return Response.json(
    {
      error: {
        code,
        message: mapMediaUploadError(status, code),
      },
    },
    { status, headers: resHeaders }
  );
});
