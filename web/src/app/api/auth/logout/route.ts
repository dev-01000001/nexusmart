import { cookies } from "next/headers";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/server/config";
import { GatewayError, logServer } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, resolveForwardedClientIp } from "@/lib/server/http";
import {
  clearAuthCookies,
  invalidateRefreshCache,
  revokeTokenPairBestEffort,
} from "@/lib/server/tokens";
import { resolveTraceContext } from "@/lib/server/trace";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  const store = await cookies();
  const access = store.get(ACCESS_COOKIE)?.value;
  const refresh = store.get(REFRESH_COOKIE)?.value;
  const clientIp = resolveForwardedClientIp(req.headers);
  const trace = resolveTraceContext(req.headers);

  // Immediately purge the shared refresh grace cache and cancel any in-flight refresh for this
  // token before awaiting upstream calls so concurrent refreshes cannot repopulate the cache.
  const { purgedTokens, inFlightPromise } = await invalidateRefreshCache(refresh);

  try {
    if (access || refresh) {
      await gw<{ ok?: boolean }>("/api/v1/auth/logout", {
        method: "POST",
        body: refresh ? { refresh_token: refresh } : {},
      });
    }
    if (purgedTokens && purgedTokens.accessToken !== access) {
      await revokeTokenPairBestEffort(purgedTokens, trace, clientIp);
    }
    if (inFlightPromise) {
      await inFlightPromise.catch(() => null);
    }
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "rate_limited") {
      throw err;
    }
    if (err instanceof GatewayError && err.kind === "unavailable") {
      logServer("warn", "bff_logout_gateway_unavailable", { trace_id: err.traceId });
    }
    // Clear cookies even if the session was already expired or backend was unreachable.
  } finally {
    await invalidateRefreshCache(refresh);
    if (purgedTokens) {
      await invalidateRefreshCache(purgedTokens.refreshToken);
    }
    clearAuthCookies(store);
  }

  return Response.json({ ok: true }, { headers: { "Cache-Control": "no-store" } });
});
