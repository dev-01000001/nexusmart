"use client";

import React, { useMemo } from "react";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { Button } from "@/components/Button";
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

/**
 * Crisp deterministic flat SVG VietQR display in Frost & Slate palette (no card form).
 */
export function VietQrPreview({
  amount,
  reference,
  qrCode,
  bankName = "ACB · Chi nhánh TP. Hồ Chí Minh",
  accountNo = "2026 8841 9001",
  accountName = "Công ty TNHH NexusMart Việt Nam",
  expiresAt,
}: VietQrPreviewProps) {
  const { showToast } = useStore();

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

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(reference);
      showToast(`Đã sao chép nội dung chuyển khoản ${reference}.`);
    } catch {
      showToast(`Nội dung chuyển khoản: ${reference}`);
    }
  };

  return (
    <div className={styles.qrWrap}>
      <div className={styles.qrBox}>
        <svg
          viewBox="0 0 21 21"
          width="148"
          height="148"
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
                  fill={x >= 8 && x <= 12 && y >= 8 && y <= 12 ? "#0d7480" : "#1f3038"}
                />
              ) : null
            )
          )}
        </svg>
      </div>

      <div className={styles.qrInfo}>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Ngân hàng thụ hưởng</span>
          <span className={styles.qrVal}>{bankName}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Số tài khoản</span>
          <span className={`${styles.qrVal} num`}>{accountNo}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Chủ tài khoản</span>
          <span className={styles.qrVal}>{accountName}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Số tiền chuyển khoản</span>
          <span className={`${styles.qrAmount} num`}>{formatVnd(amount)}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Nội dung bắt buộc</span>
          <span className={`${styles.qrVal} num`}>{reference}</span>
        </div>
        {expiresAt && (
          <div className={styles.qrRow}>
            <span className={styles.qrLabel}>Hạn thanh toán</span>
            <span className={`${styles.qrVal} num`}>{formatDateTimeVi(expiresAt)}</span>
          </div>
        )}
        <div className={styles.formActions}>
          <Button variant="secondary" size="sm" onClick={handleCopy}>
            Sao chép nội dung
          </Button>
        </div>
        <p className={styles.qrNote}>
          Mở ứng dụng ngân hàng để quét mã và chuyển khoản đúng số tiền cùng nội dung ở trên. Đơn
          hàng sẽ tự động xác nhận ngay khi nhận được thanh toán.
        </p>
      </div>
    </div>
  );
}
