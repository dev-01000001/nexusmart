"use client";

import React, { useEffect, useRef } from "react";

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
      muted
      loop
      playsInline
      preload="metadata"
      aria-label={alt}
      className={className}
    />
  );
}
