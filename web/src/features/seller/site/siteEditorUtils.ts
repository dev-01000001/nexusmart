// Pure client & server utilities for Seller Site Editor (v1 backwards compatible + v2 Template Studio):
// WCAG 2.1 contrast calculation, subdomain rules, SafeURL/ImageURL/XSS checks, presets, section/block defaults.

import type {
  SectionSettings,
  SiteArchetype,
  SiteBlockType,
  SiteCardStyle,
  SiteCartLayout,
  SiteCatalogLayout,
  SiteConfig,
  SiteContainerWidth,
  SiteDensity,
  SiteFooterStyle,
  SiteHeaderStyle,
  SiteInfoLayout,
  SitePdpGalleryStyle,
  SitePdpLayout,
  SiteRadius,
  SiteSection,
  SiteSectionBlock,
  SiteSectionType,
  SiteSectionV2,
  SiteSectionV2Type,
  SiteTemplateConfig,
  SiteTheme,
  TemplatePageKey,
} from "./siteEditorTypes";
import {
  ALLOWED_BLOCK_TYPES,
  ALLOWED_V2_SECTION_TYPES,
  MAX_BLOCKS_PER_SECTION,
  MAX_CONFIG_BYTES,
  MAX_HOME_SECTIONS,
  MAX_PAGE_SECTIONS,
  MAX_TOTAL_SECTIONS,
  SECTION_VARIANTS_BY_TYPE,
  SITE_SCHEMA_VERSION_V2,
} from "../../site/templateTypes.ts";
import {
  checkUnsafeText,
  createDefaultLayoutConfig,
  createDefaultPagesConfig,
  normalizeSiteTemplateConfig,
  validateImageUrlStrict,
  validateSafeHrefStrict,
  validateSiteTemplateJson,
} from "../../site/templateValidator.ts";

export {
  ALLOWED_BLOCK_TYPES,
  ALLOWED_V2_SECTION_TYPES,
  MAX_BLOCKS_PER_SECTION,
  MAX_CONFIG_BYTES,
  MAX_HOME_SECTIONS,
  MAX_PAGE_SECTIONS,
  MAX_TOTAL_SECTIONS,
  SECTION_VARIANTS_BY_TYPE,
  SITE_SCHEMA_VERSION_V2,
  checkUnsafeText,
  createDefaultLayoutConfig,
  createDefaultPagesConfig,
  normalizeSiteTemplateConfig,
  validateImageUrlStrict,
  validateSafeHrefStrict,
  validateSiteTemplateJson,
};

export const MAX_SECTIONS = 20;
export const MIN_WCAG_CONTRAST = 4.5;

export interface ThemePalettePreset {
  id: string;
  name: string;
  description: string;
  colors: Pick<
    SiteTheme,
    "bg_color" | "surface_color" | "text_color" | "accent_color" | "accent_text_color"
  >;
}

