"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  Check,
  Copy,
  ExternalLink,
  Package,
  Printer,
  RefreshCw,
  Truck,
} from "lucide-react";
import {
  DetailLayout,
  EmptyState,
  ErrorState,
  Money,
  PageHeader,
  StatusBadge,
  Timeline,
  type PageHeaderMoreAction,
  type TimelineItem,
} from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import { api, getErrorMessage } from "@/lib/api";
import { formatCarrierLabel, formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta, normalizeSalesChannel } from "@/lib/salesChannel";
import {
  buildShippingLabelHref,
  getReturnStatusInfo,
  getShipmentStatusInfo,
  getTimelineEventTitle,
  isAddressUpdatedTimelineNote,
  localizeTimelineNote,
} from "@/features/account/orderMeta";
import {
  resolvePaymentStatusTone,
  resolveSellerOrderTone,
  resolveSellerReturnTone,
} from "./ops/sellerTone";
import {
  buildCarrierSelectOptions,
  buildSellerOrderBreakdownLines,
  canSellerCancelOrder,
  canSellerConfirmOrder,
  canSellerShipOrder,
  resolveOrderShippingDefaults,
  translateSellerShipError,
} from "./types";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getSellerOrderStatusMeta,
  normalizeSellerShipmentResponse,
  resolveSellerShipmentFeeVnd,
  type GwOrderShipment,
  type GwSellerOrder,
  type GwShipmentEvent,
} from "./sellerApiTypes";
import { useSellerContext } from "./SellerShell";

const EMPTY_DASH = "—";

export interface SellerOrderDetailClientProps {
  initialOrder: GwSellerOrder | null;
  initialShipment: GwOrderShipment | null;
  initialShipmentEvents?: GwShipmentEvent[];
  loadError: string | null;
}

