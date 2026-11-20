"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ORDER_STATUS_META, PAYMENT_METHOD_LABELS, PAYMENT_STATUS_META, PRODUCTS } from "@/data";
import type { OrderStatus, Product, Shop, StatusTone } from "@/data";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, IconChat, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/don-hang/page.module.css";
import type { OrderView } from "./orderView";

type FilterKey = "all" | "cho_thanh_toan" | OrderStatus;

const FILTER_TABS: Array<{ key: FilterKey; label: string }> = [
  { key: "all", label: "Tất cả" },
  { key: "cho_thanh_toan", label: "Chờ thanh toán" },
  { key: "cho_xac_nhan", label: "Chờ xác nhận" },
  { key: "dang_chuan_bi", label: "Đang chuẩn bị" },
  { key: "dang_giao", label: "Đang giao" },
  { key: "da_giao", label: "Đã giao" },
  { key: "yeu_cau_tra_hang", label: "Đổi trả 45 ngày" },
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
  initialOrders: OrderView[];
  totalOrders: number;
  shops: Shop[];
  suggestedProducts: Product[];
  initialError?: string | null;
}

function OrdersClientInner({
  initialOrders,
  totalOrders,
  shops,
  suggestedProducts,
  initialError = null,
}: OrdersClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { openChat, addToCart, showToast } = useStore();
  const [activeTab, setActiveTab] = useState<FilterKey>("all");
  const [selectedShopId, setSelectedShopId] = useState<string>("all");
  const [reorderingId, setReorderingId] = useState<string | null>(null);

  useRevealOnScroll(
    `${viewState ?? "live"}-${activeTab}-${selectedShopId}-${initialOrders.length}`
  );

  // Count how many sub-orders belong to each checkout group so multi-shop orders are clearly labeled.
  const groupCounts = useMemo(() => {
    const map = new Map<string, number>();
    for (const o of initialOrders) {
      if (!o.checkoutGroupId) continue;
      map.set(o.checkoutGroupId, (map.get(o.checkoutGroupId) ?? 0) + 1);
    }
    return map;
  }, [initialOrders]);

  const filterShops = useMemo(() => {
    const seen = new Map<string, { id: string; label: string }>();
    for (const sh of shops) {
      const backendKey = sh.backendId ?? sh.id;
      seen.set(backendKey, { id: backendKey, label: sh.shortName || sh.name });
    }
    for (const o of initialOrders) {
      if (!seen.has(o.shopId)) {
        seen.set(o.shopId, { id: o.shopId, label: o.shopName });
      }
    }
    return [...seen.values()];
  }, [shops, initialOrders]);

  const shopScopedOrders = useMemo(
    () =>
      selectedShopId === "all"
        ? initialOrders
        : initialOrders.filter((o) => o.shopId === selectedShopId),
    [initialOrders, selectedShopId]
  );

  const counts = useMemo(() => {
    const map: Record<FilterKey, number> = {
      all: shopScopedOrders.length,
      cho_thanh_toan: 0,
      cho_xac_nhan: 0,
      dang_chuan_bi: 0,
      dang_giao: 0,
      da_giao: 0,
      yeu_cau_tra_hang: 0,
      da_huy: 0,
    };
    for (const order of shopScopedOrders) {
      map[order.status] += 1;
      if (isUnpaidVietQr(order)) {
        map.cho_thanh_toan += 1;
      }
    }
    return map;
  }, [shopScopedOrders]);

  const filteredOrders = useMemo(() => {
    if (activeTab === "all") return shopScopedOrders;
    if (activeTab === "cho_thanh_toan") {
      return shopScopedOrders.filter(isUnpaidVietQr);
    }
    return shopScopedOrders.filter((o) => o.status === activeTab);
  }, [shopScopedOrders, activeTab]);

  const inProgressCount = initialOrders.filter(
    (o) => o.status === "cho_xac_nhan" || o.status === "dang_chuan_bi" || o.status === "dang_giao"
  ).length;
  const pendingPayCount = initialOrders.filter(isUnpaidVietQr).length;

  const addonProducts = suggestedProducts.length > 0 ? suggestedProducts : PRODUCTS.slice(0, 4);

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
        {renderHeader("Đang cập nhật lịch sử đơn hàng và trạng thái giao nhận của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách đơn hàng</h2>
          <StateView kind="loading" title="Đang tải danh sách đơn hàng..." rows={5} />
        </section>
        <ProductAddonStrip title="Sản phẩm cơ bản nổi bật" products={addonProducts} />
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
        <ProductAddonStrip title="Gợi ý xem nhanh danh mục" products={addonProducts} />
      </div>
    );
  }

  if (viewState === "empty" || initialOrders.length === 0) {
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
        <ProductAddonStrip title="Gợi ý cho đơn hàng đầu tiên" products={addonProducts} />
      </div>
    );
  }

  return (
    <div className={shared.page}>
      {renderHeader(
        "Theo dõi hành trình giao hàng, hoàn tất thanh toán VietQR hoặc quản lý đổi trả trong 45 ngày."
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

          {filterShops.length > 0 && (
            <div className={styles.shopFilterBar} role="group" aria-label="Lọc theo gian hàng">
              <span className={styles.shopFilterLabel}>Gian hàng:</span>
              <button
                type="button"
                className={`${styles.shopChip} ${selectedShopId === "all" ? styles.shopChipActive : ""}`}
                onClick={() => setSelectedShopId("all")}
              >
                Tất cả xưởng
              </button>
              {filterShops.map((shop) => (
                <button
                  key={shop.id}
                  type="button"
                  className={`${styles.shopChip} ${
                    selectedShopId === shop.id ? styles.shopChipActive : ""
                  }`}
                  onClick={() => setSelectedShopId(shop.id)}
                >
                  {shop.label}
                </button>
              ))}
            </div>
          )}
        </div>

        {filteredOrders.length === 0 ? (
          <StateView
            kind="empty"
            title="Không có đơn hàng khớp bộ lọc hiện tại"
            message="Đặt lại bộ lọc để xem toàn bộ lịch sử mua hàng của bạn."
            actionLabel="Xem tất cả đơn hàng"
            onAction={() => {
              setActiveTab("all");
              setSelectedShopId("all");
            }}
          />
        ) : (
          <div className={styles.orderList}>
            {filteredOrders.map((order, idx) => {
              const meta = ORDER_STATUS_META[order.status];
              const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
              const pendingQr = isUnpaidVietQr(order);
              const groupSize = order.checkoutGroupId
                ? (groupCounts.get(order.checkoutGroupId) ?? 1)
                : 1;

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
                      {order.shopSlug ? (
                        <Link href={`/gian-hang/${order.shopSlug}`} className={styles.shopLink}>
                          {order.shopName}
                        </Link>
                      ) : (
                        <span className={styles.shopLink}>{order.shopName}</span>
                      )}
                      {groupSize > 1 && (
                        <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                          Đơn gộp {groupSize} gian hàng
                        </span>
                      )}
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
                      <Button variant="secondary" size="sm" onClick={() => openChat(order.shopId)}>
                        <IconChat size={14} />
                        <span>Chat xưởng</span>
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

      <ProductAddonStrip
        title="Mua thêm đồ cơ bản từ xưởng dệt"
        subtitle="Chuẩn chất liệu, đổi trả 45 ngày và giao nhanh toàn quốc"
        products={addonProducts}
      />
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
