// Runtime-agnostic (Edge middleware + Node route handlers) lookup of a PUBLISHED site by host.
// Uses only `fetch`, so middleware and the BFF share one definition of "this host is a live site".
import { GATEWAY_URL, getSiteHostCacheTtls } from "./config.ts";
import { createSiteHostCache, type SiteHostLookupResult } from "../site/cache.ts";

const SITE_LOOKUP_TIMEOUT_MS = 4000;

interface RawByHostResponse {
  site?: { id?: string; shop_id?: string; status?: string; primary_host?: string };
  shop?: { status?: string };
}

/** Pure check of a `/sites/by-host` body: only a PUBLISHED site of an ACTIVE shop counts. */
export function parsePublishedSiteHost(
  data: RawByHostResponse | null | undefined,
  hostname: string
): SiteHostLookupResult {
  const site = data?.site;
  const shopStatus = data?.shop?.status ? String(data.shop.status).toUpperCase() : "ACTIVE";
  if (
    !site?.id ||
    !site?.shop_id ||
    String(site.status ?? "").toUpperCase() !== "PUBLISHED" ||
    shopStatus !== "ACTIVE"
  ) {
    return { status: "not_found" };
  }
  return {
    status: "found",
    siteId: site.id,
    shopId: site.shop_id,
    primaryHost: (site.primary_host || hostname).toLowerCase(),
  };
}

/** Uncached gateway lookup. Never throws: upstream failures resolve to `{ status: "error" }`. */
export async function lookupPublishedSiteHost(hostname: string): Promise<SiteHostLookupResult> {
  try {
    const res = await fetch(
      `${GATEWAY_URL}/api/v1/sites/by-host?host=${encodeURIComponent(hostname)}`,
      {
        method: "GET",
        headers: { Accept: "application/json" },
        cache: "no-store",
        signal: AbortSignal.timeout(SITE_LOOKUP_TIMEOUT_MS),
      }
    );
    if (res.status === 404) return { status: "not_found" };
    if (!res.ok) return { status: "error" };
    return parsePublishedSiteHost((await res.json()) as RawByHostResponse, hostname);
  } catch {
    return { status: "error" };
  }
}

const ttls = getSiteHostCacheTtls();

/** Short-TTL per-process host cache (separate instances in the Edge and Node runtimes). */
export const publishedSiteHostCache = createSiteHostCache({
  positiveTtlMs: ttls.positiveTtlMs,
  negativeTtlMs: ttls.negativeTtlMs,
  maxEntries: 2000,
  maxNegativeEntries: 1000,
});
