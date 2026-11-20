import { cookies } from "next/headers";
import { REFRESH_COOKIE } from "@/lib/server/config";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { clearAuthCookies, rotateRefreshToken, setAuthCookies } from "@/lib/server/tokens";

export const POST = apiRoute(async () => {
  const store = await cookies();
  const refresh = store.get(REFRESH_COOKIE)?.value;
  if (!refresh) {
    clearAuthCookies(store);
    throw new GatewayError("unauthenticated");
  }

  const pair = await rotateRefreshToken(refresh);
  if (!pair) {
    clearAuthCookies(store);
    throw new GatewayError("unauthenticated");
  }

  setAuthCookies(store, pair);
  return Response.json({ ok: true }, { headers: { "Cache-Control": "no-store" } });
});
