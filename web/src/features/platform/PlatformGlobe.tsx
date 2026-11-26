"use client";

import { useEffect, useRef, useState } from "react";
import { IconTruck, IconWallet } from "@/components/Icons";
import type { GlobeController, LandDot } from "./globeRenderer";
import styles from "./PlatformGlobe.module.css";

const LAND_DOTS_URL = "/globe/land-dots.json";

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

  const rootClassName = className ? `${styles.root} ${className}` : styles.root;

  return (
    <div ref={rootRef} className={rootClassName}>
      <div
        className={canvasReady ? `${styles.fallback} ${styles.fallbackHidden}` : styles.fallback}
        aria-hidden="true"
      />
      <canvas
        ref={canvasRef}
        className={canvasReady ? `${styles.canvas} ${styles.canvasReady}` : styles.canvas}
        aria-hidden="true"
      />
      <ul className={styles.cards}>
        <li className={`${styles.card} ${styles.cardLeft}`}>
          <span className={styles.cardIcon} aria-hidden="true">
            <IconTruck size={15} />
          </span>
          <span className={styles.cardBody}>
            <span className={styles.cardTitle}>34 TỈNH THÀNH</span>
            <span className={styles.cardDetail}>TÍNH PHÍ GIAO HÀNG THEO TỪNG NƠI</span>
          </span>
        </li>
        <li className={`${styles.card} ${styles.cardRight}`}>
          <span className={styles.cardIcon} aria-hidden="true">
            <IconWallet size={15} />
          </span>
          <span className={styles.cardBody}>
            <span className={styles.cardTitle}>COD VÀ VIETQR</span>
            <span className={styles.cardDetail}>CÓ SẴN TRÊN MỌI CỬA HÀNG</span>
          </span>
        </li>
      </ul>
    </div>
  );
}
