import React from "react";
import Link from "next/link";
import styles from "./Button.module.css";

export type ButtonVariant = "primary" | "ink" | "secondary" | "outline" | "ghost" | "danger";
export type ButtonSize = "sm" | "md" | "lg";
export type ButtonShape = "square" | "pill" | "app";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  shape?: ButtonShape;
  fullWidth?: boolean;
  loading?: boolean;
  loadingLabel?: string;
  href?: string;
}

const SHAPE_CLASS: Record<ButtonShape, string> = {
  square: styles.shapeSquare,
  pill: styles.shapePill,
  app: styles.shapeApp,
};

export function Button({
  variant = "primary",
  size = "md",
  shape = "square",
  fullWidth = false,
  loading = false,
  loadingLabel = "Đang xử lý",
  href,
  className,
  children,
  disabled,
  type = "button",
  ...rest
}: ButtonProps) {
  const isPlainLabel = typeof children === "string" || typeof children === "number";
  const labelText = isPlainLabel ? String(children) : null;
  const isDisabled = Boolean(disabled || loading);
  const hasSlideLabel = isPlainLabel && !loading;

  const classes = [
    styles.btn,
    styles[variant],
    styles[size],
    SHAPE_CLASS[shape],
    fullWidth ? styles.fullWidth : "",
    loading ? styles.loading : "",
    hasSlideLabel ? styles.hasSlideLabel : "",
    className ?? "",
  ]
    .filter(Boolean)
    .join(" ");

  const innerContent =
    labelText !== null ? (
      <span className={styles.labelViewport}>
        <span className={styles.labelTrack} data-label={labelText}>
          {labelText}
        </span>
      </span>
    ) : (
      <span className={styles.content}>{children}</span>
    );

  if (href) {
    const {
      id,
      title,
      role,
      tabIndex,
      onClick,
      "aria-label": ariaLabel,
      "aria-current": ariaCurrent,
      "aria-describedby": ariaDescribedBy,
    } = rest;

    if (isDisabled) {
      return (
        <span
          id={id}
          title={title}
          role={role ?? "link"}
          aria-label={ariaLabel}
          aria-current={ariaCurrent}
          aria-describedby={ariaDescribedBy}
          aria-busy={loading || undefined}
          aria-disabled="true"
          className={classes}
        >
          {innerContent}
          {loading && (
            <>
              <span className={styles.loadingTrack} aria-hidden="true">
                <span className={styles.loadingBar} />
              </span>
              <span className="sr-only">{loadingLabel}</span>
            </>
          )}
        </span>
      );
    }

    const linkClickProps =
      typeof onClick === "function"
        ? { onClick: onClick as unknown as React.MouseEventHandler<HTMLAnchorElement> }
        : {};

    return (
      <Link
        href={href}
        id={id}
        title={title}
        role={role}
        tabIndex={tabIndex}
        aria-label={ariaLabel}
        aria-current={ariaCurrent}
        aria-describedby={ariaDescribedBy}
        className={classes}
        {...linkClickProps}
      >
        {innerContent}
      </Link>
    );
  }

  return (
    <button
      type={type}
      className={classes}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      {...rest}
    >
      {innerContent}
      {loading && (
        <>
          <span className={styles.loadingTrack} aria-hidden="true">
            <span className={styles.loadingBar} />
          </span>
          <span className="sr-only">{loadingLabel}</span>
        </>
      )}
    </button>
  );
}
