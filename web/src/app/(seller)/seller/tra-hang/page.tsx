"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type {
  GatewayReturnRequest,
  GatewayShop,
  PaginatedResponse,
  ReturnSellerDecision,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import {
  MAX_EVIDENCE_URLS,
  buildShippingLabelHref,
  getReturnFeeBearerLabel,
  getReturnStatusInfo,
  localizeTimelineNote,
  normalizeEnumSuffix,
  parseSafeEvidenceUrl,
  validateEvidenceUrls,
} from "@/features/account/orderMeta";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import { EMPTY_DASH, parseUiState } from "@/features/seller/sellerStore";
import { SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE } from "@/features/seller/types";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type ReturnFilterKey = "all" | "PENDING" | "RETURNING" | "RECEIVED" | "ESCALATED" | "DONE";

function pillClass(tone: "neutral" | "warn" | "pos" | "neg"): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
}

function SellerReturnsInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialSelected = searchParams.get("selected");

  const { showToast } = useMockStore();
  const { refreshPendingReturns } = useSellerContext();

  const [returns, setReturns] = useState<GatewayReturnRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [filter, setFilter] = useState<ReturnFilterKey>("all");
  const [query, setQuery] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);

  // Action form state
  const [responseNote, setResponseNote] = useState("");
  const [evidenceUrls, setEvidenceUrls] = useState<string[]>([""]);
  const [disputeReason, setDisputeReason] = useState("");
  const [disputeUrls, setDisputeUrls] = useState<string[]>([""]);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const loadReturns = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<PaginatedResponse<GatewayReturnRequest>>(
        "seller/returns?page=1&page_size=100"
      );
      setReturns(res.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách yêu cầu trả hàng lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!uiState) {
      void loadReturns();
    }
  }, [uiState, loadReturns]);

  const counts = useMemo(() => {
    const c = { all: returns.length, PENDING: 0, RETURNING: 0, RECEIVED: 0, ESCALATED: 0, DONE: 0 };
    for (const r of returns) {
      const st = normalizeEnumSuffix(r.status, "RETURN_STATUS_");
      if (st === "PENDING") c.PENDING += 1;
      else if (st === "AWAITING_SHIPMENT" || st === "IN_TRANSIT") c.RETURNING += 1;
      else if (st === "RECEIVED") c.RECEIVED += 1;
      else if (st === "ESCALATED" || st === "EVIDENCE_REQUESTED") c.ESCALATED += 1;
      else c.DONE += 1;
    }
    return c;
  }, [returns]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return returns.filter((r) => {
      const st = normalizeEnumSuffix(r.status, "RETURN_STATUS_");
      if (filter === "PENDING" && st !== "PENDING") return false;
      if (filter === "RETURNING" && st !== "AWAITING_SHIPMENT" && st !== "IN_TRANSIT") return false;
      if (filter === "RECEIVED" && st !== "RECEIVED") return false;
      if (filter === "ESCALATED" && st !== "ESCALATED" && st !== "EVIDENCE_REQUESTED") return false;
      if (
        filter === "DONE" &&
        st !== "REFUNDING" &&
        st !== "REFUNDED" &&
        st !== "REJECTED" &&
        st !== "CLOSED"
      ) {
        return false;
      }
      if (!q) return true;
      return (
        r.order_code.toLowerCase().includes(q) ||
        r.reason.toLowerCase().includes(q) ||
        (r.return_tracking_code ?? "").toLowerCase().includes(q)
      );
    });
  }, [returns, filter, query]);

  const selectedReturn = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((r) => r.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  const handleSelectReturn = (r: GatewayReturnRequest) => {
    setSelectedId(r.id);
    setResponseNote("");
    setEvidenceUrls([""]);
    setDisputeReason("");
    setDisputeUrls([""]);
    setFormError(null);
  };

  const handleRespond = async (ret: GatewayReturnRequest, decision: ReturnSellerDecision) => {
    if (busy) return;
    const cleanNote = responseNote.trim();
    if (decision === "REJECT" && cleanNote.length < 4) {
      setFormError("Vui lòng nhập lý do từ chối yêu cầu trả hàng (tối thiểu 4 ký tự).");
      return;
    }
    const urlCheck = validateEvidenceUrls(evidenceUrls);
    if (!urlCheck.ok) {
      setFormError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `seller/returns/${encodeURIComponent(ret.id)}/respond`,
        {
          method: "POST",
          json: {
            decision,
            note: cleanNote,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request : item)));
      } else {
        await loadReturns();
      }
      setResponseNote("");
      setEvidenceUrls([""]);
      await refreshPendingReturns();
      const actionMsg =
        decision === "ACCEPT_REFUND"
          ? "Đã chấp nhận hoàn tiền ngay không cần trả hàng."
          : decision === "ACCEPT_RETURN"
            ? "Đã chấp nhận yêu cầu trả hàng và khởi tạo vận đơn chiều trả."
            : "Đã gửi phản hồi từ chối yêu cầu trả hàng.";
      showToast(actionMsg);
    } catch (err) {
      if (err instanceof ApiError && err.code === "SHOP_PICKUP_ADDRESS_REQUIRED") {
        setFormError(SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE);
      } else {
        setFormError(getErrorMessage(err, "Không thể gửi phản hồi lúc này."));
      }
    } finally {
      setBusy(false);
    }
  };

  const handleConfirmReceived = async (ret: GatewayReturnRequest) => {
    if (busy) return;
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `seller/returns/${encodeURIComponent(ret.id)}/confirm-received`,
        { method: "POST" }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request : item)));
      } else {
        await loadReturns();
      }
      await refreshPendingReturns();
      showToast(
        `Đã xác nhận nhận hàng trả cho đơn ${ret.order_code}. Hệ thống đang xử lý hoàn tiền.`
      );
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể xác nhận đã nhận hàng trả lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const handleDisputeGoods = async (ret: GatewayReturnRequest) => {
    if (busy) return;
    const reason = disputeReason.trim();
    if (reason.length < 4) {
      setFormError("Vui lòng mô tả rõ lý do hàng trả không đúng (tối thiểu 4 ký tự).");
      return;
    }
    const urlCheck = validateEvidenceUrls(disputeUrls);
    if (!urlCheck.ok) {
      setFormError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `seller/returns/${encodeURIComponent(ret.id)}/dispute`,
        {
          method: "POST",
          json: {
            reason,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request : item)));
      } else {
        await loadReturns();
      }
      setDisputeReason("");
      setDisputeUrls([""]);
      await refreshPendingReturns();
      showToast(
        `Đã báo hàng trả không đúng cho đơn ${ret.order_code} và chuyển quản trị viên phân xử.`
      );
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể gửi báo cáo hàng sai lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const countCards: Array<{ key: ReturnFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "PENDING", label: "Chờ phản hồi", count: counts.PENDING },
    { key: "RETURNING", label: "Đang gửi trả", count: counts.RETURNING },
    { key: "RECEIVED", label: "Chờ xác nhận nhận", count: counts.RECEIVED },
    { key: "ESCALATED", label: "Khiếu nại sàn", count: counts.ESCALATED },
    { key: "DONE", label: "Hoàn tiền / Đã đóng", count: counts.DONE },
  ];

  const selectedStatusKey = selectedReturn
    ? normalizeEnumSuffix(selectedReturn.status, "RETURN_STATUS_")
    : "";

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Trả hàng & hoàn tiền</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(counts.PENDING)} yêu cầu chờ phản hồi trong 3 ngày ·{" "}
            {formatInt(counts.RECEIVED)} kiện đã về kho chờ xác nhận
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadReturns()}>
            Làm mới
          </Button>
        </div>
      </header>

      {(uiState === "loading" || (!uiState && loading)) && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải danh sách yêu cầu trả hàng..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách trả hàng"
          message={errorMsg ?? "Hệ thống xử lý trả hàng tạm gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => void loadReturns()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có yêu cầu trả hàng nào"
          message="Khi người mua gửi yêu cầu trả hàng trong 7 ngày sau khi giao, hồ sơ sẽ hiển thị tại đây."
          actionLabel="Xem đơn hàng gian hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && !loading && !errorMsg && (
        <>
          <div className={common.countCards6} role="tablist" aria-label="Lọc trạng thái trả hàng">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                  onClick={() => setFilter(c.key)}
                >
                  <span className={common.countCardLabel}>{c.label}</span>
                  <span className={common.countCardValue}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          <div className={common.chipScroller} role="tablist" aria-label="Lọc trạng thái trả hàng">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.chipBtn} ${selected ? common.chipBtnActive : ""}`}
                  onClick={() => setFilter(c.key)}
                >
                  <span>{c.label}</span>
                  <span className={common.chipCount}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          <div className={styles.splitLayout}>
            <section className={common.card} aria-label="Danh sách yêu cầu trả hàng">
              <div className={common.toolbar} style={{ marginBottom: 14 }}>
                <div className={common.toolbarLeft}>
                  <div className={common.searchWrap}>
                    <span className={common.searchIcon}>
                      <IconSearch size={14} />
                    </span>
                    <input
                      type="search"
                      className={common.searchInput}
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                      placeholder="Tìm mã đơn, lý do, mã vận đơn trả..."
                      aria-label="Tìm yêu cầu trả hàng"
                    />
                  </div>
                </div>
                <span className={common.cardMeta}>
                  {formatInt(filtered.length)} / {formatInt(returns.length)} yêu cầu
                </span>
              </div>

              {filtered.length === 0 ? (
                <div className={common.inlineEmpty}>
                  {returns.length === 0
                    ? "Gian hàng chưa có yêu cầu trả hàng nào."
                    : "Không có yêu cầu trả hàng nào khớp bộ lọc hiện tại."}
                </div>
              ) : (
                <>
                  <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Đơn hàng & lý do</th>
                          <th className={common.numCol}>Hoàn dự kiến</th>
                          <th>Hạn phản hồi</th>
                          <th>Trạng thái</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filtered.map((ret) => {
                          const meta = getReturnStatusInfo(ret.status);
                          const isSelected = selectedReturn?.id === ret.id;
                          return (
                            <tr
                              key={ret.id}
                              className={`${styles.rowClickable} ${isSelected ? styles.rowSelected : ""}`}
                              onClick={() => handleSelectReturn(ret)}
                            >
                              <td>
                                <span className={common.cellLink}>{ret.order_code}</span>
                                <span className={`${common.cellSub} ${styles.reasonEllipsis}`}>
                                  {ret.reason}
                                </span>
                              </td>
                              <td className={common.numCol}>
                                <strong>{formatVnd(ret.refund_amount_vnd)}</strong>
                              </td>
                              <td>
                                {ret.deadline_at ? formatDateTimeVi(ret.deadline_at) : EMPTY_DASH}
                              </td>
                              <td>
                                <span className={pillClass(meta.tone)}>{meta.label}</span>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={common.mobileList}>
                    {filtered.map((ret) => {
                      const meta = getReturnStatusInfo(ret.status);
                      return (
                        <div
                          key={ret.id}
                          className={common.mobileRow}
                          role="button"
                          tabIndex={0}
                          onClick={() => handleSelectReturn(ret)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              handleSelectReturn(ret);
                            }
                          }}
                        >
                          <div className={common.mobileRowTop}>
                            <div>
                              <span className={common.cellLink}>{ret.order_code}</span>
                              <span className={common.cellSub}>{ret.reason}</span>
                            </div>
                            <strong>{formatVnd(ret.refund_amount_vnd)}</strong>
                          </div>
                          <div className={common.mobileRowMeta}>
                            <span className={pillClass(meta.tone)}>{meta.label}</span>
                            <span>
                              {ret.deadline_at
                                ? `Hạn ${formatDateTimeVi(ret.deadline_at)}`
                                : EMPTY_DASH}
                            </span>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </section>

            {/* Right detail & action panel */}
            <section className={common.card} aria-label="Chi tiết và xử lý yêu cầu trả hàng">
              {!selectedReturn ? (
                <div className={common.inlineEmpty}>
                  Chọn một yêu cầu trả hàng ở danh sách để xem bằng chứng và phản hồi.
                </div>
              ) : (
                <div className={styles.detailStack}>
                  <div className={common.cardHeader} style={{ marginBottom: 0 }}>
                    <div>
                      <h2 className={common.cardTitle}>Hồ sơ đơn {selectedReturn.order_code}</h2>
                      <Link
                        href={`/seller/don-hang/${encodeURIComponent(selectedReturn.order_id)}`}
                        className={common.cardLink}
                      >
                        Mở chi tiết đơn hàng
                      </Link>
                    </div>
                    <span className={pillClass(getReturnStatusInfo(selectedReturn.status).tone)}>
                      {getReturnStatusInfo(selectedReturn.status).label}
                    </span>
                  </div>

                  <div className={styles.infoGrid}>
                    <div className={styles.infoItem}>
                      <span className={styles.infoKey}>Số tiền hoàn dự kiến</span>
                      <span className={styles.infoVal}>
                        {formatVnd(selectedReturn.refund_amount_vnd)}
                      </span>
                    </div>
                    <div className={styles.infoItem}>
                      <span className={styles.infoKey}>Hạn xử lý kế tiếp</span>
                      <span className={styles.infoVal}>
                        {selectedReturn.deadline_at
                          ? formatDateTimeVi(selectedReturn.deadline_at)
                          : EMPTY_DASH}
                      </span>
                    </div>
                    <div className={styles.infoItem}>
                      <span className={styles.infoKey}>Vận đơn chiều trả</span>
                      <span className={styles.infoVal}>
                        {selectedReturn.return_tracking_code ? (
                          <>
                            <span>{selectedReturn.return_tracking_code}</span>
                            {(() => {
                              const labelHref = buildShippingLabelHref(
                                selectedReturn.return_tracking_code
                              );
                              if (!labelHref) return null;
                              return (
                                <>
                                  {" · "}
                                  <a
                                    href={labelHref}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className={common.cardLink}
                                    title="Mở nhãn trả hàng ở tab mới — nhấn Ctrl+P để in"
                                  >
                                    In nhãn trả hàng
                                  </a>{" "}
                                  <span className={common.cardMeta}>(Ctrl+P để in)</span>
                                </>
                              );
                            })()}
                          </>
                        ) : (
                          EMPTY_DASH
                        )}
                      </span>
                    </div>
                    <div className={styles.infoItem}>
                      <span className={styles.infoKey}>Phí trả hàng</span>
                      <span className={styles.infoVal}>
                        {selectedReturn.return_fee_vnd > 0
                          ? `${formatVnd(selectedReturn.return_fee_vnd)} · `
                          : ""}
                        {getReturnFeeBearerLabel(selectedReturn.return_fee_bearer)}
                      </span>
                    </div>
                  </div>

                  <div className={styles.well}>
                    <span className={styles.wellHeading}>Nội dung từ người mua</span>
                    <p className={styles.wellText}>{selectedReturn.reason}</p>
                    {(() => {
                      const buyerLinks = (selectedReturn.evidence_urls ?? [])
                        .map((u) => parseSafeEvidenceUrl(u))
                        .filter((x): x is NonNullable<typeof x> => x !== null);
                      if (buyerLinks.length === 0) return null;
                      return (
                        <div className={styles.evidenceList}>
                          <span className={styles.infoKey}>
                            Liên kết bằng chứng người mua (mở trang ngoài)
                          </span>
                          {buyerLinks.map((ev, idx) => (
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
                    {selectedReturn.seller_response && (
                      <p className={styles.wellText}>
                        <strong>Phản hồi gian hàng:</strong> {selectedReturn.seller_response}
                      </p>
                    )}
                    {(() => {
                      const sellerLinks = (selectedReturn.seller_evidence_urls ?? [])
                        .map((u) => parseSafeEvidenceUrl(u))
                        .filter((x): x is NonNullable<typeof x> => x !== null);
                      if (sellerLinks.length === 0) return null;
                      return (
                        <div className={styles.evidenceList}>
                          <span className={styles.infoKey}>
                            Chứng từ gian hàng (mở trang ngoài)
                          </span>
                          {sellerLinks.map((ev, idx) => (
                            <a
                              key={`${ev.href}-${idx}`}
                              href={ev.href}
                              target="_blank"
                              rel="noopener noreferrer nofollow"
                              className={styles.evidenceLink}
                            >
                              Chứng từ #{idx + 1} ({ev.hostname})
                            </a>
                          ))}
                        </div>
                      );
                    })()}
                    {selectedReturn.dispute_reason && (
                      <p className={styles.wellText}>
                        <strong>Nội dung tranh chấp:</strong> {selectedReturn.dispute_reason}
                      </p>
                    )}
                    {selectedReturn.admin_note && (
                      <p className={styles.wellText}>
                        <strong>Kết luận quản trị viên:</strong> {selectedReturn.admin_note}
                      </p>
                    )}
                  </div>

                  {/* PENDING: Seller can ACCEPT_REFUND, ACCEPT_RETURN, or REJECT */}
                  {selectedStatusKey === "PENDING" && (
                    <div className={styles.well}>
                      <span className={styles.wellHeading}>Phản hồi yêu cầu trả hàng</span>
                      <div className={common.field}>
                        <label htmlFor="seller-ret-note" className={common.fieldLabel}>
                          Ghi chú phản hồi (bắt buộc nếu từ chối)
                        </label>
                        <textarea
                          id="seller-ret-note"
                          className={common.textArea}
                          value={responseNote}
                          onChange={(e) => setResponseNote(e.target.value)}
                          placeholder="Nhập hướng dẫn gửi trả hoặc lý do từ chối cụ thể..."
                          disabled={busy}
                        />
                      </div>

                      <div className={common.field}>
                        <span className={common.fieldLabel}>
                          Liên kết chứng từ gian hàng (tùy chọn, tối đa {MAX_EVIDENCE_URLS})
                        </span>
                        {evidenceUrls.map((url, idx) => (
                          <div key={`sel-ev-${idx}`} className={styles.urlRow}>
                            <input
                              type="url"
                              className={common.textInput}
                              value={url}
                              onChange={(e) => {
                                const next = [...evidenceUrls];
                                next[idx] = e.target.value;
                                setEvidenceUrls(next);
                              }}
                              placeholder="https://... (ảnh/video đóng gói)"
                              aria-label={`Liên kết chứng từ gian hàng ${idx + 1}`}
                              disabled={busy}
                            />
                            {evidenceUrls.length > 1 && (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={busy}
                                onClick={() =>
                                  setEvidenceUrls(evidenceUrls.filter((_, i) => i !== idx))
                                }
                              >
                                Xóa
                              </Button>
                            )}
                          </div>
                        ))}
                        {evidenceUrls.length < MAX_EVIDENCE_URLS && (
                          <div>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={busy}
                              onClick={() => setEvidenceUrls([...evidenceUrls, ""])}
                            >
                              Thêm liên kết
                            </Button>
                          </div>
                        )}
                      </div>

                      {formError && (
                        <p className={common.fieldError} role="alert">
                          {formError}
                        </p>
                      )}

                      <div className={styles.actionRow}>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={busy}
                          onClick={() => void handleRespond(selectedReturn, "REJECT")}
                        >
                          Từ chối
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={busy}
                          onClick={() => void handleRespond(selectedReturn, "ACCEPT_REFUND")}
                        >
                          Chấp nhận hoàn tiền ngay
                        </Button>
                        <Button
                          type="button"
                          variant="ink"
                          size="sm"
                          shape="app"
                          disabled={busy}
                          onClick={() => void handleRespond(selectedReturn, "ACCEPT_RETURN")}
                        >
                          {busy ? "Đang xử lý..." : "Yêu cầu trả hàng về"}
                        </Button>
                      </div>
                    </div>
                  )}

                  {/* RECEIVED: Seller can confirm received goods or dispute wrong goods */}
                  {selectedStatusKey === "RECEIVED" && (
                    <div className={styles.well}>
                      <span className={styles.wellHeading}>
                        Kiểm tra hàng trả đã về kho gian hàng
                      </span>
                      <p className={styles.wellText}>
                        Xác nhận nếu hàng trả đúng sản phẩm và tình trạng để hoàn tiền cho người mua
                        (tồn kho sẽ tự động cộng lại). Nếu hàng trả sai/hư hỏng ngoài mô tả, báo
                        hàng sai để quản trị viên phân xử.
                      </p>

                      <div className={common.field}>
                        <label htmlFor="seller-dispute-reason" className={common.fieldLabel}>
                          Lý do báo hàng trả không đúng (chỉ cần nhập khi báo hàng sai)
                        </label>
                        <textarea
                          id="seller-dispute-reason"
                          className={common.textArea}
                          value={disputeReason}
                          onChange={(e) => setDisputeReason(e.target.value)}
                          placeholder="Mô tả sai khác giữa kiện hàng nhận về và đơn hàng gốc..."
                          disabled={busy}
                        />
                      </div>

                      <div className={common.field}>
                        <span className={common.fieldLabel}>
                          Liên kết bằng chứng khui kiện trả (tùy chọn, tối đa {MAX_EVIDENCE_URLS})
                        </span>
                        {disputeUrls.map((url, idx) => (
                          <div key={`sel-disp-${idx}`} className={styles.urlRow}>
                            <input
                              type="url"
                              className={common.textInput}
                              value={url}
                              onChange={(e) => {
                                const next = [...disputeUrls];
                                next[idx] = e.target.value;
                                setDisputeUrls(next);
                              }}
                              placeholder="https://... (ảnh/video mở kiện trả)"
                              aria-label={`Liên kết bằng chứng báo hàng sai ${idx + 1}`}
                              disabled={busy}
                            />
                            {disputeUrls.length > 1 && (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={busy}
                                onClick={() =>
                                  setDisputeUrls(disputeUrls.filter((_, i) => i !== idx))
                                }
                              >
                                Xóa
                              </Button>
                            )}
                          </div>
                        ))}
                        {disputeUrls.length < MAX_EVIDENCE_URLS && (
                          <div>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={busy}
                              onClick={() => setDisputeUrls([...disputeUrls, ""])}
                            >
                              Thêm liên kết
                            </Button>
                          </div>
                        )}
                      </div>

                      {formError && (
                        <p className={common.fieldError} role="alert">
                          {formError}
                        </p>
                      )}

                      <div className={styles.actionRow}>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={busy}
                          onClick={() => void handleDisputeGoods(selectedReturn)}
                        >
                          Báo hàng trả không đúng
                        </Button>
                        <Button
                          type="button"
                          variant="ink"
                          size="sm"
                          shape="app"
                          disabled={busy}
                          onClick={() => void handleConfirmReceived(selectedReturn)}
                        >
                          {busy ? "Đang xác nhận..." : "Xác nhận đã nhận hàng"}
                        </Button>
                      </div>
                    </div>
                  )}

                  {selectedReturn.events && selectedReturn.events.length > 0 && (
                    <div>
                      <h3 className={styles.wellHeading} style={{ marginBottom: 6 }}>
                        Lịch sử xử lý ({formatInt(selectedReturn.events.length)})
                      </h3>
                      <div className={styles.timelineList}>
                        {selectedReturn.events.map((ev) => (
                          <div key={ev.id} className={styles.timelineItem}>
                            <div className={styles.timelineHead}>
                              <span className={styles.timelineTitle}>
                                {getReturnStatusInfo(ev.to_status).label}
                              </span>
                              <span className={styles.timelineTime}>
                                {ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH}
                              </span>
                            </div>
                            {ev.note && (
                              <p className={styles.timelineNote}>{localizeTimelineNote(ev.note)}</p>
                            )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </section>
          </div>
        </>
      )}
    </div>
  );
}

function SellerReturnsContent() {
  return (
    <SellerShopGate pageTitle="Trả hàng & hoàn tiền" allowSuspended>
      {(shop) => <SellerReturnsInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerReturnsPage() {
  return (
    <SellerSuspense>
      <SellerReturnsContent />
    </SellerSuspense>
  );
}
