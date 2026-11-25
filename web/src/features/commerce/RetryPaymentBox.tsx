"use client";

import React, { useState } from "react";
import { useRouter } from "next/navigation";
import type { Order } from "@/data/orders";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button } from "@/components/Button";
import type { GwPayment } from "./types";
import styles from "./commerce.module.css";

export interface RetryPaymentBoxProps {
  order: Order & { paymentId?: string };
  paymentId?: string;
  onCompleted?: (nextPayment?: GwPayment) => void;
  onCancel?: () => void;
}

export function RetryPaymentBox({ order, paymentId, onCompleted, onCancel }: RetryPaymentBoxProps) {
  const router = useRouter();
  const { showToast } = useStore();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const targetPaymentId = (paymentId ?? order.paymentId ?? "").trim();

  const handleOpenWaiting = () => {
    if (!targetPaymentId) return;
    router.push(
      `/thanh-toan/cho/${encodeURIComponent(targetPaymentId)}?order=${encodeURIComponent(order.id)}`
    );
  };

  const handleCreateNewQr = async () => {
    if (!targetPaymentId || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      const res = await api<{ payment: GwPayment }>(
        `/api/payments/${encodeURIComponent(targetPaymentId)}/retry`,
        {
          method: "POST",
          body: { method: "VIETQR" },
        }
      );
      showToast("Đã tạo mã VietQR mới.");
      onCompleted?.(res.payment);
      if (res.payment?.payment_id) {
        router.push(
          `/thanh-toan/cho/${encodeURIComponent(res.payment.payment_id)}?order=${encodeURIComponent(order.id)}`
        );
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Chưa thể tạo lại mã thanh toán lúc này.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      className={styles.retryBox}
      role="region"
      aria-label={`Thanh toán lại đơn ${order.orderCode}`}
    >
      <div className={styles.retryHead}>
        <p className={styles.retryTitle}>
          Đơn <span className="num">{order.orderCode}</span> chờ thanh toán VietQR
        </p>
        <span className={`${styles.statusTag} ${styles.toneWarn} num`}>
          {formatVnd(order.total)}
        </span>
      </div>

      {order.paymentDueAt && (
        <p className={styles.retryText}>
          Giữ tồn kho đến <span className="num">{formatDateTimeVi(order.paymentDueAt)}</span>.
        </p>
      )}

      {error && (
        <p className={`${styles.feedbackBox} ${styles.feedbackError}`} role="alert">
          {error}
        </p>
      )}

      <div className={styles.formActions}>
        {targetPaymentId ? (
          <>
            <Button
              variant="primary"
              size="md"
              className={styles.wrapBtn}
              onClick={handleOpenWaiting}
            >
              Mở mã VietQR ({formatVnd(order.total)})
            </Button>
            <Button
              variant="secondary"
              size="md"
              className={styles.wrapBtn}
              loading={submitting}
              loadingLabel="Đang tạo mã mới"
              onClick={handleCreateNewQr}
            >
              Tạo mã VietQR mới
            </Button>
          </>
        ) : (
          <Button variant="primary" size="md" href="/don-hang">
            Xem đơn hàng
          </Button>
        )}
        {onCancel && (
          <Button variant="secondary" size="md" onClick={onCancel}>
            Đóng
          </Button>
        )}
      </div>
    </div>
  );
}
