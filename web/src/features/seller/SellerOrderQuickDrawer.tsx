"use client";

import React from "react";
import Link from "next/link";
import { Button, IconArrowUpRight } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { buildShippingLabelHref } from "@/features/account/orderMeta";
import { SellerDrawer, statusPillClass } from "./SellerUiPrimitives";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { buildSellerOrderBreakdownLines } from "./types";
import common from "./SellerCommon.module.css";

export interface SellerOrderQuickDrawerProps {
  order: GwSellerOrder | null;
  onClose: () => void;
  onAction: (order: GwSellerOrder) => void;
  actionBusy: boolean;
  actionDisabled?: boolean;
}

/**
 * Shared slide-over order quick-inspection drawer used by Seller Overview and Seller Orders.
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
  const channelMeta = order ? getSalesChannelMeta(order.sales_channel) : null;
  const nextAction = order
    ? getNextBackendOrderAction(order.status, order.tracking_code, order.shipment_job_status)
    : null;
  const breakdownLines = order ? buildSellerOrderBreakdownLines(order) : [];
  const labelHref = order?.tracking_code ? buildShippingLabelHref(order.tracking_code) : null;

  return (
    <SellerDrawer
      open={Boolean(order)}
      onClose={onClose}
      title={order ? order.order_code : ""}
      subtitle={order?.created_at ? `Đặt lúc ${formatDateTimeVi(order.created_at)}` : ""}
      badge={
        statusMeta && channelMeta ? (
          <>
            <span className={statusPillClass(statusMeta.tone)}>{statusMeta.label}</span>
            <span
              className={`${common.channelTag} ${channelMeta.isStorefront ? common.channelTagStorefront : ""}`}
              data-sales-channel={channelMeta.value}
            >
              {channelMeta.label}
            </span>
          </>
        ) : null
      }
      footer={
        order ? (
          <>
            <Link href={`/seller/don-hang/${order.id}`} className={common.cardLink}>
              <span>Mở trang chi tiết</span>
              <IconArrowUpRight size={14} />
            </Link>
            {labelHref && (
              <a
                href={labelHref}
                target="_blank"
                rel="noopener noreferrer"
                className={common.cardLink}
              >
                <span>In nhãn vận đơn</span>
                <IconArrowUpRight size={14} />
              </a>
            )}
            {nextAction && (
              <Button
                variant="primary"
                size="sm"
                shape="app"
                disabled={actionBusy || actionDisabled}
                onClick={() => onAction(order)}
              >
                {actionBusy ? "Đang xử lý..." : nextAction.label}
              </Button>
            )}
          </>
        ) : null
      }
    >
      {order && (
        <>
          <div className={common.drawerSection}>
            <h3 className={common.drawerSectionTitle}>Sản phẩm</h3>
            <div className={common.drawerKvList}>
              {(order.items ?? []).map((item) => (
                <div key={item.id} className={common.drawerKvRow}>
                  <span className={common.drawerKvKey}>
                    {item.product_name}
                    {item.sku_name ? ` (${item.sku_name})` : ""} × {formatInt(item.quantity)}
                  </span>
                  <span className={common.drawerKvVal}>{formatVnd(item.line_total_vnd)}</span>
                </div>
              ))}
            </div>
          </div>

          <div className={common.drawerSection}>
            <h3 className={common.drawerSectionTitle}>Đối soát</h3>
            <div className={common.drawerKvList}>
              {breakdownLines.map((line) => (
                <div key={line.key} className={common.drawerKvRow}>
                  <span className={common.drawerKvKey}>{line.label}</span>
                  <span className={common.drawerKvVal}>
                    {line.isDeduction
                      ? line.amountVnd < 0
                        ? `−${formatVnd(Math.abs(line.amountVnd))}`
                        : "0₫"
                      : formatVnd(line.amountVnd)}
                  </span>
                </div>
              ))}
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Thực nhận dự kiến</span>
                <span className={common.drawerKvVal}>
                  <strong>{formatVnd(computeSellerNetReceivable(order))}</strong>
                </span>
              </div>
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Thanh toán</span>
                <span className={common.drawerKvVal}>
                  {formatPaymentMethodVi(order.payment_method)}
                  {order.payment_status || (order.payment_method ?? "").toUpperCase() === "COD"
                    ? `, ${formatPaymentStatusVi(order.payment_status ?? "", order.payment_method, order.status)}`
                    : ""}
                </span>
              </div>
            </div>
          </div>

          <div className={common.drawerSection}>
            <h3 className={common.drawerSectionTitle}>Giao nhận</h3>
            <div className={common.drawerKvList}>
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Người nhận</span>
                <span className={common.drawerKvVal}>
                  {order.shipping_address?.recipient_name || "Người mua"}
                </span>
              </div>
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Điện thoại</span>
                <span className={common.drawerKvVal}>
                  {order.shipping_address?.recipient_phone || "Chưa cập nhật"}
                </span>
              </div>
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Địa chỉ</span>
                <span className={common.drawerKvVal}>
                  {[
                    order.shipping_address?.street_address,
                    order.shipping_address?.ward_name,
                    order.shipping_address?.province_name,
                  ]
                    .filter(Boolean)
                    .join(", ") || "Chưa cập nhật"}
                </span>
              </div>
              {order.tracking_code && (
                <div className={common.drawerKvRow}>
                  <span className={common.drawerKvKey}>Mã vận đơn</span>
                  <span className={common.drawerKvVal}>{order.tracking_code}</span>
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </SellerDrawer>
  );
}
