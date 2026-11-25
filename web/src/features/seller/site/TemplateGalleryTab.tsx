"use client";

import React, { useEffect, useMemo, useState } from "react";
import { Button, IconClose } from "@/components";
import { cloneTemplateConfigForShop } from "@/features/site/templateCatalog";
import { sanitizeSafeImageUrl } from "@/features/site/templateValidator";
import common from "@/features/seller/SellerCommon.module.css";
import type { PlatformTemplateDTO, SiteTemplateConfig, TemplatePageKey } from "./siteEditorTypes";
import { ARCHETYPE_OPTIONS, TEMPLATE_PAGE_TABS } from "./siteEditorUtils";
import type { PreviewStorefrontDataset } from "./previewData";
import { SitePreviewContent } from "./SitePreviewContent";
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";
import styles from "./SellerSiteEditor.module.css";

export type { DevicePreviewWidth };

export interface TemplateGalleryTabProps {
  templates: PlatformTemplateDTO[];
  currentTemplateId: string;
  currentConfig: SiteTemplateConfig;
  previewDataset: PreviewStorefrontDataset;
  platformOrigin: string;
  applyingTemplateId: string | null;
  onApplyTemplate: (template: PlatformTemplateDTO) => Promise<void>;
}

/** Distinct real storefront/atmosphere photographs for each of the 8 platform templates. */
const TEMPLATE_GALLERY_PHOTOS: Record<string, string> = {
  "minimal-fashion": "/banners/campaign-everyday.jpg",
  editorial: "/banners/campaign-office.jpg",
  "dense-grid": "/banners/campaign-green-fiber.jpg",
  "craft-atelier": "/banners/vietnam-workshop.jpg",
  "sport-active": "/banners/campaign-running.jpg",
  "luxury-maison": "/banners/fabric-closeup.jpg",
  "one-page-launch": "/atmosphere/cotton-twill-slate.jpg",
  "multi-sku-catalog": "/atmosphere/jacquard-loom-poster.jpg",
};

/** Crisp <= 8-word one-line summaries for each template card. */
const TEMPLATE_SHORT_NOTES: Record<string, string> = {
  "minimal-fashion": "Lưới 3 cột so le, chữ tinh gọn",
  editorial: "Đầu trang tạp chí, ảnh lệch tầng",
  "dense-grid": "Thanh lọc dính, lưới 5 cột dày",
  "craft-atelier": "Ảnh chất vải tràn màn, dải công đoạn",
  "sport-active": "Nền tối tương phản, ảnh cắt chéo",
  "luxury-maison": "Khung ảnh thoáng rộng, lưới 2 cột",
  "one-page-launch": "Tập trung một sản phẩm, chốt đơn nhanh",
  "multi-sku-catalog": "Mục lục hàng ngang, bảng chọn SKU",
};

function labelOf<T extends string>(
  options: ReadonlyArray<{ value: T; label: string }>,
  val: T
): string {
  return options.find((o) => o.value === val)?.label ?? val;
}

function getTemplatePhoto(tpl: PlatformTemplateDTO): string {
  return (
    sanitizeSafeImageUrl(TEMPLATE_GALLERY_PHOTOS[tpl.id]) ||
    sanitizeSafeImageUrl(tpl.preview_image_url) ||
    sanitizeSafeImageUrl(tpl.config.theme.banner_url) ||
    "/banners/campaign-everyday.jpg"
  );
}

function getTemplateShortNote(tpl: PlatformTemplateDTO): string {
  if (TEMPLATE_SHORT_NOTES[tpl.id]) return TEMPLATE_SHORT_NOTES[tpl.id];
  const words = (tpl.description || "").trim().split(/\s+/).filter(Boolean);
  return words.slice(0, 8).join(" ");
}