export function SellerOrderDetailClient({
  initialOrder,
  initialShipment,
  initialShipmentEvents = [],
  loadError,
}: SellerOrderDetailClientProps) {
  const router = useRouter();
  const { showToast } = useSellerContext();

  const [order, setOrder] = useState<GwSellerOrder | null>(initialOrder);
  const [shipment, setShipment] = useState<GwOrderShipment | null>(initialShipment);
  const [shipmentEvents, setShipmentEvents] = useState<GwShipmentEvent[]>(() =>
    initialShipmentEvents.length > 0 ? initialShipmentEvents : (initialShipment?.events ?? [])
  );
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  const initialDefaults = useMemo(() => resolveOrderShippingDefaults(initialOrder), [initialOrder]);
  const [carrierCode, setCarrierCode] = useState(initialDefaults.carrierCode);
  const [serviceCode, setServiceCode] = useState(initialDefaults.serviceCode);

  const [cancelOpen, setCancelOpen] = useState(false);
  const [cancelReason, setCancelReason] = useState("");
  const [cancelError, setCancelError] = useState<string | null>(null);

  const pollCountRef = useRef(0);
  const [pollTick, setPollTick] = useState(0);

  const reloadDetail = useCallback(
    async (opts?: { silent?: boolean }) => {
      if (!order?.id) return;
      if (!opts?.silent) setRefreshing(true);
      try {
        const [ordRes, shipRes] = await Promise.all([
          api<{ order: GwSellerOrder }>(`/api/seller/orders/${encodeURIComponent(order.id)}`),
          api<unknown>(`/api/seller/orders/${encodeURIComponent(order.id)}/shipment`).catch(
            () => null
          ),
        ]);
        if (ordRes.order) setOrder(ordRes.order);
        if (shipRes !== null) {
          const parsedShip = normalizeSellerShipmentResponse(shipRes);
          setShipment(parsedShip.shipment);
          setShipmentEvents(parsedShip.events);
        }
      } catch (err) {
        if (!opts?.silent) {
          showToast(getErrorMessage(err, "Không thể tải lại chi tiết đơn hàng."));
        }
      } finally {
        if (!opts?.silent) setRefreshing(false);
      }
    },
    [order?.id, showToast]
  );

  // Silent poll while shipment job is QUEUED
  const isShipmentQueued =
    Boolean(order) &&
    !order?.tracking_code &&
    (order?.shipment_job_status ?? "").trim().toUpperCase() === "QUEUED";

  useEffect(() => {
    if (!isShipmentQueued) {
      pollCountRef.current = 0;
      return;
    }
    if (pollCountRef.current >= 20) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      pollCountRef.current += 1;
      void reloadDetail({ silent: true }).finally(() => {
        if (!cancelled) {
          setPollTick((v) => v + 1);
        }
      });
    }, 3000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [isShipmentQueued, reloadDetail, pollTick]);

  const carrierOptions = useMemo(() => buildCarrierSelectOptions(order), [order]);
  const activeCarrierOption = useMemo(
    () => carrierOptions.find((c) => c.carrierCode === carrierCode) ?? carrierOptions[0],
    [carrierOptions, carrierCode]
  );

  const copyValue = async (text: string, label: string) => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      showToast(`Đã sao chép ${label}.`);
    } catch {
      showToast(`Không thể sao chép ${label}.`);
    }
  };

  if (loadError) {
    return (
      <div className="mx-auto max-w-[1000px] space-y-4">
        <PageHeader title="Chi tiết đơn hàng" backHref="/seller/don-hang" />
        <ErrorState
          title="Không thể tải chi tiết đơn hàng"
          description={loadError}
          onRetry={() => router.refresh()}
        />
      </div>
    );
  }

  if (!order) {
    return (
      <div className="mx-auto max-w-[1000px] space-y-4">
        <PageHeader title="Chi tiết đơn hàng" backHref="/seller/don-hang" />
        <EmptyState
          icon={Package}
          title="Không tìm thấy đơn hàng"
          description="Đơn hàng không tồn tại hoặc không thuộc gian hàng của bạn."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/don-hang">Về danh sách đơn hàng</Link>
            </Button>
          }
        />
      </div>
    );
  }

  const statusMeta = getSellerOrderStatusMeta(
    order.status,
    order.shipment_job_status,
    order.tracking_code
  );
  const orderTone = resolveSellerOrderTone(
    order.status,
    order.shipment_job_status,
    order.tracking_code
  );
  const jobStatus = (order.shipment_job_status ?? "").trim().toUpperCase();
  const isShipmentFailed = !order.tracking_code && jobStatus === "FAILED";
  const canConfirm = canSellerConfirmOrder(order.status, order.shipment_job_status);
  const canShip = canSellerShipOrder(order.status, order.tracking_code);
  const canCancel = canSellerCancelOrder(order.status);

  const netReceivable = computeSellerNetReceivable(order);
  const breakdownLines = buildSellerOrderBreakdownLines(order);
  const totalQty = (order.items ?? []).reduce((s, it) => s + Math.max(0, it.quantity), 0);
  const labelHref = buildShippingLabelHref(order.tracking_code);
  const channel = normalizeSalesChannel(order.sales_channel);
  const channelBadge = getSalesChannelMeta(channel);
  const localizedCancelReason = localizeTimelineNote(order.cancel_reason ?? "");
  const hasReturnLink =
    Boolean(order.return_request_id) ||
    order.status === "RETURN_REQUESTED" ||
    order.status === "RETURNED" ||
    order.status === "REFUNDED";

  const handleConfirm = async () => {
    if (!canConfirm || busy) return;
    setBusy(true);
    try {
      const res = await api<{ order: GwSellerOrder }>(
        `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`,
        { method: "POST" }
      );
      if (res.order) setOrder(res.order);
      router.refresh();
      showToast(`Đã xác nhận đơn ${order.order_code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const handleShip = async () => {
    if (!canShip || busy) return;
    setBusy(true);
    try {
      const res = await api<{
        order: GwSellerOrder;
        tracking_code?: string;
        queued?: boolean;
      }>(`/api/seller/orders/${encodeURIComponent(order.id)}/ship`, {
        method: "POST",
        body: {
          carrier_code: carrierCode,
          service_code: serviceCode,
        },
      });
      if (res.order) setOrder(res.order);
      await reloadDetail({ silent: true });
      router.refresh();
      if (res.queued) {
        showToast(
          "Hãng vận chuyển đang tạm gián đoạn. Đơn chuyển sang Đang chờ tạo vận đơn — bạn có thể thử tạo lại ngay."
        );
      } else {
        showToast(
          res.tracking_code
            ? `Đã tạo vận đơn ${res.tracking_code}.`
            : `Đã tạo vận đơn cho đơn ${order.order_code}.`
        );
      }
    } catch (err) {
      showToast(translateSellerShipError(err));
    } finally {
      setBusy(false);
    }
  };

  const handleCancelOrder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canCancel || busy) return;
    const reason = cancelReason.trim();
    if (reason.length < 3) {
      setCancelError("Vui lòng nhập lý do hủy đơn (tối thiểu 3 ký tự).");
      return;
    }
    setCancelError(null);
    setBusy(true);
    try {
      const res = await api<{ order: GwSellerOrder }>(
        `/api/seller/orders/${encodeURIComponent(order.id)}/cancel`,
        {
          method: "POST",
          body: { reason },
        }
      );
      if (res.order) setOrder(res.order);
      setCancelOpen(false);
      setCancelReason("");
      router.refresh();
      showToast(`Đã hủy đơn ${order.order_code}.`);
    } catch (err) {
      setCancelError(getErrorMessage(err, "Không thể hủy đơn hàng lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const shipmentStatusInfo = shipment?.status ? getShipmentStatusInfo(shipment.status) : null;
  const shipmentFeeVnd = resolveSellerShipmentFeeVnd(shipment);
  const paymentStatusLabel =
    order.payment_status || (order.payment_method ?? "").toUpperCase() === "COD"
      ? formatPaymentStatusVi(order.payment_status ?? "", order.payment_method, order.status)
      : "";
  const paymentTone = resolvePaymentStatusTone(
    order.payment_status ?? "",
    order.payment_method,
    order.status
  );

  const fullAddress = order.shipping_address
    ? [
        order.shipping_address.street_address,
        order.shipping_address.ward_name,
        order.shipping_address.province_name,
      ]
        .filter(Boolean)
        .join(", ")
    : "";

  const moreActions: PageHeaderMoreAction[] = [
    {
      label: "Sao chép mã đơn",
      onSelect: () => void copyValue(order.order_code, `mã đơn ${order.order_code}`),
    },
  ];
  if (canCancel) {
    moreActions.push({
      label: cancelOpen ? "Đóng hủy đơn" : "Hủy đơn",
      destructive: true,
      onSelect: () => {
        setCancelOpen((prev) => !prev);
        setCancelError(null);
      },
    });
  }

  const orderTimelineItems: TimelineItem[] = (order.timeline ?? []).map((ev) => {
    const title = isAddressUpdatedTimelineNote(ev.note)
      ? "Cập nhật địa chỉ giao hàng"
      : getTimelineEventTitle(ev, order.payment_method);
    const noteText = localizeTimelineNote(ev.note);
    return {
      id: ev.id,
      at: ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH,
      title,
      description: noteText || undefined,
    };
  });

  const shipmentTimelineItems: TimelineItem[] = shipmentEvents.map((ev) => {
    const stInfo = getShipmentStatusInfo(ev.status);
    const descParts = [ev.description, ev.location].filter(Boolean);
    return {
      id: ev.id,
      at: ev.occurred_at ? formatDateTimeVi(ev.occurred_at) : EMPTY_DASH,
      title: stInfo.label,
      description: descParts.length > 0 ? descParts.join(" · ") : undefined,
    };
  });

  return (
    <div className="mx-auto max-w-[1000px] space-y-4">
      <PageHeader
        title={order.order_code}
        backHref="/seller/don-hang"
        badges={
          <>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              onClick={() => void copyValue(order.order_code, `mã đơn ${order.order_code}`)}
              aria-label="Sao chép mã đơn hàng"
              title="Sao chép mã đơn"
            >
              <Copy className="size-3.5" aria-hidden="true" />
            </Button>
            {paymentStatusLabel ? (
              <StatusBadge tone={paymentTone}>{paymentStatusLabel}</StatusBadge>
            ) : null}
            <StatusBadge tone={orderTone}>{statusMeta.label}</StatusBadge>
            <Badge variant={channel === "STOREFRONT" ? "info" : "secondary"} className="rounded-md">
              {channelBadge.label}
            </Badge>
          </>
        }
        subtitle={`Đặt lúc ${order.created_at ? formatDateTimeVi(order.created_at) : EMPTY_DASH} · ${formatPaymentMethodVi(order.payment_method)}`}
        secondaryActions={
          <>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              disabled={refreshing}
              onClick={() => void reloadDetail()}
            >
              <RefreshCw
                className={refreshing ? "size-3.5 animate-spin" : "size-3.5"}
                aria-hidden="true"
              />
              <span>Làm mới</span>
            </Button>
            {labelHref ? (
              <Button variant="secondary" size="sm" asChild>
                <a
                  href={labelHref}
                  target="_blank"
                  rel="noopener noreferrer"
                  title="Mở nhãn vận đơn ở tab mới — nhấn Ctrl+P để in"
                >
                  <Printer className="size-3.5" aria-hidden="true" />
                  <span>In nhãn (Ctrl+P)</span>
                </a>
              </Button>
            ) : null}
          </>
        }
        moreActions={moreActions}
        primaryAction={
          canConfirm ? (
            <Button type="button" size="sm" disabled={busy} onClick={() => void handleConfirm()}>
              <Check className="size-4" aria-hidden="true" />
              <span>{busy ? "Đang xác nhận..." : "Xác nhận đơn"}</span>
            </Button>
          ) : canShip ? (
            <Button type="button" size="sm" disabled={busy} onClick={() => void handleShip()}>
              <Truck className="size-4" aria-hidden="true" />
              <span>
                {busy
                  ? "Đang tạo vận đơn..."
                  : isShipmentQueued || isShipmentFailed
                    ? "Thử tạo lại ngay"
                    : "Tạo vận đơn"}
              </span>
            </Button>
          ) : null
        }
      />

      {/* Async Shipment Job Banner (QUEUED / FAILED) */}
      {isShipmentQueued || isShipmentFailed ? (
        <Alert
          variant={isShipmentFailed ? "destructive" : "default"}
          role="status"
          aria-label="Trạng thái tạo vận đơn"
        >
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertTitle>
            {isShipmentQueued
              ? "Đơn đang chờ tạo vận đơn với hãng vận chuyển"
              : "Tạo vận đơn với hãng thất bại"}
          </AlertTitle>
          <AlertDescription>
            {isShipmentQueued
              ? "Hệ thống đang tự động thử lại theo chu kỳ. Bạn cũng có thể bấm nút thử lại ở đầu trang."
              : "Vui lòng kiểm tra địa chỉ kho lấy hàng và thử tạo lại vận đơn."}
          </AlertDescription>
        </Alert>
      ) : null}

      {/* Cancel Order Reason Form */}
      {cancelOpen && canCancel ? (
        <Card>
          <CardHeader>
            <CardTitle>Hủy đơn hàng {order.order_code}</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={(e) => void handleCancelOrder(e)} className="space-y-4">
              <Field>
                <FieldLabel htmlFor="seller-cancel-reason">
                  Lý do hủy đơn (gửi cho người mua)
                </FieldLabel>
                <Textarea
                  id="seller-cancel-reason"
                  rows={3}
                  value={cancelReason}
                  onChange={(e) => setCancelReason(e.target.value)}
                  placeholder="Ví dụ: Hết hàng đột xuất hoặc không thể đáp ứng thời gian giao..."
                  disabled={busy}
                />
                {cancelError ? <FieldError role="alert">{cancelError}</FieldError> : null}
              </Field>
              <div className="flex flex-wrap justify-end gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setCancelOpen(false)}
                >
                  Đóng
                </Button>
                <Button type="submit" variant="destructive" size="sm" disabled={busy}>
                  {busy ? "Đang hủy..." : "Xác nhận hủy đơn"}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      ) : null}

      {/* Return link banner if order has a return request */}
      {hasReturnLink ? (
        <Card className="py-0" aria-label="Thông tin yêu cầu trả hàng">
          <CardContent className="flex flex-col gap-3 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-sm font-semibold text-foreground">
                  Yêu cầu trả hàng / hoàn tiền
                </h2>
                {order.return_status ? (
                  <StatusBadge tone={resolveSellerReturnTone(order.return_status)}>
                    {getReturnStatusInfo(order.return_status).label}
                  </StatusBadge>
                ) : null}
              </div>
              <p className="text-[13px] text-muted-foreground">
                {order.return_status
                  ? `Trạng thái: ${getReturnStatusInfo(order.return_status).label}`
                  : "Đơn hàng có hồ sơ trả hàng đang được xử lý tại mục Trả hàng."}
              </p>
            </div>
            <Button size="sm" asChild className="shrink-0">
              <Link
                href={
                  order.return_request_id
                    ? `/seller/tra-hang?selected=${encodeURIComponent(order.return_request_id)}`
                    : "/seller/tra-hang"
                }
              >
                <span>Xử lý trả hàng</span>
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </Link>
            </Button>
          </CardContent>
        </Card>
      ) : null}

      {/* A2 DetailLayout: 2/3 main + 1/3 aside (Figma 03) */}
      <DetailLayout
        main={
          <>
            {/* 1. Fulfillment / Items Card */}
            <Card aria-label="Sản phẩm trong đơn">
              <CardHeader className="flex-row flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge tone={orderTone}>
                    {statusMeta.label} ({formatInt(totalQty)})
                  </StatusBadge>
                  <CardTitle className="sr-only">Sản phẩm trong đơn</CardTitle>
                </div>
                {order.carrier_code ? (
                  <span className="text-xs text-muted-foreground">
                    {formatCarrierLabel(order.carrier_code)}
                    {order.tracking_code ? ` · ${order.tracking_code}` : ""}
                  </span>
                ) : null}
              </CardHeader>

              <CardContent className="space-y-3">
                {(order.items ?? []).map((item) => (
                  <div
                    key={item.id}
                    className="flex flex-col gap-2 rounded-lg border border-border p-3 sm:flex-row sm:items-start sm:justify-between"
                  >
                    <div className="min-w-0 flex-1 space-y-1">
                      <Link
                        href={`/seller/san-pham/${item.product_id}`}
                        className="block font-medium text-foreground hover:text-brand hover:underline"
                      >
                        {item.product_name}
                      </Link>
                      {item.sku_name ? (
                        <Badge variant="secondary" className="rounded-md">
                          {item.sku_name}
                        </Badge>
                      ) : null}
                    </div>
                    <div className="flex shrink-0 items-baseline justify-between gap-4 text-sm tabular-nums sm:flex-col sm:items-end sm:gap-0.5">
                      <span className="text-xs text-muted-foreground">
                        {formatVnd(item.unit_price_vnd)} × {formatInt(item.quantity)}
                      </span>
                      <Money vnd={item.line_total_vnd} />
                    </div>
                  </div>
                ))}
              </CardContent>

              {canConfirm || canShip ? (
                <CardFooter className="flex-col items-stretch gap-3 border-t border-border pt-4">
                  {canShip ? (
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <Field>
                        <FieldLabel htmlFor="detail-carrier">Đơn vị vận chuyển</FieldLabel>
                        <Select
                          value={carrierCode}
                          onValueChange={(nextCarrier) => {
                            setCarrierCode(nextCarrier);
                            const opt = carrierOptions.find((c) => c.carrierCode === nextCarrier);
                            if (opt?.services[0]) setServiceCode(opt.services[0].serviceCode);
                          }}
                        >
                          <SelectTrigger id="detail-carrier" className="w-full bg-card">
                            <SelectValue>{activeCarrierOption.carrierLabel}</SelectValue>
                          </SelectTrigger>
                          <SelectContent>
                            {carrierOptions.map((c) => (
                              <SelectItem key={c.carrierCode} value={c.carrierCode}>
                                {c.carrierLabel}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="detail-service">Gói dịch vụ</FieldLabel>
                        <Select value={serviceCode} onValueChange={setServiceCode}>
                          <SelectTrigger id="detail-service" className="w-full bg-card">
                            <SelectValue>
                              {activeCarrierOption.services.find(
                                (s) => s.serviceCode === serviceCode
                              )?.serviceLabel ?? serviceCode}
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent>
                            {activeCarrierOption.services.map((s) => (
                              <SelectItem key={s.serviceCode} value={s.serviceCode}>
                                {s.serviceLabel}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                    </div>
                  ) : null}

                  <div className="flex flex-wrap items-center justify-end gap-2">
                    {canCancel ? (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => {
                          setCancelOpen((prev) => !prev);
                          setCancelError(null);
                        }}
                      >
                        {cancelOpen ? "Đóng hủy đơn" : "Hủy đơn"}
                      </Button>
                    ) : null}
                    {canConfirm ? (
                      <Button
                        type="button"
                        variant={canShip ? "outline" : "default"}
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleConfirm()}
                      >
                        {busy ? "Đang xác nhận..." : "Xác nhận đơn"}
                      </Button>
                    ) : null}
                    {canShip && !isShipmentQueued ? (
                      <Button
                        type="button"
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleShip()}
                      >
                        {busy ? "Đang tạo vận đơn..." : "Tạo vận đơn"}
                      </Button>
                    ) : null}
                  </div>
                </CardFooter>
              ) : null}
            </Card>

            {/* 2. Payment & Settlement Summary Card */}
            <Card aria-label="Thanh toán và đối soát">
              <CardHeader className="flex-row flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge tone={paymentTone}>
                    {paymentStatusLabel || formatPaymentMethodVi(order.payment_method)}
                  </StatusBadge>
                  <CardTitle className="text-sm font-semibold">Thanh toán &amp; đối soát</CardTitle>
                </div>
                <span className="text-xs text-muted-foreground">
                  {formatPaymentMethodVi(order.payment_method)}
                </span>
              </CardHeader>
              <CardContent className="space-y-2.5 text-sm tabular-nums">
                {breakdownLines.map((line) => (
                  <div key={line.key} className="flex items-baseline justify-between gap-3">
                    <span className="text-muted-foreground">{line.label}</span>
                    <span
                      className={
                        line.isDeduction && line.amountVnd < 0
                          ? "text-muted-foreground"
                          : "text-foreground"
                      }
                    >
                      {line.amountVnd < 0
                        ? `−${formatVnd(Math.abs(line.amountVnd))}`
                        : formatVnd(line.amountVnd)}
                    </span>
                  </div>
                ))}

                <Separator />

                <div className="flex items-baseline justify-between gap-3 font-semibold text-foreground">
                  <span>Thực nhận dự kiến</span>
                  <Money vnd={netReceivable} />
                </div>

                <Separator />

                <div className="flex items-baseline justify-between gap-3 text-[13px] text-muted-foreground">
                  <span>Phí ship khách trả</span>
                  <span>{formatVnd(order.shipping_fee_vnd)}</span>
                </div>
                <div className="flex items-baseline justify-between gap-3 text-[13px] text-muted-foreground">
                  <span>Khách thanh toán ({formatInt(totalQty)} sản phẩm)</span>
                  <span className="font-medium text-foreground">
                    {formatVnd(order.total_payable_vnd)}
                  </span>
                </div>
                {shipment && shipment.cod_amount_vnd > 0 ? (
                  <div className="flex items-baseline justify-between gap-3 text-[13px] text-muted-foreground">
                    <span>Thu hộ COD</span>
                    <span className="font-medium text-foreground">
                      {formatVnd(shipment.cod_amount_vnd)}
                    </span>
                  </div>
                ) : null}
              </CardContent>
            </Card>

            {/* 3. Carrier Tracking Events (Timeline) */}
            {shipmentTimelineItems.length > 0 ? (
              <Card aria-label="Hành trình vận đơn">
                <CardHeader>
                  <CardTitle>
                    Hành trình vận đơn (
                    {shipment?.tracking_code || order.tracking_code || order.order_code})
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <Timeline items={shipmentTimelineItems} />
                </CardContent>
              </Card>
            ) : null}

            {/* 4. Order Status Timeline (Read-only per §9 A2) */}
            {orderTimelineItems.length > 0 ? (
              <Card aria-label="Lịch sử đơn hàng">
                <CardHeader>
                  <CardTitle>Lịch sử xử lý đơn hàng</CardTitle>
                </CardHeader>
                <CardContent>
                  <Timeline items={orderTimelineItems} />
                </CardContent>
              </Card>
            ) : null}
          </>
        }
        aside={
          <>
            {/* Customer & Delivery Address Card */}
            <Card aria-label="Thông tin giao hàng">
              <CardHeader>
                <CardTitle>Khách hàng</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 text-[13px]">
                <div className="font-medium text-foreground">
                  {order.shipping_address?.recipient_name || EMPTY_DASH}
                </div>

                <Separator />

                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="font-medium text-foreground">Liên hệ</div>
                    <div className="mt-0.5 text-muted-foreground tabular-nums">
                      {order.shipping_address?.recipient_phone || EMPTY_DASH}
                    </div>
                  </div>
                  {order.shipping_address?.recipient_phone ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      aria-label="Sao chép số điện thoại"
                      onClick={() =>
                        void copyValue(
                          order.shipping_address!.recipient_phone,
                          "số điện thoại người nhận"
                        )
                      }
                    >
                      <Copy className="size-3.5" aria-hidden="true" />
                    </Button>
                  ) : null}
                </div>

                <Separator />

                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="font-medium text-foreground">Địa chỉ giao hàng</div>
                    <div className="mt-0.5 text-muted-foreground">{fullAddress || EMPTY_DASH}</div>
                  </div>
                  {fullAddress ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      aria-label="Sao chép địa chỉ giao hàng"
                      onClick={() => void copyValue(fullAddress, "địa chỉ giao hàng")}
                    >
                      <Copy className="size-3.5" aria-hidden="true" />
                    </Button>
                  ) : null}
                </div>
              </CardContent>
            </Card>

            {/* Shipping / Carrier Info Card */}
            <Card aria-label="Vận chuyển và vận đơn">
              <CardHeader>
                <CardTitle>Vận chuyển</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2.5 text-[13px]">
                <div className="flex items-center gap-2 font-medium text-foreground">
                  <Truck className="size-4 shrink-0 text-brand" aria-hidden="true" />
                  <span>
                    {order.carrier_code ? formatCarrierLabel(order.carrier_code) : "Chưa gán hãng"}
                  </span>
                </div>

                {order.tracking_code ? (
                  <div className="flex items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="text-[11px] text-muted-foreground">Mã vận đơn</div>
                      <div className="truncate font-mono text-xs font-medium text-foreground">
                        {order.tracking_code}
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      aria-label="Sao chép mã vận đơn"
                      onClick={() => void copyValue(order.tracking_code, "mã vận đơn")}
                    >
                      <Copy className="size-3.5" aria-hidden="true" />
                    </Button>
                  </div>
                ) : null}

                {shipment ? (
                  <dl className="space-y-2 pt-1 tabular-nums">
                    <div className="flex items-baseline justify-between gap-2">
                      <dt className="text-muted-foreground">Trạng thái vận đơn</dt>
                      <dd className="text-right font-medium text-foreground">
                        {shipmentStatusInfo ? shipmentStatusInfo.label : shipment.status}
                      </dd>
                    </div>
                    {shipmentFeeVnd > 0 ? (
                      <div className="flex items-baseline justify-between gap-2">
                        <dt className="text-muted-foreground">Phí vận chuyển hãng</dt>
                        <dd className="font-medium text-foreground">{formatVnd(shipmentFeeVnd)}</dd>
                      </div>
                    ) : null}
                    {typeof shipment.cod_fee_vnd === "number" && shipment.cod_fee_vnd > 0 ? (
                      <div className="flex items-baseline justify-between gap-2">
                        <dt className="text-muted-foreground">Phí thu hộ COD</dt>
                        <dd className="font-medium text-foreground">
                          {formatVnd(shipment.cod_fee_vnd)}
                        </dd>
                      </div>
                    ) : null}
                  </dl>
                ) : null}

                {localizedCancelReason ? (
                  <>
                    <Separator />
                    <div>
                      <div className="text-xs font-medium text-destructive">Lý do hủy</div>
                      <p className="mt-0.5 text-muted-foreground">{localizedCancelReason}</p>
                    </div>
                  </>
                ) : null}
              </CardContent>
            </Card>
          </>
        }
      />
    </div>
  );
}
