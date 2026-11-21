// Pure shaping of the public site directory (`GET /api/v1/sites`, swarm contract #1).
// No I/O and only relative imports so it runs in Server Components and `node --test` alike.
import { classifyHost, deriveSiteOrigin, normalizeHostHeader } from "../../lib/site/host.ts";
import { sanitizeSafeUrl } from "../../lib/site/theme.ts";

export const DIRECTORY_PAGE_SIZE = 24;
export const DIRECTORY_MAX_PAGE_SIZE = 48;

export interface RawDirectorySite {
  site_id?: unknown;
  shop_id?: unknown;
  shop_name?: unknown;
  subdomain?: unknown;
  primary_host?: unknown;
  logo_url?: unknown;
  cover_url?: unknown;
  tagline?: unknown;
}

export interface RawDirectoryResponse {
  sites?: RawDirectorySite[] | null;
  total?: unknown;
  page?: unknown;
  page_size?: unknown;
}

export interface DirectorySite {
  siteId: string;
  shopId: string;
  name: string;
  /** Bare host shown to people, e.g. `bach-dang.nexusmart.vn`. */
  host: string;
  /** Absolute origin used for links, e.g. `https://bach-dang.nexusmart.vn`. */
  origin: string;
  logoUrl: string;
  coverUrl: string;
  tagline: string;
}

export interface DirectoryPage {
  sites: DirectorySite[];
  total: number;
  page: number;
  pageSize: number;
  totalPages: number;
}

export interface DirectoryContext {
  rootDomain: string;
  /** Incoming request host (keeps the dev port on generated links). */
  rawHost?: string | null;
  proto?: string;
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function positiveInt(value: unknown, fallback: number): number {
  const n = typeof value === "number" ? value : Number(value);
  return Number.isFinite(n) && n >= 1 ? Math.floor(n) : fallback;
}

/** Parse the `?trang=` search param into a page number (1 when missing or invalid). */
export function parseDirectoryPage(raw: string | string[] | undefined): number {
  const value = Array.isArray(raw) ? raw[0] : raw;
  if (!value || !/^\d{1,4}$/.test(value)) return 1;
  return Math.max(1, Number(value));
}

/**
 * Resolve the public host of one directory entry. Only hosts that classify as a site of the
 * configured root domain are accepted, so a bad row can never send visitors to a foreign domain.
 */
function resolveSiteHost(raw: RawDirectorySite, rootDomain: string): string {
  const candidates = [
    str(raw.primary_host),
    str(raw.subdomain) && `${str(raw.subdomain)}.${rootDomain}`,
  ];
  for (const candidate of candidates) {
    if (!candidate) continue;
    const { hostname } = normalizeHostHeader(candidate);
    if (hostname && classifyHost(hostname, rootDomain).kind === "site") return hostname;
  }
  return "";
}

export function normalizeDirectorySite(
  raw: RawDirectorySite,
  ctx: DirectoryContext
): DirectorySite | null {
  const siteId = str(raw.site_id);
  const shopId = str(raw.shop_id);
  const name = str(raw.shop_name);
  if (!siteId || !shopId || !name) return null;

  const host = resolveSiteHost(raw, ctx.rootDomain);
  if (!host) return null;
  const origin = deriveSiteOrigin({ siteHost: host, rawHost: ctx.rawHost, proto: ctx.proto });
  if (!origin) return null;

  return {
    siteId,
    shopId,
    name,
    host,
    origin,
    logoUrl: sanitizeSafeUrl(str(raw.logo_url)),
    coverUrl: sanitizeSafeUrl(str(raw.cover_url)),
    tagline: str(raw.tagline).slice(0, 240),
  };
}

export function normalizeDirectoryResponse(
  raw: RawDirectoryResponse | null | undefined,
  ctx: DirectoryContext,
  requested: { page: number; pageSize: number }
): DirectoryPage {
  const seen = new Set<string>();
  const sites: DirectorySite[] = [];
  for (const row of Array.isArray(raw?.sites) ? raw.sites : []) {
    const site = row ? normalizeDirectorySite(row, ctx) : null;
    if (!site || seen.has(site.siteId)) continue;
    seen.add(site.siteId);
    sites.push(site);
  }

  const pageSize = Math.min(
    positiveInt(raw?.page_size, requested.pageSize),
    DIRECTORY_MAX_PAGE_SIZE
  );
  const page = positiveInt(raw?.page, requested.page);
  const rawTotal = typeof raw?.total === "number" ? raw.total : Number(raw?.total);
  const total = Number.isFinite(rawTotal) && rawTotal >= 0 ? Math.floor(rawTotal) : sites.length;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return { sites, total, page, pageSize, totalPages };
}

/**
 * Compact page list for the pager: always first/last, the current page and its neighbours,
 * `null` marks a gap. Example: (6, 12) -> [1, null, 5, 6, 7, null, 12].
 */
export function buildPageList(current: number, totalPages: number): (number | null)[] {
  if (totalPages <= 1) return [1];
  const pages = new Set<number>([1, totalPages, current - 1, current, current + 1]);
  const sorted = [...pages].filter((p) => p >= 1 && p <= totalPages).sort((a, b) => a - b);
  const out: (number | null)[] = [];
  for (const p of sorted) {
    const prev = out[out.length - 1];
    if (typeof prev === "number" && p - prev > 1) out.push(null);
    out.push(p);
  }
  return out;
}
