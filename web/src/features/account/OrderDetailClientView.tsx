"use client";

import React, { Suspense, useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ORDER_STATUS_META,
  PAYMENT_METHOD_LABELS,
  PAYMENT_STATUS_META,
  type OrderStatus,
} from "@/data/orders";
import type { Product } from "@/data/products";
import type {
  GatewayAddress,
  GatewayProductReview,
  GatewayRefund,
  GatewayReturnRequest,
  GatewayShipment,
  GatewayShipmentEvent,
} from "@/lib/gateway-types";
import { ApiError, api } from "@/lib/api";
import { formatCarrierLabel, formatDateTimeVi, formatSignedVnd, formatVnd } from "@/lib/format";
import {
  type UpdateOrderAddressResponse,
  applyTopLevelAddressFeeQuote,
  canBuyerUpdateOrderAddress,
  classifyAddressUpdatePreview,
  isAddressPreviewStale,
  isAddressUpdateInProgressError,
  translateAddressUpdateError,
} from "@/lib/a8-types";
import { readCheckoutWarnings } from "@/features/checkout/helpers";
import { useStore } from "@/context/StoreContext";
import { Button, IconCheck, IconChevronDown, IconClose, IconTrash, StateView } from "@/components";
import { useModalFocusTrap } from "@/components/modalFocus";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { formatAddressLine } from "@/features/commerce/cartEnrich";
import { renderOrderDateTime, statusToneClass } from "@/features/commerce/orderDisplay";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import {
  MAX_REVIEW_COMMENT_LEN,
  MAX_REVIEW_IMAGES,
  canReviewOrderStatus,
  isOrderItemReviewed,
  parseSafeReviewImageUrl,
  validateReviewImageUrls,
} from "@/features/product/reviewHelpers";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./OrderDetailClientView.module.css";
import {
  reconcileServerSnapshot,
  resolveClientReturnRequest,
  resolveUpdatedRefunds,
  resolveUpdatedReviews,
} from "./orderReturnSync";
import type { OrderView } from "./orderView";
import {
  MAX_EVIDENCE_URLS,
  VN_BANK_OPTIONS,
  buildShippingLabelHref,
  canRequestRedelivery,
  canSubmitBuyerRefundBankAccount,
  getRefundStatusInfo,
  getReturnFeeBearerLabel,
  getReturnStatusInfo,
  getShipmentStatusInfo,
  isWalletRefund,
  localizeRefundReason,
  needsVietQrPayment,
  normalizeEnumSuffix,
  parseSafeEvidenceUrl,
  validateBankAccountNumber,
  validateEvidenceUrls,
} from "./orderMeta";

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
  "Sản phẩm thiếu phụ kiện hoặc hư hại khi nhận",
  "Chất liệu chưa phù hợp nhu cầu",
];

function getVietnamTodayIso(): string {
  const vnNow = new Date(Date.now() + 7 * 3600 * 1000);
  return vnNow.toISOString().slice(0, 10);
}

export interface OrderDetailClientViewProps {
  brandName: string;
  orderId: string;
  initialOrder: OrderView | null;
  suggestedProducts: Product[];
  initialShipment?: GatewayShipment | null;
  initialShipmentEvents?: GatewayShipmentEvent[];
  initialReturnShipment?: GatewayShipment | null;
  initialReturnRequest?: GatewayReturnRequest | null;
  initialRefunds?: GatewayRefund[];
  initialReviews?: GatewayProductReview[];
  initialAddresses?: GatewayAddress[];
  initialError?: string | null;
}

