"use client";

import React, { useState } from "react";
import type { Order, PaymentMethod } from "@/data";
import { PAYMENT_METHOD_LABELS, WALLET_PAYMENT_METHOD } from "@/data";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button } from "@/components/Button";
import { VietQrPreview } from "./VietQrPreview";
import styles from "./commerce.module.css";

type GatewayChoice = "vietqr" | "nexuspay" | "linked_wallet" | "cod";

const GATEWAY_CHOICES: Array<{
  id: GatewayChoice;
  method: PaymentMethod;
  title: string;
  shortLabel: string;
  subtitle: string;
}> = [
  {
    id: "vietqr",
    method: "vietqr",
    title: PAYMENT_METHOD_LABELS.vietqr,
    shortLabel: "VietQR",
    subtitle: "Quét mã QR ngân hàng, xác nhận đơn ngay lập tức",
  },
  {
    id: "nexuspay",
    method: WALLET_PAYMENT_METHOD,
    title: PAYMENT_METHOD_LABELS[WALLET_PAYMENT_METHOD],
    shortLabel: "Ví NexusPay",
    subtitle: "Thanh toán nhanh bằng số dư ví NexusPay của bạn",
  },
  {
    id: "linked_wallet",
    method: WALLET_PAYMENT_METHOD,
    title: "Ví điện tử liên kết",
    shortLabel: "Ví liên kết",
    subtitle: "Thanh toán qua ví nội địa đã liên kết, không cần nhập thẻ",
  },
  {
    id: "cod",
    method: "cod",
    title: "Đổi sang thanh toán khi nhận hàng (COD)",
    shortLabel: "COD",
    subtitle: "Trả tiền khi nhận hàng, được mở kiểm tra mẫu và size",
  },
];

export interface RetryPaymentBoxProps {
  order: Order;
  onCompleted?: () => void;
  onCancel?: () => void;
}

export function RetryPaymentBox({ order, onCompleted, onCancel }: RetryPaymentBoxProps) {
  const { retryOrderPayment } = useStore();
  const [choice, setChoice] = useState<GatewayChoice>(
    order.paymentMethod === "cod"
      ? "vietqr"
      : order.paymentMethod === WALLET_PAYMENT_METHOD
        ? "nexuspay"
        : "vietqr"
  );

  const selectedConfig = GATEWAY_CHOICES.find((c) => c.id === choice) ?? GATEWAY_CHOICES[0];

  const handleConfirm = () => {
    const ok = retryOrderPayment(order.id, selectedConfig.method);
    if (ok) {
      onCompleted?.();
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
          Đơn <span className="num">{order.orderCode}</span> đang chờ thanh toán lại
        </p>
        <span className={`${styles.statusTag} ${styles.toneWarn} num`}>
          Cần thanh toán: {formatVnd(order.total)}
        </span>
      </div>

      <p className={styles.retryText}>
        Giao dịch trước đó chưa hoàn tất. Các sản phẩm đúng màu và size vẫn được giữ sẵn cho bạn
        {order.paymentDueAt ? ` đến ${formatDateTimeVi(order.paymentDueAt)}` : ""}. Chọn phương thức
        bên dưới để hoàn tất ngay mà không cần đặt lại giỏ hàng.
      </p>

      <div
        className={styles.retryMethods}
        role="radiogroup"
        aria-label="Chọn phương thức thanh toán lại"
      >
        {GATEWAY_CHOICES.map((gw) => {
          const active = choice === gw.id;
          return (
            <button
              key={gw.id}
              type="button"
              role="radio"
              aria-checked={active}
              className={`${styles.retryMethodBtn} ${active ? styles.retryMethodActive : ""}`}
              onClick={() => setChoice(gw.id)}
            >
              <span className={styles.retryMethodName}>{gw.title}</span>
              <span className={styles.retryMethodSub}>{gw.subtitle}</span>
            </button>
          );
        })}
      </div>

      {choice === "vietqr" && <VietQrPreview amount={order.total} reference={order.orderCode} />}

      <div className={styles.formActions}>
        <Button variant="primary" size="md" className={styles.wrapBtn} onClick={handleConfirm}>
          Thanh toán · {formatVnd(order.total)} ({selectedConfig.shortLabel})
        </Button>
        {onCancel && (
          <Button variant="secondary" size="md" onClick={onCancel}>
            Đóng
          </Button>
        )}
      </div>
    </div>
  );
}
