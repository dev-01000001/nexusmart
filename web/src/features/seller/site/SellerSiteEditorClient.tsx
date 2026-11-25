"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Button, IconArrowUpRight, IconChevronDown, IconPlus, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import type { SiteDomainDTO } from "@/lib/site/domains";
import { PLATFORM_TEMPLATES, cloneTemplateConfigForShop } from "@/features/site/templateCatalog";
import { useSellerContext } from "@/features/seller/SellerShell";
import { SellerSlidingTabs } from "@/features/seller/SellerUiPrimitives";
import type { GwCategory, GwSellerProduct, GwSellerShop } from "@/features/seller/sellerApiTypes";
import common from "@/features/seller/SellerCommon.module.css";
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
import { SectionEditorPanel } from "./SectionEditorPanel";
import { SellerSiteDomainsSection } from "./SellerSiteDomainsSection";
import { SitePreviewContent } from "./SitePreviewContent";
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";
import { TemplateGalleryTab } from "./TemplateGalleryTab";
import { TemplateCodeEditorTab } from "./TemplateCodeEditorTab";
import { buildPublicSiteUrl } from "./siteSummary";
import domainStyles from "./SellerSiteDomains.module.css";
import styles from "./SellerSiteEditor.module.css";

export type EditorTab = "templates" | "theme" | "sections" | "code" | "domains" | "versions";

