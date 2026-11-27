// Shared multi-tenant SiteConfig schema & DTO types.
// Mirrors the strict Go schema in services/user (Phase 2.A) and is shared between
// the public subdomain site renderer (@web-site) and Seller/Admin editor (@web-editor).

export const ALLOWED_SITE_FONTS = [
  "work-sans",
  "be-vietnam-pro",
  "inter",
  "ibm-plex-sans",
  "plus-jakarta-sans",
] as const;

export type SiteFontFamily = (typeof ALLOWED_SITE_FONTS)[number];

export const SITE_FONT_LABELS: Record<SiteFontFamily, string> = {
  "work-sans": "Work Sans",
  "be-vietnam-pro": "Be Vietnam Pro",
  inter: "Inter",
  "ibm-plex-sans": "IBM Plex Sans",
  "plus-jakarta-sans": "Plus Jakarta Sans",
};

export const ALLOWED_SITE_SECTION_TYPES = [
  "hero",
  "featured_products",
  "collection",
  "text",
  "banner",
  "voucher_strip",
  "policies",
] as const;

export type SiteSectionType = (typeof ALLOWED_SITE_SECTION_TYPES)[number];

export const MAX_SITE_SECTIONS = 20;

export type SiteStatus = "DRAFT" | "PUBLISHED" | "SUSPENDED";

export interface SiteTheme {
  bg_color: string;
  surface_color: string;
  text_color: string;
  accent_color: string;
  accent_text_color: string;
  font_family: SiteFontFamily;
  logo_url?: string;
  banner_url?: string;
  brand_name: string;
  tagline?: string;
}

export interface HeroSectionProps {
  eyebrow?: string;
  title: string;
  subtitle?: string;
  image_url?: string;
  cta_label?: string;
  cta_href?: string;
}

export interface FeaturedProductsSectionProps {
  title: string;
  subtitle?: string;
  product_ids?: string[];
  limit?: number;
}

export interface CollectionSectionProps {
  title: string;
  subtitle?: string;
  collection_slug?: string;
  category_slug?: string;
  image_url?: string;
  limit?: number;
}

export interface TextSectionProps {
  title: string;
  body: string;
  align?: "" | "left" | "center";
}

export interface BannerSectionProps {
  title: string;
  subtitle?: string;
  image_url?: string;
  cta_label?: string;
  cta_href?: string;
}

export interface VoucherStripSectionProps {
  title: string;
  subtitle?: string;
  limit?: number;
}

export interface PolicyItem {
  heading: string;
  detail: string;
}

export interface PoliciesSectionProps {
  title: string;
  items: PolicyItem[];
}

export interface HeroSection {
  id: string;
  type: "hero";
  visible: boolean;
  props: HeroSectionProps;
}

export interface FeaturedProductsSection {
  id: string;
  type: "featured_products";
  visible: boolean;
  props: FeaturedProductsSectionProps;
}

export interface CollectionSection {
  id: string;
  type: "collection";
  visible: boolean;
  props: CollectionSectionProps;
}

export interface TextSection {
  id: string;
  type: "text";
  visible: boolean;
  props: TextSectionProps;
}

export interface BannerSection {
  id: string;
  type: "banner";
  visible: boolean;
  props: BannerSectionProps;
}

export interface VoucherStripSection {
  id: string;
  type: "voucher_strip";
  visible: boolean;
  props: VoucherStripSectionProps;
}

export interface PoliciesSection {
  id: string;
  type: "policies";
  visible: boolean;
  props: PoliciesSectionProps;
}

export type SiteSection =
  | HeroSection
  | FeaturedProductsSection
  | CollectionSection
  | TextSection
  | BannerSection
  | VoucherStripSection
  | PoliciesSection;

export interface SiteConfig {
  theme: SiteTheme;
  sections: SiteSection[];
}

export interface SitePublicShopDTO {
  id: string;
  name: string;
  description?: string;
  status?: string;
}

export interface SiteVersionDTO {
  id: string;
  site_id: string;
  version_number: number;
  config?: SiteConfig;
  config_json?: string;
  published_by: string;
  note: string;
  created_at: string;
}

export interface SiteDTO {
  id: string;
  shop_id: string;
  shop_name?: string;
  subdomain: string;
  primary_host: string;
  status: SiteStatus;
  config?: SiteConfig;
  draft_config?: SiteConfig;
  published_config?: SiteConfig;
  config_json?: string;
  draft_config_json?: string;
  published_version_id?: string | null;
  published_version_number?: number;
  published_at?: string | null;
  suspended_reason?: string;
  created_at?: string;
  updated_at?: string;
}

