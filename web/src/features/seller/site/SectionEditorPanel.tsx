"use client";

import React from "react";
import { Button, IconPlus } from "@/components";
import { formatVnd } from "@/lib/format";
import type { GwCategory, GwSellerProduct } from "@/features/seller/sellerApiTypes";
import common from "@/features/seller/SellerCommon.module.css";
import type { PolicyItem, SiteSection } from "./siteEditorTypes";
import { QUICK_MEDIA_ASSETS, SECTION_TYPE_META, validateSafeUrlClient } from "./siteEditorUtils";
import styles from "./SellerSiteEditor.module.css";

export interface SectionEditorPanelProps {
  section: SiteSection | null;
  products: GwSellerProduct[];
  categories: GwCategory[];
  onChange: (updated: SiteSection) => void;
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
  if (!section) {
    return (
      <div className={common.card}>
        <div className={common.cardHeader}>
          <h2 className={common.cardTitle}>Chi tiết khối nội dung</h2>
        </div>
        <div className={common.inlineEmpty}>
          Chọn một khối ở cột bên trái để chỉnh sửa nội dung, hoặc thêm khối mới.
        </div>
      </div>
    );
  }

  const meta = SECTION_TYPE_META[section.type];

  const renderUrlField = (
    id: string,
    label: string,
    value: string,
    onUpdate: (next: string) => void,
    kind: "image" | "href"
  ) => {
    const err = validateSafeUrlClient(value);
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
            </div>

            {renderUrlField(
              "sec-hero-img",
              "Ảnh chủ đạo (tùy chọn)",
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
      </div>
    </div>
  );
}
