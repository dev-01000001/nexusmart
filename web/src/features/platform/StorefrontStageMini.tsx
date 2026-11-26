import React from "react";
import Image from "next/image";
import { IconBag, IconMenu } from "@/components/Icons";
import { formatVnd } from "@/lib/format";
import { SHOWCASE_TEMPLATES, type ShowcaseTemplateSpec } from "./showcaseTemplates";
import styles from "./StorefrontStageMini.module.css";

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
      <div className={`${styles.stage} ${styles.stageMobile}`} style={stageStyle} {...wrapperA11y}>
        <div className={styles.mobileChrome} aria-hidden="true">
          <IconMenu size={13} />
          <span className={styles.mobileBrand}>{title}</span>
          <IconBag size={13} />
        </div>

        <div className={styles.mobilePhotoWrap} aria-hidden="true">
          <Image
            src={photo}
            alt=""
            fill
            sizes="(max-width: 900px) 78vw, 240px"
            className={styles.coverTop}
          />
        </div>

        <div className={styles.mobileSheet} aria-hidden="true">
          <div className={styles.mobileMetaRow}>
            <span className={styles.mobileProductName}>{template.sampleProduct}</span>
            <span className={`${styles.mobilePrice} num`}>
              {formatVnd(template.samplePriceVnd)}
            </span>
          </div>
          <div className={styles.mobileSizes}>
            <span className={styles.sizeChip}>S</span>
            <span className={`${styles.sizeChip} ${styles.sizeChipActive}`}>M</span>
            <span className={styles.sizeChip}>L</span>
            <span className={styles.mobileAddBtn}>Thêm vào giỏ</span>
          </div>
        </div>
      </div>
    );
  }

  if (variant === "site-fallback") {
    return (
      <div
        className={`${styles.stage} ${styles.stageFallback}`}
        style={stageStyle}
        {...wrapperA11y}
      >
        <div className={styles.desktopNav} aria-hidden="true">
          <span className={styles.navBrand}>{title}</span>
          <div className={styles.navLinks}>
            <span>Sản phẩm</span>
            <span>Bộ sưu tập</span>
            <IconBag size={12} />
          </div>
        </div>
        <div className={styles.fallbackHero} aria-hidden="true">
          {logoUrl ? (
            <img
              src={logoUrl}
              alt=""
              width={64}
              height={64}
              loading="lazy"
              className={styles.fallbackLogo}
            />
          ) : null}
          <span className={styles.fallbackWord}>{title}</span>
          {heroCopy ? <span className={styles.fallbackTagline}>{heroCopy}</span> : null}
          <span className={styles.heroCta}>Xem cửa hàng</span>
        </div>
      </div>
    );
  }

  const isStudio = variant === "studio";

  return (
    <div
      className={`${styles.stage} ${isStudio ? styles.stageStudio : styles.stageDesktop}`}
      style={stageStyle}
      {...wrapperA11y}
    >
      <div className={styles.desktopNav} aria-hidden="true">
        <span className={styles.navBrand}>{title}</span>
        <div className={styles.navLinks}>
          <span>Hàng mới</span>
          <span>Bộ sưu tập</span>
          {!isStudio ? <span>Giới thiệu</span> : null}
          <IconBag size={13} />
        </div>
      </div>

      <div
        className={`${styles.desktopHero} ${selectedLabel ? styles.desktopHeroSelected : ""}`}
        aria-hidden="true"
      >
        <Image
          src={photo}
          alt=""
          fill
          sizes={isStudio ? "(max-width: 900px) 85vw, 560px" : "(max-width: 900px) 85vw, 520px"}
          className={styles.coverHero}
        />
        <div className={styles.heroScrim} />

        {selectedLabel ? <span className={styles.selectedTag}>{selectedLabel}</span> : null}

        <div className={styles.heroOverlayCopy}>
          <span className={styles.heroHeadline}>{heroCopy}</span>
          <span className={styles.heroCta}>Xem bộ sưu tập</span>
        </div>
      </div>

      <div className={styles.desktopBottomStrip} aria-hidden="true">
        {template.chips.map((chip) => (
          <span key={chip} className={styles.stripTile}>
            <span className={styles.stripDot} />
            <span className={styles.stripText}>{chip}</span>
          </span>
        ))}
        <span className={styles.stripTile}>
          <span className={styles.stripText}>{template.sampleProduct}</span>
          <span className={`${styles.stripPrice} num`}>{formatVnd(template.samplePriceVnd)}</span>
        </span>
      </div>
    </div>
  );
}
