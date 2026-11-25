"use client";

import React, { useEffect, useMemo, useState } from "react";
import Image from "next/image";
import { IconPause, IconPlay } from "@/components/Icons";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import { SubdomainPreview } from "./SubdomainPreview";
import { suggestSubdomain } from "./subdomain";
import styles from "./HeroLiveDoor.module.css";

export interface HeroLiveDoorProps {
  rootDomain: string;
}

export function HeroLiveDoor({ rootDomain }: HeroLiveDoorProps) {
  const [brandInput, setBrandInput] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const [userPaused, setUserPaused] = useState(false);
  const [hoverPaused, setHoverPaused] = useState(false);
  const [focusPaused, setFocusPaused] = useState(false);
  const [reducedMotion, setReducedMotion] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const sync = () => setReducedMotion(mq.matches);
    sync();
    mq.addEventListener?.("change", sync);
    return () => mq.removeEventListener?.("change", sync);
  }, []);

  const isRunning = !userPaused && !hoverPaused && !focusPaused && !reducedMotion;

  useEffect(() => {
    if (!isRunning) return;
    const id = window.setInterval(() => {
      setActiveIndex((prev) => (prev + 1) % SHOWCASE_TEMPLATES.length);
    }, 4000);
    return () => window.clearInterval(id);
  }, [isRunning]);

  const stage = SHOWCASE_TEMPLATES[activeIndex] ?? SHOWCASE_TEMPLATES[0];
  const typedTrimmed = brandInput.trim();
  const displayBrand = typedTrimmed || stage.defaultBrand;

  const displaySlug = useMemo(() => {
    const slug = suggestSubdomain(brandInput);
    if (slug) return slug;
    return suggestSubdomain(stage.defaultBrand) || "ten-ban";
  }, [brandInput, stage.defaultBrand]);

  const stageStyle = {
    "--stage-bg": stage.bg,
    "--stage-surface": stage.surface,
    "--stage-text": stage.text,
    "--stage-accent": stage.accent,
    "--stage-accent-text": stage.accentText,
  } as React.CSSProperties;

  return (
    <section className={styles.hero} aria-label="Giới thiệu nền tảng NexusMart">
      <div className={styles.leftCol}>
        <h1 className={`${styles.headline} type-wide lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Cửa hàng
            </span>
          </span>
          <span className="line">
            <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
              mang tên bạn
            </span>
          </span>
        </h1>

        <div
          className={styles.doorCluster}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <p className={styles.lead}>Gõ tên thương hiệu, xem cửa hàng riêng hiện ra ngay.</p>
          <SubdomainPreview
            rootDomain={rootDomain}
            value={brandInput}
            onValueChange={setBrandInput}
          />
        </div>
      </div>

      <div
        className={styles.stageCol}
        data-ui="live-storefront"
        style={stageStyle}
        onMouseEnter={() => setHoverPaused(true)}
        onMouseLeave={() => setHoverPaused(false)}
        onFocus={() => setFocusPaused(true)}
        onBlur={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
            setFocusPaused(false);
          }
        }}
      >
        <div className={styles.stageTopBar}>
          <div className={styles.addressPill}>
            <span className={styles.addressDot} aria-hidden="true" />
            <span className={styles.addressSlug}>{displaySlug}</span>
            <span className={styles.addressDomain}>.{rootDomain}</span>
          </div>

          <div className={styles.stageControls}>
            <div className={styles.dots} role="group" aria-label="Chọn giao diện cửa hàng mẫu">
              {SHOWCASE_TEMPLATES.map((item, idx) => {
                const selected = idx === activeIndex;
                return (
                  <button
                    key={item.key}
                    type="button"
                    aria-pressed={selected}
                    aria-label={`Mẫu ${item.label}`}
                    className={`${styles.dotBtn} ${selected ? styles.dotBtnActive : ""}`}
                    onClick={() => setActiveIndex(idx)}
                  >
                    <span
                      className={styles.dotSwatch}
                      style={{ background: item.accent }}
                      aria-hidden="true"
                    />
                  </button>
                );
              })}
            </div>

            {!reducedMotion && (
              <button
                type="button"
                className={styles.pauseBtn}
                onClick={() => setUserPaused((v) => !v)}
                aria-pressed={userPaused}
                aria-label={
                  userPaused ? "Tiếp tục chuyển mẫu tự động" : "Tạm dừng chuyển mẫu tự động"
                }
              >
                {userPaused ? <IconPlay size={14} filled /> : <IconPause size={14} />}
              </button>
            )}
          </div>
        </div>

        <div key={stage.key} className={styles.stageCanvas} data-archetype={stage.key}>
          {stage.key === "sport-active" && (
            <div className={styles.sportLayout}>
              <div className={styles.sportHeader}>
                <span className={`${styles.sportBrand} type-poster`}>{displayBrand}</span>
                <div className={styles.chipRow}>
                  {stage.chips.map((c) => (
                    <span key={c} className={styles.sportChip}>
                      {c}
                    </span>
                  ))}
                </div>
              </div>

              <div className={styles.sportVisualGrid}>
                <div className={styles.sportMainPhoto}>
                  <Image
                    src={stage.primaryImg}
                    alt={stage.primaryAlt}
                    fill
                    priority
                    sizes="(max-width: 960px) 100vw, 42vw"
                    className={styles.coverImg}
                  />
                </div>
                <div className={styles.sportSideCard}>
                  <div className={styles.sportInsetPhoto}>
                    <Image
                      src={stage.secondaryImg}
                      alt={stage.secondaryAlt}
                      fill
                      priority
                      sizes="(max-width: 960px) 50vw, 18vw"
                      className={styles.coverImg}
                    />
                  </div>
                  <div className={styles.sportCaptionBlock}>
                    <p className={`${styles.stageHeadline} type-tight`}>{stage.headline}</p>
                    <span className={styles.mockCta}>Bộ sưu tập mới</span>
                  </div>
                </div>
              </div>
            </div>
          )}

          {stage.key === "luxury-maison" && (
            <div className={styles.maisonLayout}>
              <div className={styles.maisonTop}>
                <span className={styles.maisonBrand}>{displayBrand}</span>
              </div>

              <div className={styles.maisonCenter}>
                <div className={styles.maisonPortrait}>
                  <Image
                    src={stage.primaryImg}
                    alt={stage.primaryAlt}
                    fill
                    priority
                    sizes="(max-width: 960px) 60vw, 24vw"
                    className={styles.coverImg}
                  />
                </div>

                <div className={styles.maisonAside}>
                  <div className={styles.maisonTexture}>
                    <Image
                      src={stage.secondaryImg}
                      alt={stage.secondaryAlt}
                      fill
                      sizes="(max-width: 960px) 40vw, 20vw"
                      className={styles.coverImg}
                    />
                  </div>
                  <p className={styles.maisonHeadline}>{stage.headline}</p>
                  <span className={styles.maisonCta}>Khám phá</span>
                </div>
              </div>
            </div>
          )}

          {stage.key === "craft-atelier" && (
            <div className={styles.atelierLayout}>
              <div className={styles.atelierLeft}>
                <div className={styles.atelierHead}>
                  <span className={`${styles.atelierBrand} type-wide`}>{displayBrand}</span>
                  <p className={styles.atelierSub}>{stage.headline}</p>
                </div>
                <div className={styles.atelierBanner}>
                  <Image
                    src={stage.primaryImg}
                    alt={stage.primaryAlt}
                    fill
                    priority
                    sizes="(max-width: 960px) 100vw, 34vw"
                    className={styles.coverImg}
                  />
                </div>
              </div>

              <div className={styles.atelierRight}>
                <div className={styles.atelierModel}>
                  <Image
                    src={stage.secondaryImg}
                    alt={stage.secondaryAlt}
                    fill
                    sizes="(max-width: 960px) 50vw, 20vw"
                    className={styles.coverImg}
                  />
                </div>
                <div className={styles.atelierSpecs}>
                  {stage.chips.map((c) => (
                    <span key={c} className={styles.atelierSpecTag}>
                      {c}
                    </span>
                  ))}
                </div>
              </div>
            </div>
          )}

          {stage.key === "editorial" && (
            <div className={styles.editorialLayout}>
              <div className={styles.editorialMasthead}>
                <span className={`${styles.editorialBrand} type-tight`}>{displayBrand}</span>
                <span className={styles.editorialPill}>Bộ sưu tập</span>
              </div>

              <div className={styles.editorialSpread}>
                <div className={styles.editorialWide}>
                  <Image
                    src={stage.primaryImg}
                    alt={stage.primaryAlt}
                    fill
                    priority
                    sizes="(max-width: 960px) 100vw, 36vw"
                    className={styles.coverImg}
                  />
                </div>
                <div className={styles.editorialColumn}>
                  <div className={styles.editorialPortrait}>
                    <Image
                      src={stage.secondaryImg}
                      alt={stage.secondaryAlt}
                      fill
                      sizes="(max-width: 960px) 50vw, 18vw"
                      className={styles.coverImg}
                    />
                  </div>
                  <p className={`${styles.editorialQuote} type-normal`}>{stage.headline}</p>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
