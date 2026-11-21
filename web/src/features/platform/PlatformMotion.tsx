"use client";

import React, { useEffect, useRef } from "react";

/**
 * Progressive scroll-reveal observer for platform pages on the root host.
 * Elements with `[data-reveal]` start visible under `prefers-reduced-motion: reduce` (CSS in globals.css)
 * and animate into place once scrolled into view when motion is permitted.
 */
export function PlatformMotion({ depKey = "" }: { depKey?: string | number }) {
  useEffect(() => {
    if (typeof window === "undefined") return;
    const targets = Array.from(document.querySelectorAll<HTMLElement>("[data-reveal]"));
    if (targets.length === 0) return;

    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced || typeof IntersectionObserver === "undefined") {
      targets.forEach((el) => el.classList.add("is-in"));
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            entry.target.classList.add("is-in");
            observer.unobserve(entry.target);
          }
        }
      },
      { rootMargin: "0px 0px -6% 0px", threshold: 0.06 }
    );

    const vh = window.innerHeight;
    for (const el of targets) {
      const rect = el.getBoundingClientRect();
      if (rect.top < vh * 0.94) {
        el.classList.add("is-in");
      } else {
        observer.observe(el);
      }
    }

    return () => observer.disconnect();
  }, [depKey]);

  return null;
}

export interface AmbientLoomVideoProps {
  src: string;
  poster: string;
  alt: string;
  width: number;
  height: number;
  className?: string;
}

/** Looping workshop loom clip that pauses automatically under `prefers-reduced-motion: reduce`. */
export function AmbientLoomVideo({
  src,
  poster,
  alt,
  width,
  height,
  className,
}: AmbientLoomVideoProps) {
  const ref = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    const vid = ref.current;
    if (!vid || typeof window === "undefined") return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const sync = () => {
      if (mq.matches) vid.pause();
      else vid.play().catch(() => {});
    };
    sync();
    mq.addEventListener?.("change", sync);
    return () => mq.removeEventListener?.("change", sync);
  }, []);

  return (
    <video
      ref={ref}
      src={src}
      poster={poster}
      width={width}
      height={height}
      autoPlay
      muted
      loop
      playsInline
      preload="metadata"
      aria-label={alt}
      className={className}
    />
  );
}
