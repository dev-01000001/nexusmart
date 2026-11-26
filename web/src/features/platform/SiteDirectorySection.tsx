import React from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import type { DirectoryResult } from "./directory.server";
import { buildPageList, type DirectorySite } from "./directoryModel";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import { DirectoryErrorState } from "./SiteDirectoryDoors";
import { StorefrontStageMini } from "./StorefrontStageMini";

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

const MOSAIC_ROWS_CLS =
  "flex flex-col gap-[var(--home-gap)] [@media(max-width:960px)]:mr-[calc(var(--home-gutter)*-1)] [@media(max-width:960px)]:flex-row [@media(max-width:960px)]:gap-[var(--space-3)] [@media(max-width:960px)]:overflow-x-auto [@media(max-width:960px)]:pb-[var(--space-1)] [@media(max-width:960px)]:pr-[var(--home-gutter)] [@media(max-width:960px)]:[scroll-snap-type:x_mandatory] [@media(max-width:960px)]:[scrollbar-width:none] [@media(max-width:960px)]:[&::-webkit-scrollbar]:hidden";

const CARD_MEDIA_CLS =
  "relative h-[clamp(300px,26.5vw,381px)] w-full overflow-hidden rounded-[var(--home-radius-media)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] [@media(max-width:960px)]:h-[300px]";

const EMPTY_BAR_CLS =
  "flex flex-wrap items-center justify-between gap-x-[var(--space-6)] gap-y-[var(--space-4)] rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-[var(--space-6)] py-[var(--space-5)]";

const EMPTY_CTA_CLS =
  "inline-flex min-h-[var(--home-btn-header-h)] items-center justify-center whitespace-nowrap rounded-[var(--radius-pill)] bg-[var(--home-ink)] px-[var(--space-5)] py-0 text-[length:var(--home-fs-small)] font-[550] text-[var(--home-bg)] [transition:background-color_var(--t-hover)_var(--ease-out)] hover:bg-[var(--bg-2)]";

const PAGE_BTN_CLS =
  "inline-flex min-h-[var(--home-btn-header-h)] min-w-[var(--home-btn-header-h)] items-center justify-center rounded-[var(--radius-pill)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-[var(--space-4)] py-0 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink)] [transition:border-color_var(--t-hover)_var(--ease-out)] hover:border-[color:var(--home-border-strong)]";

const PAGE_BTN_DISABLED_CLS =
  "inline-flex min-h-[var(--home-btn-header-h)] min-w-[var(--home-btn-header-h)] items-center justify-center rounded-[var(--radius-pill)] [border:1px_solid_var(--home-border)] bg-[var(--home-glass)] px-[var(--space-4)] py-0 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink-dim)]";

function mosaicRowGridCols(cols: number): string {
  if (cols === 1) return "grid-cols-[minmax(0,1fr)]";
  if (cols === 2) return "grid-cols-[repeat(2,minmax(0,1fr))]";
  return "grid-cols-[minmax(0,41fr)_minmax(0,18fr)_minmax(0,41fr)]";
}

function chunkIntoRows<T>(items: readonly T[], size = 3): T[][] {
  const rows: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    rows.push(items.slice(i, i + size));
  }
  return rows;
}

