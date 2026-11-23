"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { ORDER_STATUS_META, PAYMENT_METHOD_LABELS, PAYMENT_STATUS_META, PRODUCTS } from "@/data";
import { MEDIA_BANNERS } from "@/data/media";
import type { Order, StatusTone } from "@/data";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import {
  GUEST_LOOKUP_DEFAULT_PHONE,
  GUEST_LOOKUP_SAMPLES,
  cancelGuestSampleOrder,
  createInitialGuestLookupOrders,
  lookupGuestSampleOrder,
  requestEditGuestSampleAddress,
} from "@/mock/guestOrderLookup";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconChat, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderLookupView.module.css";

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function OrderLookupContent() {
  const viewState = usePageStateParam();
  const { openChat, showToast } = useMockStore();

  const [sampleOrders, setSampleOrders] = useState<Order[]>(() => createInitialGuestLookupOrders());
  const [orderCode, setOrderCode] = useState("NM-261120-109");
  const [phone, setPhone] = useState(GUEST_LOOKUP_DEFAULT_PHONE);
  const [searchedKey, setSearchedKey] = useState<{ code: string; phone: string } | null>({
    code: "NM-261120-109",
    phone: GUEST_LOOKUP_DEFAULT_PHONE,
  });
  const [formError, setFormError] = useState<string | null>(null);

  const [newAddress, setNewAddress] = useState("");
  const [editReason, setEditReason] = useState("Cập nhật địa chỉ nhận hàng");
  const [editError, setEditError] = useState<string | null>(null);

  const foundOrder = useMemo(() => {
    if (!searchedKey) return undefined;
    return lookupGuestSampleOrder(sampleOrders, searchedKey.code, searchedKey.phone);
  }, [sampleOrders, searchedKey]);

  const suggestedProducts = useMemo(() => PRODUCTS.slice(2, 6), []);

  useRevealOnScroll(
    `${viewState ?? "live"}-${foundOrder?.id ?? "none"}-${foundOrder?.status ?? ""}`
  );

  const handleLookupSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = orderCode.trim();
    const digits = phone.replace(/\D/g, "");
    if (cleanCode.length < 5) {
      setFormError("Vui lòng nhập mã đơn hàng hợp lệ (ví dụ: NM-261120-109).");
      return;
    }
    if (digits.length < 8) {
      setFormError("Vui lòng nhập số điện thoại đã dùng khi đặt hàng (tối thiểu 8 chữ số).");
      return;
    }
    setFormError(null);
    setSearchedKey({ code: cleanCode, phone: phone.trim() });
  };

  const handleSelectSample = (code: string, samplePhone: string) => {
    setOrderCode(code);
    setPhone(samplePhone);
    setFormError(null);
    setSearchedKey({ code, phone: samplePhone });
  };

  const handleCancelSample = (targetOrderId: string) => {
    setSampleOrders((prev) =>
      cancelGuestSampleOrder(
        prev,
        targetOrderId,
        "Người mua tự hủy trên trang tra cứu trước khi đóng gói."
      )
    );
    showToast("Đã hủy đơn hàng trước khi đóng gói.");
  };

  const handleEditAddress = (e: React.FormEvent) => {
    e.preventDefault();
    if (!foundOrder) return;
    if (newAddress.trim().length < 10) {
      setEditError("Vui lòng nhập đầy đủ số nhà, Phường/Xã và Tỉnh/Thành phố mới.");
      return;
    }
    setEditError(null);
    setSampleOrders((prev) =>
      requestEditGuestSampleAddress(prev, foundOrder.id, newAddress, editReason)
    );
    showToast("Đã ghi nhận yêu cầu đổi địa chỉ giao hàng.");
    setNewAddress("");
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
            message="Mở lại trang tra cứu và chọn một mã đơn như NM-261120-109 để xem chi tiết hành trình giao hàng."
            actionLabel="Mở tra cứu đơn"
            actionHref="/tra-cuu-don-hang"
          />
        </section>
        <ProductAddonStrip title="Gợi ý sản phẩm cơ bản" products={suggestedProducts} />
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
  const canEditAddr = Boolean(
    foundOrder &&
    (foundOrder.status === "cho_xac_nhan" ||
      foundOrder.status === "dang_chuan_bi" ||
      foundOrder.status === "dang_giao")
  );

  return (
    <div className={shared.page}>
      {renderHeader(
        "Nhập mã đơn và số điện thoại để theo dõi hành trình giao hàng, không cần đăng nhập."
      )}

      <section className={shared.section}>
        <div className={styles.lookupLayout}>
          {/* Left: Single white search surface + unboxed reassurance below */}
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
                      placeholder="Ví dụ: NM-261120-109"
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

              <div className={styles.sampleWrap}>
                <p className={shared.fieldLabel}>Chọn nhanh một đơn gần đây</p>
                <div className={styles.sampleList}>
                  {GUEST_LOOKUP_SAMPLES.map((sample, idx) => {
                    const isActive = searchedKey?.code.toUpperCase() === sample.code.toUpperCase();
                    return (
                      <button
                        key={sample.code}
                        type="button"
                        data-rise=""
                        style={{ "--i": Math.min(idx + 3, 6) } as React.CSSProperties}
                        className={`${styles.sampleBtn} ${isActive ? styles.sampleBtnActive : ""}`}
                        onClick={() => handleSelectSample(sample.code, sample.phone)}
                      >
                        <span className={`${styles.sampleCode} num`}>{sample.code}</span>
                        <span className={styles.sampleHint}>{sample.hint}</span>
                      </button>
                    );
                  })}
                </div>
              </div>
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
                  Thông báo hành trình vận đơn và bản nháp hóa đơn VAT được gửi tự động qua{" "}
                  <strong>Email</strong> và <strong>Zalo OA NexusMart</strong>.
                </p>
                <p className={styles.bodyCopy}>
                  Đặt hàng không cần tài khoản vẫn được hủy trước khi đóng gói, gửi yêu cầu đổi địa
                  chỉ và hưởng trọn đổi trả 7 ngày.
                </p>
              </div>
            </div>
          </div>

          {/* Right: Single white order result sheet */}
          <div
            className={styles.resultCol}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            {!foundOrder ? (
              <StateView
                kind="empty"
                title="Không tìm thấy đơn hàng khớp mã và số điện thoại"
                message="Kiểm tra lại mã đơn gửi trong email xác nhận hoặc chọn nhanh một đơn bên trái."
                actionLabel="Xem đơn NM-261120-109"
                onAction={() => handleSelectSample("NM-261120-109", GUEST_LOOKUP_DEFAULT_PHONE)}
              />
            ) : (
              <div className={styles.resultCard}>
                {/* Dark header band for clear hierarchy */}
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
                    <div
                      className={shared.retryBox}
                      role="region"
                      aria-label={`Thông tin chờ thanh toán đơn ${foundOrder.orderCode}`}
                      data-rise=""
                      style={{ "--i": 4 } as React.CSSProperties}
                    >
                      <div className={shared.retryHead}>
                        <p className={shared.retryTitle}>
                          Đơn <span className="num">{foundOrder.orderCode}</span> đang chờ hoàn tất
                          thanh toán
                        </p>
                        <span className={`${shared.statusTag} ${shared.toneWarn} num`}>
                          Cần thanh toán: {formatVnd(foundOrder.total)}
                        </span>
                      </div>
                      <p className={shared.retryText}>
                        Giao dịch chuyển khoản trước đó chưa hoàn tất. Các sản phẩm đúng màu và size
                        vẫn đang được giữ chỗ
                        {foundOrder.paymentDueAt
                          ? ` đến ${formatDateTimeVi(foundOrder.paymentDueAt)}`
                          : ""}
                        . Đăng nhập vào tài khoản mua hàng để mở lại mã VietQR và hoàn tất thanh
                        toán an toàn.
                      </p>
                      <div className={shared.formActions}>
                        <Button variant="primary" size="md" href="/don-hang">
                          Đăng nhập để thanh toán lại
                        </Button>
                        <Button variant="secondary" size="md" href="/tro-giup">
                          Liên hệ hỗ trợ
                        </Button>
                      </div>
                    </div>
                  )}

                  {/* Unboxed 2-column order summary details */}
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
                        <dt className={styles.infoKey}>Giao hàng & Thông báo</dt>
                        <dd className={`${styles.infoVal} num`}>
                          {foundOrder.carrier ?? "SPX"} ·{" "}
                          {foundOrder.trackingCode ?? "Đang cấp mã vận đơn"}
                        </dd>
                        <dd className={styles.infoSub}>
                          Nhận thông báo qua Email và Zalo OA NexusMart
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
                      <div className={styles.infoTile} data-reveal="">
                        <dt className={styles.infoKey}>Quyền lợi đi kèm</dt>
                        <dd className={styles.infoVal}>
                          {foundOrder.coInspectionEnabled
                            ? "Mở kiểm tra khi nhận hàng"
                            : "Giao tiêu chuẩn"}
                        </dd>
                        <dd className={styles.infoSub}>Đổi size 2 chiều tận nơi trong 7 ngày</dd>
                      </div>
                    </dl>
                  </div>

                  {/* Products */}
                  <div className={styles.sheetSection}>
                    <h3 className={styles.sheetHeading}>Sản phẩm trong đơn</h3>
                    <div className={styles.itemList}>
                      {foundOrder.items.map((item) => (
                        <div key={item.skuCode} className={styles.itemRow} data-reveal="">
                          <img
                            src={item.image}
                            alt={item.productName}
                            width={896}
                            height={1200}
                            className={styles.itemImg}
                          />
                          <div className={styles.itemInfo}>
                            <Link
                              href={`/san-pham/${item.productSlug}`}
                              className={styles.itemName}
                            >
                              {item.productName}
                            </Link>
                            <span className={styles.itemMeta}>
                              Màu: {item.colorName} · Size {item.size} · SL {item.quantity}
                            </span>
                          </div>
                          <span className={`${styles.itemPrice} num`}>
                            {formatVnd(item.lineTotal)}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>

                  {/* Timeline */}
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

                  {/* Self-cancel */}
                  {foundOrder.status === "cho_xac_nhan" && (
                    <div className={styles.guestActionBlock} data-reveal="">
                      <h3 className={styles.sheetHeading}>Hủy đơn trước khi đóng gói</h3>
                      <p className={styles.bodyCopy}>
                        Bạn có thể hủy ngay khi xưởng chưa đóng gói. Nếu đã thanh toán trước, tiền
                        được hoàn về tài khoản gốc trong vòng 24 giờ.
                      </p>
                      <div className={shared.formActions}>
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => handleCancelSample(foundOrder.id)}
                        >
                          Hủy đơn {foundOrder.orderCode}
                        </Button>
                      </div>
                    </div>
                  )}

                  {/* Edit address / support request */}
                  {canEditAddr && (
                    <form
                      className={styles.guestActionBlock}
                      onSubmit={handleEditAddress}
                      noValidate
                      data-reveal=""
                    >
                      <h3 className={styles.sheetHeading}>Yêu cầu đổi địa chỉ nhận hàng</h3>
                      {foundOrder.addressEditRequest && (
                        <p className={`${shared.feedbackBox} ${shared.feedbackSuccess}`}>
                          Đã ghi nhận địa chỉ mới: {foundOrder.addressEditRequest.requestedAddress}{" "}
                          (
                          {foundOrder.addressEditRequest.status === "da_cap_nhat"
                            ? "Đã cập nhật"
                            : "Chờ xưởng duyệt"}
                          ).
                        </p>
                      )}
                      <div className={shared.formGrid}>
                        <div className={`${shared.field} ${shared.formFull}`}>
                          <label htmlFor="guest-edit-addr" className={shared.fieldLabel}>
                            Địa chỉ giao hàng mới
                          </label>
                          <input
                            id="guest-edit-addr"
                            type="text"
                            className={shared.input}
                            value={newAddress}
                            onChange={(e) => setNewAddress(e.target.value)}
                            placeholder="Số nhà, đường, Phường/Xã, Tỉnh/Thành phố"
                          />
                        </div>
                        <div className={`${shared.field} ${shared.formFull}`}>
                          <label htmlFor="guest-edit-reason" className={shared.fieldLabel}>
                            Loại yêu cầu
                          </label>
                          <select
                            id="guest-edit-reason"
                            className={shared.select}
                            value={editReason}
                            onChange={(e) => setEditReason(e.target.value)}
                          >
                            <option value="Cập nhật địa chỉ nhận hàng">
                              Cập nhật địa chỉ nhận hàng
                            </option>
                            <option value="Yêu cầu hủy muộn trước khi giao bưu tá">
                              Yêu cầu hủy đơn trước khi giao bưu tá
                            </option>
                          </select>
                        </div>
                      </div>
                      {editError && (
                        <p className={shared.fieldError} role="alert">
                          {editError}
                        </p>
                      )}
                      <div className={shared.formActions}>
                        <Button type="submit" variant="ink" size="sm">
                          Gửi yêu cầu cập nhật
                        </Button>
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          onClick={() => openChat(foundOrder.shopId)}
                        >
                          <IconChat size={14} />
                          <span>Nhắn tin cho xưởng</span>
                        </Button>
                        <Button variant="secondary" size="sm" href="/don-hang">
                          Đơn hàng của tôi
                        </Button>
                      </div>
                    </form>
                  )}
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
