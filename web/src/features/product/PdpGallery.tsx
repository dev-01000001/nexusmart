"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import type { Product } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { IconChevronRight, IconClose, IconPlay } from "@/components";
import styles from "./PdpGallery.module.css";

export interface PdpGalleryProps {
  product: Product;
  activeImage: string;
  onSelectImage: (src: string) => void;
}

export function PdpGallery({ product, activeImage, onSelectImage }: PdpGalleryProps) {
  const onModelSrc = product.onModelImage ?? MODEL_SHOTS[product.id]?.src ?? null;

  // Keep flat-lay primary first and on-model shot immediately second for clear comparison
  const images = useMemo(() => {
    const ordered = [
      product.image,
      ...(onModelSrc ? [onModelSrc] : []),
      ...product.gallery,
      ...product.colors.map((c) => c.image),
    ];
    return Array.from(new Set(ordered));
  }, [product, onModelSrc]);

  const isModelShot = (src: string) => src.startsWith("/models/") || src === onModelSrc;

  const getImageLabel = (src: string, idx: number): string => {
    if (isModelShot(src)) return "Ảnh người mặc";
    const matchedColor = product.colors.find((c) => c.image === src);
    if (matchedColor) return idx === 0 ? "Ảnh sản phẩm" : `Màu ${matchedColor.name}`;
    if (src.startsWith("/atmosphere/") || src.startsWith("/banners/")) return "Cận chất liệu";
    return `Góc nhìn ${idx + 1}`;
  };

  const getImageAlt = (src: string, idx: number): string => {
    if (isModelShot(src)) {
      return MODEL_SHOTS[product.id]?.alt ?? `${product.name} - ảnh người mẫu mặc thực tế`;
    }
    const matchedColor = product.colors.find((c) => c.image === src);
    if (matchedColor) return `${product.name} - màu ${matchedColor.name}`;
    if (src.startsWith("/atmosphere/") || src.startsWith("/banners/")) {
      return `Cận cảnh bề mặt vải của ${product.name}`;
    }
    return `${product.name} - góc nhìn ${idx + 1}`;
  };

  const currentIndex = Math.max(0, images.indexOf(activeImage));
  const currentSrc = images[currentIndex] ?? product.image;
  const currentIsModel = isModelShot(currentSrc);

  // Bind video mode to the image key so clicking a color swatch cleanly exits video mode
  const [videoKey, setVideoKey] = useState<string | null>(null);
  const [videoPlaying, setVideoPlaying] = useState<boolean>(true);
  const [lightboxOpen, setLightboxOpen] = useState<boolean>(false);
  const [lightboxIdx, setLightboxIdx] = useState<number>(0);

  const videoRef = useRef<HTMLVideoElement>(null);
  const hasVideo = Boolean(product.hasVideo && product.videoUrl);
  const isVideoMode = hasVideo && videoKey === currentSrc;

  useEffect(() => {
    if (!isVideoMode || !videoRef.current) return;
    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) {
      videoRef.current.pause();
      return;
    }
    videoRef.current
      .play()
      .then(() => setVideoPlaying(true))
      .catch(() => setVideoPlaying(false));
  }, [isVideoMode]);

  useEffect(() => {
    if (!lightboxOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setLightboxOpen(false);
      } else if (e.key === "ArrowRight") {
        setLightboxIdx((idx) => (idx + 1) % images.length);
      } else if (e.key === "ArrowLeft") {
        setLightboxIdx((idx) => (idx - 1 + images.length) % images.length);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [lightboxOpen, images.length]);

  const handleStep = (delta: number) => {
    setVideoKey(null);
    const nextIdx = (currentIndex + delta + images.length) % images.length;
    onSelectImage(images[nextIdx]);
  };

  const handlePickImage = (src: string) => {
    setVideoKey(null);
    onSelectImage(src);
  };

  const handleOpenVideo = () => {
    if (!hasVideo) return;
    if (isVideoMode) {
      setVideoKey(null);
    } else {
      setVideoKey(currentSrc);
      setVideoPlaying(true);
    }
  };

  const handleTogglePlay = () => {
    const el = videoRef.current;
    if (!el) return;
    if (el.paused) {
      el.play()
        .then(() => setVideoPlaying(true))
        .catch(() => setVideoPlaying(false));
    } else {
      el.pause();
      setVideoPlaying(false);
    }
  };

  const handleOpenLightbox = (startIndex: number) => {
    setLightboxIdx(startIndex);
    setLightboxOpen(true);
  };

  const lightboxSrc = images[lightboxIdx] ?? product.image;
  const lightboxIsModel = isModelShot(lightboxSrc);

  return (
    <div className={styles.wrap}>
      <div className={styles.mainFrame}>
        {isVideoMode && product.videoUrl ? (
          <div className={styles.videoStage}>
            <video
              ref={videoRef}
              src={product.videoUrl}
              poster={product.videoPoster}
              muted
              loop
              playsInline
              className={styles.mainVideo}
              aria-label={`Video chất liệu ${product.shortName}`}
            />
            <div className={styles.videoBar}>
              <button
                type="button"
                className={styles.videoCtrlBtn}
                onClick={handleTogglePlay}
                aria-label={videoPlaying ? "Tạm dừng video chất vải" : "Phát video chất vải"}
              >
                <IconPlay size={14} />
                <span>{videoPlaying ? "Tạm dừng" : "Phát video"}</span>
              </button>
              <button
                type="button"
                className={styles.videoCtrlBtn}
                onClick={() => setVideoKey(null)}
              >
                Quay lại ảnh sản phẩm
              </button>
            </div>
          </div>
        ) : (
          <>
            <Image
              src={currentSrc}
              alt={getImageAlt(currentSrc, currentIndex)}
              fill
              priority
              sizes="(max-width: 1024px) 100vw, 50vw"
              className={currentIsModel ? styles.mainImgModel : styles.mainImg}
            />
            {images.length > 1 && (
              <>
                <button
                  type="button"
                  className={`${styles.navBtn} ${styles.navPrev}`}
                  onClick={() => handleStep(-1)}
                  aria-label="Ảnh sản phẩm trước"
                >
                  <IconChevronRight size={16} className={styles.chevronLeft} />
                </button>
                <button
                  type="button"
                  className={`${styles.navBtn} ${styles.navNext}`}
                  onClick={() => handleStep(1)}
                  aria-label="Ảnh sản phẩm tiếp theo"
                >
                  <IconChevronRight size={16} />
                </button>
              </>
            )}
            <span className={styles.viewLabel}>{getImageLabel(currentSrc, currentIndex)}</span>
            <span className={styles.counter} aria-live="polite">
              {currentIndex + 1}/{images.length}
            </span>
          </>
        )}
      </div>

      <div className={styles.controlsRow}>
        {onModelSrc && (
          <button
            type="button"
            className={`${styles.actionBtn} ${!isVideoMode && currentIsModel ? styles.actionBtnActive : ""}`}
            onClick={() => handlePickImage(currentIsModel ? product.image : onModelSrc)}
            aria-pressed={!isVideoMode && currentIsModel}
            aria-label={currentIsModel ? "Xem ảnh sản phẩm" : "Xem ảnh người mặc"}
          >
            {currentIsModel ? "Ảnh sản phẩm" : "Ảnh người mặc"}
          </button>
        )}

        <button
          type="button"
          className={styles.actionBtn}
          onClick={() => handleOpenLightbox(currentIndex)}
          aria-label={`Phóng lớn bộ ${images.length} ảnh`}
        >
          Phóng lớn ({images.length})
        </button>

        {hasVideo && (
          <button
            type="button"
            className={`${styles.actionBtn} ${isVideoMode ? styles.actionBtnActive : ""}`}
            onClick={handleOpenVideo}
            aria-pressed={isVideoMode}
            aria-label={isVideoMode ? "Đang xem video chất vải" : "Xem video chất vải"}
          >
            <IconPlay size={14} />
            <span>{isVideoMode ? "Đang xem video" : "Video vải"}</span>
          </button>
        )}
      </div>

      {(images.length > 1 || hasVideo) && (
        <div className={styles.thumbRow} role="group" aria-label="Thư viện ảnh và video sản phẩm">
          {images.map((src, idx) => {
            const active = !isVideoMode && currentSrc === src;
            const modelThumb = isModelShot(src);
            const thumbLabel = getImageLabel(src, idx);
            return (
              <button
                key={src}
                type="button"
                className={`${styles.thumbBtn} ${active ? styles.thumbBtnActive : ""}`}
                onClick={() => handlePickImage(src)}
                aria-label={`${thumbLabel} của ${product.shortName}`}
                aria-pressed={active}
                title={thumbLabel}
              >
                <Image
                  src={src}
                  alt={getImageAlt(src, idx)}
                  fill
                  sizes="96px"
                  className={modelThumb ? styles.thumbImgModel : styles.thumbImg}
                />
                {modelThumb && <span className={styles.thumbTag}>Người mặc</span>}
              </button>
            );
          })}

          {hasVideo && product.videoPoster && (
            <button
              type="button"
              className={`${styles.thumbBtn} ${styles.videoThumb} ${isVideoMode ? styles.thumbBtnActive : ""}`}
              onClick={handleOpenVideo}
              aria-label={`Phát video chất vải ${product.shortName}`}
              aria-pressed={isVideoMode}
            >
              <Image
                src={product.videoPoster}
                alt={`Ảnh bìa video chất vải ${product.shortName}`}
                fill
                sizes="96px"
                className={styles.thumbImg}
              />
              <span className={styles.videoThumbTag} aria-hidden="true">
                <IconPlay size={13} />
                <span>Video</span>
              </span>
            </button>
          )}
        </div>
      )}

      {lightboxOpen && (
        <div
          className={styles.lightboxBackdrop}
          role="dialog"
          aria-modal="true"
          aria-labelledby="pdp-lightbox-title"
          onClick={(e) => {
            if (e.target === e.currentTarget) setLightboxOpen(false);
          }}
        >
          <div className={styles.lightboxPanel}>
            <div className={styles.lightboxHeader}>
              <h2 id="pdp-lightbox-title" className={styles.lightboxTitle}>
                Thư viện ảnh {product.shortName} ({lightboxIdx + 1}/{images.length})
              </h2>
              <button
                type="button"
                className={styles.lightboxClose}
                onClick={() => setLightboxOpen(false)}
                aria-label="Đóng thư viện ảnh"
              >
                <IconClose size={18} />
              </button>
            </div>

            <div className={styles.lightboxBody}>
              <div className={styles.lightboxStage}>
                <div className={styles.lightboxMediaFrame}>
                  <Image
                    src={lightboxSrc}
                    alt={getImageAlt(lightboxSrc, lightboxIdx)}
                    fill
                    sizes="(max-width: 1024px) 92vw, 560px"
                    className={lightboxIsModel ? styles.lightboxImgModel : styles.lightboxMainImg}
                  />
                </div>
                {images.length > 1 && (
                  <div className={styles.lightboxNav}>
                    <button
                      type="button"
                      className={styles.actionBtn}
                      onClick={() =>
                        setLightboxIdx((idx) => (idx - 1 + images.length) % images.length)
                      }
                    >
                      Ảnh trước
                    </button>
                    <button
                      type="button"
                      className={styles.actionBtn}
                      onClick={() => setLightboxIdx((idx) => (idx + 1) % images.length)}
                    >
                      Ảnh tiếp theo
                    </button>
                  </div>
                )}
              </div>

              <div className={styles.lightboxGrid} role="group" aria-label="Chọn ảnh trong bộ">
                {images.map((src, idx) => {
                  const modelThumb = isModelShot(src);
                  return (
                    <button
                      key={src}
                      type="button"
                      className={`${styles.lightboxThumb} ${lightboxIdx === idx ? styles.lightboxThumbActive : ""}`}
                      onClick={() => {
                        setLightboxIdx(idx);
                        handlePickImage(src);
                      }}
                      aria-label={`Chọn ${getImageLabel(src, idx)}`}
                      aria-pressed={lightboxIdx === idx}
                    >
                      <Image
                        src={src}
                        alt={getImageAlt(src, idx)}
                        fill
                        sizes="180px"
                        className={modelThumb ? styles.thumbImgModel : styles.thumbImg}
                      />
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
