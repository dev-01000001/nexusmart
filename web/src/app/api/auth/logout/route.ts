import { cookies } from "next/headers";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/server/config";
import { GatewayError, logServer } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { clearAuthCookies } from "@/lib/server/tokens";

export const POST = apiRoute(async () => {
  const store = await cookies();
  const access = store.get(ACCESS_COOKIE)?.value;
  const refresh = store.get(REFRESH_COOKIE)?.value;

  try {
    if (access || refresh) {
      await gw<{ ok?: boolean }>("/api/v1/auth/logout", {
        method: "POST",
        body: refresh ? { refresh_token: refresh } : {},
      });
    }
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "unavailable") {
      logServer("warn", "bff_logout_gateway_unavailable", { trace_id: err.traceId });
    }
    // Always clear cookies even if the session was already expired or backend was unreachable.
  } finally {
    clearAuthCookies(store);
  }

  return Response.json({ ok: true }, { headers: { "Cache-Control": "no-store" } });
});
