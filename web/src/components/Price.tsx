import React from "react";
import { formatDiscountPercent, formatVnd } from "@/lib/format";
import styles from "./Price.module.css";

export interface PriceProps {
  amount: number;
  originalAmount?: number;
  size?: "sm" | "md" | "lg";
  showDiscount?: boolean;
  className?: string;
}

export function Price({
  amount,
  originalAmount,
  size = "md",
  showDiscount = false,
  className,
}: PriceProps) {
  const hasDiscount = originalAmount != null && originalAmount > amount;
  return (
    <span className={[styles.wrap, styles[size], className ?? ""].filter(Boolean).join(" ")}>
      <span className={styles.current}>{formatVnd(amount)}</span>
      {hasDiscount && <span className={styles.original}>{formatVnd(originalAmount)}</span>}
      {hasDiscount && showDiscount && (
        <span className={styles.discount}>{formatDiscountPercent(originalAmount, amount)}</span>
      )}
    </span>
  );
}
