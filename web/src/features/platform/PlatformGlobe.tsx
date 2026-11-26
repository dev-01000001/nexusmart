"use client";

import { useEffect, useRef, useState } from "react";
import { IconTruck, IconWallet } from "@/components/Icons";
import type { GlobeController, LandDot } from "./globeRenderer";

const LAND_DOTS_URL = "/globe/land-dots.json";

const GLOBE_CARD_CLS =
  "pointer-events-auto absolute flex items-start gap-[10px] rounded-[var(--home-radius-card)] [border:1px_solid_color-mix(in_srgb,var(--home-accent)_24%,transparent)] bg-[color-mix(in_srgb,var(--home-band)_74%,transparent)] px-[14px] py-[11px] [-webkit-backdrop-filter:blur(10px)] backdrop-blur-[10px] [box-shadow:0_12px_32px_color-mix(in_srgb,var(--home-band)_68%,transparent)] [@media(max-width:767px)]:gap-[7px] [@media(max-width:767px)]:px-[10px] [@media(max-width:767px)]:py-[8px]";

const GLOBE_CARD_TEXT_CLS =
  "block text-[length:11px] uppercase leading-[1.4] tracking-[0.07em] [@media(max-width:767px)]:text-[length:10.5px] [@media(max-width:767px)]:leading-[1.35] [@media(max-width:767px)]:tracking-[0.05em]";

