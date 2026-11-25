"use client";

import React, { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Button,
  IconArrowLeft,
  IconCheck,
  IconCopy,
  IconRefresh,
  IconTruck,
  StateView,
} from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { useSellerContext } from "./SellerShell";
import { SellerSlidingTabs, statusPillClass } from "./SellerUiPrimitives";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  formatPaymentStatusVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  type GwSellerOrder,
} from "./sellerApiTypes";
import {
  getReturnStatusMeta,
  normalizeSellerReturns,
  parseUiStateParam,
  validateRejectReason,
  type SellerReturnRequest,
} from "./sellerReturns";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/don-hang/[id]/page.module.css";

type DetailTab = "overview" | "shipping" | "history";

const EMPTY_VALUE = "-";
const RETURN_CHECK_STATUSES = new Set(["RETURN_REQUESTED", "RETURNED", "DELIVERED"]);

export interface SellerOrderDetailClientProps {
  initialOrder: GwSellerOrder | null;
  loadError: string | null;
}

export function SellerOrderDetailClient({ initialOrder, loadError }: SellerOrderDetailClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiStateParam(searchParams.get("state"));
  const { showToast } = useSellerContext();

  const [order, setOrder] = useState<GwSellerOrder | null>(initialOrder);
  const [tab, setTab] = useState<DetailTab>("overview");
  const [busy, setBusy] = useState(false);

  const [returns, setReturns] = useState<SellerReturnRequest[]>([]);
  const [returnsLoading, setReturnsLoading] = useState(false);
  const [returnsUnavailable, setReturnsUnavailable] = useState(false);
  const [returnBusyId, setReturnBusyId] = useState<string | null>(null);
  const [rejectOpenId, setRejectOpenId] = useState<string | null>(null);
  const [rejectDrafts, setRejectDrafts] = useState<Record<string, string>>({});
  const [rejectErrors, setRejectErrors] = useState<Record<string, string>>({});

  const orderId = order?.id ?? "";
  const orderStatusUpper = (order?.status ?? "").toUpperCase();
  const shouldLoadReturns = Boolean(orderId) && RETURN_CHECK_STATUSES.has(orderStatusUpper);

  const fetchReturns = useCallback(async (targetOrderId: string, signal?: AbortSignal) => {
    setReturnsLoading(true);
    setReturnsUnavailable(false);
    try {
      const res = await api<{ returns?: SellerReturnRequest[] }>(
        `/api/seller/orders/${encodeURIComponent(targetOrderId)}/returns`,
        { signal }
      );
      setReturns(normalizeSellerReturns(res));
    } catch (err) {
      if (signal?.aborted) return;
      if (
        err instanceof ApiError &&
        (err.status === 404 || err.status === 503 || err.status === 0)
      ) {
        setReturnsUnavailable(true);
      }
      setReturns([]);
    } finally {
      if (!signal?.aborted) {
        setReturnsLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    if (!shouldLoadReturns || !orderId) return;
    const ctrl = new AbortController();
    void fetchReturns(orderId, ctrl.signal);
    return () => ctrl.abort();
  }, [shouldLoadReturns, orderId, fetchReturns]);

  if (uiState === "loading") {
    return (
      <StateView
        kind="loading"
        surface="app"
        skeletonVariant="detail"
        title="Đang tải chi tiết đơn hàng..."
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
        message={loadError ?? "Hệ thống đơn hàng tạm gián đoạn."}
        actionLabel="Thử lại"
        onAction={() => router.refresh()}
      />
    );
  }

  if (uiState === "empty" || !order) {
    return (
      <div className={common.page}>
        <Link href="/seller/don-hang" className={styles.backLink}>
          <IconArrowLeft size={14} />
          <span>Đơn hàng</span>
        </Link>
        <StateView
          kind="empty"
          surface="app"
          title="Không tìm thấy mã đơn hàng."
          actionLabel="Về danh sách"
          actionHref="/seller/don-hang"
        />
      </div>
    );
  }

  const meta = getSellerOrderStatusMeta(order.status);
  const channelMeta = getSalesChannelMeta(order.sales_channel);
  const nextAction = getNextBackendOrderAction(order.status);
  const totalQty = (order.items ?? []).reduce((s, it) => s + it.quantity, 0);
  const netReceivable = computeSellerNetReceivable(order);
  const totalDeductions =
    order.flash_sale_discount_vnd + order.shop_voucher_discount_vnd + order.commission_vnd;
  const addr = order.shipping_address;
  const fullAddress = addr
    ? [addr.street_address, addr.ward_name, addr.province_name].filter(Boolean).join(", ")
    : EMPTY_VALUE;

  const showReturnsCard = returns.length > 0 || orderStatusUpper === "RETURN_REQUESTED";

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

  const handleApproveReturn = async (ret: SellerReturnRequest) => {
    if (returnBusyId) return;
    setReturnBusyId(ret.id);
    try {
      const res = await api<{ return_request?: SellerReturnRequest | null }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/approve`,
        { method: "POST", body: {} }
      );
      setReturns((prev) =>
        prev.map((item) =>
          item.id === ret.id ? (res.return_request ?? { ...item, status: "APPROVED" }) : item
        )
      );
      if (rejectOpenId === ret.id) setRejectOpenId(null);
      showToast(`Đã duyệt yêu cầu đổi trả cho đơn ${order.order_code}.`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể duyệt yêu cầu đổi trả lúc này.");
    } finally {
      setReturnBusyId(null);
    }
  };

  const handleRejectReturn = async (ret: SellerReturnRequest) => {
    if (returnBusyId) return;
    const check = validateRejectReason(rejectDrafts[ret.id] ?? "");
    if (!check.ok) {
      setRejectErrors((prev) => ({ ...prev, [ret.id]: check.error }));
      return;
    }
    setRejectErrors((prev) => ({ ...prev, [ret.id]: "" }));
    setReturnBusyId(ret.id);
    try {
      const res = await api<{ return_request?: SellerReturnRequest | null }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/reject`,
        { method: "POST", body: { reason: check.value } }
      );
      setReturns((prev) =>
        prev.map((item) =>
          item.id === ret.id
            ? (res.return_request ?? { ...item, status: "REJECTED", reject_reason: check.value })
            : item
        )
      );
      setRejectOpenId(null);
      setRejectDrafts((prev) => ({ ...prev, [ret.id]: "" }));
      showToast(`Đã từ chối yêu cầu đổi trả cho đơn ${order.order_code}.`);
      router.refresh();
    } catch (err) {
      showToast(
        err instanceof ApiError ? err.message : "Không thể từ chối yêu cầu đổi trả lúc này."
      );
    } finally {
      setReturnBusyId(null);
    }
  };

  return (
    <div className={common.page}>
      <Link href="/seller/don-hang" className={styles.backLink}>
        <IconArrowLeft size={14} />
        <span>Đơn hàng</span>
      </Link>

      <header className={styles.objectHeader}>
        <div className={styles.objectIdentity}>
          <div className={styles.objectText}>
            <div className={styles.objectTitleRow}>
              <h1 className={styles.objectTitle}>{order.order_code}</h1>
              <span className={statusPillClass(meta.tone)}>{meta.label}</span>
              <span
                className={`${common.channelTag} ${channelMeta.isStorefront ? common.channelTagStorefront : ""}`}
                data-sales-channel={channelMeta.value}
              >
                {channelMeta.label}
              </span>
              <button
                type="button"
                className={styles.iconBtn28}
                onClick={handleCopyCode}
                title="Sao chép mã đơn"
                aria-label="Sao chép mã đơn hàng"
              >
                <IconCopy size={14} />
              </button>
            </div>
            <p className={styles.objectMeta}>
              {order.created_at ? formatDateTimeVi(order.created_at) : EMPTY_VALUE}
              {addr?.province_name ? `, ${addr.ward_name}, ${addr.province_name}` : ""}
            </p>
          </div>
        </div>

        <div className={styles.objectActions}>
          {nextAction && (
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={busy}
              onClick={() => void handleAction()}
            >
              {nextAction.kind === "ship" ? <IconTruck size={14} /> : <IconCheck size={14} />}
              <span>{busy ? "Đang xử lý..." : nextAction.label}</span>
            </Button>
          )}
        </div>
      </header>

      <SellerSlidingTabs
        ariaLabel="Phân mục chi tiết đơn hàng"
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "overview", label: "Tổng quan" },
          { key: "shipping", label: "Vận chuyển" },
          { key: "history", label: "Lịch sử", count: order.timeline?.length ?? 0 },
        ]}
      />

      <div className={styles.sixGrid}>
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Tiền hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(order.subtotal_vnd)}</span>
          <span className={styles.kpiTileSub}>
            {formatInt(totalQty)} sản phẩm, {formatInt(order.items?.length ?? 0)} SKU
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Phí &amp; chiết khấu</span>
          <span className={styles.kpiTileVal}>{formatVnd(totalDeductions)}</span>
          <span className={styles.kpiTileSub}>
            Voucher {formatVnd(order.shop_voucher_discount_vnd)}, Phí sàn{" "}
            {formatVnd(order.commission_vnd)}
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Thực nhận</span>
          <span className={styles.kpiTileVal}>{formatVnd(netReceivable)}</span>
          <span className={styles.kpiTileSub}>
            {formatPaymentMethodVi(order.payment_method)},{" "}
            {formatPaymentStatusVi(order.payment_status)}
          </span>
        </div>

        {tab === "overview" && (
          <>
            {showReturnsCard && (
              <section
                className={`${styles.detailCard} ${styles.span6}`}
                aria-labelledby="returns-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="returns-heading" className={common.cardTitle}>
                    Yêu cầu đổi trả
                  </h2>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={returnsLoading}
                    onClick={() => void fetchReturns(order.id)}
                  >
                    <IconRefresh size={14} />
                    <span>{returnsLoading ? "Đang tải..." : "Làm mới"}</span>
                  </Button>
                </div>

                {returnsLoading && returns.length === 0 ? (
                  <div className={common.inlineEmpty}>Đang tải yêu cầu đổi trả...</div>
                ) : returns.length === 0 ? (
                  <div className={common.inlineEmpty}>
                    {returnsUnavailable
                      ? "Dịch vụ đổi trả tạm thời chưa phản hồi."
                      : "Chưa có hồ sơ đổi trả chi tiết."}
                  </div>
                ) : (
                  <div className={styles.kvList}>
                    {returns.map((ret) => {
                      const retMeta = getReturnStatusMeta(ret.status);
                      const isRejectOpen = rejectOpenId === ret.id;
                      const isProcessing = returnBusyId === ret.id;
                      const safePhotos = ret.photos.map((p) => sanitizeSafeUrl(p)).filter(Boolean);

                      return (
                        <div key={ret.id} className={styles.giftBox}>
                          <div className={styles.giftRow}>
                            <div>
                              <strong>{ret.id}</strong>
                              {ret.created_at && (
                                <span className={common.cellSub}>
                                  {formatDateTimeVi(ret.created_at)}
                                </span>
                              )}
                            </div>
                            <span className={statusPillClass(retMeta.tone)}>{retMeta.label}</span>
                          </div>

                          <div className={styles.kvList}>
                            <div className={styles.kvRow}>
                              <span className={styles.kvKey}>Lý do</span>
                              <span className={styles.kvVal}>{ret.reason || EMPTY_VALUE}</span>
                            </div>

                            {ret.refund_amount_vnd > 0 && (
                              <div className={styles.kvRow}>
                                <span className={styles.kvKey}>Hoàn dự kiến</span>
                                <span className={styles.kvVal}>
                                  {formatVnd(ret.refund_amount_vnd)}
                                </span>
                              </div>
                            )}

                            {ret.items.length > 0 && (
                              <div className={styles.kvRow}>
                                <span className={styles.kvKey}>Sản phẩm hoàn</span>
                                <span className={styles.kvVal}>
                                  {ret.items
                                    .map((it) => {
                                      const label = it.product_name || it.sku_name || it.sku_id;
                                      const variant =
                                        it.product_name && it.sku_name ? ` (${it.sku_name})` : "";
                                      return `${label}${variant} ×${formatInt(it.quantity)}`;
                                    })
                                    .join(", ")}
                                </span>
                              </div>
                            )}

                            {safePhotos.length > 0 && (
                              <div className={styles.kvRow}>
                                <span className={styles.kvKey}>Ảnh minh chứng</span>
                                <span className={styles.kvVal}>
                                  {safePhotos.map((photoUrl, idx) => (
                                    <React.Fragment key={`${ret.id}-photo-${idx}`}>
                                      {idx > 0 ? ", " : ""}
                                      <a
                                        href={photoUrl}
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        className={common.cellLink}
                                      >
                                        Ảnh {idx + 1}
                                      </a>
                                    </React.Fragment>
                                  ))}
                                </span>
                              </div>
                            )}

                            {ret.reject_reason && (
                              <div className={styles.kvRow}>
                                <span className={styles.kvKey}>Lý do từ chối</span>
                                <span className={styles.kvVal}>{ret.reject_reason}</span>
                              </div>
                            )}

                            {ret.seller_note && (
                              <div className={styles.kvRow}>
                                <span className={styles.kvKey}>Ghi chú</span>
                                <span className={styles.kvVal}>{ret.seller_note}</span>
                              </div>
                            )}
                          </div>

                          {retMeta.canDecide && (
                            <>
                              {isRejectOpen && (
                                <div className={styles.opsReplyBox}>
                                  <div className={common.field}>
                                    <label
                                      htmlFor={`reject-reason-${ret.id}`}
                                      className={common.fieldLabel}
                                    >
                                      Lý do từ chối (tối thiểu 5 ký tự)
                                    </label>
                                    <textarea
                                      id={`reject-reason-${ret.id}`}
                                      className={common.textArea}
                                      value={rejectDrafts[ret.id] ?? ""}
                                      onChange={(e) => {
                                        const val = e.target.value;
                                        setRejectDrafts((prev) => ({ ...prev, [ret.id]: val }));
                                        if (rejectErrors[ret.id]) {
                                          setRejectErrors((prev) => ({ ...prev, [ret.id]: "" }));
                                        }
                                      }}
                                      placeholder="Nêu rõ lý do từ chối..."
                                    />
                                    {rejectErrors[ret.id] && (
                                      <span className={common.fieldError}>
                                        {rejectErrors[ret.id]}
                                      </span>
                                    )}
                                  </div>
                                </div>
                              )}

                              <div className={styles.opsActionBar}>
                                {isRejectOpen ? (
                                  <>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      shape="app"
                                      disabled={isProcessing}
                                      onClick={() => setRejectOpenId(null)}
                                    >
                                      Đóng
                                    </Button>
                                    <Button
                                      variant="danger"
                                      size="sm"
                                      shape="app"
                                      disabled={isProcessing}
                                      onClick={() => void handleRejectReturn(ret)}
                                    >
                                      {isProcessing ? "Đang gửi..." : "Xác nhận từ chối"}
                                    </Button>
                                  </>
                                ) : (
                                  <>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      shape="app"
                                      disabled={isProcessing}
                                      onClick={() => setRejectOpenId(ret.id)}
                                    >
                                      Từ chối đổi trả
                                    </Button>
                                    <Button
                                      variant="primary"
                                      size="sm"
                                      shape="app"
                                      disabled={isProcessing}
                                      onClick={() => void handleApproveReturn(ret)}
                                    >
                                      <IconCheck size={14} />
                                      <span>
                                        {isProcessing ? "Đang xử lý..." : "Duyệt đổi trả"}
                                      </span>
                                    </Button>
                                  </>
                                )}
                              </div>
                            </>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </section>
            )}

            <section
              className={`${styles.detailCard} ${styles.span4}`}
              aria-labelledby="items-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="items-heading" className={common.cardTitle}>
                  Sản phẩm
                </h2>
              </div>

              <div className={common.tableWrap}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm &amp; SKU</th>
                      <th className={common.numCol}>Đơn giá</th>
                      <th className={common.numCol}>SL</th>
                      <th className={common.numCol}>Thành tiền</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(order.items ?? []).map((item) => (
                      <tr key={item.id}>
                        <td>
                          <Link
                            href={`/seller/san-pham/${item.product_id}`}
                            className={common.cellLink}
                          >
                            {item.product_name}
                          </Link>
                          <span className={common.cellSub}>{item.sku_name || item.sku_id}</span>
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
                    <span>{`−${formatVnd(order.flash_sale_discount_vnd)}`}</span>
                  </div>
                )}
                <div className={styles.reconRow}>
                  <span>Voucher shop</span>
                  <span>
                    {order.shop_voucher_discount_vnd > 0
                      ? `−${formatVnd(order.shop_voucher_discount_vnd)}`
                      : "0₫"}
                  </span>
                </div>
                <div className={styles.reconRow}>
                  <span>Phí hoa hồng sàn</span>
                  <span>
                    {order.commission_vnd > 0 ? `−${formatVnd(order.commission_vnd)}` : "0₫"}
                  </span>
                </div>
                <div className={`${styles.reconRow} ${styles.reconRowTotal}`}>
                  <span>Thực nhận</span>
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
                  Giao nhận
                </h2>
              </div>

              <div className={styles.kvList}>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Kênh bán</span>
                  <span className={styles.kvVal}>
                    <span
                      className={`${common.channelTag} ${channelMeta.isStorefront ? common.channelTagStorefront : ""}`}
                      data-sales-channel={channelMeta.value}
                    >
                      {channelMeta.label}
                    </span>
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Người nhận</span>
                  <span className={styles.kvVal}>{addr?.recipient_name || EMPTY_VALUE}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Điện thoại</span>
                  <span className={styles.kvVal}>{addr?.recipient_phone || EMPTY_VALUE}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Địa chỉ</span>
                  <span className={styles.kvVal}>{fullAddress}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>ĐVVC</span>
                  <span className={styles.kvVal}>{order.carrier_code || "Tiêu chuẩn"}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Mã vận đơn</span>
                  <span className={styles.kvVal}>{order.tracking_code || EMPTY_VALUE}</span>
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
                Vận đơn &amp; bàn giao
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
                <span className={styles.kvVal}>{order.tracking_code || "Chưa tạo"}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Phí vận chuyển</span>
                <span className={styles.kvVal}>{formatVnd(order.shipping_fee_vnd)}</span>
              </div>
              <div className={styles.kvRow}>
                <span className={styles.kvKey}>Khách thanh toán</span>
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
                Nhật ký trạng thái
              </h2>
            </div>

            {(order.timeline ?? []).length === 0 ? (
              <div className={common.inlineEmpty}>Chưa có mốc trạng thái.</div>
            ) : (
              <div className={styles.kvList}>
                {order.timeline.map((ev) => {
                  const toMeta = getSellerOrderStatusMeta(ev.to_status);
                  return (
                    <div key={ev.id} className={styles.kvRow}>
                      <span className={styles.kvKey}>
                        {ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_VALUE}
                      </span>
                      <span className={styles.kvVal}>
                        <strong>{toMeta.label}</strong>
                        {ev.note ? `, ${ev.note}` : ""}
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