export interface PublicSitePayload {
  site: SiteDTO;
  shop: SitePublicShopDTO;
  config: SiteConfig;
}

export interface SiteThemePreset {
  id: string;
  name: string;
  description: string;
  colors: Pick<
    SiteTheme,
    "bg_color" | "surface_color" | "text_color" | "accent_color" | "accent_text_color"
  >;
}

/** Curated WCAG >= 4.5:1 palettes (shared by Seller Site Editor and default fallbacks). */
export const SITE_THEME_PRESETS: readonly SiteThemePreset[] = [
  {
    id: "frost-slate",
    name: "Xương đá & Lam biển",
    description: "Nền sáng trong trẻo, điểm nhấn xanh ngọc đậm rõ nét.",
    colors: {
      bg_color: "#F7F9FA",
      surface_color: "#FFFFFF",
      text_color: "#1F3038",
      accent_color: "#0D7480",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "editorial-stone",
    name: "Giấy mộc & Xanh chàm",
    description: "Nền đá sáng ấm nhẹ kết hợp xanh lam kiến trúc.",
    colors: {
      bg_color: "#F8F6F1",
      surface_color: "#FFFFFF",
      text_color: "#182026",
      accent_color: "#1D4ED8",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "studio-charcoal",
    name: "Than chì & Hổ phách",
    description: "Nền tối thể thao hiện đại, độ tương phản cao.",
    colors: {
      bg_color: "#14181C",
      surface_color: "#1E242B",
      text_color: "#F4F6F8",
      accent_color: "#D97706",
      accent_text_color: "#111418",
    },
  },
  {
    id: "forest-linen",
    name: "Mộc & Xanh rêu",
    description: "Sắc xanh tự nhiên cho cửa hàng thủ công và vật liệu hữu cơ.",
    colors: {
      bg_color: "#F4F6F4",
      surface_color: "#FFFFFF",
      text_color: "#1A2B22",
      accent_color: "#1B6E4B",
      accent_text_color: "#FFFFFF",
    },
  },
];

export const DEFAULT_SITE_THEME: SiteTheme = {
  bg_color: "#F7F9FA",
  surface_color: "#FFFFFF",
  text_color: "#1F3038",
  accent_color: "#0D7480",
  accent_text_color: "#FFFFFF",
  font_family: "work-sans",
  logo_url: "",
  banner_url: "",
  brand_name: "Cửa hàng trên NexusMart",
  tagline: "",
};

export function createDefaultSiteConfig(brandName = "Cửa hàng của bạn"): SiteConfig {
  return {
    theme: {
      ...DEFAULT_SITE_THEME,
      brand_name: brandName,
      tagline: "Sản phẩm chính hãng, minh bạch thông tin và tồn kho",
    },
    sections: [
      {
        id: "hero-main",
        type: "hero",
        visible: true,
        props: {
          title: brandName,
          subtitle: "Khám phá các sản phẩm tuyển chọn và tồn kho cập nhật trực tiếp tại cửa hàng.",
          image_url: "/banners/vietnam-workshop.jpg",
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
          subtitle: "Các mẫu bán chạy và sẵn giao ngay tại kho.",
          limit: 8,
        },
      },
      {
        id: "policies-main",
        type: "policies",
        visible: true,
        props: {
          title: "Cam kết của cửa hàng",
          items: [
            {
              heading: "Đổi trả minh bạch",
              detail: "Hỗ trợ đổi trả nhanh chóng nếu sản phẩm lỗi hoặc chưa qua sử dụng.",
            },
            {
              heading: "Giao hàng đồng kiểm",
              detail: "Kiểm tra đúng sản phẩm, phân loại và số lượng trước khi nhận hàng.",
            },
          ],
        },
      },
    ],
  };
}

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function asOptionalString(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function asOptionalInt(value: unknown): number | undefined {
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  return Math.trunc(value);
}

function normalizeFontFamily(raw: unknown): SiteFontFamily {
  if (typeof raw === "string") {
    const clean = raw.trim().toLowerCase() as SiteFontFamily;
    if ((ALLOWED_SITE_FONTS as readonly string[]).includes(clean)) {
      return clean;
    }
  }
  return "work-sans";
}

function normalizeSection(raw: unknown, index: number): SiteSection | null {
  if (!isObjectRecord(raw)) return null;
  const type = asString(raw.type) as SiteSectionType;
  if (!(ALLOWED_SITE_SECTION_TYPES as readonly string[]).includes(type)) return null;

  const id = asString(raw.id).trim() || `section-${index + 1}`;
  const visible = typeof raw.visible === "boolean" ? raw.visible : true;
  const props = isObjectRecord(raw.props) ? raw.props : {};

  switch (type) {
    case "hero":
      return {
        id,
        type,
        visible,
        props: {
          eyebrow: asOptionalString(props.eyebrow),
          title: asString(props.title),
          subtitle: asOptionalString(props.subtitle),
          image_url: asOptionalString(props.image_url),
          cta_label: asOptionalString(props.cta_label),
          cta_href: asOptionalString(props.cta_href),
        },
      };
    case "featured_products":
      return {
        id,
        type,
        visible,
        props: {
          title: asString(props.title),
          subtitle: asOptionalString(props.subtitle),
          product_ids: Array.isArray(props.product_ids)
            ? props.product_ids.filter((x): x is string => typeof x === "string" && x.length > 0)
            : undefined,
          limit: asOptionalInt(props.limit),
        },
      };
    case "collection":
      return {
        id,
        type,
        visible,
        props: {
          title: asString(props.title),
          subtitle: asOptionalString(props.subtitle),
          collection_slug: asOptionalString(props.collection_slug),
          category_slug: asOptionalString(props.category_slug),
          image_url: asOptionalString(props.image_url),
          limit: asOptionalInt(props.limit),
        },
      };
    case "text": {
      const rawAlign = asString(props.align);
      const align = rawAlign === "left" || rawAlign === "center" ? rawAlign : "";
      return {
        id,
        type,
        visible,
        props: {
          title: asString(props.title),
          body: asString(props.body),
          align,
        },
      };
    }
    case "banner":
      return {
        id,
        type,
        visible,
        props: {
          title: asString(props.title),
          subtitle: asOptionalString(props.subtitle),
          image_url: asOptionalString(props.image_url),
          cta_label: asOptionalString(props.cta_label),
          cta_href: asOptionalString(props.cta_href),
        },
      };
    case "voucher_strip":
      return {
        id,
        type,
        visible,
        props: {
          title: asString(props.title),
          subtitle: asOptionalString(props.subtitle),
          limit: asOptionalInt(props.limit),
        },
      };
    case "policies": {
      const items: PolicyItem[] = Array.isArray(props.items)
        ? props.items
            .filter(isObjectRecord)
            .map((it) => ({
              heading: asString(it.heading),
              detail: asString(it.detail),
            }))
            .filter((it) => it.heading.length > 0 || it.detail.length > 0)
        : [];
      return {
        id,
        type,
        visible,
        props: {
          title: asString(props.title),
          items,
        },
      };
    }
  }
}

/**
 * Safely parse and normalize a `SiteConfig` from either a JSON string (`config_json`)
 * or an already-parsed object (`config`). Returns null if structurally invalid.
 */
export function parseSiteConfig(raw: unknown, fallbackBrandName = ""): SiteConfig | null {
  let obj: unknown = raw;
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) return null;
    try {
      obj = JSON.parse(trimmed);
    } catch {
      return null;
    }
  }
  if (!isObjectRecord(obj)) return null;

  const rawTheme = isObjectRecord(obj.theme) ? obj.theme : {};
  const theme: SiteTheme = {
    bg_color: asString(rawTheme.bg_color, DEFAULT_SITE_THEME.bg_color),
    surface_color: asString(rawTheme.surface_color, DEFAULT_SITE_THEME.surface_color),
    text_color: asString(rawTheme.text_color, DEFAULT_SITE_THEME.text_color),
    accent_color: asString(rawTheme.accent_color, DEFAULT_SITE_THEME.accent_color),
    accent_text_color: asString(rawTheme.accent_text_color, DEFAULT_SITE_THEME.accent_text_color),
    font_family: normalizeFontFamily(rawTheme.font_family),
    logo_url: asOptionalString(rawTheme.logo_url) ?? "",
    banner_url: asOptionalString(rawTheme.banner_url) ?? "",
    brand_name:
      asString(rawTheme.brand_name).trim() || fallbackBrandName || DEFAULT_SITE_THEME.brand_name,
    tagline: asString(rawTheme.tagline),
  };

  const sections: SiteSection[] = Array.isArray(obj.sections)
    ? obj.sections
        .slice(0, MAX_SITE_SECTIONS)
        .map((sec, idx) => normalizeSection(sec, idx))
        .filter((sec): sec is SiteSection => sec !== null)
    : [];

  return { theme, sections };
}
