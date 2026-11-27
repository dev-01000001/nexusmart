import React from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import { buildPageList, type DirectorySite } from "./directoryModel";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import { DirectoryErrorState } from "./SiteDirectoryDoors";
import { StorefrontStageMini } from "./StorefrontStageMini";

export const SITE_MOSAIC_ROWS_CLS =
  "flex flex-col gap-[var(--home-gap)] [@media(max-width:960px)]:mr-[calc(var(--home-gutter)*-1)] [@media(max-width:960px)]:flex-row [@media(max-width:960px)]:gap-3 [@media(max-width:960px)]:overflow-x-auto [@media(max-width:960px)]:pb-1 [@media(max-width:960px)]:pr-[var(--home-gutter)] [@media(max-width:960px)]:[scroll-snap-type:x_mandatory] [@media(max-width:960px)]:[scrollbar-width:none] [@media(max-width:960px)]:[&::-webkit-scrollbar]:hidden";

export const SITE_CARD_MEDIA_CLS =
  "relative h-[clamp(300px,26.5vw,381px)] w-full overflow-hidden rounded-[var(--home-radius-media)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] [@media(max-width:960px)]:h-[300px]";

export const SITE_GRID_MEDIA_CLS =
  "relative aspect-[4/3] w-full overflow-hidden rounded-[var(--home-radius-media)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)]";

const EMPTY_BAR_CLS =
  "flex flex-wrap items-center justify-between gap-x-6 gap-y-4 rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-6 py-5";

const EMPTY_CTA_CLS =
  "inline-flex min-h-[var(--home-btn-header-h)] items-center justify-center whitespace-nowrap rounded-full bg-[var(--home-ink)] px-5 py-0 text-[length:var(--home-fs-small)] font-[550] text-[var(--home-bg)] [transition:background-color_200ms_cubic-bezier(0.22,1,0.36,1)] hover:bg-[#eff3f5]";

const PAGE_BTN_CLS =
  "inline-flex min-h-[var(--home-btn-header-h)] min-w-[var(--home-btn-header-h)] items-center justify-center rounded-full [border:1px_solid_var(--home-border)] bg-[var(--home-card)] px-4 py-0 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink)] [transition:border-color_200ms_cubic-bezier(0.22,1,0.36,1)] hover:border-[color:var(--home-border-strong)]";

const PAGE_BTN_DISABLED_CLS =
  "inline-flex min-h-[var(--home-btn-header-h)] min-w-[var(--home-btn-header-h)] items-center justify-center rounded-full [border:1px_solid_var(--home-border)] bg-[var(--home-glass)] px-4 py-0 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink-dim)]";

const MOSAIC_TEMPLATES = [
  { template: SHOWCASE_TEMPLATES[0], variant: "desktop" as const },
  { template: SHOWCASE_TEMPLATES[1], variant: "mobile" as const },
  { template: SHOWCASE_TEMPLATES[2], variant: "desktop" as const },
] as const;

export function mosaicRowGridCols(cols: number): string {
  if (cols === 1) return "grid-cols-[minmax(0,1fr)]";
  if (cols === 2) return "grid-cols-[repeat(2,minmax(0,1fr))]";
  return "grid-cols-[minmax(0,41fr)_minmax(0,18fr)_minmax(0,41fr)]";
}

export interface SiteDirectoryCardProps {
  site: DirectorySite;
  fallbackIndex: number;
  className?: string;
  mediaClassName?: string;
  imageLoading?: "lazy" | "eager";
}

