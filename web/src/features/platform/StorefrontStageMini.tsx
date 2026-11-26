import React from "react";
import Image from "next/image";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import { suggestSubdomain } from "./subdomain";
import styles from "./StorefrontStageMini.module.css";

export interface StorefrontStageMiniProps {
  rootDomain: string;
}

export function StorefrontStageMini({ rootDomain }: StorefrontStageMiniProps) {
  const stage = SHOWCASE_TEMPLATES[0];
  const slug = suggestSubdomain(stage.defaultBrand) || "ten-ban";

  const stageStyle = {
    "--stage-bg": stage.bg,
    "--stage-surface": stage.surface,
    "--stage-text": stage.text,
    "--stage-accent": stage.accent,
    "--stage-accent-text": stage.accentText,
  } as React.CSSProperties;

  return (
    <div
      className={styles.stage}
      style={stageStyle}
      role="img"
      aria-label="Minh họa giao diện cửa hàng riêng với thanh địa chỉ tên miền phụ và bảng màu tùy biến"
      data-ui="mini-storefront"
    >
      <div className={styles.topBar} aria-hidden="true">
        <div className={styles.addressPill}>
          <span className={styles.addressDot} />
          <span className={styles.addressText}>
            {slug}.{rootDomain}
          </span>
        </div>

        <div className={styles.swatches}>
          {SHOWCASE_TEMPLATES.map((tpl, idx) => (
            <span
              key={tpl.key}
              className={`${styles.swatchRing} ${idx === 0 ? styles.swatchRingActive : ""}`}
            >
              <span className={styles.swatchDot} style={{ background: tpl.accent }} />
            </span>
          ))}
        </div>
      </div>

      <div className={styles.canvas} aria-hidden="true">
        <div className={styles.headRow}>
          <span className={styles.brandName}>{stage.defaultBrand}</span>
          <span className={styles.badge}>{stage.label}</span>
        </div>

        <div className={styles.visualGrid}>
          <div className={styles.mainPhoto}>
            <Image
              src={stage.primaryImg}
              alt=""
              fill
              sizes="(max-width: 900px) 52vw, 16vw"
              className={styles.coverImg}
            />
          </div>

          <div className={styles.sideCol}>
            <div className={styles.sidePhoto}>
              <Image
                src={stage.secondaryImg}
                alt=""
                fill
                sizes="(max-width: 900px) 42vw, 14vw"
                className={styles.coverImg}
              />
            </div>
            <div className={styles.sideCopy}>
              <p className={styles.headline}>{stage.headline}</p>
              <span className={styles.miniCta}>Xem bộ sưu tập</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
