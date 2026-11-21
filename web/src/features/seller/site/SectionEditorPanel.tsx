"use client";

import React, { useState } from "react";
import { Button, IconPlus } from "@/components";
import { formatVnd } from "@/lib/format";
import type { GwCategory, GwSellerProduct } from "@/features/seller/sellerApiTypes";
import common from "@/features/seller/SellerCommon.module.css";
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
import styles from "./SellerSiteEditor.module.css";

export interface SectionEditorPanelProps {
  section: SiteSectionV2 | null;
  products: GwSellerProduct[];
  categories: GwCategory[];
  onChange: (updated: SiteSectionV2) => void;
}

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
      <div className={common.card}>
        <div className={common.cardHeader}>
          <h2 className={common.cardTitle}>Chi tiết khối & Block</h2>
        </div>
        <div className={common.inlineEmpty}>
          Chọn một khối ở cột bên trái để chỉnh sửa nội dung, biến thể và các block con, hoặc thêm
          khối mới.
        </div>
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
    return <span className={common.fieldError}>Nội dung bị chặn vì {reason}.</span>;
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
      <div className={common.field}>
        <label htmlFor={id} className={common.fieldLabel}>
          {label}
        </label>
        <input
          id={id}
          type="text"
          className={common.textInput}
          value={value}
          onChange={(e) => onUpdate(e.target.value)}
          placeholder={
            kind === "image" ? "/banners/campaign-everyday.jpg hoặc https://..." : "/san-pham"
          }
          maxLength={500}
        />
        {err ? (
          <span className={common.fieldError}>{err}</span>
        ) : (
          <div className={styles.quickChips}>
            {(kind === "image" ? QUICK_MEDIA_ASSETS : INTERNAL_HREF_SUGGESTIONS).map((item) => {
              const targetVal = "url" in item ? item.url : item.href;
              const active = value.trim() === targetVal;
              return (
                <button
                  key={targetVal}
                  type="button"
                  className={`${styles.quickChip} ${active ? styles.quickChipActive : ""}`}
                  onClick={() => onUpdate(targetVal)}
                >
                  {item.label}
                </button>
              );
            })}
          </div>
        )}
      </div>
    );
  };

  const renderBlockFields = (blk: SiteSectionBlock, idx: number) => {
    const replaceBlock = (updated: SiteSectionBlock) => {
      updateBlocks((prev) => prev.map((b, i) => (i === idx ? updated : b)));
    };

    switch (blk.type) {
      case "heading":
        return (
          <div className={styles.fields2}>
            <div className={common.field}>
              <label htmlFor={`blk-hd-${idx}`} className={common.fieldLabel}>
                Nội dung tiêu đề (tối đa 120 ký tự)
              </label>
              <input
                id={`blk-hd-${idx}`}
                type="text"
                className={common.textInput}
                value={blk.props.text}
                maxLength={120}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, text: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.text)}
            </div>
            <div className={common.field}>
              <label htmlFor={`blk-hdl-${idx}`} className={common.fieldLabel}>
                Cấp tiêu đề
              </label>
              <select
                id={`blk-hdl-${idx}`}
                className={common.textInput}
                value={blk.props.level || "h3"}
                onChange={(e) =>
                  replaceBlock({
                    ...blk,
                    props: { ...blk.props, level: e.target.value as "h2" | "h3" },
                  })
                }
              >
                <option value="h2">Tiêu đề lớn (H2)</option>
                <option value="h3">Tiêu đề phụ (H3)</option>
              </select>
            </div>
          </div>
        );

      case "paragraph":
        return (
          <div className={common.field}>
            <label htmlFor={`blk-p-${idx}`} className={common.fieldLabel}>
              Đoạn văn bản (tối đa 800 ký tự)
            </label>
            <textarea
              id={`blk-p-${idx}`}
              className={common.textArea}
              rows={3}
              value={blk.props.text}
              maxLength={800}
              onChange={(e) =>
                replaceBlock({ ...blk, props: { ...blk.props, text: e.target.value } })
              }
            />
            {renderUnsafeWarning(blk.props.text)}
          </div>
        );

      case "badge":
        return (
          <div className={styles.fields2}>
            <div className={common.field}>
              <label htmlFor={`blk-bdg-${idx}`} className={common.fieldLabel}>
                Nhãn hiển thị (tối đa 48 ký tự)
              </label>
              <input
                id={`blk-bdg-${idx}`}
                type="text"
                className={common.textInput}
                value={blk.props.label}
                maxLength={48}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, label: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.label)}
            </div>
            <div className={common.field}>
              <label htmlFor={`blk-bdgt-${idx}`} className={common.fieldLabel}>
                Sắc thái nhãn
              </label>
              <select
                id={`blk-bdgt-${idx}`}
                className={common.textInput}
                value={blk.props.tone || "neutral"}
                onChange={(e) =>
                  replaceBlock({
                    ...blk,
                    props: {
                      ...blk.props,
                      tone: e.target.value as "neutral" | "accent" | "band",
                    },
                  })
                }
              >
                <option value="neutral">Trung tính</option>
                <option value="accent">Màu nhấn</option>
                <option value="band">Dải tương phản</option>
              </select>
            </div>
          </div>
        );

      case "cta":
        return (
          <div className={styles.fieldStack}>
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor={`blk-cta-lbl-${idx}`} className={common.fieldLabel}>
                  Nhãn nút (tối đa 48 ký tự)
                </label>
                <input
                  id={`blk-cta-lbl-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.label}
                  maxLength={48}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, label: e.target.value } })
                  }
                />
                {renderUnsafeWarning(blk.props.label)}
              </div>
              <div className={common.field}>
                <label htmlFor={`blk-cta-st-${idx}`} className={common.fieldLabel}>
                  Kiểu nút
                </label>
                <select
                  id={`blk-cta-st-${idx}`}
                  className={common.textInput}
                  value={blk.props.style || "primary"}
                  onChange={(e) =>
                    replaceBlock({
                      ...blk,
                      props: {
                        ...blk.props,
                        style: e.target.value as "primary" | "secondary" | "link",
                      },
                    })
                  }
                >
                  <option value="primary">Nút chính</option>
                  <option value="secondary">Nút phụ viền</option>
                  <option value="link">Liên kết chữ</option>
                </select>
              </div>
            </div>
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
          <div className={styles.fieldStack}>
            {renderUrlField(
              `blk-img-url-${idx}`,
              "Đường dẫn ảnh (chặn SVG & IP nội bộ)",
              blk.props.image_url,
              (image_url) => replaceBlock({ ...blk, props: { ...blk.props, image_url } }),
              "image",
              true
            )}
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor={`blk-img-alt-${idx}`} className={common.fieldLabel}>
                  Mô tả ảnh (alt)
                </label>
                <input
                  id={`blk-img-alt-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.alt ?? ""}
                  maxLength={140}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, alt: e.target.value } })
                  }
                />
                {renderUnsafeWarning(blk.props.alt)}
              </div>
              <div className={common.field}>
                <label htmlFor={`blk-img-asp-${idx}`} className={common.fieldLabel}>
                  Tỷ lệ khung ảnh
                </label>
                <select
                  id={`blk-img-asp-${idx}`}
                  className={common.textInput}
                  value={blk.props.aspect || "4:3"}
                  onChange={(e) =>
                    replaceBlock({
                      ...blk,
                      props: {
                        ...blk.props,
                        aspect: e.target.value as "16:9" | "4:3" | "3:4" | "1:1",
                      },
                    })
                  }
                >
                  <option value="16:9">Ngang rộng (16:9)</option>
                  <option value="4:3">Tiêu chuẩn (4:3)</option>
                  <option value="3:4">Ảnh đứng Lookbook (3:4)</option>
                  <option value="1:1">Vuông (1:1)</option>
                </select>
              </div>
            </div>
            <div className={common.field}>
              <label htmlFor={`blk-img-cap-${idx}`} className={common.fieldLabel}>
                Chú thích bên dưới ảnh (tùy chọn)
              </label>
              <input
                id={`blk-img-cap-${idx}`}
                type="text"
                className={common.textInput}
                value={blk.props.caption ?? ""}
                maxLength={200}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, caption: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.caption)}
            </div>
          </div>
        );

      case "stat":
        return (
          <div className={styles.fieldStack}>
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor={`blk-st-val-${idx}`} className={common.fieldLabel}>
                  Giá trị chỉ số (tối đa 32 ký tự)
                </label>
                <input
                  id={`blk-st-val-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.value}
                  maxLength={32}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, value: e.target.value } })
                  }
                  placeholder="220gsm"
                />
                {renderUnsafeWarning(blk.props.value)}
              </div>
              <div className={common.field}>
                <label htmlFor={`blk-st-lbl-${idx}`} className={common.fieldLabel}>
                  Nhãn chỉ số (tối đa 60 ký tự)
                </label>
                <input
                  id={`blk-st-lbl-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.label}
                  maxLength={60}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, label: e.target.value } })
                  }
                  placeholder="Định lượng vải"
                />
                {renderUnsafeWarning(blk.props.label)}
              </div>
            </div>
            <div className={common.field}>
              <label htmlFor={`blk-st-note-${idx}`} className={common.fieldLabel}>
                Ghi chú giải thích (tùy chọn)
              </label>
              <input
                id={`blk-st-note-${idx}`}
                type="text"
                className={common.textInput}
                value={blk.props.note ?? ""}
                maxLength={140}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, note: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.note)}
            </div>
          </div>
        );

      case "policy":
        return (
          <div className={styles.fieldStack}>
            <div className={common.field}>
              <label htmlFor={`blk-pol-h-${idx}`} className={common.fieldLabel}>
                Tiêu đề cam kết (tối đa 80 ký tự)
              </label>
              <input
                id={`blk-pol-h-${idx}`}
                type="text"
                className={common.textInput}
                value={blk.props.heading}
                maxLength={80}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, heading: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.heading)}
            </div>
            <div className={common.field}>
              <label htmlFor={`blk-pol-d-${idx}`} className={common.fieldLabel}>
                Chi tiết cam kết (tối đa 400 ký tự)
              </label>
              <textarea
                id={`blk-pol-d-${idx}`}
                className={common.textArea}
                rows={2}
                value={blk.props.detail}
                maxLength={400}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, detail: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.detail)}
            </div>
          </div>
        );

      case "quote":
        return (
          <div className={styles.fieldStack}>
            <div className={common.field}>
              <label htmlFor={`blk-qt-b-${idx}`} className={common.fieldLabel}>
                Nội dung trích dẫn (tối đa 400 ký tự)
              </label>
              <textarea
                id={`blk-qt-b-${idx}`}
                className={common.textArea}
                rows={2}
                value={blk.props.body}
                maxLength={400}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, body: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.body)}
            </div>
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor={`blk-qt-a-${idx}`} className={common.fieldLabel}>
                  Tác giả / Khách hàng
                </label>
                <input
                  id={`blk-qt-a-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.author}
                  maxLength={80}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, author: e.target.value } })
                  }
                />
                {renderUnsafeWarning(blk.props.author)}
              </div>
              <div className={common.field}>
                <label htmlFor={`blk-qt-r-${idx}`} className={common.fieldLabel}>
                  Vai trò / Địa phương (tùy chọn)
                </label>
                <input
                  id={`blk-qt-r-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.role ?? ""}
                  maxLength={80}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, role: e.target.value } })
                  }
                />
                {renderUnsafeWarning(blk.props.role)}
              </div>
            </div>
          </div>
        );

      case "feature":
        return (
          <div className={styles.fieldStack}>
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor={`blk-ft-t-${idx}`} className={common.fieldLabel}>
                  Tiêu đề điểm nổi bật (tối đa 80 ký tự)
                </label>
                <input
                  id={`blk-ft-t-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.title}
                  maxLength={80}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, title: e.target.value } })
                  }
                />
                {renderUnsafeWarning(blk.props.title)}
              </div>
              <div className={common.field}>
                <label htmlFor={`blk-ft-tag-${idx}`} className={common.fieldLabel}>
                  Nhãn ngắn (tùy chọn)
                </label>
                <input
                  id={`blk-ft-tag-${idx}`}
                  type="text"
                  className={common.textInput}
                  value={blk.props.tag ?? ""}
                  maxLength={40}
                  onChange={(e) =>
                    replaceBlock({ ...blk, props: { ...blk.props, tag: e.target.value } })
                  }
                />
                {renderUnsafeWarning(blk.props.tag)}
              </div>
            </div>
            <div className={common.field}>
              <label htmlFor={`blk-ft-d-${idx}`} className={common.fieldLabel}>
                Mô tả chi tiết (tối đa 300 ký tự)
              </label>
              <textarea
                id={`blk-ft-d-${idx}`}
                className={common.textArea}
                rows={2}
                value={blk.props.detail}
                maxLength={300}
                onChange={(e) =>
                  replaceBlock({ ...blk, props: { ...blk.props, detail: e.target.value } })
                }
              />
              {renderUnsafeWarning(blk.props.detail)}
            </div>
          </div>
        );
    }
  };

  return (
    <div className={common.card}>
      <div className={common.cardHeader}>
        <div>
          <h2 className={common.cardTitle}>{meta.label}</h2>
          <p className={common.cardMeta}>{meta.description}</p>
        </div>
        <label className={styles.visLabel}>
          <input
            type="checkbox"
            className={common.toggleSwitch}
            checked={section.visible}
            onChange={(e) => onChange({ ...section, visible: e.target.checked })}
          />
          <span>{section.visible ? "Đang hiển thị" : "Đang ẩn"}</span>
        </label>
      </div>

      <div className={styles.fieldStack}>
        {/* Section Variant & Layout Settings */}
        <div className={styles.sectionSettingsBox}>
          <div className={styles.sectionSettingsTitle}>Biến thể & Cài đặt bố cục khối</div>
          <div className={styles.settingsGrid3}>
            <div className={common.field}>
              <label htmlFor="sec-variant" className={common.fieldLabel}>
                Biến thể hiển thị
              </label>
              <select
                id="sec-variant"
                className={common.textInput}
                value={section.variant ?? ""}
                onChange={(e) => onChange({ ...section, variant: e.target.value })}
              >
                {allowedVariants.map((v) => (
                  <option key={v || "default"} value={v}>
                    {SECTION_VARIANT_LABELS[v] ?? v}
                  </option>
                ))}
              </select>
            </div>

            <div className={common.field}>
              <label htmlFor="sec-surface" className={common.fieldLabel}>
                Bề mặt nền
              </label>
              <select
                id="sec-surface"
                className={common.textInput}
                value={settings.surface ?? ""}
                onChange={(e) =>
                  updateSettings({ surface: e.target.value as SectionSettings["surface"] })
                }
              >
                {SECTION_SURFACE_OPTIONS.map((opt) => (
                  <option key={opt.value || "default"} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </div>

            <div className={common.field}>
              <label htmlFor="sec-padding" className={common.fieldLabel}>
                Khoảng đệm dọc
              </label>
              <select
                id="sec-padding"
                className={common.textInput}
                value={settings.padding ?? ""}
                onChange={(e) =>
                  updateSettings({ padding: e.target.value as SectionSettings["padding"] })
                }
              >
                {SECTION_PADDING_OPTIONS.map((opt) => (
                  <option key={opt.value || "default"} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </div>

            <div className={common.field}>
              <label htmlFor="sec-align" className={common.fieldLabel}>
                Căn lề tiêu đề
              </label>
              <select
                id="sec-align"
                className={common.textInput}
                value={settings.align ?? ""}
                onChange={(e) =>
                  updateSettings({
                    align: e.target.value as "" | "left" | "center",
                  })
                }
              >
                <option value="">Theo mặc định</option>
                <option value="left">Căn trái</option>
                <option value="center">Căn giữa</option>
              </select>
            </div>

            <div className={common.field}>
              <label htmlFor="sec-columns" className={common.fieldLabel}>
                Số cột (0 = mặc định, 1–5)
              </label>
              <input
                id="sec-columns"
                type="number"
                min={0}
                max={5}
                className={common.textInput}
                value={settings.columns ?? 0}
                onChange={(e) =>
                  updateSettings({
                    columns: Math.max(0, Math.min(5, Math.trunc(Number(e.target.value) || 0))),
                  })
                }
              />
            </div>

            <div className={common.field}>
              <label htmlFor="sec-max-items" className={common.fieldLabel}>
                Giới hạn mục (0 = mặc định, 1–24)
              </label>
              <input
                id="sec-max-items"
                type="number"
                min={0}
                max={24}
                className={common.textInput}
                value={settings.max_items ?? 0}
                onChange={(e) =>
                  updateSettings({
                    max_items: Math.max(0, Math.min(24, Math.trunc(Number(e.target.value) || 0))),
                  })
                }
              />
            </div>
          </div>
        </div>

        {/* Section-specific props */}
        {section.type === "hero" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-hero-kicker" className={common.fieldLabel}>
                  Nhãn ngắn phía trên tiêu đề (tùy chọn)
                </label>
                <span className={styles.charCount}>{(section.props.eyebrow ?? "").length}/60</span>
              </div>
              <input
                id="sec-hero-kicker"
                type="text"
                className={common.textInput}
                value={section.props.eyebrow ?? ""}
                maxLength={60}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, eyebrow: e.target.value },
                  })
                }
                placeholder="Ví dụ: Xưởng dệt Hải Phòng"
              />
              {renderUnsafeWarning(section.props.eyebrow)}
            </div>

            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-hero-title" className={common.fieldLabel}>
                  Tiêu đề chính
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-hero-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, title: e.target.value },
                  })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>

            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-hero-sub" className={common.fieldLabel}>
                  Mô tả ngắn
                </label>
                <span className={styles.charCount}>
                  {(section.props.subtitle ?? "").length}/400
                </span>
              </div>
              <textarea
                id="sec-hero-sub"
                className={common.textArea}
                value={section.props.subtitle ?? ""}
                maxLength={400}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, subtitle: e.target.value },
                  })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>

            {renderUrlField(
              "sec-hero-img",
              "Ảnh chủ đạo (tùy chọn, chặn SVG & SSRF)",
              section.props.image_url ?? "",
              (url) => onChange({ ...section, props: { ...section.props, image_url: url } }),
              "image"
            )}

            <div className={styles.fields2}>
              <div className={common.field}>
                <div className={styles.charRow}>
                  <label htmlFor="sec-hero-cta" className={common.fieldLabel}>
                    Nhãn nút hành động
                  </label>
                  <span className={styles.charCount}>
                    {(section.props.cta_label ?? "").length}/40
                  </span>
                </div>
                <input
                  id="sec-hero-cta"
                  type="text"
                  className={common.textInput}
                  value={section.props.cta_label ?? ""}
                  maxLength={40}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: { ...section.props, cta_label: e.target.value },
                    })
                  }
                  placeholder="Xem sản phẩm"
                />
                {renderUnsafeWarning(section.props.cta_label)}
              </div>

              {renderUrlField(
                "sec-hero-href",
                "Đường dẫn nút",
                section.props.cta_href ?? "",
                (href) => onChange({ ...section, props: { ...section.props, cta_href: href } }),
                "href"
              )}
            </div>
          </>
        )}

        {section.type === "featured_products" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-fp-title" className={common.fieldLabel}>
                  Tiêu đề khối sản phẩm
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-fp-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-fp-sub" className={common.fieldLabel}>
                  Mô tả ngắn
                </label>
                <input
                  id="sec-fp-sub"
                  type="text"
                  className={common.textInput}
                  value={section.props.subtitle ?? ""}
                  maxLength={300}
                  onChange={(e) =>
                    onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                  }
                />
                {renderUnsafeWarning(section.props.subtitle)}
              </div>
              <div className={common.field}>
                <label htmlFor="sec-fp-limit" className={common.fieldLabel}>
                  Số sản phẩm tối đa (0 đến 12)
                </label>
                <input
                  id="sec-fp-limit"
                  type="number"
                  min={0}
                  max={12}
                  className={common.textInput}
                  value={section.props.limit ?? 8}
                  onChange={(e) => {
                    const num = Math.max(0, Math.min(12, Math.trunc(Number(e.target.value) || 0)));
                    onChange({ ...section, props: { ...section.props, limit: num } });
                  }}
                />
              </div>
            </div>

            <div className={common.field}>
              <div className={styles.charRow}>
                <span className={common.fieldLabel}>
                  Chọn sản phẩm ghim nổi bật (để trống để tự hiển thị sản phẩm mới nhất của gian
                  hàng)
                </span>
                <span className={styles.charCount}>
                  {(section.props.product_ids ?? []).length}/12
                </span>
              </div>
              {products.length === 0 ? (
                <p className={common.fieldHint}>
                  Gian hàng chưa có sản phẩm nào. Khi bạn thêm sản phẩm, chúng sẽ xuất hiện tại đây.
                </p>
              ) : (
                <div className={styles.productPickList}>
                  {products.map((prod) => {
                    const selectedIds = section.props.product_ids ?? [];
                    const checked = selectedIds.includes(prod.id);
                    const disabled = !checked && selectedIds.length >= 12;
                    return (
                      <label key={prod.id} className={styles.productPickItem}>
                        <input
                          type="checkbox"
                          checked={checked}
                          disabled={disabled}
                          onChange={(e) => {
                            const nextIds = e.target.checked
                              ? [...selectedIds, prod.id].slice(0, 12)
                              : selectedIds.filter((id) => id !== prod.id);
                            onChange({
                              ...section,
                              props: { ...section.props, product_ids: nextIds },
                            });
                          }}
                        />
                        <span className={styles.productPickText}>
                          <span className={styles.productPickName}>{prod.name}</span>
                          <span className={styles.productPickSub}>
                            {formatVnd(prod.min_price_vnd)}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </div>
              )}
            </div>
          </>
        )}

        {section.type === "collection" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-col-title" className={common.fieldLabel}>
                  Tiêu đề bộ sưu tập
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-col-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>

            <div className={common.field}>
              <label htmlFor="sec-col-sub" className={common.fieldLabel}>
                Mô tả ngắn
              </label>
              <input
                id="sec-col-sub"
                type="text"
                className={common.textInput}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-col-cat" className={common.fieldLabel}>
                  Lọc theo danh mục sản phẩm
                </label>
                <select
                  id="sec-col-cat"
                  className={common.textInput}
                  value={section.props.category_slug ?? ""}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: { ...section.props, category_slug: e.target.value },
                    })
                  }
                >
                  <option value="">Tất cả danh mục của gian hàng</option>
                  {categories.map((cat) => (
                    <option key={cat.slug} value={cat.slug}>
                      {cat.name} ({cat.slug})
                    </option>
                  ))}
                </select>
              </div>

              <div className={common.field}>
                <label htmlFor="sec-col-slug" className={common.fieldLabel}>
                  Mã bộ sưu tập (tùy chọn)
                </label>
                <input
                  id="sec-col-slug"
                  type="text"
                  className={common.textInput}
                  value={section.props.collection_slug ?? ""}
                  maxLength={80}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: { ...section.props, collection_slug: e.target.value },
                    })
                  }
                  placeholder="co-ban-hang-ngay"
                />
                <div className={styles.quickChips}>
                  {COLLECTION_SUGGESTIONS.map((col) => (
                    <button
                      key={col.slug}
                      type="button"
                      className={`${styles.quickChip} ${
                        section.props.collection_slug === col.slug ? styles.quickChipActive : ""
                      }`}
                      onClick={() =>
                        onChange({
                          ...section,
                          props: { ...section.props, collection_slug: col.slug },
                        })
                      }
                    >
                      {col.label}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-col-limit" className={common.fieldLabel}>
                  Số sản phẩm hiển thị (0 đến 12)
                </label>
                <input
                  id="sec-col-limit"
                  type="number"
                  min={0}
                  max={12}
                  className={common.textInput}
                  value={section.props.limit ?? 4}
                  onChange={(e) => {
                    const num = Math.max(0, Math.min(12, Math.trunc(Number(e.target.value) || 0)));
                    onChange({ ...section, props: { ...section.props, limit: num } });
                  }}
                />
              </div>
              <div />
            </div>

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
            <div className={styles.fields2}>
              <div className={common.field}>
                <div className={styles.charRow}>
                  <label htmlFor="sec-txt-title" className={common.fieldLabel}>
                    Tiêu đề đoạn giới thiệu
                  </label>
                  <span className={styles.charCount}>{section.props.title.length}/120</span>
                </div>
                <input
                  id="sec-txt-title"
                  type="text"
                  className={common.textInput}
                  value={section.props.title}
                  maxLength={120}
                  onChange={(e) =>
                    onChange({ ...section, props: { ...section.props, title: e.target.value } })
                  }
                />
                {renderUnsafeWarning(section.props.title)}
              </div>
              <div className={common.field}>
                <label htmlFor="sec-txt-align" className={common.fieldLabel}>
                  Căn lề văn bản
                </label>
                <select
                  id="sec-txt-align"
                  className={common.textInput}
                  value={section.props.align ?? "left"}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: {
                        ...section.props,
                        align: e.target.value === "center" ? "center" : "left",
                      },
                    })
                  }
                >
                  <option value="left">Căn trái (mặc định)</option>
                  <option value="center">Căn giữa</option>
                </select>
              </div>
            </div>

            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-txt-body" className={common.fieldLabel}>
                  Nội dung văn bản thuần
                </label>
                <span className={styles.charCount}>{section.props.body.length}/2000</span>
              </div>
              <textarea
                id="sec-txt-body"
                className={common.textArea}
                rows={5}
                value={section.props.body}
                maxLength={2000}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, body: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.body)}
              <span className={common.fieldHint}>
                Mọi nội dung hiển thị dưới dạng văn bản thuần để đảm bảo an toàn cho người mua.
              </span>
            </div>
          </>
        )}

        {section.type === "banner" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-ban-title" className={common.fieldLabel}>
                  Tiêu đề dải quảng bá
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-ban-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>

            <div className={common.field}>
              <label htmlFor="sec-ban-sub" className={common.fieldLabel}>
                Mô tả ngắn
              </label>
              <textarea
                id="sec-ban-sub"
                className={common.textArea}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>

            {renderUrlField(
              "sec-ban-img",
              "Ảnh nền dải quảng bá (tùy chọn)",
              section.props.image_url ?? "",
              (url) => onChange({ ...section, props: { ...section.props, image_url: url } }),
              "image"
            )}

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-ban-cta" className={common.fieldLabel}>
                  Nhãn nút hành động
                </label>
                <input
                  id="sec-ban-cta"
                  type="text"
                  className={common.textInput}
                  value={section.props.cta_label ?? ""}
                  maxLength={40}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: { ...section.props, cta_label: e.target.value },
                    })
                  }
                />
                {renderUnsafeWarning(section.props.cta_label)}
              </div>

              {renderUrlField(
                "sec-ban-href",
                "Đường dẫn nút",
                section.props.cta_href ?? "",
                (href) => onChange({ ...section, props: { ...section.props, cta_href: href } }),
                "href"
              )}
            </div>
          </>
        )}

        {section.type === "voucher_strip" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-vs-title" className={common.fieldLabel}>
                  Tiêu đề khu vực mã giảm giá
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-vs-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-vs-sub" className={common.fieldLabel}>
                  Mô tả ngắn
                </label>
                <input
                  id="sec-vs-sub"
                  type="text"
                  className={common.textInput}
                  value={section.props.subtitle ?? ""}
                  maxLength={300}
                  onChange={(e) =>
                    onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                  }
                />
                {renderUnsafeWarning(section.props.subtitle)}
              </div>
              <div className={common.field}>
                <label htmlFor="sec-vs-limit" className={common.fieldLabel}>
                  Số mã hiển thị tối đa (0 đến 6)
                </label>
                <input
                  id="sec-vs-limit"
                  type="number"
                  min={0}
                  max={6}
                  className={common.textInput}
                  value={section.props.limit ?? 4}
                  onChange={(e) => {
                    const num = Math.max(0, Math.min(6, Math.trunc(Number(e.target.value) || 0)));
                    onChange({ ...section, props: { ...section.props, limit: num } });
                  }}
                />
              </div>
            </div>
            <p className={common.fieldHint}>
              Tự động hiển thị các mã khuyến mãi đang hoạt động do riêng gian hàng phát hành.
            </p>
          </>
        )}

        {section.type === "policies" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-pol-title" className={common.fieldLabel}>
                  Tiêu đề khối cam kết & chính sách
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-pol-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>

            <div className={styles.fieldStack}>
              {section.props.items.map((item: PolicyItem, idx: number) => (
                <div key={`pol-${idx}`} className={styles.policyItemCard}>
                  <div className={styles.policyItemHead}>
                    <span className={styles.policyItemLabel}>Chính sách #{idx + 1}</span>
                    {section.props.items.length > 1 && (
                      <button
                        type="button"
                        className={`${styles.miniBtn} ${styles.miniBtnDanger}`}
                        onClick={() => {
                          const nextItems = section.props.items.filter((_, i) => i !== idx);
                          onChange({ ...section, props: { ...section.props, items: nextItems } });
                        }}
                      >
                        Xóa mục
                      </button>
                    )}
                  </div>
                  <div className={common.field}>
                    <label htmlFor={`pol-h-${idx}`} className={common.fieldLabel}>
                      Tiêu đề ngắn (tối đa 80 ký tự)
                    </label>
                    <input
                      id={`pol-h-${idx}`}
                      type="text"
                      className={common.textInput}
                      value={item.heading}
                      maxLength={80}
                      onChange={(e) => {
                        const nextItems = section.props.items.map((it, i) =>
                          i === idx ? { ...it, heading: e.target.value } : it
                        );
                        onChange({ ...section, props: { ...section.props, items: nextItems } });
                      }}
                    />
                    {renderUnsafeWarning(item.heading)}
                  </div>
                  <div className={common.field}>
                    <label htmlFor={`pol-d-${idx}`} className={common.fieldLabel}>
                      Chi tiết cam kết (tối đa 400 ký tự)
                    </label>
                    <textarea
                      id={`pol-d-${idx}`}
                      className={common.textArea}
                      rows={2}
                      value={item.detail}
                      maxLength={400}
                      onChange={(e) => {
                        const nextItems = section.props.items.map((it, i) =>
                          i === idx ? { ...it, detail: e.target.value } : it
                        );
                        onChange({ ...section, props: { ...section.props, items: nextItems } });
                      }}
                    />
                    {renderUnsafeWarning(item.detail)}
                  </div>
                </div>
              ))}

              {section.props.items.length < 8 && (
                <div>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
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
                    <IconPlus size={14} />
                    <span>Thêm mục chính sách ({section.props.items.length}/8)</span>
                  </Button>
                </div>
              )}
            </div>
          </>
        )}

        {section.type === "lookbook" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-lb-title" className={common.fieldLabel}>
                  Tiêu đề Lookbook
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-lb-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>
            <div className={common.field}>
              <label htmlFor="sec-lb-sub" className={common.fieldLabel}>
                Mô tả ngắn
              </label>
              <input
                id="sec-lb-sub"
                type="text"
                className={common.textInput}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-lb-cta" className={common.fieldLabel}>
                  Nhãn liên kết (tùy chọn)
                </label>
                <input
                  id="sec-lb-cta"
                  type="text"
                  className={common.textInput}
                  value={section.props.cta_label ?? ""}
                  maxLength={40}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: { ...section.props, cta_label: e.target.value },
                    })
                  }
                />
                {renderUnsafeWarning(section.props.cta_label)}
              </div>
              {renderUrlField(
                "sec-lb-href",
                "Đường dẫn liên kết",
                section.props.cta_href ?? "",
                (cta_href) => onChange({ ...section, props: { ...section.props, cta_href } }),
                "href"
              )}
            </div>
            <p className={common.fieldHint}>
              Thêm các block loại &ldquo;Ảnh minh họa&rdquo; ở phần Block bên dưới để dựng khung ảnh
              Lookbook.
            </p>
          </>
        )}

        {section.type === "metrics_strip" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-ms-title" className={common.fieldLabel}>
                  Tiêu đề dải chỉ số
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-ms-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>
            <div className={common.field}>
              <label htmlFor="sec-ms-sub" className={common.fieldLabel}>
                Mô tả ngắn
              </label>
              <input
                id="sec-ms-sub"
                type="text"
                className={common.textInput}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>
            <p className={common.fieldHint}>
              Thêm các block loại &ldquo;Chỉ số số liệu&rdquo; ở bên dưới để hiển thị từng chỉ số
              (GSM, thời gian giao, bảo hành...).
            </p>
          </>
        )}

        {section.type === "sku_matrix" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-sku-title" className={common.fieldLabel}>
                  Tiêu đề bảng ma trận SKU
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-sku-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>
            <div className={common.field}>
              <label htmlFor="sec-sku-sub" className={common.fieldLabel}>
                Mô tả ngắn
              </label>
              <input
                id="sec-sku-sub"
                type="text"
                className={common.textInput}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>
            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="sec-sku-cat" className={common.fieldLabel}>
                  Lọc theo danh mục (tùy chọn)
                </label>
                <select
                  id="sec-sku-cat"
                  className={common.textInput}
                  value={section.props.category_slug ?? ""}
                  onChange={(e) =>
                    onChange({
                      ...section,
                      props: { ...section.props, category_slug: e.target.value },
                    })
                  }
                >
                  <option value="">Toàn bộ sản phẩm</option>
                  {categories.map((cat) => (
                    <option key={cat.slug} value={cat.slug}>
                      {cat.name} ({cat.slug})
                    </option>
                  ))}
                </select>
              </div>
              <div className={common.field}>
                <label htmlFor="sec-sku-limit" className={common.fieldLabel}>
                  Số dòng SKU tối đa (0 đến 24)
                </label>
                <input
                  id="sec-sku-limit"
                  type="number"
                  min={0}
                  max={24}
                  className={common.textInput}
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
                />
              </div>
            </div>
            <label className={styles.checkRow}>
              <input
                type="checkbox"
                checked={section.props.show_out_of_stock ?? true}
                onChange={(e) =>
                  onChange({
                    ...section,
                    props: { ...section.props, show_out_of_stock: e.target.checked },
                  })
                }
              />
              <span>Hiển thị cả biến thể tạm hết hàng trong bảng SKU</span>
            </label>
          </>
        )}

        {section.type === "testimonials" && (
          <>
            <div className={common.field}>
              <div className={styles.charRow}>
                <label htmlFor="sec-ts-title" className={common.fieldLabel}>
                  Tiêu đề khu vực đánh giá
                </label>
                <span className={styles.charCount}>{section.props.title.length}/120</span>
              </div>
              <input
                id="sec-ts-title"
                type="text"
                className={common.textInput}
                value={section.props.title}
                maxLength={120}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, title: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.title)}
            </div>
            <div className={common.field}>
              <label htmlFor="sec-ts-sub" className={common.fieldLabel}>
                Mô tả ngắn
              </label>
              <input
                id="sec-ts-sub"
                type="text"
                className={common.textInput}
                value={section.props.subtitle ?? ""}
                maxLength={300}
                onChange={(e) =>
                  onChange({ ...section, props: { ...section.props, subtitle: e.target.value } })
                }
              />
              {renderUnsafeWarning(section.props.subtitle)}
            </div>
            <p className={common.fieldHint}>
              Thêm các block loại &ldquo;Trích dẫn&rdquo; ở bên dưới để đưa nhận xét khách hàng vào
              khối.
            </p>
          </>
        )}

        {/* Full Block Editor (9 typed block types, max 12 blocks per section) */}
        <div className={styles.blocksEditorBox}>
          <div className={styles.blocksEditorHead}>
            <div>
              <h3 className={styles.blocksEditorTitle}>
                Block con bên trong khối ({blocks.length}/{MAX_BLOCKS_PER_SECTION})
              </h3>
              <p className={common.cardMeta}>
                Chèn nhãn, chỉ số, ảnh minh họa, trích dẫn hoặc nút CTA bổ sung theo cấu trúc an
                toàn.
              </p>
            </div>
          </div>

          <div className={styles.addBlockBar}>
            <select
              className={common.textInput}
              value={newBlockType}
              onChange={(e) => setNewBlockType(e.target.value as SiteBlockType)}
              aria-label="Chọn loại block cần thêm"
            >
              {BLOCK_TYPE_ORDER.map((bt) => (
                <option key={bt} value={bt}>
                  {BLOCK_TYPE_META[bt].label} — {BLOCK_TYPE_META[bt].description}
                </option>
              ))}
            </select>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={blocks.length >= MAX_BLOCKS_PER_SECTION}
              onClick={handleAddBlock}
            >
              <IconPlus size={14} />
              <span>Thêm block</span>
            </Button>
          </div>

          {blocks.length === 0 ? (
            <div className={styles.blocksEmpty}>
              Khối này chưa có block con nào. Chọn loại block ở trên để bổ sung chi tiết.
            </div>
          ) : (
            <div className={styles.blockList}>
              {blocks.map((blk, idx) => (
                <div key={blk.id} className={styles.blockCard}>
                  <div className={styles.blockCardHead}>
                    <div className={styles.blockCardTitleGroup}>
                      <span className={styles.blockTypePill}>
                        {BLOCK_TYPE_META[blk.type].label}
                      </span>
                      <span className={styles.blockIdMeta}>
                        #{idx + 1} · {blk.id}
                      </span>
                    </div>
                    <div className={styles.sectionMoveGroup}>
                      <button
                        type="button"
                        className={styles.miniBtn}
                        disabled={idx === 0}
                        onClick={() => handleMoveBlock(idx, -1)}
                      >
                        Lên
                      </button>
                      <button
                        type="button"
                        className={styles.miniBtn}
                        disabled={idx === blocks.length - 1}
                        onClick={() => handleMoveBlock(idx, 1)}
                      >
                        Xuống
                      </button>
                      <button
                        type="button"
                        className={`${styles.miniBtn} ${styles.miniBtnDanger}`}
                        onClick={() => handleDeleteBlock(blk.id)}
                      >
                        Xóa
                      </button>
                    </div>
                  </div>
                  {renderBlockFields(blk, idx)}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
