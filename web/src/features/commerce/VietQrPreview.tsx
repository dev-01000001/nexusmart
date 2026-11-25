"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { IconCheck, IconCopy } from "@/components/Icons";
import { useStore } from "@/context/StoreContext";
import styles from "./commerce.module.css";

export interface VietQrPreviewProps {
  amount: number;
  reference: string;
  qrCode?: string;
  bankName?: string;
  accountNo?: string;
  accountName?: string;
  expiresAt?: string;
}

type CopyKey = "amount" | "account" | "reference";

/**
 * Centered high-contrast flat SVG VietQR stage with one-click copy for amount, account, and transfer ref.
 */
export function VietQrPreview({
  amount,
  reference,
  qrCode,
  bankName = "ACB, TP. Hồ Chí Minh",
  accountNo = "2026 8841 9001",
  accountName = "Công ty TNHH NexusMart Việt Nam",
  expiresAt,
}: VietQrPreviewProps) {
  const { showToast } = useStore();
  const [copiedKey, setCopiedKey] = useState<CopyKey | null>(null);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    };
  }, []);

  const modules = useMemo(() => {
    const size = 21;
    const grid: boolean[][] = Array.from({ length: size }, () => Array(size).fill(false));

    const drawFinder = (ox: number, oy: number) => {
      for (let y = 0; y < 7; y++) {
        for (let x = 0; x < 7; x++) {
          const border = x === 0 || x === 6 || y === 0 || y === 6;
          const core = x >= 2 && x <= 4 && y >= 2 && y <= 4;
          grid[oy + y][ox + x] = border || core;
        }
      }
    };

    drawFinder(0, 0);
    drawFinder(size - 7, 0);
    drawFinder(0, size - 7);

    let hash = 2166136261;
    const seedStr = `${qrCode || reference}-${amount}`;
    for (let i = 0; i < seedStr.length; i++) {
      hash ^= seedStr.charCodeAt(i);
      hash = Math.imul(hash, 16777619);
    }

    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        const inTopLeft = x < 8 && y < 8;
        const inTopRight = x >= size - 8 && y < 8;
        const inBottomLeft = x < 8 && y >= size - 8;
        if (inTopLeft || inTopRight || inBottomLeft) continue;
        if (x === 6 || y === 6) {
          grid[y][x] = (x + y) % 2 === 0;
          continue;
        }
        hash ^= (x * 31 + y * 17) & 0xff;
        hash = Math.imul(hash, 16777619);
        grid[y][x] = (hash >>> 28) % 2 === 0;
      }
    }

    return grid;
  }, [amount, qrCode, reference]);

  const handleCopy = async (key: CopyKey, rawValue: string, label: string) => {
    try {
      await navigator.clipboard.writeText(rawValue);
      setCopiedKey(key);
      showToast(`Đã sao chép ${label}.`);
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => setCopiedKey(null), 2200);
    } catch {
      showToast(`${label}: ${rawValue}`);
    }
  };

  return (
    <div className={styles.qrStage}>
      <div className={styles.qrFrame} data-reveal="scale">
        <svg
          viewBox="0 0 21 21"
          width="232"
          height="232"
          className={styles.qrSvg}
          role="img"
          aria-label={`Mã VietQR thanh toán ${formatVnd(amount)}`}
        >
          <rect width="21" height="21" fill="#ffffff" />
          {modules.map((row, y) =>
            row.map((on, x) =>
              on ? (
                <rect
                  key={`${x}-${y}`}
                  x={x}
                  y={y}
                  width="1"
                  height="1"
                  fill={x >= 8 && x <= 12 && y >= 8 && y <= 12 ? "#0d7480" : "#152229"}
                />
              ) : null
            )
          )}
        </svg>
      </div>

      <div className={styles.qrDetails}>
        <div className={styles.qrRow}>
          <div className={styles.qrRowMain}>
            <span className={styles.qrLabel}>Số tiền</span>
            <span className={`${styles.qrAmount} num`}>{formatVnd(amount)}</span>
          </div>
          <button
            type="button"
            className={`${styles.copyBtn} ${copiedKey === "amount" ? styles.copyBtnDone : ""}`}
            onClick={() => void handleCopy("amount", String(amount), "số tiền")}
          >
            {copiedKey === "amount" ? <IconCheck size={14} /> : <IconCopy size={14} />}
            <span>{copiedKey === "amount" ? "Đã chép" : "Chép số tiền"}</span>
          </button>
        </div>

        <div className={styles.qrRow}>
          <div className={styles.qrRowMain}>
            <span className={styles.qrLabel}>Nội dung chuyển khoản</span>
            <span className={`${styles.qrVal} num`}>{reference}</span>
          </div>
          <button
            type="button"
            className={`${styles.copyBtn} ${copiedKey === "reference" ? styles.copyBtnDone : ""}`}
            onClick={() => void handleCopy("reference", reference, "nội dung chuyển khoản")}
          >
            {copiedKey === "reference" ? <IconCheck size={14} /> : <IconCopy size={14} />}
            <span>{copiedKey === "reference" ? "Đã chép" : "Chép nội dung"}</span>
          </button>
        </div>

        <div className={styles.qrRow}>
          <div className={styles.qrRowMain}>
            <span className={styles.qrLabel}>{bankName}</span>
            <span className={`${styles.qrVal} num`}>{accountNo}</span>
            <span className={styles.qrLabel}>{accountName}</span>
          </div>
          <button
            type="button"
            className={`${styles.copyBtn} ${copiedKey === "account" ? styles.copyBtnDone : ""}`}
            onClick={() =>
              void handleCopy("account", accountNo.replace(/\s+/g, ""), "số tài khoản")
            }
          >
            {copiedKey === "account" ? <IconCheck size={14} /> : <IconCopy size={14} />}
            <span>{copiedKey === "account" ? "Đã chép" : "Chép STK"}</span>
          </button>
        </div>

        {expiresAt && (
          <div className={styles.qrRow}>
            <div className={styles.qrRowMain}>
              <span className={styles.qrLabel}>Hạn chuyển khoản</span>
              <span className={`${styles.qrVal} num`}>{formatDateTimeVi(expiresAt)}</span>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
