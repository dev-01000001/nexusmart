"use client";

import React, { Suspense, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type StatusTone,
} from "@/data/orders";
import type { Product } from "@/data/products";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import type { OrderView } from "@/features/account/orderView";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { focusAndShakeInvalid } from "@/features/commerce/formFeedback";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderLookupView.module.css";

export interface OrderLookupViewProps {
  brandName?: string;
  suggestedProducts?: Product[];
}

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function digitsOnly(phone: string): string {
  return phone.replace(/\D/g, "");
}

function renderDateTime(iso: string): React.ReactNode {
  const parts = formatDateTimeVi(iso).split(", ");
  if (parts.length !== 2) return iso;
  return (
    <span className={`${styles.dateParts} num`}>
      <span>{parts[0]}</span>
      <span>{parts[1]}</span>
    </span>
  );
}

function OrderLookupContent({ brandName, suggestedProducts = [] }: OrderLookupViewProps) {
  const { showToast } = useStore();
  const { isAuthenticated, loading: sessionLoading, user } = useSession();

  const [orders, setOrders] = useState<OrderView[]>([]);
  const [loadingOrders, setLoadingOrders] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const lookupFormRef = useRef<HTMLFormElement | null>(null);
  const [orderCode, setOrderCode] = useState("");
  const [phone, setPhone] = useState("");
  const [searchedKey, setSearchedKey] = useState<{ code: string; phone: string } | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);
  const [cancelling, setCancelling] = useState(false);

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(lookupFormRef.current);
    }
  }, [errorPulse]);

  const storeLabel = brandName || "cửa hàng";

  useEffect(() => {
    if (user?.phone && !phone) setPhone(user.phone);
  }, [user?.phone, phone]);

  useEffect(() => {
    if (!isAuthenticated) {
      setOrders([]);
      return;
    }
    let active = true;
    setLoadingOrders(true);
    setLoadError(null);
    api<{ items?: OrderView[] }>("/api/orders?page=1&page_size=50")
      .then((res) => {
        if (!active) return;
        const list = res.items ?? [];
        setOrders(list);
        if (list[0]) {
          setOrderCode((prev) => prev || list[0].orderCode);
          setSearchedKey(
            (prev) =>
              prev ?? { code: list[0].orderCode, phone: list[0].buyerPhone || user?.phone || "" }
          );
        }
      })
      .catch((err) => {
        if (!active) return;
        setLoadError(err instanceof ApiError ? err.message : "Chưa tải được danh sách đơn hàng.");
      })
      .finally(() => {
        if (active) setLoadingOrders(false);
      });
    return () => {
      active = false;
    };
  }, [isAuthenticated, user?.phone]);

  const foundOrder = useMemo(() => {
    if (!searchedKey) return undefined;
    const cleanCode = searchedKey.code.trim().toUpperCase();
    const cleanPhone = digitsOnly(searchedKey.phone);
    return orders.find((o) => {
      const codeMatches =
        o.orderCode.toUpperCase() === cleanCode || o.id.toUpperCase() === cleanCode;
      if (!codeMatches) return false;
      if (cleanPhone.length < 8) return true;
      return digitsOnly(o.buyerPhone).endsWith(cleanPhone.slice(-8));
    });
  }, [orders, searchedKey]);

  const handleLookupSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = orderCode.trim();
    if (cleanCode.length < 4) {
      setFormError("Nhập mã đơn hàng cần tra cứu.");
      setErrorPulse((n) => n + 1);
      return;
    }
    setFormError(null);
    setSearchedKey({ code: cleanCode, phone: phone.trim() });
  };

  const handleCancelOrder = async (orderId: string) => {
    if (cancelling) return;
    setCancelling(true);
    try {
      const res = await api<{ order: OrderView }>(
        `/api/orders/${encodeURIComponent(orderId)}/cancel`,
        {
          method: "POST",
          body: { reason: "Người mua hủy đơn từ trang tra cứu." },
        }
      );
      setOrders((prev) => prev.map((o) => (o.id === orderId ? res.order : o)));
      showToast("Đã hủy đơn hàng.");
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Chưa thể hủy đơn hàng lúc này.");
    } finally {
      setCancelling(false);
    }
  };

  const renderHeroHead = (lead: string) => (
    <header className={styles.heroCopy}>
      <h1 className={`${shared.pageTitle} lines`}>
        <span className="line">
          <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
            Tra cứu đơn hàng
          </span>
        </span>
      </h1>
      <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
        {lead}
      </p>
    </header>
  );

  if (sessionLoading || loadingOrders) {
    return (
      <div className={shared.page}>
        <section className={styles.heroStage}>
          {renderHeroHead(storeLabel)}
          <div style={{ width: "100%" }}>
            <StateView
              kind="loading"
              skeletonVariant="detail"
              title="Đang tải đơn hàng..."
              rows={3}
            />
          </div>
        </section>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={shared.page}>
        <section className={styles.heroStage}>
          {renderHeroHead(`Đăng nhập để xem hành trình đơn tại ${storeLabel}.`)}
          <div style={{ width: "100%" }}>
            <StateView
              kind="empty"
              title="Cần đăng nhập tài khoản đã đặt hàng."
              actionLabel="Đăng nhập"
              actionHref="/dang-nhap?next=%2Ftra-cuu-don-hang"
            />
          </div>
        </section>
        {suggestedProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm tại ${storeLabel}`} products={suggestedProducts} />
        )}
      </div>
    );
  }

  if (loadError) {
    return (
      <div className={shared.page}>
        <section className={styles.heroStage}>
          {renderHeroHead(storeLabel)}
          <div style={{ width: "100%" }}>
            <StateView
              kind="error"
              title="Chưa thể tra cứu đơn hàng."
              message={loadError}
              actionLabel="Mở danh sách đơn"
              actionHref="/don-hang"
            />
          </div>
        </section>
      </div>
    );
  }

  const statusMeta = foundOrder ? ORDER_STATUS_META[foundOrder.status] : null;
  const payMeta = foundOrder?.paymentStatus ? PAYMENT_STATUS_META[foundOrder.paymentStatus] : null;
  const needsRetry = Boolean(
    foundOrder &&
    foundOrder.status !== "da_huy" &&
    (foundOrder.paymentStatus === "cho_thanh_toan" ||
      foundOrder.paymentStatus === "thanh_toan_that_bai")
  );

  return (
    <div className={shared.page}>
      <section className={styles.heroStage}>
        {renderHeroHead("Nhập mã đơn và số điện thoại nhận hàng.")}

        <form
          ref={lookupFormRef}
          className={styles.lookupForm}
          onSubmit={handleLookupSubmit}
          noValidate
        >
          <div className={styles.bigInputsRow}>
            <div className={shared.field} data-shake-target="">
              <label htmlFor="lookup-code" className={shared.fieldLabel}>
                Mã đơn hàng
              </label>
              <input
                id="lookup-code"
                name="orderCode"
                type="text"
                autoComplete="off"
                className={`${styles.bigInput} ${formError ? styles.bigInputInvalid : ""}`}
                aria-invalid={Boolean(formError) || undefined}
                aria-describedby={formError ? "lookup-code-error" : undefined}
                value={orderCode}
                onChange={(e) => {
                  setOrderCode(e.target.value);
                  if (formError) setFormError(null);
                }}
                placeholder="VD: NM-2026..."
              />
            </div>

            <div className={shared.field}>
              <label htmlFor="lookup-phone" className={shared.fieldLabel}>
                Số điện thoại (tùy chọn)
              </label>
              <input
                id="lookup-phone"
                name="phone"
                type="tel"
                autoComplete="tel"
                className={styles.bigInput}
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                placeholder="0912 486 305"
              />
            </div>
          </div>

          {formError && (
            <p id="lookup-code-error" className={shared.fieldError} role="alert">
              {formError}
            </p>
          )}

          <Button type="submit" variant="primary" size="lg" fullWidth>
            Tra cứu đơn hàng
          </Button>
        </form>

        {orders.length > 0 && (
          <div className={styles.recentWrap}>
            <span className={styles.recentLabel}>Đơn gần đây</span>
            <div className={styles.recentChips}>
              {orders.slice(0, 4).map((o) => {
                const isActive = foundOrder?.id === o.id;
                const meta = ORDER_STATUS_META[o.status];
                return (
                  <button
                    key={o.id}
                    type="button"
                    className={`${styles.recentChip} ${isActive ? styles.recentChipActive : ""}`}
                    onClick={() => {
                      setOrderCode(o.orderCode);
                      setPhone(o.buyerPhone || "");
                      setFormError(null);
                      setSearchedKey({ code: o.orderCode, phone: o.buyerPhone || "" });
                    }}
                  >
                    <span className="num">{o.orderCode}</span>
                    <span className={styles.recentChipStatus}>{meta.label}</span>
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </section>

      <section className={styles.resultWrap}>
        {!foundOrder ? (
          <StateView
            kind="empty"
            title={
              orders.length === 0
                ? "Tài khoản chưa có đơn hàng nào."
                : "Không tìm thấy mã đơn phù hợp."
            }
            actionLabel={orders.length === 0 ? "Xem sản phẩm" : "Mở danh sách đơn"}
            actionHref={orders.length === 0 ? "/san-pham" : "/don-hang"}
          />
        ) : (
          <div className={styles.resultCard}>
            <div className={styles.resultBanner}>
              <div className={styles.resultTitleGroup}>
                <h2 className={`${styles.resultOrderCode} num`}>{foundOrder.orderCode}</h2>
                <p className={styles.resultBannerMeta}>
                  <span>{foundOrder.shopName}</span>
                  {renderDateTime(foundOrder.createdAt)}
                </p>
                <div className={styles.badgeGroup}>
                  {statusMeta && (
                    <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
                      {statusMeta.label}
                    </span>
                  )}
                  {payMeta && (
                    <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
                      {payMeta.label}
                    </span>
                  )}
                </div>
              </div>
              <div className={styles.resultTotalCluster}>
                <span className={styles.resultBannerMeta}>Tổng thanh toán</span>
                <span className={`${styles.resultTotalVal} num`}>
                  {formatVnd(foundOrder.total)}
                </span>
              </div>
            </div>

            <div className={styles.sheetBody}>
              {needsRetry && (
                <RetryPaymentBox order={foundOrder} paymentId={foundOrder.paymentId} />
              )}

              <div className={styles.sheetSectionFirst}>
                <h3 className={styles.sheetHeading}>Giao nhận & thanh toán</h3>
                <dl className={styles.infoGrid}>
                  <div className={styles.infoTile}>
                    <dt className={styles.infoKey}>Người nhận</dt>
                    <dd className={`${styles.infoVal} ${styles.metaPair}`}>
                      <span>{foundOrder.buyerName}</span>
                      <span className="num">{foundOrder.buyerPhone}</span>
                    </dd>
                    <dd className={styles.infoSub}>{foundOrder.shippingAddress}</dd>
                  </div>
                  <div className={styles.infoTile}>
                    <dt className={styles.infoKey}>Vận chuyển</dt>
                    <dd className={`${styles.infoVal} ${styles.metaPair} num`}>
                      <span>{foundOrder.carrier ?? "Đang xử lý"}</span>
                      {foundOrder.trackingCode && <span>{foundOrder.trackingCode}</span>}
                    </dd>
                  </div>
                  <div className={styles.infoTile}>
                    <dt className={styles.infoKey}>Thanh toán</dt>
                    <dd className={styles.infoVal}>
                      {PAYMENT_METHOD_LABELS[foundOrder.paymentMethod]}
                    </dd>
                  </div>
                </dl>
              </div>

              <div className={styles.sheetSection}>
                <h3 className={styles.sheetHeading}>Sản phẩm</h3>
                <div className={styles.itemList}>
                  {foundOrder.items.map((item) => (
                    <div key={item.skuCode} className={styles.itemRow}>
                      <img
                        src={item.image || "/products/ao-thun-cotton-trang.png"}
                        alt={item.productName}
                        width={896}
                        height={1200}
                        className={styles.itemImg}
                      />
                      <div className={styles.itemInfo}>
                        <Link
                          href={item.productSlug ? `/san-pham/${item.productSlug}` : "/san-pham"}
                          className={styles.itemName}
                        >
                          {item.productName}
                        </Link>
                        <span className={styles.itemMeta}>
                          <span>{item.colorName}</span>
                          {item.size && <span>Size {item.size}</span>}
                          <span className="num">×{item.quantity}</span>
                        </span>
                      </div>
                      <span className={`${styles.itemPrice} num`}>{formatVnd(item.lineTotal)}</span>
                    </div>
                  ))}
                </div>
              </div>

              {foundOrder.timeline.length > 0 && (
                <div className={styles.sheetSection}>
                  <h3 className={styles.sheetHeading}>Hành trình</h3>
                  <ol className={styles.timelineList}>
                    {foundOrder.timeline
                      .slice()
                      .reverse()
                      .map((ev, i) => (
                        <li
                          key={`${ev.timestamp}-${i}`}
                          className={`${styles.timelineRow} ${
                            i === 0 ? styles.timelineRowLatest : ""
                          }`}
                        >
                          <span className={styles.timelineStem} aria-hidden="true">
                            <span className={styles.timelineDot} />
                            <span className={styles.timelineLine} />
                          </span>
                          <span className={styles.timelineTime}>
                            {renderDateTime(ev.timestamp)}
                          </span>
                          <div>
                            <p className={styles.infoVal}>{ev.title}</p>
                            {ev.note && <p className={styles.infoSub}>{ev.note}</p>}
                          </div>
                        </li>
                      ))}
                  </ol>
                </div>
              )}

              <div className={shared.formActions}>
                <Button variant="ink" size="md" href={`/don-hang/${foundOrder.id}`}>
                  Chi tiết đơn hàng
                </Button>
                {foundOrder.canCancel && (
                  <Button
                    variant="danger"
                    size="md"
                    loading={cancelling}
                    loadingLabel="Đang hủy"
                    onClick={() => void handleCancelOrder(foundOrder.id)}
                  >
                    Hủy đơn
                  </Button>
                )}
              </div>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}

export function OrderLookupView(props: OrderLookupViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" skeletonVariant="detail" title="Đang mở tra cứu..." rows={3} />
        </div>
      }
    >
      <OrderLookupContent {...props} />
    </Suspense>
  );
}
