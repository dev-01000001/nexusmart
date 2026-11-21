"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type OrderStatus,
  type StatusTone,
} from "@/data/orders";
import type { Product } from "@/data/products";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrdersClientView.module.css";
import type { OrderView } from "./orderView";

type FilterKey = "all" | "cho_thanh_toan" | OrderStatus;

const FILTER_TABS: Array<{ key: FilterKey; label: string }> = [
  { key: "all", label: "Tất cả" },
  { key: "cho_thanh_toan", label: "Chờ thanh toán" },
  { key: "cho_xac_nhan", label: "Chờ xác nhận" },
  { key: "dang_chuan_bi", label: "Đang chuẩn bị" },
  { key: "dang_giao", label: "Đang giao" },
  { key: "da_giao", label: "Đã giao" },
  { key: "yeu_cau_tra_hang", label: "Đổi trả" },
  { key: "da_huy", label: "Đã hủy" },
];

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function isUnpaidVietQr(order: OrderView): boolean {
  return (
    order.paymentMethod === "vietqr" &&
    (order.paymentStatus === "cho_thanh_toan" || order.paymentStatus === "thanh_toan_that_bai") &&
    order.status !== "da_huy"
  );
}

export interface OrdersClientViewProps {
  brandName: string;
  initialOrders: OrderView[];
  totalOrders: number;
  suggestedProducts: Product[];
  initialError?: string | null;
}

