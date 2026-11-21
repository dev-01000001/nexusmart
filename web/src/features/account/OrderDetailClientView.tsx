"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type OrderLineItem,
  type OrderStatus,
  type StatusTone,
} from "@/data/orders";
import type { Product } from "@/data/products";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, IconMinus, IconPlus, IconTrash, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderDetailClientView.module.css";
import type { OrderView } from "./orderView";
import {
  MAX_RETURN_PHOTOS,
  RETURN_STATUS_META,
  normalizeReturnRequest,
  normalizeReturnRequests,
  sanitizeReturnPhotoUrl,
  validateReturnPayload,
  type ReturnRequestItem,
  type ReturnRequestView,
} from "./returnsTypes";

const STANDARD_STEPS: Array<{ status: OrderStatus; label: string }> = [
  { status: "cho_xac_nhan", label: "Chờ xác nhận" },
  { status: "dang_chuan_bi", label: "Đang chuẩn bị" },
  { status: "dang_giao", label: "Đang giao hàng" },
  { status: "da_giao", label: "Đã giao" },
];

const CANCEL_REASONS = [
  "Thay đổi địa chỉ nhận hàng sang tỉnh/phường khác",
  "Muốn đổi size hoặc chọn thêm màu khác",
  "Quên áp mã giảm giá của cửa hàng",
  "Đặt trùng đơn hàng, không còn nhu cầu",
];

const RETURN_REASONS = [
  "Cần đổi size khác để mặc vừa vai hơn",
  "Sản phẩm có lỗi đường may hoặc bề mặt vải",
  "Giao nhầm màu sắc hoặc nhầm mẫu so với đơn đặt",
  "Chất liệu thực tế chưa phù hợp nhu cầu sử dụng",
];

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

interface ReturnItemSelection {
  selected: boolean;
  quantity: number;
}

function buildInitialSelections(items: OrderLineItem[]): Record<string, ReturnItemSelection> {
  const next: Record<string, ReturnItemSelection> = {};
  for (const item of items) {
    next[item.skuCode] = { selected: true, quantity: Math.max(1, item.quantity) };
  }
  return next;
}

export interface OrderDetailClientViewProps {
  brandName: string;
  orderId: string;
  initialOrder: OrderView | null;
  suggestedProducts: Product[];
  initialError?: string | null;
}

