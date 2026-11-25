"use client";

import React, { useEffect, useRef, useState } from "react";
import Image from "next/image";
import { IconCheck } from "@/components/Icons";
import { SHOWCASE_TEMPLATES } from "./showcaseTemplates";
import styles from "./PlatformPinnedStory.module.css";

interface StoryStep {
  id: "theme" | "catalog" | "order";
  sentence: string;
}

const STEPS: readonly StoryStep[] = [
  { id: "theme", sentence: "Chọn giao diện riêng" },
  { id: "catalog", sentence: "Đăng sản phẩm lên kệ" },
  { id: "order", sentence: "Nhận đơn, quét VietQR" },
];

const CATALOG_ITEMS = [
  {
    id: "t-shirt",
    name: "Cotton Compact",
    img: "/models/model-ao-thun-trang.jpg",
    alt: "Mẫu áo thun cotton trắng",
  },
  {
    id: "polo",
    name: "Polo Pique",
    img: "/models/model-polo-xanh-da.jpg",
    alt: "Mẫu áo polo xanh đá",
  },
  {
    id: "short",
    name: "Short Daily",
    img: "/models/model-short-daily-be.jpg",
    alt: "Mẫu quần short be cát",
  },
  {
    id: "jacket",
    name: "Áo Gió Nhẹ",
    img: "/models/model-ao-khoac-gio.jpg",
    alt: "Mẫu áo khoác gió xám tro",
  },
] as const;

function VietQrSvg() {
  const finder = (r: number, c: number) =>
    (r < 8 && c < 8) || (r < 8 && c > 12) || (r > 12 && c < 8);
  const cells: Array<[number, number]> = [];
  for (let r = 0; r < 21; r++) {
    for (let c = 0; c < 21; c++) {
      if (!finder(r, c) && (r * 17 + c * 31 + r * c * 7) % 11 < 5) cells.push([r, c]);
    }
  }
  return (
    <svg
      viewBox="0 0 210 210"
      width={220}
      height={220}
      className={styles.qrSvg}
      role="img"
      aria-label="Mã VietQR"
    >
      <rect width="210" height="210" fill="var(--bg-0)" />
      {[
        [12, 12],
        [138, 12],
        [12, 138],
      ].map(([x, y]) => (
        <g key={`${x}-${y}`}>
          <rect x={x} y={y} width="60" height="60" fill="var(--ink-1)" />
          <rect x={x + 9} y={y + 9} width="42" height="42" fill="var(--bg-0)" />
          <rect x={x + 18} y={y + 18} width="24" height="24" fill="var(--ink-1)" />
        </g>
      ))}
      {cells.map(([r, c]) => (
        <rect
          key={`${r}-${c}`}
          x={12 + c * 9}
          y={12 + r * 9}
          width="7.6"
          height="7.6"
          fill="var(--ink-1)"
        />
      ))}
    </svg>
  );
}