/** All presets satisfy WCAG 2.1 >= 4.5:1 on every required pair. */
export const THEME_PRESETS: readonly ThemePalettePreset[] = [
  {
    id: "frost-slate",
    name: "Sương & Đá",
    description: "Nền sáng tinh gọn, nhấn xanh ngọc đậm.",
    colors: {
      bg_color: "#F7F9FA",
      surface_color: "#FFFFFF",
      text_color: "#1F3038",
      accent_color: "#0D7480",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "pine-linen",
    name: "Xưởng Mộc Xanh",
    description: "Nền đá sáng ấm, nhấn xanh lá rừng.",
    colors: {
      bg_color: "#F4F5F2",
      surface_color: "#FFFFFF",
      text_color: "#1F2622",
      accent_color: "#1E5C45",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "harbor-navy",
    name: "Hải Cảng Xanh Than",
    description: "Nền xanh sương nhẹ, chữ xanh than đậm.",
    colors: {
      bg_color: "#F2F5F9",
      surface_color: "#FFFFFF",
      text_color: "#17263B",
      accent_color: "#1E4B7A",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "studio-charcoal",
    name: "Studio Than Đêm",
    description: "Nền tối sâu, điểm nhấn xanh biển sáng.",
    colors: {
      bg_color: "#14191F",
      surface_color: "#1E252D",
      text_color: "#F2F5F8",
      accent_color: "#5CC6D0",
      accent_text_color: "#10161B",
    },
  },
];

export const QUICK_MEDIA_ASSETS: ReadonlyArray<{ label: string; url: string }> = [
  { label: "Xưởng dệt Việt Nam", url: "/banners/vietnam-workshop.jpg" },
  { label: "Cơ bản hằng ngày", url: "/banners/campaign-everyday.jpg" },
  { label: "Công sở gọn gàng", url: "/banners/campaign-office.jpg" },
  { label: "Chạy bộ & Tập luyện", url: "/banners/campaign-running.jpg" },
  { label: "Sợi Xanh Nexus", url: "/banners/campaign-green-fiber.jpg" },
  { label: "Cận cảnh chất vải", url: "/banners/fabric-closeup.jpg" },
];

export const SECTION_TYPE_META: Record<SiteSectionType, { label: string; description: string }> = {
  hero: {
    label: "Banner mở đầu",
    description: "Tiêu đề lớn, ảnh chủ đạo và nút điều hướng đầu trang.",
  },
  featured_products: {
    label: "Sản phẩm nổi bật",
    description: "Hiển thị sản phẩm tiêu biểu của riêng gian hàng.",
  },
  collection: {
    label: "Bộ sưu tập / Danh mục",
    description: "Giới thiệu một dòng sản phẩm hoặc danh mục cụ thể.",
  },
  text: {
    label: "Khối câu chuyện",
    description: "Giới thiệu xưởng may, chất liệu hoặc thông điệp thương hiệu.",
  },
  banner: {
    label: "Dải quảng bá",
    description: "Khối nhấn ngang kèm ảnh và nút xem ưu đãi.",
  },
  voucher_strip: {
    label: "Mã giảm giá gian hàng",
    description: "Hiển thị các mã giảm giá riêng của gian hàng để khách lưu.",
  },
  policies: {
    label: "Cam kết & Chính sách",
    description: "Danh sách chính sách đổi trả, kiểm hàng và giao nhận.",
  },
};

export const SECTION_TYPE_ORDER: readonly SiteSectionType[] = [
  "hero",
  "featured_products",
  "collection",
  "voucher_strip",
  "banner",
  "text",
  "policies",
];

export const SECTION_V2_TYPE_META: Record<
  SiteSectionV2Type,
  { label: string; description: string }
> = {
  ...SECTION_TYPE_META,
  lookbook: {
    label: "Lookbook hình ảnh",
    description: "Trưng bày khung ảnh chiến dịch bất đối xứng hoặc song song cùng block ảnh.",
  },
  metrics_strip: {
    label: "Dải chỉ số & thông số",
    description: "Nêu bật định lượng GSM, độ co rút, thời gian giao hàng qua block thông số.",
  },
  sku_matrix: {
    label: "Bảng ma trận SKU",
    description: "Liệt kê nhanh các biến thể màu, size, giá VND và tồn kho thực tế.",
  },
  testimonials: {
    label: "Đánh giá khách hàng",
    description: "Trích dẫn phản hồi thực tế của người mua qua block trích dẫn.",
  },
};

export const SECTION_V2_TYPE_ORDER: readonly SiteSectionV2Type[] = [
  "hero",
  "featured_products",
  "collection",
  "lookbook",
  "metrics_strip",
  "sku_matrix",
  "voucher_strip",
  "banner",
  "testimonials",
  "text",
  "policies",
];

export const BLOCK_TYPE_META: Record<SiteBlockType, { label: string; description: string }> = {
  heading: { label: "Tiêu đề phụ", description: "Tiêu đề cấp H2 hoặc H3 bên trong khối." },
  paragraph: { label: "Đoạn văn", description: "Đoạn văn bản thuần bổ sung thông tin." },
  badge: { label: "Nhãn ngắn", description: "Nhãn chất liệu, định lượng hoặc điểm nhấn." },
  cta: { label: "Nút liên kết", description: "Nút điều hướng nội bộ hoặc https://." },
  image: { label: "Ảnh minh họa", description: "Khung ảnh có chú thích và tỷ lệ cố định." },
  stat: { label: "Chỉ số số liệu", description: "Con số nổi bật kèm nhãn và ghi chú." },
  policy: { label: "Mục cam kết", description: "Một tiêu chí chính sách ngắn gọn." },
  quote: { label: "Trích dẫn", description: "Nhận xét của khách hàng hoặc ban biên tập." },
  feature: { label: "Tính năng nổi bật", description: "Điểm mạnh sản phẩm kèm nhãn phân loại." },
};

export const BLOCK_TYPE_ORDER: readonly SiteBlockType[] = [
  "badge",
  "heading",
  "paragraph",
  "cta",
  "image",
  "stat",
  "feature",
  "quote",
  "policy",
];

export const TEMPLATE_PAGE_TABS: ReadonlyArray<{
  key: TemplatePageKey;
  label: string;
  shortLabel: string;
  maxSections: number;
}> = [
  { key: "home", label: "Trang chủ", shortLabel: "Trang chủ", maxSections: MAX_HOME_SECTIONS },
  {
    key: "catalog",
    label: "Danh sách SP",
    shortLabel: "Danh sách",
    maxSections: MAX_PAGE_SECTIONS,
  },
  { key: "pdp", label: "Chi tiết SP", shortLabel: "Chi tiết SP", maxSections: MAX_PAGE_SECTIONS },
  { key: "cart", label: "Giỏ hàng", shortLabel: "Giỏ hàng", maxSections: MAX_PAGE_SECTIONS },
  {
    key: "info",
    label: "Trang thông tin",
    shortLabel: "Thông tin",
    maxSections: MAX_PAGE_SECTIONS,
  },
];

export const ARCHETYPE_OPTIONS: ReadonlyArray<{ value: SiteArchetype; label: string }> = [
  { value: "minimal-fashion", label: "Thời trang tối giản" },
  { value: "editorial", label: "Tạp chí Editorial" },
  { value: "dense-grid", label: "Lưới dày hiệu suất" },
  { value: "craft-atelier", label: "Xưởng thủ công" },
  { value: "sport-active", label: "Thể thao năng động" },
  { value: "luxury-maison", label: "Cao cấp Maison" },
  { value: "one-page-launch", label: "Một trang (Landing)" },
  { value: "multi-sku-catalog", label: "Catalog nhiều SKU" },
  { value: "classic", label: "Tiêu chuẩn (Classic)" },
];

export const HEADER_STYLE_OPTIONS: ReadonlyArray<{ value: SiteHeaderStyle; label: string }> = [
  { value: "minimal-split", label: "Tối giản 1 hàng" },
  { value: "centered-masthead", label: "Masthead tạp chí căn giữa" },
  { value: "utility-compact", label: "Thanh tiện ích gọn mật độ cao" },
  { value: "atelier-framed", label: "Khung xưởng thủ công" },
  { value: "bold-dark", label: "Thể thao tương phản đậm" },
  { value: "maison-centered", label: "Boutique căn giữa rộng" },
  { value: "anchor-minimal", label: "Landing điều hướng neo" },
  { value: "catalog-rail", label: "Thanh Catalog đa SKU" },
];

export const FOOTER_STYLE_OPTIONS: ReadonlyArray<{ value: SiteFooterStyle; label: string }> = [
  { value: "minimal-row", label: "Một hàng tối giản" },
  { value: "editorial-cols", label: "Nhiều cột kiểu tạp chí" },
  { value: "compact-bar", label: "Thanh gọn mật độ cao" },
  { value: "atelier-footer", label: "Chân trang hồ sơ xưởng" },
  { value: "bold-dark", label: "Nền đậm thể thao" },
  { value: "maison-minimal", label: "Boutique thanh lịch" },
];

export const CONTAINER_WIDTH_OPTIONS: ReadonlyArray<{ value: SiteContainerWidth; label: string }> =
  [
    { value: "narrow", label: "Hẹp (Boutique, 1080px)" },
    { value: "standard", label: "Tiêu chuẩn (1240px)" },
    { value: "wide", label: "Rộng (1400px)" },
    { value: "full", label: "Toàn chiều ngang (Kho/Lưới dày)" },
  ];

export const CARD_STYLE_OPTIONS: ReadonlyArray<{ value: SiteCardStyle; label: string }> = [
  { value: "minimal", label: "Tối giản không viền nặng" },
  { value: "editorial", label: "Editorial kèm ghi chú chất liệu" },
  { value: "dense", label: "Mật độ cao kèm tồn kho" },
  { value: "atelier", label: "Xưởng may kèm thông số GSM" },
  { value: "sport", label: "Thể thao tương phản mạnh" },
  { value: "luxury", label: "Khung phòng trưng bày Maison" },
  { value: "compact-row", label: "Hàng ngang tóm tắt SKU" },
];

export const RADIUS_OPTIONS: ReadonlyArray<{ value: SiteRadius; label: string }> = [
  { value: "", label: "Theo mặc định mẫu" },
  { value: "none", label: "Vuông sắc nét (0px)" },
  { value: "sm", label: "Bo nhẹ (4px)" },
  { value: "md", label: "Bo vừa (8px)" },
  { value: "lg", label: "Bo mềm (12px)" },
];

export const DENSITY_OPTIONS: ReadonlyArray<{ value: SiteDensity; label: string }> = [
  { value: "", label: "Theo mặc định mẫu" },
  { value: "compact", label: "Gọn mật độ cao" },
  { value: "balanced", label: "Cân bằng" },
  { value: "airy", label: "Thoáng rộng" },
];

export const CATALOG_LAYOUT_OPTIONS: ReadonlyArray<{ value: SiteCatalogLayout; label: string }> = [
  { value: "minimal-gallery", label: "Gallery tối giản" },
  { value: "editorial-magazine", label: "Tạp chí 2 cột rộng" },
  { value: "top-bar", label: "Thanh lọc trên + lưới dày" },
  { value: "sidebar-left", label: "Thanh lọc cố định bên trái" },
  { value: "luxury-curated", label: "Phòng trưng bày 2 cột" },
  { value: "single-strip", label: "Dải thẻ ngang tinh gọn" },
  { value: "sku-table", label: "Bảng ma trận đa SKU + lưới" },
];

export const PDP_LAYOUT_OPTIONS: ReadonlyArray<{ value: SitePdpLayout; label: string }> = [
  { value: "classic-split", label: "Chia đôi tiêu chuẩn" },
  { value: "editorial-tall", label: "Ảnh cuộn dọc + hộp mua cố định" },
  { value: "dense-spec", label: "3 vùng thông số gọn" },
  { value: "atelier-story", label: "Kèm dải câu chuyện xưởng" },
  { value: "sport-telemetry", label: "Chỉ số hiệu năng thể thao" },
  { value: "luxury-framed", label: "Khung trưng bày Maison" },
  { value: "one-page-drawer", label: "Chốt đơn gọn một màn hình" },
  { value: "sku-order-matrix", label: "Bảng ma trận chọn nhanh SKU" },
];

export const PDP_GALLERY_OPTIONS: ReadonlyArray<{ value: SitePdpGalleryStyle; label: string }> = [
  { value: "thumbnails", label: "Ảnh chính + ảnh thu nhỏ" },
  { value: "stacked", label: "Ảnh xếp dọc toàn khổ" },
  { value: "framed", label: "Khung viền phòng trưng bày" },
  { value: "compact", label: "Khung ảnh gọn" },
];

export const CART_LAYOUT_OPTIONS: ReadonlyArray<{ value: SiteCartLayout; label: string }> = [
  { value: "split-summary", label: "Chia đôi + tóm tắt bên phải" },
  { value: "compact-table", label: "Bảng gọn mật độ cao" },
  { value: "editorial-ledger", label: "Phiếu đơn kiểu tạp chí" },
  { value: "atelier-bag", label: "Túi hàng xưởng may" },
  { value: "express-bar", label: "Thanh xác nhận nhanh" },
  { value: "luxury-folio", label: "Folio cao cấp" },
];

export const INFO_LAYOUT_OPTIONS: ReadonlyArray<{ value: SiteInfoLayout; label: string }> = [
  { value: "split-hero", label: "Ảnh + giới thiệu chia đôi" },
  { value: "editorial-essay", label: "Bài luận kèm mục lục" },
  { value: "craft-timeline", label: "Dòng thời gian quy trình xưởng" },
  { value: "athletic-manifesto", label: "Tuyên ngôn vận động" },
  { value: "luxury-maison", label: "Câu chuyện Maison" },
  { value: "compact-faq", label: "Hỏi đáp & chính sách gọn" },
  { value: "b2b-catalog-sheet", label: "Hồ sơ năng lực & bảng sỉ" },
];

export const SECTION_SURFACE_OPTIONS: ReadonlyArray<{
  value: NonNullable<SectionSettings["surface"]>;
  label: string;
}> = [
  { value: "", label: "Mặc định" },
  { value: "bg", label: "Nền trang chính" },
  { value: "surface", label: "Nền thẻ nổi" },
  { value: "band", label: "Dải tương phản đậm" },
  { value: "accent-soft", label: "Nền màu nhấn nhẹ" },
];

export const SECTION_PADDING_OPTIONS: ReadonlyArray<{
  value: NonNullable<SectionSettings["padding"]>;
  label: string;
}> = [
  { value: "", label: "Mặc định" },
  { value: "compact", label: "Gọn" },
  { value: "normal", label: "Tiêu chuẩn" },
  { value: "generous", label: "Thoáng rộng" },
];

export const SECTION_VARIANT_LABELS: Record<string, string> = {
  "": "Mặc định",
  split: "Chia đôi chữ & ảnh",
  fullbleed: "Ảnh toàn khung",
  editorial: "Tạp chí Editorial",
  centered: "Căn giữa tối giản",
  "compact-bar": "Thanh gọn mật độ cao",
  atelier: "Xưởng thủ công",
  "sport-diagonal": "Thể thao đường cắt chéo",
  "luxury-framed": "Khung trưng bày cao cấp",
  "grid-4": "Lưới 4 cột",
  "grid-3": "Lưới 3 cột",
  "grid-2": "Lưới 2 cột lớn",
  "dense-5": "Lưới dày 5 cột",
  "editorial-asym": "Lưới bất đối xứng",
  "list-sku": "Danh sách kèm tóm tắt SKU",
  compact: "Gọn",
  cards: "Dạng thẻ",
  pullquote: "Trích dẫn nhấn mạnh",
  monograph: "Chuyên khảo căn giữa",
  band: "Dải ngang đậm",
  "split-image": "Chia đôi kèm ảnh",
  framed: "Khung viền nổi bật",
  grid: "Lưới cột",
  timeline: "Dòng thời gian từng bước",
  "compact-list": "Danh sách gọn",
  "asym-3": "Bất đối xứng 1 lớn + 2 nhỏ",
  "duo-split": "Song song 2 khung",
  "strip-4": "Dải 4 khung ảnh",
  "bar-4": "Thanh ngang 4 chỉ số",
  "cards-3": "3 thẻ số liệu",
  telemetry: "Bảng chỉ số hiệu năng",
  "compact-table": "Bảng SKU gọn",
  grouped: "Nhóm theo sản phẩm",
  "editorial-quotes": "Trích dẫn tạp chí",
};

const SUBDOMAIN_RE = /^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$/;

const RESERVED_SUBDOMAINS = new Set([
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

const FORBIDDEN_BRAND_PREFIXES = [
  "nexus-",
  "nexusmart-",
  "nexuspay-",
  "vietcombank-",
  "techcombank-",
  "bidv-",
  "agribank-",
  "momo-",
  "zalopay-",
  "vnpay-",
  "shopee-",
  "lazada-",
  "tiki-",
  "gov-",
  "chinhphu-",
];

export function normalizeSubdomainInput(raw: string): string {
  return raw.trim().toLowerCase();
}

export function validateSubdomainClient(raw: string): string | null {
  const s = normalizeSubdomainInput(raw);
  if (!s) return "Vui lòng nhập tên miền phụ cho gian hàng.";
  if (s.length < 3 || s.length > 40) {
    return "Tên miền phụ cần dài từ 3 đến 40 ký tự.";
  }
  if (!SUBDOMAIN_RE.test(s)) {
    return "Chỉ dùng chữ thường (a-z), số (0-9) và dấu gạch nối (-), không mở đầu hoặc kết thúc bằng dấu gạch nối.";
  }
  if (RESERVED_SUBDOMAINS.has(s) || FORBIDDEN_BRAND_PREFIXES.some((p) => s.startsWith(p))) {
    return "Tên miền phụ này thuộc danh sách bảo vệ hệ thống hoặc nhãn hiệu. Vui lòng chọn tên khác.";
  }
  return null;
}

const HEX_COLOR_RE = /^#[0-9a-fA-F]{6}$/;

export function isValidHexColor(value: string): boolean {
  return HEX_COLOR_RE.test(value.trim());
}

export function normalizeHexColor(value: string): string {
  const trimmed = value.trim();
  return trimmed.startsWith("#") ? trimmed.toUpperCase() : `#${trimmed.toUpperCase()}`;
}

function srgbChannelToLinear(c: number): number {
  const s = c / 255;
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

export function relativeLuminance(hex: string): number | null {
  if (!isValidHexColor(hex)) return null;
  const clean = hex.trim().slice(1);
  const r = parseInt(clean.slice(0, 2), 16);
  const g = parseInt(clean.slice(2, 4), 16);
  const b = parseInt(clean.slice(4, 6), 16);
  return (
    0.2126 * srgbChannelToLinear(r) +
    0.7152 * srgbChannelToLinear(g) +
    0.0722 * srgbChannelToLinear(b)
  );
}

/** Real WCAG 2.1 contrast ratio between two #RRGGBB hex colors. Returns null if either is invalid. */
export function contrastRatio(hexA: string, hexB: string): number | null {
  const l1 = relativeLuminance(hexA);
  const l2 = relativeLuminance(hexB);
  if (l1 === null || l2 === null) return null;
  const lighter = Math.max(l1, l2);
  const darker = Math.min(l1, l2);
  return (lighter + 0.05) / (darker + 0.05);
}

export interface ContrastPairCheck {
  id: "bg-text" | "surface-text" | "accent-text";
  label: string;
  bg: string;
  fg: string;
  ratio: number | null;
  passes: boolean;
}

export function checkThemeContrast(theme: SiteTheme): ContrastPairCheck[] {
  const pairs: Array<{ id: ContrastPairCheck["id"]; label: string; bg: string; fg: string }> = [
    { id: "bg-text", label: "Nền trang / Chữ chính", bg: theme.bg_color, fg: theme.text_color },
    {
      id: "surface-text",
      label: "Bề mặt thẻ / Chữ chính",
      bg: theme.surface_color,
      fg: theme.text_color,
    },
    {
      id: "accent-text",
      label: "Màu nhấn / Chữ nút",
      bg: theme.accent_color,
      fg: theme.accent_text_color,
    },
  ];
  return pairs.map((p) => {
    const ratio = contrastRatio(p.bg, p.fg);
    return {
      ...p,
      ratio,
      passes: ratio !== null && ratio >= MIN_WCAG_CONTRAST,
    };
  });
}

export function formatContrastRatio(ratio: number | null): string {
  if (ratio === null) return "Chưa hợp lệ";
  return `${ratio.toFixed(2).replace(".", ",")}:1`;
}

export function formatBytesVi(bytes: number): string {
  return bytes.toLocaleString("vi-VN");
}

/** SafeURL / SafeHref check matching backend Go rules. Empty is valid for optional fields. */
export function validateSafeUrlClient(raw: string | undefined): string | null {
  const trimmed = (raw ?? "").trim();
  if (!trimmed) return null;
  if (trimmed.length > 500) return "Đường dẫn tối đa 500 ký tự.";
  if (/[\x00-\x1f\x7f\\]/.test(trimmed)) {
    return "Đường dẫn chứa ký tự không được phép.";
  }
  const lower = trimmed.toLowerCase();
  if (
    lower.startsWith("javascript:") ||
    lower.startsWith("data:") ||
    lower.startsWith("vbscript:") ||
    lower.startsWith("http:")
  ) {
    return "Chỉ chấp nhận đường dẫn https:// hoặc đường dẫn nội bộ bắt đầu bằng /.";
  }
  if (trimmed.startsWith("/")) {
    if (trimmed.startsWith("//")) {
      return "Đường dẫn nội bộ chỉ bắt đầu bằng một dấu /.";
    }
    if (/\s/.test(trimmed)) {
      return "Đường dẫn nội bộ không chứa khoảng trắng.";
    }
    return null;
  }
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:" || !parsed.hostname) {
      return "Chỉ chấp nhận đường dẫn https:// hợp lệ hoặc đường dẫn nội bộ bắt đầu bằng /.";
    }
    return null;
  } catch {
    return "Đường dẫn chưa đúng định dạng (ví dụ /banners/campaign-everyday.jpg hoặc https://...).";
  }
}

export function createDefaultSiteConfig(shopName: string, shopDescription = ""): SiteConfig {
  const brand = shopName.trim() || "Gian hàng NexusMart";
  const tagline =
    shopDescription.trim().slice(0, 150) ||
    "Sản phẩm cơ bản may trực tiếp tại xưởng Việt Nam, minh bạch chất liệu và bảng size.";
  const preset = THEME_PRESETS[0].colors;

  return {
    theme: {
      ...preset,
      font_family: "work-sans",
      logo_url: "/banners/vietnam-workshop.jpg",
      banner_url: "/banners/campaign-everyday.jpg",
      brand_name: brand.slice(0, 80),
      tagline,
    },
    sections: [
      {
        id: "hero-main",
        type: "hero",
        visible: true,
        props: {
          eyebrow: "Cửa hàng chính thức",
          title: brand.slice(0, 120),
          subtitle: tagline,
          image_url: "/banners/campaign-everyday.jpg",
          cta_label: "Xem tất cả sản phẩm",
          cta_href: "/san-pham",
        },
      },
      {
        id: "featured-main",
        type: "featured_products",
        visible: true,
        props: {
          title: "Sản phẩm nổi bật",
          subtitle: "Những mẫu bán chạy và sẵn đủ size trực tiếp từ kho xưởng.",
          product_ids: [],
          limit: 8,
        },
      },
      {
        id: "vouchers-main",
        type: "voucher_strip",
        visible: true,
        props: {
          title: "Ưu đãi riêng của gian hàng",
          subtitle: "Lưu mã giảm giá trực tiếp từ gian hàng trước khi đặt mua.",
          limit: 4,
        },
      },
      {
        id: "policies-main",
        type: "policies",
        visible: true,
        props: {
          title: "Cam kết phục vụ từ gian hàng",
          items: [
            {
              heading: "Đổi trả linh hoạt 45 ngày",
              detail: "Hỗ trợ đổi size nhanh khi sản phẩm còn nguyên tem mác và chưa qua sử dụng.",
            },
            {
              heading: "Kiểm tra hàng khi nhận",
              detail:
                "Cho phép đồng kiểm đúng mẫu, đúng cỡ và đúng chất liệu vải trước khi thanh toán.",
            },
            {
              heading: "Giao hàng toàn quốc",
              detail: "Đóng gói chuẩn xưởng, bàn giao vận chuyển trong ngày làm việc.",
            },
          ],
        },
      },
    ],
  };
}

export function createDefaultTemplateConfig(
  shopName: string,
  shopDescription = ""
): SiteTemplateConfig {
  const base = createDefaultSiteConfig(shopName, shopDescription);
  return {
    schema_version: SITE_SCHEMA_VERSION_V2,
    template_id: "minimal-fashion",
    theme: {
      ...base.theme,
      radius: "sm",
      density: "balanced",
    },
    layout: createDefaultLayoutConfig(),
    sections: base.sections.map((sec) => ({ ...sec })) as SiteSectionV2[],
    pages: createDefaultPagesConfig(),
  };
}

export function createSectionTemplate(type: SiteSectionType, existingCount: number): SiteSection {
  const id = `${type.replace(/_/g, "-")}-${Date.now().toString(36).slice(-4)}-${existingCount + 1}`;
  switch (type) {
    case "hero":
      return {
        id,
        type: "hero",
        visible: true,
        props: {
          eyebrow: "Bộ sưu tập mới",
          title: "Trang phục cơ bản chuẩn phom xưởng Việt",
          subtitle: "Chọn lọc chất liệu thoáng mát, bền màu sau nhiều lần giặt.",
          image_url: "/banners/campaign-everyday.jpg",
          cta_label: "Khám phá ngay",
          cta_href: "/san-pham",
        },
      };
    case "featured_products":
      return {
        id,
        type: "featured_products",
        visible: true,
        props: {
          title: "Sản phẩm tiêu biểu",
          subtitle: "Các thiết kế được người mua chọn nhiều nhất tuần này.",
          product_ids: [],
          limit: 8,
        },
      };
    case "collection":
      return {
        id,
        type: "collection",
        visible: true,
        props: {
          title: "Dòng sản phẩm chủ lực",
          subtitle: "Nhóm sản phẩm cùng chất liệu, dễ phối mặc mỗi ngày.",
          collection_slug: "co-ban-hang-ngay",
          category_slug: "",
          image_url: "/banners/fabric-closeup.jpg",
          limit: 4,
        },
      };
    case "text":
      return {
        id,
        type: "text",
        visible: true,
        props: {
          title: "Câu chuyện từ xưởng may",
          body: "Từng lô vải được kiểm định định lượng GSM và độ co rút trước khi lên rập, giúp người mặc luôn thoải mái và giữ phom ổn định qua thời gian.",
          align: "left",
        },
      };
    case "banner":
      return {
        id,
        type: "banner",
        visible: true,
        props: {
          title: "Ưu đãi mua theo bộ tiết kiệm hơn",
          subtitle:
            "Kết hợp áo thun, polo và quần short từ cùng một xưởng để tối ưu phí giao hàng.",
          image_url: "/banners/vietnam-workshop.jpg",
          cta_label: "Xem danh mục",
          cta_href: "/san-pham",
        },
      };
    case "voucher_strip":
      return {
        id,
        type: "voucher_strip",
        visible: true,
        props: {
          title: "Mã giảm giá của gian hàng",
          subtitle: "Áp dụng ngay khi thanh toán đơn hàng của gian hàng.",
          limit: 4,
        },
      };
    case "policies":
      return {
        id,
        type: "policies",
        visible: true,
        props: {
          title: "Chính sách gian hàng",
          items: [
            {
              heading: "Đổi size tận nơi",
              detail: "Hỗ trợ đổi cỡ nhanh chóng trong vòng 45 ngày kể từ khi nhận hàng.",
            },
            {
              heading: "Bảo hành đường may",
              detail: "Kiểm tra QC kỹ lưỡng từng sản phẩm trước khi đóng gói.",
            },
          ],
        },
      };
  }
}

export function createBlockTemplate(type: SiteBlockType, existingCount: number): SiteSectionBlock {
  const id = `blk-${type}-${Date.now().toString(36).slice(-4)}-${existingCount + 1}`;
  switch (type) {
    case "heading":
      return {
        id,
        type: "heading",
        props: { text: "Điểm nhấn chất liệu xưởng", level: "h3" },
      };
    case "paragraph":
      return {
        id,
        type: "paragraph",
        props: {
          text: "Chất liệu được dệt và kiểm định độ bền màu trực tiếp tại xưởng trước khi đưa vào sản xuất.",
        },
      };
    case "badge":
      return {
        id,
        type: "badge",
        props: { label: "Cotton Compact 220gsm", tone: "neutral" },
      };
    case "cta":
      return {
        id,
        type: "cta",
        props: { label: "Xem toàn bộ sản phẩm", href: "/san-pham", style: "primary" },
      };
    case "image":
      return {
        id,
        type: "image",
        props: {
          image_url: "/banners/fabric-closeup.jpg",
          alt: "Cận cảnh chất liệu vải",
          caption: "Bề mặt sợi mịn thoáng khí",
          aspect: "4:3",
        },
      };
    case "stat":
      return {
        id,
        type: "stat",
        props: { value: "220gsm", label: "Định lượng vải", note: "Đứng phom, không xù lông" },
      };
    case "policy":
      return {
        id,
        type: "policy",
        props: {
          heading: "Đổi size trong 45 ngày",
          detail: "Hỗ trợ đổi cỡ tận nơi khi sản phẩm còn nguyên tem mác.",
        },
      };
    case "quote":
      return {
        id,
        type: "quote",
        props: {
          body: "Mặc rất thoáng mát, giặt máy nhiều lần cổ áo vẫn giữ phom phẳng.",
          author: "Khách hàng đã mua",
          role: "Đơn hàng xác thực",
        },
      };
    case "feature":
      return {
        id,
        type: "feature",
        props: {
          title: "Xử lý chống co rút trước khi may",
          detail: "Giữ ổn định số đo vai và chiều dài áo qua nhiều chu kỳ giặt.",
          tag: "Chuẩn xưởng",
        },
      };
  }
}

export function createSectionV2Template(
  type: SiteSectionV2Type,
  existingCount: number
): SiteSectionV2 {
  const id = `${type.replace(/_/g, "-")}-${Date.now().toString(36).slice(-4)}-${existingCount + 1}`;
  switch (type) {
    case "hero":
    case "featured_products":
    case "collection":
    case "text":
    case "banner":
    case "voucher_strip":
    case "policies": {
      const base = createSectionTemplate(type, existingCount);
      return {
        ...base,
        variant: "",
        settings: { surface: "bg", padding: "normal" },
        blocks: [],
      } as SiteSectionV2;
    }
    case "lookbook":
      return {
        id,
        type: "lookbook",
        visible: true,
        variant: "asym-3",
        settings: { surface: "surface", padding: "normal" },
        props: {
          title: "Khung hình Lookbook mới",
          subtitle: "Trưng bày cận cảnh phom dáng và kết cấu sợi vải.",
          cta_label: "Xem danh mục",
          cta_href: "/san-pham",
        },
        blocks: [
          {
            id: `${id}-img-1`,
            type: "image",
            props: {
              image_url: "/banners/campaign-office.jpg",
              alt: "Khung hình bộ sưu tập",
              caption: "Phom mặc hằng ngày gọn gàng",
              aspect: "3:4",
            },
          },
          {
            id: `${id}-img-2`,
            type: "image",
            props: {
              image_url: "/banners/fabric-closeup.jpg",
              alt: "Cận cảnh bề mặt vải",
              caption: "Chi tiết bề mặt dệt thoáng mịn",
              aspect: "4:3",
            },
          },
        ],
      };
    case "metrics_strip":
      return {
        id,
        type: "metrics_strip",
        visible: true,
        variant: "bar-4",
        settings: { surface: "surface", padding: "normal", columns: 3 },
        props: {
          title: "Thông số nổi bật của gian hàng",
          subtitle: "Minh bạch tiêu chuẩn chất liệu và dịch vụ giao nhận.",
        },
        blocks: [
          {
            id: `${id}-s1`,
            type: "stat",
            props: { value: "220gsm", label: "Định lượng vải", note: "Dày dặn, thoáng mát" },
          },
          {
            id: `${id}-s2`,
            type: "stat",
            props: { value: "45 ngày", label: "Đổi size tận nơi", note: "Thủ tục đơn giản" },
          },
          {
            id: `${id}-s3`,
            type: "stat",
            props: { value: "100%", label: "Đồng kiểm COD", note: "Kiểm hàng trước thanh toán" },
          },
        ],
      };
    case "sku_matrix":
      return {
        id,
        type: "sku_matrix",
        visible: true,
        variant: "compact-table",
        settings: { surface: "surface", padding: "normal", max_items: 12 },
        props: {
          title: "Bảng tồn kho nhanh theo SKU",
          subtitle: "Tra cứu trực tiếp màu sắc, kích cỡ và số lượng sẵn kho.",
          category_slug: "",
          limit: 12,
          show_out_of_stock: true,
        },
        blocks: [],
      };
    case "testimonials":
      return {
        id,
        type: "testimonials",
        visible: true,
        variant: "grid-3",
        settings: { surface: "surface", padding: "normal", columns: 3 },
        props: {
          title: "Phản hồi từ người mặc",
          subtitle: "Đánh giá thực tế sau quá trình sử dụng và giặt giũ.",
        },
        blocks: [
          {
            id: `${id}-q1`,
            type: "quote",
            props: {
              body: "Chất vải mịn, thấm mồ hôi tốt và giao hàng rất đúng hẹn.",
              author: "Nguyễn Minh Quân",
              role: "Khách mua tại Hà Nội",
            },
          },
          {
            id: `${id}-q2`,
            type: "quote",
            props: {
              body: "Đổi size rất nhanh, đóng gói gọn gàng đúng như mô tả trên cửa hàng.",
              author: "Trần Hải Anh",
              role: "Khách mua tại TP.HCM",
            },
          },
        ],
      };
  }
}

export function getSiteStatusMeta(status: string | null | undefined): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
  adminTone: "positive" | "warning" | "negative" | "neutral";
} {
  switch ((status ?? "").toUpperCase()) {
    case "PUBLISHED":
      return { label: "Đang hoạt động", tone: "pos", adminTone: "positive" };
    case "DRAFT":
      return { label: "Bản nháp", tone: "warn", adminTone: "warning" };
    case "SUSPENDED":
      return { label: "Tạm khóa", tone: "neg", adminTone: "negative" };
    default:
      return { label: "Chưa thiết lập", tone: "neutral", adminTone: "neutral" };
  }
}

export function getSectionHeadingPreview(section: SiteSection | SiteSectionV2): string {
  const title = section.props.title?.trim();
  if (title) return title;
  return SECTION_V2_TYPE_META[section.type]?.label ?? section.type;
}

export function getPageSections(
  config: SiteTemplateConfig,
  page: TemplatePageKey
): SiteSectionV2[] {
  if (page === "home") return config.sections;
  return config.pages[page].sections ?? [];
}

export function setPageSections(
  config: SiteTemplateConfig,
  page: TemplatePageKey,
  sections: SiteSectionV2[]
): SiteTemplateConfig {
  if (page === "home") {
    return { ...config, sections };
  }
  return {
    ...config,
    pages: {
      ...config.pages,
      [page]: {
        ...config.pages[page],
        sections,
      },
    },
  };
}

export function countAllSections(config: SiteTemplateConfig): number {
  return (
    config.sections.length +
    (config.pages.catalog.sections?.length ?? 0) +
    (config.pages.pdp.sections?.length ?? 0) +
    (config.pages.cart.sections?.length ?? 0) +
    (config.pages.info.sections?.length ?? 0)
  );
}

/** Suggest a valid subdomain from a Vietnamese shop name (for first-time setup). */
export function suggestSubdomainFromShopName(shopName: string): string {
  const base = shopName
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 36);
  if (base.length >= 3 && !validateSubdomainClient(base)) return base;
  return "cua-hang-rieng";
}

function cleanBlockPayload(blk: SiteSectionBlock): SiteSectionBlock {
  const id = blk.id.trim();
  switch (blk.type) {
    case "heading":
      return {
        id,
        type: "heading",
        props: {
          text: blk.props.text.trim(),
          level: blk.props.level === "h2" || blk.props.level === "h3" ? blk.props.level : "h3",
        },
      };
    case "paragraph":
      return {
        id,
        type: "paragraph",
        props: { text: blk.props.text.trim() },
      };
    case "badge":
      return {
        id,
        type: "badge",
        props: {
          label: blk.props.label.trim(),
          tone:
            blk.props.tone === "accent" || blk.props.tone === "band" || blk.props.tone === "neutral"
              ? blk.props.tone
              : "neutral",
        },
      };
    case "cta":
      return {
        id,
        type: "cta",
        props: {
          label: blk.props.label.trim(),
          href: blk.props.href.trim(),
          style:
            blk.props.style === "secondary" ||
            blk.props.style === "link" ||
            blk.props.style === "primary"
              ? blk.props.style
              : "primary",
        },
      };
    case "image":
      return {
        id,
        type: "image",
        props: {
          image_url: blk.props.image_url.trim(),
          alt: (blk.props.alt ?? "").trim(),
          caption: (blk.props.caption ?? "").trim(),
          aspect:
            blk.props.aspect === "16:9" ||
            blk.props.aspect === "4:3" ||
            blk.props.aspect === "3:4" ||
            blk.props.aspect === "1:1"
              ? blk.props.aspect
              : "4:3",
        },
      };
    case "stat":
      return {
        id,
        type: "stat",
        props: {
          value: blk.props.value.trim(),
          label: blk.props.label.trim(),
          note: (blk.props.note ?? "").trim(),
        },
      };
    case "policy":
      return {
        id,
        type: "policy",
        props: {
          heading: blk.props.heading.trim(),
          detail: blk.props.detail.trim(),
        },
      };
    case "quote":
      return {
        id,
        type: "quote",
        props: {
          body: blk.props.body.trim(),
          author: blk.props.author.trim(),
          role: (blk.props.role ?? "").trim(),
        },
      };
    case "feature":
      return {
        id,
        type: "feature",
        props: {
          title: blk.props.title.trim(),
          detail: blk.props.detail.trim(),
          tag: (blk.props.tag ?? "").trim(),
        },
      };
  }
}

function cleanSectionSettings(settings: SectionSettings | undefined): SectionSettings | undefined {
  if (!settings) return undefined;
  const cleaned: SectionSettings = {};
  if (settings.surface) cleaned.surface = settings.surface;
  if (settings.padding) cleaned.padding = settings.padding;
  if (typeof settings.columns === "number" && settings.columns > 0) {
    cleaned.columns = Math.max(1, Math.min(5, Math.trunc(settings.columns)));
  }
  if (settings.align === "left" || settings.align === "center") {
    cleaned.align = settings.align;
  }
  if (typeof settings.max_items === "number" && settings.max_items > 0) {
    cleaned.max_items = Math.max(1, Math.min(24, Math.trunc(settings.max_items)));
  }
  return Object.keys(cleaned).length > 0 ? cleaned : undefined;
}

function cleanSectionV2Payload(sec: SiteSectionV2): SiteSectionV2 {
  const cleanedSettings = cleanSectionSettings(sec.settings);
  const cleanedBlocks =
    sec.blocks && sec.blocks.length > 0 ? sec.blocks.map(cleanBlockPayload) : undefined;
  const base = {
    id: sec.id.trim(),
    visible: Boolean(sec.visible),
    ...(sec.variant ? { variant: sec.variant.trim() } : {}),
    ...(cleanedSettings ? { settings: cleanedSettings } : {}),
    ...(cleanedBlocks ? { blocks: cleanedBlocks } : {}),
  };

  switch (sec.type) {
    case "hero":
      return {
        ...base,
        type: "hero",
        props: {
          eyebrow: (sec.props.eyebrow ?? "").trim(),
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          image_url: (sec.props.image_url ?? "").trim(),
          cta_label: (sec.props.cta_label ?? "").trim(),
          cta_href: (sec.props.cta_href ?? "").trim(),
        },
      };
    case "featured_products":
      return {
        ...base,
        type: "featured_products",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          product_ids: (sec.props.product_ids ?? []).map((x) => x.trim()).filter(Boolean),
          limit: Math.max(0, Math.min(12, Math.trunc(sec.props.limit ?? 8))),
        },
      };
    case "collection":
      return {
        ...base,
        type: "collection",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          collection_slug: (sec.props.collection_slug ?? "").trim(),
          category_slug: (sec.props.category_slug ?? "").trim(),
          image_url: (sec.props.image_url ?? "").trim(),
          limit: Math.max(0, Math.min(12, Math.trunc(sec.props.limit ?? 4))),
        },
      };
    case "text":
      return {
        ...base,
        type: "text",
        props: {
          title: sec.props.title.trim(),
          body: sec.props.body.trim(),
          align: sec.props.align === "center" ? "center" : sec.props.align === "left" ? "left" : "",
        },
      };
    case "banner":
      return {
        ...base,
        type: "banner",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          image_url: (sec.props.image_url ?? "").trim(),
          cta_label: (sec.props.cta_label ?? "").trim(),
          cta_href: (sec.props.cta_href ?? "").trim(),
        },
      };
    case "voucher_strip":
      return {
        ...base,
        type: "voucher_strip",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          limit: Math.max(0, Math.min(6, Math.trunc(sec.props.limit ?? 4))),
        },
      };
    case "policies":
      return {
        ...base,
        type: "policies",
        props: {
          title: sec.props.title.trim(),
          items: sec.props.items.map((it) => ({
            heading: it.heading.trim(),
            detail: it.detail.trim(),
          })),
        },
      };
    case "lookbook":
      return {
        ...base,
        type: "lookbook",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          cta_label: (sec.props.cta_label ?? "").trim(),
          cta_href: (sec.props.cta_href ?? "").trim(),
        },
      };
    case "metrics_strip":
      return {
        ...base,
        type: "metrics_strip",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
        },
      };
    case "sku_matrix":
      return {
        ...base,
        type: "sku_matrix",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
          category_slug: (sec.props.category_slug ?? "").trim(),
          limit: Math.max(0, Math.min(24, Math.trunc(sec.props.limit ?? 12))),
          show_out_of_stock: Boolean(sec.props.show_out_of_stock),
        },
      };
    case "testimonials":
      return {
        ...base,
        type: "testimonials",
        props: {
          title: sec.props.title.trim(),
          subtitle: (sec.props.subtitle ?? "").trim(),
        },
      };
  }
}

