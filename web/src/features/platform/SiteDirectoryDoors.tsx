"use client";

import React from "react";
import { useRouter } from "next/navigation";
import { IconAlert, IconArrowUpRight, IconRefresh } from "@/components/Icons";
import type { DirectorySite } from "./directoryModel";
import styles from "./SiteDirectorySection.module.css";

export function DirectoryErrorState() {
  const router = useRouter();
  return (
    <div className={styles.errorBanner} role="alert">
      <div className={styles.errorCopy}>
        <IconAlert size={18} className={styles.errorIcon} />
        <p className={styles.errorTitle}>Chưa thể tải danh bạ lúc này.</p>
      </div>
      <button type="button" className={styles.errorBtn} onClick={() => router.refresh()}>
        <IconRefresh size={14} />
        <span>Tải lại</span>
      </button>
    </div>
  );
}

export interface SiteDirectoryDoorsProps {
  doors: DirectorySite[];
  extra: DirectorySite[];
}

const FALLBACK_TONES = [
  "var(--band-dark)",
  "var(--band-dark-surface)",
  "color-mix(in srgb, var(--accent) 20%, var(--ink-1))",
  "var(--band-dark)",
  "var(--band-dark-surface)",
  "color-mix(in srgb, var(--accent) 16%, var(--ink-1))",
] as const;

export function SiteDirectoryDoors({ doors, extra }: SiteDirectoryDoorsProps) {
  return (
    <>
      <div className={styles.mosaic} data-count={doors.length} data-reveal="rows">
        {doors.map((site, idx) => {
          const monogram = site.name.trim().slice(0, 1).toUpperCase() || "N";
          const fallbackBg = FALLBACK_TONES[idx % FALLBACK_TONES.length];

          return (
            <a key={site.siteId} href={site.origin} className={styles.siteCard}>
              <div className={styles.cardMedia} style={{ background: fallbackBg }}>
                {site.coverUrl ? (
                  <img
                    src={site.coverUrl}
                    alt={`Ảnh bìa cửa hàng ${site.name}`}
                    width={960}
                    height={720}
                    loading="lazy"
                    className={styles.cardImg}
                  />
                ) : site.logoUrl ? (
                  <div className={styles.cardFallback}>
                    <img
                      src={site.logoUrl}
                      alt={`Biểu trưng ${site.name}`}
                      width={80}
                      height={80}
                      loading="lazy"
                      className={styles.cardFallbackLogo}
                    />
                  </div>
                ) : (
                  <div className={styles.cardFallback} aria-hidden="true">
                    <span className={styles.cardMonogram}>{monogram}</span>
                  </div>
                )}
              </div>

              <div className={styles.cardMeta}>
                <div className={styles.cardTitleRow}>
                  <div className={styles.cardIdentity}>
                    {site.logoUrl && (
                      <img
                        src={site.logoUrl}
                        alt=""
                        width={22}
                        height={22}
                        loading="lazy"
                        className={styles.cardLogo}
                        aria-hidden="true"
                      />
                    )}
                    <h3 className={styles.cardName}>{site.name}</h3>
                  </div>
                  <span className={styles.cardArrow} aria-hidden="true">
                    <IconArrowUpRight size={16} />
                  </span>
                </div>
                <p className={styles.cardHost}>{site.host}</p>
              </div>
            </a>
          );
        })}
      </div>

      {extra.length > 0 && (
        <ul className={styles.extraList} data-reveal="rows">
          {extra.map((site) => (
            <li key={site.siteId}>
              <a href={site.origin} className={styles.extraRow}>
                <div className={styles.extraMain}>
                  <span className={styles.extraName}>{site.name}</span>
                  {site.tagline && <span className={styles.extraTagline}>{site.tagline}</span>}
                </div>

                <div className={styles.extraRight}>
                  <span className={styles.extraHost}>{site.host}</span>
                  <IconArrowUpRight size={16} className={styles.extraArrow} />
                </div>
              </a>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
