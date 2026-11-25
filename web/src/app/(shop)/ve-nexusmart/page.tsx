import type { Metadata } from "next";
import React from "react";
import Image from "next/image";
import { redirect } from "next/navigation";
import { Button } from "@/components/Button";
import { FABRIC_MEDIA, MODEL_SHOTS, WORKSHOP_MEDIA } from "@/data/media";
import styles from "./page.module.css";

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
    <div className={styles.page}>
      {/* Screen 1: Top-left wide headline + full-bleed workshop panorama below */}
      <section className={styles.screenOne} aria-label="Tuyên ngôn mở đầu">
        <div className={styles.screenOneHead}>
          <h1
            className={`${styles.displayTitle} type-wide`}
            data-rise=""
            style={{ "--i": 0 } as React.CSSProperties}
          >
            Mỗi thương hiệu Việt xứng đáng một cửa hàng của riêng mình.
          </h1>
        </div>

        <figure
          className={styles.panoramaFrame}
          data-rise=""
          style={{ "--i": 1 } as React.CSSProperties}
        >
          <Image
            src={WORKSHOP_MEDIA.src}
            alt={WORKSHOP_MEDIA.alt}
            fill
            priority
            sizes="100vw"
            className={styles.coverImg}
          />
        </figure>
      </section>

      {/* Screen 2: Dark slate split: tall portrait left, bottom-anchored statement right */}
      <section className={styles.screenTwo} aria-label="Độc lập không chung kệ">
        <figure className={styles.portraitFrame} data-reveal="clip">
          <Image
            src={MODEL_SHOTS["prod-at-xamda"].src}
            alt={MODEL_SHOTS["prod-at-xamda"].alt}
            fill
            sizes="(max-width: 960px) 100vw, 50vw"
            className={styles.coverPortrait}
          />
        </figure>

        <div className={styles.screenTwoCopy}>
          <h2 className={`${styles.manifestoTight} type-tight`} data-reveal="">
            Không chung kệ, không bị kéo khách sang đối thủ ngay trang sản phẩm.
          </h2>
        </div>
      </section>

      {/* Screen 3: Pure white gallery: right-offset headline + inset wide fabric plate */}
      <section className={styles.screenThree} aria-label="Tách bạch dữ liệu theo tên miền">
        <div className={styles.screenThreeHead}>
          <h2 className={`${styles.manifestoWide} type-wide`} data-reveal="">
            Giao diện, giỏ hàng, thanh toán và đơn hàng tách bạch hoàn toàn theo tên miền.
          </h2>
        </div>

        <figure className={styles.insetPlate} data-reveal="scale">
          <Image
            src={FABRIC_MEDIA.src}
            alt={FABRIC_MEDIA.alt}
            fill
            sizes="(max-width: 960px) 100vw, 76vw"
            className={styles.coverImg}
          />
        </figure>
      </section>

      {/* Screen 4: Tinted neutral split: left statement + CTA, right edge-to-edge full-height photo */}
      <section className={styles.screenFour} aria-label="Hạ tầng sẵn sàng">
        <div className={styles.screenFourCopy}>
          <h2 className={`${styles.manifestoWide} type-wide`} data-reveal="">
            Tồn kho, VietQR và đối soát sẵn sàng cho xưởng Việt.
          </h2>

          <div className={styles.actions}>
            <Button variant="primary" size="lg" shape="pill" href="/dang-ky?role=seller">
              Mở gian hàng
            </Button>
            <Button variant="ink" size="lg" shape="pill" href="/#danh-ba-gian-hang">
              Xem danh bạ
            </Button>
            <Button variant="ghost" size="lg" shape="pill" href="/tro-giup">
              Trợ giúp
            </Button>
          </div>
        </div>

        <figure className={styles.finalePhoto} data-reveal="clip">
          <Image
            src={WORKSHOP_MEDIA.greenFiber.src}
            alt={WORKSHOP_MEDIA.greenFiber.alt}
            fill
            sizes="(max-width: 960px) 100vw, 58vw"
            className={styles.coverImg}
          />
        </figure>
      </section>
    </div>
  );
}
