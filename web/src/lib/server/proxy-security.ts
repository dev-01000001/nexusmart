export const MAX_BFF_BODY_BYTES = 2 * 1024 * 1024; // 2 MiB (PLT-B6)
export const COD_IMPORT_MULTIPART_OVERHEAD_BYTES = 64 * 1024; // +64 KiB for multipart/form-data envelope on cod/import

/**
 * Resolves the body byte limit for BFF proxy requests:
 * - Default: 2 MiB (`MAX_BFF_BODY_BYTES`).
 * - `POST /admin/reconciliation/cod/import` with `multipart/form-data`: 2 MiB + 64 KiB
 *   so a valid <= 2 MiB CSV file is never rejected due to multipart boundaries/headers.
 */
export function resolveMaxBffBodyBytes(
  method: string,
  segments: readonly string[],
  contentType: string | null | undefined
): number {
  if (
    method.toUpperCase() === "POST" &&
    segments.join("/").toLowerCase() === "admin/reconciliation/cod/import" &&
    (contentType ?? "").toLowerCase().includes("multipart/form-data")
  ) {
    return MAX_BFF_BODY_BYTES + COD_IMPORT_MULTIPART_OVERHEAD_BYTES;
  }
  return MAX_BFF_BODY_BYTES;
}

export const FALLBACK_HTML_SANDBOX_CSP =
  "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'";

export const PAYLOAD_TOO_LARGE_BODY = {
  error: {
    code: "PAYLOAD_TOO_LARGE",
    message: "Dữ liệu gửi lên vượt quá giới hạn cho phép (tối đa 2 MB).",
  },
};

/**
 * Applies mandatory security headers to every BFF proxy response:
 * - Always enforces `X-Content-Type-Options: nosniff` (single value).
 * - Forwards upstream `Content-Security-Policy` when present.
 * - For any `text/html` response missing upstream CSP, enforces a strict sandbox CSP.
 */
export function applyProxySecurityHeaders(
  targetHeaders: Headers,
  upstreamHeaders: Headers | null | undefined,
  contentType: string
): void {
  const upstreamNoSniff = upstreamHeaders?.get("x-content-type-options")?.trim();
  targetHeaders.set(
    "X-Content-Type-Options",
    upstreamNoSniff && upstreamNoSniff.toLowerCase() === "nosniff" ? upstreamNoSniff : "nosniff"
  );

  const upstreamCsp = upstreamHeaders?.get("content-security-policy")?.trim() ?? "";
  const isHtml = (contentType ?? "").toLowerCase().includes("text/html");

  if (upstreamCsp) {
    targetHeaders.set("Content-Security-Policy", upstreamCsp);
  } else if (isHtml) {
    targetHeaders.set("Content-Security-Policy", FALLBACK_HTML_SANDBOX_CSP);
  }
}

export type BoundedBodyResult = { ok: true; body: Uint8Array } | { ok: false; status: 413 };

/**
 * Reads a request body up to `maxBytes`, handling both explicit Content-Length
 * and chunked/streaming bodies without buffering unbounded payloads into memory.
 */
export async function readBoundedBody(
  req: Request,
  maxBytes: number = MAX_BFF_BODY_BYTES
): Promise<BoundedBodyResult> {
  const rawLen = req.headers.get("content-length");
  if (rawLen !== null) {
    const parsed = Number(rawLen.trim());
    if (Number.isFinite(parsed) && parsed > maxBytes) {
      await req.body?.cancel().catch(() => {});
      return { ok: false, status: 413 };
    }
  }

  if (!req.body) {
    return { ok: true, body: new Uint8Array(0) };
  }

  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value || value.byteLength === 0) continue;
      total += value.byteLength;
      if (total > maxBytes) {
        await reader.cancel().catch(() => {});
        return { ok: false, status: 413 };
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  if (total === 0) {
    return { ok: true, body: new Uint8Array(0) };
  }
  if (chunks.length === 1) {
    return { ok: true, body: chunks[0] };
  }

  const merged = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return { ok: true, body: merged };
}

export type BoundedJsonResult<T> = { ok: true; data: T } | { ok: false; status: 413 | 400 };

export async function readBoundedJson<T>(
  req: Request,
  maxBytes: number = MAX_BFF_BODY_BYTES
): Promise<BoundedJsonResult<T>> {
  const res = await readBoundedBody(req, maxBytes);
  if (!res.ok) {
    return { ok: false, status: 413 };
  }
  try {
    const text = new TextDecoder().decode(res.body);
    return { ok: true, data: JSON.parse(text) as T };
  } catch {
    return { ok: false, status: 400 };
  }
}
