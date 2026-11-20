"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useSearchParams } from "next/navigation";
import { PAYMENT_METHOD_LABELS, SHOPS, getShopById } from "@/data";
import type { TwoWayCourierStatus, VatInvoiceStatus } from "@/data";
import { useStore } from "@/context/StoreContext";
import {
  IconArrowUpRight,
  IconChat,
  IconCheck,
  IconFileText,
  IconTruck,
  StateView,
} from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import {
  EMPTY_DASH,
  advanceOrderTwoWayExchange,
  advanceOrderVatStatus,
  applySellerOrderStatusOverride,
  findSellerOrderByAnyId,
  getNextSellerTransition,
  getSafeOrderStatusMeta,
  parseUiState,
  replyOrderTier1Complaint,
  resolveOrderAmendment,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type DetailTab = "overview" | "ops" | "shipping" | "history";

const VAT_STATUS_LABEL: Record<
  VatInvoiceStatus,
  { label: string; tone: "neutral" | "warn" | "pos" }
> = {
  chua_yeu_cau: { label: "Không yêu cầu VAT", tone: "neutral" },
  cho_duyet_nhap: { label: "Chờ gửi nháp 24h", tone: "warn" },
  da_gui_nhap_24h: { label: "Đã gửi nháp (chờ duyệt 24h)", tone: "warn" },
  da_phat_hanh: { label: "Đã phát hành HĐĐT", tone: "pos" },
};

const EXCHANGE_STEPS: Array<{ key: TwoWayCourierStatus; label: string }> = [
  { key: "cho_dieu_phoi", label: "Chờ điều phối" },
  { key: "da_dat_buu_ta", label: "Đã đặt bưu tá" },
  { key: "dang_doi_2_chieu", label: "Đang đổi 2 chiều" },
  { key: "hoan_tat", label: "Hoàn tất đổi size" },
];

function SellerOrderDetailContent() {
  const params = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialTab = searchParams.get("tab") as DetailTab | null;
  const orderParam = typeof params?.id === "string" ? params.id : "";

  const { activeSellerShopId, orders: storeOrders, updateOrderStatus, showToast } = useStore();
  const session = useSellerSession();
  const [tab, setTab] = useState<DetailTab>(
    initialTab && ["overview", "ops", "shipping", "history"].includes(initialTab)
      ? initialTab
      : "overview"
  );
  const [amendNote, setAmendNote] = useState("");
  const [complaintDraft, setComplaintDraft] = useState("");

  const order = useMemo(
    () => findSellerOrderByAnyId(activeSellerShopId, orderParam, storeOrders, session),
    [activeSellerShopId, orderParam, storeOrders, session]
  );

  const shop = getShopById(order?.shopId ?? activeSellerShopId) ?? SHOPS[0];

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

  if (uiState === "error") {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể tải chi tiết đơn hàng"
        message="Hệ thống đơn hàng tạm thời chưa phản hồi. Vui lòng thử tải lại."
        lastUpdated="02:18 · 20/11/2026"
        actionLabel="Thử lại"
        actionHref={`/seller/don-hang/${orderParam}`}
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
          message="Đơn hàng không tồn tại trong hệ thống hoặc đã bị lưu trữ."
          actionLabel="Về danh sách đơn hàng"
          actionHref="/seller/don-hang"
        />
      </div>
    );
  }

  const meta = getSafeOrderStatusMeta(order.status);
  const nextAction = getNextSellerTransition(order.status);
  const coverImage = order.items[0]?.image ?? "/products/ao-thun-cotton-trang.png";
  const totalFeesAndVoucher = order.shopVoucherDiscount + order.platformFee;
  const ex = order.wave2Extra;

  const activeOpsCount = [
    ex?.vatRequest ? 1 : 0,
    ex?.amendment ? 1 : 0,
    ex?.twoWayExchange ? 1 : 0,
    ex?.tier1Complaint ? 1 : 0,
  ].reduce((s, n) => s + n, 0);

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
      navigator.clipboard.writeText(order.orderCode).catch(() => {});
    }
    showToast(`Đã sao chép mã đơn ${order.orderCode}.`);
  };

  const handlePrintSlip = () => {
    showToast(`Đã chuẩn bị phiếu đóng gói cho đơn ${order.orderCode}.`);
  };

  const handleAdvance = () => {
    if (!nextAction) return;
    applySellerOrderStatusOverride(order.orderCode, nextAction.nextStatus, nextAction.note);
    if (order.storeOrderId) {
      updateOrderStatus(order.storeOrderId, nextAction.nextStatus, nextAction.note);
    }
    showToast(`Đơn ${order.orderCode}: ${nextAction.label} thành công.`);
  };

  const handleVatAdvance = (nextVat: VatInvoiceStatus) => {
    advanceOrderVatStatus(order.orderCode, order.status, nextVat);
    showToast(
      nextVat === "da_gui_nhap_24h"
        ? `Đã gửi bản nháp VAT (duyệt trong 24h) cho đơn ${order.orderCode}.`
        : `Đã phát hành hóa đơn điện tử VAT cho đơn ${order.orderCode}.`
    );
  };

  const handleAmendmentDecision = (decision: "da_duyet" | "tu_choi") => {
    const defaultNote =
      decision === "da_duyet"
        ? "Gian hàng đã kiểm tra kiện hàng tại kho và chấp thuận yêu cầu."
        : "Kiện hàng đã bàn giao đơn vị vận chuyển, không thể thay đổi thông tin.";
    resolveOrderAmendment(order.orderCode, order.status, decision, amendNote.trim() || defaultNote);
    setAmendNote("");
    showToast(
      decision === "da_duyet"
        ? `Đã duyệt yêu cầu xử lý của đơn ${order.orderCode}.`
        : `Đã từ chối yêu cầu của đơn ${order.orderCode}.`
    );
  };

  const handleExchangeStep = (nextStep: TwoWayCourierStatus) => {
    advanceOrderTwoWayExchange(order.orderCode, order.status, nextStep);
    showToast(`Đã cập nhật điều phối đổi size 2 chiều đơn ${order.orderCode}.`);
  };

  const handleComplaintAction = (nextStatus: "da_phan_hoi" | "da_xu_ly" | "chuyen_san") => {
    const text =
      complaintDraft.trim() ||
      (nextStatus === "chuyen_san"
        ? "Gian hàng đề nghị bộ phận trọng tài NexusMart hỗ trợ xác minh cấp 2."
        : "Xưởng đã tiếp nhận phản ánh và cam kết hỗ trợ đổi sản phẩm mới miễn phí.");
    replyOrderTier1Complaint(order.orderCode, text, nextStatus);
    setComplaintDraft("");
    showToast(
      nextStatus === "da_xu_ly"
        ? `Đã đóng khiếu nại cấp 1 của đơn ${order.orderCode}.`
        : nextStatus === "chuyen_san"
          ? `Đã chuyển khiếu nại đơn ${order.orderCode} lên trọng tài sàn cấp 2.`
          : `Đã gửi phản hồi khiếu nại cấp 1 cho người mua đúng hạn SLA.`
    );
  };

  const exchangeCurrentIdx = ex?.twoWayExchange
    ? EXCHANGE_STEPS.findIndex((s) => s.key === ex.twoWayExchange?.courierStatus)
    : -1;

  return (
    <div className={common.page}>
      <div className={styles.backRow}>
        <Link href="/seller/don-hang" className={styles.backLink}>
          Quay lại danh sách đơn hàng
        </Link>
      </div>

      {/* 88px Object Header Card */}
      <header className={styles.objectHeader}>
        <div className={styles.objectIdentity}>
          <img
            src={coverImage}
            alt={order.items[0]?.productName ?? order.orderCode}
            className={styles.objectCover}
            width={50}
            height={62}
          />
          <div className={styles.objectText}>
            <div className={styles.objectTitleRow}>
              <h1 className={styles.objectTitle}>{order.orderCode}</h1>
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
              <button
                type="button"
                className={styles.iconBtn28}
                onClick={handlePrintSlip}
                title="Mở phiếu đóng gói"
                aria-label="Mở phiếu đóng gói"
              >
                <IconArrowUpRight size={14} />
              </button>
            </div>
            <p className={styles.objectMeta}>
              Đặt lúc {formatDateTimeVi(order.createdAt)} · {order.wardName}, {order.provinceName}
            </p>
          </div>
        </div>

        <div className={styles.objectActions}>
          <Link href="/seller/chat" className={`${styles.actionBtn36} ${styles.actionOutline}`}>
            <IconChat size={14} />
            <span>Nhắn người mua</span>
          </Link>
          {nextAction && (
            <button
              type="button"
              className={`${styles.actionBtn36} ${styles.actionInk}`}
              onClick={handleAdvance}
            >
              <IconCheck size={14} />
              <span>{nextAction.label}</span>
            </button>
          )}
        </div>
      </header>

      {/* Underline tabs on full-width 1px rule */}
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
          aria-selected={tab === "ops"}
          className={`${common.underlineTab} ${tab === "ops" ? common.underlineTabActive : ""}`}
          onClick={() => setTab("ops")}
        >
          VAT, Hủy/Đổi & Khiếu nại ({formatInt(activeOpsCount)})
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
          Lịch sử trạng thái ({formatInt(order.timeline.length)})
        </button>
      </div>

      {/* 6-column CSS grid */}
      <div className={styles.sixGrid}>
        {/* Row 1: 3 KPI tiles (2 columns each) */}
        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Giá trị đơn hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(order.subtotal)}</span>
          <span className={styles.kpiTileSub}>
            {formatInt(order.quantity)} sản phẩm · {formatInt(order.items.length)} mã SKU
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Phí sàn & chiết khấu</span>
          <span className={styles.kpiTileVal}>{formatVnd(totalFeesAndVoucher)}</span>
          <span className={styles.kpiTileSub}>
            Voucher {formatVnd(order.shopVoucherDiscount)} · Phí sàn {formatVnd(order.platformFee)}
          </span>
        </div>

        <div className={`${styles.kpiTile} ${styles.span2}`}>
          <span className={styles.kpiTileLabel}>Thực nhận gian hàng</span>
          <span className={styles.kpiTileVal}>{formatVnd(order.netReceivable)}</span>
          <span className={styles.kpiTileSub}>
            {order.isPaid ? "Đã thanh toán trước" : "Thu hộ tiền mặt COD"} ·{" "}
            {PAYMENT_METHOD_LABELS[order.paymentMethod]}
          </span>
        </div>

        {/* Tab 1: Overview */}
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
                <span className={common.cardMeta}>Khớp tồn kho theo từng màu và kích cỡ</span>
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
                    {order.items.map((item) => (
                      <tr key={item.skuCode}>
                        <td>
                          <div className={styles.itemCell}>
                            <img
                              src={item.image}
                              alt={item.productName}
                              className={styles.itemThumb}
                              width={40}
                              height={50}
                            />
                            <div>
                              <Link
                                href={`/seller/san-pham/${item.productId}`}
                                className={common.cellLink}
                              >
                                {item.productName}
                              </Link>
                              <span className={common.cellSub}>
                                {item.colorName} / Size {item.size} · {item.skuCode}
                              </span>
                            </div>
                          </div>
                        </td>
                        <td className={common.numCol}>{formatVnd(item.unitPrice)}</td>
                        <td className={common.numCol}>{formatInt(item.quantity)}</td>
                        <td className={common.numCol}>
                          <strong>{formatVnd(item.lineTotal)}</strong>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {/* Delivery note, gift card message & gift bag well */}
              <div className={styles.giftBox} aria-label="Ghi chú giao hàng, thiệp và túi quà">
                <div className={styles.giftRow}>
                  <span className={styles.kvKey}>Ghi chú giao hàng</span>
                  <span className={styles.kvVal}>
                    {ex?.deliveryNote ?? order.note ?? "Không có ghi chú đặc biệt"}
                  </span>
                </div>
                <div className={styles.giftRow}>
                  <span className={styles.kvKey}>Túi quà gian hàng</span>
                  <span className={styles.kvVal}>
                    {ex?.giftBagRequested
                      ? "Có yêu cầu kèm túi quà giấy NexusMart"
                      : "Đóng gói tiêu chuẩn"}
                  </span>
                </div>
                {ex?.giftCardMessage && (
                  <div>
                    <span className={styles.kvKey}>Lời nhắn in trên thiệp gửi kèm</span>
                    <p className={styles.giftQuote}>{ex.giftCardMessage}</p>
                  </div>
                )}
              </div>

              <div className={styles.reconBlock}>
                <div className={styles.reconRow}>
                  <span>Tổng tiền hàng</span>
                  <span>{formatVnd(order.subtotal)}</span>
                </div>
                <div className={styles.reconRow}>
                  <span>Voucher gian hàng tài trợ</span>
                  <span>
                    {order.shopVoucherDiscount > 0
                      ? `\u2212${formatVnd(order.shopVoucherDiscount)}`
                      : "0₫"}
                  </span>
                </div>
                <div className={styles.reconRow}>
                  <span>
                    Phí dịch vụ sàn ({(shop.commissionRate * 100).toFixed(1).replace(".", ",")}%)
                  </span>
                  <span>{`\u2212${formatVnd(order.platformFee)}`}</span>
                </div>
                <div className={`${styles.reconRow} ${styles.reconRowTotal}`}>
                  <span>Thực nhận về sổ cái gian hàng</span>
                  <span>{formatVnd(order.netReceivable)}</span>
                </div>
              </div>
            </section>

            <section
              className={`${styles.detailCard} ${styles.span2}`}
              aria-labelledby="buyer-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="buyer-heading" className={common.cardTitle}>
                  Người nhận & yêu cầu sau mua
                </h2>
              </div>

              <div className={styles.kvList}>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Người mua</span>
                  <span className={styles.kvVal}>{order.buyerName}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Điện thoại</span>
                  <span className={styles.kvVal}>{order.buyerPhone}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Địa chỉ giao hàng</span>
                  <span className={styles.kvVal}>{order.shippingAddress}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Hạn bàn giao</span>
                  <span className={styles.kvVal}>{formatDateTimeVi(order.slaDeadline)}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Mã vận đơn</span>
                  <span className={styles.kvVal}>{order.trackingCode ?? EMPTY_DASH}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Hóa đơn VAT</span>
                  <span className={styles.kvVal}>
                    {ex?.vatRequest
                      ? VAT_STATUS_LABEL[ex.vatRequest.status].label
                      : "Không yêu cầu"}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Hủy muộn / Đổi ĐC</span>
                  <span className={styles.kvVal}>
                    {ex?.amendment
                      ? ex.amendment.status === "cho_duyet"
                        ? "Đang chờ duyệt"
                        : ex.amendment.status === "da_duyet"
                          ? "Đã duyệt"
                          : "Đã từ chối"
                      : EMPTY_DASH}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Đổi size 2 chiều</span>
                  <span className={styles.kvVal}>
                    {ex?.twoWayExchange ? "Có yêu cầu (45 ngày)" : EMPTY_DASH}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Khiếu nại cấp 1</span>
                  <span className={styles.kvVal}>
                    {ex?.tier1Complaint ? ex.tier1Complaint.ticketCode : EMPTY_DASH}
                  </span>
                </div>
              </div>

              <div className={styles.opsActionBar}>
                <button
                  type="button"
                  className={`${styles.actionBtn36} ${styles.actionOutline}`}
                  onClick={() => setTab("ops")}
                >
                  Mở khu xử lý VAT, Hủy/Đổi & Khiếu nại
                </button>
              </div>
            </section>
          </>
        )}

        {/* Tab 2: VAT, Late Cancel/Address, 2-Way Exchange & Tier-1 Complaints */}
        {tab === "ops" && (
          <>
            {ex?.vatRequest && (
              <section
                className={`${styles.detailCard} ${activeOpsCount === 1 ? styles.span6 : styles.span3}`}
                aria-labelledby="vat-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="vat-heading" className={common.cardTitle}>
                    Xuất hóa đơn điện tử VAT
                  </h2>
                  <span className={pillCls(VAT_STATUS_LABEL[ex.vatRequest.status].tone)}>
                    {VAT_STATUS_LABEL[ex.vatRequest.status].label}
                  </span>
                </div>

                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Đơn vị mua hàng</span>
                    <span className={styles.kvVal}>{ex.vatRequest.companyName}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Mã số thuế (MST)</span>
                    <span className={styles.kvVal}>{ex.vatRequest.taxCode}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Email nhận HĐĐT</span>
                    <span className={styles.kvVal}>{ex.vatRequest.email}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Địa chỉ doanh nghiệp</span>
                    <span className={styles.kvVal}>{ex.vatRequest.companyAddress}</span>
                  </div>
                  {ex.vatRequest.draftDeadline24h && ex.vatRequest.status !== "da_phat_hanh" && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Hạn duyệt bản nháp (24h)</span>
                      <span className={styles.kvVal}>
                        {formatDateTimeVi(ex.vatRequest.draftDeadline24h)}
                      </span>
                    </div>
                  )}
                  {ex.vatRequest.issuedInvoiceNo && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Số HĐĐT đã phát hành</span>
                      <span className={styles.kvVal}>{ex.vatRequest.issuedInvoiceNo}</span>
                    </div>
                  )}
                </div>

                <div className={styles.opsActionBar}>
                  {ex.vatRequest.status === "cho_duyet_nhap" && (
                    <button
                      type="button"
                      className={`${styles.actionBtn36} ${styles.actionInk}`}
                      onClick={() => handleVatAdvance("da_gui_nhap_24h")}
                    >
                      Gửi bản nháp VAT (duyệt 24h)
                    </button>
                  )}
                  {ex.vatRequest.status === "da_gui_nhap_24h" && (
                    <button
                      type="button"
                      className={`${styles.actionBtn36} ${styles.actionInk}`}
                      onClick={() => handleVatAdvance("da_phat_hanh")}
                    >
                      Xác nhận & phát hành HĐĐT
                    </button>
                  )}
                  {ex.vatRequest.status === "da_phat_hanh" && (
                    <span className={common.cardMeta}>
                      Đã gửi file PDF & XML về {ex.vatRequest.email}
                    </span>
                  )}
                </div>
              </section>
            )}

            {ex?.amendment && (
              <section
                className={`${styles.detailCard} ${activeOpsCount === 1 ? styles.span6 : styles.span3}`}
                aria-labelledby="amend-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="amend-heading" className={common.cardTitle}>
                    Duyệt yêu cầu hủy muộn / Đổi địa chỉ
                  </h2>
                  <span
                    className={pillCls(
                      ex.amendment.status === "da_duyet"
                        ? "pos"
                        : ex.amendment.status === "tu_choi"
                          ? "neg"
                          : "warn"
                    )}
                  >
                    {ex.amendment.status === "cho_duyet"
                      ? "Chờ duyệt"
                      : ex.amendment.status === "da_duyet"
                        ? "Đã duyệt"
                        : "Đã từ chối"}
                  </span>
                </div>

                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Loại yêu cầu</span>
                    <span className={styles.kvVal}>
                      {ex.amendment.kind === "huy_muon"
                        ? "Yêu cầu hủy muộn (khi đang đóng gói)"
                        : "Yêu cầu đổi địa chỉ giao hàng"}
                    </span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Thời điểm gửi</span>
                    <span className={styles.kvVal}>
                      {formatDateTimeVi(ex.amendment.requestedAt)}
                    </span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Lý do người mua</span>
                    <span className={styles.kvVal}>{ex.amendment.reason}</span>
                  </div>
                  {ex.amendment.proposedAddress && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Địa chỉ đề xuất mới</span>
                      <span className={styles.kvVal}>{ex.amendment.proposedAddress}</span>
                    </div>
                  )}
                  {ex.amendment.resolutionNote && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Kết quả phản hồi xưởng</span>
                      <span className={styles.kvVal}>{ex.amendment.resolutionNote}</span>
                    </div>
                  )}
                </div>

                {ex.amendment.status === "cho_duyet" && (
                  <div className={styles.opsReplyBox}>
                    <input
                      type="text"
                      className={common.textInput}
                      value={amendNote}
                      onChange={(e) => setAmendNote(e.target.value)}
                      placeholder="Ghi chú duyệt hoặc lý do từ chối gửi tới người mua..."
                      aria-label="Ghi chú xử lý yêu cầu hủy muộn hoặc đổi địa chỉ"
                    />
                    <div className={styles.opsActionBar}>
                      <button
                        type="button"
                        className={`${styles.actionBtn36} ${styles.actionOutline}`}
                        onClick={() => handleAmendmentDecision("tu_choi")}
                      >
                        Từ chối
                      </button>
                      <button
                        type="button"
                        className={`${styles.actionBtn36} ${styles.actionInk}`}
                        onClick={() => handleAmendmentDecision("da_duyet")}
                      >
                        {ex.amendment.kind === "huy_muon"
                          ? "Duyệt hủy & hoàn tồn SKU"
                          : "Duyệt địa chỉ mới"}
                      </button>
                    </div>
                  </div>
                )}
              </section>
            )}

            {ex?.twoWayExchange && (
              <section
                className={`${styles.detailCard} ${activeOpsCount === 1 || activeOpsCount === 3 ? styles.span6 : styles.span3}`}
                aria-labelledby="exchange-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="exchange-heading" className={common.cardTitle}>
                    Đổi size 2 chiều tận nơi (45 ngày)
                  </h2>
                  <span
                    className={pillCls(
                      ex.twoWayExchange.courierStatus === "hoan_tat" ? "pos" : "warn"
                    )}
                  >
                    {EXCHANGE_STEPS.find((s) => s.key === ex.twoWayExchange?.courierStatus)?.label}
                  </span>
                </div>

                <div className={styles.stepTrack}>
                  {EXCHANGE_STEPS.map((st, idx) => (
                    <div
                      key={st.key}
                      className={`${styles.stepCell} ${idx <= exchangeCurrentIdx ? styles.stepCellActive : ""}`}
                    >
                      {st.label}
                    </div>
                  ))}
                </div>

                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Biến thể thu hồi</span>
                    <span className={styles.kvVal}>{ex.twoWayExchange.fromVariant}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Biến thể giao đổi mới</span>
                    <span className={styles.kvVal}>{ex.twoWayExchange.toVariant}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Khung hẹn bưu tá 2 chiều</span>
                    <span className={styles.kvVal}>{ex.twoWayExchange.pickupWindow}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Vận đơn 2 chiều</span>
                    <span className={styles.kvVal}>
                      {ex.twoWayExchange.exchangeWaybill ?? "Chưa khởi tạo"}
                    </span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Cam kết áp dụng</span>
                    <span className={styles.kvVal}>{ex.twoWayExchange.policyNote}</span>
                  </div>
                </div>

                <div className={styles.opsActionBar}>
                  {ex.twoWayExchange.courierStatus === "cho_dieu_phoi" && (
                    <button
                      type="button"
                      className={`${styles.actionBtn36} ${styles.actionInk}`}
                      onClick={() => handleExchangeStep("da_dat_buu_ta")}
                    >
                      Điều phối bưu tá 2 chiều
                    </button>
                  )}
                  {ex.twoWayExchange.courierStatus === "da_dat_buu_ta" && (
                    <button
                      type="button"
                      className={`${styles.actionBtn36} ${styles.actionInk}`}
                      onClick={() => handleExchangeStep("dang_doi_2_chieu")}
                    >
                      Xác nhận bưu tá đã lấy hàng mới
                    </button>
                  )}
                  {ex.twoWayExchange.courierStatus === "dang_doi_2_chieu" && (
                    <button
                      type="button"
                      className={`${styles.actionBtn36} ${styles.actionInk}`}
                      onClick={() => handleExchangeStep("hoan_tat")}
                    >
                      Chốt hoàn tất đổi 2 chiều
                    </button>
                  )}
                  {ex.twoWayExchange.courierStatus === "hoan_tat" && (
                    <span className={common.cardMeta}>
                      Đã hoàn tất thu hồi và bàn giao size mới cho khách
                    </span>
                  )}
                </div>
              </section>
            )}

            {ex?.tier1Complaint && (
              <section
                className={`${styles.detailCard} ${activeOpsCount === 1 || activeOpsCount === 3 ? styles.span6 : styles.span3}`}
                aria-labelledby="complaint-heading"
              >
                <div className={common.cardHeader}>
                  <h2 id="complaint-heading" className={common.cardTitle}>
                    Khiếu nại cấp 1 của gian hàng
                  </h2>
                  <span
                    className={pillCls(
                      ex.tier1Complaint.status === "da_xu_ly"
                        ? "pos"
                        : ex.tier1Complaint.status === "chuyen_san"
                          ? "neg"
                          : "warn"
                    )}
                  >
                    {ex.tier1Complaint.ticketCode}
                  </span>
                </div>

                <div className={styles.slaBanner}>
                  <span>Mốc SLA phản hồi gian hàng</span>
                  <span>{ex.tier1Complaint.slaRemainingLabel}</span>
                </div>

                <div className={styles.kvList}>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Nhóm vấn đề</span>
                    <span className={styles.kvVal}>{ex.tier1Complaint.category}</span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Hạn SLA 24h</span>
                    <span className={styles.kvVal}>
                      {formatDateTimeVi(ex.tier1Complaint.slaDeadline)}
                    </span>
                  </div>
                  <div className={styles.kvRow}>
                    <span className={styles.kvKey}>Nội dung khách gửi</span>
                    <span className={styles.kvVal}>{ex.tier1Complaint.buyerMessage}</span>
                  </div>
                  {ex.tier1Complaint.sellerResponse && (
                    <div className={styles.kvRow}>
                      <span className={styles.kvKey}>Phản hồi của xưởng</span>
                      <span className={styles.kvVal}>{ex.tier1Complaint.sellerResponse}</span>
                    </div>
                  )}
                </div>

                {ex.tier1Complaint.status !== "da_xu_ly" &&
                  ex.tier1Complaint.status !== "chuyen_san" && (
                    <div className={styles.opsReplyBox}>
                      <textarea
                        className={common.textArea}
                        value={complaintDraft}
                        onChange={(e) => setComplaintDraft(e.target.value)}
                        placeholder="Nhập phương án xử lý (đổi sản phẩm mới, hoàn Xu hoặc đối soát) để gửi người mua..."
                        aria-label="Nội dung phản hồi khiếu nại cấp 1"
                      />
                      <div className={styles.opsActionBar}>
                        <button
                          type="button"
                          className={`${styles.actionBtn36} ${styles.actionOutline}`}
                          onClick={() => handleComplaintAction("chuyen_san")}
                        >
                          Chuyển cấp 2 (Admin)
                        </button>
                        <button
                          type="button"
                          className={`${styles.actionBtn36} ${styles.actionOutline}`}
                          onClick={() => handleComplaintAction("da_phan_hoi")}
                        >
                          Gửi phản hồi
                        </button>
                        <button
                          type="button"
                          className={`${styles.actionBtn36} ${styles.actionInk}`}
                          onClick={() => handleComplaintAction("da_xu_ly")}
                        >
                          Chốt đã xử lý
                        </button>
                      </div>
                    </div>
                  )}
              </section>
            )}

            <section
              className={`${styles.detailCard} ${styles.span6}`}
              aria-labelledby="ops-summary-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="ops-summary-heading" className={common.cardTitle}>
                  Tình trạng nghiệp vụ sau mua của đơn {order.orderCode}
                </h2>
                <span className={common.cardMeta}>
                  Quy chuẩn vận hành NexusMart (đổi trả 45 ngày)
                </span>
              </div>
              <div className={styles.kvList}>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Xuất hóa đơn VAT</span>
                  <span className={styles.kvVal}>
                    {ex?.vatRequest
                      ? `${VAT_STATUS_LABEL[ex.vatRequest.status].label} · MST ${ex.vatRequest.taxCode}`
                      : "Chưa phát sinh (hỗ trợ duyệt nháp 24h -> HĐĐT)"}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Yêu cầu hủy muộn / Đổi địa chỉ</span>
                  <span className={styles.kvVal}>
                    {ex?.amendment
                      ? `${ex.amendment.kind === "huy_muon" ? "Hủy muộn" : "Đổi địa chỉ"} (${ex.amendment.status === "cho_duyet" ? "Chờ duyệt" : ex.amendment.status === "da_duyet" ? "Đã duyệt" : "Đã từ chối"})`
                      : "Không có yêu cầu thay đổi"}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Đổi size 2 chiều tận nơi</span>
                  <span className={styles.kvVal}>
                    {ex?.twoWayExchange
                      ? `${EXCHANGE_STEPS.find((s) => s.key === ex.twoWayExchange?.courierStatus)?.label} · ${ex.twoWayExchange.toVariant}`
                      : "Chưa phát sinh (miễn phí đổi 2 chiều trong 45 ngày)"}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Khiếu nại cấp 1 gian hàng</span>
                  <span className={styles.kvVal}>
                    {ex?.tier1Complaint
                      ? `${ex.tier1Complaint.ticketCode} · ${ex.tier1Complaint.slaRemainingLabel}`
                      : "Không có khiếu nại mở (SLA phản hồi 24h)"}
                  </span>
                </div>
              </div>
            </section>
          </>
        )}

        {/* Tab 3: Shipping & Documents */}
        {tab === "shipping" && (
          <>
            <section
              className={`${styles.detailCard} ${styles.span4}`}
              aria-labelledby="ship-doc-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="ship-doc-heading" className={common.cardTitle}>
                  Thông tin vận chuyển & phiếu đóng gói
                </h2>
                <button
                  type="button"
                  className={`${styles.actionBtn36} ${styles.actionOutline}`}
                  onClick={handlePrintSlip}
                >
                  <IconTruck size={14} />
                  <span>In phiếu vận đơn</span>
                </button>
              </div>
              <div className={styles.kvList}>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Đơn vị vận chuyển</span>
                  <span className={styles.kvVal}>NexusLogistics Tiêu chuẩn</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Mã vận đơn</span>
                  <span className={styles.kvVal}>
                    {order.trackingCode ?? "Chưa cấp (chờ xác nhận đơn)"}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Kho lấy hàng</span>
                  <span className={styles.kvVal}>{shop.warehouseAddress}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Địa chỉ giao hàng</span>
                  <span className={styles.kvVal}>{order.shippingAddress}</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Yêu cầu đóng gói quà</span>
                  <span className={styles.kvVal}>
                    {ex?.giftBagRequested ? "Kèm túi quà và thiệp in" : "Đóng gói chuẩn"}
                  </span>
                </div>
              </div>
            </section>

            <section
              className={`${styles.detailCard} ${styles.span2}`}
              aria-labelledby="settlement-heading"
            >
              <div className={common.cardHeader}>
                <h2 id="settlement-heading" className={common.cardTitle}>
                  Lịch đối soát dự kiến
                </h2>
              </div>
              <div className={styles.kvList}>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Thanh toán</span>
                  <span className={styles.kvVal}>
                    {order.isPaid ? "Đã thu trực tuyến" : "Thu hộ COD"}
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Chu kỳ giữ tiền</span>
                  <span className={styles.kvVal}>3 ngày sau giao</span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Tỉ lệ hoa hồng</span>
                  <span className={styles.kvVal}>
                    {(shop.commissionRate * 100).toFixed(1).replace(".", ",")}%
                  </span>
                </div>
                <div className={styles.kvRow}>
                  <span className={styles.kvKey}>Ghi có sổ cái</span>
                  <span className={styles.kvVal}>{formatVnd(order.netReceivable)}</span>
                </div>
              </div>
            </section>
          </>
        )}

        {/* Tab 4: History Timeline */}
        {tab === "history" && (
          <section
            className={`${styles.detailCard} ${styles.span6}`}
            aria-labelledby="timeline-heading"
          >
            <div className={common.cardHeader}>
              <h2 id="timeline-heading" className={common.cardTitle}>
                Nhật ký trạng thái đơn hàng
              </h2>
              <span className={common.cardMeta}>
                Máy trạng thái đơn hàng ghi nhận theo mốc thời gian
              </span>
            </div>
            <div className={styles.timeline}>
              {order.timeline.map((ev, idx) => {
                const evMeta = getSafeOrderStatusMeta(ev.status);
                return (
                  <div key={`${ev.timestamp}-${idx}`} className={styles.timelineRow}>
                    <span className={styles.timelineTime}>{formatDateTimeVi(ev.timestamp)}</span>
                    <div>
                      <p className={styles.timelineTitle}>{ev.title}</p>
                      <p className={styles.timelineNote}>{ev.note}</p>
                    </div>
                    <span className={pillCls(evMeta.tone)}>{evMeta.label}</span>
                  </div>
                );
              })}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}

export default function SellerOrderDetailPage() {
  return (
    <SellerSuspense>
      <SellerOrderDetailContent />
    </SellerSuspense>
  );
}
