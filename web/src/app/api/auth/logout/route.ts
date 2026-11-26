import { cookies } from "next/headers";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/server/config";
import { GatewayError, logServer } from "@/lib/server/errors";
import { apiRoute, resolveForwardedClientIp } from "@/lib/server/http";
import { clearAuthCookies, revokeSessionTokens } from "@/lib/server/tokens";
import { resolveTraceContext } from "@/lib/server/trace";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  const store = await cookies();
  const access = store.get(ACCESS_COOKIE)?.value;
  const refresh = store.get(REFRESH_COOKIE)?.value;
  const clientIp = resolveForwardedClientIp(req.headers);
  const trace = resolveTraceContext(req.headers);

  try {
    if (access || refresh) {
      await revokeSessionTokens({
        accessToken: access,
        refreshToken: refresh,
        trace,
        clientIp,
      });
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
    clearAuthCookies(store);
  }

  return Response.json({ ok: true }, { headers: { "Cache-Control": "no-store" } });
});
