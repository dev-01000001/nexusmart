import type { Metadata } from "next";
import React from "react";
import Image from "next/image";
import Link from "next/link";
import { WORKSHOP_MEDIA } from "@/data/media";
import { directoryContext, loadSiteDirectory } from "@/features/platform/directory.server";
import { storesDirectoryPageHref } from "@/features/platform/directoryHref";
import { parseDirectoryPage } from "@/features/platform/directoryModel";
import {
  PLATFORM_BTN_GLASS,
  PLATFORM_BTN_OUTLINE,
  PLATFORM_BTN_PRIMARY,
  PLATFORM_CONTAINER,
  PLATFORM_LEAD,
  PLATFORM_OVERLAP_BAND,
  PLATFORM_PAGE_H1,
  PLATFORM_SECTION_H2,
} from "@/features/platform/platformStyles";
import {
  SiteDirectoryEmpty,
  SiteDirectoryErrorBlock,
  SiteDirectoryGrid,
  SiteDirectoryOutOfRange,
  SiteDirectoryPagination,
} from "@/features/platform/SiteDirectoryShared";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Cửa hàng",
  description:
    "Danh bạ các cửa hàng độc lập đã xuất bản trên NexusMart. Chọn thương hiệu để truy cập thẳng địa chỉ riêng.",
};

interface StoresDirectoryPageProps {
  searchParams?: Promise<{ trang?: string | string[] }>;
}

export default async function StoresDirectoryPage({ searchParams }: StoresDirectoryPageProps) {
  const params = searchParams ? await searchParams : {};
  const page = parseDirectoryPage(params.trang);
  const ctx = await directoryContext();
  const directory = await loadSiteDirectory(page, undefined, ctx);

  const hasRealSites = directory.ok && directory.sites.length > 0;
  const isOutOfRangePage = directory.ok && directory.total > 0 && directory.sites.length === 0;

  return (
    <div className="w-full overflow-x-clip bg-[var(--home-bg)] text-[var(--home-ink)]">
      {/* Band 1: Dark editorial directory header + full grid of published sites */}
      <section
        className="relative pt-[calc(var(--home-header-h)+clamp(44px,5.5vw,84px))] pb-[calc(var(--home-space-section)+var(--home-radius-band))] [background:radial-gradient(ellipse_56%_38%_at_86%_8%,var(--home-glow-soft),transparent_62%),var(--home-band)] [@media(max-width:960px)]:pb-[calc(var(--home-space-section)+var(--home-radius-band-sm))]"
        aria-labelledby="stores-directory-heading"
      >
        <div className={`${PLATFORM_CONTAINER} flex flex-col gap-[var(--home-space-head)]`}>
          <div className="flex flex-wrap items-end justify-between gap-x-8 gap-y-4 border-b border-[var(--home-border)] pb-8">
            <div className="flex min-w-0 max-w-[760px] flex-col gap-3">
              <h1 id="stores-directory-heading" className={PLATFORM_PAGE_H1}>
                Cửa hàng trên NexusMart
              </h1>
              <p className={`${PLATFORM_LEAD} max-w-[56ch]`}>
                Chọn thương hiệu để mở thẳng địa chỉ riêng với giao diện, giỏ hàng và thanh toán
                tách bạch.
              </p>
            </div>

            {directory.ok && directory.total > 0 && (
              <div className="flex flex-col items-start gap-1 sm:items-end">
                <p className="text-[length:var(--home-fs-small)] font-[450] whitespace-nowrap text-[var(--home-ink-2)] tabular-nums">
                  {directory.total} gian hàng đang mở
                </p>
                {directory.totalPages > 1 && (
                  <p className="text-[length:var(--home-fs-caption)] whitespace-nowrap text-[var(--home-ink-3)] tabular-nums">
                    Trang {directory.page} / {directory.totalPages}
                  </p>
                )}
              </div>
            )}
          </div>

          {!directory.ok ? (
            <SiteDirectoryErrorBlock />
          ) : isOutOfRangePage ? (
            <SiteDirectoryOutOfRange firstPageHref={storesDirectoryPageHref(1)} />
          ) : !hasRealSites ? (
            <SiteDirectoryEmpty />
          ) : (
            <div className="flex flex-col gap-[var(--home-gap)]">
              <SiteDirectoryGrid sites={directory.sites} />
              <SiteDirectoryPagination
                page={directory.page}
                totalPages={directory.totalPages}
                pageHref={storesDirectoryPageHref}
              />
            </div>
          )}
        </div>
      </section>

      {/* Band 2: Overlapping rounded deep pine closing CTA band matching /ve-nexusmart */}
      <section
        className={`${PLATFORM_OVERLAP_BAND} z-[2] py-[var(--home-space-section)] [background:linear-gradient(180deg,rgba(2,8,8,0.28)_0%,transparent_24%,rgba(0,0,0,0.52)_100%),radial-gradient(ellipse_56%_58%_at_84%_22%,var(--home-glow-soft),transparent_64%),var(--home-pine)]`}
        aria-labelledby="stores-cta-heading"
      >
        <div className={PLATFORM_CONTAINER}>
          <div className="grid grid-cols-1 items-center gap-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.06fr)] lg:gap-14">
            <div className="flex min-w-0 flex-col items-start gap-8" data-reveal="">
              <div className="flex flex-col gap-4">
                <h2 id="stores-cta-heading" className={`${PLATFORM_SECTION_H2} max-w-[18ch]`}>
                  Mở cửa hàng mang tên thương hiệu của bạn.
                </h2>
                <p className={`${PLATFORM_LEAD} max-w-[44ch]`}>
                  Tên miền riêng, giao diện tùy biến, tồn kho đồng bộ và thanh toán COD / VietQR sẵn
                  sàng.
                </p>
              </div>

              <div className="flex flex-wrap items-center gap-3.5 [@media(max-width:640px)]:w-full [@media(max-width:640px)]:flex-col [@media(max-width:640px)]:items-stretch">
                <Link href="/dang-ky?role=seller" className={PLATFORM_BTN_PRIMARY}>
                  Mở gian hàng
                </Link>
                <Link href="/ve-nexusmart" className={PLATFORM_BTN_OUTLINE}>
                  Về NexusMart
                </Link>
                <Link href="/tro-giup" className={PLATFORM_BTN_GLASS}>
                  Trợ giúp
                </Link>
              </div>
            </div>

            <figure
              className="relative aspect-[4/3] w-full overflow-hidden rounded-[var(--home-radius-card)] border border-[var(--home-border)] bg-[var(--home-card)] shadow-[var(--home-shadow-card)]"
              data-reveal="scale"
            >
              <Image
                src={WORKSHOP_MEDIA.greenFiber.src}
                alt={WORKSHOP_MEDIA.greenFiber.alt}
                fill
                loading="eager"
                sizes="(max-width: 1024px) 92vw, 620px"
                className="object-cover object-center"
              />
            </figure>
          </div>
        </div>
      </section>
    </div>
  );
}