function OrdersClientInner({
  brandName,
  initialOrders,
  totalOrders,
  suggestedProducts,
  initialError = null,
}: OrdersClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { addToCart, showToast } = useStore();
  const [activeTab, setActiveTab] = useState<FilterKey>("all");
  const [reorderingId, setReorderingId] = useState<string | null>(null);

  useRevealOnScroll(`${viewState ?? "live"}-${activeTab}-${initialOrders.length}`);

  const counts = useMemo(() => {
    const map: Record<FilterKey, number> = {
      all: initialOrders.length,
      cho_thanh_toan: 0,
      cho_xac_nhan: 0,
      dang_chuan_bi: 0,
      dang_giao: 0,
      da_giao: 0,
      yeu_cau_tra_hang: 0,
      da_huy: 0,
    };
    for (const order of initialOrders) {
      map[order.status] += 1;
      if (isUnpaidVietQr(order)) {
        map.cho_thanh_toan += 1;
      }
    }
    return map;
  }, [initialOrders]);

  const filteredOrders = useMemo(() => {
    if (activeTab === "all") return initialOrders;
    if (activeTab === "cho_thanh_toan") {
      return initialOrders.filter(isUnpaidVietQr);
    }
    return initialOrders.filter((o) => o.status === activeTab);
  }, [initialOrders, activeTab]);

  const inProgressCount = initialOrders.filter(
    (o) => o.status === "cho_xac_nhan" || o.status === "dang_chuan_bi" || o.status === "dang_giao"
  ).length;
  const pendingPayCount = initialOrders.filter(isUnpaidVietQr).length;

  const handleReorder = async (order: OrderView) => {
    setReorderingId(order.id);
    let added = 0;
    try {
      for (const item of order.items) {
        const skuId = order.itemSkuIds[item.skuCode];
        if (!skuId) continue;
        const res = await addToCart({
          skuId,
          quantity: item.quantity,
          productName: item.productName,
          size: item.size || undefined,
        });
        if (res.unauthenticated) {
          router.push("/dang-nhap?next=%2Fdon-hang");
          return;
        }
        if (res.ok) added += 1;
      }
      if (added > 0) {
        showToast(`Đã thêm lại ${added} sản phẩm từ đơn ${order.orderCode} vào giỏ hàng.`);
        router.push("/gio-hang");
      } else {
        showToast("Sản phẩm trong đơn hiện không còn sẵn hàng để mua lại.");
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
              Đơn hàng tại {brandName}
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
          <span className={`${styles.statVal} num`}>{totalOrders} đơn</span>
        </div>
        <div className={styles.statItem}>
          <span className={styles.statLabel}>Đang xử lý</span>
          <span className={`${styles.statVal} num`}>{inProgressCount} đơn</span>
        </div>
        {pendingPayCount > 0 && (
          <div className={styles.statItem}>
            <span className={styles.statLabel}>Chờ thanh toán</span>
            <span className={`${styles.statVal} num`}>{pendingPayCount} đơn</span>
          </div>
        )}
        <Button variant="secondary" size="md" href="/tai-khoan">
          Tài khoản
        </Button>
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader(`Đang cập nhật lịch sử đơn hàng của bạn tại ${brandName}.`)}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <StateView kind="loading" title="Đang tải danh sách đơn hàng..." rows={5} />
        </section>
        {suggestedProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm của ${brandName}`} products={suggestedProducts} />
        )}
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối đến lịch sử đơn hàng lúc này.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tải đơn hàng</h2>
          <StateView
            kind="error"
            title="Chưa tải được danh sách đơn hàng"
            message={initialError ?? "Kết nối đang tạm gián đoạn. Vui lòng thử lại sau ít giây."}
            actionLabel="Tải lại trang"
            actionHref="/don-hang"
          />
        </section>
        {suggestedProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm của ${brandName}`} products={suggestedProducts} />
        )}
      </div>
    );
  }

  if (viewState === "empty" || initialOrders.length === 0) {
    return (
      <div className={shared.page}>
        {renderHeader(`Bạn chưa có đơn hàng nào tại ${brandName}.`)}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Lịch sử mua hàng</h2>
          <StateView
            kind="empty"
            title="Chưa có đơn hàng nào"
            message={`Mọi đơn hàng bạn đặt tại ${brandName} sẽ được lưu và theo dõi từng chặng tại đây.`}
            actionLabel="Xem sản phẩm cửa hàng"
            actionHref="/san-pham"
          />
        </section>
        {suggestedProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={suggestedProducts} />
        )}
      </div>
    );
  }

  return (
    <div className={shared.page}>
      {renderHeader(
        `Theo dõi hành trình giao hàng, hoàn tất thanh toán VietQR hoặc gửi yêu cầu đổi trả cho các đơn đặt tại ${brandName}.`
      )}

      <section className={shared.section}>
        <div className={shared.sectionHead}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <p className={shared.sectionNote}>
            Hiển thị <strong className="num">{filteredOrders.length}</strong> /{" "}
            <span className="num">{initialOrders.length}</span> đơn
          </p>
        </div>

        <div
          className={styles.filterControls}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.tabBar} role="tablist" aria-label="Lọc trạng thái đơn hàng">
            {FILTER_TABS.map((tab) => {
              const isActive = activeTab === tab.key;
              return (
                <button
                  key={tab.key}
                  type="button"
                  role="tab"
                  aria-selected={isActive}
                  className={`${styles.tabBtn} ${isActive ? styles.tabBtnActive : ""}`}
                  onClick={() => setActiveTab(tab.key)}
                >
                  <span>{tab.label}</span>
                  <span className={`${styles.tabCount} num`}>{counts[tab.key]}</span>
                </button>
              );
            })}
          </div>
        </div>

        {filteredOrders.length === 0 ? (
          <StateView
            kind="empty"
            title="Không có đơn hàng khớp bộ lọc hiện tại"
            message="Đặt lại bộ lọc để xem toàn bộ lịch sử mua hàng của bạn."
            actionLabel="Xem tất cả đơn hàng"
            onAction={() => setActiveTab("all")}
          />
        ) : (
          <div className={styles.orderList}>
            {filteredOrders.map((order, idx) => {
              const meta = ORDER_STATUS_META[order.status];
              const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
              const pendingQr = isUnpaidVietQr(order);

              return (
                <article
                  key={order.id}
                  className={`${styles.orderCard} ${pendingQr ? styles.orderCardPending : ""}`}
                  data-rise=""
                  style={{ "--i": Math.min(idx + 3, 7) } as React.CSSProperties}
                >
                  <div className={styles.orderTop}>
                    <div className={styles.orderMetaLeft}>
                      <Link href={`/don-hang/${order.id}`} className={`${styles.orderCode} num`}>
                        {order.orderCode}
                      </Link>
                      <span className={styles.shopLink}>{brandName}</span>
                      {order.createdAt && (
                        <span className={`${styles.orderDate} num`}>
                          {formatDateTimeVi(order.createdAt)}
                        </span>
                      )}
                    </div>

                    <div className={styles.orderMetaRight}>
                      <span className={styles.payNote}>
                        {PAYMENT_METHOD_LABELS[order.paymentMethod]}
                      </span>
                      {payMeta && (
                        <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
                          {payMeta.label}
                        </span>
                      )}
                      <span className={`${shared.statusTag} ${toneClass(meta.tone)}`}>
                        {meta.label}
                      </span>
                    </div>
                  </div>

                  <div className={styles.itemsWrap}>
                    {order.items.map((item) => {
                      const productHref = `/san-pham/${item.productSlug || item.productId}`;
                      return (
                        <div key={item.skuCode} className={styles.itemLine}>
                          <Link href={productHref} className={styles.itemThumbLink}>
                            <img
                              src={item.image || "/products/ao-thun-cotton-trang.png"}
                              alt={item.productName}
                              width={896}
                              height={1200}
                              loading="lazy"
                              className={styles.itemThumb}
                            />
                          </Link>
                          <div className={styles.itemDetails}>
                            <Link href={productHref} className={styles.itemTitle}>
                              {item.productName}
                            </Link>
                            <span className={styles.itemVariant}>
                              {item.colorName}
                              {item.size ? ` · Size ${item.size}` : ""} · SL{" "}
                              <span className="num">{item.quantity}</span> ×{" "}
                              <span className="num">{formatVnd(item.unitPrice)}</span>
                            </span>
                          </div>
                          <span className={`${styles.itemSubtotal} num`}>
                            {formatVnd(item.lineTotal)}
                          </span>
                        </div>
                      );
                    })}
                  </div>

                  <div className={styles.orderFoot}>
                    <div className={styles.totalCluster}>
                      <span className={styles.totalLabel}>Tổng thanh toán</span>
                      <span className={`${styles.totalValue} num`}>{formatVnd(order.total)}</span>
                      {order.trackingCode && (
                        <span className={`${styles.trackingCode} num`}>
                          {order.carrier ? `${order.carrier} · ` : ""}Vận đơn: {order.trackingCode}
                        </span>
                      )}
                    </div>

                    <div className={styles.actionRow}>
                      {pendingQr && order.paymentId && (
                        <Button
                          variant="primary"
                          size="sm"
                          href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}`}
                        >
                          Thanh toán VietQR
                        </Button>
                      )}
                      <Button variant="ink" size="sm" href={`/don-hang/${order.id}`}>
                        Xem chi tiết
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={reorderingId === order.id}
                        onClick={() => void handleReorder(order)}
                      >
                        {reorderingId === order.id ? "Đang thêm..." : "Mua lại"}
                      </Button>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </section>

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip
          title={`Mua thêm từ ${brandName}`}
          subtitle="Sản phẩm sẵn hàng tại kho của cửa hàng"
          products={suggestedProducts}
        />
      )}
    </div>
  );
}

export function OrdersClientView(props: OrdersClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải danh sách đơn hàng..." rows={5} />
        </div>
      }
    >
      <OrdersClientInner {...props} />
    </Suspense>
  );
}
