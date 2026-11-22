"use client";

import React, { useMemo, useState } from "react";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { Button } from "@/components/Button";
import styles from "./commerce.module.css";

export interface VietQrPreviewProps {
  amount: number;
  reference: string;
  qrUrl?: string;
  qrCode?: string;
  expiresAt?: string;
}

interface ParsedBankMeta {
  bankName: string;
  accountNo: string;
  accountName: string;
  transferNote: string;
}

const BANK_BIN_NAMES: Record<string, string> = {
  "970436": "Vietcombank",
  "970416": "ACB",
  "970407": "Techcombank",
  "970422": "MB Bank",
  "970415": "VietinBank",
  "970418": "BIDV",
  "970432": "VPBank",
  "970423": "TPBank",
};

function parseQrUrlMeta(qrUrl: string | undefined, fallbackRef: string): ParsedBankMeta {
  const defaults: ParsedBankMeta = {
    bankName: "ACB · Ngân hàng Á Châu",
    accountNo: "2026 8841 9001",
    accountName: "Công ty TNHH NexusMart Việt Nam",
    transferNote: fallbackRef,
  };
  if (!qrUrl) return defaults;

  try {
    const parsed = new URL(qrUrl);
    const accParam = parsed.searchParams.get("acc");
    const bankParam = parsed.searchParams.get("bank");
    const addInfo = parsed.searchParams.get("addInfo") || parsed.searchParams.get("des");
    const accountName = parsed.searchParams.get("accountName");

    let bankName = bankParam || defaults.bankName;
    let accountNo = accParam || defaults.accountNo;

    // Parse standard img.vietqr.io/image/<BIN>-<ACC>-compact2.png path
    const imgMatch = parsed.pathname.match(/\/image\/([^-]+)-([^-/.]+)/i);
    if (imgMatch) {
      const binOrCode = imgMatch[1];
      bankName = BANK_BIN_NAMES[binOrCode] ?? binOrCode;
      accountNo = imgMatch[2];
    }

    let safeAccountName = defaults.accountName;
    if (accountName) {
      const decoded = decodeURIComponent(accountName).trim();
      if (decoded && !/(sandbox|mock)/i.test(decoded)) {
        safeAccountName = decoded;
      }
    }

    return {
      bankName,
      accountNo,
      accountName: safeAccountName,
      transferNote: addInfo || fallbackRef,
    };
  } catch {
    return defaults;
  }
}

/**
 * Displays the real VietQR image when available and falls back to a crisp flat SVG QR
 * in Frost & Slate palette (zero card forms).
 */
export function VietQrPreview({ amount, reference, qrUrl, qrCode, expiresAt }: VietQrPreviewProps) {
  const [imgFailed, setImgFailed] = useState(false);
  const [copiedField, setCopiedField] = useState<"ref" | "qr" | null>(null);

  const bankMeta = useMemo(() => parseQrUrlMeta(qrUrl, reference), [qrUrl, reference]);

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

  const copyText = async (value: string, field: "ref" | "qr") => {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedField(field);
      window.setTimeout(() => {
        setCopiedField((cur) => (cur === field ? null : cur));
      }, 2500);
    } catch {
      setCopiedField(field);
    }
  };

  const showRemoteQr = Boolean(qrUrl) && !imgFailed;

  return (
    <div className={styles.qrWrap}>
      <div className={styles.qrBox}>
        {showRemoteQr ? (
          <img
            src={qrUrl}
            alt={`Mã VietQR thanh toán ${formatVnd(amount)}`}
            width={148}
            height={148}
            className={styles.qrImage}
            onError={() => setImgFailed(true)}
          />
        ) : (
          <svg
            viewBox="0 0 21 21"
            width="148"
            height="148"
            role="img"
            aria-label={`Mã VietQR thanh toán ${formatVnd(amount)}`}
          >
            <rect width="21" height="21" fill="var(--surface-white)" />
            {modules.map((row, y) =>
              row.map((on, x) =>
                on ? (
                  <rect
                    key={`${x}-${y}`}
                    x={x}
                    y={y}
                    width="1"
                    height="1"
                    fill={x >= 8 && x <= 12 && y >= 8 && y <= 12 ? "var(--accent)" : "var(--ink)"}
                  />
                ) : null
              )
            )}
          </svg>
        )}
      </div>

      <div className={styles.qrInfo}>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Ngân hàng thụ hưởng</span>
          <span className={styles.qrVal}>{bankMeta.bankName}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Số tài khoản</span>
          <span className={`${styles.qrVal} num`}>{bankMeta.accountNo}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Chủ tài khoản</span>
          <span className={styles.qrVal}>{bankMeta.accountName}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Số tiền chuyển khoản</span>
          <span className={`${styles.qrAmount} num`}>{formatVnd(amount)}</span>
        </div>
        <div className={styles.qrRow}>
          <span className={styles.qrLabel}>Nội dung bắt buộc</span>
          <span className={`${styles.qrVal} num`}>{bankMeta.transferNote}</span>
        </div>
        {expiresAt && (
          <div className={styles.qrRow}>
            <span className={styles.qrLabel}>Hạn thanh toán</span>
            <span className={`${styles.qrVal} num`}>{formatDateTimeVi(expiresAt)}</span>
          </div>
        )}
        <div className={styles.formActions}>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void copyText(bankMeta.transferNote, "ref")}
          >
            {copiedField === "ref" ? "Đã sao chép nội dung" : "Sao chép nội dung"}
          </Button>
          {qrCode && (
            <Button variant="secondary" size="sm" onClick={() => void copyText(qrCode, "qr")}>
              {copiedField === "qr" ? "Đã sao chép chuỗi QR" : "Sao chép chuỗi QR"}
            </Button>
          )}
        </div>
        <p className={styles.qrNote}>
          Mở ứng dụng ngân hàng để quét mã và chuyển đúng số tiền cùng nội dung chuyển khoản. Đơn
          hàng sẽ được xác nhận tự động, không cần nhập số thẻ.
        </p>
      </div>
    </div>
  );
}
