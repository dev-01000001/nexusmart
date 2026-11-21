"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Button, IconArrowUpRight, IconPlus, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import type { SiteDomainDTO } from "@/lib/site/domains";
import { useSellerContext } from "@/features/seller/SellerShell";
import type { GwCategory, GwSellerProduct, GwSellerShop } from "@/features/seller/sellerApiTypes";
import common from "@/features/seller/SellerCommon.module.css";
import type {
  GwSite,
  GwSiteVersion,
  SiteConfig,
  SiteFontFamily,
  SiteSection,
  SiteSectionType,
  SiteTheme,
} from "./siteEditorTypes";
import {
  ALLOWED_FONTS,
  MAX_SECTIONS,
  QUICK_MEDIA_ASSETS,
  SECTION_TYPE_META,
  SECTION_TYPE_ORDER,
  THEME_PRESETS,
  buildCleanSiteConfigPayload,
  checkThemeContrast,
  createDefaultSiteConfig,
  createSectionTemplate,
  formatContrastRatio,
  getSectionHeadingPreview,
  getSiteStatusMeta,
  isValidHexColor,
  normalizeSubdomainInput,
  suggestSubdomainFromShopName,
  validateSafeUrlClient,
  validateSiteDraftClient,
  validateSubdomainClient,
} from "./siteEditorUtils";
import { SectionEditorPanel } from "./SectionEditorPanel";
import { SellerSiteDomainsSection } from "./SellerSiteDomainsSection";
import { buildPublicSiteUrl } from "./siteSummary";
import domainStyles from "./SellerSiteDomains.module.css";
import styles from "./SellerSiteEditor.module.css";

export type EditorTab = "theme" | "domains" | "sections" | "versions";

export interface SellerSiteEditorClientProps {
  initialSite: GwSite | null;
  initialShop: GwSellerShop | null;
  initialVersions: GwSiteVersion[];
  initialDomains?: SiteDomainDTO[];
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
  domainsLoadError = null,
  initialTab = "theme",
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
  const [config, setConfig] = useState<SiteConfig>(() => {
    if (initialSite?.draft_config) return initialSite.draft_config;
    return createDefaultSiteConfig(activeShop?.name ?? "", activeShop?.description ?? "");
  });
  const [selectedSectionId, setSelectedSectionId] = useState<string | null>(
    () => initialSite?.draft_config?.sections[0]?.id ?? "hero-main"
  );
  const [newSectionType, setNewSectionType] = useState<SiteSectionType>("banner");

  const [dirty, setDirty] = useState<boolean>(!initialSite);
  const [formError, setFormError] = useState<string | null>(null);
  const [savingDraft, setSavingDraft] = useState<boolean>(false);
  const [publishing, setPublishing] = useState<boolean>(false);
  const [publishOpen, setPublishOpen] = useState<boolean>(false);
  const [publishNote, setPublishNote] = useState<string>("");
  const [rollbackNote, setRollbackNote] = useState<string>("");
  const [rollingBackId, setRollingBackId] = useState<string | null>(null);

  const contrastChecks = useMemo(() => checkThemeContrast(config.theme), [config.theme]);
  const allContrastPass = contrastChecks.every((c) => c.passes);
  const subdomainError = useMemo(() => validateSubdomainClient(subdomain), [subdomain]);
  const logoUrlError = useMemo(
    () => validateSafeUrlClient(config.theme.logo_url),
    [config.theme.logo_url]
  );
  const bannerUrlError = useMemo(
    () => validateSafeUrlClient(config.theme.banner_url),
    [config.theme.banner_url]
  );

  const statusMeta = getSiteStatusMeta(site?.status);
  const isSuspended = (site?.status ?? "").toUpperCase() === "SUSPENDED";
  const isPublished = (site?.status ?? "").toUpperCase() === "PUBLISHED";
  const cleanSub = normalizeSubdomainInput(subdomain);
  const previewHost = cleanSub ? `${cleanSub}.${rootDomain}` : `<ten-mien>.${rootDomain}`;
  const visibleSectionsCount = config.sections.filter((s) => s.visible).length;

  const selectedSection = useMemo(() => {
    if (config.sections.length === 0) return null;
    if (selectedSectionId) {
      const found = config.sections.find((s) => s.id === selectedSectionId);
      if (found) return found;
    }
    return config.sections[0];
  }, [config.sections, selectedSectionId]);

