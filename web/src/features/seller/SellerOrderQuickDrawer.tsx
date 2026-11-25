"use client";

import React from "react";
import Link from "next/link";
import { Button, IconArrowUpRight } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { SellerDrawer, statusPillClass } from "./SellerUiPrimitives";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  type GwSellerOrder,
} from "./sellerApiTypes";
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
  const statusMeta = order ? getSellerOrderStatusMeta(order.status) : null;
  const channelMeta = order ? getSalesChannelMeta(order.sales_channel) : null;
  const nextAction = order ? getNextBackendOrderAction(order.status) : null;

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
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Tiền hàng</span>
                <span className={common.drawerKvVal}>{formatVnd(order.subtotal_vnd)}</span>
              </div>
              {order.flash_sale_discount_vnd > 0 && (
                <div className={common.drawerKvRow}>
                  <span className={common.drawerKvKey}>Giảm Flash sale</span>
                  <span className={common.drawerKvVal}>
                    −{formatVnd(order.flash_sale_discount_vnd)}
                  </span>
                </div>
              )}
              {order.shop_voucher_discount_vnd > 0 && (
                <div className={common.drawerKvRow}>
                  <span className={common.drawerKvKey}>Voucher shop</span>
                  <span className={common.drawerKvVal}>
                    −{formatVnd(order.shop_voucher_discount_vnd)}
                  </span>
                </div>
              )}
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Phí sàn</span>
                <span className={common.drawerKvVal}>
                  {order.commission_vnd > 0 ? `−${formatVnd(order.commission_vnd)}` : "0₫"}
                </span>
              </div>
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Thực nhận</span>
                <span className={common.drawerKvVal}>
                  <strong>{formatVnd(computeSellerNetReceivable(order))}</strong>
                </span>
              </div>
              <div className={common.drawerKvRow}>
                <span className={common.drawerKvKey}>Thanh toán</span>
                <span className={common.drawerKvVal}>
                  {formatPaymentMethodVi(order.payment_method)},{" "}
                  {formatPaymentStatusVi(order.payment_status)}
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
