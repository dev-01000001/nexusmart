"use client";

import React, { useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { IconCheck, IconFileText, IconTruck, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { useSellerContext } from "./SellerShell";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { EMPTY_DASH, parseUiState } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/don-hang/[id]/page.module.css";

type DetailTab = "overview" | "shipping" | "history";

export interface SellerOrderDetailClientProps {
  initialOrder: GwSellerOrder | null;
  loadError: string | null;
}

export function SellerOrderDetailClient({ initialOrder, loadError }: SellerOrderDetailClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { showToast } = useSellerContext();

  const [order, setOrder] = useState<GwSellerOrder | null>(initialOrder);
  const [tab, setTab] = useState<DetailTab>("overview");
  const [busy, setBusy] = useState(false);

  if (uiState === "loading") {
    return (
      <StateView
        kind="loading"
        surface="app"
        title="Đang tải chi tiết đơn hàng và chứng từ vận chuyển..."
        rows={5}
      />
    );
  }

  if (uiState === "error" || loadError) {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể tải chi tiết đơn hàng"
        message={loadError ?? "Hệ thống đơn hàng tạm thời chưa phản hồi. Vui lòng thử tải lại."}
        actionLabel="Thử lại"
        onAction={() => router.refresh()}
      />
    );
  }

  if (uiState === "empty" || !order) {
    return (
      <div className={common.page}>
        <div className={styles.backRow}>
          <Link href="/seller/don-hang" className={styles.backLink}>
            Quay lại danh sách đơn hàng
          </Link>
        </div>
        <StateView
          kind="empty"
          surface="app"
          title="Không tìm thấy mã đơn hàng"
          message="Đơn hàng không tồn tại hoặc không thuộc gian hàng của bạn."
          actionLabel="Về danh sách đơn hàng"
          actionHref="/seller/don-hang"
        />
      </div>
    );
  }

  const meta = getSellerOrderStatusMeta(order.status);
  const nextAction = getNextBackendOrderAction(order.status);
  const totalQty = (order.items ?? []).reduce((s, it) => s + it.quantity, 0);
  const netReceivable = computeSellerNetReceivable(order);
  const totalDeductions =
    order.flash_sale_discount_vnd + order.shop_voucher_discount_vnd + order.commission_vnd;
  const addr = order.shipping_address;
  const fullAddress = addr
    ? [addr.street_address, addr.ward_name, addr.province_name].filter(Boolean).join(", ")
    : EMPTY_DASH;

  const pillCls = (tone: "neutral" | "warn" | "pos" | "neg") =>
    tone === "pos"
      ? `${common.pill} ${common.pillPos}`
      : tone === "warn"
        ? `${common.pill} ${common.pillWarn}`
        : tone === "neg"
          ? `${common.pill} ${common.pillNeg}`
          : common.pill;

  const handleCopyCode = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(order.order_code).catch(() => {});
    }
    showToast(`Đã sao chép mã đơn ${order.order_code}.`);
  };

  const handleAction = async () => {
    if (!nextAction || busy) return;
    setBusy(true);
    try {
      const url =
        nextAction.kind === "confirm"
          ? `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`
          : `/api/seller/orders/${encodeURIComponent(order.id)}/ship`;
      const res = await api<{ order: GwSellerOrder }>(url, { method: "POST" });
      if (res.order) {
        setOrder(res.order);
      }
      showToast(`Đơn ${order.order_code}: ${nextAction.label} thành công.`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái đơn hàng.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className={common.page}>
      <div className={styles.backRow}>
        <Link href="/seller/don-hang" className={styles.backLink}>
          Quay lại danh sách đơn hàng
        </Link>
      </div>

      <header className={styles.objectHeader}>
        <div className={styles.objectIdentity}>
          <div className={styles.objectText}>
            <div className={styles.objectTitleRow}>
              <h1 className={styles.objectTitle}>{order.order_code}</h1>
              <span className={pillCls(meta.tone)}>{meta.label}</span>
              <button
                type="button"
                className={styles.iconBtn28}
                onClick={handleCopyCode}
                title="Sao chép mã đơn"
                aria-label="Sao chép mã đơn hàng"
              >
                <IconFileText size={14} />
              </button>
            </div>
            <p className={styles.objectMeta}>
              Đặt lúc {order.created_at ? formatDateTimeVi(order.created_at) : EMPTY_DASH}
              {addr?.province_name ? ` · ${addr.ward_name}, ${addr.province_name}` : ""}
            </p>
          </div>
        </div>

        <div className={styles.objectActions}>
          {nextAction && (
            <button
              type="button"
              className={`${styles.actionBtn36} ${styles.actionInk}`}
              disabled={busy}
              onClick={() => void handleAction()}
            >
              {nextAction.kind === "ship" ? <IconTruck size={14} /> : <IconCheck size={14} />}
              <span>{busy ? "Đang xử lý..." : nextAction.label}</span>
            </button>
          )}
        </div>
      </header>

      <div className={common.underlineTabs} role="tablist" aria-label="Phân mục chi tiết đơn hàng">
        <button
          type="button"
          role="tab"
          aria-selected={tab === "overview"}
          className={`${common.underlineTab} ${tab === "overview" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("overview")}
        >
          Tổng quan đơn
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "shipping"}
          className={`${common.underlineTab} ${tab === "shipping" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("shipping")}
        >
          Vận chuyển & chứng từ
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "history"}
          className={`${common.underlineTab} ${tab === "history" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("history")}
        >
          Lịch sử trạng thái ({formatInt(order.timeline?.length ?? 0)})
        </button>
      </div>

      <div className={styles.sixGrid}>
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Giá trị tiền hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(order.subtotal_vnd)}</span>
          <span className={styles.kpiTileSub}>
            {formatInt(totalQty)} sản phẩm · {formatInt(order.items?.length ?? 0)} dòng SKU
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Phí sàn & chiết khấu shop</span>
          <span className={styles.kpiTileVal}>{formatVnd(totalDeductions)}</span>
          <span className={styles.kpiTileSub}>
            Voucher {formatVnd(order.shop_voucher_discount_vnd)} · Phí sàn{" "}
            {formatVnd(order.commission_vnd)}
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Thực nhận gian hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(netReceivable)}</span>
          <span className={styles.kpiTileSub}>
            {formatPaymentMethodVi(order.payment_method)} ·{" "}
            {formatPaymentStatusVi(order.payment_status)}
          </span>
        </div>

        {tab === "overview" && (
          <>
            <section
              className={`${styles.detailCard} ${styles.span4}`}
              aria-labelledby="items-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="items-heading" className={common.cardTitle}>
                  Sản phẩm và mã SKU
                </h2>
                <span className={common.cardMeta}>Số liệu đồng bộ từ hệ thống đơn hàng</span>
              </div>

              <div className={common.tableWrap}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm & biến thể</th>
                      <th className={common.numCol}>Đơn giá</th>
                      <th className={common.numCol}>SL</th>
                      <th className={common.numCol}>Thành tiền</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(order.items ?? []).map((item) => (
                      <tr key={item.id}>
                        <td>
                          <div>
                            <Link
                              href={`/seller/san-pham/${item.product_id}`}
                              className={common.cellLink}
                            >
                              {item.product_name}
                            </Link>
                            <span className={common.cellSub}>{item.sku_name || item.sku_id}</span>
                          </div>
                        </td>
                        <td className={common.numCol}>
                          {formatVnd(item.effective_unit_price_vnd || item.unit_price_vnd)}
                        </td>
                        <td className={common.numCol}>{formatInt(item.quantity)}</td>
                        <td className={common.numCol}>
                          <strong>{formatVnd(item.line_total_vnd)}</strong>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className={styles.reconBlock}>
                <div className={styles.reconRow}>
                  <span>Tổng tiền hàng</span>
                  <span>{formatVnd(order.subtotal_vnd)}</span>
                </div>
                {order.flash_sale_discount_vnd > 0 && (
                  <div className={styles.reconRow}>
                    <span>Giảm Flash sale</span>
                    <span>{`\u2212${formatVnd(order.flash_sale_discount_vnd)}`}</span>
                  </div>
                )}
                <div className={styles.reconRow}>
                  <span>Voucher gian hàng tài trợ</span>
                  <span>
                    {order.shop_voucher_discount_vnd > 0
                      ? `\u2212${formatVnd(order.shop_voucher_discount_vnd)}`
                      : "0₫"}
                  </span>
                </div>
                <div className={styles.reconRow}>
                  <span>Phí hoa hồng sàn</span>
                  <span>
                    {order.commission_vnd > 0 ? `\u2212${formatVnd(order.commission_vnd)}` : "0₫"}
                  </span>
                </div>
                <div className={`${styles.reconRow} ${styles.reconRowTotal}`}>
                  <span>Thực nhận về sổ cái gian hàng</span>
                  <span>{formatVnd(netReceivable)}</span>
                </div>
              </div>
            </section>

            <section
              className={`${styles.detailCard} ${styles.span2}`}
              aria-labelledby="buyer-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="buyer-heading" className={common.cardTitle}>
                  Thông tin giao nhận
                </h2>
              </div>

              <div className={styles.kvList}>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Người nhận</span>
                  <span className={styles.kvVal}>{addr?.recipient_name || EMPTY_DASH}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Điện thoại</span>
                  <span className={styles.kvVal}>{addr?.recipient_phone || EMPTY_DASH}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Địa chỉ giao hàng</span>
                  <span className={styles.kvVal}>{fullAddress}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Đơn vị vận chuyển</span>
                  <span className={styles.kvVal}>{order.carrier_code || "Tiêu chuẩn"}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Mã vận đơn</span>
                  <span className={styles.kvVal}>{order.tracking_code || EMPTY_DASH}</span>
                </div>
                {order.cancel_reason && (
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Lý do hủy</span>
                    <span className={styles.kvVal}>{order.cancel_reason}</span>
                  </div>
                )}
              </div>
            </section>
          </>
        )}

        {tab === "shipping" && (
          <section
            className={`${styles.detailCard} ${styles.span6}`}
            aria-labelledby="shipping-heading"
          >
            <div className={common.cardHeader}>
              <h2 id="shipping-heading" className={common.cardTitle}>
                Thông tin vận đơn & bàn giao ĐVVC
              </h2>
            </div>

            <div className={styles.kvList}>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Đơn vị vận chuyển</span>
                <span className={styles.kvVal}>{order.carrier_code || "Chưa gán"}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Gói dịch vụ</span>
                <span className={styles.kvVal}>{order.service_code || "Tiêu chuẩn"}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Mã vận đơn</span>
                <span className={styles.kvVal}>{order.tracking_code || "Chưa tạo vận đơn"}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Phí vận chuyển khách trả</span>
                <span className={styles.kvVal}>{formatVnd(order.shipping_fee_vnd)}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Tổng thanh toán của khách</span>
                <span className={styles.kvVal}>{formatVnd(order.total_payable_vnd)}</span>
              </div>
            </div>
          </section>
        )}

        {tab === "history" && (
          <section
            className={`${styles.detailCard} ${styles.span6}`}
            aria-labelledby="history-heading"
          >
            <div className={common.cardHeader}>
              <h2 id="history-heading" className={common.cardTitle}>
                Nhật ký chuyển trạng thái đơn hàng
              </h2>
            </div>

            {(order.timeline ?? []).length === 0 ? (
              <div className={common.inlineEmpty}>Chưa có mốc cập nhật trạng thái nào.</div>
            ) : (
              <div className={styles.kvList}>
                {order.timeline.map((ev) => {
                  const toMeta = getSellerOrderStatusMeta(ev.to_status);
                  return (
                    <div key={ev.id} className={styles.kvRow}>
                      <span className={styles.kvKey}>
                        {ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH}
                      </span>
                      <span className={styles.kvVal}>
                        <strong>{toMeta.label}</strong>
                        {ev.note ? ` · ${ev.note}` : ""}
                      </span>
                    </div>
                  );
                })}
              </div>
            )}
          </section>
        )}
      </div>
    </div>
  );
}
