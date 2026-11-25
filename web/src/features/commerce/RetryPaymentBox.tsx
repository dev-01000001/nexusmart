"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import type { Order } from "@/data/orders";
import type {
  GatewayOrder,
  GatewayPayment,
  GatewayPaymentMethod,
  GatewayPaymentMethodConfig,
  GatewayPaymentMethodsResponse,
  GatewayPaymentResponse,
  GatewayRetryPaymentResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage, refreshSessionOnce } from "@/lib/api";
import { GATEWAY_PAYMENT_METHOD_LABELS, formatVnd } from "@/lib/format";
import { validateSafePayUrl } from "@/lib/payment-url";
import { Button } from "@/components/Button";
import { StateView } from "@/components/StateView";
import { VietQrPreview } from "./VietQrPreview";
import { formatCountdown, getPaymentStatusView } from "@/features/checkout/messages";
import {
  type StableReferenceCache,
  canRetryPayment,
  getSseReconnectDelayMs,
  isPaymentDeadlinePassed,
  isTerminalPaymentStatus,
  resolveStableReferenceKey,
  shouldApplyPaymentFetchResult,
} from "@/features/checkout/helpers";
import type { GwPayment } from "./types";
import styles from "./commerce.module.css";

export interface RetryPaymentBoxProps {
  paymentId?: string;
  order?: Order & { paymentId?: string };
  onPaid?: () => void;
  /** Optional server-fetched payment snapshot used by the dedicated waiting page. */
  initialPayment?: GatewayPayment | GwPayment | null;
  /** Optional callback invoked when retry generates a replacement payment ID. */
  onPaymentReplaced?: (nextPayment: GatewayPayment) => void;
  /** Backwards-compatible callback alias when retry generates a replacement payment. */
  onCompleted?: (nextPayment?: GatewayPayment) => void;
  /** Optional callback invoked when retry switches the order group to COD. */
  onSwitchedToCod?: (orders: GatewayOrder[]) => void;
  /** Optional close callback when displayed inline. */
  onCancel?: () => void;
  /** Hide shortcut link to `/thanh-toan/cho/[paymentId]` when already on the waiting page. */
  hideWaitingPageLink?: boolean;
}

const POLL_INTERVAL_MS = 3500;
const SSE_FALLBACK_POLL_MS = 2500;
const METHOD_ORDER: readonly GatewayPaymentMethod[] = ["VIETQR", "ZALOPAY", "COD"];

function statusToneClass(tone: "pos" | "warn" | "neg" | "neutral"): string {
  if (tone === "pos") return styles.tonePos;
  if (tone === "warn") return styles.toneWarn;
  if (tone === "neg") return styles.toneNeg;
  return styles.toneNeutral;
}

function getMethodHeading(method: string): string {
  const key = method.trim().toUpperCase() as GatewayPaymentMethod;
  return GATEWAY_PAYMENT_METHOD_LABELS[key] ?? "Thanh toán trực tuyến";
}

function normalizeGatewayPayment(
  raw: GatewayPayment | GwPayment | null | undefined
): GatewayPayment | null {
  if (!raw?.payment_id) return null;
  return {
    payment_id: raw.payment_id,
    order_group_id: raw.order_group_id ?? "",
    order_id: raw.order_id ?? "",
    user_id: raw.user_id ?? "",
    amount_vnd: raw.amount_vnd ?? 0,
    method: raw.method ?? "VIETQR",
    status: raw.status ?? "PENDING",
    qr_url: raw.qr_url ?? "",
    qr_code: raw.qr_code ?? "",
    pay_url: raw.pay_url,
    provider: raw.provider ?? "",
    provider_ref: raw.provider_ref ?? "",
    shop_allocations: raw.shop_allocations ?? [],
    expires_at: raw.expires_at,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
  };
}

