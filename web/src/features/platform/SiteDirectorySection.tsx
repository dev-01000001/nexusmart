import React from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import type { DirectoryResult } from "./directory.server";
import { buildPageList, type DirectorySite } from "./directoryModel";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import { DirectoryErrorState } from "./SiteDirectoryDoors";
import { StorefrontStageMini } from "./StorefrontStageMini";
import styles from "./SiteDirectorySection.module.css";

export interface SiteDirectorySectionProps {
  directory: DirectoryResult;
}

function pageHref(page: number): string {
  return page <= 1 ? "/#danh-ba-gian-hang" : `/?trang=${page}#danh-ba-gian-hang`;
}

const MOSAIC_TEMPLATES = [
  { template: SHOWCASE_TEMPLATES[0], variant: "desktop" as const },
  { template: SHOWCASE_TEMPLATES[1], variant: "mobile" as const },
  { template: SHOWCASE_TEMPLATES[2], variant: "desktop" as const },
] as const;

function chunkIntoRows<T>(items: readonly T[], size = 3): T[][] {
  const rows: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    rows.push(items.slice(i, i + size));
  }
  return rows;
}

function TemplateShowcaseMosaic() {
  return (
    <div className={styles.mosaicRows} data-total="3" data-reveal="rows">
      <div className={styles.mosaicRow} data-cols="3">
        {MOSAIC_TEMPLATES.map(({ template, variant }) => (
          <article
            key={template.key}
            className={`${styles.templateCard} ${variant === "mobile" ? styles.cardNarrow : styles.cardWide}`}
          >
            <div className={styles.cardMedia}>
              <StorefrontStageMini
                template={template}
                variant={variant}
                ariaLabel={`Minh họa cửa hàng theo mẫu giao diện ${template.label}`}
              />
            </div>

            <div className={styles.cardMeta}>
              <p className={styles.templateLabel}>Mẫu giao diện · {template.label}</p>
            </div>
          </article>
        ))}
      </div>
    </div>
  );
}

