"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCTS } from "@/data";
import type {
  GatewayOrder,
  GatewayOrderReviewsResponse,
  GatewayOrderTimelineEvent,
  GatewayProductReview,
  GatewayPublicShop,
  GatewayRefund,
  GatewayReturnRequest,
  GatewayShipmentResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import {
  formatCarrierLabel,
  formatDateTimeVi,
  formatSignedVnd,
  formatVnDayKey,
  formatVnd,
} from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPackage, IconStar, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import { clearCheckoutWarnings, readCheckoutWarnings } from "@/features/checkout/helpers";
import {
  MAX_REVIEW_COMMENT_LEN,
  MAX_REVIEW_IMAGES,
  canReviewOrderStatus,
  validateReviewImageUrls,
} from "@/features/product/reviewHelpers";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/don-hang/[id]/page.module.css";
import type { OrderVisualLookup } from "./types";
import {
  type BadgeTone,
  MAX_EVIDENCE_URLS,
  MAX_REDELIVERY_ATTEMPTS,
  VN_BANK_OPTIONS,
  buildBuyerOrderBreakdownLines,
  buildShippingLabelHref,
  canCancelOrder,
  canConfirmReceived,
  canRequestRedelivery,
  canRequestReturn,
  formatUnpaidGroupCancelWarning,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
  getRefundStatusInfo,
  getReturnFeeBearerLabel,
  getReturnStatusInfo,
  getShipmentStatusInfo,
  getTimelineEventTitle,
  isRefundPending,
  localizeTimelineNote,
  needsVietQrPayment,
  normalizeEnumSuffix,
  parseSafeEvidenceUrl,
  resolveUnpaidVietQrCancelGroup,
  validateBankAccountNumber,
  validateEvidenceUrls,
} from "./orderMeta";

export interface OrderDetailClientProps {
  initialOrder: GatewayOrder | null;
  initialTimeline: GatewayOrderTimelineEvent[];
  initialShipment: GatewayShipmentResponse | null;
  initialReturnRequest?: GatewayReturnRequest | null;
  initialReturnShipment?: GatewayShipmentResponse | null;
  initialRefunds?: GatewayRefund[];
  initialReviews?: GatewayProductReview[];
  shop: GatewayPublicShop | null;
  visuals: OrderVisualLookup;
  orderId: string;
  initialGroupOrders?: GatewayOrder[];
  groupShopsMap?: Record<string, GatewayPublicShop>;
  initialError?: string | null;
  forcedState?: "loading" | "empty" | "error" | null;
}

const CANCEL_REASONS = [
  "Thay đổi địa chỉ nhận hàng sang khu vực khác",
  "Muốn đổi kích cỡ hoặc chọn thêm màu khác",
  "Quên áp mã ưu đãi hoặc muốn đổi phương thức thanh toán",
  "Đặt nhầm hoặc không còn nhu cầu mua",
];

const RETURN_REASONS = [
  "Sản phẩm có lỗi đường may hoặc bề mặt vải",
  "Giao nhầm màu sắc, kích cỡ hoặc nhầm mẫu",
  "Sản phẩm không đúng mô tả",
  "Muốn đổi kích cỡ phù hợp hơn",
];

function toneClass(tone: BadgeTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

function getMilestoneIndex(status: string): number {
  switch (status) {
    case "PENDING":
    case "CONFIRMED":
      return 0;
    case "PREPARING":
      return 1;
    case "SHIPPING":
      return 2;
    case "DELIVERED":
    case "COMPLETED":
      return 3;
    default:
      return -1;
  }
}

function EvidenceUrlInputs({
  idPrefix,
  urls,
  onChange,
  disabled,
}: {
  idPrefix: string;
  urls: string[];
  onChange: (next: string[]) => void;
  disabled: boolean;
}) {
  const handleItemChange = (idx: number, val: string) => {
    const next = [...urls];
    next[idx] = val;
    onChange(next);
  };

  const handleAdd = () => {
    if (urls.length >= MAX_EVIDENCE_URLS) return;
    onChange([...urls, ""]);
  };

  const handleRemove = (idx: number) => {
    if (urls.length <= 1) {
      onChange([""]);
      return;
    }
    onChange(urls.filter((_, i) => i !== idx));
  };

  return (
    <div className={shared.field}>
      <span className={shared.fieldLabel}>
        Liên kết bằng chứng (không bắt buộc, tối đa {MAX_EVIDENCE_URLS} liên kết)
      </span>
      <p className={shared.fieldHint}>
        Hiện chưa hỗ trợ tải ảnh trực tiếp, vui lòng dán liên kết ảnh/video bằng chứng bắt đầu bằng
        http:// hoặc https:// (tối đa 500 ký tự mỗi liên kết).
      </p>
      <div className={styles.evidenceList}>
        {urls.map((val, idx) => (
          <div key={`${idPrefix}-${idx}`} className={styles.evidenceRow}>
            <input
              id={`${idPrefix}-url-${idx}`}
              type="url"
              className={shared.input}
              value={val}
              onChange={(e) => handleItemChange(idx, e.target.value)}
              placeholder={`https://... (liên kết bằng chứng ${idx + 1})`}
              aria-label={`Liên kết bằng chứng thứ ${idx + 1}`}
              disabled={disabled}
            />
            {urls.length > 1 && (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                disabled={disabled}
                onClick={() => handleRemove(idx)}
              >
                Xóa
              </Button>
            )}
          </div>
        ))}
      </div>
      {urls.length < MAX_EVIDENCE_URLS && (
        <div className={shared.formActions}>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={disabled}
            onClick={handleAdd}
          >
            Thêm liên kết bằng chứng ({urls.length}/{MAX_EVIDENCE_URLS})
          </Button>
        </div>
      )}
    </div>
  );
}

export function OrderDetailClient({
  initialOrder,
  initialTimeline,
  initialShipment,
  initialReturnRequest = null,
  initialReturnShipment = null,
  initialRefunds = [],
  initialReviews = [],
  shop,
  visuals,
  orderId,
  initialGroupOrders = [],
  groupShopsMap = {},
  initialError,
  forcedState,
}: OrderDetailClientProps) {
  const router = useRouter();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();

  const [order, setOrder] = useState<GatewayOrder | null>(initialOrder);
  const [timeline, setTimeline] = useState<GatewayOrderTimelineEvent[]>(initialTimeline);
  const [shipmentData, setShipmentData] = useState<GatewayShipmentResponse | null>(initialShipment);
  const [returnRequest, setReturnRequest] = useState<GatewayReturnRequest | null>(
    initialReturnRequest
  );
  const [returnShipmentData, setReturnShipmentData] = useState<GatewayShipmentResponse | null>(
    initialReturnShipment
  );
  const [refunds, setRefunds] = useState<GatewayRefund[]>(initialRefunds);
  const [orderReviews, setOrderReviews] = useState<GatewayProductReview[]>(initialReviews);
  const [reviewingItemId, setReviewingItemId] = useState<string | null>(null);
  const [reviewRating, setReviewRating] = useState<number>(5);
  const [reviewComment, setReviewComment] = useState<string>("");
  const [reviewImageUrls, setReviewImageUrls] = useState<string[]>([""]);
  const [submittingReview, setSubmittingReview] = useState<boolean>(false);
  const [reviewError, setReviewError] = useState<string | null>(null);
  const [groupOrders, setGroupOrders] = useState<GatewayOrder[]>(initialGroupOrders);
  const [checkoutWarnings, setCheckoutWarnings] = useState<string[]>([]);

  useEffect(() => {
    const id = order?.id || orderId;
    if (!id) return;
    const found = readCheckoutWarnings({ orderId: id, paymentId: order?.payment_id });
    if (found.length > 0) {
      setCheckoutWarnings(found);
    }
  }, [order?.id, order?.payment_id, orderId]);

  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);
  const [cancelNote, setCancelNote] = useState("");
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [confirmGroupCancelOpen, setConfirmGroupCancelOpen] = useState(false);

  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);

  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [returnEvidenceUrls, setReturnEvidenceUrls] = useState<string[]>([""]);
  const [returning, setReturning] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);

  const [escalateReason, setEscalateReason] = useState("");
  const [escalateUrls, setEscalateUrls] = useState<string[]>([""]);
  const [escalating, setEscalating] = useState(false);
  const [escalateError, setEscalateError] = useState<string | null>(null);

  const [evidenceNote, setEvidenceNote] = useState("");
  const [evidenceExtraUrls, setEvidenceExtraUrls] = useState<string[]>([""]);
  const [submittingEvidence, setSubmittingEvidence] = useState(false);
  const [evidenceError, setEvidenceError] = useState<string | null>(null);

  const [redeliveryDate, setRedeliveryDate] = useState("");
  const [redeliveryNote, setRedeliveryNote] = useState("");
  const [requestingRedelivery, setRequestingRedelivery] = useState(false);
  const [redeliveryError, setRedeliveryError] = useState<string | null>(null);

  const [editingBankRefundId, setEditingBankRefundId] = useState<string | null>(null);
  const [bankCode, setBankCode] = useState(VN_BANK_OPTIONS[0].code);
  const [bankAccountNumber, setBankAccountNumber] = useState("");
  const [bankAccountName, setBankAccountName] = useState("");
  const [savingBank, setSavingBank] = useState(false);
  const [bankError, setBankError] = useState<string | null>(null);

  const [reordering, setReordering] = useState(false);

  const cancelGroupInfo = useMemo(
    () => resolveUnpaidVietQrCancelGroup(order, groupOrders),
    [order, groupOrders]
  );

  useEffect(() => {
    if (!confirmGroupCancelOpen) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !cancelling) {
        setConfirmGroupCancelOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [confirmGroupCancelOpen, cancelling]);

  useRevealOnScroll(
    `${forcedState ?? "live"}-${order?.id ?? "none"}-${order?.status ?? ""}-${order?.payment_status ?? ""}`
  );

  const suggestedProducts = useMemo(() => PRODUCTS.slice(0, 4), []);

  const refreshOrder = useCallback(async () => {
    const id = encodeURIComponent(orderId);
    try {
      const [orderRes, timelineRes, shipRes, returnRes, returnShipRes, refundsRes] =
        await Promise.all([
          api<{ order: GatewayOrder }>(`/orders/${id}`),
          api<{ events: GatewayOrderTimelineEvent[] }>(`/orders/${id}/timeline`).catch(() => null),
          api<GatewayShipmentResponse>(`/shipping/shipments/${id}`).catch(() => null),
          api<{ return_request: GatewayReturnRequest }>(`/orders/${id}/return`).catch(() => null),
          api<GatewayShipmentResponse>(`/shipping/shipments/${id}?direction=RETURN`).catch(
            () => null
          ),
          api<PaginatedResponse<GatewayRefund>>(`/orders/${id}/refunds?page=1&page_size=20`).catch(
            () => null
          ),
        ]);
      if (orderRes?.order) {
        setOrder(orderRes.order);
        if (timelineRes && Array.isArray(timelineRes.events)) {
          setTimeline(timelineRes.events);
        } else if (Array.isArray(orderRes.order.timeline)) {
          setTimeline(orderRes.order.timeline);
        }
        if (canReviewOrderStatus(orderRes.order.status)) {
          const revRes = await api<GatewayOrderReviewsResponse>(`/orders/${id}/reviews`).catch(
            () => null
          );
          if (revRes && Array.isArray(revRes.reviews)) {
            setOrderReviews(revRes.reviews);
          }
        }
      }
      if (shipRes?.shipment) {
        setShipmentData(shipRes);
      }
      if (returnRes?.return_request) {
        setReturnRequest(returnRes.return_request);
      }
      if (returnShipRes?.shipment) {
        setReturnShipmentData(returnShipRes);
      }
      if (refundsRes && Array.isArray(refundsRes.items)) {
        setRefunds(refundsRes.items);
      }
    } catch {
      router.refresh();
    }
  }, [orderId, router]);

  const handleOpenReviewForm = (itemId: string) => {
    setReviewingItemId((cur) => (cur === itemId ? null : itemId));
    setReviewRating(5);
    setReviewComment("");
    setReviewImageUrls([""]);
    setReviewError(null);
  };

  const handleSubmitReview = async (e: React.FormEvent, itemId: string) => {
    e.preventDefault();
    if (!order || !canReviewOrderStatus(order.status) || submittingReview) return;
    if (reviewRating < 1 || reviewRating > 5) {
      setReviewError("Vui lòng chọn số sao đánh giá từ 1 đến 5.");
      return;
    }
    const cleanComment = reviewComment.trim();
    if (cleanComment.length > MAX_REVIEW_COMMENT_LEN) {
      setReviewError(`Nội dung đánh giá tối đa ${MAX_REVIEW_COMMENT_LEN} ký tự.`);
      return;
    }
    const { urls, error: imgErr } = validateReviewImageUrls(reviewImageUrls);
    if (imgErr) {
      setReviewError(imgErr);
      return;
    }

    setReviewError(null);
    setSubmittingReview(true);
    try {
      const res = await api<{ review: GatewayProductReview }>(
        `/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          method: "POST",
          json: {
            order_item_id: itemId,
            rating: reviewRating,
            comment: cleanComment,
            image_urls: urls,
          },
        }
      );
      if (res?.review) {
        setOrderReviews((prev) => [...prev.filter((r) => r.order_item_id !== itemId), res.review]);
      }
      setReviewingItemId(null);
      setReviewComment("");
      setReviewImageUrls([""]);
      showToast("Đã gửi đánh giá sản phẩm thành công.");
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setReviewError("Bạn đã đánh giá sản phẩm này trong đơn hàng.");
        void refreshOrder();
      } else {
        setReviewError(getErrorMessage(err, "Không thể gửi đánh giá lúc này. Vui lòng thử lại."));
      }
    } finally {
      setSubmittingReview(false);
    }
  };

  const handleReorder = async () => {
    if (!order || reordering || order.items.length === 0) return;
    setReordering(true);
    let added = 0;
    let clamped = false;
    let lastError = "";
    try {
      for (const item of order.items) {
        const res = await addToCart({
          skuId: item.sku_id,
          quantity: item.quantity,
        });
        if (res.ok) {
          added += 1;
          if (res.clampedToStock) clamped = true;
        } else if (res.message) {
          lastError = res.message;
        }
      }
      if (added > 0) {
        showToast(
          clamped
            ? `Đã thêm ${added} sản phẩm vào giỏ hàng (đã điều chỉnh theo số lượng còn sẵn).`
            : `Đã đưa lại ${added} sản phẩm vào giỏ hàng.`
        );
        router.push("/gio-hang");
      } else {
        showToast(lastError || "Các sản phẩm trong đơn này hiện không còn sẵn để mua lại.");
      }
    } finally {
      setReordering(false);
    }
  };

  const executeCancelOrder = async () => {
    if (!order || cancelling) return;
    const wasMultiShopGroup = cancelGroupInfo.isMultiShopGroup;
    const groupShopCount = cancelGroupInfo.shopCount;
    const targetGroupId = order.checkout_group_id;

    setCancelError(null);
    setCancelling(true);
    const fullReason = cancelNote.trim() ? `${cancelReason}: ${cancelNote.trim()}` : cancelReason;
    try {
      const res = await api<{ order: GatewayOrder }>(
        `/orders/${encodeURIComponent(order.id)}/cancel`,
        {
          method: "POST",
          json: { reason: fullReason },
        }
      );
      if (res?.order) {
        setOrder(res.order);
        if (Array.isArray(res.order.timeline) && res.order.timeline.length > 0) {
          setTimeline(res.order.timeline);
        }
      }
      setConfirmGroupCancelOpen(false);
      setCancelNote("");
      showToast(
        wasMultiShopGroup
          ? `Đã hủy toàn bộ lần đặt hàng gồm ${groupShopCount} gian hàng.`
          : "Đã hủy đơn hàng thành công."
      );
      await refreshOrder();
      if (wasMultiShopGroup && targetGroupId) {
        try {
          const refreshedList = await api<PaginatedResponse<GatewayOrder>>(
            "/orders?page=1&page_size=50"
          );
          if (refreshedList && Array.isArray(refreshedList.items)) {
            const refreshedSiblings = refreshedList.items.filter(
              (item) => item.checkout_group_id === targetGroupId
            );
            if (refreshedSiblings.length > 0) {
              setGroupOrders(refreshedSiblings);
            }
          }
        } catch {
          // Ignore secondary list reload errors; router.refresh refreshes server cache below.
        }
        router.refresh();
      }
    } catch (err) {
      setCancelError(getErrorMessage(err, "Chưa thể hủy đơn hàng lúc này. Vui lòng thử lại."));
    } finally {
      setCancelling(false);
    }
  };

  const handleCancelSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || cancelling) return;
    setCancelError(null);
    if (cancelGroupInfo.isMultiShopGroup) {
      setConfirmGroupCancelOpen(true);
      return;
    }
    void executeCancelOrder();
  };

  const handleConfirmReceived = async () => {
    if (!order || confirming) return;
    setConfirmError(null);
    setConfirming(true);
    try {
      const res = await api<{ order: GatewayOrder }>(
        `/orders/${encodeURIComponent(order.id)}/confirm-received`,
        { method: "POST" }
      );
      if (res?.order) {
        setOrder(res.order);
        if (Array.isArray(res.order.timeline) && res.order.timeline.length > 0) {
          setTimeline(res.order.timeline);
        }
      }
      showToast("Đã xác nhận nhận hàng. Đơn hàng hoàn tất!");
      await refreshOrder();
    } catch (err) {
      setConfirmError(
        getErrorMessage(err, "Chưa thể xác nhận đã nhận hàng lúc này. Vui lòng thử lại.")
      );
    } finally {
      setConfirming(false);
    }
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || returning) return;
    const cleanDetail = returnDetail.trim();
    if (cleanDetail.length < 6) {
      setReturnError("Vui lòng mô tả ngắn tình trạng sản phẩm cần trả hàng (tối thiểu 6 ký tự).");
      return;
    }
    const urlCheck = validateEvidenceUrls(returnEvidenceUrls);
    if (!urlCheck.ok) {
      setReturnError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    setReturnError(null);
    setReturning(true);
    try {
      const res = await api<{ order: GatewayOrder; return_request_id?: string }>(
        `/orders/${encodeURIComponent(order.id)}/return`,
        {
          method: "POST",
          json: {
            reason: `${returnReason}. Ghi chú: ${cleanDetail}`,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.order) {
        setOrder(res.order);
        if (Array.isArray(res.order.timeline) && res.order.timeline.length > 0) {
          setTimeline(res.order.timeline);
        }
      }
      setReturnDetail("");
      setReturnEvidenceUrls([""]);
      showToast("Đã gửi yêu cầu trả hàng đến gian hàng.");
      await refreshOrder();
    } catch (err) {
      setReturnError(
        getErrorMessage(err, "Chưa thể gửi yêu cầu trả hàng lúc này. Vui lòng thử lại.")
      );
    } finally {
      setReturning(false);
    }
  };

  const handleEscalateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!returnRequest || escalating) return;
    const reason = escalateReason.trim();
    if (reason.length < 6) {
      setEscalateError("Vui lòng nêu rõ lý do khiếu nại (tối thiểu 6 ký tự).");
      return;
    }
    const urlCheck = validateEvidenceUrls(escalateUrls);
    if (!urlCheck.ok) {
      setEscalateError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    setEscalateError(null);
    setEscalating(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `/returns/${encodeURIComponent(returnRequest.id)}/escalate`,
        {
          method: "POST",
          json: {
            reason,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturnRequest(res.return_request);
      }
      setEscalateReason("");
      setEscalateUrls([""]);
      showToast("Đã gửi khiếu nại lên bộ phận quản trị NexusMart.");
      await refreshOrder();
    } catch (err) {
      setEscalateError(getErrorMessage(err, "Chưa thể mở khiếu nại lúc này. Vui lòng thử lại."));
    } finally {
      setEscalating(false);
    }
  };

  const handleAddEvidenceSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!returnRequest || submittingEvidence) return;
    const note = evidenceNote.trim();
    const urlCheck = validateEvidenceUrls(evidenceExtraUrls);
    if (!urlCheck.ok) {
      setEvidenceError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    if (note.length < 4 && urlCheck.urls.length === 0) {
      setEvidenceError("Vui lòng nhập ghi chú bổ sung hoặc ít nhất một liên kết bằng chứng.");
      return;
    }
    setEvidenceError(null);
    setSubmittingEvidence(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `/returns/${encodeURIComponent(returnRequest.id)}/evidence`,
        {
          method: "POST",
          json: {
            note,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturnRequest(res.return_request);
      }
      setEvidenceNote("");
      setEvidenceExtraUrls([""]);
      showToast("Đã bổ sung bằng chứng cho hồ sơ khiếu nại.");
      await refreshOrder();
    } catch (err) {
      setEvidenceError(
        getErrorMessage(err, "Chưa thể gửi bằng chứng bổ sung lúc này. Vui lòng thử lại.")
      );
    } finally {
      setSubmittingEvidence(false);
    }
  };

  const handleRedeliverySubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || requestingRedelivery) return;
    setRedeliveryError(null);
    setRequestingRedelivery(true);
    try {
      await api<{ shipment: GatewayShipmentResponse["shipment"] }>(
        `/orders/${encodeURIComponent(order.id)}/redelivery`,
        {
          method: "POST",
          json: {
            preferred_date: redeliveryDate.trim(),
            note: redeliveryNote.trim(),
          },
        }
      );
      setRedeliveryDate("");
      setRedeliveryNote("");
      showToast("Đã gửi yêu cầu giao lại cho đơn vị vận chuyển.");
      await refreshOrder();
    } catch (err) {
      setRedeliveryError(
        getErrorMessage(err, "Chưa thể gửi yêu cầu giao lại lúc này. Vui lòng thử lại.")
      );
    } finally {
      setRequestingRedelivery(false);
    }
  };

  const handleOpenBankForm = (rf: GatewayRefund) => {
    setEditingBankRefundId(rf.refund_id);
    setBankCode(rf.bank_code || VN_BANK_OPTIONS[0].code);
    setBankAccountNumber("");
    setBankAccountName(rf.bank_account_name || "");
    setBankError(null);
  };

  const handleSubmitBankAccount = async (e: React.FormEvent, rf: GatewayRefund) => {
    e.preventDefault();
    if (savingBank) return;
    const isEditingThis = editingBankRefundId === rf.refund_id;
    const activeCode = (isEditingThis ? bankCode : rf.bank_code || VN_BANK_OPTIONS[0].code).trim();
    const cleanNum = (isEditingThis ? bankAccountNumber : "").replace(/\s+/g, "").trim();
    const cleanName = (isEditingThis ? bankAccountName : rf.bank_account_name || "").trim();
    if (!activeCode) {
      setEditingBankRefundId(rf.refund_id);
      setBankError("Vui lòng chọn ngân hàng nhận tiền.");
      return;
    }
    if (!validateBankAccountNumber(cleanNum)) {
      setEditingBankRefundId(rf.refund_id);
      setBankError("Số tài khoản ngân hàng cần gồm 6 đến 19 chữ số.");
      return;
    }
    if (cleanName.length < 2) {
      setEditingBankRefundId(rf.refund_id);
      setBankError("Vui lòng nhập đầy đủ họ tên chủ tài khoản ngân hàng.");
      return;
    }
    setBankError(null);
    setSavingBank(true);
    try {
      const res = await api<{ refund: GatewayRefund }>(
        `/refunds/${encodeURIComponent(rf.refund_id)}/bank-account`,
        {
          method: "POST",
          json: {
            bank_code: activeCode,
            bank_account_number: cleanNum,
            bank_account_name: cleanName,
          },
        }
      );
      if (res?.refund) {
        setRefunds((prev) =>
          prev.map((item) => (item.refund_id === rf.refund_id ? res.refund : item))
        );
      }
      setEditingBankRefundId(null);
      setBankAccountNumber("");
      showToast("Đã lưu thông tin tài khoản nhận hoàn tiền.");
      await refreshOrder();
    } catch (err) {
      setEditingBankRefundId(rf.refund_id);
      setBankError(
        getErrorMessage(err, "Chưa thể lưu tài khoản ngân hàng lúc này. Vui lòng thử lại.")
      );
    } finally {
      setSavingBank(false);
    }
  };

  const renderBreadcrumb = () => (
    <nav className={shared.breadcrumbNav} aria-label="Đường dẫn đơn hàng">
      <Link href="/don-hang" className={shared.breadcrumbLink}>
        Đơn hàng của tôi
      </Link>
      <span aria-hidden="true">/</span>
      <span className="num">{order?.order_code ?? orderId}</span>
    </nav>
  );

  if (forcedState === "loading") {
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
        <ProductAddonStrip title="Sản phẩm cơ bản từ NexusMart" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "error" || initialError) {
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
              initialError || "Hệ thống theo dõi đơn hàng đang tạm gián đoạn. Vui lòng thử tải lại."
            }
            actionLabel="Quay lại danh sách đơn"
            actionHref="/don-hang"
          />
        </section>
        <ProductAddonStrip title="Sản phẩm gợi ý" products={suggestedProducts} />
      </div>
    );
  }

  if (forcedState === "empty" || !order) {
    return (
      <div className={shared.page}>
        {renderBreadcrumb()}
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Không tìm thấy đơn</h1>
            <p className={styles.detailLead}>
              Đơn hàng bạn mở không thuộc tài khoản hiện tại hoặc không còn tồn tại.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái tra cứu</h2>
          <StateView
            kind="empty"
            title="Không tìm thấy đơn hàng tương ứng"
            message="Kiểm tra danh sách đơn hàng của bạn hoặc quay lại mua sắm."
            actionLabel="Về danh sách đơn hàng"
            actionHref="/don-hang"
          />
        </section>
        <ProductAddonStrip title="Gợi ý sản phẩm cơ bản" products={suggestedProducts} />
      </div>
    );
  }

  const statusMeta = getOrderStatusInfo(order.status);
  const payMeta = getPaymentStatusInfo(order.payment_status, order.payment_method, order.status);
  const needsRetry = needsVietQrPayment(order);
  const currentStepIdx = getMilestoneIndex(order.status);
  const isTerminalSpecial =
    order.status === "CANCELLED" ||
    order.status === "RETURN_REQUESTED" ||
    order.status === "RETURNED";
  const allowCancel = canCancelOrder(order.status);
  const allowConfirm = canConfirmReceived(order.status);
  const allowReturn = canRequestReturn(order.status) && !returnRequest;
  const awaitingCarrierPickup = order.status === "PREPARING" && Boolean(order.tracking_code);

  const milestoneSteps = [
    {
      key: "confirm",
      label: order.status === "PENDING" ? "Chờ thanh toán" : "Đã xác nhận",
    },
    {
      key: "preparing",
      label: awaitingCarrierPickup ? "Chờ hãng lấy hàng" : "Đang chuẩn bị",
    },
    { key: "shipping", label: "Đang giao hàng" },
    {
      key: "done",
      label: order.status === "COMPLETED" ? "Hoàn tất" : "Đã giao hàng",
    },
  ];

  const sortedTimeline = [...timeline].sort((a, b) => {
    const ta = a.created_at ? Date.parse(a.created_at) : 0;
    const tb = b.created_at ? Date.parse(b.created_at) : 0;
    return tb - ta;
  });

  const shipment = shipmentData?.shipment ?? null;
  const shipmentEvents = Array.isArray(shipmentData?.events)
    ? [...shipmentData.events].sort((a, b) => {
        const ta = a.occurred_at ? Date.parse(a.occurred_at) : 0;
        const tb = b.occurred_at ? Date.parse(b.occurred_at) : 0;
        return tb - ta;
      })
    : [];
  const forwardShipmentFailed =
    Boolean(shipment) &&
    normalizeEnumSuffix(shipment?.status, "SHIPMENT_STATUS_") === "FAILED" &&
    normalizeEnumSuffix(shipment?.direction || "FORWARD", "SHIPMENT_DIRECTION_") === "FORWARD";
  const allowRedelivery = canRequestRedelivery(shipment);
  const minRedeliveryDate = formatVnDayKey(Date.now());

  const returnStatusKey = normalizeEnumSuffix(returnRequest?.status, "RETURN_STATUS_");
  const returnMeta = returnRequest ? getReturnStatusInfo(returnRequest.status) : null;
  const returnEvents = Array.isArray(returnRequest?.events)
    ? [...returnRequest.events].sort((a, b) => {
        const ta = a.created_at ? Date.parse(a.created_at) : 0;
        const tb = b.created_at ? Date.parse(b.created_at) : 0;
        return tb - ta;
      })
    : [];
  const returnShipment = returnShipmentData?.shipment ?? null;
  const returnShipmentEvents = Array.isArray(returnShipmentData?.events)
    ? [...returnShipmentData.events].sort((a, b) => {
        const ta = a.occurred_at ? Date.parse(a.occurred_at) : 0;
        const tb = b.occurred_at ? Date.parse(b.occurred_at) : 0;
        return tb - ta;
      })
    : [];
  const showRefundSection =
    refunds.length > 0 || returnStatusKey === "REFUNDING" || returnStatusKey === "REFUNDED";

  const shopName = shop?.name ?? "Gian hàng NexusMart";

  return (
    <div className={shared.page}>
      {renderBreadcrumb()}

      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${shared.pageTitle} lines num`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Chi tiết đơn {order.order_code}
              </span>
            </span>
          </h1>
          <p className={styles.detailLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Đơn hàng tại <strong>{shopName}</strong>
            {order.created_at ? (
              <>
                , đặt lúc <span className="num">{formatDateTimeVi(order.created_at)}</span>
              </>
            ) : null}
            . Theo dõi giao nhận và quản lý trạng thái đơn hàng.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <span className={`${shared.statusTag} ${toneClass(payMeta.tone)}`}>{payMeta.label}</span>
          <span className={`${shared.statusTag} ${toneClass(statusMeta.tone)}`}>
            {statusMeta.label}
          </span>
          {needsRetry && (
            <Button
              variant="primary"
              size="md"
              href={`/thanh-toan/cho-thanh-toan/${encodeURIComponent(order.payment_id)}`}
            >
              Thanh toán VietQR
            </Button>
          )}
          {order.items.length > 0 && (
            <Button
              variant="ink"
              size="md"
              disabled={reordering}
              onClick={() => void handleReorder()}
            >
              {reordering ? "Đang thêm vào giỏ..." : "Mua lại"}
            </Button>
          )}
        </div>
      </section>

      <section className={shared.section}>
        {checkoutWarnings.length > 0 && (
          <div
            className={`${shared.feedbackBox} ${shared.feedbackWarn} ${shared.noticeSpacer}`}
            role="status"
          >
            <div>
              <strong>Lưu ý về ưu đãi khi tạo đơn:</strong>
              {checkoutWarnings.map((w, idx) => (
                <p key={idx} className={shared.noticeItem}>
                  {w}
                </p>
              ))}
              <p className={shared.noticeSubtle}>
                Tổng tiền hiển thị trên đơn là số tiền cuối cùng do hệ thống xác nhận.
              </p>
            </div>
            <div className={shared.formActions}>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  clearCheckoutWarnings();
                  setCheckoutWarnings([]);
                }}
              >
                Đã hiểu
              </Button>
            </div>
          </div>
        )}
        <div className={styles.detailGrid}>
          {/* Left column: VietQR retry highlight + ONE white order sheet + unboxed state-gated actions */}
          <div className={styles.mainCol}>
            {needsRetry && (
              <div
                className={styles.retryStack}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <RetryPaymentBox
                  paymentId={order.payment_id}
                  onPaid={() => {
                    showToast(`Đơn ${order.order_code} đã hoàn tất thanh toán.`);
                    void refreshOrder();
                  }}
                  onPaymentReplaced={(next) => {
                    setOrder((prev) =>
                      prev
                        ? {
                            ...prev,
                            payment_id: next.payment_id,
                            payment_status: next.status,
                            payment_expires_at: next.expires_at,
                          }
                        : prev
                    );
                    void refreshOrder();
                  }}
                />
              </div>
            )}

            <div className={styles.orderSheet}>
              {/* Timeline section */}
              <div data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Tiến trình đơn hàng</h2>
                  {order.updated_at && (
                    <span className={`${shared.sectionNote} num`}>
                      Cập nhật: {formatDateTimeVi(order.updated_at)}
                    </span>
                  )}
                </div>

                {!isTerminalSpecial ? (
                  <div className={styles.milestoneGrid}>
                    {milestoneSteps.map((step, idx) => {
                      const done =
                        currentStepIdx > idx ||
                        (order.status === "COMPLETED" && idx === milestoneSteps.length - 1);
                      const current = currentStepIdx === idx && order.status !== "COMPLETED";
                      return (
                        <div
                          key={step.key}
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
                      Trạng thái đơn hàng: <strong>{statusMeta.label}</strong>
                    </p>
                    <p>
                      {order.cancel_reason
                        ? localizeTimelineNote(order.cancel_reason)
                        : statusMeta.description}
                    </p>
                  </div>
                )}

                {sortedTimeline.length > 0 ? (
                  <ol className={styles.eventList}>
                    {sortedTimeline.map((ev, idx) => (
                      <li
                        key={ev.id || `${ev.created_at ?? idx}-${idx}`}
                        className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                        data-reveal=""
                      >
                        <span className={`${styles.eventTime} num`}>
                          {ev.created_at ? formatDateTimeVi(ev.created_at) : "—"}
                        </span>
                        <div className={styles.eventBody}>
                          <span className={styles.eventTitle}>{getTimelineEventTitle(ev)}</span>
                          {ev.note && (
                            <span className={styles.eventNote}>
                              {localizeTimelineNote(ev.note)}
                            </span>
                          )}
                        </div>
                      </li>
                    ))}
                  </ol>
                ) : (
                  <p className={styles.sideMuted} style={{ marginTop: 16 }}>
                    Chưa có mốc cập nhật chi tiết nào cho đơn hàng này.
                  </p>
                )}
              </div>

              {/* Real shipment block — hidden when no shipment exists */}
              {shipment && (
                <div className={styles.sheetDivider} data-reveal="">
                  <div className={shared.sectionHead}>
                    <h2 className={shared.sectionTitle}>Thông tin vận đơn</h2>
                    <span
                      className={`${shared.statusTag} ${toneClass(
                        getShipmentStatusInfo(shipment.status).tone
                      )}`}
                    >
                      {getShipmentStatusInfo(shipment.status).label}
                    </span>
                  </div>

                  <dl className={styles.shipmentMetaGrid}>
                    <div className={styles.shipmentMetaItem}>
                      <dt className={styles.shipmentKey}>Đơn vị vận chuyển</dt>
                      <dd className={styles.shipmentVal}>
                        {formatCarrierLabel(shipment.carrier_code)}
                      </dd>
                    </div>
                    <div className={styles.shipmentMetaItem}>
                      <dt className={styles.shipmentKey}>Mã vận đơn</dt>
                      <dd className={`${styles.shipmentVal} num`}>
                        {shipment.tracking_code || "Đang cấp mã"}
                      </dd>
                    </div>
                    <div className={styles.shipmentMetaItem}>
                      <dt className={styles.shipmentKey}>Phí giao hàng</dt>
                      <dd className={`${styles.shipmentVal} num`}>{formatVnd(shipment.fee_vnd)}</dd>
                    </div>
                    {shipment.cod_amount_vnd > 0 && (
                      <div className={styles.shipmentMetaItem}>
                        <dt className={styles.shipmentKey}>Thu hộ COD</dt>
                        <dd className={`${styles.shipmentVal} num`}>
                          {formatVnd(shipment.cod_amount_vnd)}
                        </dd>
                      </div>
                    )}
                  </dl>

                  {(() => {
                    const forwardLabelHref = buildShippingLabelHref(shipment.tracking_code);
                    if (!forwardLabelHref) return null;
                    return (
                      <div className={shared.formActions} style={{ marginTop: 12 }}>
                        <a
                          href={forwardLabelHref}
                          target="_blank"
                          rel="noopener noreferrer"
                          className={shared.breadcrumbLink}
                        >
                          Mở phiếu giao hàng
                        </a>
                        <span className={styles.sideMuted}>
                          (Mở tab mới, nhấn Ctrl+P hoặc ⌘P để in)
                        </span>
                      </div>
                    );
                  })()}

                  {shipmentEvents.length > 0 && (
                    <ol className={styles.eventList}>
                      {shipmentEvents.map((ev, idx) => (
                        <li
                          key={ev.id || `${ev.occurred_at ?? idx}-${idx}`}
                          className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                        >
                          <span className={`${styles.eventTime} num`}>
                            {ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : "—"}
                          </span>
                          <div className={styles.eventBody}>
                            <span className={styles.eventTitle}>
                              {getShipmentStatusInfo(ev.status).label}
                            </span>
                            {ev.description && (
                              <span className={styles.eventNote}>{ev.description}</span>
                            )}
                          </div>
                        </li>
                      ))}
                    </ol>
                  )}

                  {forwardShipmentFailed && (
                    <div className={styles.redeliveryBox}>
                      <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                        <h3 className={styles.sideTitle}>Yêu cầu giao lại đơn hàng</h3>
                        <span className={`${shared.sectionNote} num`}>
                          Đã yêu cầu {shipment.redelivery_count ?? 0}/{MAX_REDELIVERY_ATTEMPTS} lần
                        </span>
                      </div>
                      {allowRedelivery ? (
                        <form onSubmit={(e) => void handleRedeliverySubmit(e)} noValidate>
                          <p className={styles.actionPanelDesc} style={{ marginBottom: 12 }}>
                            Kiện hàng giao chưa thành công. Chọn ngày nhận mong muốn để hãng sắp xếp
                            giao lại (tối đa {MAX_REDELIVERY_ATTEMPTS} lần).
                          </p>
                          <div className={shared.formGrid}>
                            <div className={shared.field}>
                              <label htmlFor="redelivery-date" className={shared.fieldLabel}>
                                Ngày giao lại mong muốn (không bắt buộc)
                              </label>
                              <input
                                id="redelivery-date"
                                type="date"
                                min={minRedeliveryDate}
                                className={shared.input}
                                value={redeliveryDate}
                                onChange={(e) => setRedeliveryDate(e.target.value)}
                                disabled={requestingRedelivery}
                              />
                            </div>
                            <div className={shared.field}>
                              <label htmlFor="redelivery-note" className={shared.fieldLabel}>
                                Ghi chú cho bưu tá (không bắt buộc)
                              </label>
                              <input
                                id="redelivery-note"
                                type="text"
                                className={shared.input}
                                value={redeliveryNote}
                                onChange={(e) => setRedeliveryNote(e.target.value)}
                                placeholder="Ví dụ: Giao buổi chiều sau 14h, gọi trước 15 phút"
                                disabled={requestingRedelivery}
                              />
                            </div>
                          </div>
                          {redeliveryError && (
                            <p className={shared.fieldError} role="alert" style={{ marginTop: 8 }}>
                              {redeliveryError}
                            </p>
                          )}
                          <div className={shared.formActions}>
                            <Button
                              type="submit"
                              variant="primary"
                              size="md"
                              disabled={requestingRedelivery}
                            >
                              {requestingRedelivery
                                ? "Đang gửi yêu cầu giao lại..."
                                : "Gửi yêu cầu giao lại"}
                            </Button>
                          </div>
                        </form>
                      ) : (
                        <p className={styles.actionPanelDesc}>
                          Đơn hàng đã sử dụng hết {MAX_REDELIVERY_ATTEMPTS} lượt yêu cầu giao lại.
                          Kiện hàng sẽ được xử lý hoàn về gian hàng theo quy định.
                        </p>
                      )}
                    </div>
                  )}
                </div>
              )}

              {/* Return request progress & buyer dispute/evidence actions */}
              {returnRequest && returnMeta && (
                <div className={styles.sheetDivider} data-reveal="">
                  <div className={styles.returnCardStack}>
                    <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                      <h2 className={shared.sectionTitle}>Tiến trình trả hàng</h2>
                      <span className={`${shared.statusTag} ${toneClass(returnMeta.tone)}`}>
                        {returnMeta.label}
                      </span>
                    </div>

                    <p className={styles.actionPanelDesc}>{returnMeta.description}</p>

                    <dl className={styles.returnInfoGrid}>
                      {returnRequest.deadline_at && (
                        <div className={styles.shipmentMetaItem}>
                          <dt className={styles.shipmentKey}>Hạn xử lý kế tiếp</dt>
                          <dd className={`${styles.shipmentVal} num`}>
                            {formatDateTimeVi(returnRequest.deadline_at)}
                          </dd>
                        </div>
                      )}
                      {returnRequest.refund_amount_vnd > 0 && (
                        <div className={styles.shipmentMetaItem}>
                          <dt className={styles.shipmentKey}>Số tiền hoàn dự kiến</dt>
                          <dd className={`${styles.shipmentVal} num`}>
                            {formatVnd(returnRequest.refund_amount_vnd)}
                          </dd>
                        </div>
                      )}
                      {returnRequest.return_tracking_code && (
                        <div className={styles.shipmentMetaItem}>
                          <dt className={styles.shipmentKey}>Mã vận đơn chiều trả</dt>
                          <dd className={`${styles.shipmentVal} num`}>
                            {returnRequest.return_tracking_code}
                          </dd>
                        </div>
                      )}
                      {(returnRequest.return_fee_vnd > 0 || returnRequest.return_fee_bearer) && (
                        <div className={styles.shipmentMetaItem}>
                          <dt className={styles.shipmentKey}>Phí vận chuyển trả hàng</dt>
                          <dd className={`${styles.shipmentVal} num`}>
                            {returnRequest.return_fee_vnd > 0
                              ? `${formatVnd(returnRequest.return_fee_vnd)} · `
                              : ""}
                            {getReturnFeeBearerLabel(returnRequest.return_fee_bearer)}
                          </dd>
                        </div>
                      )}
                    </dl>

                    {(() => {
                      const returnLabelHref = buildShippingLabelHref(
                        returnRequest.return_tracking_code
                      );
                      if (!returnLabelHref) return null;
                      return (
                        <div className={shared.formActions}>
                          <a
                            href={returnLabelHref}
                            target="_blank"
                            rel="noopener noreferrer"
                            className={shared.breadcrumbLink}
                          >
                            Mở / in phiếu gửi hàng trả
                          </a>
                          <span className={styles.sideMuted}>
                            (Mở tab mới, nhấn Ctrl+P hoặc ⌘P để in)
                          </span>
                        </div>
                      );
                    })()}

                    <div className={styles.returnSubPanel}>
                      <p className={styles.sideLine}>
                        <strong>Lý do bạn đã gửi:</strong> {returnRequest.reason}
                      </p>
                      {(() => {
                        const buyerEv = (returnRequest.evidence_urls ?? [])
                          .map((u) => parseSafeEvidenceUrl(u))
                          .filter((x): x is NonNullable<typeof x> => x !== null);
                        if (buyerEv.length === 0) return null;
                        return (
                          <div className={styles.evidenceLinkList}>
                            <span className={styles.shipmentKey}>
                              Liên kết bằng chứng của bạn (mở trang ngoài)
                            </span>
                            {buyerEv.map((ev, idx) => (
                              <a
                                key={`${ev.href}-${idx}`}
                                href={ev.href}
                                target="_blank"
                                rel="noopener noreferrer nofollow"
                                className={styles.evidenceLink}
                              >
                                Bằng chứng #{idx + 1} ({ev.hostname})
                              </a>
                            ))}
                          </div>
                        );
                      })()}
                      {returnRequest.seller_response && (
                        <p className={styles.sideLine}>
                          <strong>Phản hồi từ gian hàng:</strong> {returnRequest.seller_response}
                        </p>
                      )}
                      {(() => {
                        const sellerEv = (returnRequest.seller_evidence_urls ?? [])
                          .map((u) => parseSafeEvidenceUrl(u))
                          .filter((x): x is NonNullable<typeof x> => x !== null);
                        if (sellerEv.length === 0) return null;
                        return (
                          <div className={styles.evidenceLinkList}>
                            <span className={styles.shipmentKey}>
                              Bằng chứng từ gian hàng (mở trang ngoài)
                            </span>
                            {sellerEv.map((ev, idx) => (
                              <a
                                key={`${ev.href}-${idx}`}
                                href={ev.href}
                                target="_blank"
                                rel="noopener noreferrer nofollow"
                                className={styles.evidenceLink}
                              >
                                Chứng từ gian hàng #{idx + 1} ({ev.hostname})
                              </a>
                            ))}
                          </div>
                        );
                      })()}
                      {returnRequest.dispute_reason && (
                        <p className={styles.sideLine}>
                          <strong>Nội dung khiếu nại / báo sai hàng:</strong>{" "}
                          {returnRequest.dispute_reason}
                        </p>
                      )}
                      {returnRequest.admin_note && (
                        <p className={styles.sideLine}>
                          <strong>Kết luận quản trị viên:</strong> {returnRequest.admin_note}
                        </p>
                      )}
                    </div>

                    {returnShipment && (
                      <div className={styles.returnSubPanel}>
                        <div className={styles.refundHead}>
                          <span className={styles.sideTitle}>Vận đơn chiều trả hàng</span>
                          <span
                            className={`${shared.statusTag} ${toneClass(
                              getShipmentStatusInfo(returnShipment.status).tone
                            )}`}
                          >
                            {getShipmentStatusInfo(returnShipment.status).label}
                          </span>
                        </div>
                        <p className={`${styles.sideMuted} num`}>
                          {formatCarrierLabel(returnShipment.carrier_code)} · Mã vận đơn:{" "}
                          <strong>{returnShipment.tracking_code}</strong>
                        </p>
                        {returnShipmentEvents.length > 0 && (
                          <ol className={styles.eventList} style={{ marginTop: 8 }}>
                            {returnShipmentEvents.map((ev, idx) => (
                              <li
                                key={ev.id || `${ev.occurred_at ?? idx}-${idx}`}
                                className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                              >
                                <span className={`${styles.eventTime} num`}>
                                  {ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : "—"}
                                </span>
                                <div className={styles.eventBody}>
                                  <span className={styles.eventTitle}>
                                    {getShipmentStatusInfo(ev.status).label}
                                  </span>
                                  {ev.description && (
                                    <span className={styles.eventNote}>{ev.description}</span>
                                  )}
                                </div>
                              </li>
                            ))}
                          </ol>
                        )}
                      </div>
                    )}

                    {returnEvents.length > 0 && (
                      <div>
                        <h3 className={styles.sideTitle}>Lịch sử xử lý yêu cầu trả hàng</h3>
                        <ol className={styles.eventList}>
                          {returnEvents.map((ev, idx) => (
                            <li
                              key={ev.id || `${ev.created_at ?? idx}-${idx}`}
                              className={`${styles.eventRow} ${idx === 0 ? styles.eventRowLatest : ""}`}
                            >
                              <span className={`${styles.eventTime} num`}>
                                {ev.created_at ? formatDateTimeVi(ev.created_at) : "—"}
                              </span>
                              <div className={styles.eventBody}>
                                <span className={styles.eventTitle}>
                                  {getReturnStatusInfo(ev.to_status).label}
                                </span>
                                {ev.note && (
                                  <span className={styles.eventNote}>
                                    {localizeTimelineNote(ev.note)}
                                  </span>
                                )}
                              </div>
                            </li>
                          ))}
                        </ol>
                      </div>
                    )}

                    {/* Buyer escalate when seller rejected */}
                    {returnStatusKey === "REJECTED" && (
                      <form
                        className={styles.returnSubPanel}
                        onSubmit={(e) => void handleEscalateSubmit(e)}
                        noValidate
                      >
                        <h3 className={styles.sideTitle}>Mở khiếu nại lên NexusMart</h3>
                        <p className={styles.actionPanelDesc}>
                          Nếu bạn không đồng ý với lý do từ chối của gian hàng, hãy gửi khiếu nại
                          cùng liên kết bằng chứng để quản trị viên NexusMart phân xử.
                        </p>
                        <div className={shared.field}>
                          <label htmlFor="escalate-reason" className={shared.fieldLabel}>
                            Lý do khiếu nại
                          </label>
                          <textarea
                            id="escalate-reason"
                            className={shared.textarea}
                            value={escalateReason}
                            onChange={(e) => setEscalateReason(e.target.value)}
                            placeholder="Trình bày rõ tình trạng sản phẩm và lý do không đồng ý với phản hồi từ gian hàng..."
                            disabled={escalating}
                            required
                          />
                        </div>
                        <EvidenceUrlInputs
                          idPrefix="escalate"
                          urls={escalateUrls}
                          onChange={setEscalateUrls}
                          disabled={escalating}
                        />
                        {escalateError && (
                          <p className={shared.fieldError} role="alert">
                            {escalateError}
                          </p>
                        )}
                        <div className={shared.formActions}>
                          <Button type="submit" variant="primary" size="md" disabled={escalating}>
                            {escalating ? "Đang gửi khiếu nại..." : "Gửi khiếu nại lên sàn"}
                          </Button>
                        </div>
                      </form>
                    )}

                    {/* Buyer submit extra evidence when admin requests evidence */}
                    {returnStatusKey === "EVIDENCE_REQUESTED" && (
                      <form
                        className={styles.returnSubPanel}
                        onSubmit={(e) => void handleAddEvidenceSubmit(e)}
                        noValidate
                      >
                        <h3 className={styles.sideTitle}>Bổ sung bằng chứng theo yêu cầu</h3>
                        <p className={styles.actionPanelDesc}>
                          Quản trị viên yêu cầu bổ sung thêm thông tin để hoàn tất phân xử trước hạn
                          chót.
                        </p>
                        <div className={shared.field}>
                          <label htmlFor="extra-evidence-note" className={shared.fieldLabel}>
                            Ghi chú bổ sung
                          </label>
                          <textarea
                            id="extra-evidence-note"
                            className={shared.textarea}
                            value={evidenceNote}
                            onChange={(e) => setEvidenceNote(e.target.value)}
                            placeholder="Mô tả chi tiết bổ sung hoặc ghi chú ảnh/video mở hộp..."
                            disabled={submittingEvidence}
                          />
                        </div>
                        <EvidenceUrlInputs
                          idPrefix="extra-evidence"
                          urls={evidenceExtraUrls}
                          onChange={setEvidenceExtraUrls}
                          disabled={submittingEvidence}
                        />
                        {evidenceError && (
                          <p className={shared.fieldError} role="alert">
                            {evidenceError}
                          </p>
                        )}
                        <div className={shared.formActions}>
                          <Button
                            type="submit"
                            variant="primary"
                            size="md"
                            disabled={submittingEvidence}
                          >
                            {submittingEvidence
                              ? "Đang gửi bằng chứng..."
                              : "Gửi bằng chứng bổ sung"}
                          </Button>
                        </div>
                      </form>
                    )}
                  </div>
                </div>
              )}

              {/* Refund status & buyer bank account form */}
              {showRefundSection && (
                <div className={styles.sheetDivider} data-reveal="">
                  <div className={styles.returnCardStack}>
                    <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                      <h2 className={shared.sectionTitle}>Thông tin hoàn tiền</h2>
                      <span className={`${shared.sectionNote} num`}>
                        {refunds.length} khoản hoàn tiền
                      </span>
                    </div>

                    {refunds.length === 0 ? (
                      <p className={styles.actionPanelDesc}>
                        Yêu cầu hoàn tiền đang được hệ thống khởi tạo. Vui lòng tải lại sau ít phút
                        để nhập tài khoản ngân hàng nhận tiền.
                      </p>
                    ) : (
                      refunds.map((rf) => {
                        const rfMeta = getRefundStatusInfo(rf.status);
                        const pending = isRefundPending(rf.status);
                        const hasBank = Boolean(rf.bank_code && rf.bank_account_masked);
                        const formOpen =
                          pending && (!hasBank || editingBankRefundId === rf.refund_id);

                        return (
                          <div key={rf.refund_id} className={styles.refundItem}>
                            <div className={styles.refundHead}>
                              <span className={`${styles.refundAmount} num`}>
                                {formatVnd(rf.amount_vnd)}
                              </span>
                              <span className={`${shared.statusTag} ${toneClass(rfMeta.tone)}`}>
                                {rfMeta.label}
                              </span>
                            </div>

                            {rf.reason && (
                              <p className={styles.sideLine}>Lý do hoàn tiền: {rf.reason}</p>
                            )}
                            {rf.created_at && (
                              <p className={`${styles.sideMuted} num`}>
                                Tạo lúc: {formatDateTimeVi(rf.created_at)}
                                {rf.settled_at
                                  ? ` · Đã chuyển khoản lúc: ${formatDateTimeVi(rf.settled_at)}`
                                  : ""}
                              </p>
                            )}
                            {rf.provider_refund_id && (
                              <p className={`${styles.sideMuted} num`}>
                                Mã tham chiếu chuyển khoản: <strong>{rf.provider_refund_id}</strong>
                              </p>
                            )}

                            {hasBank && (
                              <div className={styles.refundHead}>
                                <p className={`${styles.sideLine} num`}>
                                  Tài khoản nhận: <strong>{rf.bank_code}</strong> ·{" "}
                                  <strong>{rf.bank_account_masked}</strong>
                                  {rf.bank_account_name ? ` · ${rf.bank_account_name}` : ""}
                                </p>
                                {pending && editingBankRefundId !== rf.refund_id && (
                                  <Button
                                    type="button"
                                    variant="secondary"
                                    size="sm"
                                    onClick={() => handleOpenBankForm(rf)}
                                  >
                                    Đổi tài khoản nhận
                                  </Button>
                                )}
                              </div>
                            )}

                            {formOpen && (
                              <form
                                onSubmit={(e) => void handleSubmitBankAccount(e, rf)}
                                noValidate
                                style={{
                                  display: "flex",
                                  flexDirection: "column",
                                  gap: 12,
                                  paddingTop: 8,
                                }}
                              >
                                <p className={styles.actionPanelDesc}>
                                  {hasBank
                                    ? "Bạn có thể cập nhật tài khoản ngân hàng nhận tiền trước khi quản trị viên tất toán."
                                    : "Vui lòng nhập tài khoản ngân hàng nhận tiền để bộ phận kế toán NexusMart chuyển khoản hoàn tiền."}
                                </p>
                                <div className={shared.formGrid}>
                                  <div className={shared.field}>
                                    <label
                                      htmlFor={`bank-code-${rf.refund_id}`}
                                      className={shared.fieldLabel}
                                    >
                                      Ngân hàng nhận
                                    </label>
                                    <select
                                      id={`bank-code-${rf.refund_id}`}
                                      className={shared.select}
                                      value={
                                        editingBankRefundId === rf.refund_id
                                          ? bankCode
                                          : rf.bank_code || VN_BANK_OPTIONS[0].code
                                      }
                                      onChange={(e) => {
                                        if (editingBankRefundId !== rf.refund_id) {
                                          setEditingBankRefundId(rf.refund_id);
                                          setBankAccountNumber("");
                                          setBankAccountName(rf.bank_account_name || "");
                                          setBankError(null);
                                        }
                                        setBankCode(e.target.value);
                                      }}
                                      disabled={savingBank}
                                    >
                                      {VN_BANK_OPTIONS.map((b) => (
                                        <option key={b.code} value={b.code}>
                                          {b.label}
                                        </option>
                                      ))}
                                    </select>
                                  </div>
                                  <div className={shared.field}>
                                    <label
                                      htmlFor={`bank-num-${rf.refund_id}`}
                                      className={shared.fieldLabel}
                                    >
                                      Số tài khoản (6–19 chữ số)
                                    </label>
                                    <input
                                      id={`bank-num-${rf.refund_id}`}
                                      type="text"
                                      inputMode="numeric"
                                      className={`${shared.input} num`}
                                      value={
                                        editingBankRefundId === rf.refund_id
                                          ? bankAccountNumber
                                          : ""
                                      }
                                      onChange={(e) => {
                                        if (editingBankRefundId !== rf.refund_id) {
                                          setEditingBankRefundId(rf.refund_id);
                                          setBankCode(rf.bank_code || VN_BANK_OPTIONS[0].code);
                                          setBankAccountName(rf.bank_account_name || "");
                                          setBankError(null);
                                        }
                                        setBankAccountNumber(e.target.value.replace(/[^\d]/g, ""));
                                      }}
                                      placeholder="Ví dụ: 0123456789"
                                      disabled={savingBank}
                                      required
                                    />
                                  </div>
                                  <div className={`${shared.field} ${shared.formFull}`}>
                                    <label
                                      htmlFor={`bank-holder-${rf.refund_id}`}
                                      className={shared.fieldLabel}
                                    >
                                      Tên chủ tài khoản
                                    </label>
                                    <input
                                      id={`bank-holder-${rf.refund_id}`}
                                      type="text"
                                      className={shared.input}
                                      value={
                                        editingBankRefundId === rf.refund_id
                                          ? bankAccountName
                                          : rf.bank_account_name || ""
                                      }
                                      onChange={(e) => {
                                        if (editingBankRefundId !== rf.refund_id) {
                                          setEditingBankRefundId(rf.refund_id);
                                          setBankCode(rf.bank_code || VN_BANK_OPTIONS[0].code);
                                          setBankAccountNumber("");
                                          setBankError(null);
                                        }
                                        setBankAccountName(e.target.value);
                                      }}
                                      placeholder="Ví dụ: NGUYEN MINH QUAN"
                                      disabled={savingBank}
                                      required
                                    />
                                  </div>
                                </div>
                                {bankError &&
                                  (editingBankRefundId === rf.refund_id ||
                                    refunds.length === 1) && (
                                    <p className={shared.fieldError} role="alert">
                                      {bankError}
                                    </p>
                                  )}
                                <div className={shared.formActions}>
                                  <Button
                                    type="submit"
                                    variant="primary"
                                    size="md"
                                    disabled={savingBank}
                                  >
                                    {savingBank
                                      ? "Đang lưu tài khoản..."
                                      : "Lưu tài khoản nhận hoàn tiền"}
                                  </Button>
                                  {hasBank && (
                                    <Button
                                      type="button"
                                      variant="secondary"
                                      size="md"
                                      disabled={savingBank}
                                      onClick={() => {
                                        setEditingBankRefundId(null);
                                        setBankError(null);
                                      }}
                                    >
                                      Hủy
                                    </Button>
                                  )}
                                </div>
                              </form>
                            )}
                          </div>
                        );
                      })
                    )}
                  </div>
                </div>
              )}

              {/* Ordered products */}
              <div
                className={styles.sheetDivider}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm đã đặt</h2>
                  {shop?.slug && (
                    <Link href={`/gian-hang/${shop.slug}`} className={shared.breadcrumbLink}>
                      Xem gian hàng {shop.name}
                    </Link>
                  )}
                </div>

                <div className={styles.itemStack}>
                  {order.items.map((item) => {
                    const prodVisual = visuals.byProductId[item.product_id];
                    const thumbUrl = visuals.bySkuId[item.sku_id] ?? prodVisual?.image;
                    const productHref = prodVisual?.slug ? `/san-pham/${prodVisual.slug}` : null;
                    const effectivePrice = item.effective_unit_price_vnd || item.unit_price_vnd;
                    const hasFlashDiscount =
                      item.unit_price_vnd > 0 && effectivePrice < item.unit_price_vnd;
                    const reviewEligible = canReviewOrderStatus(order.status) && Boolean(item.id);
                    const existingReview = reviewEligible
                      ? orderReviews.find((r) => r.order_item_id === item.id)
                      : undefined;
                    const isReviewOpen = reviewEligible && reviewingItemId === item.id;

                    return (
                      <div key={item.id || item.sku_id} data-reveal="">
                        <div className={styles.itemRow}>
                          {productHref ? (
                            <Link href={productHref} className={styles.itemThumbLink}>
                              {thumbUrl ? (
                                <img
                                  src={thumbUrl}
                                  alt={item.product_name}
                                  width={896}
                                  height={1200}
                                  className={styles.itemThumb}
                                />
                              ) : (
                                <span className={styles.itemThumbPlaceholder} aria-hidden="true">
                                  <IconPackage size={24} />
                                </span>
                              )}
                            </Link>
                          ) : (
                            <div className={styles.itemThumbLink}>
                              {thumbUrl ? (
                                <img
                                  src={thumbUrl}
                                  alt={item.product_name}
                                  width={896}
                                  height={1200}
                                  className={styles.itemThumb}
                                />
                              ) : (
                                <span className={styles.itemThumbPlaceholder} aria-hidden="true">
                                  <IconPackage size={24} />
                                </span>
                              )}
                            </div>
                          )}

                          <div className={styles.itemInfo}>
                            {productHref ? (
                              <Link href={productHref} className={styles.itemName}>
                                {item.product_name}
                              </Link>
                            ) : (
                              <span className={styles.itemName}>{item.product_name}</span>
                            )}
                            {item.sku_name && (
                              <span className={styles.itemMeta}>{item.sku_name}</span>
                            )}
                            <span className={`${styles.itemMeta} num`}>
                              {formatVnd(effectivePrice)} × {item.quantity}
                              {hasFlashDiscount && (
                                <>
                                  {" "}
                                  <span className={styles.priceOriginal}>
                                    {formatVnd(item.unit_price_vnd)}
                                  </span>
                                </>
                              )}
                            </span>
                            {reviewEligible && (
                              <div className={shared.formActions}>
                                {existingReview ? (
                                  <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                    Đã đánh giá ({existingReview.rating}/5 sao)
                                  </span>
                                ) : (
                                  <Button
                                    type="button"
                                    variant="secondary"
                                    size="sm"
                                    onClick={() => handleOpenReviewForm(item.id)}
                                  >
                                    {isReviewOpen ? "Đóng form đánh giá" : "Viết đánh giá"}
                                  </Button>
                                )}
                              </div>
                            )}
                          </div>
                          <span className={`${styles.itemAmount} num`}>
                            {formatVnd(item.line_total_vnd)}
                          </span>
                        </div>

                        {existingReview && existingReview.comment && (
                          <div className={styles.returnSubPanel} style={{ marginBottom: 12 }}>
                            <p className={styles.sideMuted}>
                              Nhận xét của bạn: {existingReview.comment}
                            </p>
                          </div>
                        )}

                        {isReviewOpen && (
                          <form
                            className={styles.returnSubPanel}
                            style={{ marginBottom: 16 }}
                            noValidate
                            onSubmit={(e) => void handleSubmitReview(e, item.id)}
                          >
                            <div className={shared.field}>
                              <span className={shared.fieldLabel}>
                                Đánh giá cho {item.product_name}
                                {item.sku_name ? ` (${item.sku_name})` : ""}
                              </span>
                              <div
                                className={shared.formActions}
                                role="group"
                                aria-label="Chọn số sao đánh giá"
                              >
                                {[1, 2, 3, 4, 5].map((star) => (
                                  <button
                                    key={star}
                                    type="button"
                                    className={`${shared.modeBtn} ${reviewRating === star ? shared.modeBtnActive : ""}`}
                                    aria-pressed={reviewRating === star}
                                    onClick={() => setReviewRating(star)}
                                  >
                                    <IconStar size={14} filled={star <= reviewRating} /> {star} sao
                                  </button>
                                ))}
                              </div>
                            </div>

                            <div className={shared.field}>
                              <label
                                htmlFor={`review-comment-${item.id}`}
                                className={shared.fieldLabel}
                              >
                                Nhận xét (không bắt buộc, tối đa {MAX_REVIEW_COMMENT_LEN} ký tự)
                              </label>
                              <textarea
                                id={`review-comment-${item.id}`}
                                className={shared.textarea}
                                rows={3}
                                maxLength={MAX_REVIEW_COMMENT_LEN}
                                value={reviewComment}
                                onChange={(e) => {
                                  setReviewComment(e.target.value);
                                  if (reviewError) setReviewError(null);
                                }}
                                placeholder="Chia sẻ cảm nhận thực tế về chất liệu, đường may và đóng gói..."
                                disabled={submittingReview}
                              />
                            </div>

                            <div className={shared.field}>
                              <span className={shared.fieldLabel}>
                                Liên kết ảnh đính kèm (tối đa {MAX_REVIEW_IMAGES} liên kết https://
                                hoặc /products/...)
                              </span>
                              <div className={styles.evidenceList}>
                                {reviewImageUrls.map((urlVal, idx) => (
                                  <div
                                    key={`${item.id}-rimg-${idx}`}
                                    className={styles.evidenceRow}
                                  >
                                    <input
                                      id={`review-img-${item.id}-${idx}`}
                                      type="url"
                                      className={shared.input}
                                      value={urlVal}
                                      onChange={(e) => {
                                        const next = [...reviewImageUrls];
                                        next[idx] = e.target.value;
                                        setReviewImageUrls(next);
                                        if (reviewError) setReviewError(null);
                                      }}
                                      placeholder="https://... hoặc /products/..."
                                      aria-label={`Liên kết ảnh đánh giá ${idx + 1}`}
                                      disabled={submittingReview}
                                    />
                                    {reviewImageUrls.length > 1 && (
                                      <Button
                                        type="button"
                                        variant="secondary"
                                        size="sm"
                                        disabled={submittingReview}
                                        onClick={() =>
                                          setReviewImageUrls((prev) =>
                                            prev.filter((_, i) => i !== idx)
                                          )
                                        }
                                      >
                                        Xóa
                                      </Button>
                                    )}
                                  </div>
                                ))}
                              </div>
                              {reviewImageUrls.length < MAX_REVIEW_IMAGES && (
                                <div className={shared.formActions}>
                                  <Button
                                    type="button"
                                    variant="secondary"
                                    size="sm"
                                    disabled={submittingReview}
                                    onClick={() =>
                                      setReviewImageUrls((prev) =>
                                        prev.length < MAX_REVIEW_IMAGES ? [...prev, ""] : prev
                                      )
                                    }
                                  >
                                    Thêm liên kết ảnh ({reviewImageUrls.length}/{MAX_REVIEW_IMAGES})
                                  </Button>
                                </div>
                              )}
                            </div>

                            {reviewError && (
                              <p className={shared.fieldError} role="alert">
                                {reviewError}
                              </p>
                            )}

                            <div className={shared.formActions}>
                              <Button
                                type="submit"
                                variant="primary"
                                size="md"
                                disabled={submittingReview}
                              >
                                {submittingReview ? "Đang gửi đánh giá..." : "Gửi đánh giá"}
                              </Button>
                              <Button
                                type="button"
                                variant="secondary"
                                size="md"
                                disabled={submittingReview}
                                onClick={() => {
                                  setReviewingItemId(null);
                                  setReviewError(null);
                                }}
                              >
                                Hủy
                              </Button>
                            </div>
                          </form>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            {/* Confirm received — ONLY allowed in DELIVERED per docs/contracts.md §6 */}
            {allowConfirm && (
              <div className={styles.confirmBox} data-reveal="">
                <h2 className={shared.sectionTitle}>Xác nhận đã nhận hàng</h2>
                <p className={styles.actionPanelDesc}>
                  Nếu bạn đã nhận đủ sản phẩm và hài lòng với chất lượng, hãy xác nhận để hoàn tất
                  đơn hàng và nhận điểm thưởng Xu vào tài khoản.
                </p>
                {confirmError && (
                  <p className={shared.fieldError} role="alert">
                    {confirmError}
                  </p>
                )}
                <div className={shared.formActions}>
                  <Button
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

            {/* Cancel order — ONLY allowed in PENDING, CONFIRMED, PREPARING per docs/contracts.md §6 */}
            {allowCancel && (
              <form
                className={styles.unboxedSection}
                onSubmit={handleCancelSubmit}
                noValidate
                data-reveal=""
              >
                <h2 className={shared.sectionTitle}>Hủy đơn hàng</h2>
                {cancelGroupInfo.isMultiShopGroup ? (
                  <div className={`${shared.feedbackBox} ${shared.feedbackError}`}>
                    <strong>{formatUnpaidGroupCancelWarning(cancelGroupInfo.shopCount)}.</strong>{" "}
                    Các gian hàng trong lần đặt này dùng chung một mã chuyển khoản VietQR nên khi
                    hủy sẽ áp dụng cho toàn bộ đơn đang chờ thanh toán trong nhóm.
                  </div>
                ) : awaitingCarrierPickup ? (
                  <p className={styles.actionPanelDesc}>
                    Đơn hàng đã tạo vận đơn và đang chờ đơn vị vận chuyển lấy hàng. Bạn vẫn có thể
                    hủy trước khi hãng nhận kiện; nếu hãng đã lấy hàng, hệ thống sẽ thông báo không
                    thể hủy.
                  </p>
                ) : (
                  <p className={styles.actionPanelDesc}>
                    Bạn có thể hủy đơn trước khi bàn giao cho đơn vị vận chuyển. Nếu đơn đã thanh
                    toán trước, số tiền{" "}
                    <strong className="num">{formatVnd(order.total_payable_vnd)}</strong> sẽ được xử
                    lý hoàn lại theo quy định.
                  </p>
                )}

                <div className={shared.formGrid}>
                  <div className={`${shared.field} ${shared.formFull}`}>
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
                  <div className={`${shared.field} ${shared.formFull}`}>
                    <label htmlFor="cancel-note" className={shared.fieldLabel}>
                      Ghi chú thêm (không bắt buộc)
                    </label>
                    <input
                      id="cancel-note"
                      type="text"
                      className={shared.input}
                      value={cancelNote}
                      onChange={(e) => setCancelNote(e.target.value)}
                      placeholder="Nhập chi tiết nếu muốn đổi sản phẩm hoặc địa chỉ..."
                      disabled={cancelling}
                    />
                  </div>
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

            {groupOrders.length > 1 && (
              <div className={styles.unboxedSection}>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>
                    Các đơn trong cùng lần đặt hàng ({groupOrders.length} gian hàng)
                  </h2>
                  <Link href="/don-hang" className={shared.breadcrumbLink}>
                    Xem danh sách đơn hàng
                  </Link>
                </div>
                <ul className={shared.confirmList}>
                  {groupOrders.map((sib) => {
                    const sibMeta = getOrderStatusInfo(sib.status);
                    const sibShopName =
                      groupShopsMap[sib.shop_id]?.name ??
                      (sib.shop_id === order.shop_id ? shopName : "Gian hàng NexusMart");
                    return (
                      <li key={sib.id} className={shared.confirmListItem}>
                        <span>
                          <Link href={`/don-hang/${sib.id}`} className={shared.breadcrumbLink}>
                            <strong className="num">{sib.order_code}</strong>
                          </Link>{" "}
                          · {sibShopName}
                        </span>
                        <span className={`${shared.statusTag} ${toneClass(sibMeta.tone)}`}>
                          {sibMeta.label}
                        </span>
                      </li>
                    );
                  })}
                </ul>
              </div>
            )}

            {/* Request return — ONLY allowed in DELIVERED per docs/contracts.md §6 */}
            {allowReturn && (
              <form
                className={styles.unboxedSection}
                onSubmit={(e) => void handleReturnSubmit(e)}
                noValidate
                data-reveal=""
              >
                <h2 className={shared.sectionTitle}>Yêu cầu trả hàng / hoàn tiền</h2>
                <p className={styles.actionPanelDesc}>
                  Nếu sản phẩm bị lỗi, giao nhầm mẫu hoặc không đúng kích cỡ, bạn có thể gửi yêu cầu
                  trả hàng trong vòng 7 ngày kể từ khi nhận hàng.
                </p>

                <div className={shared.field}>
                  <label htmlFor="return-reason" className={shared.fieldLabel}>
                    Lý do trả hàng
                  </label>
                  <select
                    id="return-reason"
                    className={shared.select}
                    value={returnReason}
                    onChange={(e) => setReturnReason(e.target.value)}
                    disabled={returning}
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
                    Mô tả chi tiết tình trạng sản phẩm
                  </label>
                  <textarea
                    id="return-note"
                    className={shared.textarea}
                    value={returnDetail}
                    onChange={(e) => setReturnDetail(e.target.value)}
                    placeholder="Ví dụ: Áo còn nguyên tem mác, cần trả hàng do giao nhầm size."
                    disabled={returning}
                  />
                </div>

                <EvidenceUrlInputs
                  idPrefix="return-create"
                  urls={returnEvidenceUrls}
                  onChange={setReturnEvidenceUrls}
                  disabled={returning}
                />

                {returnError && (
                  <p className={shared.fieldError} role="alert">
                    {returnError}
                  </p>
                )}

                <div className={shared.formActions}>
                  <Button type="submit" variant="ink" size="md" disabled={returning}>
                    {returning ? "Đang gửi yêu cầu..." : "Gửi yêu cầu trả hàng"}
                  </Button>
                </div>
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
                {order.shipping_address ? (
                  <>
                    <p className={styles.sideLine}>
                      <strong>{order.shipping_address.recipient_name}</strong> ·{" "}
                      <span className="num">{order.shipping_address.recipient_phone}</span>
                    </p>
                    <p className={styles.sideLine}>
                      {[
                        order.shipping_address.street_address,
                        order.shipping_address.ward_name,
                        order.shipping_address.province_name,
                      ]
                        .filter(Boolean)
                        .join(", ")}
                    </p>
                  </>
                ) : (
                  <p className={styles.sideMuted}>Đang cập nhật địa chỉ nhận hàng.</p>
                )}

                {order.carrier_code && (
                  <p className={styles.sideMuted}>
                    Đơn vị giao: <strong>{formatCarrierLabel(order.carrier_code)}</strong>
                  </p>
                )}
                {order.tracking_code && (
                  <p className={`${styles.sideLine} num`}>
                    Mã vận đơn: <strong>{order.tracking_code}</strong>
                  </p>
                )}
              </div>

              {/* Payment summary — strictly integer VND from backend */}
              <div className={styles.sideBlock}>
                <h2 className={styles.sideTitle}>Chi tiết thanh toán</h2>
                <p className={styles.sideLine}>{getPaymentMethodLabel(order.payment_method)}</p>
                <p className={styles.sideMuted}>
                  Trạng thái: <strong>{payMeta.label}</strong>
                </p>

                <dl className={styles.ledgerBox}>
                  {buildBuyerOrderBreakdownLines(order).map((line) => (
                    <div key={line.key} className={styles.ledgerRow}>
                      <dt className={styles.ledgerLabel}>{line.label}</dt>
                      <dd className={`${line.isDiscount ? styles.ledgerDiscount : ""} num`}>
                        {line.isDiscount
                          ? line.amountVnd < 0
                            ? formatSignedVnd(line.amountVnd)
                            : "0₫"
                          : formatVnd(line.amountVnd)}
                      </dd>
                    </div>
                  ))}
                </dl>
              </div>

              <div className={styles.totalRow}>
                <span>Tổng thanh toán</span>
                <span className={`${styles.totalVal} num`}>
                  {formatVnd(order.total_payable_vnd)}
                </span>
              </div>
            </div>
          </aside>
        </div>
      </section>

      <ProductAddonStrip
        title="Gợi ý thêm đồ cơ bản trên NexusMart"
        subtitle="Đồ cơ bản từ các xưởng dệt Việt Nam, tiện mua thêm cho đơn kế tiếp"
        products={suggestedProducts}
      />

      {confirmGroupCancelOpen && cancelGroupInfo.isMultiShopGroup && (
        <div
          className={shared.confirmBackdrop}
          onClick={(ev) => {
            if (ev.target === ev.currentTarget && !cancelling) {
              setConfirmGroupCancelOpen(false);
            }
          }}
        >
          <div
            className={shared.confirmDialog}
            role="dialog"
            aria-modal="true"
            aria-labelledby="detail-cancel-dialog-title"
          >
            <h2 id="detail-cancel-dialog-title" className={shared.confirmTitle}>
              Xác nhận hủy lần đặt hàng
            </h2>
            <p className={shared.confirmBody}>
              <strong>{formatUnpaidGroupCancelWarning(cancelGroupInfo.shopCount)}.</strong>
            </p>
            <p className={shared.confirmBody}>
              Các đơn hàng đang chờ thanh toán chung mã VietQR bên dưới sẽ được hủy cùng lúc:
            </p>
            <ul className={shared.confirmList}>
              {cancelGroupInfo.groupOrders.map((sib) => (
                <li key={sib.id} className={shared.confirmListItem}>
                  <span>
                    <strong className="num">{sib.order_code}</strong> ·{" "}
                    {groupShopsMap[sib.shop_id]?.name ??
                      (sib.shop_id === order.shop_id ? shopName : "Gian hàng NexusMart")}
                  </span>
                  <span className="num">{formatVnd(sib.total_payable_vnd)}</span>
                </li>
              ))}
            </ul>
            {cancelError && (
              <p className={shared.fieldError} role="alert">
                {cancelError}
              </p>
            )}
            <div className={shared.formActions}>
              <Button
                type="button"
                variant="secondary"
                size="md"
                disabled={cancelling}
                onClick={() => setConfirmGroupCancelOpen(false)}
              >
                Quay lại
              </Button>
              <Button
                type="button"
                variant="danger"
                size="md"
                disabled={cancelling}
                onClick={() => void executeCancelOrder()}
              >
                {cancelling ? "Đang hủy..." : `Đồng ý hủy cả ${cancelGroupInfo.shopCount} đơn`}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
