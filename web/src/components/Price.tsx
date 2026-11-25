import React from "react";
import { formatDiscountPercent, formatVnd } from "@/lib/format";
import styles from "./Price.module.css";

export type PriceSize = "sm" | "md" | "lg";
export type PriceTone = "default" | "accent" | "inverted";

export interface PriceProps {
  amount: number;
  maxAmount?: number;
  originalAmount?: number;
  size?: PriceSize;
  tone?: PriceTone;
  showDiscount?: boolean;
  className?: string;
}

const TONE_CLASS: Record<PriceTone, string> = {
  default: "",
  accent: styles.toneAccent,
  inverted: styles.toneInverted,
};

export function Price({
  amount,
  maxAmount,
  originalAmount,
  size = "md",
  tone = "default",
  showDiscount = false,
  className,
}: PriceProps) {
  const hasRange = maxAmount != null && maxAmount > amount;
  const upperDisplayed = hasRange ? maxAmount : amount;
  const hasDiscount = originalAmount != null && originalAmount > upperDisplayed;
  return (
    <span
      className={[styles.wrap, styles[size], TONE_CLASS[tone], className ?? ""]
        .filter(Boolean)
        .join(" ")}
    >
      <span className={styles.current}>
        {hasRange ? `${formatVnd(amount)} – ${formatVnd(maxAmount)}` : formatVnd(amount)}
      </span>
      {hasDiscount && <span className={styles.original}>{formatVnd(originalAmount)}</span>}
      {hasDiscount && showDiscount && (
        <span className={styles.discount}>{formatDiscountPercent(originalAmount, amount)}</span>
      )}
    </span>
  );
}
