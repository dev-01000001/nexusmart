import { cookies } from "next/headers";
import { REFRESH_COOKIE } from "@/lib/server/config";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute, resolveForwardedClientIp } from "@/lib/server/http";
import { clearAuthCookies, rotateRefreshToken, setAuthCookies } from "@/lib/server/tokens";
import { resolveTraceContext } from "@/lib/server/trace";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  const store = await cookies();
  const refresh = store.get(REFRESH_COOKIE)?.value;
  if (!refresh) {
    clearAuthCookies(store);
    throw new GatewayError("unauthenticated");
  }

  const trace = resolveTraceContext(req.headers);
  const clientIp = resolveForwardedClientIp(req.headers);

  // On 429 or 5xx, rotateRefreshToken throws GatewayError("rate_limited"|"unavailable")
  // so cookies are NOT cleared and Retry-After is preserved by apiRoute.
  const pair = await rotateRefreshToken(refresh, trace, { clientIp });
  if (!pair) {
    clearAuthCookies(store);
    throw new GatewayError("unauthenticated");
  }

  setAuthCookies(store, pair);
  return Response.json({ ok: true }, { headers: { "Cache-Control": "no-store" } });
});
