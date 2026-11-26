import React from "react";
import Image from "next/image";
import { IconBag, IconMenu } from "@/components/Icons";
import { formatVnd } from "@/lib/format";
import { SHOWCASE_TEMPLATES, type ShowcaseTemplateSpec } from "./showcaseTemplates";

export interface StorefrontStageMiniProps {
  template?: ShowcaseTemplateSpec;
  variant?: "desktop" | "mobile" | "studio" | "site-fallback";
  brandName?: string;
  headline?: string;
  imageSrc?: string;
  logoUrl?: string;
  selectedLabel?: string;
  ariaLabel?: string;
  decorative?: boolean;
}

const STAGE_BASE =
  "relative flex h-full w-full select-none flex-col overflow-hidden rounded-[var(--home-radius-media)] bg-[var(--stage-bg)] font-[family-name:var(--home-font)] text-[var(--stage-text)]";

const DESKTOP_NAV_CLS =
  "flex flex-none items-center justify-between gap-[var(--space-3)] [border-bottom:1px_solid_color-mix(in_srgb,var(--stage-text)_12%,transparent)] bg-[var(--stage-bg)] px-[var(--space-4)] py-[var(--space-3)]";

const NAV_BRAND_CLS =
  "overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-caption)] font-semibold tracking-[-0.01em] text-[var(--stage-text)]";

const NAV_LINKS_CLS =
  "inline-flex flex-none items-center gap-[var(--space-3)] whitespace-nowrap text-[length:var(--home-fs-caption)] text-[color-mix(in_srgb,var(--stage-text)_72%,transparent)]";

const HERO_CTA_CLS =
  "inline-flex items-center justify-center whitespace-nowrap rounded-[var(--radius-pill)] bg-[var(--stage-accent)] px-[var(--space-4)] py-[var(--space-2)] text-[length:var(--home-fs-caption)] font-semibold leading-[1.2] text-[var(--stage-accent-text)]";

const SIZE_CHIP_BASE =
  "inline-flex h-[var(--space-6)] w-[var(--space-6)] flex-none items-center justify-center rounded-[var(--radius-tail)] text-[length:var(--home-fs-caption)] text-[var(--stage-text)]";

const STRIP_TILE_CLS =
  "inline-flex items-center gap-[var(--space-2)] whitespace-nowrap rounded-[var(--radius-tail)] [border:1px_solid_color-mix(in_srgb,var(--stage-text)_10%,transparent)] bg-[var(--stage-surface)] px-[var(--space-3)] py-[var(--space-1)] text-[length:var(--home-fs-caption)] text-[var(--stage-text)]";

