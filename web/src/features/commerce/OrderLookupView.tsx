"use client";

import React, { Suspense, useEffect, useRef, useState } from "react";
import type { Product } from "@/data/products";
import { api } from "@/lib/api";
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
import type { OrderView } from "@/features/account/orderView";
import { useSession } from "@/components/SessionProvider";
import { Button, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { focusAndShakeInvalid } from "@/features/commerce/formFeedback";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderLookupView.module.css";

export interface OrderLookupViewProps {
  brandName?: string;
  suggestedProducts?: Product[];
}

type LookupResultState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "found"; order: PublicOrderSummary }
  | { kind: "not_found"; message: string }
  | { kind: "rate_limited"; message: string }
  | { kind: "error"; message: string };

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
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
  const viewState = usePageStateParam();
  const { isAuthenticated, user } = useSession();

  const lookupFormRef = useRef<HTMLFormElement | null>(null);
  const [orderCode, setOrderCode] = useState("");
  const [phone, setPhone] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);
  const [lookupState, setLookupState] = useState<LookupResultState>({ kind: "idle" });
  const [recentOrders, setRecentOrders] = useState<OrderView[]>([]);

  const storeLabel = brandName || "cửa hàng";

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(lookupFormRef.current);
    }
  }, [errorPulse]);

  useEffect(() => {
    if (user?.phone && !phone) {
      setPhone(user.phone);
    }
  }, [user?.phone, phone]);

  // Optional convenience chips for already-signed-in buyers (public lookup still works without login)
  useEffect(() => {
    if (!isAuthenticated) {
      setRecentOrders([]);
      return;
    }
    let active = true;
    api<{ items?: OrderView[] }>("/api/orders?page=1&page_size=6", { retryOn401: false })
      .then((res) => {
        if (active && Array.isArray(res?.items)) {
          setRecentOrders(res.items.slice(0, 4));
        }
      })
      .catch(() => {
        // Non-blocking
      });
    return () => {
      active = false;
    };
  }, [isAuthenticated]);

  const runLookup = async (code: string, recipientPhone: string) => {
    setLookupState({ kind: "loading" });
    try {
      const res = await fetch("/api/orders/lookup", {
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
      setErrorPulse((n) => n + 1);
      return;
    }
    if (digits.length < 8) {
      setFormError(
        "Vui lòng nhập số điện thoại người nhận đã dùng khi đặt hàng (tối thiểu 8 chữ số)."
      );
      setErrorPulse((n) => n + 1);
      return;
    }
    setFormError(null);
    void runLookup(cleanCode, phone.trim());
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

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={styles.heroStage}>
          {renderHeroHead(storeLabel)}
          <div style={{ width: "100%" }}>
            <StateView
              kind="loading"
              skeletonVariant="detail"
              title="Đang tra cứu đơn hàng..."
              rows={3}
            />
          </div>
        </section>
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        <section className={styles.heroStage}>
          {renderHeroHead("Nhập mã đơn và số điện thoại đặt hàng để kiểm tra không cần đăng nhập.")}
          <div style={{ width: "100%" }}>
            <StateView
              kind="empty"
              title="Chưa nhập mã đơn cần tra cứu"
              message="Nhập mã đơn hàng trong email xác nhận kèm số điện thoại người nhận để xem tiến trình giao hàng."
              actionLabel="Mở tra cứu đơn"
              actionHref="/tra-cuu-don-hang"
            />
          </div>
        </section>
      </div>
    );
  }

  const foundOrder = lookupState.kind === "found" ? lookupState.order : null;
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
      <section className={styles.heroStage}>
        {renderHeroHead(
          `Nhập mã đơn hàng và số điện thoại người nhận để theo dõi tiến trình giao nhận tại ${storeLabel} mà không cần đăng nhập.`
        )}

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
                disabled={isSubmitting}
              />
            </div>

            <div className={shared.field} data-shake-target="">
              <label htmlFor="lookup-phone" className={shared.fieldLabel}>
                Số điện thoại người nhận
              </label>
              <input
                id="lookup-phone"
                name="phone"
                type="tel"
                autoComplete="tel"
                className={`${styles.bigInput} ${formError ? styles.bigInputInvalid : ""}`}
                value={phone}
                onChange={(e) => {
                  setPhone(e.target.value);
                  if (formError) setFormError(null);
                }}
                placeholder="0912 486 305"
                disabled={isSubmitting}
              />
            </div>
          </div>

          {formError && (
            <p id="lookup-code-error" className={shared.fieldError} role="alert">
              {formError}
            </p>
          )}

          <Button type="submit" variant="primary" size="lg" fullWidth disabled={isSubmitting}>
            {isSubmitting ? "Đang tra cứu..." : "Tra cứu đơn hàng"}
          </Button>
        </form>

        {recentOrders.length > 0 && (
          <div className={styles.recentWrap}>
            <span className={styles.recentLabel}>Đơn gần đây của bạn</span>
            <div className={styles.recentChips}>
              {recentOrders.map((o) => {
                const isActive = foundOrder?.order_code === o.orderCode;
                const meta = getOrderStatusInfo(o.backendStatus);
                return (
                  <button
                    key={o.id}
                    type="button"
                    className={`${styles.recentChip} ${isActive ? styles.recentChipActive : ""}`}
                    onClick={() => {
                      const nextPhone = o.buyerPhone || phone || user?.phone || "";
                      setOrderCode(o.orderCode);
                      if (nextPhone) setPhone(nextPhone);
                      setFormError(null);
                      if (nextPhone.replace(/\D/g, "").length >= 8) {
                        void runLookup(o.orderCode, nextPhone);
                      }
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
        {lookupState.kind === "idle" && (
          <StateView
            kind="empty"
            title="Nhập mã đơn và số điện thoại để bắt đầu"
            message="Để bảo vệ quyền riêng tư, trang tra cứu công khai chỉ hiển thị thông tin người nhận đã che và khu vực giao hàng (Phường/Xã, Tỉnh/Thành phố)."
            actionLabel="Mở đơn hàng của tôi"
            actionHref="/don-hang"
          />
        )}

        {lookupState.kind === "loading" && (
          <StateView
            kind="loading"
            skeletonVariant="detail"
            title="Đang tra cứu thông tin đơn hàng..."
            rows={4}
          />
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
                  <span>{storeLabel}</span>
                  {foundOrder.created_at ? renderDateTime(foundOrder.created_at) : null}
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
                            {item.sku_name ? <span>{item.sku_name}</span> : null}
                            <span className="num">SL {item.quantity}</span>
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
                          <span className={styles.timelineStem} aria-hidden="true">
                            <span className={styles.timelineDot} />
                            <span className={styles.timelineLine} />
                          </span>
                          <span className={styles.timelineTime}>
                            {ev.created_at ? renderDateTime(ev.created_at) : "—"}
                          </span>
                          <div>
                            <p className={styles.infoVal}>{evMeta.label}</p>
                          </div>
                        </li>
                      );
                    })}
                  </ol>
                ) : (
                  <p className={styles.infoSub}>Chưa có mốc cập nhật hành trình cho đơn này.</p>
                )}
              </div>

              <div className={styles.sheetSection}>
                <p className={styles.infoSub} style={{ marginBottom: 12 }}>
                  Để sửa địa chỉ nhận hàng, hủy đơn, thanh toán lại hoặc gửi yêu cầu trả hàng, vui
                  lòng đăng nhập tài khoản đặt hàng của bạn.
                </p>
                <div className={shared.formActions}>
                  <Button variant="primary" size="md" href="/don-hang">
                    Quản lý đơn hàng của tôi
                  </Button>
                  <Button variant="secondary" size="md" href="/chinh-sach">
                    Chính sách cửa hàng
                  </Button>
                </div>
              </div>
            </div>
          </div>
        )}
      </section>

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip title={`Sản phẩm tại ${storeLabel}`} products={suggestedProducts} />
      )}
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
