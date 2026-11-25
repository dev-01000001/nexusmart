// Shared Declarative Theme JSON v2 types & constants for NexusMart storefront templates.
// Matches ADR-0001 (docs/adr/0001-site-template-format.md) and Go validator in services/user/internal/site/.
// Extends v1 SiteConfig cleanly so existing SEO and theme helpers stay compatible without touching web/src/lib/.

import type {
  BannerSectionProps,
  CollectionSectionProps,
  FeaturedProductsSectionProps,
  HeroSectionProps,
  PoliciesSectionProps,
  PolicyItem,
  SiteConfig,
  SiteFontFamily,
  SiteTheme,
  TextSectionProps,
  VoucherStripSectionProps,
} from "@/lib/site/types";

export const SITE_SCHEMA_VERSION_V2 = 2 as const;
export const MAX_CONFIG_BYTES = 128 * 1024;
export const MAX_JSON_DEPTH = 8;
export const MAX_HOME_SECTIONS = 20;
export const MAX_PAGE_SECTIONS = 8;
export const MAX_TOTAL_SECTIONS = 32;
export const MAX_BLOCKS_PER_SECTION = 12;
export const MAX_URL_LENGTH = 500;
export const MIN_WCAG_CONTRAST = 4.5;

export const ALLOWED_RADII = ["", "none", "sm", "md", "lg"] as const;
export type SiteRadius = (typeof ALLOWED_RADII)[number];

export const ALLOWED_DENSITIES = ["", "compact", "balanced", "airy"] as const;
export type SiteDensity = (typeof ALLOWED_DENSITIES)[number];

export const ALLOWED_ARCHETYPES = [
  "minimal-fashion",
  "editorial",
  "dense-grid",
  "craft-atelier",
  "sport-active",
  "luxury-maison",
  "one-page-launch",
  "multi-sku-catalog",
  "classic",
] as const;
export type SiteArchetype = (typeof ALLOWED_ARCHETYPES)[number];

export const ALLOWED_HEADER_STYLES = [
  "minimal-split",
  "centered-masthead",
  "utility-compact",
  "atelier-framed",
  "bold-dark",
  "maison-centered",
  "anchor-minimal",
  "catalog-rail",
] as const;
export type SiteHeaderStyle = (typeof ALLOWED_HEADER_STYLES)[number];

export const ALLOWED_FOOTER_STYLES = [
  "minimal-row",
  "editorial-cols",
  "compact-bar",
  "atelier-footer",
  "bold-dark",
  "maison-minimal",
] as const;
export type SiteFooterStyle = (typeof ALLOWED_FOOTER_STYLES)[number];

export const ALLOWED_CONTAINER_WIDTHS = ["narrow", "standard", "wide", "full"] as const;
export type SiteContainerWidth = (typeof ALLOWED_CONTAINER_WIDTHS)[number];

export const ALLOWED_CARD_STYLES = [
  "minimal",
  "editorial",
  "dense",
  "atelier",
  "sport",
  "luxury",
  "compact-row",
] as const;
export type SiteCardStyle = (typeof ALLOWED_CARD_STYLES)[number];

export const ALLOWED_CATALOG_LAYOUTS = [
  "minimal-gallery",
  "editorial-magazine",
  "top-bar",
  "sidebar-left",
  "luxury-curated",
  "single-strip",
  "sku-table",
] as const;
export type SiteCatalogLayout = (typeof ALLOWED_CATALOG_LAYOUTS)[number];

export const ALLOWED_PDP_LAYOUTS = [
  "classic-split",
  "editorial-tall",
  "dense-spec",
  "atelier-story",
  "sport-telemetry",
  "luxury-framed",
  "one-page-drawer",
  "sku-order-matrix",
] as const;
export type SitePdpLayout = (typeof ALLOWED_PDP_LAYOUTS)[number];

export const ALLOWED_PDP_GALLERY_STYLES = ["thumbnails", "stacked", "framed", "compact"] as const;
export type SitePdpGalleryStyle = (typeof ALLOWED_PDP_GALLERY_STYLES)[number];

export const ALLOWED_CART_LAYOUTS = [
  "split-summary",
  "compact-table",
  "editorial-ledger",
  "atelier-bag",
  "express-bar",
  "luxury-folio",
] as const;
export type SiteCartLayout = (typeof ALLOWED_CART_LAYOUTS)[number];