function TemplateShowcaseMosaic() {
  return (
    <div className={MOSAIC_ROWS_CLS} data-total="3" data-reveal="rows">
      <div
        className={`grid gap-[var(--home-gap-sm)] [@media(max-width:960px)]:contents ${mosaicRowGridCols(3)}`}
        data-cols="3"
      >
        {MOSAIC_TEMPLATES.map(({ template, variant }) => (
          <article
            key={template.key}
            className={`flex min-w-0 flex-col gap-[var(--space-3)] text-[var(--home-ink)] [@media(max-width:960px)]:[scroll-snap-align:start] ${
              variant === "mobile"
                ? "[@media(max-width:960px)]:flex-[0_0_60%]"
                : "[@media(max-width:960px)]:flex-[0_0_78%]"
            }`}
          >
            <div className={CARD_MEDIA_CLS}>
              <StorefrontStageMini
                template={template}
                variant={variant}
                ariaLabel={`Minh họa cửa hàng theo mẫu giao diện ${template.label}`}
              />
            </div>

            <div className="flex min-w-0 flex-col gap-[var(--space-1)]">
              <p className="text-[length:var(--home-fs-small)] font-[420] leading-[1.4] text-[var(--home-ink-3)]">
                Mẫu giao diện · {template.label}
              </p>
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
      <div className={MOSAIC_ROWS_CLS} data-total={doors.length} data-reveal="rows">
        {rows.map((row, rowIdx) => (
          <div
            key={`row-${rowIdx}`}
            className={`grid gap-[var(--home-gap-sm)] [@media(max-width:960px)]:contents ${mosaicRowGridCols(row.length)}`}
            data-cols={row.length}
          >
            {row.map((site, colIdx) => {
              const globalIdx = rowIdx * 3 + colIdx;
              const isNarrow = row.length === 3 && colIdx === 1;
              const fallbackTpl = SHOWCASE_TEMPLATES[globalIdx % SHOWCASE_TEMPLATES.length];
              const mobileWidthCls =
                doors.length === 1
                  ? "[@media(max-width:960px)]:flex-[0_0_100%]"
                  : isNarrow
                    ? "[@media(max-width:960px)]:flex-[0_0_60%]"
                    : "[@media(max-width:960px)]:flex-[0_0_78%]";

              return (
                <a
                  key={site.siteId}
                  href={site.origin}
                  className={`group flex min-w-0 flex-col gap-[var(--space-3)] text-[var(--home-ink)] [@media(max-width:960px)]:[scroll-snap-align:start] ${mobileWidthCls}`}
                >
                  <div className={CARD_MEDIA_CLS}>
                    {site.coverUrl ? (
                      <img
                        src={site.coverUrl}
                        alt={`Ảnh bìa cửa hàng ${site.name}`}
                        width={960}
                        height={720}
                        loading="lazy"
                        className="h-full w-full object-cover object-[center_20%] [transition:transform_var(--t-page)_var(--ease-expo)] group-hover:[transform:scale(1.03)] group-focus-visible:[transform:scale(1.03)]"
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

                  <div className="flex min-w-0 flex-col gap-[var(--space-1)]">
                    <div className="flex items-center justify-between gap-[var(--space-3)]">
                      <div className="inline-flex min-w-0 items-center gap-[var(--space-2)]">
                        {site.logoUrl && (
                          <img
                            src={site.logoUrl}
                            alt=""
                            width={22}
                            height={22}
                            loading="lazy"
                            className="h-[var(--space-5)] w-[var(--space-5)] flex-none rounded-[var(--home-radius-media)] bg-[var(--home-ink)] object-cover"
                            aria-hidden="true"
                          />
                        )}
                        <h3 className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-body)] font-medium leading-[1.35] text-[var(--home-ink)]">
                          {site.name}
                        </h3>
                      </div>
                      <span
                        className="inline-flex flex-none text-[var(--home-ink-3)] [transition:transform_var(--t-hover)_var(--ease-expo),color_var(--t-hover)_var(--ease-out)] group-hover:[transform:translate(2px,-2px)] group-hover:text-[var(--home-accent)] group-focus-visible:[transform:translate(2px,-2px)] group-focus-visible:text-[var(--home-accent)]"
                        aria-hidden="true"
                      >
                        <IconArrowUpRight size={16} />
                      </span>
                    </div>
                    <p className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-small)] leading-[1.4] text-[var(--home-ink-3)]">
                      {site.host}
                    </p>
                  </div>
                </a>
              );
            })}
          </div>
        ))}
      </div>

      {extra.length > 0 && (
        <ul className="flex flex-col gap-[var(--space-2)]" data-reveal="rows">
          {extra.map((site) => (
            <li key={site.siteId}>
              <a
                href={site.origin}
                className="group flex items-center justify-between gap-[var(--space-4)] rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-[var(--space-5)] py-[var(--space-4)] text-[var(--home-ink)] [transition:border-color_var(--t-hover)_var(--ease-out)] hover:border-[color:var(--home-border-strong)] focus-visible:border-[color:var(--home-border-strong)] [@media(max-width:960px)]:flex-col [@media(max-width:960px)]:items-start [@media(max-width:960px)]:gap-[var(--space-2)]"
              >
                <div className="flex min-w-0 flex-1 flex-col gap-[var(--space-1)]">
                  <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-body)] font-medium text-[var(--home-ink)]">
                    {site.name}
                  </span>
                  {site.tagline && (
                    <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-small)] text-[var(--home-ink-2)]">
                      {site.tagline}
                    </span>
                  )}
                </div>

                <div className="inline-flex flex-none items-center gap-[var(--space-3)]">
                  <span className="text-[length:var(--home-fs-small)] text-[var(--home-ink-3)]">
                    {site.host}
                  </span>
                  <IconArrowUpRight
                    size={16}
                    className="text-[var(--home-accent)] [transition:transform_var(--t-hover)_var(--ease-expo)] group-hover:[transform:translate(2px,-2px)] group-focus-visible:[transform:translate(2px,-2px)]"
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
      className="relative z-[2] mt-[calc(var(--home-radius-band)*-1)] w-full scroll-mt-[var(--home-header-h)] rounded-t-[var(--home-radius-band)] bg-[var(--home-band)] pb-0 pt-[clamp(56px,7vw,96px)] text-[var(--home-ink)] [@media(max-width:960px)]:mt-[calc(var(--home-radius-band-sm)*-1)] [@media(max-width:960px)]:rounded-t-[var(--home-radius-band-sm)] [@media(max-width:960px)]:pt-[var(--space-8)]"
      aria-labelledby="platform-directory-heading"
    >
      <div className="mx-auto flex w-[min(var(--home-max),100%_-_2_*_var(--home-gutter))] flex-col gap-[var(--home-space-head)]">
        <div className="flex flex-wrap items-end justify-between gap-x-[var(--space-7)] gap-y-[var(--space-4)]">
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
            <p className="num whitespace-nowrap text-[length:var(--home-fs-small)] font-[450] text-[var(--home-ink-2)]">
              {directory.total} gian hàng
            </p>
          )}
        </div>

        {!directory.ok ? (
          <div className="flex flex-col gap-[var(--home-gap)]">
            <DirectoryErrorState />
            <TemplateShowcaseMosaic />
          </div>
        ) : isOutOfRangePage ? (
          <div className={EMPTY_BAR_CLS}>
            <p className="text-[length:var(--home-fs-body)] leading-[1.5] text-[var(--home-ink-2)]">
              Trang này không có cửa hàng nào.
            </p>
            <Link href={pageHref(1)} className={EMPTY_CTA_CLS}>
              Về trang đầu
            </Link>
          </div>
        ) : !hasRealSites ? (
          <div className="flex flex-col gap-[var(--home-gap)]">
            <TemplateShowcaseMosaic />
            <div className={EMPTY_BAR_CLS}>
              <p className="text-[length:var(--home-fs-body)] leading-[1.5] text-[var(--home-ink-2)]">
                Chưa có cửa hàng nào xuất bản. Cửa hàng đầu tiên có thể là của bạn.
              </p>
              <Link href="/dang-ky?role=seller" className={EMPTY_CTA_CLS}>
                Mở gian hàng
              </Link>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-[var(--home-gap)]">
            <RealSiteMosaic doors={doors} extra={extra} />

            {directory.totalPages > 1 && (
              <nav
                className="flex flex-wrap items-center justify-between gap-[var(--space-3)] pt-[var(--space-2)]"
                aria-label="Phân trang danh bạ gian hàng"
              >
                {directory.page > 1 ? (
                  <Link href={pageHref(directory.page - 1)} className={PAGE_BTN_CLS}>
                    Trang trước
                  </Link>
                ) : (
                  <span className={PAGE_BTN_DISABLED_CLS} aria-disabled="true">
                    Trang trước
                  </span>
                )}

                <div className="flex flex-wrap items-center gap-[var(--space-2)]">
                  {buildPageList(directory.page, directory.totalPages).map((item, idx) =>
                    item === null ? (
                      <span
                        key={`gap-${idx}`}
                        className="px-[var(--space-2)] py-0 text-[length:var(--home-fs-small)] text-[var(--home-ink-3)]"
                        aria-hidden="true"
                      >
                        …
                      </span>
                    ) : item === directory.page ? (
                      <span
                        key={item}
                        className="num inline-flex min-h-[var(--home-btn-header-h)] min-w-[var(--home-btn-header-h)] items-center justify-center rounded-[var(--radius-pill)] bg-[var(--home-ink)] px-[var(--space-4)] py-0 text-[length:var(--home-fs-small)] font-semibold text-[var(--home-bg)]"
                        aria-current="page"
                      >
                        {item}
                      </span>
                    ) : (
                      <Link key={item} href={pageHref(item)} className={`${PAGE_BTN_CLS} num`}>
                        {item}
                      </Link>
                    )
                  )}
                </div>

                {directory.page < directory.totalPages ? (
                  <Link href={pageHref(directory.page + 1)} className={PAGE_BTN_CLS}>
                    Trang sau
                  </Link>
                ) : (
                  <span className={PAGE_BTN_DISABLED_CLS} aria-disabled="true">
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
