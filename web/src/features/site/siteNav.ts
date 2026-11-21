export type SiteNavKey = "home" | "products" | "flash" | "vouchers" | "policies" | "about";

export const SITE_NAV_ITEMS: ReadonlyArray<{ key: SiteNavKey; label: string; href: string }> = [
  { key: "products", label: "Sản phẩm", href: "/san-pham" },
  { key: "flash", label: "Flash sale", href: "/flash-sale" },
  { key: "vouchers", label: "Mã giảm giá", href: "/voucher" },
  { key: "about", label: "Giới thiệu", href: "/gioi-thieu" },
];
