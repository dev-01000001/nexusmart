// Editor & Admin Site DTO definitions aligned with shared @/lib/site/types and v2 templateTypes.
export type {
  BannerSection,
  BannerSectionProps,
  CollectionSection,
  CollectionSectionProps,
  FeaturedProductsSection,
  FeaturedProductsSectionProps,
  HeroSection,
  HeroSectionProps,
  PoliciesSection,
  PoliciesSectionProps,
  PolicyItem,
  SiteConfig,
  SiteFontFamily,
  SiteSection,
  SiteSectionType,
  SiteStatus,
  SiteTheme,
  TextSection,
  TextSectionProps,
  VoucherStripSection,
  VoucherStripSectionProps,
} from "@/lib/site/types";

export type {
  BadgeBlockProps,
  BaseSectionV2,
  CartPageConfig,
  CatalogPageConfig,
  CtaBlockProps,
  FeatureBlockProps,
  HeadingBlockProps,
  ImageBlockProps,
  InfoPageConfig,
  LookbookSectionProps,
  MetricsStripSectionProps,
  ParagraphBlockProps,
  PdpPageConfig,
  PlatformTemplateDTO,
  PolicyBlockProps,
  QuoteBlockProps,
  SectionPadding,
  SectionSettings,
  SectionSurface,
  SiteArchetype,
  SiteBlockType,
  SiteCardStyle,
  SiteCartLayout,
  SiteCatalogLayout,
  SiteContainerWidth,
  SiteDensity,
  SiteFooterStyle,
  SiteHeaderStyle,
  SiteInfoLayout,
  SiteLayoutConfig,
  SiteLayoutSlots,
  SitePagesConfig,
  SitePdpGalleryStyle,
  SitePdpLayout,
  SiteRadius,
  SiteSectionBlock,
  SiteSectionV2,
  SiteSectionV2Type,
  SiteTemplateConfig,
  SiteThemeV2,
  SkuMatrixSectionProps,
  StatBlockProps,
  TemplatePageKey,
  TemplateValidationIssue,
  TemplateValidationResult,
  TestimonialsSectionProps,
} from "@/features/site/templateTypes";

import type { SiteStatus } from "@/lib/site/types";
import type { SiteTemplateConfig } from "@/features/site/templateTypes";

export interface GwSite {
  id: string;
  shop_id: string;
  shop_name?: string;
  subdomain: string;
  primary_host: string;
  status: SiteStatus | string;
  config?: SiteTemplateConfig | null;
  draft_config: SiteTemplateConfig | null;
  published_config?: SiteTemplateConfig | null;
  published_version_id: string;
  published_version_number?: number;
  published_at?: string;
  suspended_reason: string;
  created_at?: string;
  updated_at?: string;
}

export interface GwSiteVersion {
  id: string;
  site_id: string;
  version_number: number;
  config: SiteTemplateConfig | null;
  published_by: string;
  note: string;
  created_at?: string;
}

export interface SellerSiteEnvelope {
  site: GwSite | null;
  shop: {
    id: string;
    name: string;
    description?: string;
    status: string;
  } | null;
  root_domain: string;
}

export interface AdminSitesListResponse {
  items: GwSite[];
  total: number;
  page: number;
  page_size: number;
  root_domain?: string;
}
