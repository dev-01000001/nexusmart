"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ApiError, api } from "@/lib/api";
import { formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { VietQrPreview } from "@/features/commerce/VietQrPreview";
import type { GwPayment } from "@/features/commerce/types";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

const POLL_INTERVAL_MS = 2500;
const DEFAULT_TTL_MS = 15 * 60 * 1000;
const RING_RADIUS = 36;
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS;

export interface PaymentWaitingClientProps {
  brandName: string;
  paymentId: string;
  initialPayment: GwPayment | null;
  fallbackOrderId: string;
  initialError: string | null;
}

function formatRemaining(ms: number): string {
  if (ms <= 0) return "00:00";
  const totalSecs = Math.floor(ms / 1000);
  const mins = Math.floor(totalSecs / 60);
  const secs = totalSecs % 60;
  return `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
}

export function PaymentWaitingClient({
  brandName,
  paymentId,
  initialPayment,
  fallbackOrderId,
  initialError,
}: PaymentWaitingClientProps) {
  const router = useRouter();
  const { refreshCart, showToast } = useStore();

  const [payment, setPayment] = useState<GwPayment | null>(initialPayment);
  const [error, setError] = useState<string | null>(initialError);
  const [pollWarning, setPollWarning] = useState<string | null>(null);
  const [checking, setChecking] = useState<boolean>(false);
  const [retrying, setRetrying] = useState<boolean>(false);
  const [sessionExpired, setSessionExpired] = useState<boolean>(false);
  const [nowMs, setNowMs] = useState<number | null>(null);

  const mountedRef = useRef(true);
  const inFlightRef = useRef(false);
  const hasPaymentRef = useRef(Boolean(initialPayment));
  hasPaymentRef.current = Boolean(payment);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const status = (payment?.status ?? "").toUpperCase();
  const isPending = status === "PENDING";
  const isPaid = status === "SUCCEEDED";
  const isTerminalFailed = status === "EXPIRED" || status === "FAILED" || status === "CANCELLED";

  const primaryOrderId = useMemo(() => {
    if (payment?.shop_allocations?.length && payment.shop_allocations[0]?.order_id) {
      return payment.shop_allocations[0].order_id;
    }
    if (payment?.order_id) return payment.order_id;
    return fallbackOrderId;
  }, [payment, fallbackOrderId]);

  const orderDetailHref = primaryOrderId
    ? `/don-hang/${encodeURIComponent(primaryOrderId)}`
    : "/don-hang";

  const fetchStatus = useCallback(
    async (manual = false) => {
      if (!manual && inFlightRef.current) return;
      inFlightRef.current = true;
      if (manual) setChecking(true);
      try {
        const res = await api<{ payment: GwPayment }>(
          `/api/payments/${encodeURIComponent(paymentId)}`
        );
        if (!mountedRef.current) return;
        setPayment(res.payment);
        setError(null);
        setPollWarning(null);
        if (res.payment.status.toUpperCase() === "SUCCEEDED") {
          void refreshCart();
        }
      } catch (err) {
        if (!mountedRef.current) return;
        if (err instanceof ApiError) {
          if (err.status === 401) {
            setSessionExpired(true);
            setPollWarning("Phiên đăng nhập đã hết hạn.");
            return;
          }
          if (!hasPaymentRef.current) {
            setError(err.message);
          } else {
            setPollWarning(err.message);
          }
        } else if (!hasPaymentRef.current) {
          setError("Chưa thể tải thông tin thanh toán lúc này.");
        }
      } finally {
        inFlightRef.current = false;
        if (manual && mountedRef.current) setChecking(false);
      }
    },
    [paymentId, refreshCart]
  );

  useEffect(() => {
    if (!isPending || sessionExpired) return;
    const timer = window.setInterval(() => {
      void fetchStatus(false);
    }, POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [isPending, sessionExpired, fetchStatus]);

  useEffect(() => {
    if (!isPending || !payment?.expires_at) return;
    setNowMs(Date.now());
    const timer = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, [isPending, payment?.expires_at]);

  useEffect(() => {
    if (!isPaid) return;
    showToast("Đã nhận thanh toán VietQR.");
    const timer = window.setTimeout(() => {
      router.push(orderDetailHref);
    }, 1800);
    return () => window.clearTimeout(timer);
  }, [isPaid, orderDetailHref, router, showToast]);

  const handleRetryPayment = async () => {
    if (!payment || retrying) return;
    setRetrying(true);
    setPollWarning(null);
    try {
      const res = await api<{ payment: GwPayment }>(
        `/api/payments/${encodeURIComponent(payment.payment_id)}/retry`,
        {
          method: "POST",
          body: { method: "VIETQR" },
        }
      );
      showToast("Đã tạo mã VietQR mới.");
      const nextId = res.payment.payment_id;
      const q = primaryOrderId ? `?order=${encodeURIComponent(primaryOrderId)}` : "";
      router.push(`/thanh-toan/cho/${encodeURIComponent(nextId)}${q}`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setSessionExpired(true);
        setPollWarning("Phiên đăng nhập đã hết hạn.");
      } else {
        setPollWarning(
          err instanceof ApiError ? err.message : "Chưa tạo được mã VietQR mới lúc này."
        );
      }
    } finally {
      setRetrying(false);
    }
  };

  if (error && !payment) {
    return (
      <div className={shared.page}>
        <div className={styles.stageWrap}>
          <header className={styles.stageHead}>
            <h1 className={shared.pageTitle}>Thanh toán VietQR</h1>
          </header>
          <StateView
            kind="error"
            title="Chưa thể hiển thị mã VietQR."
            message={error}
            actionLabel="Thử lại"
            onAction={() => void fetchStatus(true)}
          />
          <div className={styles.actionsCenter}>
            <Button variant="secondary" size="md" href="/don-hang">
              Đơn hàng của tôi
            </Button>
          </div>
        </div>
      </div>
    );
  }

  if (!payment) {
    return (
      <div className={shared.page}>
        <div className={styles.stageWrap}>
          <header className={styles.stageHead}>
            <h1 className={shared.pageTitle}>Thanh toán VietQR</h1>
          </header>
          <StateView
            kind="loading"
            skeletonVariant="detail"
            title="Đang tạo mã VietQR..."
            rows={3}
          />
        </div>
      </div>
    );
  }

  const expiresMs = payment.expires_at ? Date.parse(payment.expires_at) : NaN;
  const createdMs = payment.created_at ? Date.parse(payment.created_at) : NaN;
  const totalWindowMs =
    Number.isFinite(expiresMs) && Number.isFinite(createdMs) && expiresMs > createdMs
      ? expiresMs - createdMs
      : DEFAULT_TTL_MS;
  const remainingMs =
    nowMs !== null && Number.isFinite(expiresMs) ? Math.max(0, expiresMs - nowMs) : null;
  const progressRatio =
    remainingMs !== null ? Math.max(0, Math.min(1, remainingMs / totalWindowMs)) : 1;
  const dashOffset = RING_CIRCUMFERENCE * (1 - progressRatio);
  const ringToneClass =
    remainingMs !== null && remainingMs < 60_000
      ? styles.ringUrgent
      : remainingMs !== null && remainingMs < 180_000
        ? styles.ringWarn
        : "";

  const transferRef =
    payment.provider_ref ||
    (payment.order_group_id ? `NEXUSMART ${payment.order_group_id}` : payment.payment_id);

  const badgeTone =
    isPaid || status === "REFUNDED"
      ? shared.tonePos
      : isTerminalFailed
        ? shared.toneNeg
        : isPending
          ? shared.toneWarn
          : shared.toneNeutral;
  const statusLabel = isPaid
    ? "Đã thanh toán"
    : status === "EXPIRED"
      ? "Đã hết hạn"
      : status === "FAILED"
        ? "Chưa thành công"
        : status === "CANCELLED"
          ? "Đã hủy"
          : status === "REFUNDED"
            ? "Đã hoàn tiền"
            : isPending
              ? "Đang chờ chuyển khoản"
              : "Đã cập nhật";

  return (
    <div className={shared.page}>
      <div className={styles.stageWrap}>
        <header className={styles.stageHead}>
          <h1 className={`${shared.pageTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Thanh toán VietQR
              </span>
            </span>
          </h1>
          <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {isPending ? `Quét mã bằng ứng dụng ngân hàng tại ${brandName}` : brandName}
          </p>
        </header>

        <section className={styles.sheet}>
          {pollWarning && (
            <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="alert">
              <p>{pollWarning}</p>
              {sessionExpired && (
                <div style={{ marginTop: 10 }}>
                  <Button
                    variant="secondary"
                    size="sm"
                    href={`/dang-nhap?next=${encodeURIComponent(`/thanh-toan/cho/${paymentId}`)}`}
                  >
                    Đăng nhập lại
                  </Button>
                </div>
              )}
            </div>
          )}

          {isPaid ? (
            <div className={styles.successStage} role="status">
              <div className={styles.successCircle} aria-hidden="true">
                <svg width="36" height="36" viewBox="0 0 36 36" fill="none">
                  <path
                    d="M8 18.5L15 25.5L28 11.5"
                    stroke="currentColor"
                    strokeWidth="3"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    className={styles.successCheckPath}
                  />
                </svg>
              </div>
              <h2 className={styles.successTitle}>Thanh toán thành công</h2>
              <p className={`${styles.successAmount} num`}>{formatVnd(payment.amount_vnd)}</p>
              <p className={styles.successSub}>Đang chuyển tới trang chi tiết đơn hàng.</p>
              <div className={styles.redirectTrack} aria-hidden="true">
                <span className={styles.redirectFill} />
              </div>
              <Button variant="primary" size="md" href={orderDetailHref}>
                Xem chi tiết đơn hàng
              </Button>
            </div>
          ) : isTerminalFailed ? (
            <>
              <div className={styles.failedStage} role="alert">
                <div>
                  <span className={`${shared.statusTag} ${badgeTone}`}>{statusLabel}</span>
                </div>
                <h2 className={styles.failedTitle}>
                  {status === "EXPIRED"
                    ? "Mã VietQR đã hết thời hạn"
                    : status === "CANCELLED"
                      ? "Giao dịch đã hủy"
                      : "Giao dịch chưa hoàn tất"}
                </h2>
                <p className={styles.failedSub}>
                  {status === "FAILED"
                    ? `Tạo mã VietQR mới để thanh toán lại cho đơn tại ${brandName}.`
                    : "Mở chi tiết đơn hàng để xem trạng thái hiện tại."}
                </p>
              </div>

              <div className={styles.actionsCenter}>
                {status === "FAILED" && (
                  <Button
                    variant="primary"
                    size="md"
                    loading={retrying}
                    loadingLabel="Đang tạo mã mới"
                    onClick={handleRetryPayment}
                  >
                    Tạo mã VietQR mới
                  </Button>
                )}
                <Button
                  variant={status === "FAILED" ? "secondary" : "primary"}
                  size="md"
                  href={orderDetailHref}
                >
                  Chi tiết đơn hàng
                </Button>
              </div>
            </>
          ) : isPending ? (
            <>
              <div className={styles.timerRow}>
                <div className={styles.timerCopy} role="status">
                  <div>
                    <span className={`${shared.statusTag} ${badgeTone}`}>{statusLabel}</span>
                  </div>
                  <h2 className={styles.timerTitle}>Tự động xác nhận sau chuyển khoản</h2>
                  <p className={styles.timerSub}>Giữ trang mở khi quét mã.</p>
                </div>

                {remainingMs !== null && (
                  <div
                    className={styles.ringWrap}
                    role="timer"
                    aria-label={`Thời gian còn lại ${formatRemaining(remainingMs)}`}
                  >
                    <svg className={styles.ringSvg} viewBox="0 0 84 84" aria-hidden="true">
                      <circle cx="42" cy="42" r={RING_RADIUS} className={styles.ringTrack} />
                      <circle
                        cx="42"
                        cy="42"
                        r={RING_RADIUS}
                        strokeDasharray={RING_CIRCUMFERENCE}
                        strokeDashoffset={dashOffset}
                        className={`${styles.ringProgress} ${ringToneClass}`}
                      />
                    </svg>
                    <span className={styles.ringText}>{formatRemaining(remainingMs)}</span>
                  </div>
                )}
              </div>

              <VietQrPreview
                amount={payment.amount_vnd}
                reference={transferRef}
                qrCode={payment.qr_code}
                expiresAt={payment.expires_at}
              />

              <div className={styles.actionsCenter}>
                <Button
                  variant="ink"
                  size="md"
                  loading={checking}
                  loadingLabel="Đang kiểm tra"
                  onClick={() => void fetchStatus(true)}
                >
                  Tôi đã chuyển khoản
                </Button>
                <Button
                  variant="secondary"
                  size="md"
                  loading={retrying}
                  loadingLabel="Đang tạo mã"
                  onClick={handleRetryPayment}
                >
                  Tạo mã mới
                </Button>
                <Button variant="ghost" size="md" href={orderDetailHref}>
                  Xem đơn hàng
                </Button>
              </div>
            </>
          ) : (
            <div className={styles.statusSummary} role="status">
              <div>
                <span className={`${shared.statusTag} ${badgeTone}`}>{statusLabel}</span>
              </div>
              <h2 className={styles.timerTitle}>
                {status === "REFUNDED" ? "Giao dịch đã được hoàn tiền" : "Trạng thái thanh toán"}
              </h2>
              <p className={`${styles.statusAmount} num`}>{formatVnd(payment.amount_vnd)}</p>
              <div className={styles.actionsCenter}>
                <Button variant="primary" size="md" href={orderDetailHref}>
                  Xem chi tiết đơn hàng
                </Button>
              </div>
            </div>
          )}

          {payment.shop_allocations && payment.shop_allocations.length > 0 && (
            <div className={styles.allocSection}>
              <h3 className={styles.allocHeading}>Đơn hàng</h3>
              {payment.shop_allocations.map((alloc) => (
                <div key={alloc.order_id} className={styles.allocRow}>
                  <div className={shared.formActions}>
                    <span>{brandName}</span>
                    <Link
                      href={`/don-hang/${encodeURIComponent(alloc.order_id)}`}
                      className={styles.orderLink}
                    >
                      Mở đơn
                    </Link>
                  </div>
                  <span className="num">
                    {formatVnd(alloc.subtotal_vnd + alloc.shipping_fee_vnd)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
