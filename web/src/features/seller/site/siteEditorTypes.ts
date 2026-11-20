// Editor & Admin Site DTO definitions aligned directly with shared @/lib/site/types.
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

import type { SiteConfig, SiteStatus } from "@/lib/site/types";

export interface GwSite {
  id: string;
  shop_id: string;
  shop_name?: string;
  subdomain: string;
  primary_host: string;
  status: SiteStatus | string;
  config?: SiteConfig | null;
  draft_config: SiteConfig | null;
  published_config?: SiteConfig | null;
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
  config: SiteConfig | null;
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
