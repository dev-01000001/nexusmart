// Server-only loader for the public site directory shown on the root host.
import { headers } from "next/headers";
import { getSiteRootDomain } from "@/lib/server/config";
import { GatewayError, logServer } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import {
  DIRECTORY_MAX_PAGE_SIZE,
  DIRECTORY_PAGE_SIZE,
  normalizeDirectoryResponse,
  type DirectoryContext,
  type DirectoryPage,
  type RawDirectoryResponse,
} from "./directoryModel";

export type DirectoryResult =
  ({ ok: true } & DirectoryPage) | { ok: false; page: number; pageSize: number };

export function directoryContextFromHeaders(
  h: Pick<Headers, "get">,
  fallbackUrl = "http://localhost"
): DirectoryContext {
  return {
    rootDomain: getSiteRootDomain(),
    rawHost: resolveRequestHost(h, "localhost"),
    proto: resolveRequestProto(h, fallbackUrl),
  };
}

export async function directoryContext(req?: Request): Promise<DirectoryContext> {
  if (req) {
    return directoryContextFromHeaders(req.headers, req.url);
  }
  const h = await headers();
  return directoryContextFromHeaders(h, "http://localhost");
}

/**
 * One page of PUBLISHED sites (shop ACTIVE), newest first.
 * Uses uncached `no-store` reads so newly published or suspended sites are reflected immediately.
 * Never throws.
 */
export async function loadSiteDirectory(
  page: number,
  pageSize = DIRECTORY_PAGE_SIZE,
  explicitCtx?: DirectoryContext
): Promise<DirectoryResult> {
  const size = Math.min(Math.max(1, pageSize), DIRECTORY_MAX_PAGE_SIZE);
  try {
    const [raw, ctx] = await Promise.all([
      gw<RawDirectoryResponse>("/api/v1/sites", {
        auth: "none",
        query: { page, page_size: size },
      }),
      explicitCtx ? Promise.resolve(explicitCtx) : directoryContext(),
    ]);
    return { ok: true, ...normalizeDirectoryResponse(raw, ctx, { page, pageSize: size }) };
  } catch (err) {
    logServer("warn", "site_directory_fetch_failed", {
      page,
      kind: err instanceof GatewayError ? err.kind : "unknown",
    });
    return { ok: false, page, pageSize: size };
  }
}

/** Every PUBLISHED site (bounded), for the root sitemap and legacy URL lookups. */
export async function loadAllDirectorySites(
  maxPages = 20,
  explicitCtx?: DirectoryContext
): Promise<DirectoryPage["sites"]> {
  const ctx = explicitCtx ?? (await directoryContext());
  const all: DirectoryPage["sites"] = [];
  for (let page = 1; page <= maxPages; page++) {
    const res = await loadSiteDirectory(page, DIRECTORY_MAX_PAGE_SIZE, ctx);
    if (!res.ok) break;
    all.push(...res.sites);
    if (page >= res.totalPages || res.sites.length === 0) break;
  }
  return all;
}
