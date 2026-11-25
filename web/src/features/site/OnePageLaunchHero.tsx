"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import type { Product } from "@/data/products";
import { formatVnd } from "@/lib/format";
import { sanitizeSafeHref } from "@/lib/site/theme";
import { SafeActionLink } from "./SafeActionLink";
import { sanitizeSafeImageUrl } from "./templateValidator";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

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
    <div className={tplStyles.launchHero}>
      <div className={tplStyles.launchStickyViewport}>
        <div className={tplStyles.launchHeaderRow}>
          <HeadingTag
            className={tplStyles.launchTitle}
            data-rise=""
            style={{ "--i": 0 } as React.CSSProperties}
          >
            {title}
          </HeadingTag>
          {subtitle?.trim() && (
            <p
              className={tplStyles.launchSub}
              data-rise=""
              style={{ "--i": 1 } as React.CSSProperties}
            >
              {subtitle}
            </p>
          )}
        </div>

        <div className={tplStyles.launchStageGrid}>
          <div className={tplStyles.launchStagePills} role="group" aria-label="Chặng xem sản phẩm">
            {stages.map((st, idx) => {
              const active = idx === activeIdx;
              return (
                <button
                  key={st.key}
                  type="button"
                  aria-pressed={active}
                  className={`${tplStyles.launchStageBtn} ${
                    active ? tplStyles.launchStageBtnActive : ""
                  }`}
                  onClick={() => setActiveIdx(idx)}
                >
                  {st.swatchHex && (
                    <span
                      className={tplStyles.launchSwatchDot}
                      style={{ backgroundColor: st.swatchHex }}
                      aria-hidden="true"
                    />
                  )}
                  <span className={tplStyles.launchStageLabel}>{st.label}</span>
                  <span className={tplStyles.launchStageDesc}>{st.detail}</span>
                </button>
              );
            })}
          </div>

          <div className={tplStyles.launchCenterFrame}>
            {stages.map((st, idx) => (
              <img
                key={`${st.key}-${idx}`}
                src={st.image}
                alt={idx === activeIdx ? product?.name || title : ""}
                aria-hidden={idx !== activeIdx}
                width={896}
                height={1200}
                className={`${tplStyles.launchFrameImg} ${
                  idx === activeIdx ? tplStyles.launchFrameImgActive : ""
                }`}
              />
            ))}
          </div>

          <div className={tplStyles.launchSideCaption}>
            <p className={tplStyles.launchCaptionName}>{product?.name || title}</p>
            <p className={tplStyles.launchCaptionDetail}>{currentStage.detail}</p>
            {product && (
              <p className={`${tplStyles.launchCaptionPrice} num`}>{formatVnd(product.price)}</p>
            )}
          </div>
        </div>
      </div>

      {/* Scroll chapter triggers on desktop (skipped in static preview) */}
      {!previewMode && (
        <div className={tplStyles.launchScrollTrack} aria-hidden="true">
          {stages.map((st, idx) => (
            <div
              key={st.key}
              ref={(el) => {
                markerRefs.current[idx] = el;
              }}
              data-stage-idx={idx}
              className={tplStyles.launchScrollStep}
            />
          ))}
        </div>
      )}

      {/* Persistent always-visible Buy dock */}
      <div className={tplStyles.launchBuyDock} data-mobile-buybar="">
        <div className={tplStyles.launchBuyMeta}>
          <span className={tplStyles.launchBuyName}>{product?.name || title}</span>
          {product && (
            <span className={`${tplStyles.launchBuyPrice} num`}>{formatVnd(product.price)}</span>
          )}
        </div>
        <SafeActionLink
          href={buyHref}
          fallbackHref="/san-pham"
          label={ctaLabel}
          className={`${styles.btnPrimary} ${tplStyles.launchBuyCta}`}
          previewMode={previewMode}
        />
      </div>
    </div>
  );
}