function StageContent({
  stepIndex,
  selectedTpl,
  onSelectTpl,
}: {
  stepIndex: number;
  selectedTpl: number;
  onSelectTpl: (idx: number) => void;
}) {
  const tpl = SHOWCASE_TEMPLATES[selectedTpl] ?? SHOWCASE_TEMPLATES[0];

  if (stepIndex === 0) {
    return (
      <div
        className={styles.themeStage}
        style={
          {
            "--tpl-bg": tpl.bg,
            "--tpl-surface": tpl.surface,
            "--tpl-text": tpl.text,
            "--tpl-accent": tpl.accent,
            "--tpl-accent-text": tpl.accentText,
          } as React.CSSProperties
        }
      >
        <div className={styles.swatchBar} role="group" aria-label="Chọn mẫu giao diện">
          {SHOWCASE_TEMPLATES.map((opt, idx) => {
            const active = idx === selectedTpl;
            return (
              <button
                key={opt.key}
                type="button"
                aria-pressed={active}
                aria-label={`Mẫu ${opt.label}`}
                className={`${styles.swatchBtn} ${active ? styles.swatchBtnActive : ""}`}
                onClick={() => onSelectTpl(idx)}
              >
                <span
                  className={styles.swatchDot}
                  style={{ background: opt.accent }}
                  aria-hidden="true"
                />
                <span className={styles.swatchName}>{opt.label}</span>
              </button>
            );
          })}
        </div>

        <div key={tpl.key} className={styles.themePreview}>
          <div className={styles.themePhotoMain}>
            <Image
              src={tpl.primaryImg}
              alt={tpl.primaryAlt}
              fill
              sizes="(max-width: 960px) 100vw, 42vw"
              className={styles.coverImg}
            />
          </div>

          <div className={styles.themeSideCol}>
            <div className={styles.themePhotoInset}>
              <Image
                src={tpl.secondaryImg}
                alt={tpl.secondaryAlt}
                fill
                sizes="(max-width: 960px) 50vw, 20vw"
                className={styles.coverImg}
              />
            </div>
            <div className={styles.themeMeta}>
              <span className={`${styles.themeBrand} type-tight`}>{tpl.defaultBrand}</span>
              <span className={styles.themeTag}>{tpl.headline}</span>
            </div>
          </div>
        </div>
      </div>
    );
  }

  if (stepIndex === 1) {
    return (
      <div className={styles.catalogStage} key="stage-catalog">
        {CATALOG_ITEMS.map((item, idx) => (
          <figure
            key={item.id}
            className={styles.catalogTile}
            style={{ "--i": idx } as React.CSSProperties}
          >
            <Image
              src={item.img}
              alt={item.alt}
              fill
              sizes="(max-width: 960px) 50vw, 30vw"
              className={styles.coverImg}
            />
            <figcaption className={styles.catalogLabel}>{item.name}</figcaption>
          </figure>
        ))}
      </div>
    );
  }

  return (
    <div className={styles.orderStage} key="stage-order">
      <div className={styles.qrFrame}>
        <VietQrSvg />
      </div>

      <div className={styles.orderCard}>
        <span className={styles.orderBadge}>
          <IconCheck size={14} />
          <span>Đơn mới</span>
        </span>
        <p className={`${styles.orderLine} type-tight`}>Thanh toán VietQR</p>
      </div>
    </div>
  );
}

export function PlatformPinnedStory() {
  const [activeStep, setActiveStep] = useState(0);
  const [selectedTpl, setSelectedTpl] = useState(0);
  const stepRefs = useRef<Array<HTMLDivElement | null>>([]);

  useEffect(() => {
    if (typeof window === "undefined" || typeof IntersectionObserver === "undefined") return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    const nodes = stepRefs.current.filter((n): n is HTMLDivElement => n !== null);
    if (nodes.length === 0) return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            const idx = Number((entry.target as HTMLElement).dataset.stepIndex);
            if (Number.isFinite(idx)) {
              setActiveStep(idx);
            }
          }
        }
      },
      { rootMargin: "-36% 0px -44% 0px", threshold: 0.05 }
    );

    for (const node of nodes) observer.observe(node);
    return () => observer.disconnect();
  }, []);

  return (
    <section
      id="cach-van-hanh"
      className={styles.band}
      aria-labelledby="platform-pinned-story-heading"
    >
      <div className={styles.head}>
        <h2
          id="platform-pinned-story-heading"
          className={`${styles.heading} type-wide`}
          data-reveal=""
        >
          Ba bước lên sóng
        </h2>
      </div>

      {/* Desktop pinned split */}
      <div className={styles.pinnedGrid}>
        <div className={styles.stepsCol}>
          {STEPS.map((step, idx) => {
            const active = activeStep === idx;
            return (
              <div
                key={step.id}
                ref={(el) => {
                  stepRefs.current[idx] = el;
                }}
                data-step-index={idx}
                className={`${styles.stepTriggerWrap} ${active ? styles.stepTriggerActive : ""}`}
              >
                <button
                  type="button"
                  className={styles.stepButton}
                  aria-current={active ? "step" : undefined}
                  onClick={() => setActiveStep(idx)}
                >
                  <span className={`${styles.stepSentence} type-tight`}>{step.sentence}</span>
                </button>
              </div>
            );
          })}
        </div>

        <div className={styles.stageWrap}>
          <div className={styles.stageSticky} data-ui="pinned-story-stage">
            <StageContent
              stepIndex={activeStep}
              selectedTpl={selectedTpl}
              onSelectTpl={setSelectedTpl}
            />
          </div>
        </div>
      </div>

      {/* Mobile & reduced-motion vertical stack */}
      <div className={styles.mobileStack}>
        {STEPS.map((step, idx) => (
          <article key={step.id} className={styles.mobileStep}>
            <h3 className={`${styles.mobileStepTitle} type-tight`}>{step.sentence}</h3>
            <div className={styles.mobileStage} data-ui={`pinned-story-mobile-${step.id}`}>
              <StageContent
                stepIndex={idx}
                selectedTpl={selectedTpl}
                onSelectTpl={setSelectedTpl}
              />
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