export function PlatformGlobe({ className }: { className?: string }) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [canvasReady, setCanvasReady] = useState(false);

  useEffect(() => {
    const root = rootRef.current;
    const canvas = canvasRef.current;
    if (!root || !canvas) return;

    let disposed = false;
    let bootStarted = false;
    let isIntersecting = false;
    let controller: GlobeController | null = null;
    const abortController = new AbortController();

    const motionQuery =
      typeof window.matchMedia === "function"
        ? window.matchMedia("(prefers-reduced-motion: reduce)")
        : null;

    const handleMotionChange = (event: MediaQueryListEvent) => {
      controller?.setReducedMotion(event.matches);
    };
    motionQuery?.addEventListener("change", handleMotionChange);

    const resizeObserver =
      typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(() => {
            controller?.resize();
          })
        : null;
    resizeObserver?.observe(root);

    async function startGlobe() {
      if (bootStarted || disposed) return;
      bootStarted = true;
      try {
        const [{ createGlobeRenderer }, response] = await Promise.all([
          import("./globeRenderer"),
          fetch(LAND_DOTS_URL, { signal: abortController.signal }),
        ]);
        if (!response.ok || disposed || !canvas) return;
        const landDots = (await response.json()) as LandDot[];
        if (disposed || !Array.isArray(landDots) || landDots.length === 0) return;

        controller = createGlobeRenderer({
          canvas,
          landDots,
          reducedMotion: motionQuery?.matches ?? false,
          onFirstRender: () => {
            if (!disposed) setCanvasReady(true);
          },
        });
        controller.setVisible(isIntersecting);
      } catch {
        // Keep the CSS fallback globe visible if fetch is aborted or unavailable.
      }
    }

    let intersectionObserver: IntersectionObserver | null = null;
    if (typeof IntersectionObserver !== "undefined") {
      intersectionObserver = new IntersectionObserver(
        (entries) => {
          const entry = entries[0];
          if (!entry) return;
          isIntersecting = entry.isIntersecting;
          if (entry.isIntersecting) {
            void startGlobe();
          }
          controller?.setVisible(isIntersecting);
        },
        { rootMargin: "200px 0px" }
      );
      intersectionObserver.observe(root);
    } else {
      isIntersecting = true;
      void startGlobe();
    }

    return () => {
      disposed = true;
      abortController.abort();
      intersectionObserver?.disconnect();
      resizeObserver?.disconnect();
      motionQuery?.removeEventListener("change", handleMotionChange);
      controller?.destroy();
    };
  }, []);

  const rootBase =
    "relative isolate aspect-[760/720] w-full overflow-hidden [@media(max-width:767px)]:aspect-square";
  const rootClassName = className ? `${rootBase} ${className}` : rootBase;

  return (
    <div ref={rootRef} className={rootClassName}>
      {/* Pure CSS globe fallback: matches canvas center (46%, 56%) and diameter (78% of width) */}
      <div
        className={`pointer-events-none absolute left-[7%] top-[56%] aspect-square w-[78%] [transform:translateY(-50%)] rounded-full [background:radial-gradient(circle_at_22%_24%,color-mix(in_srgb,var(--home-accent)_68%,var(--home-teal-deep))_0%,var(--home-teal-deep)_26%,var(--home-navy)_70%,var(--home-band)_100%)] [box-shadow:inset_18px_14px_44px_color-mix(in_srgb,var(--home-accent)_32%,transparent),0_0_64px_var(--home-glow),-14px_-12px_48px_color-mix(in_srgb,var(--home-teal-deep)_52%,transparent)] [transition:opacity_280ms_ease-out] motion-reduce:transition-none ${
          canvasReady ? "opacity-0" : ""
        }`}
        aria-hidden="true"
      />
      <canvas
        ref={canvasRef}
        className={`pointer-events-none absolute inset-0 block h-full w-full [transition:opacity_320ms_ease-out] [-webkit-mask-image:linear-gradient(90deg,transparent_0%,#000_4.5%,#000_95.5%,transparent_100%),linear-gradient(180deg,transparent_0%,#000_3.5%,#000_94%,transparent_100%)] [-webkit-mask-composite:source-in] [mask-image:linear-gradient(90deg,transparent_0%,#000_4.5%,#000_95.5%,transparent_100%),linear-gradient(180deg,transparent_0%,#000_3.5%,#000_94%,transparent_100%)] [mask-composite:intersect] motion-reduce:transition-none ${
          canvasReady ? "opacity-100" : "opacity-0"
        }`}
        aria-hidden="true"
      />
      <ul className="pointer-events-none absolute inset-0 m-0 list-none p-0">
        <li
          className={`${GLOBE_CARD_CLS} left-0 top-[56%] max-w-[232px] [transform:translateY(-50%)] [@media(max-width:767px)]:left-0 [@media(max-width:767px)]:top-[46%] [@media(max-width:767px)]:max-w-[162px] [@media(max-width:767px)]:[transform:none]`}
        >
          <span
            className="mt-px inline-flex flex-none items-center justify-center text-[var(--home-accent)]"
            aria-hidden="true"
          >
            <IconTruck size={15} />
          </span>
          <span className="flex min-w-0 flex-col gap-[3px]">
            <span className={`${GLOBE_CARD_TEXT_CLS} font-semibold text-[var(--home-ink)]`}>
              34 TỈNH THÀNH
            </span>
            <span className={`${GLOBE_CARD_TEXT_CLS} font-medium text-[var(--home-accent)]`}>
              TÍNH PHÍ GIAO HÀNG THEO TỪNG NƠI
            </span>
          </span>
        </li>
        <li
          className={`${GLOBE_CARD_CLS} bottom-[11%] right-[2%] max-w-[236px] [@media(max-width:767px)]:bottom-[2%] [@media(max-width:767px)]:right-0 [@media(max-width:767px)]:max-w-[174px]`}
        >
          <span
            className="mt-px inline-flex flex-none items-center justify-center text-[var(--home-accent)]"
            aria-hidden="true"
          >
            <IconWallet size={15} />
          </span>
          <span className="flex min-w-0 flex-col gap-[3px]">
            <span className={`${GLOBE_CARD_TEXT_CLS} font-semibold text-[var(--home-ink)]`}>
              COD VÀ VIETQR
            </span>
            <span className={`${GLOBE_CARD_TEXT_CLS} font-medium text-[var(--home-accent)]`}>
              CÓ SẴN TRÊN MỌI CỬA HÀNG
            </span>
          </span>
        </li>
      </ul>
    </div>
  );
}
