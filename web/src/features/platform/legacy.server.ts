// Server-only resolution of old marketplace URLs to the shop's own site.
// Only PUBLISHED sites of ACTIVE shops are returned (the directory and site loaders enforce it).
import { PRODUCTS } from "@/data/products";
import { SHOPS } from "@/data/shops";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { loadPublicSiteProduct } from "@/lib/server/sites";
import { directoryContext, loadAllDirectorySites } from "./directory.server";
import type { DirectoryContext, DirectorySite } from "./directoryModel";
import { legacySiteTarget, shopMatchesLegacySlug } from "./legacyModel";

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function profileNameForShop(contentShopIdOrSlug: string): string | null {
  const shop = SHOPS.find((s) => s.id === contentShopIdOrSlug || s.slug === contentShopIdOrSlug);
  return shop?.name ?? null;
}

async function findSite(
  slug: string,
  profileName: string | null,
  ctx: DirectoryContext
): Promise<DirectorySite | null> {
  const sites = await loadAllDirectorySites(20, ctx);
  return sites.find((site) => shopMatchesLegacySlug(site, slug, profileName)) ?? null;
}

/** `/gian-hang/<slug>` -> the shop site's home page, or null (404). */
export async function resolveLegacyShopUrl(slug: string, req?: Request): Promise<string | null> {
  const ctx = await directoryContext(req);
  const site = await findSite(slug, profileNameForShop(slug), ctx);
  return site ? legacySiteTarget(site.origin) : null;
}

async function backendProductShopId(productId: string): Promise<string | null> {
  try {
    const res = await gw<{ product?: { shop_id?: string } | null }>(
      `/api/v1/products/${encodeURIComponent(productId)}`,
      { auth: "none" }
    );
    return res.product?.shop_id || null;
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") return null;
    throw err;
  }
}

/**
 * `/san-pham/<slug>` -> the product page on its shop's site, or null (404).
 * The product must really be listed on that site, otherwise the visitor gets a 404 here
 * instead of a redirect to a missing page.
 */
export async function resolveLegacyProductUrl(slug: string, req?: Request): Promise<string | null> {
  const ctx = await directoryContext(req);
  let site: DirectorySite | null = null;
  if (UUID_RE.test(slug)) {
    const shopId = await backendProductShopId(slug);
    if (shopId) site = await findSite(shopId, null, ctx);
  } else {
    const content = PRODUCTS.find((p) => p.slug === slug);
    if (content) site = await findSite(content.shopId, profileNameForShop(content.shopId), ctx);
  }
  if (!site) return null;

  const found = await loadPublicSiteProduct(site.host, slug);
  return found && !found.discontinued ? legacySiteTarget(site.origin, found.product.slug) : null;
}