  const activePublishedVersionNumber = useMemo(() => {
    if (site?.published_version_number) return site.published_version_number;
    if (!site?.published_version_id) return null;
    const found = versions.find((v) => v.id === site.published_version_id);
    return found?.version_number ?? null;
  }, [site, versions]);

  const updateTheme = (patch: Partial<SiteTheme>) => {
    setConfig((prev) => ({
      ...prev,
      theme: { ...prev.theme, ...patch },
    }));
    setDirty(true);
    setFormError(null);
  };

  const updateSections = (updater: (prev: SiteSection[]) => SiteSection[]) => {
    setConfig((prev) => ({
      ...prev,
      sections: updater(prev.sections),
    }));
    setDirty(true);
    setFormError(null);
  };

  const handleAddSection = () => {
    if (config.sections.length >= MAX_SECTIONS) {
      setFormError(`Site riêng hỗ trợ tối đa ${MAX_SECTIONS} khối nội dung.`);
      return;
    }
    const created = createSectionTemplate(newSectionType, config.sections.length);
    updateSections((prev) => [...prev, created]);
    setSelectedSectionId(created.id);
    showToast(`Đã thêm khối "${SECTION_TYPE_META[newSectionType].label}" vào bản nháp.`);
  };

  const handleMoveSection = (index: number, delta: -1 | 1) => {
    const target = index + delta;
    if (target < 0 || target >= config.sections.length) return;
    updateSections((prev) => {
      const copy = [...prev];
      const [moved] = copy.splice(index, 1);
      copy.splice(target, 0, moved);
      return copy;
    });
  };

