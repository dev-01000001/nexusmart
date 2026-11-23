"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { GatewayPayment, GatewayPaymentResponse } from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage, refreshSessionOnce } from "@/lib/api";
import { formatVnd } from "@/lib/format";
import { Button } from "@/components/Button";
import { StateView } from "@/components/StateView";
import { VietQrPreview } from "./VietQrPreview";
import { formatCountdown, getPaymentStatusView } from "@/features/checkout/messages";
import {
  type StableReferenceCache,
  canRetryPayment,
  getSseReconnectDelayMs,
  isPaymentDeadlinePassed,
  resolveStableReferenceKey,
} from "@/features/checkout/helpers";
import styles from "./commerce.module.css";

export interface RetryPaymentBoxProps {
  paymentId: string;
  onPaid?: () => void;
  /** Optional server-fetched payment snapshot used by the dedicated waiting page. */
  initialPayment?: GatewayPayment;
  /** Optional callback invoked when retry generates a replacement payment ID. */
  onPaymentReplaced?: (nextPayment: GatewayPayment) => void;
  /** Hide shortcut link to `/thanh-toan/cho-thanh-toan/[paymentId]` when already there. */
  hideWaitingPageLink?: boolean;
}

const POLL_INTERVAL_MS = 3500;
const SSE_FALLBACK_POLL_MS = 2500;

function statusToneClass(tone: "pos" | "warn" | "neg" | "neutral"): string {
  if (tone === "pos") return styles.tonePos;
  if (tone === "warn") return styles.toneWarn;
  if (tone === "neg") return styles.toneNeg;
  return styles.toneNeutral;
}

