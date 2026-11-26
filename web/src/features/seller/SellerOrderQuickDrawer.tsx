"use client";

import React from "react";
import Link from "next/link";
import { ExternalLink, Printer } from "lucide-react";
import { Money, StatusBadge } from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { buildShippingLabelHref } from "@/features/account/orderMeta";
import { resolveSellerOrderTone } from "./ops/sellerTone";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { buildSellerOrderBreakdownLines } from "./types";

export interface SellerOrderQuickDrawerProps {
  order: GwSellerOrder | null;
  onClose: () => void;
  onAction: (order: GwSellerOrder) => void;
  actionBusy: boolean;
  actionDisabled?: boolean;
}

/**
 * Shared slide-over order quick-inspection Sheet used by Seller Overview and Seller Orders.
 */
export function SellerOrderQuickDrawer({
  order,
  onClose,
  onAction,
  actionBusy,
  actionDisabled = false,
}: SellerOrderQuickDrawerProps) {
  const statusMeta = order
    ? getSellerOrderStatusMeta(order.status, order.shipment_job_status, order.tracking_code)
    : null;
  const orderTone = order
    ? resolveSellerOrderTone(order.status, order.shipment_job_status, order.tracking_code)
    : "neutral";
  const channelMeta = order ? getSalesChannelMeta(order.sales_channel) : null;
  const nextAction = order
    ? getNextBackendOrderAction(order.status, order.tracking_code, order.shipment_job_status)
    : null;
  const breakdownLines = order ? buildSellerOrderBreakdownLines(order) : [];
  const labelHref = order?.tracking_code ? buildShippingLabelHref(order.tracking_code) : null;

  return (
    <Sheet open={Boolean(order)} onOpenChange={(open) => !open && onClose()}>
      <SheetContent side="right" className="w-full gap-0 p-0 sm:max-w-md">
        {order ? (
          <>
            <SheetHeader className="border-b border-border px-5 py-4 pr-12">
              <div className="flex flex-wrap items-center gap-2">
                <SheetTitle className="text-base font-semibold text-foreground">
                  {order.order_code}
                </SheetTitle>
                {statusMeta ? <StatusBadge tone={orderTone}>{statusMeta.label}</StatusBadge> : null}
                {channelMeta ? (
                  <Badge
                    variant={channelMeta.isStorefront ? "info" : "secondary"}
                    className="rounded-md"
                    data-sales-channel={channelMeta.value}
                  >
                    {channelMeta.label}
                  </Badge>
                ) : null}
              </div>
              {order.created_at ? (
                <SheetDescription className="text-xs tabular-nums">
                  Đặt lúc {formatDateTimeVi(order.created_at)}
                </SheetDescription>
              ) : null}
            </SheetHeader>

            <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4 text-sm">
              {/* Items */}
              <section className="space-y-2.5">
                <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
                  Sản phẩm
                </h3>
                <div className="divide-y divide-border rounded-lg border border-border bg-muted/30">
                  {(order.items ?? []).map((item) => (
                    <div
                      key={item.id}
                      className="flex items-start justify-between gap-3 px-3 py-2.5 text-[13px]"
                    >
                      <span className="min-w-0 flex-1 text-foreground">
                        {item.product_name}
                        {item.sku_name ? (
                          <span className="text-muted-foreground"> ({item.sku_name})</span>
                        ) : null}
                        <span className="ml-1 text-muted-foreground tabular-nums">
                          × {formatInt(item.quantity)}
                        </span>
                      </span>
                      <Money vnd={item.line_total_vnd} size="sm" className="shrink-0" />
                    </div>
                  ))}
                </div>
              </section>

              <Separator />

              {/* Settlement */}
              <section className="space-y-2">
                <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
                  Đối soát
                </h3>
                <div className="space-y-1.5 text-[13px] tabular-nums">
                  {breakdownLines.map((line) => (
                    <div key={line.key} className="flex items-baseline justify-between gap-3">
                      <span className="text-muted-foreground">{line.label}</span>
                      <span className="text-foreground">
                        {line.isDeduction
                          ? line.amountVnd < 0
                            ? `−${formatVnd(Math.abs(line.amountVnd))}`
                            : "0₫"
                          : formatVnd(line.amountVnd)}
                      </span>
                    </div>
                  ))}
                  <Separator className="my-2" />
                  <div className="flex items-baseline justify-between gap-3 font-semibold text-foreground">
                    <span>Thực nhận dự kiến</span>
                    <Money vnd={computeSellerNetReceivable(order)} />
                  </div>
                  <div className="flex items-baseline justify-between gap-3 pt-1">
                    <span className="text-muted-foreground">Thanh toán</span>
                    <span className="text-right text-foreground">
                      {formatPaymentMethodVi(order.payment_method)}
                      {order.payment_status || (order.payment_method ?? "").toUpperCase() === "COD"
                        ? `, ${formatPaymentStatusVi(
                            order.payment_status ?? "",
                            order.payment_method,
                            order.status
                          )}`
                        : ""}
                    </span>
                  </div>
                </div>
              </section>

              <Separator />

              {/* Shipping */}
              <section className="space-y-2">
                <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
                  Giao nhận
                </h3>
                <dl className="space-y-2 text-[13px]">
                  <div className="flex items-baseline justify-between gap-3">
                    <dt className="shrink-0 text-muted-foreground">Người nhận</dt>
                    <dd className="text-right font-medium text-foreground">
                      {order.shipping_address?.recipient_name || "Người mua"}
                    </dd>
                  </div>
                  <div className="flex items-baseline justify-between gap-3">
                    <dt className="shrink-0 text-muted-foreground">Điện thoại</dt>
                    <dd className="text-right text-foreground tabular-nums">
                      {order.shipping_address?.recipient_phone || "Chưa cập nhật"}
                    </dd>
                  </div>
                  <div className="flex items-baseline justify-between gap-3">
                    <dt className="shrink-0 text-muted-foreground">Địa chỉ</dt>
                    <dd className="text-right text-foreground">
                      {[
                        order.shipping_address?.street_address,
                        order.shipping_address?.ward_name,
                        order.shipping_address?.province_name,
                      ]
                        .filter(Boolean)
                        .join(", ") || "Chưa cập nhật"}
                    </dd>
                  </div>
                  {order.tracking_code ? (
                    <div className="flex items-baseline justify-between gap-3">
                      <dt className="shrink-0 text-muted-foreground">Mã vận đơn</dt>
                      <dd className="font-mono text-xs font-medium text-foreground">
                        {order.tracking_code}
                      </dd>
                    </div>
                  ) : null}
                </dl>
              </section>
            </div>

            <SheetFooter className="flex-row flex-wrap items-center justify-between gap-2 border-t border-border bg-card px-5 py-3.5">
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" asChild>
                  <Link href={`/seller/don-hang/${order.id}`} onClick={onClose}>
                    <span>Mở trang chi tiết</span>
                    <ExternalLink className="size-3.5" aria-hidden="true" />
                  </Link>
                </Button>
                {labelHref ? (
                  <Button variant="ghost" size="sm" asChild>
                    <a href={labelHref} target="_blank" rel="noopener noreferrer">
                      <Printer className="size-3.5" aria-hidden="true" />
                      <span>In nhãn vận đơn</span>
                    </a>
                  </Button>
                ) : null}
              </div>

              {nextAction ? (
                <Button
                  size="sm"
                  disabled={actionBusy || actionDisabled}
                  onClick={() => onAction(order)}
                >
                  {actionBusy ? "Đang xử lý..." : nextAction.label}
                </Button>
              ) : null}
            </SheetFooter>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
