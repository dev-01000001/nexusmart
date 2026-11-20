import React from "react";
import { Button } from "./Button";
import { IconAlert, IconRefresh } from "./Icons";
import styles from "./StateView.module.css";

export interface StateViewProps {
  kind: "loading" | "empty" | "error";
  surface?: "shop" | "app";
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
  title,
  message,
  lastUpdated,
  actionLabel,
  actionHref,
  onAction,
  rows = 4,
}: StateViewProps) {
  const wrapClass = [styles.container, surface === "app" ? styles.appContainer : ""]
    .filter(Boolean)
    .join(" ");

  if (kind === "loading") {
    return (
      <div className={wrapClass} role="status" aria-live="polite" aria-label="Đang tải dữ liệu">
        {title && <p className={styles.meta}>{title}</p>}
        <div className={styles.skeletonWrap}>
          <div className={styles.skeletonTall} />
          {Array.from({ length: rows }).map((_, idx) => (
            <div key={idx} className={styles.skeletonRow}>
              <div className={styles.skeletonBar} />
              <div className={styles.skeletonBar} />
              <div className={styles.skeletonBar} />
            </div>
          ))}
        </div>
      </div>
    );
  }

  if (kind === "error") {
    return (
      <div className={wrapClass} role="alert">
        <p className={styles.errorHeading}>
          <IconAlert size={16} />
          <span>{title ?? "Không thể tải dữ liệu lúc này"}</span>
        </p>
        <p className={styles.message}>
          {message ?? "Kết nối đến máy chủ tạm gián đoạn. Vui lòng thử tải lại."}
        </p>
        {lastUpdated && <p className={styles.meta}>Cập nhật gần nhất: {lastUpdated}</p>}
        {(onAction || actionHref) && (
          <div className={styles.actions}>
            <Button
              variant={surface === "app" ? "outline" : "secondary"}
              size="sm"
              shape={surface === "app" ? "app" : "square"}
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
      <p className={styles.title}>{title ?? "Chưa có dữ liệu trong mục này"}</p>
      {message && <p className={styles.message}>{message}</p>}
      {(onAction || actionHref) && actionLabel && (
        <div className={styles.actions}>
          <Button
            variant={surface === "app" ? "ink" : "primary"}
            size="sm"
            shape={surface === "app" ? "app" : "square"}
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