export const ALLOWED_INFO_LAYOUTS = [
  "split-hero",
  "editorial-essay",
  "craft-timeline",
  "athletic-manifesto",
  "luxury-maison",
  "compact-faq",
  "b2b-catalog-sheet",
] as const;
export type SiteInfoLayout = (typeof ALLOWED_INFO_LAYOUTS)[number];

export const ALLOWED_SECTION_SURFACES = ["", "bg", "surface", "band", "accent-soft"] as const;
export type SectionSurface = (typeof ALLOWED_SECTION_SURFACES)[number];

export const ALLOWED_SECTION_PADDINGS = ["", "compact", "normal", "generous"] as const;
export type SectionPadding = (typeof ALLOWED_SECTION_PADDINGS)[number];

export const ALLOWED_V2_SECTION_TYPES = [
  "hero",
  "featured_products",
  "collection",
  "text",
  "banner",
  "voucher_strip",
  "policies",
  "lookbook",
  "metrics_strip",
  "sku_matrix",
  "testimonials",
] as const;
export type SiteSectionV2Type = (typeof ALLOWED_V2_SECTION_TYPES)[number];

export const SECTION_VARIANTS_BY_TYPE: Record<SiteSectionV2Type, readonly string[]> = {
  hero: [
    "",
    "split",
    "fullbleed",
    "editorial",
    "centered",
    "compact-bar",
    "atelier",
    "sport-diagonal",
    "luxury-framed",
  ],
  featured_products: ["", "grid-4", "grid-3", "grid-2", "dense-5", "editorial-asym", "list-sku"],
  collection: ["", "split", "editorial", "compact", "cards"],
  text: ["", "split", "centered", "pullquote", "monograph"],
  banner: ["", "band", "split-image", "framed", "compact"],
  voucher_strip: ["", "grid", "compact-bar", "cards"],
  policies: ["", "grid", "timeline", "compact-list", "cards"],
  lookbook: ["", "asym-3", "duo-split", "strip-4"],
  metrics_strip: ["", "bar-4", "cards-3", "telemetry"],
  sku_matrix: ["", "compact-table", "grouped"],
  testimonials: ["", "grid-3", "editorial-quotes"],
};

export const ALLOWED_BLOCK_TYPES = [
  "heading",
  "paragraph",
  "badge",
  "cta",
  "image",
  "stat",
  "policy",
  "quote",
  "feature",
] as const;
export type SiteBlockType = (typeof ALLOWED_BLOCK_TYPES)[number];

export interface HeadingBlockProps {
  text: string;
  level?: "" | "h2" | "h3";
}

export interface ParagraphBlockProps {
  text: string;
}

export interface BadgeBlockProps {
  label: string;
  tone?: "" | "neutral" | "accent" | "band";
}

export interface CtaBlockProps {
  label: string;
  href: string;
  style?: "" | "primary" | "secondary" | "link";
}

export interface ImageBlockProps {
  image_url: string;
  alt?: string;
  caption?: string;
  aspect?: "" | "16:9" | "4:3" | "3:4" | "1:1";
}

export interface StatBlockProps {
  value: string;
  label: string;
  note?: string;
}

export interface PolicyBlockProps {
  heading: string;
  detail: string;
}

export interface QuoteBlockProps {
  body: string;
  author: string;
  role?: string;
}

export interface FeatureBlockProps {
  title: string;
  detail: string;
  tag?: string;
}

export type SiteSectionBlock =
  | { id: string; type: "heading"; props: HeadingBlockProps }
  | { id: string; type: "paragraph"; props: ParagraphBlockProps }
  | { id: string; type: "badge"; props: BadgeBlockProps }
  | { id: string; type: "cta"; props: CtaBlockProps }
  | { id: string; type: "image"; props: ImageBlockProps }
  | { id: string; type: "stat"; props: StatBlockProps }
  | { id: string; type: "policy"; props: PolicyBlockProps }
  | { id: string; type: "quote"; props: QuoteBlockProps }
  | { id: string; type: "feature"; props: FeatureBlockProps };

export interface SectionSettings {
  surface?: SectionSurface;
  padding?: SectionPadding;
  columns?: number;
  align?: "" | "left" | "center";
  max_items?: number;
}

export interface LookbookSectionProps {
  title: string;
  subtitle?: string;
  cta_label?: string;
  cta_href?: string;
}

export interface MetricsStripSectionProps {
  title: string;
  subtitle?: string;
}