export function StorefrontStageMini({
  template = SHOWCASE_TEMPLATES[0],
  variant = "desktop",
  brandName,
  headline,
  imageSrc,
  logoUrl,
  selectedLabel,
  ariaLabel,
  decorative = false,
}: StorefrontStageMiniProps) {
  const title = brandName || template.defaultBrand;
  const heroCopy = headline || template.headline;
  const photo = imageSrc || template.primaryImg;

  const stageStyle = {
    "--stage-bg": template.bg,
    "--stage-surface": template.surface,
    "--stage-text": template.text,
    "--stage-accent": template.accent,
    "--stage-accent-text": template.accentText,
  } as React.CSSProperties;

  const wrapperA11y = decorative
    ? { "aria-hidden": true as const }
    : {
        role: "img" as const,
        "aria-label": ariaLabel || `Minh họa cửa hàng theo mẫu giao diện ${template.label}`,
      };

  if (variant === "mobile") {
    return (
      <div className={STAGE_BASE} style={stageStyle} {...wrapperA11y}>
        <div
          className="flex flex-none items-center justify-between bg-[var(--stage-bg)] p-[var(--space-3)] text-[var(--stage-text)]"
          aria-hidden="true"
        >
          <IconMenu size={13} />
          <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-caption)] font-semibold tracking-[-0.01em]">
            {title}
          </span>
          <IconBag size={13} />
        </div>

        <div
          className="relative min-h-0 w-full flex-1 overflow-hidden bg-[var(--stage-surface)]"
          aria-hidden="true"
        >
          <Image
            src={photo}
            alt=""
            fill
            sizes="(max-width: 900px) 78vw, 240px"
            className="object-cover object-[center_16%]"
          />
        </div>

        <div
          className="flex flex-none flex-col gap-[var(--space-2)] [border-top:1px_solid_color-mix(in_srgb,var(--stage-text)_12%,transparent)] bg-[var(--stage-bg)] p-[var(--space-3)]"
          aria-hidden="true"
        >
          <div className="flex flex-col gap-[2px]">
            <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-caption)] font-medium text-[var(--stage-text)]">
              {template.sampleProduct}
            </span>
            <span className="num text-[length:var(--home-fs-caption)] font-semibold text-[var(--stage-accent)]">
              {formatVnd(template.samplePriceVnd)}
            </span>
          </div>
          <div className="flex items-center gap-[var(--space-1)]">
            <span
              className={`${SIZE_CHIP_BASE} [border:1px_solid_color-mix(in_srgb,var(--stage-text)_22%,transparent)]`}
            >
              S
            </span>
            <span
              className={`${SIZE_CHIP_BASE} [border:1px_solid_var(--stage-accent)] bg-[var(--stage-surface)] font-semibold`}
            >
              M
            </span>
            <span
              className={`${SIZE_CHIP_BASE} [border:1px_solid_color-mix(in_srgb,var(--stage-text)_22%,transparent)]`}
            >
              L
            </span>
            <span className="ml-auto inline-flex h-[var(--space-6)] items-center justify-center whitespace-nowrap rounded-[var(--radius-tail)] bg-[var(--stage-accent)] px-[var(--space-2)] py-0 text-[length:var(--home-fs-caption)] font-semibold text-[var(--stage-accent-text)]">
              Thêm vào giỏ
            </span>
          </div>
        </div>
      </div>
    );
  }

  if (variant === "site-fallback") {
    return (
      <div className={`${STAGE_BASE} justify-between`} style={stageStyle} {...wrapperA11y}>
        <div className={DESKTOP_NAV_CLS} aria-hidden="true">
          <span className={NAV_BRAND_CLS}>{title}</span>
          <div className={NAV_LINKS_CLS}>
            <span>Sản phẩm</span>
            <span>Bộ sưu tập</span>
            <IconBag size={12} />
          </div>
        </div>
        <div
          className="flex flex-1 flex-col items-start justify-end gap-[var(--space-3)] px-[var(--space-5)] py-[var(--space-6)] [background:radial-gradient(ellipse_65%_65%_at_80%_25%,var(--home-glow-soft),transparent_65%),var(--stage-bg)]"
          aria-hidden="true"
        >
          {logoUrl ? (
            <img
              src={logoUrl}
              alt=""
              width={64}
              height={64}
              loading="lazy"
              className="h-[var(--space-8)] w-[var(--space-8)] rounded-[var(--home-radius-card)] bg-[var(--home-ink)] object-cover"
            />
          ) : null}
          <span className="text-[length:clamp(1.5rem,2.2vw,2.125rem)] font-[330] leading-[1.1] tracking-[-0.02em] text-[var(--stage-text)]">
            {title}
          </span>
          {heroCopy ? (
            <span className="max-w-[34ch] text-[length:var(--home-fs-small)] text-[color-mix(in_srgb,var(--stage-text)_72%,transparent)]">
              {heroCopy}
            </span>
          ) : null}
          <span className={HERO_CTA_CLS}>Xem cửa hàng</span>
        </div>
      </div>
    );
  }

  const isStudio = variant === "studio";

  return (
    <div className={STAGE_BASE} style={stageStyle} {...wrapperA11y}>
      <div className={DESKTOP_NAV_CLS} aria-hidden="true">
        <span className={NAV_BRAND_CLS}>{title}</span>
        <div className={NAV_LINKS_CLS}>
          <span>Hàng mới</span>
          <span>Bộ sưu tập</span>
          {!isStudio ? <span>Giới thiệu</span> : null}
          <IconBag size={13} />
        </div>
      </div>

      <div
        className={`relative flex min-h-0 flex-1 items-end overflow-hidden p-[var(--space-5)] ${
          selectedLabel
            ? "m-[var(--space-2)] w-auto rounded-[var(--home-radius-media)] [outline:2px_solid_var(--home-accent)] [outline-offset:-2px]"
            : "w-full"
        }`}
        aria-hidden="true"
      >
        <Image
          src={photo}
          alt=""
          fill
          sizes={isStudio ? "(max-width: 900px) 85vw, 560px" : "(max-width: 900px) 85vw, 520px"}
          className="object-cover object-[center_22%]"
        />
        <div className="absolute inset-0 z-[1] [background:linear-gradient(180deg,transparent_25%,rgba(2,9,10,0.72)_100%),linear-gradient(90deg,rgba(2,9,10,0.56)_0%,transparent_65%)]" />

        {selectedLabel ? (
          <span className="absolute left-[var(--space-3)] top-[var(--space-3)] z-[3] inline-flex items-center rounded-[var(--radius-tail)] bg-[var(--home-accent)] px-[var(--space-3)] py-[var(--space-1)] text-[length:var(--home-fs-caption)] font-semibold leading-[1.2] text-[var(--home-ink-dark)]">
            {selectedLabel}
          </span>
        ) : null}

        <div className="relative z-[2] flex max-w-[82%] flex-col items-start gap-[var(--space-3)]">
          <span
            className={`${
              isStudio
                ? "text-[length:clamp(1.25rem,1.6vw,1.625rem)]"
                : "text-[length:clamp(1.375rem,1.9vw,1.875rem)]"
            } font-normal leading-[1.12] tracking-[-0.025em] text-[var(--home-ink)]`}
          >
            {heroCopy}
          </span>
          <span className={HERO_CTA_CLS}>Xem bộ sưu tập</span>
        </div>
      </div>

      <div
        className="flex flex-none items-center gap-[var(--space-2)] overflow-hidden bg-[var(--stage-bg)] px-[var(--space-4)] pb-[var(--space-3)] pt-[var(--space-2)]"
        aria-hidden="true"
      >
        {template.chips.map((chip) => (
          <span key={chip} className={STRIP_TILE_CLS}>
            <span className="h-[var(--space-2)] w-[var(--space-2)] flex-none rounded-[var(--radius-pill)] bg-[var(--stage-accent)]" />
            <span className="overflow-hidden text-ellipsis">{chip}</span>
          </span>
        ))}
        <span className={STRIP_TILE_CLS}>
          <span className="overflow-hidden text-ellipsis">{template.sampleProduct}</span>
          <span className="num font-semibold text-[var(--stage-accent)]">
            {formatVnd(template.samplePriceVnd)}
          </span>
        </span>
      </div>
    </div>
  );
}
