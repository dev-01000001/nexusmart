"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { GatewayOrder, GatewayPayment } from "@/lib/gateway-types";
import { formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { computeShopAllocationPayableVnd, readCheckoutWarnings } from "@/features/checkout/helpers";
import type { GwPayment } from "@/features/commerce/types";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

export interface PaymentWaitingClientProps {
  brandName: string;
  paymentId: string;
  initialPayment: GwPayment | null;
  fallbackOrderId: string;
  initialError: string | null;
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

  const [activePaymentId, setActivePaymentId] = useState<string>(
    initialPayment?.payment_id || paymentId
  );
  const [snapshot, setSnapshot] = useState<GwPayment | GatewayPayment | null>(initialPayment);
  const [paidRedirecting, setPaidRedirecting] = useState<boolean>(
    (initialPayment?.status ?? "").toUpperCase() === "SUCCEEDED"
  );
  const [checkoutWarnings, setCheckoutWarnings] = useState<string[]>([]);
  const redirectTimerRef = useRef<number | null>(null);

  useEffect(() => {
    setCheckoutWarnings(readCheckoutWarnings({ paymentId: activePaymentId }));
  }, [activePaymentId]);

  useEffect(() => {
    return () => {
      if (redirectTimerRef.current !== null) {
        window.clearTimeout(redirectTimerRef.current);
      }
    };
  }, []);

  const primaryOrderId = useMemo(() => {
    if (snapshot?.shop_allocations?.length && snapshot.shop_allocations[0]?.order_id) {
      return snapshot.shop_allocations[0].order_id;
    }
    if (snapshot?.order_id) return snapshot.order_id;
    return fallbackOrderId;
  }, [snapshot, fallbackOrderId]);

  const orderDetailHref = primaryOrderId
    ? `/don-hang/${encodeURIComponent(primaryOrderId)}`
    : "/don-hang";

  const isZaloPay = (snapshot?.method ?? "").toUpperCase() === "ZALOPAY";
  const isPaid = paidRedirecting || (snapshot?.status ?? "").toUpperCase() === "SUCCEEDED";
  const pageTitle = isZaloPay ? "Thanh toán ZaloPay" : "Thanh toán VietQR";
  const pageLead = isZaloPay
    ? `Hoàn tất thanh toán qua Ví ZaloPay trong thời gian chờ thanh toán tại ${brandName}`
    : `Quét mã bằng ứng dụng ngân hàng tại ${brandName}`;

  const handlePaid = useCallback(() => {
    void refreshCart();
    setPaidRedirecting(true);
    showToast("Đã nhận thanh toán thành công.");
    if (redirectTimerRef.current !== null) {
      window.clearTimeout(redirectTimerRef.current);
    }
    redirectTimerRef.current = window.setTimeout(() => {
      router.push(orderDetailHref);
    }, 1800);
  }, [orderDetailHref, refreshCart, router, showToast]);

  const handlePaymentReplaced = useCallback(
    (nextPayment: GatewayPayment) => {
      setSnapshot(nextPayment);
      setActivePaymentId(nextPayment.payment_id);
      showToast(
        nextPayment.method.toUpperCase() === "ZALOPAY"
          ? "Đã tạo liên kết thanh toán ZaloPay mới."
          : "Đã tạo mã VietQR mới."
      );
      const q = primaryOrderId ? `?order=${encodeURIComponent(primaryOrderId)}` : "";
      router.replace(`/thanh-toan/cho/${encodeURIComponent(nextPayment.payment_id)}${q}`);
    },
    [primaryOrderId, router, showToast]
  );

  const handleSwitchedToCod = useCallback(
    (orders: GatewayOrder[]) => {
      void refreshCart();
      showToast("Đã chuyển đơn sang thanh toán khi nhận hàng (COD).");
      const nextHref =
        orders.length === 1 ? `/don-hang/${encodeURIComponent(orders[0].id)}` : orderDetailHref;
      router.replace(nextHref);
    },
    [orderDetailHref, refreshCart, router, showToast]
  );

  if (initialError && !snapshot) {
    return (
      <div className={shared.page}>
        <div className={styles.stageWrap}>
          <header className={styles.stageHead}>
            <h1 className={shared.pageTitle}>Chờ thanh toán</h1>
          </header>
          <StateView
            kind="error"
            title="Chưa thể hiển thị thông tin thanh toán."
            message={initialError}
            actionLabel="Xem đơn hàng"
            actionHref={orderDetailHref}
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

  return (
    <div className={shared.page}>
      <div className={styles.stageWrap}>
        <header className={styles.stageHead}>
          <h1 className={`${shared.pageTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {pageTitle}
              </span>
            </span>
          </h1>
          <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {pageLead}
          </p>
        </header>

        <section className={styles.sheet}>
          {checkoutWarnings.length > 0 && (
            <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="status">
              {checkoutWarnings.map((w) => (
                <p key={w}>{w}</p>
              ))}
            </div>
          )}

          {paidRedirecting && (
            <div className={`${shared.feedbackBox} ${shared.feedbackSuccess}`} role="status">
              Thanh toán đã được xác nhận. Đang chuyển tới chi tiết đơn hàng…
            </div>
          )}

          {snapshot && (
            <div className={styles.allocRow}>
              <span>{isPaid ? "Tổng đã thanh toán" : "Tổng cần thanh toán"}</span>
              <strong className="num">{formatVnd(snapshot.amount_vnd)}</strong>
            </div>
          )}

          <RetryPaymentBox
            paymentId={activePaymentId}
            initialPayment={snapshot}
            hideWaitingPageLink
            onPaid={handlePaid}
            onPaymentReplaced={handlePaymentReplaced}
            onSwitchedToCod={handleSwitchedToCod}
          />

          {snapshot?.shop_allocations && snapshot.shop_allocations.length > 0 && (
            <div className={styles.allocSection}>
              <h3 className={styles.allocHeading}>Đơn hàng</h3>
              {snapshot.shop_allocations.map((alloc) => (
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
                  <span className="num">{formatVnd(computeShopAllocationPayableVnd(alloc))}</span>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
