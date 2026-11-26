"use client";

import React, { useState } from "react";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { GwCategory, GwSellerProduct } from "@/features/seller/sellerApiTypes";
import type {
  PolicyItem,
  SectionSettings,
  SiteBlockType,
  SiteSectionBlock,
  SiteSectionV2,
} from "./siteEditorTypes";
import {
  BLOCK_TYPE_META,
  BLOCK_TYPE_ORDER,
  MAX_BLOCKS_PER_SECTION,
  QUICK_MEDIA_ASSETS,
  SECTION_PADDING_OPTIONS,
  SECTION_SURFACE_OPTIONS,
  SECTION_V2_TYPE_META,
  SECTION_VARIANTS_BY_TYPE,
  SECTION_VARIANT_LABELS,
  checkUnsafeText,
  createBlockTemplate,
  validateImageUrlStrict,
  validateSafeHrefStrict,
} from "./siteEditorUtils";

export interface SectionEditorPanelProps {
  section: SiteSectionV2 | null;
  products: GwSellerProduct[];
  categories: GwCategory[];
  onChange: (updated: SiteSectionV2) => void;
}

const DEFAULT_SELECT_VALUE = "__default__";

const COLLECTION_SUGGESTIONS: ReadonlyArray<{ slug: string; label: string }> = [
  { slug: "co-ban-hang-ngay", label: "Cơ bản hằng ngày" },
  { slug: "cong-so-gon-gang", label: "Công sở gọn gàng" },
  { slug: "chay-bo-tap-luyen", label: "Chạy bộ & Tập luyện" },
  { slug: "soi-xanh-nexus", label: "Sợi Xanh Nexus" },
];

const INTERNAL_HREF_SUGGESTIONS: ReadonlyArray<{ href: string; label: string }> = [
  { href: "/san-pham", label: "Tất cả sản phẩm" },
  { href: "/flash-sale", label: "Flash sale của cửa hàng" },
  { href: "/voucher", label: "Mã giảm giá" },
  { href: "/chinh-sach", label: "Trang chính sách" },
  { href: "/gioi-thieu", label: "Trang giới thiệu" },
];

