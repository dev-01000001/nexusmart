"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type OrderStatus,
} from "@/data/orders";
import type { Product } from "@/data/products";
import { formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { renderOrderDateTime, statusToneClass } from "@/features/commerce/orderDisplay";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrdersClientView.module.css";
import type { OrderView } from "./orderView";

type FilterKey = "all" | "cho_thanh_toan" | "da_tra_hang" | OrderStatus;

const FILTER_TABS: Array<{ key: FilterKey; label: string }> = [
  { key: "all", label: "Tất cả" },
  { key: "cho_thanh_toan", label: "Chờ thanh toán" },
  { key: "cho_xac_nhan", label: "Chờ xác nhận" },
  { key: "dang_chuan_bi", label: "Đang chuẩn bị" },
  { key: "dang_giao", label: "Đang giao" },
  { key: "da_giao", label: "Đã giao" },
  { key: "yeu_cau_tra_hang", label: "Yêu cầu trả hàng" },
  { key: "da_tra_hang", label: "Đã trả hàng" },
  { key: "da_huy", label: "Đã hủy" },
];

function isUnpaidOnlineOrder(order: OrderView): boolean {
  return (
    (order.paymentMethod === "vietqr" || order.paymentMethod === "vi_sandbox") &&
    (order.paymentStatus === "cho_thanh_toan" || order.paymentStatus === "thanh_toan_that_bai") &&
    order.status !== "da_huy" &&
    order.paymentStatusRaw !== "EXPIRED"
  );
}

export interface OrdersClientViewProps {
  brandName: string;
  initialOrders: OrderView[];
  suggestedProducts: Product[];
  initialError?: string | null;
}

function OrdersClientInner({
  brandName,
  initialOrders,
  suggestedProducts,
  initialError = null,
}: OrdersClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { addToCart, showToast } = useStore();
  const [activeTab, setActiveTab] = useState<FilterKey>("all");
  const [reorderingId, setReorderingId] = useState<string | null>(null);

  const tabBarRef = useRef<HTMLDivElement | null>(null);
  const tabRefs = useRef<Partial<Record<FilterKey, HTMLButtonElement | null>>>({});
  const [indicator, setIndicator] = useState<{ left: number; width: number }>({
    left: 0,
    width: 0,
  });

  const measureIndicator = useCallback(() => {
    const activeBtn = tabRefs.current[activeTab];
    if (!activeBtn) return;
    setIndicator({ left: activeBtn.offsetLeft, width: activeBtn.offsetWidth });
  }, [activeTab]);

  useEffect(() => {
    measureIndicator();
    const bar = tabBarRef.current;
    if (!bar || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => measureIndicator());
    ro.observe(bar);
    return () => ro.disconnect();
  }, [measureIndicator]);

  const counts = useMemo(() => {
    const map: Record<FilterKey, number> = {
      all: initialOrders.length,
      cho_thanh_toan: 0,
      cho_xac_nhan: 0,
      dang_chuan_bi: 0,
      dang_giao: 0,
      da_giao: 0,
      yeu_cau_tra_hang: 0,
      da_tra_hang: 0,
      da_huy: 0,
    };
    for (const order of initialOrders) {
      if (order.backendStatus === "RETURNED") {
        map.da_tra_hang += 1;
      } else {
        map[order.status] += 1;
      }
      if (isUnpaidOnlineOrder(order)) {
        map.cho_thanh_toan += 1;
      }
    }
    return map;
  }, [initialOrders]);

  const filteredOrders = useMemo(() => {
    if (activeTab === "all") return initialOrders;
    if (activeTab === "cho_thanh_toan") {
      return initialOrders.filter(isUnpaidOnlineOrder);
    }
    if (activeTab === "da_tra_hang") {
      return initialOrders.filter((o) => o.backendStatus === "RETURNED");
    }
    if (activeTab === "yeu_cau_tra_hang") {
      return initialOrders.filter((o) => o.backendStatus === "RETURN_REQUESTED");
    }
    return initialOrders.filter((o) => o.status === activeTab);
  }, [initialOrders, activeTab]);

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
        showToast(`Đã thêm ${added} sản phẩm vào giỏ hàng.`);
        router.push("/gio-hang");
      } else {
        showToast("Sản phẩm trong đơn hiện hết hàng.");
      }
    } finally {
      setReorderingId(null);
    }
  };

  const renderHeader = () => (
    <section className={styles.ordersHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${styles.ordersTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Đơn hàng <span className={styles.ordersShopSuffix}>tại {brandName}</span>
            </span>
          </span>
        </h1>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/tai-khoan">
          Tài khoản
        </Button>
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView kind="loading" skeletonVariant="table" title="Đang tải đơn hàng..." rows={5} />
        </section>
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView
            kind="error"
            title="Chưa tải được danh sách đơn hàng."
            message={initialError ?? "Thử lại sau ít giây."}
            actionLabel="Tải lại"
            actionHref="/don-hang"
          />
        </section>
      </div>
    );
  }

  if (viewState === "empty" || initialOrders.length === 0) {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Chưa có đơn hàng nào."
            actionLabel="Xem sản phẩm"
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
      {renderHeader()}

      <section className={shared.section}>
        <div className={styles.tabRailWrap}>
          <div
            ref={tabBarRef}
            className={styles.tabBar}
            role="tablist"
            aria-label="Lọc trạng thái đơn hàng"
          >
            {FILTER_TABS.map((tab) => {
              const isActive = activeTab === tab.key;
              return (
                <button
                  key={tab.key}
                  ref={(el) => {
                    tabRefs.current[tab.key] = el;
                  }}
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
            {indicator.width > 0 && (
              <span
                className={styles.tabIndicator}
                aria-hidden="true"
                style={{
                  transform: `translateX(${indicator.left}px) scaleX(${indicator.width})`,
                }}
              />
            )}
          </div>
        </div>

        {filteredOrders.length === 0 ? (
          <StateView
            kind="empty"
            title="Không có đơn hàng ở trạng thái này."
            actionLabel="Xem tất cả"
            onAction={() => setActiveTab("all")}
          />
        ) : (
          <div className={styles.orderList} data-reveal="rows">
            {filteredOrders.map((order) => {
              const meta =
                order.backendStatus === "RETURNED"
                  ? { label: "Đã trả hàng", tone: "neutral" as const }
                  : ORDER_STATUS_META[order.status];
              const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
              const pendingOnline = isUnpaidOnlineOrder(order);
              const visibleThumbs = order.items.slice(0, 3);
              const extraCount = Math.max(0, order.items.length - visibleThumbs.length);

              return (
                <article
                  key={order.id}
                  className={`${styles.orderCard} ${pendingOnline ? styles.orderCardPending : ""}`}
                >
                  <div className={styles.thumbStrip}>
                    {visibleThumbs.map((item) => (
                      <Link
                        key={item.skuCode}
                        href={`/don-hang/${order.id}`}
                        className={`${styles.thumbLink} ${
                          visibleThumbs.length > 1 ? styles.thumbLinkSmall : ""
                        }`}
                      >
                        <img
                          src={item.image || "/products/ao-thun-cotton-trang.png"}
                          alt={item.productName}
                          width={896}
                          height={1200}
                          loading="lazy"
                          className={styles.thumbImg}
                        />
                      </Link>
                    ))}
                    {extraCount > 0 && (
                      <span className={`${styles.thumbMore} num`}>+{extraCount}</span>
                    )}
                  </div>

                  <div className={styles.orderBody}>
                    <div className={styles.orderHeadRow}>
                      <Link href={`/don-hang/${order.id}`} className={`${styles.orderCode} num`}>
                        {order.orderCode}
                      </Link>
                      <span className={`${shared.statusTag} ${statusToneClass(meta.tone)}`}>
                        {meta.label}
                      </span>
                      {payMeta && (
                        <span className={`${shared.statusTag} ${statusToneClass(payMeta.tone)}`}>
                          {payMeta.label}
                        </span>
                      )}
                      {order.createdAt && (
                        <span className={styles.orderDate}>
                          {renderOrderDateTime(order.createdAt)}
                        </span>
                      )}
                    </div>

                    <div className={styles.itemSummaryList}>
                      {order.items.map((item) => (
                        <div key={item.skuCode} className={styles.itemSummaryLine}>
                          <span className={styles.itemSummaryName}>{item.productName}</span>
                          <span className={styles.itemSummaryMeta}>
                            <span>{item.colorName}</span>
                            {item.size && <span>Size {item.size}</span>}
                            <span className="num">×{item.quantity}</span>
                          </span>
                        </div>
                      ))}
                    </div>

                    <div className={styles.metaFootLine}>
                      <span>{PAYMENT_METHOD_LABELS[order.paymentMethod]}</span>
                      {order.carrier && <span>{order.carrier}</span>}
                      {order.trackingCode && <span className="num">{order.trackingCode}</span>}
                    </div>
                  </div>

                  <div className={styles.orderAside}>
                    <span className={`${styles.totalVal} num`}>{formatVnd(order.total)}</span>
                    <div className={styles.actionRow}>
                      {pendingOnline && order.paymentId && (
                        <Button
                          variant="primary"
                          size="sm"
                          href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}?order=${encodeURIComponent(order.id)}`}
                        >
                          {order.paymentMethod === "vi_sandbox"
                            ? "Thanh toán ZaloPay"
                            : "Thanh toán VietQR"}
                        </Button>
                      )}
                      <Button variant="ink" size="sm" href={`/don-hang/${order.id}`}>
                        Xem chi tiết
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        loading={reorderingId === order.id}
                        loadingLabel="Đang thêm"
                        onClick={() => void handleReorder(order)}
                      >
                        Mua lại
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
        <ProductAddonStrip title={`Mua thêm từ ${brandName}`} products={suggestedProducts} />
      )}
    </div>
  );
}

export function OrdersClientView(props: OrdersClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" skeletonVariant="table" title="Đang tải đơn hàng..." rows={5} />
        </div>
      }
    >
      <OrdersClientInner {...props} />
    </Suspense>
  );
}
