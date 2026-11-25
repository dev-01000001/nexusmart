"use client";

import React, { Suspense, useMemo, useState } from "react";
import { PRODUCTS } from "@/data";
import { MEDIA_BANNERS } from "@/data/media";
import { formatCarrierLabel, formatDateTimeVi, formatVnd } from "@/lib/format";
import {
  ORDER_LOOKUP_NOT_FOUND_MESSAGE,
  type OrderLookupResponse,
  type PublicOrderSummary,
  formatLookupRateLimitMessage,
  formatPublicOrderArea,
  translateOrderLookupError,
} from "@/lib/a8-types";
import {
  type BadgeTone,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
} from "@/features/account/orderMeta";
import { Button, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderLookupView.module.css";

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

type LookupResultState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "found"; order: PublicOrderSummary }
  | { kind: "not_found"; message: string }
  | { kind: "rate_limited"; message: string }
  | { kind: "error"; message: string };

function OrderLookupContent() {
  const viewState = usePageStateParam();

  const [orderCode, setOrderCode] = useState("");
  const [phone, setPhone] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [lookupState, setLookupState] = useState<LookupResultState>({ kind: "idle" });

  const suggestedProducts = useMemo(() => PRODUCTS.slice(2, 6), []);

  const foundOrder = lookupState.kind === "found" ? lookupState.order : null;

  useRevealOnScroll(
    `${viewState ?? "live"}-${lookupState.kind}-${foundOrder?.order_code ?? "none"}-${foundOrder?.status ?? ""}`
  );

  const runLookup = async (code: string, recipientPhone: string) => {
    setLookupState({ kind: "loading" });
    try {
      const res = await fetch("/api/gw/orders/lookup", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          order_code: code,
          phone: recipientPhone,
        }),
      });

      if (res.status === 404) {
        setLookupState({
          kind: "not_found",
          message: ORDER_LOOKUP_NOT_FOUND_MESSAGE,
        });
        return;
      }

      if (res.status === 429) {
        setLookupState({
          kind: "rate_limited",
          message: formatLookupRateLimitMessage(res.headers.get("retry-after")),
        });
        return;
      }

      if (!res.ok) {
        let errorCode: string | null = null;
        try {
          const errBody = (await res.json()) as { error?: { code?: string } } | null;
          if (typeof errBody?.error?.code === "string") {
            errorCode = errBody.error.code;
          }
        } catch {
          // Keep status-based Vietnamese fallback
        }
        setLookupState({
          kind: "error",
          message: translateOrderLookupError(res.status, errorCode, res.headers.get("retry-after")),
        });
        return;
      }

      const data = (await res.json()) as Partial<OrderLookupResponse> | null;
      if (!data?.order?.order_code) {
        setLookupState({
          kind: "not_found",
          message: ORDER_LOOKUP_NOT_FOUND_MESSAGE,
        });
        return;
      }
      setLookupState({ kind: "found", order: data.order });
    } catch {
      setLookupState({
        kind: "error",
        message: "Kết nối đến hệ thống tra cứu đơn hàng đang tạm gián đoạn. Vui lòng thử lại.",
      });
    }
  };

  const handleLookupSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = orderCode.trim();
    const digits = phone.replace(/\D/g, "");
    if (cleanCode.length < 3) {
      setFormError("Vui lòng nhập mã đơn hàng nhận được trong email hoặc mục Đơn hàng của tôi.");
      return;
    }
    if (digits.length < 8) {
      setFormError(
        "Vui lòng nhập số điện thoại người nhận đã dùng khi đặt hàng (tối thiểu 8 chữ số)."
      );
      return;
    }
    setFormError(null);
    void runLookup(cleanCode, phone.trim());
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

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang kiểm tra hành trình giao hàng và trạng thái thanh toán của đơn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Kết quả tra cứu</h2>
          <StateView kind="loading" title="Đang tìm thông tin đơn hàng..." rows={4} />
        </section>
        <ProductAddonStrip title="Đồ cơ bản bán chạy tại NexusMart" products={suggestedProducts} />
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối dịch vụ tra cứu vận đơn lúc này.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="error"
            title="Chưa thể tra cứu đơn hàng lúc này"
            message="Vui lòng thử lại sau ít giây hoặc gọi Hotline 1900 6845 (8:30 - 22:00) để được hỗ trợ ngay."
            actionLabel="Thử lại"
            actionHref="/tra-cuu-don-hang"
          />
        </section>
        <ProductAddonStrip title="Đồ cơ bản nổi bật" products={suggestedProducts} />
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        {renderHeader("Nhập mã đơn và số điện thoại đặt hàng để kiểm tra không cần đăng nhập.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Chưa có mã đơn được tra</h2>
          <StateView
            kind="empty"
            title="Chưa nhập mã đơn cần tra cứu"
            message="Nhập mã đơn hàng trong email xác nhận kèm số điện thoại người nhận để xem tiến trình giao hàng."
            actionLabel="Mở tra cứu đơn"
            actionHref="/tra-cuu-don-hang"
          />
        </section>
        <ProductAddonStrip title="Gợi ý sản phẩm cơ bản" products={suggestedProducts} />
      </div>
    );
  }

  const statusMeta = foundOrder ? getOrderStatusInfo(foundOrder.status) : null;
  const payMeta = foundOrder
    ? getPaymentStatusInfo(foundOrder.payment_status, foundOrder.payment_method, foundOrder.status)
    : null;
  const sortedTimeline = foundOrder
    ? [...(foundOrder.timeline ?? [])].sort((a, b) => {
        const ta = a.created_at ? Date.parse(a.created_at) : 0;
        const tb = b.created_at ? Date.parse(b.created_at) : 0;
        return tb - ta;
      })
    : [];
  const isSubmitting = lookupState.kind === "loading";

  return (
    <div className={shared.page}>
      {renderHeader(
        "Nhập mã đơn hàng và số điện thoại người nhận để theo dõi tiến trình giao nhận mà không cần đăng nhập."
      )}

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
                      autoComplete="off"
                      disabled={isSubmitting}
                    />
                  </div>
                  <div className={`${shared.field} ${shared.formFull}`}>
                    <label htmlFor="lookup-phone" className={shared.fieldLabel}>
                      Số điện thoại người nhận
                    </label>
                    <input
                      id="lookup-phone"
                      type="tel"
                      className={shared.input}
                      value={phone}
                      onChange={(e) => setPhone(e.target.value)}
                      placeholder="Ví dụ: 0912345678"
                      autoComplete="tel"
                      disabled={isSubmitting}
                    />
                  </div>
                </div>

                {formError && (
                  <p className={shared.fieldError} role="alert" style={{ marginTop: 10 }}>
                    {formError}
                  </p>
                )}

                <div className={shared.formActions} style={{ marginTop: 16 }}>
                  <Button
                    type="submit"
                    variant="primary"
                    size="md"
                    fullWidth
                    disabled={isSubmitting}
                  >
                    {isSubmitting ? "Đang tra cứu..." : "Tra cứu đơn hàng"}
                  </Button>
                </div>
              </form>
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
                  Để bảo vệ quyền riêng tư, trang tra cứu công khai chỉ hiển thị thông tin người
                  nhận đã che và khu vực giao hàng (Phường/Xã, Tỉnh/Thành phố).
                </p>
                <p className={styles.bodyCopy}>
                  Cần sửa địa chỉ, hủy đơn, thanh toán lại hoặc yêu cầu trả hàng? Vui lòng đăng nhập
                  vào mục <strong>Đơn hàng của tôi</strong>.
                </p>
              </div>
            </div>
          </div>

          <div
            className={styles.resultCol}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            {lookupState.kind === "idle" && (
              <StateView
                kind="empty"
                title="Nhập mã đơn và số điện thoại để bắt đầu"
                message="Hệ thống sẽ tra cứu trực tiếp trạng thái xử lý, mã vận đơn và lịch sử giao hàng của đơn hàng."
                actionLabel="Mở đơn hàng của tôi"
                actionHref="/don-hang"
              />
            )}

            {lookupState.kind === "loading" && (
              <StateView kind="loading" title="Đang tra cứu thông tin đơn hàng..." rows={4} />
            )}

            {lookupState.kind === "not_found" && (
              <StateView
                kind="empty"
                title="Không tìm thấy đơn hàng"
                message={lookupState.message}
                actionLabel="Đăng nhập xem đơn của tôi"
                actionHref="/don-hang"
              />
            )}

            {lookupState.kind === "rate_limited" && (
              <StateView
                kind="error"
                title="Tạm giới hạn lượt tra cứu"
                message={lookupState.message}
                actionLabel="Đăng nhập xem đơn của tôi"
                actionHref="/don-hang"
              />
            )}

            {lookupState.kind === "error" && (
              <StateView
                kind="error"
                title="Chưa thể tra cứu đơn hàng"
                message={lookupState.message}
                actionLabel="Thử lại"
                onAction={() => {
                  const cleanCode = orderCode.trim();
                  if (cleanCode && phone.replace(/\D/g, "").length >= 8) {
                    void runLookup(cleanCode, phone.trim());
                  }
                }}
              />
            )}

            {foundOrder && statusMeta && payMeta && (
              <div className={styles.resultCard}>
                <div className={styles.resultBanner}>
                  <div className={styles.resultTitleGroup}>
                    <h2 className={`${styles.resultOrderCode} num`}>{foundOrder.order_code}</h2>
                    <p className={styles.resultBannerMeta}>
                      {foundOrder.created_at
                        ? `Đặt lúc ${formatDateTimeVi(foundOrder.created_at)}`
                        : "Thông tin đơn hàng trên NexusMart"}
                    </p>
                    <div className={styles.badgeGroup}>
                      <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
                        {payMeta.label}
                      </span>
                      <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
                        {statusMeta.label}
                      </span>
                    </div>
                  </div>
                  <div className={styles.resultTotalCluster}>
                    <span className={styles.resultBannerMeta}>Tổng thanh toán</span>
                    <span className={`${styles.resultTotalVal} num`}>
                      {formatVnd(foundOrder.total_payable_vnd)}
                    </span>
                  </div>
                </div>

                <div className={styles.sheetBody}>
                  <div className={styles.sheetSectionFirst}>
                    <h3 className={styles.sheetHeading}>Thông tin giao nhận</h3>
                    <dl className={styles.infoGrid}>
                      <div className={styles.infoTile}>
                        <dt className={styles.infoKey}>Người nhận &amp; khu vực</dt>
                        <dd className={styles.infoVal}>
                          {[foundOrder.recipient_name_masked, foundOrder.recipient_phone_masked]
                            .filter(Boolean)
                            .join(" · ") || "Thông tin đã được che"}
                        </dd>
                        <dd className={styles.infoSub}>
                          {formatPublicOrderArea(foundOrder.ward_name, foundOrder.province_name)}
                        </dd>
                      </div>

                      <div className={styles.infoTile}>
                        <dt className={styles.infoKey}>Đơn vị vận chuyển</dt>
                        <dd className={`${styles.infoVal} num`}>
                          {foundOrder.carrier_code
                            ? formatCarrierLabel(foundOrder.carrier_code)
                            : "Đang sắp xếp vận chuyển"}
                        </dd>
                        <dd className={`${styles.infoSub} num`}>
                          {foundOrder.tracking_code
                            ? `Mã vận đơn: ${foundOrder.tracking_code}`
                            : "Chưa có mã vận đơn"}
                        </dd>
                      </div>

                      <div className={styles.infoTile}>
                        <dt className={styles.infoKey}>Thanh toán</dt>
                        <dd className={styles.infoVal}>
                          {getPaymentMethodLabel(foundOrder.payment_method)}
                        </dd>
                        <dd className={`${styles.infoSub} num`}>
                          Tiền hàng: {formatVnd(foundOrder.subtotal_vnd)} · Phí giao hàng:{" "}
                          {formatVnd(foundOrder.shipping_fee_vnd)}
                        </dd>
                      </div>

                      <div className={styles.infoTile}>
                        <dt className={styles.infoKey}>Cập nhật gần nhất</dt>
                        <dd className={`${styles.infoVal} num`}>
                          {foundOrder.updated_at
                            ? formatDateTimeVi(foundOrder.updated_at)
                            : statusMeta.label}
                        </dd>
                        <dd className={styles.infoSub}>
                          {foundOrder.delivered_at
                            ? `Giao thành công lúc ${formatDateTimeVi(foundOrder.delivered_at)}`
                            : statusMeta.description}
                        </dd>
                      </div>
                    </dl>
                  </div>

                  {Array.isArray(foundOrder.items) && foundOrder.items.length > 0 && (
                    <div className={styles.sheetSection}>
                      <h3 className={styles.sheetHeading}>Sản phẩm trong đơn</h3>
                      <div className={styles.itemList}>
                        {foundOrder.items.map((item, idx) => (
                          <div
                            key={`${item.product_name}-${item.sku_name}-${idx}`}
                            className={styles.itemRow}
                            style={{ gridTemplateColumns: "minmax(0, 1fr) auto" }}
                          >
                            <div className={styles.itemInfo}>
                              <span className={styles.itemName}>{item.product_name}</span>
                              <span className={styles.itemMeta}>
                                {item.sku_name ? `${item.sku_name} · ` : ""}SL {item.quantity}
                              </span>
                            </div>
                            <span className={`${styles.itemPrice} num`}>
                              {formatVnd(item.line_total_vnd)}
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  <div className={styles.sheetSection}>
                    <h3 className={styles.sheetHeading}>Hành trình đơn hàng</h3>
                    {sortedTimeline.length > 0 ? (
                      <ol className={styles.timelineList}>
                        {sortedTimeline.map((ev, i) => {
                          const evMeta = getOrderStatusInfo(ev.to_status);
                          return (
                            <li
                              key={`${ev.to_status}-${ev.created_at ?? i}-${i}`}
                              className={`${styles.timelineRow} ${
                                i === 0 ? styles.timelineRowLatest : ""
                              }`}
                            >
                              <span className={`${styles.timelineTime} num`}>
                                {ev.created_at ? formatDateTimeVi(ev.created_at) : "—"}
                              </span>
                              <div>
                                <p className={styles.infoVal}>{evMeta.label}</p>
                              </div>
                            </li>
                          );
                        })}
                      </ol>
                    ) : (
                      <p className={styles.bodyCopy}>
                        Chưa có mốc cập nhật hành trình cho đơn này.
                      </p>
                    )}
                  </div>

                  <div className={styles.guestActionBlock}>
                    <h3 className={styles.sheetHeading}>Thao tác với đơn hàng</h3>
                    <p className={styles.bodyCopy}>
                      Để thay đổi địa chỉ nhận hàng, hủy đơn, thanh toán lại hoặc gửi yêu cầu trả
                      hàng, vui lòng đăng nhập tài khoản đặt hàng của bạn.
                    </p>
                    <div className={shared.formActions}>
                      <Button variant="primary" size="sm" href="/don-hang">
                        Đăng nhập để quản lý đơn hàng
                      </Button>
                      <Button variant="secondary" size="sm" href="/tro-giup">
                        Trung tâm trợ giúp
                      </Button>
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      </section>

      <ProductAddonStrip
        title="Đồ cơ bản bán chạy tại NexusMart"
        subtitle="Cotton Compact 220gsm, Dry-Fit 145gsm và Linen pha Cotton chuẩn xưởng"
        products={suggestedProducts}
      />
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
