// Pure multi-tenant host normalization, classification, blocked-path rules, and cross-host URL derivation.
// Zero I/O so it runs identically in Edge/Node middleware, Server Components, and unit tests.

export const SUBDOMAIN_PATTERN = /^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$/;

/** Reserved infrastructure & brand-impersonation subdomains (mirrors services/user Phase 2.A). */
export const RESERVED_SUBDOMAINS: ReadonlySet<string> = new Set([
  "www",
  "api",
  "admin",
  "seller",
  "app",
  "mail",
  "smtp",
  "imap",
  "pop",
  "static",
  "cdn",
  "assets",
  "media",
  "img",
  "images",
  "files",
  "auth",
  "login",
  "sso",
  "oauth",
  "account",
  "accounts",
  "checkout",
  "cart",
  "pay",
  "payment",
  "payments",
  "billing",
  "shop",
  "store",
  "storefront",
  "s",
  "status",
  "health",
  "docs",
  "help",
  "support",
  "blog",
  "news",
  "portal",
  "dashboard",
  "console",
  "internal",
  "staging",
  "dev",
  "test",
  "demo",
  "localhost",
  "nexus",
  "nexusmart",
  "nexuspay",
  "vietcombank",
  "techcombank",
  "bidv",
  "agribank",
  "momo",
  "zalopay",
  "vnpay",
  "shopee",
  "lazada",
  "tiki",
  "gov",
  "chinhphu",
]);

/** Brand/bank prefixes rejected even when followed by a hyphen (e.g. `vietcombank-uudai`). */
export const FORBIDDEN_BRAND_PREFIXES: readonly string[] = [
  "nexus",
  "nexusmart",
  "nexuspay",
  "vietcombank",
  "techcombank",
  "bidv",
  "agribank",
  "momo",
  "zalopay",
  "vnpay",
  "shopee",
  "lazada",
  "tiki",
  "gov",
  "chinhphu",
];

export interface ParsedHost {
  /** Normalized hostname (lowercased, no port, no brackets, no trailing dot). Empty if malformed. */
  hostname: string;
  /** Optional numeric port string (e.g. "3300"). */
  port: string;
}

