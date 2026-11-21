import React from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import { StateView } from "@/components/StateView";
import type { DirectoryResult } from "./directory.server";
import { buildPageList, type DirectorySite } from "./directoryModel";
import styles from "./SiteDirectorySection.module.css";

export interface SiteDirectorySectionProps {
  directory: DirectoryResult;
}

function pageHref(page: number): string {
  return page <= 1 ? "/#danh-ba-gian-hang" : `/?trang=${page}#danh-ba-gian-hang`;
}

function SiteCard({ site }: { site: DirectorySite }) {
  const monogram = site.name.trim().slice(0, 1).toUpperCase() || "N";

  return (
    <a href={site.origin} className={styles.card} data-reveal="">
      <div className={styles.coverWrap}>
        {site.coverUrl ? (
          <img
            src={site.coverUrl}
            alt={`Ảnh bìa cửa hàng ${site.name}`}
            width={1280}
            height={720}
            loading="lazy"
            className={styles.coverImg}
          />
        ) : (
          <div className={styles.coverFallback} aria-hidden="true">
            <span className={styles.coverInitial}>{monogram}</span>
          </div>
        )}
      </div>

      <div className={styles.cardBody}>
        <div className={styles.brandRow}>
          {site.logoUrl ? (
            <span className={styles.logoFrame}>
              <img
                src={site.logoUrl}
                alt={`Biểu trưng ${site.name}`}
                width={44}
                height={44}
                loading="lazy"
                className={styles.logoImg}
              />
            </span>
          ) : (
            <span className={styles.logoInitial} aria-hidden="true">
              {monogram}
            </span>
          )}

          <div className={styles.brandMeta}>
            <h3 className={styles.siteName}>{site.name}</h3>
            <p className={styles.siteHost}>{site.host}</p>
          </div>
        </div>

        <p className={styles.tagline}>
          {site.tagline || "Cửa hàng chính thức vận hành độc lập trên nền tảng NexusMart."}
        </p>

        <span className={styles.visitLine}>
          <span>Ghé cửa hàng</span>
          <IconArrowUpRight size={15} />
        </span>
      </div>
    </a>
  );
}

export function SiteDirectorySection({ directory }: SiteDirectorySectionProps) {
  const countText =
    directory.ok && directory.total > 0
      ? `${directory.total} cửa hàng đang mở bán`
      : "Danh sách cập nhật trực tiếp từ hệ thống";

  return (
    <section
      id="danh-ba-gian-hang"
      className={styles.band}
      aria-labelledby="platform-directory-heading"
    >
      <div className={styles.inner}>
        <div className={styles.head} data-reveal="">
          <div className={styles.headCopy}>
            <h2 id="platform-directory-heading" className={styles.heading}>
              Danh bạ gian hàng đang mở bán
            </h2>
            <p className={styles.lead}>
              Mỗi cửa hàng dưới đây vận hành trên địa chỉ riêng. Chọn một cửa hàng để xem sản phẩm,
              chương trình ưu đãi và đặt mua trực tiếp với người bán.
            </p>
          </div>
          <p className={`${styles.count} num`}>{countText}</p>
        </div>

        {!directory.ok ? (
          <StateView
            kind="error"
            title="Chưa thể tải danh bạ gian hàng lúc này"
            message="Hệ thống đang tạm gián đoạn kết nối. Bạn có thể tải lại trang sau ít phút."
            actionLabel="Tải lại trang"
            actionHref="/#danh-ba-gian-hang"
          />
        ) : directory.sites.length === 0 ? (
          <StateView
            kind="empty"
            title="Chưa có cửa hàng nào xuất bản công khai"
            message="Khi gian hàng hoàn tất thiết lập và bấm xuất bản trong Kênh người bán, cửa hàng sẽ xuất hiện tại danh bạ này."
            actionLabel="Mở gian hàng"
            actionHref="/dang-ky?role=seller"
          />
        ) : (
          <>
            <div className={styles.grid}>
              {directory.sites.map((site) => (
                <SiteCard key={site.siteId} site={site} />
              ))}
            </div>

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
                        ...
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
          </>
        )}
      </div>
    </section>
  );
}
