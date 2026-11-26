import React from "react";
import Image from "next/image";
import Link from "next/link";
import type { DirectoryResult } from "./directory.server";
import { buildPageList } from "./directoryModel";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import { DirectoryErrorState, SiteDirectoryDoors } from "./SiteDirectoryDoors";
import styles from "./SiteDirectorySection.module.css";

export interface SiteDirectorySectionProps {
  directory: DirectoryResult;
}

function pageHref(page: number): string {
  return page <= 1 ? "/#danh-ba-gian-hang" : `/?trang=${page}#danh-ba-gian-hang`;
}

function TemplateShowcaseMosaic() {
  return (
    <div className={styles.mosaic} data-count={SHOWCASE_TEMPLATES.length} data-reveal="rows">
      {SHOWCASE_TEMPLATES.map((tpl, idx) => {
        // Alternate landscape/portrait assets so narrow slots (1, 3) frame cleanly without repeating hero photo
        const useSecondary = idx === 3;
        const imgSrc = useSecondary ? tpl.secondaryImg : tpl.primaryImg;
        const imgAlt = useSecondary ? tpl.secondaryAlt : tpl.primaryAlt;

        return (
          <article key={tpl.key} className={styles.templateCard}>
            <div className={styles.cardMedia} style={{ background: tpl.bg }}>
              <Image
                src={imgSrc}
                alt={imgAlt}
                fill
                sizes="(max-width: 900px) 78vw, (max-width: 1200px) 32vw, 360px"
                className={styles.cardImg}
              />
            </div>

            <div className={styles.cardMeta}>
              <p className={styles.templateLabel}>Mẫu giao diện · {tpl.label}</p>
              <div className={styles.cardTitleRow}>
                <h3 className={styles.cardName}>{tpl.defaultBrand}</h3>
                <div
                  className={styles.paletteDots}
                  role="img"
                  aria-label={`Bảng màu mẫu ${tpl.label}`}
                >
                  {[tpl.bg, tpl.surface, tpl.accent].map((color, cIdx) => (
                    <span
                      key={`${tpl.key}-${cIdx}`}
                      className={styles.paletteDot}
                      style={{ background: color }}
                      aria-hidden="true"
                    />
                  ))}
                </div>
              </div>
            </div>
          </article>
        );
      })}
    </div>
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
              Giao diện riêng. Khách riêng. Thanh toán ngay trên cửa hàng.
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
            <SiteDirectoryDoors doors={doors} extra={extra} />

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
