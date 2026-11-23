import { createHash } from "node:crypto";
import { NextRequest, NextResponse } from "next/server";
import { verifyCsrfOrigin } from "@/lib/server/csrf";
import type { GatewayRefreshResponse } from "@/lib/gateway-types";
import { GatewayError, gatewayErrorResponse, gatewayFetch } from "@/lib/server/gateway";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  clearSessionCookies,
  extractClientIp,
  invalidateRefreshCache,
  refreshTokensWithGateway,
} from "@/lib/server/session";

export const dynamic = "force-dynamic";

const PENDING_REVOCATION_TTL_MS = 90_000;
const pendingRevocations = new Map<string, { tokens: GatewayRefreshResponse; expiresAt: number }>();

function hashKey(token: string): string {
  return createHash("sha256").update(token).digest("hex");
}

function takePendingRevocation(refreshToken: string | undefined): GatewayRefreshResponse | null {
  if (!refreshToken) return null;
  const now = Date.now();
  for (const [k, v] of pendingRevocations) {
    if (v.expiresAt <= now) pendingRevocations.delete(k);
  }
  const key = hashKey(refreshToken);
  const entry = pendingRevocations.get(key);
  if (!entry) return null;
  pendingRevocations.delete(key);
  return entry.tokens;
}

function stashPendingRevocation(
  refreshToken: string | undefined,
  tokens: GatewayRefreshResponse | null
): void {
  if (!refreshToken || !tokens) return;
  pendingRevocations.set(hashKey(refreshToken), {
    tokens,
    expiresAt: Date.now() + PENDING_REVOCATION_TTL_MS,
  });
}

export async function POST(req: NextRequest) {
  const csrfErr = verifyCsrfOrigin(req);
  if (csrfErr) return csrfErr;

  const accessToken = req.cookies.get(ACCESS_COOKIE)?.value;
  const refreshToken = req.cookies.get(REFRESH_COOKIE)?.value;
  const clientIp = extractClientIp(req.headers);

  // Immediately purge the shared grace cache and cancel any in-flight refresh for this token
  // before awaiting upstream calls so concurrent refreshes cannot repopulate the cache.
  const { purgedTokens: freshlyPurged, inFlightPromise } = invalidateRefreshCache(refreshToken);
  const purgedTokens = freshlyPurged ?? takePendingRevocation(refreshToken);

  let revoked = false;
  if (accessToken) {
    try {
      await gatewayFetch("/auth/logout", {
        method: "POST",
        token: accessToken,
        clientIp,
        body: { refresh_token: refreshToken ?? "" },
      });
      revoked = true;
    } catch (err) {
      if (err instanceof GatewayError && err.status === 429) {
        stashPendingRevocation(refreshToken, purgedTokens);
        return gatewayErrorResponse(err);
      }
      if (!(err instanceof GatewayError && err.status === 401)) {
        // Best effort: still clear cookies below even if gateway is unreachable.
        revoked = true;
      }
    }
  }

  // If refreshToken had just rotated inside the 5s grace window, also revoke the newly
  // issued access token's JTI and refresh family at gateway.
  if (purgedTokens && purgedTokens.access_token !== accessToken) {
    try {
      await gatewayFetch("/auth/logout", {
        method: "POST",
        token: purgedTokens.access_token,
        clientIp,
        body: { refresh_token: purgedTokens.refresh_token },
      });
      revoked = true;
    } catch (err) {
      if (err instanceof GatewayError && err.status === 429) {
        stashPendingRevocation(refreshToken, purgedTokens);
        return gatewayErrorResponse(err);
      }
      // Best effort; caller tokens and cache were already invalidated.
    }
  }

  // Wait for any cancelled in-flight refresh to finish revoking its minted pair.
  if (inFlightPromise) {
    await inFlightPromise;
    revoked = true;
  }

  // Gateway /auth/logout requires a valid access token; if access expired while refresh
  // still exists (and no cached/in-flight rotation already handled it), exchange once so
  // the refresh token family is revoked at services/user.
  let rotatedRefresh: string | undefined;
  if (!revoked && refreshToken) {
    const refreshed = await refreshTokensWithGateway(refreshToken, { clientIp });
    if (refreshed.ok && refreshed.tokens) {
      rotatedRefresh = refreshed.tokens.refresh_token;
      try {
        await gatewayFetch("/auth/logout", {
          method: "POST",
          token: refreshed.tokens.access_token,
          clientIp,
          body: { refresh_token: refreshed.tokens.refresh_token },
        });
      } catch (err) {
        if (err instanceof GatewayError && err.status === 429) {
          stashPendingRevocation(refreshToken, refreshed.tokens);
          return gatewayErrorResponse(err);
        }
        // Ignore upstream errors; cookies are always cleared.
      }
    } else if (refreshed.status === 429) {
      return gatewayErrorResponse(
        new GatewayError(
          429,
          refreshed.code ?? "RESOURCE_EXHAUSTED",
          "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút.",
          refreshed.retryAfter
        )
      );
    }
  }

  invalidateRefreshCache(refreshToken);
  if (rotatedRefresh) {
    invalidateRefreshCache(rotatedRefresh);
  }

  const res = NextResponse.json(
    { ok: true },
    { status: 200, headers: { "Cache-Control": "no-store" } }
  );
  clearSessionCookies(res);
  return res;
}
