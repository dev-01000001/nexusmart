"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import {
  DELIVERY_METHOD_LABELS,
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  PRODUCTS,
  getProductById,
  getProductsByShop,
  getShopById,
} from "@/data";
import type { OrderExchangeDetail, OrderStatus, SizeCode, StatusTone } from "@/data";
import { formatDateTimeVi, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, IconChat, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

const STANDARD_STEPS: Array<{ status: OrderStatus; label: string }> = [
  { status: "cho_xac_nhan", label: "Chờ xác nhận" },
  { status: "dang_chuan_bi", label: "Đang chuẩn bị" },
  { status: "dang_giao", label: "Đang giao hàng" },
  { status: "da_giao", label: "Đã giao" },
];

const CANCEL_REASONS = [
  "Thay đổi địa chỉ nhận hàng sang tỉnh/phường khác",
  "Muốn đổi size hoặc chọn thêm màu khác",
  "Quên áp mã giảm giá gian hàng hoặc mã freeship",
  "Đặt trùng đơn hàng, không còn nhu cầu",
];

const RETURN_REASONS = [
  "Cần đổi size khác để mặc vừa vai hơn",
  "Sản phẩm có lỗi đường may hoặc bề mặt vải",
  "Giao nhầm màu sắc hoặc nhầm mẫu so với đơn đặt",
  "Chất liệu thực tế chưa phù hợp nhu cầu sử dụng",
];

const SIZES: SizeCode[] = ["S", "M", "L", "XL", "2XL"];

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function OrderDetailContent() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const viewState = usePageStateParam();
  const {
    orders,
    user,
    cancelOrder,
    requestEditOrderAddress,
    requestExchangeOrder,
    requestVatInvoice,
    openChat,
    addToCart,
    showToast,
  } = useStore();

  const orderId = Array.isArray(params?.id) ? params.id[0] : params?.id;
  const order = useMemo(() => orders.find((o) => o.id === orderId), [orders, orderId]);

  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);

  const [editAddressOpen, setEditAddressOpen] = useState(false);
  const [newAddress, setNewAddress] = useState("");
  const [editAddrReason, setEditAddrReason] = useState("Cập nhật địa chỉ nhận hàng mới");
  const [editAddrError, setEditAddrError] = useState<string | null>(null);

  const [exchangeType, setExchangeType] = useState<OrderExchangeDetail["type"]>("doi_size_2_chieu");
  const [selectedExchangeSku, setSelectedExchangeSku] = useState<string>("");
  const [targetSize, setTargetSize] = useState<SizeCode>("XL");
  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [returnError, setReturnError] = useState<string | null>(null);

  const [vatOpen, setVatOpen] = useState(false);
  const [vatTaxCode, setVatTaxCode] = useState("");
  const [vatCompany, setVatCompany] = useState("");
  const [vatAddress, setVatAddress] = useState("");
  const [vatEmail, setVatEmail] = useState(user.email);
  const [vatError, setVatError] = useState<string | null>(null);

  useRevealOnScroll(
    `${viewState ?? "live"}-${order?.id ?? "none"}-${order?.status ?? ""}-${order?.paymentStatus ?? ""}`
  );

  const shop = order ? getShopById(order.shopId) : undefined;
  const shopProducts = useMemo(() => {
    if (!order) return PRODUCTS.slice(0, 4);
    const sameShop = getProductsByShop(order.shopId);
    if (sameShop.length >= 4) return sameShop.slice(0, 4);
    const others = PRODUCTS.filter((p) => p.shopId !== order.shopId);
    return [...sameShop, ...others].slice(0, 4);
  }, [order]);

  const itemEligibility = useMemo(() => {
    if (!order) return [];
    return order.items.map((item) => {
      const prod = getProductById(item.productId);
      const eligible = prod?.returnEligible !== false;
      return {
        item,
        eligible,
        exclusionReason:
          prod?.returnExclusionReason ?? "Hàng Outlet hoặc pack niêm phong không áp dụng đổi trả.",
      };
    });
  }, [order]);

  const eligibleItems = itemEligibility.filter((x) => x.eligible);
  const allIneligible = itemEligibility.length > 0 && eligibleItems.length === 0;
  const activeExchangeItem =
    eligibleItems.find((e) => e.item.skuCode === selectedExchangeSku)?.item ??
    eligibleItems[0]?.item ??
    order?.items[0];

  const handleReorder = () => {
    if (!order) return;
    let added = 0;
    for (const item of order.items) {
      const product = getProductById(item.productId);
      const sku = product?.skus.find((s) => s.skuCode === item.skuCode);
      const colorCode = sku?.colorCode ?? product?.colors[0]?.code;
      if (product && colorCode) {
        if (
          addToCart({
            productId: product.id,
            colorCode,
            size: item.size,
            quantity: item.quantity,
          })
        ) {
          added += 1;
        }
      }
    }
    if (added > 0) {
      showToast(`Đã đưa lại ${added} sản phẩm vào giỏ hàng.`);
      router.push("/gio-hang");
    }
  };

  const handleCancelSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    cancelOrder(order.id, cancelReason);
  };

  const handleEditAddressSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    if (newAddress.trim().length < 10) {
      setEditAddrError("Vui lòng nhập đầy đủ số nhà, Phường/Xã và Tỉnh/Thành phố mới.");
      return;
    }
    setEditAddrError(null);
    requestEditOrderAddress(order.id, newAddress, editAddrReason);
    setNewAddress("");
    setEditAddressOpen(false);
  };

  const handleExchangeSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    if (exchangeType !== "bu_le_tat" && allIneligible) {
      setReturnError("Toàn bộ sản phẩm trong đơn thuộc nhóm ngoại lệ không áp dụng đổi trả.");
      return;
    }
    const cleanNote = returnDetail.trim();
    if (cleanNote.length < 6) {
      setReturnError(
        "Vui lòng mô tả ngắn tình trạng sản phẩm, size cần đổi hoặc màu chiếc tất thất lạc (tối thiểu 6 ký tự)."
      );
      return;
    }
    setReturnError(null);
    requestExchangeOrder(order.id, {
      type: exchangeType,
      reason:
        exchangeType === "bu_le_tat"
          ? `Yêu cầu bù lẻ chiếc tất thất lạc: ${cleanNote}`
          : `${returnReason}. Ghi chú: ${cleanNote}`,
      fromSize: exchangeType === "doi_size_2_chieu" ? activeExchangeItem?.size : undefined,
      toSize: exchangeType === "doi_size_2_chieu" ? targetSize : undefined,
      pickupAddress: order.shippingAddress,
    });
    setReturnDetail("");
  };

  const handleVatSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    if (vatTaxCode.trim().length < 10 || vatCompany.trim().length < 3 || !vatEmail.includes("@")) {
      setVatError("Vui lòng nhập đầy đủ MST (tối thiểu 10 ký tự), Tên đơn vị và Email nhận HĐĐT.");
      return;
    }
    setVatError(null);
    requestVatInvoice(order.id, {
      companyName: vatCompany,
      taxCode: vatTaxCode,
      email: vatEmail,
      companyAddress: vatAddress || undefined,
    });
    setVatOpen(false);
  };

  const renderBreadcrumb = () => (
    <nav className={shared.breadcrumbNav} aria-label="Đường dẫn đơn hàng">
      <Link href="/don-hang" className={shared.breadcrumbLink}>
        Đơn hàng của tôi
      </Link>
      <span aria-hidden="true">/</span>
      <span className="num">{order?.orderCode ?? orderId ?? "Chi tiết"}</span>
    </nav>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Chi tiết đơn hàng</h1>
            <p className={styles.detailLead}>
              Đang tải hành trình giao hàng và thông tin thanh toán cho đơn hàng này.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Thông tin đơn</h2>
          <StateView kind="loading" title="Đang tải chi tiết đơn hàng..." rows={5} />
        </section>
        <ProductAddonStrip title="Sản phẩm cơ bản từ NexusMart" products={shopProducts} />
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Chi tiết đơn hàng</h1>
            <p className={styles.detailLead}>
              Không thể lấy dữ liệu mới nhất cho mã đơn hàng này vào lúc này.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
          <StateView
            kind="error"
            title="Chưa thể tải chi tiết đơn hàng"
            message="Hệ thống theo dõi đơn hàng đang tạm gián đoạn. Vui lòng thử tải lại."
            actionLabel="Quay lại danh sách đơn"
            actionHref="/don-hang"
          />
        </section>
        <ProductAddonStrip title="Sản phẩm gợi ý" products={shopProducts} />
      </div>
    );
  }

  if (viewState === "empty" || !order) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Không tìm thấy đơn</h1>
            <p className={styles.detailLead}>
              Mã đơn hàng bạn mở không thuộc tài khoản hiện tại hoặc không còn tồn tại.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="empty"
            title="Không tìm thấy đơn hàng tương ứng"
            message="Kiểm tra danh sách đơn hoặc tra cứu bằng Mã đơn và Số điện thoại."
            actionLabel="Tra cứu đơn hàng"
            actionHref="/tra-cuu-don-hang"
          />
        </section>
        <ProductAddonStrip title="Gợi ý sản phẩm cơ bản" products={shopProducts} />
      </div>
    );
  }

  const statusMeta = ORDER_STATUS_META[order.status];
  const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
  const needsRetry =
    order.paymentStatus === "cho_thanh_toan" || order.paymentStatus === "thanh_toan_that_bai";
  const currentStepIdx = STANDARD_STEPS.findIndex((s) => s.status === order.status);
  const isTerminalSpecial = order.status === "da_huy" || order.status === "yeu_cau_tra_hang";
  const canRequestAddrEdit =
    order.status === "cho_xac_nhan" ||
    order.status === "dang_chuan_bi" ||
    order.status === "dang_giao";

  return (
    <div className={shared.page}>
      {renderBreadcrumb()}

      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${shared.pageTitle} lines num`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Chi tiết đơn {order.orderCode}
              </span>
            </span>
          </h1>
          <p className={styles.detailLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Đơn hàng tại <strong>{order.shopName}</strong>, đặt lúc{" "}
            <span className="num">{formatDateTimeVi(order.createdAt)}</span> — theo dõi giao nhận và
            hỗ trợ sau mua.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          {payMeta && (
            <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>
              {payMeta.label}
            </span>
          )}
          <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
            {statusMeta.label}
          </span>
          <Button variant="secondary" size="md" onClick={() => openChat(order.shopId)}>
            <IconChat size={15} />
            <span>Chat xưởng</span>
          </Button>
          <Button variant="ink" size="md" onClick={handleReorder}>
            Mua lại
          </Button>
        </div>
      </section>

      <section className={shared.section}>
        <div className={styles.detailGrid}>
          {/* Left column: retry highlight + ONE white order sheet + unboxed secondary actions */}
          <div className={styles.mainCol}>
            {needsRetry && (
              <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
                <RetryPaymentBox order={order} />
              </div>
            )}

            <div className={styles.orderSheet}>
              {/* Timeline section */}
              <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Tiến trình đơn hàng</h2>
                  <span className={`${shared.sectionNote} num`}>
                    Cập nhật: {formatDateTimeVi(order.updatedAt)}
                  </span>
                </div>

                {!isTerminalSpecial ? (
                  <div className={styles.milestoneGrid}>
                    {STANDARD_STEPS.map((step, idx) => {
                      const done = currentStepIdx > idx;
                      const current = currentStepIdx === idx;
                      return (
                        <div
                          key={step.status}
                          className={`${styles.milestoneStep} ${
                            current ? styles.milestoneCurrent : done ? styles.milestoneDone : ""
                          }`}
                        >
                          <span className={styles.milestoneLabel}>{step.label}</span>
                          <span className={styles.milestoneSub}>
                            {current ? "Hiện tại" : done ? "Hoàn tất" : "Sắp tới"}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <div className={styles.reasonBanner}>
                    <p>
                      Trạng thái xử lý: <strong>{statusMeta.label}</strong>
                    </p>
                    <p>{order.returnReason ?? order.cancelReason ?? statusMeta.description}</p>
                    {order.exchangeRequest && <p>{order.exchangeRequest.refundScheduleNote}</p>}
                  </div>
                )}

                <ol className={styles.eventList}>
                  {order.timeline
                    .slice()
                    .reverse()
                    .map((ev, idx) => (
                      <li
                        key={`${ev.timestamp}-${idx}`}
                        className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                        data-reveal=""
                      >
                        <span className={`${styles.eventTime} num`}>
                          {formatDateTimeVi(ev.timestamp)}
                        </span>
                        <div className={styles.eventBody}>
                          <span className={styles.eventTitle}>{ev.title}</span>
                          <span className={styles.eventNote}>{ev.note}</span>
                        </div>
                      </li>
                    ))}
                </ol>
              </div>

              {/* Ordered products */}
              <div
                className={styles.sheetDivider}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm đã đặt</h2>
                  {shop && (
                    <Link href={`/gian-hang/${shop.slug}`} className={shared.breadcrumbLink}>
                      Xem gian hàng {shop.shortName}
                    </Link>
                  )}
                </div>

                <div className={styles.itemStack}>
                  {itemEligibility.map(({ item, eligible, exclusionReason }) => (
                    <div key={item.skuCode} className={styles.itemRow} data-reveal="">
                      <Link href={`/san-pham/${item.productSlug}`} className={styles.itemThumbLink}>
                        <img
                          src={item.image}
                          alt={item.productName}
                          width={896}
                          height={1200}
                          className={styles.itemThumb}
                        />
                      </Link>
                      <div className={styles.itemInfo}>
                        <Link href={`/san-pham/${item.productSlug}`} className={styles.itemName}>
                          {item.productName}
                        </Link>
                        <span className={styles.itemMeta}>
                          Màu: {item.colorName} · Size {item.size}
                        </span>
                        <span className={`${styles.itemMeta} num`}>
                          {formatVnd(item.unitPrice)} × {item.quantity}
                        </span>
                        {!eligible && (
                          <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                            Không đổi trả: {exclusionReason}
                          </span>
                        )}
                      </div>
                      <span className={`${styles.itemAmount} num`}>
                        {formatVnd(item.lineTotal)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            {/* Unboxed notification summary on --bg */}
            <div className={styles.unboxedSection} data-reveal="">
              <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                <h2 className={shared.sectionTitle}>Thông báo đơn hàng</h2>
                <span className={shared.sectionNote}>Cập nhật qua Email & Zalo OA</span>
              </div>
              <div className={styles.notifyList}>
                <div className={styles.notifyRow}>
                  <div className={styles.notifyHead}>
                    <strong>Email xác nhận ({order.buyerEmail ?? user.email})</strong>
                    <span className={`${shared.statusTag} ${shared.tonePos}`}>Đã gửi</span>
                  </div>
                  <p className={styles.actionPanelDesc}>
                    Mã đơn <span className="num">{order.orderCode}</span> · Gửi lúc{" "}
                    <span className="num">{formatDateTimeVi(order.createdAt)}</span>.
                  </p>
                </div>
                <div className={styles.notifyRow}>
                  <div className={styles.notifyHead}>
                    <strong>Zalo OA NexusMart ({order.buyerPhone})</strong>
                    <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                      {order.trackingCode ? "Đã gửi mã vận đơn" : "Đã kích hoạt"}
                    </span>
                  </div>
                  <p className={styles.actionPanelDesc}>
                    {order.trackingCode
                      ? `Vận đơn ${order.trackingCode} (${order.carrier ?? "SPX"}) đã gửi về Zalo OA của bạn.`
                      : "Tự động gửi mã vận đơn và lịch đổi size 2 chiều ngay khi xưởng bàn giao."}
                  </p>
                </div>
              </div>
            </div>

            {/* Unboxed Cancel order form when cho_xac_nhan */}
            {order.status === "cho_xac_nhan" && (
              <form className={styles.unboxedSection} onSubmit={handleCancelSubmit} data-reveal="">
                <h2 className={shared.sectionTitle}>Hủy đơn trước khi đóng gói</h2>
                <p className={styles.actionPanelDesc}>
                  Đơn đang chờ xưởng xác nhận nên bạn có thể hủy ngay. Với đơn đã thanh toán trước,
                  số tiền <strong className="num">{formatVnd(order.total)}</strong> sẽ được hoàn về
                  tài khoản gốc trong vòng <strong>24 giờ</strong>.
                </p>

                <div className={shared.field}>
                  <label htmlFor="cancel-reason" className={shared.fieldLabel}>
                    Lý do hủy đơn
                  </label>
                  <select
                    id="cancel-reason"
                    className={shared.select}
                    value={cancelReason}
                    onChange={(e) => setCancelReason(e.target.value)}
                  >
                    {CANCEL_REASONS.map((reason) => (
                      <option key={reason} value={reason}>
                        {reason}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={shared.formActions}>
                  <Button type="submit" variant="danger" size="md">
                    Xác nhận hủy đơn
                  </Button>
                </div>
              </form>
            )}

            {/* Unboxed Edit shipping address / late-cancel request */}
            {canRequestAddrEdit && (
              <div className={styles.unboxedSection} data-reveal="">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Đổi địa chỉ nhận hàng</h2>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setEditAddressOpen((v) => !v)}
                  >
                    {editAddressOpen ? "Đóng biểu mẫu" : "Gửi yêu cầu tới xưởng"}
                  </Button>
                </div>
                {order.addressEditRequest && (
                  <div className={styles.reasonBanner}>
                    <p>
                      Yêu cầu gần nhất:{" "}
                      <strong>
                        {order.addressEditRequest.status === "da_cap_nhat"
                          ? "Đã cập nhật trực tiếp"
                          : "Đang chờ xưởng duyệt"}
                      </strong>
                    </p>
                    <p>Địa chỉ yêu cầu: {order.addressEditRequest.requestedAddress}</p>
                    <p>Lý do: {order.addressEditRequest.reason}</p>
                  </div>
                )}
                {editAddressOpen && (
                  <form onSubmit={handleEditAddressSubmit} noValidate>
                    <div className={shared.formGrid}>
                      <div className={`${shared.field} ${shared.formFull}`}>
                        <label htmlFor="edit-addr-input" className={shared.fieldLabel}>
                          Địa chỉ giao hàng mới (số nhà, Phường/Xã, Tỉnh/Thành phố)
                        </label>
                        <input
                          id="edit-addr-input"
                          type="text"
                          className={shared.input}
                          value={newAddress}
                          onChange={(e) => setNewAddress(e.target.value)}
                          placeholder="Ví dụ: Số 18 Lê Thánh Tôn, Phường Bến Nghé, TP. Hồ Chí Minh"
                        />
                      </div>
                      <div className={`${shared.field} ${shared.formFull}`}>
                        <label htmlFor="edit-addr-reason" className={shared.fieldLabel}>
                          Loại yêu cầu / Lý do
                        </label>
                        <select
                          id="edit-addr-reason"
                          className={shared.select}
                          value={editAddrReason}
                          onChange={(e) => setEditAddrReason(e.target.value)}
                        >
                          <option value="Cập nhật địa chỉ nhận hàng mới">
                            Đổi địa chỉ nhận hàng mới
                          </option>
                          <option value="Đổi số điện thoại người nhận">
                            Đổi số điện thoại người nhận
                          </option>
                          <option value="Yêu cầu hủy muộn trước khi bưu tá lấy hàng">
                            Yêu cầu hủy đơn trước khi bàn giao bưu tá
                          </option>
                        </select>
                      </div>
                    </div>
                    {editAddrError && (
                      <p className={shared.fieldError} role="alert">
                        {editAddrError}
                      </p>
                    )}
                    <div className={shared.formActions}>
                      <Button type="submit" variant="ink" size="md">
                        Gửi yêu cầu cập nhật
                      </Button>
                    </div>
                  </form>
                )}
              </div>
            )}

            {/* Unboxed 45-day Return / Two-way Size Exchange / Missing Sock Replacement */}
            {order.status === "da_giao" && (
              <form
                className={styles.unboxedSection}
                onSubmit={handleExchangeSubmit}
                noValidate
                data-reveal=""
              >
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Đổi trả 45 ngày & Đổi size 2 chiều</h2>
                  <span className={`${shared.statusTag} ${shared.tonePos}`}>
                    Tối đa 3 lần / đơn
                  </span>
                </div>
                <p className={styles.actionPanelDesc}>
                  Miễn phí trong <strong>45 ngày</strong> kể từ khi nhận hàng: bưu tá đổi size 2
                  chiều tận nơi, hoàn chuyển khoản vào <strong>Thứ 2 & Thứ 5</strong> hằng tuần,
                  hoặc bù lẻ chiếc tất thất lạc. Không áp dụng cho hàng Outlet và pack niêm phong.
                </p>

                <div
                  className={styles.exchangeTypeGrid}
                  role="radiogroup"
                  aria-label="Chọn hình thức hỗ trợ sau mua"
                >
                  <button
                    type="button"
                    role="radio"
                    aria-checked={exchangeType === "doi_size_2_chieu"}
                    className={`${styles.exchangeTypeBtn} ${
                      exchangeType === "doi_size_2_chieu" ? styles.exchangeTypeActive : ""
                    }`}
                    onClick={() => setExchangeType("doi_size_2_chieu")}
                  >
                    <span className={styles.exchangeTypeTitle}>Đổi size 2 chiều tận nơi</span>
                    <span className={styles.sideMuted}>Bưu tá giao cỡ mới và nhận lại cỡ cũ</span>
                  </button>

                  <button
                    type="button"
                    role="radio"
                    aria-checked={exchangeType === "tra_hang_hoan_tien"}
                    className={`${styles.exchangeTypeBtn} ${
                      exchangeType === "tra_hang_hoan_tien" ? styles.exchangeTypeActive : ""
                    }`}
                    onClick={() => setExchangeType("tra_hang_hoan_tien")}
                  >
                    <span className={styles.exchangeTypeTitle}>Trả hàng hoàn tiền</span>
                    <span className={styles.sideMuted}>Hoàn chuyển khoản Thứ 2 & Thứ 5</span>
                  </button>

                  <button
                    type="button"
                    role="radio"
                    aria-checked={exchangeType === "bu_le_tat"}
                    className={`${styles.exchangeTypeBtn} ${
                      exchangeType === "bu_le_tat" ? styles.exchangeTypeActive : ""
                    }`}
                    onClick={() => setExchangeType("bu_le_tat")}
                  >
                    <span className={styles.exchangeTypeTitle}>Bù chiếc tất thất lạc</span>
                    <span className={styles.sideMuted}>Tặng kèm 1 chiếc đồng màu miễn phí</span>
                  </button>
                </div>

                {allIneligible && exchangeType !== "bu_le_tat" ? (
                  <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="status">
                    Các sản phẩm trong đơn này đều thuộc nhóm ngoại lệ (Outlet / pack niêm phong)
                    nên không áp dụng đổi size hoặc trả hàng 45 ngày.
                  </div>
                ) : (
                  <>
                    {exchangeType === "doi_size_2_chieu" && (
                      <div className={shared.formGrid}>
                        <div className={shared.field}>
                          <label htmlFor="ex-item" className={shared.fieldLabel}>
                            Sản phẩm đủ điều kiện đổi size
                          </label>
                          <select
                            id="ex-item"
                            className={shared.select}
                            value={activeExchangeItem?.skuCode ?? ""}
                            onChange={(e) => setSelectedExchangeSku(e.target.value)}
                          >
                            {eligibleItems.map(({ item }) => (
                              <option key={item.skuCode} value={item.skuCode}>
                                {item.productName} (đang mặc size {item.size})
                              </option>
                            ))}
                          </select>
                        </div>

                        <div className={shared.field}>
                          <label htmlFor="ex-target-size" className={shared.fieldLabel}>
                            Chọn size mới muốn đổi
                          </label>
                          <select
                            id="ex-target-size"
                            className={shared.select}
                            value={targetSize}
                            onChange={(e) => setTargetSize(e.target.value as SizeCode)}
                          >
                            {SIZES.map((s) => (
                              <option key={s} value={s}>
                                Size {s}
                              </option>
                            ))}
                          </select>
                        </div>
                      </div>
                    )}

                    {exchangeType !== "bu_le_tat" && (
                      <div className={shared.field}>
                        <label htmlFor="return-reason" className={shared.fieldLabel}>
                          Lý do đổi trả
                        </label>
                        <select
                          id="return-reason"
                          className={shared.select}
                          value={returnReason}
                          onChange={(e) => setReturnReason(e.target.value)}
                        >
                          {RETURN_REASONS.map((reason) => (
                            <option key={reason} value={reason}>
                              {reason}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}

                    <div className={shared.field}>
                      <label htmlFor="return-note" className={shared.fieldLabel}>
                        {exchangeType === "bu_le_tat"
                          ? "Mô tả màu sắc và cỡ chiếc tất bị thất lạc"
                          : "Mô tả chi tiết tình trạng sản phẩm và khung giờ bưu tá tới đổi"}
                      </label>
                      <textarea
                        id="return-note"
                        className={shared.textarea}
                        value={returnDetail}
                        onChange={(e) => setReturnDetail(e.target.value)}
                        placeholder={
                          exchangeType === "bu_le_tat"
                            ? "Ví dụ: Thất lạc 1 chiếc tất màu xám đá size L sau lần giặt đầu."
                            : "Ví dụ: Áo còn nguyên tem mác, nhờ bưu tá giao size mới vào buổi chiều."
                        }
                      />
                    </div>

                    {returnError && (
                      <p className={shared.fieldError} role="alert">
                        {returnError}
                      </p>
                    )}

                    <div className={shared.formActions}>
                      <Button type="submit" variant="primary" size="md">
                        {exchangeType === "doi_size_2_chieu"
                          ? `Gửi yêu cầu đổi sang size ${targetSize}`
                          : exchangeType === "bu_le_tat"
                            ? "Gửi yêu cầu bù chiếc tất thất lạc"
                            : "Gửi yêu cầu trả hàng hoàn tiền"}
                      </Button>
                    </div>
                  </>
                )}
              </form>
            )}
          </div>

          {/* Right column: ONE unified white summary sheet with dark total footer */}
          <aside className={styles.sideCol}>
            <div
              className={styles.summarySheet}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              {/* Delivery & Recipient */}
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Địa chỉ & Giao hàng</h2>
                <p className={styles.sideLine}>
                  <strong>{order.buyerName}</strong> · {order.buyerPhone}
                </p>
                <p className={styles.sideLine}>{order.shippingAddress}</p>
                {order.deliveryMethod && (
                  <p className={styles.sideMuted}>{DELIVERY_METHOD_LABELS[order.deliveryMethod]}</p>
                )}
                {order.trackingCode && (
                  <p className={`${styles.sideLine} num`}>
                    {order.carrier ? `${order.carrier} · ` : ""}Mã vận đơn:{" "}
                    <strong>{order.trackingCode}</strong>
                  </p>
                )}
                {order.coInspectionEnabled && (
                  <div>
                    <span className={`${shared.statusTag} ${shared.tonePos}`}>
                      Mở kiểm tra hàng khi nhận
                    </span>
                  </div>
                )}
                {order.note && <p className={styles.sideMuted}>Ghi chú: {order.note}</p>}
                {(order.giftWrap || order.giftCardMessage) && (
                  <div className={styles.reasonBanner}>
                    <strong>Túi quà & Thiệp tặng</strong>
                    <p>{order.giftCardMessage || "Đã chọn đóng gói túi quà NexusMart."}</p>
                  </div>
                )}
              </div>

              {/* VAT Invoice */}
              <div className={styles.sideBlock}>
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={styles.sideTitle}>Hóa đơn VAT điện tử</h2>
                  {order.vatInvoice?.requested && (
                    <span
                      className={`${shared.statusTag} ${
                        order.vatInvoice.status === "da_phat_hanh"
                          ? shared.tonePos
                          : shared.toneWarn
                      }`}
                    >
                      {order.vatInvoice.status === "da_phat_hanh"
                        ? "Đã phát hành"
                        : "Duyệt nháp 24h"}
                    </span>
                  )}
                </div>

                {order.vatInvoice?.requested ? (
                  <div className={styles.ledgerBox}>
                    <p className={styles.sideLine}>
                      <strong>{order.vatInvoice.companyName}</strong>
                    </p>
                    <p className={`${styles.sideMuted} num`}>MST: {order.vatInvoice.taxCode}</p>
                    <p className={styles.sideMuted}>Email nhận HĐĐT: {order.vatInvoice.email}</p>
                    {order.vatInvoice.companyAddress && (
                      <p className={styles.sideMuted}>{order.vatInvoice.companyAddress}</p>
                    )}
                  </div>
                ) : order.status !== "da_huy" ? (
                  <>
                    <p className={styles.sideLine}>
                      Chưa yêu cầu xuất hóa đơn VAT. Bạn vẫn có thể bổ sung mã số thuế sau khi nhận
                      hàng để xưởng gửi bản nháp duyệt trong 24 giờ.
                    </p>
                    <div>
                      <Button variant="secondary" size="sm" onClick={() => setVatOpen((v) => !v)}>
                        {vatOpen ? "Đóng biểu mẫu VAT" : "Yêu cầu xuất hóa đơn VAT"}
                      </Button>
                    </div>

                    {vatOpen && (
                      <form onSubmit={handleVatSubmit} className={styles.vatForm} noValidate>
                        <div className={shared.field}>
                          <label htmlFor="od-vat-mst" className={shared.fieldLabel}>
                            Mã số thuế
                          </label>
                          <input
                            id="od-vat-mst"
                            type="text"
                            className={shared.input}
                            value={vatTaxCode}
                            onChange={(e) => setVatTaxCode(e.target.value)}
                            placeholder="0316482910"
                          />
                        </div>
                        <div className={shared.field}>
                          <label htmlFor="od-vat-name" className={shared.fieldLabel}>
                            Tên công ty / đơn vị
                          </label>
                          <input
                            id="od-vat-name"
                            type="text"
                            className={shared.input}
                            value={vatCompany}
                            onChange={(e) => setVatCompany(e.target.value)}
                            placeholder="Công ty TNHH..."
                          />
                        </div>
                        <div className={shared.field}>
                          <label htmlFor="od-vat-email" className={shared.fieldLabel}>
                            Email nhận bản nháp & hóa đơn
                          </label>
                          <input
                            id="od-vat-email"
                            type="email"
                            className={shared.input}
                            value={vatEmail}
                            onChange={(e) => setVatEmail(e.target.value)}
                            placeholder="ketoan@congty.vn"
                          />
                        </div>
                        <div className={shared.field}>
                          <label htmlFor="od-vat-addr" className={shared.fieldLabel}>
                            Địa chỉ đơn vị (không bắt buộc)
                          </label>
                          <input
                            id="od-vat-addr"
                            type="text"
                            className={shared.input}
                            value={vatAddress}
                            onChange={(e) => setVatAddress(e.target.value)}
                          />
                        </div>
                        {vatError && (
                          <p className={shared.fieldError} role="alert">
                            {vatError}
                          </p>
                        )}
                        <div>
                          <Button type="submit" variant="ink" size="sm">
                            Gửi yêu cầu VAT
                          </Button>
                        </div>
                      </form>
                    )}
                  </>
                ) : (
                  <p className={styles.sideLine}>Đơn hàng đã hủy không phát hành hóa đơn VAT.</p>
                )}
              </div>

              {/* Payment summary */}
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Chi tiết thanh toán</h2>
                <p className={styles.sideLine}>{PAYMENT_METHOD_LABELS[order.paymentMethod]}</p>
                <p className={styles.sideMuted}>
                  Trạng thái:{" "}
                  <strong>
                    {payMeta?.label ?? (order.isPaid ? "Đã thanh toán" : "Thu hộ khi giao")}
                  </strong>
                </p>

                <dl className={styles.ledgerBox}>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Tạm tính tiền hàng</dt>
                    <dd className="num">{formatVnd(order.subtotal)}</dd>
                  </div>
                  {(order.tierDiscount ?? 0) > 0 && (
                    <div className={styles.ledgerRow}>
                      <dt className={styles.ledgerLabel}>Giảm mua nhiều</dt>
                      <dd className={`${styles.ledgerDiscount} num`}>
                        {formatSignedVnd(-(order.tierDiscount ?? 0))}
                      </dd>
                    </div>
                  )}
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Voucher gian hàng</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.shopVoucherDiscount > 0
                        ? formatSignedVnd(-order.shopVoucherDiscount)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Voucher toàn sàn</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.platformVoucherDiscount > 0
                        ? formatSignedVnd(-order.platformVoucherDiscount)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                    <dd className="num">{formatVnd(order.shippingFee)}</dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Giảm phí vận chuyển</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.freeshipDiscount > 0 ? formatSignedVnd(-order.freeshipDiscount) : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Trừ Xu NexusMart</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.coinDiscount > 0 ? formatSignedVnd(-order.coinDiscount) : "0₫"}
                    </dd>
                  </div>
                  {(order.giftCardDiscount ?? 0) > 0 && (
                    <div className={styles.ledgerRow}>
                      <dt className={styles.ledgerLabel}>Trừ thẻ quà tặng</dt>
                      <dd className={`${styles.ledgerDiscount} num`}>
                        {formatSignedVnd(-(order.giftCardDiscount ?? 0))}
                      </dd>
                    </div>
                  )}
                </dl>
              </div>

              <div className={styles.totalRow}>
                <span>Tổng thanh toán</span>
                <span className={`${styles.totalVal} num`}>{formatVnd(order.total)}</span>
              </div>
            </div>
          </aside>
        </div>
      </section>

      <ProductAddonStrip
        title={`Gợi ý thêm từ ${order.shopName}`}
        subtitle="Đồ cơ bản cùng xưởng dệt, tiện gộp voucher cho đơn kế tiếp"
        products={shopProducts}
      />
    </div>
  );
}

export default function OrderDetailPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải chi tiết đơn hàng..." rows={5} />
        </div>
      }
    >
      <OrderDetailContent />
    </Suspense>
  );
}
