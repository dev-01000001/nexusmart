"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AlertCircle,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ExternalLink,
  Eye,
  EyeOff,
  History,
  Plus,
  RotateCcw,
  Trash2,
} from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  StatusBadge,
  siteStatusTone,
} from "@/components/app";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useSidebar } from "@/components/ui/sidebar";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import type { SiteDomainDTO } from "@/lib/site/domains";
import { SITE_UI_FONT_STACKS } from "@/lib/site/theme";
import { SITE_FONT_LABELS } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { PLATFORM_TEMPLATES, cloneTemplateConfigForShop } from "@/features/site/templateCatalog";
import { useSellerContext } from "@/features/seller/SellerShell";
import { SELLER_SIDEBAR_STORAGE_KEY } from "@/features/seller/shell/sellerNav";
import type { GwCategory, GwSellerProduct, GwSellerShop } from "@/features/seller/sellerApiTypes";
import type {
  CartPageConfig,
  CatalogPageConfig,
  GwSite,
  GwSiteVersion,
  InfoPageConfig,
  PdpPageConfig,
  PlatformTemplateDTO,
  SiteArchetype,
  SiteCardStyle,
  SiteCartLayout,
  SiteCatalogLayout,
  SiteContainerWidth,
  SiteDensity,
  SiteFontFamily,
  SiteFooterStyle,
  SiteHeaderStyle,
  SiteInfoLayout,
  SiteLayoutConfig,
  SitePdpGalleryStyle,
  SitePdpLayout,
  SiteRadius,
  SiteSectionV2,
  SiteSectionV2Type,
  SiteTemplateConfig,
  SiteThemeV2,
  TemplatePageKey,
} from "./siteEditorTypes";
import {
  ARCHETYPE_OPTIONS,
  CARD_STYLE_OPTIONS,
  CART_LAYOUT_OPTIONS,
  CATALOG_LAYOUT_OPTIONS,
  CONTAINER_WIDTH_OPTIONS,
  DENSITY_OPTIONS,
  FOOTER_STYLE_OPTIONS,
  HEADER_STYLE_OPTIONS,
  INFO_LAYOUT_OPTIONS,
  MAX_TOTAL_SECTIONS,
  PDP_GALLERY_OPTIONS,
  PDP_LAYOUT_OPTIONS,
  QUICK_MEDIA_ASSETS,
  RADIUS_OPTIONS,
  SECTION_V2_TYPE_META,
  SECTION_V2_TYPE_ORDER,
  SECTION_VARIANT_LABELS,
  TEMPLATE_PAGE_TABS,
  THEME_PRESETS,
  buildCleanTemplateConfigPayload,
  checkThemeContrast,
  checkUnsafeText,
  countAllSections,
  createDefaultTemplateConfig,
  createSectionV2Template,
  formatContrastRatio,
  getPageSections,
  getSectionHeadingPreview,
  getSiteStatusMeta,
  isValidHexColor,
  normalizeSubdomainInput,
  setPageSections,
  suggestSubdomainFromShopName,
  validateImageUrlStrict,
  validateSubdomainClient,
  validateTemplateDraftClient,
} from "./siteEditorUtils";
import { buildPreviewStorefrontDataset } from "./previewData";
import { PreviewToolbar } from "./PreviewToolbar";
import { SectionEditorPanel } from "./SectionEditorPanel";
import { SellerSiteDomainsSection } from "./SellerSiteDomainsSection";
import { SitePreviewContent } from "./SitePreviewContent";
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";
import { TemplateCodeEditorTab } from "./TemplateCodeEditorTab";
import { TemplateGalleryTab } from "./TemplateGalleryTab";
import { buildPublicSiteUrl } from "./siteSummary";

export type EditorTab = "templates" | "customize" | "code" | "domains" | "versions";

const DEFAULT_SELECT_VALUE = "__default__";

const SITE_FONT_OPTIONS: ReadonlyArray<{
  value: SiteFontFamily;
  label: string;
  stack: string;
}> = (Object.keys(SITE_UI_FONT_STACKS) as SiteFontFamily[]).map((fontKey) => ({
  value: fontKey,
  label: SITE_FONT_LABELS[fontKey] ?? fontKey,
  stack: SITE_UI_FONT_STACKS[fontKey],
}));

export interface SellerSiteEditorClientProps {
  initialSite: GwSite | null;
  initialShop: GwSellerShop | null;
  initialVersions: GwSiteVersion[];
  initialDomains?: SiteDomainDTO[];
  initialTemplates?: PlatformTemplateDTO[];
  domainsLoadError?: string | null;
  initialTab?: EditorTab;
  initialCustomizeSection?: "theme" | "sections";
  products: GwSellerProduct[];
  categories: GwCategory[];
  rootDomain: string;
  requestHost: string;
  requestProto: string;
  loadError: string | null;
}