/** Clean v2 template payload before sending to strict DisallowUnknownFields backend validator. */
export function buildCleanTemplateConfigPayload(config: SiteTemplateConfig): SiteTemplateConfig {
  return {
    schema_version: SITE_SCHEMA_VERSION_V2,
    template_id: (config.template_id || "custom").trim().toLowerCase(),
    theme: {
      bg_color: normalizeHexColor(config.theme.bg_color),
      surface_color: normalizeHexColor(config.theme.surface_color),
      text_color: normalizeHexColor(config.theme.text_color),
      accent_color: normalizeHexColor(config.theme.accent_color),
      accent_text_color: normalizeHexColor(config.theme.accent_text_color),
      font_family: config.theme.font_family,
      logo_url: (config.theme.logo_url ?? "").trim(),
      banner_url: (config.theme.banner_url ?? "").trim(),
      brand_name: config.theme.brand_name.trim(),
      tagline: (config.theme.tagline ?? "").trim(),
      radius: config.theme.radius ?? "",
      density: config.theme.density ?? "",
    },
    layout: {
      archetype: config.layout.archetype,
      header_style: config.layout.header_style,
      footer_style: config.layout.footer_style,
      container_width: config.layout.container_width,
      card_style: config.layout.card_style,
      show_announcement: Boolean(config.layout.show_announcement),
      announcement_text: (config.layout.announcement_text ?? "").trim(),
      slots: {
        search: Boolean(config.layout.slots.search),
        chat: Boolean(config.layout.slots.chat),
        assistant: Boolean(config.layout.slots.assistant),
      },
    },
    sections: config.sections.map(cleanSectionV2Payload),
    pages: {
      catalog: {
        layout: config.pages.catalog.layout,
        columns: Math.max(1, Math.min(5, Math.trunc(config.pages.catalog.columns || 4))),
        show_filters: Boolean(config.pages.catalog.show_filters),
        show_search: Boolean(config.pages.catalog.show_search),
        show_sku_matrix: Boolean(config.pages.catalog.show_sku_matrix),
        intro_title: (config.pages.catalog.intro_title ?? "").trim(),
        intro_subtitle: (config.pages.catalog.intro_subtitle ?? "").trim(),
        sections: (config.pages.catalog.sections ?? []).map(cleanSectionV2Payload),
      },
      pdp: {
        layout: config.pages.pdp.layout,
        gallery_style: config.pages.pdp.gallery_style,
        show_vouchers: Boolean(config.pages.pdp.show_vouchers),
        show_sku_table: Boolean(config.pages.pdp.show_sku_table),
        show_story_band: Boolean(config.pages.pdp.show_story_band),
        show_related: Boolean(config.pages.pdp.show_related),
        sections: (config.pages.pdp.sections ?? []).map(cleanSectionV2Payload),
      },
      cart: {
        layout: config.pages.cart.layout,
        show_vouchers: Boolean(config.pages.cart.show_vouchers),
        show_addons: Boolean(config.pages.cart.show_addons),
        trust_note: (config.pages.cart.trust_note ?? "").trim(),
        sections: (config.pages.cart.sections ?? []).map(cleanSectionV2Payload),
      },
      info: {
        layout: config.pages.info.layout,
        show_factory_profile: Boolean(config.pages.info.show_factory_profile),
        show_toc: Boolean(config.pages.info.show_toc),
        show_showcase: Boolean(config.pages.info.show_showcase),
        sections: (config.pages.info.sections ?? []).map(cleanSectionV2Payload),
      },
    },
  };
}

