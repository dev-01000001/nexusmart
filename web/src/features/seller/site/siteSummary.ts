// Pure helpers describing a seller's storefront site for Seller Center chrome (shell + overview).
// No Next.js/Node runtime imports so it runs in Server Components, client components and `node --test`.

export type SellerSiteState = "none" | "draft" | "published" | "suspended";

export interface SellerSiteSummary {
  state: SellerSiteState;
  /** Public host of the site (e.g. `bach-dang.nexusmart.vn`), empty when no site exists yet. */
  host: string;
  /** Absolute public URL, only set when the site is PUBLISHED and buyers can reach it. */
  url: string;
  suspendedReason: string;
}

function splitHostPort(raw: string): { hostname: string; port: string } {
  const value = raw.trim().toLowerCase();
  if (!value) return { hostname: "", port: "" };
  if (value.startsWith("[")) {
    const end = value.indexOf("]");
    if (end < 0) return { hostname: "", port: "" };
    const rest = value.slice(end + 1);
    return {
      hostname: value.slice(0, end + 1),
      port: /^:\d{1,5}$/.test(rest) ? rest.slice(1) : "",
    };
  }
  const idx = value.lastIndexOf(":");
  if (idx > 0 && value.indexOf(":") === idx) {
    const port = value.slice(idx + 1);
    if (!/^\d{1,5}$/.test(port)) return { hostname: "", port: "" };
    return { hostname: value.slice(0, idx), port };
  }
  return { hostname: value, port: "" };
}

/**
 * Build the public URL of a shop site. The site host comes from the backend; the request host only
 * contributes its port (so `bach-dang.nexusmart.localhost` opened from `nexusmart.localhost:3300` in
 * dev resolves to `:3300`). `proto` is `https` only when explicitly given.
 */
export function buildPublicSiteUrl(
  siteHost: string,
  requestHost?: string | null,
  proto?: string | null
): string {
  const site = splitHostPort(siteHost || "");
  if (!site.hostname || !/^[a-z0-9.\-[\]:]+$/.test(site.hostname)) return "";
  const port = site.port || splitHostPort(requestHost || "").port;
  const scheme =
    (proto || "").trim().toLowerCase().replace(/:$/, "") === "https" ? "https" : "http";
  return `${scheme}://${site.hostname}${port ? `:${port}` : ""}`;
}

export function summarizeSellerSite(
  site: { status?: string; primary_host?: string; suspended_reason?: string } | null | undefined,
  requestHost?: string | null,
  proto?: string | null
): SellerSiteSummary {
  if (!site) return { state: "none", host: "", url: "", suspendedReason: "" };
  const host = (site.primary_host || "").trim().toLowerCase();
  const status = (site.status || "").trim().toUpperCase();
  if (status === "PUBLISHED") {
    const url = buildPublicSiteUrl(host, requestHost, proto);
    return { state: url ? "published" : "draft", host, url, suspendedReason: "" };
  }
  if (status === "SUSPENDED") {
    return {
      state: "suspended",
      host,
      url: "",
      suspendedReason: (site.suspended_reason || "").trim(),
    };
  }
  return { state: "draft", host, url: "", suspendedReason: "" };
}
