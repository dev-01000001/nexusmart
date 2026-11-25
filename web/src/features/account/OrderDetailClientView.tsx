"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type OrderLineItem,
  type OrderStatus,
} from "@/data/orders";
import type { Product } from "@/data/products";
import { ApiError, api } from "@/lib/api";
import { formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import {
  Button,
  IconCheck,
  IconChevronDown,
  IconClose,
  IconMinus,
  IconPlus,
  IconTrash,
  StateView,
} from "@/components";
import { useModalFocusTrap } from "@/components/modalFocus";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { renderOrderDateTime, statusToneClass } from "@/features/commerce/orderDisplay";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
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
  "Đổi địa chỉ nhận hàng",
  "Đổi size hoặc màu khác",
  "Quên áp mã giảm giá",
  "Không còn nhu cầu mua",
];

const RETURN_REASONS = [
  "Đổi size khác",
  "Sản phẩm có lỗi may hoặc bề mặt vải",
  "Giao nhầm màu hoặc nhầm mẫu",
  "Chất liệu chưa phù hợp nhu cầu",
];

const RETURN_STEP_LABELS = ["Chọn sản phẩm", "Lý do & ảnh", "Xác nhận"] as const;
const RETURN_STEP_GUARD_MS = 600;

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
  const [returnSheetOpen, setReturnSheetOpen] = useState(false);
  const [returnStep, setReturnStep] = useState<0 | 1 | 2>(0);
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
  const returnSheetRef = useRef<HTMLDivElement | null>(null);
  const returnStepEnteredAtRef = useRef(-Infinity);

  const closeReturnSheet = useCallback(() => setReturnSheetOpen(false), []);

  useEffect(() => {
    setReturnSelections(buildInitialSelections(order?.items ?? []));
  }, [order?.id, order?.items]);

  useModalFocusTrap({
    open: returnSheetOpen && Boolean(order?.canRequestReturn),
    containerRef: returnSheetRef,
    onEscape: closeReturnSheet,
  });

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
        // Non-blocking
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
        showToast(`Đã thêm ${added} sản phẩm vào giỏ hàng.`);
        router.push("/gio-hang");
      } else {
        showToast("Sản phẩm trong đơn hiện hết hàng.");
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
        { method: "POST", body: { method: "VIETQR" } }
      );
      const nextId = res.payment?.payment_id || order.paymentId;
      router.push(`/thanh-toan/cho/${encodeURIComponent(nextId)}`);
    } catch (err) {
      setRetryError(err instanceof ApiError ? err.message : "Chưa tạo lại được mã VietQR.");
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
        { method: "POST", body: { reason: cancelReason } }
      );
      setOrder(res.order);
      showToast(`Đã hủy đơn ${res.order.orderCode}.`);
      router.refresh();
    } catch (err) {
      setCancelError(err instanceof ApiError ? err.message : "Chưa hủy được đơn hàng.");
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
      showToast("Đã xác nhận nhận hàng.");
      router.refresh();
    } catch (err) {
      setConfirmError(err instanceof ApiError ? err.message : "Chưa xác nhận được lúc này.");
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
      return { ...prev, [skuCode]: { selected: true, quantity: nextQty } };
    });
  };

  const handleAddReturnPhoto = () => {
    setPhotoError(null);
    const trimmed = photoInput.trim();
    if (!trimmed) {
      setPhotoError("Nhập đường dẫn ảnh trước khi thêm.");
      return;
    }
    if (returnPhotos.length >= MAX_RETURN_PHOTOS) {
      setPhotoError(`Tối đa ${MAX_RETURN_PHOTOS} ảnh.`);
      return;
    }
    const safeUrl = sanitizeReturnPhotoUrl(trimmed);
    if (!safeUrl) {
      setPhotoError("Chỉ chấp nhận đường dẫn /... hoặc https://.");
      return;
    }
    if (returnPhotos.includes(safeUrl)) {
      setPhotoError("Ảnh này đã được thêm.");
      return;
    }
    setReturnPhotos((prev) => [...prev, safeUrl]);
    setPhotoInput("");
  };

  const handleAdvanceReturnStep = () => {
    const now = performance.now();
    if (now - returnStepEnteredAtRef.current < RETURN_STEP_GUARD_MS) return;
    setReturnError(null);
    if (returnStep === 0) {
      if (selectedReturnSummary.count === 0) {
        setReturnError("Chọn ít nhất 1 sản phẩm.");
        return;
      }
      returnStepEnteredAtRef.current = now;
      setReturnStep(1);
      return;
    }
    if (returnStep === 1) {
      if (returnDetail.trim().length < 5) {
        setReturnError("Mô tả tình trạng sản phẩm (tối thiểu 5 ký tự).");
        return;
      }
      returnStepEnteredAtRef.current = now;
      setReturnStep(2);
    }
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (
      returnStep !== 2 ||
      !order ||
      submittingReturn ||
      performance.now() - returnStepEnteredAtRef.current < RETURN_STEP_GUARD_MS
    ) {
      return;
    }

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
      setReturnError("Chọn ít nhất 1 sản phẩm.");
      setReturnStep(0);
      return;
    }

    const cleanNote = returnDetail.trim();
    if (cleanNote.length < 5) {
      setReturnError("Mô tả tình trạng sản phẩm (tối thiểu 5 ký tự).");
      setReturnStep(1);
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
      const res = await api<{ return_request?: unknown; order?: OrderView | null }>(
        `/api/orders/${encodeURIComponent(order.id)}/returns`,
        { method: "POST", body: validation.data }
      );
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
      returnStepEnteredAtRef.current = -Infinity;
      setReturnStep(0);
      setReturnSheetOpen(false);
      showToast(`Đã gửi yêu cầu đổi trả tới ${brandName}.`);
      router.refresh();
    } catch (err) {
      setReturnError(err instanceof ApiError ? err.message : "Chưa gửi được yêu cầu đổi trả.");
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
        <StateView kind="loading" skeletonVariant="detail" title="Đang tải đơn hàng..." rows={5} />
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <StateView
          kind="error"
          title="Chưa thể tải chi tiết đơn hàng."
          message={initialError ?? undefined}
          actionLabel="Về danh sách đơn"
          actionHref="/don-hang"
        />
      </div>
    );
  }

  if (viewState === "empty" || !order) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <StateView
          kind="empty"
          title="Không tìm thấy đơn hàng này."
          actionLabel="Về danh sách đơn"
          actionHref="/don-hang"
        />
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
  const progressFraction = currentStepIdx <= 0 ? 0 : currentStepIdx / (STANDARD_STEPS.length - 1);

  return (
    <div className={shared.page}>
      {renderBreadcrumb()}

      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${styles.detailTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Chi tiết đơn{" "}
              </span>
            </span>
            <span className="line">
              <span
                data-line=""
                style={{ "--i": 1 } as React.CSSProperties}
                className={`${styles.detailOrderCode} type-tight num`}
              >
                {order.orderCode}
              </span>
            </span>
          </h1>
          <p
            className={`${shared.pageLead} ${styles.leadMetaRow}`}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <span>{brandName}</span>
            {order.createdAt ? renderOrderDateTime(order.createdAt) : null}
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <span className={`${shared.statusTag} ${statusToneClass(statusMeta.tone)}`}>
            {statusMeta.label}
          </span>
          {payMeta && (
            <span className={`${shared.statusTag} ${statusToneClass(payMeta.tone)}`}>
              {payMeta.label}
            </span>
          )}
          {isUnpaidQr && (
            <Button
              variant="primary"
              size="md"
              href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}`}
            >
              Thanh toán VietQR
            </Button>
          )}
          {order.canConfirmReceived && (
            <Button
              variant="primary"
              size="md"
              loading={confirming}
              loadingLabel="Đang xác nhận"
              onClick={() => void handleConfirmReceived()}
            >
              Đã nhận được hàng
            </Button>
          )}
          {order.canRequestReturn && (
            <Button
              variant="secondary"
              size="md"
              onClick={() => {
                returnStepEnteredAtRef.current = -Infinity;
                setReturnStep(0);
                setReturnError(null);
                setPhotoError(null);
                setReturnSheetOpen(true);
              }}
            >
              Yêu cầu đổi trả
            </Button>
          )}
          <Button
            variant="ink"
            size="md"
            loading={reordering}
            loadingLabel="Đang thêm"
            onClick={() => void handleReorder()}
          >
            Mua lại
          </Button>
        </div>
      </section>

      <section className={shared.section}>
        <div className={styles.detailGrid}>
          <div className={styles.mainCol}>
            {isUnpaidQr && (
              <div className={styles.actionBanner} role="region" aria-label="Thanh toán VietQR">
                <div className={styles.actionBannerCopy}>
                  <p className={styles.actionBannerTitle}>
                    <span>Chờ thanh toán VietQR</span>
                    <span className="num">{formatVnd(order.total)}</span>
                  </p>
                  {order.paymentDueAt && (
                    <p className={styles.actionBannerSub}>
                      Hạn trước {renderOrderDateTime(order.paymentDueAt)}.
                    </p>
                  )}
                  {retryError && (
                    <p className={shared.fieldError} role="alert">
                      {retryError}
                    </p>
                  )}
                </div>
                <div className={shared.formActions}>
                  <Button
                    variant="primary"
                    size="md"
                    href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}`}
                  >
                    Mở mã VietQR
                  </Button>
                  {order.paymentStatus === "thanh_toan_that_bai" && (
                    <Button
                      variant="secondary"
                      size="md"
                      loading={retryingPayment}
                      loadingLabel="Đang tạo mã"
                      onClick={() => void handleRetryVietQr()}
                    >
                      Tạo mã mới
                    </Button>
                  )}
                </div>
              </div>
            )}

            {confirmError && (
              <p className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                {confirmError}
              </p>
            )}

            <div className={styles.orderSheet}>
              <div>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Trạng thái đơn hàng</h2>
                  {order.updatedAt && (
                    <span className={shared.sectionNote}>
                      {renderOrderDateTime(order.updatedAt)}
                    </span>
                  )}
                </div>

                {!isTerminalSpecial ? (
                  <div className={styles.stepperContainer}>
                    <div className={styles.stepperTrack} aria-hidden="true">
                      <span
                        className={styles.stepperTrackFill}
                        style={{ transform: `scaleX(${progressFraction})` }}
                      />
                    </div>
                    <ol className={styles.stepperWrap} aria-label="Tiến trình giao hàng">
                      {STANDARD_STEPS.map((step, idx) => {
                        const done = currentStepIdx > idx;
                        const current = currentStepIdx === idx;
                        return (
                          <li
                            key={step.status}
                            className={`${styles.stepNode} ${
                              done ? styles.stepNodeDone : current ? styles.stepNodeCurrent : ""
                            }`}
                            aria-current={current ? "step" : undefined}
                          >
                            <span className={styles.stepDot} aria-hidden="true">
                              {done ? (
                                <IconCheck size={14} />
                              ) : current ? (
                                <span className={styles.stepCurrentPip} />
                              ) : null}
                            </span>
                            <span className={styles.stepLabel}>{step.label}</span>
                          </li>
                        );
                      })}
                    </ol>
                  </div>
                ) : (
                  <div className={styles.reasonBanner}>
                    <p>
                      <strong>{statusMeta.label}</strong>
                    </p>
                    <p>{order.returnReason ?? order.cancelReason ?? statusMeta.description}</p>
                  </div>
                )}
              </div>

              <div className={styles.sheetDivider}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm</h2>
                  <span className={`${shared.sectionNote} num`}>{order.items.length} mục</span>
                </div>

                <div className={styles.itemStack}>
                  {order.items.map((item) => {
                    const productHref = `/san-pham/${item.productSlug || item.productId}`;
                    return (
                      <div key={item.skuCode} className={styles.itemRow}>
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
                            <span>{item.colorName}</span>
                            {item.size && <span>Size {item.size}</span>}
                            <span className="num">
                              {formatVnd(item.unitPrice)} × {item.quantity}
                            </span>
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
              <section className={styles.returnSheet} aria-label="Yêu cầu đổi trả">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Yêu cầu đổi trả</h2>
                  <span className={`${shared.sectionNote} num`}>{returns.length}</span>
                </div>

                {returns.map((ret) => {
                  const retMeta = RETURN_STATUS_META[ret.status];
                  return (
                    <article key={ret.id} className={styles.returnRequestItem}>
                      <div className={styles.returnRequestHead}>
                        <span className={`${shared.statusTag} ${statusToneClass(retMeta.tone)}`}>
                          {retMeta.label}
                        </span>
                        {ret.createdAt && (
                          <span className={shared.sectionNote}>
                            {renderOrderDateTime(ret.createdAt)}
                          </span>
                        )}
                      </div>

                      {ret.items.length > 0 && (
                        <div>
                          {ret.items.map((rit, idx) => {
                            const matched = rit.sku_id
                              ? itemLookupBySkuId.get(rit.sku_id)
                              : undefined;
                            const variantParts = matched
                              ? [
                                  matched.colorName,
                                  matched.size ? `Size ${matched.size}` : "",
                                ].filter(Boolean)
                              : [];
                            return (
                              <div
                                key={`${rit.order_item_id || rit.sku_id || idx}`}
                                className={styles.returnItemSummaryRow}
                              >
                                <span>
                                  {matched
                                    ? variantParts.length > 0
                                      ? `${matched.productName} (${variantParts.join(", ")})`
                                      : matched.productName
                                    : "Sản phẩm trong đơn"}
                                </span>
                                <span className="num">× {rit.quantity}</span>
                              </div>
                            );
                          })}
                        </div>
                      )}

                      {ret.reason && <p className={styles.returnBodyText}>{ret.reason}</p>}

                      {ret.photos.length > 0 && (
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
                                alt={`Ảnh đổi trả ${pIdx + 1}`}
                                className={styles.returnPhotoThumb}
                                loading="lazy"
                              />
                            </a>
                          ))}
                        </div>
                      )}

                      {ret.refundAmountVnd > 0 && (
                        <div className={styles.returnRefundRow}>
                          <span>{ret.status === "REFUNDED" ? "Đã hoàn" : "Hoàn dự kiến"}</span>
                          <strong className="num">{formatVnd(ret.refundAmountVnd)}</strong>
                        </div>
                      )}

                      {ret.reverseTrackingCode && (
                        <p className={`${styles.sideMuted} num`}>
                          Vận đơn hoàn: <strong>{ret.reverseTrackingCode}</strong>
                        </p>
                      )}

                      {ret.sellerNote && (
                        <div className={styles.returnNoteBox}>
                          <span className={styles.returnSubLabel}>Phản hồi từ {brandName}</span>
                          <p className={styles.returnBodyText}>{ret.sellerNote}</p>
                        </div>
                      )}

                      {ret.status === "REJECTED" && (
                        <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="note">
                          <p>
                            <strong>Hướng dẫn tiếp theo:</strong> Xem phản hồi từ {brandName}. Nếu
                            cần kiểm tra lại, nhắn gian hàng hoặc liên hệ hỗ trợ kèm mã đơn{" "}
                            <strong className="num">{order.orderCode}</strong>.
                          </p>
                        </div>
                      )}
                    </article>
                  );
                })}
              </section>
            )}

            {/* Secondary sections folded inside native <details> */}
            {order.timeline.length > 0 && (
              <details className={shared.disclosure}>
                <summary className={shared.disclosureSummary}>
                  <span>
                    Nhật ký hành trình (<span className="num">{order.timeline.length}</span>)
                  </span>
                  <IconChevronDown size={16} className={shared.disclosureChevron} />
                </summary>
                <div className={shared.disclosureBody}>
                  <ol className={styles.eventList}>
                    {order.timeline
                      .slice()
                      .reverse()
                      .map((ev, idx) => (
                        <li
                          key={`${ev.timestamp}-${idx}`}
                          className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                        >
                          <span className={styles.eventStem} aria-hidden="true">
                            <span className={styles.eventDot} />
                            <span className={styles.eventLine} />
                          </span>
                          <span className={styles.eventTime}>
                            {ev.timestamp ? renderOrderDateTime(ev.timestamp) : ""}
                          </span>
                          <div className={styles.eventBody}>
                            <span className={styles.eventTitle}>{ev.title}</span>
                            {ev.note && <span className={styles.eventNote}>{ev.note}</span>}
                          </div>
                        </li>
                      ))}
                  </ol>
                </div>
              </details>
            )}

            {order.canCancel && (
              <details className={shared.disclosure}>
                <summary className={shared.disclosureSummary}>
                  <span>Hủy đơn hàng</span>
                  <IconChevronDown size={16} className={shared.disclosureChevron} />
                </summary>
                <div className={shared.disclosureBody}>
                  <form className={styles.cancelForm} onSubmit={handleCancelSubmit}>
                    <div className={shared.field}>
                      <label htmlFor="cancel-reason" className={shared.fieldLabel}>
                        Lý do hủy
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
                      <Button
                        type="submit"
                        variant="danger"
                        size="md"
                        loading={cancelling}
                        loadingLabel="Đang hủy"
                      >
                        Xác nhận hủy đơn
                      </Button>
                    </div>
                  </form>
                </div>
              </details>
            )}
          </div>

          <aside className={styles.sideCol}>
            <div className={styles.summarySheet}>
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Địa chỉ nhận hàng</h2>
                <p className={`${styles.sideLine} ${styles.metaPair}`}>
                  <strong>{order.buyerName || "Người nhận"}</strong>
                  {order.buyerPhone && <span className="num">{order.buyerPhone}</span>}
                </p>
                <p className={styles.sideLine}>
                  {order.shippingAddress || "Đang cập nhật địa chỉ"}
                </p>
                {order.trackingCode && (
                  <p className={`${styles.sideMuted} ${styles.metaPair} num`}>
                    {order.carrier && <span>{order.carrier}</span>}
                    <span>
                      Vận đơn: <strong>{order.trackingCode}</strong>
                    </span>
                  </p>
                )}
              </div>

              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Thanh toán</h2>
                <p className={styles.sideLine}>{PAYMENT_METHOD_LABELS[order.paymentMethod]}</p>
                <p className={styles.sideMuted}>
                  {payMeta?.label ?? (order.isPaid ? "Đã thanh toán" : "Thu hộ khi nhận hàng")}
                </p>

                <dl className={styles.ledgerBox}>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Tạm tính</dt>
                    <dd className="num">{formatVnd(order.subtotal)}</dd>
                  </div>
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Mã giảm giá</dt>
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
        <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={suggestedProducts} />
      )}

      {/* Step-by-step slide-over sheet for returns */}
      {returnSheetOpen && order.canRequestReturn && (
        <div
          className={shared.sheetBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) setReturnSheetOpen(false);
          }}
        >
          <div
            ref={returnSheetRef}
            tabIndex={-1}
            className={shared.sheetPanel}
            role="dialog"
            aria-modal="true"
            aria-labelledby="return-sheet-title"
          >
            <div className={shared.sheetHeader}>
              <h2 id="return-sheet-title" className={shared.sheetTitle}>
                Yêu cầu đổi trả
              </h2>
              <button
                type="button"
                className={shared.sheetCloseBtn}
                aria-label="Đóng bảng đổi trả"
                onClick={() => setReturnSheetOpen(false)}
              >
                <IconClose size={18} />
              </button>
            </div>

            <form className={shared.sheetBody} onSubmit={handleReturnSubmit} noValidate>
              <div className={styles.sheetStepBar} aria-label="Tiến độ yêu cầu đổi trả">
                {RETURN_STEP_LABELS.map((label, idx) => (
                  <div
                    key={label}
                    className={`${styles.sheetStepSeg} ${
                      idx <= returnStep ? styles.sheetStepSegActive : ""
                    }`}
                  >
                    <span className={styles.sheetStepTrack} aria-hidden="true">
                      <span className={styles.sheetStepTrackFill} />
                    </span>
                    <span className={styles.sheetStepName}>{label}</span>
                  </div>
                ))}
              </div>

              {returnStep === 0 && (
                <div className={shared.field}>
                  <span className={shared.fieldLabel}>Chọn sản phẩm và số lượng</span>
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
                              width={48}
                              height={64}
                              className={styles.returnPickerThumb}
                            />
                            <span className={styles.returnPickerInfo}>
                              <span className={styles.returnPickerName}>{item.productName}</span>
                              <span className={styles.returnPickerMeta}>
                                <span>{item.colorName}</span>
                                {item.size && <span>{item.size}</span>}
                                <span className="num">{formatVnd(item.unitPrice)}</span>
                              </span>
                            </span>
                          </label>

                          <div
                            className={styles.qtyStepper}
                            aria-label={`Số lượng đổi trả ${item.productName}`}
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

                  {selectedReturnSummary.count > 0 && (
                    <p className={`${styles.sideLine} ${styles.metaPair}`}>
                      <span>
                        Đã chọn <strong className="num">{selectedReturnSummary.totalQty}</strong>
                      </span>
                      <span>
                        Tạm tính{" "}
                        <strong className="num">
                          {formatVnd(selectedReturnSummary.estimatedVnd)}
                        </strong>
                      </span>
                    </p>
                  )}
                </div>
              )}

              {returnStep === 1 && (
                <>
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
                      Mô tả chi tiết
                    </label>
                    <textarea
                      id="return-note"
                      className={`${shared.textarea} ${returnError ? shared.textareaInvalid : ""}`}
                      aria-invalid={Boolean(returnError) || undefined}
                      aria-describedby={returnError ? "return-form-error" : undefined}
                      value={returnDetail}
                      onChange={(e) => {
                        setReturnDetail(e.target.value);
                        if (returnError) setReturnError(null);
                      }}
                      placeholder="Áo còn nguyên tem, cần đổi từ size M sang L."
                      disabled={submittingReturn}
                    />
                  </div>

                  <div className={shared.field}>
                    <label htmlFor="return-photo-url" className={shared.fieldLabel}>
                      Ảnh minh chứng (tối đa {MAX_RETURN_PHOTOS})
                    </label>
                    <div className={shared.codeInputRow}>
                      <input
                        id="return-photo-url"
                        type="text"
                        className={shared.input}
                        aria-describedby={photoError ? "return-photo-error" : undefined}
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
                        placeholder="/products/... hoặc https://..."
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
                    {photoError && (
                      <p id="return-photo-error" className={shared.fieldError} role="alert">
                        {photoError}
                      </p>
                    )}

                    {returnPhotos.length > 0 && (
                      <div className={styles.returnPhotoDraftList}>
                        {returnPhotos.map((url, idx) => (
                          <div key={url} className={styles.returnPhotoDraftCard}>
                            <img
                              src={url}
                              alt={`Ảnh ${idx + 1}`}
                              className={styles.returnPhotoDraftImg}
                            />
                            <span className={styles.returnPhotoDraftUrl}>{url}</span>
                            <button
                              type="button"
                              className={styles.returnPhotoRemoveBtn}
                              aria-label={`Xóa ảnh ${idx + 1}`}
                              disabled={submittingReturn}
                              onClick={() => {
                                setReturnPhotos((prev) => prev.filter((u) => u !== url));
                                setPhotoError(null);
                              }}
                            >
                              <IconTrash size={14} />
                              <span>Xóa</span>
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </>
              )}

              {returnStep === 2 && (
                <div className={styles.confirmBox}>
                  <p className={styles.sideTitle}>Xác nhận gửi tới {brandName}</p>
                  <p className={`${styles.sideLine} ${styles.metaPair}`}>
                    <span>
                      <strong className="num">{selectedReturnSummary.totalQty}</strong> sản phẩm
                    </span>
                    <span>
                      Tạm tính{" "}
                      <strong className="num">
                        {formatVnd(selectedReturnSummary.estimatedVnd)}
                      </strong>
                    </span>
                  </p>
                  <p className={styles.sideLine}>
                    <strong>Lý do:</strong> {returnReason}
                  </p>
                  <p className={styles.sideLine}>{returnDetail.trim()}</p>
                  {returnPhotos.length > 0 && (
                    <p className={styles.sideMuted}>
                      Đính kèm <span className="num">{returnPhotos.length}</span> ảnh minh chứng.
                    </p>
                  )}
                </div>
              )}

              {returnError && (
                <p id="return-form-error" className={shared.fieldError} role="alert">
                  {returnError}
                </p>
              )}

              <div className={shared.sheetFooter} style={{ margin: "auto -24px -24px" }}>
                {returnStep > 0 ? (
                  <Button
                    type="button"
                    variant="secondary"
                    size="md"
                    disabled={submittingReturn}
                    onClick={() => {
                      returnStepEnteredAtRef.current = -Infinity;
                      setReturnError(null);
                      setReturnStep((s) => (s === 2 ? 1 : 0));
                    }}
                  >
                    Quay lại
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="secondary"
                    size="md"
                    onClick={() => setReturnSheetOpen(false)}
                  >
                    Đóng
                  </Button>
                )}

                {returnStep < 2 ? (
                  <Button
                    key="return-next"
                    type="button"
                    variant="ink"
                    size="md"
                    onClick={handleAdvanceReturnStep}
                  >
                    Tiếp tục
                  </Button>
                ) : (
                  <Button
                    key="return-submit"
                    type="submit"
                    variant="primary"
                    size="md"
                    loading={submittingReturn}
                    loadingLabel="Đang gửi"
                  >
                    Gửi yêu cầu đổi trả
                  </Button>
                )}
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}

export function OrderDetailClientView(props: OrderDetailClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView
            kind="loading"
            skeletonVariant="detail"
            title="Đang tải đơn hàng..."
            rows={5}
          />
        </div>
      }
    >
      <OrderDetailInner {...props} />
    </Suspense>
  );
}
