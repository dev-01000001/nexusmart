"use client";

import React, { useEffect, useMemo, useState } from "react";
import { Button, IconClose } from "@/components";
import { cloneTemplateConfigForShop } from "@/features/site/templateCatalog";
import common from "@/features/seller/SellerCommon.module.css";
import type {
  PlatformTemplateDTO,
  SiteTemplateConfig,
  TemplatePageKey,
} from "./siteEditorTypes";
import {
  ARCHETYPE_OPTIONS,
  CART_LAYOUT_OPTIONS,
  CATALOG_LAYOUT_OPTIONS,
  HEADER_STYLE_OPTIONS,
  INFO_LAYOUT_OPTIONS,
  PDP_LAYOUT_OPTIONS,
  TEMPLATE_PAGE_TABS,
} from "./siteEditorUtils";
import type { PreviewStorefrontDataset } from "./previewData";
import { SitePreviewContent } from "./SitePreviewContent";
import styles from "./SellerSiteEditor.module.css";

export interface TemplateGalleryTabProps {
  templates: PlatformTemplateDTO[];
  currentTemplateId: string;
  currentConfig: SiteTemplateConfig;
  previewDataset: PreviewStorefrontDataset;
  platformOrigin: string;
  applyingTemplateId: string | null;
  onApplyTemplate: (template: PlatformTemplateDTO) => Promise<void>;
}

