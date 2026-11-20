// Route-handler plumbing: CSRF origin check + uniform error handling.
import type { NextRequest } from "next/server";
import { APP_ORIGINS } from "./config";
import { GatewayError, errorResponse } from "./errors";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

/**
 * CSRF guard for write requests. Requires an `Origin` header equal to this site's origin
 * (APP_ORIGIN allow-list when set, else the request Host). Missing Origin or a cross-site
 * `Sec-Fetch-Site` is rejected. Throws GatewayError("forbidden", "BAD_ORIGIN").
 * Server actions are already origin-checked by Next; route handlers must call this
 * (apiRoute does it automatically for non-GET methods).
 */
export function assertSameOrigin(req: Request): void {
  const bad = () => new GatewayError("forbidden", "BAD_ORIGIN");
  if (req.headers.get("sec-fetch-site") === "cross-site") throw bad();
  const origin = req.headers.get("origin");
  if (!origin || origin === "null") throw bad();
  if (APP_ORIGINS.length > 0) {
    if (!APP_ORIGINS.includes(origin.replace(/\/+$/, ""))) throw bad();
    return;
  }
  const host = req.headers.get("x-forwarded-host")?.split(",")[0]?.trim() || req.headers.get("host");
  let originHost: string;
  try {
    originHost = new URL(origin).host;
  } catch {
    throw bad();
  }
  if (!host || originHost !== host) throw bad();
}

type Handler<C> = (req: NextRequest, ctx: C) => Promise<Response>;

/**
 * Wrap a route handler: CSRF check for non-GET methods, and any thrown error (GatewayError or
 * unexpected) becomes `{ error: { code, message } }` with a safe status. `messages` overrides the
 * Vietnamese text per code/kind.
 *
 *   export const POST = apiRoute(async (req) => { ... return Response.json(data); });
 */
export function apiRoute<C = unknown>(
  fn: Handler<C>,
  opts: { messages?: Record<string, string> } = {}
): Handler<C> {
  return async (req, ctx) => {
    try {
      if (!SAFE_METHODS.has(req.method)) assertSameOrigin(req);
      return await fn(req, ctx);
    } catch (err) {
      // Let Next handle its own control-flow errors (redirect()/notFound()).
      if (isNextControlFlow(err)) throw err;
      return errorResponse(err, opts.messages);
    }
  };
}

/** Parse a JSON body or throw GatewayError("invalid"). */
export async function readJson<T>(req: Request): Promise<T> {
  try {
    return (await req.json()) as T;
  } catch {
    throw new GatewayError("invalid", "INVALID_JSON");
  }
}

function isNextControlFlow(err: unknown): boolean {
  const digest = (err as { digest?: unknown } | null)?.digest;
  return typeof digest === "string" && /^(NEXT_REDIRECT|NEXT_NOT_FOUND|NEXT_HTTP_ERROR)/.test(digest);
}