function RealSiteMosaic({ doors, extra }: { doors: DirectorySite[]; extra: DirectorySite[] }) {
  const rows = chunkIntoRows(doors, 3);

  return (
    <>
      <div className={styles.mosaicRows} data-total={doors.length} data-reveal="rows">
        {rows.map((row, rowIdx) => (
          <div key={`row-${rowIdx}`} className={styles.mosaicRow} data-cols={row.length}>
            {row.map((site, colIdx) => {
              const globalIdx = rowIdx * 3 + colIdx;
              const isNarrow = row.length === 3 && colIdx === 1;
              const fallbackTpl = SHOWCASE_TEMPLATES[globalIdx % SHOWCASE_TEMPLATES.length];

              return (
                <a
                  key={site.siteId}
                  href={site.origin}
                  className={`${styles.siteCard} ${isNarrow ? styles.cardNarrow : styles.cardWide}`}
                >
                  <div className={styles.cardMedia}>
                    {site.coverUrl ? (
                      <img
                        src={site.coverUrl}
                        alt={`Ảnh bìa cửa hàng ${site.name}`}
                        width={960}
                        height={720}
                        loading="lazy"
                        className={styles.cardImg}
                      />
                    ) : (
                      <StorefrontStageMini
                        template={fallbackTpl}
                        variant="site-fallback"
                        brandName={site.name}
                        headline={site.tagline || undefined}
                        logoUrl={site.logoUrl || undefined}
                        decorative
                      />
                    )}
                  </div>

                  <div className={styles.cardMeta}>
                    <div className={styles.cardTitleRow}>
                      <div className={styles.cardIdentity}>
                        {site.logoUrl && (
                          <img
                            src={site.logoUrl}
                            alt=""
                            width={22}
                            height={22}
                            loading="lazy"
                            className={styles.cardLogo}
                            aria-hidden="true"
                          />
                        )}
                        <h3 className={styles.cardName}>{site.name}</h3>
                      </div>
                      <span className={styles.cardArrow} aria-hidden="true">
                        <IconArrowUpRight size={16} />
                      </span>
                    </div>
                    <p className={styles.cardHost}>{site.host}</p>
                  </div>
                </a>
              );
            })}
          </div>
        ))}
      </div>

      {extra.length > 0 && (
        <ul className={styles.extraList} data-reveal="rows">
          {extra.map((site) => (
            <li key={site.siteId}>
              <a href={site.origin} className={styles.extraRow}>
                <div className={styles.extraMain}>
                  <span className={styles.extraName}>{site.name}</span>
                  {site.tagline && <span className={styles.extraTagline}>{site.tagline}</span>}
                </div>

                <div className={styles.extraRight}>
                  <span className={styles.extraHost}>{site.host}</span>
                  <IconArrowUpRight size={16} className={styles.extraArrow} />
                </div>
              </a>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

export function SiteDirectorySection({ directory }: SiteDirectorySectionProps) {
  const hasRealSites = directory.ok && directory.sites.length > 0;
  const isOutOfRangePage = directory.ok && directory.total > 0 && directory.sites.length === 0;
  const doors = hasRealSites ? directory.sites.slice(0, 6) : [];
  const extra = hasRealSites ? directory.sites.slice(6) : [];

  return (
    <section
      id="danh-ba-gian-hang"
      className={styles.band}
      aria-labelledby="platform-directory-heading"
    >
      <div className={styles.inner}>
        <div className={styles.head}>
          <h2 id="platform-directory-heading" className={styles.heading} data-reveal="">
            <span className={styles.headingPrimary}>Bán trên địa chỉ của riêng bạn.</span>{" "}
            <span className={styles.headingSecondary}>
              Giao diện riêng. Khách riêng. Giỏ hàng và thanh toán ngay trên cửa hàng.
            </span>
          </h2>

          {directory.ok && directory.total > 0 && (
            <p className={`${styles.count} num`}>{directory.total} gian hàng</p>
          )}
        </div>

        {!directory.ok ? (
          <div className={styles.stack}>
            <DirectoryErrorState />
            <TemplateShowcaseMosaic />
          </div>
        ) : isOutOfRangePage ? (
          <div className={styles.emptyBar}>
            <p className={styles.emptyCopy}>Trang này không có cửa hàng nào.</p>
            <Link href={pageHref(1)} className={styles.emptyCta}>
              Về trang đầu
            </Link>
          </div>
        ) : !hasRealSites ? (
          <div className={styles.stack}>
            <TemplateShowcaseMosaic />
            <div className={styles.emptyBar}>
              <p className={styles.emptyCopy}>
                Chưa có cửa hàng nào xuất bản. Cửa hàng đầu tiên có thể là của bạn.
              </p>
              <Link href="/dang-ky?role=seller" className={styles.emptyCta}>
                Mở gian hàng
              </Link>
            </div>
          </div>
        ) : (
          <div className={styles.stack}>
            <RealSiteMosaic doors={doors} extra={extra} />

            {directory.totalPages > 1 && (
              <nav className={styles.pager} aria-label="Phân trang danh bạ gian hàng">
                {directory.page > 1 ? (
                  <Link href={pageHref(directory.page - 1)} className={styles.pageBtn}>
                    Trang trước
                  </Link>
                ) : (
                  <span className={styles.pageBtnDisabled} aria-disabled="true">
                    Trang trước
                  </span>
                )}

                <div className={styles.pageNumbers}>
                  {buildPageList(directory.page, directory.totalPages).map((item, idx) =>
                    item === null ? (
                      <span key={`gap-${idx}`} className={styles.pageGap} aria-hidden="true">
                        …
                      </span>
                    ) : item === directory.page ? (
                      <span
                        key={item}
                        className={`${styles.pageNumActive} num`}
                        aria-current="page"
                      >
                        {item}
                      </span>
                    ) : (
                      <Link key={item} href={pageHref(item)} className={`${styles.pageNum} num`}>
                        {item}
                      </Link>
                    )
                  )}
                </div>

                {directory.page < directory.totalPages ? (
                  <Link href={pageHref(directory.page + 1)} className={styles.pageBtn}>
                    Trang sau
                  </Link>
                ) : (
                  <span className={styles.pageBtnDisabled} aria-disabled="true">
                    Trang sau
                  </span>
                )}
              </nav>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