export interface SellerSiteEditorClientProps {
  initialSite: GwSite | null;
  initialShop: GwSellerShop | null;
  initialVersions: GwSiteVersion[];
  initialDomains?: SiteDomainDTO[];
  initialTemplates?: PlatformTemplateDTO[];
  domainsLoadError?: string | null;
  initialTab?: EditorTab;
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
  products,
  categories,
  rootDomain,
  requestHost,
  requestProto,
  loadError,
}: SellerSiteEditorClientProps) {
  const router = useRouter();
  const { shop: shellShop, showToast } = useSellerContext();
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

  // Shared live preview state for Theme & Sections studio split
  const [previewPage, setPreviewPage] = useState<TemplatePageKey>("home");
  const [previewDevice, setPreviewDevice] = useState<DevicePreviewWidth>("1440");

  const [dirty, setDirty] = useState<boolean>(!initialSite);
  const [formError, setFormError] = useState<string | null>(null);
  const [savingDraft, setSavingDraft] = useState<boolean>(false);
  const [applyingTemplateId, setApplyingTemplateId] = useState<string | null>(null);
  const [publishing, setPublishing] = useState<boolean>(false);
  const [publishOpen, setPublishOpen] = useState<boolean>(false);
  const [publishNote, setPublishNote] = useState<string>("");
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
      setActiveTab("theme");
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
      <div className={common.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải cấu hình site riêng"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const pillToneClass =
    statusMeta.tone === "pos"
      ? common.pillPos
      : statusMeta.tone === "warn"
        ? common.pillWarn
        : statusMeta.tone === "neg"
          ? common.pillNeg
          : "";

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

  const studioTabs: ReadonlyArray<{ key: EditorTab; label: string; count?: number }> = [
    { key: "templates", label: "Thư viện mẫu", count: templates.length },
    { key: "theme", label: "Giao diện & Bố cục" },
    { key: "sections", label: "Khối & Block", count: totalSectionsCount },
    { key: "code", label: "Mã template" },
    { key: "domains", label: "Tên miền" },
    { key: "versions", label: "Phiên bản", count: versions.length },
  ];

  const renderLivePreviewPane = () => (
    <aside className={styles.previewPaneCol} aria-label="Xem trước trực tiếp site">
      <div className={styles.previewPaneSticky}>
        <div className={styles.previewPaneBar}>
          <div
            className={styles.pageSwitcherGroup}
            role="tablist"
            aria-label="Chọn trang xem trước"
          >
            {TEMPLATE_PAGE_TABS.map((tab) => (
              <button
                key={tab.key}
                type="button"
                role="tab"
                aria-selected={previewPage === tab.key}
                className={`${styles.pageSwitchBtn} ${
                  previewPage === tab.key ? styles.pageSwitchBtnActive : ""
                }`}
                onClick={() => {
                  setPreviewPage(tab.key);
                  if (activeTab === "sections") {
                    setSectionPageKey(tab.key);
                    const nextSecs = getPageSections(config, tab.key);
                    setSelectedSectionId(nextSecs[0]?.id ?? null);
                  }
                }}
              >
                {tab.shortLabel}
              </button>
            ))}
          </div>

          <div
            className={styles.viewportGroup}
            role="group"
            aria-label="Chọn chiều rộng khung xem trước"
          >
            {(["390", "768", "1440"] as const).map((w) => (
              <button
                key={w}
                type="button"
                aria-pressed={previewDevice === w}
                className={`${styles.viewportBtn} ${
                  previewDevice === w ? styles.viewportBtnActive : ""
                }`}
                onClick={() => setPreviewDevice(w)}
              >
                {w}
              </button>
            ))}
          </div>
        </div>

        <div className={styles.previewStage}>
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
      </div>
    </aside>
  );

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Site bán hàng</h1>
        </div>
        <div className={common.headerActions}>
          {site ? (
            <Button variant="outline" size="sm" shape="app" href="/seller/site/xem-truoc">
              Xem trước
            </Button>
          ) : (
            <Button variant="outline" size="sm" shape="app" disabled>
              Xem trước
            </Button>
          )}
          <Button
            variant="secondary"
            size="sm"
            shape="app"
            disabled={savingDraft || publishing || Boolean(applyingTemplateId)}
            onClick={() => void handleSaveDraftClick()}
          >
            {savingDraft ? "Đang lưu..." : "Lưu nháp"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            shape="app"
            disabled={isSuspended || savingDraft || publishing || Boolean(applyingTemplateId)}
            onClick={() => setPublishOpen((prev) => !prev)}
          >
            {publishing ? "Đang xuất bản..." : "Xuất bản"}
          </Button>
        </div>
      </header>

      {/* Compact single-row Draft / Published status bar */}
      <section className={styles.studioStatusBar} aria-label="Trạng thái xuất bản site">
        <div className={styles.studioStatusLeft}>
          <span className={`${common.pill} ${pillToneClass}`}>
            {statusMeta.label}
            {activePublishedVersionNumber ? ` v${activePublishedVersionNumber}` : ""}
          </span>
          <span className={styles.syncIndicator}>
            <span
              className={`${styles.syncDot} ${dirty ? styles.syncDotDirty : ""}`}
              aria-hidden="true"
            />
            <span>{dirty ? "Bản nháp chưa lưu" : "Đã đồng bộ"}</span>
          </span>
          <span className={styles.statusDivider} aria-hidden="true">
            |
          </span>
          <span className={styles.hostPreviewUrl}>{activeHostDisplay}</span>
          {isPublished && site?.primary_host && (
            <a
              href={buildPublicSiteUrl(site.primary_host, requestHost, requestProto) || "#"}
              target="_blank"
              rel="noopener noreferrer"
              className={styles.publicLink}
            >
              <span>Mở site</span>
              <IconArrowUpRight size={13} />
            </a>
          )}
        </div>

        <div className={styles.studioStatusRight}>
          <span className={styles.studioMetaText}>
            Mẫu: {currentTemplateMeta?.name || config.template_id}
          </span>
          <span className={styles.studioMetaText}>
            {totalSectionsCount}/{MAX_TOTAL_SECTIONS} khối
          </span>
          <button
            type="button"
            className={domainStyles.summaryManageBtn}
            onClick={() => setActiveTab("domains")}
          >
            Tên miền
          </button>
        </div>
      </section>

      {isSuspended && (
        <div className={styles.statusAlert} role="alert">
          <div>
            <strong>Site riêng đang bị tạm khóa bởi quản trị NexusMart.</strong>{" "}
            {site?.suspended_reason ? `Lý do: ${site.suspended_reason}. ` : ""}
            Bạn vẫn có thể chỉnh sửa và lưu bản nháp, nhưng cần quản trị mở khóa trước khi xuất bản
            lại.
          </div>
        </div>
      )}

      {publishOpen && !isSuspended && (
        <div className={styles.publishDrawer} role="region" aria-label="Xác nhận xuất bản site">
          <div className={styles.publishNoteField}>
            <label htmlFor="publish-note-input" className={common.fieldLabel}>
              Ghi chú phiên bản xuất bản (tùy chọn)
            </label>
            <input
              id="publish-note-input"
              type="text"
              className={common.textInput}
              value={publishNote}
              maxLength={300}
              onChange={(e) => setPublishNote(e.target.value)}
              placeholder="Ví dụ: Cập nhật mẫu và bảng màu mới"
            />
          </div>
          <div className={styles.publishButtons}>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={publishing || savingDraft}
              onClick={() => setPublishOpen(false)}
            >
              Đóng
            </Button>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={publishing || savingDraft}
              onClick={() => void handleConfirmPublish()}
            >
              {publishing
                ? "Đang xuất bản..."
                : dirty
                  ? "Lưu nháp & Xuất bản"
                  : "Xác nhận xuất bản"}
            </Button>
          </div>
        </div>
      )}

      {formError && (
        <div className={styles.errorBanner} role="alert">
          {formError}
        </div>
      )}

      <SellerSlidingTabs
        tabs={studioTabs}
        value={activeTab}
        onChange={(nextTab) => {
          setActiveTab(nextTab);
          if (nextTab === "sections") {
            setPreviewPage(sectionPageKey);
          }
        }}
        ariaLabel="Các mục cấu hình site bán hàng"
      />

      {/* TAB 1: Real-Photo Template Gallery */}
      {activeTab === "templates" && (
        <TemplateGalleryTab
          templates={templates}
          currentTemplateId={config.template_id}
          currentConfig={config}
          previewDataset={previewDataset}
          platformOrigin={platformOrigin}
          applyingTemplateId={applyingTemplateId}
          onApplyTemplate={handleApplyTemplate}
        />
      )}

      {/* TAB 2: Studio Split — Left Collapsible Inspector (36%) + Right Live Preview (64%) */}
      {activeTab === "theme" && (
        <div className={styles.studioWorkspace}>
          <div className={styles.inspectorCol}>
            {/* Group 1: Brand & Subdomain (no font selector) */}
            <details className={common.accordion} open>
              <summary className={common.accordionSummary}>
                <span className={styles.inspectorAccordionTitle}>
                  <span>Thương hiệu &amp; Tên miền phụ</span>
                </span>
                <span className={common.accordionChevron} aria-hidden="true">
                  <IconChevronDown size={16} />
                </span>
              </summary>
              <div className={common.accordionBody}>
                <div className={common.field}>
                  <label htmlFor="site-subdomain" className={common.fieldLabel}>
                    Tên miền phụ
                  </label>
                  <input
                    id="site-subdomain"
                    type="text"
                    className={common.textInput}
                    value={subdomain}
                    maxLength={40}
                    onChange={(e) => {
                      setSubdomain(e.target.value.toLowerCase());
                      setDirty(true);
                      setFormError(null);
                    }}
                    placeholder="bach-dang"
                  />
                  {subdomainError ? (
                    <span className={common.fieldError}>{subdomainError}</span>
                  ) : (
                    <div className={styles.hostPreviewRow}>
                      <span className={styles.hostPreviewUrl}>
                        {cleanSub}
                        <span className={styles.hostSuffix}>.{rootDomain}</span>
                      </span>
                      <span className={`${common.pill} ${common.pillPos}`}>Hợp lệ</span>
                    </div>
                  )}
                </div>

                <div className={common.field}>
                  <div className={styles.charRow}>
                    <label htmlFor="site-brand" className={common.fieldLabel}>
                      Tên thương hiệu
                    </label>
                    <span className={styles.charCount}>{config.theme.brand_name.length}/80</span>
                  </div>
                  <input
                    id="site-brand"
                    type="text"
                    className={common.textInput}
                    value={config.theme.brand_name}
                    maxLength={80}
                    onChange={(e) => updateTheme({ brand_name: e.target.value })}
                  />
                  {brandUnsafe && (
                    <span className={common.fieldError}>Bị chặn vì {brandUnsafe}.</span>
                  )}
                </div>

                <div className={styles.fields2}>
                  <div className={common.field}>
                    <label htmlFor="site-radius" className={common.fieldLabel}>
                      Bo góc
                    </label>
                    <select
                      id="site-radius"
                      className={common.textInput}
                      value={config.theme.radius ?? ""}
                      onChange={(e) => updateTheme({ radius: e.target.value as SiteRadius })}
                    >
                      {RADIUS_OPTIONS.map((r) => (
                        <option key={r.value || "default"} value={r.value}>
                          {r.label}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="site-density" className={common.fieldLabel}>
                      Mật độ
                    </label>
                    <select
                      id="site-density"
                      className={common.textInput}
                      value={config.theme.density ?? ""}
                      onChange={(e) => updateTheme({ density: e.target.value as SiteDensity })}
                    >
                      {DENSITY_OPTIONS.map((d) => (
                        <option key={d.value || "default"} value={d.value}>
                          {d.label}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                <div className={common.field}>
                  <div className={styles.charRow}>
                    <label htmlFor="site-tagline" className={common.fieldLabel}>
                      Khẩu hiệu ngắn
                    </label>
                    <span className={styles.charCount}>
                      {(config.theme.tagline ?? "").length}/160
                    </span>
                  </div>
                  <input
                    id="site-tagline"
                    type="text"
                    className={common.textInput}
                    value={config.theme.tagline ?? ""}
                    maxLength={160}
                    onChange={(e) => updateTheme({ tagline: e.target.value })}
                    placeholder="Áo thun cotton 220gsm may trực tiếp tại xưởng"
                  />
                  {taglineUnsafe && (
                    <span className={common.fieldError}>Bị chặn vì {taglineUnsafe}.</span>
                  )}
                </div>

                <div className={common.field}>
                  <label htmlFor="site-logo-url" className={common.fieldLabel}>
                    Ảnh logo
                  </label>
                  <input
                    id="site-logo-url"
                    type="text"
                    className={common.textInput}
                    value={config.theme.logo_url ?? ""}
                    maxLength={500}
                    onChange={(e) => updateTheme({ logo_url: e.target.value })}
                    placeholder="/banners/vietnam-workshop.jpg"
                  />
                  {logoUrlError ? (
                    <span className={common.fieldError}>{logoUrlError}</span>
                  ) : (
                    <div className={styles.quickChips}>
                      {QUICK_MEDIA_ASSETS.slice(0, 3).map((asset) => (
                        <button
                          key={`logo-${asset.url}`}
                          type="button"
                          className={`${styles.quickChip} ${
                            config.theme.logo_url === asset.url ? styles.quickChipActive : ""
                          }`}
                          onClick={() => updateTheme({ logo_url: asset.url })}
                        >
                          {asset.label}
                        </button>
                      ))}
                    </div>
                  )}
                </div>

                <div className={common.field}>
                  <label htmlFor="site-banner-url" className={common.fieldLabel}>
                    Ảnh bìa mặc định
                  </label>
                  <input
                    id="site-banner-url"
                    type="text"
                    className={common.textInput}
                    value={config.theme.banner_url ?? ""}
                    maxLength={500}
                    onChange={(e) => updateTheme({ banner_url: e.target.value })}
                    placeholder="/banners/campaign-everyday.jpg"
                  />
                  {bannerUrlError ? (
                    <span className={common.fieldError}>{bannerUrlError}</span>
                  ) : (
                    <div className={styles.quickChips}>
                      {QUICK_MEDIA_ASSETS.map((asset) => (
                        <button
                          key={`banner-${asset.url}`}
                          type="button"
                          className={`${styles.quickChip} ${
                            config.theme.banner_url === asset.url ? styles.quickChipActive : ""
                          }`}
                          onClick={() => updateTheme({ banner_url: asset.url })}
                        >
                          {asset.label}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            </details>

            {/* Group 2: Colors & WCAG Contrast */}
            <details className={common.accordion} open>
              <summary className={common.accordionSummary}>
                <span className={styles.inspectorAccordionTitle}>
                  <span>Bảng màu &amp; WCAG</span>
                  <span
                    className={`${common.pill} ${allContrastPass ? common.pillPos : common.pillNeg}`}
                  >
                    {allContrastPass ? "Đạt chuẩn" : "Cần sửa"}
                  </span>
                </span>
                <span className={common.accordionChevron} aria-hidden="true">
                  <IconChevronDown size={16} />
                </span>
              </summary>
              <div className={common.accordionBody}>
                <div className={styles.presetGrid}>
                  {THEME_PRESETS.map((preset) => {
                    const active = activePresetId === preset.id;
                    return (
                      <button
                        key={preset.id}
                        type="button"
                        className={`${styles.presetBtn} ${active ? styles.presetBtnActive : ""}`}
                        onClick={() => updateTheme(preset.colors)}
                      >
                        <div className={styles.presetHead}>
                          <span className={styles.presetName}>{preset.name}</span>
                          <span className={styles.presetDots} aria-hidden="true">
                            <span
                              className={styles.presetDot}
                              style={{ backgroundColor: preset.colors.bg_color }}
                            />
                            <span
                              className={styles.presetDot}
                              style={{ backgroundColor: preset.colors.text_color }}
                            />
                            <span
                              className={styles.presetDot}
                              style={{ backgroundColor: preset.colors.accent_color }}
                            />
                          </span>
                        </div>
                      </button>
                    );
                  })}
                </div>

                <div className={styles.colorGrid}>
                  {colorFields.map((field) => {
                    const rawVal = String(config.theme[field.key] ?? "");
                    const validHex = isValidHexColor(rawVal);
                    return (
                      <div key={field.key} className={common.field}>
                        <label htmlFor={`color-${field.key}`} className={common.fieldLabel}>
                          {field.label}
                        </label>
                        <div className={styles.colorControl}>
                          <input
                            type="color"
                            className={styles.colorPicker}
                            aria-label={`Chọn ${field.label}`}
                            value={validHex ? rawVal : "#FFFFFF"}
                            onChange={(e) =>
                              updateTheme({ [field.key]: e.target.value.toUpperCase() })
                            }
                          />
                          <input
                            id={`color-${field.key}`}
                            type="text"
                            className={styles.hexInput}
                            value={rawVal}
                            maxLength={7}
                            onChange={(e) =>
                              updateTheme({ [field.key]: e.target.value.toUpperCase() })
                            }
                            placeholder="#FFFFFF"
                          />
                        </div>
                      </div>
                    );
                  })}
                </div>

                <div className={styles.contrastList} aria-live="polite">
                  <span className={styles.contrastTitle}>Tương phản chữ / nền (≥ 4,5:1)</span>
                  {contrastChecks.map((check) => (
                    <div key={check.id} className={styles.contrastRow}>
                      <span className={styles.contrastLabel}>{check.label}</span>
                      <span className={styles.contrastRight}>
                        <span className={styles.contrastRatioVal}>
                          {formatContrastRatio(check.ratio)}
                        </span>
                        <span
                          className={`${common.pill} ${
                            check.passes ? common.pillPos : common.pillNeg
                          }`}
                        >
                          {check.passes ? "Đạt" : "Thấp"}
                        </span>
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            </details>

            {/* Group 3: Global Chrome & Storefront Widgets */}
            <details className={common.accordion}>
              <summary className={common.accordionSummary}>
                <span>Khung giao diện &amp; Tiện ích</span>
                <span className={common.accordionChevron} aria-hidden="true">
                  <IconChevronDown size={16} />
                </span>
              </summary>
              <div className={common.accordionBody}>
                <div className={styles.fields2}>
                  <div className={common.field}>
                    <label htmlFor="layout-archetype" className={common.fieldLabel}>
                      Phong cách
                    </label>
                    <select
                      id="layout-archetype"
                      className={common.textInput}
                      value={config.layout.archetype}
                      onChange={(e) => updateLayout({ archetype: e.target.value as SiteArchetype })}
                    >
                      {ARCHETYPE_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="layout-header" className={common.fieldLabel}>
                      Đầu trang
                    </label>
                    <select
                      id="layout-header"
                      className={common.textInput}
                      value={config.layout.header_style}
                      onChange={(e) =>
                        updateLayout({ header_style: e.target.value as SiteHeaderStyle })
                      }
                    >
                      {HEADER_STYLE_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="layout-footer" className={common.fieldLabel}>
                      Chân trang
                    </label>
                    <select
                      id="layout-footer"
                      className={common.textInput}
                      value={config.layout.footer_style}
                      onChange={(e) =>
                        updateLayout({ footer_style: e.target.value as SiteFooterStyle })
                      }
                    >
                      {FOOTER_STYLE_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={common.field}>
                    <label htmlFor="layout-width" className={common.fieldLabel}>
                      Độ rộng khung
                    </label>
                    <select
                      id="layout-width"
                      className={common.textInput}
                      value={config.layout.container_width}
                      onChange={(e) =>
                        updateLayout({ container_width: e.target.value as SiteContainerWidth })
                      }
                    >
                      {CONTAINER_WIDTH_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                <div className={common.field}>
                  <label htmlFor="layout-card" className={common.fieldLabel}>
                    Kiểu thẻ sản phẩm
                  </label>
                  <select
                    id="layout-card"
                    className={common.textInput}
                    value={config.layout.card_style}
                    onChange={(e) => updateLayout({ card_style: e.target.value as SiteCardStyle })}
                  >
                    {CARD_STYLE_OPTIONS.map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={styles.announcementRow}>
                  <label className={styles.checkRow}>
                    <input
                      type="checkbox"
                      checked={config.layout.show_announcement}
                      onChange={(e) => updateLayout({ show_announcement: e.target.checked })}
                    />
                    <span>Hiển thị thanh thông báo đầu trang</span>
                  </label>

                  {config.layout.show_announcement && (
                    <div className={common.field}>
                      <div className={styles.charRow}>
                        <label htmlFor="layout-announce-text" className={common.fieldLabel}>
                          Nội dung thông báo
                        </label>
                        <span className={styles.charCount}>
                          {(config.layout.announcement_text ?? "").length}/140
                        </span>
                      </div>
                      <input
                        id="layout-announce-text"
                        type="text"
                        className={common.textInput}
                        value={config.layout.announcement_text ?? ""}
                        maxLength={140}
                        onChange={(e) => updateLayout({ announcement_text: e.target.value })}
                        placeholder="Giao toàn quốc 1-3 ngày, đổi size 45 ngày"
                      />
                      {announceUnsafe && (
                        <span className={common.fieldError}>Bị chặn vì {announceUnsafe}.</span>
                      )}
                    </div>
                  )}
                </div>

                <div className={styles.slotsToggleStack}>
                  <label className={styles.slotToggleCard}>
                    <input
                      type="checkbox"
                      className={common.toggleSwitch}
                      checked={config.layout.slots.search}
                      onChange={(e) => updateSlots({ search: e.target.checked })}
                    />
                    <span>
                      <strong>Tìm kiếm nhanh</strong>
                      <small>Thanh tìm sản phẩm trên đầu trang</small>
                    </span>
                  </label>
                  <label className={styles.slotToggleCard}>
                    <input
                      type="checkbox"
                      className={common.toggleSwitch}
                      checked={config.layout.slots.chat}
                      onChange={(e) => updateSlots({ chat: e.target.checked })}
                    />
                    <span>
                      <strong>Khung chat gian hàng</strong>
                      <small>Người mua nhắn trực tiếp với cửa hàng</small>
                    </span>
                  </label>
                  <label className={styles.slotToggleCard}>
                    <input
                      type="checkbox"
                      className={common.toggleSwitch}
                      checked={config.layout.slots.assistant}
                      onChange={(e) => updateSlots({ assistant: e.target.checked })}
                    />
                    <span>
                      <strong>Trợ lý AI tư vấn</strong>
                      <small>Hỗ trợ chọn cỡ và chính sách</small>
                    </span>
                  </label>
                </div>
              </div>
            </details>

            {/* Group 4: 4 Sub-Page Layouts */}
            <details className={common.accordion}>
              <summary className={common.accordionSummary}>
                <span>Bố cục 4 trang phụ</span>
                <span className={common.accordionChevron} aria-hidden="true">
                  <IconChevronDown size={16} />
                </span>
              </summary>
              <div className={common.accordionBody}>
                <div className={styles.subpageStack}>
                  {/* Catalog */}
                  <div className={styles.subpageCard}>
                    <h3 className={styles.subpageCardTitle}>Danh sách (/san-pham)</h3>
                    <div className={styles.fields2}>
                      <div className={common.field}>
                        <label htmlFor="pg-cat-layout" className={common.fieldLabel}>
                          Bố cục
                        </label>
                        <select
                          id="pg-cat-layout"
                          className={common.textInput}
                          value={config.pages.catalog.layout}
                          onChange={(e) =>
                            updateCatalogPage({ layout: e.target.value as SiteCatalogLayout })
                          }
                        >
                          {CATALOG_LAYOUT_OPTIONS.map((opt) => (
                            <option key={opt.value} value={opt.value}>
                              {opt.label}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div className={common.field}>
                        <label htmlFor="pg-cat-cols" className={common.fieldLabel}>
                          Số cột (1-5)
                        </label>
                        <input
                          id="pg-cat-cols"
                          type="number"
                          min={1}
                          max={5}
                          className={common.textInput}
                          value={config.pages.catalog.columns}
                          onChange={(e) =>
                            updateCatalogPage({
                              columns: Math.max(
                                1,
                                Math.min(5, Math.trunc(Number(e.target.value) || 3))
                              ),
                            })
                          }
                        />
                      </div>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="pg-cat-title" className={common.fieldLabel}>
                        Tiêu đề đầu trang
                      </label>
                      <input
                        id="pg-cat-title"
                        type="text"
                        className={common.textInput}
                        maxLength={120}
                        value={config.pages.catalog.intro_title}
                        onChange={(e) => updateCatalogPage({ intro_title: e.target.value })}
                      />
                    </div>
                    <div className={common.field}>
                      <label htmlFor="pg-cat-sub" className={common.fieldLabel}>
                        Mô tả ngắn
                      </label>
                      <input
                        id="pg-cat-sub"
                        type="text"
                        className={common.textInput}
                        maxLength={300}
                        value={config.pages.catalog.intro_subtitle}
                        onChange={(e) => updateCatalogPage({ intro_subtitle: e.target.value })}
                      />
                    </div>
                    <div className={styles.checkStack}>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.catalog.show_filters}
                          onChange={(e) => updateCatalogPage({ show_filters: e.target.checked })}
                        />
                        <span>Hiện bộ lọc danh mục</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.catalog.show_search}
                          onChange={(e) => updateCatalogPage({ show_search: e.target.checked })}
                        />
                        <span>Hiện ô tìm kiếm</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.catalog.show_sku_matrix}
                          onChange={(e) => updateCatalogPage({ show_sku_matrix: e.target.checked })}
                        />
                        <span>Chế độ bảng ma trận SKU</span>
                      </label>
                    </div>
                  </div>

                  {/* PDP */}
                  <div className={styles.subpageCard}>
                    <h3 className={styles.subpageCardTitle}>Chi tiết sản phẩm (PDP)</h3>
                    <div className={styles.fields2}>
                      <div className={common.field}>
                        <label htmlFor="pg-pdp-layout" className={common.fieldLabel}>
                          Bố cục PDP
                        </label>
                        <select
                          id="pg-pdp-layout"
                          className={common.textInput}
                          value={config.pages.pdp.layout}
                          onChange={(e) =>
                            updatePdpPage({ layout: e.target.value as SitePdpLayout })
                          }
                        >
                          {PDP_LAYOUT_OPTIONS.map((opt) => (
                            <option key={opt.value} value={opt.value}>
                              {opt.label}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div className={common.field}>
                        <label htmlFor="pg-pdp-gal" className={common.fieldLabel}>
                          Bộ ảnh SP
                        </label>
                        <select
                          id="pg-pdp-gal"
                          className={common.textInput}
                          value={config.pages.pdp.gallery_style}
                          onChange={(e) =>
                            updatePdpPage({ gallery_style: e.target.value as SitePdpGalleryStyle })
                          }
                        >
                          {PDP_GALLERY_OPTIONS.map((opt) => (
                            <option key={opt.value} value={opt.value}>
                              {opt.label}
                            </option>
                          ))}
                        </select>
                      </div>
                    </div>
                    <div className={styles.checkStack}>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.pdp.show_vouchers}
                          onChange={(e) => updatePdpPage({ show_vouchers: e.target.checked })}
                        />
                        <span>Hiện mã giảm giá</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.pdp.show_sku_table}
                          onChange={(e) => updatePdpPage({ show_sku_table: e.target.checked })}
                        />
                        <span>Hiện bảng tồn kho SKU</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.pdp.show_story_band}
                          onChange={(e) => updatePdpPage({ show_story_band: e.target.checked })}
                        />
                        <span>Hiện dải chất liệu &amp; xưởng</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.pdp.show_related}
                          onChange={(e) => updatePdpPage({ show_related: e.target.checked })}
                        />
                        <span>Hiện sản phẩm liên quan</span>
                      </label>
                    </div>
                  </div>

                  {/* Cart */}
                  <div className={styles.subpageCard}>
                    <h3 className={styles.subpageCardTitle}>Giỏ hàng (/gio-hang)</h3>
                    <div className={common.field}>
                      <label htmlFor="pg-cart-layout" className={common.fieldLabel}>
                        Bố cục giỏ hàng
                      </label>
                      <select
                        id="pg-cart-layout"
                        className={common.textInput}
                        value={config.pages.cart.layout}
                        onChange={(e) =>
                          updateCartPage({ layout: e.target.value as SiteCartLayout })
                        }
                      >
                        {CART_LAYOUT_OPTIONS.map((opt) => (
                          <option key={opt.value} value={opt.value}>
                            {opt.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className={common.field}>
                      <label htmlFor="pg-cart-trust" className={common.fieldLabel}>
                        Cam kết dưới nút thanh toán
                      </label>
                      <textarea
                        id="pg-cart-trust"
                        className={common.textArea}
                        rows={2}
                        maxLength={240}
                        value={config.pages.cart.trust_note}
                        onChange={(e) => updateCartPage({ trust_note: e.target.value })}
                      />
                    </div>
                    <div className={styles.checkStack}>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.cart.show_vouchers}
                          onChange={(e) => updateCartPage({ show_vouchers: e.target.checked })}
                        />
                        <span>Hiện khung áp dụng voucher</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.cart.show_addons}
                          onChange={(e) => updateCartPage({ show_addons: e.target.checked })}
                        />
                        <span>Gợi ý mua kèm</span>
                      </label>
                    </div>
                  </div>

                  {/* Info */}
                  <div className={styles.subpageCard}>
                    <h3 className={styles.subpageCardTitle}>Thông tin (/gioi-thieu)</h3>
                    <div className={common.field}>
                      <label htmlFor="pg-info-layout" className={common.fieldLabel}>
                        Bố cục trang thông tin
                      </label>
                      <select
                        id="pg-info-layout"
                        className={common.textInput}
                        value={config.pages.info.layout}
                        onChange={(e) =>
                          updateInfoPage({ layout: e.target.value as SiteInfoLayout })
                        }
                      >
                        {INFO_LAYOUT_OPTIONS.map((opt) => (
                          <option key={opt.value} value={opt.value}>
                            {opt.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className={styles.checkStack}>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.info.show_factory_profile}
                          onChange={(e) =>
                            updateInfoPage({ show_factory_profile: e.target.checked })
                          }
                        />
                        <span>Hồ sơ xưởng sản xuất</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.info.show_toc}
                          onChange={(e) => updateInfoPage({ show_toc: e.target.checked })}
                        />
                        <span>Mục lục điều hướng</span>
                      </label>
                      <label className={styles.checkRow}>
                        <input
                          type="checkbox"
                          checked={config.pages.info.show_showcase}
                          onChange={(e) => updateInfoPage({ show_showcase: e.target.checked })}
                        />
                        <span>Sản phẩm tiêu biểu cuối trang</span>
                      </label>
                    </div>
                  </div>
                </div>
              </div>
            </details>
          </div>

          {renderLivePreviewPane()}
        </div>
      )}

      {/* TAB 3: Studio Split — Section & Block Inspector (36%) + Live Preview (64%) */}
      {activeTab === "sections" && (
        <div className={styles.studioWorkspace}>
          <div className={styles.inspectorCol}>
            <details className={common.accordion} open>
              <summary className={common.accordionSummary}>
                <span className={styles.inspectorAccordionTitle}>
                  <span>
                    Khối trang ({activePageSections.length}/{activePageMeta.maxSections})
                  </span>
                  <span className={common.accordionMeta}>
                    Tổng {totalSectionsCount}/{MAX_TOTAL_SECTIONS}
                  </span>
                </span>
                <span className={common.accordionChevron} aria-hidden="true">
                  <IconChevronDown size={16} />
                </span>
              </summary>

              <div className={common.accordionBody}>
                <div
                  className={styles.sectionPageBar}
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
                        className={`${styles.pageSwitchBtn} ${active ? styles.pageSwitchBtnActive : ""}`}
                        onClick={() => {
                          setSectionPageKey(tab.key);
                          setPreviewPage(tab.key);
                          const nextSecs = getPageSections(config, tab.key);
                          setSelectedSectionId(nextSecs[0]?.id ?? null);
                        }}
                      >
                        <span>{tab.shortLabel}</span>
                        <span className={styles.pageCountBadge}>{count}</span>
                      </button>
                    );
                  })}
                </div>

                <div className={styles.addSectionBar}>
                  <select
                    className={common.textInput}
                    value={newSectionType}
                    onChange={(e) => setNewSectionType(e.target.value as SiteSectionV2Type)}
                    aria-label="Chọn loại khối nội dung muốn thêm"
                  >
                    {SECTION_V2_TYPE_ORDER.map((t) => (
                      <option key={t} value={t}>
                        {SECTION_V2_TYPE_META[t].label}
                      </option>
                    ))}
                  </select>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={
                      activePageSections.length >= activePageMeta.maxSections ||
                      totalSectionsCount >= MAX_TOTAL_SECTIONS
                    }
                    onClick={handleAddSection}
                  >
                    <IconPlus size={14} />
                    <span>Thêm</span>
                  </Button>
                </div>

                {activePageSections.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    Chưa có khối nội dung cho {activePageMeta.label}.
                  </div>
                ) : (
                  <div className={styles.sectionList}>
                    {activePageSections.map((sec, idx) => {
                      const isSelected = selectedSection?.id === sec.id;
                      const blockCount = sec.blocks?.length ?? 0;
                      const variantLabel = sec.variant
                        ? (SECTION_VARIANT_LABELS[sec.variant] ?? sec.variant)
                        : "";
                      return (
                        <div
                          key={sec.id}
                          className={`${styles.sectionItem} ${isSelected ? styles.sectionItemActive : ""}`}
                        >
                          <div className={styles.sectionItemHead}>
                            <button
                              type="button"
                              className={styles.sectionSelectBtn}
                              onClick={() => setSelectedSectionId(sec.id)}
                            >
                              <span className={styles.sectionItemTitle}>
                                {getSectionHeadingPreview(sec)}
                              </span>
                              <span className={styles.sectionItemMeta}>
                                {SECTION_V2_TYPE_META[sec.type].label}
                                {variantLabel ? `, ${variantLabel}` : ""}
                                {blockCount > 0 ? `, ${blockCount} block` : ""},{" "}
                                {sec.visible ? "Hiện" : "Ẩn"}
                              </span>
                            </button>
                          </div>

                          <div className={styles.sectionItemActions}>
                            <div className={styles.sectionMoveGroup}>
                              <button
                                type="button"
                                className={styles.miniBtn}
                                disabled={idx === 0}
                                onClick={() => handleMoveSection(idx, -1)}
                                aria-label="Di chuyển khối lên trên"
                              >
                                Lên
                              </button>
                              <button
                                type="button"
                                className={styles.miniBtn}
                                disabled={idx === activePageSections.length - 1}
                                onClick={() => handleMoveSection(idx, 1)}
                                aria-label="Di chuyển khối xuống dưới"
                              >
                                Xuống
                              </button>
                              <button
                                type="button"
                                className={styles.miniBtn}
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
                                {sec.visible ? "Ẩn" : "Hiện"}
                              </button>
                            </div>

                            <button
                              type="button"
                              className={`${styles.miniBtn} ${styles.miniBtnDanger}`}
                              onClick={() => handleDeleteSection(sec.id)}
                            >
                              Xóa
                            </button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </details>

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
          </div>

          {renderLivePreviewPane()}
        </div>
      )}

      {/* TAB 4: Line-Aware JSON v2 Code Editor + Live Preview (>= 60%) */}
      {activeTab === "code" && (
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
      )}

      {/* TAB 5: Domains */}
      {activeTab === "domains" && (
        <SellerSiteDomainsSection
          site={site}
          initialDomains={domains}
          rootDomain={rootDomain}
          requestHost={requestHost}
          requestProto={requestProto}
          loadError={domainsLoadError}
          onDomainsChange={handleDomainsChange}
          onSwitchToTheme={() => setActiveTab("theme")}
        />
      )}

      {/* TAB 6: Version History & Rollback */}
      {activeTab === "versions" && (
        <section className={common.card} aria-label="Lịch sử phiên bản đã xuất bản">
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Lịch sử phiên bản</h2>
            {site && (
              <Link href="/seller/site/xem-truoc" className={common.cardLink}>
                Xem trước bản nháp
              </Link>
            )}
          </div>

          {versions.length > 0 && !isSuspended && (
            <div className={styles.rollbackBar}>
              <div className={styles.publishNoteField}>
                <label htmlFor="rollback-note" className={common.fieldLabel}>
                  Ghi chú khi khôi phục (tùy chọn)
                </label>
                <input
                  id="rollback-note"
                  type="text"
                  className={common.textInput}
                  value={rollbackNote}
                  maxLength={300}
                  onChange={(e) => setRollbackNote(e.target.value)}
                  placeholder="Ví dụ: Quay lại bố cục trước chiến dịch cuối tuần"
                />
              </div>
            </div>
          )}

          {versions.length === 0 ? (
            <div className={common.inlineEmpty}>
              Chưa có phiên bản xuất bản. Lưu nháp và bấm &ldquo;Xuất bản&rdquo; để tạo v1.
            </div>
          ) : (
            <>
              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Phiên bản</th>
                      <th>Mẫu</th>
                      <th>Thời gian</th>
                      <th>Ghi chú</th>
                      <th>Trạng thái</th>
                      <th className={common.numCol}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {versions.map((ver) => {
                      const isActive = site?.published_version_id === ver.id;
                      const isRolling = rollingBackId === ver.id;
                      const tplId = ver.config?.template_id || "legacy-v1";
                      const tplLabel = templates.find((t) => t.id === tplId)?.name || tplId;
                      return (
                        <tr key={ver.id}>
                          <td>
                            <span className={styles.versionNum}>v{ver.version_number}</span>
                          </td>
                          <td>
                            <span className={styles.templateTag}>{tplLabel}</span>
                          </td>
                          <td>{ver.created_at ? formatDateTimeVi(ver.created_at) : "Chưa rõ"}</td>
                          <td>{ver.note || "Xuất bản cập nhật"}</td>
                          <td>
                            <span className={`${common.pill} ${isActive ? common.pillPos : ""}`}>
                              {isActive ? "Đang hiển thị" : "Lưu trữ"}
                            </span>
                          </td>
                          <td className={common.numCol}>
                            <div
                              className={`${common.rowHoverActions} ${isRolling ? common.rowHoverActionsVisible : ""}`}
                            >
                              <Button
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={isSuspended || isRolling || Boolean(rollingBackId)}
                                onClick={() => void handleRollback(ver)}
                              >
                                {isRolling
                                  ? "Đang khôi phục..."
                                  : isActive
                                    ? "Phát hành lại"
                                    : "Khôi phục"}
                              </Button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={common.mobileList}>
                {versions.map((ver) => {
                  const isActive = site?.published_version_id === ver.id;
                  const isRolling = rollingBackId === ver.id;
                  const tplId = ver.config?.template_id || "legacy-v1";
                  const tplLabel = templates.find((t) => t.id === tplId)?.name || tplId;
                  return (
                    <div key={ver.id} className={common.mobileRow}>
                      <div className={common.mobileRowTop}>
                        <div>
                          <span className={styles.versionNum}>Phiên bản v{ver.version_number}</span>
                          <span className={common.cellSub}>
                            {tplLabel}
                            {ver.created_at ? `, ${formatDateTimeVi(ver.created_at)}` : ""}
                          </span>
                        </div>
                        <span className={`${common.pill} ${isActive ? common.pillPos : ""}`}>
                          {isActive ? "Đang hiển thị" : "Lưu trữ"}
                        </span>
                      </div>
                      <p className={common.cardMeta}>{ver.note || "Xuất bản cập nhật"}</p>
                      <div>
                        <Button
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={isSuspended || isRolling || Boolean(rollingBackId)}
                          onClick={() => void handleRollback(ver)}
                        >
                          {isRolling ? "Đang khôi phục..." : "Khôi phục"}
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </section>
      )}
    </div>
  );
}
