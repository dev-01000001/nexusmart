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
  bankName: string | null;
  accountNo: string | null;
  accountName: string | null;
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
  const empty: ParsedBankMeta = {
    bankName: null,
    accountNo: null,
    accountName: null,
    transferNote: fallbackRef,
  };
  if (!qrUrl) return empty;

  try {
    const parsed = new URL(qrUrl);
    const accParam = parsed.searchParams.get("acc")?.trim() || null;
    const bankParam = parsed.searchParams.get("bank")?.trim() || null;
    const addInfo =
      parsed.searchParams.get("addInfo")?.trim() || parsed.searchParams.get("des")?.trim() || "";
    const rawAccountName = parsed.searchParams.get("accountName")?.trim() || "";

    let bankName = bankParam;
    let accountNo = accParam;

    // Parse standard img.vietqr.io/image/<BIN>-<ACC>-compact2.png path
    const imgMatch = parsed.pathname.match(/\/image\/([^-]+)-([^-/.]+)/i);
    if (imgMatch) {
      const binOrCode = imgMatch[1];
      bankName = BANK_BIN_NAMES[binOrCode] ?? binOrCode;
      accountNo = imgMatch[2];
    }

    const safeAccountName =
      rawAccountName && !/(sandbox|mock)/i.test(rawAccountName) ? rawAccountName : null;

    return {
      bankName,
      accountNo,
      accountName: safeAccountName,
      transferNote: addInfo || fallbackRef,
    };
  } catch {
    return empty;
  }
}

/**
 * Displays the real VietQR image when available and falls back to an explicit error + reload state
 * with real transfer metadata (never shows fake hardcoded bank accounts or unscannable noise QR).
 */
export function VietQrPreview({ amount, reference, qrUrl, qrCode, expiresAt }: VietQrPreviewProps) {
  const [imgFailed, setImgFailed] = useState(false);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [copiedField, setCopiedField] = useState<"ref" | "qr" | null>(null);

  const bankMeta = useMemo(() => parseQrUrlMeta(qrUrl, reference), [qrUrl, reference]);

  const effectiveQrUrl = useMemo(() => {
    if (!qrUrl) return "";
    if (reloadNonce === 0) return qrUrl;
    try {
      const u = new URL(qrUrl);
      u.searchParams.set("_r", String(reloadNonce));
      return u.toString();
    } catch {
      return qrUrl;
    }
  }, [qrUrl, reloadNonce]);

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

  const showRemoteQr = Boolean(effectiveQrUrl) && !imgFailed;

  return (
    <div className={styles.qrWrap}>
      <div className={styles.qrBox}>
        {showRemoteQr ? (
          <img
            src={effectiveQrUrl}
            alt={`Mã VietQR thanh toán ${formatVnd(amount)}`}
            width={148}
            height={148}
            className={styles.qrImage}
            onError={() => setImgFailed(true)}
          />
        ) : (
          <div role="status" className={styles.qrFallbackBox}>
            <span className={styles.qrFallbackText}>
              {qrUrl
                ? "Không tải được ảnh mã VietQR."
                : "Chưa có ảnh mã VietQR từ cổng thanh toán."}
            </span>
            {qrUrl && (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  setImgFailed(false);
                  setReloadNonce((n) => n + 1);
                }}
              >
                Tải lại ảnh QR
              </Button>
            )}
          </div>
        )}
      </div>

      <div className={styles.qrInfo}>
        {bankMeta.bankName && (
          <div className={styles.qrRow}>
            <span className={styles.qrLabel}>Ngân hàng thụ hưởng</span>
            <span className={styles.qrVal}>{bankMeta.bankName}</span>
          </div>
        )}
        {bankMeta.accountNo && (
          <div className={styles.qrRow}>
            <span className={styles.qrLabel}>Số tài khoản</span>
            <span className={`${styles.qrVal} num`}>{bankMeta.accountNo}</span>
          </div>
        )}
        {bankMeta.accountName && (
          <div className={styles.qrRow}>
            <span className={styles.qrLabel}>Chủ tài khoản</span>
            <span className={styles.qrVal}>{bankMeta.accountName}</span>
          </div>
        )}
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
