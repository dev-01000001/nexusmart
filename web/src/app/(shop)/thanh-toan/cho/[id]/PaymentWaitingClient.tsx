"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { VietQrPreview } from "@/features/commerce/VietQrPreview";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import type { GwPayment } from "@/features/commerce/types";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

const POLL_INTERVAL_MS = 2500;

export interface PaymentWaitingClientProps {
  paymentId: string;
  initialPayment: GwPayment | null;
  fallbackOrderId: string;
  initialError: string | null;
  shopNameById: Record<string, string>;
}

function formatRemaining(ms: number): string {
  if (ms <= 0) return "00:00";
  const totalSecs = Math.floor(ms / 1000);
  const mins = Math.floor(totalSecs / 60);
  const secs = totalSecs % 60;
  return `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
}

export function PaymentWaitingClient({
  paymentId,
  initialPayment,
  fallbackOrderId,
  initialError,
  shopNameById,
}: PaymentWaitingClientProps) {
  const router = useRouter();
  const { refreshCart, showToast } = useStore();

  const [payment, setPayment] = useState<GwPayment | null>(initialPayment);
  const [error, setError] = useState<string | null>(initialError);
  const [pollWarning, setPollWarning] = useState<string | null>(null);
  const [checking, setChecking] = useState<boolean>(false);
  const [retrying, setRetrying] = useState<boolean>(false);
  const [sessionExpired, setSessionExpired] = useState<boolean>(false);
  const [nowMs, setNowMs] = useState<number>(() => Date.now());

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

  useRevealOnScroll(`pay-wait-${paymentId}-${status}`);

  const fetchStatus = useCallback(
    async (manual = false) => {
      if (manual) setChecking(true);
      try {
        const res = await api<{ payment: GwPayment }>(
          `/api/payments/${encodeURIComponent(paymentId)}`
        );
        setPayment(res.payment);
        setError(null);
        setPollWarning(null);
        if (res.payment.status.toUpperCase() === "SUCCEEDED") {
          void refreshCart();
        }
      } catch (err) {
        if (err instanceof ApiError) {
          if (err.status === 401) {
            setSessionExpired(true);
            setPollWarning(
              "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại để theo dõi thanh toán."
            );
            return;
          }
          if (!payment) {
            setError(err.message);
          } else {
            setPollWarning(err.message);
          }
        } else if (!payment) {
          setError("Chưa thể tải thông tin thanh toán lúc này.");
        }
      } finally {
        if (manual) setChecking(false);
      }
    },
    [paymentId, payment, refreshCart]
  );

  // Poll real payment status while PENDING
  useEffect(() => {
    if (!isPending || sessionExpired) return;
    const timer = window.setInterval(() => {
      void fetchStatus(false);
    }, POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [isPending, sessionExpired, fetchStatus]);

  // Countdown tick while PENDING
  useEffect(() => {
    if (!isPending || !payment?.expires_at) return;
    const timer = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, [isPending, payment?.expires_at]);

  // Auto-navigate to real order detail shortly after payment succeeds
  useEffect(() => {
    if (!isPaid) return;
    showToast("Đã nhận thanh toán VietQR thành công!");
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
      showToast("Đã tạo mã VietQR mới cho đơn hàng của bạn.");
      const nextId = res.payment.payment_id;
      const q = primaryOrderId ? `?order=${encodeURIComponent(primaryOrderId)}` : "";
      router.push(`/thanh-toan/cho/${encodeURIComponent(nextId)}${q}`);
    } catch (err) {
      setPollWarning(
        err instanceof ApiError
          ? err.message
          : "Chưa thể tạo mã VietQR mới lúc này. Bạn thử lại nhé."
      );
    } finally {
      setRetrying(false);
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Thanh toán VietQR
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/don-hang">
          Đơn hàng của tôi
        </Button>
      </div>
    </section>
  );

  if (error && !payment) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể tải thông tin giao dịch thanh toán lúc này.")}
        <section className={shared.section}>
          <StateView
            kind="error"
            title="Chưa thể hiển thị mã VietQR"
            message={error}
            actionLabel="Thử tải lại"
            onAction={() => void fetchStatus(true)}
          />
        </section>
      </div>
    );
  }

  if (!payment) {
    return (
      <div className={shared.page}>
        {renderHeader("Đang tải thông tin mã chuyển khoản VietQR.")}
        <section className={shared.section}>
          <StateView kind="loading" title="Đang chuẩn bị mã VietQR..." rows={3} />
        </section>
      </div>
    );
  }

  const expiresMs = payment.expires_at ? Date.parse(payment.expires_at) : NaN;
  const remainingMs = Number.isFinite(expiresMs) ? Math.max(0, expiresMs - nowMs) : null;
  const transferRef =
    payment.provider_ref ||
    (payment.order_group_id ? `NEXUSMART ${payment.order_group_id}` : payment.payment_id);

  const badgeTone = isPaid ? shared.tonePos : isTerminalFailed ? shared.toneNeg : shared.toneWarn;

  const statusLabel = isPaid
    ? "Đã thanh toán"
    : status === "EXPIRED"
      ? "Đã hết hạn"
      : status === "FAILED"
        ? "Thanh toán chưa thành công"
        : status === "CANCELLED"
          ? "Đã hủy"
          : status === "REFUNDED"
            ? "Đã hoàn tiền"
            : "Đang chờ thanh toán";

  return (
    <div className={shared.page}>
      {renderHeader(
        isPaid
          ? "Thanh toán đã được xác nhận. Đơn hàng đang chuyển tới xưởng chuẩn bị."
          : "Quét mã QR bằng ứng dụng ngân hàng để hoàn tất đơn. Trang sẽ tự động cập nhật."
      )}

      <section className={shared.section}>
        <div className={styles.sheet} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
          <div className={styles.banner}>
            <div className={styles.bannerLeft}>
              <div>
                <span className={`${shared.statusTag} ${badgeTone}`}>{statusLabel}</span>
              </div>
              <h2 className={styles.bannerTitle}>
                {isPaid
                  ? "Xác nhận thanh toán thành công"
                  : isTerminalFailed
                    ? "Mã thanh toán không còn hiệu lực"
                    : "Chờ xác nhận chuyển khoản VietQR"}
              </h2>
              <p className={styles.bannerMeta}>
                Nội dung chuyển khoản: <strong className="num">{transferRef}</strong>
                {payment.expires_at && isPending
                  ? ` · Hết hạn lúc ${formatDateTimeVi(payment.expires_at)}`
                  : ""}
              </p>
            </div>
            <div className={styles.bannerAmountCol}>
              <span className={styles.bannerMeta}>Số tiền thanh toán</span>
              <span className={`${styles.bannerAmount} num`}>{formatVnd(payment.amount_vnd)}</span>
            </div>
          </div>

          <div className={styles.body}>
            {isPaid ? (
              <div
                className={`${styles.statusBar} ${styles.statusBarSuccess}`}
                role="status"
                aria-live="polite"
              >
                <div className={styles.statusTextGroup}>
                  <span className={styles.statusTitle}>Đã nhận thanh toán đủ số tiền</span>
                  <span className={styles.statusSub}>
                    Đơn hàng của bạn đã được xác nhận. Hệ thống đang chuyển tới trang chi tiết đơn
                    hàng.
                  </span>
                </div>
                <Button variant="primary" size="md" href={orderDetailHref}>
                  Xem chi tiết đơn hàng
                </Button>
              </div>
            ) : isTerminalFailed ? (
              <div className={`${styles.statusBar} ${styles.statusBarFailed}`} role="alert">
                <div className={styles.statusTextGroup}>
                  <span className={styles.statusTitle}>
                    {status === "EXPIRED"
                      ? "Mã VietQR đã hết thời hạn thanh toán"
                      : "Giao dịch thanh toán chưa hoàn tất"}
                  </span>
                  <span className={styles.statusSub}>
                    Bạn có thể tạo mã VietQR mới để tiếp tục thanh toán hoặc kiểm tra trạng thái
                    trong danh sách đơn hàng.
                  </span>
                </div>
                <div className={shared.formActions}>
                  {status === "FAILED" && (
                    <Button
                      variant="primary"
                      size="md"
                      disabled={retrying}
                      onClick={handleRetryPayment}
                    >
                      {retrying ? "Đang tạo mã mới..." : "Tạo mã VietQR mới"}
                    </Button>
                  )}
                  <Button variant="secondary" size="md" href={orderDetailHref}>
                    Xem chi tiết đơn
                  </Button>
                </div>
              </div>
            ) : (
              <div className={styles.statusBar} role="status" aria-live="polite">
                <div className={styles.statusTextGroup}>
                  <span className={styles.statusTitle}>Đang tự động kiểm tra giao dịch</span>
                  <span className={styles.statusSub}>
                    Giữ nguyên trang này sau khi chuyển khoản. Đơn hàng sẽ tự chuyển sang trạng thái
                    Đã xác nhận.
                  </span>
                </div>
                {remainingMs !== null && (
                  <span className={`${styles.countdown} num`} aria-label="Thời gian còn lại">
                    {formatRemaining(remainingMs)}
                  </span>
                )}
              </div>
            )}

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

            {isPending && (
              <VietQrPreview
                amount={payment.amount_vnd}
                reference={transferRef}
                qrCode={payment.qr_code}
                expiresAt={payment.expires_at}
              />
            )}

            <div className={shared.formActions}>
              {isPending && (
                <>
                  <Button
                    variant="ink"
                    size="md"
                    disabled={checking}
                    onClick={() => void fetchStatus(true)}
                  >
                    {checking ? "Đang kiểm tra..." : "Tôi đã chuyển khoản · Kiểm tra ngay"}
                  </Button>
                  <Button
                    variant="secondary"
                    size="md"
                    disabled={retrying}
                    onClick={handleRetryPayment}
                  >
                    {retrying ? "Đang tạo lại..." : "Tạo mã QR mới"}
                  </Button>
                </>
              )}
              <Button variant="secondary" size="md" href={orderDetailHref}>
                Xem chi tiết đơn hàng
              </Button>
            </div>

            {payment.shop_allocations && payment.shop_allocations.length > 0 && (
              <div className={styles.allocSection}>
                <h3 className={styles.allocHeading}>
                  Đơn hàng trong lượt thanh toán ({payment.shop_allocations.length} gian hàng)
                </h3>
                <div className={styles.allocList}>
                  {payment.shop_allocations.map((alloc) => (
                    <div key={alloc.order_id} className={styles.allocRow}>
                      <div>
                        <span>{shopNameById[alloc.shop_id] || "Gian hàng NexusMart"}</span>
                        {" · "}
                        <Link
                          href={`/don-hang/${encodeURIComponent(alloc.order_id)}`}
                          className={styles.orderLink}
                        >
                          Xem đơn hàng
                        </Link>
                      </div>
                      <span className="num">
                        {formatVnd(alloc.subtotal_vnd + alloc.shipping_fee_vnd)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