const INVALID_HOST_CHARS_RE = /[\s/@?#\\,\u0000-\u001F\u007F]/;

/**
 * Normalize an incoming `Host` (or trusted `X-Forwarded-Host`) header value:
 * lowercase, strip `:port`, strip trailing `.`, and reject malformed host values.
 */
export function normalizeHostHeader(rawHost: string | null | undefined): ParsedHost {
  if (!rawHost) return { hostname: "", port: "" };
  const trimmed = rawHost.trim().toLowerCase();
  if (!trimmed || INVALID_HOST_CHARS_RE.test(trimmed)) {
    return { hostname: "", port: "" };
  }

  // Bracketed IPv6 literal, e.g. `[::1]:3300` or `[::1]`
  if (trimmed.startsWith("[")) {
    const closeIdx = trimmed.indexOf("]");
    if (closeIdx <= 1) return { hostname: "", port: "" };
    const ipv6 = trimmed.slice(1, closeIdx);
    const rest = trimmed.slice(closeIdx + 1);
    if (!isIpv6Literal(ipv6)) return { hostname: "", port: "" };
    if (!rest) return { hostname: ipv6, port: "" };
    if (rest.startsWith(":")) {
      const port = rest.slice(1);
      if (/^\d{1,5}$/.test(port)) return { hostname: ipv6, port };
    }
    return { hostname: "", port: "" };
  }

  // Bare IPv6 literal without brackets (contains 2+ colons)
  const firstColon = trimmed.indexOf(":");
  const lastColon = trimmed.lastIndexOf(":");
  if (firstColon !== -1 && firstColon !== lastColon) {
    return isIpv6Literal(trimmed) ? { hostname: trimmed, port: "" } : { hostname: "", port: "" };
  }

  let hostPart = trimmed;
  let port = "";
  if (firstColon !== -1) {
    hostPart = trimmed.slice(0, firstColon);
    port = trimmed.slice(firstColon + 1);
    if (!/^\d{1,5}$/.test(port)) return { hostname: "", port: "" };
  }

  // Strip trailing DNS root dot(s)
  hostPart = hostPart.replace(/\.+$/, "");
  if (!hostPart) return { hostname: "", port: "" };

  return { hostname: hostPart, port };
}

export function isIpv4Literal(hostname: string): boolean {
  const parts = hostname.split(".");
  if (parts.length !== 4) return false;
  return parts.every((p) => {
    if (!/^\d{1,3}$/.test(p)) return false;
    const n = Number(p);
    return n >= 0 && n <= 255;
  });
}

export function isIpv6Literal(hostname: string): boolean {
  return hostname.includes(":") && /^[0-9a-f:.]+$/i.test(hostname);
}

export function isIpLiteral(hostname: string): boolean {
  return isIpv4Literal(hostname) || isIpv6Literal(hostname);
}

export function isReservedSubdomain(subdomain: string): boolean {
  const clean = subdomain.trim().toLowerCase();
  if (!clean) return true;
  if (clean.startsWith("xn--")) return true;
  if (RESERVED_SUBDOMAINS.has(clean)) return true;
  return FORBIDDEN_BRAND_PREFIXES.some((prefix) => clean.startsWith(`${prefix}-`));
}

export function isValidSubdomainSyntax(subdomain: string): boolean {
  const clean = subdomain.trim().toLowerCase();
  return SUBDOMAIN_PATTERN.test(clean) && !isReservedSubdomain(clean);
}

export type HostClassification =
  | { kind: "root"; hostname: string; port: string }
  | { kind: "site"; hostname: string; port: string; subdomain: string }
  | { kind: "unknown"; hostname: string; port: string; reason: string };

/**
 * Classify a request Host against `rootDomain` (default `nexusmart.localhost`):
 * - `root`: marketplace host (`rootDomain`, `localhost`, `web`, or IPv4/IPv6 literal)
 * - `site`: valid non-reserved single-label `<subdomain>.<rootDomain>`
 * - `unknown`: foreign domain, multi-level subdomain, reserved label, or malformed host
 */
export function classifyHost(
  rawHost: string | null | undefined,
  rootDomain = "nexusmart.localhost"
): HostClassification {
  const { hostname, port } = normalizeHostHeader(rawHost);
  if (!hostname) {
    return { kind: "unknown", hostname: "", port: "", reason: "malformed_host" };
  }

  const cleanRoot = rootDomain
    .trim()
    .toLowerCase()
    .replace(/^\.+|\.+$/g, "");

  if (
    hostname === cleanRoot ||
    hostname === "localhost" ||
    hostname === "web" ||
    isIpLiteral(hostname)
  ) {
    return { kind: "root", hostname, port };
  }

  const suffix = `.${cleanRoot}`;
  if (!hostname.endsWith(suffix)) {
    return { kind: "unknown", hostname, port, reason: "foreign_domain" };
  }

  const label = hostname.slice(0, hostname.length - suffix.length);
  if (!label || label.includes(".")) {
    return { kind: "unknown", hostname, port, reason: "multi_level_subdomain" };
  }

  if (!SUBDOMAIN_PATTERN.test(label)) {
    return { kind: "unknown", hostname, port, reason: "invalid_subdomain_syntax" };
  }

  if (isReservedSubdomain(label)) {
    return { kind: "unknown", hostname, port, reason: "reserved_subdomain" };
  }

  return { kind: "site", hostname, port, subdomain: label };
}

function matchesPrefix(pathname: string, prefix: string): boolean {
  return pathname === prefix || pathname.startsWith(`${prefix}/`);
}

/** Direct external access to `/s` or `/s/...` must 404 on ANY host (root or site). */
export function isDirectInternalSitePath(pathname: string): boolean {
  return matchesPrefix(pathname, "/s");
}

const SITE_BLOCKED_PREFIXES = ["/api", "/seller", "/admin", "/s"] as const;

/** Routes forbidden on subdomain site hosts (must return 404 immediately). */
export function isBlockedSitePath(pathname: string): boolean {
  return SITE_BLOCKED_PREFIXES.some((prefix) => matchesPrefix(pathname, prefix));
}

export function isSiteMetadataPath(pathname: string): boolean {
  return pathname === "/robots.txt" || pathname === "/sitemap.xml";
}

function formatHostWithPort(hostname: string, port: string): string {
  const formattedHost = isIpv6Literal(hostname) ? `[${hostname}]` : hostname;
  return port ? `${formattedHost}:${port}` : formattedHost;
}

/**
 * Derive the main marketplace origin for checkout handoff and PDP canonical URLs.
 * Uses `appOrigin` when explicitly configured; otherwise keeps the request's protocol + port
 * and strips the shop subdomain back to `rootDomain`.
 */
export function deriveMarketplaceOrigin(opts: {
  rawHost: string | null | undefined;
  proto?: string;
  rootDomain?: string;
  appOrigin?: string;
}): string {
  const cleanAppOrigin = (opts.appOrigin || "").trim().replace(/\/+$/, "");
  if (cleanAppOrigin) return cleanAppOrigin;

  const rootDomain = (opts.rootDomain || "nexusmart.localhost")
    .trim()
    .toLowerCase()
    .replace(/^\.+|\.+$/g, "");
  const proto = opts.proto === "https" ? "https" : "http";
  const cls = classifyHost(opts.rawHost, rootDomain);

  if (cls.kind === "site") {
    return `${proto}://${formatHostWithPort(rootDomain, cls.port)}`;
  }
  if (cls.kind === "root") {
    return `${proto}://${formatHostWithPort(cls.hostname, cls.port)}`;
  }
  return `${proto}://${rootDomain}`;
}

/**
 * Derive the public origin for a shop's subdomain site, preserving the incoming port in dev
 * (e.g. `http://bach-dang.nexusmart.localhost:3300`) unless the site host already carries one.
 */
export function deriveSiteOrigin(opts: {
  siteHost: string;
  rawHost?: string | null;
  proto?: string;
}): string {
  const proto = opts.proto === "https" ? "https" : "http";
  const parsedSite = normalizeHostHeader(opts.siteHost);
  if (!parsedSite.hostname) return "";
  const reqPort = parsedSite.port || normalizeHostHeader(opts.rawHost).port;
  return `${proto}://${formatHostWithPort(parsedSite.hostname, reqPort)}`;
}

/**
 * Build the marketplace cart handoff URL from a subdomain site Buy/Add-to-cart action.
 */
export function buildStorefrontCartUrl(opts: {
  marketplaceOrigin: string;
  shopId: string;
  siteHost: string;
  skuId?: string;
  qty?: number;
  flashSaleItemId?: string;
}): string {
  const base = opts.marketplaceOrigin.replace(/\/+$/, "");
  const params = new URLSearchParams();
  params.set("shop", opts.shopId);
  params.set("channel", "storefront");
  if (opts.skuId) {
    params.set("sku", opts.skuId);
    params.set("qty", String(Math.max(1, Math.trunc(opts.qty ?? 1))));
  }
  if (opts.flashSaleItemId) {
    params.set("flash", opts.flashSaleItemId);
  }
  if (opts.siteHost) {
    params.set("site", opts.siteHost);
  }
  return `${base}/gio-hang?${params.toString()}`;
}