  const handleDeleteSection = (id: string) => {
    updateSections((prev) => {
      const next = prev.filter((s) => s.id !== id);
      if (selectedSectionId === id) {
        setSelectedSectionId(next[0]?.id ?? null);
      }
      return next;
    });
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
    const cleanPayload = buildCleanSiteConfigPayload(config);
    const clientErr = validateSiteDraftClient(cleanSub, cleanPayload);
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
      showToast("Đã lưu bản nháp site riêng. Bạn có thể xem trước trước khi xuất bản.");
      router.refresh();
    }
  };

  const handleConfirmPublish = async () => {
    if (isSuspended) return;
    setFormError(null);

    // Save unsaved changes before publishing so the live snapshot matches what seller sees.
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
      showToast(
        `Đã xuất bản phiên bản ${verLabel} lên tên miền ${res.site?.primary_host || previewHost}.`
      );
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

  const colorFields: Array<{ key: keyof SiteTheme; label: string }> = [
    { key: "bg_color", label: "Nền trang" },
    { key: "surface_color", label: "Nền thẻ nội dung" },
    { key: "text_color", label: "Màu chữ chính" },
    { key: "accent_color", label: "Màu nhấn / Nút" },
    { key: "accent_text_color", label: "Chữ trên nút nhấn" },
  ];

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Site riêng</h1>
          <p className={common.finding}>
            Thiết lập tên miền phụ, bảng màu thương hiệu và bố cục cửa hàng riêng cho{" "}
            {activeShop?.name || "gian hàng của bạn"}.
          </p>
        </div>
        <div className={common.headerActions}>
          {site ? (
            <Button variant="outline" size="sm" shape="app" href="/seller/site/xem-truoc">
              Xem trước bản nháp
            </Button>
          ) : (
            <Button variant="outline" size="sm" shape="app" disabled>
              Xem trước bản nháp
            </Button>
          )}
          <Button
            variant="secondary"
            size="sm"
            shape="app"
            disabled={savingDraft || publishing}
            onClick={() => void handleSaveDraftClick()}
          >
            {savingDraft ? "Đang lưu..." : "Lưu nháp"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            shape="app"
            disabled={isSuspended || savingDraft || publishing}
            onClick={() => setPublishOpen((prev) => !prev)}
          >
            {publishing ? "Đang xuất bản..." : "Xuất bản"}
          </Button>
        </div>
      </header>

      <section className={common.balanceStrip} aria-label="Tóm tắt trạng thái site riêng">
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Trạng thái</span>
          <div>
            <span className={`${common.pill} ${pillToneClass}`}>{statusMeta.label}</span>
          </div>
          <span className={common.balanceCaption}>
            {dirty ? "Có thay đổi chưa lưu nháp" : "Bản nháp đã đồng bộ"}
          </span>
        </div>

        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Địa chỉ site riêng</span>
          <span className={styles.hostPreviewUrl}>{site?.primary_host || previewHost}</span>
          <div className={domainStyles.summaryActionRow}>
            {isPublished && site?.primary_host ? (
              <a
                href={buildPublicSiteUrl(site.primary_host, requestHost, requestProto) || "#"}
                target="_blank"
                rel="noopener noreferrer"
                className={styles.publicLink}
              >
                <span>Xem site công khai</span>
                <IconArrowUpRight size={13} />
              </a>
            ) : (
              <span className={common.balanceCaption}>Hiển thị công khai sau khi xuất bản</span>
            )}
            <button
              type="button"
              className={domainStyles.summaryManageBtn}
              onClick={() => setActiveTab("domains")}
            >
              Quản lý tên miền
            </button>
          </div>
        </div>

        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Phiên bản đang phát hành</span>
          <span className={common.balanceValue}>
            {activePublishedVersionNumber
              ? `v${activePublishedVersionNumber}`
              : site?.published_version_id
                ? "Đang hoạt động"
                : "Chưa có"}
          </span>
          <span className={common.balanceCaption}>
            {site?.published_at ? formatDateTimeVi(site.published_at) : "Chưa xuất bản lần nào"}
          </span>
        </div>

        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Khối nội dung</span>
          <span className={common.balanceValue}>
            {config.sections.length}/{MAX_SECTIONS}
          </span>
          <span className={common.balanceCaption}>
            {visibleSectionsCount} khối đang bật hiển thị
          </span>
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
              Ghi chú cho phiên bản xuất bản (tùy chọn)
            </label>
            <input
              id="publish-note-input"
              type="text"
              className={common.textInput}
              value={publishNote}
              maxLength={300}
              onChange={(e) => setPublishNote(e.target.value)}
              placeholder="Ví dụ: Cập nhật bảng màu mới và thêm sản phẩm nổi bật tháng 11"
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
                  ? "Lưu nháp & Xuất bản ngay"
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

      <div className={common.underlineTabs} role="tablist" aria-label="Các mục cấu hình site riêng">
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "theme"}
          className={`${common.underlineTab} ${activeTab === "theme" ? common.underlineTabActive : ""}`}
          onClick={() => setActiveTab("theme")}
        >
          Giao diện
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "domains"}
          className={`${common.underlineTab} ${activeTab === "domains" ? common.underlineTabActive : ""}`}
          onClick={() => setActiveTab("domains")}
        >
          Tên miền
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "sections"}
          className={`${common.underlineTab} ${activeTab === "sections" ? common.underlineTabActive : ""}`}
          onClick={() => setActiveTab("sections")}
        >
          Các khối nội dung ({config.sections.length})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "versions"}
          className={`${common.underlineTab} ${activeTab === "versions" ? common.underlineTabActive : ""}`}
          onClick={() => setActiveTab("versions")}
        >
          Lịch sử phiên bản ({versions.length})
        </button>
      </div>

      {activeTab === "theme" && (
        <div className={styles.twoCol}>
          <section className={common.card} aria-label="Tên miền phụ và nhận diện thương hiệu">
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>Tên miền phụ & Thương hiệu</h2>
              {dirty && <span className={styles.dirtyNotice}>Chưa lưu</span>}
            </div>

            <div className={styles.fieldStack}>
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

              <div className={styles.fields2}>
                <div className={common.field}>
                  <div className={styles.charRow}>
                    <label htmlFor="site-brand" className={common.fieldLabel}>
                      Tên thương hiệu hiển thị
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
                </div>

                <div className={common.field}>
                  <label htmlFor="site-font" className={common.fieldLabel}>
                    Phông chữ cửa hàng
                  </label>
                  <select
                    id="site-font"
                    className={common.textInput}
                    value={config.theme.font_family}
                    onChange={(e) => updateTheme({ font_family: e.target.value as SiteFontFamily })}
                  >
                    {ALLOWED_FONTS.map((f) => (
                      <option key={f.value} value={f.value}>
                        {f.label} ({f.hint})
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              <div className={common.field}>
                <div className={styles.charRow}>
                  <label htmlFor="site-tagline" className={common.fieldLabel}>
                    Khẩu hiệu ngắn (tagline)
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
                  placeholder="Áo thun cotton 220gsm may trực tiếp tại xưởng Việt Nam"
                />
              </div>

              <div className={common.field}>
                <label htmlFor="site-logo-url" className={common.fieldLabel}>
                  Đường dẫn ảnh logo (tùy chọn)
                </label>
                <input
                  id="site-logo-url"
                  type="text"
                  className={common.textInput}
                  value={config.theme.logo_url ?? ""}
                  maxLength={500}
                  onChange={(e) => updateTheme({ logo_url: e.target.value })}
                  placeholder="/banners/vietnam-workshop.jpg hoặc https://..."
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
                  Đường dẫn ảnh bìa mặc định (tùy chọn)
                </label>
                <input
                  id="site-banner-url"
                  type="text"
                  className={common.textInput}
                  value={config.theme.banner_url ?? ""}
                  maxLength={500}
                  onChange={(e) => updateTheme({ banner_url: e.target.value })}
                  placeholder="/banners/campaign-everyday.jpg hoặc https://..."
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
          </section>

          <section className={common.card} aria-label="Bảng màu và kiểm tra độ tương phản">
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>Bảng màu & Độ tương phản</h2>
              <span
                className={`${common.pill} ${allContrastPass ? common.pillPos : common.pillNeg}`}
              >
                {allContrastPass ? "Đạt chuẩn đọc rõ" : "Cần tăng tương phản"}
              </span>
            </div>

            <div className={styles.fieldStack}>
              <div className={common.field}>
                <span className={common.fieldLabel}>Bộ màu gợi ý (đạt chuẩn ≥ 4,5:1)</span>
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
                        <span className={styles.presetDesc}>{preset.description}</span>
                      </button>
                    );
                  })}
                </div>
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
                <span className={styles.contrastTitle}>
                  Kiểm tra tương phản chữ / nền (≥ 4,5:1)
                </span>
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
                        {check.passes ? "Đạt chuẩn" : "Dưới 4,5:1"}
                      </span>
                    </span>
                  </div>
                ))}
              </div>

              <div
                className={styles.themeSample}
                style={{
                  backgroundColor: isValidHexColor(config.theme.bg_color)
                    ? config.theme.bg_color
                    : "#F7F9FA",
                  color: isValidHexColor(config.theme.text_color)
                    ? config.theme.text_color
                    : "#1F3038",
                }}
              >
                <div
                  className={styles.themeSampleCard}
                  style={{
                    backgroundColor: isValidHexColor(config.theme.surface_color)
                      ? config.theme.surface_color
                      : "#FFFFFF",
                  }}
                >
                  <span className={styles.themeSampleBrand}>
                    {config.theme.brand_name || "Tên thương hiệu"}
                  </span>
                  <span className={styles.themeSampleTagline}>
                    {config.theme.tagline || "Xem nhanh phối màu chữ, nền thẻ và nút hành động."}
                  </span>
                </div>
                <span
                  className={styles.themeSampleBtn}
                  style={{
                    backgroundColor: isValidHexColor(config.theme.accent_color)
                      ? config.theme.accent_color
                      : "#0D7480",
                    color: isValidHexColor(config.theme.accent_text_color)
                      ? config.theme.accent_text_color
                      : "#FFFFFF",
                  }}
                >
                  Xem sản phẩm
                </span>
              </div>
            </div>
          </section>
        </div>
      )}

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

      {activeTab === "sections" && (
        <div className={styles.sectionsGrid}>
          <section className={common.card} aria-label="Danh sách các khối nội dung">
            <div className={common.cardHeader}>
              <h2 className={common.cardTitle}>
                Thứ tự các khối ({config.sections.length}/{MAX_SECTIONS})
              </h2>
            </div>

            <div className={styles.addSectionBar}>
              <select
                className={common.textInput}
                value={newSectionType}
                onChange={(e) => setNewSectionType(e.target.value as SiteSectionType)}
                aria-label="Chọn loại khối nội dung muốn thêm"
              >
                {SECTION_TYPE_ORDER.map((t) => (
                  <option key={t} value={t}>
                    {SECTION_TYPE_META[t].label}
                  </option>
                ))}
              </select>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={config.sections.length >= MAX_SECTIONS}
                onClick={handleAddSection}
              >
                <IconPlus size={14} />
                <span>Thêm khối</span>
              </Button>
            </div>

            {config.sections.length === 0 ? (
              <div className={common.inlineEmpty}>
                Chưa có khối nội dung nào. Hãy chọn loại khối ở trên và bấm &ldquo;Thêm khối&rdquo;.
              </div>
            ) : (
              <div className={styles.sectionList}>
                {config.sections.map((sec, idx) => {
                  const isSelected = selectedSection?.id === sec.id;
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
                            {SECTION_TYPE_META[sec.type].label} ·{" "}
                            {sec.visible ? "Đang bật" : "Đang ẩn"}
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
                            disabled={idx === config.sections.length - 1}
                            onClick={() => handleMoveSection(idx, 1)}
                            aria-label="Di chuyển khối xuống dưới"
                          >
                            Xuống
                          </button>
                          <button
                            type="button"
                            className={styles.miniBtn}
                            onClick={() =>
                              updateSections((prev) =>
                                prev.map((item) =>
                                  item.id === sec.id ? { ...item, visible: !item.visible } : item
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
          </section>

          <SectionEditorPanel
            section={selectedSection}
            products={products}
            categories={categories}
            onChange={(updated) =>
              updateSections((prev) =>
                prev.map((item) => (item.id === updated.id ? updated : item))
              )
            }
          />
        </div>
      )}

      {activeTab === "versions" && (
        <section className={common.card} aria-label="Lịch sử phiên bản đã xuất bản">
          <div className={common.cardHeader}>
            <div>
              <h2 className={common.cardTitle}>Lịch sử phiên bản & Khôi phục</h2>
              <p className={common.cardMeta}>
                Mỗi lần xuất bản lưu một bản chụp bất biến. Bạn có thể quay về bất kỳ phiên bản cũ
                nào bất cứ lúc nào.
              </p>
            </div>
            {site && (
              <Link href="/seller/site/xem-truoc" className={common.cardLink}>
                Xem trước bản nháp hiện tại
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
              Chưa có phiên bản nào được xuất bản. Hãy lưu bản nháp và bấm &ldquo;Xuất bản&rdquo; để
              tạo phiên bản v1.
            </div>
          ) : (
            <>
              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Phiên bản</th>
                      <th>Thời gian xuất bản</th>
                      <th>Ghi chú</th>
                      <th>Trạng thái</th>
                      <th className={common.numCol}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {versions.map((ver) => {
                      const isActive = site?.published_version_id === ver.id;
                      const isRolling = rollingBackId === ver.id;
                      return (
                        <tr key={ver.id}>
                          <td>
                            <span className={styles.versionNum}>v{ver.version_number}</span>
                          </td>
                          <td>{ver.created_at ? formatDateTimeVi(ver.created_at) : "Chưa rõ"}</td>
                          <td>{ver.note || "Xuất bản cập nhật giao diện"}</td>
                          <td>
                            <span className={`${common.pill} ${isActive ? common.pillPos : ""}`}>
                              {isActive ? "Đang hiển thị" : "Bản lưu trữ"}
                            </span>
                          </td>
                          <td className={common.numCol}>
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
                                  ? "Đưa về nháp & phát hành lại"
                                  : "Khôi phục phiên bản này"}
                            </Button>
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
                  return (
                    <div key={ver.id} className={common.mobileRow}>
                      <div className={common.mobileRowTop}>
                        <div>
                          <span className={styles.versionNum}>Phiên bản v{ver.version_number}</span>
                          <span className={common.cellSub}>
                            {ver.created_at ? formatDateTimeVi(ver.created_at) : ""}
                          </span>
                        </div>
                        <span className={`${common.pill} ${isActive ? common.pillPos : ""}`}>
                          {isActive ? "Đang hiển thị" : "Bản lưu trữ"}
                        </span>
                      </div>
                      <p className={common.cardMeta}>{ver.note || "Xuất bản cập nhật giao diện"}</p>
                      <div>
                        <Button
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={isSuspended || isRolling || Boolean(rollingBackId)}
                          onClick={() => void handleRollback(ver)}
                        >
                          {isRolling ? "Đang khôi phục..." : "Khôi phục phiên bản này"}
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
