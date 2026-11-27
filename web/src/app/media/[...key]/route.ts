import type { NextRequest } from "next/server";
import {
  buildGatewayMediaPath,
  normalizeMediaContentType,
} from "@/components/app/mediaUploadUtils";
import { GATEWAY_URL } from "@/lib/server/config";
import { logServer } from "@/lib/server/errors";
import { resolveForwardedClientIp } from "@/lib/server/http";
import { resolveTraceContext } from "@/lib/server/trace";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const MEDIA_FETCH_TIMEOUT_MS = 30_000;
const FALLBACK_CACHE_CONTROL = "public, max-age=31536000, immutable";
const MEDIA_CSP = "default-src 'none'";

type RouteCtx = { params: Promise<{ key: string[] }> };

function plainResponse(status: number, body: string): Response {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
    },
  });
}

function buildMediaResponseHeaders(upstreamHeaders: Headers, gatewayPath: string): Headers {
  const out = new Headers();
  const safeCt =
    normalizeMediaContentType(upstreamHeaders.get("content-type")) ??
    (gatewayPath.endsWith(".png")
      ? "image/png"
      : gatewayPath.endsWith(".webp")
        ? "image/webp"
        : "image/jpeg");

  out.set("Content-Type", safeCt);
  out.set("Cache-Control", upstreamHeaders.get("cache-control")?.trim() || FALLBACK_CACHE_CONTROL);
  out.set("X-Content-Type-Options", "nosniff");
  out.set("Content-Security-Policy", MEDIA_CSP);

  const etag = upstreamHeaders.get("etag")?.trim();
  if (etag) out.set("ETag", etag);

  const contentLength = upstreamHeaders.get("content-length")?.trim();
  if (contentLength && /^\d+$/.test(contentLength)) {
    out.set("Content-Length", contentLength);
  }
  return out;
}

/**
 * Public media streaming proxy (`GET /media/shops/<shop_id>/<sha256>.<ext>`).
 * Accessible on both the root platform host and published shop site hosts; validates the key
 * strictly before proxying and streams the object body straight from gateway preserving cache
 * headers (`Cache-Control`, `ETag`, `304 Not Modified`) and strict `default-src 'none'` CSP.
 */
export async function GET(req: NextRequest, { params }: RouteCtx): Promise<Response> {
  const { key } = await params;
  const gatewayPath = buildGatewayMediaPath(key);
  if (!gatewayPath || req.nextUrl.pathname !== gatewayPath) {
    return plainResponse(404, "Not Found");
  }

  const trace = resolveTraceContext(req.headers);
  const clientIp = resolveForwardedClientIp(req.headers);
  const reqHeaders: Record<string, string> = {
    traceparent: trace.traceparent,
  };
  if (trace.tracestate) reqHeaders["tracestate"] = trace.tracestate;
  if (clientIp) reqHeaders["X-Forwarded-For"] = clientIp;
  const ifNoneMatch = req.headers.get("if-none-match")?.trim();
  if (ifNoneMatch) reqHeaders["If-None-Match"] = ifNoneMatch;

  let upstream: Response;
  try {
    upstream = await fetch(`${GATEWAY_URL}${gatewayPath}`, {
      method: "GET",
      headers: reqHeaders,
      cache: "no-store",
      signal: AbortSignal.timeout(MEDIA_FETCH_TIMEOUT_MS),
    });
  } catch {
    logServer("warn", "bff_media_get_gateway_unavailable", { trace_id: trace.traceId });
    return plainResponse(503, "Service Unavailable");
  }

  if (upstream.status === 304) {
    const out = buildMediaResponseHeaders(upstream.headers, gatewayPath);
    out.delete("Content-Length");
    return new Response(null, { status: 304, headers: out });
  }

  if (upstream.status === 404) {
    return plainResponse(404, "Not Found");
  }

  if (upstream.status === 429) {
    const headers = new Headers({
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
    });
    const retryAfter = upstream.headers.get("retry-after")?.trim();
    if (retryAfter) headers.set("Retry-After", retryAfter);
    return new Response("Too Many Requests", { status: 429, headers });
  }

  if (!upstream.ok) {
    return plainResponse(503, "Service Unavailable");
  }

  return new Response(upstream.body, {
    status: 200,
    headers: buildMediaResponseHeaders(upstream.headers, gatewayPath),
  });
}