/** Validate full v2 template draft on the client (subdomain + strict schema + XSS/SVG/SSRF + WCAG). */
export function validateTemplateDraftClient(
  subdomain: string,
  config: SiteTemplateConfig
): string | null {
  const subErr = validateSubdomainClient(subdomain);
  if (subErr) return subErr;
  const res = validateSiteTemplateJson(config);
  if (!res.ok) {
    const first = res.issues[0];
    return first ? `[${first.path}] ${first.message}` : "Cấu hình template v2 chưa hợp lệ.";
  }
  return null;
}

/** Clean v1 payload before sending to strict DisallowUnknownFields backend validator (legacy compat). */
export function buildCleanSiteConfigPayload(config: SiteConfig): SiteConfig {
  return {
    theme: {
      bg_color: normalizeHexColor(config.theme.bg_color),
      surface_color: normalizeHexColor(config.theme.surface_color),
      text_color: normalizeHexColor(config.theme.text_color),
      accent_color: normalizeHexColor(config.theme.accent_color),
      accent_text_color: normalizeHexColor(config.theme.accent_text_color),
      font_family: config.theme.font_family,
      logo_url: (config.theme.logo_url ?? "").trim(),
      banner_url: (config.theme.banner_url ?? "").trim(),
      brand_name: config.theme.brand_name.trim(),
      tagline: (config.theme.tagline ?? "").trim(),
    },
    sections: config.sections.map((sec): SiteSection => {
      switch (sec.type) {
        case "hero":
          return {
            id: sec.id.trim(),
            type: "hero",
            visible: sec.visible,
            props: {
              eyebrow: (sec.props.eyebrow ?? "").trim(),
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              image_url: (sec.props.image_url ?? "").trim(),
              cta_label: (sec.props.cta_label ?? "").trim(),
              cta_href: (sec.props.cta_href ?? "").trim(),
            },
          };
        case "featured_products":
          return {
            id: sec.id.trim(),
            type: "featured_products",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              product_ids: (sec.props.product_ids ?? []).map((x) => x.trim()).filter(Boolean),
              limit: Math.max(0, Math.min(12, Math.trunc(sec.props.limit ?? 8))),
            },
          };
        case "collection":
          return {
            id: sec.id.trim(),
            type: "collection",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              collection_slug: (sec.props.collection_slug ?? "").trim(),
              category_slug: (sec.props.category_slug ?? "").trim(),
              image_url: (sec.props.image_url ?? "").trim(),
              limit: Math.max(0, Math.min(12, Math.trunc(sec.props.limit ?? 4))),
            },
          };
        case "text":
          return {
            id: sec.id.trim(),
            type: "text",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              body: sec.props.body.trim(),
              align:
                sec.props.align === "center" ? "center" : sec.props.align === "left" ? "left" : "",
            },
          };
        case "banner":
          return {
            id: sec.id.trim(),
            type: "banner",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              image_url: (sec.props.image_url ?? "").trim(),
              cta_label: (sec.props.cta_label ?? "").trim(),
              cta_href: (sec.props.cta_href ?? "").trim(),
            },
          };
        case "voucher_strip":
          return {
            id: sec.id.trim(),
            type: "voucher_strip",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              limit: Math.max(0, Math.min(6, Math.trunc(sec.props.limit ?? 4))),
            },
          };
        case "policies":
          return {
            id: sec.id.trim(),
            type: "policies",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              items: sec.props.items.map((it) => ({
                heading: it.heading.trim(),
                detail: it.detail.trim(),
              })),
            },
          };
      }
    }),
  };
}

