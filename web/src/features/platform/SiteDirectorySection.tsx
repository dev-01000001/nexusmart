import React from "react";
import { IconArrowUpRight } from "@/components/Icons";
import type { DirectoryResult } from "./directory.server";
import { homeDirectoryPageHref } from "./directoryHref";
import type { DirectorySite } from "./directoryModel";
import {
  mosaicRowGridCols,
  SITE_MOSAIC_ROWS_CLS,
  SiteDirectoryCard,
  SiteDirectoryEmpty,
  SiteDirectoryErrorBlock,
  SiteDirectoryOutOfRange,
  SiteDirectoryPagination,
} from "./SiteDirectoryShared";

export interface SiteDirectorySectionProps {
  directory: DirectoryResult;
}

function chunkIntoRows<T>(items: readonly T[], size = 3): T[][] {
  const rows: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    rows.push(items.slice(i, i + size));
  }
  return rows;
}

function RealSiteMosaic({ doors, extra }: { doors: DirectorySite[]; extra: DirectorySite[] }) {
  const rows = chunkIntoRows(doors, 3);

  return (
    <>
      <div className={SITE_MOSAIC_ROWS_CLS} data-total={doors.length} data-reveal="rows">
        {rows.map((row, rowIdx) => (
          <div
            key={`row-${rowIdx}`}
            className={`grid gap-[var(--home-gap-sm)] [@media(max-width:960px)]:contents ${mosaicRowGridCols(row.length)}`}
            data-cols={row.length}
          >
            {row.map((site, colIdx) => {
              const globalIdx = rowIdx * 3 + colIdx;
              const isNarrow = row.length === 3 && colIdx === 1;
              const mobileWidthCls =
                doors.length === 1
                  ? "[@media(max-width:960px)]:flex-[0_0_100%]"
                  : isNarrow
                    ? "[@media(max-width:960px)]:flex-[0_0_60%]"
                    : "[@media(max-width:960px)]:flex-[0_0_78%]";

              return (
                <SiteDirectoryCard
                  key={site.siteId}
                  site={site}
                  fallbackIndex={globalIdx}
                  className={`[@media(max-width:960px)]:[scroll-snap-align:start] ${mobileWidthCls}`}
                />
              );
            })}
          </div>
        ))}
      </div>

      {extra.length > 0 && (
        <ul className="flex flex-col gap-2" data-reveal="rows">
          {extra.map((site) => (
            <li key={site.siteId}>
              <a
                href={site.origin}
                className="group flex items-center justify-between gap-4 rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-5 py-4 text-[var(--home-ink)] [transition:border-color_200ms_cubic-bezier(0.22,1,0.36,1)] hover:border-[color:var(--home-border-strong)] focus-visible:border-[color:var(--home-border-strong)] [@media(max-width:960px)]:flex-col [@media(max-width:960px)]:items-start [@media(max-width:960px)]:gap-2"
              >
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-body)] font-medium text-[var(--home-ink)]">
                    {site.name}
                  </span>
                  {site.tagline && (
                    <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-small)] text-[var(--home-ink-2)]">
                      {site.tagline}
                    </span>
                  )}
                </div>

                <div className="inline-flex flex-none items-center gap-3">
                  <span className="text-[length:var(--home-fs-small)] text-[var(--home-ink-3)]">
                    {site.host}
                  </span>
                  <IconArrowUpRight
                    size={16}
                    className="text-[var(--home-accent)] [transition:transform_200ms_cubic-bezier(0.16,1,0.3,1)] group-hover:[transform:translate(2px,-2px)] group-focus-visible:[transform:translate(2px,-2px)]"
                  />
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
      className="relative z-[2] mt-[calc(var(--home-radius-band)*-1)] w-full scroll-mt-[var(--home-header-h)] rounded-t-[var(--home-radius-band)] bg-[var(--home-band)] pb-0 pt-[clamp(56px,7vw,96px)] text-[var(--home-ink)] [@media(max-width:960px)]:mt-[calc(var(--home-radius-band-sm)*-1)] [@media(max-width:960px)]:rounded-t-[var(--home-radius-band-sm)] [@media(max-width:960px)]:pt-12"
      aria-labelledby="platform-directory-heading"
    >
      <div className="mx-auto flex w-[min(var(--home-max),100%_-_2_*_var(--home-gutter))] flex-col gap-[var(--home-space-head)]">
        <div className="flex flex-wrap items-end justify-between gap-x-8 gap-y-4">
          <h2
            id="platform-directory-heading"
            className="max-w-[1080px] font-[family-name:var(--home-font)] text-[length:var(--home-fs-h2)] font-light leading-[1.1] tracking-[-0.025em] [text-wrap:pretty]"
            data-reveal=""
          >
            <span className="text-[var(--home-ink)]">Bán trên địa chỉ của riêng bạn.</span>{" "}
            <span className="text-[var(--home-ink-dim)] supports-[animation-timeline:view()]:motion-safe:[animation-fill-mode:both] supports-[animation-timeline:view()]:motion-safe:[animation-name:nm-home-brighten-copy] supports-[animation-timeline:view()]:motion-safe:[animation-range:entry_20%_cover_46%] supports-[animation-timeline:view()]:motion-safe:[animation-timeline:view()] supports-[animation-timeline:view()]:motion-safe:[animation-timing-function:linear]">
              Giao diện riêng. Khách riêng. Giỏ hàng và thanh toán ngay trên cửa hàng.
            </span>
          </h2>

          {directory.ok && directory.total > 0 && (
            <p className="tabular-nums whitespace-nowrap text-[length:var(--home-fs-small)] font-[450] text-[var(--home-ink-2)]">
              {directory.total} gian hàng
            </p>
          )}
        </div>

        {!directory.ok ? (
          <SiteDirectoryErrorBlock />
        ) : isOutOfRangePage ? (
          <SiteDirectoryOutOfRange firstPageHref={homeDirectoryPageHref(1)} />
        ) : !hasRealSites ? (
          <SiteDirectoryEmpty />
        ) : (
          <div className="flex flex-col gap-[var(--home-gap)]">
            <RealSiteMosaic doors={doors} extra={extra} />
            <SiteDirectoryPagination
              page={directory.page}
              totalPages={directory.totalPages}
              pageHref={homeDirectoryPageHref}
            />
          </div>
        )}
      </div>
    </section>
  );
}
