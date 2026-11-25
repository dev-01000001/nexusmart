// Server-only custom-domain loader, cache revalidation, and BFF error mapping helpers.
// Import only from Server Components and Route Handlers.
import { revalidatePath, revalidateTag } from "next/cache";
import {
  normalizeSiteDomains,
  resolveDomainGatewayError,
  type RawSiteDomain,
  type SiteDomainDTO,
} from "@/lib/site/domains";
import { GatewayError, SAFE_MESSAGES, errorResponse, logServer } from "./errors";
import { gw } from "./gateway";
import { publishedSiteHostCache } from "./siteLookup";

/**
 * Load all domains (default subdomain + optional custom domain) for the authenticated seller's
 * site. Never throws: returns an empty list on 404 (site not created yet) or a safe Vietnamese
 * error message on gateway failure.
 */
export async function loadSellerSiteDomainsServer(): Promise<{
  domains: SiteDomainDTO[];
  error: string | null;
}> {
  try {
    const res = await gw<{ domains?: RawSiteDomain[] }>("/api/v1/seller/site/domains");
    return {
      domains: normalizeSiteDomains(res?.domains),
      error: null,
    };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return { domains: [], error: null };
    }
    return {
      domains: [],
      error: err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable,
    };
  }
}

/**
 * Best-effort invalidation of Next.js cache tags, `/s/[host]` layouts, and the Node runtime's
 * in-memory `publishedSiteHostCache` after domain additions, checks, or removals.
 */
export function revalidateDomainCaches(opts: {
  shopId?: string;
  hosts?: string[];
  clearHostCache?: boolean;
}): void {
  const shopId = opts.shopId?.trim() || "";
  const hosts = new Set<string>();
  for (const raw of opts.hosts ?? []) {
    const clean = raw?.trim().toLowerCase();
    if (clean) hosts.add(clean);
  }

  for (const h of hosts) {
    publishedSiteHostCache.delete(h);
  }
  if (opts.clearHostCache) {
    publishedSiteHostCache.clear();
  }

  try {
    if (shopId) {
      revalidateTag(`site-shop:${shopId}`);
    }
    for (const h of hosts) {
      revalidateTag(`site-host:${h}`);
      revalidatePath(`/s/${h}`, "layout");
    }
  } catch {
    // Revalidation is best-effort outside static-cache contexts.
  }
}

/**
 * Map a domain BFF error into a safe JSON response with `Cache-Control: no-store`.
 * Preserves contract codes (`invalid_hostname`, `domain_taken`, `domain_limit`) while never
 * leaking provider tokens, raw upstream error strings, or stack traces.
 */
export function domainErrorResponse(err: unknown): Response {
  if (err instanceof GatewayError) {
    if (err.kind === "unavailable") {
      logServer("warn", "bff_domain_gateway_unavailable", { trace_id: err.traceId });
    }
    // Only inspect gateway detail strings for 400/409 domain validation/conflict errors so
    // auth, rate-limit, 404, or 5xx internal text can never be misclassified.
    const safeErr =
      err.kind === "invalid" || err.kind === "conflict"
        ? err
        : { kind: err.kind, status: err.status, code: err.code, detail: "" };
    const mapped = resolveDomainGatewayError(safeErr);
    return Response.json(
      { error: { code: mapped.code, message: mapped.message } },
      { status: mapped.status, headers: { "Cache-Control": "no-store" } }
    );
  }
  return errorResponse(err);
}