export function SiteDirectoryCard({
  site,
  fallbackIndex,
  className,
  mediaClassName = SITE_CARD_MEDIA_CLS,
  imageLoading = "lazy",
}: SiteDirectoryCardProps) {
  const fallbackTpl = SHOWCASE_TEMPLATES[fallbackIndex % SHOWCASE_TEMPLATES.length];

  return (
    <a
      href={site.origin}
      className={
        className
          ? `group flex min-w-0 flex-col gap-3 text-[var(--home-ink)] ${className}`
          : "group flex min-w-0 flex-col gap-3 text-[var(--home-ink)]"
      }
    >
      <div className={mediaClassName}>
        {site.coverUrl ? (
          <img
            src={site.coverUrl}
            alt={`Ảnh bìa cửa hàng ${site.name}`}
            width={960}
            height={720}
            loading={imageLoading}
            className="h-full w-full object-cover object-[center_20%] [transition:transform_320ms_cubic-bezier(0.16,1,0.3,1)] group-hover:[transform:scale(1.03)] group-focus-visible:[transform:scale(1.03)]"
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

      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex items-center justify-between gap-3">
          <div className="inline-flex min-w-0 items-center gap-2">
            {site.logoUrl && (
              <img
                src={site.logoUrl}
                alt=""
                width={22}
                height={22}
                loading={imageLoading}
                className="h-5 w-5 flex-none rounded-[var(--home-radius-media)] bg-[var(--home-ink)] object-cover"
                aria-hidden="true"
              />
            )}
            <h3 className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-body)] font-medium leading-[1.35] text-[var(--home-ink)]">
              {site.name}
            </h3>
          </div>
          <span
            className="inline-flex flex-none text-[var(--home-ink-3)] [transition:transform_200ms_cubic-bezier(0.16,1,0.3,1),color_200ms_cubic-bezier(0.22,1,0.36,1)] group-hover:[transform:translate(2px,-2px)] group-hover:text-[var(--home-accent)] group-focus-visible:[transform:translate(2px,-2px)] group-focus-visible:text-[var(--home-accent)]"
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
}

export interface SiteDirectoryGridProps {
  sites: readonly DirectorySite[];
  imageLoading?: "lazy" | "eager";
}

export function SiteDirectoryGrid({ sites, imageLoading = "eager" }: SiteDirectoryGridProps) {
  return (
    <div
      className="grid grid-cols-1 gap-x-[var(--home-gap)] gap-y-10 min-[641px]:grid-cols-2 min-[1025px]:grid-cols-3"
      data-total={sites.length}
    >
      {sites.map((site, idx) => (
        <SiteDirectoryCard
          key={site.siteId}
          site={site}
          fallbackIndex={idx}
          mediaClassName={SITE_GRID_MEDIA_CLS}
          imageLoading={imageLoading}
        />
      ))}
    </div>
  );
}

export function TemplateShowcaseMosaic() {
  return (
    <div className={SITE_MOSAIC_ROWS_CLS} data-total="3" data-reveal="rows">
      <div
        className={`grid gap-[var(--home-gap-sm)] [@media(max-width:960px)]:contents ${mosaicRowGridCols(3)}`}
        data-cols="3"
      >
        {MOSAIC_TEMPLATES.map(({ template, variant }) => (
          <article
            key={template.key}
            className={`flex min-w-0 flex-col gap-3 text-[var(--home-ink)] [@media(max-width:960px)]:[scroll-snap-align:start] ${
              variant === "mobile"
                ? "[@media(max-width:960px)]:flex-[0_0_60%]"
                : "[@media(max-width:960px)]:flex-[0_0_78%]"
            }`}
          >
            <div className={SITE_CARD_MEDIA_CLS}>
              <StorefrontStageMini
                template={template}
                variant={variant}
                ariaLabel={`Minh họa cửa hàng theo mẫu giao diện ${template.label}`}
              />
            </div>

            <div className="flex min-w-0 flex-col gap-1">
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

export function SiteDirectoryErrorBlock() {
  return (
    <div className="flex flex-col gap-[var(--home-gap)]">
      <DirectoryErrorState />
      <TemplateShowcaseMosaic />
    </div>
  );
}

export function SiteDirectoryOutOfRange({ firstPageHref }: { firstPageHref: string }) {
  return (
    <div className={EMPTY_BAR_CLS}>
      <p className="text-[length:var(--home-fs-body)] leading-[1.5] text-[var(--home-ink-2)]">
        Trang này không có cửa hàng nào.
      </p>
      <Link href={firstPageHref} className={EMPTY_CTA_CLS}>
        Về trang đầu
      </Link>
    </div>
  );
}

export function SiteDirectoryEmpty() {
  return (
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
  );
}

export interface SiteDirectoryPaginationProps {
  page: number;
  totalPages: number;
  pageHref: (page: number) => string;
}

export function SiteDirectoryPagination({
  page,
  totalPages,
  pageHref,
}: SiteDirectoryPaginationProps) {
  if (totalPages <= 1) return null;

  return (
    <nav
      className="flex flex-wrap items-center justify-between gap-3 pt-2"
      aria-label="Phân trang danh bạ gian hàng"
    >
      {page > 1 ? (
        <Link href={pageHref(page - 1)} className={PAGE_BTN_CLS}>
          Trang trước
        </Link>
      ) : (
        <span className={PAGE_BTN_DISABLED_CLS} aria-disabled="true">
          Trang trước
        </span>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {buildPageList(page, totalPages).map((item, idx) =>
          item === null ? (
            <span
              key={`gap-${idx}`}
              className="px-2 py-0 text-[length:var(--home-fs-small)] text-[var(--home-ink-3)]"
              aria-hidden="true"
            >
              …
            </span>
          ) : item === page ? (
            <span
              key={item}
              className="tabular-nums inline-flex min-h-[var(--home-btn-header-h)] min-w-[var(--home-btn-header-h)] items-center justify-center rounded-full bg-[var(--home-ink)] px-4 py-0 text-[length:var(--home-fs-small)] font-semibold text-[var(--home-bg)]"
              aria-current="page"
            >
              {item}
            </span>
          ) : (
            <Link key={item} href={pageHref(item)} className={`${PAGE_BTN_CLS} tabular-nums`}>
              {item}
            </Link>
          )
        )}
      </div>

      {page < totalPages ? (
        <Link href={pageHref(page + 1)} className={PAGE_BTN_CLS}>
          Trang sau
        </Link>
      ) : (
        <span className={PAGE_BTN_DISABLED_CLS} aria-disabled="true">
          Trang sau
        </span>
      )}
    </nav>
  );
}
