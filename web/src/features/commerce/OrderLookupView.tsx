"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ORDER_STATUS_META, PAYMENT_METHOD_LABELS, PAYMENT_STATUS_META, PRODUCTS } from "@/data";
import { MEDIA_BANNERS } from "@/data/media";
import type { StatusTone } from "@/data";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import type { OrderView } from "@/features/account/orderView";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button, IconChat, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderLookupView.module.css";

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function digitsOnly(phone: string): string {
  return phone.replace(/\D/g, "");
}

function OrderLookupContent() {
  const { openChat, showToast } = useStore();
  const { isAuthenticated, loading: sessionLoading, user } = useSession();

  const [orders, setOrders] = useState<OrderView[]>([]);
  const [loadingOrders, setLoadingOrders] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [orderCode, setOrderCode] = useState("");
  const [phone, setPhone] = useState("");
  const [searchedKey, setSearchedKey] = useState<{ code: string; phone: string } | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);

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
        setLoadError(
          err instanceof ApiError ? err.message : "Chưa thể tải danh sách đơn hàng lúc này."
        );
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

  const suggestedProducts = useMemo(() => PRODUCTS.slice(2, 6), []);

  useRevealOnScroll(
    `lookup-${isAuthenticated ? "auth" : "guest"}-${foundOrder?.id ?? "none"}-${foundOrder?.status ?? ""}`
  );

  const handleLookupSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = orderCode.trim();
    if (cleanCode.length < 4) {
      setFormError("Vui lòng nhập mã đơn hàng cần tra cứu.");
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
          body: { reason: "Người mua hủy đơn từ trang tra cứu trước khi đóng gói." },
        }
      );
      setOrders((prev) => prev.map((o) => (o.id === orderId ? res.order : o)));
      showToast("Đã hủy đơn hàng thành công.");
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Chưa thể hủy đơn hàng lúc này.");
    } finally {
      setCancelling(false);
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Tra cứu đơn hàng
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/don-hang">
          Đơn hàng của tôi
        </Button>
        <Button variant="secondary" size="md" href="/tro-giup">
          Trung tâm trợ giúp
        </Button>
      </div>
    </section>
  );

  if (sessionLoading || loadingOrders) {
    return (
      <div className={shared.page}>
        {renderHeader("Đang kiểm tra hành trình giao hàng và trạng thái thanh toán.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Kết quả tra cứu</h2>
          <StateView kind="loading" title="Đang tìm thông tin đơn hàng..." rows={4} />
        </section>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={shared.page}>
        {renderHeader("Đăng nhập tài khoản để bảo mật thông tin và theo dõi hành trình đơn hàng.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Tra cứu đơn thuộc tài khoản của bạn</h2>
          <StateView
            kind="empty"
            title="Đăng nhập để xem trạng thái đơn hàng"
            message="Vì lý do bảo mật thông tin người nhận và vận đơn, vui lòng đăng nhập để tra cứu đơn hàng của bạn hoặc gọi Hotline 1900 6845 (8:30 - 22:00)."
            actionLabel="Đăng nhập ngay"
            actionHref="/dang-nhap?next=%2Ftra-cuu-don-hang"
          />
        </section>
        <ProductAddonStrip title="Đồ cơ bản bán chạy tại NexusMart" products={suggestedProducts} />
      </div>
    );
  }

  if (loadError) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối dịch vụ tra cứu vận đơn lúc này.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="error"
            title="Chưa thể tra cứu đơn hàng lúc này"
            message={loadError}
            actionLabel="Mở danh sách đơn hàng"
            actionHref="/don-hang"
          />
        </section>
      </div>
    );
  }

  const statusMeta = foundOrder ? ORDER_STATUS_META[foundOrder.status] : null;
  const payMeta = foundOrder?.paymentStatus ? PAYMENT_STATUS_META[foundOrder.paymentStatus] : null;
  const needsRetry = Boolean(
    foundOrder &&
    (foundOrder.paymentStatus === "cho_thanh_toan" ||
      foundOrder.paymentStatus === "thanh_toan_that_bai")
  );

  return (
    <div className={shared.page}>
      {renderHeader("Nhập mã đơn hàng thuộc tài khoản của bạn để xem hành trình và thanh toán.")}

      <section className={shared.section}>
        <div className={styles.lookupLayout}>
          <div className={styles.lookupCol}>
            <div
              className={styles.lookupPanel}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <h2 className={shared.sectionTitle}>Thông tin tra cứu</h2>

              <form onSubmit={handleLookupSubmit} noValidate>
                <div className={shared.formGrid}>
                  <div className={`${shared.field} ${shared.formFull}`}>
                    <label htmlFor="lookup-code" className={shared.fieldLabel}>
                      Mã đơn hàng
                    </label>
                    <input
                      id="lookup-code"
                      type="text"
                      className={shared.input}
                      value={orderCode}
                      onChange={(e) => setOrderCode(e.target.value)}
                      placeholder="Nhập mã đơn hàng của bạn"
                    />
                  </div>
                  <div className={`${shared.field} ${shared.formFull}`}>
                    <label htmlFor="lookup-phone" className={shared.fieldLabel}>
                      Số điện thoại người nhận (tùy chọn)
                    </label>
                    <input
                      id="lookup-phone"
                      type="tel"
                      className={shared.input}
                      value={phone}
                      onChange={(e) => setPhone(e.target.value)}
                      placeholder="0912 486 305"
                    />
                  </div>
                </div>

                {formError && (
                  <p className={shared.fieldError} role="alert" style={{ marginTop: 10 }}>
                    {formError}
                  </p>
                )}

                <div className={shared.formActions} style={{ marginTop: 16 }}>
                  <Button type="submit" variant="primary" size="md" fullWidth>
                    Tra cứu đơn hàng
                  </Button>
                </div>
              </form>

              {orders.length > 0 && (
                <div className={styles.sampleWrap}>
                  <p className={shared.fieldLabel}>Đơn hàng gần đây của bạn</p>
                  <div className={styles.sampleList}>
                    {orders.slice(0, 4).map((o, idx) => {
                      const isActive = foundOrder?.id === o.id;
                      const meta = ORDER_STATUS_META[o.status];
                      return (
                        <button
                          key={o.id}
                          type="button"
                          data-rise=""
                          style={{ "--i": Math.min(idx + 3, 6) } as React.CSSProperties}
                          className={`${styles.sampleBtn} ${isActive ? styles.sampleBtnActive : ""}`}
                          onClick={() => {
                            setOrderCode(o.orderCode);
                            setPhone(o.buyerPhone || "");
                            setSearchedKey({ code: o.orderCode, phone: o.buyerPhone || "" });
                          }}
                        >
                          <span className={`${styles.sampleCode} num`}>{o.orderCode}</span>
                          <span className={styles.sampleHint}>
                            {meta.label} · {formatVnd(o.total)}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>

            <div className={styles.guideBlock} data-reveal="">
              <img
                src={MEDIA_BANNERS.workshop.src}
                alt={MEDIA_BANNERS.workshop.alt}
                width={MEDIA_BANNERS.workshop.width}
                height={MEDIA_BANNERS.workshop.height}
                className={styles.guidePhoto}
              />
              <div className={styles.guideList}>
                <p className={styles.bodyCopy}>
                  Mọi đơn hàng đều được bảo vệ theo tài khoản người mua và cập nhật trạng thái trực
                  tiếp từ xưởng.
                </p>
                <p className={styles.bodyCopy}>
                  Cần hỗ trợ nhanh về đơn hàng? Gọi Hotline{" "}
                  <strong className="num">1900 6845</strong> (8:30 - 22:00 T2 - CN).
                </p>
              </div>
            </div>
          </div>

          <div
            className={styles.resultCol}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            {!foundOrder ? (
              <StateView
                kind="empty"
                title={
                  orders.length === 0
                    ? "Tài khoản chưa có đơn hàng nào"
                    : "Không tìm thấy mã đơn phù hợp"
                }
                message={
                  orders.length === 0
                    ? "Hãy chọn sản phẩm yêu thích và hoàn tất đặt hàng để theo dõi hành trình tại đây."
                    : "Kiểm tra lại mã đơn hoặc chọn một đơn gần đây ở cột bên trái."
                }
                actionLabel={orders.length === 0 ? "Khám phá sản phẩm" : "Mở danh sách đơn"}
                actionHref={orders.length === 0 ? "/danh-muc" : "/don-hang"}
              />
            ) : (
              <div className={styles.resultCard}>
                <div className={styles.resultBanner}>
                  <div className={styles.resultTitleGroup}>
                    <h2 className={`${styles.resultOrderCode} num`}>{foundOrder.orderCode}</h2>
                    <p className={styles.resultBannerMeta}>
                      {foundOrder.shopName} · Đặt lúc {formatDateTimeVi(foundOrder.createdAt)}
                    </p>
                    <div className={styles.badgeGroup}>
                      {payMeta && (
                        <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
                          {payMeta.label}
                        </span>
                      )}
                      {statusMeta && (
                        <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
                          {statusMeta.label}
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
                    <div data-rise="" style={{ "--i": 4 } as React.CSSProperties}>
                      <RetryPaymentBox order={foundOrder} paymentId={foundOrder.paymentId} />
                    </div>
                  )}

                  <div className={styles.sheetSectionFirst}>
                    <h3 className={styles.sheetHeading}>Thông tin giao nhận</h3>
                    <dl className={styles.infoGrid}>
                      <div className={styles.infoTile} data-reveal="">
                        <dt className={styles.infoKey}>Địa chỉ nhận hàng</dt>
                        <dd className={styles.infoVal}>
                          {foundOrder.buyerName} · {foundOrder.buyerPhone}
                        </dd>
                        <dd className={styles.infoSub}>{foundOrder.shippingAddress}</dd>
                      </div>
                      <div className={styles.infoTile} data-reveal="">
                        <dt className={styles.infoKey}>Vận chuyển</dt>
                        <dd className={`${styles.infoVal} num`}>
                          {foundOrder.carrier ?? "Đơn vị vận chuyển"} ·{" "}
                          {foundOrder.trackingCode ?? "Đang cấp mã vận đơn"}
                        </dd>
                      </div>
                      <div className={styles.infoTile} data-reveal="">
                        <dt className={styles.infoKey}>Phương thức thanh toán</dt>
                        <dd className={styles.infoVal}>
                          {PAYMENT_METHOD_LABELS[foundOrder.paymentMethod]}
                        </dd>
                        <dd className={styles.infoSub}>
                          {foundOrder.isPaid ? "Đã thanh toán" : "Chưa thanh toán / Thu hộ"}
                        </dd>
                      </div>
                    </dl>
                  </div>

                  <div className={styles.sheetSection}>
                    <h3 className={styles.sheetHeading}>Sản phẩm trong đơn</h3>
                    <div className={styles.itemList}>
                      {foundOrder.items.map((item) => (
                        <div key={item.skuCode} className={styles.itemRow} data-reveal="">
                          <img
                            src={item.image || "/products/ao-thun-cotton-trang.png"}
                            alt={item.productName}
                            width={896}
                            height={1200}
                            className={styles.itemImg}
                          />
                          <div className={styles.itemInfo}>
                            <Link
                              href={
                                item.productSlug ? `/san-pham/${item.productSlug}` : "/danh-muc"
                              }
                              className={styles.itemName}
                            >
                              {item.productName}
                            </Link>
                            <span className={styles.itemMeta}>
                              {item.colorName}
                              {item.size ? ` · Size ${item.size}` : ""} · SL {item.quantity}
                            </span>
                          </div>
                          <span className={`${styles.itemPrice} num`}>
                            {formatVnd(item.lineTotal)}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>

                  {foundOrder.timeline.length > 0 && (
                    <div className={styles.sheetSection}>
                      <h3 className={styles.sheetHeading}>Hành trình đơn hàng</h3>
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
                              data-reveal=""
                            >
                              <span className={`${styles.timelineTime} num`}>
                                {formatDateTimeVi(ev.timestamp)}
                              </span>
                              <div>
                                <p className={styles.infoVal}>{ev.title}</p>
                                <p className={styles.bodyCopy}>{ev.note}</p>
                              </div>
                            </li>
                          ))}
                      </ol>
                    </div>
                  )}

                  <div className={shared.formActions}>
                    {foundOrder.canCancel && (
                      <Button
                        variant="danger"
                        size="sm"
                        disabled={cancelling}
                        onClick={() => void handleCancelOrder(foundOrder.id)}
                      >
                        {cancelling ? "Đang hủy..." : `Hủy đơn ${foundOrder.orderCode}`}
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="secondary"
                      size="sm"
                      onClick={() => openChat(foundOrder.shopId)}
                    >
                      <IconChat size={14} />
                      <span>Nhắn tin cho xưởng</span>
                    </Button>
                    <Button variant="secondary" size="sm" href={`/don-hang/${foundOrder.id}`}>
                      Xem chi tiết đầy đủ
                    </Button>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}

export function OrderLookupView() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang mở trang tra cứu đơn hàng..." rows={4} />
        </div>
      }
    >
      <OrderLookupContent />
    </Suspense>
  );
}
