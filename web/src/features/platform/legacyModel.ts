// Pure rules for redirecting old marketplace URLs (`/gian-hang/<slug>`, `/san-pham/<slug>`)
// on the root host to the matching shop site. Client-safe, unit tested.
import type { DirectorySite } from "./directoryModel";

function foldName(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * True when an old shop slug points at this site: the backend shop id, the site's subdomain,
 * or the display name of the old storefront profile that used `slug`.
 */
export function shopMatchesLegacySlug(
  site: DirectorySite,
  slug: string,
  profileName: string | null | undefined
): boolean {
  const clean = slug.trim().toLowerCase();
  if (!clean) return false;
  if (site.shopId.toLowerCase() === clean) return true;
  if (site.host.split(".")[0] === clean) return true;
  return Boolean(profileName) && foldName(site.name) === foldName(profileName ?? "");
}

/** Absolute URL on the shop site: its home page, or a product page when `productSlug` is set. */
export function legacySiteTarget(origin: string, productSlug?: string): string {
  const base = origin.replace(/\/+$/, "");
  return productSlug ? `${base}/san-pham/${encodeURIComponent(productSlug)}` : `${base}/`;
}
