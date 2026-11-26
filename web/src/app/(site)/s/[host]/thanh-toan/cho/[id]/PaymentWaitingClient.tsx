"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AlertTriangle, CheckCircle2, ExternalLink } from "lucide-react";
import type { GatewayOrder, GatewayPayment } from "@/lib/gateway-types";
import { formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { SitePageFrame, SitePageHeader } from "@/features/commerce/ui/SitePageFrame";
import { computeShopAllocationPayableVnd, readCheckoutWarnings } from "@/features/checkout/helpers";
import type { GwPayment } from "@/features/commerce/types";

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
      <SitePageFrame narrow>
        <SitePageHeader title="Chờ thanh toán" subtitle={brandName} centered />
        <div className="space-y-4">
          <ErrorState
            title="Chưa thể hiển thị thông tin thanh toán."
            description={initialError}
            retryHref={orderDetailHref}
            className="border border-border shadow-none"
          />
          <div className="flex justify-center">
            <Button asChild variant="outline" className="min-h-10 shadow-none">
              <Link href="/don-hang">Đơn hàng của tôi</Link>
            </Button>
          </div>
        </div>
      </SitePageFrame>
    );
  }

  return (
    <SitePageFrame narrow>
      <SitePageHeader title={pageTitle} subtitle={pageLead} centered />

      <Card className="border border-border shadow-none">
        <CardContent className="space-y-5">
          {checkoutWarnings.length > 0 && (
            <Alert
              className="border-badge-warning-ink/25 bg-badge-warning-bg text-badge-warning-ink shadow-none"
              role="status"
            >
              <AlertTriangle className="size-4" aria-hidden="true" />
              <AlertDescription className="space-y-1 text-xs text-badge-warning-ink">
                {checkoutWarnings.map((w) => (
                  <p key={w}>{w}</p>
                ))}
              </AlertDescription>
            </Alert>
          )}

          {paidRedirecting && (
            <Alert
              className="border-badge-success-ink/25 bg-badge-success-bg text-badge-success-ink shadow-none"
              role="status"
            >
              <CheckCircle2 className="size-4" aria-hidden="true" />
              <AlertDescription className="text-badge-success-ink">
                Thanh toán đã được xác nhận. Đang chuyển tới chi tiết đơn hàng…
              </AlertDescription>
            </Alert>
          )}

          {snapshot && (
            <div className="flex flex-wrap items-baseline justify-between gap-2 rounded-lg border border-border bg-muted/40 px-4 py-3">
              <span className="text-sm text-muted-foreground">
                {isPaid ? "Tổng đã thanh toán" : "Tổng cần thanh toán"}
              </span>
              <Money vnd={snapshot.amount_vnd} size="lg" />
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
            <div className="space-y-3 pt-1">
              <Separator />
              <h2 className="text-sm font-semibold text-foreground">Đơn hàng</h2>
              <div className="space-y-2">
                {snapshot.shop_allocations.map((alloc) => (
                  <div
                    key={alloc.order_id}
                    className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-background px-3.5 py-2.5 text-sm"
                  >
                    <div className="flex flex-wrap items-center gap-3">
                      <span className="font-medium text-foreground">{brandName}</span>
                      <Button asChild variant="link" size="sm" className="h-auto px-0 text-brand">
                        <Link href={`/don-hang/${encodeURIComponent(alloc.order_id)}`}>
                          <span>Mở đơn</span>
                          <ExternalLink className="size-3.5" aria-hidden="true" />
                        </Link>
                      </Button>
                    </div>
                    <span className="font-semibold text-foreground tabular-nums">
                      {formatVnd(computeShopAllocationPayableVnd(alloc))}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </CardContent>
      </Card>
    </SitePageFrame>
  );
}
