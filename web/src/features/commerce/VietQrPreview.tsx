"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { Check, Copy, QrCode, RefreshCw } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";

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

  const copyBtnClass = (done: boolean) =>
    cn(
      "min-h-10 shrink-0 shadow-none sm:min-h-8",
      done && "border-badge-success-ink/30 bg-badge-success-bg text-badge-success-ink"
    );

  return (
    <div className="grid grid-cols-1 gap-5 md:grid-cols-[248px_minmax(0,1fr)] md:items-start">
      <div
        className={cn(
          "mx-auto flex w-full max-w-[248px] items-center justify-center rounded-xl border border-border p-3",
          showRemoteQr ? "bg-white" : "bg-card"
        )}
      >
        {showRemoteQr ? (
          <img
            src={effectiveQrUrl}
            alt={`Mã VietQR thanh toán ${formatVnd(amount)}`}
            width={232}
            height={232}
            referrerPolicy="no-referrer"
            className="aspect-square w-full max-w-[224px] rounded-md object-contain"
            onError={() => setImgFailed(true)}
          />
        ) : (
          <div
            role="status"
            className="flex aspect-square w-full flex-col items-center justify-center gap-3 rounded-lg bg-muted p-4 text-center"
          >
            <QrCode className="size-8 text-muted-foreground" aria-hidden="true" />
            <span className="text-xs leading-relaxed text-muted-foreground">
              {safeQrUrl
                ? "Không tải được ảnh mã VietQR."
                : "Chưa có ảnh mã VietQR từ cổng thanh toán."}
            </span>
            {safeQrUrl && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="min-h-9 shadow-none"
                onClick={() => {
                  setImgFailed(false);
                  setReloadNonce((n) => n + 1);
                }}
              >
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>Tải lại ảnh QR</span>
              </Button>
            )}
          </div>
        )}
      </div>

      <div className="min-w-0 space-y-2.5">
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
          <div className="min-w-0 space-y-0.5">
            <span className="block text-xs text-muted-foreground">Số tiền chuyển khoản</span>
            <span className="block text-lg font-semibold text-foreground tabular-nums">
              {formatVnd(amount)}
            </span>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={copyBtnClass(copiedKey === "amount")}
            onClick={() => void handleCopy("amount", String(amount))}
          >
            {copiedKey === "amount" ? (
              <Check className="size-3.5" aria-hidden="true" />
            ) : (
              <Copy className="size-3.5" aria-hidden="true" />
            )}
            <span>{copiedKey === "amount" ? "Đã chép" : "Chép số tiền"}</span>
          </Button>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
          <div className="min-w-0 space-y-0.5">
            <span className="block text-xs text-muted-foreground">Nội dung bắt buộc</span>
            <span className="block break-all text-sm font-semibold text-foreground tabular-nums">
              {bankMeta.transferNote}
            </span>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={copyBtnClass(copiedKey === "reference")}
            onClick={() => void handleCopy("reference", bankMeta.transferNote)}
          >
            {copiedKey === "reference" ? (
              <Check className="size-3.5" aria-hidden="true" />
            ) : (
              <Copy className="size-3.5" aria-hidden="true" />
            )}
            <span>{copiedKey === "reference" ? "Đã chép" : "Chép nội dung"}</span>
          </Button>
        </div>

        {(bankMeta.bankName || bankMeta.accountNo || bankMeta.accountName) && (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
            <div className="min-w-0 space-y-0.5">
              {bankMeta.bankName && (
                <span className="block text-xs text-muted-foreground">{bankMeta.bankName}</span>
              )}
              {bankMeta.accountNo && (
                <span className="block text-sm font-semibold text-foreground tabular-nums">
                  {bankMeta.accountNo}
                </span>
              )}
              {bankMeta.accountName && (
                <span className="block text-xs text-muted-foreground">{bankMeta.accountName}</span>
              )}
            </div>
            {bankMeta.accountNo && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className={copyBtnClass(copiedKey === "account")}
                onClick={() => void handleCopy("account", bankMeta.accountNo!.replace(/\s+/g, ""))}
              >
                {copiedKey === "account" ? (
                  <Check className="size-3.5" aria-hidden="true" />
                ) : (
                  <Copy className="size-3.5" aria-hidden="true" />
                )}
                <span>{copiedKey === "account" ? "Đã chép" : "Chép STK"}</span>
              </Button>
            )}
          </div>
        )}

        {qrCode && (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
            <div className="min-w-0 space-y-0.5">
              <span className="block text-xs text-muted-foreground">Chuỗi dữ liệu VietQR</span>
              <span className="block break-all text-xs font-medium text-foreground tabular-nums">
                {qrCode.length > 36 ? `${qrCode.slice(0, 36)}…` : qrCode}
              </span>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={copyBtnClass(copiedKey === "qr")}
              onClick={() => void handleCopy("qr", qrCode)}
            >
              {copiedKey === "qr" ? (
                <Check className="size-3.5" aria-hidden="true" />
              ) : (
                <Copy className="size-3.5" aria-hidden="true" />
              )}
              <span>{copiedKey === "qr" ? "Đã chép" : "Chép chuỗi QR"}</span>
            </Button>
          </div>
        )}

        {expiresAt && (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3.5 py-2.5">
            <div className="min-w-0 space-y-0.5">
              <span className="block text-xs text-muted-foreground">Hạn chuyển khoản</span>
              <span className="block text-sm font-medium text-foreground tabular-nums">
                {formatDateTimeVi(expiresAt)}
              </span>
            </div>
          </div>
        )}

        <Alert className="border-border bg-muted/30 shadow-none">
          <AlertDescription className="text-xs leading-relaxed">
            Mở ứng dụng ngân hàng để quét mã và chuyển đúng số tiền cùng nội dung chuyển khoản. Đơn
            hàng sẽ được xác nhận tự động, không cần nhập số thẻ.
          </AlertDescription>
        </Alert>
      </div>
    </div>
  );
}