export function SellerSiteEditorClient({
  initialSite,
  initialShop,
  initialVersions,
  initialDomains = [],
  initialTemplates,
  domainsLoadError = null,
  initialTab = "templates",
  initialCustomizeSection = "theme",
  products,
  categories,
  rootDomain,
  requestHost,
  requestProto,
  loadError,
}: SellerSiteEditorClientProps) {
  const router = useRouter();
  const { shop: shellShop, showToast } = useSellerContext();
  const { setOpen } = useSidebar();
  const setOpenRef = useRef(setOpen);
  setOpenRef.current = setOpen;

  // Collapse SellerShell left navigation to icon rail while in Site Studio (A7)
  useEffect(() => {
    let prevStored: string | null = null;
    try {
      prevStored = window.localStorage.getItem(SELLER_SIDEBAR_STORAGE_KEY);
    } catch {
      // Ignore storage access errors
    }

    setOpenRef.current(false);

    try {
      if (prevStored === null) {
        window.localStorage.removeItem(SELLER_SIDEBAR_STORAGE_KEY);
      } else {
        window.localStorage.setItem(SELLER_SIDEBAR_STORAGE_KEY, prevStored);
      }
    } catch {
      // Ignore storage access errors
    }

    return () => {
      const isDesktop =
        typeof window !== "undefined" && window.matchMedia("(min-width: 1024px)").matches;
      if (isDesktop && prevStored !== "false") {
        setOpenRef.current(true);
        try {
          if (prevStored === null) {
            window.localStorage.removeItem(SELLER_SIDEBAR_STORAGE_KEY);
          } else {
            window.localStorage.setItem(SELLER_SIDEBAR_STORAGE_KEY, prevStored);
          }
        } catch {
          // Ignore storage access errors
        }
      }
    };
  }, []);

  const activeShop = initialShop ?? shellShop;

  const templates = useMemo<PlatformTemplateDTO[]>(
    () =>
      initialTemplates && initialTemplates.length > 0 ? initialTemplates : [...PLATFORM_TEMPLATES],
    [initialTemplates]
  );

  const [site, setSite] = useState<GwSite | null>(initialSite);
  const [versions, setVersions] = useState<GwSiteVersion[]>(initialVersions);
  const [domains, setDomains] = useState<SiteDomainDTO[]>(initialDomains);
  const [activeTab, setActiveTab] = useState<EditorTab>(initialTab);
  const [openAccordions, setOpenAccordions] = useState<string[]>(() =>
    initialCustomizeSection === "sections" ? ["sections"] : ["colors", "brand-font"]
  );

  useEffect(() => {
    setDomains(initialDomains);
  }, [initialDomains]);

  const [subdomain, setSubdomain] = useState<string>(
    initialSite?.subdomain || suggestSubdomainFromShopName(activeShop?.name ?? "")
  );
  const [config, setConfig] = useState<SiteTemplateConfig>(() => {
    if (initialSite?.draft_config) return initialSite.draft_config;
    return createDefaultTemplateConfig(activeShop?.name ?? "", activeShop?.description ?? "");
  });

  const [sectionPageKey, setSectionPageKey] = useState<TemplatePageKey>("home");
  const [selectedSectionId, setSelectedSectionId] = useState<string | null>(
    () => initialSite?.draft_config?.sections[0]?.id ?? "hero-main"
  );
  const [newSectionType, setNewSectionType] = useState<SiteSectionV2Type>("banner");

  // Shared live preview state for Customize split workspace
  const [previewPage, setPreviewPage] = useState<TemplatePageKey>("home");
  const [previewDevice, setPreviewDevice] = useState<DevicePreviewWidth>("1440");

  const [dirty, setDirty] = useState<boolean>(!initialSite);
  const [formError, setFormError] = useState<string | null>(null);
  const [savingDraft, setSavingDraft] = useState<boolean>(false);
  const [applyingTemplateId, setApplyingTemplateId] = useState<string | null>(null);
  const [publishing, setPublishing] = useState<boolean>(false);
  const [publishOpen, setPublishOpen] = useState<boolean>(false);
  const [publishNote, setPublishNote] = useState<string>("");
  const [rollbackTarget, setRollbackTarget] = useState<GwSiteVersion | null>(null);
  const [rollbackNote, setRollbackNote] = useState<string>("");
  const [rollingBackId, setRollingBackId] = useState<string | null>(null);

  const contrastChecks = useMemo(() => checkThemeContrast(config.theme), [config.theme]);
  const allContrastPass = contrastChecks.every((c) => c.passes);
  const subdomainError = useMemo(() => validateSubdomainClient(subdomain), [subdomain]);
  const logoUrlError = useMemo(
    () => validateImageUrlStrict(config.theme.logo_url, false),
    [config.theme.logo_url]
  );
  const bannerUrlError = useMemo(
    () => validateImageUrlStrict(config.theme.banner_url, false),
    [config.theme.banner_url]
  );

  const previewDataset = useMemo(
    () =>
      buildPreviewStorefrontDataset({
        shopId: activeShop?.id || site?.shop_id || "preview-shop",
        shopName: activeShop?.name || site?.shop_name || config.theme.brand_name,
        shopDescription: activeShop?.description || config.theme.tagline,
        sellerProducts: products,
        sellerCategories: categories,
      }),
    [activeShop, site, config.theme.brand_name, config.theme.tagline, products, categories]
  );

  const platformOrigin = useMemo(() => {
    const scheme = requestProto === "https" ? "https" : "http";
    const host = requestHost || rootDomain || "localhost:3300";
    return `${scheme}://${host}`;
  }, [requestProto, requestHost, rootDomain]);

  const statusMeta = getSiteStatusMeta(site?.status);
  const isSuspended = (site?.status ?? "").toUpperCase() === "SUSPENDED";
  const isPublished = (site?.status ?? "").toUpperCase() === "PUBLISHED";
  const cleanSub = normalizeSubdomainInput(subdomain);
  const previewHost = cleanSub ? `${cleanSub}.${rootDomain}` : `<ten-mien>.${rootDomain}`;
  const activeHostDisplay = site?.primary_host || previewHost;

  const activePageMeta = useMemo(
    () => TEMPLATE_PAGE_TABS.find((t) => t.key === sectionPageKey) ?? TEMPLATE_PAGE_TABS[0],
    [sectionPageKey]
  );
  const activePageSections = useMemo(
    () => getPageSections(config, sectionPageKey),
    [config, sectionPageKey]
  );
  const totalSectionsCount = useMemo(() => countAllSections(config), [config]);

  const selectedSection = useMemo(() => {
    if (activePageSections.length === 0) return null;
    if (selectedSectionId) {
      const found = activePageSections.find((s) => s.id === selectedSectionId);
      if (found) return found;
    }
    return activePageSections[0];
  }, [activePageSections, selectedSectionId]);

  const activePublishedVersionNumber = useMemo(() => {
    if (site?.published_version_number) return site.published_version_number;
    if (!site?.published_version_id) return null;
    const found = versions.find((v) => v.id === site.published_version_id);
    return found?.version_number ?? null;
  }, [site, versions]);

  const currentTemplateMeta = useMemo(
    () => templates.find((t) => t.id === config.template_id) ?? null,
    [templates, config.template_id]
  );

  const updateTheme = (patch: Partial<SiteThemeV2>) => {
    setConfig((prev) => ({
      ...prev,
      theme: { ...prev.theme, ...patch },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateLayout = (patch: Partial<SiteLayoutConfig>) => {
    setConfig((prev) => ({
      ...prev,
      layout: { ...prev.layout, ...patch },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateSlots = (patch: Partial<SiteLayoutConfig["slots"]>) => {
    setConfig((prev) => ({
      ...prev,
      layout: {
        ...prev.layout,
        slots: { ...prev.layout.slots, ...patch },
      },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateCatalogPage = (patch: Partial<CatalogPageConfig>) => {
    setConfig((prev) => ({
      ...prev,
      pages: { ...prev.pages, catalog: { ...prev.pages.catalog, ...patch } },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updatePdpPage = (patch: Partial<PdpPageConfig>) => {
    setConfig((prev) => ({
      ...prev,
      pages: { ...prev.pages, pdp: { ...prev.pages.pdp, ...patch } },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateCartPage = (patch: Partial<CartPageConfig>) => {
    setConfig((prev) => ({
      ...prev,
      pages: { ...prev.pages, cart: { ...prev.pages.cart, ...patch } },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateInfoPage = (patch: Partial<InfoPageConfig>) => {
    setConfig((prev) => ({
      ...prev,
      pages: { ...prev.pages, info: { ...prev.pages.info, ...patch } },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateActivePageSections = (updater: (prev: SiteSectionV2[]) => SiteSectionV2[]) => {
    setConfig((prev) =>
      setPageSections(prev, sectionPageKey, updater(getPageSections(prev, sectionPageKey)))
    );
    setDirty(true);
    setFormError(null);
  };

  const handleAddSection = () => {
    if (activePageSections.length >= activePageMeta.maxSections) {
      setFormError(
        `${activePageMeta.label} hỗ trợ tối đa ${activePageMeta.maxSections} khối nội dung.`
      );
      return;
    }
    if (totalSectionsCount >= MAX_TOTAL_SECTIONS) {
      setFormError(`Toàn bộ site hỗ trợ tối đa ${MAX_TOTAL_SECTIONS} khối nội dung.`);
      return;
    }
    const created = createSectionV2Template(newSectionType, activePageSections.length);
    updateActivePageSections((prev) => [...prev, created]);
    setSelectedSectionId(created.id);
    showToast(
      `Đã thêm khối "${SECTION_V2_TYPE_META[newSectionType].label}" vào ${activePageMeta.label}.`
    );
  };

  const handleMoveSection = (index: number, delta: -1 | 1) => {
    const target = index + delta;
    if (target < 0 || target >= activePageSections.length) return;
    updateActivePageSections((prev) => {
      const copy = [...prev];
      const [moved] = copy.splice(index, 1);
      copy.splice(target, 0, moved);
      return copy;
    });
  };

  const handleDeleteSection = (id: string) => {
    updateActivePageSections((prev) => {
      const next = prev.filter((s) => s.id !== id);
      if (selectedSectionId === id) {
        setSelectedSectionId(next[0]?.id ?? null);
      }
      return next;
    });
  };

  const handleApplyTemplate = async (template: PlatformTemplateDTO) => {
    const subErr = validateSubdomainClient(cleanSub);
    if (subErr) {
      setFormError(subErr);
      setActiveTab("domains");
      return;
    }
    setFormError(null);
    setApplyingTemplateId(template.id);
    try {
      const res = await api<{ site: GwSite | null; template_id?: string }>(
        "/api/seller/site/apply-template",
        {
          method: "POST",
          body: {
            template_id: template.id,
            subdomain: cleanSub,
          },
        }
      );
      if (res.site) {
        setSite(res.site);
        const appliedCfg =
          res.site.draft_config ??
          cloneTemplateConfigForShop(template, {
            brandName: config.theme.brand_name || activeShop?.name,
            tagline: config.theme.tagline || activeShop?.description,
            logoUrl: config.theme.logo_url,
          });
        setConfig(appliedCfg);
        setSubdomain(res.site.subdomain || cleanSub);
        setSectionPageKey("home");
        setPreviewPage("home");
        setSelectedSectionId(appliedCfg.sections[0]?.id ?? null);
        setDirty(false);
        showToast(
          `Đã áp dụng mẫu "${template.name}" vào bản nháp. Trang công khai giữ nguyên tới khi Xuất bản.`
        );
        router.refresh();
      }
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Không thể áp dụng mẫu giao diện lúc này. Vui lòng thử lại.";
      setFormError(msg);
      showToast(msg);
    } finally {
      setApplyingTemplateId(null);
    }
  };

  const refreshVersions = async () => {
    try {
      const res = await api<{ versions: GwSiteVersion[] }>("/api/seller/site/versions");
      setVersions(res.versions ?? []);
    } catch {
      // Non-blocking refresh
    }
  };

  const saveDraftInternal = async (): Promise<GwSite | null> => {
    const cleanPayload = buildCleanTemplateConfigPayload(config);
    const clientErr = validateTemplateDraftClient(cleanSub, cleanPayload);
    if (clientErr) {
      setFormError(clientErr);
      return null;
    }
    setFormError(null);
    setSavingDraft(true);
    try {
      const res = await api<{ site: GwSite | null }>("/api/seller/site/draft", {
        method: "PUT",
        body: {
          subdomain: cleanSub,
          config: cleanPayload,
        },
      });
      if (res.site) {
        setSite(res.site);
        if (res.site.draft_config) {
          setConfig(res.site.draft_config);
        }
        setSubdomain(res.site.subdomain || cleanSub);
      }
      setDirty(false);
      return res.site ?? site;
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể lưu bản nháp site riêng lúc này.";
      setFormError(msg);
      showToast(msg);
      return null;
    } finally {
      setSavingDraft(false);
    }
  };

  const handleSaveDraftClick = async () => {
    const saved = await saveDraftInternal();
    if (saved) {
      showToast("Đã lưu bản nháp site riêng.");
      router.refresh();
    }
  };

  const handleConfirmPublish = async () => {
    if (isSuspended) return;
    setFormError(null);

    if (dirty || !site) {
      const saved = await saveDraftInternal();
      if (!saved) return;
    }

    setPublishing(true);
    try {
      const res = await api<{ site: GwSite | null; version: GwSiteVersion | null }>(
        "/api/seller/site/publish",
        {
          method: "POST",
          body: { note: publishNote.trim() },
        }
      );
      if (res.site) {
        setSite(res.site);
      }
      setPublishNote("");
      setPublishOpen(false);
      await refreshVersions();
      const verLabel = res.version?.version_number ? `v${res.version.version_number}` : "mới nhất";
      showToast(`Đã xuất bản phiên bản ${verLabel} lên ${res.site?.primary_host || previewHost}.`);
      router.refresh();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Không thể xuất bản site riêng lúc này.";
      setFormError(msg);
      showToast(msg);
    } finally {
      setPublishing(false);
    }
  };

  const handleRollback = async (version: GwSiteVersion) => {
    if (isSuspended) return;
    setFormError(null);
    setRollingBackId(version.id);
    try {
      const res = await api<{ site: GwSite | null; version: GwSiteVersion | null }>(
        "/api/seller/site/rollback",
        {
          method: "POST",
          body: {
            version_id: version.id,
            note: rollbackNote.trim() || `Khôi phục về phiên bản v${version.version_number}`,
          },
        }
      );
      if (res.site) {
        setSite(res.site);
        if (res.site.draft_config) {
          setConfig(res.site.draft_config);
          setSectionPageKey("home");
          setPreviewPage("home");
          setSelectedSectionId(res.site.draft_config.sections[0]?.id ?? null);
        }
        setSubdomain(res.site.subdomain || subdomain);
      }
      setDirty(false);
      setRollbackNote("");
      setRollbackTarget(null);
      await refreshVersions();
      showToast(`Đã khôi phục site riêng về phiên bản v${version.version_number}.`);
      router.refresh();
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể khôi phục phiên bản đã chọn lúc này.";
      setFormError(msg);
      showToast(msg);
    } finally {
      setRollingBackId(null);
    }
  };

  const handleDomainsChange = (nextDomains: SiteDomainDTO[]) => {
    setDomains(nextDomains);
    const activeCustom = nextDomains.find(
      (d) => d.kind === "CUSTOM" && d.status === "ACTIVE" && d.is_primary
    );
    setSite((prev) => {
      if (!prev) return prev;
      const fallbackHost = prev.subdomain ? `${prev.subdomain}.${rootDomain}` : prev.primary_host;
      const nextPrimary = activeCustom ? activeCustom.host : fallbackHost;
      if (nextPrimary === prev.primary_host) return prev;
      return { ...prev, primary_host: nextPrimary };
    });
    router.refresh();
  };

  if (loadError) {
    return (
      <div className="space-y-4">
        <div className="rounded-xl bg-card px-4 py-3 shadow-card">
          <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground">
            Site bán hàng
          </h1>
        </div>
        <ErrorState
          title="Không thể tải cấu hình site riêng"
          description={loadError}
          onRetry={() => router.refresh()}
        />
      </div>
    );
  }

  const activePresetId = THEME_PRESETS.find(
    (p) =>
      p.colors.bg_color.toUpperCase() === config.theme.bg_color.toUpperCase() &&
      p.colors.surface_color.toUpperCase() === config.theme.surface_color.toUpperCase() &&
      p.colors.text_color.toUpperCase() === config.theme.text_color.toUpperCase() &&
      p.colors.accent_color.toUpperCase() === config.theme.accent_color.toUpperCase() &&
      p.colors.accent_text_color.toUpperCase() === config.theme.accent_text_color.toUpperCase()
  )?.id;

  const colorFields: Array<{ key: keyof SiteThemeV2; label: string }> = [
    { key: "bg_color", label: "Nền trang" },
    { key: "surface_color", label: "Nền thẻ" },
    { key: "text_color", label: "Chữ chính" },
    { key: "accent_color", label: "Màu nhấn" },
    { key: "accent_text_color", label: "Chữ trên nút" },
  ];

  const brandUnsafe = checkUnsafeText(config.theme.brand_name);
  const taglineUnsafe = checkUnsafeText(config.theme.tagline ?? "");
  const announceUnsafe = checkUnsafeText(config.layout.announcement_text ?? "");

  const currentFontKey: SiteFontFamily = Object.prototype.hasOwnProperty.call(
    SITE_UI_FONT_STACKS,
    config.theme.font_family
  )
    ? config.theme.font_family
    : "work-sans";

  const publicSiteHref =
    isPublished && site?.primary_host
      ? buildPublicSiteUrl(site.primary_host, requestHost, requestProto)
      : "";

  const rollbackTargetTemplateLabel = rollbackTarget
    ? templates.find((t) => t.id === (rollbackTarget.config?.template_id || "legacy-v1"))?.name ||
      rollbackTarget.config?.template_id ||
      "legacy-v1"
    : "";

  return (
    <div className="space-y-4">
      {/* Studio Top Bar (A7) */}
      <header className="flex flex-col gap-3 rounded-xl bg-card px-4 py-3 shadow-card md:flex-row md:items-center md:justify-between">
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-lg leading-7 font-semibold tracking-tight text-foreground sm:text-xl">
              Site bán hàng
            </h1>
            <StatusBadge tone={siteStatusTone(site?.status)}>
              {statusMeta.label}
              {activePublishedVersionNumber ? ` v${activePublishedVersionNumber}` : ""}
            </StatusBadge>
            <span className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <span
                aria-hidden="true"
                className={cn(
                  "size-2 shrink-0 rounded-full",
                  dirty ? "bg-badge-warning-ink" : "bg-badge-success-ink"
                )}
              />
              <span>{dirty ? "Bản nháp chưa lưu" : "Đã đồng bộ"}</span>
            </span>
          </div>

          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[13px] text-muted-foreground">
            <span className="font-medium break-words [overflow-wrap:anywhere] text-foreground">
              {activeHostDisplay}
            </span>
            {publicSiteHref ? (
              <a
                href={publicSiteHref}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 font-medium text-brand hover:underline"
              >
                <span>Mở site</span>
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </a>
            ) : null}
            <span aria-hidden="true">·</span>
            <span>Mẫu: {currentTemplateMeta?.name || config.template_id}</span>
            <span aria-hidden="true">·</span>
            <span className="tabular-nums">
              {totalSectionsCount}/{MAX_TOTAL_SECTIONS} khối
            </span>
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {site ? (
            <Button variant="ghost" size="sm" asChild className="min-h-10 md:min-h-8">
              <Link href="/seller/site/xem-truoc">Xem trước</Link>
            </Button>
          ) : (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled
              className="min-h-10 md:min-h-8"
            >
              Xem trước
            </Button>
          )}

          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={savingDraft || publishing || Boolean(applyingTemplateId)}
            onClick={() => void handleSaveDraftClick()}
            className="min-h-10 md:min-h-8"
          >
            {savingDraft ? "Đang lưu..." : "Lưu nháp"}
          </Button>

          <Button
            type="button"
            size="sm"
            disabled={isSuspended || savingDraft || publishing || Boolean(applyingTemplateId)}
            onClick={() => setPublishOpen(true)}
            className="min-h-10 md:min-h-8"
          >
            {publishing ? "Đang xuất bản..." : "Xuất bản"}
          </Button>
        </div>
      </header>

      {isSuspended && (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertTitle>Site riêng đang bị tạm khóa bởi quản trị NexusMart</AlertTitle>
          <AlertDescription>
            {site?.suspended_reason ? `Lý do: ${site.suspended_reason}. ` : ""}
            Bạn vẫn có thể chỉnh sửa và lưu bản nháp, nhưng cần quản trị mở khóa trước khi xuất bản
            lại.
          </AlertDescription>
        </Alert>
      )}

      {formError && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      {/* 5-Mode Studio Tabs (A7) */}
      <Tabs
        value={activeTab}
        onValueChange={(next) => setActiveTab(next as EditorTab)}
        className="gap-4"
      >
        <TabsList
          aria-label="Các chế độ cấu hình Site Studio"
          className="flex w-full flex-wrap justify-start gap-1 rounded-xl bg-card p-1.5 shadow-card group-data-[orientation=horizontal]/tabs:h-auto"
        >
          <TabsTrigger value="templates" className="min-h-10 shrink-0 md:min-h-8">
            <span>Mẫu</span>
            <span className="text-xs text-muted-foreground tabular-nums">{templates.length}</span>
          </TabsTrigger>
          <TabsTrigger value="customize" className="min-h-10 shrink-0 md:min-h-8">
            <span>Tuỳ chỉnh</span>
            <span className="text-xs text-muted-foreground tabular-nums">{totalSectionsCount}</span>
          </TabsTrigger>
          <TabsTrigger value="code" className="min-h-10 shrink-0 md:min-h-8">
            Mã JSON
          </TabsTrigger>
          <TabsTrigger value="domains" className="min-h-10 shrink-0 md:min-h-8">
            Tên miền
          </TabsTrigger>
          <TabsTrigger value="versions" className="min-h-10 shrink-0 md:min-h-8">
            <span>Phiên bản</span>
            <span className="text-xs text-muted-foreground tabular-nums">{versions.length}</span>
          </TabsTrigger>
        </TabsList>

        {/* MODE 1: Mẫu (A8 3-column gallery + large Dialog preview) */}
        <TabsContent value="templates">
          <TemplateGalleryTab
            templates={templates}
            currentTemplateId={config.template_id}
            currentConfig={config}
            previewDataset={previewDataset}
            platformOrigin={platformOrigin}
            applyingTemplateId={applyingTemplateId}
            onApplyTemplate={handleApplyTemplate}
          />
        </TabsContent>

        {/* MODE 2: Tuỳ chỉnh — Left 360px Accordion Inspector + Right Live Preview */}
        <TabsContent value="customize">
          <div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-[360px_minmax(0,1fr)]">
            <Card className="py-2 md:py-2">
              <CardContent className="px-4">
                <Accordion type="multiple" value={openAccordions} onValueChange={setOpenAccordions}>
                  {/* Group 1: Colors & WCAG Contrast */}
                  <AccordionItem value="colors">
                    <AccordionTrigger className="items-center py-3.5 hover:no-underline">
                      <span className="flex items-center gap-2 font-semibold text-foreground">
                        <span>Màu sắc &amp; Tương phản</span>
                        <StatusBadge tone={allContrastPass ? "success" : "critical"}>
                          {allContrastPass ? "Đạt chuẩn" : "Cần sửa"}
                        </StatusBadge>
                      </span>
                    </AccordionTrigger>
                    <AccordionContent className="space-y-3.5">
                      <div className="grid grid-cols-2 gap-2">
                        {THEME_PRESETS.map((preset) => {
                          const active = activePresetId === preset.id;
                          return (
                            <button
                              key={preset.id}
                              type="button"
                              onClick={() => updateTheme(preset.colors)}
                              className={cn(
                                "flex flex-col gap-1.5 rounded-lg border px-2.5 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                                active
                                  ? "border-brand bg-badge-info-bg/45"
                                  : "border-border bg-muted/35 hover:border-input hover:bg-muted/70"
                              )}
                            >
                              <div className="flex items-center justify-between gap-1.5">
                                <span className="truncate text-xs font-semibold text-foreground">
                                  {preset.name}
                                </span>
                                <span
                                  className="inline-flex shrink-0 items-center gap-1"
                                  aria-hidden="true"
                                >
                                  <span
                                    className="size-2.5 rounded-full border border-input/60"
                                    style={{ backgroundColor: preset.colors.bg_color }}
                                  />
                                  <span
                                    className="size-2.5 rounded-full border border-input/60"
                                    style={{ backgroundColor: preset.colors.text_color }}
                                  />
                                  <span
                                    className="size-2.5 rounded-full border border-input/60"
                                    style={{ backgroundColor: preset.colors.accent_color }}
                                  />
                                </span>
                              </div>
                            </button>
                          );
                        })}
                      </div>

                      <div className="grid grid-cols-2 gap-2.5">
                        {colorFields.map((field) => {
                          const rawVal = String(config.theme[field.key] ?? "");
                          const validHex = isValidHexColor(rawVal);
                          return (
                            <Field key={field.key} className="gap-1">
                              <FieldLabel
                                htmlFor={`color-${field.key}`}
                                className="text-xs font-medium"
                              >
                                {field.label}
                              </FieldLabel>
                              <div className="flex items-center gap-1.5">
                                <input
                                  type="color"
                                  aria-label={`Chọn ${field.label}`}
                                  value={validHex ? rawVal : "#FFFFFF"}
                                  onChange={(e) =>
                                    updateTheme({ [field.key]: e.target.value.toUpperCase() })
                                  }
                                  className="h-8 w-8 shrink-0 cursor-pointer rounded-md border border-input bg-card p-0.5"
                                />
                                <Input
                                  id={`color-${field.key}`}
                                  type="text"
                                  value={rawVal}
                                  maxLength={7}
                                  onChange={(e) =>
                                    updateTheme({ [field.key]: e.target.value.toUpperCase() })
                                  }
                                  placeholder="#FFFFFF"
                                  className="h-8 bg-card px-2 font-mono text-xs tabular-nums"
                                />
                              </div>
                            </Field>
                          );
                        })}
                      </div>

                      <div
                        className={cn(
                          "space-y-2 rounded-lg border p-3",
                          allContrastPass
                            ? "border-border bg-muted/35"
                            : "border-destructive/40 bg-badge-critical-bg/45"
                        )}
                        aria-live="polite"
                      >
                        <div className="flex items-center justify-between gap-2 text-xs font-semibold text-foreground">
                          <span>Tương phản chữ / nền (≥ 4,5:1)</span>
                        </div>
                        {!allContrastPass && (
                          <p
                            role="alert"
                            className="text-xs leading-snug font-medium text-badge-critical-ink"
                          >
                            Cảnh báo tương phản thấp: hãy điều chỉnh cặp màu đạt tối thiểu 4,5:1 để
                            đảm bảo dễ đọc trước khi lưu nháp.
                          </p>
                        )}
                        <div className="space-y-1.5">
                          {contrastChecks.map((check) => (
                            <div
                              key={check.id}
                              className="flex items-center justify-between gap-2 text-xs"
                            >
                              <span className="min-w-0 truncate text-muted-foreground">
                                {check.label}
                              </span>
                              <span className="flex shrink-0 items-center gap-1.5">
                                <span className="font-semibold text-foreground tabular-nums">
                                  {formatContrastRatio(check.ratio)}
                                </span>
                                <StatusBadge tone={check.passes ? "success" : "critical"}>
                                  {check.passes ? "Đạt" : "Thấp"}
                                </StatusBadge>
                              </span>
                            </div>
                          ))}
                        </div>
                      </div>
                    </AccordionContent>
                  </AccordionItem>

                  {/* Group 2: Brand, Font (§17.2), Logo / Banner */}
                  <AccordionItem value="brand-font">
                    <AccordionTrigger className="py-3.5 font-semibold text-foreground hover:no-underline">
                      Phông chữ, Thương hiệu &amp; Ảnh
                    </AccordionTrigger>
                    <AccordionContent className="space-y-3">
                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="site-font-family" className="text-xs font-medium">
                          Phông chữ site
                        </FieldLabel>
                        <Select
                          value={currentFontKey}
                          onValueChange={(v) => updateTheme({ font_family: v as SiteFontFamily })}
                        >
                          <SelectTrigger id="site-font-family" size="sm" className="w-full bg-card">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {SITE_FONT_OPTIONS.map((fontOpt) => (
                              <SelectItem
                                key={fontOpt.value}
                                value={fontOpt.value}
                                style={{ fontFamily: fontOpt.stack }}
                              >
                                {fontOpt.label}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        <div
                          className="rounded-md border border-border bg-muted/40 px-2.5 py-1.5 text-xs text-foreground"
                          style={{ fontFamily: SITE_UI_FONT_STACKS[currentFontKey] }}
                        >
                          Mẫu chữ: {SITE_FONT_LABELS[currentFontKey]} · Tiếng Việt · 249.000₫
                        </div>
                      </Field>

                      <Field className="gap-1.5">
                        <div className="flex items-center justify-between gap-2">
                          <FieldLabel htmlFor="site-brand" className="text-xs font-medium">
                            Tên thương hiệu
                          </FieldLabel>
                          <span className="text-[11px] text-muted-foreground tabular-nums">
                            {config.theme.brand_name.length}/80
                          </span>
                        </div>
                        <Input
                          id="site-brand"
                          type="text"
                          value={config.theme.brand_name}
                          maxLength={80}
                          onChange={(e) => updateTheme({ brand_name: e.target.value })}
                          className="h-8 bg-card text-[13px]"
                        />
                        {brandUnsafe ? (
                          <FieldError className="text-xs">Bị chặn vì {brandUnsafe}.</FieldError>
                        ) : null}
                      </Field>

                      <div className="grid grid-cols-2 gap-2.5">
                        <Field className="gap-1">
                          <FieldLabel htmlFor="site-radius" className="text-xs font-medium">
                            Bo góc
                          </FieldLabel>
                          <Select
                            value={config.theme.radius || DEFAULT_SELECT_VALUE}
                            onValueChange={(v) =>
                              updateTheme({
                                radius: (v === DEFAULT_SELECT_VALUE ? "" : v) as SiteRadius,
                              })
                            }
                          >
                            <SelectTrigger id="site-radius" size="sm" className="w-full bg-card">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {RADIUS_OPTIONS.map((r) => (
                                <SelectItem
                                  key={r.value || "default"}
                                  value={r.value || DEFAULT_SELECT_VALUE}
                                >
                                  {r.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>

                        <Field className="gap-1">
                          <FieldLabel htmlFor="site-density" className="text-xs font-medium">
                            Mật độ
                          </FieldLabel>
                          <Select
                            value={config.theme.density || DEFAULT_SELECT_VALUE}
                            onValueChange={(v) =>
                              updateTheme({
                                density: (v === DEFAULT_SELECT_VALUE ? "" : v) as SiteDensity,
                              })
                            }
                          >
                            <SelectTrigger id="site-density" size="sm" className="w-full bg-card">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {DENSITY_OPTIONS.map((d) => (
                                <SelectItem
                                  key={d.value || "default"}
                                  value={d.value || DEFAULT_SELECT_VALUE}
                                >
                                  {d.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>
                      </div>

                      <Field className="gap-1.5">
                        <div className="flex items-center justify-between gap-2">
                          <FieldLabel htmlFor="site-tagline" className="text-xs font-medium">
                            Khẩu hiệu ngắn
                          </FieldLabel>
                          <span className="text-[11px] text-muted-foreground tabular-nums">
                            {(config.theme.tagline ?? "").length}/160
                          </span>
                        </div>
                        <Input
                          id="site-tagline"
                          type="text"
                          value={config.theme.tagline ?? ""}
                          maxLength={160}
                          onChange={(e) => updateTheme({ tagline: e.target.value })}
                          placeholder="Áo thun cotton 220gsm may trực tiếp tại xưởng"
                          className="h-8 bg-card text-[13px]"
                        />
                        {taglineUnsafe ? (
                          <FieldError className="text-xs">Bị chặn vì {taglineUnsafe}.</FieldError>
                        ) : null}
                      </Field>

                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="site-logo-url" className="text-xs font-medium">
                          Ảnh logo
                        </FieldLabel>
                        <Input
                          id="site-logo-url"
                          type="text"
                          value={config.theme.logo_url ?? ""}
                          maxLength={500}
                          onChange={(e) => updateTheme({ logo_url: e.target.value })}
                          placeholder="/banners/vietnam-workshop.jpg"
                          aria-invalid={Boolean(logoUrlError)}
                          className="h-8 bg-card text-[13px]"
                        />
                        {logoUrlError ? (
                          <FieldError className="text-xs">{logoUrlError}</FieldError>
                        ) : (
                          <div className="flex flex-wrap gap-1.5 pt-0.5">
                            {QUICK_MEDIA_ASSETS.slice(0, 3).map((asset) => {
                              const active = config.theme.logo_url === asset.url;
                              return (
                                <button
                                  key={`logo-${asset.url}`}
                                  type="button"
                                  onClick={() => updateTheme({ logo_url: asset.url })}
                                  className={cn(
                                    "inline-flex min-h-7 items-center rounded-sm border px-2 py-0.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                                    active
                                      ? "border-brand/40 bg-badge-info-bg text-badge-info-ink"
                                      : "border-border bg-muted/60 text-muted-foreground hover:bg-secondary hover:text-foreground"
                                  )}
                                >
                                  {asset.label}
                                </button>
                              );
                            })}
                          </div>
                        )}
                      </Field>

                      <Field className="gap-1.5">
                        <FieldLabel htmlFor="site-banner-url" className="text-xs font-medium">
                          Ảnh bìa mặc định
                        </FieldLabel>
                        <Input
                          id="site-banner-url"
                          type="text"
                          value={config.theme.banner_url ?? ""}
                          maxLength={500}
                          onChange={(e) => updateTheme({ banner_url: e.target.value })}
                          placeholder="/banners/campaign-everyday.jpg"
                          aria-invalid={Boolean(bannerUrlError)}
                          className="h-8 bg-card text-[13px]"
                        />
                        {bannerUrlError ? (
                          <FieldError className="text-xs">{bannerUrlError}</FieldError>
                        ) : (
                          <div className="flex flex-wrap gap-1.5 pt-0.5">
                            {QUICK_MEDIA_ASSETS.map((asset) => {
                              const active = config.theme.banner_url === asset.url;
                              return (
                                <button
                                  key={`banner-${asset.url}`}
                                  type="button"
                                  onClick={() => updateTheme({ banner_url: asset.url })}
                                  className={cn(
                                    "inline-flex min-h-7 items-center rounded-sm border px-2 py-0.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                                    active
                                      ? "border-brand/40 bg-badge-info-bg text-badge-info-ink"
                                      : "border-border bg-muted/60 text-muted-foreground hover:bg-secondary hover:text-foreground"
                                  )}
                                >
                                  {asset.label}
                                </button>
                              );
                            })}
                          </div>
                        )}
                      </Field>
                    </AccordionContent>
                  </AccordionItem>

                  {/* Group 3: Global Chrome, Announcement & Storefront Slots */}
                  <AccordionItem value="chrome-slots">
                    <AccordionTrigger className="py-3.5 font-semibold text-foreground hover:no-underline">
                      Khung, Thông báo &amp; Tiện ích
                    </AccordionTrigger>
                    <AccordionContent className="space-y-3">
                      <div className="space-y-2.5">
                        <Field className="gap-1">
                          <FieldLabel htmlFor="layout-archetype" className="text-xs font-medium">
                            Phong cách
                          </FieldLabel>
                          <Select
                            value={config.layout.archetype}
                            onValueChange={(v) => updateLayout({ archetype: v as SiteArchetype })}
                          >
                            <SelectTrigger
                              id="layout-archetype"
                              size="sm"
                              className="w-full min-w-0 bg-card"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {ARCHETYPE_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>

                        <Field className="gap-1">
                          <FieldLabel htmlFor="layout-header" className="text-xs font-medium">
                            Đầu trang
                          </FieldLabel>
                          <Select
                            value={config.layout.header_style}
                            onValueChange={(v) =>
                              updateLayout({ header_style: v as SiteHeaderStyle })
                            }
                          >
                            <SelectTrigger
                              id="layout-header"
                              size="sm"
                              className="w-full min-w-0 bg-card"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {HEADER_STYLE_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>

                        <Field className="gap-1">
                          <FieldLabel htmlFor="layout-footer" className="text-xs font-medium">
                            Chân trang
                          </FieldLabel>
                          <Select
                            value={config.layout.footer_style}
                            onValueChange={(v) =>
                              updateLayout({ footer_style: v as SiteFooterStyle })
                            }
                          >
                            <SelectTrigger
                              id="layout-footer"
                              size="sm"
                              className="w-full min-w-0 bg-card"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {FOOTER_STYLE_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>

                        <Field className="gap-1">
                          <FieldLabel htmlFor="layout-width" className="text-xs font-medium">
                            Độ rộng khung
                          </FieldLabel>
                          <Select
                            value={config.layout.container_width}
                            onValueChange={(v) =>
                              updateLayout({ container_width: v as SiteContainerWidth })
                            }
                          >
                            <SelectTrigger
                              id="layout-width"
                              size="sm"
                              className="w-full min-w-0 bg-card"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {CONTAINER_WIDTH_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>
                      </div>

                      <Field className="gap-1">
                        <FieldLabel htmlFor="layout-card" className="text-xs font-medium">
                          Kiểu thẻ sản phẩm
                        </FieldLabel>
                        <Select
                          value={config.layout.card_style}
                          onValueChange={(v) => updateLayout({ card_style: v as SiteCardStyle })}
                        >
                          <SelectTrigger id="layout-card" size="sm" className="w-full bg-card">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {CARD_STYLE_OPTIONS.map((opt) => (
                              <SelectItem key={opt.value} value={opt.value}>
                                {opt.label}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>

                      <div className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3">
                        <label className="flex cursor-pointer items-center justify-between gap-2 text-xs font-medium text-foreground">
                          <span>Hiển thị thanh thông báo đầu trang</span>
                          <Switch
                            size="sm"
                            checked={config.layout.show_announcement}
                            onCheckedChange={(checked) =>
                              updateLayout({ show_announcement: checked })
                            }
                          />
                        </label>

                        {config.layout.show_announcement && (
                          <Field className="gap-1">
                            <div className="flex items-center justify-between gap-2">
                              <FieldLabel
                                htmlFor="layout-announce-text"
                                className="text-xs font-medium"
                              >
                                Nội dung thông báo
                              </FieldLabel>
                              <span className="text-[11px] text-muted-foreground tabular-nums">
                                {(config.layout.announcement_text ?? "").length}/140
                              </span>
                            </div>
                            <Input
                              id="layout-announce-text"
                              type="text"
                              value={config.layout.announcement_text ?? ""}
                              maxLength={140}
                              onChange={(e) => updateLayout({ announcement_text: e.target.value })}
                              placeholder="Giao toàn quốc 1-3 ngày, đổi size 45 ngày"
                              className="h-8 bg-card text-[13px]"
                            />
                            {announceUnsafe ? (
                              <FieldError className="text-xs">
                                Bị chặn vì {announceUnsafe}.
                              </FieldError>
                            ) : null}
                          </Field>
                        )}
                      </div>

                      <div className="space-y-2">
                        <label className="flex cursor-pointer items-start justify-between gap-3 rounded-lg border border-border bg-muted/35 p-3">
                          <span className="min-w-0">
                            <strong className="block text-xs font-semibold text-foreground">
                              Tìm kiếm nhanh
                            </strong>
                            <small className="text-xs text-muted-foreground">
                              Thanh tìm sản phẩm trên đầu trang
                            </small>
                          </span>
                          <Switch
                            size="sm"
                            checked={config.layout.slots.search}
                            onCheckedChange={(checked) => updateSlots({ search: checked })}
                          />
                        </label>

                        <label className="flex cursor-pointer items-start justify-between gap-3 rounded-lg border border-border bg-muted/35 p-3">
                          <span className="min-w-0">
                            <strong className="block text-xs font-semibold text-foreground">
                              Khung chat gian hàng
                            </strong>
                            <small className="text-xs text-muted-foreground">
                              Người mua nhắn trực tiếp với cửa hàng
                            </small>
                          </span>
                          <Switch
                            size="sm"
                            checked={config.layout.slots.chat}
                            onCheckedChange={(checked) => updateSlots({ chat: checked })}
                          />
                        </label>

                        <label className="flex cursor-pointer items-start justify-between gap-3 rounded-lg border border-border bg-muted/35 p-3">
                          <span className="min-w-0">
                            <strong className="block text-xs font-semibold text-foreground">
                              Trợ lý AI tư vấn
                            </strong>
                            <small className="text-xs text-muted-foreground">
                              Hỗ trợ chọn cỡ và chính sách
                            </small>
                          </span>
                          <Switch
                            size="sm"
                            checked={config.layout.slots.assistant}
                            onCheckedChange={(checked) => updateSlots({ assistant: checked })}
                          />
                        </label>
                      </div>
                    </AccordionContent>
                  </AccordionItem>

                  {/* Group 4: 4 Sub-Page Layouts */}
                  <AccordionItem value="subpages">
                    <AccordionTrigger className="py-3.5 font-semibold text-foreground hover:no-underline">
                      Bố cục 4 trang phụ
                    </AccordionTrigger>
                    <AccordionContent className="space-y-3">
                      {/* Catalog */}
                      <div className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3">
                        <h3 className="text-xs font-semibold text-foreground">
                          Danh sách (/san-pham)
                        </h3>
                        <div className="space-y-2.5">
                          <Field className="gap-1">
                            <FieldLabel htmlFor="pg-cat-layout" className="text-xs font-medium">
                              Bố cục
                            </FieldLabel>
                            <Select
                              value={config.pages.catalog.layout}
                              onValueChange={(v) =>
                                updateCatalogPage({ layout: v as SiteCatalogLayout })
                              }
                            >
                              <SelectTrigger
                                id="pg-cat-layout"
                                size="sm"
                                className="w-full min-w-0 bg-card"
                              >
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {CATALOG_LAYOUT_OPTIONS.map((opt) => (
                                  <SelectItem key={opt.value} value={opt.value}>
                                    {opt.label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </Field>
                          <Field className="gap-1">
                            <FieldLabel htmlFor="pg-cat-cols" className="text-xs font-medium">
                              Số cột (1–5)
                            </FieldLabel>
                            <Input
                              id="pg-cat-cols"
                              type="number"
                              min={1}
                              max={5}
                              value={config.pages.catalog.columns}
                              onChange={(e) =>
                                updateCatalogPage({
                                  columns: Math.max(
                                    1,
                                    Math.min(5, Math.trunc(Number(e.target.value) || 3))
                                  ),
                                })
                              }
                              className="h-8 bg-card text-[13px] tabular-nums"
                            />
                          </Field>
                        </div>

                        <Field className="gap-1">
                          <FieldLabel htmlFor="pg-cat-title" className="text-xs font-medium">
                            Tiêu đề đầu trang
                          </FieldLabel>
                          <Input
                            id="pg-cat-title"
                            type="text"
                            maxLength={120}
                            value={config.pages.catalog.intro_title}
                            onChange={(e) => updateCatalogPage({ intro_title: e.target.value })}
                            className="h-8 bg-card text-[13px]"
                          />
                        </Field>

                        <Field className="gap-1">
                          <FieldLabel htmlFor="pg-cat-sub" className="text-xs font-medium">
                            Mô tả ngắn
                          </FieldLabel>
                          <Input
                            id="pg-cat-sub"
                            type="text"
                            maxLength={300}
                            value={config.pages.catalog.intro_subtitle}
                            onChange={(e) => updateCatalogPage({ intro_subtitle: e.target.value })}
                            className="h-8 bg-card text-[13px]"
                          />
                        </Field>

                        <div className="space-y-2 pt-0.5">
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.catalog.show_filters}
                              onCheckedChange={(checked) =>
                                updateCatalogPage({ show_filters: checked === true })
                              }
                            />
                            <span>Hiện bộ lọc danh mục</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.catalog.show_search}
                              onCheckedChange={(checked) =>
                                updateCatalogPage({ show_search: checked === true })
                              }
                            />
                            <span>Hiện ô tìm kiếm</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.catalog.show_sku_matrix}
                              onCheckedChange={(checked) =>
                                updateCatalogPage({ show_sku_matrix: checked === true })
                              }
                            />
                            <span>Chế độ bảng ma trận SKU</span>
                          </label>
                        </div>
                      </div>

                      {/* PDP */}
                      <div className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3">
                        <h3 className="text-xs font-semibold text-foreground">
                          Chi tiết sản phẩm (PDP)
                        </h3>
                        <div className="space-y-2.5">
                          <Field className="gap-1">
                            <FieldLabel htmlFor="pg-pdp-layout" className="text-xs font-medium">
                              Bố cục PDP
                            </FieldLabel>
                            <Select
                              value={config.pages.pdp.layout}
                              onValueChange={(v) => updatePdpPage({ layout: v as SitePdpLayout })}
                            >
                              <SelectTrigger
                                id="pg-pdp-layout"
                                size="sm"
                                className="w-full min-w-0 bg-card"
                              >
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {PDP_LAYOUT_OPTIONS.map((opt) => (
                                  <SelectItem key={opt.value} value={opt.value}>
                                    {opt.label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </Field>
                          <Field className="gap-1">
                            <FieldLabel htmlFor="pg-pdp-gal" className="text-xs font-medium">
                              Bộ ảnh SP
                            </FieldLabel>
                            <Select
                              value={config.pages.pdp.gallery_style}
                              onValueChange={(v) =>
                                updatePdpPage({ gallery_style: v as SitePdpGalleryStyle })
                              }
                            >
                              <SelectTrigger
                                id="pg-pdp-gal"
                                size="sm"
                                className="w-full min-w-0 bg-card"
                              >
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {PDP_GALLERY_OPTIONS.map((opt) => (
                                  <SelectItem key={opt.value} value={opt.value}>
                                    {opt.label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </Field>
                        </div>

                        <div className="space-y-2 pt-0.5">
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.pdp.show_vouchers}
                              onCheckedChange={(checked) =>
                                updatePdpPage({ show_vouchers: checked === true })
                              }
                            />
                            <span>Hiện mã giảm giá</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.pdp.show_sku_table}
                              onCheckedChange={(checked) =>
                                updatePdpPage({ show_sku_table: checked === true })
                              }
                            />
                            <span>Hiện bảng tồn kho SKU</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.pdp.show_story_band}
                              onCheckedChange={(checked) =>
                                updatePdpPage({ show_story_band: checked === true })
                              }
                            />
                            <span>Hiện dải chất liệu &amp; xưởng</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.pdp.show_related}
                              onCheckedChange={(checked) =>
                                updatePdpPage({ show_related: checked === true })
                              }
                            />
                            <span>Hiện sản phẩm liên quan</span>
                          </label>
                        </div>
                      </div>

                      {/* Cart */}
                      <div className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3">
                        <h3 className="text-xs font-semibold text-foreground">
                          Giỏ hàng (/gio-hang)
                        </h3>
                        <Field className="gap-1">
                          <FieldLabel htmlFor="pg-cart-layout" className="text-xs font-medium">
                            Bố cục giỏ hàng
                          </FieldLabel>
                          <Select
                            value={config.pages.cart.layout}
                            onValueChange={(v) => updateCartPage({ layout: v as SiteCartLayout })}
                          >
                            <SelectTrigger id="pg-cart-layout" size="sm" className="w-full bg-card">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {CART_LAYOUT_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>
                        <Field className="gap-1">
                          <FieldLabel htmlFor="pg-cart-trust" className="text-xs font-medium">
                            Cam kết dưới nút thanh toán
                          </FieldLabel>
                          <Textarea
                            id="pg-cart-trust"
                            rows={2}
                            maxLength={240}
                            value={config.pages.cart.trust_note}
                            onChange={(e) => updateCartPage({ trust_note: e.target.value })}
                            className="bg-card text-[13px]"
                          />
                        </Field>
                        <div className="space-y-2 pt-0.5">
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.cart.show_vouchers}
                              onCheckedChange={(checked) =>
                                updateCartPage({ show_vouchers: checked === true })
                              }
                            />
                            <span>Hiện khung áp dụng voucher</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.cart.show_addons}
                              onCheckedChange={(checked) =>
                                updateCartPage({ show_addons: checked === true })
                              }
                            />
                            <span>Gợi ý mua kèm</span>
                          </label>
                        </div>
                      </div>

                      {/* Info */}
                      <div className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3">
                        <h3 className="text-xs font-semibold text-foreground">
                          Thông tin (/gioi-thieu)
                        </h3>
                        <Field className="gap-1">
                          <FieldLabel htmlFor="pg-info-layout" className="text-xs font-medium">
                            Bố cục trang thông tin
                          </FieldLabel>
                          <Select
                            value={config.pages.info.layout}
                            onValueChange={(v) => updateInfoPage({ layout: v as SiteInfoLayout })}
                          >
                            <SelectTrigger id="pg-info-layout" size="sm" className="w-full bg-card">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {INFO_LAYOUT_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>
                        <div className="space-y-2 pt-0.5">
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.info.show_factory_profile}
                              onCheckedChange={(checked) =>
                                updateInfoPage({ show_factory_profile: checked === true })
                              }
                            />
                            <span>Hồ sơ xưởng sản xuất</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.info.show_toc}
                              onCheckedChange={(checked) =>
                                updateInfoPage({ show_toc: checked === true })
                              }
                            />
                            <span>Mục lục điều hướng</span>
                          </label>
                          <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                            <Checkbox
                              checked={config.pages.info.show_showcase}
                              onCheckedChange={(checked) =>
                                updateInfoPage({ show_showcase: checked === true })
                              }
                            />
                            <span>Sản phẩm tiêu biểu cuối trang</span>
                          </label>
                        </div>
                      </div>
                    </AccordionContent>
                  </AccordionItem>

                  {/* Group 5: Sections & Blocks across 5 Pages */}
                  <AccordionItem value="sections">
                    <AccordionTrigger className="items-center py-3.5 hover:no-underline">
                      <span className="flex items-center gap-2 font-semibold text-foreground">
                        <span>Khối &amp; Block (5 trang)</span>
                        <Badge variant="neutral" className="tabular-nums">
                          {totalSectionsCount}/{MAX_TOTAL_SECTIONS}
                        </Badge>
                      </span>
                    </AccordionTrigger>
                    <AccordionContent className="space-y-3">
                      <div
                        className="flex flex-wrap items-center gap-1.5"
                        role="tablist"
                        aria-label="Chọn trang cần chỉnh khối"
                      >
                        {TEMPLATE_PAGE_TABS.map((tab) => {
                          const count = getPageSections(config, tab.key).length;
                          const active = sectionPageKey === tab.key;
                          return (
                            <button
                              key={tab.key}
                              type="button"
                              role="tab"
                              aria-selected={active}
                              onClick={() => {
                                setSectionPageKey(tab.key);
                                setPreviewPage(tab.key);
                                const nextSecs = getPageSections(config, tab.key);
                                setSelectedSectionId(nextSecs[0]?.id ?? null);
                              }}
                              className={cn(
                                "inline-flex min-h-8 items-center gap-1.5 rounded-md border px-2.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                                active
                                  ? "border-brand/40 bg-badge-info-bg font-semibold text-badge-info-ink"
                                  : "border-border bg-muted/40 text-muted-foreground hover:bg-secondary hover:text-foreground"
                              )}
                            >
                              <span>{tab.shortLabel}</span>
                              <span className="tabular-nums opacity-80">{count}</span>
                            </button>
                          );
                        })}
                      </div>

                      <div className="flex items-center gap-2">
                        <Select
                          value={newSectionType}
                          onValueChange={(v) => setNewSectionType(v as SiteSectionV2Type)}
                        >
                          <SelectTrigger
                            size="sm"
                            className="min-w-0 flex-1 bg-card"
                            aria-label="Chọn loại khối nội dung muốn thêm"
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {SECTION_V2_TYPE_ORDER.map((t) => (
                              <SelectItem key={t} value={t}>
                                {SECTION_V2_TYPE_META[t].label}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={
                            activePageSections.length >= activePageMeta.maxSections ||
                            totalSectionsCount >= MAX_TOTAL_SECTIONS
                          }
                          onClick={handleAddSection}
                        >
                          <Plus className="size-3.5" aria-hidden="true" />
                          <span>Thêm</span>
                        </Button>
                      </div>

                      {activePageSections.length === 0 ? (
                        <div className="rounded-lg border border-dashed border-border bg-muted/35 p-3 text-xs text-muted-foreground">
                          Chưa có khối nội dung cho {activePageMeta.label}.
                        </div>
                      ) : (
                        <div className="space-y-2">
                          {activePageSections.map((sec, idx) => {
                            const isSelected = selectedSection?.id === sec.id;
                            const blockCount = sec.blocks?.length ?? 0;
                            const variantLabel = sec.variant
                              ? (SECTION_VARIANT_LABELS[sec.variant] ?? sec.variant)
                              : "";
                            return (
                              <div
                                key={sec.id}
                                className={cn(
                                  "space-y-2 rounded-lg border p-2.5 transition-colors",
                                  isSelected
                                    ? "border-brand bg-badge-info-bg/35"
                                    : "border-border bg-muted/35 hover:border-input"
                                )}
                              >
                                <button
                                  type="button"
                                  onClick={() => setSelectedSectionId(sec.id)}
                                  className="block w-full min-w-0 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                                >
                                  <span className="block truncate text-[13px] font-semibold text-foreground">
                                    {getSectionHeadingPreview(sec)}
                                  </span>
                                  <span className="block truncate text-xs text-muted-foreground">
                                    {SECTION_V2_TYPE_META[sec.type].label}
                                    {variantLabel ? ` · ${variantLabel}` : ""}
                                    {blockCount > 0 ? ` · ${blockCount} block` : ""} ·{" "}
                                    {sec.visible ? "Hiện" : "Ẩn"}
                                  </span>
                                </button>

                                <div className="flex items-center justify-between gap-2">
                                  <div className="flex items-center gap-1">
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="icon-xs"
                                      disabled={idx === 0}
                                      onClick={() => handleMoveSection(idx, -1)}
                                      aria-label="Di chuyển khối lên trên"
                                    >
                                      <ArrowUp className="size-3" aria-hidden="true" />
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="icon-xs"
                                      disabled={idx === activePageSections.length - 1}
                                      onClick={() => handleMoveSection(idx, 1)}
                                      aria-label="Di chuyển khối xuống dưới"
                                    >
                                      <ArrowDown className="size-3" aria-hidden="true" />
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      onClick={() =>
                                        updateActivePageSections((prev) =>
                                          prev.map((item) =>
                                            item.id === sec.id
                                              ? { ...item, visible: !item.visible }
                                              : item
                                          )
                                        )
                                      }
                                    >
                                      {sec.visible ? (
                                        <EyeOff className="size-3" aria-hidden="true" />
                                      ) : (
                                        <Eye className="size-3" aria-hidden="true" />
                                      )}
                                      <span>{sec.visible ? "Ẩn" : "Hiện"}</span>
                                    </Button>
                                  </div>

                                  <Button
                                    type="button"
                                    variant="ghost"
                                    size="xs"
                                    className="text-destructive hover:text-destructive"
                                    onClick={() => handleDeleteSection(sec.id)}
                                  >
                                    <Trash2 className="size-3" aria-hidden="true" />
                                    <span>Xóa</span>
                                  </Button>
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      )}

                      <SectionEditorPanel
                        section={selectedSection}
                        products={products}
                        categories={categories}
                        onChange={(updated) =>
                          updateActivePageSections((prev) =>
                            prev.map((item) => (item.id === updated.id ? updated : item))
                          )
                        }
                      />
                    </AccordionContent>
                  </AccordionItem>
                </Accordion>
              </CardContent>
            </Card>

            {/* Right Sticky Live Preview */}
            <Card
              aria-label="Xem trước trực tiếp site"
              className="gap-0 overflow-hidden py-0 lg:sticky lg:top-[72px]"
            >
              <PreviewToolbar
                activePage={previewPage}
                onPageChange={(nextPage) => {
                  setPreviewPage(nextPage);
                  setSectionPageKey(nextPage);
                  const nextSecs = getPageSections(config, nextPage);
                  setSelectedSectionId(nextSecs[0]?.id ?? null);
                }}
                device={previewDevice}
                onDeviceChange={setPreviewDevice}
              />

              <div className="overflow-hidden bg-muted/60 p-3 sm:p-4">
                <SitePreviewFrame
                  device={previewDevice}
                  title={`Xem trước trực tiếp site (${previewDevice}px)`}
                  hostLabel={activeHostDisplay}
                  showChromeBar={true}
                  resetScrollKey={`${config.template_id}:${previewPage}:${previewDevice}`}
                  viewportHeight={600}
                >
                  <SitePreviewContent
                    config={config}
                    page={previewPage}
                    shop={previewDataset.shop}
                    products={previewDataset.products}
                    categories={previewDataset.categories}
                    vouchers={previewDataset.vouchers}
                    platformOrigin={platformOrigin}
                  />
                </SitePreviewFrame>
              </div>
            </Card>
          </div>
        </TabsContent>

        {/* MODE 3: Mã JSON */}
        <TabsContent value="code">
          <TemplateCodeEditorTab
            config={config}
            templates={templates}
            previewDataset={previewDataset}
            platformOrigin={platformOrigin}
            onValidConfigChange={(next) => {
              setConfig(next);
              setDirty(true);
              setFormError(null);
            }}
            onNotify={(msg) => showToast(msg)}
          />
        </TabsContent>

        {/* MODE 4: Tên miền (A5 + subdomain moved here) */}
        <TabsContent value="domains">
          <SellerSiteDomainsSection
            site={site}
            initialDomains={domains}
            rootDomain={rootDomain}
            requestHost={requestHost}
            requestProto={requestProto}
            loadError={domainsLoadError}
            onDomainsChange={handleDomainsChange}
            onSwitchToTheme={() => setActiveTab("customize")}
            subdomainEditor={{
              value: subdomain,
              cleanSub,
              error: subdomainError,
              onChange: (nextVal) => {
                setSubdomain(nextVal);
                setDirty(true);
                setFormError(null);
              },
              dirty,
              saving: savingDraft,
              onSaveDraft: () => void handleSaveDraftClick(),
            }}
          />
        </TabsContent>

        {/* MODE 5: Phiên bản (A1 + AlertDialog rollback confirmation) */}
        <TabsContent value="versions">
          {versions.length === 0 ? (
            <EmptyState
              icon={History}
              title="Chưa có phiên bản xuất bản"
              description='Lưu nháp và bấm "Xuất bản" ở thanh trên để tạo phiên bản v1 cho cửa hàng.'
              action={
                site ? (
                  <Button variant="outline" size="sm" asChild>
                    <Link href="/seller/site/xem-truoc">Xem trước bản nháp</Link>
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <IndexTable
              toolbar={
                site ? (
                  <Button variant="outline" size="sm" asChild>
                    <Link href="/seller/site/xem-truoc">Xem trước bản nháp</Link>
                  </Button>
                ) : undefined
              }
              footer={
                <>
                  <span>Tổng {versions.length} phiên bản đã xuất bản</span>
                  <span>Bấm Khôi phục để hoàn nguyên về một phiên bản cũ</span>
                </>
              }
            >
              {/* Desktop table */}
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>Phiên bản</TableHead>
                      <TableHead>Mẫu</TableHead>
                      <TableHead>Thời gian</TableHead>
                      <TableHead>Ghi chú</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead className="text-right">Thao tác</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {versions.map((ver) => {
                      const isActive = site?.published_version_id === ver.id;
                      const isRolling = rollingBackId === ver.id;
                      const tplId = ver.config?.template_id || "legacy-v1";
                      const tplLabel = templates.find((t) => t.id === tplId)?.name || tplId;
                      return (
                        <TableRow key={ver.id}>
                          <TableCell className="font-semibold text-foreground tabular-nums">
                            v{ver.version_number}
                          </TableCell>
                          <TableCell>
                            <Badge variant="neutral" className="rounded-sm">
                              {tplLabel}
                            </Badge>
                          </TableCell>
                          <TableCell className="text-[13px] text-muted-foreground tabular-nums">
                            {ver.created_at ? formatDateTimeVi(ver.created_at) : "Chưa rõ"}
                          </TableCell>
                          <TableCell className="max-w-[280px] text-[13px] whitespace-normal text-foreground">
                            {ver.note || "Xuất bản cập nhật"}
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={isActive ? "success" : "neutral"}>
                              {isActive ? "Đang hiển thị" : "Lưu trữ"}
                            </StatusBadge>
                          </TableCell>
                          <TableCell className="text-right">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              disabled={isSuspended || isRolling || Boolean(rollingBackId)}
                              onClick={() => {
                                setRollbackNote("");
                                setRollbackTarget(ver);
                              }}
                            >
                              <RotateCcw className="size-3.5" aria-hidden="true" />
                              <span>{isRolling ? "Đang khôi phục..." : "Khôi phục"}</span>
                            </Button>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              {/* Mobile row cards (< 768px) */}
              <div className="md:hidden">
                {versions.map((ver) => {
                  const isActive = site?.published_version_id === ver.id;
                  const isRolling = rollingBackId === ver.id;
                  const tplId = ver.config?.template_id || "legacy-v1";
                  const tplLabel = templates.find((t) => t.id === tplId)?.name || tplId;
                  return (
                    <IndexRowCard
                      key={ver.id}
                      title={`Phiên bản v${ver.version_number}`}
                      badge={
                        <StatusBadge tone={isActive ? "success" : "neutral"}>
                          {isActive ? "Đang hiển thị" : "Lưu trữ"}
                        </StatusBadge>
                      }
                      meta={
                        <div className="flex w-full flex-col gap-2 pt-0.5">
                          <span>
                            {tplLabel}
                            {ver.created_at ? ` · ${formatDateTimeVi(ver.created_at)}` : ""}
                          </span>
                          <span className="text-foreground">{ver.note || "Xuất bản cập nhật"}</span>
                          <div>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              disabled={isSuspended || isRolling || Boolean(rollingBackId)}
                              onClick={() => {
                                setRollbackNote("");
                                setRollbackTarget(ver);
                              }}
                              className="min-h-10"
                            >
                              <RotateCcw className="size-3.5" aria-hidden="true" />
                              <span>{isRolling ? "Đang khôi phục..." : "Khôi phục"}</span>
                            </Button>
                          </div>
                        </div>
                      }
                    />
                  );
                })}
              </div>
            </IndexTable>
          )}
        </TabsContent>
      </Tabs>

      {/* Publish Confirmation Dialog */}
      <Dialog
        open={publishOpen && !isSuspended}
        onOpenChange={(open) => {
          if (!publishing && !savingDraft) setPublishOpen(open);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Xuất bản site bán hàng</DialogTitle>
            <DialogDescription>
              Bản nháp hiện tại sẽ được xuất bản công khai lên <strong>{activeHostDisplay}</strong>.
            </DialogDescription>
          </DialogHeader>

          <Field className="gap-1.5">
            <FieldLabel htmlFor="publish-note-input">
              Ghi chú phiên bản xuất bản (tùy chọn)
            </FieldLabel>
            <Input
              id="publish-note-input"
              type="text"
              value={publishNote}
              maxLength={300}
              onChange={(e) => setPublishNote(e.target.value)}
              placeholder="Ví dụ: Cập nhật mẫu và bảng màu mới"
            />
            <FieldDescription className="text-xs">
              Ghi chú sẽ lưu trong tab Phiên bản để dễ đối chiếu và khôi phục sau này.
            </FieldDescription>
          </Field>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={publishing || savingDraft}
              onClick={() => setPublishOpen(false)}
            >
              Đóng
            </Button>
            <Button
              type="button"
              disabled={publishing || savingDraft}
              onClick={() => void handleConfirmPublish()}
            >
              {publishing
                ? "Đang xuất bản..."
                : dirty
                  ? "Lưu nháp & Xuất bản"
                  : "Xác nhận xuất bản"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Rollback Confirmation AlertDialog */}
      <AlertDialog
        open={rollbackTarget !== null}
        onOpenChange={(open) => {
          if (!open && !rollingBackId) setRollbackTarget(null);
        }}
      >
        <AlertDialogContent>
          {rollbackTarget ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  Khôi phục về phiên bản v{rollbackTarget.version_number}?
                </AlertDialogTitle>
                <AlertDialogDescription>
                  Cửa hàng sẽ khôi phục về cấu hình phiên bản{" "}
                  <strong>v{rollbackTarget.version_number}</strong> (mẫu{" "}
                  <strong>{rollbackTargetTemplateLabel}</strong>). Thay đổi sẽ cập nhật cả bản nháp
                  và bản đang hiển thị.
                </AlertDialogDescription>
              </AlertDialogHeader>

              <Field className="gap-1.5">
                <FieldLabel htmlFor="rollback-note">Ghi chú khi khôi phục (tùy chọn)</FieldLabel>
                <Input
                  id="rollback-note"
                  type="text"
                  value={rollbackNote}
                  maxLength={300}
                  onChange={(e) => setRollbackNote(e.target.value)}
                  placeholder="Ví dụ: Quay lại bố cục trước chiến dịch cuối tuần"
                />
              </Field>

              <AlertDialogFooter>
                <AlertDialogCancel disabled={Boolean(rollingBackId)}>Hủy</AlertDialogCancel>
                <AlertDialogAction
                  disabled={Boolean(rollingBackId)}
                  onClick={(e) => {
                    e.preventDefault();
                    void handleRollback(rollbackTarget);
                  }}
                >
                  {rollingBackId === rollbackTarget.id ? "Đang khôi phục..." : "Xác nhận khôi phục"}
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : null}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
