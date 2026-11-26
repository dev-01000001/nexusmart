"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { buttonVariants } from "@/components/ui/button";
import type { Product } from "@/data/products";
import { formatVnd } from "@/lib/format";
import { sanitizeSafeHref } from "@/lib/site/theme";
import { cn } from "@/lib/utils";
import { SafeActionLink } from "./SafeActionLink";
import { sanitizeSafeImageUrl } from "./templateValidator";

export interface OnePageLaunchHeroProps {
  title: string;
  subtitle?: string;
  ctaLabel: string;
  ctaHref: string;
  fallbackImage: string;
  product?: Product;
  isFirstHeading: boolean;
  previewMode?: boolean;
}

interface LaunchStage {
  key: string;
  label: string;
  detail: string;
  image: string;
  swatchHex?: string;
}

/**
 * Signature hero for `one-page-launch`:
 * Holds one spotlight product centered in a sticky stage across 3 scroll/click stages
 * (switching real product colors/angles with smooth crossfade) with an always-visible buy bar.
 */
export function OnePageLaunchHero({
  title,
  subtitle,
  ctaLabel,
  ctaHref,
  fallbackImage,
  product,
  isFirstHeading,
  previewMode = false,
}: OnePageLaunchHeroProps) {
  const stages = useMemo<LaunchStage[]>(() => {
    const imgs = product
      ? Array.from(
          new Set(
            [
              product.onModelImage,
              product.image,
              ...(product.gallery ?? []),
              ...product.colors.map((c) => c.image),
            ]
              .map((u) => sanitizeSafeImageUrl(u))
              .filter(Boolean)
          )
        )
      : [];

    const img0 = imgs[0] || fallbackImage;
    const img1 = imgs[1] || product?.colors[1]?.image || "/banners/fabric-closeup.jpg";
    const img2 = imgs[2] || product?.colors[2]?.image || "/banners/campaign-everyday.jpg";

    const color0 = product?.colors[0];
    const color1 = product?.colors[1];
    const color2 = product?.colors[2];

    return [
      {
        key: "fit",
        label: color0?.name || "Phom dáng",
        detail: product?.fit ? `Phom ${product.fit}, đứng vai gọn gàng.` : "Tỷ lệ cắt may gọn vai.",
        image: sanitizeSafeImageUrl(img0) || fallbackImage,
        swatchHex: color0?.hex,
      },
      {
        key: "fabric",
        label: color1?.name || "Chất liệu",
        detail: product?.fabric
          ? `${product.fabric}${product.gsm > 0 ? ` ${product.gsm}gsm` : ""}, bền màu.`
          : "Sợi tự nhiên thoáng khí, ít co rút.",
        image: sanitizeSafeImageUrl(img1) || img0,
        swatchHex: color1?.hex,
      },
      {
        key: "finish",
        label: color2?.name || "Hoàn thiện",
        detail: product?.origin
          ? `Hoàn thiện tại ${product.origin}.`
          : "Đường may phẳng, đổi cỡ 45 ngày.",
        image: sanitizeSafeImageUrl(img2) || img0,
        swatchHex: color2?.hex,
      },
    ];
  }, [product, fallbackImage]);

  const [activeIdx, setActiveIdx] = useState(0);
  const markerRefs = useRef<Array<HTMLDivElement | null>>([]);

  useEffect(() => {
    if (previewMode || typeof IntersectionObserver === "undefined") return;
    const nodes = markerRefs.current.filter((n): n is HTMLDivElement => n !== null);
    if (nodes.length === 0) return;

    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            const idx = Number((entry.target as HTMLElement).dataset.stageIdx ?? "0");
            if (Number.isFinite(idx)) setActiveIdx(idx);
          }
        }
      },
      { rootMargin: "-35% 0px -45% 0px", threshold: 0.05 }
    );

    for (const node of nodes) io.observe(node);
    return () => io.disconnect();
  }, [previewMode]);

  const HeadingTag = isFirstHeading ? "h1" : "h2";
  const buyHref =
    sanitizeSafeHref(ctaHref, "") ||
    (product ? `/san-pham/${encodeURIComponent(product.slug)}` : "/san-pham");
  const currentStage = stages[activeIdx] ?? stages[0];

  return (
    <div className="relative flex flex-col gap-6">
      <div className="z-[5] flex flex-col gap-[clamp(18px,2.5vw,28px)] rounded-lg bg-background p-[clamp(16px,2.5vw,32px)] lg:sticky lg:top-[72px]">
        <div className="flex flex-wrap items-end justify-between gap-x-8 gap-y-3">
          <HeadingTag
            className="max-w-[18ch] text-[clamp(32px,4.4vw,60px)] leading-[1.06] font-bold tracking-[-0.025em] text-foreground"
            data-rise=""
            style={{ "--i": 0 } as React.CSSProperties}
          >
            {title}
          </HeadingTag>
          {subtitle?.trim() && (
            <p
              className="max-w-[38ch] text-base text-muted-foreground"
              data-rise=""
              style={{ "--i": 1 } as React.CSSProperties}
            >
              {subtitle}
            </p>
          )}
        </div>

        <div className="grid grid-cols-1 items-center gap-[clamp(16px,2.5vw,36px)] lg:grid-cols-[minmax(0,3.5fr)_minmax(260px,4.5fr)_minmax(0,3.5fr)]">
          <div className="flex flex-col gap-2" role="group" aria-label="Chặng xem sản phẩm">
            {stages.map((st, idx) => {
              const active = idx === activeIdx;
              return (
                <button
                  key={st.key}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setActiveIdx(idx)}
                  className={cn(
                    "grid min-h-11 w-full grid-cols-[auto_minmax(0,1fr)] items-center gap-x-2.5 gap-y-0.5 rounded-lg border px-4 py-3.5 text-left transition-colors",
                    active
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-border bg-card text-card-foreground hover:bg-accent hover:text-accent-foreground"
                  )}
                >
                  {st.swatchHex && (
                    <span
                      className="row-span-2 size-3 rounded-full border border-current/35"
                      style={{ backgroundColor: st.swatchHex }}
                      aria-hidden="true"
                    />
                  )}
                  <span className="text-[15px] leading-snug font-semibold">{st.label}</span>
                  <span className="text-[13px] leading-[1.4] opacity-85">{st.detail}</span>
                </button>
              );
            })}
          </div>

          <div className="relative aspect-[3/4] max-h-[520px] w-full overflow-hidden rounded-lg bg-card">
            {stages.map((st, idx) => (
              <img
                key={`${st.key}-${idx}`}
                src={st.image}
                alt={idx === activeIdx ? product?.name || title : ""}
                aria-hidden={idx !== activeIdx}
                width={896}
                height={1200}
                className={cn(
                  "absolute inset-0 size-full object-cover object-[center_16%] transition-all duration-300",
                  idx === activeIdx ? "scale-100 opacity-100" : "scale-[1.02] opacity-0"
                )}
              />
            ))}
          </div>

          <div className="flex flex-col gap-2.5 rounded-lg border border-border bg-card p-5 text-card-foreground">
            <p className="text-xl leading-[1.15] font-bold text-foreground">
              {product?.name || title}
            </p>
            <p className="text-[15px] leading-[1.5] text-muted-foreground">{currentStage.detail}</p>
            {product && (
              <p className="text-[22px] font-semibold text-brand tabular-nums">
                {formatVnd(product.price)}
              </p>
            )}
          </div>
        </div>
      </div>

      {/* Scroll chapter triggers on desktop (skipped in static preview) */}
      {!previewMode && (
        <div className="hidden flex-col lg:flex" aria-hidden="true">
          {stages.map((st, idx) => (
            <div
              key={st.key}
              ref={(el) => {
                markerRefs.current[idx] = el;
              }}
              data-stage-idx={idx}
              className="h-[36svh]"
            />
          ))}
        </div>
      )}

      {/* Persistent always-visible Buy dock */}
      <div
        className="fixed inset-x-0 bottom-0 z-25 flex w-full items-center justify-between gap-4 bg-[var(--site-band-bg)] px-4 py-3 text-[var(--site-band-ink)] sm:sticky sm:bottom-4 sm:mx-auto sm:w-[min(640px,100%)] sm:rounded-lg"
        data-mobile-buybar=""
      >
        <div className="flex min-w-0 flex-col pl-1">
          <span className="truncate text-[15px] font-semibold">{product?.name || title}</span>
          {product && (
            <span className="text-sm tabular-nums opacity-85">{formatVnd(product.price)}</span>
          )}
        </div>
        <SafeActionLink
          href={buyHref}
          fallbackHref="/san-pham"
          label={ctaLabel}
          className={cn(
            buttonVariants({ variant: "default", size: "lg" }),
            "min-h-11 shrink-0 px-5 whitespace-nowrap shadow-none"
          )}
          previewMode={previewMode}
        />
      </div>
    </div>
  );
}