export function RetryPaymentBox({
  paymentId,
  order,
  onPaid,
  initialPayment,
  onPaymentReplaced,
  onCompleted,
  onSwitchedToCod,
  onCancel,
  hideWaitingPageLink = false,
}: RetryPaymentBoxProps) {
  const router = useRouter();
  const normalizedInitial = useMemo(
    () => normalizeGatewayPayment(initialPayment),
    [initialPayment]
  );
  const resolvedInitialId = (
    normalizedInitial?.payment_id ||
    paymentId ||
    order?.paymentId ||
    ""
  ).trim();

  const [activePaymentId, setActivePaymentId] = useState<string>(resolvedInitialId);
  const [payment, setPayment] = useState<GatewayPayment | null>(normalizedInitial);
  const [loading, setLoading] = useState<boolean>(!normalizedInitial && Boolean(resolvedInitialId));
  const [loadError, setLoadError] = useState<string | null>(null);
  const [retryingMethod, setRetryingMethod] = useState<GatewayPaymentMethod | null>(null);
  const [codConfirmOpen, setCodConfirmOpen] = useState<boolean>(false);
  const [retryFeedback, setRetryFeedback] = useState<{
    tone: "error" | "info" | "success";
    message: string;
    code?: string;
  } | null>(null);
  const [checkingStatus, setCheckingStatus] = useState<boolean>(false);
  const [sseDisconnected, setSseDisconnected] = useState<boolean>(false);
  const [nowMs, setNowMs] = useState<number | null>(null);
  const [enabledMethods, setEnabledMethods] = useState<GatewayPaymentMethodConfig[]>([]);

  const activePaymentIdRef = useRef<string>(resolvedInitialId);
  const fetchSeqRef = useRef<number>(0);
  const retryingRef = useRef<boolean>(false);
  const paidNotifiedRef = useRef<boolean>(
    (normalizedInitial?.status ?? "").toUpperCase() === "SUCCEEDED"
  );
  const retryIdempotencyCacheRef = useRef<StableReferenceCache | null>(null);
  const onPaidRef = useRef(onPaid);
  onPaidRef.current = onPaid;

  useEffect(() => {
    const nextId = (normalizedInitial?.payment_id || paymentId || order?.paymentId || "").trim();
    activePaymentIdRef.current = nextId;
    fetchSeqRef.current += 1;
    setActivePaymentId(nextId);
    if (normalizedInitial) {
      setPayment(normalizedInitial);
      setLoading(false);
      setLoadError(null);
      paidNotifiedRef.current = normalizedInitial.status.toUpperCase() === "SUCCEEDED";
    }
  }, [paymentId, order?.paymentId, normalizedInitial]);

  // Fetch active payment methods so buyer only sees enabled methods when switching/retrying
  useEffect(() => {
    let active = true;
    api<GatewayPaymentMethodsResponse>("/api/payment-methods", { retryOn401: false })
      .then((res) => {
        if (active && Array.isArray(res?.methods)) {
          setEnabledMethods(res.methods.filter((m) => m.enabled));
        }
      })
      .catch(() => {
        // Non-blocking: retry still works with current method even if config fetch fails transiently
      });
    return () => {
      active = false;
    };
  }, []);

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
      const reqSeq = ++fetchSeqRef.current;
      if (!silent) {
        setLoading(true);
        setLoadError(null);
      }
      try {
        const res = await api<GatewayPaymentResponse>(
          `/api/payments/${encodeURIComponent(targetId)}`
        );
        if (
          !shouldApplyPaymentFetchResult({
            targetId,
            activePaymentId: activePaymentIdRef.current,
            requestSeq: reqSeq,
            latestSeq: fetchSeqRef.current,
          })
        ) {
          return;
        }
        if (res?.payment) {
          applyPaymentUpdate(res.payment);
        } else if (!silent) {
          setLoadError("Không tìm thấy thông tin giao dịch thanh toán.");
        }
      } catch (err) {
        if (
          !shouldApplyPaymentFetchResult({
            targetId,
            activePaymentId: activePaymentIdRef.current,
            requestSeq: reqSeq,
            latestSeq: fetchSeqRef.current,
          })
        ) {
          return;
        }
        if (!silent) {
          setLoadError(getErrorMessage(err, "Không thể tải thông tin thanh toán lúc này."));
        }
      } finally {
        if (
          !silent &&
          shouldApplyPaymentFetchResult({
            targetId,
            activePaymentId: activePaymentIdRef.current,
            requestSeq: reqSeq,
            latestSeq: fetchSeqRef.current,
          })
        ) {
          setLoading(false);
        }
      }
    },
    [applyPaymentUpdate]
  );

  // Initial load when no SSR snapshot is provided
  useEffect(() => {
    if (!activePaymentId) {
      setLoading(false);
      return;
    }
    if (normalizedInitial && normalizedInitial.payment_id === activePaymentId) {
      return;
    }
    void fetchPayment(activePaymentId, false);
  }, [activePaymentId, fetchPayment, normalizedInitial]);

  const normalizedStatus = (payment?.status ?? "").toUpperCase();
  const normalizedMethod = (payment?.method ?? "VIETQR").toUpperCase();
  const isZaloPay = normalizedMethod === "ZALOPAY";
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

  // Live SSE subscription while PENDING via explicit BFF route
  useEffect(() => {
    if (!isPending || !activePaymentId) return;

    let closed = false;
    let source: EventSource | null = null;
    let reconnectTimer: number | null = null;
    let retryAttempt = 0;
    let refreshedForThisDisconnect = false;

    const handleSsePayload = (rawData: string) => {
      if (closed) return;
      try {
        const parsed = JSON.parse(rawData) as { status?: string };
        const evtStatus = (parsed.status ?? "").toUpperCase();
        if (evtStatus && evtStatus !== "PENDING" && evtStatus !== "UNKNOWN") {
          if (isTerminalPaymentStatus(evtStatus)) {
            closed = true;
            if (reconnectTimer !== null) {
              window.clearTimeout(reconnectTimer);
              reconnectTimer = null;
            }
            if (source) {
              source.close();
              source = null;
            }
          }
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

      try {
        const nextSource = new EventSource(
          `/api/payments/${encodeURIComponent(activePaymentId)}/events`,
          { withCredentials: true }
        );
        source = nextSource;

        nextSource.onopen = () => {
          if (closed) return;
          retryAttempt = 0;
          refreshedForThisDisconnect = false;
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

  // Separate polling fallback effect while PENDING
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

  useEffect(() => {
    if (!codConfirmOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !retryingRef.current) {
        setCodConfirmOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [codConfirmOpen]);

  const handleManualCheck = async () => {
    setCheckingStatus(true);
    setRetryFeedback(null);
    await fetchPayment(activePaymentId, true);
    setCheckingStatus(false);
  };

  const handleRetryOrSwitch = async (targetMethod: GatewayPaymentMethod) => {
    if (retryingRef.current || retryingMethod || !activePaymentId) return;
    if (isPaymentDeadlinePassed(payment?.expires_at)) {
      setCodConfirmOpen(false);
      setNowMs(Date.now());
      return;
    }
    retryingRef.current = true;
    setRetryingMethod(targetMethod);
    setRetryFeedback(null);
    try {
      const { key: idempotencyKey, cache } = resolveStableReferenceKey({
        prefix: "nm-pay-retry",
        fingerprint: `${activePaymentId}:${targetMethod}`,
        cache: retryIdempotencyCacheRef.current,
      });
      retryIdempotencyCacheRef.current = cache;
      const res = await api<GatewayRetryPaymentResponse>(
        `/api/payments/${encodeURIComponent(activePaymentId)}/retry`,
        {
          method: "POST",
          headers: {
            "Idempotency-Key": idempotencyKey,
          },
          body: { method: targetMethod },
        }
      );

      retryIdempotencyCacheRef.current = null;
      setCodConfirmOpen(false);

      if (res?.switched_to_cod) {
        const orders = Array.isArray(res.orders) ? res.orders : [];
        setRetryFeedback({
          tone: "success",
          message:
            "Đã chuyển sang thanh toán khi nhận hàng (COD) và xác nhận đơn hàng. Đang chuyển tới trang đơn hàng...",
        });
        if (onSwitchedToCod) {
          onSwitchedToCod(orders);
        } else {
          const nextHref =
            orders.length === 1
              ? `/don-hang/${encodeURIComponent(orders[0].id)}`
              : order?.id
                ? `/don-hang/${encodeURIComponent(order.id)}`
                : "/don-hang";
          router.replace(nextHref);
        }
        return;
      }

      if (res?.payment) {
        const mergedPayment: GatewayPayment = {
          ...res.payment,
          pay_url: res.payment.pay_url || res.pay_url,
        };
        paidNotifiedRef.current = mergedPayment.status.toUpperCase() === "SUCCEEDED";
        activePaymentIdRef.current = mergedPayment.payment_id;
        fetchSeqRef.current += 1;
        setActivePaymentId(mergedPayment.payment_id);
        setPayment(mergedPayment);
        setRetryFeedback({
          tone: "success",
          message:
            targetMethod === "ZALOPAY"
              ? "Đã tạo liên kết thanh toán ZaloPay mới. Nhấn Thanh toán qua ZaloPay trong thời hạn đếm ngược."
              : "Đã tạo mã VietQR mới. Vui lòng quét mã trong thời hạn đếm ngược trên màn hình.",
        });
        onPaymentReplaced?.(mergedPayment);
        onCompleted?.(mergedPayment);
      }
    } catch (err) {
      setCodConfirmOpen(false);
      if (
        err instanceof ApiError &&
        (err.code === "COD_PHONE_NOT_VERIFIED" ||
          err.code === "COD_AREA_NOT_SUPPORTED" ||
          err.code === "COD_REJECTED_LIMIT_EXCEEDED" ||
          err.code === "COD_LIMIT_EXCEEDED" ||
          err.code === "FLASH_SALE_COD_NOT_ALLOWED" ||
          err.code === "PAYMENT_METHOD_DISABLED" ||
          err.code === "ORDER_AMOUNT_OUT_OF_RANGE" ||
          err.code === "SHOP_SUSPENDED")
      ) {
        setRetryFeedback({
          tone: "error",
          message: err.message,
          code: err.code,
        });
      } else if (
        err instanceof ApiError &&
        (err.status === 409 || err.code === "FAILED_PRECONDITION")
      ) {
        setRetryFeedback({
          tone: "error",
          message:
            err.message ||
            "Giao dịch đã hết hạn hoặc không thể đổi phương thức lúc này. Vui lòng kiểm tra đơn hàng.",
        });
        await fetchPayment(activePaymentId, true);
      } else {
        setRetryFeedback({
          tone: "error",
          message: getErrorMessage(
            err,
            "Chưa thể xử lý yêu cầu thanh toán lúc này. Vui lòng thử lại."
          ),
        });
      }
    } finally {
      retryingRef.current = false;
      setRetryingMethod(null);
    }
  };

  if (loading) {
    return <StateView kind="loading" title="Đang tải thông tin giao dịch thanh toán..." rows={3} />;
  }

  if (!activePaymentId || loadError || !payment) {
    return (
      <StateView
        kind="error"
        title="Không thể tải thông tin thanh toán"
        message={loadError ?? "Giao dịch không tồn tại hoặc không thuộc tài khoản của bạn."}
        actionLabel={activePaymentId ? "Thử tải lại" : "Xem đơn hàng"}
        onAction={activePaymentId ? () => void fetchPayment(activePaymentId, false) : undefined}
        actionHref={activePaymentId ? undefined : "/don-hang"}
      />
    );
  }

  const statusView = getPaymentStatusView(payment.status, payment.method);
  const transferRef = payment.provider_ref || payment.order_group_id || payment.payment_id;
  const allocatedOrderIds = Array.from(
    new Set(
      (payment.shop_allocations?.map((a) => a.order_id) ?? []).filter((id): id is string =>
        Boolean(id)
      )
    )
  );
  const orderIds =
    allocatedOrderIds.length > 0
      ? allocatedOrderIds
      : payment.order_id
        ? [payment.order_id]
        : order?.id
          ? [order.id]
          : [];
  const primaryOrderHref =
    orderIds.length === 1 ? `/don-hang/${encodeURIComponent(orderIds[0])}` : "/don-hang";
  const canRetry = canRetryPayment(normalizedStatus, countdownExpired);
  const isExpiredState = normalizedStatus === "EXPIRED" || countdownExpired;
  const currentOrigin = typeof window !== "undefined" ? window.location.origin : undefined;
  const validatedPayUrl = isZaloPay ? validateSafePayUrl(payment.pay_url, currentOrigin) : null;

  const availableMethodSet = new Set<GatewayPaymentMethod>(
    enabledMethods.length > 0
      ? enabledMethods
          .map((m) => String(m.method).toUpperCase())
          .filter(
            (m): m is GatewayPaymentMethod => m === "COD" || m === "VIETQR" || m === "ZALOPAY"
          )
      : METHOD_ORDER
  );
  const currentRetryMethod: GatewayPaymentMethod = isZaloPay ? "ZALOPAY" : "VIETQR";
  const switchableMethods = METHOD_ORDER.filter(
    (m) => m !== currentRetryMethod && availableMethodSet.has(m)
  );
  const canRetryCurrentMethod = availableMethodSet.has(currentRetryMethod);

  const waitingHref =
    orderIds.length === 1
      ? `/thanh-toan/cho/${encodeURIComponent(activePaymentId)}?order=${encodeURIComponent(orderIds[0])}`
      : `/thanh-toan/cho/${encodeURIComponent(activePaymentId)}`;

  return (
    <div
      className={styles.retryBox}
      role="region"
      aria-label={`Thông tin thanh toán ${getMethodHeading(payment.method)} ${transferRef}`}
    >
      <div className={styles.retryHead}>
        <div className={styles.paymentTitleGroup}>
          <p className={styles.retryTitle}>
            {getMethodHeading(payment.method)} ·{" "}
            <span className="num">{formatVnd(payment.amount_vnd)}</span>
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

      {isPending && isZaloPay && (
        <div className={styles.surfacePanel}>
          <p className={styles.fieldLabel}>Liên kết thanh toán Ví ZaloPay</p>
          <p className={styles.fieldHint} style={{ marginTop: 6 }}>
            Mã tham chiếu giao dịch: <strong className="num">{transferRef}</strong> · Số tiền:{" "}
            <strong className="num">{formatVnd(payment.amount_vnd)}</strong>
          </p>
          {validatedPayUrl?.safe && validatedPayUrl.href ? (
            <div className={styles.formActions} style={{ marginTop: 12 }}>
              <a
                href={validatedPayUrl.href}
                target={validatedPayUrl.isExternal ? "_blank" : undefined}
                rel={validatedPayUrl.isExternal ? "noopener noreferrer" : undefined}
                className={`${styles.wrapBtn} ${styles.zaloPayLinkBtn}`}
              >
                Thanh toán qua ZaloPay
              </a>
            </div>
          ) : (
            <p
              className={`${styles.feedbackBox} ${styles.feedbackError}`}
              role="alert"
              style={{ marginTop: 10 }}
            >
              Liên kết thanh toán ZaloPay chưa sẵn sàng hoặc không hợp lệ. Vui lòng tạo lại liên kết
              hoặc chọn phương thức thanh toán khác bên dưới.
            </p>
          )}
          <p
            className={styles.paymentLiveStatus}
            role="status"
            aria-live="polite"
            style={{ marginTop: 12 }}
          >
            {countdownExpired
              ? "Hạn giữ đơn đã qua và đơn hàng sẽ tự động hủy. Vui lòng kiểm tra trạng thái đơn hoặc đặt lại đơn mới từ giỏ hàng."
              : "Đang tự động theo dõi trạng thái thanh toán từ Ví ZaloPay..."}
          </p>
        </div>
      )}

      {isPending && !isZaloPay && (
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
        <div
          className={`${styles.feedbackBox} ${
            retryFeedback.tone === "error"
              ? styles.feedbackError
              : retryFeedback.tone === "success"
                ? styles.feedbackSuccess
                : styles.feedbackInfo
          }`}
          role={retryFeedback.tone === "error" ? "alert" : "status"}
        >
          <p>{retryFeedback.message}</p>
          {retryFeedback.code === "COD_PHONE_NOT_VERIFIED" && (
            <div className={styles.formActions}>
              <Button variant="secondary" size="sm" href="/tai-khoan#xac-minh-sdt">
                Xác minh SĐT ngay
              </Button>
            </div>
          )}
        </div>
      )}

      <div className={styles.formActions}>
        {normalizedStatus === "SUCCEEDED" ? (
          <>
            <Button variant="primary" size="md" href={primaryOrderHref}>
              {orderIds.length > 1 ? "Xem các đơn hàng của bạn" : "Xem chi tiết đơn hàng"}
            </Button>
            <Button variant="secondary" size="md" href="/san-pham">
              Tiếp tục mua sắm
            </Button>
          </>
        ) : (
          <>
            {canRetry && canRetryCurrentMethod && (
              <Button
                variant={isPending ? "secondary" : "primary"}
                size="md"
                className={styles.wrapBtn}
                disabled={retryingMethod !== null}
                onClick={() => void handleRetryOrSwitch(currentRetryMethod)}
              >
                {retryingMethod === currentRetryMethod
                  ? isZaloPay
                    ? "Đang tạo liên kết ZaloPay..."
                    : "Đang tạo mã VietQR mới..."
                  : isPending
                    ? isZaloPay
                      ? "Tạo liên kết ZaloPay mới"
                      : "Tạo mã VietQR mới"
                    : isZaloPay
                      ? "Thanh toán lại qua ZaloPay"
                      : "Thanh toán lại qua VietQR"}
              </Button>
            )}
            {canRetry &&
              switchableMethods.map((methodOption) => (
                <Button
                  key={methodOption}
                  variant="secondary"
                  size="md"
                  className={styles.wrapBtn}
                  disabled={retryingMethod !== null}
                  onClick={() => {
                    if (methodOption === "COD") {
                      if (!retryingRef.current) {
                        setCodConfirmOpen(true);
                      }
                      return;
                    }
                    void handleRetryOrSwitch(methodOption);
                  }}
                >
                  {retryingMethod === methodOption
                    ? "Đang chuyển phương thức..."
                    : methodOption === "COD"
                      ? "Đổi sang COD"
                      : methodOption === "ZALOPAY"
                        ? "Đổi sang Ví ZaloPay"
                        : "Đổi sang VietQR"}
                </Button>
              ))}
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
              <Button variant="ink" size="md" href={waitingHref}>
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
            {onCancel && (
              <Button variant="ghost" size="md" onClick={onCancel}>
                Đóng
              </Button>
            )}
          </>
        )}
      </div>

      {codConfirmOpen && (
        <div
          className={styles.confirmBackdrop}
          role="dialog"
          aria-modal="true"
          aria-labelledby="cod-switch-dialog-title"
          onClick={(e) => {
            if (e.target === e.currentTarget && !retryingRef.current) {
              setCodConfirmOpen(false);
            }
          }}
        >
          <div className={styles.confirmDialog}>
            <h3 id="cod-switch-dialog-title" className={styles.confirmTitle}>
              Xác nhận đổi sang thanh toán khi nhận hàng (COD)
            </h3>
            <p className={styles.confirmBody}>
              Sau khi xác nhận, đơn hàng sẽ được chuyển ngay sang trạng thái{" "}
              <strong>Đã xác nhận</strong> với hình thức thanh toán khi nhận hàng và không thể quay
              lại thanh toán trực tuyến.
            </p>
            <ul className={styles.confirmList}>
              <li className={styles.confirmListItem}>
                <span>Số tiền thanh toán khi nhận hàng</span>
                <strong className="num">{formatVnd(payment.amount_vnd)}</strong>
              </li>
              <li className={styles.confirmListItem}>
                <span>Hình thức mới</span>
                <strong>Thanh toán khi nhận hàng (COD)</strong>
              </li>
            </ul>
            <div className={styles.formActions}>
              <Button
                variant="secondary"
                size="md"
                disabled={retryingMethod !== null}
                onClick={() => setCodConfirmOpen(false)}
              >
                Quay lại
              </Button>
              <Button
                variant="primary"
                size="md"
                disabled={retryingMethod !== null}
                onClick={() => void handleRetryOrSwitch("COD")}
              >
                {retryingMethod === "COD" ? "Đang xác nhận COD..." : "Xác nhận đổi sang COD"}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
