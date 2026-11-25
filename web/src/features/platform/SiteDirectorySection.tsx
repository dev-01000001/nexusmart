import React from "react";
import Link from "next/link";
import { StateView } from "@/components/StateView";
import type { DirectoryResult } from "./directory.server";
import { buildPageList } from "./directoryModel";
import { DirectoryErrorState, SiteDirectoryDoors } from "./SiteDirectoryDoors";
import styles from "./SiteDirectorySection.module.css";

export interface SiteDirectorySectionProps {
  directory: DirectoryResult;
}

function pageHref(page: number): string {
  return page <= 1 ? "/#danh-ba-gian-hang" : `/?trang=${page}#danh-ba-gian-hang`;
}

export function SiteDirectorySection({ directory }: SiteDirectorySectionProps) {
  const doors = directory.ok ? directory.sites.slice(0, 6) : [];
  const extra = directory.ok ? directory.sites.slice(6) : [];

  return (
    <section
      id="danh-ba-gian-hang"
      className={styles.band}
      aria-labelledby="platform-directory-heading"
    >
      <div className={styles.head}>
        <h2
          id="platform-directory-heading"
          className={`${styles.heading} type-wide`}
          data-reveal=""
        >
          Cửa hàng đang mở
        </h2>

        {directory.ok && directory.total > 0 && (
          <p className={`${styles.count} num`}>{directory.total} gian hàng</p>
        )}
      </div>

      {!directory.ok ? (
        <div className={styles.stateWrap}>
          <DirectoryErrorState />
        </div>
      ) : directory.sites.length === 0 ? (
        <div className={styles.stateWrap}>
          <StateView
            kind="empty"
            title="Chưa có cửa hàng nào xuất bản."
            actionLabel="Mở gian hàng"
            actionHref="/dang-ky?role=seller"
          />
        </div>
      ) : (
        <>
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
                    <span key={item} className={`${styles.pageNumActive} num`} aria-current="page">
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
    </section>
  );
}
