"use client";

import React, { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { IconArrowUpRight } from "@/components/Icons";
import { StateView } from "@/components/StateView";
import type { DirectorySite } from "./directoryModel";
import styles from "./SiteDirectorySection.module.css";

export function DirectoryErrorState() {
  const router = useRouter();
  return (
    <StateView
      kind="error"
      title="Chưa thể tải danh bạ lúc này."
      actionLabel="Tải lại"
      onAction={() => router.refresh()}
    />
  );
}

export interface SiteDirectoryDoorsProps {
  doors: DirectorySite[];
  extra: DirectorySite[];
}

const FALLBACK_TONES = ["#1f3038", "#233935", "#2c2a32", "#1e3440", "#2f332c", "#26313a"] as const;

export function SiteDirectoryDoors({ doors, extra }: SiteDirectoryDoorsProps) {
  const [activeDoor, setActiveDoor] = useState<number | null>(null);

  const gridTemplateColumns = useMemo(() => {
    const count = doors.length;
    if (count <= 1) return "1fr";
    if (activeDoor === null || activeDoor < 0 || activeDoor >= count) {
      return Array.from({ length: count }, () => "1fr").join(" ");
    }
    const expanded = count === 2 ? "1.65fr" : count === 3 ? "2fr" : "2.35fr";
    const resting = count === 2 ? "0.75fr" : "0.78fr";
    return Array.from({ length: count }, (_, idx) =>
      idx === activeDoor ? expanded : resting
    ).join(" ");
  }, [doors.length, activeDoor]);

  return (
    <>
      <div
        className={styles.doorsStage}
        style={{ "--door-cols": gridTemplateColumns } as React.CSSProperties}
        onMouseLeave={() => setActiveDoor(null)}
      >
        {doors.map((site, idx) => {
          const isExpanded = activeDoor === idx;
          const monogram = site.name.trim().slice(0, 1).toUpperCase() || "N";
          const fallbackBg = FALLBACK_TONES[idx % FALLBACK_TONES.length];

          return (
            <a
              key={site.siteId}
              href={site.origin}
              className={`${styles.door} ${isExpanded ? styles.doorActive : ""}`}
              onMouseEnter={() => setActiveDoor(idx)}
              onFocus={() => setActiveDoor(idx)}
              onBlur={(e) => {
                if (!e.currentTarget.parentElement?.contains(e.relatedTarget as Node | null)) {
                  setActiveDoor(null);
                }
              }}
            >
              <div className={styles.doorMedia} style={{ background: fallbackBg }}>
                {site.coverUrl ? (
                  <img
                    src={site.coverUrl}
                    alt={`Ảnh bìa cửa hàng ${site.name}`}
                    width={1280}
                    height={900}
                    loading="lazy"
                    className={styles.doorImg}
                  />
                ) : site.logoUrl ? (
                  <div className={styles.doorFallback}>
                    <img
                      src={site.logoUrl}
                      alt={`Biểu trưng ${site.name}`}
                      width={88}
                      height={88}
                      loading="lazy"
                      className={styles.doorFallbackLogo}
                    />
                  </div>
                ) : (
                  <div className={styles.doorFallback} aria-hidden="true">
                    <span className={`${styles.doorMonogram} type-poster`}>{monogram}</span>
                  </div>
                )}
              </div>

              <div className={styles.doorPlinth}>
                <div className={styles.doorTitleRow}>
                  <div className={styles.doorIdentity}>
                    {site.logoUrl && (
                      <img
                        src={site.logoUrl}
                        alt=""
                        width={24}
                        height={24}
                        loading="lazy"
                        className={styles.doorLogo}
                        aria-hidden="true"
                      />
                    )}
                    <h3 className={`${styles.doorName} type-tight`}>{site.name}</h3>
                  </div>
                  <span className={styles.doorArrow} aria-hidden="true">
                    <IconArrowUpRight size={18} />
                  </span>
                </div>
                <p className={styles.doorHost}>{site.host}</p>
                {site.tagline && <p className={styles.doorTagline}>{site.tagline}</p>}
              </div>
            </a>
          );
        })}
      </div>

      {extra.length > 0 && (
        <ul className={styles.extraList} data-reveal="rows">
          {extra.map((site, idx) => (
            <li key={site.siteId}>
              <a href={site.origin} className={styles.extraRow}>
                <div className={styles.extraMain}>
                  <span className={`${styles.extraName} type-tight`}>{site.name}</span>
                  {site.tagline && <span className={styles.extraTagline}>{site.tagline}</span>}
                </div>

                <div className={styles.extraRight}>
                  <span className={styles.extraHost}>{site.host}</span>
                  <IconArrowUpRight size={18} className={styles.extraArrow} />
                </div>

                <span
                  className={styles.extraPreview}
                  style={{ background: FALLBACK_TONES[idx % FALLBACK_TONES.length] }}
                  aria-hidden="true"
                >
                  {site.coverUrl || site.logoUrl ? (
                    <img
                      src={site.coverUrl || site.logoUrl}
                      alt=""
                      width={260}
                      height={164}
                      loading="lazy"
                      className={styles.extraPreviewImg}
                    />
                  ) : (
                    <span className={`${styles.extraPreviewLetter} type-poster`}>
                      {site.name.trim().slice(0, 1).toUpperCase() || "N"}
                    </span>
                  )}
                </span>
              </a>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
