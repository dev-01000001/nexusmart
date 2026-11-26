import type { Metadata } from "next";
import React from "react";
import Link from "next/link";
import { redirect } from "next/navigation";
import { Button } from "@/components/ui/button";
import { UiScope } from "@/components/ui/ui-scope";
import { FABRIC_MEDIA, MODEL_SHOTS, WORKSHOP_MEDIA } from "@/data/media";

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
    redirect("/#danh-ba-gian-hang");
  }
  if (tab === "onboarding") {
    redirect("/dang-ky?role=seller");
  }

  return (
    <UiScope className="w-full bg-background text-foreground">
      {/* Screen 1: Top-left headline + full-bleed workshop panorama below */}
      <section
        className="flex flex-col justify-between gap-7 bg-background pt-24 md:gap-12 lg:min-h-[94svh] lg:pt-28"
        aria-label="Tuyên ngôn mở đầu"
      >
        <div className="w-full px-4 sm:px-6 lg:px-8">
          <h1 className="max-w-[26ch] text-[clamp(1.75rem,3.85vw,3.5rem)] leading-[1.12] font-semibold tracking-[-0.025em] text-balance text-foreground">
            Mỗi thương hiệu Việt xứng đáng một cửa hàng của riêng mình.
          </h1>
        </div>

        <figure className="relative h-[clamp(280px,50svh,520px)] w-full overflow-hidden bg-secondary">
          <img
            src={WORKSHOP_MEDIA.src}
            alt={WORKSHOP_MEDIA.alt}
            width={WORKSHOP_MEDIA.width}
            height={WORKSHOP_MEDIA.height}
            loading="eager"
            decoding="sync"
            className="absolute inset-0 h-full w-full object-cover object-center"
          />
        </figure>
      </section>

      {/* Screen 2: Dark slate split: tall portrait left, bottom-anchored statement right */}
      <section
        className="grid grid-cols-1 items-stretch gap-7 bg-topbar px-4 py-12 text-white sm:px-6 md:py-16 lg:min-h-[88svh] lg:grid-cols-2 lg:gap-14 lg:px-8 lg:py-20"
        aria-label="Độc lập không chung kệ"
      >
        <figure className="relative min-h-[380px] w-full overflow-hidden rounded-xl bg-topbar-input lg:min-h-[clamp(380px,66svh,660px)]">
          <img
            src={MODEL_SHOTS["prod-at-xamda"].src}
            alt={MODEL_SHOTS["prod-at-xamda"].alt}
            width={MODEL_SHOTS["prod-at-xamda"].width}
            height={MODEL_SHOTS["prod-at-xamda"].height}
            loading="eager"
            decoding="sync"
            className="absolute inset-0 h-full w-full object-cover object-[center_18%]"
          />
        </figure>

        <div className="flex flex-col justify-start lg:justify-end lg:pb-6">
          <h2 className="max-w-[18ch] text-[clamp(1.85rem,3.5vw,3.25rem)] leading-[1.1] font-semibold tracking-[-0.025em] text-balance text-white">
            Không chung kệ, không bị kéo khách sang đối thủ ngay trang sản phẩm.
          </h2>
        </div>
      </section>

      {/* Screen 3: Pure white gallery: right-offset headline + inset wide fabric plate */}
      <section
        className="grid grid-cols-12 content-center gap-y-7 bg-card px-4 py-14 text-foreground sm:px-6 md:gap-y-12 md:py-20 lg:min-h-[88svh] lg:px-8 lg:py-24"
        aria-label="Tách bạch dữ liệu theo tên miền"
      >
        <div className="col-span-12 lg:col-span-10 lg:col-start-3">
          <h2 className="max-w-[24ch] text-[clamp(1.75rem,3.3vw,3.1rem)] leading-[1.12] font-semibold tracking-[-0.025em] text-balance text-foreground">
            Giao diện, giỏ hàng, thanh toán và đơn hàng tách bạch hoàn toàn theo tên miền.
          </h2>
        </div>

        <figure className="relative col-span-12 aspect-video w-full overflow-hidden rounded-xl bg-muted lg:col-span-9 lg:col-start-4">
          <img
            src={FABRIC_MEDIA.src}
            alt={FABRIC_MEDIA.alt}
            width={FABRIC_MEDIA.width}
            height={FABRIC_MEDIA.height}
            loading="eager"
            decoding="sync"
            className="absolute inset-0 h-full w-full object-cover object-center"
          />
        </figure>
      </section>

      {/* Screen 4: Tinted neutral split: left statement + CTA, right edge-to-edge full-height photo */}
      <section
        className="grid grid-cols-1 items-stretch bg-secondary text-foreground lg:min-h-[84svh] lg:grid-cols-[44%_56%]"
        aria-label="Hạ tầng sẵn sàng"
      >
        <div className="flex flex-col items-start justify-center gap-7 px-4 py-12 sm:px-6 lg:py-20 lg:pr-12 lg:pl-8">
          <h2 className="max-w-[24ch] text-[clamp(1.75rem,3.2vw,3rem)] leading-[1.12] font-semibold tracking-[-0.025em] text-balance text-foreground">
            Tồn kho, VietQR và đối soát sẵn sàng cho xưởng Việt.
          </h2>

          <div className="flex flex-wrap gap-3">
            <Button size="lg" className="min-h-11 px-6" asChild>
              <Link href="/dang-ky?role=seller">Mở gian hàng</Link>
            </Button>
            <Button variant="outline" size="lg" className="min-h-11 px-6" asChild>
              <Link href="/#danh-ba-gian-hang">Xem danh bạ</Link>
            </Button>
            <Button variant="ghost" size="lg" className="min-h-11 px-5" asChild>
              <Link href="/tro-giup">Trợ giúp</Link>
            </Button>
          </div>
        </div>

        <figure className="relative min-h-[320px] w-full overflow-hidden bg-muted lg:min-h-[clamp(340px,64svh,640px)]">
          <img
            src={WORKSHOP_MEDIA.greenFiber.src}
            alt={WORKSHOP_MEDIA.greenFiber.alt}
            width={WORKSHOP_MEDIA.greenFiber.width}
            height={WORKSHOP_MEDIA.greenFiber.height}
            loading="eager"
            decoding="sync"
            className="absolute inset-0 h-full w-full object-cover object-center"
          />
        </figure>
      </section>
    </UiScope>
  );
}
