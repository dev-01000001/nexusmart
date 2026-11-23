"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { GatewayOrder, GatewayPayment, GatewayProduct } from "@/lib/gateway-types";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { Button, StateView } from "@/components";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import { CheckoutSuggestionStrip } from "@/features/checkout/CheckoutSuggestionStrip";
import {
  clearCheckoutWarnings,
  computeShopAllocationPayableVnd,
  readCheckoutWarnings,
} from "@/features/checkout/helpers";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

export interface PaymentWaitingClientProps {
  initialPayment: GatewayPayment | null;
  initialOrders: GatewayOrder[];
  initialError?: string | null;
  suggestedProducts: GatewayProduct[];
  shopNameById: Record<string, string>;
}

export function PaymentWaitingClient({
  initialPayment,
  initialOrders,
  initialError,
  suggestedProducts,
  shopNameById,
}: PaymentWaitingClientProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const [payment, setPayment] = useState<GatewayPayment | null>(initialPayment);
  const [paidConfirmed, setPaidConfirmed] = useState<boolean>(
    (initialPayment?.status ?? "").toUpperCase() === "SUCCEEDED"
  );
  const [checkoutWarnings, setCheckoutWarnings] = useState<string[]>([]);

  useEffect(() => {
    const paymentId = payment?.payment_id;
    const orderIds = initialOrders.map((o) => o.id).filter(Boolean);
    if (!paymentId && orderIds.length === 0) return;
    const found = readCheckoutWarnings({ paymentId, orderIds });
    if (found.length > 0) {
      setCheckoutWarnings(found);
    }
  }, [payment?.payment_id, initialOrders]);

  useRevealOnScroll(`${viewState ?? "live"}-${payment?.payment_id ?? "none"}-${paidConfirmed}`);

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <nav className={shared.breadcrumbNav} aria-label="Đường dẫn">
          <Link href="/" className={shared.breadcrumbLink}>
            Trang chủ
          </Link>
          <span aria-hidden="true">/</span>
          <Link href="/don-hang" className={shared.breadcrumbLink}>
            Đơn hàng của tôi
          </Link>
          <span aria-hidden="true">/</span>
          <span>Thanh toán VietQR</span>
        </nav>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              {paidConfirmed ? "Thanh toán thành công" : "Chờ thanh toán VietQR"}
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/don-hang">
          Xem danh sách đơn hàng
        </Button>
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang tải mã chuyển khoản VietQR và trạng thái xác nhận đơn hàng.")}
        <StateView kind="loading" title="Đang kết nối cổng thanh toán..." rows={4} />
      </div>
    );
  }

  if (viewState === "error" || initialError || !payment) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể tải thông tin thanh toán lúc này. Chưa có khoản tiền nào bị trừ.")}
        <StateView
          kind="error"
          title="Chưa thể hiển thị giao dịch thanh toán"
          message={
            initialError ??
            "Hệ thống thanh toán đang tạm gián đoạn. Vui lòng thử tải lại hoặc kiểm tra trong mục Đơn hàng của tôi."
          }
          actionLabel="Tải lại trang"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        {renderHeader("Không có giao dịch chờ thanh toán nào đang mở.")}
        <StateView
          kind="empty"
          title="Chưa có giao dịch cần thanh toán"
          message="Bạn có thể xem lại toàn bộ đơn hàng đã đặt hoặc tiếp tục mua sắm."
          actionLabel="Xem đơn hàng của tôi"
          actionHref="/don-hang"
        />
      </div>
    );
  }

  const primaryOrderHref =
    initialOrders.length === 1
      ? `/don-hang/${encodeURIComponent(initialOrders[0].id)}`
      : "/don-hang";

  return (
    <div className={shared.page}>
      {renderHeader(
        paidConfirmed
          ? "Giao dịch chuyển khoản đã được xác nhận. Các gian hàng đang chuẩn bị đơn cho bạn."
          : "Quét mã VietQR bằng ứng dụng ngân hàng để hoàn tất đơn hàng. Trạng thái sẽ tự cập nhật ngay sau khi chuyển khoản."
      )}

      <section className={shared.section}>
        {checkoutWarnings.length > 0 && (
          <div
            className={`${shared.feedbackBox} ${shared.feedbackWarn} ${shared.noticeSpacer}`}
            role="status"
          >
            <div>
              <strong>Lưu ý về ưu đãi khi tạo đơn:</strong>
              {checkoutWarnings.map((w, idx) => (
                <p key={idx} className={shared.noticeItem}>
                  {w}
                </p>
              ))}
              <p className={shared.noticeSubtle}>
                Số tiền thanh toán bên dưới là giá cuối cùng đã được hệ thống xác nhận trên đơn
                hàng.
              </p>
            </div>
            <div className={shared.formActions}>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  clearCheckoutWarnings();
                  setCheckoutWarnings([]);
                }}
              >
                Đã hiểu
              </Button>
            </div>
          </div>
        )}
        <div className={styles.waitGrid}>
          <div
            className={styles.mainSheet}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <RetryPaymentBox
              paymentId={payment.payment_id}
              initialPayment={payment}
              hideWaitingPageLink
              onPaid={() => setPaidConfirmed(true)}
              onPaymentReplaced={(next) => {
                setPayment(next);
                setPaidConfirmed(next.status.toUpperCase() === "SUCCEEDED");
                router.replace(`/thanh-toan/cho-thanh-toan/${encodeURIComponent(next.payment_id)}`);
              }}
            />

            <div className={styles.stepsList}>
              <h2 className={styles.stepsTitle}>Hướng dẫn hoàn tất chuyển khoản</h2>
              <p className={styles.stepItem}>
                Mở ứng dụng ngân hàng trên điện thoại, chọn tính năng quét mã QR và kiểm tra đúng số
                tiền cùng nội dung chuyển khoản bắt buộc.
              </p>
              <p className={styles.stepItem}>
                Giữ nguyên trang này trong vài giây sau khi chuyển khoản; đơn hàng sẽ tự động chuyển
                sang trạng thái Đã xác nhận mà không cần tải lại thủ công.
              </p>
              <p className={styles.stepItem}>
                Nếu mã VietQR hết hạn trước khi bạn kịp chuyển khoản, đơn hàng sẽ tự động hủy và
                hoàn lại tồn kho; bạn hãy quay lại giỏ hàng hoặc trang sản phẩm để đặt lại đơn mới.
              </p>
            </div>
          </div>

          <aside
            className={styles.asideSheet}
            aria-label="Thông tin đơn hàng cần thanh toán"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.asideInner}>
              <h2 className={styles.asideHeading}>
                Đơn hàng trong giao dịch (
                {initialOrders.length || payment.shop_allocations.length || 1})
              </h2>

              {initialOrders.length > 0 ? (
                <div className={styles.orderList}>
                  {initialOrders.map((order) => (
                    <div key={order.id} className={styles.orderRow}>
                      <div>
                        <Link
                          href={`/don-hang/${encodeURIComponent(order.id)}`}
                          className={`${styles.orderLink} num`}
                        >
                          {order.order_code}
                        </Link>
                        <p className={styles.orderMeta}>
                          {order.items.reduce((sum, it) => sum + it.quantity, 0)} sản phẩm
                          {order.created_at
                            ? ` · Đặt lúc ${formatDateTimeVi(order.created_at)}`
                            : ""}
                        </p>
                      </div>
                      <span className={`${styles.orderTotal} num`}>
                        {formatVnd(order.total_payable_vnd)}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className={styles.orderList}>
                  {payment.shop_allocations.map((alloc) => (
                    <div key={alloc.order_id} className={styles.orderRow}>
                      <Link
                        href={`/don-hang/${encodeURIComponent(alloc.order_id)}`}
                        className={styles.orderLink}
                      >
                        Xem chi tiết đơn hàng
                      </Link>
                      <span className={`${styles.orderTotal} num`}>
                        {formatVnd(computeShopAllocationPayableVnd(alloc))}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className={styles.darkBand}>
              <div className={styles.totalRow}>
                <span className={styles.totalLabel}>Tổng cần thanh toán</span>
                <span className={`${styles.totalVal} num`}>{formatVnd(payment.amount_vnd)}</span>
              </div>
              <p className={styles.darkNote}>
                {paidConfirmed
                  ? "Cảm ơn bạn đã đặt hàng tại NexusMart. Bạn có thể theo dõi hành trình đóng gói và giao hàng ngay bây giờ."
                  : "Sản phẩm trong đơn đang được giữ riêng cho bạn trong thời gian chờ chuyển khoản."}
              </p>
              <Button variant="primary" size="md" fullWidth href={primaryOrderHref}>
                {initialOrders.length === 1 ? "Mở chi tiết đơn hàng" : "Về danh sách đơn hàng"}
              </Button>
            </div>
          </aside>
        </div>
      </section>

      <CheckoutSuggestionStrip
        title="Tiếp tục khám phá tại NexusMart"
        subtitle="Sản phẩm cơ bản chuẩn chất liệu từ các xưởng dệt Việt Nam."
        products={suggestedProducts}
        shopNameById={shopNameById}
        showQuickAdd={false}
      />
    </div>
  );
}