function OrderDetailInner({
  brandName,
  orderId,
  initialOrder,
  suggestedProducts,
  initialShipment = null,
  initialShipmentEvents = [],
  initialReturnShipment = null,
  initialReturnRequest = null,
  initialRefunds = [],
  initialReviews = [],
  initialAddresses = [],
  initialError = null,
}: OrderDetailClientViewProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const { addToCart, showToast } = useStore();

  const [order, setOrder] = useState<OrderView | null>(initialOrder);
  const [shipment, setShipment] = useState<GatewayShipment | null>(initialShipment);
  const [shipmentEvents, setShipmentEvents] =
    useState<GatewayShipmentEvent[]>(initialShipmentEvents);
  const [returnShipment, setReturnShipment] = useState<GatewayShipment | null>(
    initialReturnShipment
  );
  const [returnRequest, setReturnRequest] = useState<GatewayReturnRequest | null>(
    initialReturnRequest
  );
  const [refunds, setRefunds] = useState<GatewayRefund[]>(initialRefunds);
  const [reviews, setReviews] = useState<GatewayProductReview[]>(initialReviews);

  const prevOrderIdRef = useRef(orderId);
  const prevInitialOrderRef = useRef(initialOrder);
  const prevInitialShipmentRef = useRef(initialShipment);
  const prevInitialShipmentEventsRef = useRef(initialShipmentEvents);
  const prevInitialReturnShipmentRef = useRef(initialReturnShipment);
  const prevInitialReturnRequestRef = useRef(initialReturnRequest);
  const prevInitialRefundsRef = useRef(initialRefunds);
  const prevInitialReviewsRef = useRef(initialReviews);

  useEffect(() => {
    const orderChanged = prevOrderIdRef.current !== orderId;
    prevOrderIdRef.current = orderId;

    if (orderChanged || prevInitialOrderRef.current !== initialOrder) {
      prevInitialOrderRef.current = initialOrder;
      setOrder((prev) =>
        reconcileServerSnapshot({ orderChanged, current: prev, incoming: initialOrder })
      );
    }
    if (orderChanged || prevInitialShipmentRef.current !== initialShipment) {
      prevInitialShipmentRef.current = initialShipment;
      setShipment((prev) =>
        reconcileServerSnapshot({ orderChanged, current: prev, incoming: initialShipment })
      );
    }
    if (orderChanged || prevInitialShipmentEventsRef.current !== initialShipmentEvents) {
      prevInitialShipmentEventsRef.current = initialShipmentEvents;
      setShipmentEvents((prev) =>
        orderChanged || initialShipmentEvents.length > 0 ? initialShipmentEvents : prev
      );
    }
    if (orderChanged || prevInitialReturnShipmentRef.current !== initialReturnShipment) {
      prevInitialReturnShipmentRef.current = initialReturnShipment;
      setReturnShipment((prev) =>
        reconcileServerSnapshot({ orderChanged, current: prev, incoming: initialReturnShipment })
      );
    }
    if (orderChanged || prevInitialReturnRequestRef.current !== initialReturnRequest) {
      prevInitialReturnRequestRef.current = initialReturnRequest;
      setReturnRequest((prev) =>
        reconcileServerSnapshot({ orderChanged, current: prev, incoming: initialReturnRequest })
      );
    }
    if (orderChanged || prevInitialRefundsRef.current !== initialRefunds) {
      prevInitialRefundsRef.current = initialRefunds;
      setRefunds((prev) => (orderChanged || initialRefunds.length > 0 ? initialRefunds : prev));
    }
    if (orderChanged || prevInitialReviewsRef.current !== initialReviews) {
      prevInitialReviewsRef.current = initialReviews;
      setReviews((prev) => (orderChanged || initialReviews.length > 0 ? initialReviews : prev));
    }
  }, [
    orderId,
    initialOrder,
    initialShipment,
    initialShipmentEvents,
    initialReturnShipment,
    initialReturnRequest,
    initialRefunds,
    initialReviews,
  ]);

  // Cancel & confirm
  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const [reordering, setReordering] = useState(false);

  // Return request creation sheet (singular POST /api/orders/[id]/return)
  const [returnSheetOpen, setReturnSheetOpen] = useState(false);
  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [returnEvidenceUrls, setReturnEvidenceUrls] = useState<string[]>([]);
  const [evidenceInput, setEvidenceInput] = useState("");
  const [evidenceInputError, setEvidenceInputError] = useState<string | null>(null);
  const [submittingReturn, setSubmittingReturn] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);
  const returnSheetRef = useRef<HTMLDivElement | null>(null);

  // Return escalate & additional evidence
  const [escalateReason, setEscalateReason] = useState("");
  const [escalating, setEscalating] = useState(false);
  const [escalateFeedback, setEscalateFeedback] = useState<{ ok: boolean; message: string } | null>(
    null
  );
  const [extraEvidenceNote, setExtraEvidenceNote] = useState("");
  const [extraEvidenceUrls, setExtraEvidenceUrls] = useState<string[]>([]);
  const [extraEvidenceInput, setExtraEvidenceInput] = useState("");
  const [submittingExtraEvidence, setSubmittingExtraEvidence] = useState(false);
  const [extraEvidenceFeedback, setExtraEvidenceFeedback] = useState<{
    ok: boolean;
    message: string;
  } | null>(null);

  // Redelivery
  const [redeliveryDate, setRedeliveryDate] = useState<string>(() => getVietnamTodayIso());
  const [redeliveryNote, setRedeliveryNote] = useState("");
  const [submittingRedelivery, setSubmittingRedelivery] = useState(false);
  const [redeliveryFeedback, setRedeliveryFeedback] = useState<{
    ok: boolean;
    message: string;
  } | null>(null);

  // Refunds bank account submission (keyed by refund_id)
  const [bankFormByRefund, setBankFormByRefund] = useState<
    Record<string, { bankCode: string; accountNumber: string; accountName: string }>
  >({});
  const [submittingBankRefundId, setSubmittingBankRefundId] = useState<string | null>(null);
  const [bankFeedbackByRefund, setBankFeedbackByRefund] = useState<
    Record<string, { ok: boolean; message: string }>
  >({});

  // Product reviews (keyed by order_item_id)
  const [reviewDraftByItem, setReviewDraftByItem] = useState<
    Record<string, { rating: number; comment: string; imageInput: string; imageUrls: string[] }>
  >({});
  const [submittingReviewItemId, setSubmittingReviewItemId] = useState<string | null>(null);
  const [reviewErrorByItem, setReviewErrorByItem] = useState<Record<string, string>>({});

  // Pre-pickup shipping address update
  const [selectedNewAddrId, setSelectedNewAddrId] = useState<string>(initialAddresses[0]?.id ?? "");
  const [addrPreview, setAddrPreview] = useState<UpdateOrderAddressResponse | null>(null);
  const [addrPreviewForId, setAddrPreviewForId] = useState<string>("");
  const [acceptFeeIncrease, setAcceptFeeIncrease] = useState<boolean>(false);
  const [loadingAddrPreview, setLoadingAddrPreview] = useState<boolean>(false);
  const [submittingAddrUpdate, setSubmittingAddrUpdate] = useState<boolean>(false);
  const [addressInProgressLock, setAddressInProgressLock] = useState<boolean>(false);
  const [checkoutWarnings, setCheckoutWarnings] = useState<string[]>([]);
  const [addrUpdateFeedback, setAddrUpdateFeedback] = useState<{
    ok: boolean;
    message: string;
  } | null>(null);

  useEffect(() => {
    if (order?.id) {
      setCheckoutWarnings(readCheckoutWarnings({ orderId: order.id }));
    }
  }, [order?.id]);

  const closeReturnSheet = useCallback(() => setReturnSheetOpen(false), []);

  useModalFocusTrap({
    open: returnSheetOpen && Boolean(order?.canRequestReturn),
    containerRef: returnSheetRef,
    onEscape: closeReturnSheet,
  });

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

  const handleAddEvidenceUrl = () => {
    setEvidenceInputError(null);
    const trimmed = evidenceInput.trim();
    if (!trimmed) {
      setEvidenceInputError("Nhập đường dẫn https:// của ảnh hoặc video trước khi thêm.");
      return;
    }
    const candidateList = [...returnEvidenceUrls, trimmed];
    const check = validateEvidenceUrls(candidateList);
    if (!check.ok) {
      setEvidenceInputError(check.error ?? "Liên kết bằng chứng không hợp lệ.");
      return;
    }
    if (returnEvidenceUrls.includes(trimmed)) {
      setEvidenceInputError("Liên kết này đã được thêm.");
      return;
    }
    setReturnEvidenceUrls(check.urls);
    setEvidenceInput("");
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || submittingReturn) return;
    const cleanNote = returnDetail.trim();
    if (cleanNote.length < 5) {
      setReturnError("Vui lòng mô tả tình trạng sản phẩm (tối thiểu 5 ký tự).");
      return;
    }
    const evCheck = validateEvidenceUrls(returnEvidenceUrls);
    if (!evCheck.ok) {
      setReturnError(evCheck.error ?? "Liên kết bằng chứng không hợp lệ.");
      return;
    }

    const combinedReason = `${returnReason}. ${cleanNote}`.slice(0, 500);
    setReturnError(null);
    setSubmittingReturn(true);
    try {
      const res = await api<{
        return_request?: GatewayReturnRequest | null;
        return_request_id?: string;
        order?: OrderView | null;
      }>(`/api/orders/${encodeURIComponent(order.id)}/return`, {
        method: "POST",
        body: { reason: combinedReason, evidence_urls: evCheck.urls },
      });
      const resolvedReturnRequest = await resolveClientReturnRequest({
        inlineReturnRequest: res.return_request,
        fetchFallback: () =>
          api<{ return_request?: GatewayReturnRequest | null }>(
            `/api/orders/${encodeURIComponent(order.id)}/return`
          ),
      });
      if (resolvedReturnRequest) {
        setReturnRequest(resolvedReturnRequest);
      }
      if (res.order) {
        setOrder(res.order);
      } else {
        setOrder((prev) =>
          prev
            ? {
                ...prev,
                status: "yeu_cau_tra_hang",
                backendStatus: "RETURN_REQUESTED",
                canRequestReturn: false,
                canConfirmReceived: false,
              }
            : prev
        );
      }
      setReturnDetail("");
      setReturnEvidenceUrls([]);
      setEvidenceInput("");
      setReturnSheetOpen(false);
      showToast(`Đã gửi yêu cầu đổi trả tới ${brandName}.`);
      router.refresh();
    } catch (err) {
      setReturnError(err instanceof ApiError ? err.message : "Chưa gửi được yêu cầu đổi trả.");
    } finally {
      setSubmittingReturn(false);
    }
  };

  const handleEscalateReturn = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || !returnRequest || escalating) return;
    const clean = escalateReason.trim();
    if (clean.length < 10) {
      setEscalateFeedback({
        ok: false,
        message: "Nhập lý do khiếu nại chi tiết (tối thiểu 10 ký tự).",
      });
      return;
    }
    setEscalateFeedback(null);
    setEscalating(true);
    try {
      const res = await api<{ return_request?: GatewayReturnRequest | null }>(
        `/api/returns/${encodeURIComponent(returnRequest.id)}/escalate`,
        { method: "POST", body: { reason: clean } }
      );
      const resolvedReturnRequest = await resolveClientReturnRequest({
        inlineReturnRequest: res.return_request,
        fetchFallback: () =>
          api<{ return_request?: GatewayReturnRequest | null }>(
            `/api/orders/${encodeURIComponent(order.id)}/return`
          ),
      });
      if (resolvedReturnRequest) {
        setReturnRequest(resolvedReturnRequest);
      }
      setEscalateReason("");
      setEscalateFeedback({ ok: true, message: "Đã gửi khiếu nại lên NexusMart." });
      showToast("Đã gửi khiếu nại trả hàng.");
      router.refresh();
    } catch (err) {
      setEscalateFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa thể gửi khiếu nại lúc này.",
      });
    } finally {
      setEscalating(false);
    }
  };

  const handleSubmitExtraEvidence = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || !returnRequest || submittingExtraEvidence) return;
    const evCheck = validateEvidenceUrls(extraEvidenceUrls);
    if (!evCheck.ok) {
      setExtraEvidenceFeedback({ ok: false, message: evCheck.error ?? "Liên kết không hợp lệ." });
      return;
    }
    if (evCheck.urls.length === 0 && extraEvidenceNote.trim().length < 5) {
      setExtraEvidenceFeedback({
        ok: false,
        message: "Cần nhập ghi chú (tối thiểu 5 ký tự) hoặc thêm ít nhất 1 liên kết bằng chứng.",
      });
      return;
    }
    setExtraEvidenceFeedback(null);
    setSubmittingExtraEvidence(true);
    try {
      const res = await api<{ return_request?: GatewayReturnRequest | null }>(
        `/api/returns/${encodeURIComponent(returnRequest.id)}/evidence`,
        {
          method: "POST",
          body: {
            note: extraEvidenceNote.trim(),
            evidence_urls: evCheck.urls,
          },
        }
      );
      const resolvedReturnRequest = await resolveClientReturnRequest({
        inlineReturnRequest: res.return_request,
        fetchFallback: () =>
          api<{ return_request?: GatewayReturnRequest | null }>(
            `/api/orders/${encodeURIComponent(order.id)}/return`
          ),
      });
      if (resolvedReturnRequest) {
        setReturnRequest(resolvedReturnRequest);
      }
      setExtraEvidenceNote("");
      setExtraEvidenceUrls([]);
      setExtraEvidenceInput("");
      setExtraEvidenceFeedback({ ok: true, message: "Đã bổ sung bằng chứng thành công." });
      showToast("Đã gửi bằng chứng bổ sung.");
      router.refresh();
    } catch (err) {
      setExtraEvidenceFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa thể gửi bằng chứng bổ sung.",
      });
    } finally {
      setSubmittingExtraEvidence(false);
    }
  };

  const handleRequestRedelivery = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || submittingRedelivery) return;
    setRedeliveryFeedback(null);
    setSubmittingRedelivery(true);
    try {
      const res = await api<{ shipment?: GatewayShipment }>(
        `/api/orders/${encodeURIComponent(order.id)}/redelivery`,
        {
          method: "POST",
          body: {
            preferred_date: redeliveryDate || undefined,
            note: redeliveryNote.trim() || undefined,
          },
        }
      );
      if (res.shipment) setShipment(res.shipment);
      setRedeliveryNote("");
      setRedeliveryFeedback({
        ok: true,
        message: "Đã gửi yêu cầu giao lại cho đơn vị vận chuyển.",
      });
      showToast("Đã gửi yêu cầu giao lại.");
      router.refresh();
    } catch (err) {
      setRedeliveryFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa thể gửi yêu cầu giao lại lúc này.",
      });
    } finally {
      setSubmittingRedelivery(false);
    }
  };

  const handleSubmitRefundBankAccount = async (e: React.FormEvent, refundId: string) => {
    e.preventDefault();
    if (!order || submittingBankRefundId) return;
    const draft = bankFormByRefund[refundId] ?? {
      bankCode: VN_BANK_OPTIONS[0].code,
      accountNumber: "",
      accountName: "",
    };
    const cleanNum = draft.accountNumber.replace(/\s+/g, "").trim();
    const cleanName = draft.accountName.trim();
    if (!validateBankAccountNumber(cleanNum)) {
      setBankFeedbackByRefund((prev) => ({
        ...prev,
        [refundId]: { ok: false, message: "Số tài khoản ngân hàng cần gồm 6–19 chữ số." },
      }));
      return;
    }
    if (cleanName.length < 2) {
      setBankFeedbackByRefund((prev) => ({
        ...prev,
        [refundId]: { ok: false, message: "Nhập đầy đủ tên chủ tài khoản nhận hoàn tiền." },
      }));
      return;
    }

    setSubmittingBankRefundId(refundId);
    setBankFeedbackByRefund((prev) => {
      const next = { ...prev };
      delete next[refundId];
      return next;
    });
    try {
      const res = await api<{ refund?: GatewayRefund | null }>(
        `/api/refunds/${encodeURIComponent(refundId)}/bank-account`,
        {
          method: "POST",
          body: {
            order_id: order.id,
            bank_code: draft.bankCode,
            bank_account_number: cleanNum,
            bank_account_name: cleanName,
          },
        }
      );
      const nextRefunds = await resolveUpdatedRefunds({
        refundId,
        updatedRefund: res.refund,
        currentRefunds: refunds,
        fetchFallback: () =>
          api<{ items?: GatewayRefund[]; refunds?: GatewayRefund[] }>(
            `/api/orders/${encodeURIComponent(order.id)}/refunds`
          ),
      });
      setRefunds(nextRefunds);
      setBankFeedbackByRefund((prev) => ({
        ...prev,
        [refundId]: { ok: true, message: "Đã lưu tài khoản ngân hàng nhận hoàn tiền." },
      }));
      showToast("Đã lưu thông tin tài khoản hoàn tiền.");
      router.refresh();
    } catch (err) {
      setBankFeedbackByRefund((prev) => ({
        ...prev,
        [refundId]: {
          ok: false,
          message: err instanceof ApiError ? err.message : "Chưa thể lưu tài khoản ngân hàng.",
        },
      }));
    } finally {
      setSubmittingBankRefundId(null);
    }
  };

  const handleSubmitReview = async (e: React.FormEvent, orderItemId: string) => {
    e.preventDefault();
    if (!order || submittingReviewItemId) return;
    const draft = reviewDraftByItem[orderItemId] ?? {
      rating: 5,
      comment: "",
      imageInput: "",
      imageUrls: [],
    };
    if (draft.rating < 1 || draft.rating > 5) {
      setReviewErrorByItem((prev) => ({ ...prev, [orderItemId]: "Chọn số sao từ 1 đến 5." }));
      return;
    }
    if (draft.comment.trim().length > MAX_REVIEW_COMMENT_LEN) {
      setReviewErrorByItem((prev) => ({
        ...prev,
        [orderItemId]: `Nội dung đánh giá tối đa ${MAX_REVIEW_COMMENT_LEN} ký tự.`,
      }));
      return;
    }
    const imgCheck = validateReviewImageUrls(draft.imageUrls);
    if (imgCheck.error) {
      setReviewErrorByItem((prev) => ({ ...prev, [orderItemId]: imgCheck.error! }));
      return;
    }

    setSubmittingReviewItemId(orderItemId);
    setReviewErrorByItem((prev) => {
      const next = { ...prev };
      delete next[orderItemId];
      return next;
    });
    try {
      const res = await api<{ review?: GatewayProductReview | null }>(
        `/api/orders/${encodeURIComponent(order.id)}/reviews`,
        {
          method: "POST",
          body: {
            order_item_id: orderItemId,
            rating: draft.rating,
            comment: draft.comment.trim(),
            image_urls: imgCheck.urls,
          },
        }
      );
      const nextReviews = await resolveUpdatedReviews({
        submittedReview: res.review,
        currentReviews: reviews,
        fetchFallback: () =>
          api<{ reviews?: GatewayProductReview[] }>(
            `/api/orders/${encodeURIComponent(order.id)}/reviews`
          ),
      });
      setReviews(nextReviews);
      showToast("Cảm ơn bạn đã gửi đánh giá sản phẩm.");
      router.refresh();
    } catch (err) {
      setReviewErrorByItem((prev) => ({
        ...prev,
        [orderItemId]: err instanceof ApiError ? err.message : "Chưa gửi được đánh giá lúc này.",
      }));
    } finally {
      setSubmittingReviewItemId(null);
    }
  };

  const handlePreviewAddressUpdate = async () => {
    if (!order || !selectedNewAddrId || loadingAddrPreview) return;
    setAddrUpdateFeedback(null);
    setAcceptFeeIncrease(false);
    setLoadingAddrPreview(true);
    try {
      const res = await api<UpdateOrderAddressResponse>(
        `/api/orders/${encodeURIComponent(order.id)}/address`,
        {
          method: "PATCH",
          body: { address_id: selectedNewAddrId, preview_only: true },
        }
      );
      setAddrPreview(res);
      setAddrPreviewForId(selectedNewAddrId);
    } catch (err) {
      setAddrPreview(null);
      setAddrPreviewForId("");
      if (isAddressUpdateInProgressError(err)) {
        setAddressInProgressLock(true);
      }
      setAddrUpdateFeedback({ ok: false, message: translateAddressUpdateError(err) });
    } finally {
      setLoadingAddrPreview(false);
    }
  };

  const handleConfirmAddressUpdate = async () => {
    if (
      !order ||
      !addrPreview ||
      !selectedNewAddrId ||
      submittingAddrUpdate ||
      addressInProgressLock
    )
      return;
    if (isAddressPreviewStale(addrPreviewForId, selectedNewAddrId)) {
      setAddrUpdateFeedback({
        ok: false,
        message: "Địa chỉ đã thay đổi sau khi xem trước. Vui lòng bấm Xem phí mới lại.",
      });
      return;
    }
    const cls = classifyAddressUpdatePreview(order.paymentMethodRaw, addrPreview);
    if (cls === "prepaid_increase_blocked") {
      setAddrUpdateFeedback({
        ok: false,
        message:
          "Phí giao hàng mới cao hơn đơn đã thanh toán trước. Hiện chưa hỗ trợ thu phụ thu thêm.",
      });
      return;
    }
    if (cls === "cod_increase" && !acceptFeeIncrease) {
      setAddrUpdateFeedback({
        ok: false,
        message: "Vui lòng xác nhận đồng ý chênh lệch phí vận chuyển trước khi cập nhật.",
      });
      return;
    }

    setAddrUpdateFeedback(null);
    setSubmittingAddrUpdate(true);
    try {
      const res = await api<UpdateOrderAddressResponse>(
        `/api/orders/${encodeURIComponent(order.id)}/address`,
        {
          method: "PATCH",
          body: {
            address_id: selectedNewAddrId,
            preview_only: false,
            accept_fee_increase: cls === "cod_increase" ? acceptFeeIncrease : false,
            expected_new_shipping_fee_vnd: addrPreview.new_shipping_fee_vnd,
          },
        }
      );
      if (res.resolved_address) {
        setOrder((prev) =>
          prev
            ? {
                ...prev,
                buyerName: res.resolved_address!.recipient_name,
                buyerPhone: res.resolved_address!.recipient_phone,
                shippingAddress: formatAddressLine(
                  res.resolved_address!.street_address,
                  res.resolved_address!.ward_name,
                  res.resolved_address!.province_name
                ),
                shippingFee: res.new_shipping_fee_vnd,
                total: res.new_total_payable_vnd,
              }
            : prev
        );
      }
      setAddrPreview(null);
      setAddrPreviewForId("");
      setAddrUpdateFeedback({ ok: true, message: "Đã cập nhật địa chỉ nhận hàng cho đơn." });
      showToast("Đã cập nhật địa chỉ nhận hàng.");
      router.refresh();
    } catch (err) {
      if (isAddressUpdateInProgressError(err)) {
        setAddressInProgressLock(true);
      }
      if (err instanceof ApiError && err.code === "ADDRESS_FEE_CHANGED" && err.quote) {
        const updatedPreview = applyTopLevelAddressFeeQuote(
          { quote: err.quote },
          addrPreview,
          order.paymentMethodRaw
        );
        if (updatedPreview) {
          setAddrPreview(updatedPreview);
          setAcceptFeeIncrease(false);
        }
      }
      setAddrUpdateFeedback({ ok: false, message: translateAddressUpdateError(err) });
    } finally {
      setSubmittingAddrUpdate(false);
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

  const statusMeta =
    order.backendStatus === "RETURNED"
      ? {
          label: "Đã trả hàng",
          tone: "neutral" as const,
          description: "Đơn hàng đã hoàn tất thủ tục trả hàng.",
        }
      : ORDER_STATUS_META[order.status];
  const payMeta = order.paymentStatus ? PAYMENT_STATUS_META[order.paymentStatus] : null;
  const showOnlinePaymentBox = needsVietQrPayment({
    status: order.backendStatus,
    payment_method: order.paymentMethodRaw,
    payment_id: order.paymentId,
    payment_status: order.paymentStatusRaw,
    payment_expires_at: order.paymentExpiresAt,
  });
  const currentStepIdx = STANDARD_STEPS.findIndex((s) => s.status === order.status);
  const isTerminalSpecial = order.status === "da_huy" || order.status === "yeu_cau_tra_hang";
  const progressFraction = currentStepIdx <= 0 ? 0 : currentStepIdx / (STANDARD_STEPS.length - 1);

  const forwardLabelHref = buildShippingLabelHref(
    shipment?.tracking_code || order.trackingCode,
    order.id
  );
  const returnTracking = returnRequest?.return_tracking_code || returnShipment?.tracking_code || "";
  const returnLabelHref = buildShippingLabelHref(returnTracking, order.id);
  const returnStatusNorm = returnRequest
    ? normalizeEnumSuffix(returnRequest.status, "RETURN_STATUS_")
    : "";
  const returnStatusMeta = returnRequest ? getReturnStatusInfo(returnRequest.status) : null;
  const canRedeliver = canRequestRedelivery(shipment);
  const canReview = canReviewOrderStatus(order.backendStatus);
  const canUpdateAddress =
    canBuyerUpdateOrderAddress({
      status: order.backendStatus,
      shipment_job_status: order.shipmentJobStatus,
    }) && initialAddresses.length > 0;
  const addrPreviewCls = addrPreview
    ? classifyAddressUpdatePreview(order.paymentMethodRaw, addrPreview)
    : null;

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
          {showOnlinePaymentBox && order.paymentId && (
            <Button
              variant="primary"
              size="md"
              href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}?order=${encodeURIComponent(order.id)}`}
            >
              Tiếp tục thanh toán
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
          {order.canRequestReturn && !returnRequest && (
            <Button
              variant="secondary"
              size="md"
              onClick={() => {
                setReturnError(null);
                setEvidenceInputError(null);
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
            {checkoutWarnings.length > 0 && (
              <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="status">
                {checkoutWarnings.map((w) => (
                  <p key={w}>{w}</p>
                ))}
              </div>
            )}

            {showOnlinePaymentBox && order.paymentId && (
              <RetryPaymentBox
                paymentId={order.paymentId}
                order={order}
                onPaid={() => router.refresh()}
                onSwitchedToCod={() => router.refresh()}
              />
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

            {/* Forward Shipment & Redelivery */}
            {shipment && (
              <section className={styles.returnSheet} aria-label="Thông tin vận đơn">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Vận đơn giao hàng</h2>
                  <span
                    className={`${shared.statusTag} ${statusToneClass(
                      getShipmentStatusInfo(shipment.status).tone
                    )}`}
                  >
                    {getShipmentStatusInfo(shipment.status).label}
                  </span>
                </div>
                <p className={styles.sideLine}>
                  Hãng vận chuyển: <strong>{formatCarrierLabel(shipment.carrier_code)}</strong> · Mã
                  vận đơn:{" "}
                  <strong className="num">{shipment.tracking_code || "Đang cập nhật"}</strong>
                </p>
                {forwardLabelHref && (
                  <div className={shared.formActions}>
                    <a
                      href={forwardLabelHref}
                      target="_blank"
                      rel="noopener noreferrer"
                      className={shared.breadcrumbLink}
                    >
                      Xem phiếu vận đơn (nhấn Ctrl+P để in)
                    </a>
                  </div>
                )}

                {shipmentEvents.length > 0 && (
                  <ol className={styles.eventList}>
                    {shipmentEvents.slice(0, 6).map((ev) => (
                      <li key={ev.id} className={styles.eventRow}>
                        <span className={styles.eventTime}>
                          {ev.occurred_at ? renderOrderDateTime(ev.occurred_at) : ""}
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

                {canRedeliver && (
                  <form onSubmit={handleRequestRedelivery} className={styles.returnNoteBox}>
                    <span className={styles.returnSubLabel}>
                      Giao hàng chưa thành công — Yêu cầu giao lại
                    </span>
                    <div className={shared.formGrid} style={{ marginTop: 8 }}>
                      <div className={shared.field}>
                        <label htmlFor="redelivery-date" className={shared.fieldLabel}>
                          Ngày hẹn giao lại
                        </label>
                        <input
                          id="redelivery-date"
                          type="date"
                          min={getVietnamTodayIso()}
                          className={shared.input}
                          value={redeliveryDate}
                          onChange={(e) => setRedeliveryDate(e.target.value)}
                          disabled={submittingRedelivery}
                        />
                      </div>
                      <div className={shared.field}>
                        <label htmlFor="redelivery-note" className={shared.fieldLabel}>
                          Ghi chú cho bưu tá
                        </label>
                        <input
                          id="redelivery-note"
                          type="text"
                          className={shared.input}
                          value={redeliveryNote}
                          onChange={(e) => setRedeliveryNote(e.target.value)}
                          placeholder="Ví dụ: Giao trong giờ hành chính, gọi trước 15 phút"
                          disabled={submittingRedelivery}
                        />
                      </div>
                    </div>
                    {redeliveryFeedback && (
                      <p
                        className={`${shared.feedbackBox} ${
                          redeliveryFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                        }`}
                        style={{ marginTop: 8 }}
                        role={redeliveryFeedback.ok ? "status" : "alert"}
                      >
                        {redeliveryFeedback.message}
                      </p>
                    )}
                    <div className={shared.formActions} style={{ marginTop: 10 }}>
                      <Button
                        type="submit"
                        variant="primary"
                        size="sm"
                        loading={submittingRedelivery}
                        loadingLabel="Đang gửi"
                      >
                        Gửi yêu cầu giao lại
                      </Button>
                    </div>
                  </form>
                )}
              </section>
            )}

            {/* Return Request (10 statuses + Escalate + Additional Evidence) */}
            {returnRequest && returnStatusMeta && (
              <section className={styles.returnSheet} aria-label="Yêu cầu đổi trả">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Yêu cầu đổi trả</h2>
                  <span className={`${shared.statusTag} ${statusToneClass(returnStatusMeta.tone)}`}>
                    {returnStatusMeta.label}
                  </span>
                </div>

                <article className={styles.returnRequestItem}>
                  <p className={styles.sideMuted}>{returnStatusMeta.description}</p>

                  {returnRequest.reason && (
                    <p className={styles.returnBodyText}>
                      <strong>Lý do:</strong> {returnRequest.reason}
                    </p>
                  )}

                  {returnRequest.deadline_at && (
                    <p className={styles.sideMuted}>
                      Hạn xử lý:{" "}
                      <strong className="num">{formatDateTimeVi(returnRequest.deadline_at)}</strong>
                    </p>
                  )}

                  {returnRequest.evidence_urls && returnRequest.evidence_urls.length > 0 && (
                    <div>
                      <span className={styles.returnSubLabel}>Liên kết bằng chứng của bạn</span>
                      <div className={shared.formActions} style={{ marginTop: 6 }}>
                        {returnRequest.evidence_urls.map((rawUrl, idx) => {
                          const parsed = parseSafeEvidenceUrl(rawUrl);
                          if (!parsed) return null;
                          return (
                            <a
                              key={parsed.href}
                              href={parsed.href}
                              target="_blank"
                              rel="noopener noreferrer nofollow"
                              className={shared.breadcrumbLink}
                            >
                              Bằng chứng {idx + 1} ({parsed.hostname})
                            </a>
                          );
                        })}
                      </div>
                    </div>
                  )}

                  {returnRequest.refund_amount_vnd > 0 && (
                    <div className={styles.returnRefundRow}>
                      <span>
                        {returnStatusNorm === "REFUNDED" ? "Đã tạo hoàn tiền" : "Hoàn dự kiến"}
                      </span>
                      <strong className="num">{formatVnd(returnRequest.refund_amount_vnd)}</strong>
                    </div>
                  )}

                  {(returnRequest.return_fee_vnd > 0 || returnRequest.return_fee_bearer) && (
                    <p className={styles.sideMuted}>
                      Phí gửi hoàn:{" "}
                      <strong className="num">{formatVnd(returnRequest.return_fee_vnd)}</strong> (
                      {getReturnFeeBearerLabel(returnRequest.return_fee_bearer)})
                    </p>
                  )}

                  {returnTracking && (
                    <p className={`${styles.sideMuted} num`}>
                      Vận đơn hoàn trả: <strong>{returnTracking}</strong>{" "}
                      {returnLabelHref && (
                        <>
                          ·{" "}
                          <a
                            href={returnLabelHref}
                            target="_blank"
                            rel="noopener noreferrer"
                            className={shared.breadcrumbLink}
                          >
                            Mở phiếu hoàn hàng (Ctrl+P để in)
                          </a>
                        </>
                      )}
                    </p>
                  )}

                  {returnRequest.seller_response && (
                    <div className={styles.returnNoteBox}>
                      <span className={styles.returnSubLabel}>Phản hồi từ {brandName}</span>
                      <p className={styles.returnBodyText}>{returnRequest.seller_response}</p>
                    </div>
                  )}

                  {returnRequest.admin_note && (
                    <div className={styles.returnNoteBox}>
                      <span className={styles.returnSubLabel}>Ghi chú từ NexusMart</span>
                      <p className={styles.returnBodyText}>{returnRequest.admin_note}</p>
                    </div>
                  )}

                  {/* Escalate dispute when REJECTED */}
                  {returnStatusNorm === "REJECTED" && (
                    <form onSubmit={handleEscalateReturn} className={styles.returnNoteBox}>
                      <span className={styles.returnSubLabel}>
                        Không đồng ý với kết quả? Khiếu nại lên NexusMart
                      </span>
                      <textarea
                        aria-label="Lý do khiếu nại trả hàng"
                        className={shared.textarea}
                        style={{ marginTop: 8 }}
                        value={escalateReason}
                        onChange={(e) => setEscalateReason(e.target.value)}
                        placeholder="Trình bày rõ lý do bạn yêu cầu NexusMart xem xét phân xử (tối thiểu 10 ký tự)..."
                        disabled={escalating}
                      />
                      {escalateFeedback && (
                        <p
                          className={`${shared.feedbackBox} ${
                            escalateFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                          }`}
                          style={{ marginTop: 8 }}
                          role={escalateFeedback.ok ? "status" : "alert"}
                        >
                          {escalateFeedback.message}
                        </p>
                      )}
                      <div className={shared.formActions} style={{ marginTop: 10 }}>
                        <Button
                          type="submit"
                          variant="primary"
                          size="sm"
                          loading={escalating}
                          loadingLabel="Đang gửi"
                        >
                          Mở khiếu nại lên NexusMart
                        </Button>
                      </div>
                    </form>
                  )}

                  {/* Submit additional evidence when EVIDENCE_REQUESTED */}
                  {returnStatusNorm === "EVIDENCE_REQUESTED" && (
                    <form onSubmit={handleSubmitExtraEvidence} className={styles.returnNoteBox}>
                      <span className={styles.returnSubLabel}>Bổ sung bằng chứng theo yêu cầu</span>
                      <textarea
                        aria-label="Ghi chú bằng chứng bổ sung"
                        className={shared.textarea}
                        style={{ marginTop: 8 }}
                        value={extraEvidenceNote}
                        onChange={(e) => setExtraEvidenceNote(e.target.value)}
                        placeholder="Mô tả bổ sung tình trạng sản phẩm..."
                        disabled={submittingExtraEvidence}
                      />
                      <div className={shared.codeInputRow} style={{ marginTop: 8 }}>
                        <input
                          type="url"
                          className={shared.input}
                          value={extraEvidenceInput}
                          onChange={(e) => setExtraEvidenceInput(e.target.value)}
                          placeholder="https://... (liên kết ảnh/video bổ sung)"
                          disabled={
                            submittingExtraEvidence || extraEvidenceUrls.length >= MAX_EVIDENCE_URLS
                          }
                        />
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          disabled={
                            submittingExtraEvidence || extraEvidenceUrls.length >= MAX_EVIDENCE_URLS
                          }
                          onClick={() => {
                            const check = validateEvidenceUrls([
                              ...extraEvidenceUrls,
                              extraEvidenceInput.trim(),
                            ]);
                            if (!check.ok) {
                              setExtraEvidenceFeedback({
                                ok: false,
                                message: check.error ?? "Liên kết không hợp lệ.",
                              });
                              return;
                            }
                            setExtraEvidenceUrls(check.urls);
                            setExtraEvidenceInput("");
                            setExtraEvidenceFeedback(null);
                          }}
                        >
                          Thêm liên kết
                        </Button>
                      </div>
                      {extraEvidenceUrls.length > 0 && (
                        <div className={shared.formActions} style={{ marginTop: 8 }}>
                          {extraEvidenceUrls.map((u, idx) => (
                            <Button
                              key={u}
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() =>
                                setExtraEvidenceUrls((prev) => prev.filter((x) => x !== u))
                              }
                            >
                              Xóa liên kết {idx + 1}
                            </Button>
                          ))}
                        </div>
                      )}
                      {extraEvidenceFeedback && (
                        <p
                          className={`${shared.feedbackBox} ${
                            extraEvidenceFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                          }`}
                          style={{ marginTop: 8 }}
                          role={extraEvidenceFeedback.ok ? "status" : "alert"}
                        >
                          {extraEvidenceFeedback.message}
                        </p>
                      )}
                      <div className={shared.formActions} style={{ marginTop: 10 }}>
                        <Button
                          type="submit"
                          variant="primary"
                          size="sm"
                          loading={submittingExtraEvidence}
                          loadingLabel="Đang gửi"
                        >
                          Gửi bằng chứng bổ sung
                        </Button>
                      </div>
                    </form>
                  )}
                </article>
              </section>
            )}

            {/* Refunds & Buyer Bank Account */}
            {refunds.length > 0 && (
              <section className={styles.returnSheet} aria-label="Tiến trình hoàn tiền">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Hoàn tiền</h2>
                  <span className={`${shared.sectionNote} num`}>{refunds.length} lệnh</span>
                </div>
                {refunds.map((rf) => {
                  const rfInfo = getRefundStatusInfo(rf.status, rf);
                  const needsBank = canSubmitBuyerRefundBankAccount(rf);
                  const walletRefund = isWalletRefund(rf);
                  const draft = bankFormByRefund[rf.refund_id] ?? {
                    bankCode: rf.bank_code || VN_BANK_OPTIONS[0].code,
                    accountNumber: "",
                    accountName: rf.bank_account_name || order.buyerName || "",
                  };
                  const fb = bankFeedbackByRefund[rf.refund_id];

                  return (
                    <article key={rf.refund_id} className={styles.returnRequestItem}>
                      <div className={styles.returnRequestHead}>
                        <span className={`${shared.statusTag} ${statusToneClass(rfInfo.tone)}`}>
                          {rfInfo.label}
                        </span>
                        <strong className="num">{formatVnd(rf.amount_vnd)}</strong>
                      </div>
                      {rf.reason && (
                        <p className={styles.sideMuted}>{localizeRefundReason(rf.reason)}</p>
                      )}
                      {walletRefund && !rf.bank_code && (
                        <p className={styles.sideMuted}>
                          Khoản hoàn đang được xử lý tự động về ví ZaloPay gốc.
                        </p>
                      )}
                      {rf.bank_code && rf.bank_account_masked && (
                        <p className={styles.sideMuted}>
                          Tài khoản nhận hoàn: <strong>{rf.bank_code}</strong> ·{" "}
                          <span className="num">{rf.bank_account_masked}</span>{" "}
                          {rf.bank_account_name ? `(${rf.bank_account_name})` : ""}
                        </p>
                      )}

                      {needsBank && (
                        <form
                          onSubmit={(e) => void handleSubmitRefundBankAccount(e, rf.refund_id)}
                          className={styles.returnNoteBox}
                        >
                          <span className={styles.returnSubLabel}>
                            Cung cấp tài khoản ngân hàng nhận hoàn tiền
                          </span>
                          <div className={shared.formGrid} style={{ marginTop: 8 }}>
                            <div className={shared.field}>
                              <label
                                htmlFor={`refund-bank-${rf.refund_id}`}
                                className={shared.fieldLabel}
                              >
                                Ngân hàng
                              </label>
                              <select
                                id={`refund-bank-${rf.refund_id}`}
                                className={shared.select}
                                value={draft.bankCode}
                                onChange={(e) =>
                                  setBankFormByRefund((prev) => ({
                                    ...prev,
                                    [rf.refund_id]: { ...draft, bankCode: e.target.value },
                                  }))
                                }
                                disabled={submittingBankRefundId === rf.refund_id}
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
                                htmlFor={`refund-acct-${rf.refund_id}`}
                                className={shared.fieldLabel}
                              >
                                Số tài khoản (6–19 số)
                              </label>
                              <input
                                id={`refund-acct-${rf.refund_id}`}
                                type="text"
                                inputMode="numeric"
                                className={shared.input}
                                value={draft.accountNumber}
                                onChange={(e) =>
                                  setBankFormByRefund((prev) => ({
                                    ...prev,
                                    [rf.refund_id]: {
                                      ...draft,
                                      accountNumber: e.target.value.replace(/\D/g, ""),
                                    },
                                  }))
                                }
                                placeholder="0123456789"
                                disabled={submittingBankRefundId === rf.refund_id}
                              />
                            </div>
                            <div className={`${shared.field} ${shared.formFull}`}>
                              <label
                                htmlFor={`refund-holder-${rf.refund_id}`}
                                className={shared.fieldLabel}
                              >
                                Tên chủ tài khoản
                              </label>
                              <input
                                id={`refund-holder-${rf.refund_id}`}
                                type="text"
                                className={shared.input}
                                value={draft.accountName}
                                onChange={(e) =>
                                  setBankFormByRefund((prev) => ({
                                    ...prev,
                                    [rf.refund_id]: { ...draft, accountName: e.target.value },
                                  }))
                                }
                                placeholder="NGUYEN VAN A"
                                disabled={submittingBankRefundId === rf.refund_id}
                              />
                            </div>
                          </div>
                          {fb && (
                            <p
                              className={`${shared.feedbackBox} ${
                                fb.ok ? shared.feedbackSuccess : shared.feedbackError
                              }`}
                              style={{ marginTop: 8 }}
                              role={fb.ok ? "status" : "alert"}
                            >
                              {fb.message}
                            </p>
                          )}
                          <div className={shared.formActions} style={{ marginTop: 10 }}>
                            <Button
                              type="submit"
                              variant="primary"
                              size="sm"
                              loading={submittingBankRefundId === rf.refund_id}
                              loadingLabel="Đang lưu"
                            >
                              Lưu tài khoản nhận hoàn tiền
                            </Button>
                          </div>
                        </form>
                      )}
                    </article>
                  );
                })}
              </section>
            )}

            {/* Buyer Product Reviews (ONLY when backendStatus === COMPLETED) */}
            {canReview && (
              <section className={styles.returnSheet} aria-label="Đánh giá sản phẩm">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Đánh giá sản phẩm</h2>
                  <span className={`${shared.sectionNote} num`}>
                    {reviews.length}/{order.items.length}
                  </span>
                </div>

                {order.items.map((item) => {
                  const orderItemId = order.itemOrderIds[item.skuCode] || "";
                  if (!orderItemId) return null;
                  const alreadyReviewed = isOrderItemReviewed(orderItemId, reviews);
                  const existingReview = reviews.find((r) => r.order_item_id === orderItemId);
                  const draft = reviewDraftByItem[orderItemId] ?? {
                    rating: 5,
                    comment: "",
                    imageInput: "",
                    imageUrls: [],
                  };
                  const itemError = reviewErrorByItem[orderItemId];

                  return (
                    <article key={orderItemId} className={styles.returnRequestItem}>
                      <div className={styles.returnRequestHead}>
                        <strong>{item.productName}</strong>
                        <span className={styles.sideMuted}>
                          {[item.colorName, item.size ? `Size ${item.size}` : ""]
                            .filter(Boolean)
                            .join(" · ")}
                        </span>
                      </div>

                      {alreadyReviewed && existingReview ? (
                        <div className={styles.returnNoteBox}>
                          <p className={styles.sideLine}>
                            <strong>{existingReview.rating}/5 ★</strong>{" "}
                            {existingReview.created_at
                              ? `· ${formatDateTimeVi(existingReview.created_at)}`
                              : ""}
                          </p>
                          {existingReview.comment && (
                            <p className={styles.returnBodyText}>{existingReview.comment}</p>
                          )}
                          {existingReview.image_urls && existingReview.image_urls.length > 0 && (
                            <div className={styles.returnPhotoGrid}>
                              {existingReview.image_urls.map((rawUrl, idx) => {
                                const safe = parseSafeReviewImageUrl(rawUrl);
                                if (!safe) return null;
                                return (
                                  <img
                                    key={safe}
                                    src={safe}
                                    alt={`Ảnh đánh giá ${idx + 1}`}
                                    referrerPolicy="no-referrer"
                                    className={styles.returnPhotoThumb}
                                    loading="lazy"
                                  />
                                );
                              })}
                            </div>
                          )}
                        </div>
                      ) : (
                        <form onSubmit={(e) => void handleSubmitReview(e, orderItemId)}>
                          <div
                            className={shared.formActions}
                            role="radiogroup"
                            aria-label="Chọn số sao"
                          >
                            {[1, 2, 3, 4, 5].map((star) => (
                              <button
                                key={star}
                                type="button"
                                role="radio"
                                aria-checked={draft.rating === star}
                                className={`${shared.statusTag} ${
                                  draft.rating >= star ? shared.toneWarn : shared.toneNeutral
                                }`}
                                onClick={() =>
                                  setReviewDraftByItem((prev) => ({
                                    ...prev,
                                    [orderItemId]: { ...draft, rating: star },
                                  }))
                                }
                              >
                                {star} ★
                              </button>
                            ))}
                          </div>
                          <div className={shared.field} style={{ marginTop: 10 }}>
                            <label
                              htmlFor={`rev-comment-${orderItemId}`}
                              className={shared.fieldLabel}
                            >
                              Nhận xét của bạn
                            </label>
                            <textarea
                              id={`rev-comment-${orderItemId}`}
                              className={shared.textarea}
                              value={draft.comment}
                              maxLength={MAX_REVIEW_COMMENT_LEN}
                              onChange={(e) =>
                                setReviewDraftByItem((prev) => ({
                                  ...prev,
                                  [orderItemId]: { ...draft, comment: e.target.value },
                                }))
                              }
                              placeholder="Chất liệu, phom dáng, giao hàng..."
                              disabled={submittingReviewItemId === orderItemId}
                            />
                          </div>
                          <div className={shared.field} style={{ marginTop: 8 }}>
                            <label htmlFor={`rev-img-${orderItemId}`} className={shared.fieldLabel}>
                              Liên kết ảnh thực tế (tùy chọn, tối đa {MAX_REVIEW_IMAGES}, https://)
                            </label>
                            <div className={shared.codeInputRow}>
                              <input
                                id={`rev-img-${orderItemId}`}
                                type="text"
                                className={shared.input}
                                value={draft.imageInput}
                                onChange={(e) =>
                                  setReviewDraftByItem((prev) => ({
                                    ...prev,
                                    [orderItemId]: { ...draft, imageInput: e.target.value },
                                  }))
                                }
                                placeholder="https://..."
                                disabled={
                                  submittingReviewItemId === orderItemId ||
                                  draft.imageUrls.length >= MAX_REVIEW_IMAGES
                                }
                              />
                              <Button
                                type="button"
                                variant="secondary"
                                size="sm"
                                disabled={
                                  submittingReviewItemId === orderItemId ||
                                  draft.imageUrls.length >= MAX_REVIEW_IMAGES
                                }
                                onClick={() => {
                                  const candidate = draft.imageInput.trim();
                                  const safe = parseSafeReviewImageUrl(candidate);
                                  if (!safe) {
                                    setReviewErrorByItem((prev) => ({
                                      ...prev,
                                      [orderItemId]:
                                        "Chỉ chấp nhận đường dẫn https:// hoặc ảnh nội bộ /products/...",
                                    }));
                                    return;
                                  }
                                  setReviewErrorByItem((prev) => {
                                    const next = { ...prev };
                                    delete next[orderItemId];
                                    return next;
                                  });
                                  setReviewDraftByItem((prev) => ({
                                    ...prev,
                                    [orderItemId]: {
                                      ...draft,
                                      imageInput: "",
                                      imageUrls: [...draft.imageUrls, safe],
                                    },
                                  }));
                                }}
                              >
                                Thêm ảnh
                              </Button>
                            </div>
                          </div>
                          {itemError && (
                            <p className={shared.fieldError} style={{ marginTop: 8 }} role="alert">
                              {itemError}
                            </p>
                          )}
                          <div className={shared.formActions} style={{ marginTop: 10 }}>
                            <Button
                              type="submit"
                              variant="primary"
                              size="sm"
                              loading={submittingReviewItemId === orderItemId}
                              loadingLabel="Đang gửi"
                            >
                              Gửi đánh giá
                            </Button>
                          </div>
                        </form>
                      )}
                    </article>
                  );
                })}
              </section>
            )}

            {/* Pre-pickup Shipping Address Update */}
            {canUpdateAddress && (
              <details
                className={shared.disclosure}
                onToggle={(e) => {
                  if (!e.currentTarget.open) setAddressInProgressLock(false);
                }}
              >
                <summary className={shared.disclosureSummary}>
                  <span>Sửa địa chỉ nhận hàng (trước khi lấy hàng)</span>
                  <IconChevronDown size={16} className={shared.disclosureChevron} />
                </summary>
                <div className={shared.disclosureBody}>
                  <p className={styles.sideMuted} style={{ marginBottom: 8 }}>
                    Chọn từ sổ địa chỉ đã lưu. Cần thêm địa chỉ mới?{" "}
                    <Link href="/tai-khoan?tab=dia-chi" className={shared.textLink}>
                      Quản lý sổ địa chỉ
                    </Link>
                    .
                  </p>
                  <div className={shared.field}>
                    <label htmlFor="order-new-address" className={shared.fieldLabel}>
                      Chọn địa chỉ từ sổ địa chỉ
                    </label>
                    <select
                      id="order-new-address"
                      className={shared.select}
                      value={selectedNewAddrId}
                      onChange={(e) => {
                        setSelectedNewAddrId(e.target.value);
                        setAddrPreview(null);
                        setAddrPreviewForId("");
                        setAcceptFeeIncrease(false);
                        setAddrUpdateFeedback(null);
                      }}
                    >
                      {initialAddresses.map((addr) => (
                        <option key={addr.id} value={addr.id}>
                          {addr.recipient_name} · {addr.recipient_phone} —{" "}
                          {formatAddressLine(
                            addr.street_address,
                            addr.ward_name,
                            addr.province_name
                          )}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={shared.formActions} style={{ marginTop: 10 }}>
                    <Button
                      type="button"
                      variant="secondary"
                      size="sm"
                      loading={loadingAddrPreview}
                      loadingLabel="Đang kiểm tra phí"
                      onClick={() => void handlePreviewAddressUpdate()}
                    >
                      Xem trước phí giao hàng mới
                    </Button>
                  </div>

                  {addrPreview && (
                    <div className={styles.returnNoteBox} style={{ marginTop: 12 }}>
                      <p className={styles.sideLine}>
                        Phí vận chuyển:{" "}
                        <span className="num">{formatVnd(addrPreview.old_shipping_fee_vnd)}</span> →{" "}
                        <strong className="num">
                          {formatVnd(addrPreview.new_shipping_fee_vnd)}
                        </strong>
                      </p>
                      <p className={styles.sideLine}>
                        Tổng thanh toán mới:{" "}
                        <strong className="num">
                          {formatVnd(addrPreview.new_total_payable_vnd)}
                        </strong>
                      </p>

                      {addrPreviewCls === "prepaid_increase_blocked" ? (
                        <p className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                          Phí giao hàng mới cao hơn đơn đã thanh toán trước. Hiện chưa hỗ trợ thu
                          phụ thu thêm.
                        </p>
                      ) : (
                        <>
                          {addrPreviewCls === "cod_increase" && (
                            <label className={shared.checkboxRow} style={{ marginTop: 8 }}>
                              <input
                                type="checkbox"
                                className={shared.checkbox}
                                checked={acceptFeeIncrease}
                                onChange={(e) => setAcceptFeeIncrease(e.target.checked)}
                              />
                              <span>
                                Tôi đồng ý trả thêm{" "}
                                <strong className="num">
                                  {formatVnd(addrPreview.fee_delta_vnd)}
                                </strong>{" "}
                                chênh lệch phí giao hàng khi nhận COD.
                              </span>
                            </label>
                          )}
                          <div className={shared.formActions} style={{ marginTop: 10 }}>
                            <Button
                              type="button"
                              variant="primary"
                              size="sm"
                              loading={submittingAddrUpdate}
                              loadingLabel="Đang cập nhật"
                              disabled={
                                addressInProgressLock ||
                                (addrPreviewCls === "cod_increase" && !acceptFeeIncrease)
                              }
                              onClick={() => void handleConfirmAddressUpdate()}
                            >
                              {addrPreviewCls === "cod_increase"
                                ? "Xác nhận trả thêm"
                                : "Xác nhận đổi địa chỉ"}
                            </Button>
                            <Button
                              type="button"
                              variant="secondary"
                              size="sm"
                              onClick={() => {
                                setAddrPreview(null);
                                setAddrPreviewForId("");
                                setAcceptFeeIncrease(false);
                                setAddressInProgressLock(false);
                              }}
                            >
                              Giữ địa chỉ cũ
                            </Button>
                          </div>
                        </>
                      )}
                    </div>
                  )}

                  {addrUpdateFeedback && (
                    <p
                      className={`${shared.feedbackBox} ${
                        addrUpdateFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                      }`}
                      style={{ marginTop: 10 }}
                      role={addrUpdateFeedback.ok ? "status" : "alert"}
                    >
                      {addrUpdateFeedback.message}
                    </p>
                  )}
                </div>
              </details>
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
                  {order.shopProgramDiscount > 0 && (
                    <div className={styles.ledgerRow}>
                      <dt className={styles.ledgerLabel}>Giảm giá chương trình gian hàng</dt>
                      <dd className={`${styles.ledgerDiscount} num`}>
                        {formatSignedVnd(-order.shopProgramDiscount)}
                      </dd>
                    </div>
                  )}
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
                  {order.shopFreeshipDiscount > 0 && (
                    <div className={styles.ledgerRow}>
                      <dt className={styles.ledgerLabel}>Gian hàng hỗ trợ phí ship</dt>
                      <dd className={`${styles.ledgerDiscount} num`}>
                        {formatSignedVnd(-order.shopFreeshipDiscount)}
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

      {suggestedProducts.length > 0 && (
        <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={suggestedProducts} />
      )}

      {/* Slide-over sheet for submitting return request */}
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
                  Mô tả chi tiết tình trạng sản phẩm
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
                  placeholder="Áo còn nguyên tem mác, cần đổi trả do..."
                  disabled={submittingReturn}
                />
              </div>

              <div className={shared.field}>
                <label htmlFor="return-evidence-url" className={shared.fieldLabel}>
                  Liên kết bằng chứng (tối đa {MAX_EVIDENCE_URLS})
                </label>
                <p className={shared.fieldHint}>
                  Hiện chưa hỗ trợ tải ảnh trực tiếp; vui lòng dán đường dẫn https:// của ảnh hoặc
                  video minh chứng.
                </p>
                <div className={shared.codeInputRow}>
                  <input
                    id="return-evidence-url"
                    type="url"
                    className={shared.input}
                    aria-describedby={evidenceInputError ? "return-evidence-error" : undefined}
                    value={evidenceInput}
                    onChange={(e) => {
                      setEvidenceInput(e.target.value);
                      if (evidenceInputError) setEvidenceInputError(null);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        handleAddEvidenceUrl();
                      }
                    }}
                    placeholder="https://..."
                    disabled={submittingReturn || returnEvidenceUrls.length >= MAX_EVIDENCE_URLS}
                  />
                  <Button
                    type="button"
                    variant="secondary"
                    size="md"
                    disabled={submittingReturn || returnEvidenceUrls.length >= MAX_EVIDENCE_URLS}
                    onClick={handleAddEvidenceUrl}
                  >
                    Thêm
                  </Button>
                </div>
                {evidenceInputError && (
                  <p id="return-evidence-error" className={shared.fieldError} role="alert">
                    {evidenceInputError}
                  </p>
                )}

                {returnEvidenceUrls.length > 0 && (
                  <div className={styles.returnPhotoDraftList}>
                    {returnEvidenceUrls.map((url, idx) => (
                      <div key={url} className={styles.returnPhotoDraftCard}>
                        <span className={styles.returnPhotoDraftUrl}>{url}</span>
                        <button
                          type="button"
                          className={styles.returnPhotoRemoveBtn}
                          aria-label={`Xóa liên kết ${idx + 1}`}
                          disabled={submittingReturn}
                          onClick={() => {
                            setReturnEvidenceUrls((prev) => prev.filter((u) => u !== url));
                            setEvidenceInputError(null);
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

              {returnError && (
                <p id="return-form-error" className={shared.fieldError} role="alert">
                  {returnError}
                </p>
              )}

              <div className={shared.sheetFooter} style={{ margin: "auto -24px -24px" }}>
                <Button
                  type="button"
                  variant="secondary"
                  size="md"
                  disabled={submittingReturn}
                  onClick={() => setReturnSheetOpen(false)}
                >
                  Đóng
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  size="md"
                  loading={submittingReturn}
                  loadingLabel="Đang gửi"
                >
                  Gửi yêu cầu đổi trả
                </Button>
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