/** Validate the entire v1 draft payload on the client before submitting to BFF (legacy compat). */
export function validateSiteDraftClient(subdomain: string, config: SiteConfig): string | null {
  const subErr = validateSubdomainClient(subdomain);
  if (subErr) return subErr;

  const { theme, sections } = config;
  if (!theme.brand_name.trim() || theme.brand_name.trim().length > 80) {
    return "Tên thương hiệu cần từ 1 đến 80 ký tự.";
  }
  if ((theme.tagline ?? "").length > 160) {
    return "Khẩu hiệu thương hiệu tối đa 160 ký tự.";
  }
  const colorFields: Array<[string, string]> = [
    ["Màu nền trang", theme.bg_color],
    ["Màu bề mặt thẻ", theme.surface_color],
    ["Màu chữ chính", theme.text_color],
    ["Màu nhấn", theme.accent_color],
    ["Màu chữ trên nút", theme.accent_text_color],
  ];
  for (const [label, hex] of colorFields) {
    if (!isValidHexColor(hex)) {
      return `${label} cần là mã màu 6 ký tự dạng #RRGGBB.`;
    }
  }
  const failedContrast = checkThemeContrast(theme).find((c) => !c.passes);
  if (failedContrast) {
    return `Độ tương phản "${failedContrast.label}" đang là ${formatContrastRatio(failedContrast.ratio)}, cần tối thiểu 4,5:1.`;
  }
  const logoErr = validateSafeUrlClient(theme.logo_url);
  if (logoErr) return `Logo thương hiệu: ${logoErr}`;
  const bannerErr = validateSafeUrlClient(theme.banner_url);
  if (bannerErr) return `Ảnh bìa thương hiệu: ${bannerErr}`;

  if (sections.length > MAX_SECTIONS) {
    return `Site riêng hỗ trợ tối đa ${MAX_SECTIONS} khối nội dung.`;
  }

  for (let i = 0; i < sections.length; i++) {
    const sec = sections[i];
    const label = `Khối ${i + 1} (${SECTION_TYPE_META[sec.type].label})`;
    if (!sec.id.trim() || sec.id.length > 64) {
      return `${label}: mã khối không hợp lệ.`;
    }
    if (!sec.props.title.trim() || sec.props.title.trim().length > 120) {
      return `${label}: tiêu đề cần từ 1 đến 120 ký tự.`;
    }
    switch (sec.type) {
      case "hero": {
        if ((sec.props.eyebrow ?? "").length > 60) return `${label}: nhãn phụ tối đa 60 ký tự.`;
        if ((sec.props.subtitle ?? "").length > 400) return `${label}: mô tả tối đa 400 ký tự.`;
        if ((sec.props.cta_label ?? "").length > 40) return `${label}: nhãn nút tối đa 40 ký tự.`;
        const imgErr = validateSafeUrlClient(sec.props.image_url);
        if (imgErr) return `${label} (ảnh): ${imgErr}`;
        const hrefErr = validateSafeUrlClient(sec.props.cta_href);
        if (hrefErr) return `${label} (liên kết nút): ${hrefErr}`;
        break;
      }
      case "featured_products": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        if ((sec.props.product_ids ?? []).length > 12) return `${label}: chọn tối đa 12 sản phẩm.`;
        const limit = sec.props.limit ?? 8;
        if (limit < 0 || limit > 12) {
          return `${label}: số sản phẩm hiển thị từ 0 đến 12.`;
        }
        break;
      }
      case "collection": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        if ((sec.props.collection_slug ?? "").length > 80)
          return `${label}: mã bộ sưu tập tối đa 80 ký tự.`;
        if ((sec.props.category_slug ?? "").length > 80)
          return `${label}: mã danh mục tối đa 80 ký tự.`;
        const imgErr = validateSafeUrlClient(sec.props.image_url);
        if (imgErr) return `${label} (ảnh): ${imgErr}`;
        const limit = sec.props.limit ?? 4;
        if (limit < 0 || limit > 12) {
          return `${label}: số sản phẩm hiển thị từ 0 đến 12.`;
        }
        break;
      }
      case "text": {
        if (!sec.props.body.trim() || sec.props.body.trim().length > 2000) {
          return `${label}: nội dung cần từ 1 đến 2000 ký tự.`;
        }
        break;
      }
      case "banner": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        if ((sec.props.cta_label ?? "").length > 40) return `${label}: nhãn nút tối đa 40 ký tự.`;
        const imgErr = validateSafeUrlClient(sec.props.image_url);
        if (imgErr) return `${label} (ảnh): ${imgErr}`;
        const hrefErr = validateSafeUrlClient(sec.props.cta_href);
        if (hrefErr) return `${label} (liên kết nút): ${hrefErr}`;
        break;
      }
      case "voucher_strip": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        const limit = sec.props.limit ?? 4;
        if (limit < 0 || limit > 6) {
          return `${label}: số mã hiển thị từ 0 đến 6.`;
        }
        break;
      }
      case "policies": {
        if (sec.props.items.length < 1 || sec.props.items.length > 8) {
          return `${label}: cần từ 1 đến 8 mục chính sách.`;
        }
        for (let j = 0; j < sec.props.items.length; j++) {
          const item = sec.props.items[j];
          if (!item.heading.trim() || item.heading.trim().length > 80) {
            return `${label}, mục ${j + 1}: tiêu đề cần từ 1 đến 80 ký tự.`;
          }
          if (!item.detail.trim() || item.detail.trim().length > 400) {
            return `${label}, mục ${j + 1}: mô tả cần từ 1 đến 400 ký tự.`;
          }
        }
        break;
      }
    }
  }
  return null;
}

/** Strips the parent `js` class token so iframe previews never activate scroll-reveal hiding without an observer. */
export function stripPreviewJsClass(className: string): string {
  return className
    .split(/\s+/)
    .filter((token) => token && token !== "js")
    .join(" ");
}