export function RetryPaymentBox({
  paymentId,
  onPaid,
  initialPayment,
  onPaymentReplaced,
  hideWaitingPageLink = false,
}: RetryPaymentBoxProps) {
  const [activePaymentId, setActivePaymentId] = useState<string>(
    initialPayment?.payment_id || paymentId
  );
  const [payment, setPayment] = useState<GatewayPayment | null>(initialPayment ?? null);
  const [loading, setLoading] = useState<boolean>(!initialPayment);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [retrying, setRetrying] = useState<boolean>(false);
  const [retryFeedback, setRetryFeedback] = useState<{
    tone: "error" | "info" | "success";
    message: string;
  } | null>(null);
  const [checkingStatus, setCheckingStatus] = useState<boolean>(false);
  const [sseDisconnected, setSseDisconnected] = useState<boolean>(false);
  const [nowMs, setNowMs] = useState<number | null>(null);

  const paidNotifiedRef = useRef<boolean>(
    (initialPayment?.status ?? "").toUpperCase() === "SUCCEEDED"
  );
  const retryIdempotencyCacheRef = useRef<StableReferenceCache | null>(null);
  const onPaidRef = useRef(onPaid);
  onPaidRef.current = onPaid;

  useEffect(() => {
    setActivePaymentId(initialPayment?.payment_id || paymentId);
    if (initialPayment) {
      setPayment(initialPayment);
      setLoading(false);
      setLoadError(null);
      paidNotifiedRef.current = initialPayment.status.toUpperCase() === "SUCCEEDED";
    }
  }, [paymentId, initialPayment]);

  const applyPaymentUpdate = useCallback((next: GatewayPayment) => {
    setPayment(next);
    setLoadError(null);
    if (next.status.toUpperCase() === "SUCCEEDED" && !paidNotifiedRef.current) {
      paidNotifiedRef.current = true;
      onPaidRef.current?.();
    }
  }, []);

  const fetchPayment = useCallback(
    async (targetId: string, silent = false) => {
      if (!targetId) return;
      if (!silent) {
        setLoading(true);
        setLoadError(null);
      }
      try {
        const res = await api<GatewayPaymentResponse>(`payments/${encodeURIComponent(targetId)}`);
        if (res?.payment) {
          applyPaymentUpdate(res.payment);
        } else if (!silent) {
          setLoadError("Không tìm thấy thông tin giao dịch thanh toán.");
        }
      } catch (err) {
        if (!silent) {
          setLoadError(getErrorMessage(err, "Không thể tải thông tin thanh toán lúc này."));
        }
      } finally {
        if (!silent) {
          setLoading(false);
        }
      }
    },
    [applyPaymentUpdate]
  );

  // Initial load when no SSR snapshot is provided
  useEffect(() => {
    if (initialPayment && initialPayment.payment_id === activePaymentId) {
      return;
    }
    void fetchPayment(activePaymentId, false);
  }, [activePaymentId, fetchPayment, initialPayment]);

  const normalizedStatus = (payment?.status ?? "").toUpperCase();
  const isPending = normalizedStatus === "PENDING";
  const tracksExpiry = isPending || normalizedStatus === "FAILED";

  // Countdown tick while PENDING or FAILED (so expired FAILED payments cannot be retried)
  useEffect(() => {
    if (!tracksExpiry || !payment?.expires_at) return;
    setNowMs(Date.now());
    const timer = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, [tracksExpiry, payment?.expires_at]);

  // Live SSE subscription while PENDING (isolated from polling interval & sseDisconnected state).
  // On error: close EventSource, refresh session at most once per disconnect, and reconnect with exponential backoff (5s..60s).
  useEffect(() => {
    if (!isPending || !activePaymentId) return;

    let closed = false;
    let source: EventSource | null = null;
    let reconnectTimer: number | null = null;
    let retryAttempt = 0;

    const handleSsePayload = (rawData: string) => {
      if (closed) return;
      try {
        const parsed = JSON.parse(rawData) as { status?: string };
        const evtStatus = (parsed.status ?? "").toUpperCase();
        if (evtStatus && evtStatus !== "PENDING" && evtStatus !== "UNKNOWN") {
          void fetchPayment(activePaymentId, true);
        }
      } catch {
        // Ignore keep-alive or malformed event chunks
      }
    };

    const eventNames = [
      "payment.succeeded",
      "payment.failed",
      "payment.expired",
      "payment.refunded",
      "payment.pending",
      "payment.status",
    ];
    const onNamedEvent = (ev: Event) => {
      const msg = ev as MessageEvent<string>;
      if (typeof msg.data === "string") {
        handleSsePayload(msg.data);
      }
    };

    const connectSse = () => {
      if (closed) return;
      if (typeof window === "undefined" || typeof EventSource === "undefined") {
        setSseDisconnected(true);
        return;
      }

      let refreshedForThisDisconnect = false;
      try {
        const nextSource = new EventSource(
          `/api/gw/payments/${encodeURIComponent(activePaymentId)}/events`,
          { withCredentials: true }
        );
        source = nextSource;

        nextSource.onopen = () => {
          if (closed) return;
          retryAttempt = 0;
          setSseDisconnected(false);
        };
        for (const name of eventNames) {
          nextSource.addEventListener(name, onNamedEvent);
        }
        nextSource.onmessage = (ev) => {
          if (typeof ev.data === "string") {
            handleSsePayload(ev.data);
          }
        };
        nextSource.onerror = () => {
          if (closed) return;
          nextSource.close();
          if (source === nextSource) {
            source = null;
          }
          setSseDisconnected(true);
          if (!refreshedForThisDisconnect) {
            refreshedForThisDisconnect = true;
            void refreshSessionOnce();
          }
          const delayMs = getSseReconnectDelayMs(retryAttempt);
          retryAttempt += 1;
          reconnectTimer = window.setTimeout(() => {
            reconnectTimer = null;
            connectSse();
          }, delayMs);
        };
      } catch {
        setSseDisconnected(true);
        const delayMs = getSseReconnectDelayMs(retryAttempt);
        retryAttempt += 1;
        reconnectTimer = window.setTimeout(() => {
          reconnectTimer = null;
          connectSse();
        }, delayMs);
      }
    };

    connectSse();

    return () => {
      closed = true;
      if (reconnectTimer !== null) {
        window.clearTimeout(reconnectTimer);
      }
      if (source) {
        source.close();
      }
    };
  }, [activePaymentId, fetchPayment, isPending]);

  // Separate polling fallback effect while PENDING (speeds up automatically when SSE is disconnected)
  useEffect(() => {
    if (!isPending || !activePaymentId) return;
    let closed = false;
    const pollInterval = window.setInterval(
      () => {
        if (!closed) {
          void fetchPayment(activePaymentId, true);
        }
      },
      sseDisconnected ? SSE_FALLBACK_POLL_MS : POLL_INTERVAL_MS
    );
    return () => {
      closed = true;
      window.clearInterval(pollInterval);
    };
  }, [activePaymentId, fetchPayment, isPending, sseDisconnected]);

  const remainingSeconds = useMemo(() => {
    if (!payment?.expires_at || nowMs === null) return null;
    const expiresMs = Date.parse(payment.expires_at);
    if (Number.isNaN(expiresMs)) return null;
    return Math.max(0, Math.ceil((expiresMs - nowMs) / 1000));
  }, [payment?.expires_at, nowMs]);

  const countdownExpired =
    tracksExpiry && nowMs !== null && isPaymentDeadlinePassed(payment?.expires_at, nowMs);

  // Trigger a status refresh right when countdown reaches zero
  useEffect(() => {
    if (!countdownExpired || !activePaymentId) return;
    void fetchPayment(activePaymentId, true);
  }, [countdownExpired, activePaymentId, fetchPayment]);

  const handleManualCheck = async () => {
    setCheckingStatus(true);
    setRetryFeedback(null);
    await fetchPayment(activePaymentId, true);
    setCheckingStatus(false);
  };

  const handleRetryVietQr = async () => {
    if (retrying || !activePaymentId) return;
    if (isPaymentDeadlinePassed(payment?.expires_at)) {
      setNowMs(Date.now());
      return;
    }
    setRetrying(true);
    setRetryFeedback(null);
    try {
      const { key: idempotencyKey, cache } = resolveStableReferenceKey({
        prefix: "nm-pay-retry",
        fingerprint: activePaymentId,
        cache: retryIdempotencyCacheRef.current,
      });
      retryIdempotencyCacheRef.current = cache;
      const res = await api<GatewayPaymentResponse>(
        `payments/${encodeURIComponent(activePaymentId)}/retry`,
        {
          method: "POST",
          headers: {
            "Idempotency-Key": idempotencyKey,
          },
          json: { method: "VIETQR" },
        }
      );
      if (res?.payment) {
        retryIdempotencyCacheRef.current = null;
        paidNotifiedRef.current = res.payment.status.toUpperCase() === "SUCCEEDED";
        setActivePaymentId(res.payment.payment_id);
        setPayment(res.payment);
        setRetryFeedback({
          tone: "success",
          message: "Đã tạo mã VietQR mới. Vui lòng quét mã trong thời hạn đếm ngược trên màn hình.",
        });
        onPaymentReplaced?.(res.payment);
      }
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.code === "FAILED_PRECONDITION")) {
        setRetryFeedback({
          tone: "error",
          message:
            "Giao dịch đã hết hạn hoặc đơn hàng đã được hủy tự động. Vui lòng kiểm tra đơn hàng hoặc đặt lại từ giỏ hàng.",
        });
        await fetchPayment(activePaymentId, true);
      } else {
        setRetryFeedback({
          tone: "error",
          message: getErrorMessage(err, "Chưa thể tạo lại mã VietQR lúc này. Vui lòng thử lại."),
        });
      }
    } finally {
      setRetrying(false);
    }
  };

  if (loading) {
    return <StateView kind="loading" title="Đang tải thông tin giao dịch thanh toán..." rows={3} />;
  }

  if (loadError || !payment) {
    return (
      <StateView
        kind="error"
        title="Không thể tải thông tin thanh toán"
        message={loadError ?? "Giao dịch không tồn tại hoặc không thuộc tài khoản của bạn."}
        actionLabel="Thử tải lại"
        onAction={() => void fetchPayment(activePaymentId, false)}
      />
    );
  }

  const statusView = getPaymentStatusView(payment.status);
  const transferRef = payment.provider_ref || payment.order_group_id || payment.payment_id;
  const allocatedOrderIds = Array.from(
    new Set(
      (payment.shop_allocations?.map((a) => a.order_id) ?? []).filter((id): id is string =>
        Boolean(id)
      )
    )
  );
  const orderIds =
    allocatedOrderIds.length > 0 ? allocatedOrderIds : payment.order_id ? [payment.order_id] : [];
  const primaryOrderHref =
    orderIds.length === 1 ? `/don-hang/${encodeURIComponent(orderIds[0])}` : "/don-hang";
  const canRetry = canRetryPayment(normalizedStatus, countdownExpired);
  const isExpiredState = normalizedStatus === "EXPIRED" || countdownExpired;

  return (
    <div
      className={styles.retryBox}
      role="region"
      aria-label={`Thông tin thanh toán VietQR ${transferRef}`}
    >
      <div className={styles.retryHead}>
        <div className={styles.paymentTitleGroup}>
          <p className={styles.retryTitle}>
            Chuyển khoản VietQR · <span className="num">{formatVnd(payment.amount_vnd)}</span>
          </p>
          <span className={`${styles.statusTag} ${statusToneClass(statusView.tone)}`}>
            {statusView.label}
          </span>
        </div>

        {tracksExpiry && remainingSeconds !== null && (
          <span
            className={`${styles.countdownPill} ${
              countdownExpired ? styles.toneNeg : styles.toneNeutral
            } num`}
          >
            {countdownExpired
              ? "Đã quá hạn thanh toán"
              : `Còn lại ${formatCountdown(remainingSeconds)}`}
          </span>
        )}
      </div>

      <p className={styles.retryText}>{statusView.description}</p>

      {isPending && (
        <>
          <VietQrPreview
            amount={payment.amount_vnd}
            reference={transferRef}
            qrUrl={payment.qr_url}
            qrCode={payment.qr_code}
            expiresAt={payment.expires_at}
          />
          <p className={styles.paymentLiveStatus} role="status" aria-live="polite">
            {countdownExpired
              ? "Hạn giữ đơn đã qua và đơn hàng sẽ tự động hủy. Vui lòng kiểm tra trạng thái đơn hoặc đặt lại đơn mới từ giỏ hàng."
              : "Đang tự động theo dõi trạng thái chuyển khoản từ ngân hàng..."}
          </p>
        </>
      )}

      {retryFeedback && (
        <p
          className={`${styles.feedbackBox} ${
            retryFeedback.tone === "error"
              ? styles.feedbackError
              : retryFeedback.tone === "success"
                ? styles.feedbackSuccess
                : styles.feedbackInfo
          }`}
          role={retryFeedback.tone === "error" ? "alert" : "status"}
        >
          {retryFeedback.message}
        </p>
      )}

      <div className={styles.formActions}>
        {normalizedStatus === "SUCCEEDED" ? (
          <>
            <Button variant="primary" size="md" href={primaryOrderHref}>
              {orderIds.length > 1 ? "Xem các đơn hàng của bạn" : "Xem chi tiết đơn hàng"}
            </Button>
            <Button variant="secondary" size="md" href="/danh-muc">
              Tiếp tục mua sắm
            </Button>
          </>
        ) : (
          <>
            {canRetry && (
              <Button
                variant={isPending ? "secondary" : "primary"}
                size="md"
                className={styles.wrapBtn}
                disabled={retrying}
                onClick={() => void handleRetryVietQr()}
              >
                {retrying
                  ? "Đang tạo mã VietQR mới..."
                  : isPending
                    ? "Tạo mã VietQR mới"
                    : "Thanh toán lại qua VietQR"}
              </Button>
            )}
            {isPending && (
              <Button
                variant="secondary"
                size="md"
                disabled={checkingStatus}
                onClick={() => void handleManualCheck()}
              >
                {checkingStatus ? "Đang kiểm tra..." : "Kiểm tra trạng thái"}
              </Button>
            )}
            {!hideWaitingPageLink && isPending && (
              <Button
                variant="ink"
                size="md"
                href={`/thanh-toan/cho-thanh-toan/${encodeURIComponent(activePaymentId)}`}
              >
                Mở trang chờ thanh toán
              </Button>
            )}
            {isExpiredState && (
              <Button variant="primary" size="md" href="/gio-hang">
                Đặt lại từ giỏ hàng
              </Button>
            )}
            {(isExpiredState || normalizedStatus === "CANCELLED") && (
              <Button variant="secondary" size="md" href="/don-hang">
                Xem danh sách đơn hàng
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  );
}