export function TemplateGalleryTab({
  templates,
  currentTemplateId,
  currentConfig,
  previewDataset,
  platformOrigin,
  applyingTemplateId,
  onApplyTemplate,
}: TemplateGalleryTabProps) {
  const [previewTemplate, setPreviewTemplate] = useState<PlatformTemplateDTO | null>(null);
  const [previewPage, setPreviewPage] = useState<TemplatePageKey>("home");
  const [previewDevice, setPreviewDevice] = useState<DevicePreviewWidth>("1440");
  const [confirmApplyId, setConfirmApplyId] = useState<string | null>(null);

  const previewConfig = useMemo(() => {
    if (!previewTemplate) return null;
    return cloneTemplateConfigForShop(previewTemplate, {
      brandName: currentConfig.theme.brand_name || previewDataset.shop.name,
      tagline: currentConfig.theme.tagline || previewDataset.shop.description,
      logoUrl: currentConfig.theme.logo_url || "",
    });
  }, [previewTemplate, currentConfig.theme, previewDataset.shop]);

  useEffect(() => {
    if (!previewTemplate) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setPreviewTemplate(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [previewTemplate]);

  const handleConfirmApply = async (tpl: PlatformTemplateDTO) => {
    await onApplyTemplate(tpl);
    setConfirmApplyId(null);
    if (previewTemplate?.id === tpl.id) {
      setPreviewTemplate(null);
    }
  };

  return (
    <div className={styles.fieldStack}>
      <div className={styles.templateGrid}>
        {templates.map((tpl) => {
          const isActive = currentTemplateId === tpl.id;
          const isApplying = applyingTemplateId === tpl.id;
          const isConfirming = confirmApplyId === tpl.id;
          const theme = tpl.config.theme;
          const photoUrl = getTemplatePhoto(tpl);
          const shortNote = getTemplateShortNote(tpl);

          return (
            <article
              key={tpl.id}
              className={`${styles.templateCard} ${isActive ? styles.templateCardActive : ""}`}
            >
              <div className={styles.templatePhotoWrap}>
                <img
                  src={photoUrl}
                  alt={`Ảnh minh họa mẫu ${tpl.name}`}
                  className={styles.templatePhoto}
                  width={640}
                  height={400}
                />
                <div className={styles.templatePhotoBadges}>
                  <span
                    className={styles.templateArchetypeBadge}
                    style={{
                      backgroundColor: theme.accent_color,
                      color: theme.accent_text_color,
                    }}
                  >
                    {labelOf(ARCHETYPE_OPTIONS, tpl.archetype)}
                  </span>
                  {isActive && (
                    <span className={`${common.pill} ${common.pillPos}`}>Đang dùng</span>
                  )}
                </div>
              </div>

              <div className={styles.templateCardBody}>
                <div className={styles.templateCardHead}>
                  <div className={styles.templateCardTitleGroup}>
                    <h3 className={styles.templateCardName}>{tpl.name}</h3>
                    {shortNote && <p className={styles.templateCardNote}>{shortNote}</p>}
                  </div>
                  <span className={styles.presetDots} aria-label="Bảng màu mẫu">
                    <span
                      className={styles.presetDot}
                      style={{ backgroundColor: theme.bg_color }}
                      title={`Nền ${theme.bg_color}`}
                    />
                    <span
                      className={styles.presetDot}
                      style={{ backgroundColor: theme.surface_color }}
                      title={`Thẻ ${theme.surface_color}`}
                    />
                    <span
                      className={styles.presetDot}
                      style={{ backgroundColor: theme.text_color }}
                      title={`Chữ ${theme.text_color}`}
                    />
                    <span
                      className={styles.presetDot}
                      style={{ backgroundColor: theme.accent_color }}
                      title={`Nhấn ${theme.accent_color}`}
                    />
                  </span>
                </div>

                {isConfirming ? (
                  <div className={styles.confirmApplyBox}>
                    <p className={styles.confirmApplyText}>
                      Áp dụng mẫu <strong>{tpl.name}</strong> vào <strong>bản nháp</strong>? Trang
                      công khai giữ nguyên tới khi bạn bấm Xuất bản.
                    </p>
                    <div className={styles.confirmApplyBtns}>
                      <Button
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={Boolean(applyingTemplateId)}
                        onClick={() => setConfirmApplyId(null)}
                      >
                        Hủy
                      </Button>
                      <Button
                        variant="primary"
                        size="sm"
                        shape="app"
                        disabled={Boolean(applyingTemplateId)}
                        onClick={() => void handleConfirmApply(tpl)}
                      >
                        {isApplying ? "Đang áp dụng..." : "Xác nhận áp dụng nháp"}
                      </Button>
                    </div>
                  </div>
                ) : (
                  <div className={styles.templateCardActions}>
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      onClick={() => {
                        setPreviewTemplate(tpl);
                        setPreviewPage("home");
                        setPreviewDevice("1440");
                      }}
                    >
                      Xem trước
                    </Button>
                    <Button
                      variant={isActive ? "secondary" : "primary"}
                      size="sm"
                      shape="app"
                      disabled={Boolean(applyingTemplateId)}
                      onClick={() => setConfirmApplyId(tpl.id)}
                    >
                      {isApplying
                        ? "Đang áp dụng..."
                        : isActive
                          ? "Áp dụng lại mẫu gốc"
                          : "Áp dụng vào bản nháp"}
                    </Button>
                  </div>
                )}
              </div>
            </article>
          );
        })}
      </div>

      {previewTemplate && previewConfig && (
        <div
          className={styles.previewModalBackdrop}
          role="dialog"
          aria-modal="true"
          aria-label={`Xem trước mẫu ${previewTemplate.name}`}
          onClick={(e) => {
            if (e.target === e.currentTarget) setPreviewTemplate(null);
          }}
        >
          <div className={styles.previewModalPanel}>
            <div className={styles.previewModalHeader}>
              <div className={styles.previewModalMeta}>
                <strong className={styles.previewModalTitle}>{previewTemplate.name}</strong>
                <span className={styles.previewModalHint}>
                  Xem trước với dữ liệu gian hàng, chưa thay đổi site công khai
                </span>
              </div>
              <div className={styles.previewModalRight}>
                <Button
                  variant="primary"
                  size="sm"
                  shape="app"
                  disabled={Boolean(applyingTemplateId)}
                  onClick={() => void handleConfirmApply(previewTemplate)}
                >
                  {applyingTemplateId === previewTemplate.id
                    ? "Đang áp dụng..."
                    : "Áp dụng vào bản nháp"}
                </Button>
                <button
                  type="button"
                  className={styles.previewModalCloseBtn}
                  onClick={() => setPreviewTemplate(null)}
                  aria-label="Đóng cửa sổ xem trước"
                >
                  <IconClose size={16} />
                </button>
              </div>
            </div>

            <div className={styles.previewToolbar}>
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
                    onClick={() => setPreviewPage(tab.key)}
                  >
                    {tab.shortLabel}
                  </button>
                ))}
              </div>

              <div className={styles.viewportGroup} role="group" aria-label="Kích thước khung xem">
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

            <div className={styles.previewModalBody}>
              <SitePreviewFrame
                device={previewDevice}
                title={`Xem trước mẫu ${previewTemplate.name} (${previewDevice}px)`}
                resetScrollKey={`${previewTemplate.id}:${previewPage}:${previewDevice}`}
                viewportHeight={640}
                onEscape={() => setPreviewTemplate(null)}
              >
                <SitePreviewContent
                  config={previewConfig}
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
        </div>
      )}
    </div>
  );
}
