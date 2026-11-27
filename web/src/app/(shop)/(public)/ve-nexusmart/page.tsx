import type { Metadata } from "next";
import React from "react";
import Image from "next/image";
import Link from "next/link";
import { redirect } from "next/navigation";
import { FABRIC_MEDIA, MODEL_SHOTS, WORKSHOP_MEDIA } from "@/data/media";
import {
  PLATFORM_BTN_GLASS,
  PLATFORM_BTN_OUTLINE,
  PLATFORM_BTN_PRIMARY,
  PLATFORM_CONTAINER,
  PLATFORM_DISPLAY_H1,
  PLATFORM_OVERLAP_BAND,
  PLATFORM_SECTION_H2,
} from "@/features/platform/platformStyles";

export const metadata: Metadata = {
  title: "Về NexusMart",
  description:
    "Nền tảng trao cho mỗi thương hiệu và xưởng sản xuất Việt Nam một cửa hàng độc lập trên tên miền riêng.",
};

interface AboutNexusMartPageProps {
  searchParams?: Promise<{ tab?: string | string[] }>;
}

export default async function AboutNexusMartPage({ searchParams }: AboutNexusMartPageProps) {
  const params = searchParams ? await searchParams : {};
  const rawTab = Array.isArray(params.tab) ? params.tab[0] : params.tab;
  const tab = rawTab?.trim().toLowerCase() ?? "";
  if (tab === "stores") {
    redirect("/cua-hang");
  }
  if (tab === "onboarding") {
    redirect("/dang-ky?role=seller");
  }

  return (
    <div className="w-full overflow-x-clip bg-[var(--home-bg)] text-[var(--home-ink)]">
      {/* Band 1: Full-bleed editorial workshop hero matching homepage Hero grammar */}
      <section
        className="relative flex h-[clamp(560px,84svh,780px)] min-h-[540px] w-full items-end overflow-hidden bg-[var(--home-band)] [@media(max-width:640px)]:h-[clamp(520px,78svh,660px)] [@media(max-width:640px)]:min-h-[500px]"
        aria-label="Tuyên ngôn mở đầu"
      >
        <div className="absolute inset-0 overflow-hidden">
          <Image
            src={WORKSHOP_MEDIA.src}
            alt={WORKSHOP_MEDIA.alt}
            fill
            priority
            sizes="100vw"
            className="object-cover object-center"
          />
          <div
            className="absolute inset-0 [background:radial-gradient(ellipse_74%_58%_at_24%_68%,rgba(2,9,10,0.68)_0%,rgba(2,9,10,0.44)_62%,transparent_94%),linear-gradient(90deg,rgba(2,9,10,0.84)_0%,rgba(2,9,10,0.58)_42%,rgba(2,9,10,0.24)_72%,rgba(2,9,10,0.12)_100%),linear-gradient(180deg,rgba(2,9,10,0.68)_0%,rgba(2,9,10,0.22)_18%,transparent_34%),linear-gradient(0deg,rgba(2,9,10,0.82)_0%,rgba(2,9,10,0.32)_28%,transparent_54%)] [@media(max-width:640px)]:[background:linear-gradient(180deg,rgba(2,9,10,0.68)_0%,rgba(2,9,10,0.46)_38%,rgba(2,9,10,0.9)_100%)]"
            aria-hidden="true"
          />
        </div>

        <div
          className={`relative z-[1] ${PLATFORM_CONTAINER} pt-[calc(var(--home-header-h)+48px)] pb-[calc(var(--home-radius-band)+clamp(44px,6vw,84px))] [@media(max-width:640px)]:pb-[calc(var(--home-radius-band-sm)+44px)]`}
        >
          <h1 className={`${PLATFORM_DISPLAY_H1} max-w-[19ch]`}>
            Mỗi thương hiệu Việt xứng đáng một cửa hàng của riêng mình.
          </h1>
        </div>
      </section>

      {/* Band 2: Overlapping rounded --home-band canvas with alternating editorial stories */}
      <div className={`${PLATFORM_OVERLAP_BAND} z-[2] bg-[var(--home-band)]`}>
        {/* Story 1: Balanced split — portrait left, vertically centered statement with teal accent right */}
        <section className="pt-[var(--home-space-section)]" aria-label="Độc lập không chung kệ">
          <div className={PLATFORM_CONTAINER}>
            <div className="grid grid-cols-1 items-center gap-10 lg:grid-cols-[minmax(0,0.88fr)_minmax(0,1.12fr)] lg:gap-16">
              <figure
                className="relative aspect-[4/5] w-full overflow-hidden rounded-[var(--home-radius-card)] border border-[var(--home-border)] bg-[var(--home-card)] shadow-[var(--home-shadow-card)] sm:aspect-[5/4] lg:aspect-[4/5]"
                data-reveal="scale"
              >
                <Image
                  src={MODEL_SHOTS["prod-at-xamda"].src}
                  alt={MODEL_SHOTS["prod-at-xamda"].alt}
                  fill
                  sizes="(max-width: 1024px) 92vw, 520px"
                  className="object-cover object-[center_18%]"
                />
              </figure>

              <div className="flex min-w-0 flex-col justify-center" data-reveal="">
                <h2
                  className={`${PLATFORM_SECTION_H2} max-w-[17ch] border-l-2 border-[var(--home-accent)] pl-6 sm:pl-8`}
                >
                  Không chung kệ, không bị kéo khách sang đối thủ ngay trang sản phẩm.
                </h2>
              </div>
            </div>
          </div>
        </section>

        {/* Story 2: Wide editorial heading + gallery fabric frame */}
        <section
          className="pt-[var(--home-space-head)] pb-[calc(var(--home-space-section)+var(--home-radius-band))] [@media(max-width:960px)]:pb-[calc(var(--home-space-section)+var(--home-radius-band-sm))]"
          aria-label="Tách bạch dữ liệu theo tên miền"
        >
          <div className={PLATFORM_CONTAINER}>
            <div className="flex flex-col gap-[var(--home-space-head)] border-t border-[var(--home-border)] pt-[var(--home-space-head)]">
              <h2 className={`${PLATFORM_SECTION_H2} max-w-[23ch]`} data-reveal="">
                Giao diện, giỏ hàng, thanh toán và đơn hàng tách bạch hoàn toàn theo tên miền.
              </h2>

              <figure
                className="relative aspect-[16/10] w-full overflow-hidden rounded-[var(--home-radius-card)] border border-[var(--home-border)] bg-[var(--home-card)] shadow-[var(--home-shadow-card)] lg:aspect-[21/10]"
                data-reveal="scale"
              >
                <Image
                  src={FABRIC_MEDIA.src}
                  alt={FABRIC_MEDIA.alt}
                  fill
                  sizes="(max-width: 768px) 92vw, 1260px"
                  className="object-cover object-center"
                />
              </figure>
            </div>
          </div>
        </section>
      </div>

      {/* Band 3: Overlapping rounded deep pine closing CTA band matching homepage rhythm */}
      <section
        className={`${PLATFORM_OVERLAP_BAND} z-[3] py-[var(--home-space-section)] [background:linear-gradient(180deg,rgba(2,8,8,0.28)_0%,transparent_24%,rgba(0,0,0,0.52)_100%),radial-gradient(ellipse_56%_58%_at_84%_22%,var(--home-glow-soft),transparent_64%),var(--home-pine)]`}
        aria-label="Hạ tầng sẵn sàng"
      >
        <div className={PLATFORM_CONTAINER}>
          <div className="grid grid-cols-1 items-center gap-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.06fr)] lg:gap-14">
            <div className="flex min-w-0 flex-col items-start gap-8" data-reveal="">
              <h2 className={`${PLATFORM_SECTION_H2} max-w-[18ch]`}>
                Tồn kho, VietQR và đối soát sẵn sàng cho xưởng Việt.
              </h2>

              <div className="flex flex-wrap items-center gap-3.5 [@media(max-width:640px)]:w-full [@media(max-width:640px)]:flex-col [@media(max-width:640px)]:items-stretch">
                <Link href="/dang-ky?role=seller" className={PLATFORM_BTN_PRIMARY}>
                  Mở gian hàng
                </Link>
                <Link href="/cua-hang" className={PLATFORM_BTN_OUTLINE}>
                  Xem danh bạ
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
