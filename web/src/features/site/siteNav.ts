export type SiteNavKey = "home" | "products" | "flash" | "vouchers" | "policies" | "about";

export const SITE_NAV_ITEMS: ReadonlyArray<{ key: SiteNavKey; label: string; href: string }> = [
  { key: "products", label: "Sản phẩm", href: "/san-pham" },
  { key: "flash", label: "Flash sale", href: "/flash-sale" },
  { key: "vouchers", label: "Mã giảm giá", href: "/voucher" },
  { key: "about", label: "Giới thiệu", href: "/gioi-thieu" },
];

/**
 * Strips the internal `/s/<host>` rewrite prefix so storefront paths stay visible-relative (`/`..).
 */
export function stripInternalSitePrefix(rawPath: string | null | undefined): string {
  const path = rawPath || "/";
  const m = path.match(/^\/s\/[^/]+(\/.*)?$/);
  return m ? m[1] || "/" : path;
}
