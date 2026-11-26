"use client";

import React, { useMemo, useState } from "react";
import { Check, Eye, X } from "lucide-react";
import { StatusBadge } from "@/components/app";
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
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cloneTemplateConfigForShop } from "@/features/site/templateCatalog";
import { sanitizeSafeImageUrl } from "@/features/site/templateValidator";
import { cn } from "@/lib/utils";
import type { PlatformTemplateDTO, SiteTemplateConfig, TemplatePageKey } from "./siteEditorTypes";
import { ARCHETYPE_OPTIONS } from "./siteEditorUtils";
import type { PreviewStorefrontDataset } from "./previewData";
import { PreviewToolbar } from "./PreviewToolbar";
import { SitePreviewContent } from "./SitePreviewContent";
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";

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

/** Crisp one-line summaries when template description is empty. */
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

function getTemplateDescription(tpl: PlatformTemplateDTO): string {
  return (tpl.description || "").trim() || TEMPLATE_SHORT_NOTES[tpl.id] || "";
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
  const [confirmTemplate, setConfirmTemplate] = useState<PlatformTemplateDTO | null>(null);

  const previewConfig = useMemo(() => {
    if (!previewTemplate) return null;
    return cloneTemplateConfigForShop(previewTemplate, {
      brandName: currentConfig.theme.brand_name || previewDataset.shop.name,
      tagline: currentConfig.theme.tagline || previewDataset.shop.description,
      logoUrl: currentConfig.theme.logo_url || "",
    });
  }, [previewTemplate, currentConfig.theme, previewDataset.shop]);

  const handleConfirmApply = async (tpl: PlatformTemplateDTO) => {
    await onApplyTemplate(tpl);
    setConfirmTemplate(null);
    if (previewTemplate?.id === tpl.id) {
      setPreviewTemplate(null);
    }
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        {templates.map((tpl) => {
          const isActive = currentTemplateId === tpl.id;
          const isApplying = applyingTemplateId === tpl.id;
          const theme = tpl.config.theme;
          const photoUrl = getTemplatePhoto(tpl);
          const description = getTemplateDescription(tpl);
          const features =
            tpl.tags && tpl.tags.length > 0
              ? tpl.tags
              : [labelOf(ARCHETYPE_OPTIONS, tpl.archetype)];

          return (
            <Card
              key={tpl.id}
              className={cn(
                "justify-between transition-shadow",
                isActive && "ring-2 ring-brand/60"
              )}
            >
              <CardHeader className="gap-2">
                <div className="flex min-h-6 items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-1.5">
                    {isActive ? <StatusBadge tone="info">Đang dùng</StatusBadge> : null}
                    <Badge variant="neutral" className="rounded-sm font-medium">
                      {labelOf(ARCHETYPE_OPTIONS, tpl.archetype)}
                    </Badge>
                  </div>
                  <span
                    className="inline-flex shrink-0 items-center gap-1"
                    aria-label="Bảng màu mẫu"
                  >
                    <span
                      className="size-3 rounded-full border border-input/60"
                      style={{ backgroundColor: theme.bg_color }}
                      title={`Nền ${theme.bg_color}`}
                    />
                    <span
                      className="size-3 rounded-full border border-input/60"
                      style={{ backgroundColor: theme.surface_color }}
                      title={`Thẻ ${theme.surface_color}`}
                    />
                    <span
                      className="size-3 rounded-full border border-input/60"
                      style={{ backgroundColor: theme.text_color }}
                      title={`Chữ ${theme.text_color}`}
                    />
                    <span
                      className="size-3 rounded-full border border-input/60"
                      style={{ backgroundColor: theme.accent_color }}
                      title={`Nhấn ${theme.accent_color}`}
                    />
                  </span>
                </div>

                <h2 className="text-2xl leading-8 font-semibold tracking-tight text-foreground">
                  {tpl.name}
                </h2>
                <CardDescription className="line-clamp-2 min-h-9">{description}</CardDescription>
              </CardHeader>

              <CardContent className="flex flex-1 flex-col gap-4">
                <div className="overflow-hidden rounded-lg border border-border bg-muted">
                  <img
                    src={photoUrl}
                    alt={`Ảnh minh họa mẫu ${tpl.name}`}
                    width={640}
                    height={400}
                    className="aspect-[16/10] w-full object-cover"
                  />
                </div>

                <div className="space-y-2">
                  <Button
                    type="button"
                    className="min-h-10 w-full md:min-h-9"
                    variant={isActive ? "outline" : "default"}
                    disabled={Boolean(applyingTemplateId)}
                    onClick={() => setConfirmTemplate(tpl)}
                  >
                    {isApplying
                      ? "Đang áp dụng..."
                      : isActive
                        ? "Áp dụng lại mẫu gốc"
                        : "Dùng mẫu này"}
                  </Button>
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    className="min-h-10 w-full md:min-h-8"
                    onClick={() => {
                      setPreviewTemplate(tpl);
                      setPreviewPage("home");
                      setPreviewDevice("1440");
                    }}
                  >
                    <Eye className="size-4" aria-hidden="true" />
                    <span>Xem trước</span>
                  </Button>
                </div>

                <div className="mt-auto space-y-2 border-t border-border pt-3">
                  <div className="text-xs font-semibold text-foreground">Đặc điểm bố cục</div>
                  <ul className="space-y-1.5 text-[13px] text-muted-foreground">
                    {features.map((feat) => (
                      <li key={feat} className="flex items-start gap-2">
                        <Check className="mt-0.5 size-4 shrink-0 text-brand" aria-hidden="true" />
                        <span>{feat}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      {/* Large Preview Dialog */}
      <Dialog
        open={Boolean(previewTemplate && previewConfig)}
        onOpenChange={(open) => {
          if (!open) setPreviewTemplate(null);
        }}
      >
        <DialogContent
          showCloseButton={false}
          className="flex h-[90vh] max-h-[880px] w-[calc(100%-1.5rem)] max-w-[min(1280px,calc(100vw-1.5rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-[min(1280px,calc(100vw-2rem))]"
        >
          {previewTemplate && previewConfig ? (
            <>
              <DialogHeader className="flex flex-row flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3 text-left sm:px-5">
                <div className="min-w-0 flex-1 space-y-0.5">
                  <div className="flex flex-wrap items-center gap-2">
                    <DialogTitle className="truncate text-base font-semibold text-foreground">
                      {previewTemplate.name}
                    </DialogTitle>
                    {currentTemplateId === previewTemplate.id ? (
                      <StatusBadge tone="info">Đang dùng</StatusBadge>
                    ) : null}
                  </div>
                  <DialogDescription className="text-[13px]">
                    Xem trước với dữ liệu gian hàng, chưa thay đổi site công khai
                  </DialogDescription>
                </div>

                <div className="flex shrink-0 items-center gap-2">
                  <Button
                    type="button"
                    size="sm"
                    disabled={Boolean(applyingTemplateId)}
                    onClick={() => setConfirmTemplate(previewTemplate)}
                  >
                    {applyingTemplateId === previewTemplate.id ? "Đang áp dụng..." : "Dùng mẫu này"}
                  </Button>
                  <DialogClose asChild>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-sm"
                      aria-label="Đóng cửa sổ xem trước"
                    >
                      <X className="size-4" aria-hidden="true" />
                    </Button>
                  </DialogClose>
                </div>
              </DialogHeader>

              <PreviewToolbar
                activePage={previewPage}
                onPageChange={setPreviewPage}
                device={previewDevice}
                onDeviceChange={setPreviewDevice}
              />

              <div className="min-h-0 flex-1 overflow-y-auto bg-muted/60 p-4">
                <SitePreviewFrame
                  device={previewDevice}
                  title={`Xem trước mẫu ${previewTemplate.name} (${previewDevice}px)`}
                  resetScrollKey={`${previewTemplate.id}:${previewPage}:${previewDevice}`}
                  viewportHeight={620}
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
            </>
          ) : null}
        </DialogContent>
      </Dialog>

      {/* Confirm Apply Template to Draft AlertDialog */}
      <AlertDialog
        open={confirmTemplate !== null}
        onOpenChange={(open) => {
          if (!open && !applyingTemplateId) setConfirmTemplate(null);
        }}
      >
        <AlertDialogContent>
          {confirmTemplate ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Áp dụng mẫu {confirmTemplate.name}?</AlertDialogTitle>
                <AlertDialogDescription>
                  Áp dụng mẫu <strong>{confirmTemplate.name}</strong> vào <strong>bản nháp</strong>?
                  Trang công khai của cửa hàng vẫn giữ nguyên cho tới khi bạn bấm Xuất bản.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel disabled={Boolean(applyingTemplateId)}>Hủy</AlertDialogCancel>
                <AlertDialogAction
                  disabled={Boolean(applyingTemplateId)}
                  onClick={(e) => {
                    e.preventDefault();
                    void handleConfirmApply(confirmTemplate);
                  }}
                >
                  {applyingTemplateId === confirmTemplate.id
                    ? "Đang áp dụng..."
                    : "Xác nhận áp dụng nháp"}
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : null}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
