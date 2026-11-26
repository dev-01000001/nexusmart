"use client";

import React, { Suspense, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AlertCircle,
  Check,
  ChevronDown,
  Copy,
  ExternalLink,
  PackageSearch,
  Star,
  Trash2,
} from "lucide-react";
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
import {
  GATEWAY_ORDER_STATUS_LABELS,
  formatCarrierLabel,
  formatDateTimeVi,
  formatSignedVnd,
  formatVnd,
} from "@/lib/format";
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
import { DetailLayout } from "@/components/app/DetailLayout";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Timeline, type TimelineItem } from "@/components/app/Timeline";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { RetryPaymentBox } from "@/features/commerce/RetryPaymentBox";
import { formatAddressLine } from "@/features/commerce/cartEnrich";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import {
  MAX_REVIEW_COMMENT_LEN,
  MAX_REVIEW_IMAGES,
  canReviewOrderStatus,
  isOrderItemReviewed,
  parseSafeReviewImageUrl,
  validateReviewImageUrls,
} from "@/features/product/reviewHelpers";
import { cn } from "@/lib/utils";
import { resolveListMutation, resolveReturnRequest } from "./orderReturnSync";
import type { OrderView } from "./orderView";
import {
  MAX_EVIDENCE_URLS,
  VN_BANK_OPTIONS,
  buildShippingLabelHref,
  canRequestRedelivery,
  canSubmitBuyerRefundBankAccount,
  getOrderStatusInfo,
  getPaymentMethodLabel,
  getPaymentStatusInfo,
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
import {
  RETURN_MAIN_MILESTONES,
  SITE_PAGE_CONTAINER,
  getReturnLifecycleProgress,
  orderStatusTone,
  paymentStatusTone,
  refundStatusTone,
  returnStatusTone,
  shipmentStatusTone,
} from "./ui/siteStatus";

function buildOrderSteps(backendStatus: string): Array<{ key: string; label: string }> {
  const norm = backendStatus.toUpperCase();
  return [
    {
      key: "CONFIRMED",
      label:
        norm === "PENDING"
          ? GATEWAY_ORDER_STATUS_LABELS.PENDING
          : GATEWAY_ORDER_STATUS_LABELS.CONFIRMED,
    },
    { key: "PREPARING", label: GATEWAY_ORDER_STATUS_LABELS.PREPARING },
    { key: "SHIPPING", label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING },
    {
      key: "DELIVERED",
      label:
        norm === "COMPLETED"
          ? GATEWAY_ORDER_STATUS_LABELS.COMPLETED
          : GATEWAY_ORDER_STATUS_LABELS.DELIVERED,
    },
  ];
}

function getOrderStepIndex(backendStatus: string): number {
  switch (backendStatus.toUpperCase()) {
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

const RETURN_WIZARD_STEPS = [
  { id: 1, label: "Lý do" },
  { id: 2, label: "Bằng chứng" },
  { id: 3, label: "Xác nhận" },
] as const;

function getVietnamTodayIso(): string {
  const vnNow = new Date(Date.now() + 7 * 3600 * 1000);
  return vnNow.toISOString().slice(0, 10);
}

function DetailLoadingSkeleton() {
  return (
    <div className="mx-auto max-w-[1000px] space-y-4" aria-busy="true" aria-label="Đang tải đơn">
      <Skeleton className="h-8 w-56" />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <div className="space-y-4">
          <Skeleton className="h-36 w-full rounded-xl" />
          <Skeleton className="h-52 w-full rounded-xl" />
        </div>
        <div className="space-y-4">
          <Skeleton className="h-44 w-full rounded-xl" />
          <Skeleton className="h-56 w-full rounded-xl" />
        </div>
      </div>
    </div>
  );
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
      setOrder((prev) => (orderChanged ? initialOrder : (initialOrder ?? prev)));
    }
    if (orderChanged || prevInitialShipmentRef.current !== initialShipment) {
      prevInitialShipmentRef.current = initialShipment;
      setShipment((prev) => (orderChanged ? initialShipment : (initialShipment ?? prev)));
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
        orderChanged ? initialReturnShipment : (initialReturnShipment ?? prev)
      );
    }
    if (orderChanged || prevInitialReturnRequestRef.current !== initialReturnRequest) {
      prevInitialReturnRequestRef.current = initialReturnRequest;
      setReturnRequest((prev) =>
        orderChanged ? initialReturnRequest : (initialReturnRequest ?? prev)
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
  const [cancelOpen, setCancelOpen] = useState(false);
  const [cancelReason, setCancelReason] = useState(CANCEL_REASONS[0]);
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const [reordering, setReordering] = useState(false);

  // Return request creation Sheet wizard (3 guided steps, singular POST /api/orders/[id]/return)
  const [returnSheetOpen, setReturnSheetOpen] = useState(false);
  const [returnWizardStep, setReturnWizardStep] = useState<1 | 2 | 3>(1);
  const [returnReason, setReturnReason] = useState(RETURN_REASONS[0]);
  const [returnDetail, setReturnDetail] = useState("");
  const [returnEvidenceUrls, setReturnEvidenceUrls] = useState<string[]>([]);
  const [evidenceInput, setEvidenceInput] = useState("");
  const [evidenceInputError, setEvidenceInputError] = useState<string | null>(null);
  const [submittingReturn, setSubmittingReturn] = useState(false);
  const [returnError, setReturnError] = useState<string | null>(null);

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
  const [addressEditOpen, setAddressEditOpen] = useState(false);
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

  const copyValue = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      showToast(`Đã sao chép ${label}.`);
    } catch {
      showToast(`Không sao chép được ${label}.`);
    }
  };

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

  const handleAdvanceReturnStep = () => {
    if (returnWizardStep === 1) {
      if (returnDetail.trim().length < 5) {
        setReturnError("Vui lòng mô tả tình trạng sản phẩm (tối thiểu 5 ký tự).");
        return;
      }
      setReturnError(null);
      setReturnWizardStep(2);
      return;
    }
    if (returnWizardStep === 2) {
      const evCheck = validateEvidenceUrls(returnEvidenceUrls);
      if (!evCheck.ok) {
        setEvidenceInputError(evCheck.error ?? "Liên kết bằng chứng không hợp lệ.");
        return;
      }
      setEvidenceInputError(null);
      setReturnWizardStep(3);
    }
  };

  const handleReturnSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!order || submittingReturn) return;
    const cleanNote = returnDetail.trim();
    if (cleanNote.length < 5) {
      setReturnError("Vui lòng mô tả tình trạng sản phẩm (tối thiểu 5 ký tự).");
      setReturnWizardStep(1);
      return;
    }
    const evCheck = validateEvidenceUrls(returnEvidenceUrls);
    if (!evCheck.ok) {
      setReturnError(evCheck.error ?? "Liên kết bằng chứng không hợp lệ.");
      setReturnWizardStep(2);
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
      const resolvedReturnRequest = await resolveReturnRequest(res.return_request, () =>
        api<{ return_request?: GatewayReturnRequest | null }>(
          `/api/orders/${encodeURIComponent(order.id)}/return`
        )
      );
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
      setReturnWizardStep(1);
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
      const resolvedReturnRequest = await resolveReturnRequest(res.return_request, () =>
        api<{ return_request?: GatewayReturnRequest | null }>(
          `/api/orders/${encodeURIComponent(order.id)}/return`
        )
      );
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
      const resolvedReturnRequest = await resolveReturnRequest(res.return_request, () =>
        api<{ return_request?: GatewayReturnRequest | null }>(
          `/api/orders/${encodeURIComponent(order.id)}/return`
        )
      );
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
      const nextRefunds = await resolveListMutation(
        res.refund,
        refunds,
        (rf) => rf.refund_id === refundId,
        async () => {
          const fallback = await api<{ items?: GatewayRefund[]; refunds?: GatewayRefund[] }>(
            `/api/orders/${encodeURIComponent(order.id)}/refunds`
          );
          return fallback?.items ?? fallback?.refunds;
        }
      );
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
      const nextReviews = await resolveListMutation(
        res.review,
        reviews,
        (existing) =>
          Boolean(
            (res.review?.id && existing.id === res.review.id) ||
            (res.review?.order_item_id && existing.order_item_id === res.review.order_item_id)
          ),
        async () => {
          const fallback = await api<{ reviews?: GatewayProductReview[] }>(
            `/api/orders/${encodeURIComponent(order.id)}/reviews`
          );
          return fallback?.reviews;
        }
      );
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
    <Breadcrumb className="mx-auto w-full max-w-[1000px]">
      <BreadcrumbList>
        <BreadcrumbItem>
          <BreadcrumbLink asChild>
            <Link href="/don-hang">Đơn hàng của tôi</Link>
          </BreadcrumbLink>
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>
          <BreadcrumbPage className="tabular-nums">{order?.orderCode ?? orderId}</BreadcrumbPage>
        </BreadcrumbItem>
      </BreadcrumbList>
    </Breadcrumb>
  );

  if (viewState === "loading") {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-4")}>
        {renderBreadcrumb()}
        <DetailLoadingSkeleton />
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-4")}>
        {renderBreadcrumb()}
        <div className="mx-auto max-w-[1000px]">
          <ErrorState
            title="Chưa thể tải chi tiết đơn hàng."
            description={initialError ?? "Thử lại sau ít giây."}
            retryHref="/don-hang"
            className="border border-border shadow-none"
          />
        </div>
      </div>
    );
  }

  if (viewState === "empty" || !order) {
    return (
      <div className={cn(SITE_PAGE_CONTAINER, "space-y-4")}>
        {renderBreadcrumb()}
        <div className="mx-auto max-w-[1000px]">
          <EmptyState
            icon={PackageSearch}
            title="Không tìm thấy đơn hàng này."
            description="Đơn hàng không tồn tại hoặc không thuộc tài khoản đang đăng nhập."
            action={
              <Button asChild className="min-h-10 shadow-none sm:min-h-9">
                <Link href="/don-hang">Về danh sách đơn</Link>
              </Button>
            }
            className="border border-border shadow-none"
          />
        </div>
      </div>
    );
  }

  const statusMeta = getOrderStatusInfo(order.backendStatus);
  const payMeta =
    order.paymentStatusRaw || order.paymentMethodRaw === "COD"
      ? getPaymentStatusInfo(order.paymentStatusRaw, order.paymentMethodRaw, order.backendStatus)
      : null;
  const showOnlinePaymentBox = needsVietQrPayment({
    status: order.backendStatus,
    payment_method: order.paymentMethodRaw,
    payment_id: order.paymentId,
    payment_status: order.paymentStatusRaw,
    payment_expires_at: order.paymentExpiresAt,
  });
  const orderSteps = buildOrderSteps(order.backendStatus);
  const currentStepIdx = getOrderStepIndex(order.backendStatus);
  const isCompletedOrder = order.backendStatus.toUpperCase() === "COMPLETED";
  const isTerminalSpecial =
    order.backendStatus === "CANCELLED" ||
    order.backendStatus === "RETURN_REQUESTED" ||
    order.backendStatus === "RETURNED";
  const progressPercent = isCompletedOrder
    ? 100
    : currentStepIdx <= 0
      ? 12
      : Math.round((currentStepIdx / (orderSteps.length - 1)) * 100);

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
  const returnProgress = returnRequest ? getReturnLifecycleProgress(returnRequest.status) : null;
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

  const orderTimelineItems: TimelineItem[] = order.timeline
    .slice()
    .reverse()
    .map((ev, idx) => ({
      id: `${ev.timestamp}-${idx}`,
      at: ev.timestamp ? formatDateTimeVi(ev.timestamp) : "",
      title: ev.title,
      description: ev.note || undefined,
    }));

  const shipmentTimelineItems: TimelineItem[] = shipmentEvents.slice(0, 6).map((ev) => ({
    id: ev.id,
    at: ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : "",
    title: getShipmentStatusInfo(ev.status).label,
    description: ev.description || undefined,
  }));

  const hasPrimaryHeaderAction = Boolean(
    (showOnlinePaymentBox && order.paymentId) || order.canConfirmReceived
  );

  const mainColumn = (
    <>
      {checkoutWarnings.length > 0 && (
        <Alert className="border-border shadow-none">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle>Lưu ý đơn hàng</AlertTitle>
          <AlertDescription>
            {checkoutWarnings.map((w) => (
              <p key={w}>{w}</p>
            ))}
          </AlertDescription>
        </Alert>
      )}

      {showOnlinePaymentBox && order.paymentId && (
        <RetryPaymentBox
          paymentId={order.paymentId}
          order={{ ...order, carrier: undefined }}
          onPaid={() => router.refresh()}
          onSwitchedToCod={() => router.refresh()}
        />
      )}

      {confirmError && (
        <Alert variant="destructive" className="shadow-none">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{confirmError}</AlertDescription>
        </Alert>
      )}

      {/* Status progress + items */}
      <Card className="border border-border shadow-none">
        <CardHeader className="border-b border-border pb-4">
          <CardTitle>Trạng thái đơn hàng</CardTitle>
          {order.updatedAt && (
            <CardAction>
              <span className="text-xs text-muted-foreground tabular-nums">
                {formatDateTimeVi(order.updatedAt)}
              </span>
            </CardAction>
          )}
        </CardHeader>
        <CardContent className="space-y-4">
          {!isTerminalSpecial ? (
            <div className="space-y-3">
              <Progress value={progressPercent} aria-label="Tiến trình giao hàng" />
              <ol
                className="grid grid-cols-2 gap-2.5 sm:grid-cols-4"
                aria-label="Các bước xử lý đơn hàng"
              >
                {orderSteps.map((step, idx) => {
                  const done = isCompletedOrder || currentStepIdx > idx;
                  const current = !isCompletedOrder && currentStepIdx === idx;
                  return (
                    <li
                      key={step.key}
                      aria-current={current ? "step" : undefined}
                      className={cn(
                        "flex items-center gap-2 rounded-lg border px-2.5 py-2 text-xs",
                        done
                          ? "border-brand/40 bg-background text-foreground"
                          : current
                            ? "border-brand bg-background font-semibold text-foreground"
                            : "border-border bg-muted/40 text-muted-foreground"
                      )}
                    >
                      <span
                        aria-hidden="true"
                        className={cn(
                          "flex size-5 shrink-0 items-center justify-center rounded-full text-[11px]",
                          done
                            ? "bg-brand text-brand-foreground"
                            : current
                              ? "border-2 border-brand text-brand"
                              : "border border-border text-muted-foreground"
                        )}
                      >
                        {done ? <Check className="size-3" /> : idx + 1}
                      </span>
                      <span className="truncate">{step.label}</span>
                    </li>
                  );
                })}
              </ol>
            </div>
          ) : (
            <Alert
              variant={order.backendStatus === "CANCELLED" ? "destructive" : "default"}
              className="shadow-none"
            >
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertTitle>{statusMeta.label}</AlertTitle>
              <AlertDescription>
                {order.returnReason ?? order.cancelReason ?? statusMeta.description}
              </AlertDescription>
            </Alert>
          )}
        </CardContent>
      </Card>

      {/* Order items */}
      <Card className="border border-border shadow-none">
        <CardHeader className="border-b border-border pb-4">
          <CardTitle>Sản phẩm</CardTitle>
          <CardAction>
            <span className="text-xs text-muted-foreground tabular-nums">
              {order.items.length} mục
            </span>
          </CardAction>
        </CardHeader>
        <CardContent className="divide-y divide-border">
          {order.items.map((item) => {
            const productHref = `/san-pham/${item.productSlug || item.productId}`;
            return (
              <div key={item.skuCode} className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
                <Link
                  href={productHref}
                  className="block h-20 w-16 shrink-0 overflow-hidden rounded-md border border-border bg-muted"
                >
                  <img
                    src={item.image || "/products/ao-thun-cotton-trang.png"}
                    alt={item.productName}
                    width={896}
                    height={1200}
                    className="size-full object-cover"
                  />
                </Link>
                <div className="min-w-0 flex-1 space-y-1.5">
                  <Link
                    href={productHref}
                    className="line-clamp-2 text-sm font-medium text-foreground hover:text-brand hover:underline"
                  >
                    {item.productName}
                  </Link>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {item.colorName && (
                      <Badge variant="secondary" className="rounded-sm">
                        {item.colorName}
                      </Badge>
                    )}
                    {item.size && (
                      <Badge variant="secondary" className="rounded-sm">
                        Size {item.size}
                      </Badge>
                    )}
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {formatVnd(item.unitPrice)} × {item.quantity}
                    </span>
                  </div>
                </div>
                <Money vnd={item.lineTotal} size="sm" className="shrink-0 pt-0.5" />
              </div>
            );
          })}
        </CardContent>
      </Card>

      {/* Forward Shipment & Redelivery */}
      {shipment && (
        <Card className="border border-border shadow-none" aria-label="Thông tin vận đơn">
          <CardHeader className="border-b border-border pb-4">
            <CardTitle>Vận đơn giao hàng</CardTitle>
            <CardAction>
              <StatusBadge tone={shipmentStatusTone(shipment.status)}>
                {getShipmentStatusInfo(shipment.status).label}
              </StatusBadge>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <p className="text-foreground">
                Hãng vận chuyển: <strong>{formatCarrierLabel(shipment.carrier_code)}</strong> · Mã
                vận đơn:{" "}
                <strong className="font-mono tabular-nums">
                  {shipment.tracking_code || "Đang cập nhật"}
                </strong>
              </p>
              {forwardLabelHref && (
                <a
                  href={forwardLabelHref}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-xs font-medium text-brand hover:underline"
                >
                  <span>Xem phiếu vận đơn (nhấn Ctrl+P để in)</span>
                  <ExternalLink className="size-3.5" aria-hidden="true" />
                </a>
              )}
            </div>

            {shipmentTimelineItems.length > 0 && (
              <div className="pt-1">
                <Timeline items={shipmentTimelineItems} />
              </div>
            )}

            {canRedeliver && (
              <form
                onSubmit={handleRequestRedelivery}
                className="space-y-3 rounded-lg border border-border bg-background p-4"
              >
                <p className="text-sm font-semibold text-foreground">
                  Giao hàng chưa thành công — Yêu cầu giao lại
                </p>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="redelivery-date">Ngày hẹn giao lại</FieldLabel>
                    <Input
                      id="redelivery-date"
                      type="date"
                      min={getVietnamTodayIso()}
                      value={redeliveryDate}
                      onChange={(e) => setRedeliveryDate(e.target.value)}
                      disabled={submittingRedelivery}
                      className="h-10 bg-card"
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="redelivery-note">Ghi chú cho bưu tá</FieldLabel>
                    <Input
                      id="redelivery-note"
                      type="text"
                      value={redeliveryNote}
                      onChange={(e) => setRedeliveryNote(e.target.value)}
                      placeholder="Ví dụ: Giao trong giờ hành chính, gọi trước 15 phút"
                      disabled={submittingRedelivery}
                      className="h-10 bg-card"
                    />
                  </Field>
                </div>
                {redeliveryFeedback && (
                  <Alert
                    variant={redeliveryFeedback.ok ? "default" : "destructive"}
                    className="shadow-none"
                  >
                    <AlertDescription>{redeliveryFeedback.message}</AlertDescription>
                  </Alert>
                )}
                <Button
                  type="submit"
                  size="sm"
                  disabled={submittingRedelivery}
                  className="min-h-10 shadow-none sm:min-h-8"
                >
                  {submittingRedelivery ? "Đang gửi..." : "Gửi yêu cầu giao lại"}
                </Button>
              </form>
            )}
          </CardContent>
        </Card>
      )}

      {/* Return Request (10-status lifecycle + Escalate + Additional Evidence) */}
      {returnRequest && returnStatusMeta && returnProgress && (
        <Card className="border border-border shadow-none" aria-label="Yêu cầu đổi trả">
          <CardHeader className="border-b border-border pb-4">
            <CardTitle>Yêu cầu đổi trả</CardTitle>
            <CardAction>
              <StatusBadge tone={returnStatusTone(returnRequest.status)}>
                {returnStatusMeta.label}
              </StatusBadge>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2.5">
              <Progress value={returnProgress.percent} aria-label="Tiến độ xử lý đổi trả" />
              {!returnProgress.isDisputeBranch && !returnProgress.isClosed && (
                <ol className="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
                  {RETURN_MAIN_MILESTONES.map((step, idx) => {
                    const reached = idx <= returnProgress.stepIndex;
                    return (
                      <li
                        key={step.key}
                        className={cn(
                          "flex items-center gap-1.5 rounded-md border px-2 py-1.5 text-xs",
                          reached
                            ? "border-brand/40 bg-background font-medium text-foreground"
                            : "border-border text-muted-foreground"
                        )}
                      >
                        <span
                          aria-hidden="true"
                          className={cn(
                            "flex size-4 shrink-0 items-center justify-center rounded-full text-[10px]",
                            reached
                              ? "bg-brand text-brand-foreground"
                              : "border border-border text-muted-foreground"
                          )}
                        >
                          {idx < returnProgress.stepIndex ? (
                            <Check className="size-2.5" />
                          ) : (
                            idx + 1
                          )}
                        </span>
                        <span className="truncate">{step.label}</span>
                      </li>
                    );
                  })}
                </ol>
              )}
            </div>

            <p className="text-sm text-muted-foreground">{returnStatusMeta.description}</p>

            {returnRequest.reason && (
              <p className="text-sm text-foreground">
                <strong>Lý do:</strong> {returnRequest.reason}
              </p>
            )}

            {returnRequest.deadline_at && (
              <p className="text-xs text-muted-foreground tabular-nums">
                Hạn xử lý:{" "}
                <strong className="text-foreground">
                  {formatDateTimeVi(returnRequest.deadline_at)}
                </strong>
              </p>
            )}

            {returnRequest.evidence_urls && returnRequest.evidence_urls.length > 0 && (
              <div className="space-y-1.5">
                <span className="block text-xs font-medium text-muted-foreground">
                  Liên kết bằng chứng của bạn
                </span>
                <div className="flex flex-wrap gap-2">
                  {returnRequest.evidence_urls.map((rawUrl, idx) => {
                    const parsed = parseSafeEvidenceUrl(rawUrl);
                    if (!parsed) return null;
                    return (
                      <a
                        key={parsed.href}
                        href={parsed.href}
                        target="_blank"
                        rel="noopener noreferrer nofollow"
                        className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-2.5 py-1 text-xs font-medium text-brand hover:underline"
                      >
                        <span>
                          Bằng chứng {idx + 1} ({parsed.hostname})
                        </span>
                        <ExternalLink className="size-3" aria-hidden="true" />
                      </a>
                    );
                  })}
                </div>
              </div>
            )}

            {returnRequest.refund_amount_vnd > 0 && (
              <div className="flex items-center justify-between rounded-lg border border-border bg-background px-3.5 py-2.5 text-sm">
                <span className="text-muted-foreground">
                  {returnStatusNorm === "REFUNDED" ? "Đã tạo hoàn tiền" : "Hoàn dự kiến"}
                </span>
                <Money vnd={returnRequest.refund_amount_vnd} size="md" />
              </div>
            )}

            {(returnRequest.return_fee_vnd > 0 || returnRequest.return_fee_bearer) && (
              <p className="text-xs text-muted-foreground tabular-nums">
                Phí gửi hoàn:{" "}
                <strong className="text-foreground">
                  {formatVnd(returnRequest.return_fee_vnd)}
                </strong>{" "}
                ({getReturnFeeBearerLabel(returnRequest.return_fee_bearer)})
              </p>
            )}

            {returnTracking && (
              <p className="text-xs text-muted-foreground tabular-nums">
                Vận đơn hoàn trả: <strong className="text-foreground">{returnTracking}</strong>{" "}
                {returnLabelHref && (
                  <>
                    ·{" "}
                    <a
                      href={returnLabelHref}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="font-medium text-brand hover:underline"
                    >
                      Mở phiếu hoàn hàng (Ctrl+P để in)
                    </a>
                  </>
                )}
              </p>
            )}

            {returnRequest.seller_response && (
              <div className="rounded-lg border border-border bg-background p-3.5">
                <span className="block text-xs font-semibold text-muted-foreground">
                  Phản hồi từ {brandName}
                </span>
                <p className="mt-1 text-sm text-foreground">{returnRequest.seller_response}</p>
              </div>
            )}

            {returnRequest.admin_note && (
              <div className="rounded-lg border border-border bg-background p-3.5">
                <span className="block text-xs font-semibold text-muted-foreground">
                  Ghi chú từ NexusMart
                </span>
                <p className="mt-1 text-sm text-foreground">{returnRequest.admin_note}</p>
              </div>
            )}

            {returnStatusNorm === "REJECTED" && (
              <form
                onSubmit={handleEscalateReturn}
                className="space-y-3 rounded-lg border border-border bg-background p-4"
              >
                <Field>
                  <FieldLabel htmlFor="escalate-return-reason">
                    Không đồng ý với kết quả? Khiếu nại lên NexusMart
                  </FieldLabel>
                  <Textarea
                    id="escalate-return-reason"
                    aria-label="Lý do khiếu nại trả hàng"
                    value={escalateReason}
                    onChange={(e) => setEscalateReason(e.target.value)}
                    placeholder="Trình bày rõ lý do bạn yêu cầu NexusMart xem xét phân xử (tối thiểu 10 ký tự)..."
                    disabled={escalating}
                    className="bg-card"
                  />
                </Field>
                {escalateFeedback && (
                  <Alert
                    variant={escalateFeedback.ok ? "default" : "destructive"}
                    className="shadow-none"
                  >
                    <AlertDescription>{escalateFeedback.message}</AlertDescription>
                  </Alert>
                )}
                <Button
                  type="submit"
                  size="sm"
                  disabled={escalating}
                  className="min-h-10 shadow-none sm:min-h-8"
                >
                  {escalating ? "Đang gửi..." : "Mở khiếu nại lên NexusMart"}
                </Button>
              </form>
            )}

            {returnStatusNorm === "EVIDENCE_REQUESTED" && (
              <form
                onSubmit={handleSubmitExtraEvidence}
                className="space-y-3 rounded-lg border border-border bg-background p-4"
              >
                <Field>
                  <FieldLabel htmlFor="extra-evidence-note">
                    Bổ sung bằng chứng theo yêu cầu
                  </FieldLabel>
                  <Textarea
                    id="extra-evidence-note"
                    aria-label="Ghi chú bằng chứng bổ sung"
                    value={extraEvidenceNote}
                    onChange={(e) => setExtraEvidenceNote(e.target.value)}
                    placeholder="Mô tả bổ sung tình trạng sản phẩm..."
                    disabled={submittingExtraEvidence}
                    className="bg-card"
                  />
                </Field>
                <div className="flex flex-col gap-2 sm:flex-row">
                  <Input
                    type="url"
                    value={extraEvidenceInput}
                    onChange={(e) => setExtraEvidenceInput(e.target.value)}
                    placeholder="https://... (liên kết ảnh/video bổ sung)"
                    disabled={
                      submittingExtraEvidence || extraEvidenceUrls.length >= MAX_EVIDENCE_URLS
                    }
                    className="h-10 bg-card"
                  />
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    className="min-h-10 shrink-0"
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
                  <div className="flex flex-wrap gap-2">
                    {extraEvidenceUrls.map((u, idx) => (
                      <Button
                        key={u}
                        type="button"
                        variant="outline"
                        size="sm"
                        className="shadow-none"
                        onClick={() => setExtraEvidenceUrls((prev) => prev.filter((x) => x !== u))}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        <span>Xóa liên kết {idx + 1}</span>
                      </Button>
                    ))}
                  </div>
                )}
                {extraEvidenceFeedback && (
                  <Alert
                    variant={extraEvidenceFeedback.ok ? "default" : "destructive"}
                    className="shadow-none"
                  >
                    <AlertDescription>{extraEvidenceFeedback.message}</AlertDescription>
                  </Alert>
                )}
                <Button
                  type="submit"
                  size="sm"
                  disabled={submittingExtraEvidence}
                  className="min-h-10 shadow-none sm:min-h-8"
                >
                  {submittingExtraEvidence ? "Đang gửi..." : "Gửi bằng chứng bổ sung"}
                </Button>
              </form>
            )}
          </CardContent>
        </Card>
      )}

      {/* Refunds & Buyer Bank Account */}
      {refunds.length > 0 && (
        <Card className="border border-border shadow-none" aria-label="Tiến trình hoàn tiền">
          <CardHeader className="border-b border-border pb-4">
            <CardTitle>Hoàn tiền</CardTitle>
            <CardAction>
              <span className="text-xs text-muted-foreground tabular-nums">
                {refunds.length} lệnh
              </span>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4">
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
                <article
                  key={rf.refund_id}
                  className="space-y-3 rounded-lg border border-border bg-background p-4"
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <StatusBadge tone={refundStatusTone(rf.status, rf)}>{rfInfo.label}</StatusBadge>
                    <Money vnd={rf.amount_vnd} size="md" />
                  </div>
                  {rf.reason && (
                    <p className="text-xs text-muted-foreground">
                      {localizeRefundReason(rf.reason)}
                    </p>
                  )}
                  {walletRefund && !rf.bank_code && (
                    <p className="text-xs text-muted-foreground">
                      Khoản hoàn đang được xử lý tự động về ví ZaloPay gốc.
                    </p>
                  )}
                  {rf.bank_code && rf.bank_account_masked && (
                    <p className="text-xs text-muted-foreground">
                      Tài khoản nhận hoàn:{" "}
                      <strong className="text-foreground">{rf.bank_code}</strong> ·{" "}
                      <span className="tabular-nums">{rf.bank_account_masked}</span>{" "}
                      {rf.bank_account_name ? `(${rf.bank_account_name})` : ""}
                    </p>
                  )}

                  {needsBank && (
                    <form
                      onSubmit={(e) => void handleSubmitRefundBankAccount(e, rf.refund_id)}
                      className="space-y-3 border-t border-border pt-3"
                    >
                      <p className="text-xs font-semibold text-foreground">
                        Cung cấp tài khoản ngân hàng nhận hoàn tiền
                      </p>
                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                        <Field>
                          <FieldLabel htmlFor={`refund-bank-${rf.refund_id}`}>Ngân hàng</FieldLabel>
                          <Select
                            value={draft.bankCode}
                            onValueChange={(value) =>
                              setBankFormByRefund((prev) => ({
                                ...prev,
                                [rf.refund_id]: { ...draft, bankCode: value },
                              }))
                            }
                            disabled={submittingBankRefundId === rf.refund_id}
                          >
                            <SelectTrigger
                              id={`refund-bank-${rf.refund_id}`}
                              className="h-10 w-full bg-card"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {VN_BANK_OPTIONS.map((b) => (
                                <SelectItem key={b.code} value={b.code}>
                                  {b.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>
                        <Field>
                          <FieldLabel htmlFor={`refund-acct-${rf.refund_id}`}>
                            Số tài khoản (6–19 số)
                          </FieldLabel>
                          <Input
                            id={`refund-acct-${rf.refund_id}`}
                            type="text"
                            inputMode="numeric"
                            className="h-10 bg-card tabular-nums"
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
                        </Field>
                        <Field className="sm:col-span-2">
                          <FieldLabel htmlFor={`refund-holder-${rf.refund_id}`}>
                            Tên chủ tài khoản
                          </FieldLabel>
                          <Input
                            id={`refund-holder-${rf.refund_id}`}
                            type="text"
                            className="h-10 bg-card"
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
                        </Field>
                      </div>
                      {fb && (
                        <Alert variant={fb.ok ? "default" : "destructive"} className="shadow-none">
                          <AlertDescription>{fb.message}</AlertDescription>
                        </Alert>
                      )}
                      <Button
                        type="submit"
                        size="sm"
                        disabled={submittingBankRefundId === rf.refund_id}
                        className="min-h-10 shadow-none sm:min-h-8"
                      >
                        {submittingBankRefundId === rf.refund_id
                          ? "Đang lưu..."
                          : "Lưu tài khoản nhận hoàn tiền"}
                      </Button>
                    </form>
                  )}
                </article>
              );
            })}
          </CardContent>
        </Card>
      )}

      {/* Buyer Product Reviews (ONLY when backendStatus === COMPLETED) */}
      {canReview && (
        <Card className="border border-border shadow-none" aria-label="Đánh giá sản phẩm">
          <CardHeader className="border-b border-border pb-4">
            <CardTitle>Đánh giá sản phẩm</CardTitle>
            <CardAction>
              <span className="text-xs text-muted-foreground tabular-nums">
                {reviews.length}/{order.items.length}
              </span>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4">
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
                <article
                  key={orderItemId}
                  className="space-y-3 rounded-lg border border-border bg-background p-4"
                >
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <strong className="text-sm font-semibold text-foreground">
                      {item.productName}
                    </strong>
                    <span className="text-xs text-muted-foreground">
                      {[item.colorName, item.size ? `Size ${item.size}` : ""]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </div>

                  {alreadyReviewed && existingReview ? (
                    <div className="space-y-2 rounded-md border border-border bg-card p-3">
                      <p className="flex items-center gap-1.5 text-sm text-foreground tabular-nums">
                        <strong>{existingReview.rating}/5 ★</strong>
                        {existingReview.created_at && (
                          <span className="text-xs text-muted-foreground">
                            · {formatDateTimeVi(existingReview.created_at)}
                          </span>
                        )}
                      </p>
                      {existingReview.comment && (
                        <p className="text-sm text-foreground">{existingReview.comment}</p>
                      )}
                      {existingReview.image_urls && existingReview.image_urls.length > 0 && (
                        <div className="flex flex-wrap gap-2">
                          {existingReview.image_urls.map((rawUrl, idx) => {
                            const safe = parseSafeReviewImageUrl(rawUrl);
                            if (!safe) return null;
                            return (
                              <img
                                key={safe}
                                src={safe}
                                alt={`Ảnh đánh giá ${idx + 1}`}
                                referrerPolicy="no-referrer"
                                className="size-16 rounded-md border border-border object-cover"
                                loading="lazy"
                              />
                            );
                          })}
                        </div>
                      )}
                    </div>
                  ) : (
                    <form
                      onSubmit={(e) => void handleSubmitReview(e, orderItemId)}
                      className="space-y-3"
                    >
                      <div
                        className="flex flex-wrap gap-1.5"
                        role="radiogroup"
                        aria-label="Chọn số sao"
                      >
                        {[1, 2, 3, 4, 5].map((star) => {
                          const selected = draft.rating >= star;
                          return (
                            <Button
                              key={star}
                              type="button"
                              role="radio"
                              aria-checked={draft.rating === star}
                              variant={selected ? "default" : "outline"}
                              size="sm"
                              className="min-h-10 shadow-none sm:min-h-8"
                              onClick={() =>
                                setReviewDraftByItem((prev) => ({
                                  ...prev,
                                  [orderItemId]: { ...draft, rating: star },
                                }))
                              }
                            >
                              <Star
                                className={cn("size-3.5", selected && "fill-current")}
                                aria-hidden="true"
                              />
                              <span>{star} ★</span>
                            </Button>
                          );
                        })}
                      </div>

                      <Field>
                        <FieldLabel htmlFor={`rev-comment-${orderItemId}`}>
                          Nhận xét của bạn
                        </FieldLabel>
                        <Textarea
                          id={`rev-comment-${orderItemId}`}
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
                          className="bg-card"
                        />
                      </Field>

                      <Field>
                        <FieldLabel htmlFor={`rev-img-${orderItemId}`}>
                          Liên kết ảnh thực tế (tùy chọn, tối đa {MAX_REVIEW_IMAGES}, https://)
                        </FieldLabel>
                        <div className="flex flex-col gap-2 sm:flex-row">
                          <Input
                            id={`rev-img-${orderItemId}`}
                            type="text"
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
                            className="h-10 bg-card"
                          />
                          <Button
                            type="button"
                            variant="secondary"
                            size="sm"
                            className="min-h-10 shrink-0"
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
                      </Field>

                      {draft.imageUrls.length > 0 && (
                        <div className="flex flex-wrap gap-2">
                          {draft.imageUrls.map((imgUrl, idx) => (
                            <Button
                              key={imgUrl}
                              type="button"
                              variant="outline"
                              size="sm"
                              className="shadow-none"
                              onClick={() =>
                                setReviewDraftByItem((prev) => ({
                                  ...prev,
                                  [orderItemId]: {
                                    ...draft,
                                    imageUrls: draft.imageUrls.filter((x) => x !== imgUrl),
                                  },
                                }))
                              }
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                              <span>Xóa ảnh {idx + 1}</span>
                            </Button>
                          ))}
                        </div>
                      )}

                      {itemError && <FieldError>{itemError}</FieldError>}

                      <Button
                        type="submit"
                        size="sm"
                        disabled={submittingReviewItemId === orderItemId}
                        className="min-h-10 shadow-none sm:min-h-8"
                      >
                        {submittingReviewItemId === orderItemId ? "Đang gửi..." : "Gửi đánh giá"}
                      </Button>
                    </form>
                  )}
                </article>
              );
            })}
          </CardContent>
        </Card>
      )}

      {/* Pre-pickup Shipping Address Update */}
      {canUpdateAddress && (
        <Card className="border border-border py-0 shadow-none">
          <Collapsible
            open={addressEditOpen}
            onOpenChange={(open) => {
              setAddressEditOpen(open);
              if (!open) setAddressInProgressLock(false);
            }}
          >
            <CollapsibleTrigger asChild>
              <button
                type="button"
                className="flex min-h-12 w-full items-center justify-between gap-3 px-4 py-3 text-left text-sm font-semibold text-foreground transition-colors hover:bg-accent/50 md:px-5"
              >
                <span>Sửa địa chỉ nhận hàng (trước khi lấy hàng)</span>
                <ChevronDown
                  className={cn(
                    "size-4 shrink-0 text-muted-foreground transition-transform",
                    addressEditOpen && "rotate-180"
                  )}
                  aria-hidden="true"
                />
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent className="space-y-4 border-t border-border px-4 py-4 md:px-5">
              <p className="text-xs text-muted-foreground">
                Chọn từ sổ địa chỉ đã lưu. Cần thêm địa chỉ mới?{" "}
                <Link
                  href="/tai-khoan?tab=dia-chi"
                  className="font-medium text-brand hover:underline"
                >
                  Quản lý sổ địa chỉ
                </Link>
                .
              </p>
              <Field>
                <FieldLabel htmlFor="order-new-address">Chọn địa chỉ từ sổ địa chỉ</FieldLabel>
                <Select
                  value={selectedNewAddrId}
                  onValueChange={(value) => {
                    setSelectedNewAddrId(value);
                    setAddrPreview(null);
                    setAddrPreviewForId("");
                    setAcceptFeeIncrease(false);
                    setAddrUpdateFeedback(null);
                  }}
                >
                  <SelectTrigger id="order-new-address" className="h-10 w-full bg-background">
                    <SelectValue placeholder="Chọn địa chỉ" />
                  </SelectTrigger>
                  <SelectContent>
                    {initialAddresses.map((addr) => (
                      <SelectItem key={addr.id} value={addr.id}>
                        {addr.recipient_name} · {addr.recipient_phone} —{" "}
                        {formatAddressLine(addr.street_address, addr.ward_name, addr.province_name)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <div>
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  disabled={loadingAddrPreview}
                  onClick={() => void handlePreviewAddressUpdate()}
                  className="min-h-10 sm:min-h-8"
                >
                  {loadingAddrPreview ? "Đang kiểm tra phí..." : "Xem trước phí giao hàng mới"}
                </Button>
              </div>

              {addrPreview && (
                <div className="space-y-3 rounded-lg border border-border bg-background p-4">
                  <div className="space-y-1 text-sm tabular-nums">
                    <p>
                      Phí vận chuyển: {formatVnd(addrPreview.old_shipping_fee_vnd)} →{" "}
                      <strong>{formatVnd(addrPreview.new_shipping_fee_vnd)}</strong>
                    </p>
                    <p>
                      Tổng thanh toán mới:{" "}
                      <strong>{formatVnd(addrPreview.new_total_payable_vnd)}</strong>
                    </p>
                  </div>

                  {addrPreviewCls === "prepaid_increase_blocked" ? (
                    <Alert variant="destructive" className="shadow-none">
                      <AlertDescription>
                        Phí giao hàng mới cao hơn đơn đã thanh toán trước. Hiện chưa hỗ trợ thu phụ
                        thu thêm.
                      </AlertDescription>
                    </Alert>
                  ) : (
                    <>
                      {addrPreviewCls === "cod_increase" && (
                        <label className="flex cursor-pointer items-start gap-2.5 text-sm">
                          <Checkbox
                            checked={acceptFeeIncrease}
                            onCheckedChange={(checked) => setAcceptFeeIncrease(checked === true)}
                            className="mt-0.5"
                          />
                          <span>
                            Tôi đồng ý trả thêm{" "}
                            <strong className="tabular-nums">
                              {formatVnd(addrPreview.fee_delta_vnd)}
                            </strong>{" "}
                            chênh lệch phí giao hàng khi nhận COD.
                          </span>
                        </label>
                      )}
                      <div className="flex flex-wrap gap-2">
                        <Button
                          type="button"
                          size="sm"
                          disabled={
                            submittingAddrUpdate ||
                            addressInProgressLock ||
                            (addrPreviewCls === "cod_increase" && !acceptFeeIncrease)
                          }
                          onClick={() => void handleConfirmAddressUpdate()}
                          className="min-h-10 shadow-none sm:min-h-8"
                        >
                          {submittingAddrUpdate
                            ? "Đang cập nhật..."
                            : addrPreviewCls === "cod_increase"
                              ? "Xác nhận trả thêm"
                              : "Xác nhận đổi địa chỉ"}
                        </Button>
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          className="min-h-10 sm:min-h-8"
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
                <Alert
                  variant={addrUpdateFeedback.ok ? "default" : "destructive"}
                  className="shadow-none"
                >
                  <AlertDescription>{addrUpdateFeedback.message}</AlertDescription>
                </Alert>
              )}
            </CollapsibleContent>
          </Collapsible>
        </Card>
      )}

      {/* Read-only Order Timeline (always visible per A2) */}
      {orderTimelineItems.length > 0 && (
        <Card className="border border-border shadow-none">
          <CardHeader className="border-b border-border pb-4">
            <CardTitle>Nhật ký hành trình</CardTitle>
            <CardAction>
              <span className="text-xs text-muted-foreground tabular-nums">
                {orderTimelineItems.length} mốc
              </span>
            </CardAction>
          </CardHeader>
          <CardContent>
            <Timeline items={orderTimelineItems} />
          </CardContent>
        </Card>
      )}

      {/* Cancel Order Collapsible */}
      {order.canCancel && (
        <Card className="border border-border py-0 shadow-none">
          <Collapsible open={cancelOpen} onOpenChange={setCancelOpen}>
            <CollapsibleTrigger asChild>
              <button
                type="button"
                className="flex min-h-12 w-full items-center justify-between gap-3 px-4 py-3 text-left text-sm font-semibold text-destructive transition-colors hover:bg-accent/50 md:px-5"
              >
                <span>Hủy đơn hàng</span>
                <ChevronDown
                  className={cn("size-4 shrink-0 transition-transform", cancelOpen && "rotate-180")}
                  aria-hidden="true"
                />
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent className="border-t border-border px-4 py-4 md:px-5">
              <form className="space-y-4" onSubmit={handleCancelSubmit}>
                <Field>
                  <FieldLabel htmlFor="cancel-reason">Lý do hủy</FieldLabel>
                  <Select
                    value={cancelReason}
                    onValueChange={setCancelReason}
                    disabled={cancelling}
                  >
                    <SelectTrigger id="cancel-reason" className="h-10 w-full bg-background">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {CANCEL_REASONS.map((reason) => (
                        <SelectItem key={reason} value={reason}>
                          {reason}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                {cancelError && <FieldError>{cancelError}</FieldError>}
                <Button
                  type="submit"
                  variant="destructive"
                  size="sm"
                  disabled={cancelling}
                  className="min-h-10 shadow-none sm:min-h-9"
                >
                  {cancelling ? "Đang hủy..." : "Xác nhận hủy đơn"}
                </Button>
              </form>
            </CollapsibleContent>
          </Collapsible>
        </Card>
      )}
    </>
  );

  const asideColumn = (
    <>
      <Card className="border border-border shadow-none">
        <CardHeader className="border-b border-border pb-4">
          <CardTitle>Địa chỉ nhận hàng</CardTitle>
          {order.shippingAddress && (
            <CardAction>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Sao chép địa chỉ nhận hàng"
                onClick={() => void copyValue(order.shippingAddress, "địa chỉ")}
              >
                <Copy className="size-3.5" aria-hidden="true" />
              </Button>
            </CardAction>
          )}
        </CardHeader>
        <CardContent className="space-y-2.5 text-sm">
          <div className="flex items-center justify-between gap-2">
            <span className="font-semibold text-foreground">{order.buyerName || "Người nhận"}</span>
            {order.buyerPhone && (
              <div className="flex items-center gap-1">
                <span className="text-xs text-muted-foreground tabular-nums">
                  {order.buyerPhone}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label="Sao chép số điện thoại"
                  onClick={() => void copyValue(order.buyerPhone, "số điện thoại")}
                >
                  <Copy className="size-3" aria-hidden="true" />
                </Button>
              </div>
            )}
          </div>
          <p className="text-sm leading-5 text-muted-foreground">
            {order.shippingAddress || "Đang cập nhật địa chỉ"}
          </p>
          {order.trackingCode && (
            <div className="flex items-center justify-between gap-2 border-t border-border pt-2.5 text-xs text-muted-foreground">
              <span className="truncate tabular-nums">
                {order.carrier ? `${order.carrier} · ` : ""}
                Vận đơn: <strong className="text-foreground">{order.trackingCode}</strong>
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label="Sao chép mã vận đơn"
                onClick={() => void copyValue(order.trackingCode!, "mã vận đơn")}
              >
                <Copy className="size-3" aria-hidden="true" />
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      <Card className="border border-border shadow-none">
        <CardHeader className="border-b border-border pb-4">
          <CardTitle>Thanh toán</CardTitle>
          {payMeta && (
            <CardAction>
              <StatusBadge
                tone={paymentStatusTone(
                  order.paymentStatusRaw,
                  order.paymentMethodRaw,
                  order.backendStatus
                )}
              >
                {payMeta.label}
              </StatusBadge>
            </CardAction>
          )}
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p className="font-medium text-foreground">
            {getPaymentMethodLabel(order.paymentMethodRaw)}
          </p>

          <dl className="space-y-2 text-[13px] tabular-nums">
            <div className="flex items-center justify-between gap-2">
              <dt className="text-muted-foreground">Tạm tính</dt>
              <dd className="text-foreground">{formatVnd(order.subtotal)}</dd>
            </div>
            {order.shopProgramDiscount > 0 && (
              <div className="flex items-center justify-between gap-2">
                <dt className="text-muted-foreground">Giảm giá chương trình gian hàng</dt>
                <dd className="text-badge-success-ink">
                  {formatSignedVnd(-order.shopProgramDiscount)}
                </dd>
              </div>
            )}
            <div className="flex items-center justify-between gap-2">
              <dt className="text-muted-foreground">Mã giảm giá</dt>
              <dd
                className={
                  order.shopVoucherDiscount > 0 ? "text-badge-success-ink" : "text-muted-foreground"
                }
              >
                {order.shopVoucherDiscount > 0 ? formatSignedVnd(-order.shopVoucherDiscount) : "0₫"}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-2">
              <dt className="text-muted-foreground">Phí vận chuyển</dt>
              <dd className="text-foreground">{formatVnd(order.shippingFee)}</dd>
            </div>
            {order.shopFreeshipDiscount > 0 && (
              <div className="flex items-center justify-between gap-2">
                <dt className="text-muted-foreground">Gian hàng hỗ trợ phí ship</dt>
                <dd className="text-badge-success-ink">
                  {formatSignedVnd(-order.shopFreeshipDiscount)}
                </dd>
              </div>
            )}
          </dl>

          <Separator />

          <div className="flex items-baseline justify-between gap-2">
            <span className="font-semibold text-foreground">Tổng thanh toán</span>
            <Money vnd={order.total} size="lg" />
          </div>
        </CardContent>
      </Card>
    </>
  );

  return (
    <div className={cn(SITE_PAGE_CONTAINER, "space-y-6")}>
      {renderBreadcrumb()}

      <header className="mx-auto flex w-full max-w-[1000px] flex-col gap-4 border-b border-border pb-4 md:flex-row md:items-start md:justify-between">
        <div className="min-w-0 space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground tabular-nums sm:text-2xl sm:leading-8">
              Chi tiết đơn {order.orderCode}
            </h1>
            <StatusBadge tone={orderStatusTone(order.backendStatus)}>
              {statusMeta.label}
            </StatusBadge>
            {payMeta && (
              <StatusBadge
                tone={paymentStatusTone(
                  order.paymentStatusRaw,
                  order.paymentMethodRaw,
                  order.backendStatus
                )}
              >
                {payMeta.label}
              </StatusBadge>
            )}
          </div>
          <p className="text-[13px] leading-[18px] text-muted-foreground tabular-nums">
            {brandName}
            {order.createdAt ? ` · ${formatDateTimeVi(order.createdAt)}` : ""}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {showOnlinePaymentBox && order.paymentId && (
            <Button asChild size="sm" className="min-h-10 shadow-none sm:min-h-9">
              <Link
                href={`/thanh-toan/cho/${encodeURIComponent(order.paymentId)}?order=${encodeURIComponent(order.id)}`}
              >
                Tiếp tục thanh toán
              </Link>
            </Button>
          )}
          {order.canConfirmReceived && (
            <Button
              type="button"
              size="sm"
              disabled={confirming}
              onClick={() => void handleConfirmReceived()}
              className="min-h-10 shadow-none sm:min-h-9"
            >
              {confirming ? "Đang xác nhận..." : "Đã nhận được hàng"}
            </Button>
          )}
          {order.canRequestReturn && !returnRequest && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="min-h-10 shadow-none sm:min-h-9"
              onClick={() => {
                setReturnError(null);
                setEvidenceInputError(null);
                setReturnWizardStep(1);
                setReturnSheetOpen(true);
              }}
            >
              Yêu cầu đổi trả
            </Button>
          )}
          <Button
            type="button"
            variant={hasPrimaryHeaderAction ? "secondary" : "default"}
            size="sm"
            disabled={reordering}
            onClick={() => void handleReorder()}
            className="min-h-10 shadow-none sm:min-h-9"
          >
            {reordering ? "Đang thêm..." : "Mua lại"}
          </Button>
        </div>
      </header>

      <DetailLayout main={mainColumn} aside={asideColumn} />

      {suggestedProducts.length > 0 && (
        <div className="mx-auto max-w-[1000px]">
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={suggestedProducts} />
        </div>
      )}

      {/* Guided Return Request Sheet (step indicator + reason + evidence + confirm) */}
      {order.canRequestReturn && (
        <Sheet open={returnSheetOpen} onOpenChange={setReturnSheetOpen}>
          <SheetContent side="right" className="w-full gap-0 p-0 sm:max-w-lg">
            <SheetHeader className="border-b border-border px-5 py-4">
              <SheetTitle>Yêu cầu đổi trả</SheetTitle>
              <SheetDescription className="tabular-nums">
                Bước {returnWizardStep} / {RETURN_WIZARD_STEPS.length} · Đơn {order.orderCode}
              </SheetDescription>
              <div className="space-y-2 pt-2">
                <Progress
                  value={Math.round((returnWizardStep / RETURN_WIZARD_STEPS.length) * 100)}
                  aria-label={`Bước ${returnWizardStep} trên ${RETURN_WIZARD_STEPS.length}`}
                />
                <ol className="grid grid-cols-3 gap-1.5">
                  {RETURN_WIZARD_STEPS.map((st) => {
                    const active = st.id === returnWizardStep;
                    const done = st.id < returnWizardStep;
                    return (
                      <li key={st.id}>
                        <button
                          type="button"
                          disabled={st.id > returnWizardStep}
                          onClick={() => {
                            if (st.id < returnWizardStep) setReturnWizardStep(st.id);
                          }}
                          className={cn(
                            "flex w-full items-center gap-1.5 rounded-md border px-2 py-1.5 text-left text-xs transition-colors disabled:cursor-default",
                            active
                              ? "border-brand bg-background font-semibold text-foreground"
                              : done
                                ? "border-brand/40 bg-background text-foreground hover:bg-accent"
                                : "border-border text-muted-foreground opacity-70"
                          )}
                        >
                          <span
                            className={cn(
                              "flex size-4 shrink-0 items-center justify-center rounded-full text-[10px]",
                              done || active
                                ? "bg-brand text-brand-foreground"
                                : "border border-border"
                            )}
                          >
                            {done ? <Check className="size-2.5" /> : st.id}
                          </span>
                          <span className="truncate">{st.label}</span>
                        </button>
                      </li>
                    );
                  })}
                </ol>
              </div>
            </SheetHeader>

            <form className="flex min-h-0 flex-1 flex-col" onSubmit={handleReturnSubmit} noValidate>
              <div className="flex-1 space-y-4 overflow-y-auto px-5 py-5">
                {returnWizardStep === 1 && (
                  <>
                    <Field>
                      <FieldLabel htmlFor="return-reason">Lý do đổi trả</FieldLabel>
                      <Select
                        value={returnReason}
                        onValueChange={setReturnReason}
                        disabled={submittingReturn}
                      >
                        <SelectTrigger id="return-reason" className="h-10 w-full bg-background">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {RETURN_REASONS.map((reason) => (
                            <SelectItem key={reason} value={reason}>
                              {reason}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>

                    <Field data-invalid={Boolean(returnError) || undefined}>
                      <FieldLabel htmlFor="return-note">
                        Mô tả chi tiết tình trạng sản phẩm
                      </FieldLabel>
                      <Textarea
                        id="return-note"
                        rows={4}
                        aria-invalid={Boolean(returnError) || undefined}
                        aria-describedby={returnError ? "return-form-error" : undefined}
                        value={returnDetail}
                        onChange={(e) => {
                          setReturnDetail(e.target.value);
                          if (returnError) setReturnError(null);
                        }}
                        placeholder="Áo còn nguyên tem mác, cần đổi trả do..."
                        disabled={submittingReturn}
                        className="bg-background"
                      />
                      <FieldDescription>
                        Nêu rõ lỗi hoặc yêu cầu đổi size (tối thiểu 5 ký tự) để {brandName} phản hồi
                        nhanh hơn.
                      </FieldDescription>
                    </Field>
                  </>
                )}

                {returnWizardStep === 2 && (
                  <Field>
                    <FieldLabel htmlFor="return-evidence-url">
                      Liên kết bằng chứng (tối đa {MAX_EVIDENCE_URLS})
                    </FieldLabel>
                    <FieldDescription>
                      Hiện chưa hỗ trợ tải ảnh trực tiếp; vui lòng dán đường dẫn https:// của ảnh
                      hoặc video minh chứng.
                    </FieldDescription>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Input
                        id="return-evidence-url"
                        type="url"
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
                        disabled={
                          submittingReturn || returnEvidenceUrls.length >= MAX_EVIDENCE_URLS
                        }
                        className="h-10 bg-background"
                      />
                      <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        className="min-h-10 shrink-0"
                        disabled={
                          submittingReturn || returnEvidenceUrls.length >= MAX_EVIDENCE_URLS
                        }
                        onClick={handleAddEvidenceUrl}
                      >
                        Thêm
                      </Button>
                    </div>
                    {evidenceInputError && (
                      <FieldError id="return-evidence-error">{evidenceInputError}</FieldError>
                    )}

                    {returnEvidenceUrls.length > 0 && (
                      <div className="space-y-2 pt-2">
                        {returnEvidenceUrls.map((url, idx) => (
                          <div
                            key={url}
                            className="flex items-center justify-between gap-2 rounded-md border border-border bg-background px-3 py-2 text-xs"
                          >
                            <span className="min-w-0 truncate text-foreground">{url}</span>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              aria-label={`Xóa liên kết ${idx + 1}`}
                              disabled={submittingReturn}
                              onClick={() => {
                                setReturnEvidenceUrls((prev) => prev.filter((u) => u !== url));
                                setEvidenceInputError(null);
                              }}
                              className="h-8 shrink-0 text-destructive hover:text-destructive"
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                              <span>Xóa</span>
                            </Button>
                          </div>
                        ))}
                      </div>
                    )}
                  </Field>
                )}

                {returnWizardStep === 3 && (
                  <div className="space-y-4">
                    <div className="space-y-2.5 rounded-lg border border-border bg-background p-4 text-sm">
                      <div>
                        <span className="text-xs font-medium text-muted-foreground">
                          Lý do đổi trả
                        </span>
                        <p className="font-medium text-foreground">{returnReason}</p>
                      </div>
                      <Separator />
                      <div>
                        <span className="text-xs font-medium text-muted-foreground">
                          Mô tả tình trạng
                        </span>
                        <p className="text-foreground">{returnDetail.trim()}</p>
                      </div>
                      <Separator />
                      <div>
                        <span className="text-xs font-medium text-muted-foreground">
                          Liên kết bằng chứng ({returnEvidenceUrls.length}/{MAX_EVIDENCE_URLS})
                        </span>
                        {returnEvidenceUrls.length === 0 ? (
                          <p className="text-xs text-muted-foreground">
                            Chưa đính kèm liên kết bằng chứng.
                          </p>
                        ) : (
                          <ul className="mt-1 space-y-1 text-xs text-muted-foreground">
                            {returnEvidenceUrls.map((u) => (
                              <li key={u} className="truncate">
                                {u}
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                    </div>

                    <div className="rounded-lg border border-border bg-muted/40 p-3.5 text-xs leading-5 text-muted-foreground">
                      <p className="font-medium text-foreground">Quy trình xử lý sau khi gửi:</p>
                      <p className="mt-1">
                        {brandName} có 3 ngày phản hồi. Sau khi được duyệt, bạn bàn giao hàng hoàn
                        theo vận đơn trong 5 ngày và nhận hoàn tiền sau khi gian hàng kiểm tra xong.
                      </p>
                    </div>
                  </div>
                )}

                {returnError && <FieldError id="return-form-error">{returnError}</FieldError>}
              </div>

              <SheetFooter className="flex-row items-center justify-between gap-2 border-t border-border px-5 py-4">
                {returnWizardStep > 1 ? (
                  <Button
                    type="button"
                    variant="outline"
                    disabled={submittingReturn}
                    onClick={() => setReturnWizardStep((prev) => (prev === 3 ? 2 : 1))}
                    className="min-h-10 shadow-none"
                  >
                    Quay lại
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    disabled={submittingReturn}
                    onClick={() => setReturnSheetOpen(false)}
                    className="min-h-10 shadow-none"
                  >
                    Đóng
                  </Button>
                )}

                <div className="flex items-center gap-2">
                  {returnWizardStep === 3 && (
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={submittingReturn}
                      onClick={() => setReturnSheetOpen(false)}
                      className="min-h-10"
                    >
                      Huỷ
                    </Button>
                  )}
                  {returnWizardStep < 3 ? (
                    <Button
                      type="button"
                      onClick={handleAdvanceReturnStep}
                      className="min-h-10 shadow-none"
                    >
                      Tiếp theo
                    </Button>
                  ) : (
                    <Button
                      type="submit"
                      disabled={submittingReturn}
                      className="min-h-10 shadow-none"
                    >
                      {submittingReturn ? "Đang gửi..." : "Gửi yêu cầu đổi trả"}
                    </Button>
                  )}
                </div>
              </SheetFooter>
            </form>
          </SheetContent>
        </Sheet>
      )}
    </div>
  );
}

export function OrderDetailClientView(props: OrderDetailClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={cn(SITE_PAGE_CONTAINER, "space-y-4")}>
          <DetailLoadingSkeleton />
        </div>
      }
    >
      <OrderDetailInner {...props} />
    </Suspense>
  );
}
