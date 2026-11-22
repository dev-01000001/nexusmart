"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCTS } from "@/data";
import type { GatewayOrder, GatewayOrderStatus, GatewayPublicShop } from "@/lib/gateway-types";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPackage, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/don-hang/page.module.css";
import type { OrderVisualLookup } from "./types";
import {
  ORDER_FILTER_TABS,
  type BadgeTone,
  getCarrierLabel,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
  needsVietQrPayment,
} from "./orderMeta";

export interface OrdersListClientProps {
  orders: GatewayOrder[];
  total: number;
  allOrdersTotal: number;
  page: number;
  pageSize: number;
  activeStatus: "" | GatewayOrderStatus;
  shopsMap: Record<string, GatewayPublicShop>;
  visuals: OrderVisualLookup;
  initialError?: string | null;
  forcedState?: "loading" | "empty" | "error" | null;
}

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function buildOrdersHref(status: string, page: number): string {
  const params = new URLSearchParams();
  if (status) params.set("status", status);
  if (page > 1) params.set("page", String(page));
  const qs = params.toString();
  return qs ? `/don-hang?${qs}` : "/don-hang";
}

export function OrdersListClient({
  orders,
  total,
  allOrdersTotal,
  page,
  pageSize,
  activeStatus,
  shopsMap,
  visuals,
  initialError,
  forcedState,
}: OrdersListClientProps) {
  const router = useRouter();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();
  const [retryingOrderId, setRetryingOrderId] = useState<string | null>(null);
  const [reorderingId, setReorderingId] = useState<string | null>(null);

  useRevealOnScroll(`${forcedState ?? "live"}-${activeStatus}-${page}-${orders.length}`);

  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const suggestedProducts = useMemo(() => PRODUCTS.slice(0, 4), []);

  const inProgressCount = useMemo(
    () =>
      orders.filter(
        (o) =>
          o.status === "PENDING" ||
          o.status === "CONFIRMED" ||
          o.status === "PREPARING" ||
          o.status === "SHIPPING"
      ).length,
    [orders]
  );

  const pendingPayCount = useMemo(() => orders.filter(needsVietQrPayment).length, [orders]);

  const handleReorder = async (order: GatewayOrder) => {
    if (reorderingId || order.items.length === 0) return;
    setReorderingId(order.id);
    let added = 0;
    let clamped = false;
    let lastError = "";

    try {
      for (const item of order.items) {
        const res = await addToCart({
          skuId: item.sku_id,
          quantity: item.quantity,
        });
        if (res.ok) {
          added += 1;
          if (res.clampedToStock) clamped = true;
        } else if (res.message) {
          lastError = res.message;
        }
      }

      if (added > 0) {
        showToast(
          clamped
            ? `Đã thêm ${added} sản phẩm từ đơn ${order.order_code} vào giỏ (đã điều chỉnh theo số lượng còn sẵn).`
            : `Đã thêm lại ${added} sản phẩm từ đơn ${order.order_code} vào giỏ hàng.`
        );
        router.push("/gio-hang");
      } else {
        showToast(lastError || "Các sản phẩm trong đơn này hiện không còn sẵn để mua lại.");
      }
    } finally {
      setReorderingId(null);
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Đơn hàng của tôi
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={styles.summaryStat} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <div className={styles.statItem}>
          <span className={styles.statLabel}>Tổng số đơn</span>
          <span className={`${styles.statVal} num`}>{allOrdersTotal} đơn</span>
        </div>
        {inProgressCount > 0 && (
          <div className={styles.statItem}>
            <span className={styles.statLabel}>Đang xử lý</span>
            <span className={`${styles.statVal} num`}>{inProgressCount} đơn</span>
          </div>
        )}
        {pendingPayCount > 0 && (
          <div className={styles.statItem}>
            <span className={styles.statLabel}>Chờ thanh toán</span>
            <span className={`${styles.statVal} num`}>{pendingPayCount} đơn</span>
          </div>
        )}
        <Button variant="secondary" size="md" href="/tra-cuu-don-hang">
          Tra cứu bằng mã đơn
        </Button>
      </div>
    </section>
  );

  if (forcedState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật lịch sử đơn hàng và trạng thái giao nhận của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <StateView kind="loading" title="Đang tải danh sách đơn hàng..." rows={5} />
        </section>
        <ProductAddonStrip title="Sản phẩm cơ bản nổi bật" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "error" || initialError) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối đến lịch sử đơn hàng lúc này.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tải đơn hàng</h2>
          <StateView
            kind="error"
            title="Chưa tải được danh sách đơn hàng"
            message={initialError || "Kết nối đang tạm gián đoạn. Vui lòng thử lại sau ít giây."}
            actionLabel="Tải lại trang"
            onAction={() => router.refresh()}
          />
        </section>
        <ProductAddonStrip title="Gợi ý xem nhanh danh mục" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "empty" || (allOrdersTotal === 0 && !activeStatus)) {
    return (
      <div className={shared.page}>
        {renderHeader("Bạn chưa có đơn hàng nào. Khám phá đồ cơ bản chuẩn chất liệu để bắt đầu.")}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Lịch sử mua hàng</h2>
          <StateView
            kind="empty"
            title="Chưa có đơn hàng nào"
            message="Mọi đơn đặt từ các xưởng dệt Việt Nam trên NexusMart sẽ được lưu và theo dõi từng chặng tại đây."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        <ProductAddonStrip title="Gợi ý cho đơn hàng đầu tiên" products={suggestedProducts} />
      </div>
    );
  }

  return (
    <div className={shared.page}>
      {renderHeader(
        "Theo dõi hành trình giao hàng, hoàn tất thanh toán hoặc gửi yêu cầu trả hàng trực tiếp."
      )}

      <section className={shared.section}>
        <div className={shared.sectionHead}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <p className={shared.sectionNote}>
            Hiển thị <strong className="num">{orders.length}</strong> /{" "}
            <span className="num">{total}</span> đơn
            {totalPages > 1 && (
              <>
                {" "}
                · Trang <span className="num">{page}</span>/
                <span className="num">{totalPages}</span>
              </>
            )}
          </p>
        </div>

        <div
          className={styles.filterControls}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.tabBar} role="tablist" aria-label="Lọc trạng thái đơn hàng">
            {ORDER_FILTER_TABS.map((tab) => {
              const isActive = activeStatus === tab.key;
              return (
                <Link
                  key={tab.key || "all"}
                  href={buildOrdersHref(tab.key, 1)}
                  role="tab"
                  aria-selected={isActive}
                  className={`${styles.tabBtn} ${isActive ? styles.tabBtnActive : ""}`}
                >
                  <span>{tab.label}</span>
                </Link>
              );
            })}
          </div>
        </div>

        {orders.length === 0 ? (
          <StateView
            kind="empty"
            title="Không có đơn hàng khớp trạng thái đã chọn"
            message="Chuyển về mục Tất cả để xem toàn bộ lịch sử mua hàng của bạn."
            actionLabel="Xem tất cả đơn hàng"
            actionHref="/don-hang"
          />
        ) : (
          <>
            <div className={styles.orderList}>
              {orders.map((order, idx) => {
                const statusMeta = getOrderStatusInfo(order.status);
                const payMeta = getPaymentStatusInfo(
                  order.payment_status,
                  order.payment_method,
                  order.status
                );
                const shop = shopsMap[order.shop_id];
                const canRetryPay = needsVietQrPayment(order);
                const showRetryForm = retryingOrderId === order.id;

                return (
                  <article
                    key={order.id}
                    className={`${styles.orderCard} ${canRetryPay ? styles.orderCardPending : ""}`}
                    data-rise=""
                    style={{ "--i": Math.min(idx + 3, 7) } as React.CSSProperties}
                  >
                    <div className={styles.orderTop}>
                      <div className={styles.orderMetaLeft}>
                        <Link href={`/don-hang/${order.id}`} className={`${styles.orderCode} num`}>
                          {order.order_code}
                        </Link>
                        {shop?.slug ? (
                          <Link href={`/gian-hang/${shop.slug}`} className={styles.shopLink}>
                            {shop.name}
                          </Link>
                        ) : (
                          <span className={styles.shopLink}>
                            {shop?.name ?? "Gian hàng NexusMart"}
                          </span>
                        )}
                        {order.created_at && (
                          <span className={`${styles.orderDate} num`}>
                            {formatDateTimeVi(order.created_at)}
                          </span>
                        )}
                      </div>

                      <div className={styles.orderMetaRight}>
                        <span className={styles.payNote}>
                          {getPaymentMethodLabel(order.payment_method)}
                        </span>
                        <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
                          {payMeta.label}
                        </span>
                        <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
                          {statusMeta.label}
                        </span>
                      </div>
                    </div>

                    <div className={styles.itemsWrap}>
                      {order.items.map((item) => {
                        const prodVisual = visuals.byProductId[item.product_id];
                        const thumbUrl = visuals.bySkuId[item.sku_id] ?? prodVisual?.image;
                        const productHref = prodVisual?.slug
                          ? `/san-pham/${prodVisual.slug}`
                          : `/don-hang/${order.id}`;

                        return (
                          <div key={item.id || item.sku_id} className={styles.itemLine}>
                            <Link href={productHref} className={styles.itemThumbLink}>
                              {thumbUrl ? (
                                <img
                                  src={thumbUrl}
                                  alt={item.product_name}
                                  width={896}
                                  height={1200}
                                  loading="lazy"
                                  className={styles.itemThumb}
                                />
                              ) : (
                                <span className={styles.itemThumbPlaceholder} aria-hidden="true">
                                  <IconPackage size={24} />
                                </span>
                              )}
                            </Link>
                            <div className={styles.itemDetails}>
                              <Link href={productHref} className={styles.itemTitle}>
                                {item.product_name}
                              </Link>
                              <span className={styles.itemVariant}>
                                {item.sku_name ? `${item.sku_name} · ` : ""}SL{" "}
                                <span className="num">{item.quantity}</span> ×{" "}
                                <span className="num">
                                  {formatVnd(item.effective_unit_price_vnd || item.unit_price_vnd)}
                                </span>
                              </span>
                            </div>
                            <span className={`${styles.itemSubtotal} num`}>
                              {formatVnd(item.line_total_vnd)}
                            </span>
                          </div>
                        );
                      })}
                    </div>

                    {canRetryPay && showRetryForm && (
                      <div className={styles.retryWrap}>
                        <RetryPaymentBox
                          paymentId={order.payment_id}
                          onPaid={() => {
                            setRetryingOrderId(null);
                            showToast(`Đơn ${order.order_code} đã được ghi nhận thanh toán.`);
                            router.refresh();
                          }}
                        />
                      </div>
                    )}

                    <div className={styles.orderFoot}>
                      <div className={styles.totalCluster}>
                        <span className={styles.totalLabel}>Tổng thanh toán</span>
                        <span className={`${styles.totalValue} num`}>
                          {formatVnd(order.total_payable_vnd)}
                        </span>
                        {order.tracking_code && (
                          <span className={`${styles.trackingCode} num`}>
                            {order.carrier_code ? `${getCarrierLabel(order.carrier_code)} · ` : ""}
                            Vận đơn: {order.tracking_code}
                          </span>
                        )}
                      </div>

                      <div className={styles.actionRow}>
                        {canRetryPay && (
                          <>
                            <Button
                              variant="primary"
                              size="sm"
                              onClick={() => setRetryingOrderId(showRetryForm ? null : order.id)}
                            >
                              {showRetryForm ? "Ẩn thanh toán" : "Thanh toán lại"}
                            </Button>
                            <Button
                              variant="secondary"
                              size="sm"
                              href={`/thanh-toan/cho-thanh-toan/${order.payment_id}`}
                            >
                              Mở trang chờ VietQR
                            </Button>
                          </>
                        )}
                        <Button variant="ink" size="sm" href={`/don-hang/${order.id}`}>
                          Xem chi tiết
                        </Button>
                        {order.items.length > 0 && (
                          <Button
                            variant="secondary"
                            size="sm"
                            disabled={reorderingId === order.id}
                            onClick={() => void handleReorder(order)}
                          >
                            {reorderingId === order.id ? "Đang thêm..." : "Mua lại"}
                          </Button>
                        )}
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>

            {totalPages > 1 && (
              <nav className={styles.paginationBar} aria-label="Phân trang danh sách đơn hàng">
                <span className={`${styles.paginationInfo} num`}>
                  Trang {page} / {totalPages} ({total} đơn hàng)
                </span>
                <div className={styles.paginationControls}>
                  {page > 1 ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      href={buildOrdersHref(activeStatus, page - 1)}
                    >
                      Trang trước
                    </Button>
                  ) : (
                    <Button variant="secondary" size="sm" disabled>
                      Trang trước
                    </Button>
                  )}
                  {page < totalPages ? (
                    <Button variant="ink" size="sm" href={buildOrdersHref(activeStatus, page + 1)}>
                      Trang sau
                    </Button>
                  ) : (
                    <Button variant="secondary" size="sm" disabled>
                      Trang sau
                    </Button>
                  )}
                </div>
              </nav>
            )}
          </>
        )}
      </section>

      <ProductAddonStrip
        title="Mua thêm đồ cơ bản từ xưởng dệt"
        subtitle="Chuẩn chất liệu, giao nhanh toàn quốc và theo dõi trực tuyến"
        products={suggestedProducts}
      />
    </div>
  );
}