function OrderDetailInner({
  brandName,
  orderId,
  initialOrder,
  suggestedProducts,
  initialError = null,
}: OrderDetailClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { addToCart, showToast } = useStore();

  const [order, setOrder] = useState<OrderView | null>(initialOrder);
  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);

  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);

  const [returns, setReturns] = useState<ReturnRequestView[]>([]);
  const [returnSelections, setReturnSelections] = useState<Record<string, ReturnItemSelection>>(
    () => buildInitialSelections(initialOrder?.items ?? [])
  );
  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [returnPhotos, setReturnPhotos] = useState<string[]>([]);
  const [photoInput, setPhotoInput] = useState("");
  const [photoError, setPhotoError] = useState<string | null>(null);
  const [submittingReturn, setSubmittingReturn] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);

  const [retryingPayment, setRetryingPayment] = useState(false);
  const [retryError, setRetryError] = useState<string | null>(null);
  const [reordering, setReordering] = useState(false);

  useEffect(() => {
    setReturnSelections(buildInitialSelections(order?.items ?? []));
  }, [order?.id, order?.items]);

  // Fetch existing return requests for this order; silently ignore 404/503 if not deployed yet.
  useEffect(() => {
    if (!order?.id || viewState) return;
    const controller = new AbortController();
    const loadReturns = async () => {
      try {
        const res = await fetch(`/api/orders/${encodeURIComponent(order.id)}/returns`, {
          method: "GET",
          headers: { Accept: "application/json" },
          credentials: "same-origin",
          cache: "no-store",
          signal: controller.signal,
        });
        if (!res.ok) return;
        const data = (await res.json().catch(() => null)) as unknown;
        if (!controller.signal.aborted) {
          setReturns(normalizeReturnRequests(data));
        }
      } catch {
        // Silently ignore network/abort/503 errors so order detail remains responsive.
      }
    };
    void loadReturns();
    return () => controller.abort();
  }, [order?.id, viewState]);

  const itemLookupBySkuId = useMemo(() => {
    const map = new Map<string, OrderLineItem>();
    if (!order) return map;
    for (const item of order.items) {
      map.set(item.skuCode, item);
      const backendSkuId = order.itemSkuIds[item.skuCode];
      if (backendSkuId) map.set(backendSkuId, item);
    }
    return map;
  }, [order]);

  const selectedReturnSummary = useMemo(() => {
    if (!order) return { count: 0, totalQty: 0, estimatedVnd: 0 };
    let count = 0;
    let totalQty = 0;
    let estimatedVnd = 0;
    for (const item of order.items) {
      const sel = returnSelections[item.skuCode];
      if (sel?.selected && sel.quantity > 0) {
        count += 1;
        totalQty += sel.quantity;
        estimatedVnd += item.unitPrice * sel.quantity;
      }
    }
    return { count, totalQty, estimatedVnd };
  }, [order, returnSelections]);

  useRevealOnScroll(
    `${viewState ?? "live"}-${order?.id ?? "none"}-${order?.status ?? ""}-${order?.paymentStatus ?? ""}-${returns.length}`
  );

  const handleReorder = async () => {
    if (!order) return;
    setReordering(true);
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
          router.push(`/dang-nhap?next=${encodeURIComponent(`/don-hang/${order.id}`)}`);
          return;
        }
        if (res.ok) added += 1;
      }
      if (added > 0) {
        showToast(`Đã đưa lại ${added} sản phẩm vào giỏ hàng.`);
        router.push("/gio-hang");
      } else {
        showToast("Sản phẩm trong đơn hiện không còn sẵn hàng để mua lại.");
      }
    } finally {
      setReordering(false);
    }
  };

  const handleRetryVietQr = async () => {
    if (!order?.paymentId) return;
    setRetryError(null);
    setRetryingPayment(true);
    try {
      const res = await api<{ payment: { payment_id: string } }>(
        `/api/payments/${encodeURIComponent(order.paymentId)}/retry`,
        {
          method: "POST",
          body: { method: "VIETQR" },
        }
      );
      const nextId = res.payment?.payment_id || order.paymentId;
      router.push(`/thanh-toan/cho/${encodeURIComponent(nextId)}`);
    } catch (err) {
      setRetryError(
        err instanceof ApiError
          ? err.message
          : "Chưa tạo lại được mã VietQR lúc này. Bạn vui lòng thử lại."
      );
    } finally {
      setRetryingPayment(false);
    }
  };

  const handleCancelSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;
    setCancelError(null);
    setCancelling(true);
    try {
      const res = await api<{ order: OrderView }>(
        `/api/orders/${encodeURIComponent(order.id)}/cancel`,
        {
          method: "POST",
          body: { reason: cancelReason },
        }
      );
      setOrder(res.order);
      showToast(`Đã hủy đơn hàng ${res.order.orderCode}.`);
      router.refresh();
    } catch (err) {
      setCancelError(
        err instanceof ApiError ? err.message : "Chưa hủy được đơn hàng lúc này. Vui lòng thử lại."
      );
    } finally {
      setCancelling(false);
    }
  };

  const handleConfirmReceived = async () => {
    if (!order) return;
    setConfirmError(null);
    setConfirming(true);
    try {
      const res = await api<{ order: OrderView }>(
        `/api/orders/${encodeURIComponent(order.id)}/confirm-received`,
        { method: "POST" }
      );
      setOrder(res.order);
      showToast(`Đã xác nhận nhận hàng. Cảm ơn bạn đã mua sắm tại ${brandName}.`);
      router.refresh();
    } catch (err) {
      setConfirmError(
        err instanceof ApiError ? err.message : "Chưa xác nhận được lúc này. Bạn vui lòng thử lại."
      );
    } finally {
      setConfirming(false);
    }
  };

  const handleToggleReturnItem = (skuCode: string, maxQty: number) => {
    setReturnError(null);
    setReturnSelections((prev) => {
      const current = prev[skuCode] ?? { selected: true, quantity: maxQty };
      return {
        ...prev,
        [skuCode]: {
          selected: !current.selected,
          quantity: Math.max(1, Math.min(maxQty, current.quantity || 1)),
        },
      };
    });
  };

  const handleStepReturnQty = (skuCode: string, delta: number, maxQty: number) => {
    setReturnError(null);
    setReturnSelections((prev) => {
      const current = prev[skuCode] ?? { selected: true, quantity: maxQty };
      const nextQty = Math.max(1, Math.min(maxQty, current.quantity + delta));
      return {
        ...prev,
        [skuCode]: { selected: true, quantity: nextQty },
      };
    });
  };

  const handleAddReturnPhoto = () => {
    setPhotoError(null);
    const trimmed = photoInput.trim();
    if (!trimmed) {
      setPhotoError("Vui lòng nhập đường dẫn ảnh minh chứng trước khi thêm.");
      return;
    }
    if (returnPhotos.length >= MAX_RETURN_PHOTOS) {
      setPhotoError(`Bạn chỉ có thể đính kèm tối đa ${MAX_RETURN_PHOTOS} ảnh minh chứng.`);
      return;
    }
    const safeUrl = sanitizeReturnPhotoUrl(trimmed);
    if (!safeUrl) {
      setPhotoError(
        "Đường dẫn ảnh không hợp lệ. Chỉ chấp nhận ảnh nội bộ (/...) hoặc liên kết https://."
      );
      return;
    }
    if (returnPhotos.includes(safeUrl)) {
      setPhotoError("Ảnh này đã có trong danh sách đính kèm.");
      return;
    }
    setReturnPhotos((prev) => [...prev, safeUrl]);
    setPhotoInput("");
  };

  const handleRemoveReturnPhoto = (urlToRemove: string) => {
    setPhotoError(null);
    setReturnPhotos((prev) => prev.filter((u) => u !== urlToRemove));
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order) return;

    const selectedItems: ReturnRequestItem[] = [];
    for (const item of order.items) {
      const sel = returnSelections[item.skuCode];
      if (!sel?.selected) continue;
      const skuId = order.itemSkuIds[item.skuCode] || item.skuCode;
      selectedItems.push({
        sku_id: skuId,
        quantity: Math.max(1, Math.min(item.quantity, Math.trunc(sel.quantity))),
      });
    }

    if (selectedItems.length === 0) {
      setReturnError("Vui lòng chọn ít nhất 1 sản phẩm cần đổi trả.");
      return;
    }

    const cleanNote = returnDetail.trim();
    if (cleanNote.length < 5) {
      setReturnError(
        "Vui lòng mô tả ngắn tình trạng sản phẩm hoặc size cần đổi (tối thiểu 5 ký tự)."
      );
      return;
    }

    const combinedReason = `${returnReason}. ${cleanNote}`.slice(0, 500);
    const validation = validateReturnPayload({
      items: selectedItems,
      reason: combinedReason,
      photos: returnPhotos,
    });
    if (!validation.ok) {
      setReturnError(validation.message);
      return;
    }

    setReturnError(null);
    setSubmittingReturn(true);
    try {
      const res = await api<{
        return_request?: unknown;
        order?: OrderView | null;
      }>(`/api/orders/${encodeURIComponent(order.id)}/returns`, {
        method: "POST",
        body: validation.data,
      });

      const created = normalizeReturnRequest(res.return_request);
      if (created) {
        setReturns((prev) => [created, ...prev.filter((r) => r.id !== created.id)]);
      }
      if (res.order) {
        setOrder(res.order);
      }
      setReturnDetail("");
      setReturnPhotos([]);
      setPhotoInput("");
      setPhotoError(null);
      showToast(`Đã gửi yêu cầu đổi trả tới ${brandName}.`);
      router.refresh();
    } catch (err) {
      setReturnError(
        err instanceof ApiError
          ? err.message
          : "Chưa gửi được yêu cầu đổi trả lúc này. Vui lòng thử lại."
      );
    } finally {
      setSubmittingReturn(false);
    }
  };

  const renderBreadcrumb = () => (
    <nav className={shared.breadcrumbNav} aria-label="Đường dẫn đơn hàng">
      <Link href="/don-hang" className={shared.breadcrumbLink}>
        Đơn hàng của tôi
      </Link>
      <span aria-hidden="true">/</span>
      <span className="num">{order?.orderCode ?? orderId}</span>
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
      </div>
    );
  }

  if (viewState === "error" || initialError) {
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
            message={
              initialError ?? "Hệ thống theo dõi đơn hàng đang tạm gián đoạn. Vui lòng thử tải lại."
            }
            actionLabel="Quay lại danh sách đơn"
            actionHref="/don-hang"
          />
        </section>
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
              Mã đơn hàng bạn mở không thuộc tài khoản hiện tại tại {brandName}.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="empty"
            title="Không tìm thấy đơn hàng tương ứng"
            message="Quay lại danh sách đơn hàng của tài khoản để kiểm tra lại."
            actionLabel="Về danh sách đơn hàng"
            actionHref="/don-hang"
          />
        </section>
      </div>
    );
  }

  const statusMeta = ORDER_STATUS_META[order.status];
  const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
  const isUnpaidQr =
    order.paymentMethod === "vietqr" &&
    order.status !== "da_huy" &&
    (order.paymentStatus === "cho_thanh_toan" || order.paymentStatus === "thanh_toan_that_bai") &&
    Boolean(order.paymentId);
  const currentStepIdx = STANDARD_STEPS.findIndex((s) => s.status === order.status);
  const isTerminalSpecial = order.status === "da_huy" || order.status === "yeu_cau_tra_hang";

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
            Đơn hàng tại <strong>{brandName}</strong>
            {order.createdAt ? (
              <>
                , đặt lúc <span className="num">{formatDateTimeVi(order.createdAt)}</span>
              </>
            ) : null}
            . Theo dõi giao nhận và hỗ trợ sau mua.
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
          <Button
            variant="ink"
            size="md"
            disabled={reordering}
            onClick={() => void handleReorder()}
          >
            {reordering ? "Đang thêm..." : "Mua lại"}
          </Button>
        </div>
      </section>

      <section className={shared.section}>
        <div className={styles.detailGrid}>
          <div className={styles.mainCol}>
            {isUnpaidQr && (
              <div
                className={styles.vietQrBanner}
                role="region"
                aria-label="Hoàn tất thanh toán VietQR"
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div className={styles.notifyHead}>
                  <strong>Đơn {order.orderCode} đang chờ hoàn tất thanh toán VietQR</strong>
                  <span className={`${shared.statusTag} ${shared.toneWarn} num`}>
                    Cần thanh toán: {formatVnd(order.total)}
                  </span>
                </div>
                <p className={styles.actionPanelDesc}>
                  Mở trang quét mã VietQR để hoàn tất chuyển khoản
                  {order.paymentDueAt ? ` trước ${formatDateTimeVi(order.paymentDueAt)}` : ""}. Hệ
                  thống sẽ tự xác nhận đơn ngay khi nhận được thanh toán.
                </p>
                {retryError && (
                  <p className={shared.fieldError} role="alert">
                    {retryError}
                  </p>
                )}
                <div className={shared.formActions}>
                  <Button
                    variant="primary"
                    size="md"
                    href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}`}
                  >
                    Mở trang thanh toán VietQR
                  </Button>
                  {order.paymentStatus === "thanh_toan_that_bai" && (
                    <Button
                      variant="secondary"
                      size="md"
                      disabled={retryingPayment}
                      onClick={() => void handleRetryVietQr()}
                    >
                      {retryingPayment ? "Đang tạo mã mới..." : "Tạo lại mã VietQR"}
                    </Button>
                  )}
                </div>
              </div>
            )}

            <div className={styles.orderSheet}>
              <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Tiến trình đơn hàng</h2>
                  {order.updatedAt && (
                    <span className={`${shared.sectionNote} num`}>
                      Cập nhật: {formatDateTimeVi(order.updatedAt)}
                    </span>
                  )}
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
                  </div>
                )}

                {order.timeline.length > 0 && (
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
                            {ev.timestamp ? formatDateTimeVi(ev.timestamp) : ""}
                          </span>
                          <div className={styles.eventBody}>
                            <span className={styles.eventTitle}>{ev.title}</span>
                            <span className={styles.eventNote}>{ev.note}</span>
                          </div>
                        </li>
                      ))}
                  </ol>
                )}
              </div>

              <div
                className={styles.sheetDivider}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm đã đặt</h2>
                  <Link href="/san-pham" className={shared.breadcrumbLink}>
                    Xem thêm tại {brandName}
                  </Link>
                </div>

                <div className={styles.itemStack}>
                  {order.items.map((item) => {
                    const productHref = `/san-pham/${item.productSlug || item.productId}`;
                    return (
                      <div key={item.skuCode} className={styles.itemRow} data-reveal="">
                        <Link href={productHref} className={styles.itemThumbLink}>
                          <img
                            src={item.image || "/products/ao-thun-cotton-trang.png"}
                            alt={item.productName}
                            width={896}
                            height={1200}
                            className={styles.itemThumb}
                          />
                        </Link>
                        <div className={styles.itemInfo}>
                          <Link href={productHref} className={styles.itemName}>
                            {item.productName}
                          </Link>
                          <span className={styles.itemMeta}>
                            {item.colorName}
                            {item.size ? ` · Size ${item.size}` : ""}
                          </span>
                          <span className={`${styles.itemMeta} num`}>
                            {formatVnd(item.unitPrice)} × {item.quantity}
                          </span>
                        </div>
                        <span className={`${styles.itemAmount} num`}>
                          {formatVnd(item.lineTotal)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            {returns.length > 0 && (
              <section
                className={styles.returnSheet}
                aria-label="Yêu cầu đổi trả và hoàn tiền"
                data-reveal=""
              >
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Yêu cầu đổi trả & hoàn tiền</h2>
                  <span className={`${shared.sectionNote} num`}>{returns.length} yêu cầu</span>
                </div>

                <div className={styles.returnRequestList}>
                  {returns.map((ret) => {
                    const retMeta = RETURN_STATUS_META[ret.status];
                    return (
                      <article key={ret.id} className={styles.returnRequestItem}>
                        <div className={styles.returnRequestHead}>
                          <span className={`${shared.statusTag} ${toneClass(retMeta.tone)}`}>
                            {retMeta.label}
                          </span>
                          {ret.createdAt && (
                            <span className={`${shared.sectionNote} num`}>
                              Gửi lúc {formatDateTimeVi(ret.createdAt)}
                            </span>
                          )}
                        </div>

                        <p className={styles.actionPanelDesc}>{retMeta.description}</p>

                        {ret.items.length > 0 && (
                          <div className={styles.returnSubBlock}>
                            <span className={styles.returnSubLabel}>Sản phẩm yêu cầu đổi trả</span>
                            <ul className={styles.returnItemSummaryList}>
                              {ret.items.map((rit, idx) => {
                                const matched =
                                  (rit.sku_id ? itemLookupBySkuId.get(rit.sku_id) : undefined) ??
                                  undefined;
                                return (
                                  <li
                                    key={`${rit.order_item_id || rit.sku_id || idx}`}
                                    className={styles.returnItemSummaryRow}
                                  >
                                    <span>
                                      {matched
                                        ? `${matched.productName}${
                                            matched.colorName ? ` (${matched.colorName}` : ""
                                          }${matched.size ? `, Size ${matched.size}` : ""}${
                                            matched.colorName ? ")" : ""
                                          }`
                                        : "Sản phẩm trong đơn"}
                                    </span>
                                    <span className="num">× {rit.quantity}</span>
                                  </li>
                                );
                              })}
                            </ul>
                          </div>
                        )}

                        {ret.reason && (
                          <div className={styles.returnSubBlock}>
                            <span className={styles.returnSubLabel}>Lý do của bạn</span>
                            <p className={styles.returnBodyText}>{ret.reason}</p>
                          </div>
                        )}

                        {ret.photos.length > 0 && (
                          <div className={styles.returnSubBlock}>
                            <span className={styles.returnSubLabel}>Ảnh minh chứng</span>
                            <div className={styles.returnPhotoGrid}>
                              {ret.photos.map((photoUrl, pIdx) => (
                                <a
                                  key={photoUrl}
                                  href={photoUrl}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  className={styles.returnPhotoThumbLink}
                                >
                                  <img
                                    src={photoUrl}
                                    alt={`Ảnh minh chứng đổi trả ${pIdx + 1}`}
                                    className={styles.returnPhotoThumb}
                                    loading="lazy"
                                  />
                                </a>
                              ))}
                            </div>
                          </div>
                        )}

                        {ret.refundAmountVnd > 0 && (
                          <div className={styles.returnRefundRow}>
                            <span>
                              {ret.status === "REFUNDED"
                                ? "Số tiền đã hoàn"
                                : "Số tiền hoàn dự kiến"}
                            </span>
                            <strong className={`${styles.returnRefundVal} num`}>
                              {formatVnd(ret.refundAmountVnd)}
                            </strong>
                          </div>
                        )}

                        {ret.reverseTrackingCode && (
                          <p className={`${styles.returnMetaLine} num`}>
                            Mã vận đơn hoàn hàng: <strong>{ret.reverseTrackingCode}</strong>
                          </p>
                        )}

                        {ret.sellerNote && (
                          <div className={styles.returnSellerNote}>
                            <span className={styles.returnSubLabel}>Phản hồi từ {brandName}</span>
                            <p className={styles.returnBodyText}>{ret.sellerNote}</p>
                          </div>
                        )}

                        {ret.status === "REJECTED" && (
                          <div className={styles.returnRejectedGuide} role="note">
                            <p>
                              <strong>Hướng dẫn tiếp theo:</strong> Vui lòng xem kỹ phản hồi từ{" "}
                              {brandName}. Nếu sản phẩm vẫn còn nguyên tem mác hoặc có lỗi cần kiểm
                              tra thêm, bạn có thể nhắn trực tiếp cho gian hàng hoặc liên hệ bộ phận
                              hỗ trợ khách hàng kèm mã đơn{" "}
                              <strong className="num">{order.orderCode}</strong> để được xem xét
                              lại.
                            </p>
                          </div>
                        )}
                      </article>
                    );
                  })}
                </div>
              </section>
            )}

            {order.canConfirmReceived && (
              <div className={styles.unboxedSection} data-reveal="">
                <h2 className={shared.sectionTitle}>Xác nhận đã nhận hàng</h2>
                <p className={styles.actionPanelDesc}>
                  Nếu bạn đã kiểm tra đủ sản phẩm và hài lòng với kiện hàng, hãy xác nhận để hoàn
                  tất đơn hàng.
                </p>
                {confirmError && (
                  <p className={shared.fieldError} role="alert">
                    {confirmError}
                  </p>
                )}
                <div className={shared.formActions}>
                  <Button
                    type="button"
                    variant="primary"
                    size="md"
                    disabled={confirming}
                    onClick={() => void handleConfirmReceived()}
                  >
                    {confirming ? "Đang xác nhận..." : "Đã nhận được hàng"}
                  </Button>
                </div>
              </div>
            )}

            {order.canCancel && (
              <form className={styles.unboxedSection} onSubmit={handleCancelSubmit} data-reveal="">
                <h2 className={shared.sectionTitle}>Hủy đơn trước khi bàn giao vận chuyển</h2>
                <p className={styles.actionPanelDesc}>
                  Đơn chưa bàn giao cho đơn vị vận chuyển nên bạn có thể hủy ngay. Với đơn đã thanh
                  toán trước, số tiền <strong className="num">{formatVnd(order.total)}</strong> sẽ
                  được hoàn về tài khoản gốc.
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
                    disabled={cancelling}
                  >
                    {CANCEL_REASONS.map((reason) => (
                      <option key={reason} value={reason}>
                        {reason}
                      </option>
                    ))}
                  </select>
                </div>

                {cancelError && (
                  <p className={shared.fieldError} role="alert">
                    {cancelError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button type="submit" variant="danger" size="md" disabled={cancelling}>
                    {cancelling ? "Đang hủy đơn..." : "Xác nhận hủy đơn"}
                  </Button>
                </div>
              </form>
            )}

            {order.canRequestReturn && (
              <form
                className={styles.unboxedSection}
                onSubmit={handleReturnSubmit}
                noValidate
                data-reveal=""
              >
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Gửi yêu cầu đổi trả hàng</h2>
                  <span className={`${shared.statusTag} ${shared.tonePos}`}>Hỗ trợ sau mua</span>
                </div>
                <p className={styles.actionPanelDesc}>
                  Chọn sản phẩm và số lượng cần đổi trả, kèm mô tả tình trạng hoặc ảnh minh chứng để{" "}
                  {brandName} tiếp nhận nhanh hơn.
                </p>

                <div className={shared.field}>
                  <span className={shared.fieldLabel}>Chọn sản phẩm cần đổi trả</span>
                  <div className={styles.returnPickerList}>
                    {order.items.map((item) => {
                      const sel = returnSelections[item.skuCode] ?? {
                        selected: true,
                        quantity: item.quantity,
                      };
                      const checkboxId = `return-item-${item.skuCode}`;
                      return (
                        <div
                          key={item.skuCode}
                          className={`${styles.returnPickerRow} ${
                            sel.selected ? styles.returnPickerRowActive : ""
                          }`}
                        >
                          <label htmlFor={checkboxId} className={styles.returnPickerLabel}>
                            <input
                              id={checkboxId}
                              type="checkbox"
                              className={shared.checkbox}
                              checked={sel.selected}
                              disabled={submittingReturn}
                              onChange={() => handleToggleReturnItem(item.skuCode, item.quantity)}
                            />
                            <img
                              src={item.image || "/products/ao-thun-cotton-trang.png"}
                              alt={item.productName}
                              width={56}
                              height={74}
                              className={styles.returnPickerThumb}
                            />
                            <span className={styles.returnPickerInfo}>
                              <span className={styles.returnPickerName}>{item.productName}</span>
                              <span className={styles.returnPickerMeta}>
                                {item.colorName}
                                {item.size ? ` · Size ${item.size}` : ""}
                                {" · "}
                                <span className="num">{formatVnd(item.unitPrice)}</span>
                              </span>
                            </span>
                          </label>

                          <div
                            className={styles.qtyStepper}
                            aria-label={`Số lượng đổi trả cho ${item.productName}`}
                          >
                            <button
                              type="button"
                              className={styles.qtyStepBtn}
                              aria-label="Giảm số lượng"
                              disabled={submittingReturn || !sel.selected || sel.quantity <= 1}
                              onClick={() => handleStepReturnQty(item.skuCode, -1, item.quantity)}
                            >
                              <IconMinus size={14} />
                            </button>
                            <span className={`${styles.qtyStepVal} num`}>
                              {sel.selected ? sel.quantity : 0}
                            </span>
                            <button
                              type="button"
                              className={styles.qtyStepBtn}
                              aria-label="Tăng số lượng"
                              disabled={
                                submittingReturn || !sel.selected || sel.quantity >= item.quantity
                              }
                              onClick={() => handleStepReturnQty(item.skuCode, 1, item.quantity)}
                            >
                              <IconPlus size={14} />
                            </button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                  <p className={styles.returnSelectionSummary}>
                    {selectedReturnSummary.count > 0 ? (
                      <>
                        Đã chọn <strong className="num">{selectedReturnSummary.totalQty}</strong>{" "}
                        sản phẩm
                        {" · "}Giá trị tạm tính:{" "}
                        <strong className="num">
                          {formatVnd(selectedReturnSummary.estimatedVnd)}
                        </strong>
                      </>
                    ) : (
                      "Chưa chọn sản phẩm nào. Vui lòng tích chọn ít nhất 1 sản phẩm."
                    )}
                  </p>
                </div>

                <div className={shared.field}>
                  <label htmlFor="return-reason" className={shared.fieldLabel}>
                    Lý do đổi trả
                  </label>
                  <select
                    id="return-reason"
                    className={shared.select}
                    value={returnReason}
                    onChange={(e) => setReturnReason(e.target.value)}
                    disabled={submittingReturn}
                  >
                    {RETURN_REASONS.map((reason) => (
                      <option key={reason} value={reason}>
                        {reason}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={shared.field}>
                  <label htmlFor="return-note" className={shared.fieldLabel}>
                    Mô tả tình trạng sản phẩm hoặc size cần đổi
                  </label>
                  <textarea
                    id="return-note"
                    className={shared.textarea}
                    value={returnDetail}
                    onChange={(e) => setReturnDetail(e.target.value)}
                    placeholder="Ví dụ: Áo còn nguyên tem mác, cần đổi từ size M sang size L."
                    disabled={submittingReturn}
                  />
                </div>

                <div className={shared.field}>
                  <label htmlFor="return-photo-url" className={shared.fieldLabel}>
                    Ảnh minh chứng (tùy chọn, tối đa {MAX_RETURN_PHOTOS} ảnh)
                  </label>
                  <div className={shared.codeInputRow}>
                    <input
                      id="return-photo-url"
                      type="text"
                      className={shared.input}
                      value={photoInput}
                      onChange={(e) => {
                        setPhotoInput(e.target.value);
                        if (photoError) setPhotoError(null);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          handleAddReturnPhoto();
                        }
                      }}
                      placeholder="Nhập đường dẫn ảnh (/products/... hoặc https://...)"
                      disabled={submittingReturn || returnPhotos.length >= MAX_RETURN_PHOTOS}
                    />
                    <Button
                      type="button"
                      variant="secondary"
                      size="md"
                      disabled={submittingReturn || returnPhotos.length >= MAX_RETURN_PHOTOS}
                      onClick={handleAddReturnPhoto}
                    >
                      Thêm ảnh
                    </Button>
                  </div>
                  <p className={shared.fieldHint}>
                    Hỗ trợ liên kết ảnh bắt đầu bằng / hoặc https:// ({returnPhotos.length}/
                    {MAX_RETURN_PHOTOS} ảnh).
                  </p>
                  {photoError && (
                    <p className={shared.fieldError} role="alert">
                      {photoError}
                    </p>
                  )}

                  {returnPhotos.length > 0 && (
                    <div className={styles.returnPhotoDraftList}>
                      {returnPhotos.map((url, idx) => (
                        <div key={url} className={styles.returnPhotoDraftCard}>
                          <img
                            src={url}
                            alt={`Ảnh minh chứng ${idx + 1}`}
                            className={styles.returnPhotoDraftImg}
                          />
                          <span className={styles.returnPhotoDraftUrl}>{url}</span>
                          <button
                            type="button"
                            className={styles.returnPhotoRemoveBtn}
                            aria-label={`Xóa ảnh minh chứng ${idx + 1}`}
                            disabled={submittingReturn}
                            onClick={() => handleRemoveReturnPhoto(url)}
                          >
                            <IconTrash size={15} />
                            <span>Xóa</span>
                          </button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                {returnError && (
                  <p className={shared.fieldError} role="alert">
                    {returnError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button
                    type="submit"
                    variant="primary"
                    size="md"
                    disabled={submittingReturn || selectedReturnSummary.count === 0}
                  >
                    {submittingReturn ? "Đang gửi yêu cầu..." : "Gửi yêu cầu đổi trả"}
                  </Button>
                </div>
              </form>
            )}
          </div>

          <aside className={styles.sideCol}>
            <div
              className={styles.summarySheet}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Địa chỉ & Giao hàng</h2>
                <p className={styles.sideLine}>
                  <strong>{order.buyerName || "Người nhận"}</strong>
                  {order.buyerPhone ? ` · ${order.buyerPhone}` : ""}
                </p>
                <p className={styles.sideLine}>
                  {order.shippingAddress || "Đang cập nhật địa chỉ"}
                </p>
                {order.trackingCode && (
                  <p className={`${styles.sideLine} num`}>
                    {order.carrier ? `${order.carrier} · ` : ""}Mã vận đơn:{" "}
                    <strong>{order.trackingCode}</strong>
                  </p>
                )}
              </div>

              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Chi tiết thanh toán</h2>
                <p className={styles.sideLine}>{PAYMENT_METHOD_LABELS[order.paymentMethod]}</p>
                <p className={styles.sideMuted}>
                  Trạng thái:{" "}
                  <strong>
                    {payMeta?.label ?? (order.isPaid ? "Đã thanh toán" : "Thu hộ khi nhận hàng")}
                  </strong>
                </p>

                <dl className={styles.ledgerBox}>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Tạm tính tiền hàng</dt>
                    <dd className="num">{formatVnd(order.subtotal)}</dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Mã giảm giá cửa hàng</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {order.shopVoucherDiscount > 0
                        ? formatSignedVnd(-order.shopVoucherDiscount)
                        : "0₫"}
                    </dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                    <dd className="num">{formatVnd(order.shippingFee)}</dd>
                  </div>
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

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip
          title={`Gợi ý thêm từ ${brandName}`}
          subtitle="Sản phẩm đang sẵn hàng tại kho của cửa hàng"
          products={suggestedProducts}
        />
      )}
    </div>
  );
}

export function OrderDetailClientView(props: OrderDetailClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải chi tiết đơn hàng..." rows={5} />
        </div>
      }
    >
      <OrderDetailInner {...props} />
    </Suspense>
  );
}
