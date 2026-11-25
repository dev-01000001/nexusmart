import React from "react";
import { Button } from "./Button";
import { IconAlert, IconRefresh } from "./Icons";
import styles from "./StateView.module.css";

export type StateSkeletonVariant = "table" | "cards" | "detail";

export interface StateViewProps {
  kind: "loading" | "empty" | "error";
  surface?: "shop" | "app";
  skeletonVariant?: StateSkeletonVariant;
  title?: string;
  message?: string;
  lastUpdated?: string;
  actionLabel?: string;
  actionHref?: string;
  onAction?: () => void;
  rows?: number;
}

export function StateView({
  kind,
  surface = "shop",
  skeletonVariant,
  title,
  message,
  lastUpdated,
  actionLabel,
  actionHref,
  onAction,
  rows = 4,
}: StateViewProps) {
  const isApp = surface === "app";
  const resolvedSkeleton: StateSkeletonVariant = skeletonVariant ?? (isApp ? "table" : "cards");
  const wrapClass = [styles.container, isApp ? styles.appContainer : "", styles[kind]]
    .filter(Boolean)
    .join(" ");

  if (kind === "loading") {
    return (
      <div className={wrapClass} role="status" aria-live="polite" aria-label={title ?? "Đang tải"}>
        {resolvedSkeleton === "cards" ? (
          <div className={styles.skeletonCards} aria-hidden="true">
            {Array.from({ length: Math.max(2, Math.min(rows, 6)) }).map((_, idx) => (
              <div key={idx} className={styles.skeletonCard}>
                <div className={styles.skeletonMedia} />
                <div className={styles.skeletonLineWide} />
                <div className={styles.skeletonLineShort} />
              </div>
            ))}
          </div>
        ) : resolvedSkeleton === "detail" ? (
          <div className={styles.skeletonDetail} aria-hidden="true">
            <div className={styles.skeletonTall} />
            <div className={styles.skeletonLineWide} />
            <div className={styles.skeletonLineMid} />
            <div className={styles.skeletonBlock} />
          </div>
        ) : (
          <div className={styles.skeletonWrap} aria-hidden="true">
            <div className={styles.skeletonTall} />
            {Array.from({ length: rows }).map((_, idx) => (
              <div key={idx} className={styles.skeletonRow}>
                <div className={styles.skeletonBar} />
                <div className={styles.skeletonBar} />
                <div className={styles.skeletonBar} />
              </div>
            ))}
          </div>
        )}
        <span className="sr-only">{title ?? "Đang tải dữ liệu"}</span>
      </div>
    );
  }

  if (kind === "error") {
    const primaryLine = message ?? title ?? "Không thể tải dữ liệu lúc này.";
    const headingLine = message && title ? title : null;

    return (
      <div className={wrapClass} role="alert">
        <div className={styles.copyStack}>
          {headingLine ? (
            <>
              <p className={styles.errorHeading}>
                <IconAlert size={16} />
                <span>{headingLine}</span>
              </p>
              <p className={styles.message}>{primaryLine}</p>
            </>
          ) : (
            <p className={styles.errorHeading}>
              <IconAlert size={16} />
              <span>{primaryLine}</span>
            </p>
          )}
          {lastUpdated && <p className={styles.meta}>Cập nhật: {lastUpdated}</p>}
        </div>
        {(onAction || actionHref) && (
          <div className={styles.actions}>
            <Button
              variant={isApp ? "outline" : "secondary"}
              size="sm"
              shape={isApp ? "app" : "square"}
              href={actionHref}
              onClick={onAction}
            >
              <IconRefresh size={14} />
              <span>{actionLabel ?? "Thử lại"}</span>
            </Button>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className={wrapClass}>
      <div className={styles.copyStack}>
        <p className={styles.title}>{title ?? "Chưa có dữ liệu."}</p>
        {message && <p className={styles.message}>{message}</p>}
      </div>
      {(onAction || actionHref) && actionLabel && (
        <div className={styles.actions}>
          <Button
            variant={isApp ? "ink" : "primary"}
            size="sm"
            shape={isApp ? "app" : "square"}
            href={actionHref}
            onClick={onAction}
          >
            {actionLabel}
          </Button>
        </div>
      )}
    </div>
  );
}