export function SectionEditorPanel({
  section,
  products,
  categories,
  onChange,
}: SectionEditorPanelProps) {
  const [newBlockType, setNewBlockType] = useState<SiteBlockType>("badge");

  if (!section) {
    return (
      <div className="rounded-lg border border-border bg-muted/40 p-3.5 text-[13px] text-muted-foreground">
        Chọn một khối ở danh sách phía trên để chỉnh sửa nội dung, biến thể và các block con, hoặc
        thêm khối mới.
      </div>
    );
  }

  const meta = SECTION_V2_TYPE_META[section.type];
  const allowedVariants = SECTION_VARIANTS_BY_TYPE[section.type] ?? [""];
  const settings: SectionSettings = section.settings ?? {};
  const blocks: SiteSectionBlock[] = section.blocks ?? [];

  const updateSettings = (patch: Partial<SectionSettings>) => {
    onChange({
      ...section,
      settings: { ...settings, ...patch },
    });
  };

  const updateBlocks = (updater: (prev: SiteSectionBlock[]) => SiteSectionBlock[]) => {
    onChange({
      ...section,
      blocks: updater(blocks),
    });
  };

  const handleAddBlock = () => {
    if (blocks.length >= MAX_BLOCKS_PER_SECTION) return;
    const created = createBlockTemplate(newBlockType, blocks.length);
    updateBlocks((prev) => [...prev, created]);
  };

  const handleMoveBlock = (idx: number, delta: -1 | 1) => {
    const target = idx + delta;
    if (target < 0 || target >= blocks.length) return;
    updateBlocks((prev) => {
      const next = [...prev];
      const [moved] = next.splice(idx, 1);
      next.splice(target, 0, moved);
      return next;
    });
  };

  const handleDeleteBlock = (blockId: string) => {
    updateBlocks((prev) => prev.filter((b) => b.id !== blockId));
  };

  const renderUnsafeWarning = (val: string | undefined) => {
    if (!val) return null;
    const reason = checkUnsafeText(val);
    if (!reason) return null;
    return <FieldError className="text-xs">Nội dung bị chặn vì {reason}.</FieldError>;
  };

  const renderUrlField = (
    id: string,
    label: string,
    value: string,
    onUpdate: (next: string) => void,
    kind: "image" | "href",
    required = false
  ) => {
    const err =
      kind === "image"
        ? validateImageUrlStrict(value, required)
        : required && !value.trim()
          ? "Đường dẫn là bắt buộc."
          : validateSafeHrefStrict(value);

    return (
      <Field className="gap-1.5">
        <FieldLabel htmlFor={id} className="text-xs font-medium text-foreground">
          {label}
        </FieldLabel>
        <Input
          id={id}
          type="text"
          value={value}
          onChange={(e) => onUpdate(e.target.value)}
          placeholder={
            kind === "image" ? "/banners/campaign-everyday.jpg hoặc https://..." : "/san-pham"
          }
          maxLength={500}
          aria-invalid={Boolean(err)}
          className="h-8 bg-card text-[13px]"
        />
        {err ? (
          <FieldError className="text-xs">{err}</FieldError>
        ) : (
          <div className="flex flex-wrap gap-1.5 pt-0.5">
            {(kind === "image" ? QUICK_MEDIA_ASSETS : INTERNAL_HREF_SUGGESTIONS).map((item) => {
              const targetVal = "url" in item ? item.url : item.href;
              const active = value.trim() === targetVal;
              return (
                <button
                  key={targetVal}
                  type="button"
                  onClick={() => onUpdate(targetVal)}
                  className={cn(
                    "inline-flex min-h-7 items-center rounded-sm border px-2 py-0.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    active
                      ? "border-brand/40 bg-badge-info-bg text-badge-info-ink"
                      : "border-border bg-muted/60 text-muted-foreground hover:bg-secondary hover:text-foreground"
                  )}
                >
                  {item.label}
                </button>
              );
            })}
          </div>
        )}
      </Field>
    );
  };

  const renderBlockFields = (blk: SiteSectionBlock, idx: number) => {
    const replaceBlock = (updated: SiteSectionBlock) => {
      updateBlocks((prev) => prev.map((b, i) => (i === idx ? updated : b)));
    };

    switch (blk.type) {
      case "heading":
        return (
          <div className="space-y-2.5">
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-hd-${idx}`} className="text-xs font-medium">
                Nội dung tiêu đề (tối đa 120 ký tự)
              </FieldLabel>
              <Input
                id={`blk-hd-${idx}`}
                type="text"
                value={blk.props.text}
                maxLength={120}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, text: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.text)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-hdl-${idx}`} className="text-xs font-medium">
                Cấp tiêu đề
              </FieldLabel>
              <Select
                value={blk.props.level || "h3"}
                onValueChange={(v) =>
                  replaceBlock({ ...blk, props: { ...blk.props, level: v as "h2" | "h3" } })
                }
              >
                <SelectTrigger id={`blk-hdl-${idx}`} size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="h2">Tiêu đề lớn (H2)</SelectItem>
                  <SelectItem value="h3">Tiêu đề phụ (H3)</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>
        );

      case "paragraph":
        return (
          <Field className="gap-1.5">
            <FieldLabel htmlFor={`blk-p-${idx}`} className="text-xs font-medium">
              Đoạn văn bản (tối đa 800 ký tự)
            </FieldLabel>
            <Textarea
              id={`blk-p-${idx}`}
              rows={3}
              value={blk.props.text}
              maxLength={800}
              onChange={(e) =>
                replaceBlock({ ...blk, props: { ...blk.props, text: e.target.value } })
              }
              className="bg-card text-[13px]"
            />
            {renderUnsafeWarning(blk.props.text)}
          </Field>
        );

      case "badge":
        return (
          <div className="space-y-2.5">
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-bdg-${idx}`} className="text-xs font-medium">
                Nhãn hiển thị (tối đa 48 ký tự)
              </FieldLabel>
              <Input
                id={`blk-bdg-${idx}`}
                type="text"
                value={blk.props.label}
                maxLength={48}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, label: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.label)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-bdgt-${idx}`} className="text-xs font-medium">
                Sắc thái nhãn
              </FieldLabel>
              <Select
                value={blk.props.tone || "neutral"}
                onValueChange={(v) =>
                  replaceBlock({
                    ...blk,
                    props: { ...blk.props, tone: v as "neutral" | "accent" | "band" },
                  })
                }
              >
                <SelectTrigger id={`blk-bdgt-${idx}`} size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="neutral">Trung tính</SelectItem>
                  <SelectItem value="accent">Màu nhấn</SelectItem>
                  <SelectItem value="band">Dải tương phản</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>
        );

      case "cta":
        return (
          <div className="space-y-2.5">
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-cta-lbl-${idx}`} className="text-xs font-medium">
                Nhãn nút (tối đa 48 ký tự)
              </FieldLabel>
              <Input
                id={`blk-cta-lbl-${idx}`}
                type="text"
                value={blk.props.label}
                maxLength={48}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, label: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.label)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-cta-st-${idx}`} className="text-xs font-medium">
                Kiểu nút
              </FieldLabel>
              <Select
                value={blk.props.style || "primary"}
                onValueChange={(v) =>
                  replaceBlock({
                    ...blk,
                    props: { ...blk.props, style: v as "primary" | "secondary" | "link" },
                  })
                }
              >
                <SelectTrigger id={`blk-cta-st-${idx}`} size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="primary">Nút chính</SelectItem>
                  <SelectItem value="secondary">Nút phụ viền</SelectItem>
                  <SelectItem value="link">Liên kết chữ</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {renderUrlField(
              `blk-cta-href-${idx}`,
              "Đường dẫn liên kết (bắt buộc)",
              blk.props.href,
              (href) => replaceBlock({ ...blk, props: { ...blk.props, href } }),
              "href",
              true
            )}
          </div>
        );

      case "image":
        return (
          <div className="space-y-2.5">
            {renderUrlField(
              `blk-img-url-${idx}`,
              "Đường dẫn ảnh (chặn SVG & IP nội bộ)",
              blk.props.image_url,
              (image_url) => replaceBlock({ ...blk, props: { ...blk.props, image_url } }),
              "image",
              true
            )}
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-img-alt-${idx}`} className="text-xs font-medium">
                Mô tả ảnh (alt)
              </FieldLabel>
              <Input
                id={`blk-img-alt-${idx}`}
                type="text"
                value={blk.props.alt ?? ""}
                maxLength={140}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, alt: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.alt)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-img-asp-${idx}`} className="text-xs font-medium">
                Tỷ lệ khung ảnh
              </FieldLabel>
              <Select
                value={blk.props.aspect || "4:3"}
                onValueChange={(v) =>
                  replaceBlock({
                    ...blk,
                    props: { ...blk.props, aspect: v as "16:9" | "4:3" | "3:4" | "1:1" },
                  })
                }
              >
                <SelectTrigger id={`blk-img-asp-${idx}`} size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="16:9">Ngang rộng (16:9)</SelectItem>
                  <SelectItem value="4:3">Tiêu chuẩn (4:3)</SelectItem>
                  <SelectItem value="3:4">Ảnh đứng Lookbook (3:4)</SelectItem>
                  <SelectItem value="1:1">Vuông (1:1)</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-img-cap-${idx}`} className="text-xs font-medium">
                Chú thích bên dưới ảnh (tùy chọn)
              </FieldLabel>
              <Input
                id={`blk-img-cap-${idx}`}
                type="text"
                value={blk.props.caption ?? ""}
                maxLength={200}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, caption: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.caption)}
            </Field>
          </div>
        );

      case "stat":
        return (
          <div className="space-y-2.5">
            <div className="grid grid-cols-2 gap-2.5">
              <Field className="gap-1.5">
                <FieldLabel htmlFor={`blk-st-val-${idx}`} className="text-xs font-medium">
                  Giá trị chỉ số
                </FieldLabel>
                <Input
                  id={`blk-st-val-${idx}`}
                  type="text"
                  value={blk.props.value}
                  maxLength={32}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, value: e.target.value } })
                  }
                  placeholder="220gsm"
                  className="h-8 bg-card text-[13px]"
                />
                {renderUnsafeWarning(blk.props.value)}
              </Field>
              <Field className="gap-1.5">
                <FieldLabel htmlFor={`blk-st-lbl-${idx}`} className="text-xs font-medium">
                  Nhãn chỉ số
                </FieldLabel>
                <Input
                  id={`blk-st-lbl-${idx}`}
                  type="text"
                  value={blk.props.label}
                  maxLength={60}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, label: e.target.value } })
                  }
                  placeholder="Định lượng vải"
                  className="h-8 bg-card text-[13px]"
                />
                {renderUnsafeWarning(blk.props.label)}
              </Field>
            </div>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-st-note-${idx}`} className="text-xs font-medium">
                Ghi chú giải thích (tùy chọn)
              </FieldLabel>
              <Input
                id={`blk-st-note-${idx}`}
                type="text"
                value={blk.props.note ?? ""}
                maxLength={140}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, note: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.note)}
            </Field>
          </div>
        );

      case "policy":
        return (
          <div className="space-y-2.5">
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-pol-h-${idx}`} className="text-xs font-medium">
                Tiêu đề cam kết (tối đa 80 ký tự)
              </FieldLabel>
              <Input
                id={`blk-pol-h-${idx}`}
                type="text"
                value={blk.props.heading}
                maxLength={80}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, heading: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.heading)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-pol-d-${idx}`} className="text-xs font-medium">
                Chi tiết cam kết (tối đa 400 ký tự)
              </FieldLabel>
              <Textarea
                id={`blk-pol-d-${idx}`}
                rows={2}
                value={blk.props.detail}
                maxLength={400}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, detail: e.target.value } })
                }
                className="bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.detail)}
            </Field>
          </div>
        );

      case "quote":
        return (
          <div className="space-y-2.5">
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-qt-b-${idx}`} className="text-xs font-medium">
                Nội dung trích dẫn (tối đa 400 ký tự)
              </FieldLabel>
              <Textarea
                id={`blk-qt-b-${idx}`}
                rows={2}
                value={blk.props.body}
                maxLength={400}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, body: e.target.value } })
                }
                className="bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.body)}
            </Field>
            <div className="grid grid-cols-2 gap-2.5">
              <Field className="gap-1.5">
                <FieldLabel htmlFor={`blk-qt-a-${idx}`} className="text-xs font-medium">
                  Tác giả / Khách hàng
                </FieldLabel>
                <Input
                  id={`blk-qt-a-${idx}`}
                  type="text"
                  value={blk.props.author}
                  maxLength={80}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, author: e.target.value } })
                  }
                  className="h-8 bg-card text-[13px]"
                />
                {renderUnsafeWarning(blk.props.author)}
              </Field>
              <Field className="gap-1.5">
                <FieldLabel htmlFor={`blk-qt-r-${idx}`} className="text-xs font-medium">
                  Vai trò (tùy chọn)
                </FieldLabel>
                <Input
                  id={`blk-qt-r-${idx}`}
                  type="text"
                  value={blk.props.role ?? ""}
                  maxLength={80}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, role: e.target.value } })
                  }
                  className="h-8 bg-card text-[13px]"
                />
                {renderUnsafeWarning(blk.props.role)}
              </Field>
            </div>
          </div>
        );

      case "feature":
        return (
          <div className="space-y-2.5">
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-ft-t-${idx}`} className="text-xs font-medium">
                Tiêu đề điểm nổi bật (tối đa 80 ký tự)
              </FieldLabel>
              <Input
                id={`blk-ft-t-${idx}`}
                type="text"
                value={blk.props.title}
                maxLength={80}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.title)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-ft-tag-${idx}`} className="text-xs font-medium">
                Nhãn ngắn (tùy chọn)
              </FieldLabel>
              <Input
                id={`blk-ft-tag-${idx}`}
                type="text"
                value={blk.props.tag ?? ""}
                maxLength={40}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, tag: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.tag)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor={`blk-ft-d-${idx}`} className="text-xs font-medium">
                Mô tả chi tiết (tối đa 300 ký tự)
              </FieldLabel>
              <Textarea
                id={`blk-ft-d-${idx}`}
                rows={2}
                value={blk.props.detail}
                maxLength={300}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, detail: e.target.value } })
                }
                className="bg-card text-[13px]"
              />
              {renderUnsafeWarning(blk.props.detail)}
            </Field>
          </div>
        );
    }
  };

  return (
    <div className="space-y-4 rounded-lg border border-border bg-card p-3.5">
      <div className="flex items-center justify-between gap-2 border-b border-border pb-3">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold text-foreground">{meta.label}</h3>
          <p className="text-xs text-muted-foreground">{section.id}</p>
        </div>
        <label className="inline-flex shrink-0 cursor-pointer items-center gap-2 text-xs font-medium text-foreground">
          <Switch
            size="sm"
            checked={section.visible}
            onCheckedChange={(checked) => onChange({ ...section, visible: checked })}
            aria-label={`Hiển thị khối ${meta.label}`}
          />
          <span>{section.visible ? "Hiển thị" : "Đang ẩn"}</span>
        </label>
      </div>

      {/* Section Variant & Layout Settings */}
      <div className="space-y-2.5 rounded-lg border border-border bg-muted/40 p-3">
        <div className="text-xs font-semibold text-foreground">Biến thể &amp; Bố cục khối</div>
        <div className="space-y-2.5">
          <Field className="gap-1">
            <FieldLabel htmlFor="sec-variant" className="text-xs font-medium">
              Biến thể
            </FieldLabel>
            <Select
              value={section.variant || DEFAULT_SELECT_VALUE}
              onValueChange={(v) =>
                onChange({ ...section, variant: v === DEFAULT_SELECT_VALUE ? "" : v })
              }
            >
              <SelectTrigger id="sec-variant" size="sm" className="w-full min-w-0 bg-card">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {allowedVariants.map((v) => (
                  <SelectItem key={v || "default"} value={v || DEFAULT_SELECT_VALUE}>
                    {SECTION_VARIANT_LABELS[v] ?? v}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>

          <Field className="gap-1">
            <FieldLabel htmlFor="sec-surface" className="text-xs font-medium">
              Bề mặt nền
            </FieldLabel>
            <Select
              value={settings.surface || DEFAULT_SELECT_VALUE}
              onValueChange={(v) =>
                updateSettings({
                  surface: (v === DEFAULT_SELECT_VALUE ? "" : v) as SectionSettings["surface"],
                })
              }
            >
              <SelectTrigger id="sec-surface" size="sm" className="w-full min-w-0 bg-card">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SECTION_SURFACE_OPTIONS.map((opt) => (
                  <SelectItem
                    key={opt.value || "default"}
                    value={opt.value || DEFAULT_SELECT_VALUE}
                  >
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>

          <div className="grid grid-cols-2 gap-2.5">
            <Field className="min-w-0 gap-1">
              <FieldLabel htmlFor="sec-padding" className="text-xs font-medium">
                Khoảng đệm dọc
              </FieldLabel>
              <Select
                value={settings.padding || DEFAULT_SELECT_VALUE}
                onValueChange={(v) =>
                  updateSettings({
                    padding: (v === DEFAULT_SELECT_VALUE ? "" : v) as SectionSettings["padding"],
                  })
                }
              >
                <SelectTrigger id="sec-padding" size="sm" className="w-full min-w-0 bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SECTION_PADDING_OPTIONS.map((opt) => (
                    <SelectItem
                      key={opt.value || "default"}
                      value={opt.value || DEFAULT_SELECT_VALUE}
                    >
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>

            <Field className="min-w-0 gap-1">
              <FieldLabel htmlFor="sec-align" className="text-xs font-medium">
                Căn lề tiêu đề
              </FieldLabel>
              <Select
                value={settings.align || DEFAULT_SELECT_VALUE}
                onValueChange={(v) =>
                  updateSettings({
                    align: (v === DEFAULT_SELECT_VALUE ? "" : v) as "" | "left" | "center",
                  })
                }
              >
                <SelectTrigger id="sec-align" size="sm" className="w-full min-w-0 bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={DEFAULT_SELECT_VALUE}>Theo mặc định</SelectItem>
                  <SelectItem value="left">Căn trái</SelectItem>
                  <SelectItem value="center">Căn giữa</SelectItem>
                </SelectContent>
              </Select>
            </Field>

            <Field className="min-w-0 gap-1">
              <FieldLabel htmlFor="sec-columns" className="text-xs font-medium">
                Số cột (0–5)
              </FieldLabel>
              <Input
                id="sec-columns"
                type="number"
                min={0}
                max={5}
                value={settings.columns ?? 0}
                onChange={(e) =>
                  updateSettings({
                    columns: Math.max(0, Math.min(5, Math.trunc(Number(e.target.value) || 0))),
                  })
                }
                className="h-8 bg-card text-[13px] tabular-nums"
              />
            </Field>

            <Field className="min-w-0 gap-1">
              <FieldLabel htmlFor="sec-max-items" className="text-xs font-medium">
                Giới hạn mục (0–24)
              </FieldLabel>
              <Input
                id="sec-max-items"
                type="number"
                min={0}
                max={24}
                value={settings.max_items ?? 0}
                onChange={(e) =>
                  updateSettings({
                    max_items: Math.max(0, Math.min(24, Math.trunc(Number(e.target.value) || 0))),
                  })
                }
                className="h-8 bg-card text-[13px] tabular-nums"
              />
            </Field>
          </div>
        </div>
      </div>

      {/* Section-specific props */}
      <div className="space-y-3">
        {section.type === "hero" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-hero-kicker" className="text-xs font-medium">
                  Nhãn ngắn phía trên tiêu đề (tùy chọn)
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {(section.props.eyebrow ?? "").length}/60
                </span>
              </div>
              <Input
                id="sec-hero-kicker"
                type="text"
                value={section.props.eyebrow ?? ""}
                maxLength={60}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, eyebrow: e.target.value },
                  })
                }
                placeholder="Ví dụ: Xưởng dệt Hải Phòng"
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.eyebrow)}
            </Field>

            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-hero-title" className="text-xs font-medium">
                  Tiêu đề chính
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-hero-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, title: e.target.value },
                  })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-hero-sub" className="text-xs font-medium">
                  Mô tả ngắn
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {(section.props.subtitle ?? "").length}/400
                </span>
              </div>
              <Textarea
                id="sec-hero-sub"
                rows={2}
                value={section.props.subtitle ?? ""}
                maxLength={400}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, subtitle: e.target.value },
                  })
                }
                className="bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>

            {renderUrlField(
              "sec-hero-img",
              "Ảnh chủ đạo (tùy chọn, chặn SVG & SSRF)",
              section.props.image_url ?? "",
              (url) => onChange({ ...section, props: { ...section.props, image_url: url } }),
              "image"
            )}

            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-hero-cta" className="text-xs font-medium">
                  Nhãn nút hành động
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {(section.props.cta_label ?? "").length}/40
                </span>
              </div>
              <Input
                id="sec-hero-cta"
                type="text"
                value={section.props.cta_label ?? ""}
                maxLength={40}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, cta_label: e.target.value },
                  })
                }
                placeholder="Xem sản phẩm"
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.cta_label)}
            </Field>

            {renderUrlField(
              "sec-hero-href",
              "Đường dẫn nút",
              section.props.cta_href ?? "",
              (href) => onChange({ ...section, props: { ...section.props, cta_href: href } }),
              "href"
            )}
          </>
        )}

        {section.type === "featured_products" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-fp-title" className="text-xs font-medium">
                  Tiêu đề khối sản phẩm
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-fp-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-fp-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-fp-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-fp-limit" className="text-xs font-medium">
                Số sản phẩm tối đa (0 đến 12)
              </FieldLabel>
              <Input
                id="sec-fp-limit"
                type="number"
                min={0}
                max={12}
                value={section.props.limit ?? 8}
                onChange={(e) => {
                  const num = Math.max(0, Math.min(12, Math.trunc(Number(e.target.value) || 0)));
                  onChange({ ...section, props: { ...section.props, limit: num } });
                }}
                className="h-8 bg-card text-[13px] tabular-nums"
              />
            </Field>

            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium text-foreground">
                  Ghim sản phẩm nổi bật (để trống để tự lấy mới nhất)
                </span>
                <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                  {(section.props.product_ids ?? []).length}/12
                </span>
              </div>
              {products.length === 0 ? (
                <FieldDescription className="text-xs">
                  Gian hàng chưa có sản phẩm nào. Khi bạn thêm sản phẩm, chúng sẽ xuất hiện tại đây.
                </FieldDescription>
              ) : (
                <div className="max-h-48 space-y-1.5 overflow-y-auto rounded-md border border-border bg-muted/35 p-2">
                  {products.map((prod) => {
                    const selectedIds = section.props.product_ids ?? [];
                    const checked = selectedIds.includes(prod.id);
                    const disabled = !checked && selectedIds.length >= 12;
                    return (
                      <label
                        key={prod.id}
                        className={cn(
                          "flex cursor-pointer items-center gap-2 rounded-md border border-transparent bg-card px-2.5 py-1.5 text-xs transition-colors hover:border-border",
                          disabled && "cursor-not-allowed opacity-50"
                        )}
                      >
                        <Checkbox
                          checked={checked}
                          disabled={disabled}
                          onCheckedChange={(isChecked) => {
                            const nextIds =
                              isChecked === true
                                ? [...selectedIds, prod.id].slice(0, 12)
                                : selectedIds.filter((id) => id !== prod.id);
                            onChange({
                              ...section,
                              props: { ...section.props, product_ids: nextIds },
                            });
                          }}
                        />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-medium text-foreground">
                            {prod.name}
                          </span>
                          <span className="text-[11px] text-muted-foreground tabular-nums">
                            {formatVnd(prod.min_price_vnd)}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </div>
              )}
            </Field>
          </>
        )}

        {section.type === "collection" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-col-title" className="text-xs font-medium">
                  Tiêu đề bộ sưu tập
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-col-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-col-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-col-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-col-cat" className="text-xs font-medium">
                Lọc theo danh mục sản phẩm
              </FieldLabel>
              <Select
                value={section.props.category_slug || DEFAULT_SELECT_VALUE}
                onValueChange={(v) =>
                  onChange({
                    ...section,
                    props: {
                      ...section.props,
                      category_slug: v === DEFAULT_SELECT_VALUE ? "" : v,
                    },
                  })
                }
              >
                <SelectTrigger id="sec-col-cat" size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={DEFAULT_SELECT_VALUE}>
                    Tất cả danh mục của gian hàng
                  </SelectItem>
                  {categories.map((cat) => (
                    <SelectItem key={cat.slug} value={cat.slug}>
                      {cat.name} ({cat.slug})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-col-slug" className="text-xs font-medium">
                Mã bộ sưu tập (tùy chọn)
              </FieldLabel>
              <Input
                id="sec-col-slug"
                type="text"
                value={section.props.collection_slug ?? ""}
                maxLength={80}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, collection_slug: e.target.value },
                  })
                }
                placeholder="co-ban-hang-ngay"
                className="h-8 bg-card text-[13px]"
              />
              <div className="flex flex-wrap gap-1.5 pt-0.5">
                {COLLECTION_SUGGESTIONS.map((col) => {
                  const active = section.props.collection_slug === col.slug;
                  return (
                    <button
                      key={col.slug}
                      type="button"
                      onClick={() =>
                        onChange({
                          ...section,
                          props: { ...section.props, collection_slug: col.slug },
                        })
                      }
                      className={cn(
                        "inline-flex min-h-7 items-center rounded-sm border px-2 py-0.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        active
                          ? "border-brand/40 bg-badge-info-bg text-badge-info-ink"
                          : "border-border bg-muted/60 text-muted-foreground hover:bg-secondary hover:text-foreground"
                      )}
                    >
                      {col.label}
                    </button>
                  );
                })}
              </div>
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-col-limit" className="text-xs font-medium">
                Số sản phẩm hiển thị (0 đến 12)
              </FieldLabel>
              <Input
                id="sec-col-limit"
                type="number"
                min={0}
                max={12}
                value={section.props.limit ?? 4}
                onChange={(e) => {
                  const num = Math.max(0, Math.min(12, Math.trunc(Number(e.target.value) || 0)));
                  onChange({ ...section, props: { ...section.props, limit: num } });
                }}
                className="h-8 bg-card text-[13px] tabular-nums"
              />
            </Field>

            {renderUrlField(
              "sec-col-img",
              "Ảnh đại diện bộ sưu tập (tùy chọn)",
              section.props.image_url ?? "",
              (url) => onChange({ ...section, props: { ...section.props, image_url: url } }),
              "image"
            )}
          </>
        )}

        {section.type === "text" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-txt-title" className="text-xs font-medium">
                  Tiêu đề đoạn giới thiệu
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-txt-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-txt-align" className="text-xs font-medium">
                Căn lề văn bản
              </FieldLabel>
              <Select
                value={section.props.align || "left"}
                onValueChange={(v) =>
                  onChange({
                    ...section,
                    props: { ...section.props, align: v === "center" ? "center" : "left" },
                  })
                }
              >
                <SelectTrigger id="sec-txt-align" size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="left">Căn trái (mặc định)</SelectItem>
                  <SelectItem value="center">Căn giữa</SelectItem>
                </SelectContent>
              </Select>
            </Field>

            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-txt-body" className="text-xs font-medium">
                  Nội dung văn bản thuần
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.body.length}/2000
                </span>
              </div>
              <Textarea
                id="sec-txt-body"
                rows={4}
                value={section.props.body}
                maxLength={2000}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, body: e.target.value } })
                }
                className="bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.body)}
              <FieldDescription className="text-xs">
                Mọi nội dung hiển thị dưới dạng văn bản thuần để đảm bảo an toàn cho người mua.
              </FieldDescription>
            </Field>
          </>
        )}

        {section.type === "banner" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-ban-title" className="text-xs font-medium">
                  Tiêu đề dải quảng bá
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-ban-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-ban-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Textarea
                id="sec-ban-sub"
                rows={2}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>

            {renderUrlField(
              "sec-ban-img",
              "Ảnh nền dải quảng bá (tùy chọn)",
              section.props.image_url ?? "",
              (url) => onChange({ ...section, props: { ...section.props, image_url: url } }),
              "image"
            )}

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-ban-cta" className="text-xs font-medium">
                Nhãn nút hành động
              </FieldLabel>
              <Input
                id="sec-ban-cta"
                type="text"
                value={section.props.cta_label ?? ""}
                maxLength={40}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, cta_label: e.target.value },
                  })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.cta_label)}
            </Field>

            {renderUrlField(
              "sec-ban-href",
              "Đường dẫn nút",
              section.props.cta_href ?? "",
              (href) => onChange({ ...section, props: { ...section.props, cta_href: href } }),
              "href"
            )}
          </>
        )}

        {section.type === "voucher_strip" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-vs-title" className="text-xs font-medium">
                  Tiêu đề khu vực mã giảm giá
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-vs-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-vs-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-vs-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-vs-limit" className="text-xs font-medium">
                Số mã hiển thị tối đa (0 đến 6)
              </FieldLabel>
              <Input
                id="sec-vs-limit"
                type="number"
                min={0}
                max={6}
                value={section.props.limit ?? 4}
                onChange={(e) => {
                  const num = Math.max(0, Math.min(6, Math.trunc(Number(e.target.value) || 0)));
                  onChange({ ...section, props: { ...section.props, limit: num } });
                }}
                className="h-8 bg-card text-[13px] tabular-nums"
              />
              <FieldDescription className="text-xs">
                Tự động hiển thị các mã khuyến mãi đang hoạt động do riêng gian hàng phát hành.
              </FieldDescription>
            </Field>
          </>
        )}

        {section.type === "policies" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-pol-title" className="text-xs font-medium">
                  Tiêu đề khối cam kết &amp; chính sách
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-pol-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>

            <div className="space-y-2.5">
              {section.props.items.map((item: PolicyItem, idx: number) => (
                <div
                  key={`pol-${idx}`}
                  className="space-y-2 rounded-lg border border-border bg-muted/40 p-2.5"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs font-semibold text-foreground">
                      Chính sách #{idx + 1}
                    </span>
                    {section.props.items.length > 1 && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        className="text-destructive hover:text-destructive"
                        onClick={() => {
                          const nextItems = section.props.items.filter((_, i) => i !== idx);
                          onChange({ ...section, props: { ...section.props, items: nextItems } });
                        }}
                      >
                        <Trash2 className="size-3" aria-hidden="true" />
                        <span>Xóa mục</span>
                      </Button>
                    )}
                  </div>
                  <Field className="gap-1">
                    <FieldLabel htmlFor={`pol-h-${idx}`} className="text-xs font-medium">
                      Tiêu đề ngắn (tối đa 80 ký tự)
                    </FieldLabel>
                    <Input
                      id={`pol-h-${idx}`}
                      type="text"
                      value={item.heading}
                      maxLength={80}
                      onChange={(e) => {
                        const nextItems = section.props.items.map((it, i) =>
                          i === idx ? { ...it, heading: e.target.value } : it
                        );
                        onChange({ ...section, props: { ...section.props, items: nextItems } });
                      }}
                      className="h-8 bg-card text-[13px]"
                    />
                    {renderUnsafeWarning(item.heading)}
                  </Field>
                  <Field className="gap-1">
                    <FieldLabel htmlFor={`pol-d-${idx}`} className="text-xs font-medium">
                      Chi tiết cam kết (tối đa 400 ký tự)
                    </FieldLabel>
                    <Textarea
                      id={`pol-d-${idx}`}
                      rows={2}
                      value={item.detail}
                      maxLength={400}
                      onChange={(e) => {
                        const nextItems = section.props.items.map((it, i) =>
                          i === idx ? { ...it, detail: e.target.value } : it
                        );
                        onChange({ ...section, props: { ...section.props, items: nextItems } });
                      }}
                      className="bg-card text-[13px]"
                    />
                    {renderUnsafeWarning(item.detail)}
                  </Field>
                </div>
              ))}

              {section.props.items.length < 8 && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-full"
                  onClick={() => {
                    const nextItems = [
                      ...section.props.items,
                      {
                        heading: "Hỗ trợ tư vấn chọn cỡ",
                        detail: "Đội ngũ gian hàng tư vấn theo chiều cao, cân nặng và phom mặc.",
                      },
                    ];
                    onChange({ ...section, props: { ...section.props, items: nextItems } });
                  }}
                >
                  <Plus className="size-3.5" aria-hidden="true" />
                  <span>Thêm mục chính sách ({section.props.items.length}/8)</span>
                </Button>
              )}
            </div>
          </>
        )}

        {section.type === "lookbook" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-lb-title" className="text-xs font-medium">
                  Tiêu đề Lookbook
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-lb-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-lb-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-lb-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-lb-cta" className="text-xs font-medium">
                Nhãn liên kết (tùy chọn)
              </FieldLabel>
              <Input
                id="sec-lb-cta"
                type="text"
                value={section.props.cta_label ?? ""}
                maxLength={40}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, cta_label: e.target.value },
                  })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.cta_label)}
            </Field>
            {renderUrlField(
              "sec-lb-href",
              "Đường dẫn liên kết",
              section.props.cta_href ?? "",
              (cta_href) => onChange({ ...section, props: { ...section.props, cta_href } }),
              "href"
            )}
            <FieldDescription className="text-xs">
              Thêm các block loại &ldquo;Ảnh minh họa&rdquo; ở phần Block bên dưới để dựng khung ảnh
              Lookbook.
            </FieldDescription>
          </>
        )}

        {section.type === "metrics_strip" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-ms-title" className="text-xs font-medium">
                  Tiêu đề dải chỉ số
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-ms-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-ms-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-ms-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>
            <FieldDescription className="text-xs">
              Thêm các block loại &ldquo;Chỉ số số liệu&rdquo; ở bên dưới để hiển thị từng chỉ số
              (GSM, thời gian giao, bảo hành...).
            </FieldDescription>
          </>
        )}

        {section.type === "sku_matrix" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-sku-title" className="text-xs font-medium">
                  Tiêu đề bảng ma trận SKU
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-sku-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-sku-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-sku-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-sku-cat" className="text-xs font-medium">
                Lọc theo danh mục (tùy chọn)
              </FieldLabel>
              <Select
                value={section.props.category_slug || DEFAULT_SELECT_VALUE}
                onValueChange={(v) =>
                  onChange({
                    ...section,
                    props: {
                      ...section.props,
                      category_slug: v === DEFAULT_SELECT_VALUE ? "" : v,
                    },
                  })
                }
              >
                <SelectTrigger id="sec-sku-cat" size="sm" className="w-full bg-card">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={DEFAULT_SELECT_VALUE}>Toàn bộ sản phẩm</SelectItem>
                  {categories.map((cat) => (
                    <SelectItem key={cat.slug} value={cat.slug}>
                      {cat.name} ({cat.slug})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-sku-limit" className="text-xs font-medium">
                Số dòng SKU tối đa (0 đến 24)
              </FieldLabel>
              <Input
                id="sec-sku-limit"
                type="number"
                min={0}
                max={24}
                value={section.props.limit ?? 12}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: {
                      ...section.props,
                      limit: Math.max(0, Math.min(24, Math.trunc(Number(e.target.value) || 0))),
                    },
                  })
                }
                className="h-8 bg-card text-[13px] tabular-nums"
              />
            </Field>
            <label className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
              <Checkbox
                checked={section.props.show_out_of_stock ?? true}
                onCheckedChange={(checked) =>
                  onChange({
                    ...section,
                    props: { ...section.props, show_out_of_stock: checked === true },
                  })
                }
              />
              <span>Hiển thị cả biến thể tạm hết hàng trong bảng SKU</span>
            </label>
          </>
        )}

        {section.type === "testimonials" && (
          <>
            <Field className="gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="sec-ts-title" className="text-xs font-medium">
                  Tiêu đề khu vực đánh giá
                </FieldLabel>
                <span className="text-[11px] text-muted-foreground tabular-nums">
                  {section.props.title.length}/120
                </span>
              </div>
              <Input
                id="sec-ts-title"
                type="text"
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.title)}
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="sec-ts-sub" className="text-xs font-medium">
                Mô tả ngắn
              </FieldLabel>
              <Input
                id="sec-ts-sub"
                type="text"
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
                className="h-8 bg-card text-[13px]"
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </Field>
            <FieldDescription className="text-xs">
              Thêm các block loại &ldquo;Trích dẫn&rdquo; ở bên dưới để đưa nhận xét khách hàng vào
              khối.
            </FieldDescription>
          </>
        )}
      </div>

      <Separator />

      {/* Full Block Editor (9 typed block types, max 12 blocks per section) */}
      <div className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-foreground">
            Block con ({blocks.length}/{MAX_BLOCKS_PER_SECTION})
          </h4>
        </div>

        <div className="flex items-center gap-2">
          <Select value={newBlockType} onValueChange={(v) => setNewBlockType(v as SiteBlockType)}>
            <SelectTrigger
              size="sm"
              className="min-w-0 flex-1 bg-card"
              aria-label="Chọn loại block cần thêm"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {BLOCK_TYPE_ORDER.map((bt) => (
                <SelectItem key={bt} value={bt}>
                  {BLOCK_TYPE_META[bt].label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={blocks.length >= MAX_BLOCKS_PER_SECTION}
            onClick={handleAddBlock}
          >
            <Plus className="size-3.5" aria-hidden="true" />
            <span>Thêm block</span>
          </Button>
        </div>

        {blocks.length === 0 ? (
          <div className="rounded-md border border-dashed border-border bg-muted/35 p-3 text-xs text-muted-foreground">
            Khối này chưa có block con nào. Chọn loại block ở trên để bổ sung chi tiết.
          </div>
        ) : (
          <div className="space-y-2.5">
            {blocks.map((blk, idx) => (
              <div
                key={blk.id}
                className="space-y-2.5 rounded-lg border border-border bg-muted/40 p-3"
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-1.5">
                    <Badge variant="info" className="rounded-sm px-2 py-0.5 text-[11px]">
                      {BLOCK_TYPE_META[blk.type].label}
                    </Badge>
                    <span className="truncate text-xs text-muted-foreground tabular-nums">
                      #{idx + 1}
                    </span>
                  </div>
                  <div className="flex items-center gap-1">
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-xs"
                      disabled={idx === 0}
                      onClick={() => handleMoveBlock(idx, -1)}
                      aria-label={`Di chuyển block #${idx + 1} lên trên`}
                    >
                      <ArrowUp className="size-3" aria-hidden="true" />
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-xs"
                      disabled={idx === blocks.length - 1}
                      onClick={() => handleMoveBlock(idx, 1)}
                      aria-label={`Di chuyển block #${idx + 1} xuống dưới`}
                    >
                      <ArrowDown className="size-3" aria-hidden="true" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      className="text-destructive hover:text-destructive"
                      onClick={() => handleDeleteBlock(blk.id)}
                      aria-label={`Xóa block #${idx + 1}`}
                    >
                      <Trash2 className="size-3" aria-hidden="true" />
                    </Button>
                  </div>
                </div>
                {renderBlockFields(blk, idx)}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