export interface SkuMatrixSectionProps {
  title: string;
  subtitle?: string;
  category_slug?: string;
  limit?: number;
  show_out_of_stock?: boolean;
}

export interface TestimonialsSectionProps {
  title: string;
  subtitle?: string;
}

export interface BaseSectionV2 {
  id: string;
  visible: boolean;
  variant?: string;
  settings?: SectionSettings;
  blocks?: SiteSectionBlock[];
}

export type SiteSectionV2 =
  | (BaseSectionV2 & { type: "hero"; props: HeroSectionProps })
  | (BaseSectionV2 & { type: "featured_products"; props: FeaturedProductsSectionProps })
  | (BaseSectionV2 & { type: "collection"; props: CollectionSectionProps })
  | (BaseSectionV2 & { type: "text"; props: TextSectionProps })
  | (BaseSectionV2 & { type: "banner"; props: BannerSectionProps })
  | (BaseSectionV2 & { type: "voucher_strip"; props: VoucherStripSectionProps })
  | (BaseSectionV2 & { type: "policies"; props: PoliciesSectionProps })
  | (BaseSectionV2 & { type: "lookbook"; props: LookbookSectionProps })
  | (BaseSectionV2 & { type: "metrics_strip"; props: MetricsStripSectionProps })
  | (BaseSectionV2 & { type: "sku_matrix"; props: SkuMatrixSectionProps })
  | (BaseSectionV2 & { type: "testimonials"; props: TestimonialsSectionProps });

export interface SiteThemeV2 extends SiteTheme {
  radius?: SiteRadius;
  density?: SiteDensity;
}

export interface SiteLayoutSlots {
  search: boolean;
  chat: boolean;
  assistant: boolean;
}

export interface SiteLayoutConfig {
  archetype: SiteArchetype;
  header_style: SiteHeaderStyle;
  footer_style: SiteFooterStyle;
  container_width: SiteContainerWidth;
  card_style: SiteCardStyle;
  show_announcement: boolean;
  announcement_text: string;
  slots: SiteLayoutSlots;
}

export interface CatalogPageConfig {
  layout: SiteCatalogLayout;
  columns: number;
  show_filters: boolean;
  show_search: boolean;
  show_sku_matrix: boolean;
  intro_title: string;
  intro_subtitle: string;
  sections?: SiteSectionV2[];
}

export interface PdpPageConfig {
  layout: SitePdpLayout;
  gallery_style: SitePdpGalleryStyle;
  show_vouchers: boolean;
  show_sku_table: boolean;
  show_story_band: boolean;
  show_related: boolean;
  sections?: SiteSectionV2[];
}

export interface CartPageConfig {
  layout: SiteCartLayout;
  show_vouchers: boolean;
  show_addons: boolean;
  trust_note: string;
  sections?: SiteSectionV2[];
}

export interface InfoPageConfig {
  layout: SiteInfoLayout;
  show_factory_profile: boolean;
  show_toc: boolean;
  show_showcase: boolean;
  sections?: SiteSectionV2[];
}

export interface SitePagesConfig {
  catalog: CatalogPageConfig;
  pdp: PdpPageConfig;
  cart: CartPageConfig;
  info: InfoPageConfig;
}

export interface SiteTemplateConfig {
  schema_version: 2;
  template_id: string;
  theme: SiteThemeV2;
  layout: SiteLayoutConfig;
  sections: SiteSectionV2[];
  pages: SitePagesConfig;
}

export interface PlatformTemplateDTO {
  id: string;
  slug: string;
  name: string;
  category: string;
  archetype: SiteArchetype;
  description: string;
  preview_image_url: string;
  tags: string[];
  config: SiteTemplateConfig;
}

export type TemplatePageKey = "home" | "catalog" | "pdp" | "cart" | "info";

export interface TemplateValidationIssue {
  path: string;
  line: number;
  column: number;
  code: string;
  message: string;
}

export interface TemplateValidationResult {
  ok: boolean;
  config: SiteTemplateConfig | null;
  issues: TemplateValidationIssue[];
}

export type {
  BannerSectionProps,
  CollectionSectionProps,
  FeaturedProductsSectionProps,
  HeroSectionProps,
  PoliciesSectionProps,
  PolicyItem,
  SiteConfig,
  SiteFontFamily,
  SiteTheme,
  TextSectionProps,
  VoucherStripSectionProps,
};