function labelOf<T extends string>(
  options: ReadonlyArray<{ value: T; label: string }>,
  val: T
): string {
  return options.find((o) => o.value === val)?.label ?? val;
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
  const [selectedCategory, setSelectedCategory] = useState<string>("all");
  const [previewTemplate, setPreviewTemplate] = useState<PlatformTemplateDTO | null>(null);
  const [previewPage, setPreviewPage] = useState<TemplatePageKey>("home");
  const [previewViewport, setPreviewViewport] = useState<"desktop" | "mobile">("desktop");
  const [confirmApplyId, setConfirmApplyId] = useState<string | null>(null);

  const categories = useMemo(() => {
    const counts = new Map<string, number>();
    for (const tpl of templates) {
      counts.set(tpl.category, (counts.get(tpl.category) ?? 0) + 1);
    }
    return Array.from(counts.entries()).map(([name, count]) => ({ name, count }));
  }, [templates]);

  const filteredTemplates = useMemo(() => {
    if (selectedCategory === "all") return templates;
    return templates.filter((t) => t.category === selectedCategory);
  }, [templates, selectedCategory]);

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
      <section className={common.card} aria-label="Thư viện mẫu giao diện cửa hàng">
        <div className={common.cardHeader}>
          <div>
            <h2 className={common.cardTitle}>
              Thư viện mẫu giao diện ({templates.length} mẫu bố cục riêng biệt)
            </h2>
            <p className={common.cardMeta}>
              Mỗi mẫu thiết kế cấu trúc riêng cho cả 5 trang: Trang chủ, Danh sách sản phẩm, Chi
              tiết sản phẩm, Giỏ hàng và Trang thông tin. Áp dụng mẫu chỉ cập nhật bản nháp và không
              ảnh hưởng trang công khai cho đến khi bạn Xuất bản.
            </p>
          </div>
        </div>

        <div className={styles.categoryFilterBar} role="group" aria-label="Lọc mẫu theo ngành hàng">
          <button
            type="button"
            className={`${styles.filterPill} ${selectedCategory === "all" ? styles.filterPillActive : ""}`}
            onClick={() => setSelectedCategory("all")}
          >
            Tất cả ({templates.length})
          </button>
          {categories.map((cat) => (
            <button
              key={cat.name}
              type="button"
              className={`${styles.filterPill} ${selectedCategory === cat.name ? styles.filterPillActive : ""}`}
              onClick={() => setSelectedCategory(cat.name)}
            >
              {cat.name} ({cat.count})
            </button>
          ))}
        </div>

        <div className={styles.templateGrid}>
          {filteredTemplates.map((tpl) => {
            const isActive = currentTemplateId === tpl.id;
            const isApplying = applyingTemplateId === tpl.id;
            const isConfirming = confirmApplyId === tpl.id;
            const theme = tpl.config.theme;

            return (
              <article
                key={tpl.id}
                className={`${styles.templateCard} ${isActive ? styles.templateCardActive : ""}`}
              >
                {/* Miniature visual token & layout header preview */}
                <div
                  className={styles.templatePreviewStrip}
                  style={{
                    backgroundColor: theme.bg_color,
                    color: theme.text_color,
                  }}
                >
                  <div className={styles.templateStripTop}>
                    <span
                      className={styles.templateStripBrand}
                      style={{ color: theme.text_color }}
                    >
                      {tpl.name}
                    </span>
                    <span
                      className={styles.templateStripCta}
                      style={{
                        backgroundColor: theme.accent_color,
                        color: theme.accent_text_color,
                      }}
                    >
                      {labelOf(ARCHETYPE_OPTIONS, tpl.archetype)}
                    </span>
                  </div>
                  <div
                    className={styles.templateStripMock}
                    style={{ backgroundColor: theme.surface_color }}
                  >
                    <span className={styles.templateStripCols}>
                      Danh mục: {tpl.config.pages.catalog.columns} cột ·{" "}
                      {tpl.config.sections.length} khối trang chủ
                    </span>
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
                </div>

                <div className={styles.templateCardBody}>
                  <div className={styles.templateCardHead}>
                    <div>
                      <h3 className={styles.templateCardName}>{tpl.name}</h3>
                      <span className={styles.templateCardCategory}>{tpl.category}</span>
                    </div>
                    {isActive && (
                      <span className={`${common.pill} ${common.pillPos}`}>Đang dùng</span>
                    )}
                  </div>

                  <p className={styles.templateCardDesc}>{tpl.description}</p>

                  <dl className={styles.templateSpecList}>
                    <div className={styles.templateSpecRow}>
                      <dt>Đầu trang</dt>
                      <dd>{labelOf(HEADER_STYLE_OPTIONS, tpl.config.layout.header_style)}</dd>
                    </div>
                    <div className={styles.templateSpecRow}>
                      <dt>Danh sách</dt>
                      <dd>
                        {labelOf(CATALOG_LAYOUT_OPTIONS, tpl.config.pages.catalog.layout)} (
                        {tpl.config.pages.catalog.columns} cột)
                      </dd>
                    </div>
                    <div className={styles.templateSpecRow}>
                      <dt>Chi tiết SP</dt>
                      <dd>{labelOf(PDP_LAYOUT_OPTIONS, tpl.config.pages.pdp.layout)}</dd>
                    </div>
                    <div className={styles.templateSpecRow}>
                      <dt>Giỏ hàng</dt>
                      <dd>{labelOf(CART_LAYOUT_OPTIONS, tpl.config.pages.cart.layout)}</dd>
                    </div>
                    <div className={styles.templateSpecRow}>
                      <dt>Thông tin</dt>
                      <dd>{labelOf(INFO_LAYOUT_OPTIONS, tpl.config.pages.info.layout)}</dd>
                    </div>
                  </dl>

                  {tpl.tags.length > 0 && (
                    <div className={styles.templateTagRow}>
                      {tpl.tags.map((tag) => (
                        <span key={tag} className={styles.templateTag}>
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}

                  {isConfirming ? (
                    <div className={styles.confirmApplyBox}>
                      <p className={styles.confirmApplyText}>
                        Áp dụng mẫu <strong>{tpl.name}</strong> chỉ cập nhật{" "}
                        <strong>bản nháp</strong> (giữ tên thương hiệu của bạn); trang đang công
                        khai không bị ghi đè cho tới khi bạn bấm Xuất bản.
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
                          setPreviewViewport("desktop");
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
      </section>

      {/* Interactive 5-page Template Preview Modal */}
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
                <strong className={styles.previewModalTitle}>
                  Xem trước: {previewTemplate.name}
                </strong>
                <span className={styles.previewModalHint}>
                  Hiển thị trực tiếp với tên thương hiệu &amp; sản phẩm của gian hàng (chưa thay đổi
                  site công khai)
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
              <div className={styles.pageSwitcherGroup} role="tablist" aria-label="Chọn trang xem trước">
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
                <button
                  type="button"
                  className={`${styles.viewportBtn} ${
                    previewViewport === "desktop" ? styles.viewportBtnActive : ""
                  }`}
                  onClick={() => setPreviewViewport("desktop")}
                >
                  Máy tính
                </button>
                <button
                  type="button"
                  className={`${styles.viewportBtn} ${
                    previewViewport === "mobile" ? styles.viewportBtnActive : ""
                  }`}
                  onClick={() => setPreviewViewport("mobile")}
                >
                  Di động 390px
                </button>
              </div>
            </div>

            <div className={styles.previewModalBody}>
              <div
                className={
                  previewViewport === "mobile"
                    ? styles.previewCanvasMobile
                    : styles.previewCanvasDesktop
                }
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
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
