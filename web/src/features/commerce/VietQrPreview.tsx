"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { Button } from "@/components/Button";
import { IconCheck, IconCopy } from "@/components/Icons";
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

function parseSafeQrImageUrl(raw: string | undefined): string {
  const trimmed = (raw ?? "").trim();
  if (!trimmed) return "";
  if (trimmed.startsWith("/") && !trimmed.startsWith("//") && !trimmed.includes("\\")) {
    return trimmed;
  }
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
      return "";
    }
    return parsed.toString();
  } catch {
    return "";
  }
}

export function parseQrUrlMeta(qrUrl: string | undefined, fallbackRef: string): ParsedBankMeta {
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

type CopyKey = "amount" | "account" | "reference" | "qr";

/**
 * Displays the real VietQR image with one-click copy actions for transfer metadata.
 * Falls back to an explicit error + reload state when the image is unavailable
 * (never renders hardcoded fake bank accounts or unscannable noise QR).
 */
export function VietQrPreview({ amount, reference, qrUrl, qrCode, expiresAt }: VietQrPreviewProps) {
  const [imgFailed, setImgFailed] = useState(false);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [copiedKey, setCopiedKey] = useState<CopyKey | null>(null);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    setImgFailed(false);
  }, [qrUrl]);

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    };
  }, []);

  const safeQrUrl = useMemo(() => parseSafeQrImageUrl(qrUrl), [qrUrl]);
  const bankMeta = useMemo(() => parseQrUrlMeta(safeQrUrl, reference), [safeQrUrl, reference]);

  const effectiveQrUrl = useMemo(() => {
    if (!safeQrUrl) return "";
    if (reloadNonce === 0) return safeQrUrl;
    try {
      const u = new URL(safeQrUrl, "https://nexusmart.local");
      u.searchParams.set("_r", String(reloadNonce));
      return safeQrUrl.startsWith("/") ? `${u.pathname}${u.search}` : u.toString();
    } catch {
      return safeQrUrl;
    }
  }, [safeQrUrl, reloadNonce]);

  const handleCopy = async (key: CopyKey, rawValue: string) => {
    try {
      await navigator.clipboard.writeText(rawValue);
      setCopiedKey(key);
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => setCopiedKey(null), 2200);
    } catch {
      setCopiedKey(key);
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => setCopiedKey(null), 2200);
    }
  };

  const showRemoteQr = Boolean(effectiveQrUrl) && !imgFailed;

  return (
    <div className={styles.qrStage}>
      <div className={styles.qrFrame} data-reveal="scale">
        {showRemoteQr ? (
          <img
            src={effectiveQrUrl}
            alt={`Mã VietQR thanh toán ${formatVnd(amount)}`}
            width={232}
            height={232}
            referrerPolicy="no-referrer"
            className={styles.qrImage}
            onError={() => setImgFailed(true)}
          />
        ) : (
          <div role="status" className={styles.qrFallbackBox}>
            <span className={styles.qrFallbackText}>
              {safeQrUrl
                ? "Không tải được ảnh mã VietQR."
                : "Chưa có ảnh mã VietQR từ cổng thanh toán."}
            </span>
            {safeQrUrl && (
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

      <div className={styles.qrDetails}>
        <div className={styles.qrRow}>
          <div className={styles.qrRowMain}>
            <span className={styles.qrLabel}>Số tiền chuyển khoản</span>
            <span className={`${styles.qrAmount} num`}>{formatVnd(amount)}</span>
          </div>
          <button
            type="button"
            className={`${styles.copyBtn} ${copiedKey === "amount" ? styles.copyBtnDone : ""}`}
            onClick={() => void handleCopy("amount", String(amount))}
          >
            {copiedKey === "amount" ? <IconCheck size={14} /> : <IconCopy size={14} />}
            <span>{copiedKey === "amount" ? "Đã chép" : "Chép số tiền"}</span>
          </button>
        </div>

        <div className={styles.qrRow}>
          <div className={styles.qrRowMain}>
            <span className={styles.qrLabel}>Nội dung bắt buộc</span>
            <span className={`${styles.qrVal} num`}>{bankMeta.transferNote}</span>
          </div>
          <button
            type="button"
            className={`${styles.copyBtn} ${copiedKey === "reference" ? styles.copyBtnDone : ""}`}
            onClick={() => void handleCopy("reference", bankMeta.transferNote)}
          >
            {copiedKey === "reference" ? <IconCheck size={14} /> : <IconCopy size={14} />}
            <span>{copiedKey === "reference" ? "Đã chép" : "Chép nội dung"}</span>
          </button>
        </div>

        {(bankMeta.bankName || bankMeta.accountNo || bankMeta.accountName) && (
          <div className={styles.qrRow}>
            <div className={styles.qrRowMain}>
              {bankMeta.bankName && <span className={styles.qrLabel}>{bankMeta.bankName}</span>}
              {bankMeta.accountNo && (
                <span className={`${styles.qrVal} num`}>{bankMeta.accountNo}</span>
              )}
              {bankMeta.accountName && (
                <span className={styles.qrLabel}>{bankMeta.accountName}</span>
              )}
            </div>
            {bankMeta.accountNo && (
              <button
                type="button"
                className={`${styles.copyBtn} ${copiedKey === "account" ? styles.copyBtnDone : ""}`}
                onClick={() => void handleCopy("account", bankMeta.accountNo!.replace(/\s+/g, ""))}
              >
                {copiedKey === "account" ? <IconCheck size={14} /> : <IconCopy size={14} />}
                <span>{copiedKey === "account" ? "Đã chép" : "Chép STK"}</span>
              </button>
            )}
          </div>
        )}

        {qrCode && (
          <div className={styles.qrRow}>
            <div className={styles.qrRowMain}>
              <span className={styles.qrLabel}>Chuỗi dữ liệu VietQR</span>
              <span className={`${styles.qrVal} num`}>
                {qrCode.length > 36 ? `${qrCode.slice(0, 36)}…` : qrCode}
              </span>
            </div>
            <button
              type="button"
              className={`${styles.copyBtn} ${copiedKey === "qr" ? styles.copyBtnDone : ""}`}
              onClick={() => void handleCopy("qr", qrCode)}
            >
              {copiedKey === "qr" ? <IconCheck size={14} /> : <IconCopy size={14} />}
              <span>{copiedKey === "qr" ? "Đã chép" : "Chép chuỗi QR"}</span>
            </button>
          </div>
        )}

        {expiresAt && (
          <div className={styles.qrRow}>
            <div className={styles.qrRowMain}>
              <span className={styles.qrLabel}>Hạn chuyển khoản</span>
              <span className={`${styles.qrVal} num`}>{formatDateTimeVi(expiresAt)}</span>
            </div>
          </div>
        )}

        <p className={styles.qrNote}>
          Mở ứng dụng ngân hàng để quét mã và chuyển đúng số tiền cùng nội dung chuyển khoản. Đơn
          hàng sẽ được xác nhận tự động, không cần nhập số thẻ.
        </p>
      </div>
    </div>
  );
}
